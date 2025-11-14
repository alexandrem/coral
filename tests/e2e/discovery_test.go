package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/coral-io/coral/tests/helpers"
	"github.com/stretchr/testify/suite"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	discoveryv1 "github.com/coral-io/coral/proto/gen/discovery/v1"
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
	stunPort := s.GetFreePort()

	configPath := s.configBuilder.WriteDiscoveryConfig("discovery1", grpcPort, stunPort)

	// Start discovery service
	proc := s.procMgr.Start(
		s.Ctx,
		"discovery",
		"./bin/coral-discovery",
		"start",
		"--config", configPath,
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

// TestPeerRegistration tests peer registration and discovery.
func (s *DiscoveryE2ESuite) TestPeerRegistration() {
	grpcPort := s.GetFreePort()
	stunPort := s.GetFreePort()

	configPath := s.configBuilder.WriteDiscoveryConfig("discovery2", grpcPort, stunPort)

	// Start discovery service
	s.procMgr.Start(
		s.Ctx,
		"discovery",
		"./bin/coral-discovery",
		"start",
		"--config", configPath,
	)

	// Wait for service to be ready
	s.Require().True(
		s.WaitForPort("127.0.0.1", grpcPort, 30*time.Second),
		"Discovery service did not start",
	)

	// Create gRPC client
	conn, err := grpc.NewClient(
		fmt.Sprintf("127.0.0.1:%d", grpcPort),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	s.Require().NoError(err, "Failed to create gRPC client")
	defer conn.Close()

	client := discoveryv1.NewDiscoveryServiceClient(conn)

	// Register a peer
	registerReq := &discoveryv1.RegisterPeerRequest{
		PeerId:     "peer-1",
		PublicKey:  "test-public-key-1",
		Endpoints:  []string{"192.168.1.100:51820"},
		Metadata:   map[string]string{"type": "agent"},
		TtlSeconds: 60,
	}

	registerResp, err := client.RegisterPeer(s.Ctx, registerReq)
	s.Require().NoError(err, "Failed to register peer")
	s.Require().NotNil(registerResp)

	s.T().Logf("Registered peer: %s", registerReq.PeerId)

	// Discover peers
	discoverReq := &discoveryv1.DiscoverPeersRequest{
		PeerId: "peer-2",
	}

	discoverResp, err := client.DiscoverPeers(s.Ctx, discoverReq)
	s.Require().NoError(err, "Failed to discover peers")
	s.Require().NotNil(discoverResp)

	// Should find the registered peer
	s.Require().GreaterOrEqual(len(discoverResp.Peers), 1, "Expected at least 1 peer")

	found := false
	for _, peer := range discoverResp.Peers {
		if peer.PeerId == "peer-1" {
			found = true
			s.Equal("test-public-key-1", peer.PublicKey)
			s.Equal([]string{"192.168.1.100:51820"}, peer.Endpoints)
			break
		}
	}
	s.Require().True(found, "Registered peer not found in discovery")

	s.T().Log("Peer discovery successful")
}

// TestPeerHeartbeat tests peer heartbeat and TTL expiration.
func (s *DiscoveryE2ESuite) TestPeerHeartbeat() {
	grpcPort := s.GetFreePort()
	stunPort := s.GetFreePort()

	configPath := s.configBuilder.WriteDiscoveryConfig("discovery3", grpcPort, stunPort)

	// Start discovery service
	s.procMgr.Start(
		s.Ctx,
		"discovery",
		"./bin/coral-discovery",
		"start",
		"--config", configPath,
	)

	s.Require().True(
		s.WaitForPort("127.0.0.1", grpcPort, 30*time.Second),
		"Discovery service did not start",
	)

	// Create gRPC client
	conn, err := grpc.NewClient(
		fmt.Sprintf("127.0.0.1:%d", grpcPort),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	s.Require().NoError(err, "Failed to create gRPC client")
	defer conn.Close()

	client := discoveryv1.NewDiscoveryServiceClient(conn)

	// Register peer with short TTL
	registerReq := &discoveryv1.RegisterPeerRequest{
		PeerId:     "peer-heartbeat",
		PublicKey:  "test-public-key-hb",
		Endpoints:  []string{"192.168.1.200:51820"},
		TtlSeconds: 5, // 5 second TTL
	}

	_, err = client.RegisterPeer(s.Ctx, registerReq)
	s.Require().NoError(err, "Failed to register peer")

	// Send heartbeat
	heartbeatReq := &discoveryv1.HeartbeatRequest{
		PeerId: "peer-heartbeat",
	}

	heartbeatResp, err := client.Heartbeat(s.Ctx, heartbeatReq)
	s.Require().NoError(err, "Failed to send heartbeat")
	s.Require().NotNil(heartbeatResp)

	s.T().Log("Heartbeat sent successfully")

	// Verify peer is still registered
	discoverReq := &discoveryv1.DiscoverPeersRequest{
		PeerId: "peer-other",
	}

	discoverResp, err := client.DiscoverPeers(s.Ctx, discoverReq)
	s.Require().NoError(err)

	found := false
	for _, peer := range discoverResp.Peers {
		if peer.PeerId == "peer-heartbeat" {
			found = true
			break
		}
	}
	s.Require().True(found, "Peer should still be registered after heartbeat")
}

// TestMultiplePeers tests registration of multiple peers.
func (s *DiscoveryE2ESuite) TestMultiplePeers() {
	grpcPort := s.GetFreePort()
	stunPort := s.GetFreePort()

	configPath := s.configBuilder.WriteDiscoveryConfig("discovery4", grpcPort, stunPort)

	// Start discovery service
	s.procMgr.Start(
		s.Ctx,
		"discovery",
		"./bin/coral-discovery",
		"start",
		"--config", configPath,
	)

	s.Require().True(
		s.WaitForPort("127.0.0.1", grpcPort, 30*time.Second),
		"Discovery service did not start",
	)

	// Create gRPC client
	conn, err := grpc.NewClient(
		fmt.Sprintf("127.0.0.1:%d", grpcPort),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	s.Require().NoError(err)
	defer conn.Close()

	client := discoveryv1.NewDiscoveryServiceClient(conn)

	// Register multiple peers
	numPeers := 5
	for i := 0; i < numPeers; i++ {
		registerReq := &discoveryv1.RegisterPeerRequest{
			PeerId:     fmt.Sprintf("peer-%d", i),
			PublicKey:  fmt.Sprintf("pubkey-%d", i),
			Endpoints:  []string{fmt.Sprintf("192.168.1.%d:51820", 100+i)},
			TtlSeconds: 60,
		}

		_, err = client.RegisterPeer(s.Ctx, registerReq)
		s.Require().NoError(err, "Failed to register peer %d", i)
	}

	s.T().Logf("Registered %d peers", numPeers)

	// Discover all peers
	discoverReq := &discoveryv1.DiscoverPeersRequest{
		PeerId: "observer",
	}

	discoverResp, err := client.DiscoverPeers(s.Ctx, discoverReq)
	s.Require().NoError(err)

	s.Require().GreaterOrEqual(
		len(discoverResp.Peers),
		numPeers,
		"Expected to discover at least %d peers", numPeers,
	)

	s.T().Log("Multiple peer discovery successful")
}

// TestSTUNEndpointDiscovery tests STUN-based endpoint discovery.
// Note: This is a placeholder for when STUN implementation is complete.
func (s *DiscoveryE2ESuite) TestSTUNEndpointDiscovery() {
	s.T().Skip("STUN endpoint discovery - implementation pending")

	// TODO: Implement when STUN server is fully functional
	// This test should:
	// 1. Start discovery service with STUN enabled
	// 2. Make STUN binding request
	// 3. Verify public IP/port is returned
	// 4. Verify NAT type detection works
}
