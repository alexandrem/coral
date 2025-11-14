package colony

import (
	"fmt"
	"net"
	"net/http"

	"github.com/coral-io/coral/coral/agent/v1/agentv1connect"
	"github.com/coral-io/coral/internal/colony/registry"
)

// GetAgentClient creates a gRPC client for communicating with an agent over the mesh network.
// The agent must be registered in the registry to get its mesh IP address.
func GetAgentClient(agent *registry.Entry) agentv1connect.AgentServiceClient {
	// Agent gRPC API is exposed on port 9001.
	agentAddr := net.JoinHostPort(agent.MeshIPv4, "9001")
	baseURL := fmt.Sprintf("http://%s", agentAddr)

	// Create Connect client for agent service.
	client := agentv1connect.NewAgentServiceClient(
		http.DefaultClient,
		baseURL,
	)

	return client
}
