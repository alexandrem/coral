package debug

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/agent/ebpf/binaryscanner"
	"github.com/coral-mesh/coral/internal/duckdb"
	"github.com/coral-mesh/coral/pkg/embedding"
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
			func_offset      BIGINT,
			has_dwarf        BOOLEAN DEFAULT false,
			embedding        FLOAT[384],
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
// If sdkAddr is provided, it will fetch functions from the SDK HTTP API.
// Otherwise, it falls back to DWARF parsing.
func (c *FunctionCache) DiscoverAndCache(ctx context.Context, serviceName, binaryPath, sdkAddr string) error {
	return c.DiscoverAndCacheWithHash(ctx, serviceName, binaryPath, sdkAddr, "")
}

// DiscoverAndCacheWithHash discovers functions with an optional pre-computed binary hash.
// If binaryHash is provided (e.g., from SDK capabilities), it's used directly.
// Otherwise, the hash is computed from the binary file.
//
// Discovery fallback chain (RFD 065):
//  1. SDK HTTP API (if sdkAddr provided)
//  2. Binary Scanner (if PID available, no SDK)
//  3. Direct DWARF parsing (fallback)
func (c *FunctionCache) DiscoverAndCacheWithHash(ctx context.Context, serviceName, binaryPath, sdkAddr, binaryHash string) error {
	return c.DiscoverAndCacheWithPID(ctx, serviceName, binaryPath, sdkAddr, binaryHash, 0)
}

// DiscoverAndCacheWithPID discovers functions with optional PID for binary scanner fallback.
// PID enables agentless binary scanning (RFD 065) when SDK is not available.
func (c *FunctionCache) DiscoverAndCacheWithPID(ctx context.Context, serviceName, binaryPath, sdkAddr, binaryHash string, pid uint32) error {
	c.logger.Info().
		Str("service", serviceName).
		Str("binary", binaryPath).
		Str("sdk_addr", sdkAddr).
		Uint32("pid", pid).
		Msg("Discovering and caching functions")

	var err error

	// Compute binary hash if not provided.
	if binaryHash == "" {
		binaryHash, err = computeBinaryHash(binaryPath)
		if err != nil {
			// If we can't compute the hash and SDK is available, we can still proceed.
			// This handles Docker scenarios where the binary is in a different container.
			if sdkAddr != "" || pid > 0 {
				c.logger.Warn().
					Err(err).
					Str("service", serviceName).
					Msg("Cannot access binary file (cross-container), using SDK/scanner-only mode")
				// Use a placeholder hash based on service name.
				// This is safe because we'll re-discover if the binary changes.
				binaryHash = fmt.Sprintf("runtime-%s", serviceName)
			} else {
				return fmt.Errorf("failed to compute binary hash: %w", err)
			}
		}
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

	var functions []*agentv1.FunctionInfo

	// Priority 1: Try SDK HTTP API first if SDK address is provided.
	if sdkAddr != "" {
		c.logger.Info().
			Str("service", serviceName).
			Str("sdk_addr", sdkAddr).
			Msg("Fetching functions from SDK HTTP API")

		var sdkErr error
		functions, sdkErr = c.fetchFunctionsFromSDK(ctx, serviceName, sdkAddr)
		if sdkErr != nil {
			c.logger.Warn().
				Err(sdkErr).
				Str("service", serviceName).
				Msg("Failed to fetch functions from SDK, falling back to binary scanner")
			// Fall through to binary scanner
		} else {
			c.logger.Info().
				Str("service", serviceName).
				Int("function_count", len(functions)).
				Msg("Successfully fetched functions from SDK HTTP API")
		}
	}

	// Priority 2: Try Binary Scanner if PID provided and SDK failed/unavailable (RFD 065).
	if functions == nil && pid > 0 {
		c.logger.Info().
			Str("service", serviceName).
			Uint32("pid", pid).
			Msg("Discovering functions via binary scanner (agentless)")

		var scanErr error
		functions, scanErr = c.fetchFunctionsFromBinaryScanner(ctx, serviceName, pid)
		if scanErr != nil {
			c.logger.Warn().
				Err(scanErr).
				Str("service", serviceName).
				Msg("Failed to discover functions via binary scanner, falling back to DWARF parsing")
			// Fall through to DWARF parsing
		} else {
			c.logger.Info().
				Str("service", serviceName).
				Int("function_count", len(functions)).
				Msg("Successfully discovered functions via binary scanner")
		}
	}

	// Priority 3: Fall back to direct DWARF parsing if all else failed.
	if functions == nil {
		c.logger.Info().
			Str("service", serviceName).
			Str("binary", binaryPath).
			Msg("Discovering functions via direct DWARF parsing")

		functions, err = c.discoverer.DiscoverFunctions(binaryPath, serviceName)
		if err != nil {
			return fmt.Errorf("failed to discover functions via DWARF: %w", err)
		}
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
			package_name, file_path, line_number, func_offset, has_dwarf, embedding
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?::FLOAT[384])
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer func() {
		if closeErr := stmt.Close(); closeErr != nil {
			c.logger.Warn().Err(closeErr).Msg("Failed to close statement")
		}
	}()

	for _, fn := range functions {
		// Convert []float32 embedding to DuckDB array string format.
		var embeddingStr interface{}
		if len(fn.Embedding) == 384 {
			embeddingStr = duckdb.Float64ArrayToString(duckdb.Float32ToFloat64(fn.Embedding))
		} else {
			embeddingStr = nil // NULL if wrong size
		}

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
			embeddingStr,
		)
		if err != nil {
			return fmt.Errorf("failed to insert function %s: %w", fn.Name, err)
		}
	}

	// Update binary hash tracking.
	// Note: DuckDB doesn't support CURRENT_TIMESTAMP in ON CONFLICT UPDATE,
	// so we update last_checked in a separate statement.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO binary_hashes (service_name, binary_path, binary_hash, function_count)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (service_name) DO UPDATE SET
			binary_path = EXCLUDED.binary_path,
			binary_hash = EXCLUDED.binary_hash,
			function_count = EXCLUDED.function_count
	`, serviceName, binaryPath, binaryHash, len(functions))
	if err != nil {
		return fmt.Errorf("failed to upsert binary hash: %w", err)
	}

	// Update last_checked timestamp.
	_, err = tx.ExecContext(ctx, `
		UPDATE binary_hashes
		SET last_checked = CURRENT_TIMESTAMP
		WHERE service_name = ?
	`, serviceName)
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
		SELECT function_name, package_name, file_path, line_number, func_offset, has_dwarf, embedding
		FROM functions_cache
		WHERE service_name = ?
		ORDER BY function_name
	`, serviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to query cached functions: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			c.logger.Warn().Err(closeErr).Msg("Failed to close rows")
		}
	}()

	var functions []*agentv1.FunctionInfo
	for rows.Next() {
		var fn agentv1.FunctionInfo
		var packageName, filePath sql.NullString
		var lineNumber sql.NullInt32
		var offset sql.NullInt64
		var embeddingData interface{} // Can be string or bytes depending on DuckDB version

		err := rows.Scan(
			&fn.Name,
			&packageName,
			&filePath,
			&lineNumber,
			&offset,
			&fn.HasDwarf,
			&embeddingData,
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

		// Convert embedding data back to []float32 if present.
		if embeddingData != nil {
			switch v := embeddingData.(type) {
			case []byte:
				// DuckDB returns FLOAT arrays as raw bytes.
				fn.Embedding = duckdb.BytesToFloat32Array(v)
				c.logger.Debug().
					Str("function", fn.Name).
					Int("embedding_bytes", len(v)).
					Int("embedding_floats", len(fn.Embedding)).
					Msg("Loaded embedding from cache")
			case string:
				// Some DuckDB versions might return as string.
				// Skip string parsing for now - shouldn't happen.
				c.logger.Warn().
					Str("function", fn.Name).
					Msg("Embedding returned as string, skipping")
			default:
				c.logger.Warn().
					Str("function", fn.Name).
					Str("type", fmt.Sprintf("%T", v)).
					Msg("Unexpected embedding data type")
			}
		} else {
			c.logger.Err(errors.New("missing embedding data in functions cache")).
				Str("function", fn.Name).
				Msg("No embedding in cache for function")
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
	//nolint:gosec // G304: binaryPath is from controlled internal sources
	f, err := os.Open(binaryPath)
	if err != nil {
		return "", fmt.Errorf("failed to open binary: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("failed to hash binary: %w", err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// BasicFunctionInfo represents minimal function metadata before enrichment.
// JSON tags allow direct unmarshaling from SDK NDJSON export format.
type BasicFunctionInfo struct {
	Name   string `json:"name"`
	Offset uint64 `json:"offset"`
	File   string `json:"file"`
	Line   int    `json:"line"`
}

// enrichAndDeduplicateFunctions converts basic function info to FunctionInfo with embeddings.
// This common logic was previously duplicated across all 3 discovery methods (SDK, Scanner, DWARF).
// Making it a package-level function allows reuse across FunctionCache and FunctionDiscoverer.
func enrichAndDeduplicateFunctions(
	basicFunctions []BasicFunctionInfo,
	serviceName string,
	hasDwarf bool,
	logger zerolog.Logger,
) []*agentv1.FunctionInfo {
	functions := make([]*agentv1.FunctionInfo, 0, len(basicFunctions))
	seenFunctions := make(map[string]bool)
	duplicates := 0

	for _, fn := range basicFunctions {
		// Skip duplicates (Go binaries can have duplicate function names).
		// Keep first occurrence only to satisfy PRIMARY KEY (service_name, function_name).
		if seenFunctions[fn.Name] {
			duplicates++
			continue
		}
		seenFunctions[fn.Name] = true

		// Generate embedding for semantic search.
		emb := embedding.GenerateFunctionEmbedding(embedding.FunctionMetadata{
			Name:       fn.Name,
			Package:    extractPackageName(fn.Name),
			FilePath:   fn.File,
			Parameters: nil,
		})

		// Convert []float64 to []float32 for protobuf.
		emb32 := make([]float32, len(emb))
		for i, v := range emb {
			emb32[i] = float32(v)
		}

		functions = append(functions, &agentv1.FunctionInfo{
			Name:        fn.Name,
			Package:     extractPackageName(fn.Name),
			FilePath:    fn.File,
			LineNumber:  int32(fn.Line),
			Offset:      int64(fn.Offset),
			HasDwarf:    hasDwarf,
			ServiceName: serviceName,
			Embedding:   emb32,
		})
	}

	if duplicates > 0 {
		logger.Debug().
			Int("duplicates_skipped", duplicates).
			Msg("Skipped duplicate function names")
	}

	return functions
}

// fetchFunctionsFromSDK fetches functions from the SDK HTTP API using bulk export.
// This uses the /debug/functions/export endpoint with NDJSON format for efficient streaming.
func (c *FunctionCache) fetchFunctionsFromSDK(ctx context.Context, serviceName, sdkAddr string) ([]*agentv1.FunctionInfo, error) {
	// Use the bulk export endpoint for efficient retrieval (RFD 066).
	// This streams NDJSON data which is much faster than fetching functions individually.
	exportURL := fmt.Sprintf("http://%s/debug/functions/export?format=ndjson", sdkAddr)

	c.logger.Info().
		Str("service", serviceName).
		Str("url", exportURL).
		Msg("Fetching functions from SDK via bulk NDJSON export")

	// Create HTTP request with context.
	req, err := http.NewRequestWithContext(ctx, "GET", exportURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create export request: %w", err)
	}

	// Execute request.
	client := &http.Client{Timeout: 60 * time.Second} // Longer timeout for large exports
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch export: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("export endpoint returned status %d", resp.StatusCode)
	}

	// Check if response is gzipped (RFD 066 specifies Content-Type: application/gzip).
	// The export endpoint always returns gzipped data.
	var reader io.Reader = resp.Body
	contentType := resp.Header.Get("Content-Type")
	contentEncoding := resp.Header.Get("Content-Encoding")

	if contentType == "application/gzip" || contentEncoding == "gzip" {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer func() { _ = gzReader.Close() }()
		reader = gzReader
	}

	// Parse NDJSON stream line by line.
	scanner := bufio.NewScanner(reader)
	basicFunctions := make([]BasicFunctionInfo, 0, 1000) // Pre-allocate reasonable size

	lineCount := 0
	for scanner.Scan() {
		lineCount++
		var fn BasicFunctionInfo
		if err := json.Unmarshal(scanner.Bytes(), &fn); err != nil {
			c.logger.Warn().
				Err(err).
				Int("line", lineCount).
				Msg("Failed to parse function from NDJSON, skipping")
			continue
		}

		basicFunctions = append(basicFunctions, fn)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading NDJSON stream: %w", err)
	}

	c.logger.Info().
		Str("service", serviceName).
		Int("raw_function_count", len(basicFunctions)).
		Msg("Fetched functions from SDK, now enriching with embeddings")

	// Enrich with embeddings and deduplicate.
	functions := enrichAndDeduplicateFunctions(basicFunctions, serviceName, true, c.logger)

	c.logger.Info().
		Str("service", serviceName).
		Int("function_count", len(functions)).
		Msg("Successfully fetched functions from SDK bulk export")

	return functions, nil
}

// fetchFunctionsFromBinaryScanner discovers functions via binary scanning (RFD 065).
// This is used when SDK is not available but we have a PID for the target process.
func (c *FunctionCache) fetchFunctionsFromBinaryScanner(ctx context.Context, serviceName string, pid uint32) ([]*agentv1.FunctionInfo, error) {
	c.logger.Info().
		Str("service", serviceName).
		Uint32("pid", pid).
		Msg("Discovering functions via binary scanner (agentless)")

	// Create binary scanner with default configuration.
	cfg := binaryscanner.DefaultConfig()
	// Convert zerolog.Logger to slog.Logger for binary scanner.
	cfg.Logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	scanner, err := binaryscanner.NewScanner(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create binary scanner: %w", err)
	}
	defer func() {
		if closeErr := scanner.Close(); closeErr != nil {
			c.logger.Warn().Err(closeErr).Msg("Failed to close binary scanner")
		}
	}()

	// Fetch all functions from the binary via scanner.
	scannerFunctions, err := scanner.ListAllFunctions(ctx, pid)
	if err != nil {
		return nil, fmt.Errorf("failed to list functions via binary scanner: %w", err)
	}

	c.logger.Info().
		Str("service", serviceName).
		Int("raw_function_count", len(scannerFunctions)).
		Msg("Retrieved functions from binary scanner, now enriching with embeddings")

	// Convert scanner output to BasicFunctionInfo.
	basicFunctions := make([]BasicFunctionInfo, len(scannerFunctions))
	for i, fn := range scannerFunctions {
		basicFunctions[i] = BasicFunctionInfo{
			Name:   fn.Name,
			Offset: fn.Offset,
			File:   fn.File,
			Line:   fn.Line,
		}
	}

	// Enrich with embeddings and deduplicate.
	functions := enrichAndDeduplicateFunctions(basicFunctions, serviceName, true, c.logger)

	c.logger.Info().
		Str("service", serviceName).
		Int("function_count", len(functions)).
		Msg("Successfully discovered functions via binary scanner with embeddings")

	return functions, nil
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
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			c.logger.Warn().Err(closeErr).Msg("Failed to close rows")
		}
	}()

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
