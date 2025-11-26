## Storage Architecture

Coral uses a **layered storage architecture** to achieve horizontal scalability while maintaining fast query performance for AI analysis.

### Architecture Overview

```
┌────────────────────────────────────────────────────────────┐
│                   COLONY LAYER                              │
│                   (Summaries + History)                     │
│                                                             │
│  DuckDB Database:                                           │
│  ├─ Aggregated metrics (compressed summaries)              │
│  ├─ Historical trends and patterns                         │
│  ├─ Cross-agent correlations                               │
│  ├─ Topology graph (service dependencies)                  │
│  ├─ Event history (deploys, crashes, anomalies)            │
│  └─ Learned baselines and patterns                         │
│                                                             │
│  Query federation:                                          │
│  └─ Can query agent layer on-demand for recent details     │
└─────────────────┬───────────────────────────────────────────┘
                  │
            gRPC SQL Proxy
                  │
    ┌─────────────┼─────────────┬─────────────┐
    │             │             │             │
┌───▼─────┐  ┌───▼─────┐  ┌───▼─────┐  ┌───▼─────┐
│ AGENT 1 │  │ AGENT 2 │  │ AGENT 3 │  │ AGENT N │
│         │  │         │  │         │  │         │
│ DuckDB: │  │ DuckDB: │  │ DuckDB: │  │ DuckDB: │
│ • Raw   │  │ • Raw   │  │ • Raw   │  │ • Raw   │
│   metrics│  │   metrics│  │   metrics│  │   metrics│
│   (~1hr)│  │   (~1hr)│  │   (~1hr)│  │   (~1hr)│
│ • Events│  │ • Events│  │ • Events│  │ • Events│
│ • Proc  │  │ • Proc  │  │ • Proc  │  │ • Proc  │
│   stats │  │   stats │  │   stats │  │   stats │
└─────────┘  └─────────┘  └─────────┘  └─────────┘
     │            │            │            │
    Observes    Observes    Observes    Observes
     │            │            │            │
  Service(s)  Service(s)  Service(s)  Service(s)
```

### Storage Layers

#### 1. Agent Layer (Local DuckDB)

**Purpose**: Store recent high-resolution data locally on each agent

**What agents store**:
- **Raw metrics** (~1 hour retention)
    - Process metrics: CPU, memory, file descriptors, threads
    - Network metrics: Connection counts, bandwidth, errors
    - Custom metrics: Scraped from Prometheus endpoints
- **Event log** (last 1 hour)
    - Process lifecycle events (start, stop, crash)
    - Network connection events (new connections, failures)
    - Health check results
- **Process observations**
    - Active connections (from netstat/ss)
    - Resource usage snapshots
    - Version information

**DuckDB Schema (Agent)**:
```sql
-- Time-series metrics (high-resolution)
CREATE TABLE metrics (
  timestamp TIMESTAMP,
  metric_name TEXT,
  value DOUBLE,
  labels TEXT,  -- JSON string: service, host, etc.
  INDEX idx_timestamp (timestamp)
);

-- Process events
CREATE TABLE events (
  id INTEGER PRIMARY KEY,
  timestamp TIMESTAMP,
  event_type TEXT,  -- start, stop, crash, deploy
  process_name TEXT,
  details TEXT  -- JSON string
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

**Retention Policy**:
- Keep raw data for **~1 hour** (configurable)
- After 1 hour, data is either:
    - Archived to colony (for important events)
    - Discarded (for routine metrics already summarized)
- Automatic cleanup via DuckDB TTL or scheduled deletion

**Data Flow**:
```
Agent observes → Store in local DuckDB → Push summaries to colony
                       ↓
              Colony can query back for details
```

#### 2. Colony Layer (Central DuckDB)

**Purpose**: Store summaries, aggregations, and cross-agent correlations

**What colony stores**:
- **Aggregated metrics** (long-term)
    - Per-service summaries (p50, p95, p99, mean, max)
    - Downsampled time-series (5min, 15min, 1hr intervals)
    - Historical trends (days to weeks)
- **Event correlations**
    - Cross-service events (deploy A → crash B pattern)
    - Anomaly detection results
    - AI-generated insights
- **Topology graph**
    - Service dependency map (auto-discovered)
    - Connection metadata (protocols, frequencies)
    - Version tracking per service
- **Learned baselines**
    - Normal behavior patterns per service
    - Statistical models (mean, stddev, percentiles)
    - Anomaly thresholds

**DuckDB Schema (Colony)**:
```sql
-- Services in the mesh
CREATE TABLE IF NOT EXISTS services (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  app_id TEXT NOT NULL,
  version TEXT,
  agent_id TEXT NOT NULL,
  labels TEXT,  -- JSON string
  last_seen TIMESTAMP NOT NULL,
  status TEXT NOT NULL  -- running, stopped, error
);
CREATE INDEX IF NOT EXISTS idx_services_agent_id ON services(agent_id);
CREATE INDEX IF NOT EXISTS idx_services_status ON services(status);
CREATE INDEX IF NOT EXISTS idx_services_last_seen ON services(last_seen);

-- Aggregated metrics (downsampled)
CREATE TABLE IF NOT EXISTS metric_summaries (
  timestamp TIMESTAMP NOT NULL,
  service_id TEXT NOT NULL,
  metric_name TEXT NOT NULL,
  interval TEXT NOT NULL,  -- '5m', '15m', '1h', '1d'
  p50 DOUBLE,
  p95 DOUBLE,
  p99 DOUBLE,
  mean DOUBLE,
  max DOUBLE,
  count INTEGER,
  PRIMARY KEY (timestamp, service_id, metric_name, interval)
);
CREATE INDEX IF NOT EXISTS idx_metric_summaries_service_id ON metric_summaries(service_id);
CREATE INDEX IF NOT EXISTS idx_metric_summaries_metric_name ON metric_summaries(metric_name);

-- Event log (important events only)
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY,
  timestamp TIMESTAMP NOT NULL,
  service_id TEXT NOT NULL,
  event_type TEXT NOT NULL,  -- deploy, crash, restart, alert, connection
  details TEXT,  -- JSON string
  correlation_group TEXT
);
CREATE INDEX IF NOT EXISTS idx_events_service_id ON events(service_id);
CREATE INDEX IF NOT EXISTS idx_events_type ON events(event_type);
CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp);
CREATE INDEX IF NOT EXISTS idx_events_correlation ON events(correlation_group);

-- AI-generated insights
CREATE TABLE IF NOT EXISTS insights (
  id INTEGER PRIMARY KEY,
  created_at TIMESTAMP NOT NULL,
  insight_type TEXT NOT NULL,  -- anomaly, pattern, recommendation
  priority TEXT NOT NULL,  -- high, medium, low
  title TEXT NOT NULL,
  summary TEXT NOT NULL,
  details TEXT,  -- JSON string
  affected_services TEXT,  -- JSON array
  status TEXT NOT NULL,  -- active, dismissed, resolved
  confidence DOUBLE,
  expires_at TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_insights_status ON insights(status);
CREATE INDEX IF NOT EXISTS idx_insights_priority ON insights(priority);
CREATE INDEX IF NOT EXISTS idx_insights_created_at ON insights(created_at);

-- Service topology (auto-discovered)
CREATE TABLE IF NOT EXISTS service_connections (
  from_service TEXT NOT NULL,
  to_service TEXT NOT NULL,
  protocol TEXT NOT NULL,
  first_observed TIMESTAMP NOT NULL,
  last_observed TIMESTAMP NOT NULL,
  connection_count INTEGER NOT NULL,
  PRIMARY KEY (from_service, to_service, protocol)
);
CREATE INDEX IF NOT EXISTS idx_service_connections_from ON service_connections(from_service);
CREATE INDEX IF NOT EXISTS idx_service_connections_to ON service_connections(to_service);

-- Learned baselines
CREATE TABLE IF NOT EXISTS baselines (
  service_id TEXT NOT NULL,
  metric_name TEXT NOT NULL,
  time_window TEXT NOT NULL,  -- '1h', '1d', '7d'
  mean DOUBLE,
  stddev DOUBLE,
  p50 DOUBLE,
  p95 DOUBLE,
  p99 DOUBLE,
  sample_count INTEGER,
  last_updated TIMESTAMP NOT NULL,
  PRIMARY KEY (service_id, metric_name, time_window)
);
CREATE INDEX IF NOT EXISTS idx_baselines_service_id ON baselines(service_id);
CREATE INDEX IF NOT EXISTS idx_baselines_metric_name ON baselines(metric_name);
```

**Data Flow**:
```
Agents push summaries → Colony stores aggregations
                              ↓
                    Colony queries agents for details
                              ↓
                    AI analysis on colony data
```


### Data Flows

#### 1. Regular Data Collection (Push Model)

```
┌─────────┐
│  Agent  │
└────┬────┘
     │ Every 10-60s (configurable)
     │
     ├─► Collect raw metrics → Store in local DuckDB
     │
     ├─► Compute local summaries:
     │   • p50, p95, p99 per metric
     │   • Event counts
     │   • Connection deltas (new/closed)
     │
     └─► Push compressed summary to colony via gRPC
              ↓
         ┌────────────┐
         │ Colony│
         └────────────┘
         Stores summary in DuckDB
         Updates aggregations
         Triggers AI analysis if anomaly detected
```

**Network Efficiency**:
- Agents send **compressed summaries**, not raw data
- Target: <1KB per agent per minute
- Example summary payload:
```json
{
  "agent_id": "agent-us-east-001",
  "timestamp": "2025-10-28T14:30:00Z",
  "metrics": {
    "cpu_percent": {"p50": 12.5, "p95": 45.2, "max": 78.1},
    "memory_mb": {"p50": 256, "p95": 312, "max": 389},
    "connections": {"count": 42, "delta": +3}
  },
  "events": [
    {"type": "connection_new", "remote": "10.100.0.5:8080"}
  ]
}
```

#### 2. On-Demand Detail Retrieval (Pull Model)

When colony needs high-resolution data (e.g., during investigation):

```
┌────────────┐
│ Colony│  User asks: "Show me exact memory pattern
└─────┬──────┘              before the crash"
      │
      │ Determines relevant agent
      │
      ├─► gRPC query to agent:
      │   CoralAgent.QueryMetrics({
      │     metric: "memory_mb",
      │     start: "2025-10-28T14:00:00Z",
      │     end: "2025-10-28T14:10:00Z",
      │     resolution: "10s"
      │   })
      │
   ┌──▼──────┐
   │  Agent  │
   └──┬──────┘
      │ Query local DuckDB for raw data
      │
      └─► Return detailed results
              ↓
         Colony uses for AI analysis
```

**SQL Proxy Implementation**:
Agents expose a gRPC endpoint that accepts SQL-like queries:

```protobuf
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
  map<string, string> filters = 5;  // Additional filters
}

message MetricsQueryResponse {
  repeated MetricPoint points = 1;
}

message MetricPoint {
  google.protobuf.Timestamp timestamp = 1;
  double value = 2;
  map<string, string> labels = 3;
}
```

#### 3. Offline Agent Handling

When an agent goes offline:

```
┌────────────┐
│ Colony│
└─────┬──────┘
      │ Agent last seen: 5 minutes ago
      │
      ├─► Mark agent status as "stale"
      │   Continue operating on last received summaries
      │
      ├─► Show data with staleness indicator:
      │   "⚠️ Data is 5 minutes stale"
      │
      └─► When agent reconnects:
          • Agent pushes catch-up summaries
          • Colony updates to current state
          • Historical raw data on agent is lost (outside retention window)
```

**Graceful Degradation**:
- Colony continues showing trends from summaries
- UI indicates staleness
- AI analysis uses "last known good" data
- No data loss for already-summarized metrics
- Detailed investigation limited to online agents

### Scalability Benefits

**Horizontal Scaling**:
- Each agent stores only its local data (~1 hour)
- Storage grows linearly with fleet size, not centrally
- Agents can be added/removed without colony bottleneck

**Query Performance**:
- **Summary queries**: Fast (<100ms) from colony DuckDB
    - "What's the average CPU across all services?"
    - "Show me deployment timeline for last week"
- **Detail queries**: Fast (<500ms) from specific agent
    - "Show me exact memory pattern for api-service before crash"
    - Agent responds from its local DuckDB

**Network Efficiency**:
- Normal operations: Only summaries transmitted (<1KB/min/agent)
- Deep investigation: Colony pulls details from specific agents only
- No continuous streaming of raw metrics

**Colony Resource Usage**:
```
Example: 1000 agents

Without layered storage:
- All raw metrics (1000 agents × 100 metrics × 10s resolution × 1 hour)
- Colony DuckDB: ~36M data points/hour
- Memory: ~2-3GB just for metrics
- Disk: ~100GB/day uncompressed

With layered storage:
- Only summaries (1000 agents × 10 summaries × 1min resolution)
- Colony DuckDB: ~600K data points/hour
- Memory: ~100MB for summaries
- Disk: ~5GB/day
- On-demand details: Queried from agents as needed

Result: 60x reduction in colony storage
```


### Query Routing

Colony intelligently routes queries based on data needs:

**Query Types**:

1. **Summary/Trend Queries** → Colony DuckDB
    - "What's the P95 latency trend over the last week?"
    - "Show me all deployments in the last 24 hours"
    - "Which services are using the most memory?"

2. **Recent Detail Queries** → Agent DuckDB
    - "Show me exact CPU pattern for api-service in the last 10 minutes"
    - "What were the connection errors on worker-3 before the crash?"
    - "Give me raw metrics at 1s resolution for the spike"

3. **Cross-Agent Correlation** → Both layers
    - Colony: Identifies pattern across multiple services
    - Agents: Queried for detailed data to confirm hypothesis

**Example Routing Logic**:
```python
def route_query(query):
    if query.time_range > 1_hour:
        # Historical data, use colony summaries
        return colony.query_duckdb(query)

    elif query.resolution == "raw" or query.resolution < "1m":
        # High-resolution recent data, query specific agent(s)
        agents = identify_relevant_agents(query.services)
        return parallel_query_agents(agents, query)

    elif query.is_cross_service():
        # Cross-service correlation
        # 1. Get summary from colony
        summary = colony.query_duckdb(query)
        # 2. If needed, get details from agents
        if needs_detail_investigation(summary):
            details = query_relevant_agents(summary.affected_services)
            return correlate(summary, details)
        return summary

    else:
        # Default: use colony
        return colony.query_duckdb(query)
```

### Data Retention Policies

**Agent Layer**:
- Raw metrics: **1 hour** (configurable: 30min - 2hours)
- Events: **1 hour** with exception for crash events (kept until pushed to colony)
- Automatic cleanup: DuckDB DELETE WHERE timestamp < now() - interval '1 hour'

**Colony Layer**:
- Event log: **30 days** for all events, **indefinite** for critical events (crashes, deploys)
- Metric summaries:
    - 5-minute resolution: **7 days**
    - 15-minute resolution: **30 days**
    - 1-hour resolution: **90 days**
    - 1-day resolution: **1 year**
- Topology/baselines: **Indefinite** (continuously updated)
- AI insights: **30 days** active, **90 days** archived

**Storage Estimation** (Colony):
```
Example: 100 services

Metric summaries:
- 5min (7 days): 100 services × 50 metrics × (7d × 288 intervals/day) × 40 bytes ≈ 400MB
- 15min (30 days): 100 services × 50 metrics × (30d × 96 intervals/day) × 40 bytes ≈ 576MB
- 1hr (90 days): 100 services × 50 metrics × (90d × 24 intervals/day) × 40 bytes ≈ 432MB
- 1day (1 year): 100 services × 50 metrics × 365 days × 40 bytes ≈ 73MB

Events (30 days): ~10K events × 500 bytes ≈ 5MB

Topology + baselines: ~10MB

Total: ~1.5GB for 100 services over full retention
```

### Why DuckDB?

**Advantages for this architecture**:
- **Embedded database**: No separate process, easy deployment
- **Columnar storage**: Excellent for time-series and analytical queries
- **SQL interface**: No custom query language needed
- **Built-in aggregations**: window functions, percentiles, stddev natively supported
- **Fast queries**: <100ms for analytical queries over 7-day windows
- **Compression**: 5-10x compression for time-series data
- **Zero-config**: No tuning required for most use cases

**Performance Characteristics**:
- Agent DuckDB (1hr data): 10-50MB memory, queries <50ms
- Colony DuckDB (summaries): 100MB-1GB memory, queries <100ms
- Ingest rate: >100K rows/sec per DuckDB instance
- Query concurrency: 10-20 concurrent queries without degradation

**Alternative Considered**: PostgreSQL
- Pros: More familiar, better for relational data, mature ecosystem
- Cons: Slower for analytical queries, requires more tuning, heavier resource usage
- Decision: DuckDB for time-series/analytics, PostgreSQL optional for colony metadata

### Migration Path

**Phase 1: Centralized (Current/Simple)**
- All data stored in colony DuckDB
- Works for typical applications (~5-20 services)
- Simple to implement and understand
- Perfect for local development

**Phase 2: Layered (Scalability)**
- Add agent-local DuckDB
- Implement summary push + detail pull
- Colony remains compatible with Phase 1 queries
- Scales to complex applications (50+ services)

**Phase 3: Reef Architecture (Future)**
- Multiple colonies federated together into a "reef"
- Use cases:
    - **Multi-environment**: dev-colony, staging-colony, prod-colony in one reef
    - **Multi-application**: shop-colony, blog-colony, analytics-colony in one reef
    - **Geographic**: US-colony, EU-colony, Asia-colony in one reef
- Cross-colony queries and topology visualization
- Each colony stores its own data, reef provides unified view
- Scales to entire product portfolios
