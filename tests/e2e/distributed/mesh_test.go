package distributed

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	discoveryv1 "github.com/coral-mesh/coral/coral/discovery/v1"
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

// TestAgentMeshConfiguration verifies that agents properly configure WireGuard mesh after registration.
// This test queries the agent's /status endpoint to validate mesh setup.
func (s *MeshSuite) TestAgentMeshConfiguration() {
	s.T().Log("Testing agent WireGuard mesh configuration...")

	// Get agent-0 endpoint.
	agent0Endpoint, err := s.fixture.GetAgentGRPCEndpoint(s.ctx, 0)
	s.Require().NoError(err, "Failed to get agent-0 endpoint")

	// Query agent /status endpoint.
	statusURL := agent0Endpoint + "/status"
	s.T().Logf("Querying agent status: %s", statusURL)

	var statusResp map[string]interface{}
	err = helpers.WaitForCondition(s.ctx, func() bool {
		resp, fetchErr := http.Get(statusURL)
		if fetchErr != nil {
			s.T().Logf("Failed to fetch status (will retry): %v", fetchErr)
			return false
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			s.T().Logf("Status endpoint returned %d (will retry)", resp.StatusCode)
			return false
		}

		if err := json.NewDecoder(resp.Body).Decode(&statusResp); err != nil {
			s.T().Logf("Failed to decode status JSON (will retry): %v", err)
			return false
		}

		// Check if mesh is configured.
		mesh, ok := statusResp["mesh"].(map[string]interface{})
		if !ok {
			return false
		}

		meshIP, _ := mesh["mesh_ip"].(string)
		return meshIP != "" // Wait until mesh IP is set
	}, 90*time.Second, 2*time.Second)

	s.Require().NoError(err, "Agent should configure mesh within 90 seconds")
	s.Require().NotNil(statusResp, "Status response should not be nil")

	// Validate mesh configuration.
	mesh, ok := statusResp["mesh"].(map[string]interface{})
	s.Require().True(ok, "Status should include mesh section")

	meshIP, _ := mesh["mesh_ip"].(string)
	meshSubnet, _ := mesh["mesh_subnet"].(string)
	s.Require().NotEmpty(meshIP, "Agent should have mesh IP configured")
	s.Require().NotEmpty(meshSubnet, "Agent should have mesh subnet configured")

	s.T().Logf("✓ Mesh IP configured: %s", meshIP)
	s.T().Logf("✓ Mesh subnet configured: %s", meshSubnet)

	// Validate WireGuard configuration.
	wg, ok := mesh["wireguard"].(map[string]interface{})
	s.Require().True(ok, "Status should include wireguard section")

	interfaceExists, _ := wg["interface_exists"].(bool)
	peerCount, _ := wg["peer_count"].(float64) // JSON numbers are float64
	linkStatus, _ := wg["link_status"].(string)

	s.Require().True(interfaceExists, "WireGuard interface should exist")
	s.Require().Greater(int(peerCount), 0, "Agent should have at least 1 WireGuard peer (colony)")
	s.Require().Contains(linkStatus, "UP", "WireGuard interface should be UP")

	s.T().Logf("✓ WireGuard interface: UP")
	s.T().Logf("✓ WireGuard peers: %d", int(peerCount))
	s.T().Log("✓ Agent mesh configuration validated successfully")
}

// TestMultiAgentMesh verifies that multiple agents get unique mesh IPs.
func (s *MeshSuite) TestMultiAgentMesh() {
	s.T().Log("Testing multi-agent mesh IP allocation...")

	// Get colony endpoint and create client.
	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	client := helpers.NewColonyClient(colonyEndpoint)

	// Wait for all expected agents (from docker-compose) to register.
	// docker-compose up starts 2 agents by default.
	expectedAgents := 2
	var agents *colonyv1.ListAgentsResponse
	err = helpers.WaitForCondition(s.ctx, func() bool {
		resp, listErr := helpers.ListAgents(s.ctx, client)
		if listErr != nil {
			return false
		}
		agents = resp
		return len(resp.Agents) >= expectedAgents
	}, 90*time.Second, 2*time.Second)

	s.Require().NoError(err, "All agents should register within 90 seconds")
	s.Require().GreaterOrEqual(len(agents.Agents), expectedAgents, "Should have at least %d agents", expectedAgents)

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

	s.T().Logf("Successfully verified agents have unique mesh IPs")
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
// TestAgentReconnection verifies agent reconnection after colony restarts.
func (s *MeshSuite) TestAgentReconnection() {
	s.T().Log("Testing agent reconnection logic...")

	// 1. Verify agents are connected initially.
	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")
	client := helpers.NewColonyClient(colonyEndpoint)

	agents, err := helpers.ListAgents(s.ctx, client)
	s.Require().NoError(err, "Failed to list agents before restart")
	s.Require().GreaterOrEqual(len(agents.Agents), 1, "Should have agents connected")

	s.T().Logf("Verified %d agents connected before restart", len(agents.Agents))

	// 2. Restart colony service.
	s.T().Log("Restarting colony service...")
	err = s.fixture.RestartService(s.ctx, "colony")
	s.Require().NoError(err, "Failed to restart colony")

	// 3. Wait for colony to be healthy again.
	s.T().Log("Waiting for colony to recover...")
	// Wait for discovery to register it again (can take up to a minute).
	discoveryEndpoint, err := s.fixture.GetDiscoveryEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get discovery endpoint")
	discoveryClient := helpers.NewDiscoveryClient(discoveryEndpoint)

	err = helpers.WaitForCondition(s.ctx, func() bool {
		resp, lookupErr := helpers.LookupColony(s.ctx, discoveryClient, s.fixture.ColonyID)
		if lookupErr != nil {
			return false
		}
		// Also verify we can talk to the colony directly
		status, statusErr := helpers.GetColonyStatus(s.ctx, client)
		return resp != nil && statusErr == nil && status.Status == "running"
	}, 60*time.Second, 2*time.Second)
	s.Require().NoError(err, "Colony should recover after restart")

	s.T().Log("Colony has recovered")

	// 4. Wait for agents to reconnect.
	s.T().Log("Waiting for agents to reconnect...")
	err = helpers.WaitForCondition(s.ctx, func() bool {
		agentsResp, listErr := helpers.ListAgents(s.ctx, client)
		if listErr != nil {
			return false
		}
		// We expect the original agent to be present.
		// If it's a new ID (due to restart), we should just check we have ANY agents.
		// Real agents persist their ID, so it should be the same.
		return len(agentsResp.Agents) >= 1
	}, 90*time.Second, 2*time.Second)

	s.Require().NoError(err, "Agents should reconnect after colony restart")

	// Double check agent status
	agents, err = helpers.ListAgents(s.ctx, client)
	s.Require().NoError(err)
	s.T().Logf("Successfully verified %d agents reconnected", len(agents.Agents))
}
