package client

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"

	discoveryv1 "github.com/coral-io/coral/coral/discovery/v1"
	"github.com/coral-io/coral/coral/discovery/v1/discoveryv1connect"
)

// Client wraps the discovery service client.
type Client struct {
	client  discoveryv1connect.DiscoveryServiceClient
	timeout time.Duration
}

// New creates a new discovery client.
func New(endpoint string, timeout time.Duration) *Client {
	httpClient := &http.Client{
		Timeout: timeout,
	}

	return &Client{
		client:  discoveryv1connect.NewDiscoveryServiceClient(httpClient, endpoint),
		timeout: timeout,
	}
}

// NewDiscoveryClient creates a new discovery client with default timeout.
func NewDiscoveryClient(endpoint string) *Client {
	return New(endpoint, 10*time.Second)
}

// RegisterColonyRequest contains the information needed to register a colony.
type RegisterColonyRequest struct {
	MeshID      string
	PublicKey   string
	Endpoints   []string
	MeshIPv4    string
	MeshIPv6    string
	ConnectPort uint32
	Metadata    map[string]string
}

// RegisterColonyResponse contains the registration response.
type RegisterColonyResponse struct {
	Success   bool
	TTL       int32
	ExpiresAt time.Time
}

// RegisterColony registers a colony with the discovery service.
func (c *Client) RegisterColony(ctx context.Context, req *RegisterColonyRequest) (*RegisterColonyResponse, error) {
	protoReq := &discoveryv1.RegisterColonyRequest{
		MeshId:      req.MeshID,
		Pubkey:      req.PublicKey,
		Endpoints:   req.Endpoints,
		MeshIpv4:    req.MeshIPv4,
		MeshIpv6:    req.MeshIPv6,
		ConnectPort: req.ConnectPort,
		Metadata:    req.Metadata,
	}

	resp, err := c.client.RegisterColony(ctx, connect.NewRequest(protoReq))
	if err != nil {
		return nil, fmt.Errorf("failed to register colony: %w", err)
	}

	var expiresAt time.Time
	if resp.Msg.ExpiresAt != nil {
		expiresAt = resp.Msg.ExpiresAt.AsTime()
	}

	return &RegisterColonyResponse{
		Success:   resp.Msg.Success,
		TTL:       resp.Msg.Ttl,
		ExpiresAt: expiresAt,
	}, nil
}

// LookupColonyResponse contains the colony lookup response.
type LookupColonyResponse struct {
	MeshID      string
	Pubkey      string
	Endpoints   []string
	MeshIPv4    string
	MeshIPv6    string
	ConnectPort uint32
	Metadata    map[string]string
	LastSeen    time.Time
}

// LookupColony looks up a colony by mesh ID.
func (c *Client) LookupColony(ctx context.Context, meshID string) (*LookupColonyResponse, error) {
	protoReq := &discoveryv1.LookupColonyRequest{
		MeshId: meshID,
	}

	resp, err := c.client.LookupColony(ctx, connect.NewRequest(protoReq))
	if err != nil {
		return nil, fmt.Errorf("failed to lookup colony: %w", err)
	}

	var lastSeen time.Time
	if resp.Msg.LastSeen != nil {
		lastSeen = resp.Msg.LastSeen.AsTime()
	}

	return &LookupColonyResponse{
		MeshID:      resp.Msg.MeshId,
		Pubkey:      resp.Msg.Pubkey,
		Endpoints:   resp.Msg.Endpoints,
		MeshIPv4:    resp.Msg.MeshIpv4,
		MeshIPv6:    resp.Msg.MeshIpv6,
		ConnectPort: resp.Msg.ConnectPort,
		Metadata:    resp.Msg.Metadata,
		LastSeen:    lastSeen,
	}, nil
}

// Health checks the health of the discovery service.
func (c *Client) Health(ctx context.Context) error {
	_, err := c.client.Health(ctx, connect.NewRequest(&discoveryv1.HealthRequest{}))
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	return nil
}
