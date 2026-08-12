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
  protocol-specific (HTTP/gRPC/SQL) RED metrics and distributed traces.
  - **Bridging**: The Coral agent acts as a controller for the Beyla process,
    managing its configuration, lifecycle, and data ingestion via an embedded
    OTLP receiver.

## Beyla Integration & Bridging Architecture

Beyla is integrated into the Coral ecosystem as a "managed sub-process" rather
than a library. This provides process isolation and allows the agent to treat
Beyla as a high-performance protocol parser while maintaining Coral's
distributed storage and query philosophy.

### Process Management (`internal/agent/beyla/manager.go`)

The `Manager` is responsible for the end-to-end lifecycle of the Beyla binary:

- **Embedded Distribution**: Beyla binaries (x86_64, ARM64) are embedded into the
  Coral Agent binary during build-time using `go generate`. On startup, the
  Agent extracts the appropriate binary to a temporary directory.
- **Dynamic Configuration**: On every discovery change (a new process observed,
  a `coral connect`/`disconnect` call), the `Manager` generates a fresh Beyla
  YAML configuration file and restarts Beyla after a debounce window. This is
  **Runtime Service Tracking (RFD 053)**; see **Pluggable Process Discovery
  (RFD 102)** below for how the candidate set feeding this generation is
  produced.
- **Sub-process Supervision**: Beyla runs as an external process supervised via
  `os/exec`. Terminal output (`stdout`/`stderr`) is wrapped and piped into
  Coral's `zerolog` system for unified debugging.

### Data Bridging & OTLP Loopback

Beyla exports metrics and traces using the **OpenTelemetry Protocol (OTLP)**.
Coral bridges this data into its own engine via a local gRPC/HTTP loopback:

- **Dedicated Receiver**: The Agent starts a dedicated OTLP receiver instance
  listening on `127.0.0.1:4319` (gRPC) and `4320` (HTTP). This is separate from
  the "User OTLP Receiver" (4317/4318) to prevent ingestion crosstalk and enable
  different security policies.
- **OTLP Loopback**: Beyla is configured to export to these loopback addresses.
  This allows Coral to capture "auto-instrumented" data using the same
  high-performance pipeline as manual instrumentation.
- **Transformation Pipeline (`transformer.go`)**: OTEL metric batches and trace
  spans are intercepted and transformed:
  - **Metrics**: Standard OTLP metrics (e.g., `http.server.duration`) are
    converted into `EbpfEvent` protobufs.
  - **Traces**: Distributed trace spans are captured and routed to dedicated
    Beyla trace storage.

### Pluggable Process Discovery (RFD 102)

#### The Problem: One Name for Every Process

Before RFD 102, `--monitor-all` (zero-config mode) emitted a single Beyla
discovery rule: `open_ports: "1-65535"`. Beyla groups every process matching
the *same* rule into one logical service, named after the first binary it
encounters — typically `coral-agent`. Every call between two real services
therefore appeared in trace data as originating from the same name, and the
topology materialization join (`child.service_name != parent.service_name`,
see chapter 15) always evaluated false, so `coral query topology` reported no
edges at all despite traffic flowing. Client-only processes (workers,
consumers — anything that never binds a listening socket) were invisible
entirely, since the socket-table-based fallback had nothing to key on.

#### Architecture (`internal/agent/beyla/discovery`)

A `ProcessDiscoveryProvider` interface abstracts "how do we find and name a
process":

```go
type ProcessDiscoveryProvider interface {
    Name() string
    Probe() bool                                          // is this provider usable here?
    Discover(ctx context.Context) ([]ProcessCandidate, error)
}
```

`ProcessCandidate` carries `PID`, `Ports`, `Name`, `Source`, `Labels`, and
`IsClientOnly`. A `DiscoveryManager` polls every registered provider on an
interval (`discovery_sync_interval`, default 30s), merges their candidates,
detects changes against the previous merge, and — on change — invokes an
`applyDiscovery` callback that feeds the existing RFD 053 debounced-restart
path.

**Merge priority** (highest wins per process):

1. **Static candidates** (`SetStaticCandidates`) — pinned entries from
   `coral connect`/`coral disconnect` and the agent's config-file
   `beyla.discovery.services` map. Re-applied on *every* poll, so they are
   never aged out by a cycle where no provider currently reports the same
   process.
2. **`EnvVarProvider`** — reads `OTEL_SERVICE_NAME` (falling back to
   `SERVICE_NAME`) from `/proc/<pid>/environ`. Respects an operator-set name
   in any environment (bare metal, Docker Compose, Kubernetes) without
   requiring `coral connect`.
3. **`ProcFSProvider`** — the universal fallback. A socket-table scan
   (`/proc/net/tcp[6]`, resolving listening-socket inodes to PIDs via each
   process's `fd` directory) finds every listening server; a full
   `/proc/<pid>/comm` walk finds every *other* running process and reports it
   with `IsClientOnly=true`. This is what makes client-only processes visible
   at all — they were structurally unreachable under the old catch-all.

**Merge mechanics** (`internal/agent/beyla/discovery/manager.go`): candidates
are joined primarily by PID, so an `EnvVarProvider` name hint and a
`ProcFSProvider` port list for the same running process collapse into one
entry. Static candidates registered before the real PID is known (a
`coral connect` call made ahead of the process starting) are additionally
joined by **port**, once a provider reports a real process there — this
prevents the static entry and the provider-discovered entry from producing
two separate, conflicting Beyla rules for the same port.

#### From Candidates to Beyla Rules (`generateBeylaConfig`)

`beyla.Manager.generateBeylaConfig` maps the merged candidate list directly to
Beyla `discovery.services` rules:

- A candidate with `Ports` becomes a named `open_ports` rule (plus an
  `exe_path: .*<name>.*` clause, so outbound calls on other ports are still
  attributed to the same service).
- A candidate with `IsClientOnly=true` becomes a named `exe_path`-only rule —
  there is no port to match on.
- The residual `open_ports: "1-65535"` catch-all is only emitted when
  `MonitorAll` is set **and** no candidate resolved to a named rule (e.g. the
  very first poll cycle, before `ProcFSProvider` has run). Mixing named rules
  with the catch-all causes Beyla to attach eBPF uprobes to the same process
  twice, which silently drops all spans for it — so the two are always
  mutually exclusive.

### Default-On Observation and OTLP Feedback (RFD 103)

#### Zero-Flag Startup

Before RFD 103, `coral agent start` collected nothing unless the operator
passed `--monitor-all` — a flag most users only discovered after filing a
"why is nothing showing up?" issue. `--monitor-all` is now implicit: Beyla
starts unconditionally unless the operator opts out.

- **`internal/cli/agent/startup/storage.go`**: `StorageManager.Initialize`
  reads `agentCfg.Agent.MonitorAll` (config field `agent.monitor_all`, env
  `CORAL_MONITOR_ALL`, default `true`) instead of gating on a CLI-only flag.
- **`--no-monitor-all`**: the supported escape hatch for resource-constrained
  hosts, threaded through `AgentServerBuilder` → `ConfigValidator` →
  `agentCfg.Agent.MonitorAll` (`internal/cli/agent/startup/{start,builder,
  validator}.go`). It forces `MonitorAll` to `false` regardless of what the
  config file/env set — CLI opt-out always wins.
- **`--monitor-all` is now a no-op**: still accepted so existing scripts and
  container images don't break, but `start.go`'s `RunE` checks
  `cmd.Flags().Changed("monitor-all")` and emits a deprecation warning to
  stderr rather than gating anything on the flag's value.
- **`--connect` was removed** from `agent start` (it duplicated default
  observation's job). The standalone `coral connect` command
  (`internal/cli/agent/connect.go`) is unaffected — it remains the way to
  pin an explicit name/health-check for a process ahead of
  `ProcFSProvider`/`EnvVarProvider` naming it.

#### OTLP Feedback Callback

Default-on observation means the agent no longer knows in advance which
ports it will see traffic on — RFD 102's `DiscoveryManager` poll loop still
runs on a timer (`discovery_sync_interval`, default 30s), which is too slow
to give upstream components (service map, RFD 105) *immediate* awareness of
a newly observed process. Rather than add a second poll loop, RFD 103 reuses
data already flowing through the system: every OTLP span Beyla emits.

- **`beyla.Manager.HandleSpan`** (the `SpanHandler` callback that routes
  every incoming span to `beyla_traces_local`) calls
  `notifyServiceObserved` before the DuckDB write. `extractSpanPort` reads
  the span's `server.port` attribute (falling back to the older
  `net.host.port` semconv key) — the same attribute a `server`-kind HTTP
  span from Beyla's OTel exporter always carries.
- **Dedup**: `Manager.observedPorts` (guarded by `Manager.mu`) tracks which
  ports have already fired this session, so a hot endpoint doesn't spam the
  callback on every request — only the *first* span for a given port
  triggers it.
- **Callback**: `Manager.SetServiceObservedHandler` lets `agent.New` wire
  `Agent.onBeylaServiceObserved(port, pid, serviceName)`
  (`internal/agent/agent.go`). This is the write-path into the unified
  service map — see **Unified Service Map and Naming (RFD 104)** below.
- **Why spans and not metrics**: `Transformer.TransformMetrics` only
  threads `service.name` through to the `ebpfpb.EbpfEvent` payloads it
  emits — RFD 103 explicitly avoided adding protobuf fields, so
  metric-only-sampled processes (traces sampled out but metrics still
  aggregated) won't trigger the callback. Not a coverage gap under Coral's
  default protocol/sampling config, since spans and metrics are emitted for
  the same requests.

### Unified Service Map and Naming (RFD 104)

#### The Problem: Anonymous Auto-Discovered Processes

Default-on observation (RFD 103) means the agent sees traffic on ports it
was never told about. Before RFD 104, `Agent.monitors` was a
`map[string]*ServiceMonitor` keyed by name and populated only by explicit
`coral connect` calls — an auto-discovered port had nowhere to live. There
was no agent-level record of "what is listening on port 3000," which left
`onBeylaServiceObserved` with nothing to write into, and left `coral
services` (RFD 107) and topology joins (`service_name`, chapter 15) with no
name to attach to auto-discovered traffic.

#### `ServiceEntry` and the Port-Keyed Map (`internal/agent/service_entry.go`)

`Agent.monitors` is replaced by `Agent.services map[int32]*ServiceEntry`,
keyed by **port** rather than name — the one identifier every observed
process has, whether or not it was ever named. A `ServiceEntry` is the
single record of truth for a port:

```go
type ServiceEntry struct {
    Port                        int32
    AutoName, AuthoritativeName string
    NamingSource                NamingSource // Auto | Authoritative
    Tier                        Tier         // TierObserved (0) | TierWatched (1)
    PID                         int32
    BinaryPath, BinaryHash      string
    Monitor                     *ServiceMonitor // nil until TierWatched
}
```

- **`NamingSource`**: `Auto` (set by the naming adaptor chain from process
  metadata) or `Authoritative` (set explicitly via `ConnectService` —
  `coral connect` / `coral services watch`). Authoritative always wins:
  `ServiceEntry.Name()` returns `AuthoritativeName` if set, else `AutoName`.
- **`Tier`**: `TierObserved` (seen via OTLP feedback, no health checking) or
  `TierWatched` (has a running `ServiceMonitor`). `ConnectService` only
  constructs and starts a `ServiceMonitor` — and only then fires the CPU
  (RFD 072) / memory (RFD 077) profiling callbacks — when the caller
  supplies a `health_endpoint`. A bare `coral connect` with no health check
  records an authoritative name but stays `TierObserved`; there is nothing
  meaningful to poll.
- **Promotion, not replacement**: `onBeylaServiceObserved` and
  `ConnectService` share one code path (`connectServiceLocked`) that either
  creates a fresh `ServiceEntry` or promotes an existing `TierObserved` entry
  in place — an auto-named `node` process becomes `frontend` the moment
  `coral services watch frontend:3000:/health` runs, without losing its PID
  or binary path.

#### `ServiceNameAdaptor` Chain (`internal/agent/naming`)

A pluggable interface derives a name from process metadata:

```go
type ServiceNameAdaptor interface {
    Resolve(port int32, pid int32, binaryPath string) (name string, ok bool)
}
```

`Chain` runs a priority-ordered list of adaptors and returns the first
`ok=true` result — the same "first non-empty wins" pattern `DiscoveryManager`
(RFD 102) uses for provider merging, so a future `KubernetesAdaptor` or
`DockerAdaptor` can be prepended without touching agent code. The only
built-in today is `ProcessNameAdaptor`:

- **Unique binary** → the binary's base name (e.g. `node`).
- **Conflict** (a second port resolves to a binary name already claimed by
  another port) → `<name>-<port>` for the *new* port. The first port keeps
  its bare name — `ProcessNameAdaptor.Resolve` only returns a value for the
  port being resolved right now, with no channel back to rename an earlier
  caller's stored `AutoName`. Retroactive renaming of the first entry is
  deferred (see Future Engineering Note).
- **No binary info** (non-Linux, or `pid` didn't resolve via
  `proc.GetBinaryPath`) → `port-<N>`.

`onBeylaServiceObserved` resolves `binaryPath` via `proc.GetBinaryPath(pid)`
(the same `/proc/<pid>/exe` helper `ServiceMonitor.discoverProcessInfo`
uses) and only runs the chain when the entry isn't already
`NamingSource=Authoritative` — an explicitly connected service's name is
never overwritten by auto-discovery.

### Distributed Pull-Based Storage

Unlike standard OTEL setups that "push" to a central collector, Coral implements
a **Pull-Based Edge Storage** model (RFD 032):

1. **Local Buffering**: Beyla data is stored in the agent's local DuckDB
   (`beyla_metrics_local`, `beyla_traces_local`) with a short-term retention (
   default: 1 hour).
2. **Colony Polling**: The Colony periodically polls agents via the
   `QueryBeylaMetrics` RPC. Only summarized or requested data is pulled across
   the network, reducing bandwidth significantly in high-throughput
   environments.
3. **Sequence Tracking**: The bridge implements `seq_id` (Sequence ID) tracking (
   RFD 089) to ensure the Colony can resume polling from the exact last-seen
   event, providing gapless reliability.

## Capacity Detection

The system performs runtime capability detection (`detect.go`) checking for \*
\*BTF (BPF Type Format)** and **CO-RE (Compile Once - Run Everywhere)\*\* support.
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

### Metric-Triggered Service Observation

`onBeylaServiceObserved` (RFD 103) currently fires only from the span path
(`Manager.HandleSpan`). A process whose spans are always sampled out but
whose metrics still aggregate normally would not be observed until
`DiscoveryManager`'s next poll. Extending `Manager.consumeMetrics` to
extract `server.port` from metric data-point attributes and fire the same
callback — without adding fields to the `ebpfpb.EbpfEvent` protobufs — would
close this gap; deferred because it's not a coverage gap under the default
protocol/sampling configuration.

### Retroactive Naming Conflict Resolution

`ProcessNameAdaptor` (RFD 104) only suffixes the port it is actively
resolving when a binary-name conflict appears; an earlier port that already
resolved to the bare name keeps it until something re-triggers resolution
for it. A `KubernetesAdaptor`'s pod labels can also change after the fact
(a rolling deploy relabels a pod). Both cases need the same missing
primitive: a way for an adaptor to signal "re-resolve these already-known
ports," and for `Agent` to apply the update to entries whose `NamingSource`
hasn't been pinned by `ConnectService` in the meantime.

### Container-Aware Discovery Providers

The `ProcessDiscoveryProvider` chain (RFD 102) is designed so that
container-aware sources slot in above `EnvVarProvider` without touching
existing code: a planned `ContainerRuntimeProvider` (Docker/Compose service
labels) and `KubernetesProvider` (pod name/namespace via the downward API)
are pure additions — `RegisterProvider` order is the only integration point.
`KubernetesProvider` would also let `coral query topology` show pod/namespace
boundaries directly.

## Related Design Documents (RFDs)

- [**RFD 013**: eBPF Introspection](../../RFDs/013-ebpf-introspection.md)
- [**RFD 032**: BEYLA RED Metrics Integration](../../RFDs/032-beyla-red-metrics-integration.md)
- [**RFD 036**: BEYLA Distributed Tracing](../../RFDs/036-beyla-distributed-tracing.md)
- [**RFD 053**: Beyla Dynamic Service Discovery](../../RFDs/053-beyla-dynamic-service-discovery.md)
- [**RFD 061**: eBPF Uprobe Mechanism](../../RFDs/061-ebpf-uprobe-mechanism.md)
- [**RFD 063**: Intelligent Function Discovery](../../RFDs/063-intelligent-function-discovery.md)
- [**RFD 073**: Return-Instruction Uprobes for Go](../../RFDs/073-return-instruction-uprobes.md)
- [**RFD 090**: eBPF Probe Runtime Filtering](../../RFDs/090-ebpf-probe-runtime-filtering.md)
- [**RFD 091**: Probe Correlation DSL](../../RFDs/091-probe-correlation-dsl.md)
- [**RFD 102**: Pluggable Service Discovery Providers](../../RFDs/102-pluggable-service-discovery-providers.md)
- [**RFD 103**: Default-On Observation](../../RFDs/103-default-on-observation.md)
- [**RFD 104**: ServiceNameAdaptor and Unified Service Map](../../RFDs/104-service-name-adaptor.md)
