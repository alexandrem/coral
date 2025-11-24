package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"
)

// DuckDBHandler serves DuckDB files over HTTP for remote attach (RFD 039).
// This enables CLI tools to attach to agent databases using DuckDB's HTTP remote feature.
type DuckDBHandler struct {
	// Map of allowlisted database names to file paths.
	// Only files in this map can be served (prevents directory traversal).
	databases map[string]string
	logger    zerolog.Logger
}

// NewDuckDBHandler creates a new DuckDB HTTP handler.
func NewDuckDBHandler(logger zerolog.Logger) *DuckDBHandler {
	return &DuckDBHandler{
		databases: make(map[string]string),
		logger:    logger.With().Str("component", "duckdb_handler").Logger(),
	}
}

// RegisterDatabase adds a database file to the allowlist for serving.
// name: The URL path component (e.g., "beyla.duckdb").
// filePath: Absolute path to the DuckDB file on disk.
func (h *DuckDBHandler) RegisterDatabase(name string, filePath string) error {
	// Validate file exists.
	if _, err := os.Stat(filePath); err != nil {
		return fmt.Errorf("database file not found: %w", err)
	}

	// Clean the name to prevent path traversal.
	name = filepath.Base(name)

	h.databases[name] = filePath
	h.logger.Info().
		Str("name", name).
		Str("path", filePath).
		Msg("Registered DuckDB database for HTTP serving")

	return nil
}

// ServeHTTP implements http.Handler for serving DuckDB files.
// Handles GET requests to /duckdb/<filename> (serve file) or /duckdb (list databases).
func (h *DuckDBHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	// Extract filename from URL path.
	// Expected formats:
	//   - /duckdb (list databases)
	//   - /duckdb/<filename> (serve specific database)
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 1 || pathParts[0] != "duckdb" {
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

	filePath, ok := h.databases[dbName]
	if !ok {
		h.logger.Debug().
			Str("db_name", dbName).
			Msg("Database not found in registry")
		http.Error(w, "database not found", http.StatusNotFound)
		return
	}

	// If requesting WAL, append .wal to the path.
	if isWal {
		filePath = filePath + ".wal"
	}

	// Verify file still exists before serving.
	if _, err := os.Stat(filePath); err != nil {
		// If it's a WAL file, it might have been checkpointed and removed.
		// This is expected behavior, so just return 404 without warning.
		if isWal && os.IsNotExist(err) {
			h.logger.Debug().
				Str("db_name", dbName).
				Msg("WAL file not found (likely checkpointed)")
			http.Error(w, "wal not found", http.StatusNotFound)
			return
		}

		h.logger.Warn().
			Str("db_name", dbName).
			Str("path", filePath).
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
	http.ServeFile(w, r, filePath)
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
