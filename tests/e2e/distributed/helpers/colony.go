package helpers

import (
	"context"
	"fmt"

	"connectrpc.com/connect"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	discoveryv1 "github.com/coral-mesh/coral/coral/discovery/v1"
	"github.com/coral-mesh/coral/coral/discovery/v1/discoveryv1connect"
)

// LookupColony queries the discovery service for colony information.
func LookupColony(ctx context.Context, client discoveryv1connect.DiscoveryServiceClient, meshID string) (*discoveryv1.LookupColonyResponse, error) {
	req := connect.NewRequest(&discoveryv1.LookupColonyRequest{
		MeshId: meshID,
	})

	resp, err := client.LookupColony(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup colony: %w", err)
	}

	return resp.Msg, nil
}

// GetColonyStatus queries the colony for its status.
func GetColonyStatus(ctx context.Context, client colonyv1connect.ColonyServiceClient) (*colonyv1.GetStatusResponse, error) {
	req := connect.NewRequest(&colonyv1.GetStatusRequest{})

	resp, err := client.GetStatus(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get colony status: %w", err)
	}

	return resp.Msg, nil
}

// ListAgents queries the colony for registered agents.
func ListAgents(ctx context.Context, client colonyv1connect.ColonyServiceClient) (*colonyv1.ListAgentsResponse, error) {
	req := connect.NewRequest(&colonyv1.ListAgentsRequest{})

	resp, err := client.ListAgents(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list agents: %w", err)
	}

	return resp.Msg, nil
}
