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

// TelemetryReceiver wraps the OTLP receiver for external use.
type TelemetryReceiver struct {
	receiver *telemetry.OTLPReceiver
	storage  *telemetry.Storage
	db       *sql.DB
	dbPath   string // Database file path for HTTP serving (RFD 039).
	logger   zerolog.Logger
}

// NewTelemetryReceiver creates a new telemetry receiver.
func NewTelemetryReceiver(config telemetry.Config, logger zerolog.Logger) (*TelemetryReceiver, error) {
	if config.Disabled {
		return nil, fmt.Errorf("telemetry is disabled")
	}

	// Determine database path: use file-based storage for HTTP serving.
	// Default to ~/.coral/agent/telemetry.duckdb if not specified.
	dbPath := config.DatabasePath
	if dbPath == "" {
		dbPath = ":memory:"
		logger.Warn().Msg("No database path configured, using in-memory storage (HTTP serving disabled)")
	}

	// Create DuckDB database.
	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create telemetry database: %w", err)
	}

	// Create storage.
	storage, err := telemetry.NewStorage(db, logger)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create telemetry storage: %w", err)
	}

	// Create OTLP receiver.
	receiver, err := telemetry.NewOTLPReceiver(config, storage, logger)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create OTLP receiver: %w", err)
	}

	return &TelemetryReceiver{
		receiver: receiver,
		storage:  storage,
		db:       db,
		dbPath:   dbPath,
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

// GetDatabasePath returns the file path to the telemetry DuckDB database (RFD 039).
// Returns empty string if database is in-memory.
func (r *TelemetryReceiver) GetDatabasePath() string {
	if r.dbPath == ":memory:" {
		return ""
	}
	return r.dbPath
}
