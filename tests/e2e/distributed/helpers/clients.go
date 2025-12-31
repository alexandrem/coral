package helpers

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	agentv1connect "github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	colonyv1connect "github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	discoveryv1 "github.com/coral-mesh/coral/coral/discovery/v1"
	discoveryv1connect "github.com/coral-mesh/coral/coral/discovery/v1/discoveryv1connect"
)

// NewDiscoveryClient creates a new discovery service client.
func NewDiscoveryClient(endpoint string) discoveryv1connect.DiscoveryServiceClient {
	return discoveryv1connect.NewDiscoveryServiceClient(
		http.DefaultClient,
		endpoint,
	)
}

// NewColonyClient creates a new colony service client.
func NewColonyClient(endpoint string) colonyv1connect.ColonyServiceClient {
	return colonyv1connect.NewColonyServiceClient(
		http.DefaultClient,
		endpoint,
	)
}

// LookupColony queries the discovery service for colony information.
func LookupColony(ctx context.Context, client discoveryv1connect.DiscoveryServiceClient, meshID string) (*discoveryv1.LookupColonyResponse, error) {
	req := connect.NewRequest(&discoveryv1.LookupColonyRequest{
		MeshId: meshID,
	})

	resp, err := client.LookupColony(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup colony: %w", err)
	}

	return resp.Msg, nil
}

// GetColonyStatus queries the colony for its status.
func GetColonyStatus(ctx context.Context, client colonyv1connect.ColonyServiceClient) (*colonyv1.GetStatusResponse, error) {
	req := connect.NewRequest(&colonyv1.GetStatusRequest{})

	resp, err := client.GetStatus(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get colony status: %w", err)
	}

	return resp.Msg, nil
}

// ListAgents queries the colony for registered agents.
func ListAgents(ctx context.Context, client colonyv1connect.ColonyServiceClient) (*colonyv1.ListAgentsResponse, error) {
	req := connect.NewRequest(&colonyv1.ListAgentsRequest{})

	resp, err := client.ListAgents(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list agents: %w", err)
	}

	return resp.Msg, nil
}

// NewAgentClient creates a new agent service client.
func NewAgentClient(endpoint string) agentv1connect.AgentServiceClient {
	return agentv1connect.NewAgentServiceClient(
		http.DefaultClient,
		endpoint,
	)
}

// QueryAgentTelemetry queries an agent for telemetry spans.
func QueryAgentTelemetry(
	ctx context.Context,
	client agentv1connect.AgentServiceClient,
	startTime, endTime int64,
	serviceNames []string,
) (*agentv1.QueryTelemetryResponse, error) {
	req := connect.NewRequest(&agentv1.QueryTelemetryRequest{
		StartTime:    startTime,
		EndTime:      endTime,
		ServiceNames: serviceNames,
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

// QueryAgentSystemMetrics queries an agent for system metrics.
func QueryAgentSystemMetrics(
	ctx context.Context,
	client agentv1connect.AgentServiceClient,
	startTime, endTime int64,
	metricNames []string,
) (*agentv1.QuerySystemMetricsResponse, error) {
	req := connect.NewRequest(&agentv1.QuerySystemMetricsRequest{
		StartTime:   startTime,
		EndTime:     endTime,
		MetricNames: metricNames,
	})

	resp, err := client.QuerySystemMetrics(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to query system metrics: %w", err)
	}

	return resp.Msg, nil
}
