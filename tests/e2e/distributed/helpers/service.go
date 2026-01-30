package helpers

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
)

// AgentEndpointGetter provides access to agent gRPC endpoints.
// This interface allows service helpers to work with different fixture types.
type AgentEndpointGetter interface {
	GetAgentGRPCEndpoint(ctx context.Context, index int) (string, error)
}

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

// ConnectServiceToAgent connects a service to a specific agent by index.
// This helper combines the boilerplate of getting agent endpoint, creating client,
// and connecting the service with automatic error checking. It's useful for e2e
// tests that need to quickly connect services without repetitive code.
//
// The function will automatically fail the test (via require) if any error occurs,
// so callers don't need to check errors manually.
//
// Example:
//
//	helpers.ConnectServiceToAgent(t, ctx, fixture, 0, "otel-app", 8090, "/health")
func ConnectServiceToAgent(
	t T,
	ctx context.Context,
	fixture AgentEndpointGetter,
	agentIndex int,
	serviceName string,
	port int32,
	healthEndpoint string,
) {
	t.Helper()

	// Get agent endpoint.
	agentEndpoint, err := fixture.GetAgentGRPCEndpoint(ctx, agentIndex)
	require.NoError(t, err, "Failed to get agent-%d endpoint", agentIndex)

	// Create agent client.
	agentClient := NewAgentClient(agentEndpoint)

	// Connect service.
	_, err = ConnectService(ctx, agentClient, serviceName, port, healthEndpoint)
	require.NoError(t, err, "Failed to connect %s to agent-%d", serviceName, agentIndex)
}

// ColonyEndpointGetter provides access to colony endpoint.
// This interface allows service helpers to work with different fixture types.
type ColonyEndpointGetter interface {
	GetColonyEndpoint(ctx context.Context) (string, error)
}

// ServiceFixture combines the interfaces needed for service setup operations.
type ServiceFixture interface {
	AgentEndpointGetter
	ColonyEndpointGetter
}

// ServiceConfig defines configuration for a service to connect.
type ServiceConfig struct {
	Name           string
	Port           int32
	HealthEndpoint string
}

// EnsureServicesConnected ensures that test services are connected to an agent.
// This function is idempotent - it checks if services are already connected before
// attempting to connect them. This allows test suites to work both when running
// the full suite and when running individual tests.
//
// The function will wait for services to be registered in the colony after connecting.
//
// Example:
//
//	helpers.EnsureServicesConnected(t, ctx, fixture, 0, []ServiceConfig{
//	    {Name: "cpu-app", Port: 8080, HealthEndpoint: "/health"},
//	    {Name: "otel-app", Port: 8090, HealthEndpoint: "/health"},
//	})
func EnsureServicesConnected(
	t T,
	ctx context.Context,
	fixture ServiceFixture,
	agentIndex int,
	services []ServiceConfig,
) {
	t.Helper()
	t.Log("Ensuring services are connected...")

	// Get agent endpoint.
	agentEndpoint, err := fixture.GetAgentGRPCEndpoint(ctx, agentIndex)
	require.NoError(t, err, "Failed to get agent-%d endpoint", agentIndex)

	agentClient := NewAgentClient(agentEndpoint)

	// Connect services if not already connected.
	t.Log("Connecting test services...")

	for _, svc := range services {
		t.Logf("Connecting service %s on port %d with health %s", svc.Name, svc.Port, svc.HealthEndpoint)
		_, err = ConnectService(ctx, agentClient, svc.Name, svc.Port, svc.HealthEndpoint)
		if err != nil {
			if !isAlreadyConnected(err) {
				t.Errorf("Failed to connect %s:%v", svc.Name, err)
				t.FailNow()
			}
		}
	}

	t.Log("✓ Services connected - waiting for colony to poll...")

	// Wait for colony to poll services from agent (runs every 10 seconds).
	// Using sleep instead of polling to avoid excessive API calls.
	WaitForServices(ctx, 15)

	// Verify services are registered in colony.
	colonyEndpoint, err := fixture.GetColonyEndpoint(ctx)
	if err == nil {
		colonyClient := NewColonyClient(colonyEndpoint)

		// Wait for services to appear in colony registry.
		err = WaitForCondition(ctx, func() bool {
			resp, listErr := ListServices(ctx, colonyClient, "")
			if listErr != nil {
				t.Logf("List services failed (will retry): %v", listErr)
				return false
			}
			return len(resp.Services) >= len(services)
		}, 60000, 2000) // 60s timeout, 2s interval

		if err != nil {
			t.Logf("Warning: Services may not be registered in colony yet: %v", err)
		} else {
			t.Log("✓ Services registered in colony")
		}
	}
}

// DisconnectAllServices disconnects multiple services from an agent.
// Errors are logged but not returned to allow best-effort cleanup.
//
// Example:
//
//	helpers.DisconnectAllServices(t, ctx, fixture, 0, []string{"cpu-app", "otel-app"})
func DisconnectAllServices(
	t T,
	ctx context.Context,
	fixture AgentEndpointGetter,
	agentIndex int,
	serviceNames []string,
) {
	t.Helper()
	t.Log("Disconnecting services...")

	agentEndpoint, err := fixture.GetAgentGRPCEndpoint(ctx, agentIndex)
	if err != nil {
		t.Logf("Failed to get agent-%d endpoint: %v", agentIndex, err)
		return
	}

	agentClient := NewAgentClient(agentEndpoint)

	for _, serviceName := range serviceNames {
		_, err = DisconnectService(ctx, agentClient, serviceName)
		if err != nil {
			t.Logf("Failed to disconnect %s: %v", serviceName, err)
		}
	}

	t.Log("✓ Services disconnected")
}

func isAlreadyConnected(err error) bool {
	return strings.Contains(err.Error(), "already connected")
}
