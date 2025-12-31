package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coral-mesh/coral/internal/colony/database"
)

// NewTestDatabase creates an in-memory test database.
// The database is automatically cleaned up when the test completes.
func NewTestDatabase(t *testing.T) *database.Database {
	t.Helper()

	// Create temporary directory for test database.
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	logger := NewTestLogger(t)

	// Create database.
	db, err := database.New(tmpDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}

	// Clean up on test completion.
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("failed to close test database: %v", err)
		}
		// Remove database files.
		_ = os.RemoveAll(dbPath)
	})

	return db
}
