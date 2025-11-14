package telemetry

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Receiver handles OTLP data ingestion, filtering, and aggregation.
// This is a placeholder for the actual OTLP receiver implementation.
// The full implementation would integrate with go.opentelemetry.io/collector.
type Receiver struct {
	config     Config
	filter     *Filter
	aggregator *Aggregator
	logger     zerolog.Logger
	mu         sync.Mutex
	running    bool
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

// NewReceiver creates a new OTLP receiver.
func NewReceiver(config Config, logger zerolog.Logger) (*Receiver, error) {
	if !config.Enabled {
		return nil, fmt.Errorf("telemetry is not enabled")
	}

	return &Receiver{
		config:     config,
		filter:     NewFilter(config.Filters),
		aggregator: NewAggregator(config.AgentID),
		logger:     logger.With().Str("component", "telemetry_receiver").Logger(),
		stopCh:     make(chan struct{}),
	}, nil
}

// Start begins the OTLP receiver.
func (r *Receiver) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return fmt.Errorf("receiver already running")
	}

	r.logger.Info().
		Str("endpoint", r.config.Endpoint).
		Msg("Starting OTLP receiver")

	// Start flush goroutine.
	r.wg.Add(1)
	go r.flushLoop(ctx)

	r.running = true

	// TODO: Start actual OTLP gRPC/HTTP servers.
	// This would use go.opentelemetry.io/collector components.
	// For now, this is a placeholder showing the structure.

	r.logger.Info().Msg("OTLP receiver started")
	return nil
}

// Stop stops the OTLP receiver.
func (r *Receiver) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return nil
	}

	r.logger.Info().Msg("Stopping OTLP receiver")

	close(r.stopCh)
	r.wg.Wait()

	r.running = false

	r.logger.Info().Msg("OTLP receiver stopped")
	return nil
}

// ProcessSpan processes a single span through filtering and aggregation.
// This would be called by the OTLP receiver for each span.
func (r *Receiver) ProcessSpan(span Span) {
	// Apply filtering.
	if !r.filter.ShouldCapture(span) {
		return
	}

	// Add to aggregator.
	r.aggregator.AddSpan(span)
}

// flushLoop periodically flushes aggregated buckets.
func (r *Receiver) flushLoop(ctx context.Context) {
	defer r.wg.Done()

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			buckets := r.aggregator.FlushBuckets()
			if len(buckets) > 0 {
				r.logger.Debug().
					Int("bucket_count", len(buckets)).
					Msg("Flushed telemetry buckets")

				// TODO: Send buckets to colony via gRPC.
				// This would call colony.IngestTelemetry(buckets).
			}

		case <-r.stopCh:
			// Final flush before stopping.
			buckets := r.aggregator.FlushBuckets()
			if len(buckets) > 0 {
				r.logger.Debug().
					Int("bucket_count", len(buckets)).
					Msg("Final flush of telemetry buckets")
			}
			return

		case <-ctx.Done():
			return
		}
	}
}

// GetBuckets returns the current aggregated buckets (for testing).
func (r *Receiver) GetBuckets() []Bucket {
	return r.aggregator.FlushBuckets()
}
