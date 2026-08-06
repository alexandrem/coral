package database

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coral-mesh/coral/internal/constants"
)

func newTestDB(t *testing.T) *Database {
	t.Helper()
	db, err := New(t.TempDir(), "test-topo", constants.DefaultConnectionsCacheTTL, zerolog.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestUpsertTopologyConnections_Insert verifies that a fresh batch is persisted
// as a single row per edge key.
func TestUpsertTopologyConnections_Insert(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)

	err := db.UpsertTopologyConnections(ctx, []TopologyConnection{
		{
			SourceAgentID: "agent-a",
			DestAgentID:   "agent-b",
			DestIP:        "10.0.0.2",
			DestPort:      5432,
			Protocol:      "tcp",
			BytesSent:     100,
			BytesReceived: 200,
			Retransmits:   1,
			RTTUS:         500,
			FirstObserved: now,
			LastObserved:  now,
		},
	})
	require.NoError(t, err)

	rows, err := db.GetL4Connections(ctx, now.Add(-time.Minute))
	require.NoError(t, err)
	require.Len(t, rows, 1)

	r := rows[0]
	assert.Equal(t, "agent-a", r.SourceAgentID)
	assert.Equal(t, "agent-b", r.DestAgentID)
	assert.Equal(t, "10.0.0.2", r.DestIP)
	assert.Equal(t, 5432, r.DestPort)
	assert.Equal(t, "tcp", r.Protocol)
	assert.Equal(t, uint64(100), r.BytesSent)
	assert.Equal(t, uint64(200), r.BytesReceived)
	assert.Equal(t, 1, r.Retransmits)
	assert.Equal(t, 500, r.RTTUS)
}

// TestUpsertTopologyConnections_MetricAccumulation verifies that a second batch
// for the same edge accumulates bytes and retransmits, and refreshes last_observed.
func TestUpsertTopologyConnections_MetricAccumulation(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	t1 := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	t2 := time.Now().UTC().Truncate(time.Millisecond)

	base := TopologyConnection{
		SourceAgentID: "agent-a",
		DestAgentID:   "agent-b",
		DestIP:        "10.0.0.2",
		DestPort:      5432,
		Protocol:      "tcp",
	}

	// First batch.
	first := base
	first.BytesSent = 100
	first.BytesReceived = 200
	first.Retransmits = 1
	first.FirstObserved = t1
	first.LastObserved = t1
	require.NoError(t, db.UpsertTopologyConnections(ctx, []TopologyConnection{first}))

	// Second batch — same edge key, new metrics and later timestamp.
	second := base
	second.BytesSent = 50
	second.BytesReceived = 80
	second.Retransmits = 2
	second.FirstObserved = t2
	second.LastObserved = t2
	require.NoError(t, db.UpsertTopologyConnections(ctx, []TopologyConnection{second}))

	rows, err := db.GetL4Connections(ctx, t1.Add(-time.Minute))
	require.NoError(t, err)
	require.Len(t, rows, 1, "same edge key must produce exactly one row")

	r := rows[0]
	// Metrics are accumulated across batches.
	assert.Equal(t, uint64(150), r.BytesSent, "bytes_sent should be accumulated")
	assert.Equal(t, uint64(280), r.BytesReceived, "bytes_received should be accumulated")
	assert.Equal(t, 3, r.Retransmits, "retransmits should be accumulated")
	// last_observed advances to the most recent batch timestamp.
	assert.True(t, !r.LastObserved.Before(t2), "last_observed should be refreshed to the later timestamp")
}

// TestUpsertTopologyConnections_LastObservedFilter verifies that GetL4Connections
// excludes rows whose last_observed is older than the provided since time.
func TestUpsertTopologyConnections_LastObservedFilter(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	recent := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)

	// Old edge — should be filtered out.
	require.NoError(t, db.UpsertTopologyConnections(ctx, []TopologyConnection{
		{
			SourceAgentID: "agent-a",
			DestIP:        "10.0.0.2",
			DestPort:      80,
			Protocol:      "tcp",
			FirstObserved: old,
			LastObserved:  old,
		},
	}))

	// Recent edge — should appear.
	require.NoError(t, db.UpsertTopologyConnections(ctx, []TopologyConnection{
		{
			SourceAgentID: "agent-b",
			DestIP:        "10.0.0.3",
			DestPort:      443,
			Protocol:      "tcp",
			FirstObserved: recent,
			LastObserved:  recent,
		},
	}))

	since := time.Now().UTC().Add(-time.Hour)
	rows, err := db.GetL4Connections(ctx, since)
	require.NoError(t, err)
	require.Len(t, rows, 1, "only the recent edge should be returned")
	assert.Equal(t, "agent-b", rows[0].SourceAgentID)
}

// TestUpsertTopologyConnections_ExternalEdge verifies that an edge with an empty
// DestAgentID (external destination) is stored and retrieved correctly.
func TestUpsertTopologyConnections_ExternalEdge(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)

	require.NoError(t, db.UpsertTopologyConnections(ctx, []TopologyConnection{
		{
			SourceAgentID: "agent-a",
			DestAgentID:   "", // External destination — no matching agent.
			DestIP:        "8.8.8.8",
			DestPort:      443,
			Protocol:      "tcp",
			FirstObserved: now,
			LastObserved:  now,
		},
	}))

	rows, err := db.GetL4Connections(ctx, now.Add(-time.Minute))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "", rows[0].DestAgentID, "external edge should have empty DestAgentID")
	assert.Equal(t, "8.8.8.8", rows[0].DestIP)
}

// TestUpsertTopologyConnections_EmptyBatch verifies that an empty batch is a no-op.
func TestUpsertTopologyConnections_EmptyBatch(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	err := db.UpsertTopologyConnections(ctx, nil)
	require.NoError(t, err)

	rows, err := db.GetL4Connections(ctx, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.Empty(t, rows)
}
