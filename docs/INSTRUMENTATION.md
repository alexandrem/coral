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

Coral also supports **agentless binary scanning** - you can debug
applications **without SDK integration** if the binary has debug symbols.

> **Important:** Most production Go binaries use `-ldflags="-w -s"` to fully
> strip debug symbols. For these binaries, **SDK integration is required**.
> Agentless mode works best for development builds and legacy apps with symbols.

### Features

- **Zero-code debugging**: Attach uprobes to any function in your binary
- **On-demand instrumentation**: Probes only exist during debugging sessions
- **Function metadata**: Automatic extraction from DWARF symbols
- **Live data collection**: Capture function calls, arguments, execution time,
  and call stacks
- **AI orchestration**: LLM decides which functions to probe based on metrics
  analysis
- **Agentless mode**: Works with binaries that have debug symbols (dev builds or
  legacy apps)

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

Coral supports two modes for live debugging:

#### With SDK Integration (Recommended)

1. **SDK Integration**: `sdk.EnableRuntimeMonitoring()` starts a debug server
   that exposes function metadata
2. **Fast Discovery**: Agent fetches function list via HTTP API (~1-2s for 50k
   functions)
3. **Robust Fallback**: If DWARF is stripped, SDK falls back to symbol table (
   `.symtab`/`.dynsym`)
4. **On-Demand Probes**: When debugging is needed, the agent attaches eBPF
   uprobes to function entry points
5. **Live Data Collection**: Capture function calls, arguments, execution time,
   call stacks
6. **Zero Standing Overhead**: Probes only exist during debugging sessions

#### Without SDK (Agentless Mode)

1. **Automatic Discovery**: Agent discovers services via process monitoring or
   explicit `coral connect`
2. **Binary Scanning**: Agent directly parses DWARF symbols from the binary (~
   100-200ms)
3. **Semantic Indexing**: Functions are indexed with AI embeddings for
   intelligent search
4. **Uprobe Attachment**: Works identically to SDK mode once functions are
   discovered
5. **Symbol Table Fallback**: If DWARF is stripped, falls back to ELF symbol
   table (same as SDK!)

**Discovery Priority Chain:**

- Priority 1: SDK HTTP API (recommended - optimized bulk export)
- Priority 2: Binary Scanner with symbol table fallback (works with `-w`
  stripped binaries!)
- Priority 3: Direct DWARF parsing (legacy fallback)

### Building with Debug Symbols

Different build configurations affect what Coral can discover:

```bash
# Development: Full debug symbols (DWARF + symbols)
# ✅ SDK works: Full metadata (args, return values, line numbers)
# ✅ Agentless works: Full metadata via DWARF parsing
go build -gcflags="all=-N -l" -o myapp main.go

# Uncommon: DWARF stripped, symbols intact (-w only)
# ✅ SDK works: Function discovery via symbol table (no arg/return info)
# ✅ Agentless works: Function discovery via symbol table (no file/line info)
# Note: Rarely used in production (most strip both DWARF and symbols)
go build -ldflags="-w" -o myapp main.go

# Production (typical): Fully stripped (-w -s)
# ✅ SDK works: Uses SDK metadata API (bypasses binary symbols entirely)
# ❌ Agentless fails: No symbols or DWARF available
# Note: Most production builds use this for size and security
go build -ldflags="-w -s" -o myapp main.go
```

**IMPORTANT:** Most production Go binaries use `-ldflags="-w -s"` for size
reduction and security. For these binaries, **you must integrate the SDK** -
agentless mode will not work.

### CPU Profiling Requirements (ARM64)

Coral's eBPF-based CPU profiler requires **frame pointers** for stack unwinding.
On AMD64, Go enables frame pointers by default (Go 1.7+). However, on **ARM64
(Apple Silicon, AWS Graviton)**, frame pointers must be explicitly enabled:

```bash
# ARM64: Enable frame pointers for CPU profiling
go build -tags=framepointer -o myapp main.go

# AMD64: Frame pointers enabled by default (no special flags needed)
go build -o myapp main.go
```

**Why This Matters:**

- **eBPF Stack Unwinding**: The eBPF profiler uses `bpf_get_stackid()` which
  performs kernel-side stack unwinding
- **DWARF Not Available**: Unlike userspace profilers (like `perf`), eBPF cannot
  use DWARF symbols for unwinding
- **Frame Pointers Required**: The kernel's BPF stack walker requires frame
  pointers to traverse the call stack

**Symptoms Without Frame Pointers:**

- CPU profiler captures 0 samples even under load
- Continuous profiling shows `total_samples=0` in logs
- On-demand profiling returns success but no stack traces

**Platform-Specific Behavior:**

| Platform | Frame Pointers | Build Flag Required |
|----------|----------------|---------------------|
| AMD64 (x86_64) | ✅ Default (Go 1.7+) | None |
| ARM64 (Apple Silicon, Graviton) | ❌ Not default | `-tags=framepointer` |
| ARM32 | ❌ Not default | `-tags=framepointer` |

**Docker/Container Example:**

```dockerfile
FROM golang:1.25-alpine

WORKDIR /app
COPY . .

# Enable frame pointers for ARM64 CPU profiling
RUN go build -tags=framepointer -o myapp main.go

CMD ["./myapp"]
```

**Verification:**

```bash
# Check if your app is capturing CPU samples
docker exec <container> cat /sys/kernel/debug/tracing/trace_pipe
# Should show perf_event samples when profiler is active

# Or check agent logs
docker logs <agent-container> | grep "CPU profile collected"
# Should show total_samples > 0
```

**Alternative: System-Level Perf Events**

If you cannot rebuild with `-tags=framepointer`, ensure the host/VM has perf
events enabled:

```bash
# Required for eBPF profiling (default is often 4, which blocks everything)
sudo sysctl -w kernel.perf_event_paranoid=-1

# For Colima users
colima ssh -- sudo sysctl -w kernel.perf_event_paranoid=-1
```

This setting persists until reboot. For permanent configuration, add to `/etc/sysctl.conf`:

```bash
kernel.perf_event_paranoid=-1
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
    "arguments": {
        "amount": 99.99,
        "currency": "USD"
    }
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

### Agentless Binary Scanning (No SDK Required)

For applications where SDK integration isn't possible (legacy apps, binaries
with DWARF symbols), Coral can **automatically discover functions** by scanning
the binary:

```bash
# Link a binary without SDK integration
coral connect legacy-app --pid 12345

# Semantic search works identically!
coral debug search --service legacy-app database
# Found: executeQuery, saveToDatabase, dbConnection

# Attach uprobes - no code changes needed
coral debug attach legacy-app --function main.ProcessPayment
```

**How It Works:**

1. Agent discovers binary via PID or explicit link
2. Parses DWARF symbols directly from `/proc/<pid>/exe`
3. Generates AI embeddings for semantic search
4. Caches functions with same performance as SDK mode

**Requirements:**

- Binary must have **symbols** (DWARF preferred for full metadata, `-w` stripped
  works via symbol table)
- Agent must have access to the binary (same host or container namespace)
- **Does NOT work with fully stripped binaries** (`-w -s` - typical production
  builds)

**When to Use SDK vs Agentless:**

| Scenario                                     | SDK                    | Agentless                | Winner                   |
|----------------------------------------------|------------------------|--------------------------|--------------------------|
| **Dev/debug builds** (full symbols)          | ✅ Works                | ✅ Works                  | SDK (easier integration) |
| **Semi-stripped** (`-w` only)                | ✅ Works (symbol table) | ✅ Works (symbol table)   | SDK (easier)             |
| **Fully stripped** (`-w -s`)                 | ✅ **Works (SDK API)**  | ❌ Fails                  | **SDK required**         |
| **Legacy apps** (can't modify code)          | ❌ Can't add            | ✅ Works (if has symbols) | **Agentless only**       |
| **Production deployments** (typical `-w -s`) | ✅ **Works (SDK API)**  | ❌ Fails (no symbols)     | **SDK required**         |

**Performance (when both work):**
| Metric | With SDK | Without SDK (Binary Scanner) |
|--------|----------|------------------------------|
| First discovery | ~1-2s (HTTP) | ~100-200ms (DWARF parse) |
| Cached lookup | ~1ms | ~1ms |
| Semantic search | Identical | Identical |
| Function count | 50k+ | 50k+ |
| **Works with `-w`** | ✅ Yes (symbol table) | ✅ Yes (symbol table) |
| **Works with `-w -s`** | ✅ **Yes (SDK API)** | ❌ **No (needs symbols)** |

**Key Insight:** SDK is required for typical production deployments (which use
`-w -s`). The SDK provides its own metadata API that bypasses the need for debug
symbols. Agentless mode is best for development builds and legacy applications
where SDK integration isn't possible.

### Security Considerations

**With SDK:**

- Debug server listens only on **localhost** (127.0.0.1)
- Only accessible by local agents (not exposed to network)
- Read-only access to function metadata
- No ability to modify application state

**Agentless Mode:**

- No network exposure (reads directly from filesystem/proc)
- Requires agent to run on same host or with container access
- Read-only binary analysis via DWARF symbols
- Uses nsenter for cross-namespace access in daemonset deployments

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
