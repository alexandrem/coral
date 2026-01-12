package helpers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// MCPProxyClient manages a subprocess running the MCP proxy and provides methods
// to send JSON-RPC requests and receive responses.
type MCPProxyClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr io.Reader
	ctx    context.Context
	cancel context.CancelFunc
}

// MCPResponse represents a JSON-RPC 2.0 response from the MCP proxy.
type MCPResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *MCPError       `json:"error,omitempty"`
}

// MCPError represents a JSON-RPC 2.0 error object.
type MCPError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// MCPInitializeResponse represents the response from the initialize method.
type MCPInitializeResponse struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ServerInfo      MCPServerInfo          `json:"serverInfo"`
}

// MCPServerInfo represents MCP server information.
type MCPServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// MCPToolsListResponse represents the response from the tools/list method.
type MCPToolsListResponse struct {
	Tools []MCPTool `json:"tools"`
}

// MCPTool represents an MCP tool metadata.
type MCPTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// MCPCallToolResponse represents the response from the tools/call method.
type MCPCallToolResponse struct {
	Content []MCPContent `json:"content"`
}

// MCPContent represents content in an MCP response.
type MCPContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// StartMCPProxyWithEnv starts the MCP proxy subprocess with a test CLI environment.
//
// The proxy reads colony config from the test environment's HOME directory.
// This is the recommended way to start the proxy in E2E tests.
func StartMCPProxyWithEnv(ctx context.Context, colonyID string, cliEnv *CLITestEnv) (*MCPProxyClient, error) {
	// Create cancellable context for subprocess
	proxyCtx, cancel := context.WithCancel(ctx)

	// Get coral binary path
	coralBin := getCoralBinaryPath()

	// Create command
	cmd := exec.CommandContext(proxyCtx, coralBin, "colony", "mcp", "proxy", "--colony", colonyID)

	// Set environment - start with parent env, then add test env vars
	cmd.Env = os.Environ()
	for key, value := range cliEnv.EnvVars() {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
	}

	return startMCPProxyWithCmd(ctx, proxyCtx, cancel, cmd)
}

// StartMCPProxy starts the MCP proxy subprocess for the given colony.
//
// Deprecated: Use StartMCPProxyWithEnv instead for E2E tests.
// This function is kept for backward compatibility.
func StartMCPProxy(ctx context.Context, colonyEndpoint, colonyID string) (*MCPProxyClient, error) {
	// Create cancellable context for subprocess
	proxyCtx, cancel := context.WithCancel(ctx)

	// Get coral binary path
	coralBin := getCoralBinaryPath()

	// Create command
	cmd := exec.CommandContext(proxyCtx, coralBin, "colony", "mcp", "proxy", "--colony", colonyID)

	// Set environment - start with parent env to inherit PATH, etc.
	cmd.Env = append(os.Environ(), fmt.Sprintf("CORAL_COLONY_ENDPOINT=%s", colonyEndpoint))

	return startMCPProxyWithCmd(ctx, proxyCtx, cancel, cmd)
}

// startMCPProxyWithCmd is the common implementation for starting the MCP proxy.
func startMCPProxyWithCmd(
	ctx context.Context,
	proxyCtx context.Context,
	cancel context.CancelFunc,
	cmd *exec.Cmd,
) (*MCPProxyClient, error) {

	// Setup stdin pipe
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	// Setup stdout pipe
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = stdin.Close()
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	// Setup stderr pipe (for logging)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		_ = stdin.Close()
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the proxy process
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start MCP proxy: %w", err)
	}

	client := &MCPProxyClient{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
		stderr: stderr,
		ctx:    proxyCtx,
		cancel: cancel,
	}

	// Read stderr in background for debugging (errors will be logged to test output)
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			// Stderr output is available but not actively logged to avoid noise
			// Can be retrieved via ReadStderr() if needed
			_ = scanner.Text()
		}
	}()

	// Give proxy a moment to start up and connect to the colony.
	// This prevents EOF errors when immediately trying to send requests.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(500 * time.Millisecond):
		// Proxy should be ready now
	}

	return client, nil
}

// SendRequest sends a JSON-RPC 2.0 request to the MCP proxy and returns the parsed response.
func (c *MCPProxyClient) SendRequest(method string, params interface{}, requestID int) (*MCPResponse, error) {
	// Construct JSON-RPC request
	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      requestID,
		"method":  method,
		"params":  params,
	}

	// Marshal request to JSON
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Send request to stdin
	if _, err := c.stdin.Write(append(requestBytes, '\n')); err != nil {
		return nil, fmt.Errorf("failed to write request to stdin: %w", err)
	}

	// Read response from stdout
	responseLine, err := c.stdout.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read response from stdout: %w", err)
	}

	// Parse response
	var response MCPResponse
	if err := json.Unmarshal(responseLine, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w\nResponse: %s", err, string(responseLine))
	}

	return &response, nil
}

// Initialize sends the MCP initialize request and returns the response.
func (c *MCPProxyClient) Initialize() (*MCPInitializeResponse, error) {
	response, err := c.SendRequest("initialize", map[string]interface{}{}, 1)
	if err != nil {
		return nil, fmt.Errorf("initialize request failed: %w", err)
	}

	if response.Error != nil {
		return nil, fmt.Errorf("initialize returned error: %s (code %d)", response.Error.Message, response.Error.Code)
	}

	var initResp MCPInitializeResponse
	if err := json.Unmarshal(response.Result, &initResp); err != nil {
		return nil, fmt.Errorf("failed to parse initialize response: %w", err)
	}

	return &initResp, nil
}

// ListTools sends the tools/list request and returns the list of tools.
func (c *MCPProxyClient) ListTools() (*MCPToolsListResponse, error) {
	response, err := c.SendRequest("tools/list", map[string]interface{}{}, 2)
	if err != nil {
		return nil, fmt.Errorf("tools/list request failed: %w", err)
	}

	if response.Error != nil {
		return nil, fmt.Errorf("tools/list returned error: %s (code %d)", response.Error.Message, response.Error.Code)
	}

	var toolsResp MCPToolsListResponse
	if err := json.Unmarshal(response.Result, &toolsResp); err != nil {
		return nil, fmt.Errorf("failed to parse tools/list response: %w", err)
	}

	return &toolsResp, nil
}

// CallTool sends a tools/call request for the given tool and arguments.
func (c *MCPProxyClient) CallTool(toolName string, arguments map[string]interface{}, requestID int) (*MCPCallToolResponse, error) {
	params := map[string]interface{}{
		"name":      toolName,
		"arguments": arguments,
	}

	response, err := c.SendRequest("tools/call", params, requestID)
	if err != nil {
		return nil, fmt.Errorf("tools/call request failed: %w", err)
	}

	if response.Error != nil {
		return nil, fmt.Errorf("tools/call returned error: %s (code %d)", response.Error.Message, response.Error.Code)
	}

	var callResp MCPCallToolResponse
	if err := json.Unmarshal(response.Result, &callResp); err != nil {
		return nil, fmt.Errorf("failed to parse tools/call response: %w", err)
	}

	return &callResp, nil
}

// CallToolExpectError sends a tools/call request and expects an error response.
// Returns the error object if the call failed as expected, or an error if it succeeded.
func (c *MCPProxyClient) CallToolExpectError(toolName string, arguments map[string]interface{}, requestID int) (*MCPError, error) {
	params := map[string]interface{}{
		"name":      toolName,
		"arguments": arguments,
	}

	response, err := c.SendRequest("tools/call", params, requestID)
	if err != nil {
		return nil, fmt.Errorf("tools/call request failed: %w", err)
	}

	if response.Error == nil {
		return nil, fmt.Errorf("expected error but tools/call succeeded")
	}

	return response.Error, nil
}

// Close closes the MCP proxy subprocess and cleans up resources.
func (c *MCPProxyClient) Close() error {
	// Close stdin to signal proxy to exit
	if c.stdin != nil {
		_ = c.stdin.Close()
	}

	// Cancel context to kill subprocess if still running
	if c.cancel != nil {
		c.cancel()
	}

	// Wait for process to exit
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Wait()
	}

	return nil
}

// ReadStderr reads and returns stderr output from the proxy (useful for debugging).
func (c *MCPProxyClient) ReadStderr() (string, error) {
	if c.stderr == nil {
		return "", nil
	}

	stderrBytes, err := io.ReadAll(c.stderr)
	if err != nil {
		return "", fmt.Errorf("failed to read stderr: %w", err)
	}

	return string(stderrBytes), nil
}
