package colony

import (
	"context"
	"fmt"
	"time"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
)

// ebpfDatabase defines the interface for database operations needed by the service.
type ebpfDatabase interface {
	QueryBeylaHTTPMetrics(ctx context.Context, serviceName string, startTime, endTime time.Time, filters map[string]string) ([]*database.BeylaHTTPMetricResult, error)
	QueryBeylaGRPCMetrics(ctx context.Context, serviceName string, startTime, endTime time.Time, filters map[string]string) ([]*database.BeylaGRPCMetricResult, error)
	QueryBeylaSQLMetrics(ctx context.Context, serviceName string, startTime, endTime time.Time, filters map[string]string) ([]*database.BeylaSQLMetricResult, error)
	QueryBeylaTraces(ctx context.Context, traceID, serviceName string, startTime, endTime time.Time, minDurationUs int64, maxTraces int) ([]*database.BeylaTraceResult, error)
	QueryTelemetrySummaries(ctx context.Context, agentID string, startTime, endTime time.Time) ([]database.TelemetrySummary, error)
}

// EbpfQueryService provides eBPF metrics querying with validation.
type EbpfQueryService struct {
	db ebpfDatabase
}

// NewEbpfQueryService creates a new eBPF query service.
func NewEbpfQueryService(db *database.Database) *EbpfQueryService {
	return &EbpfQueryService{db: db}
}

// QueryMetrics queries eBPF metrics based on the request.
func (s *EbpfQueryService) QueryMetrics(ctx context.Context, req *agentv1.QueryEbpfMetricsRequest) (*agentv1.QueryEbpfMetricsResponse, error) {
	// Validate time range.
	if req.StartTime <= 0 || req.EndTime <= 0 {
		return nil, fmt.Errorf("start_time and end_time are required")
	}
	if req.StartTime >= req.EndTime {
		return nil, fmt.Errorf("start_time must be before end_time")
	}

	startTime := time.Unix(req.StartTime, 0)
	endTime := time.Unix(req.EndTime, 0)

	// Validate time range is reasonable (not too far in past, not in future).
	now := time.Now()
	if endTime.After(now.Add(time.Hour)) {
		return nil, fmt.Errorf("end_time cannot be more than 1 hour in the future")
	}
	if startTime.Before(now.Add(-30 * 24 * time.Hour)) {
		return nil, fmt.Errorf("start_time cannot be more than 30 days in the past")
	}

	resp := &agentv1.QueryEbpfMetricsResponse{}

	// Query each requested metric type.
	for _, metricType := range req.MetricTypes {
		switch metricType {
		case agentv1.EbpfMetricType_EBPF_METRIC_TYPE_HTTP:
			httpMetrics, err := s.queryHTTPMetrics(ctx, req, startTime, endTime)
			if err != nil {
				return nil, fmt.Errorf("failed to query HTTP metrics: %w", err)
			}
			resp.HttpMetrics = httpMetrics

		case agentv1.EbpfMetricType_EBPF_METRIC_TYPE_GRPC:
			grpcMetrics, err := s.queryGRPCMetrics(ctx, req, startTime, endTime)
			if err != nil {
				return nil, fmt.Errorf("failed to query gRPC metrics: %w", err)
			}
			resp.GrpcMetrics = grpcMetrics

		case agentv1.EbpfMetricType_EBPF_METRIC_TYPE_SQL:
			sqlMetrics, err := s.querySQLMetrics(ctx, req, startTime, endTime)
			if err != nil {
				return nil, fmt.Errorf("failed to query SQL metrics: %w", err)
			}
			resp.SqlMetrics = sqlMetrics
		}
	}

	// Query traces if requested.
	if req.IncludeTraces {
		traceSpans, err := s.queryTraceSpans(ctx, req, startTime, endTime)
		if err != nil {
			return nil, fmt.Errorf("failed to query trace spans: %w", err)
		}
		resp.TraceSpans = traceSpans
	}

	return resp, nil
}

func (s *EbpfQueryService) queryHTTPMetrics(ctx context.Context, req *agentv1.QueryEbpfMetricsRequest, startTime, endTime time.Time) ([]*agentv1.EbpfHttpMetric, error) {
	// If no service names specified, query all services.
	serviceNames := req.ServiceNames
	if len(serviceNames) == 0 {
		serviceNames = []string{""} // Empty string queries all services
	}

	// Map to aggregate metrics by service+method+route+status.
	type metricKey struct {
		serviceName string
		method      string
		route       string
		statusCode  int
	}
	aggregated := make(map[metricKey]*agentv1.EbpfHttpMetric)

	for _, serviceName := range serviceNames {
		filters := make(map[string]string)
		results, err := s.db.QueryBeylaHTTPMetrics(ctx, serviceName, startTime, endTime, filters)
		if err != nil {
			return nil, err
		}

		// Aggregate bucket data into histograms.
		for _, r := range results {
			key := metricKey{
				serviceName: r.ServiceName,
				method:      r.HTTPMethod,
				route:       r.HTTPRoute,
				statusCode:  r.HTTPStatusCode,
			}

			metric, exists := aggregated[key]
			if !exists {
				metric = &agentv1.EbpfHttpMetric{
					Timestamp:      r.LastSeen.UnixMilli(),
					ServiceName:    r.ServiceName,
					HttpMethod:     r.HTTPMethod,
					HttpRoute:      r.HTTPRoute,
					HttpStatusCode: uint32(r.HTTPStatusCode),
					LatencyBuckets: []float64{},
					LatencyCounts:  []uint64{},
					RequestCount:   0,
				}
				aggregated[key] = metric
			}

			// Add bucket and count.
			metric.LatencyBuckets = append(metric.LatencyBuckets, r.LatencyBucketMs)
			metric.LatencyCounts = append(metric.LatencyCounts, uint64(r.Count))
			metric.RequestCount += uint64(r.Count)
		}
	}

	// Convert map to slice.
	allMetrics := make([]*agentv1.EbpfHttpMetric, 0, len(aggregated))
	for _, metric := range aggregated {
		allMetrics = append(allMetrics, metric)
	}

	return allMetrics, nil
}

func (s *EbpfQueryService) queryGRPCMetrics(ctx context.Context, req *agentv1.QueryEbpfMetricsRequest, startTime, endTime time.Time) ([]*agentv1.EbpfGrpcMetric, error) {
	serviceNames := req.ServiceNames
	if len(serviceNames) == 0 {
		serviceNames = []string{""}
	}

	// Map to aggregate metrics by service+method+status.
	type metricKey struct {
		serviceName string
		method      string
		statusCode  int
	}
	aggregated := make(map[metricKey]*agentv1.EbpfGrpcMetric)

	for _, serviceName := range serviceNames {
		filters := make(map[string]string)
		results, err := s.db.QueryBeylaGRPCMetrics(ctx, serviceName, startTime, endTime, filters)
		if err != nil {
			return nil, err
		}

		// Aggregate bucket data into histograms.
		for _, r := range results {
			key := metricKey{
				serviceName: r.ServiceName,
				method:      r.GRPCMethod,
				statusCode:  r.GRPCStatusCode,
			}

			metric, exists := aggregated[key]
			if !exists {
				metric = &agentv1.EbpfGrpcMetric{
					Timestamp:      r.LastSeen.UnixMilli(),
					ServiceName:    r.ServiceName,
					GrpcMethod:     r.GRPCMethod,
					GrpcStatusCode: uint32(r.GRPCStatusCode),
					LatencyBuckets: []float64{},
					LatencyCounts:  []uint64{},
					RequestCount:   0,
				}
				aggregated[key] = metric
			}

			// Add bucket and count.
			metric.LatencyBuckets = append(metric.LatencyBuckets, r.LatencyBucketMs)
			metric.LatencyCounts = append(metric.LatencyCounts, uint64(r.Count))
			metric.RequestCount += uint64(r.Count)
		}
	}

	// Convert map to slice.
	allMetrics := make([]*agentv1.EbpfGrpcMetric, 0, len(aggregated))
	for _, metric := range aggregated {
		allMetrics = append(allMetrics, metric)
	}

	return allMetrics, nil
}

func (s *EbpfQueryService) querySQLMetrics(ctx context.Context, req *agentv1.QueryEbpfMetricsRequest, startTime, endTime time.Time) ([]*agentv1.EbpfSqlMetric, error) {
	serviceNames := req.ServiceNames
	if len(serviceNames) == 0 {
		serviceNames = []string{""}
	}

	// Map to aggregate metrics by service+operation+table.
	type metricKey struct {
		serviceName string
		operation   string
		tableName   string
	}
	aggregated := make(map[metricKey]*agentv1.EbpfSqlMetric)

	for _, serviceName := range serviceNames {
		filters := make(map[string]string)
		results, err := s.db.QueryBeylaSQLMetrics(ctx, serviceName, startTime, endTime, filters)
		if err != nil {
			return nil, err
		}

		// Aggregate bucket data into histograms.
		for _, r := range results {
			key := metricKey{
				serviceName: r.ServiceName,
				operation:   r.SQLOperation,
				tableName:   r.TableName,
			}

			metric, exists := aggregated[key]
			if !exists {
				metric = &agentv1.EbpfSqlMetric{
					Timestamp:      r.LastSeen.UnixMilli(),
					ServiceName:    r.ServiceName,
					SqlOperation:   r.SQLOperation,
					TableName:      r.TableName,
					LatencyBuckets: []float64{},
					LatencyCounts:  []uint64{},
					QueryCount:     0,
				}
				aggregated[key] = metric
			}

			// Add bucket and count.
			metric.LatencyBuckets = append(metric.LatencyBuckets, r.LatencyBucketMs)
			metric.LatencyCounts = append(metric.LatencyCounts, uint64(r.Count))
			metric.QueryCount += uint64(r.Count)
		}
	}

	// Convert map to slice.
	allMetrics := make([]*agentv1.EbpfSqlMetric, 0, len(aggregated))
	for _, metric := range aggregated {
		allMetrics = append(allMetrics, metric)
	}

	return allMetrics, nil
}

func (s *EbpfQueryService) queryTraceSpans(ctx context.Context, req *agentv1.QueryEbpfMetricsRequest, startTime, endTime time.Time) ([]*agentv1.EbpfTraceSpan, error) {
	var allSpans []*agentv1.EbpfTraceSpan

	// If trace ID is specified, query by trace ID only (ignore service filter for efficiency).
	if req.TraceId != "" {
		maxTraces := int(req.MaxTraces)
		if maxTraces == 0 {
			maxTraces = 100
		}

		results, err := s.db.QueryBeylaTraces(ctx, req.TraceId, "", startTime, endTime, 0, maxTraces)
		if err != nil {
			return nil, err
		}

		for _, r := range results {
			allSpans = append(allSpans, &agentv1.EbpfTraceSpan{
				TraceId:      r.TraceID,
				SpanId:       r.SpanID,
				ParentSpanId: r.ParentSpanID,
				ServiceName:  r.ServiceName,
				SpanName:     r.SpanName,
				SpanKind:     r.SpanKind,
				StartTime:    r.StartTime.UnixMilli(),
				DurationUs:   r.DurationUs,
				StatusCode:   uint32(r.StatusCode),
			})
		}

		return allSpans, nil
	}

	// Otherwise, query by service names.
	serviceNames := req.ServiceNames
	if len(serviceNames) == 0 {
		serviceNames = []string{""}
	}

	for _, serviceName := range serviceNames {
		// Use max traces from request, default to 100.
		maxTraces := int(req.MaxTraces)
		if maxTraces == 0 {
			maxTraces = 100
		}

		results, err := s.db.QueryBeylaTraces(ctx, "", serviceName, startTime, endTime, 0, maxTraces)
		if err != nil {
			return nil, err
		}

		for _, r := range results {
			allSpans = append(allSpans, &agentv1.EbpfTraceSpan{
				TraceId:      r.TraceID,
				SpanId:       r.SpanID,
				ParentSpanId: r.ParentSpanID,
				ServiceName:  r.ServiceName,
				SpanName:     r.SpanName,
				SpanKind:     r.SpanKind,
				StartTime:    r.StartTime.UnixMilli(),
				DurationUs:   r.DurationUs,
				StatusCode:   uint32(r.StatusCode),
			})
		}
	}

	return allSpans, nil
}

// Unified Query Methods (RFD 067)

// UnifiedSummaryResult represents the health summary of a service.
type UnifiedSummaryResult struct {
	ServiceName string
	Status      string // healthy, degraded, critical
	RequestRate float64
	ErrorRate   float64
	P95Latency  float64
	Issues      []string
}

// QueryUnifiedSummary provides a high-level health summary for services.
func (s *EbpfQueryService) QueryUnifiedSummary(ctx context.Context, serviceName string, startTime, endTime time.Time) ([]UnifiedSummaryResult, error) {
	// TODO: Implement full logic. For now, return a placeholder based on eBPF metrics.
	// This will be expanded to include OTLP data and anomaly detection.

	filters := make(map[string]string)
	httpMetrics, err := s.db.QueryBeylaHTTPMetrics(ctx, serviceName, startTime, endTime, filters)
	if err != nil {
		return nil, err
	}

	// Simple aggregation for demonstration.
	summaryMap := make(map[string]*UnifiedSummaryResult)

	for _, m := range httpMetrics {
		if _, exists := summaryMap[m.ServiceName]; !exists {
			summaryMap[m.ServiceName] = &UnifiedSummaryResult{
				ServiceName: m.ServiceName,
				Status:      "healthy",
			}
		}
		// In a real implementation, we would aggregate rates and latencies here.
	}

	var results []UnifiedSummaryResult
	for _, r := range summaryMap {
		results = append(results, *r)
	}

	return results, nil
}

// QueryUnifiedTraces queries traces from both eBPF and OTLP sources.
func (s *EbpfQueryService) QueryUnifiedTraces(ctx context.Context, traceID, serviceName string, startTime, endTime time.Time, minDurationUs int64, maxTraces int) ([]*agentv1.EbpfTraceSpan, error) {
	// 1. Query eBPF traces.
	ebpfSpans, err := s.queryTraceSpans(ctx, &agentv1.QueryEbpfMetricsRequest{
		TraceId:      traceID,
		ServiceNames: []string{serviceName},
		MaxTraces:    int32(maxTraces),
	}, startTime, endTime)
	if err != nil {
		return nil, err
	}

	// 2. Query OTLP traces (placeholder for now, as we only have summaries).
	// In the future, this will query the OTLP span store.

	// 3. Merge results.
	// For now, just return eBPF spans.
	return ebpfSpans, nil
}

// QueryUnifiedMetrics queries metrics from both eBPF and OTLP sources.
func (s *EbpfQueryService) QueryUnifiedMetrics(ctx context.Context, serviceName string, startTime, endTime time.Time) (*agentv1.QueryEbpfMetricsResponse, error) {
	// Reuse existing logic for eBPF metrics.
	return s.QueryMetrics(ctx, &agentv1.QueryEbpfMetricsRequest{
		ServiceNames: []string{serviceName},
		StartTime:    startTime.Unix(),
		EndTime:      endTime.Unix(),
		MetricTypes: []agentv1.EbpfMetricType{
			agentv1.EbpfMetricType_EBPF_METRIC_TYPE_HTTP,
			agentv1.EbpfMetricType_EBPF_METRIC_TYPE_GRPC,
			agentv1.EbpfMetricType_EBPF_METRIC_TYPE_SQL,
		},
	})
}

// QueryUnifiedLogs queries logs from OTLP sources.
func (s *EbpfQueryService) QueryUnifiedLogs(ctx context.Context, serviceName string, startTime, endTime time.Time, level string, search string) ([]string, error) {
	// Placeholder for log querying.
	return []string{}, nil
}
