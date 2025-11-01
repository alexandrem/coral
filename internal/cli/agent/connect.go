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
	"github.com/coral-io/coral/internal/agent"
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
		Use:   "connect <service-spec>...",
		Short: "Connect an agent to observe one or more services",
		Long: `Connect a Coral agent to observe services or application components.

The agent will:
- Monitor the process health and resource usage
- Detect network connections and dependencies
- Report observations to the colony
- Store recent metrics locally

Service Specification Format:
  name:port[:health][:type]
  - name: Service/component name (alphanumeric + hyphens)
  - port: TCP port number (1-65535)
  - health: Optional health check endpoint path (e.g., /health)
  - type: Optional service type hint (e.g., http, redis, postgres)

Examples:
  # Single service (new syntax)
  coral connect frontend:3000
  coral connect api:8080:/health:http

  # Single service (legacy syntax, backward compatible)
  coral connect frontend --port 3000 --health /health

  # Multiple services (RFD 011)
  coral connect frontend:3000:/health redis:6379 metrics:9090:/metrics
  coral connect api:8080:/health:http cache:6379::redis worker:9000

Note: Legacy flags (--port, --health) only work with single service specifications.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse service specifications
			serviceSpecs, err := parseServiceSpecsWithLegacySupport(args, port, healthURL)
			if err != nil {
				return err
			}

			// Validate service specs
			if err := ValidateServiceSpecs(serviceSpecs); err != nil {
				return fmt.Errorf("invalid service configuration: %w", err)
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

			// Display connection information
			if len(serviceSpecs) == 1 {
				fmt.Printf("Connecting agent for service: %s\n", serviceSpecs[0].Name)
				fmt.Printf("Port: %d\n", serviceSpecs[0].Port)
				if serviceSpecs[0].HealthEndpoint != "" {
					fmt.Printf("Health endpoint: %s\n", serviceSpecs[0].HealthEndpoint)
				}
				if serviceSpecs[0].ServiceType != "" {
					fmt.Printf("Service type: %s\n", serviceSpecs[0].ServiceType)
				}
			} else {
				fmt.Printf("Connecting agent for %d services:\n", len(serviceSpecs))
				for _, spec := range serviceSpecs {
					fmt.Printf("  • %s (port %d", spec.Name, spec.Port)
					if spec.HealthEndpoint != "" {
						fmt.Printf(", health: %s", spec.HealthEndpoint)
					}
					if spec.ServiceType != "" {
						fmt.Printf(", type: %s", spec.ServiceType)
					}
					fmt.Printf(")\n")
				}
			}

			fmt.Printf("Colony ID: %s\n", cfg.ColonyID)
			fmt.Printf("Application: %s (%s)\n", cfg.ApplicationName, cfg.Environment)
			fmt.Printf("Discovery: %s\n", cfg.DiscoveryURL)

			if len(tags) > 0 {
				fmt.Printf("Tags: %s\n", strings.Join(tags, ", "))
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
			agentID := generateAgentID(serviceSpecs)
			meshIP, err := registerWithColony(cfg, agentID, serviceSpecs, agentKeys.PublicKey, colonyInfo, logger)
			if err != nil {
				return fmt.Errorf("failed to register with colony: %w", err)
			}

			fmt.Println("\n✓ Agent connected successfully")
			fmt.Printf("Agent ID: %s\n", agentID)
			fmt.Printf("Mesh IP: %s\n", meshIP)

			if len(serviceSpecs) == 1 {
				fmt.Printf("Observing: %s:%d\n", serviceSpecs[0].Name, serviceSpecs[0].Port)
			} else {
				fmt.Printf("Observing %d services\n", len(serviceSpecs))
			}

			fmt.Printf("Authenticated with colony using colony_secret\n")

			// Step 5: Start agent to monitor services
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

			// Display initial status
			fmt.Printf("\nAgent Status: %s\n", agentInstance.GetStatus())
			for name, status := range agentInstance.GetServiceStatuses() {
				fmt.Printf("  • %s: %s\n", name, status.Status)
			}

			fmt.Println("\nPress Ctrl+C to disconnect")

			// Wait for interrupt signal
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
			<-sigChan

			fmt.Println("\n\nDisconnecting agent...")
			return nil
		},
	}

	cmd.Flags().IntVarP(&port, "port", "p", 0, "Service port (legacy, only works with single service)")
	cmd.Flags().StringVar(&colonyID, "colony-id", "", "Colony ID to connect to (auto-discover if not set)")
	cmd.Flags().StringSliceVarP(&tags, "tags", "t", nil, "Tags for the service (key=value)")
	cmd.Flags().StringVar(&healthURL, "health", "", "Health check endpoint (legacy, only works with single service)")

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
	serviceSpecs []*ServiceSpec,
	agentPubKey string,
	colonyInfo *discoverypb.LookupColonyResponse,
	logger logging.Logger,
) (string, error) {
	logger.Info().
		Str("agent_id", agentID).
		Int("service_count", len(serviceSpecs)).
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

	// Convert service specs to protobuf ServiceInfo messages
	services := make([]*meshv1.ServiceInfo, len(serviceSpecs))
	for i, spec := range serviceSpecs {
		services[i] = spec.ToProto()
	}

	// Build registration request with multi-service support
	regReq := &meshv1.RegisterRequest{
		AgentId:         agentID,
		ColonyId:        cfg.ColonyID,
		ColonySecret:    cfg.ColonySecret,
		WireguardPubkey: agentPubKey,
		Version:         "0.1.0",
		Labels:          make(map[string]string),
		Services:        services,
	}

	// For backward compatibility, also set ComponentName if single service
	if len(serviceSpecs) == 1 {
		regReq.ComponentName = serviceSpecs[0].Name
	}

	req := connect.NewRequest(regReq)

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

// parseServiceSpecsWithLegacySupport parses service specs with backward compatibility.
func parseServiceSpecsWithLegacySupport(args []string, legacyPort int, legacyHealth string) ([]*ServiceSpec, error) {
	// Check if using new syntax (contains colon) or legacy syntax
	hasColonSyntax := false
	for _, arg := range args {
		if strings.Contains(arg, ":") {
			hasColonSyntax = true
			break
		}
	}

	// New syntax: parse service specs directly
	if hasColonSyntax {
		// If new syntax is used, legacy flags should not be set
		if legacyPort > 0 || legacyHealth != "" {
			return nil, fmt.Errorf("cannot use --port or --health flags with new service spec syntax (name:port[:health][:type])")
		}
		return ParseMultipleServiceSpecs(args)
	}

	// Legacy syntax: single service with --port flag required
	if len(args) > 1 {
		return nil, fmt.Errorf("multiple services require new syntax (e.g., 'coral connect frontend:3000 redis:6379')")
	}

	if legacyPort == 0 {
		return nil, fmt.Errorf("--port flag is required when using legacy syntax (or use new syntax: 'coral connect %s:PORT')", args[0])
	}

	// Build service spec from legacy format
	serviceName := args[0]
	spec := &ServiceSpec{
		Name:   serviceName,
		Port:   int32(legacyPort),
		Labels: make(map[string]string),
	}

	// Add health endpoint if provided
	if legacyHealth != "" {
		// Ensure it starts with /
		if !strings.HasPrefix(legacyHealth, "/") {
			spec.HealthEndpoint = "/" + legacyHealth
		} else {
			spec.HealthEndpoint = legacyHealth
		}
	}

	// Validate the service name
	if !serviceNameRegex.MatchString(serviceName) {
		return nil, fmt.Errorf("invalid service name '%s': must be alphanumeric with hyphens", serviceName)
	}

	return []*ServiceSpec{spec}, nil
}

// generateAgentID generates an agent ID based on service specs.
func generateAgentID(serviceSpecs []*ServiceSpec) string {
	timestamp := time.Now().Format("20060102-150405")

	if len(serviceSpecs) == 1 {
		// Single service: use service name in ID
		return fmt.Sprintf("%s-%s", serviceSpecs[0].Name, timestamp)
	}

	// Multi-service: use generic name with service count
	return fmt.Sprintf("agent-%dsvcs-%s", len(serviceSpecs), timestamp)
}
