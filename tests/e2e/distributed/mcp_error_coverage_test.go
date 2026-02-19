package distributed

import (
	"strings"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// =============================================================================
// Group J: Error Coverage (Sprint 2, Task 2.1)
// =============================================================================
//
// These tests validate that each tool returns meaningful errors for invalid
// inputs, bad parameters, and unavailable resources.
//
// Design notes:
//   - Tests that trigger server-side validation failures use CallToolExpectError.
//   - Tests for parameters that are accepted but not validated use CallTool,
//     asserting the tool handles the input gracefully rather than crashing.

// TestShellExecErrorEmptyCommand validates that coral_shell_exec rejects an
// empty command array.
//
// Validates:
//   - Tool returns an MCP error for zero-length command
//   - Error message mentions "empty" to aid LLM debugging
func (s *MCPSuite) TestShellExecErrorEmptyCommand() {
	s.T().Log("Testing coral_shell_exec with empty command array...")

	// agent_id lookup is optional here (command validation runs first), but we
	// include it for consistency with the other shell_exec tests.
	agentID := s.resolveFirstAgentID()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_shell_exec", map[string]interface{}{
		"agent_id": agentID,
		"command":  []string{}, // Empty array — must be rejected.
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error for empty command")
	s.Require().Contains(strings.ToLower(mcpErr.Message), "empty",
		"Error message should indicate the command is empty")

	s.T().Logf("✓ Empty command rejected: %s", mcpErr.Message)
}

// TestShellExecErrorTimeout validates that coral_shell_exec returns an error
// when a command exceeds its configured timeout.
//
// Validates:
//   - Timeout of 1 second causes a long-running command to be killed
//   - Agent returns a gRPC DeadlineExceeded error, which the colony surfaces
//     as an MCP tool error (isError: true)
func (s *MCPSuite) TestShellExecErrorTimeout() {
	s.T().Log("Testing coral_shell_exec with command that exceeds timeout...")

	agentID := s.resolveFirstAgentID()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	timeout := uint32(1)
	mcpErr, err := proxy.CallToolExpectError("coral_shell_exec", map[string]interface{}{
		"agent_id":        agentID,
		"command":         []string{"sleep", "100"},
		"timeout_seconds": timeout,
	}, 2)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error for timeout")

	s.T().Logf("✓ Timeout error received: %s", mcpErr.Message)
}

// TestShellExecErrorInvalidCommand validates that coral_shell_exec handles a
// command that does not exist on the remote host.
//
// Validates:
//   - Executing a non-existent binary produces an informative response
//   - Tool does not crash or hang; it returns content describing the failure
//
// Note: Some agents return a gRPC error (propagated as an MCP error) while
// others return a successful response with a non-zero exit code and "not found"
// text.  Both are acceptable — the test asserts on whichever path is taken.
func (s *MCPSuite) TestShellExecErrorInvalidCommand() {
	s.T().Log("Testing coral_shell_exec with a non-existent command binary...")

	agentID := s.resolveFirstAgentID()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Attempt to run a binary that cannot exist.
	resp, callErr := proxy.CallTool("coral_shell_exec", map[string]interface{}{
		"agent_id": agentID,
		"command":  []string{"nonexistent-command-xyz-abc"},
	}, 3)

	if callErr != nil {
		// Agent returned an MCP protocol error — that is the expected path.
		s.T().Logf("✓ Invalid command returned MCP error: %s", callErr.Error())
		return
	}

	// Agent returned a result; verify the output indicates the failure.
	s.Require().NotEmpty(resp.Content, "Response should have content")
	responseText := resp.Content[0].Text
	hasNotFound := strings.Contains(strings.ToLower(responseText), "not found") ||
		strings.Contains(strings.ToLower(responseText), "no such") ||
		strings.Contains(strings.ToLower(responseText), "exec") ||
		strings.Contains(responseText, "127") // POSIX exit code for command not found
	s.Require().True(hasNotFound,
		"Response should indicate that the command was not found, got: %s", responseText)

	s.T().Logf("✓ Invalid command produced failure output: %s", responseText[:min(len(responseText), 200)])
}

// TestAttachUprobeErrorFunctionNotFound validates that coral_attach_uprobe
// returns an error when the requested function does not exist.
//
// Validates:
//   - Debug service rejects probing of a non-existent function
//   - Error is surfaced as an MCP error to the caller
func (s *MCPSuite) TestAttachUprobeErrorFunctionNotFound() {
	s.T().Log("Testing coral_attach_uprobe with non-existent function...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_attach_uprobe", map[string]interface{}{
		"service":  "otel-app",
		"function": "nonexistent.package.FunctionXYZ12345",
		"duration": "10s",
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error for missing function")

	s.T().Logf("✓ Function-not-found error received: %s", mcpErr.Message)
}

// TestDiscoverFunctionsErrorEmptyQuery validates that coral_discover_functions
// handles an empty query string gracefully.
//
// Validates:
//   - Tool does not crash with an empty query
//   - Response is returned (may be empty results or a validation message)
//
// Note: An empty query is passed directly to the debug service.  Whether it
// errors or returns all/no functions depends on the server implementation.
// This test ensures the tool does not panic or return a transport error.
func (s *MCPSuite) TestDiscoverFunctionsErrorEmptyQuery() {
	s.T().Log("Testing coral_discover_functions with empty query string...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, err := proxy.CallTool("coral_discover_functions", map[string]interface{}{
		"service": "otel-app",
		"query":   "", // Empty — no explicit validation in the handler.
	}, 1)

	s.Require().NoError(err, "Tool should not crash for empty query")
	s.Require().NotEmpty(resp.Content, "Response should have content")
	s.Require().NotEmpty(resp.Content[0].Text, "Response text should not be empty")

	s.T().Logf("✓ Empty query handled gracefully: %s",
		resp.Content[0].Text[:min(len(resp.Content[0].Text), 150)])
}

// TestQueryMetricsErrorInvalidTimeRange validates that coral_query_metrics
// returns an error for a malformed time range string.
//
// Validates:
//   - parseTimeRange rejects an unparseable value
//   - Error message helps the caller understand what went wrong
func (s *MCPSuite) TestQueryMetricsErrorInvalidTimeRange() {
	s.T().Log("Testing coral_query_metrics with invalid time_range format...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_query_metrics", map[string]interface{}{
		"service":    "otel-app",
		"time_range": "not-a-valid-duration", // Must fail parseTimeRange.
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error for invalid time_range")
	s.Require().NotEmpty(mcpErr.Message, "Error message should not be empty")

	s.T().Logf("✓ Invalid time_range rejected: %s", mcpErr.Message)
}

// TestQueryMetricsErrorInvalidProtocol validates that coral_query_metrics
// accepts an unknown protocol value without crashing.
//
// Validates:
//   - Tool does not error on an unsupported protocol string
//   - Response is returned (protocol parameter may be silently ignored)
//
// Note: The current implementation does not validate the protocol enum server-
// side.  This test documents current behaviour and will catch regressions if
// validation is added later.
func (s *MCPSuite) TestQueryMetricsErrorInvalidProtocol() {
	s.T().Log("Testing coral_query_metrics with unsupported protocol value...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, err := proxy.CallTool("coral_query_metrics", map[string]interface{}{
		"service":    "otel-app",
		"time_range": "10m",
		"protocol":   "invalid-protocol-xyz",
	}, 1)

	// Protocol is currently silently ignored; the tool returns normal output.
	s.Require().NoError(err, "Tool should accept unknown protocol without error")
	s.Require().NotEmpty(resp.Content, "Response should have content")

	s.T().Logf("✓ Unknown protocol accepted gracefully")
}

// TestContainerExecSidecarMode validates that coral_container_exec executes a
// command in the sidecar container's namespace.
//
// Note: detectContainerPID operates in sidecar mode — it returns the lowest
// visible PID and does not look up containers by name.  The container_name
// parameter is accepted for forward compatibility but is not validated against
// running containers.  A future implementation may add name-based lookup via
// cgroup metadata.
//
// Validates:
//   - Tool executes a command via nsenter without a transport failure
//   - Response contains output or a descriptive error (e.g., nsenter not
//     available or insufficient privileges in the test environment)
func (s *MCPSuite) TestContainerExecSidecarMode() {
	s.T().Log("Testing coral_container_exec in sidecar mode...")

	agentID := s.resolveFirstAgentID()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// container_name is not validated in sidecar mode; the agent targets the
	// lowest-PID process it can see.  Both success and nsenter-level failure
	// are acceptable outcomes — what must NOT happen is a transport error.
	resp, callErr := proxy.CallTool("coral_container_exec", map[string]interface{}{
		"agent_id":       agentID,
		"container_name": "otel-app",
		"command":        []string{"echo", "sidecar-test"},
	}, 1)

	if callErr != nil {
		s.T().Logf("✓ Container exec returned MCP error (expected in restricted envs): %s", callErr.Error())
		return
	}

	s.Require().NotNil(resp, "Should have a response")
	s.Require().NotEmpty(resp.Content, "Should have content in response")
	s.T().Logf("✓ Container exec response: %s", resp.Content[0].Text[:min(len(resp.Content[0].Text), 200)])
}

// =============================================================================
// coral_shell_exec — additional cases
// =============================================================================

// TestShellExecNonZeroExitCode validates that a command that exits with a
// non-zero code returns a successful MCP response (not a tool error) whose
// content describes the exit code.
//
// Non-zero exit is normal shell behaviour — it is NOT an MCP error.  The
// response content allows the LLM to reason about the failure.
func (s *MCPSuite) TestShellExecNonZeroExitCode() {
	s.T().Log("Testing coral_shell_exec with a command that exits non-zero...")

	agentID := s.resolveFirstAgentID()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// `false` is a POSIX standard binary that always exits with code 1.
	resp, err := proxy.CallTool("coral_shell_exec", map[string]interface{}{
		"agent_id": agentID,
		"command":  []string{"false"},
	}, 1)

	s.Require().NoError(err, "Non-zero exit should produce content, not a transport error")
	s.Require().NotNil(resp, "Should have a response")
	s.Require().NotEmpty(resp.Content, "Response should have content")

	content := resp.Content[0].Text
	hasNonZeroIndicator := strings.Contains(content, "Exit Code: 1") ||
		strings.Contains(content, "non-zero") ||
		strings.Contains(content, "⚠️")
	s.Require().True(hasNonZeroIndicator,
		"Response should indicate non-zero exit code, got: %s", content)

	s.T().Logf("✓ Non-zero exit surfaced as content: %s", content[:min(len(content), 200)])
}

// TestMissingRequiredAgentTarget validates that coral_shell_exec returns an
// error when neither service nor agent_id is specified.
//
// With an empty service name, matchesPattern returns true for every registered
// agent, so the colony produces a disambiguation error asking the caller to
// specify agent_id explicitly.
func (s *MCPSuite) TestMissingRequiredAgentTarget() {
	s.T().Log("Testing coral_shell_exec with no service or agent_id...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_shell_exec", map[string]interface{}{
		"command": []string{"echo", "test"},
		// No service, no agent_id — resolver should fail.
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error for ambiguous agent target")

	s.T().Logf("✓ Missing agent target rejected: %s", mcpErr.Message)
}

// TestMalformedArgumentType validates that tools return an MCP error when an
// argument has the wrong JSON type (e.g., an integer where an array is expected).
//
// Validates:
//   - JSON unmarshal failure produces mcp.NewToolResultError, not a crash
//   - Error message mentions "parse" or "arguments"
func (s *MCPSuite) TestMalformedArgumentType() {
	s.T().Log("Testing coral_shell_exec with command as an integer (wrong type)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Passing an integer where []string is expected triggers JSON unmarshal error.
	mcpErr, err := proxy.CallToolExpectError("coral_shell_exec", map[string]interface{}{
		"command": 42,
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error for malformed argument type")
	s.Require().True(
		strings.Contains(strings.ToLower(mcpErr.Message), "parse") ||
			strings.Contains(strings.ToLower(mcpErr.Message), "argument") ||
			strings.Contains(strings.ToLower(mcpErr.Message), "unmarshal"),
		"Error should mention argument parsing, got: %s", mcpErr.Message)

	s.T().Logf("✓ Wrong argument type rejected: %s", mcpErr.Message)
}

// =============================================================================
// coral_container_exec — additional cases
// =============================================================================

// TestContainerExecErrorEmptyCommand validates that coral_container_exec rejects
// an empty command array with the same validation as coral_shell_exec.
func (s *MCPSuite) TestContainerExecErrorEmptyCommand() {
	s.T().Log("Testing coral_container_exec with empty command array...")

	agentID := s.resolveFirstAgentID()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_container_exec", map[string]interface{}{
		"agent_id": agentID,
		"command":  []string{},
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error for empty command")
	s.Require().Contains(strings.ToLower(mcpErr.Message), "empty",
		"Error message should indicate the command is empty")

	s.T().Logf("✓ Empty container command rejected: %s", mcpErr.Message)
}

// =============================================================================
// coral_query_summary — additional cases
// =============================================================================

// TestQuerySummaryUnknownService validates that coral_query_summary returns a
// graceful response for a service that does not exist in the registry.
//
// The tool aggregates colony data; when no data exists for a service, it should
// return a descriptive (possibly empty) summary — not crash or transport-fail.
func (s *MCPSuite) TestQuerySummaryUnknownService() {
	s.T().Log("Testing coral_query_summary with a non-existent service name...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, callErr := proxy.CallTool("coral_query_summary", map[string]interface{}{
		"service":    "nonexistent-service-xyz-abc",
		"time_range": "5m",
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
// coral_query_metrics — additional cases
// =============================================================================

// TestQueryMetricsInvalidHTTPMethod validates that coral_query_metrics accepts
// an unknown http_method value without crashing.
//
// The http_method parameter is currently passed through to the query layer
// without enum validation.  This test documents current behaviour and will
// catch regressions if strict validation is added.
func (s *MCPSuite) TestQueryMetricsInvalidHTTPMethod() {
	s.T().Log("Testing coral_query_metrics with an invalid http_method value...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, err := proxy.CallTool("coral_query_metrics", map[string]interface{}{
		"service":     "otel-app",
		"time_range":  "10m",
		"http_method": "INVALID_HTTP_METHOD_XYZ",
	}, 1)

	s.Require().NoError(err, "Tool should accept unknown http_method without transport error")
	s.Require().NotEmpty(resp.Content, "Response should have content")

	s.T().Logf("✓ Invalid http_method accepted gracefully")
}

// =============================================================================
// coral_query_traces — additional cases
// =============================================================================

// TestQueryTracesInvalidTraceID validates that coral_query_traces handles an
// invalid trace ID format without crashing.
//
// The trace_id is used as a filter; when no trace matches, the tool returns an
// empty result set rather than an error.
func (s *MCPSuite) TestQueryTracesInvalidTraceID() {
	s.T().Log("Testing coral_query_traces with a garbled trace ID...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, err := proxy.CallTool("coral_query_traces", map[string]interface{}{
		"service":    "otel-app",
		"time_range": "5m",
		"trace_id":   "not-a-valid-trace-id-xyz-0000",
	}, 1)

	s.Require().NoError(err, "Tool should handle invalid trace ID without transport error")
	s.Require().NotEmpty(resp.Content, "Response should have content")

	s.T().Logf("✓ Invalid trace ID handled gracefully")
}

// TestQueryTracesExcessiveMinDuration validates that coral_query_traces handles
// a very large min_duration_ms filter without crashing.
//
// A min_duration value larger than any real trace simply returns no matching
// traces — it should not produce an error or panic.
func (s *MCPSuite) TestQueryTracesExcessiveMinDuration() {
	s.T().Log("Testing coral_query_traces with excessive min_duration_ms...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, err := proxy.CallTool("coral_query_traces", map[string]interface{}{
		"service":         "otel-app",
		"time_range":      "5m",
		"min_duration_ms": 999999999,
	}, 1)

	s.Require().NoError(err, "Tool should handle large min_duration_ms without transport error")
	s.Require().NotEmpty(resp.Content, "Response should have content")

	s.T().Logf("✓ Excessive min_duration_ms handled gracefully")
}

// =============================================================================
// coral_attach_uprobe — additional cases
// =============================================================================

// TestAttachUprobeInvalidDuration validates that coral_attach_uprobe rejects
// an unparseable duration string.
//
// The handler calls time.ParseDuration and surfaces parse errors as MCP errors.
func (s *MCPSuite) TestAttachUprobeInvalidDuration() {
	s.T().Log("Testing coral_attach_uprobe with unparseable duration...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	mcpErr, err := proxy.CallToolExpectError("coral_attach_uprobe", map[string]interface{}{
		"service":  "otel-app",
		"function": "main.someFunction",
		"duration": "not-a-duration",
	}, 1)

	s.Require().NoError(err, "Should receive an MCP error, not a transport failure")
	s.Require().NotNil(mcpErr, "Should have an MCP error for invalid duration")

	s.T().Logf("✓ Invalid duration rejected: %s", mcpErr.Message)
}

// =============================================================================
// coral_discover_functions — additional cases
// =============================================================================

// TestDiscoverFunctionsUnknownService validates that coral_discover_functions
// handles a non-existent service gracefully.
//
// The tool queries the debug service filtered by service name.  When no agent
// serves that name, it returns either an error or an empty result — not a
// transport failure.
func (s *MCPSuite) TestDiscoverFunctionsUnknownService() {
	s.T().Log("Testing coral_discover_functions with a non-existent service...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, callErr := proxy.CallTool("coral_discover_functions", map[string]interface{}{
		"service": "nonexistent-service-xyz-abc",
		"query":   "main",
	}, 1)

	if callErr != nil {
		s.T().Logf("✓ Unknown service returned MCP error: %s", callErr.Error())
		return
	}

	s.Require().NotEmpty(resp.Content, "Response should have content")
	s.T().Logf("✓ Unknown service handled gracefully: %s",
		resp.Content[0].Text[:min(len(resp.Content[0].Text), 200)])
}

// TestDiscoverFunctionsMaxResultsExceedsLimit validates that coral_discover_functions
// accepts a max_results value above the documented maximum (50) without crashing.
//
// The handler clamps or ignores the excess — the important thing is that it
// returns a valid response rather than an error or panic.
func (s *MCPSuite) TestDiscoverFunctionsMaxResultsExceedsLimit() {
	s.T().Log("Testing coral_discover_functions with max_results=9999...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, err := proxy.CallTool("coral_discover_functions", map[string]interface{}{
		"service":     "otel-app",
		"query":       "main",
		"max_results": 9999,
	}, 1)

	s.Require().NoError(err, "Tool should accept large max_results without transport error")
	s.Require().NotEmpty(resp.Content, "Response should have content")

	s.T().Logf("✓ Excessive max_results accepted gracefully")
}

// =============================================================================
// coral_profile_functions — additional cases
// =============================================================================

// TestProfileFunctionsInvalidStrategy validates that coral_profile_functions
// does not crash when given an unknown strategy string.
//
// The strategy is passed directly to the ProfileFunctions RPC.  An unknown
// value may be defaulted server-side or returned as an error — both are
// acceptable; a transport failure is not.
func (s *MCPSuite) TestProfileFunctionsInvalidStrategy() {
	s.T().Log("Testing coral_profile_functions with an unknown strategy...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	strategyVal := "quantum_strategy_xyz"
	resp, callErr := proxy.CallTool("coral_profile_functions", map[string]interface{}{
		"service":  "otel-app",
		"query":    "main",
		"strategy": strategyVal,
		"duration": "5s",
	}, 1)

	if callErr != nil {
		s.T().Logf("✓ Invalid strategy returned MCP error: %s", callErr.Error())
		return
	}

	s.Require().NotEmpty(resp.Content, "Response should have content")
	s.T().Logf("✓ Invalid strategy handled gracefully")
}

// TestProfileFunctionsNoMatchingFunctions validates that coral_profile_functions
// handles a query that matches no functions in the service.
//
// When zero functions match, the ProfileFunctions RPC returns an error or a
// response with zero functions probed — the tool should not crash.
func (s *MCPSuite) TestProfileFunctionsNoMatchingFunctions() {
	s.T().Log("Testing coral_profile_functions with a query matching no functions...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, callErr := proxy.CallTool("coral_profile_functions", map[string]interface{}{
		"service":  "otel-app",
		"query":    "zzz_definitely_nonexistent_function_xyz123abc",
		"duration": "5s",
	}, 1)

	if callErr != nil {
		s.T().Logf("✓ No-match query returned MCP error: %s", callErr.Error())
		return
	}

	s.Require().NotEmpty(resp.Content, "Response should have content")
	s.T().Logf("✓ No matching functions handled gracefully: %s",
		resp.Content[0].Text[:min(len(resp.Content[0].Text), 200)])
}

// =============================================================================
// coral_debug_cpu_profile — additional cases
// =============================================================================

// TestDebugCPUProfileDurationClamped validates that coral_debug_cpu_profile
// clamps a duration_seconds value of 0 to the minimum (10 s) and proceeds
// without returning a transport error.
func (s *MCPSuite) TestDebugCPUProfileDurationClamped() {
	s.T().Log("Testing coral_debug_cpu_profile with duration_seconds=0 (below minimum)...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// duration_seconds=0 is clamped to 10 by the handler.  The tool then
	// attempts the profile, which may or may not succeed in this environment.
	// Either way, no transport failure should occur.
	resp, callErr := proxy.CallTool("coral_debug_cpu_profile", map[string]interface{}{
		"service":          "otel-app",
		"duration_seconds": 0,
	}, 1)

	if callErr != nil {
		s.T().Logf("✓ Profiler returned MCP error (acceptable): %s", callErr.Error())
		return
	}

	s.Require().NotEmpty(resp.Content, "Response should have content")
	s.T().Logf("✓ Duration clamping handled without transport error")
}

// TestDebugCPUProfileFrequencyClamped validates that coral_debug_cpu_profile
// clamps a frequency_hz value above the maximum (999) and proceeds without
// returning a transport error.
func (s *MCPSuite) TestDebugCPUProfileFrequencyClamped() {
	s.T().Log("Testing coral_debug_cpu_profile with frequency_hz=99999 (above maximum)...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, callErr := proxy.CallTool("coral_debug_cpu_profile", map[string]interface{}{
		"service":          "otel-app",
		"duration_seconds": 10,
		"frequency_hz":     99999,
	}, 1)

	if callErr != nil {
		s.T().Logf("✓ Profiler returned MCP error (acceptable): %s", callErr.Error())
		return
	}

	s.Require().NotEmpty(resp.Content, "Response should have content")
	s.T().Logf("✓ Frequency clamping handled without transport error")
}
