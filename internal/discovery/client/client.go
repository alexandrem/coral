// Package client provides a client for the discovery service.
package client

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"

	discoveryv1 "github.com/coral-mesh/coral/coral/discovery/v1"
	"github.com/coral-mesh/coral/coral/discovery/v1/discoveryv1connect"
)

const (
	defaultTimeout = 10 * time.Second
)

// Option defines a function signature for configuring the client.
type Option func(*Client)

// WithTimeout allows users to provide a custom timeout.
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		c.timeout = timeout
	}
}

// WithHttpClient allows users to override the http client.
func WithHttpClient(httpClient *http.Client) Option {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

// Client wraps the discovery service client.
type Client struct {
	client     discoveryv1connect.DiscoveryServiceClient
	timeout    time.Duration
	httpClient *http.Client
}

// New creates a raw Connect client for the discovery service.
// Uses JSON encoding for compatibility with Cloudflare Workers.
func New(endpoint string, opts ...Option) *Client {
	c := &Client{
		timeout:    defaultTimeout,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(c)
	}

	c.httpClient.Timeout = c.timeout

	c.client = discoveryv1connect.NewDiscoveryServiceClient(
		c.httpClient,
		endpoint,
		connect.WithProtoJSON(),
	)

	return c
}

// RegisterColonyRequest contains the information needed to register a colony.
type RegisterColonyRequest struct {
	MeshID           string
	PublicKey        string
	Endpoints        []string
	MeshIPv4         string
	MeshIPv6         string
	ConnectPort      uint32
	PublicPort       uint32 // Public HTTPS port for bootstrap (e.g., 8443).
	Metadata         map[string]string
	ObservedEndpoint interface{} // *discoveryv1.Endpoint (using interface{} to avoid import issues)
	PublicEndpoint   interface{} // *discoveryv1.PublicEndpointInfo (RFD 085)
}

// RegisterColonyResponse contains the registration response.
type RegisterColonyResponse struct {
	Success          bool
	TTL              int32
	ExpiresAt        time.Time
	ObservedEndpoint *Endpoint
}

// RegisterColony registers a colony with the discovery service.
func (c *Client) RegisterColony(ctx context.Context, req *RegisterColonyRequest) (*RegisterColonyResponse, error) {
	// Convert observed endpoint if provided.
	var observedEndpoint *discoveryv1.Endpoint
	if req.ObservedEndpoint != nil {
		if ep, ok := req.ObservedEndpoint.(*discoveryv1.Endpoint); ok {
			observedEndpoint = ep
		}
	}

	// Convert public endpoint if provided (RFD 085).
	var publicEndpoint *discoveryv1.PublicEndpointInfo
	if req.PublicEndpoint != nil {
		if ep, ok := req.PublicEndpoint.(*discoveryv1.PublicEndpointInfo); ok {
			publicEndpoint = ep
		}
	}

	protoReq := &discoveryv1.RegisterColonyRequest{
		MeshId:           req.MeshID,
		Pubkey:           req.PublicKey,
		Endpoints:        req.Endpoints,
		MeshIpv4:         req.MeshIPv4,
		MeshIpv6:         req.MeshIPv6,
		ConnectPort:      req.ConnectPort,
		PublicPort:       req.PublicPort,
		Metadata:         req.Metadata,
		ObservedEndpoint: observedEndpoint,
		PublicEndpoint:   publicEndpoint,
	}

	resp, err := c.client.RegisterColony(ctx, connect.NewRequest(protoReq))
	if err != nil {
		return nil, fmt.Errorf("failed to register colony: %w", err)
	}

	var expiresAt time.Time
	if resp.Msg.ExpiresAt != nil {
		expiresAt = resp.Msg.ExpiresAt.AsTime()
	}

	var respObserved *Endpoint
	if resp.Msg.ObservedEndpoint != nil {
		respObserved = &Endpoint{
			IP:       resp.Msg.ObservedEndpoint.Ip,
			Port:     resp.Msg.ObservedEndpoint.Port,
			Protocol: resp.Msg.ObservedEndpoint.Protocol,
			ViaRelay: resp.Msg.ObservedEndpoint.ViaRelay,
		}
	}
	return &RegisterColonyResponse{
		Success:          resp.Msg.Success,
		TTL:              resp.Msg.Ttl,
		ExpiresAt:        expiresAt,
		ObservedEndpoint: respObserved,
	}, nil
}

// LookupColonyResponse contains the colony lookup response.
type LookupColonyResponse struct {
	MeshID            string
	Pubkey            string
	Endpoints         []string
	ObservedEndpoints []Endpoint
	MeshIPv4          string
	MeshIPv6          string
	ConnectPort       uint32
	PublicPort        uint32 // Public HTTPS port for bootstrap (e.g., 8443).
	Metadata          map[string]string
	LastSeen          time.Time
	PublicEndpoint    *PublicEndpointInfo // RFD 085: public HTTPS endpoint for CLI access.
}

// PublicEndpointInfo contains public endpoint information for CLI access (RFD 085).
type PublicEndpointInfo struct {
	Enabled       bool
	URL           string
	CACert        string // Base64-encoded PEM.
	CAFingerprint string // sha256:hex format.
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

	// Convert public endpoint info (RFD 085).
	var publicEndpoint *PublicEndpointInfo
	if resp.Msg.PublicEndpoint != nil && resp.Msg.PublicEndpoint.Enabled {
		// Format CA fingerprint as sha256:hex.
		var caFingerprint string
		if resp.Msg.PublicEndpoint.CaFingerprint != nil {
			caFingerprint = fmt.Sprintf("sha256:%x", resp.Msg.PublicEndpoint.CaFingerprint.Value)
		}
		publicEndpoint = &PublicEndpointInfo{
			Enabled:       resp.Msg.PublicEndpoint.Enabled,
			URL:           resp.Msg.PublicEndpoint.Url,
			CACert:        resp.Msg.PublicEndpoint.CaCert,
			CAFingerprint: caFingerprint,
		}
	}

	var observedEndpoints []Endpoint
	for _, endpoint := range resp.Msg.ObservedEndpoints {
		if endpoint != nil {
			observedEndpoints = append(observedEndpoints, Endpoint{
				IP:       endpoint.Ip,
				Port:     endpoint.Port,
				Protocol: endpoint.Protocol,
				ViaRelay: endpoint.ViaRelay,
			})
		}
	}

	return &LookupColonyResponse{
		MeshID:            resp.Msg.MeshId,
		Pubkey:            resp.Msg.Pubkey,
		Endpoints:         resp.Msg.Endpoints,
		ObservedEndpoints: observedEndpoints,
		MeshIPv4:          resp.Msg.MeshIpv4,
		MeshIPv6:          resp.Msg.MeshIpv6,
		ConnectPort:       resp.Msg.ConnectPort,
		PublicPort:        resp.Msg.PublicPort,
		Metadata:          resp.Msg.Metadata,
		LastSeen:          lastSeen,
		PublicEndpoint:    publicEndpoint,
	}, nil
}

// HealthResponse is the health request response.
type HealthResponse struct {
	Status             string
	Version            string
	UptimeSeconds      int64
	RegisteredColonies int32
}

// Health checks the health of the discovery service.
func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	resp, err := c.client.Health(ctx, connect.NewRequest(&discoveryv1.HealthRequest{}))
	if err != nil {
		return nil, fmt.Errorf("health check failed: %w", err)
	}
	return &HealthResponse{
		Status:             resp.Msg.Status,
		Version:            resp.Msg.Version,
		UptimeSeconds:      resp.Msg.UptimeSeconds,
		RegisteredColonies: resp.Msg.RegisteredColonies,
	}, nil
}

// Endpoint represents a network endpoint for connectivity.
type Endpoint struct {
	IP       string
	Port     uint32
	Protocol string
	ViaRelay bool
}

// toProto converts the Endpoint to its proto representation.
func (e *Endpoint) toProto() *discoveryv1.Endpoint {
	if e == nil {
		return nil
	}
	return &discoveryv1.Endpoint{
		Ip:       e.IP,
		Port:     e.Port,
		Protocol: e.Protocol,
		ViaRelay: e.ViaRelay,
	}
}

// endpointFromProto converts a proto Endpoint to the wrapper type.
func endpointFromProto(ep *discoveryv1.Endpoint) *Endpoint {
	if ep == nil {
		return nil
	}
	return &Endpoint{
		IP:       ep.Ip,
		Port:     ep.Port,
		Protocol: ep.Protocol,
		ViaRelay: ep.ViaRelay,
	}
}

// CreateBootstrapTokenRequest contains the information needed to create a bootstrap token.
type CreateBootstrapTokenRequest struct {
	ReefID   string
	ColonyID string
	AgentID  string
	Intent   string // e.g., "register", "renew"
}

// CreateBootstrapTokenResponse contains the bootstrap token response.
type CreateBootstrapTokenResponse struct {
	JWT       string
	ExpiresAt time.Time
}

// CreateBootstrapToken requests a bootstrap token from the discovery service (RFD 047/049).
func (c *Client) CreateBootstrapToken(ctx context.Context, req *CreateBootstrapTokenRequest) (*CreateBootstrapTokenResponse, error) {
	protoReq := &discoveryv1.CreateBootstrapTokenRequest{
		ReefId:   req.ReefID,
		ColonyId: req.ColonyID,
		AgentId:  req.AgentID,
		Intent:   req.Intent,
	}

	resp, err := c.client.CreateBootstrapToken(ctx, connect.NewRequest(protoReq))
	if err != nil {
		return nil, fmt.Errorf("failed to create bootstrap token: %w", err)
	}

	return &CreateBootstrapTokenResponse{
		JWT:       resp.Msg.Jwt,
		ExpiresAt: time.Unix(resp.Msg.ExpiresAt, 0),
	}, nil
}

// RegisterAgentRequest contains the information needed to register an agent.
type RegisterAgentRequest struct {
	AgentID          string
	MeshID           string
	Pubkey           string
	Endpoints        []string
	ObservedEndpoint *Endpoint
	Metadata         map[string]string
}

// RegisterAgentResponse contains the agent registration response.
type RegisterAgentResponse struct {
	Success          bool
	TTL              int32
	ExpiresAt        time.Time
	ObservedEndpoint *Endpoint
	StunServers      []string
}

// RegisterAgent registers an agent with the discovery service.
func (c *Client) RegisterAgent(ctx context.Context, req *RegisterAgentRequest) (*RegisterAgentResponse, error) {
	protoReq := &discoveryv1.RegisterAgentRequest{
		AgentId:          req.AgentID,
		MeshId:           req.MeshID,
		Pubkey:           req.Pubkey,
		Endpoints:        req.Endpoints,
		ObservedEndpoint: req.ObservedEndpoint.toProto(),
		Metadata:         req.Metadata,
	}

	resp, err := c.client.RegisterAgent(ctx, connect.NewRequest(protoReq))
	if err != nil {
		return nil, fmt.Errorf("failed to register agent: %w", err)
	}

	var expiresAt time.Time
	if resp.Msg.ExpiresAt != nil {
		expiresAt = resp.Msg.ExpiresAt.AsTime()
	}

	return &RegisterAgentResponse{
		Success:          resp.Msg.Success,
		TTL:              resp.Msg.Ttl,
		ExpiresAt:        expiresAt,
		ObservedEndpoint: endpointFromProto(resp.Msg.ObservedEndpoint),
		StunServers:      resp.Msg.StunServers,
	}, nil
}

// RequestRelayRequest contains the information needed to request a relay.
type RequestRelayRequest struct {
	MeshID       string
	AgentPubkey  string
	ColonyPubkey string
}

// RequestRelayResponse contains the relay allocation response.
type RequestRelayResponse struct {
	RelayEndpoint *Endpoint
	SessionID     string
	ExpiresAt     time.Time
	RelayID       string
}

// RequestRelay requests a relay allocation for NAT traversal.
func (c *Client) RequestRelay(ctx context.Context, req *RequestRelayRequest) (*RequestRelayResponse, error) {
	protoReq := &discoveryv1.RequestRelayRequest{
		MeshId:       req.MeshID,
		AgentPubkey:  req.AgentPubkey,
		ColonyPubkey: req.ColonyPubkey,
	}

	resp, err := c.client.RequestRelay(ctx, connect.NewRequest(protoReq))
	if err != nil {
		return nil, fmt.Errorf("failed to request relay: %w", err)
	}

	var expiresAt time.Time
	if resp.Msg.ExpiresAt != nil {
		expiresAt = resp.Msg.ExpiresAt.AsTime()
	}

	return &RequestRelayResponse{
		RelayEndpoint: endpointFromProto(resp.Msg.RelayEndpoint),
		SessionID:     resp.Msg.SessionId,
		ExpiresAt:     expiresAt,
		RelayID:       resp.Msg.RelayId,
	}, nil
}

// LookupAgentResponse contains the agent lookup response.
type LookupAgentResponse struct {
	AgentID           string
	MeshID            string
	Pubkey            string
	Endpoints         []string
	ObservedEndpoints []*Endpoint
	Metadata          map[string]string
	LastSeen          time.Time
}

// LookupAgent looks up an agent by agent ID and mesh ID.
func (c *Client) LookupAgent(ctx context.Context, agentID, meshID string) (*LookupAgentResponse, error) {
	protoReq := &discoveryv1.LookupAgentRequest{
		AgentId: agentID,
		MeshId:  meshID,
	}

	resp, err := c.client.LookupAgent(ctx, connect.NewRequest(protoReq))
	if err != nil {
		return nil, fmt.Errorf("failed to lookup agent: %w", err)
	}

	var lastSeen time.Time
	if resp.Msg.LastSeen != nil {
		lastSeen = resp.Msg.LastSeen.AsTime()
	}

	var observedEndpoints []*Endpoint
	for _, ep := range resp.Msg.ObservedEndpoints {
		observedEndpoints = append(observedEndpoints, endpointFromProto(ep))
	}

	return &LookupAgentResponse{
		AgentID:           resp.Msg.AgentId,
		MeshID:            resp.Msg.MeshId,
		Pubkey:            resp.Msg.Pubkey,
		Endpoints:         resp.Msg.Endpoints,
		ObservedEndpoints: observedEndpoints,
		Metadata:          resp.Msg.Metadata,
		LastSeen:          lastSeen,
	}, nil
}
