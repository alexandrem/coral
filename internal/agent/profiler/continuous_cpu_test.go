//go:build linux

package profiler

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/coral-mesh/coral/internal/agent/debug"
	debugconfig "github.com/coral-mesh/coral/internal/config"
)

// TestContinuousProfilingEndToEnd tests the complete continuous profiling flow.
func TestContinuousProfilingEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Skip if not running as root or without eBPF capabilities.
	// eBPF requires CAP_BPF (or root) and adequate RLIMIT_MEMLOCK.
	if os.Geteuid() != 0 {
		t.Skip("Skipping eBPF test: requires root or CAP_BPF capability")
	}

	// Create temporary database.
	tmpDB := t.TempDir() + "/test.duckdb"
	db, err := sql.Open("duckdb", tmpDB)
	require.NoError(t, err)
	defer db.Close()

	// Create logger.
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()

	// Create a mock session manager with minimal config.
	debugCfg := debugconfig.DebugConfig{
		Enabled: true,
	}
	sessionManager := debug.NewSessionManager(debugCfg, logger, nil)

	// Create continuous profiler with short intervals for testing.
	config := Config{
		Enabled:           true,
		FrequencyHz:       19,
		Interval:          2 * time.Second, // Short interval for testing
		SampleRetention:   1 * time.Hour,
		MetadataRetention: 7 * 24 * time.Hour,
	}

	profiler, err := NewContinuousCPUProfiler(context.Background(), db, sessionManager, logger, config)
	require.NoError(t, err, "Failed to create continuous profiler")

	// Get our own PID to profile.
	pid := os.Getpid()

	// Start profiling ourselves.
	services := []ServiceInfo{
		{
			ServiceID:  "test-service",
			PID:        pid,
			BinaryPath: "/proc/self/exe",
		},
	}

	profiler.Start(services)
	defer profiler.Stop()

	// Wait for at least 2 collection cycles (2s interval * 2 = 4s + buffer).
	logger.Info().Msg("Waiting for continuous profiler to collect samples...")
	time.Sleep(6 * time.Second)

	// Query the storage to verify samples were collected.
	storage := profiler.GetStorage().(*Storage)
	ctx := context.Background()

	// Query all samples using sequence-based polling.
	samples, _, err := storage.QuerySamplesBySeqID(ctx, 0, 10000, "test-service")
	require.NoError(t, err, "Failed to query samples")

	// Verify we got samples.
	require.NotEmpty(t, samples, "No samples were collected - continuous profiling may not be working")

	logger.Info().
		Int("sample_count", len(samples)).
		Msg("Successfully collected continuous profiling samples")

	// Verify sample structure.
	for i, sample := range samples {
		require.NotEmpty(t, sample.StackFrameIDs, "Sample %d has empty stack frame IDs", i)
		require.Positive(t, sample.SampleCount, "Sample %d has zero sample count", i)
		require.NotEmpty(t, sample.BuildID, "Sample %d has empty build ID", i)
		require.Equal(t, "test-service", sample.ServiceID, "Sample %d has wrong service ID", i)

		// Decode and verify we can get frame names.
		frameNames, err := storage.DecodeStackFrames(ctx, sample.StackFrameIDs)
		require.NoError(t, err, "Failed to decode stack frames for sample %d", i)
		require.NotEmpty(t, frameNames, "Sample %d has empty decoded frame names", i)

		// Log first sample for debugging.
		if i == 0 {
			logger.Info().
				Strs("stack", frameNames).
				Uint32("count", sample.SampleCount).
				Msg("First sample stack trace")
		}
	}

	// Test frame dictionary.
	t.Run("FrameDictionary", func(t *testing.T) {
		// Verify frame dictionary was populated.
		var frameCount int
		err := db.QueryRow("SELECT COUNT(*) FROM profile_frame_dictionary_local").Scan(&frameCount)
		require.NoError(t, err)
		require.Positive(t, frameCount, "Frame dictionary is empty")

		logger.Info().Int("frame_count", frameCount).Msg("Frame dictionary populated")
	})

	// Test binary metadata.
	t.Run("BinaryMetadata", func(t *testing.T) {
		// Verify binary metadata was stored.
		var metadataCount int
		err := db.QueryRow("SELECT COUNT(*) FROM binary_metadata_local").Scan(&metadataCount)
		require.NoError(t, err)
		require.Positive(t, metadataCount, "Binary metadata is empty")

		logger.Info().Int("metadata_count", metadataCount).Msg("Binary metadata stored")
	})
}

// TestStorageEncodeDecodeStackFrames tests the stack frame encoding/decoding.
func TestStorageEncodeDecodeStackFrames(t *testing.T) {
	// Create temporary database.
	tmpDB := t.TempDir() + "/test.duckdb"
	db, err := sql.Open("duckdb", tmpDB)
	require.NoError(t, err)
	defer db.Close()

	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()
	storage, err := NewStorage(db, logger)
	require.NoError(t, err)

	ctx := context.Background()

	// Test frame names.
	frameNames := []string{
		"main.main",
		"runtime.main",
		"runtime.goexit",
	}

	// Encode frames.
	frameIDs, err := storage.encodeStackFrames(ctx, frameNames)
	require.NoError(t, err)
	require.Len(t, frameIDs, len(frameNames))

	// Verify IDs are sequential.
	require.Equal(t, int64(1), frameIDs[0])
	require.Equal(t, int64(2), frameIDs[1])
	require.Equal(t, int64(3), frameIDs[2])

	// Encode same frames again - should get same IDs.
	frameIDs2, err := storage.encodeStackFrames(ctx, frameNames)
	require.NoError(t, err)
	require.Equal(t, frameIDs, frameIDs2)

	// Decode frames.
	decodedNames, err := storage.DecodeStackFrames(ctx, frameIDs)
	require.NoError(t, err)
	require.Equal(t, frameNames, decodedNames)

	// Verify frame counts were incremented.
	var frameCount int64
	err = db.QueryRow("SELECT frame_count FROM profile_frame_dictionary_local WHERE frame_id = 1").Scan(&frameCount)
	require.NoError(t, err)
	require.Equal(t, int64(2), frameCount, "Frame count should be 2 (encoded twice)")
}
