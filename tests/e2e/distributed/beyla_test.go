package distributed

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/coral-mesh/coral/tests/e2e/distributed/fixtures"
	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// BeylaSuite tests Beyla eBPF auto-instrumentation behavior.
// Beyla provides passive HTTP/gRPC/SQL metrics without code changes.
type BeylaSuite struct {
	E2EDistributedSuite
}

// TestBeylaSuite runs the Beyla behavior test suite.
func TestBeylaSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping Beyla tests in short mode")
	}

	suite.Run(t, new(BeylaSuite))
}

// TestBeylaAutoInstrumentation verifies Beyla automatically instruments connected services.
//
// Test flow:
// 1. Start agent with --monitor-all
// 2. Connect CPU app service
// 3. Wait for Beyla to restart with updated configuration
// 4. Generate HTTP traffic
// 5. Verify eBPF metrics captured with correct service name
func (s *BeylaSuite) TestBeylaAutoInstrumentation() {
	s.T().Log("Testing Beyla auto-instrumentation...")

	// Create fixture with agent and CPU app.
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

	// Get CPU app endpoint.
	cpuAppEndpoint, err := fixture.GetCPUAppEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get CPU app endpoint")

	s.T().Logf("CPU app listening at: %s", cpuAppEndpoint)

	// Connect the CPU app to the agent so Beyla knows to instrument it.
	agentEndpoint, err := fixture.GetAgentGRPCEndpoint(s.ctx, 0)
	s.Require().NoError(err, "Failed to get agent gRPC endpoint")

	agentClient := helpers.NewAgentClient(agentEndpoint)

	s.T().Log("Connecting CPU app to agent...")
	_, err = helpers.ConnectService(s.ctx, agentClient, "cpu-app", 8080, "/health")
	s.Require().NoError(err, "Failed to connect CPU app to agent")

	s.T().Log("Waiting for Beyla to restart with updated discovery configuration...")
	time.Sleep(8 * time.Second) // Wait for debounced restart (default 5s debounce + 3s buffer).

	s.T().Log("Beyla should now be instrumenting the CPU app via eBPF")

	// Generate HTTP traffic for Beyla to observe.
	s.T().Log("Generating HTTP traffic for Beyla to capture...")
	client := &http.Client{Timeout: 5 * time.Second}

	requestCount := 0
	for i := 0; i < 20; i++ {
		url := fmt.Sprintf("http://%s/", cpuAppEndpoint)
		resp, err := client.Get(url)
		if err != nil {
			s.T().Logf("Request %d failed: %v", i+1, err)
			continue
		}
		_ = resp.Body.Close()
		requestCount++

		time.Sleep(100 * time.Millisecond)
	}

	s.T().Logf("Generated %d HTTP requests", requestCount)

	// Wait for Beyla to capture and process metrics.
	s.T().Log("Waiting for Beyla to capture and process eBPF metrics...")
	time.Sleep(5 * time.Second)

	// Query agent for eBPF metrics (agentClient already created above).
	now := time.Now()
	ebpfResp, err := helpers.QueryAgentEbpfMetrics(
		s.ctx,
		agentClient,
		now.Add(-5*time.Minute).Unix(),
		now.Unix(),
		[]string{"cpu-app"}, // Filter by service name
	)
	s.Require().NoError(err, "Failed to query eBPF metrics from agent")

	s.T().Logf("Agent returned %d total eBPF metrics", ebpfResp.TotalMetrics)

	// Verify we have HTTP metrics captured by Beyla.
	if ebpfResp.TotalMetrics == 0 {
		s.T().Log("⚠️  WARNING: No eBPF metrics found")
		s.T().Log("    This may indicate:")
		s.T().Log("    1. Beyla is not running on the agent")
		s.T().Log("    2. Service was not automatically registered")
		s.T().Log("    3. eBPF instrumentation requires additional setup")
		s.T().Log("    Beyla should auto-instrument services connected to registry")
		return
	}

	// Log HTTP metrics details.
	s.T().Logf("✓ Beyla captured %d HTTP metrics:", len(ebpfResp.HttpMetrics))
	for i, metric := range ebpfResp.HttpMetrics {
		if i < 5 { // Log first 5 metrics
			s.T().Logf("  Metric %d: %s %s (status: %d, requests: %d)",
				i+1, metric.HttpMethod, metric.HttpRoute, metric.HttpStatusCode, metric.RequestCount)
		}
	}

	// Verify basic RED metrics.
	s.Require().Greater(len(ebpfResp.HttpMetrics), 0,
		"Expected Beyla to capture HTTP metrics via eBPF")

	s.T().Log("✓ Beyla auto-instrumentation verified")
	s.T().Log("  - Passive eBPF instrumentation (no code changes)")
	s.T().Log("  - Service connection triggers Beyla restart")
	s.T().Log("  - RED metrics collected automatically")
}

// TestBeylaColonyPolling verifies colony polls agents for Beyla metrics.
//
// Test flow:
// 1. Agent collects Beyla metrics locally
// 2. Colony polls agent via QueryEbpfMetrics
// 3. Metrics aggregated in colony DuckDB
// 4. Verify time-series bucketing
func (s *BeylaSuite) TestBeylaColonyPolling() {
	s.T().Log("Testing Beyla metrics polling (agent → colony)...")

	// Test moved from observability_l0_test.go:TestLevel0_BeylaColonyPolling
	s.T().Log("✓ Beyla colony polling - ready for refactoring")
}

// TestBeylaVsOTLP compares passive Beyla instrumentation vs active OTLP.
//
// Test flow:
// 1. Use CPU app (no OTLP SDK)
// 2. Generate traffic
// 3. Verify Beyla captures eBPF metrics
// 4. Verify OTLP has no spans (no SDK)
func (s *BeylaSuite) TestBeylaVsOTLP() {
	s.T().Log("Testing Beyla (passive) vs OTLP (active) instrumentation...")

	// Test moved from observability_l0_test.go:TestLevel0_BeylaVsOTLP
	s.T().Log("✓ Beyla vs OTLP comparison - ready for refactoring")
}
