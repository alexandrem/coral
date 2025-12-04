package colony

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
)

// mockDatabase implements a mock database for testing.
type mockDatabase struct {
	httpMetrics  []*database.BeylaHTTPMetricResult
	grpcMetrics  []*database.BeylaGRPCMetricResult
	sqlMetrics   []*database.BeylaSQLMetricResult
	traceResults []*database.BeylaTraceResult
	queryError   error
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
