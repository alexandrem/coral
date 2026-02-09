package colony

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/agent/telemetry"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/poller"
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
	colonyDB, err := database.New(t.TempDir(), "test-colony", logger)
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

	colonyDB, err := database.New(t.TempDir(), "test-colony", logger)
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

// TestCheckpointPolling_GapDetection tests that gaps in seq_ids are detected and recorded.
func TestCheckpointPolling_GapDetection(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	colonyDB, err := database.New(t.TempDir(), "test-colony", logger)
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
