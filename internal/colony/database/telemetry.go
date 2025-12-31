package database

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/coral-mesh/coral/internal/colony/database/query"
)

// TelemetrySummary represents an aggregated telemetry summary from queried agents (RFD 025 - pull-based).
type TelemetrySummary struct {
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

// otelSummary is the database representation of TelemetrySummary.
type otelSummary struct {
	BucketTime       time.Time `duckdb:"bucket_time,pk"`
	AgentID          string    `duckdb:"agent_id,pk"`
	ServiceName      string    `duckdb:"service_name,pk"`
	SpanKind         string    `duckdb:"span_kind,pk"`
	P50Ms            float64   `duckdb:"p50_ms"`
	P95Ms            float64   `duckdb:"p95_ms"`
	P99Ms            float64   `duckdb:"p99_ms"`
	ErrorCount       int32     `duckdb:"error_count"`
	TotalSpans       int32     `duckdb:"total_spans"`
	SampleTracesJSON string    `duckdb:"sample_traces"`
}

// InsertTelemetrySummaries inserts telemetry summaries into the database.
// Summaries are created by the colony after querying and aggregating agent data (RFD 025).
func (d *Database) InsertTelemetrySummaries(ctx context.Context, summaries []TelemetrySummary) error {
	if len(summaries) == 0 {
		return nil
	}

	dbItems := make([]*otelSummary, 0, len(summaries))
	for _, s := range summaries {
		jsonBytes, err := json.Marshal(s.SampleTraces)
		if err != nil {
			return fmt.Errorf("failed to marshal sample traces: %w", err)
		}

		dbItems = append(dbItems, &otelSummary{
			BucketTime:       s.BucketTime,
			AgentID:          s.AgentID,
			ServiceName:      s.ServiceName,
			SpanKind:         s.SpanKind,
			P50Ms:            s.P50Ms,
			P95Ms:            s.P95Ms,
			P99Ms:            s.P99Ms,
			ErrorCount:       s.ErrorCount,
			TotalSpans:       s.TotalSpans,
			SampleTracesJSON: string(jsonBytes),
		})
	}

	if err := d.telemetryTable.BatchUpsert(ctx, dbItems); err != nil {
		return fmt.Errorf("failed to batch upsert telemetry summaries: %w", err)
	}

	d.logger.Debug().
		Int("summary_count", len(summaries)).
		Msg("Inserted telemetry summaries")

	return nil
}

// QueryTelemetrySummaries retrieves telemetry summaries for a given time range and agent.
func (d *Database) QueryTelemetrySummaries(ctx context.Context, agentID string, startTime, endTime time.Time) ([]TelemetrySummary, error) {
	sql, args, err := query.New("otel_summaries").
		Select("bucket_time", "agent_id", "service_name", "span_kind",
			"p50_ms", "p95_ms", "p99_ms", "error_count", "total_spans", "sample_traces").
		TimeColumn("bucket_time").
		TimeRange(startTime, endTime).
		Eq("agent_id", agentID).
		OrderBy("-bucket_time").
		Build()

	if err != nil {
		return nil, fmt.Errorf("failed to build query: %w", err)
	}

	rows, err := d.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query telemetry summaries: %w", err)
	}
	defer func() { _ = rows.Close() }() // TODO: errcheck

	var summaries []TelemetrySummary
	for rows.Next() {
		var summary TelemetrySummary
		var sampleTracesJSON string

		err := rows.Scan(
			&summary.BucketTime,
			&summary.AgentID,
			&summary.ServiceName,
			&summary.SpanKind,
			&summary.P50Ms,
			&summary.P95Ms,
			&summary.P99Ms,
			&summary.ErrorCount,
			&summary.TotalSpans,
			&sampleTracesJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// DuckDB returns times in UTC, convert to local timezone.
		summary.BucketTime = summary.BucketTime.Local()

		// Decode SampleTraces from JSON.
		if err := json.Unmarshal([]byte(sampleTracesJSON), &summary.SampleTraces); err != nil {
			return nil, fmt.Errorf("failed to unmarshal sample traces: %w", err)
		}

		summaries = append(summaries, summary)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return summaries, nil
}

// CleanupOldTelemetry removes telemetry data older than the specified retention period.
// RFD 025 specifies a 24-hour TTL for telemetry summaries.
func (d *Database) CleanupOldTelemetry(ctx context.Context, retentionHours int) (int64, error) {
	cutoffTime := time.Now().Add(-time.Duration(retentionHours) * time.Hour)

	result, err := d.db.ExecContext(ctx, `
		DELETE FROM otel_summaries
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
			Msg("Cleaned up old telemetry summaries")
	}

	return rowsAffected, nil
}

// CorrelateEbpfAndTelemetry runs a correlation query joining eBPF and OTel data.
// This is an example query showing how to correlate data for AI analysis (RFD 025).
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
		FROM otel_summaries o
		WHERE o.service_name = ? AND o.bucket_time = ?
	`

	rows, err := d.db.QueryContext(ctx, query, serviceName, bucketTime)
	if err != nil {
		return nil, fmt.Errorf("failed to run correlation query: %w", err)
	}
	defer func() { _ = rows.Close() }() // TODO: errcheck

	var results []map[string]interface{}
	for rows.Next() {
		var serviceName string
		var bucketTime time.Time
		var p99Latency float64
		var errorCount int32
		var sampleTracesJSON string

		err := rows.Scan(&serviceName, &bucketTime, &p99Latency, &errorCount, &sampleTracesJSON)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// DuckDB returns times in UTC, convert to local timezone.
		bucketTime = bucketTime.Local()

		// Decode SampleTraces from JSON.
		var sampleTraces []string
		if err := json.Unmarshal([]byte(sampleTracesJSON), &sampleTraces); err != nil {
			return nil, fmt.Errorf("failed to unmarshal sample traces: %w", err)
		}

		result := map[string]interface{}{
			"service_name":  serviceName,
			"bucket_time":   bucketTime,
			"otel_p99_ms":   p99Latency,
			"error_count":   errorCount,
			"sample_traces": sampleTraces,
		}
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return results, nil
}
