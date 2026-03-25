package duckdb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"net/url"
	"strings"
	"sync"
	"time"

	duckdbDriver "github.com/marcboeker/go-duckdb"
)

var (
	// installMu serializes all INSTALL calls (process-wide) to prevent
	// concurrent downloads that can corrupt the extension cache.
	installMu sync.Mutex

	// preinstallVSSOnce ensures the global VSS installation (which populates
	// the extension directory) happens at most once, even across concurrent
	// OpenDB calls for different database files.
	preinstallVSSOnce sync.Once
)

// OpenDB opens a DuckDB database with robust VSS (HNSW) support.
//
// It guarantees:
//   - VSS is pre-installed before opening any disk database that already exists
//     (critical for WAL replay of HNSW indexes).
//   - autoinstall + autoload are forced on the DSN so the extension is available
//     during duckdb_open_ext / WAL replay.
//   - Every pooled connection explicitly LOADs VSS (with retry) and enables
//     experimental persistence.
//
// VSS loading is non-fatal; the database opens successfully even if the
// extension is unavailable (e.g. offline, permission issues).
func OpenDB(dsn string) (*sql.DB, error) {
	dsn = injectAutoloadConfig(dsn)

	// Pre-install VSS once per process for any existing disk database.
	// This populates the extension cache before WAL replay occurs.
	if isDiskDatabase(dsn) {
		preinstallVSSOnce.Do(preinstallVSS)
	}

	connector, err := duckdbDriver.NewConnector(dsn, func(execer driver.ExecerContext) error {
		ctx := context.Background()

		// INSTALL is idempotent and serialized process-wide.
		func() {
			installMu.Lock()
			defer installMu.Unlock()
			_, _ = execer.ExecContext(ctx, "INSTALL vss", nil)
		}()

		// LOAD with retry to survive transient races after INSTALL.
		var loadErr error
		for attempt := 1; attempt <= 3; attempt++ {
			if _, err := execer.ExecContext(ctx, "LOAD vss", nil); err == nil {
				loadErr = nil
				break
			} else {
				loadErr = err
				time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)
			}
		}

		// Only enable experimental persistence when VSS actually loaded.
		if loadErr == nil {
			_, _ = execer.ExecContext(ctx, "SET hnsw_enable_experimental_persistence = true", nil)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return sql.OpenDB(connector), nil
}

// preinstallVSS forces a one-time global installation of the VSS extension
// using a throw-away in-memory connection. Errors are ignored (VSS is optional).
func preinstallVSS() {
	installMu.Lock()
	defer installMu.Unlock()

	tempConnector, err := duckdbDriver.NewConnector(":memory:", nil)
	if err != nil {
		return
	}
	tempDB := sql.OpenDB(tempConnector)
	defer tempDB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _ = tempDB.ExecContext(ctx, "INSTALL vss")
}

// isDiskDatabase returns true for file-based DSNs (not :memory:).
func isDiskDatabase(dsn string) bool {
	if dsn == "" || dsn == ":memory:" || strings.HasPrefix(dsn, ":memory:") {
		return false
	}
	return true
}

// injectAutoloadConfig forces the two flags required for safe VSS usage
// (including during WAL replay). It always overrides them to true.
func injectAutoloadConfig(dsn string) string {
	if dsn == "" || dsn == ":memory:" {
		return ":memory:?autoinstall_known_extensions=true&autoload_known_extensions=true"
	}

	sep := strings.IndexByte(dsn, '?')
	path := dsn
	query := ""
	if sep >= 0 {
		path = dsn[:sep]
		query = dsn[sep+1:]
	}

	params, err := url.ParseQuery(query)
	if err != nil {
		// Malformed query is rare; return original DSN unchanged.
		return dsn
	}

	// Set the flags only when the caller has not already specified them,
	// so that explicit user overrides (e.g. autoload=false) are preserved.
	if params.Get("autoinstall_known_extensions") == "" {
		params.Set("autoinstall_known_extensions", "true")
	}
	if params.Get("autoload_known_extensions") == "" {
		params.Set("autoload_known_extensions", "true")
	}

	return path + "?" + params.Encode()
}
