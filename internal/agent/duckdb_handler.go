package agent

import (
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
// Handles GET requests to /duckdb/<filename>.
func (h *DuckDBHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only allow GET requests (read-only).
	if r.Method != http.MethodGet {
		h.logger.Warn().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Msg("Rejected non-GET request to DuckDB endpoint")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract filename from URL path.
	// Expected format: /duckdb/<filename>
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 2 || pathParts[0] != "duckdb" {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	// Get requested database name (filename only, no directory components).
	dbName := filepath.Base(pathParts[1])

	// Check if database is registered (allowlist check).
	filePath, ok := h.databases[dbName]
	if !ok {
		h.logger.Debug().
			Str("db_name", dbName).
			Msg("Database not found in registry")
		http.Error(w, "database not found", http.StatusNotFound)
		return
	}

	// Verify file still exists before serving.
	if _, err := os.Stat(filePath); err != nil {
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
