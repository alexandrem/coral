package server

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/colony"
)

// QueryUnifiedSummary handles unified summary queries (RFD 067).
func (s *Server) QueryUnifiedSummary(
	ctx context.Context,
	req *connect.Request[colonyv1.QueryUnifiedSummaryRequest],
) (*connect.Response[colonyv1.QueryUnifiedSummaryResponse], error) {
	// Type assert to get the actual eBPF service.
	ebpfQueryService, ok := s.ebpfService.(interface {
		QueryUnifiedSummary(ctx context.Context, serviceName string, startTime, endTime time.Time) ([]colony.UnifiedSummaryResult, error)
	})
	if !ok || ebpfQueryService == nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("eBPF service not available"))
	}

	// Parse time range
	startTime, endTime, err := parseTimeRange(req.Msg.TimeRange)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid time_range: %w", err))
	}

	// Call backend service
	results, err := ebpfQueryService.QueryUnifiedSummary(ctx, req.Msg.Service, startTime, endTime)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query summary: %w", err))
	}

	// Format results as text with source annotations
	text := "Service Health Summary:\n\n"
	for _, r := range results {
		statusIcon := "✅"
		if r.Status == "degraded" {
			statusIcon = "⚠️"
		} else if r.Status == "critical" {
			statusIcon = "❌"
		}

		text += fmt.Sprintf("%s %s (%s)\n", statusIcon, r.ServiceName, r.Source)
		text += fmt.Sprintf("   Status: %s\n", r.Status)
		text += fmt.Sprintf("   Requests: %d\n", r.RequestCount)
		text += fmt.Sprintf("   Error Rate: %.2f%%\n", r.ErrorRate)
		text += fmt.Sprintf("   Avg Latency: %.2fms\n\n", r.AvgLatencyMs)
	}

	return connect.NewResponse(&colonyv1.QueryUnifiedSummaryResponse{
		Result: text,
	}), nil
}

// QueryUnifiedTraces handles unified trace queries (RFD 067).
func (s *Server) QueryUnifiedTraces(
	ctx context.Context,
	req *connect.Request[colonyv1.QueryUnifiedTracesRequest],
) (*connect.Response[colonyv1.QueryUnifiedTracesResponse], error) {
	// Type assert to get the actual eBPF service.
	ebpfQueryService, ok := s.ebpfService.(interface {
		QueryUnifiedTraces(ctx context.Context, traceID, serviceName string, startTime, endTime time.Time, minDurationUs int64, maxTraces int) ([]*agentv1.EbpfTraceSpan, error)
	})
	if !ok || ebpfQueryService == nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("eBPF service not available"))
	}

	// Parse time range
	startTime, endTime, err := parseTimeRange(req.Msg.TimeRange)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid time_range: %w", err))
	}

	// Convert min_duration_ms to microseconds
	minDurationUs := int64(req.Msg.MinDurationMs) * 1000

	// Call backend service
	spans, err := ebpfQueryService.QueryUnifiedTraces(ctx, req.Msg.TraceId, req.Msg.Service, startTime, endTime, minDurationUs, int(req.Msg.MaxTraces))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query traces: %w", err))
	}

	// Format results as text with source annotations
	text := fmt.Sprintf("Found %d spans:\n\n", len(spans))

	// Group spans by trace ID for better readability
	traceGroups := make(map[string][]*agentv1.EbpfTraceSpan)
	for _, span := range spans {
		traceGroups[span.TraceId] = append(traceGroups[span.TraceId], span)
	}

	for traceID, traceSpans := range traceGroups {
		text += fmt.Sprintf("Trace: %s (%d spans)\n", traceID, len(traceSpans))
		for _, span := range traceSpans {
			durationMs := float64(span.DurationUs) / 1000.0
			sourceIcon := "📍" // Default eBPF
			if span.ServiceName != "" && len(span.ServiceName) > 6 {
				if span.ServiceName[len(span.ServiceName)-6:] == "[OTLP]" {
					sourceIcon = "📊" // OTLP data
				}
			}

			text += fmt.Sprintf("  %s %s: %s (%.2fms)\n",
				sourceIcon, span.ServiceName, span.SpanName, durationMs)

			// Show OTLP attributes if present
			if source, ok := span.Attributes["source"]; ok && source == "OTLP" {
				text += fmt.Sprintf("     Aggregated: %s spans, %s errors\n",
					span.Attributes["total_spans"], span.Attributes["error_count"])
			}
		}
		text += "\n"
	}

	return connect.NewResponse(&colonyv1.QueryUnifiedTracesResponse{
		Result: text,
	}), nil
}

// QueryUnifiedMetrics handles unified metrics queries (RFD 067).
func (s *Server) QueryUnifiedMetrics(
	ctx context.Context,
	req *connect.Request[colonyv1.QueryUnifiedMetricsRequest],
) (*connect.Response[colonyv1.QueryUnifiedMetricsResponse], error) {
	// Type assert to get the actual eBPF service.
	ebpfQueryService, ok := s.ebpfService.(interface {
		QueryUnifiedMetrics(ctx context.Context, serviceName string, startTime, endTime time.Time) (*agentv1.QueryEbpfMetricsResponse, error)
	})
	if !ok || ebpfQueryService == nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("eBPF service not available"))
	}

	// Parse time range
	startTime, endTime, err := parseTimeRange(req.Msg.TimeRange)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid time_range: %w", err))
	}

	// Call backend service
	metrics, err := ebpfQueryService.QueryUnifiedMetrics(ctx, req.Msg.Service, startTime, endTime)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query metrics: %w", err))
	}

	// Format results as text with source annotations
	text := fmt.Sprintf("Metrics for %s:\n\n", req.Msg.Service)

	if len(metrics.HttpMetrics) > 0 {
		text += "HTTP Metrics:\n"
		for _, m := range metrics.HttpMetrics {
			text += fmt.Sprintf("  %s %s %s\n", m.HttpMethod, m.HttpRoute, m.ServiceName)
			// Calculate percentiles from buckets if available
			p50, p95, p99 := "-", "-", "-"
			if len(m.LatencyBuckets) >= 3 {
				p50 = fmt.Sprintf("%.2fms", m.LatencyBuckets[0])
				p95 = fmt.Sprintf("%.2fms", m.LatencyBuckets[1])
				p99 = fmt.Sprintf("%.2fms", m.LatencyBuckets[2])
			}
			text += fmt.Sprintf("    Requests: %d | P50: %s | P95: %s | P99: %s\n",
				m.RequestCount, p50, p95, p99)
		}
		text += "\n"
	}

	if len(metrics.GrpcMetrics) > 0 {
		text += fmt.Sprintf("gRPC Metrics: %d\n", len(metrics.GrpcMetrics))
	}

	if len(metrics.SqlMetrics) > 0 {
		text += fmt.Sprintf("SQL Metrics: %d\n", len(metrics.SqlMetrics))
	}

	return connect.NewResponse(&colonyv1.QueryUnifiedMetricsResponse{
		Result: text,
	}), nil
}

// QueryUnifiedLogs handles unified log queries (RFD 067).
func (s *Server) QueryUnifiedLogs(
	ctx context.Context,
	req *connect.Request[colonyv1.QueryUnifiedLogsRequest],
) (*connect.Response[colonyv1.QueryUnifiedLogsResponse], error) {
	// Type assert to get the actual eBPF service.
	ebpfQueryService, ok := s.ebpfService.(interface {
		QueryUnifiedLogs(ctx context.Context, serviceName string, startTime, endTime time.Time, level string, search string) ([]string, error)
	})
	if !ok || ebpfQueryService == nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("eBPF service not available"))
	}

	// Parse time range
	startTime, endTime, err := parseTimeRange(req.Msg.TimeRange)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid time_range: %w", err))
	}

	// Call backend service
	logs, err := ebpfQueryService.QueryUnifiedLogs(ctx, req.Msg.Service, startTime, endTime, req.Msg.Level, req.Msg.Search)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query logs: %w", err))
	}

	// Format results as text
	text := fmt.Sprintf("Found %d logs.\n", len(logs))

	return connect.NewResponse(&colonyv1.QueryUnifiedLogsResponse{
		Result: text,
	}), nil
}

// parseTimeRange parses a time range string (e.g., "5m", "1h") into start and end times.
func parseTimeRange(timeRange string) (time.Time, time.Time, error) {
	if timeRange == "" {
		timeRange = "1h"
	}

	duration, err := time.ParseDuration(timeRange)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid duration format: %w", err)
	}

	endTime := time.Now()
	startTime := endTime.Add(-duration)

	return startTime, endTime, nil
}
