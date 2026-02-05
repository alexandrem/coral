package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// insertTestMemoryProfiles is a test helper that inserts memory profile summaries via the ORM.
func insertTestMemoryProfiles(t *testing.T, db *Database, summaries ...MemoryProfileSummary) {
	t.Helper()
	err := db.InsertMemoryProfileSummaries(context.Background(), summaries)
	require.NoError(t, err)
}

func TestInsertMemoryProfileSummaries_Basic(t *testing.T) {
	db := setupTestDBForProfiling(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	insertTestMemoryProfiles(t, db, MemoryProfileSummary{
		Timestamp:     now,
		AgentID:       "agent-1",
		ServiceName:   "order-service",
		BuildID:       "build-abc",
		StackFrameIDs: []int64{1, 2, 3},
		AllocBytes:    1024000,
		AllocObjects:  500,
	})

	results, err := db.QueryMemoryProfileSummaries(ctx, "order-service", now.Add(-time.Minute), now.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "agent-1", results[0].AgentID)
	assert.Equal(t, int64(1024000), results[0].AllocBytes)
	assert.Equal(t, int64(500), results[0].AllocObjects)
	assert.Equal(t, []int64{1, 2, 3}, results[0].StackFrameIDs)
}

func TestInsertMemoryProfileSummaries_Empty(t *testing.T) {
	db := setupTestDBForProfiling(t)
	ctx := context.Background()

	err := db.InsertMemoryProfileSummaries(ctx, nil)
	require.NoError(t, err)

	err = db.InsertMemoryProfileSummaries(ctx, []MemoryProfileSummary{})
	require.NoError(t, err)
}

func TestQueryMemoryProfileSummaries_TimeRange(t *testing.T) {
	db := setupTestDBForProfiling(t)
	ctx := context.Background()

	base := time.Now().Truncate(time.Second)

	insertTestMemoryProfiles(t, db,
		MemoryProfileSummary{Timestamp: base.Add(-2 * time.Hour), AgentID: "agent-1", ServiceName: "svc", BuildID: "b1", StackFrameIDs: []int64{1}, AllocBytes: 100, AllocObjects: 10},
		MemoryProfileSummary{Timestamp: base.Add(-30 * time.Minute), AgentID: "agent-1", ServiceName: "svc", BuildID: "b1", StackFrameIDs: []int64{2}, AllocBytes: 200, AllocObjects: 20},
		MemoryProfileSummary{Timestamp: base, AgentID: "agent-1", ServiceName: "svc", BuildID: "b1", StackFrameIDs: []int64{3}, AllocBytes: 300, AllocObjects: 30},
	)

	// Query last hour only.
	results, err := db.QueryMemoryProfileSummaries(ctx, "svc", base.Add(-1*time.Hour), base.Add(time.Minute))
	require.NoError(t, err)
	assert.Len(t, results, 2)
}

func TestQueryMemoryProfileSummaries_ServiceFilter(t *testing.T) {
	db := setupTestDBForProfiling(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)

	insertTestMemoryProfiles(t, db,
		MemoryProfileSummary{Timestamp: now, AgentID: "agent-1", ServiceName: "svc-a", BuildID: "b1", StackFrameIDs: []int64{1}, AllocBytes: 100, AllocObjects: 10},
		MemoryProfileSummary{Timestamp: now, AgentID: "agent-1", ServiceName: "svc-b", BuildID: "b1", StackFrameIDs: []int64{2}, AllocBytes: 200, AllocObjects: 20},
	)

	results, err := db.QueryMemoryProfileSummaries(ctx, "svc-a", now.Add(-time.Minute), now.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "svc-a", results[0].ServiceName)

	// Empty service name returns all.
	results, err = db.QueryMemoryProfileSummaries(ctx, "", now.Add(-time.Minute), now.Add(time.Minute))
	require.NoError(t, err)
	assert.Len(t, results, 2)
}

func TestCleanupOldMemoryProfiles(t *testing.T) {
	db := setupTestDBForProfiling(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)

	insertTestMemoryProfiles(t, db,
		MemoryProfileSummary{Timestamp: now.Add(-60 * 24 * time.Hour), AgentID: "agent-1", ServiceName: "svc", BuildID: "b1", StackFrameIDs: []int64{1}, AllocBytes: 100, AllocObjects: 10},
		MemoryProfileSummary{Timestamp: now.Add(-1 * time.Hour), AgentID: "agent-1", ServiceName: "svc", BuildID: "b1", StackFrameIDs: []int64{2}, AllocBytes: 200, AllocObjects: 20},
	)

	rowsAffected, err := db.CleanupOldMemoryProfiles(ctx, 30)
	require.NoError(t, err)
	assert.Equal(t, int64(1), rowsAffected)

	// Only recent profile should remain.
	results, err := db.QueryMemoryProfileSummaries(ctx, "", now.Add(-90*24*time.Hour), now.Add(time.Minute))
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, int64(200), results[0].AllocBytes)
}
