# Coral - Technical Implementation Guide

**Version**: 0.1 (Design Phase)
**Last Updated**: 2025-10-27

---

## Tech Stack

### Colony

- **Language**: Go
- **Storage**: DuckDB (embedded, analytical queries, time-series optimization)
- **Networking**: wireguard-go (pure Go implementation)
- **API**: Buf Connect (agents) + HTTP/WebSocket (dashboard, CLI)
- **Query Federation**: gRPC to agents for on-demand detailed data
- **AI**:
  - HTTP clients for Anthropic/OpenAI APIs
  - ONNX runtime for local models
- **Web**: Embedded static assets (React/Vue compiled)

### Agent

- **Language**: Go
- **Storage**: DuckDB (embedded, ~6 hours raw data retention)
- **Size**: <10MB binary, <50MB RAM usage (including local DuckDB)
- **Deps**: Minimal (networking, process monitoring, local storage)
- **Query API**: gRPC endpoints for colony to query local data

### CLI

- **Language**: Go (single binary distribution)
- **UI**: [Bubble Tea](https://github.com/charmbracelet/bubbletea) for rich TUI
- **Packaging**: Homebrew, apt, Docker, direct download

### Discovery Service

- **Language**: Go
- **Storage**: In-memory + Redis (for multi-instance redundancy)
- **Scale**: Single $5/mo VPS handles thousands of colonies

### Dashboard

- **Frontend**: React or Svelte (embedded in coral binary)
- **Visualization**: D3.js (topology), Recharts (time series)
- **Real-time**: WebSocket for live updates

---

## Design

### Colony (Your Application's Brain)

**Purpose**: Makes your application self-aware - aggregates observations and provides AI-powered insights

**Where it runs**: On your laptop during development, alongside your app in production

**Responsibilities**:
- Manage control plane mesh (agent connectivity only)
- Accept agent connections from your app components
- Store application data locally (in your project directory)
- Run AI analysis and correlation
- Serve dashboard and CLI API
- Generate insights and recommendations about your app

**Storage** (Layered DuckDB Architecture - see DESIGN.md for full details):

**Colony DuckDB Schema** (Summaries + History):
```sql
-- Components in the application
CREATE TABLE services (
  id TEXT PRIMARY KEY,
  name TEXT,  -- frontend, api, database, worker, etc.
  app_id TEXT,  -- application identifier
  version TEXT,
  agent_id TEXT,
  labels JSONB,
  last_seen TIMESTAMP,
  status TEXT
);

-- Aggregated metrics (downsampled summaries from agents)
CREATE TABLE metric_summaries (
  timestamp TIMESTAMP,
  service_id TEXT,
  metric_name TEXT,
  interval TEXT,  -- '5m', '15m', '1h', '1d'
  p50 DOUBLE,
  p95 DOUBLE,
  p99 DOUBLE,
  mean DOUBLE,
  max DOUBLE,
  count INTEGER,
  PRIMARY KEY (timestamp, service_id, metric_name, interval)
);

-- Event log (important events from agents)
CREATE TABLE events (
  id INTEGER PRIMARY KEY,
  timestamp TIMESTAMP,
  service_id TEXT,
  event_type TEXT,  -- deploy, crash, restart, alert, connection
  details JSONB,
  correlation_group TEXT
);

-- AI-generated insights
CREATE TABLE insights (
  id INTEGER PRIMARY KEY,
  created_at TIMESTAMP,
  insight_type TEXT,  -- anomaly, pattern, recommendation
  priority TEXT,  -- high, medium, low
  title TEXT,
  summary TEXT,
  details JSONB,
  affected_services TEXT[],
  status TEXT,  -- active, dismissed, resolved
  confidence DOUBLE,
  expires_at TIMESTAMP
);

-- Service topology (auto-discovered)
CREATE TABLE service_connections (
  from_service TEXT,
  to_service TEXT,
  protocol TEXT,
  first_observed TIMESTAMP,
  last_observed TIMESTAMP,
  connection_count INTEGER,
  PRIMARY KEY (from_service, to_service, protocol)
);

-- Learned baselines
CREATE TABLE baselines (
  service_id TEXT,
  metric_name TEXT,
  time_window TEXT,  -- '1h', '1d', '7d'
  mean DOUBLE,
  stddev DOUBLE,
  p50 DOUBLE,
  p95 DOUBLE,
  p99 DOUBLE,
  sample_count INTEGER,
  last_updated TIMESTAMP,
  PRIMARY KEY (service_id, metric_name, time_window)
);
```

**Agent DuckDB Schema** (Recent Raw Data ~6 hours):
```sql
-- Time-series metrics (high-resolution)
CREATE TABLE metrics (
  timestamp TIMESTAMP,
  metric_name TEXT,
  value DOUBLE,
  labels JSONB,
  INDEX idx_timestamp (timestamp)
);

-- Process events (local)
CREATE TABLE events (
  id INTEGER PRIMARY KEY,
  timestamp TIMESTAMP,
  event_type TEXT,  -- start, stop, crash, deploy
  process_name TEXT,
  details JSONB
);

-- Network connections (current state)
CREATE TABLE connections (
  local_addr TEXT,
  remote_addr TEXT,
  state TEXT,
  observed_at TIMESTAMP
);

-- Process state snapshots
CREATE TABLE process_snapshots (
  timestamp TIMESTAMP,
  pid INTEGER,
  cpu_percent DOUBLE,
  memory_bytes BIGINT,
  fd_count INTEGER,
  thread_count INTEGER
);
```

**Configuration** (`.coral/config.yaml` in your project directory):
```yaml
colony:
  app_name: my-shop       # Your application name
  environment: dev        # dev, staging, prod
  colony_id: my-shop-dev  # Unique colony identifier

  storage:
    type: duckdb          # embedded analytical database
    path: .coral/colony.duckdb  # Stored in your project
    # Data retention policies
    retention:
      metrics_5m: 7d      # 5-minute resolution for 7 days
      metrics_15m: 30d    # 15-minute resolution for 30 days
      metrics_1h: 90d     # 1-hour resolution for 90 days
      metrics_1d: 1y      # 1-day resolution for 1 year
      events: 30d         # Event log retention
      insights: 90d       # AI insights retention

  wireguard:
    port: 41820  # Non-standard port to avoid conflicts (standard WG is 51820)
    interface: coral0
    network: 10.100.0.0/16

  dashboard:
    enabled: true
    port: 3000

agent:
  storage:
    type: duckdb          # embedded local storage
    path: ./agent.duckdb
    retention: 1h         # Keep raw data for 1 hour
    cleanup_interval: 5m  # Clean up old data every 5 minutes

  # Summary push frequency
  summary_interval: 60s   # Push summaries to colony every 60s

  # Query endpoint for colony
  query_api:
    enabled: true
    port: 6001            # gRPC port for colony queries

discovery:
  endpoint: https://discovery.coral.io
  # or self-host: https://disco.mycompany.com

ai:
  provider: anthropic      # or openai
  model: claude-3-5-sonnet-20241022
  api_key: ${ANTHROPIC_API_KEY}

  # Optional: local models for simple tasks
  local:
    enabled: true
    models_path: ./models

  # Cost control
  rate_limit: 100          # calls per hour
  max_cost_daily: 5.00     # USD

  # Caching
  cache_ttl: 300           # seconds

mcp:
  # Enable MCP server to expose Coral data
  server:
    enabled: true
    transport: stdio       # or sse
    port: 3001             # if using sse transport

  # MCP clients for external tools
  servers:
    grafana:
      command: "npx"
      args: ["-y", "@grafana/mcp-server"]
      env:
        GRAFANA_URL: ${GRAFANA_URL}
        GRAFANA_TOKEN: ${GRAFANA_TOKEN}

    sentry:
      command: "npx"
      args: ["-y", "@sentry/mcp-server"]
      env:
        SENTRY_DSN: ${SENTRY_DSN}
        SENTRY_ORG: ${SENTRY_ORG}

security:
  mesh_password: "..."     # optional additional auth
  tls:
    enabled: true
    cert: ./certs/colony.crt
    key: ./certs/colony.key
```

**Deployment Options**:

**Development (Local)**:
```bash
# In your project directory
$ cd ~/projects/my-shop
$ coral colony start

# Runs locally, stores data in .coral/
# Dashboard at http://localhost:3000
```

**Production (Deployed with your app)**:
```bash
# Run as daemon on your server
$ coral colony start --daemon

# Docker (as part of docker-compose)
$ docker run -v .coral:/data \
  -e ANTHROPIC_API_KEY=sk-ant-... \
  coral/colony

# Kubernetes (deployed in same namespace as your app)
$ kubectl apply -f colony.yaml
```

### Agent (Runs with Apps)

**Purpose**: Lightweight observer that monitors apps locally and relays observations to colony

**Critical**: Agents are **observers, not proxies**. They run alongside your apps and watch them, but never intercept, route, or proxy application traffic.

**Responsibilities**:
- Establish Wireguard tunnel to colony (control plane only)
- Observe application locally (health, process state, resource usage)
- Detect network connections via local introspection (netstat/ss)
- Report events and observations to colony
- Execute local queries (e.g., feature flag checks - future)

**How agents observe** (all local, no proxying):
```go
// Example: Agent observes locally
type Agent struct {
    app *Application
}

func (a *Agent) Observe() Observation {
    return Observation{
        // Process info via OS APIs
        Health: a.callHealthEndpoint(),      // GET http://localhost:8080/health
        CPU: a.getCPUUsage(),                // via /proc or OS APIs
        Memory: a.getMemoryUsage(),          // via /proc or OS APIs

        // Network topology via netstat/ss
        Connections: a.getNetworkConnections(), // netstat -an

        // Version from env/labels
        Version: os.Getenv("APP_VERSION"),

        // Metrics scraping (if exposed)
        Metrics: a.scrapeMetrics(),          // GET http://localhost:9090/metrics
    }
}
```

**Installation**:

**Development (Local)**:
```bash
# Simple connection by name
$ coral connect frontend --port 3000
$ coral connect api --port 8080
$ coral connect database --port 5432

# Automatically discovers:
# - Running process
# - Dependencies
# - Connects to local colony
```

**Production**:
```bash
# Binary with explicit config
$ coral connect api \
  --colony-id my-shop-prod \
  --port 8080 \
  --tags version=2.1.0

# Systemd service
$ coral connect api --colony-id my-shop-prod --daemon

# Docker sidecar (in docker-compose.yml)
services:
  api:
    image: myapp:2.1.0
    ports: ["8080:8080"]

  coral-agent-api:
    image: coral/agent
    network_mode: "service:api"  # Shares network with api
    environment:
      CORAL_COLONY_ID: my-shop-prod
      CORAL_COMPONENT: api
      CORAL_PORT: 8080

# Kubernetes sidecar
apiVersion: v1
kind: Pod
metadata:
  name: api
spec:
  containers:
  - name: api
    image: myapp:2.1.0
    ports:
    - containerPort: 8080

  - name: coral-agent
    image: coral/agent
    env:
    - name: CORAL_COLONY_ID
      value: my-shop-prod
    - name: CORAL_COMPONENT
      value: api
    - name: CORAL_PORT
      value: "8080"
```

**Agent Protocol** (gRPC):
```protobuf
// Agent → Colony (push summaries)
service CoralMesh {
  // Initial connection
  rpc Register(RegisterRequest) returns (RegisterResponse);

  // Periodic health check
  rpc Heartbeat(HeartbeatRequest) returns (HeartbeatResponse);

  // Report events
  rpc ReportEvent(Event) returns (EventAck);

  // Push metric summaries
  rpc PushSummary(MetricSummary) returns (SummaryAck);

  // Query coordination state
  rpc Query(QueryRequest) returns (QueryResponse);
}

message Event {
  string app_id = 1;
  string type = 2;  // restart, deploy, crash, connection, metric_spike
  google.protobuf.Timestamp timestamp = 3;
  map<string, string> tags = 4;
  bytes data = 5;  // flexible JSON payload
}

message MetricSummary {
  string agent_id = 1;
  string service_id = 2;
  google.protobuf.Timestamp timestamp = 3;
  repeated MetricAggregation metrics = 4;
  repeated Event events = 5;
}

message MetricAggregation {
  string metric_name = 1;
  double p50 = 2;
  double p95 = 3;
  double p99 = 4;
  double mean = 5;
  double max = 6;
  int64 count = 7;
}

// Colony → Agent (query for details)
service CoralAgent {
  // Query agent's local DuckDB
  rpc QueryMetrics(MetricsQueryRequest) returns (MetricsQueryResponse);

  // Get recent events
  rpc GetEvents(EventsQueryRequest) returns (EventsQueryResponse);

  // Get process snapshots
  rpc GetProcessSnapshots(ProcessSnapshotRequest) returns (ProcessSnapshotResponse);
}

message MetricsQueryRequest {
  string metric_name = 1;
  google.protobuf.Timestamp start_time = 2;
  google.protobuf.Timestamp end_time = 3;
  string resolution = 4;  // "1s", "10s", "1m"
  map<string, string> filters = 5;
}

message MetricsQueryResponse {
  repeated MetricPoint points = 1;
}

message MetricPoint {
  google.protobuf.Timestamp timestamp = 1;
  double value = 2;
  map<string, string> labels = 3;
}

message EventsQueryRequest {
  google.protobuf.Timestamp start_time = 1;
  google.protobuf.Timestamp end_time = 2;
  repeated string event_types = 3;  // Filter by event types
}

message EventsQueryResponse {
  repeated Event events = 1;
}

message ProcessSnapshotRequest {
  google.protobuf.Timestamp start_time = 1;
  google.protobuf.Timestamp end_time = 2;
  int32 pid = 3;  // Optional: filter by PID
}

message ProcessSnapshotResponse {
  repeated ProcessSnapshot snapshots = 1;
}

message ProcessSnapshot {
  google.protobuf.Timestamp timestamp = 1;
  int32 pid = 2;
  double cpu_percent = 3;
  int64 memory_bytes = 4;
  int32 fd_count = 5;
  int32 thread_count = 6;
}
```

**Resource Usage Target**:
- Memory: <50MB (including local DuckDB with 1 hour data)
- CPU: <0.1% average
- Network: <1KB/s (heartbeats + summaries)
- Disk: ~10-50MB (1 hour of raw metrics, auto-cleaned)


---

## Key Libraries

### Networking

- [wireguard-go](https://git.zx2c4.com/wireguard-go) - Pure Go Wireguard implementation
- [netlink](https://github.com/vishvananda/netlink) - Network interface management
- [Buf Connect](https://github.com/connectrpc/connect-go) - Type-safe RPC framework (agent protocol)

### Storage

- [DuckDB Go](https://github.com/marcboeker/go-duckdb) - DuckDB driver for Go
- Alternative: [DuckDB CGO](https://github.com/duckdb/duckdb/tree/master/tools/go) - Official CGO bindings
- **Why DuckDB**: Columnar storage optimized for analytical queries, perfect for time-series data

### AI

- [Anthropic Go SDK](https://github.com/anthropics/anthropic-sdk-go)
- [OpenAI Go SDK](https://github.com/sashabaranov/go-openai)
- [ONNX Runtime Go](https://github.com/yalue/onnxruntime_go) - Local inference

### MCP Integration

- [MCP Go SDK](https://github.com/mark3labs/mcp-go) - MCP client and server implementation
- [MCP Protocol Spec](https://spec.modelcontextprotocol.io) - Official protocol specification
- Alternative: Custom implementation following JSON-RPC 2.0 over stdio/SSE

### Application SDK

- [grpc-go](https://github.com/grpc/grpc-go) - gRPC framework for Go
- [protobuf](https://github.com/protocolbuffers/protobuf-go) - Protocol Buffers for Go
- [Prometheus client_golang](https://github.com/prometheus/client_golang) - For metrics scraping
- [cilium/ebpf](https://github.com/cilium/ebpf) - eBPF support (optional, Tier 3)

### Observability

- [gopsutil](https://github.com/shirou/gopsutil) - Process/system monitoring
- [PromQL](https://prometheus.io/docs/prometheus/latest/querying/basics/) - Query existing metrics

### CLI

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) - Terminal UI framework
- [Cobra](https://github.com/spf13/cobra) - CLI framework
- [Viper](https://github.com/spf13/viper) - Configuration management

---

## MCP Integration Architecture

### Coral as MCP Client

Coral colony acts as an MCP client to query specialized tools (Grafana, Sentry, PagerDuty, etc.):

**Architecture**:
```go
// pkg/mcp/client.go
package mcp

import (
    "context"
    "github.com/mark3labs/mcp-go/client"
)

type MCPClientManager struct {
    clients map[string]*client.StdioMCPClient
}

// Initialize MCP servers from config
func (m *MCPClientManager) Initialize(config *Config) error {
    for name, serverConfig := range config.MCP.Servers {
        client, err := client.NewStdioMCPClient(
            serverConfig.Command,
            serverConfig.Args,
            serverConfig.Env,
        )
        if err != nil {
            return fmt.Errorf("failed to start MCP server %s: %w", name, err)
        }
        m.clients[name] = client
    }
    return nil
}

// Call a tool on a specific MCP server
func (m *MCPClientManager) CallTool(
    ctx context.Context,
    serverName string,
    toolName string,
    arguments map[string]interface{},
) (interface{}, error) {
    client, ok := m.clients[serverName]
    if !ok {
        return nil, fmt.Errorf("MCP server %s not found", serverName)
    }

    result, err := client.CallTool(ctx, toolName, arguments)
    if err != nil {
        return nil, fmt.Errorf("tool call failed: %w", err)
    }

    return result, nil
}
```

**Example Usage in AI Orchestrator**:
```go
// pkg/ai/orchestrator.go
func (o *Orchestrator) AnalyzeIncident(ctx context.Context, incident *Incident) (*Analysis, error) {
    // 1. Get Coral's own data
    events, err := o.db.QueryEvents(incident.ServiceName, incident.TimeRange)

    // 2. Call Grafana MCP for metrics
    metricsResult, err := o.mcpClient.CallTool(ctx, "grafana", "query_metrics", map[string]interface{}{
        "service": incident.ServiceName,
        "metric": "response_time_p95",
        "range": incident.TimeRange,
    })

    // 3. Call Sentry MCP for errors
    errorsResult, err := o.mcpClient.CallTool(ctx, "sentry", "query_errors", map[string]interface{}{
        "service": incident.ServiceName,
        "time_range": incident.TimeRange,
    })

    // 4. Synthesize with AI
    prompt := o.buildPrompt(events, metricsResult, errorsResult)
    analysis, err := o.aiClient.Analyze(ctx, prompt)

    return analysis, nil
}
```

### Coral as MCP Server

Coral colony exposes its own data as an MCP server for other AI assistants:

**Implementation**:
```go
// pkg/mcp/server.go
package mcp

import (
    "github.com/mark3labs/mcp-go/server"
)

type CoralMCPServer struct {
    db        *Database
    topology  *TopologyService
}

func NewCoralMCPServer(db *Database, topology *TopologyService) *CoralMCPServer {
    return &CoralMCPServer{
        db:       db,
        topology: topology,
    }
}

func (s *CoralMCPServer) Initialize() error {
    mcpServer := server.NewStdioMCPServer("coral", "1.0.0")

    // Register tools
    mcpServer.AddTool(server.Tool{
        Name:        "coral_get_topology",
        Description: "Get current service topology and dependencies",
        InputSchema: map[string]interface{}{
            "type": "object",
            "properties": map[string]interface{}{
                "filter": map[string]interface{}{
                    "type": "string",
                    "description": "Filter by service name, tag, or region",
                },
            },
        },
        Handler: s.handleGetTopology,
    })

    mcpServer.AddTool(server.Tool{
        Name:        "coral_query_events",
        Description: "Query deployment and operational events",
        InputSchema: map[string]interface{}{
            "type": "object",
            "properties": map[string]interface{}{
                "service": map[string]string{"type": "string"},
                "event_type": map[string]interface{}{
                    "type": "string",
                    "enum": []string{"deploy", "restart", "crash", "connection", "alert"},
                },
                "time_range": map[string]string{"type": "string"},
            },
        },
        Handler: s.handleQueryEvents,
    })

    mcpServer.AddTool(server.Tool{
        Name:        "coral_analyze_correlation",
        Description: "Correlate events across services",
        InputSchema: map[string]interface{}{
            "type": "object",
            "properties": map[string]interface{}{
                "incident_time": map[string]string{"type": "string"},
                "affected_services": map[string]interface{}{
                    "type": "array",
                    "items": map[string]string{"type": "string"},
                },
            },
        },
        Handler: s.handleAnalyzeCorrelation,
    })

    return mcpServer.Serve()
}

func (s *CoralMCPServer) handleGetTopology(args map[string]interface{}) (interface{}, error) {
    filter, _ := args["filter"].(string)
    topology, err := s.topology.GetTopology(filter)
    if err != nil {
        return nil, err
    }
    return topology, nil
}

func (s *CoralMCPServer) handleQueryEvents(args map[string]interface{}) (interface{}, error) {
    service, _ := args["service"].(string)
    eventType, _ := args["event_type"].(string)
    timeRange, _ := args["time_range"].(string)

    events, err := s.db.QueryEvents(service, eventType, timeRange)
    if err != nil {
        return nil, err
    }
    return events, nil
}

func (s *CoralMCPServer) handleAnalyzeCorrelation(args map[string]interface{}) (interface{}, error) {
    incidentTime, _ := args["incident_time"].(string)
    services, _ := args["affected_services"].([]interface{})

    correlation, err := s.db.AnalyzeCorrelation(incidentTime, services)
    if err != nil {
        return nil, err
    }
    return correlation, nil
}
```

### MCP Server Manifest

When running as an MCP server, Coral provides this manifest (for Claude Desktop, etc.):

**`~/.config/claude/mcp.json`**:
```json
{
  "mcpServers": {
    "coral": {
      "command": "coral",
      "args": ["mcp", "server"],
      "env": {
        "CORAL_CONFIG": "/Users/you/.coral/config.yaml"
      }
    }
  }
}
```

### Configuration Schema

**`~/.coral/config.yaml`** (MCP section):
```yaml
mcp:
  # MCP Server (expose Coral data)
  server:
    enabled: true
    transport: stdio        # stdio or sse
    port: 3001             # only for sse transport
    tools:
      - coral_get_topology
      - coral_query_events
      - coral_analyze_correlation
      - coral_get_insights

  # MCP Clients (query external tools)
  servers:
    grafana:
      command: "npx"
      args: ["-y", "@grafana/mcp-server"]
      env:
        GRAFANA_URL: "https://grafana.company.com"
        GRAFANA_TOKEN: "${GRAFANA_TOKEN}"
      timeout: 30s
      retry:
        max_attempts: 3
        backoff: exponential

    sentry:
      command: "npx"
      args: ["-y", "@sentry/mcp-server"]
      env:
        SENTRY_DSN: "${SENTRY_DSN}"
        SENTRY_ORG: "my-org"
      timeout: 30s

    pagerduty:
      command: "npx"
      args: ["-y", "@pagerduty/mcp-server"]
      env:
        PD_API_KEY: "${PD_API_KEY}"
      timeout: 30s

    # Custom company MCP server
    internal-tools:
      command: "/usr/local/bin/company-mcp-server"
      env:
        API_ENDPOINT: "https://internal.company.com"
      timeout: 60s
```

### Data Flow: MCP Orchestration

```
┌─────────────────────────────────────────────────────────┐
│  User: "Why did the API crash?"                          │
└──────────────────┬──────────────────────────────────────┘
                   │
                   ▼
         ┌─────────────────────┐
         │  Coral Colony  │
         │   AI Orchestrator   │
         └──────────┬──────────┘
                    │
        ┌───────────┼───────────┬───────────┐
        │           │           │           │
        ▼           ▼           ▼           ▼
   ┌────────┐ ┌─────────┐ ┌────────┐ ┌─────────┐
   │ Coral  │ │Grafana  │ │Sentry  │ │PagerDuty│
   │  DB    │ │  MCP    │ │  MCP   │ │  MCP    │
   └────┬───┘ └────┬────┘ └────┬───┘ └────┬────┘
        │          │           │          │
        ▼          ▼           ▼          ▼
   Events:    Metrics:    Errors:    Incidents:
   3 restarts Memory      OOMError   2 auto-
   in 1 hour  spike 95%   47 times   resolved
        │          │           │          │
        └──────────┴───────────┴──────────┘
                      │
                      ▼
            ┌─────────────────┐
            │   AI Synthesis  │
            │  (Claude/GPT)   │
            └────────┬────────┘
                     │
                     ▼
         ┌───────────────────────┐
         │  Root Cause + Actions │
         │  "Memory leak in      │
         │   v2.3.0, rollback"   │
         └───────────────────────┘
```

---

## Application SDK Architecture

### Overview

The Coral SDK enables applications to provide structured data to agents for enhanced observability. The SDK is **optional** - agents work via passive observation without it, but SDK integration unlocks critical features.

**Design Principle**: Standards-first approach
- Use Prometheus for metrics (don't create custom protocol)
- Use OpenTelemetry for traces
- SDK provides structured data where standards don't exist

### SDK Components

```
┌──────────────────────────────────────┐
│  Application Process                 │
│                                      │
│  ┌────────────────────────────────┐ │
│  │  App Code (user's logic)       │ │
│  └────────────────────────────────┘ │
│                                      │
│  ┌────────────────────────────────┐ │
│  │  Coral SDK (coral-io/sdk-go)   │ │
│  │                                 │ │
│  │  ┌──────────────────────────┐  │ │
│  │  │ gRPC Server (:6000)      │  │ │
│  │  │ - GetHealth()            │  │ │
│  │  │ - GetInfo()              │  │ │
│  │  │ - GetConfig()            │  │ │
│  │  └──────────────────────────┘  │ │
│  │                                 │ │
│  │  ┌──────────────────────────┐  │ │
│  │  │ Health Check Registry    │  │ │
│  │  │ - Database checks        │  │ │
│  │  │ - Cache checks           │  │ │
│  │  │ - Custom checks          │  │ │
│  │  └──────────────────────────┘  │ │
│  │                                 │ │
│  │  ┌──────────────────────────┐  │ │
│  │  │ Build Metadata           │  │ │
│  │  │ - Version, git commit    │  │ │
│  │  │ - Build time             │  │ │
│  │  │ - Runtime info           │  │ │
│  │  └──────────────────────────┘  │ │
│  └────────────────────────────────┘ │
└──────────────────────────────────────┘
         ▲
         │ gRPC (localhost only)
         │
    ┌────┴─────────────┐
    │  Coral Agent     │
    │  (same host)     │
    └──────────────────┘
```

### Protobuf Schema (Full Specification)

**File: `proto/coral/sdk/v1/sdk.proto`**

```protobuf
syntax = "proto3";
package coral.sdk.v1;

import "google/protobuf/timestamp.proto";
import "google/protobuf/duration.proto";

option go_package = "github.com/coral-io/sdk-go/proto/sdk/v1;sdkpb";

// Coral SDK Service
// Applications implement this interface for enhanced Coral integration
service CoralApp {
  // Get health status (overall and per-component)
  rpc GetHealth(HealthRequest) returns (HealthResponse);

  // Get version and build metadata
  rpc GetInfo(InfoRequest) returns (InfoResponse);

  // Get configuration (where to find standard endpoints)
  rpc GetConfig(ConfigRequest) returns (ConfigResponse);
}

// ========== Health Check ==========

message HealthRequest {
  // Optional component filter
  // Empty = overall health + all components
  // "database" = only database component
  string component = 1;
}

message HealthResponse {
  enum Status {
    UNKNOWN = 0;
    HEALTHY = 1;
    DEGRADED = 2;
    UNHEALTHY = 3;
  }

  // Overall status (worst of all components)
  Status overall_status = 1;

  // Component-level health
  repeated ComponentHealth components = 2;

  // Optional message
  string message = 3;

  // When was this health check performed
  google.protobuf.Timestamp checked_at = 4;
}

message ComponentHealth {
  // Component name (e.g., "database", "cache", "payment-gateway")
  string name = 1;

  // Component status
  HealthResponse.Status status = 2;

  // Optional status message
  string message = 3;

  // How long the health check took
  google.protobuf.Duration response_time = 4;

  // Additional metadata
  map<string, string> metadata = 5;

  // Last successful check (for UNHEALTHY components)
  google.protobuf.Timestamp last_success = 6;
}

// ========== Version and Build Info ==========

message InfoRequest {}

message InfoResponse {
  // Service identification
  string service_name = 1;
  string service_version = 2;
  string service_namespace = 3;  // e.g., "production", "staging"

  // Git metadata
  string git_commit = 4;      // Short or full SHA
  string git_branch = 5;      // Branch name
  bool git_dirty = 6;         // Uncommitted changes?
  string git_tag = 7;         // Tag if built from tag

  // Build metadata
  google.protobuf.Timestamp build_time = 8;
  string builder = 9;         // CI system, user, etc.
  string go_version = 10;     // Go version (or other runtime)

  // Runtime metadata
  google.protobuf.Timestamp start_time = 11;
  google.protobuf.Duration uptime = 12;
  string hostname = 13;

  // Deployment metadata
  map<string, string> labels = 14;  // Arbitrary key-value pairs
  string environment = 15;          // prod, staging, dev
  string region = 16;               // us-east-1, eu-west-1, etc.
  string availability_zone = 17;

  // SDK version
  string sdk_version = 18;
}

// ========== Configuration ==========

message ConfigRequest {}

message ConfigResponse {
  // Standard endpoints (Prometheus, OTEL, etc.)
  string metrics_endpoint = 1;     // Prometheus: "http://localhost:8080/metrics"
  string traces_endpoint = 2;      // OTEL: "http://localhost:9411/traces"
  string pprof_endpoint = 3;       // Go profiling: "http://localhost:6060/debug/pprof"
  string health_endpoint = 4;      // HTTP health: "http://localhost:8080/healthz"

  // Custom application-specific endpoints
  map<string, string> custom_endpoints = 5;

  // Endpoint authentication (if needed)
  message EndpointAuth {
    enum Type {
      NONE = 0;
      BASIC = 1;
      BEARER = 2;
      MTLS = 3;
    }
    Type type = 1;
    string username = 2;  // For BASIC
    string password = 3;  // For BASIC
    string token = 4;     // For BEARER
  }
  map<string, EndpointAuth> endpoint_auth = 6;
}
```

### Go SDK Implementation

**Package Structure:**

```
github.com/coral-io/sdk-go/
├── coral.go              # Main API
├── config.go             # Configuration types
├── health.go             # Health check registry
├── server.go             # gRPC server
├── metadata.go           # Build metadata collection
├── proto/
│   └── sdk/
│       └── v1/
│           ├── sdk.proto
│           └── sdk.pb.go (generated)
└── examples/
    ├── basic/
    ├── health-checks/
    └── custom-metadata/
```

**Main API (`coral.go`):**

```go
package coral

import (
    "context"
    "fmt"
    "net"
    "time"

    "google.golang.org/grpc"
    sdkpb "github.com/coral-io/sdk-go/proto/sdk/v1"
)

// Initialize starts the Coral SDK
func Initialize(cfg Config) error {
    // Validate config
    if err := cfg.Validate(); err != nil {
        return fmt.Errorf("invalid config: %w", err)
    }

    // Create gRPC server
    server := NewCoralServer(cfg)

    // Start gRPC listener
    lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
    if err != nil {
        return fmt.Errorf("failed to listen: %w", err)
    }

    grpcServer := grpc.NewServer()
    sdkpb.RegisterCoralAppServer(grpcServer, server)

    // Start server in background
    go func() {
        if err := grpcServer.Serve(lis); err != nil {
            fmt.Printf("gRPC server error: %v\n", err)
        }
    }()

    // Store for shutdown
    globalServer = &sdkServer{
        grpc:   grpcServer,
        server: server,
    }

    return nil
}

// Shutdown stops the SDK gracefully
func Shutdown() error {
    if globalServer != nil {
        globalServer.grpc.GracefulStop()
    }
    return nil
}
```

**Configuration (`config.go`):**

```go
package coral

type Config struct {
    // Service identification
    ServiceName string
    Version     string
    Environment string
    Region      string

    // SDK configuration
    Port int  // Default: 6000

    // Standard endpoints
    Endpoints Endpoints

    // Health checks
    HealthChecks []HealthCheck

    // Custom labels
    Labels map[string]string
}

type Endpoints struct {
    Metrics string  // Prometheus endpoint
    Traces  string  // OTEL traces
    Pprof   string  // Go pprof
    Health  string  // HTTP health check
    Custom  map[string]string
}

func (c *Config) Validate() error {
    if c.ServiceName == "" {
        return fmt.Errorf("ServiceName is required")
    }
    if c.Version == "" {
        return fmt.Errorf("Version is required")
    }
    if c.Port == 0 {
        c.Port = 6000  // Default
    }
    return nil
}
```

**Health Check Registry (`health.go`):**

```go
package coral

import (
    "context"
    "time"
)

type HealthCheck func(ctx context.Context) HealthCheckResult

type HealthCheckResult struct {
    Name         string
    Status       Status
    Message      string
    ResponseTime time.Duration
    Metadata     map[string]string
}

type Status int

const (
    StatusUnknown Status = iota
    StatusHealthy
    StatusDegraded
    StatusUnhealthy
)

// Built-in health checks

func DatabaseHealthCheck(db *sql.DB) HealthCheck {
    return func(ctx context.Context) HealthCheckResult {
        start := time.Now()
        err := db.PingContext(ctx)
        elapsed := time.Since(start)

        if err != nil {
            return HealthCheckResult{
                Name:         "database",
                Status:       StatusUnhealthy,
                Message:      err.Error(),
                ResponseTime: elapsed,
            }
        }

        return HealthCheckResult{
            Name:         "database",
            Status:       StatusHealthy,
            ResponseTime: elapsed,
        }
    }
}

func RedisHealthCheck(client *redis.Client) HealthCheck {
    return func(ctx context.Context) HealthCheckResult {
        start := time.Now()
        _, err := client.Ping(ctx).Result()
        elapsed := time.Since(start)

        if err != nil {
            return HealthCheckResult{
                Name:         "cache",
                Status:       StatusUnhealthy,
                Message:      err.Error(),
                ResponseTime: elapsed,
            }
        }

        return HealthCheckResult{
            Name:         "cache",
            Status:       StatusHealthy,
            ResponseTime: elapsed,
        }
    }
}
```

### Agent SDK Client

**Agent code to query SDK:**

```go
package agent

import (
    "context"
    "time"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"
    sdkpb "github.com/coral-io/sdk-go/proto/sdk/v1"
)

type SDKClient struct {
    client sdkpb.CoralAppClient
    conn   *grpc.ClientConn
}

func NewSDKClient(address string) (*SDKClient, error) {
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()

    // Try to connect to SDK gRPC server
    conn, err := grpc.DialContext(ctx, address,
        grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithBlock(),
    )
    if err != nil {
        return nil, err
    }

    return &SDKClient{
        client: sdkpb.NewCoralAppClient(conn),
        conn:   conn,
    }, nil
}

func (c *SDKClient) GetHealth(ctx context.Context) (*sdkpb.HealthResponse, error) {
    return c.client.GetHealth(ctx, &sdkpb.HealthRequest{})
}

func (c *SDKClient) GetInfo(ctx context.Context) (*sdkpb.InfoResponse, error) {
    return c.client.GetInfo(ctx, &sdkpb.InfoRequest{})
}

func (c *SDKClient) GetConfig(ctx context.Context) (*sdkpb.ConfigResponse, error) {
    return c.client.GetConfig(ctx, &sdkpb.ConfigRequest{})
}

func (c *SDKClient) Close() error {
    return c.conn.Close()
}
```

### Language Support Matrix

| Feature | Go | Python | Java | Node.js | Rust | Others |
|---------|-----|--------|------|---------|------|--------|
| **Health/Version/Config** | ✅ v1.0 | 🔄 Planned | 🔄 Planned | ⚠️ Community | ⚠️ Community | ⚠️ Community |
| **Custom Health Checks** | ✅ v1.0 | 🔄 Planned | 🔄 Planned | ⚠️ Community | ⚠️ Community | ❌ |
| **Build Metadata Auto-collect** | ✅ v1.0 | ⚠️ Limited | ⚠️ Limited | ⚠️ Community | ⚠️ Community | ❌ |
| **eBPF Introspection (Tier 3)** | ✅ v2.0 | ❌ | ❌ | ❌ | ✅ v2.0 | ❌ |

**Legend:**
- ✅ Official support, full-featured
- 🔄 Planned (official)
- ⚠️ Community-maintained or partial support
- ❌ Not planned/not feasible

### SDK Distribution

**Go:**
```bash
go get github.com/coral-io/sdk-go@latest
```

**Python (Future):**
```bash
pip install coral-sdk
```

**Java (Future):**
```xml
<dependency>
  <groupId>io.coral</groupId>
  <artifactId>coral-sdk</artifactId>
  <version>1.0.0</version>
</dependency>
```

---

## Control Plane Features

The SDK enables full operations control when integrated with applications. These features transform Coral from an observability tool into a unified operations platform.

### Feature Flags

**Purpose**: Runtime feature toggling without redeployment

**Architecture**:

```
┌──────────────────────────────────────────────┐
│  CLI: coral flags enable new-checkout         │
└──────────┬───────────────────────────────────┘
           │
           ▼
┌──────────────────────────────────────────────┐
│  Colony: Feature Flag Store (DuckDB)         │
│  ┌────────────────────────────────────────┐  │
│  │ CREATE TABLE feature_flags (           │  │
│  │   flag_name TEXT PRIMARY KEY,          │  │
│  │   service_id TEXT,                     │  │
│  │   enabled BOOLEAN,                     │  │
│  │   rollout_percentage INTEGER,          │  │
│  │   targeting_rules JSONB,               │  │
│  │   updated_at TIMESTAMP                 │  │
│  │ );                                     │  │
│  └────────────────────────────────────────┘  │
└──────────┬───────────────────────────────────┘
           │ gRPC: UpdateFeatureFlag()
           ▼
┌──────────────────────────────────────────────┐
│  Agent: Flag Cache (in-memory)               │
│  - Receives flag updates from colony         │
│  - Pushes to SDK via gRPC                    │
└──────────┬───────────────────────────────────┘
           │ gRPC: UpdateFlag()
           ▼
┌──────────────────────────────────────────────┐
│  SDK: Feature Flag Client                    │
│  ┌────────────────────────────────────────┐  │
│  │  if coral.IsEnabled("new-checkout") {  │  │
│  │      useNewCheckout()                  │  │
│  │  }                                     │  │
│  └────────────────────────────────────────┘  │
│  - Local cache for fast checks (<1μs)        │
│  - Receives updates via gRPC stream          │
└──────────────────────────────────────────────┘
```

**Implementation**:

```protobuf
// proto/coral/control/v1/flags.proto
service FeatureFlagControl {
    // SDK → Colony: Register flags
    rpc RegisterFlags(RegisterFlagsRequest) returns (RegisterFlagsResponse);

    // SDK ← Colony: Receive flag updates (streaming)
    rpc WatchFlags(WatchFlagsRequest) returns (stream FlagUpdate);

    // SDK → Colony: Evaluate flag (with context)
    rpc EvaluateFlag(EvaluateFlagRequest) returns (EvaluateFlagResponse);
}

message RegisterFlagsRequest {
    string service_id = 1;
    repeated FlagDefinition flags = 2;
}

message FlagDefinition {
    string name = 1;
    bool default_enabled = 2;
    string description = 3;
    map<string, string> variants = 4;  // For A/B testing
}

message FlagUpdate {
    string flag_name = 1;
    bool enabled = 2;
    int32 rollout_percentage = 3;  // 0-100
    repeated TargetingRule targeting_rules = 4;
}

message TargetingRule {
    enum RuleType {
        USER_ID = 0;
        SEGMENT = 1;
        PERCENTAGE = 2;
        CUSTOM = 3;
    }
    RuleType type = 1;
    repeated string values = 2;  // e.g., ["user123", "user456"]
}

message EvaluateFlagRequest {
    string flag_name = 1;
    string user_id = 2;  // Optional
    map<string, string> context = 3;  // Custom attributes
}

message EvaluateFlagResponse {
    bool enabled = 1;
    string variant = 2;
    string reason = 3;  // "global", "targeted", "rollout-50%"
}
```

**SDK Go Implementation**:

```go
// pkg/sdk/flags.go
type FlagClient struct {
    cache     map[string]*FlagState
    mu        sync.RWMutex
    stream    FeatureFlagControl_WatchFlagsClient
    serviceID string
}

func (c *FlagClient) IsEnabled(flagName string) bool {
    c.mu.RLock()
    defer c.mu.RUnlock()

    if state, ok := c.cache[flagName]; ok {
        return state.Enabled && c.evaluateRollout(state)
    }
    return false  // Default: disabled
}

func (c *FlagClient) IsEnabledForUser(flagName, userID string) bool {
    c.mu.RLock()
    defer c.mu.RUnlock()

    state, ok := c.cache[flagName]
    if !ok {
        return false
    }

    // Check user targeting rules
    for _, rule := range state.TargetingRules {
        if rule.Type == USER_ID && contains(rule.Values, userID) {
            return true
        }
    }

    // Fallback to global state
    return state.Enabled && c.evaluateRollout(state)
}

func (c *FlagClient) watchUpdates(ctx context.Context) {
    for {
        update, err := c.stream.Recv()
        if err != nil {
            log.Printf("Flag stream error: %v", err)
            return
        }

        c.mu.Lock()
        c.cache[update.FlagName] = &FlagState{
            Enabled:        update.Enabled,
            Rollout:        update.RolloutPercentage,
            TargetingRules: update.TargetingRules,
        }
        c.mu.Unlock()
    }
}
```

**Colony Implementation**:

```go
// internal/colony/flags/store.go
func (s *FlagStore) UpdateFlag(ctx context.Context, req *UpdateFlagRequest) error {
    // Update in DuckDB
    _, err := s.db.ExecContext(ctx, `
        INSERT INTO feature_flags (flag_name, service_id, enabled, rollout_percentage, updated_at)
        VALUES (?, ?, ?, ?, NOW())
        ON CONFLICT (flag_name, service_id) DO UPDATE SET
            enabled = excluded.enabled,
            rollout_percentage = excluded.rollout_percentage,
            updated_at = excluded.updated_at
    `, req.FlagName, req.ServiceID, req.Enabled, req.RolloutPercentage)

    if err != nil {
        return err
    }

    // Broadcast to agents (who push to SDKs)
    s.broadcastFlagUpdate(req)
    return nil
}
```

**Performance Targets**:
- Flag evaluation: <1μs (local cache)
- Flag update propagation: <100ms (colony → agent → SDK)
- Memory overhead: <1MB for 1000 flags

---

### Traffic Inspection

**Purpose**: Sample and inspect live HTTP requests without SSH or log aggregation

**Architecture**:

```
┌──────────────────────────────────────────────┐
│  Application (with SDK)                      │
│  ┌────────────────────────────────────────┐  │
│  │  HTTP Handler                          │  │
│  │  ↓                                     │  │
│  │  coral.TrafficMiddleware()             │  │
│  │  ↓                                     │  │
│  │  • Sample 10% of requests              │  │
│  │  • Capture headers, body, timing       │  │
│  │  • Store in circular buffer (10MB)     │  │
│  └────────────────────────────────────────┘  │
└──────────┬───────────────────────────────────┘
           │ gRPC stream: Traffic samples
           ▼
┌──────────────────────────────────────────────┐
│  Agent: Traffic Buffer (50MB)                │
│  - Receives samples from SDK                 │
│  - Indexed by timestamp, path, status        │
│  - Auto-expire after 1 hour                  │
└──────────┬───────────────────────────────────┘
           │ On-demand query
           ▼
┌──────────────────────────────────────────────┐
│  Colony: Query Interface                     │
│  - CLI requests samples via colony           │
│  - Colony queries agents on-demand           │
│  - Filters applied at agent (efficient)      │
└──────────────────────────────────────────────┘
```

**Implementation**:

```protobuf
// proto/coral/control/v1/traffic.proto
service TrafficInspection {
    // SDK → Agent: Stream traffic samples
    rpc StreamSamples(stream TrafficSample) returns (StreamAck);

    // Colony → Agent: Query samples
    rpc QuerySamples(QuerySamplesRequest) returns (QuerySamplesResponse);

    // Colony → Agent: Configure sampling
    rpc ConfigureSampling(SamplingConfig) returns (SamplingResponse);
}

message TrafficSample {
    string request_id = 1;
    google.protobuf.Timestamp timestamp = 2;

    // Request
    string method = 3;
    string path = 4;
    map<string, string> request_headers = 5;
    bytes request_body = 6;

    // Response
    int32 status_code = 7;
    map<string, string> response_headers = 8;
    bytes response_body = 9;

    // Timing
    google.protobuf.Duration duration = 10;

    // Context
    string trace_id = 11;
    string user_id = 12;
    string session_id = 13;
}

message QuerySamplesRequest {
    // Filters
    repeated string paths = 1;        // Filter by path pattern
    repeated string methods = 2;      // GET, POST, etc.
    repeated int32 status_codes = 3;  // 200, 500, etc.
    google.protobuf.Timestamp start_time = 4;
    google.protobuf.Timestamp end_time = 5;

    // Pagination
    int32 limit = 6;
    string cursor = 7;
}
```

**SDK Go Implementation**:

```go
// pkg/sdk/traffic/middleware.go
func TrafficMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Sampling decision
        if !shouldSample() {
            next.ServeHTTP(w, r)
            return
        }

        // Capture request
        reqBody, _ := io.ReadAll(io.LimitReader(r.Body, maxBodySize))
        r.Body = io.NopCloser(bytes.NewBuffer(reqBody))

        // Wrap response writer
        rw := &responseRecorder{ResponseWriter: w, statusCode: 200}

        start := time.Now()
        next.ServeHTTP(rw, r)
        duration := time.Since(start)

        // Send sample to agent (async, non-blocking)
        go func() {
            sample := &TrafficSample{
                RequestID:       generateID(),
                Timestamp:       timestamppb.Now(),
                Method:          r.Method,
                Path:            r.URL.Path,
                RequestHeaders:  headersToMap(r.Header),
                RequestBody:     reqBody,
                StatusCode:      int32(rw.statusCode),
                ResponseHeaders: headersToMap(rw.Header()),
                ResponseBody:    rw.body.Bytes(),
                Duration:        durationpb.New(duration),
                TraceID:         getTraceID(r),
                UserID:          getUserID(r),
            }

            trafficClient.StreamSample(sample)
        }()
    })
}

func shouldSample() bool {
    return rand.Float64() < config.SampleRate
}
```

**Agent Storage** (DuckDB):

```sql
CREATE TABLE traffic_samples (
    request_id TEXT PRIMARY KEY,
    timestamp TIMESTAMP,
    method TEXT,
    path TEXT,
    status_code INTEGER,
    duration_ms INTEGER,
    request_size INTEGER,
    response_size INTEGER,
    trace_id TEXT,
    user_id TEXT,
    -- Compressed blob for full request/response
    sample_data BLOB
);

CREATE INDEX idx_traffic_timestamp ON traffic_samples(timestamp);
CREATE INDEX idx_traffic_path ON traffic_samples(path);
CREATE INDEX idx_traffic_status ON traffic_samples(status_code);

-- Auto-cleanup old samples (6 hours retention)
DELETE FROM traffic_samples WHERE timestamp < NOW() - INTERVAL '6 hours';
```

**Performance Targets**:
- Sampling overhead: <1% latency impact at 10% sample rate
- Sample capture: <500μs (async, non-blocking)
- Memory: 50MB circular buffer per agent
- Query latency: <100ms for filtered queries

---

### Remote Profiling

**Purpose**: Trigger profilers remotely without SSH access

**Architecture**:

```
┌──────────────────────────────────────────────┐
│  CLI: coral profile start api --type cpu     │
└──────────┬───────────────────────────────────┘
           │
           ▼
┌──────────────────────────────────────────────┐
│  Colony: Profile Request Router              │
└──────────┬───────────────────────────────────┘
           │ gRPC: StartProfile()
           ▼
┌──────────────────────────────────────────────┐
│  Agent: Profile Coordinator                  │
└──────────┬───────────────────────────────────┘
           │ gRPC: StartProfile()
           ▼
┌──────────────────────────────────────────────┐
│  SDK: Profiler                               │
│  ┌────────────────────────────────────────┐  │
│  │  pprof.StartCPUProfile(w)              │  │
│  │  time.Sleep(duration)                  │  │
│  │  pprof.StopCPUProfile()                │  │
│  └────────────────────────────────────────┘  │
│  - Stores profile in /tmp                    │
│  - Returns via gRPC stream                   │
└──────────────────────────────────────────────┘
```

**Implementation**:

```protobuf
// proto/coral/control/v1/profiling.proto
service Profiling {
    // Colony → Agent → SDK: Start profiling
    rpc StartProfile(ProfileRequest) returns (ProfileResponse);

    // Colony → Agent → SDK: Stop profiling
    rpc StopProfile(StopProfileRequest) returns (StopProfileResponse);

    // Colony → Agent: Download profile data
    rpc GetProfile(GetProfileRequest) returns (stream ProfileData);

    // Agent → Colony: List available profile types
    rpc ListProfileTypes(ListProfileTypesRequest) returns (ListProfileTypesResponse);
}

message ProfileRequest {
    enum ProfileType {
        CPU = 0;
        HEAP = 1;
        GOROUTINE = 2;
        MUTEX = 3;
        BLOCK = 4;
        THREADCREATE = 5;
        ALLOCS = 6;
    }

    string service_id = 1;
    ProfileType type = 2;
    google.protobuf.Duration duration = 3;  // For CPU profiling
}

message ProfileResponse {
    string profile_id = 1;
    ProfileRequest.ProfileType type = 2;
    google.protobuf.Timestamp started_at = 3;
    google.protobuf.Duration expected_duration = 4;
}

message ProfileData {
    string profile_id = 1;
    bytes chunk = 2;  // Streamed in chunks
    int64 total_size = 3;
}
```

**SDK Go Implementation**:

```go
// pkg/sdk/profiling/profiler.go
type Profiler struct {
    mu              sync.Mutex
    activeProfiles  map[string]*activeProfile
    profileDir      string
}

func (p *Profiler) StartProfile(ctx context.Context, req *ProfileRequest) (*ProfileResponse, error) {
    p.mu.Lock()
    defer p.mu.Unlock()

    profileID := generateID()
    profilePath := filepath.Join(p.profileDir, profileID+".pprof")

    profile := &activeProfile{
        ID:        profileID,
        Type:      req.Type,
        StartedAt: time.Now(),
        Path:      profilePath,
    }

    switch req.Type {
    case ProfileType_CPU:
        f, err := os.Create(profilePath)
        if err != nil {
            return nil, err
        }

        if err := pprof.StartCPUProfile(f); err != nil {
            return nil, err
        }

        profile.File = f

        // Auto-stop after duration
        go func() {
            time.Sleep(req.Duration.AsDuration())
            p.StopProfile(ctx, &StopProfileRequest{ProfileID: profileID})
        }()

    case ProfileType_HEAP:
        // Heap profile is instant
        f, err := os.Create(profilePath)
        if err != nil {
            return nil, err
        }
        defer f.Close()

        runtime.GC() // Get up-to-date stats
        if err := pprof.WriteHeapProfile(f); err != nil {
            return nil, err
        }

    case ProfileType_GOROUTINE:
        f, err := os.Create(profilePath)
        if err != nil {
            return nil, err
        }
        defer f.Close()

        profile := pprof.Lookup("goroutine")
        if err := profile.WriteTo(f, 0); err != nil {
            return nil, err
        }
    }

    p.activeProfiles[profileID] = profile

    return &ProfileResponse{
        ProfileID:        profileID,
        Type:             req.Type,
        StartedAt:        timestamppb.New(profile.StartedAt),
        ExpectedDuration: req.Duration,
    }, nil
}

func (p *Profiler) StopProfile(ctx context.Context, req *StopProfileRequest) error {
    p.mu.Lock()
    defer p.mu.Unlock()

    profile, ok := p.activeProfiles[req.ProfileID]
    if !ok {
        return ErrProfileNotFound
    }

    if profile.Type == ProfileType_CPU {
        pprof.StopCPUProfile()
        if profile.File != nil {
            profile.File.Close()
        }
    }

    delete(p.activeProfiles, req.ProfileID)
    return nil
}
```

**Performance Targets**:
- Profile trigger latency: <1s
- CPU profile overhead: <5% during profiling
- Heap profile: instant (GC pause)
- Profile size: 1-50MB depending on type

---

### Deployment Rollbacks

**Purpose**: Coordinate rollbacks through Coral CLI with AI insights

**Architecture**:

```
┌──────────────────────────────────────────────┐
│  CLI: coral rollback api                     │
│       or                                     │
│       coral ask → AI recommends → approve    │
└──────────┬───────────────────────────────────┘
           │
           ▼
┌──────────────────────────────────────────────┐
│  Colony: Rollback Coordinator                │
│  - Determines target version (previous)      │
│  - Validates rollback safety                 │
│  - Coordinates with agent                    │
└──────────┬───────────────────────────────────┘
           │ gRPC: ExecuteRollback()
           ▼
┌──────────────────────────────────────────────┐
│  Agent: Rollback Executor                    │
│  - Calls SDK rollback handler                │
│  - Or orchestrates kubectl/systemctl         │
│  - Monitors health during rollback           │
└──────────┬───────────────────────────────────┘
           │ gRPC: OnRollback()
           ▼
┌──────────────────────────────────────────────┐
│  SDK: Rollback Handler (app-defined)         │
│  coral.OnRollback(func(target) {             │
│      deployVersion(target.Version)           │
│  })                                          │
└──────────────────────────────────────────────┘
```

**Implementation**:

```protobuf
// proto/coral/control/v1/rollback.proto
service RollbackControl {
    // Colony → Agent → SDK: Execute rollback
    rpc ExecuteRollback(RollbackRequest) returns (RollbackResponse);

    // Colony → Agent: Get rollback status
    rpc GetRollbackStatus(GetRollbackStatusRequest) returns (RollbackStatus);

    // Agent → Colony: Stream rollback progress
    rpc StreamRollbackProgress(StreamRollbackProgressRequest) returns (stream RollbackProgress);

    // Agent → Colony: List available rollback targets
    rpc ListRollbackTargets(ListRollbackTargetsRequest) returns (ListRollbackTargetsResponse);
}

message RollbackRequest {
    string service_id = 1;
    string target_version = 2;  // Empty = previous version
    bool dry_run = 3;
    google.protobuf.Duration timeout = 4;
    RollbackStrategy strategy = 5;
}

enum RollbackStrategy {
    AUTO = 0;           // SDK handler or kubectl (auto-detect)
    KUBERNETES = 1;     // kubectl rollout undo
    SYSTEMD = 2;        // systemctl restart with old version
    SDK_HANDLER = 3;    // Use app-defined SDK handler
}

message RollbackResponse {
    string rollback_id = 1;
    bool success = 2;
    string message = 3;
    RollbackStatus status = 4;
}

message RollbackStatus {
    enum State {
        PENDING = 0;
        IN_PROGRESS = 1;
        COMPLETED = 2;
        FAILED = 3;
        ROLLED_BACK = 4;  // Rollback itself failed, rolled back
    }

    string rollback_id = 1;
    State state = 2;
    string current_version = 3;
    string target_version = 4;
    google.protobuf.Timestamp started_at = 5;
    google.protobuf.Duration elapsed = 6;
    repeated RollbackStep steps = 7;
}

message RollbackStep {
    string description = 1;
    bool completed = 2;
    string error = 3;
}

message RollbackProgress {
    string rollback_id = 1;
    string message = 2;
    int32 progress_percent = 3;  // 0-100
}
```

**SDK Go Implementation**:

```go
// pkg/sdk/rollback/handler.go
type RollbackHandler func(ctx context.Context, target RollbackTarget) error

type RollbackTarget struct {
    Version string
    GitCommit string
    PreviousVersion string
}

var registeredHandler RollbackHandler

func OnRollback(handler RollbackHandler) {
    registeredHandler = handler
}

// Called by agent via gRPC
func (s *SDKServer) ExecuteRollback(ctx context.Context, req *RollbackRequest) (*RollbackResponse, error) {
    if registeredHandler == nil {
        return nil, errors.New("no rollback handler registered")
    }

    target := RollbackTarget{
        Version:         req.TargetVersion,
        PreviousVersion: s.getCurrentVersion(),
    }

    if err := registeredHandler(ctx, target); err != nil {
        return &RollbackResponse{
            Success: false,
            Message: fmt.Sprintf("Rollback failed: %v", err),
        }, nil
    }

    return &RollbackResponse{
        Success: true,
        Message: fmt.Sprintf("Rolled back to %s", target.Version),
    }, nil
}
```

**Agent Implementation** (Kubernetes mode):

```go
// internal/agent/rollback/kubernetes.go
func (r *KubernetesRollback) Execute(ctx context.Context, req *RollbackRequest) error {
    // Get deployment
    deployment, err := r.k8sClient.AppsV1().Deployments(r.namespace).Get(ctx, r.deploymentName, metav1.GetOptions{})
    if err != nil {
        return err
    }

    // Get rollback target
    var targetRevision int64
    if req.TargetVersion != "" {
        // Find revision by version label
        targetRevision, err = r.findRevisionByVersion(ctx, req.TargetVersion)
    } else {
        // Use previous revision
        currentRevision := deployment.Status.Revision
        targetRevision = currentRevision - 1
    }

    // Execute rollback
    cmd := exec.CommandContext(ctx, "kubectl", "rollout", "undo",
        fmt.Sprintf("deployment/%s", r.deploymentName),
        fmt.Sprintf("--to-revision=%d", targetRevision),
        fmt.Sprintf("--namespace=%s", r.namespace))

    if output, err := cmd.CombinedOutput(); err != nil {
        return fmt.Errorf("kubectl rollout failed: %w, output: %s", err, output)
    }

    // Wait for rollout to complete
    return r.waitForRollout(ctx, r.deploymentName, 5*time.Minute)
}
```

**Colony DuckDB Schema**:

```sql
CREATE TABLE rollback_history (
    rollback_id TEXT PRIMARY KEY,
    service_id TEXT,
    from_version TEXT,
    to_version TEXT,
    strategy TEXT,
    initiated_by TEXT,  -- 'user', 'ai'
    success BOOLEAN,
    started_at TIMESTAMP,
    completed_at TIMESTAMP,
    duration_seconds INTEGER,
    error_message TEXT
);

CREATE INDEX idx_rollback_service ON rollback_history(service_id, started_at);
```

**Performance Targets**:
- Rollback decision latency: <1s (version lookup)
- Kubernetes rollback: 30-120s (depends on app)
- SDK handler rollback: Varies by implementation
- Health check confirmation: <30s after rollback

---

## Wire Protocol

### Agent ↔ Colony (Buf Connect over Wireguard)

**Using Buf Connect**: We use [Buf Connect](https://connectrpc.com/) instead of pure gRPC for:
- Type-safe code generation
- Better browser support (HTTP/1.1 + HTTP/2)
- Simpler tooling (Buf CLI)
- Compatible with gRPC clients if needed

```protobuf
syntax = "proto3";
package coral.mesh.v1;

// Mesh coordination service
service CoralMesh {
  // Agent registers with colony
  rpc Register(RegisterRequest) returns (RegisterResponse);

  // Periodic heartbeat (every 30s)
  rpc Heartbeat(HeartbeatRequest) returns (HeartbeatResponse);

  // Agent reports event
  rpc ReportEvent(Event) returns (EventAck);

  // Query mesh state
  rpc Query(QueryRequest) returns (QueryResponse);
}

message RegisterRequest {
  string agent_id = 1;
  string app_name = 2;
  string version = 3;
  map<string, string> tags = 4;
  string wireguard_pubkey = 5;
}

message RegisterResponse {
  string mesh_id = 1;
  string assigned_ip = 2;  // Agent's IP in the mesh
  repeated PeerInfo peers = 3;  // Other agents in mesh
}

message HeartbeatRequest {
  string agent_id = 1;
  HealthStatus health = 2;
  ResourceUsage resources = 3;
}

message HeartbeatResponse {
  bool ok = 1;
  repeated Command commands = 2;  // Future: colony → agent commands
}

message Event {
  string agent_id = 1;
  string type = 2;  // deploy, restart, crash, connection, metric_spike
  google.protobuf.Timestamp timestamp = 3;
  map<string, string> tags = 4;
  bytes payload = 5;  // JSON-encoded event details
}

message EventAck {
  string event_id = 1;
  bool stored = 2;
}

enum HealthStatus {
  UNKNOWN = 0;
  HEALTHY = 1;
  DEGRADED = 2;
  UNHEALTHY = 3;
}

message ResourceUsage {
  float cpu_percent = 1;
  uint64 memory_bytes = 2;
  uint64 network_rx_bytes = 3;
  uint64 network_tx_bytes = 4;
}
```

### Discovery Service (HTTP REST)

```
POST /v1/mesh/:mesh_id/register
Request:
{
  "type": "colony",
  "pubkey": "...",
  "endpoints": ["203.0.113.42:41820"]
}
Response:
{
  "ok": true,
  "ttl": 300
}

GET /v1/mesh/:mesh_id/colony
Response:
{
  "pubkey": "...",
  "endpoints": ["203.0.113.42:41820", "198.51.100.5:41820"],
  "last_seen": "2025-10-27T15:30:00Z"
}
```

---

## Deployment Patterns

### Pattern 1: Local Development (Recommended)

```bash
# In your project directory
cd ~/projects/my-shop

# Start colony
coral colony start

# In separate terminals, connect your app components
coral connect frontend --port 3000
coral connect api --port 8080
coral connect database --port 5432

# Colony and agents all run locally
# Dashboard: http://localhost:3000
```

### Pattern 2: Docker Compose (Application Stack)

```yaml
version: '3'
services:
  # Your application colony
  coral-colony:
    image: coral/colony
    volumes:
      - .coral:/data
    environment:
      - ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}
      - CORAL_APP_NAME=my-shop
      - CORAL_ENVIRONMENT=prod
    ports:
      - "41820:41820/udp"  # Wireguard (non-standard port)
      - "3000:3000"        # Dashboard

  # Your frontend
  frontend:
    image: myapp/frontend:latest
    ports: ["80:3000"]

  frontend-agent:
    image: coral/agent
    network_mode: "service:frontend"
    environment:
      - CORAL_COLONY_ID=my-shop-prod
      - CORAL_COMPONENT=frontend

  # Your API
  api:
    image: myapp/api:latest
    ports: ["8080:8080"]

  api-agent:
    image: coral/agent
    network_mode: "service:api"
    environment:
      - CORAL_COLONY_ID=my-shop-prod
      - CORAL_COMPONENT=api

  # Your database
  database:
    image: postgres:14
    ports: ["5432:5432"]

  db-agent:
    image: coral/agent
    network_mode: "service:database"
    environment:
      - CORAL_COLONY_ID=my-shop-prod
      - CORAL_COMPONENT=database
```

### Pattern 3: Kubernetes (Application Namespace)

```yaml
# Deploy colony for your application
apiVersion: apps/v1
kind: Deployment
metadata:
  name: coral-colony
  namespace: my-shop-prod
spec:
  replicas: 1
  template:
    spec:
      containers:
      - name: colony
        image: coral/colony
        env:
        - name: ANTHROPIC_API_KEY
          valueFrom:
            secretKeyRef:
              name: coral-secrets
              key: anthropic-api-key
        - name: CORAL_APP_NAME
          value: my-shop
        - name: CORAL_ENVIRONMENT
          value: prod
        ports:
        - containerPort: 41820  # Wireguard (non-standard port)
          protocol: UDP
        - containerPort: 3000

---
# Your app pods with Coral agents
apiVersion: v1
kind: Pod
metadata:
  name: api
  namespace: my-shop-prod
spec:
  containers:
  - name: api
    image: myapp/api:latest
    ports:
    - containerPort: 8080

  - name: coral-agent
    image: coral/agent
    env:
    - name: CORAL_COLONY_ID
      value: my-shop-prod
    - name: CORAL_COMPONENT
      value: api
```

---

## Open Technical Questions

### 1. Wireguard vs alternatives for control plane?
- **Wireguard**: Secure, lightweight, proven
- **Plain TLS**: Simpler, but need custom NAT traversal
- **Tailscale library**: Easy, but external dependency
- **Note**: Only for control plane (agent ↔ colony), not app traffic

### 2. Local AI models vs API-only?
- **Local**: Faster, cheaper, but less capable
- **API**: Better quality, but costs add up
- **Hybrid approach makes sense?**

### 3. How to handle agent authentication?
- **Colony password**: Simple, good for single-app deployments
- **Per-agent tokens**: More complex, better for production
- **Certificate-based**: Most secure, overkill for typical apps

### 4. Should agents talk to colony directly?
- **Currently**: All agents → colony (star topology)
- **Works well for**: Typical applications (3-50 components)
- **Future (Reef)**: For multi-colony setups, might need gateway pattern
- **Note**: Agents never need to talk to each other (control plane only)

### 5. Go vs Rust for implementation?
- **Go**: Faster development, easier deployment, great networking libraries
- **Rust**: Better performance, memory safety, steeper learning curve

### 6. DuckDB for layered storage - decided!
- **Decision**: Use DuckDB for both colony and agent storage
- **Why**: Columnar storage optimized for analytical queries, perfect for time-series data
- **Layered approach**: Agents store recent raw data (~6 hours), colony stores summaries + historical data
- **Scalability**: Horizontal scaling through distributed agent storage, colony queries agents on-demand
- **Performance**: <100ms queries on colony summaries, <500ms for agent detail queries
- **Migration path**: Can add multi-colony federation in Phase 3 if needed

### 7. MCP client library vs custom implementation?
- **Use mcp-go library**: Faster implementation, standard compliance
- **Custom implementation**: More control, lighter weight
- **Decision**: Start with mcp-go, optimize later if needed

### 8. How to handle MCP server failures?
- **Fail gracefully**: Continue without that data source
- **Retry logic**: Exponential backoff with max attempts
- **Fallback**: Cache previous results, warn user about stale data
- **Health checks**: Monitor MCP servers and report status

### 9. Should Coral cache MCP results?
- **Pro**: Faster responses, reduced external API calls, cost savings
- **Con**: Stale data risk, memory overhead
- **Approach**: Short TTL cache (30-60s) with invalidation on events

---

## Performance Targets

### Agent Resource Usage (with local DuckDB storage)
- **Memory**: <50MB (including local DuckDB with 1 hour data)
- **CPU**: <0.1% average
- **Disk**: 10-50MB (1 hour raw metrics, auto-cleanup)
- **Network**: <1KB/s (heartbeats + summaries)
- **Storage I/O**: Minimal (buffered writes, periodic cleanup)

### Colony Performance (with layered storage)
**Target**: Typical application (3-20 components)
- **Agent connections**: Support 50+ concurrent agents (enough for complex apps)
- **Summary ingestion**: >1,000 summaries/second (far exceeds typical app needs)
- **Query latency**:
  - Summary queries: <100ms (from colony DuckDB)
  - Detail queries: <500ms (federated from agent DuckDB)
  - Cross-agent correlation: <1s
- **Dashboard load time**: <1s
- **Storage**: ~100-500MB for typical app with full retention (see DESIGN.md)
- **Resource usage (colony)**:
  - Memory: <500MB
  - CPU: <5% on modern laptop
  - Disk: Auto-managed, grows with history

### Network Requirements
**Primary**: Local development (localhost)
- **Latency**: Sub-millisecond (same machine)
- **Bandwidth**: Minimal (<1KB/s per agent)
- **Reliability**: Excellent (local loopback)

**Production**: Deployed alongside app
- **Latency tolerance**: Works well up to 200ms RTT
- **Bandwidth**: Still minimal (<1KB/s per agent)
- **Packet loss**: Tolerant up to 5% loss

---

## Security Implementation

### Authentication Flow

```
1. User starts colony
   → Generates Wireguard keypair
   → Registers pubkey with discovery service

2. User starts agent with mesh ID
   → Agent contacts discovery: "Where is mesh-abc?"
   → Discovery returns: colony endpoints + pubkey

3. Agent verifies pubkey
   → Compares with expected pubkey (from initial setup)
   → If mismatch: refuses to connect

4. Agent establishes Wireguard tunnel
   → Encrypted connection to colony

5. Colony challenges agent (optional)
   → Mesh password authentication
   → Agent responds with password

6. Connected!
   → Agent can send events
   → Colony can query agent
```

### Encryption

- **Control plane**: Wireguard (ChaCha20-Poly1305)
- **Storage at rest**: Optional SQLCipher
- **Dashboard**: TLS 1.3
- **API keys**: Stored encrypted with user-provided key

### Trust Boundaries

**Discovery Service** (Untrusted):
- Can see: mesh IDs, IP addresses, public keys
- Cannot see: application data, encrypted traffic
- Cannot: impersonate colony (no private keys)

**Colony** (Trusted - User Controls):
- Has: all application data, AI API keys, private keys
- User's responsibility to secure

**Agents** (Trusted - User Controls):
- Connect only to verified colony (pubkey pinning)
- Encrypted tunnel (Wireguard)
- Minimal privileges (observe only, don't control)

---

## Development Setup

### Prerequisites

```bash
# Install Go 1.25+
brew install go

# Install Buf CLI for protobuf management
brew install bufbuild/buf/buf

# Install Connect code generator
go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest
```

### Build from Source

```bash
# Clone repository
git clone https://github.com/coral-io/coral.git
cd coral

# Build colony
go build -o bin/coral-colony ./cmd/colony

# Build agent
go build -o bin/coral-agent ./cmd/agent

# Build CLI
go build -o bin/coral ./cmd/cli

# Run tests
go test ./...
```

### Development Workflow

```bash
# Run colony locally
./bin/coral-colony start --dev

# Run agent in dev mode (connects to localhost)
./bin/coral-agent connect --mesh dev --name test-app

# Generate protobuf code
make proto

# Run linter
make lint

# Run integration tests
make test-integration
```

---

## References

### Documentation
- [Wireguard Protocol](https://www.wireguard.com/protocol/)
- [Buf Connect](https://connectrpc.com/) - RPC framework
- [Buf Documentation](https://buf.build/docs) - Protobuf tooling
- [Anthropic API](https://docs.anthropic.com/)

### Similar Projects (for inspiration)
- [Tailscale](https://tailscale.com/) - Mesh networking
- [Dapr](https://dapr.io/) - Distributed app building blocks
- [Netdata](https://www.netdata.cloud/) - Real-time monitoring

---

## Related Documents

- **[CONCEPT.md](./CONCEPT.md)** - High-level concept and key ideas
- **[DESIGN.md](./DESIGN.md)** - Design philosophy and feature overview
- **[ROADMAP.md](./ROADMAP.md)** - Development roadmap and milestones
- **[EXAMPLES.md](./EXAMPLES.md)** - Concrete use cases with MCP orchestration
