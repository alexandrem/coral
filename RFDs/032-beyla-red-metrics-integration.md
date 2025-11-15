---
rfd: "032"
title: "Beyla Integration for RED Metrics Collection"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "011", "013", "025" ]
database_migrations: [ "ebpf_beyla_metrics", "ebpf_beyla_traces" ]
areas: [ "observability", "ebpf", "metrics", "tracing" ]
---

# RFD 032 - Beyla Integration for RED Metrics Collection

**Status:** ✅ Implemented

**Date:** 2025-11-15

## Summary

Integrate Beyla, an OpenTelemetry eBPF-based auto-instrumentation tool (
originally developed by Grafana Labs, now donated to the CNCF OpenTelemetry
project), into Coral agents to provide production-ready RED (Rate, Errors,
Duration) metrics collection for HTTP, gRPC, databases, and message queues.
Beyla provides zero-code instrumentation using eBPF kernel probes, automatically
collecting application performance metrics without requiring SDK integration or
code changes.

## Problem

**Current behavior/limitations**

RFD 013 proposes building eBPF instrumentation from scratch, which presents
several challenges:

- Writing production-ready eBPF programs requires deep kernel expertise and
  extensive testing across kernel versions, CPU architectures, and workload
  types.
- Protocol parsers (HTTP/1.1, HTTP/2, gRPC, SQL) are complex to implement
  correctly and maintain as protocols evolve.
- Supporting multiple languages and runtimes (Go, Java, Python, Node.js, Rust,
  C++) requires runtime-specific stack unwinding and symbolization.
- Ensuring safety, performance, and compatibility across diverse production
  environments demands significant engineering effort and long stabilization
  cycles.
- The initial implementation in RFD 013 is a stub with no real eBPF programs
  deployed.

**Why this matters**

- Coral's value proposition depends on **passive, zero-configuration
  observability** that works immediately without SDK integration or code
  changes.
- Distributed incident response requires **reliable, comprehensive RED metrics**
  as the foundation for AI-driven diagnostics ("Why is checkout slow?").
- Users expect **production-grade reliability** from day one, not beta-quality
  instrumentation that requires months of hardening.
- Engineering resources are better spent on **Coral-specific innovations** (
  multi-service correlation, AI orchestration, cross-colony federation) rather
  than reimplementing commodity observability infrastructure.

**Use cases affected**

- Immediate observability for legacy applications, third-party services, or
  polyglot stacks where SDK integration is infeasible.
- AI queries like "Why is payments-api slow?" require accurate latency
  distributions, error rates, and throughput metrics as baseline evidence.
- Real-time performance dashboards and alerting based on service-level metrics.
- Distributed tracing for request flows across microservices (Beyla supports
  OpenTelemetry trace propagation).

## Solution

Embed Beyla as a library component within Coral agents to handle standard RED
metrics collection for common protocols (HTTP/1.1, HTTP/2, gRPC, Kafka, Redis,
PostgreSQL, MySQL). Beyla provides battle-tested, production-ready
instrumentation maintained by the OpenTelemetry community under CNCF governance.

### Integration Architecture

**How Beyla works**:

- Beyla runs as a process/goroutine that instruments target applications using
  eBPF
- It discovers processes to instrument (via port numbers, Kubernetes labels, or
  process names)
- eBPF programs capture protocol-level events (HTTP requests, gRPC calls, SQL
  queries)
- Beyla aggregates events into OpenTelemetry metrics and traces
- Metrics/traces are exported via **OTLP (OpenTelemetry Protocol)** to a
  collector endpoint

**Coral integration approach**:

**Input**: Beyla configuration specifying which processes/services to instrument

- Process discovery rules (port numbers, K8s pod labels, process names)
- Protocol filters (enable HTTP, gRPC, SQL, etc.)
- Sampling rates and cardinality controls

**Processing**: Beyla runs embedded within the Coral agent process

- Started as a goroutine using Beyla's Go library API
- Configured programmatically (not via YAML files)
- Instruments local processes using eBPF

**Output**: Beyla exports OpenTelemetry metrics and traces via OTLP

- **Option A** (recommended): Coral agent runs embedded OTLP receiver to consume
  Beyla's output in-process
- **Option B**: Beyla exports to local OTLP endpoint (e.g., `localhost:4318`),
  agent consumes via HTTP
- Agent transforms OTLP data → Coral's internal format → streams to Colony via
  gRPC

**Data flow**:

```
Target Apps → Beyla (eBPF) → OTLP metrics/traces → Agent OTLP Receiver →
Coral Aggregator → Colony (gRPC) → DuckDB
```

Coral agents will:

1. **Use Beyla for baseline RED metrics**: Leverage Beyla's mature protocol
   parsers, kernel compatibility matrix, and extensive testing.
2. **Supplement with custom eBPF programs**: Add Coral-specific collectors for
   advanced use cases (detailed in a future RFD) such as:
    - Cross-service correlation using WireGuard mesh metadata
    - AI-triggered deep profiling (CPU flamegraphs, memory allocation tracking)
    - Security-focused syscall monitoring and anomaly detection
    - Custom application-specific instrumentation based on user-defined policies

This hybrid approach combines the reliability of a proven tool with the
flexibility to extend observability for Coral's unique distributed architecture.

### Key Design Decisions

- **Beyla as separate process** (⚠️ Updated from original plan): Run Beyla as a
  separate process managed by the Coral agent, rather than embedding as a Go
  library. This approach was chosen because:
    - Beyla's official releases are production-ready binaries with pre-compiled
      eBPF bytecode
    - Infrastructure already in place: OTLP receiver (RFD 025) can consume Beyla
      output
    - Process isolation provides better stability and resource management
    - Lower maintenance burden than forking and maintaining library integration
    - Ability to upgrade Beyla independently of Coral agent
- **Binary distribution via `go generate`**: Beyla binaries are downloaded
  during
  build-time using `go generate` and embedded using platform-specific build
  tags:
    - Script `scripts/download-beyla.sh` fetches binaries from Grafana GitHub
      releases
    - Platform-specific embed files (`embed_linux_amd64.go`,
      `embed_darwin_arm64.go`,
      etc.) use build tags to include only the binary for the target platform
    - Binary extraction to temp directory on agent startup with automatic
      cleanup
      on shutdown
    - Fallback priority: `BEYLA_PATH` env var → embedded binary → system PATH
    - Build integration: `make build` automatically runs `go generate` to
      download
      binaries
- **Pull-based data flow** (RFD 025 architecture): Colony queries agents for
  Beyla metrics on-demand via gRPC RPC, rather than agents pushing data:
    - Agent stores Beyla metrics in local DuckDB (~1 hour retention)
    - Colony periodically polls agents using `QueryBeylaMetrics` RPC
    - Colony stores aggregated metrics in its own DuckDB (30 days retention)
    - Enables distributed querying without central bottleneck
- **Beyla handles commodity protocols**: HTTP, gRPC, Kafka, Redis, SQL databases
  benefit from Beyla's mature parsers and broad runtime support (Go, Java,
  Python, Node.js, Rust, .NET, Ruby).
- **Custom eBPF for Coral innovations**: Use custom programs for features Beyla
  doesn't provide—multi-colony correlation, AI-orchestrated profiling, WireGuard
  tunnel metrics, container runtime insights.
- **Unified data pipeline**: Beyla metrics and custom eBPF outputs flow through
  the same aggregation pipeline, stored in DuckDB, and surfaced via CLI/MCP.
- **Graceful fallback**: If Beyla cannot instrument a workload (e.g.,
  unsupported protocol, kernel version), custom eBPF or userspace polling
  provides partial coverage.
- **OpenTelemetry bridge**: Beyla natively exports OpenTelemetry metrics and
  traces. Coral consumes these via the OTLP receiver infrastructure (RFD 025).

### Benefits

- **Faster time-to-production**: Beyla is production-ready today, supporting 10+
  protocols and 7+ language runtimes. No months-long stabilization cycle.
- **Broad compatibility**: Beyla handles kernel 4.18+ (RHEL 8), 5.8+ (Ubuntu
  20.04), and gracefully degrades on older kernels. Covers 95%+ of production
  Linux environments.
- **CNCF/OpenTelemetry governance**: As part of the OpenTelemetry project under
  CNCF, Beyla benefits from vendor-neutral governance, broad industry adoption,
  and long-term sustainability. The OpenTelemetry community continuously updates
  Beyla for new kernel versions, protocol changes, and runtime updates (e.g., Go
  1.23, Java 21 virtual threads).
- **Rich protocol support**: Out-of-the-box instrumentation for HTTP/1.1,
  HTTP/2, gRPC (unary + streaming), Kafka, Redis (RESP2/RESP3), PostgreSQL,
  MySQL, SQL Server.
- **Native OpenTelemetry integration**: Beyla natively exports OpenTelemetry
  metrics and traces, providing seamless integration with the broader OTEL
  ecosystem and propagating W3C Trace Context and Baggage headers for end-to-end
  trace correlation.
- **Resource efficiency**: Beyla uses CO-RE (Compile Once, Run Everywhere) eBPF
  programs, minimizing memory footprint and CPU overhead (<2% in typical
  workloads).
- **Focus engineering on differentiation**: Coral team can prioritize AI
  orchestration, multi-colony federation, and advanced correlation instead of
  reinventing protocol parsers.

### Architecture Overview

```
┌───────────────────────────────────────────────────────────────┐
│ Host / Container                                              │
│                                                               │
│  ┌─────────────────┐         ┌──────────────────┐            │
│  │ Beyla Process   │         │ Coral Agent      │            │
│  │ (separate PID)  │         │                  │            │
│  │                 │         │ ┌──────────────┐ │            │
│  │ • HTTP/gRPC     │         │ │ OTLP Receiver│ │            │
│  │ • Kafka         │         │ │ (localhost:  │ │            │
│  │ • Redis/SQL     │         │ │  4317/4318)  │ │            │
│  │ • eBPF probes   │         │ └──────┬───────┘ │            │
│  └────────┬────────┘         │        │         │            │
│           │ OTLP              │        │         │            │
│           │ localhost:4317    │        ▼         │            │
│           └───────────────────┼─> Transformer    │            │
│                               │        │         │            │
│                               │        ▼         │            │
│                               │  BeylaStorage    │            │
│                               │  (DuckDB local)  │            │
│                               │  ~1hr retention  │            │
│                               │        ▲         │            │
│                               │        │         │            │
│  ┌──────────────────┐         │ QueryBeylaMetrics RPC        │
│  │ Custom eBPF Mgr  │         │   (pull-based)   │            │
│  │ • WireGuard stats│─────────┼────────┼─────────┤            │
│  │ • AI profiling   │         │        │         │            │
│  └──────────────────┘         └────────┼─────────┘            │
│                                        │                      │
└────────────────────────────────────────┼──────────────────────┘
                                         │
                                         │ gRPC/WireGuard mesh
                                         ▼
                              ┌──────────────────────┐
                              │ Colony               │
                              │  • Polls agents      │
                              │  • DuckDB (30d)      │
                              │  • Serves AI queries │
                              └──────────────────────┘
```

**Implementation Details (as of commit 82afd14):**

- **Beyla Process**: Managed by agent via `os/exec`, logs integrated with
  zerolog
- **Binary Embedding**: Platform-specific via build tags (
  `embed_linux_amd64.go`, etc.)
- **Local Storage**: `beyla_http_metrics_local`, `beyla_grpc_metrics_local`,
  `beyla_sql_metrics_local` tables
- **Pull Queries**: `QueryBeylaMetrics` RPC defined in
  `proto/coral/agent/v1/agent.proto`
- **Cleanup**: Automatic configurable retention cleanup loop in agent

---

## Implementation Status

**Core Capability:** ✅ Complete

Agents run Beyla as a separate process, collect RED metrics via OTLP, store
metrics locally in DuckDB with configurable retention, and respond to Colony
queries via `QueryBeylaMetrics` RPC. Colony polls agents periodically, stores
aggregated metrics with 30-day retention (HTTP/gRPC) or 14-day retention (SQL).

**Architecture Note:** Implemented as process-based (Beyla binary managed by
agent) rather than library-based, and follows pull-based architecture (Colony
queries agents) rather than push-based, aligning with Coral's distributed
storage model.

### Component Changes

1. **Agent** (`internal/agent/beyla/`):
    - Runs Beyla as separate process with embedded binary distribution
    - OTLP receiver consumes Beyla metrics via localhost:4317/4318
    - Local DuckDB storage with configurable retention (default: 1 hour)
    - Exposes `QueryBeylaMetrics` RPC for Colony to pull metrics
    - Supports HTTP, gRPC, and SQL protocol metrics

2. **Colony** (`internal/colony/beyla_poller.go`,
   `internal/colony/database/beyla.go`):
    - Periodic poller queries all agents via `QueryBeylaMetrics` RPC
    - DuckDB storage with 30-day retention (HTTP/gRPC) and 14-day retention (
      SQL)
    - Automatic cleanup of expired metrics

3. **Protobuf API** (`proto/coral/agent/v1/agent.proto`):
    - New `QueryBeylaMetrics` RPC with filtering by time range, service names,
      and metric types
    - New message types: `BeylaHttpMetric`, `BeylaGrpcMetric`, `BeylaSqlMetric`

**Configuration Example:**

```yaml
# agent-config.yaml excerpt
beyla:
    enabled: true

    # Discovery: which processes to instrument
    discovery:
        services:
            -   name: "checkout-api"
                open_port: 8080           # Instrument process listening on this port
                k8s_pod_name: "checkout-*" # Kubernetes pod name pattern
            -   name: "payments-api"
                k8s_namespace: "prod"
                k8s_pod_label:
                    app: "payments"

    # Protocol-specific configuration
    protocols:
        http:
            enabled: true
            capture_headers: false      # Privacy: don't store header values
            route_patterns: # Cardinality reduction
                - "/api/v1/users/:id"
                - "/api/v1/orders/:id"
        grpc:
            enabled: true
        sql:
            enabled: true
            obfuscate_queries: true     # Replace literals with "?"

    # Attributes to add to all metrics/traces
    attributes:
        environment: "production"
        cluster: "us-west-2"
        colony_id: "colony-abc123"

    # Performance tuning
    sampling:
        rate: 1.0                     # 100% sampling (adjust if overhead too high)

    # Local storage retention (hours)
    storage_retention_hours: 1        # How long to keep metrics in agent's local DuckDB
    # Should be >= colony poll interval (default: 1 hour)

    # Resource limits
    limits:
        max_traced_connections: 1000  # Prevent memory exhaustion
        ring_buffer_size: 65536

# Custom eBPF collectors (supplementing Beyla)
ebpf:
    enabled: true
    custom_collectors:
        -   name: wireguard_tunnel_stats
            mode: continuous
        -   name: ai_deep_profiler
            mode: on_demand
```

---

### Deployment Examples

Beyla works in any environment where the agent has eBPF capabilities. Here are
common deployment patterns:

**Docker Compose**:

```yaml
# docker-compose.yml
version: '3.8'
services:
    # Your application services
    web:
        image: myapp:latest
        ports:
            - "8080:8080"

    api:
        image: myapi:latest
        ports:
            - "9090:9090"

    postgres:
        image: postgres:15
        ports:
            - "5432:5432"

    # Coral agent with Beyla
    coral-agent:
        image: coral/agent:latest
        privileged: true  # Required for eBPF (or use cap_add: [SYS_ADMIN, BPF])
        network_mode: "host"  # Access to host network for process discovery
        pid: "host"  # Access to host PID namespace
        volumes:
            - /sys/kernel/debug:/sys/kernel/debug:ro  # For eBPF
            - ./agent-config.yaml:/etc/coral/agent.yaml
        environment:
            - COLONY_URL=https://colony.example.com
            - AGENT_ID=docker-compose-stack
        command:
            - --config=/etc/coral/agent.yaml
            - --discover=ports:8080,9090,5432  # Discover services by port
```

**Agent config for docker-compose** (`agent-config.yaml`):

```yaml
beyla:
    enabled: true
    discovery:
        services:
            -   name: "web"
                open_port: 8080
            -   name: "api"
                open_port: 9090
            -   name: "postgres"
                open_port: 5432
    protocols:
        http:
            enabled: true
        sql:
            enabled: true
    attributes:
        environment: "local"
        stack: "docker-compose"
```

**systemd Service**:

```ini
# /etc/systemd/system/coral-agent.service
[Unit]
Description=Coral Agent with Beyla
After=network.target

[Service]
Type=simple
User=root  # Required for eBPF
ExecStart=/usr/local/bin/coral-agent \
  --config=/etc/coral/agent.yaml \
  --discover=ports:8080,9090,5432
Restart=always
# Capabilities for eBPF
AmbientCapabilities=CAP_BPF CAP_PERFMON CAP_NET_ADMIN
# Or on older kernels:
# AmbientCapabilities=CAP_SYS_ADMIN

[Install]
WantedBy=multi-user.target
```

**Kubernetes (without RFD 012)**:

```yaml
# Privileged sidecar example
apiVersion: v1
kind: Pod
metadata:
    name: myapp
spec:
    shareProcessNamespace: true
    containers:
        -   name: app
            image: myapp:latest
            ports:
                -   containerPort: 8080

        -   name: coral-agent
            image: coral/agent:latest
            securityContext:
                privileged: true  # Required for eBPF
            env:
                -   name: BEYLA_DISCOVER_PORTS
                    value: "8080"
                -   name: COLONY_URL
                    value: "https://colony.example.com"
```

**Bare Metal**:

```bash
# Run agent with CAP_BPF (kernel 5.8+)
sudo setcap cap_bpf,cap_perfmon,cap_net_admin+eip /usr/local/bin/coral-agent
coral-agent --config=/etc/coral/agent.yaml --discover=ports:8080,9090

# Or on older kernels, run as root
sudo coral-agent --config=/etc/coral/agent.yaml --discover=ports:8080,9090
```

**Key requirements across all deployments**:

1. **eBPF capabilities**: `CAP_BPF` + `CAP_PERFMON` (kernel 5.8+) or
   `CAP_SYS_ADMIN` (older kernels)
2. **Host network access**: To discover processes by port (or use process names,
   container runtime APIs)
3. **Kernel headers**: Not required with CO-RE (kernel 5.2+), but may be needed
   on older kernels

### Beyla Capabilities Matrix

Beyla supports a wide range of protocols and runtimes. Here's what's available:

| Protocol      | Beyla Support        | Metrics Collected                                         | Trace Propagation           |
|---------------|----------------------|-----------------------------------------------------------|-----------------------------|
| HTTP/1.1      | ✅ Full               | Request rate, latency (P50/P95/P99), status codes, routes | ✅ W3C Trace Context         |
| HTTP/2        | ✅ Full               | Request rate, latency, status codes, routes               | ✅ W3C Trace Context         |
| gRPC          | ✅ Full               | RPC rate, latency, status codes, method names             | ✅ gRPC metadata propagation |
| Kafka         | ✅ Full               | Message rate, partition, topic, consumer lag              | ✅ Kafka headers             |
| Redis         | ✅ Full (RESP2/RESP3) | Command rate, latency, command types                      | ⚠️ Limited                  |
| PostgreSQL    | ✅ Full               | Query rate, latency, query patterns (obfuscated)          | ⚠️ Limited                  |
| MySQL         | ✅ Full               | Query rate, latency, query patterns (obfuscated)          | ⚠️ Limited                  |
| SQL Server    | ✅ Partial            | Basic query metrics                                       | ❌                           |
| TCP (generic) | ✅ Fallback           | Connection rate, bytes transferred                        | ❌                           |

**Runtime support** (as of Beyla 1.x):

- Go (all versions)
- Java (JVM 8+, including virtual threads in Java 21+)
- Python (CPython 3.x)
- Node.js (v12+)
- .NET (Core 3.1+, .NET 5+)
- Ruby (2.7+)
- Rust (native binaries)

### Performance Overhead

Beyla is optimized for production use with minimal overhead:

| Workload Type                              | CPU Overhead | Memory Footprint | Latency Impact |
|--------------------------------------------|--------------|------------------|----------------|
| **HTTP REST API** (high throughput)        | 1-2%         | 50-100 MB        | <100μs P99     |
| **gRPC streaming**                         | 1.5-3%       | 60-120 MB        | <200μs P99     |
| **Database queries** (PostgreSQL/MySQL)    | 0.5-1.5%     | 40-80 MB         | <50μs P99      |
| **Message queues** (Kafka/Redis)           | 1-2%         | 50-90 MB         | <100μs P99     |
| **Mixed protocols** (typical microservice) | 2-4%         | 80-150 MB        | <200μs P99     |

**Compared to custom eBPF** (RFD 013 estimates):

- Beyla overhead is **comparable or lower** than hand-written eBPF for
  HTTP/gRPC (due to years of optimization).
- Beyla's CO-RE implementation ensures compatibility across kernels without
  runtime recompilation.
- Combined Beyla + custom eBPF overhead: 3-6% CPU (well within acceptable limits
  for observability).

**Mitigation strategies**:

- Use sampling (`sampling.rate: 0.1` for 10% sampling) in extremely
  high-throughput services (>100k RPS).
- Disable unused protocols (`sql.enabled: false` if no database instrumentation
  needed).
- Configure cardinality limits (`route_patterns`) to prevent metric explosion
  from dynamic URL paths.

### Integration with RFD 013 (Custom eBPF)

Beyla and custom eBPF programs are **complementary, not competitive**:

| Data Source                                  | Use Case                                               | Collected By                 |
|----------------------------------------------|--------------------------------------------------------|------------------------------|
| **HTTP request rate, latency, status codes** | Baseline RED metrics for all services                  | **Beyla**                    |
| **gRPC method-level metrics**                | RPC performance tracking                               | **Beyla**                    |
| **Database query performance**               | SQL latency, query patterns                            | **Beyla**                    |
| **Distributed traces** (spans)               | Request flow across services                           | **Beyla**                    |
| **WireGuard tunnel metrics**                 | Mesh network performance (bytes, latency, packet loss) | **Custom eBPF** (future RFD) |
| **Cross-colony correlation**                 | Multi-cluster request tracing using Coral metadata     | **Custom eBPF** (future RFD) |
| **AI-triggered deep profiling**              | CPU flamegraphs, memory allocation on-demand           | **Custom eBPF** (RFD 013)    |
| **Security event monitoring**                | Anomalous syscalls, privilege escalation detection     | **Custom eBPF** (future RFD) |
| **Container runtime insights**               | cgroup stats, OOM events, resource throttling          | **Custom eBPF** (future RFD) |

**Example combined workflow**:

```bash
$ coral ask "Why is payments-api slow?"

🤖 Analyzing payments-api performance...
📊 Collecting data:
  - Beyla: HTTP latency histogram (30s sample)
  - Custom eBPF: CPU profile (on-demand, 60s)
  - Custom eBPF: WireGuard mesh latency to dependencies

Analysis:
  - HTTP P95: 450ms (baseline: 80ms) [Beyla]
  - 60% of time in external API calls [Beyla traces]
  - WireGuard latency to card-validation-svc: 200ms (baseline: 5ms) [Custom eBPF]
  - CPU profile shows no application hotspots [Custom eBPF]

Diagnosis: Network latency spike to card-validation-svc is causing slowdown.
Recommendation: Check card-validation-svc health and network path.
```

### Dependencies Clarification

This RFD depends on RFDs 011, 013, and 025. Here's why:

**RFD 011 (Multi-service agents)**:

- Beyla can discover and instrument multiple processes/services per agent.
- Multi-service agents provide the architectural foundation for process
  discovery and isolation.
- **Works in any environment**: docker-compose (one agent monitors multiple
  containers), systemd (one agent monitors multiple services), Kubernetes (
  sidecar or DaemonSet), bare metal, etc.
- **Not a hard blocker**: Beyla can work with single-service agents initially,
  but multi-service support maximizes efficiency.

**RFD 013 (eBPF introspection)**:

- Establishes eBPF infrastructure patterns (capability detection, manager
  lifecycle, safety limits).
- Beyla and custom eBPF programs coexist within the same agent architecture.
- **Complementary**: RFD 013's custom collectors supplement Beyla's commodity
  protocol support.

**RFD 025 (Basic OpenTelemetry Ingestion)** - **Critical dependency**:

- Provides OTLP receiver infrastructure in Coral agents.
- Beyla exports metrics/traces via OTLP (OpenTelemetry Protocol).
- Agents consume Beyla's OTLP exports using the receiver established by RFD 025.
- **Hard blocker**: Without OTLP receiver infrastructure, agents cannot consume
  Beyla's output.

**NOT dependent on RFD 012 (Kubernetes node agents)**:

- RFD 012 is Kubernetes-specific deployment patterns (DaemonSet vs Sidecar).
- Beyla works in **any environment** where the agent has eBPF capabilities:
    - **docker-compose**: Agent container with `--privileged` or `CAP_BPF`
    - **systemd**: Agent service running with appropriate capabilities
    - **Kubernetes**: DaemonSet (RFD 012) or privileged sidecar
    - **Bare metal**: Agent process with `CAP_BPF` or `CAP_SYS_ADMIN`
- Process discovery works via port numbers, process names, or container runtime
  APIs (not just K8s API).

**Recommended implementation order**:

1. RFD 025 (OTLP ingestion) → establishes receiver infrastructure
2. RFD 011 (multi-service) → enables multi-process discovery
3. RFD 032 (Beyla) → leverages OTLP receiver and multi-service architecture
4. RFD 013 (custom eBPF) + RFD 032 → combined observability stack
5. RFD 012 (K8s deployment patterns) → optional, for Kubernetes-specific
   deployments

### Kernel Compatibility & Fallback Strategy

Beyla has extensive kernel support with graceful degradation:

| Kernel Version | Beyla Support | Features Available                          | Notes                                  |
|----------------|---------------|---------------------------------------------|----------------------------------------|
| 5.8+           | ✅ Full        | All protocols, CO-RE, ring buffers, BTF     | **Recommended**                        |
| 5.2-5.7        | ✅ Full        | All protocols, CO-RE, BTF                   | Some performance optimizations missing |
| 4.18-5.1       | ⚠️ Limited    | HTTP/gRPC only, no BTF (pre-built programs) | RHEL 8 backports supported             |
| 4.14-4.17      | ⚠️ Degraded   | HTTP/1.1 only, limited tracing              | Legacy Ubuntu LTS                      |
| <4.14          | ❌ Unsupported | N/A                                         | Fall back to userspace polling         |

**Coral fallback strategy**:

1. **Kernel 5.8+**: Use Beyla for all protocols + custom eBPF for advanced
   features.
2. **Kernel 4.18-5.7**: Use Beyla for HTTP/gRPC + custom eBPF where supported.
3. **Kernel <4.18**: Disable Beyla, use userspace HTTP endpoint polling +
   process metrics only.

**Detection and reporting**:

- Agent detects kernel version and Beyla compatibility at startup.
- Reports capabilities to colony in `RegisterRequest.ebpf_capabilities`.
- CLI shows degraded mode:
  `⚠️ payments-api: Beyla limited (kernel 4.15), HTTP metrics only`.

## API Changes

### Protobuf Extensions

Extend existing eBPF protobuf definitions (`proto/coral/mesh/v1/ebpf.proto`):

```protobuf
syntax = "proto3";
package coral.mesh.v1;

import "google/protobuf/timestamp.proto";

// Beyla-specific capabilities reported by agent
message BeylaCapabilities {
    bool enabled = 1;
    string version = 2;  // Beyla library version
    repeated string supported_protocols = 3;  // ["http", "grpc", "kafka", ...]
    repeated string supported_runtimes = 4;   // ["go", "java", "python", ...]
    bool tracing_enabled = 5;
}

// Beyla RED metrics (aggregated by agent before sending)
message BeylaHttpMetrics {
    google.protobuf.Timestamp timestamp = 1;
    string service_name = 2;
    string http_route = 3;          // e.g., "/api/v1/users/:id"
    string http_method = 4;         // GET, POST, etc.
    uint32 http_status_code = 5;

    // Latency histogram buckets (milliseconds)
    repeated double latency_buckets = 6;  // [10, 25, 50, 100, 250, 500, 1000, 2500, 5000]
    repeated uint64 latency_counts = 7;   // Counts per bucket

    uint64 request_count = 8;             // Total requests in time window
    map<string, string> attributes = 9;   // pod, namespace, cluster, etc.
}

message BeylaGrpcMetrics {
    google.protobuf.Timestamp timestamp = 1;
    string service_name = 2;
    string grpc_method = 3;         // e.g., "/payments.PaymentService/Charge"
    uint32 grpc_status_code = 4;    // 0 = OK, 1 = CANCELLED, etc.

    repeated double latency_buckets = 5;
    repeated uint64 latency_counts = 6;

    uint64 request_count = 7;
    map<string, string> attributes = 8;
}

message BeylaSqlMetrics {
    google.protobuf.Timestamp timestamp = 1;
    string service_name = 2;
    string sql_operation = 3;       // SELECT, INSERT, UPDATE, DELETE
    string table_name = 4;          // Extracted from query (if possible)

    repeated double latency_buckets = 5;
    repeated uint64 latency_counts = 6;

    uint64 query_count = 7;
    map<string, string> attributes = 8;
}

// Distributed trace span (OpenTelemetry-compatible)
message BeylaTraceSpan {
    string trace_id = 1;            // 32-char hex string
    string span_id = 2;             // 16-char hex string
    string parent_span_id = 3;      // Empty if root span

    string service_name = 4;
    string span_name = 5;           // e.g., "GET /api/v1/users/:id"
    string span_kind = 6;           // "server", "client", "producer", "consumer"

    google.protobuf.Timestamp start_time = 7;
    google.protobuf.Duration duration = 8;

    uint32 status_code = 9;         // HTTP/gRPC status
    map<string, string> attributes = 10;
}

// Update EbpfEvent to include Beyla payloads
message EbpfEvent {
    google.protobuf.Timestamp timestamp = 1;
    string collector_id = 2;

    oneof payload {
        // Existing custom eBPF collectors
        HttpLatencyHistogram http_latency = 10;
        CpuProfileSample cpu_profile = 11;

        // Beyla collectors
        BeylaHttpMetrics beyla_http = 20;
        BeylaGrpcMetrics beyla_grpc = 21;
        BeylaSqlMetrics beyla_sql = 22;
        BeylaTraceSpan beyla_trace = 23;
    }
}
```

### DuckDB Storage Schema

**Beyla HTTP Metrics Table**:

```sql
CREATE TABLE beyla_http_metrics
(
    timestamp        TIMESTAMPTZ NOT NULL,
    agent_id         VARCHAR     NOT NULL,
    service_name     VARCHAR     NOT NULL,
    http_method      VARCHAR(10),
    http_route       VARCHAR(255),
    http_status_code SMALLINT,
    latency_bucket_ms DOUBLE NOT NULL,
    count            BIGINT      NOT NULL,
    attributes       MAP(VARCHAR, VARCHAR),
    PRIMARY KEY (timestamp, agent_id, service_name, http_method, http_route,
                 http_status_code, latency_bucket_ms)
);

CREATE INDEX idx_beyla_http_service_time ON beyla_http_metrics (service_name, timestamp DESC);
CREATE INDEX idx_beyla_http_route ON beyla_http_metrics (http_route, timestamp DESC);
```

**Beyla gRPC Metrics Table**:

```sql
CREATE TABLE beyla_grpc_metrics
(
    timestamp        TIMESTAMPTZ NOT NULL,
    agent_id         VARCHAR     NOT NULL,
    service_name     VARCHAR     NOT NULL,
    grpc_method      VARCHAR(255),
    grpc_status_code SMALLINT,
    latency_bucket_ms DOUBLE NOT NULL,
    count            BIGINT      NOT NULL,
    attributes       MAP(VARCHAR, VARCHAR),
    PRIMARY KEY (timestamp, agent_id, service_name, grpc_method,
                 grpc_status_code, latency_bucket_ms)
);

CREATE INDEX idx_beyla_grpc_service_time ON beyla_grpc_metrics (service_name, timestamp DESC);
```

**Beyla Traces Table** (OpenTelemetry-compatible):

```sql
CREATE TABLE beyla_traces
(
    trace_id       VARCHAR(32) NOT NULL,
    span_id        VARCHAR(16) NOT NULL,
    parent_span_id VARCHAR(16),
    service_name   VARCHAR     NOT NULL,
    span_name      VARCHAR     NOT NULL,
    span_kind      VARCHAR(10),
    start_time     TIMESTAMPTZ NOT NULL,
    duration_us    BIGINT      NOT NULL,
    status_code    SMALLINT,
    attributes     MAP(VARCHAR, VARCHAR),
    PRIMARY KEY (trace_id, span_id)
);

CREATE INDEX idx_beyla_traces_service_time ON beyla_traces (service_name, start_time DESC);
CREATE INDEX idx_beyla_traces_trace_id ON beyla_traces (trace_id, start_time DESC);
```

### CLI Commands

> **Note:** CLI integration is deferred to a future RFD (Phase 5). The examples
> below are illustrative of the eventual user experience but are not implemented
> in this RFD. This RFD implements the underlying gRPC API that future CLI
> commands will consume.

Beyla metrics will eventually be accessible through `coral` commands:

**Query RED metrics**:

```bash
$ coral query beyla http payments-api --since 1h

Service: payments-api (last 1 hour, Beyla HTTP metrics)

Route                        | Requests | P50   | P95   | P99   | Errors
-----------------------------|----------|-------|-------|-------|-------
POST /api/v1/payments        | 45.2k    | 45ms  | 180ms | 420ms | 2.3%
GET /api/v1/payments/:id     | 12.8k    | 8ms   | 25ms  | 60ms  | 0.1%
POST /api/v1/refunds         | 3.1k     | 120ms | 350ms | 800ms | 5.7%

Overall: 61.1k requests, P95=180ms, error rate=2.8%
```

**Query distributed traces**:

```bash
$ coral query beyla traces --trace-id abc123def456 --format tree

Trace ID: abc123def456
Duration: 1.2s
Spans: 8

frontend-api (1.2s, GET /checkout)
├─ payments-api (450ms, POST /api/v1/payments)
│  ├─ card-validator-svc (380ms, POST /validate)
│  │  └─ postgres (12ms, SELECT from cards)
│  └─ fraud-detector (35ms, gRPC Check)
├─ inventory-api (180ms, POST /api/v1/reserve)
│  └─ redis (2ms, SET order:lock:12345)
└─ email-svc (15ms, Kafka publish to notifications)
```

**AI-driven analysis with Beyla data**:

```bash
$ coral ask "What's the slowest API endpoint in payments-api?"

🤖 Analyzing payments-api (Beyla HTTP metrics, last 24h)...

Slowest endpoints by P95 latency:
1. POST /api/v1/refunds: P95=350ms (baseline: 120ms, +192%)
2. POST /api/v1/payments: P95=180ms (baseline: 150ms, +20%)
3. GET /api/v1/statements/:id: P95=95ms (baseline: 80ms, +19%)

Diagnosis for POST /api/v1/refunds:
- Latency spike started 6 hours ago
- Distributed traces show 80% of time in card-validator-svc
- Recommendation: Investigate card-validator-svc performance

Evidence: ./evidence/beyla-http-payments-api-2025-11-13.json
```

**Integration with `coral tap`**:

```bash
$ coral tap payments-api --beyla-http --beyla-traces --duration 60s

🔍 Tap session started (beyla + packets)
📊 Data sources: Beyla HTTP metrics, Beyla traces, network packets

[Live tail of metrics...]

HTTP Metrics (last 60s):
  POST /api/v1/payments: 120 req/s, P95=180ms, 2% errors
  GET /api/v1/payments/:id: 45 req/s, P95=25ms, 0% errors

Active Traces: 8
  Trace abc123: 1.2s (frontend → payments → card-validator)
  Trace def456: 850ms (frontend → payments → fraud-detector)

✓ Session completed. Data saved to: ./tap-sessions/tap-2025-11-13-14-30/
```

### Configuration Changes

**Agent config** (`agent-config.yaml`):

- New `beyla` section (see Configuration Example above).
- `beyla.enabled` flag (default: `true` on supported kernels).
- `beyla.discovery` for process/pod selection.
- `beyla.protocols` for per-protocol configuration.
- `beyla.attributes` for custom enrichment.

**Colony config** (`colony-config.yaml`):

```yaml
storage:
    beyla:
        # Retention by metric type
        retention:
            http_metrics: 30d
            grpc_metrics: 30d
            sql_metrics: 14d
            traces: 7d              # Traces are large, shorter retention

        # Compression
        compression: zstd

        # Trace sampling (reduce storage for high-volume services)
        trace_sampling_rate: 0.1  # Keep 10% of traces

# AI integration (deferred to RFD 004 - MCP server implementation)
# ai:
#     beyla_integration:
#         auto_query: true
#         triggers:
#             - pattern: "slow|latency|performance"
#               data_sources: ["beyla_http", "beyla_grpc"]
```

## Testing Strategy

> **Note:** Comprehensive testing (Phase 7) is deferred to a future testing RFD.
> This section outlines testing approaches for reference. This RFD includes
> basic
> unit tests for storage and transformation logic.

### Unit Tests

- Beyla configuration parsing and validation.
- Metric aggregation logic (histogram bucketing, attribute merging).
- Protocol-specific metric extraction (HTTP route normalization, gRPC method
  parsing).
- Fallback behavior when Beyla unavailable (kernel version checks).

### Integration Tests

- Run Beyla-instrumented agent against test HTTP server, verify metrics
  collected.
- Test multi-protocol workload (HTTP + gRPC + PostgreSQL), ensure all metrics
  appear in DuckDB.
- Verify distributed trace propagation across multiple services.
- Test kernel compatibility matrix (5.8+, 4.18, 4.14) using VMs or containers.

### E2E Tests

- Full CLI workflow: `coral query beyla http <service>`, verify output matches
  expected format.
- AI query integration: `coral ask "Why is X slow?"`, verify Beyla metrics
  referenced in analysis.
- Trace visualization: `coral query beyla traces --trace-id <id>`, verify span
  tree structure.
- Combined Beyla + custom eBPF: Ensure both data sources coexist without
  conflicts.

## Security Considerations

- Beyla requires `CAP_BPF` (kernel 5.8+) or `CAP_SYS_ADMIN` (older kernels).
  Coral agents must run with these capabilities (already required for RFD 013
  custom eBPF).
- **Privacy**: Disable header/payload capture by default (
  `capture_headers: false`). SQL queries are obfuscated (literals replaced with
  `?`).
- **Cardinality explosion**: Enforce route patterns (`/api/v1/users/:id`) to
  prevent unbounded metric labels from dynamic URLs.
- **Resource limits**: Beyla's `max_traced_connections` prevents memory
  exhaustion from tracking too many simultaneous connections.
- **Audit logging**: Log Beyla lifecycle events (startup, protocol enablement,
  errors) for security audits.

## Future Enhancements

**Separate RFD for custom eBPF programs**:

- WireGuard tunnel performance monitoring (packet loss, latency, throughput per
  peer).
- Multi-colony trace correlation using Coral-specific metadata (colony ID, mesh
  IP).
- AI-triggered deep profiling (CPU flamegraphs, memory allocation, lock
  contention).
- Security-focused collectors (anomalous syscalls, privilege escalation
  detection, container escape attempts).
- Container runtime insights (cgroup throttling, OOM events, CPU/memory
  pressure).

**Beyla enhancements**:

- Contribute Coral-specific features back to the OpenTelemetry Beyla project (
  e.g., custom attribute injection for colony ID).
- Explore Beyla's roadmap for new protocols (WebSockets, QUIC/HTTP3, MQTT,
  NATS).

**Advanced tracing**:

- Integrate with external trace backends (Jaeger, Tempo) via OpenTelemetry
  export.
- Implement trace-based alerting (e.g., "Alert if trace duration >5s").

## Appendix

### Beyla vs. Custom eBPF Trade-offs

| Aspect                      | Beyla                                              | Custom eBPF (RFD 013)              |
|-----------------------------|----------------------------------------------------|------------------------------------|
| **Development time**        | Zero (library integration)                         | Weeks-months per collector         |
| **Protocol support**        | 10+ protocols out-of-box                           | Implement each protocol manually   |
| **Kernel compatibility**    | Tested on 100+ kernel versions                     | Requires extensive testing         |
| **Runtime support**         | 7+ languages (Go, Java, Python, etc.)              | Language-specific unwinders needed |
| **Maintenance burden**      | CNCF/OpenTelemetry community-maintained            | Coral team maintains all code      |
| **Customization**           | Limited (fork required)                            | Full control over implementation   |
| **Coral-specific features** | Not available (mesh correlation, AI orchestration) | Designed for Coral architecture    |
| **Production readiness**    | Battle-tested across OTEL ecosystem                | Requires stabilization period      |

**Conclusion**: Use Beyla for commodity observability (RED metrics, traces),
custom eBPF for differentiation (WireGuard stats, AI profiling, multi-colony
correlation).

---

### Beyla References

- **Official repository
  **: https://github.com/open-telemetry/opentelemetry-ebpf (OpenTelemetry eBPF
  project, includes Beyla)
- **Legacy repository**: https://github.com/grafana/beyla (original Grafana
  repository, may redirect)
- **OpenTelemetry documentation**: https://opentelemetry.io/docs/
- **Note**: As Beyla was recently donated to OpenTelemetry, documentation and
  repository locations may be in transition. Check the OpenTelemetry project for
  the most current information.

### Example Beyla Configuration (Standalone)

For reference, here's how Beyla is typically configured as a standalone tool.
Coral runs Beyla as a separate process and translates agent YAML configuration
into Beyla command-line arguments:

```yaml
# beyla-config.yaml (standalone example, not Coral config)
discovery:
    services:
        -   k8s_namespace: "production"
            k8s_pod_label:
                app: "payments-api"

attributes:
    kubernetes:
        enable: cluster_name
        cluster_name: "us-west-2"

routes:
    patterns:
        - "/api/v1/users/:id"
        - "/api/v1/orders/:id"

otel_metrics_export:
    endpoint: http://otel-collector:4318

otel_traces_export:
    endpoint: http://tempo:4318
    sampler: parentbased_traceidratio
    sampler_arg: "0.1"
```

Coral agents run Beyla as a separate process (not as an embedded library) and
dynamically construct command-line arguments from the agent's YAML
configuration,
providing flexibility while maintaining process isolation.

---

## Deferred Features

The following features are deferred to future RFDs as they build on the core
Beyla integration but are not required for basic RED metrics collection:

- **CLI Query Commands** (RFD 035): Interactive queries like
  `coral query beyla http payments-api --since 1h` with percentile calculations
  and formatted output
- **MCP Integration** (RFD 004): Expose Beyla metrics as MCP tools for
  AI-driven diagnostics (e.g., `coral_query_beyla_metrics` tool)
- **Advanced Correlation** (Future RFD): Correlation queries joining Beyla RED
  metrics with custom eBPF data (WireGuard stats, CPU profiles, etc.)
- **Distributed Tracing** (Future RFD): Full trace storage and visualization (
  RFD 032 focuses on RED metrics, not traces)
- **Production Testing** (RFD 037): Comprehensive kernel compatibility testing,
  performance benchmarks, and canary deployment strategies
- **Colony Server Integration** (Pending): Wire BeylaPoller into Colony startup
  and configuration
