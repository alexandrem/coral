package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/rs/zerolog"
)

// Database wraps a DuckDB connection for colony storage.
type Database struct {
	db       *sql.DB
	path     string
	colonyID string
	logger   zerolog.Logger
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
	if err := os.MkdirAll(storagePath, 0755); err != nil {
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

	// Configure connection pool.
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	// Test connection.
	if err := db.Ping(); err != nil {
		_ = db.Close() // TODO: errcheck
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	database := &Database{
		db:       db,
		path:     dbPath,
		colonyID: colonyID,
		logger:   logger,
	}

	// Initialize schema (only in read-write mode).
	if !readOnly {
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
