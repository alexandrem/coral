package profiler

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
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

func TestStoreSamplesMergesAndAccumulates(t *testing.T) {
	storage, cleanup := setupTestProfilerStorage(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	sample := func(hash string, count uint32) ProfileSample {
		return ProfileSample{
			Timestamp:     now,
			ServiceID:     "api-service",
			BuildID:       "build-1",
			TGID:          42,
			StackHash:     hash,
			StackFrameIDs: []int64{1, 2},
			SampleCount:   count,
		}
	}

	// Duplicate keys within one batch are summed into a single row.
	require.NoError(t, storage.StoreSamples(ctx, []ProfileSample{
		sample("a", 1), sample("b", 2), sample("a", 3),
	}))

	// A later batch adds to existing rows and inserts new ones.
	require.NoError(t, storage.StoreSamples(ctx, []ProfileSample{
		sample("a", 10), sample("c", 5),
	}))

	results, _, err := storage.QuerySamplesBySeqID(ctx, 0, 100, "")
	require.NoError(t, err)
	require.Len(t, results, 3)

	seqIDs := make([]uint64, 0, len(results))
	for _, r := range results {
		seqIDs = append(seqIDs, r.SeqID)
		assert.Equal(t, uint32(42), r.TGID)
		assert.Equal(t, []int64{1, 2}, r.StackFrameIDs)
	}

	// Only inserted rows consume seq_ids, so the sequence has no gaps.
	assert.Equal(t, []uint64{seqIDs[0], seqIDs[0] + 1, seqIDs[0] + 2}, seqIDs)

	var countA uint32
	require.NoError(t, storage.db.QueryRow(
		"SELECT sample_count FROM cpu_profile_samples_local WHERE stack_hash = 'a'",
	).Scan(&countA))
	assert.Equal(t, uint32(14), countA)
}

func TestStoreMemorySamplesMergesAndAccumulates(t *testing.T) {
	storage, cleanup := setupTestProfilerStorage(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	sample := func(hash string, bytes int64) MemoryProfileSample {
		return MemoryProfileSample{
			Timestamp:     now,
			ServiceID:     "api-service",
			BuildID:       "build-1",
			StackHash:     hash,
			StackFrameIDs: []int64{3},
			AllocBytes:    bytes,
			AllocObjects:  1,
		}
	}

	require.NoError(t, storage.StoreMemorySamples(ctx, []MemoryProfileSample{
		sample("a", 100), sample("a", 50), sample("b", 10),
	}))
	require.NoError(t, storage.StoreMemorySamples(ctx, []MemoryProfileSample{
		sample("a", 1), sample("c", 7),
	}))

	results, _, err := storage.QueryMemorySamplesBySeqID(ctx, 0, 100, "")
	require.NoError(t, err)
	require.Len(t, results, 3)
	assert.Equal(t, results[0].SeqID+1, results[1].SeqID)
	assert.Equal(t, results[1].SeqID+1, results[2].SeqID)

	var bytesA, objectsA int64
	require.NoError(t, storage.db.QueryRow(
		"SELECT alloc_bytes, alloc_objects FROM memory_profile_samples_local WHERE stack_hash = 'a'",
	).Scan(&bytesA, &objectsA))
	assert.Equal(t, int64(151), bytesA)
	assert.Equal(t, int64(3), objectsA)
}

func TestEncodeStackFramesBatchesNewFrames(t *testing.T) {
	storage, cleanup := setupTestProfilerStorage(t)
	defer cleanup()

	ctx := context.Background()

	// A recursive stack repeats an unseen frame; it must get a single ID.
	ids, err := storage.encodeStackFrames(ctx, []string{"main.fib", "main.fib", "main.main"})
	require.NoError(t, err)
	assert.Equal(t, []int64{1, 1, 2}, ids)

	// Known frames resolve from the cache; new ones extend the dictionary.
	ids, err = storage.encodeStackFrames(ctx, []string{"main.main", "main.handler", "main.fib"})
	require.NoError(t, err)
	assert.Equal(t, []int64{2, 3, 1}, ids)

	names, err := storage.DecodeStackFrames(ctx, ids)
	require.NoError(t, err)
	assert.Equal(t, []string{"main.main", "main.handler", "main.fib"}, names)

	var rows int
	require.NoError(t, storage.db.QueryRow("SELECT count(*) FROM profile_frame_dictionary_local").Scan(&rows))
	assert.Equal(t, 3, rows)

	// A new storage reloads the dictionary and continues the ID sequence.
	reloaded, err := NewStorage(storage.db, zerolog.Nop())
	require.NoError(t, err)
	ids, err = reloaded.encodeStackFrames(ctx, []string{"main.fib", "main.worker"})
	require.NoError(t, err)
	assert.Equal(t, []int64{1, 4}, ids)
}
