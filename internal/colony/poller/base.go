// Package poller provides common polling infrastructure for colony pollers.
// It eliminates duplicated code across multiple poller implementations.
package poller

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"
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
	if err := poller.PollOnce(b.ctx); err != nil {
		b.logger.Error().Err(err).Msg("Initial poll failed")
	}

	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			if err := poller.PollOnce(b.ctx); err != nil {
				b.logger.Error().Err(err).Msg("Poll failed")
			}
		}
	}
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
			if err := poller.RunCleanup(b.ctx); err != nil {
				b.logger.Error().Err(err).Msg("Cleanup failed")
			}
		}
	}
}
