package database

import (
	"context"
	"fmt"
	"time"
)

// TelemetryBucket represents an aggregated telemetry bucket for storage.
type TelemetryBucket struct {
	BucketTime   time.Time
	AgentID      string
	ServiceName  string
	SpanKind     string
	P50Ms        float64
	P95Ms        float64
	P99Ms        float64
	ErrorCount   int32
	TotalSpans   int32
	SampleTraces []string
}

// InsertTelemetryBuckets inserts telemetry buckets into the database.
func (d *Database) InsertTelemetryBuckets(ctx context.Context, buckets []TelemetryBucket) error {
	if len(buckets) == 0 {
		return nil
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO otel_spans (
			bucket_time, agent_id, service_name, span_kind,
			p50_ms, p95_ms, p99_ms, error_count, total_spans, sample_traces
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (bucket_time, agent_id, service_name, span_kind) DO UPDATE SET
			p50_ms = excluded.p50_ms,
			p95_ms = excluded.p95_ms,
			p99_ms = excluded.p99_ms,
			error_count = excluded.error_count,
			total_spans = excluded.total_spans,
			sample_traces = excluded.sample_traces
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, bucket := range buckets {
		_, err := stmt.ExecContext(ctx,
			bucket.BucketTime,
			bucket.AgentID,
			bucket.ServiceName,
			bucket.SpanKind,
			bucket.P50Ms,
			bucket.P95Ms,
			bucket.P99Ms,
			bucket.ErrorCount,
			bucket.TotalSpans,
			bucket.SampleTraces,
		)
		if err != nil {
			return fmt.Errorf("failed to insert bucket: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	d.logger.Debug().
		Int("bucket_count", len(buckets)).
		Msg("Inserted telemetry buckets")

	return nil
}

// QueryTelemetryBuckets retrieves telemetry buckets for a given time range and agent.
func (d *Database) QueryTelemetryBuckets(ctx context.Context, agentID string, startTime, endTime time.Time) ([]TelemetryBucket, error) {
	query := `
		SELECT bucket_time, agent_id, service_name, span_kind,
		       p50_ms, p95_ms, p99_ms, error_count, total_spans, sample_traces
		FROM otel_spans
		WHERE agent_id = ? AND bucket_time >= ? AND bucket_time <= ?
		ORDER BY bucket_time DESC
	`

	rows, err := d.db.QueryContext(ctx, query, agentID, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("failed to query telemetry buckets: %w", err)
	}
	defer rows.Close()

	var buckets []TelemetryBucket
	for rows.Next() {
		var bucket TelemetryBucket
		err := rows.Scan(
			&bucket.BucketTime,
			&bucket.AgentID,
			&bucket.ServiceName,
			&bucket.SpanKind,
			&bucket.P50Ms,
			&bucket.P95Ms,
			&bucket.P99Ms,
			&bucket.ErrorCount,
			&bucket.TotalSpans,
			&bucket.SampleTraces,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		buckets = append(buckets, bucket)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return buckets, nil
}

// CleanupOldTelemetry removes telemetry data older than the specified retention period.
// RFD 025 specifies a 24-hour TTL for telemetry data.
func (d *Database) CleanupOldTelemetry(ctx context.Context, retentionHours int) (int64, error) {
	cutoffTime := time.Now().Add(-time.Duration(retentionHours) * time.Hour)

	result, err := d.db.ExecContext(ctx, `
		DELETE FROM otel_spans
		WHERE bucket_time < ?
	`, cutoffTime)

	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old telemetry: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected > 0 {
		d.logger.Debug().
			Int64("rows_deleted", rowsAffected).
			Time("cutoff_time", cutoffTime).
			Msg("Cleaned up old telemetry data")
	}

	return rowsAffected, nil
}

// CorrelateEbpfAndTelemetry runs a correlation query joining eBPF and OTel data.
// This is an example query showing how to correlate data for AI analysis.
func (d *Database) CorrelateEbpfAndTelemetry(ctx context.Context, serviceName string, bucketTime time.Time) ([]map[string]interface{}, error) {
	// This is a placeholder showing the correlation pattern described in RFD 025.
	// The actual eBPF table structure would need to be implemented as part of RFD 013.
	query := `
		SELECT
			o.service_name,
			o.bucket_time,
			o.p99_ms as otel_p99_latency,
			o.error_count,
			o.sample_traces
		FROM otel_spans o
		WHERE o.service_name = ? AND o.bucket_time = ?
	`

	rows, err := d.db.QueryContext(ctx, query, serviceName, bucketTime)
	if err != nil {
		return nil, fmt.Errorf("failed to run correlation query: %w", err)
	}
	defer rows.Close()

	var results []map[string]interface{}
	for rows.Next() {
		var serviceName string
		var bucketTime time.Time
		var p99Latency float64
		var errorCount int32
		var sampleTraces []string

		err := rows.Scan(&serviceName, &bucketTime, &p99Latency, &errorCount, &sampleTraces)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		result := map[string]interface{}{
			"service_name":   serviceName,
			"bucket_time":    bucketTime,
			"otel_p99_ms":    p99Latency,
			"error_count":    errorCount,
			"sample_traces":  sampleTraces,
		}
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return results, nil
}
