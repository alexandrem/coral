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

	// Format results as text
	text := "Service Health Summary:\n\n"
	for _, r := range results {
		text += fmt.Sprintf("Service: %s, Status: %s\n", r.ServiceName, r.Status)
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

	// Format results as text
	text := fmt.Sprintf("Found %d spans.\n", len(spans))

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

	// Format results as text
	text := fmt.Sprintf("Metrics for %s:\n", req.Msg.Service)
	if len(metrics.HttpMetrics) > 0 {
		text += fmt.Sprintf("HTTP Metrics: %d\n", len(metrics.HttpMetrics))
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
