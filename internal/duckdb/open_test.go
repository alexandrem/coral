package duckdb

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	duckdbDriver "github.com/marcboeker/go-duckdb"
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

func TestIsFatal(t *testing.T) {
	fatal := &duckdbDriver.Error{Type: duckdbDriver.ErrorTypeFatal, Msg: "FATAL Error: Failed to create checkpoint"}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "plain error", err: errors.New("boom"), want: false},
		{name: "non-fatal duckdb error", err: &duckdbDriver.Error{Type: duckdbDriver.ErrorTypeCatalog, Msg: "Catalog Error"}, want: false},
		{name: "fatal duckdb error", err: fatal, want: true},
		{name: "wrapped fatal duckdb error", err: fmt.Errorf("checkpoint: %w", fatal), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsFatal(tt.err); got != tt.want {
				t.Errorf("IsFatal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRebuildIndexes(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "test.duckdb"))
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	setup := `
		CREATE TABLE spans (id UBIGINT PRIMARY KEY, service VARCHAR, ts TIMESTAMP);
		CREATE INDEX idx_spans_service ON spans(service, ts);
		CREATE INDEX idx_spans_ts ON spans(ts);
		INSERT INTO spans SELECT i, 'svc-' || (i % 5), TIMESTAMP '2026-01-01' + INTERVAL (i) SECOND FROM range(1000) t(i);
	`
	if _, err := db.Exec(setup); err != nil {
		t.Fatalf("Failed to set up table: %v", err)
	}

	if err := RebuildIndexes(db); err != nil {
		t.Fatalf("RebuildIndexes() error = %v", err)
	}

	var indexes int
	if err := db.QueryRow(`SELECT count(*) FROM duckdb_indexes() WHERE table_name = 'spans'`).Scan(&indexes); err != nil {
		t.Fatalf("Failed to count indexes: %v", err)
	}
	if indexes != 2 {
		t.Errorf("index count = %d, want 2", indexes)
	}

	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM spans WHERE service = 'svc-1'`).Scan(&rows); err != nil {
		t.Fatalf("Failed to count rows: %v", err)
	}
	if rows != 200 {
		t.Errorf("row count = %d, want 200", rows)
	}

	// The primary key index must still be enforced.
	if _, err := db.Exec(`INSERT INTO spans VALUES (1, 'dup', NULL)`); err == nil {
		t.Error("duplicate primary key insert succeeded, want constraint error")
	}
}
