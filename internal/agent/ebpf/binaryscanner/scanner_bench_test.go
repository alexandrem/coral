package binaryscanner

import (
	"context"
	"log/slog"
	"os"
	"testing"
)

// BenchmarkListAllFunctions benchmarks bulk export of all functions.
func BenchmarkListAllFunctions(b *testing.B) {
	// Get current test binary path.
	binaryPath, err := os.Executable()
	if err != nil {
		b.Fatalf("Failed to get executable path: %v", err)
	}

	cfg := DefaultConfig()
	cfg.Logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	scanner, err := NewScanner(cfg)
	if err != nil {
		b.Fatalf("Failed to create scanner: %v", err)
	}
	defer scanner.Close()

	pid := os.Getpid()

	// Create metadata provider directly (skip PID discovery for benchmark).
	provider, err := scanner.getOrCreateProvider(binaryPath, pid)
	if err != nil {
		b.Skipf("Skipping benchmark: %v (binary may be stripped)", err)
	}

	b.ResetTimer()

	// Benchmark the actual export.
	for i := 0; i < b.N; i++ {
		allFunctions := provider.ListAllFunctions()
		if len(allFunctions) == 0 {
			b.Fatal("Expected functions, got 0")
		}
	}
}

// BenchmarkGetFunctionMetadata benchmarks single function lookup.
func BenchmarkGetFunctionMetadata(b *testing.B) {
	cfg := DefaultConfig()
	cfg.Logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
	cfg.CacheEnabled = true

	scanner, err := NewScanner(cfg)
	if err != nil {
		b.Fatalf("Failed to create scanner: %v", err)
	}
	defer scanner.Close()

	ctx := context.Background()
	pid := uint32(os.Getpid())

	// Warm up cache.
	_, err = scanner.GetFunctionMetadata(ctx, pid, "github.com/coral-mesh/coral/internal/agent/ebpf/binaryscanner.BenchmarkGetFunctionMetadata")
	if err != nil {
		b.Skipf("Skipping benchmark: %v", err)
	}

	b.ResetTimer()

	// Benchmark cached lookups.
	for i := 0; i < b.N; i++ {
		_, err := scanner.GetFunctionMetadata(ctx, pid, "github.com/coral-mesh/coral/internal/agent/ebpf/binaryscanner.BenchmarkGetFunctionMetadata")
		if err != nil {
			b.Fatal(err)
		}
	}
}
