package ca

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupPSKTestDB creates a temporary DuckDB with the bootstrap_psks table and
// a CA directory with root key. Returns the Manager and cleanup function.
func setupPSKTestDB(t *testing.T) *Manager {
	t.Helper()

	tmpDir := t.TempDir()
	caDir := filepath.Join(tmpDir, "ca")

	// Initialize a real CA hierarchy (generates root key).
	result, err := Initialize(caDir, "psk-test-colony")
	require.NoError(t, err)
	require.NotEmpty(t, result.BootstrapPSK)

	// Open DuckDB.
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := sql.Open("duckdb", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	// Create schema.
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS bootstrap_psks (
		id TEXT PRIMARY KEY,
		encrypted_psk BLOB NOT NULL,
		encryption_nonce BLOB NOT NULL,
		status TEXT NOT NULL DEFAULT 'active',
		created_at TIMESTAMP NOT NULL,
		grace_expires_at TIMESTAMP,
		revoked_at TIMESTAMP
	)`)
	require.NoError(t, err)

	// Create Manager with real filesystem storage.
	m := &Manager{
		db:        db,
		colonyID:  "psk-test-colony",
		caDir:     caDir,
		fsStorage: NewFilesystemStorage(caDir),
	}

	return m
}

func TestManager_StorePSK(t *testing.T) {
	m := setupPSKTestDB(t)
	ctx := context.Background()

	psk, err := GeneratePSK()
	require.NoError(t, err)

	err = m.StorePSK(ctx, psk)
	require.NoError(t, err)

	// Verify it can be retrieved.
	got, err := m.GetActivePSK(ctx)
	require.NoError(t, err)
	assert.Equal(t, psk, got)
}

func TestManager_ValidateBootstrapPSK(t *testing.T) {
	m := setupPSKTestDB(t)
	ctx := context.Background()

	psk, err := GeneratePSK()
	require.NoError(t, err)
	require.NoError(t, m.StorePSK(ctx, psk))

	t.Run("valid PSK", func(t *testing.T) {
		err := m.ValidateBootstrapPSK(ctx, psk)
		assert.NoError(t, err)
	})

	t.Run("invalid PSK", func(t *testing.T) {
		err := m.ValidateBootstrapPSK(ctx, "coral-psk:0000000000000000000000000000000000000000000000000000000000000000")
		assert.Error(t, err)
	})

	t.Run("empty PSK", func(t *testing.T) {
		err := m.ValidateBootstrapPSK(ctx, "")
		assert.Error(t, err)
	})
}

func TestManager_RotatePSK(t *testing.T) {
	m := setupPSKTestDB(t)
	ctx := context.Background()

	// Store initial PSK.
	originalPSK, err := GeneratePSK()
	require.NoError(t, err)
	require.NoError(t, m.StorePSK(ctx, originalPSK))

	// Rotate with 1 hour grace period.
	newPSK, err := m.RotatePSK(ctx, 1*time.Hour)
	require.NoError(t, err)
	assert.NotEqual(t, originalPSK, newPSK)

	t.Run("new PSK is valid", func(t *testing.T) {
		assert.NoError(t, m.ValidateBootstrapPSK(ctx, newPSK))
	})

	t.Run("old PSK is valid during grace period", func(t *testing.T) {
		assert.NoError(t, m.ValidateBootstrapPSK(ctx, originalPSK))
	})

	t.Run("active PSK returns new one", func(t *testing.T) {
		got, err := m.GetActivePSK(ctx)
		require.NoError(t, err)
		assert.Equal(t, newPSK, got)
	})
}

func TestManager_RotatePSK_GraceExpiry(t *testing.T) {
	m := setupPSKTestDB(t)
	ctx := context.Background()

	originalPSK, err := GeneratePSK()
	require.NoError(t, err)
	require.NoError(t, m.StorePSK(ctx, originalPSK))

	// Rotate with 0 grace period (immediately expires).
	newPSK, err := m.RotatePSK(ctx, 0)
	require.NoError(t, err)

	// New PSK should be valid.
	assert.NoError(t, m.ValidateBootstrapPSK(ctx, newPSK))

	// Old PSK should be rejected (grace period expired).
	assert.Error(t, m.ValidateBootstrapPSK(ctx, originalPSK))
}

func TestManager_ImportPSKFromFile(t *testing.T) {
	m := setupPSKTestDB(t)
	ctx := context.Background()

	// Initialize already created a PSK file. Import it.
	err := m.ImportPSKFromFile(ctx)
	require.NoError(t, err)

	// Should have an active PSK now.
	psk, err := m.GetActivePSK(ctx)
	require.NoError(t, err)
	assert.True(t, len(psk) > 0)
	assert.NoError(t, ValidatePSKFormat(psk))
}

func TestManager_ImportPSKFromFile_Idempotent(t *testing.T) {
	m := setupPSKTestDB(t)
	ctx := context.Background()

	// Import twice should not create duplicates.
	require.NoError(t, m.ImportPSKFromFile(ctx))
	require.NoError(t, m.ImportPSKFromFile(ctx))

	// Should still have exactly one active PSK.
	var count int
	err := m.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM bootstrap_psks WHERE status = 'active'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestInitialize_GeneratesPSK(t *testing.T) {
	tmpDir := t.TempDir()
	caDir := filepath.Join(tmpDir, "ca")

	result, err := Initialize(caDir, "psk-init-test")
	require.NoError(t, err)

	// PSK should be in result.
	assert.NotEmpty(t, result.BootstrapPSK)
	assert.NoError(t, ValidatePSKFormat(result.BootstrapPSK))

	// PSK file should exist.
	assert.True(t, PSKFileExists(caDir))
	assert.FileExists(t, filepath.Join(caDir, pskFileName))

	// PSK file should be readable with the root key.
	fsStorage := NewFilesystemStorage(caDir)
	rootKey, err := fsStorage.LoadKey("root-ca")
	require.NoError(t, err)

	loaded, err := LoadPSKFromFile(caDir, rootKey)
	require.NoError(t, err)
	assert.Equal(t, result.BootstrapPSK, loaded)
}

func TestInitialize_IdempotentPSK(t *testing.T) {
	tmpDir := t.TempDir()
	caDir := filepath.Join(tmpDir, "ca")

	// First init generates PSK.
	result1, err := Initialize(caDir, "psk-idem-test")
	require.NoError(t, err)

	// Second init loads existing CA (no new PSK generated).
	result2, err := Initialize(caDir, "psk-idem-test")
	require.NoError(t, err)

	// The second call loads from existing files, PSK field will be empty
	// since loadInitResult doesn't load PSK. This is by design - PSK is
	// shown only on first init.
	_ = result1
	_ = result2

	// But the PSK file should still exist from first init.
	assert.True(t, PSKFileExists(caDir))

	// And it should still be valid.
	fsStorage := NewFilesystemStorage(caDir)
	rootKey, err := fsStorage.LoadKey("root-ca")
	require.NoError(t, err)
	psk, err := LoadPSKFromFile(caDir, rootKey)
	require.NoError(t, err)
	assert.Equal(t, result1.BootstrapPSK, psk)
}

func TestManager_GetActivePSK_NoPSK(t *testing.T) {
	m := setupPSKTestDB(t)

	// No PSK stored, should error.
	_, err := m.GetActivePSK(context.Background())
	assert.Error(t, err)
}

func TestManager_ImportPSKFromFile_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	caDir := filepath.Join(tmpDir, "empty-ca")
	require.NoError(t, os.MkdirAll(caDir, 0700))

	db, err := sql.Open("duckdb", filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS bootstrap_psks (
		id TEXT PRIMARY KEY,
		encrypted_psk BLOB NOT NULL,
		encryption_nonce BLOB NOT NULL,
		status TEXT NOT NULL DEFAULT 'active',
		created_at TIMESTAMP NOT NULL,
		grace_expires_at TIMESTAMP,
		revoked_at TIMESTAMP
	)`)
	require.NoError(t, err)

	m := &Manager{
		db:        db,
		caDir:     caDir,
		fsStorage: NewFilesystemStorage(caDir),
	}

	// No PSK file - should be a no-op.
	err = m.ImportPSKFromFile(context.Background())
	assert.NoError(t, err)
}
