# OTLP Ingestion Architecture

## Overview

Coral agents provide OpenTelemetry Protocol (OTLP) endpoints for ingesting
telemetry from instrumented applications. This document describes the
architecture, data flow, and design decisions for OTLP ingestion.

## Endpoints

Each Coral agent exposes two sets of OTLP endpoints:

### 1. Shared OTLP Receiver (User Applications)

- **gRPC**: `0.0.0.0:4317`
- **HTTP**: `0.0.0.0:4318`
- **Purpose**: Receives telemetry from user applications instrumented with
  OpenTelemetry SDKs
- **Protocols**: OTLP/gRPC and OTLP/HTTP
- **Data Types**: Traces and Metrics

### 2. Beyla OTLP Receiver (Internal)

- **gRPC**: `127.0.0.1:4319`
- **HTTP**: `127.0.0.1:4320`
- **Purpose**: Receives telemetry from Beyla's eBPF instrumentation
- **Protocols**: OTLP/gRPC and OTLP/HTTP
- **Data Types**: Metrics (HTTP/gRPC/SQL from eBPF hooks)

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                        User Application                         │
│  ┌──────────────────┐         ┌─────────────────────────────┐   │
│  │ OTel SDK         │         │ No Instrumentation          │   │
│  │ (Active)         │         │ (Passive eBPF)              │   │
│  └────────┬─────────┘         └────────────────┬────────────┘   │
└───────────┼────────────────────────────────────┼────────────────┘
            │                                    │
            │ OTLP (4317/4318)                   │ eBPF hooks
            ▼                                    ▼
┌───────────────────────┐           ┌────────────────────────────┐
│ Shared OTLP Receiver  │           │   Beyla (eBPF)             │
│  - Port 4317 (gRPC)   │           │  - Captures HTTP/gRPC/SQL  │
│  - Port 4318 (HTTP)   │           │  - Exports as OTLP         │
└───────┬───────────────┘           └────────────┬───────────────┘
        │                                        │
        │ Traces (push)    Metrics (buffer)      │ OTLP (4319/4320)
        │       │              │                 │
        ▼       │              │                 ▼
   ┌────────┐   │              │      ┌────────────────────────┐
   │DuckDB  │◄──┘              │      │ Beyla OTLP Receiver    │
   │otel_   │                  │      │  - Port 4319 (gRPC)    │
   │spans_  │                  │      │  - Port 4320 (HTTP)    │
   │local   │                  │      └────────────┬───────────┘
   └────────┘                  │                   │
                               │                   │ Metrics (buffer)
                               └───────┬───────────┘
                                       │
                                       │ Beyla polls both (5s interval)
                                       ▼
                          ┌─────────────────────────┐
                          │   Beyla Transformer     │
                          │  - http.server.duration │
                          │  - rpc.server.duration  │
                          │  - db.client.operation  │
                          └────────────┬────────────┘
                                       │
                                       │ eBPF format
                                       ▼
                          ┌─────────────────────────┐
                          │        DuckDB           │
                          │  - beyla_http_metrics   │
                          │  - beyla_grpc_metrics   │
                          │  - beyla_sql_metrics    │
                          └────────────┬────────────┘
                                       │
                                       │ gRPC API
                                       ▼
                          ┌─────────────────────────┐
                          │  QueryEbpfMetrics RPC   │
                          │  (Unified query API)    │
                          └─────────────────────────┘
```

## Data Flow

### Traces (Push-Based)

**Path**: User App → Shared Receiver → Storage → Query

1. User application exports traces via OTLP (port 4317 or 4318)
2. Shared OTLP receiver ingests traces
3. Traces are **immediately written** to DuckDB `otel_spans_local` table
4. Colony queries traces via `QueryTelemetry` RPC (pull-based)

**Key characteristics**:

- Direct storage (no transformation)
- Filtering applied during ingestion (errors, high latency)
- Sample rate configurable (default: 10%)

### Metrics (Pull-Based with Transformation)

**Path**: User App → Shared Receiver → Buffer → Beyla → Transform → Storage →
Query

1. User application exports metrics via OTLP (port 4317 or 4318)
2. Shared OTLP receiver ingests metrics into **in-memory buffer** (100 batches)
3. Beyla manager polls **both receivers** every 5 seconds:
    - Shared receiver (user app metrics)
    - Beyla receiver (eBPF metrics)
4. Beyla transformer converts OTLP metrics to eBPF format:
    - `http.server.duration` → `BeylaHttpMetrics`
    - `rpc.server.duration` → `BeylaGrpcMetrics`
    - `db.client.operation.duration` → `BeylaSqlMetrics`
5. Transformed metrics stored in DuckDB
6. Colony queries metrics via `QueryEbpfMetrics` RPC

**Key characteristics**:

- Transformation layer (OTLP → eBPF format)
- Unified storage (passive + active metrics)
- 5-second polling interval
- 100-batch in-memory buffer per receiver

## Design Decisions

### Why Two OTLP Receivers?

**Separation of Concerns**:

- **Shared Receiver**: Public-facing, accepts telemetry from any application
- **Beyla Receiver**: Internal, only for Beyla's eBPF output

**Isolation**:

- Beyla's metrics don't interfere with user app telemetry
- Different performance characteristics (eBPF is high-volume)

**Port Conflicts**:

- Beyla exports to OTLP itself, so it can't use standard ports

### Why Transform Metrics Instead of Direct Storage?

**Unified Query Interface**:

```go
// Single API for both passive and active observability
QueryEbpfMetrics(service, time_range) → {
    http_metrics: [...], // From eBPF OR OTLP
    grpc_metrics: [...], // From eBPF OR OTLP
    sql_metrics: [...]   // From eBPF OR OTLP
}
```

**Consistent Format**:

- All metrics use eBPF-style schema (service, method, route, status, latency)
- Same aggregations (RED metrics, percentiles, histograms)
- No need for separate OTLP vs eBPF query logic

**Hybrid Observability**:

- Combine passive (eBPF) and active (OTLP) metrics seamlessly
- Example: eBPF captures external HTTP calls, OTLP captures internal business
  metrics
- Single view for both

### Why Async (Polling) Instead of Sync (Push)?

**Buffer Management**:

- OTLP receiver can accept high-volume metric exports
- Transformation is CPU-intensive (histogram processing)
- Buffer smooths out traffic spikes

**Decoupling**:

- Receiver continues accepting metrics even if Beyla is restarting
- Transformation failure doesn't block ingestion

**Batch Processing**:

- Poll 100 batches → process in bulk → efficient DuckDB writes
- Better throughput than individual metric writes

## Supported Metric Types

Beyla's transformer recognizes OpenTelemetry semantic conventions:

### HTTP Metrics

```
Metric Name: http.server.duration OR http.server.request.duration
Type: Histogram
Unit: milliseconds
Attributes:
  - http.method (GET, POST, etc.)
  - http.route (/api/users/:id)
  - http.status_code (200, 404, 500)
```

### gRPC Metrics

```
Metric Name: rpc.server.duration
Type: Histogram
Unit: milliseconds
Attributes:
  - rpc.method (e.g., /api.UserService/GetUser)
  - rpc.system (grpc)
  - rpc.grpc.status_code (0=OK, 2=UNKNOWN, etc.)
```

### SQL Metrics

```
Metric Name: db.client.operation.duration
Type: Histogram
Unit: milliseconds
Attributes:
  - db.system (postgresql, mysql, etc.)
  - db.operation (SELECT, INSERT, UPDATE)
  - db.statement (optional, query text)
```

**Note**: Only these specific metric names are processed. Other OTLP metrics are
logged (at DEBUG level) but **not stored or queryable**.

### Custom Metrics Limitation

**What happens to custom OTLP metrics?**

Custom metrics (e.g., `business.checkout.total`, `cache.hit_rate`,
`queue.depth`) are:

1. ✅ Accepted by the OTLP receiver
2. ✅ Stored in the in-memory buffer
3. ✅ Polled by Beyla
4. ❌ **Rejected by the transformer** (unrecognized name)
5. 🗑️ **Discarded** (logged at DEBUG level)

**Why this limitation?**

Coral's current architecture focuses on **request-level observability** (HTTP,
gRPC, SQL). Custom business metrics, runtime metrics, and infrastructure metrics
are expected to be sent to dedicated monitoring systems (Prometheus, Datadog,
etc.).

**Workarounds**:

- **Separate Pipeline**: Send custom metrics to a different backend
- **Span Attributes**: Encode business data as trace span attributes
- **Extend Transformer**: Add custom metric handlers (requires code changes)

**Example - Detecting Dropped Metrics**:

```bash
# Look for "Skipping unknown metric" in agent logs
docker logs agent-0 | grep "Skipping unknown metric"

# Output shows custom metrics being dropped:
# DBG Skipping unknown metric component=beyla_transformer metric_name=cache.hit_rate
# DBG Skipping unknown metric component=beyla_transformer metric_name=business.revenue
```

## Configuration

### Agent Configuration

```yaml
# Agent telemetry configuration
telemetry:
    disabled: false
    grpc_endpoint: "0.0.0.0:4317"  # Shared receiver
    http_endpoint: "0.0.0.0:4318"  # Shared receiver
    storage_retention_hours: 1
    filters:
        sample_rate: 0.1                    # 10% of spans
        always_capture_errors: true
        high_latency_threshold_ms: 500.0    # Spans > 500ms

# Beyla configuration (automatic if enabled)
beyla:
    enabled: true
    # Beyla receiver uses 4319/4320 automatically
```

### Application Configuration

#### OpenTelemetry SDK (Go)

```go
import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
    "go.opentelemetry.io/otel/sdk/metric"
)

// Configure OTLP exporter
exporter, _ := otlpmetricgrpc.New(ctx,
    otlpmetricgrpc.WithEndpoint("agent-host:4317"),
    otlpmetricgrpc.WithInsecure(),
)

// Create meter provider
provider := metric.NewMeterProvider(
    metric.WithReader(metric.NewPeriodicReader(exporter,
    metric.WithInterval(5*time.Second))),
)
otel.SetMeterProvider(provider)

// Create metrics
meter := otel.Meter("my-app")
requestDuration, _ := meter.Float64Histogram(
    "http.server.duration", // Beyla will recognize this!
    metric.WithUnit("ms"),
)
```

#### Environment Variables

```bash
# Standard OpenTelemetry environment variables
export OTEL_EXPORTER_OTLP_ENDPOINT=http://agent-host:4317
export OTEL_SERVICE_NAME=my-service
export OTEL_METRICS_EXPORTER=otlp
export OTEL_TRACES_EXPORTER=otlp
```

## Performance Characteristics

### Throughput

- **Traces**: ~10,000 spans/sec per agent (with 10% sampling)
- **Metrics**: ~100,000 data points/sec per agent (before transformation)
- **Buffer Capacity**: 100 batches × avg 1000 metrics/batch = 100k metrics

### Latency

- **Trace Ingestion → Storage**: < 100ms (direct write)
- **Metric Ingestion → Query**: 5-10 seconds (polling interval + transformation)
- **Query Response**: < 50ms (DuckDB indexed queries)

### Resource Usage

- **Memory**: ~50MB per receiver (buffer + connection overhead)
- **CPU**: Minimal for ingestion, ~10% for transformation
- **Storage**: ~1GB per agent per hour (depends on traffic volume)

## Troubleshooting

### No Metrics Appearing in Queries

**Check 1: Verify OTLP export is working**

```bash
# Look for metrics_count > 0 in agent logs
docker logs agent-0 | grep "Processed OTLP metrics"

# Expected output:
# DBG Processed OTLP metrics export component=otlp_receiver metrics_count=3
```

**Check 2: Verify metric names match semantic conventions**

```bash
# Beyla only transforms specific metric names:
# - http.server.duration
# - rpc.server.duration
# - db.client.operation.duration

# Check application logs for exported metric names
```

**Check 3: Verify Beyla is polling shared receiver**

```bash
# Look for shared receiver configuration
docker logs agent-0 | grep "Configured Beyla to process user application"

# Expected output:
# INF Configured Beyla to process user application OTLP metrics
```

### Metrics Stuck in Buffer

**Symptom**: `metrics_count > 0` but `event_count=0` in transformer logs

**Cause**: Beyla transformer doesn't recognize metric name

**Solution**: Use OpenTelemetry semantic conventions (see "Supported Metric
Types")

### High Memory Usage

**Symptom**: Agent memory grows continuously

**Cause**: Metrics buffering faster than Beyla can process

**Solution**:

1. Increase polling frequency (reduce from 5s)
2. Reduce metric cardinality (fewer label combinations)
3. Increase buffer size (modify `metricsBuffer` capacity)

## Future Extensions

### Support for Custom Metrics

To support custom OTLP metrics, the following changes would be needed:

**1. Bypass Transformation (Direct Storage)**

```go
// Option A: Store raw OTLP metrics without transformation
case default:
    // Store in new table: otlp_metrics_raw
    rawMetrics := t.storeRawMetric(metric, serviceName)
```

**Pros**: Simple, preserves all metric types (Counter, Gauge, Histogram,
Summary) **Cons**: Separate query API, no unified view with eBPF metrics

**2. Generic Transformation**

```go
// Option B: Transform all metrics to generic format
case default:
    // Convert to generic time-series format
    genericMetrics := t.transformGenericMetric(metric, serviceName)
```

**Pros**: Unified storage format **Cons**: Loses metric-specific semantics
(histogram buckets, counter vs gauge)

**3. Pluggable Transformers**

```go
// Option C: User-configurable metric handlers
transformers := []MetricTransformer{
    NewHTTPTransformer(),
    NewBusinessMetricTransformer(customRules),
}
```

**Pros**: Flexible, user-defined transformations **Cons**: Complex
configuration, potential performance impact

### Selective Metric Storage

Rather than "all or nothing", allow configuration:

```yaml
beyla:
    metric_transformers:
        -   type: semantic_convention # http.server.duration, etc.
            enabled: true
        -   type: custom_pattern
            enabled: true
            patterns:
                - "business.*" # Store business.* metrics
                - "cache.*" # Store cache.* metrics
            storage: raw # Don't transform, store as-is
```

## Related RFDs

- **RFD 025**: OTLP Receiver Implementation (Traces)
- **RFD 032**: Beyla Integration (eBPF + OTLP Metrics)
- **RFD 039**: Database HTTP Serving (Storage layer)

## References

- [OpenTelemetry Protocol Specification](https://opentelemetry.io/docs/specs/otlp/)
- [OpenTelemetry Semantic Conventions](https://opentelemetry.io/docs/specs/semconv/)
- [Grafana Beyla Documentation](https://grafana.com/docs/beyla/)
