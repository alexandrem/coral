package database

import (
	"context"
	"fmt"
	"time"
)

// MemoryProfileSummary represents a 1-minute aggregated memory profile sample (RFD 077).
type MemoryProfileSummary struct {
	Timestamp     time.Time `duckdb:"timestamp,pk,immutable"`
	AgentID       string    `duckdb:"agent_id,pk,immutable"`
	ServiceName   string    `duckdb:"service_name,pk,immutable"`
	BuildID       string    `duckdb:"build_id,pk,immutable"`
	StackHash     string    `duckdb:"stack_hash,pk,immutable"`
	StackFrameIDs []int64   `duckdb:"stack_frame_ids,immutable"`
	AllocBytes    int64     `duckdb:"alloc_bytes"`
	AllocObjects  int64     `duckdb:"alloc_objects"`
}

// InsertMemoryProfileSummaries inserts 1-minute aggregated memory profile summaries (RFD 077).
func (d *Database) InsertMemoryProfileSummaries(ctx context.Context, summaries []MemoryProfileSummary) error {
	if len(summaries) == 0 {
		return nil
	}

	items := make([]*MemoryProfileSummary, len(summaries))
	for i := range summaries {
		s := summaries[i]
		if s.StackHash == "" {
			s.StackHash = ComputeStackHash(s.StackFrameIDs)
		}
		items[i] = &s
	}

	if err := d.memoryProfilesTable.BatchUpsert(ctx, items); err != nil {
		return fmt.Errorf("failed to batch upsert memory profile summaries: %w", err)
	}

	d.logger.Debug().
		Int("summary_count", len(summaries)).
		Msg("Inserted memory profile summaries")

	return nil
}

// QueryMemoryProfileSummaries retrieves memory profile summaries for a given time range and service (RFD 077).
func (d *Database) QueryMemoryProfileSummaries(ctx context.Context, serviceName string, startTime, endTime time.Time) ([]MemoryProfileSummary, error) {
	query := `SELECT timestamp, agent_id, service_name, build_id, stack_hash, stack_frame_ids, alloc_bytes, alloc_objects
		FROM memory_profile_summaries
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
		return nil, fmt.Errorf("failed to query memory profile summaries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var summaries []MemoryProfileSummary
	for rows.Next() {
		var summary MemoryProfileSummary
		var frameIDsRaw interface{}

		err := rows.Scan(
			&summary.Timestamp,
			&summary.AgentID,
			&summary.ServiceName,
			&summary.BuildID,
			&summary.StackHash,
			&frameIDsRaw,
			&summary.AllocBytes,
			&summary.AllocObjects,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

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

	return summaries, nil
}

// MemoryProfilingHotspot represents a top-K memory allocation hotspot (RFD 077).
type MemoryProfilingHotspot struct {
	Rank         int32
	Frames       []string
	Percentage   float64
	AllocBytes   int64
	AllocObjects int64
}

// MemoryProfilingSummaryResult contains the top-K memory hotspots and total allocations (RFD 077).
type MemoryProfilingSummaryResult struct {
	Hotspots        []MemoryProfilingHotspot
	TotalAllocBytes int64
	TotalAllocObjs  int64
	SamplingPeriod  string
}

// GetTopKMemoryHotspots returns the top-K memory allocation hotspots for a service in the given time range (RFD 077).
// It aggregates memory_profile_summaries by stack_hash, sums allocation bytes/objects, and decodes frame IDs.
func (d *Database) GetTopKMemoryHotspots(ctx context.Context, serviceName string, startTime, endTime time.Time, topK int) (*MemoryProfilingSummaryResult, error) {
	if topK <= 0 {
		topK = 5
	}
	if topK > 20 {
		topK = 20
	}

	// Get total allocations first.
	var totalAllocBytes, totalAllocObjs int64
	err := d.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(alloc_bytes), 0), COALESCE(SUM(alloc_objects), 0)
		FROM memory_profile_summaries
		WHERE service_name = ?
		  AND timestamp >= ? AND timestamp <= ?
	`, serviceName, startTime, endTime).Scan(&totalAllocBytes, &totalAllocObjs)
	if err != nil {
		return nil, fmt.Errorf("failed to query total allocations: %w", err)
	}

	if totalAllocBytes == 0 {
		return &MemoryProfilingSummaryResult{}, nil
	}

	// Aggregate by stack_hash (which groups identical stacks), get top-K by alloc_bytes.
	rows, err := d.db.QueryContext(ctx, `
		SELECT stack_frame_ids, SUM(alloc_bytes) as total_bytes, SUM(alloc_objects) as total_objects
		FROM memory_profile_summaries
		WHERE service_name = ?
		  AND timestamp >= ? AND timestamp <= ?
		GROUP BY stack_hash, stack_frame_ids
		ORDER BY total_bytes DESC
		LIMIT ?
	`, serviceName, startTime, endTime, topK)
	if err != nil {
		return nil, fmt.Errorf("failed to query top-K memory hotspots: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var hotspots []MemoryProfilingHotspot
	rank := int32(1)

	for rows.Next() {
		var frameIDsRaw interface{}
		var allocBytes, allocObjects int64

		if err := rows.Scan(&frameIDsRaw, &allocBytes, &allocObjects); err != nil {
			return nil, fmt.Errorf("failed to scan memory hotspot row: %w", err)
		}

		frameIDs, err := convertArrayToInt64(frameIDsRaw)
		if err != nil {
			return nil, fmt.Errorf("failed to convert frame IDs: %w", err)
		}

		frameNames, err := d.DecodeStackFrames(ctx, frameIDs)
		if err != nil {
			return nil, fmt.Errorf("failed to decode stack frames: %w", err)
		}

		hotspots = append(hotspots, MemoryProfilingHotspot{
			Rank:         rank,
			Frames:       frameNames,
			Percentage:   float64(allocBytes) * 100.0 / float64(totalAllocBytes),
			AllocBytes:   allocBytes,
			AllocObjects: allocObjects,
		})
		rank++
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating memory hotspot rows: %w", err)
	}

	return &MemoryProfilingSummaryResult{
		Hotspots:        hotspots,
		TotalAllocBytes: totalAllocBytes,
		TotalAllocObjs:  totalAllocObjs,
	}, nil
}

// CleanupOldMemoryProfiles removes memory profile data older than the specified retention period (RFD 077).
func (d *Database) CleanupOldMemoryProfiles(ctx context.Context, retentionDays int) (int64, error) {
	cutoffTime := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)

	result, err := d.db.ExecContext(ctx, `
		DELETE FROM memory_profile_summaries
		WHERE timestamp < ?
	`, cutoffTime)

	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old memory profiles: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return rowsAffected, nil
}
