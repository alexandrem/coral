package debug

import (
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

// TestNewFunctionMetadataProvider tests creating a metadata provider.
func TestNewFunctionMetadataProvider(t *testing.T) {
	logger := zerolog.Nop()

	provider, err := NewFunctionMetadataProvider(logger)
	if err != nil {
		// It's okay if DWARF symbols aren't available in test binary.
		if strings.Contains(err.Error(), "debug symbols") {
			t.Skip("Test binary doesn't have DWARF symbols (expected in CI)")
		}
		t.Fatalf("NewFunctionMetadataProvider() error = %v", err)
	}
	defer provider.Close()

	if provider.binaryPath == "" {
		t.Error("binaryPath should not be empty")
	}

	if provider.pid == 0 {
		t.Error("pid should not be zero")
	}
}

// TestMatchesPattern tests the pattern matching function.
func TestMatchesPattern(t *testing.T) {
	tests := []struct {
		name     string
		funcName string
		pattern  string
		want     bool
	}{
		{
			name:     "wildcard matches all",
			funcName: "github.com/foo/bar.Baz",
			pattern:  "*",
			want:     true,
		},
		{
			name:     "empty pattern matches all",
			funcName: "github.com/foo/bar.Baz",
			pattern:  "",
			want:     true,
		},
		{
			name:     "exact match",
			funcName: "main.ProcessPayment",
			pattern:  "main.ProcessPayment",
			want:     true,
		},
		{
			name:     "package prefix match",
			funcName: "github.com/coral-mesh/coral/pkg.Function",
			pattern:  "github.com/coral-mesh/coral/*",
			want:     true,
		},
		{
			name:     "no match",
			funcName: "other.Function",
			pattern:  "main/*",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesPattern(tt.funcName, tt.pattern)
			if got != tt.want {
				t.Errorf("matchesPattern(%q, %q) = %v, want %v",
					tt.funcName, tt.pattern, got, tt.want)
			}
		})
	}
}

// TestFunctionMetadataCache tests that function metadata is cached.
func TestFunctionMetadataCache(t *testing.T) {
	logger := zerolog.Nop()

	provider, err := NewFunctionMetadataProvider(logger)
	if err != nil {
		if strings.Contains(err.Error(), "debug symbols") {
			t.Skip("Test binary doesn't have DWARF symbols (expected in CI)")
		}
		t.Fatalf("NewFunctionMetadataProvider() error = %v", err)
	}
	defer provider.Close()

	// Try to get a non-existent function.
	funcName := "nonexistent.TestFunction"
	_, err = provider.GetFunctionMetadata(funcName)
	if err == nil {
		t.Error("Expected error for non-existent function")
	}

	// Verify cache is working by checking the cache map.
	provider.mu.RLock()
	cacheSize := len(provider.functionCache)
	provider.mu.RUnlock()

	// After a failed lookup, cache should not contain the entry.
	if cacheSize != 0 {
		t.Errorf("Expected empty cache after failed lookup, got %d entries", cacheSize)
	}
}
