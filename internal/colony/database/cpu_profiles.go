package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/coral-mesh/coral/internal/duckdb"
)

// CPUProfileSummary represents a 1-minute aggregated CPU profile sample (RFD 072).
// CPUProfileSummary represents a 1-minute aggregated CPU profile sample (RFD 072).
type CPUProfileSummary struct {
	Timestamp     time.Time `duckdb:"timestamp,pk,immutable"`    // Immutable: PRIMARY KEY, cannot be updated.
	AgentID       string    `duckdb:"agent_id,pk,immutable"`     // Immutable: PRIMARY KEY, cannot be updated.
	ServiceName   string    `duckdb:"service_name,pk,immutable"` // Immutable: PRIMARY KEY, cannot be updated.
	BuildID       string    `duckdb:"build_id,pk,immutable"`     // Immutable: PRIMARY KEY, cannot be updated.
	StackHash     string    `duckdb:"stack_hash,pk,immutable"`   // Immutable: PRIMARY KEY, cannot be updated.
	StackFrameIDs []int64   `duckdb:"stack_frame_ids,immutable"` // Immutable: determined by stack_hash (part of PK).
	SampleCount   uint32    `duckdb:"sample_count"`              // Mutable: updated when aggregating samples.
}

// ComputeStackHash generates a SHA-256 hash of the stack frame IDs.
// This is used as a unique identifier for deduplication and aggregation.
func ComputeStackHash(frameIDs []int64) string {
	h := sha256.New()
	for _, id := range frameIDs {
		buf := make([]byte, 8)
		// #nosec G115 - Intentional binary encoding for hash; preserves bit pattern regardless of sign.
		binary.LittleEndian.PutUint64(buf, uint64(id))
		h.Write(buf)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// BinaryMetadata tracks build ID to service mapping (RFD 072).
// BinaryMetadata tracks build ID to service mapping (RFD 072).
type BinaryMetadata struct {
	BuildID      string    `duckdb:"build_id,pk,immutable"`  // Immutable: PRIMARY KEY, cannot be updated.
	ServiceName  string    `duckdb:"service_name,immutable"` // Immutable: has INDEX, cannot be updated in DuckDB.
	BinaryPath   string    `duckdb:"binary_path,immutable"`  // Immutable: path shouldn't change for a build_id.
	FirstSeen    time.Time `duckdb:"first_seen,immutable"`   // Immutable: first seen timestamp is fixed.
	LastSeen     time.Time `duckdb:"last_seen"`              // Mutable: updated on each observation.
	HasDebugInfo bool      `duckdb:"has_debug_info"`         // Mutable: may be discovered later.
}

// ProfileFrameStore manages the global frame dictionary for CPU profiles (RFD 072).
// This is similar to the agent-local frame dictionary but shared across all services.
type ProfileFrameStore struct {
	mu             sync.RWMutex
	frameDictCache map[string]int64 // frame_name -> frame_id.
	nextFrameID    int64
}

// NewProfileFrameStore creates a new profile frame store.
func NewProfileFrameStore() *ProfileFrameStore {
	return &ProfileFrameStore{
		frameDictCache: make(map[string]int64),
		nextFrameID:    1,
	}
}

// EncodeStackFrames converts frame names to integer IDs using the global dictionary.
// This provides 85% compression by storing frame names once and referencing by ID.
func (d *Database) EncodeStackFrames(ctx context.Context, frameNames []string) ([]int64, error) {
	if len(frameNames) == 0 {
		return []int64{}, nil
	}

	d.profileFrameStore.mu.Lock()
	defer d.profileFrameStore.mu.Unlock()

	frameIDs := make([]int64, len(frameNames))

	for i, frameName := range frameNames {
		// Check cache first.
		if frameID, exists := d.profileFrameStore.frameDictCache[frameName]; exists {
			frameIDs[i] = frameID
			continue
		}

		// Check database.
		var frameID int64
		err := d.db.QueryRowContext(ctx, `
			SELECT frame_id FROM profile_frame_dictionary WHERE frame_name = ?
		`, frameName).Scan(&frameID)

		if err == nil {
			// Found in database, cache it.
			d.profileFrameStore.frameDictCache[frameName] = frameID
			frameIDs[i] = frameID
			continue
		}

		if err != sql.ErrNoRows {
			return nil, fmt.Errorf("failed to query frame dictionary: %w", err)
		}

		// Not found, insert new frame (or do nothing if already exists due to race).
		_, err = d.db.ExecContext(ctx, `
			INSERT INTO profile_frame_dictionary (frame_name)
			VALUES (?)
			ON CONFLICT (frame_name) DO NOTHING
		`, frameName)

		if err != nil {
			return nil, fmt.Errorf("failed to insert frame: %w", err)
		}

		// Query to get the frame_id (whether just inserted or already existed).
		err = d.db.QueryRowContext(ctx, `
			SELECT frame_id FROM profile_frame_dictionary WHERE frame_name = ?
		`, frameName).Scan(&frameID)

		if err != nil {
			return nil, fmt.Errorf("failed to query frame_id after insert: %w", err)
		}

		// Cache the new frame ID.
		d.profileFrameStore.frameDictCache[frameName] = frameID
		frameIDs[i] = frameID
	}

	return frameIDs, nil
}

// DecodeStackFrames converts frame IDs back to frame names using the global dictionary.
func (d *Database) DecodeStackFrames(ctx context.Context, frameIDs []int64) ([]string, error) {
	if len(frameIDs) == 0 {
		return []string{}, nil
	}

	d.profileFrameStore.mu.RLock()
	defer d.profileFrameStore.mu.RUnlock()

	// Build reverse cache lookup.
	reverseCache := make(map[int64]string)
	for name, id := range d.profileFrameStore.frameDictCache {
		reverseCache[id] = name
	}

	frameNames := make([]string, len(frameIDs))
	missingIDs := make([]int64, 0)

	// First pass: check cache.
	for i, frameID := range frameIDs {
		if frameName, exists := reverseCache[frameID]; exists {
			frameNames[i] = frameName
		} else {
			missingIDs = append(missingIDs, frameID)
		}
	}

	// Second pass: query database for missing IDs.
	if len(missingIDs) > 0 {
		// Build IN clause.
		query := `SELECT frame_id, frame_name FROM profile_frame_dictionary WHERE frame_id IN (`
		args := make([]interface{}, len(missingIDs))
		for i, id := range missingIDs {
			if i > 0 {
				query += ", "
			}
			query += "?"
			args[i] = id
		}
		query += ")"

		rows, err := d.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("failed to query frame names: %w", err)
		}
		defer func() { _ = rows.Close() }()

		dbFrames := make(map[int64]string)
		for rows.Next() {
			var frameID int64
			var frameName string
			if err := rows.Scan(&frameID, &frameName); err != nil {
				return nil, fmt.Errorf("failed to scan frame: %w", err)
			}
			dbFrames[frameID] = frameName
		}

		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("error iterating rows: %w", err)
		}

		// Fill in missing frame names.
		for i, frameID := range frameIDs {
			if frameNames[i] == "" {
				if frameName, exists := dbFrames[frameID]; exists {
					frameNames[i] = frameName
				} else {
					frameNames[i] = fmt.Sprintf("unknown_frame_%d", frameID)
				}
			}
		}
	}

	return frameNames, nil
}

// InsertCPUProfileSummaries inserts 1-minute aggregated CPU profile summaries.
// Summaries are created by the colony after polling and aggregating agent samples (RFD 072).
// InsertCPUProfileSummaries inserts 1-minute aggregated CPU profile summaries.
// Summaries are created by the colony after polling and aggregating agent samples (RFD 072).
func (d *Database) InsertCPUProfileSummaries(ctx context.Context, summaries []CPUProfileSummary) error {
	if len(summaries) == 0 {
		return nil
	}

	// Prepare items with computed hash if missing
	items := make([]*CPUProfileSummary, len(summaries))
	for i := range summaries {
		// Compute stack hash if not provided.
		// Note: We are modifying the input slice's element indirect via pointer if we assign back,
		// but here we just set it in the copy if we want to be pure.
		// However, the original code computed it on the fly.
		// Let's modify the copy we send to ORM.
		s := summaries[i] // Copy
		if s.StackHash == "" {
			s.StackHash = ComputeStackHash(s.StackFrameIDs)
		}
		items[i] = &s

		// Debug logging for first few summaries.
		if i < 3 {
			d.logger.Debug().
				Time("timestamp", s.Timestamp).
				Str("agent_id", s.AgentID).
				Str("service_name", s.ServiceName).
				Str("build_id", s.BuildID).
				Uint32("sample_count", s.SampleCount).
				Msg("Storing CPU profile summary")
		}
	}

	if err := d.cpuProfilesTable.BatchUpsert(ctx, items); err != nil {
		return fmt.Errorf("failed to batch upsert CPU profile summaries: %w", err)
	}

	d.logger.Debug().
		Int("summary_count", len(summaries)).
		Msg("Inserted CPU profile summaries")

	return nil
}

// UpsertBinaryMetadata inserts or updates binary metadata.
// UpsertBinaryMetadata inserts or updates binary metadata.
func (d *Database) UpsertBinaryMetadata(ctx context.Context, metadata BinaryMetadata) error {
	if err := d.binaryMetadataTable.Upsert(ctx, &metadata); err != nil {
		return fmt.Errorf("failed to upsert binary metadata: %w", err)
	}
	return nil
}

// QueryCPUProfileSummaries retrieves CPU profile summaries for a given time range and service.
func (d *Database) QueryCPUProfileSummaries(ctx context.Context, serviceName string, startTime, endTime time.Time) ([]CPUProfileSummary, error) {
	d.logger.Debug().
		Str("service_name", serviceName).
		Time("start_time", startTime).
		Time("end_time", endTime).
		Msg("Querying CPU profile summaries")

	query := `SELECT timestamp, agent_id, service_name, build_id, stack_hash, stack_frame_ids, sample_count
		FROM cpu_profile_summaries
		WHERE timestamp >= ? AND timestamp <= ?
	`
	args := []interface{}{startTime, endTime}

	if serviceName != "" {
		query += " AND service_name = ?"
		args = append(args, serviceName)
	}

	query += " ORDER BY timestamp DESC"

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query CPU profile summaries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var summaries []CPUProfileSummary
	for rows.Next() {
		var summary CPUProfileSummary
		var frameIDsRaw interface{}

		err := rows.Scan(
			&summary.Timestamp,
			&summary.AgentID,
			&summary.ServiceName,
			&summary.BuildID,
			&summary.StackHash,
			&frameIDsRaw,
			&summary.SampleCount,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// DuckDB can return arrays as []interface{} or string, convert to []int64.
		frameIDs, err := convertArrayToInt64(frameIDsRaw)
		if err != nil {
			return nil, fmt.Errorf("failed to convert frame IDs: %w", err)
		}
		summary.StackFrameIDs = frameIDs

		summary.Timestamp = summary.Timestamp.Local()
		summaries = append(summaries, summary)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	d.logger.Debug().
		Int("summary_count", len(summaries)).
		Str("service_name", serviceName).
		Msg("Query completed")

	return summaries, nil
}

// CleanupOldCPUProfiles removes CPU profile data older than the specified retention period.
// RFD 072 specifies a 30-day retention for CPU profile summaries.
func (d *Database) CleanupOldCPUProfiles(ctx context.Context, retentionDays int) (int64, error) {
	cutoffTime := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)

	result, err := d.db.ExecContext(ctx, `
		DELETE FROM cpu_profile_summaries
		WHERE timestamp < ?
	`, cutoffTime)

	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old CPU profiles: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected > 0 {
		d.logger.Debug().
			Int64("rows_deleted", rowsAffected).
			Time("cutoff_time", cutoffTime).
			Msg("Cleaned up old CPU profile summaries")
	}

	return rowsAffected, nil
}

// CleanupOrphanedFrames removes frame dictionary entries that are no longer referenced.
// This should be run periodically to prevent unbounded growth.
func (d *Database) CleanupOrphanedFrames(ctx context.Context) (int64, error) {
	result, err := d.db.ExecContext(ctx, `
		DELETE FROM profile_frame_dictionary
		WHERE frame_id NOT IN (
			SELECT UNNEST(stack_frame_ids) FROM cpu_profile_summaries
			UNION
			SELECT UNNEST(stack_frame_ids) FROM memory_profile_summaries
		)
	`)

	if err != nil {
		return 0, fmt.Errorf("failed to cleanup orphaned frames: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected > 0 {
		d.logger.Debug().
			Int64("frames_deleted", rowsAffected).
			Msg("Cleaned up orphaned frame dictionary entries")
	}

	return rowsAffected, nil
}

// convertArrayToInt64 converts a DuckDB array ([]interface{} or string) to []int64.
// DuckDB Go driver may return arrays in different formats depending on the query.
func convertArrayToInt64(val interface{}) ([]int64, error) {
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
	for i, v := range arr {
		// Each element should be a number.
		switch num := v.(type) {
		case int64:
			ids[i] = num
		case int32:
			ids[i] = int64(num)
		case int:
			ids[i] = int64(num)
		case float64:
			ids[i] = int64(num)
		default:
			return nil, fmt.Errorf("unexpected array element type: %T", v)
		}
	}

	return ids, nil
}
