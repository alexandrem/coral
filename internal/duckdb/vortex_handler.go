package duckdb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	duckdbDriver "github.com/marcboeker/go-duckdb"
	"github.com/rs/zerolog"
	"golang.org/x/sys/unix"
)

const (
	// vortexDefaultDiskThreshold is the maximum allowed temp-dir utilization
	// before the handler refuses to write a Vortex export (prevents ENOSPC).
	vortexDefaultDiskThreshold = 0.80
)

// VortexHandler exports DuckDB query results as Vortex (.vx) files (RFD 097).
//
// Routes:
//
//	GET /vortex/<db>/<table>        — full table export
//	GET /vortex/<db>?query=<sql>    — custom SQL export
type VortexHandler struct {
	// databases is the allowlist of registered database names to file paths.
	databases     map[string]string
	enabled       bool
	diskThreshold float64
	logger        zerolog.Logger
}

// NewVortexHandler creates a VortexHandler.
// enabled controls whether the /vortex endpoint is active.
// diskThreshold is a fraction in [0, 1]; exports that would push temp-dir
// utilization above this value are rejected with 413.
func NewVortexHandler(enabled bool, diskThreshold float64, logger zerolog.Logger) *VortexHandler {
	if diskThreshold <= 0 || diskThreshold > 1 {
		diskThreshold = vortexDefaultDiskThreshold
	}
	return &VortexHandler{
		databases:     make(map[string]string),
		enabled:       enabled,
		diskThreshold: diskThreshold,
		logger:        logger.With().Str("component", "vortex_handler").Logger(),
	}
}

// RegisterDatabase adds a database to the allowlist for Vortex export.
// name is the URL path component (e.g. "beyla"); filePath is the absolute path
// to the DuckDB file on disk.
func (h *VortexHandler) RegisterDatabase(name string, filePath string) error {
	if _, err := os.Stat(filePath); err != nil {
		return fmt.Errorf("database file not found: %w", err)
	}
	name = filepath.Base(name)
	h.databases[name] = filePath
	h.logger.Info().
		Str("name", name).
		Str("path", filePath).
		Msg("Registered database for Vortex export.")
	return nil
}

// ServeHTTP handles GET /vortex/<db>/<table> and GET /vortex/<db>?query=<sql>.
func (h *VortexHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.enabled {
		http.Error(w, "Vortex extension not enabled on this agent", http.StatusNotImplemented)
		return
	}

	// Parse path: /vortex/<db>[/<table>]
	trimmed := strings.Trim(r.URL.Path, "/")
	parts := strings.SplitN(trimmed, "/", 3)
	if len(parts) < 2 || parts[0] != "vortex" {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	dbName := parts[1]
	if !isValidIdentifier(dbName) {
		http.Error(w, "invalid database name", http.StatusBadRequest)
		return
	}

	filePath, ok := h.databases[dbName]
	if !ok {
		http.Error(w, "database not registered", http.StatusNotFound)
		return
	}

	// Determine the query to export.
	var query, outFilename string

	if len(parts) == 3 && parts[2] != "" {
		// Table export: /vortex/<db>/<table>
		tableName := parts[2]
		if !isValidIdentifier(tableName) {
			http.Error(w, "invalid table name", http.StatusBadRequest)
			return
		}
		query = fmt.Sprintf("SELECT * FROM %s", tableName)
		outFilename = tableName + ".vx"
	} else {
		// Custom SQL export: /vortex/<db>?query=<sql>
		rawQuery := r.URL.Query().Get("query")
		if rawQuery == "" {
			http.Error(w, "missing table name or ?query= parameter", http.StatusBadRequest)
			return
		}
		if !isSelectQuery(rawQuery) {
			http.Error(w, "only SELECT queries are allowed", http.StatusBadRequest)
			return
		}
		query = rawQuery
		outFilename = "query-export.vx"
	}

	h.serveVortexExport(w, r, filePath, query, outFilename)
}

// serveVortexExport opens the database, runs COPY to a temp file, checks disk
// safety, then streams the result to the client.
func (h *VortexHandler) serveVortexExport(w http.ResponseWriter, r *http.Request, dbFilePath, query, filename string) {
	// Create a temp file path for the Vortex output.
	tmpFile, err := os.CreateTemp("", "coral-vortex-*.vx")
	if err != nil {
		h.logger.Error().Err(err).Msg("Failed to create temp file for Vortex export.")
		http.Error(w, "failed to create temp file", http.StatusInternalServerError)
		return
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	// Always remove the temp file when done.
	defer func() { _ = os.Remove(tmpPath) }()

	// Pre-flight: estimate output size and check available disk space.
	// Use the size of the source database file as a conservative upper bound.
	sourceInfo, err := os.Stat(dbFilePath)
	if err != nil {
		h.logger.Error().Err(err).Str("path", dbFilePath).Msg("Failed to stat source database.")
		http.Error(w, "database file unavailable", http.StatusInternalServerError)
		return
	}
	projectedBytes := sourceInfo.Size()

	tmpDir := os.TempDir()
	availableBytes, totalBytes, diskErr := vortexDiskSpace(tmpDir)
	if diskErr != nil {
		// Non-fatal: skip disk check if we can't stat the filesystem.
		h.logger.Warn().Err(diskErr).Str("dir", tmpDir).Msg("Unable to check disk space; proceeding with export.")
	} else {
		// Check if writing projectedBytes would exceed the threshold.
		used := totalBytes - availableBytes
		usedAfter := float64(used+uint64(projectedBytes)) / float64(totalBytes)
		if usedAfter > h.diskThreshold {
			h.logger.Warn().
				Int64("projected_bytes", projectedBytes).
				Uint64("available_bytes", availableBytes).
				Float64("threshold", h.diskThreshold).
				Msg("Refusing Vortex export: projected utilization exceeds disk threshold.")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"available_bytes": availableBytes,
				"projected_bytes": projectedBytes,
			})
			return
		}
	}

	// Open the DuckDB file, load the Vortex community extension, and run COPY.
	if err := h.runVortexCopy(r.Context(), dbFilePath, query, tmpPath); err != nil {
		h.logger.Error().Err(err).Msg("Vortex COPY failed.")
		errMsg := err.Error()
		if strings.Contains(errMsg, "vortex") || strings.Contains(errMsg, "Extension") || strings.Contains(errMsg, "extension") {
			http.Error(w, "Vortex extension unavailable on this agent", http.StatusNotImplemented)
		} else {
			http.Error(w, "export failed: "+errMsg, http.StatusInternalServerError)
		}
		return
	}

	// Stream the resulting file.
	f, err := os.Open(tmpPath) //nolint:gosec // tmpPath is generated by os.CreateTemp, not user-controlled
	if err != nil {
		h.logger.Error().Err(err).Str("path", tmpPath).Msg("Failed to open Vortex output file.")
		http.Error(w, "failed to read export", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		h.logger.Error().Err(err).Msg("Failed to stat Vortex output file.")
		http.Error(w, "failed to stat export", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	h.logger.Debug().
		Str("filename", filename).
		Int64("size_bytes", info.Size()).
		Str("remote_addr", r.RemoteAddr).
		Msg("Streaming Vortex export.")

	http.ServeContent(w, r, filename, info.ModTime(), f)
}

// runVortexCopy opens the DuckDB file read-only, installs and loads the
// community Vortex extension, then runs COPY ... TO tmpPath (FORMAT vortex).
func (h *VortexHandler) runVortexCopy(ctx context.Context, dbFilePath, query, tmpPath string) error {
	// Open DuckDB read-only; we only read and export to a local temp file.
	connector, err := duckdbDriver.NewConnector(dbFilePath+"?access_mode=read_only", func(_ driver.ExecerContext) error {
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to open DuckDB: %w", err)
	}

	db := sql.OpenDB(connector)
	defer func() { _ = db.Close() }()

	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// Install and load the Vortex community extension.
	if _, err := conn.ExecContext(ctx, "INSTALL vortex FROM community"); err != nil {
		return fmt.Errorf("INSTALL vortex FROM community failed: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "LOAD vortex"); err != nil {
		return fmt.Errorf("LOAD vortex failed: %w", err)
	}

	// Run COPY to the temp file.
	copySQL := fmt.Sprintf("COPY (%s) TO '%s' (FORMAT vortex)", query, tmpPath)
	if _, err := conn.ExecContext(ctx, copySQL); err != nil {
		return fmt.Errorf("COPY failed: %w", err)
	}

	return nil
}

// vortexDiskSpace returns available and total bytes for the filesystem hosting dir.
func vortexDiskSpace(dir string) (available, total uint64, err error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		return 0, 0, err
	}
	// Bavail is blocks available to unprivileged processes; Blocks is total.
	available = stat.Bavail * uint64(stat.Bsize) //nolint:gosec
	total = stat.Blocks * uint64(stat.Bsize)     //nolint:gosec
	return available, total, nil
}

// isValidIdentifier returns true if s is a safe SQL identifier:
// non-empty, starts with a letter or underscore, and contains only
// letters, digits, and underscores.
func isValidIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return false
			}
		} else {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
				return false
			}
		}
	}
	return true
}

// isSelectQuery returns true if query (after trimming) starts with SELECT (case-insensitive).
// This prevents write operations through the ?query= parameter.
func isSelectQuery(query string) bool {
	trimmed := strings.TrimLeftFunc(query, unicode.IsSpace)
	return len(trimmed) >= 6 && strings.EqualFold(trimmed[:6], "select")
}
