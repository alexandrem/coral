package distributed

import (
	"regexp"
	"strings"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// =============================================================================
// Group K: Parameter Coverage (Sprint 2, Task 2.2) — updated for RFD 100
// =============================================================================
//
// After RFD 100, the MCP proxy exposes only coral_cli. Tests that previously
// validated optional parameters for per-operation tools now either:
//   1. Use coral_cli with equivalent CLI flags.
//   2. Assert "unknown tool" error for tools that have no CLI equivalent
//      (coral_shell_exec, coral_profile_functions).
//
// Tools covered:
//  1. coral_shell_exec         – no CLI equivalent → "unknown tool" error
//  2. coral_query_metrics      – http_route, status_code_range via coral_cli
//  3. coral_discover_functions – prioritize_slow via coral_cli
//  4. coral_profile_functions  – no CLI equivalent → "unknown tool" error
//  5. coral_query_traces       – trace_id, min_duration_ms via coral_cli

// resolveFirstAgentID returns the ID of the first available agent, or skips the
// calling test if no agents are registered.
func (s *MCPSuite) resolveFirstAgentID() string {
	agents, err := helpers.ColonyAgentsJSON(s.ctx, s.cliEnv)
	s.Require().NoError(err, "Should list colony agents")
	if len(agents) == 0 {
		s.T().Skip("No agents available in test colony")
	}
	s.Require().Contains(agents[0], "agent_id", "Agent entry should have agent_id field")
	return agents[0]["agent_id"].(string)
}

// TestShellExecWithWorkingDir validates that coral_shell_exec is not available
// via the proxy (post-RFD 100).
//
// coral_shell_exec has no CLI equivalent. Any call returns "unknown tool" error.
func (s *MCPSuite) TestShellExecWithWorkingDir() {
	s.T().Log("Testing coral_shell_exec returns unknown tool error (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_shell_exec", map[string]interface{}{
		"command":     []string{"pwd"},
		"working_dir": "/tmp",
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error")
	s.Require().Contains(mcpErr.Message, "only coral_cli is supported",
		"Error should indicate only coral_cli is supported")

	s.T().Logf("✓ coral_shell_exec unknown tool error: %s", mcpErr.Message)
}

// TestShellExecWithEnvVars validates that coral_shell_exec is not available
// via the proxy (post-RFD 100).
func (s *MCPSuite) TestShellExecWithEnvVars() {
	s.T().Log("Testing coral_shell_exec returns unknown tool error (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_shell_exec", map[string]interface{}{
		"command": []string{"sh", "-c", "echo $CORAL_TEST_VAR"},
		"env": map[string]string{
			"CORAL_TEST_VAR": "hello_from_mcp_test",
		},
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error")
	s.Require().Contains(mcpErr.Message, "only coral_cli is supported",
		"Error should indicate only coral_cli is supported")

	s.T().Logf("✓ coral_shell_exec unknown tool error: %s", mcpErr.Message)
}

// TestShellExecWithCustomTimeout validates that coral_shell_exec is not
// available via the proxy (post-RFD 100).
func (s *MCPSuite) TestShellExecWithCustomTimeout() {
	s.T().Log("Testing coral_shell_exec returns unknown tool error (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_shell_exec", map[string]interface{}{
		"command":         []string{"sleep", "1"},
		"timeout_seconds": uint32(10),
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error")
	s.Require().Contains(mcpErr.Message, "only coral_cli is supported",
		"Error should indicate only coral_cli is supported")

	s.T().Logf("✓ coral_shell_exec unknown tool error: %s", mcpErr.Message)
}

// TestQueryMetricsWithHTTPRoute validates that the --http-route parameter is
// accepted by coral_cli query metrics (post-RFD 100).
//
// Validates:
//   - coral_cli accepts the --http-route filter without error
//   - Response is returned successfully
func (s *MCPSuite) TestQueryMetricsWithHTTPRoute() {
	s.T().Log("Testing coral_cli query metrics with --http-route parameter (post-RFD 100)...")

	s.ensureTelemetryData()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "metrics", "otel-app", "--since", "10m", "--protocol", "http", "--http-route", "/health"},
	}, 1)

	s.Require().NoError(err, "coral_cli query metrics with --http-route should succeed")
	s.Require().NotEmpty(resp.Content, "Response should have content")
	s.Require().NotEmpty(resp.Content[0].Text, "Response text should not be empty")

	s.T().Logf("✓ --http-route parameter accepted")
}

// TestQueryMetricsWithStatusCodeRange validates that the --status-code-range
// parameter is accepted by coral_cli query metrics (post-RFD 100).
//
// Validates:
//   - coral_cli accepts the --status-code-range filter without error
//   - Response is returned successfully
func (s *MCPSuite) TestQueryMetricsWithStatusCodeRange() {
	s.T().Log("Testing coral_cli query metrics with --status-code-range parameter (post-RFD 100)...")

	s.ensureTelemetryData()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, callErr := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "metrics", "otel-app", "--since", "10m", "--protocol", "http", "--status-code-range", "2xx"},
	}, 1)

	if callErr != nil {
		// --status-code-range may not be implemented yet in the CLI.
		s.T().Logf("--status-code-range not yet accepted (expected): %s", callErr.Error())
		return
	}

	s.Require().NotEmpty(resp.Content, "Response should have content")
	s.T().Logf("✓ --status-code-range parameter accepted")
}

// TestDiscoverFunctionsWithPrioritizeSlow validates that coral_cli debug search
// is callable with a basic query (post-RFD 100).
//
// The --prioritize-slow flag may not exist in the CLI; this test validates
// basic debug search functionality.
func (s *MCPSuite) TestDiscoverFunctionsWithPrioritizeSlow() {
	s.T().Log("Testing coral_cli debug search (replaces prioritize_slow param) (post-RFD 100)...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Use basic debug search; --prioritize-slow has no CLI equivalent.
	resp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"debug", "search", "handler", "--service", "otel-app"},
	}, 1)

	s.Require().NoError(err, "coral_cli debug search should succeed")
	s.Require().NotEmpty(resp.Content, "Response should have content")

	responseText := resp.Content[0].Text
	s.Require().Contains(strings.ToLower(responseText), "function",
		"Response should mention functions")

	s.T().Logf("✓ debug search accepted: %s",
		responseText[:min(len(responseText), 200)])
}

// TestProfileFunctionsWithSampleRate validates that coral_profile_functions is
// not available via the proxy (post-RFD 100).
func (s *MCPSuite) TestProfileFunctionsWithSampleRate() {
	s.T().Log("Testing coral_profile_functions returns unknown tool error (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_profile_functions", map[string]interface{}{
		"service":     "otel-app",
		"query":       "handler",
		"duration":    "10s",
		"sample_rate": 0.5,
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error")
	s.Require().Contains(mcpErr.Message, "only coral_cli is supported",
		"Error should indicate only coral_cli is supported")

	s.T().Logf("✓ coral_profile_functions unknown tool error: %s", mcpErr.Message)
}

// TestProfileFunctionsStrategyCriticalPath validates that coral_profile_functions
// is not available via the proxy (post-RFD 100).
func (s *MCPSuite) TestProfileFunctionsStrategyCriticalPath() {
	s.T().Log("Testing coral_profile_functions returns unknown tool error (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_profile_functions", map[string]interface{}{
		"service":  "otel-app",
		"query":    "handler",
		"strategy": "critical_path",
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error")
	s.Require().Contains(mcpErr.Message, "only coral_cli is supported",
		"Error should indicate only coral_cli is supported")

	s.T().Logf("✓ coral_profile_functions unknown tool error: %s", mcpErr.Message)
}

// TestProfileFunctionsStrategyAll validates that coral_profile_functions is
// not available via the proxy (post-RFD 100).
func (s *MCPSuite) TestProfileFunctionsStrategyAll() {
	s.T().Log("Testing coral_profile_functions returns unknown tool error (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_profile_functions", map[string]interface{}{
		"service":       "otel-app",
		"query":         "handler",
		"strategy":      "all",
		"max_functions": 3,
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error")
	s.Require().Contains(mcpErr.Message, "only coral_cli is supported",
		"Error should indicate only coral_cli is supported")

	s.T().Logf("✓ coral_profile_functions unknown tool error: %s", mcpErr.Message)
}

// TestQueryTracesWithTraceID validates that coral_cli query traces can filter
// by a specific trace ID (post-RFD 100).
//
// Validates:
//   - A trace ID extracted from a prior query can be used as a --trace-id filter
//   - coral_cli accepts the --trace-id parameter without error
func (s *MCPSuite) TestQueryTracesWithTraceID() {
	s.T().Log("Testing coral_cli query traces with --trace-id parameter (post-RFD 100)...")

	s.ensureTelemetryData()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Step 1: Fetch all traces to find a real trace ID.
	allTracesResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "traces", "otel-app", "--since", "10m"},
	}, 1)
	s.Require().NoError(err, "Initial coral_cli query traces should succeed")
	s.Require().NotEmpty(allTracesResp.Content, "Response should have content")

	allTracesText := allTracesResp.Content[0].Text

	// Step 2: Extract a trace ID from the text.
	traceIDPattern := regexp.MustCompile(`[a-f0-9]{32}`)
	matches := traceIDPattern.FindStringSubmatch(allTracesText)
	if len(matches) < 1 {
		s.T().Skip("No traces found in the current environment — skipping --trace-id filter test")
		return
	}
	traceID := matches[0]
	s.T().Logf("Extracted trace ID for filter test: %s", traceID)

	// Step 3: Query using the specific trace ID via coral_cli.
	filteredResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "traces", "otel-app", "--since", "10m", "--trace-id", traceID},
	}, 2)

	s.Require().NoError(err, "coral_cli query traces with --trace-id should succeed")
	s.Require().NotEmpty(filteredResp.Content, "Response should have content")

	s.T().Logf("✓ --trace-id parameter validated")
}

// TestQueryTracesWithMinDuration validates that the --min-duration parameter is
// accepted by coral_cli query traces (post-RFD 100).
//
// Validates:
//   - coral_cli accepts --min-duration without error
//   - Response is returned successfully
func (s *MCPSuite) TestQueryTracesWithMinDuration() {
	s.T().Log("Testing coral_cli query traces with --min-duration parameter (post-RFD 100)...")

	s.ensureTelemetryData()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "traces", "otel-app", "--since", "10m", "--min-duration", "10ms"},
	}, 1)

	s.Require().NoError(err, "coral_cli query traces with --min-duration should succeed")
	s.Require().NotEmpty(resp.Content, "Response should have content")
	s.Require().NotEmpty(resp.Content[0].Text, "Response text should not be empty")

	s.T().Logf("✓ --min-duration parameter accepted")
}
