package helpers

import (
	"context"
	"fmt"

	"connectrpc.com/connect"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	discoveryclient "github.com/coral-mesh/coral/internal/discovery/client"
)

// LookupColony queries the discovery service for colony information.
func LookupColony(
	ctx context.Context,
	client *discoveryclient.Client,
	meshID string,
) (*discoveryclient.LookupColonyResponse, error) {
	resp, err := client.LookupColony(ctx, meshID)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup colony: %w", err)
	}

	return resp, nil
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
