package agent

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/agent/telemetry"
	"github.com/coral-mesh/coral/internal/duckdb"
)

// TelemetryReceiver wraps the OTLP receiver for external use.
type TelemetryReceiver struct {
	receiver *telemetry.OTLPReceiver
	storage  *telemetry.Storage
	db       *sql.DB
	dbPath   string // Database file path for HTTP serving (RFD 039).
	ownsDB   bool   // Whether this receiver owns the database (should close it on Stop).
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
	db, err := duckdb.OpenDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create telemetry database: %w", err)
	}

	// Create storage.
	storage, err := telemetry.NewStorage(db, logger)
	if err != nil {
		_ = db.Close() // TODO: errcheck
		return nil, fmt.Errorf("failed to create telemetry storage: %w", err)
	}

	// Create OTLP receiver.
	receiver, err := telemetry.NewOTLPReceiver(config, storage, logger)
	if err != nil {
		_ = db.Close() // TODO: errcheck
		return nil, fmt.Errorf("failed to create OTLP receiver: %w", err)
	}

	return &TelemetryReceiver{
		receiver: receiver,
		storage:  storage,
		db:       db,
		dbPath:   dbPath,
		ownsDB:   true, // This receiver created the database, so it owns it.
		logger:   logger,
	}, nil
}

// NewTelemetryReceiverWithSharedDB creates a telemetry receiver using a shared database.
// The receiver will NOT close the database on Stop() since it doesn't own it.
func NewTelemetryReceiverWithSharedDB(config telemetry.Config, db *sql.DB, dbPath string, logger zerolog.Logger) (*TelemetryReceiver, error) {
	if config.Disabled {
		return nil, fmt.Errorf("telemetry is disabled")
	}

	if db == nil {
		return nil, fmt.Errorf("shared database is required")
	}

	// Create storage using the shared database.
	storage, err := telemetry.NewStorage(db, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create telemetry storage: %w", err)
	}

	// Create OTLP receiver.
	receiver, err := telemetry.NewOTLPReceiver(config, storage, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP receiver: %w", err)
	}

	return &TelemetryReceiver{
		receiver: receiver,
		storage:  storage,
		db:       db,
		dbPath:   dbPath,
		ownsDB:   false, // Database is shared, don't close it on Stop().
		logger:   logger,
	}, nil
}

// Start starts the telemetry receiver.
func (r *TelemetryReceiver) Start(ctx context.Context) error {
	// Start cleanup loop (RFD 032).
	// Use configured retention or default to 1 hour.
	retention := 1 * time.Hour
	// TODO: Pass retention from config. For now, hardcode to 1h to match default.
	go r.storage.RunCleanupLoop(ctx, retention)

	return r.receiver.Start(ctx)
}

// Stop stops the telemetry receiver.
func (r *TelemetryReceiver) Stop() error {
	if err := r.receiver.Stop(); err != nil {
		return err
	}

	// Only close the database if we own it (not using a shared database).
	if r.ownsDB {
		if err := r.db.Close(); err != nil {
			return fmt.Errorf("failed to close telemetry database: %w", err)
		}
	}

	return nil
}

// QuerySpansBySeqID queries spans with seq_id > startSeqID (RFD 089).
func (r *TelemetryReceiver) QuerySpansBySeqID(ctx context.Context, startSeqID uint64, maxRecords int32, serviceNames []string) ([]telemetry.Span, uint64, error) {
	return r.receiver.QuerySpansBySeqID(ctx, startSeqID, maxRecords, serviceNames)
}

// GetDatabasePath returns the file path to the telemetry DuckDB database (RFD 039).
// Returns empty string if database is in-memory.
func (r *TelemetryReceiver) GetDatabasePath() string {
	if r.dbPath == ":memory:" {
		return ""
	}
	return r.dbPath
}

// GetReceiver returns the underlying OTLP receiver.
// This allows other components (like Beyla) to access the receiver for metrics polling.
func (r *TelemetryReceiver) GetReceiver() *telemetry.OTLPReceiver {
	return r.receiver
}
