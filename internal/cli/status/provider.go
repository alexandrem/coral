package status

import (
	"context"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/cli/helpers"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/constants"
)

// ColonyStatusInfo holds status information for a single colony.
type ColonyStatusInfo struct {
	ColonyID           string `json:"colony_id"`
	Application        string `json:"application"`
	Environment        string `json:"environment"`
	IsDefault          bool   `json:"is_default"`
	Running            bool   `json:"running"`
	Status             string `json:"status"`
	Endpoint           string `json:"endpoint,omitempty"`
	Error              string `json:"error,omitempty"`
	UptimeSeconds      int64  `json:"uptime_seconds,omitempty"`
	AgentCount         int32  `json:"agent_count,omitempty"`
	ActiveAgentCount   int32  `json:"active_agent_count,omitempty"`
	DegradedAgentCount int32  `json:"degraded_agent_count,omitempty"`
	WireGuardPort      int    `json:"wireguard_port"`
	ConnectPort        int    `json:"connect_port"`
	LocalEndpoint      string `json:"local_endpoint,omitempty"`
	MeshEndpoint       string `json:"mesh_endpoint,omitempty"`
	PublicEndpointURL  string `json:"public_endpoint_url,omitempty"`
	MeshIPv4           string `json:"mesh_ipv4"`
	WireGuardPubkey    string `json:"wireguard_pubkey,omitempty"`
}

// Provider handles querying colony status.
type Provider struct {
	loader *config.Loader
}

// NewProvider creates a new status provider.
func NewProvider(loader *config.Loader) *Provider {
	return &Provider{
		loader: loader,
	}
}

// QueryColoniesInParallel queries all colonies concurrently with timeout.
func (p *Provider) QueryColoniesInParallel(colonyIDs []string, defaultColony string) []ColonyStatusInfo {
	var wg sync.WaitGroup
	results := make([]ColonyStatusInfo, len(colonyIDs))

	for i, colonyID := range colonyIDs {
		wg.Add(1)
		go func(index int, id string) {
			defer wg.Done()
			results[index] = p.QueryColonyStatus(id, defaultColony)
		}(i, colonyID)
	}

	wg.Wait()
	return results
}

// QueryColonyStatus queries a single colony's status with timeout.
func (p *Provider) QueryColonyStatus(colonyID string, defaultColony string) ColonyStatusInfo {
	info := ColonyStatusInfo{
		ColonyID:  colonyID,
		IsDefault: colonyID == defaultColony,
		Status:    "unknown",
		Running:   false,
	}

	// Load colony config
	cfg, err := p.loader.LoadColonyConfig(colonyID)
	if err != nil {
		info.Error = err.Error()
		return info
	}

	info.Application = cfg.ApplicationName
	info.Environment = cfg.Environment
	info.WireGuardPort = cfg.WireGuard.Port
	info.MeshIPv4 = cfg.WireGuard.MeshIPv4
	info.WireGuardPubkey = cfg.WireGuard.PublicKey

	// Get connect port
	connectPort := cfg.Services.ConnectPort
	if connectPort == 0 {
		connectPort = constants.DefaultColonyPort
	}
	info.ConnectPort = connectPort

	info.Endpoint = cfg.Remote.Endpoint
	if info.Endpoint == "" {
		info.Endpoint = fmt.Sprintf("http://localhost:%d", connectPort)
	}
	client, err := helpers.GetColonyClient(colonyID)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := client.GetStatus(ctx, connect.NewRequest(&colonyv1.GetStatusRequest{}))
	if err != nil {
		info.Status = "unreachable"
		info.Error = err.Error()
		return info
	}
	if resp.Msg.ColonyId != "" && resp.Msg.ColonyId != colonyID {
		info.Error = fmt.Sprintf("endpoint returned colony %q, expected %q", resp.Msg.ColonyId, colonyID)
		return info
	}
	info.Status = resp.Msg.Status
	if info.Status == "" {
		info.Status = "running"
	}
	info.Running = info.Status != "stopped"
	info.UptimeSeconds = resp.Msg.UptimeSeconds
	info.AgentCount = resp.Msg.AgentCount
	info.ActiveAgentCount = resp.Msg.ActiveAgentCount
	info.DegradedAgentCount = resp.Msg.DegradedAgentCount
	if cfg.Remote.Endpoint == "" {
		info.LocalEndpoint = info.Endpoint
	}
	if resp.Msg.MeshIpv4 != "" {
		info.MeshEndpoint = fmt.Sprintf("http://%s:%d", resp.Msg.MeshIpv4, resp.Msg.ConnectPort)
	}
	info.PublicEndpointURL = resp.Msg.PublicEndpointUrl
	return info
}
