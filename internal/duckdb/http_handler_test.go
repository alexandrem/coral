package duckdb

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
)

func TestDuckDBHandler_ServeFile_Success(t *testing.T) {
	// Create temporary database file for testing.
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.duckdb")
	testData := []byte("mock duckdb data")
	if err := os.WriteFile(dbPath, testData, 0644); err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	// Create handler and register database.
	logger := zerolog.Nop()
	handler := NewDuckDBHandler(logger)
	if err := handler.RegisterDatabase("test.duckdb", dbPath); err != nil {
		t.Fatalf("Failed to register database: %v", err)
	}

	// Create test request.
	req := httptest.NewRequest(http.MethodGet, "/duckdb/test.duckdb", nil)
	w := httptest.NewRecorder()

	// Serve request.
	handler.ServeHTTP(w, req)

	// Verify response.
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	if w.Header().Get("Content-Type") != "application/octet-stream" {
		t.Errorf("Expected Content-Type application/octet-stream, got %s", w.Header().Get("Content-Type"))
	}

	if w.Header().Get("Accept-Ranges") != "bytes" {
		t.Errorf("Expected Accept-Ranges bytes, got %s", w.Header().Get("Accept-Ranges"))
	}

	if w.Body.String() != string(testData) {
		t.Errorf("Expected body %q, got %q", string(testData), w.Body.String())
	}
}

func TestDuckDBHandler_NotFound(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewDuckDBHandler(logger)

	// Create test request for non-existent database.
	req := httptest.NewRequest(http.MethodGet, "/duckdb/nonexistent.duckdb", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

func TestDuckDBHandler_MethodNotAllowed(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewDuckDBHandler(logger)

	methods := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/duckdb/test.duckdb", nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("Expected status 405 for %s, got %d", method, w.Code)
			}
		})
	}
}

func TestDuckDBHandler_NoDirectoryTraversal(t *testing.T) {
	// Create temporary database file for testing.
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.duckdb")
	if err := os.WriteFile(dbPath, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	logger := zerolog.Nop()
	handler := NewDuckDBHandler(logger)
	if err := handler.RegisterDatabase("test.duckdb", dbPath); err != nil {
		t.Fatalf("Failed to register database: %v", err)
	}

	// Try directory traversal attacks.
	traversalPaths := []string{
		"/duckdb/../../../etc/passwd",
		"/duckdb/../../test.duckdb",
		"/duckdb/.../test.duckdb",
		"/duckdb/subdir/../test.duckdb",
	}

	for _, path := range traversalPaths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			// Should return 404 (not found) because path is sanitized.
			if w.Code != http.StatusNotFound {
				t.Errorf("Expected status 404 for path %s, got %d", path, w.Code)
			}
		})
	}
}

func TestDuckDBHandler_RangeRequests(t *testing.T) {
	// Create temporary database file with known content.
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.duckdb")
	testData := []byte("0123456789abcdefghij") // 20 bytes
	if err := os.WriteFile(dbPath, testData, 0644); err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	logger := zerolog.Nop()
	handler := NewDuckDBHandler(logger)
	if err := handler.RegisterDatabase("test.duckdb", dbPath); err != nil {
		t.Fatalf("Failed to register database: %v", err)
	}

	// Test range request (bytes 0-9).
	req := httptest.NewRequest(http.MethodGet, "/duckdb/test.duckdb", nil)
	req.Header.Set("Range", "bytes=0-9")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// http.ServeFile should handle range requests and return 206 Partial Content.
	if w.Code != http.StatusPartialContent {
		t.Errorf("Expected status 206, got %d", w.Code)
	}

	if w.Body.String() != "0123456789" {
		t.Errorf("Expected body %q, got %q", "0123456789", w.Body.String())
	}
}

func TestDuckDBHandler_RegisterDatabase_FileNotFound(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewDuckDBHandler(logger)

	err := handler.RegisterDatabase("test.duckdb", "/nonexistent/path/test.duckdb")
	if err == nil {
		t.Error("Expected error when registering non-existent file, got nil")
	}
}

func TestDuckDBHandler_InvalidPath(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewDuckDBHandler(logger)

	// Test invalid paths.
	// Note: /duckdb and /duckdb/ are now valid (list databases endpoint).
	invalidPaths := []string{
		"/",
		"/other/path",
	}

	for _, path := range invalidPaths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
				t.Errorf("Expected status 400 or 404 for path %s, got %d", path, w.Code)
			}
		})
	}
}

func TestDuckDBHandler_ListDatabases(t *testing.T) {
	// Create temporary database files for testing.
	tmpDir := t.TempDir()
	db1Path := filepath.Join(tmpDir, "test1.duckdb")
	db2Path := filepath.Join(tmpDir, "test2.duckdb")
	if err := os.WriteFile(db1Path, []byte("db1"), 0644); err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	if err := os.WriteFile(db2Path, []byte("db2"), 0644); err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	// Create handler and register databases.
	logger := zerolog.Nop()
	handler := NewDuckDBHandler(logger)
	if err := handler.RegisterDatabase("test1.duckdb", db1Path); err != nil {
		t.Fatalf("Failed to register database: %v", err)
	}
	if err := handler.RegisterDatabase("test2.duckdb", db2Path); err != nil {
		t.Fatalf("Failed to register database: %v", err)
	}

	// Test list databases endpoint.
	req := httptest.NewRequest(http.MethodGet, "/duckdb", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Verify response.
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Expected Content-Type application/json, got %s", w.Header().Get("Content-Type"))
	}

	// Verify JSON response contains database names.
	body := w.Body.String()
	if !contains(body, "test1.duckdb") {
		t.Errorf("Expected response to contain 'test1.duckdb', got %s", body)
	}
	if !contains(body, "test2.duckdb") {
		t.Errorf("Expected response to contain 'test2.duckdb', got %s", body)
	}
}

// Helper function to check if string contains substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
