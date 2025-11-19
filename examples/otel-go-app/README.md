# OpenTelemetry Go Application Example

This example demonstrates how to integrate a Go application with OpenTelemetry instrumentation and the Coral agent for distributed tracing and metrics collection.

## Overview

This example includes:

- **Sample Go application** with HTTP endpoints instrumented using OpenTelemetry
- **Coral agent** configured to receive OTLP data
- **Docker Compose** setup connecting both components

The application exports traces and metrics to the Coral agent's OTLP endpoint (`localhost:4317`), which then processes and stores the telemetry data for AI-driven correlation.

## Architecture

```
┌─────────────────────────────────────────┐
│ Docker Network                          │
│                                         │
│  ┌──────────────────┐                   │
│  │  otel-demo-app   │                   │
│  │  (port 8080)     │                   │
│  │                  │                   │
│  │  OTLP → localhost:4317              │
│  └──────────────────┘                   │
│           ↓                             │
│  ┌──────────────────┐                   │
│  │  coral-agent     │                   │
│  │  (shares network)│                   │
│  │  OTLP receiver:  │                   │
│  │  - 0.0.0.0:4317  │                   │
│  │  - 0.0.0.0:4318  │                   │
│  └──────────────────┘                   │
└─────────────────────────────────────────┘
```

## Application Features

The demo application includes several HTTP endpoints that demonstrate different instrumentation patterns:

### Endpoints

- **`GET /`** - Home page with endpoint documentation
- **`GET /api/users`** - Simulates a database query (20-50ms latency)
- **`GET /api/products`** - Simulates cache lookup + database query (35-85ms latency)
- **`POST /api/checkout`** - Multi-step checkout process with varying latency and occasional errors
- **`GET /health`** - Health check endpoint (no instrumentation)

### OpenTelemetry Instrumentation

The application demonstrates:

1. **Distributed Tracing**
   - Automatic span creation for HTTP requests
   - Nested spans for database queries, cache lookups, and business logic
   - Proper span context propagation
   - Error tracking and status codes

2. **Metrics Collection**
   - Request counter (`http.server.requests`)
   - Request duration histogram (`http.server.duration`)
   - Attributes for method, route, and status code

3. **Resource Attributes**
   - Service name: `otel-demo-app`
   - Service version: `1.0.0`
   - Environment: `demo`

## Running the Example

### Prerequisites

- Docker and Docker Compose
- Coral discovery service running (or set `CORAL_DISCOVERY_ENDPOINT`)
- Coral colony credentials in `~/.coral/` (optional, for mesh connectivity)

### Start the Services

```bash
cd examples/otel-go-app
docker-compose up --build
```

This will:
1. Build the Go application
2. Build the Coral agent (from the root directory)
3. Start both services with the agent sharing the app's network namespace

### Generate Sample Traffic

Once running, you can generate traffic to see traces and metrics:

```bash
# Home page
curl http://localhost:8080/

# Fetch users
curl http://localhost:8080/api/users

# Fetch products
curl http://localhost:8080/api/products

# Trigger checkout (may occasionally fail with 500 errors)
curl -X POST http://localhost:8080/api/checkout

# Load test with multiple requests
for i in {1..20}; do
  curl -s http://localhost:8080/api/users > /dev/null &
  curl -s http://localhost:8080/api/products > /dev/null &
  curl -s -X POST http://localhost:8080/api/checkout > /dev/null &
done
wait
```

### Viewing Telemetry Data

The Coral agent will receive and process the OTLP data. You can verify this by checking the agent logs:

```bash
docker-compose logs coral-agent | grep -i otlp
```

You should see log entries like:
```
OTLP receiver started
Processed OTLP trace export received=X filtered=Y stored=Z
```

## Configuration

### Environment Variables

The `docker-compose.yml` supports the following environment variables:

- **`CORAL_COLONY_ID`** - Colony identifier (default: `otel-demo`)
- **`CORAL_DISCOVERY_ENDPOINT`** - Discovery service endpoint
- **`HOST_CORAL_DIR`** - Host directory with Coral credentials (default: `~/.coral`)
- **`OTEL_EXPORTER_OTLP_ENDPOINT`** - OTLP endpoint for the app (default: `localhost:4317`)

### Agent Telemetry Configuration

The agent has telemetry **enabled by default**. No special flags are required:

```bash
coral agent start  # OTLP receiver starts automatically
```

The agent exposes:
- **gRPC endpoint**: `0.0.0.0:4317` (default)
- **HTTP endpoint**: `0.0.0.0:4318` (default)

To disable telemetry, set `telemetry.disabled: true` in the agent config or use the environment variable `CORAL_TELEMETRY_DISABLED=true`.

## Understanding the Code

### Main Components

1. **`main.go`** - Application entry point with:
   - OpenTelemetry SDK initialization
   - OTLP exporter configuration
   - HTTP server setup
   - Request instrumentation middleware

2. **`initOTel()`** - Initializes the OpenTelemetry SDK:
   - Creates resource with service metadata
   - Sets up OTLP gRPC exporter
   - Configures trace and meter providers
   - Returns shutdown function for cleanup

3. **`instrumentHandler()`** - Middleware that:
   - Creates spans for each HTTP request
   - Records metrics (counter and histogram)
   - Captures HTTP status codes and errors
   - Propagates context to handlers

### Key OpenTelemetry Patterns

**Creating Spans:**
```go
ctx, span := tracer.Start(ctx, "operation-name",
    trace.WithSpanKind(trace.SpanKindServer),
    trace.WithAttributes(
        attribute.String("key", "value"),
    ),
)
defer span.End()
```

**Recording Metrics:**
```go
requestCounter.Add(ctx, 1,
    metric.WithAttributes(attrs...))

requestDuration.Record(ctx, durationMs,
    metric.WithAttributes(attrs...))
```

**Error Handling:**
```go
if err != nil {
    span.SetAttributes(attribute.Bool("error", true))
    // Handle error
}
```

## Testing Integration with Coral

This example demonstrates the OTLP integration described in **RFD 025: OpenTelemetry Ingestion for Observability Correlation**.

The agent will:
1. Receive OTLP traces on `localhost:4317`
2. Apply static filtering (errors, high latency, sampling)
3. Store filtered spans locally with ~1 hour retention
4. Forward aggregated data to the colony (when connected)

You can query the collected telemetry through the Coral CLI (when the colony is running):

```bash
coral ask "Why is checkout slow?"
coral ask "What are the error rates for the demo app?"
```

## Troubleshooting

### Agent not receiving traces

1. Check that the agent is running and listening:
   ```bash
   docker-compose logs coral-agent | grep "OTLP receiver listening"
   ```

2. Verify the app can reach the endpoint:
   ```bash
   docker-compose exec otel-demo-app nc -zv localhost 4317
   ```

3. Check the app logs for connection errors:
   ```bash
   docker-compose logs otel-demo-app
   ```

### No traces visible

The agent applies filtering by default. Traces may be filtered out if they don't match criteria:
- Error spans are always captured
- High latency spans (>500ms) are always captured
- Normal spans are sampled at 10%

Try generating some errors or high-latency requests:
```bash
# Checkout has ~15% error rate per step
for i in {1..20}; do curl -X POST http://localhost:8080/api/checkout; done
```

## Cleanup

Stop and remove the containers:

```bash
docker-compose down
```

Remove volumes (if needed):

```bash
docker-compose down -v
```

## Related Documentation

- **RFD 025**: OpenTelemetry Ingestion for Observability Correlation
- **RFD 032**: Beyla RED Metrics Integration
- **Internal Agent Docs**: `/internal/agent/telemetry/`
