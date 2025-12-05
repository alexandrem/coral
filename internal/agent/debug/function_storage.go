package debug

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"time"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/rs/zerolog"
)

// FunctionCache stores discovered functions locally in agent's DuckDB.
// Functions are discovered once per binary and cached until the binary hash changes.
type FunctionCache struct {
	db         *sql.DB
	discoverer *FunctionDiscoverer
	logger     zerolog.Logger
}

// NewFunctionCache creates a new function cache.
func NewFunctionCache(db *sql.DB, logger zerolog.Logger) (*FunctionCache, error) {
	cache := &FunctionCache{
		db:         db,
		discoverer: NewFunctionDiscoverer(logger),
		logger:     logger.With().Str("component", "function_cache").Logger(),
	}

	// Initialize schema.
	if err := cache.initSchema(); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return cache, nil
}

// initSchema creates the functions cache table in agent's local DuckDB.
func (c *FunctionCache) initSchema() error {
	schema := `
		-- Cached functions discovered from service binaries (RFD 063).
		CREATE TABLE IF NOT EXISTS functions_cache (
			service_name     VARCHAR NOT NULL,
			binary_path      VARCHAR NOT NULL,
			binary_hash      VARCHAR(64) NOT NULL,
			function_name    VARCHAR NOT NULL,
			package_name     VARCHAR,
			file_path        VARCHAR,
			line_number      INTEGER,
			offset           BIGINT,
			has_dwarf        BOOLEAN DEFAULT false,
			discovered_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (service_name, function_name)
		);

		-- Track binary hashes to detect when re-discovery is needed.
		CREATE TABLE IF NOT EXISTS binary_hashes (
			service_name  VARCHAR PRIMARY KEY,
			binary_path   VARCHAR NOT NULL,
			binary_hash   VARCHAR(64) NOT NULL,
			last_checked  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			function_count INTEGER DEFAULT 0
		);

		CREATE INDEX IF NOT EXISTS idx_functions_cache_service
		ON functions_cache(service_name);

		CREATE INDEX IF NOT EXISTS idx_functions_cache_binary
		ON functions_cache(binary_hash);
	`

	if _, err := c.db.Exec(schema); err != nil {
		return fmt.Errorf("failed to create function cache schema: %w", err)
	}

	c.logger.Info().Msg("Function cache schema initialized")
	return nil
}

// DiscoverAndCache discovers functions for a service and caches them.
// This should be called when a service connects or when the binary hash changes.
func (c *FunctionCache) DiscoverAndCache(ctx context.Context, serviceName, binaryPath string) error {
	c.logger.Info().
		Str("service", serviceName).
		Str("binary", binaryPath).
		Msg("Discovering and caching functions")

	// Compute binary hash.
	binaryHash, err := computeBinaryHash(binaryPath)
	if err != nil {
		return fmt.Errorf("failed to compute binary hash: %w", err)
	}

	// Check if we already have cached functions for this binary hash.
	var existingHash string
	err = c.db.QueryRowContext(ctx,
		"SELECT binary_hash FROM binary_hashes WHERE service_name = ?",
		serviceName,
	).Scan(&existingHash)

	if err == nil && existingHash == binaryHash {
		c.logger.Info().
			Str("service", serviceName).
			Str("binary_hash", binaryHash[:16]+"...").
			Msg("Functions already cached for this binary version")
		return nil
	}

	// Binary hash has changed or this is a new service - discover functions.
	functions, err := c.discoverer.DiscoverFunctions(binaryPath, serviceName)
	if err != nil {
		return fmt.Errorf("failed to discover functions: %w", err)
	}

	c.logger.Info().
		Str("service", serviceName).
		Int("function_count", len(functions)).
		Str("binary_hash", binaryHash[:16]+"...").
		Msg("Discovered functions from binary")

	// Store in cache.
	if err := c.storeFunctions(ctx, serviceName, binaryPath, binaryHash, functions); err != nil {
		return fmt.Errorf("failed to store functions: %w", err)
	}

	return nil
}

// storeFunctions stores discovered functions in the cache.
func (c *FunctionCache) storeFunctions(ctx context.Context, serviceName, binaryPath, binaryHash string, functions []*agentv1.FunctionInfo) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Clear old functions for this service.
	if _, err := tx.ExecContext(ctx, "DELETE FROM functions_cache WHERE service_name = ?", serviceName); err != nil {
		return fmt.Errorf("failed to clear old functions: %w", err)
	}

	// Insert new functions.
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO functions_cache (
			service_name, binary_path, binary_hash, function_name,
			package_name, file_path, line_number, offset, has_dwarf
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, fn := range functions {
		_, err := stmt.ExecContext(ctx,
			serviceName,
			binaryPath,
			binaryHash,
			fn.Name,
			fn.Package,
			fn.FilePath,
			fn.LineNumber,
			fn.Offset,
			fn.HasDwarf,
		)
		if err != nil {
			return fmt.Errorf("failed to insert function %s: %w", fn.Name, err)
		}
	}

	// Update binary hash tracking.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO binary_hashes (service_name, binary_path, binary_hash, function_count)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (service_name) DO UPDATE SET
			binary_path = EXCLUDED.binary_path,
			binary_hash = EXCLUDED.binary_hash,
			last_checked = CURRENT_TIMESTAMP,
			function_count = EXCLUDED.function_count
	`, serviceName, binaryPath, binaryHash, len(functions))
	if err != nil {
		return fmt.Errorf("failed to update binary hash: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	c.logger.Info().
		Str("service", serviceName).
		Int("function_count", len(functions)).
		Msg("Functions cached successfully")

	return nil
}

// GetCachedFunctions retrieves cached functions for a service.
func (c *FunctionCache) GetCachedFunctions(ctx context.Context, serviceName string) ([]*agentv1.FunctionInfo, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT function_name, package_name, file_path, line_number, offset, has_dwarf
		FROM functions_cache
		WHERE service_name = ?
		ORDER BY function_name
	`, serviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to query cached functions: %w", err)
	}
	defer rows.Close()

	var functions []*agentv1.FunctionInfo
	for rows.Next() {
		var fn agentv1.FunctionInfo
		var packageName, filePath sql.NullString
		var lineNumber sql.NullInt32
		var offset sql.NullInt64

		err := rows.Scan(
			&fn.Name,
			&packageName,
			&filePath,
			&lineNumber,
			&offset,
			&fn.HasDwarf,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan function row: %w", err)
		}

		fn.ServiceName = serviceName
		if packageName.Valid {
			fn.Package = packageName.String
		}
		if filePath.Valid {
			fn.FilePath = filePath.String
		}
		if lineNumber.Valid {
			fn.LineNumber = lineNumber.Int32
		}
		if offset.Valid {
			fn.Offset = offset.Int64
		}

		functions = append(functions, &fn)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating function rows: %w", err)
	}

	return functions, nil
}

// NeedsUpdate checks if a service needs function re-discovery.
// Returns true if binary hash has changed or no cache exists.
func (c *FunctionCache) NeedsUpdate(ctx context.Context, serviceName, binaryPath string) (bool, error) {
	// Compute current binary hash.
	currentHash, err := computeBinaryHash(binaryPath)
	if err != nil {
		return false, fmt.Errorf("failed to compute binary hash: %w", err)
	}

	// Check cached hash.
	var cachedHash string
	err = c.db.QueryRowContext(ctx,
		"SELECT binary_hash FROM binary_hashes WHERE service_name = ?",
		serviceName,
	).Scan(&cachedHash)

	if err == sql.ErrNoRows {
		// No cache exists - needs discovery.
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to query binary hash: %w", err)
	}

	// Compare hashes.
	return currentHash != cachedHash, nil
}

// computeBinaryHash computes SHA256 hash of a binary file.
func computeBinaryHash(binaryPath string) (string, error) {
	f, err := os.Open(binaryPath)
	if err != nil {
		return "", fmt.Errorf("failed to open binary: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("failed to hash binary: %w", err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// GetCacheStats returns statistics about the function cache.
func (c *FunctionCache) GetCacheStats(ctx context.Context) (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Count total services.
	var serviceCount int
	err := c.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM binary_hashes").Scan(&serviceCount)
	if err != nil {
		return nil, err
	}
	stats["service_count"] = serviceCount

	// Count total functions.
	var functionCount int
	err = c.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM functions_cache").Scan(&functionCount)
	if err != nil {
		return nil, err
	}
	stats["function_count"] = functionCount

	// Get per-service breakdown.
	rows, err := c.db.QueryContext(ctx, `
		SELECT service_name, function_count, last_checked
		FROM binary_hashes
		ORDER BY service_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	services := []map[string]interface{}{}
	for rows.Next() {
		var serviceName string
		var funcCount int
		var lastChecked time.Time

		if err := rows.Scan(&serviceName, &funcCount, &lastChecked); err != nil {
			return nil, err
		}

		services = append(services, map[string]interface{}{
			"service_name":   serviceName,
			"function_count": funcCount,
			"last_checked":   lastChecked,
		})
	}
	stats["services"] = services

	return stats, nil
}
