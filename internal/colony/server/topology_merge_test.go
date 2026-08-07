package server

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
)

// TestGetTopology_L4OnlyEdge verifies that an L4-only edge (no matching L7
// trace) appears in the topology response with EvidenceLayer = EVIDENCE_LAYER_L4_NETWORK.
func TestGetTopology_L4OnlyEdge(t *testing.T) {
	srv, cleanup := newTestServer(t, Config{ColonyID: "test-merge"})
	defer cleanup()

	ctx := context.Background()
	since := time.Now().Add(-time.Hour)

	// Insert an L4 edge directly into topology_connections.
	err := srv.database.UpsertTopologyConnections(ctx, []database.TopologyConnection{
		{
			SourceAgentID: "agent-a",
			DestAgentID:   "agent-b",
			DestIP:        "10.100.0.2",
			DestPort:      5432,
			Protocol:      "tcp",
			FirstObserved: since,
			LastObserved:  time.Now(),
		},
	})
	require.NoError(t, err)

	resp, err := srv.GetTopology(ctx, connect.NewRequest(&colonyv1.GetTopologyRequest{}))
	require.NoError(t, err)

	conns := resp.Msg.Connections
	require.Len(t, conns, 1, "expected exactly one connection")

	c := conns[0]
	assert.Equal(t, "agent-a", c.SourceId)
	assert.Equal(t, "agent-b", c.TargetId)
	assert.Equal(t, colonyv1.EvidenceLayer_EVIDENCE_LAYER_L4_NETWORK, c.EvidenceLayer)
}

// TestGetTopology_L4ExternalEdge verifies that an L4 edge with no matching
// agent in the registry uses the dest IP as the target ID.
func TestGetTopology_L4ExternalEdge(t *testing.T) {
	srv, cleanup := newTestServer(t, Config{ColonyID: "test-external"})
	defer cleanup()

	ctx := context.Background()
	since := time.Now().Add(-time.Hour)

	err := srv.database.UpsertTopologyConnections(ctx, []database.TopologyConnection{
		{
			SourceAgentID: "agent-a",
			DestAgentID:   "", // No matching agent — external destination.
			DestIP:        "8.8.8.8",
			DestPort:      443,
			Protocol:      "tcp",
			FirstObserved: since,
			LastObserved:  time.Now(),
		},
	})
	require.NoError(t, err)

	resp, err := srv.GetTopology(ctx, connect.NewRequest(&colonyv1.GetTopologyRequest{}))
	require.NoError(t, err)

	require.Len(t, resp.Msg.Connections, 1)
	c := resp.Msg.Connections[0]

	// Target should fall back to dest IP when no agent ID is available.
	assert.Equal(t, "8.8.8.8", c.TargetId)
	assert.Equal(t, colonyv1.EvidenceLayer_EVIDENCE_LAYER_L4_NETWORK, c.EvidenceLayer)
}

// TestGetTopology_EmptyReturnsNoConnections verifies no connections are returned
// when neither L7 traces nor L4 observations are present.
func TestGetTopology_EmptyReturnsNoConnections(t *testing.T) {
	srv, cleanup := newTestServer(t, Config{ColonyID: "test-empty"})
	defer cleanup()

	resp, err := srv.GetTopology(context.Background(), connect.NewRequest(&colonyv1.GetTopologyRequest{}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.Connections)
}
