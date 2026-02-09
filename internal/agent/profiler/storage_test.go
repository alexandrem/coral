package profiler

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestProfilerStorage(t *testing.T) (*Storage, func()) {
	t.Helper()

	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}

	logger := zerolog.Nop()
	storage, err := NewStorage(db, logger)
	if err != nil {
		_ = db.Close()
		t.Fatalf("Failed to create storage: %v", err)
	}

	cleanup := func() { _ = db.Close() }
	return storage, cleanup
}

func TestQuerySamplesBySeqID(t *testing.T) {
	storage, cleanup := setupTestProfilerStorage(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Insert 5 CPU profile samples.
	samples := make([]ProfileSample, 5)
	for i := range samples {
		samples[i] = ProfileSample{
			Timestamp:     now.Add(-time.Duration(5-i) * time.Second),
			ServiceID:     "api-service",
			BuildID:       "build-1",
			StackHash:     "hash-" + string(rune('a'+i)),
			StackFrameIDs: []int64{1, 2, 3},
			SampleCount:   uint32(i + 1),
		}
	}
	require.NoError(t, storage.StoreSamples(ctx, samples))

	// Query all (seq_id > 0).
	results, maxSeqID, err := storage.QuerySamplesBySeqID(ctx, 0, 100, "")
	require.NoError(t, err)
	assert.Len(t, results, 5)
	assert.Greater(t, maxSeqID, uint64(0))

	// Query after max should return empty.
	results2, maxSeqID2, err := storage.QuerySamplesBySeqID(ctx, maxSeqID, 100, "")
	require.NoError(t, err)
	assert.Len(t, results2, 0)
	assert.Equal(t, uint64(0), maxSeqID2)

	// Insert 2 more and query from previous max.
	moreSamples := []ProfileSample{
		{
			Timestamp:     now,
			ServiceID:     "api-service",
			BuildID:       "build-1",
			StackHash:     "hash-x",
			StackFrameIDs: []int64{4, 5},
			SampleCount:   10,
		},
		{
			Timestamp:     now,
			ServiceID:     "api-service",
			BuildID:       "build-1",
			StackHash:     "hash-y",
			StackFrameIDs: []int64{6, 7},
			SampleCount:   20,
		},
	}
	require.NoError(t, storage.StoreSamples(ctx, moreSamples))

	results3, maxSeqID3, err := storage.QuerySamplesBySeqID(ctx, maxSeqID, 100, "")
	require.NoError(t, err)
	assert.Len(t, results3, 2)
	assert.Greater(t, maxSeqID3, maxSeqID)
}

func TestQueryMemorySamplesBySeqID(t *testing.T) {
	storage, cleanup := setupTestProfilerStorage(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Insert 3 memory profile samples.
	samples := []MemoryProfileSample{
		{
			Timestamp:     now.Add(-3 * time.Second),
			ServiceID:     "api-service",
			BuildID:       "build-1",
			StackHash:     "mem-hash-a",
			StackFrameIDs: []int64{1, 2},
			AllocBytes:    1024,
			AllocObjects:  10,
		},
		{
			Timestamp:     now.Add(-2 * time.Second),
			ServiceID:     "api-service",
			BuildID:       "build-1",
			StackHash:     "mem-hash-b",
			StackFrameIDs: []int64{3, 4},
			AllocBytes:    2048,
			AllocObjects:  20,
		},
		{
			Timestamp:     now.Add(-1 * time.Second),
			ServiceID:     "api-service",
			BuildID:       "build-1",
			StackHash:     "mem-hash-c",
			StackFrameIDs: []int64{5, 6},
			AllocBytes:    4096,
			AllocObjects:  30,
		},
	}
	require.NoError(t, storage.StoreMemorySamples(ctx, samples))

	// Query all (seq_id > 0).
	results, maxSeqID, err := storage.QueryMemorySamplesBySeqID(ctx, 0, 100, "")
	require.NoError(t, err)
	assert.Len(t, results, 3)
	assert.Greater(t, maxSeqID, uint64(0))

	// Verify seq_ids are monotonically increasing.
	var prevSeqID uint64
	for _, r := range results {
		assert.Greater(t, r.SeqID, prevSeqID, "seq_ids must be monotonically increasing")
		prevSeqID = r.SeqID
	}

	// Query after max should return empty.
	results2, _, err := storage.QueryMemorySamplesBySeqID(ctx, maxSeqID, 100, "")
	require.NoError(t, err)
	assert.Len(t, results2, 0)
}
