package startup

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/coral-mesh/coral/internal/auth"
	"github.com/coral-mesh/coral/internal/cli/agent/types"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/constants"
	"github.com/coral-mesh/coral/internal/discovery"
	"github.com/coral-mesh/coral/internal/logging"
	"github.com/coral-mesh/coral/internal/wireguard"
)

// NetworkResult contains the results of network initialization.
type NetworkResult struct {
	WireGuardDevice       *wireguard.Device
	AgentKeys             *auth.WireGuardKeyPair
	ColonyInfo            *discovery.LookupColonyResponse
	AgentObservedEndpoint *discovery.Endpoint
	STUNServers           []string
	MeshIP                string
	MeshSubnet            string
}

// NetworkInitializer handles WireGuard, STUN, discovery, and mesh setup.
type NetworkInitializer struct {
	logger       logging.Logger
	cfg          *config.ResolvedConfig
	agentCfg     *config.AgentConfig
	serviceSpecs []*types.ServiceSpec
	agentID      string
}

// NewNetworkInitializer creates a new network initializer.
func NewNetworkInitializer(
	logger logging.Logger,
	cfg *config.ResolvedConfig,
	agentCfg *config.AgentConfig,
	serviceSpecs []*types.ServiceSpec,
	agentID string,
) *NetworkInitializer {
	return &NetworkInitializer{
		logger:       logger,
		cfg:          cfg,
		agentCfg:     agentCfg,
		serviceSpecs: serviceSpecs,
		agentID:      agentID,
	}
}

// Initialize performs network initialization.
func (n *NetworkInitializer) Initialize() (*NetworkResult, error) {
	result := &NetworkResult{}

	// Step 1: Query discovery service for colony information.
	n.logger.Info().
		Str("colony_id", n.cfg.ColonyID).
		Msg("Querying discovery service for colony information")

	colonyInfo, err := QueryDiscoveryForColony(n.cfg, n.logger)
	if err != nil {
		n.logger.Warn().
			Err(err).
			Msg("Failed to query discovery service - will retry in background")
		colonyInfo = nil // Agent will start in waiting_discovery state
	} else {
		n.logger.Info().
			Str("colony_pubkey", colonyInfo.Pubkey).
			Strs("endpoints", colonyInfo.Endpoints).
			Msg("Received colony information from discovery")
	}
	result.ColonyInfo = colonyInfo

	// Step 2: Generate WireGuard keys for this agent.
	agentKeys, err := auth.GenerateWireGuardKeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate WireGuard keys: %w", err)
	}

	n.logger.Info().
		Str("agent_pubkey", agentKeys.PublicKey).
		Msg("Generated agent WireGuard keys")
	result.AgentKeys = agentKeys

	// Step 3: Get STUN servers for NAT traversal.
	stunServers := n.getSTUNServers(colonyInfo)
	if len(stunServers) > 0 {
		n.logger.Info().
			Strs("stun_servers", stunServers).
			Msg("STUN servers configured for NAT traversal")
	}
	result.STUNServers = stunServers

	// Relay setting is loaded from config (env var override via MergeFromEnv)
	enableRelay := n.agentCfg.Agent.NAT.EnableRelay

	// Step 5: Use a fixed, discoverable port by default. STUN must bind the
	// intended WireGuard port before the device starts, so an ephemeral port
	// cannot be advertised for RFD 109 rendezvous enrollment.
	envPort := os.Getenv("CORAL_WIREGUARD_PORT")
	wgPort, portErr := resolveAgentWireGuardPort(envPort)
	if envPort != "" {
		if portErr == nil {
			if wgPort < 0 {
				n.logger.Warn().Msg("Using explicitly configured ephemeral WireGuard port; STUN and NAT-local Colony enrollment will be unavailable")
			} else {
				n.logger.Info().
					Int("port", wgPort).
					Msg("Using configured WireGuard port")
			}
		} else {
			wgPort = constants.DefaultAgentWireGuardPort
			n.logger.Warn().
				Str("port", envPort).
				Int("default_port", wgPort).
				Msg("Invalid CORAL_WIREGUARD_PORT value, using default Agent WireGuard port")
		}
	} else {
		n.logger.Info().
			Int("port", wgPort).
			Msg("Using default Agent WireGuard port")
	}

	// Step 6: Create and start WireGuard device (RFD 019: without peer, without IP).
	wgDevice, agentObservedEndpoint, _, err := SetupAgentWireGuard(
		agentKeys,
		colonyInfo,
		stunServers,
		enableRelay,
		wgPort,
		n.logger,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to setup WireGuard: %w", err)
	}
	result.WireGuardDevice = wgDevice

	n.logger.Debug().Msg("Agent running with elevated privileges for eBPF/Beyla operations")

	// Step 7: Register agent with discovery service using the observed endpoint from STUN.
	if agentObservedEndpoint != nil {
		n.logger.Info().
			Str("agent_id", n.agentID).
			Str("public_ip", agentObservedEndpoint.IP).
			Uint32("public_port", agentObservedEndpoint.Port).
			Msg("Registering agent with discovery service")

		registeredEndpoint, err := RegisterAgentWithDiscovery(n.cfg, n.agentID, agentKeys.PublicKey, agentObservedEndpoint, n.logger)
		if err != nil {
			n.logger.Warn().Err(err).Msg("Failed to register agent with discovery service (continuing anyway)")
		} else {
			// Use the endpoint acknowledged by Discovery for bootstrap endpoint
			// derivation and RFD 109, rather than assuming the submitted STUN
			// observation was stored unchanged.
			if registeredEndpoint != nil {
				result.AgentObservedEndpoint = registeredEndpoint
			} else {
				result.AgentObservedEndpoint = agentObservedEndpoint
			}
		}
	} else {
		n.logger.Info().Msg("No observed endpoint available (STUN not configured or failed), skipping discovery service registration")
	}

	return result, nil
}

// resolveAgentWireGuardPort resolves the optional environment override. Zero
// and -1 are explicit opt-outs for deployments that require an ephemeral
// port; positive values select a fixed, STUN-discoverable UDP port.
func resolveAgentWireGuardPort(value string) (int, error) {
	if value == "" {
		return constants.DefaultAgentWireGuardPort, nil
	}

	return parseAgentWireGuardPort(value)
}

// parseAgentWireGuardPort parses a non-empty environment override. Zero and
// -1 are explicit opt-outs for deployments that require an ephemeral port;
// positive values select a fixed, STUN-discoverable UDP port.
func parseAgentWireGuardPort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid integer: %w", err)
	}
	if port == 0 || port == -1 {
		return -1, nil
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("port must be between 1 and 65535, or 0/-1 for ephemeral")
	}
	return port, nil
}

// ConfigureMesh configures the agent mesh with permanent IP from colony.
func (n *NetworkInitializer) ConfigureMesh(
	result *NetworkResult,
	meshIP, meshSubnet string,
	colonyEndpoint string,
) error {
	// Parse IP and subnet for mesh configuration (RFD 019).
	parsedMeshIP := net.ParseIP(meshIP)
	if parsedMeshIP == nil {
		return fmt.Errorf("invalid mesh IP from colony: %s", meshIP)
	}

	_, parsedMeshSubnet, err := net.ParseCIDR(meshSubnet)
	if err != nil {
		return fmt.Errorf("invalid mesh subnet from colony: %w", err)
	}

	// Configure agent mesh with permanent IP (RFD 019).
	n.logger.Info().
		Str("mesh_ip", meshIP).
		Str("subnet", meshSubnet).
		Msg("Configuring agent mesh with permanent IP from colony")

	if err := ConfigureAgentMesh(result.WireGuardDevice, parsedMeshIP, parsedMeshSubnet, result.ColonyInfo, colonyEndpoint, n.logger); err != nil {
		return fmt.Errorf("failed to configure agent mesh: %w", err)
	}

	n.logger.Info().
		Str("mesh_ip", meshIP).
		Msg("Agent mesh configured successfully - tunnel ready")

	result.MeshIP = meshIP
	result.MeshSubnet = meshSubnet

	// Test connectivity to colony via mesh.
	if result.ColonyInfo != nil {
		connectPort := result.ColonyInfo.ConnectPort
		if connectPort == 0 {
			connectPort = constants.DefaultColonyPort
		}
		meshAddr := net.JoinHostPort(result.ColonyInfo.MeshIPv4, fmt.Sprintf("%d", connectPort))
		n.logger.Info().
			Str("mesh_addr", meshAddr).
			Msg("Testing connectivity to colony via mesh to establish WireGuard handshake")

		conn, err := net.DialTimeout("tcp", meshAddr, 5*time.Second)
		if err != nil {
			n.logger.Warn().
				Err(err).
				Str("mesh_addr", meshAddr).
				Msg("Unable to establish connection to colony via mesh - handshake may not be complete")
		} else {
			_ = conn.Close() // TODO: errcheck
			n.logger.Info().
				Str("mesh_addr", meshAddr).
				Msg("Successfully established WireGuard tunnel to colony")
		}
	}

	return nil
}

// getSTUNServers determines which STUN servers to use for NAT traversal.
// Priority: agent config (with env override via MergeFromEnv) > discovery response > default.
func (n *NetworkInitializer) getSTUNServers(_ *discovery.LookupColonyResponse) []string {
	// Check agent config (env var CORAL_STUN_SERVERS is merged via MergeFromEnv).
	if len(n.agentCfg.Agent.NAT.STUNServers) > 0 {
		return n.agentCfg.Agent.NAT.STUNServers
	}

	// Use STUN servers from discovery response (not yet implemented in response).
	// This would be added when colonies register with their STUN servers.

	// Fall back to default.
	return []string{constants.DefaultSTUNServer}
}
