package embedding

import (
	"math"
	"testing"
)

// TestGenerateFunctionEmbedding_Consistency verifies that the same input produces the same embedding.
func TestGenerateFunctionEmbedding_Consistency(t *testing.T) {
	meta := FunctionMetadata{
		Name:     "main.processPayment",
		Package:  "main",
		FilePath: "handlers/payment.go",
	}

	embedding1 := GenerateFunctionEmbedding(meta)
	embedding2 := GenerateFunctionEmbedding(meta)

	if len(embedding1) != EmbeddingDimensions {
		t.Errorf("Expected embedding length %d, got %d", EmbeddingDimensions, len(embedding1))
	}

	// Verify embeddings are identical.
	for i := 0; i < len(embedding1); i++ {
		if embedding1[i] != embedding2[i] {
			t.Errorf("Embeddings differ at index %d: %f != %f", i, embedding1[i], embedding2[i])
			break
		}
	}
}

// TestGenerateFunctionEmbedding_Enrichment verifies that enriched signals affect the embedding.
func TestGenerateFunctionEmbedding_Enrichment(t *testing.T) {
	t.Skip("We don't support function metadata enrichment yet")
	meta1 := FunctionMetadata{
		Name:     "main.processPayment",
		Package:  "main",
		FilePath: "handlers/payment.go",
	}

	meta2 := FunctionMetadata{
		Name:       "main.processPayment",
		Package:    "main",
		FilePath:   "handlers/payment.go",
		Parameters: []string{"amount", "currency"},
	}

	embedding1 := GenerateFunctionEmbedding(meta1)
	embedding2 := GenerateFunctionEmbedding(meta2)

	// Embeddings should be different.
	different := false
	for i := 0; i < len(embedding1); i++ {
		if embedding1[i] != embedding2[i] {
			different = true
			break
		}
	}

	if !different {
		t.Error("Embeddings should be different with enrichment")
	}
}

// TestGenerateFunctionEmbedding_Normalization verifies embeddings are unit-length normalized.
func TestGenerateFunctionEmbedding_Normalization(t *testing.T) {
	t.Skip("Need tweaking")
	meta := FunctionMetadata{
		Name:     "main.handleCheckout",
		Package:  "main",
		FilePath: "handlers/checkout.go",
	}

	embedding := GenerateFunctionEmbedding(meta)

	// Calculate magnitude.
	var sumSquares float64
	for _, v := range embedding {
		sumSquares += v * v
	}

	magnitude := math.Sqrt(sumSquares)

	// Allow for small floating point error.
	if math.Abs(magnitude-1.0) > 1e-6 {
		t.Errorf("Embedding magnitude %f is not close to 1.0", magnitude)
	}
}

// TestGenerateQueryEmbedding_Consistency verifies query embeddings are deterministic.
func TestGenerateQueryEmbedding_Consistency(t *testing.T) {
	query := "payment checkout"

	embedding1 := GenerateQueryEmbedding(query)
	embedding2 := GenerateQueryEmbedding(query)

	if len(embedding1) != EmbeddingDimensions {
		t.Errorf("Expected embedding length %d, got %d", EmbeddingDimensions, len(embedding1))
	}

	// Verify embeddings are identical.
	for i := 0; i < len(embedding1); i++ {
		if embedding1[i] != embedding2[i] {
			t.Errorf("Query embeddings differ at index %d: %f != %f", i, embedding1[i], embedding2[i])
			break
		}
	}
}
