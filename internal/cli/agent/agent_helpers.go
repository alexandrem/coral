package agent

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"

	discoverypb "github.com/coral-io/coral/coral/discovery/v1"
	"github.com/coral-io/coral/coral/discovery/v1/discoveryv1connect"
	meshv1 "github.com/coral-io/coral/coral/mesh/v1"
	"github.com/coral-io/coral/coral/mesh/v1/meshv1connect"
	"github.com/coral-io/coral/internal/agent/ebpf"
	"github.com/coral-io/coral/internal/auth"
	"github.com/coral-io/coral/internal/config"
	"github.com/coral-io/coral/internal/constants"
	"github.com/coral-io/coral/internal/logging"
	"github.com/coral-io/coral/internal/wireguard"
)

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

// registerAgentWithDiscovery registers the agent with the discovery service.
func registerAgentWithDiscovery(
	cfg *config.ResolvedConfig,
	agentID string,
	agentPubKey string,
	observedEndpoint *discoverypb.Endpoint,
	logger logging.Logger,
) error {
	// Create discovery client
	client := discoveryv1connect.NewDiscoveryServiceClient(
		http.DefaultClient,
		cfg.DiscoveryURL,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Register agent
	req := connect.NewRequest(&discoverypb.RegisterAgentRequest{
		AgentId:          agentID,
		MeshId:           cfg.ColonyID,
		Pubkey:           agentPubKey,
		Endpoints:        []string{}, // Agents typically don't have static endpoints
		ObservedEndpoint: observedEndpoint,
		Metadata:         make(map[string]string),
	})

	resp, err := client.RegisterAgent(ctx, req)
	if err != nil {
		return fmt.Errorf("agent registration with discovery failed: %w", err)
	}

	logger.Info().
		Str("agent_id", agentID).
		Bool("success", resp.Msg.Success).
		Int32("ttl", resp.Msg.Ttl).
		Interface("observed_endpoint", resp.Msg.ObservedEndpoint).
		Msg("Agent registered with discovery service")

	return nil
}

// resolveToIPv4 resolves a hostname to an IPv4 address.
// This ensures we don't accidentally use IPv6 addresses that may cause issues.
func resolveToIPv4(host string, logger logging.Logger) (string, error) {
	// If already an IP address, validate it's IPv4
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() != nil {
			return host, nil
		}
		return "", fmt.Errorf("address is IPv6, need IPv4")
	}

	// Resolve hostname to IP addresses
	ips, err := net.LookupIP(host)
	if err != nil {
		return "", fmt.Errorf("failed to resolve hostname: %w", err)
	}

	// Find first IPv4 address
	for _, ip := range ips {
		if ip.To4() != nil {
			logger.Debug().
				Str("hostname", host).
				Str("resolved_ipv4", ip.String()).
				Msg("Resolved hostname to IPv4")
			return ip.String(), nil
		}
	}

	return "", fmt.Errorf("no IPv4 address found for hostname %s", host)
}

// setupAgentWireGuard creates and configures the agent's WireGuard device.
// Returns the WireGuard device and the discovered public endpoint (if STUN was successful).
func setupAgentWireGuard(
	agentKeys *auth.WireGuardKeyPair,
	colonyInfo *discoverypb.LookupColonyResponse,
	stunServers []string,
	enableRelay bool,
	wgPort int,
	logger logging.Logger,
) (*wireguard.Device, *discoverypb.Endpoint, error) {
	logger.Info().
		Int("port", wgPort).
		Msg("Setting up WireGuard device for agent")

	// Perform STUN discovery BEFORE starting WireGuard to avoid port conflicts.
	var agentPublicEndpoint *discoverypb.Endpoint
	if len(stunServers) > 0 && wgPort > 0 {
		// Only do STUN discovery if we have a configured port (not ephemeral).
		// For ephemeral ports, we'd need to bind first to know the port.
		logger.Info().
			Int("port", wgPort).
			Msg("Discovering public endpoint via STUN before starting WireGuard")

		agentPublicEndpoint = wireguard.DiscoverPublicEndpoint(stunServers, wgPort, logger)
		if agentPublicEndpoint != nil {
			logger.Info().
				Str("public_ip", agentPublicEndpoint.Ip).
				Uint32("public_port", agentPublicEndpoint.Port).
				Msg("Agent public endpoint discovered via STUN")
		}
	}

	// Create WireGuard config for agent
	wgConfig := &config.WireGuardConfig{
		PrivateKey: agentKeys.PrivateKey,
		PublicKey:  agentKeys.PublicKey,
		Port:       wgPort, // Use configured port (or -1 for ephemeral)
		MTU:        constants.DefaultWireGuardMTU,
	}

	// Create device
	wgDevice, err := wireguard.NewDevice(wgConfig, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create WireGuard device: %w", err)
	}

	// Start device
	if err := wgDevice.Start(); err != nil {
		return nil, nil, fmt.Errorf("failed to start WireGuard device: %w", err)
	}

	logger.Info().
		Str("interface", wgDevice.InterfaceName()).
		Msg("WireGuard device started")

	// LIMITATION: STUN discovery with ephemeral ports requires binding first.
	// Since we perform STUN before starting WireGuard, ephemeral ports require
	// a second STUN attempt after the WireGuard device is started to discover
	// the actual assigned port.
	// RECOMMENDATION: Use a configured port (not ephemeral) for agents that need
	// NAT traversal to ensure reliable STUN discovery.
	if agentPublicEndpoint == nil && len(stunServers) > 0 {
		localPort := wgDevice.ListenPort()
		if localPort > 0 {
			logger.Warn().
				Int("port", localPort).
				Msg("Performing STUN discovery after WireGuard started with ephemeral port")

			agentPublicEndpoint = wireguard.DiscoverPublicEndpoint(stunServers, localPort, logger)
			if agentPublicEndpoint != nil {
				logger.Info().
					Str("public_ip", agentPublicEndpoint.Ip).
					Uint32("public_port", agentPublicEndpoint.Port).
					Msg("Agent public endpoint discovered via STUN (ephemeral port)")
			}
		}
	}

	// Select colony endpoint for establishing the WireGuard peer.
	// Priority: observed endpoints (for NAT traversal) > regular endpoints
	var colonyEndpoint string

	// Try observed endpoints first (these are the colony's public NAT addresses)
	for _, observedEp := range colonyInfo.ObservedEndpoints {
		if observedEp == nil || observedEp.Ip == "" {
			continue
		}

		// LIMITATION: IPv6 support is not yet implemented.
		// IPv6 addresses are skipped in favor of IPv4 for NAT traversal.
		// TODO: Add proper IPv6 support with dual-stack handling.
		// For now, only IPv4 endpoints are used for agent-colony connectivity.
		ip := net.ParseIP(observedEp.Ip)
		if ip != nil && ip.To4() == nil {
			// This is an IPv6 address - skip it for now as we only support IPv4.
			logger.Debug().
				Str("ipv6_endpoint", observedEp.Ip).
				Msg("Skipping IPv6 observed endpoint (IPv4-only mode)")
			continue
		}

		// Skip loopback addresses
		if ip != nil && ip.IsLoopback() {
			logger.Debug().
				Str("loopback_endpoint", observedEp.Ip).
				Msg("Skipping loopback observed endpoint")
			continue
		}

		colonyEndpoint = net.JoinHostPort(observedEp.Ip, fmt.Sprintf("%d", observedEp.Port))
		logger.Info().
			Str("endpoint", colonyEndpoint).
			Msg("Using colony's observed public endpoint for NAT traversal")
		break
	}

	// Fall back to regular discovery endpoints
	// Note: Discovery endpoints contain the gRPC/Connect port, not WireGuard port.
	// We need to extract the host and use the WireGuard port instead.
	if colonyEndpoint == "" {
		for _, ep := range colonyInfo.Endpoints {
			if ep == "" {
				continue
			}

			host, _, err := net.SplitHostPort(ep)
			if err != nil {
				logger.Warn().Err(err).Str("endpoint", ep).Msg("Invalid colony endpoint from discovery")
				continue
			}

			if host == "" {
				logger.Warn().Str("endpoint", ep).Msg("Skipping discovery endpoint without host")
				continue
			}

			// Resolve hostname to IPv4 address to avoid IPv6 issues
			resolvedHost, err := resolveToIPv4(host, logger)
			if err != nil {
				logger.Warn().
					Err(err).
					Str("host", host).
					Msg("Failed to resolve endpoint to IPv4, using as-is")
				resolvedHost = host
			}

			// Determine WireGuard port from multiple sources in priority order:
			// 1. Observed endpoint (STUN-discovered port)
			// 2. Metadata field "wireguard_port"
			// 3. Default 51820
			wgPort := uint32(51820) // Default WireGuard port
			portSource := "default"

			if len(colonyInfo.ObservedEndpoints) > 0 && colonyInfo.ObservedEndpoints[0] != nil && colonyInfo.ObservedEndpoints[0].Port > 0 {
				// Use the port from the observed endpoint (STUN-discovered)
				wgPort = colonyInfo.ObservedEndpoints[0].Port
				portSource = "observed_endpoint"
			} else if colonyInfo.Metadata != nil {
				if portStr, ok := colonyInfo.Metadata["wireguard_port"]; ok && portStr != "" {
					if port, err := strconv.ParseUint(portStr, 10, 32); err == nil && port > 0 {
						wgPort = uint32(port)
						portSource = "metadata"
					}
				}
			}

			logger.Debug().
				Uint32("wireguard_port", wgPort).
				Str("source", portSource).
				Msg("Determined WireGuard port for colony connection")

			colonyEndpoint = net.JoinHostPort(resolvedHost, fmt.Sprintf("%d", wgPort))
			logger.Info().
				Str("endpoint", colonyEndpoint).
				Str("original_host", host).
				Uint32("wireguard_port", wgPort).
				Msg("Using colony's regular endpoint with WireGuard port")
			break
		}
	}

	// If still no endpoint and relay is enabled, request a relay
	if colonyEndpoint == "" && enableRelay && len(colonyInfo.Relays) > 0 {
		logger.Info().Msg("No direct colony endpoint available, attempting relay allocation")

		relayEndpoint, err := requestRelayAllocation(colonyInfo, agentKeys.PublicKey, logger)
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to allocate relay")
		} else if relayEndpoint != nil {
			colonyEndpoint = net.JoinHostPort(relayEndpoint.Ip, fmt.Sprintf("%d", relayEndpoint.Port))
			logger.Info().
				Str("relay_endpoint", colonyEndpoint).
				Msg("Using relay endpoint for NAT traversal")
		}
	}

	if colonyEndpoint == "" {
		return nil, nil, fmt.Errorf("no usable colony endpoint available (tried: observed, direct, relay)")
	}

	allowedIPs := make([]string, 0, 2)
	if colonyInfo.MeshIpv4 != "" {
		allowedIPs = append(allowedIPs, colonyInfo.MeshIpv4+"/32")
	}
	if colonyInfo.MeshIpv6 != "" {
		allowedIPs = append(allowedIPs, colonyInfo.MeshIpv6+"/128")
	}

	if len(allowedIPs) == 0 {
		return nil, nil, fmt.Errorf("discovery response missing mesh IPs; unable to configure WireGuard peer")
	}

	// Assign a temporary IP to the agent interface BEFORE adding peer.
	// Routes can only be added after the interface has an IP address.
	// We'll use a temporary IP in the high range (.254) which will be updated after registration.
	if colonyInfo.MeshIpv4 != "" {
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

	// Now add peer - routes will be created successfully since interface has an IP.
	peerConfig := &wireguard.PeerConfig{
		PublicKey:           colonyInfo.Pubkey,
		Endpoint:            colonyEndpoint,
		AllowedIPs:          allowedIPs,
		PersistentKeepalive: 25, // Keep NAT mapping alive
	}

	logger.Info().
		Str("endpoint", colonyEndpoint).
		Strs("allowed_ips", allowedIPs).
		Str("public_key", agentKeys.PublicKey).
		Msg("Adding colony WireGuard peer")

	if err := wgDevice.AddPeer(peerConfig); err != nil {
		wgDevice.Stop()
		return nil, nil, fmt.Errorf("failed to add colony as peer: %w", err)
	}

	logger.Info().
		Str("colony_endpoint", colonyEndpoint).
		Str("colony_mesh_ip", colonyInfo.MeshIpv4).
		Msg("Added colony as WireGuard peer")

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

	return wgDevice, agentPublicEndpoint, nil
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

	// Detect eBPF capabilities.
	ebpfCaps := ebpf.DetectCapabilities()
	logger.Info().
		Bool("ebpf_supported", ebpfCaps.Supported).
		Str("kernel_version", ebpfCaps.KernelVersion).
		Bool("btf_available", ebpfCaps.BtfAvailable).
		Int("available_collectors", len(ebpfCaps.AvailableCollectors)).
		Msg("Detected eBPF capabilities")

	// Build registration request with multi-service support
	regReq := &meshv1.RegisterRequest{
		AgentId:          agentID,
		ColonyId:         cfg.ColonyID,
		ColonySecret:     cfg.ColonySecret,
		WireguardPubkey:  agentPubKey,
		Version:          "0.1.0",
		Labels:           make(map[string]string),
		Services:         services,
		EbpfCapabilities: ebpfCaps,
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

// startHeartbeatLoop sends periodic heartbeats to the colony to keep the agent's status healthy.
func startHeartbeatLoop(
	ctx context.Context,
	agentID string,
	colonyMeshIP string,
	connectPort uint32,
	interval time.Duration,
	logger logging.Logger,
) {
	if connectPort == 0 {
		connectPort = 9000
	}

	colonyURL := fmt.Sprintf("http://%s", net.JoinHostPort(colonyMeshIP, fmt.Sprintf("%d", connectPort)))
	client := meshv1connect.NewMeshServiceClient(http.DefaultClient, colonyURL)

	logger.Info().
		Str("agent_id", agentID).
		Str("colony_url", colonyURL).
		Dur("interval", interval).
		Msg("Starting heartbeat loop")

	// Send immediate heartbeat to establish healthy status right away.
	sendHeartbeat := func() {
		heartbeatCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		resp, err := client.Heartbeat(heartbeatCtx, connect.NewRequest(&meshv1.HeartbeatRequest{
			AgentId: agentID,
			Status:  "healthy",
		}))
		cancel()

		if err != nil {
			logger.Warn().
				Err(err).
				Str("agent_id", agentID).
				Msg("Failed to send heartbeat")
		} else if !resp.Msg.Ok {
			logger.Warn().
				Str("agent_id", agentID).
				Msg("Heartbeat rejected by colony")
		} else {
			logger.Debug().
				Str("agent_id", agentID).
				Msg("Heartbeat sent successfully")
		}
	}

	// Send first heartbeat immediately.
	sendHeartbeat()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info().Msg("Heartbeat loop stopping")
			return
		case <-ticker.C:
			sendHeartbeat()
		}
	}
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

// requestRelayAllocation requests a relay allocation from the discovery service.
func requestRelayAllocation(
	colonyInfo *discoverypb.LookupColonyResponse,
	agentPubKey string,
	logger logging.Logger,
) (*discoverypb.Endpoint, error) {
	// Get discovery URL from environment or use default
	discoveryURL := os.Getenv("CORAL_DISCOVERY_URL")
	if discoveryURL == "" {
		discoveryURL = constants.DefaultDiscoveryEndpoint
	}

	// Create discovery client
	client := discoveryv1connect.NewDiscoveryServiceClient(
		http.DefaultClient,
		discoveryURL,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Request relay allocation
	req := connect.NewRequest(&discoverypb.RequestRelayRequest{
		MeshId:       colonyInfo.MeshId,
		AgentPubkey:  agentPubKey,
		ColonyPubkey: colonyInfo.Pubkey,
	})

	resp, err := client.RequestRelay(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("relay request failed: %w", err)
	}

	logger.Info().
		Str("session_id", resp.Msg.SessionId).
		Str("relay_id", resp.Msg.RelayId).
		Time("expires_at", resp.Msg.ExpiresAt.AsTime()).
		Msg("Relay allocated successfully")

	return resp.Msg.RelayEndpoint, nil
}
