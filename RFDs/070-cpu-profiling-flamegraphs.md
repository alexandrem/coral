---
rfd: "070"
title: "CPU Profiling and Flame Graphs"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "069" ]
database_migrations: [ ]
areas: [ "agent", "ebpf", "cli", "debugging" ]
---

# RFD 070 - CPU Profiling and Flame Graphs

**Status:** 🚧 Draft

## Summary

Add support for sampling CPU profilers to Coral, enabling the generation of
Flame Graphs. This complements the existing uprobe-based function latency
tracing (RFD 069) by providing visibility into CPU-bound workloads and detailed
stack traces via eBPF `perf_event` sampling.

## Problem

- **Current limitations**: RFD 069 introduced "profiling" that is strictly *
  *uprobe-based tracing** of function entry/exit. This measures *latency* (how
  long a function takes) but cannot explain *why* it takes that long if the time
  is spent on CPU (tight loops, expensive calculations). It also doesn't provide
  stack traces for what is running on the CPU between those entry/exit points.
- **Why this matters**: Users debugging high CPU usage scenarios have no
  visibility. They can see *which* function is slow, but not *what* it is doing
  inside.
- **Use cases affected**: Debugging CPU spikes, analyzing algorithm performance,
  optimizing compute-intensive services.

## Solution

Implement a Sampling CPU Profiler using eBPF attached to
`PERF_COUNT_SW_CPU_CLOCK`.

**Key Design Decisions:**

- **eBPF Sampling**: Use standard eBPF pattern for sampling stack traces (99Hz).
  This is low overhead and production safe.
- **Stack Map**: Use `BPF_MAP_TYPE_STACK_TRACE` to store stack IDs and a
  `BPF_MAP_TYPE_HASH` to count frequencies of
  `(pid, user_stack_id, kernel_stack_id)`.
- **Symbolization**: Perform symbolization in userspace (Agent) after profile
  collection, using the existing DWARF/SymbolTable cache from RFD 063/069.
- **Output Format**: Support "Collapsed Stack" format (standard input for
  FlameGraph scripts) and JSON.

**Benefits:**

- **Flame Graphs**: Enables standard industry visualizations.
- **Low Overhead**: Sampling at 99Hz is negligible for most systems.
- **Deep Visibility**: Shows exactly what code paths are consuming CPU cycles.

**Architecture Overview:**

```
CLI → Colony → Agent → eBPF (Profile)
                         ↓
                      Stack Map (Counts)
                         ↓
Agent (Read & Symbolize) → Colony → CLI (FlameGraph)
```

### Component Changes

1. **Agent (eBPF)**:
    - New BPF program `cpu_profile.bpf.c` attached to perf events.
    - New map definitions for stack storage and counts.

2. **Agent (Go)**:
    - New `ProfileCPU` RPC handler in `DebugService`.
    - Logic to load BPF, wait duration, read maps, and symbolize stacks.

3. **CLI**:
    - New command `coral debug cpu-profile` (or similar).
    - Output options for raw folded stacks (for piping to `flamegraph.pl`) or
      ASCII visualization.

## Implementation Plan

### Phase 1: eBPF Implementation

- [ ] Create `internal/agent/debug/bpf/cpu_profile.bpf.c`.
- [ ] Define maps: `stack_counts` (Hash), `stack_traces` (StackTrace).
- [ ] Implement `perf_event` handler calling `bpf_get_stackid`.

### Phase 2: Agent Integration

- [ ] Add `StartCPUProfile` and `StopCPUProfile` (or `CollectCPUProfile` with
  duration) to `DebugService`.
- [ ] Implement map reading and stack walking logic.
- [ ] Integrate symbolization (IP -> Function Name) using existing
  `FunctionCache`.

### Phase 3: API & CLI

- [ ] Define `ProfileCPURequest` / `ProfileCPUResponse` protobuf.
- [ ] Implement `coral debug cpu-profile` command.
- [ ] Add `--format flag` (folded, json).

## API Changes

### New Protobuf Messages

```protobuf
message ProfileCPURequest {
    string service_name = 1;
    string pod_name = 2; // Optional, specific instance
    int32 duration_seconds = 3;
    int32 frequency_hz = 4; // Default 99
}

message StackSample {
    repeated string frame_names = 1;
    uint64 count = 2;
}

message ProfileCPUResponse {
    repeated StackSample samples = 1;
}
```

### CLI Commands

```bash
# Capture 30s CPU profile and output folded format
coral debug cpu-profile --service api --duration 30s > profile.folded

# Generate flamegraph (requires external tool installed or piped)
coral debug cpu-profile -s api -d 30s | flamegraph.pl > cpu.svg
```

## Security Considerations

- **Privileges**: Requires `CAP_BPF` / `CAP_PERFMON` (already required for
  existing agent features).
- **Overhead**: Sampling frequency should be capped (e.g., max 1000Hz) to
  prevent DoS.

## Future Work

- **Integrated Flame Graphs**: Generate SVG/HTML flame graphs directly in the
  CLI/UI without external tools.
- **Differential Flame Graphs**: Compare two profiles (e.g., normal vs slow) to
  highlight regressions.
- **Wall-clock Profiling**: Support off-cpu profiling (waiting on I/O, locks)
  using scheduler tracepoints.
- **Allocations Profiling**: Use uprobes on malloc/go runtime for memory
  profiling.
- **Continuous Profiling**: Low-overhead background profiling with historical
  storage.
