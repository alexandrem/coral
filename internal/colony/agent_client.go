// Package colony implements the central coordinator for distributed agents.
package colony

import (
	"fmt"
	"net"
	"net/http"
	"strconv"

	"connectrpc.com/connect"

	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/constants"
)

// GetAgentClient creates a gRPC client for communicating with an agent over the mesh network.
// The agent must be registered in the registry to get its mesh IP address.
func GetAgentClient(agent *registry.Entry) agentv1connect.AgentServiceClient {
	agentAddr := net.JoinHostPort(agent.MeshIPv4, strconv.Itoa(constants.DefaultAgentPort))
	baseURL := fmt.Sprintf("http://%s", agentAddr)

	// Create Connect client for agent service.
	client := agentv1connect.NewAgentServiceClient(
		http.DefaultClient,
		baseURL,
	)

	return client
}

// GetDebugClient creates a gRPC client for communicating with an agent's debug service over the mesh network.
// This is a factory function that can be used by pollers and other colony components.
func GetDebugClient(httpClient connect.HTTPClient, url string, opts ...connect.ClientOption) agentv1connect.AgentDebugServiceClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	// Create Connect client for debug service.
	return agentv1connect.NewAgentDebugServiceClient(httpClient, url, opts...)
}
