---
rfd: "034"
title: "Serverless OTLP Forwarding with Regional Agents"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "025", "022", "023" ]
areas: [ "observability", "serverless", "networking" ]
---

# RFD 034 - Serverless OTLP Forwarding with Regional Agents

**Status:** 🚧 Draft

## Summary

Enable Coral to observe serverless workloads (AWS Lambda, Google Cloud Run, Azure Functions) by deploying regional OTLP forwarders that receive telemetry from functions via VPC private endpoints and relay it to colonies over WireGuard mesh. This extends RFD 025's OTLP ingestion to environments where persistent agents cannot run.

## Problem

- **Current behavior/limitations**:
    - Serverless functions cannot run persistent Coral agents (Lambda/Cloud Run are stateless, ephemeral)
    - Functions have outbound-only networking with no ability to receive incoming connections
    - eBPF is unavailable in Lambda, Cloud Run, and Fargate
    - Teams with mixed architectures (VMs + Kubernetes + serverless) cannot get unified Coral insights across their entire stack

- **Why this matters**:
    - Modern deployments increasingly use serverless (30-50% of workloads in cloud-native environments)
    - Serverless-heavy architectures currently have zero Coral visibility
    - Functions already emit OpenTelemetry data; teams need a way to funnel it to their Coral colony
    - Manual correlation between serverless traces and infrastructure metrics is time-consuming

- **Use cases affected**:
    - "Why is the Lambda checkout handler slow?" queries that need both application traces and downstream infrastructure metrics
    - Event-driven architectures where Lambda triggers impact services monitored by Coral
    - Multi-region serverless deployments requiring local collection with centralized analysis

## Solution

Deploy **regional agent forwarders** in each cloud region where serverless workloads run. These are lightweight, stateless Coral agents running in `--mode=forwarder` that:

1. Accept OTLP data from Lambda/Cloud Run functions via VPC private endpoints
2. Apply the same static filtering and aggregation as regular agents (RFD 025)
3. Forward aggregated telemetry buckets to the colony via WireGuard mesh
4. Scale horizontally and operate without local state

**Architecture:**

```
┌────────────────────────────────────────────┐
│ AWS us-east-1 VPC                          │
│  ┌──────────────┐   ┌──────────────┐      │
│  │ Lambda 1     │   │ Lambda 2     │      │
│  └─────┬────────┘   └─────┬────────┘      │
│        │                  │                │
│        └──────────────────┘                │
│                │                           │
│    VPC Endpoint (PrivateLink)              │
│                │                           │
│  ┌─────────────▼──────────────┐           │
│  │ Regional Agent Forwarder   │           │
│  │ (Container runtime)        │           │
│  └─────────────┬──────────────┘           │
└────────────────┼───────────────────────────┘
                 │
                 │ WireGuard mesh
                 ▼
     ┌────────────────────────┐
     │       Colony           │
     └────────────────────────┘
```

**Key Design Decisions:**

1. **Regional deployment**: One forwarder cluster per cloud region (e.g., us-east-1, eu-west-1) to minimize latency
2. **VPC private endpoints**: Functions reach forwarders via PrivateLink (AWS) or Private Service Connect (GCP), keeping traffic private
3. **Stateless and scalable**: Forwarders are horizontally scalable with no local storage; they immediately forward to colony
4. **Same filtering/aggregation**: Reuse RFD 025's static filtering and 1-minute bucketing logic
5. **Mesh connectivity**: Forwarders join the WireGuard mesh like regular agents, authenticate via step-ca (RFD 022)

## Application Integration

### AWS Lambda

**Lambda function configuration:**

```python
# Python Lambda example
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.trace.export import BatchSpanProcessor

# Primary observability (Honeycomb)
exporter_honeycomb = OTLPSpanExporter(
    endpoint="https://api.honeycomb.io"
)

# Coral regional forwarder (via VPC PrivateLink)
# Endpoint URL provided by VPC endpoint DNS
exporter_coral = OTLPSpanExporter(
    endpoint=os.getenv("CORAL_OTLP_ENDPOINT"),  # e.g., "vpce-abc123.execute-api.us-east-1.vpce.amazonaws.com:4317"
    insecure=True  # Within VPC
)

tracer_provider.add_span_processor(BatchSpanProcessor(exporter_honeycomb))
tracer_provider.add_span_processor(BatchSpanProcessor(exporter_coral))
```

**Requirements:**
- Lambda must be attached to a VPC with access to the VPC endpoint
- VPC endpoint must be configured to route to the forwarder service
- Environment variable `CORAL_OTLP_ENDPOINT` set to VPC endpoint DNS name

### Google Cloud Run / Cloud Functions

**Cloud Run function configuration:**

```javascript
// Node.js Cloud Run example
const { OTLPTraceExporter } = require('@opentelemetry/exporter-trace-otlp-grpc');
const grpc = require('@grpc/grpc-js');

// Coral forwarder (via Private Service Connect)
const coralExporter = new OTLPTraceExporter({
  url: process.env.CORAL_OTLP_ENDPOINT,  // PSC endpoint
  credentials: grpc.credentials.createInsecure() // Within VPC
});

provider.addSpanProcessor(new BatchSpanProcessor(coralExporter));
```

**Requirements:**
- Cloud Run service must have VPC connector configured
- Private Service Connect endpoint must be accessible from the VPC
- Environment variable `CORAL_OTLP_ENDPOINT` set to PSC endpoint

### Azure Functions

**Azure Function configuration:**

```csharp
// C# Azure Function example
using OpenTelemetry.Exporter;

var coralExporter = new OtlpExporter(new OtlpExporterOptions
{
    Endpoint = new Uri(Environment.GetEnvironmentVariable("CORAL_OTLP_ENDPOINT")),
    Protocol = OtlpExportProtocol.Grpc
});
```

**Requirements:**
- Function must be integrated with a Virtual Network
- Azure Private Link endpoint configured
- Environment variable `CORAL_OTLP_ENDPOINT` set to Private Link DNS

## Component Changes

### 1. Agent Forwarder Mode

Add `--mode=forwarder` flag to agent binary:

```go
// cmd/coral/agent.go
func NewServeCommand() *cobra.Command {
    var mode string

    cmd := &cobra.Command{
        Use:   "serve",
        Short: "Start Coral agent",
        RunE: func(cmd *cobra.Command, args []string) error {
            switch mode {
            case "agent":
                // Full agent with eBPF + OTLP (RFD 025)
                return startFullAgent(cfg)
            case "otel-collector":
                // K8s collector - OTLP only, no eBPF (RFD 025)
                return startOTLPCollector(cfg)
            case "forwarder":
                // Serverless forwarder - OTLP only, no eBPF, no local storage (RFD 034)
                return startForwarder(cfg)
            default:
                return fmt.Errorf("unknown mode: %s", mode)
            }
        },
    }

    cmd.Flags().StringVar(&mode, "mode", "agent", "Agent mode: agent, otel-collector, forwarder")
    return cmd
}
```

### 2. Forwarder Startup Logic

```go
// internal/agent/forwarder/forwarder.go
package forwarder

import (
    "context"
    "time"

    "github.com/coral/internal/agent/telemetry"
    "github.com/coral/internal/colony/client"
)

// Forwarder is a stateless OTLP forwarder for serverless environments.
type Forwarder struct {
    receiver     *telemetry.Receiver
    colonyClient *client.Client
    logger       *log.Logger
}

func New(cfg *config.Config, logger *log.Logger) (*Forwarder, error) {
    // Initialize OTLP receiver (same as RFD 025)
    receiver, err := telemetry.NewReceiver(cfg.Telemetry, logger)
    if err != nil {
        return nil, fmt.Errorf("failed to create receiver: %w", err)
    }

    // Connect to colony via WireGuard mesh
    colonyClient, err := client.New(cfg.Colony, logger)
    if err != nil {
        return nil, fmt.Errorf("failed to connect to colony: %w", err)
    }

    return &Forwarder{
        receiver:     receiver,
        colonyClient: colonyClient,
        logger:       logger,
    }, nil
}

func (f *Forwarder) Start(ctx context.Context) error {
    // Start OTLP receiver
    if err := f.receiver.Start(ctx); err != nil {
        return fmt.Errorf("failed to start receiver: %w", err)
    }

    // Forward telemetry buckets to colony every minute
    go f.forwardLoop(ctx)

    return nil
}

func (f *Forwarder) forwardLoop(ctx context.Context) {
    ticker := time.NewTicker(1 * time.Minute)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            // Get aggregated buckets from receiver
            buckets := f.receiver.FlushBuckets()
            if len(buckets) == 0 {
                continue
            }

            // Forward to colony
            if err := f.colonyClient.IngestTelemetry(ctx, buckets); err != nil {
                f.logger.Errorf("failed to forward telemetry: %v", err)
                // Buckets are lost; forwarder is stateless
                continue
            }

            f.logger.Infof("forwarded %d buckets to colony", len(buckets))

        case <-ctx.Done():
            return
        }
    }
}
```

### 3. Configuration Schema

```yaml
# forwarder-us-east-1.yaml
agent:
  mode: forwarder
  region: us-east-1  # For discovery service integration

telemetry:
  enabled: true
  endpoint: "0.0.0.0:4317"  # Accept from all VPC IPs
  filters:
    always_capture_errors: true
    latency_threshold_ms: 500
    sample_rate: 0.10

colony:
  endpoint: "10.42.0.1:9000"  # Colony WireGuard mesh IP

wireguard:
  enabled: true
  private_key_file: /etc/coral/wireguard.key
  public_key: ${WIREGUARD_PUBLIC_KEY}

discovery:
  enabled: true
  endpoint: "https://discovery.coral.io"
  bootstrap_token_file: /etc/coral/bootstrap.token
```

**Configuration differences from regular agent:**

| Setting                | Regular Agent      | Forwarder            |
|------------------------|--------------------|----------------------|
| `agent.mode`           | `agent`            | `forwarder`          |
| eBPF collectors        | Enabled            | Disabled             |
| OTLP receiver          | Optional           | Required             |
| Local storage          | Yes (DuckDB)       | No                   |
| WireGuard mesh         | Yes                | Yes                  |
| Telemetry forwarding   | Every 1 minute     | Every 1 minute       |

## Implementation Plan

### Phase 1: Forwarder Mode Implementation

- [ ] Add `--mode=forwarder` flag to agent CLI
- [ ] Implement `forwarder.Forwarder` type (stateless OTLP receiver + colony client)
- [ ] Disable eBPF initialization when `mode=forwarder`
- [ ] Disable local storage (no DuckDB) when `mode=forwarder`
- [ ] Reuse RFD 025's telemetry filtering and aggregation logic
- [ ] Add configuration validation for forwarder mode

### Phase 2: Testing & Documentation

- [ ] Unit tests: forwarder receives OTLP, forwards to colony
- [ ] Integration test: mock Lambda → forwarder → colony
- [ ] E2E test: real Lambda function in test VPC → forwarder → colony
- [ ] Document configuration differences vs. regular agent
- [ ] Document VPC endpoint requirements for each cloud provider

### Phase 3: Endpoint Discovery Integration

- [ ] Extend discovery service (RFD 001/023) to return forwarder endpoints
- [ ] Functions query discovery at startup: "Give me nearest OTLP endpoint for my region"
- [ ] Discovery returns region-specific forwarder VPC endpoint URL
- [ ] Eliminate hardcoded endpoints from function code
- [ ] Document discovery-based configuration pattern

## API Changes

### Configuration Types

```go
// internal/config/schema.go
type AgentConfig struct {
    Mode     string           // "agent", "otel-collector", "forwarder"
    Region   string           // For discovery integration
    // ... existing fields
}

// Validation
func (c *AgentConfig) Validate() error {
    switch c.Mode {
    case "agent", "otel-collector", "forwarder":
        // Valid
    default:
        return fmt.Errorf("invalid mode: %s", c.Mode)
    }

    if c.Mode == "forwarder" {
        if c.Telemetry.Enabled == false {
            return fmt.Errorf("forwarder mode requires telemetry.enabled=true")
        }
        if c.Colony.Endpoint == "" {
            return fmt.Errorf("forwarder mode requires colony.endpoint")
        }
    }

    return nil
}
```

### Forwarder Startup

```go
// cmd/coral/agent.go
func startForwarder(cfg *config.AgentConfig) error {
    logger := log.NewLogger(cfg.LogLevel)

    // Create forwarder (OTLP receiver + colony client)
    fwd, err := forwarder.New(cfg, logger)
    if err != nil {
        return fmt.Errorf("failed to create forwarder: %w", err)
    }

    ctx := context.Background()
    return fwd.Start(ctx)
}
```

## Security Considerations

- **VPC isolation**: Forwarders accept traffic only from VPC private endpoints, not the public internet
- **Authentication**: Forwarders authenticate to colony using step-ca client certificates (RFD 022)
- **Encryption**: All traffic encrypted via WireGuard mesh
- **PII handling**: Same 24-hour TTL and filtering rules as RFD 025
- **Access control**: VPC endpoint policies restrict which Lambda functions/service accounts can connect
- **No local storage**: Forwarders are stateless; no telemetry persisted locally

## Operational Characteristics

**Deployment:**
- One forwarder cluster per region per colony
- Example naming: `coral-forwarder-us-east-1-prod`, `coral-forwarder-eu-west-1-prod`

**Scaling:**
- Horizontal scaling based on OTLP request rate
- Typical: 2-3 replicas per region for high availability
- Forwarders are stateless; can scale up/down without data loss
- Autoscaling trigger: CPU > 70% or request queue depth > threshold

**Monitoring:**
- Forwarders emit their own telemetry to colony (meta-observability)
- Metrics: OTLP requests/sec, forwarding latency, colony connectivity status, bucket drop rate
- Alerts: Colony connection loss, high rejection rate, VPC endpoint unreachable

**Failure Modes:**
- **Forwarder down**: Functions cannot send OTLP; data lost until forwarder recovers (stateless)
- **Colony unreachable**: Forwarder drops buckets; no local buffering
- **VPC endpoint unreachable**: Functions see OTLP export errors; primary observability (Honeycomb) unaffected

## Relationship to Other RFDs

- **RFD 025**: Forwarders reuse OTLP ingestion, filtering, and aggregation logic
- **RFD 022**: Forwarders authenticate to colony via step-ca certificates over WireGuard mesh
- **RFD 023**: Discovery service can provide forwarder endpoints (Phase 3)
- **RFD 027**: Serverless functions cannot use sidecars (different concern)
- **RFD 032**: Beyla cannot run in serverless; forwarders only handle application OTel

## Future Enhancements

- **Multi-region failover**: If regional forwarder is down, discovery service redirects to nearest healthy region
- **Buffering**: Optional local buffering (Redis/Memcached) for temporary colony unavailability
- **Protocol translation**: Support AWS Kinesis, GCP Pub/Sub as alternative ingestion methods
- **Batching optimization**: Intelligent batching based on function invocation patterns
- **Cost optimization**: Adaptive sampling based on function invocation frequency

## Pattern Comparison

| Pattern         | Use Case   | Endpoint                          | Overhead     | HA    | Local State |
|-----------------|------------|-----------------------------------|--------------|-------|-------------|
| **localhost**   | Native/VM  | `localhost:4317`                  | 50MB/host    | N/A   | ✅ Yes       |
| **K8s Service** | Kubernetes | `coral-otel.namespace:4317`       | 150MB total  | ✅ Yes | ❌ No        |
| **Regional**    | Serverless | `vpce-*.vpce.amazonaws.com:4317`  | 100MB/region | ✅ Yes | ❌ No        |

Serverless forwarders are the only viable option for Lambda/Cloud Run/Azure Functions, as these platforms do not support persistent agents or eBPF instrumentation.
