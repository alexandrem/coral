package agent

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	discoverypb "github.com/coral-io/coral/coral/discovery/v1"
	"github.com/coral-io/coral/coral/discovery/v1/discoveryv1connect"
	meshv1 "github.com/coral-io/coral/coral/mesh/v1"
	"github.com/coral-io/coral/coral/mesh/v1/meshv1connect"
	"github.com/coral-io/coral/internal/agent"
	"github.com/coral-io/coral/internal/auth"
	"github.com/coral-io/coral/internal/config"
	"github.com/coral-io/coral/internal/constants"
	"github.com/coral-io/coral/internal/logging"
	"github.com/coral-io/coral/internal/wireguard"
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
			meshIP := net.ParseIP(meshIPStr)
			if meshIP == nil {
				return fmt.Errorf("invalid mesh IP from colony: %s", meshIPStr)
			}

			_, meshSubnet, err := net.ParseCIDR(meshSubnetStr)
			if err != nil {
				return fmt.Errorf("invalid mesh subnet from colony: %w", err)
			}

			logger.Info().
				Str("interface", wgDevice.InterfaceName()).
				Str("ip", meshIP.String()).
				Str("subnet", meshSubnet.String()).
				Msg("Assigning IP address to agent WireGuard interface")

			iface := wgDevice.Interface()
			if iface == nil {
				return fmt.Errorf("WireGuard device has no interface")
			}

			if err := iface.AssignIP(meshIP, meshSubnet); err != nil {
				return fmt.Errorf("failed to assign IP to agent interface: %w", err)
			}

			logger.Info().
				Str("interface", wgDevice.InterfaceName()).
				Str("ip", meshIP.String()).
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

			// Wait for changes to propagate
			time.Sleep(300 * time.Millisecond)

			fmt.Println("\n✓ Agent connected successfully")
			fmt.Printf("Agent ID: %s\n", agentID)
			fmt.Printf("Mesh IP: %s\n", meshIPStr)

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
		Port:       -1, // Use ephemeral port for agent
		MTU:        constants.DefaultWireGuardMTU,
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

	// Select a discovery endpoint for establishing the WireGuard peer.
	var colonyEndpoint string
	for _, ep := range colonyInfo.Endpoints {
		if ep == "" {
			continue
		}

		host, port, err := net.SplitHostPort(ep)
		if err != nil {
			logger.Warn().Err(err).Str("endpoint", ep).Msg("Invalid colony endpoint from discovery")
			continue
		}

		if host == "" {
			logger.Warn().Str("endpoint", ep).Msg("Skipping discovery endpoint without host")
			continue
		}

		colonyEndpoint = net.JoinHostPort(host, port)
		break
	}

	if colonyEndpoint == "" {
		return nil, fmt.Errorf("discovery did not provide a usable WireGuard endpoint")
	}

	allowedIPs := make([]string, 0, 2)
	if colonyInfo.MeshIpv4 != "" {
		allowedIPs = append(allowedIPs, colonyInfo.MeshIpv4+"/32")
	}
	if colonyInfo.MeshIpv6 != "" {
		allowedIPs = append(allowedIPs, colonyInfo.MeshIpv6+"/128")
	}

	if len(allowedIPs) == 0 {
		return nil, fmt.Errorf("discovery response missing mesh IPs; unable to configure WireGuard peer")
	}

	peerConfig := &wireguard.PeerConfig{
		PublicKey:           colonyInfo.Pubkey,
		Endpoint:            colonyEndpoint,
		AllowedIPs:          allowedIPs,
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

	// Assign a temporary IP to the agent interface so it can communicate with the colony.
	// We'll use a temporary IP in the high range (.254) which will be updated after registration.
	// Parse the colony's mesh network to determine the subnet.
	if colonyInfo.MeshIpv4 != "" {
		// Extract subnet from colony's mesh IP (assuming it's in the same subnet)
		// For now, we'll use a hardcoded temporary IP in the standard range.
		// TODO: Make this more robust by getting the subnet from discovery or config.
		tempIP := net.ParseIP("10.42.255.254") // Temporary IP in high range
		_, meshSubnet, err := net.ParseCIDR("10.42.0.0/16")
		if err == nil {
			logger.Info().
				Str("interface", wgDevice.InterfaceName()).
				Str("temp_ip", tempIP.String()).
				Msg("Assigning temporary IP to agent interface for initial registration")

			iface := wgDevice.Interface()
			if iface != nil {
				if err := iface.AssignIP(tempIP, meshSubnet); err != nil {
					logger.Warn().Err(err).Msg("Failed to assign temporary IP to agent interface")
				} else {
					logger.Info().
						Str("interface", wgDevice.InterfaceName()).
						Str("temp_ip", tempIP.String()).
						Msg("Temporary IP assigned successfully")
				}
			}
		}
	}

	// Wait for WireGuard tunnel to establish and routes to be configured.
	// This gives the kernel time to set up the interface and routing.
	logger.Info().Msg("Waiting for WireGuard tunnel to establish...")
	time.Sleep(2 * time.Second)

	// Verify mesh IP is reachable via TCP connection test.
	if colonyInfo.MeshIpv4 != "" {
		connectPort := colonyInfo.ConnectPort
		if connectPort == 0 {
			connectPort = 9000 // Default connect port
		}

		meshAddr := net.JoinHostPort(colonyInfo.MeshIpv4, fmt.Sprintf("%d", connectPort))
		logger.Info().
			Str("mesh_addr", meshAddr).
			Msg("Testing connectivity to colony via mesh")

		// Try to establish TCP connection to verify tunnel is working
		conn, err := net.DialTimeout("tcp", meshAddr, 3*time.Second)
		if err != nil {
			logger.Warn().
				Err(err).
				Str("mesh_addr", meshAddr).
				Msg("Unable to reach colony via mesh IP - tunnel may not be fully established")
			// Don't fail here - registration will retry anyway
		} else {
			conn.Close()
			logger.Info().
				Str("mesh_addr", meshAddr).
				Msg("Successfully verified connectivity to colony via WireGuard mesh")
		}
	}

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

	connectPort := colonyInfo.ConnectPort
	if connectPort == 0 {
		connectPort = 9000
	}

	candidateURLs := buildMeshServiceURLs(colonyInfo, connectPort)
	logger.Debug().
		Strs("candidate_urls", candidateURLs).
		Msg("Prepared colony registration endpoints")

	if len(candidateURLs) == 0 {
		return "", fmt.Errorf("registration request failed: discovery did not provide mesh connectivity information")
	}

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

	var lastErr error
	var attemptErrors []string
	for _, baseURL := range candidateURLs {
		client := meshv1connect.NewMeshServiceClient(http.DefaultClient, baseURL)

		for attempt := 1; attempt <= 3; attempt++ {
			logger.Info().
				Str("agent_id", agentID).
				Str("endpoint", baseURL).
				Int("attempt", attempt).
				Msg("Attempting colony registration")

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			resp, err := client.Register(ctx, connect.NewRequest(regReq))
			cancel()

			if err != nil {
				logger.Warn().
					Err(err).
					Str("endpoint", baseURL).
					Int("attempt", attempt).
					Msg("Colony registration attempt failed")

				lastErr = err
				attemptErrors = append(attemptErrors, fmt.Sprintf("%s attempt %d: %v", baseURL, attempt, err))

				if attempt < 3 {
					time.Sleep(time.Duration(attempt) * time.Second)
				}
				continue
			}

			if !resp.Msg.Accepted {
				lastErr = fmt.Errorf("registration rejected by colony: %s", resp.Msg.Reason)
				logger.Warn().
					Str("endpoint", baseURL).
					Int("attempt", attempt).
					Msg(lastErr.Error())

				attemptErrors = append(attemptErrors, fmt.Sprintf("%s attempt %d: %s", baseURL, attempt, resp.Msg.Reason))
				if attempt < 3 {
					time.Sleep(time.Duration(attempt) * time.Second)
				}
				continue
			}

			logger.Info().
				Str("assigned_ip", resp.Msg.AssignedIp).
				Str("mesh_subnet", resp.Msg.MeshSubnet).
				Int("peer_count", len(resp.Msg.Peers)).
				Msg("Successfully registered with colony")

			// Return both IP and subnet for interface configuration
			result := fmt.Sprintf("%s|%s", resp.Msg.AssignedIp, resp.Msg.MeshSubnet)
			return result, nil
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no registration endpoints available")
	}

	if len(attemptErrors) > 0 {
		return "", fmt.Errorf("registration attempts exhausted: %w (attempts: %s)", lastErr, strings.Join(attemptErrors, "; "))
	}

	return "", fmt.Errorf("registration attempts exhausted: %w", lastErr)
}

// buildMeshServiceURLs returns candidate URLs for contacting the colony's mesh service.
//
// WireGuard Bootstrap Problem:
//   - Agent needs to register to become a WireGuard peer
//   - But agent can't reach colony through mesh until it's a peer
//   - Solution: Initial registration uses the discovery endpoint host,
//     then after registration all communication goes through mesh IPs
func buildMeshServiceURLs(colonyInfo *discoverypb.LookupColonyResponse, connectPort uint32) []string {
	seen := make(map[string]struct{})
	var candidates []string

	add := func(host string) {
		if host == "" {
			return
		}
		url := fmt.Sprintf("http://%s", net.JoinHostPort(host, fmt.Sprintf("%d", connectPort)))
		if _, exists := seen[url]; exists {
			return
		}
		seen[url] = struct{}{}
		candidates = append(candidates, url)
	}

	// Extract host from discovery endpoint for bootstrap registration.
	// This allows the agent to reach the colony before the WireGuard tunnel is established.
	for _, ep := range colonyInfo.Endpoints {
		if ep != "" {
			if host, _, err := net.SplitHostPort(ep); err == nil {
				add(host) // Use same host as WireGuard endpoint for initial registration
			}
		}
	}

	// Also try mesh IPs in case this is a re-registration with tunnel already established.
	add(colonyInfo.MeshIpv4)
	add(colonyInfo.MeshIpv6)

	return candidates
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

// generateAgentID generates a stable agent ID based on hostname and service specs.
// The ID remains consistent across agent restarts to maintain identity in the colony.
func generateAgentID(serviceSpecs []*ServiceSpec) string {
	// Get hostname for stable identification
	hostname, err := os.Hostname()
	if err != nil {
		// Fallback to "unknown" if hostname cannot be determined
		hostname = "unknown"
	}

	// Sanitize hostname: replace dots and underscores with hyphens
	hostname = strings.ReplaceAll(hostname, ".", "-")
	hostname = strings.ReplaceAll(hostname, "_", "-")
	hostname = strings.ToLower(hostname)

	if len(serviceSpecs) == 1 {
		// Single service: hostname-servicename
		// Example: "myserver-frontend", "myserver-api"
		return fmt.Sprintf("%s-%s", hostname, serviceSpecs[0].Name)
	}

	if len(serviceSpecs) > 1 {
		// Multi-service: hostname-multi
		// Example: "myserver-multi" for an agent monitoring multiple services
		return fmt.Sprintf("%s-multi", hostname)
	}

	// No services (daemon mode): just hostname
	// Example: "myserver" for a standalone agent
	return hostname
}
