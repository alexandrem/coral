package database

import (
	"context"
	"fmt"
	"time"
)

// SystemMetricsSummary represents an aggregated system metrics summary for a 1-minute bucket (RFD 071).
// SystemMetricsSummary represents an aggregated system metrics summary for a 1-minute bucket (RFD 071).
type SystemMetricsSummary struct {
	BucketTime  time.Time `duckdb:"bucket_time,pk"`
	AgentID     string    `duckdb:"agent_id,pk"`
	MetricName  string    `duckdb:"metric_name,pk"`
	MinValue    float64   `duckdb:"min_value"`
	MaxValue    float64   `duckdb:"max_value"`
	AvgValue    float64   `duckdb:"avg_value"`
	P95Value    float64   `duckdb:"p95_value"`
	DeltaValue  float64   `duckdb:"delta_value"`
	SampleCount uint64    `duckdb:"sample_count"`
	Unit        string    `duckdb:"unit"`
	MetricType  string    `duckdb:"metric_type"`
	Attributes  string    `duckdb:"attributes,pk"` // Includes attributes in PK for conflict resolution
}

// InsertSystemMetricsSummaries inserts system metrics summaries into the database.
// Summaries are created by the colony after querying and aggregating agent data (RFD 071).
// InsertSystemMetricsSummaries inserts system metrics summaries into the database.
// Summaries are created by the colony after querying and aggregating agent data (RFD 071).
func (d *Database) InsertSystemMetricsSummaries(ctx context.Context, summaries []SystemMetricsSummary) error {
	if len(summaries) == 0 {
		return nil
	}

	// Create a slice of pointers for BatchUpsert
	items := make([]*SystemMetricsSummary, len(summaries))
	for i := range summaries {
		items[i] = &summaries[i]
	}

	if err := d.systemMetricsTable.BatchUpsert(ctx, items); err != nil {
		return fmt.Errorf("failed to batch upsert system metrics summaries: %w", err)
	}

	d.logger.Debug().
		Int("summary_count", len(summaries)).
		Msg("Inserted system metrics summaries")

	return nil
}

// QuerySystemMetricsSummaries retrieves system metrics summaries for a given time range and agent.
func (d *Database) QuerySystemMetricsSummaries(ctx context.Context, agentID string, startTime, endTime time.Time) ([]SystemMetricsSummary, error) {
	query := `SELECT bucket_time, agent_id, metric_name, min_value, max_value,
		       avg_value, p95_value, delta_value, sample_count, unit, metric_type, attributes
			FROM system_metrics_summaries
			WHERE bucket_time >= ? AND bucket_time <= ?
	`
	args := []interface{}{startTime, endTime}

	if agentID != "" {
		query += " AND agent_id = ?"
		args = append(args, agentID)
	}

	query += " ORDER BY bucket_time DESC, metric_name"

	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query system metrics summaries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var summaries []SystemMetricsSummary
	for rows.Next() {
		var summary SystemMetricsSummary

		err := rows.Scan(
			&summary.BucketTime,
			&summary.AgentID,
			&summary.MetricName,
			&summary.MinValue,
			&summary.MaxValue,
			&summary.AvgValue,
			&summary.P95Value,
			&summary.DeltaValue,
			&summary.SampleCount,
			&summary.Unit,
			&summary.MetricType,
			&summary.Attributes,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// DuckDB returns times in UTC, convert to local timezone.
		summary.BucketTime = summary.BucketTime.Local()

		summaries = append(summaries, summary)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return summaries, nil
}

// CleanupOldSystemMetrics removes system metrics data older than the specified retention period.
// RFD 071 specifies a 30-day retention for system metrics summaries.
func (d *Database) CleanupOldSystemMetrics(ctx context.Context, retentionDays int) (int64, error) {
	cutoffTime := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)

	result, err := d.db.ExecContext(ctx, `
		DELETE FROM system_metrics_summaries
		WHERE bucket_time < ?
	`, cutoffTime)

	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old system metrics: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected > 0 {
		d.logger.Debug().
			Int64("rows_deleted", rowsAffected).
			Time("cutoff_time", cutoffTime).
			Msg("Cleaned up old system metrics summaries")
	}

	return rowsAffected, nil
}
