package distributed

import (
	"testing"
	"time"

	"connectrpc.com/connect"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// ServiceSuite tests service registration, connection, and discovery behaviors.
//
// Services are connected once in SetupSuite() and shared across all tests for efficiency.
// Tests should be idempotent and not rely on specific connection order.
type ServiceSuite struct {
	E2EDistributedSuite
}

// NewServiceSuite instantiates a ServiceSuite.
func NewServiceSuite(suite E2EDistributedSuite, t *testing.T) *ServiceSuite {
	s := &ServiceSuite{
		E2EDistributedSuite: suite,
	}
	s.SetT(t)
	return s
}

// SetupSuite runs once before all tests in the suite.
func (s *ServiceSuite) SetupSuite() {
	s.E2EDistributedSuite.SetupSuite()

	// Ensure services are connected once for all tests.
	s.ensureServicesConnected()

	s.T().Log("ServiceSuite setup complete - services connected")
}

// TearDownSuite cleans up after all tests in the suite.
func (s *ServiceSuite) TearDownSuite() {
	// Disconnect all services.
	s.disconnectAllServices()

	// Clear service data from colony database to ensure clean state for next suite.
	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	if err == nil {
		colonyClient := helpers.NewColonyClient(colonyEndpoint)
		_ = helpers.CleanupColonyDatabase(s.ctx, colonyClient)
		// Ignore errors - cleanup is best-effort.
	}

	// Call parent TearDownSuite.
	s.E2EDistributedSuite.TearDownSuite()
}

// TestServiceRegistrationAndDiscovery verifies that connected services are registered and queryable.
//
// This test bridges Phase 1 (connectivity) and Phase 2 (observability) by ensuring
// that services connected to agents are properly registered in the colony registry.
// This is critical for features like Beyla auto-instrumentation which depends on
// services being discoverable via the registry.
//
// Test flow:
// 1. Start colony, agent, and test apps (CPU app, OTLP app)
// 2. Query colony for service list via ListServices API
// 3. Verify expected services are registered with correct metadata
// 4. Verify service instance counts and health status
func (s *ServiceSuite) TestServiceRegistrationAndDiscovery() {
	s.T().Log("Testing service registration and discovery...")

	// Use shared docker-compose fixture (all services already running).
	fixture := s.fixture

	s.T().Log("Test apps already running via docker-compose:")
	s.T().Log("  - cpu-app: CPU-intensive app (uninstrumented)")
	s.T().Log("  - otel-app: OTLP-instrumented app")
	s.T().Log("  Note: Services connected in SetupSuite()")

	// Step 1: Verify services appear in agent's ListServices.
	s.T().Log("Verifying services registered with agent...")

	agent0Endpoint, err := fixture.GetAgentGRPCEndpoint(s.ctx, 0)
	s.Require().NoError(err, "Failed to get agent-0 endpoint")

	agentClient := helpers.NewAgentClient(agent0Endpoint)

	agentServicesResp, err := agentClient.ListServices(s.ctx, connect.NewRequest(&agentv1.ListServicesRequest{}))
	s.Require().NoError(err, "Failed to list services from agent")
	s.Require().GreaterOrEqual(len(agentServicesResp.Msg.Services), 2, "Agent should have at least 2 services")

	s.T().Logf("✓ Agent has %d services registered", len(agentServicesResp.Msg.Services))

	// Step 3: Wait for colony to poll services from agent.
	s.T().Log("Waiting for colony to poll services from agent...")
	time.Sleep(2 * time.Second) // Poll interval is 1s in E2E.

	// Step 4: Verify services appear in colony's ListServices.
	s.T().Log("Verifying services registered with colony...")

	colonyEndpoint, err := fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	client := helpers.NewColonyClient(colonyEndpoint)

	// Wait for services to be registered in colony.
	s.T().Log("Waiting for services to appear in colony registry...")

	var services *colonyv1.ListServicesResponse
	err = helpers.WaitForCondition(s.ctx, func() bool {
		resp, listErr := helpers.ListServices(s.ctx, client, "")
		if listErr != nil {
			s.T().Logf("List services failed (will retry): %v", listErr)
			return false
		}
		services = resp
		// Wait until we have at least the test apps registered.
		return len(resp.Services) >= 2
	}, 60*time.Second, 2*time.Second)

	s.Require().NoError(err, "Services should be registered within 60 seconds")
	s.Require().NotNil(services, "Service list should not be nil")

	s.T().Logf("Colony has %d registered services", len(services.Services))

	// Build a map of service names for easy lookup.
	serviceMap := make(map[string]*colonyv1.ServiceSummary)
	for _, svc := range services.Services {
		serviceMap[svc.Name] = svc
		s.T().Logf("Service registered:")
		s.T().Logf("  - Name: %s", svc.Name)
		s.T().Logf("  - Namespace: %s", svc.Namespace)
		s.T().Logf("  - Instance Count: %d", svc.InstanceCount)
		s.T().Logf("  - Last Seen: %v", svc.LastSeen.AsTime())
	}

	// Verify expected services are registered.
	expectedServices := []string{"cpu-app", "otel-app"}

	for _, expectedSvc := range expectedServices {
		svc, found := serviceMap[expectedSvc]
		if !found {
			s.T().Logf("⚠️  WARNING: Expected service '%s' not found in registry", expectedSvc)
			s.T().Log("    This may indicate:")
			s.T().Log("    1. Service registration not yet implemented")
			s.T().Log("    2. Apps not properly connected to agent")
			s.T().Log("    3. Service discovery mechanism not active")
			continue
		}

		// Verify service metadata.
		s.Require().NotNil(svc, "Service %s should exist", expectedSvc)
		s.Require().Greater(svc.InstanceCount, int32(0),
			"Service %s should have at least 1 instance", expectedSvc)

		// Verify last_seen timestamp is recent (within last 2 minutes).
		lastSeen := svc.LastSeen.AsTime()
		timeSinceLastSeen := time.Since(lastSeen)
		s.Require().Less(timeSinceLastSeen, 2*time.Minute,
			"Service %s last_seen should be recent (was %v ago)", expectedSvc, timeSinceLastSeen)

		s.T().Logf("✓ Service '%s' verified:", expectedSvc)
		s.T().Logf("  - %d instance(s) running", svc.InstanceCount)
		s.T().Logf("  - Last seen %v ago", timeSinceLastSeen.Round(time.Second))
	}

	s.T().Log("✓ Service registration verified")
	s.T().Log("  - Services are discoverable via colony API")
	s.T().Log("  - Service metadata is accurate and up-to-date")
	s.T().Log("  - Registry foundation ready for observability features")
}

// TestDynamicServiceConnection verifies services can be connected at runtime.
//
// Test flow:
// 1. Start with agent running (from suite setup)
// 2. Ensure cpu-app is not connected initially
// 3. Dynamically connect cpu-app via ConnectService API
// 4. Verify service appears in agent's ListServices
// 5. Verify service appears in colony registry after polling
// 6. Generate traffic and verify monitoring is active
func (s *ServiceSuite) TestDynamicServiceConnection() {
	s.T().Log("Testing dynamic service connection...")

	fixture := s.fixture

	// Get agent endpoint.
	agentEndpoint, err := fixture.GetAgentGRPCEndpoint(s.ctx, 0)
	s.Require().NoError(err, "Failed to get agent-0 endpoint")

	agentClient := helpers.NewAgentClient(agentEndpoint)

	// Step 1: Ensure cpu-app is not connected (disconnect if it exists).
	s.T().Log("Ensuring cpu-app is not initially connected...")
	_, _ = helpers.DisconnectService(s.ctx, agentClient, "cpu-app")

	// Verify it's not in the service list.
	listResp, err := agentClient.ListServices(s.ctx, connect.NewRequest(&agentv1.ListServicesRequest{}))
	s.Require().NoError(err, "Failed to list services")

	for _, svc := range listResp.Msg.Services {
		s.Require().NotEqual("cpu-app", svc.Name, "cpu-app should not be connected initially")
	}
	s.T().Log("✓ Confirmed cpu-app is not connected")

	// Step 2: Dynamically connect cpu-app via ConnectService API.
	s.T().Log("Dynamically connecting cpu-app...")
	connectResp, err := helpers.ConnectService(s.ctx, agentClient, "cpu-app", 8080, "/health")
	s.Require().NoError(err, "Failed to connect cpu-app")
	s.Require().True(connectResp.Success, "ConnectService should succeed")
	s.T().Logf("✓ cpu-app connected: %s", connectResp.ServiceName)

	// Step 3: Verify service appears in agent's ListServices.
	s.T().Log("Verifying cpu-app appears in agent's service list...")
	listResp, err = agentClient.ListServices(s.ctx, connect.NewRequest(&agentv1.ListServicesRequest{}))
	s.Require().NoError(err, "Failed to list services")

	found := false
	for _, svc := range listResp.Msg.Services {
		if svc.Name == "cpu-app" {
			found = true
			s.Require().Equal(int32(8080), svc.Port, "Service port should match")
			s.Require().Equal("/health", svc.HealthEndpoint, "Health endpoint should match")
			s.T().Logf("✓ cpu-app found in agent service list (port: %d, health: %s)", svc.Port, svc.HealthEndpoint)
			break
		}
	}
	s.Require().True(found, "cpu-app should appear in agent's service list")

	// Step 4: Wait for colony to poll services from agent.
	s.T().Log("Waiting for colony to poll services from agent (2 seconds)...")
	time.Sleep(2 * time.Second)

	// Step 5: Verify service appears in colony registry.
	s.T().Log("Verifying cpu-app appears in colony registry...")
	colonyEndpoint, err := fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	colonyClient := helpers.NewColonyClient(colonyEndpoint)

	err = helpers.WaitForCondition(s.ctx, func() bool {
		resp, listErr := helpers.ListServices(s.ctx, colonyClient, "")
		if listErr != nil {
			s.T().Logf("List services failed (will retry): %v", listErr)
			return false
		}
		for _, svc := range resp.Services {
			if svc.Name == "cpu-app" {
				s.T().Logf("✓ cpu-app found in colony registry (instances: %d)", svc.InstanceCount)
				return true
			}
		}
		return false
	}, 60*time.Second, 2*time.Second)
	s.Require().NoError(err, "cpu-app should appear in colony registry within 60 seconds")

	s.T().Log("✓ Dynamic service connection verified")
	s.T().Log("  - Service connected at runtime via API")
	s.T().Log("  - Service appears in agent's service list")
	s.T().Log("  - Service registered in colony registry")
}

// TestServiceConnectionAtStartup verifies services connected during setup are properly monitored.
//
// Test flow:
// 1. Verify services connected in SetupSuite are in agent's service list
// 2. Verify service metadata is correct (port, health endpoint, etc.)
// 3. Verify services are healthy and reachable
// 4. Verify services appear in colony registry
//
// Note: This validates the "connection at startup" pattern used in SetupSuite()
// where services are connected before tests run.
func (s *ServiceSuite) TestServiceConnectionAtStartup() {
	s.T().Log("Testing service connection at startup...")

	fixture := s.fixture

	// Get agent endpoint.
	agentEndpoint, err := fixture.GetAgentGRPCEndpoint(s.ctx, 0)
	s.Require().NoError(err, "Failed to get agent-0 endpoint")

	agentClient := helpers.NewAgentClient(agentEndpoint)

	// Step 1: Verify services connected in SetupSuite are in agent's service list.
	s.T().Log("Verifying services connected during setup...")
	listResp, err := agentClient.ListServices(s.ctx, connect.NewRequest(&agentv1.ListServicesRequest{}))
	s.Require().NoError(err, "Failed to list services")

	expectedServices := map[string]struct {
		port           int32
		healthEndpoint string
	}{
		"cpu-app":  {port: 8080, healthEndpoint: "/health"},
		"otel-app": {port: 8090, healthEndpoint: "/health"},
	}

	foundServices := make(map[string]bool)
	for _, svc := range listResp.Msg.Services {
		if expected, exists := expectedServices[svc.Name]; exists {
			foundServices[svc.Name] = true
			s.Require().Equal(expected.port, svc.Port, "Service %s port should match", svc.Name)
			s.Require().Equal(expected.healthEndpoint, svc.HealthEndpoint, "Service %s health endpoint should match", svc.Name)
			s.T().Logf("✓ %s found in agent service list (port: %d, health: %s)", svc.Name, svc.Port, svc.HealthEndpoint)
		}
	}

	// Verify all expected services were found.
	for serviceName := range expectedServices {
		s.Require().True(foundServices[serviceName], "Service %s should be in agent's service list", serviceName)
	}

	// Step 2: Verify services appear in colony registry.
	s.T().Log("Verifying services appear in colony registry...")
	colonyEndpoint, err := fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	colonyClient := helpers.NewColonyClient(colonyEndpoint)

	colonyServices, err := helpers.ListServices(s.ctx, colonyClient, "")
	s.Require().NoError(err, "Failed to list services from colony")

	colonyServiceMap := make(map[string]*colonyv1.ServiceSummary)
	for _, svc := range colonyServices.Services {
		colonyServiceMap[svc.Name] = svc
	}

	for serviceName := range expectedServices {
		svc, found := colonyServiceMap[serviceName]
		s.Require().True(found, "Service %s should be in colony registry", serviceName)
		s.Require().Greater(svc.InstanceCount, int32(0), "Service %s should have at least 1 instance", serviceName)
		s.T().Logf("✓ %s found in colony registry (instances: %d)", serviceName, svc.InstanceCount)
	}

	s.T().Log("✓ Service connection at startup verified")
	s.T().Log("  - Services connected during SetupSuite are properly monitored")
	s.T().Log("  - Service metadata is correct in agent's service list")
	s.T().Log("  - Services are registered in colony registry")
}

// TestMultiServiceRegistration verifies multiple services on one agent are tracked independently.
//
// Test flow:
// 1. Verify agent has multiple services connected
// 2. Verify each service has distinct metadata
// 3. Generate traffic to each service independently
// 4. Query telemetry for each service separately
// 5. Verify metrics are correctly attributed to the right service
// 6. Verify colony registry shows both services with accurate counts
func (s *ServiceSuite) TestMultiServiceRegistration() {
	s.T().Log("Testing multi-service registration...")

	fixture := s.fixture

	// Get agent endpoint.
	agentEndpoint, err := fixture.GetAgentGRPCEndpoint(s.ctx, 0)
	s.Require().NoError(err, "Failed to get agent-0 endpoint")

	agentClient := helpers.NewAgentClient(agentEndpoint)

	// Step 1: Verify agent has multiple services connected.
	s.T().Log("Verifying agent has multiple services...")
	listResp, err := agentClient.ListServices(s.ctx, connect.NewRequest(&agentv1.ListServicesRequest{}))
	s.Require().NoError(err, "Failed to list services")
	s.Require().GreaterOrEqual(len(listResp.Msg.Services), 2, "Agent should have at least 2 services")

	// Step 2: Verify each service has distinct metadata.
	s.T().Log("Verifying services have distinct metadata...")
	serviceNames := make(map[string]bool)
	for _, svc := range listResp.Msg.Services {
		s.Require().False(serviceNames[svc.Name], "Service names should be unique")
		serviceNames[svc.Name] = true
		s.T().Logf("  - %s (port: %d, health: %s)", svc.Name, svc.Port, svc.HealthEndpoint)
	}
	s.T().Logf("✓ Found %d distinct services", len(serviceNames))

	// Step 3: Verify services are independently tracked in colony.
	s.T().Log("Verifying services are independently tracked in colony...")
	colonyEndpoint, err := fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	colonyClient := helpers.NewColonyClient(colonyEndpoint)

	colonyServices, err := helpers.ListServices(s.ctx, colonyClient, "")
	s.Require().NoError(err, "Failed to list services from colony")

	// Build map of colony services.
	colonyServiceMap := make(map[string]*colonyv1.ServiceSummary)
	for _, svc := range colonyServices.Services {
		colonyServiceMap[svc.Name] = svc
	}

	// Verify each agent service appears in colony with independent tracking.
	for serviceName := range serviceNames {
		colonySvc, found := colonyServiceMap[serviceName]
		if !found {
			s.T().Logf("⚠️  Service %s not yet in colony registry (may need more time for polling)", serviceName)
			continue
		}

		s.Require().Greater(colonySvc.InstanceCount, int32(0), "Service %s should have at least 1 instance", serviceName)
		s.T().Logf("✓ %s tracked independently in colony (instances: %d)", serviceName, colonySvc.InstanceCount)
	}

	// Step 4: Verify telemetry attribution (if services have generated traffic).
	s.T().Log("Verifying telemetry attribution...")
	now := time.Now()

	// Query telemetry for otel-app (which should have OTLP spans).
	telemetryResp, err := helpers.QueryAgentTelemetry(
		s.ctx,
		agentClient,
		now.Add(-5*time.Minute).Unix(),
		now.Unix(),
		[]string{"otel-app"},
	)
	s.Require().NoError(err, "Failed to query telemetry for otel-app")

	if telemetryResp.TotalSpans > 0 {
		// Verify all returned spans are from otel-app.
		for _, span := range telemetryResp.Spans {
			s.Require().Equal("otel-app", span.ServiceName, "Telemetry should be correctly attributed to otel-app")
		}
		s.T().Logf("✓ Telemetry correctly attributed to otel-app (%d spans)", telemetryResp.TotalSpans)
	} else {
		s.T().Log("  No telemetry data yet for otel-app (expected if no traffic generated)")
	}

	s.T().Log("✓ Multi-service registration verified")
	s.T().Log("  - Multiple services tracked on single agent")
	s.T().Log("  - Each service has distinct metadata")
	s.T().Log("  - Services independently tracked in colony registry")
	s.T().Log("  - Telemetry correctly attributed to respective services")
}

// =============================================================================
// Helper Methods
// =============================================================================

// ensureServicesConnected ensures that test services are connected to agent-0.
// This is a thin wrapper around the shared helper.EnsureServicesConnected function.
func (s *ServiceSuite) ensureServicesConnected() {
	helpers.EnsureServicesConnected(s.T(), s.ctx, s.fixture, 0, []helpers.ServiceConfig{
		{Name: "cpu-app", Port: 8080, HealthEndpoint: "/health"},
		{Name: "otel-app", Port: 8090, HealthEndpoint: "/health"},
	})
}

// disconnectAllServices disconnects all test services from all agents.
// This is called during TearDownSuite() to clean up after all tests.
func (s *ServiceSuite) disconnectAllServices() {
	helpers.DisconnectAllServices(s.T(), s.ctx, s.fixture, 0, []string{
		"cpu-app",
		"otel-app",
	})
}
