package colony

import (
	"context"
	"fmt"
	"sort"
	"time"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
)

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

// QueryMetrics queries eBPF metrics from the colony's summary database for a given time range.
func (s *EbpfQueryService) QueryMetrics(ctx context.Context, startTime, endTime time.Time, req *agentv1.QueryEbpfMetricsRequest) (*agentv1.QueryEbpfMetricsResponse, error) {
	// Validate time range.
	if startTime.IsZero() || endTime.IsZero() {
		return nil, fmt.Errorf("start_time and end_time are required")
	}
	if !startTime.Before(endTime) {
		return nil, fmt.Errorf("start_time must be before end_time")
	}

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

	// 5. Enrich with profiling data (RFD 074, RFD 077).
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
		s.enrichSummaryWithProfiling(ctx, summary, startTime, endTime, topK)
	}

	return convertSummaryMapToSlice(summaryMap), nil
}

// enrichSummaryWithProfiling adds CPU profiling, memory profiling, deployment context,
// and regression indicators to a single service summary (RFD 074, RFD 077).
func (s *EbpfQueryService) enrichSummaryWithProfiling(ctx context.Context, summary *UnifiedSummaryResult, startTime, endTime time.Time, topK int) {
	profilingResult, err := s.db.GetTopKHotspots(ctx, summary.ServiceName, startTime, endTime, topK)
	if err != nil || profilingResult == nil || profilingResult.TotalSamples < database.MinSamplesForSummary {
		return
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

	// Enrich with memory profiling data (RFD 077).
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
	ebpfMetrics, err := s.QueryMetrics(ctx, startTime, endTime, &agentv1.QueryEbpfMetricsRequest{
		ServiceNames: []string{serviceName},
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
