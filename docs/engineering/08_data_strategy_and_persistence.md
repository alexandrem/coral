# Data Strategy: Edge Precision vs. Colony Aggregation

Coral implements a tiered data persistence model designed to balance
high-fidelity observability with central storage efficiency.

## Edge Layer: High-Precision Buffering

Agents store raw, high-precision observability data in a localized **DuckDB**
instance.

### Raw Data Integrity (`internal/agent/telemetry/storage.go`)

- **Granularity**: Every individual span, metric sample, and uprobe event is
  recorded as a row in tables like `otel_spans_local`.
- **Schema**: Stores full context including Trace IDs, Span IDs,
  nanosecond-precision timestamps, and raw JSON attributes.
- **Retention**: Designed for high-write/high-delete churn. Data is typically
  retained for only **1 hour**.
- **Role**: This layer acts as a "flight recorder." It provides the source of
  truth for the Colony's pull requests and allows for deep-dive "on-demand"
  queries for raw traces that haven't been aggregated yet.

## Colony Layer: Aggregated Insights

The central Colony pulls data from agents and transforms it into long-term
analytical summaries.

### The Aggregation Pipeline (`internal/colony/telemetry_aggregator.go`)

1. **Pull**: Colony fetches batches of raw records using `seq_id` checkpoints.
2. **Bucket**: Data is aligned to **1-minute time buckets**.
3. **Summarize**:
    - **Latencies**: Calculates P50, P95, and P99 percentiles for the bucket.
    - **Errors**: Counts total occurrences of error flags.
    - **Throughput**: Records total span/sample counts.
    - **Service Topology**: Discovers and persists cross-service dependencies in `service_connections` (materialized with a 30s TTL).
    - **Exemplars**: Selects up to 5 "Sample Trace IDs" per bucket to allow "
      pivot-to-trace" navigation from aggregate charts.
4. **Store**: Only the summarized `otel_summaries` and `service_connections` are kept in the central
   DuckDB.

## Persistence Trade-offs

| Feature       | Edge (Agent)                 | Center (Colony)                  |
|:--------------|:-----------------------------|:---------------------------------|
| **Data Type** | Raw / Individual Records     | Aggregated Summaries             |
| **Precision** | Nanosecond / Full Attributes | 1-Minute Buckets / Percentiles   |
| **Storage**   | Volatile / High Churn        | Durable / Analytical             |
| **Retention** | ~1 Hour                      | 7 - 30 Days                      |
| **Query Use** | Diagnostics / Exemplars      | Trends / Alerting / Dashboarding |

## Engineering Note: ACID on the Edge

Using DuckDB on the edge ensures that sequence IDs (`seq_id`) are persisted with
ACID guarantees. This is the foundation of Coral's **at-least-once** delivery
model; even if an agent crashes, it resumes from the last successfully committed
sequence in its local WAL (Write-Ahead Log).

## Design Decision: In-Process vs Out-of-Process DB

Coral uses embedded databases (like DuckDB or SQLite via specialized drivers) to
minimize operational complexity. This eliminates the need for managing a
separate database cluster for simple deployments.

## Future Engineering Note: Cold Storage

For long-term analysis, a "Ship to Object Storage" (S3/GCS) pipeline for the
edge DuckDB files could provide infinite retention without bloating the live
Colony database.

## Related Design Documents (RFDs)

- [**RFD 010**: DuckDB Storage Initialization](../../RFDs/010-duckdb-storage-initialization.md)
- [**RFD 039**: DuckDB Remote Query CLI](../../RFDs/039-duckdb-remote-query-cli.md)
- [**RFD 046**: Colony DuckDB Remote Query](../../RFDs/046-colony-duckdb-remote-query.md)
- [**RFD 089**: Sequence Based Polling Checkpoints](../../RFDs/089-sequence-based-polling-checkpoints.md)
- [**RFD 096**: Agent DuckDB Proxy](../../RFDs/096-agent-duckdb-proxy.md)
