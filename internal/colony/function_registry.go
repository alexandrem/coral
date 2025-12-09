package colony

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/duckdb"
	"github.com/coral-mesh/coral/pkg/embedding"
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
		// If embedding is empty or has wrong size, use NULL instead.
		var embeddingStr interface{}
		if len(fn.Embedding) == 384 {
			embeddingStr = duckdb.Float64ArrayToString(duckdb.Float32ToFloat64(fn.Embedding))
		} else {
			embeddingStr = nil // NULL in SQL
		}

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
	queryEmbedding := embedding.GenerateQueryEmbedding(query)
	queryEmbeddingStr := duckdb.Float64ArrayToString(queryEmbedding)

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
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			r.logger.Warn().Err(closeErr).Msg("Failed to close rows")
		}
	}()

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
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			r.logger.Warn().Err(closeErr).Msg("Failed to close rows")
		}
	}()

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
