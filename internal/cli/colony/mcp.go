package colony

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"connectrpc.com/connect"
	colonyv1 "github.com/coral-io/coral/coral/colony/v1"
	"github.com/coral-io/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-io/coral/internal/colony/database"
	"github.com/coral-io/coral/internal/colony/mcp"
	"github.com/coral-io/coral/internal/colony/registry"
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
  coral colony proxy mcp`,
	}

	cmd.AddCommand(newMCPListToolsCmd())
	cmd.AddCommand(newMCPTestToolCmd())
	cmd.AddCommand(newMCPGenerateConfigCmd())

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

			// TODO: Implement direct tool invocation via RPC or local execution.
			// For now, print placeholder.

			fmt.Println("Response:")
			fmt.Println("  (Tool execution not yet implemented)")
			fmt.Println()
			fmt.Println("Note: This will be implemented to call the tool directly.")

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
					"args":    []string{"colony", "proxy", "mcp"},
				}
			} else {
				// Multiple colonies: use "coral-<colony-id>".
				for _, cid := range colonies {
					serverName := fmt.Sprintf("coral-%s", cid)
					servers[serverName] = map[string]interface{}{
						"command": "coral",
						"args":    []string{"colony", "proxy", "mcp", "--colony", cid},
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

// newProxyMCPCmd creates the 'coral colony proxy mcp' command.
// This is registered separately in proxy.go but defined here for clarity.
func newProxyMCPCmd() *cobra.Command {
	var colonyID string

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Proxy to colony MCP server",
		Long: `Connect to a running colony's MCP server and proxy stdio.

This command is used by Claude Desktop to communicate with the colony's
MCP server. It connects to the running colony and forwards MCP protocol
messages over stdio.

The colony must be running for this command to work.

Examples:
  # Connect to default colony
  coral colony proxy mcp

  # Connect to specific colony
  coral colony proxy mcp --colony my-shop-production`,
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

			// Load colony configuration.
			cfg, err := resolver.ResolveConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			// Initialize logger.
			logger := logging.NewWithComponent(logging.Config{
				Level:  "error", // Only errors for MCP proxy
				Pretty: false,
			}, "mcp-proxy")

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
			client := colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			_, err = client.GetStatus(ctx, connect.NewRequest(&colonyv1.GetStatusRequest{}))
			if err != nil {
				return fmt.Errorf("colony is not running (failed to connect on port %d): %w", connectPort, err)
			}

			// Initialize database connection for MCP server.
			db, err := database.New(cfg.StoragePath, cfg.ColonyID, logger)
			if err != nil {
				return fmt.Errorf("failed to initialize database: %w", err)
			}
			defer db.Close()

			// Create agent registry (empty for proxy mode).
			agentRegistry := registry.New()

			// Create MCP server configuration.
			mcpConfig := mcp.Config{
				ColonyID:              cfg.ColonyID,
				ApplicationName:       cfg.ApplicationName,
				Environment:           cfg.Environment,
				Disabled:              colonyConfig.MCP.Disabled,
				EnabledTools:          colonyConfig.MCP.EnabledTools,
				RequireRBACForActions: colonyConfig.MCP.Security.RequireRBACForActions,
				AuditEnabled:          colonyConfig.MCP.Security.AuditEnabled,
			}

			// Start MCP server on stdio.
			logger.Info().
				Str("colony_id", cfg.ColonyID).
				Msg("Starting MCP server proxy")

			return mcp.StartStdioServer(agentRegistry, db, mcpConfig, logger)
		},
	}

	cmd.Flags().StringVar(&colonyID, "colony", "", "Colony ID (overrides auto-detection)")

	return cmd
}
