# eBPF Instrumentation Engine

## Architecture (`internal/agent/ebpf`)

The eBPF engine is managed by a central `Manager` that decouples the lifecycle
of collectors from the main Agent process.

### Kernel-Side Logic (`bpf/uprobe.c`)

The core observability logic resides in the eBPF programs loaded into the
kernel.

- **Data Structures**: Uses a `HASH` map (`entry_times`) to store function entry
  timestamps indexed by `TID` (Thread ID), allowing precise duration calculation
  on function return.
- **Efficient Streaming**: Employs `BPF_MAP_TYPE_RINGBUF` for streaming events
  to userspace. Ring buffers provide better performance and memory efficiency
  compared to older perf buffers by allowing zero-copy reads and shared memory
  between kernel and userspace.
- **Contention-Free Counters**: Uses `PERCPU_ARRAY` maps for sampling counters.
  This pattern is critical in distributed systems to avoid lock contention and
  cache-line bouncing (atomic operations) on high-frequency code paths across
  multiple CPU cores.

### Collector Types

- **Uprobe Collector**: Attaches probes to user-space functions. It captures
  `timestamp_ns`, `PID`, `TID`, and `duration_ns`.
    - _Note on Go compatibility_: Uretprobes (return probes) are currently
      disabled for Go binaries due to stack management incompatibilities;
      duration is captured via entry probes at multiple return points if
      necessary.
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

## Future Improvement: JIT Filtering

Currently, basic filtering is done via eBPF maps. Moving towards more complex
kernel-side predicates (using eBPF instructions or specialized maps) would
further reduce the overhead of high-frequency probe points.

## Related Design Documents (RFDs)

- [**RFD 013**: eBPF Introspection](../../RFDs/013-ebpf-introspection.md)
- [**RFD 032
  **: BEYLA RED Metrics Integration](../../RFDs/032-beyla-red-metrics-integration.md)
- [**RFD 036
  **: BEYLA Distributed Tracing](../../RFDs/036-beyla-distributed-tracing.md)
- [**RFD 061**: eBPF Uprobe Mechanism](../../RFDs/061-ebpf-uprobe-mechanism.md)
- [**RFD 063
  **: Intelligent Function Discovery](../../RFDs/063-intelligent-function-discovery.md)
- [**RFD 090
  **: eBPF Probe Runtime Filtering](../../RFDs/090-ebpf-probe-runtime-filtering.md)
