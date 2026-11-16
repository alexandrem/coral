# Host Metrics

Host metrics describe the machine on which an agent runs, independently of
application OTLP output. The agent's `SystemCollector` samples operating-system
counters through `gopsutil`, writes local records, and exposes them to the
Colony's system-metrics poller.

This is host-level evidence. Its scope depends on the operating system and the
agent's container and namespace visibility; it should not be interpreted as
per-request or automatically per-container resource accounting.

## Sampling and local records

The collector's default interval is 15 seconds. It gathers aggregate and
per-core CPU usage, memory usage, disk I/O, root-filesystem usage, and network
interface counters. A sampling cycle produces several named measurements,
rather than one row containing a complete host snapshot.

`system_metrics_local` stores an observation timestamp, metric name, value,
unit, and JSON attributes such as an interface or mount. DuckDB assigns a
`seq_id` using the dedicated `seq_system_metrics` sequence. CPU and network
measurements from the same cycle can therefore have distinct sequence IDs and
must retain their own dimensions when aggregated.

Local cleanup maintains a rolling retention window. The typical one-hour
buffer permits later collection after a temporary outage, while rows still
exist. It does not protect unread measurements from retention expiry.

Implementation: [system collector](../../internal/agent/collector/system_collector.go),
[local storage](../../internal/agent/collector/storage.go), and
[storage tests](../../internal/agent/collector/storage_test.go).

## Polling and aggregation

The Colony stores a checkpoint for the agent's system-metrics stream and asks
for subsequent rows. The response includes the database session so a consumer
can reset its cursor if the agent database was recreated. The sequence is local
to this stream, not a shared ordering with application spans or profiles.

The system-metrics poller groups samples into time buckets for central storage.
It holds back the incomplete-minute suffix of a batch and advances its cursor
only through the returned complete-minute prefix, so held-back rows remain
eligible for the next poll. Each measurement's timestamp determines its bucket,
including during gap
recovery; a batch spanning several minutes must not be assigned wholesale to
the most recent minute. Destination storage precedes checkpoint advancement.
The applicable retry and retention limits are the same boundaries described in
[telemetry polling](telemetry-polling.md), subject to this poller's commit path.

A central result can lag the host by sampling and polling intervals. If the
agent is unreachable or a source buffer has expired, a missing bucket does not
mean zero utilization. The current collector uses configured periodic sampling;
this chapter does not assume incident-triggered changes to one-second sampling.

Implementation: [system-metrics poller](../../internal/colony/system_metrics_poller.go)
and [query handler](../../internal/agent/system_metrics_handler.go).

## Design history

- [RFD 071: System Metrics](../../RFDs/071-system-metrics.md)
- [RFD 089: Sequence-Based Polling](../../RFDs/089-sequence-based-polling-checkpoints.md)
