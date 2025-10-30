package database

import (
	"context"
	"os"
	"path/filepath"
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
	defer db.Close()

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
	defer db.Close()

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
			defer db.Close()

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
	defer db.Close()

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
	defer db.Close()

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
