package telemetry

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Receiver handles OTLP data ingestion, filtering, and local storage.
// This is a placeholder for the actual OTLP receiver implementation.
// The full implementation would integrate with go.opentelemetry.io/collector.
// Receiver stores filtered spans locally; colony queries on-demand (RFD 025).
type Receiver struct {
	config  Config
	filter  *Filter
	storage *Storage
	logger  zerolog.Logger
	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

// NewReceiver creates a new OTLP receiver.
func NewReceiver(config Config, storage *Storage, logger zerolog.Logger) (*Receiver, error) {
	if !config.Enabled {
		return nil, fmt.Errorf("telemetry is not enabled")
	}

	return &Receiver{
		config:  config,
		filter:  NewFilter(config.Filters),
		storage: storage,
		logger:  logger.With().Str("component", "telemetry_receiver").Logger(),
		stopCh:  make(chan struct{}),
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

	// Start cleanup goroutine for local storage (~1 hour retention).
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.storage.RunCleanupLoop(ctx, 1*time.Hour)
	}()

	r.running = true

	// TODO: Start actual OTLP gRPC/HTTP servers.
	// This would use go.opentelemetry.io/collector components.
	// For now, this is a placeholder showing the structure.

	r.logger.Info().Msg("OTLP receiver started with local storage")
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

// ProcessSpan processes a single span through filtering and local storage.
// This would be called by the OTLP receiver for each span.
func (r *Receiver) ProcessSpan(ctx context.Context, span Span) error {
	// Apply filtering.
	if !r.filter.ShouldCapture(span) {
		return nil
	}

	// Store in local storage.
	if err := r.storage.StoreSpan(ctx, span); err != nil {
		r.logger.Warn().
			Err(err).
			Str("trace_id", span.TraceID).
			Msg("Failed to store span")
		return err
	}

	return nil
}

// QuerySpans queries filtered spans from local storage.
// This is called by the QueryTelemetry RPC handler (colony → agent).
func (r *Receiver) QuerySpans(ctx context.Context, startTime, endTime time.Time, serviceNames []string) ([]Span, error) {
	return r.storage.QuerySpans(ctx, startTime, endTime, serviceNames)
}
