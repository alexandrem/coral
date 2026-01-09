package helpers

import (
	"net/http"

	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/coral/discovery/v1/discoveryv1connect"
)

// NewDiscoveryClient creates a new discovery service client.
func NewDiscoveryClient(endpoint string) discoveryv1connect.DiscoveryServiceClient {
	return discoveryv1connect.NewDiscoveryServiceClient(
		http.DefaultClient,
		endpoint,
	)
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

// NewDebugClient creates a new debug service client.
func NewDebugClient(endpoint string) colonyv1connect.ColonyDebugServiceClient {
	return colonyv1connect.NewColonyDebugServiceClient(
		http.DefaultClient,
		endpoint,
	)
}
