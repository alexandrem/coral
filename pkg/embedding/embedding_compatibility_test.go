package embedding

import (
	"math"
	"testing"
)

// TestEmbeddingCompatibility ensures function and query embeddings are compatible.
// This is a critical test that validates the core assumption of semantic search:
// that query embeddings can meaningfully match function embeddings using cosine similarity.
func TestEmbeddingCompatibility(t *testing.T) {
	tests := []struct {
		functionName  string
		query         string
		minSimilarity float64
		description   string
	}{
		{
			functionName:  "main.ProcessPayment",
			query:         "payment",
			minSimilarity: 0.6,
			description:   "Query 'payment' should strongly match 'ProcessPayment'",
		},
		{
			functionName:  "main.ValidateCard",
			query:         "validate card",
			minSimilarity: 0.4,
			description:   "Multi-word query should match camelCase function",
		},
		{
			functionName:  "api.HandleCheckout",
			query:         "checkout",
			minSimilarity: 0.4,
			description:   "Simple query should match compound function name",
		},
		{
			functionName:  "github.com/shop/payments.AuthorizeTransaction",
			query:         "authorize transaction",
			minSimilarity: 0.4,
			description:   "Query should match qualified name with package path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			// Generate function embedding
			funcEmb := GenerateFunctionEmbedding(FunctionMetadata{
				Name: tt.functionName,
			})

			// Generate query embedding
			queryEmb := GenerateQueryEmbedding(tt.query)

			// Calculate cosine similarity
			similarity := cosineSimilarity(funcEmb, queryEmb)

			t.Logf("Function: %s, Query: %s, Similarity: %.4f", tt.functionName, tt.query, similarity)

			if similarity < tt.minSimilarity {
				t.Errorf("Similarity %.4f is below minimum %.4f - embeddings are not compatible!",
					similarity, tt.minSimilarity)
				t.Errorf("This indicates GenerateFunctionEmbedding and GenerateQueryEmbedding use different algorithms")
			}
		})
	}
}

// TestEmbeddingAlgorithmConsistency validates that both embedding functions use the same approach.
func TestEmbeddingAlgorithmConsistency(t *testing.T) {
	// These should produce similar patterns since they use the same algorithm
	funcEmb := GenerateFunctionEmbedding(FunctionMetadata{Name: "TestFunction"})
	queryEmb := GenerateQueryEmbedding("test function")

	// Both should have the same dimensions
	if len(funcEmb) != len(queryEmb) {
		t.Fatalf("Embedding dimensions don't match: func=%d, query=%d", len(funcEmb), len(queryEmb))
	}

	// Both should use the same value range (±1.0 for SimHash)
	for i, v := range funcEmb {
		if math.Abs(v) != 1.0 {
			t.Errorf("Function embedding value at index %d is %f, expected ±1.0", i, v)
			break
		}
	}

	for i, v := range queryEmb {
		if math.Abs(v) != 1.0 {
			t.Errorf("Query embedding value at index %d is %f, expected ±1.0", i, v)
			break
		}
	}
}

// TestEmbeddingDiscrimination ensures embeddings can distinguish between different concepts.
func TestEmbeddingDiscrimination(t *testing.T) {
	funcPayment := GenerateFunctionEmbedding(FunctionMetadata{Name: "main.ProcessPayment"})
	funcUser := GenerateFunctionEmbedding(FunctionMetadata{Name: "main.GetUser"})

	queryPayment := GenerateQueryEmbedding("payment")
	queryUser := GenerateQueryEmbedding("user")

	// Payment query should be more similar to payment function than to user function
	simPaymentPayment := cosineSimilarity(funcPayment, queryPayment)
	simPaymentUser := cosineSimilarity(funcPayment, queryUser)

	t.Logf("Payment function vs payment query: %.4f", simPaymentPayment)
	t.Logf("Payment function vs user query: %.4f", simPaymentUser)

	if simPaymentPayment <= simPaymentUser {
		t.Errorf("Payment query should be more similar to payment function than user query")
		t.Errorf("Payment-Payment: %.4f, Payment-User: %.4f", simPaymentPayment, simPaymentUser)
	}

	// User query should be more similar to user function than to payment function
	simUserUser := cosineSimilarity(funcUser, queryUser)
	simUserPayment := cosineSimilarity(funcUser, queryPayment)

	t.Logf("User function vs user query: %.4f", simUserUser)
	t.Logf("User function vs payment query: %.4f", simUserPayment)

	if simUserUser <= simUserPayment {
		t.Errorf("User query should be more similar to user function than payment query")
		t.Errorf("User-User: %.4f, User-Payment: %.4f", simUserUser, simUserPayment)
	}
}

// cosineSimilarity computes cosine similarity between two vectors.
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := 0; i < len(a); i++ {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}
