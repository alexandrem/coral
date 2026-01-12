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
	s.Require().Contains(result.Output, "coral_list_services", "Should list coral_list_services tool")
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

	// Ensure services are connected for testing
	s.ensureServicesConnected()

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
		"command":  "echo 'Hello from MCP'",
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
// Helper Methods
// =============================================================================

// ensureServicesConnected ensures that test services are connected.
func (s *MCPSuite) ensureServicesConnected() {
	s.T().Log("Ensuring services are connected...")

	// List current services
	services, err := helpers.ServiceListJSON(s.ctx, s.cliEnv.ColonyEndpoint)
	if err == nil && len(services) > 0 {
		s.T().Logf("Services already connected: %d", len(services))
		return
	}

	// Connect test services if none connected
	// This allows tests to run standalone (e.g., with test filter)
	s.T().Log("No services connected - connecting test services...")

	// Connect OTEL app to agent-0 (primary test app)
	helpers.ConnectServiceToAgent(s.T(), s.ctx, s.fixture, 0, "otel-app", 8080, "/health")

	s.T().Log("✓ Services connected - waiting for colony to poll...")

	// Wait for colony to poll services from agent (runs every 10 seconds)
	time.Sleep(15 * time.Second)

	// Verify services are now listed
	services, err = helpers.ServiceListJSON(s.ctx, s.cliEnv.ColonyEndpoint)
	s.Require().NoError(err, "Failed to list services after connect")
	s.Require().NotEmpty(services, "Services should be registered after connect")

	s.T().Logf("✓ Services registered in colony: %d", len(services))
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
