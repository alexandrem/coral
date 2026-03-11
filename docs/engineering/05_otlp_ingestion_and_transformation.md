# OTLP Ingestion and Transformation

Coral provides native support for the **OpenTelemetry Protocol (OTLP)**,
allowing it to act as a seamless drop-in replacement or augmentation for
existing observability pipelines.

## OTLP Receiver Architecture (`internal/agent/telemetry`)

The Agent acts as an OTLP endpoint, exposing both gRPC and HTTP listeners to
collect telemetry from instrumented applications, sidecars, or other collectors.

### 1. Dual-Transport Support

- **gRPC Receiver**: Listens on the configured `GRPCEndpoint` (default `:4317`).
  It implements the standard OTLP `TraceService` and `MetricsService`.
- **HTTP/Protobuf Receiver**: Listens on `HTTPEndpoint` (default `:4318`) at
  `/v1/traces` and `/v1/metrics`. It unmarshals OTLP Protobuf payloads and
  routes them through the same processing logic as the gRPC receiver.

### 2. Trace Transformation Pipeline

When an OTLP trace batch is received, it undergoes a transformation into Coral's
internal high-precision format:

- **Service Discovery**: The `service.name` is extracted from the Resource
  attributes. If missing, it defaults to `unknown`.
- **Identity Conversion**: Trace and Span IDs are converted from raw bytes to
  lowercase hex strings for indexing.
- **Duration Calculation**: Start and end timestamps (nanoseconds) are used to
  calculate millisecond-precision duration.
- **Attribute Mapping**: Standard OTLP attributes (e.g., `http.method`,
  `http.status_code`, `db.statement`) are promoted to first-class fields in the
  internal `Span` struct to enable optimized SQL querying.
- **Sequence Assignment**: Every transformed span is assigned a monotonically
  increasing `seq_id` before being persisted to the local DuckDB. This is
  critical for the Colony's reliable polling mechanism.

### 3. Metric Aggregation and Buffering

Metrics follow a slightly different path to optimize for memory and central
storage:

- **In-Memory Buffering**: Incoming metric batches are stored in a thread-safe,
  size-limited buffer (`metricsBuffer`) on the agent.
- **PData Conversion**: Coral leverages the OpenTelemetry `pdata` library to
  transform raw Protobuf metrics into a structured format that supports
  Histograms, Sums, and Gauges.
- **Short-Term Retention**: Metrics are kept in the agent's memory until the
  Colony's next polling cycle, at which point they are cleared to prevent memory
  bloat.

## Transformation mapping logic

| OTLP Field              | Internal Span Field | Notes                                |
|:------------------------|:--------------------|:-------------------------------------|
| `TraceId`               | `trace_id`          | Hex encoded string                   |
| `Resource.service.name` | `service_name`      | Primary grouping key                 |
| `status_code == ERROR`  | `is_error`          | Boolean flag for rapid filtering     |
| `http.status_code`      | `http_status`       | Promoted attribute                   |
| `Attributes`            | `attributes`        | Stored as JSON for flexible querying |

## Engineering Note: Beyla vs. OTLP

The `OTLPReceiver` allows for a **SpanHandler** callback. This allows Coral to
route traces differently based on their source. For example, traces coming from
the **Beyla** auto-instrumentation engine are routed to specialized tables (
`beyla_traces_local`) which have a schema optimized for the specific L7 (
HTTP/SQL) metadata that Beyla provides, while standard OTLP traces go to the
general-purpose `otel_spans_local`.

## Related Design Documents (RFDs)

- [**RFD 024**: OTEL Integration](../../RFDs/024-otel-integration.md)
- [**RFD 025**: Basic OTLP Ingestion](../../RFDs/025-basic-otel-ingestion.md)
- [**RFD 034**: Serverless OTLP Forwarding](../../RFDs/034-serverless-otlp-forwarding.md)
