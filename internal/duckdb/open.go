package duckdb

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"

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

// IsFatal reports whether err is a DuckDB fatal error. A fatal error
// invalidates the database: every later statement fails until the database is
// closed and opened again.
func IsFatal(err error) bool {
	var dbErr *duckdbDriver.Error
	return errors.As(err, &dbErr) && dbErr.Type == duckdbDriver.ErrorTypeFatal
}

// RebuildIndexes drops every secondary index in db, checkpoints, and creates
// the indexes again from their stored definitions.
//
// It repairs databases whose persisted ART indexes make CHECKPOINT fail, for
// example with "Invalid node type for TransformToDeprecated". Indexes backing
// PRIMARY KEY and UNIQUE constraints cannot be dropped and are left in place.
func RebuildIndexes(db *sql.DB) error {
	type index struct {
		schema, name, sql string
	}

	rows, err := db.Query(`SELECT schema_name, index_name, sql FROM duckdb_indexes() WHERE sql IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("failed to list indexes: %w", err)
	}
	var indexes []index
	for rows.Next() {
		var idx index
		if err := rows.Scan(&idx.schema, &idx.name, &idx.sql); err != nil {
			_ = rows.Close()
			return fmt.Errorf("failed to scan index: %w", err)
		}
		indexes = append(indexes, idx)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to list indexes: %w", err)
	}

	for _, idx := range indexes {
		if _, err := db.Exec("DROP INDEX " + quoteIdent(idx.schema) + "." + quoteIdent(idx.name)); err != nil {
			return fmt.Errorf("failed to drop index %s: %w", idx.name, err)
		}
	}

	if _, err := db.Exec("CHECKPOINT"); err != nil {
		return fmt.Errorf("failed to checkpoint without secondary indexes: %w", err)
	}

	for _, idx := range indexes {
		if _, err := db.Exec(idx.sql); err != nil {
			return fmt.Errorf("failed to recreate index %s: %w", idx.name, err)
		}
	}

	if _, err := db.Exec("CHECKPOINT"); err != nil {
		return fmt.Errorf("failed to checkpoint rebuilt indexes: %w", err)
	}

	return nil
}
