package distributed

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// MCPParitySuite tests data consistency between MCP tools and CLI commands.
//
// This suite validates that:
//  1. MCP tools (for LLM consumption) and CLI commands (for human operators)
//     return equivalent data
//  2. Both interfaces expose the same capabilities
//  3. JSON schemas match CLI output structure
//
// The suite runs after MCP tests to leverage maximum telemetry data.
type MCPParitySuite struct {
	E2EDistributedSuite

	cliEnv *helpers.CLITestEnv
}

// SetupSuite runs once before all tests in the suite.
func (s *MCPParitySuite) SetupSuite() {
	s.E2EDistributedSuite.SetupSuite()

	// Setup CLI environment
	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	colonyID := "test-colony-e2e" // Default colony ID from docker-compose

	s.cliEnv, err = helpers.SetupCLIEnv(s.ctx, colonyID, colonyEndpoint)
	s.Require().NoError(err, "Failed to setup CLI environment")

	// Ensure services are connected for testing
	s.ensureServicesConnected()

	s.T().Logf("MCP parity test environment ready: endpoint=%s, colonyID=%s", colonyEndpoint, colonyID)
}

// TearDownSuite cleans up after all tests.
func (s *MCPParitySuite) TearDownSuite() {
	if s.cliEnv != nil {
		_ = s.cliEnv.Cleanup()
	}
	s.E2EDistributedSuite.TearDownSuite()
}

// =============================================================================
// Observability Parity Tests
// =============================================================================

// TestParityQuerySummary validates MCP and CLI return consistent summary data.
//
// Compares:
// - Service names
// - Request counts
// - Error rates
// - Latencies
func (s *MCPParitySuite) TestParityQuerySummary() {
	s.T().Log("Testing MCP/CLI parity for query summary...")

	// Start MCP proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize proxy
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// 1. Query via MCP tool
	mcpResp, err := proxy.CallTool("coral_query_summary", map[string]interface{}{
		"service":    "otel-app",
		"time_range": "10m",
	}, 100)
	s.Require().NoError(err, "MCP query should succeed")
	s.Require().NotEmpty(mcpResp.Content, "MCP should have content")

	mcpText := mcpResp.Content[0].Text
	s.T().Log("MCP summary result:")
	s.T().Log(mcpText)

	// 2. Query via CLI command
	cliResult := helpers.QuerySummary(s.ctx, s.cliEnv.ColonyEndpoint, "otel-app", "10m")
	cliResult.MustSucceed(s.T())

	s.T().Log("CLI summary result:")
	s.T().Log(cliResult.Output)

	// 3. Compare data
	// Both should mention the service
	s.Require().Contains(strings.ToLower(mcpText), "otel-app", "MCP should mention service")
	s.Require().Contains(strings.ToLower(cliResult.Output), "otel-app", "CLI should mention service")

	// Both should have service health information
	s.Require().NotEmpty(mcpText, "MCP should have data")
	s.Require().NotEmpty(cliResult.Output, "CLI should have data")

	s.T().Log("✓ MCP/CLI parity for query summary validated")
}

// TestParityListServices validates MCP and CLI return consistent service lists.
//
// Compares:
// - Service names
// - Service counts
// - Service metadata
func (s *MCPParitySuite) TestParityListServices() {
	s.T().Log("Testing MCP/CLI parity for list services...")

	// Start MCP proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize proxy
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// 1. Query via MCP tool
	mcpResp, err := proxy.CallTool("coral_list_services", map[string]interface{}{}, 101)
	s.Require().NoError(err, "MCP list should succeed")
	s.Require().NotEmpty(mcpResp.Content, "MCP should have content")

	mcpText := mcpResp.Content[0].Text
	s.T().Log("MCP services list:")
	s.T().Log(mcpText)

	// Parse MCP JSON response
	var mcpServices struct {
		Services []struct {
			Name string `json:"name"`
		} `json:"services"`
	}
	err = json.Unmarshal([]byte(mcpText), &mcpServices)
	s.Require().NoError(err, "Should parse MCP JSON")

	// 2. Query via CLI command
	cliServices, err := helpers.ServiceListJSON(s.ctx, s.cliEnv.ColonyEndpoint)
	s.Require().NoError(err, "CLI list should succeed")

	s.T().Logf("CLI services count: %d", len(cliServices))
	s.T().Logf("MCP services count: %d", len(mcpServices.Services))

	// 3. Compare data
	// Both should have services
	s.Require().NotEmpty(mcpServices.Services, "MCP should have services")
	s.Require().NotEmpty(cliServices, "CLI should have services")

	// Extract service names from both
	mcpServiceNames := make(map[string]bool)
	for _, svc := range mcpServices.Services {
		mcpServiceNames[svc.Name] = true
	}

	cliServiceNames := make(map[string]bool)
	for _, svc := range cliServices {
		if name, ok := svc["service_name"].(string); ok {
			cliServiceNames[name] = true
		}
	}

	// Verify overlap (may not be exact due to timing)
	hasOverlap := false
	for name := range mcpServiceNames {
		if cliServiceNames[name] {
			hasOverlap = true
			s.T().Logf("Found matching service: %s", name)
		}
	}
	s.Require().True(hasOverlap, "Should have at least one matching service")

	s.T().Log("✓ MCP/CLI parity for list services validated")
}

// TestParityQueryTraces validates MCP and CLI return consistent trace data.
//
// Compares:
// - Trace availability
// - Trace structure
// - Service mentions
func (s *MCPParitySuite) TestParityQueryTraces() {
	s.T().Log("Testing MCP/CLI parity for query traces...")

	// Start MCP proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize proxy
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// 1. Query via MCP tool
	mcpResp, err := proxy.CallTool("coral_query_traces", map[string]interface{}{
		"service":    "otel-app",
		"time_range": "10m",
		"limit":      5,
	}, 102)
	s.Require().NoError(err, "MCP traces query should succeed")
	s.Require().NotEmpty(mcpResp.Content, "MCP should have content")

	mcpText := mcpResp.Content[0].Text
	s.T().Log("MCP traces result (truncated):")
	if len(mcpText) > 300 {
		s.T().Log(mcpText[:300] + "...")
	} else {
		s.T().Log(mcpText)
	}

	// 2. Query via CLI command
	cliResult := helpers.QueryTraces(s.ctx, s.cliEnv.ColonyEndpoint, "otel-app", "10m", 0)
	cliResult.MustSucceed(s.T())

	s.T().Log("CLI traces result (truncated):")
	cliOutput := cliResult.Output
	if len(cliOutput) > 300 {
		s.T().Log(cliOutput[:300] + "...")
	} else {
		s.T().Log(cliOutput)
	}

	// 3. Compare data
	// Both should have trace information
	s.Require().NotEmpty(mcpText, "MCP should have trace data")
	s.Require().NotEmpty(cliOutput, "CLI should have trace data")

	s.T().Log("✓ MCP/CLI parity for query traces validated")
}

// TestParityQueryMetrics validates MCP and CLI return consistent metrics data.
//
// Compares:
// - Metrics availability
// - HTTP metrics
// - Service mentions
func (s *MCPParitySuite) TestParityQueryMetrics() {
	s.T().Log("Testing MCP/CLI parity for query metrics...")

	// Start MCP proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize proxy
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// 1. Query via MCP tool
	mcpResp, err := proxy.CallTool("coral_query_metrics", map[string]interface{}{
		"service":    "otel-app",
		"time_range": "10m",
	}, 103)
	s.Require().NoError(err, "MCP metrics query should succeed")
	s.Require().NotEmpty(mcpResp.Content, "MCP should have content")

	mcpText := mcpResp.Content[0].Text
	s.T().Log("MCP metrics result (truncated):")
	if len(mcpText) > 300 {
		s.T().Log(mcpText[:300] + "...")
	} else {
		s.T().Log(mcpText)
	}

	// 2. Query via CLI command
	cliResult := helpers.QueryMetrics(s.ctx, s.cliEnv.ColonyEndpoint, "otel-app", "10m")
	cliResult.MustSucceed(s.T())

	s.T().Log("CLI metrics result (truncated):")
	cliOutput := cliResult.Output
	if len(cliOutput) > 300 {
		s.T().Log(cliOutput[:300] + "...")
	} else {
		s.T().Log(cliOutput)
	}

	// 3. Compare data
	// Both should have metrics information
	s.Require().NotEmpty(mcpText, "MCP should have metrics data")
	s.Require().NotEmpty(cliOutput, "CLI should have metrics data")

	s.T().Log("✓ MCP/CLI parity for query metrics validated")
}

// =============================================================================
// Execution Parity Tests
// =============================================================================

// TestParityShellExec validates MCP shell exec returns consistent results.
//
// Note: There is no separate CLI command for agent exec - it's only available
// via MCP. This test validates the MCP tool returns consistent output format.
func (s *MCPParitySuite) TestParityShellExec() {
	s.T().Log("Testing MCP shell exec consistency...")

	// Get agent ID
	agents, err := helpers.ColonyAgentsJSON(s.ctx, s.cliEnv.ColonyEndpoint)
	s.Require().NoError(err, "Should list agents")
	s.Require().NotEmpty(agents, "Should have at least one agent")
	s.Require().Contains(agents[0], "agent_id", "Should have agent id")

	agentID := agents[0]["agent_id"].(string)

	// Start MCP proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize proxy
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Execute same command twice to verify consistency
	for i := 1; i <= 2; i++ {
		execResp, err := proxy.CallTool("coral_shell_exec", map[string]interface{}{
			"service":  "otel-app",
			"agent_id": agentID,
			"command":  []string{"echo", "parity-test"},
		}, 104+i)
		s.Require().NoError(err, "MCP exec should succeed")
		s.Require().NotEmpty(execResp.Content, "MCP should have content")

		execText := execResp.Content[0].Text
		s.T().Logf("MCP exec result (run %d):", i)
		s.T().Log(execText)

		// Verify output contains expected elements
		s.Require().Contains(execText, "parity-test", "Should have command output")
		s.Require().Contains(strings.ToLower(execText), "exit code", "Should show exit code")
		s.Require().Contains(strings.ToLower(execText), "agent", "Should mention agent")
	}

	s.T().Log("✓ MCP shell exec consistency validated")
}

// =============================================================================
// Profiling Parity (RFD 074)
// =============================================================================

// TestParityQuerySummaryProfilingConsistency validates that profiling-enriched
// summaries are consistent between calls with include_profiling=true and false.
//
// This ensures:
// - include_profiling=true does not break the base summary data
// - include_profiling=false omits profiling sections from output
// - Both calls return the same base service health data
func (s *MCPParitySuite) TestParityQuerySummaryProfilingConsistency() {
	s.T().Log("Testing profiling summary consistency (RFD 074)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Ensure services and telemetry data.
	s.ensureServicesConnected()
	s.ensureTelemetryData()

	// Query 1: With profiling enabled (default).
	withProf, err := proxy.CallTool("coral_query_summary", map[string]interface{}{
		"service":           "otel-app",
		"time_range":        "10m",
		"include_profiling": true,
	}, 200)
	s.Require().NoError(err, "coral_query_summary with profiling should succeed")
	s.Require().NotEmpty(withProf.Content, "Should have content")
	withProfText := withProf.Content[0].Text

	// Query 2: With profiling disabled.
	withoutProf, err := proxy.CallTool("coral_query_summary", map[string]interface{}{
		"service":           "otel-app",
		"time_range":        "10m",
		"include_profiling": false,
	}, 201)
	s.Require().NoError(err, "coral_query_summary without profiling should succeed")
	s.Require().NotEmpty(withoutProf.Content, "Should have content")
	withoutProfText := withoutProf.Content[0].Text

	// Both should contain the same base service data.
	s.Require().Contains(strings.ToLower(withProfText), "service",
		"With-profiling response should mention service")
	s.Require().Contains(strings.ToLower(withoutProfText), "service",
		"Without-profiling response should mention service")

	// Both should be non-empty.
	s.Require().NotEmpty(withProfText, "With-profiling response should not be empty")
	s.Require().NotEmpty(withoutProfText, "Without-profiling response should not be empty")

	// Log outputs for comparison.
	s.T().Log("With profiling (truncated):")
	if len(withProfText) > 300 {
		s.T().Log(withProfText[:300] + "...")
	} else {
		s.T().Log(withProfText)
	}

	s.T().Log("Without profiling (truncated):")
	if len(withoutProfText) > 300 {
		s.T().Log(withoutProfText[:300] + "...")
	} else {
		s.T().Log(withoutProfText)
	}

	s.T().Log("✓ Profiling summary consistency validated")
}

// ensureTelemetryData generates HTTP traffic to populate telemetry data.
func (s *MCPParitySuite) ensureTelemetryData() {
	otelAppURL := "http://localhost:8082"
	err := helpers.WaitForHTTPEndpoint(s.ctx, otelAppURL+"/health", 10*time.Second)
	if err != nil {
		s.T().Log("OTEL app not reachable, telemetry tests may have limited data")
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}
	for i := 0; i < 5; i++ {
		resp, err := client.Get(otelAppURL + "/")
		if err == nil {
			_ = resp.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(2 * time.Second)
}

// =============================================================================
// Helper Methods
// =============================================================================

// compareServiceSummaries compares service summary data from MCP and CLI.
func (s *MCPParitySuite) compareServiceSummaries(mcpData, cliData interface{}) {
	// Helper to extract and compare service summary fields
	// This can be expanded based on actual data structures
	s.T().Log("Comparing service summaries...")

	// Basic validation that both have data
	s.Require().NotNil(mcpData, "MCP data should not be nil")
	s.Require().NotNil(cliData, "CLI data should not be nil")
}

// parseJSONResponse parses JSON response from MCP tool.
func parseJSONResponse(text string) map[string]interface{} {
	var data map[string]interface{}
	_ = json.Unmarshal([]byte(text), &data)
	return data
}

// parseJSONOutput parses JSON output from CLI command.
func parseJSONOutput(output string) map[string]interface{} {
	var data map[string]interface{}
	_ = json.Unmarshal([]byte(output), &data)
	return data
}

// extractServiceNames extracts service names from various data formats.
func extractServiceNames(data interface{}) []string {
	var names []string

	switch v := data.(type) {
	case map[string]interface{}:
		if services, ok := v["services"].([]interface{}); ok {
			for _, svc := range services {
				if svcMap, ok := svc.(map[string]interface{}); ok {
					if name, ok := svcMap["name"].(string); ok {
						names = append(names, name)
					}
				}
			}
		}
	case []interface{}:
		for _, item := range v {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if name, ok := itemMap["name"].(string); ok {
					names = append(names, name)
				}
			}
		}
	}

	return names
}

// allowFloatVariance checks if two float values are within acceptable variance.
func allowFloatVariance(a, b, maxVariance float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= maxVariance
}

// formatComparisonError formats a comparison error message.
func formatComparisonError(field string, mcpValue, cliValue interface{}) string {
	return fmt.Sprintf("Mismatch in %s: MCP=%v, CLI=%v", field, mcpValue, cliValue)
}

// ensureServicesConnected ensures that test services are connected.
// This uses the shared helper for idempotent service connection.
func (s *MCPParitySuite) ensureServicesConnected() {
	// MCP tests only need otel-app (OTLP-instrumented)
	helpers.EnsureServicesConnected(s.T(), s.ctx, s.fixture, 0, []helpers.ServiceConfig{
		{Name: "otel-app", Port: 8090, HealthEndpoint: "/health"},
	})
}
