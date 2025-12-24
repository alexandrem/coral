package script

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	_ "github.com/marcboeker/go-duckdb" // DuckDB driver
	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	sdkv1 "github.com/coral/proto/coral/sdk/v1"
)

const (
	// DefaultSocketPath is the default Unix Domain Socket path.
	DefaultSocketPath = "/var/run/coral-sdk.sock"

	// MaxRowsPerQuery is the maximum number of rows returned by a single query.
	MaxRowsPerQuery = 10000

	// DefaultQueryTimeout is the default timeout for queries.
	DefaultQueryTimeout = 30 * time.Second

	// DaemonQueryTimeout is the timeout for daemon script queries.
	DaemonQueryTimeout = 24 * time.Hour

	// DefaultTimeFilter is the default time range filter applied to queries.
	DefaultTimeFilter = "1 hour"
)

// GRPCSDKServer provides gRPC endpoints for TypeScript scripts to access Coral data.
// It maintains a read-only connection pool to the agent's DuckDB database.
type GRPCSDKServer struct {
	sdkv1.UnimplementedSDKServiceServer

	socketPath string
	dbPath     string
	db         *sql.DB
	logger     zerolog.Logger
	server     *grpc.Server
	listener   net.Listener

	// Concurrency tracking.
	mu         sync.RWMutex
	activeReqs int64

	// Resource tracking.
	totalQueriesExecuted int64
	totalBytesScanned    int64
}

// NewGRPCSDKServer creates a new gRPC SDK server.
func NewGRPCSDKServer(socketPath string, dbPath string, logger zerolog.Logger) *GRPCSDKServer {
	if socketPath == "" {
		socketPath = DefaultSocketPath
	}

	return &GRPCSDKServer{
		socketPath: socketPath,
		dbPath:     dbPath,
		logger:     logger.With().Str("component", "sdk-grpc-server").Logger(),
	}
}

// Start starts the SDK gRPC server on a Unix Domain Socket.
func (s *GRPCSDKServer) Start(ctx context.Context) error {
	// Open DuckDB in read-only mode to allow concurrent readers.
	connStr := fmt.Sprintf("%s?access_mode=read_only&threads=4", s.dbPath)

	db, err := sql.Open("duckdb", connStr)
	if err != nil {
		return fmt.Errorf("failed to open DuckDB: %w", err)
	}

	// Configure connection pool for concurrent script access.
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(1 * time.Minute)

	// Verify connection works.
	if err := db.Ping(); err != nil {
		db.Close()
		return fmt.Errorf("failed to ping DuckDB: %w", err)
	}

	s.db = db

	s.logger.Info().
		Str("db_path", s.dbPath).
		Int("max_conns", 20).
		Msg("Opened read-only DuckDB connection pool")

	// Remove existing socket if it exists.
	if err := os.RemoveAll(s.socketPath); err != nil {
		return fmt.Errorf("failed to remove existing socket: %w", err)
	}

	// Create Unix domain socket listener.
	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on socket %s: %w", s.socketPath, err)
	}

	// Set socket permissions (readable/writable by user and group).
	if err := os.Chmod(s.socketPath, 0660); err != nil {
		listener.Close()
		return fmt.Errorf("failed to set socket permissions: %w", err)
	}

	s.listener = listener

	// Create gRPC server.
	s.server = grpc.NewServer(
		grpc.UnaryInterceptor(s.unaryInterceptor),
		grpc.StreamInterceptor(s.streamInterceptor),
	)

	// Register service.
	sdkv1.RegisterSDKServiceServer(s.server, s)

	// Start serving in background.
	go func() {
		s.logger.Info().Str("socket", s.socketPath).Msg("SDK gRPC server listening")
		if err := s.server.Serve(listener); err != nil {
			s.logger.Error().Err(err).Msg("SDK gRPC server error")
		}
	}()

	return nil
}

// Stop stops the SDK server and closes the database connection.
func (s *GRPCSDKServer) Stop(ctx context.Context) error {
	s.logger.Info().Msg("Stopping SDK gRPC server")

	// Graceful shutdown with timeout.
	if s.server != nil {
		stopped := make(chan struct{})
		go func() {
			s.server.GracefulStop()
			close(stopped)
		}()

		select {
		case <-stopped:
			s.logger.Info().Msg("gRPC server stopped gracefully")
		case <-time.After(5 * time.Second):
			s.logger.Warn().Msg("gRPC server did not stop gracefully, forcing stop")
			s.server.Stop()
		}
	}

	// Close database connection pool.
	if s.db != nil {
		if err := s.db.Close(); err != nil {
			s.logger.Error().Err(err).Msg("Error closing database")
			return err
		}
	}

	// Remove socket file.
	if s.socketPath != "" {
		os.Remove(s.socketPath)
	}

	return nil
}

// unaryInterceptor tracks active requests and adds logging.
func (s *GRPCSDKServer) unaryInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	s.mu.Lock()
	s.activeReqs++
	current := s.activeReqs
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.activeReqs--
		s.mu.Unlock()
	}()

	// Log high concurrency.
	if current > 10 {
		s.logger.Warn().
			Int64("active_requests", current).
			Str("method", info.FullMethod).
			Msg("High SDK server concurrency")
	}

	start := time.Now()
	resp, err := handler(ctx, req)
	duration := time.Since(start)

	s.logger.Debug().
		Str("method", info.FullMethod).
		Dur("duration", duration).
		Err(err).
		Msg("SDK request completed")

	return resp, err
}

// streamInterceptor tracks streaming requests.
func (s *GRPCSDKServer) streamInterceptor(
	srv interface{},
	ss grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	s.mu.Lock()
	s.activeReqs++
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.activeReqs--
		s.mu.Unlock()
	}()

	s.logger.Debug().
		Str("method", info.FullMethod).
		Msg("SDK stream started")

	return handler(srv, ss)
}

// Health implements the health check endpoint.
func (s *GRPCSDKServer) Health(ctx context.Context, req *sdkv1.HealthRequest) (*sdkv1.HealthResponse, error) {
	// Check database connectivity.
	if err := s.db.PingContext(ctx); err != nil {
		return &sdkv1.HealthResponse{
			Status:  "unhealthy",
			Message: fmt.Sprintf("Database error: %v", err),
		}, nil
	}

	s.mu.RLock()
	activeReqs := s.activeReqs
	s.mu.RUnlock()

	stats := s.db.Stats()

	return &sdkv1.HealthResponse{
		Status:  "healthy",
		Message: "SDK server is operational",
		DbStats: &sdkv1.DBStats{
			OpenConnections: int32(stats.OpenConnections),
			InUse:           int32(stats.InUse),
			Idle:            int32(stats.Idle),
		},
		ActiveRequests: activeReqs,
	}, nil
}

// Query implements the raw SQL query endpoint with semantic guardrails.
func (s *GRPCSDKServer) Query(ctx context.Context, req *sdkv1.QueryRequest) (*sdkv1.QueryResponse, error) {
	if req.Sql == "" {
		return nil, fmt.Errorf("SQL query is required")
	}

	// Apply semantic guardrails.
	sql := req.Sql
	guardrailsApplied := false

	// Auto-inject LIMIT clause if not present and not disabled.
	if !req.DisableLimitInjection && !hasLimit(sql) {
		sql = injectLimit(sql, MaxRowsPerQuery)
		guardrailsApplied = true
	}

	// Auto-inject time filter if not present and not disabled.
	if !req.DisableTimeFilterInjection && !hasTimeFilter(sql) {
		sql = injectTimeFilter(sql, DefaultTimeFilter)
		guardrailsApplied = true
	}

	// Set query timeout based on script type.
	timeout := DefaultQueryTimeout
	if req.QueryType == sdkv1.QueryType_QUERY_TYPE_DAEMON {
		timeout = DaemonQueryTimeout
	}

	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Execute query.
	start := time.Now()
	rows, err := s.db.QueryContext(queryCtx, sql)
	if err != nil {
		s.logger.Error().Err(err).Str("sql", sql).Msg("Query failed")
		return nil, fmt.Errorf("query execution failed: %w", err)
	}
	defer rows.Close()

	// Get column names.
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	// Read rows with limit.
	results := make([]*sdkv1.Row, 0)
	rowsScanned := int64(0)

	for rows.Next() && len(results) < MaxRowsPerQuery {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range columns {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Convert to protobuf Row.
		row := &sdkv1.Row{
			Values: make(map[string]*sdkv1.Value),
		}

		for i, col := range columns {
			row.Values[col] = convertToProtoValue(values[i])
		}

		results = append(results, row)
		rowsScanned++
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	executionTime := time.Since(start)

	// Track metrics.
	s.mu.Lock()
	s.totalQueriesExecuted++
	s.mu.Unlock()

	return &sdkv1.QueryResponse{
		Rows:      results,
		Count:     int32(len(results)),
		Truncated: len(results) >= MaxRowsPerQuery,
		Stats: &sdkv1.QueryStats{
			ExecutionTime:     durationpb.New(executionTime),
			RowsScanned:       rowsScanned,
			BytesScanned:      0, // TODO: Track bytes scanned
			GuardrailsApplied: guardrailsApplied,
		},
	}, nil
}

// GetPercentile implements high-level percentile query helper.
func (s *GRPCSDKServer) GetPercentile(ctx context.Context, req *sdkv1.GetPercentileRequest) (*sdkv1.GetPercentileResponse, error) {
	if req.Service == "" || req.Metric == "" {
		return nil, fmt.Errorf("service and metric are required")
	}

	if req.Percentile < 0 || req.Percentile > 1 {
		return nil, fmt.Errorf("percentile must be between 0 and 1")
	}

	timeWindow := time.Duration(req.TimeWindowMs) * time.Millisecond
	if timeWindow == 0 {
		timeWindow = 5 * time.Minute
	}

	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Query percentile from DuckDB.
	query := `
		SELECT PERCENTILE_CONT($1) WITHIN GROUP (ORDER BY duration_ns) as value
		FROM otel_spans_local
		WHERE service_name = $2
		  AND start_time > now() - INTERVAL $3
	`

	start := time.Now()
	var value float64
	err := s.db.QueryRowContext(queryCtx, query, req.Percentile, req.Service, fmt.Sprintf("%d milliseconds", timeWindow.Milliseconds())).Scan(&value)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to query percentile")
		return nil, fmt.Errorf("percentile query failed: %w", err)
	}

	executionTime := time.Since(start)

	return &sdkv1.GetPercentileResponse{
		Value: value,
		Unit:  "nanoseconds",
		Stats: &sdkv1.QueryStats{
			ExecutionTime:     durationpb.New(executionTime),
			RowsScanned:       1,
			BytesScanned:      0,
			GuardrailsApplied: false,
		},
	}, nil
}

// GetErrorRate implements high-level error rate query helper.
func (s *GRPCSDKServer) GetErrorRate(ctx context.Context, req *sdkv1.GetErrorRateRequest) (*sdkv1.GetErrorRateResponse, error) {
	if req.Service == "" {
		return nil, fmt.Errorf("service is required")
	}

	timeWindow := time.Duration(req.TimeWindowMs) * time.Millisecond
	if timeWindow == 0 {
		timeWindow = 5 * time.Minute
	}

	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	query := `
		SELECT
			COUNT(*) as total,
			COUNT(CASE WHEN is_error THEN 1 END) as errors
		FROM otel_spans_local
		WHERE service_name = $1
		  AND start_time > now() - INTERVAL $2
	`

	start := time.Now()
	var total, errors int64
	err := s.db.QueryRowContext(queryCtx, query, req.Service, fmt.Sprintf("%d milliseconds", timeWindow.Milliseconds())).Scan(&total, &errors)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to query error rate")
		return nil, fmt.Errorf("error rate query failed: %w", err)
	}

	executionTime := time.Since(start)

	errorRate := 0.0
	if total > 0 {
		errorRate = float64(errors) / float64(total)
	}

	return &sdkv1.GetErrorRateResponse{
		Rate:          errorRate,
		TotalRequests: total,
		ErrorRequests: errors,
		Stats: &sdkv1.QueryStats{
			ExecutionTime:     durationpb.New(executionTime),
			RowsScanned:       1,
			BytesScanned:      0,
			GuardrailsApplied: false,
		},
	}, nil
}

// FindSlowTraces implements slow trace discovery.
func (s *GRPCSDKServer) FindSlowTraces(ctx context.Context, req *sdkv1.FindSlowTracesRequest) (*sdkv1.FindSlowTracesResponse, error) {
	if req.Service == "" {
		return nil, fmt.Errorf("service is required")
	}

	timeRange := time.Duration(req.TimeRangeMs) * time.Millisecond
	if timeRange == 0 {
		timeRange = 1 * time.Hour
	}

	limit := req.Limit
	if limit == 0 || limit > MaxRowsPerQuery {
		limit = 100
	}

	minDurationNs := req.MinDurationMs * 1_000_000

	queryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	query := `
		SELECT trace_id, span_id, service_name, duration_ns, is_error,
		       http_status, http_method, http_route, start_time
		FROM otel_spans_local
		WHERE service_name = $1
		  AND duration_ns > $2
		  AND start_time > now() - INTERVAL $3
		ORDER BY duration_ns DESC
		LIMIT $4
	`

	start := time.Now()
	rows, err := s.db.QueryContext(queryCtx, query, req.Service, minDurationNs,
		fmt.Sprintf("%d milliseconds", timeRange.Milliseconds()), limit)
	if err != nil {
		return nil, fmt.Errorf("slow traces query failed: %w", err)
	}
	defer rows.Close()

	traces := make([]*sdkv1.Trace, 0)
	for rows.Next() {
		var trace sdkv1.Trace
		var startTime time.Time

		err := rows.Scan(
			&trace.TraceId,
			&trace.SpanId,
			&trace.ServiceName,
			&trace.DurationNs,
			&trace.IsError,
			&trace.HttpStatus,
			&trace.HttpMethod,
			&trace.HttpRoute,
			&startTime,
		)
		if err != nil {
			s.logger.Error().Err(err).Msg("Failed to scan trace row")
			continue
		}

		trace.StartTime = timestamppb.New(startTime)
		traces = append(traces, &trace)
	}

	executionTime := time.Since(start)

	return &sdkv1.FindSlowTracesResponse{
		Traces:     traces,
		TotalCount: int32(len(traces)),
		Stats: &sdkv1.QueryStats{
			ExecutionTime:     durationpb.New(executionTime),
			RowsScanned:       int64(len(traces)),
			BytesScanned:      0,
			GuardrailsApplied: false,
		},
	}, nil
}

// CorrelateTraces implements trace correlation across services.
func (s *GRPCSDKServer) CorrelateTraces(ctx context.Context, req *sdkv1.CorrelateTracesRequest) (*sdkv1.CorrelateTracesResponse, error) {
	if req.TraceId == "" {
		return nil, fmt.Errorf("trace_id is required")
	}

	queryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// Build query to find all spans in the trace.
	query := `
		SELECT trace_id, span_id, service_name, duration_ns, is_error,
		       http_status, http_method, http_route, start_time
		FROM otel_spans_local
		WHERE trace_id = $1
	`

	// Add service filter if specified.
	args := []interface{}{req.TraceId}
	if len(req.Services) > 0 {
		query += " AND service_name IN ("
		for i, svc := range req.Services {
			if i > 0 {
				query += ", "
			}
			query += fmt.Sprintf("$%d", i+2)
			args = append(args, svc)
		}
		query += ")"
	}

	query += " ORDER BY start_time ASC"

	start := time.Now()
	rows, err := s.db.QueryContext(queryCtx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("correlation query failed: %w", err)
	}
	defer rows.Close()

	traces := make([]*sdkv1.Trace, 0)
	for rows.Next() {
		var trace sdkv1.Trace
		var startTime time.Time

		err := rows.Scan(
			&trace.TraceId,
			&trace.SpanId,
			&trace.ServiceName,
			&trace.DurationNs,
			&trace.IsError,
			&trace.HttpStatus,
			&trace.HttpMethod,
			&trace.HttpRoute,
			&startTime,
		)
		if err != nil {
			continue
		}

		trace.StartTime = timestamppb.New(startTime)
		traces = append(traces, &trace)
	}

	executionTime := time.Since(start)

	// TODO: Implement correlation analysis.
	correlations := make([]*sdkv1.Correlation, 0)

	return &sdkv1.CorrelateTracesResponse{
		RelatedTraces: traces,
		Correlations:  correlations,
		Stats: &sdkv1.QueryStats{
			ExecutionTime:     durationpb.New(executionTime),
			RowsScanned:       int64(len(traces)),
			BytesScanned:      0,
			GuardrailsApplied: false,
		},
	}, nil
}

// GetSystemMetrics implements system metrics query.
func (s *GRPCSDKServer) GetSystemMetrics(ctx context.Context, req *sdkv1.GetSystemMetricsRequest) (*sdkv1.GetSystemMetricsResponse, error) {
	if len(req.MetricNames) == 0 {
		return nil, fmt.Errorf("at least one metric name is required")
	}

	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Build query for requested metrics.
	query := `
		SELECT name, value, unit, timestamp
		FROM system_metrics_local
		WHERE name IN (`

	args := make([]interface{}, len(req.MetricNames))
	for i, name := range req.MetricNames {
		if i > 0 {
			query += ", "
		}
		query += fmt.Sprintf("$%d", i+1)
		args[i] = name
	}

	query += `) ORDER BY timestamp DESC LIMIT ` + fmt.Sprintf("%d", len(req.MetricNames))

	start := time.Now()
	rows, err := s.db.QueryContext(queryCtx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("system metrics query failed: %w", err)
	}
	defer rows.Close()

	metrics := make(map[string]*sdkv1.Metric)
	for rows.Next() {
		var name, unit string
		var value float64
		var timestamp time.Time

		if err := rows.Scan(&name, &value, &unit, &timestamp); err != nil {
			continue
		}

		metrics[name] = &sdkv1.Metric{
			Value:     value,
			Unit:      unit,
			Timestamp: timestamppb.New(timestamp),
		}
	}

	executionTime := time.Since(start)

	return &sdkv1.GetSystemMetricsResponse{
		Metrics: metrics,
		Stats: &sdkv1.QueryStats{
			ExecutionTime:     durationpb.New(executionTime),
			RowsScanned:       int64(len(metrics)),
			BytesScanned:      0,
			GuardrailsApplied: false,
		},
	}, nil
}

// Emit implements custom event emission.
func (s *GRPCSDKServer) Emit(ctx context.Context, req *sdkv1.EmitRequest) (*sdkv1.EmitResponse, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("event name is required")
	}

	// Log the event (in production, forward to colony).
	s.logger.Info().
		Str("event_name", req.Name).
		Str("severity", req.Severity.String()).
		Interface("data", req.Data).
		Msg("Script event emitted")

	// TODO: Forward event to colony via gRPC.

	return &sdkv1.EmitResponse{
		Success: true,
		EventId: fmt.Sprintf("evt-%d", time.Now().UnixNano()),
	}, nil
}

// AttachUprobe implements Level 3 eBPF userspace probe attachment.
func (s *GRPCSDKServer) AttachUprobe(req *sdkv1.AttachUprobeRequest, stream sdkv1.SDKService_AttachUprobeServer) error {
	// TODO: Implement eBPF uprobe attachment.
	// This requires integration with RFD 063 (function metadata) and eBPF subsystem.
	return fmt.Errorf("uprobe attachment not yet implemented (requires Level 3 authorization)")
}

// AttachKprobe implements Level 3 eBPF kernel probe attachment.
func (s *GRPCSDKServer) AttachKprobe(req *sdkv1.AttachKprobeRequest, stream sdkv1.SDKService_AttachKprobeServer) error {
	// TODO: Implement eBPF kprobe attachment.
	return fmt.Errorf("kprobe attachment not yet implemented (requires Level 3 authorization)")
}

// DetachProbe implements Level 3 probe detachment.
func (s *GRPCSDKServer) DetachProbe(ctx context.Context, req *sdkv1.DetachProbeRequest) (*sdkv1.DetachProbeResponse, error) {
	// TODO: Implement probe detachment.
	return nil, fmt.Errorf("probe detachment not yet implemented")
}

// Helper functions for semantic guardrails.

func hasLimit(sql string) bool {
	upperSQL := strings.ToUpper(sql)
	return strings.Contains(upperSQL, "LIMIT")
}

func injectLimit(sql string, limit int) string {
	// Simple implementation: append LIMIT to the end.
	// TODO: More sophisticated SQL parsing for subqueries.
	return fmt.Sprintf("%s LIMIT %d", strings.TrimSuffix(sql, ";"), limit)
}

func hasTimeFilter(sql string) bool {
	upperSQL := strings.ToUpper(sql)
	// Check if query already has time-based WHERE clause.
	return strings.Contains(upperSQL, "START_TIME >") ||
		strings.Contains(upperSQL, "TIMESTAMP >") ||
		strings.Contains(upperSQL, "TIME >")
}

func injectTimeFilter(sql string, timeRange string) string {
	// Simple implementation: inject WHERE clause if not present, or AND clause if WHERE exists.
	// TODO: More sophisticated SQL parsing.
	upperSQL := strings.ToUpper(sql)

	timeFilter := fmt.Sprintf("start_time > now() - INTERVAL '%s'", timeRange)

	if strings.Contains(upperSQL, "WHERE") {
		// Add AND clause.
		return strings.Replace(sql, "WHERE", fmt.Sprintf("WHERE %s AND ", timeFilter), 1)
	} else {
		// Add WHERE clause before ORDER BY or LIMIT if present.
		for _, keyword := range []string{"ORDER BY", "LIMIT", "GROUP BY"} {
			if strings.Contains(upperSQL, keyword) {
				return strings.Replace(sql, keyword, fmt.Sprintf("WHERE %s %s", timeFilter, keyword), 1)
			}
		}
		// Append at the end.
		return fmt.Sprintf("%s WHERE %s", strings.TrimSuffix(sql, ";"), timeFilter)
	}
}

func convertToProtoValue(val interface{}) *sdkv1.Value {
	if val == nil {
		return &sdkv1.Value{Kind: &sdkv1.Value_NullValue{NullValue: true}}
	}

	switch v := val.(type) {
	case string:
		return &sdkv1.Value{Kind: &sdkv1.Value_StringValue{StringValue: v}}
	case int64:
		return &sdkv1.Value{Kind: &sdkv1.Value_IntValue{IntValue: v}}
	case float64:
		return &sdkv1.Value{Kind: &sdkv1.Value_FloatValue{FloatValue: v}}
	case bool:
		return &sdkv1.Value{Kind: &sdkv1.Value_BoolValue{BoolValue: v}}
	case time.Time:
		return &sdkv1.Value{Kind: &sdkv1.Value_TimestampValue{TimestampValue: timestamppb.New(v)}}
	default:
		// Fallback to string representation.
		return &sdkv1.Value{Kind: &sdkv1.Value_StringValue{StringValue: fmt.Sprintf("%v", v)}}
	}
}
