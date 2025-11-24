package colony

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	colonyv1 "github.com/coral-io/coral/coral/colony/v1"
)

// TestMCPProxyE2E tests the full MCP proxy with a real subprocess and mock colony server.
func TestMCPProxyE2E(t *testing.T) {
	t.Skip("Currently broken, need more work")

	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Step 1: Build the binary if it doesn't exist.
	binaryPath := filepath.Join(os.TempDir(), "coral-test-mcp-e2e")
	buildBinary(t, binaryPath)
	defer func() { _ = os.Remove(binaryPath) }() // TODO: errcheck

	// Step 2: Start mock colony HTTP server.
	mockServer := startMockColonyServer(t)
	defer mockServer.Close()

	// Step 3: Create test colony config pointing to mock server.
	configDir := createTestConfig(t, mockServer.URL)
	defer func() { _ = os.RemoveAll(configDir) }() // TODO: errcheck

	// Step 4: Start proxy subprocess.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, "colony", "mcp", "proxy", "--colony", "test-colony")
	cmd.Env = append(os.Environ(), "CORAL_CONFIG_HOME="+configDir)

	stdin, err := cmd.StdinPipe()
	require.NoError(t, err, "Should create stdin pipe")

	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err, "Should create stdout pipe")

	stderr, err := cmd.StderrPipe()
	require.NoError(t, err, "Should create stderr pipe")

	// Start the proxy.
	err = cmd.Start()
	require.NoError(t, err, "Should start proxy command")

	// Read stderr in background for debugging.
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			t.Logf("PROXY STDERR: %s", scanner.Text())
		}
	}()

	// Give proxy time to start and connect.
	time.Sleep(500 * time.Millisecond)

	// Step 5: Test initialize.
	t.Run("initialize", func(t *testing.T) {
		request := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "initialize",
			"params":  map[string]interface{}{},
		}

		response := sendMCPRequest(t, stdin, stdout, request)

		// Validate response.
		assert.Equal(t, "2.0", response["jsonrpc"])
		assert.Equal(t, float64(1), response["id"])
		assert.Nil(t, response["error"], "Should not have error")

		result, ok := response["result"].(map[string]interface{})
		require.True(t, ok, "result should be a map")

		// Check protocol version.
		assert.Equal(t, "2024-11-05", result["protocolVersion"])

		// Check server info.
		serverInfo := result["serverInfo"].(map[string]interface{})
		assert.Equal(t, "coral-test-colony", serverInfo["name"])
		assert.Equal(t, "1.0.0", serverInfo["version"])
	})

	// Step 6: Test tools/list.
	t.Run("tools/list", func(t *testing.T) {
		request := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "tools/list",
			"params":  map[string]interface{}{},
		}

		response := sendMCPRequest(t, stdin, stdout, request)

		// Validate response.
		assert.Equal(t, "2.0", response["jsonrpc"])
		assert.Equal(t, float64(2), response["id"])
		assert.Nil(t, response["error"], "Should not have error")

		result, ok := response["result"].(map[string]interface{})
		require.True(t, ok, "result should be a map")

		// Check tools list.
		tools := result["tools"].([]interface{})
		require.Len(t, tools, 2, "Should have 2 tools from mock server")

		// Verify first tool.
		tool1 := tools[0].(map[string]interface{})
		assert.Equal(t, "coral_get_service_health", tool1["name"])
		assert.NotEmpty(t, tool1["description"])
	})

	// Step 7: Test tools/call.
	t.Run("tools/call", func(t *testing.T) {
		request := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      3,
			"method":  "tools/call",
			"params": map[string]interface{}{
				"name": "coral_get_service_health",
				"arguments": map[string]interface{}{
					"service_filter": "test*",
				},
			},
		}

		response := sendMCPRequest(t, stdin, stdout, request)

		// Validate response.
		assert.Equal(t, "2.0", response["jsonrpc"])
		assert.Equal(t, float64(3), response["id"])
		assert.Nil(t, response["error"], "Should not have error")

		result, ok := response["result"].(map[string]interface{})
		require.True(t, ok, "result should be a map")

		// Check content.
		content := result["content"].([]interface{})
		require.Len(t, content, 1)

		contentItem := content[0].(map[string]interface{})
		assert.Equal(t, "text", contentItem["type"])
		assert.Contains(t, contentItem["text"], "Mock tool result")
	})

	// Step 8: Test invalid method.
	t.Run("invalid_method", func(t *testing.T) {
		request := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      4,
			"method":  "list_tools", // Wrong! Should be "tools/list"
			"params":  map[string]interface{}{},
		}

		response := sendMCPRequest(t, stdin, stdout, request)

		// Validate error response.
		assert.Equal(t, "2.0", response["jsonrpc"])
		assert.Equal(t, float64(4), response["id"])
		assert.Nil(t, response["result"], "Should not have result")

		errorObj := response["error"].(map[string]interface{})
		assert.Equal(t, float64(-32601), errorObj["code"])
		assert.Contains(t, errorObj["message"], "list_tools")
	})

	// Cleanup: Close stdin to signal proxy to exit.
	_ = stdin.Close() // TODO: errcheck
	_ = cmd.Wait()    // TODO: errcheck
}

// buildBinary builds the coral binary for testing.
func buildBinary(t *testing.T, outputPath string) {
	t.Helper()

	// Check if binary already exists and is recent.
	if info, err := os.Stat(outputPath); err == nil {
		if time.Since(info.ModTime()) < 5*time.Minute {
			t.Logf("Using existing test binary: %s", outputPath)
			return
		}
	}

	t.Logf("Building test binary: %s", outputPath)

	cmd := exec.Command("go", "build", "-o", outputPath, "./cmd/coral")
	cmd.Dir = filepath.Join("..", "..", "..")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Should build binary: %s", string(output))

	t.Logf("Built test binary successfully")
}

// startMockColonyServer starts an HTTP server that mocks the Colony Connect RPCs.
func startMockColonyServer(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	// Mock GetStatus RPC.
	mux.HandleFunc("/coral.colony.v1.ColonyService/GetStatus", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{ // TODO: errcheck
			"colonyId": "test-colony",
			"status":   "running",
		})
	})

	// Mock ListTools RPC.
	mux.HandleFunc("/coral.colony.v1.ColonyService/ListTools", func(w http.ResponseWriter, r *http.Request) {
		tools := []*colonyv1.ToolInfo{
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
		}

		response := &colonyv1.ListToolsResponse{
			Tools: tools,
		}

		// Encode as Connect protocol response.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response) // TODO: errcheck
	})

	// Mock CallTool RPC.
	mux.HandleFunc("/coral.colony.v1.ColonyService/CallTool", func(w http.ResponseWriter, r *http.Request) {
		var req colonyv1.CallToolRequest
		_ = json.NewDecoder(r.Body).Decode(&req) // TODO: errcheck

		response := &colonyv1.CallToolResponse{
			Result:  "Mock tool result for " + req.ToolName,
			Success: true,
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response) // TODO: errcheck
	})

	server := httptest.NewServer(mux)
	t.Logf("Started mock colony server at: %s", server.URL)
	return server
}

// createTestConfig creates a temporary config directory with colony.yaml.
func createTestConfig(t *testing.T, colonyURL string) string {
	t.Helper()

	configDir, err := os.MkdirTemp("", "coral-test-config-*")
	require.NoError(t, err, "Should create temp config dir")

	coloniesDir := filepath.Join(configDir, "colonies")
	err = os.MkdirAll(coloniesDir, 0755)
	require.NoError(t, err, "Should create colonies dir")

	// Extract port from URL (e.g., "http://127.0.0.1:12345" -> "12345").
	var port int
	_, err = fmt.Sscanf(colonyURL, "http://127.0.0.1:%d", &port)
	require.NoError(t, err, "Should parse colony URL")

	// Create colony config.
	config := map[string]interface{}{
		"id":          "test-colony",
		"name":        "Test Colony",
		"application": "test-app",
		"environment": "test",
		"services": map[string]interface{}{
			"connect_port": port,
		},
		"mcp": map[string]interface{}{
			"disabled": false,
		},
	}

	configPath := filepath.Join(coloniesDir, "test-colony.yaml")
	configBytes, err := yaml.Marshal(config)
	require.NoError(t, err, "Should marshal config")

	err = os.WriteFile(configPath, configBytes, 0644)
	require.NoError(t, err, "Should write config file")

	t.Logf("Created test config at: %s", configDir)
	return configDir
}

// sendMCPRequest sends a JSON-RPC request and returns the response.
func sendMCPRequest(t *testing.T, stdin io.Writer, stdout io.Reader, request map[string]interface{}) map[string]interface{} {
	t.Helper()

	// Send request.
	requestBytes, err := json.Marshal(request)
	require.NoError(t, err, "Should marshal request")

	t.Logf("→ Sending MCP request: %s", string(requestBytes))

	_, err = stdin.Write(append(requestBytes, '\n'))
	require.NoError(t, err, "Should write request")

	// Read response.
	reader := bufio.NewReader(stdout)
	responseLine, err := reader.ReadBytes('\n')
	require.NoError(t, err, "Should read response")

	t.Logf("← Received MCP response: %s", string(responseLine))

	var response map[string]interface{}
	err = json.Unmarshal(responseLine, &response)
	require.NoError(t, err, "Should parse response JSON")

	return response
}
