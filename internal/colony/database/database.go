// Package database provides DuckDB database operations for the colony.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/duckdb"
	"github.com/coral-mesh/coral/internal/privilege"
)

// Database wraps a DuckDB connection for colony storage.
type Database struct {
	db                *sql.DB
	path              string
	colonyID          string
	logger            zerolog.Logger
	profileFrameStore *ProfileFrameStore // RFD 072: Global frame dictionary for CPU profiling.

	// Tables (ORM)
	servicesTable       *duckdb.Table[Service]
	heartbeatsTable     *duckdb.Table[ServiceHeartbeat]
	telemetryTable      *duckdb.Table[otelSummary]
	systemMetricsTable  *duckdb.Table[SystemMetricsSummary]
	cpuProfilesTable    *duckdb.Table[CPUProfileSummary]
	memoryProfilesTable *duckdb.Table[MemoryProfileSummary]
	binaryMetadataTable *duckdb.Table[BinaryMetadata]
	beylaHTTPTable      *duckdb.Table[beylaHTTPMetricDB]
	beylaGRPCTable      *duckdb.Table[beylaGRPCMetricDB]
	beylaSQLTable       *duckdb.Table[beylaSQLMetricDB]
	beylaTracesTable    *duckdb.Table[beylaTraceDB]
	ipAllocationsTable  *duckdb.Table[IPAllocation]
	debugSessionsTable  *duckdb.Table[DebugSession]
	debugEventsTable    *duckdb.Table[DebugEvent]
	connectionsTable    *duckdb.Table[ServiceConnection]
}

// New creates and initializes a DuckDB database for the colony.
// It creates the storage directory if it doesn't exist, opens the database
// connection, and initializes the schema.
func New(storagePath, colonyID string, logger zerolog.Logger) (*Database, error) {
	return open(storagePath, colonyID, logger, false)
}

// NewReadOnly opens the database in read-only mode for read-only access.
// This allows multiple processes to read from the same database without lock conflicts.
func NewReadOnly(storagePath, colonyID string, logger zerolog.Logger) (*Database, error) {
	return open(storagePath, colonyID, logger, true)
}

// open is the internal function that opens the database with optional read-only mode.
func open(storagePath, colonyID string, logger zerolog.Logger, readOnly bool) (*Database, error) {
	// Ensure storage directory exists.
	if err := os.MkdirAll(storagePath, 0750); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	// Construct database file path.
	dbPath := filepath.Join(storagePath, colonyID+".duckdb")

	// Build connection string.
	connStr := dbPath
	if readOnly {
		connStr = dbPath + "?access_mode=READ_ONLY"
	}

	// Open DuckDB connection.
	db, err := sql.Open("duckdb", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Fix ownership of storage directory and database file if running as root.
	if !readOnly {
		if err := privilege.FixFileOwnership(storagePath); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed to fix storage directory ownership: %w", err)
		}
		if err := privilege.FixFileOwnership(dbPath); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed to fix database file ownership: %w", err)
		}
		// Also fix .wal file if it exists (DuckDB write-ahead log).
		walPath := dbPath + ".wal"
		if _, err := os.Stat(walPath); err == nil {
			if err := privilege.FixFileOwnership(walPath); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("failed to fix WAL file ownership: %w", err)
			}
		}
	}

	// Configure connection pool.
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	// Test connection.
	if err := db.Ping(); err != nil {
		_ = db.Close() // TODO: errcheck
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	database := &Database{
		db:                db,
		path:              dbPath,
		colonyID:          colonyID,
		logger:            logger,
		profileFrameStore: NewProfileFrameStore(), // RFD 072.

		servicesTable:       duckdb.NewTable[Service](db, "services"),
		heartbeatsTable:     duckdb.NewTable[ServiceHeartbeat](db, "service_heartbeats"),
		telemetryTable:      duckdb.NewTable[otelSummary](db, "otel_summaries"),
		systemMetricsTable:  duckdb.NewTable[SystemMetricsSummary](db, "system_metrics_summaries"),
		cpuProfilesTable:    duckdb.NewTable[CPUProfileSummary](db, "cpu_profile_summaries"),
		memoryProfilesTable: duckdb.NewTable[MemoryProfileSummary](db, "memory_profile_summaries"),
		binaryMetadataTable: duckdb.NewTable[BinaryMetadata](db, "binary_metadata_registry"),
		beylaHTTPTable:      duckdb.NewTable[beylaHTTPMetricDB](db, "beyla_http_metrics"),
		beylaGRPCTable:      duckdb.NewTable[beylaGRPCMetricDB](db, "beyla_grpc_metrics"),
		beylaSQLTable:       duckdb.NewTable[beylaSQLMetricDB](db, "beyla_sql_metrics"),
		beylaTracesTable:    duckdb.NewTable[beylaTraceDB](db, "beyla_traces"),
		ipAllocationsTable:  duckdb.NewTable[IPAllocation](db, "agent_ip_allocations"),
		debugSessionsTable:  duckdb.NewTable[DebugSession](db, "debug_sessions"),
		debugEventsTable:    duckdb.NewTable[DebugEvent](db, "debug_events"),
		connectionsTable:    duckdb.NewTable[ServiceConnection](db, "service_connections"),
	}

	// Initialize schema (only in read-write mode).
	if !readOnly {
		// Load VSS extension first, then enable HNSW persistence.
		if err := database.ensureVSSExtension(); err != nil {
			logger.Warn().Err(err).Msg("Failed to load VSS extension, vector search features may be unavailable")
		} else {
			// Enable HNSW experimental persistence for vector indexes.
			if _, err := db.Exec("SET hnsw_enable_experimental_persistence = true"); err != nil {
				logger.Warn().Err(err).Msg("Failed to enable HNSW persistence, vector indexes may not work")
			}
		}

		if err := database.initSchema(); err != nil {
			_ = db.Close() // TODO: errcheck
			return nil, fmt.Errorf("failed to initialize schema: %w", err)
		}
	}

	mode := "read-write"
	if readOnly {
		mode = "read-only"
	}

	logger.Info().
		Str("path", dbPath).
		Str("colony_id", colonyID).
		Str("mode", mode).
		Msg("Database initialized")

	return database, nil
}

// Close closes the database connection gracefully.
func (d *Database) Close() error {
	if d.db == nil {
		return nil
	}

	if err := d.db.Close(); err != nil {
		return fmt.Errorf("failed to close database: %w", err)
	}

	d.logger.Info().
		Str("path", d.path).
		Msg("Database closed")
	return nil
}

// Ping checks if the database connection is alive.
func (d *Database) Ping(ctx context.Context) error {
	return d.db.PingContext(ctx)
}

// DB returns the underlying sql.DB connection for use by other packages.
func (d *Database) DB() *sql.DB {
	return d.db
}

// Path returns the file path of the database.
func (d *Database) Path() string {
	return d.path
}

// ColonyID returns the colony ID this database belongs to.
func (d *Database) ColonyID() string {
	return d.colonyID
}

// ExecContext executes a query with logging and timing.
func (d *Database) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	start := time.Now()
	d.logger.Trace().
		Str("query", formatQuery(query, args)).
		Msg("Executing query")

	result, err := d.db.ExecContext(ctx, query, args...)

	d.logger.Trace().
		Str("query", formatQuery(query, args)).
		Dur("duration_ms", time.Since(start)).
		Err(err).
		Msg("Query executed")

	return result, err
}

// QueryContext executes a query with logging and timing.
func (d *Database) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	start := time.Now()
	d.logger.Trace().
		Str("query", formatQuery(query, args)).
		Msg("Executing query")

	rows, err := d.db.QueryContext(ctx, query, args...)

	d.logger.Trace().
		Str("query", formatQuery(query, args)).
		Dur("duration_ms", time.Since(start)).
		Err(err).
		Msg("Query executed")

	return rows, err
}

// QueryRowContext executes a query that returns at most one row with logging and timing.
func (d *Database) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	start := time.Now()
	d.logger.Trace().
		Str("query", formatQuery(query, args)).
		Msg("Executing query")

	row := d.db.QueryRowContext(ctx, query, args...)

	d.logger.Trace().
		Str("query", formatQuery(query, args)).
		Dur("duration_ms", time.Since(start)).
		Msg("Query executed")

	return row
}

// QueryAllServiceNames returns all unique service names from the service registry.
// This includes both active and recently-seen services persisted in the database.
func (d *Database) QueryAllServiceNames(ctx context.Context) ([]string, error) {
	query := `
		SELECT DISTINCT name 
		FROM services 
		WHERE name IS NOT NULL AND name != ''
		ORDER BY name
	`

	rows, err := d.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query service names: %w", err)
	}
	defer func(rows *sql.Rows) {
		_ = rows.Close() // TODO: errcheck
	}(rows)

	var services []string
	for rows.Next() {
		var serviceName string
		if err := rows.Scan(&serviceName); err != nil {
			return nil, fmt.Errorf("failed to scan service name: %w", err)
		}
		services = append(services, serviceName)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return services, nil
}

// formatQuery formats a query with interpolated arguments for logging.
func formatQuery(query string, args []interface{}) string {
	// Interpolate arguments into query.
	interpolated := duckdb.InterpolateQuery(query, args)

	// Clean up whitespace for prettier logging.
	return cleanQueryWhitespace(interpolated)
}

// cleanQueryWhitespace condenses multiple whitespace characters into single spaces.
func cleanQueryWhitespace(query string) string {
	var result strings.Builder
	result.Grow(len(query))

	inWhitespace := false
	for _, ch := range query {
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			if !inWhitespace {
				result.WriteRune(' ')
				inWhitespace = true
			}
		} else {
			result.WriteRune(ch)
			inWhitespace = false
		}
	}

	return strings.TrimSpace(result.String())
}
