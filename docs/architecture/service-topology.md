# Service Topology

Coral builds a dependency graph from two kinds of evidence. Trace-derived
edges describe relationships between service spans. Network-observed edges
describe TCP endpoints seen by an agent. `GetTopology` returns both, marking
each connection's evidence layer so consumers can distinguish their origins.

These observations have different identities and coverage. A trace edge can
name an application service, while a TCP edge may identify only an agent and a
remote IP. Combining the lists does not automatically resolve every network
endpoint to an application service or prove a complete dependency graph.

## Trace-derived edges

The Colony stores polled Beyla spans in `beyla_traces`. Those rows, rather than
`otel_summaries`, are the input to `MaterializeConnections`. The materialized
`service_connections` table is owned by the Colony; agents do not write it.

A propagated trace commonly gives a server span a `parent_span_id` equal to an
upstream span's `span_id`. The materializer prefers this direct link. If it
cannot use that relation, it considers a client/server pair sharing a trace ID
under the query's parent-ID conditions. Its last fallback matches client and
server spans within two seconds. It selects one match per child span, preferring
the stronger strategy and then temporal proximity.

For example, a client span from `frontend` and a linked server span from `api`
produce `frontend → api`. If the identifiers are inconsistent, temporal
matching may still produce that edge, but coincidence is weaker evidence than
parentage. Simultaneous unrelated traffic can make a fallback match ambiguous.
An `L7` label reports the trace source, not the confidence of the matching
strategy used for that individual edge.

All strategies exclude equal normalized service names. Distinct attribution
from [service discovery](service-discovery.md) is therefore a prerequisite for
separating processes into different nodes. Trace coverage and propagation also
depend on instrumentation and protocol support; the materializer cannot infer
every asynchronous or unobserved call from absent spans.

Implementation: [materialization](../../internal/colony/database/connections.go)
and [matching tests](../../internal/colony/database/connections_test.go).

## Materialization and count semantics

The query selects child spans from the requested window and includes an extra
60 seconds of candidate history for matching parents. It groups the chosen
matches by source, destination, and protocol. The current query assigns `http`
as the protocol; it does not derive a precise application protocol from every
span's attributes.

A transaction first resets stored connection counts to zero, then upserts the
recomputed window counts and observation bounds. Repeated materialization over
overlapping windows replaces counts rather than accumulating them. Otherwise,
reading topology repeatedly would inflate apparent call volume. Edges absent
from the current window remain in the table with a zero count.

`GetServiceConnections` uses a mutex-protected materialization timestamp and a
30-second cache TTL. If refresh fails, it logs a warning and reads the previous
table. The result is therefore allowed to be stale. The cache timestamp is
shared, not keyed by each requested time window, and the read returns stored
edges without a `last_observed` filter. A request that refreshes a one-hour
window does not imply that every returned edge was observed in the last hour.

## Network-observed edges

The agent's `netobs` poller runs `ss -tnp`, falling back to `netstat -an` when
needed, and parses established TCP connections. The manager filters and
aggregates observations before reporting them. This is snapshot polling, not
an installed Linux TCP-connect eBPF probe. Short-lived connections between polls
can be missed, and the available metrics depend on what the command output
actually supplies.

The aggregator deduplicates by destination IP, destination port, and protocol
within a flush window. `Streamer` opens a `ReportConnections` client stream,
sends its batch, and closes it for the response. This path does not retain a
sequence-indexed agent database log for replay after a failed transfer.

The Colony report handler looks up the destination IP in the agent registry.
A match supplies `dest_agent_id`; otherwise the destination remains external.
It stores `topology_connections` in a transaction keyed by
`(source_agent_id, dest_ip, dest_port, protocol)`. Upserts add byte and retransmit
counters, keep the newer `last_observed`, and retain an existing RTT when the
new batch has no measurement. Replaying the same report is not automatically
deduplicated by a batch identity.

Implementation: [network observer](../../internal/agent/netobs/),
[report handler](../../internal/colony/server/server.go), and
[TCP edge storage](../../internal/colony/database/topology_connections.go).

## Merging evidence and identities

`GetTopology` reads the agent registry, fetches L7 connections, and fetches L4
rows observed within the last hour. It marks trace edges
`EVIDENCE_LAYER_L7_TRACE`. An L4 source is its reporting agent ID; the target is
the resolved destination agent ID or the raw destination IP. The response does
not include the port in that target string even though storage keys include it.

The handler compares source/target strings with those already present in the L7
edge set. An exact overlap promotes the existing edge to `EVIDENCE_LAYER_BOTH`;
otherwise it appends `EVIDENCE_LAYER_L4_NETWORK`. Because L7 uses service names
and L4 generally uses agent IDs, observations of the same real-world dependency
need not overlap under this comparison. The merge does not perform a general
agent-to-service identity reconciliation.

Failures fetching either connection layer are nonfatal: the handler can return
registry entries with a partial or empty connection list. An empty graph must
therefore be interpreted alongside collection, polling, and query health.

Implementation and validation:
[GetTopology](../../internal/colony/server/server.go),
[merge tests](../../internal/colony/server/topology_merge_test.go), and
[L4 storage tests](../../internal/colony/database/topology_connections_test.go).

## Query surfaces

`coral query topology` calls the same handler and renders a `LAYER` column or a
JSON `layer` field. `--include-l4=false` removes L4-only edges while retaining
trace-derived and combined edges. MCP clients invoke the command through the
local `coral_cli` tool; they do not use a separate Colony topology tool.

Implementation: [CLI topology query](../../internal/cli/query/topology.go) and
[output tests](../../internal/cli/query/topology_test.go).
[MCP and LLM integration](mcp-and-llm.md) explains command dispatch.

## Design history

- [RFD 033: L4 Topology Discovery](../../RFDs/033-service-topology-discovery.md)
- [RFD 092: Service Topology](../../RFDs/092-service-topology.md)
- [RFD 089: Telemetry Polling](../../RFDs/089-sequence-based-polling-checkpoints.md)
- [RFD 102: Process Discovery](../../RFDs/102-pluggable-service-discovery-providers.md)
