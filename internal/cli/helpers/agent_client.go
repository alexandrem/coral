package helpers

import (
	"net/http"

	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
)

// GetAgentClient creates an AgentServiceClient for the specified colony.
func GetAgentClient(colonyID string) (agentv1connect.AgentServiceClient, error) {
	url, err := GetColonyURL(colonyID)
	if err != nil {
		return nil, err
	}

	client := agentv1connect.NewAgentServiceClient(
		http.DefaultClient,
		url,
	)

	return client, nil
}
