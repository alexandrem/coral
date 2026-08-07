package integration

import (
	"context"
	"testing"
	"time"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/constants"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceTopologyOtelLeakFix(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.New(tempDir, "test-topology", constants.DefaultConnectionsCacheTTL, zerolog.Nop())
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	clientTraceID := "trace000000000000000000000000099"
	otelSpanID := "span-otel-sdk00"
	beylaClientSpanID := "span-beyla-clnt"

	// 1. Simulate the BUG behavior:
	// OTel context leaked into the HTTP Request, so Beyla recorded a CLIENT span
	// but the SERVER span picked up the OTel SDK traceparent, which has parent_span_id pointing
	// to the OTel span, not Beyla's CLIENT span.

	bugClientSpan := &agentv1.EbpfTraceSpan{
		TraceId:     clientTraceID,
		SpanId:      beylaClientSpanID, // Beyla generated span
		ServiceName: "otel-app-bug",
		SpanName:    "GET /",
		SpanKind:    "client",
		StartTime:   now.UnixMilli(),
		DurationUs:  1000,
		StatusCode:  200,
	}

	bugServerSpan := &agentv1.EbpfTraceSpan{
		TraceId:      clientTraceID,
		SpanId:       "span-server0001",
		ParentSpanId: otelSpanID, // Missing parent reference to Beyla's CLIENT span due to leak!
		ServiceName:  "cpu-app-bug",
		SpanName:     "GET /",
		SpanKind:     "server",
		StartTime:    now.UnixMilli() + 10,
		DurationUs:   500,
		StatusCode:   200,
	}

	err = db.InsertBeylaTraces(ctx, "agent-1", []*agentv1.EbpfTraceSpan{bugClientSpan, bugServerSpan})
	require.NoError(t, err)

	// 2. Simulate the FIX behavior:
	// We used context.Background() in otel-app, so Beyla sees a clean HTTP request.
	// Beyla generates its own trace and span IDs, and injects them. The SERVER span picks those up.
	// As a result, the SERVER span's parent_span_id correctly points to Beyla's CLIENT span.

	fixTraceID := "trace000000000000000000000000100"
	fixClientSpanID := "span-beyla-c002"

	fixClientSpan := &agentv1.EbpfTraceSpan{
		TraceId:     fixTraceID,
		SpanId:      fixClientSpanID,
		ServiceName: "otel-app-fix",
		SpanName:    "GET /",
		SpanKind:    "client",
		StartTime:   now.UnixMilli(),
		DurationUs:  1000,
		StatusCode:  200,
	}

	fixServerSpan := &agentv1.EbpfTraceSpan{
		TraceId:      fixTraceID,
		SpanId:       "span-server0002",
		ParentSpanId: fixClientSpanID, // Correct parent-child linkage!
		ServiceName:  "cpu-app-fix",
		SpanName:     "GET /",
		SpanKind:     "server",
		StartTime:    now.UnixMilli() + 10,
		DurationUs:   500,
		StatusCode:   200,
	}

	err = db.InsertBeylaTraces(ctx, "agent-1", []*agentv1.EbpfTraceSpan{fixClientSpan, fixServerSpan})
	require.NoError(t, err)

	since := now.Add(-time.Hour)
	err = db.MaterializeConnections(ctx, since)
	require.NoError(t, err)

	conns, err := db.GetServiceConnections(ctx, since)
	require.NoError(t, err)

	// Both pairs should materialize:
	// - The BUG pair (otel-app-bug -> cpu-app-bug): parent_span_id is broken (points to an OTel SDK
	//   span not in beyla_traces), but the trace_id fallback recovers the edge because both spans share
	//   the same trace_id and have CLIENT/SERVER kinds respectively.
	// - The FIX pair (otel-app-fix -> cpu-app-fix): parent_span_id correctly links the spans.
	require.Len(t, conns, 2, "Both connections (bug recovery via trace_id and fix via parent_span_id) should be materialized")

	// Collect connections into a map for order-independent assertions.
	connMap := make(map[string]string, len(conns))
	for _, c := range conns {
		connMap[c.FromService] = c.ToService
	}
	assert.Equal(t, "cpu-app-bug", connMap["otel-app-bug"], "trace_id fallback should recover the bug-case edge")
	assert.Equal(t, "cpu-app-fix", connMap["otel-app-fix"], "parent_span_id join should handle the fix-case edge")
}
