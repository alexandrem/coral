# System Host Metrics Collection

While Coral supports standard OTLP metrics from applications, it also implements
a dedicated, high-precision collector for host-level infrastructure metrics.
This ensures that operators have immediate visibility into the underlying state
of the nodes running the services.

## Architecture Overview (`internal/agent/collector`)

The **SystemCollector** is a background worker in the Agent that periodically
samples the host's hardware and operating system state.

### 1. The Collection Engine

Coral utilizes the `gopsutil` library to maintain cross-platform compatibility
for its infrastructure metrics.

- **Sampling Interval**: By default, metrics are sampled every **15 seconds**.
- **Scope**: The collector targets five main domains:
    - **CPU**: Aggregate and per-core utilization percentages, and cumulative
      CPU time.
    - **Memory**: Physical memory usage (bytes), total limits, and percentage
      utilization.
    - **Disk I/O**: Total bytes read/written to block devices.
    - **Disk Usage**: Partition-level usage and utilization (focused on `/`).
    - **Network**: Bytes received/transmitted and error counts across all
      physical interfaces.

### 2. Local Tiered Storage (`system_metrics_local`)

Host metrics are stored in a dedicated DuckDB table on the agent.

- **Precision**: 15-second resolution.
- **Retention**: Local storage is ephemeral, with a default retention of **~1
  hour**. This is designed to act as a high-precision buffer for recent
  performance spikes.
- **Sequence Tracking**: Like other telemetry in Coral, every metric data point
  is assigned a `seq_id` via a DuckDB SEQUENCE. This allows the Colony to
  perform efficient, gap-free increments when polling for infrastructure data.

## Metric Schema and Metadata

The `system_metrics_local` table uses a simplified schema optimized for rapid
insertion and retrieval:

| Column        | Type        | Description                                           |
|:--------------|:------------|:------------------------------------------------------|
| `seq_id`      | `UBIGINT`   | The global ordering key for polling.                  |
| `timestamp`   | `TIMESTAMP` | When the sample was taken.                            |
| `metric_name` | `VARCHAR`   | Namespaced name (e.g., `system.cpu.utilization`).     |
| `value`       | `DOUBLE`    | The raw measurement.                                  |
| `unit`        | `VARCHAR`   | Measurements units (e.g., `bytes`, `percent`).        |
| `attributes`  | `JSON`      | Dimensions (e.g., `mount: "/"`, `interface: "eth0"`). |

## Integration with Reliable Polling

The Colony retrieves these metrics using the **Sequence-Aware Polling** model.

1. The Colony stores the last `seq_id` it successfully ingested for a node's
   host metrics.
2. In the next cycle, it requests only records where `seq_id > last_checkpoint`.
3. This ensures that infrastructure metrics are synchronized with the same
   reliability guarantees as application traces and eBPF events, even across
   network partitions.

A planned improvement involves "Triggered Sampling." If a service (discovered
via eBPF) enters a degraded state (e.g., 5xx spike), the Agent can dynamically
increase the `SystemCollector` sampling frequency to 1-second intervals for the
duration of the incident, providing "black-box" flight recorder capabilities for
infrastructure bottlenecks.

## Related Design Documents (RFDs)

- [**RFD 071**: System Metrics](../../RFDs/071-system-metrics.md)
