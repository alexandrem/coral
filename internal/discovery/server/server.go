package server

import (
	"context"
	"fmt"
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
	registry  *registry.Registry
	version   string
	startTime time.Time
	logger    zerolog.Logger
}

// New creates a new discovery server.
func New(reg *registry.Registry, version string, logger zerolog.Logger) *Server {
	return &Server{
		registry:  reg,
		version:   version,
		startTime: time.Now(),
		logger:    logger,
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

	// Log incoming registration request
	s.logger.Info().
		Str("mesh_id", req.Msg.MeshId).
		Str("pubkey", req.Msg.Pubkey).
		Strs("endpoints", req.Msg.Endpoints).
		Str("mesh_ipv4", req.Msg.MeshIpv4).
		Str("mesh_ipv6", req.Msg.MeshIpv6).
		Uint32("connect_port", req.Msg.ConnectPort).
		Interface("metadata", req.Msg.Metadata).
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
		Success:   true,
		Ttl:       ttlSeconds,
		ExpiresAt: timestamppb.New(entry.ExpiresAt),
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

	// Build response
	resp := &discoveryv1.LookupColonyResponse{
		MeshId:      entry.MeshID,
		Pubkey:      entry.PubKey,
		Endpoints:   entry.Endpoints,
		MeshIpv4:    entry.MeshIPv4,
		MeshIpv6:    entry.MeshIPv6,
		ConnectPort: entry.ConnectPort,
		Metadata:    entry.Metadata,
		LastSeen:    timestamppb.New(entry.LastSeen),
	}

	s.logger.Debug().
		Str("mesh_id", entry.MeshID).
		Time("last_seen", entry.LastSeen).
		Msg("Colony lookup successful")

	return connect.NewResponse(resp), nil
}

// Health handles health check requests
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
