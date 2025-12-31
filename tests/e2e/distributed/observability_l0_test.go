package distributed

import (
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/coral-mesh/coral/tests/e2e/distributed/fixtures"
)

// ObservabilityL0Suite tests Level 0 - Beyla eBPF Metrics observability.
// This is passive RED metrics collection via Beyla eBPF instrumentation.
type ObservabilityL0Suite struct {
	E2EDistributedSuite
}

// TestObservabilityL0Suite runs the Level 0 observability test suite.
func TestObservabilityL0Suite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping observability L0 tests in short mode")
	}

	suite.Run(t, new(ObservabilityL0Suite))
}

// TestLevel0_BeylaHTTPMetrics verifies Beyla eBPF captures HTTP metrics.
//
// Test flow:
// 1. Start agent with Beyla enabled
// 2. Start test app (HTTP server)
// 3. Generate HTTP traffic to app
// 4. Beyla captures spans via eBPF (no code instrumentation)
// 5. Verify RED metrics (Request rate, Error rate, Duration)
//
// Note: Requires Beyla binary in agent container image.
// Per user preference (PLAN.md), we run actual Beyla subprocess, not mocks.
func (s *ObservabilityL0Suite) TestLevel0_BeylaHTTPMetrics() {
	s.T().Skip("Beyla binary not yet included in agent container image")

	s.T().Log("Testing Beyla eBPF HTTP metrics collection...")

	// Create fixture with agent.
	// TODO: Add BelyaEnabled option to FixtureOptions.
	fixture, err := fixtures.NewContainerFixture(s.ctx, fixtures.FixtureOptions{
		NumAgents: 1,
		// TODO: WithBeyla: true,
	})
	s.Require().NoError(err, "Failed to create container fixture")
	defer func() {
		if fixture != nil {
			_ = fixture.Cleanup(s.ctx)
		}
	}()

	// TODO: Start HTTP test app (could use otel-app or cpu-intensive-app).

	s.T().Log("Generating HTTP traffic for Beyla to observe...")
	// TODO: Generate HTTP requests to test app.

	s.T().Log("Waiting for Beyla to capture and process spans...")
	time.Sleep(5 * time.Second)

	// TODO: Verify Beyla metrics.
	// This requires:
	// 1. Query agent's DuckDB beyla_http_metrics table
	// 2. Verify request count metrics
	// 3. Verify error rate metrics (status >= 400)
	// 4. Verify latency metrics (P50/P95/P99)
	// 5. Verify service and endpoint identification

	s.T().Log("✓ Beyla HTTP metrics test placeholder")
	s.T().Log("")
	s.T().Log("Prerequisites for Beyla tests:")
	s.T().Log("  1. Include Beyla binary in agent Docker image")
	s.T().Log("  2. Implement Beyla manager startup in agent")
	s.T().Log("  3. Configure Beyla OTLP export to agent receiver")
	s.T().Log("  4. Expose agent Beyla metrics query API")
}

// TestLevel0_BeylaColonyPolling verifies colony polls agent for Beyla metrics.
//
// Test flow:
// 1. Agent collects Beyla metrics locally
// 2. Colony polls agent via AgentService.QueryEbpfMetrics
// 3. Verify metrics aggregation in colony DuckDB
// 4. Verify 1-minute time-series buckets
//
// Note: Requires Beyla integration and colony polling implementation.
func (s *ObservabilityL0Suite) TestLevel0_BeylaColonyPolling() {
	s.T().Skip("Beyla integration not yet implemented")

	s.T().Log("Testing Beyla metrics polling (agent → colony)...")

	// Create fixture with colony and agent.
	fixture, err := fixtures.NewContainerFixture(s.ctx, fixtures.FixtureOptions{
		NumAgents: 1,
		// TODO: WithBeyla: true,
	})
	s.Require().NoError(err, "Failed to create container fixture")
	defer func() {
		if fixture != nil {
			_ = fixture.Cleanup(s.ctx)
		}
	}()

	// TODO: Generate metrics via HTTP traffic.

	s.T().Log("Waiting for Beyla metrics collection and colony polling...")
	time.Sleep(30 * time.Second)

	// TODO: Verify colony Beyla polling.
	// This requires:
	// 1. Query colony database for beyla_summaries table
	// 2. Verify 1-minute aggregation buckets
	// 3. Verify RED metrics aggregation
	// 4. Verify multiple agents if testing multi-agent setup

	s.T().Log("✓ Beyla colony polling test placeholder")
}

// TestLevel0_BeylaVsOTLP compares Beyla (passive) vs OTLP (active) metrics.
//
// This test verifies that Beyla captures metrics for uninstrumented apps,
// while OTLP requires explicit instrumentation.
//
// Test flow:
// 1. Start agent with both Beyla and OTLP receivers
// 2. Start uninstrumented app (no OTLP SDK)
// 3. Generate traffic
// 4. Verify Beyla captures metrics (passive eBPF)
// 5. Verify OTLP does NOT capture metrics (no instrumentation)
func (s *ObservabilityL0Suite) TestLevel0_BeylaVsOTLP() {
	s.T().Skip("Beyla integration and uninstrumented test app not yet available")

	s.T().Log("✓ Beyla vs OTLP comparison test placeholder")
	s.T().Log("")
	s.T().Log("This test validates Beyla's value proposition:")
	s.T().Log("  - Beyla: Passive eBPF instrumentation (no code changes)")
	s.T().Log("  - OTLP: Active instrumentation (requires SDK)")
}
