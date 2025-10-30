package agent

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	discoverypb "github.com/coral-io/coral/coral/discovery/v1"
	"github.com/coral-io/coral/coral/discovery/v1/discoveryv1connect"
	meshv1 "github.com/coral-io/coral/coral/mesh/v1"
	"github.com/coral-io/coral/coral/mesh/v1/meshv1connect"
	"github.com/coral-io/coral/internal/auth"
	"github.com/coral-io/coral/internal/config"
	"github.com/coral-io/coral/internal/logging"
	"github.com/coral-io/coral/internal/wireguard"
	"github.com/spf13/cobra"
)

// NewConnectCmd creates the connect command for agents
func NewConnectCmd() *cobra.Command {
	var (
		port      int
		colonyID  string
		tags      []string
		healthURL string
	)

	cmd := &cobra.Command{
		Use:   "connect <service-name>",
		Short: "Connect an agent to observe a service",
		Long: `Connect a Coral agent to observe a service or application component.

The agent will:
- Monitor the process health and resource usage
- Detect network connections and dependencies
- Report observations to the colony
- Store recent metrics locally

Example:
  coral connect frontend --port 3000
  coral connect api --port 8080 --tags version=2.1.0,region=us-east
  coral connect database --port 5432 --health http://localhost:5432/health`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			serviceName := args[0]

			// Create resolver
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}

			// Resolve colony ID
			if colonyID == "" {
				colonyID, err = resolver.ResolveColonyID()
				if err != nil {
					return fmt.Errorf("failed to resolve colony: %w\n\nRun 'coral init <app-name>' or set CORAL_COLONY_ID", err)
				}
			}

			// Load resolved configuration
			cfg, err := resolver.ResolveConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			// Initialize logger
			logger := logging.NewWithComponent(logging.Config{
				Level:  "info",
				Pretty: true,
			}, "agent")

			fmt.Printf("Connecting agent for service: %s\n", serviceName)
			fmt.Printf("Port: %d\n", port)
			fmt.Printf("Colony ID: %s\n", cfg.ColonyID)
			fmt.Printf("Application: %s (%s)\n", cfg.ApplicationName, cfg.Environment)
			fmt.Printf("Discovery: %s\n", cfg.DiscoveryURL)

			if len(tags) > 0 {
				fmt.Printf("Tags: %s\n", strings.Join(tags, ", "))
			}

			if healthURL != "" {
				fmt.Printf("Health endpoint: %s\n", healthURL)
			}

			// Step 1: Query discovery service for colony information
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

			// Step 2: Generate WireGuard keys for this agent
			agentKeys, err := auth.GenerateWireGuardKeyPair()
			if err != nil {
				return fmt.Errorf("failed to generate WireGuard keys: %w", err)
			}

			logger.Info().
				Str("agent_pubkey", agentKeys.PublicKey).
				Msg("Generated agent WireGuard keys")

			// Step 3: Create and start WireGuard device
			wgDevice, err := setupAgentWireGuard(agentKeys, colonyInfo, logger)
			if err != nil {
				return fmt.Errorf("failed to setup WireGuard: %w", err)
			}
			defer wgDevice.Stop()

			// Step 4: Register with colony
			agentID := fmt.Sprintf("%s-%s", serviceName, time.Now().Format("20060102-150405"))
			meshIP, err := registerWithColony(cfg, agentID, serviceName, agentKeys.PublicKey, colonyInfo, logger)
			if err != nil {
				return fmt.Errorf("failed to register with colony: %w", err)
			}

			fmt.Println("\n✓ Agent connected successfully")
			fmt.Printf("Agent ID: %s\n", agentID)
			fmt.Printf("Mesh IP: %s\n", meshIP)
			fmt.Printf("Observing: %s:%d\n", serviceName, port)
			fmt.Printf("Authenticated with colony using colony_secret\n")
			fmt.Println("\nPress Ctrl+C to disconnect")

			// Wait for interrupt signal
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
			<-sigChan

			fmt.Println("\n\nDisconnecting agent...")
			return nil
		},
	}

	cmd.Flags().IntVarP(&port, "port", "p", 0, "Service port to observe (required)")
	cmd.Flags().StringVar(&colonyID, "colony-id", "", "Colony ID to connect to (auto-discover if not set)")
	cmd.Flags().StringSliceVarP(&tags, "tags", "t", nil, "Tags for the service (key=value)")
	cmd.Flags().StringVar(&healthURL, "health", "", "Health check endpoint URL")

	cmd.MarkFlagRequired("port")

	return cmd
}

// queryDiscoveryForColony queries the discovery service for colony information.
func queryDiscoveryForColony(cfg *config.ResolvedConfig, logger logging.Logger) (*discoverypb.LookupColonyResponse, error) {
	// Create discovery client
	client := discoveryv1connect.NewDiscoveryServiceClient(
		http.DefaultClient,
		cfg.DiscoveryURL,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Lookup colony by mesh_id (which is the colony_id)
	req := connect.NewRequest(&discoverypb.LookupColonyRequest{
		MeshId: cfg.ColonyID,
	})

	resp, err := client.LookupColony(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("discovery lookup failed: %w", err)
	}

	return resp.Msg, nil
}

// setupAgentWireGuard creates and configures the agent's WireGuard device.
func setupAgentWireGuard(
	agentKeys *auth.WireGuardKeyPair,
	colonyInfo *discoverypb.LookupColonyResponse,
	logger logging.Logger,
) (*wireguard.Device, error) {
	logger.Info().Msg("Setting up WireGuard device for agent")

	// Create WireGuard config for agent
	wgConfig := &config.WireGuardConfig{
		PrivateKey: agentKeys.PrivateKey,
		PublicKey:  agentKeys.PublicKey,
		Port:       0, // Use random port for agent
		MTU:        1420,
	}

	// Create device
	wgDevice, err := wireguard.NewDevice(wgConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create WireGuard device: %w", err)
	}

	// Start device
	if err := wgDevice.Start(); err != nil {
		return nil, fmt.Errorf("failed to start WireGuard device: %w", err)
	}

	logger.Info().
		Str("interface", wgDevice.InterfaceName()).
		Msg("WireGuard device started")

	// Add colony as peer
	// Use the first endpoint from discovery
	var colonyEndpoint string
	if len(colonyInfo.Endpoints) > 0 {
		colonyEndpoint = colonyInfo.Endpoints[0]
	}

	peerConfig := &wireguard.PeerConfig{
		PublicKey:           colonyInfo.Pubkey,
		Endpoint:            colonyEndpoint,
		AllowedIPs:          []string{colonyInfo.MeshIpv4 + "/32"},
		PersistentKeepalive: 25, // Keep NAT mapping alive
	}

	if err := wgDevice.AddPeer(peerConfig); err != nil {
		wgDevice.Stop()
		return nil, fmt.Errorf("failed to add colony as peer: %w", err)
	}

	logger.Info().
		Str("colony_endpoint", colonyEndpoint).
		Str("colony_mesh_ip", colonyInfo.MeshIpv4).
		Msg("Added colony as WireGuard peer")

	return wgDevice, nil
}

// registerWithColony sends a registration request to the colony.
func registerWithColony(
	cfg *config.ResolvedConfig,
	agentID string,
	componentName string,
	agentPubKey string,
	colonyInfo *discoverypb.LookupColonyResponse,
	logger logging.Logger,
) (string, error) {
	logger.Info().
		Str("agent_id", agentID).
		Str("component_name", componentName).
		Msg("Registering with colony")

	// Build mesh service URL using colony mesh IP and connect port
	meshServiceURL := fmt.Sprintf("http://%s:%d", colonyInfo.MeshIpv4, colonyInfo.ConnectPort)

	// Create mesh service client
	client := meshv1connect.NewMeshServiceClient(
		http.DefaultClient,
		meshServiceURL,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build registration request
	req := connect.NewRequest(&meshv1.RegisterRequest{
		AgentId:         agentID,
		ComponentName:   componentName,
		ColonyId:        cfg.ColonyID,
		ColonySecret:    cfg.ColonySecret,
		WireguardPubkey: agentPubKey,
		Version:         "0.1.0",
		Labels:          make(map[string]string),
	})

	// Send registration
	resp, err := client.Register(ctx, req)
	if err != nil {
		return "", fmt.Errorf("registration request failed: %w", err)
	}

	if !resp.Msg.Accepted {
		return "", fmt.Errorf("registration rejected by colony: %s", resp.Msg.Reason)
	}

	logger.Info().
		Str("assigned_ip", resp.Msg.AssignedIp).
		Str("mesh_subnet", resp.Msg.MeshSubnet).
		Int("peer_count", len(resp.Msg.Peers)).
		Msg("Successfully registered with colony")

	return resp.Msg.AssignedIp, nil
}
