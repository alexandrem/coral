---
rfd: "091"
title: "Probe Correlation DSL"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "059", "061", "090" ]
database_migrations: [ ]
areas: [ "agent", "colony", "debugging", "ai" ]
---

# RFD 091 - Probe Correlation DSL

**Status:** 🚧 Draft

## Summary

Define a declarative `CorrelationDescriptor` (expressed as protobuf) that the
colony LLM generates to detect temporal event patterns directly on agents.
The agent evaluates descriptors using a Go strategy engine with CEL filter
predicates, replacing the need for a scripting runtime. This closes the
feedback loop for AI-driven debugging: LLM observes anomaly → generates
descriptor → deploys to agent → receives structured trigger events → refines
hypothesis.

## Problem

**Current behavior/limitations:**

The existing uprobe system is stateless and passive. The colony attaches a
probe and receives a raw event stream. Detecting patterns in that stream —
"three slow calls within 500ms," "a DB error that coincides with a slow HTTP
request" — requires the colony to receive all events, store them, and run
post-hoc queries against DuckDB.

This creates three gaps:

1. **Latency**: By the time the colony queries its DuckDB for a pattern, the
   relevant process state (goroutine stacks, CPU registers) has changed.
   Snapshot actions must fire within milliseconds of the pattern, not seconds.
2. **Volume**: The colony must receive every event to find the few that match,
   even when RFD 090 filtering has reduced the stream. Cross-probe correlation
   (join events from two different functions) cannot be expressed as a simple
   per-event filter.
3. **LLM iteration cost**: The LLM issues a probe, waits for a full data dump,
   reasons over it, and issues another probe. Stateful pattern detection on the
   agent would let the LLM specify *what to wait for* and be notified only when
   the condition is met, enabling tighter investigation loops.

**Why this matters:**

Coral's value proposition is giving AI assistants direct, real-time access to
running distributed systems. That requires not just collecting events but
detecting meaningful patterns close to the data. Without agent-side
correlation, Coral is limited to offline analysis of event snapshots, not
live pattern detection.

**Use cases affected:**

- "Capture a goroutine snapshot the next time three DB calls each take longer
  than 100ms within a 500ms window."
- "Alert when an HTTP error immediately follows a slow DB call on the same
  trace ID."
- "Tell me every time the payment function transitions from fast to slow
  (edge trigger)."
- "After 10 seconds with no calls to `validateCart`, emit an inactivity
  alert."

## Solution

Define a finite set of named correlation strategies implemented as Go state
machines. The colony LLM generates a `CorrelationDescriptor` protobuf
selecting one strategy and supplying its parameters (including CEL filter
expressions). The agent instantiates the corresponding Go evaluator, feeds it
the filtered event stream from RFD 090, and fires a configurable action when
the strategy's condition is satisfied.

**Key Design Decisions:**

- **Declarative descriptors, not code**: The LLM generates structured config,
  not a scripting language. The descriptor schema is validated at the colony
  before being sent to the agent. Syntax errors and type mismatches are caught
  before deployment, not at runtime.
- **CEL for filter predicates**: `google/cel-go` (pure Go, ~1 MB, no CGo)
  evaluates per-event filter expressions like `event.duration_ns > 100000000`.
  CEL is not Turing-complete and always terminates, giving a bounded execution
  guarantee within the eBPF event processing path.
- **Finite strategy set**: Six strategies cover the realistic debugging
  patterns. Each is ~100 lines of idiomatic Go. New strategies require a code
  change and RFD, not a scripting runtime upgrade.
- **No scripting runtime on the agent**: Lua, Wasm, and TypeScript runtimes
  are all rejected. The per-VM overhead, sandboxing complexity, and LLM
  codegen reliability for Lua/Wasm do not justify a runtime dependency on
  resource-constrained agent nodes.
- **Colony validates before deploying**: The colony compiles CEL expressions
  and checks descriptor fields before forwarding to the agent. An invalid
  descriptor never reaches an agent.
- **Escape hatch via CLI TypeScript**: Patterns the descriptor language cannot
  express are handled by the existing CLI-side TypeScript scripting (RFD 076),
  which operates over colony-aggregated data after the fact.

**Benefits:**

- Pattern detection fires at the agent within microseconds of the condition
  being met — enabling time-sensitive actions like goroutine snapshots.
- Colony receives only trigger events, not raw event streams, for correlation
  use cases.
- LLM generates a JSON/proto document rather than code, reducing generation
  errors and enabling schema validation.
- Strategy implementations are auditable Go code, not arbitrary scripts.

**Architecture Overview:**

```
Colony LLM
    │ generates CorrelationDescriptor (protobuf)
    │ validates CEL expressions before sending
    ▼
Colony DebugService
    │ DeployCorrelation → routes to target agent
    ▼
Agent CorrelationEngine
    │ instantiates StrategyEvaluator for the descriptor
    ▼
Event stream (from UprobeCollector, RFD 090 filtered)
    │ each event passed through CEL filter predicate
    │ matching events fed to strategy state machine
    │
    ├── no match yet → accumulate state (counter, timestamps, pairs)
    │
    └── condition satisfied
            │
            ▼
        Action dispatcher
            ├── emit_event    → colony (structured trigger event)
            ├── goroutine_snapshot → agent debug service (RFD 059)
            └── cpu_profile   → agent profiler (RFD 070)
```

### Correlation Strategies

#### rate_gate
Trigger when N or more events matching the filter occur within a sliding
window of duration T.

*Example*: "Three DB calls slower than 100ms within 500ms."

#### edge_trigger
Trigger once when the filter transitions from not matching to matching. Reset
after a configurable cooldown period.

*Example*: "First time `processPayment` exceeds 200ms after being fast."

#### causal_pair
Trigger when an event from source A is followed by an event from source B
within window T, sharing a common field value (e.g., `trace_id`).

*Example*: "A slow DB call followed by an HTTP 5xx on the same trace."

#### absence
Trigger when no event matching the filter occurs within window T. Resets on
each matching event.

*Example*: "No calls to `validateCart` for 10 seconds."

#### percentile_alarm
Trigger when the rolling percentile (P50/P90/P99) of a numeric event field
exceeds a threshold, evaluated over a sliding window.

*Example*: "P99 of `db.Query` duration exceeds 500ms over a 30-second window."

#### sequence
Trigger when events matching filter A are followed by events matching filter B
within window T, in that order.

*Example*: "A cache miss followed by a DB call within 10ms."

### Component Changes

1. **Agent correlation engine** (new: `internal/agent/correlation/`):
    - `Evaluator` interface implemented by each of the six strategies.
    - `Engine` manages active evaluators, routes events from collectors, and
      dispatches actions.
    - CEL environment shared across evaluators; expressions compiled once at
      descriptor load time.

2. **Agent debug service** (`internal/agent/debug_service.go`):
    - Handle `DeployCorrelation`, `RemoveCorrelation`, `ListCorrelations` RPCs.
    - Wire events from active `UprobeCollector` instances into the `Engine`.
    - Dispatch `goroutine_snapshot` and `cpu_profile` actions via existing
      services.

3. **Colony debug orchestrator**:
    - Validate CEL expressions in `CorrelationDescriptor` before forwarding.
    - Implement `DeployCorrelation` RPC that routes to the correct agent by
      service name.
    - Receive `emit_event` trigger events from agents and store in
      `correlation_triggers` DuckDB table.

4. **Protobuf** (new: `proto/coral/agent/v1/correlation.proto`):
    - `CorrelationDescriptor`, `SourceSpec`, `ActionSpec`, all strategy-specific
      parameter fields.
    - `DeployCorrelation`, `RemoveCorrelation`, `ListCorrelations` RPCs on the
      agent `DebugService`.

## API Changes

### New Protobuf Messages

```protobuf
// proto/coral/agent/v1/correlation.proto

syntax = "proto3";
package coral.agent.v1;

import "google/protobuf/duration.proto";

// StrategyKind selects the temporal correlation pattern to evaluate.
enum StrategyKind {
    RATE_GATE        = 0;
    EDGE_TRIGGER     = 1;
    CAUSAL_PAIR      = 2;
    ABSENCE          = 3;
    PERCENTILE_ALARM = 4;
    SEQUENCE         = 5;
}

// ActionKind defines what the agent does when the strategy condition fires.
enum ActionKind {
    EMIT_EVENT         = 0;  // Send a TriggerEvent to the colony.
    GOROUTINE_SNAPSHOT = 1;  // Capture goroutine stacks for the service.
    CPU_PROFILE        = 2;  // Capture a CPU profile (RFD 070).
}

// SourceSpec identifies a probe and an optional CEL filter predicate.
message SourceSpec {
    // probe is the fully-qualified function name (e.g., "db.Query").
    string probe = 1;

    // filter_expr is a CEL expression evaluated against each event.
    // Available fields: event.duration_ns, event.pid, event.tid,
    // event.function_name, event.trace_id (if present).
    // Empty string means accept all events from this probe.
    string filter_expr = 2;
}

// ActionSpec describes the action to take when the strategy fires.
message ActionSpec {
    ActionKind kind = 1;

    // profile_duration_ms controls how long a CPU profile is captured.
    // Only used when kind = CPU_PROFILE.
    uint32 profile_duration_ms = 2;
}

// CorrelationDescriptor is the unit of deployment for agent-side correlation.
// The colony LLM generates this; the agent evaluates it.
message CorrelationDescriptor {
    // id is a colony-assigned unique identifier for this descriptor.
    string id = 1;

    // strategy selects the evaluation pattern.
    StrategyKind strategy = 2;

    // source is the primary (or only) event source.
    // Used by: RATE_GATE, EDGE_TRIGGER, ABSENCE, PERCENTILE_ALARM, SEQUENCE.
    SourceSpec source = 3;

    // source_a and source_b are used by CAUSAL_PAIR and SEQUENCE.
    SourceSpec source_a = 4;
    SourceSpec source_b = 5;

    // join_on is the event field name used to correlate source_a and source_b
    // events. Used by CAUSAL_PAIR. Example: "trace_id".
    string join_on = 6;

    // window is the sliding time window over which the strategy is evaluated.
    google.protobuf.Duration window = 7;

    // threshold is the event count trigger for RATE_GATE (fire when count >=
    // threshold) and the numeric threshold for PERCENTILE_ALARM.
    double threshold = 8;

    // field is the numeric event field name for PERCENTILE_ALARM.
    // Example: "duration_ns".
    string field = 9;

    // percentile is the percentile to compute for PERCENTILE_ALARM (0.0–1.0).
    // Example: 0.99 for P99.
    double percentile = 10;

    // action defines what happens when the strategy condition fires.
    ActionSpec action = 11;

    // cooldown_ms is the minimum interval between consecutive firings.
    // Prevents action storms when the condition is persistently true.
    uint32 cooldown_ms = 12;
}

// TriggerEvent is emitted to the colony when a strategy fires with
// kind = EMIT_EVENT.
message TriggerEvent {
    string correlation_id = 1;
    string strategy       = 2;
    google.protobuf.Timestamp fired_at = 3;
    string agent_id       = 4;
    string service_name   = 5;
    // context contains strategy-specific detail (count, matched events, etc.)
    map<string, string> context = 6;
}
```

### New RPC Endpoints

```protobuf
// proto/coral/agent/v1/agent.proto

service DebugService {
    // ... existing RPCs ...

    // DeployCorrelation installs a correlation descriptor on the agent.
    // The agent begins evaluating it against the active event stream immediately.
    rpc DeployCorrelation(DeployCorrelationRequest) returns (DeployCorrelationResponse);

    // RemoveCorrelation uninstalls an active correlation descriptor.
    rpc RemoveCorrelation(RemoveCorrelationRequest) returns (RemoveCorrelationResponse);

    // ListCorrelations returns all active descriptors on this agent.
    rpc ListCorrelations(ListCorrelationsRequest) returns (ListCorrelationsResponse);
}

message DeployCorrelationRequest {
    CorrelationDescriptor descriptor = 1;
}

message DeployCorrelationResponse {
    string correlation_id = 1;
}

message RemoveCorrelationRequest {
    string correlation_id = 1;
}

message RemoveCorrelationResponse {}

message ListCorrelationsRequest {}

message ListCorrelationsResponse {
    repeated CorrelationDescriptor descriptors = 1;
}
```

```protobuf
// proto/coral/colony/v1/debug.proto

service DebugService {
    // ... existing RPCs ...

    // DeployCorrelation validates the descriptor, resolves the target agent for
    // the named service, and forwards the deployment.
    rpc DeployCorrelation(ColonyDeployCorrelationRequest) returns (ColonyDeployCorrelationResponse);

    // RemoveCorrelation routes removal to the correct agent.
    rpc RemoveCorrelation(ColonyRemoveCorrelationRequest) returns (ColonyRemoveCorrelationResponse);

    // ListCorrelations returns descriptors across all agents, optionally filtered
    // by service.
    rpc ListCorrelations(ColonyListCorrelationsRequest) returns (ColonyListCorrelationsResponse);
}

message ColonyDeployCorrelationRequest {
    string service_name = 1;
    CorrelationDescriptor descriptor = 2;
}

message ColonyDeployCorrelationResponse {
    string correlation_id = 1;
    string agent_id       = 2;
}
```

### CLI Commands

```bash
# List active correlations across the mesh
coral debug correlations

# Output:
ID              Service    Strategy     Window  Threshold  Action
corr-abc123     payments   rate_gate    500ms   3          goroutine_snapshot
corr-def456     orders     causal_pair  100ms   —          emit_event

# Remove a correlation
coral debug correlations remove corr-abc123
```

Correlations are created by the colony LLM through the MCP interface, not
directly by CLI users. The CLI provides read and remove operations for
operator visibility and incident cleanup.

## Implementation Plan

### Phase 1: Protobuf and CEL Foundation

- [ ] Define `proto/coral/agent/v1/correlation.proto` with all messages above.
- [ ] Add `DeployCorrelation`, `RemoveCorrelation`, `ListCorrelations` RPCs to
  agent `DebugService` proto.
- [ ] Add colony-side `DebugService` RPCs for correlation routing.
- [ ] Add `google/cel-go` dependency; implement shared CEL environment in
  `internal/agent/correlation/cel.go` with the standard event field set.
- [ ] Validate CEL compilation at descriptor load time (fail fast with a
  descriptive error).

### Phase 2: Strategy Implementations

- [ ] Implement `Evaluator` interface and `Engine` in
  `internal/agent/correlation/`.
- [ ] Implement `RateGateEvaluator` (sliding window counter).
- [ ] Implement `EdgeTriggerEvaluator` (boolean state with cooldown).
- [ ] Implement `CausalPairEvaluator` (two-source join with expiry).
- [ ] Implement `AbsenceEvaluator` (inactivity timer reset on match).
- [ ] Implement `PercentileAlarmEvaluator` (T-Digest or fixed-bucket over
  sliding window).
- [ ] Implement `SequenceEvaluator` (ordered two-stage match).

### Phase 3: Agent Integration

- [ ] Implement `DeployCorrelation`, `RemoveCorrelation`, `ListCorrelations`
  RPC handlers in the agent debug service.
- [ ] Wire events from `UprobeCollector` instances into `Engine.OnEvent`.
- [ ] Implement `goroutine_snapshot` and `cpu_profile` action dispatch.
- [ ] Implement `emit_event` action: stream `TriggerEvent` back to colony.

### Phase 4: Colony Routing and Storage

- [ ] Colony validates CEL expressions before forwarding descriptor to agent.
- [ ] Colony routes `DeployCorrelation` to the correct agent by service name
  via the existing agent connection pool.
- [ ] Add `correlation_triggers` table to colony DuckDB for `TriggerEvent`
  records.
- [ ] Expose `ListCorrelations` and remove CLI commands.
- [ ] Add MCP tool `coral_deploy_correlation` for LLM use.

### Phase 5: Testing

- [ ] Unit test each strategy evaluator: confirm trigger conditions, window
  expiry, cooldown, and reset behaviour.
- [ ] Unit test CEL validation: reject invalid expressions at deploy time.
- [ ] Integration test: deploy `rate_gate` descriptor against a live uprobe
  session, verify `TriggerEvent` fires at correct count.
- [ ] Integration test: deploy `causal_pair`, verify join fires only when
  `join_on` field matches across both sources.
- [ ] E2E test: colony LLM generates descriptor → deploys → agent fires action
  → colony receives `TriggerEvent`.

## Security Considerations

**CEL execution safety**: CEL is not Turing-complete and has no I/O
primitives. Expressions cannot loop indefinitely, allocate unbounded memory,
or access the filesystem. Execution time is bounded by the expression
complexity, which the colony validates before deployment.

**Descriptor authority**: Only colony principals authorised under the existing
RBAC model (RFD 058) may call `DeployCorrelation`. An agent rejects
descriptors not signed by a colony it trusts (mTLS session identity, RFD 020).

**Action blast radius**: `goroutine_snapshot` and `cpu_profile` actions add
overhead to the target service. The `cooldown_ms` field limits how frequently
an action can fire. The colony caps `cooldown_ms` at a minimum of 1000ms
before forwarding. The `cpu_profile` action duration is capped at the value
configured in the agent's profiler settings (RFD 070).

**Audit**: Every `DeployCorrelation` and `TriggerEvent` is appended to the
colony's debug session audit log (same table as `debug_sessions`, RFD 059).

## Implementation Status

**Core Capability:** ⏳ Not Started

## Future Work

**LLM-facing MCP tools** (follow-up RFD)

Expose `coral_deploy_correlation` and `coral_list_correlations` as MCP tools
so the colony LLM can manage the full lifecycle without human CLI interaction.
Blocked by completing the colony routing in Phase 4 of this RFD.

**Argument-based correlation** (Future RFD)

Allow `SourceSpec.filter_expr` to reference captured function arguments
(e.g., `event.args[0] == "user-123"`). Requires argument capture capability
from RFD 061 future work.

**Cross-agent correlation** (Future RFD)

Detect patterns spanning two agents (e.g., slow upstream call on agent A
correlated with high CPU on agent B). Requires a colony-side correlation
engine that aggregates `TriggerEvent` streams from multiple agents.
