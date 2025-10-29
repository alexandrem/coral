# Coral Architecture

**Version**: 0.1 (Design Phase)

This document describes Coral's technical architecture - how the components work together to create application-scoped agentic intelligence.

---

## Overview

Coral gives distributed applications **autonomous observability and analysis** through a three-tier architecture:

1. **Colony** (central intelligence): AI analysis, cross-agent correlation, historical patterns
2. **Agents** (local observers): Per-component monitoring, recent high-resolution data
3. **SDK** (optional enhancement): Structured metadata for better observability

**Key Principle**: The system is self-sufficient from local data alone. External MCP integrations (Grafana, Sentry) add depth but aren't required for core intelligence.

---

## Core Architecture: The Nervous System

Think of Coral as giving your distributed system **sensory nerves, a spinal cord, and a brain**:

```
┌──────────────────────────────────────────────────────┐
│          THE BRAIN (Colony)                          │
│                                                      │
│  Layered Intelligence (Colony Storage):              │
│  • Summaries & aggregations across all agents        │
│  • Historical data (beyond agent retention window)   │
│  • Cross-agent correlations and patterns             │
│  • Topology knowledge graph (auto-discovered)        │
│  • Learned behavioral baselines                      │
│  • AI synthesis & reasoning (Claude/GPT)             │
│  • Can query agents on-demand for recent details     │
│                                                      │
└──────┬──────────┬──────────┬────────────────────────┘
       │          │          │
       │ Encrypted control mesh (WireGuard)
       │          │          │
       │   (push summaries + bidirectional queries)
       │          │          │
   ┌───▼─────┐ ┌──▼─────┐ ┌──▼─────┐
   │ Agent   │ │ Agent  │ │ Agent  │  ← Nerve endings
   │Frontend │ │  API   │ │   DB   │     (rich local sensing
   │         │ │        │ │        │      + recent raw data)
   │ Local   │ │ Local  │ │ Local  │  ← Agent storage:
   │ Store   │ │ Store  │ │ Store  │     Recent high-res data
   └───┬─────┘ └──┬─────┘ └──┬─────┘     (~1 hour raw metrics)
       │          │          │
   ┌───▼─────┐ ┌──▼─────┐ ┌──▼─────┐
   │Frontend │ │  API   │ │   DB   │  ← Your app components
   │(React)  │ │(Node)  │ │(Postgres)   (run normally)
   └──┬──────┘ └──┬─────┘ └──┬─────┘
      │           │          │
   [Metrics] [Health] [Connections]  ← Local observation

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
TIER 1: ↑ Self-sufficient (works standalone, air-gapped)
TIER 2: ↓ Optional enrichment (when available)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

   Optional: External MCP Integrations (for depth)
   ┌────────────────────────────────────────┐
   │  [Grafana MCP]  - Long-term metrics    │
   │  [Sentry MCP]   - Error grouping       │
   │  [PagerDuty]    - Incident history     │
   │  [Custom MCPs]  - Your internal tools  │
   └────────────────────────────────────────┘
```

**Critical Separation**: Coral operates ONLY in the control plane. Your application components communicate via their existing infrastructure (VPC, service mesh, load balancers). Coral agents observe locally and relay intelligence via a separate encrypted mesh.

---

## Layered Storage Architecture

The system uses **layered storage** for horizontal scalability:

```
┌─────────────────────────────────────────────┐
│  COLONY LAYER (Summaries + History)         │
│  • Cross-agent aggregations                 │
│  • Historical trends and patterns           │
│  • Topology graph (auto-discovered)         │
│  • Learned behavioral baselines             │
│  • Event correlations across services       │
│  • Can query agent layer on-demand          │
│                                             │
│  Storage: DuckDB (in-memory + persistence)  │
└─────────────────────────────────────────────┘
                      ↕
        (Push summaries + pull details)
                      ↕
┌─────────────────────────────────────────────┐
│  AGENT LAYER (Recent Raw Data)              │
│  • High-resolution metrics (~1 hour)        │
│  • Detailed event logs (local services)     │
│  • Process-level observations               │
│  • Network connection details               │
│  • Responds to colony queries               │
│                                             │
│  Storage: Embedded (details TBD)            │
└─────────────────────────────────────────────┘
```

### Why Layered Storage?

- **Scalability**: Colony doesn't store all raw data from every agent
- **Performance**: Recent high-resolution data available on-demand from agents
- **Efficiency**: Agents push compressed summaries, full data stays local
- **Resilience**: Colony operates on stale summaries if agents temporarily offline
- **Natural distribution**: Storage grows with application complexity

### Query Performance

- **Summary queries** (from colony): <1 second
- **Detail queries** (colony → agent): +network latency (typically <100ms)

---

## Component Details

### 1. Colony (Central Intelligence)

**Purpose**: Aggregates observations, runs AI analysis, provides conversational interface

**Responsibilities**:
- Manage encrypted control mesh (WireGuard)
- Accept agent connections and observations
- Store aggregated summaries and historical data
- Query agents on-demand for recent details
- Run AI correlation and synthesis
- Serve dashboard and API
- Generate insights and recommendations
- Orchestrate MCP queries to external tools

**Storage** (DuckDB):
- Time-series summaries (aggregated from agents)
- Event log (deployments, crashes, anomalies)
- Topology graph (auto-discovered from agent network observations)
- Learned baselines (normal behavior patterns)
- Investigation history (for pattern learning)

**AI Integration**:
- Direct API calls to Anthropic/OpenAI
- Uses user's API keys (configured in colony)
- All prompts and data stay on user's infrastructure
- No data sent to Coral (us)

**API Surface**:
- CLI: `coral ask "what happened?"` (conversational queries)
- gRPC: Agent registration and data ingestion
- HTTP REST: Dashboard and MCP server endpoints
- MCP Server: Exports topology/events for other AI assistants

**Deployment**:
```bash
# Local development
coral colony start

# Production (daemon)
coral colony start --daemon --data-dir=/var/lib/coral

# Docker
docker run -v coral-data:/data -e ANTHROPIC_API_KEY=... coral/colony

# Kubernetes
kubectl apply -f colony.yaml
```

### 2. Agent (Local Observer)

**Purpose**: Lightweight observer that monitors apps locally and relays to colony

**Critical**: Agents are **observers, not proxies**. They run alongside apps and watch them, but never intercept or route application traffic.

**Responsibilities**:
- Establish WireGuard tunnel to colony
- Observe application components locally
- Detect network connections (netstat/ss)
- Store recent raw data (~1 hour)
- Push compressed summaries to colony (every 10-60s)
- Respond to colony queries for detailed data
- Execute local health checks

**Local Observations**:
```
Every 10-30 seconds:
├─ Process metrics (from /proc or equivalent)
│  ├─ CPU, memory, file descriptors, threads
│  ├─ Process restarts, crashes, exit codes
│  └─ Version info (SDK or binary inspection)
│
├─ Network connections (netstat/ss)
│  ├─ Active connections → builds dependency graph
│  ├─ Connection rates, errors
│  └─ Discovers service topology automatically
│
├─ Application health
│  ├─ HTTP health endpoint polling
│  └─ SDK: Component-level health (DB, cache, etc.)
│
└─ Metrics collection (optional)
   └─ Scrape Prometheus /metrics endpoints
```

**What Agent Does NOT Do**:
- ❌ Proxy or intercept application traffic
- ❌ Route requests between services
- ❌ Provide load balancing or circuit breaking
- ❌ Collect detailed metrics (use Prometheus/OTEL)
- ❌ Aggregate logs (use your log aggregator)
- ❌ Collect traces (use Jaeger/Zipkin)

**Resource Usage**:
- Memory: <10MB
- CPU: <0.1%
- Network: <1KB/s to colony
- Disk: <100MB for local storage

**Deployment**:
```bash
# Systemd service
coral agent start --colony=wg://mesh-id

# Docker sidecar
docker run --pid=host --net=host coral/agent

# Kubernetes DaemonSet
kubectl apply -f agent-daemonset.yaml
```

### 3. Discovery Service (Coordination)

**Purpose**: Lightweight coordination for NAT traversal (similar to Tailscale)

**Responsibilities**:
- Agent/colony registration
- Endpoint discovery
- NAT traversal assistance

**What it stores**:
```
mesh_id → {
  colony_pubkey: "...",
  endpoints: ["1.2.3.4:41820"],
  last_seen: timestamp
}
```

**What it CANNOT do**:
- Decrypt WireGuard traffic (no private keys)
- See application data
- Impersonate colonies (pubkey verification)

**Trust Model**:
- Open source, auditable
- Can be self-hosted
- Similar to Tailscale's coordination server

---

## SDK Integration: Optional Enhancement

### Philosophy: Standards-First

Coral works **without SDK** (passive observation), but SDK provides enhanced capabilities.

**Key Principle**: Don't reinvent existing standards
- ❌ Don't create custom metrics → Use Prometheus /metrics
- ❌ Don't create custom tracing → Use OpenTelemetry
- ✅ SDK provides discovery + structured data where standards don't exist

### What SDK Provides

**1. Endpoint Discovery**
```go
coral.Initialize(coral.Config{
    ServiceName: "api",
    Version:     "2.1.0",
    Endpoints: coral.Endpoints{
        Metrics: "http://localhost:8080/metrics",  // Prometheus
        Health:  "http://localhost:8080/health",
    },
})
```

**2. Enhanced Health Checks**
```
Without SDK: Binary health (up/down)
With SDK:    Component health (database: healthy, cache: degraded)
```

**3. Build Metadata**
```
Git commit, branch, build timestamp, language version
```

### Integration Tiers

```
Tier 0 (No SDK - Default)
  - Passive agent observation
  - netstat/ss for connections
  - /proc for process stats
  - HTTP health endpoint polling
  └─> Works everywhere, zero integration

Tier 1 (Lightweight SDK - 5 minutes)
  - gRPC health/version endpoints
  - Endpoint discovery config
  - Structured build metadata
  └─> Accurate versioning, component health

Tier 2 (Enhanced SDK - Future)
  - Custom health checks
  - Business metrics
  - Trace context propagation
  └─> Deep insights, custom correlations
```

---

## MCP Integration

Coral acts as both **MCP client** (queries tools) and **MCP server** (exports intelligence).

### As MCP Client

Coral can query external tools for enrichment:

```
When to query MCPs:
├─ User request: "Check Grafana too"
├─ Low confidence: Need more evidence
└─ Configuration: mcp.auto_query = true (future)

Available MCPs:
├─ Grafana: Long-term metrics, complex queries
├─ Sentry: Error grouping, stack traces
├─ PagerDuty: Historical incidents
└─ Custom: Your internal tools/runbooks
```

**Important**: MCP queries are optional enrichment. Core intelligence works from local data alone.

### As MCP Server

Coral exports its intelligence via MCP:

```
Exposed resources:
├─ coral://topology - Auto-discovered service graph
├─ coral://events - Deployment, crash, anomaly events
├─ coral://baselines - Learned normal behavior
└─ coral://correlations - Cross-service event correlations

Consumers:
├─ Claude Desktop (ask Coral about your app)
├─ Other AI assistants
└─ Custom integrations
```

---

## Intelligence Layer: How AI Understands

### Continuous Sensing

Agents collect rich local data:
- Process state and resource usage
- Network connection lifecycle
- Health check results
- Metrics from Prometheus endpoints (optional)

Data flows:
1. Agent observes locally (every 10-30s)
2. Agent stores raw data locally (~1 hour retention)
3. Agent sends compressed summary to colony
4. Colony aggregates and correlates summaries
5. Colony queries agent for details when investigating

### AI Synthesis

The LLM creates emergent understanding:
1. **Pattern recognition**: "Deploy → crash → error spike = deployment issue"
2. **Correlation**: Events across multiple agents
3. **Hypothesis generation**: "Memory leak in v2.3.0 (85% confidence)"
4. **Evidence gathering**: Queries relevant data sources
5. **Natural language explanation**: "What" + "Why" + "How to fix"

### Example Investigation Flow

```
[14:10:15] Agent: "API crashed (OOM)"
           └─> Colony: Anomaly detected

[14:10:16] Colony queries LOCAL summaries (DuckDB):
           ├─> Events: v2.3.0 deployed 10min ago
           ├─> Time-series: Memory 250MB→512MB (linear)
           ├─> Topology: API → DB (healthy), Cache (healthy)
           └─> History: v2.2.5 stable for 3 days

[14:10:16] AI synthesis (from local data):
           "Memory leak in v2.3.0 (85% confidence)
            Evidence: Linear growth post-deploy
            Recommendation: Rollback to v2.2.5"

[Optional] User: "Check Sentry too"

[14:10:18] Colony queries Sentry MCP:
           └─> 47 OOM exceptions in connection pool

[14:10:18] Enhanced synthesis:
           "Connection pool leak (95% confidence)
            Sentry confirms: All exceptions in pool code
            Recommendation: Rollback or patch pool"
```

**Key Insight**: Local intelligence (85% confidence) in <1s. MCP enrichment (95% confidence) in ~6s.

---

## Design Constraints

### Must Have

- ✅ Self-sufficient from local data (air-gap compatible)
- ✅ Control plane only (never in data path)
- ✅ User-controlled infrastructure (no SaaS dependency)
- ✅ Fast inference (<1s from local summaries)
- ✅ Human-in-the-loop for actions
- ✅ Application-scoped (one colony per app)

### Must NOT

- ❌ Require external MCPs to function
- ❌ Replace existing tools (enhance, don't duplicate)
- ❌ Operate in data path (can't impact app performance)
- ❌ Auto-execute dangerous operations
- ❌ Send data to cloud/SaaS

---

## Similar Projects & Inspiration

**Learn from**:
- **Tailscale**: Mesh networking, coordination server model
- **Netdata**: Real-time monitoring with local intelligence
- **Dapr**: Distributed app building blocks
- **Grafana**: Multi-source visualization

**Differentiation**:
- Unlike service meshes: Control plane only, not in data path
- Unlike monitoring tools: Agentic AI, not passive dashboards
- Unlike SaaS: User-controlled, air-gap compatible
- Unlike platforms: Focused on intelligence, not storage/collection

---

*For implementation details, see IMPLEMENTATION.md*
