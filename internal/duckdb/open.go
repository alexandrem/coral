package duckdb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"net/url"
	"strings"

	duckdbDriver "github.com/marcboeker/go-duckdb"
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
		bootQueries := []string{
			"LOAD vss",
			"SET hnsw_enable_experimental_persistence = true",
		}
		for _, query := range bootQueries {
			if _, err := execer.ExecContext(ctx, query, nil); err != nil {
				// Non-fatal: vector search features may be unavailable
				// (e.g. in-memory DBs without network access for extension install).
				return nil
			}
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
	// Handle empty DSN (in-memory database).
	if dsn == "" || dsn == ":memory:" {
		return dsn
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
