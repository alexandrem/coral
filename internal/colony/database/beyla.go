package database

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	agentv1 "github.com/coral-io/coral/coral/agent/v1"
)

// InsertBeylaHTTPMetrics inserts Beyla HTTP metrics into the database (RFD 032).
func (d *Database) InsertBeylaHTTPMetrics(ctx context.Context, agentID string, metrics []*agentv1.BeylaHttpMetric) error {
	if len(metrics) == 0 {
		return nil
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO beyla_http_metrics (
			timestamp, agent_id, service_name, http_method, http_route,
			http_status_code, latency_bucket_ms, count, attributes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, metric := range metrics {
		timestamp := time.UnixMilli(metric.Timestamp)

		// Convert attributes to JSON.
		attributesJSON, err := json.Marshal(metric.Attributes)
		if err != nil {
			return fmt.Errorf("failed to marshal attributes: %w", err)
		}

		// Insert each histogram bucket as a separate row.
		for i, bucket := range metric.LatencyBuckets {
			if i >= len(metric.LatencyCounts) {
				break
			}

			count := metric.LatencyCounts[i]
			if count == 0 {
				continue // Skip empty buckets.
			}

			_, err = stmt.ExecContext(ctx,
				timestamp,
				agentID,
				metric.ServiceName,
				metric.HttpMethod,
				metric.HttpRoute,
				metric.HttpStatusCode,
				bucket,
				count,
				string(attributesJSON),
			)
			if err != nil {
				return fmt.Errorf("failed to insert HTTP metric: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	d.logger.Debug().
		Int("metric_count", len(metrics)).
		Str("agent_id", agentID).
		Msg("Inserted Beyla HTTP metrics")

	return nil
}

// InsertBeylaGRPCMetrics inserts Beyla gRPC metrics into the database (RFD 032).
func (d *Database) InsertBeylaGRPCMetrics(ctx context.Context, agentID string, metrics []*agentv1.BeylaGrpcMetric) error {
	if len(metrics) == 0 {
		return nil
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO beyla_grpc_metrics (
			timestamp, agent_id, service_name, grpc_method,
			grpc_status_code, latency_bucket_ms, count, attributes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, metric := range metrics {
		timestamp := time.UnixMilli(metric.Timestamp)

		// Convert attributes to JSON.
		attributesJSON, err := json.Marshal(metric.Attributes)
		if err != nil {
			return fmt.Errorf("failed to marshal attributes: %w", err)
		}

		// Insert each histogram bucket as a separate row.
		for i, bucket := range metric.LatencyBuckets {
			if i >= len(metric.LatencyCounts) {
				break
			}

			count := metric.LatencyCounts[i]
			if count == 0 {
				continue // Skip empty buckets.
			}

			_, err = stmt.ExecContext(ctx,
				timestamp,
				agentID,
				metric.ServiceName,
				metric.GrpcMethod,
				metric.GrpcStatusCode,
				bucket,
				count,
				string(attributesJSON),
			)
			if err != nil {
				return fmt.Errorf("failed to insert gRPC metric: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	d.logger.Debug().
		Int("metric_count", len(metrics)).
		Str("agent_id", agentID).
		Msg("Inserted Beyla gRPC metrics")

	return nil
}

// InsertBeylaSQLMetrics inserts Beyla SQL metrics into the database (RFD 032).
func (d *Database) InsertBeylaSQLMetrics(ctx context.Context, agentID string, metrics []*agentv1.BeylaSqlMetric) error {
	if len(metrics) == 0 {
		return nil
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO beyla_sql_metrics (
			timestamp, agent_id, service_name, sql_operation, table_name,
			latency_bucket_ms, count, attributes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, metric := range metrics {
		timestamp := time.UnixMilli(metric.Timestamp)

		// Convert attributes to JSON.
		attributesJSON, err := json.Marshal(metric.Attributes)
		if err != nil {
			return fmt.Errorf("failed to marshal attributes: %w", err)
		}

		// Insert each histogram bucket as a separate row.
		for i, bucket := range metric.LatencyBuckets {
			if i >= len(metric.LatencyCounts) {
				break
			}

			count := metric.LatencyCounts[i]
			if count == 0 {
				continue // Skip empty buckets.
			}

			_, err = stmt.ExecContext(ctx,
				timestamp,
				agentID,
				metric.ServiceName,
				metric.SqlOperation,
				metric.TableName,
				bucket,
				count,
				string(attributesJSON),
			)
			if err != nil {
				return fmt.Errorf("failed to insert SQL metric: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	d.logger.Debug().
		Int("metric_count", len(metrics)).
		Str("agent_id", agentID).
		Msg("Inserted Beyla SQL metrics")

	return nil
}

// CleanupOldBeylaMetrics removes Beyla metrics older than the specified retention periods.
// Accepts retention in days for each metric type.
func (d *Database) CleanupOldBeylaMetrics(ctx context.Context, httpRetentionDays, grpcRetentionDays, sqlRetentionDays int) (int64, error) {
	var totalDeleted int64

	// Cleanup HTTP metrics.
	httpCutoff := time.Now().Add(-time.Duration(httpRetentionDays) * 24 * time.Hour)
	httpResult, err := d.db.ExecContext(ctx, `
		DELETE FROM beyla_http_metrics
		WHERE timestamp < ?
	`, httpCutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup HTTP metrics: %w", err)
	}
	if httpRows, err := httpResult.RowsAffected(); err == nil {
		totalDeleted += httpRows
	}

	// Cleanup gRPC metrics.
	grpcCutoff := time.Now().Add(-time.Duration(grpcRetentionDays) * 24 * time.Hour)
	grpcResult, err := d.db.ExecContext(ctx, `
		DELETE FROM beyla_grpc_metrics
		WHERE timestamp < ?
	`, grpcCutoff)
	if err != nil {
		return totalDeleted, fmt.Errorf("failed to cleanup gRPC metrics: %w", err)
	}
	if grpcRows, err := grpcResult.RowsAffected(); err == nil {
		totalDeleted += grpcRows
	}

	// Cleanup SQL metrics.
	sqlCutoff := time.Now().Add(-time.Duration(sqlRetentionDays) * 24 * time.Hour)
	sqlResult, err := d.db.ExecContext(ctx, `
		DELETE FROM beyla_sql_metrics
		WHERE timestamp < ?
	`, sqlCutoff)
	if err != nil {
		return totalDeleted, fmt.Errorf("failed to cleanup SQL metrics: %w", err)
	}
	if sqlRows, err := sqlResult.RowsAffected(); err == nil {
		totalDeleted += sqlRows
	}

	if totalDeleted > 0 {
		d.logger.Debug().
			Int64("rows_deleted", totalDeleted).
			Time("http_cutoff", httpCutoff).
			Time("grpc_cutoff", grpcCutoff).
			Time("sql_cutoff", sqlCutoff).
			Msg("Cleaned up old Beyla metrics")
	}

	return totalDeleted, nil
}
