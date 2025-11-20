package proxy

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	colonyv1 "github.com/coral-io/coral/coral/colony/v1"
	"github.com/coral-io/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-io/coral/internal/colony/database"
	"github.com/coral-io/coral/internal/colony/mcp"
	"github.com/coral-io/coral/internal/colony/registry"
	"github.com/coral-io/coral/internal/config"
	"github.com/coral-io/coral/internal/logging"
)

// mcpCmd creates the 'coral colony proxy mcp' command.
func mcpCmd() *cobra.Command {
	var colonyID string

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Proxy to colony MCP server",
		Long: `Connect to a running colony's MCP server and proxy stdio.

This command provides an MCP protocol bridge between AI assistants and the colony's
MCP server. It connects to the running colony and forwards MCP protocol
messages over stdio.

Compatible with any MCP client including Claude Desktop, custom LLM applications,
and AI agent frameworks that support the Model Context Protocol.

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
			defer func() { _ = db.Close() }() // TODO: errcheck

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
