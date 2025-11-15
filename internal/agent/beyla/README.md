# Beyla Integration for RED Metrics Collection

This package implements RFD 032 - Beyla Integration for RED Metrics Collection.

## Overview

Beyla is an OpenTelemetry eBPF-based auto-instrumentation tool that provides production-ready RED (Rate, Errors, Duration) metrics collection for HTTP, gRPC, databases, and message queues. This integration embeds Beyla within Coral agents to provide:

- **Passive observability**: No code changes required in target applications
- **Production-ready instrumentation**: Battle-tested protocol parsers and kernel compatibility
- **Broad protocol support**: HTTP/1.1, HTTP/2, gRPC, Kafka, Redis, PostgreSQL, MySQL
- **Multi-runtime support**: Go, Java, Python, Node.js, .NET, Ruby, Rust

## Architecture

```
Target Apps → Beyla (eBPF) → OTLP metrics/traces → Agent OTLP Receiver →
Coral Aggregator → Colony (gRPC) → DuckDB
```

The integration consists of three main components:

1. **Manager** (`manager.go`): Manages Beyla lifecycle within the agent
2. **Transformer** (`transformer.go`): Converts OTLP data to Coral's internal format
3. **Configuration**: Agent configuration schema for Beyla settings

## Current Status

**Implementation Status**: Phase 1 Complete (OTLP Receiver Integration)

This implementation is progressing in phases:

- ✅ Protobuf definitions for Beyla metrics and traces
- ✅ Manager lifecycle management
- ✅ Configuration schema and examples
- ✅ DuckDB schema for storage
- ✅ Agent integration hooks
- ✅ Unit tests
- ✅ **Phase 1: OTLP receiver integration** (using RFD 025 infrastructure)
- ✅ **Trace data consumer** (polls storage, transforms to BeylaTrace format)
- ⚠️ Beyla Go library integration (waiting for library availability)
- ⚠️ Metrics support (requires extending RFD 025 OTLPReceiver)
- ⚠️ Transformation layer (stub)

## Dependencies

This implementation integrates with:

1. **RFD 025** (Basic OpenTelemetry Ingestion) - ✅ Integrated
   - Provides OTLP receiver infrastructure (HTTP/gRPC endpoints)
   - Storage backend for local span retention (~1 hour)
   - Query API for retrieving filtered spans
2. **Beyla Go library** - ⚠️ Pending
   - Will be integrated when the official OpenTelemetry eBPF project structure is finalized
3. **Database** - Required for OTLP receiver
   - DuckDB for local agent storage (~1 hour retention)
   - DuckDB for Colony storage (long-term summaries)

## Usage

### Configuration

See `examples/beyla-agent-config.yaml` for a complete configuration example:

```yaml
beyla:
    enabled: true

    discovery:
        services:
            - name: "checkout-api"
              open_port: 8080

    protocols:
        http:
            enabled: true
        grpc:
            enabled: true
        sql:
            enabled: true

    attributes:
        environment: "production"
        colony_id: "colony-abc123"

    sampling:
        rate: 1.0

    otlp_endpoint: "localhost:4318"
```

### Agent Integration

```go
import (
    "database/sql"
    "github.com/coral-io/coral/internal/agent/beyla"
    _ "github.com/marcboeker/go-duckdb"
)

// Database is required for OTLP receiver
db, err := sql.Open("duckdb", "/path/to/agent.db")
if err != nil {
    log.Fatal(err)
}
defer db.Close()

beylaConfig := &beyla.Config{
    Enabled:      true,
    OTLPEndpoint: "localhost:4318",
    SamplingRate: 1.0,
    DB:           db,  // Required for OTLP receiver
    Discovery: beyla.DiscoveryConfig{
        OpenPorts: []int{8080},
    },
    Protocols: beyla.ProtocolsConfig{
        HTTPEnabled: true,
        GRPCEnabled: true,
    },
}

agent, err := agent.New(agent.Config{
    AgentID:     "my-agent",
    BeylaConfig: beylaConfig,
    Logger:      logger,
})

// Start agent (also starts Beyla)
agent.Start()

// Access Beyla manager
beylaManager := agent.GetBeylaManager()
capabilities := beylaManager.GetCapabilities()

// Read traces from channel
tracesCh := beylaManager.GetTraces()
for trace := range tracesCh {
    log.Printf("Received trace: %s from service %s", trace.TraceID, trace.ServiceName)
}
```

## Database Schema

The implementation adds four new tables to the Colony DuckDB:

1. **beyla_http_metrics**: HTTP request RED metrics
2. **beyla_grpc_metrics**: gRPC method-level metrics
3. **beyla_sql_metrics**: Database query performance
4. **beyla_traces**: Distributed trace spans (OpenTelemetry-compatible)

See `internal/colony/database/schema.go` for the complete schema.

## Testing

Run tests:

```bash
make test
# or
go test ./internal/agent/beyla/...
```

Tests cover:
- Manager lifecycle (start/stop)
- Configuration validation
- Capabilities reporting
- Agent integration
- Channel-based data flow

## Implementation Details

### Phase 1: OTLP Receiver Integration (✅ Complete)

The Beyla manager now integrates with RFD 025's OTLP receiver infrastructure:

1. **OTLP Receiver**:
   - Creates `telemetry.OTLPReceiver` on startup
   - Listens on `localhost:4318` (HTTP) and `localhost:4317` (gRPC)
   - Stores spans in local DuckDB database (~1 hour retention)

2. **Trace Consumer**:
   - Polls storage every 5 seconds for new spans
   - Transforms OTLP spans to `BeylaTrace` format
   - Forwards to traces channel for downstream consumers

3. **Data Flow**:
   ```
   Beyla (eBPF) → OTLP HTTP/gRPC → OTLPReceiver → Storage (DuckDB) →
   consumeTraces() → BeylaTrace channel → Colony
   ```

### Next Steps

To complete the implementation:

1. **✅ Integrate OTLP Receiver** (RFD 025) - COMPLETE
   - ✅ Implemented `startOTLPReceiver()` in `manager.go`
   - ✅ Integrated with RFD 025 telemetry package
   - ✅ Added trace consumer goroutine

2. **⚠️ Integrate Beyla Go Library** (waiting for library):
   - Add Beyla dependency when OTEL project structure is finalized
   - Implement actual Beyla startup in `startBeyla()`
   - Configure Beyla via Go API

3. **⚠️ Extend RFD 025 for Metrics**:
   - Add OTLP metrics support to OTLPReceiver
   - Create metrics consumer similar to trace consumer
   - Transform OTLP metrics to Beyla metric types

4. **⚠️ Implement Transformation Layer**:
   - Complete `transformer.go` OTLP-to-Coral conversion
   - Map OTLP metric names to Beyla types
   - Extract histogram buckets and attributes

5. **⚠️ Add Data Pipeline**:
   - Stream metrics/traces from Beyla to Colony
   - Implement aggregation and storage
   - Add query APIs for RED metrics

6. **⚠️ CLI Integration**:
   - Add `coral query beyla` commands
   - Integrate with `coral ask` AI queries
   - Add trace visualization

## References

- [RFD 032](../../../RFDs/032-beyla-red-metrics-integration.md) - Full specification
- [RFD 025](../../../RFDs/025-basic-otel-ingestion.md) - OTLP receiver infrastructure
- [OpenTelemetry eBPF Project](https://github.com/open-telemetry/opentelemetry-ebpf) - Beyla repository
