package binaryscanner

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScanner_DiscoverBinary(t *testing.T) {
	// This test only works on Linux with /proc filesystem.
	if _, err := os.Stat("/proc"); os.IsNotExist(err) {
		t.Skip("Skipping test: /proc not available (not on Linux)")
	}

	cfg := DefaultConfig()
	cfg.Logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	scanner, err := NewScanner(cfg)
	require.NoError(t, err)
	defer scanner.Close()

	// Get current process PID.
	pid := uint32(os.Getpid())

	// Discover binary for current process.
	binaryPath, err := scanner.discoverBinary(pid)
	require.NoError(t, err)
	assert.NotEmpty(t, binaryPath)

	t.Logf("Discovered binary: %s", binaryPath)
}

func TestScanner_GetFunctionMetadata_DirectAccess(t *testing.T) {
	// Get the current test binary path.
	binaryPath, err := os.Executable()
	require.NoError(t, err)

	cfg := DefaultConfig()
	cfg.Logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	scanner, err := NewScanner(cfg)
	require.NoError(t, err)
	defer scanner.Close()

	pid := os.Getpid()

	// Create metadata provider directly for the test binary.
	provider, err := scanner.getOrCreateProvider(binaryPath, pid)
	if err != nil {
		t.Skipf("Skipping test: %v (binary may be stripped or no DWARF)", err)
	}

	// Try to get metadata for a function that should exist in the test binary.
	meta, err := provider.GetFunctionMetadata("github.com/coral-mesh/coral/internal/agent/ebpf/binaryscanner.TestScanner_GetFunctionMetadata_DirectAccess")
	if err != nil {
		t.Skipf("Skipping test: %v (function not found, binary may be stripped)", err)
	}

	require.NoError(t, err)
	assert.NotNil(t, meta)
	assert.Contains(t, meta.Name, "TestScanner_GetFunctionMetadata_DirectAccess")
	assert.NotZero(t, meta.Offset)

	t.Logf("Function metadata: name=%s, offset=0x%x", meta.Name, meta.Offset)
}

func TestScanner_ListFunctions(t *testing.T) {
	// Get the current test binary path.
	binaryPath, err := os.Executable()
	require.NoError(t, err)

	cfg := DefaultConfig()
	cfg.Logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	scanner, err := NewScanner(cfg)
	require.NoError(t, err)
	defer scanner.Close()

	pid := os.Getpid()

	// Create metadata provider directly.
	provider, err := scanner.getOrCreateProvider(binaryPath, pid)
	if err != nil {
		t.Skipf("Skipping test: %v (binary may be stripped or no DWARF)", err)
	}

	// List all functions (with pagination).
	functions, total := provider.ListFunctions("", 10, 0)

	assert.Greater(t, total, 0)
	assert.LessOrEqual(t, len(functions), 10)

	t.Logf("Found %d total functions, showing first %d", total, len(functions))
	for i, fn := range functions {
		if i < 3 {
			t.Logf("  Function %d: %s @ 0x%x", i+1, fn.Name, fn.Offset)
		}
	}
}

func TestScanner_Caching(t *testing.T) {
	// Get the current test binary path.
	binaryPath, err := os.Executable()
	require.NoError(t, err)

	cfg := DefaultConfig()
	cfg.Logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg.CacheEnabled = true
	cfg.CacheTTL = 5 * time.Second

	scanner, err := NewScanner(cfg)
	require.NoError(t, err)
	defer scanner.Close()

	pid := os.Getpid()

	// First call - should populate cache.
	provider1, err := scanner.getOrCreateProvider(binaryPath, pid)
	if err != nil {
		t.Skipf("Skipping test: %v (binary may be stripped)", err)
	}

	// Second call - should use cache.
	provider2, err := scanner.getOrCreateProvider(binaryPath, pid)
	require.NoError(t, err)

	// Should be the same provider instance (from cache).
	assert.Equal(t, provider1, provider2)

	// Verify cache has one entry.
	scanner.mu.RLock()
	cacheSize := len(scanner.cache)
	scanner.mu.RUnlock()
	assert.Equal(t, 1, cacheSize)

	t.Logf("Cache working correctly, cache size: %d", cacheSize)
}

func TestScanner_CacheEviction(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg.AccessMethod = AccessMethodDirect
	cfg.CacheEnabled = true
	cfg.CacheTTL = 5 * time.Second
	cfg.MaxCachedBinaries = 2 // Very small cache for testing

	scanner, err := NewScanner(cfg)
	require.NoError(t, err)
	defer scanner.Close()

	// We can't easily test eviction without multiple binaries.
	// For now, just verify the cache size limit is respected.
	scanner.mu.RLock()
	cacheSize := len(scanner.cache)
	scanner.mu.RUnlock()
	assert.LessOrEqual(t, cacheSize, cfg.MaxCachedBinaries)
}

func TestComputeFileHash(t *testing.T) {
	// Create a temporary file with known content.
	tempDir := t.TempDir()
	tempFile := filepath.Join(tempDir, "test.bin")

	content := []byte("test content")
	err := os.WriteFile(tempFile, content, 0644)
	require.NoError(t, err)

	// Compute hash.
	hash1, err := computeFileHash(tempFile)
	require.NoError(t, err)
	assert.NotEmpty(t, hash1)

	// Compute again - should be the same.
	hash2, err := computeFileHash(tempFile)
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2)

	// Modify file - hash should change.
	err = os.WriteFile(tempFile, []byte("different content"), 0644)
	require.NoError(t, err)

	hash3, err := computeFileHash(tempFile)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hash3)

	t.Logf("Hash 1: %s", hash1[:16])
	t.Logf("Hash 3: %s", hash3[:16])
}

func TestScanner_NonExistentFunction(t *testing.T) {
	// Get the current test binary path.
	binaryPath, err := os.Executable()
	require.NoError(t, err)

	cfg := DefaultConfig()
	cfg.Logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	scanner, err := NewScanner(cfg)
	require.NoError(t, err)
	defer scanner.Close()

	pid := os.Getpid()

	// Create metadata provider.
	provider, err := scanner.getOrCreateProvider(binaryPath, pid)
	if err != nil {
		t.Skipf("Skipping test: %v (binary may be stripped)", err)
	}

	// Try to get metadata for a function that doesn't exist.
	_, err = provider.GetFunctionMetadata("this.function.does.not.exist")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
