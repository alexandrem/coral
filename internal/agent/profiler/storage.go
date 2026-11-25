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

// ORM models for profiler tables.

type binaryMetadataDB struct {
	BuildID      string    `duckdb:"build_id,pk"`
	ServiceID    string    `duckdb:"service_id,immutable"`
	BinaryPath   string    `duckdb:"binary_path,immutable"`
	FirstSeen    time.Time `duckdb:"first_seen,immutable"`
	LastSeen     time.Time `duckdb:"last_seen"`
	HasDebugInfo bool      `duckdb:"has_debug_info"`
}

// Storage handles local storage of continuous CPU profile samples.
type Storage struct {
	db     *sql.DB
	logger zerolog.Logger
	mu     sync.RWMutex

	// Frame dictionary cache: frame_name -> frame_id.
	frameDictCache map[string]int64
	nextFrameID    int64

	// ORM tables.
	binaryMetadataTable *duckdb.Table[binaryMetadataDB]
}

// ProfileSample represents a single aggregated CPU profile sample.
type ProfileSample struct {
	Timestamp     time.Time
	ServiceID     string
	BuildID       string
	StackHash     string  // Hash of stack for deduplication.
	StackFrameIDs []int64 // Integer-encoded stack frames.
	SampleCount   uint32  // Number of samples (matches protobuf).
	SeqID         uint64  // Sequence ID for checkpoint-based polling (RFD 089).
	TGID          uint32  // OS process ID (TGID) of the profiled process (RFD 078).
}

// MemoryProfileSample represents a single aggregated memory profile sample (RFD 077).
type MemoryProfileSample struct {
	Timestamp     time.Time
	ServiceID     string
	BuildID       string
	StackHash     string  // Hash of stack for deduplication.
	StackFrameIDs []int64 // Integer-encoded stack frames.
	AllocBytes    int64   // Bytes allocated at this stack.
	AllocObjects  int64   // Objects allocated at this stack.
	SeqID         uint64  // Sequence ID for checkpoint-based polling (RFD 089).
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
		db:                  db,
		logger:              logger.With().Str("component", "continuous_profiler_storage").Logger(),
		frameDictCache:      make(map[string]int64),
		binaryMetadataTable: duckdb.NewTable[binaryMetadataDB](db, "binary_metadata_local"),
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
		-- Sequences for checkpoint-based polling (RFD 089).
		CREATE SEQUENCE IF NOT EXISTS seq_cpu_profile_samples START 1;
		CREATE SEQUENCE IF NOT EXISTS seq_memory_profile_samples START 1;

		-- Frame dictionary for compression (shared across all profiles).
		CREATE TABLE IF NOT EXISTS profile_frame_dictionary_local (
			frame_id    INTEGER PRIMARY KEY,
			frame_name  TEXT UNIQUE NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_profile_frame_dictionary_name
			ON profile_frame_dictionary_local (frame_name);

		-- Raw 15-second profile samples with integer-encoded stacks.
		CREATE TABLE IF NOT EXISTS cpu_profile_samples_local (
			seq_id           UBIGINT DEFAULT nextval('seq_cpu_profile_samples'),
			timestamp        TIMESTAMP  NOT NULL,
			service_id       TEXT       NOT NULL,
			build_id         TEXT       NOT NULL,
			tgid             INTEGER    NOT NULL DEFAULT 0,
			stack_hash       TEXT       NOT NULL,    -- Hash of stack for deduplication.
			stack_frame_ids  INTEGER[]  NOT NULL,
			sample_count     INTEGER    NOT NULL,
			PRIMARY KEY (timestamp, service_id, build_id, tgid, stack_hash)
		);
		CREATE INDEX IF NOT EXISTS idx_cpu_profile_samples_timestamp
			ON cpu_profile_samples_local (timestamp);
		CREATE INDEX IF NOT EXISTS idx_cpu_profile_samples_service
			ON cpu_profile_samples_local (service_id);
		CREATE INDEX IF NOT EXISTS idx_cpu_profile_samples_build_id
			ON cpu_profile_samples_local (build_id);
		CREATE INDEX IF NOT EXISTS idx_cpu_profile_samples_seq_id
			ON cpu_profile_samples_local (seq_id);
		CREATE INDEX IF NOT EXISTS idx_cpu_profile_samples_tgid
			ON cpu_profile_samples_local (tgid, timestamp);

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

		-- Memory profile samples with integer-encoded stacks (RFD 077).
		CREATE TABLE IF NOT EXISTS memory_profile_samples_local (
			seq_id           UBIGINT DEFAULT nextval('seq_memory_profile_samples'),
			timestamp        TIMESTAMP  NOT NULL,
			service_id       TEXT       NOT NULL,
			build_id         TEXT       NOT NULL,
			stack_hash       TEXT       NOT NULL,
			stack_frame_ids  INTEGER[]  NOT NULL,
			alloc_bytes      BIGINT     NOT NULL,
			alloc_objects    BIGINT     NOT NULL,
			PRIMARY KEY (timestamp, service_id, build_id, stack_hash)
		);
		CREATE INDEX IF NOT EXISTS idx_memory_profile_samples_timestamp
			ON memory_profile_samples_local (timestamp);
		CREATE INDEX IF NOT EXISTS idx_memory_profile_samples_service
			ON memory_profile_samples_local (service_id);
		CREATE INDEX IF NOT EXISTS idx_memory_profile_samples_seq_id
			ON memory_profile_samples_local (seq_id);
	`

	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	// Force WAL checkpoint so remote HTTP clients can see the schema.
	// A fatal error invalidates the database, so stop rather than run more statements.
	if _, err := s.db.Exec("CHECKPOINT"); err != nil {
		if duckdb.IsFatal(err) {
			return fmt.Errorf("failed to checkpoint database: %w", err)
		}
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

// sampleBatchSize bounds the number of rows bound into a single batched
// statement, keeping the SQL text and argument count reasonable.
const sampleBatchSize = 500

// cpuSampleKey identifies a CPU profile sample row (the table's primary key).
type cpuSampleKey struct {
	timestamp time.Time
	serviceID string
	buildID   string
	tgid      uint32
	stackHash string
}

// StoreSamples stores multiple profile samples in a single transaction.
//
// Samples sharing a primary key are summed, both within the batch and with
// rows already stored. Each batch is written with one UPDATE for existing rows
// and one INSERT for new rows, rather than a statement pair per sample. Only
// new rows consume a seq_id, which keeps the sequence free of gaps that the
// colony would report as data loss.
func (s *Storage) StoreSamples(ctx context.Context, samples []ProfileSample) error {
	if len(samples) == 0 {
		return nil
	}

	samples = mergeCPUSamples(samples)

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for start := 0; start < len(samples); start += sampleBatchSize {
		batch := samples[start:min(start+sampleBatchSize, len(samples))]

		rows := make([]string, len(batch))
		args := make([]any, 0, len(batch)*6)
		for i, sample := range batch {
			// #nosec G202 - the array literal is formatted integers, not user input.
			rows[i] = "(?::TIMESTAMP, ?::TEXT, ?::TEXT, ?::INTEGER, ?::TEXT, " +
				duckdb.Int64ArrayToString(sample.StackFrameIDs) + "::INTEGER[], ?::INTEGER)"
			args = append(args,
				sample.Timestamp,
				sample.ServiceID,
				sample.BuildID,
				sample.TGID,
				sample.StackHash,
				sample.SampleCount,
			)
		}
		values := "(VALUES " + strings.Join(rows, ", ") + `) AS b(
			timestamp, service_id, build_id, tgid, stack_hash, stack_frame_ids, sample_count
		)`
		match := `t.timestamp = b.timestamp AND t.service_id = b.service_id
			AND t.build_id = b.build_id AND t.tgid = b.tgid AND t.stack_hash = b.stack_hash`

		// Update existing rows first so the INSERT below only sees new keys.
		// #nosec G202 - values and match are built from constants and placeholders.
		updateQuery := `
			UPDATE cpu_profile_samples_local AS t
			SET sample_count = t.sample_count + b.sample_count
			FROM ` + values + `
			WHERE ` + match
		if _, err := tx.ExecContext(ctx, updateQuery, args...); err != nil {
			return fmt.Errorf("failed to update samples: %w", err)
		}

		// #nosec G202 - values and match are built from constants and placeholders.
		insertQuery := `
			INSERT INTO cpu_profile_samples_local (
				timestamp, service_id, build_id, tgid, stack_hash, stack_frame_ids, sample_count
			)
			SELECT b.timestamp, b.service_id, b.build_id, b.tgid, b.stack_hash, b.stack_frame_ids, b.sample_count
			FROM ` + values + `
			WHERE NOT EXISTS (SELECT 1 FROM cpu_profile_samples_local AS t WHERE ` + match + `)`
		if _, err := tx.ExecContext(ctx, insertQuery, args...); err != nil {
			return fmt.Errorf("failed to insert samples: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// mergeCPUSamples sums the counts of samples sharing a primary key, preserving
// the order of first occurrence. Distinct kernel stacks can symbolize to the
// same frames, so a single drain may produce duplicate keys.
func mergeCPUSamples(samples []ProfileSample) []ProfileSample {
	index := make(map[cpuSampleKey]int, len(samples))
	merged := make([]ProfileSample, 0, len(samples))
	for _, sample := range samples {
		key := cpuSampleKey{sample.Timestamp, sample.ServiceID, sample.BuildID, sample.TGID, sample.StackHash}
		if i, ok := index[key]; ok {
			merged[i].SampleCount += sample.SampleCount
			continue
		}
		index[key] = len(merged)
		merged = append(merged, sample)
	}
	return merged
}

// UpsertBinaryMetadata updates or creates binary metadata entry.
func (s *Storage) UpsertBinaryMetadata(ctx context.Context, metadata BinaryMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	item := &binaryMetadataDB{
		BuildID:      metadata.BuildID,
		ServiceID:    metadata.ServiceID,
		BinaryPath:   metadata.BinaryPath,
		FirstSeen:    metadata.FirstSeen,
		LastSeen:     metadata.LastSeen,
		HasDebugInfo: metadata.HasDebugInfo,
	}

	if err := s.binaryMetadataTable.Upsert(ctx, item); err != nil {
		return fmt.Errorf("failed to upsert binary metadata: %w", err)
	}

	return nil
}

// QuerySamplesBySeqID queries CPU profile samples with seq_id > startSeqID (RFD 089).
// Returns samples and the maximum seq_id in the result set.
func (s *Storage) QuerySamplesBySeqID(ctx context.Context, startSeqID uint64, maxRecords int32, serviceID string) ([]ProfileSample, uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if maxRecords <= 0 {
		maxRecords = 5000
	} else if maxRecords > 50000 {
		maxRecords = 50000
	}

	query := `
		SELECT seq_id, timestamp, service_id, build_id, tgid, stack_frame_ids, sample_count
		FROM cpu_profile_samples_local
		WHERE seq_id > ?
	`
	args := []interface{}{startSeqID}

	if serviceID != "" {
		query += " AND service_id = ?"
		args = append(args, serviceID)
	}

	query += " ORDER BY seq_id ASC LIMIT ?"
	args = append(args, maxRecords)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query samples by seq_id: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var samples []ProfileSample
	var maxSeqID uint64

	for rows.Next() {
		var sample ProfileSample
		var frameIDsInterface interface{}

		err := rows.Scan(
			&sample.SeqID,
			&sample.Timestamp,
			&sample.ServiceID,
			&sample.BuildID,
			&sample.TGID,
			&frameIDsInterface,
			&sample.SampleCount,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan row: %w", err)
		}

		sample.StackFrameIDs, err = s.convertArrayToInt64(frameIDsInterface)
		if err != nil {
			s.logger.Warn().Err(err).Msg("Failed to convert stack frame IDs")
			continue
		}

		if sample.SeqID > maxSeqID {
			maxSeqID = sample.SeqID
		}
		samples = append(samples, sample)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating rows: %w", err)
	}

	return samples, maxSeqID, nil
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

// memorySampleKey identifies a memory profile sample row (the table's primary key).
type memorySampleKey struct {
	timestamp time.Time
	serviceID string
	buildID   string
	stackHash string
}

// StoreMemorySamples stores multiple memory profile samples in a single transaction (RFD 077).
//
// It batches writes the same way as StoreSamples: samples sharing a primary
// key are summed, and only new rows consume a seq_id.
func (s *Storage) StoreMemorySamples(ctx context.Context, samples []MemoryProfileSample) error {
	if len(samples) == 0 {
		return nil
	}

	samples = mergeMemorySamples(samples)

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for start := 0; start < len(samples); start += sampleBatchSize {
		batch := samples[start:min(start+sampleBatchSize, len(samples))]

		rows := make([]string, len(batch))
		args := make([]any, 0, len(batch)*6)
		for i, sample := range batch {
			// #nosec G202 - the array literal is formatted integers, not user input.
			rows[i] = "(?::TIMESTAMP, ?::TEXT, ?::TEXT, ?::TEXT, " +
				duckdb.Int64ArrayToString(sample.StackFrameIDs) + "::INTEGER[], ?::BIGINT, ?::BIGINT)"
			args = append(args,
				sample.Timestamp,
				sample.ServiceID,
				sample.BuildID,
				sample.StackHash,
				sample.AllocBytes,
				sample.AllocObjects,
			)
		}
		values := "(VALUES " + strings.Join(rows, ", ") + `) AS b(
			timestamp, service_id, build_id, stack_hash, stack_frame_ids, alloc_bytes, alloc_objects
		)`
		match := `t.timestamp = b.timestamp AND t.service_id = b.service_id
			AND t.build_id = b.build_id AND t.stack_hash = b.stack_hash`

		// Update existing rows first so the INSERT below only sees new keys.
		// #nosec G202 - values and match are built from constants and placeholders.
		updateQuery := `
			UPDATE memory_profile_samples_local AS t
			SET alloc_bytes = t.alloc_bytes + b.alloc_bytes,
				alloc_objects = t.alloc_objects + b.alloc_objects
			FROM ` + values + `
			WHERE ` + match
		if _, err := tx.ExecContext(ctx, updateQuery, args...); err != nil {
			return fmt.Errorf("failed to update memory samples: %w", err)
		}

		// #nosec G202 - values and match are built from constants and placeholders.
		insertQuery := `
			INSERT INTO memory_profile_samples_local (
				timestamp, service_id, build_id, stack_hash, stack_frame_ids, alloc_bytes, alloc_objects
			)
			SELECT b.timestamp, b.service_id, b.build_id, b.stack_hash, b.stack_frame_ids, b.alloc_bytes, b.alloc_objects
			FROM ` + values + `
			WHERE NOT EXISTS (SELECT 1 FROM memory_profile_samples_local AS t WHERE ` + match + `)`
		if _, err := tx.ExecContext(ctx, insertQuery, args...); err != nil {
			return fmt.Errorf("failed to insert memory samples: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// mergeMemorySamples sums the allocations of samples sharing a primary key,
// preserving the order of first occurrence.
func mergeMemorySamples(samples []MemoryProfileSample) []MemoryProfileSample {
	index := make(map[memorySampleKey]int, len(samples))
	merged := make([]MemoryProfileSample, 0, len(samples))
	for _, sample := range samples {
		key := memorySampleKey{sample.Timestamp, sample.ServiceID, sample.BuildID, sample.StackHash}
		if i, ok := index[key]; ok {
			merged[i].AllocBytes += sample.AllocBytes
			merged[i].AllocObjects += sample.AllocObjects
			continue
		}
		index[key] = len(merged)
		merged = append(merged, sample)
	}
	return merged
}

// QueryMemorySamplesBySeqID queries memory profile samples with seq_id > startSeqID (RFD 089).
// Returns samples and the maximum seq_id in the result set.
func (s *Storage) QueryMemorySamplesBySeqID(ctx context.Context, startSeqID uint64, maxRecords int32, serviceID string) ([]MemoryProfileSample, uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if maxRecords <= 0 {
		maxRecords = 5000
	} else if maxRecords > 50000 {
		maxRecords = 50000
	}

	query := `
		SELECT seq_id, timestamp, service_id, build_id, stack_frame_ids, alloc_bytes, alloc_objects
		FROM memory_profile_samples_local
		WHERE seq_id > ?
	`
	args := []interface{}{startSeqID}

	if serviceID != "" {
		query += " AND service_id = ?"
		args = append(args, serviceID)
	}

	query += " ORDER BY seq_id ASC LIMIT ?"
	args = append(args, maxRecords)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query memory samples by seq_id: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var samples []MemoryProfileSample
	var maxSeqID uint64

	for rows.Next() {
		var sample MemoryProfileSample
		var frameIDsInterface interface{}

		err := rows.Scan(
			&sample.SeqID,
			&sample.Timestamp,
			&sample.ServiceID,
			&sample.BuildID,
			&frameIDsInterface,
			&sample.AllocBytes,
			&sample.AllocObjects,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan row: %w", err)
		}

		sample.StackFrameIDs, err = s.convertArrayToInt64(frameIDsInterface)
		if err != nil {
			s.logger.Warn().Err(err).Msg("Failed to convert memory stack frame IDs")
			continue
		}

		if sample.SeqID > maxSeqID {
			maxSeqID = sample.SeqID
		}
		samples = append(samples, sample)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating rows: %w", err)
	}

	return samples, maxSeqID, nil
}

// CleanupOldMemorySamples removes memory profile samples older than the retention period (RFD 077).
func (s *Storage) CleanupOldMemorySamples(ctx context.Context, retention time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-retention)
	query := `DELETE FROM memory_profile_samples_local WHERE timestamp < ?`

	result, err := s.db.ExecContext(ctx, query, cutoff)
	if err != nil {
		return fmt.Errorf("failed to cleanup old memory samples: %w", err)
	}

	rowsDeleted, _ := result.RowsAffected()
	if rowsDeleted > 0 {
		s.logger.Debug().
			Int64("rows_deleted", rowsDeleted).
			Time("cutoff", cutoff).
			Msg("Cleaned up old memory profile samples")
	}

	return nil
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
			if err := s.CleanupOldMemorySamples(ctx, sampleRetention); err != nil {
				s.logger.Error().Err(err).Msg("Failed to cleanup old memory profile samples")
			}
		case <-metadataTicker.C:
			if err := s.CleanupOldBinaryMetadata(ctx, metadataRetention); err != nil {
				s.logger.Error().Err(err).Msg("Failed to cleanup old binary metadata")
			}
		}
	}
}

// encodeStackFrames converts frame names to integer-encoded frame IDs.
//
// Known frames are resolved from the in-memory cache without touching the
// database. Unseen frames are assigned IDs and written in one INSERT, so the
// steady state, where every frame is already known, issues no statements.
func (s *Storage) encodeStackFrames(ctx context.Context, frameNames []string) ([]int64, error) {
	if len(frameNames) == 0 {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	frameIDs := make([]int64, len(frameNames))
	var pending map[string]int64 // Unseen frames in this stack: frame_name -> frame_id.
	var placeholders []string
	var args []any

	for i, frameName := range frameNames {
		if frameID, exists := s.frameDictCache[frameName]; exists {
			frameIDs[i] = frameID
			continue
		}
		// A recursive stack can repeat an unseen frame; assign it once.
		if frameID, exists := pending[frameName]; exists {
			frameIDs[i] = frameID
			continue
		}
		if pending == nil {
			pending = make(map[string]int64)
		}
		frameID := s.nextFrameID
		s.nextFrameID++
		pending[frameName] = frameID
		frameIDs[i] = frameID
		placeholders = append(placeholders, "(?, ?)")
		args = append(args, frameID, frameName)
	}

	if len(pending) == 0 {
		return frameIDs, nil
	}

	// #nosec G202 - placeholders are constant strings.
	query := `INSERT OR IGNORE INTO profile_frame_dictionary_local (frame_id, frame_name) VALUES ` +
		strings.Join(placeholders, ", ")
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		return nil, fmt.Errorf("failed to insert %d frames: %w", len(pending), err)
	}

	// Cache only after the insert succeeds, so a failed write is retried.
	for frameName, frameID := range pending {
		s.frameDictCache[frameName] = frameID
	}

	return frameIDs, nil
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
