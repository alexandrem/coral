// Package duckdb provides HTTP handlers for serving DuckDB databases remotely.
// This package is shared between agent and colony for RFD 039 and RFD 046.
package duckdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

const (
	// checkpointWait bounds how long a checkpoint request waits for the
	// database connection, which other work may be holding.
	checkpointWait = 10 * time.Second

	// checkpointMinInterval is how recent a checkpoint must be for a new
	// request to reuse it instead of running another one.
	checkpointMinInterval = 2 * time.Second
)

// DuckDBHandler serves DuckDB files over HTTP for remote attach (RFD 039, RFD 046).
// This enables CLI tools to attach to agent and colony databases using DuckDB's HTTP remote feature.
type DuckDBHandler struct {
	// Map of allowlisted database names to served databases.
	// Only files in this map can be served (prevents directory traversal).
	databases map[string]*servedDatabase
	logger    zerolog.Logger
}

// servedDatabase is a database file and, when available, the open handle of
// the process that writes it.
type servedDatabase struct {
	path string
	db   *sql.DB

	// sem serializes checkpoints; holding it guards lastStart.
	sem       chan struct{}
	lastStart time.Time
}

// NewDuckDBHandler creates a new DuckDB HTTP handler.
func NewDuckDBHandler(logger zerolog.Logger) *DuckDBHandler {
	return &DuckDBHandler{
		databases: make(map[string]*servedDatabase),
		logger:    logger.With().Str("component", "duckdb_handler").Logger(),
	}
}

// RegisterDatabase adds a database file to the allowlist for serving.
// name: The URL path component (e.g., "beyla.duckdb").
// filePath: Absolute path to the DuckDB file on disk.
// db: The open handle that writes filePath. If db is not nil, clients can
// request a checkpoint so that committed data reaches the file.
func (h *DuckDBHandler) RegisterDatabase(name string, filePath string, db *sql.DB) error {
	// Validate file exists.
	if _, err := os.Stat(filePath); err != nil {
		return fmt.Errorf("database file not found: %w", err)
	}

	// Clean the name to prevent path traversal.
	name = filepath.Base(name)

	h.databases[name] = &servedDatabase{path: filePath, db: db, sem: make(chan struct{}, 1)}
	h.logger.Info().
		Str("name", name).
		Str("path", filePath).
		Msg("Registered DuckDB database for HTTP serving")

	return nil
}

// ServeHTTP implements http.Handler for serving DuckDB files.
// Handles GET requests to /duckdb/<filename> (serve file) or /duckdb (list
// databases), and POST requests to /duckdb/<filename>/checkpoint.
func (h *DuckDBHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) == 3 && pathParts[0] == "duckdb" && pathParts[2] == "checkpoint" {
		h.serveCheckpoint(w, r, pathParts[1])
		return
	}

	// Allow GET and HEAD requests (read-only).
	// HEAD is used by DuckDB to check file existence and size.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.logger.Warn().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Msg("Rejected non-GET/HEAD request to DuckDB endpoint")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Expected formats:
	//   - /duckdb (list databases)
	//   - /duckdb/<filename> (serve specific database)
	if pathParts[0] != "duckdb" {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	// Handle database list request.
	if len(pathParts) == 1 {
		h.serveListDatabases(w, r)
		return
	}

	// Get requested database name (filename only, no directory components).
	dbName := filepath.Base(pathParts[1])

	// Check if database is registered (allowlist check).
	// Also handle .wal files (e.g. metrics.duckdb.wal) if the main DB is registered.
	isWal := false
	if strings.HasSuffix(dbName, ".wal") {
		dbName = strings.TrimSuffix(dbName, ".wal")
		isWal = true
	}

	database, ok := h.databases[dbName]
	if !ok {
		h.logger.Debug().
			Str("db_name", dbName).
			Msg("Database not found in registry")
		http.Error(w, "database not found", http.StatusNotFound)
		return
	}

	// WAL files are for crash recovery of the local writer process.
	// A remote READ_ONLY ATTACH must read from the last checkpoint in the main
	// database file; replaying a live WAL is unreliable (TOCTOU: the agent may
	// checkpoint and truncate the WAL between DuckDB's HEAD and range-GET calls,
	// turning a satisfiable range into a 416). Returning 404 tells DuckDB "no WAL",
	// which is the correct and safe signal for a read-only remote consumer.
	if isWal {
		h.logger.Debug().
			Str("db_name", dbName).
			Msg("Declining WAL request for remote read-only access")
		http.Error(w, "wal not available", http.StatusNotFound)
		return
	}

	// Verify file still exists before serving.
	if _, err := os.Stat(database.path); err != nil {
		h.logger.Warn().
			Str("db_name", dbName).
			Str("path", database.path).
			Err(err).
			Msg("Database file no longer exists")
		http.Error(w, "database not found", http.StatusNotFound)
		return
	}

	// Serve the file with appropriate headers.
	// DuckDB uses HTTP range requests to fetch specific database pages.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-cache") // Metrics data changes frequently
	w.Header().Set("Accept-Ranges", "bytes")    // Enable range requests for DuckDB

	h.logger.Debug().
		Str("db_name", dbName).
		Str("remote_addr", r.RemoteAddr).
		Str("range", r.Header.Get("Range")).
		Msg("Serving DuckDB database")

	// Use http.ServeFile for efficient range request handling.
	http.ServeFile(w, r, database.path)
}

// serveCheckpoint checkpoints a database so that a remote read-only attach
// sees the data committed before the request. Remote readers only see the
// main database file because the WAL is never served.
func (h *DuckDBHandler) serveCheckpoint(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	database, ok := h.databases[name]
	if !ok || database.db == nil {
		http.Error(w, "database not found", http.StatusNotFound)
		return
	}

	if err := database.checkpoint(r.Context()); err != nil {
		event := h.logger.Warn()
		if IsFatal(err) {
			event = h.logger.Error()
		}
		event.Err(err).Str("db_name", name).Msg("Failed to checkpoint DuckDB database for remote readers")
		http.Error(w, "checkpoint failed", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// checkpoint writes committed WAL data to the database file. Requests that
// arrive shortly after a checkpoint started reuse it, which bounds how often
// clients can make the server checkpoint.
func (d *servedDatabase) checkpoint(ctx context.Context) error {
	requested := time.Now()
	ctx, cancel := context.WithTimeout(ctx, checkpointWait)
	defer cancel()

	select {
	case d.sem <- struct{}{}:
		defer func() { <-d.sem }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if requested.Sub(d.lastStart) < checkpointMinInterval {
		return nil
	}

	conn, err := d.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	// Never interrupt a checkpoint once it has the connection.
	start := time.Now()
	if _, err := conn.ExecContext(context.WithoutCancel(ctx), "CHECKPOINT"); err != nil {
		return err
	}
	d.lastStart = start
	return nil
}

// serveListDatabases returns a JSON list of available databases.
func (h *DuckDBHandler) serveListDatabases(w http.ResponseWriter, r *http.Request) {
	// Build list of database names.
	databases := make([]string, 0, len(h.databases))
	for name := range h.databases {
		databases = append(databases, name)
	}

	// Return as JSON.
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"databases": databases,
	}); err != nil {
		h.logger.Error().
			Err(err).
			Msg("Failed to encode database list")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	h.logger.Debug().
		Str("remote_addr", r.RemoteAddr).
		Int("count", len(databases)).
		Msg("Served database list")
}
