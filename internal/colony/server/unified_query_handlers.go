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

	// Convert to protobuf structured results
	summaries := make([]*colonyv1.UnifiedSummaryResult, 0, len(results))
	for _, r := range results {
		summaries = append(summaries, &colonyv1.UnifiedSummaryResult{
			ServiceName:           r.ServiceName,
			Status:                r.Status,
			RequestCount:          r.RequestCount,
			ErrorRate:             r.ErrorRate,
			AvgLatencyMs:          r.AvgLatencyMs,
			Source:                r.Source,
			Issues:                r.Issues,
			HostCpuUtilization:    r.HostCPUUtilization,
			HostCpuUtilizationAvg: r.HostCPUUtilizationAvg,
			HostMemoryUsageGb:     r.HostMemoryUsageGB,
			HostMemoryLimitGb:     r.HostMemoryLimitGB,
			HostMemoryUtilization: r.HostMemoryUtilization,
			AgentId:               r.AgentID,
		})
	}

	return connect.NewResponse(&colonyv1.QueryUnifiedSummaryResponse{
		Summaries: summaries,
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

	// Count unique traces
	traceGroups := make(map[string]bool)
	for _, span := range spans {
		traceGroups[span.TraceId] = true
	}

	return connect.NewResponse(&colonyv1.QueryUnifiedTracesResponse{
		Spans:       spans,
		TotalTraces: int32(len(traceGroups)),
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

	// Calculate total metrics count
	totalMetrics := len(metrics.HttpMetrics) + len(metrics.GrpcMetrics) + len(metrics.SqlMetrics)

	return connect.NewResponse(&colonyv1.QueryUnifiedMetricsResponse{
		HttpMetrics:  metrics.HttpMetrics,
		GrpcMetrics:  metrics.GrpcMetrics,
		SqlMetrics:   metrics.SqlMetrics,
		TotalMetrics: int32(totalMetrics),
	}), nil
}

// QueryUnifiedLogs handles unified log queries (RFD 067).
func (s *Server) QueryUnifiedLogs(
	ctx context.Context,
	req *connect.Request[colonyv1.QueryUnifiedLogsRequest],
) (*connect.Response[colonyv1.QueryUnifiedLogsResponse], error) {
	// TODO: Implement log querying when log ingestion is available
	// For now, return empty results
	return connect.NewResponse(&colonyv1.QueryUnifiedLogsResponse{
		Logs:      []*colonyv1.UnifiedLogEntry{},
		TotalLogs: 0,
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
