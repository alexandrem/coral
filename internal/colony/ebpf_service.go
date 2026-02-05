package colony

import (
	"context"
	"fmt"
	"sort"
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
	QuerySystemMetricsSummaries(ctx context.Context, agentID string, startTime, endTime time.Time) ([]database.SystemMetricsSummary, error)
	GetServiceByName(ctx context.Context, serviceName string) (*database.Service, error)
	QueryAllServiceNames(ctx context.Context) ([]string, error)
	// RFD 074: Profiling-enriched summary.
	GetTopKHotspots(ctx context.Context, serviceName string, startTime, endTime time.Time, topK int) (*database.ProfilingSummaryResult, error)
	GetLatestBinaryMetadata(ctx context.Context, serviceName string) (*database.BinaryMetadata, error)
	GetPreviousBinaryMetadata(ctx context.Context, serviceName, currentBuildID string) (*database.BinaryMetadata, error)
	CompareHotspotsWithBaseline(ctx context.Context, serviceName, currentBuildID, baselineBuildID string, startTime, endTime time.Time, topK int) ([]database.RegressionIndicatorResult, error)
	// RFD 077: Memory profiling.
	GetTopKMemoryHotspots(ctx context.Context, serviceName string, startTime, endTime time.Time, topK int) (*database.MemoryProfilingSummaryResult, error)
}

// ProfilingEnrichmentConfig controls profiling enrichment in query summaries (RFD 074).
type ProfilingEnrichmentConfig struct {
	// Disabled controls whether profiling data is excluded from summaries.
	// Zero value (false) means profiling is enabled by default.
	Disabled bool
	// TopKHotspots is the default number of top hotspots. Default: 5, max: 20.
	TopKHotspots int
}

// EbpfQueryService provides eBPF metrics querying with validation.
type EbpfQueryService struct {
	db              ebpfDatabase
	profilingConfig ProfilingEnrichmentConfig
}

// NewEbpfQueryService creates a new eBPF query service.
func NewEbpfQueryService(db *database.Database) *EbpfQueryService {
	return &EbpfQueryService{
		db: db,
		profilingConfig: ProfilingEnrichmentConfig{
			TopKHotspots: 5,
		},
	}
}

// NewEbpfQueryServiceWithConfig creates a new eBPF query service with profiling config (RFD 074).
func NewEbpfQueryServiceWithConfig(db *database.Database, profilingConfig ProfilingEnrichmentConfig) *EbpfQueryService {
	return &EbpfQueryService{
		db:              db,
		profilingConfig: profilingConfig,
	}
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

// ServiceStatus represents the health status of a service.
type ServiceStatus int

const (
	// ServiceStatusHealthy indicates the service is operating normally.
	ServiceStatusHealthy ServiceStatus = iota
	// ServiceStatusDegraded indicates the service has issues but is still operational.
	ServiceStatusDegraded
	// ServiceStatusCritical indicates the service has severe issues.
	ServiceStatusCritical
	// ServiceStatusIdle indicates the service is registered but not receiving traffic.
	ServiceStatusIdle
)

// String returns the string representation of ServiceStatus.
func (s ServiceStatus) String() string {
	switch s {
	case ServiceStatusHealthy:
		return "healthy"
	case ServiceStatusDegraded:
		return "degraded"
	case ServiceStatusCritical:
		return "critical"
	case ServiceStatusIdle:
		return "idle"
	default:
		return "unknown"
	}
}

// UnifiedSummaryResult represents the health summary of a service.
type UnifiedSummaryResult struct {
	ServiceName  string
	Status       ServiceStatus // healthy, degraded, critical, idle
	RequestCount int64         // Total requests/spans
	ErrorRate    float64       // Error rate as percentage
	AvgLatencyMs float64       // Average latency in milliseconds
	Source       string        // eBPF, OTLP, or eBPF+OTLP
	Issues       []string
	// Host resources (RFD 071).
	HostCPUUtilization    float64 // CPU utilization percentage (max in time window)
	HostCPUUtilizationAvg float64 // CPU utilization percentage (average in time window)
	HostMemoryUsageGB     float64 // Memory usage in GB (max in time window)
	HostMemoryLimitGB     float64 // Memory limit in GB
	HostMemoryUtilization float64 // Memory utilization percentage (max in time window)
	AgentID               string  // Agent ID for correlation
	// RFD 074: Profiling-enriched data.
	ProfilingSummary     *ProfilingSummaryData
	Deployment           *DeploymentData
	RegressionIndicators []RegressionIndicatorData
}

// ProfilingSummaryData contains top-K CPU and memory hotspots (RFD 074, RFD 077).
type ProfilingSummaryData struct {
	Hotspots       []HotspotData
	TotalSamples   uint64
	SamplingPeriod string
	BuildID        string

	// Compact representation computed from Hotspots for client consumption.
	// HotPath is the call chain from the hottest stack in caller→callee order.
	HotPath []string
	// SamplesByFunction maps each unique leaf function to its aggregated percentage.
	SamplesByFunction []FunctionSample

	// Memory profiling data (RFD 077).
	MemoryHotspots    []MemoryHotspotData
	TotalAllocBytes   int64
	TotalAllocObjects int64
	MemoryHotPath     []string
	MemoryByFunction  []FunctionMemorySample
}

// HotspotData represents a single CPU hotspot (RFD 074).
type HotspotData struct {
	Rank        int32
	Frames      []string
	Percentage  float64
	SampleCount uint64
}

// FunctionSample pairs a function name with its CPU percentage.
type FunctionSample struct {
	Function   string
	Percentage float64
}

// MemoryHotspotData represents a single memory allocation hotspot (RFD 077).
type MemoryHotspotData struct {
	Rank         int32
	Frames       []string
	Percentage   float64
	AllocBytes   int64
	AllocObjects int64
}

// FunctionMemorySample pairs a function name with its memory allocation data.
type FunctionMemorySample struct {
	Function   string
	Percentage float64
	AllocBytes int64
}

// DeploymentData contains deployment context (RFD 074).
type DeploymentData struct {
	BuildID    string
	DeployedAt time.Time
	Age        string
}

// RegressionIndicatorData contains a regression indicator (RFD 074).
type RegressionIndicatorData struct {
	Type               string
	Message            string
	BaselinePercentage float64
	CurrentPercentage  float64
	Delta              float64
}

// QueryUnifiedSummary provides a high-level health summary for services.
func (s *EbpfQueryService) QueryUnifiedSummary(ctx context.Context, serviceName string, startTime, endTime time.Time) ([]UnifiedSummaryResult, error) {
	summaryMap := make(map[string]*UnifiedSummaryResult)

	// 1. Query eBPF HTTP metrics.
	filters := make(map[string]string)
	httpMetrics, err := s.db.QueryBeylaHTTPMetrics(ctx, serviceName, startTime, endTime, filters)
	if err != nil {
		return nil, fmt.Errorf("failed to query eBPF HTTP metrics: %w", err)
	}

	// Aggregate eBPF metrics by service.
	ebpfRequestCounts := make(map[string]int64)
	ebpfErrorCounts := make(map[string]int64)
	ebpfLatencies := make(map[string][]float64)

	for _, m := range httpMetrics {
		ebpfRequestCounts[m.ServiceName] += m.Count

		// Count errors (5xx status codes).
		if m.HTTPStatusCode >= 500 && m.HTTPStatusCode < 600 {
			ebpfErrorCounts[m.ServiceName] += m.Count
		}

		// Collect latencies for P95 calculation.
		if m.LatencyBucketMs > 0 {
			ebpfLatencies[m.ServiceName] = append(ebpfLatencies[m.ServiceName], m.LatencyBucketMs)
		}
	}

	// Create summaries from eBPF data.
	for svc, reqCount := range ebpfRequestCounts {
		errorCount := ebpfErrorCounts[svc]
		errorRate := float64(0)
		if reqCount > 0 {
			errorRate = float64(errorCount) / float64(reqCount) * 100
		}

		// Calculate average latency (simplified - should be weighted average).
		avgLatency := float64(0)
		if latencies := ebpfLatencies[svc]; len(latencies) > 0 {
			sum := float64(0)
			for _, l := range latencies {
				sum += l
			}
			avgLatency = sum / float64(len(latencies))
		}

		status := ServiceStatusHealthy
		if errorRate > 5.0 {
			status = ServiceStatusCritical
		} else if errorRate > 1.0 || avgLatency > 1000 {
			status = ServiceStatusDegraded
		}

		summaryMap[svc] = &UnifiedSummaryResult{
			ServiceName:  svc,
			Status:       status,
			RequestCount: reqCount,
			ErrorRate:    errorRate,
			AvgLatencyMs: avgLatency,
			Source:       "eBPF",
		}
	}

	// 2. Query OTLP telemetry summaries (query all agents, empty agentID means all).
	telemetrySummaries, err := s.db.QueryTelemetrySummaries(ctx, "", startTime, endTime)
	if err != nil {
		// Don't fail if OTLP data is unavailable, just log and continue.
		// This allows the system to work with only eBPF data.
		return convertSummaryMapToSlice(summaryMap), nil
	}

	// 3. Merge OTLP data with eBPF data.
	for _, otlp := range telemetrySummaries {
		if serviceName != "" && otlp.ServiceName != serviceName {
			continue
		}

		existing, exists := summaryMap[otlp.ServiceName]
		if exists {
			// Service has both eBPF and OTLP data - merge.
			existing.Source = "eBPF+OTLP"

			// Add OTLP metrics.
			otlpReqCount := int64(otlp.TotalSpans)
			otlpErrorCount := int64(otlp.ErrorCount)

			existing.RequestCount += otlpReqCount

			// Recalculate error rate with both sources.
			totalErrors := float64(ebpfErrorCounts[otlp.ServiceName] + otlpErrorCount)
			totalRequests := float64(existing.RequestCount)
			if totalRequests > 0 {
				existing.ErrorRate = totalErrors / totalRequests * 100
			}

			// Average the P95 latencies (simplified merging).
			if otlp.P95Ms > 0 {
				existing.AvgLatencyMs = (existing.AvgLatencyMs + otlp.P95Ms) / 2
			}

			// Re-evaluate status.
			if existing.ErrorRate > 5.0 || existing.AvgLatencyMs > 2000 {
				existing.Status = ServiceStatusCritical
			} else if existing.ErrorRate > 1.0 || existing.AvgLatencyMs > 1000 {
				existing.Status = ServiceStatusDegraded
			}
		} else {
			// Service has only OTLP data.
			errorRate := float64(0)
			if otlp.TotalSpans > 0 {
				errorRate = float64(otlp.ErrorCount) / float64(otlp.TotalSpans) * 100
			}

			status := ServiceStatusHealthy
			if errorRate > 5.0 || otlp.P95Ms > 2000 {
				status = ServiceStatusCritical
			} else if errorRate > 1.0 || otlp.P95Ms > 1000 {
				status = ServiceStatusDegraded
			}

			summaryMap[otlp.ServiceName] = &UnifiedSummaryResult{
				ServiceName:  otlp.ServiceName,
				Status:       status,
				RequestCount: int64(otlp.TotalSpans),
				ErrorRate:    errorRate,
				AvgLatencyMs: otlp.P95Ms,
				Source:       "OTLP",
			}
		}
	}

	// 4. Query system metrics summaries (RFD 071).
	systemMetricsSummaries, err := s.db.QuerySystemMetricsSummaries(ctx, "", startTime, endTime)
	if err != nil {
		// Don't fail if system metrics are unavailable, just continue without them.
		return convertSummaryMapToSlice(summaryMap), nil
	}

	// Group system metrics by agent_id.
	agentMetrics := make(map[string]map[string]database.SystemMetricsSummary)
	for _, metric := range systemMetricsSummaries {
		if _, exists := agentMetrics[metric.AgentID]; !exists {
			agentMetrics[metric.AgentID] = make(map[string]database.SystemMetricsSummary)
		}
		agentMetrics[metric.AgentID][metric.MetricName] = metric
	}

	// Ensure all relevant services are in the summary map.
	// If specific service requested: check just that one.
	// If all services requested: check all known service names (to include idle ones).
	var targetServices []string
	if serviceName != "" {
		targetServices = []string{serviceName}
	} else {
		names, err := s.db.QueryAllServiceNames(ctx)
		if err != nil {
			// Still return what we have so far rather than failing completely.
			// This maintains backward compatibility while logging the error.
			return convertSummaryMapToSlice(summaryMap), nil
		}
		targetServices = names
	}

	for _, name := range targetServices {
		if _, exists := summaryMap[name]; !exists && name != "" {
			svc, err := s.db.GetServiceByName(ctx, name)
			if err != nil {
				// Error querying service registry - continue.
				continue
			} else if svc == nil {
				// Service not found in registry - skip it.
				// With proper service persistence, all registered services should be in the database.
				continue
			} else {
				// Service is registered but has no recent metrics - mark as idle.
				summaryMap[name] = &UnifiedSummaryResult{
					ServiceName: svc.Name,
					AgentID:     svc.AgentID,
					Status:      ServiceStatusIdle,
				}
			}
		}
	}

	// Associate services with agents using database as primary source, OTLP as fallback.
	// Build OTLP agent mapping for fallback.
	otlpAgentMap := make(map[string]string)
	for _, otlp := range telemetrySummaries {
		if otlp.AgentID != "" {
			otlpAgentMap[otlp.ServiceName] = otlp.AgentID
		}
	}

	// Populate agent IDs and system metrics for all services.
	for _, summary := range summaryMap {
		if summary.AgentID == "" {
			// Primary: look up agent ID from service registry (most reliable).
			svc, err := s.db.GetServiceByName(ctx, summary.ServiceName)
			if err == nil && svc != nil && svc.AgentID != "" {
				summary.AgentID = svc.AgentID
			} else if otlpAgentID, found := otlpAgentMap[summary.ServiceName]; found {
				// Fallback: use OTLP agent ID if database doesn't have it.
				summary.AgentID = otlpAgentID
			}
		}

		// If we have an agent ID, attach system metrics.
		if summary.AgentID != "" {
			if metrics, hasMetrics := agentMetrics[summary.AgentID]; hasMetrics {
				// CPU utilization.
				if cpuUtil, found := metrics["system.cpu.utilization"]; found {
					summary.HostCPUUtilization = cpuUtil.MaxValue
					summary.HostCPUUtilizationAvg = cpuUtil.AvgValue

					// Add warning if CPU is high.
					if cpuUtil.MaxValue > 80 {
						summary.Issues = append(summary.Issues,
							fmt.Sprintf("⚠️  High CPU: %.0f%% (threshold: 80%%)", cpuUtil.MaxValue))
						// Upgrade status to degraded if currently healthy or idle.
						if summary.Status == ServiceStatusHealthy || summary.Status == ServiceStatusIdle {
							summary.Status = ServiceStatusDegraded
						}
					}
				}

				// Memory usage and utilization.
				if memUsage, found := metrics["system.memory.usage"]; found {
					summary.HostMemoryUsageGB = memUsage.MaxValue / 1e9 // Convert bytes to GB.
				}
				if memLimit, found := metrics["system.memory.limit"]; found {
					summary.HostMemoryLimitGB = memLimit.AvgValue / 1e9 // Convert bytes to GB.
				}
				if memUtil, found := metrics["system.memory.utilization"]; found {
					summary.HostMemoryUtilization = memUtil.MaxValue

					// Add warning if memory is high.
					if memUtil.MaxValue > 85 {
						summary.Issues = append(summary.Issues,
							fmt.Sprintf("⚠️  High Memory: %.1fGB/%.1fGB (%.0f%%, threshold: 85%%)",
								summary.HostMemoryUsageGB,
								summary.HostMemoryLimitGB,
								memUtil.MaxValue))
						// Upgrade status to degraded if currently healthy or idle.
						if summary.Status == ServiceStatusHealthy || summary.Status == ServiceStatusIdle {
							summary.Status = ServiceStatusDegraded
						}
					}
				}
			}
		}
	}

	// 5. Enrich with CPU profiling data (RFD 074).
	if s.profilingConfig.Disabled {
		return convertSummaryMapToSlice(summaryMap), nil
	}

	topK := s.profilingConfig.TopKHotspots
	if topK <= 0 {
		topK = 5
	}

	for _, summary := range summaryMap {
		if summary.ServiceName == "" {
			continue
		}

		profilingResult, err := s.db.GetTopKHotspots(ctx, summary.ServiceName, startTime, endTime, topK)
		if err != nil || profilingResult == nil || profilingResult.TotalSamples < database.MinSamplesForSummary {
			continue
		}

		hotspots := make([]HotspotData, len(profilingResult.Hotspots))
		for i, h := range profilingResult.Hotspots {
			hotspots[i] = HotspotData{
				Rank:        h.Rank,
				Frames:      database.CleanFrames(h.Frames),
				Percentage:  h.Percentage,
				SampleCount: h.SampleCount,
			}
		}

		ps := &ProfilingSummaryData{
			Hotspots:       hotspots,
			TotalSamples:   profilingResult.TotalSamples,
			SamplingPeriod: formatDurationShort(endTime.Sub(startTime)),
		}

		// Compute compact representation from hotspots.
		if len(hotspots) > 0 {
			// Hot path: reverse the hottest stack to caller→callee order.
			frames := hotspots[0].Frames
			reversed := make([]string, len(frames))
			for i, f := range frames {
				reversed[len(frames)-1-i] = f
			}
			ps.HotPath = reversed

			// Samples by function: deduplicated leaf function → percentage.
			seen := make(map[string]float64)
			var order []string
			for _, h := range hotspots {
				if len(h.Frames) == 0 {
					continue
				}
				name := database.ShortFunctionName(h.Frames[0])
				if _, ok := seen[name]; !ok {
					order = append(order, name)
				}
				seen[name] += h.Percentage
			}
			for _, name := range order {
				ps.SamplesByFunction = append(ps.SamplesByFunction, FunctionSample{
					Function:   name,
					Percentage: seen[name],
				})
			}
		}

		summary.ProfilingSummary = ps

		// 6. Enrich with memory profiling data (RFD 077).
		memProfilingResult, err := s.db.GetTopKMemoryHotspots(ctx, summary.ServiceName, startTime, endTime, topK)
		if err == nil && memProfilingResult != nil && memProfilingResult.TotalAllocBytes >= database.MinAllocBytesForSummary {
			memHotspots := make([]MemoryHotspotData, len(memProfilingResult.Hotspots))
			for i, h := range memProfilingResult.Hotspots {
				memHotspots[i] = MemoryHotspotData{
					Rank:         h.Rank,
					Frames:       database.CleanFrames(h.Frames),
					Percentage:   h.Percentage,
					AllocBytes:   h.AllocBytes,
					AllocObjects: h.AllocObjects,
				}
			}

			ps.MemoryHotspots = memHotspots
			ps.TotalAllocBytes = memProfilingResult.TotalAllocBytes
			ps.TotalAllocObjects = memProfilingResult.TotalAllocObjs

			// Compute memory hot path from hotspots.
			if len(memHotspots) > 0 {
				// Hot path: reverse the hottest stack to caller→callee order.
				frames := memHotspots[0].Frames
				reversed := make([]string, len(frames))
				for i, f := range frames {
					reversed[len(frames)-1-i] = f
				}
				ps.MemoryHotPath = reversed

				// Allocations by function: deduplicated leaf function → percentage and bytes.
				type memEntry struct {
					percentage float64
					bytes      int64
				}
				seen := make(map[string]*memEntry)
				var order []string
				for _, h := range memHotspots {
					if len(h.Frames) == 0 {
						continue
					}
					name := database.ShortFunctionName(h.Frames[0])
					if _, ok := seen[name]; !ok {
						order = append(order, name)
						seen[name] = &memEntry{}
					}
					seen[name].percentage += h.Percentage
					seen[name].bytes += h.AllocBytes
				}
				for _, name := range order {
					ps.MemoryByFunction = append(ps.MemoryByFunction, FunctionMemorySample{
						Function:   name,
						Percentage: seen[name].percentage,
						AllocBytes: seen[name].bytes,
					})
				}
			}
		}

		// Deployment context.
		latestBuild, err := s.db.GetLatestBinaryMetadata(ctx, summary.ServiceName)
		if err == nil && latestBuild != nil {
			age := time.Since(latestBuild.FirstSeen)
			summary.Deployment = &DeploymentData{
				BuildID:    latestBuild.BuildID,
				DeployedAt: latestBuild.FirstSeen,
				Age:        formatDurationShort(age),
			}
			summary.ProfilingSummary.BuildID = latestBuild.BuildID

			// Regression detection.
			prevBuild, err := s.db.GetPreviousBinaryMetadata(ctx, summary.ServiceName, latestBuild.BuildID)
			if err == nil && prevBuild != nil {
				indicators, err := s.db.CompareHotspotsWithBaseline(ctx, summary.ServiceName, latestBuild.BuildID, prevBuild.BuildID, startTime, endTime, topK)
				if err == nil {
					for _, ind := range indicators {
						summary.RegressionIndicators = append(summary.RegressionIndicators, RegressionIndicatorData{
							Type:               ind.Type,
							Message:            ind.Message,
							BaselinePercentage: ind.BaselinePercentage,
							CurrentPercentage:  ind.CurrentPercentage,
							Delta:              ind.Delta,
						})
					}
				}
			}
		}
	}

	return convertSummaryMapToSlice(summaryMap), nil
}

// formatDurationShort formats a duration in short human-readable form.
func formatDurationShort(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	if minutes > 0 {
		return fmt.Sprintf("%dh%dm", hours, minutes)
	}
	return fmt.Sprintf("%dh", hours)
}

// convertSummaryMapToSlice converts a map of summaries to a slice.
// Results are sorted by service name for deterministic ordering.
func convertSummaryMapToSlice(summaryMap map[string]*UnifiedSummaryResult) []UnifiedSummaryResult {
	results := make([]UnifiedSummaryResult, 0, len(summaryMap))
	for _, r := range summaryMap {
		results = append(results, *r)
	}

	// Sort by service name for deterministic ordering.
	sort.Slice(results, func(i, j int) bool {
		return results[i].ServiceName < results[j].ServiceName
	})

	return results
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
		return nil, fmt.Errorf("failed to query eBPF traces: %w", err)
	}

	// 2. Query OTLP telemetry summaries.
	// Note: OTLP spans are stored as aggregated summaries, not individual spans.
	// We create synthetic spans from summaries to provide OTLP visibility.
	telemetrySummaries, err := s.db.QueryTelemetrySummaries(ctx, "", startTime, endTime)
	if err != nil {
		// If OTLP data unavailable, return eBPF spans only.
		return ebpfSpans, nil
	}

	// 3. Convert OTLP summaries to synthetic spans.
	otlpSpans := make([]*agentv1.EbpfTraceSpan, 0)
	for _, summary := range telemetrySummaries {
		// Filter by service name if specified
		if serviceName != "" && summary.ServiceName != serviceName {
			continue
		}

		// Filter by trace ID if specified (check sample traces)
		if traceID != "" {
			found := false
			for _, sampleTraceID := range summary.SampleTraces {
				if sampleTraceID == traceID {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Create a synthetic span representing the OTLP summary
		// Use the first sample trace ID if available, otherwise generate one
		spanTraceID := "otlp-aggregate"
		if len(summary.SampleTraces) > 0 {
			spanTraceID = summary.SampleTraces[0]
		}

		syntheticSpan := &agentv1.EbpfTraceSpan{
			TraceId:     spanTraceID,
			SpanId:      fmt.Sprintf("otlp-%s-%d", summary.ServiceName, summary.BucketTime.Unix()),
			ServiceName: summary.ServiceName + " [OTLP]", // Source annotation
			SpanName:    fmt.Sprintf("OTLP Summary (%s)", summary.SpanKind),
			SpanKind:    summary.SpanKind,
			StartTime:   summary.BucketTime.UnixMilli(), // Unix milliseconds
			// Use P95 latency as duration estimate (converted to microseconds)
			DurationUs: int64(summary.P95Ms * 1000),
			StatusCode: 0,
			Attributes: map[string]string{
				"source":      "OTLP",
				"total_spans": fmt.Sprintf("%d", summary.TotalSpans),
				"error_count": fmt.Sprintf("%d", summary.ErrorCount),
				"p50_ms":      fmt.Sprintf("%.2f", summary.P50Ms),
				"p95_ms":      fmt.Sprintf("%.2f", summary.P95Ms),
				"p99_ms":      fmt.Sprintf("%.2f", summary.P99Ms),
			},
		}

		otlpSpans = append(otlpSpans, syntheticSpan)
	}

	// 4. Merge eBPF and OTLP spans.
	// Note: We don't deduplicate because OTLP summaries and eBPF spans
	// represent different granularities of data.
	mergedSpans := make([]*agentv1.EbpfTraceSpan, 0, len(ebpfSpans)+len(otlpSpans))
	mergedSpans = append(mergedSpans, ebpfSpans...)
	mergedSpans = append(mergedSpans, otlpSpans...)

	// 5. Apply filters.
	filteredSpans := make([]*agentv1.EbpfTraceSpan, 0)
	for _, span := range mergedSpans {
		// Filter by minimum duration if specified
		if minDurationUs > 0 && span.DurationUs < minDurationUs {
			continue
		}
		filteredSpans = append(filteredSpans, span)
	}

	// 6. Limit results.
	if maxTraces > 0 && len(filteredSpans) > maxTraces {
		filteredSpans = filteredSpans[:maxTraces]
	}

	return filteredSpans, nil
}

// QueryUnifiedMetrics queries metrics from both eBPF and OTLP sources.
func (s *EbpfQueryService) QueryUnifiedMetrics(ctx context.Context, serviceName string, startTime, endTime time.Time) (*agentv1.QueryEbpfMetricsResponse, error) {
	// 1. Query eBPF metrics.
	ebpfMetrics, err := s.QueryMetrics(ctx, &agentv1.QueryEbpfMetricsRequest{
		ServiceNames: []string{serviceName},
		StartTime:    startTime.Unix(),
		EndTime:      endTime.Unix(),
		MetricTypes: []agentv1.EbpfMetricType{
			agentv1.EbpfMetricType_EBPF_METRIC_TYPE_HTTP,
			agentv1.EbpfMetricType_EBPF_METRIC_TYPE_GRPC,
			agentv1.EbpfMetricType_EBPF_METRIC_TYPE_SQL,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query eBPF metrics: %w", err)
	}

	// 2. Query OTLP telemetry summaries.
	telemetrySummaries, err := s.db.QueryTelemetrySummaries(ctx, "", startTime, endTime)
	if err != nil {
		// If OTLP data unavailable, return eBPF metrics only.
		return ebpfMetrics, nil
	}

	// 3. Convert OTLP summaries to HTTP metrics format for unified response.
	// Note: This is a simplified conversion. In a real implementation, we would
	// store OTLP metrics in a more structured format.
	for _, otlp := range telemetrySummaries {
		if serviceName != "" && otlp.ServiceName != serviceName {
			continue
		}

		// Convert OTLP summary to HTTP metric format.
		// Note: This is a simplified conversion - we're using the available fields.
		otlpMetric := &agentv1.EbpfHttpMetric{
			ServiceName:    otlp.ServiceName + " [OTLP]", // Add source annotation
			HttpRoute:      "aggregated",
			HttpMethod:     otlp.SpanKind,
			HttpStatusCode: 200,
			RequestCount:   uint64(otlp.TotalSpans),
			// Store percentiles in latency buckets (simplified)
			LatencyBuckets: []float64{otlp.P50Ms, otlp.P95Ms, otlp.P99Ms},
			LatencyCounts:  []uint64{uint64(otlp.TotalSpans), uint64(otlp.TotalSpans), uint64(otlp.TotalSpans)},
		}

		ebpfMetrics.HttpMetrics = append(ebpfMetrics.HttpMetrics, otlpMetric)
	}

	return ebpfMetrics, nil
}

// QueryUnifiedLogs queries logs from OTLP sources.
func (s *EbpfQueryService) QueryUnifiedLogs(ctx context.Context, serviceName string, startTime, endTime time.Time, level string, search string) ([]string, error) {
	// Placeholder for log querying.
	return []string{}, nil
}
