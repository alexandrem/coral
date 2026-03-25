package duckdb

import (
	"path/filepath"
	"testing"
)

func TestOpenDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.duckdb")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}

	if err := db.Ping(); err != nil {
		t.Fatalf("Failed to ping database: %v", err)
	}

	_ = db.Close()
}
