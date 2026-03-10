package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
)

func TestFilterEvents(t *testing.T) {
	now := time.Now()
	events := []*meshv1.EbpfEvent{
		{Timestamp: timestamppb.New(now.Add(-10 * time.Minute))}, // Oldest
		{Timestamp: timestamppb.New(now.Add(-5 * time.Minute))},
		{Timestamp: timestamppb.New(now.Add(-1 * time.Minute))}, // Newest
	}

	tests := []struct {
		name      string
		req       *agentv1.QueryUprobeEventsRequest
		events    []*meshv1.EbpfEvent
		wantCount int
		wantFirst *timestamppb.Timestamp
	}{
		{
			name:      "No limits",
			req:       &agentv1.QueryUprobeEventsRequest{},
			events:    events,
			wantCount: 3,
			wantFirst: events[0].Timestamp,
		},
		{
			name: "Max events (current behavior - returns oldest)",
			req: &agentv1.QueryUprobeEventsRequest{
				MaxEvents: 2,
			},
			events:    events,
			wantCount: 2,
			wantFirst: events[0].Timestamp, // Currently returns oldest
		},
		{
			name: "Max events with StartTime (streaming)",
			req: &agentv1.QueryUprobeEventsRequest{
				MaxEvents: 2,
				StartTime: timestamppb.New(now.Add(-6 * time.Minute)),
			},
			events:    events,
			wantCount: 2,
			wantFirst: events[1].Timestamp,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterEvents(tt.events, tt.req)
			assert.Len(t, got, tt.wantCount)
			if len(got) > 0 {
				assert.Equal(t, tt.wantFirst, got[0].Timestamp)
			}
		})
	}
}

// Copy of the logic from debug_service.go for testing purposes before refactoring
func filterEvents(events []*meshv1.EbpfEvent, req *agentv1.QueryUprobeEventsRequest) []*meshv1.EbpfEvent {
	var filteredEvents []*meshv1.EbpfEvent
	for _, event := range events {
		// Check time range
		if req.StartTime != nil && event.Timestamp.AsTime().Before(req.StartTime.AsTime()) {
			continue
		}
		if req.EndTime != nil && event.Timestamp.AsTime().After(req.EndTime.AsTime()) {
			continue
		}

		filteredEvents = append(filteredEvents, event)

		// Check max events limit
		if req.MaxEvents > 0 && len(filteredEvents) >= int(req.MaxEvents) {
			break
		}
	}
	return filteredEvents
}

// --- Correlation integration tests (RFD 091) ---

// newTestAgent creates an Agent wired with a correlation engine for testing.
func newTestAgent(t *testing.T) *Agent {
	t.Helper()
	a, err := New(Config{AgentID: "test-agent", Context: context.Background()})
	require.NoError(t, err)
	return a
}

// makeUprobeEvent creates a minimal UprobeEvent for correlation testing.
func makeUprobeEvent(fn string, durationNs uint64) *agentv1.UprobeEvent {
	return &agentv1.UprobeEvent{
		FunctionName: fn,
		DurationNs:   durationNs,
		EventType:    "return",
		ServiceName:  "svc",
		Labels:       map[string]string{},
	}
}

// TestCorrelationIntegration_RateGate deploys a rate_gate descriptor and verifies
// the engine fires after the threshold count is reached within the window.
func TestCorrelationIntegration_RateGate(t *testing.T) {
	a := newTestAgent(t)
	svc := NewDebugService(a, a.logger)
	engine := a.GetCorrelationEngine()
	require.NotNil(t, engine)

	desc := &agentv1.CorrelationDescriptor{
		Id:       "corr-rate-gate",
		Strategy: agentv1.StrategyKind_RATE_GATE,
		Source: &agentv1.SourceSpec{
			Probe:      "db.Query",
			FilterExpr: "event.duration_ns > 50000000", // >50ms
		},
		Window:    durationpb.New(500 * time.Millisecond),
		Threshold: 3,
		Action:    &agentv1.ActionSpec{Kind: agentv1.ActionKind_EMIT_EVENT},
	}

	// Deploy via RPC handler.
	resp, err := svc.DeployCorrelation(context.Background(), &agentv1.DeployCorrelationRequest{
		Descriptor_: desc,
	})
	require.NoError(t, err)
	assert.Equal(t, "corr-rate-gate", resp.CorrelationId)

	// Verify it appears in ListCorrelations.
	list, err := svc.ListCorrelations(context.Background(), &agentv1.ListCorrelationsRequest{})
	require.NoError(t, err)
	assert.Len(t, list.Descriptors, 1)

	// Feed 2 slow events — below threshold, no action yet.
	var firedActions []*agentv1.TriggerEvent
	for i := 0; i < 2; i++ {
		actions := engine.OnEvent(makeUprobeEvent("db.Query", 100_000_000)) // 100ms
		for _, a := range actions {
			if a.TriggerEvent != nil {
				firedActions = append(firedActions, a.TriggerEvent)
			}
		}
	}
	assert.Empty(t, firedActions, "expected no trigger before threshold")

	// 3rd slow event crosses the threshold.
	actions := engine.OnEvent(makeUprobeEvent("db.Query", 100_000_000))
	for _, a := range actions {
		if a.TriggerEvent != nil {
			firedActions = append(firedActions, a.TriggerEvent)
		}
	}
	require.Len(t, firedActions, 1, "expected trigger at threshold")
	assert.Equal(t, "corr-rate-gate", firedActions[0].CorrelationId)
	assert.Equal(t, "RATE_GATE", firedActions[0].Strategy)

	// Fast events (below filter) must not count.
	actions = engine.OnEvent(makeUprobeEvent("db.Query", 10_000_000)) // 10ms — below filter
	assert.Empty(t, actions, "fast event should not trigger")

	// Remove via RPC and verify list is empty.
	_, err = svc.RemoveCorrelation(context.Background(), &agentv1.RemoveCorrelationRequest{
		CorrelationId: "corr-rate-gate",
	})
	require.NoError(t, err)
	list, _ = svc.ListCorrelations(context.Background(), &agentv1.ListCorrelationsRequest{})
	assert.Empty(t, list.Descriptors)
}

// TestCorrelationIntegration_CausalPair verifies that a causal_pair descriptor
// only fires when events from source A and source B share the same join field.
func TestCorrelationIntegration_CausalPair(t *testing.T) {
	a := newTestAgent(t)
	engine := a.GetCorrelationEngine()
	require.NotNil(t, engine)

	svc := NewDebugService(a, a.logger)

	desc := &agentv1.CorrelationDescriptor{
		Id:       "corr-causal",
		Strategy: agentv1.StrategyKind_CAUSAL_PAIR,
		SourceA: &agentv1.SourceSpec{
			Probe:      "db.Query",
			FilterExpr: "event.duration_ns > 100000000", // >100ms
		},
		SourceB: &agentv1.SourceSpec{
			Probe: "http.ServeHTTP",
		},
		JoinOn: "trace_id",
		Window: durationpb.New(200 * time.Millisecond),
		Action: &agentv1.ActionSpec{Kind: agentv1.ActionKind_EMIT_EVENT},
	}

	_, err := svc.DeployCorrelation(context.Background(), &agentv1.DeployCorrelationRequest{
		Descriptor_: desc,
	})
	require.NoError(t, err)

	// A slow DB event on trace "t1".
	dbEvent := makeUprobeEvent("db.Query", 200_000_000) // 200ms
	dbEvent.Labels = map[string]string{"trace_id": "t1"}
	actions := engine.OnEvent(dbEvent)
	assert.Empty(t, actions, "no trigger: B not seen yet")

	// HTTP event on a different trace — must NOT fire (mismatched join).
	httpWrong := makeUprobeEvent("http.ServeHTTP", 0)
	httpWrong.Labels = map[string]string{"trace_id": "t2"}
	actions = engine.OnEvent(httpWrong)
	assert.Empty(t, actions, "no trigger: trace_id mismatch")

	// HTTP event on the same trace — MUST fire.
	httpMatch := makeUprobeEvent("http.ServeHTTP", 0)
	httpMatch.Labels = map[string]string{"trace_id": "t1"}
	actions = engine.OnEvent(httpMatch)
	require.Len(t, actions, 1, "expected trigger on matching trace_id")
	assert.Equal(t, "corr-causal", actions[0].CorrelationID)
}

func TestUpdateProbeFilter(t *testing.T) {
	agentCfg := Config{
		AgentID: "test-agent",
		Context: context.Background(),
	}
	a, err := New(agentCfg)
	assert.NoError(t, err)

	svc := NewDebugService(a, a.logger)

	tests := []struct {
		name        string
		req         *agentv1.UpdateProbeFilterRequest
		wantSuccess bool
	}{
		{
			name:        "Nil filter returns quickly",
			req:         &agentv1.UpdateProbeFilterRequest{CollectorId: "some-id", Filter: nil},
			wantSuccess: true, // Should succeed with no-op
		},
		{
			name: "Fails gracefully when collector not found",
			req: &agentv1.UpdateProbeFilterRequest{
				CollectorId: "non-existent",
				Filter: &agentv1.UprobeFilter{
					SampleRate: 42,
				},
			},
			wantSuccess: false, // ebpfManager will return collector not found
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.UpdateProbeFilter(context.Background(), tt.req)
			if tt.wantSuccess {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "collector not found")
			}
		})
	}
}
