package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/spf13/cobra"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"gopkg.in/yaml.v3"

	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	discoverypb "github.com/coral-mesh/coral/coral/discovery/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/coral/mesh/v1/meshv1connect"
	"github.com/coral-mesh/coral/internal/agent"
	"github.com/coral-mesh/coral/internal/agent/beyla"
	"github.com/coral-mesh/coral/internal/agent/collector"
	"github.com/coral-mesh/coral/internal/agent/profiler"
	"github.com/coral-mesh/coral/internal/agent/telemetry"
	"github.com/coral-mesh/coral/internal/auth"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/constants"
	"github.com/coral-mesh/coral/internal/duckdb"
	"github.com/coral-mesh/coral/internal/logging"
	"github.com/coral-mesh/coral/internal/privilege"
	pkgruntime "github.com/coral-mesh/coral/internal/runtime"
	"github.com/coral-mesh/coral/internal/wireguard"
)

// NewStartCmd creates the start command for agents.
func NewStartCmd() *cobra.Command {
	var (
		configFile     string
		colonyID       string
		daemon         bool
		monitorAll     bool
		connectService []string // Service URIs to connect at startup (e.g., "frontend:3000")
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start a Coral agent as a daemon",
		Long: `Start a Coral agent as a long-running daemon.

The agent will:
- Monitor configured services (if any)
- Detect and report runtime context
- Connect to colony (if configured)
- Store metrics locally
- Run until stopped by signal
- Accept dynamic service connections via 'coral connect'

Modes:
  Passive mode:  Start without services (use 'coral connect' later)
  Active mode:   Start with pre-configured services
  Monitor all:   Auto-discover and monitor all processes (--monitor-all)

Configuration sources (in order of precedence):
1. Environment variables (CORAL_*)
2. Config file (--config flag or /etc/coral/agent.yaml)
3. Defaults

Environment Variables:
  CORAL_COLONY_ID        - Colony ID to connect to
  CORAL_COLONY_SECRET    - Colony authentication secret
  CORAL_SERVICES         - Services to monitor (format: name:port[:health][:type],...)
  CORAL_LOG_LEVEL        - Logging level (debug, info, warn, error)
  CORAL_LOG_FORMAT       - Logging format (json, pretty)

Configuration File Format:
  agent:
    runtime: auto
    colony:
      id: "production"
      auto_discover: true
  services:
    - name: "api"
      port: 8080
      health_endpoint: "/health"
      type: "http"

Examples:
  # Passive mode (no services, use 'coral connect' later)
  coral agent start

  # Connect to services at startup
  coral agent start --connect frontend:3000 --connect api:8080:/health

  # With config file
  coral agent start --config /etc/coral/agent.yaml

  # With environment variables
  CORAL_COLONY_ID=prod CORAL_SERVICES=api:8080:/health coral agent start

  # Monitor all processes (auto-discovery)
  coral agent start --monitor-all

  # Development mode (pretty logging)
  coral agent start --config ./agent.yaml --log-format=pretty`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Initialize logger early for preflight checks
			logger := logging.NewWithComponent(logging.Config{
				Level:  "debug",
				Pretty: true,
			}, "agent")

			// Perform preflight checks - warns about missing capabilities on Linux (allows degraded operation)
			// On macOS, fails hard if not running as root
			if err := performAgentPreflightChecks(logger); err != nil {
				return err
			}

			// Load configuration
			cfg, serviceSpecs, agentCfg, err := loadAgentConfig(configFile, colonyID)
			if err != nil {
				return fmt.Errorf("failed to load agent configuration: %w", err)
			}

			// Parse services from --connect flag (RFD 053).
			if len(connectService) > 0 {
				connectSpecs, err := ParseMultipleServiceSpecs(connectService)
				if err != nil {
					return fmt.Errorf("failed to parse --connect services: %w", err)
				}
				// Merge with config file services (--connect takes precedence).
				serviceSpecs = append(serviceSpecs, connectSpecs...)
			}

			// Validate service specs (if any provided)
			if len(serviceSpecs) > 0 {
				if err := ValidateServiceSpecs(serviceSpecs); err != nil {
					return fmt.Errorf("invalid service configuration: %w", err)
				}
			}

			// Determine agent mode
			agentMode := "passive"
			if monitorAll {
				agentMode = "monitor-all"
			} else if len(serviceSpecs) > 0 {
				agentMode = "active"
			}

			logger.Info().
				Str("colony_id", cfg.ColonyID).
				Int("service_count", len(serviceSpecs)).
				Str("runtime", "auto-detect").
				Str("mode", agentMode).
				Msg("Starting Coral agent")

			switch agentMode {
			case "passive":
				logger.Info().Msg("Agent running in passive mode - use 'coral connect' to attach services")
			case "monitor-all":
				logger.Info().Msg("Agent running in monitor-all mode - auto-discovering processes")
			}

			// Log service configuration.
			for _, spec := range serviceSpecs {
				logger.Info().
					Str("service", spec.Name).
					Int32("port", spec.Port).
					Str("health_endpoint", spec.HealthEndpoint).
					Str("type", spec.ServiceType).
					Msg("Configured service")
			}

			// Query discovery service for colony information.
			logger.Info().
				Str("colony_id", cfg.ColonyID).
				Msg("Querying discovery service for colony information")

			// Attempt to query discovery service.
			// If this fails, agent will continue startup and retry in background.
			colonyInfo, err := queryDiscoveryForColony(cfg, logger)
			if err != nil {
				logger.Warn().
					Err(err).
					Msg("Failed to query discovery service - will retry in background")
				colonyInfo = nil // Agent will start in waiting_discovery state
			} else {
				logger.Info().
					Str("colony_pubkey", colonyInfo.Pubkey).
					Strs("endpoints", colonyInfo.Endpoints).
					Msg("Received colony information from discovery")
			}

			// Generate WireGuard keys for this agent.
			agentKeys, err := auth.GenerateWireGuardKeyPair()
			if err != nil {
				return fmt.Errorf("failed to generate WireGuard keys: %w", err)
			}

			logger.Info().
				Str("agent_pubkey", agentKeys.PublicKey).
				Msg("Generated agent WireGuard keys")

			// Get STUN servers for NAT traversal
			stunServers := getSTUNServers(agentCfg, colonyInfo)
			if len(stunServers) > 0 {
				logger.Info().
					Strs("stun_servers", stunServers).
					Msg("STUN servers configured for NAT traversal")
			}

			// Check if relay is enabled
			enableRelay := agentCfg.Agent.NAT.EnableRelay
			if envRelay := os.Getenv("CORAL_ENABLE_RELAY"); envRelay != "" {
				enableRelay = envRelay == "true" || envRelay == "1"
			}

			// Get WireGuard port from environment or use ephemeral (-1).
			// For Docker deployments with NAT, use a static port (e.g., 51821) and map it in docker-compose.
			// Example: CORAL_WIREGUARD_PORT=51821
			wgPort := -1 // Default: ephemeral port
			if envPort := os.Getenv("CORAL_WIREGUARD_PORT"); envPort != "" {
				if port, err := strconv.Atoi(envPort); err == nil && port > 0 && port < 65536 {
					wgPort = port
					logger.Info().
						Int("port", wgPort).
						Msg("Using configured WireGuard port")
				} else {
					logger.Warn().
						Str("port", envPort).
						Msg("Invalid CORAL_WIREGUARD_PORT value, using ephemeral port")
				}
			}

			// Create and start WireGuard device (RFD 019: without peer, without IP).
			// This also performs STUN discovery before starting WireGuard to avoid port conflicts.
			wgDevice, agentObservedEndpoint, _, err := setupAgentWireGuard(
				agentKeys,
				colonyInfo,
				stunServers,
				enableRelay,
				wgPort,
				logger,
			)
			if err != nil {
				return fmt.Errorf("failed to setup WireGuard: %w", err)
			}
			defer func() { _ = wgDevice.Stop() }() // TODO: errcheck

			// Note: Agent continues running with elevated privileges for eBPF operations.
			// Required capabilities: CAP_NET_ADMIN, CAP_SYS_PTRACE, CAP_SYS_RESOURCE.
			// Modern (kernel 5.8+): CAP_BPF, CAP_PERFMON, CAP_SYSLOG (optional).
			// CAP_SYS_ADMIN: Only needed for nsenter exec mode or as fallback on older kernels.
			logger.Debug().Msg("Agent running with elevated privileges for eBPF/Beyla operations")

			// Generate agent ID early so we can use it for registration
			agentID := generateAgentID(serviceSpecs)

			// Register agent with discovery service using the observed endpoint from STUN.
			// Skip registration if we don't have an observed endpoint (STUN failed or not configured).
			if agentObservedEndpoint != nil {
				logger.Info().
					Str("agent_id", agentID).
					Str("public_ip", agentObservedEndpoint.Ip).
					Uint32("public_port", agentObservedEndpoint.Port).
					Msg("Registering agent with discovery service")

				if err := registerAgentWithDiscovery(cfg, agentID, agentKeys.PublicKey, agentObservedEndpoint, logger); err != nil {
					logger.Warn().Err(err).Msg("Failed to register agent with discovery service (continuing anyway)")
				}
			} else {
				logger.Info().Msg("No observed endpoint available (STUN not configured or failed), skipping discovery service registration")
			}

			// Create and start runtime service early so it's available for registration (RFD 018).
			// This ensures the runtime context is detected before we attempt colony registration.
			runtimeService, err := agent.NewRuntimeService(agent.RuntimeServiceConfig{
				AgentID:         agentID,
				Logger:          logger,
				Version:         "dev", // TODO: Get version from build info
				RefreshInterval: 5 * time.Minute,
			})
			if err != nil {
				return fmt.Errorf("failed to create runtime service: %w", err)
			}

			if err := runtimeService.Start(); err != nil {
				return fmt.Errorf("failed to start runtime service: %w", err)
			}
			defer func() { _ = runtimeService.Stop() }() // TODO: errcheck

			// Create connection manager to handle registration and reconnection.
			connMgr := NewConnectionManager(
				agentID,
				colonyInfo,
				cfg,
				serviceSpecs,
				agentKeys.PublicKey,
				wgDevice,
				runtimeService,
				logger,
			)

			// Attempt initial registration with colony.
			// If this fails, agent will continue startup and attempt reconnection in background.
			meshIPStr, meshSubnetStr, err := connMgr.AttemptRegistration()
			if err != nil {
				logger.Warn().
					Err(err).
					Msg("Failed initial registration with colony - will retry in background")
				// Agent continues in unregistered state, reconnection loop will handle retries.
			} else {
				// Parse IP and subnet for mesh configuration (RFD 019).
				meshIP := net.ParseIP(meshIPStr)
				if meshIP == nil {
					return fmt.Errorf("invalid mesh IP from colony: %s", meshIPStr)
				}

				_, meshSubnet, err := net.ParseCIDR(meshSubnetStr)
				if err != nil {
					return fmt.Errorf("invalid mesh subnet from colony: %w", err)
				}

				// Configure agent mesh with permanent IP (RFD 019).
				// This assigns the IP and adds the colony as a peer with correct routing.
				logger.Info().
					Str("mesh_ip", meshIPStr).
					Str("subnet", meshSubnetStr).
					Msg("Configuring agent mesh with permanent IP from colony")

				// Get colony endpoint from connection manager (handles cases where discovery succeeded later).
				colonyEndpointForMesh := connMgr.GetColonyEndpoint()
				if colonyEndpointForMesh == "" {
					return fmt.Errorf("no colony endpoint available for mesh configuration")
				}

				if err := configureAgentMesh(wgDevice, meshIP, meshSubnet, connMgr.GetColonyInfo(), colonyEndpointForMesh, logger); err != nil {
					return fmt.Errorf("failed to configure agent mesh: %w", err)
				}

				logger.Info().
					Str("mesh_ip", meshIPStr).
					Msg("Agent mesh configured successfully - tunnel ready")

				// Get connect port for heartbeat.
				currentColonyInfo := connMgr.GetColonyInfo()
				if currentColonyInfo != nil {
					connectPort := currentColonyInfo.ConnectPort
					if connectPort == 0 {
						connectPort = 9000
					}
					meshAddr := net.JoinHostPort(currentColonyInfo.MeshIpv4, fmt.Sprintf("%d", connectPort))
					logger.Info().
						Str("mesh_addr", meshAddr).
						Msg("Testing connectivity to colony via mesh to establish WireGuard handshake")

					conn, err := net.DialTimeout("tcp", meshAddr, 5*time.Second)
					if err != nil {
						logger.Warn().
							Err(err).
							Str("mesh_addr", meshAddr).
							Msg("Unable to establish connection to colony via mesh - handshake may not be complete")
					} else {
						_ = conn.Close() // TODO: errcheck
						logger.Info().
							Str("mesh_addr", meshAddr).
							Msg("Successfully established WireGuard tunnel to colony")
					}
				}
			}

			// Log agent startup status.
			currentIP, _ := connMgr.GetAssignedIP()
			currentState := connMgr.GetState()
			if currentIP != "" {
				logger.Info().
					Str("agent_id", agentID).
					Str("mesh_ip", currentIP).
					Int("service_count", len(serviceSpecs)).
					Str("state", currentState.String()).
					Msg("Agent connected successfully")
			} else if currentState == StateWaitingDiscovery {
				logger.Info().
					Str("agent_id", agentID).
					Int("service_count", len(serviceSpecs)).
					Str("state", currentState.String()).
					Msg("Agent started (waiting for discovery service - will connect when available)")
			} else {
				logger.Info().
					Str("agent_id", agentID).
					Int("service_count", len(serviceSpecs)).
					Str("state", currentState.String()).
					Msg("Agent started (unregistered - attempting reconnection in background)")
			}

			// Start agent instance (always created, even in passive mode).
			serviceInfos := make([]*meshv1.ServiceInfo, len(serviceSpecs))
			for i, spec := range serviceSpecs {
				serviceInfos[i] = spec.ToProto()
			}

			// Create shared DuckDB database for all agent data (telemetry + Beyla + custom).
			// All tables (spans, beyla_http_metrics_local, etc.) live in the same database.
			var sharedDB *sql.DB
			var sharedDBPath string
			homeDir, err := os.UserHomeDir()
			if err == nil {
				// Create parent directories if they don't exist.
				dbDir := homeDir + "/.coral/agent"
				if err := os.MkdirAll(dbDir, 0750); err != nil {
					logger.Warn().Err(err).Msg("Failed to create agent directory - using in-memory storage")
				} else {
					sharedDBPath = dbDir + "/metrics.duckdb"
					sharedDB, err = sql.Open("duckdb", sharedDBPath)
					if err != nil {
						logger.Warn().Err(err).Msg("Failed to create shared metrics database - using in-memory storage")
						sharedDB = nil
						sharedDBPath = ""
					} else {
						logger.Info().
							Str("db_path", sharedDBPath).
							Msg("Initialized shared metrics database")
					}
				}
			} else {
				logger.Warn().Err(err).Msg("Failed to get user home directory - using in-memory storage")
			}

			// Initialize Beyla configuration (RFD 032 + RFD 053).
			var beylaConfig *beyla.Config
			if sharedDB != nil && !agentCfg.Beyla.Disabled {
				// Check if we have any services to monitor (configured, dynamic, or monitor-all)
				hasConfiguredServices := len(agentCfg.Beyla.Discovery.Services) > 0
				hasDynamicServices := len(serviceSpecs) > 0

				if monitorAll || hasConfiguredServices || hasDynamicServices {
					logger.Info().Msg("Initializing Beyla configuration")

					// Convert config.BeylaConfig to beyla.Config
					// We use values from agentCfg.Beyla which are populated with defaults + user overrides
					beylaConfig = &beyla.Config{
						Enabled:      true,
						OTLPEndpoint: agentCfg.Beyla.OTLPEndpoint,
						Protocols: beyla.ProtocolsConfig{
							HTTPEnabled:  agentCfg.Beyla.Protocols.HTTP.Enabled,
							GRPCEnabled:  agentCfg.Beyla.Protocols.GRPC.Enabled,
							SQLEnabled:   agentCfg.Beyla.Protocols.SQL.Enabled,
							KafkaEnabled: agentCfg.Beyla.Protocols.Kafka.Enabled,
							RedisEnabled: agentCfg.Beyla.Protocols.Redis.Enabled,
						},
						Attributes:            agentCfg.Beyla.Attributes,
						SamplingRate:          agentCfg.Beyla.Sampling.Rate,
						DB:                    sharedDB,
						DBPath:                sharedDBPath,
						StorageRetentionHours: 1, // Default: 1 hour (TODO: make configurable)
						MonitorAll:            monitorAll,
					}

					// Add configured services from config file to discovery
					for _, svc := range agentCfg.Beyla.Discovery.Services {
						if svc.OpenPort > 0 {
							beylaConfig.Discovery.OpenPorts = append(beylaConfig.Discovery.OpenPorts, svc.OpenPort)
						}
						// TODO: Support K8s discovery mapping when available in beyla.DiscoveryConfig
					}

					// Add dynamic ports from services (RFD 053)
					for _, spec := range serviceSpecs {
						beylaConfig.Discovery.OpenPorts = append(beylaConfig.Discovery.OpenPorts, int(spec.Port))
					}

					if monitorAll {
						logger.Info().Msg("Monitor-all mode enabled - Beyla will instrument all listening processes")
					}
				} else {
					logger.Info().Msg("No services configured - Beyla will not start (use --monitor-all or --connect to enable)")
				}
			} else if agentCfg.Beyla.Disabled {
				logger.Info().Msg("Beyla explicitly disabled in configuration")
			}

			// Close shared database LAST (defer added first = executes last in LIFO order).
			if sharedDB != nil {
				defer func() {
					logger.Info().Msg("Closing shared database")
					if err := sharedDB.Close(); err != nil {
						logger.Error().Err(err).Msg("Failed to close shared database")
					} else {
						logger.Info().Msg("Closed shared database")
					}
				}()
			}

			// Create function cache with agent's DuckDB (RFD 063).
			// Must be created before agent since agent needs it for monitors.
			functionCache, err := agent.NewFunctionCache(sharedDB, logger)
			if err != nil {
				return fmt.Errorf("failed to create function cache: %w", err)
			}

			agentInstance, err := agent.New(agent.Config{
				AgentID:       agentID,
				Services:      serviceInfos,
				BeylaConfig:   beylaConfig,
				FunctionCache: functionCache,
				Logger:        logger,
			})
			if err != nil {
				return fmt.Errorf("failed to create agent: %w", err)
			}

			if err := agentInstance.Start(); err != nil {
				return fmt.Errorf("failed to start agent: %w", err)
			}
			defer func() { _ = agentInstance.Stop() }() // TODO: errcheck

			// Create context for background operations.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Start OTLP receiver if telemetry is not disabled (RFD 025).
			// Telemetry is enabled by default and uses the shared database.
			// This receiver handles BOTH application telemetry AND Beyla's output.
			var otlpReceiver *agent.TelemetryReceiver
			if !agentCfg.Telemetry.Disabled && sharedDB != nil {
				logger.Info().Msg("Starting OTLP receiver for telemetry collection")

				telemetryConfig := telemetry.Config{
					Disabled:              agentCfg.Telemetry.Disabled,
					GRPCEndpoint:          agentCfg.Telemetry.GRPCEndpoint,
					HTTPEndpoint:          agentCfg.Telemetry.HTTPEndpoint,
					DatabasePath:          sharedDBPath, // Use shared database path
					StorageRetentionHours: agentCfg.Telemetry.StorageRetentionHours,
					AgentID:               agentID,
				}

				// Set filter config with defaults if not specified.
				if agentCfg.Telemetry.Filters.AlwaysCaptureErrors {
					telemetryConfig.Filters.AlwaysCaptureErrors = true
				} else {
					telemetryConfig.Filters.AlwaysCaptureErrors = true // Default: true
				}

				if agentCfg.Telemetry.Filters.HighLatencyThresholdMs > 0 {
					telemetryConfig.Filters.HighLatencyThresholdMs = agentCfg.Telemetry.Filters.HighLatencyThresholdMs
				} else {
					telemetryConfig.Filters.HighLatencyThresholdMs = 500.0 // Default: 500ms
				}

				if agentCfg.Telemetry.Filters.SampleRate > 0 {
					telemetryConfig.Filters.SampleRate = agentCfg.Telemetry.Filters.SampleRate
				} else {
					telemetryConfig.Filters.SampleRate = 0.10 // Default: 10%
				}

				// Set default endpoints if not specified.
				if telemetryConfig.GRPCEndpoint == "" {
					telemetryConfig.GRPCEndpoint = "0.0.0.0:4317"
				}
				if telemetryConfig.HTTPEndpoint == "" {
					telemetryConfig.HTTPEndpoint = "0.0.0.0:4318"
				}
				if telemetryConfig.StorageRetentionHours == 0 {
					telemetryConfig.StorageRetentionHours = 1 // Default: 1 hour
				}

				otlpReceiver, err = agent.NewTelemetryReceiverWithSharedDB(telemetryConfig, sharedDB, sharedDBPath, logger)
				if err != nil {
					logger.Warn().Err(err).Msg("Failed to create OTLP receiver - telemetry disabled")
				} else {
					if err := otlpReceiver.Start(ctx); err != nil {
						logger.Warn().Err(err).Msg("Failed to start OTLP receiver - telemetry disabled")
					} else {
						logger.Info().
							Str("grpc_endpoint", telemetryConfig.GRPCEndpoint).
							Str("http_endpoint", telemetryConfig.HTTPEndpoint).
							Float64("sample_rate", telemetryConfig.Filters.SampleRate).
							Float64("latency_threshold_ms", telemetryConfig.Filters.HighLatencyThresholdMs).
							Msg("OTLP receiver started successfully")

						defer func() {
							if err := otlpReceiver.Stop(); err != nil {
								logger.Error().Err(err).Msg("Failed to stop OTLP receiver")
							}
						}()
					}
				}
			} else if agentCfg.Telemetry.Disabled {
				logger.Info().Msg("Telemetry collection is disabled")
			} else {
				logger.Warn().Msg("Telemetry disabled - shared database not available")
			}

			// Start system metrics collector if enabled (RFD 071).
			var systemMetricsHandler *agent.SystemMetricsHandler
			if !agentCfg.SystemMetrics.Disabled && sharedDB != nil {
				logger.Info().Msg("Initializing system metrics collector")

				// Create collector config from agent config.
				collectorConfig := collector.Config{
					Enabled:        !agentCfg.SystemMetrics.Disabled,
					Interval:       agentCfg.SystemMetrics.Interval,
					CPUEnabled:     agentCfg.SystemMetrics.CPUEnabled,
					MemoryEnabled:  agentCfg.SystemMetrics.MemoryEnabled,
					DiskEnabled:    agentCfg.SystemMetrics.DiskEnabled,
					NetworkEnabled: agentCfg.SystemMetrics.NetworkEnabled,
				}

				// Create storage for system metrics.
				metricsStorage, err := collector.NewStorage(sharedDB, logger)
				if err != nil {
					logger.Warn().Err(err).Msg("Failed to create system metrics storage - metrics disabled")
				} else {
					// Create and start collector.
					systemCollector := collector.NewSystemCollector(metricsStorage, collectorConfig, logger)

					// Start collector in background.
					go func() {
						if err := systemCollector.Start(ctx); err != nil && err != context.Canceled {
							logger.Error().Err(err).Msg("System metrics collector stopped with error")
						}
					}()

					// Start cleanup goroutine for old metrics (1 hour retention).
					go func() {
						cleanupTicker := time.NewTicker(10 * time.Minute)
						defer cleanupTicker.Stop()

						for {
							select {
							case <-ctx.Done():
								return
							case <-cleanupTicker.C:
								if err := metricsStorage.CleanupOldMetrics(ctx, agentCfg.SystemMetrics.Retention); err != nil {
									logger.Error().Err(err).Msg("Failed to cleanup old system metrics")
								} else {
									logger.Debug().Msg("Cleaned up old system metrics")
								}
							}
						}
					}()

					// Create system metrics handler for RPC queries.
					systemMetricsHandler = agent.NewSystemMetricsHandler(metricsStorage)

					logger.Info().
						Dur("interval", collectorConfig.Interval).
						Dur("retention", agentCfg.SystemMetrics.Retention).
						Bool("cpu", collectorConfig.CPUEnabled).
						Bool("memory", collectorConfig.MemoryEnabled).
						Bool("disk", collectorConfig.DiskEnabled).
						Bool("network", collectorConfig.NetworkEnabled).
						Msg("System metrics collector started successfully")
				}
			} else if agentCfg.SystemMetrics.Disabled {
				logger.Info().Msg("System metrics collection is disabled")
			} else {
				logger.Warn().Msg("System metrics disabled - shared database not available")
			}

			// Initialize continuous CPU profiling (RFD 072).
			if sharedDB != nil && agentCfg.ContinuousProfiling.Enabled && agentCfg.ContinuousProfiling.CPU.Enabled {
				logger.Info().Msg("Initializing continuous CPU profiling")

				// Import profiler package.
				profilerConfig := profiler.Config{
					Enabled:           agentCfg.ContinuousProfiling.CPU.Enabled,
					FrequencyHz:       agentCfg.ContinuousProfiling.CPU.FrequencyHz,
					Interval:          agentCfg.ContinuousProfiling.CPU.Interval,
					SampleRetention:   agentCfg.ContinuousProfiling.CPU.Retention,
					MetadataRetention: agentCfg.ContinuousProfiling.CPU.MetadataRetention,
				}

				// Get debug manager for kernel symbolizer access.
				debugManager := agentInstance.GetDebugManager()

				cpuProfiler, err := profiler.NewContinuousCPUProfiler(
					sharedDB,
					debugManager,
					logger,
					profilerConfig,
				)
				if err != nil {
					logger.Warn().Err(err).Msg("Failed to create continuous CPU profiler - profiling disabled")
				} else {
					// Build list of services to profile.
					var profilingServices []profiler.ServiceInfo
					for _, svc := range serviceInfos {
						if svc.ProcessId > 0 {
							profilingServices = append(profilingServices, profiler.ServiceInfo{
								ServiceID:  svc.Name,
								PID:        int(svc.ProcessId),
								BinaryPath: fmt.Sprintf("/proc/%d/exe", svc.ProcessId),
							})
						}
					}

					// Set profiler on agent for RPC access.
					agentInstance.SetContinuousProfiler(cpuProfiler)

					// Start profiling.
					if len(profilingServices) > 0 {
						cpuProfiler.Start(profilingServices)
						logger.Info().
							Int("frequency_hz", profilerConfig.FrequencyHz).
							Dur("interval", profilerConfig.Interval).
							Int("service_count", len(profilingServices)).
							Msg("Continuous CPU profiling started successfully")
					} else {
						logger.Info().Msg("No services with PIDs to profile - continuous profiling ready for new services")
					}
				}
			} else if agentCfg.ContinuousProfiling.Enabled && !agentCfg.ContinuousProfiling.CPU.Enabled {
				logger.Info().Msg("Continuous CPU profiling is disabled via configuration")
			} else if !agentCfg.ContinuousProfiling.Enabled {
				logger.Info().Msg("Continuous profiling is disabled")
			} else {
				logger.Warn().Msg("Continuous profiling disabled - shared database not available")
			}

			// Log initial status.
			if len(serviceSpecs) > 0 {
				logger.Info().
					Str("status", string(agentInstance.GetStatus())).
					Msg("Agent status")

				for name, status := range agentInstance.GetServiceStatuses() {
					logger.Info().
						Str("service", name).
						Str("status", string(status.Status)).
						Msg("Service status")
				}
			} else {
				logger.Info().Msg("Agent started in passive mode - waiting for service connections via 'coral connect'")
			}

			// NOTE: Runtime service was created and started earlier (before ConnectionManager)
			// to ensure runtime context is available during colony registration (RFD 018).

			// Create shell handler (RFD 026).
			shellHandler := agent.NewShellHandler(logger)

			// Create container handler (RFD 056).
			containerHandler := agent.NewContainerHandler(logger)

			// Create service handler and HTTP server for gRPC API.
			serviceHandler := agent.NewServiceHandler(agentInstance, runtimeService, otlpReceiver, shellHandler, containerHandler, functionCache, systemMetricsHandler)
			path, handler := agentv1connect.NewAgentServiceHandler(serviceHandler)

			// Create debug service handler (RFD 059).
			debugService := agent.NewDebugService(agentInstance, logger)
			debugAdapter := agent.NewDebugServiceAdapter(debugService)
			debugPath, debugHandler := meshv1connect.NewDebugServiceHandler(debugAdapter)

			mux := http.NewServeMux()
			mux.Handle(path, handler)
			mux.Handle(debugPath, debugHandler)

			// Add /status endpoint that returns JSON with mesh network info for debugging.
			mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
				// Get runtime context from cache directly to avoid protocol overhead.
				runtimeCtx := runtimeService.GetCachedContext()
				if runtimeCtx == nil {
					http.Error(w, "runtime context not yet detected", http.StatusServiceUnavailable)
					return
				}

				// Gather mesh network information for debugging
				meshInfo := gatherMeshNetworkInfo(wgDevice, meshIPStr, meshSubnetStr, colonyInfo, agentID, logger)

				// Combine runtime context with mesh info
				status := map[string]interface{}{
					"runtime": runtimeCtx,
					"mesh":    meshInfo,
				}

				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(status); err != nil {
					logger.Error().Err(err).Msg("Failed to encode status response")
				}
			})

			// Add /duckdb/ endpoint for serving DuckDB files (RFD 039).
			// Register the shared metrics database containing all agent data.
			duckdbHandler := duckdb.NewDuckDBHandler(logger)
			registeredCount := 0

			// Register shared metrics database (if using file-based storage).
			if sharedDBPath != "" {
				if err := duckdbHandler.RegisterDatabase("metrics.duckdb", sharedDBPath); err != nil {
					logger.Warn().Err(err).Msg("Failed to register metrics database for HTTP serving")
				} else {
					logger.Info().
						Str("db_name", "metrics.duckdb").
						Str("db_path", sharedDBPath).
						Msg("Shared metrics database registered for HTTP serving")
					registeredCount++
				}
			}

			// TODO: Register additional custom databases from configuration.

			if registeredCount == 0 {
				logger.Warn().Msg("No DuckDB databases registered for HTTP serving (all using in-memory storage)")
			} else {
				logger.Info().
					Int("count", registeredCount).
					Msg("DuckDB databases available for remote queries")
			}

			mux.Handle("/duckdb/", duckdbHandler)

			// Enable HTTP/2 Cleartext (h2c) for bidirectional streaming (RFD 026).
			h2s := &http2.Server{}
			httpHandler := h2c.NewHandler(mux, h2s)

			// Create two HTTP servers for security (RFD 039):
			// 1. Mesh IP: Accessible from other agents/colony via WireGuard
			// 2. Localhost: Accessible locally for debugging (not exposed externally)
			var meshServer, localhostServer *http.Server

			// Server 1: Bind to WireGuard mesh IP (secure remote access).
			if meshIPStr != "" {
				meshAddr := net.JoinHostPort(meshIPStr, "9001")
				//nolint:gosec // G112: ReadHeaderTimeout will be added in future refactoring
				meshServer = &http.Server{
					Addr:    meshAddr,
					Handler: httpHandler,
				}

				go func() {
					logger.Info().
						Str("addr", meshAddr).
						Msg("Agent API listening on WireGuard mesh")

					if err := meshServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
						logger.Error().
							Err(err).
							Str("addr", meshAddr).
							Msg("Mesh API server error")
					}
				}()
			} else {
				logger.Warn().Msg("No mesh IP available, skipping mesh server (agent not registered)")
			}

			// Server 2: Bind to localhost (local debugging only).
			localhostAddr := "127.0.0.1:9001"
			localhostServer = &http.Server{
				Addr:              localhostAddr,
				Handler:           httpHandler,
				ReadHeaderTimeout: 30 * time.Second,
			}

			go func() {
				logger.Info().
					Str("addr", localhostAddr).
					Msg("Agent API listening on localhost")

				if err := localhostServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					logger.Error().
						Err(err).
						Str("addr", localhostAddr).
						Msg("Localhost API server error")
				}
			}()

			// Graceful shutdown for both servers.
			defer func() {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				if meshServer != nil {
					if err := meshServer.Shutdown(shutdownCtx); err != nil {
						logger.Error().Err(err).Msg("Failed to shutdown mesh API server")
					}
				}

				if err := localhostServer.Shutdown(shutdownCtx); err != nil {
					logger.Error().Err(err).Msg("Failed to shutdown localhost API server")
				}
			}()

			logger.Info().Msg("Agent started successfully - waiting for shutdown signal")

			// Start discovery loop in background to handle discovery service reconnection.
			// This callback is invoked when discovery succeeds after initially failing.
			go connMgr.StartDiscoveryLoop(ctx, func(discoveredColonyInfo *discoverypb.LookupColonyResponse) {
				logger.Info().
					Str("colony_pubkey", discoveredColonyInfo.Pubkey).
					Msg("Discovery succeeded - configuring mesh and attempting registration")

				// Note: At this point, WireGuard device exists but colony peer isn't configured yet.
				// The mesh configuration and registration will happen through the reconnection loop
				// which will be triggered automatically when state transitions from waiting_discovery
				// to unregistered.
			})

			// Start heartbeat loop in background to keep agent status healthy.
			go connMgr.StartHeartbeatLoop(ctx, 15*time.Second)

			// Start reconnection loop in background to handle colony reconnection.
			go connMgr.StartReconnectionLoop(ctx)

			// Wait for interrupt signal.
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
			sig := <-sigChan

			logger.Info().
				Str("signal", sig.String()).
				Msg("Received shutdown signal - stopping agent")

			cancel() // Stop heartbeat loop
			return nil
		},
	}

	cmd.Flags().StringVar(&configFile, "config", "", "Path to agent configuration file (default: /etc/coral/agent.yaml)")
	cmd.Flags().StringVar(&colonyID, "colony-id", "", "Colony ID to connect to (overrides config file)")
	cmd.Flags().BoolVar(&daemon, "daemon", false, "Run in background (requires PID file support)")
	cmd.Flags().BoolVar(&monitorAll, "monitor-all", false, "Monitor all processes (auto-discovery mode)")
	cmd.Flags().StringArrayVar(&connectService, "connect", []string{}, "Service to connect at startup (format: name:port[:health][:type], can be specified multiple times)")

	return cmd
}

// loadAgentConfig loads agent configuration from file and environment variables.
func loadAgentConfig(
	configFile, colonyIDOverride string,
) (*config.ResolvedConfig, []*ServiceSpec, *config.AgentConfig, error) {
	agentCfg := config.DefaultAgentConfig()
	var serviceSpecs []*ServiceSpec

	// Try to load config file.
	if configFile == "" {
		// Check default locations.
		defaultPaths := []string{
			"./agent.yaml",
			"/etc/coral/agent.yaml",
		}
		for _, path := range defaultPaths {
			if _, err := os.Stat(path); err == nil {
				configFile = path
				break
			}
		}
	}

	if configFile != "" {
		//nolint:gosec // G304: Config file path from command line argument.
		data, err := os.ReadFile(configFile)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to read config file %s: %w", configFile, err)
		}

		if err := yaml.Unmarshal(data, agentCfg); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to parse config file %s: %w", configFile, err)
		}

		// Parse services from config file.
		for _, svc := range agentCfg.Services {
			spec := &ServiceSpec{
				Name:           svc.Name,
				Port:           int32(svc.Port),
				HealthEndpoint: svc.HealthEndpoint,
				ServiceType:    svc.Type,
				Labels:         make(map[string]string),
			}
			serviceSpecs = append(serviceSpecs, spec)
		}
	}

	// Check environment variables (they take precedence).
	envColonyID := os.Getenv("CORAL_COLONY_ID")
	if envColonyID != "" {
		agentCfg.Agent.Colony.ID = envColonyID
	}

	// Apply colony ID override from flag.
	if colonyIDOverride != "" {
		agentCfg.Agent.Colony.ID = colonyIDOverride
	}

	// Parse CORAL_SERVICES environment variable.
	envServices := os.Getenv("CORAL_SERVICES")
	if envServices != "" {
		// Format: name:port[:health][:type],name:port[:health][:type],...
		envSpecs, err := ParseMultipleServiceSpecs(strings.Split(envServices, ","))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to parse CORAL_SERVICES: %w", err)
		}
		// Environment services override config file services.
		serviceSpecs = envSpecs
	}

	// Resolve colony configuration.
	resolver, err := config.NewResolver()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create config resolver: %w", err)
	}

	// Determine colony ID.
	colonyID := agentCfg.Agent.Colony.ID
	if colonyID == "" {
		colonyID, err = resolver.ResolveColonyID()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to resolve colony ID: %w\n\nRun 'coral init <app-name>' or set CORAL_COLONY_ID", err)
		}
	}

	// Load resolved configuration.
	cfg, err := resolver.ResolveConfig(colonyID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to load colony config: %w", err)
	}

	return cfg, serviceSpecs, agentCfg, nil
}

// getSTUNServers determines which STUN servers to use for NAT traversal.
// Priority: env variable > agent config > discovery response > default.
func getSTUNServers(agentCfg *config.AgentConfig, _ *discoverypb.LookupColonyResponse) []string {
	// Check environment variable first
	envSTUN := os.Getenv("CORAL_STUN_SERVERS")
	if envSTUN != "" {
		servers := strings.Split(envSTUN, ",")
		for i := range servers {
			servers[i] = strings.TrimSpace(servers[i])
		}
		return servers
	}

	// Check agent config
	if len(agentCfg.Agent.NAT.STUNServers) > 0 {
		return agentCfg.Agent.NAT.STUNServers
	}

	// Use STUN servers from discovery response (not yet implemented in response)
	// This would be added when colonies register with their STUN servers

	// Fall back to default
	return []string{constants.DefaultSTUNServer}
}

// gatherMeshNetworkInfo collects mesh network debugging information.
func gatherMeshNetworkInfo(
	wgDevice *wireguard.Device,
	meshIP, meshSubnet string,
	colonyInfo *discoverypb.LookupColonyResponse,
	agentID string,
	logger logging.Logger,
) map[string]interface{} {
	info := make(map[string]interface{})

	// Basic mesh info
	info["agent_id"] = agentID
	info["mesh_ip"] = meshIP
	info["mesh_subnet"] = meshSubnet

	// WireGuard interface info
	if wgDevice != nil {
		wgInfo := make(map[string]interface{})
		wgInfo["interface_name"] = wgDevice.InterfaceName()
		wgInfo["listen_port"] = wgDevice.ListenPort()

		// Get interface status
		iface := wgDevice.Interface()
		if iface != nil {
			wgInfo["interface_exists"] = true

			// Try to get IP addresses
			//nolint:gosec // G204: Interface name is from controlled WireGuard device
			if addrs, err := exec.Command("ip", "addr", "show", wgDevice.InterfaceName()).Output(); err == nil {
				wgInfo["ip_addresses"] = string(addrs)
			}

			// Try to get link status
			//nolint:gosec // G204: Diagnostic command with validated interface name.
			if link, err := exec.Command("ip", "link", "show", wgDevice.InterfaceName()).Output(); err == nil {
				wgInfo["link_status"] = string(link)
			}
		} else {
			wgInfo["interface_exists"] = false
		}

		// Get peer information
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

		info["wireguard"] = wgInfo
	}

	// Colony info
	if colonyInfo != nil {
		colonyInfoMap := make(map[string]interface{})
		colonyInfoMap["id"] = colonyInfo.MeshId // Colony ID (same as mesh_id)
		colonyInfoMap["mesh_ipv4"] = colonyInfo.MeshIpv4
		colonyInfoMap["mesh_ipv6"] = colonyInfo.MeshIpv6
		colonyInfoMap["connect_port"] = colonyInfo.ConnectPort
		colonyInfoMap["endpoints"] = colonyInfo.Endpoints

		// Add observed endpoints
		observedEps := make([]map[string]interface{}, 0, len(colonyInfo.ObservedEndpoints))
		for _, ep := range colonyInfo.ObservedEndpoints {
			if ep != nil {
				observedEps = append(observedEps, map[string]interface{}{
					"ip":       ep.Ip,
					"port":     ep.Port,
					"protocol": ep.Protocol,
				})
			}
		}
		colonyInfoMap["observed_endpoints"] = observedEps

		info["colony"] = colonyInfoMap
	}

	// Route information
	if wgDevice != nil && wgDevice.Interface() != nil {
		//nolint:gosec // G204: Diagnostic command with validated interface name.
		routes, err := exec.Command("ip", "route", "show", "dev", wgDevice.InterfaceName()).Output()
		if err == nil {
			info["routes"] = string(routes)
		} else {
			info["routes_error"] = err.Error()
		}

		// Also get all routes for reference
		allRoutes, err := exec.Command("ip", "route", "show").Output()
		if err == nil {
			info["all_routes"] = string(allRoutes)
		}
	}

	// Connectivity test to colony mesh IP
	if colonyInfo != nil && colonyInfo.MeshIpv4 != "" {
		connectPort := colonyInfo.ConnectPort
		if connectPort == 0 {
			connectPort = 9000
		}
		meshAddr := net.JoinHostPort(colonyInfo.MeshIpv4, fmt.Sprintf("%d", connectPort))

		connTest := make(map[string]interface{})
		connTest["target"] = meshAddr

		// Quick connectivity check
		conn, err := net.DialTimeout("tcp", meshAddr, 2*time.Second)
		if err != nil {
			connTest["reachable"] = false
			connTest["error"] = err.Error()
		} else {
			connTest["reachable"] = true
			_ = conn.Close() // TODO: errcheck
		}

		info["colony_connectivity"] = connTest

		// Ping test (if available)
		//nolint:gosec // G204: Diagnostic ping command with validated colony mesh IP.
		if pingOut, err := exec.Command("ping", "-c", "1", "-W", "1", colonyInfo.MeshIpv4).CombinedOutput(); err == nil {
			info["ping_result"] = string(pingOut)
		} else {
			info["ping_error"] = err.Error()
			info["ping_output"] = string(pingOut)
		}
	}

	return info
}

// performAgentPreflightChecks validates agent prerequisites with graceful degradation.
// On Linux, missing capabilities result in warnings allowing reduced functionality.
// On macOS, root is required (returns error if missing).
func performAgentPreflightChecks(logger logging.Logger) error {
	logger.Info().Msg("Running agent preflight checks...")

	var warnings []string
	hasFullCapabilities := true

	// Check if running as root or with sudo
	isRoot := privilege.IsRoot()

	// On macOS, we must run as root (no capability system, no graceful degradation)
	if runtime.GOOS == "darwin" && !isRoot {
		return fmt.Errorf("agent must be run with sudo on macOS:\n  sudo coral agent start\n\n" +
			"macOS requires root privileges for TUN device creation and configuration")
	}

	if !isRoot {
		warnings = append(warnings, "Not running as root - TUN device creation may fail")
		hasFullCapabilities = false
		logger.Warn().Msg("⚠️  Not running with elevated privileges")
	} else {
		logger.Debug().Msg("✓ Running with elevated privileges")

		// Detect original user for privilege context
		if privilege.IsRunningUnderSudo() {
			userCtx, err := privilege.DetectOriginalUser()
			if err != nil {
				logger.Debug().Err(err).Msg("Could not detect original user from sudo")
			} else {
				logger.Debug().
					Str("user", userCtx.Username).
					Int("uid", userCtx.UID).
					Msg("Detected original user from sudo")
			}
		}
	}

	// Detect Linux capabilities (Linux-specific)
	if runtime.GOOS == "linux" {
		caps, err := pkgruntime.DetectLinuxCapabilities()
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to detect Linux capabilities")
			warnings = append(warnings, "Could not detect capabilities - assuming degraded mode")
			hasFullCapabilities = false
		} else {
			logger.Info().Msg("Detected Linux capabilities:")

			// Check required capabilities and report status
			checkCap := func(name string, has bool, required bool, purpose string) {
				status := "✓"
				if !has {
					status = "✗"
					if required {
						warnings = append(warnings, fmt.Sprintf("Missing %s - %s unavailable", name, purpose))
						hasFullCapabilities = false
					}
				}
				logger.Info().Msgf("  %s %s: %s", status, name, purpose)
			}

			checkCap("CAP_NET_ADMIN", caps.CapNetAdmin, true, "TUN device, network config")
			checkCap("CAP_SYS_PTRACE", caps.CapSysPtrace, true, "Process tracing")
			checkCap("CAP_SYS_RESOURCE", caps.CapSysResource, true, "Memory locking for eBPF")
			checkCap("CAP_BPF", caps.CapBpf, false, "eBPF operations (Linux 5.8+)")
			checkCap("CAP_PERFMON", caps.CapPerfmon, false, "CPU profiling (Linux 5.8+)")
			checkCap("CAP_SYSLOG", caps.CapSyslog, false, "Kernel symbols (CPU profiling)")
			checkCap("CAP_SYS_ADMIN", caps.CapSysAdmin, false, "nsenter exec mode + fallback for older kernels")

			// Verify we have eBPF capabilities (either CAP_BPF or CAP_SYS_ADMIN)
			if !caps.CapBpf && !caps.CapSysAdmin {
				warnings = append(warnings, "eBPF requires CAP_BPF or CAP_SYS_ADMIN")
				hasFullCapabilities = false
			}

			// Verify we have perf capabilities (either CAP_PERFMON or CAP_SYS_ADMIN)
			if !caps.CapPerfmon && !caps.CapSysAdmin {
				warnings = append(warnings, "CPU profiling requires CAP_PERFMON or CAP_SYS_ADMIN")
				hasFullCapabilities = false
			}
		}
	} else if isRoot {
		// Non-Linux: just check root
		logger.Info().Msg("Running as root (non-Linux platform)")
		logger.Info().Msg("  ✓ Full privileges available")
	}

	// Report overall status
	if len(warnings) > 0 {
		logger.Warn().Msg("Agent will start with reduced functionality:")
		for _, w := range warnings {
			logger.Warn().Msg("  ⚠️  " + w)
		}
		if runtime.GOOS == "linux" {
			logger.Info().Msg("To enable all capabilities:")
			logger.Info().Msg("  sudo setcap 'cap_net_admin,cap_sys_admin,cap_sys_ptrace,cap_sys_resource,cap_bpf+ep' $(which coral)")
		} else {
			logger.Info().Msg("  Run with: sudo coral agent start")
		}
	}

	if hasFullCapabilities {
		logger.Info().Msg("✓ All required capabilities available")
	} else {
		logger.Info().Msg("⚠️  Starting in degraded mode with available capabilities")
	}

	return nil
}
