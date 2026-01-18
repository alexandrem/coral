package status

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
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
		Status:    "stopped",
		Running:   false,
	}

	// Load colony config
	cfg, err := p.loader.LoadColonyConfig(colonyID)
	if err != nil {
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

	// Try to query running colony (quick timeout)
	baseURL := fmt.Sprintf("http://localhost:%d", connectPort)
	client := colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	req := connect.NewRequest(&colonyv1.GetStatusRequest{})
	resp, err := client.GetStatus(ctx, req)
	if err == nil && resp.Msg != nil {
		// Colony is running
		info.Running = true
		info.Status = resp.Msg.Status
		info.UptimeSeconds = resp.Msg.UptimeSeconds
		info.AgentCount = resp.Msg.AgentCount
		info.ActiveAgentCount = resp.Msg.ActiveAgentCount
		info.DegradedAgentCount = resp.Msg.DegradedAgentCount
		info.LocalEndpoint = fmt.Sprintf("http://localhost:%d", resp.Msg.ConnectPort)
		info.MeshEndpoint = fmt.Sprintf("http://%s:%d", resp.Msg.MeshIpv4, resp.Msg.ConnectPort)
		info.PublicEndpointURL = resp.Msg.PublicEndpointUrl
	}

	return info
}
