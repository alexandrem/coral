package duckdb

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
)

func TestVortexHandler_DisabledReturns501(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewVortexHandler(false, 0, logger)

	req := httptest.NewRequest(http.MethodGet, "/vortex/beyla/some_table", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Errorf("Expected 501 when Vortex disabled, got %d", w.Code)
	}
}

func TestVortexHandler_MethodNotAllowed(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewVortexHandler(true, 0, logger)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/vortex/beyla/some_table", nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("Expected 405 for %s, got %d", method, w.Code)
			}
		})
	}
}

func TestVortexHandler_UnregisteredDatabaseReturns404(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewVortexHandler(true, 0, logger)

	req := httptest.NewRequest(http.MethodGet, "/vortex/notregistered/some_table", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected 404 for unregistered database, got %d", w.Code)
	}
}

func TestVortexHandler_InvalidTableName(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.duckdb")
	if err := os.WriteFile(dbPath, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	logger := zerolog.Nop()
	handler := NewVortexHandler(true, 0, logger)
	if err := handler.RegisterDatabase("beyla", dbPath); err != nil {
		t.Fatal(err)
	}

	invalidTables := []string{
		"/vortex/beyla/../etc/passwd",
		"/vortex/beyla/table-with-dashes",
		"/vortex/beyla/123starts_digit",
	}

	for _, path := range invalidTables {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("Expected 400 for path %s, got %d", path, w.Code)
			}
		})
	}
}

func TestVortexHandler_InvalidDatabaseName(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewVortexHandler(true, 0, logger)

	req := httptest.NewRequest(http.MethodGet, "/vortex/../../etc/passwd", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Either 400 (invalid identifier) or 404 (not registered).
	if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
		t.Errorf("Expected 400 or 404, got %d", w.Code)
	}
}

func TestVortexHandler_QueryEndpointRequiresSelect(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.duckdb")
	if err := os.WriteFile(dbPath, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	logger := zerolog.Nop()
	handler := NewVortexHandler(true, 0, logger)
	if err := handler.RegisterDatabase("beyla", dbPath); err != nil {
		t.Fatal(err)
	}

	badQueries := []string{
		"DROP TABLE foo",
		"INSERT INTO foo VALUES (1)",
		"UPDATE foo SET bar=1",
		"DELETE FROM foo",
	}

	for _, q := range badQueries {
		t.Run(q, func(t *testing.T) {
			reqURL := "/vortex/beyla?query=" + url.QueryEscape(q)
			req := httptest.NewRequest(http.MethodGet, reqURL, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("Expected 400 for non-SELECT query %q, got %d", q, w.Code)
			}
		})
	}
}

func TestVortexHandler_RegisterDatabase_FileNotFound(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewVortexHandler(true, 0, logger)

	err := handler.RegisterDatabase("test", "/nonexistent/path/test.duckdb")
	if err == nil {
		t.Error("Expected error when registering non-existent file, got nil")
	}
}

func TestVortexHandler_MissingQueryParameter(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.duckdb")
	if err := os.WriteFile(dbPath, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	logger := zerolog.Nop()
	handler := NewVortexHandler(true, 0, logger)
	if err := handler.RegisterDatabase("beyla", dbPath); err != nil {
		t.Fatal(err)
	}

	// Request to /vortex/<db> with no table and no ?query= should return 400.
	req := httptest.NewRequest(http.MethodGet, "/vortex/beyla", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 when no table or query specified, got %d", w.Code)
	}
}

func TestIsValidIdentifier(t *testing.T) {
	cases := []struct {
		input string
		valid bool
	}{
		{"beyla_http_metrics_local", true},
		{"beyla", true},
		{"_private", true},
		{"table1", true},
		{"", false},
		{"table with spaces", false},
		{"123starts_with_digit", false},
		{"table;drop", false},
		{"../traversal", false},
		{"table-name", false},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := isValidIdentifier(tc.input)
			if got != tc.valid {
				t.Errorf("isValidIdentifier(%q) = %v, want %v", tc.input, got, tc.valid)
			}
		})
	}
}

func TestIsSelectQuery(t *testing.T) {
	cases := []struct {
		query string
		ok    bool
	}{
		{"SELECT * FROM foo", true},
		{"select * from foo", true},
		{"  SELECT 1", true},
		{"DROP TABLE foo", false},
		{"INSERT INTO foo VALUES (1)", false},
		{"", false},
		{"SEL", false},
	}

	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			got := isSelectQuery(tc.query)
			if got != tc.ok {
				t.Errorf("isSelectQuery(%q) = %v, want %v", tc.query, got, tc.ok)
			}
		})
	}
}
