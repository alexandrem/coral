# Reliable Telemetry Polling: The Sequence Model

Coral avoids common pitfalls of timestamp-based distributed polling (clock skew,
out-of-order arrival) by using a **Sequence-Aware Log** model inspired by
distributed stream processing.

## Sequence-Based Checkpoints (RFD 089)

To ensure no data is lost during the pull process, every telemetry record (
metric, span, profile) is assigned a monotonically increasing `seq_id` at the
Agent level.

### Why not use timestamps?

In a distributed system, relying on wall-clock time for "last seen" markers is
dangerous because:

- **Clock Skew**: Agents and Colony may have drifting clocks.
- **Precision Loss**: Timestamps might not be unique for high-frequency events.
- **Commit Latency**: A record with an older timestamp might be committed to the
  database _after_ a newer one.

The `seq_id` acts as a logical clock that guarantees a strict total order of
events produced by a single agent.

### The Checkpoint Loop

1. **Query Checkpoint**: Colony retrieves the `last_seq_id` and `session_id` for
   a specific Agent/DataType.
2. **Range Request**: Requests a batch starting from `last_seq_id + 1`.
3. **Commit**: Colony stores the records and updates its local checkpoint. This
   ensures **At-Least-Once** delivery.

## Gap Detection and Recovery

Wait-free concurrency in the Agent's DuckDB can lead to "holes" in the sequence
if one transaction commits faster than another.

### Detective Logic (`internal/colony/poller/base.go`)

The `DetectGaps` function scans batches for non-consecutive IDs.

- **Consistency vs. Availability**: To avoid blocking the pipeline for transient
  in-flight transactions, Coral uses a **Grace Period** (10s). Gaps are only "
  finalized" and moved to the recovery queue if data _after_ the gap is old
  enough to guarantee the missing records weren't just delayed.

### Gap Recovery Service (`internal/colony/gap_recovery.go`)

When a gap is finalized, it enters a specialized recovery loop.

- **Retry Strategy**: Performs up to 3 re-queries with exponential backoff.
- **Permanent Loss Handle**: If data has aged out of the Agent's 1-hour buffer,
  the gap is marked as `permanent`. This allows the main checkpoint to progress
  while flagging exactly where data was lost for auditing.

## Session Resilience

Agents expose a `session_id` (typically a UUID generated at startup).

- **Hard Resets**: If the agent loses its persistent volume and restarts with a
  clean database, the `session_id` changes.
- **Conflict Resolution**: Colony detects the mismatch and resets its local
  checkpoint to 0, preventing "sequence ID from the future" errors.

## Related Design Documents (RFDs)

- [**RFD 089
  **: Sequence Based Polling Checkpoints](../../RFDs/089-sequence-based-polling-checkpoints.md)
- [**RFD 018
  **: Runtime Context Reporting](../../RFDs/018-runtime-context-reporting.md)
