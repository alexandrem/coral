package proxy

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/discovery/client"
)

// ColonyInfo holds colony connection information.
type ColonyInfo struct {
	MeshID      string
	Pubkey      string
	Endpoints   []string
	MeshIPv4    string
	MeshIPv6    string
	ConnectPort uint32
	Metadata    map[string]string
}

// LookupColony queries the discovery service for colony information.
func LookupColony(ctx context.Context, discoveryEndpoint string, meshID string, logger zerolog.Logger) (*ColonyInfo, error) {
	logger.Info().
		Str("discovery_endpoint", discoveryEndpoint).
		Str("mesh_id", meshID).
		Msg("Looking up colony in discovery service")

	// Create discovery client.
	discoveryClient := client.New(discoveryEndpoint)

	// Lookup colony.
	resp, err := discoveryClient.LookupColony(ctx, meshID)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup colony: %w", err)
	}

	info := &ColonyInfo{
		MeshID:      resp.MeshID,
		Pubkey:      resp.Pubkey,
		Endpoints:   resp.Endpoints,
		MeshIPv4:    resp.MeshIPv4,
		MeshIPv6:    resp.MeshIPv6,
		ConnectPort: resp.ConnectPort,
		Metadata:    resp.Metadata,
	}

	logger.Info().
		Str("mesh_id", info.MeshID).
		Str("mesh_ipv4", info.MeshIPv4).
		Str("mesh_ipv6", info.MeshIPv6).
		Uint32("connect_port", info.ConnectPort).
		Int("endpoints", len(info.Endpoints)).
		Msg("Colony found")

	return info, nil
}
