# Coral Agent

The Coral Agent is a lightweight observability daemon that runs alongside your
applications to collect telemetry data and respond to colony queries.

## Table of Contents

- [Architecture Overview](#architecture-overview)
- [How It Works](#how-it-works)
- [Port Overview](#port-overview)
- [OpenTelemetry Integration](#opentelemetry-integration)
- [Beyla Integration (eBPF Metrics)](#beyla-integration-ebpf-metrics)
- [System Metrics](#system-metrics)
- [Agent API](#agent-api)
- [Static Filtering](#static-filtering)
- [Data Flow](#data-flow)
- [Performance Characteristics](#performance-characteristics)
- [Security Model](#security-model)

---

## Architecture Overview

The agent operates on a **pull-based architecture**:

- Receives OpenTelemetry (OTLP) traces from your applications
- Applies static filtering rules to reduce data volume
- Stores filtered spans locally (~1 hour retention)
- Responds to colony queries for recent telemetry data

**Key Design Principles:**

- **Stateless from colony perspective**: Colony pulls data on-demand
- **Local-first**: All data stored locally with automatic TTL cleanup
- **Zero-configuration discovery**: Automatically discovers and registers with
  colony
- **Minimal overhead**: ~5-10% CPU idle, ~50-100 MB base memory

---

## How It Works

### 1. Telemetry Collection

The agent receives traces via OTLP (OpenTelemetry Protocol) from instrumented
applications:

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
            Static Filtering
```

### 2. Static Filtering

The agent applies deterministic filtering rules to reduce data volume while
capturing important signals:

1. **Always capture errors**: Spans with `status.code = ERROR` are always kept
2. **Always capture high latency**: Spans with
   `duration > high_latency_threshold_ms` are always kept
3. **Sample normal spans**: Other spans are sampled at `sample_rate`

**Why Static Filtering?**

- **Predictable**: Sampling behavior is deterministic and easy to reason about
- **Debuggable**: No complex adaptive algorithms that change behavior over time
- **Operational simplicity**: No need to tune ML models or dynamic thresholds
- **Captures important signals**: Errors and high-latency spans are critical for
  debugging

### 3. Local Storage

Filtered spans are stored in a local DuckDB database:

- **Retention**: ~1 hour (configurable)
- **Table**: `otel_spans_local`
- **Indexed by**: timestamp, service_name
- **TTL Cleanup**: Automatic hourly cleanup of old spans

### 4. Colony Queries

The colony queries agents on-demand via the Agent API (port 9001):

- **Pull-based**: Colony initiates requests when needed
- **Time-range queries**: Colony requests spans for specific time windows
- **Service filtering**: Query specific services or all services
- **Aggregation**: Colony aggregates data from multiple agents

---

## Port Overview

The Coral Agent exposes the following ports:

| Port     | Protocol             | Purpose                            | Bind Address                           | Access              |
|----------|----------------------|------------------------------------|----------------------------------------|---------------------|
| **4317** | OTLP/gRPC            | OpenTelemetry trace ingestion      | Configurable (default: `0.0.0.0:4317`) | Applications, Beyla |
| **4318** | OTLP/HTTP            | OpenTelemetry trace ingestion      | Configurable (default: `0.0.0.0:4318`) | Applications, Beyla |
| **4319** | OTLP/gRPC            | Internal Beyla trace ingestion     | Localhost only (`127.0.0.1:4319`)      | Beyla (Internal)    |
| **9001** | HTTP/2 (Connect RPC) | Agent API for colony communication | Mesh IP + localhost                    | Colony, local CLI   |

**Network Topology:**

```
┌─────────────────────────────────────────────────────────────┐
│  Host / Container                                           │
│                                                             │
│  ┌──────────────┐         ┌───────────────────────────────┐ │
│  │ Application  │ OTLP    │  Coral Agent                  │ │
│  │ (OTel SDK)   ├────────►│  • OTLP Receivers             │ │
│  └──────────────┘  :4317  │    - gRPC: 4317               │ │
│                    :4318  │    - HTTP: 4318               │ │
│  ┌──────────────┐         │  • Beyla Receiver             │ │
│  │ Beyla        │ OTLP    │    - gRPC: 4319               │ │
│  │ (eBPF)       ├────────►│  • Agent API: 9001            │ │
│  └──────────────┘  :4319  │    (Connect RPC)              │ │
│                           └───────────────▲───────────────┘ │
└───────────────────────────────────────────│─────────────────┘
                                            │ WireGuard mesh
                                            │ :9001
                                            │
                                 ┌──────────────────┐
                                 │ Colony           │ --- Colony pulls data
                                 │ (gRPC client)    │
                                 └──────────────────┘
```

**Security Notes:**

- **Ports 4317/4318**: Bind to `0.0.0.0` by default for application access.
  Consider binding to `127.0.0.1` if applications run on the same host.
- **Port 4319**: Localhost-only for Beyla integration to avoid conflicts with
  application traces
- **Port 9001**: Automatically binds to WireGuard mesh IP and localhost. Only
  accessible from colony (via mesh) and local debugging.

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

The agent is protocol-agnostic and works with any OTLP-compliant exporter. You
can use any OpenTelemetry SDK (Go, Node.js, Python, Java, Ruby, .NET, etc.) to
send traces to the agent.

For detailed instrumentation examples and best practices, see the
**[Instrumentation Guide](INSTRUMENTATION.md)**.

> **Configuration**: For agent configuration options (bind addresses, retention,
> filters), see [`docs/CONFIG.md`](CONFIG.md#agent-configuration).

---

## Beyla Integration (eBPF Metrics)

### Overview

The agent can optionally run **Beyla**, an eBPF-based auto-instrumentation tool
that collects RED (Rate, Errors, Duration) metrics for HTTP, gRPC, and database
protocols **without requiring code changes** to your applications.

**Key Features:**

- Zero-code instrumentation using eBPF kernel probes
- Automatic discovery of services by port, process name, or Kubernetes labels
- Collects HTTP, gRPC, and SQL query performance metrics
- Local DuckDB storage with configurable retention
- Pull-based: Colony queries agent on-demand for metrics

**Beyla Output:**

- Beyla exports metrics and traces via OTLP to the agent's dedicated local
  receiver
- Default: `localhost:4319` (gRPC)
- Agent ingests Beyla's output through this dedicated OTLP receiver to avoid
  conflicts with application traces

### How Beyla Works

```
┌─────────────────────────────────────────────────────────────┐
│  Kernel Space                                               │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ eBPF Probes (kprobes, uprobes, tracepoints)          │   │
│  │ • HTTP request/response                              │   │
│  │ • gRPC calls                                         │   │
│  │ • SQL queries                                        │   │
│  └──────────────────┬───────────────────────────────────┘   │
└─────────────────────│───────────────────────────────────────┘
                      │ BPF ring buffer
                      ▼
┌─────────────────────────────────────────────────────────────┐
│  User Space - Beyla Process                                 │
│  • Aggregates metrics                                       │
│  • Enriches with metadata                                   │
│  • Exports via OTLP                                         │
└─────────────────┬───────────────────────────────────────────┘
                  │ OTLP/gRPC :4319
                  ▼
┌─────────────────────────────────────────────────────────────┐
│  Coral Agent - Beyla Receiver                               │
│  • Stores in local DuckDB                                   │
│  • Responds to colony queries                               │
└─────────────────────────────────────────────────────────────┘
```

### Beyla vs OpenTelemetry Traces

| Aspect                 | Beyla (eBPF)              | OpenTelemetry SDK     |
|------------------------|---------------------------|-----------------------|
| **Instrumentation**    | Automatic via eBPF        | Requires code changes |
| **Protocols**          | HTTP, gRPC, SQL           | Any (custom spans)    |
| **Data Type**          | RED metrics               | Distributed traces    |
| **Overhead**           | ~1-2% CPU                 | ~2-5% CPU             |
| **Kernel Requirement** | 4.18+ with eBPF           | Any                   |
| **Use Case**           | Infrastructure monitoring | Application debugging |

**Recommendation:** Use both - Beyla for automatic RED metrics and OpenTelemetry
SDK for detailed application traces.

> **Configuration**: For Beyla configuration options (discovery, protocols,
> attributes), see [`docs/CONFIG.md`](CONFIG.md#beyla-integration-configuration).

---

## System Metrics

### Overview

The agent collects host-level system metrics to provide infrastructure visibility
alongside application telemetry. This allows you to correlate application
performance issues with underlying resource constraints (e.g., CPU throttling,
OOM kills, disk saturation).

**Collected Metrics:**

- **CPU**: Utilization percentage, user/system/idle time breakdown
- **Memory**: Used/available bytes, utilization percentage
- **Disk**: IOPS, throughput (bytes/sec), disk usage percentage
- **Network**: Bandwidth (bytes sent/recv), packet errors/drops

### Architecture

The system metrics subsystem follows the same **pull-based** and **local-first**
philosophy as other agent components:

```
┌─────────────────────────────────────────────────────────────┐
│  Coral Agent - System Metrics Collector                     │
│  • Samples host metrics (via gopsutil)                      │
│  • Interval: ~15s                                           │
│  • Overhead: <1% CPU                                        │
└─────────────────┬───────────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────────────┐
│  Local Storage (DuckDB)                                     │
│  • Table: system_metrics_local                              │
│  • Retention: ~1 hour                                       │
└─────────────────┬───────────────────────────────────────────┘
                  │
                  │ Colony Poll (every 60s)
                  ▼
          ┌──────────────────┐
          │  Colony          │
          │  • Aggregates    │
          │  • Summarizes    │
          └──────────────────┘
```

**Key Characteristics:**

- **Lightweight**: Uses efficient native syscalls via `gopsutil`
- **Privacy-safe**: Only collects aggregate counters, no PII or process commands
- **Correlation**: Metrics are timestamped to align perfectly with traces and Beyla metrics

> **Configuration**: For system metrics configuration options (poll interval,
> enabling/disabling), see
> [`docs/CONFIG.md`](CONFIG.md#system-metrics-configuration-rfd-071).

---

## Agent API

The agent exposes a **Connect RPC** service on **port 9001** for communication
with the colony and local CLI tools.

### Port 9001 - Agent API

**Purpose**: Colony queries, service management, telemetry requests, remote
shell execution

**Binding:**

- **Mesh IP** (`<mesh-ip>:9001`): Accessible from colony via WireGuard mesh
- **Localhost** (`127.0.0.1:9001`): Accessible from local CLI commands (
  `coral connect`, `coral agent status`)

**Protocol**: HTTP/2 (Connect RPC) with bidirectional streaming support

**Endpoints:**

- `/coral.agent.v1.AgentService/*` - Main agent API
- `/status` - Runtime and mesh network debugging info (JSON)
- `/duckdb/<database-name>` - Remote DuckDB query endpoint

**Security:**

- Not exposed outside the WireGuard mesh
- No authentication required (protected by mesh network isolation)
- Uses WireGuard's encrypted tunnel for all colony-to-agent communication

**Example Usage:**

```bash
# Local CLI access
curl http://localhost:9001/status

# Colony access (from within mesh)
curl http://100.64.0.5:9001/status

# Remote DuckDB query
curl http://100.64.0.5:9001/duckdb/metrics.duckdb
```

---

## Static Filtering

The agent applies **static filtering rules** to reduce data volume while
capturing important spans.

### Filtering Rules (Applied in Order)

1. **Always capture errors**: Spans with `status.code = ERROR` are always kept.
2. **Always capture high latency**: Spans with
   `duration > high_latency_threshold_ms` are always kept.
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

> **Configuration**: For filter configuration options, see [
`docs/CONFIG.md`](CONFIG.md#agent-configuration).

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
│  Local Storage (DuckDB)                                     │
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

## Security Model

### Network Security

**OTLP Endpoints (4317/4318)**:

- Bind to `0.0.0.0` by default for application access
- **Recommendation**: Bind to `127.0.0.1` if applications run on the same host
- No authentication - relies on network isolation

**Agent API (9001)**:

- Binds to WireGuard mesh IP and localhost
- Not exposed outside the mesh
- No authentication required (protected by mesh network isolation)
- Uses WireGuard's encrypted tunnel for all colony-to-agent communication

### Data Privacy

**PII in Spans**: OpenTelemetry spans may contain:

- HTTP headers (Authorization, cookies)
- Request/response bodies
- User IDs in span attributes

**Mitigation**:

1. Configure application to exclude sensitive attributes
2. Short retention period (1 hour) limits exposure
3. Review application's OTel instrumentation

---

## See Also

- **[Instrumentation Guide](INSTRUMENTATION.md)**: How to instrument
  applications with Coral SDK and OpenTelemetry
- **[Configuration Guide](CONFIG.md)**: Detailed configuration options for
  agents
- **[Kubernetes Deployments](../deployments/k8s/README.md)**: Kubernetes
  deployment patterns (Sidecar, DaemonSet)
- **[CLI Reference](CLI.md)**: Agent management commands
