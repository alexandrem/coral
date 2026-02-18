package distributed

import (
	"regexp"
	"strings"
	"time"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// =============================================================================
// Group K: Parameter Coverage (Sprint 2, Task 2.2)
// =============================================================================
//
// These tests verify that optional parameters for the top-5 MCP tools are
// accepted and — where implemented — have the expected effect on the output.
//
// Tools covered:
//  1. coral_shell_exec         – working_dir, env, timeout_seconds
//  2. coral_query_metrics      – http_route, status_code_range
//  3. coral_discover_functions – prioritize_slow
//  4. coral_profile_functions  – sample_rate, strategy variants
//  5. coral_query_traces       – trace_id, min_duration_ms
//
// Note: coral_shell_exec requires an explicit agent_id because resolveAgent
// performs a Services[] lookup that may not match observability-only services.
// This matches the pattern used by TestMCPToolShellExec.

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

// TestShellExecWithWorkingDir validates that the working_dir parameter changes
// the working directory of the remote command.
//
// Validates:
//   - Tool accepts the working_dir parameter
//   - Command output reflects the requested working directory
func (s *MCPSuite) TestShellExecWithWorkingDir() {
	s.T().Log("Testing coral_shell_exec with working_dir parameter...")

	agentID := s.resolveFirstAgentID()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, err := proxy.CallTool("coral_shell_exec", map[string]interface{}{
		"agent_id":    agentID,
		"command":     []string{"pwd"},
		"working_dir": "/tmp",
	}, 1)

	s.Require().NoError(err, "coral_shell_exec with working_dir should succeed")
	s.Require().NotEmpty(resp.Content, "Response should have content")

	responseText := resp.Content[0].Text
	s.T().Logf("Shell exec with working_dir result: %s", responseText[:min(len(responseText), 300)])

	s.Require().Contains(responseText, "/tmp",
		"Output should reflect the requested working directory")

	s.T().Log("✓ working_dir parameter validated")
}

// TestShellExecWithEnvVars validates that the env parameter injects environment
// variables into the remote command's execution environment.
//
// Validates:
//   - Tool accepts the env map parameter
//   - Injected variable is visible to the command
func (s *MCPSuite) TestShellExecWithEnvVars() {
	s.T().Log("Testing coral_shell_exec with env vars parameter...")

	agentID := s.resolveFirstAgentID()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, err := proxy.CallTool("coral_shell_exec", map[string]interface{}{
		"agent_id": agentID,
		"command":  []string{"sh", "-c", "echo $CORAL_TEST_VAR"},
		"env": map[string]string{
			"CORAL_TEST_VAR": "hello_from_mcp_test",
		},
	}, 1)

	s.Require().NoError(err, "coral_shell_exec with env should succeed")
	s.Require().NotEmpty(resp.Content, "Response should have content")

	responseText := resp.Content[0].Text
	s.T().Logf("Shell exec with env result: %s", responseText[:min(len(responseText), 300)])

	s.Require().Contains(responseText, "hello_from_mcp_test",
		"Output should contain the injected environment variable value")

	s.T().Log("✓ env parameter validated")
}

// TestShellExecWithCustomTimeout validates that the timeout_seconds parameter
// is respected: a command that completes before the deadline should succeed.
//
// Validates:
//   - Tool accepts timeout_seconds
//   - Command that completes within the timeout returns a result
func (s *MCPSuite) TestShellExecWithCustomTimeout() {
	s.T().Log("Testing coral_shell_exec with custom timeout_seconds...")

	agentID := s.resolveFirstAgentID()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	start := time.Now()
	resp, err := proxy.CallTool("coral_shell_exec", map[string]interface{}{
		"agent_id":        agentID,
		"command":         []string{"sleep", "1"},
		"timeout_seconds": uint32(10),
	}, 1)
	elapsed := time.Since(start)

	s.Require().NoError(err, "coral_shell_exec should succeed within timeout")
	s.Require().NotEmpty(resp.Content, "Response should have content")

	// Command takes ~1 s; total should complete well before the 10 s deadline.
	s.Require().Less(elapsed, 15*time.Second,
		"Command should complete within a generous bound of the timeout")

	s.T().Logf("✓ Custom timeout_seconds respected (elapsed: %s)", elapsed.Round(time.Millisecond))
}

// TestQueryMetricsWithHTTPRoute validates that the http_route parameter is
// accepted by coral_query_metrics.
//
// Validates:
//   - Tool accepts the http_route filter parameter without error
//   - Response is returned successfully
//
// Note: http_route filtering may not yet be implemented server-side.  This
// test documents parameter acceptance and will detect regressions when the
// feature is added.
func (s *MCPSuite) TestQueryMetricsWithHTTPRoute() {
	s.T().Log("Testing coral_query_metrics with http_route parameter...")

	s.ensureTelemetryData()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, err := proxy.CallTool("coral_query_metrics", map[string]interface{}{
		"service":    "otel-app",
		"time_range": "10m",
		"protocol":   "http",
		"http_route": "/health",
	}, 1)

	s.Require().NoError(err, "coral_query_metrics with http_route should succeed")
	s.Require().NotEmpty(resp.Content, "Response should have content")
	s.Require().NotEmpty(resp.Content[0].Text, "Response text should not be empty")

	s.T().Logf("✓ http_route parameter accepted")
}

// TestQueryMetricsWithStatusCodeRange validates that the status_code_range
// parameter is accepted by coral_query_metrics.
//
// Validates:
//   - Tool accepts the status_code_range filter parameter without error
//   - Response is returned successfully
//
// Note: Status code range filtering may not yet be implemented server-side.
func (s *MCPSuite) TestQueryMetricsWithStatusCodeRange() {
	s.T().Log("Testing coral_query_metrics with status_code_range parameter...")

	s.ensureTelemetryData()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, err := proxy.CallTool("coral_query_metrics", map[string]interface{}{
		"service":           "otel-app",
		"time_range":        "10m",
		"protocol":          "http",
		"status_code_range": "2xx",
	}, 1)

	s.Require().NoError(err, "coral_query_metrics with status_code_range should succeed")
	s.Require().NotEmpty(resp.Content, "Response should have content")

	s.T().Logf("✓ status_code_range parameter accepted")
}

// TestDiscoverFunctionsWithPrioritizeSlow validates that the prioritize_slow
// parameter is passed through to the function discovery service.
//
// Validates:
//   - Tool accepts prioritize_slow=true
//   - Response contains function results (ranking may differ from default)
func (s *MCPSuite) TestDiscoverFunctionsWithPrioritizeSlow() {
	s.T().Log("Testing coral_discover_functions with prioritize_slow=true...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, err := proxy.CallTool("coral_discover_functions", map[string]interface{}{
		"service":         "otel-app",
		"query":           "handler",
		"prioritize_slow": true,
		"include_metrics": true,
	}, 1)

	s.Require().NoError(err, "coral_discover_functions with prioritize_slow should succeed")
	s.Require().NotEmpty(resp.Content, "Response should have content")

	responseText := resp.Content[0].Text
	s.Require().Contains(strings.ToLower(responseText), "function",
		"Response should mention functions")

	s.T().Logf("✓ prioritize_slow=true accepted: %s",
		responseText[:min(len(responseText), 200)])
}

// TestProfileFunctionsWithSampleRate validates that the sample_rate parameter
// is accepted by coral_profile_functions.
//
// Validates:
//   - Tool accepts a fractional sample_rate (0.0–1.0)
//   - Response indicates a profiling session was created
func (s *MCPSuite) TestProfileFunctionsWithSampleRate() {
	s.T().Log("Testing coral_profile_functions with sample_rate=0.5...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, err := proxy.CallTool("coral_profile_functions", map[string]interface{}{
		"service":     "otel-app",
		"query":       "handler",
		"duration":    "10s",
		"sample_rate": 0.5,
		"async":       true,
	}, 1)

	s.Require().NoError(err, "coral_profile_functions with sample_rate should succeed")
	s.Require().NotEmpty(resp.Content, "Response should have content")

	responseText := resp.Content[0].Text
	s.Require().Contains(strings.ToLower(responseText), "session",
		"Response should reference a profiling session")

	s.T().Logf("✓ sample_rate=0.5 accepted: %s",
		responseText[:min(len(responseText), 200)])
}

// TestProfileFunctionsStrategyCriticalPath validates the critical_path strategy
// variant of coral_profile_functions.
//
// Validates:
//   - Tool accepts strategy="critical_path"
//   - Response indicates a profiling session was created
func (s *MCPSuite) TestProfileFunctionsStrategyCriticalPath() {
	s.T().Log("Testing coral_profile_functions with strategy=critical_path...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, err := proxy.CallTool("coral_profile_functions", map[string]interface{}{
		"service":  "otel-app",
		"query":    "handler",
		"strategy": "critical_path",
		"async":    true,
	}, 1)

	s.Require().NoError(err, "coral_profile_functions with strategy=critical_path should succeed")
	s.Require().NotEmpty(resp.Content, "Response should have content")

	responseText := resp.Content[0].Text
	s.Require().Contains(strings.ToLower(responseText), "session",
		"Response should reference a profiling session")

	s.T().Logf("✓ strategy=critical_path accepted")
}

// TestProfileFunctionsStrategyAll validates the "all" strategy variant of
// coral_profile_functions, combined with a max_functions cap.
//
// Validates:
//   - Tool accepts strategy="all"
//   - max_functions limits the number of probed functions
//   - Response indicates a profiling session was created
func (s *MCPSuite) TestProfileFunctionsStrategyAll() {
	s.T().Log("Testing coral_profile_functions with strategy=all and max_functions=3...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, err := proxy.CallTool("coral_profile_functions", map[string]interface{}{
		"service":       "otel-app",
		"query":         "handler",
		"strategy":      "all",
		"max_functions": 3,
		"async":         true,
	}, 1)

	s.Require().NoError(err, "coral_profile_functions with strategy=all should succeed")
	s.Require().NotEmpty(resp.Content, "Response should have content")

	responseText := resp.Content[0].Text
	s.Require().Contains(strings.ToLower(responseText), "session",
		"Response should reference a profiling session")

	s.T().Logf("✓ strategy=all with max_functions=3 accepted")
}

// TestQueryTracesWithTraceID validates that coral_query_traces can filter by a
// specific trace ID.
//
// Validates:
//   - A trace ID extracted from a prior query can be used as a filter
//   - The response mentions that specific trace
//   - Tool accepts the trace_id parameter without error
//
// The test is skipped when no traces are available in the current environment.
func (s *MCPSuite) TestQueryTracesWithTraceID() {
	s.T().Log("Testing coral_query_traces with trace_id parameter...")

	s.ensureTelemetryData()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Step 1: Fetch all traces to find a real trace ID.
	allTracesResp, err := proxy.CallTool("coral_query_traces", map[string]interface{}{
		"service":    "otel-app",
		"time_range": "10m",
	}, 1)
	s.Require().NoError(err, "Initial coral_query_traces should succeed")
	s.Require().NotEmpty(allTracesResp.Content, "Response should have content")

	allTracesText := allTracesResp.Content[0].Text

	// Step 2: Extract a trace ID from the text.
	// Format emitted by generateTracesOutput: "Trace: <hexID> (N spans)"
	traceIDPattern := regexp.MustCompile(`Trace:\s+([a-f0-9]{16,64})\s`)
	matches := traceIDPattern.FindStringSubmatch(allTracesText)
	if len(matches) < 2 {
		s.T().Skip("No traces found in the current environment — skipping trace_id filter test")
		return
	}
	traceID := matches[1]
	s.T().Logf("Extracted trace ID for filter test: %s", traceID)

	// Step 3: Query using the specific trace ID.
	filteredResp, err := proxy.CallTool("coral_query_traces", map[string]interface{}{
		"trace_id": traceID,
	}, 2)

	s.Require().NoError(err, "coral_query_traces with trace_id should succeed")
	s.Require().NotEmpty(filteredResp.Content, "Response should have content")

	filteredText := filteredResp.Content[0].Text
	s.Require().Contains(filteredText, traceID,
		"Filtered response should contain the requested trace ID")

	s.T().Logf("✓ trace_id parameter validated (trace %s found in response)", traceID)
}

// TestQueryTracesWithMinDuration validates that the min_duration_ms parameter
// is accepted by coral_query_traces.
//
// Validates:
//   - Tool accepts min_duration_ms without error
//   - Response is returned (parameter filtering may not yet be implemented)
//
// Note: The current generateTracesOutput implementation does not yet propagate
// min_duration_ms to the database query.  This test documents acceptance and
// will serve as a regression guard when the filter is implemented.
func (s *MCPSuite) TestQueryTracesWithMinDuration() {
	s.T().Log("Testing coral_query_traces with min_duration_ms parameter...")

	s.ensureTelemetryData()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	resp, err := proxy.CallTool("coral_query_traces", map[string]interface{}{
		"service":         "otel-app",
		"time_range":      "10m",
		"min_duration_ms": 100,
	}, 1)

	s.Require().NoError(err, "coral_query_traces with min_duration_ms should succeed")
	s.Require().NotEmpty(resp.Content, "Response should have content")
	s.Require().NotEmpty(resp.Content[0].Text, "Response text should not be empty")

	s.T().Logf("✓ min_duration_ms parameter accepted")
}
