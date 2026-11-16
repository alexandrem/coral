# eBPF Instrumentation

Coral uses two collection mechanisms at the agent. Managed Beyla observes
supported application protocols and exports OTLP metrics and traces. Coral's
own function probes capture entry and return events for selected functions.
They share an agent lifecycle, but they have different configuration, buffering,
and failure behavior.

## Managed Beyla and the OTLP bridge

The agent supervises Beyla as a subprocess rather than linking it as a library.
Build tooling can embed platform binaries, which the manager extracts before
starting the process. The manager generates configuration from discovered and
explicitly connected services, supervises the subprocess, and routes its output
through agent logging.

When the candidate set changes, the manager regenerates configuration and
restarts Beyla after a debounce window. Discovery and instrumentation are
therefore separate stages: finding a PID does not itself establish that Beyla
has attached to it. [Service discovery](service-discovery.md) explains candidate
merging, namespace visibility, naming, and the fallback catch-all rule.

Beyla exports to a dedicated loopback OTLP receiver, using default gRPC port
4319 and HTTP port 4320. This receiver is separate from the user OTLP receiver
on 4317/4318. The bridge transforms protocol metrics into Coral records and
routes spans through `HandleSpan` to local Beyla storage. The Colony later reads
that storage through `QueryEbpfMetrics`, with separate cursors for HTTP, gRPC,
SQL, and traces.

The agent's local tables retain data for a configured rolling window. Polling
can resume after a temporary disconnection while those rows remain; it does
not make collection or delivery lossless. Reconfiguration, unsupported traffic,
sampling, and retention can all reduce the evidence available to the Colony.

Implementation: [Beyla manager](../../internal/agent/beyla/manager.go),
[transformer](../../internal/agent/beyla/transformer.go), and
[storage](../../internal/agent/beyla/storage.go).
[OTLP ingestion](otlp-ingestion.md) explains receiver routing;
[telemetry polling](telemetry-polling.md) explains cursor semantics.

## Function probes and return instructions

The eBPF `Manager` owns collectors independently of the main agent process.
A uprobe collector resolves a target process and function, attaches probes, and
reads kernel events into its userspace buffer. Symbol and binary metadata come
from the discovery paths described in [function indexing](function-indexing.md).

For Go return timing, Coral attaches ordinary uprobes to the function's `RET`
instructions rather than using the kernel's uretprobe return-address mechanism.
The collector obtains the function offset and size, reads its executable, and
uses the architecture-specific disassembler to locate return sites. One entry
attachment and potentially several return attachments then describe a function.

At entry, the BPF program stores a timestamp keyed by process TGID and stack
pointer. Using the process identity avoids making the key depend on a particular
OS thread; the stack pointer distinguishes active call frames. A return probe
looks up that key, computes elapsed time, and deletes the entry. This key is
not a goroutine identity or proof that every entry will find a matching return.
Panics, process termination, and stack behavior can leave unmatched entries.

A userspace janitor sweeps entries older than 60 seconds every 30 seconds. That
bounds stale state but can also remove an entry for a sufficiently long call.
The BPF hash map and 256 KiB ring buffer are finite; failed ring-buffer
reservations drop events. Function-probe output is therefore sampled diagnostic
evidence, not a durable record of every call.

Implementation: [uprobe collector](../../internal/agent/ebpf/uprobe_collector.go),
[BPF program](../../internal/agent/ebpf/bpf/uprobe.c), and
[disassembler](../../internal/agent/ebpf/disasm/).

## Runtime filters and collector lifetime

`UpdateFilter` writes the single-entry `filter_config_map` without detaching
the probes. The return program applies minimum/maximum duration and one-in-N
sampling before reserving ring-buffer space. Sampling counters are per CPU;
this is not one globally coordinated counter across all CPUs. The entry path
still records timestamps and emits its entry event.

Collectors can stop capturing after their configured duration while leaving
buffered events available for a final fetch. The manager later removes expired
collectors after its cleanup grace period. These buffers live in the running
process and do not inherit the persistence guarantees of the telemetry tables.

Capability detection checks the kernel and platform features needed by the
collector. BTF/CO-RE support and sufficient privileges are prerequisites where
required; they do not mean every agent platform can run every eBPF collector.

Implementation: [manager](../../internal/agent/ebpf/manager.go),
[capability detection](../../internal/agent/ebpf/detect.go), and
[filter tests](../../internal/agent/ebpf/uprobe_collector_test.go).

## Local correlation and action delivery

The agent's correlation engine evaluates descriptors with CEL predicates and
strategy-specific state machines. Strategies include rate gates, causal pairs,
absence, percentile alarms, edge transitions, and ordered sequences. A match
produces an `Action`; producing that value is separate from executing a
snapshot, starting a profile, or delivering a notification.

The manager's event subscriber feeds uprobe events to the engine when
`GetEvents` returns them. `routeEventsToCorrelation` currently discards actions
returned by synchronous strategies. Absence evaluators can invoke a callback
registered during deployment. This wiring does not establish continuous,
immediate action dispatch for every strategy merely because a descriptor names
`CPU_PROFILE` or `GOROUTINE_SNAPSHOT`.

Read the relevant deployment handler and action consumer before describing an
automated investigation as supported end to end. The
[TypeScript correlation API](../../pkg/sdk/typescript/correlation.ts) exposes
orchestration primitives; it does not remove these agent-side delivery limits.

Implementation: [engine](../../internal/agent/correlation/engine.go),
[strategy tests](../../internal/agent/correlation/strategies_test.go), and
[agent event routing](../../internal/agent/agent.go).

## Design history

- [RFD 032: Beyla Metrics](../../RFDs/032-beyla-red-metrics-integration.md)
- [RFD 036: Beyla Tracing](../../RFDs/036-beyla-distributed-tracing.md)
- [RFD 053: Runtime Service Tracking](../../RFDs/053-beyla-dynamic-service-discovery.md)
- [RFD 061: Uprobe Mechanism](../../RFDs/061-ebpf-uprobe-mechanism.md)
- [RFD 073: Return-Instruction Probes](../../RFDs/073-return-instruction-uprobes.md)
- [RFD 090: Runtime Filtering](../../RFDs/090-ebpf-probe-runtime-filtering.md)
- [RFD 091: Correlation DSL](../../RFDs/091-probe-correlation-dsl.md)
