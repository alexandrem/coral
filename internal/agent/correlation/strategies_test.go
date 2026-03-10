package correlation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
)

// makeEvent is a test helper that builds a minimal UprobeEvent.
func makeEvent(functionName string, durationNs uint64) *agentv1.UprobeEvent {
	return &agentv1.UprobeEvent{
		FunctionName: functionName,
		DurationNs:   durationNs,
		EventType:    "return",
		ServiceName:  "test-service",
		AgentId:      "agent-1",
		Labels:       map[string]string{},
	}
}

// makeDesc builds a minimal CorrelationDescriptor for a given strategy.
func makeDesc(strategy agentv1.StrategyKind) *agentv1.CorrelationDescriptor {
	return &agentv1.CorrelationDescriptor{
		Id:          "corr-test",
		Strategy:    strategy,
		ServiceName: "test-service",
		Action:      &agentv1.ActionSpec{Kind: agentv1.ActionKind_EMIT_EVENT},
	}
}

// --- CEL validation ---

func TestCompileCEL_valid(t *testing.T) {
	prog, err := CompileCEL("event.duration_ns > 100")
	require.NoError(t, err)
	require.NotNil(t, prog)
}

func TestCompileCEL_empty(t *testing.T) {
	prog, err := CompileCEL("")
	require.NoError(t, err)
	assert.Nil(t, prog)
}

func TestCompileCEL_invalid(t *testing.T) {
	_, err := CompileCEL("event.duration_ns >>>> 100")
	require.Error(t, err)
}

func TestEvalCEL_nilProg(t *testing.T) {
	event := makeEvent("db.Query", 500)
	match, err := EvalCEL(nil, event)
	require.NoError(t, err)
	assert.True(t, match)
}

func TestEvalCEL_match(t *testing.T) {
	prog, err := CompileCEL("event.duration_ns > 100")
	require.NoError(t, err)
	event := makeEvent("db.Query", 200)
	match, err := EvalCEL(prog, event)
	require.NoError(t, err)
	assert.True(t, match)
}

func TestEvalCEL_noMatch(t *testing.T) {
	prog, err := CompileCEL("event.duration_ns > 100")
	require.NoError(t, err)
	event := makeEvent("db.Query", 50)
	match, err := EvalCEL(prog, event)
	require.NoError(t, err)
	assert.False(t, match)
}

// --- RateGate ---

func TestRateGate_firesAtThreshold(t *testing.T) {
	d := makeDesc(agentv1.StrategyKind_RATE_GATE)
	d.Source = &agentv1.SourceSpec{Probe: "db.Query"}
	d.Threshold = 3
	d.Window = durationpb.New(500 * time.Millisecond)
	d.CooldownMs = 0

	ev, err := newRateGate(d, "agent-1", testLogger())
	require.NoError(t, err)

	event := makeEvent("db.Query", 100)

	// First two events should not fire.
	action, err := ev.OnEvent(event)
	require.NoError(t, err)
	assert.Nil(t, action)

	action, err = ev.OnEvent(event)
	require.NoError(t, err)
	assert.Nil(t, action)

	// Third event fires.
	action, err = ev.OnEvent(event)
	require.NoError(t, err)
	require.NotNil(t, action)
	assert.Equal(t, "corr-test", action.CorrelationID)
}

func TestRateGate_ignoredWrongProbe(t *testing.T) {
	d := makeDesc(agentv1.StrategyKind_RATE_GATE)
	d.Source = &agentv1.SourceSpec{Probe: "db.Query"}
	d.Threshold = 1
	d.Window = durationpb.New(500 * time.Millisecond)

	ev, err := newRateGate(d, "agent-1", testLogger())
	require.NoError(t, err)

	action, err := ev.OnEvent(makeEvent("other.Func", 100))
	require.NoError(t, err)
	assert.Nil(t, action)
}

func TestRateGate_filterExpr(t *testing.T) {
	d := makeDesc(agentv1.StrategyKind_RATE_GATE)
	d.Source = &agentv1.SourceSpec{Probe: "db.Query", FilterExpr: "event.duration_ns > 200"}
	d.Threshold = 2
	d.Window = durationpb.New(500 * time.Millisecond)

	ev, err := newRateGate(d, "agent-1", testLogger())
	require.NoError(t, err)

	// Two fast events (below filter threshold) should not fire.
	action, _ := ev.OnEvent(makeEvent("db.Query", 100))
	assert.Nil(t, action)
	action, _ = ev.OnEvent(makeEvent("db.Query", 150))
	assert.Nil(t, action)

	// Two slow events should fire.
	action, _ = ev.OnEvent(makeEvent("db.Query", 300))
	assert.Nil(t, action)
	action, _ = ev.OnEvent(makeEvent("db.Query", 400))
	require.NotNil(t, action)
}

func TestRateGate_windowExpiry(t *testing.T) {
	d := makeDesc(agentv1.StrategyKind_RATE_GATE)
	d.Source = &agentv1.SourceSpec{Probe: "db.Query"}
	d.Threshold = 3
	d.Window = durationpb.New(1 * time.Millisecond)

	ev, err := newRateGate(d, "agent-1", testLogger())
	require.NoError(t, err)

	event := makeEvent("db.Query", 100)

	// Two events...
	ev.OnEvent(event) //nolint:errcheck
	ev.OnEvent(event) //nolint:errcheck

	// Wait for the window to expire.
	time.Sleep(5 * time.Millisecond)

	// Third event arrives outside window — should not fire.
	action, err := ev.OnEvent(event)
	require.NoError(t, err)
	assert.Nil(t, action)
}

// --- EdgeTrigger ---

func TestEdgeTrigger_risingEdgeFires(t *testing.T) {
	d := makeDesc(agentv1.StrategyKind_EDGE_TRIGGER)
	d.Source = &agentv1.SourceSpec{Probe: "pay.Process", FilterExpr: "event.duration_ns > 200"}

	ev, err := newEdgeTrigger(d, "agent-1", testLogger())
	require.NoError(t, err)

	// Fast call — filter does not match; lastMatched = false.
	action, _ := ev.OnEvent(makeEvent("pay.Process", 100))
	assert.Nil(t, action)

	// Slow call — filter matches, rising edge fires.
	action, err = ev.OnEvent(makeEvent("pay.Process", 300))
	require.NoError(t, err)
	require.NotNil(t, action)
	assert.Equal(t, agentv1.ActionKind_EMIT_EVENT, action.Kind)
}

func TestEdgeTrigger_noRefire(t *testing.T) {
	d := makeDesc(agentv1.StrategyKind_EDGE_TRIGGER)
	d.Source = &agentv1.SourceSpec{Probe: "pay.Process", FilterExpr: "event.duration_ns > 200"}
	d.CooldownMs = 10000 // 10 seconds

	ev, err := newEdgeTrigger(d, "agent-1", testLogger())
	require.NoError(t, err)

	// First rising edge fires.
	ev.OnEvent(makeEvent("pay.Process", 100)) //nolint:errcheck
	action, _ := ev.OnEvent(makeEvent("pay.Process", 300))
	require.NotNil(t, action)

	// Second slow call — no rising edge (still matched).
	action, _ = ev.OnEvent(makeEvent("pay.Process", 400))
	assert.Nil(t, action)
}

// --- CausalPair ---

func TestCausalPair_matchingJoin(t *testing.T) {
	d := makeDesc(agentv1.StrategyKind_CAUSAL_PAIR)
	d.SourceA = &agentv1.SourceSpec{Probe: "db.Query"}
	d.SourceB = &agentv1.SourceSpec{Probe: "http.Handler"}
	d.JoinOn = "trace_id"
	d.Window = durationpb.New(500 * time.Millisecond)

	ev, err := newCausalPair(d, "agent-1", testLogger())
	require.NoError(t, err)

	// Source A event with trace_id = "t1".
	evtA := makeEvent("db.Query", 500)
	evtA.Labels["trace_id"] = "t1"
	action, _ := ev.OnEvent(evtA)
	assert.Nil(t, action) // No B yet.

	// Source B event with same trace_id.
	evtB := makeEvent("http.Handler", 100)
	evtB.Labels["trace_id"] = "t1"
	action, err = ev.OnEvent(evtB)
	require.NoError(t, err)
	require.NotNil(t, action)
	assert.Equal(t, "corr-test", action.CorrelationID)
}

func TestCausalPair_mismatchedJoin(t *testing.T) {
	d := makeDesc(agentv1.StrategyKind_CAUSAL_PAIR)
	d.SourceA = &agentv1.SourceSpec{Probe: "db.Query"}
	d.SourceB = &agentv1.SourceSpec{Probe: "http.Handler"}
	d.JoinOn = "trace_id"
	d.Window = durationpb.New(500 * time.Millisecond)

	ev, err := newCausalPair(d, "agent-1", testLogger())
	require.NoError(t, err)

	evtA := makeEvent("db.Query", 500)
	evtA.Labels["trace_id"] = "t1"
	ev.OnEvent(evtA) //nolint:errcheck

	// Different trace_id on B — should not fire.
	evtB := makeEvent("http.Handler", 100)
	evtB.Labels["trace_id"] = "t2"
	action, err := ev.OnEvent(evtB)
	require.NoError(t, err)
	assert.Nil(t, action)
}

// --- PercentileAlarm ---

func TestPercentileAlarm_firesAboveThreshold(t *testing.T) {
	d := makeDesc(agentv1.StrategyKind_PERCENTILE_ALARM)
	d.Source = &agentv1.SourceSpec{Probe: "db.Query"}
	d.Field = "duration_ns"
	d.Percentile = 0.99
	d.Threshold = 400 // 400ns
	d.Window = durationpb.New(5 * time.Second)

	ev, err := newPercentileAlarm(d, "agent-1", testLogger())
	require.NoError(t, err)

	// Feed 9 fast events and 1 slow event.
	// P99 of [50(×9), 500]: idx=0.99*9=8.91 → interpolate vals[8]=50, vals[9]=500
	// P99 ≈ 50*0.09 + 500*0.91 ≈ 460 > 400 → should fire on last event.
	for i := 0; i < 9; i++ {
		ev.OnEvent(makeEvent("db.Query", 50)) //nolint:errcheck
	}
	action, err := ev.OnEvent(makeEvent("db.Query", 500))
	require.NoError(t, err)
	require.NotNil(t, action)
}

func TestPercentileAlarm_doesNotFireBelowThreshold(t *testing.T) {
	d := makeDesc(agentv1.StrategyKind_PERCENTILE_ALARM)
	d.Source = &agentv1.SourceSpec{Probe: "db.Query"}
	d.Field = "duration_ns"
	d.Percentile = 0.99
	d.Threshold = 10000 // high threshold
	d.Window = durationpb.New(5 * time.Second)

	ev, err := newPercentileAlarm(d, "agent-1", testLogger())
	require.NoError(t, err)

	for i := 0; i < 100; i++ {
		action, err := ev.OnEvent(makeEvent("db.Query", 100))
		require.NoError(t, err)
		assert.Nil(t, action)
	}
}

// --- Sequence ---

func TestSequence_orderedFires(t *testing.T) {
	d := makeDesc(agentv1.StrategyKind_SEQUENCE)
	d.SourceA = &agentv1.SourceSpec{Probe: "cache.Miss"}
	d.SourceB = &agentv1.SourceSpec{Probe: "db.Query"}
	d.Window = durationpb.New(500 * time.Millisecond)

	ev, err := newSequence(d, "agent-1", testLogger())
	require.NoError(t, err)

	// A then B in order.
	action, _ := ev.OnEvent(makeEvent("cache.Miss", 0))
	assert.Nil(t, action)

	action, err = ev.OnEvent(makeEvent("db.Query", 200))
	require.NoError(t, err)
	require.NotNil(t, action)
}

func TestSequence_wrongOrderDoesNotFire(t *testing.T) {
	d := makeDesc(agentv1.StrategyKind_SEQUENCE)
	d.SourceA = &agentv1.SourceSpec{Probe: "cache.Miss"}
	d.SourceB = &agentv1.SourceSpec{Probe: "db.Query"}
	d.Window = durationpb.New(500 * time.Millisecond)

	ev, err := newSequence(d, "agent-1", testLogger())
	require.NoError(t, err)

	// B before A — should not fire.
	action, _ := ev.OnEvent(makeEvent("db.Query", 200))
	assert.Nil(t, action)

	action, _ = ev.OnEvent(makeEvent("cache.Miss", 0))
	assert.Nil(t, action)
}

// --- Engine ---

func TestEngine_deployAndRoute(t *testing.T) {
	engine := NewEngine("agent-1", testLogger())

	d := makeDesc(agentv1.StrategyKind_RATE_GATE)
	d.Source = &agentv1.SourceSpec{Probe: "db.Query"}
	d.Threshold = 2
	d.Window = durationpb.New(500 * time.Millisecond)

	err := engine.Deploy(d, nil)
	require.NoError(t, err)

	assert.Len(t, engine.List(), 1)

	// First event — no fire.
	actions := engine.OnEvent(makeEvent("db.Query", 100))
	assert.Empty(t, actions)

	// Second event — fires.
	actions = engine.OnEvent(makeEvent("db.Query", 100))
	assert.Len(t, actions, 1)
}

func TestEngine_remove(t *testing.T) {
	engine := NewEngine("agent-1", testLogger())

	d := makeDesc(agentv1.StrategyKind_RATE_GATE)
	d.Source = &agentv1.SourceSpec{Probe: "db.Query"}
	d.Threshold = 1
	d.Window = durationpb.New(500 * time.Millisecond)

	require.NoError(t, engine.Deploy(d, nil))
	assert.Len(t, engine.List(), 1)

	ok := engine.Remove(d.Id)
	assert.True(t, ok)
	assert.Empty(t, engine.List())
}

func TestEngine_unknownStrategyReturnsError(t *testing.T) {
	engine := NewEngine("agent-1", testLogger())

	d := &agentv1.CorrelationDescriptor{
		Id:       "bad",
		Strategy: agentv1.StrategyKind(99),
		Action:   &agentv1.ActionSpec{Kind: agentv1.ActionKind_EMIT_EVENT},
	}
	err := engine.Deploy(d, nil)
	require.Error(t, err)
}
