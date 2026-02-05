package colony

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
)

// mockDatabase implements a mock database for testing.
type mockDatabase struct {
	httpMetrics            []*database.BeylaHTTPMetricResult
	grpcMetrics            []*database.BeylaGRPCMetricResult
	sqlMetrics             []*database.BeylaSQLMetricResult
	traceResults           []*database.BeylaTraceResult
	telemetrySummaries     []database.TelemetrySummary
	systemMetricsSummaries []database.SystemMetricsSummary
	services               map[string]*database.Service
	queryError             error
	// RFD 074: Profiling-enriched summary fields.
	profilingResult      *database.ProfilingSummaryResult
	latestBinaryMetadata *database.BinaryMetadata
	prevBinaryMetadata   *database.BinaryMetadata
	regressionIndicators []database.RegressionIndicatorResult
	// RFD 077: Memory profiling fields.
	memoryProfilingResult *database.MemoryProfilingSummaryResult
}

func (m *mockDatabase) QueryBeylaHTTPMetrics(ctx context.Context, serviceName string, startTime, endTime time.Time, filters map[string]string) ([]*database.BeylaHTTPMetricResult, error) {
	if m.queryError != nil {
		return nil, m.queryError
	}
	return m.httpMetrics, nil
}

func (m *mockDatabase) QueryBeylaGRPCMetrics(ctx context.Context, serviceName string, startTime, endTime time.Time, filters map[string]string) ([]*database.BeylaGRPCMetricResult, error) {
	if m.queryError != nil {
		return nil, m.queryError
	}
	return m.grpcMetrics, nil
}

func (m *mockDatabase) QueryBeylaSQLMetrics(ctx context.Context, serviceName string, startTime, endTime time.Time, filters map[string]string) ([]*database.BeylaSQLMetricResult, error) {
	if m.queryError != nil {
		return nil, m.queryError
	}
	return m.sqlMetrics, nil
}

func (m *mockDatabase) QueryBeylaTraces(ctx context.Context, traceID, serviceName string, startTime, endTime time.Time, minDurationUs int64, maxTraces int) ([]*database.BeylaTraceResult, error) {
	if m.queryError != nil {
		return nil, m.queryError
	}
	return m.traceResults, nil
}

func (m *mockDatabase) QueryTelemetrySummaries(ctx context.Context, agentID string, startTime, endTime time.Time) ([]database.TelemetrySummary, error) {
	if m.queryError != nil {
		return nil, m.queryError
	}
	// Return empty by default, tests can override by setting telemetrySummaries field
	if m.telemetrySummaries != nil {
		return m.telemetrySummaries, nil
	}
	return []database.TelemetrySummary{}, nil
}

func (m *mockDatabase) QuerySystemMetricsSummaries(ctx context.Context, agentID string, startTime, endTime time.Time) ([]database.SystemMetricsSummary, error) {
	if m.queryError != nil {
		return nil, m.queryError
	}
	// Return empty by default, tests can override by setting systemMetricsSummaries field (RFD 071).
	if m.systemMetricsSummaries != nil {
		return m.systemMetricsSummaries, nil
	}
	return []database.SystemMetricsSummary{}, nil
}

func (m *mockDatabase) GetServiceByName(ctx context.Context, serviceName string) (*database.Service, error) {
	if m.queryError != nil {
		return nil, m.queryError
	}
	if svc, ok := m.services[serviceName]; ok {
		return svc, nil
	}
	return nil, nil // Return nil by default
}

func (m *mockDatabase) QueryAllServiceNames(ctx context.Context) ([]string, error) {
	if m.queryError != nil {
		return nil, m.queryError
	}
	names := make([]string, 0, len(m.services))
	for name := range m.services {
		names = append(names, name)
	}
	return names, nil
}

// RFD 074: Profiling-enriched summary mock methods.

func (m *mockDatabase) GetTopKHotspots(_ context.Context, _ string, _, _ time.Time, _ int) (*database.ProfilingSummaryResult, error) {
	if m.profilingResult != nil {
		return m.profilingResult, nil
	}
	return &database.ProfilingSummaryResult{}, nil
}

func (m *mockDatabase) GetLatestBinaryMetadata(_ context.Context, _ string) (*database.BinaryMetadata, error) {
	if m.latestBinaryMetadata != nil {
		return m.latestBinaryMetadata, nil
	}
	return nil, fmt.Errorf("no binary metadata")
}

func (m *mockDatabase) GetPreviousBinaryMetadata(_ context.Context, _, _ string) (*database.BinaryMetadata, error) {
	if m.prevBinaryMetadata != nil {
		return m.prevBinaryMetadata, nil
	}
	return nil, fmt.Errorf("no previous binary metadata")
}

func (m *mockDatabase) CompareHotspotsWithBaseline(_ context.Context, _, _, _ string, _, _ time.Time, _ int) ([]database.RegressionIndicatorResult, error) {
	return m.regressionIndicators, nil
}

// RFD 077: Memory profiling mock method.

func (m *mockDatabase) GetTopKMemoryHotspots(_ context.Context, _ string, _, _ time.Time, _ int) (*database.MemoryProfilingSummaryResult, error) {
	if m.memoryProfilingResult != nil {
		return m.memoryProfilingResult, nil
	}
	return &database.MemoryProfilingSummaryResult{}, nil
}

// TestQueryMetrics_TimeRangeValidation tests time range validation.
func TestQueryMetrics_TimeRangeValidation(t *testing.T) {
	mockDB := &mockDatabase{}
	service := &EbpfQueryService{db: mockDB}
	ctx := context.Background()

	tests := []struct {
		name        string
		startTime   int64
		endTime     int64
		wantErr     bool
		errContains string
	}{
		{
			name:        "missing start time",
			startTime:   0,
			endTime:     time.Now().Unix(),
			wantErr:     true,
			errContains: "start_time and end_time are required",
		},
		{
			name:        "missing end time",
			startTime:   time.Now().Unix(),
			endTime:     0,
			wantErr:     true,
			errContains: "start_time and end_time are required",
		},
		{
			name:        "start after end",
			startTime:   time.Now().Unix(),
			endTime:     time.Now().Add(-1 * time.Hour).Unix(),
			wantErr:     true,
			errContains: "start_time must be before end_time",
		},
		{
			name:        "end time in future",
			startTime:   time.Now().Unix(),
			endTime:     time.Now().Add(2 * time.Hour).Unix(),
			wantErr:     true,
			errContains: "end_time cannot be more than 1 hour in the future",
		},
		{
			name:        "start time too far in past",
			startTime:   time.Now().Add(-31 * 24 * time.Hour).Unix(),
			endTime:     time.Now().Unix(),
			wantErr:     true,
			errContains: "start_time cannot be more than 30 days in the past",
		},
		{
			name:      "valid time range",
			startTime: time.Now().Add(-1 * time.Hour).Unix(),
			endTime:   time.Now().Unix(),
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &agentv1.QueryEbpfMetricsRequest{
				StartTime:   tt.startTime,
				EndTime:     tt.endTime,
				MetricTypes: []agentv1.EbpfMetricType{agentv1.EbpfMetricType_EBPF_METRIC_TYPE_HTTP},
			}

			_, err := service.QueryMetrics(ctx, req)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestQueryHTTPMetrics_Aggregation tests histogram aggregation for HTTP metrics.
func TestQueryHTTPMetrics_Aggregation(t *testing.T) {
	now := time.Now()
	startTime := now.Add(-1 * time.Hour)
	endTime := now

	// Mock database returns individual bucket rows.
	mockDB := &mockDatabase{
		httpMetrics: []*database.BeylaHTTPMetricResult{
			// First metric: GET /users with multiple buckets
			{
				ServiceName:     "api-server",
				HTTPMethod:      "GET",
				HTTPRoute:       "/users",
				HTTPStatusCode:  200,
				LatencyBucketMs: 10.0,
				Count:           50,
				FirstSeen:       startTime,
				LastSeen:        endTime,
			},
			{
				ServiceName:     "api-server",
				HTTPMethod:      "GET",
				HTTPRoute:       "/users",
				HTTPStatusCode:  200,
				LatencyBucketMs: 50.0,
				Count:           30,
				FirstSeen:       startTime,
				LastSeen:        endTime,
			},
			{
				ServiceName:     "api-server",
				HTTPMethod:      "GET",
				HTTPRoute:       "/users",
				HTTPStatusCode:  200,
				LatencyBucketMs: 100.0,
				Count:           10,
				FirstSeen:       startTime,
				LastSeen:        endTime,
			},
			// Second metric: POST /users with error status
			{
				ServiceName:     "api-server",
				HTTPMethod:      "POST",
				HTTPRoute:       "/users",
				HTTPStatusCode:  500,
				LatencyBucketMs: 200.0,
				Count:           5,
				FirstSeen:       startTime,
				LastSeen:        endTime,
			},
		},
	}

	service := &EbpfQueryService{db: mockDB}
	ctx := context.Background()

	req := &agentv1.QueryEbpfMetricsRequest{
		StartTime:    startTime.Unix(),
		EndTime:      endTime.Unix(),
		ServiceNames: []string{"api-server"},
		MetricTypes:  []agentv1.EbpfMetricType{agentv1.EbpfMetricType_EBPF_METRIC_TYPE_HTTP},
	}

	resp, err := service.QueryMetrics(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Should have 2 aggregated metrics (one for GET 200, one for POST 500).
	assert.Len(t, resp.HttpMetrics, 2)

	// Find the GET /users metric.
	var getMetric *agentv1.EbpfHttpMetric
	for _, m := range resp.HttpMetrics {
		if m.HttpMethod == "GET" && m.HttpRoute == "/users" {
			getMetric = m
			break
		}
	}
	require.NotNil(t, getMetric, "GET /users metric should exist")

	// Verify aggregation.
	assert.Equal(t, "api-server", getMetric.ServiceName)
	assert.Equal(t, "GET", getMetric.HttpMethod)
	assert.Equal(t, "/users", getMetric.HttpRoute)
	assert.Equal(t, uint32(200), getMetric.HttpStatusCode)
	assert.Equal(t, uint64(90), getMetric.RequestCount) // 50 + 30 + 10
	assert.Equal(t, []float64{10.0, 50.0, 100.0}, getMetric.LatencyBuckets)
	assert.Equal(t, []uint64{50, 30, 10}, getMetric.LatencyCounts)

	// Find the POST /users metric.
	var postMetric *agentv1.EbpfHttpMetric
	for _, m := range resp.HttpMetrics {
		if m.HttpMethod == "POST" && m.HttpRoute == "/users" {
			postMetric = m
			break
		}
	}
	require.NotNil(t, postMetric, "POST /users metric should exist")

	// Verify POST metric.
	assert.Equal(t, "POST", postMetric.HttpMethod)
	assert.Equal(t, uint32(500), postMetric.HttpStatusCode)
	assert.Equal(t, uint64(5), postMetric.RequestCount)
}

// TestQueryHTTPMetrics_EmptyServiceNames tests behavior with empty service names.
func TestQueryHTTPMetrics_EmptyServiceNames(t *testing.T) {
	now := time.Now()
	startTime := now.Add(-1 * time.Hour)
	endTime := now

	mockDB := &mockDatabase{
		httpMetrics: []*database.BeylaHTTPMetricResult{
			{
				ServiceName:     "service-1",
				HTTPMethod:      "GET",
				HTTPRoute:       "/",
				HTTPStatusCode:  200,
				LatencyBucketMs: 10.0,
				Count:           100,
				FirstSeen:       startTime,
				LastSeen:        endTime,
			},
		},
	}

	service := &EbpfQueryService{db: mockDB}
	ctx := context.Background()

	// Empty service names should query all services.
	req := &agentv1.QueryEbpfMetricsRequest{
		StartTime:    startTime.Unix(),
		EndTime:      endTime.Unix(),
		ServiceNames: []string{}, // Empty
		MetricTypes:  []agentv1.EbpfMetricType{agentv1.EbpfMetricType_EBPF_METRIC_TYPE_HTTP},
	}

	resp, err := service.QueryMetrics(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.HttpMetrics, 1)
	assert.Equal(t, "service-1", resp.HttpMetrics[0].ServiceName)
}

// TestQueryGRPCMetrics_Aggregation tests gRPC metrics aggregation.
func TestQueryGRPCMetrics_Aggregation(t *testing.T) {
	now := time.Now()
	startTime := now.Add(-1 * time.Hour)
	endTime := now

	mockDB := &mockDatabase{
		grpcMetrics: []*database.BeylaGRPCMetricResult{
			{
				ServiceName:     "payment-service",
				GRPCMethod:      "/payment.Service/Process",
				GRPCStatusCode:  0,
				LatencyBucketMs: 20.0,
				Count:           100,
				FirstSeen:       startTime,
				LastSeen:        endTime,
			},
			{
				ServiceName:     "payment-service",
				GRPCMethod:      "/payment.Service/Process",
				GRPCStatusCode:  0,
				LatencyBucketMs: 50.0,
				Count:           50,
				FirstSeen:       startTime,
				LastSeen:        endTime,
			},
		},
	}

	service := &EbpfQueryService{db: mockDB}
	ctx := context.Background()

	req := &agentv1.QueryEbpfMetricsRequest{
		StartTime:    startTime.Unix(),
		EndTime:      endTime.Unix(),
		ServiceNames: []string{"payment-service"},
		MetricTypes:  []agentv1.EbpfMetricType{agentv1.EbpfMetricType_EBPF_METRIC_TYPE_GRPC},
	}

	resp, err := service.QueryMetrics(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Len(t, resp.GrpcMetrics, 1)
	metric := resp.GrpcMetrics[0]

	assert.Equal(t, "payment-service", metric.ServiceName)
	assert.Equal(t, "/payment.Service/Process", metric.GrpcMethod)
	assert.Equal(t, uint32(0), metric.GrpcStatusCode)
	assert.Equal(t, uint64(150), metric.RequestCount) // 100 + 50
	assert.Equal(t, []float64{20.0, 50.0}, metric.LatencyBuckets)
	assert.Equal(t, []uint64{100, 50}, metric.LatencyCounts)
}

// TestQuerySQLMetrics_Aggregation tests SQL metrics aggregation.
func TestQuerySQLMetrics_Aggregation(t *testing.T) {
	now := time.Now()
	startTime := now.Add(-1 * time.Hour)
	endTime := now

	mockDB := &mockDatabase{
		sqlMetrics: []*database.BeylaSQLMetricResult{
			{
				ServiceName:     "api-server",
				SQLOperation:    "SELECT",
				TableName:       "users",
				LatencyBucketMs: 5.0,
				Count:           200,
				FirstSeen:       startTime,
				LastSeen:        endTime,
			},
			{
				ServiceName:     "api-server",
				SQLOperation:    "SELECT",
				TableName:       "users",
				LatencyBucketMs: 15.0,
				Count:           50,
				FirstSeen:       startTime,
				LastSeen:        endTime,
			},
		},
	}

	service := &EbpfQueryService{db: mockDB}
	ctx := context.Background()

	req := &agentv1.QueryEbpfMetricsRequest{
		StartTime:    startTime.Unix(),
		EndTime:      endTime.Unix(),
		ServiceNames: []string{"api-server"},
		MetricTypes:  []agentv1.EbpfMetricType{agentv1.EbpfMetricType_EBPF_METRIC_TYPE_SQL},
	}

	resp, err := service.QueryMetrics(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Len(t, resp.SqlMetrics, 1)
	metric := resp.SqlMetrics[0]

	assert.Equal(t, "api-server", metric.ServiceName)
	assert.Equal(t, "SELECT", metric.SqlOperation)
	assert.Equal(t, "users", metric.TableName)
	assert.Equal(t, uint64(250), metric.QueryCount) // 200 + 50
	assert.Equal(t, []float64{5.0, 15.0}, metric.LatencyBuckets)
	assert.Equal(t, []uint64{200, 50}, metric.LatencyCounts)
}

// TestQueryTraceSpans_TraceIDFilter tests trace ID filtering.
func TestQueryTraceSpans_TraceIDFilter(t *testing.T) {
	now := time.Now()
	startTime := now.Add(-1 * time.Hour)
	endTime := now

	mockDB := &mockDatabase{
		traceResults: []*database.BeylaTraceResult{
			{
				TraceID:      "abc123",
				SpanID:       "span-1",
				ParentSpanID: "",
				ServiceName:  "api-gateway",
				SpanName:     "/checkout",
				SpanKind:     "server",
				StartTime:    startTime,
				DurationUs:   1000,
				StatusCode:   0,
			},
			{
				TraceID:      "abc123",
				SpanID:       "span-2",
				ParentSpanID: "span-1",
				ServiceName:  "payment-service",
				SpanName:     "processPayment",
				SpanKind:     "server",
				StartTime:    startTime.Add(100 * time.Microsecond),
				DurationUs:   500,
				StatusCode:   0,
			},
		},
	}

	service := &EbpfQueryService{db: mockDB}
	ctx := context.Background()

	// Query by trace ID.
	req := &agentv1.QueryEbpfMetricsRequest{
		StartTime:     startTime.Unix(),
		EndTime:       endTime.Unix(),
		TraceId:       "abc123",
		IncludeTraces: true,
		MaxTraces:     100,
	}

	resp, err := service.QueryMetrics(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Len(t, resp.TraceSpans, 2)

	// Verify first span.
	span1 := resp.TraceSpans[0]
	assert.Equal(t, "abc123", span1.TraceId)
	assert.Equal(t, "span-1", span1.SpanId)
	assert.Equal(t, "", span1.ParentSpanId)
	assert.Equal(t, "api-gateway", span1.ServiceName)
	assert.Equal(t, "/checkout", span1.SpanName)
	assert.Equal(t, int64(1000), span1.DurationUs)

	// Verify second span.
	span2 := resp.TraceSpans[1]
	assert.Equal(t, "abc123", span2.TraceId)
	assert.Equal(t, "span-2", span2.SpanId)
	assert.Equal(t, "span-1", span2.ParentSpanId)
	assert.Equal(t, "payment-service", span2.ServiceName)
}

// TestQueryTraceSpans_ServiceFilter tests service name filtering for traces.
func TestQueryTraceSpans_ServiceFilter(t *testing.T) {
	now := time.Now()
	startTime := now.Add(-1 * time.Hour)
	endTime := now

	mockDB := &mockDatabase{
		traceResults: []*database.BeylaTraceResult{
			{
				TraceID:     "xyz789",
				SpanID:      "span-1",
				ServiceName: "specific-service",
				SpanName:    "operation",
				SpanKind:    "server",
				StartTime:   startTime,
				DurationUs:  1000,
				StatusCode:  0,
			},
		},
	}

	service := &EbpfQueryService{db: mockDB}
	ctx := context.Background()

	// Query by service name (no trace ID).
	req := &agentv1.QueryEbpfMetricsRequest{
		StartTime:     startTime.Unix(),
		EndTime:       endTime.Unix(),
		ServiceNames:  []string{"specific-service"},
		IncludeTraces: true,
		MaxTraces:     100,
	}

	resp, err := service.QueryMetrics(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Len(t, resp.TraceSpans, 1)
	assert.Equal(t, "specific-service", resp.TraceSpans[0].ServiceName)
}

// TestQueryMetrics_MultipleMetricTypes tests querying multiple metric types at once.
func TestQueryMetrics_MultipleMetricTypes(t *testing.T) {
	now := time.Now()
	startTime := now.Add(-1 * time.Hour)
	endTime := now

	mockDB := &mockDatabase{
		httpMetrics: []*database.BeylaHTTPMetricResult{
			{
				ServiceName:     "api",
				HTTPMethod:      "GET",
				HTTPRoute:       "/",
				HTTPStatusCode:  200,
				LatencyBucketMs: 10.0,
				Count:           100,
				FirstSeen:       startTime,
				LastSeen:        endTime,
			},
		},
		grpcMetrics: []*database.BeylaGRPCMetricResult{
			{
				ServiceName:     "api",
				GRPCMethod:      "/api.Service/Method",
				GRPCStatusCode:  0,
				LatencyBucketMs: 20.0,
				Count:           50,
				FirstSeen:       startTime,
				LastSeen:        endTime,
			},
		},
		sqlMetrics: []*database.BeylaSQLMetricResult{
			{
				ServiceName:     "api",
				SQLOperation:    "SELECT",
				TableName:       "users",
				LatencyBucketMs: 5.0,
				Count:           200,
				FirstSeen:       startTime,
				LastSeen:        endTime,
			},
		},
	}

	service := &EbpfQueryService{db: mockDB}
	ctx := context.Background()

	// Query all metric types.
	req := &agentv1.QueryEbpfMetricsRequest{
		StartTime:    startTime.Unix(),
		EndTime:      endTime.Unix(),
		ServiceNames: []string{"api"},
		MetricTypes: []agentv1.EbpfMetricType{
			agentv1.EbpfMetricType_EBPF_METRIC_TYPE_HTTP,
			agentv1.EbpfMetricType_EBPF_METRIC_TYPE_GRPC,
			agentv1.EbpfMetricType_EBPF_METRIC_TYPE_SQL,
		},
	}

	resp, err := service.QueryMetrics(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Should have metrics for all types.
	assert.Len(t, resp.HttpMetrics, 1)
	assert.Len(t, resp.GrpcMetrics, 1)
	assert.Len(t, resp.SqlMetrics, 1)

	assert.Equal(t, "api", resp.HttpMetrics[0].ServiceName)
	assert.Equal(t, "api", resp.GrpcMetrics[0].ServiceName)
	assert.Equal(t, "api", resp.SqlMetrics[0].ServiceName)
}

// TestQueryMetrics_EmptyResults tests handling of empty results.
func TestQueryMetrics_EmptyResults(t *testing.T) {
	now := time.Now()
	startTime := now.Add(-1 * time.Hour)
	endTime := now

	// Mock database returns no results.
	mockDB := &mockDatabase{
		httpMetrics: []*database.BeylaHTTPMetricResult{},
	}

	service := &EbpfQueryService{db: mockDB}
	ctx := context.Background()

	req := &agentv1.QueryEbpfMetricsRequest{
		StartTime:    startTime.Unix(),
		EndTime:      endTime.Unix(),
		ServiceNames: []string{"nonexistent"},
		MetricTypes:  []agentv1.EbpfMetricType{agentv1.EbpfMetricType_EBPF_METRIC_TYPE_HTTP},
	}

	resp, err := service.QueryMetrics(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Should return empty arrays, not nil.
	assert.NotNil(t, resp.HttpMetrics)
	assert.Len(t, resp.HttpMetrics, 0)
}

// TestQueryMetrics_MaxTracesDefault tests default max traces value.
func TestQueryMetrics_MaxTracesDefault(t *testing.T) {
	now := time.Now()
	startTime := now.Add(-1 * time.Hour)
	endTime := now

	mockDB := &mockDatabase{
		traceResults: []*database.BeylaTraceResult{
			{
				TraceID:     "trace-1",
				SpanID:      "span-1",
				ServiceName: "service",
				SpanName:    "operation",
				SpanKind:    "server",
				StartTime:   startTime,
				DurationUs:  1000,
				StatusCode:  0,
			},
		},
	}

	service := &EbpfQueryService{db: mockDB}
	ctx := context.Background()

	// Don't set MaxTraces - should default to 100.
	req := &agentv1.QueryEbpfMetricsRequest{
		StartTime:     startTime.Unix(),
		EndTime:       endTime.Unix(),
		IncludeTraces: true,
		MaxTraces:     0, // Should use default
	}

	resp, err := service.QueryMetrics(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.TraceSpans, 1)
}

// TestQueryUnifiedSummary tests the unified summary query method.
func TestQueryUnifiedSummary(t *testing.T) {
	now := time.Now()
	startTime := now.Add(-5 * time.Minute)
	endTime := now

	t.Run("successful summary query", func(t *testing.T) {
		mockDB := &mockDatabase{
			httpMetrics: []*database.BeylaHTTPMetricResult{
				{
					ServiceName:     "api-service",
					HTTPMethod:      "GET",
					HTTPRoute:       "/api/users",
					HTTPStatusCode:  200,
					Count:           1000,
					LatencyBucketMs: 23.5,
				},
				{
					ServiceName:     "payment-service",
					HTTPMethod:      "POST",
					HTTPRoute:       "/api/payment",
					HTTPStatusCode:  200,
					Count:           500,
					LatencyBucketMs: 50.0,
				},
			},
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		results, err := service.QueryUnifiedSummary(ctx, "", startTime, endTime)
		require.NoError(t, err)
		assert.NotEmpty(t, results)
		assert.Len(t, results, 2) // Two services
		assert.Equal(t, "api-service", results[0].ServiceName)
		assert.Equal(t, "payment-service", results[1].ServiceName)
	})

	t.Run("filter by service name", func(t *testing.T) {
		mockDB := &mockDatabase{
			httpMetrics: []*database.BeylaHTTPMetricResult{
				{
					ServiceName:     "api-service",
					HTTPMethod:      "GET",
					HTTPRoute:       "/api/users",
					HTTPStatusCode:  200,
					Count:           1000,
					LatencyBucketMs: 23.5,
				},
			},
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		results, err := service.QueryUnifiedSummary(ctx, "api-service", startTime, endTime)
		require.NoError(t, err)
		assert.Len(t, results, 1)
		assert.Equal(t, "api-service", results[0].ServiceName)
	})

	t.Run("database error", func(t *testing.T) {
		mockDB := &mockDatabase{
			queryError: fmt.Errorf("database connection failed"),
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		results, err := service.QueryUnifiedSummary(ctx, "", startTime, endTime)
		assert.Error(t, err)
		assert.Nil(t, results)
		assert.Contains(t, err.Error(), "database connection failed")
	})

	t.Run("empty results", func(t *testing.T) {
		mockDB := &mockDatabase{
			httpMetrics: []*database.BeylaHTTPMetricResult{},
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		results, err := service.QueryUnifiedSummary(ctx, "", startTime, endTime)
		require.NoError(t, err)
		assert.Empty(t, results)
	})
}

// TestQueryUnifiedTraces tests the unified traces query method.
func TestQueryUnifiedTraces(t *testing.T) {
	now := time.Now()
	startTime := now.Add(-1 * time.Hour)
	endTime := now

	t.Run("successful traces query", func(t *testing.T) {
		mockDB := &mockDatabase{
			traceResults: []*database.BeylaTraceResult{
				{
					TraceID:     "trace-123",
					SpanID:      "span-456",
					ServiceName: "api-service",
					SpanName:    "GET /api/users",
					SpanKind:    "server",
					StartTime:   startTime,
					DurationUs:  5000,
					StatusCode:  0,
				},
				{
					TraceID:      "trace-123",
					SpanID:       "span-789",
					ParentSpanID: "span-456",
					ServiceName:  "db-service",
					SpanName:     "SELECT users",
					SpanKind:     "client",
					StartTime:    startTime.Add(100 * time.Microsecond),
					DurationUs:   3000,
					StatusCode:   0,
				},
			},
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		spans, err := service.QueryUnifiedTraces(ctx, "", "api-service", startTime, endTime, 0, 10)
		require.NoError(t, err)
		assert.Len(t, spans, 2)
		assert.Equal(t, "trace-123", spans[0].TraceId)
	})

	t.Run("filter by trace ID", func(t *testing.T) {
		mockDB := &mockDatabase{
			traceResults: []*database.BeylaTraceResult{
				{
					TraceID:     "trace-123",
					SpanID:      "span-456",
					ServiceName: "api-service",
					SpanName:    "GET /api/users",
					SpanKind:    "server",
					StartTime:   startTime,
					DurationUs:  5000,
					StatusCode:  0,
				},
			},
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		spans, err := service.QueryUnifiedTraces(ctx, "trace-123", "", startTime, endTime, 0, 10)
		require.NoError(t, err)
		assert.Len(t, spans, 1)
		assert.Equal(t, "trace-123", spans[0].TraceId)
	})

	t.Run("filter by minimum duration", func(t *testing.T) {
		mockDB := &mockDatabase{
			traceResults: []*database.BeylaTraceResult{
				{
					TraceID:     "trace-slow",
					SpanID:      "span-1",
					ServiceName: "api-service",
					SpanName:    "Slow operation",
					SpanKind:    "server",
					StartTime:   startTime,
					DurationUs:  600000, // 600ms
					StatusCode:  0,
				},
			},
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		// Query with 500ms minimum duration
		spans, err := service.QueryUnifiedTraces(ctx, "", "", startTime, endTime, 500000, 10)
		require.NoError(t, err)
		assert.Len(t, spans, 1)
		assert.Equal(t, int64(600000), spans[0].DurationUs)
	})

	t.Run("empty results", func(t *testing.T) {
		mockDB := &mockDatabase{
			traceResults: []*database.BeylaTraceResult{},
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		spans, err := service.QueryUnifiedTraces(ctx, "", "", startTime, endTime, 0, 10)
		require.NoError(t, err)
		assert.Empty(t, spans)
	})

	t.Run("database error", func(t *testing.T) {
		mockDB := &mockDatabase{
			queryError: fmt.Errorf("database error"),
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		spans, err := service.QueryUnifiedTraces(ctx, "", "", startTime, endTime, 0, 10)
		assert.Error(t, err)
		assert.Nil(t, spans)
	})
}

// TestQueryUnifiedMetrics tests the unified metrics query method.
func TestQueryUnifiedMetrics(t *testing.T) {
	now := time.Now()
	startTime := now.Add(-1 * time.Hour)
	endTime := now

	t.Run("successful metrics query", func(t *testing.T) {
		mockDB := &mockDatabase{
			httpMetrics: []*database.BeylaHTTPMetricResult{
				{
					ServiceName:     "api-service",
					HTTPMethod:      "GET",
					HTTPRoute:       "/api/users",
					HTTPStatusCode:  200,
					Count:           1000,
					LatencyBucketMs: 23.5,
				},
			},
			grpcMetrics: []*database.BeylaGRPCMetricResult{
				{
					ServiceName:     "grpc-service",
					GRPCMethod:      "/users.UserService/GetUser",
					GRPCStatusCode:  0,
					Count:           500,
					LatencyBucketMs: 15.0,
				},
			},
			sqlMetrics: []*database.BeylaSQLMetricResult{
				{
					ServiceName:     "db-service",
					SQLOperation:    "SELECT",
					TableName:       "users",
					Count:           200,
					LatencyBucketMs: 5.0,
				},
			},
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		resp, err := service.QueryUnifiedMetrics(ctx, "", startTime, endTime)
		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Len(t, resp.HttpMetrics, 1)
		assert.Len(t, resp.GrpcMetrics, 1)
		assert.Len(t, resp.SqlMetrics, 1)
	})

	t.Run("filter by service name", func(t *testing.T) {
		mockDB := &mockDatabase{
			httpMetrics: []*database.BeylaHTTPMetricResult{
				{
					ServiceName:     "api-service",
					HTTPMethod:      "GET",
					HTTPRoute:       "/api/users",
					HTTPStatusCode:  200,
					Count:           1000,
					LatencyBucketMs: 23.5,
				},
			},
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		resp, err := service.QueryUnifiedMetrics(ctx, "api-service", startTime, endTime)
		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Len(t, resp.HttpMetrics, 1)
		assert.Equal(t, "api-service", resp.HttpMetrics[0].ServiceName)
	})

	t.Run("empty results", func(t *testing.T) {
		mockDB := &mockDatabase{
			httpMetrics: []*database.BeylaHTTPMetricResult{},
			grpcMetrics: []*database.BeylaGRPCMetricResult{},
			sqlMetrics:  []*database.BeylaSQLMetricResult{},
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		resp, err := service.QueryUnifiedMetrics(ctx, "", startTime, endTime)
		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Empty(t, resp.HttpMetrics)
		assert.Empty(t, resp.GrpcMetrics)
		assert.Empty(t, resp.SqlMetrics)
	})
}

// TestQueryUnifiedLogs tests the unified logs query method.
func TestQueryUnifiedLogs(t *testing.T) {
	now := time.Now()
	startTime := now.Add(-1 * time.Hour)
	endTime := now

	t.Run("returns empty logs (placeholder implementation)", func(t *testing.T) {
		mockDB := &mockDatabase{}
		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		logs, err := service.QueryUnifiedLogs(ctx, "api-service", startTime, endTime, "error", "timeout")
		require.NoError(t, err)
		assert.Empty(t, logs) // Current implementation returns empty array
	})

	t.Run("handles all parameters", func(t *testing.T) {
		mockDB := &mockDatabase{}
		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		logs, err := service.QueryUnifiedLogs(ctx, "", startTime, endTime, "", "")
		require.NoError(t, err)
		assert.Empty(t, logs)
	})
}

// TestQueryUnifiedSummary_DataMerging tests OTLP + eBPF data merging.
func TestQueryUnifiedSummary_DataMerging(t *testing.T) {
	now := time.Now()
	startTime := now.Add(-5 * time.Minute)
	endTime := now

	t.Run("merge eBPF and OTLP data for same service", func(t *testing.T) {
		mockDB := &mockDatabase{
			httpMetrics: []*database.BeylaHTTPMetricResult{
				{
					ServiceName:     "api-service",
					HTTPMethod:      "GET",
					HTTPRoute:       "/api/users",
					HTTPStatusCode:  200,
					Count:           1000,
					LatencyBucketMs: 50.0,
				},
			},
			telemetrySummaries: []database.TelemetrySummary{
				{
					ServiceName: "api-service",
					SpanKind:    "server",
					TotalSpans:  500,
					ErrorCount:  10,
					P50Ms:       45.0,
					P95Ms:       120.0,
					P99Ms:       200.0,
				},
			},
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		results, err := service.QueryUnifiedSummary(ctx, "", startTime, endTime)
		require.NoError(t, err)
		assert.Len(t, results, 1)

		// Check merged data
		result := results[0]
		assert.Equal(t, "api-service", result.ServiceName)
		assert.Equal(t, "eBPF+OTLP", result.Source)       // Both sources merged
		assert.Equal(t, int64(1500), result.RequestCount) // 1000 + 500
		// Error rate should be recalculated with both sources
		assert.InDelta(t, 0.67, result.ErrorRate, 0.1) // 10 errors / 1500 requests
	})

	t.Run("eBPF only data", func(t *testing.T) {
		mockDB := &mockDatabase{
			httpMetrics: []*database.BeylaHTTPMetricResult{
				{
					ServiceName:     "api-service",
					HTTPMethod:      "GET",
					HTTPRoute:       "/api/users",
					HTTPStatusCode:  200,
					Count:           1000,
					LatencyBucketMs: 50.0,
				},
			},
			telemetrySummaries: []database.TelemetrySummary{}, // No OTLP data
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		results, err := service.QueryUnifiedSummary(ctx, "", startTime, endTime)
		require.NoError(t, err)
		assert.Len(t, results, 1)
		assert.Equal(t, "eBPF", results[0].Source)
	})

	t.Run("OTLP only data", func(t *testing.T) {
		mockDB := &mockDatabase{
			httpMetrics: []*database.BeylaHTTPMetricResult{}, // No eBPF data
			telemetrySummaries: []database.TelemetrySummary{
				{
					ServiceName: "otlp-service",
					SpanKind:    "server",
					TotalSpans:  500,
					ErrorCount:  5,
					P50Ms:       45.0,
					P95Ms:       120.0,
					P99Ms:       200.0,
				},
			},
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		results, err := service.QueryUnifiedSummary(ctx, "", startTime, endTime)
		require.NoError(t, err)
		assert.Len(t, results, 1)

		result := results[0]
		assert.Equal(t, "otlp-service", result.ServiceName)
		assert.Equal(t, "OTLP", result.Source)
		assert.Equal(t, int64(500), result.RequestCount)
	})

	t.Run("multiple services with different sources", func(t *testing.T) {
		mockDB := &mockDatabase{
			httpMetrics: []*database.BeylaHTTPMetricResult{
				{
					ServiceName:     "ebpf-service",
					HTTPMethod:      "GET",
					HTTPRoute:       "/api/users",
					HTTPStatusCode:  200,
					Count:           1000,
					LatencyBucketMs: 50.0,
				},
			},
			telemetrySummaries: []database.TelemetrySummary{
				{
					ServiceName: "otlp-service",
					SpanKind:    "server",
					TotalSpans:  500,
					ErrorCount:  5,
					P95Ms:       120.0,
				},
			},
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		results, err := service.QueryUnifiedSummary(ctx, "", startTime, endTime)
		require.NoError(t, err)
		assert.Len(t, results, 2)

		// Verify each service has correct source
		sourceMap := make(map[string]string)
		for _, r := range results {
			sourceMap[r.ServiceName] = r.Source
		}

		assert.Equal(t, "eBPF", sourceMap["ebpf-service"])
		assert.Equal(t, "OTLP", sourceMap["otlp-service"])
	})

	t.Run("status calculation with high error rate", func(t *testing.T) {
		mockDB := &mockDatabase{
			httpMetrics: []*database.BeylaHTTPMetricResult{
				{
					ServiceName:     "failing-service",
					HTTPMethod:      "GET",
					HTTPRoute:       "/api/users",
					HTTPStatusCode:  500, // Server error
					Count:           100,
					LatencyBucketMs: 50.0,
				},
			},
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		results, err := service.QueryUnifiedSummary(ctx, "", startTime, endTime)
		require.NoError(t, err)
		assert.Len(t, results, 1)

		result := results[0]
		assert.Equal(t, ServiceStatusCritical, result.Status) // High error rate should mark as critical
		assert.Equal(t, 100.0, result.ErrorRate)              // All requests are errors
	})
}

// TestQueryUnifiedMetrics_DataMerging tests OTLP + eBPF metrics merging.
func TestQueryUnifiedMetrics_DataMerging(t *testing.T) {
	now := time.Now()
	startTime := now.Add(-1 * time.Hour)
	endTime := now

	t.Run("includes both eBPF and OTLP metrics", func(t *testing.T) {
		mockDB := &mockDatabase{
			httpMetrics: []*database.BeylaHTTPMetricResult{
				{
					ServiceName:     "api-service",
					HTTPMethod:      "GET",
					HTTPRoute:       "/api/users",
					HTTPStatusCode:  200,
					Count:           1000,
					LatencyBucketMs: 50.0,
				},
			},
			telemetrySummaries: []database.TelemetrySummary{
				{
					ServiceName: "api-service",
					SpanKind:    "server",
					TotalSpans:  500,
					ErrorCount:  5,
					P50Ms:       45.0,
					P95Ms:       120.0,
					P99Ms:       200.0,
				},
			},
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		resp, err := service.QueryUnifiedMetrics(ctx, "", startTime, endTime)
		require.NoError(t, err)
		assert.NotNil(t, resp)

		// Should have both eBPF metric and OTLP metric (converted to HTTP metric format)
		assert.GreaterOrEqual(t, len(resp.HttpMetrics), 2)

		// Find OTLP metric (has [OTLP] suffix in service name)
		foundOTLP := false
		for _, m := range resp.HttpMetrics {
			if m.ServiceName == "api-service [OTLP]" {
				foundOTLP = true
				assert.Equal(t, uint64(500), m.RequestCount)
				// Check that latency buckets contain our P50, P95, P99 values
				assert.Contains(t, m.LatencyBuckets, 45.0)
				assert.Contains(t, m.LatencyBuckets, 120.0)
				break
			}
		}
		assert.True(t, foundOTLP, "Should include OTLP metrics with source annotation")
	})

	t.Run("continues without OTLP data if unavailable", func(t *testing.T) {
		mockDB := &mockDatabase{
			httpMetrics: []*database.BeylaHTTPMetricResult{
				{
					ServiceName:     "api-service",
					HTTPMethod:      "GET",
					HTTPRoute:       "/api/users",
					HTTPStatusCode:  200,
					Count:           1000,
					LatencyBucketMs: 50.0,
				},
			},
			queryError: nil, // No error for eBPF
			// telemetrySummaries will return empty
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		resp, err := service.QueryUnifiedMetrics(ctx, "", startTime, endTime)
		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Len(t, resp.HttpMetrics, 1) // Only eBPF metrics
	})
}

// TestQueryUnifiedTraces_SpanMerging tests OTLP + eBPF span merging.
func TestQueryUnifiedTraces_SpanMerging(t *testing.T) {
	now := time.Now()
	startTime := now.Add(-1 * time.Hour)
	endTime := now

	t.Run("includes both eBPF and OTLP spans", func(t *testing.T) {
		mockDB := &mockDatabase{
			traceResults: []*database.BeylaTraceResult{
				{
					TraceID:     "trace-123",
					SpanID:      "span-ebpf-1",
					ServiceName: "api-service",
					SpanName:    "GET /api/users",
					SpanKind:    "server",
					StartTime:   startTime,
					DurationUs:  5000,
					StatusCode:  0,
				},
			},
			telemetrySummaries: []database.TelemetrySummary{
				{
					ServiceName:  "api-service",
					SpanKind:     "server",
					TotalSpans:   100,
					ErrorCount:   5,
					P50Ms:        45.0,
					P95Ms:        120.0,
					P99Ms:        200.0,
					BucketTime:   startTime,
					SampleTraces: []string{"trace-456"},
				},
			},
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		spans, err := service.QueryUnifiedTraces(ctx, "", "", startTime, endTime, 0, 100)
		require.NoError(t, err)

		// Should have both eBPF span and synthetic OTLP span
		assert.GreaterOrEqual(t, len(spans), 2)

		// Find OTLP span (has [OTLP] suffix)
		foundOTLP := false
		for _, span := range spans {
			if span.ServiceName == "api-service [OTLP]" {
				foundOTLP = true
				assert.Equal(t, "OTLP Summary (server)", span.SpanName)
				assert.Equal(t, "OTLP", span.Attributes["source"])
				assert.Equal(t, "100", span.Attributes["total_spans"])
				assert.Equal(t, "5", span.Attributes["error_count"])
				break
			}
		}
		assert.True(t, foundOTLP, "Should include synthetic OTLP span")
	})

	t.Run("filters by service name", func(t *testing.T) {
		mockDB := &mockDatabase{
			traceResults: []*database.BeylaTraceResult{
				{
					TraceID:     "trace-1",
					SpanID:      "span-1",
					ServiceName: "api-service",
					SpanName:    "GET /api/users",
					SpanKind:    "server",
					StartTime:   startTime,
					DurationUs:  5000,
					StatusCode:  0,
				},
			},
			telemetrySummaries: []database.TelemetrySummary{
				{
					ServiceName:  "other-service",
					SpanKind:     "server",
					TotalSpans:   50,
					P95Ms:        100.0,
					BucketTime:   startTime,
					SampleTraces: []string{},
				},
			},
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		// Query only api-service
		spans, err := service.QueryUnifiedTraces(ctx, "", "api-service", startTime, endTime, 0, 100)
		require.NoError(t, err)

		// Should only have api-service spans (eBPF), not other-service (OTLP)
		for _, span := range spans {
			assert.Contains(t, span.ServiceName, "api-service")
		}
	})

	t.Run("filters by trace ID using sample traces", func(t *testing.T) {
		mockDB := &mockDatabase{
			traceResults: []*database.BeylaTraceResult{
				{
					TraceID:     "trace-123",
					SpanID:      "span-1",
					ServiceName: "api-service",
					SpanName:    "GET /api/users",
					SpanKind:    "server",
					StartTime:   startTime,
					DurationUs:  5000,
					StatusCode:  0,
				},
			},
			telemetrySummaries: []database.TelemetrySummary{
				{
					ServiceName:  "api-service",
					SpanKind:     "server",
					TotalSpans:   100,
					P95Ms:        120.0,
					BucketTime:   startTime,
					SampleTraces: []string{"trace-123", "trace-456"},
				},
				{
					ServiceName:  "other-service",
					SpanKind:     "server",
					TotalSpans:   50,
					P95Ms:        100.0,
					BucketTime:   startTime,
					SampleTraces: []string{"trace-999"},
				},
			},
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		// Query specific trace ID
		spans, err := service.QueryUnifiedTraces(ctx, "trace-123", "", startTime, endTime, 0, 100)
		require.NoError(t, err)

		// Should include eBPF span with trace-123 and OTLP summary that has it in samples
		assert.GreaterOrEqual(t, len(spans), 2)

		// Verify OTLP span was included because trace-123 is in sample traces
		foundOTLP := false
		for _, span := range spans {
			if span.ServiceName == "api-service [OTLP]" {
				foundOTLP = true
				assert.Equal(t, "trace-123", span.TraceId) // Should use first sample trace
			}
		}
		assert.True(t, foundOTLP)
	})

	t.Run("filters by minimum duration", func(t *testing.T) {
		mockDB := &mockDatabase{
			traceResults: []*database.BeylaTraceResult{
				{
					TraceID:     "trace-fast",
					SpanID:      "span-1",
					ServiceName: "api-service",
					SpanName:    "Fast operation",
					SpanKind:    "server",
					StartTime:   startTime,
					DurationUs:  1000, // 1ms
					StatusCode:  0,
				},
				{
					TraceID:     "trace-slow",
					SpanID:      "span-2",
					ServiceName: "api-service",
					SpanName:    "Slow operation",
					SpanKind:    "server",
					StartTime:   startTime,
					DurationUs:  600000, // 600ms
					StatusCode:  0,
				},
			},
			telemetrySummaries: []database.TelemetrySummary{
				{
					ServiceName:  "api-service",
					SpanKind:     "server",
					TotalSpans:   100,
					P95Ms:        500.0, // 500ms
					BucketTime:   startTime,
					SampleTraces: []string{},
				},
			},
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		// Query with 100ms minimum duration (100000 microseconds)
		spans, err := service.QueryUnifiedTraces(ctx, "", "", startTime, endTime, 100000, 100)
		require.NoError(t, err)

		// Should only include spans > 100ms (slow eBPF span and OTLP synthetic span)
		assert.GreaterOrEqual(t, len(spans), 2)
		for _, span := range spans {
			assert.GreaterOrEqual(t, span.DurationUs, int64(100000))
		}
	})

	t.Run("limits max traces", func(t *testing.T) {
		mockDB := &mockDatabase{
			traceResults: []*database.BeylaTraceResult{
				{TraceID: "trace-1", SpanID: "span-1", ServiceName: "api", SpanName: "op1", SpanKind: "server", StartTime: startTime, DurationUs: 1000},
				{TraceID: "trace-2", SpanID: "span-2", ServiceName: "api", SpanName: "op2", SpanKind: "server", StartTime: startTime, DurationUs: 2000},
				{TraceID: "trace-3", SpanID: "span-3", ServiceName: "api", SpanName: "op3", SpanKind: "server", StartTime: startTime, DurationUs: 3000},
			},
			telemetrySummaries: []database.TelemetrySummary{
				{ServiceName: "api", SpanKind: "server", TotalSpans: 100, P95Ms: 100.0, BucketTime: startTime},
			},
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		// Limit to 2 spans
		spans, err := service.QueryUnifiedTraces(ctx, "", "", startTime, endTime, 0, 2)
		require.NoError(t, err)
		assert.Len(t, spans, 2)
	})

	t.Run("continues without OTLP if unavailable", func(t *testing.T) {
		mockDB := &mockDatabase{
			traceResults: []*database.BeylaTraceResult{
				{
					TraceID:     "trace-123",
					SpanID:      "span-1",
					ServiceName: "api-service",
					SpanName:    "GET /api/users",
					SpanKind:    "server",
					StartTime:   startTime,
					DurationUs:  5000,
					StatusCode:  0,
				},
			},
			// No telemetry summaries
		}

		service := &EbpfQueryService{db: mockDB}
		ctx := context.Background()

		spans, err := service.QueryUnifiedTraces(ctx, "", "", startTime, endTime, 0, 100)
		require.NoError(t, err)
		assert.Len(t, spans, 1)                              // Only eBPF span
		assert.Equal(t, "api-service", spans[0].ServiceName) // No [OTLP] suffix
	})
}

// TestQueryUnifiedSummary_IdleService verifies that services with only system metrics (no traffic)
// are correctly returned when specifically requested.
func TestQueryUnifiedSummary_IdleService(t *testing.T) {
	// Simulate a scenario where the user queries for the last 15 minutes.
	// But the database stores data in UTC.

	// Use a fixed time for reproducibility.
	// Let's say "Now" is 12:00 PM UTC.
	now := time.Date(2023, 10, 27, 12, 0, 0, 0, time.UTC)

	// The query window is [11:45 AM UTC, 12:00 PM UTC].
	startTime := now.Add(-15 * time.Minute)
	endTime := now

	mockDB := &mockDatabase{
		// Simulate system metrics stored in the DB.
		// These would be returned by the DB query.
		systemMetricsSummaries: []database.SystemMetricsSummary{
			{
				BucketTime: now.Add(-5 * time.Minute), // 11:55 AM UTC
				AgentID:    "agent-1",
				MetricName: "system.cpu.utilization", // metric_name
				AvgValue:   50.0,
			},
		},
		// Simulate OTLP summaries - EMPTY (Idle service simulation)
		telemetrySummaries: []database.TelemetrySummary{},
		// Setup service registry to link agent-1 to my-service
		services: map[string]*database.Service{
			"my-service": {Name: "my-service", AgentID: "agent-1"},
		},
	}

	service := &EbpfQueryService{db: mockDB}
	ctx := context.Background()

	// Execute the query for "my-service".
	// The system logic currently only builds summaries if eBPF or OTLP data exists.
	// If this returns empty, it confirms that idle services with only system metrics are ignored.
	results, err := service.QueryUnifiedSummary(ctx, "my-service", startTime, endTime)
	require.NoError(t, err)

	// Verify we got results.
	assert.NotEmpty(t, results, "Expected results for idle service with system metrics, but got none.")

	if len(results) > 0 {
		assert.Equal(t, "my-service", results[0].ServiceName)
		assert.Equal(t, "agent-1", results[0].AgentID)
		// Check CPU utilization is populated
		assert.Greater(t, results[0].HostCPUUtilizationAvg, 0.0)
	}
}

// TestQueryUnifiedSummary_ProfilingEnrichment tests RFD 074 profiling data enrichment.
func TestQueryUnifiedSummary_ProfilingEnrichment(t *testing.T) {
	now := time.Now()

	t.Run("enriches summary with profiling hotspots", func(t *testing.T) {
		mockDB := &mockDatabase{
			httpMetrics: []*database.BeylaHTTPMetricResult{
				{ServiceName: "order-svc", HTTPMethod: "GET", HTTPRoute: "/orders", HTTPStatusCode: 200, Count: 1000, LatencyBucketMs: 50, LastSeen: now},
			},
			services: map[string]*database.Service{
				"order-svc": {Name: "order-svc", AgentID: "agent-1"},
			},
			profilingResult: &database.ProfilingSummaryResult{
				TotalSamples: 5000,
				Hotspots: []database.ProfilingHotspot{
					{Rank: 1, Frames: []string{"main", "processOrder", "validateSignature"}, Percentage: 40.0, SampleCount: 2000},
					{Rank: 2, Frames: []string{"runtime", "gcBgMarkWorker"}, Percentage: 15.0, SampleCount: 750},
				},
			},
			latestBinaryMetadata: &database.BinaryMetadata{
				BuildID:     "build-abc",
				ServiceName: "order-svc",
				FirstSeen:   now.Add(-2 * time.Hour),
				LastSeen:    now,
			},
		}

		service := &EbpfQueryService{
			db:              mockDB,
			profilingConfig: ProfilingEnrichmentConfig{TopKHotspots: 5},
		}

		results, err := service.QueryUnifiedSummary(context.Background(), "order-svc", now.Add(-5*time.Minute), now)
		require.NoError(t, err)
		require.Len(t, results, 1)

		r := results[0]
		assert.Equal(t, "order-svc", r.ServiceName)

		// Verify profiling summary.
		require.NotNil(t, r.ProfilingSummary)
		assert.Equal(t, uint64(5000), r.ProfilingSummary.TotalSamples)
		require.Len(t, r.ProfilingSummary.Hotspots, 2)
		assert.Equal(t, int32(1), r.ProfilingSummary.Hotspots[0].Rank)
		assert.Equal(t, "validateSignature", r.ProfilingSummary.Hotspots[0].Frames[2])
		assert.InDelta(t, 40.0, r.ProfilingSummary.Hotspots[0].Percentage, 0.01)

		// Verify deployment context.
		require.NotNil(t, r.Deployment)
		assert.Equal(t, "build-abc", r.Deployment.BuildID)
		assert.Contains(t, r.Deployment.Age, "h")
	})

	t.Run("no profiling data returns nil", func(t *testing.T) {
		mockDB := &mockDatabase{
			httpMetrics: []*database.BeylaHTTPMetricResult{
				{ServiceName: "api-svc", HTTPMethod: "GET", HTTPRoute: "/health", HTTPStatusCode: 200, Count: 100, LatencyBucketMs: 10, LastSeen: now},
			},
			services: map[string]*database.Service{
				"api-svc": {Name: "api-svc", AgentID: "agent-1"},
			},
			// profilingResult is nil -> returns empty ProfilingSummaryResult.
		}

		service := &EbpfQueryService{
			db:              mockDB,
			profilingConfig: ProfilingEnrichmentConfig{TopKHotspots: 5},
		}

		results, err := service.QueryUnifiedSummary(context.Background(), "api-svc", now.Add(-5*time.Minute), now)
		require.NoError(t, err)
		require.Len(t, results, 1)

		// No profiling data -> nil summary.
		assert.Nil(t, results[0].ProfilingSummary)
	})

	t.Run("profiling disabled skips enrichment", func(t *testing.T) {
		mockDB := &mockDatabase{
			httpMetrics: []*database.BeylaHTTPMetricResult{
				{ServiceName: "api-svc", HTTPMethod: "GET", HTTPRoute: "/health", HTTPStatusCode: 200, Count: 100, LatencyBucketMs: 10, LastSeen: now},
			},
			services: map[string]*database.Service{
				"api-svc": {Name: "api-svc", AgentID: "agent-1"},
			},
			profilingResult: &database.ProfilingSummaryResult{
				TotalSamples: 1000,
				Hotspots: []database.ProfilingHotspot{
					{Rank: 1, Frames: []string{"main"}, Percentage: 100, SampleCount: 1000},
				},
			},
		}

		service := &EbpfQueryService{
			db:              mockDB,
			profilingConfig: ProfilingEnrichmentConfig{Disabled: true, TopKHotspots: 5},
		}

		results, err := service.QueryUnifiedSummary(context.Background(), "api-svc", now.Add(-5*time.Minute), now)
		require.NoError(t, err)
		require.Len(t, results, 1)

		// Profiling disabled -> no enrichment.
		assert.Nil(t, results[0].ProfilingSummary)
	})

	t.Run("regression detection with previous build", func(t *testing.T) {
		mockDB := &mockDatabase{
			httpMetrics: []*database.BeylaHTTPMetricResult{
				{ServiceName: "order-svc", HTTPMethod: "POST", HTTPRoute: "/orders", HTTPStatusCode: 200, Count: 500, LatencyBucketMs: 100, LastSeen: now},
			},
			services: map[string]*database.Service{
				"order-svc": {Name: "order-svc", AgentID: "agent-1"},
			},
			profilingResult: &database.ProfilingSummaryResult{
				TotalSamples: 3000,
				Hotspots: []database.ProfilingHotspot{
					{Rank: 1, Frames: []string{"main", "newExpensiveFunc"}, Percentage: 55.0, SampleCount: 1650},
				},
			},
			latestBinaryMetadata: &database.BinaryMetadata{
				BuildID:     "build-new",
				ServiceName: "order-svc",
				FirstSeen:   now.Add(-30 * time.Minute),
			},
			prevBinaryMetadata: &database.BinaryMetadata{
				BuildID:     "build-old",
				ServiceName: "order-svc",
				FirstSeen:   now.Add(-24 * time.Hour),
			},
			regressionIndicators: []database.RegressionIndicatorResult{
				{
					Type:               "new_hotspot",
					Message:            "newExpensiveFunc (55.0%) was not in top-5 before this deployment",
					BaselinePercentage: 0,
					CurrentPercentage:  55.0,
					Delta:              55.0,
				},
			},
		}

		service := &EbpfQueryService{
			db:              mockDB,
			profilingConfig: ProfilingEnrichmentConfig{TopKHotspots: 5},
		}

		results, err := service.QueryUnifiedSummary(context.Background(), "order-svc", now.Add(-5*time.Minute), now)
		require.NoError(t, err)
		require.Len(t, results, 1)

		r := results[0]
		require.NotNil(t, r.ProfilingSummary)
		require.NotNil(t, r.Deployment)
		assert.Equal(t, "build-new", r.Deployment.BuildID)

		// Verify regression indicators.
		require.Len(t, r.RegressionIndicators, 1)
		assert.Equal(t, "new_hotspot", r.RegressionIndicators[0].Type)
		assert.Contains(t, r.RegressionIndicators[0].Message, "newExpensiveFunc")
		assert.InDelta(t, 55.0, r.RegressionIndicators[0].CurrentPercentage, 0.01)
	})
}

// TestFormatDurationShort tests the formatDurationShort helper (RFD 074).
func TestFormatDurationShort(t *testing.T) {
	tests := []struct {
		d        time.Duration
		expected string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{1 * time.Hour, "1h"},
		{2*time.Hour + 15*time.Minute, "2h15m"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, formatDurationShort(tt.d))
		})
	}
}
