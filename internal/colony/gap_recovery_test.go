package colony

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/constants"
)

func setupGapRecoveryTestDB(t *testing.T) *database.Database {
	t.Helper()
	tempDir := t.TempDir()
	logger := zerolog.Nop()
	db, err := database.New(tempDir, "test-gap-recovery", constants.DefaultConnectionsCacheTTL, logger)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func setupTestRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	return registry.New(nil)
}

func TestGapRecoveryService_StartStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db := setupGapRecoveryTestDB(t)
	reg := setupTestRegistry(t)
	logger := zerolog.Nop()

	svc := NewGapRecoveryService(ctx, reg, db, logger)

	err := svc.Start()
	require.NoError(t, err)

	// Should be running.
	assert.True(t, svc.IsRunning())

	err = svc.Stop()
	require.NoError(t, err)

	assert.False(t, svc.IsRunning())
}

func TestGapRecoveryService_NoPendingGaps(t *testing.T) {
	ctx := context.Background()
	db := setupGapRecoveryTestDB(t)
	reg := setupTestRegistry(t)
	logger := zerolog.Nop()

	svc := NewGapRecoveryService(ctx, reg, db, logger)

	// PollOnce with no gaps should return nil.
	err := svc.PollOnce(ctx)
	require.NoError(t, err)
}

func TestGapRecoveryService_AgentNotFound(t *testing.T) {
	ctx := context.Background()
	db := setupGapRecoveryTestDB(t)
	reg := setupTestRegistry(t)
	logger := zerolog.Nop()

	// Record a gap for a non-existent agent.
	require.NoError(t, db.RecordSequenceGap(ctx, "missing-agent", "telemetry", 5, 10))

	svc := NewGapRecoveryService(ctx, reg, db, logger)

	// PollOnce should skip the gap (agent not in registry).
	err := svc.PollOnce(ctx)
	require.NoError(t, err)

	// Gap should still be pending (not recovered, not permanent).
	gaps, err := db.GetPendingSequenceGaps(ctx, 10)
	require.NoError(t, err)
	assert.Len(t, gaps, 1)
	assert.Equal(t, "detected", gaps[0].Status)
}

func TestGapRecoveryService_AgentUnhealthy(t *testing.T) {
	ctx := context.Background()
	db := setupGapRecoveryTestDB(t)
	reg := setupTestRegistry(t)
	logger := zerolog.Nop()

	// Register an agent but make it unhealthy (LastSeen far in the past).
	_, err := reg.Register("agent-1", "test-agent", "10.0.0.1", "", nil, nil, "1.0")
	require.NoError(t, err)

	// Manually set LastSeen to make it unhealthy.
	agent, err := reg.Get("agent-1")
	require.NoError(t, err)
	agent.LastSeen = time.Now().Add(-10 * time.Minute) // Well past unhealthy threshold.

	// Record a gap.
	require.NoError(t, db.RecordSequenceGap(ctx, "agent-1", "telemetry", 5, 10))

	svc := NewGapRecoveryService(ctx, reg, db, logger)

	// PollOnce should skip the gap (agent unhealthy).
	err = svc.PollOnce(ctx)
	require.NoError(t, err)

	// Gap should still be pending.
	gaps, err := db.GetPendingSequenceGaps(ctx, 10)
	require.NoError(t, err)
	assert.Len(t, gaps, 1)
}

func TestGapRecoveryService_RunCleanup(t *testing.T) {
	ctx := context.Background()
	db := setupGapRecoveryTestDB(t)
	reg := setupTestRegistry(t)
	logger := zerolog.Nop()

	// Record and recover a gap.
	require.NoError(t, db.RecordSequenceGap(ctx, "agent-1", "telemetry", 1, 5))
	gaps, _ := db.GetPendingSequenceGaps(ctx, 10)
	require.Len(t, gaps, 1)
	require.NoError(t, db.MarkGapRecovered(ctx, gaps[0].ID))

	svc := NewGapRecoveryService(ctx, reg, db, logger)

	// RunCleanup should not error.
	err := svc.RunCleanup(ctx)
	require.NoError(t, err)
}

// --- Aggregation Helper Tests ---

func TestAggregateSystemMetricsForRecovery(t *testing.T) {
	ts := time.Date(2026, 1, 1, 10, 0, 5, 0, time.UTC).UnixMilli()
	metrics := []*agentv1.SystemMetric{
		{Name: "cpu_usage", Value: 50.0, Unit: "%", MetricType: "gauge", Timestamp: ts, Attributes: "{}"},
		{Name: "cpu_usage", Value: 80.0, Unit: "%", MetricType: "gauge", Timestamp: ts + 1000, Attributes: "{}"},
		{Name: "cpu_usage", Value: 70.0, Unit: "%", MetricType: "gauge", Timestamp: ts + 2000, Attributes: "{}"},
		{Name: "mem_bytes", Value: 1024, Unit: "bytes", MetricType: "counter", Timestamp: ts, Attributes: "{}"},
		{Name: "mem_bytes", Value: 2048, Unit: "bytes", MetricType: "counter", Timestamp: ts + 1000, Attributes: "{}"},
	}

	summaries := aggregateSystemMetrics("agent-1", metrics)
	require.Len(t, summaries, 2)

	// Find cpu_usage summary.
	var cpuSummary, memSummary *database.SystemMetricsSummary
	for i := range summaries {
		if summaries[i].MetricName == "cpu_usage" {
			cpuSummary = &summaries[i]
		}
		if summaries[i].MetricName == "mem_bytes" {
			memSummary = &summaries[i]
		}
	}

	require.NotNil(t, cpuSummary)
	assert.Equal(t, "agent-1", cpuSummary.AgentID)
	assert.Equal(t, 50.0, cpuSummary.MinValue)
	assert.Equal(t, 80.0, cpuSummary.MaxValue)
	assert.InDelta(t, 66.67, cpuSummary.AvgValue, 0.1)
	assert.Equal(t, uint64(3), cpuSummary.SampleCount)
	assert.Equal(t, 0.0, cpuSummary.DeltaValue, "gauge should have 0 delta")

	require.NotNil(t, memSummary)
	assert.Equal(t, 1024.0, memSummary.DeltaValue, "counter delta should be max-min")
}

// TestAggregateSystemMetricsForRecovery_MultiMinuteGap verifies that a gap
// spanning several minutes produces one bucket per minute instead of
// collapsing every sample into the earliest minute.
func TestAggregateSystemMetricsForRecovery_MultiMinuteGap(t *testing.T) {
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	// 30 minutes of data, 4 samples per minute (15s interval). Minute i has value i.
	var metrics []*agentv1.SystemMetric
	for minute := 0; minute < 30; minute++ {
		for s := 0; s < 4; s++ {
			ts := start.Add(time.Duration(minute)*time.Minute + time.Duration(s)*15*time.Second)
			metrics = append(metrics, &agentv1.SystemMetric{
				Name: "cpu_usage", Value: float64(minute), Unit: "%", MetricType: "gauge",
				Timestamp: ts.UnixMilli(), Attributes: "{}",
			})
		}
	}

	summaries := aggregateSystemMetrics("agent-1", metrics)
	require.Len(t, summaries, 30, "expected one bucket per minute")

	byBucket := make(map[time.Time]database.SystemMetricsSummary, len(summaries))
	for _, s := range summaries {
		byBucket[s.BucketTime.UTC()] = s
	}
	for minute := 0; minute < 30; minute++ {
		bucket := start.Add(time.Duration(minute) * time.Minute)
		s, ok := byBucket[bucket]
		require.True(t, ok, "missing bucket %s", bucket)
		assert.Equal(t, uint64(4), s.SampleCount)
		assert.Equal(t, float64(minute), s.MinValue)
		assert.Equal(t, float64(minute), s.MaxValue)
	}
}

func TestAggregateSystemMetricsForRecovery_Empty(t *testing.T) {
	summaries := aggregateSystemMetrics("agent-1", nil)
	assert.Nil(t, summaries)

	summaries = aggregateSystemMetrics("agent-1", []*agentv1.SystemMetric{})
	assert.Nil(t, summaries)
}

func TestAggregateCPUProfileForRecovery(t *testing.T) {
	ctx := context.Background()
	db := setupGapRecoveryTestDB(t)
	reg := setupTestRegistry(t)
	logger := zerolog.Nop()

	svc := NewGapRecoveryService(ctx, reg, db, logger)

	now := time.Now().Truncate(time.Minute)
	samples := []*agentv1.CPUProfileSample{
		{
			Timestamp:   timestamppb.New(now),
			BuildId:     "build-1",
			Tgid:        42,
			StackFrames: []string{"main.go:10", "handler.go:20"},
			SampleCount: 5,
			ServiceName: "api-service",
		},
		{
			Timestamp:   timestamppb.New(now),
			BuildId:     "build-1",
			Tgid:        42,
			StackFrames: []string{"main.go:10", "handler.go:20"},
			SampleCount: 3,
			ServiceName: "api-service",
		},
	}

	summaries := aggregateCPUProfileSamples(ctx, svc.db, svc.logger, "agent-1", samples)
	require.Len(t, summaries, 1, "Same stack should be merged")
	assert.Equal(t, "agent-1", summaries[0].AgentID)
	assert.Equal(t, "api-service", summaries[0].ServiceName)
	assert.Equal(t, uint32(8), summaries[0].SampleCount, "Sample counts should be summed")
	assert.NotEmpty(t, summaries[0].StackHash)
	assert.NotEmpty(t, summaries[0].StackFrameIDs)
	assert.Equal(t, uint32(42), summaries[0].TGID, "TGID should be preserved")

	// The same stack in another process must not be merged.
	samples = append(samples, &agentv1.CPUProfileSample{
		Timestamp:   timestamppb.New(now),
		BuildId:     "build-1",
		Tgid:        43,
		StackFrames: []string{"main.go:10", "handler.go:20"},
		SampleCount: 2,
		ServiceName: "api-service",
	})
	summaries = aggregateCPUProfileSamples(ctx, svc.db, svc.logger, "agent-1", samples)
	require.Len(t, summaries, 2, "Different TGIDs should not be merged")
}

func TestAggregateMemoryProfileForRecovery(t *testing.T) {
	ctx := context.Background()
	db := setupGapRecoveryTestDB(t)
	reg := setupTestRegistry(t)
	logger := zerolog.Nop()

	svc := NewGapRecoveryService(ctx, reg, db, logger)

	now := time.Now().Truncate(time.Minute)
	samples := []*agentv1.MemoryProfileSample{
		{
			Timestamp:    timestamppb.New(now),
			BuildId:      "build-1",
			StackFrames:  []string{"alloc.go:5", "main.go:15"},
			AllocBytes:   1024,
			AllocObjects: 10,
			ServiceName:  "api-service",
		},
		{
			Timestamp:    timestamppb.New(now),
			BuildId:      "build-1",
			StackFrames:  []string{"alloc.go:5", "main.go:15"},
			AllocBytes:   2048,
			AllocObjects: 20,
			ServiceName:  "api-service",
		},
	}

	summaries := aggregateMemoryProfileSamples(ctx, svc.db, svc.logger, "agent-1", samples)
	require.Len(t, summaries, 1, "Same stack should be merged")
	assert.Equal(t, "agent-1", summaries[0].AgentID)
	assert.Equal(t, int64(3072), summaries[0].AllocBytes, "Alloc bytes should be summed")
	assert.Equal(t, int64(30), summaries[0].AllocObjects, "Alloc objects should be summed")
}
