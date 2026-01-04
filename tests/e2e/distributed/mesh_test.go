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

// MeshSuite tests WireGuard mesh connectivity, agent registration, and heartbeat mechanisms.
//
// This suite covers the foundation of Coral's distributed architecture:
// - Discovery service registration and lookup
// - Colony and agent mesh establishment
// - WireGuard mesh IP allocation
// - Agent registration and heartbeat
// - Reconnection resilience
type MeshSuite struct {
	E2EDistributedSuite
}

// TestMeshSuite runs the mesh connectivity test suite.
func TestMeshSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping mesh tests in short mode")
	}

	suite.Run(t, new(MeshSuite))
}

// TestDiscoveryServiceAvailability verifies that the discovery service is running and healthy.
func (s *MeshSuite) TestDiscoveryServiceAvailability() {
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

// TestColonyRegistration verifies that the colony registers with the discovery service.
func (s *MeshSuite) TestColonyRegistration() {
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
func (s *MeshSuite) TestColonyStatus() {
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
func (s *MeshSuite) TestAgentRegistration() {
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
		return len(resp.Agents) >= 1
	}, 60*time.Second, 2*time.Second)

	s.Require().NoError(err, "All agents should register within 60 seconds")
	s.Require().NotNil(agents, "Agent list should not be nil")
	s.Require().GreaterOrEqual(len(agents.Agents), 1, "Should have at least one agent")

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

// TestMultiAgentMesh verifies that multiple agents get unique mesh IPs.
func (s *MeshSuite) TestMultiAgentMesh() {
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

// TestHeartbeat verifies that agents send heartbeats to the colony.
func (s *MeshSuite) TestHeartbeat() {
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

// TestAgentReconnection verifies agent reconnection after colony restarts.
func (s *MeshSuite) TestAgentReconnection() {
	s.T().Skip("SKIPPED: Cannot restart colony with docker-compose (would need docker-compose restart)")

	// Note: With docker-compose, we can't stop/start individual containers from within tests.
	// To test reconnection, we would need to:
	// 1. Run `docker-compose restart colony` externally
	// 2. Or use testcontainers (which we removed for performance)
	// 3. Or add a separate test script that uses docker-compose CLI
	//
	// For now, we rely on the agent's reconnection logic being tested in unit tests.
}
