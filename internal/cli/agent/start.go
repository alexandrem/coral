package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/coral-io/coral/coral/agent/v1/agentv1connect"
	discoverypb "github.com/coral-io/coral/coral/discovery/v1"
	meshv1 "github.com/coral-io/coral/coral/mesh/v1"
	"github.com/coral-io/coral/internal/agent"
	"github.com/coral-io/coral/internal/auth"
	"github.com/coral-io/coral/internal/config"
	"github.com/coral-io/coral/internal/constants"
	"github.com/coral-io/coral/internal/logging"
	"github.com/coral-io/coral/internal/wireguard"
)

// AgentConfig represents the agent configuration file.
type AgentConfig struct {
	Agent struct {
		Runtime string `yaml:"runtime"` // auto, native, docker, kubernetes
		Colony  struct {
			ID           string `yaml:"id"`
			AutoDiscover bool   `yaml:"auto_discover"`
		} `yaml:"colony"`
		NAT struct {
			STUNServers []string `yaml:"stun_servers,omitempty"` // STUN servers for NAT traversal
			EnableRelay bool     `yaml:"enable_relay,omitempty"` // Enable relay fallback
		} `yaml:"nat,omitempty"`
	} `yaml:"agent"`
	Services []struct {
		Name           string `yaml:"name"`
		Port           int    `yaml:"port"`
		HealthEndpoint string `yaml:"health_endpoint,omitempty"`
		Type           string `yaml:"type,omitempty"`
	} `yaml:"services"`
}

// NewStartCmd creates the start command for agents.
func NewStartCmd() *cobra.Command {
	var (
		configFile string
		colonyID   string
		daemon     bool
		monitorAll bool
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

  # With config file
  coral agent start --config /etc/coral/agent.yaml

  # With environment variables
  CORAL_COLONY_ID=prod CORAL_SERVICES=api:8080:/health coral agent start

  # Monitor all processes (auto-discovery)
  coral agent start --monitor-all

  # Development mode (pretty logging)
  coral agent start --config ./agent.yaml --log-format=pretty`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load configuration
			cfg, serviceSpecs, agentCfg, err := loadAgentConfig(configFile, colonyID)
			if err != nil {
				return fmt.Errorf("failed to load agent configuration: %w", err)
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

			// Initialize logger with specified format.
			logger := logging.NewWithComponent(logging.Config{
				Level:  "debug",
				Pretty: true,
			}, "agent")

			logger.Info().
				Str("colony_id", cfg.ColonyID).
				Int("service_count", len(serviceSpecs)).
				Str("runtime", "auto-detect").
				Str("mode", agentMode).
				Msg("Starting Coral agent")

			if agentMode == "passive" {
				logger.Info().Msg("Agent running in passive mode - use 'coral connect' to attach services")
			} else if agentMode == "monitor-all" {
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

			colonyInfo, err := queryDiscoveryForColony(cfg, logger)
			if err != nil {
				return fmt.Errorf("failed to query discovery service: %w", err)
			}

			logger.Info().
				Str("colony_pubkey", colonyInfo.Pubkey).
				Strs("endpoints", colonyInfo.Endpoints).
				Msg("Received colony information from discovery")

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

			// Create and start WireGuard device.
			wgDevice, err := setupAgentWireGuard(agentKeys, colonyInfo, stunServers, enableRelay, logger)
			if err != nil {
				return fmt.Errorf("failed to setup WireGuard: %w", err)
			}
			defer wgDevice.Stop()

			// Generate agent ID early so we can use it for registration
			agentID := generateAgentID(serviceSpecs)

			// Discover agent's public endpoint via STUN (if configured)
			var agentObservedEndpoint *discoverypb.Endpoint
			if len(stunServers) > 0 {
				localPort := wgDevice.ListenPort()
				agentObservedEndpoint = wireguard.DiscoverPublicEndpoint(stunServers, localPort, logger)
			}

			// Register agent with discovery service
			if err := registerAgentWithDiscovery(cfg, agentID, agentKeys.PublicKey, agentObservedEndpoint, logger); err != nil {
				logger.Warn().Err(err).Msg("Failed to register agent with discovery service (continuing anyway)")
			}

			// Register with colony.
			registrationResult, err := registerWithColony(cfg, agentID, serviceSpecs, agentKeys.PublicKey, colonyInfo, logger)
			if err != nil {
				return fmt.Errorf("failed to register with colony: %w", err)
			}

			// Parse registration result (format: "IP|SUBNET")
			parts := strings.Split(registrationResult, "|")
			if len(parts) != 2 {
				return fmt.Errorf("invalid registration response format")
			}
			meshIPStr := parts[0]
			meshSubnetStr := parts[1]

			// Assign IP to the agent's WireGuard interface
			logger.Info().
				Str("interface", wgDevice.InterfaceName()).
				Str("ip", meshIPStr).
				Str("subnet", meshSubnetStr).
				Msg("Assigning IP address to agent WireGuard interface")

			// Parse IP and subnet for interface assignment
			meshIP := net.ParseIP(meshIPStr)
			if meshIP == nil {
				return fmt.Errorf("invalid mesh IP from colony: %s", meshIPStr)
			}

			_, meshSubnet, err := net.ParseCIDR(meshSubnetStr)
			if err != nil {
				return fmt.Errorf("invalid mesh subnet from colony: %w", err)
			}

			iface := wgDevice.Interface()
			if iface == nil {
				return fmt.Errorf("WireGuard device has no interface")
			}

			if err := iface.AssignIP(meshIP, meshSubnet); err != nil {
				return fmt.Errorf("failed to assign IP to agent interface: %w", err)
			}

			logger.Info().
				Str("interface", wgDevice.InterfaceName()).
				Str("ip", meshIPStr).
				Msg("Successfully assigned IP to agent WireGuard interface")

			// Delete all existing routes for this interface to clear cached source IPs.
			// When we used a temporary IP, the kernel cached it as the source address.
			logger.Info().Msg("Flushing routes to clear temporary IP cache")
			if err := wgDevice.FlushAllPeerRoutes(); err != nil {
				logger.Warn().Err(err).Msg("Failed to flush peer routes")
			}

			// Wait for route deletion to complete.
			time.Sleep(200 * time.Millisecond)

			// Re-add peer routes with the new IP as source.
			if err := wgDevice.RefreshPeerRoutes(); err != nil {
				logger.Warn().Err(err).Msg("Failed to refresh peer routes after IP change")
			}

			// Wait briefly for IP and route changes to propagate through the kernel.
			// Without this delay, connection attempts may fail with "can't assign requested address".
			time.Sleep(500 * time.Millisecond)

			// Trigger WireGuard handshake by attempting to connect to colony over mesh.
			// This ensures the tunnel is established before we try to send heartbeats.
			connectPort := colonyInfo.ConnectPort
			if connectPort == 0 {
				connectPort = 9000
			}
			meshAddr := net.JoinHostPort(colonyInfo.MeshIpv4, fmt.Sprintf("%d", connectPort))
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
				conn.Close()
				logger.Info().
					Str("mesh_addr", meshAddr).
					Msg("Successfully established WireGuard tunnel to colony")
			}

			logger.Info().
				Str("agent_id", agentID).
				Str("mesh_ip", meshIPStr).
				Int("service_count", len(serviceSpecs)).
				Msg("Agent connected successfully")

			// Start agent instance (always created, even in passive mode).
			serviceInfos := make([]*meshv1.ServiceInfo, len(serviceSpecs))
			for i, spec := range serviceSpecs {
				serviceInfos[i] = spec.ToProto()
			}

			agentInstance, err := agent.New(agent.Config{
				AgentID:  agentID,
				Services: serviceInfos,
				Logger:   logger,
			})
			if err != nil {
				return fmt.Errorf("failed to create agent: %w", err)
			}

			if err := agentInstance.Start(); err != nil {
				return fmt.Errorf("failed to start agent: %w", err)
			}
			defer agentInstance.Stop()

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

			// Create and start runtime service for status API.
			runtimeService, err := agent.NewRuntimeService(agent.RuntimeServiceConfig{
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
			defer runtimeService.Stop()

			// Create service handler and HTTP server for gRPC API.
			serviceHandler := agent.NewServiceHandler(agentInstance, runtimeService)
			path, handler := agentv1connect.NewAgentServiceHandler(serviceHandler)

			mux := http.NewServeMux()
			mux.Handle(path, handler)

			// Add simple /status endpoint that returns JSON.
			mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
				ctx := r.Context()
				runtimeCtx, err := runtimeService.GetRuntimeContext(ctx, nil)
				if err != nil {
					http.Error(w, fmt.Sprintf("failed to get runtime context: %v", err), http.StatusInternalServerError)
					return
				}

				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(runtimeCtx); err != nil {
					logger.Error().Err(err).Msg("Failed to encode status response")
				}
			})

			httpServer := &http.Server{
				Addr:    ":9001",
				Handler: mux,
			}

			// Start HTTP server in background.
			go func() {
				logger.Info().
					Int("port", 9001).
					Msg("Agent status API listening")

				if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					logger.Error().
						Err(err).
						Msg("Status API server error")
				}
			}()
			defer func() {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := httpServer.Shutdown(shutdownCtx); err != nil {
					logger.Error().Err(err).Msg("Failed to shutdown status API server")
				}
			}()

			logger.Info().Msg("Agent started successfully - waiting for shutdown signal")

			// Start heartbeat loop in background to keep agent status healthy
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			go startHeartbeatLoop(ctx, agentID, colonyInfo.MeshIpv4, colonyInfo.ConnectPort, 15*time.Second, logger)

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

	return cmd
}

// loadAgentConfig loads agent configuration from file and environment variables.
func loadAgentConfig(configFile, colonyIDOverride string) (*config.ResolvedConfig, []*ServiceSpec, *AgentConfig, error) {
	agentCfg := &AgentConfig{}
	var serviceSpecs []*ServiceSpec

	// Try to load config file.
	if configFile == "" {
		// Check default locations.
		defaultPaths := []string{
			"/etc/coral/agent.yaml",
			"./agent.yaml",
		}
		for _, path := range defaultPaths {
			if _, err := os.Stat(path); err == nil {
				configFile = path
				break
			}
		}
	}

	if configFile != "" {
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
func getSTUNServers(agentCfg *AgentConfig, colonyInfo *discoverypb.LookupColonyResponse) []string {
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
