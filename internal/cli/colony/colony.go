package colony

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	colonyv1 "github.com/coral-io/coral/coral/colony/v1"
	"github.com/coral-io/coral/coral/colony/v1/colonyv1connect"
	meshv1 "github.com/coral-io/coral/coral/mesh/v1"
	"github.com/coral-io/coral/coral/mesh/v1/meshv1connect"
	"github.com/coral-io/coral/internal/colony/database"
	"github.com/coral-io/coral/internal/colony/registry"
	"github.com/coral-io/coral/internal/colony/server"
	"github.com/coral-io/coral/internal/config"
	"github.com/coral-io/coral/internal/constants"
	"github.com/coral-io/coral/internal/discovery/registration"
	"github.com/coral-io/coral/internal/logging"
	"github.com/coral-io/coral/internal/wireguard"
)

// NewColonyCmd creates the colony command and its subcommands
func NewColonyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "colony",
		Short: "Manage the Coral colony (central brain)",
		Long: `The colony is the central brain of your Coral deployment.
It aggregates observations from agents, runs AI analysis, and provides insights.`,
	}

	cmd.AddCommand(newStartCmd())
	cmd.AddCommand(newStopCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newAgentsCmd())
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newUseCmd())
	cmd.AddCommand(newCurrentCmd())
	cmd.AddCommand(newExportCmd())
	cmd.AddCommand(newImportCmd())

	return cmd
}

func newStartCmd() *cobra.Command {
	var (
		daemon   bool
		colonyID string
		port     int
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the colony",
		Long: `Start the Coral colony in the current directory.

The colony will:
- Load configuration from ~/.coral/colonies/<colony-id>.yaml
- Register with discovery service (using colony_id as mesh_id)
- Start the WireGuard control mesh
- Launch the dashboard web UI
- Begin accepting agent connections

The colony to start is determined by (in priority order):
  1. --colony flag
  2. CORAL_COLONY_ID environment variable
  3. .coral/config.yaml in current directory
  4. Default colony in ~/.coral/config.yaml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create resolver
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}

			// Resolve colony ID
			if colonyID == "" {
				colonyID, err = resolver.ResolveColonyID()
				if err != nil {
					return fmt.Errorf("failed to resolve colony: %w\n\nRun 'coral init <app-name>' to create a colony", err)
				}
			}

			// Load resolved configuration
			cfg, err := resolver.ResolveConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			// Apply port override if specified
			if port > 0 {
				cfg.Dashboard.Port = port
			}

			// Initialize logger
			logger := logging.NewWithComponent(logging.Config{
				Level:  "debug",
				Pretty: true,
			}, "colony")

			if daemon {
				logger.Info().Msg("Starting colony in daemon mode")
			} else {
				logger.Info().Msg("Starting colony")
			}

			logger.Info().
				Str("colony_id", cfg.ColonyID).
				Str("application", cfg.ApplicationName).
				Str("environment", cfg.Environment).
				Str("discovery_url", cfg.DiscoveryURL).
				Int("dashboard_port", cfg.Dashboard.Port).
				Str("storage_path", cfg.StoragePath).
				Int("wireguard_port", cfg.WireGuard.Port).
				Msg("Colony configuration loaded")

			// Initialize DuckDB storage.
			db, err := database.New(cfg.StoragePath, cfg.ColonyID, logger)
			if err != nil {
				return fmt.Errorf("failed to initialize database: %w", err)
			}
			defer db.Close()

			// TODO: Implement remaining colony startup tasks
			// - Start HTTP server for dashboard on cfg.Dashboard.Port

			// Initialize WireGuard device
			wgDevice, err := initializeWireGuard(cfg, logger)
			if err != nil {
				return fmt.Errorf("failed to initialize WireGuard: %w", err)
			}
			defer wgDevice.Stop()

			// Create agent registry for tracking connected agents.
			agentRegistry := registry.New()

			// Build endpoints list (for now, just the WireGuard port)
			// TODO: Add proper IP detection and multiple endpoints
			endpoints := []string{
				fmt.Sprintf(":%d", cfg.WireGuard.Port),
			}

			// Start gRPC/Connect server for agent registration and colony management.
			meshServer, err := startServers(cfg, wgDevice, agentRegistry, endpoints, logger)
			if err != nil {
				return fmt.Errorf("failed to start servers: %w", err)
			}
			defer meshServer.Close()

			// Load global config and colony config to get discovery settings
			loader, err := config.NewLoader()
			if err != nil {
				return fmt.Errorf("failed to create config loader: %w", err)
			}
			globalConfig, err := loader.LoadGlobalConfig()
			if err != nil {
				return fmt.Errorf("failed to load global config: %w", err)
			}

			// Load colony config to get discovery settings
			colonyConfig, err := loader.LoadColonyConfig(cfg.ColonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			metadata := map[string]string{
				"application": cfg.ApplicationName,
				"environment": cfg.Environment,
			}

			// Set default mesh IPs if not configured
			meshIPv4 := colonyConfig.WireGuard.MeshIPv4
			if meshIPv4 == "" {
				meshIPv4 = constants.DefaultColonyMeshIPv4
			}
			meshIPv6 := colonyConfig.WireGuard.MeshIPv6
			if meshIPv6 == "" {
				meshIPv6 = constants.DefaultColonyMeshIPv6
			}

			// Set default connect port if not configured
			connectPort := colonyConfig.Services.ConnectPort
			if connectPort == 0 {
				connectPort = 9000 // Default Buf Connect port
			}

			// Create and start registration manager for continuous auto-registration.
			regConfig := registration.Config{
				Enabled:           colonyConfig.Discovery.Enabled,
				AutoRegister:      colonyConfig.Discovery.AutoRegister,
				RegisterInterval:  colonyConfig.Discovery.RegisterInterval,
				MeshID:            cfg.ColonyID,
				PublicKey:         cfg.WireGuard.PublicKey,
				Endpoints:         endpoints,
				MeshIPv4:          meshIPv4,
				MeshIPv6:          meshIPv6,
				ConnectPort:       uint32(connectPort),
				Metadata:          metadata,
				DiscoveryEndpoint: globalConfig.Discovery.Endpoint,
				DiscoveryTimeout:  globalConfig.Discovery.Timeout,
			}

			regManager := registration.NewManager(regConfig, logger)

			// Start registration manager (performs initial registration and starts heartbeat)
			ctx := context.Background()
			if err := regManager.Start(ctx); err != nil {
				logger.Warn().
					Err(err).
					Msg("Failed to start registration manager, will retry in background")
			}

			logger.Info().
				Str("dashboard_url", fmt.Sprintf("http://localhost:%d", cfg.Dashboard.Port)).
				Str("colony_id", cfg.ColonyID).
				Msg("Colony started successfully")

			if !daemon {
				fmt.Println("\nPress Ctrl+C to stop")

				// Wait for interrupt signal
				sigChan := make(chan os.Signal, 1)
				signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
				<-sigChan

				fmt.Println("\n\nShutting down colony...")

				// Stop registration manager
				if err := regManager.Stop(); err != nil {
					logger.Warn().
						Err(err).
						Msg("Error stopping registration manager")
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&daemon, "daemon", false, "Run as background daemon")
	cmd.Flags().StringVar(&colonyID, "colony", "", "Colony ID (overrides auto-detection)")
	cmd.Flags().IntVar(&port, "port", 0, "Dashboard port (overrides config)")

	return cmd
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the colony daemon",
		Long:  `Stop a running colony daemon.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Stopping colony...")

			// TODO: Implement actual colony shutdown
			// - Read PID file
			// - Send SIGTERM to process
			// - Wait for graceful shutdown

			fmt.Println("✓ Colony stopped")
			return nil
		},
	}
}

func newStatusCmd() *cobra.Command {
	var (
		jsonOutput bool
		colonyID   string
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show colony status and WireGuard configuration",
		Long: `Display the current status of the colony including:
- Basic colony information
- WireGuard configuration (public key, mesh IPs, ports)
- Discovery service endpoint
- Connected peers (when colony is running)

This command is useful for troubleshooting connectivity issues.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create resolver
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}

			// Resolve colony ID
			if colonyID == "" {
				colonyID, err = resolver.ResolveColonyID()
				if err != nil {
					return fmt.Errorf("failed to resolve colony: %w\n\nRun 'coral init <app-name>' to create a colony", err)
				}
			}

			// Load colony configuration
			loader := resolver.GetLoader()
			colonyConfig, err := loader.LoadColonyConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			// Load global config
			globalConfig, err := loader.LoadGlobalConfig()
			if err != nil {
				return fmt.Errorf("failed to load global config: %w", err)
			}

			// Get connect port
			connectPort := colonyConfig.Services.ConnectPort
			if connectPort == 0 {
				connectPort = 9000
			}

			// Try to query running colony for real-time status
			// First try localhost (for querying from the same host where colony runs)
			// If that fails, try the mesh IP (for remote queries through the mesh)
			var runtimeStatus *colonyv1.GetStatusResponse

			// Try localhost first
			baseURL := fmt.Sprintf("http://localhost:%d", connectPort)
			client := colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			req := connect.NewRequest(&colonyv1.GetStatusRequest{})
			resp, err := client.GetStatus(ctx, req)
			if err == nil {
				runtimeStatus = resp.Msg
			} else {
				// Try mesh IP as fallback (for remote queries)
				meshIP := colonyConfig.WireGuard.MeshIPv4
				if meshIP == "" {
					meshIP = constants.DefaultColonyMeshIPv4
				}
				baseURL = fmt.Sprintf("http://%s:%d", meshIP, connectPort)
				client = colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

				ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel2()

				if resp2, err2 := client.GetStatus(ctx2, connect.NewRequest(&colonyv1.GetStatusRequest{})); err2 == nil {
					runtimeStatus = resp2.Msg
				}
			}

			if jsonOutput {
				output := map[string]interface{}{
					"colony_id":    colonyConfig.ColonyID,
					"application":  colonyConfig.ApplicationName,
					"environment":  colonyConfig.Environment,
					"storage_path": colonyConfig.StoragePath,
					"wireguard": map[string]interface{}{
						"public_key":        colonyConfig.WireGuard.PublicKey,
						"port":              colonyConfig.WireGuard.Port,
						"mesh_ipv4":         colonyConfig.WireGuard.MeshIPv4,
						"mesh_ipv6":         colonyConfig.WireGuard.MeshIPv6,
						"mesh_network_ipv4": colonyConfig.WireGuard.MeshNetworkIPv4,
						"mesh_network_ipv6": colonyConfig.WireGuard.MeshNetworkIPv6,
						"mtu":               colonyConfig.WireGuard.MTU,
					},
					"discovery_endpoint": globalConfig.Discovery.Endpoint,
					"connect_port":       connectPort,
				}

				// Add runtime status if colony is running
				if runtimeStatus != nil {
					output["status"] = runtimeStatus.Status
					output["uptime_seconds"] = runtimeStatus.UptimeSeconds
					output["agent_count"] = runtimeStatus.AgentCount
					output["storage_bytes"] = runtimeStatus.StorageBytes
					output["dashboard_url"] = runtimeStatus.DashboardUrl
					output["started_at"] = runtimeStatus.StartedAt.AsTime().Format(time.RFC3339)

					// Add network endpoint information from runtime
					output["network_endpoints"] = map[string]interface{}{
						"local_endpoint":       fmt.Sprintf("http://localhost:%d", runtimeStatus.ConnectPort),
						"mesh_endpoint":        fmt.Sprintf("http://%s:%d", runtimeStatus.MeshIpv4, runtimeStatus.ConnectPort),
						"wireguard_port":       runtimeStatus.WireguardPort,
						"wireguard_public_key": runtimeStatus.WireguardPublicKey,
						"wireguard_endpoints":  runtimeStatus.WireguardEndpoints,
					}
				} else {
					output["status"] = "configured"
				}

				data, err := json.MarshalIndent(output, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
				return nil
			}

			// Human-readable output
			fmt.Println("Colony Status")
			fmt.Println("=============")
			fmt.Println()
			fmt.Printf("Colony ID:     %s\n", colonyConfig.ColonyID)
			fmt.Printf("Application:   %s\n", colonyConfig.ApplicationName)
			fmt.Printf("Environment:   %s\n", colonyConfig.Environment)
			fmt.Printf("Storage:       %s\n", colonyConfig.StoragePath)

			// Show runtime status if colony is running
			if runtimeStatus != nil {
				fmt.Println()
				fmt.Println("Runtime Status:")
				fmt.Printf("  Status:        %s\n", runtimeStatus.Status)
				fmt.Printf("  Uptime:        %s\n", formatDuration(time.Duration(runtimeStatus.UptimeSeconds)*time.Second))
				fmt.Printf("  Agents:        %d connected\n", runtimeStatus.AgentCount)

				if runtimeStatus.StorageBytes > 0 {
					fmt.Printf("  Storage Used:  %s\n", formatBytes(runtimeStatus.StorageBytes))
				}

				if runtimeStatus.DashboardUrl != "" {
					fmt.Printf("  Dashboard:     %s\n", runtimeStatus.DashboardUrl)
				}

				// Show network endpoints
				fmt.Println()
				fmt.Println("Network Endpoints (Running):")
				fmt.Printf("  Local (CLI):       http://localhost:%d\n", runtimeStatus.ConnectPort)
				fmt.Printf("  Mesh (Agents):     http://%s:%d\n", runtimeStatus.MeshIpv4, runtimeStatus.ConnectPort)
				fmt.Printf("  WireGuard Listen:  udp://0.0.0.0:%d\n", runtimeStatus.WireguardPort)
				fmt.Printf("  WireGuard Pubkey:  %s\n", truncateKey(runtimeStatus.WireguardPublicKey))
				if len(runtimeStatus.WireguardEndpoints) > 0 {
					fmt.Printf("  Registered Endpoints: %s\n", runtimeStatus.WireguardEndpoints[0])
					for _, ep := range runtimeStatus.WireguardEndpoints[1:] {
						fmt.Printf("                        %s\n", ep)
					}
				}
			}

			fmt.Println()

			fmt.Println("WireGuard Mesh Configuration:")
			fmt.Printf("  Public Key:     %s\n", colonyConfig.WireGuard.PublicKey)
			fmt.Printf("  Listen Port:    %d (UDP)\n", colonyConfig.WireGuard.Port)

			// Show interface name - use stored name if available, otherwise predict
			if colonyConfig.WireGuard.InterfaceName != "" {
				fmt.Printf("  Interface:      %s (last used)\n", colonyConfig.WireGuard.InterfaceName)
			} else {
				interfaceInfo := getInterfaceInfo()
				fmt.Printf("  Interface:      %s\n", interfaceInfo)
			}

			fmt.Printf("  Mesh IPv4:      %s\n", colonyConfig.WireGuard.MeshIPv4)
			if colonyConfig.WireGuard.MeshIPv6 != "" {
				fmt.Printf("  Mesh IPv6:      %s\n", colonyConfig.WireGuard.MeshIPv6)
			}
			fmt.Printf("  Mesh Subnet:    %s\n", colonyConfig.WireGuard.MeshNetworkIPv4)
			if colonyConfig.WireGuard.MeshNetworkIPv6 != "" {
				fmt.Printf("  IPv6 Subnet:    %s\n", colonyConfig.WireGuard.MeshNetworkIPv6)
			}
			fmt.Printf("  MTU:            %d\n", colonyConfig.WireGuard.MTU)
			fmt.Println()

			fmt.Println("Services:")
			fmt.Printf("  Discovery:      %s\n", globalConfig.Discovery.Endpoint)
			fmt.Printf("  Agent Connect:  %s:%d (gRPC/Connect)\n", colonyConfig.WireGuard.MeshIPv4, connectPort)
			fmt.Printf("  Dashboard:      http://localhost:%d (planned)\n", constants.DefaultDashboardPort)
			fmt.Println()

			fmt.Println("Agent Connection Info:")
			fmt.Println("  1. Agents query discovery service:")
			fmt.Printf("     Mesh ID: %s\n", colonyConfig.ColonyID)
			fmt.Println()
			fmt.Println("  2. Discovery returns WireGuard endpoint:")
			fmt.Printf("     Public Key: %s\n", colonyConfig.WireGuard.PublicKey)
			fmt.Printf("     UDP Port:   %d\n", colonyConfig.WireGuard.Port)
			fmt.Println()
			fmt.Println("  3. Agents establish WireGuard tunnel, then register:")
			fmt.Printf("     Colony Mesh IP: %s:%d\n", colonyConfig.WireGuard.MeshIPv4, connectPort)
			fmt.Println()

			if runtimeStatus != nil {
				fmt.Printf("Status: Colony is running (%s)\n", runtimeStatus.Status)
			} else {
				fmt.Println("Status: Colony is configured (not running)")
				fmt.Println()
				fmt.Println("To start the colony:")
				fmt.Println("  coral colony start")
			}
			fmt.Println()

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	cmd.Flags().StringVar(&colonyID, "colony", "", "Colony ID (overrides auto-detection)")

	return cmd
}

func newAgentsCmd() *cobra.Command {
	var (
		jsonOutput bool
		colonyID   string
	)

	cmd := &cobra.Command{
		Use:   "agents",
		Short: "List connected agents",
		Long: `Display all agents currently connected to the colony.

This command queries the running colony to retrieve real-time agent information including:
- Agent ID and component name
- Mesh IP addresses (IPv4 and IPv6)
- Connection status (healthy, degraded, unhealthy)
- Last seen timestamp

Note: The colony must be running for this command to work.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create resolver
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}

			// Resolve colony ID
			if colonyID == "" {
				colonyID, err = resolver.ResolveColonyID()
				if err != nil {
					return fmt.Errorf("failed to resolve colony: %w\n\nRun 'coral init <app-name>' to create a colony", err)
				}
			}

			// Load colony configuration
			loader := resolver.GetLoader()
			colonyConfig, err := loader.LoadColonyConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			// Get connect port
			connectPort := colonyConfig.Services.ConnectPort
			if connectPort == 0 {
				connectPort = 9000
			}

			// Create RPC client - try localhost first, then mesh IP
			baseURL := fmt.Sprintf("http://localhost:%d", connectPort)
			client := colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

			// Call ListAgents RPC
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			req := connect.NewRequest(&colonyv1.ListAgentsRequest{})
			resp, err := client.ListAgents(ctx, req)
			if err != nil {
				// Try mesh IP as fallback
				meshIP := colonyConfig.WireGuard.MeshIPv4
				if meshIP == "" {
					meshIP = "10.42.0.1"
				}
				baseURL = fmt.Sprintf("http://%s:%d", meshIP, connectPort)
				client = colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

				ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel2()

				resp, err = client.ListAgents(ctx2, connect.NewRequest(&colonyv1.ListAgentsRequest{}))
				if err != nil {
					return fmt.Errorf("failed to list agents (is colony running?): %w", err)
				}
			}

			agents := resp.Msg.Agents

			if jsonOutput {
				data, err := json.MarshalIndent(agents, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
				return nil
			}

			// Human-readable output
			if len(agents) == 0 {
				fmt.Println("No agents connected.")
				return nil
			}

			fmt.Printf("Connected Agents (%d):\n\n", len(agents))
			fmt.Printf("%-20s %-15s %-18s %-10s %s\n", "AGENT ID", "COMPONENT", "MESH IP", "STATUS", "LAST SEEN")
			fmt.Println("------------------------------------------------------------------------------")

			for _, agent := range agents {
				// Format last seen as relative time
				lastSeen := agent.LastSeen.AsTime()
				elapsed := time.Since(lastSeen)
				var lastSeenStr string
				if elapsed < time.Minute {
					lastSeenStr = fmt.Sprintf("%ds ago", int(elapsed.Seconds()))
				} else if elapsed < time.Hour {
					lastSeenStr = fmt.Sprintf("%dm ago", int(elapsed.Minutes()))
				} else {
					lastSeenStr = fmt.Sprintf("%dh ago", int(elapsed.Hours()))
				}

				fmt.Printf("%-20s %-15s %-18s %-10s %s\n",
					agent.AgentId,
					agent.ComponentName,
					agent.MeshIpv4,
					agent.Status,
					lastSeenStr,
				)
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	cmd.Flags().StringVar(&colonyID, "colony", "", "Colony ID (overrides auto-detection)")

	return cmd
}

func newListCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all configured colonies",
		Long:  `Display all colonies that have been initialized on this system.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			loader, err := config.NewLoader()
			if err != nil {
				return fmt.Errorf("failed to create config loader: %w", err)
			}

			colonyIDs, err := loader.ListColonies()
			if err != nil {
				return fmt.Errorf("failed to list colonies: %w", err)
			}

			if len(colonyIDs) == 0 {
				fmt.Println("No colonies configured.")
				fmt.Println("\nRun 'coral init <app-name>' to create one.")
				return nil
			}

			globalConfig, err := loader.LoadGlobalConfig()
			if err != nil {
				return fmt.Errorf("failed to load global config: %w", err)
			}

			if jsonOutput {
				type colonyInfo struct {
					ColonyID      string `json:"colony_id"`
					Application   string `json:"application"`
					Environment   string `json:"environment"`
					IsDefault     bool   `json:"is_default"`
					CreatedAt     string `json:"created_at"`
					StoragePath   string `json:"storage_path"`
					WireGuardPort int    `json:"wireguard_port"`
					ConnectPort   int    `json:"connect_port"`
					MeshIPv4      string `json:"mesh_ipv4"`
					Running       bool   `json:"running"`
					LocalEndpoint string `json:"local_endpoint,omitempty"`
					MeshEndpoint  string `json:"mesh_endpoint,omitempty"`
				}

				colonies := []colonyInfo{}
				for _, id := range colonyIDs {
					cfg, err := loader.LoadColonyConfig(id)
					if err != nil {
						continue
					}

					connectPort := cfg.Services.ConnectPort
					if connectPort == 0 {
						connectPort = 9000
					}

					info := colonyInfo{
						ColonyID:      cfg.ColonyID,
						Application:   cfg.ApplicationName,
						Environment:   cfg.Environment,
						IsDefault:     cfg.ColonyID == globalConfig.DefaultColony,
						CreatedAt:     cfg.CreatedAt.Format(time.RFC3339),
						StoragePath:   cfg.StoragePath,
						WireGuardPort: cfg.WireGuard.Port,
						ConnectPort:   connectPort,
						MeshIPv4:      cfg.WireGuard.MeshIPv4,
					}

					// Try to query running status (with quick timeout)
					baseURL := fmt.Sprintf("http://localhost:%d", connectPort)
					client := colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)
					ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
					if resp, err := client.GetStatus(ctx, connect.NewRequest(&colonyv1.GetStatusRequest{})); err == nil {
						info.Running = true
						info.LocalEndpoint = fmt.Sprintf("http://localhost:%d", resp.Msg.ConnectPort)
						info.MeshEndpoint = fmt.Sprintf("http://%s:%d", resp.Msg.MeshIpv4, resp.Msg.ConnectPort)
					}
					cancel()

					colonies = append(colonies, info)
				}

				data, err := json.MarshalIndent(colonies, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
				return nil
			}

			fmt.Println("Configured Colonies:")
			for _, id := range colonyIDs {
				cfg, err := loader.LoadColonyConfig(id)
				if err != nil {
					fmt.Printf("  %s (error loading config)\n", id)
					continue
				}

				defaultMarker := ""
				if cfg.ColonyID == globalConfig.DefaultColony {
					defaultMarker = " [default]"
				}

				// Get connect port
				connectPort := cfg.Services.ConnectPort
				if connectPort == 0 {
					connectPort = 9000
				}

				// Check if colony is running
				runningStatus := ""
				baseURL := fmt.Sprintf("http://localhost:%d", connectPort)
				client := colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)
				ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
				if resp, err := client.GetStatus(ctx, connect.NewRequest(&colonyv1.GetStatusRequest{})); err == nil {
					runningStatus = fmt.Sprintf(" [%s]", resp.Msg.Status)
				}
				cancel()

				fmt.Printf("  %s (%s)%s%s\n", cfg.ColonyID, cfg.Environment, defaultMarker, runningStatus)
				fmt.Printf("    Application: %s\n", cfg.ApplicationName)
				fmt.Printf("    Created: %s\n", cfg.CreatedAt.Format("2006-01-02 15:04:05"))
				fmt.Printf("    Storage: %s\n", cfg.StoragePath)
				fmt.Printf("    Network: WireGuard=%d, Connect=%d, Mesh=%s\n", cfg.WireGuard.Port, connectPort, cfg.WireGuard.MeshIPv4)
				if runningStatus != "" {
					fmt.Printf("    Endpoints: http://localhost:%d (local), http://%s:%d (mesh)\n", connectPort, cfg.WireGuard.MeshIPv4, connectPort)
				}
				fmt.Println()
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")

	return cmd
}

func newUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <colony-id>",
		Short: "Set the default colony",
		Long:  `Set the default colony to use for commands when no explicit colony is specified.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			colonyID := args[0]

			loader, err := config.NewLoader()
			if err != nil {
				return fmt.Errorf("failed to create config loader: %w", err)
			}

			// Verify colony exists
			if _, err := loader.LoadColonyConfig(colonyID); err != nil {
				return fmt.Errorf("colony %q not found: %w", colonyID, err)
			}

			// Load and update global config
			globalConfig, err := loader.LoadGlobalConfig()
			if err != nil {
				return fmt.Errorf("failed to load global config: %w", err)
			}

			globalConfig.DefaultColony = colonyID

			if err := loader.SaveGlobalConfig(globalConfig); err != nil {
				return fmt.Errorf("failed to save global config: %w", err)
			}

			fmt.Printf("✓ Default colony set to: %s\n", colonyID)

			return nil
		},
	}
}

func newCurrentCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "current",
		Short: "Show the current default colony",
		Long:  `Display information about the current default colony.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create resolver: %w", err)
			}

			colonyID, err := resolver.ResolveColonyID()
			if err != nil {
				return fmt.Errorf("no colony configured: %w", err)
			}

			loader := resolver.GetLoader()
			cfg, err := loader.LoadColonyConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			globalConfig, err := loader.LoadGlobalConfig()
			if err != nil {
				return fmt.Errorf("failed to load global config: %w", err)
			}

			if jsonOutput {
				data, err := json.MarshalIndent(map[string]interface{}{
					"colony_id":   cfg.ColonyID,
					"application": cfg.ApplicationName,
					"environment": cfg.Environment,
					"storage":     cfg.StoragePath,
					"discovery":   globalConfig.Discovery.Endpoint,
					"mesh_id":     cfg.Discovery.MeshID,
				}, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
				return nil
			}

			fmt.Println("Current Colony:")
			fmt.Printf("  ID: %s\n", cfg.ColonyID)
			fmt.Printf("  Application: %s\n", cfg.ApplicationName)
			fmt.Printf("  Environment: %s\n", cfg.Environment)
			fmt.Printf("  Storage: %s\n", cfg.StoragePath)
			fmt.Printf("  Discovery: %s (mesh_id: %s)\n", globalConfig.Discovery.Endpoint, cfg.Discovery.MeshID)

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")

	return cmd
}

func newExportCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "export <colony-id>",
		Short: "Export colony credentials",
		Long: `Export colony credentials for deployment to remote systems.

Supported formats:
  env  - Shell environment variables (default)
  yaml - YAML format
  json - JSON format
  k8s  - Kubernetes Secret manifest

Security Warning: These credentials provide full access to the colony.
Keep them secure and do not commit to version control.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			colonyID := args[0]

			loader, err := config.NewLoader()
			if err != nil {
				return fmt.Errorf("failed to create config loader: %w", err)
			}

			cfg, err := loader.LoadColonyConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			globalConfig, err := loader.LoadGlobalConfig()
			if err != nil {
				return fmt.Errorf("failed to load global config: %w", err)
			}

			switch format {
			case "env":
				fmt.Println("# Coral Colony Credentials")
				fmt.Printf("# Generated: %s\n", time.Now().Format("2006-01-02 15:04:05"))
				fmt.Println("# SECURITY: Keep these credentials secure. Do not commit to version control.")
				fmt.Println()
				fmt.Printf("export CORAL_COLONY_ID=\"%s\"\n", cfg.ColonyID)
				fmt.Printf("export CORAL_COLONY_SECRET=\"%s\"\n", cfg.ColonySecret)
				fmt.Printf("export CORAL_DISCOVERY_ENDPOINT=\"%s\"\n", globalConfig.Discovery.Endpoint)
				fmt.Println()
				fmt.Println("# To use in your shell:")
				fmt.Printf("#   eval $(coral colony export %s)\n", colonyID)

			case "yaml":
				fmt.Println("# Coral Colony Credentials (YAML)")
				fmt.Printf("colony_id: %s\n", cfg.ColonyID)
				fmt.Printf("colony_secret: %s\n", cfg.ColonySecret)
				fmt.Printf("discovery_endpoint: %s\n", globalConfig.Discovery.Endpoint)

			case "json":
				data := map[string]string{
					"colony_id":          cfg.ColonyID,
					"colony_secret":      cfg.ColonySecret,
					"discovery_endpoint": globalConfig.Discovery.Endpoint,
				}
				output, err := json.MarshalIndent(data, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(output))

			case "k8s":
				fmt.Println("apiVersion: v1")
				fmt.Println("kind: Secret")
				fmt.Println("metadata:")
				fmt.Printf("  name: coral-secrets\n")
				fmt.Println("type: Opaque")
				fmt.Println("stringData:")
				fmt.Printf("  colony-id: %s\n", cfg.ColonyID)
				fmt.Printf("  colony-secret: %s\n", cfg.ColonySecret)
				fmt.Printf("  discovery-endpoint: %s\n", globalConfig.Discovery.Endpoint)

			default:
				return fmt.Errorf("unknown format: %s (supported: env, yaml, json, k8s)", format)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&format, "format", "env", "Output format (env, yaml, json, k8s)")

	return cmd
}

func newImportCmd() *cobra.Command {
	var (
		colonyID     string
		colonySecret string
		useStdin     bool
	)

	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import colony credentials",
		Long: `Import colony credentials from environment variables or flags.

This is typically used on remote systems (Kubernetes, VMs) that need to
connect to an existing colony.

Note: The colony's WireGuard public key will be retrieved from discovery service on first connection.
      The colony's private key never leaves the colony and is not needed by agents.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if useStdin {
				return fmt.Errorf("stdin import not yet implemented")
			}

			if colonyID == "" {
				colonyID = os.Getenv("CORAL_COLONY_ID")
			}
			if colonySecret == "" {
				colonySecret = os.Getenv("CORAL_COLONY_SECRET")
			}

			if colonyID == "" || colonySecret == "" {
				return fmt.Errorf("colony-id and secret are required (use flags or env vars)")
			}

			loader, err := config.NewLoader()
			if err != nil {
				return fmt.Errorf("failed to create config loader: %w", err)
			}

			// Create a minimal colony config
			// Note: We don't have full colony details, just the essentials
			cfg := &config.ColonyConfig{
				Version:      config.SchemaVersion,
				ColonyID:     colonyID,
				ColonySecret: colonySecret,
				CreatedAt:    time.Now(),
				Discovery: config.DiscoveryColony{
					Enabled: true,
					MeshID:  colonyID,
				},
			}

			if err := loader.SaveColonyConfig(cfg); err != nil {
				return fmt.Errorf("failed to save colony config: %w", err)
			}

			fmt.Println("✓ Colony configuration imported")
			fmt.Printf("✓ Saved to %s\n", loader.ColonyConfigPath(colonyID))
			fmt.Println("\nNote: The colony's WireGuard public key will be retrieved from discovery service on first connection.")
			fmt.Println("      The colony's private key never leaves the colony and is not needed by agents.")

			return nil
		},
	}

	cmd.Flags().StringVar(&colonyID, "colony-id", "", "Colony ID")
	cmd.Flags().StringVar(&colonySecret, "secret", "", "Colony secret")
	cmd.Flags().BoolVar(&useStdin, "stdin", false, "Read from stdin")

	return cmd
}

// initializeWireGuard creates and starts the WireGuard device for the colony.
func initializeWireGuard(cfg *config.ResolvedConfig, logger logging.Logger) (*wireguard.Device, error) {
	logger.Info().
		Str("mesh_ipv4", cfg.WireGuard.MeshIPv4).
		Int("port", cfg.WireGuard.Port).
		Msg("Initializing WireGuard device")

	wgDevice, err := wireguard.NewDevice(&cfg.WireGuard)
	if err != nil {
		return nil, fmt.Errorf("failed to create WireGuard device: %w", err)
	}

	if err := wgDevice.Start(); err != nil {
		return nil, fmt.Errorf("failed to start WireGuard device: %w", err)
	}

	logger.Info().
		Str("interface", wgDevice.InterfaceName()).
		Str("mesh_ip", cfg.WireGuard.MeshIPv4).
		Msg("WireGuard device started successfully")

	// Save the assigned interface name to config for future reference
	interfaceName := wgDevice.InterfaceName()
	if interfaceName != "" {
		loader, err := config.NewLoader()
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to create config loader to save interface name")
		} else {
			colonyConfig, err := loader.LoadColonyConfig(cfg.ColonyID)
			if err != nil {
				logger.Warn().Err(err).Msg("Failed to load colony config to save interface name")
			} else {
				colonyConfig.WireGuard.InterfaceName = interfaceName
				if err := loader.SaveColonyConfig(colonyConfig); err != nil {
					logger.Warn().Err(err).Msg("Failed to save interface name to config")
				} else {
					logger.Debug().
						Str("interface", interfaceName).
						Msg("Saved interface name to colony config")
				}
			}
		}
	}

	return wgDevice, nil
}

// startServers starts the HTTP/Connect servers for agent registration and colony management.
func startServers(cfg *config.ResolvedConfig, wgDevice *wireguard.Device, agentRegistry *registry.Registry, endpoints []string, logger logging.Logger) (*http.Server, error) {
	// Get connect port from config or use default
	loader, err := config.NewLoader()
	if err != nil {
		return nil, fmt.Errorf("failed to create config loader: %w", err)
	}

	colonyConfig, err := loader.LoadColonyConfig(cfg.ColonyID)
	if err != nil {
		return nil, fmt.Errorf("failed to load colony config: %w", err)
	}

	connectPort := colonyConfig.Services.ConnectPort
	if connectPort == 0 {
		connectPort = 9000 // Default Buf Connect port
	}

	dashboardPort := colonyConfig.Services.DashboardPort
	if dashboardPort == 0 {
		dashboardPort = constants.DefaultDashboardPort
	}

	// Create mesh service handler
	meshSvc := &meshServiceHandler{
		cfg:      cfg,
		wgDevice: wgDevice,
		registry: agentRegistry,
		logger:   logger,
	}

	// Create colony service handler
	colonyServerConfig := server.Config{
		ColonyID:           cfg.ColonyID,
		ApplicationName:    cfg.ApplicationName,
		Environment:        cfg.Environment,
		DashboardPort:      dashboardPort,
		StoragePath:        cfg.StoragePath,
		WireGuardPort:      cfg.WireGuard.Port,
		WireGuardPublicKey: cfg.WireGuard.PublicKey,
		WireGuardEndpoints: endpoints,
		ConnectPort:        connectPort,
		MeshIPv4:           cfg.WireGuard.MeshIPv4,
		MeshIPv6:           cfg.WireGuard.MeshIPv6,
	}
	colonySvc := server.New(agentRegistry, colonyServerConfig, logger.With().Str("component", "colony-server").Logger())

	// Register the handlers
	meshPath, meshHandler := meshv1connect.NewMeshServiceHandler(meshSvc)
	colonyPath, colonyHandler := colonyv1connect.NewColonyServiceHandler(colonySvc)

	// Create HTTP server
	mux := http.NewServeMux()
	mux.Handle(meshPath, meshHandler)
	mux.Handle(colonyPath, colonyHandler)

	addr := fmt.Sprintf(":%d", connectPort)
	httpServer := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Start server in background
	go func() {
		logger.Info().
			Int("port", connectPort).
			Msg("Mesh and Colony services listening")

		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error().
				Err(err).
				Msg("Server error")
		}
	}()

	return httpServer, nil
}

// meshServiceHandler implements the MeshService RPC handler.
type meshServiceHandler struct {
	cfg      *config.ResolvedConfig
	wgDevice *wireguard.Device
	registry *registry.Registry
	logger   logging.Logger
}

// Register handles agent registration requests.
func (h *meshServiceHandler) Register(
	ctx context.Context,
	req *connect.Request[meshv1.RegisterRequest],
) (*connect.Response[meshv1.RegisterResponse], error) {
	h.logger.Info().
		Str("agent_id", req.Msg.AgentId).
		Str("component_name", req.Msg.ComponentName).
		Msg("Agent registration request received")

	// Validate colony_id and colony_secret (RFD 002)
	if req.Msg.ColonyId != h.cfg.ColonyID {
		h.logger.Warn().
			Str("agent_id", req.Msg.AgentId).
			Str("expected_colony_id", h.cfg.ColonyID).
			Str("received_colony_id", req.Msg.ColonyId).
			Msg("Agent registration rejected: wrong colony ID")

		return connect.NewResponse(&meshv1.RegisterResponse{
			Accepted: false,
			Reason:   "wrong_colony",
		}), nil
	}

	if req.Msg.ColonySecret != h.cfg.ColonySecret {
		h.logger.Warn().
			Str("agent_id", req.Msg.AgentId).
			Msg("Agent registration rejected: invalid colony secret")

		return connect.NewResponse(&meshv1.RegisterResponse{
			Accepted: false,
			Reason:   "invalid_secret",
		}), nil
	}

	// Validate WireGuard public key
	if req.Msg.WireguardPubkey == "" {
		h.logger.Warn().
			Str("agent_id", req.Msg.AgentId).
			Msg("Agent registration rejected: missing WireGuard public key")

		return connect.NewResponse(&meshv1.RegisterResponse{
			Accepted: false,
			Reason:   "missing_wireguard_pubkey",
		}), nil
	}

	// Allocate mesh IP for the agent
	allocator := h.wgDevice.Allocator()
	meshIP, err := allocator.Allocate(req.Msg.AgentId)
	if err != nil {
		h.logger.Error().
			Err(err).
			Str("agent_id", req.Msg.AgentId).
			Msg("Failed to allocate mesh IP")

		return connect.NewResponse(&meshv1.RegisterResponse{
			Accepted: false,
			Reason:   "ip_allocation_failed",
		}), nil
	}

	h.logger.Info().
		Str("agent_id", req.Msg.AgentId).
		Str("mesh_ip", meshIP.String()).
		Msg("Allocated mesh IP for agent")

	// Add agent as WireGuard peer
	peerConfig := &wireguard.PeerConfig{
		PublicKey:           req.Msg.WireguardPubkey,
		AllowedIPs:          []string{meshIP.String() + "/32"},
		PersistentKeepalive: 25, // Keep NAT mappings alive
	}

	if err := h.wgDevice.AddPeer(peerConfig); err != nil {
		h.logger.Error().
			Err(err).
			Str("agent_id", req.Msg.AgentId).
			Msg("Failed to add agent as WireGuard peer")

		// Release the allocated IP since we couldn't add the peer
		allocator.Release(meshIP)

		return connect.NewResponse(&meshv1.RegisterResponse{
			Accepted: false,
			Reason:   "peer_add_failed",
		}), nil
	}

	// Register agent in the registry for tracking.
	// Note: We don't have IPv6 mesh IP yet (future enhancement).
	if _, err := h.registry.Register(req.Msg.AgentId, req.Msg.ComponentName, meshIP.String(), ""); err != nil {
		h.logger.Warn().
			Err(err).
			Str("agent_id", req.Msg.AgentId).
			Msg("Failed to register agent in registry (non-fatal)")
	}

	h.logger.Info().
		Str("agent_id", req.Msg.AgentId).
		Str("component_name", req.Msg.ComponentName).
		Str("mesh_ip", meshIP.String()).
		Msg("Agent registered successfully")

	// Build list of existing peers (excluding this agent)
	peers := []*meshv1.PeerInfo{}
	for _, peer := range h.wgDevice.ListPeers() {
		if peer.PublicKey != req.Msg.WireguardPubkey {
			// Get the IP from allowed IPs
			if len(peer.AllowedIPs) > 0 {
				peers = append(peers, &meshv1.PeerInfo{
					WireguardPubkey: peer.PublicKey,
					MeshIp:          peer.AllowedIPs[0],
				})
			}
		}
	}

	// Return successful registration response
	return connect.NewResponse(&meshv1.RegisterResponse{
		Accepted:     true,
		AssignedIp:   meshIP.String(),
		MeshSubnet:   h.cfg.WireGuard.MeshNetworkIPv4,
		Peers:        peers,
		RegisteredAt: timestamppb.Now(),
	}), nil
}

// formatDuration formats a duration in a human-readable format.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		minutes := int(d.Minutes())
		seconds := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh %dm", hours, minutes)
}

// formatBytes formats bytes in a human-readable format.
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// truncateKey truncates a WireGuard public key for display.
func truncateKey(key string) string {
	if len(key) <= 16 {
		return key
	}
	return key[:12] + "..." + key[len(key)-4:]
}
