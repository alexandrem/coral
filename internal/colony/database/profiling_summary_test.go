package database

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDBForProfiling(t *testing.T) *Database {
	t.Helper()
	tempDir := t.TempDir()
	logger := zerolog.New(os.Stdout)
	db, err := New(tempDir, "test-profiling", logger)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// insertProfileSummarySQL inserts a CPU profile summary using raw SQL to avoid ORM array issues.
func insertProfileSummarySQL(t *testing.T, db *Database, ts time.Time, agentID, serviceName, buildID string, frameIDs []int64, sampleCount uint32) {
	t.Helper()
	stackHash := ComputeStackHash(frameIDs)
	// Format frame IDs as DuckDB array literal.
	arrayLiteral := "["
	for i, id := range frameIDs {
		if i > 0 {
			arrayLiteral += ", "
		}
		arrayLiteral += fmt.Sprintf("%d", id)
	}
	arrayLiteral += "]"

	_, err := db.db.Exec(fmt.Sprintf(`
		INSERT INTO cpu_profile_summaries (timestamp, agent_id, service_name, build_id, stack_hash, stack_frame_ids, sample_count)
		VALUES (?, ?, ?, ?, ?, %s::BIGINT[], ?)
	`, arrayLiteral), ts, agentID, serviceName, buildID, stackHash, sampleCount)
	require.NoError(t, err)
}

func TestGetTopKHotspots_NoData(t *testing.T) {
	db := setupTestDBForProfiling(t)
	ctx := context.Background()

	result, err := db.GetTopKHotspots(ctx, "test-service", time.Now().Add(-5*time.Minute), time.Now(), 5)
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, uint64(0), result.TotalSamples)
	assert.Empty(t, result.Hotspots)
}

func TestGetTopKHotspots_WithData(t *testing.T) {
	db := setupTestDBForProfiling(t)
	ctx := context.Background()

	// Encode some frames.
	frames1, err := db.EncodeStackFrames(ctx, []string{"main", "processOrder", "validateSignature"})
	require.NoError(t, err)
	frames2, err := db.EncodeStackFrames(ctx, []string{"runtime", "gcBgMarkWorker"})
	require.NoError(t, err)

	now := time.Now().Truncate(time.Minute)

	// Insert profiling data using raw SQL.
	insertProfileSummarySQL(t, db, now, "agent-1", "order-processor", "build-abc123", frames1, 100)
	insertProfileSummarySQL(t, db, now, "agent-1", "order-processor", "build-abc123", frames2, 30)

	result, err := db.GetTopKHotspots(ctx, "order-processor", now.Add(-time.Minute), now.Add(time.Minute), 5)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, uint64(130), result.TotalSamples)
	require.Len(t, result.Hotspots, 2)

	// First hotspot should be the one with more samples.
	assert.Equal(t, int32(1), result.Hotspots[0].Rank)
	assert.Equal(t, uint64(100), result.Hotspots[0].SampleCount)
	assert.Equal(t, []string{"main", "processOrder", "validateSignature"}, result.Hotspots[0].Frames)
	assert.InDelta(t, 76.9, result.Hotspots[0].Percentage, 0.1)

	assert.Equal(t, int32(2), result.Hotspots[1].Rank)
	assert.Equal(t, uint64(30), result.Hotspots[1].SampleCount)
}

func TestGetTopKHotspots_RespectsLimit(t *testing.T) {
	db := setupTestDBForProfiling(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Minute)

	// Insert 5 different stacks.
	for i := 0; i < 5; i++ {
		frames, err := db.EncodeStackFrames(ctx, []string{"main", "func" + string(rune('A'+i))})
		require.NoError(t, err)
		insertProfileSummarySQL(t, db, now, "agent-1", "test-svc", "build-1", frames, uint32(100-i*10))
	}

	// Request top 2.
	result, err := db.GetTopKHotspots(ctx, "test-svc", now.Add(-time.Minute), now.Add(time.Minute), 2)
	require.NoError(t, err)
	require.Len(t, result.Hotspots, 2)
}

func TestGetLatestBinaryMetadata(t *testing.T) {
	db := setupTestDBForProfiling(t)
	ctx := context.Background()

	now := time.Now()

	// Insert two builds.
	err := db.UpsertBinaryMetadata(ctx, BinaryMetadata{
		BuildID:     "build-old",
		ServiceName: "test-svc",
		FirstSeen:   now.Add(-2 * time.Hour),
		LastSeen:    now.Add(-1 * time.Hour),
	})
	require.NoError(t, err)

	err = db.UpsertBinaryMetadata(ctx, BinaryMetadata{
		BuildID:     "build-new",
		ServiceName: "test-svc",
		FirstSeen:   now.Add(-30 * time.Minute),
		LastSeen:    now,
	})
	require.NoError(t, err)

	latest, err := db.GetLatestBinaryMetadata(ctx, "test-svc")
	require.NoError(t, err)
	assert.Equal(t, "build-new", latest.BuildID)
}

func TestGetPreviousBinaryMetadata(t *testing.T) {
	db := setupTestDBForProfiling(t)
	ctx := context.Background()

	now := time.Now()

	err := db.UpsertBinaryMetadata(ctx, BinaryMetadata{
		BuildID:     "build-old",
		ServiceName: "test-svc",
		FirstSeen:   now.Add(-2 * time.Hour),
		LastSeen:    now.Add(-1 * time.Hour),
	})
	require.NoError(t, err)

	err = db.UpsertBinaryMetadata(ctx, BinaryMetadata{
		BuildID:     "build-new",
		ServiceName: "test-svc",
		FirstSeen:   now.Add(-30 * time.Minute),
		LastSeen:    now,
	})
	require.NoError(t, err)

	prev, err := db.GetPreviousBinaryMetadata(ctx, "test-svc", "build-new")
	require.NoError(t, err)
	assert.Equal(t, "build-old", prev.BuildID)
}

func TestCompareHotspotsWithBaseline_NewHotspot(t *testing.T) {
	db := setupTestDBForProfiling(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Minute)

	// Old build: only one hotspot.
	framesOld, err := db.EncodeStackFrames(ctx, []string{"main", "handleRequest"})
	require.NoError(t, err)
	insertProfileSummarySQL(t, db, now.Add(-2*time.Hour), "agent-1", "test-svc", "build-old", framesOld, 100)

	// New build: new hotspot appears.
	framesNew, err := db.EncodeStackFrames(ctx, []string{"main", "processOrder", "validateSignature"})
	require.NoError(t, err)
	insertProfileSummarySQL(t, db, now, "agent-1", "test-svc", "build-new", framesNew, 100)

	indicators, err := db.CompareHotspotsWithBaseline(ctx, "test-svc", "build-new", "build-old",
		now.Add(-time.Minute), now.Add(time.Minute), 5)
	require.NoError(t, err)
	require.NotEmpty(t, indicators)

	// Should detect new hotspot.
	found := false
	for _, ind := range indicators {
		if ind.Type == "new_hotspot" {
			found = true
			assert.Greater(t, ind.CurrentPercentage, 0.0)
		}
	}
	assert.True(t, found, "Expected to find a new_hotspot regression indicator")
}

func TestCompareHotspotsWithBaseline_NoPreviousBuild(t *testing.T) {
	db := setupTestDBForProfiling(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Minute)

	// No baseline data - should return empty indicators.
	indicators, err := db.CompareHotspotsWithBaseline(ctx, "test-svc", "build-new", "build-nonexistent",
		now.Add(-time.Minute), now.Add(time.Minute), 5)
	require.NoError(t, err)
	assert.Empty(t, indicators)
}
