package debug

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func setupTestFunctionCache(t *testing.T) *FunctionCache {
	t.Helper()

	db, err := sql.Open("duckdb", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cache, err := NewFunctionCache(db, zerolog.Nop())
	require.NoError(t, err)
	return cache
}

// writeFakeBinary creates a small file to stand in for a service binary.
// computeBinaryHash only needs readable bytes, not a valid executable, so
// this is enough to exercise the cache's hash-tracking logic without
// depending on real DWARF debug info.
func writeFakeBinary(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "fake-binary")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

// TestFunctionCache_LazyNotEager verifies the core laziness contract of
// RFD 104: EnsureIndexed is a no-op when a binary is already cached under
// its current hash (proving no discovery work happens), and it does attempt
// discovery when the cache is missing or stale.
func TestFunctionCache_LazyNotEager(t *testing.T) {
	cache := setupTestFunctionCache(t)
	ctx := context.Background()
	serviceName := "svc"

	t.Run("skips discovery when already cached", func(t *testing.T) {
		binaryPath := writeFakeBinary(t, "already-cached-contents")

		hash, err := computeBinaryHash(binaryPath)
		require.NoError(t, err)

		// Seed the cache as if discovery had already run for this exact
		// binary hash.
		_, err = cache.db.ExecContext(ctx,
			"INSERT INTO binary_hashes (service_name, binary_path, binary_hash) VALUES (?, ?, ?)",
			serviceName, binaryPath, hash,
		)
		require.NoError(t, err)

		// The fake binary contents are not a valid executable, so if
		// EnsureIndexed attempted discovery it would fail. A nil result
		// proves it skipped straight past the discovery step.
		err = cache.EnsureIndexed(ctx, serviceName, binaryPath, "")
		require.NoError(t, err)
	})

	t.Run("attempts discovery when not cached", func(t *testing.T) {
		binaryPath := writeFakeBinary(t, "never-cached-contents")

		// No binary_hashes row exists for this service/binary, so
		// EnsureIndexed must proceed into discovery. The fake contents
		// aren't a parsable binary, so discovery fails — that failure is
		// itself the evidence that discovery was attempted rather than
		// skipped.
		err := cache.EnsureIndexed(ctx, serviceName+"-uncached", binaryPath, "")
		require.Error(t, err)
	})
}
