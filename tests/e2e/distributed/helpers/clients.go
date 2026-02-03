package helpers

import (
	"net/http"

	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	discoveryclient "github.com/coral-mesh/coral/internal/discovery/client"
)

// NewDiscoveryClient creates a new discovery service client.
// Uses JSON encoding for compatibility with Cloudflare Workers (which only supports JSON).
func NewDiscoveryClient(endpoint string) *discoveryclient.Client {
	return discoveryclient.New(endpoint)
}

// NewColonyClient creates a new colony service client.
func NewColonyClient(endpoint string) colonyv1connect.ColonyServiceClient {
	return colonyv1connect.NewColonyServiceClient(
		http.DefaultClient,
		endpoint,
	)
}

// NewAgentClient creates a new agent service client.
func NewAgentClient(endpoint string) agentv1connect.AgentServiceClient {
	return agentv1connect.NewAgentServiceClient(
		http.DefaultClient,
		endpoint,
	)
}

// NewAgentDebugClient creates a new agent debug service client.
func NewAgentDebugClient(endpoint string) agentv1connect.AgentDebugServiceClient {
	return agentv1connect.NewAgentDebugServiceClient(
		http.DefaultClient,
		endpoint,
	)
}

// NewDebugClient creates a new debug service client.
func NewDebugClient(endpoint string) colonyv1connect.ColonyDebugServiceClient {
	return colonyv1connect.NewColonyDebugServiceClient(
		http.DefaultClient,
		endpoint,
	)
}
