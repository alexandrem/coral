package helpers

import (
	"context"
	"fmt"

	"connectrpc.com/connect"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
)

// QueryColonyMetrics queries colony for unified metrics (HTTP/gRPC/SQL).
func QueryColonyMetrics(
	ctx context.Context,
	client colonyv1connect.ColonyServiceClient,
	serviceName string,
	timeRange string,
	source string, // "ebpf", "telemetry", or "all"
) (*colonyv1.QueryUnifiedMetricsResponse, error) {
	req := connect.NewRequest(&colonyv1.QueryUnifiedMetricsRequest{
		Service:   serviceName,
		TimeRange: timeRange,
		Source:    source,
	})

	resp, err := client.QueryUnifiedMetrics(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to query colony metrics: %w", err)
	}

	return resp.Msg, nil
}

// QueryAgentSystemMetrics queries an agent for system metrics using sequence-based polling.
func QueryAgentSystemMetrics(
	ctx context.Context,
	client agentv1connect.AgentServiceClient,
	metricNames []string,
) (*agentv1.QuerySystemMetricsResponse, error) {
	req := connect.NewRequest(&agentv1.QuerySystemMetricsRequest{
		MetricNames: metricNames,
		StartSeqId:  0,
		MaxRecords:  10000,
	})

	resp, err := client.QuerySystemMetrics(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to query system metrics: %w", err)
	}

	return resp.Msg, nil
}

// QueryAgentEbpfMetrics queries an agent for eBPF metrics (Beyla) using sequence-based polling.
func QueryAgentEbpfMetrics(
	ctx context.Context,
	client agentv1connect.AgentServiceClient,
	serviceNames []string,
) (*agentv1.QueryEbpfMetricsResponse, error) {
	req := connect.NewRequest(&agentv1.QueryEbpfMetricsRequest{
		ServiceNames: serviceNames,
		MetricTypes:  nil, // Query all metric types.
		MaxRecords:   10000,
	})

	resp, err := client.QueryEbpfMetrics(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to query eBPF metrics: %w", err)
	}

	return resp.Msg, nil
}

// QueryUprobeEvents queries uprobe events for a session.
func QueryUprobeEvents(
	ctx context.Context,
	client colonyv1connect.ColonyDebugServiceClient,
	sessionID string,
	maxEvents int32,
) (*colonyv1.QueryUprobeEventsResponse, error) {
	req := connect.NewRequest(&colonyv1.QueryUprobeEventsRequest{
		SessionId: sessionID,
		MaxEvents: maxEvents,
	})

	resp, err := client.QueryUprobeEvents(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to query uprobe events: %w", err)
	}

	return resp.Msg, nil
}

// QueryAgentTelemetry queries an agent for telemetry spans using sequence-based polling.
func QueryAgentTelemetry(
	ctx context.Context,
	client agentv1connect.AgentServiceClient,
	serviceNames []string,
) (*agentv1.QueryTelemetryResponse, error) {
	req := connect.NewRequest(&agentv1.QueryTelemetryRequest{
		ServiceNames: serviceNames,
		StartSeqId:   0,
		MaxRecords:   10000,
	})

	resp, err := client.QueryTelemetry(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to query telemetry: %w", err)
	}

	return resp.Msg, nil
}

// QueryColonySummary queries colony for unified summary.
func QueryColonySummary(
	ctx context.Context,
	client colonyv1connect.ColonyServiceClient,
	serviceName string,
	timeRange string,
) (*colonyv1.QueryUnifiedSummaryResponse, error) {
	req := connect.NewRequest(&colonyv1.QueryUnifiedSummaryRequest{
		Service:   serviceName,
		TimeRange: timeRange,
	})

	resp, err := client.QueryUnifiedSummary(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to query colony summary: %w", err)
	}

	return resp.Msg, nil
}

// ExecuteColonyQuery executes a raw SQL query on colony's DuckDB.
func ExecuteColonyQuery(
	ctx context.Context,
	client colonyv1connect.ColonyServiceClient,
	sql string,
	maxRows int32,
) (*colonyv1.ExecuteQueryResponse, error) {
	req := connect.NewRequest(&colonyv1.ExecuteQueryRequest{
		Sql:     sql,
		MaxRows: maxRows,
	})

	resp, err := client.ExecuteQuery(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute colony query: %w", err)
	}

	return resp.Msg, nil
}
