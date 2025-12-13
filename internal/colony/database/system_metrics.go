package database

import (
	"context"
	"fmt"
	"time"
)

// SystemMetricsSummary represents an aggregated system metrics summary for a 1-minute bucket (RFD 071).
type SystemMetricsSummary struct {
	BucketTime  time.Time
	AgentID     string
	MetricName  string
	MinValue    float64
	MaxValue    float64
	AvgValue    float64
	P95Value    float64
	DeltaValue  float64 // For counters: total change in window.
	SampleCount int32
	Unit        string
	MetricType  string
	Attributes  string // JSON string.
}

// InsertSystemMetricsSummaries inserts system metrics summaries into the database.
// Summaries are created by the colony after querying and aggregating agent data (RFD 071).
func (d *Database) InsertSystemMetricsSummaries(ctx context.Context, summaries []SystemMetricsSummary) error {
	if len(summaries) == 0 {
		return nil
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO system_metrics_summaries (
			bucket_time, agent_id, metric_name, min_value, max_value, avg_value,
			p95_value, delta_value, sample_count, unit, metric_type, attributes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (bucket_time, agent_id, metric_name, attributes) DO UPDATE SET
			min_value = excluded.min_value,
			max_value = excluded.max_value,
			avg_value = excluded.avg_value,
			p95_value = excluded.p95_value,
			delta_value = excluded.delta_value,
			sample_count = excluded.sample_count
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, summary := range summaries {
		_, err = stmt.ExecContext(ctx,
			summary.BucketTime,
			summary.AgentID,
			summary.MetricName,
			summary.MinValue,
			summary.MaxValue,
			summary.AvgValue,
			summary.P95Value,
			summary.DeltaValue,
			summary.SampleCount,
			summary.Unit,
			summary.MetricType,
			summary.Attributes,
		)
		if err != nil {
			return fmt.Errorf("failed to insert summary: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
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
