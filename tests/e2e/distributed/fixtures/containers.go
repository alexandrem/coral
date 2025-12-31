package fixtures

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// ContainerFixture manages all containers for E2E tests.
type ContainerFixture struct {
	Network   *testcontainers.DockerNetwork
	Discovery testcontainers.Container
	Colony    testcontainers.Container
	Agents    []testcontainers.Container
	Apps      map[string]testcontainers.Container

	// Configuration.
	ColonyID     string
	ColonySecret string
}

// FixtureOptions configures the container fixture.
type FixtureOptions struct {
	NumAgents    int
	ColonyID     string
	ColonySecret string
	WithOTELApp  bool
	WithSDKApp   bool
}

// NewContainerFixture creates and starts all containers for E2E testing.
func NewContainerFixture(ctx context.Context, opts FixtureOptions) (*ContainerFixture, error) {
	// Set defaults.
	if opts.ColonyID == "" {
		opts.ColonyID = fmt.Sprintf("test-colony-%d", time.Now().Unix())
	}
	if opts.ColonySecret == "" {
		opts.ColonySecret = "test-secret-12345"
	}
	if opts.NumAgents == 0 {
		opts.NumAgents = 1
	}

	fixture := &ContainerFixture{
		ColonyID:     opts.ColonyID,
		ColonySecret: opts.ColonySecret,
		Apps:         make(map[string]testcontainers.Container),
	}

	// 1. Create Docker network.
	net, err := network.New(ctx,
		network.WithCheckDuplicate(),
		network.WithDriver("bridge"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create network: %w", err)
	}
	fixture.Network = net

	// 2. Start discovery service.
	discovery, err := fixture.startDiscoveryService(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start discovery service: %w", err)
	}
	fixture.Discovery = discovery

	// 3. Start colony.
	colony, err := fixture.startColony(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start colony: %w", err)
	}
	fixture.Colony = colony

	// 4. Start agents.
	for i := 0; i < opts.NumAgents; i++ {
		agent, err := fixture.startAgent(ctx, i)
		if err != nil {
			return nil, fmt.Errorf("failed to start agent %d: %w", i, err)
		}
		fixture.Agents = append(fixture.Agents, agent)
	}

	// 5. Start test applications if requested.
	if opts.WithOTELApp {
		app, err := fixture.startOTELApp(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to start OTEL app: %w", err)
		}
		fixture.Apps["otel-app"] = app
	}

	if opts.WithSDKApp {
		app, err := fixture.startSDKApp(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to start SDK app: %w", err)
		}
		fixture.Apps["sdk-app"] = app
	}

	return fixture, nil
}

// Cleanup stops and removes all containers.
func (f *ContainerFixture) Cleanup(ctx context.Context) error {
	var errs []error

	// Stop apps.
	for name, app := range f.Apps {
		if err := app.Terminate(ctx); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop app %s: %w", name, err))
		}
	}

	// Stop agents.
	for i, agent := range f.Agents {
		if err := agent.Terminate(ctx); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop agent %d: %w", i, err))
		}
	}

	// Stop colony.
	if f.Colony != nil {
		if err := f.Colony.Terminate(ctx); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop colony: %w", err))
		}
	}

	// Stop discovery.
	if f.Discovery != nil {
		if err := f.Discovery.Terminate(ctx); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop discovery: %w", err))
		}
	}

	// Remove network.
	if f.Network != nil {
		if err := f.Network.Remove(ctx); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove network: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %v", errs)
	}
	return nil
}

func (f *ContainerFixture) startDiscoveryService(ctx context.Context) (testcontainers.Container, error) {
	// Get project root for building context.
	// From tests/e2e/distributed/fixtures, go up 3 levels to reach coral root.
	projectRoot, err := filepath.Abs("../../..")
	if err != nil {
		return nil, fmt.Errorf("failed to get project root: %w", err)
	}

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    projectRoot,
			Dockerfile: "cmd/discovery/Dockerfile",
		},
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{f.Network.Name},
		NetworkAliases: map[string][]string{
			f.Network.Name: {"discovery"},
		},
		WaitingFor: wait.ForHTTP("/health").WithPort("8080/tcp"),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, err
	}

	// Wait for health check.
	endpoint, err := container.Endpoint(ctx, "http")
	if err != nil {
		return nil, fmt.Errorf("failed to get discovery endpoint: %w", err)
	}

	if err := helpers.WaitForHTTPEndpoint(ctx, endpoint+"/health", 30*time.Second); err != nil {
		return nil, fmt.Errorf("discovery service health check failed: %w", err)
	}

	return container, nil
}

func (f *ContainerFixture) startColony(ctx context.Context) (testcontainers.Container, error) {
	// Get project root for building context.
	// From tests/e2e/distributed/fixtures, go up 3 levels to reach coral root.
	projectRoot, err := filepath.Abs("../../..")
	if err != nil {
		return nil, fmt.Errorf("failed to get project root: %w", err)
	}

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    projectRoot,
			Dockerfile: "Dockerfile",
		},
		Cmd: []string{
			"colony", "start",
			"--colony-id", f.ColonyID,
			"--discovery-endpoint", "http://discovery:8080",
		},
		Env: map[string]string{
			"CORAL_COLONY_SECRET": f.ColonySecret,
		},
		ExposedPorts: []string{"9000/tcp", "51820/udp"},
		Networks:     []string{f.Network.Name},
		NetworkAliases: map[string][]string{
			f.Network.Name: {"colony"},
		},
		Privileged: true, // Required for WireGuard.
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, err
	}

	// Wait for colony to be ready (check logs for "WireGuard device created").
	time.Sleep(5 * time.Second) // Give it time to initialize.

	return container, nil
}

func (f *ContainerFixture) startAgent(ctx context.Context, index int) (testcontainers.Container, error) {
	// Get project root for building context.
	// From tests/e2e/distributed/fixtures, go up 3 levels to reach coral root.
	projectRoot, err := filepath.Abs("../../..")
	if err != nil {
		return nil, fmt.Errorf("failed to get project root: %w", err)
	}

	agentID := fmt.Sprintf("agent-%d", index)

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    projectRoot,
			Dockerfile: "Dockerfile",
		},
		Cmd: []string{
			"agent", "start",
			"--agent-id", agentID,
			"--colony-id", f.ColonyID,
			"--discovery-endpoint", "http://discovery:8080",
		},
		Env: map[string]string{
			"CORAL_COLONY_SECRET": f.ColonySecret,
		},
		ExposedPorts: []string{"4317/tcp", "4318/tcp"}, // OTLP ports.
		Networks:     []string{f.Network.Name},
		NetworkAliases: map[string][]string{
			f.Network.Name: {agentID},
		},
		Privileged: true, // Required for eBPF and WireGuard.
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, err
	}

	// Wait for agent to register.
	time.Sleep(5 * time.Second)

	return container, nil
}

func (f *ContainerFixture) startOTELApp(ctx context.Context) (testcontainers.Container, error) {
	// Get project root for building context.
	// From tests/e2e/distributed/fixtures, go up 3 levels to reach coral root.
	projectRoot, err := filepath.Abs("../../..")
	if err != nil {
		return nil, fmt.Errorf("failed to get project root: %w", err)
	}

	// Assume agent-0 for OTLP endpoint.
	otlpEndpoint := "agent-0:4317"

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    projectRoot,
			Dockerfile: "tests/e2e/distributed/fixtures/apps/otel-app/Dockerfile",
		},
		Env: map[string]string{
			"OTEL_EXPORTER_OTLP_ENDPOINT": otlpEndpoint,
		},
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{f.Network.Name},
		NetworkAliases: map[string][]string{
			f.Network.Name: {"otel-app"},
		},
		WaitingFor: wait.ForHTTP("/health").WithPort("8080/tcp").WithStartupTimeout(30 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, err
	}

	return container, nil
}

func (f *ContainerFixture) startSDKApp(ctx context.Context) (testcontainers.Container, error) {
	// Get project root for building context.
	// From tests/e2e/distributed/fixtures, go up 3 levels to reach coral root.
	projectRoot, err := filepath.Abs("../../..")
	if err != nil {
		return nil, fmt.Errorf("failed to get project root: %w", err)
	}

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    projectRoot,
			Dockerfile: "tests/e2e/distributed/fixtures/apps/sdk-app/Dockerfile",
		},
		ExposedPorts: []string{"3001/tcp", "9002/tcp"},
		Networks:     []string{f.Network.Name},
		NetworkAliases: map[string][]string{
			f.Network.Name: {"sdk-app"},
		},
		WaitingFor: wait.ForHTTP("/health").WithPort(nat.Port("3001/tcp")).WithStartupTimeout(30 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, err
	}

	return container, nil
}

// GetDiscoveryEndpoint returns the HTTP endpoint for the discovery service.
func (f *ContainerFixture) GetDiscoveryEndpoint(ctx context.Context) (string, error) {
	return f.Discovery.Endpoint(ctx, "http")
}

// GetColonyEndpoint returns the HTTP endpoint for the colony.
func (f *ContainerFixture) GetColonyEndpoint(ctx context.Context) (string, error) {
	host, err := f.Colony.Host(ctx)
	if err != nil {
		return "", err
	}

	port, err := f.Colony.MappedPort(ctx, "9000/tcp")
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("http://%s:%s", host, port.Port()), nil
}

// GetAgentEndpoint returns the OTLP gRPC endpoint for an agent.
func (f *ContainerFixture) GetAgentEndpoint(ctx context.Context, index int) (string, error) {
	if index >= len(f.Agents) {
		return "", fmt.Errorf("agent index %d out of range", index)
	}

	host, err := f.Agents[index].Host(ctx)
	if err != nil {
		return "", err
	}

	port, err := f.Agents[index].MappedPort(ctx, "4317/tcp")
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s:%s", host, port.Port()), nil
}
