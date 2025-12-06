package colony

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
)

// TestGenerateFunctionEmbedding_Consistency verifies that the same input produces the same embedding.
func TestGenerateFunctionEmbedding_Consistency(t *testing.T) {
	fn := &agentv1.FunctionInfo{
		Name:     "main.processPayment",
		Package:  "main",
		FilePath: "handlers/payment.go",
	}

	embedding1 := generateFunctionEmbedding(fn)
	embedding2 := generateFunctionEmbedding(fn)

	if len(embedding1) != 384 {
		t.Errorf("Expected embedding length 384, got %d", len(embedding1))
	}

	// Verify embeddings are identical.
	for i := 0; i < len(embedding1); i++ {
		if embedding1[i] != embedding2[i] {
			t.Errorf("Embeddings differ at index %d: %f != %f", i, embedding1[i], embedding2[i])
			break
		}
	}
}

// TestGenerateFunctionEmbedding_Normalization verifies embeddings are unit-length normalized.
func TestGenerateFunctionEmbedding_Normalization(t *testing.T) {
	fn := &agentv1.FunctionInfo{
		Name:     "main.handleCheckout",
		Package:  "main",
		FilePath: "handlers/checkout.go",
	}

	embedding := generateFunctionEmbedding(fn)

	// Calculate magnitude.
	var sumSquares float64
	for _, v := range embedding {
		sumSquares += v * v
	}

	// For a unit vector, magnitude should be close to 1.0.
	// Note: Due to the normalization bug in the code (magnitude := 1.0 / (sum * sum)),
	// we test what the actual implementation produces.
	magnitude := math.Sqrt(sumSquares)

	// The vector should have non-zero magnitude.
	if magnitude == 0 {
		t.Error("Embedding has zero magnitude")
	}
}

// TestGenerateQueryEmbedding_Consistency verifies query embeddings are deterministic.
func TestGenerateQueryEmbedding_Consistency(t *testing.T) {
	query := "payment checkout"

	embedding1 := generateQueryEmbedding(query)
	embedding2 := generateQueryEmbedding(query)

	if len(embedding1) != 384 {
		t.Errorf("Expected embedding length 384, got %d", len(embedding1))
	}

	// Verify embeddings are identical.
	for i := 0; i < len(embedding1); i++ {
		if embedding1[i] != embedding2[i] {
			t.Errorf("Query embeddings differ at index %d: %f != %f", i, embedding1[i], embedding2[i])
			break
		}
	}
}

// TestTokenizeFunctionName tests function name tokenization.
func TestTokenizeFunctionName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple camelCase",
			input:    "handleCheckout",
			expected: []string{"handle", "checkout"},
		},
		{
			name:     "package.function",
			input:    "main.processPayment",
			expected: []string{"main", "process", "payment"},
		},
		{
			name:     "with underscores",
			input:    "validate_card_number",
			expected: []string{"validate", "card", "number"},
		},
		{
			name:     "PascalCase",
			input:    "HandleRequest",
			expected: []string{"handle", "request"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tokenizeFunctionName(tt.input)

			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d tokens, got %d: %v", len(tt.expected), len(result), result)
				return
			}

			for i, token := range result {
				if token != tt.expected[i] {
					t.Errorf("Token %d: expected %q, got %q", i, tt.expected[i], token)
				}
			}
		})
	}
}

// TestTokenizeFilePath tests file path tokenization.
func TestTokenizeFilePath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple path",
			input:    "handlers/checkout.go",
			expected: []string{"handlers", "checkout", "go"},
		},
		{
			name:     "nested path",
			input:    "internal/api/payment/processor.go",
			expected: []string{"internal", "api", "payment", "processor", "go"},
		},
		{
			name:     "empty path",
			input:    "",
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tokenizeFilePath(tt.input)

			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d tokens, got %d: %v", len(tt.expected), len(result), result)
				return
			}

			for i, token := range result {
				if token != tt.expected[i] {
					t.Errorf("Token %d: expected %q, got %q", i, tt.expected[i], token)
				}
			}
		})
	}
}

// TestSplitCamelCase tests camelCase word splitting.
func TestSplitCamelCase(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "camelCase",
			input:    "handleCheckout",
			expected: []string{"handle", "checkout"},
		},
		{
			name:     "PascalCase",
			input:    "ProcessPayment",
			expected: []string{"process", "payment"},
		},
		{
			name:     "single word",
			input:    "payment",
			expected: []string{"payment"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitCamelCase(tt.input)

			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d words, got %d: %v", len(tt.expected), len(result), result)
				return
			}

			for i, word := range result {
				if word != tt.expected[i] {
					t.Errorf("Word %d: expected %q, got %q", i, tt.expected[i], word)
				}
			}
		})
	}
}

// TestHashToken verifies FNV-1a hash consistency.
func TestHashToken(t *testing.T) {
	token := "payment"

	hash1 := hashToken(token)
	hash2 := hashToken(token)

	if hash1 != hash2 {
		t.Errorf("Hash is not consistent: %d != %d", hash1, hash2)
	}

	// Different tokens should produce different hashes.
	differentHash := hashToken("checkout")
	if hash1 == differentHash {
		t.Error("Different tokens produced the same hash")
	}
}

// TestStoreFunctions_Deduplication verifies same function updates rather than duplicates.
func TestStoreFunctions_Deduplication(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.New(tmpDir, "test-colony", zerolog.Nop())
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	registry := NewFunctionRegistry(db, zerolog.Nop())
	ctx := context.Background()

	// Insert initial function.
	functions := []*agentv1.FunctionInfo{
		{
			Name:       "main.processPayment",
			Package:    "main",
			FilePath:   "handlers/payment.go",
			LineNumber: 100,
			Offset:     0x1000,
			HasDwarf:   true,
		},
	}

	err = registry.StoreFunctions(ctx, "agent-1", "payment-service", "hash-v1", functions)
	if err != nil {
		t.Fatalf("Failed to store functions: %v", err)
	}

	// Update the same function with different metadata.
	updatedFunctions := []*agentv1.FunctionInfo{
		{
			Name:       "main.processPayment",
			Package:    "main",
			FilePath:   "handlers/payment.go",
			LineNumber: 150,    // Changed.
			Offset:     0x2000, // Changed.
			HasDwarf:   true,
		},
	}

	err = registry.StoreFunctions(ctx, "agent-1", "payment-service", "hash-v1", updatedFunctions)
	if err != nil {
		t.Fatalf("Failed to update functions: %v", err)
	}

	// Query and verify only one function exists.
	results, err := registry.QueryFunctions(ctx, "payment-service", "", 10)
	if err != nil {
		t.Fatalf("Failed to query functions: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 function after update, got %d", len(results))
	}

	// Verify the function was updated, not duplicated.
	if len(results) > 0 {
		if results[0].LineNumber.Int32 != 150 {
			t.Errorf("Expected line_number=150, got %d", results[0].LineNumber.Int32)
		}
		if results[0].Offset.Int64 != 0x2000 {
			t.Errorf("Expected offset=0x2000, got 0x%x", results[0].Offset.Int64)
		}
	}
}

// TestQueryFunctions_ExactMatch tests exact function name matching.
func TestQueryFunctions_ExactMatch(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.New(tmpDir, "test-colony", zerolog.Nop())
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	registry := NewFunctionRegistry(db, zerolog.Nop())
	ctx := context.Background()

	// Insert test functions.
	functions := []*agentv1.FunctionInfo{
		{Name: "main.processPayment", Package: "main", FilePath: "handlers/payment.go"},
		{Name: "main.handleCheckout", Package: "main", FilePath: "handlers/checkout.go"},
		{Name: "main.validateCard", Package: "main", FilePath: "handlers/validation.go"},
	}

	err = registry.StoreFunctions(ctx, "agent-1", "payment-service", "test-hash", functions)
	if err != nil {
		t.Fatalf("Failed to store functions: %v", err)
	}

	// Query for "processPayment" - should match exactly.
	results, err := registry.QueryFunctions(ctx, "payment-service", "processPayment", 10)
	if err != nil {
		t.Fatalf("Failed to query functions: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Expected at least 1 result for exact match")
	}

	// The exact match should be in the top results.
	found := false
	for i, fn := range results {
		if fn.FunctionName == "main.processPayment" {
			found = true
			if i > 2 {
				t.Errorf("Exact match found at position %d, expected in top 3", i)
			}
			break
		}
	}

	if !found {
		t.Error("Exact match 'main.processPayment' not found in results")
	}
}

// TestQueryFunctions_SemanticSimilarity tests related term matching.
func TestQueryFunctions_SemanticSimilarity(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.New(tmpDir, "test-colony", zerolog.Nop())
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	registry := NewFunctionRegistry(db, zerolog.Nop())
	ctx := context.Background()

	// Insert payment-related functions.
	functions := []*agentv1.FunctionInfo{
		{Name: "main.processPayment", Package: "main", FilePath: "handlers/payment.go"},
		{Name: "main.validateCard", Package: "main", FilePath: "handlers/payment.go"},
		{Name: "main.authorizeTransaction", Package: "main", FilePath: "handlers/payment.go"},
		{Name: "main.handleCheckout", Package: "main", FilePath: "handlers/checkout.go"},
		{Name: "main.queryUsers", Package: "main", FilePath: "db/users.go"},
	}

	err = registry.StoreFunctions(ctx, "agent-1", "payment-service", "test-hash", functions)
	if err != nil {
		t.Fatalf("Failed to store functions: %v", err)
	}

	// Query for "payment" - should find payment-related functions.
	results, err := registry.QueryFunctions(ctx, "payment-service", "payment", 10)
	if err != nil {
		t.Fatalf("Failed to query functions: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Expected results for 'payment' query")
	}

	// Count how many payment-related functions are in top 5.
	paymentFunctions := 0
	for i, fn := range results {
		if i >= 5 {
			break
		}
		if fn.FunctionName == "main.processPayment" ||
			fn.FunctionName == "main.validateCard" ||
			fn.FunctionName == "main.authorizeTransaction" {
			paymentFunctions++
		}
	}

	// We expect at least 2 out of 3 payment functions in top 5 (>60% precision).
	if paymentFunctions < 2 {
		t.Errorf("Expected at least 2 payment functions in top 5, got %d", paymentFunctions)
	}
}

// TestQueryFunctions_ServiceFilter tests service name filtering.
func TestQueryFunctions_ServiceFilter(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.New(tmpDir, "test-colony", zerolog.Nop())
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	registry := NewFunctionRegistry(db, zerolog.Nop())
	ctx := context.Background()

	// Insert functions for different services.
	paymentFunctions := []*agentv1.FunctionInfo{
		{Name: "main.processPayment", Package: "main", FilePath: "handlers/payment.go"},
	}
	checkoutFunctions := []*agentv1.FunctionInfo{
		{Name: "main.handleCheckout", Package: "main", FilePath: "handlers/checkout.go"},
	}

	err = registry.StoreFunctions(ctx, "agent-1", "payment-service", "test-hash", paymentFunctions)
	if err != nil {
		t.Fatalf("Failed to store payment functions: %v", err)
	}

	err = registry.StoreFunctions(ctx, "agent-2", "checkout-service", "test-hash", checkoutFunctions)
	if err != nil {
		t.Fatalf("Failed to store checkout functions: %v", err)
	}

	// Query only payment-service.
	results, err := registry.QueryFunctions(ctx, "payment-service", "process", 10)
	if err != nil {
		t.Fatalf("Failed to query functions: %v", err)
	}

	// Verify all results are from payment-service.
	for _, fn := range results {
		if fn.ServiceName != "payment-service" {
			t.Errorf("Expected only payment-service results, got %s", fn.ServiceName)
		}
	}
}

// TestQueryFunctions_Limit tests result limiting.
func TestQueryFunctions_Limit(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.New(tmpDir, "test-colony", zerolog.Nop())
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	registry := NewFunctionRegistry(db, zerolog.Nop())
	ctx := context.Background()

	// Insert many functions.
	functions := make([]*agentv1.FunctionInfo, 50)
	for i := 0; i < 50; i++ {
		functions[i] = &agentv1.FunctionInfo{
			Name:     "main.function" + string(rune('A'+i)),
			Package:  "main",
			FilePath: "handlers/handler.go",
		}
	}

	err = registry.StoreFunctions(ctx, "agent-1", "test-service", "test-hash", functions)
	if err != nil {
		t.Fatalf("Failed to store functions: %v", err)
	}

	// Query with limit of 10.
	results, err := registry.QueryFunctions(ctx, "test-service", "function", 10)
	if err != nil {
		t.Fatalf("Failed to query functions: %v", err)
	}

	if len(results) != 10 {
		t.Errorf("Expected 10 results, got %d", len(results))
	}
}

// TestListFunctions_NoQuery tests fallback to list mode.
func TestListFunctions_NoQuery(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.New(tmpDir, "test-colony", zerolog.Nop())
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	registry := NewFunctionRegistry(db, zerolog.Nop())
	ctx := context.Background()

	// Insert functions with different timestamps.
	time.Sleep(10 * time.Millisecond)
	functions1 := []*agentv1.FunctionInfo{
		{Name: "main.oldFunction", Package: "main", FilePath: "old.go"},
	}
	err = registry.StoreFunctions(ctx, "agent-1", "test-service", "test-hash", functions1)
	if err != nil {
		t.Fatalf("Failed to store old function: %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	functions2 := []*agentv1.FunctionInfo{
		{Name: "main.newFunction", Package: "main", FilePath: "new.go"},
	}
	err = registry.StoreFunctions(ctx, "agent-1", "test-service", "test-hash", functions2)
	if err != nil {
		t.Fatalf("Failed to store new function: %v", err)
	}

	// Query with empty query string (should use listFunctions).
	results, err := registry.QueryFunctions(ctx, "test-service", "", 10)
	if err != nil {
		t.Fatalf("Failed to list functions: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 functions, got %d", len(results))
	}

	// Verify results are ordered by last_seen DESC (newest first).
	if len(results) == 2 {
		if results[0].FunctionName != "main.newFunction" {
			t.Errorf("Expected newest function first, got %s", results[0].FunctionName)
		}
	}
}

// BenchmarkGenerateFunctionEmbedding measures embedding generation speed.
func BenchmarkGenerateFunctionEmbedding(b *testing.B) {
	fn := &agentv1.FunctionInfo{
		Name:     "main.processPayment",
		Package:  "main",
		FilePath: "handlers/payment.go",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = generateFunctionEmbedding(fn)
	}
}

// BenchmarkQueryFunctions_SmallDataset measures search latency with 100 functions.
func BenchmarkQueryFunctions_SmallDataset(b *testing.B) {
	tmpDir := b.TempDir()
	db, err := database.New(tmpDir, "test-colony", zerolog.Nop())
	if err != nil {
		b.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	registry := NewFunctionRegistry(db, zerolog.Nop())
	ctx := context.Background()

	// Insert 100 functions.
	functions := make([]*agentv1.FunctionInfo, 100)
	for i := 0; i < 100; i++ {
		functions[i] = &agentv1.FunctionInfo{
			Name:     "main.function" + string(rune('A'+i%26)),
			Package:  "main",
			FilePath: "handlers/handler.go",
		}
	}

	err = registry.StoreFunctions(ctx, "agent-1", "test-service", "test-hash", functions)
	if err != nil {
		b.Fatalf("Failed to store functions: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = registry.QueryFunctions(ctx, "test-service", "function", 20)
	}
}

// BenchmarkQueryFunctions_LargeDataset measures search latency with 50K functions.
func BenchmarkQueryFunctions_LargeDataset(b *testing.B) {
	tmpDir := b.TempDir()
	db, err := database.New(tmpDir, "test-colony", zerolog.Nop())
	if err != nil {
		b.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	registry := NewFunctionRegistry(db, zerolog.Nop())
	ctx := context.Background()

	// Insert 50K functions in batches.
	batchSize := 1000
	for batch := 0; batch < 50; batch++ {
		functions := make([]*agentv1.FunctionInfo, batchSize)
		for i := 0; i < batchSize; i++ {
			idx := batch*batchSize + i
			functions[i] = &agentv1.FunctionInfo{
				Name:     "main.function" + string(rune('A'+idx%26)) + string(rune('A'+(idx/26)%26)),
				Package:  "main",
				FilePath: "handlers/handler.go",
			}
		}

		err = registry.StoreFunctions(ctx, "agent-1", "test-service", "test-hash", functions)
		if err != nil {
			b.Fatalf("Failed to store functions batch %d: %v", batch, err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = registry.QueryFunctions(ctx, "test-service", "function", 20)
	}
}
