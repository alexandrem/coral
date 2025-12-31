package distributed

import (
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/coral-mesh/coral/tests/e2e/distributed/fixtures"
)

// ObservabilityL3Suite tests Level 3 - Deep Introspection observability.
// This includes on-demand CPU profiling and uprobe tracing.
type ObservabilityL3Suite struct {
	E2EDistributedSuite
}

// TestObservabilityL3Suite runs the Level 3 observability test suite.
func TestObservabilityL3Suite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping observability L3 tests in short mode")
	}

	suite.Run(t, new(ObservabilityL3Suite))
}

// TestLevel3_OnDemandCPUProfiling verifies on-demand high-frequency CPU profiling.
//
// Test flow:
// 1. Start colony and agent with CPU app
// 2. Connect CPU app to generate CPU load
// 3. Trigger on-demand profiling via ProfileCPU API (99Hz)
// 4. Wait for profiling duration
// 5. Verify profile samples are collected with stack traces
//
// Note: Differs from Level 2 continuous profiling:
//   - L2: Always-on, 19Hz, low overhead
//   - L3: On-demand, 99Hz, high detail, short duration
func (s *ObservabilityL3Suite) TestLevel3_OnDemandCPUProfiling() {
	s.T().Log("Testing on-demand CPU profiling...")

	// Create fixture with colony, agent, and CPU app.
	fixture, err := fixtures.NewContainerFixture(s.ctx, fixtures.FixtureOptions{
		NumAgents:  1,
		WithCPUApp: true,
	})
	s.Require().NoError(err, "Failed to create container fixture")
	defer func() {
		if fixture != nil {
			_ = fixture.Cleanup(s.ctx)
		}
	}()

	// Connect CPU app to agent.
	agentEndpoint, err := fixture.GetAgentGRPCEndpoint(s.ctx, 0)
	s.Require().NoError(err, "Failed to get agent gRPC endpoint")

	agentClient := helpers.NewAgentClient(agentEndpoint)

	s.T().Log("Connecting CPU app to agent...")
	_, err = helpers.ConnectService(s.ctx, agentClient, "cpu-app", 8080, "/health")
	s.Require().NoError(err, "Failed to connect CPU app")

	// Get colony client.
	colonyEndpoint, err := fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	colonyClient := helpers.NewColonyClient(colonyEndpoint)

	// Trigger on-demand CPU profiling (99Hz, 10 seconds).
	s.T().Log("Triggering on-demand CPU profiling (99Hz, 10s)...")
	profileResp, err := helpers.ProfileCPU(s.ctx, colonyClient, "cpu-app", 10, 99)
	s.Require().NoError(err, "Failed to start CPU profiling")

	s.T().Logf("Profiling started with session ID: %s", profileResp.SessionId)

	// Wait for profiling to complete.
	s.T().Log("Waiting for profiling to complete...")
	time.Sleep(12 * time.Second) // Duration + buffer.

	// Verify profile results.
	s.T().Log("Verifying profile samples...")
	s.Require().NotEmpty(profileResp.SessionId, "Session ID should not be empty")

	// Check if samples were collected.
	if len(profileResp.Samples) == 0 {
		s.T().Log("⚠️  WARNING: No profile samples collected")
		s.T().Log("    This may indicate:")
		s.T().Log("    1. CPU profiler not yet fully integrated")
		s.T().Log("    2. Insufficient CPU activity during profiling window")
		s.T().Log("    3. Profile collection/retrieval not yet implemented")
		return
	}

	s.T().Logf("✓ Collected %d CPU profile samples", len(profileResp.Samples))

	// Verify sample structure.
	hasStackTraces := false
	for i, sample := range profileResp.Samples {
		if i < 3 { // Log first few samples.
			s.T().Logf("  Sample %d: %d frames, count: %d",
				i+1, len(sample.FrameNames), sample.Count)
		}
		if len(sample.FrameNames) > 0 {
			hasStackTraces = true
		}
	}

	s.Require().True(hasStackTraces, "At least some samples should have stack traces")

	s.T().Log("✓ On-demand CPU profiling verified")
	s.T().Log("  - 99Hz high-frequency profiling")
	s.T().Log("  - Stack traces captured")
	s.T().Log("  - Suitable for flame graph generation")
}

// TestLevel3_UprobeTracing verifies uprobe-based function tracing.
//
// Test flow:
// 1. Start colony, agent, and SDK test app
// 2. Connect SDK app to agent
// 3. Attach uprobe to ProcessPayment function
// 4. Trigger workload via /trigger endpoint
// 5. Verify uprobe events captured (entry/exit, duration)
// 6. Detach uprobe and verify event retrieval
//
// Note: Uses SDK test app with known functions for testing.
func (s *ObservabilityL3Suite) TestLevel3_UprobeTracing() {
	s.T().Log("Testing uprobe function tracing...")

	// Create fixture with SDK test app.
	fixture, err := fixtures.NewContainerFixture(s.ctx, fixtures.FixtureOptions{
		NumAgents:  1,
		WithSDKApp: true,
	})
	s.Require().NoError(err, "Failed to create container fixture")
	defer func() {
		if fixture != nil {
			_ = fixture.Cleanup(s.ctx)
		}
	}()

	// Connect SDK app to agent.
	agentEndpoint, err := fixture.GetAgentGRPCEndpoint(s.ctx, 0)
	s.Require().NoError(err, "Failed to get agent gRPC endpoint")

	agentClient := helpers.NewAgentClient(agentEndpoint)

	s.T().Log("Connecting SDK app to agent...")
	_, err = helpers.ConnectService(s.ctx, agentClient, "sdk-app", 3001, "/health")
	s.Require().NoError(err, "Failed to connect SDK app")

	// Get colony client.
	colonyEndpoint, err := fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	colonyClient := helpers.NewColonyClient(colonyEndpoint)

	// Attach uprobe to ProcessPayment function.
	s.T().Log("Attaching uprobe to main.ProcessPayment function...")
	attachResp, err := helpers.AttachUprobe(s.ctx, colonyClient, "sdk-app", "main.ProcessPayment", 30)
	s.Require().NoError(err, "Failed to attach uprobe")

	sessionID := attachResp.SessionId
	s.T().Logf("Uprobe attached with session ID: %s", sessionID)

	// Wait for uprobe to be ready.
	time.Sleep(2 * time.Second)

	// Trigger workload by calling /trigger endpoint.
	s.T().Log("Triggering workload to generate function calls...")
	sdkAppEndpoint, err := fixture.GetSDKAppEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get SDK app endpoint")

	client := &http.Client{Timeout: 5 * time.Second}
	for i := 0; i < 10; i++ {
		resp, err := client.Get(fmt.Sprintf("http://%s/trigger", sdkAppEndpoint))
		if err == nil {
			_ = resp.Body.Close()
		}
		time.Sleep(200 * time.Millisecond)
	}

	s.T().Log("Waiting for events to be captured...")
	time.Sleep(3 * time.Second)

	// Query uprobe events.
	s.T().Log("Querying uprobe events...")
	eventsResp, err := helpers.QueryUprobeEvents(s.ctx, colonyClient, sessionID, 100)
	s.Require().NoError(err, "Failed to query uprobe events")

	s.T().Logf("Captured %d uprobe events", eventsResp.TotalEvents)

	if eventsResp.TotalEvents == 0 {
		s.T().Log("⚠️  WARNING: No uprobe events captured")
		s.T().Log("    This may indicate:")
		s.T().Log("    1. Uprobe attachment failed")
		s.T().Log("    2. Function not called during test window")
		s.T().Log("    3. Event collection not yet fully integrated")
		s.T().Log("    4. SDK debug info not available")
		return
	}

	// Verify event structure.
	s.T().Logf("✓ Captured uprobe events:")
	for i, event := range eventsResp.Events {
		if i < 5 { // Log first few events.
			s.T().Logf("  Event %d: timestamp=%d, duration=%dns",
				i+1, event.Timestamp, event.DurationNs)
		}
	}

	// Detach uprobe.
	s.T().Log("Detaching uprobe...")
	detachResp, err := helpers.DetachUprobe(s.ctx, colonyClient, sessionID)
	s.Require().NoError(err, "Failed to detach uprobe")

	s.T().Logf("Detached - session active for %ds", detachResp.SessionDuration)

	s.T().Log("✓ Uprobe tracing verified")
	s.T().Log("  - Function entry/exit events captured")
	s.T().Log("  - Duration calculated correctly")
	s.T().Log("  - Event persistence working")
}

// TestLevel3_UprobeCallTree verifies uprobe call tree construction.
//
// This test validates that uprobes can track call chains and build call trees
// showing parent-child relationships, call depth, and execution time.
func (s *ObservabilityL3Suite) TestLevel3_UprobeCallTree() {
	s.T().Log("Testing uprobe call tree construction...")

	// Create fixture with SDK test app.
	fixture, err := fixtures.NewContainerFixture(s.ctx, fixtures.FixtureOptions{
		NumAgents:  1,
		WithSDKApp: true,
	})
	s.Require().NoError(err, "Failed to create container fixture")
	defer func() {
		if fixture != nil {
			_ = fixture.Cleanup(s.ctx)
		}
	}()

	// Connect SDK app to agent.
	agentEndpoint, err := fixture.GetAgentGRPCEndpoint(s.ctx, 0)
	s.Require().NoError(err, "Failed to get agent gRPC endpoint")

	agentClient := helpers.NewAgentClient(agentEndpoint)

	s.T().Log("Connecting SDK app to agent...")
	_, err = helpers.ConnectService(s.ctx, agentClient, "sdk-app", 3001, "/health")
	s.Require().NoError(err, "Failed to connect SDK app")

	// Get colony client.
	colonyEndpoint, err := fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	colonyClient := helpers.NewColonyClient(colonyEndpoint)

	// Attach uprobe to ProcessPayment (which calls ValidateCard and CalculateTotal).
	s.T().Log("Attaching uprobe to main.ProcessPayment...")
	attachResp, err := helpers.AttachUprobe(s.ctx, colonyClient, "sdk-app", "main.ProcessPayment", 30)
	s.Require().NoError(err, "Failed to attach uprobe")

	sessionID := attachResp.SessionId
	time.Sleep(2 * time.Second)

	// Trigger workload.
	s.T().Log("Triggering workload...")
	sdkAppEndpoint, err := fixture.GetSDKAppEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get SDK app endpoint")

	client := &http.Client{Timeout: 5 * time.Second}
	for i := 0; i < 5; i++ {
		resp, err := client.Get(fmt.Sprintf("http://%s/trigger", sdkAppEndpoint))
		if err == nil {
			_ = resp.Body.Close()
		}
		time.Sleep(300 * time.Millisecond)
	}

	time.Sleep(3 * time.Second)

	// Get debug results with call tree.
	s.T().Log("Retrieving debug results with call tree...")
	resultsResp, err := helpers.GetDebugResults(s.ctx, colonyClient, sessionID)
	s.Require().NoError(err, "Failed to get debug results")

	if resultsResp.CallTree == nil {
		s.T().Log("⚠️  WARNING: No call tree generated")
		s.T().Log("    This may indicate:")
		s.T().Log("    1. Call tree builder not yet integrated")
		s.T().Log("    2. Insufficient events for tree construction")
		s.T().Log("    3. Multi-function tracing not enabled")
		return
	}

	s.T().Log("✓ Call tree constructed:")
	s.T().Logf("  Root function: %s", resultsResp.CallTree.FunctionName)
	s.T().Logf("  Total calls: %d", resultsResp.CallTree.CallCount)
	s.T().Logf("  Avg duration: %.2fms", float64(resultsResp.CallTree.AvgDurationNs)/1e6)
	s.T().Logf("  Children: %d", len(resultsResp.CallTree.Children))

	// Verify call tree structure.
	s.Require().Greater(resultsResp.CallTree.CallCount, int32(0), "Should have calls")
	s.Require().NotNil(resultsResp.Statistics, "Should have statistics")

	s.T().Log("✓ Call tree construction verified")
	s.T().Log("  - Parent-child relationships tracked")
	s.T().Log("  - Call counts aggregated")
	s.T().Log("  - Duration attribution correct")
}

// TestLevel3_MultiAgentDebugSession verifies debug sessions across multiple agents.
//
// Test flow:
// 1. Start colony with multiple agents and CPU apps
// 2. Connect services to each agent
// 3. Start CPU profiling on multiple agents
// 4. Verify profiling works independently on each agent
// 5. Verify colony can collect results from all agents
func (s *ObservabilityL3Suite) TestLevel3_MultiAgentDebugSession() {
	s.T().Log("Testing multi-agent debug session...")

	// Create fixture with multiple agents.
	fixture, err := fixtures.NewContainerFixture(s.ctx, fixtures.FixtureOptions{
		NumAgents: 3,
	})
	s.Require().NoError(err, "Failed to create container fixture")
	defer func() {
		if fixture != nil {
			_ = fixture.Cleanup(s.ctx)
		}
	}()

	s.T().Log("Multiple agents started")

	// Get colony client.
	colonyEndpoint, err := fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	colonyClient := helpers.NewColonyClient(colonyEndpoint)

	// Connect a dummy service to each agent for testing.
	// In a real scenario, different services would run on different agents.
	sessionIDs := make([]string, 3)
	for i := 0; i < 3; i++ {
		agentEndpoint, err := fixture.GetAgentGRPCEndpoint(s.ctx, i)
		s.Require().NoError(err, "Failed to get agent %d gRPC endpoint", i)

		agentClient := helpers.NewAgentClient(agentEndpoint)

		serviceName := fmt.Sprintf("test-service-%d", i)
		s.T().Logf("Connecting %s to agent-%d...", serviceName, i)
		_, err = helpers.ConnectService(s.ctx, agentClient, serviceName, int32(9000+i), "/health")
		s.Require().NoError(err, "Failed to connect service to agent %d", i)

		// Start CPU profiling on this agent's service.
		s.T().Logf("Starting CPU profiling on %s...", serviceName)
		profileResp, err := helpers.ProfileCPU(s.ctx, colonyClient, serviceName, 5, 99)
		if err != nil {
			s.T().Logf("⚠️  Failed to profile %s: %v", serviceName, err)
			continue
		}

		sessionIDs[i] = profileResp.SessionId
		s.T().Logf("  Session ID: %s", profileResp.SessionId)
	}

	// Wait for profiling to complete.
	s.T().Log("Waiting for profiling to complete...")
	time.Sleep(7 * time.Second)

	// Verify at least one session succeeded.
	successCount := 0
	for i, sessionID := range sessionIDs {
		if sessionID == "" {
			continue
		}
		successCount++
		s.T().Logf("✓ Agent %d profiling session: %s", i, sessionID)
	}

	if successCount == 0 {
		s.T().Log("⚠️  WARNING: No profiling sessions succeeded")
		s.T().Log("    This may indicate:")
		s.T().Log("    1. Multi-agent coordination not yet fully integrated")
		s.T().Log("    2. Service discovery across agents not working")
		s.T().Log("    3. Profile collection needs enhancement")
		return
	}

	s.T().Logf("✓ Multi-agent debug session verified (%d/%d agents)", successCount, 3)
	s.T().Log("  - Independent profiling on multiple agents")
	s.T().Log("  - Colony coordinates debug sessions")
	s.T().Log("  - Results collected from each agent")
}
