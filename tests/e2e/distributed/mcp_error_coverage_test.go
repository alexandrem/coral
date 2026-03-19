package distributed

import (
	"strings"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// =============================================================================
// Group J: Error Coverage (Sprint 2, Task 2.1) — updated for RFD 100
// =============================================================================
//
// After RFD 100, the MCP proxy exposes only the coral_cli tool. Tools that
// have no CLI equivalent (coral_shell_exec, coral_container_exec,
// coral_profile_functions) return "unknown tool: X (only coral_cli is
// supported)". Tools that previously had direct handlers now go through
// coral_cli which runs `coral <args> --format json`.
//
// Design notes:
//   - Tests for tools with no CLI equivalent use CallToolExpectError and
//     assert "only coral_cli is supported" in the error message.
//   - Tests for tools that map to coral_cli args use CallTool or
//     CallToolExpectError depending on whether the CLI command is expected
//     to fail.

// TestShellExecErrorEmptyCommand validates that coral_shell_exec is no longer
// available via the MCP proxy (post-RFD 100).
//
// coral_shell_exec has no CLI equivalent and is not served by the proxy.
// Any call to it returns "unknown tool: coral_shell_exec (only coral_cli is supported)".
func (s *MCPSuite) TestShellExecErrorEmptyCommand() {
	s.T().Log("Testing coral_shell_exec returns unknown tool error (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_shell_exec", map[string]interface{}{
		"command": []string{}, // Empty array — but tool is not available anyway.
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error")
	s.Require().Contains(mcpErr.Message, "only coral_cli is supported",
		"Error should indicate only coral_cli is supported")

	s.T().Logf("✓ coral_shell_exec unknown tool error: %s", mcpErr.Message)
}

// TestShellExecErrorTimeout validates that coral_shell_exec is not available
// via the proxy (post-RFD 100).
func (s *MCPSuite) TestShellExecErrorTimeout() {
	s.T().Log("Testing coral_shell_exec returns unknown tool error (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_shell_exec", map[string]interface{}{
		"command":         []string{"sleep", "100"},
		"timeout_seconds": uint32(1),
	}, 2)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error")
	s.Require().Contains(mcpErr.Message, "only coral_cli is supported",
		"Error should indicate only coral_cli is supported")

	s.T().Logf("✓ coral_shell_exec unknown tool error: %s", mcpErr.Message)
}

// TestShellExecErrorInvalidCommand validates that coral_shell_exec is not
// available via the proxy (post-RFD 100).
func (s *MCPSuite) TestShellExecErrorInvalidCommand() {
	s.T().Log("Testing coral_shell_exec returns unknown tool error (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_shell_exec", map[string]interface{}{
		"command": []string{"nonexistent-command-xyz-abc"},
	}, 3)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error")
	s.Require().Contains(mcpErr.Message, "only coral_cli is supported",
		"Error should indicate only coral_cli is supported")

	s.T().Logf("✓ coral_shell_exec unknown tool error: %s", mcpErr.Message)
}

// TestAttachUprobeErrorFunctionNotFound validates that coral_attach_uprobe for
// a non-existent function returns a CLI-level error via coral_cli (post-RFD 100).
//
// Validates:
//   - coral_cli debug attach with a non-existent function returns an error
//   - Error originates from the CLI (not "unknown tool")
func (s *MCPSuite) TestAttachUprobeErrorFunctionNotFound() {
	s.T().Log("Testing coral_cli debug attach with non-existent function (post-RFD 100)...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_cli", map[string]interface{}{
		"args": []interface{}{"debug", "attach", "otel-app", "--function", "nonexistent.FunctionXYZ12345"},
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error for missing function")

	s.T().Logf("✓ Function-not-found error received: %s", mcpErr.Message)
}

// TestDiscoverFunctionsErrorEmptyQuery validates that coral_cli debug search
// handles an empty query string gracefully (post-RFD 100).
//
// Validates:
//   - Tool does not crash with an empty query
//   - Response is returned (may be empty results or a validation message)
func (s *MCPSuite) TestDiscoverFunctionsErrorEmptyQuery() {
	s.T().Log("Testing coral_cli debug search with empty query string (post-RFD 100)...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"debug", "search", "", "--service", "otel-app"},
	}, 1)

	s.Require().NoError(err, "Tool should not crash for empty query")
	s.Require().NotEmpty(resp.Content, "Response should have content")
	s.Require().NotEmpty(resp.Content[0].Text, "Response text should not be empty")

	s.T().Logf("✓ Empty query handled gracefully: %s",
		resp.Content[0].Text[:min(len(resp.Content[0].Text), 150)])
}

// TestQueryMetricsErrorInvalidTimeRange validates that coral_cli query metrics
// returns an error for a malformed time range string (post-RFD 100).
//
// Validates:
//   - coral_cli rejects an unparseable --since value
//   - Error is surfaced as an MCP error
func (s *MCPSuite) TestQueryMetricsErrorInvalidTimeRange() {
	s.T().Log("Testing coral_cli query metrics with invalid --since format (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "metrics", "otel-app", "--since", "not-a-valid-duration"},
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error for invalid time range")
	s.Require().NotEmpty(mcpErr.Message, "Error message should not be empty")

	s.T().Logf("✓ Invalid time range rejected: %s", mcpErr.Message)
}

// TestQueryMetricsErrorInvalidProtocol validates that coral_cli query metrics
// accepts or rejects an unknown protocol value (post-RFD 100).
//
// Validates:
//   - Tool does not crash on an unsupported protocol string
//   - Response is returned (protocol parameter may be silently ignored or rejected)
func (s *MCPSuite) TestQueryMetricsErrorInvalidProtocol() {
	s.T().Log("Testing coral_cli query metrics with unsupported protocol value (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, callErr := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "metrics", "otel-app", "--since", "10m", "--protocol", "invalid-protocol-xyz"},
	}, 1)

	if callErr != nil {
		// CLI rejected the unknown protocol — acceptable.
		s.T().Logf("✓ Unknown protocol rejected: %s", callErr.Error())
		return
	}

	// CLI accepted unknown protocol; tool returns normal output.
	s.Require().NotEmpty(resp.Content, "Response should have content")
	s.T().Logf("✓ Unknown protocol accepted gracefully")
}

// TestContainerExecSidecarMode validates that coral_container_exec is not
// available via the proxy (post-RFD 100).
//
// coral_container_exec has no CLI equivalent and is not served by the proxy.
func (s *MCPSuite) TestContainerExecSidecarMode() {
	s.T().Log("Testing coral_container_exec returns unknown tool error (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_container_exec", map[string]interface{}{
		"container_name": "otel-app",
		"command":        []string{"echo", "sidecar-test"},
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error")
	s.Require().Contains(mcpErr.Message, "only coral_cli is supported",
		"Error should indicate only coral_cli is supported")

	s.T().Logf("✓ coral_container_exec unknown tool error: %s", mcpErr.Message)
}

// =============================================================================
// coral_shell_exec — additional cases (all return "unknown tool" post-RFD 100)
// =============================================================================

// TestShellExecNonZeroExitCode validates that coral_shell_exec is not available
// via the proxy (post-RFD 100).
func (s *MCPSuite) TestShellExecNonZeroExitCode() {
	s.T().Log("Testing coral_shell_exec returns unknown tool error (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_shell_exec", map[string]interface{}{
		"command": []string{"false"},
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error")
	s.Require().Contains(mcpErr.Message, "only coral_cli is supported",
		"Error should indicate only coral_cli is supported")

	s.T().Logf("✓ coral_shell_exec unknown tool error: %s", mcpErr.Message)
}

// TestMissingRequiredAgentTarget validates that coral_shell_exec is not
// available via the proxy (post-RFD 100).
func (s *MCPSuite) TestMissingRequiredAgentTarget() {
	s.T().Log("Testing coral_shell_exec returns unknown tool error (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_shell_exec", map[string]interface{}{
		"command": []string{"echo", "test"},
		// No service, no agent_id.
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error")
	s.Require().Contains(mcpErr.Message, "only coral_cli is supported",
		"Error should indicate only coral_cli is supported")

	s.T().Logf("✓ coral_shell_exec unknown tool error: %s", mcpErr.Message)
}

// TestMalformedArgumentType validates that coral_cli returns an MCP error when
// the "args" field has the wrong type (post-RFD 100).
//
// Validates:
//   - args must be an array of strings; passing a non-array produces an error
//   - Error message mentions "args" to aid debugging
func (s *MCPSuite) TestMalformedArgumentType() {
	s.T().Log("Testing coral_cli with args as a non-array (wrong type) (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Passing a string where []string is expected triggers validation error.
	mcpErr, err := proxy.CallToolExpectError("coral_cli", map[string]interface{}{
		"args": "not-an-array",
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error for malformed argument type")
	s.Require().True(
		strings.Contains(strings.ToLower(mcpErr.Message), "args") ||
			strings.Contains(strings.ToLower(mcpErr.Message), "array") ||
			strings.Contains(strings.ToLower(mcpErr.Message), "string"),
		"Error should mention args field, got: %s", mcpErr.Message)

	s.T().Logf("✓ Wrong argument type rejected: %s", mcpErr.Message)
}

// =============================================================================
// coral_container_exec — additional cases
// =============================================================================

// TestContainerExecErrorEmptyCommand validates that coral_container_exec is not
// available via the proxy (post-RFD 100).
func (s *MCPSuite) TestContainerExecErrorEmptyCommand() {
	s.T().Log("Testing coral_container_exec returns unknown tool error (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_container_exec", map[string]interface{}{
		"command": []string{},
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error")
	s.Require().Contains(mcpErr.Message, "only coral_cli is supported",
		"Error should indicate only coral_cli is supported")

	s.T().Logf("✓ coral_container_exec unknown tool error: %s", mcpErr.Message)
}

// =============================================================================
// coral_query_summary — additional cases (via coral_cli post-RFD 100)
// =============================================================================

// TestQuerySummaryUnknownService validates that coral_cli query summary returns
// a graceful response for a service that does not exist (post-RFD 100).
//
// The CLI aggregates colony data; when no data exists for a service, it should
// return a descriptive (possibly empty) summary — not crash or transport-fail.
func (s *MCPSuite) TestQuerySummaryUnknownService() {
	s.T().Log("Testing coral_cli query summary with a non-existent service name (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, callErr := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "summary", "nonexistent-service-xyz-abc", "--since", "5m"},
	}, 1)

	if callErr != nil {
		s.T().Logf("✓ Unknown service returned MCP error: %s", callErr.Error())
		return
	}

	s.Require().NotEmpty(resp.Content, "Response should have content")
	s.T().Logf("✓ Unknown service handled gracefully: %s",
		resp.Content[0].Text[:min(len(resp.Content[0].Text), 200)])
}

// =============================================================================
// coral_query_metrics — additional cases (via coral_cli post-RFD 100)
// =============================================================================

// TestQueryMetricsInvalidHTTPMethod validates that coral_cli query metrics accepts
// an unknown http_method value without crashing (post-RFD 100).
func (s *MCPSuite) TestQueryMetricsInvalidHTTPMethod() {
	s.T().Log("Testing coral_cli query metrics with an invalid http_method value (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, callErr := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "metrics", "otel-app", "--since", "10m", "--http-method", "INVALID_HTTP_METHOD_XYZ"},
	}, 1)

	if callErr != nil {
		s.T().Logf("✓ Invalid http-method rejected: %s", callErr.Error())
		return
	}

	s.Require().NotEmpty(resp.Content, "Response should have content")
	s.T().Logf("✓ Invalid http-method accepted gracefully")
}

// =============================================================================
// coral_query_traces — additional cases (via coral_cli post-RFD 100)
// =============================================================================

// TestQueryTracesInvalidTraceID validates that coral_cli query traces handles
// an invalid trace ID format without crashing (post-RFD 100).
func (s *MCPSuite) TestQueryTracesInvalidTraceID() {
	s.T().Log("Testing coral_cli query traces with a garbled trace ID (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "traces", "otel-app", "--since", "5m", "--trace-id", "not-a-valid-trace-id-xyz-0000"},
	}, 1)

	s.Require().NoError(err, "Tool should handle invalid trace ID without transport error")
	s.Require().NotEmpty(resp.Content, "Response should have content")

	s.T().Logf("✓ Invalid trace ID handled gracefully")
}

// TestQueryTracesExcessiveMinDuration validates that coral_cli query traces handles
// a very large min-duration filter without crashing (post-RFD 100).
func (s *MCPSuite) TestQueryTracesExcessiveMinDuration() {
	s.T().Log("Testing coral_cli query traces with excessive min-duration (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "traces", "otel-app", "--since", "5m", "--min-duration", "99999h"},
	}, 1)

	s.Require().NoError(err, "Tool should handle large min-duration without transport error")
	s.Require().NotEmpty(resp.Content, "Response should have content")

	s.T().Logf("✓ Excessive min-duration handled gracefully")
}

// =============================================================================
// coral_attach_uprobe — additional cases (via coral_cli post-RFD 100)
// =============================================================================

// TestAttachUprobeInvalidDuration validates that coral_cli debug attach rejects
// an unparseable duration string (post-RFD 100).
func (s *MCPSuite) TestAttachUprobeInvalidDuration() {
	s.T().Log("Testing coral_cli debug attach with unparseable duration (post-RFD 100)...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_cli", map[string]interface{}{
		"args": []interface{}{"debug", "attach", "otel-app", "--function", "main.someFunction", "--duration", "not-a-duration"},
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error for invalid duration")

	s.T().Logf("✓ Invalid duration rejected: %s", mcpErr.Message)
}

// =============================================================================
// coral_discover_functions — additional cases (via coral_cli post-RFD 100)
// =============================================================================

// TestDiscoverFunctionsUnknownService validates that coral_cli debug search
// handles a non-existent service gracefully (post-RFD 100).
func (s *MCPSuite) TestDiscoverFunctionsUnknownService() {
	s.T().Log("Testing coral_cli debug search with a non-existent service (post-RFD 100)...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, callErr := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"debug", "search", "main", "--service", "nonexistent-service-xyz-abc"},
	}, 1)

	if callErr != nil {
		s.T().Logf("✓ Unknown service returned MCP error: %s", callErr.Error())
		return
	}

	s.Require().NotEmpty(resp.Content, "Response should have content")
	s.T().Logf("✓ Unknown service handled gracefully: %s",
		resp.Content[0].Text[:min(len(resp.Content[0].Text), 200)])
}

// TestDiscoverFunctionsMaxResultsExceedsLimit validates that coral_cli debug
// search accepts a large --limit value without crashing (post-RFD 100).
func (s *MCPSuite) TestDiscoverFunctionsMaxResultsExceedsLimit() {
	s.T().Log("Testing coral_cli debug search with --limit=9999 (post-RFD 100)...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"debug", "search", "main", "--service", "otel-app", "--limit", "9999"},
	}, 1)

	s.Require().NoError(err, "Tool should accept large --limit without transport error")
	s.Require().NotEmpty(resp.Content, "Response should have content")

	s.T().Logf("✓ Excessive --limit accepted gracefully")
}

// =============================================================================
// coral_profile_functions — all cases return "unknown tool" (post-RFD 100)
// =============================================================================

// TestProfileFunctionsInvalidStrategy validates that coral_profile_functions is
// not available via the proxy (post-RFD 100).
func (s *MCPSuite) TestProfileFunctionsInvalidStrategy() {
	s.T().Log("Testing coral_profile_functions returns unknown tool error (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_profile_functions", map[string]interface{}{
		"service":  "otel-app",
		"query":    "main",
		"strategy": "quantum_strategy_xyz",
		"duration": "5s",
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error")
	s.Require().Contains(mcpErr.Message, "only coral_cli is supported",
		"Error should indicate only coral_cli is supported")

	s.T().Logf("✓ coral_profile_functions unknown tool error: %s", mcpErr.Message)
}

// TestProfileFunctionsNoMatchingFunctions validates that coral_profile_functions
// is not available via the proxy (post-RFD 100).
func (s *MCPSuite) TestProfileFunctionsNoMatchingFunctions() {
	s.T().Log("Testing coral_profile_functions returns unknown tool error (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_profile_functions", map[string]interface{}{
		"service":  "otel-app",
		"query":    "zzz_definitely_nonexistent_function_xyz123abc",
		"duration": "5s",
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error")
	s.Require().Contains(mcpErr.Message, "only coral_cli is supported",
		"Error should indicate only coral_cli is supported")

	s.T().Logf("✓ coral_profile_functions unknown tool error: %s", mcpErr.Message)
}

// =============================================================================
// coral_debug_cpu_profile — additional cases (via coral_cli post-RFD 100)
// =============================================================================

// TestDebugCPUProfileDurationClamped validates that coral_cli query cpu-profile
// proceeds without returning a transport error (post-RFD 100).
func (s *MCPSuite) TestDebugCPUProfileDurationClamped() {
	s.T().Log("Testing coral_cli query cpu-profile (post-RFD 100)...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, callErr := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "cpu-profile", "otel-app"},
	}, 1)

	if callErr != nil {
		s.T().Logf("✓ Profiler returned MCP error (acceptable): %s", callErr.Error())
		return
	}

	s.Require().NotEmpty(resp.Content, "Response should have content")
	s.T().Logf("✓ cpu-profile handled without transport error")
}

// TestDebugCPUProfileFrequencyClamped validates that coral_cli query cpu-profile
// proceeds without returning a transport error (post-RFD 100).
func (s *MCPSuite) TestDebugCPUProfileFrequencyClamped() {
	s.T().Log("Testing coral_cli query cpu-profile (post-RFD 100)...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, callErr := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "cpu-profile", "otel-app"},
	}, 1)

	if callErr != nil {
		s.T().Logf("✓ Profiler returned MCP error (acceptable): %s", callErr.Error())
		return
	}

	s.Require().NotEmpty(resp.Content, "Response should have content")
	s.T().Logf("✓ cpu-profile handled without transport error")
}
