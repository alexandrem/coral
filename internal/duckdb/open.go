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

	db := sql.OpenDB(connector)
	// DuckDB does not support concurrent writes well, and standard database/sql parallel
	// connections will result in 'TransactionContext Error: Conflict on update' errors
	// and lock contention. Restricting to a single connection prevents these issues.
	db.SetMaxOpenConns(1)

	return db, nil
}
