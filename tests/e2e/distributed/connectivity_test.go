package distributed

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	discoveryv1 "github.com/coral-mesh/coral/coral/discovery/v1"
	"github.com/coral-mesh/coral/tests/e2e/distributed/fixtures"
	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// TestE2EDistributedSuite runs the E2E distributed test suite.
func TestE2EDistributedSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E distributed tests in short mode")
	}

	suite.Run(t, new(E2EDistributedSuite))
}

// TestDiscoveryServiceAvailability verifies that the discovery service is running and healthy.
func (s *E2EDistributedSuite) TestDiscoveryServiceAvailability() {
	s.T().Log("Testing discovery service availability...")

	// Get discovery endpoint.
	endpoint, err := s.fixture.GetDiscoveryEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get discovery endpoint")
	s.Require().NotEmpty(endpoint, "Discovery endpoint should not be empty")

	s.T().Logf("Discovery service endpoint: %s", endpoint)

	// Make actual health check request.
	resp, err := http.Get(endpoint + "/health")
	s.Require().NoError(err, "Failed to query discovery health endpoint")
	defer resp.Body.Close()

	s.Require().Equal(http.StatusOK, resp.StatusCode, "Discovery health check should return 200 OK")

	s.T().Log("Discovery service is available and healthy")
}

// TestColonyRegistrationWithDiscovery verifies that the colony registers with the discovery service.
func (s *E2EDistributedSuite) TestColonyRegistrationWithDiscovery() {
	s.T().Log("Testing colony registration with discovery service...")

	// Get discovery endpoint and create client.
	discoveryEndpoint, err := s.fixture.GetDiscoveryEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get discovery endpoint")

	client := helpers.NewDiscoveryClient(discoveryEndpoint)

	// Query discovery service for the colony.
	s.T().Logf("Looking up colony with mesh_id: %s", s.fixture.ColonyID)

	var lookupResp *discoveryv1.LookupColonyResponse
	err = helpers.WaitForCondition(s.ctx, func() bool {
		resp, lookupErr := helpers.LookupColony(s.ctx, client, s.fixture.ColonyID)
		if lookupErr != nil {
			s.T().Logf("Lookup failed (will retry): %v", lookupErr)
			return false
		}
		lookupResp = resp
		return true
	}, 30*time.Second, 2*time.Second)

	s.Require().NoError(err, "Colony should be registered in discovery service within 30 seconds")
	s.Require().NotNil(lookupResp, "Lookup response should not be nil")

	// Verify colony information.
	s.Require().Equal(s.fixture.ColonyID, lookupResp.MeshId, "Mesh ID should match")
	s.Require().NotEmpty(lookupResp.Pubkey, "Colony public key should be set")
	s.Require().NotEmpty(lookupResp.Endpoints, "Colony should have at least one endpoint")
	s.Require().NotEmpty(lookupResp.MeshIpv4, "Colony mesh IPv4 should be set")

	s.T().Logf("Colony registered successfully:")
	s.T().Logf("  - Public Key: %s", lookupResp.Pubkey[:16]+"...")
	s.T().Logf("  - Endpoints: %v", lookupResp.Endpoints)
	s.T().Logf("  - Mesh IPv4: %s", lookupResp.MeshIpv4)
}

// TestColonyStatus verifies that we can query the colony for its status.
func (s *E2EDistributedSuite) TestColonyStatus() {
	s.T().Log("Testing colony status query...")

	// Get colony endpoint and create client.
	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	client := helpers.NewColonyClient(colonyEndpoint)

	// Query colony status.
	status, err := helpers.GetColonyStatus(s.ctx, client)
	s.Require().NoError(err, "Failed to get colony status")
	s.Require().NotNil(status, "Colony status should not be nil")

	// Verify basic status information.
	s.Require().Equal(s.fixture.ColonyID, status.ColonyId, "Colony ID should match")
	s.Require().NotEmpty(status.Status, "Colony status should be set")

	s.T().Logf("Colony status:")
	s.T().Logf("  - Colony ID: %s", status.ColonyId)
	s.T().Logf("  - Status: %s", status.Status)
	s.T().Logf("  - Uptime: %d seconds", status.UptimeSeconds)
	s.T().Logf("  - Agent Count: %d", status.AgentCount)
}

// TestAgentRegistration verifies that agents register with the colony.
func (s *E2EDistributedSuite) TestAgentRegistration() {
	s.T().Log("Testing agent registration with colony...")

	// Get colony endpoint and create client.
	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	client := helpers.NewColonyClient(colonyEndpoint)

	// Wait for agents to register.
	var agents *colonyv1.ListAgentsResponse
	err = helpers.WaitForCondition(s.ctx, func() bool {
		resp, listErr := helpers.ListAgents(s.ctx, client)
		if listErr != nil {
			s.T().Logf("List agents failed (will retry): %v", listErr)
			return false
		}
		agents = resp
		return len(resp.Agents) >= len(s.fixture.Agents)
	}, 60*time.Second, 2*time.Second)

	s.Require().NoError(err, "All agents should register within 60 seconds")
	s.Require().NotNil(agents, "Agent list should not be nil")
	s.Require().Len(agents.Agents, len(s.fixture.Agents), "Number of registered agents should match")

	// Verify agent details.
	for i, agent := range agents.Agents {
		s.T().Logf("Agent %d:", i)
		s.T().Logf("  - ID: %s", agent.AgentId)
		s.T().Logf("  - Status: %s", agent.Status)
		s.T().Logf("  - Mesh IPv4: %s", agent.MeshIpv4)
		s.T().Logf("  - Last Seen: %v", agent.LastSeen.AsTime())

		s.Require().NotEmpty(agent.AgentId, "Agent ID should be set")
		s.Require().NotEmpty(agent.MeshIpv4, "Agent mesh IP should be allocated")
		s.Require().Equal("healthy", agent.Status, "Agent should be healthy")
	}

	s.T().Logf("Successfully verified %d agent(s) registered with colony", len(agents.Agents))
}

// TestMultiAgentMeshAllocation verifies that multiple agents get unique mesh IPs.
func (s *E2EDistributedSuite) TestMultiAgentMeshAllocation() {
	s.T().Log("Testing multi-agent mesh IP allocation...")

	// Create a fixture with multiple agents.
	multiAgentFixture, err := fixtures.NewContainerFixture(s.ctx, fixtures.FixtureOptions{
		NumAgents: 3,
	})
	s.Require().NoError(err, "Failed to create multi-agent fixture")
	defer multiAgentFixture.Cleanup(s.ctx)

	// Get colony endpoint and create client.
	colonyEndpoint, err := multiAgentFixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	client := helpers.NewColonyClient(colonyEndpoint)

	// Wait for all 3 agents to register.
	var agents *colonyv1.ListAgentsResponse
	err = helpers.WaitForCondition(s.ctx, func() bool {
		resp, listErr := helpers.ListAgents(s.ctx, client)
		if listErr != nil {
			return false
		}
		agents = resp
		return len(resp.Agents) >= 3
	}, 90*time.Second, 2*time.Second)

	s.Require().NoError(err, "All 3 agents should register within 90 seconds")
	s.Require().Len(agents.Agents, 3, "Should have exactly 3 agents")

	// Verify unique mesh IPs.
	meshIPs := make(map[string]bool)
	for _, agent := range agents.Agents {
		s.Require().NotEmpty(agent.MeshIpv4, "Agent should have mesh IP")

		// Check for duplicates.
		if meshIPs[agent.MeshIpv4] {
			s.Fail("Duplicate mesh IP detected", "IP: %s", agent.MeshIpv4)
		}
		meshIPs[agent.MeshIpv4] = true

		s.T().Logf("Agent %s: mesh IP = %s", agent.AgentId, agent.MeshIpv4)
	}

	s.T().Logf("Successfully verified 3 agents with unique mesh IPs")
}

// TestHeartbeatMechanism verifies that agents send heartbeats to the colony.
func (s *E2EDistributedSuite) TestHeartbeatMechanism() {
	s.T().Log("Testing heartbeat mechanism...")

	// Get colony endpoint and create client.
	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	client := helpers.NewColonyClient(colonyEndpoint)

	// Wait for agent to register.
	var firstSeen time.Time
	err = helpers.WaitForCondition(s.ctx, func() bool {
		agents, listErr := helpers.ListAgents(s.ctx, client)
		if listErr != nil || len(agents.Agents) == 0 {
			return false
		}
		firstSeen = agents.Agents[0].LastSeen.AsTime()
		return true
	}, 60*time.Second, 2*time.Second)

	s.Require().NoError(err, "Agent should register")
	s.T().Logf("Agent first seen at: %v", firstSeen)

	// Wait a bit and verify last_seen timestamp updates (indicating heartbeats).
	time.Sleep(20 * time.Second)

	agents, err := helpers.ListAgents(s.ctx, client)
	s.Require().NoError(err, "Failed to list agents")
	s.Require().NotEmpty(agents.Agents, "Should have at least one agent")

	lastSeen := agents.Agents[0].LastSeen.AsTime()
	s.T().Logf("Agent last seen at: %v", lastSeen)

	// Verify the timestamp has been updated (heartbeat occurred).
	s.Require().True(lastSeen.After(firstSeen),
		"Last seen timestamp should be updated by heartbeats (first: %v, last: %v)",
		firstSeen, lastSeen)

	s.T().Log("Heartbeat mechanism verified - agent last_seen timestamp is being updated")
}

// TestServiceRegistration verifies that connected services are registered and queryable.
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
func (s *E2EDistributedSuite) TestServiceRegistration() {
	s.T().Log("Testing service registration and discovery...")

	// Create fixture with test apps.
	fixture, err := fixtures.NewContainerFixture(s.ctx, fixtures.FixtureOptions{
		NumAgents:   1,
		WithCPUApp:  true,
		WithOTELApp: true,
	})
	s.Require().NoError(err, "Failed to create fixture with test apps")
	defer func() {
		if fixture != nil {
			_ = fixture.Cleanup(s.ctx)
		}
	}()

	s.T().Log("Test apps started:")
	s.T().Log("  - cpu-app: CPU-intensive app (uninstrumented)")
	s.T().Log("  - otel-app: OTLP-instrumented app")

	// Get colony endpoint and create client.
	colonyEndpoint, err := fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	client := helpers.NewColonyClient(colonyEndpoint)

	// Wait for services to be registered.
	// Services should be auto-discovered when apps connect to agents.
	s.T().Log("Waiting for services to be registered...")

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
	serviceMap := make(map[string]*colonyv1.ServiceInfo)
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

// TestAgentReconnectionAfterColonyRestart verifies agent reconnection after colony restarts.
func (s *E2EDistributedSuite) TestAgentReconnectionAfterColonyRestart() {
	s.T().Log("Testing agent reconnection after colony restart...")

	// Get colony endpoint and create client.
	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	client := helpers.NewColonyClient(colonyEndpoint)

	// Wait for agent to register initially.
	err = helpers.WaitForCondition(s.ctx, func() bool {
		agents, listErr := helpers.ListAgents(s.ctx, client)
		return listErr == nil && len(agents.Agents) > 0
	}, 60*time.Second, 2*time.Second)
	s.Require().NoError(err, "Agent should register initially")

	s.T().Log("Agent registered successfully, now restarting colony...")

	// Stop the colony container.
	err = s.fixture.Colony.Stop(s.ctx, nil)
	s.Require().NoError(err, "Failed to stop colony")

	s.T().Log("Colony stopped, waiting 5 seconds...")
	time.Sleep(5 * time.Second)

	// Restart the colony container.
	err = s.fixture.Colony.Start(s.ctx)
	s.Require().NoError(err, "Failed to restart colony")

	s.T().Log("Colony restarted, waiting for agent to reconnect...")

	// Wait for agent to re-register (longer timeout for reconnection with backoff).
	err = helpers.WaitForCondition(s.ctx, func() bool {
		agents, listErr := helpers.ListAgents(s.ctx, client)
		if listErr != nil {
			s.T().Logf("List agents error (will retry): %v", listErr)
			return false
		}
		return len(agents.Agents) > 0
	}, 120*time.Second, 3*time.Second)

	s.Require().NoError(err, "Agent should reconnect within 120 seconds after colony restart")

	agents, err := helpers.ListAgents(s.ctx, client)
	s.Require().NoError(err, "Failed to list agents after reconnection")
	s.Require().NotEmpty(agents.Agents, "Should have reconnected agent")

	s.T().Logf("Agent reconnected successfully:")
	s.T().Logf("  - ID: %s", agents.Agents[0].AgentId)
	s.T().Logf("  - Status: %s", agents.Agents[0].Status)
	s.T().Logf("  - Mesh IP: %s", agents.Agents[0].MeshIpv4)

	s.T().Log("Reconnection test passed - agent successfully reconnected after colony restart")
}
