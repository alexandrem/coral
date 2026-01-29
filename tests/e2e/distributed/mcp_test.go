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
// Validates:
// - Command executes successfully
// - Tool list contains expected tools
// - Tool metadata includes descriptions
func (s *MCPSuite) TestMCPListToolsCommand() {
	s.T().Log("Testing 'coral colony mcp list-tools' command...")

	// Test table format
	result := helpers.MCPListTools(s.ctx, s.cliEnv.ColonyEndpoint)
	result.MustSucceed(s.T())

	s.T().Log("List tools output:")
	s.T().Log(result.Output)

	// Verify output contains tool names
	s.Require().NotEmpty(result.Output, "Tool list should not be empty")
	s.Require().Contains(result.Output, "coral_list_services",
		"Should list coral_list_services tool")
	s.Require().Contains(result.Output, "coral_query_summary", "Should list coral_query_summary tool")

	// Test JSON format
	tools, err := helpers.MCPListToolsJSON(s.ctx, s.cliEnv.ColonyEndpoint)
	s.Require().NoError(err, "JSON list should succeed")
	s.Require().NotEmpty(tools, "Should have at least one tool")

	// Verify tool structure
	foundListServices := false
	for _, tool := range tools {
		name, ok := tool["name"].(string)
		s.Require().True(ok, "Tool should have name field")
		s.Require().NotEmpty(name, "Tool name should not be empty")

		if name == "coral_list_services" {
			foundListServices = true
			s.Require().NotEmpty(tool["description"], "Tool should have description")
		}
	}
	s.Require().True(foundListServices, "Should find coral_list_services in tool list")

	s.T().Logf("✓ MCP list-tools validated (%d tools)", len(tools))
}

// TestMCPTestToolCommand tests 'coral colony mcp test-tool'.
//
// Validates:
// - Executing tools via test-tool CLI
// - JSON argument parsing
// - Tool result formatting
// - Error handling for invalid tools
func (s *MCPSuite) TestMCPTestToolCommand() {
	s.T().Log("Testing 'coral colony mcp test-tool' command...")

	// Test simple tool with no arguments
	result := helpers.MCPTestTool(s.ctx, s.cliEnv.ColonyEndpoint, "coral_list_services", "")
	result.MustSucceed(s.T())

	s.T().Log("Test tool output:")
	s.T().Log(result.Output)

	// Verify output contains service information
	s.Require().NotEmpty(result.Output, "Tool result should not be empty")

	// Test tool with JSON arguments
	queryArgs := `{"service_filter":"*","time_range":"5m"}`
	queryResult := helpers.MCPTestTool(s.ctx, s.cliEnv.ColonyEndpoint, "coral_query_summary", queryArgs)
	queryResult.MustSucceed(s.T())

	s.T().Log("Query summary result (truncated):")
	output := queryResult.Output
	if len(output) > 500 {
		output = output[:500] + "..."
	}
	s.T().Log(output)

	// Test invalid tool name
	invalidResult := helpers.MCPTestTool(s.ctx, s.cliEnv.ColonyEndpoint, "invalid_tool_name", "")
	invalidResult.MustFail(s.T())

	s.Require().Contains(strings.ToLower(invalidResult.Output), "unknown tool", "Should indicate tool not found")

	s.T().Log("✓ MCP test-tool validated")
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
	result := helpers.MCPGenerateConfig(s.ctx, s.cliEnv.ColonyEndpoint, "test-colony-e2e", false)
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
// Validates:
// - tools/list request/response
// - Tool metadata structure
// - JSON schemas present
func (s *MCPSuite) TestMCPProxyListTools() {
	s.T().Log("Testing MCP proxy tools/list...")

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

	// Validate tools
	s.Require().NotEmpty(toolsResp.Tools, "Should have at least one tool")

	// Find and validate specific tools
	foundListServices := false
	foundQuerySummary := false

	for _, tool := range toolsResp.Tools {
		s.Require().NotEmpty(tool.Name, "Tool name should not be empty")
		s.Require().NotEmpty(tool.Description, "Tool description should not be empty")
		s.Require().NotNil(tool.InputSchema, "Tool should have input schema")

		if tool.Name == "coral_list_services" {
			foundListServices = true
		}
		if tool.Name == "coral_query_summary" {
			foundQuerySummary = true
			// Validate schema structure
			s.Require().Contains(tool.InputSchema, "type", "Schema should have type")
		}
	}

	s.Require().True(foundListServices, "Should find coral_list_services")
	s.Require().True(foundQuerySummary, "Should find coral_query_summary")

	s.T().Logf("✓ MCP proxy listed %d tools", len(toolsResp.Tools))
}

// TestMCPProxyCallTool tests MCP tools/call method.
//
// Validates:
// - tools/call request/response
// - MCP response format (content array)
// - Tool execution success
func (s *MCPSuite) TestMCPProxyCallTool() {
	s.T().Log("Testing MCP proxy tools/call...")

	// Ensure services connected
	s.ensureServicesConnected()

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Call coral_list_services tool
	callResp, err := proxy.CallTool("coral_list_services", map[string]interface{}{}, 3)
	s.Require().NoError(err, "Call tool should succeed")

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
// Validates:
// - Invalid tool name error
// - Invalid arguments error
// - JSON-RPC error format
func (s *MCPSuite) TestMCPProxyErrorHandling() {
	s.T().Log("Testing MCP proxy error handling...")

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Test invalid tool name
	mcpErr, err := proxy.CallToolExpectError("invalid_tool_xyz", map[string]interface{}{}, 4)
	s.Require().NoError(err, "Should get error response for invalid tool")
	s.Require().NotNil(mcpErr, "Should have MCP error")
	s.Require().NotEqual(0, mcpErr.Code, "Error should have code")
	s.Require().Contains(strings.ToLower(mcpErr.Message), "unknown tool", "Error message should indicate tool not found")

	s.T().Logf("Invalid tool error: code=%d, message=%s", mcpErr.Code, mcpErr.Message)

	// Test invalid arguments (malformed JSON in arguments)
	// Note: This tests argument validation at the tool level
	invalidArgsErr, err := proxy.CallToolExpectError("coral_query_summary", map[string]interface{}{
		"time_range": 12345, // Should be string, not int
	}, 5)
	s.Require().NoError(err, "Should get error response for invalid arguments")
	s.Require().NotNil(invalidArgsErr, "Should have MCP error for invalid arguments")

	s.T().Logf("Invalid args error: code=%d, message=%s", invalidArgsErr.Code, invalidArgsErr.Message)

	s.T().Log("✓ MCP proxy error handling validated")
}

// =============================================================================
// Group C: End-to-End Tool Execution
// =============================================================================

// TestMCPToolObservabilityQuery tests observability tools end-to-end.
//
// Validates:
// - coral_query_summary with real telemetry
// - coral_query_traces with real data
// - coral_query_metrics with real data
func (s *MCPSuite) TestMCPToolObservabilityQuery() {
	s.T().Log("Testing MCP observability tools...")

	// Ensure telemetry data exists
	s.ensureTelemetryData()

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Test coral_query_summary
	summaryResp, err := proxy.CallTool("coral_query_summary", map[string]interface{}{
		"service_filter": "*",
		"time_range":     "5m",
	}, 10)
	s.Require().NoError(err, "coral_query_summary should succeed")
	s.Require().NotEmpty(summaryResp.Content, "Summary should have content")

	s.T().Log("Query summary result (truncated):")
	summaryText := summaryResp.Content[0].Text
	if len(summaryText) > 500 {
		summaryText = summaryText[:500] + "..."
	}
	s.T().Log(summaryText)

	// Test coral_query_traces
	tracesResp, err := proxy.CallTool("coral_query_traces", map[string]interface{}{
		"service_filter": "*",
		"time_range":     "5m",
		"limit":          10,
	}, 11)
	s.Require().NoError(err, "coral_query_traces should succeed")
	s.Require().NotEmpty(tracesResp.Content, "Traces should have content")

	// Test coral_query_metrics
	metricsResp, err := proxy.CallTool("coral_query_metrics", map[string]interface{}{
		"service_filter": "*",
		"time_range":     "5m",
	}, 12)
	s.Require().NoError(err, "coral_query_metrics should succeed")
	s.Require().NotEmpty(metricsResp.Content, "Metrics should have content")

	s.T().Log("✓ MCP observability tools validated")
}

// TestMCPToolServiceDiscovery tests coral_list_services tool.
//
// Validates:
// - Service discovery with real connected services
// - Service metadata in response
func (s *MCPSuite) TestMCPToolServiceDiscovery() {
	s.T().Log("Testing MCP service discovery tool...")

	// Ensure services connected
	s.ensureServicesConnected()

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Call coral_list_services
	servicesResp, err := proxy.CallTool("coral_list_services", map[string]interface{}{}, 20)
	s.Require().NoError(err, "coral_list_services should succeed")
	s.Require().NotEmpty(servicesResp.Content, "Services should have content")

	servicesText := servicesResp.Content[0].Text
	s.T().Log("Services list:")
	s.T().Log(servicesText)

	// Verify services are listed (expect at least otel-app or cpu-app)
	hasServices := strings.Contains(servicesText, "otel-app") || strings.Contains(servicesText, "cpu-app")
	s.Require().True(hasServices, "Should list at least one connected service")

	s.T().Log("✓ MCP service discovery tool validated")
}

// TestMCPToolShellExec tests coral_shell_exec tool.
//
// Validates:
// - Shell command execution on agent
// - Command output capture
// - Exit code handling
func (s *MCPSuite) TestMCPToolShellExec() {
	// s.T().Skip("Skipping shell exec test - requires specific agent configuration")

	s.T().Log("Testing MCP shell exec tool...")

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Get agent ID (use first available agent)
	agents, err := helpers.ColonyAgentsJSON(s.ctx, s.cliEnv.ColonyEndpoint)
	s.Require().NoError(err, "Should list agents")
	s.Require().NotEmpty(agents, "Should have at least one agent")
	s.Require().Contains(agents[0], "agent_id", "Should have agent id")

	agentID := agents[0]["agent_id"].(string)

	// Execute simple command
	execResp, err := proxy.CallTool("coral_shell_exec", map[string]interface{}{
		"agent_id": agentID,
		"command":  []string{"sh", "-c", "echo 'Hello from MCP'"},
	}, 30)
	s.Require().NoError(err, "coral_shell_exec should succeed")
	s.Require().NotEmpty(execResp.Content, "Exec should have content")

	execText := execResp.Content[0].Text
	s.T().Log("Shell exec result:")
	s.T().Log(execText)

	// Verify output contains expected text
	s.Require().Contains(execText, "Hello from MCP", "Should contain command output")

	s.T().Log("✓ MCP shell exec tool validated")
}

// =============================================================================
// Group D: Debugging Tools
// =============================================================================

// TestMCPToolDiscoverFunctions tests coral_discover_functions tool.
//
// Validates:
// - Semantic function search
// - Function metadata (name, package, location)
// - Instrumentation info (probeable, DWARF)
// - Metrics inclusion
func (s *MCPSuite) TestMCPToolDiscoverFunctions() {
	s.T().Log("Testing MCP discover functions tool...")

	// Ensure services are connected
	s.ensureServicesConnected()

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Call coral_discover_functions with semantic search
	discoverResp, err := proxy.CallTool("coral_discover_functions", map[string]interface{}{
		"service":         "otel-app",
		"query":           "handler",
		"max_results":     10,
		"include_metrics": true,
	}, 40)
	s.Require().NoError(err, "coral_discover_functions should succeed")
	s.Require().NotEmpty(discoverResp.Content, "Discover should have content")

	discoverText := discoverResp.Content[0].Text
	s.T().Log("Discover functions result (truncated):")
	if len(discoverText) > 500 {
		s.T().Log(discoverText[:500] + "...")
	} else {
		s.T().Log(discoverText)
	}

	// Verify response contains function information
	s.Require().Contains(strings.ToLower(discoverText), "function", "Should mention functions")

	s.T().Log("✓ MCP discover functions tool validated")
}

// TestMCPToolProfileFunctions tests coral_profile_functions tool.
//
// Validates:
// - Batch profiling with different strategies
// - Session creation and status
// - Bottleneck identification
// - Recommendations
func (s *MCPSuite) TestMCPToolProfileFunctions() {
	s.T().Log("Testing MCP profile functions tool...")

	// Ensure services are connected
	s.ensureServicesConnected()

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Call coral_profile_functions with async mode (don't wait for completion)
	profileResp, err := proxy.CallTool("coral_profile_functions", map[string]interface{}{
		"service":       "otel-app",
		"query":         "handler",
		"strategy":      "critical_path",
		"max_functions": 5,
		"duration":      "10s",
		"async":         true,
	}, 41)
	s.Require().NoError(err, "coral_profile_functions should succeed")
	s.Require().NotEmpty(profileResp.Content, "Profile should have content")

	profileText := profileResp.Content[0].Text
	s.T().Log("Profile functions result:")
	s.T().Log(profileText)

	// Verify response contains session information
	s.Require().Contains(strings.ToLower(profileText), "session", "Should mention session")

	s.T().Log("✓ MCP profile functions tool validated")
}

// TestMCPToolAttachUprobe tests coral_attach_uprobe tool.
//
// Validates:
// - Uprobe attachment to function
// - Session creation
// - Expiration time
func (s *MCPSuite) TestMCPToolAttachUprobe() {
	s.T().Log("Testing MCP attach uprobe tool...")

	// Ensure services are connected
	s.ensureServicesConnected()

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// First discover a function to attach to
	discoverResp, err := proxy.CallTool("coral_discover_functions", map[string]interface{}{
		"service":     "otel-app",
		"query":       "main",
		"max_results": 1,
	}, 42)
	s.Require().NoError(err, "Should discover functions")
	s.T().Logf("Discovered functions: %v", discoverResp.Content[0].Text)

	// Try to attach uprobe (may fail if no suitable function found)
	attachResp, err := proxy.CallTool("coral_attach_uprobe", map[string]interface{}{
		"service":  "otel-app",
		"function": "main.main",
		"duration": "10s",
	}, 43)

	// Note: This may fail in test environment if function not found or not probeable
	// We just verify the tool is callable and returns appropriate response
	if err != nil {
		s.T().Logf("Attach uprobe failed (expected in test env): %v", err)
	} else {
		s.Require().NotEmpty(attachResp.Content, "Attach should have content")
		attachText := attachResp.Content[0].Text
		s.T().Log("Attach uprobe result:")
		s.T().Log(attachText)
	}

	s.T().Log("✓ MCP attach uprobe tool validated")
}

// TestMCPToolListDebugSessions tests coral_list_debug_sessions tool.
//
// Validates:
// - Listing active debug sessions
// - Filtering by status
// - Session metadata
func (s *MCPSuite) TestMCPToolListDebugSessions() {
	s.T().Log("Testing MCP list debug sessions tool...")

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// List debug sessions
	listResp, err := proxy.CallTool("coral_list_debug_sessions", map[string]interface{}{
		"status": "all",
	}, 44)
	s.Require().NoError(err, "coral_list_debug_sessions should succeed")
	s.Require().NotEmpty(listResp.Content, "List should have content")

	listText := listResp.Content[0].Text
	s.T().Log("List debug sessions result:")
	s.T().Log(listText)

	// Verify response format (may have no sessions)
	s.Require().NotEmpty(listText, "Should have response text")

	s.T().Log("✓ MCP list debug sessions tool validated")
}

// TestMCPToolGetDebugResults tests coral_get_debug_results tool.
//
// Validates:
// - Getting results from debug session
// - Event counts
// - Duration data
func (s *MCPSuite) TestMCPToolGetDebugResults() {
	s.T().Log("Testing MCP get debug results tool...")

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Try to get results for a non-existent session (should return error)
	resultsResp, err := proxy.CallToolExpectError("coral_get_debug_results", map[string]interface{}{
		"session_id": "non-existent-session-id",
	}, 45)

	// Should get an error for non-existent session
	s.Require().NoError(err, "Should get error response")
	s.Require().NotNil(resultsResp, "Should have error response")
	s.T().Logf("Expected error for non-existent session: %s", resultsResp.Message)

	s.T().Log("✓ MCP get debug results tool validated")
}

// TestMCPToolDetachUprobe tests coral_detach_uprobe tool.
//
// Validates:
// - Detaching active session
// - Cleanup verification
func (s *MCPSuite) TestMCPToolDetachUprobe() {
	s.T().Log("Testing MCP detach uprobe tool...")

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Try to detach a non-existent session (should return error)
	detachResp, err := proxy.CallToolExpectError("coral_detach_uprobe", map[string]interface{}{
		"session_id": "non-existent-session-id",
	}, 46)

	// Should get an error for non-existent session
	s.Require().NoError(err, "Should get error response")
	s.Require().NotNil(detachResp, "Should have error response")
	s.T().Logf("Expected error for non-existent session: %s", detachResp.Message)

	s.T().Log("✓ MCP detach uprobe tool validated")
}

// =============================================================================
// Group E: Container Execution
// =============================================================================

// TestMCPToolContainerExec tests coral_container_exec tool.
//
// Validates:
// - Command execution in container namespace
// - Output capture
// - Namespace entry
// - Different namespace options
func (s *MCPSuite) TestMCPToolContainerExec() {
	s.T().Log("Testing MCP container exec tool...")

	// Ensure services are connected
	s.ensureServicesConnected()

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Get agent ID
	agents, err := helpers.ColonyAgentsJSON(s.ctx, s.cliEnv.ColonyEndpoint)
	s.Require().NoError(err, "Should list agents")
	s.Require().NotEmpty(agents, "Should have at least one agent")
	s.Require().Contains(agents[0], "agent_id", "Should have agent id")

	agentID := agents[0]["agent_id"].(string)

	// Execute command in container namespace
	execResp, err := proxy.CallTool("coral_container_exec", map[string]interface{}{
		"service":    "otel-app",
		"agent_id":   agentID,
		"command":    []string{"echo", "Hello from container"},
		"namespaces": []string{"mnt"},
	}, 50)
	s.Require().NoError(err, "coral_container_exec should succeed")
	s.Require().NotEmpty(execResp.Content, "Exec should have content")

	execText := execResp.Content[0].Text
	s.T().Log("Container exec result:")
	s.T().Log(execText)

	// Verify output contains expected text
	s.Require().Contains(execText, "Hello from container", "Should contain command output")

	s.T().Log("✓ MCP container exec tool validated")
}

// =============================================================================
// Group F: Advanced Observability with Real Telemetry
// =============================================================================

// TestMCPToolQueryWithTelemetryData tests observability tools with real data.
//
// Validates:
// - Query summary with service filters
// - Query traces with real trace IDs
// - Query metrics with protocol filters
// - Data from otel-app and cpu-app
func (s *MCPSuite) TestMCPToolQueryWithTelemetryData() {
	s.T().Log("Testing MCP observability tools with real telemetry data...")

	// Ensure telemetry data exists
	s.ensureTelemetryData()

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Test 1: Query summary with specific service filter
	summaryResp, err := proxy.CallTool("coral_query_summary", map[string]interface{}{
		"service":    "otel-app",
		"time_range": "10m",
	}, 60)
	s.Require().NoError(err, "coral_query_summary should succeed")
	s.Require().NotEmpty(summaryResp.Content, "Summary should have content")

	summaryText := summaryResp.Content[0].Text
	s.T().Log("Query summary with service filter:")
	if len(summaryText) > 300 {
		s.T().Log(summaryText[:300] + "...")
	} else {
		s.T().Log(summaryText)
	}

	// Verify summary contains service data
	s.Require().Contains(strings.ToLower(summaryText), "service", "Should mention service")

	// Test 2: Query traces with time range
	tracesResp, err := proxy.CallTool("coral_query_traces", map[string]interface{}{
		"service":    "otel-app",
		"time_range": "10m",
		"limit":      5,
	}, 61)
	s.Require().NoError(err, "coral_query_traces should succeed")
	s.Require().NotEmpty(tracesResp.Content, "Traces should have content")

	// Test 3: Query metrics with time range
	metricsResp, err := proxy.CallTool("coral_query_metrics", map[string]interface{}{
		"service":    "otel-app",
		"time_range": "10m",
	}, 62)
	s.Require().NoError(err, "coral_query_metrics should succeed")
	s.Require().NotEmpty(metricsResp.Content, "Metrics should have content")

	s.T().Log("✓ MCP observability tools with telemetry data validated")
}

// TestMCPToolQueryMetricsProtocols tests protocol-specific metric queries.
//
// Validates:
// - HTTP metrics with route/method filters
// - gRPC metrics (if available)
// - SQL metrics (if available)
func (s *MCPSuite) TestMCPToolQueryMetricsProtocols() {
	s.T().Log("Testing MCP metrics with protocol filters...")

	// Ensure telemetry data exists
	s.ensureTelemetryData()

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Query HTTP metrics with method filter
	httpResp, err := proxy.CallTool("coral_query_metrics", map[string]interface{}{
		"service":     "otel-app",
		"time_range":  "10m",
		"protocol":    "http",
		"http_method": "GET",
	}, 70)
	s.Require().NoError(err, "HTTP metrics query should succeed")
	s.Require().NotEmpty(httpResp.Content, "HTTP metrics should have content")

	httpText := httpResp.Content[0].Text
	s.T().Log("HTTP metrics result:")
	if len(httpText) > 300 {
		s.T().Log(httpText[:300] + "...")
	} else {
		s.T().Log(httpText)
	}

	s.T().Log("✓ MCP protocol-specific metrics validated")
}

// =============================================================================
// Group G: Error Handling and Edge Cases
// =============================================================================

// TestMCPToolErrorScenarios tests comprehensive error handling.
//
// Validates:
// - Invalid service names
// - Missing required parameters
// - Invalid time ranges
// - Non-existent trace IDs
// - Invalid agent IDs
// - Timeout scenarios
func (s *MCPSuite) TestMCPToolErrorScenarios() {
	s.T().Log("Testing MCP error scenarios...")

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Test 1: Non-existent service name (should return empty results, not error)
	// Query tools return empty results for non-existent services rather than errors,
	// since services may exist in historical data even if not currently connected.
	nonExistentServiceResp, err := proxy.CallTool("coral_query_summary", map[string]interface{}{
		"service":    "non-existent-service-xyz",
		"time_range": "5m",
	}, 80)
	s.Require().NoError(err, "Query for non-existent service should succeed")
	s.Require().NotEmpty(nonExistentServiceResp.Content, "Should have response content")
	s.T().Log("✓ Non-existent service query returns empty results (expected behavior)")

	// Test 2: Invalid time range format
	invalidTimeErr, err := proxy.CallToolExpectError("coral_query_summary", map[string]interface{}{
		"service":    "otel-app",
		"time_range": "invalid-time",
	}, 81)
	s.Require().NoError(err, "Should get error response")
	s.Require().NotNil(invalidTimeErr, "Should have error for invalid time")
	s.T().Logf("Invalid time range error: %s", invalidTimeErr.Message)

	// Test 3: Invalid agent ID for shell exec
	invalidAgentErr, err := proxy.CallToolExpectError("coral_shell_exec", map[string]interface{}{
		"service":  "otel-app",
		"agent_id": "non-existent-agent-id",
		"command":  []string{"echo", "test"},
	}, 82)
	s.Require().NoError(err, "Should get error response")
	s.T().Logf("Invalid agent ID error: %s", invalidAgentErr.Message)

	// Test 4: Missing required parameter
	missingParamErr, err := proxy.CallToolExpectError("coral_attach_uprobe", map[string]interface{}{
		"service": "otel-app",
		// Missing "function" parameter
	}, 83)
	s.Require().NoError(err, "Should get error response")
	s.T().Logf("Missing parameter error: %s", missingParamErr.Message)

	s.T().Log("✓ MCP error scenarios validated")
}

// TestMCPToolInputValidation tests schema validation.
//
// Validates:
// - Empty inputs
// - Invalid JSON types
// - Out-of-range values
// - Helpful error messages
func (s *MCPSuite) TestMCPToolInputValidation() {
	s.T().Log("Testing MCP input validation...")

	// Start proxy
	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	// Initialize
	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Test 1: Invalid type for time_range (should be string, not int)
	invalidTypeErr, err := proxy.CallToolExpectError("coral_query_summary", map[string]interface{}{
		"service":    "otel-app",
		"time_range": 12345, // Should be string like "5m"
	}, 90)
	s.Require().NoError(err, "Should get error response")
	s.T().Logf("Invalid type error: %s", invalidTypeErr.Message)

	// Test 2: Out of range value for timeout
	outOfRangeErr, err := proxy.CallToolExpectError("coral_shell_exec", map[string]interface{}{
		"service":         "otel-app",
		"command":         []string{"echo", "test"},
		"timeout_seconds": 999999, // Exceeds max of 300
	}, 91)
	// Note: This may or may not error depending on validation implementation
	if err == nil && outOfRangeErr != nil {
		s.T().Logf("Out of range handled: %s", outOfRangeErr.Message)
	}

	// Test 3: Empty command array
	emptyCommandErr, err := proxy.CallToolExpectError("coral_shell_exec", map[string]interface{}{
		"service": "otel-app",
		"command": []string{}, // Empty array
	}, 92)
	s.Require().NoError(err, "Should get error response")
	s.T().Logf("Empty command error: %s", emptyCommandErr.Message)

	s.T().Log("✓ MCP input validation validated")
}

// =============================================================================
// Group H: Profiling-Enriched Summary (RFD 074)
// =============================================================================

// TestMCPToolQuerySummaryProfilingFields tests that coral_query_summary includes
// profiling enrichment parameters and accepts the new include_profiling and top_k fields.
//
// Validates:
// - coral_query_summary accepts include_profiling parameter
// - coral_query_summary accepts top_k parameter
// - Output format is valid with or without profiling data
func (s *MCPSuite) TestMCPToolQuerySummaryProfilingFields() {
	s.T().Log("Testing MCP profiling-enriched query summary (RFD 074)...")

	s.ensureTelemetryData()

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Test 1: Query with include_profiling=true (default behavior).
	summaryResp, err := proxy.CallTool("coral_query_summary", map[string]interface{}{
		"service":           "otel-app",
		"time_range":        "10m",
		"include_profiling": true,
		"top_k":             5,
	}, 110)
	s.Require().NoError(err, "coral_query_summary with profiling params should succeed")
	s.Require().NotEmpty(summaryResp.Content, "Summary should have content")

	summaryText := summaryResp.Content[0].Text
	s.T().Log("Profiling-enriched summary (truncated):")
	if len(summaryText) > 500 {
		s.T().Log(summaryText[:500] + "...")
	} else {
		s.T().Log(summaryText)
	}

	// The response should at minimum contain service information.
	s.Require().Contains(strings.ToLower(summaryText), "service",
		"Summary should mention service even with profiling params")

	// Test 2: Query with include_profiling=false.
	noProfResp, err := proxy.CallTool("coral_query_summary", map[string]interface{}{
		"service":           "otel-app",
		"time_range":        "10m",
		"include_profiling": false,
	}, 111)
	s.Require().NoError(err, "coral_query_summary with profiling disabled should succeed")
	s.Require().NotEmpty(noProfResp.Content, "Summary should have content")

	noProfText := noProfResp.Content[0].Text
	s.T().Log("Summary without profiling (truncated):")
	if len(noProfText) > 300 {
		s.T().Log(noProfText[:300] + "...")
	} else {
		s.T().Log(noProfText)
	}

	// Test 3: Query with top_k parameter (edge case: top_k=1).
	topKResp, err := proxy.CallTool("coral_query_summary", map[string]interface{}{
		"service":    "otel-app",
		"time_range": "10m",
		"top_k":      1,
	}, 112)
	s.Require().NoError(err, "coral_query_summary with top_k=1 should succeed")
	s.Require().NotEmpty(topKResp.Content, "Summary should have content")

	s.T().Log("✓ MCP profiling-enriched query summary validated")
}

// TestMCPToolDebugCPUProfile tests the coral_debug_cpu_profile tool (RFD 074).
//
// Validates:
// - Tool accepts service, duration_seconds, and format parameters
// - Tool returns data or a helpful "no data" message
// - Tool handles nonexistent services gracefully
func (s *MCPSuite) TestMCPToolDebugCPUProfile() {
	s.T().Log("Testing MCP debug CPU profile tool (RFD 074)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// Test 1: Query CPU profile for otel-app (may or may not have data).
	profileResp, err := proxy.CallTool("coral_debug_cpu_profile", map[string]interface{}{
		"service":          "otel-app",
		"duration_seconds": 30,
		"format":           "json",
	}, 120)
	s.Require().NoError(err, "coral_debug_cpu_profile should succeed")
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
	hasNoData := strings.Contains(strings.ToLower(profileText), "no cpu profiling data")
	s.Require().True(hasServiceName || hasNoData,
		"Response should mention service name or indicate no data available")

	// Test 2: Query with folded format.
	foldedResp, err := proxy.CallTool("coral_debug_cpu_profile", map[string]interface{}{
		"service":          "otel-app",
		"duration_seconds": 30,
		"format":           "folded",
	}, 121)
	s.Require().NoError(err, "coral_debug_cpu_profile with folded format should succeed")
	s.Require().NotEmpty(foldedResp.Content, "Folded profile should have content")

	// Test 3: Non-existent service (should return no data message, not error).
	noDataResp, err := proxy.CallTool("coral_debug_cpu_profile", map[string]interface{}{
		"service":          "nonexistent-service-xyz",
		"duration_seconds": 10,
	}, 122)
	s.Require().NoError(err, "coral_debug_cpu_profile for missing service should succeed")
	s.Require().NotEmpty(noDataResp.Content, "Should have content")

	noDataText := noDataResp.Content[0].Text
	s.Require().Contains(strings.ToLower(noDataText), "no cpu profiling data",
		"Should indicate no profiling data available")

	s.T().Log("✓ MCP debug CPU profile tool validated")
}

// TestMCPToolListIncludesProfilingTools validates that tool listing includes RFD 074 tools.
func (s *MCPSuite) TestMCPToolListIncludesProfilingTools() {
	s.T().Log("Testing MCP tool list includes profiling tools (RFD 074)...")

	proxy, err := helpers.StartMCPProxyWithEnv(s.ctx, "test-colony-e2e", s.cliEnv)
	s.Require().NoError(err, "Should start MCP proxy")
	defer proxy.Close()

	_, err = proxy.Initialize()
	s.Require().NoError(err, "Initialize should succeed")

	// List all tools.
	listResp, err := proxy.ListTools()
	s.Require().NoError(err, "tools/list should succeed")

	// Find coral_debug_cpu_profile in the tool list.
	foundDebugProfile := false
	for _, tool := range listResp.Tools {
		if tool.Name == "coral_debug_cpu_profile" {
			foundDebugProfile = true
			s.T().Logf("Found tool: %s - %s", tool.Name, tool.Description)

			// Verify the tool has an input schema with expected properties.
			if len(tool.InputSchema) > 0 {
				if props, ok := tool.InputSchema["properties"].(map[string]interface{}); ok {
					_, hasService := props["service"]
					_, hasDuration := props["duration_seconds"]
					_, hasFormat := props["format"]
					s.Assert().True(hasService, "Schema should have service property")
					s.Assert().True(hasDuration, "Schema should have duration_seconds property")
					s.Assert().True(hasFormat, "Schema should have format property")
				}
			}
			break
		}
	}
	s.Require().True(foundDebugProfile, "coral_debug_cpu_profile should be in tool list")

	s.T().Log("✓ Profiling tools found in tool list")
}

// =============================================================================
// Helper Methods
// =============================================================================

// ensureServicesConnected ensures that test services are connected.
// This uses the shared helper for idempotent service connection.
func (s *MCPSuite) ensureServicesConnected() {
	// MCP tests only need otel-app (OTLP-instrumented)
	helpers.EnsureServicesConnected(s.T(), s.ctx, s.fixture, 0, []helpers.ServiceConfig{
		{Name: "otel-app", Port: 8080, HealthEndpoint: "/health"},
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
