package database

import (
	"context"
	"fmt"
	"time"
)

// TraceProfileSpanResult holds CPU profile samples correlated with a single trace span.
type TraceProfileSpanResult struct {
	ServiceName   string
	SpanName      string
	TGID          uint32
	DurationUs    int64
	StartTime     time.Time
	StackFrameIDs []int64
	TotalSamples  int64
}

// TraceMetadata holds top-level metadata about a trace.
type TraceMetadata struct {
	TraceID         string
	StartTime       time.Time
	TotalDurationMs int64
	Services        []string
	SpanCount       int32
}

// QueryTraceProfileCPU executes the trace-to-profile join for CPU samples (RFD 078).
// Returns per-span profile rows ordered by sample count descending.
// Only spans with process_pid > 0 are included (those with extractable PIDs).
func (d *Database) QueryTraceProfileCPU(ctx context.Context, traceID string, serviceName string) ([]TraceProfileSpanResult, *TraceMetadata, error) {
	// First, get trace metadata.
	metadata, err := d.queryTraceMetadata(ctx, traceID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query trace metadata: %w", err)
	}
	if metadata == nil {
		return nil, nil, nil
	}

	// Execute the trace-to-profile join.
	// The timestamp column in cpu_profile_summaries is a 1-minute bucket, so we extend
	// the window by 1 minute on each side to catch bucket edges.
	query := `
		SELECT
			t.service_name,
			t.span_name,
			CAST(t.process_pid AS INTEGER) AS tgid,
			t.duration_us,
			t.start_time,
			p.stack_frame_ids,
			SUM(p.sample_count) AS total_samples
		FROM beyla_traces t
		INNER JOIN cpu_profile_summaries p ON
			p.tgid = CAST(t.process_pid AS INTEGER) AND
			p.timestamp >= t.start_time - INTERVAL '1 minute' AND
			p.timestamp <= t.start_time + (t.duration_us * INTERVAL '1 microsecond') + INTERVAL '1 minute'
		WHERE t.trace_id = ?
			AND t.process_pid > 0
	`
	args := []interface{}{traceID}

	if serviceName != "" {
		query += " AND t.service_name = ?"
		args = append(args, serviceName)
	}

	query += `
		GROUP BY t.service_name, t.span_name, t.process_pid, t.duration_us, t.start_time, p.stack_frame_ids
		ORDER BY total_samples DESC
		LIMIT 100
	`

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query trace profile: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []TraceProfileSpanResult
	for rows.Next() {
		var r TraceProfileSpanResult
		var frameIDsRaw interface{}
		var tgid int32

		err := rows.Scan(
			&r.ServiceName,
			&r.SpanName,
			&tgid,
			&r.DurationUs,
			&r.StartTime,
			&frameIDsRaw,
			&r.TotalSamples,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to scan trace profile row: %w", err)
		}
		r.TGID = uint32(tgid) // #nosec G115 - PIDs are always positive.

		frameIDs, err := convertArrayToInt64(frameIDsRaw)
		if err != nil {
			d.logger.Warn().Err(err).Msg("Failed to convert stack frame IDs in trace profile")
			continue
		}
		r.StackFrameIDs = frameIDs

		results = append(results, r)
	}

	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("error iterating trace profile rows: %w", err)
	}

	return results, metadata, nil
}

// queryTraceMetadata retrieves top-level metadata for a trace from beyla_traces.
func (d *Database) queryTraceMetadata(ctx context.Context, traceID string) (*TraceMetadata, error) {
	query := `
		SELECT
			MIN(start_time) AS trace_start,
			MAX(start_time + (duration_us * INTERVAL '1 microsecond')) AS trace_end,
			COUNT(*) AS span_count,
			LIST_DISTINCT(service_name ORDER BY service_name) AS services
		FROM beyla_traces
		WHERE trace_id = ?
	`

	row := d.db.QueryRowContext(ctx, query, traceID)

	var traceStart, traceEnd time.Time
	var spanCount int32
	var servicesRaw interface{}

	if err := row.Scan(&traceStart, &traceEnd, &spanCount, &servicesRaw); err != nil {
		return nil, fmt.Errorf("failed to scan trace metadata: %w", err)
	}

	if spanCount == 0 {
		return nil, nil
	}

	// Parse services list.
	var services []string
	switch v := servicesRaw.(type) {
	case []interface{}:
		for _, s := range v {
			if str, ok := s.(string); ok {
				services = append(services, str)
			}
		}
	case string:
		if v != "" {
			services = []string{v}
		}
	}

	totalDurationMs := traceEnd.Sub(traceStart).Milliseconds()

	return &TraceMetadata{
		TraceID:         traceID,
		StartTime:       traceStart,
		TotalDurationMs: totalDurationMs,
		Services:        services,
		SpanCount:       spanCount,
	}, nil
}
