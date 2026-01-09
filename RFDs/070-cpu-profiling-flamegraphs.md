---
rfd: "070"
title: "CPU Profiling and Flame Graphs"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "069" ]
database_migrations: [ ]
areas: [ "agent", "ebpf", "cli", "debugging", "protobuf" ]
---

# RFD 070 - CPU Profiling and Flame Graphs

**Status:** 🎉 Implemented

**Date:** 2025-12-16

## Summary

Add support for sampling CPU profilers to Coral, enabling the generation of
Flame Graphs. This complements the existing uprobe-based function latency
tracing (RFD 069) by providing visibility into CPU-bound workloads and detailed
stack traces via eBPF `perf_event` sampling.

## Problem

- **Current limitations**: RFD 069 introduced "profiling" that is strictly
  **uprobe-based tracing** of function entry/exit. This measures *latency* (how
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

### Phase 1: eBPF Implementation ✅ Completed

- [x] Create `internal/agent/debug/bpf/cpu_profile.bpf.c`.
- [x] Define maps: `stack_counts` (Hash), `stack_traces` (StackTrace).
- [x] Implement `perf_event` handler calling `bpf_get_stackid`.

### Phase 2: Agent Integration ✅ Completed

- [x] Add `StartCPUProfile` and `CollectCPUProfile` to `DebugService`.
- [x] Implement map reading and stack walking logic (convert stack IDs to
  instruction pointer arrays).
- [x] Implement symbolization (IP -> Function Name) using DWARF/ELF
  parsing.
- [x] Create `Symbolizer` component with DWARF debug info support.
- [x] Create Kernel symbolizer.
- [x] Handle both user-space and kernel-space stack traces.
- [x] Implement fallback for missing symbols (display raw addresses with hex
  format).

### Phase 3: API & CLI ✅ Completed

- [x] Define `ProfileCPURequest` / `ProfileCPUResponse` protobuf.
- [x] Add `ProfileCPU` RPC to `DebugService`.
- [x] Implement `coral debug cpu-profile` command.
- [x] Add `--format` flag (folded, json).
- [x] Add error handling for missing services/pods.
- [x] Colony orchestrator integration with service discovery.
- [x] PID resolution via agent query.

### Phase 4: Testing & Validation ✅ Completed

- [x] Add unit tests for symbolization (mock debug client).
- [x] Add integration tests to `debug_integration_test.go`.
- [x] Validate folded stack format output compatibility with `flamegraph.pl`.
- [x] Create E2E test suite in `tests/e2e/distributed/` (scripts and fixtures).
- [x] Create CPU-intensive test application for validation.
- [x] Test with CPU-bound benchmark programs (SHA-256 hashing).
- [x] Test behavior with missing debug symbols (graceful fallback to addresses).
- [x] Verify overhead is acceptable (99Hz sampling, negligible impact).

## API Changes

### New RPC Service

```protobuf
service DebugService {
    // Existing RPCs from RFD 069...

    // Collect CPU profile samples for a target service/pod
    rpc ProfileCPU(ProfileCPURequest) returns (ProfileCPUResponse);
}
```

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
    uint64 total_samples = 2; // Total samples collected
    uint32 lost_samples = 3; // Samples lost due to map overflow
    string error = 4; // Error message if collection failed
}
```

### CLI Commands

```bash
# Capture 30s CPU profile and output folded format (default)
coral debug cpu-profile --service api --duration 30s > profile.folded

# Generate flamegraph (requires flamegraph.pl installed)
coral debug cpu-profile --service api --duration 30s | flamegraph.pl > cpu.svg

# Profile specific pod instance with JSON output
coral debug cpu-profile --service api --pod api-7d8f9c --duration 10s --format json

# Custom sampling frequency (default 99Hz)
coral debug cpu-profile --service api --duration 30s --frequency 49

# Expected output (folded format):
# main;processRequest;parseJSON;unmarshal 127
# main;processRequest;validateData 89
# main;processRequest;saveToDatabase;executeQuery 234
# ...
```

### Error Handling

**Service/Pod Not Found:**
```
Error: Service "api" not found in colony
Available services: frontend, backend, worker
```

**eBPF Attach Failure:**
```
Error: Failed to attach CPU profiler to pod "api-7d8f9c"
Reason: Insufficient permissions (requires CAP_BPF/CAP_PERFMON)
```

**Incomplete Profile:**
```
Warning: Lost 42 samples due to map overflow (increase map size)
Total samples collected: 2958/3000
```

## Security Considerations

- **Privileges**:
  - Requires `CAP_BPF` / `CAP_PERFMON` (already required for
    existing agent features).
  - Requires `CAP_SYSLOG` for Kernel symbolization.
- **Overhead**: Sampling frequency should be capped (e.g., max 1000Hz) to
  prevent DoS.
- **Stack Information Exposure**: CPU profiles may expose sensitive function
  names and call paths. Ensure proper RBAC controls at Colony level.
- **Resource Limits**: Profile duration should be capped (e.g., max 300
  seconds) to prevent excessive map memory usage.

## Implementation Status

**Core Capability:** ✅ Fully Implemented

All four phases have been completed and tested. CPU profiling is production-ready.

### Completed Deliverables

**Phase 1-2: eBPF and Agent**
- ✅ BPF program for perf_event sampling
- ✅ Stack trace collection and aggregation
- ✅ Symbol resolution with DWARF/ELF parsing
- ✅ Bug fixes for BPF map types and perf event configuration

**Phase 3: API and CLI**
- ✅ Complete RPC implementation
- ✅ CLI command with folded/JSON output
- ✅ Colony orchestration and service discovery

**Phase 4: Testing**
- ✅ Unit tests for symbolization
- ✅ Integration tests with mock agents
- ✅ E2E test suite with docker-compose
- ✅ CPU-intensive test application
- ✅ Comprehensive documentation

### Key Implementation Notes

1. **Symbolization:** Implemented from scratch using Go's `debug/dwarf` and
   `debug/elf` packages. Parses debug info on-demand and caches symbol lookups
   for performance.

2. **Testing:** Created comprehensive test suite including E2E tests with
   CPU-intensive workload that actually generates samples (unlike nginx which is
   too efficient).

3**Documentation:** Added detailed guides for symbolization, troubleshooting,
   and flame graph generation.

### Known Limitations

1. **Inline Functions:** Not yet resolved (shows outermost function only).
2. **Build ID Matching:** Not implemented (offline symbolization not supported).

These limitations are acceptable for v1 and can be addressed in future iterations.

## Future Work

The following features are out of scope for this RFD and may be addressed in
future enhancements:

**Integrated Flame Graphs** (Future RFD)
- Generate SVG/HTML flame graphs directly in CLI without external tools.
- Embed interactive flame graphs in Colony UI.
- Enables easier adoption without requiring `flamegraph.pl` installation.

**Differential Flame Graphs** (Medium Priority)
- Compare two profiles (e.g., normal vs slow) to highlight regressions.
- Visual diff showing which code paths increased/decreased CPU usage.
- Useful for performance regression analysis.

**Off-CPU Profiling** (Future RFD - Requires Scheduler Tracing)
- Track time spent waiting on I/O, locks, or sleep.
- Use scheduler tracepoints (`sched_switch`, `sched_wakeup`).
- Complements CPU profiling for complete latency analysis.

**Memory Profiling** (Future RFD - Requires Allocation Tracking)
- Use uprobes on `malloc`/`free` or Go runtime allocator.
- Generate allocation flame graphs.
- Identify memory leaks and excessive allocations.

**Continuous Profiling** (Future RFD - Requires Storage Architecture)
- Low-overhead background profiling (1Hz or less).
- Historical profile storage in Colony/DuckDB.
- Time-series view of CPU usage patterns.
- Blocked by: Storage retention policies and query APIs.
