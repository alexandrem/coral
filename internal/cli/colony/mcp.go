package colony

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"connectrpc.com/connect"

	colonyv1 "github.com/coral-io/coral/coral/colony/v1"
	"github.com/coral-io/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-io/coral/internal/config"
	"github.com/coral-io/coral/internal/logging"
)

// newMCPCmd creates the 'coral colony mcp' command.
func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage MCP (Model Context Protocol) server",
		Long: `Manage the colony's MCP server for AI assistant integration.

The MCP server exposes colony observability and debugging capabilities as
tools that can be consumed by any MCP-compatible client (Claude Desktop,
coral ask, custom agents).

Examples:
  # List available MCP tools
  coral colony mcp list-tools

  # Test a tool locally
  coral colony mcp test-tool coral_get_service_health

  # Generate Claude Desktop config
  coral colony mcp generate-config

  # Start MCP server proxy (used by Claude Desktop)
  coral colony mcp proxy`,
	}

	cmd.AddCommand(newMCPListToolsCmd())
	cmd.AddCommand(newMCPTestToolCmd())
	cmd.AddCommand(newMCPGenerateConfigCmd())
	cmd.AddCommand(newMCPProxyCmd())

	return cmd
}

// newMCPListToolsCmd creates the 'coral colony mcp list-tools' command.
func newMCPListToolsCmd() *cobra.Command {
	var colonyID string

	cmd := &cobra.Command{
		Use:   "list-tools",
		Short: "List available MCP tools",
		Long: `List all MCP tools available from the running colony.

This shows the tools that can be called by MCP clients like Claude Desktop.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create resolver.
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}

			// Resolve colony ID.
			if colonyID == "" {
				colonyID, err = resolver.ResolveColonyID()
				if err != nil {
					return fmt.Errorf("failed to resolve colony: %w\n\nRun 'coral init <app-name>' to create a colony", err)
				}
			}

			// Load colony configuration.
			loader := resolver.GetLoader()
			colonyConfig, err := loader.LoadColonyConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			fmt.Printf("Available MCP Tools for colony %s:\n\n", colonyID)

			// List all tools.
			tools := []struct {
				name        string
				description string
				required    []string
			}{
				// Observability tools
				{"coral_get_service_health", "Get current health status of services", []string{}},
				{"coral_get_service_topology", "Get service dependency graph", []string{}},
				{"coral_query_events", "Query operational events", []string{}},
				{"coral_query_beyla_http_metrics", "Query HTTP RED metrics from Beyla", []string{"service"}},
				{"coral_query_beyla_grpc_metrics", "Query gRPC metrics from Beyla", []string{"service"}},
				{"coral_query_beyla_sql_metrics", "Query SQL metrics from Beyla", []string{"service"}},
				{"coral_query_beyla_traces", "Query distributed traces", []string{}},
				{"coral_get_trace_by_id", "Get specific trace by ID", []string{"trace_id"}},
				{"coral_query_telemetry_spans", "Query OTLP spans", []string{"service"}},
				{"coral_query_telemetry_metrics", "Query OTLP metrics", []string{}},
				{"coral_query_telemetry_logs", "Query OTLP logs", []string{}},

				// Live debugging tools (Phase 3)
				{"coral_start_ebpf_collector", "Start on-demand eBPF profiling", []string{"collector_type", "service"}},
				{"coral_stop_ebpf_collector", "Stop running eBPF collector", []string{"collector_id"}},
				{"coral_list_ebpf_collectors", "List active eBPF collectors", []string{}},
				{"coral_exec_command", "Execute command in container", []string{"service", "command"}},
				{"coral_shell_start", "Interactive agent debug shell", []string{"service"}},
			}

			// Filter based on enabled tools if configured.
			enabledTools := colonyConfig.MCP.EnabledTools
			isToolEnabled := func(name string) bool {
				if len(enabledTools) == 0 {
					return true
				}
				for _, t := range enabledTools {
					if t == name {
						return true
					}
				}
				return false
			}

			for _, tool := range tools {
				if !isToolEnabled(tool.name) {
					continue
				}

				fmt.Printf("  %s\n", tool.name)
				fmt.Printf("    %s\n", tool.description)
				if len(tool.required) > 0 {
					fmt.Printf("    Required: %v\n", tool.required)
				}
				fmt.Println()
			}

			if colonyConfig.MCP.Disabled {
				fmt.Println("Note: MCP server is DISABLED in configuration.")
				fmt.Println("      Enable it in the colony config to use these tools.")
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&colonyID, "colony", "", "Colony ID (overrides auto-detection)")

	return cmd
}

// newMCPTestToolCmd creates the 'coral colony mcp test-tool' command.
func newMCPTestToolCmd() *cobra.Command {
	var (
		colonyID string
		argsJSON string
	)

	cmd := &cobra.Command{
		Use:   "test-tool <tool-name>",
		Short: "Test an MCP tool locally",
		Long: `Test an MCP tool by calling it directly without an MCP client.

This is useful for testing tool functionality before using it with
Claude Desktop or other MCP clients.

Examples:
  # Test health check
  coral colony mcp test-tool coral_get_service_health

  # Test with arguments
  coral colony mcp test-tool coral_query_beyla_http_metrics \
    --args '{"service":"api","time_range":"1h"}'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			toolName := args[0]

			// Create resolver.
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}

			// Resolve colony ID.
			if colonyID == "" {
				colonyID, err = resolver.ResolveColonyID()
				if err != nil {
					return fmt.Errorf("failed to resolve colony: %w\n\nRun 'coral init <app-name>' to create a colony", err)
				}
			}

			// Load colony configuration.
			loader := resolver.GetLoader()
			colonyConfig, err := loader.LoadColonyConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			if colonyConfig.MCP.Disabled {
				return fmt.Errorf("MCP server is disabled in configuration")
			}

			// Get connect port.
			connectPort := colonyConfig.Services.ConnectPort
			if connectPort == 0 {
				connectPort = 9000
			}

			// Parse args.
			var toolArgs map[string]interface{}
			if argsJSON != "" {
				if err := json.Unmarshal([]byte(argsJSON), &toolArgs); err != nil {
					return fmt.Errorf("failed to parse args JSON: %w", err)
				}
			} else {
				toolArgs = make(map[string]interface{})
			}

			fmt.Printf("Calling tool: %s\n", toolName)
			argsBytes, _ := json.MarshalIndent(toolArgs, "", "  ")
			fmt.Printf("Arguments: %s\n\n", string(argsBytes))

			// Verify colony is running.
			baseURL := fmt.Sprintf("http://localhost:%d", connectPort)
			client := colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Check colony is running.
			if _, err := client.GetStatus(ctx, connect.NewRequest(&colonyv1.GetStatusRequest{})); err != nil {
				return fmt.Errorf("colony is not running (failed to connect on port %d): %w\n\nRun 'coral colony start' first", connectPort, err)
			}

			// Call the tool via RPC.
			req := &colonyv1.CallToolRequest{
				ToolName:      toolName,
				ArgumentsJson: string(argsBytes),
			}

			resp, err := client.CallTool(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("RPC call failed: %w", err)
			}

			// Display result.
			fmt.Println("Response:")
			if !resp.Msg.Success {
				fmt.Printf("  Error: %s\n", resp.Msg.Error)
				return fmt.Errorf("tool execution failed")
			}

			fmt.Println(resp.Msg.Result)

			return nil
		},
	}

	cmd.Flags().StringVar(&colonyID, "colony", "", "Colony ID (overrides auto-detection)")
	cmd.Flags().StringVar(&argsJSON, "args", "{}", "Tool arguments as JSON")

	return cmd
}

// newMCPGenerateConfigCmd creates the 'coral colony mcp generate-config' command.
func newMCPGenerateConfigCmd() *cobra.Command {
	var (
		allColonies bool
		colonyID    string
	)

	cmd := &cobra.Command{
		Use:   "generate-config",
		Short: "Generate Claude Desktop MCP configuration",
		Long: `Generate configuration snippet for Claude Desktop.

Copy the output to ~/.config/claude/claude_desktop_config.json to enable
Coral MCP integration in Claude Desktop.

Examples:
  # Generate config for default colony
  coral colony mcp generate-config

  # Generate config for all colonies
  coral colony mcp generate-config --all-colonies

  # Generate config for specific colony
  coral colony mcp generate-config --colony my-shop-production`,
		RunE: func(cmd *cobra.Command, args []string) error {
			loader, err := config.NewLoader()
			if err != nil {
				return fmt.Errorf("failed to create config loader: %w", err)
			}

			var colonies []string
			if allColonies {
				colonies, err = loader.ListColonies()
				if err != nil {
					return fmt.Errorf("failed to list colonies: %w", err)
				}
			} else {
				// Use specified colony or default.
				resolver, err := config.NewResolver()
				if err != nil {
					return fmt.Errorf("failed to create resolver: %w", err)
				}

				if colonyID == "" {
					colonyID, err = resolver.ResolveColonyID()
					if err != nil {
						return fmt.Errorf("failed to resolve colony: %w", err)
					}
				}
				colonies = []string{colonyID}
			}

			fmt.Println("Copy this to ~/.config/claude/claude_desktop_config.json:")
			fmt.Println()

			// Generate MCP servers configuration.
			config := map[string]interface{}{
				"mcpServers": make(map[string]interface{}),
			}

			servers := config["mcpServers"].(map[string]interface{})

			if len(colonies) == 1 {
				// Single colony: use simple name "coral".
				servers["coral"] = map[string]interface{}{
					"command": "coral",
					"args":    []string{"colony", "mcp", "proxy"},
				}
			} else {
				// Multiple colonies: use "coral-<colony-id>".
				for _, cid := range colonies {
					serverName := fmt.Sprintf("coral-%s", cid)
					servers[serverName] = map[string]interface{}{
						"command": "coral",
						"args":    []string{"colony", "mcp", "proxy", "--colony", cid},
					}
				}
			}

			// Print formatted JSON.
			output, err := json.MarshalIndent(config, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal config: %w", err)
			}
			fmt.Println(string(output))
			fmt.Println()
			fmt.Println("After adding this config, restart Claude Desktop to enable Coral MCP servers.")

			return nil
		},
	}

	cmd.Flags().BoolVar(&allColonies, "all-colonies", false, "Generate config for all colonies")
	cmd.Flags().StringVar(&colonyID, "colony", "", "Colony ID (overrides default)")

	return cmd
}

// newMCPProxyCmd creates the 'coral colony mcp proxy' command.
func newMCPProxyCmd() *cobra.Command {
	var colonyID string

	cmd := &cobra.Command{
		Use:   "proxy",
		Short: "Proxy to colony MCP server",
		Long: `Connect to a running colony's MCP server and proxy stdio.

This command is used by Claude Desktop to communicate with the colony's
MCP server. It connects to the running colony and forwards MCP protocol
messages over stdio.

The colony must be running for this command to work.

Examples:
  # Connect to default colony
  coral colony mcp proxy

  # Connect to specific colony
  coral colony mcp proxy --colony my-shop-production`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create resolver.
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}

			// Resolve colony ID.
			if colonyID == "" {
				colonyID, err = resolver.ResolveColonyID()
				if err != nil {
					return fmt.Errorf("failed to resolve colony: %w", err)
				}
			}

			// Initialize logger.
			logger := logging.NewWithComponent(logging.Config{
				Level:  "info", // Info level to show startup messages
				Pretty: true,
				Output: os.Stderr, // CRITICAL: Must use stderr, not stdout (MCP uses stdout for JSON-RPC)
			}, "mcp-proxy")

			// Print startup banner to stderr
			logger.Info().
				Str("colony_id", colonyID).
				Msg("Coral MCP Proxy starting...")

			// Check if MCP is disabled.
			loader := resolver.GetLoader()
			colonyConfig, err := loader.LoadColonyConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			if colonyConfig.MCP.Disabled {
				return fmt.Errorf("MCP server is disabled for colony %s", colonyID)
			}

			// Verify colony is running.
			connectPort := colonyConfig.Services.ConnectPort
			if connectPort == 0 {
				connectPort = 9000
			}

			baseURL := fmt.Sprintf("http://localhost:%d", connectPort)

			logger = logger.With().
				Str("colony_id", colonyID).
				Str("connect_url", baseURL).
				Logger()

			logger.Info().Msg("Connecting to colony...")

			client := colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			_, err = client.GetStatus(ctx, connect.NewRequest(&colonyv1.GetStatusRequest{}))
			if err != nil {
				return fmt.Errorf("colony is not running (failed to connect on port %d): %w", connectPort, err)
			}

			logger.Info().Msg("Connected to colony")

			// Proxy is now a simple MCP ↔ RPC translator (no database access!).
			// It reads MCP JSON-RPC from stdin, calls colony via Connect RPC,
			// and writes MCP responses to stdout.

			logger.Info().Msg("MCP protocol: stdio (JSON-RPC 2.0)")
			logger.Info().Msg("Backend: Buf Connect gRPC")
			logger.Info().Msg("MCP Proxy ready - waiting for requests...")

			// Create MCP proxy that translates stdio MCP requests to RPC calls.
			proxy := &mcpProxy{
				client:   client,
				colonyID: colonyID,
				logger:   logger,
			}

			// Start serving MCP protocol on stdio.
			return proxy.Serve(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&colonyID, "colony", "", "Colony ID (overrides auto-detection)")

	return cmd
}

// mcpProxy is a simple MCP ↔ Connect RPC translator.
// It reads MCP JSON-RPC from stdin, translates to Connect RPC, calls colony, and writes MCP responses to stdout.
type mcpProxy struct {
	client   colonyv1connect.ColonyServiceClient
	colonyID string
	logger   logging.Logger
}

// mcpRequest represents an MCP JSON-RPC request.
type mcpRequest struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      interface{}            `json:"id"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params,omitempty"`
}

// mcpResponse represents an MCP JSON-RPC response.
type mcpResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *mcpError   `json:"error,omitempty"`
}

// mcpError represents an MCP JSON-RPC error.
type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve starts the MCP proxy, reading from stdin and writing to stdout.
func (p *mcpProxy) Serve(ctx context.Context) error {
	scanner := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Read MCP request from stdin.
		var req mcpRequest
		if err := scanner.Decode(&req); err != nil {
			if err == io.EOF {
				fmt.Fprintf(os.Stderr, "\n✓ MCP Proxy shutting down (EOF received)\n")
				return nil // Clean shutdown
			}
			p.logger.Error().Err(err).Msg("Failed to decode MCP request")
			continue
		}

		// Log request to stderr for visibility
		if req.Method == "tools/call" {
			if toolName, ok := req.Params["name"].(string); ok {
				fmt.Fprintf(os.Stderr, "→ Tool call: %s\n", toolName)
			}
		} else {
			fmt.Fprintf(os.Stderr, "→ MCP request: %s\n", req.Method)
		}

		p.logger.Info().
			Str("method", req.Method).
			Interface("id", req.ID).
			Msg("Received MCP request")

		// Handle the request.
		resp := p.handleRequest(ctx, &req)

		// Write MCP response to stdout.
		if err := encoder.Encode(resp); err != nil {
			p.logger.Error().Err(err).Msg("Failed to encode MCP response")
			continue
		}

		// Log response to stderr
		if resp.Error != nil {
			fmt.Fprintf(os.Stderr, "← Error: %s\n", resp.Error.Message)
		} else {
			fmt.Fprintf(os.Stderr, "← Success\n")
		}

		p.logger.Info().
			Interface("id", resp.ID).
			Bool("success", resp.Error == nil).
			Msg("Sent MCP response")
	}
}

// handleRequest processes an MCP request and returns an MCP response.
func (p *mcpProxy) handleRequest(ctx context.Context, req *mcpRequest) *mcpResponse {
	// Handle MCP protocol methods.
	switch req.Method {
	case "tools/list":
		return p.handleListTools(ctx, req)
	case "tools/call":
		return p.handleCallTool(ctx, req)
	case "initialize":
		return p.handleInitialize(ctx, req)
	default:
		return &mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &mcpError{
				Code:    -32601,
				Message: fmt.Sprintf("method not found: %s", req.Method),
			},
		}
	}
}

// handleInitialize handles MCP initialize requests.
func (p *mcpProxy) handleInitialize(ctx context.Context, req *mcpRequest) *mcpResponse {
	return &mcpResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]bool{},
			},
			"serverInfo": map[string]interface{}{
				"name":    fmt.Sprintf("coral-%s", p.colonyID),
				"version": "1.0.0",
			},
		},
	}
}

// handleListTools calls colony's ListTools RPC.
func (p *mcpProxy) handleListTools(ctx context.Context, req *mcpRequest) *mcpResponse {
	resp, err := p.client.ListTools(ctx, connect.NewRequest(&colonyv1.ListToolsRequest{}))
	if err != nil {
		return &mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &mcpError{
				Code:    -32603,
				Message: fmt.Sprintf("failed to list tools: %v", err),
			},
		}
	}

	// Convert to MCP format.
	tools := make([]map[string]interface{}, 0, len(resp.Msg.Tools))
	for _, tool := range resp.Msg.Tools {
		if !tool.Enabled {
			continue
		}

		// Parse the input schema JSON from the response.
		var inputSchema map[string]interface{}
		if tool.InputSchemaJson != "" {
			if err := json.Unmarshal([]byte(tool.InputSchemaJson), &inputSchema); err != nil {
				p.logger.Warn().
					Err(err).
					Str("tool", tool.Name).
					Msg("Failed to parse input schema, using empty schema")
				inputSchema = map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				}
			}
		} else {
			// Default to empty object schema if not provided.
			inputSchema = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
		}

		tools = append(tools, map[string]interface{}{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": inputSchema,
		})
	}

	return &mcpResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"tools": tools,
		},
	}
}

// handleCallTool calls colony's CallTool RPC.
func (p *mcpProxy) handleCallTool(ctx context.Context, req *mcpRequest) *mcpResponse {
	// Extract tool name and arguments from params.
	toolName, ok := req.Params["name"].(string)
	if !ok {
		return &mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &mcpError{
				Code:    -32602,
				Message: "missing or invalid 'name' parameter",
			},
		}
	}

	arguments, ok := req.Params["arguments"].(map[string]interface{})
	if !ok {
		arguments = make(map[string]interface{})
	}

	// Convert arguments to JSON.
	argsJSON, err := json.Marshal(arguments)
	if err != nil {
		return &mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &mcpError{
				Code:    -32603,
				Message: fmt.Sprintf("failed to marshal arguments: %v", err),
			},
		}
	}

	// Call colony's CallTool RPC.
	rpcReq := &colonyv1.CallToolRequest{
		ToolName:      toolName,
		ArgumentsJson: string(argsJSON),
	}

	rpcResp, err := p.client.CallTool(ctx, connect.NewRequest(rpcReq))
	if err != nil {
		return &mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &mcpError{
				Code:    -32603,
				Message: fmt.Sprintf("RPC call failed: %v", err),
			},
		}
	}

	// Check if tool execution failed.
	if !rpcResp.Msg.Success {
		return &mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &mcpError{
				Code:    -32000,
				Message: rpcResp.Msg.Error,
			},
		}
	}

	// Return successful result.
	return &mcpResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": rpcResp.Msg.Result,
				},
			},
		},
	}
}
