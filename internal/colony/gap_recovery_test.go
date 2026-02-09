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
)

func setupGapRecoveryTestDB(t *testing.T) *database.Database {
	t.Helper()
	tempDir := t.TempDir()
	logger := zerolog.Nop()
	db, err := database.New(tempDir, "test-gap-recovery", logger)
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
	metrics := []*agentv1.SystemMetric{
		{Name: "cpu_usage", Value: 50.0, Unit: "%", MetricType: "gauge", Timestamp: time.Now().UnixMilli(), Attributes: "{}"},
		{Name: "cpu_usage", Value: 80.0, Unit: "%", MetricType: "gauge", Timestamp: time.Now().UnixMilli(), Attributes: "{}"},
		{Name: "cpu_usage", Value: 70.0, Unit: "%", MetricType: "gauge", Timestamp: time.Now().UnixMilli(), Attributes: "{}"},
		{Name: "mem_bytes", Value: 1024, Unit: "bytes", MetricType: "counter", Timestamp: time.Now().UnixMilli(), Attributes: "{}"},
		{Name: "mem_bytes", Value: 2048, Unit: "bytes", MetricType: "counter", Timestamp: time.Now().UnixMilli(), Attributes: "{}"},
	}

	summaries := aggregateSystemMetricsForRecovery("agent-1", metrics)
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

func TestAggregateSystemMetricsForRecovery_Empty(t *testing.T) {
	summaries := aggregateSystemMetricsForRecovery("agent-1", nil)
	assert.Nil(t, summaries)

	summaries = aggregateSystemMetricsForRecovery("agent-1", []*agentv1.SystemMetric{})
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
			StackFrames: []string{"main.go:10", "handler.go:20"},
			SampleCount: 5,
			ServiceName: "api-service",
		},
		{
			Timestamp:   timestamppb.New(now),
			BuildId:     "build-1",
			StackFrames: []string{"main.go:10", "handler.go:20"},
			SampleCount: 3,
			ServiceName: "api-service",
		},
	}

	summaries := svc.aggregateCPUProfileForRecovery(ctx, "agent-1", samples)
	require.Len(t, summaries, 1, "Same stack should be merged")
	assert.Equal(t, "agent-1", summaries[0].AgentID)
	assert.Equal(t, "api-service", summaries[0].ServiceName)
	assert.Equal(t, uint32(8), summaries[0].SampleCount, "Sample counts should be summed")
	assert.NotEmpty(t, summaries[0].StackHash)
	assert.NotEmpty(t, summaries[0].StackFrameIDs)
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

	summaries := svc.aggregateMemoryProfileForRecovery(ctx, "agent-1", samples)
	require.Len(t, summaries, 1, "Same stack should be merged")
	assert.Equal(t, "agent-1", summaries[0].AgentID)
	assert.Equal(t, int64(3072), summaries[0].AllocBytes, "Alloc bytes should be summed")
	assert.Equal(t, int64(30), summaries[0].AllocObjects, "Alloc objects should be summed")
}
