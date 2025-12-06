package colony

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/pkg/embedding"
)

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
	// Pre-compute embedding
	emb := embedding.GenerateFunctionEmbedding(embedding.FunctionMetadata{
		Name:     functions[0].Name,
		Package:  functions[0].Package,
		FilePath: functions[0].FilePath,
	})
	emb32 := make([]float32, len(emb))
	for i, v := range emb {
		emb32[i] = float32(v)
	}
	functions[0].Embedding = emb32

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
	// Pre-compute embedding
	emb = embedding.GenerateFunctionEmbedding(embedding.FunctionMetadata{
		Name:     updatedFunctions[0].Name,
		Package:  updatedFunctions[0].Package,
		FilePath: updatedFunctions[0].FilePath,
	})
	emb32 = make([]float32, len(emb))
	for i, v := range emb {
		emb32[i] = float32(v)
	}
	updatedFunctions[0].Embedding = emb32

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

	// Add embeddings
	for _, fn := range functions {
		emb := embedding.GenerateFunctionEmbedding(embedding.FunctionMetadata{
			Name:     fn.Name,
			Package:  fn.Package,
			FilePath: fn.FilePath,
		})
		emb32 := make([]float32, len(emb))
		for i, v := range emb {
			emb32[i] = float32(v)
		}
		fn.Embedding = emb32
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

	// Add embeddings
	for _, fn := range functions {
		emb := embedding.GenerateFunctionEmbedding(embedding.FunctionMetadata{
			Name:     fn.Name,
			Package:  fn.Package,
			FilePath: fn.FilePath,
		})
		emb32 := make([]float32, len(emb))
		for i, v := range emb {
			emb32[i] = float32(v)
		}
		fn.Embedding = emb32
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

	// Add embeddings
	for _, fn := range paymentFunctions {
		emb := embedding.GenerateFunctionEmbedding(embedding.FunctionMetadata{
			Name:     fn.Name,
			Package:  fn.Package,
			FilePath: fn.FilePath,
		})
		emb32 := make([]float32, len(emb))
		for i, v := range emb {
			emb32[i] = float32(v)
		}
		fn.Embedding = emb32
	}
	for _, fn := range checkoutFunctions {
		emb := embedding.GenerateFunctionEmbedding(embedding.FunctionMetadata{
			Name:     fn.Name,
			Package:  fn.Package,
			FilePath: fn.FilePath,
		})
		emb32 := make([]float32, len(emb))
		for i, v := range emb {
			emb32[i] = float32(v)
		}
		fn.Embedding = emb32
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
		emb := embedding.GenerateFunctionEmbedding(embedding.FunctionMetadata{
			Name:     functions[i].Name,
			Package:  functions[i].Package,
			FilePath: functions[i].FilePath,
		})
		emb32 := make([]float32, len(emb))
		for j, v := range emb {
			emb32[j] = float32(v)
		}
		functions[i].Embedding = emb32
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
	// Add embeddings
	for _, fn := range functions1 {
		emb := embedding.GenerateFunctionEmbedding(embedding.FunctionMetadata{
			Name:     fn.Name,
			Package:  fn.Package,
			FilePath: fn.FilePath,
		})
		emb32 := make([]float32, len(emb))
		for i, v := range emb {
			emb32[i] = float32(v)
		}
		fn.Embedding = emb32
	}
	err = registry.StoreFunctions(ctx, "agent-1", "test-service", "test-hash", functions1)
	if err != nil {
		t.Fatalf("Failed to store old function: %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	functions2 := []*agentv1.FunctionInfo{
		{Name: "main.newFunction", Package: "main", FilePath: "new.go"},
	}
	// Add embeddings
	for _, fn := range functions2 {
		emb := embedding.GenerateFunctionEmbedding(embedding.FunctionMetadata{
			Name:     fn.Name,
			Package:  fn.Package,
			FilePath: fn.FilePath,
		})
		emb32 := make([]float32, len(emb))
		for i, v := range emb {
			emb32[i] = float32(v)
		}
		fn.Embedding = emb32
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
	meta := embedding.FunctionMetadata{
		Name:     "main.processPayment",
		Package:  "main",
		FilePath: "handlers/payment.go",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = embedding.GenerateFunctionEmbedding(meta)
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
		emb := embedding.GenerateFunctionEmbedding(embedding.FunctionMetadata{
			Name:     functions[i].Name,
			Package:  functions[i].Package,
			FilePath: functions[i].FilePath,
		})
		emb32 := make([]float32, len(emb))
		for j, v := range emb {
			emb32[j] = float32(v)
		}
		functions[i].Embedding = emb32
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
			emb := embedding.GenerateFunctionEmbedding(embedding.FunctionMetadata{
				Name:     functions[i].Name,
				Package:  functions[i].Package,
				FilePath: functions[i].FilePath,
			})
			emb32 := make([]float32, len(emb))
			for j, v := range emb {
				emb32[j] = float32(v)
			}
			functions[i].Embedding = emb32
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
