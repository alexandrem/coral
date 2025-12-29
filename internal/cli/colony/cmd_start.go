package colony

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	discoverypb "github.com/coral-mesh/coral/coral/discovery/v1"
	"github.com/coral-mesh/coral/internal/colony"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/registry"
	colonywg "github.com/coral-mesh/coral/internal/colony/wireguard"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/constants"
	"github.com/coral-mesh/coral/internal/discovery/registration"
	"github.com/coral-mesh/coral/internal/logging"
)

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
			// Initialize logger early for preflight checks.
			logger := logging.NewWithComponent(logging.Config{
				Level:  "info",
				Pretty: true,
			}, "colony")

			// Perform preflight checks early (validates sudo context).
			if err := performPreflightChecks(logger); err != nil {
				return fmt.Errorf("preflight checks failed: %w", err)
			}

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

			// Update logger to debug level now that config is loaded.
			logger = logging.NewWithComponent(logging.Config{
				Level:  "trace",
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
			defer func() { _ = db.Close() }() // TODO: errcheck

			// TODO: Implement remaining colony startup tasks
			// - Start HTTP server for dashboard on cfg.Dashboard.Port

			// Initialize WireGuard device (but don't start it yet)
			wgDevice, err := colonywg.CreateDevice(cfg, logger)
			if err != nil {
				return fmt.Errorf("failed to create WireGuard device: %w", err)
			}
			defer func() { _ = wgDevice.Stop() }() // TODO: errcheck

			// Set up the persistent allocator BEFORE starting the device (RFD 019).
			// This enables IP allocation recovery after colony restarts.
			if err := colonywg.InitializePersistentIPAllocator(wgDevice, db, logger); err != nil {
				logger.Warn().
					Err(err).
					Msg("Failed to initialize persistent IP allocator, using in-memory allocator")
			} else {
				logger.Info().Msg("Persistent IP allocator initialized")
			}

			// Now start the WireGuard device with the persistent allocator configured
			if err := colonywg.StartDevice(wgDevice, cfg, logger); err != nil {
				return fmt.Errorf("failed to start WireGuard device: %w", err)
			}

			// Note: Colony continues running with elevated privileges for network management.
			// As agents connect, colony dynamically adds routes for their AllowedIPs, which
			// requires root privileges (route command on macOS/Linux).
			logger.Debug().Msg("Colony running with elevated privileges for dynamic network management")

			// Create agent registry for tracking connected agents.
			agentRegistry := registry.New(db)

			// Load persisted services from database to restore registry state after restart.
			if err := agentRegistry.LoadFromDatabase(context.Background()); err != nil {
				logger.Warn().
					Err(err).
					Msg("Failed to load persisted services from database")
			}

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

			endpoints := colonywg.BuildEndpoints(cfg.WireGuard.Port, colonyConfigForEndpoints)
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
			defer func() { _ = meshServer.Close() }() // TODO: errcheck

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
				ConnectPort:       uint32(connectPort), //nolint:gosec // G115: Port numbers are small positive values
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

			// Create and start System Metrics poller for RFD 071.
			// Read system metrics configuration from colony config, with sensible defaults.
			systemMetricsPollIntervalSecs := 60 // Default: poll every 60 seconds
			systemMetricsRetentionDays := 30    // Default: 30 days retention

			if colonyConfigForEndpoints != nil && colonyConfigForEndpoints.SystemMetrics.PollInterval > 0 {
				systemMetricsPollIntervalSecs = colonyConfigForEndpoints.SystemMetrics.PollInterval
			}
			if colonyConfigForEndpoints != nil && colonyConfigForEndpoints.SystemMetrics.RetentionDays > 0 {
				systemMetricsRetentionDays = colonyConfigForEndpoints.SystemMetrics.RetentionDays
			}

			systemMetricsPoller := colony.NewSystemMetricsPoller(
				agentRegistry,
				db,
				time.Duration(systemMetricsPollIntervalSecs)*time.Second,
				systemMetricsRetentionDays,
				logger,
			)

			if err := systemMetricsPoller.Start(); err != nil {
				logger.Warn().
					Err(err).
					Msg("Failed to start system metrics poller")
			} else {
				logger.Info().
					Int("poll_interval_secs", systemMetricsPollIntervalSecs).
					Int("retention_days", systemMetricsRetentionDays).
					Msg("System metrics poller started")
			}

			// Create and start CPU Profile poller for RFD 072.
			// Read CPU profiling configuration from colony config, with sensible defaults.
			cpuProfilePollIntervalSecs := 30 // Default: poll every 30 seconds
			cpuProfileRetentionDays := 30    // Default: 30 days retention

			if colonyConfigForEndpoints != nil && colonyConfigForEndpoints.ContinuousProfiling.PollInterval > 0 {
				cpuProfilePollIntervalSecs = colonyConfigForEndpoints.ContinuousProfiling.PollInterval
			}
			if colonyConfigForEndpoints != nil && colonyConfigForEndpoints.ContinuousProfiling.RetentionDays > 0 {
				cpuProfileRetentionDays = colonyConfigForEndpoints.ContinuousProfiling.RetentionDays
			}

			cpuProfilePoller := colony.NewCPUProfilePoller(
				agentRegistry,
				db,
				time.Duration(cpuProfilePollIntervalSecs)*time.Second,
				cpuProfileRetentionDays,
				logger,
			)

			if err := cpuProfilePoller.Start(); err != nil {
				logger.Warn().
					Err(err).
					Msg("Failed to start CPU profile poller")
			} else {
				logger.Info().
					Int("poll_interval_secs", cpuProfilePollIntervalSecs).
					Int("retention_days", cpuProfileRetentionDays).
					Msg("CPU profile poller started")
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

				// Stop System Metrics poller
				if err := systemMetricsPoller.Stop(); err != nil {
					logger.Warn().
						Err(err).
						Msg("Error stopping system metrics poller")
				}

				// Stop CPU Profile poller
				if err := cpuProfilePoller.Stop(); err != nil {
					logger.Warn().
						Err(err).
						Msg("Error stopping CPU profile poller")
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
