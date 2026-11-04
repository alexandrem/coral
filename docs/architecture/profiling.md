# Profiling and Trace Correlation

Profiles attribute sampled CPU execution or heap allocation to code locations.
Coral collects them at the agent, stores recent samples locally, and polls them
into Colony summaries. A profile measures a process over a time interval;
correlating it with a trace does not automatically isolate the goroutine that
served a particular request.

## CPU collection

The on-demand CPU profiler attaches an eBPF program to
`PERF_COUNT_SW_CPU_CLOCK` events and captures user and kernel stack references.
Its default sampling frequency is 99 Hz. Userspace resolves instruction
addresses to frame names using binary and kernel symbolization support. Useful
attribution depends on permissions, platform support, and available symbols;
an unresolved address is not evidence that the corresponding code was idle.

Continuous CPU profiling uses a persistent BPF session for a watched process.
The defaults are 19 Hz sampling and a 15-second collection interval. The kernel
continues sampling between collection ticks; the agent periodically drains
stack counts, encodes frames, and stores the resulting samples. This avoids
repeatedly attaching for every interval, but finite maps and failed collection
or storage still limit coverage. A configured low frequency is not a measured
CPU-overhead guarantee for every workload.

Watched-service process callbacks add eligible services to continuous profiling.
An automatically observed port without a monitor does not acquire a profiler
solely because Beyla emitted traffic for it. Service removal and process
lifecycle changes must also update the profiling lifecycle.

Implementation: [CPU profiler](../../internal/agent/debug/cpu_profiler.go),
[continuous CPU collection](../../internal/agent/profiler/continuous_cpu.go), and
[agent lifecycle](../../internal/agent/agent.go).
[Service discovery](service-discovery.md) defines watched services;
[function indexing](function-indexing.md) explains binary metadata.

## Memory collection

The memory profiler fetches heap information from a compatible runtime/SDK
endpoint and parses profile samples. It reports allocation bytes and objects,
stack frames, and available runtime statistics. Continuous memory profiling
periodically collects this information and stores samples locally for later
polling. It requires the corresponding runtime capability; CPU eBPF support
does not by itself provide heap profiles for every language.

The implementation also groups allocation sites into type categories using
leaf function names. These are heuristic categories, not a complete runtime
object-type census. Comparing snapshots can support an investigation into heap
growth, but a rising allocation metric alone does not prove a leak or identify
its cause. The SDK's reusable investigation skills interpret this evidence;
the collector does not establish an autonomous diagnosis contract.

Implementation: [memory profiler](../../internal/agent/debug/memory_profiler.go),
[continuous memory collection](../../internal/agent/profiler/continuous_memory.go),
and [profile parsing tests](../../internal/agent/debug/memory_profiler_test.go).

## Frame dictionaries and central summaries

Repeated stacks contain many repeated function names. The agent dictionary
assigns integer frame IDs, and local samples store stacks as arrays of those
IDs. The stack representation includes a hash for grouping, together with
service, build, process, time, and sample-count metadata as applicable.

Dictionary IDs are local to their database. The Colony poller receives frame
names and encodes them using the Colony's dictionary before storing summaries;
it must not interpret an agent's integer frame ID as a Colony frame ID. CPU
aggregation groups by time bucket, build, TGID, and stack, preserving process
identity needed by trace correlation.

Agent sample retention and Colony summary retention are separate policies.
Cleanup removes expired samples and can remove orphaned dictionary entries.
The representation reduces repeated strings, but storage volume still depends
on stack cardinality, fleet size, and retention; there is no fixed compression
ratio or fleet capacity implied by dictionary encoding.

Implementation: [local profile storage](../../internal/agent/profiler/storage.go),
[CPU poller](../../internal/colony/cpu_profile_poller.go), and
[memory poller](../../internal/colony/memory_profile_poller.go).
[Telemetry polling](telemetry-polling.md) covers the separate CPU and memory
stream checkpoints and session changes.

## Correlating a trace with CPU samples

The trace/profile query joins centrally stored Beyla spans to CPU profile
summaries. A span contributes its service name, `process_pid`, start time, and
duration. Profile samples contribute their service name, `tgid`, timestamp,
and stack counts. Rows can match when the service and process ID agree and the
profile timestamp falls within the span interval padded by one minute on each
side. Spans without a positive process PID cannot participate in this join.

For example, a slow request in `api` can be compared with stacks sampled for
that process during the surrounding interval. If many other requests run in
the same process, their stacks also contribute. The padding and minute buckets
further broaden attribution. Even a dominant stack is evidence about process
activity around the request, not proof that this request executed it.

The query does not join on a goroutine ID. Service names and PIDs are also not
globally unique process identities across hosts and time; the current join
must be interpreted with that scope in mind. Low sample coverage produces a
coverage warning, and no profile samples can mean missing collection rather
than absence of CPU work.

`QueryTraceProfile` and `coral query trace-profile <trace-id>` expose this
correlation. Read the query implementation for the precise supported profile
type and output rather than assuming the CPU join also supplies request-scoped
heap attribution.

Implementation: [trace/profile SQL](../../internal/colony/database/trace_profile.go)
and [CLI trace profile](../../internal/cli/query/trace_profile.go).
Usage is documented in [Live debugging](../LIVE_DEBUGGING.md).

## Design history

- [RFD 070: CPU Profiling](../../RFDs/070-cpu-profiling-flamegraphs.md)
- [RFD 072: Continuous CPU Profiling](../../RFDs/072-continuous-cpu-profiling.md)
- [RFD 077: Memory Profiling](../../RFDs/077-memory-profiling.md)
- [RFD 078: Trace-Driven Profiling](../../RFDs/078-trace-driven-profiling.md)
- [RFD 080: Advanced Trace Analysis](../../RFDs/080-advanced-trace-analysis.md)
  records further proposals; it is not the contract of the current join.
