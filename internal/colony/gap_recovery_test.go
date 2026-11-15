package colony

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
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

// gapRecoveryAgent serves real RPC responses while tests inspect persisted gap status.
type gapRecoveryAgent struct {
	agentv1connect.UnimplementedAgentServiceHandler
	agentv1connect.UnimplementedAgentDebugServiceHandler
	metrics      func(*agentv1.QuerySystemMetricsRequest) (*agentv1.QuerySystemMetricsResponse, error)
	profileError string
}

func (a *gapRecoveryAgent) QuerySystemMetrics(_ context.Context, req *connect.Request[agentv1.QuerySystemMetricsRequest]) (*connect.Response[agentv1.QuerySystemMetricsResponse], error) {
	if a.metrics == nil {
		return connect.NewResponse(&agentv1.QuerySystemMetricsResponse{}), nil
	}
	resp, err := a.metrics(req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *gapRecoveryAgent) QueryTelemetry(context.Context, *connect.Request[agentv1.QueryTelemetryRequest]) (*connect.Response[agentv1.QueryTelemetryResponse], error) {
	return connect.NewResponse(&agentv1.QueryTelemetryResponse{}), nil
}

func (a *gapRecoveryAgent) QueryEbpfMetrics(context.Context, *connect.Request[agentv1.QueryEbpfMetricsRequest]) (*connect.Response[agentv1.QueryEbpfMetricsResponse], error) {
	return connect.NewResponse(&agentv1.QueryEbpfMetricsResponse{}), nil
}

func (a *gapRecoveryAgent) QueryCPUProfileSamples(context.Context, *connect.Request[agentv1.QueryCPUProfileSamplesRequest]) (*connect.Response[agentv1.QueryCPUProfileSamplesResponse], error) {
	return connect.NewResponse(&agentv1.QueryCPUProfileSamplesResponse{Error: a.profileError}), nil
}

func (a *gapRecoveryAgent) QueryMemoryProfileSamples(context.Context, *connect.Request[agentv1.QueryMemoryProfileSamplesRequest]) (*connect.Response[agentv1.QueryMemoryProfileSamplesResponse], error) {
	return connect.NewResponse(&agentv1.QueryMemoryProfileSamplesResponse{Error: a.profileError}), nil
}

type gapRecoveryHTTPClient struct {
	client *http.Client
	target *url.URL
}

func (c gapRecoveryHTTPClient) Do(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = c.target.Scheme
	req.URL.Host = c.target.Host
	return c.client.Do(req)
}

func setupGapRecoveryAgent(t *testing.T, handler *gapRecoveryAgent) *GapRecoveryService {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(agentv1connect.NewAgentServiceHandler(handler))
	mux.Handle(agentv1connect.NewAgentDebugServiceHandler(handler))
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	reg := setupTestRegistry(t)
	_, err = reg.Register("agent-1", "test-agent", "127.0.0.1", "", nil, nil, "1.0")
	require.NoError(t, err)
	svc := NewGapRecoveryService(context.Background(), reg, setupGapRecoveryTestDB(t), zerolog.Nop())
	svc.httpClient = gapRecoveryHTTPClient{client: server.Client(), target: target}
	return svc
}

func requireGapStatus(t *testing.T, svc *GapRecoveryService, dataType, expected string) {
	t.Helper()
	var status string
	var recoveredAt *time.Time
	err := svc.db.DB().QueryRow("SELECT status, recovered_at FROM sequence_gaps WHERE data_type = ?", dataType).Scan(&status, &recoveredAt)
	require.NoError(t, err)
	require.Equal(t, expected, status)
	if expected == "recovered" {
		require.NotNil(t, recoveredAt)
	} else {
		require.Nil(t, recoveredAt)
	}
}

func TestGapRecoveryService_Pagination(t *testing.T) {
	for _, pageSize := range []int{gapMaxSize, 3000} {
		t.Run(fmt.Sprintf("page_size_%d", pageSize), func(t *testing.T) {
			ctx := context.Background()
			const start, end = uint64(51), uint64(25050)
			now := time.Now().Truncate(time.Minute)
			next := start
			calls := 0
			svc := setupGapRecoveryAgent(t, &gapRecoveryAgent{metrics: func(req *agentv1.QuerySystemMetricsRequest) (*agentv1.QuerySystemMetricsResponse, error) {
				require.Equal(t, next-1, req.StartSeqId)
				require.Equal(t, int32(min(gapMaxSize, int(end-next+1))), req.MaxRecords)
				calls++
				resp := &agentv1.QuerySystemMetricsResponse{}
				for range min(pageSize, int(req.MaxRecords)) {
					resp.Metrics = append(resp.Metrics, &agentv1.SystemMetric{
						SeqId: next, Name: "cpu", Value: 42, Unit: "%", MetricType: "gauge", Attributes: "{}", Timestamp: now.UnixMilli(),
					})
					next++
				}
				return resp, nil
			}})
			require.NoError(t, svc.db.RecordSequenceGap(ctx, "agent-1", systemMetricsDataType, start, end))
			require.NoError(t, svc.PollOnce(ctx))
			requireGapStatus(t, svc, systemMetricsDataType, "recovered")
			require.Equal(t, end+1, next)
			require.GreaterOrEqual(t, calls, 3)
			summaries, err := svc.db.QuerySystemMetricsSummaries(ctx, "agent-1", now.Add(-time.Minute), now.Add(time.Minute))
			require.NoError(t, err)
			require.Len(t, summaries, 1)
			require.Equal(t, end-start+1, summaries[0].SampleCount, "pages sharing a bucket must not overwrite each other")
		})
	}
}

func TestGapRecoveryService_EmptySourceIsPermanent(t *testing.T) {
	svc := setupGapRecoveryAgent(t, &gapRecoveryAgent{})
	ctx := context.Background()
	for dataType := range recoveryRegistry {
		require.NoError(t, svc.db.RecordSequenceGap(ctx, "agent-1", dataType, 5, 10))
	}
	require.NoError(t, svc.PollOnce(ctx))
	for dataType := range recoveryRegistry {
		requireGapStatus(t, svc, dataType, "permanent")
	}
}

func TestGapRecoveryService_ProfileErrorsAreRetried(t *testing.T) {
	svc := setupGapRecoveryAgent(t, &gapRecoveryAgent{profileError: "storage unavailable"})
	ctx := context.Background()
	for _, dataType := range []string{cpuProfileDataType, memoryProfileDataType, "unsupported"} {
		require.NoError(t, svc.db.RecordSequenceGap(ctx, "agent-1", dataType, 5, 10))
	}
	for attempt := 1; attempt <= gapRecoveryMaxAttempts; attempt++ {
		require.NoError(t, svc.PollOnce(ctx))
		status := "recovering"
		if attempt == gapRecoveryMaxAttempts {
			status = "permanent"
		}
		for _, dataType := range []string{cpuProfileDataType, memoryProfileDataType, "unsupported"} {
			requireGapStatus(t, svc, dataType, status)
		}
	}
}

func TestGapRecoveryService_LaterPageFailure(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired_%t", expired), func(t *testing.T) {
			ctx := context.Background()
			svc := setupGapRecoveryAgent(t, &gapRecoveryAgent{metrics: func(req *agentv1.QuerySystemMetricsRequest) (*agentv1.QuerySystemMetricsResponse, error) {
				if req.StartSeqId == 4 {
					return &agentv1.QuerySystemMetricsResponse{Metrics: []*agentv1.SystemMetric{{SeqId: 5, Name: "cpu"}}}, nil
				}
				if expired {
					return &agentv1.QuerySystemMetricsResponse{}, nil
				}
				return nil, connect.NewError(connect.CodeUnavailable, errors.New("offline"))
			}})
			require.NoError(t, svc.db.RecordSequenceGap(ctx, "agent-1", systemMetricsDataType, 5, 10))
			require.NoError(t, svc.PollOnce(ctx))
			status := "recovering"
			if expired {
				status = "permanent"
			}
			requireGapStatus(t, svc, systemMetricsDataType, status)
			var count int
			require.NoError(t, svc.db.DB().QueryRow("SELECT count(*) FROM system_metrics_summaries").Scan(&count))
			require.Zero(t, count, "partial aggregates must not be stored")
		})
	}
}

func TestQueryGapRecords_SequenceValidation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		seqs        []uint64
		unavailable bool
		wantErr     bool
	}{
		{"empty", nil, true, true},
		{"expired prefix", []uint64{6, 7}, true, true},
		{"hole", []uint64{5, 7}, true, true},
		{"only newer records", []uint64{20, 21}, true, true},
		{"stale cursor", []uint64{4}, false, true},
		{"duplicate", []uint64{5, 5}, false, true},
		{"exclude records after gap", []uint64{5, 6, 7, 8}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			records, err := queryGapRecords(context.Background(), &database.SequenceGap{StartSeqID: 5, EndSeqID: 7}, gapMaxSize,
				func(context.Context, uint64, int32) ([]*agentv1.SystemMetric, error) {
					var page []*agentv1.SystemMetric
					for _, seq := range tc.seqs {
						page = append(page, &agentv1.SystemMetric{SeqId: seq})
					}
					return page, nil
				})
			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, records)
				require.Equal(t, tc.unavailable, errors.Is(err, errGapDataUnavailable))
			} else {
				require.NoError(t, err)
				require.Len(t, records, 3)
			}
		})
	}
}

func TestQueryGapRecords_Canceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := queryGapRecords(ctx, &database.SequenceGap{StartSeqID: 5, EndSeqID: 7}, gapMaxSize,
		func(context.Context, uint64, int32) ([]*agentv1.SystemMetric, error) {
			calls++
			cancel()
			return []*agentv1.SystemMetric{{SeqId: 5}}, nil
		})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls)
}
