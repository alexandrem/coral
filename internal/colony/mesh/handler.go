// Package mesh implements the MeshService RPC handlers for agent registration and heartbeat management.
// This package handles the mesh network coordination, including WireGuard peer configuration,
// mesh IP allocation, and discovery service integration for NAT traversal.
package mesh

import (
	"context"
	"fmt"
	"net"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	discoverypb "github.com/coral-mesh/coral/coral/discovery/v1"
	"github.com/coral-mesh/coral/coral/discovery/v1/discoveryv1connect"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/logging"
	"github.com/coral-mesh/coral/internal/wireguard"
)

// Handler implements the MeshService RPC handler.
type Handler struct {
	cfg             *config.ResolvedConfig
	wgDevice        *wireguard.Device
	registry        *registry.Registry
	logger          logging.Logger
	discoveryClient discoveryv1connect.DiscoveryServiceClient
}

// NewHandler creates a new mesh service handler.
func NewHandler(
	cfg *config.ResolvedConfig,
	wgDevice *wireguard.Device,
	registry *registry.Registry,
	discoveryClient discoveryv1connect.DiscoveryServiceClient,
	logger logging.Logger,
) *Handler {
	return &Handler{
		cfg:             cfg,
		wgDevice:        wgDevice,
		registry:        registry,
		discoveryClient: discoveryClient,
		logger:          logger,
	}
}

// Register handles agent registration requests.
func (h *Handler) Register(
	ctx context.Context,
	req *connect.Request[meshv1.RegisterRequest],
) (*connect.Response[meshv1.RegisterResponse], error) {
	// Extract peer address from request headers for WireGuard endpoint detection.
	var peerAddr string
	if req.Peer().Addr != "" {
		peerAddr = req.Peer().Addr
	}

	h.logger.Info().
		Str("agent_id", req.Msg.AgentId).
		Str("component_name", req.Msg.ComponentName). //nolint:staticcheck // ComponentName is deprecated but kept for backward compatibility
		Str("peer_addr", peerAddr).
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

	// Get agent's public endpoint from discovery service.
	// The agent registers its STUN-discovered public endpoint with discovery,
	// which we need for NAT traversal.
	var agentEndpoint string

	// Query discovery service for agent's observed endpoint
	if h.discoveryClient != nil {
		agentInfo, err := h.discoveryClient.LookupAgent(ctx, connect.NewRequest(&discoverypb.LookupAgentRequest{
			AgentId: req.Msg.AgentId,
		}))

		if err == nil && agentInfo.Msg != nil && len(agentInfo.Msg.ObservedEndpoints) > 0 {
			// Extract the peer's source IP from the HTTP connection to help select the right endpoint.
			var peerHost string
			if peerAddr != "" {
				if host, _, err := net.SplitHostPort(peerAddr); err == nil {
					peerHost = host
				}
			}

			// Select the best observed endpoint from the list.
			selectedEp, matchType := selectBestAgentEndpoint(agentInfo.Msg.ObservedEndpoints, peerHost, h.logger, req.Msg.AgentId)

			// Build endpoint string and log selection.
			if selectedEp != nil {
				agentEndpoint = net.JoinHostPort(selectedEp.Ip, fmt.Sprintf("%d", selectedEp.Port))
				if matchType == "matching" {
					h.logger.Info().
						Str("agent_id", req.Msg.AgentId).
						Str("endpoint", agentEndpoint).
						Str("peer_host", peerHost).
						Msg("Using agent's endpoint matching connection source")
				} else {
					h.logger.Info().
						Str("agent_id", req.Msg.AgentId).
						Str("endpoint", agentEndpoint).
						Msg("Using agent's observed endpoint from discovery service")
				}
			} else {
				h.logger.Warn().
					Str("agent_id", req.Msg.AgentId).
					Msg("All observed endpoints were localhost - agent may not be reachable via WireGuard")
			}
		} else {
			h.logger.Debug().
				Err(err).
				Str("agent_id", req.Msg.AgentId).
				Msg("Could not get agent endpoint from discovery service")
		}
	}

	// Fallback: extract agent's source address from HTTP connection.
	// This works for same-host testing but not for NAT traversal.
	// Note: peerAddr includes the HTTP port, not the WireGuard port.
	if agentEndpoint == "" && peerAddr != "" {
		if host, _, err := net.SplitHostPort(peerAddr); err == nil {
			// Use a default WireGuard port (this is just a guess and likely wrong for agents)
			// WireGuard will learn the correct endpoint from incoming packets
			h.logger.Debug().
				Str("agent_id", req.Msg.AgentId).
				Str("peer_addr", peerAddr).
				Msg("No discovery endpoint available, WireGuard will learn endpoint from incoming packets")
			_ = host
		}
	}

	// Add agent as WireGuard peer
	peerConfig := &wireguard.PeerConfig{
		PublicKey:           req.Msg.WireguardPubkey,
		Endpoint:            agentEndpoint, // Use detected endpoint
		AllowedIPs:          []string{meshIP.String() + "/32"},
		PersistentKeepalive: 25, // Keep NAT mappings alive
	}

	h.logger.Info().
		Str("agent_id", req.Msg.AgentId).
		Str("endpoint", agentEndpoint).
		Str("pubkey", req.Msg.WireguardPubkey[:8]+"...").
		Msg("Adding agent as WireGuard peer")

	if err := h.wgDevice.AddPeer(peerConfig); err != nil {
		h.logger.Error().
			Err(err).
			Str("agent_id", req.Msg.AgentId).
			Msg("Failed to add agent as WireGuard peer")

		// Release the allocated IP since we couldn't add the peer
		_ = allocator.Release(meshIP) // TODO: errcheck

		return connect.NewResponse(&meshv1.RegisterResponse{
			Accepted: false,
			Reason:   "peer_add_failed",
		}), nil
	}

	// Register agent in the registry for tracking.
	// Note: We don't have IPv6 mesh IP yet (future enhancement).
	//nolint:staticcheck // ComponentName is deprecated but kept for backward compatibility
	if _, err := h.registry.Register(req.Msg.AgentId, req.Msg.ComponentName, meshIP.String(), "", req.Msg.Services, req.Msg.RuntimeContext, req.Msg.ProtocolVersion); err != nil {
		h.logger.Warn().
			Err(err).
			Str("agent_id", req.Msg.AgentId).
			Msg("Failed to register agent in registry (non-fatal)")
	}

	// Log registration with service details
	logEvent := h.logger.Info().
		Str("agent_id", req.Msg.AgentId).
		Str("component_name", req.Msg.ComponentName). //nolint:staticcheck // ComponentName is deprecated but kept for backward compatibility
		Str("mesh_ip", meshIP.String())

	if len(req.Msg.Services) > 0 {
		logEvent.Int("service_count", len(req.Msg.Services))
	}

	logEvent.Msg("Agent registered successfully")

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

// Heartbeat handles agent heartbeat requests to update last_seen timestamp.
func (h *Handler) Heartbeat(
	ctx context.Context,
	req *connect.Request[meshv1.HeartbeatRequest],
) (*connect.Response[meshv1.HeartbeatResponse], error) {
	h.logger.Debug().
		Str("agent_id", req.Msg.AgentId).
		Str("status", req.Msg.Status).
		Msg("Agent heartbeat received")

	// Validate agent_id
	if req.Msg.AgentId == "" {
		h.logger.Warn().Msg("Heartbeat rejected: missing agent_id")
		return connect.NewResponse(&meshv1.HeartbeatResponse{
			Ok: false,
		}), nil
	}

	// Update heartbeat in registry
	if err := h.registry.UpdateHeartbeat(req.Msg.AgentId); err != nil {
		h.logger.Warn().
			Err(err).
			Str("agent_id", req.Msg.AgentId).
			Msg("Failed to update agent heartbeat")
		return connect.NewResponse(&meshv1.HeartbeatResponse{
			Ok: false,
		}), nil
	}

	h.logger.Debug().
		Str("agent_id", req.Msg.AgentId).
		Msg("Agent heartbeat updated successfully")

	return connect.NewResponse(&meshv1.HeartbeatResponse{
		Ok:       true,
		Commands: []string{}, // Future: colony can send commands to agent
	}), nil
}

// selectBestAgentEndpoint selects the best WireGuard endpoint for an agent from a list of observed endpoints.
// Strategy:
//  1. Skip localhost/127.0.0.1 endpoints (would be self-referential from colony's perspective)
//  2. Prefer an endpoint matching the peer's source IP (how they connected to us)
//  3. Otherwise use the first non-localhost endpoint
//
// Returns the selected endpoint and a match type ("matching" or "first"), or (nil, "") if no valid endpoint found.
func selectBestAgentEndpoint(
	observedEndpoints []*discoverypb.Endpoint,
	peerHost string,
	logger logging.Logger,
	agentID string,
) (*discoverypb.Endpoint, string) {
	var selectedEp *discoverypb.Endpoint
	var matchingEp *discoverypb.Endpoint

	for _, ep := range observedEndpoints {
		if ep == nil || ep.Ip == "" {
			continue
		}

		isLocalhost := ep.Ip == "127.0.0.1" || ep.Ip == "::1" || ep.Ip == "localhost"

		// If this endpoint's IP matches how the agent connected to us, prefer it.
		// This handles same-host deployments where agent connects from 127.0.0.1.
		if peerHost != "" && ep.Ip == peerHost && matchingEp == nil {
			matchingEp = ep
			if isLocalhost {
				logger.Debug().
					Str("agent_id", agentID).
					Str("endpoint", net.JoinHostPort(ep.Ip, fmt.Sprintf("%d", ep.Port))).
					Msg("Using localhost endpoint (agent connected from same host)")
			}
		}

		// Skip localhost endpoints UNLESS they matched the connection source.
		// This allows same-host deployments while preventing container issues.
		if isLocalhost && matchingEp == nil {
			logger.Debug().
				Str("agent_id", agentID).
				Str("endpoint", net.JoinHostPort(ep.Ip, fmt.Sprintf("%d", ep.Port))).
				Msg("Skipping localhost endpoint (agent connected from different host)")
			continue
		}

		// Track the first valid endpoint as fallback.
		if selectedEp == nil {
			selectedEp = ep
		}
	}

	// Prefer the matching endpoint, fallback to first non-localhost.
	if matchingEp != nil {
		return matchingEp, "matching"
	} else if selectedEp != nil {
		return selectedEp, "first"
	}

	return nil, ""
}
