// Package profiler implements continuous CPU profiling for the agent.
package profiler

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/duckdb"
)

// Storage handles local storage of continuous CPU profile samples.
type Storage struct {
	db     *sql.DB
	logger zerolog.Logger
	mu     sync.RWMutex

	// Frame dictionary cache: frame_name -> frame_id.
	frameDictCache map[string]int64
	nextFrameID    int64
}

// ProfileSample represents a single aggregated profile sample.
type ProfileSample struct {
	Timestamp     time.Time
	ServiceID     string
	BuildID       string
	StackHash     string  // Hash of stack for deduplication
	StackFrameIDs []int64 // Integer-encoded stack frames
	SampleCount   int
}

// BinaryMetadata represents metadata about a profiled binary.
type BinaryMetadata struct {
	BuildID      string
	ServiceID    string
	BinaryPath   string
	FirstSeen    time.Time
	LastSeen     time.Time
	HasDebugInfo bool
}

// NewStorage creates a new continuous profiling storage.
func NewStorage(db *sql.DB, logger zerolog.Logger) (*Storage, error) {
	s := &Storage{
		db:             db,
		logger:         logger.With().Str("component", "continuous_profiler_storage").Logger(),
		frameDictCache: make(map[string]int64),
	}

	// Initialize schema.
	if err := s.initSchema(); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	// Load existing frame dictionary into cache.
	if err := s.loadFrameDictionary(); err != nil {
		return nil, fmt.Errorf("failed to load frame dictionary: %w", err)
	}

	return s, nil
}

// initSchema creates the local continuous profiling tables.
func (s *Storage) initSchema() error {
	schema := `
		-- Frame dictionary for compression (shared across all profiles).
		CREATE TABLE IF NOT EXISTS profile_frame_dictionary_local (
			frame_id    INTEGER PRIMARY KEY,
			frame_name  TEXT UNIQUE NOT NULL,
			frame_count BIGINT NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_profile_frame_dictionary_name
			ON profile_frame_dictionary_local (frame_name);

		-- Raw 15-second profile samples with integer-encoded stacks.
		CREATE TABLE IF NOT EXISTS cpu_profile_samples_local (
			timestamp        TIMESTAMP  NOT NULL,
			service_id       TEXT       NOT NULL,
			build_id         TEXT       NOT NULL,
			stack_hash       TEXT       NOT NULL,    -- Hash of stack for deduplication
			stack_frame_ids  INTEGER[]  NOT NULL,
			sample_count     INTEGER    NOT NULL,
			PRIMARY KEY (timestamp, service_id, build_id, stack_hash)
		);
		CREATE INDEX IF NOT EXISTS idx_cpu_profile_samples_timestamp
			ON cpu_profile_samples_local (timestamp);
		CREATE INDEX IF NOT EXISTS idx_cpu_profile_samples_service
			ON cpu_profile_samples_local (service_id);
		CREATE INDEX IF NOT EXISTS idx_cpu_profile_samples_build_id
			ON cpu_profile_samples_local (build_id);

		-- Binary metadata for symbol resolution.
		CREATE TABLE IF NOT EXISTS binary_metadata_local (
			build_id       TEXT PRIMARY KEY,
			service_id     TEXT      NOT NULL,
			binary_path    TEXT      NOT NULL,
			first_seen     TIMESTAMP NOT NULL,
			last_seen      TIMESTAMP NOT NULL,
			has_debug_info BOOLEAN   NOT NULL DEFAULT false
		);
		CREATE INDEX IF NOT EXISTS idx_binary_metadata_service
			ON binary_metadata_local (service_id);
	`

	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	// Force WAL checkpoint so remote HTTP clients can see the schema.
	if _, err := s.db.Exec("CHECKPOINT"); err != nil {
		s.logger.Warn().Err(err).Msg("Failed to checkpoint database")
	}

	s.logger.Info().Msg("Continuous profiling storage schema initialized")

	return nil
}

// loadFrameDictionary loads the existing frame dictionary into memory cache.
func (s *Storage) loadFrameDictionary() error {
	rows, err := s.db.Query("SELECT frame_id, frame_name FROM profile_frame_dictionary_local")
	if err != nil {
		return fmt.Errorf("failed to query frame dictionary: %w", err)
	}
	defer func() { _ = rows.Close() }()

	maxFrameID := int64(0)
	for rows.Next() {
		var frameID int64
		var frameName string
		if err := rows.Scan(&frameID, &frameName); err != nil {
			return fmt.Errorf("failed to scan frame dictionary row: %w", err)
		}
		s.frameDictCache[frameName] = frameID
		if frameID > maxFrameID {
			maxFrameID = frameID
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating frame dictionary: %w", err)
	}

	s.nextFrameID = maxFrameID + 1
	s.logger.Info().
		Int("frame_count", len(s.frameDictCache)).
		Int64("next_frame_id", s.nextFrameID).
		Msg("Loaded frame dictionary cache")

	return nil
}

// StoreSample stores a single profile sample with integer-encoded stacks.
func (s *Storage) StoreSample(ctx context.Context, sample ProfileSample) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Convert integer array to DuckDB LIST format.
	frameIDsStr := duckdb.Int64ArrayToString(sample.StackFrameIDs)

	// #nosec G202 - frameIDsStr is a formatted integer array, not user input.
	query := `
		INSERT INTO cpu_profile_samples_local (
			timestamp, service_id, build_id, stack_hash, stack_frame_ids, sample_count
		) VALUES (?, ?, ?, ?, ` + frameIDsStr + `, ?)
		ON CONFLICT (timestamp, service_id, build_id, stack_hash)
		DO UPDATE SET sample_count = cpu_profile_samples_local.sample_count + EXCLUDED.sample_count
	`

	_, err := s.db.ExecContext(ctx, query,
		sample.Timestamp,
		sample.ServiceID,
		sample.BuildID,
		sample.StackHash,
		sample.SampleCount,
	)

	if err != nil {
		return fmt.Errorf("failed to store sample: %w", err)
	}

	return nil
}

// StoreSamples stores multiple profile samples in a single transaction.
func (s *Storage) StoreSamples(ctx context.Context, samples []ProfileSample) error {
	if len(samples) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, sample := range samples {
		frameIDsStr := duckdb.Int64ArrayToString(sample.StackFrameIDs)

		// #nosec G202 - frameIDsStr is a formatted integer array, not user input.
		query := `
			INSERT INTO cpu_profile_samples_local (
				timestamp, service_id, build_id, stack_hash, stack_frame_ids, sample_count
			) VALUES (?, ?, ?, ?, ` + frameIDsStr + `, ?)
			ON CONFLICT (timestamp, service_id, build_id, stack_hash)
			DO UPDATE SET sample_count = cpu_profile_samples_local.sample_count + EXCLUDED.sample_count
		`

		_, err := tx.ExecContext(ctx, query,
			sample.Timestamp,
			sample.ServiceID,
			sample.BuildID,
			sample.StackHash,
			sample.SampleCount,
		)
		if err != nil {
			return fmt.Errorf("failed to store sample: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// UpsertBinaryMetadata updates or creates binary metadata entry.
func (s *Storage) UpsertBinaryMetadata(ctx context.Context, metadata BinaryMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
		INSERT INTO binary_metadata_local (
			build_id, service_id, binary_path, first_seen, last_seen, has_debug_info
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (build_id) DO UPDATE SET
			last_seen = EXCLUDED.last_seen,
			has_debug_info = EXCLUDED.has_debug_info
	`

	_, err := s.db.ExecContext(ctx, query,
		metadata.BuildID,
		metadata.ServiceID,
		metadata.BinaryPath,
		metadata.FirstSeen,
		metadata.LastSeen,
		metadata.HasDebugInfo,
	)

	if err != nil {
		return fmt.Errorf("failed to upsert binary metadata: %w", err)
	}

	return nil
}

// QuerySamples retrieves profile samples for a given time range.
func (s *Storage) QuerySamples(ctx context.Context, startTime, endTime time.Time, serviceID string) ([]ProfileSample, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
		SELECT timestamp, service_id, build_id, stack_frame_ids, sample_count
		FROM cpu_profile_samples_local
		WHERE timestamp >= ? AND timestamp <= ?
	`
	args := []interface{}{startTime, endTime}

	if serviceID != "" {
		query += " AND service_id = ?"
		args = append(args, serviceID)
	}

	query += " ORDER BY timestamp ASC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query samples: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var samples []ProfileSample
	for rows.Next() {
		var sample ProfileSample
		var frameIDsInterface interface{}

		err := rows.Scan(
			&sample.Timestamp,
			&sample.ServiceID,
			&sample.BuildID,
			&frameIDsInterface,
			&sample.SampleCount,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Convert DuckDB array to []int64.
		sample.StackFrameIDs, err = s.convertArrayToInt64(frameIDsInterface)
		if err != nil {
			s.logger.Warn().Err(err).Msg("Failed to convert stack frame IDs")
			continue
		}

		samples = append(samples, sample)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return samples, nil
}

// encodeStackFrames converts frame names to integer-encoded frame IDs.
func (s *Storage) encodeStackFrames(ctx context.Context, frameNames []string) ([]int64, error) {
	if len(frameNames) == 0 {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	frameIDs := make([]int64, len(frameNames))

	// Process each frame name.
	for i, frameName := range frameNames {
		// Check cache first.
		if frameID, exists := s.frameDictCache[frameName]; exists {
			frameIDs[i] = frameID

			// Increment frame count.
			_, err := s.db.ExecContext(ctx, `
				UPDATE profile_frame_dictionary_local
				SET frame_count = frame_count + 1
				WHERE frame_id = ?
			`, frameID)
			if err != nil {
				s.logger.Warn().Err(err).Int64("frame_id", frameID).Msg("Failed to increment frame count")
			}
			continue
		}

		// Not in cache - assign new ID and insert.
		frameID := s.nextFrameID
		s.nextFrameID++

		_, err := s.db.ExecContext(ctx, `
			INSERT INTO profile_frame_dictionary_local (frame_id, frame_name, frame_count)
			VALUES (?, ?, 1)
		`, frameID, frameName)
		if err != nil {
			return nil, fmt.Errorf("failed to insert frame %q: %w", frameName, err)
		}

		// Add to cache.
		s.frameDictCache[frameName] = frameID
		frameIDs[i] = frameID
	}

	return frameIDs, nil
}

// DecodeStackFrames converts integer-encoded frame IDs back to frame names.
func (s *Storage) DecodeStackFrames(ctx context.Context, frameIDs []int64) ([]string, error) {
	if len(frameIDs) == 0 {
		return nil, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Build placeholders for IN clause.
	placeholders := strings.Repeat("?,", len(frameIDs))
	placeholders = placeholders[:len(placeholders)-1]

	// #nosec G201 - placeholders is a generated string of "?,?,?...", not user input.
	query := fmt.Sprintf(`
		SELECT frame_id, frame_name
		FROM profile_frame_dictionary_local
		WHERE frame_id IN (%s)
	`, placeholders)

	args := make([]interface{}, len(frameIDs))
	for i, id := range frameIDs {
		args[i] = id
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query frame names: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// Build map of frame_id -> frame_name.
	frameMap := make(map[int64]string)
	for rows.Next() {
		var frameID int64
		var frameName string
		if err := rows.Scan(&frameID, &frameName); err != nil {
			return nil, fmt.Errorf("failed to scan frame: %w", err)
		}
		frameMap[frameID] = frameName
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating frames: %w", err)
	}

	// Reconstruct stack in original order.
	frameNames := make([]string, len(frameIDs))
	for i, frameID := range frameIDs {
		if frameName, exists := frameMap[frameID]; exists {
			frameNames[i] = frameName
		} else {
			frameNames[i] = fmt.Sprintf("unknown_frame_%d", frameID)
		}
	}

	return frameNames, nil
}

// CleanupOldSamples removes profile samples older than the retention period.
func (s *Storage) CleanupOldSamples(ctx context.Context, retention time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-retention)
	query := `DELETE FROM cpu_profile_samples_local WHERE timestamp < ?`

	result, err := s.db.ExecContext(ctx, query, cutoff)
	if err != nil {
		return fmt.Errorf("failed to cleanup old samples: %w", err)
	}

	rowsDeleted, _ := result.RowsAffected()
	if rowsDeleted > 0 {
		s.logger.Debug().
			Int64("rows_deleted", rowsDeleted).
			Time("cutoff", cutoff).
			Msg("Cleaned up old profile samples")
	}

	return nil
}

// CleanupOldBinaryMetadata removes binary metadata older than the retention period.
func (s *Storage) CleanupOldBinaryMetadata(ctx context.Context, retention time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-retention)
	query := `DELETE FROM binary_metadata_local WHERE last_seen < ?`

	result, err := s.db.ExecContext(ctx, query, cutoff)
	if err != nil {
		return fmt.Errorf("failed to cleanup old binary metadata: %w", err)
	}

	rowsDeleted, _ := result.RowsAffected()
	if rowsDeleted > 0 {
		s.logger.Debug().
			Int64("rows_deleted", rowsDeleted).
			Time("cutoff", cutoff).
			Msg("Cleaned up old binary metadata")
	}

	return nil
}

// RunCleanupLoop runs periodic cleanup goroutines for samples and metadata.
func (s *Storage) RunCleanupLoop(ctx context.Context, sampleRetention, metadataRetention time.Duration) {
	sampleTicker := time.NewTicker(10 * time.Minute)
	metadataTicker := time.NewTicker(24 * time.Hour)
	defer sampleTicker.Stop()
	defer metadataTicker.Stop()

	s.logger.Info().
		Dur("sample_retention", sampleRetention).
		Dur("metadata_retention", metadataRetention).
		Msg("Starting continuous profiling cleanup loop")

	for {
		select {
		case <-ctx.Done():
			s.logger.Info().Msg("Stopping continuous profiling cleanup loop")
			return
		case <-sampleTicker.C:
			if err := s.CleanupOldSamples(ctx, sampleRetention); err != nil {
				s.logger.Error().Err(err).Msg("Failed to cleanup old profile samples")
			}
		case <-metadataTicker.C:
			if err := s.CleanupOldBinaryMetadata(ctx, metadataRetention); err != nil {
				s.logger.Error().Err(err).Msg("Failed to cleanup old binary metadata")
			}
		}
	}
}

// computeStackHash computes a hash for a stack trace for deduplication.
func computeStackHash(frameIDs []int64) string {
	// Simple hash: join frame IDs with semicolons.
	// This matches what the colony database uses.
	parts := make([]string, len(frameIDs))
	for i, id := range frameIDs {
		parts[i] = fmt.Sprintf("%d", id)
	}
	return strings.Join(parts, ";")
}

// convertArrayToInt64 converts a DuckDB array ([]interface{}) to []int64.
func (s *Storage) convertArrayToInt64(val interface{}) ([]int64, error) {
	if val == nil {
		return nil, nil
	}

	// DuckDB Go driver returns arrays as []interface{}.
	arr, ok := val.([]interface{})
	if !ok {
		// Fallback: Try as string for backwards compatibility.
		if str, ok := val.(string); ok {
			return duckdb.ParseInt64Array(str)
		}
		return nil, fmt.Errorf("unexpected type for array: %T", val)
	}

	ids := make([]int64, len(arr))
	for i, elem := range arr {
		switch v := elem.(type) {
		case int64:
			ids[i] = v
		case int32:
			ids[i] = int64(v)
		case int:
			ids[i] = int64(v)
		case float64:
			ids[i] = int64(v)
		default:
			return nil, fmt.Errorf("unexpected array element type: %T", elem)
		}
	}
	return ids, nil
}
