# Instrumenting Applications with Coral

This guide shows how to instrument your applications for observability with
Coral. There are two complementary approaches:

1. **Coral SDK Runtime Monitoring** (Go only): Enable live debugging with eBPF
   uprobes
2. **OpenTelemetry SDK**: Send distributed traces to the Coral Agent (all
   languages)

## Table of Contents

- [Coral SDK Runtime Monitoring](#coral-sdk-runtime-monitoring)
- [OpenTelemetry Instrumentation](#opentelemetry-instrumentation)

---

## Coral SDK Runtime Monitoring

> Status: experimental

The Coral SDK enables **live debugging** of your Go applications using eBPF
uprobes. Unlike traditional observability, this allows you to attach probes to
running code on-demand without redeploying.

### Features

- **Zero-code debugging**: Attach uprobes to any function in your binary
- **On-demand instrumentation**: Probes only exist during debugging sessions
- **Function metadata**: Automatic extraction from DWARF symbols
- **Live data collection**: Capture function calls, arguments, execution time,
  and call stacks
- **AI orchestration**: LLM decides which functions to probe based on metrics
  analysis

### Quick Start

Add the Coral SDK to your Go application:

```go
import "github.com/coral-mesh/coral/pkg/sdk"

func main() {
    // Enable runtime monitoring (starts debug server)
    err := sdk.EnableRuntimeMonitoring(sdk.Options{
        DebugAddr: ":9002", // Optional, defaults to :9002
    })
    if err != nil {
        log.Printf("Failed to enable runtime monitoring: %v", err)
        // App continues normally - SDK is optional
    }

    // Your application code...
}
```

### How It Works

1. **SDK Integration**: `sdk.EnableRuntimeMonitoring()` starts a gRPC server
   that exposes function metadata
2. **Discovery**: Agent discovers SDK-enabled services via service labels or
   explicit service link (`coral connect`)
3. **On-Demand Probes**: When debugging is needed, the agent attaches eBPF
   uprobes to function entry points
4. **Live Data Collection**: Capture function calls, arguments, execution time,
   call stacks
5. **Zero Standing Overhead**: Probes only exist during debugging sessions

### Building with Debug Symbols

For full debugging support (including function arguments and return values),
build with debug symbols:

```bash
# Recommended: Full debug symbols
go build -gcflags="all=-N -l" -o myapp main.go

# Alternative: Stripped binaries (reflection fallback)
# Function discovery works, but cannot capture arguments/return values
go build -ldflags="-w" -o myapp main.go
```

### Example: Live Debugging Session

```bash
# Attach uprobe to a function
coral debug attach payment-service --function main.ProcessPayment

# Stream captured events in real-time
coral debug events <SESSION_ID> --follow
```

**Output:**

```json
{
    "timestamp": "2025-12-03T22:15:30Z",
    "event_type": "entry",
    "function_name": "main.ProcessPayment",
    "arguments": {"amount": 99.99, "currency": "USD"}
}
{
    "timestamp": "2025-12-03T22:15:30Z",
    "event_type": "return",
    "function_name": "main.ProcessPayment",
    "duration_ns": 52341000
}
```

### LLM-Orchestrated Debugging

The live debugging capability integrates with AI assistants via MCP tools:

```bash
$ coral ask "Why is the payment API slow?"

🤖 Analyzing payment service metrics...
   P95 latency: 2.3s (baseline: 150ms)

   Root cause unclear from metrics. Attaching live probes...

   ✓ Uprobe attached: payment.ProcessPayment() [offset 0x4a20]
   ✓ Uprobe attached: db.QueryTransactions() [offset 0x3f10]

   Collecting traces for 30 seconds...

   Analysis:
     • ProcessPayment(): 2.1s avg (2,847 calls)
       └─ db.QueryTransactions(): 2.0s (95% of time)
          └─ Query plan: Sequential scan (234,891 rows)
          └─ Missing index on transactions.user_id

   Root Cause: Missing database index causing slow queries

   Recommendation:
     CREATE INDEX idx_transactions_user_id ON transactions(user_id);

   Detaching probes...
   ✓ Cleanup complete (zero overhead restored)
```

### Security Considerations

- Debug server listens only on **localhost** (127.0.0.1)
- Only accessible by local agents (not exposed to network)
- Read-only access to function metadata
- No ability to modify application state

### See Also

- **[Live Debugging Guide](LIVE_DEBUGGING.md)**: Detailed debugging workflows
- **[SDK Demo](../examples/sdk-demo/)**: Complete example application
- **[RFD 059](../RFDs/059-live-debugging-architecture.md)**: Architecture
  details

---

## OpenTelemetry Instrumentation

This section covers traditional distributed tracing using OpenTelemetry SDKs.

### Overview

The Coral Agent implements the **OpenTelemetry Protocol (OTLP)** for receiving
traces from instrumented applications. The agent is protocol-agnostic and works
with any OTLP-compliant exporter.

### Supported Protocols

- **OTLP/gRPC**: Port `4317` (default)
- **OTLP/HTTP**: Port `4318` (default)

Both protocols support:

- Trace exports
- Resource attributes (service.name, etc.)
- Span attributes (http.method, http.status_code, etc.)
- Span status (OK, ERROR)

## Language Examples

### Go

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

### Node.js

```javascript
const {NodeSDK} = require('@opentelemetry/sdk-node');
const {OTLPTraceExporter} = require('@opentelemetry/exporter-trace-otlp-grpc');

const sdk = new NodeSDK({
    traceExporter: new OTLPTraceExporter({
        url: 'http://localhost:4317',
    }),
});

sdk.start();
```

### Python

```python
from opentelemetry import trace
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import (
    OTLPSpanExporter
)

# Configure OTLP exporter.
otlp_exporter = OTLPSpanExporter(endpoint="localhost:4317", insecure=True)

# Create trace provider.
trace.set_tracer_provider(TracerProvider())
trace.get_tracer_provider().add_span_processor(
    BatchSpanProcessor(otlp_exporter)
)
```

### .NET / C#

```csharp
using OpenTelemetry;
using OpenTelemetry.Exporter;
using OpenTelemetry.Trace;

var tracerProvider = Sdk.CreateTracerProviderBuilder()
    .AddOtlpExporter(options =>
    {
        options.Endpoint = new Uri("http://localhost:4317");
        options.Protocol = OtlpExportProtocol.Grpc;
    })
    .Build();
```

## Using HTTP Instead of gRPC

If you prefer to use OTLP/HTTP instead of OTLP/gRPC, configure your exporter to
use port `4318`.

## Environment Variables

Most OpenTelemetry SDKs support configuration via environment variables:

```bash
# OTLP/gRPC endpoint
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317

# OTLP/HTTP endpoint
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318

# Service name
export OTEL_SERVICE_NAME=my-service

# Resource attributes
export OTEL_RESOURCE_ATTRIBUTES=environment=production,version=1.0.0
```

## Troubleshooting

### Traces Not Appearing

1. **Verify agent is running**:
   ```bash
   curl http://localhost:9001/status
   ```

2. **Check agent is listening on OTLP ports**:
   ```bash
   netstat -tuln | grep -E '4317|4318'
   ```

3. **Test OTLP endpoint**:
   ```bash
   # gRPC
   grpcurl -plaintext localhost:4317 list

   # HTTP
   curl http://localhost:4318/v1/traces
   ```

4. **Enable debug logging in your application**:
   ```bash
   export OTEL_LOG_LEVEL=debug
   ```

### High Overhead

If instrumentation is causing performance issues:

1. **Reduce sampling rate** (application-level):
   ```go
   tp := sdktrace.NewTracerProvider(
       sdktrace.WithSampler(sdktrace.TraceIDRatioBased(0.1)), // 10%
       sdktrace.WithBatcher(exporter),
   )
   ```

2. **Increase batch size**:
   ```go
   tp := sdktrace.NewTracerProvider(
       sdktrace.WithBatcher(exporter,
           sdktrace.WithMaxExportBatchSize(512),
           sdktrace.WithBatchTimeout(5 * time.Second),
       ),
   )
   ```

3. **Exclude high-frequency endpoints** (see "Exclude Sensitive Data" above)

## See Also

- **[Agent Documentation](AGENT.md)**: How the agent processes traces
- **[Configuration Guide](CONFIG.md)**: Agent configuration options
- **[OpenTelemetry Documentation](https://opentelemetry.io/docs/)**: Official
  OTel docs
