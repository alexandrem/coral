package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
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
	defer func() { _ = tx.Rollback() }() // TODO: errcheck

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
	defer func() { _ = stmt.Close() }() // TODO: errcheck

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
	defer func() { _ = tx.Rollback() }() // TODO: errcheck

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
	defer func() { _ = stmt.Close() }() // TODO: errcheck

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
	defer func() { _ = tx.Rollback() }() // TODO: errcheck

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
	defer func() { _ = stmt.Close() }() // TODO: errcheck

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

// InsertBeylaTraces inserts Beyla trace spans into the database (RFD 036).
func (d *Database) InsertBeylaTraces(ctx context.Context, agentID string, spans []*agentv1.BeylaTraceSpan) error {
	if len(spans) == 0 {
		return nil
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func(tx *sql.Tx) {
		_ = tx.Rollback() // TODO: errcheck
	}(tx)

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO beyla_traces (
			trace_id, span_id, parent_span_id, agent_id, service_name,
			span_name, span_kind, start_time, duration_us, status_code, attributes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer func(stmt *sql.Stmt) {
		_ = stmt.Close() // TODO: errcheck
	}(stmt)

	for _, span := range spans {
		startTime := time.UnixMilli(span.StartTime)

		// Convert attributes to JSON.
		attributesJSON, err := json.Marshal(span.Attributes)
		if err != nil {
			return fmt.Errorf("failed to marshal attributes: %w", err)
		}

		_, err = stmt.ExecContext(ctx,
			span.TraceId,
			span.SpanId,
			span.ParentSpanId,
			agentID,
			span.ServiceName,
			span.SpanName,
			span.SpanKind,
			startTime,
			span.DurationUs,
			span.StatusCode,
			string(attributesJSON),
		)
		if err != nil {
			return fmt.Errorf("failed to insert trace span: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	d.logger.Debug().
		Int("span_count", len(spans)).
		Str("agent_id", agentID).
		Msg("Inserted Beyla trace spans")

	return nil
}

// QueryBeylaHTTPMetrics queries HTTP metrics from colony database.
// Returns aggregated metrics grouped by (service, method, route, status).
func (d *Database) QueryBeylaHTTPMetrics(ctx context.Context, serviceName string, startTime, endTime time.Time, filters map[string]string) ([]*BeylaHTTPMetricResult, error) {
	query := `
		SELECT
			service_name,
			http_method,
			http_route,
			http_status_code,
			latency_bucket_ms,
			SUM(count) as total_count,
			MIN(timestamp) as first_seen,
			MAX(timestamp) as last_seen
		FROM beyla_http_metrics
		WHERE service_name = ? AND timestamp >= ? AND timestamp <= ?
	`

	args := []interface{}{serviceName, startTime, endTime}

	// Add optional filters.
	if method, ok := filters["http_method"]; ok && method != "" {
		query += " AND http_method = ?"
		args = append(args, method)
	}
	if route, ok := filters["http_route"]; ok && route != "" {
		query += " AND http_route = ?"
		args = append(args, route)
	}
	if statusRange, ok := filters["status_code_range"]; ok && statusRange != "" {
		// Convert status_code_range (e.g., "2xx") to SQL BETWEEN.
		switch statusRange {
		case "2xx":
			query += " AND http_status_code BETWEEN 200 AND 299"
		case "3xx":
			query += " AND http_status_code BETWEEN 300 AND 399"
		case "4xx":
			query += " AND http_status_code BETWEEN 400 AND 499"
		case "5xx":
			query += " AND http_status_code BETWEEN 500 AND 599"
		}
	}

	query += `
		GROUP BY service_name, http_method, http_route, http_status_code, latency_bucket_ms
		ORDER BY http_route, latency_bucket_ms
	`

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query HTTP metrics: %w", err)
	}
	defer func() { _ = rows.Close() }() // TODO: errcheck

	var results []*BeylaHTTPMetricResult
	for rows.Next() {
		var r BeylaHTTPMetricResult
		err := rows.Scan(
			&r.ServiceName,
			&r.HTTPMethod,
			&r.HTTPRoute,
			&r.HTTPStatusCode,
			&r.LatencyBucketMs,
			&r.Count,
			&r.FirstSeen,
			&r.LastSeen,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		results = append(results, &r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	d.logger.Debug().
		Str("service", serviceName).
		Int("result_count", len(results)).
		Msg("Queried Beyla HTTP metrics")

	return results, nil
}

// QueryBeylaGRPCMetrics queries gRPC metrics from colony database.
func (d *Database) QueryBeylaGRPCMetrics(ctx context.Context, serviceName string, startTime, endTime time.Time, filters map[string]string) ([]*BeylaGRPCMetricResult, error) {
	query := `
		SELECT
			service_name,
			grpc_method,
			grpc_status_code,
			latency_bucket_ms,
			SUM(count) as total_count,
			MIN(timestamp) as first_seen,
			MAX(timestamp) as last_seen
		FROM beyla_grpc_metrics
		WHERE service_name = ? AND timestamp >= ? AND timestamp <= ?
	`

	args := []interface{}{serviceName, startTime, endTime}

	// Add optional filters.
	if method, ok := filters["grpc_method"]; ok && method != "" {
		query += " AND grpc_method = ?"
		args = append(args, method)
	}
	if status, ok := filters["status_code"]; ok && status != "" {
		query += " AND grpc_status_code = ?"
		args = append(args, status)
	}

	query += `
		GROUP BY service_name, grpc_method, grpc_status_code, latency_bucket_ms
		ORDER BY grpc_method, latency_bucket_ms
	`

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query gRPC metrics: %w", err)
	}
	defer func() { _ = rows.Close() }() // TODO: errcheck

	var results []*BeylaGRPCMetricResult
	for rows.Next() {
		var r BeylaGRPCMetricResult
		err := rows.Scan(
			&r.ServiceName,
			&r.GRPCMethod,
			&r.GRPCStatusCode,
			&r.LatencyBucketMs,
			&r.Count,
			&r.FirstSeen,
			&r.LastSeen,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		results = append(results, &r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return results, nil
}

// QueryBeylaSQLMetrics queries SQL metrics from colony database.
func (d *Database) QueryBeylaSQLMetrics(ctx context.Context, serviceName string, startTime, endTime time.Time, filters map[string]string) ([]*BeylaSQLMetricResult, error) {
	query := `
		SELECT
			service_name,
			sql_operation,
			table_name,
			latency_bucket_ms,
			SUM(count) as total_count,
			MIN(timestamp) as first_seen,
			MAX(timestamp) as last_seen
		FROM beyla_sql_metrics
		WHERE service_name = ? AND timestamp >= ? AND timestamp <= ?
	`

	args := []interface{}{serviceName, startTime, endTime}

	// Add optional filters.
	if op, ok := filters["sql_operation"]; ok && op != "" {
		query += " AND sql_operation = ?"
		args = append(args, op)
	}
	if table, ok := filters["table_name"]; ok && table != "" {
		query += " AND table_name = ?"
		args = append(args, table)
	}

	query += `
		GROUP BY service_name, sql_operation, table_name, latency_bucket_ms
		ORDER BY sql_operation, table_name, latency_bucket_ms
	`

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query SQL metrics: %w", err)
	}
	defer func() { _ = rows.Close() }() // TODO: errcheck

	var results []*BeylaSQLMetricResult
	for rows.Next() {
		var r BeylaSQLMetricResult
		err := rows.Scan(
			&r.ServiceName,
			&r.SQLOperation,
			&r.TableName,
			&r.LatencyBucketMs,
			&r.Count,
			&r.FirstSeen,
			&r.LastSeen,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		results = append(results, &r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return results, nil
}

// QueryBeylaTraces queries distributed traces from colony database (RFD 036).
func (d *Database) QueryBeylaTraces(ctx context.Context, serviceName string, startTime, endTime time.Time, minDurationUs int64, maxTraces int) ([]*BeylaTraceResult, error) {
	query := `
		SELECT
			trace_id,
			span_id,
			parent_span_id,
			service_name,
			span_name,
			span_kind,
			start_time,
			duration_us,
			status_code
		FROM beyla_traces
		WHERE start_time >= ? AND start_time <= ?
	`

	args := []interface{}{startTime, endTime}

	if serviceName != "" {
		query += " AND service_name = ?"
		args = append(args, serviceName)
	}

	if minDurationUs > 0 {
		query += " AND duration_us >= ?"
		args = append(args, minDurationUs)
	}

	query += " ORDER BY start_time DESC"

	if maxTraces > 0 {
		query += " LIMIT ?"
		args = append(args, maxTraces)
	}

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query traces: %w", err)
	}
	defer func() { _ = rows.Close() }() // TODO: errcheck

	var results []*BeylaTraceResult
	for rows.Next() {
		var r BeylaTraceResult
		var parentSpanID *string
		err := rows.Scan(
			&r.TraceID,
			&r.SpanID,
			&parentSpanID,
			&r.ServiceName,
			&r.SpanName,
			&r.SpanKind,
			&r.StartTime,
			&r.DurationUs,
			&r.StatusCode,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		if parentSpanID != nil {
			r.ParentSpanID = *parentSpanID
		}
		results = append(results, &r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return results, nil
}

// BeylaHTTPMetricResult represents an aggregated HTTP metric result.
type BeylaHTTPMetricResult struct {
	ServiceName     string
	HTTPMethod      string
	HTTPRoute       string
	HTTPStatusCode  int
	LatencyBucketMs float64
	Count           int64
	FirstSeen       time.Time
	LastSeen        time.Time
}

// BeylaGRPCMetricResult represents an aggregated gRPC metric result.
type BeylaGRPCMetricResult struct {
	ServiceName     string
	GRPCMethod      string
	GRPCStatusCode  int
	LatencyBucketMs float64
	Count           int64
	FirstSeen       time.Time
	LastSeen        time.Time
}

// BeylaSQLMetricResult represents an aggregated SQL metric result.
type BeylaSQLMetricResult struct {
	ServiceName     string
	SQLOperation    string
	TableName       string
	LatencyBucketMs float64
	Count           int64
	FirstSeen       time.Time
	LastSeen        time.Time
}

// BeylaTraceResult represents a trace span result.
type BeylaTraceResult struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	ServiceName  string
	SpanName     string
	SpanKind     string
	StartTime    time.Time
	DurationUs   int64
	StatusCode   int
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

// CleanupOldBeylaTraces removes Beyla traces older than the specified retention period (RFD 036).
func (d *Database) CleanupOldBeylaTraces(ctx context.Context, traceRetentionDays int) (int64, error) {
	traceCutoff := time.Now().Add(-time.Duration(traceRetentionDays) * 24 * time.Hour)
	traceResult, err := d.db.ExecContext(ctx, `
		DELETE FROM beyla_traces
		WHERE start_time < ?
	`, traceCutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup traces: %w", err)
	}

	deleted, _ := traceResult.RowsAffected()
	if deleted > 0 {
		d.logger.Debug().
			Int64("rows_deleted", deleted).
			Time("trace_cutoff", traceCutoff).
			Msg("Cleaned up old Beyla traces")
	}

	return deleted, nil
}
