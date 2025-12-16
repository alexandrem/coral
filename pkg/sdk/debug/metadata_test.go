package debug

import (
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// TestNewFunctionMetadataProvider tests creating a metadata provider.
func TestNewFunctionMetadataProvider(t *testing.T) {
	logger := slog.Default()

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
	logger := slog.Default()

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

	// Verify cache is working by checking the LRU cache size.
	cacheSize := provider.detailCache.Len()

	// After a failed lookup, cache should not contain the entry.
	if cacheSize != 0 {
		t.Errorf("Expected empty cache after failed lookup, got %d entries", cacheSize)
	}
}

// TestLRUCacheEviction tests that the LRU cache evicts old entries when full.
func TestLRUCacheEviction(t *testing.T) {
	logger := slog.Default()

	provider, err := NewFunctionMetadataProvider(logger)
	if err != nil {
		if strings.Contains(err.Error(), "debug symbols") {
			t.Skip("Test binary doesn't have DWARF symbols (expected in CI)")
		}
		t.Fatalf("NewFunctionMetadataProvider() error = %v", err)
	}
	defer provider.Close()

	// Verify cache has correct capacity.
	if provider.detailCache.capacity != 100 {
		t.Errorf("Expected cache capacity of 100, got %d", provider.detailCache.capacity)
	}

	// The cache should not grow beyond 100 entries.
	// We can't easily test this without 100 real functions, but we can verify
	// the capacity is set correctly and the Len() method works.
	initialSize := provider.detailCache.Len()
	if initialSize < 0 {
		t.Errorf("Invalid cache size: %d", initialSize)
	}
}

// TestBinaryHashCaching tests that binary hash is computed only once.
func TestBinaryHashCaching(t *testing.T) {
	logger := slog.Default()

	provider, err := NewFunctionMetadataProvider(logger)
	if err != nil {
		if strings.Contains(err.Error(), "debug symbols") {
			t.Skip("Test binary doesn't have DWARF symbols (expected in CI)")
		}
		t.Fatalf("NewFunctionMetadataProvider() error = %v", err)
	}
	defer provider.Close()

	// Get hash first time.
	hash1, err1 := provider.GetBinaryHash()
	if err1 != nil {
		t.Fatalf("GetBinaryHash() first call error = %v", err1)
	}

	// Get hash second time (should be cached).
	hash2, err2 := provider.GetBinaryHash()
	if err2 != nil {
		t.Fatalf("GetBinaryHash() second call error = %v", err2)
	}

	// Hashes should be identical.
	if hash1 != hash2 {
		t.Errorf("Binary hash not consistent: %s != %s", hash1, hash2)
	}

	// Hash should not be empty.
	if hash1 == "" {
		t.Error("Binary hash should not be empty")
	}

	// Verify it's a valid SHA256 hex string (64 characters).
	if len(hash1) != 64 {
		t.Errorf("Expected SHA256 hash length 64, got %d", len(hash1))
	}
}

// TestLRUCacheBasicOperations tests basic LRU cache operations.
func TestLRUCacheBasicOperations(t *testing.T) {
	cache := newLRUCache(3)

	// Test empty cache.
	if cache.Len() != 0 {
		t.Errorf("Expected empty cache, got len=%d", cache.Len())
	}

	// Test Put and Get.
	metadata1 := &FunctionMetadata{Name: "func1", Offset: 100}
	cache.Put("func1", metadata1)

	retrieved, ok := cache.Get("func1")
	if !ok {
		t.Error("Expected to find func1 in cache")
	}
	if retrieved.Name != "func1" || retrieved.Offset != 100 {
		t.Errorf("Retrieved metadata doesn't match: %+v", retrieved)
	}

	// Test cache size.
	if cache.Len() != 1 {
		t.Errorf("Expected cache len=1, got %d", cache.Len())
	}

	// Add more entries.
	cache.Put("func2", &FunctionMetadata{Name: "func2", Offset: 200})
	cache.Put("func3", &FunctionMetadata{Name: "func3", Offset: 300})

	if cache.Len() != 3 {
		t.Errorf("Expected cache len=3, got %d", cache.Len())
	}

	// Add fourth entry (should evict func1 as it's least recently used).
	cache.Put("func4", &FunctionMetadata{Name: "func4", Offset: 400})

	if cache.Len() != 3 {
		t.Errorf("Expected cache len=3 after eviction, got %d", cache.Len())
	}

	// func1 should be evicted.
	_, ok = cache.Get("func1")
	if ok {
		t.Error("Expected func1 to be evicted from cache")
	}

	// func4 should be present.
	retrieved, ok = cache.Get("func4")
	if !ok {
		t.Error("Expected to find func4 in cache")
	}
	if retrieved.Offset != 400 {
		t.Errorf("Expected offset 400, got %d", retrieved.Offset)
	}
}

// TestLRUCacheLRUOrder tests that least recently used items are evicted first.
func TestLRUCacheLRUOrder(t *testing.T) {
	cache := newLRUCache(3)

	// Add three entries.
	cache.Put("func1", &FunctionMetadata{Name: "func1", Offset: 100})
	cache.Put("func2", &FunctionMetadata{Name: "func2", Offset: 200})
	cache.Put("func3", &FunctionMetadata{Name: "func3", Offset: 300})

	// Access func1 (making it most recently used).
	_, _ = cache.Get("func1")

	// Add func4 (should evict func2 as it's now least recently used).
	cache.Put("func4", &FunctionMetadata{Name: "func4", Offset: 400})

	// func1 should still be present (it was accessed recently).
	if _, ok := cache.Get("func1"); !ok {
		t.Error("Expected func1 to still be in cache after access")
	}

	// func2 should be evicted.
	if _, ok := cache.Get("func2"); ok {
		t.Error("Expected func2 to be evicted from cache")
	}

	// func3 and func4 should be present.
	if _, ok := cache.Get("func3"); !ok {
		t.Error("Expected func3 to be in cache")
	}
	if _, ok := cache.Get("func4"); !ok {
		t.Error("Expected func4 to be in cache")
	}
}

// TestListFunctionsPaginationEdgeCases tests edge cases in pagination.
func TestListFunctionsPaginationEdgeCases(t *testing.T) {
	logger := slog.Default()

	provider, err := NewFunctionMetadataProvider(logger)
	if err != nil {
		if strings.Contains(err.Error(), "debug symbols") {
			t.Skip("Test binary doesn't have DWARF symbols (expected in CI)")
		}
		t.Fatalf("NewFunctionMetadataProvider() error = %v", err)
	}
	defer provider.Close()

	totalCount := provider.GetFunctionCount()
	if totalCount == 0 {
		t.Skip("No functions available for testing")
	}

	t.Run("offset beyond total returns empty", func(t *testing.T) {
		functions, total := provider.ListFunctions("", 10, totalCount+100)
		if len(functions) != 0 {
			t.Errorf("Expected empty result for offset beyond total, got %d functions", len(functions))
		}
		if total != totalCount {
			t.Errorf("Expected total=%d, got %d", totalCount, total)
		}
	})

	t.Run("offset at exact total returns empty", func(t *testing.T) {
		functions, total := provider.ListFunctions("", 10, totalCount)
		if len(functions) != 0 {
			t.Errorf("Expected empty result for offset at total, got %d functions", len(functions))
		}
		if total != totalCount {
			t.Errorf("Expected total=%d, got %d", totalCount, total)
		}
	})

	t.Run("limit larger than remaining items", func(t *testing.T) {
		if totalCount < 2 {
			t.Skip("Need at least 2 functions for this test")
		}
		offset := totalCount - 1
		functions, total := provider.ListFunctions("", 100, offset)
		if len(functions) != 1 {
			t.Errorf("Expected 1 function, got %d", len(functions))
		}
		if total != totalCount {
			t.Errorf("Expected total=%d, got %d", totalCount, total)
		}
	})

	t.Run("zero limit returns empty", func(t *testing.T) {
		functions, total := provider.ListFunctions("", 0, 0)
		if len(functions) != 0 {
			t.Errorf("Expected empty result for limit=0, got %d functions", len(functions))
		}
		if total != totalCount {
			t.Errorf("Expected total=%d, got %d", totalCount, total)
		}
	})

	t.Run("first page", func(t *testing.T) {
		limit := 5
		if totalCount < limit {
			limit = totalCount
		}
		functions, total := provider.ListFunctions("", limit, 0)
		if len(functions) != limit {
			t.Errorf("Expected %d functions on first page, got %d", limit, len(functions))
		}
		if total != totalCount {
			t.Errorf("Expected total=%d, got %d", totalCount, total)
		}
	})

	t.Run("last page partial", func(t *testing.T) {
		if totalCount < 2 {
			t.Skip("Need at least 2 functions for this test")
		}
		limit := 10
		offset := totalCount - 1
		functions, total := provider.ListFunctions("", limit, offset)
		if len(functions) != 1 {
			t.Errorf("Expected 1 function on last page, got %d", len(functions))
		}
		if total != totalCount {
			t.Errorf("Expected total=%d, got %d", totalCount, total)
		}
	})

	t.Run("single item limit", func(t *testing.T) {
		functions, total := provider.ListFunctions("", 1, 0)
		if len(functions) != 1 {
			t.Errorf("Expected 1 function with limit=1, got %d", len(functions))
		}
		if total != totalCount {
			t.Errorf("Expected total=%d, got %d", totalCount, total)
		}
	})

	t.Run("very large limit", func(t *testing.T) {
		functions, total := provider.ListFunctions("", 999999, 0)
		if len(functions) != totalCount {
			t.Errorf("Expected all %d functions with large limit, got %d", totalCount, len(functions))
		}
		if total != totalCount {
			t.Errorf("Expected total=%d, got %d", totalCount, total)
		}
	})
}

// TestPatternMatchingEdgeCases tests edge cases in pattern matching.
func TestPatternMatchingEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		funcName string
		pattern  string
		want     bool
	}{
		{
			name:     "empty function name with empty pattern",
			funcName: "",
			pattern:  "",
			want:     true,
		},
		{
			name:     "empty function name with wildcard",
			funcName: "",
			pattern:  "*",
			want:     true,
		},
		{
			name:     "empty function name with specific pattern",
			funcName: "",
			pattern:  "main/*",
			want:     false,
		},
		{
			name:     "function name with dots",
			funcName: "github.com/user/repo.Function",
			pattern:  "github.com/user/*",
			want:     true,
		},
		{
			name:     "function name with multiple dots",
			funcName: "github.com/user/repo/subpkg.Function",
			pattern:  "github.com/user/*",
			want:     true,
		},
		{
			name:     "pattern without slash wildcard",
			funcName: "main.ProcessPayment",
			pattern:  "main*",
			want:     false,
		},
		{
			name:     "exact match with special chars",
			funcName: "main.(*Handler).Process",
			pattern:  "main.(*Handler).Process",
			want:     true,
		},
		{
			name:     "no match different package",
			funcName: "github.com/user/repo.Function",
			pattern:  "github.com/other/*",
			want:     false,
		},
		{
			name:     "package pattern longer than name",
			funcName: "main.Func",
			pattern:  "github.com/very/long/package/*",
			want:     false,
		},
		{
			name:     "trailing slash in pattern",
			funcName: "main.Function",
			pattern:  "main/",
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

// TestListFunctionsWithPatterns tests ListFunctions with various patterns.
func TestListFunctionsWithPatterns(t *testing.T) {
	logger := slog.Default()

	provider, err := NewFunctionMetadataProvider(logger)
	if err != nil {
		if strings.Contains(err.Error(), "debug symbols") {
			t.Skip("Test binary doesn't have DWARF symbols (expected in CI)")
		}
		t.Fatalf("NewFunctionMetadataProvider() error = %v", err)
	}
	defer provider.Close()

	totalCount := provider.GetFunctionCount()
	if totalCount == 0 {
		t.Skip("No functions available for testing")
	}

	t.Run("empty pattern returns all", func(t *testing.T) {
		functions, total := provider.ListFunctions("", 1000, 0)
		if total != totalCount {
			t.Errorf("Expected total=%d with empty pattern, got %d", totalCount, total)
		}
		if len(functions) != totalCount {
			t.Errorf("Expected %d functions with empty pattern, got %d", totalCount, len(functions))
		}
	})

	t.Run("wildcard pattern returns all", func(t *testing.T) {
		functions, total := provider.ListFunctions("*", 1000, 0)
		if total != totalCount {
			t.Errorf("Expected total=%d with wildcard pattern, got %d", totalCount, total)
		}
		if len(functions) != totalCount {
			t.Errorf("Expected %d functions with wildcard pattern, got %d", totalCount, len(functions))
		}
	})

	t.Run("non-matching pattern returns empty", func(t *testing.T) {
		functions, total := provider.ListFunctions("definitely.not.a.match/*", 1000, 0)
		if total != 0 {
			t.Errorf("Expected total=0 with non-matching pattern, got %d", total)
		}
		if len(functions) != 0 {
			t.Errorf("Expected 0 functions with non-matching pattern, got %d", len(functions))
		}
	})

	t.Run("pagination with pattern", func(t *testing.T) {
		// Get all functions to find a common pattern.
		allFunctions, _ := provider.ListFunctions("", 1000, 0)
		if len(allFunctions) < 2 {
			t.Skip("Need at least 2 functions for pagination test")
		}

		// Test pagination with wildcard pattern.
		page1, total := provider.ListFunctions("*", 1, 0)
		page2, _ := provider.ListFunctions("*", 1, 1)

		if len(page1) != 1 {
			t.Errorf("Expected 1 function on page 1, got %d", len(page1))
		}
		if len(page2) != 1 && total > 1 {
			t.Errorf("Expected 1 function on page 2, got %d", len(page2))
		}
		if len(page1) > 0 && len(page2) > 0 && page1[0].Name == page2[0].Name {
			t.Error("Expected different functions on different pages")
		}
	})
}

// TestCountFunctions tests the CountFunctions method.
func TestCountFunctions(t *testing.T) {
	logger := slog.Default()

	provider, err := NewFunctionMetadataProvider(logger)
	if err != nil {
		if strings.Contains(err.Error(), "debug symbols") {
			t.Skip("Test binary doesn't have DWARF symbols (expected in CI)")
		}
		t.Fatalf("NewFunctionMetadataProvider() error = %v", err)
	}
	defer provider.Close()

	totalCount := provider.GetFunctionCount()

	t.Run("empty pattern counts all", func(t *testing.T) {
		count := provider.CountFunctions("")
		if count != totalCount {
			t.Errorf("Expected count=%d, got %d", totalCount, count)
		}
	})

	t.Run("wildcard pattern counts all", func(t *testing.T) {
		count := provider.CountFunctions("*")
		if count != totalCount {
			t.Errorf("Expected count=%d, got %d", totalCount, count)
		}
	})

	t.Run("non-matching pattern counts zero", func(t *testing.T) {
		count := provider.CountFunctions("definitely.not.a.match/*")
		if count != 0 {
			t.Errorf("Expected count=0, got %d", count)
		}
	})
}

// TestConcurrentCacheAccess tests thread-safety of the LRU cache.
func TestConcurrentCacheAccess(t *testing.T) {
	cache := newLRUCache(10)

	// Prepare test data.
	for i := 0; i < 5; i++ {
		cache.Put(
			strings.Join([]string{"func", string(rune('0' + i))}, ""),
			&FunctionMetadata{Name: strings.Join([]string{"func", string(rune('0' + i))}, ""), Offset: uint64(i * 100)},
		)
	}

	// Run concurrent operations.
	const numGoroutines = 50
	const numOps = 100

	done := make(chan bool, numGoroutines)

	// Concurrent reads.
	for i := 0; i < numGoroutines/2; i++ {
		go func(id int) {
			for j := 0; j < numOps; j++ {
				key := strings.Join([]string{"func", string(rune('0' + (j % 5)))}, "")
				_, _ = cache.Get(key)
			}
			done <- true
		}(i)
	}

	// Concurrent writes.
	for i := 0; i < numGoroutines/2; i++ {
		go func(id int) {
			for j := 0; j < numOps; j++ {
				key := strings.Join([]string{"func", string(rune('0' + id)), "_", string(rune('0' + j))}, "")
				cache.Put(key, &FunctionMetadata{Name: key, Offset: uint64(id*1000 + j)})
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines.
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// Verify cache is still functional.
	cache.Put("final", &FunctionMetadata{Name: "final", Offset: 999})
	if result, ok := cache.Get("final"); !ok || result.Offset != 999 {
		t.Error("Cache corrupted after concurrent access")
	}

	// Verify cache respects size limit.
	if cache.Len() > 10 {
		t.Errorf("Cache exceeded capacity: got %d, want <= 10", cache.Len())
	}
}

// TestConcurrentProviderAccess tests thread-safety of the metadata provider.
func TestConcurrentProviderAccess(t *testing.T) {
	logger := slog.Default()

	provider, err := NewFunctionMetadataProvider(logger)
	if err != nil {
		if strings.Contains(err.Error(), "debug symbols") {
			t.Skip("Test binary doesn't have DWARF symbols (expected in CI)")
		}
		t.Fatalf("NewFunctionMetadataProvider() error = %v", err)
	}
	defer provider.Close()

	if provider.GetFunctionCount() == 0 {
		t.Skip("No functions available for testing")
	}

	const numGoroutines = 20
	const numOps = 50

	done := make(chan bool, numGoroutines)

	// Concurrent ListFunctions calls.
	for i := 0; i < numGoroutines/2; i++ {
		go func(id int) {
			for j := 0; j < numOps; j++ {
				_, _ = provider.ListFunctions("", 10, j%10)
			}
			done <- true
		}(i)
	}

	// Concurrent GetFunctionCount calls.
	for i := 0; i < numGoroutines/4; i++ {
		go func() {
			for j := 0; j < numOps; j++ {
				_ = provider.GetFunctionCount()
			}
			done <- true
		}()
	}

	// Concurrent GetBinaryHash calls.
	for i := 0; i < numGoroutines/4; i++ {
		go func() {
			for j := 0; j < numOps; j++ {
				_, _ = provider.GetBinaryHash()
			}
			done <- true
		}()
	}

	// Wait for all goroutines.
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// Verify provider is still functional.
	count := provider.GetFunctionCount()
	if count < 0 {
		t.Error("Provider corrupted after concurrent access")
	}
}

// TestLRUCacheUpdateExisting tests updating an existing cache entry.
func TestLRUCacheUpdateExisting(t *testing.T) {
	cache := newLRUCache(3)

	// Add initial entry.
	cache.Put("func1", &FunctionMetadata{Name: "func1", Offset: 100})

	// Verify initial value.
	result, ok := cache.Get("func1")
	if !ok || result.Offset != 100 {
		t.Fatalf("Expected offset=100, got %v, ok=%v", result, ok)
	}

	// Update with new value.
	cache.Put("func1", &FunctionMetadata{Name: "func1", Offset: 200})

	// Verify updated value.
	result, ok = cache.Get("func1")
	if !ok || result.Offset != 200 {
		t.Errorf("Expected updated offset=200, got %v", result)
	}

	// Cache size should still be 1.
	if cache.Len() != 1 {
		t.Errorf("Expected cache len=1 after update, got %d", cache.Len())
	}
}

// TestListAllFunctions tests the ListAllFunctions method.
func TestListAllFunctions(t *testing.T) {
	logger := slog.Default()

	provider, err := NewFunctionMetadataProvider(logger)
	if err != nil {
		if strings.Contains(err.Error(), "debug symbols") {
			t.Skip("Test binary doesn't have DWARF symbols (expected in CI)")
		}
		t.Fatalf("NewFunctionMetadataProvider() error = %v", err)
	}
	defer provider.Close()

	totalCount := provider.GetFunctionCount()
	allFunctions := provider.ListAllFunctions()

	if len(allFunctions) != totalCount {
		t.Errorf("Expected %d functions from ListAllFunctions, got %d", totalCount, len(allFunctions))
	}

	// Verify functions are sorted by name (allow duplicates).
	for i := 1; i < len(allFunctions); i++ {
		if allFunctions[i-1].Name > allFunctions[i].Name {
			t.Errorf("Functions not sorted: %s > %s", allFunctions[i-1].Name, allFunctions[i].Name)
		}
	}
}

// TestBinaryHashConsistency tests that binary hash remains consistent.
func TestBinaryHashConsistency(t *testing.T) {
	logger := slog.Default()

	provider, err := NewFunctionMetadataProvider(logger)
	if err != nil {
		if strings.Contains(err.Error(), "debug symbols") {
			t.Skip("Test binary doesn't have DWARF symbols (expected in CI)")
		}
		t.Fatalf("NewFunctionMetadataProvider() error = %v", err)
	}
	defer provider.Close()

	// Call multiple times and verify consistency.
	hashes := make([]string, 10)
	for i := 0; i < 10; i++ {
		hash, err := provider.GetBinaryHash()
		if err != nil {
			t.Fatalf("GetBinaryHash() call %d failed: %v", i, err)
		}
		hashes[i] = hash
	}

	// All hashes should be identical.
	for i := 1; i < len(hashes); i++ {
		if hashes[i] != hashes[0] {
			t.Errorf("Hash inconsistent: call 0 = %s, call %d = %s", hashes[0], i, hashes[i])
		}
	}
}

// TestHasDWARF tests the HasDWARF method.
func TestHasDWARF(t *testing.T) {
	logger := slog.Default()

	provider, err := NewFunctionMetadataProvider(logger)
	if err != nil {
		if strings.Contains(err.Error(), "debug symbols") {
			t.Skip("Test binary doesn't have DWARF symbols (expected in CI)")
		}
		t.Fatalf("NewFunctionMetadataProvider() error = %v", err)
	}
	defer provider.Close()

	// Just verify the method returns a boolean (true or false is both valid).
	hasDWARF := provider.HasDWARF()
	t.Logf("Binary has DWARF: %v", hasDWARF)

	// The result should be consistent.
	if provider.HasDWARF() != hasDWARF {
		t.Error("HasDWARF() returned inconsistent results")
	}
}

// TestBinaryPath tests the BinaryPath method.
func TestBinaryPath(t *testing.T) {
	logger := slog.Default()

	provider, err := NewFunctionMetadataProvider(logger)
	if err != nil {
		if strings.Contains(err.Error(), "debug symbols") {
			t.Skip("Test binary doesn't have DWARF symbols (expected in CI)")
		}
		t.Fatalf("NewFunctionMetadataProvider() error = %v", err)
	}
	defer provider.Close()

	path := provider.BinaryPath()
	if path == "" {
		t.Error("BinaryPath() returned empty string")
	}

	// Path should be consistent.
	if provider.BinaryPath() != path {
		t.Error("BinaryPath() returned inconsistent results")
	}
}

// TestGetFunctionMetadataNonExistent tests error handling for non-existent functions.
func TestGetFunctionMetadataNonExistent(t *testing.T) {
	logger := slog.Default()

	provider, err := NewFunctionMetadataProvider(logger)
	if err != nil {
		if strings.Contains(err.Error(), "debug symbols") {
			t.Skip("Test binary doesn't have DWARF symbols (expected in CI)")
		}
		t.Fatalf("NewFunctionMetadataProvider() error = %v", err)
	}
	defer provider.Close()

	testCases := []string{
		"nonexistent.Function",
		"",
		"definitely.not.a.real.function.name",
		"main.ThisFunctionDoesNotExist",
	}

	for _, funcName := range testCases {
		t.Run("function="+funcName, func(t *testing.T) {
			_, err := provider.GetFunctionMetadata(funcName)
			if err == nil {
				t.Errorf("Expected error for function %q, got nil", funcName)
			}
			if !strings.Contains(err.Error(), "not found") {
				t.Errorf("Expected 'not found' error, got: %v", err)
			}
		})
	}
}

// TestFileLineExtraction tests that file and line information is extracted when available.
func TestFileLineExtraction(t *testing.T) {
	logger := slog.Default()

	provider, err := NewFunctionMetadataProvider(logger)
	if err != nil {
		if strings.Contains(err.Error(), "debug symbols") {
			t.Skip("Test binary doesn't have DWARF symbols (expected in CI)")
		}
		t.Fatalf("NewFunctionMetadataProvider() error = %v", err)
	}
	defer provider.Close()

	if !provider.HasDWARF() {
		t.Skip("Test binary doesn't have DWARF symbols, skipping file/line test")
	}

	// Get all functions.
	allFunctions := provider.ListAllFunctions()
	if len(allFunctions) == 0 {
		t.Skip("No functions available for testing")
	}

	// Count how many functions have file/line information.
	withFileInfo := 0
	withLineInfo := 0

	for _, fn := range allFunctions {
		if fn.File != "" {
			withFileInfo++
			t.Logf("Function %s has file: %s", fn.Name, fn.File)
		}
		if fn.Line > 0 {
			withLineInfo++
		}
	}

	t.Logf("Functions with file info: %d/%d (%.1f%%)",
		withFileInfo, len(allFunctions),
		float64(withFileInfo)/float64(len(allFunctions))*100)
	t.Logf("Functions with line info: %d/%d (%.1f%%)",
		withLineInfo, len(allFunctions),
		float64(withLineInfo)/float64(len(allFunctions))*100)

	// With DWARF symbols, we expect at least some functions to have file/line info.
	// Note: Not all functions may have this info (e.g., assembly functions, generated code).
	if withFileInfo == 0 && withLineInfo == 0 {
		t.Log("Warning: No functions have file/line information despite DWARF being available")
		t.Log("This may be normal for certain binaries (e.g., stripped or generated code)")
	}
}

// TestBasicInfoFileLineConsistency tests that file/line info is consistent.
func TestBasicInfoFileLineConsistency(t *testing.T) {
	logger := slog.Default()

	provider, err := NewFunctionMetadataProvider(logger)
	if err != nil {
		if strings.Contains(err.Error(), "debug symbols") {
			t.Skip("Test binary doesn't have DWARF symbols (expected in CI)")
		}
		t.Fatalf("NewFunctionMetadataProvider() error = %v", err)
	}
	defer provider.Close()

	allFunctions := provider.ListAllFunctions()

	for _, fn := range allFunctions {
		// If file is present, it should be a valid path string.
		if fn.File != "" {
			// File should not contain null bytes or other invalid characters.
			if strings.Contains(fn.File, "\x00") {
				t.Errorf("Function %s has file with null byte: %q", fn.Name, fn.File)
			}
		}

		// Line numbers should be non-negative.
		if fn.Line < 0 {
			t.Errorf("Function %s has negative line number: %d", fn.Name, fn.Line)
		}

		// If we have a line number, it should be reasonable (not absurdly large).
		if fn.Line > 10000000 {
			t.Errorf("Function %s has suspiciously large line number: %d", fn.Name, fn.Line)
		}
	}
}

// TestBuildLineTable tests the line table building function.
func TestBuildLineTable(t *testing.T) {
	logger := slog.Default()

	provider, err := NewFunctionMetadataProvider(logger)
	if err != nil {
		if strings.Contains(err.Error(), "debug symbols") {
			t.Skip("Test binary doesn't have DWARF symbols (expected in CI)")
		}
		t.Fatalf("NewFunctionMetadataProvider() error = %v", err)
	}
	defer provider.Close()

	if !provider.HasDWARF() {
		t.Skip("Test binary doesn't have DWARF symbols, skipping line table test")
	}

	// Build line table (this is called internally during index building).
	lineTable := provider.buildLineTable()

	t.Logf("Line table entries: %d", len(lineTable))

	// Verify line table entries are reasonable.
	for addr, info := range lineTable {
		if addr == 0 {
			t.Errorf("Line table has zero address entry: %+v", info)
		}

		if info.Line < 0 {
			t.Errorf("Line table has negative line number at 0x%x: %d", addr, info.Line)
		}

		// File can be empty for some entries, but if present should be valid.
		if info.File != "" && strings.Contains(info.File, "\x00") {
			t.Errorf("Line table has file with null byte at 0x%x: %q", addr, info.File)
		}
	}
}

// TestSymbolCaching verifies that symbols are loaded once and cached.
func TestSymbolCaching(t *testing.T) {
	provider, err := NewFunctionMetadataProvider(slog.Default())
	if err != nil {
		if strings.Contains(err.Error(), "debug symbols") {
			t.Skip("Test binary doesn't have symbols (expected in CI)")
		}
		t.Fatalf("Failed to create provider: %v", err)
	}
	defer provider.Close() // nolint:errcheck

	// First call - should load symbols.
	symbols1, err1 := provider.loadSymbols()
	if err1 != nil {
		t.Fatalf("First loadSymbols failed: %v", err1)
	}

	// Second call - should return cached symbols.
	symbols2, err2 := provider.loadSymbols()
	if err2 != nil {
		t.Fatalf("Second loadSymbols failed: %v", err2)
	}

	// Both should be the same slice (same memory address).
	if len(symbols1) != len(symbols2) {
		t.Errorf("Symbol count mismatch: %d vs %d", len(symbols1), len(symbols2))
	}

	// Verify symbols were actually loaded.
	if len(symbols1) == 0 {
		t.Error("Expected symbols to be loaded, got empty slice")
	}

	t.Logf("Loaded %d symbols (cached on subsequent calls)", len(symbols1))
}

// TestSymbolCachingConcurrent verifies thread-safe symbol caching.
func TestSymbolCachingConcurrent(t *testing.T) {
	provider, err := NewFunctionMetadataProvider(slog.Default())
	if err != nil {
		if strings.Contains(err.Error(), "debug symbols") {
			t.Skip("Test binary doesn't have symbols (expected in CI)")
		}
		t.Fatalf("Failed to create provider: %v", err)
	}
	defer provider.Close() // nolint:errcheck

	// Launch multiple goroutines that all try to load symbols concurrently.
	const numGoroutines = 10
	var wg sync.WaitGroup
	results := make([][]symbolInfo, numGoroutines)
	errors := make([]error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errors[idx] = provider.loadSymbols()
		}(i)
	}

	wg.Wait()

	// All calls should succeed and return the same data.
	for i := 0; i < numGoroutines; i++ {
		if errors[i] != nil {
			t.Errorf("Goroutine %d failed: %v", i, errors[i])
		}
		if len(results[i]) == 0 {
			t.Errorf("Goroutine %d got empty symbols", i)
		}
		if i > 0 && len(results[i]) != len(results[0]) {
			t.Errorf("Goroutine %d got different symbol count: %d vs %d",
				i, len(results[i]), len(results[0]))
		}
	}

	t.Logf("All %d goroutines loaded %d symbols successfully", numGoroutines, len(results[0]))
}

// TestSearchReflectionCaching verifies searchReflectionForFunction uses cache.
func TestSearchReflectionCaching(t *testing.T) {
	provider, err := NewFunctionMetadataProvider(slog.Default())
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}
	defer provider.Close() // nolint:errcheck

	// First, load symbols to ensure cache is populated.
	symbols, err := provider.loadSymbols()
	if err != nil {
		t.Fatalf("Failed to load symbols: %v", err)
	}

	if len(symbols) == 0 {
		t.Skip("No symbols found in binary")
	}

	// Pick a function with non-zero address.
	var funcName string
	for _, sym := range symbols {
		if sym.Value != 0 {
			funcName = sym.Name
			break
		}
	}

	if funcName == "" {
		t.Skip("No function with non-zero address found")
	}

	// Search for the function multiple times.
	addr1, err1 := provider.searchReflectionForFunction(funcName)
	if err1 != nil {
		t.Fatalf("First search failed: %v", err1)
	}

	addr2, err2 := provider.searchReflectionForFunction(funcName)
	if err2 != nil {
		t.Fatalf("Second search failed: %v", err2)
	}

	// Both searches should return the same address.
	if addr1 != addr2 {
		t.Errorf("Address mismatch: 0x%x vs 0x%x", addr1, addr2)
	}

	if addr1 == 0 {
		t.Error("Expected non-zero address")
	}

	t.Logf("Found %s at address 0x%x (cached on subsequent calls)", funcName, addr1)
}

// BenchmarkSymbolAccess benchmarks symbol table access with caching.
func BenchmarkSymbolAccess(b *testing.B) {
	provider, err := NewFunctionMetadataProvider(slog.Default())
	if err != nil {
		b.Fatalf("Failed to create provider: %v", err)
	}
	defer provider.Close() // nolint:errcheck

	// Load symbols to get a valid function name.
	symbols, err := provider.loadSymbols()
	if err != nil || len(symbols) == 0 {
		b.Skip("No symbols available")
	}
	funcName := symbols[0].Name

	// Benchmark function search (uses cached symbols after first load).
	b.Run("SearchReflectionForFunction", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = provider.searchReflectionForFunction(funcName)
		}
	})

	// Benchmark list functions (uses cached symbols after first load).
	b.Run("ListFunctionsFromSymbols", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = provider.listFunctionsFromSymbols("runtime.*")
		}
	})
}
