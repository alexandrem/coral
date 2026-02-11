package fixtures

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

const (
	publicDiscoveryEndpoint = "https://discovery.coralmesh.dev"
	localDiscoveryEndpoint  = "http://127.0.0.1:18080"
	localColonyEndpoint     = "http://127.0.0.1:9000"
	localAgent0Endpoint     = "http://127.0.0.1:9001"
	localAgent1Endpoint     = "http://127.0.0.1:9002"
	localCPUAppEndpoint     = "127.0.0.1:8081" // cpu-app on port 8080 in agent-0 namespace, exposed as 8081.
	localOTELAppEndpoint    = "127.0.0.1:8082" // otel-app on port 8090 in agent-0 namespace, exposed as 8082.
	localSDKAppEndpoint     = "127.0.0.1:3001"
	localMemoryAppEndpoint  = "127.0.0.1:8083" // memory-app on port 8080 in agent-1 namespace, exposed as 8083.
)

// isDiscoveryContainerRunning checks whether a "discovery" docker container exists and is running.
func isDiscoveryContainerRunning() bool {
	cmd := exec.Command("docker", "ps", "--filter", "name=discovery", "--filter", "status=running", "--format", "{{.Names}}")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(output), "discovery")
}

// ComposeFixture provides access to services running in docker-compose.
// Unlike ContainerFixture, this doesn't create/destroy containers - it just
// connects to existing services started by docker-compose.
type ComposeFixture struct {
	// Configuration
	ColonyID      string
	CAFingerprint string // Root CA fingerprint for agent bootstrap (RFD 048).
	BootstrapPSK  string // Bootstrap pre-shared key (RFD 088).

	// Service endpoints (set by docker-compose port mappings)
	DiscoveryEndpoint string
	ColonyEndpoint    string
	Agent0Endpoint    string
	Agent1Endpoint    string
	CPUAppEndpoint    string
	OTELAppEndpoint   string
	SDKAppEndpoint    string
	MemoryAppEndpoint string
}

// NewComposeFixture creates a fixture that connects to docker-compose services.
// It doesn't start containers - they must already be running via docker-compose.
func NewComposeFixture(ctx context.Context) (*ComposeFixture, error) {
	// Detect local discovery service. When running with local discovery
	// (make local-up), port 18080 is exposed on the host. The fixture
	// probes it to determine whether to use it for health checks and
	// the CLI .env file. This keeps CORAL_DISCOVERY_ENDPOINT free for
	// container-internal use (e.g. http://discovery:8080).
	discoveryEndpoint := publicDiscoveryEndpoint
	if isDiscoveryContainerRunning() {
		discoveryEndpoint = localDiscoveryEndpoint
	}

	fixture := &ComposeFixture{
		ColonyID:          "", // Will be discovered.
		CAFingerprint:     "", // Will be discovered.
		DiscoveryEndpoint: discoveryEndpoint,
		ColonyEndpoint:    localColonyEndpoint,
		Agent0Endpoint:    localAgent0Endpoint,
		Agent1Endpoint:    localAgent1Endpoint,
		CPUAppEndpoint:    localCPUAppEndpoint,
		OTELAppEndpoint:   localOTELAppEndpoint,
		SDKAppEndpoint:    localSDKAppEndpoint,
		MemoryAppEndpoint: localMemoryAppEndpoint,
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

	// Discover the bootstrap PSK (RFD 088).
	if err := fixture.discoverBootstrapPSK(ctx); err != nil {
		return nil, fmt.Errorf("failed to discover bootstrap PSK: %w", err)
	}

	return fixture, nil
}

// waitForServices waits for all docker-compose services to be healthy.
func (f *ComposeFixture) waitForServices(ctx context.Context) error {
	// Wait for discovery service only when using a local endpoint.
	if f.DiscoveryEndpoint != "" {
		if err := helpers.WaitForHTTPEndpoint(ctx, f.DiscoveryEndpoint+"/health", 30*time.Second); err != nil {
			return fmt.Errorf("discovery service not ready: %w (check for port conflicts with local dev services)", err)
		}
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

	// Wait for memory app
	if err := helpers.WaitForHTTPEndpoint(ctx, "http://"+f.MemoryAppEndpoint+"/health", 30*time.Second); err != nil {
		return fmt.Errorf("memory-app not ready: %w", err)
	}

	// Wait for colony and agents via their /status endpoints
	if err := helpers.WaitForHTTPEndpoint(ctx, f.ColonyEndpoint+"/status", 60*time.Second); err != nil {
		return fmt.Errorf("colony not ready: %w", err)
	}
	if err := helpers.WaitForHTTPEndpoint(ctx, f.Agent0Endpoint+"/status", 60*time.Second); err != nil {
		return fmt.Errorf("agent-0 not ready: %w", err)
	}
	if err := helpers.WaitForHTTPEndpoint(ctx, f.Agent1Endpoint+"/status", 60*time.Second); err != nil {
		return fmt.Errorf("agent-1 not ready: %w", err)
	}

	return nil
}

// discoverColonyID reads the actual colony ID from the shared volume.
// The colony writes its ID to /shared/colony_id after initialization.
func (f *ComposeFixture) discoverColonyID(ctx context.Context) error {
	// Use docker exec to read the colony ID from the shared volume
	cmd := exec.CommandContext(ctx, "docker", "exec", "coral-e2e-colony-1", "cat",
		"/shared/colony_id")
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
	cmd := exec.CommandContext(ctx, "docker", "exec", "coral-e2e-colony-1", "cat",
		"/shared/ca_fingerprint")
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

// discoverBootstrapPSK reads the bootstrap PSK from the shared volume (RFD 088).
func (f *ComposeFixture) discoverBootstrapPSK(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "docker", "exec", "coral-e2e-colony-1", "cat",
		"/shared/bootstrap_psk")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to read bootstrap PSK from container: %w", err)
	}

	psk := strings.TrimSpace(string(output))
	if psk == "" {
		return fmt.Errorf("bootstrap PSK is empty")
	}

	f.BootstrapPSK = psk
	return nil
}

// GetBootstrapPSK returns the bootstrap pre-shared key (RFD 088).
func (f *ComposeFixture) GetBootstrapPSK() string {
	return f.BootstrapPSK
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

// GetMemoryAppEndpoint returns the memory app HTTP endpoint.
func (f *ComposeFixture) GetMemoryAppEndpoint(ctx context.Context) (string, error) {
	return f.MemoryAppEndpoint, nil
}

// RestartService restarts a specific service using docker-compose.
func (f *ComposeFixture) RestartService(ctx context.Context, serviceName string) error {
	// Use docker restart directly with the container name rather than
	// docker-compose restart, which requires compose file context that
	// may not be available in the test runner's working directory.
	containerName := fmt.Sprintf("coral-e2e-%s-1", serviceName)
	cmd := exec.CommandContext(ctx, "docker", "restart", containerName)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to restart container %s: %w\nOutput: %s", containerName, err, string(output))
	}
	return nil
}

// ReloadColonyConfig sends SIGHUP to the colony process to reload configuration
// and API tokens from disk without restarting the container.
func (f *ComposeFixture) ReloadColonyConfig(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "docker", "exec", "coral-e2e-colony-1",
		"sh", "-c", "kill -HUP $(pidof coral)")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to send SIGHUP to colony: %w\nOutput: %s", err, string(output))
	}
	return nil
}

// CreateDotEnvFile creates a .env file with the environment variables needed for the CLI
// to talk to the colony endpoint hosted in the container.
func (f *ComposeFixture) CreateDotEnvFile(ctx context.Context) error {
	// Run coral colony export inside the colony container
	cmd := exec.CommandContext(ctx, "docker", "exec", "coral-e2e-colony-1", "/usr/local/bin/coral",
		"colony", "export", f.ColonyID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to run coral colony export in container: %w\nOutput: %s", err, string(output))
	}

	var envLines []string
	envLines = append(envLines, "# Coral E2E Distributed Test Environment")
	envLines = append(envLines, fmt.Sprintf("# Generated: %s", time.Now().Format("2006-01-02 15:04:05")))
	envLines = append(envLines, "")

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "export ") {
			// The container exports a Docker-internal discovery endpoint
			// (e.g. http://discovery:8080) which is unreachable from the
			// host. Always replace it with the host-accessible endpoint:
			// either the local discovery service or the public one from
			// the container's own config.
			if strings.Contains(line, "CORAL_DISCOVERY_ENDPOINT") {
				if f.DiscoveryEndpoint != "" {
					envLines = append(envLines, fmt.Sprintf("export CORAL_DISCOVERY_ENDPOINT=\"%s\"", f.DiscoveryEndpoint))
				}
				// When no local discovery endpoint is set, the containers
				// use the public discovery service directly — skip the
				// line so the CLI inherits the default.
			} else {
				envLines = append(envLines, line)
			}
		}
	}

	// Create an admin API token for the CLI
	tokenCmd := exec.CommandContext(ctx, "docker", "exec", "coral-e2e-colony-1",
		"/usr/local/bin/coral", "colony", "token", "create", "e2e-cli-admin", "--permissions", "admin", "--recreate")
	tokenOutput, err := tokenCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to run token create in container: %w\nOutput: %s", err, string(tokenOutput))
	}
	lines = strings.Split(string(tokenOutput), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "Token: ") {
			token := strings.TrimPrefix(line, "Token: ")
			envLines = append(envLines, fmt.Sprintf("export CORAL_API_TOKEN=\"%s\"", token))
			break
		}
	}

	// Add public endpoint (HTTPS) for convenience
	envLines = append(envLines, "export CORAL_COLONY_ENDPOINT=\"https://localhost:8443\"")
	envLines = append(envLines, "export CORAL_INSECURE=true")

	// Write to .env file in the current directory
	dotEnvPath := ".env"
	err = os.WriteFile(dotEnvPath, []byte(strings.Join(envLines, "\n")+"\n"), 0644)
	if err != nil {
		return fmt.Errorf("failed to write .env file: %w", err)
	}

	// Signal colony to reload tokens from disk (SIGHUP).
	if err := f.ReloadColonyConfig(ctx); err != nil {
		return fmt.Errorf("failed to reload colony config: %w", err)
	}

	return nil
}
