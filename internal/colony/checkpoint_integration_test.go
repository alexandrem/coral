package colony

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	_ "github.com/marcboeker/go-duckdb"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	"github.com/coral-mesh/coral/internal/agent/telemetry"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/poller"
	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/constants"
)

// TestCheckpointPolling_EndToEnd tests the full checkpoint-based polling flow:
// 1. Colony reads checkpoint (nil = first poll)
// 2. Agent returns spans with seq_ids
// 3. Colony aggregates and stores data
// 4. Colony updates checkpoint
// 5. Subsequent poll uses updated checkpoint, returning only new data.
func TestCheckpointPolling_EndToEnd(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	// === Setup Agent Storage ===
	agentDB, err := sql.Open("duckdb", ":memory:")
	require.NoError(t, err)
	defer func() { _ = agentDB.Close() }()

	agentStorage, err := telemetry.NewStorage(agentDB, logger)
	require.NoError(t, err)

	// === Setup Colony Database ===
	colonyDB, err := database.New(t.TempDir(), "test-colony", constants.DefaultConnectionsCacheTTL, logger)
	require.NoError(t, err)
	defer func() { _ = colonyDB.Close() }()

	agentID := "agent-test-1"
	dataType := "telemetry"
	sessionID := "session-abc-123"

	// Use a fixed base time within a single minute to avoid bucket boundary issues.
	now := time.Now().Truncate(time.Minute).Add(30 * time.Second)

	// === Step 1: First poll - checkpoint should be nil ===
	cp, err := colonyDB.GetPollingCheckpoint(ctx, agentID, dataType)
	require.NoError(t, err)
	assert.Nil(t, cp, "First poll should have no checkpoint")

	// === Step 2: Insert initial data into agent ===
	for i := 0; i < 5; i++ {
		span := telemetry.Span{
			Timestamp:   now.Add(-time.Duration(5-i) * time.Second),
			TraceID:     "trace-" + string(rune('A'+i)),
			SpanID:      "span-" + string(rune('A'+i)),
			ServiceName: "checkout",
			SpanKind:    "SERVER",
			DurationMs:  float64(100 + i*10),
			IsError:     false,
			Attributes:  map[string]string{},
		}
		require.NoError(t, agentStorage.StoreSpan(ctx, span))
	}

	// === Step 3: Colony queries agent (simulating poll) ===
	spans, maxSeqID, err := agentStorage.QuerySpansBySeqID(ctx, 0, 10000, nil)
	require.NoError(t, err)
	assert.Len(t, spans, 5)
	assert.Greater(t, maxSeqID, uint64(0))

	// Aggregate and store.
	aggregator := NewTelemetryAggregator()
	protoSpans := telemetrySpansToProto(spans)
	aggregator.AddSpans(agentID, protoSpans)
	summaries := aggregator.GetSummaries()
	require.NotEmpty(t, summaries)
	require.NoError(t, colonyDB.InsertTelemetrySummaries(ctx, summaries))

	// === Step 4: Update checkpoint ===
	require.NoError(t, colonyDB.UpdatePollingCheckpoint(ctx, agentID, dataType, sessionID, maxSeqID))

	// Verify checkpoint was stored.
	cp, err = colonyDB.GetPollingCheckpoint(ctx, agentID, dataType)
	require.NoError(t, err)
	require.NotNil(t, cp)
	assert.Equal(t, maxSeqID, cp.LastSeqID)
	assert.Equal(t, sessionID, cp.SessionID)

	// === Step 5: Insert more data into agent ===
	for i := 5; i < 8; i++ {
		span := telemetry.Span{
			Timestamp:   now.Add(time.Duration(i) * time.Second),
			TraceID:     "trace-" + string(rune('A'+i)),
			SpanID:      "span-" + string(rune('A'+i)),
			ServiceName: "checkout",
			SpanKind:    "SERVER",
			DurationMs:  float64(200 + i*10),
			IsError:     false,
			Attributes:  map[string]string{},
		}
		require.NoError(t, agentStorage.StoreSpan(ctx, span))
	}

	// === Step 6: Colony polls again using stored checkpoint ===
	spans2, maxSeqID2, err := agentStorage.QuerySpansBySeqID(ctx, cp.LastSeqID, 10000, nil)
	require.NoError(t, err)
	assert.Len(t, spans2, 3, "Second poll should only return new spans")
	assert.Greater(t, maxSeqID2, maxSeqID, "New maxSeqID should be greater")

	// Update checkpoint.
	require.NoError(t, colonyDB.UpdatePollingCheckpoint(ctx, agentID, dataType, sessionID, maxSeqID2))

	cp2, err := colonyDB.GetPollingCheckpoint(ctx, agentID, dataType)
	require.NoError(t, err)
	require.NotNil(t, cp2)
	assert.Equal(t, maxSeqID2, cp2.LastSeqID)
}

// TestCheckpointPolling_SessionReset tests that a session_id change triggers checkpoint reset.
func TestCheckpointPolling_SessionReset(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	colonyDB, err := database.New(t.TempDir(), "test-colony", constants.DefaultConnectionsCacheTTL, logger)
	require.NoError(t, err)
	defer func() { _ = colonyDB.Close() }()

	agentID := "agent-test-1"
	dataType := "telemetry"

	// Simulate first session with high seq_id.
	require.NoError(t, colonyDB.UpdatePollingCheckpoint(ctx, agentID, dataType, "session-old", 500))

	cp, err := colonyDB.GetPollingCheckpoint(ctx, agentID, dataType)
	require.NoError(t, err)
	require.NotNil(t, cp)
	assert.Equal(t, uint64(500), cp.LastSeqID)
	assert.Equal(t, "session-old", cp.SessionID)

	// Simulate agent returning a different session_id (database recreated).
	newSessionID := "session-new"

	// Colony detects mismatch and resets.
	require.NoError(t, colonyDB.ResetPollingCheckpoint(ctx, agentID, dataType))

	// Verify checkpoint is gone.
	cp2, err := colonyDB.GetPollingCheckpoint(ctx, agentID, dataType)
	require.NoError(t, err)
	assert.Nil(t, cp2, "Checkpoint should be nil after reset")

	// Colony polls from 0 and creates new checkpoint with new session.
	// Create agent storage for new session.
	agentDB, err := sql.Open("duckdb", ":memory:")
	require.NoError(t, err)
	defer func() { _ = agentDB.Close() }()

	agentStorage, err := telemetry.NewStorage(agentDB, logger)
	require.NoError(t, err)

	now := time.Now()
	for i := 0; i < 3; i++ {
		span := telemetry.Span{
			Timestamp:   now.Add(-time.Duration(i) * time.Second),
			TraceID:     "new-trace-" + string(rune('A'+i)),
			SpanID:      "new-span-" + string(rune('A'+i)),
			ServiceName: "service",
			SpanKind:    "SERVER",
			DurationMs:  50.0,
			Attributes:  map[string]string{},
		}
		require.NoError(t, agentStorage.StoreSpan(ctx, span))
	}

	// Query from 0 (full re-fetch).
	spans, maxSeqID, err := agentStorage.QuerySpansBySeqID(ctx, 0, 10000, nil)
	require.NoError(t, err)
	assert.Len(t, spans, 3)

	// Update checkpoint with new session.
	require.NoError(t, colonyDB.UpdatePollingCheckpoint(ctx, agentID, dataType, newSessionID, maxSeqID))

	cp3, err := colonyDB.GetPollingCheckpoint(ctx, agentID, dataType)
	require.NoError(t, err)
	require.NotNil(t, cp3)
	assert.Equal(t, newSessionID, cp3.SessionID)
	assert.Equal(t, maxSeqID, cp3.LastSeqID)
}

// stubSystemMetricsAgent serves QuerySystemMetrics from a fixed record set
// and records the start seq_id of each query. Other methods are not
// implemented.
type stubSystemMetricsAgent struct {
	agentv1connect.AgentServiceClient
	sessionID string
	metrics   []*agentv1.SystemMetric
	starts    []uint64
}

func (s *stubSystemMetricsAgent) QuerySystemMetrics(
	_ context.Context,
	req *connect.Request[agentv1.QuerySystemMetricsRequest],
) (*connect.Response[agentv1.QuerySystemMetricsResponse], error) {
	s.starts = append(s.starts, req.Msg.StartSeqId)
	resp := &agentv1.QuerySystemMetricsResponse{SessionId: s.sessionID}
	for _, m := range s.metrics {
		if m.SeqId > req.Msg.StartSeqId {
			resp.Metrics = append(resp.Metrics, m)
			resp.MaxSeqId = m.SeqId
		}
	}
	return connect.NewResponse(resp), nil
}

// stubCPUProfileAgent serves QueryCPUProfileSamples from a fixed sample set
// and records the start seq_id of each query.
type stubCPUProfileAgent struct {
	agentv1connect.AgentDebugServiceClient
	sessionID string
	samples   []*agentv1.CPUProfileSample
	starts    []uint64
}

func (s *stubCPUProfileAgent) QueryCPUProfileSamples(
	_ context.Context,
	req *connect.Request[agentv1.QueryCPUProfileSamplesRequest],
) (*connect.Response[agentv1.QueryCPUProfileSamplesResponse], error) {
	s.starts = append(s.starts, req.Msg.StartSeqId)
	resp := &agentv1.QueryCPUProfileSamplesResponse{SessionId: s.sessionID}
	for _, sample := range s.samples {
		if sample.SeqId > req.Msg.StartSeqId {
			resp.Samples = append(resp.Samples, sample)
			resp.MaxSeqId = sample.SeqId
		}
	}
	return connect.NewResponse(resp), nil
}

// systemMetricsAt returns one metric per seq_id, all at ts.
func systemMetricsAt(ts time.Time, seqIDs ...uint64) []*agentv1.SystemMetric {
	metrics := make([]*agentv1.SystemMetric, 0, len(seqIDs))
	for _, id := range seqIDs {
		metrics = append(metrics, &agentv1.SystemMetric{
			Name: "cpu_usage", Value: 1, MetricType: "gauge",
			Timestamp: ts.UnixMilli(), Attributes: "{}", SeqId: id,
		})
	}
	return metrics
}

// TestSystemMetricsPoller_SessionResetDetectsGaps verifies that after an agent
// session change, gaps are detected relative to the re-query (seq_id 0), not
// the stale checkpoint. With the stale checkpoint (500) every new seq_id is
// below it, so the gap was silently missed.
func TestSystemMetricsPoller_SessionResetDetectsGaps(t *testing.T) {
	ctx := context.Background()
	db := setupGapRecoveryTestDB(t)
	agent := &registry.Entry{AgentID: "agent-1"}
	require.NoError(t, db.UpdatePollingCheckpoint(ctx, agent.AgentID, systemMetricsDataType, "session-old", 500))

	old := time.Now().Add(-5 * time.Minute).Truncate(time.Minute)
	stub := &stubSystemMetricsAgent{
		sessionID: "session-new",
		metrics:   systemMetricsAt(old, 1, 2, 5, 6), // 3–4 missing.
	}
	p := NewSystemMetricsPoller(ctx, registry.New(nil), db, time.Minute, 0, zerolog.Nop())
	p.clientFactory = func(*registry.Entry) agentv1connect.AgentServiceClient { return stub }

	cutoff := time.Now().Truncate(time.Minute)
	metrics, sessionID, maxSeqID, err := p.pollAgent(ctx, agent, cutoff)
	require.NoError(t, err)

	assert.Equal(t, []uint64{500, 0}, stub.starts, "should re-query from 0 after session change")
	assert.Len(t, metrics, 4)
	assert.Equal(t, "session-new", sessionID)
	assert.Equal(t, uint64(6), maxSeqID)

	gaps, err := db.GetPendingSequenceGaps(ctx, 10)
	require.NoError(t, err)
	require.Len(t, gaps, 1)
	assert.Equal(t, uint64(3), gaps[0].StartSeqID)
	assert.Equal(t, uint64(4), gaps[0].EndSeqID)
}

// TestSystemMetricsPoller_HoldsBackIncompleteMinute verifies that pollAgent
// returns only complete minutes, sets max_seq_id to the last returned record,
// and does not report gaps in the held-back records (they are fetched again).
func TestSystemMetricsPoller_HoldsBackIncompleteMinute(t *testing.T) {
	ctx := context.Background()
	db := setupGapRecoveryTestDB(t)
	agent := &registry.Entry{AgentID: "agent-1"}

	cutoff := time.Now().Add(-time.Hour).Truncate(time.Minute)
	var all []*agentv1.SystemMetric
	all = append(all, systemMetricsAt(cutoff.Add(-2*time.Minute), 1, 2)...)
	all = append(all, systemMetricsAt(cutoff.Add(-time.Minute), 3, 4)...)
	all = append(all, systemMetricsAt(cutoff, 5, 8)...) // Incomplete minute, with a gap (6–7).

	stub := &stubSystemMetricsAgent{sessionID: "session-1", metrics: all}
	p := NewSystemMetricsPoller(ctx, registry.New(nil), db, time.Minute, 0, zerolog.Nop())
	p.clientFactory = func(*registry.Entry) agentv1connect.AgentServiceClient { return stub }

	metrics, _, maxSeqID, err := p.pollAgent(ctx, agent, cutoff)
	require.NoError(t, err)
	assert.Len(t, metrics, 4, "only the two complete minutes")
	assert.Equal(t, uint64(4), maxSeqID, "checkpoint must not pass held-back records")

	gaps, err := db.GetPendingSequenceGaps(ctx, 10)
	require.NoError(t, err)
	assert.Empty(t, gaps, "gaps in held-back records must not be reported yet")

	// Only the incomplete minute: nothing is returned and the checkpoint stays.
	stub.metrics = all[4:]
	metrics, _, maxSeqID, err = p.pollAgent(ctx, agent, cutoff)
	require.NoError(t, err)
	assert.Empty(t, metrics)
	assert.Zero(t, maxSeqID)
}

// TestCPUProfilePoller_SessionResetDetectsGaps is the CPU profile counterpart
// of TestSystemMetricsPoller_SessionResetDetectsGaps.
func TestCPUProfilePoller_SessionResetDetectsGaps(t *testing.T) {
	ctx := context.Background()
	db := setupGapRecoveryTestDB(t)
	agent := &registry.Entry{AgentID: "agent-1"}
	require.NoError(t, db.UpdatePollingCheckpoint(ctx, agent.AgentID, cpuProfileDataType, "session-old", 500))

	old := timestamppb.New(time.Now().Add(-5 * time.Minute))
	stub := &stubCPUProfileAgent{sessionID: "session-new"}
	for _, id := range []uint64{1, 2, 5} { // 3–4 missing.
		stub.samples = append(stub.samples, &agentv1.CPUProfileSample{Timestamp: old, SeqId: id})
	}
	p := NewCPUProfilePoller(ctx, registry.New(nil), db, time.Minute, 0, zerolog.Nop())
	p.clientFactory = func(connect.HTTPClient, string, ...connect.ClientOption) agentv1connect.AgentDebugServiceClient {
		return stub
	}

	samples, _, _, err := p.pollAgent(ctx, agent)
	require.NoError(t, err)
	assert.Equal(t, []uint64{500, 0}, stub.starts, "should re-query from 0 after session change")
	assert.Len(t, samples, 3)

	gaps, err := db.GetPendingSequenceGaps(ctx, 10)
	require.NoError(t, err)
	require.Len(t, gaps, 1)
	assert.Equal(t, uint64(3), gaps[0].StartSeqID)
	assert.Equal(t, uint64(4), gaps[0].EndSeqID)
}

// TestCheckpointPolling_GapDetection tests that gaps in seq_ids are detected and recorded.
func TestCheckpointPolling_GapDetection(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	colonyDB, err := database.New(t.TempDir(), "test-colony", constants.DefaultConnectionsCacheTTL, logger)
	require.NoError(t, err)
	defer func() { _ = colonyDB.Close() }()

	agentID := "agent-gap-test"
	dataType := "telemetry"

	// Simulate agent returning non-consecutive seq_ids after checkpoint 0.
	// The gap detection uses the poller.DetectGaps function.
	// Simulate: checkpoint=0, received seq_ids=[1, 2, 3, 5, 6] — gap at 4.
	seqIDs := []uint64{1, 2, 3, 5, 6}
	oldTimestamp := time.Now().Add(-30 * time.Second).UnixMilli() // Past grace period.
	timestamps := []int64{oldTimestamp, oldTimestamp, oldTimestamp, oldTimestamp, oldTimestamp}

	gaps := poller.DetectGaps(0, seqIDs, timestamps)
	require.Len(t, gaps, 1, "Should detect 1 gap")
	assert.Equal(t, uint64(4), gaps[0].StartSeqID)
	assert.Equal(t, uint64(4), gaps[0].EndSeqID)

	// Record the gap in colony database.
	require.NoError(t, colonyDB.RecordSequenceGap(ctx, agentID, dataType, gaps[0].StartSeqID, gaps[0].EndSeqID))

	// Verify gap was recorded.
	pendingGaps, err := colonyDB.GetPendingSequenceGaps(ctx, 3)
	require.NoError(t, err)
	require.Len(t, pendingGaps, 1)
	assert.Equal(t, agentID, pendingGaps[0].AgentID)
	assert.Equal(t, dataType, pendingGaps[0].DataType)
	assert.Equal(t, uint64(4), pendingGaps[0].StartSeqID)
	assert.Equal(t, uint64(4), pendingGaps[0].EndSeqID)
	assert.Equal(t, "detected", pendingGaps[0].Status)

	// Simulate gap recovery — mark as recovered.
	require.NoError(t, colonyDB.MarkGapRecovered(ctx, pendingGaps[0].ID))

	// No more pending gaps.
	remainingGaps, err := colonyDB.GetPendingSequenceGaps(ctx, 3)
	require.NoError(t, err)
	assert.Len(t, remainingGaps, 0)
}

// === Chaos Tests ===
// These tests verify that sequence-based polling is resilient to
// clock skew, network partitions, and storage failures.

// TestChaos_ClockSkew verifies that seq-based polling is unaffected by clock differences
// between agent and colony. The agent clock is simulated 1 hour ahead and 1 hour behind;
// because polling uses monotonic seq_ids (not timestamps), all data is captured correctly.
func TestChaos_ClockSkew(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	// === Setup Agent Storage ===
	agentDB, err := sql.Open("duckdb", ":memory:")
	require.NoError(t, err)
	defer func() { _ = agentDB.Close() }()

	agentStorage, err := telemetry.NewStorage(agentDB, logger)
	require.NoError(t, err)

	// === Setup Colony Database ===
	colonyDB, err := database.New(t.TempDir(), "test-colony", constants.DefaultConnectionsCacheTTL, logger)
	require.NoError(t, err)
	defer func() { _ = colonyDB.Close() }()

	agentID := "agent-clock-skew"
	dataType := "telemetry"
	sessionID := "session-clock-1"

	t.Run("agent clock 1 hour ahead", func(t *testing.T) {
		// Agent timestamps are 1 hour in the future (simulating clock skew).
		futureTime := time.Now().Add(1 * time.Hour)

		for i := 0; i < 5; i++ {
			span := telemetry.Span{
				Timestamp:   futureTime.Add(time.Duration(i) * time.Second),
				TraceID:     "future-trace-" + string(rune('A'+i)),
				SpanID:      "future-span-" + string(rune('A'+i)),
				ServiceName: "checkout",
				SpanKind:    "SERVER",
				DurationMs:  float64(100 + i*10),
				Attributes:  map[string]string{},
			}
			require.NoError(t, agentStorage.StoreSpan(ctx, span))
		}

		// Colony polls from seq_id 0 — timestamps don't matter.
		spans, maxSeqID, err := agentStorage.QuerySpansBySeqID(ctx, 0, 10000, nil)
		require.NoError(t, err)
		assert.Len(t, spans, 5, "All spans should be returned regardless of future timestamps")
		assert.Greater(t, maxSeqID, uint64(0))

		// Update checkpoint.
		require.NoError(t, colonyDB.UpdatePollingCheckpoint(ctx, agentID, dataType, sessionID, maxSeqID))

		cp, err := colonyDB.GetPollingCheckpoint(ctx, agentID, dataType)
		require.NoError(t, err)
		require.NotNil(t, cp)
		assert.Equal(t, maxSeqID, cp.LastSeqID, "Checkpoint should use seq_id, not timestamps")
	})

	t.Run("agent clock 1 hour behind", func(t *testing.T) {
		// Get current checkpoint.
		cp, err := colonyDB.GetPollingCheckpoint(ctx, agentID, dataType)
		require.NoError(t, err)
		require.NotNil(t, cp)
		oldCheckpoint := cp.LastSeqID

		// Agent timestamps are 1 hour in the past.
		pastTime := time.Now().Add(-1 * time.Hour)

		for i := 0; i < 3; i++ {
			span := telemetry.Span{
				Timestamp:   pastTime.Add(time.Duration(i) * time.Second),
				TraceID:     "past-trace-" + string(rune('A'+i)),
				SpanID:      "past-span-" + string(rune('A'+i)),
				ServiceName: "checkout",
				SpanKind:    "SERVER",
				DurationMs:  float64(200 + i*10),
				Attributes:  map[string]string{},
			}
			require.NoError(t, agentStorage.StoreSpan(ctx, span))
		}

		// Colony polls from the old checkpoint — only new spans returned.
		spans, maxSeqID, err := agentStorage.QuerySpansBySeqID(ctx, oldCheckpoint, 10000, nil)
		require.NoError(t, err)
		assert.Len(t, spans, 3, "New spans should be returned even with past timestamps")
		assert.Greater(t, maxSeqID, oldCheckpoint, "New seq_ids should be higher regardless of timestamp")

		// Update checkpoint.
		require.NoError(t, colonyDB.UpdatePollingCheckpoint(ctx, agentID, dataType, sessionID, maxSeqID))

		cp2, err := colonyDB.GetPollingCheckpoint(ctx, agentID, dataType)
		require.NoError(t, err)
		require.NotNil(t, cp2)
		assert.Equal(t, maxSeqID, cp2.LastSeqID)
	})

	t.Run("mixed clock skew does not cause duplicates", func(t *testing.T) {
		// Get current checkpoint.
		cp, err := colonyDB.GetPollingCheckpoint(ctx, agentID, dataType)
		require.NoError(t, err)
		require.NotNil(t, cp)

		// Polling again with the same checkpoint should return 0 new spans.
		spans, _, err := agentStorage.QuerySpansBySeqID(ctx, cp.LastSeqID, 10000, nil)
		require.NoError(t, err)
		assert.Empty(t, spans, "Re-polling with same checkpoint should yield no duplicates")
	})
}

// TestChaos_NetworkPartition verifies that when a network failure occurs during polling,
// the checkpoint is NOT updated and the next poll retries from the old checkpoint.
func TestChaos_NetworkPartition(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	// === Setup Agent Storage ===
	agentDB, err := sql.Open("duckdb", ":memory:")
	require.NoError(t, err)
	defer func() { _ = agentDB.Close() }()

	agentStorage, err := telemetry.NewStorage(agentDB, logger)
	require.NoError(t, err)

	// === Setup Colony Database ===
	colonyDB, err := database.New(t.TempDir(), "test-colony", constants.DefaultConnectionsCacheTTL, logger)
	require.NoError(t, err)
	defer func() { _ = colonyDB.Close() }()

	agentID := "agent-partition"
	dataType := "telemetry"
	sessionID := "session-partition-1"
	now := time.Now().Truncate(time.Minute).Add(30 * time.Second)

	// === Step 1: Successful first poll to establish baseline checkpoint ===
	for i := 0; i < 5; i++ {
		span := telemetry.Span{
			Timestamp:   now.Add(time.Duration(i) * time.Second),
			TraceID:     "trace-init-" + string(rune('A'+i)),
			SpanID:      "span-init-" + string(rune('A'+i)),
			ServiceName: "payment",
			SpanKind:    "SERVER",
			DurationMs:  float64(50 + i*10),
			Attributes:  map[string]string{},
		}
		require.NoError(t, agentStorage.StoreSpan(ctx, span))
	}

	spans, maxSeqID, err := agentStorage.QuerySpansBySeqID(ctx, 0, 10000, nil)
	require.NoError(t, err)
	assert.Len(t, spans, 5)

	// Simulate successful storage and checkpoint update.
	aggregator := NewTelemetryAggregator()
	protoSpans := telemetrySpansToProto(spans)
	aggregator.AddSpans(agentID, protoSpans)
	summaries := aggregator.GetSummaries()
	require.NotEmpty(t, summaries)
	require.NoError(t, colonyDB.InsertTelemetrySummaries(ctx, summaries))
	require.NoError(t, colonyDB.UpdatePollingCheckpoint(ctx, agentID, dataType, sessionID, maxSeqID))

	baselineCheckpoint := maxSeqID

	// === Step 2: Add more data to agent ===
	for i := 5; i < 10; i++ {
		span := telemetry.Span{
			Timestamp:   now.Add(time.Duration(i) * time.Second),
			TraceID:     "trace-new-" + string(rune('A'+i)),
			SpanID:      "span-new-" + string(rune('A'+i)),
			ServiceName: "payment",
			SpanKind:    "SERVER",
			DurationMs:  float64(100 + i*10),
			Attributes:  map[string]string{},
		}
		require.NoError(t, agentStorage.StoreSpan(ctx, span))
	}

	// === Step 3: Simulate network failure during poll ===
	// Colony queries agent successfully but "network fails" before checkpoint update.
	// In a real scenario, the gRPC call would fail. Here we simulate by querying but
	// NOT updating the checkpoint (simulating the failure path).
	spansDuringPartition, _, err := agentStorage.QuerySpansBySeqID(ctx, baselineCheckpoint, 10000, nil)
	require.NoError(t, err)
	assert.Len(t, spansDuringPartition, 5, "Agent has new data")

	// Checkpoint is NOT updated (simulating network failure after query).
	// Verify checkpoint remains at baseline.
	cp, err := colonyDB.GetPollingCheckpoint(ctx, agentID, dataType)
	require.NoError(t, err)
	require.NotNil(t, cp)
	assert.Equal(t, baselineCheckpoint, cp.LastSeqID, "Checkpoint must NOT advance during network failure")

	// === Step 4: Network recovers — retry poll from old checkpoint ===
	spansRetry, maxSeqIDRetry, err := agentStorage.QuerySpansBySeqID(ctx, cp.LastSeqID, 10000, nil)
	require.NoError(t, err)
	assert.Len(t, spansRetry, 5, "Retry should return the SAME data (idempotent)")

	// Now the poll succeeds — update checkpoint.
	require.NoError(t, colonyDB.UpdatePollingCheckpoint(ctx, agentID, dataType, sessionID, maxSeqIDRetry))

	cpAfterRecovery, err := colonyDB.GetPollingCheckpoint(ctx, agentID, dataType)
	require.NoError(t, err)
	require.NotNil(t, cpAfterRecovery)
	assert.Equal(t, maxSeqIDRetry, cpAfterRecovery.LastSeqID, "Checkpoint should advance after successful retry")
	assert.Greater(t, cpAfterRecovery.LastSeqID, baselineCheckpoint, "Checkpoint should be past baseline")

	// === Step 5: Verify no data loss — a third poll returns no new data ===
	spansFinal, _, err := agentStorage.QuerySpansBySeqID(ctx, cpAfterRecovery.LastSeqID, 10000, nil)
	require.NoError(t, err)
	assert.Empty(t, spansFinal, "No data should be missing or duplicated after recovery")
}

// TestChaos_PollingStorageFailure verifies that when colony storage fails after querying
// the agent, the checkpoint is NOT updated and the next poll re-queries the same data.
func TestChaos_PollingStorageFailure(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	// === Setup Agent Storage ===
	agentDB, err := sql.Open("duckdb", ":memory:")
	require.NoError(t, err)
	defer func() { _ = agentDB.Close() }()

	agentStorage, err := telemetry.NewStorage(agentDB, logger)
	require.NoError(t, err)

	// === Setup Colony Database ===
	colonyDB, err := database.New(t.TempDir(), "test-colony", constants.DefaultConnectionsCacheTTL, logger)
	require.NoError(t, err)
	defer func() { _ = colonyDB.Close() }()

	agentID := "agent-storage-fail"
	dataType := "telemetry"
	sessionID := "session-storage-1"
	now := time.Now().Truncate(time.Minute).Add(30 * time.Second)

	// === Step 1: Establish baseline with a successful poll ===
	for i := 0; i < 3; i++ {
		span := telemetry.Span{
			Timestamp:   now.Add(time.Duration(i) * time.Second),
			TraceID:     "trace-base-" + string(rune('A'+i)),
			SpanID:      "span-base-" + string(rune('A'+i)),
			ServiceName: "orders",
			SpanKind:    "SERVER",
			DurationMs:  float64(75 + i*10),
			Attributes:  map[string]string{},
		}
		require.NoError(t, agentStorage.StoreSpan(ctx, span))
	}

	spans, maxSeqID, err := agentStorage.QuerySpansBySeqID(ctx, 0, 10000, nil)
	require.NoError(t, err)
	assert.Len(t, spans, 3)

	aggregator := NewTelemetryAggregator()
	aggregator.AddSpans(agentID, telemetrySpansToProto(spans))
	summaries := aggregator.GetSummaries()
	require.NoError(t, colonyDB.InsertTelemetrySummaries(ctx, summaries))
	require.NoError(t, colonyDB.UpdatePollingCheckpoint(ctx, agentID, dataType, sessionID, maxSeqID))

	baselineCheckpoint := maxSeqID

	// === Step 2: Add more data to agent ===
	for i := 3; i < 8; i++ {
		span := telemetry.Span{
			Timestamp:   now.Add(time.Duration(i) * time.Second),
			TraceID:     "trace-fail-" + string(rune('A'+i)),
			SpanID:      "span-fail-" + string(rune('A'+i)),
			ServiceName: "orders",
			SpanKind:    "SERVER",
			DurationMs:  float64(150 + i*10),
			Attributes:  map[string]string{},
		}
		require.NoError(t, agentStorage.StoreSpan(ctx, span))
	}

	// === Step 3: Simulate query success but storage failure ===
	// Colony queries the agent — this succeeds.
	spansQueried, maxSeqIDQueried, err := agentStorage.QuerySpansBySeqID(ctx, baselineCheckpoint, 10000, nil)
	require.NoError(t, err)
	assert.Len(t, spansQueried, 5)
	assert.Greater(t, maxSeqIDQueried, baselineCheckpoint)

	// Colony tries to store data — simulate failure by NOT calling InsertTelemetrySummaries.
	// Because storage failed, checkpoint is NOT updated.

	// Verify checkpoint is still at baseline.
	cp, err := colonyDB.GetPollingCheckpoint(ctx, agentID, dataType)
	require.NoError(t, err)
	require.NotNil(t, cp)
	assert.Equal(t, baselineCheckpoint, cp.LastSeqID, "Checkpoint must NOT advance when storage fails")

	// === Step 4: Retry — colony re-queries the same data ===
	spansRetry, maxSeqIDRetry, err := agentStorage.QuerySpansBySeqID(ctx, cp.LastSeqID, 10000, nil)
	require.NoError(t, err)
	assert.Len(t, spansRetry, 5, "Retry must return the same 5 spans")
	assert.Equal(t, maxSeqIDQueried, maxSeqIDRetry, "Max seq_id should be identical on retry")

	// Verify the actual data matches (idempotent re-query).
	for i := range spansQueried {
		assert.Equal(t, spansQueried[i].SeqID, spansRetry[i].SeqID, "Span seq_ids must match on retry")
		assert.Equal(t, spansQueried[i].TraceID, spansRetry[i].TraceID, "Span trace_ids must match on retry")
	}

	// === Step 5: This time storage succeeds ===
	aggregator2 := NewTelemetryAggregator()
	aggregator2.AddSpans(agentID, telemetrySpansToProto(spansRetry))
	summaries2 := aggregator2.GetSummaries()
	require.NoError(t, colonyDB.InsertTelemetrySummaries(ctx, summaries2))
	require.NoError(t, colonyDB.UpdatePollingCheckpoint(ctx, agentID, dataType, sessionID, maxSeqIDRetry))

	// Verify checkpoint advanced.
	cpFinal, err := colonyDB.GetPollingCheckpoint(ctx, agentID, dataType)
	require.NoError(t, err)
	require.NotNil(t, cpFinal)
	assert.Equal(t, maxSeqIDRetry, cpFinal.LastSeqID, "Checkpoint should advance after successful retry")

	// === Step 6: No data loss — next poll returns empty ===
	spansFinal, _, err := agentStorage.QuerySpansBySeqID(ctx, cpFinal.LastSeqID, 10000, nil)
	require.NoError(t, err)
	assert.Empty(t, spansFinal, "All data should have been captured — no loss")
}

// === Load Tests ===
// These tests validate that sequence-based polling handles production-scale volumes.
// Skipped in short mode since they are resource-intensive.

// bulkInsertSpans inserts count spans into an agent DuckDB using generate_series for maximum
// throughput. A single INSERT...SELECT generates all rows server-side, avoiding row-by-row overhead.
func bulkInsertSpans(t testing.TB, db *sql.DB, count int, serviceName string, agentIdx int) {
	t.Helper()

	query := fmt.Sprintf(`
		INSERT INTO otel_spans_local (timestamp, trace_id, span_id, service_name, span_kind, duration_ms, is_error, attributes)
		SELECT
			CAST('2025-01-01 00:00:00' AS TIMESTAMP) + INTERVAL (i) MILLISECOND,
			'trace-a%d-s' || CAST(i AS TEXT),
			'span-a%d-s' || CAST(i AS TEXT),
			'%s',
			'SERVER',
			50.0 + (i %% 200),
			false,
			'{}'
		FROM generate_series(0, %d) AS t(i)
	`, agentIdx, agentIdx, serviceName, count-1)

	_, err := db.Exec(query)
	require.NoError(t, err)
}

// TestLoad_HighVolumePolling tests polling 100 agents with 60K spans each.
// Verifies that each agent can be queried within the time budget and all data is captured.
func TestLoad_HighVolumePolling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test in short mode")
	}

	ctx := context.Background()
	logger := zerolog.Nop()

	const (
		numAgents     = 100
		spansPerAgent = 60_000
		batchSize     = 10_000 // QuerySpansBySeqID default max.
		maxQueryTime  = 5 * time.Second
	)

	// === Setup Colony Database ===
	colonyDB, err := database.New(t.TempDir(), "test-colony-load", constants.DefaultConnectionsCacheTTL, logger)
	require.NoError(t, err)
	defer func() { _ = colonyDB.Close() }()

	// === Setup 100 Agent Storages in parallel ===
	type agentFixture struct {
		db      *sql.DB
		storage *telemetry.Storage
		agentID string
	}

	agents := make([]agentFixture, numAgents)

	t.Log("Setting up agents and populating data...")
	setupStart := time.Now()

	// Create and populate agents concurrently (10 workers).
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)

	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		sem <- struct{}{}

		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()

			adb, err := sql.Open("duckdb", ":memory:")
			require.NoError(t, err)

			aStorage, err := telemetry.NewStorage(adb, logger)
			require.NoError(t, err)

			bulkInsertSpans(t, adb, spansPerAgent, fmt.Sprintf("service-%d", idx), idx)

			agents[idx] = agentFixture{
				db:      adb,
				storage: aStorage,
				agentID: fmt.Sprintf("agent-load-%d", idx),
			}
		}(i)
	}
	wg.Wait()

	t.Logf("Setup complete: %d agents × %d spans in %v", numAgents, spansPerAgent, time.Since(setupStart))

	defer func() {
		for _, a := range agents {
			_ = a.db.Close()
		}
	}()

	// === Poll each agent in batches and verify ===
	pollStart := time.Now()
	sessionID := "session-load-1"

	for _, agent := range agents {
		agentQueryStart := time.Now()
		var totalSpans int
		var checkpoint uint64

		// Batched polling loop — mirrors real colony behavior.
		for {
			spans, maxSeqID, err := agent.storage.QuerySpansBySeqID(ctx, checkpoint, int32(batchSize), nil)
			require.NoError(t, err)

			totalSpans += len(spans)

			if maxSeqID > 0 {
				require.NoError(t, colonyDB.UpdatePollingCheckpoint(ctx, agent.agentID, "telemetry", sessionID, maxSeqID))
				checkpoint = maxSeqID
			}

			// No more data — polling complete for this agent.
			if len(spans) < batchSize {
				break
			}
		}

		agentQueryTime := time.Since(agentQueryStart)

		assert.Equal(t, spansPerAgent, totalSpans, "Agent %s: expected %d spans, got %d", agent.agentID, spansPerAgent, totalSpans)
		assert.Less(t, agentQueryTime, maxQueryTime, "Agent %s: query took %v, exceeds %v budget", agent.agentID, agentQueryTime, maxQueryTime)

		// Verify checkpoint is fully advanced.
		cp, err := colonyDB.GetPollingCheckpoint(ctx, agent.agentID, "telemetry")
		require.NoError(t, err)
		require.NotNil(t, cp)
		assert.Equal(t, checkpoint, cp.LastSeqID)
	}

	totalPollTime := time.Since(pollStart)
	totalSpansPolled := numAgents * spansPerAgent
	t.Logf("Polling complete: %d total spans from %d agents in %v (%.0f spans/sec)",
		totalSpansPolled, numAgents, totalPollTime, float64(totalSpansPolled)/totalPollTime.Seconds())

	// Verify no data loss — re-polling returns empty for all agents.
	for _, agent := range agents {
		cp, err := colonyDB.GetPollingCheckpoint(ctx, agent.agentID, "telemetry")
		require.NoError(t, err)
		require.NotNil(t, cp)

		spans, _, err := agent.storage.QuerySpansBySeqID(ctx, cp.LastSeqID, int32(batchSize), nil)
		require.NoError(t, err)
		assert.Empty(t, spans, "Agent %s: re-poll should return no data", agent.agentID)
	}
}

// TestLoad_CatchUpScenario simulates a colony that was offline for 30 minutes.
// Each agent has accumulated ~30K spans. Colony restarts and must catch up via batched polls.
func TestLoad_CatchUpScenario(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test in short mode")
	}

	ctx := context.Background()
	logger := zerolog.Nop()

	const (
		numAgents      = 5
		spansPerAgent  = 30_000 // ~1000 spans/sec × 30 sec/poll × ~30 missed polls.
		batchSize      = 10_000
		maxCatchUpTime = 5 * time.Minute
	)

	// === Setup Colony Database (fresh — simulates restart) ===
	colonyDB, err := database.New(t.TempDir(), "test-colony-catchup", constants.DefaultConnectionsCacheTTL, logger)
	require.NoError(t, err)
	defer func() { _ = colonyDB.Close() }()

	// === Setup agents with accumulated data ===
	type agentFixture struct {
		db      *sql.DB
		storage *telemetry.Storage
		agentID string
	}

	agents := make([]agentFixture, numAgents)

	for i := 0; i < numAgents; i++ {
		adb, err := sql.Open("duckdb", ":memory:")
		require.NoError(t, err)

		aStorage, err := telemetry.NewStorage(adb, logger)
		require.NoError(t, err)

		bulkInsertSpans(t, adb, spansPerAgent, fmt.Sprintf("catchup-svc-%d", i), i)

		agents[i] = agentFixture{
			db:      adb,
			storage: aStorage,
			agentID: fmt.Sprintf("agent-catchup-%d", i),
		}
	}

	defer func() {
		for _, a := range agents {
			_ = a.db.Close()
		}
	}()

	t.Logf("Catch-up scenario: %d agents, %d spans each, batch size %d", numAgents, spansPerAgent, batchSize)

	// === Colony starts fresh — no checkpoints exist ===
	for _, agent := range agents {
		cp, err := colonyDB.GetPollingCheckpoint(ctx, agent.agentID, "telemetry")
		require.NoError(t, err)
		assert.Nil(t, cp, "Fresh colony should have no checkpoints")
	}

	// === Catch-up: poll all agents until fully consumed ===
	catchUpStart := time.Now()
	sessionID := "session-catchup-1"

	type catchUpResult struct {
		agentID    string
		totalSpans int
		batchCount int
		duration   time.Duration
	}

	results := make([]catchUpResult, numAgents)

	for i, agent := range agents {
		agentStart := time.Now()
		var totalSpans int
		var batchCount int
		var checkpoint uint64

		for {
			spans, maxSeqID, err := agent.storage.QuerySpansBySeqID(ctx, checkpoint, int32(batchSize), nil)
			require.NoError(t, err)
			batchCount++

			totalSpans += len(spans)

			if maxSeqID > 0 {
				// Simulate full poll cycle: aggregate + store + checkpoint.
				aggregator := NewTelemetryAggregator()
				aggregator.AddSpans(agent.agentID, telemetrySpansToProto(spans))
				summaries := aggregator.GetSummaries()
				if len(summaries) > 0 {
					require.NoError(t, colonyDB.InsertTelemetrySummaries(ctx, summaries))
				}
				require.NoError(t, colonyDB.UpdatePollingCheckpoint(ctx, agent.agentID, "telemetry", sessionID, maxSeqID))
				checkpoint = maxSeqID
			}

			if len(spans) < batchSize {
				break
			}
		}

		results[i] = catchUpResult{
			agentID:    agent.agentID,
			totalSpans: totalSpans,
			batchCount: batchCount,
			duration:   time.Since(agentStart),
		}
	}

	totalCatchUpTime := time.Since(catchUpStart)

	// === Verify results ===
	for _, r := range results {
		assert.Equal(t, spansPerAgent, r.totalSpans,
			"Agent %s: expected %d spans, got %d", r.agentID, spansPerAgent, r.totalSpans)

		// When spansPerAgent is an exact multiple of batchSize, the loop does one extra
		// empty poll to confirm no more data remains (the last full batch returns exactly
		// batchSize spans, so the loop continues).
		minBatches := (spansPerAgent + batchSize - 1) / batchSize
		maxBatches := minBatches + 1
		assert.GreaterOrEqual(t, r.batchCount, minBatches,
			"Agent %s: expected at least %d batches, got %d", r.agentID, minBatches, r.batchCount)
		assert.LessOrEqual(t, r.batchCount, maxBatches,
			"Agent %s: expected at most %d batches, got %d", r.agentID, maxBatches, r.batchCount)

		t.Logf("  %s: %d spans in %d batches, took %v", r.agentID, r.totalSpans, r.batchCount, r.duration)
	}

	assert.Less(t, totalCatchUpTime, maxCatchUpTime,
		"Total catch-up took %v, exceeds %v budget", totalCatchUpTime, maxCatchUpTime)

	t.Logf("Total catch-up: %d agents in %v", numAgents, totalCatchUpTime)

	// === Verify full catch-up: re-polling returns empty ===
	for _, agent := range agents {
		cp, err := colonyDB.GetPollingCheckpoint(ctx, agent.agentID, "telemetry")
		require.NoError(t, err)
		require.NotNil(t, cp)

		spans, _, err := agent.storage.QuerySpansBySeqID(ctx, cp.LastSeqID, int32(batchSize), nil)
		require.NoError(t, err)
		assert.Empty(t, spans, "Agent %s: all data should be consumed after catch-up", agent.agentID)
	}
}

// telemetrySpansToProto converts agent-side spans to proto format for the aggregator.
func telemetrySpansToProto(spans []telemetry.Span) []*agentv1.TelemetrySpan {
	result := make([]*agentv1.TelemetrySpan, len(spans))
	for i, s := range spans {
		result[i] = &agentv1.TelemetrySpan{
			Timestamp:   s.Timestamp.UnixMilli(),
			TraceId:     s.TraceID,
			SpanId:      s.SpanID,
			ServiceName: s.ServiceName,
			SpanKind:    s.SpanKind,
			DurationMs:  s.DurationMs,
			IsError:     s.IsError,
			HttpStatus:  int32(s.HTTPStatus),
			HttpMethod:  s.HTTPMethod,
			HttpRoute:   s.HTTPRoute,
		}
	}
	return result
}
