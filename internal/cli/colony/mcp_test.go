package colony

import (
	"context"
	"encoding/json"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/logging"
)

// mockColonyClient implements the colony RPC calls needed for testing.
type mockColonyClient struct {
	tools []*colonyv1.ToolInfo
}

func (m *mockColonyClient) ListTools(ctx context.Context, req *connect.Request[colonyv1.ListToolsRequest]) (*connect.Response[colonyv1.ListToolsResponse], error) {
	return connect.NewResponse(&colonyv1.ListToolsResponse{
		Tools: m.tools,
	}), nil
}

func (m *mockColonyClient) CallTool(ctx context.Context, req *connect.Request[colonyv1.CallToolRequest]) (*connect.Response[colonyv1.CallToolResponse], error) {
	// Simple mock: echo back the tool name in the result
	return connect.NewResponse(&colonyv1.CallToolResponse{
		Result:  "Mock result for " + req.Msg.ToolName,
		Success: true,
	}), nil
}

func (m *mockColonyClient) GetStatus(ctx context.Context, req *connect.Request[colonyv1.GetStatusRequest]) (*connect.Response[colonyv1.GetStatusResponse], error) {
	return connect.NewResponse(&colonyv1.GetStatusResponse{}), nil
}

func (m *mockColonyClient) ListAgents(ctx context.Context, req *connect.Request[colonyv1.ListAgentsRequest]) (*connect.Response[colonyv1.ListAgentsResponse], error) {
	return connect.NewResponse(&colonyv1.ListAgentsResponse{}), nil
}

func (m *mockColonyClient) GetTopology(ctx context.Context, req *connect.Request[colonyv1.GetTopologyRequest]) (*connect.Response[colonyv1.GetTopologyResponse], error) {
	return connect.NewResponse(&colonyv1.GetTopologyResponse{}), nil
}

func (m *mockColonyClient) QueryTelemetry(ctx context.Context, req *connect.Request[colonyv1.QueryTelemetryRequest]) (*connect.Response[colonyv1.QueryTelemetryResponse], error) {
	return connect.NewResponse(&colonyv1.QueryTelemetryResponse{}), nil
}

func (m *mockColonyClient) QueryEbpfMetrics(ctx context.Context, req *connect.Request[agentv1.QueryEbpfMetricsRequest]) (*connect.Response[agentv1.QueryEbpfMetricsResponse], error) {
	return connect.NewResponse(&agentv1.QueryEbpfMetricsResponse{}), nil
}

func (m *mockColonyClient) StreamTool(ctx context.Context) *connect.BidiStreamForClient[colonyv1.StreamToolRequest, colonyv1.StreamToolResponse] {
	return nil
}

func (m *mockColonyClient) RequestCertificate(ctx context.Context, req *connect.Request[colonyv1.RequestCertificateRequest]) (*connect.Response[colonyv1.RequestCertificateResponse], error) {
	return connect.NewResponse(&colonyv1.RequestCertificateResponse{}), nil
}

func (m *mockColonyClient) RevokeCertificate(ctx context.Context, req *connect.Request[colonyv1.RevokeCertificateRequest]) (*connect.Response[colonyv1.RevokeCertificateResponse], error) {
	return connect.NewResponse(&colonyv1.RevokeCertificateResponse{}), nil
}

// TestMCPProxyInitialize tests the initialize method.
func TestMCPProxyInitialize(t *testing.T) {
	// Create mock colony client.
	mockClient := &mockColonyClient{}

	// Create proxy.
	proxy := &mcpProxy{
		client:   mockClient,
		colonyID: "test-colony",
		logger:   logging.New(logging.Config{Level: "error"}),
	}

	// Create initialize request.
	req := &mcpRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params:  map[string]interface{}{},
	}

	// Handle request.
	resp := proxy.handleRequest(context.Background(), req)

	// Validate response.
	require.NotNil(t, resp)
	assert.Equal(t, "2.0", resp.JSONRPC)
	assert.Equal(t, 1, resp.ID)
	assert.Nil(t, resp.Error, "Should not have error")
	assert.NotNil(t, resp.Result, "Should have result")

	// Parse result.
	result, ok := resp.Result.(map[string]interface{})
	require.True(t, ok, "Result should be a map")

	// Check protocol version.
	assert.Equal(t, "2024-11-05", result["protocolVersion"])

	// Check server info.
	serverInfo, ok := result["serverInfo"].(map[string]interface{})
	require.True(t, ok, "serverInfo should be a map")
	assert.Equal(t, "coral-test-colony", serverInfo["name"])
	assert.Equal(t, "1.0.0", serverInfo["version"])

	// Check capabilities exist.
	capabilities, ok := result["capabilities"].(map[string]interface{})
	require.True(t, ok, "capabilities should be a map")
	assert.NotNil(t, capabilities["tools"])
}

// TestMCPProxyListTools tests the tools/list method.
func TestMCPProxyListTools(t *testing.T) {
	// Create mock colony client with test tools.
	mockClient := &mockColonyClient{
		tools: []*colonyv1.ToolInfo{
			{
				Name:        "coral_get_service_health",
				Description: "Get service health",
				Enabled:     true,
			},
			{
				Name:        "coral_get_service_topology",
				Description: "Get service topology",
				Enabled:     true,
			},
			{
				Name:        "disabled_tool",
				Description: "This should not appear",
				Enabled:     false,
			},
		},
	}

	// Create proxy.
	proxy := &mcpProxy{
		client:   mockClient,
		colonyID: "test-colony",
		logger:   logging.New(logging.Config{Level: "error"}),
	}

	// Create tools/list request.
	req := &mcpRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
		Params:  map[string]interface{}{},
	}

	// Handle request.
	resp := proxy.handleRequest(context.Background(), req)

	// Validate response.
	require.NotNil(t, resp)
	assert.Equal(t, "2.0", resp.JSONRPC)
	assert.Equal(t, 2, resp.ID)
	assert.Nil(t, resp.Error, "Should not have error")
	assert.NotNil(t, resp.Result, "Should have result")

	// Parse result.
	result, ok := resp.Result.(map[string]interface{})
	require.True(t, ok, "Result should be a map")

	// Check tools list.
	toolsList, ok := result["tools"].([]map[string]interface{})
	require.True(t, ok, "tools should be a list")
	assert.Len(t, toolsList, 2, "Should have 2 enabled tools (disabled_tool filtered out)")

	// Verify tool names.
	toolNames := make([]string, len(toolsList))
	for i, tool := range toolsList {
		toolNames[i] = tool["name"].(string)
	}
	assert.Contains(t, toolNames, "coral_get_service_health")
	assert.Contains(t, toolNames, "coral_get_service_topology")
	assert.NotContains(t, toolNames, "disabled_tool", "Disabled tools should be filtered out")

	// Check tool structure.
	firstTool := toolsList[0]
	assert.NotEmpty(t, firstTool["name"])
	assert.NotEmpty(t, firstTool["description"])
	assert.NotNil(t, firstTool["inputSchema"])
}

// TestMCPProxyCallTool tests the tools/call method.
func TestMCPProxyCallTool(t *testing.T) {
	// Create mock colony client.
	mockClient := &mockColonyClient{}

	// Create proxy.
	proxy := &mcpProxy{
		client:   mockClient,
		colonyID: "test-colony",
		logger:   logging.New(logging.Config{Level: "error"}),
	}

	// Create tools/call request.
	req := &mcpRequest{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "coral_get_service_health",
			"arguments": map[string]interface{}{
				"service_filter": "api*",
			},
		},
	}

	// Handle request.
	resp := proxy.handleRequest(context.Background(), req)

	// Validate response.
	require.NotNil(t, resp)
	assert.Equal(t, "2.0", resp.JSONRPC)
	assert.Equal(t, 3, resp.ID)
	assert.Nil(t, resp.Error, "Should not have error")
	assert.NotNil(t, resp.Result, "Should have result")

	// Parse result.
	result, ok := resp.Result.(map[string]interface{})
	require.True(t, ok, "Result should be a map")

	// Check content structure.
	content, ok := result["content"].([]map[string]interface{})
	require.True(t, ok, "content should be a list")
	require.Len(t, content, 1)

	// Verify content item.
	contentItem := content[0]
	assert.Equal(t, "text", contentItem["type"])
	assert.Contains(t, contentItem["text"], "coral_get_service_health", "Result should mention the tool name")
}

// TestMCPProxyInvalidMethod tests handling of invalid methods.
func TestMCPProxyInvalidMethod(t *testing.T) {
	// Create mock colony client.
	mockClient := &mockColonyClient{}

	// Create proxy.
	proxy := &mcpProxy{
		client:   mockClient,
		colonyID: "test-colony",
		logger:   logging.New(logging.Config{Level: "error"}),
	}

	// Create request with invalid method.
	req := &mcpRequest{
		JSONRPC: "2.0",
		ID:      4,
		Method:  "list_tools", // Wrong! Should be "tools/list"
		Params:  map[string]interface{}{},
	}

	// Handle request.
	resp := proxy.handleRequest(context.Background(), req)

	// Validate error response.
	require.NotNil(t, resp)
	assert.Equal(t, "2.0", resp.JSONRPC)
	assert.Equal(t, 4, resp.ID)
	assert.Nil(t, resp.Result, "Should not have result")
	assert.NotNil(t, resp.Error, "Should have error")

	// Check error details.
	assert.Equal(t, -32601, resp.Error.Code, "Should be 'method not found' error")
	assert.Contains(t, resp.Error.Message, "list_tools")
	assert.Contains(t, resp.Error.Message, "method not found")
}

// TestMCPProxyJSONRPCSerialization tests full JSON-RPC serialization round-trip.
func TestMCPProxyJSONRPCSerialization(t *testing.T) {
	tests := []struct {
		name           string
		requestJSON    string
		expectedMethod string
		expectError    bool
	}{
		{
			name:           "initialize",
			requestJSON:    `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
			expectedMethod: "initialize",
			expectError:    false,
		},
		{
			name:           "tools/list",
			requestJSON:    `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
			expectedMethod: "tools/list",
			expectError:    false,
		},
		{
			name:           "invalid method",
			requestJSON:    `{"jsonrpc":"2.0","id":3,"method":"list_tools","params":{}}`,
			expectedMethod: "list_tools",
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Parse request JSON.
			var req mcpRequest
			err := json.Unmarshal([]byte(tt.requestJSON), &req)
			require.NoError(t, err, "Should parse request JSON")

			// Verify method parsed correctly.
			assert.Equal(t, tt.expectedMethod, req.Method)

			// Create mock and proxy.
			mockClient := &mockColonyClient{
				tools: []*colonyv1.ToolInfo{
					{Name: "test_tool", Description: "Test", Enabled: true},
				},
			}
			proxy := &mcpProxy{
				client:   mockClient,
				colonyID: "test-colony",
				logger:   logging.New(logging.Config{Level: "error"}),
			}

			// Handle request.
			resp := proxy.handleRequest(context.Background(), &req)
			require.NotNil(t, resp)

			// Validate response based on expected outcome.
			if tt.expectError {
				assert.NotNil(t, resp.Error, "Should have error")
				assert.Nil(t, resp.Result, "Should not have result")
			} else {
				assert.Nil(t, resp.Error, "Should not have error")
				assert.NotNil(t, resp.Result, "Should have result")
			}

			// Verify response can be serialized to JSON.
			responseJSON, err := json.Marshal(resp)
			require.NoError(t, err, "Should serialize response to JSON")
			assert.NotEmpty(t, responseJSON)

			// Verify JSON is valid by parsing it back.
			var parsedResp mcpResponse
			err = json.Unmarshal(responseJSON, &parsedResp)
			require.NoError(t, err, "Should parse response JSON")
			assert.Equal(t, "2.0", parsedResp.JSONRPC)
		})
	}
}
