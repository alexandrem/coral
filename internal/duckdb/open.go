package duckdb

import (
	"database/sql"
	"database/sql/driver"

	duckdbDriver "github.com/marcboeker/go-duckdb"
)

// var (
// 	installMu sync.Mutex
// 	preinstallVSSOnce sync.Once
// )

// OpenDB opens a DuckDB database.
// Vector similarity search relies on DuckDB's native array_cosine_similarity,
// avoiding the need for the VSS extension and its associated HNSW WAL replay issues.
func OpenDB(dsn string) (*sql.DB, error) {

	connector, err := duckdbDriver.NewConnector(dsn, func(execer driver.ExecerContext) error {
		return nil
	})
	if err != nil {
		return nil, err
	}

	return sql.OpenDB(connector), nil
}
