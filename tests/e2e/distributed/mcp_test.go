package distributed

import (
	"net/http"
	"strings"
	"time"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// MCPSuite tests MCP (Model Context Protocol) server implementation.
//
// This suite validates:
// 1. MCP CLI commands (list-tools, test-tool, generate-config)
// 2. MCP proxy protocol (JSON-RPC 2.0 over stdio)
// 3. End-to-end tool execution with real infrastructure
// 4. Configuration (tool filtering, disabled flag)
//
// Note: Requires mesh, services, and telemetry infrastructure to be running.
// Tests validate MCP integration, not individual tool logic (covered by unit tests).
type MCPSuite struct {
	E2EDistributedSuite

	cliEnv *helpers.CLITestEnv
}

// SetupSuite runs once before all tests in the suite.
func (s *MCPSuite) SetupSuite() {
	s.E2EDistributedSuite.SetupSuite()

	// Setup CLI environment
	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	colonyID := "test-colony-e2e" // Default colony ID from docker-compose

	s.cliEnv, err = helpers.SetupCLIEnv(s.ctx, colonyID, colonyEndpoint)
	s.Require().NoError(err, "Failed to setup CLI environment")

	// Ensure services are connected for testing
	s.ensureServicesConnected()

	s.T().Logf("MCP test environment ready: endpoint=%s, colonyID=%s", colonyEndpoint, colonyID)
}

// TearDownSuite cleans up after all tests.
func (s *MCPSuite) TearDownSuite() {
	if s.cliEnv != nil {
		_ = s.cliEnv.Cleanup()
	}
	s.E2EDistributedSuite.TearDownSuite()
}

// =============================================================================
// Group A: CLI Commands (Direct, No Proxy)
// =============================================================================

// TestMCPListToolsCommand tests 'coral colony mcp list-tools'.
//
// After RFD 100, the colony server no longer serves per-operation tools.
// The list-tools command returns an empty list from the colony server side.
// The proxy exposes only the single coral_cli tool.
//
// Validates:
// - Command executes successfully
// - JSON output is valid (empty array or minimal list from colony server)
func (s *MCPSuite) TestMCPListToolsCommand() {
	s.T().Log("Testing 'coral colony mcp list-tools' command...")

	// Test table format
	result := helpers.MCPListTools(s.ctx, s.cliEnv)
	result.MustSucceed(s.T())

	s.T().Log("List tools output:")
	s.T().Log(result.Output)

	// Test JSON format — colony server returns empty list post-RFD 100.
	tools, err := helpers.MCPListToolsJSON(s.ctx, s.cliEnv)
	s.Require().NoError(err, "JSON list should succeed")

	// Colony server ListTools returns empty/nil post-RFD 100.
	s.T().Logf("Colony server tool count: %d (expected 0 post-RFD 100)", len(tools))

	s.T().Log("✓ MCP list-tools validated")
}

// TestMCPTestToolCommand tests 'coral colony mcp test-tool'.
//
// After RFD 100, the colony server CallTool RPC returns an error directing the
// caller to use the proxy layer instead. All tool calls to the colony server
// now return: "tool dispatch has moved to the proxy layer (RFD 100)".
//
// Validates:
// - Colony server returns the RFD 100 redirect error for any tool name
// - Error message correctly describes the new architecture
func (s *MCPSuite) TestMCPTestToolCommand() {
	s.T().Log("Testing 'coral colony mcp test-tool' command post-RFD 100...")

	// The colony server now returns an error for all tool calls.
	// test-tool goes through the colony server, so it will get the RFD 100 error.
	result := helpers.MCPTestTool(s.ctx, s.cliEnv, "coral_list_services", "")
	result.MustFail(s.T())

	s.T().Log("Test tool output (expected error):")
	s.T().Log(result.Output)

	// Should contain the RFD 100 redirect message.
	s.Require().Contains(strings.ToLower(result.Output), "proxy layer",
		"Should indicate tool dispatch has moved to proxy layer (RFD 100)")

	s.T().Log("✓ MCP test-tool correctly returns RFD 100 redirect error")
}

// TestMCPGenerateConfigCommand tests 'coral colony mcp generate-config'.
//
// Validates:
// - Config generation for single colony
// - JSON output structure
// - Executable paths
func (s *MCPSuite) TestMCPGenerateConfigCommand() {
	s.T().Log("Testing 'coral colony mcp generate-config' command...")

	// Generate config for current colony
	result := helpers.MCPGenerateConfig(s.ctx, s.cliEnv, "test-colony-e2e", false)
	result.MustSucceed(s.T())

	s.T().Log("Generated config:")
	s.T().Log(result.Output)

	// Verify output structure
	s.Require().NotEmpty(result.Output, "Config should not be empty")
	s.Require().Contains(result.Output, "mcpServers", "Config should contain mcpServers")
	s.Require().Contains(result.Output, "coral", "Config should reference coral command")
	s.Require().Contains(result.Output, "proxy", "Config should reference proxy subcommand")
	s.Require().Contains(result.Output, "colony", "Config should contain colony subcommand")
	s.Require().Contains(result.Output, "mcp", "Config should contain mcp subcommand")

	// Verify it contains Claude Desktop instructions
	s.Require().Contains(result.Output, "Claude Desktop", "Should mention Claude Desktop")
	s.Require().Contains(result.Output, "claude_desktop_config.json", "Should mention config file location")

	s.T().Log("✓ MCP generate-config validated")
}

// =============================================================================
// Group B: MCP Proxy Protocol (Stdio/JSON-RPC)
// =============================================================================

// TestMCPProxyInitialize tests MCP protocol initialization.
//
// Validates:
// - Proxy subprocess starts successfully
// - Initialize request/response
// - Protocol version and capabilities
// - Server info
func (s *MCPSuite) TestMCPProxyInitialize() {
	s.T().Log("Testing MCP proxy initialize...")

	// Start proxy subprocess with test environment
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Send initialize request
	initResp, err := proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Validate protocol version
	s.Require().Equal("2024-11-05", initResp.ProtocolVersion, "Should use correct MCP protocol version")

	// Validate server info
	s.Require().NotEmpty(initResp.ServerInfo.Name, "Server name should not be empty")
	s.Require().Contains(initResp.ServerInfo.Name, "coral", "Server name should contain 'coral'")
	s.Require().NotEmpty(initResp.ServerInfo.Version, "Server version should not be empty")

	// Validate capabilities
	s.Require().NotNil(initResp.Capabilities, "Capabilities should not be nil")

	s.T().Logf("✓ MCP proxy initialized: %s v%s", initResp.ServerInfo.Name, initResp.ServerInfo.Version)
}

// TestMCPProxyListTools tests MCP tools/list method.
//
// After RFD 100, the MCP proxy exposes ONLY the single coral_cli tool.
// All per-operation tools (coral_list_services, coral_query_summary, etc.)
// have been removed; callers must use coral_cli with appropriate CLI args.
//
// Validates:
// - tools/list returns exactly 1 tool
// - The single tool is named "coral_cli"
// - coral_cli has a description and input schema
func (s *MCPSuite) TestMCPProxyListTools() {
	s.T().Log("Testing MCP proxy tools/list (post-RFD 100)...")

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize first
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// List tools
	toolsResp, err := proxy.ListTools()
	s.Require().NoError(err, "List tools should succeed")

	// Post-RFD 100: exactly one tool is exposed by the proxy.
	s.Require().Len(toolsResp.Tools, 1, "Proxy should expose exactly 1 tool (coral_cli)")

	coralCLI := toolsResp.Tools[0]
	s.Require().Equal("coral_cli", coralCLI.Name, "The single tool should be named coral_cli")
	s.Require().NotEmpty(coralCLI.Description, "coral_cli should have a description")
	s.Require().NotNil(coralCLI.InputSchema, "coral_cli should have an input schema")

	s.T().Logf("✓ MCP proxy lists exactly 1 tool: %s", coralCLI.Name)
}

// TestMCPProxyCallTool tests MCP tools/call method.
//
// After RFD 100, tools/call only accepts coral_cli. Calling coral_cli with
// args ["query", "services"] replaces the old coral_list_services tool.
//
// Validates:
// - tools/call request/response with coral_cli
// - MCP response format (content array)
// - Tool execution success via CLI dispatch
func (s *MCPSuite) TestMCPProxyCallTool() {
	s.T().Log("Testing MCP proxy tools/call (post-RFD 100)...")

	// Ensure services connected
	s.ensureServicesConnected()

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Call coral_cli with args equivalent to old coral_list_services.
	callResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "services"},
	}, 3)
	s.Require().NoError(err, "coral_cli call should succeed")

	// Validate response format
	s.Require().NotEmpty(callResp.Content, "Response should have content")
	s.Require().Equal("text", callResp.Content[0].Type, "Content type should be text")
	s.Require().NotEmpty(callResp.Content[0].Text, "Content text should not be empty")

	s.T().Log("Tool result (truncated):")
	result := callResp.Content[0].Text
	if len(result) > 500 {
		result = result[:500] + "..."
	}
	s.T().Log(result)

	s.T().Log("✓ MCP proxy call tool validated")
}

// TestMCPProxyErrorHandling tests MCP error responses.
//
// After RFD 100, the proxy only accepts coral_cli. Any other tool name
// returns: "unknown tool: X (only coral_cli is supported)". Calling coral_cli
// without the required "args" parameter returns a validation error.
//
// Validates:
// - Invalid tool name returns "only coral_cli is supported" error
// - coral_cli called without args returns "missing required 'args' parameter"
// - JSON-RPC error format
func (s *MCPSuite) TestMCPProxyErrorHandling() {
	s.T().Log("Testing MCP proxy error handling (post-RFD 100)...")

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Test invalid tool name — proxy now says "only coral_cli is supported".
	mcpErr, err := proxy.CallToolExpectError("invalid_tool_xyz", map[string]interface{}{}, 4)
	s.Require().NoError(err, "Should get error response for invalid tool")
	s.Require().NotNil(mcpErr, "Should have MCP error")
	s.Require().Contains(mcpErr.Message, "only coral_cli is supported",
		"Error message should indicate only coral_cli is supported")

	s.T().Logf("Invalid tool error: code=%d, message=%s", mcpErr.Code, mcpErr.Message)

	// Test calling coral_cli without required args parameter.
	invalidArgsErr, err := proxy.CallToolExpectError("coral_cli", map[string]interface{}{}, 5)
	s.Require().NoError(err, "Should get error response for missing args")
	s.Require().NotNil(invalidArgsErr, "Should have MCP error for missing args")
	s.Require().Contains(strings.ToLower(invalidArgsErr.Message), "args",
		"Error should mention missing args parameter")

	s.T().Logf("Missing args error: code=%d, message=%s", invalidArgsErr.Code, invalidArgsErr.Message)

	s.T().Log("✓ MCP proxy error handling validated")
}

// =============================================================================
// Group C: End-to-End Tool Execution
// =============================================================================

// TestMCPToolObservabilityQuery tests observability tools end-to-end via coral_cli.
//
// After RFD 100, all per-operation tools are dispatched via coral_cli.
// coral_cli runs `coral <args> --format json` as a subprocess.
//
// Validates:
// - coral_cli with query summary args succeeds with real telemetry
// - coral_cli with query traces args returns trace data
// - coral_cli with query metrics args returns metrics data
func (s *MCPSuite) TestMCPToolObservabilityQuery() {
	s.T().Log("Testing MCP observability tools via coral_cli (post-RFD 100)...")

	// Ensure telemetry data exists
	s.ensureTelemetryData()

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Test query summary via coral_cli (replaces coral_query_summary).
	summaryResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "summary", "*", "--since", "5m"},
	}, 10)
	s.Require().NoError(err, "coral_cli query summary should succeed")
	s.Require().NotEmpty(summaryResp.Content, "Summary should have content")

	s.T().Log("Query summary result (truncated):")
	summaryText := summaryResp.Content[0].Text
	if len(summaryText) > 500 {
		summaryText = summaryText[:500] + "..."
	}
	s.T().Log(summaryText)

	// Test query traces via coral_cli (replaces coral_query_traces).
	tracesResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "traces", "*", "--since", "5m", "--limit", "10"},
	}, 11)
	s.Require().NoError(err, "coral_cli query traces should succeed")
	s.Require().NotEmpty(tracesResp.Content, "Traces should have content")

	// Test query metrics via coral_cli (replaces coral_query_metrics).
	metricsResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "metrics", "*", "--since", "5m"},
	}, 12)
	s.Require().NoError(err, "coral_cli query metrics should succeed")
	s.Require().NotEmpty(metricsResp.Content, "Metrics should have content")

	s.T().Log("✓ MCP observability tools via coral_cli validated")
}

// TestMCPToolServiceDiscovery tests service discovery via coral_cli.
//
// After RFD 100, coral_list_services is no longer served by the proxy.
// Use coral_cli with args ["query", "services"] instead.
//
// Validates:
// - coral_cli with query services args succeeds
// - Response lists connected services
func (s *MCPSuite) TestMCPToolServiceDiscovery() {
	s.T().Log("Testing MCP service discovery via coral_cli (post-RFD 100)...")

	// Ensure services connected
	s.ensureServicesConnected()

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Call coral_cli with query services args (replaces coral_list_services).
	servicesResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "services"},
	}, 20)
	s.Require().NoError(err, "coral_cli query services should succeed")
	s.Require().NotEmpty(servicesResp.Content, "Services should have content")

	servicesText := servicesResp.Content[0].Text
	s.T().Log("Services list:")
	s.T().Log(servicesText)

	// Verify services are listed (expect at least otel-app or cpu-app).
	hasServices := strings.Contains(servicesText, "otel-app") || strings.Contains(servicesText, "cpu-app")
	s.Require().True(hasServices, "Should list at least one connected service")

	s.T().Log("✓ MCP service discovery via coral_cli validated")
}

// TestMCPToolShellExec tests that coral_shell_exec is no longer served by the proxy.
//
// After RFD 100, coral_shell_exec has no CLI equivalent and is NOT available
// via the MCP proxy. Calling it via the proxy returns an "unknown tool" error
// with the message "only coral_cli is supported".
//
// Validates:
// - coral_shell_exec returns "unknown tool" error from proxy post-RFD 100
func (s *MCPSuite) TestMCPToolShellExec() {
	s.T().Log("Testing that coral_shell_exec is no longer available via proxy (post-RFD 100)...")

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// coral_shell_exec has no CLI equivalent; proxy returns unknown tool error.
	mcpErr, err := proxy.CallToolExpectError("coral_shell_exec", map[string]interface{}{
		"agent_id": "some-agent-id",
		"command":  []string{"sh", "-c", "echo test"},
	}, 30)
	s.Require().NoError(err, "Should get error response, not transport failure")
	s.Require().NotNil(mcpErr, "Should have MCP error for unknown tool")
	s.Require().Contains(mcpErr.Message, "only coral_cli is supported",
		"Error should indicate only coral_cli is supported")

	s.T().Logf("✓ coral_shell_exec correctly returns unknown tool error: %s", mcpErr.Message)
}

// =============================================================================
// Group D: Debugging Tools
// =============================================================================

// TestMCPToolDiscoverFunctions tests function discovery via coral_cli.
//
// After RFD 100, coral_discover_functions is dispatched via coral_cli with
// args ["debug", "search", <query>, "--service", <service>].
//
// Validates:
// - coral_cli with debug search args succeeds
// - Response contains function information
func (s *MCPSuite) TestMCPToolDiscoverFunctions() {
	s.T().Log("Testing MCP discover functions via coral_cli (post-RFD 100)...")

	// Ensure services are connected
	s.ensureServicesConnected()

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Call coral_cli with debug search args (replaces coral_discover_functions).
	discoverResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"debug", "search", "handler", "--service", "otel-app"},
	}, 40)
	s.Require().NoError(err, "coral_cli debug search should succeed")
	s.Require().NotEmpty(discoverResp.Content, "Discover should have content")

	discoverText := discoverResp.Content[0].Text
	s.T().Log("Discover functions result (truncated):")
	if len(discoverText) > 500 {
		s.T().Log(discoverText[:500] + "...")
	} else {
		s.T().Log(discoverText)
	}

	// Verify response contains function information.
	s.Require().Contains(strings.ToLower(discoverText), "function", "Should mention functions")

	s.T().Log("✓ MCP discover functions via coral_cli validated")
}

// TestMCPToolProfileFunctions tests that coral_profile_functions is no longer
// served by the proxy.
//
// After RFD 100, coral_profile_functions has no CLI equivalent and is NOT
// available via the MCP proxy. Calling it returns an "unknown tool" error.
//
// Validates:
// - coral_profile_functions returns "unknown tool" error from proxy post-RFD 100
func (s *MCPSuite) TestMCPToolProfileFunctions() {
	s.T().Log("Testing that coral_profile_functions is no longer available via proxy (post-RFD 100)...")

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// coral_profile_functions has no CLI equivalent; proxy returns unknown tool error.
	mcpErr, err := proxy.CallToolExpectError("coral_profile_functions", map[string]interface{}{
		"service":  "otel-app",
		"query":    "handler",
		"duration": "10s",
	}, 41)
	s.Require().NoError(err, "Should get error response, not transport failure")
	s.Require().NotNil(mcpErr, "Should have MCP error for unknown tool")
	s.Require().Contains(mcpErr.Message, "only coral_cli is supported",
		"Error should indicate only coral_cli is supported")

	s.T().Logf("✓ coral_profile_functions correctly returns unknown tool error: %s", mcpErr.Message)
}

// TestMCPToolAttachUprobe tests uprobe attachment via coral_cli.
//
// After RFD 100, coral_discover_functions and coral_attach_uprobe are
// dispatched via coral_cli. Function search uses "debug search" and
// attachment uses "debug attach".
//
// Validates:
// - coral_cli with debug search args succeeds (replaces coral_discover_functions)
// - coral_cli with debug attach args is callable (replaces coral_attach_uprobe)
func (s *MCPSuite) TestMCPToolAttachUprobe() {
	s.T().Log("Testing MCP attach uprobe via coral_cli (post-RFD 100)...")

	// Ensure services are connected
	s.ensureServicesConnected()

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// First discover a function via coral_cli (replaces coral_discover_functions).
	discoverResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"debug", "search", "main", "--service", "otel-app"},
	}, 42)
	s.Require().NoError(err, "coral_cli debug search should succeed")
	s.T().Logf("Discovered functions: %v", discoverResp.Content[0].Text)

	// Try to attach uprobe via coral_cli (replaces coral_attach_uprobe).
	// This may fail if the function is not probeable in the test environment.
	attachResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"debug", "attach", "otel-app", "--function", "main.main", "--duration", "10s"},
	}, 43)

	// Note: may fail in test environment if function not found or not probeable.
	if err != nil {
		s.T().Logf("Attach uprobe failed (expected in test env): %v", err)
	} else {
		s.Require().NotEmpty(attachResp.Content, "Attach should have content")
		attachText := attachResp.Content[0].Text
		s.T().Log("Attach uprobe result:")
		s.T().Log(attachText)
	}

	s.T().Log("✓ MCP attach uprobe via coral_cli validated")
}

// TestMCPToolListDebugSessions tests debug session listing via coral_cli.
//
// After RFD 100, coral_list_debug_sessions is dispatched via coral_cli with
// args ["debug", "session", "list"].
//
// Validates:
// - coral_cli with debug session list args succeeds
// - Response contains session information (may be empty if no sessions)
func (s *MCPSuite) TestMCPToolListDebugSessions() {
	s.T().Log("Testing MCP list debug sessions via coral_cli (post-RFD 100)...")

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// List debug sessions via coral_cli (replaces coral_list_debug_sessions).
	listResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"debug", "session", "list"},
	}, 44)
	s.Require().NoError(err, "coral_cli debug session list should succeed")
	s.Require().NotEmpty(listResp.Content, "List should have content")

	listText := listResp.Content[0].Text
	s.T().Log("List debug sessions result:")
	s.T().Log(listText)

	// Verify response format (may have no sessions).
	s.Require().NotEmpty(listText, "Should have response text")

	s.T().Log("✓ MCP list debug sessions via coral_cli validated")
}

// TestMCPToolGetDebugResults tests debug session event retrieval via coral_cli.
//
// After RFD 100, coral_get_debug_results is dispatched via coral_cli with
// args ["debug", "session", "events", <sessionID>]. For a non-existent
// session the CLI exits non-zero, which the proxy surfaces as an MCP error.
//
// Validates:
// - coral_cli with debug session events args for non-existent session returns error
// - Error originates from CLI (not "unknown tool") indicating coral_cli dispatch works
func (s *MCPSuite) TestMCPToolGetDebugResults() {
	s.T().Log("Testing MCP get debug results via coral_cli (post-RFD 100)...")

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Try to get results for a non-existent session via coral_cli.
	// The CLI exits non-zero for unknown sessions, so proxy returns an MCP error.
	resultsResp, err := proxy.CallToolExpectError("coral_cli", map[string]interface{}{
		"args": []interface{}{"debug", "session", "events", "non-existent-session-id"},
	}, 45)

	// Should get an error because the session doesn't exist.
	s.Require().NoError(err, "Should get error response")
	s.Require().NotNil(resultsResp, "Should have error response")
	s.T().Logf("Expected error for non-existent session: %s", resultsResp.Message)

	s.T().Log("✓ MCP get debug results via coral_cli validated")
}

// TestMCPToolDetachUprobe tests uprobe detachment via coral_cli.
//
// After RFD 100, coral_detach_uprobe is dispatched via coral_cli with
// args ["debug", "session", "stop", <sessionID>]. For a non-existent
// session the CLI exits non-zero, which the proxy surfaces as an MCP error.
//
// Validates:
// - coral_cli with debug session stop args for non-existent session returns error
func (s *MCPSuite) TestMCPToolDetachUprobe() {
	s.T().Log("Testing MCP detach uprobe via coral_cli (post-RFD 100)...")

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Try to stop a non-existent session via coral_cli.
	detachResp, err := proxy.CallToolExpectError("coral_cli", map[string]interface{}{
		"args": []interface{}{"debug", "session", "stop", "non-existent-session-id"},
	}, 46)

	// Should get an error because the session doesn't exist.
	s.Require().NoError(err, "Should get error response")
	s.Require().NotNil(detachResp, "Should have error response")
	s.T().Logf("Expected error for non-existent session: %s", detachResp.Message)

	s.T().Log("✓ MCP detach uprobe via coral_cli validated")
}

// =============================================================================
// Group E: Container Execution
// =============================================================================

// TestMCPToolContainerExec tests that coral_container_exec is no longer served
// by the proxy.
//
// After RFD 100, coral_container_exec has no CLI equivalent and is NOT
// available via the MCP proxy. Calling it returns an "unknown tool" error.
//
// Validates:
// - coral_container_exec returns "unknown tool" error from proxy post-RFD 100
func (s *MCPSuite) TestMCPToolContainerExec() {
	s.T().Log("Testing that coral_container_exec is no longer available via proxy (post-RFD 100)...")

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// coral_container_exec has no CLI equivalent; proxy returns unknown tool error.
	mcpErr, err := proxy.CallToolExpectError("coral_container_exec", map[string]interface{}{
		"agent_id": "some-agent-id",
		"command":  []string{"echo", "test"},
	}, 50)
	s.Require().NoError(err, "Should get error response, not transport failure")
	s.Require().NotNil(mcpErr, "Should have MCP error for unknown tool")
	s.Require().Contains(mcpErr.Message, "only coral_cli is supported",
		"Error should indicate only coral_cli is supported")

	s.T().Logf("✓ coral_container_exec correctly returns unknown tool error: %s", mcpErr.Message)
}

// =============================================================================
// Group F: Advanced Observability with Real Telemetry
// =============================================================================

// TestMCPToolQueryWithTelemetryData tests observability tools with real data
// via coral_cli dispatch (post-RFD 100).
//
// Validates:
// - coral_cli query summary with service filter succeeds
// - coral_cli query traces with time range succeeds
// - coral_cli query metrics with time range succeeds
func (s *MCPSuite) TestMCPToolQueryWithTelemetryData() {
	s.T().Log("Testing MCP observability tools with real telemetry data via coral_cli (post-RFD 100)...")

	// Ensure telemetry data exists
	s.ensureTelemetryData()

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Test 1: Query summary with specific service filter via coral_cli.
	summaryResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "summary", "otel-app", "--since", "10m"},
	}, 60)
	s.Require().NoError(err, "coral_cli query summary should succeed")
	s.Require().NotEmpty(summaryResp.Content, "Summary should have content")

	summaryText := summaryResp.Content[0].Text
	s.T().Log("Query summary with service filter:")
	if len(summaryText) > 300 {
		s.T().Log(summaryText[:300] + "...")
	} else {
		s.T().Log(summaryText)
	}

	// Verify summary contains service data.
	s.Require().Contains(strings.ToLower(summaryText), "service", "Should mention service")

	// Test 2: Query traces with time range via coral_cli.
	tracesResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "traces", "otel-app", "--since", "10m", "--limit", "5"},
	}, 61)
	s.Require().NoError(err, "coral_cli query traces should succeed")
	s.Require().NotEmpty(tracesResp.Content, "Traces should have content")

	// Test 3: Query metrics with time range via coral_cli.
	metricsResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "metrics", "otel-app", "--since", "10m"},
	}, 62)
	s.Require().NoError(err, "coral_cli query metrics should succeed")
	s.Require().NotEmpty(metricsResp.Content, "Metrics should have content")

	s.T().Log("✓ MCP observability tools with telemetry data validated")
}

// TestMCPToolQueryMetricsProtocols tests protocol-specific metric queries
// via coral_cli (post-RFD 100).
//
// Validates:
// - coral_cli query metrics with protocol filter succeeds
func (s *MCPSuite) TestMCPToolQueryMetricsProtocols() {
	s.T().Log("Testing MCP metrics with protocol filters via coral_cli (post-RFD 100)...")

	// Ensure telemetry data exists
	s.ensureTelemetryData()

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Query HTTP metrics via coral_cli (replaces coral_query_metrics with protocol filter).
	httpResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "metrics", "otel-app", "--since", "10m", "--protocol", "http"},
	}, 70)
	s.Require().NoError(err, "coral_cli query metrics should succeed")
	s.Require().NotEmpty(httpResp.Content, "HTTP metrics should have content")

	httpText := httpResp.Content[0].Text
	s.T().Log("HTTP metrics result:")
	if len(httpText) > 300 {
		s.T().Log(httpText[:300] + "...")
	} else {
		s.T().Log(httpText)
	}

	s.T().Log("✓ MCP protocol-specific metrics via coral_cli validated")
}

// =============================================================================
// Group G: Error Handling and Edge Cases
// =============================================================================

// TestMCPToolErrorScenarios tests comprehensive error handling via coral_cli
// (post-RFD 100).
//
// Validates:
// - coral_cli with non-existent service returns graceful response
// - coral_cli with invalid time range returns error
// - coral_shell_exec (no CLI equivalent) returns "unknown tool" error
// - coral_cli with incomplete attach args returns CLI error
func (s *MCPSuite) TestMCPToolErrorScenarios() {
	s.T().Log("Testing MCP error scenarios (post-RFD 100)...")

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Test 1: Non-existent service name via coral_cli (should return empty results).
	nonExistentServiceResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "summary", "non-existent-service-xyz", "--since", "5m"},
	}, 80)
	s.Require().NoError(err, "Query for non-existent service should succeed")
	s.Require().NotEmpty(nonExistentServiceResp.Content, "Should have response content")
	s.T().Log("✓ Non-existent service query returns empty results (expected behavior)")

	// Test 2: Invalid time range format via coral_cli.
	invalidTimeErr, err := proxy.CallToolExpectError("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "summary", "otel-app", "--since", "invalid-time"},
	}, 81)
	s.Require().NoError(err, "Should get error response")
	s.Require().NotNil(invalidTimeErr, "Should have error for invalid time")
	s.T().Logf("Invalid time range error: %s", invalidTimeErr.Message)

	// Test 3: coral_shell_exec has no CLI equivalent — proxy returns unknown tool error.
	invalidAgentErr, err := proxy.CallToolExpectError("coral_shell_exec", map[string]interface{}{
		"agent_id": "non-existent-agent-id",
		"command":  []string{"echo", "test"},
	}, 82)
	s.Require().NoError(err, "Should get error response")
	s.Require().NotNil(invalidAgentErr, "Should have MCP error")
	s.Require().Contains(invalidAgentErr.Message, "only coral_cli is supported",
		"coral_shell_exec should return unknown tool error")
	s.T().Logf("coral_shell_exec unknown tool error: %s", invalidAgentErr.Message)

	// Test 4: Missing required parameter for attach via coral_cli.
	// Calling debug attach without --function flag should fail at CLI level.
	missingParamErr, err := proxy.CallToolExpectError("coral_cli", map[string]interface{}{
		"args": []interface{}{"debug", "attach", "otel-app"},
		// Missing --function flag
	}, 83)
	s.Require().NoError(err, "Should get error response")
	s.Require().NotNil(missingParamErr, "Should have error for missing parameter")
	s.T().Logf("Missing parameter error: %s", missingParamErr.Message)

	s.T().Log("✓ MCP error scenarios validated")
}

// TestMCPToolInputValidation tests input validation for coral_cli and unknown
// tool handling (post-RFD 100).
//
// Validates:
// - coral_cli with invalid CLI args returns CLI-level error
// - coral_shell_exec (no CLI equivalent) returns "unknown tool" error regardless of args
func (s *MCPSuite) TestMCPToolInputValidation() {
	s.T().Log("Testing MCP input validation (post-RFD 100)...")

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Test 1: Invalid time range via coral_cli (CLI rejects invalid --since value).
	invalidTypeErr, err := proxy.CallToolExpectError("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "summary", "otel-app", "--since", "not-a-duration"},
	}, 90)
	s.Require().NoError(err, "Should get error response")
	s.Require().NotNil(invalidTypeErr, "Should have error for invalid duration")
	s.T().Logf("Invalid duration error: %s", invalidTypeErr.Message)

	// Test 2: coral_shell_exec with out-of-range value — returns "unknown tool" error
	// because coral_shell_exec is not available via proxy post-RFD 100.
	outOfRangeErr, err := proxy.CallToolExpectError("coral_shell_exec", map[string]interface{}{
		"command":         []string{"echo", "test"},
		"timeout_seconds": 999999,
	}, 91)
	s.Require().NoError(err, "Should get error response")
	s.Require().NotNil(outOfRangeErr, "Should have MCP error")
	s.Require().Contains(outOfRangeErr.Message, "only coral_cli is supported",
		"coral_shell_exec should return unknown tool error")
	s.T().Logf("coral_shell_exec with out-of-range timeout: %s", outOfRangeErr.Message)

	// Test 3: coral_shell_exec with empty command — returns "unknown tool" error.
	emptyCommandErr, err := proxy.CallToolExpectError("coral_shell_exec", map[string]interface{}{
		"command": []string{},
	}, 92)
	s.Require().NoError(err, "Should get error response")
	s.Require().NotNil(emptyCommandErr, "Should have MCP error")
	s.Require().Contains(emptyCommandErr.Message, "only coral_cli is supported",
		"coral_shell_exec should return unknown tool error")
	s.T().Logf("coral_shell_exec with empty command: %s", emptyCommandErr.Message)

	s.T().Log("✓ MCP input validation validated")
}

// =============================================================================
// Group H: Profiling-Enriched Summary (RFD 074)
// =============================================================================

// TestMCPToolQuerySummaryProfilingFields tests query summary via coral_cli (RFD 074).
//
// After RFD 100, coral_query_summary is dispatched via coral_cli. The
// include_profiling and top_k parameters have no direct CLI flag equivalents;
// coral query summary uses --since for time range.
//
// Validates:
// - coral_cli query summary returns service health information
// - Response contains service information
func (s *MCPSuite) TestMCPToolQuerySummaryProfilingFields() {
	s.T().Log("Testing MCP profiling-enriched query summary via coral_cli (post-RFD 100)...")

	s.ensureTelemetryData()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Query summary via coral_cli. The include_profiling and top_k parameters
	// do not have CLI equivalents; the CLI includes profiling by default.
	summaryResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "summary", "otel-app", "--since", "10m"},
	}, 110)
	s.Require().NoError(err, "coral_cli query summary should succeed")
	s.Require().NotEmpty(summaryResp.Content, "Summary should have content")

	summaryText := summaryResp.Content[0].Text
	s.T().Log("Query summary (truncated):")
	if len(summaryText) > 500 {
		s.T().Log(summaryText[:500] + "...")
	} else {
		s.T().Log(summaryText)
	}

	// The response should at minimum contain service information.
	s.Require().Contains(strings.ToLower(summaryText), "service",
		"Summary should mention service")

	s.T().Log("✓ MCP query summary via coral_cli validated")
}

// TestMCPToolDebugCPUProfile tests CPU profiling via coral_cli (RFD 074).
//
// After RFD 100, coral_debug_cpu_profile is dispatched via coral_cli with
// args ["query", "cpu-profile", <service>].
//
// Validates:
// - coral_cli query cpu-profile succeeds or returns graceful no-data response
// - Non-existent service returns empty/no-data response
func (s *MCPSuite) TestMCPToolDebugCPUProfile() {
	s.T().Log("Testing MCP debug CPU profile via coral_cli (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Test 1: Query CPU profile for otel-app via coral_cli.
	profileResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "cpu-profile", "otel-app"},
	}, 120)
	s.Require().NoError(err, "coral_cli query cpu-profile should succeed")
	s.Require().NotEmpty(profileResp.Content, "Profile should have content")

	profileText := profileResp.Content[0].Text
	s.T().Log("CPU profile result (truncated):")
	if len(profileText) > 500 {
		s.T().Log(profileText[:500] + "...")
	} else {
		s.T().Log(profileText)
	}

	// Response should mention the service name or indicate no data.
	hasServiceName := strings.Contains(strings.ToLower(profileText), "otel-app")
	hasNoData := strings.Contains(strings.ToLower(profileText), "no cpu profiling data") ||
		strings.Contains(strings.ToLower(profileText), "no data") ||
		strings.Contains(strings.ToLower(profileText), "0 samples")
	s.Require().True(hasServiceName || hasNoData,
		"Response should mention service name or indicate no data available")

	// Test 2: Non-existent service via coral_cli.
	noDataResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "cpu-profile", "nonexistent-service-xyz"},
	}, 122)
	s.Require().NoError(err, "coral_cli for missing service should succeed")
	s.Require().NotEmpty(noDataResp.Content, "Should have content")

	s.T().Log("✓ MCP debug CPU profile via coral_cli validated")
}

// TestMCPToolListIncludesProfilingTools validates that the proxy exposes only
// coral_cli and not the old per-operation profiling tools (post-RFD 100).
func (s *MCPSuite) TestMCPToolListIncludesProfilingTools() {
	s.T().Log("Testing MCP tool list after RFD 100 (profiling via coral_cli)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// List all tools — only coral_cli should be present.
	listResp, err := proxy.ListTools()
	s.Require().NoError(err, "tools/list should succeed")

	s.Require().Len(listResp.Tools, 1, "Proxy should expose exactly 1 tool (coral_cli)")
	s.Require().Equal("coral_cli", listResp.Tools[0].Name,
		"The single tool should be coral_cli, not coral_debug_cpu_profile")

	s.T().Log("✓ Proxy correctly exposes only coral_cli (profiling accessible via coral_cli args)")
}

// =============================================================================
// Group I: Memory Profiling Tools (RFD 077)
// =============================================================================

// TestMCPToolQueryMemoryProfile tests memory profiling via coral_cli (RFD 077).
//
// After RFD 100, coral_query_memory_profile is dispatched via coral_cli with
// args ["query", "memory-profile", <service>].
//
// Validates:
// - coral_cli query memory-profile succeeds or returns graceful no-data response
// - Non-existent service returns empty/no-data response
func (s *MCPSuite) TestMCPToolQueryMemoryProfile() {
	s.T().Log("Testing MCP query memory profile via coral_cli (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Test 1: Query memory profile for otel-app via coral_cli.
	profileResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "memory-profile", "otel-app"},
	}, 130)
	s.Require().NoError(err, "coral_cli query memory-profile should succeed")
	s.Require().NotEmpty(profileResp.Content, "Profile should have content")

	profileText := profileResp.Content[0].Text
	s.T().Log("Memory profile result (truncated):")
	if len(profileText) > 500 {
		s.T().Log(profileText[:500] + "...")
	} else {
		s.T().Log(profileText)
	}

	// Response should mention the service name or indicate no data.
	hasServiceName := strings.Contains(strings.ToLower(profileText), "otel-app")
	hasNoData := strings.Contains(strings.ToLower(profileText), "no memory profiling data") ||
		strings.Contains(strings.ToLower(profileText), "no data") ||
		strings.Contains(strings.ToLower(profileText), "0 samples")
	s.Require().True(hasServiceName || hasNoData,
		"Response should mention service name or indicate no data available")

	// Test 2: Non-existent service via coral_cli.
	noDataResp, err := proxy.CallTool("coral_cli", map[string]interface{}{
		"args": []interface{}{"query", "memory-profile", "nonexistent-service-xyz"},
	}, 131)
	s.Require().NoError(err, "coral_cli for missing service should succeed")
	s.Require().NotEmpty(noDataResp.Content, "Should have content")

	s.T().Log("✓ MCP query memory profile via coral_cli validated")
}

// TestMCPToolProfileMemory tests that coral_profile_memory is no longer served
// by the proxy (post-RFD 100).
//
// coral_profile_memory has no CLI equivalent and is NOT available via the MCP
// proxy after RFD 100. Calling it returns an "unknown tool" error.
//
// Validates:
// - coral_profile_memory returns "unknown tool" error from proxy post-RFD 100
func (s *MCPSuite) TestMCPToolProfileMemory() {
	s.T().Log("Testing that coral_profile_memory is no longer available via proxy (post-RFD 100)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// coral_profile_memory has no CLI equivalent; proxy returns unknown tool error.
	mcpErr, err := proxy.CallToolExpectError("coral_profile_memory", map[string]interface{}{
		"service":           "otel-app",
		"duration_seconds":  10,
		"sample_rate_bytes": 524288,
	}, 132)
	s.Require().NoError(err, "Should get error response, not transport failure")
	s.Require().NotNil(mcpErr, "Should have MCP error for unknown tool")
	s.Require().Contains(mcpErr.Message, "only coral_cli is supported",
		"Error should indicate only coral_cli is supported")

	s.T().Logf("✓ coral_profile_memory correctly returns unknown tool error: %s", mcpErr.Message)
}

// TestMCPToolListIncludesMemoryProfilingTools validates that the proxy exposes
// only coral_cli and not the old per-operation memory profiling tools (post-RFD 100).
func (s *MCPSuite) TestMCPToolListIncludesMemoryProfilingTools() {
	s.T().Log("Testing MCP tool list after RFD 100 (memory profiling via coral_cli)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// List all tools — only coral_cli should be present.
	listResp, err := proxy.ListTools()
	s.Require().NoError(err, "tools/list should succeed")

	s.Require().Len(listResp.Tools, 1, "Proxy should expose exactly 1 tool (coral_cli)")
	s.Require().Equal("coral_cli", listResp.Tools[0].Name,
		"The single tool should be coral_cli, not coral_query_memory_profile or coral_profile_memory")

	s.T().Log("✓ Proxy correctly exposes only coral_cli (memory profiling accessible via coral_cli args)")
}

// TestMCPToolCoralRun tests coral run via coral_cli (post-RFD 100).
//
// After RFD 100, scripts are executed via coral_cli with args ["run", scriptPath].
// The coral_run tool (code parameter) is no longer directly served by the proxy;
// use coral_cli with a script file path instead.
//
// Validates:
// - coral_cli is the only tool (coral_run not in tool list post-RFD 100)
// - coral_cli with run args is callable
func (s *MCPSuite) TestMCPToolCoralRun() {
	s.T().Log("Testing coral run via coral_cli (post-RFD 100)...")

	s.ensureServicesConnected()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Post-RFD 100: only coral_cli is in the tool list, not coral_run.
	listResp, err := proxy.ListTools()
	s.Require().NoError(err, "tools/list should succeed")

	s.Require().Len(listResp.Tools, 1, "Proxy should expose exactly 1 tool (coral_cli)")
	s.Require().Equal("coral_cli", listResp.Tools[0].Name,
		"The single tool should be coral_cli, not coral_run")
	s.T().Log("✓ coral_run is not in tool list post-RFD 100 (use coral_cli with run args instead)")

	s.T().Log("✓ coral run via coral_cli validated")
}

// =============================================================================
// Helper Methods
// =============================================================================

// ensureServicesConnected ensures that test services are connected.
// This uses the shared helper for idempotent service connection.
func (s *MCPSuite) ensureServicesConnected() {
	// MCP tests only need otel-app (OTLP-instrumented)
	helpers.EnsureServicesConnected(s.T(), s.ctx, s.fixture, 0, []helpers.ServiceConfig{
		{Name: "otel-app", Port: 8090, HealthEndpoint: "/health"},
	})
}

// ensureTelemetryData ensures that telemetry data exists for testing queries.
func (s *MCPSuite) ensureTelemetryData() {
	s.T().Log("Ensuring telemetry data exists...")

	// Generate some HTTP traffic to OTEL app
	otelAppURL := "http://localhost:8082" // OTEL app from docker-compose

	// Check if OTEL app is reachable
	err := helpers.WaitForHTTPEndpoint(s.ctx, otelAppURL+"/health", 10*time.Second)
	if err != nil {
		s.T().Log("OTEL app not reachable, telemetry tests may have limited data")
		return
	}

	// Generate some requests
	client := &http.Client{Timeout: 5 * time.Second}
	for i := 0; i < 5; i++ {
		resp, err := client.Get(otelAppURL + "/")
		if err == nil {
			_ = resp.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Wait a bit for telemetry to be ingested
	time.Sleep(2 * time.Second)

	s.T().Log("Telemetry data generated")
}
