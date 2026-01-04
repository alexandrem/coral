package fixtures

import (
	"context"
	"fmt"
	"time"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// ComposeFixture provides access to services running in docker-compose.
// Unlike ContainerFixture, this doesn't create/destroy containers - it just
// connects to existing services started by docker-compose.
type ComposeFixture struct {
	// Configuration
	ColonyID     string
	ColonySecret string

	// Service endpoints (set by docker-compose port mappings)
	DiscoveryEndpoint string
	ColonyEndpoint    string
	Agent0Endpoint    string
	Agent1Endpoint    string
	CPUAppEndpoint    string
	OTELAppEndpoint   string
	SDKAppEndpoint    string
}

// NewComposeFixture creates a fixture that connects to docker-compose services.
// It doesn't start containers - they must already be running via docker-compose.
func NewComposeFixture(ctx context.Context) (*ComposeFixture, error) {
	fixture := &ComposeFixture{
		ColonyID:          "test-colony-e2e",
		ColonySecret:      "test-secret-12345",
		DiscoveryEndpoint: "http://localhost:8080",
		ColonyEndpoint:    "localhost:9000",
		Agent0Endpoint:    "localhost:9001",
		Agent1Endpoint:    "localhost:9002",
		CPUAppEndpoint:    "localhost:8081",
		OTELAppEndpoint:   "localhost:8082",
		SDKAppEndpoint:    "localhost:3001",
	}

	// Wait for all services to be healthy
	if err := fixture.waitForServices(ctx); err != nil {
		return nil, fmt.Errorf("services not ready: %w", err)
	}

	return fixture, nil
}

// waitForServices waits for all docker-compose services to be healthy.
func (f *ComposeFixture) waitForServices(ctx context.Context) error {
	// Wait for discovery service
	if err := helpers.WaitForHTTPEndpoint(ctx, f.DiscoveryEndpoint+"/health", 30*time.Second); err != nil {
		return fmt.Errorf("discovery service not ready: %w", err)
	}

	// Wait for CPU app
	if err := helpers.WaitForHTTPEndpoint(ctx, "http://"+f.CPUAppEndpoint+"/health", 30*time.Second); err != nil {
		return fmt.Errorf("cpu-app not ready: %w", err)
	}

	// Wait for OTEL app
	if err := helpers.WaitForHTTPEndpoint(ctx, "http://"+f.OTELAppEndpoint+"/health", 30*time.Second); err != nil {
		return fmt.Errorf("otel-app not ready: %w", err)
	}

	// Wait for SDK app
	if err := helpers.WaitForHTTPEndpoint(ctx, "http://"+f.SDKAppEndpoint+"/health", 30*time.Second); err != nil {
		return fmt.Errorf("sdk-app not ready: %w", err)
	}

	// Give colony and agents a bit more time to initialize WireGuard mesh
	// TODO: Add proper health checks for colony/agents
	time.Sleep(10 * time.Second)

	return nil
}

// Cleanup is a no-op for compose fixtures since we don't manage container lifecycle.
// Containers are stopped via docker-compose down.
func (f *ComposeFixture) Cleanup(ctx context.Context) error {
	// No-op: docker-compose manages lifecycle
	return nil
}

// GetDiscoveryEndpoint returns the discovery service HTTP endpoint.
func (f *ComposeFixture) GetDiscoveryEndpoint(ctx context.Context) (string, error) {
	return f.DiscoveryEndpoint, nil
}

// GetColonyEndpoint returns the colony gRPC endpoint.
func (f *ComposeFixture) GetColonyEndpoint(ctx context.Context) (string, error) {
	return f.ColonyEndpoint, nil
}

// GetAgentGRPCEndpoint returns the agent gRPC endpoint.
func (f *ComposeFixture) GetAgentGRPCEndpoint(ctx context.Context, index int) (string, error) {
	switch index {
	case 0:
		return f.Agent0Endpoint, nil
	case 1:
		return f.Agent1Endpoint, nil
	default:
		return "", fmt.Errorf("agent index %d not available (only 0-1 supported)", index)
	}
}

// GetCPUAppEndpoint returns the CPU app HTTP endpoint.
func (f *ComposeFixture) GetCPUAppEndpoint(ctx context.Context) (string, error) {
	return f.CPUAppEndpoint, nil
}

// GetOTELAppEndpoint returns the OTEL app HTTP endpoint.
func (f *ComposeFixture) GetOTELAppEndpoint(ctx context.Context) (string, error) {
	return f.OTELAppEndpoint, nil
}

// GetSDKAppEndpoint returns the SDK app HTTP endpoint.
func (f *ComposeFixture) GetSDKAppEndpoint(ctx context.Context) (string, error) {
	return f.SDKAppEndpoint, nil
}
