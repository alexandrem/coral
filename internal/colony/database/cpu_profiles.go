package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"fmt"
	"sync"
	"time"
)

// CPUProfileSummary represents a 1-minute aggregated CPU profile sample (RFD 072).
type CPUProfileSummary struct {
	Timestamp     time.Time
	AgentID       string
	ServiceName   string
	BuildID       string
	StackHash     string
	StackFrameIDs []int64
	SampleCount   int32
}

// ComputeStackHash generates a SHA-256 hash of the stack frame IDs.
// This is used as a unique identifier for deduplication and aggregation.
func ComputeStackHash(frameIDs []int64) string {
	h := sha256.New()
	for _, id := range frameIDs {
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, uint64(id))
		h.Write(buf)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// BinaryMetadata tracks build ID to service mapping (RFD 072).
type BinaryMetadata struct {
	BuildID      string
	ServiceName  string
	BinaryPath   string
	FirstSeen    time.Time
	LastSeen     time.Time
	HasDebugInfo bool
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

		// Not found, insert new frame.
		err = d.db.QueryRowContext(ctx, `
			INSERT INTO profile_frame_dictionary (frame_name)
			VALUES (?)
			ON CONFLICT (frame_name) DO UPDATE SET frame_name = excluded.frame_name
			RETURNING frame_id
		`, frameName).Scan(&frameID)

		if err != nil {
			return nil, fmt.Errorf("failed to insert frame: %w", err)
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
func (d *Database) InsertCPUProfileSummaries(ctx context.Context, summaries []CPUProfileSummary) error {
	if len(summaries) == 0 {
		return nil
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO cpu_profile_summaries (
			timestamp, agent_id, service_name, build_id, stack_hash, stack_frame_ids, sample_count
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (timestamp, agent_id, service_name, build_id, stack_hash) DO UPDATE SET
			sample_count = cpu_profile_summaries.sample_count + excluded.sample_count
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, summary := range summaries {
		// Compute stack hash if not provided.
		stackHash := summary.StackHash
		if stackHash == "" {
			stackHash = ComputeStackHash(summary.StackFrameIDs)
		}

		_, err = stmt.ExecContext(ctx,
			summary.Timestamp,
			summary.AgentID,
			summary.ServiceName,
			summary.BuildID,
			stackHash,
			summary.StackFrameIDs,
			summary.SampleCount,
		)
		if err != nil {
			return fmt.Errorf("failed to insert CPU profile summary: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	d.logger.Debug().
		Int("summary_count", len(summaries)).
		Msg("Inserted CPU profile summaries")

	return nil
}

// UpsertBinaryMetadata inserts or updates binary metadata.
func (d *Database) UpsertBinaryMetadata(ctx context.Context, metadata BinaryMetadata) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO binary_metadata_registry (
			build_id, service_name, binary_path, first_seen, last_seen, has_debug_info
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (build_id) DO UPDATE SET
			service_name = excluded.service_name,
			binary_path = excluded.binary_path,
			last_seen = excluded.last_seen,
			has_debug_info = excluded.has_debug_info
	`,
		metadata.BuildID,
		metadata.ServiceName,
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

// QueryCPUProfileSummaries retrieves CPU profile summaries for a given time range and service.
func (d *Database) QueryCPUProfileSummaries(ctx context.Context, serviceName string, startTime, endTime time.Time) ([]CPUProfileSummary, error) {
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
		var frameIDsStr string

		err := rows.Scan(
			&summary.Timestamp,
			&summary.AgentID,
			&summary.ServiceName,
			&summary.BuildID,
			&summary.StackHash,
			&frameIDsStr,
			&summary.SampleCount,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// DuckDB returns array as string, parse it.
		// Format: "[1, 2, 3]"
		if err := parseBigIntArray(frameIDsStr, &summary.StackFrameIDs); err != nil {
			return nil, fmt.Errorf("failed to parse frame IDs: %w", err)
		}

		summary.Timestamp = summary.Timestamp.Local()
		summaries = append(summaries, summary)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return summaries, nil
}

// parseBigIntArray parses a DuckDB BIGINT[] string representation into []int64.
// Example input: "[1, 2, 3]"
func parseBigIntArray(s string, dest *[]int64) error {
	if s == "" || s == "[]" {
		*dest = []int64{}
		return nil
	}

	// Simple parser for array format.
	// Remove brackets and split by comma.
	s = s[1 : len(s)-1] // Remove [ and ].

	if s == "" {
		*dest = []int64{}
		return nil
	}

	var values []int64
	var current int64
	var inNumber bool

	for _, c := range s {
		if c >= '0' && c <= '9' {
			current = current*10 + int64(c-'0')
			inNumber = true
		} else if c == ',' || c == ' ' {
			if inNumber {
				values = append(values, current)
				current = 0
				inNumber = false
			}
		}
	}

	// Add last number.
	if inNumber {
		values = append(values, current)
	}

	*dest = values
	return nil
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
