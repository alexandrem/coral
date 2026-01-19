package fixtures

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// ComposeFixture provides access to services running in docker-compose.
// Unlike ContainerFixture, this doesn't create/destroy containers - it just
// connects to existing services started by docker-compose.
type ComposeFixture struct {
	// Configuration
	ColonyID      string
	CAFingerprint string // Root CA fingerprint for agent bootstrap (RFD 048)

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
		ColonyID:          "",                       // Will be discovered
		CAFingerprint:     "",                       // Will be discovered
		DiscoveryEndpoint: "http://localhost:18080", // E2E uses non-standard port
		ColonyEndpoint:    "http://localhost:9000",
		Agent0Endpoint:    "http://localhost:9001",
		Agent1Endpoint:    "http://localhost:9002",
		CPUAppEndpoint:    "localhost:8081", // cpu-app on port 8080 in agent-0 namespace, exposed as 8081
		OTELAppEndpoint:   "localhost:8082", // otel-app on port 8090 in agent-0 namespace, exposed as 8082
		SDKAppEndpoint:    "localhost:3001",
	}

	// Wait for all services to be healthy
	if err := fixture.waitForServices(ctx); err != nil {
		return nil, fmt.Errorf("services not ready: %w", err)
	}

	// Discover the actual colony ID from discovery service
	if err := fixture.discoverColonyID(ctx); err != nil {
		return nil, fmt.Errorf("failed to discover colony ID: %w", err)
	}

	// Discover the CA fingerprint for agent bootstrap
	if err := fixture.discoverCAFingerprint(ctx); err != nil {
		return nil, fmt.Errorf("failed to discover CA fingerprint: %w", err)
	}

	return fixture, nil
}

// waitForServices waits for all docker-compose services to be healthy.
func (f *ComposeFixture) waitForServices(ctx context.Context) error {
	// Wait for discovery service
	if err := helpers.WaitForHTTPEndpoint(ctx, f.DiscoveryEndpoint+"/health", 30*time.Second); err != nil {
		return fmt.Errorf("discovery service not ready: %w (check for port conflicts with local dev services)", err)
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

// discoverColonyID reads the actual colony ID from the shared volume.
// The colony writes its ID to /shared/colony_id after initialization.
func (f *ComposeFixture) discoverColonyID(ctx context.Context) error {
	// Use docker exec to read the colony ID from the shared volume
	cmd := exec.CommandContext(ctx, "docker", "exec", "distributed-colony-1", "cat", "/shared/colony_id")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to read colony ID from container: %w", err)
	}

	colonyID := strings.TrimSpace(string(output))
	if colonyID == "" {
		return fmt.Errorf("colony ID is empty")
	}

	f.ColonyID = colonyID
	return nil
}

// discoverCAFingerprint reads the Root CA fingerprint from the shared volume.
// The colony writes its CA fingerprint to /shared/ca_fingerprint after initialization.
func (f *ComposeFixture) discoverCAFingerprint(ctx context.Context) error {
	// Use docker exec to read the CA fingerprint from the shared volume
	cmd := exec.CommandContext(ctx, "docker", "exec", "distributed-colony-1", "cat", "/shared/ca_fingerprint")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to read CA fingerprint from container: %w", err)
	}

	fingerprint := strings.TrimSpace(string(output))
	if fingerprint == "" {
		return fmt.Errorf("CA fingerprint is empty")
	}

	f.CAFingerprint = fingerprint
	return nil
}

// GetCAFingerprint returns the Root CA fingerprint for agent bootstrap.
func (f *ComposeFixture) GetCAFingerprint() string {
	return f.CAFingerprint
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

// RestartService restarts a specific service using docker-compose.
func (f *ComposeFixture) RestartService(ctx context.Context, serviceName string) error {
	cmd := exec.CommandContext(ctx, "docker-compose", "restart", serviceName)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to restart service %s: %w\nOutput: %s", serviceName, err, string(output))
	}
	return nil
}
