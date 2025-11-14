package helpers

import (
	"database/sql"
	"fmt"
	"path/filepath"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/stretchr/testify/require"
)

// DatabaseHelper provides utilities for testing DuckDB operations.
type DatabaseHelper struct {
	t       require.TestingT
	tempDir string
	dbs     map[string]*sql.DB
}

// NewDatabaseHelper creates a new database helper.
func NewDatabaseHelper(t require.TestingT, tempDir string) *DatabaseHelper {
	return &DatabaseHelper{
		t:       t,
		tempDir: tempDir,
		dbs:     make(map[string]*sql.DB),
	}
}

// CreateDB creates a new DuckDB database.
func (dh *DatabaseHelper) CreateDB(name string) *sql.DB {
	dbPath := filepath.Join(dh.tempDir, fmt.Sprintf("%s.db", name))

	db, err := sql.Open("duckdb", dbPath)
	require.NoError(dh.t, err, "Failed to open database")
	require.NoError(dh.t, db.Ping(), "Failed to ping database")

	dh.dbs[name] = db
	return db
}

// GetDB returns a previously created database.
func (dh *DatabaseHelper) GetDB(name string) *sql.DB {
	return dh.dbs[name]
}

// QueryRow executes a query and returns a single row.
func (dh *DatabaseHelper) QueryRow(db *sql.DB, query string, args ...interface{}) *sql.Row {
	return db.QueryRow(query, args...)
}

// Exec executes a statement.
func (dh *DatabaseHelper) Exec(db *sql.DB, query string, args ...interface{}) {
	_, err := db.Exec(query, args...)
	require.NoError(dh.t, err, "Failed to execute query: %s", query)
}

// CountRows counts rows in a table.
func (dh *DatabaseHelper) CountRows(db *sql.DB, table string) int {
	var count int
	err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&count)
	require.NoError(dh.t, err, "Failed to count rows")
	return count
}

// TableExists checks if a table exists.
func (dh *DatabaseHelper) TableExists(db *sql.DB, table string) bool {
	query := `
		SELECT COUNT(*)
		FROM information_schema.tables
		WHERE table_name = ?
	`
	var count int
	err := db.QueryRow(query, table).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

// CloseAll closes all managed databases.
func (dh *DatabaseHelper) CloseAll() {
	for name, db := range dh.dbs {
		if err := db.Close(); err != nil {
			fmt.Printf("Warning: Failed to close database %s: %v\n", name, err)
		}
	}
	dh.dbs = make(map[string]*sql.DB)
}
