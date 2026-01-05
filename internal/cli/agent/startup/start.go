package startup

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	discoverypb "github.com/coral-mesh/coral/coral/discovery/v1"
	"github.com/coral-mesh/coral/internal/logging"
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
			// Initialize logger early for preflight checks.
			logger := logging.NewWithComponent(logging.Config{
				Level:  "debug",
				Pretty: true,
			}, "agent")

			// Create and configure builder.
			builder := NewAgentServerBuilder(
				cmd.Context(),
				logger,
				configFile,
				colonyID,
				connectService,
				monitorAll,
			)

			// Phase 1: Validate (preflight + config).
			if err := builder.Validate(); err != nil {
				return err
			}

			// Phase 2: Initialize network (WireGuard, STUN, discovery).
			if err := builder.InitializeNetwork(); err != nil {
				return err
			}

			// Phase 3: Initialize storage (DuckDB, function cache).
			if err := builder.InitializeStorage(); err != nil {
				return err
			}

			// Phase 4: Create agent instance.
			if err := builder.CreateAgentInstance(); err != nil {
				return err
			}

			// Phase 5: Register with colony.
			if err := builder.RegisterWithColony(); err != nil {
				return err
			}

			// Phase 6: Register services (handlers, servers).
			if err := builder.RegisterServices(); err != nil {
				return err
			}

			// Build final server.
			server := builder.Build()
			defer func() {
				if err := server.Stop(); err != nil {
					logger.Error().Err(err).Msg("Error stopping agent server")
				}
			}()

			logger.Info().Msg("Agent started successfully - waiting for shutdown signal")

			// Start background loops.
			ctx := server.ServicesResult.Context
			go server.ConnectionManager.StartDiscoveryLoop(ctx, func(discoveredColonyInfo *discoverypb.LookupColonyResponse) {
				logger.Info().
					Str("colony_pubkey", discoveredColonyInfo.Pubkey).
					Msg("Discovery succeeded - configuring mesh and attempting registration")
			})

			go server.ConnectionManager.StartHeartbeatLoop(ctx, 15*time.Second)
			go server.ConnectionManager.StartReconnectionLoop(ctx)

			// Wait for interrupt signal.
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
			sig := <-sigChan

			logger.Info().
				Str("signal", sig.String()).
				Msg("Received shutdown signal - stopping agent")

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
