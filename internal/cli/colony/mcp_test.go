package colony

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coral-mesh/coral/internal/logging"
)

func newTestProxy() *mcpProxy {
	return &mcpProxy{
		colonyID: "test-colony",
		logger:   logging.New(logging.Config{Level: "error"}),
	}
}

// TestMCPProxyInitialize tests the initialize method.
func TestMCPProxyInitialize(t *testing.T) {
	proxy := newTestProxy()

	req := &mcpRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params:  map[string]interface{}{},
	}

	resp := proxy.handleRequest(context.Background(), req)

	require.NotNil(t, resp)
	assert.Equal(t, "2.0", resp.JSONRPC)
	assert.Equal(t, 1, resp.ID)
	assert.Nil(t, resp.Error)
	assert.NotNil(t, resp.Result)

	result, ok := resp.Result.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "2024-11-05", result["protocolVersion"])

	serverInfo, ok := result["serverInfo"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "coral-test-colony", serverInfo["name"])
	assert.Equal(t, "1.0.0", serverInfo["version"])

	capabilities, ok := result["capabilities"].(map[string]interface{})
	require.True(t, ok)
	assert.NotNil(t, capabilities["tools"])
}

// TestMCPProxyListTools tests that tools/list returns only coral_cli (RFD 100).
func TestMCPProxyListTools(t *testing.T) {
	proxy := newTestProxy()

	req := &mcpRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
		Params:  map[string]interface{}{},
	}

	resp := proxy.handleRequest(context.Background(), req)

	require.NotNil(t, resp)
	assert.Equal(t, "2.0", resp.JSONRPC)
	assert.Nil(t, resp.Error)

	result, ok := resp.Result.(map[string]interface{})
	require.True(t, ok)

	// tools is []interface{} because handleListTools builds it that way.
	toolsList, ok := result["tools"].([]interface{})
	require.True(t, ok, "tools should be a list")
	require.Len(t, toolsList, 1, "Should expose exactly one tool: coral_cli")

	tool, ok := toolsList[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "coral_cli", tool["name"])
	assert.NotEmpty(t, tool["description"])
	assert.NotNil(t, tool["inputSchema"])
}

// TestMCPProxyCallToolUnknown verifies that non-coral_cli tool names are rejected (RFD 100).
func TestMCPProxyCallToolUnknown(t *testing.T) {
	proxy := newTestProxy()

	req := &mcpRequest{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name":      "coral_query_summary",
			"arguments": map[string]interface{}{},
		},
	}

	resp := proxy.handleRequest(context.Background(), req)

	require.NotNil(t, resp)
	assert.NotNil(t, resp.Error, "per-operation MCP tools should be rejected")
	assert.Equal(t, -32601, resp.Error.Code)
	assert.Contains(t, resp.Error.Message, "coral_query_summary")
}

// TestMCPProxyCallToolMissingArgs verifies coral_cli rejects missing args.
func TestMCPProxyCallToolMissingArgs(t *testing.T) {
	proxy := newTestProxy()

	req := &mcpRequest{
		JSONRPC: "2.0",
		ID:      4,
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name":      "coral_cli",
			"arguments": map[string]interface{}{},
		},
	}

	resp := proxy.handleRequest(context.Background(), req)

	require.NotNil(t, resp)
	assert.NotNil(t, resp.Error)
	assert.Equal(t, -32602, resp.Error.Code)
	assert.Contains(t, resp.Error.Message, "args")
}

// TestMCPProxyInvalidMethod tests handling of invalid methods.
func TestMCPProxyInvalidMethod(t *testing.T) {
	proxy := newTestProxy()

	req := &mcpRequest{
		JSONRPC: "2.0",
		ID:      5,
		Method:  "list_tools", // Wrong: should be "tools/list"
		Params:  map[string]interface{}{},
	}

	resp := proxy.handleRequest(context.Background(), req)

	require.NotNil(t, resp)
	assert.NotNil(t, resp.Error)
	assert.Equal(t, -32601, resp.Error.Code)
	assert.Contains(t, resp.Error.Message, "list_tools")
	assert.Contains(t, resp.Error.Message, "method not found")
}

// TestMCPProxyJSONRPCSerialization tests full JSON-RPC serialization round-trip.
func TestMCPProxyJSONRPCSerialization(t *testing.T) {
	tests := []struct {
		name        string
		requestJSON string
		expectError bool
	}{
		{
			name:        "initialize",
			requestJSON: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
			expectError: false,
		},
		{
			name:        "tools/list",
			requestJSON: `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
			expectError: false,
		},
		{
			name:        "invalid method",
			requestJSON: `{"jsonrpc":"2.0","id":3,"method":"list_tools","params":{}}`,
			expectError: true,
		},
	}

	proxy := newTestProxy()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req mcpRequest
			err := json.Unmarshal([]byte(tt.requestJSON), &req)
			require.NoError(t, err)

			resp := proxy.handleRequest(context.Background(), &req)
			require.NotNil(t, resp)

			if tt.expectError {
				assert.NotNil(t, resp.Error)
				assert.Nil(t, resp.Result)
			} else {
				assert.Nil(t, resp.Error)
				assert.NotNil(t, resp.Result)
			}

			// Verify response can be serialized and parsed back.
			responseJSON, err := json.Marshal(resp)
			require.NoError(t, err)
			var parsedResp mcpResponse
			err = json.Unmarshal(responseJSON, &parsedResp)
			require.NoError(t, err)
			assert.Equal(t, "2.0", parsedResp.JSONRPC)
		})
	}
}

// TestAppendProxyFormatJSON tests that --format json is appended correctly.
func TestAppendProxyFormatJSON(t *testing.T) {
	tests := []struct {
		input    []string
		expected []string
	}{
		{
			input:    []string{"query", "traces"},
			expected: []string{"query", "traces", "--format", "json"},
		},
		{
			input:    []string{"query", "traces", "--format", "json"},
			expected: []string{"query", "traces", "--format", "json"},
		},
		{
			input:    []string{"query", "traces", "--format=json"},
			expected: []string{"query", "traces", "--format=json"},
		},
	}

	for _, tt := range tests {
		result := appendProxyFormatJSON(tt.input)
		assert.Equal(t, tt.expected, result)
	}
}
