//go:build linux

package ebpf_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"log/slog"

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

// TestDiscovery_WithSDK tests discovery using SDK integration.
func TestDiscovery_WithSDK(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if _, err := os.Stat("/proc"); os.IsNotExist(err) {
		t.Skip("Skipping integration test: /proc not available (not on Linux)")
	}

	binPath := filepath.Join("testdata", "bin", "app_with_sdk_dwarf")
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		t.Fatalf("Test binary not found: %s (run 'go generate ./internal/agent/ebpf/...' to build test apps)", binPath)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pidFile := filepath.Join(t.TempDir(), "app.pid")
	app := startTestApp(t, ctx, binPath, pidFile, sdkDebugPort)
	defer stopTestApp(t, app)

	pid := waitForPID(t, pidFile, 5*time.Second)
	t.Logf("Test app started with PID: %d", pid)

	discoveryService, err := ebpf.NewDiscoveryService(newDiscoveryConfig(t, true))
	require.NoError(t, err)
	defer discoveryService.Close()

	sdkAddr := fmt.Sprintf("localhost:%s", sdkDebugPort)
	result, err := discoveryService.DiscoverFunction(ctx, sdkAddr, uint32(pid), targetFunction)
	require.NoError(t, err)

	assert.Equal(t, ebpf.DiscoveryMethodSDK, result.Method)
	assert.Equal(t, targetFunction, result.Metadata.Name)
	assert.NotZero(t, result.Metadata.Offset)
	assert.Equal(t, uint32(pid), result.Metadata.Pid)

	t.Logf("✓ Discovered function via SDK: %s at offset 0x%x", result.Metadata.Name, result.Metadata.Offset)
}

// TestDiscovery_WithSDK_SymbolTableFallback tests SDK discovery with symbol table fallback.
// Validates that SDK works with binaries built with -ldflags="-w" (DWARF stripped, symbols intact).
func TestDiscovery_WithSDK_SymbolTableFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if _, err := os.Stat("/proc"); os.IsNotExist(err) {
		t.Skip("Skipping integration test: /proc not available (not on Linux)")
	}

	binPath := filepath.Join("testdata", "bin", "app_with_sdk_symtab_only")
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		t.Fatalf("Test binary not found: %s (run 'go generate ./internal/agent/ebpf/...' to build test apps)", binPath)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pidFile := filepath.Join(t.TempDir(), "app.pid")
	app := startTestApp(t, ctx, binPath, pidFile, sdkDebugPort)
	defer stopTestApp(t, app)

	pid := waitForPID(t, pidFile, 5*time.Second)
	t.Logf("Test app started with PID: %d", pid)

	discoveryService, err := ebpf.NewDiscoveryService(newDiscoveryConfig(t, true))
	require.NoError(t, err)
	defer discoveryService.Close()

	sdkAddr := fmt.Sprintf("localhost:%s", sdkDebugPort)
	result, err := discoveryService.DiscoverFunction(ctx, sdkAddr, uint32(pid), targetFunction)
	require.NoError(t, err)

	assert.Equal(t, ebpf.DiscoveryMethodSDK, result.Method)
	assert.Equal(t, targetFunction, result.Metadata.Name)
	assert.NotZero(t, result.Metadata.Offset)
	assert.Equal(t, uint32(pid), result.Metadata.Pid)

	t.Logf("✓ Discovered function via SDK symbol table fallback: %s at offset 0x%x", result.Metadata.Name, result.Metadata.Offset)
}

// TestDiscovery_BinaryScanning_WithDWARF tests discovery via binary scanning with DWARF symbols.
func TestDiscovery_BinaryScanning_WithDWARF(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if _, err := os.Stat("/proc"); os.IsNotExist(err) {
		t.Skip("Skipping integration test: /proc not available (not on Linux)")
	}

	binPath := filepath.Join("testdata", "bin", "app_no_sdk_dwarf")
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		t.Fatalf("Test binary not found: %s (run 'go generate ./internal/agent/ebpf/...' to build test apps)", binPath)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pidFile := filepath.Join(t.TempDir(), "app.pid")
	app := startTestApp(t, ctx, binPath, pidFile, "")
	defer stopTestApp(t, app)

	pid := waitForPID(t, pidFile, 5*time.Second)
	t.Logf("Test app started with PID: %d", pid)

	// SDK disabled to force binary scanning.
	discoveryService, err := ebpf.NewDiscoveryService(newDiscoveryConfig(t, false))
	require.NoError(t, err)
	defer discoveryService.Close()

	result, err := discoveryService.DiscoverFunction(ctx, "", uint32(pid), targetFunction)
	require.NoError(t, err)

	assert.Equal(t, ebpf.DiscoveryMethodBinary, result.Method)
	assert.Equal(t, targetFunction, result.Metadata.Name)
	assert.NotZero(t, result.Metadata.Offset)
	assert.Equal(t, uint32(pid), result.Metadata.Pid)

	t.Logf("✓ Discovered function via binary scanning: %s at offset 0x%x", result.Metadata.Name, result.Metadata.Offset)
}

// TestDiscovery_BinaryScanning_SymbolTableFallback tests discovery with symbol table fallback.
// Validates that binary scanning works with binaries built with -ldflags="-w" (DWARF stripped, symbols intact).
func TestDiscovery_BinaryScanning_SymbolTableFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if _, err := os.Stat("/proc"); os.IsNotExist(err) {
		t.Skip("Skipping integration test: /proc not available (not on Linux)")
	}

	binPath := filepath.Join("testdata", "bin", "app_no_sdk_symtab_only")
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		t.Fatalf("Test binary not found: %s (run 'go generate ./internal/agent/ebpf/...' to build test apps)", binPath)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pidFile := filepath.Join(t.TempDir(), "app.pid")
	app := startTestApp(t, ctx, binPath, pidFile, "")
	defer stopTestApp(t, app)

	pid := waitForPID(t, pidFile, 5*time.Second)
	t.Logf("Test app started with PID: %d", pid)

	// SDK disabled to force binary scanning.
	discoveryService, err := ebpf.NewDiscoveryService(newDiscoveryConfig(t, false))
	require.NoError(t, err)
	defer discoveryService.Close()

	result, err := discoveryService.DiscoverFunction(ctx, "", uint32(pid), targetFunction)
	require.NoError(t, err)

	assert.Equal(t, ebpf.DiscoveryMethodBinary, result.Method)
	assert.Equal(t, targetFunction, result.Metadata.Name)
	assert.NotZero(t, result.Metadata.Offset)
	assert.Equal(t, uint32(pid), result.Metadata.Pid)

	t.Logf("✓ Discovered function via binary scanning symbol table fallback: %s at offset 0x%x", result.Metadata.Name, result.Metadata.Offset)
}

// TestDiscovery_BinaryScanning_Stripped tests discovery failure with a fully stripped binary.
func TestDiscovery_BinaryScanning_Stripped(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if _, err := os.Stat("/proc"); os.IsNotExist(err) {
		t.Skip("Skipping integration test: /proc not available (not on Linux)")
	}

	binPath := filepath.Join("testdata", "bin", "app_no_sdk_stripped")
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		t.Fatalf("Test binary not found: %s (run 'go generate ./internal/agent/ebpf/...' to build test apps)", binPath)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pidFile := filepath.Join(t.TempDir(), "app.pid")
	app := startTestApp(t, ctx, binPath, pidFile, "")
	defer stopTestApp(t, app)

	pid := waitForPID(t, pidFile, 5*time.Second)
	t.Logf("Test app started with PID: %d", pid)

	discoveryService, err := ebpf.NewDiscoveryService(newDiscoveryConfig(t, false))
	require.NoError(t, err)
	defer discoveryService.Close()

	_, err = discoveryService.DiscoverFunction(ctx, "", uint32(pid), targetFunction)
	require.Error(t, err)

	errMsg := err.Error()
	assert.Contains(t, errMsg, "Failed to discover function")
	assert.Contains(t, errMsg, "Recommendations")

	t.Logf("✓ Discovery correctly failed with helpful error for stripped binary")
	t.Logf("Error message:\n%s", errMsg)
}

// TestDiscovery_Fallback tests fallback from SDK to binary scanning.
func TestDiscovery_Fallback(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if _, err := os.Stat("/proc"); os.IsNotExist(err) {
		t.Skip("Skipping integration test: /proc not available (not on Linux)")
	}

	binPath := filepath.Join("testdata", "bin", "app_no_sdk_dwarf")
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		t.Fatalf("Test binary not found: %s (run 'go generate ./internal/agent/ebpf/...' to build test apps)", binPath)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pidFile := filepath.Join(t.TempDir(), "app.pid")
	app := startTestApp(t, ctx, binPath, pidFile, "")
	defer stopTestApp(t, app)

	pid := waitForPID(t, pidFile, 5*time.Second)
	t.Logf("Test app started with PID: %d", pid)

	// SDK enabled but will fail, causing fallback to binary scanning.
	discoveryService, err := ebpf.NewDiscoveryService(newDiscoveryConfig(t, true))
	require.NoError(t, err)
	defer discoveryService.Close()

	// Wrong SDK address triggers fallback.
	result, err := discoveryService.DiscoverFunction(ctx, "localhost:9999", uint32(pid), targetFunction)
	require.NoError(t, err)

	assert.Equal(t, ebpf.DiscoveryMethodBinary, result.Method)
	assert.Equal(t, targetFunction, result.Metadata.Name)
	assert.NotZero(t, result.Metadata.Offset)

	t.Logf("✓ Successfully fell back from SDK to binary scanning")
}

// newDiscoveryConfig returns a DiscoveryConfig suitable for integration tests.
func newDiscoveryConfig(t *testing.T, enableSDK bool) *ebpf.DiscoveryConfig {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return &ebpf.DiscoveryConfig{
		EnableSDK:            enableSDK,
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
}

func startTestApp(t *testing.T, ctx context.Context, binPath, pidFile, sdkPort string) *exec.Cmd {
	t.Helper()

	cmd := exec.CommandContext(ctx, binPath)
	cmd.Env = append(os.Environ(),
		"PORT=0",
		"PID_FILE="+pidFile,
	)
	if sdkPort != "" {
		cmd.Env = append(cmd.Env, "CORAL_DEBUG_PORT="+sdkPort)
	}
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

	if cmd.Process == nil {
		return
	}

	t.Logf("Stopping test app (PID: %d)", cmd.Process.Pid)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Logf("Failed to send interrupt: %v", err)
		cmd.Process.Kill()
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-done:
		t.Logf("Test app stopped")
	case <-time.After(5 * time.Second):
		t.Logf("Test app didn't stop, killing...")
		cmd.Process.Kill()
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
