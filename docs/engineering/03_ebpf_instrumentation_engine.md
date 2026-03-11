# eBPF Instrumentation Engine

## Architecture (`internal/agent/ebpf`)

The eBPF engine is managed by a central `Manager` that decouples the lifecycle
of collectors from the main Agent process.

### Kernel-Side Logic (`bpf/uprobe.c`)

The core observability logic resides in the eBPF programs loaded into the
kernel.

- **Data Structures**: Uses a `HASH` map (`entry_times`) to store function entry
  timestamps indexed by a composite key of `TGID` (Thread Group ID) and `SP` (
  Stack Pointer).
  `TGID` ensures tracking across goroutine migrations between OS threads, while
  the `SP`
  provides recursion safety by uniquely identifying the specific call frame.
- **Efficient Streaming**: Employs `BPF_MAP_TYPE_RINGBUF` for streaming events
  to userspace. Ring buffers provide better performance and memory efficiency
  compared to older perf buffers by allowing zero-copy reads and shared memory
  between kernel and userspace.
- **Contention-Free Counters**: Uses `PERCPU_ARRAY` maps for sampling counters.
  This pattern is critical in distributed systems to avoid lock contention and
  cache-line bouncing (atomic operations) on high-frequency code paths across
  multiple CPU cores.

### Collector Types

- **Uprobe Collector**: Attaches probes to user-space functions to capture
  `timestamp_ns`, `PID`, `TID`, and `duration_ns`.
    - **Return-Instruction Uprobes (RFD 073)**: Traditional `uretprobes` are
      incompatible with
      Go's stack management (split stacks can cause "unexpected return pc"
      crashes).
      To solve this, Coral uses a **disassembly-based technique**:
        1. **SDK Interrogation**: Retrieves function offset and size from the
           SDK (derived from `DW_AT_high_pc` / `DW_AT_low_pc`).
        2. **Binary Disassembly**: The agent reads the target binary from
           `/proc/{pid}/exe` and uses `x86asm` or `arm64asm` (via
           `internal/agent/ebpf/disasm`) to find all possible `RET`
           instructions.
        3. **Multi-Point Attachment**: For a single function, the agent attaches
           an entry uprobe AND N return uprobes (one per `RET` instruction
           found).
        4. **BPF Map Coordination**: On entry, a timestamp is stored in BPF. On
           any `RET` hit, the BPF program calculates the delta and emits a
           duration event.
    - **Orphaned Entry Cleanup**: Since Go panics can unwind the stack without
      hitting a `RET`, a background janitor (every 30s) sweeps the BPF map for
      entries older than 60s to prevent memory leaks.
- **Syscall Stats Collector**: Monitors system-wide or process-specific syscall
  frequency and latency.
- **Beyla Integration**: Leverages Beyla's auto-instrumentation for
  protocol-specific (HTTP/gRPC/SQL) RED metrics.

## Capacity Detection

The system performs runtime capability detection (`detect.go`) checking for *
*BTF (BPF Type Format)** and **CO-RE (Compile Once - Run Everywhere)** support.
This ensures the agent can adapt its eBPF programs to the specific kernel
version without requiring local headers or recompilation.

## Runtime Reconfiguration (RFD 090)

A unique feature is the ability to update kernel-level filters without detaching
probes.

- **`UpdateFilter`**: Modifies the `filter_config_map` (array map) which the BPF
  program reads on every event.
- **Kernel-Side Predicates**: The BPF program applies `min_duration_ns` and
  `sample_rate` logic _before_ reserving space in the ring buffer. This
  drastically reduces the overhead for high-volume functions by dropping
  unwanted data in the kernel.

## Lifecycle Management

- **Janitor**: Automatically cleans up expired collectors after a grace period.
- **Auto-Stop**: Collectors can be started with a duration, after which they
  stop capturing but keep events available in memory for a final pull.

## Stateful Probe Correlation

The agent implements a high-performance **Correlation Engine**
(`internal/agent/correlation`) that evaluates temporal event patterns directly
on
the node. This replaces the need for post-processing raw streams on the colony
and enables millisecond-latency action dispatch.

### Evaluation Pattern

- **Declarative DSL**: The `Engine` evaluates `CorrelationDescriptor` protobufs
  rather than arbitrary scripts, ensuring predictability.
- **CEL Predicates**: Per-event filter predicates use **Common Expression
  Language (CEL)**
  via `google/cel-go`, providing bounded execution guarantees within the hot
  eBPF event path.
- **Go Strategy Engine**: Patterns are evaluated by optimized `Evaluator`
  state machines:
    - `rate_gate`: N events matching a filter within window T.
    - `causal_pair`: Event A followed by Event B (joined on `join_on` field like
      `trace_id`).
    - `absence`: Lack of event A for duration T.
    - `percentile_alarm`: Rolling percentile (P99) exceeds a threshold.
    - `edge_trigger`: First transition from fast to slow.
    - `sequence`: Strictly ordered event sequence (A then B).

### Edge Action Dispatch

When a pattern is matched, the `Engine` fires immediate local actions:

- **`EMIT_EVENT`**: Sends a structured `TriggerEvent` notification to the
  colony.
- **`GOROUTINE_SNAPSHOT`**: Triggers a stack capture via the
  `debug.SessionManager`.
- **`CPU_PROFILE`**: Dynamically starts a short profiling session via the
  `debug.CPUProfiler`.

## Future Engineering Note

### JIT Filtering

Currently, basic filtering uses eBPF maps. Moving towards more complex
kernel-side predicates (using eBPF instructions or specialized bytecode) would
further reduce the overhead of high-frequency probe points.

### Action Dispatch Loopback

The initial `GOROUTINE_SNAPSHOT` and `CPU_PROFILE` dispatch is implemented via
direct function calls in the binary. Future iterations should use a local RPC
loopback to the `DebugService` to ensure unified authorization (RFD 058)
applies even to autonomous agent actions.

### Skill SDK Integration

The `CorrelationEngine` provides the low-level primitives that higher-level
**Skills** (TypeScript scripts) use to orchestrate investigations. Future work
will expose a `coral.correlation` namespace in the SDK, allowing Skills to
programmatically deploy descriptors, wait for `TriggerEvent` notifications, and
automate the "trap-and-analyze" loop.

### Disassembly Caching

As Return-Instruction Uprobes require disassembling the function on every
session
start, implementing a local cache of `RET` offsets (keyed by binary `mtime` and
symbol offset) would optimize startup time for high-frequency debugging
sessions.

## Related Design Documents (RFDs)

- [**RFD 013**: eBPF Introspection](../../RFDs/013-ebpf-introspection.md)
- [**RFD 032
  **: BEYLA RED Metrics Integration](../../RFDs/032-beyla-red-metrics-integration.md)
- [**RFD 036
  **: BEYLA Distributed Tracing](../../RFDs/036-beyla-distributed-tracing.md)
- [**RFD 061**: eBPF Uprobe Mechanism](../../RFDs/061-ebpf-uprobe-mechanism.md)
- [**RFD 063
  **: Intelligent Function Discovery](../../RFDs/063-intelligent-function-discovery.md)
- [**RFD 073
  **: Return-Instruction Uprobes for Go](../../RFDs/073-return-instruction-uprobes.md)
- [**RFD 090
  **: eBPF Probe Runtime Filtering](../../RFDs/090-ebpf-probe-runtime-filtering.md)
- [**RFD 091**: Probe Correlation DSL](../../RFDs/091-probe-correlation-dsl.md)
