---
rfd: "090"
title: "eBPF Probe Runtime Filtering"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "059", "061" ]
database_migrations: [ ]
areas: [ "agent", "ebpf", "debugging" ]
---

# RFD 090 - eBPF Probe Runtime Filtering

**Status:** 🚧 Draft

## Summary

Add runtime-configurable filtering to eBPF uprobe programs via BPF maps,
eliminating unnecessary kernel→userspace event copies for events that do not
match caller-defined criteria. The colony sends filter parameters alongside
probe attachment requests and may update them mid-session without detaching
the probe.

## Problem

**Current behavior/limitations:**

The uprobe eBPF program (`internal/agent/ebpf/bpf/uprobe.c`) emits every
function call event unconditionally to the ring buffer. Two consequences follow:

1. **Volume**: On hot code paths (e.g., database query functions), call rates
   of 10,000+/sec saturate the 256 KB ring buffer, causing silent event drops
   before userspace can consume them.
2. **Signal-to-noise**: The colony receives and stores every call when the
   debugging intent is typically "show me the slow calls" or "sample 1 in 100."
   Filtering post-hoc at the colony wastes bandwidth and storage.

Additionally, the current `uprobe_return` handler contains a latent bug:
`duration_ns` is always emitted as zero because the subtraction
`ts - *entry_ts` is never performed after the entry timestamp is looked up.
This makes duration-based filtering impossible until the bug is fixed.

**Why this matters:**

The highest-value probe targets (hot DB/HTTP/gRPC functions) are also the
highest-volume ones. Without kernel-level filtering, attaching a probe to
`db.Query` in a high-throughput service produces unusable noise. Operators
must either avoid probing hot paths or accept dropped events, undermining the
live debugging capability of RFD 059.

**Use cases affected:**

- Attach probe to `db.Query`, emit only calls slower than 50ms.
- Attach probe to `http.HandleRequest`, sample 1 in 100 calls during steady
  state.
- Live-narrow a probe filter mid-session as the incident picture becomes
  clearer, without detaching and re-attaching.

## Solution

Introduce a `filter_config` BPF array map in the uprobe program. The Go
collector writes filter parameters into this map at attach time. The eBPF
handler reads the config on every event and discards events that do not match
before calling `bpf_ringbuf_submit`, keeping filtered events fully in kernel
space with no copy overhead.

**Key Design Decisions:**

- **BPF map over recompilation**: The filter struct lives in a BPF ARRAY map
  (single entry, key 0). Userspace updates it with a standard map write at any
  time. The running eBPF program picks up the new values on the next event.
  No reload, no reattach, no downtime.
- **Per-CPU counter for sampling**: Sample rate (1-in-N) uses a
  `BPF_MAP_TYPE_PERCPU_ARRAY` counter to avoid lock contention on high-rate
  paths. Each CPU independently counts and emits every Nth event.
- **Zero is passthrough**: All filter fields default to zero, which is defined
  as "no filter applied." An agent that does not set a filter behaves exactly
  as today, preserving backward compatibility.
- **Duration filter applied at return**: `min_duration_ns` and
  `max_duration_ns` are evaluated only in `uprobe_return`, where duration is
  known. Entry events are always emitted when duration filtering is active
  (required to preserve the entry→return pairing that callers may depend on).
  If callers only care about return events, they already set `CaptureArgs =
  false`.

**Benefits:**

- Events that do not match are dropped before crossing the kernel→userspace
  boundary: zero ring buffer pressure, zero CPU cost in userspace.
- Colony can express intent ("show me slow calls") at attach time rather than
  filtering after the fact.
- Live filter updates allow progressive narrowing during an active incident
  without interrupting the probe session.
- Fixes the duration bug, making `duration_ns` accurate for the first time.

**Architecture Overview:**

```
Colony (AttachUprobeRequest + UprobeFilter)
    │
    │ gRPC
    ▼
Agent: UprobeCollector.Start()
    │ writes filter_config via objs.FilterConfig.Put(0, cfg)
    ▼
BPF ARRAY map: filter_config (1 entry)
    │ read on each event in kernel context
    ▼
uprobe_return handler
    │ duration < min_duration_ns → return 0 (drop, no ring buffer copy)
    │ sample_rate > 1 && counter % N != 0 → return 0 (drop)
    ▼
bpf_ringbuf_submit  (only matching events reach userspace)
    │
    ▼
UprobeCollector.readEvents() → colony
```

### Component Changes

1. **Agent eBPF program** (`internal/agent/ebpf/bpf/uprobe.c`):
    - Fix `uprobe_return` duration calculation (`duration = ts - *entry_ts`).
    - Add `filter_config` BPF ARRAY map (single entry, `struct filter_config`).
    - Add `sample_counter` per-CPU ARRAY map for rate sampling.
    - Apply duration and sample-rate filters in `uprobe_return` before submit.

2. **Agent uprobe collector** (`internal/agent/ebpf/uprobe_collector.go`):
    - Accept `UprobeFilter` from `UprobeConfig`.
    - Write filter config into `objs.FilterConfig` map after loading eBPF
      objects.
    - Expose `UpdateFilter(UprobeFilter)` to allow live updates.

3. **Agent debug service** (`internal/agent/debug_service.go`):
    - Handle new `UpdateProbeFilter` RPC: look up active collector by session
      ID, call `UpdateFilter`.

4. **Colony debug orchestrator**:
    - Forward `UprobeFilter` from `AttachUprobeRequest` to the agent.
    - Expose `UpdateProbeFilter` RPC that routes to the correct agent.

5. **Protobuf** (`proto/coral/`):
    - Add `UprobeFilter` message to agent proto.
    - Embed `UprobeFilter` in `AttachUprobeRequest`.
    - Add `UpdateProbeFilter` RPC to agent `DebugService`.

## API Changes

### New Protobuf Messages

```protobuf
// proto/coral/agent/v1/agent.proto

// UprobeFilter defines runtime filter criteria applied at the eBPF level.
// All fields default to zero, meaning no filter is applied for that dimension.
message UprobeFilter {
    // min_duration_ns drops return events shorter than this threshold.
    // 0 = no minimum.
    uint64 min_duration_ns = 1;

    // max_duration_ns drops return events longer than this threshold.
    // 0 = no maximum.
    uint64 max_duration_ns = 2;

    // sample_rate emits 1 in every N events.
    // 0 or 1 = emit all events.
    uint32 sample_rate = 3;
}

message UpdateProbeFilterRequest {
    string session_id = 1;
    UprobeFilter filter = 2;
}

message UpdateProbeFilterResponse {}
```

```protobuf
// proto/coral/colony/v1/debug.proto

message UpdateProbeFilterRequest {
    string session_id = 1;
    string agent_id   = 2;
    UprobeFilter filter = 3;
}

message UpdateProbeFilterResponse {}
```

### Modified Protobuf Messages

```protobuf
// proto/coral/colony/v1/debug.proto

// Extend AttachUprobeRequest with optional filter.
message AttachUprobeRequest {
    string service_name = 1;
    string function_name = 2;
    google.protobuf.Duration duration = 3;
    UprobeConfig config = 4;
    UprobeFilter filter = 5;  // NEW: optional kernel-level filter
}
```

### New RPC Endpoints

```protobuf
// proto/coral/agent/v1/agent.proto

service DebugService {
    // ... existing RPCs ...

    // UpdateProbeFilter updates filter parameters for an active probe session
    // without detaching or interrupting event collection.
    rpc UpdateProbeFilter(UpdateProbeFilterRequest) returns (UpdateProbeFilterResponse);
}
```

```protobuf
// proto/coral/colony/v1/debug.proto

service DebugService {
    // ... existing RPCs ...

    // UpdateProbeFilter routes a filter update to the agent hosting the session.
    rpc UpdateProbeFilter(UpdateProbeFilterRequest) returns (UpdateProbeFilterResponse);
}
```

### CLI Commands

```bash
# Attach with a duration filter
coral debug attach payments db.Query --min-duration 50ms

# Attach with sampling
coral debug attach payments http.HandleRequest --sample-rate 100

# Update filter on an active session
coral debug filter <session-id> --min-duration 100ms
coral debug filter <session-id> --sample-rate 10

# Expected output when attaching with filter:
Debug session started
  Session:   sess-abc123
  Service:   payments
  Function:  db.Query
  Filter:    duration >= 50ms
  Duration:  5m

Collecting events...
[14:32:01] db.Query  duration=82ms  pid=1234
[14:32:03] db.Query  duration=115ms pid=1234
```

## Implementation Plan

### Phase 1: eBPF Program Changes

- [ ] Fix duration bug in `uprobe_return` (`duration = ts - *entry_ts`).
- [ ] Add `filter_config` BPF ARRAY map with `struct filter_config` (
  `min_duration_ns`, `max_duration_ns`, `sample_rate`).
- [ ] Add `sample_counter` per-CPU ARRAY map.
- [ ] Apply duration filter in `uprobe_return` before `bpf_ringbuf_submit`.
- [ ] Apply sample rate filter using per-CPU counter.
- [ ] Regenerate BPF Go bindings via `go generate`.

### Phase 2: Agent Collector and Service

- [ ] Extend `UprobeConfig` to carry `UprobeFilter`.
- [ ] Write filter config into BPF map after `loadUprobeObjects` in
  `UprobeCollector.Start`.
- [ ] Add `UpdateFilter(UprobeFilter) error` to `UprobeCollector`.
- [ ] Implement `UpdateProbeFilter` RPC handler in the agent debug service.
- [ ] Validate filter params on receipt (e.g., max > min if both set).

### Phase 3: Colony Routing

- [ ] Extend `AttachUprobeRequest` proto with `UprobeFilter`.
- [ ] Pass filter through colony debug orchestrator to agent attach call.
- [ ] Implement colony `UpdateProbeFilter` RPC, routing to the correct agent.
- [ ] Add `--min-duration`, `--max-duration`, `--sample-rate` flags to
  `coral debug attach`.
- [ ] Add `coral debug filter` CLI command.

### Phase 4: Testing

- [ ] Unit test: duration filter drops events below threshold (mock BPF map).
- [ ] Unit test: sample rate emits correct fraction of events.
- [ ] Unit test: zero filter values pass all events (backward compatibility).
- [ ] Unit test: `UpdateFilter` mid-session changes observed event rate.
- [ ] Integration test: attach with `min_duration=50ms` to a function with
  known latency distribution, verify only slow calls appear.
- [ ] Integration test: `UpdateProbeFilter` RPC changes filter without
  interrupting session.

## Security Considerations

Filter parameters are written into the BPF map by the agent process running
with the required `CAP_BPF` capability. The BPF verifier enforces that the
eBPF program only reads from the map and cannot modify agent process memory.
Filter updates follow the same RBAC path as session creation (RFD 058): only
principals authorised to manage debug sessions may call `UpdateProbeFilter`.

## Implementation Status

**Core Capability:** ⏳ Not Started

## Future Work

**Argument-based filtering** (Future RFD)

Filter based on function argument values (e.g., only calls where the first
argument equals a specific user ID). Requires reading function arguments in
the eBPF handler, which depends on calling-convention-aware argument capture
(blocked by Go uretprobe limitations noted in RFD 061).

**PID-scoped filtering** (Future)

Add `pid_filter` to `UprobeFilter` to restrict events to a specific process
when multiple processes share the same binary. Currently handled by the
`PIDFilter` field in `AttachConfig` but not dynamically updatable.
