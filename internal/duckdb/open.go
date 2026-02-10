package duckdb

import (
	"context"
	"database/sql"
	"database/sql/driver"

	duckdbDriver "github.com/marcboeker/go-duckdb"
)

// OpenDB opens a DuckDB database using a Connector that loads the VSS extension
// on every new connection. This ensures HNSW indexes in WAL files are recognized
// during replay, regardless of which pooled connection handles it.
//
// The connInitFn runs before any connection is returned to the pool, so
// extensions are always available before WAL replay or query execution.
func OpenDB(dsn string) (*sql.DB, error) {
	connector, err := duckdbDriver.NewConnector(dsn, func(execer driver.ExecerContext) error {
		ctx := context.Background()
		bootQueries := []string{
			"INSTALL vss FROM core",
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
