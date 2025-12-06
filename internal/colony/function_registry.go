package colony

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
)

// FunctionRegistry manages the centralized function registry for all services.
type FunctionRegistry struct {
	db     *database.Database
	logger zerolog.Logger
}

// NewFunctionRegistry creates a new function registry.
func NewFunctionRegistry(db *database.Database, logger zerolog.Logger) *FunctionRegistry {
	return &FunctionRegistry{
		db:     db,
		logger: logger.With().Str("component", "function_registry").Logger(),
	}
}

// StoreFunctions stores or updates functions from an agent in the registry.
// This is called periodically by the polling logic when it receives functions from agents.
// The binary_hash parameter identifies the binary version these functions belong to.
func (r *FunctionRegistry) StoreFunctions(ctx context.Context, agentID, serviceName, binaryHash string, functions []*agentv1.FunctionInfo) error {
	if len(functions) == 0 {
		r.logger.Debug().
			Str("agent_id", agentID).
			Str("service", serviceName).
			Msg("No functions to store")
		return nil
	}

	r.logger.Info().
		Str("agent_id", agentID).
		Str("service", serviceName).
		Int("function_count", len(functions)).
		Msg("Storing functions in registry")

	// Begin transaction.
	tx, err := r.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now()

	for _, fn := range functions {
		// Convert embedding to DuckDB array format.
		// DuckDB's go driver doesn't support []float64 directly, so we convert to string.
		embedding := generateFunctionEmbedding(fn)
		embeddingStr := floatSliceToArrayString(embedding)

		// Insert or update function using ON CONFLICT with composite primary key.
		// Note: We exclude 'embedding' from UPDATE because DuckDB doesn't support array updates.
		// We exclude 'last_seen' from UPDATE because it has an index.
		// Embeddings are deterministic (same function → same embedding), so this is safe.
		_, err := tx.Exec(`
			INSERT INTO functions (
				service_name, function_name, binary_hash, agent_id,
				package_name, file_path, line_number, func_offset,
				has_dwarf, embedding, discovered_at, last_seen
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?::FLOAT[384], ?, ?)
			ON CONFLICT (service_name, function_name, binary_hash) DO UPDATE SET
				package_name = EXCLUDED.package_name,
				file_path = EXCLUDED.file_path,
				line_number = EXCLUDED.line_number,
				func_offset = EXCLUDED.func_offset,
				has_dwarf = EXCLUDED.has_dwarf
		`,
			serviceName,
			fn.Name,
			binaryHash,
			agentID,
			fn.Package,
			fn.FilePath,
			fn.LineNumber,
			fn.Offset,
			fn.HasDwarf,
			embeddingStr,
			now,
			now,
		)
		if err != nil {
			return fmt.Errorf("failed to insert/update function %s: %w", fn.Name, err)
		}
	}

	// Commit transaction.
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	r.logger.Info().
		Str("agent_id", agentID).
		Str("service", serviceName).
		Int("function_count", len(functions)).
		Msg("Successfully stored functions")

	return nil
}

// QueryFunctions queries the function registry with optional filters.
// This uses vector similarity search (DuckDB VSS) for semantic matching.
func (r *FunctionRegistry) QueryFunctions(ctx context.Context, serviceName, query string, limit int) ([]*FunctionInfo, error) {
	if limit <= 0 {
		limit = 20 // Default limit
	}
	if limit > 100 {
		limit = 100 // Max limit
	}

	r.logger.Debug().
		Str("service", serviceName).
		Str("query", query).
		Int("limit", limit).
		Msg("Querying function registry with vector similarity search")

	// If no query provided, just list functions.
	if query == "" {
		return r.listFunctions(ctx, serviceName, limit)
	}

	// Generate query embedding for semantic search.
	queryEmbedding := generateQueryEmbedding(query)
	queryEmbeddingStr := floatSliceToArrayString(queryEmbedding)

	// Build SQL query with vector similarity search.
	sqlQuery := `
		SELECT
			service_name, function_name, agent_id,
			package_name, file_path, line_number, func_offset,
			has_dwarf, discovered_at, last_seen,
			array_cosine_similarity(embedding, ?::FLOAT[384]) AS similarity
		FROM functions
		WHERE embedding IS NOT NULL
	`
	args := []interface{}{queryEmbeddingStr}

	// Filter by service name if specified.
	if serviceName != "" {
		sqlQuery += " AND service_name = ?"
		args = append(args, serviceName)
	}

	// Order by similarity (highest first) and limit results.
	sqlQuery += " ORDER BY similarity DESC LIMIT ?"

	args = append(args, limit)

	// Execute query.
	rows, err := r.db.DB().QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query functions: %w", err)
	}
	defer rows.Close()

	// Parse results.
	var functions []*FunctionInfo
	for rows.Next() {
		var fn FunctionInfo
		var discoveredAt, lastSeen time.Time
		var similarity float64

		err := rows.Scan(
			&fn.ServiceName,
			&fn.FunctionName,
			&fn.AgentID,
			&fn.PackageName,
			&fn.FilePath,
			&fn.LineNumber,
			&fn.Offset,
			&fn.HasDwarf,
			&discoveredAt,
			&lastSeen,
			&similarity,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan function row: %w", err)
		}

		fn.DiscoveredAt = discoveredAt
		fn.LastSeen = lastSeen
		functions = append(functions, &fn)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating function rows: %w", err)
	}

	r.logger.Info().
		Str("service", serviceName).
		Str("query", query).
		Int("result_count", len(functions)).
		Msg("Vector similarity search completed")

	return functions, nil
}

// listFunctions lists functions without semantic search (no query).
func (r *FunctionRegistry) listFunctions(ctx context.Context, serviceName string, limit int) ([]*FunctionInfo, error) {
	sqlQuery := `
		SELECT
			service_name, function_name, agent_id,
			package_name, file_path, line_number, func_offset,
			has_dwarf, discovered_at, last_seen
		FROM functions
		WHERE 1=1
	`
	args := []interface{}{}

	if serviceName != "" {
		sqlQuery += " AND service_name = ?"
		args = append(args, serviceName)
	}

	sqlQuery += " ORDER BY last_seen DESC LIMIT ?"
	args = append(args, limit)

	rows, err := r.db.DB().QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list functions: %w", err)
	}
	defer rows.Close()

	var functions []*FunctionInfo
	for rows.Next() {
		var fn FunctionInfo
		var discoveredAt, lastSeen time.Time

		err := rows.Scan(
			&fn.ServiceName,
			&fn.FunctionName,
			&fn.AgentID,
			&fn.PackageName,
			&fn.FilePath,
			&fn.LineNumber,
			&fn.Offset,
			&fn.HasDwarf,
			&discoveredAt,
			&lastSeen,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan function row: %w", err)
		}

		fn.DiscoveredAt = discoveredAt
		fn.LastSeen = lastSeen
		functions = append(functions, &fn)
	}

	return functions, rows.Err()
}

// FunctionInfo represents a discovered function with metadata.
type FunctionInfo struct {
	ServiceName  string
	FunctionName string
	AgentID      string
	PackageName  sql.NullString
	FilePath     sql.NullString
	LineNumber   sql.NullInt32
	Offset       sql.NullInt64
	HasDwarf     bool
	DiscoveredAt time.Time
	LastSeen     time.Time
}

// generateFunctionID generates a deterministic ID for a function.
// Uses service name + function name to ensure the same function is updated.
func generateFunctionID(serviceName, functionName string) string {
	// Use deterministic UUID v5 (namespace-based) for consistency.
	namespace := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8") // DNS namespace
	key := fmt.Sprintf("%s/%s", serviceName, functionName)
	return uuid.NewSHA1(namespace, []byte(key)).String()
}

// tokenizeFunctionName tokenizes a function name for search.
// Example: "main.handleCheckout" → ["main", "handle", "checkout"]
func tokenizeFunctionName(name string) []string {
	// Split by dots and underscores.
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '.' || r == '_' || r == '/'
	})

	tokens := []string{}
	for _, part := range parts {
		// Split camelCase: "handleCheckout" → ["handle", "Checkout"]
		tokens = append(tokens, splitCamelCase(part)...)
	}

	// Convert to lowercase and deduplicate.
	return deduplicateTokens(tokens)
}

// tokenizeFilePath tokenizes a file path for search.
// Example: "handlers/checkout.go" → ["handlers", "checkout", "go"]
func tokenizeFilePath(path string) []string {
	if path == "" {
		return []string{}
	}

	// Split by path separators and dots.
	parts := strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '.' || r == '_'
	})

	// Convert to lowercase and deduplicate.
	tokens := []string{}
	for _, part := range parts {
		tokens = append(tokens, strings.ToLower(part))
	}

	return deduplicateTokens(tokens)
}

// tokenizeQuery tokenizes a search query.
// Example: "checkout payment" → ["checkout", "payment"]
func tokenizeQuery(query string) []string {
	// Split by whitespace.
	parts := strings.Fields(query)

	tokens := []string{}
	for _, part := range parts {
		// Remove special characters and convert to lowercase.
		cleaned := strings.ToLower(strings.Trim(part, ".,;:!?"))
		if cleaned != "" {
			tokens = append(tokens, cleaned)
		}
	}

	return tokens
}

// splitCamelCase splits a camelCase or PascalCase string into words.
// Example: "handleCheckout" → ["handle", "Checkout"]
func splitCamelCase(s string) []string {
	if s == "" {
		return []string{}
	}

	var words []string
	lastIdx := 0

	for i := 1; i < len(s); i++ {
		// Check if current character is uppercase and previous is lowercase.
		if s[i] >= 'A' && s[i] <= 'Z' && s[i-1] >= 'a' && s[i-1] <= 'z' {
			words = append(words, strings.ToLower(s[lastIdx:i]))
			lastIdx = i
		}
	}

	// Add the last word.
	if lastIdx < len(s) {
		words = append(words, strings.ToLower(s[lastIdx:]))
	}

	return words
}

// deduplicateTokens removes duplicate tokens while preserving order.
func deduplicateTokens(tokens []string) []string {
	seen := make(map[string]bool)
	result := []string{}

	for _, token := range tokens {
		if token == "" {
			continue
		}
		if !seen[token] {
			seen[token] = true
			result = append(result, token)
		}
	}

	return result
}

// generateFunctionEmbedding generates a 384-dimensional embedding vector for a function.
// Uses a simple but effective TF-IDF-like approach based on function metadata.
func generateFunctionEmbedding(fn *agentv1.FunctionInfo) []float64 {
	// Combine all metadata into a text representation.
	text := fmt.Sprintf("%s %s %s", fn.Name, fn.Package, fn.FilePath)

	// Tokenize the text.
	tokens := tokenizeForEmbedding(text)

	// Create a 384-dimensional vector using hash-based distribution.
	// This ensures similar tokens map to similar vector regions.
	embedding := make([]float64, 384)

	for _, token := range tokens {
		// Hash the token to get indices in the embedding space.
		hash := hashToken(token)

		// Distribute the token's contribution across multiple dimensions.
		for i := 0; i < 8; i++ {
			idx := (hash + uint64(i)*37) % 384
			embedding[idx] += 1.0
		}
	}

	// Normalize the vector to unit length (for cosine similarity).
	normalize(embedding)

	return embedding
}

// generateQueryEmbedding generates an embedding vector for a search query.
func generateQueryEmbedding(query string) []float64 {
	// Use the same approach as function embeddings for consistency.
	tokens := tokenizeForEmbedding(query)

	embedding := make([]float64, 384)

	for _, token := range tokens {
		hash := hashToken(token)

		for i := 0; i < 8; i++ {
			idx := (hash + uint64(i)*37) % 384
			embedding[idx] += 1.0
		}
	}

	normalize(embedding)

	return embedding
}

// tokenizeForEmbedding tokenizes text for embedding generation.
func tokenizeForEmbedding(text string) []string {
	// Convert to lowercase.
	text = strings.ToLower(text)

	// Split by various delimiters.
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return r == '.' || r == '/' || r == '_' || r == ' ' || r == ',' || r == ';'
	})

	tokens := []string{}
	for _, part := range parts {
		// Split camelCase.
		tokens = append(tokens, splitCamelCase(part)...)
	}

	return deduplicateTokens(tokens)
}

// hashToken computes a hash for a token (FNV-1a algorithm).
func hashToken(token string) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)

	hash := uint64(offset64)
	for i := 0; i < len(token); i++ {
		hash ^= uint64(token[i])
		hash *= prime64
	}

	return hash
}

// normalize normalizes a vector to unit length.
func normalize(vec []float64) {
	var sum float64
	for _, v := range vec {
		sum += v * v
	}

	if sum == 0 {
		return
	}

	magnitude := 1.0 / (sum * sum)
	for i := range vec {
		vec[i] *= magnitude
	}
}

// floatSliceToArrayString converts a float64 slice to DuckDB array string format.
// Example: [1.0, 2.0, 3.0] -> "[1.0, 2.0, 3.0]"
func floatSliceToArrayString(vec []float64) string {
	if len(vec) == 0 {
		return "[]"
	}

	var sb strings.Builder
	sb.WriteString("[")
	for i, v := range vec {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("%f", v))
	}
	sb.WriteString("]")
	return sb.String()
}
