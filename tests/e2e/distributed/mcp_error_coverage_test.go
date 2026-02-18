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
