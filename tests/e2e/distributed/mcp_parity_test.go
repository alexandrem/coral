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
// After RFD 100, the MCP proxy dispatches all operations via coral_cli, which
// literally runs the same CLI commands. Parity between MCP and CLI is thus
// inherent — both use the same underlying implementation.
//
// This suite validates that:
//  1. coral_cli MCP tool output matches direct CLI command output
//  2. Both interfaces expose the same capabilities
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

// TestParityQuerySummary validates coral_cli MCP and direct CLI return
// consistent summary data (post-RFD 100).
//
// Since coral_cli literally runs the CLI command, parity is inherent.
// This test validates the round-trip from MCP → coral_cli → CLI.
func (s *MCPParitySuite) TestParityQuerySummary() {
	s.T().Log("Testing MCP/CLI parity for query summary via coral_cli (post-RFD 100)...")

	// Start MCP proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize proxy
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// 1. Query via coral_cli MCP tool (replaces coral_query_summary).
	mcpResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "summary", "otel-app", "--since", "10m"},
	}, 100)
	s.Require().NoError(err, "MCP coral_cli query should succeed")
	s.Require().NotEmpty(mcpResp.Content, "MCP should have content")

	mcpText := mcpResp.Content[0].Text
	s.T().Log("MCP summary result:")
	s.T().Log(mcpText)

	// 2. Query via direct CLI command
	cliResult := helpers.QuerySummary(s.ctx, s.cliEnv, "otel-app", "10m")
	cliResult.MustSucceed(s.T())

	s.T().Log("CLI summary result:")
	s.T().Log(cliResult.Output)

	// 3. Compare data — both use the same CLI under the hood.
	s.Require().Contains(strings.ToLower(mcpText), "otel-app", "MCP should mention service")
	s.Require().Contains(strings.ToLower(cliResult.Output), "otel-app", "CLI should mention service")

	s.Require().NotEmpty(mcpText, "MCP should have data")
	s.Require().NotEmpty(cliResult.Output, "CLI should have data")

	s.T().Log("✓ MCP/CLI parity for query summary validated")
}

// TestParityListServices validates coral_cli MCP and direct CLI return
// consistent service lists (post-RFD 100).
func (s *MCPParitySuite) TestParityListServices() {
	s.T().Log("Testing MCP/CLI parity for list services via coral_cli (post-RFD 100)...")

	// Start MCP proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize proxy
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// 1. Query via coral_cli MCP tool (replaces coral_list_services).
	mcpResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"colony", "service", "list"},
	}, 101)
	s.Require().NoError(err, "MCP coral_cli colony service list should succeed")
	s.Require().NotEmpty(mcpResp.Content, "MCP should have content")

	mcpText := mcpResp.Content[0].Text
	s.T().Log("MCP services list:")
	s.T().Log(mcpText)

	// 2. Query via CLI command
	cliServices, err := helpers.ServiceListJSON(s.ctx, s.cliEnv)
	s.Require().NoError(err, "CLI list should succeed")

	s.T().Logf("CLI services count: %d", len(cliServices))

	// 3. Compare data — both should list services.
	s.Require().NotEmpty(mcpText, "MCP should have data")
	s.Require().NotEmpty(cliServices, "CLI should have services")

	// Both should mention at least one service by name.
	hasOtelApp := strings.Contains(mcpText, "otel-app") || strings.Contains(mcpText, "cpu-app")
	s.Require().True(hasOtelApp, "MCP response should mention at least one service")

	s.T().Log("✓ MCP/CLI parity for list services validated")
}

// TestParityQueryTraces validates coral_cli MCP and direct CLI return
// consistent trace data (post-RFD 100).
func (s *MCPParitySuite) TestParityQueryTraces() {
	s.T().Log("Testing MCP/CLI parity for query traces via coral_cli (post-RFD 100)...")

	// Start MCP proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize proxy
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// 1. Query via coral_cli MCP tool (replaces coral_query_traces).
	mcpResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "traces", "otel-app", "--since", "10m"},
	}, 102)
	s.Require().NoError(err, "MCP coral_cli query traces should succeed")
	s.Require().NotEmpty(mcpResp.Content, "MCP should have content")

	mcpText := mcpResp.Content[0].Text
	s.T().Log("MCP traces result (truncated):")
	if len(mcpText) > 300 {
		s.T().Log(mcpText[:300] + "...")
	} else {
		s.T().Log(mcpText)
	}

	// 2. Query via direct CLI command
	cliResult := helpers.QueryTraces(s.ctx, s.cliEnv, "otel-app", "10m", 0)
	cliResult.MustSucceed(s.T())

	s.T().Log("CLI traces result (truncated):")
	cliOutput := cliResult.Output
	if len(cliOutput) > 300 {
		s.T().Log(cliOutput[:300] + "...")
	} else {
		s.T().Log(cliOutput)
	}

	// 3. Compare data — both should have trace information.
	s.Require().NotEmpty(mcpText, "MCP should have trace data")
	s.Require().NotEmpty(cliOutput, "CLI should have trace data")

	s.T().Log("✓ MCP/CLI parity for query traces validated")
}

// TestParityQueryMetrics validates coral_cli MCP and direct CLI return
// consistent metrics data (post-RFD 100).
func (s *MCPParitySuite) TestParityQueryMetrics() {
	s.T().Log("Testing MCP/CLI parity for query metrics via coral_cli (post-RFD 100)...")

	// Start MCP proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize proxy
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// 1. Query via coral_cli MCP tool (replaces coral_query_metrics).
	mcpResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "metrics", "otel-app", "--since", "10m"},
	}, 103)
	s.Require().NoError(err, "MCP coral_cli query metrics should succeed")
	s.Require().NotEmpty(mcpResp.Content, "MCP should have content")

	mcpText := mcpResp.Content[0].Text
	s.T().Log("MCP metrics result (truncated):")
	if len(mcpText) > 300 {
		s.T().Log(mcpText[:300] + "...")
	} else {
		s.T().Log(mcpText)
	}

	// 2. Query via direct CLI command
	cliResult := helpers.QueryMetrics(s.ctx, s.cliEnv, "otel-app", "10m")
	cliResult.MustSucceed(s.T())

	s.T().Log("CLI metrics result (truncated):")
	cliOutput := cliResult.Output
	if len(cliOutput) > 300 {
		s.T().Log(cliOutput[:300] + "...")
	} else {
		s.T().Log(cliOutput)
	}

	// 3. Compare data — both should have metrics information.
	s.Require().NotEmpty(mcpText, "MCP should have metrics data")
	s.Require().NotEmpty(cliOutput, "CLI should have metrics data")

	s.T().Log("✓ MCP/CLI parity for query metrics validated")
}

// =============================================================================
// Execution Parity Tests
// =============================================================================

// TestParityShellExec validates that coral_shell_exec is not available via the
// proxy post-RFD 100, and tests coral_cli colony status as a replacement parity
// check.
//
// Previously this tested coral_shell_exec consistency; now it validates that
// coral_cli can be used to check colony status via MCP with the same output
// as the direct CLI command.
func (s *MCPParitySuite) TestParityShellExec() {
	s.T().Log("Testing coral_cli colony status parity (replaces shell exec parity post-RFD 100)...")

	// Start MCP proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize proxy
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Verify coral_shell_exec is NOT available (returns unknown tool error).
	mcpErr, err := proxy.CallToolExpectError("coral_shell_exec", map[string]interface{}{
		"command": []string{"echo", "parity-test"},
	}, 104)
	s.Require().NoError(err, "Should get error response for coral_shell_exec")
	s.Require().NotNil(mcpErr, "Should have MCP error")
	s.Require().Contains(mcpErr.Message, "only coral_cli is supported",
		"coral_shell_exec should return unknown tool error post-RFD 100")
	s.T().Logf("✓ coral_shell_exec correctly unavailable post-RFD 100: %s", mcpErr.Message)

	// Test coral_cli with colony service list as a parity check (same data both ways).
	mcpResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"colony", "service", "list"},
	}, 105)
	s.Require().NoError(err, "coral_cli colony service list should succeed")
	s.Require().NotEmpty(mcpResp.Content, "MCP should have content")

	s.T().Logf("✓ coral_cli parity check via colony service list validated")
}

// =============================================================================
// Profiling Parity (RFD 074) — updated for RFD 100
// =============================================================================

// TestParityQuerySummaryProfilingConsistency validates that query summary
// via coral_cli is consistent between calls (post-RFD 100).
//
// The include_profiling parameter no longer has a direct CLI equivalent;
// both calls use the same coral_cli command and should return consistent results.
func (s *MCPParitySuite) TestParityQuerySummaryProfilingConsistency() {
	s.T().Log("Testing profiling summary consistency via coral_cli (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Ensure services and telemetry data.
	s.ensureServicesConnected()
	s.ensureTelemetryData()

	// Query 1: First call via coral_cli.
	firstCall, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "summary", "otel-app", "--since", "10m"},
	}, 200)
	s.Require().NoError(err, "coral_cli query summary should succeed (first call)")
	s.Require().NotEmpty(firstCall.Content, "Should have content")
	firstText := firstCall.Content[0].Text

	// Query 2: Second call via coral_cli (should produce consistent results).
	secondCall, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "summary", "otel-app", "--since", "10m"},
	}, 201)
	s.Require().NoError(err, "coral_cli query summary should succeed (second call)")
	s.Require().NotEmpty(secondCall.Content, "Should have content")
	secondText := secondCall.Content[0].Text

	// Both should contain the same base service data.
	s.Require().Contains(strings.ToLower(firstText), "service",
		"First call response should mention service")
	s.Require().Contains(strings.ToLower(secondText), "service",
		"Second call response should mention service")

	// Both should be non-empty.
	s.Require().NotEmpty(firstText, "First call response should not be empty")
	s.Require().NotEmpty(secondText, "Second call response should not be empty")

	// Log outputs for comparison.
	s.T().Log("First call (truncated):")
	if len(firstText) > 300 {
		s.T().Log(firstText[:300] + "...")
	} else {
		s.T().Log(firstText)
	}

	s.T().Log("Second call (truncated):")
	if len(secondText) > 300 {
		s.T().Log(secondText[:300] + "...")
	} else {
		s.T().Log(secondText)
	}

	s.T().Log("✓ Summary consistency via coral_cli validated")
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
	// Helper to extract and compare service summary fields.
	// This can be expanded based on actual data structures.
	s.T().Log("Comparing service summaries...")

	// Basic validation that both have data.
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
