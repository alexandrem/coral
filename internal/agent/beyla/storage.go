package beyla

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	ebpfpb "github.com/coral-io/coral/coral/mesh/v1"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// BeylaStorage handles local storage of Beyla metrics in agent's DuckDB.
// Metrics are stored for ~1 hour and queried by Colony on-demand (RFD 025 pull-based).
type BeylaStorage struct {
	db     *sql.DB
	logger zerolog.Logger
	mu     sync.RWMutex
}

// NewBeylaStorage creates a new Beyla storage instance.
func NewBeylaStorage(db *sql.DB, logger zerolog.Logger) (*BeylaStorage, error) {
	s := &BeylaStorage{
		db:     db,
		logger: logger.With().Str("component", "beyla_storage").Logger(),
	}

	// Initialize schema.
	if err := s.initSchema(); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return s, nil
}

// initSchema creates the Beyla metrics tables in agent's local DuckDB.
func (s *BeylaStorage) initSchema() error {
	schema := `
		-- Beyla HTTP metrics (RED: Rate, Errors, Duration).
		CREATE TABLE IF NOT EXISTS beyla_http_metrics_local (
			timestamp        TIMESTAMP NOT NULL,
			service_name     VARCHAR NOT NULL,
			http_method      VARCHAR(10),
			http_route       VARCHAR(255),
			http_status_code SMALLINT,
			latency_bucket_ms DOUBLE PRECISION NOT NULL,
			count            BIGINT NOT NULL,
			attributes       JSON,
			created_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_beyla_http_timestamp
		ON beyla_http_metrics_local(timestamp DESC);

		CREATE INDEX IF NOT EXISTS idx_beyla_http_service
		ON beyla_http_metrics_local(service_name, timestamp DESC);

		-- Beyla gRPC metrics.
		CREATE TABLE IF NOT EXISTS beyla_grpc_metrics_local (
			timestamp        TIMESTAMP NOT NULL,
			service_name     VARCHAR NOT NULL,
			grpc_method      VARCHAR(255),
			grpc_status_code SMALLINT,
			latency_bucket_ms DOUBLE PRECISION NOT NULL,
			count            BIGINT NOT NULL,
			attributes       JSON,
			created_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_beyla_grpc_timestamp
		ON beyla_grpc_metrics_local(timestamp DESC);

		CREATE INDEX IF NOT EXISTS idx_beyla_grpc_service
		ON beyla_grpc_metrics_local(service_name, timestamp DESC);

		-- Beyla SQL metrics.
		CREATE TABLE IF NOT EXISTS beyla_sql_metrics_local (
			timestamp        TIMESTAMP NOT NULL,
			service_name     VARCHAR NOT NULL,
			sql_operation    VARCHAR(50),
			table_name       VARCHAR(255),
			latency_bucket_ms DOUBLE PRECISION NOT NULL,
			count            BIGINT NOT NULL,
			attributes       JSON,
			created_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_beyla_sql_timestamp
		ON beyla_sql_metrics_local(timestamp DESC);

		CREATE INDEX IF NOT EXISTS idx_beyla_sql_service
		ON beyla_sql_metrics_local(service_name, timestamp DESC);

		-- Beyla distributed traces (RFD 036).
		CREATE TABLE IF NOT EXISTS beyla_traces_local (
			trace_id       VARCHAR(32) NOT NULL,
			span_id        VARCHAR(16) NOT NULL,
			parent_span_id VARCHAR(16),
			service_name   VARCHAR NOT NULL,
			span_name      VARCHAR NOT NULL,
			span_kind      VARCHAR(10),
			start_time     TIMESTAMP NOT NULL,
			duration_us    BIGINT NOT NULL,
			status_code    SMALLINT,
			attributes     JSON,
			created_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (trace_id, span_id)
		);

		CREATE INDEX IF NOT EXISTS idx_beyla_traces_service_time
		ON beyla_traces_local(service_name, start_time DESC);

		CREATE INDEX IF NOT EXISTS idx_beyla_traces_trace_id
		ON beyla_traces_local(trace_id, start_time DESC);

		CREATE INDEX IF NOT EXISTS idx_beyla_traces_start_time
		ON beyla_traces_local(start_time DESC);
	`

	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	s.logger.Info().Msg("Beyla storage schema initialized")
	return nil
}

// StoreHTTPMetric stores a Beyla HTTP metric event.
func (s *BeylaStorage) StoreHTTPMetric(ctx context.Context, event *ebpfpb.EbpfEvent) error {
	httpMetric := event.GetBeylaHttp()
	if httpMetric == nil {
		return fmt.Errorf("event does not contain HTTP metric")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Store each histogram bucket as a separate row.
	for i, bucket := range httpMetric.LatencyBuckets {
		if i >= len(httpMetric.LatencyCounts) {
			break
		}

		count := httpMetric.LatencyCounts[i]
		if count == 0 {
			continue // Skip empty buckets
		}

		// Convert attributes to JSON.
		attributesJSON, err := json.Marshal(httpMetric.Attributes)
		if err != nil {
			return fmt.Errorf("failed to marshal attributes: %w", err)
		}

		query := `
			INSERT INTO beyla_http_metrics_local (
				timestamp, service_name, http_method, http_route, http_status_code,
				latency_bucket_ms, count, attributes
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`

		timestamp := time.Unix(0, httpMetric.Timestamp.Seconds*int64(time.Second)+int64(httpMetric.Timestamp.Nanos))

		_, err = s.db.ExecContext(
			ctx,
			query,
			timestamp,
			httpMetric.ServiceName,
			httpMetric.HttpMethod,
			httpMetric.HttpRoute,
			httpMetric.HttpStatusCode,
			bucket,
			count,
			string(attributesJSON),
		)

		if err != nil {
			return fmt.Errorf("failed to insert HTTP metric: %w", err)
		}
	}

	return nil
}

// StoreGRPCMetric stores a Beyla gRPC metric event.
func (s *BeylaStorage) StoreGRPCMetric(ctx context.Context, event *ebpfpb.EbpfEvent) error {
	grpcMetric := event.GetBeylaGrpc()
	if grpcMetric == nil {
		return fmt.Errorf("event does not contain gRPC metric")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Store each histogram bucket as a separate row.
	for i, bucket := range grpcMetric.LatencyBuckets {
		if i >= len(grpcMetric.LatencyCounts) {
			break
		}

		count := grpcMetric.LatencyCounts[i]
		if count == 0 {
			continue // Skip empty buckets
		}

		// Convert attributes to JSON.
		attributesJSON, err := json.Marshal(grpcMetric.Attributes)
		if err != nil {
			return fmt.Errorf("failed to marshal attributes: %w", err)
		}

		query := `
			INSERT INTO beyla_grpc_metrics_local (
				timestamp, service_name, grpc_method, grpc_status_code,
				latency_bucket_ms, count, attributes
			) VALUES (?, ?, ?, ?, ?, ?, ?)
		`

		timestamp := time.Unix(0, grpcMetric.Timestamp.Seconds*int64(time.Second)+int64(grpcMetric.Timestamp.Nanos))

		_, err = s.db.ExecContext(
			ctx,
			query,
			timestamp,
			grpcMetric.ServiceName,
			grpcMetric.GrpcMethod,
			grpcMetric.GrpcStatusCode,
			bucket,
			count,
			string(attributesJSON),
		)

		if err != nil {
			return fmt.Errorf("failed to insert gRPC metric: %w", err)
		}
	}

	return nil
}

// StoreSQLMetric stores a Beyla SQL metric event.
func (s *BeylaStorage) StoreSQLMetric(ctx context.Context, event *ebpfpb.EbpfEvent) error {
	sqlMetric := event.GetBeylaSql()
	if sqlMetric == nil {
		return fmt.Errorf("event does not contain SQL metric")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Store each histogram bucket as a separate row.
	for i, bucket := range sqlMetric.LatencyBuckets {
		if i >= len(sqlMetric.LatencyCounts) {
			break
		}

		count := sqlMetric.LatencyCounts[i]
		if count == 0 {
			continue // Skip empty buckets
		}

		// Convert attributes to JSON.
		attributesJSON, err := json.Marshal(sqlMetric.Attributes)
		if err != nil {
			return fmt.Errorf("failed to marshal attributes: %w", err)
		}

		query := `
			INSERT INTO beyla_sql_metrics_local (
				timestamp, service_name, sql_operation, table_name,
				latency_bucket_ms, count, attributes
			) VALUES (?, ?, ?, ?, ?, ?, ?)
		`

		timestamp := time.Unix(0, sqlMetric.Timestamp.Seconds*int64(time.Second)+int64(sqlMetric.Timestamp.Nanos))

		_, err = s.db.ExecContext(
			ctx,
			query,
			timestamp,
			sqlMetric.ServiceName,
			sqlMetric.SqlOperation,
			sqlMetric.TableName,
			bucket,
			count,
			string(attributesJSON),
		)

		if err != nil {
			return fmt.Errorf("failed to insert SQL metric: %w", err)
		}
	}

	return nil
}

// StoreEvent routes an event to the appropriate storage method based on its type.
func (s *BeylaStorage) StoreEvent(ctx context.Context, event *ebpfpb.EbpfEvent) error {
	switch event.Payload.(type) {
	case *ebpfpb.EbpfEvent_BeylaHttp:
		return s.StoreHTTPMetric(ctx, event)
	case *ebpfpb.EbpfEvent_BeylaGrpc:
		return s.StoreGRPCMetric(ctx, event)
	case *ebpfpb.EbpfEvent_BeylaSql:
		return s.StoreSQLMetric(ctx, event)
	case *ebpfpb.EbpfEvent_BeylaTrace:
		return s.StoreTrace(ctx, event)
	default:
		return fmt.Errorf("unsupported event type: %T", event.Payload)
	}
}

// StoreTrace stores a Beyla trace span event (RFD 036).
func (s *BeylaStorage) StoreTrace(ctx context.Context, event *ebpfpb.EbpfEvent) error {
	traceSpan := event.GetBeylaTrace()
	if traceSpan == nil {
		return fmt.Errorf("event does not contain trace span")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Convert attributes to JSON.
	attributesJSON, err := json.Marshal(traceSpan.Attributes)
	if err != nil {
		return fmt.Errorf("failed to marshal attributes: %w", err)
	}

	query := `
		INSERT INTO beyla_traces_local (
			trace_id, span_id, parent_span_id, service_name, span_name, span_kind,
			start_time, duration_us, status_code, attributes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	startTime := traceSpan.StartTime.AsTime()
	durationUs := traceSpan.Duration.AsDuration().Microseconds()

	_, err = s.db.ExecContext(
		ctx,
		query,
		traceSpan.TraceId,
		traceSpan.SpanId,
		traceSpan.ParentSpanId,
		traceSpan.ServiceName,
		traceSpan.SpanName,
		traceSpan.SpanKind,
		startTime,
		durationUs,
		traceSpan.StatusCode,
		string(attributesJSON),
	)

	if err != nil {
		return fmt.Errorf("failed to insert trace span: %w", err)
	}

	return nil
}

// QueryHTTPMetrics queries HTTP metrics from local storage.
func (s *BeylaStorage) QueryHTTPMetrics(ctx context.Context, startTime, endTime time.Time, serviceNames []string) ([]*ebpfpb.BeylaHttpMetrics, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
		SELECT timestamp, service_name, http_method, http_route, http_status_code,
		       latency_bucket_ms, count, attributes
		FROM beyla_http_metrics_local
		WHERE timestamp BETWEEN ? AND ?
	`

	args := []interface{}{startTime, endTime}

	if len(serviceNames) > 0 {
		placeholders := make([]string, len(serviceNames))
		for i := range serviceNames {
			placeholders[i] = "?"
			args = append(args, serviceNames[i])
		}
		query += " AND service_name IN (" + placeholders[0]
		for i := 1; i < len(placeholders); i++ {
			query += ", " + placeholders[i]
		}
		query += ")"
	}

	query += " ORDER BY timestamp DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query HTTP metrics: %w", err)
	}
	defer rows.Close()

	// Aggregate metrics by (timestamp, service, method, route, status).
	metricsMap := make(map[string]*ebpfpb.BeylaHttpMetrics)

	for rows.Next() {
		var timestamp time.Time
		var serviceName, httpMethod, httpRoute string
		var httpStatusCode int32
		var bucket float64
		var count uint64
		var attributesJSON string

		err := rows.Scan(
			&timestamp,
			&serviceName,
			&httpMethod,
			&httpRoute,
			&httpStatusCode,
			&bucket,
			&count,
			&attributesJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Create key for aggregation.
		key := fmt.Sprintf("%d_%s_%s_%s_%d",
			timestamp.Unix(),
			serviceName,
			httpMethod,
			httpRoute,
			httpStatusCode,
		)

		// Get or create metric.
		metric, exists := metricsMap[key]
		if !exists {
			var attrs map[string]string
			if err := json.Unmarshal([]byte(attributesJSON), &attrs); err != nil {
				s.logger.Warn().Err(err).Msg("Failed to unmarshal attributes")
				attrs = make(map[string]string)
			}

			metric = &ebpfpb.BeylaHttpMetrics{
				Timestamp:      timestamppb.New(timestamp),
				ServiceName:    serviceName,
				HttpMethod:     httpMethod,
				HttpRoute:      httpRoute,
				HttpStatusCode: uint32(httpStatusCode),
				LatencyBuckets: []float64{},
				LatencyCounts:  []uint64{},
				Attributes:     attrs,
			}
			metricsMap[key] = metric
		}

		// Add bucket and count.
		metric.LatencyBuckets = append(metric.LatencyBuckets, bucket)
		metric.LatencyCounts = append(metric.LatencyCounts, count)
	}

	// Convert map to slice.
	metrics := make([]*ebpfpb.BeylaHttpMetrics, 0, len(metricsMap))
	for _, metric := range metricsMap {
		metrics = append(metrics, metric)
	}

	return metrics, nil
}

// QueryGRPCMetrics queries gRPC metrics from local storage.
func (s *BeylaStorage) QueryGRPCMetrics(ctx context.Context, startTime, endTime time.Time, serviceNames []string) ([]*ebpfpb.BeylaGrpcMetrics, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
		SELECT timestamp, service_name, grpc_method, grpc_status_code,
		       latency_bucket_ms, count, attributes
		FROM beyla_grpc_metrics_local
		WHERE timestamp BETWEEN ? AND ?
	`

	args := []interface{}{startTime, endTime}

	if len(serviceNames) > 0 {
		placeholders := make([]string, len(serviceNames))
		for i := range serviceNames {
			placeholders[i] = "?"
			args = append(args, serviceNames[i])
		}
		query += " AND service_name IN (" + placeholders[0]
		for i := 1; i < len(placeholders); i++ {
			query += ", " + placeholders[i]
		}
		query += ")"
	}

	query += " ORDER BY timestamp DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query gRPC metrics: %w", err)
	}
	defer rows.Close()

	// Aggregate metrics by (timestamp, service, method, status).
	metricsMap := make(map[string]*ebpfpb.BeylaGrpcMetrics)

	for rows.Next() {
		var timestamp time.Time
		var serviceName, grpcMethod string
		var grpcStatusCode int32
		var bucket float64
		var count uint64
		var attributesJSON string

		err := rows.Scan(
			&timestamp,
			&serviceName,
			&grpcMethod,
			&grpcStatusCode,
			&bucket,
			&count,
			&attributesJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Create key for aggregation.
		key := fmt.Sprintf("%d_%s_%s_%d",
			timestamp.Unix(),
			serviceName,
			grpcMethod,
			grpcStatusCode,
		)

		// Get or create metric.
		metric, exists := metricsMap[key]
		if !exists {
			var attrs map[string]string
			if err := json.Unmarshal([]byte(attributesJSON), &attrs); err != nil {
				s.logger.Warn().Err(err).Msg("Failed to unmarshal attributes")
				attrs = make(map[string]string)
			}

			metric = &ebpfpb.BeylaGrpcMetrics{
				Timestamp:      timestamppb.New(timestamp),
				ServiceName:    serviceName,
				GrpcMethod:     grpcMethod,
				GrpcStatusCode: uint32(grpcStatusCode),
				LatencyBuckets: []float64{},
				LatencyCounts:  []uint64{},
				Attributes:     attrs,
			}
			metricsMap[key] = metric
		}

		// Add bucket and count.
		metric.LatencyBuckets = append(metric.LatencyBuckets, bucket)
		metric.LatencyCounts = append(metric.LatencyCounts, count)
	}

	// Convert map to slice.
	metrics := make([]*ebpfpb.BeylaGrpcMetrics, 0, len(metricsMap))
	for _, metric := range metricsMap {
		metrics = append(metrics, metric)
	}

	return metrics, nil
}

// QuerySQLMetrics queries SQL metrics from local storage.
func (s *BeylaStorage) QuerySQLMetrics(ctx context.Context, startTime, endTime time.Time, serviceNames []string) ([]*ebpfpb.BeylaSqlMetrics, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
		SELECT timestamp, service_name, sql_operation, table_name,
		       latency_bucket_ms, count, attributes
		FROM beyla_sql_metrics_local
		WHERE timestamp BETWEEN ? AND ?
	`

	args := []interface{}{startTime, endTime}

	if len(serviceNames) > 0 {
		placeholders := make([]string, len(serviceNames))
		for i := range serviceNames {
			placeholders[i] = "?"
			args = append(args, serviceNames[i])
		}
		query += " AND service_name IN (" + placeholders[0]
		for i := 1; i < len(placeholders); i++ {
			query += ", " + placeholders[i]
		}
		query += ")"
	}

	query += " ORDER BY timestamp DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query SQL metrics: %w", err)
	}
	defer rows.Close()

	// Aggregate metrics by (timestamp, service, operation, table).
	metricsMap := make(map[string]*ebpfpb.BeylaSqlMetrics)

	for rows.Next() {
		var timestamp time.Time
		var serviceName, sqlOperation, tableName string
		var bucket float64
		var count uint64
		var attributesJSON string

		err := rows.Scan(
			&timestamp,
			&serviceName,
			&sqlOperation,
			&tableName,
			&bucket,
			&count,
			&attributesJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Create key for aggregation.
		key := fmt.Sprintf("%d_%s_%s_%s",
			timestamp.Unix(),
			serviceName,
			sqlOperation,
			tableName,
		)

		// Get or create metric.
		metric, exists := metricsMap[key]
		if !exists {
			var attrs map[string]string
			if err := json.Unmarshal([]byte(attributesJSON), &attrs); err != nil {
				s.logger.Warn().Err(err).Msg("Failed to unmarshal attributes")
				attrs = make(map[string]string)
			}

			metric = &ebpfpb.BeylaSqlMetrics{
				Timestamp:      timestamppb.New(timestamp),
				ServiceName:    serviceName,
				SqlOperation:   sqlOperation,
				TableName:      tableName,
				LatencyBuckets: []float64{},
				LatencyCounts:  []uint64{},
				Attributes:     attrs,
			}
			metricsMap[key] = metric
		}

		// Add bucket and count.
		metric.LatencyBuckets = append(metric.LatencyBuckets, bucket)
		metric.LatencyCounts = append(metric.LatencyCounts, count)
	}

	// Convert map to slice.
	metrics := make([]*ebpfpb.BeylaSqlMetrics, 0, len(metricsMap))
	for _, metric := range metricsMap {
		metrics = append(metrics, metric)
	}

	return metrics, nil
}

// QueryTraces queries trace spans from local storage (RFD 036).
func (s *BeylaStorage) QueryTraces(ctx context.Context, startTime, endTime time.Time, serviceNames []string, traceID string, maxSpans int32) ([]*ebpfpb.BeylaTraceSpan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
		SELECT trace_id, span_id, parent_span_id, service_name, span_name, span_kind,
		       start_time, duration_us, status_code, attributes
		FROM beyla_traces_local
		WHERE start_time BETWEEN ? AND ?
	`

	args := []interface{}{startTime, endTime}

	// Filter by trace ID if provided.
	if traceID != "" {
		query += " AND trace_id = ?"
		args = append(args, traceID)
	}

	// Filter by service names if provided.
	if len(serviceNames) > 0 {
		placeholders := make([]string, len(serviceNames))
		for i := range serviceNames {
			placeholders[i] = "?"
			args = append(args, serviceNames[i])
		}
		query += " AND service_name IN (" + placeholders[0]
		for i := 1; i < len(placeholders); i++ {
			query += ", " + placeholders[i]
		}
		query += ")"
	}

	query += " ORDER BY start_time DESC"

	// Apply limit if specified.
	if maxSpans > 0 {
		query += " LIMIT ?"
		args = append(args, maxSpans)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query traces: %w", err)
	}
	defer rows.Close()

	spans := make([]*ebpfpb.BeylaTraceSpan, 0)

	for rows.Next() {
		var traceID, spanID, parentSpanID, serviceName, spanName, spanKind string
		var startTime time.Time
		var durationUs int64
		var statusCode int32
		var attributesJSON string

		err := rows.Scan(
			&traceID,
			&spanID,
			&parentSpanID,
			&serviceName,
			&spanName,
			&spanKind,
			&startTime,
			&durationUs,
			&statusCode,
			&attributesJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Unmarshal attributes.
		var attrs map[string]string
		if err := json.Unmarshal([]byte(attributesJSON), &attrs); err != nil {
			s.logger.Warn().Err(err).Msg("Failed to unmarshal trace attributes")
			attrs = make(map[string]string)
		}

		// Convert duration from microseconds to Duration.
		duration := time.Duration(durationUs) * time.Microsecond

		span := &ebpfpb.BeylaTraceSpan{
			TraceId:      traceID,
			SpanId:       spanID,
			ParentSpanId: parentSpanID,
			ServiceName:  serviceName,
			SpanName:     spanName,
			SpanKind:     spanKind,
			StartTime:    timestamppb.New(startTime),
			Duration:     durationpb.New(duration),
			StatusCode:   uint32(statusCode),
			Attributes:   attrs,
		}

		spans = append(spans, span)
	}

	return spans, nil
}

// QueryTraceByID queries all spans for a specific trace ID (RFD 036).
func (s *BeylaStorage) QueryTraceByID(ctx context.Context, traceID string) ([]*ebpfpb.BeylaTraceSpan, error) {
	// Use QueryTraces with specific trace ID and no time bounds (use a wide range).
	startTime := time.Now().Add(-24 * time.Hour) // Last 24 hours.
	endTime := time.Now()
	return s.QueryTraces(ctx, startTime, endTime, nil, traceID, 0)
}

// RunCleanupLoop periodically removes old metrics (default: 1 hour retention).
func (s *BeylaStorage) RunCleanupLoop(ctx context.Context, retention time.Duration) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info().Msg("Cleanup loop stopped")
			return

		case <-ticker.C:
			cutoff := time.Now().Add(-retention)

			s.mu.Lock()

			// Clean HTTP metrics.
			if _, err := s.db.ExecContext(ctx, "DELETE FROM beyla_http_metrics_local WHERE timestamp < ?", cutoff); err != nil {
				s.logger.Error().Err(err).Msg("Failed to clean HTTP metrics")
			}

			// Clean gRPC metrics.
			if _, err := s.db.ExecContext(ctx, "DELETE FROM beyla_grpc_metrics_local WHERE timestamp < ?", cutoff); err != nil {
				s.logger.Error().Err(err).Msg("Failed to clean gRPC metrics")
			}

			// Clean SQL metrics.
			if _, err := s.db.ExecContext(ctx, "DELETE FROM beyla_sql_metrics_local WHERE timestamp < ?", cutoff); err != nil {
				s.logger.Error().Err(err).Msg("Failed to clean SQL metrics")
			}

			// Clean traces (RFD 036).
			if _, err := s.db.ExecContext(ctx, "DELETE FROM beyla_traces_local WHERE start_time < ?", cutoff); err != nil {
				s.logger.Error().Err(err).Msg("Failed to clean traces")
			}

			s.mu.Unlock()

			s.logger.Debug().
				Time("cutoff", cutoff).
				Msg("Cleaned old Beyla metrics and traces")
		}
	}
}
