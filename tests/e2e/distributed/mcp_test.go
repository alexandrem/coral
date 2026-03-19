package distributed

import (
	"strings"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// MCPSuite tests MCP (Model Context Protocol) server implementation.
//
// This suite validates:
// 1. MCP CLI commands (list-tools, test-tool, generate-config)
// 2. MCP proxy protocol (JSON-RPC 2.0 over stdio)
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

// ensureServicesConnected ensures that test services are connected.
func (s *MCPSuite) ensureServicesConnected() {
	helpers.EnsureServicesConnected(s.T(), s.ctx, s.fixture, 0, []helpers.ServiceConfig{
		{Name: "otel-app", Port: 8090, HealthEndpoint: "/health"},
	})
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
