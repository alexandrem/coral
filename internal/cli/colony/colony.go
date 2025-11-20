package colony

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-io/coral/coral/agent/v1"
	colonyv1 "github.com/coral-io/coral/coral/colony/v1"
	"github.com/coral-io/coral/coral/colony/v1/colonyv1connect"
	discoverypb "github.com/coral-io/coral/coral/discovery/v1"
	"github.com/coral-io/coral/coral/discovery/v1/discoveryv1connect"
	meshv1 "github.com/coral-io/coral/coral/mesh/v1"
	"github.com/coral-io/coral/coral/mesh/v1/meshv1connect"
	"github.com/coral-io/coral/internal/colony"
	"github.com/coral-io/coral/internal/colony/database"
	"github.com/coral-io/coral/internal/colony/mcp"
	"github.com/coral-io/coral/internal/colony/registry"
	"github.com/coral-io/coral/internal/colony/server"
	"github.com/coral-io/coral/internal/config"
	"github.com/coral-io/coral/internal/constants"
	"github.com/coral-io/coral/internal/discovery/registration"
	"github.com/coral-io/coral/internal/logging"
	runtimepkg "github.com/coral-io/coral/internal/runtime"
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
	cmd.AddCommand(newMCPCmd())

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
  4. Default colony in ~/.coral/config.yaml

Environment Variables:
  CORAL_COLONY_ID          - Colony ID to start (overrides config)
  CORAL_DISCOVERY_ENDPOINT - Discovery service URL (default: http://localhost:8080)
  CORAL_PUBLIC_ENDPOINT    - Public WireGuard endpoint(s) for agents to connect
                             Format: hostname:port or ip:port (comma-separated for multiple)
                             Examples:
                               Single:   colony.example.com:41580
                               Multiple: 192.168.5.2:9000,10.0.0.5:9000,colony.example.com:9000
                             Default: 127.0.0.1:<port> (local development only)
                             Production: MUST be set to reachable public IP/hostname
                             Alternative: Configure public_endpoints in colony YAML config
  CORAL_MESH_SUBNET        - WireGuard mesh network subnet (CIDR notation)
                             Default: 100.64.0.0/10 (CGNAT address space, RFC 6598)
                             Examples: 100.64.0.0/10, 10.42.0.0/16, 172.16.0.0/12
                             Use CGNAT (100.64.0.0/10) to avoid conflicts with corporate networks

Production Deployment:
  For agents to connect from different machines, you MUST set CORAL_PUBLIC_ENDPOINT
  to your colony's publicly reachable IP address or hostname.

Examples:
  # Local development (agents on same machine)
  coral colony start

  # Production with public IP
  CORAL_PUBLIC_ENDPOINT=203.0.113.5:41580 coral colony start

  # Production with hostname
  CORAL_PUBLIC_ENDPOINT=colony.example.com:41580 coral colony start`,
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

			// Initialize WireGuard device (but don't start it yet)
			wgDevice, err := createWireGuardDevice(cfg, logger)
			if err != nil {
				return fmt.Errorf("failed to create WireGuard device: %w", err)
			}
			defer wgDevice.Stop()

			// Set up the persistent allocator BEFORE starting the device (RFD 019).
			// This enables IP allocation recovery after colony restarts.
			if err := initializePersistentIPAllocator(wgDevice, db, logger); err != nil {
				logger.Warn().
					Err(err).
					Msg("Failed to initialize persistent IP allocator, using in-memory allocator")
			} else {
				logger.Info().Msg("Persistent IP allocator initialized")
			}

			// Now start the WireGuard device with the persistent allocator configured
			if err := startWireGuardDevice(wgDevice, cfg, logger); err != nil {
				return fmt.Errorf("failed to start WireGuard device: %w", err)
			}

			// Create agent registry for tracking connected agents.
			agentRegistry := registry.New()

			// Build endpoints advertised to discovery using public/reachable addresses.
			// For local development, use empty host (":port") to let agents discover via local network.
			// For production, configure CORAL_PUBLIC_ENDPOINT environment variable or config file.
			//
			// Load colony config to get public endpoints configuration
			colonyConfigForEndpoints, err := resolver.GetLoader().LoadColonyConfig(cfg.ColonyID)
			if err != nil {
				logger.Warn().
					Err(err).
					Msg("Failed to load colony config for endpoints; using environment variable only")
				colonyConfigForEndpoints = nil
			}

			endpoints := buildWireGuardEndpoints(cfg.WireGuard.Port, colonyConfigForEndpoints)
			if len(endpoints) == 0 {
				logger.Warn().Msg("No WireGuard endpoints could be constructed; discovery registration will fail")
			} else {
				logger.Info().
					Strs("wireguard_endpoints", endpoints).
					Int("wireguard_port", cfg.WireGuard.Port).
					Msg("Built WireGuard endpoints for discovery registration")
			}

			// Start gRPC/Connect server for agent registration and colony management.
			meshServer, err := startServers(cfg, wgDevice, agentRegistry, db, endpoints, logger)
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
				"application":    cfg.ApplicationName,
				"environment":    cfg.Environment,
				"wireguard_port": fmt.Sprintf("%d", cfg.WireGuard.Port),
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

			// TODO: Implement STUN discovery before WireGuard initialization.
			// For now, colonies rely on configured endpoints or agents discovering them via STUN.
			// See RFD 029 for planned colony-based STUN enhancement.
			var colonyObservedEndpoint *discoverypb.Endpoint

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
				ObservedEndpoint:  colonyObservedEndpoint, // Add STUN-discovered endpoint
			}

			regManager := registration.NewManager(regConfig, logger)

			// Start registration manager (performs initial registration and starts heartbeat)
			ctx := context.Background()
			if err := regManager.Start(ctx); err != nil {
				logger.Warn().
					Err(err).
					Msg("Failed to start registration manager, will retry in background")
			}

			// Create and start telemetry poller for RFD 025 pull-based telemetry.
			// Polls agents every 1 minute for recent telemetry data.
			// Default retention: 24 hours for telemetry summaries.
			telemetryPoller := colony.NewTelemetryPoller(
				agentRegistry,
				db,
				1*time.Minute, // Poll interval
				24,            // Retention in hours (default: 24)
				logger,
			)

			if err := telemetryPoller.Start(); err != nil {
				logger.Warn().
					Err(err).
					Msg("Failed to start telemetry poller")
			} else {
				logger.Info().Msg("Telemetry poller started - will query agents every minute")
			}

			// Create and start Beyla metrics poller for RFD 032.
			// Read Beyla configuration from colony config, with sensible defaults.
			pollIntervalSecs := 60 // Default: poll every 60 seconds
			httpRetentionDays := 30
			grpcRetentionDays := 30
			sqlRetentionDays := 14
			traceRetentionDays := 7

			if colonyConfigForEndpoints != nil && colonyConfigForEndpoints.Beyla.PollInterval > 0 {
				pollIntervalSecs = colonyConfigForEndpoints.Beyla.PollInterval
			}
			if colonyConfigForEndpoints != nil && colonyConfigForEndpoints.Beyla.Retention.HTTPDays > 0 {
				httpRetentionDays = colonyConfigForEndpoints.Beyla.Retention.HTTPDays
			}
			if colonyConfigForEndpoints != nil && colonyConfigForEndpoints.Beyla.Retention.GRPCDays > 0 {
				grpcRetentionDays = colonyConfigForEndpoints.Beyla.Retention.GRPCDays
			}
			if colonyConfigForEndpoints != nil && colonyConfigForEndpoints.Beyla.Retention.SQLDays > 0 {
				sqlRetentionDays = colonyConfigForEndpoints.Beyla.Retention.SQLDays
			}
			if colonyConfigForEndpoints != nil && colonyConfigForEndpoints.Beyla.Retention.TracesDays > 0 {
				traceRetentionDays = colonyConfigForEndpoints.Beyla.Retention.TracesDays
			}

			beylaPoller := colony.NewBeylaPoller(
				agentRegistry,
				db,
				time.Duration(pollIntervalSecs)*time.Second,
				httpRetentionDays,
				grpcRetentionDays,
				sqlRetentionDays,
				traceRetentionDays,
				logger,
			)

			if err := beylaPoller.Start(); err != nil {
				logger.Warn().
					Err(err).
					Msg("Failed to start Beyla metrics poller")
			} else {
				logger.Info().
					Int("poll_interval_secs", pollIntervalSecs).
					Int("http_retention_days", httpRetentionDays).
					Int("grpc_retention_days", grpcRetentionDays).
					Int("sql_retention_days", sqlRetentionDays).
					Int("trace_retention_days", traceRetentionDays).
					Msg("Beyla metrics poller started")
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

				// Stop telemetry poller
				if err := telemetryPoller.Stop(); err != nil {
					logger.Warn().
						Err(err).
						Msg("Error stopping telemetry poller")
				}

				// Stop Beyla metrics poller
				if err := beylaPoller.Stop(); err != nil {
					logger.Warn().
						Err(err).
						Msg("Error stopping Beyla metrics poller")
				}

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
		verbose    bool
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
- Runtime context (with --verbose)

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

			if verbose {
				return outputAgentsVerbose(agents)
			}

			fmt.Printf("Connected Agents (%d):\n\n", len(agents))
			fmt.Printf("%-25s %-20s %-20s %-10s %-10s %s\n", "AGENT ID", "SERVICES", "RUNTIME", "MESH IP", "STATUS", "LAST SEEN")
			fmt.Println("--------------------------------------------------------------------------------------------------------")

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

				// Format runtime type
				runtimeStr := "-"
				if agent.RuntimeContext != nil {
					runtimeStr = formatRuntimeTypeShort(agent.RuntimeContext.RuntimeType)
					if agent.RuntimeContext.SidecarMode != 0 && agent.RuntimeContext.SidecarMode != 1 {
						runtimeStr += fmt.Sprintf(" (%s)", formatSidecarModeShort(agent.RuntimeContext.SidecarMode))
					}
				}

				// Format services list (RFD 044: use Services array, not ComponentName).
				servicesStr := formatServicesList(agent.Services)
				if servicesStr == "" {
					servicesStr = agent.ComponentName // Fallback for backward compatibility
				}

				fmt.Printf("%-25s %-20s %-20s %-10s %-10s %s\n",
					truncate(agent.AgentId, 25),
					truncate(servicesStr, 20),
					truncate(runtimeStr, 20),
					agent.MeshIpv4,
					agent.Status,
					lastSeenStr,
				)
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show detailed runtime context for each agent")
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

// createWireGuardDevice creates a WireGuard device but doesn't start it yet.
// This allows the persistent IP allocator to be configured before the device starts.
func createWireGuardDevice(cfg *config.ResolvedConfig, logger logging.Logger) (*wireguard.Device, error) {
	logger.Info().
		Str("mesh_ipv4", cfg.WireGuard.MeshIPv4).
		Int("port", cfg.WireGuard.Port).
		Msg("Creating WireGuard device")

	wgDevice, err := wireguard.NewDevice(&cfg.WireGuard, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create WireGuard device: %w", err)
	}

	return wgDevice, nil
}

// startWireGuardDevice starts the WireGuard device and assigns the mesh IP.
func startWireGuardDevice(wgDevice *wireguard.Device, cfg *config.ResolvedConfig, logger logging.Logger) error {
	if err := wgDevice.Start(); err != nil {
		return fmt.Errorf("failed to start WireGuard device: %w", err)
	}

	logger.Info().
		Str("interface", wgDevice.InterfaceName()).
		Str("mesh_ip", cfg.WireGuard.MeshIPv4).
		Msg("WireGuard device started successfully")

	// Assign mesh IP to the interface
	if cfg.WireGuard.MeshIPv4 != "" && cfg.WireGuard.MeshNetworkIPv4 != "" {
		meshIP := net.ParseIP(cfg.WireGuard.MeshIPv4)
		if meshIP == nil {
			return fmt.Errorf("invalid mesh IPv4 address: %s", cfg.WireGuard.MeshIPv4)
		}

		_, meshNet, err := net.ParseCIDR(cfg.WireGuard.MeshNetworkIPv4)
		if err != nil {
			return fmt.Errorf("invalid mesh network CIDR: %w", err)
		}

		logger.Info().
			Str("interface", wgDevice.InterfaceName()).
			Str("ip", meshIP.String()).
			Str("subnet", meshNet.String()).
			Msg("Assigning IP address to WireGuard interface")

		iface := wgDevice.Interface()
		if iface == nil {
			return fmt.Errorf("WireGuard device has no interface")
		}

		if err := iface.AssignIP(meshIP, meshNet); err != nil {
			return fmt.Errorf("failed to assign IP to interface: %w", err)
		}

		logger.Info().
			Str("interface", wgDevice.InterfaceName()).
			Str("ip", meshIP.String()).
			Msg("Successfully assigned IP to WireGuard interface")
	}

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

	return nil
}

// startServers starts the HTTP/Connect servers for agent registration and colony management.
func startServers(cfg *config.ResolvedConfig, wgDevice *wireguard.Device, agentRegistry *registry.Registry, db *database.Database, endpoints []string, logger logging.Logger) (*http.Server, error) {
	// Get connect port from config or use default
	loader, err := config.NewLoader()
	if err != nil {
		return nil, fmt.Errorf("failed to create config loader: %w", err)
	}

	colonyConfig, err := loader.LoadColonyConfig(cfg.ColonyID)
	if err != nil {
		return nil, fmt.Errorf("failed to load colony config: %w", err)
	}

	globalConfig, err := loader.LoadGlobalConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load global config: %w", err)
	}

	connectPort := colonyConfig.Services.ConnectPort
	if connectPort == 0 {
		connectPort = 9000 // Default Buf Connect port
	}

	dashboardPort := colonyConfig.Services.DashboardPort
	if dashboardPort == 0 {
		dashboardPort = constants.DefaultDashboardPort
	}

	// Create discovery client for agent endpoint lookup
	var discoveryClient discoveryv1connect.DiscoveryServiceClient
	if globalConfig.Discovery.Endpoint != "" {
		discoveryClient = discoveryv1connect.NewDiscoveryServiceClient(
			http.DefaultClient,
			globalConfig.Discovery.Endpoint,
		)
		logger.Debug().
			Str("discovery_endpoint", globalConfig.Discovery.Endpoint).
			Msg("Discovery client configured for agent endpoint lookup")
	}

	// Create mesh service handler
	meshSvc := &meshServiceHandler{
		cfg:             cfg,
		wgDevice:        wgDevice,
		registry:        agentRegistry,
		logger:          logger,
		discoveryClient: discoveryClient,
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
	colonySvc := server.New(agentRegistry, db, colonyServerConfig, logger.With().Str("component", "colony-server").Logger())

	// Initialize MCP server (RFD 004 - MCP server integration).
	// Load colony config for MCP settings.
	mcpLoader, mcpErr := config.NewLoader()
	if mcpErr != nil {
		return nil, fmt.Errorf("failed to create config loader for MCP: %w", mcpErr)
	}
	colonyConfig, mcpErr = mcpLoader.LoadColonyConfig(cfg.ColonyID)
	if mcpErr != nil {
		return nil, fmt.Errorf("failed to load colony config for MCP: %w", mcpErr)
	}

	// Create MCP server if not disabled.
	if !colonyConfig.MCP.Disabled {
		mcpConfig := mcp.Config{
			ColonyID:              cfg.ColonyID,
			ApplicationName:       cfg.ApplicationName,
			Environment:           cfg.Environment,
			Disabled:              colonyConfig.MCP.Disabled,
			EnabledTools:          colonyConfig.MCP.EnabledTools,
			RequireRBACForActions: colonyConfig.MCP.Security.RequireRBACForActions,
			AuditEnabled:          colonyConfig.MCP.Security.AuditEnabled,
		}

		mcpServer, err := mcp.New(agentRegistry, db, mcpConfig, logger.With().Str("component", "mcp-server").Logger())
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to initialize MCP server, continuing without MCP support")
		} else {
			colonySvc.SetMCPServer(mcpServer)
			logger.Info().
				Int("tool_count", len(mcpServer.ListToolNames())).
				Msg("MCP server initialized and attached to colony")

			// Log all registered MCP tools.
			toolNames := mcpServer.ListToolNames()
			if len(toolNames) > 0 {
				logger.Info().
					Strs("tools", toolNames).
					Msg("Registered MCP tools")
			}
		}
	} else {
		logger.Info().Msg("MCP server is disabled in configuration")
	}

	// Register the handlers
	meshPath, meshHandler := meshv1connect.NewMeshServiceHandler(meshSvc)
	colonyPath, colonyHandler := colonyv1connect.NewColonyServiceHandler(colonySvc)

	// Create HTTP server
	mux := http.NewServeMux()
	mux.Handle(meshPath, meshHandler)
	mux.Handle(colonyPath, colonyHandler)

	// Add simple HTTP /status endpoint (similar to agent).
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		// Call the colony service's internal GetStatusResponse method directly.
		// This avoids the Connect protocol overhead and potential auth middleware issues.
		resp := colonySvc.GetStatusResponse()

		// Gather mesh network information for debugging.
		meshInfo := gatherColonyMeshInfo(wgDevice, cfg.WireGuard.MeshIPv4, cfg.WireGuard.MeshNetworkIPv4, cfg.ColonyID, logger)

		// Gather platform information.
		platformInfo := gatherPlatformInfo()

		// Group related fields for better organization.
		status := map[string]interface{}{
			"colony": map[string]interface{}{
				"id":          resp.ColonyId,
				"app_name":    resp.AppName,
				"environment": resp.Environment,
			},
			"runtime": map[string]interface{}{
				"status":         resp.Status,
				"started_at":     resp.StartedAt.AsTime(),
				"uptime_seconds": resp.UptimeSeconds,
				"agent_count":    resp.AgentCount,
				"storage_bytes":  resp.StorageBytes,
				"dashboard_url":  resp.DashboardUrl,
				"platform":       platformInfo,
			},
			"network": map[string]interface{}{
				"wireguard_port":       resp.WireguardPort,
				"wireguard_public_key": resp.WireguardPublicKey,
				"wireguard_endpoints":  resp.WireguardEndpoints,
				"connect_port":         resp.ConnectPort,
				"mesh_ipv4":            resp.MeshIpv4,
				"mesh_ipv6":            resp.MeshIpv6,
			},
			"mesh": meshInfo,
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(status); err != nil {
			logger.Error().Err(err).Msg("Failed to encode status response")
		}
	})

	addr := fmt.Sprintf(":%d", connectPort)
	httpServer := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Start server in background
	go func() {
		logger.Info().
			Int("port", connectPort).
			Str("status_endpoint", fmt.Sprintf("http://localhost:%d/status", connectPort)).
			Msg("Mesh and Colony services listening")

		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error().
				Err(err).
				Msg("Server error")
		}
	}()

	return httpServer, nil
}

// selectBestAgentEndpoint selects the best WireGuard endpoint for an agent from a list of observed endpoints.
// Strategy:
//  1. Skip localhost/127.0.0.1 endpoints (would be self-referential from colony's perspective)
//  2. Prefer an endpoint matching the peer's source IP (how they connected to us)
//  3. Otherwise use the first non-localhost endpoint
//
// Returns the selected endpoint and a match type ("matching" or "first"), or (nil, "") if no valid endpoint found.
func selectBestAgentEndpoint(
	observedEndpoints []*discoverypb.Endpoint,
	peerHost string,
	logger logging.Logger,
	agentID string,
) (*discoverypb.Endpoint, string) {
	var selectedEp *discoverypb.Endpoint
	var matchingEp *discoverypb.Endpoint

	for _, ep := range observedEndpoints {
		if ep == nil || ep.Ip == "" {
			continue
		}

		isLocalhost := ep.Ip == "127.0.0.1" || ep.Ip == "::1" || ep.Ip == "localhost"

		// If this endpoint's IP matches how the agent connected to us, prefer it.
		// This handles same-host deployments where agent connects from 127.0.0.1.
		if peerHost != "" && ep.Ip == peerHost && matchingEp == nil {
			matchingEp = ep
			if isLocalhost {
				logger.Debug().
					Str("agent_id", agentID).
					Str("endpoint", net.JoinHostPort(ep.Ip, fmt.Sprintf("%d", ep.Port))).
					Msg("Using localhost endpoint (agent connected from same host)")
			}
		}

		// Skip localhost endpoints UNLESS they matched the connection source.
		// This allows same-host deployments while preventing container issues.
		if isLocalhost && matchingEp == nil {
			logger.Debug().
				Str("agent_id", agentID).
				Str("endpoint", net.JoinHostPort(ep.Ip, fmt.Sprintf("%d", ep.Port))).
				Msg("Skipping localhost endpoint (agent connected from different host)")
			continue
		}

		// Track the first valid endpoint as fallback.
		if selectedEp == nil {
			selectedEp = ep
		}
	}

	// Prefer the matching endpoint, fallback to first non-localhost.
	if matchingEp != nil {
		return matchingEp, "matching"
	} else if selectedEp != nil {
		return selectedEp, "first"
	}

	return nil, ""
}

// meshServiceHandler implements the MeshService RPC handler.
type meshServiceHandler struct {
	cfg             *config.ResolvedConfig
	wgDevice        *wireguard.Device
	registry        *registry.Registry
	logger          logging.Logger
	discoveryClient discoveryv1connect.DiscoveryServiceClient
}

// Register handles agent registration requests.
func (h *meshServiceHandler) Register(
	ctx context.Context,
	req *connect.Request[meshv1.RegisterRequest],
) (*connect.Response[meshv1.RegisterResponse], error) {
	// Extract peer address from request headers for WireGuard endpoint detection.
	var peerAddr string
	if req.Peer().Addr != "" {
		peerAddr = req.Peer().Addr
	}

	h.logger.Info().
		Str("agent_id", req.Msg.AgentId).
		Str("component_name", req.Msg.ComponentName).
		Str("peer_addr", peerAddr).
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

	// Get agent's public endpoint from discovery service.
	// The agent registers its STUN-discovered public endpoint with discovery,
	// which we need for NAT traversal.
	var agentEndpoint string

	// Query discovery service for agent's observed endpoint
	if h.discoveryClient != nil {
		agentInfo, err := h.discoveryClient.LookupAgent(ctx, connect.NewRequest(&discoverypb.LookupAgentRequest{
			AgentId: req.Msg.AgentId,
		}))

		if err == nil && agentInfo.Msg != nil && len(agentInfo.Msg.ObservedEndpoints) > 0 {
			// Extract the peer's source IP from the HTTP connection to help select the right endpoint.
			var peerHost string
			if peerAddr != "" {
				if host, _, err := net.SplitHostPort(peerAddr); err == nil {
					peerHost = host
				}
			}

			// Select the best observed endpoint from the list.
			selectedEp, matchType := selectBestAgentEndpoint(agentInfo.Msg.ObservedEndpoints, peerHost, h.logger, req.Msg.AgentId)

			// Build endpoint string and log selection.
			if selectedEp != nil {
				agentEndpoint = net.JoinHostPort(selectedEp.Ip, fmt.Sprintf("%d", selectedEp.Port))
				if matchType == "matching" {
					h.logger.Info().
						Str("agent_id", req.Msg.AgentId).
						Str("endpoint", agentEndpoint).
						Str("peer_host", peerHost).
						Msg("Using agent's endpoint matching connection source")
				} else {
					h.logger.Info().
						Str("agent_id", req.Msg.AgentId).
						Str("endpoint", agentEndpoint).
						Msg("Using agent's observed endpoint from discovery service")
				}
			} else {
				h.logger.Warn().
					Str("agent_id", req.Msg.AgentId).
					Msg("All observed endpoints were localhost - agent may not be reachable via WireGuard")
			}
		} else {
			h.logger.Debug().
				Err(err).
				Str("agent_id", req.Msg.AgentId).
				Msg("Could not get agent endpoint from discovery service")
		}
	}

	// Fallback: extract agent's source address from HTTP connection.
	// This works for same-host testing but not for NAT traversal.
	// Note: peerAddr includes the HTTP port, not the WireGuard port.
	if agentEndpoint == "" && peerAddr != "" {
		if host, _, err := net.SplitHostPort(peerAddr); err == nil {
			// Use a default WireGuard port (this is just a guess and likely wrong for agents)
			// WireGuard will learn the correct endpoint from incoming packets
			h.logger.Debug().
				Str("agent_id", req.Msg.AgentId).
				Str("peer_addr", peerAddr).
				Msg("No discovery endpoint available, WireGuard will learn endpoint from incoming packets")
			_ = host
		}
	}

	// Add agent as WireGuard peer
	peerConfig := &wireguard.PeerConfig{
		PublicKey:           req.Msg.WireguardPubkey,
		Endpoint:            agentEndpoint, // Use detected endpoint
		AllowedIPs:          []string{meshIP.String() + "/32"},
		PersistentKeepalive: 25, // Keep NAT mappings alive
	}

	h.logger.Info().
		Str("agent_id", req.Msg.AgentId).
		Str("endpoint", agentEndpoint).
		Str("pubkey", req.Msg.WireguardPubkey[:8]+"...").
		Msg("Adding agent as WireGuard peer")

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
	if _, err := h.registry.Register(req.Msg.AgentId, req.Msg.ComponentName, meshIP.String(), "", req.Msg.Services, req.Msg.RuntimeContext, req.Msg.ProtocolVersion); err != nil {
		h.logger.Warn().
			Err(err).
			Str("agent_id", req.Msg.AgentId).
			Msg("Failed to register agent in registry (non-fatal)")
	}

	// Log registration with service details
	logEvent := h.logger.Info().
		Str("agent_id", req.Msg.AgentId).
		Str("component_name", req.Msg.ComponentName).
		Str("mesh_ip", meshIP.String())

	if len(req.Msg.Services) > 0 {
		logEvent.Int("service_count", len(req.Msg.Services))
	}

	logEvent.Msg("Agent registered successfully")

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

// Heartbeat handles agent heartbeat requests to update last_seen timestamp.
func (h *meshServiceHandler) Heartbeat(
	ctx context.Context,
	req *connect.Request[meshv1.HeartbeatRequest],
) (*connect.Response[meshv1.HeartbeatResponse], error) {
	h.logger.Debug().
		Str("agent_id", req.Msg.AgentId).
		Str("status", req.Msg.Status).
		Msg("Agent heartbeat received")

	// Validate agent_id
	if req.Msg.AgentId == "" {
		h.logger.Warn().Msg("Heartbeat rejected: missing agent_id")
		return connect.NewResponse(&meshv1.HeartbeatResponse{
			Ok: false,
		}), nil
	}

	// Update heartbeat in registry
	if err := h.registry.UpdateHeartbeat(req.Msg.AgentId); err != nil {
		h.logger.Warn().
			Err(err).
			Str("agent_id", req.Msg.AgentId).
			Msg("Failed to update agent heartbeat")
		return connect.NewResponse(&meshv1.HeartbeatResponse{
			Ok: false,
		}), nil
	}

	h.logger.Debug().
		Str("agent_id", req.Msg.AgentId).
		Msg("Agent heartbeat updated successfully")

	return connect.NewResponse(&meshv1.HeartbeatResponse{
		Ok:       true,
		Commands: []string{}, // Future: colony can send commands to agent
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

// truncate truncates a string to a maximum length.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// formatRuntimeTypeShort formats runtime type in short form.
func formatRuntimeTypeShort(rt agentv1.RuntimeContext) string {
	switch rt {
	case agentv1.RuntimeContext_RUNTIME_CONTEXT_NATIVE:
		return "Native"
	case agentv1.RuntimeContext_RUNTIME_CONTEXT_DOCKER:
		return "Docker"
	case agentv1.RuntimeContext_RUNTIME_CONTEXT_K8S_SIDECAR:
		return "K8s Sidecar"
	case agentv1.RuntimeContext_RUNTIME_CONTEXT_K8S_DAEMONSET:
		return "K8s DaemonSet"
	default:
		return "Unknown"
	}
}

// formatSidecarModeShort formats sidecar mode in short form.
func formatSidecarModeShort(sm agentv1.SidecarMode) string {
	switch sm {
	case agentv1.SidecarMode_SIDECAR_MODE_CRI:
		return "CRI"
	case agentv1.SidecarMode_SIDECAR_MODE_SHARED_NS:
		return "SharedNS"
	case agentv1.SidecarMode_SIDECAR_MODE_PASSIVE:
		return "Passive"
	default:
		return ""
	}
}

// outputAgentsVerbose outputs agents in verbose format with full runtime context.
func outputAgentsVerbose(agents []*colonyv1.Agent) error {
	fmt.Printf("Connected Agents (%d):\n\n", len(agents))

	for i, agent := range agents {
		if i > 0 {
			fmt.Println()
		}

		fmt.Printf("┌─ %s ", agent.AgentId)
		for j := 0; j < 50-len(agent.AgentId); j++ {
			fmt.Print("─")
		}
		fmt.Println("┐")

		fmt.Printf("│ Component:  %-45s│\n", agent.ComponentName)
		fmt.Printf("│ Status:     %-45s│\n", formatAgentStatus(agent))
		fmt.Printf("│ Mesh IP:    %-45s│\n", agent.MeshIpv4)
		fmt.Println("│                                                                │")

		if agent.RuntimeContext != nil {
			rc := agent.RuntimeContext
			fmt.Printf("│ Runtime:    %-45s│\n", formatRuntimeTypeShort(rc.RuntimeType))
			if rc.SidecarMode != agentv1.SidecarMode_SIDECAR_MODE_UNKNOWN {
				fmt.Printf("│ Mode:       %-45s│\n", formatSidecarModeShort(rc.SidecarMode))
			}
			if rc.Platform != nil {
				fmt.Printf("│ Platform:   %s (%s) %-27s│\n", rc.Platform.Os, rc.Platform.Arch, "")
			}
			if rc.CriSocket != nil {
				fmt.Printf("│ CRI:        %-45s│\n", rc.CriSocket.Type)
			}
			fmt.Println("│                                                                │")

			// Capabilities
			fmt.Println("│ Capabilities:                                                  │")
			fmt.Printf("│   %s connect  %s exec  %s shell  %s run                    │\n",
				formatCapabilitySymbol(rc.Capabilities.CanConnect),
				formatCapabilitySymbol(rc.Capabilities.CanExec),
				formatCapabilitySymbol(rc.Capabilities.CanShell),
				formatCapabilitySymbol(rc.Capabilities.CanRun))
			fmt.Println("│                                                                │")

			// Visibility
			fmt.Printf("│ Visibility: %-45s│\n", formatVisibilityShort(rc.Visibility))
		} else {
			fmt.Println("│ Runtime:    Unknown (legacy agent)                            │")
		}

		fmt.Println("└────────────────────────────────────────────────────────────────┘")
	}

	fmt.Println()
	return nil
}

// formatAgentStatus formats agent status with last seen time.
func formatAgentStatus(agent *colonyv1.Agent) string {
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

	statusSymbol := "✅"
	if agent.Status == "degraded" {
		statusSymbol = "⚠️"
	} else if agent.Status == "unhealthy" {
		statusSymbol = "❌"
	}

	return fmt.Sprintf("%s %s (%s)", statusSymbol, agent.Status, lastSeenStr)
}

func buildWireGuardEndpoints(port int, colonyConfig *config.ColonyConfig) []string {
	var endpoints []string
	var rawEndpoints []string

	// Priority 1: Check for explicit public endpoint configuration via environment variable.
	// CORAL_PUBLIC_ENDPOINT can contain comma-separated list of hostnames/IPs (optionally with ports).
	// Example: CORAL_PUBLIC_ENDPOINT=192.168.5.2:9000,10.0.0.5:9000,colony.example.com:9000
	if publicEndpoint := os.Getenv("CORAL_PUBLIC_ENDPOINT"); publicEndpoint != "" {
		// Parse comma-separated endpoints
		rawEndpoints = strings.Split(publicEndpoint, ",")
		for i := range rawEndpoints {
			rawEndpoints[i] = strings.TrimSpace(rawEndpoints[i])
		}
	} else if colonyConfig != nil && len(colonyConfig.WireGuard.PublicEndpoints) > 0 {
		// Priority 2: Use endpoints from config file
		rawEndpoints = colonyConfig.WireGuard.PublicEndpoints
	}

	// Process raw endpoints: extract host and ALWAYS use the WireGuard port.
	// We extract the host and ALWAYS use the WireGuard port, not the port from the env var.
	// This is because CORAL_PUBLIC_ENDPOINT typically contains the gRPC/Connect service address,
	// but we need to advertise the WireGuard UDP port for peer connections.
	for _, endpoint := range rawEndpoints {
		if endpoint == "" {
			continue
		}

		var host string
		// Try to extract host from endpoint (may have port)
		if h, _, err := net.SplitHostPort(endpoint); err == nil {
			host = h
		} else {
			// No port in the endpoint, use as-is
			host = endpoint
		}

		// Build WireGuard endpoint with the configured WireGuard port
		if host != "" {
			endpoints = append(endpoints, net.JoinHostPort(host, fmt.Sprintf("%d", port)))
		}
	}

	// If we found any endpoints, return them
	if len(endpoints) > 0 {
		return endpoints
	}

	// For local development: use localhost.
	// Agents on the same machine can connect via 127.0.0.1.
	//
	// For production deployments:
	// - Set CORAL_PUBLIC_ENDPOINT to your public IP or hostname (comma-separated for multiple)
	// - Or configure public_endpoints in the colony YAML config
	// - Or use NAT traversal/STUN (future feature)
	if port > 0 {
		endpoints = append(endpoints, net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)))
	}

	return endpoints
}

// getColonySTUNServers determines which STUN servers to use for colony NAT traversal.
// Priority: colony config > global config > default.
func getColonySTUNServers(colonyConfig *config.ColonyConfig, globalConfig *config.GlobalConfig) []string {
	// Check environment variable first
	envSTUN := os.Getenv("CORAL_STUN_SERVERS")
	if envSTUN != "" {
		servers := strings.Split(envSTUN, ",")
		for i := range servers {
			servers[i] = strings.TrimSpace(servers[i])
		}
		return servers
	}

	// Use colony-specific STUN servers if configured
	if len(colonyConfig.Discovery.STUNServers) > 0 {
		return colonyConfig.Discovery.STUNServers
	}

	// Fall back to global STUN servers
	if len(globalConfig.Discovery.STUNServers) > 0 {
		return globalConfig.Discovery.STUNServers
	}

	// Use default STUN server
	return []string{constants.DefaultSTUNServer}
}

// formatCapabilitySymbol formats capability as a checkmark or X.
func formatCapabilitySymbol(supported bool) string {
	if supported {
		return "✅"
	}
	return "❌"
}

// formatVisibilityShort formats visibility scope in short form.
func formatVisibilityShort(vis *agentv1.VisibilityScope) string {
	if vis.AllPids {
		return "All host processes"
	}
	if vis.AllContainers {
		return "All containers"
	}
	if vis.PodScope {
		return "Pod only"
	}
	return "Limited"
}

// gatherColonyMeshInfo gathers WireGuard mesh network information for the colony status endpoint.
func gatherColonyMeshInfo(
	wgDevice *wireguard.Device,
	meshIP, meshSubnet string,
	colonyID string,
	logger logging.Logger,
) map[string]interface{} {
	info := make(map[string]interface{})

	// Basic mesh info.
	info["colony_id"] = colonyID
	info["mesh_ip"] = meshIP
	info["mesh_subnet"] = meshSubnet

	// WireGuard interface info.
	if wgDevice != nil {
		wgInfo := make(map[string]interface{})
		wgInfo["interface_name"] = wgDevice.InterfaceName()
		wgInfo["listen_port"] = wgDevice.ListenPort()

		// Get interface status.
		iface := wgDevice.Interface()
		if iface != nil {
			wgInfo["interface_exists"] = true

			// Get peer information.
			peers := wgDevice.ListPeers()
			peerInfos := make([]map[string]interface{}, 0, len(peers))
			for _, peer := range peers {
				peerInfo := make(map[string]interface{})
				peerInfo["public_key"] = peer.PublicKey[:16] + "..."
				peerInfo["endpoint"] = peer.Endpoint
				peerInfo["allowed_ips"] = peer.AllowedIPs
				peerInfo["persistent_keepalive"] = peer.PersistentKeepalive
				peerInfos = append(peerInfos, peerInfo)
			}
			wgInfo["peers"] = peerInfos
			wgInfo["peer_count"] = len(peers)
		} else {
			wgInfo["interface_exists"] = false
		}

		info["wireguard"] = wgInfo
	}

	return info
}

// gatherPlatformInfo gathers platform information for the colony.
func gatherPlatformInfo() map[string]interface{} {
	// Use a detector to get platform information.
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error", // Only log errors for this detection
		Pretty: false,
	}, "platform-detector")

	detector := runtimepkg.NewDetector(logger, "dev")

	// Detect platform.
	ctx := context.Background()
	runtimeCtx, err := detector.Detect(ctx)
	if err != nil || runtimeCtx == nil || runtimeCtx.Platform == nil {
		// Fallback to basic info if detection fails.
		return map[string]interface{}{
			"os":   runtime.GOOS,
			"arch": runtime.GOARCH,
		}
	}

	// Return platform info.
	return map[string]interface{}{
		"os":         runtimeCtx.Platform.Os,
		"arch":       runtimeCtx.Platform.Arch,
		"os_version": runtimeCtx.Platform.OsVersion,
		"kernel":     runtimeCtx.Platform.Kernel,
	}
}

// formatServicesList formats the services array for display (RFD 044).
func formatServicesList(services []*meshv1.ServiceInfo) string {
	if len(services) == 0 {
		return ""
	}

	serviceNames := make([]string, 0, len(services))
	for _, svc := range services {
		serviceNames = append(serviceNames, svc.ComponentName)
	}
	return strings.Join(serviceNames, ", ")
}

// initializePersistentIPAllocator creates and injects a persistent IP allocator (RFD 019).
func initializePersistentIPAllocator(wgDevice *wireguard.Device, db *database.Database, logger logging.Logger) error {
	// Get the mesh network subnet from WireGuard config.
	cfg := wgDevice.Config()
	if cfg.MeshNetworkIPv4 == "" {
		cfg.MeshNetworkIPv4 = constants.DefaultColonyMeshIPv4Subnet
	}

	_, subnet, err := net.ParseCIDR(cfg.MeshNetworkIPv4)
	if err != nil {
		return fmt.Errorf("invalid mesh network CIDR: %w", err)
	}

	// Create database adapter for IP allocation store.
	store := database.NewIPAllocationStore(db)

	// Create persistent allocator with database store.
	allocator, err := wireguard.NewPersistentIPAllocator(subnet, store)
	if err != nil {
		return fmt.Errorf("failed to create persistent allocator: %w", err)
	}

	// Inject the persistent allocator into the WireGuard device.
	if err := wgDevice.SetAllocator(allocator); err != nil {
		return fmt.Errorf("failed to set allocator: %w", err)
	}

	logger.Info().
		Int("loaded_allocations", allocator.AllocatedCount()).
		Msg("Persistent IP allocator loaded from database")

	return nil
}
