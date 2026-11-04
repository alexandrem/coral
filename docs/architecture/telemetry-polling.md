# Telemetry Polling and Recovery

A Colony polls retained telemetry by sequence ID rather than by observation
time. The cursor belongs to one agent and one data type. It says where that
consumer should resume within a storage session; it is not a global clock for
all observations on a host.

This model avoids using timestamps as unique ingestion positions. Two spans can
have the same timestamp, and a span can arrive after another span with a later
observation time. Sequence-based reads still have limits: allocation order need
not equal commit order, retention can remove unread rows, and destination writes
and checkpoints are not universally atomic.

## Cursor identity and a normal poll

The Colony stores `agent_id`, `data_type`, `session_id`, and `last_seq_id` in
`polling_checkpoints`. The agent has separate sequences for OTLP spans, host
metrics, Beyla HTTP/gRPC/SQL metrics and traces, and CPU/memory profile samples.
A sequence value from one table says nothing about another table's progress.

For a normal poll, the consumer reads the checkpoint and sends its last sequence
ID as `StartSeqId`. The agent returns records *after* that ID, subject to the
request's batch limit. The Colony processes the result, stores its destination
data, and then advances the checkpoint. An RPC failure leaves the batch eligible
for a later attempt; a destination-storage failure must not acknowledge it.

For example, a checkpoint of 100 means the next request asks for rows with
`seq_id > 100`. If rows 101 through 120 are returned and storage succeeds, the
consumer can advance to 120. It does not send 101 as the exclusive cursor,
which would skip the first unread row.

Implementation: [TelemetryPoller](../../internal/colony/telemetry_poller.go),
[checkpoint storage](../../internal/colony/database/checkpoints.go), and
[agent query handlers](../../internal/agent/service_handler.go).

## Storage before acknowledgement

The telemetry poller aggregates returned spans, stores summaries, and only then
writes pending checkpoints. These are separate database operations. The
checkpoint API also provides `UpdatePollingCheckpointTx` for callers using a
shared transaction; its existence does not make every poller's commit atomic.

If a process stops after storing data but before updating its checkpoint, the
next poll can read the same records again. Whether that replay duplicates,
replaces, or merges the previous result depends on the destination's keys and
upsert logic. The protocol provides retryable ingestion within the retained
source window; it does not establish exactly-once processing or universally
complete minute buckets.

## Database-session changes

The agent's storage session is persisted with its database. An ordinary restart
with that database intact can preserve it. Recreating the database creates a
new session, even if the agent ID remains the same.

The consumer compares the response's session ID with its stored session. On a
mismatch it resets the stream's checkpoint and re-queries from zero. Otherwise,
a checkpoint of 50,000 from the old database could skip every row in a new
sequence that starts at 1. Gap detection must use the cursor of the actual
re-query, rather than the obsolete stored checkpoint.

See [session-reset and gap tests](../../internal/colony/checkpoint_integration_test.go)
and [poller helpers](../../internal/colony/poller/base.go).

## Detecting sequence holes

`DetectGaps` compares ascending IDs with the expected next value. If the cursor
is 100 and the batch contains 101 and 104, it identifies 102–103 as a possible
hole. Concurrent transactions can reserve IDs and commit in a different order,
so a hole is not by itself proof of lost observations.

The helper reports a gap only when the timestamp of the record after it is at
least ten seconds old. This is a heuristic grace period. It uses record time,
so clock skew and delayed observation timestamps still matter here even though
the polling cursor is a sequence ID. The helper also cannot detect a missing
suffix without a later record exposing the hole.

Detected ranges are recorded in `sequence_gaps`; the main cursor can move
forward while recovery runs separately. The grace period does not prove that
all concurrent writes have completed or that every transient hole will later
be recovered.

## Recovery and its limits

`GapRecoveryService` checks pending gaps every five minutes and dispatches a
stream-specific query starting just before the missing range. It skips agents
that are absent or unhealthy and permits up to three attempted recoveries.
Failed attempts are retried on later recovery cycles; this is not an
exponential-backoff schedule. An error on the final attempt marks the range
`permanent`. Resolved audit rows are cleaned up after seven days.

Recovery is bounded to 10,000 records per query and does not establish complete
coverage of an arbitrary gap. In particular, several recovery handlers treat an
empty successful response as success. Such a gap can be marked `recovered`
even when the source rows have already expired. The status therefore describes
the recovery handler's outcome, not a verified assertion that every missing ID
was restored. A sequence number consumed by a rolled-back write may also have
no row to recover.

The implementation is in [gap_recovery.go](../../internal/colony/gap_recovery.go),
with [recovery tests](../../internal/colony/gap_recovery_test.go).
[Data ownership](data-ownership.md) describes the retention boundary that limits
both ordinary polling and recovery.

## Scheduling

`BasePoller` runs a poll at startup and then on its configured interval. Its
healthy-agent visitor is sequential and continues after individual query
errors. Cleanup runs separately. These choices isolate an agent failure from
later visits but do not guarantee a fixed fleet-wide freshness bound: query
timeouts, backlog, batch limits, and cleanup all affect the data a reader sees.

## Design history

- [RFD 089: Sequence-Based Polling Checkpoints](../../RFDs/089-sequence-based-polling-checkpoints.md)
- [RFD 018: Runtime Context Reporting](../../RFDs/018-runtime-context-reporting.md)
