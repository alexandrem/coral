package server

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	discoveryv1 "github.com/coral-io/coral/coral/discovery/v1"
	"github.com/coral-io/coral/coral/discovery/v1/discoveryv1connect"
	"github.com/coral-io/coral/internal/discovery/registry"
)

// Server implements the DiscoveryService
type Server struct {
	registry  *registry.Registry
	version   string
	startTime time.Time
}

// New creates a new discovery server
func New(reg *registry.Registry, version string) *Server {
	return &Server{
		registry:  reg,
		version:   version,
		startTime: time.Now(),
	}
}

// Ensure Server implements the DiscoveryServiceHandler interface
var _ discoveryv1connect.DiscoveryServiceHandler = (*Server)(nil)

// RegisterColony handles colony registration requests
func (s *Server) RegisterColony(
	ctx context.Context,
	req *connect.Request[discoveryv1.RegisterColonyRequest],
) (*connect.Response[discoveryv1.RegisterColonyResponse], error) {
	// Validate request
	if req.Msg.MeshId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("mesh_id is required"))
	}
	if req.Msg.Pubkey == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("pubkey is required"))
	}
	if len(req.Msg.Endpoints) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("at least one endpoint is required"))
	}

	// Register the colony
	entry, err := s.registry.Register(
		req.Msg.MeshId,
		req.Msg.Pubkey,
		req.Msg.Endpoints,
		req.Msg.Metadata,
	)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Build response
	ttlSeconds := int32(entry.ExpiresAt.Sub(entry.LastSeen).Seconds())
	resp := &discoveryv1.RegisterColonyResponse{
		Success:   true,
		Ttl:       ttlSeconds,
		ExpiresAt: timestamppb.New(entry.ExpiresAt),
	}

	return connect.NewResponse(resp), nil
}

// LookupColony handles colony lookup requests
func (s *Server) LookupColony(
	ctx context.Context,
	req *connect.Request[discoveryv1.LookupColonyRequest],
) (*connect.Response[discoveryv1.LookupColonyResponse], error) {
	// Validate request
	if req.Msg.MeshId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("mesh_id is required"))
	}

	// Lookup the colony
	entry, err := s.registry.Lookup(req.Msg.MeshId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	// Build response
	resp := &discoveryv1.LookupColonyResponse{
		MeshId:    entry.MeshID,
		Pubkey:    entry.PubKey,
		Endpoints: entry.Endpoints,
		Metadata:  entry.Metadata,
		LastSeen:  timestamppb.New(entry.LastSeen),
	}

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
