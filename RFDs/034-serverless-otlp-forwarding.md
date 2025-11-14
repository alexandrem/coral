---
rfd: "034"
title: "Serverless OTLP Forwarding with Regional Agents"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
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
│  │ (ECS/EKS, 2+ replicas)     │           │
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

## Deployment Patterns

### AWS Lambda + PrivateLink

**Lambda function configuration:**

```python
# Python Lambda example
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter

# Primary observability (Honeycomb)
exporter_honeycomb = OTLPSpanExporter(
    endpoint="https://api.honeycomb.io"
)

# Coral regional forwarder (via VPC PrivateLink)
exporter_coral = OTLPSpanExporter(
    endpoint="coral-forwarder-us-east-1.vpce-abc123.us-east-1.vpce.amazonaws.com:4317",
    insecure=True  # Within VPC
)

tracer_provider.add_span_processor(BatchSpanProcessor(exporter_honeycomb))
tracer_provider.add_span_processor(BatchSpanProcessor(exporter_coral))
```

**Infrastructure (Terraform):**

```hcl
# Deploy forwarder on ECS Fargate
resource "aws_ecs_service" "coral_forwarder" {
  name            = "coral-forwarder-us-east-1"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.forwarder.arn
  desired_count   = 2  # HA

  network_configuration {
    subnets         = var.private_subnets
    security_groups = [aws_security_group.forwarder.id]
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.otlp.arn
    container_name   = "coral-forwarder"
    container_port   = 4317
  }
}

# Expose via PrivateLink
resource "aws_vpc_endpoint_service" "coral_forwarder" {
  acceptance_required        = false
  network_load_balancer_arns = [aws_lb.forwarder.arn]

  tags = {
    Name = "coral-forwarder-us-east-1"
  }
}
```

### Google Cloud Run + Private Service Connect

**Cloud Run function configuration:**

```javascript
// Node.js Cloud Run example
const { OTLPTraceExporter } = require('@opentelemetry/exporter-trace-otlp-grpc');

// Coral forwarder (via Private Service Connect)
const coralExporter = new OTLPTraceExporter({
  url: 'coral-forwarder.us-central1.psc.google.com:4317',
  credentials: grpc.credentials.createInsecure() // Within VPC
});

provider.addSpanProcessor(new BatchSpanProcessor(coralExporter));
```

**Infrastructure (Terraform):**

```hcl
# Deploy forwarder on Cloud Run
resource "google_cloud_run_service" "coral_forwarder" {
  name     = "coral-forwarder"
  location = "us-central1"

  template {
    spec {
      containers {
        image = "coral-io/agent:latest"
        args  = [
          "agent", "serve",
          "--mode=forwarder",
          "--telemetry.enabled=true",
          "--telemetry.endpoint=0.0.0.0:4317",
          "--colony.endpoint=${var.colony_mesh_ip}:9000"
        ]
        ports {
          container_port = 4317
        }
      }
    }
  }
}

# Expose via Private Service Connect
resource "google_compute_service_attachment" "coral_forwarder" {
  name        = "coral-forwarder-psc"
  description = "Coral OTLP forwarder for Cloud Functions"
  target_service = google_compute_forwarding_rule.forwarder.self_link
  connection_preference = "ACCEPT_AUTOMATIC"
}
```

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
                // Full agent with eBPF + OTLP
                return startFullAgent(cfg)
            case "otel-collector":
                // K8s collector (RFD 025)
                return startOTLPCollector(cfg)
            case "forwarder":
                // Serverless forwarder (RFD 034)
                return startForwarder(cfg)
            default:
                return fmt.Errorf("unknown mode: %s", mode)
            }
        },
    }

    cmd.Flags().StringVar(&mode, "mode", "agent", "Agent mode: agent, otel-collector, forwarder")
    return cmd
}

func startForwarder(cfg *config.AgentConfig) error {
    // Start OTLP receiver (no eBPF)
    receiver := telemetry.NewReceiver(cfg.Telemetry, logger)
    receiver.Start()

    // Connect to colony via WireGuard
    meshClient := connectToColony(cfg)

    // Forward telemetry buckets
    go func() {
        for {
            buckets := receiver.GetBuckets()
            meshClient.IngestTelemetry(buckets)
            time.Sleep(1 * time.Minute)
        }
    }()

    return nil
}
```

### 2. Configuration

Forwarder configuration extends RFD 025's telemetry config:

```yaml
# forwarder-us-east-1.yaml
agent:
  mode: forwarder
  region: us-east-1

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
  private_key: ${WIREGUARD_PRIVATE_KEY}
  public_key: ${WIREGUARD_PUBLIC_KEY}

discovery:
  enabled: true
  endpoint: "https://discovery.coral.io"
```

### 3. Deployment Automation

Provide Terraform modules for common platforms:

```
deployments/serverless/
  ├── aws-lambda/
  │   ├── main.tf          # ECS Fargate forwarder
  │   ├── vpc_endpoint.tf  # PrivateLink setup
  │   └── variables.tf
  ├── gcp-cloud-run/
  │   ├── main.tf          # Cloud Run forwarder
  │   ├── psc.tf           # Private Service Connect
  │   └── variables.tf
  └── azure-functions/
      ├── main.tf          # Container Instances forwarder
      └── private_link.tf  # Azure Private Link
```

## Implementation Plan

### Phase 1: Forwarder Mode

- [ ] Add `--mode=forwarder` flag to agent
- [ ] Implement forwarder startup logic (OTLP receiver only, no eBPF)
- [ ] Test local forwarder: app → forwarder → colony

### Phase 2: AWS Integration

- [ ] Terraform module for ECS Fargate deployment
- [ ] PrivateLink endpoint configuration
- [ ] Test Lambda → VPC endpoint → forwarder → colony
- [ ] Documentation for AWS setup

### Phase 3: GCP Integration

- [ ] Terraform module for Cloud Run deployment
- [ ] Private Service Connect configuration
- [ ] Test Cloud Run function → PSC → forwarder → colony
- [ ] Documentation for GCP setup

### Phase 4: Azure Integration (Optional)

- [ ] Terraform module for Azure Container Instances
- [ ] Azure Private Link configuration
- [ ] Test Azure Functions → Private Link → forwarder → colony
- [ ] Documentation for Azure setup

### Phase 5: Endpoint Discovery

- [ ] Extend discovery service (RFD 001/023) to return forwarder endpoints
- [ ] Functions query discovery at startup: "Give me nearest OTLP endpoint"
- [ ] Discovery returns region-specific forwarder URL
- [ ] Eliminate hardcoded endpoints from function code

## Security Considerations

- **VPC isolation**: Forwarders accept traffic only from VPC private endpoints, not the public internet
- **Authentication**: Forwarders authenticate to colony using step-ca client certificates (RFD 022)
- **Encryption**: All traffic encrypted via WireGuard mesh
- **PII handling**: Same 24-hour TTL and filtering rules as RFD 025
- **Access control**: VPC endpoint policies restrict which Lambda functions can connect

## Operational Model

**Deployment:**
- One forwarder cluster per region per colony
- Example: `coral-forwarder-us-east-1-prod`, `coral-forwarder-eu-west-1-prod`

**Scaling:**
- Horizontal scaling based on OTLP request rate
- Typical: 2-3 replicas per region for HA
- Autoscaling trigger: CPU > 70% or request queue depth

**Monitoring:**
- Forwarders emit their own telemetry (meta-observability)
- Metrics: OTLP requests/sec, forwarding latency, colony connectivity
- Alerts: Colony connection loss, high rejection rate

**Cost:**
- AWS: ~$50/month per region (2 ECS Fargate tasks + NLB + PrivateLink)
- GCP: ~$40/month per region (Cloud Run + PSC)
- Scales with function invocation volume

## Relationship to Other RFDs

- **RFD 025**: Forwarders reuse OTLP ingestion, filtering, and aggregation logic
- **RFD 022**: Forwarders authenticate to colony via step-ca certificates
- **RFD 023**: Discovery service can provide forwarder endpoints (Phase 5)
- **RFD 027**: Serverless functions cannot use sidecars (different concern)

## Future Enhancements

- **Multi-region failover**: If regional forwarder is down, fall back to nearest healthy region
- **Edge locations**: Deploy forwarders in edge regions (CloudFront Edge Locations, Cloudflare Workers) for ultra-low latency
- **Protocol translation**: Support AWS Kinesis, GCP Pub/Sub as alternative ingestion methods
- **Batching optimization**: Intelligent batching based on function invocation patterns

## Pattern Comparison

| Pattern         | Use Case   | Endpoint                        | Overhead     | HA    |
|-----------------|------------|---------------------------------|--------------|-------|
| **localhost**   | Native/VM  | `localhost:4317`                | 50MB/host    | N/A   |
| **K8s Service** | Kubernetes | `coral-otel.namespace:4317`     | 150MB total  | ✅ Yes |
| **Regional**    | Serverless | `forwarder-region.vpc.aws:4317` | 100MB/region | ✅ Yes |

Serverless forwarders are the only viable option for Lambda/Cloud Run/Azure Functions, as these platforms do not support persistent agents or eBPF instrumentation.
