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

// ListServices queries colony for registered services.
func ListServices(
	ctx context.Context,
	client colonyv1connect.ColonyServiceClient,
	namespace string,
) (*colonyv1.ListServicesResponse, error) {
	req := connect.NewRequest(&colonyv1.ListServicesRequest{
		Namespace: namespace,
	})

	resp, err := client.ListServices(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}

	return resp.Msg, nil
}

// ConnectService connects a service to an agent dynamically.
func ConnectService(
	ctx context.Context,
	client agentv1connect.AgentServiceClient,
	serviceName string,
	port int32,
	healthEndpoint string,
) (*agentv1.ConnectServiceResponse, error) {
	req := connect.NewRequest(&agentv1.ConnectServiceRequest{
		Name:           serviceName,
		Port:           port,
		HealthEndpoint: healthEndpoint,
		ServiceType:    "http", // Default to HTTP for E2E tests.
	})

	resp, err := client.ConnectService(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to connect service: %w", err)
	}

	if !resp.Msg.Success {
		return nil, fmt.Errorf("service connection failed: %s", resp.Msg.Error)
	}

	return resp.Msg, nil
}

// DisconnectService disconnects a service from the agent.
func DisconnectService(
	ctx context.Context,
	client agentv1connect.AgentServiceClient,
	serviceName string,
) (*agentv1.DisconnectServiceResponse, error) {
	req := connect.NewRequest(&agentv1.DisconnectServiceRequest{
		ServiceName: serviceName,
	})

	resp, err := client.DisconnectService(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to disconnect service: %w", err)
	}

	if !resp.Msg.Success {
		return nil, fmt.Errorf("service disconnection failed: %s", resp.Msg.Error)
	}

	return resp.Msg, nil
}
