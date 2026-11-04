# Coral Architecture

Coral observes running applications through agents, collects their telemetry in
an embedded analytical database at the Colony, and exposes queries and debugging
operations through the CLI. This book explains the implemented system, including
who owns state, how it moves, and what happens when a component fails.

Start with the [system overview](system-overview.md) and
[data ownership](data-ownership.md). Then follow the subjects below in reading
order, or enter at the subsystem relevant to your work. Chapters link to code
for implementation details and to RFDs for design history. An RFD's proposed
behavior is not necessarily implemented.

## Connectivity and identity

An agent needs both a route to its Colony and authorization to join it. These
chapters separate the network path from the identities and enrollment protocol.

- [Mesh networking](mesh-networking.md): WireGuard addressing, persistent IP
  leases, platform integration, and bootstrap reachability.
- [Identity and trust](identity-and-trust.md): CA hierarchy, fingerprint
  validation, SPIFFE identities, Bootstrap PSKs, and certificate lifecycle.
- [Discovery and enrollment](discovery-and-enrollment.md): endpoint lookup,
  referral tickets, reverse-dial rendezvous, and durable compound enrollment.

## Observation at the agent

Process discovery determines what to observe and what to call it. Collectors
then produce different kinds of evidence, with different storage and coverage
limits.

- [Service discovery and naming](service-discovery.md): process candidates,
  namespace-aware scanning, naming precedence, and observed versus watched state.
- [eBPF instrumentation](ebpf-instrumentation.md): managed Beyla, function probes,
  runtime filtering, and local event correlation.
- [Function indexing](function-indexing.md): symbol discovery, lazy caching,
  binary identity, and Colony-side function search.
- [OTLP ingestion](otlp-ingestion.md): receiver transports, span transformation,
  source-specific routing, and bounded metric buffering.
- [Host metrics](host-metrics.md): periodic infrastructure sampling and storage.
- [Profiling](profiling.md): CPU and heap collection, frame dictionaries, and
  process-scoped trace/profile correlation.

## Persistence and derived views

The Colony polls retained data using stream-specific cursors. Some data takes
other paths, so local buffering is not a universal delivery guarantee.

- [Data ownership](data-ownership.md): agent and Colony databases, raw versus
  derived state, retention boundaries, and query ownership.
- [Telemetry polling](telemetry-polling.md): checkpoints, storage sessions,
  retries, sequence gaps, and recovery limitations.
- [Service topology](service-topology.md): trace-derived and TCP-observed edges,
  materialization, freshness, and evidence merging.

## Investigation and control

The same CLI operations serve human operators and AI clients. Investigation can
combine queries, local scripts, and actions on an agent.

- [Remote execution](remote-execution.md): host shells, container namespaces,
  capability checks, session lifecycle, and audit metadata.
- [TypeScript investigations](typescript-investigations.md): Deno execution,
  SDK access, reusable diagnostic skills, and result rendering.
- [MCP and LLM integration](mcp-and-llm.md): the local `coral_cli` tool,
  subprocess dispatch, provider abstraction, and conversation state.

## Other documentation

For setup and usage, see [Getting started](../guides/getting-started.md), the
[CLI handbook](../CLI.md), and [Live debugging](../LIVE_DEBUGGING.md). Interface
details live in the [CLI reference](../CLI_REFERENCE.md),
[SDK reference](../SDK_REFERENCE.md), and [Configuration guide](../CONFIG.md).
Deployment and recovery procedures live in [Deployment](../DEPLOYMENT.md) and
[Bootstrap troubleshooting](../BOOTSTRAP_TROUBLESHOOTING.md). Proposals and
decision history live in [RFDs](../../RFDs/).
