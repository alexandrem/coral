package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/coral-io/coral/tests/helpers"
	"github.com/stretchr/testify/suite"

	"connectrpc.com/connect"
	discoveryv1 "github.com/coral-io/coral/coral/discovery/v1"
	"github.com/coral-io/coral/coral/discovery/v1/discoveryv1connect"
)

// DiscoveryE2ESuite tests the discovery service end-to-end.
type DiscoveryE2ESuite struct {
	helpers.E2ETestSuite
	procMgr       *helpers.ProcessManager
	configBuilder *helpers.ConfigBuilder
}

// TestDiscoveryE2E is the entry point for the discovery E2E test suite.
func TestDiscoveryE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E tests in short mode")
	}
	suite.Run(t, new(DiscoveryE2ESuite))
}

// SetupSuite runs once before all tests in the suite.
func (s *DiscoveryE2ESuite) SetupSuite() {
	s.E2ETestSuite.SetupSuite()
	s.procMgr = helpers.NewProcessManager(s.T())
	s.configBuilder = helpers.NewConfigBuilder(s.T(), s.TempDir)
}

// TearDownSuite runs once after all tests in the suite.
func (s *DiscoveryE2ESuite) TearDownSuite() {
	s.procMgr.StopAll(10 * time.Second)
	s.configBuilder.Cleanup()
	s.E2ETestSuite.TearDownSuite()
}

// TestDiscoveryServiceStartup tests that the discovery service starts successfully.
func (s *DiscoveryE2ESuite) TestDiscoveryServiceStartup() {
	grpcPort := s.GetFreePort()

	// Start discovery service
	proc := s.procMgr.Start(
		s.Ctx,
		"discovery",
		"./bin/coral-discovery",
		"--port", fmt.Sprintf("%d", grpcPort),
		"--ttl", "300",
	)

	// Wait for service to be ready
	s.Require().True(
		s.WaitForPort("127.0.0.1", grpcPort, 30*time.Second),
		"Discovery service did not start within timeout",
	)

	// Verify process is still running
	s.Require().NotNil(proc)

	s.T().Log("Discovery service started successfully")
}

// TestHealthCheck tests the discovery service health endpoint.
func (s *DiscoveryE2ESuite) TestHealthCheck() {
	grpcPort := s.GetFreePort()

	// Start discovery service
	s.procMgr.Start(
		s.Ctx,
		"discovery",
		"./bin/coral-discovery",
		"--port", fmt.Sprintf("%d", grpcPort),
		"--ttl", "60",
	)

	s.Require().True(
		s.WaitForPort("127.0.0.1", grpcPort, 30*time.Second),
		"Discovery service did not start",
	)

	// Create Connect client
	httpClient := connect.NewClient[discoveryv1.HealthRequest, discoveryv1.HealthResponse](
		nil,
		fmt.Sprintf("http://127.0.0.1:%d/coral.discovery.v1.DiscoveryService/Health", grpcPort),
	)

	// Call health check
	resp, err := httpClient.CallUnary(s.Ctx, connect.NewRequest(&discoveryv1.HealthRequest{}))
	s.Require().NoError(err, "Health check failed")
	s.Require().NotNil(resp.Msg)

	// Verify health response
	s.NotEmpty(resp.Msg.Status, "Status should not be empty")
	s.NotEmpty(resp.Msg.Version, "Version should not be empty")
	s.GreaterOrEqual(resp.Msg.UptimeSeconds, int64(0), "Uptime should be non-negative")
	s.Equal(int32(0), resp.Msg.RegisteredColonies, "Should have no colonies initially")

	s.T().Logf("Health check successful: status=%s, version=%s, uptime=%ds",
		resp.Msg.Status, resp.Msg.Version, resp.Msg.UptimeSeconds)
}

// TestColonyRegistration tests colony registration and lookup.
func (s *DiscoveryE2ESuite) TestColonyRegistration() {
	grpcPort := s.GetFreePort()

	// Start discovery service
	s.procMgr.Start(
		s.Ctx,
		"discovery",
		"./bin/coral-discovery",
		"--port", fmt.Sprintf("%d", grpcPort),
		"--ttl", "60",
	)

	s.Require().True(
		s.WaitForPort("127.0.0.1", grpcPort, 30*time.Second),
		"Discovery service did not start",
	)

	// Create Connect client
	client := discoveryv1connect.NewDiscoveryServiceClient(
		nil,
		fmt.Sprintf("http://127.0.0.1:%d", grpcPort),
	)

	// Register a colony
	registerReq := &discoveryv1.RegisterColonyRequest{
		MeshId:      "test-mesh-1",
		Pubkey:      "test-public-key-1",
		Endpoints:   []string{"192.168.1.100:51820"},
		MeshIpv4:    "10.42.0.1",
		MeshIpv6:    "fd42::1",
		ConnectPort: 9000,
		Metadata:    map[string]string{"env": "test"},
	}

	registerResp, err := client.RegisterColony(s.Ctx, connect.NewRequest(registerReq))
	s.Require().NoError(err, "Failed to register colony")
	s.Require().NotNil(registerResp.Msg)
	s.True(registerResp.Msg.Success, "Registration should succeed")
	s.Greater(registerResp.Msg.Ttl, int32(0), "TTL should be positive")
	s.NotNil(registerResp.Msg.ExpiresAt, "Expiration time should be set")

	s.T().Logf("Registered colony: mesh_id=%s, ttl=%d", registerReq.MeshId, registerResp.Msg.Ttl)

	// Lookup the colony
	lookupReq := &discoveryv1.LookupColonyRequest{
		MeshId: "test-mesh-1",
	}

	lookupResp, err := client.LookupColony(s.Ctx, connect.NewRequest(lookupReq))
	s.Require().NoError(err, "Failed to lookup colony")
	s.Require().NotNil(lookupResp.Msg)

	// Verify colony data matches
	s.Equal("test-mesh-1", lookupResp.Msg.MeshId)
	s.Equal("test-public-key-1", lookupResp.Msg.Pubkey)
	s.Equal([]string{"192.168.1.100:51820"}, lookupResp.Msg.Endpoints)
	s.Equal("10.42.0.1", lookupResp.Msg.MeshIpv4)
	s.Equal("fd42::1", lookupResp.Msg.MeshIpv6)
	s.Equal(uint32(9000), lookupResp.Msg.ConnectPort)
	s.Equal("test", lookupResp.Msg.Metadata["env"])
	s.NotNil(lookupResp.Msg.LastSeen, "LastSeen should be set")

	s.T().Log("Colony lookup successful")
}

// TestAgentRegistration tests agent registration and lookup.
func (s *DiscoveryE2ESuite) TestAgentRegistration() {
	grpcPort := s.GetFreePort()

	s.procMgr.Start(
		s.Ctx,
		"discovery",
		"./bin/coral-discovery",
		"--port", fmt.Sprintf("%d", grpcPort),
		"--ttl", "60",
	)

	s.Require().True(
		s.WaitForPort("127.0.0.1", grpcPort, 30*time.Second),
		"Discovery service did not start",
	)

	client := discoveryv1connect.NewDiscoveryServiceClient(
		nil,
		fmt.Sprintf("http://127.0.0.1:%d", grpcPort),
	)

	// Register a colony first
	colonyReq := &discoveryv1.RegisterColonyRequest{
		MeshId:      "test-mesh-2",
		Pubkey:      "colony-pubkey",
		Endpoints:   []string{"192.168.1.1:51820"},
		MeshIpv4:    "10.42.0.1",
		ConnectPort: 9000,
	}
	_, err := client.RegisterColony(s.Ctx, connect.NewRequest(colonyReq))
	s.Require().NoError(err)

	// Register an agent
	agentReq := &discoveryv1.RegisterAgentRequest{
		AgentId:   "agent-1",
		MeshId:    "test-mesh-2",
		Pubkey:    "agent-pubkey-1",
		Endpoints: []string{"192.168.1.100:51820"},
		Metadata:  map[string]string{"type": "linux"},
	}

	agentResp, err := client.RegisterAgent(s.Ctx, connect.NewRequest(agentReq))
	s.Require().NoError(err, "Failed to register agent")
	s.Require().NotNil(agentResp.Msg)
	s.True(agentResp.Msg.Success, "Agent registration should succeed")
	s.Greater(agentResp.Msg.Ttl, int32(0), "TTL should be positive")

	s.T().Logf("Registered agent: agent_id=%s, mesh_id=%s", agentReq.AgentId, agentReq.MeshId)

	// Lookup the agent
	lookupReq := &discoveryv1.LookupAgentRequest{
		AgentId: "agent-1",
	}

	lookupResp, err := client.LookupAgent(s.Ctx, connect.NewRequest(lookupReq))
	s.Require().NoError(err, "Failed to lookup agent")
	s.Require().NotNil(lookupResp.Msg)

	// Verify agent data
	s.Equal("agent-1", lookupResp.Msg.AgentId)
	s.Equal("test-mesh-2", lookupResp.Msg.MeshId)
	s.Equal("agent-pubkey-1", lookupResp.Msg.Pubkey)
	s.Equal([]string{"192.168.1.100:51820"}, lookupResp.Msg.Endpoints)
	s.Equal("linux", lookupResp.Msg.Metadata["type"])
	s.NotNil(lookupResp.Msg.LastSeen)

	s.T().Log("Agent registration and lookup successful")
}

// TestMultipleColonies tests registration of multiple colonies.
func (s *DiscoveryE2ESuite) TestMultipleColonies() {
	grpcPort := s.GetFreePort()

	s.procMgr.Start(
		s.Ctx,
		"discovery",
		"./bin/coral-discovery",
		"--port", fmt.Sprintf("%d", grpcPort),
		"--ttl", "60",
	)

	s.Require().True(
		s.WaitForPort("127.0.0.1", grpcPort, 30*time.Second),
		"Discovery service did not start",
	)

	client := discoveryv1connect.NewDiscoveryServiceClient(
		nil,
		fmt.Sprintf("http://127.0.0.1:%d", grpcPort),
	)

	// Register multiple colonies
	numColonies := 5
	for i := 0; i < numColonies; i++ {
		registerReq := &discoveryv1.RegisterColonyRequest{
			MeshId:      fmt.Sprintf("mesh-%d", i),
			Pubkey:      fmt.Sprintf("pubkey-%d", i),
			Endpoints:   []string{fmt.Sprintf("192.168.1.%d:51820", 100+i)},
			MeshIpv4:    fmt.Sprintf("10.42.0.%d", i+1),
			ConnectPort: uint32(9000 + i),
		}

		resp, err := client.RegisterColony(s.Ctx, connect.NewRequest(registerReq))
		s.Require().NoError(err, "Failed to register colony %d", i)
		s.True(resp.Msg.Success)
	}

	s.T().Logf("Registered %d colonies", numColonies)

	// Verify each colony can be looked up
	for i := 0; i < numColonies; i++ {
		lookupReq := &discoveryv1.LookupColonyRequest{
			MeshId: fmt.Sprintf("mesh-%d", i),
		}

		lookupResp, err := client.LookupColony(s.Ctx, connect.NewRequest(lookupReq))
		s.Require().NoError(err, "Failed to lookup colony %d", i)
		s.Equal(fmt.Sprintf("mesh-%d", i), lookupResp.Msg.MeshId)
	}

	// Verify health check shows correct colony count
	healthClient := connect.NewClient[discoveryv1.HealthRequest, discoveryv1.HealthResponse](
		nil,
		fmt.Sprintf("http://127.0.0.1:%d/coral.discovery.v1.DiscoveryService/Health", grpcPort),
	)

	healthResp, err := healthClient.CallUnary(s.Ctx, connect.NewRequest(&discoveryv1.HealthRequest{}))
	s.Require().NoError(err)
	s.Equal(int32(numColonies), healthResp.Msg.RegisteredColonies, "Health check should show %d colonies", numColonies)

	s.T().Log("Multiple colony registration and lookup successful")
}

// TestColonyUpdate tests updating an existing colony registration.
func (s *DiscoveryE2ESuite) TestColonyUpdate() {
	grpcPort := s.GetFreePort()

	s.procMgr.Start(
		s.Ctx,
		"discovery",
		"./bin/coral-discovery",
		"--port", fmt.Sprintf("%d", grpcPort),
		"--ttl", "60",
	)

	s.Require().True(s.WaitForPort("127.0.0.1", grpcPort, 30*time.Second))

	client := discoveryv1connect.NewDiscoveryServiceClient(
		nil,
		fmt.Sprintf("http://127.0.0.1:%d", grpcPort),
	)

	// Initial registration
	initialReq := &discoveryv1.RegisterColonyRequest{
		MeshId:      "update-mesh",
		Pubkey:      "pubkey-v1",
		Endpoints:   []string{"192.168.1.1:51820"},
		MeshIpv4:    "10.42.0.1",
		ConnectPort: 9000,
		Metadata:    map[string]string{"version": "1.0"},
	}

	_, err := client.RegisterColony(s.Ctx, connect.NewRequest(initialReq))
	s.Require().NoError(err)

	// Update registration with new endpoints
	updateReq := &discoveryv1.RegisterColonyRequest{
		MeshId:      "update-mesh",
		Pubkey:      "pubkey-v2",
		Endpoints:   []string{"192.168.1.1:51820", "10.0.0.1:51820"},
		MeshIpv4:    "10.42.0.1",
		ConnectPort: 9000,
		Metadata:    map[string]string{"version": "2.0"},
	}

	_, err = client.RegisterColony(s.Ctx, connect.NewRequest(updateReq))
	s.Require().NoError(err)

	// Lookup and verify updated data
	lookupReq := &discoveryv1.LookupColonyRequest{MeshId: "update-mesh"}
	lookupResp, err := client.LookupColony(s.Ctx, connect.NewRequest(lookupReq))
	s.Require().NoError(err)

	s.Equal("pubkey-v2", lookupResp.Msg.Pubkey, "Public key should be updated")
	s.Equal(2, len(lookupResp.Msg.Endpoints), "Should have 2 endpoints")
	s.Equal("2.0", lookupResp.Msg.Metadata["version"], "Metadata should be updated")

	s.T().Log("Colony update successful")
}

// TestRelayRequest tests relay allocation request.
func (s *DiscoveryE2ESuite) TestRelayRequest() {
	grpcPort := s.GetFreePort()

	s.procMgr.Start(
		s.Ctx,
		"discovery",
		"./bin/coral-discovery",
		"--port", fmt.Sprintf("%d", grpcPort),
		"--ttl", "60",
	)

	s.Require().True(s.WaitForPort("127.0.0.1", grpcPort, 30*time.Second))

	client := discoveryv1connect.NewDiscoveryServiceClient(
		nil,
		fmt.Sprintf("http://127.0.0.1:%d", grpcPort),
	)

	// Request a relay allocation
	relayReq := &discoveryv1.RequestRelayRequest{
		MeshId:       "relay-mesh",
		AgentPubkey:  "agent-pubkey",
		ColonyPubkey: "colony-pubkey",
	}

	relayResp, err := client.RequestRelay(s.Ctx, connect.NewRequest(relayReq))
	s.Require().NoError(err, "Failed to request relay")
	s.Require().NotNil(relayResp.Msg)

	// Verify relay response
	s.NotNil(relayResp.Msg.RelayEndpoint, "Relay endpoint should be set")
	s.NotEmpty(relayResp.Msg.SessionId, "Session ID should be set")
	s.NotEmpty(relayResp.Msg.RelayId, "Relay ID should be set")
	s.NotNil(relayResp.Msg.ExpiresAt, "Expiration should be set")

	s.T().Logf("Relay allocated: relay_id=%s, session_id=%s, endpoint=%s:%d",
		relayResp.Msg.RelayId,
		relayResp.Msg.SessionId,
		relayResp.Msg.RelayEndpoint.Ip,
		relayResp.Msg.RelayEndpoint.Port)

	// Release the relay
	releaseReq := &discoveryv1.ReleaseRelayRequest{
		SessionId: relayResp.Msg.SessionId,
		MeshId:    "relay-mesh",
	}

	releaseResp, err := client.ReleaseRelay(s.Ctx, connect.NewRequest(releaseReq))
	s.Require().NoError(err, "Failed to release relay")
	s.True(releaseResp.Msg.Success, "Relay release should succeed")

	s.T().Log("Relay request and release successful")
}

// TestTTLExpiration tests that registrations expire after TTL.
func (s *DiscoveryE2ESuite) TestTTLExpiration() {
	grpcPort := s.GetFreePort()

	// Start discovery with very short TTL (5 seconds)
	s.procMgr.Start(
		s.Ctx,
		"discovery",
		"./bin/coral-discovery",
		"--port", fmt.Sprintf("%d", grpcPort),
		"--ttl", "5",
	)

	s.Require().True(s.WaitForPort("127.0.0.1", grpcPort, 30*time.Second))

	client := discoveryv1connect.NewDiscoveryServiceClient(
		nil,
		fmt.Sprintf("http://127.0.0.1:%d", grpcPort),
	)

	// Register a colony
	registerReq := &discoveryv1.RegisterColonyRequest{
		MeshId:      "ttl-mesh",
		Pubkey:      "ttl-pubkey",
		Endpoints:   []string{"192.168.1.1:51820"},
		MeshIpv4:    "10.42.0.1",
		ConnectPort: 9000,
	}

	_, err := client.RegisterColony(s.Ctx, connect.NewRequest(registerReq))
	s.Require().NoError(err)

	// Verify it exists
	lookupReq := &discoveryv1.LookupColonyRequest{MeshId: "ttl-mesh"}
	_, err = client.LookupColony(s.Ctx, connect.NewRequest(lookupReq))
	s.Require().NoError(err, "Colony should exist immediately after registration")

	// Wait for TTL + cleanup interval (5s + buffer)
	s.T().Log("Waiting for TTL expiration...")
	time.Sleep(8 * time.Second)

	// Verify it no longer exists
	_, err = client.LookupColony(s.Ctx, connect.NewRequest(lookupReq))
	s.Require().Error(err, "Colony should have expired")

	s.T().Log("TTL expiration verified")
}
