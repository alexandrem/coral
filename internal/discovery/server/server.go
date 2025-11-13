package server

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/timestamppb"

	discoveryv1 "github.com/coral-io/coral/coral/discovery/v1"
	"github.com/coral-io/coral/coral/discovery/v1/discoveryv1connect"
	"github.com/coral-io/coral/internal/discovery/registry"
)

// Server implements the DiscoveryService.
type Server struct {
	registry    *registry.Registry
	version     string
	startTime   time.Time
	logger      zerolog.Logger
	stunServers []string // Recommended fallback STUN servers (optional, clients may ignore)
}

// New creates a new discovery server.
func New(reg *registry.Registry, version string, logger zerolog.Logger, stunServers []string) *Server {
	return &Server{
		registry:    reg,
		version:     version,
		startTime:   time.Now(),
		logger:      logger,
		stunServers: stunServers,
	}
}

// Ensure Server implements the DiscoveryServiceHandler interface
var _ discoveryv1connect.DiscoveryServiceHandler = (*Server)(nil)

// RegisterColony handles colony registration requests.
func (s *Server) RegisterColony(
	ctx context.Context,
	req *connect.Request[discoveryv1.RegisterColonyRequest],
) (*connect.Response[discoveryv1.RegisterColonyResponse], error) {
	// Validate request
	if req.Msg.MeshId == "" {
		s.logger.Warn().Msg("Registration rejected: mesh_id is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("mesh_id is required"))
	}
	if req.Msg.Pubkey == "" {
		s.logger.Warn().Str("mesh_id", req.Msg.MeshId).Msg("Registration rejected: pubkey is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("pubkey is required"))
	}
	if len(req.Msg.Endpoints) == 0 {
		s.logger.Warn().Str("mesh_id", req.Msg.MeshId).Msg("Registration rejected: at least one endpoint is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("at least one endpoint is required"))
	}

	// Extract observed endpoint from request peer info
	observedEndpoint := s.extractObservedEndpoint(req)

	// Use client-provided observed endpoint if available, otherwise use what we observed
	clientObservedEndpoint := req.Msg.ObservedEndpoint
	if clientObservedEndpoint == nil {
		clientObservedEndpoint = observedEndpoint
	}

	// Determine NAT hint (simple heuristic for now)
	natHint := discoveryv1.NatHint_NAT_UNKNOWN
	if clientObservedEndpoint != nil {
		natHint = discoveryv1.NatHint_NAT_CONE // Default to cone NAT
	}

	// Log incoming registration request
	s.logger.Info().
		Str("mesh_id", req.Msg.MeshId).
		Str("pubkey", req.Msg.Pubkey).
		Strs("endpoints", req.Msg.Endpoints).
		Str("mesh_ipv4", req.Msg.MeshIpv4).
		Str("mesh_ipv6", req.Msg.MeshIpv6).
		Uint32("connect_port", req.Msg.ConnectPort).
		Interface("metadata", req.Msg.Metadata).
		Interface("client_observed_endpoint", req.Msg.ObservedEndpoint).
		Interface("http_observed_endpoint", observedEndpoint).
		Msg("Colony registration request received")

	// Register the colony
	entry, err := s.registry.Register(
		req.Msg.MeshId,
		req.Msg.Pubkey,
		req.Msg.Endpoints,
		req.Msg.MeshIpv4,
		req.Msg.MeshIpv6,
		req.Msg.ConnectPort,
		req.Msg.Metadata,
		clientObservedEndpoint,
		natHint,
	)
	if err != nil {
		s.logger.Error().
			Err(err).
			Str("mesh_id", req.Msg.MeshId).
			Msg("Failed to register colony")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Build response
	ttlSeconds := int32(entry.ExpiresAt.Sub(entry.LastSeen).Seconds())
	resp := &discoveryv1.RegisterColonyResponse{
		Success:          true,
		Ttl:              ttlSeconds,
		ExpiresAt:        timestamppb.New(entry.ExpiresAt),
		ObservedEndpoint: observedEndpoint,
		StunServers:      s.stunServers, // Recommended fallback STUN servers (optional)
	}

	// Log successful registration
	s.logger.Info().
		Str("mesh_id", entry.MeshID).
		Int32("ttl_seconds", ttlSeconds).
		Time("expires_at", entry.ExpiresAt).
		Msg("Colony registered successfully")

	return connect.NewResponse(resp), nil
}

// LookupColony handles colony lookup requests.
func (s *Server) LookupColony(
	ctx context.Context,
	req *connect.Request[discoveryv1.LookupColonyRequest],
) (*connect.Response[discoveryv1.LookupColonyResponse], error) {
	// Validate request
	if req.Msg.MeshId == "" {
		s.logger.Warn().Msg("Lookup rejected: mesh_id is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("mesh_id is required"))
	}

	s.logger.Debug().Str("mesh_id", req.Msg.MeshId).Msg("Colony lookup request received")

	// Lookup the colony
	entry, err := s.registry.Lookup(req.Msg.MeshId)
	if err != nil {
		s.logger.Debug().
			Str("mesh_id", req.Msg.MeshId).
			Msg("Colony not found")
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	// Prepare observed endpoints list
	observedEndpoints := []*discoveryv1.Endpoint{}
	if entry.ObservedEndpoint != nil {
		observedEndpoints = append(observedEndpoints, entry.ObservedEndpoint)
	}

	// Prepare available relay options
	relays := []*discoveryv1.RelayOption{
		{
			Endpoint: &discoveryv1.Endpoint{
				Ip:       "relay.coral.io",
				Port:     3478,
				Protocol: "udp",
				ViaRelay: true,
			},
			RelayId: "default-relay",
			Region:  "global",
			Load:    50, // Placeholder
		},
	}

	// Build response
	resp := &discoveryv1.LookupColonyResponse{
		MeshId:            entry.MeshID,
		Pubkey:            entry.PubKey,
		Endpoints:         entry.Endpoints,
		MeshIpv4:          entry.MeshIPv4,
		MeshIpv6:          entry.MeshIPv6,
		ConnectPort:       entry.ConnectPort,
		Metadata:          entry.Metadata,
		LastSeen:          timestamppb.New(entry.LastSeen),
		ObservedEndpoints: observedEndpoints,
		Nat:               entry.NatHint,
		Relays:            relays,
	}

	s.logger.Debug().
		Str("mesh_id", entry.MeshID).
		Time("last_seen", entry.LastSeen).
		Int("observed_endpoints", len(observedEndpoints)).
		Int("relay_options", len(relays)).
		Msg("Colony lookup successful")

	return connect.NewResponse(resp), nil
}

// Health handles health check requests.
func (s *Server) Health(
	ctx context.Context,
	req *connect.Request[discoveryv1.HealthRequest],
) (*connect.Response[discoveryv1.HealthResponse], error) {
	uptimeSeconds := time.Since(s.startTime).Seconds()

	resp := &discoveryv1.HealthResponse{
		Status:             "ok",
		Version:            s.version,
		UptimeSeconds:      int64(uptimeSeconds),
		RegisteredColonies: int32(s.registry.CountActive()),
	}

	return connect.NewResponse(resp), nil
}

// RequestRelay handles relay allocation requests.
func (s *Server) RequestRelay(
	ctx context.Context,
	req *connect.Request[discoveryv1.RequestRelayRequest],
) (*connect.Response[discoveryv1.RequestRelayResponse], error) {
	// Validate request
	if req.Msg.MeshId == "" {
		s.logger.Warn().Msg("Relay request rejected: mesh_id is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("mesh_id is required"))
	}
	if req.Msg.AgentPubkey == "" {
		s.logger.Warn().Str("mesh_id", req.Msg.MeshId).Msg("Relay request rejected: agent_pubkey is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("agent_pubkey is required"))
	}
	if req.Msg.ColonyPubkey == "" {
		s.logger.Warn().Str("mesh_id", req.Msg.MeshId).Msg("Relay request rejected: colony_pubkey is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("colony_pubkey is required"))
	}

	s.logger.Info().
		Str("mesh_id", req.Msg.MeshId).
		Str("agent_pubkey", req.Msg.AgentPubkey).
		Str("colony_pubkey", req.Msg.ColonyPubkey).
		Msg("Relay request received")

	// Generate session ID
	sessionID := fmt.Sprintf("relay-%d", time.Now().UnixNano())

	// LIMITATION: Relay implementation is currently a placeholder.
	// This returns a hardcoded relay endpoint that does not actually exist.
	// TODO (RFD 023 Phase 3): Implement actual relay server with:
	//   - Real relay selection logic based on load and region.
	//   - Actual packet forwarding between agent and colony.
	//   - Dedicated coral-relay binary for relay operations.
	// For now, this API exists to test the discovery service flow.
	relayEndpoint := &discoveryv1.Endpoint{
		Ip:       "relay.coral.io", // PLACEHOLDER: This endpoint does not exist yet.
		Port:     3478,
		Protocol: "udp",
		ViaRelay: true,
	}
	relayID := "default-relay"

	// Allocate the relay session
	session, err := s.registry.AllocateRelay(
		sessionID,
		req.Msg.MeshId,
		req.Msg.AgentPubkey,
		req.Msg.ColonyPubkey,
		relayEndpoint,
		relayID,
	)
	if err != nil {
		s.logger.Error().
			Err(err).
			Str("mesh_id", req.Msg.MeshId).
			Msg("Failed to allocate relay")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &discoveryv1.RequestRelayResponse{
		RelayEndpoint: session.RelayEndpoint,
		SessionId:     session.SessionID,
		ExpiresAt:     timestamppb.New(session.ExpiresAt),
		RelayId:       session.RelayID,
	}

	s.logger.Info().
		Str("session_id", session.SessionID).
		Str("relay_id", session.RelayID).
		Time("expires_at", session.ExpiresAt).
		Msg("Relay allocated successfully")

	return connect.NewResponse(resp), nil
}

// ReleaseRelay handles relay release requests.
func (s *Server) ReleaseRelay(
	ctx context.Context,
	req *connect.Request[discoveryv1.ReleaseRelayRequest],
) (*connect.Response[discoveryv1.ReleaseRelayResponse], error) {
	// Validate request
	if req.Msg.SessionId == "" {
		s.logger.Warn().Msg("Release relay rejected: session_id is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("session_id is required"))
	}

	s.logger.Info().
		Str("session_id", req.Msg.SessionId).
		Msg("Release relay request received")

	// Release the relay session
	err := s.registry.ReleaseRelay(req.Msg.SessionId)
	if err != nil {
		s.logger.Error().
			Err(err).
			Str("session_id", req.Msg.SessionId).
			Msg("Failed to release relay")
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	resp := &discoveryv1.ReleaseRelayResponse{
		Success: true,
	}

	s.logger.Info().
		Str("session_id", req.Msg.SessionId).
		Msg("Relay released successfully")

	return connect.NewResponse(resp), nil
}

// extractObservedEndpoint extracts the client's observed public endpoint from the request.
func (s *Server) extractObservedEndpoint(req *connect.Request[discoveryv1.RegisterColonyRequest]) *discoveryv1.Endpoint {
	// Priority order:
	// 1. X-Forwarded-For (if behind proxy/load balancer)
	// 2. X-Real-IP (nginx)
	// 3. X-Observed-Addr (our custom middleware)

	// First, check X-Forwarded-For header (if behind a proxy/load balancer)
	xForwardedFor := req.Header().Get("X-Forwarded-For")
	if xForwardedFor != "" {
		// X-Forwarded-For can be comma-separated list, take the first (original client)
		parts := strings.Split(xForwardedFor, ",")
		clientIP := strings.TrimSpace(parts[0])

		// Validate and filter the extracted IP
		if !isValidPublicIPv4(clientIP, s.logger) {
			s.logger.Debug().
				Str("x_forwarded_for", xForwardedFor).
				Str("rejected_ip", clientIP).
				Msg("Rejected invalid/private IP from X-Forwarded-For header")
		} else {
			s.logger.Debug().
				Str("x_forwarded_for", xForwardedFor).
				Str("extracted_ip", clientIP).
				Msg("Extracted IP from X-Forwarded-For header")

			// For X-Forwarded-For, we don't know the port, so use 0
			return &discoveryv1.Endpoint{
				Ip:       clientIP,
				Port:     0, // Unknown port when behind proxy
				Protocol: "udp",
				ViaRelay: false,
			}
		}
	}

	// Check X-Real-IP header (common with nginx)
	xRealIP := req.Header().Get("X-Real-IP")
	if xRealIP != "" {
		if !isValidPublicIPv4(xRealIP, s.logger) {
			s.logger.Debug().
				Str("x_real_ip", xRealIP).
				Msg("Rejected invalid/private IP from X-Real-IP header")
		} else {
			s.logger.Debug().
				Str("x_real_ip", xRealIP).
				Msg("Extracted IP from X-Real-IP header")

			return &discoveryv1.Endpoint{
				Ip:       xRealIP,
				Port:     0, // Unknown port when behind proxy
				Protocol: "udp",
				ViaRelay: false,
			}
		}
	}

	// Check our custom X-Observed-Addr header (set by middleware)
	xObservedAddr := req.Header().Get("X-Observed-Addr")
	if xObservedAddr != "" {
		if !isValidPublicIPv4(xObservedAddr, s.logger) {
			s.logger.Debug().
				Str("x_observed_addr", xObservedAddr).
				Msg("Rejected invalid/private IP from X-Observed-Addr header")
		} else {
			s.logger.Debug().
				Str("x_observed_addr", xObservedAddr).
				Msg("Extracted IP from X-Observed-Addr header")

			return &discoveryv1.Endpoint{
				Ip:       xObservedAddr,
				Port:     0, // Port not known from HTTP connection
				Protocol: "udp",
				ViaRelay: false,
			}
		}
	}

	s.logger.Debug().Msg("Could not extract valid observed endpoint from request")
	return nil
}

// isValidPublicIPv4 checks if an IP address is a valid public IPv4 address.
// It rejects IPv6, loopback, private, and other non-routable addresses.
func isValidPublicIPv4(ipStr string, logger zerolog.Logger) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		logger.Debug().Str("ip", ipStr).Msg("Failed to parse IP address")
		return false
	}

	// Reject IPv6 addresses
	if ip.To4() == nil {
		logger.Debug().Str("ip", ipStr).Msg("Rejecting IPv6 address")
		return false
	}

	// Reject loopback addresses (127.0.0.0/8)
	if ip.IsLoopback() {
		logger.Debug().Str("ip", ipStr).Msg("Rejecting loopback address")
		return false
	}

	// Reject private addresses (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)
	if ip.IsPrivate() {
		logger.Debug().Str("ip", ipStr).Msg("Rejecting private address")
		return false
	}

	// Reject link-local addresses (169.254.0.0/16)
	if ip.IsLinkLocalUnicast() {
		logger.Debug().Str("ip", ipStr).Msg("Rejecting link-local address")
		return false
	}

	// Reject multicast addresses
	if ip.IsMulticast() {
		logger.Debug().Str("ip", ipStr).Msg("Rejecting multicast address")
		return false
	}

	// Reject unspecified address (0.0.0.0)
	if ip.IsUnspecified() {
		logger.Debug().Str("ip", ipStr).Msg("Rejecting unspecified address")
		return false
	}

	return true
}

// RegisterAgent handles agent registration requests.
func (s *Server) RegisterAgent(
	ctx context.Context,
	req *connect.Request[discoveryv1.RegisterAgentRequest],
) (*connect.Response[discoveryv1.RegisterAgentResponse], error) {
	// Validate request
	if req.Msg.AgentId == "" {
		s.logger.Warn().Msg("Agent registration rejected: agent_id is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("agent_id is required"))
	}
	if req.Msg.MeshId == "" {
		s.logger.Warn().Str("agent_id", req.Msg.AgentId).Msg("Agent registration rejected: mesh_id is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("mesh_id is required"))
	}
	if req.Msg.Pubkey == "" {
		s.logger.Warn().Str("agent_id", req.Msg.AgentId).Msg("Agent registration rejected: pubkey is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("pubkey is required"))
	}

	// Extract observed endpoint
	observedEndpoint := s.extractAgentObservedEndpoint(req)

	// Use client-provided observed endpoint if available
	clientObservedEndpoint := req.Msg.ObservedEndpoint
	if clientObservedEndpoint == nil {
		clientObservedEndpoint = observedEndpoint
	}

	// Determine NAT hint
	natHint := discoveryv1.NatHint_NAT_UNKNOWN
	if clientObservedEndpoint != nil {
		natHint = discoveryv1.NatHint_NAT_CONE
	}

	s.logger.Info().
		Str("agent_id", req.Msg.AgentId).
		Str("mesh_id", req.Msg.MeshId).
		Str("pubkey", req.Msg.Pubkey).
		Strs("endpoints", req.Msg.Endpoints).
		Interface("client_observed_endpoint", req.Msg.ObservedEndpoint).
		Interface("http_observed_endpoint", observedEndpoint).
		Msg("Agent registration request received")

	// Register the agent (reuse colony registration for now, we'll extend later)
	entry, err := s.registry.Register(
		req.Msg.AgentId, // Use agent_id as the key
		req.Msg.Pubkey,
		req.Msg.Endpoints,
		"", // Agent doesn't have mesh IPv4 yet
		"", // Agent doesn't have mesh IPv6 yet
		0,  // Agent doesn't have connect port
		req.Msg.Metadata,
		clientObservedEndpoint,
		natHint,
	)
	if err != nil {
		s.logger.Error().
			Err(err).
			Str("agent_id", req.Msg.AgentId).
			Msg("Failed to register agent")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Build response (return the client-provided observed endpoint, not HTTP-extracted)
	ttlSeconds := int32(entry.ExpiresAt.Sub(entry.LastSeen).Seconds())
	resp := &discoveryv1.RegisterAgentResponse{
		Success:          true,
		Ttl:              ttlSeconds,
		ExpiresAt:        timestamppb.New(entry.ExpiresAt),
		ObservedEndpoint: clientObservedEndpoint, // Return what was actually stored
		StunServers:      s.stunServers,          // Recommended fallback STUN servers (optional)
	}

	s.logger.Info().
		Str("agent_id", req.Msg.AgentId).
		Int32("ttl_seconds", ttlSeconds).
		Time("expires_at", entry.ExpiresAt).
		Interface("stored_observed_endpoint", entry.ObservedEndpoint).
		Msg("Agent registered successfully")

	return connect.NewResponse(resp), nil
}

// LookupAgent handles agent lookup requests.
func (s *Server) LookupAgent(
	ctx context.Context,
	req *connect.Request[discoveryv1.LookupAgentRequest],
) (*connect.Response[discoveryv1.LookupAgentResponse], error) {
	// Validate request
	if req.Msg.AgentId == "" {
		s.logger.Warn().Msg("Agent lookup rejected: agent_id is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("agent_id is required"))
	}

	s.logger.Debug().Str("agent_id", req.Msg.AgentId).Msg("Agent lookup request received")

	// Lookup the agent
	entry, err := s.registry.Lookup(req.Msg.AgentId)
	if err != nil {
		s.logger.Debug().
			Str("agent_id", req.Msg.AgentId).
			Msg("Agent not found")
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	// Prepare observed endpoints list
	observedEndpoints := []*discoveryv1.Endpoint{}
	if entry.ObservedEndpoint != nil {
		observedEndpoints = append(observedEndpoints, entry.ObservedEndpoint)
	}

	// Build response
	resp := &discoveryv1.LookupAgentResponse{
		AgentId:           entry.MeshID, // We stored agent_id in MeshID field
		MeshId:            entry.MeshID,
		Pubkey:            entry.PubKey,
		Endpoints:         entry.Endpoints,
		ObservedEndpoints: observedEndpoints,
		Nat:               entry.NatHint,
		Metadata:          entry.Metadata,
		LastSeen:          timestamppb.New(entry.LastSeen),
	}

	s.logger.Debug().
		Str("agent_id", req.Msg.AgentId).
		Time("last_seen", entry.LastSeen).
		Msg("Agent lookup successful")

	return connect.NewResponse(resp), nil
}

// extractAgentObservedEndpoint extracts the agent's observed public endpoint from the request.
func (s *Server) extractAgentObservedEndpoint(req *connect.Request[discoveryv1.RegisterAgentRequest]) *discoveryv1.Endpoint {
	// Same logic as colony extraction with validation
	xForwardedFor := req.Header().Get("X-Forwarded-For")
	if xForwardedFor != "" {
		parts := strings.Split(xForwardedFor, ",")
		clientIP := strings.TrimSpace(parts[0])

		if !isValidPublicIPv4(clientIP, s.logger) {
			s.logger.Debug().
				Str("x_forwarded_for", xForwardedFor).
				Str("rejected_ip", clientIP).
				Msg("Rejected invalid/private agent IP from X-Forwarded-For")
		} else {
			return &discoveryv1.Endpoint{
				Ip:       clientIP,
				Port:     0,
				Protocol: "udp",
				ViaRelay: false,
			}
		}
	}

	xRealIP := req.Header().Get("X-Real-IP")
	if xRealIP != "" {
		if !isValidPublicIPv4(xRealIP, s.logger) {
			s.logger.Debug().
				Str("x_real_ip", xRealIP).
				Msg("Rejected invalid/private agent IP from X-Real-IP")
		} else {
			return &discoveryv1.Endpoint{
				Ip:       xRealIP,
				Port:     0,
				Protocol: "udp",
				ViaRelay: false,
			}
		}
	}

	xObservedAddr := req.Header().Get("X-Observed-Addr")
	if xObservedAddr != "" {
		if !isValidPublicIPv4(xObservedAddr, s.logger) {
			s.logger.Debug().
				Str("x_observed_addr", xObservedAddr).
				Msg("Rejected invalid/private agent IP from X-Observed-Addr")
		} else {
			return &discoveryv1.Endpoint{
				Ip:       xObservedAddr,
				Port:     0,
				Protocol: "udp",
				ViaRelay: false,
			}
		}
	}

	return nil
}
