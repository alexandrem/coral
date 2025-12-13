package database

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestNew_ValidPath(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger for test.
	logger := zerolog.New(os.Stdout)

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Verify database was created.
	if db.ColonyID() != "test-colony" {
		t.Errorf("Expected colony_id 'test-colony', got %s", db.ColonyID())
	}

	// Verify database file exists.
	expectedPath := filepath.Join(tempDir, "test-colony.duckdb")
	if db.Path() != expectedPath {
		t.Errorf("Expected path %s, got %s", expectedPath, db.Path())
	}

	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("Database file does not exist at %s", expectedPath)
	}
}

func TestNew_CreatesDirectory(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()
	storagePath := filepath.Join(tempDir, "storage", "nested")

	// Create logger for test.
	logger := zerolog.New(os.Stdout)

	// Initialize database in non-existent directory.
	db, err := New(storagePath, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Verify directory was created.
	if _, err := os.Stat(storagePath); os.IsNotExist(err) {
		t.Errorf("Storage directory was not created at %s", storagePath)
	}
}

func TestNew_DatabaseFilename(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger for test.
	logger := zerolog.New(os.Stdout)

	// Test various colony IDs.
	testCases := []struct {
		colonyID         string
		expectedFilename string
	}{
		{"simple", "simple.duckdb"},
		{"my-app-prod", "my-app-prod.duckdb"},
		{"test-123", "test-123.duckdb"},
	}

	for _, tc := range testCases {
		t.Run(tc.colonyID, func(t *testing.T) {
			db, err := New(tempDir, tc.colonyID, logger)
			if err != nil {
				t.Fatalf("Failed to create database: %v", err)
			}
			defer func() { _ = db.Close() }()

			expectedPath := filepath.Join(tempDir, tc.expectedFilename)
			if db.Path() != expectedPath {
				t.Errorf("Expected path %s, got %s", expectedPath, db.Path())
			}

			if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
				t.Errorf("Database file does not exist at %s", expectedPath)
			}
		})
	}
}

func TestPing(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger for test.
	logger := zerolog.New(os.Stdout)

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Test ping.
	ctx := context.Background()
	if err := db.Ping(ctx); err != nil {
		t.Errorf("Ping failed: %v", err)
	}
}

func TestClose(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger for test.
	logger := zerolog.New(os.Stdout)

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}

	// Close database.
	if err := db.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Verify ping fails after close.
	ctx := context.Background()
	if err := db.Ping(ctx); err == nil {
		t.Error("Expected ping to fail after close, but it succeeded")
	}
}

func TestDB(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger for test.
	logger := zerolog.New(os.Stdout)

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Verify DB() returns non-nil connection.
	sqlDB := db.DB()
	if sqlDB == nil {
		t.Error("Expected non-nil *sql.DB, got nil")
	}

	// Verify we can execute queries on the connection.
	var result int
	if err := sqlDB.QueryRow("SELECT 1").Scan(&result); err != nil {
		t.Errorf("Failed to execute query: %v", err)
	}
	if result != 1 {
		t.Errorf("Expected result 1, got %d", result)
	}
}

func TestQueryContext_LogsQuery(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger that writes to a buffer at trace level.
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.TraceLevel)

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Execute a query with parameters.
	ctx := context.Background()
	rows, err := db.QueryContext(ctx, "SELECT ? as value", 42)
	if err != nil {
		t.Fatalf("QueryContext failed: %v", err)
	}
	defer func() { _ = rows.Close() }()

	// Verify logs contain the query.
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "Executing query") {
		t.Error("Expected log to contain 'Executing query'")
	}
	if !strings.Contains(logOutput, "Query executed") {
		t.Error("Expected log to contain 'Query executed'")
	}
	if !strings.Contains(logOutput, "SELECT 42 as value") {
		t.Error("Expected log to contain interpolated query 'SELECT 42 as value'")
	}
}

func TestQueryContext_LogsExecutionTime(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger that writes to a buffer at trace level.
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.TraceLevel)

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Execute a query.
	ctx := context.Background()
	rows, err := db.QueryContext(ctx, "SELECT 1")
	if err != nil {
		t.Fatalf("QueryContext failed: %v", err)
	}
	defer func() { _ = rows.Close() }()

	// Parse log output to verify duration is present.
	logLines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	foundDuration := false
	for _, line := range logLines {
		if !strings.Contains(line, "Query executed") {
			continue
		}

		var logEntry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &logEntry); err != nil {
			t.Fatalf("Failed to parse log entry: %v", err)
		}

		if _, ok := logEntry["duration_ms"]; ok {
			foundDuration = true
			break
		}
	}

	if !foundDuration {
		t.Error("Expected log to contain duration_ms field")
	}
}

func TestExecContext_LogsQuery(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger that writes to a buffer at trace level.
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.TraceLevel)

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Create a test table.
	ctx := context.Background()
	_, err = db.ExecContext(ctx, "CREATE TABLE test_table (id INTEGER, name VARCHAR)")
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	// Clear the log buffer.
	logBuf.Reset()

	// Execute an insert with parameters.
	_, err = db.ExecContext(ctx, "INSERT INTO test_table VALUES (?, ?)", 1, "test")
	if err != nil {
		t.Fatalf("ExecContext failed: %v", err)
	}

	// Verify logs contain the interpolated query.
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "INSERT INTO test_table VALUES (1, 'test')") {
		t.Error("Expected log to contain interpolated query with values")
	}
	if !strings.Contains(logOutput, "Executing query") {
		t.Error("Expected log to contain 'Executing query'")
	}
	if !strings.Contains(logOutput, "Query executed") {
		t.Error("Expected log to contain 'Query executed'")
	}
}

func TestQueryRowContext_LogsQuery(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger that writes to a buffer at trace level.
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.TraceLevel)

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Execute a single-row query with parameters.
	ctx := context.Background()
	var result int
	row := db.QueryRowContext(ctx, "SELECT ? + ? as sum", 10, 20)
	if err := row.Scan(&result); err != nil {
		t.Fatalf("QueryRowContext failed: %v", err)
	}

	if result != 30 {
		t.Errorf("Expected result 30, got %d", result)
	}

	// Verify logs contain the interpolated query.
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "SELECT 10 + 20 as sum") {
		t.Error("Expected log to contain interpolated query 'SELECT 10 + 20 as sum'")
	}
}

func TestQueryLogging_TraceLevel(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger at INFO level (higher than trace).
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.InfoLevel)

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Execute a query.
	ctx := context.Background()
	rows, err := db.QueryContext(ctx, "SELECT 1")
	if err != nil {
		t.Fatalf("QueryContext failed: %v", err)
	}
	defer func() { _ = rows.Close() }()

	// Verify that trace-level logs are NOT present when logger is at INFO level.
	logOutput := logBuf.String()

	// The log buffer should not contain query logs since we're at INFO level.
	// It may contain the "Database initialized" message though.
	if strings.Contains(logOutput, "Executing query") {
		t.Error("Expected no 'Executing query' log at INFO level")
	}
}

func TestQueryLogging_WithStringParameters(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger that writes to a buffer at trace level.
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.TraceLevel)

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Execute a query with string parameters.
	ctx := context.Background()
	rows, err := db.QueryContext(ctx, "SELECT ? as name WHERE ? = ?", "Alice", "status", "active")
	if err != nil {
		t.Fatalf("QueryContext failed: %v", err)
	}
	defer func() { _ = rows.Close() }()

	// Verify string parameters are properly quoted in logs.
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "'Alice'") {
		t.Error("Expected log to contain quoted string 'Alice'")
	}
	if !strings.Contains(logOutput, "'status'") {
		t.Error("Expected log to contain quoted string 'status'")
	}
	if !strings.Contains(logOutput, "'active'") {
		t.Error("Expected log to contain quoted string 'active'")
	}
}

func TestQueryLogging_WithMixedParameters(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger that writes to a buffer at trace level.
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.TraceLevel)

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Execute a query with mixed parameter types.
	ctx := context.Background()
	rows, err := db.QueryContext(ctx, "SELECT ? as id, ? as name, ? as price", 123, "Product", 99.99)
	if err != nil {
		t.Fatalf("QueryContext failed: %v", err)
	}
	defer func() { _ = rows.Close() }()

	// Verify all parameter types are in the log.
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "123") {
		t.Error("Expected log to contain integer parameter '123'")
	}
	if !strings.Contains(logOutput, "'Product'") {
		t.Error("Expected log to contain string parameter 'Product'")
	}
	if !strings.Contains(logOutput, "99.99") {
		t.Error("Expected log to contain float parameter '99.99'")
	}
}

func TestCleanQueryWhitespace(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "multiple spaces",
			input:    "SELECT  *   FROM    table",
			expected: "SELECT * FROM table",
		},
		{
			name:     "newlines and tabs",
			input:    "SELECT *\n\tFROM table\n\tWHERE id = 1",
			expected: "SELECT * FROM table WHERE id = 1",
		},
		{
			name:     "mixed whitespace",
			input:    "SELECT   *  \n  FROM\t\ttable  \n WHERE\r\nid = 1",
			expected: "SELECT * FROM table WHERE id = 1",
		},
		{
			name:     "leading and trailing whitespace",
			input:    "  SELECT * FROM table  ",
			expected: "SELECT * FROM table",
		},
		{
			name:     "no extra whitespace",
			input:    "SELECT * FROM table",
			expected: "SELECT * FROM table",
		},
		{
			name: "multi-line query",
			input: `SELECT bucket_time, agent_id
		FROM system_metrics
		WHERE bucket_time >= 2025-01-01
		ORDER BY bucket_time DESC`,
			expected: "SELECT bucket_time, agent_id FROM system_metrics WHERE bucket_time >= 2025-01-01 ORDER BY bucket_time DESC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cleanQueryWhitespace(tt.input)
			if result != tt.expected {
				t.Errorf("cleanQueryWhitespace() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestFormatQuery_CleansWhitespace(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger that writes to a buffer at trace level.
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.TraceLevel)

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Execute a multi-line query.
	ctx := context.Background()
	query := `SELECT ?   AS value,
		?    AS id`
	rows, err := db.QueryContext(ctx, query, "test", 123)
	if err != nil {
		t.Fatalf("QueryContext failed: %v", err)
	}
	defer func() { _ = rows.Close() }()

	// Verify the logged query has cleaned whitespace.
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "SELECT 'test' AS value, 123 AS id") {
		t.Errorf("Expected log to contain cleaned query, got: %s", logOutput)
	}
	// Verify it doesn't contain newlines in the query.
	lines := strings.Split(logOutput, "\n")
	for _, line := range lines {
		if strings.Contains(line, "query=") && strings.Contains(line, "\n") {
			t.Error("Expected logged query to not contain newlines")
		}
	}
}
