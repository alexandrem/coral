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

**Implementation Status**: Stub implementation

This is a foundational implementation with the following components in place:

- ✅ Protobuf definitions for Beyla metrics and traces
- ✅ Manager lifecycle management (stub)
- ✅ Configuration schema and examples
- ✅ DuckDB schema for storage
- ✅ Agent integration hooks
- ✅ Unit tests
- ⚠️ OTLP receiver integration (requires RFD 025)
- ⚠️ Beyla Go library integration (pending)
- ⚠️ Transformation layer (stub)

## Dependencies

This implementation assumes:

1. **RFD 025** (Basic OpenTelemetry Ingestion) provides the OTLP receiver infrastructure
2. **Beyla Go library** will be integrated when the official OpenTelemetry eBPF project structure is finalized

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
import "github.com/coral-io/coral/internal/agent/beyla"

beylaConfig := &beyla.Config{
    Enabled:      true,
    OTLPEndpoint: "localhost:4318",
    SamplingRate: 1.0,
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

## Next Steps

To complete the implementation:

1. **Integrate OTLP Receiver** (RFD 025):
   - Implement `startOTLPReceiver()` in `manager.go`
   - Use OpenTelemetry collector receiver libraries
   - Forward metrics/traces to channels

2. **Integrate Beyla Go Library**:
   - Add Beyla dependency when OTEL project structure is finalized
   - Implement `startBeyla()` in `manager.go`
   - Configure Beyla via Go API

3. **Implement Transformation Layer**:
   - Complete `transformer.go` OTLP-to-Coral conversion
   - Map OTLP metric names to Beyla types
   - Extract histogram buckets and attributes

4. **Add Data Pipeline**:
   - Stream metrics/traces from Beyla to Colony
   - Implement aggregation and storage
   - Add query APIs for RED metrics

5. **CLI Integration**:
   - Add `coral query beyla` commands
   - Integrate with `coral ask` AI queries
   - Add trace visualization

## References

- [RFD 032](../../../RFDs/032-beyla-red-metrics-integration.md) - Full specification
- [RFD 025](../../../RFDs/025-basic-otel-ingestion.md) - OTLP receiver infrastructure
- [OpenTelemetry eBPF Project](https://github.com/open-telemetry/opentelemetry-ebpf) - Beyla repository
