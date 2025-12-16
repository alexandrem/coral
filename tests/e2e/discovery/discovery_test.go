package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coral-mesh/coral/internal/agent/ebpf"
	"github.com/coral-mesh/coral/internal/agent/ebpf/binaryscanner"
)

//go:generate ./testdata/build.sh

const (
	targetFunction = "main.TargetFunction"
	sdkDebugPort   = "6060"
)

// TestE2E_Discovery_WithSDK tests discovery using SDK integration.
func TestE2E_Discovery_WithSDK(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Only run on Linux (eBPF requirement).
	if _, err := os.Stat("/proc"); os.IsNotExist(err) {
		t.Skip("Skipping E2E test: /proc not available (not on Linux)")
	}

	binPath := filepath.Join("testdata", "bin", "app_with_sdk_dwarf")
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		t.Fatalf("Test binary not found: %s (run 'go generate' to build test apps)", binPath)
	}

	// Start the test application.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pidFile := filepath.Join(t.TempDir(), "app.pid")
	app := startTestApp(t, ctx, binPath, pidFile, sdkDebugPort)
	defer stopTestApp(t, app)

	// Wait for app to be ready and write PID.
	pid := waitForPID(t, pidFile, 5*time.Second)
	t.Logf("Test app started with PID: %d", pid)

	// Create discovery service.
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	discoveryCfg := &ebpf.DiscoveryConfig{
		EnableSDK:            true,
		EnablePprof:          false,
		EnableBinaryScanning: true,
		BinaryScannerConfig: &binaryscanner.Config{
			AccessMethod:      binaryscanner.AccessMethodDirect,
			CacheEnabled:      true,
			CacheTTL:          1 * time.Minute,
			MaxCachedBinaries: 10,
			TempDir:           t.TempDir(),
			Logger:            logger,
		},
		Logger: logger,
	}

	discoveryService, err := ebpf.NewDiscoveryService(discoveryCfg)
	require.NoError(t, err)
	defer discoveryService.Close()

	// Discover function metadata.
	sdkAddr := fmt.Sprintf("localhost:%s", sdkDebugPort)
	result, err := discoveryService.DiscoverFunction(ctx, sdkAddr, uint32(pid), targetFunction)
	require.NoError(t, err)

	// Verify discovery succeeded via SDK.
	assert.Equal(t, ebpf.DiscoveryMethodSDK, result.Method)
	assert.Equal(t, targetFunction, result.Metadata.Name)
	assert.NotZero(t, result.Metadata.Offset)
	assert.Equal(t, uint32(pid), result.Metadata.Pid)

	t.Logf("✓ Successfully discovered function via SDK: %s at offset 0x%x", result.Metadata.Name, result.Metadata.Offset)
}

// TestE2E_Discovery_WithSDK_SymbolTableFallback tests SDK discovery with symbol table fallback.
// This validates that SDK works with binaries built with -ldflags="-w" (DWARF stripped, symbols intact).
func TestE2E_Discovery_WithSDK_SymbolTableFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Only run on Linux (eBPF requirement).
	if _, err := os.Stat("/proc"); os.IsNotExist(err) {
		t.Skip("Skipping E2E test: /proc not available (not on Linux)")
	}

	binPath := filepath.Join("testdata", "bin", "app_with_sdk_symtab_only")
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		t.Fatalf("Test binary not found: %s (run 'go generate' to build test apps)", binPath)
	}

	// Start the test application.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pidFile := filepath.Join(t.TempDir(), "app.pid")
	app := startTestApp(t, ctx, binPath, pidFile, sdkDebugPort)
	defer stopTestApp(t, app)

	// Wait for app to be ready and write PID.
	pid := waitForPID(t, pidFile, 5*time.Second)
	t.Logf("Test app started with PID: %d", pid)

	// Create discovery service.
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	discoveryCfg := &ebpf.DiscoveryConfig{
		EnableSDK:            true,
		EnablePprof:          false,
		EnableBinaryScanning: true,
		BinaryScannerConfig: &binaryscanner.Config{
			AccessMethod:      binaryscanner.AccessMethodDirect,
			CacheEnabled:      true,
			CacheTTL:          1 * time.Minute,
			MaxCachedBinaries: 10,
			TempDir:           t.TempDir(),
			Logger:            logger,
		},
		Logger: logger,
	}

	discoveryService, err := ebpf.NewDiscoveryService(discoveryCfg)
	require.NoError(t, err)
	defer discoveryService.Close()

	// Discover function metadata.
	sdkAddr := fmt.Sprintf("localhost:%s", sdkDebugPort)
	result, err := discoveryService.DiscoverFunction(ctx, sdkAddr, uint32(pid), targetFunction)
	require.NoError(t, err)

	// Verify discovery succeeded via SDK (using symbol table fallback internally).
	assert.Equal(t, ebpf.DiscoveryMethodSDK, result.Method)
	assert.Equal(t, targetFunction, result.Metadata.Name)
	assert.NotZero(t, result.Metadata.Offset)
	assert.Equal(t, uint32(pid), result.Metadata.Pid)

	t.Logf("✓ Successfully discovered function via SDK symbol table fallback: %s at offset 0x%x", result.Metadata.Name, result.Metadata.Offset)
}

// TestE2E_Discovery_BinaryScanning_WithDWARF tests discovery via binary scanning with DWARF symbols.
func TestE2E_Discovery_BinaryScanning_WithDWARF(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Only run on Linux (eBPF requirement).
	if _, err := os.Stat("/proc"); os.IsNotExist(err) {
		t.Skip("Skipping E2E test: /proc not available (not on Linux)")
	}

	binPath := filepath.Join("testdata", "bin", "app_no_sdk_dwarf")
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		t.Fatalf("Test binary not found: %s (run 'go generate' to build test apps)", binPath)
	}

	// Start the test application (without SDK).
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pidFile := filepath.Join(t.TempDir(), "app.pid")
	app := startTestApp(t, ctx, binPath, pidFile, "")
	defer stopTestApp(t, app)

	// Wait for app to be ready.
	pid := waitForPID(t, pidFile, 5*time.Second)
	t.Logf("Test app started with PID: %d", pid)

	// Create discovery service (SDK disabled to force binary scanning).
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	discoveryCfg := &ebpf.DiscoveryConfig{
		EnableSDK:            false, // Force binary scanning
		EnablePprof:          false,
		EnableBinaryScanning: true,
		BinaryScannerConfig: &binaryscanner.Config{
			AccessMethod:      binaryscanner.AccessMethodDirect,
			CacheEnabled:      true,
			CacheTTL:          1 * time.Minute,
			MaxCachedBinaries: 10,
			TempDir:           t.TempDir(),
			Logger:            logger,
		},
		Logger: logger,
	}

	discoveryService, err := ebpf.NewDiscoveryService(discoveryCfg)
	require.NoError(t, err)
	defer discoveryService.Close()

	// Discover function metadata (no SDK address provided).
	result, err := discoveryService.DiscoverFunction(ctx, "", uint32(pid), targetFunction)
	require.NoError(t, err)

	// Verify discovery succeeded via binary scanning.
	assert.Equal(t, ebpf.DiscoveryMethodBinary, result.Method)
	assert.Equal(t, targetFunction, result.Metadata.Name)
	assert.NotZero(t, result.Metadata.Offset)
	assert.Equal(t, uint32(pid), result.Metadata.Pid)

	t.Logf("✓ Successfully discovered function via binary scanning: %s at offset 0x%x", result.Metadata.Name, result.Metadata.Offset)
}

// TestE2E_Discovery_BinaryScanning_SymbolTableFallback tests agentless discovery with symbol table fallback.
// This validates that binary scanning works with binaries built with -ldflags="-w" (DWARF stripped, symbols intact).
func TestE2E_Discovery_BinaryScanning_SymbolTableFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Only run on Linux (eBPF requirement).
	if _, err := os.Stat("/proc"); os.IsNotExist(err) {
		t.Skip("Skipping E2E test: /proc not available (not on Linux)")
	}

	binPath := filepath.Join("testdata", "bin", "app_no_sdk_symtab_only")
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		t.Fatalf("Test binary not found: %s (run 'go generate' to build test apps)", binPath)
	}

	// Start the test application (without SDK).
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pidFile := filepath.Join(t.TempDir(), "app.pid")
	app := startTestApp(t, ctx, binPath, pidFile, "")
	defer stopTestApp(t, app)

	// Wait for app to be ready.
	pid := waitForPID(t, pidFile, 5*time.Second)
	t.Logf("Test app started with PID: %d", pid)

	// Create discovery service (SDK disabled to force binary scanning).
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	discoveryCfg := &ebpf.DiscoveryConfig{
		EnableSDK:            false, // Force binary scanning
		EnablePprof:          false,
		EnableBinaryScanning: true,
		BinaryScannerConfig: &binaryscanner.Config{
			AccessMethod:      binaryscanner.AccessMethodDirect,
			CacheEnabled:      true,
			CacheTTL:          1 * time.Minute,
			MaxCachedBinaries: 10,
			TempDir:           t.TempDir(),
			Logger:            logger,
		},
		Logger: logger,
	}

	discoveryService, err := ebpf.NewDiscoveryService(discoveryCfg)
	require.NoError(t, err)
	defer discoveryService.Close()

	// Discover function metadata (no SDK address provided).
	result, err := discoveryService.DiscoverFunction(ctx, "", uint32(pid), targetFunction)
	require.NoError(t, err)

	// Verify discovery succeeded via binary scanning (using symbol table fallback internally).
	assert.Equal(t, ebpf.DiscoveryMethodBinary, result.Method)
	assert.Equal(t, targetFunction, result.Metadata.Name)
	assert.NotZero(t, result.Metadata.Offset)
	assert.Equal(t, uint32(pid), result.Metadata.Pid)

	t.Logf("✓ Successfully discovered function via binary scanning symbol table fallback: %s at offset 0x%x", result.Metadata.Name, result.Metadata.Offset)
}

// TestE2E_Discovery_BinaryScanning_Stripped tests discovery failure with fully stripped binary.
func TestE2E_Discovery_BinaryScanning_Stripped(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Only run on Linux (eBPF requirement).
	if _, err := os.Stat("/proc"); os.IsNotExist(err) {
		t.Skip("Skipping E2E test: /proc not available (not on Linux)")
	}

	binPath := filepath.Join("testdata", "bin", "app_no_sdk_stripped")
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		t.Fatalf("Test binary not found: %s (run 'go generate' to build test apps)", binPath)
	}

	// Start the test application (without SDK, stripped).
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pidFile := filepath.Join(t.TempDir(), "app.pid")
	app := startTestApp(t, ctx, binPath, pidFile, "")
	defer stopTestApp(t, app)

	// Wait for app to be ready.
	pid := waitForPID(t, pidFile, 5*time.Second)
	t.Logf("Test app started with PID: %d", pid)

	// Create discovery service (SDK disabled, only binary scanning).
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	discoveryCfg := &ebpf.DiscoveryConfig{
		EnableSDK:            false,
		EnablePprof:          false,
		EnableBinaryScanning: true,
		BinaryScannerConfig: &binaryscanner.Config{
			AccessMethod:      binaryscanner.AccessMethodDirect,
			CacheEnabled:      true,
			CacheTTL:          1 * time.Minute,
			MaxCachedBinaries: 10,
			TempDir:           t.TempDir(),
			Logger:            logger,
		},
		Logger: logger,
	}

	discoveryService, err := ebpf.NewDiscoveryService(discoveryCfg)
	require.NoError(t, err)
	defer discoveryService.Close()

	// Try to discover function metadata - should fail with helpful error.
	_, err = discoveryService.DiscoverFunction(ctx, "", uint32(pid), targetFunction)
	require.Error(t, err)

	// Verify error message is helpful.
	errMsg := err.Error()
	assert.Contains(t, errMsg, "Failed to discover function")
	assert.Contains(t, errMsg, "Recommendations")

	t.Logf("✓ Discovery correctly failed with helpful error for stripped binary")
	t.Logf("Error message:\n%s", errMsg)
}

// TestE2E_Discovery_Fallback tests fallback from SDK to binary scanning.
func TestE2E_Discovery_Fallback(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Only run on Linux (eBPF requirement).
	if _, err := os.Stat("/proc"); os.IsNotExist(err) {
		t.Skip("Skipping E2E test: /proc not available (not on Linux)")
	}

	binPath := filepath.Join("testdata", "bin", "app_no_sdk_dwarf")
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		t.Fatalf("Test binary not found: %s (run 'go generate' to build test apps)", binPath)
	}

	// Start the test application (without SDK).
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pidFile := filepath.Join(t.TempDir(), "app.pid")
	app := startTestApp(t, ctx, binPath, pidFile, "")
	defer stopTestApp(t, app)

	// Wait for app to be ready.
	pid := waitForPID(t, pidFile, 5*time.Second)
	t.Logf("Test app started with PID: %d", pid)

	// Create discovery service with SDK enabled (but will fail, then fallback).
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	discoveryCfg := &ebpf.DiscoveryConfig{
		EnableSDK:            true, // Try SDK first
		EnablePprof:          false,
		EnableBinaryScanning: true, // Fallback to binary scanning
		BinaryScannerConfig: &binaryscanner.Config{
			AccessMethod:      binaryscanner.AccessMethodDirect,
			CacheEnabled:      true,
			CacheTTL:          1 * time.Minute,
			MaxCachedBinaries: 10,
			TempDir:           t.TempDir(),
			Logger:            logger,
		},
		Logger: logger,
	}

	discoveryService, err := ebpf.NewDiscoveryService(discoveryCfg)
	require.NoError(t, err)
	defer discoveryService.Close()

	// Try to discover with wrong SDK address (should fallback to binary scanning).
	result, err := discoveryService.DiscoverFunction(ctx, "localhost:9999", uint32(pid), targetFunction)
	require.NoError(t, err)

	// Verify discovery succeeded via binary scanning (fallback).
	assert.Equal(t, ebpf.DiscoveryMethodBinary, result.Method)
	assert.Equal(t, targetFunction, result.Metadata.Name)
	assert.NotZero(t, result.Metadata.Offset)

	t.Logf("✓ Successfully fell back from SDK to binary scanning")
}

// Helper functions

func startTestApp(t *testing.T, ctx context.Context, binPath, pidFile, sdkPort string) *exec.Cmd {
	t.Helper()

	cmd := exec.CommandContext(ctx, binPath)
	cmd.Env = append(os.Environ(),
		"PORT=0", // Don't need HTTP server for discovery tests
		"PID_FILE="+pidFile,
	)
	if sdkPort != "" {
		cmd.Env = append(cmd.Env, "CORAL_DEBUG_PORT="+sdkPort)
	}

	// Capture output for debugging.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start test app: %v", err)
	}

	t.Logf("Started test app: %s (PID will be written to %s)", binPath, pidFile)
	return cmd
}

func stopTestApp(t *testing.T, cmd *exec.Cmd) {
	t.Helper()

	if cmd.Process != nil {
		t.Logf("Stopping test app (PID: %d)", cmd.Process.Pid)
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			t.Logf("Failed to send interrupt: %v", err)
			cmd.Process.Kill()
		}

		// Wait with timeout.
		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
		}()

		select {
		case <-done:
			t.Logf("Test app stopped")
		case <-time.After(5 * time.Second):
			t.Logf("Test app didn't stop, killing...")
			cmd.Process.Kill()
		}
	}
}

func waitForPID(t *testing.T, pidFile string, timeout time.Duration) int {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(pidFile); err == nil {
			pidStr := strings.TrimSpace(string(data))
			if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf("Timeout waiting for PID file: %s", pidFile)
	return 0
}
