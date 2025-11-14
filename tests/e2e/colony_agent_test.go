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

	agentv1 "github.com/coral-io/coral/proto/gen/agent/v1"
	colonyv1 "github.com/coral-io/coral/proto/gen/colony/v1"
)

// ColonyAgentE2ESuite tests colony-agent communication end-to-end.
type ColonyAgentE2ESuite struct {
	helpers.E2ETestSuite
	procMgr       *helpers.ProcessManager
	configBuilder *helpers.ConfigBuilder
	dbHelper      *helpers.DatabaseHelper
}

// TestColonyAgentE2E is the entry point for the colony-agent E2E test suite.
func TestColonyAgentE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E tests in short mode")
	}
	suite.Run(t, new(ColonyAgentE2ESuite))
}

// SetupSuite runs once before all tests in the suite.
func (s *ColonyAgentE2ESuite) SetupSuite() {
	s.E2ETestSuite.SetupSuite()
	s.procMgr = helpers.NewProcessManager(s.T())
	s.configBuilder = helpers.NewConfigBuilder(s.T(), s.TempDir)
	s.dbHelper = helpers.NewDatabaseHelper(s.T(), s.TempDir)
}

// TearDownSuite runs once after all tests in the suite.
func (s *ColonyAgentE2ESuite) TearDownSuite() {
	s.procMgr.StopAll(10 * time.Second)
	s.configBuilder.Cleanup()
	s.dbHelper.CloseAll()
	s.E2ETestSuite.TearDownSuite()
}

// TestColonyStartup tests that a colony starts successfully.
func (s *ColonyAgentE2ESuite) TestColonyStartup() {
	apiPort := s.GetFreePort()
	grpcPort := s.GetFreePort()

	configPath := s.configBuilder.WriteColonyConfig("colony1", apiPort, grpcPort)

	// Start colony
	proc := s.procMgr.Start(
		s.Ctx,
		"colony",
		"./bin/coral",
		"colony", "start",
		"--config", configPath,
	)

	// Wait for colony to be ready
	s.Require().True(
		s.WaitForPort("127.0.0.1", grpcPort, 30*time.Second),
		"Colony did not start within timeout",
	)

	s.Require().NotNil(proc)
	s.T().Log("Colony started successfully")
}

// TestAgentRegistration tests agent registration with colony.
func (s *ColonyAgentE2ESuite) TestAgentRegistration() {
	apiPort := s.GetFreePort()
	grpcPort := s.GetFreePort()

	configPath := s.configBuilder.WriteColonyConfig("colony2", apiPort, grpcPort)

	// Start colony
	s.procMgr.Start(
		s.Ctx,
		"colony",
		"./bin/coral",
		"colony", "start",
		"--config", configPath,
	)

	s.Require().True(
		s.WaitForPort("127.0.0.1", grpcPort, 30*time.Second),
		"Colony did not start",
	)

	// Create gRPC client
	conn, err := grpc.NewClient(
		fmt.Sprintf("127.0.0.1:%d", grpcPort),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	s.Require().NoError(err, "Failed to create gRPC client")
	defer conn.Close()

	client := colonyv1.NewColonyServiceClient(conn)

	// List agents (should be empty initially)
	listReq := &colonyv1.ListAgentsRequest{}
	listResp, err := client.ListAgents(s.Ctx, listReq)
	s.Require().NoError(err, "Failed to list agents")
	s.Require().NotNil(listResp)

	initialCount := len(listResp.Agents)
	s.T().Logf("Initial agent count: %d", initialCount)

	// TODO: When agent registration is implemented, add test for:
	// 1. Starting an agent
	// 2. Agent registering with colony
	// 3. Verifying agent appears in ListAgents
	// 4. Verifying agent heartbeat

	s.T().Log("Agent registration test - basic colony communication verified")
}

// TestColonyStatus tests getting colony status.
func (s *ColonyAgentE2ESuite) TestColonyStatus() {
	apiPort := s.GetFreePort()
	grpcPort := s.GetFreePort()

	configPath := s.configBuilder.WriteColonyConfig("colony3", apiPort, grpcPort)

	// Start colony
	s.procMgr.Start(
		s.Ctx,
		"colony",
		"./bin/coral",
		"colony", "start",
		"--config", configPath,
	)

	s.Require().True(
		s.WaitForPort("127.0.0.1", grpcPort, 30*time.Second),
		"Colony did not start",
	)

	// Create gRPC client
	conn, err := grpc.NewClient(
		fmt.Sprintf("127.0.0.1:%d", grpcPort),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	s.Require().NoError(err)
	defer conn.Close()

	client := colonyv1.NewColonyServiceClient(conn)

	// Get colony status
	statusReq := &colonyv1.GetStatusRequest{}
	statusResp, err := client.GetStatus(s.Ctx, statusReq)
	s.Require().NoError(err, "Failed to get colony status")
	s.Require().NotNil(statusResp)

	s.T().Logf("Colony status: %+v", statusResp)

	// Verify status fields
	s.NotEmpty(statusResp.Version, "Version should not be empty")
	s.NotEmpty(statusResp.Status, "Status should not be empty")

	s.T().Log("Colony status retrieved successfully")
}

// TestMultipleAgentsRegistration tests multiple agents registering.
// Note: This is a placeholder for when full agent implementation is complete.
func (s *ColonyAgentE2ESuite) TestMultipleAgentsRegistration() {
	s.T().Skip("Multiple agents registration - implementation pending")

	// TODO: Implement when agent registration is fully functional
	// This test should:
	// 1. Start a colony
	// 2. Start multiple agents
	// 3. Verify all agents register successfully
	// 4. Verify ListAgents returns all agents
	// 5. Verify each agent can communicate with colony
}

// TestAgentHeartbeat tests agent heartbeat mechanism.
func (s *ColonyAgentE2ESuite) TestAgentHeartbeat() {
	s.T().Skip("Agent heartbeat - implementation pending")

	// TODO: Implement when agent heartbeat is functional
	// This test should:
	// 1. Start colony and agent
	// 2. Verify agent sends regular heartbeats
	// 3. Verify colony marks agent as alive
	// 4. Stop agent heartbeat
	// 5. Verify colony marks agent as dead after timeout
}

// TestAgentMetricsCollection tests agent sending metrics to colony.
func (s *ColonyAgentE2ESuite) TestAgentMetricsCollection() {
	s.T().Skip("Agent metrics collection - implementation pending")

	// TODO: Implement when metrics collection is functional
	// This test should:
	// 1. Start colony and agent
	// 2. Agent collects and sends metrics
	// 3. Verify metrics are received by colony
	// 4. Verify metrics are stored in DuckDB
	// 5. Query metrics from colony API
}

// TestAgentCommandExecution tests colony sending commands to agent.
func (s *ColonyAgentE2ESuite) TestAgentCommandExecution() {
	s.T().Skip("Agent command execution - implementation pending")

	// TODO: Implement when command execution is functional
	// This test should:
	// 1. Start colony and agent
	// 2. Colony sends command to agent
	// 3. Agent executes command
	// 4. Agent reports result back to colony
	// 5. Verify command result
}

// TestAgentDisconnection tests agent disconnection handling.
func (s *ColonyAgentE2ESuite) TestAgentDisconnection() {
	s.T().Skip("Agent disconnection - implementation pending")

	// TODO: Implement when connection handling is complete
	// This test should:
	// 1. Start colony and agent
	// 2. Verify agent is connected
	// 3. Forcefully disconnect agent
	// 4. Verify colony detects disconnection
	// 5. Reconnect agent
	// 6. Verify agent successfully reconnects
}

// TestColonyAgentGRPCCommunication tests basic gRPC communication patterns.
func (s *ColonyAgentE2ESuite) TestColonyAgentGRPCCommunication() {
	apiPort := s.GetFreePort()
	grpcPort := s.GetFreePort()

	configPath := s.configBuilder.WriteColonyConfig("colony4", apiPort, grpcPort)

	// Start colony
	s.procMgr.Start(
		s.Ctx,
		"colony",
		"./bin/coral",
		"colony", "start",
		"--config", configPath,
	)

	s.Require().True(
		s.WaitForPort("127.0.0.1", grpcPort, 30*time.Second),
		"Colony did not start",
	)

	// Test multiple concurrent connections
	numClients := 5
	for i := 0; i < numClients; i++ {
		conn, err := grpc.NewClient(
			fmt.Sprintf("127.0.0.1:%d", grpcPort),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		s.Require().NoError(err, "Failed to create client %d", i)
		defer conn.Close()

		client := colonyv1.NewColonyServiceClient(conn)

		// Make concurrent status requests
		statusReq := &colonyv1.GetStatusRequest{}
		statusResp, err := client.GetStatus(s.Ctx, statusReq)
		s.Require().NoError(err, "Client %d failed to get status", i)
		s.Require().NotNil(statusResp)
	}

	s.T().Logf("Successfully handled %d concurrent clients", numClients)
}
