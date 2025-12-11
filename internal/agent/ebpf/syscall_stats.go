package ebpf

import (
	"context"
	"math/rand"
	"sync"
	"time"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// SyscallStatsCollector collects syscall statistics.
// This is a minimal stub implementation for testing.
// Production version would use actual eBPF programs.
type SyscallStatsCollector struct {
	logger zerolog.Logger
	config map[string]string
	ctx    context.Context
	cancel context.CancelFunc
	events []*meshv1.EbpfEvent
	mu     sync.Mutex
	ticker *time.Ticker
}

// NewSyscallStatsCollector creates a new syscall stats collector.
func NewSyscallStatsCollector(logger zerolog.Logger, config map[string]string) *SyscallStatsCollector {
	return &SyscallStatsCollector{
		logger: logger.With().Str("collector", "syscall_stats").Logger(),
		config: config,
		events: make([]*meshv1.EbpfEvent, 0),
	}
}

// Start begins collecting syscall statistics.
func (c *SyscallStatsCollector) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.ctx, c.cancel = context.WithCancel(ctx)

	// Start mock collection (in production, this would load eBPF programs).
	c.ticker = time.NewTicker(1 * time.Second)

	go c.collectLoop()

	c.logger.Info().Msg("Started syscall stats collector")
	return nil
}

// Stop stops the collector and cleans up resources.
func (c *SyscallStatsCollector) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
	}

	if c.ticker != nil {
		c.ticker.Stop()
	}

	c.logger.Info().Msg("Stopped syscall stats collector")
	return nil
}

// GetEvents retrieves collected events since last call.
func (c *SyscallStatsCollector) GetEvents() ([]*meshv1.EbpfEvent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Return all events and clear buffer.
	events := c.events
	c.events = make([]*meshv1.EbpfEvent, 0)

	return events, nil
}

// collectLoop simulates syscall data collection.
// In production, this would poll eBPF maps and aggregate results.
func (c *SyscallStatsCollector) collectLoop() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.ticker.C:
			c.collectSample()
		}
	}
}

// collectSample generates a mock syscall stats sample.
// In production, this would read from eBPF maps.
func (c *SyscallStatsCollector) collectSample() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Generate mock syscall data.
	syscalls := []string{"read", "write", "open", "close", "stat", "mmap", "poll"}

	for _, syscall := range syscalls {
		stats := &meshv1.SyscallStats{
			SyscallName: syscall,
			//nolint:gosec // G115,G404: Test data generation with intentional weak random.
			CallCount: uint64(rand.Intn(1000) + 100),
			//nolint:gosec // G404: Test data generation with intentional weak random.
			ErrorCount: uint64(rand.Intn(10)),
			//nolint:gosec // G404: Test data generation with intentional weak random.
			TotalDurationUs: uint64(rand.Intn(10000) + 1000),
			Labels:          map[string]string{},
		}

		event := &meshv1.EbpfEvent{
			Timestamp:   timestamppb.Now(),
			CollectorId: "unknown", // Will be set by manager.
			AgentId:     "unknown", // Will be set by manager.
			ServiceName: "test-service",
			Payload: &meshv1.EbpfEvent_SyscallStats{
				SyscallStats: stats,
			},
		}

		c.events = append(c.events, event)
	}

	c.logger.Debug().
		Int("event_count", len(c.events)).
		Msg("Collected syscall stats sample")
}
