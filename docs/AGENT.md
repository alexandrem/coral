# Coral Agent Configuration Guide

The Coral Agent is a lightweight observability daemon that runs alongside your applications to collect telemetry data and respond to colony queries.

## Overview

The agent operates on a **pull-based architecture**:
- Receives OpenTelemetry (OTLP) traces from your applications
- Applies static filtering rules to reduce data volume
- Stores filtered spans locally (~1 hour retention)
- Responds to colony queries for recent telemetry data

## Table of Contents

- [OpenTelemetry Integration](#opentelemetry-integration)
- [Configuration](#configuration)
- [Static Filtering](#static-filtering)
- [Examples](#examples)
- [Troubleshooting](#troubleshooting)

---

## OpenTelemetry Integration

### Supported Protocols

The agent implements the **OpenTelemetry Protocol (OTLP)** for receiving traces:

- **OTLP/gRPC**: Port `4317` (default)
- **OTLP/HTTP**: Port `4318` (default)

Both protocols support:
- Trace exports
- Resource attributes (service.name, etc.)
- Span attributes (http.method, http.status_code, etc.)
- Span status (OK, ERROR)

### Instrumenting Your Application

Use any OpenTelemetry SDK to send traces to the agent:

**Go Example**:
```go
import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Configure OTLP exporter pointing to agent.
exporter, _ := otlptracegrpc.New(
    context.Background(),
    otlptracegrpc.WithEndpoint("localhost:4317"),
    otlptracegrpc.WithInsecure(),
)

// Create trace provider.
tp := sdktrace.NewTracerProvider(
    sdktrace.WithBatcher(exporter),
)
otel.SetTracerProvider(tp)
```

**Node.js Example**:
```javascript
const { NodeSDK } = require('@opentelemetry/sdk-node');
const { OTLPTraceExporter } = require('@opentelemetry/exporter-trace-otlp-grpc');

const sdk = new NodeSDK({
  traceExporter: new OTLPTraceExporter({
    url: 'http://localhost:4317',
  }),
});

sdk.start();
```

**Python Example**:
```python
from opentelemetry import trace
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter

# Configure OTLP exporter.
otlp_exporter = OTLPSpanExporter(endpoint="localhost:4317", insecure=True)

# Create trace provider.
trace.set_tracer_provider(TracerProvider())
trace.get_tracer_provider().add_span_processor(
    BatchSpanProcessor(otlp_exporter)
)
```

**Java Example**:
```java
import io.opentelemetry.api.OpenTelemetry;
import io.opentelemetry.exporter.otlp.trace.OtlpGrpcSpanExporter;
import io.opentelemetry.sdk.OpenTelemetrySdk;
import io.opentelemetry.sdk.trace.SdkTracerProvider;
import io.opentelemetry.sdk.trace.export.BatchSpanProcessor;

OtlpGrpcSpanExporter spanExporter = OtlpGrpcSpanExporter.builder()
    .setEndpoint("http://localhost:4317")
    .build();

SdkTracerProvider tracerProvider = SdkTracerProvider.builder()
    .addSpanProcessor(BatchSpanProcessor.builder(spanExporter).build())
    .build();

OpenTelemetry openTelemetry = OpenTelemetrySdk.builder()
    .setTracerProvider(tracerProvider)
    .build();
```

---

## Configuration

### Telemetry Configuration Structure

```yaml
telemetry:
  # Disable telemetry collection (default: enabled)
  disabled: true

  # OTLP gRPC receiver endpoint.
  # Standard port: 4317
  grpc_endpoint: "0.0.0.0:4317"

  # OTLP HTTP receiver endpoint.
  # Standard port: 4318
  http_endpoint: "0.0.0.0:4318"

  # Local storage retention (hours).
  # Default: 1 hour (aligns with pull-based architecture)
  storage_retention_hours: 1

  # Static filtering rules.
  filters:
    # Always capture error spans (status = ERROR).
    always_capture_errors: true

    # High latency threshold in milliseconds.
    # Spans with duration > threshold are always captured.
    # Default: 500ms
    high_latency_threshold_ms: 500.0

    # Sample rate for normal spans (0.0 to 1.0).
    # Example: 0.10 = 10% of normal spans
    # Default: 0.10 (10%)
    sample_rate: 0.10
```

### Configuration Fields

#### `telemetry.enabled`
- **Type**: `boolean`
- **Default**: `false`
- **Description**: Master switch for telemetry collection. Set to `true` to enable OTLP receiver.

#### `telemetry.grpc_endpoint`
- **Type**: `string`
- **Default**: `"0.0.0.0:4317"`
- **Description**: Address to bind the OTLP gRPC receiver. Use `0.0.0.0` to listen on all interfaces or `127.0.0.1` for localhost only.
- **Format**: `"<host>:<port>"`

#### `telemetry.http_endpoint`
- **Type**: `string`
- **Default**: `"0.0.0.0:4318"`
- **Description**: Address to bind the OTLP HTTP receiver. Use `0.0.0.0` to listen on all interfaces or `127.0.0.1` for localhost only.
- **Format**: `"<host>:<port>"`

#### `telemetry.storage_retention_hours`
- **Type**: `integer`
- **Default**: `1`
- **Description**: How long to keep spans in local storage (in hours). Older spans are automatically deleted.
- **Rationale**: Pull-based architecture - colony queries recent data for AI investigations.

#### `telemetry.filters.always_capture_errors`
- **Type**: `boolean`
- **Default**: `true`
- **Description**: Always capture spans with error status (status code = ERROR).

#### `telemetry.filters.high_latency_threshold_ms`
- **Type**: `float`
- **Default**: `500.0`
- **Description**: Latency threshold in milliseconds. Spans with duration exceeding this value are always captured.
- **Example**: `500.0` = capture all spans > 500ms

#### `telemetry.filters.sample_rate`
- **Type**: `float` (0.0 to 1.0)
- **Default**: `0.10`
- **Description**: Sampling rate for normal spans (not errors, not high latency).
- **Example**: `0.10` = keep 10% of normal spans

---

## Static Filtering

The agent applies **static filtering rules** to reduce data volume while capturing important spans:

### Filtering Rules (Applied in Order)

1. **Always capture errors**: Spans with `status.code = ERROR` are always kept.
2. **Always capture high latency**: Spans with `duration > high_latency_threshold_ms` are always kept.
3. **Sample normal spans**: Other spans are sampled at `sample_rate`.

### Example Filtering Behavior

Given configuration:
```yaml
filters:
  always_capture_errors: true
  high_latency_threshold_ms: 500.0
  sample_rate: 0.10
```

**Spans Received**:
- 100 spans with errors → **100 kept** (100%)
- 50 spans > 500ms → **50 kept** (100%)
- 1000 normal spans → **~100 kept** (10% sample rate)

**Total**: 250 kept out of 1150 received (~22% retention)

### Why Static Filtering?

- **Predictable**: Sampling behavior is deterministic and easy to reason about.
- **Debuggable**: No complex adaptive algorithms that change behavior over time.
- **Operational simplicity**: No need to tune ML models or dynamic thresholds.
- **Captures important signals**: Errors and high-latency spans are critical for debugging.

---

## Examples

### Example 1: Local Development

**Use Case**: Testing locally on a single machine.

**Configuration**:
```yaml
telemetry:
  enabled: true
  grpc_endpoint: "127.0.0.1:4317"  # Localhost only
  http_endpoint: "127.0.0.1:4318"  # Localhost only
  storage_retention_hours: 1
  filters:
    always_capture_errors: true
    high_latency_threshold_ms: 500.0
    sample_rate: 1.0  # Keep 100% of normal spans (dev mode)
```

**Application Configuration** (Go):
```go
exporter, _ := otlptracegrpc.New(
    context.Background(),
    otlptracegrpc.WithEndpoint("localhost:4317"),
    otlptracegrpc.WithInsecure(),
)
```

### Example 2: Production (High Traffic)

**Use Case**: Production service with high request volume.

**Configuration**:
```yaml
telemetry:
  enabled: true
  grpc_endpoint: "0.0.0.0:4317"  # All interfaces
  http_endpoint: "0.0.0.0:4318"  # All interfaces
  storage_retention_hours: 1
  filters:
    always_capture_errors: true
    high_latency_threshold_ms: 500.0
    sample_rate: 0.05  # Keep 5% of normal spans (reduce volume)
```

**Rationale**: Low sample rate reduces storage/network while still capturing errors and slow requests.

### Example 3: Kubernetes Sidecar

**Use Case**: Agent running as a sidecar in Kubernetes.

**Deployment**:
```yaml
apiVersion: v1
kind: Pod
metadata:
  name: myapp
spec:
  containers:
    # Application container
    - name: app
      image: myapp:latest
      env:
        - name: OTEL_EXPORTER_OTLP_ENDPOINT
          value: "http://localhost:4317"

    # Coral agent sidecar
    - name: coral-agent
      image: coral-agent:latest
      ports:
        - containerPort: 4317
          name: otlp-grpc
        - containerPort: 4318
          name: otlp-http
```

**Agent Configuration**:
```yaml
telemetry:
  enabled: true
  grpc_endpoint: "0.0.0.0:4317"
  http_endpoint: "0.0.0.0:4318"
  storage_retention_hours: 1
  filters:
    always_capture_errors: true
    high_latency_threshold_ms: 1000.0  # Higher threshold for K8s
    sample_rate: 0.10
```

### Example 4: Serverless (Lambda/Cloud Run)

**Use Case**: Serverless workloads exporting to regional OTLP agent.

**See**: `RFDs/034-serverless-otlp-forwarding.md` for detailed serverless integration.

**Configuration** (Regional Agent):
```yaml
telemetry:
  enabled: true
  grpc_endpoint: "0.0.0.0:4317"
  http_endpoint: "0.0.0.0:4318"
  storage_retention_hours: 2  # Longer retention for serverless
  filters:
    always_capture_errors: true
    high_latency_threshold_ms: 2000.0  # Cold start tolerant
    sample_rate: 0.20  # Higher sampling for serverless
```

---

## Troubleshooting

### Agent Not Receiving Traces

**Symptoms**: No spans stored in agent database.

**Checklist**:
1. Verify `telemetry.enabled: true` in agent config
2. Check agent logs for "OTLP receiver listening" messages
3. Verify application is exporting to correct endpoint (`localhost:4317`)
4. Check firewall rules (ports 4317, 4318)
5. Test connectivity: `telnet localhost 4317`

**Debug Commands**:
```bash
# Check if agent is listening on OTLP ports
netstat -tuln | grep -E '4317|4318'

# Test gRPC endpoint
grpcurl -plaintext localhost:4317 list

# Check agent logs
journalctl -u coral-agent -f
```

### High Memory Usage

**Symptoms**: Agent using excessive memory.

**Causes**:
- High span ingestion rate
- Long `storage_retention_hours`
- High `sample_rate`

**Solutions**:
1. Reduce `sample_rate` (e.g., `0.05` instead of `0.10`)
2. Reduce `storage_retention_hours` to `1`
3. Increase `high_latency_threshold_ms` to reduce captured spans
4. Check application for span explosion (e.g., tracing in hot loops)

**Monitor Storage**:
```bash
# Check agent storage size
du -sh /var/lib/coral-agent/

# Check span count
sqlite3 /var/lib/coral-agent/telemetry.db "SELECT COUNT(*) FROM otel_spans_local;"
```

### Spans Filtered Too Aggressively

**Symptoms**: Important spans missing from colony.

**Solutions**:
1. Increase `sample_rate` (e.g., `0.20` instead of `0.10`)
2. Decrease `high_latency_threshold_ms` to capture more slow spans
3. Ensure application is setting span status correctly for errors
4. Check application is using semantic conventions (http.status_code, etc.)

### Colony Not Querying Agent

**Symptoms**: Agent has spans, but colony doesn't see them.

**Checklist**:
1. Verify agent is registered with colony (`ListAgents` RPC)
2. Check WireGuard mesh connectivity
3. Verify QueryTelemetry RPC is wired in agent server
4. Check colony logs for query errors

---

## Data Flow

```
┌─────────────────────────────────────────────────────────────┐
│  Application (Instrumented with OpenTelemetry SDK)         │
└─────────────────┬───────────────────────────────────────────┘
                  │ OTLP Export (gRPC/HTTP)
                  ▼
┌─────────────────────────────────────────────────────────────┐
│  Coral Agent - OTLP Receiver                                │
│  • Listens on ports 4317 (gRPC) / 4318 (HTTP)              │
│  • Parses OTLP trace exports                                │
└─────────────────┬───────────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────────────┐
│  Static Filtering                                           │
│  1. Always capture errors                                   │
│  2. Always capture high latency (> threshold)               │
│  3. Sample normal spans (sample_rate)                       │
└─────────────────┬───────────────────────────────────────────┘
                  │ Filtered Spans
                  ▼
┌─────────────────────────────────────────────────────────────┐
│  Local Storage (DuckDB/SQLite)                              │
│  • Retention: ~1 hour                                       │
│  • Table: otel_spans_local                                  │
│  • Indexed by timestamp, service_name                       │
└─────────────────┬───────────────────────────────────────────┘
                  │
                  │ ┌────────────────────────────────────┐
                  ├─┤ TTL Cleanup (hourly)              │
                  │ └────────────────────────────────────┘
                  │
                  │ Colony Query (on-demand)
                  ▼
┌─────────────────────────────────────────────────────────────┐
│  QueryTelemetry RPC Handler                                 │
│  • Queries local storage by time range + service names      │
│  • Returns filtered spans to colony                         │
└─────────────────┬───────────────────────────────────────────┘
                  │
                  ▼
          ┌──────────────────┐
          │  Colony          │
          │  • Aggregates    │
          │  • Summarizes    │
          └──────────────────┘
```

---

## Performance Considerations

### Agent Resource Usage

**CPU**:
- **Idle**: ~5-10% (cleanup goroutines)
- **Receiving traces**: ~20-40% per 10k spans/sec
- **Queried by colony**: ~10-20% spike

**Memory**:
- **Base**: ~50-100 MB
- **Per million spans stored**: ~200-300 MB
- **Peak during query**: +10-20% during aggregation

**Disk**:
- **Database size**: ~1 KB per span (compressed)
- **1 hour retention @ 1k spans/sec**: ~3.6 GB
- **1 hour retention @ 100 spans/sec**: ~360 MB

### Recommended Instance Sizes

| Span Ingestion Rate | vCPUs | Memory | Disk  |
|---------------------|-------|--------|-------|
| 100 spans/sec       | 1     | 512 MB | 1 GB  |
| 1,000 spans/sec     | 2     | 2 GB   | 5 GB  |
| 10,000 spans/sec    | 4     | 8 GB   | 20 GB |

---

## Security Considerations

### Network Security

**Recommendation**: Bind OTLP endpoints to localhost or private network only.

```yaml
# Secure (localhost only)
grpc_endpoint: "127.0.0.1:4317"

# Insecure (all interfaces - only use behind firewall)
grpc_endpoint: "0.0.0.0:4317"
```

### Data Privacy

**PII in Spans**: OpenTelemetry spans may contain:
- HTTP headers (Authorization, cookies)
- Request/response bodies
- User IDs in span attributes

**Mitigation**:
1. Configure application to exclude sensitive attributes
2. Short retention period (1 hour) limits exposure
3. Review application's OTel instrumentation

**Example** (Go - exclude headers):
```go
import "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

handler := otelhttp.NewHandler(myHandler, "server",
    otelhttp.WithFilter(func(r *http.Request) bool {
        // Exclude certain paths from tracing
        return !strings.HasPrefix(r.URL.Path, "/health")
    }),
)
```

---

## Related Documentation

- **RFD 025**: Basic OpenTelemetry Ingestion (`RFDs/025-basic-otel-ingestion.md`)
- **RFD 032**: Beyla Integration for eBPF correlation (`RFDs/032-beyla-integration.md`)
- **RFD 034**: Serverless OTLP Forwarding (`RFDs/034-serverless-otlp-forwarding.md`)
- **Concept**: Coral Architecture (`docs/CONCEPT.md`)

---

## Support

For questions or issues:
- Check agent logs: `journalctl -u coral-agent -f`
- Review RFD 025 for architecture details
- File issues at: https://github.com/coral-io/coral/issues
