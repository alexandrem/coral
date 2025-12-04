## Storage Architecture

Coral uses a **layered storage architecture** where agents store recent raw data
locally, and the colony stores aggregated summaries for long-term analysis.

### Architecture Overview

```
┌────────────────────────────────────────────────────────────┐
│                   COLONY (Central DuckDB)                  │
│                   Summaries + History                      │
│                                                            │
│  • Aggregated metrics (compressed summaries)               │
│  • Historical trends and patterns                          │
│  • Cross-agent correlations                                │
│  • Topology graph (service dependencies)                   │
│  • Event history (deploys, crashes, anomalies)             │
│                                                            │
│  Can query agents on-demand for recent details             │
└─────────────────┬──────────────────────────────────────────┘
                  │ gRPC
    ┌─────────────┼─────────────┬─────────────┐
    │             │             │             │
┌───▼─────┐  ┌───▼─────┐  ┌───▼─────┐  ┌───▼─────┐
│ AGENT 1 │  │ AGENT 2 │  │ AGENT 3 │  │ AGENT N │
│         │  │         │  │         │  │         │
│ DuckDB: │  │ DuckDB: │  │ DuckDB: │  │ DuckDB: │
│ • Raw   │  │ • Raw   │  │ • Raw   │  │ • Raw   │
│   metrics│ │   metrics│ │   metrics│ │   metrics│
│   (~1hr)│  │   (~1hr)│  │   (~1hr)│  │   (~1hr)│
│ • Events│  │ • Events│  │ • Events│  │ • Events│
└─────────┘  └─────────┘  └─────────┘  └─────────┘
```

### Storage Layers

#### Agent Layer (Local DuckDB)

**Purpose**: Store recent high-resolution data locally.

**What agents store:**

- **Raw metrics** (~1 hour retention): Process CPU/memory, network connections,
  custom metrics
- **Events** (last 1 hour): Process lifecycle, connection events, health checks
- **Process observations**: Active connections, resource snapshots, version info

**Retention**: Raw data kept for ~1 hour, then discarded.

**Data flow**:

```
Agent observes → Store in local DuckDB
                       ↓
              Colony queries for data when needed
```

#### Colony Layer (Central DuckDB)

**Purpose**: Store summaries, aggregations, and cross-agent correlations.

**What colony stores:**

- **Aggregated metrics**: Per-service summaries (p50, p95, p99, mean, max),
  downsampled time-series (5min, 15min, 1hr intervals)
- **Event correlations**: Cross-service events, anomaly detection results,
  AI-generated insights
- **Topology graph**: Service dependency map (auto-discovered), connection
  metadata, version tracking
- **Learned baselines**: Normal behavior patterns, statistical models, anomaly
  thresholds

**Data flow**:

```
Colony periodically queries agents → Agents return data → Colony aggregates and stores
                                                               ↓
                                                      AI analysis on colony data
```

### Data Aggregation

**Pull Model**:

- Colony periodically queries agents for recent data
- Agents respond with metrics, events, and observations from local DuckDB
- Colony computes aggregations (percentiles, summaries, trends)
- Colony stores aggregated results for long-term analysis

**Detail Retrieval**:

- During investigations, colony queries specific agents for high-resolution data
- Used when AI needs detailed context or user requests specific timeframes
- Agents respond with raw data from local DuckDB

### Query Routing

Colony routes queries based on data needs:

1. **Summary/Trend Queries** → Colony DuckDB
    - "What's the P95 latency trend over the last week?"
    - "Which services are using the most memory?"

2. **Recent Detail Queries** → Agent DuckDB
    - "Show exact CPU pattern for api-service in last 10 minutes"
    - "What were connection errors before the crash?"

3. **Cross-Agent Correlation** → Both layers
    - Colony identifies patterns across services
    - Agents provide detailed data to confirm hypothesis

### Data Retention

**Agent Layer**:

- Raw metrics: 1 hour (configurable: 30min - 2hours)
- Events: 1 hour, with crash events kept until pushed to colony
- Automatic cleanup via scheduled deletion

**Colony Layer**:

- Metric summaries:
    - 5-minute resolution: 7 days
    - 15-minute resolution: 30 days
    - 1-hour resolution: 90 days
    - 1-day resolution: 1 year
- Event log: 30 days (critical events like crashes/deploys kept indefinitely)
- Topology/baselines: Indefinite (continuously updated)

### Why DuckDB?

DuckDB provides the ideal characteristics for this architecture:

- **Embedded**: No separate process, easy deployment
- **Columnar storage**: Excellent for time-series and analytical queries
- **SQL interface**: Standard query language
- **Built-in aggregations**: Window functions, percentiles, stddev natively
  supported
- **Fast queries**: <100ms for analytical queries over multi-day windows
- **Compression**: 5-10x compression for time-series data
- **Zero-config**: No tuning required

**Performance**:

- Agent DuckDB (1hr data): 10-50MB memory, queries <50ms
- Colony DuckDB (summaries): 100MB-1GB memory, queries <100ms
- Ingest rate: >100K rows/sec per instance

### Scalability

**Key benefits**:

- Each agent stores only its local data (~1 hour)
- Storage grows linearly with fleet size, distributed across agents
- Colony stores only compressed summaries (60x reduction vs. raw data)
- On-demand detail queries avoid continuous streaming overhead
- Agents can be added/removed without central bottleneck
