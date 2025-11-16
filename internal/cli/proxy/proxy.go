package proxy

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"connectrpc.com/connect"
	colonyv1 "github.com/coral-io/coral/coral/colony/v1"
	"github.com/coral-io/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-io/coral/internal/colony/database"
	"github.com/coral-io/coral/internal/colony/mcp"
	"github.com/coral-io/coral/internal/colony/registry"
	"github.com/coral-io/coral/internal/config"
	"github.com/coral-io/coral/internal/constants"
	"github.com/coral-io/coral/internal/logging"
	"github.com/coral-io/coral/internal/proxy"
	"github.com/spf13/cobra"
)

// Command creates the proxy command with start/stop/status subcommands.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "proxy",
		Short: "Manage local proxy for colony access",
		Long: `The proxy command starts a local HTTP/2 proxy server that forwards
requests to colonies over the WireGuard mesh network. This allows CLI tools
to query colonies without implementing WireGuard logic directly.

Example usage:
  coral proxy start my-app-prod
  coral proxy status
  coral proxy stop`,
	}

	cmd.AddCommand(startCmd())
	cmd.AddCommand(statusCmd())
	cmd.AddCommand(stopCmd())
	cmd.AddCommand(mcpCmd())

	return cmd
}

func startCmd() *cobra.Command {
	var (
		listenAddr        string
		discoveryEndpoint string
	)

	cmd := &cobra.Command{
		Use:   "start <colony-mesh-id>",
		Short: "Start a proxy for a colony",
		Long: `Start a local proxy server that forwards requests to the specified colony.

The proxy will:
1. Query the discovery service to locate the colony
2. Establish a connection to the colony's mesh network
3. Start an HTTP/2 server on localhost to accept CLI requests
4. Forward all requests to the colony over the WireGuard tunnel

Example:
  coral proxy start my-app-prod
  coral proxy start my-app-prod --listen localhost:8001`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			meshID := args[0]

			// Initialize logger.
			logConfig := logging.DefaultConfig()
			logger := logging.NewWithComponent(logConfig, "coral-proxy")

			logger.Info().
				Str("mesh_id", meshID).
				Str("listen_addr", listenAddr).
				Msg("Starting coral proxy")

			// Create context with signal handling.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Handle shutdown signals.
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				sig := <-sigCh
				logger.Info().Str("signal", sig.String()).Msg("Received shutdown signal")
				cancel()
			}()

			// Lookup colony in discovery service.
			logger.Info().
				Str("discovery_endpoint", discoveryEndpoint).
				Str("mesh_id", meshID).
				Msg("Looking up colony")

			colonyInfo, err := proxy.LookupColony(ctx, discoveryEndpoint, meshID, logger)
			if err != nil {
				return fmt.Errorf("failed to lookup colony: %w", err)
			}

			logger.Info().
				Str("mesh_ipv4", colonyInfo.MeshIPv4).
				Str("mesh_ipv6", colonyInfo.MeshIPv6).
				Uint32("connect_port", colonyInfo.ConnectPort).
				Msg("Colony found")

			// Create proxy server config.
			config := proxy.Config{
				ListenAddr:        listenAddr,
				ColonyID:          colonyInfo.MeshID,
				ColonyMeshIPv4:    colonyInfo.MeshIPv4,
				ColonyMeshIPv6:    colonyInfo.MeshIPv6,
				ColonyConnectPort: colonyInfo.ConnectPort,
				Logger:            logger,
			}

			// Create and start proxy server.
			server := proxy.New(config)
			if err := server.Start(ctx); err != nil {
				return fmt.Errorf("failed to start proxy server: %w", err)
			}

			logger.Info().
				Str("listen_addr", listenAddr).
				Str("target", fmt.Sprintf("%s:%d", colonyInfo.MeshIPv4, colonyInfo.ConnectPort)).
				Msg("Proxy server running")

			fmt.Printf("✓ Proxy running on %s\n", listenAddr)
			fmt.Printf("  Forwarding to colony at %s:%d (over mesh)\n", colonyInfo.MeshIPv4, colonyInfo.ConnectPort)
			fmt.Println("\nPress Ctrl+C to stop proxy")

			// Wait for shutdown signal.
			<-ctx.Done()

			// Graceful shutdown.
			logger.Info().Msg("Shutting down proxy server")
			shutdownCtx := context.Background()
			if err := server.Stop(shutdownCtx); err != nil {
				return fmt.Errorf("failed to stop proxy: %w", err)
			}

			fmt.Println("✓ Proxy stopped")
			return nil
		},
	}

	cmd.Flags().StringVar(&listenAddr, "listen", "localhost:8000", "Local address to bind to")
	cmd.Flags().StringVar(&discoveryEndpoint, "discovery", constants.DefaultDiscoveryEndpoint, "Discovery service endpoint")

	return cmd
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show status of running proxies",
		Long:  `Display the status of all running proxy servers.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Proxy status command - not yet implemented")
			fmt.Println("This will show:")
			fmt.Println("  - List of active proxies")
			fmt.Println("  - Colony they're connected to")
			fmt.Println("  - Listen address")
			fmt.Println("  - Uptime")
			return nil
		},
	}
}

func stopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop [colony-mesh-id]",
		Short: "Stop a running proxy",
		Long:  `Stop the proxy server for the specified colony.`,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Proxy stop command - not yet implemented")
			if len(args) > 0 {
				fmt.Printf("Would stop proxy for: %s\n", args[0])
			} else {
				fmt.Println("Would stop all proxies")
			}
			return nil
		},
	}
}
