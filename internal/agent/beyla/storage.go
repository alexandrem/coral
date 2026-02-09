package beyla

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	ebpfpb "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/duckdb"
)

// ORM models for Beyla tables.

type beylaHTTPMetricDB struct {
	Timestamp       time.Time `duckdb:"timestamp"`
	ServiceName     string    `duckdb:"service_name"`
	HTTPMethod      string    `duckdb:"http_method"`
	HTTPRoute       string    `duckdb:"http_route"`
	HTTPStatusCode  int       `duckdb:"http_status_code"`
	LatencyBucketMs float64   `duckdb:"latency_bucket_ms"`
	Count           int64     `duckdb:"count"`
	Attributes      string    `duckdb:"attributes"`
	CreatedAt       time.Time `duckdb:"created_at,immutable"`
}

type beylaGRPCMetricDB struct {
	Timestamp       time.Time `duckdb:"timestamp"`
	ServiceName     string    `duckdb:"service_name"`
	GRPCMethod      string    `duckdb:"grpc_method"`
	GRPCStatusCode  int       `duckdb:"grpc_status_code"`
	LatencyBucketMs float64   `duckdb:"latency_bucket_ms"`
	Count           int64     `duckdb:"count"`
	Attributes      string    `duckdb:"attributes"`
	CreatedAt       time.Time `duckdb:"created_at,immutable"`
}

type beylaSQLMetricDB struct {
	Timestamp       time.Time `duckdb:"timestamp"`
	ServiceName     string    `duckdb:"service_name"`
	SQLOperation    string    `duckdb:"sql_operation"`
	TableName       string    `duckdb:"table_name"`
	LatencyBucketMs float64   `duckdb:"latency_bucket_ms"`
	Count           int64     `duckdb:"count"`
	Attributes      string    `duckdb:"attributes"`
	CreatedAt       time.Time `duckdb:"created_at,immutable"`
}

type beylaTraceDB struct {
	TraceID      string    `duckdb:"trace_id,pk"`
	SpanID       string    `duckdb:"span_id,pk"`
	ParentSpanID string    `duckdb:"parent_span_id,immutable"`
	AgentID      string    `duckdb:"agent_id,immutable"`
	ServiceName  string    `duckdb:"service_name,immutable"`
	SpanName     string    `duckdb:"span_name,immutable"`
	SpanKind     string    `duckdb:"span_kind,immutable"`
	StartTime    time.Time `duckdb:"start_time,immutable"`
	DurationUs   int64     `duckdb:"duration_us,immutable"`
	StatusCode   int       `duckdb:"status_code,immutable"`
	Attributes   string    `duckdb:"attributes,immutable"`
	CreatedAt    time.Time `duckdb:"created_at,immutable"`
}

// BeylaStorage handles local storage of Beyla metrics in agent's DuckDB.
// Metrics are stored for ~1 hour and queried by Colony on-demand (RFD 025 pull-based).
type BeylaStorage struct {
	db     *sql.DB
	dbPath string // File path to DuckDB file (for HTTP serving, RFD 039).
	logger zerolog.Logger
	mu     sync.RWMutex

	// ORM tables.
	httpMetricsTable *duckdb.Table[beylaHTTPMetricDB]
	grpcMetricsTable *duckdb.Table[beylaGRPCMetricDB]
	sqlMetricsTable  *duckdb.Table[beylaSQLMetricDB]
	tracesTable      *duckdb.Table[beylaTraceDB]
}

// NewBeylaStorage creates a new Beyla storage instance.
// dbPath is optional - if empty, database file path will not be available for HTTP serving.
func NewBeylaStorage(db *sql.DB, dbPath string, logger zerolog.Logger) (*BeylaStorage, error) {
	s := &BeylaStorage{
		db:               db,
		dbPath:           dbPath,
		logger:           logger.With().Str("component", "beyla_storage").Logger(),
		httpMetricsTable: duckdb.NewTable[beylaHTTPMetricDB](db, "beyla_http_metrics_local"),
		grpcMetricsTable: duckdb.NewTable[beylaGRPCMetricDB](db, "beyla_grpc_metrics_local"),
		sqlMetricsTable:  duckdb.NewTable[beylaSQLMetricDB](db, "beyla_sql_metrics_local"),
		tracesTable:      duckdb.NewTable[beylaTraceDB](db, "beyla_traces_local"),
	}

	// Initialize schema.
	if err := s.initSchema(); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return s, nil
}

// initSchema creates the Beyla metrics tables in agent's local DuckDB.
func (s *BeylaStorage) initSchema() error {
	// Sequences for checkpoint-based polling (RFD 089).
	seqSchema := `
		CREATE SEQUENCE IF NOT EXISTS seq_beyla_http_metrics START 1;
		CREATE SEQUENCE IF NOT EXISTS seq_beyla_grpc_metrics START 1;
		CREATE SEQUENCE IF NOT EXISTS seq_beyla_sql_metrics START 1;
		CREATE SEQUENCE IF NOT EXISTS seq_beyla_traces START 1;
	`
	if _, err := s.db.Exec(seqSchema); err != nil {
		return fmt.Errorf("failed to create Beyla sequences: %w", err)
	}

	// Beyla HTTP metrics (RED: Rate, Errors, Duration).
	httpMetricsSchema := `
		CREATE TABLE IF NOT EXISTS beyla_http_metrics_local (
			seq_id           UBIGINT DEFAULT nextval('seq_beyla_http_metrics'),
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

		CREATE INDEX IF NOT EXISTS idx_beyla_http_seq_id
		ON beyla_http_metrics_local(seq_id);
	`
	if _, err := s.db.Exec(httpMetricsSchema); err != nil {
		return fmt.Errorf("failed to create HTTP metrics schema: %w", err)
	}

	// Beyla gRPC metrics.
	grpcMetricsSchema := `
		CREATE TABLE IF NOT EXISTS beyla_grpc_metrics_local (
			seq_id           UBIGINT DEFAULT nextval('seq_beyla_grpc_metrics'),
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

		CREATE INDEX IF NOT EXISTS idx_beyla_grpc_seq_id
		ON beyla_grpc_metrics_local(seq_id);
	`
	if _, err := s.db.Exec(grpcMetricsSchema); err != nil {
		return fmt.Errorf("failed to create gRPC metrics schema: %w", err)
	}

	// Beyla SQL metrics.
	sqlMetricsSchema := `
		CREATE TABLE IF NOT EXISTS beyla_sql_metrics_local (
			seq_id           UBIGINT DEFAULT nextval('seq_beyla_sql_metrics'),
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

		CREATE INDEX IF NOT EXISTS idx_beyla_sql_seq_id
		ON beyla_sql_metrics_local(seq_id);
	`
	if _, err := s.db.Exec(sqlMetricsSchema); err != nil {
		return fmt.Errorf("failed to create SQL metrics schema: %w", err)
	}

	// Beyla distributed traces (RFD 036).
	tracesSchema := `
		CREATE TABLE IF NOT EXISTS beyla_traces_local (
			seq_id         UBIGINT DEFAULT nextval('seq_beyla_traces'),
			trace_id       VARCHAR(32) NOT NULL,
			span_id        VARCHAR(16) NOT NULL,
			parent_span_id VARCHAR(16),
			agent_id       VARCHAR NOT NULL,
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

		CREATE INDEX IF NOT EXISTS idx_beyla_traces_duration
		ON beyla_traces_local(duration_us DESC);

		CREATE INDEX IF NOT EXISTS idx_beyla_traces_agent_id
		ON beyla_traces_local(agent_id, start_time DESC);

		CREATE INDEX IF NOT EXISTS idx_beyla_traces_seq_id
		ON beyla_traces_local(seq_id);
	`
	if _, err := s.db.Exec(tracesSchema); err != nil {
		return fmt.Errorf("failed to create traces schema: %w", err)
	}

	s.logger.Info().Msg("Beyla storage schema initialized")

	// Set a low WAL auto-checkpoint limit (e.g., 4MB) to ensure data is flushed frequently
	// and becomes visible to remote readers without manual checkpointing.
	if _, err := s.db.Exec("PRAGMA wal_autocheckpoint='4MB'"); err != nil {
		s.logger.Warn().Err(err).Msg("Failed to set WAL auto-checkpoint limit")
	}

	// Attempt an initial checkpoint to ensure tables are visible immediately.
	// We do NOT use FORCE CHECKPOINT as it can abort active transactions.
	// If this fails (e.g. due to contention), we log and continue, relying on auto-checkpoint.
	if _, err := s.db.Exec("CHECKPOINT"); err != nil {
		s.logger.Warn().Err(err).Msg("Initial checkpoint failed (tables may take a moment to appear remotely)")
	}

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

	startTime := traceSpan.StartTime.AsTime()
	durationUs := traceSpan.Duration.AsDuration().Microseconds()

	item := &beylaTraceDB{
		TraceID:      traceSpan.TraceId,
		SpanID:       traceSpan.SpanId,
		ParentSpanID: traceSpan.ParentSpanId,
		AgentID:      event.AgentId,
		ServiceName:  traceSpan.ServiceName,
		SpanName:     traceSpan.SpanName,
		SpanKind:     traceSpan.SpanKind,
		StartTime:    startTime,
		DurationUs:   durationUs,
		StatusCode:   int(traceSpan.StatusCode),
		Attributes:   string(attributesJSON),
		CreatedAt:    time.Now(),
	}

	if err := s.tracesTable.Upsert(ctx, item); err != nil {
		return fmt.Errorf("failed to upsert trace span: %w", err)
	}

	return nil
}

// StoreOTLPSpan stores a span from the OTLP receiver into beyla_traces_local.
// This is used by the Beyla manager's SpanHandler to route Beyla traces
// to the correct table instead of otel_spans_local.
func (s *BeylaStorage) StoreOTLPSpan(ctx context.Context, agentID string, traceID, spanID, parentSpanID, serviceName, spanName, spanKind string, startTime time.Time, durationUs int64, statusCode int, attributes map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Convert attributes to JSON.
	attributesJSON, err := json.Marshal(attributes)
	if err != nil {
		return fmt.Errorf("failed to marshal attributes: %w", err)
	}

	item := &beylaTraceDB{
		TraceID:      traceID,
		SpanID:       spanID,
		ParentSpanID: parentSpanID,
		AgentID:      agentID,
		ServiceName:  serviceName,
		SpanName:     spanName,
		SpanKind:     spanKind,
		StartTime:    startTime,
		DurationUs:   durationUs,
		StatusCode:   statusCode,
		Attributes:   string(attributesJSON),
		CreatedAt:    time.Now(),
	}

	if err := s.tracesTable.Upsert(ctx, item); err != nil {
		return fmt.Errorf("failed to upsert OTLP span: %w", err)
	}

	return nil
}

// QueryHTTPMetricsBySeqID queries HTTP metrics with seq_id > startSeqID (RFD 089).
// Returns aggregated metrics, the maximum seq_id, and any error.
func (s *BeylaStorage) QueryHTTPMetricsBySeqID(ctx context.Context, startSeqID uint64, maxRecords int32, serviceNames []string) ([]*ebpfpb.BeylaHttpMetrics, uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if maxRecords <= 0 {
		maxRecords = 10000
	} else if maxRecords > 50000 {
		maxRecords = 50000
	}

	query := `
		SELECT seq_id, timestamp, service_name, http_method, http_route, http_status_code,
		       latency_bucket_ms, count, attributes::VARCHAR as attributes
		FROM beyla_http_metrics_local
		WHERE seq_id > ?
	`
	args := []interface{}{startSeqID}

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

	query += " ORDER BY seq_id ASC LIMIT ?"
	args = append(args, maxRecords)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query HTTP metrics by seq_id: %w", err)
	}
	defer func() { _ = rows.Close() }()

	metricsMap := make(map[string]*ebpfpb.BeylaHttpMetrics)
	var maxSeqID uint64

	for rows.Next() {
		var seqID uint64
		var timestamp time.Time
		var serviceName, httpMethod, httpRoute string
		var httpStatusCode int32
		var bucket float64
		var count uint64
		var attributesJSON string

		err := rows.Scan(&seqID, &timestamp, &serviceName, &httpMethod, &httpRoute,
			&httpStatusCode, &bucket, &count, &attributesJSON)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan row: %w", err)
		}

		if seqID > maxSeqID {
			maxSeqID = seqID
		}

		key := fmt.Sprintf("%d_%s_%s_%s_%d", timestamp.Unix(), serviceName, httpMethod, httpRoute, httpStatusCode)
		metric, exists := metricsMap[key]
		if !exists {
			var attrs map[string]string
			if err := json.Unmarshal([]byte(attributesJSON), &attrs); err != nil {
				attrs = make(map[string]string)
			}
			metric = &ebpfpb.BeylaHttpMetrics{
				Timestamp:      timestamppb.New(timestamp),
				ServiceName:    serviceName,
				HttpMethod:     httpMethod,
				HttpRoute:      httpRoute,
				HttpStatusCode: uint32(httpStatusCode), // #nosec G115
				LatencyBuckets: []float64{},
				LatencyCounts:  []uint64{},
				Attributes:     attrs,
			}
			metricsMap[key] = metric
		}
		metric.LatencyBuckets = append(metric.LatencyBuckets, bucket)
		metric.LatencyCounts = append(metric.LatencyCounts, count)
	}

	metrics := make([]*ebpfpb.BeylaHttpMetrics, 0, len(metricsMap))
	for _, metric := range metricsMap {
		metrics = append(metrics, metric)
	}
	return metrics, maxSeqID, nil
}

// QueryGRPCMetricsBySeqID queries gRPC metrics with seq_id > startSeqID (RFD 089).
func (s *BeylaStorage) QueryGRPCMetricsBySeqID(ctx context.Context, startSeqID uint64, maxRecords int32, serviceNames []string) ([]*ebpfpb.BeylaGrpcMetrics, uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if maxRecords <= 0 {
		maxRecords = 10000
	} else if maxRecords > 50000 {
		maxRecords = 50000
	}

	query := `
		SELECT seq_id, timestamp, service_name, grpc_method, grpc_status_code,
		       latency_bucket_ms, count, attributes::VARCHAR as attributes
		FROM beyla_grpc_metrics_local
		WHERE seq_id > ?
	`
	args := []interface{}{startSeqID}

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

	query += " ORDER BY seq_id ASC LIMIT ?"
	args = append(args, maxRecords)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query gRPC metrics by seq_id: %w", err)
	}
	defer func() { _ = rows.Close() }()

	metricsMap := make(map[string]*ebpfpb.BeylaGrpcMetrics)
	var maxSeqID uint64

	for rows.Next() {
		var seqID uint64
		var timestamp time.Time
		var serviceName, grpcMethod string
		var grpcStatusCode int32
		var bucket float64
		var count uint64
		var attributesJSON string

		err := rows.Scan(&seqID, &timestamp, &serviceName, &grpcMethod,
			&grpcStatusCode, &bucket, &count, &attributesJSON)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan row: %w", err)
		}

		if seqID > maxSeqID {
			maxSeqID = seqID
		}

		key := fmt.Sprintf("%d_%s_%s_%d", timestamp.Unix(), serviceName, grpcMethod, grpcStatusCode)
		metric, exists := metricsMap[key]
		if !exists {
			var attrs map[string]string
			if err := json.Unmarshal([]byte(attributesJSON), &attrs); err != nil {
				attrs = make(map[string]string)
			}
			metric = &ebpfpb.BeylaGrpcMetrics{
				Timestamp:      timestamppb.New(timestamp),
				ServiceName:    serviceName,
				GrpcMethod:     grpcMethod,
				GrpcStatusCode: uint32(grpcStatusCode), // #nosec G115
				LatencyBuckets: []float64{},
				LatencyCounts:  []uint64{},
				Attributes:     attrs,
			}
			metricsMap[key] = metric
		}
		metric.LatencyBuckets = append(metric.LatencyBuckets, bucket)
		metric.LatencyCounts = append(metric.LatencyCounts, count)
	}

	metrics := make([]*ebpfpb.BeylaGrpcMetrics, 0, len(metricsMap))
	for _, metric := range metricsMap {
		metrics = append(metrics, metric)
	}
	return metrics, maxSeqID, nil
}

// QuerySQLMetricsBySeqID queries SQL metrics with seq_id > startSeqID (RFD 089).
func (s *BeylaStorage) QuerySQLMetricsBySeqID(ctx context.Context, startSeqID uint64, maxRecords int32, serviceNames []string) ([]*ebpfpb.BeylaSqlMetrics, uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if maxRecords <= 0 {
		maxRecords = 5000
	} else if maxRecords > 50000 {
		maxRecords = 50000
	}

	query := `
		SELECT seq_id, timestamp, service_name, sql_operation, table_name,
		       latency_bucket_ms, count, attributes::VARCHAR as attributes
		FROM beyla_sql_metrics_local
		WHERE seq_id > ?
	`
	args := []interface{}{startSeqID}

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

	query += " ORDER BY seq_id ASC LIMIT ?"
	args = append(args, maxRecords)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query SQL metrics by seq_id: %w", err)
	}
	defer func() { _ = rows.Close() }()

	metricsMap := make(map[string]*ebpfpb.BeylaSqlMetrics)
	var maxSeqID uint64

	for rows.Next() {
		var seqID uint64
		var timestamp time.Time
		var serviceName, sqlOperation, tableName string
		var bucket float64
		var count uint64
		var attributesJSON string

		err := rows.Scan(&seqID, &timestamp, &serviceName, &sqlOperation, &tableName,
			&bucket, &count, &attributesJSON)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan row: %w", err)
		}

		if seqID > maxSeqID {
			maxSeqID = seqID
		}

		key := fmt.Sprintf("%d_%s_%s_%s", timestamp.Unix(), serviceName, sqlOperation, tableName)
		metric, exists := metricsMap[key]
		if !exists {
			var attrs map[string]string
			if err := json.Unmarshal([]byte(attributesJSON), &attrs); err != nil {
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
		metric.LatencyBuckets = append(metric.LatencyBuckets, bucket)
		metric.LatencyCounts = append(metric.LatencyCounts, count)
	}

	metrics := make([]*ebpfpb.BeylaSqlMetrics, 0, len(metricsMap))
	for _, metric := range metricsMap {
		metrics = append(metrics, metric)
	}
	return metrics, maxSeqID, nil
}

// QueryTracesBySeqID queries trace spans with seq_id > startSeqID (RFD 089).
func (s *BeylaStorage) QueryTracesBySeqID(ctx context.Context, startSeqID uint64, maxRecords int32, serviceNames []string) ([]*ebpfpb.BeylaTraceSpan, uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if maxRecords <= 0 {
		maxRecords = 1000
	} else if maxRecords > 50000 {
		maxRecords = 50000
	}

	query := `
		SELECT seq_id, trace_id, span_id, parent_span_id, service_name, span_name, span_kind,
		       start_time, duration_us, status_code, attributes::VARCHAR as attributes
		FROM beyla_traces_local
		WHERE seq_id > ?
	`
	args := []interface{}{startSeqID}

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

	query += " ORDER BY seq_id ASC LIMIT ?"
	args = append(args, maxRecords)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query traces by seq_id: %w", err)
	}
	defer func() { _ = rows.Close() }()

	spans := make([]*ebpfpb.BeylaTraceSpan, 0)
	var maxSeqID uint64

	for rows.Next() {
		var seqID uint64
		var traceID, spanID, parentSpanID, serviceName, spanName, spanKind string
		var startTime time.Time
		var durationUs int64
		var statusCode int32
		var attributesJSON string

		err := rows.Scan(&seqID, &traceID, &spanID, &parentSpanID, &serviceName, &spanName,
			&spanKind, &startTime, &durationUs, &statusCode, &attributesJSON)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan row: %w", err)
		}

		if seqID > maxSeqID {
			maxSeqID = seqID
		}

		var attrs map[string]string
		if err := json.Unmarshal([]byte(attributesJSON), &attrs); err != nil {
			attrs = make(map[string]string)
		}

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
			StatusCode:   uint32(statusCode), // #nosec G115
			Attributes:   attrs,
		}
		spans = append(spans, span)
	}

	return spans, maxSeqID, nil
}

// QueryTraceByID queries all spans for a specific trace ID (RFD 036).
func (s *BeylaStorage) QueryTraceByID(ctx context.Context, traceID string) ([]*ebpfpb.BeylaTraceSpan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
		SELECT trace_id, span_id, parent_span_id, service_name, span_name, span_kind,
		       start_time, duration_us, status_code, attributes::VARCHAR as attributes
		FROM beyla_traces_local
		WHERE trace_id = ?
		ORDER BY start_time DESC
	`

	rows, err := s.db.QueryContext(ctx, query, traceID)
	if err != nil {
		return nil, fmt.Errorf("failed to query trace by ID: %w", err)
	}
	defer func() { _ = rows.Close() }()

	spans := make([]*ebpfpb.BeylaTraceSpan, 0)

	for rows.Next() {
		var tid, spanID, parentSpanID, serviceName, spanName, spanKind string
		var startTime time.Time
		var durationUs int64
		var statusCode int32
		var attributesJSON string

		err := rows.Scan(
			&tid,
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

		var attrs map[string]string
		if err := json.Unmarshal([]byte(attributesJSON), &attrs); err != nil {
			s.logger.Warn().Err(err).Msg("Failed to unmarshal trace attributes")
			attrs = make(map[string]string)
		}

		duration := time.Duration(durationUs) * time.Microsecond

		span := &ebpfpb.BeylaTraceSpan{
			TraceId:      tid,
			SpanId:       spanID,
			ParentSpanId: parentSpanID,
			ServiceName:  serviceName,
			SpanName:     spanName,
			SpanKind:     spanKind,
			StartTime:    timestamppb.New(startTime),
			Duration:     durationpb.New(duration),
			StatusCode:   uint32(statusCode), // #nosec G115 - Status codes are always positive
			Attributes:   attrs,
		}

		spans = append(spans, span)
	}

	return spans, nil
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

// GetDatabasePath returns the file path to the DuckDB database (RFD 039).
// Returns empty string if database is in-memory or path was not provided.
func (s *BeylaStorage) GetDatabasePath() string {
	return s.dbPath
}
