package duckdb

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
)

// TestVortexHandler_E2E_FullExport exercises the full handler pipeline:
// real DuckDB database → VortexHandler HTTP server → client receives .vx bytes.
//
// The test is skipped when the Vortex community extension is not available on
// the test platform (e.g. macOS arm64 with DuckDB 1.x).
func TestVortexHandler_E2E_FullExport(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.duckdb")

	// Create a real DuckDB file with a small table.
	if err := createTestDuckDB(t, dbPath); err != nil {
		t.Fatalf("Failed to create test DuckDB: %v", err)
	}

	// Verify the Vortex extension is available; skip if not.
	if !vortexExtensionAvailable(t, dbPath) {
		t.Skip("Vortex DuckDB community extension not available on this platform")
	}

	logger := zerolog.Nop()
	handler := NewVortexHandler(true, 0, logger)
	if err := handler.RegisterDatabase("testdb", dbPath); err != nil {
		t.Fatalf("RegisterDatabase: %v", err)
	}

	// Start test HTTP server.
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Request table export.
	resp, err := http.Get(srv.URL + "/vortex/testdb/metrics")
	if err != nil {
		t.Fatalf("GET /vortex/testdb/metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected 200, got %d: %s", resp.StatusCode, body)
	}

	// Read response body.
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	if len(data) == 0 {
		t.Error("Expected non-empty Vortex file bytes, got empty response")
	}

	t.Logf("Received %d bytes of Vortex data", len(data))
}

// TestVortexHandler_E2E_QueryExport exercises the ?query= endpoint.
func TestVortexHandler_E2E_QueryExport(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.duckdb")

	if err := createTestDuckDB(t, dbPath); err != nil {
		t.Fatalf("Failed to create test DuckDB: %v", err)
	}

	if !vortexExtensionAvailable(t, dbPath) {
		t.Skip("Vortex DuckDB community extension not available on this platform")
	}

	logger := zerolog.Nop()
	handler := NewVortexHandler(true, 0, logger)
	if err := handler.RegisterDatabase("testdb", dbPath); err != nil {
		t.Fatalf("RegisterDatabase: %v", err)
	}

	srv := httptest.NewServer(handler)
	defer srv.Close()

	reqURL := srv.URL + "/vortex/testdb?query=SELECT+%2A+FROM+metrics+WHERE+value+%3E+0"
	resp, err := http.Get(reqURL)
	if err != nil {
		t.Fatalf("GET with query: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected 200, got %d: %s", resp.StatusCode, body)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	if len(data) == 0 {
		t.Error("Expected non-empty Vortex file bytes, got empty response")
	}
}

// TestVortexHandler_E2E_DiskThresholdBlocks verifies that a zero disk threshold
// causes the handler to reject the export with 413.
func TestVortexHandler_E2E_DiskThresholdBlocks(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.duckdb")

	// Write a non-empty source database so projected size > 0.
	if err := os.WriteFile(dbPath, make([]byte, 4096), 0644); err != nil {
		t.Fatal(err)
	}

	logger := zerolog.Nop()
	// diskThreshold = 0.000001 means almost any write would exceed available space.
	handler := NewVortexHandler(true, 0.000001, logger)
	if err := handler.RegisterDatabase("testdb", dbPath); err != nil {
		t.Fatalf("RegisterDatabase: %v", err)
	}

	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/vortex/testdb/sometable")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("Expected 413 when disk threshold exceeded, got %d", resp.StatusCode)
	}
}

// createTestDuckDB creates a real DuckDB file with a small `metrics` table.
func createTestDuckDB(t *testing.T, dbPath string) error {
	t.Helper()
	db, err := OpenDB(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE metrics (
			ts    TIMESTAMP,
			name  VARCHAR,
			value DOUBLE
		)
	`); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO metrics VALUES
			(now(), 'http_requests', 42.0),
			(now(), 'latency_p99',  123.5),
			(now(), 'error_rate',   0.01)
	`); err != nil {
		return err
	}
	return nil
}

// vortexExtensionAvailable attempts to install and load the Vortex community
// extension against the provided DuckDB file. Returns false if unavailable.
func vortexExtensionAvailable(t *testing.T, dbPath string) bool {
	t.Helper()
	db, err := sql.Open("duckdb", dbPath+"?access_mode=read_only")
	if err != nil {
		return false
	}
	defer db.Close()

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "INSTALL vortex FROM community"); err != nil {
		t.Logf("Vortex extension unavailable (INSTALL failed): %v", err)
		return false
	}
	if _, err := db.ExecContext(ctx, "LOAD vortex"); err != nil {
		t.Logf("Vortex extension unavailable (LOAD failed): %v", err)
		return false
	}
	return true
}
