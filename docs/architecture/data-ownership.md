# Data Ownership and Persistence

Coral stores recent observations near their source and collects selected data
at the Colony. Both sides use embedded DuckDB databases, but they own different
state. An agent's retained rows are the input to polling and local diagnostics;
the Colony owns its ingestion progress and the fleet views built from that data.

The split is per data path. Standard OTLP metrics use a bounded in-memory
buffer, and TCP observations are sent directly to the Colony. Neither inherits
the replay behavior of a sequence-indexed DuckDB table.

## Authoritative and derived state

| State | Writer and owner | Reader or consumer |
| --- | --- | --- |
| Service entries and monitors | Agent discovery and explicit connection handlers | Service listing, health, collector configuration |
| OTLP spans | Agent telemetry receiver and local storage | Colony telemetry poller and local queries |
| Beyla metrics and traces | Agent Beyla bridge and local storage | Colony Beyla poller |
| Host samples and profiles | Agent collectors and profilers | Corresponding Colony pollers |
| OTLP metric batches | Agent OTLP receiver's memory buffer | Receiver consumers; oldest batches can be evicted |
| TCP connection aggregates | Agent network observer, then Colony report handler | Colony topology queries |
| Polling checkpoints and sequence gaps | Colony pollers and recovery service | Later polls and recovery attempts |
| Summaries and collected Beyla traces | Colony ingestion paths | Unified queries, profiles, and topology derivation |
| `service_connections` | Colony materialization from collected traces | Topology query handler |
| IP allocations, enrollment attempts, and CA records | Colony coordination and enrollment paths | Registration, enrollment retries, and certificate APIs |

A materialized edge is a derived view, not a declaration made by an agent.
Likewise, a checkpoint describes what the Colony has processed for one stream;
it does not identify a globally ordered position across an agent's collectors.

## Agent storage and retention

The agent assigns sequence IDs independently in its
[OTLP span store](../../internal/agent/telemetry/storage.go),
[Beyla store](../../internal/agent/beyla/storage.go),
[host metric store](../../internal/agent/collector/storage.go), and
[profile store](../../internal/agent/profiler/storage.go). A row's sequence ID
orders ingestion within that table, while its timestamp describes observation
time. Concurrent writes can become visible out of sequence order.

These stores retain a rolling window rather than a permanent archive. Cleanup
runs independently of whether a Colony has consumed every row. Buffering can
bridge a temporary disconnection; a disconnection longer than the effective
retention window can lose data. Storage paths and retention are configured per
subsystem, so a nominal one-hour agent buffer is not a universal durability or
capacity guarantee.

The database session identifier is persisted with the agent database and tells
the Colony whether an old cursor still refers to the same storage generation.
Recreating that database changes the session. Retaining it preserves the cursor
namespace across an ordinary process restart.

## Colony ingestion and summaries

The Colony owns its [polling checkpoints](../../internal/colony/database/checkpoints.go)
and uses them to request subsequent records. It also owns aggregated tables,
including telemetry summaries, system metrics, and profiles. For example,
[TelemetryAggregator](../../internal/colony/telemetry_aggregator.go) groups
spans into minute buckets and computes counts, latency statistics, and exemplar
trace IDs. These summaries cannot reconstruct every original span or attribute.

The Colony also retains individual Beyla spans in `beyla_traces`. Those rows
support distributed-trace queries and topology materialization. Thus the Colony
is not a summaries-only database. A trace query can depend on centrally retained
rows even when the agent has already expired its copy.

Retention at the Colony is also table-specific. Consult the relevant poller's
cleanup path and [configuration defaults](../../internal/config/defaults.go)
when reasoning about a time window. An exemplar identifier does not guarantee
that its referenced raw data remains available for the summary's full lifetime.

## Commit boundaries and replay

Pollers store data before advancing their checkpoints. That ordering avoids
acknowledging a batch whose storage failed, but it is not equivalent to a single
transaction across every stream's data and checkpoint. For example, the
[telemetry poller](../../internal/colony/telemetry_poller.go) writes summaries
and then performs separate checkpoint updates. A failure between them can make
the same source rows eligible for another poll.

The observable result of replay depends on the destination table's keys and
upsert behavior. Raw-span deduplication, summary replacement, and additive
aggregation have different consequences. [Telemetry polling](telemetry-polling.md)
explains this boundary and the limits of gap recovery.

TCP observation follows another path: the agent aggregates a flush window and
sends `ReportConnections`; the Colony upserts `topology_connections`. There is
no local sequence-log replay for this path. See
[service topology](service-topology.md) for accumulation and freshness semantics.

## Query ownership

Fleet queries read the Colony's collected and derived data. On-demand local
queries and debugging can reach an agent's more recent retained state. These
views may differ because polling is periodic, retention differs, and some
sources have only partial coverage. A missing central result is therefore not
proof that the underlying process produced no observation.

Usage and SQL interfaces are documented in [Storage](data-ownership.md) and the
[CLI reference](../CLI_REFERENCE.md).

## Design history

- [RFD 010: DuckDB Initialization](../../RFDs/010-duckdb-storage-initialization.md)
- [RFD 039: DuckDB Remote Query CLI](../../RFDs/039-duckdb-remote-query-cli.md)
- [RFD 046: Colony DuckDB Remote Query](../../RFDs/046-colony-duckdb-remote-query.md)
- [RFD 089: Sequence-Based Polling](../../RFDs/089-sequence-based-polling-checkpoints.md)
- [RFD 096: Agent DuckDB Proxy](../../RFDs/096-agent-duckdb-proxy.md)
