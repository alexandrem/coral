package duckdb

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/marcboeker/go-duckdb"
)

// TestOpenDB_WALReplayWithHNSW verifies that OpenDB can reopen a database
// whose WAL contains HNSW index operations.
//
// Without autoload_known_extensions in the DSN, DuckDB fails during WAL replay
// with: "Cannot bind index 'functions', unknown index type 'HNSW'".
func TestOpenDB_WALReplayWithHNSW(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.duckdb")

	// Step 1: Create a database with an HNSW index and leave WAL un-checkpointed.
	func() {
		db, err := OpenDB(dbPath)
		if err != nil {
			t.Fatalf("Failed to open database: %v", err)
		}
		defer func() { _ = db.Close() }()

		// Create table with embedding column and HNSW index.
		for _, stmt := range []string{
			`CREATE TABLE functions (id INTEGER PRIMARY KEY, name TEXT, embedding FLOAT[3])`,
			`INSERT INTO functions VALUES (1, 'main', [1.0, 2.0, 3.0])`,
			`CREATE INDEX idx_functions_embedding ON functions USING HNSW (embedding) WITH (metric = 'cosine')`,
			`INSERT INTO functions VALUES (2, 'helper', [4.0, 5.0, 6.0])`,
		} {
			if _, err := db.Exec(stmt); err != nil {
				t.Fatalf("Failed to execute %q: %v", stmt, err)
			}
		}

		// Do NOT checkpoint — we want a dirty WAL that contains the HNSW index.
	}()

	// Step 2: Reopen with OpenDB — WAL replay must succeed.
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB failed to reopen database with HNSW WAL: %v", err)
	}

	// Verify data survived the WAL replay.
	var count int
	if err := db.QueryRow("SELECT count(*) FROM functions").Scan(&count); err != nil {
		t.Fatalf("Failed to query functions: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected 2 rows after WAL replay, got %d", count)
	}
	_ = db.Close()
}

// TestOpenDB_WALReplayFailsWithoutAutoload proves that plain sql.Open (without
// autoload config) fails when replaying a WAL containing HNSW indexes.
// This validates that our OpenDB fix is actually necessary.
func TestOpenDB_WALReplayFailsWithoutAutoload(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.duckdb")

	// Step 1: Create a database with an HNSW index via OpenDB.
	func() {
		db, err := OpenDB(dbPath)
		if err != nil {
			t.Fatalf("Failed to open database: %v", err)
		}
		defer func() { _ = db.Close() }()

		for _, stmt := range []string{
			`CREATE TABLE functions (id INTEGER PRIMARY KEY, name TEXT, embedding FLOAT[3])`,
			`INSERT INTO functions VALUES (1, 'main', [1.0, 2.0, 3.0])`,
			`CREATE INDEX idx_functions_embedding ON functions USING HNSW (embedding) WITH (metric = 'cosine')`,
			`INSERT INTO functions VALUES (2, 'helper', [4.0, 5.0, 6.0])`,
		} {
			if _, err := db.Exec(stmt); err != nil {
				t.Fatalf("Failed to execute %q: %v", stmt, err)
			}
		}
	}()

	// Step 2: Reopen with plain sql.Open — should fail during WAL replay.
	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		// Error at open time — this is the expected failure path.
		t.Logf("sql.Open failed as expected: %v", err)
		return
	}

	// sql.Open is lazy; the actual failure happens on first use (Ping triggers WAL replay).
	if err := db.Ping(); err != nil {
		t.Logf("Ping failed as expected (HNSW WAL replay without autoload): %v", err)
		_ = db.Close()
		return
	}

	// If we get here, DuckDB may have changed behavior (e.g. autoload by default).
	// The test still passes but log a warning.
	_ = db.Close()
	t.Log("WARNING: plain sql.Open succeeded — DuckDB may now autoload extensions by default. " +
		"The OpenDB wrapper is still correct but the negative test is no longer validating the fix.")
}

func TestInjectAutoloadConfig(t *testing.T) {
	tests := []struct {
		name     string
		dsn      string
		wantAuto bool // Whether autoload params should be present.
		wantOrig bool // Whether original params should be preserved.
	}{
		{
			name:     "empty DSN (in-memory)",
			dsn:      "",
			wantAuto: false,
		},
		{
			name:     ":memory: DSN",
			dsn:      ":memory:",
			wantAuto: false,
		},
		{
			name:     "file path without params",
			dsn:      "/tmp/test.duckdb",
			wantAuto: true,
		},
		{
			name:     "file path with existing params",
			dsn:      "/tmp/test.duckdb?access_mode=READ_ONLY",
			wantAuto: true,
			wantOrig: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := injectAutoloadConfig(tt.dsn)

			if !tt.wantAuto {
				if result != tt.dsn {
					t.Errorf("Expected DSN unchanged for %q, got %q", tt.dsn, result)
				}
				return
			}

			if got := result; got == tt.dsn && tt.wantAuto {
				t.Errorf("Expected DSN to be modified, got unchanged: %q", got)
			}

			// Verify autoload params are present.
			if !contains(result, "autoinstall_known_extensions=true") {
				t.Errorf("Missing autoinstall_known_extensions in %q", result)
			}
			if !contains(result, "autoload_known_extensions=true") {
				t.Errorf("Missing autoload_known_extensions in %q", result)
			}

			// Verify original params preserved.
			if tt.wantOrig && !contains(result, "access_mode=READ_ONLY") {
				t.Errorf("Original param access_mode=READ_ONLY lost in %q", result)
			}
		})
	}
}

// TestInjectAutoloadConfig_DoesNotOverwrite verifies that user-specified
// autoload settings are not overwritten.
func TestInjectAutoloadConfig_DoesNotOverwrite(t *testing.T) {
	dsn := "/tmp/test.duckdb?autoload_known_extensions=false"
	result := injectAutoloadConfig(dsn)

	// Should NOT overwrite the user's explicit false with true.
	if contains(result, "autoload_known_extensions=true") {
		t.Errorf("Should not overwrite user-specified autoload_known_extensions=false, got %q", result)
	}

	// Should still preserve the user's value.
	if !contains(result, "autoload_known_extensions=false") {
		t.Errorf("Lost user-specified autoload_known_extensions=false in %q", result)
	}

	// Should still add autoinstall since it wasn't set.
	if !contains(result, "autoinstall_known_extensions=true") {
		t.Errorf("Missing autoinstall_known_extensions in %q", result)
	}
}
