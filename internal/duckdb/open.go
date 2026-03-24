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
	// installMu serializes extension installation within the process to avoid
	// race conditions where multiple connections try to download/install the
	// same extension simultaneously, which can lead to corruption or
	// "Unknown index type" errors in DuckDB.
	installMu sync.Mutex
)

// OpenDB opens a DuckDB database with autoloading of known extensions enabled.
// This ensures the VSS extension (providing HNSW indexes) is automatically
// loaded during WAL replay when the database file is opened, preventing
// "unknown index type 'HNSW'" errors.
//
// Additionally, a per-connection init function loads VSS and enables
// experimental HNSW persistence on every pooled connection.
func OpenDB(dsn string) (*sql.DB, error) {
	// Inject autoinstall/autoload into the DSN so DuckDB loads the VSS
	// extension during database open (WAL replay), before any connection
	// is created. This is the only way to handle HNSW indexes in WAL files
	// since WAL replay happens inside duckdb_open_ext.
	dsn = injectAutoloadConfig(dsn)

	connector, err := duckdbDriver.NewConnector(dsn, func(execer driver.ExecerContext) error {
		ctx := context.Background()

		// Install VSS once with process-wide serialization to prevent concurrent
		// downloads or file corruption in CI. Errors are ignored — the extension
		// may already be installed, and LOAD below will surface any real failure.
		func() {
			installMu.Lock()
			defer installMu.Unlock()
			_, _ = execer.ExecContext(ctx, "INSTALL vss", nil)
		}()

		// Retry LOAD to handle transient file-lock or network races after install.
		var loadErr error
		for attempt := 1; attempt <= 3; attempt++ {
			if _, err := execer.ExecContext(ctx, "LOAD vss", nil); err == nil {
				break
			} else {
				loadErr = err
				time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)
			}
		}

		// VSS is non-fatal: the caller degrades gracefully without HNSW support.
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

// injectAutoloadConfig adds autoinstall_known_extensions and
// autoload_known_extensions to the DSN query parameters if not already set.
func injectAutoloadConfig(dsn string) string {
	// Handle in-memory database (empty or explicit ":memory:").
	if dsn == "" || dsn == ":memory:" {
		return ":memory:?autoinstall_known_extensions=true&autoload_known_extensions=true"
	}

	// Split path from query string.
	sep := strings.IndexByte(dsn, '?')
	path := dsn
	query := ""
	if sep >= 0 {
		path = dsn[:sep]
		query = dsn[sep+1:]
	}

	params, err := url.ParseQuery(query)
	if err != nil {
		// If we can't parse, return original DSN unchanged.
		return dsn
	}

	if !params.Has("autoinstall_known_extensions") {
		params.Set("autoinstall_known_extensions", "true")
	}
	if !params.Has("autoload_known_extensions") {
		params.Set("autoload_known_extensions", "true")
	}

	return path + "?" + params.Encode()
}
