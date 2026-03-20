# Continuous Profiling and Dynamic Introspection

Coral leverages eBPF and runtime-native hooks to provide "Always-On"
observability into the execution and resource consumption of distributed
services. This goes beyond traditional metrics by providing code-level
attribution for CPU cycles and memory allocations.

## CPU Profiling (`internal/agent/debug/cpu_profiler.go`)

Coral implements a sampling CPU profiler that identifies where the processor is
spending time by taking periodic snapshots of the instruction pointer across all
active threads.

### 1. On-Demand High-Frequency Profiling (99Hz)

When an operator or LLM triggers an investigation (e.g., `coral profile cpu`),
the agent deploys a high-fidelity eBPF program:

- **Mechanism**: The program is attached to `PERF_COUNT_SW_CPU_CLOCK` events.
- **Stack Collection**: It uses `BPF_MAP_TYPE_STACK_TRACE` to capture both
  user-space and kernel-space stacks.
- **Fidelity**: At **99Hz**, the profiler provides enough resolution to identify
  tight loops and expensive algorithmic calls within seconds.
- **Symbolization**: The agent resolves the raw instruction pointers to function
  names using the symbol cache populated in [Chapter 04](04_binary_function_indexing_and_metadata.md).

### 2. Continuous Background Profiling (19Hz)

To enable retroactive analysis, Coral maintains a **low-overhead continuous
profile** that runs in the background of every enrolled service.

- **Sparse Sampling**: By sampling at **19Hz** (a prime number to avoid harmonic
  aliasing with system interrupts), the overhead remains **< 1% CPU**.
- **Tiered Aggregation**:
  - **Agent Boundary**: Samples are aggregated into 15-second intervals and
    stored in the agent's local DuckDB (1-hour retention).
  - **Colony Boundary**: The Colony polls these profiles and merges them into
    1-minute summaries for historical trend analysis (30-day retention).
- **Flame Graph Generation**: Users can query any time window (e.g., "Show me the
  CPU profile for service X during the spike at 2:00 PM") and generate a
  standard Flame Graph instantly.

## Memory Profiling and Allocation Tracking

While CPU profiling shows what is _running_, memory profiling explains why the
system is _growing_ or why the Garbage Collector (GC) is consuming CPU.

### 1. Heap Analysis

The agent periodically interrogates the Go SDK or runtime to capture the **Heap
Profile**:

- **Allocation Hotspots**: Identifies which functions are allocating the most
  total bytes (Inuse vs. Allocated).
- **Type Attribution**: Breaks down memory consumption by Go type (e.g.,
  `[]byte` vs. `struct`), which is critical for identifying "death by a thousand
  small allocations."
- **GC Pressure Correlation**: By correlating memory allocation rates with
  `runtime.mallocgc` time in the CPU profile, the system can autonomously
  diagnose if a service is "GC Bound."

### 2. Leak Detection

By comparing heap snapshots over time, the **Reasoning Engine** (Chapter 10) can
detect monotonically increasing memory usage. If the heap grows significantly
without a corresponding increase in request throughput, the system flags a
potential memory leak and identifies the exact stack trace responsible for the
growth.

## Data Efficiency: Frame Dictionary Compression

Profiling data is notoriously verbose (repeating long string function names for
every sample). Coral uses a **Frame Dictionary** to compress this data by **>
80%**:

1. **Dictionary Encoding**: Every unique function name is assigned a 4-byte
   Integer ID.
2. **Integer Stacks**: Profiles are stored as arrays of these IDs (e.g., `[1, 5, 203]`)
   rather than strings.
3. **Columnar Compression**: DuckDB applies additional RLE (Run-Length
   Encoding) and Bit-Packing to these arrays, making 30 days of continuous
   profiling for an entire fleet fit within a few gigabytes.

## Trace-Driven Profiling

Coral correlates distributed trace spans with CPU and memory profile samples to
answer request-level questions like "what code was running during this specific
slow request?" This is implemented via a **query-time join** — no changes to the
eBPF profiler are required.

### How it works (RFD 078)

Beyla forwards finished spans to Coral's OTLP ingestion endpoint, including
`process.pid` — the OS-level PID (TGID) of the instrumented process — as a
standard OTLP resource attribute. The continuous profiler captures `tgid` in
its BPF stack key at sample time. Both values are persisted to storage, enabling
a DuckDB join at query time:

```sql
SELECT p.stack_frame_ids, p.sample_count
FROM cpu_profile_summaries p
JOIN beyla_traces t
  ON p.tgid = t.process_pid
 AND p.timestamp BETWEEN t.start_time
                     AND t.start_time + (t.duration_us * INTERVAL '1 microsecond')
WHERE t.trace_id = '<trace-id>'
  AND t.service_name = 'payment-svc'
```

This is a **process-scoped** correlation, not goroutine-scoped: samples are
attributed to the entire process during the span's time window, not just the
goroutine serving the request. For anomalously slow requests (e.g., 5 s vs.
50 ms median), the target request dominates the sample window and the
bottleneck function rises to the top of the flame graph. For short spans or
high-concurrency services, results are best-effort.

The system surfaces this via a `QueryTraceProfile` RPC at the Colony and a
`coral query trace-profile <trace-id>` CLI command, both yielding per-service
flame graphs for any stored trace.

### Future work: triggered and goroutine-scoped profiling (RFD 080)

The query-time join approach (RFD 078) is the foundation. RFD 080 extends it
with:

- **Tail-latency trigger**: Automatically activate high-frequency (99Hz)
  profiling when a request exceeds a P99 latency threshold. Because the span is
  still active, the profile captures the exact time window of the slow request
  with higher sample density.
- **Error trigger**: Start a high-frequency profile when a service begins
  emitting 5xx errors, capturing the failure state in real time.
- **Goroutine-level precision**: Eliminate process-scoped noise by tagging each
  eBPF sample with the goroutine ID serving the request (via a pinned BPF map
  shared with Beyla, or a Coral SDK injection). This provides exact per-request
  attribution rather than best-effort process-window attribution.
- **Comparative analysis**: Differential flame graphs contrasting slow vs. fast
  request cohorts, with statistical significance testing.

## Related Design Documents (RFDs)

- [**RFD 070**: CPU Profiling and Flame Graphs](../../RFDs/070-cpu-profiling-flamegraphs.md)
- [**RFD 072**: Continuous CPU Profiling](../../RFDs/072-continuous-cpu-profiling.md)
- [**RFD 077**: Memory Profiling and Allocation Flame Graphs](../../RFDs/077-memory-profiling.md)
- [**RFD 078**: Trace-Driven Profiling — Core Infrastructure](../../RFDs/078-trace-driven-profiling.md)
- [**RFD 080**: Advanced Trace Analysis & AI-Driven Diagnosis](../../RFDs/080-advanced-trace-analysis.md) _(planned)_
