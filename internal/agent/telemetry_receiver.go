package agent

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/rs/zerolog"

	"github.com/coral-io/coral/internal/agent/telemetry"
)

// TelemetryConfig contains telemetry configuration.
type TelemetryConfig struct {
	Enabled               bool
	GRPCEndpoint          string
	HTTPEndpoint          string
	StorageRetentionHours int
	AgentID               string
	Filters               TelemetryFilterConfig
}

// TelemetryFilterConfig contains filtering configuration.
type TelemetryFilterConfig struct {
	AlwaysCaptureErrors    bool
	HighLatencyThresholdMs float64
	SampleRate             float64
}

// TelemetryReceiver wraps the OTLP receiver for external use.
type TelemetryReceiver struct {
	receiver *telemetry.OTLPReceiver
	storage  *telemetry.Storage
	db       *sql.DB
	logger   zerolog.Logger
}

// NewTelemetryReceiver creates a new telemetry receiver.
func NewTelemetryReceiver(config TelemetryConfig, logger zerolog.Logger) (*TelemetryReceiver, error) {
	if !config.Enabled {
		return nil, fmt.Errorf("telemetry is not enabled")
	}

	// Create in-memory DuckDB for span storage.
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("failed to create telemetry database: %w", err)
	}

	// Create storage.
	storage, err := telemetry.NewStorage(db, logger)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create telemetry storage: %w", err)
	}

	// Create telemetry config.
	telemetryConfig := telemetry.Config{
		Enabled:               config.Enabled,
		GRPCEndpoint:          config.GRPCEndpoint,
		HTTPEndpoint:          config.HTTPEndpoint,
		StorageRetentionHours: config.StorageRetentionHours,
		AgentID:               config.AgentID,
		Filters: telemetry.FilterConfig{
			AlwaysCaptureErrors:    config.Filters.AlwaysCaptureErrors,
			HighLatencyThresholdMs: config.Filters.HighLatencyThresholdMs,
			SampleRate:             config.Filters.SampleRate,
		},
	}

	// Create OTLP receiver.
	receiver, err := telemetry.NewOTLPReceiver(telemetryConfig, storage, logger)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create OTLP receiver: %w", err)
	}

	return &TelemetryReceiver{
		receiver: receiver,
		storage:  storage,
		db:       db,
		logger:   logger,
	}, nil
}

// Start starts the telemetry receiver.
func (r *TelemetryReceiver) Start(ctx context.Context) error {
	return r.receiver.Start(ctx)
}

// Stop stops the telemetry receiver.
func (r *TelemetryReceiver) Stop() error {
	if err := r.receiver.Stop(); err != nil {
		return err
	}

	if err := r.db.Close(); err != nil {
		return fmt.Errorf("failed to close telemetry database: %w", err)
	}

	return nil
}

// QuerySpans queries spans from local storage.
// This is called by the QueryTelemetry RPC handler.
func (r *TelemetryReceiver) QuerySpans(ctx context.Context, startTime, endTime time.Time, serviceNames []string) ([]telemetry.Span, error) {
	return r.receiver.QuerySpans(ctx, startTime, endTime, serviceNames)
}
