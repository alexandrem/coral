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
	meshv1 "github.com/coral-io/coral/coral/mesh/v1"
	"github.com/coral-io/coral/coral/mesh/v1/meshv1connect"
	"github.com/coral-io/coral/internal/config"
	"github.com/coral-io/coral/internal/constants"
	"github.com/coral-io/coral/internal/discovery/registration"
	"github.com/coral-io/coral/internal/logging"
	"github.com/coral-io/coral/internal/wireguard"
	"google.golang.org/protobuf/types/known/timestamppb"
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
				Level:  "info",
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

			// TODO: Implement remaining colony startup tasks
			// - Initialize DuckDB storage at cfg.StoragePath
			// - Start HTTP server for dashboard on cfg.Dashboard.Port

			// Initialize WireGuard device
			wgDevice, err := initializeWireGuard(cfg, logger)
			if err != nil {
				return fmt.Errorf("failed to initialize WireGuard: %w", err)
			}
			defer wgDevice.Stop()

			// Start gRPC/Connect server for agent registration
			meshServer, err := startMeshServer(cfg, wgDevice, logger)
			if err != nil {
				return fmt.Errorf("failed to start mesh server: %w", err)
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

			// Build endpoints list (for now, just the WireGuard port)
			// TODO: Add proper IP detection and multiple endpoints
			endpoints := []string{
				fmt.Sprintf(":%d", cfg.WireGuard.Port),
			}

			metadata := map[string]string{
				"application": cfg.ApplicationName,
				"environment": cfg.Environment,
			}

			// Set default mesh IPs if not configured
			meshIPv4 := colonyConfig.WireGuard.MeshIPv4
			if meshIPv4 == "" {
				meshIPv4 = "10.42.0.1" // Default colony mesh IPv4
			}
			meshIPv6 := colonyConfig.WireGuard.MeshIPv6
			if meshIPv6 == "" {
				meshIPv6 = "fd42::1" // Default colony mesh IPv6
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

			if jsonOutput {
				output := map[string]interface{}{
					"colony_id":    colonyConfig.ColonyID,
					"application":  colonyConfig.ApplicationName,
					"environment":  colonyConfig.Environment,
					"status":       "configured", // TODO: Check if actually running
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

			fmt.Println("Status: Colony is configured (not running)")
			fmt.Println()
			fmt.Println("To start the colony:")
			fmt.Println("  coral colony start")
			fmt.Println()
			fmt.Println("Note: Connected peers are only visible when colony is running.")

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
					ColonyID    string `json:"colony_id"`
					Application string `json:"application"`
					Environment string `json:"environment"`
					IsDefault   bool   `json:"is_default"`
					CreatedAt   string `json:"created_at"`
					StoragePath string `json:"storage_path"`
				}

				colonies := []colonyInfo{}
				for _, id := range colonyIDs {
					cfg, err := loader.LoadColonyConfig(id)
					if err != nil {
						continue
					}
					colonies = append(colonies, colonyInfo{
						ColonyID:    cfg.ColonyID,
						Application: cfg.ApplicationName,
						Environment: cfg.Environment,
						IsDefault:   cfg.ColonyID == globalConfig.DefaultColony,
						CreatedAt:   cfg.CreatedAt.Format(time.RFC3339),
						StoragePath: cfg.StoragePath,
					})
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

				fmt.Printf("  %s (%s)%s\n", cfg.ColonyID, cfg.Environment, defaultMarker)
				fmt.Printf("    Application: %s\n", cfg.ApplicationName)
				fmt.Printf("    Created: %s\n", cfg.CreatedAt.Format("2006-01-02 15:04:05"))
				fmt.Printf("    Storage: %s\n", cfg.StoragePath)
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

// startMeshServer starts the HTTP/Connect server for agent registration.
func startMeshServer(cfg *config.ResolvedConfig, wgDevice *wireguard.Device, logger logging.Logger) (*http.Server, error) {
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

	// Create mesh service handler
	meshHandler := &meshServiceHandler{
		cfg:      cfg,
		wgDevice: wgDevice,
		logger:   logger,
	}

	// Register the handler
	path, handler := meshv1connect.NewMeshServiceHandler(meshHandler)

	// Create HTTP server
	mux := http.NewServeMux()
	mux.Handle(path, handler)

	addr := fmt.Sprintf(":%d", connectPort)
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Start server in background
	go func() {
		logger.Info().
			Int("port", connectPort).
			Msg("Mesh service listening for agent connections")

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error().
				Err(err).
				Msg("Mesh server error")
		}
	}()

	return server, nil
}

// meshServiceHandler implements the MeshService RPC handler.
type meshServiceHandler struct {
	cfg      *config.ResolvedConfig
	wgDevice *wireguard.Device
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
