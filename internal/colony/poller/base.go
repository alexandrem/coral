// Package poller provides common polling infrastructure for colony pollers.
// It eliminates duplicated code across multiple poller implementations.
package poller

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/colony/registry"
)

// Poller defines the interface for specific poller implementations.
type Poller interface {
	// PollOnce performs a single polling cycle.
	// Implementations should query agents and process data.
	PollOnce(ctx context.Context) error

	// RunCleanup performs cleanup operations.
	// Implementations should remove old data based on retention policies.
	RunCleanup(ctx context.Context) error
}

// Config contains configuration for a poller.
type Config struct {
	// Name is the poller name for logging (e.g., "system_metrics_poller").
	Name string

	// PollInterval is how often to poll agents.
	PollInterval time.Duration

	// CleanupInterval is how often to run cleanup (default: 1 hour).
	CleanupInterval time.Duration

	// Logger is the logger to use for this poller.
	Logger zerolog.Logger
}

// BasePoller provides common polling infrastructure.
// It manages the polling loop, cleanup loop, and lifecycle.
type BasePoller struct {
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	running         bool
	mu              sync.Mutex
	pollInterval    time.Duration
	cleanupInterval time.Duration
	logger          zerolog.Logger
	name            string
}

// NewBasePoller creates a new base poller.
// The parent context is used for lifecycle management.
func NewBasePoller(parentCtx context.Context, config Config) *BasePoller {
	ctx, cancel := context.WithCancel(parentCtx)

	// Default cleanup interval to 1 hour if not specified.
	cleanupInterval := config.CleanupInterval
	if cleanupInterval == 0 {
		cleanupInterval = 1 * time.Hour
	}

	return &BasePoller{
		ctx:             ctx,
		cancel:          cancel,
		pollInterval:    config.PollInterval,
		cleanupInterval: cleanupInterval,
		logger:          config.Logger,
		name:            config.Name,
	}
}

// Start begins the polling loop.
// It starts both the poll loop and cleanup loop in separate goroutines.
func (b *BasePoller) Start(poller Poller) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.running {
		return nil
	}

	b.logger.Info().
		Dur("poll_interval", b.pollInterval).
		Dur("cleanup_interval", b.cleanupInterval).
		Msg("Starting poller")

	b.wg.Add(1)
	go b.pollLoop(poller)

	b.running = true
	return nil
}

// Stop stops the polling loop and waits for all goroutines to finish.
func (b *BasePoller) Stop() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.running {
		return nil
	}

	b.logger.Info().Msg("Stopping poller")

	b.cancel()
	b.wg.Wait()

	b.running = false
	return nil
}

// IsRunning returns whether the poller is currently running.
func (b *BasePoller) IsRunning() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.running
}

// pollLoop is the main polling loop.
// It runs pollOnce immediately, then on every tick.
func (b *BasePoller) pollLoop(poller Poller) {
	defer b.wg.Done()

	ticker := time.NewTicker(b.pollInterval)
	defer ticker.Stop()

	// Start cleanup loop in a separate goroutine.
	b.wg.Add(1)
	go b.cleanupLoop(poller)

	// Run an initial poll immediately.
	b.safePollOnce(poller)

	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			b.safePollOnce(poller)
		}
	}
}

// safePollOnce calls PollOnce with panic recovery to prevent a single
// bad poll cycle from crashing the entire colony process.
func (b *BasePoller) safePollOnce(poller Poller) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Error().
				Str("panic", fmt.Sprintf("%v", r)).
				Msg("Panic recovered in poll cycle")
		}
	}()

	if err := poller.PollOnce(b.ctx); err != nil {
		b.logger.Error().Err(err).Msg("Poll failed")
	}
}

// AgentVisitor is called for each healthy agent.
// Returning an error logs a warning but continues iteration.
type AgentVisitor func(agent *registry.Entry) error

// ForEachHealthyAgent iterates registered agents, skipping unhealthy ones,
// and calls visitor for each. Returns (successCount, errorCount).
func ForEachHealthyAgent(
	reg *registry.Registry,
	logger zerolog.Logger,
	visitor AgentVisitor,
) (success, errors int) {
	agents := reg.ListAll()
	if len(agents) == 0 {
		logger.Debug().Msg("No agents registered, skipping poll")
		return 0, 0
	}
	now := time.Now()
	for _, agent := range agents {
		status := registry.DetermineStatus(agent.LastSeen, now)
		if status == registry.StatusUnhealthy {
			continue
		}
		if err := visitor(agent); err != nil {
			logger.Warn().Err(err).
				Str("agent_id", agent.AgentID).
				Str("mesh_ip", agent.MeshIPv4).
				Msg("Failed to poll agent")
			errors++
			continue
		}
		success++
	}
	return success, errors
}

// Gap represents a detected sequence gap between two seq_ids (RFD 089).
type Gap struct {
	// StartSeqID is the first missing seq_id in the gap (inclusive).
	StartSeqID uint64
	// EndSeqID is the last missing seq_id in the gap (inclusive).
	EndSeqID uint64
}

// DetectGaps finds non-consecutive sequence IDs in a batch of records (RFD 089).
// It compares each record's seq_id against the expected next seq_id (starting from
// lastCheckpointSeqID + 1). To avoid false gaps from concurrent DuckDB transactions
// that commit out of order, gaps are only reported if the record after the gap has a
// timestamp older than the grace period (10 seconds).
//
// seqIDs must be sorted in ascending order (as returned by ORDER BY seq_id ASC).
// timestampsMs are the corresponding timestamps in Unix milliseconds, aligned by index.
func DetectGaps(lastCheckpointSeqID uint64, seqIDs []uint64, timestampsMs []int64) []Gap {
	if len(seqIDs) == 0 || len(seqIDs) != len(timestampsMs) {
		return nil
	}

	const gracePeriodMs = 10_000 // 10 seconds in milliseconds.
	nowMs := time.Now().UnixMilli()

	var gaps []Gap
	expected := lastCheckpointSeqID + 1

	for i, seqID := range seqIDs {
		if seqID > expected {
			// Potential gap: check grace period on the record after the gap.
			recordAgeMs := nowMs - timestampsMs[i]
			if recordAgeMs >= gracePeriodMs {
				gaps = append(gaps, Gap{
					StartSeqID: expected,
					EndSeqID:   seqID - 1,
				})
			}
		}
		if seqID >= expected {
			expected = seqID + 1
		}
	}

	return gaps
}

// cleanupLoop runs cleanup operations periodically.
func (b *BasePoller) cleanupLoop(poller Poller) {
	defer b.wg.Done()

	ticker := time.NewTicker(b.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			b.safeRunCleanup(poller)
		}
	}
}

// safeRunCleanup calls RunCleanup with panic recovery.
func (b *BasePoller) safeRunCleanup(poller Poller) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Error().
				Str("panic", fmt.Sprintf("%v", r)).
				Msg("Panic recovered in cleanup cycle")
		}
	}()

	if err := poller.RunCleanup(b.ctx); err != nil {
		b.logger.Error().Err(err).Msg("Cleanup failed")
	}
}
