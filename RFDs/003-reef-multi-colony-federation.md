---
rfd: "003"
title: "Reef - Multi-Colony Federation"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: ["001", "002"]
database_migrations: []
areas: ["infrastructure", "federation", "ai"]
---

# RFD 003 - Reef: Multi-Colony Federation

**Status:** 🚧 Draft

## Summary

Introduce "Reef" - a meta-colony that federates multiple colonies to enable
persistent cross-environment correlation, historical multi-colony patterns, and
unified AI analysis across all managed applications. A Reef aggregates data from
multiple colonies (using ClickHouse for scale) and hosts an enterprise-grade LLM
service (via Genkit) that provides consistent, server-side intelligence through
both Buf Connect RPC (for `coral reef` commands) and MCP server (for external tools).

## Problem

**Current behavior/limitations:**

With RFD 002's per-colony isolation model:

- Each colony operates independently (my-shop-prod, my-shop-staging, my-shop-dev)
- No persistent cross-environment correlation ("Does staging behavior predict prod issues?")
- No historical multi-colony patterns ("Prod always spikes 2h after staging deploys")
- Multi-colony queries are stateless (RFD 002 enhancement) - AI must re-analyze every time
- No unified view across all applications you manage

**Why this matters:**

- **Environment comparison**: "Why is prod 20% slower than staging for same load?"
- **Deployment correlation**: "Did staging deploy cause prod errors 2 hours later?"
- **Cross-app insights**: "Payment API slowdown is affecting checkout service"
- **Fleet-wide health**: "Which services are running old versions across all apps?"
- **Predictive monitoring**: "Staging shows pattern that preceded last prod outage"

**Use cases affected:**

- DevOps teams managing multiple environments (dev/staging/prod)
- Platform teams overseeing multiple applications
- SREs investigating cross-environment incidents
- Teams wanting to learn from staging before production issues occur
- Organizations with complex microservice architectures

## Solution

Introduce **Reef** - a lightweight federation layer that sits above colonies and
provides unified intelligence:

**Key Design Decisions:**

- **Reef as meta-colony**: Runs like a colony, but stores aggregated data from multiple child colonies
  - Uses ClickHouse for distributed, scalable time-series storage (not DuckDB)
  - Queries child colonies via Buf Connect (gRPC over HTTP/2)
  - Stores summaries and cross-colony correlations with long retention (90d-1y)

- **WireGuard mesh peering (RFD 005)**: Reef peers into each colony's mesh
  - Reef generates ephemeral WireGuard keys per colony connection
  - Each colony assigns Reef a mesh IP (e.g., 10.42.0.100)
  - Authentication via colony_secret (same as agents/proxies)
  - No TLS needed (WireGuard provides encryption)
  - Unified security model across all components

- **Pull-based federation**: Reef periodically pulls summaries from colonies
  - Colonies push event streams to Reef (important events only)
  - Reef queries colonies on-demand for detailed data (federated queries)
  - All communication over encrypted WireGuard tunnels

- **AI-powered correlation**: Reef runs cross-colony correlation queries
  - "API latency in staging predicts prod issues 2 hours later"
  - "Database restarts in dev correlate with memory leaks in prod"
  - "Version X deployment shows consistent pattern across environments"

- **Server-side LLM service**: Reef hosts enterprise Genkit-powered LLM
  - Provides consistent, audited AI analysis across the organization
  - Dual interface: Buf Connect RPC (for `coral reef` commands) + MCP server (for external tools)
  - LLM queries ClickHouse for context (federated metrics, correlations, deployment timeline)
  - No client-side LLM required (unlike `coral ask` which uses local Genkit)

- **Backward compatible**: Colonies work standalone, Reef is optional
  - Existing colonies continue working without Reef
  - Reef can be added later without migration

**Benefits:**

- Persistent cross-environment intelligence (not re-analyzed on every query)
- Historical pattern detection ("This happened before in staging")
- Predictive monitoring ("Staging shows early warning signs")
- Unified dashboard across all environments and applications
- Reduced AI costs (correlation results cached, not re-computed)

**Architecture Overview:**

```
┌────────────────────────────────────────────────────────────┐
│  Reef (Meta-Colony)                                        │
│  Location: Central infrastructure (cluster deployment)     │
│                                                            │
│  WireGuard Interfaces:                                    │
│  ├─ coral-prod0:   10.42.0.100 (prod colony mesh)        │
│  ├─ coral-staging0: 10.43.0.100 (staging colony mesh)    │
│  └─ coral-payments0: 10.44.0.100 (payments colony mesh)  │
│                                                            │
│  ┌──────────────────────────────────────────────────────┐ │
│  │  ClickHouse: Federated Storage (Distributed)         │ │
│  │                                                       │ │
│  │  - Aggregated metrics from all colonies (90d)        │ │
│  │  - Cross-colony correlations (1y retention)          │ │
│  │  - Historical deployment timeline (2y)               │ │
│  │  - Cross-app dependency graph                        │ │
│  │  - Materialized views for fast queries               │ │
│  └──────────────────────────────────────────────────────┘ │
│                                                            │
│  ┌──────────────────────────────────────────────────────┐ │
│  │  Genkit LLM Service: Server-Side Intelligence        │ │
│  │                                                       │ │
│  │  - Enterprise-grade model (consistent across org)    │ │
│  │  - Queries ClickHouse for context                    │ │
│  │  - Buf Connect RPC: coral reef analyze <question>    │ │
│  │  - MCP Server: for external tools (Claude Desktop)   │ │
│  │                                                       │ │
│  │  Analysis Examples:                                  │ │
│  │  - "Staging deploy predicted prod issue"             │ │
│  │  - "Payment API affecting checkout performance"      │ │
│  │  - "Database connection pool exhaustion pattern"     │ │
│  └──────────────────────────────────────────────────────┘ │
│                                                            │
│  ┌──────────────────────────────────────────────────────┐ │
│  │  Dashboard: Unified View                             │ │
│  │  - All environments side-by-side                     │ │
│  │  - Deployment timeline across fleet                  │ │
│  │  - Cross-colony correlation graphs                   │ │
│  └──────────────────────────────────────────────────────┘ │
└──────────────┬──────────────┬──────────────┬─────────────┘
               │              │              │
    WireGuard  │   WireGuard  │   WireGuard  │  (Encrypted tunnels)
     Mesh      │     Mesh     │     Mesh     │
               │              │              │
      ┌────────▼────┐  ┌──────▼──────┐  ┌───▼──────────┐
      │  Colony     │  │  Colony     │  │  Colony      │
      │  my-shop    │  │  my-shop    │  │  payments    │
      │  prod       │  │  staging    │  │  prod        │
      │  10.42.0.1  │  │  10.43.0.1  │  │  10.44.0.1   │
      │             │  │             │  │              │
      │ - Agents    │  │ - Agents    │  │ - Agents     │
      │ - DuckDB or │  │ - DuckDB or │  │ - DuckDB or  │
      │   ClickHouse│  │   ClickHouse│  │   ClickHouse │
      │   (config)  │  │   (config)  │  │   (config)   │
      └─────────────┘  └─────────────┘  └──────────────┘
```

**How Reef differs from Colonies:**

| Feature | Colony (RFD 001/002) | Reef (RFD 003) |
|---------|---------------------|----------------|
| **Scope** | Single application + environment | Multiple colonies (all envs/apps) |
| **Agents** | Connects to application agents | Connects to colonies (no agents) |
| **Storage** | DuckDB (dev) or ClickHouse (prod) | ClickHouse (required for scale) |
| **Retention** | Hours to days | 90d-1y (configurable) |
| **LLM** | None (MCP gateway only) | Server-side Genkit service |
| **AI Analysis** | Via external clients (coral ask) | Cross-colony correlation (Reef LLM) |
| **Use Case** | "Is my API healthy?" | "Why does prod differ from staging?" |

### Component Changes

1. **Reef** (new component):
   - Runs as a separate process (cluster deployment recommended)
   - Manages list of child colonies (with credentials)
   - Periodically pulls summaries from colonies
   - Stores federated data in ClickHouse (distributed time-series storage)
   - Hosts Genkit LLM service (server-side AI for consistent analysis)
   - Exposes dual interface:
     - Buf Connect RPC: ReefLLM service for `coral reef` commands
     - MCP Server: data tools for external clients (Claude Desktop, etc.)
   - Exposes unified dashboard and API

2. **Colony** (reef integration):
   - New gRPC endpoint: `GetSummary()` - Returns recent metrics/events for Reef
   - New event stream: `StreamEvents()` - Pushes important events to Reef
   - No breaking changes - colonies work standalone without Reef

3. **CLI** (reef commands):
   - `coral reef init <name>`: Initialize new reef
   - `coral reef add-colony <colony-id>`: Add colony to reef
   - `coral reef start`: Start reef process
   - `coral reef status`: Show reef and all colonies
   - `coral ask --reef <name>`: Query across all colonies in reef

4. **Dashboard** (reef view):
   - Multi-colony view (all environments side-by-side)
   - Cross-colony correlation graphs
   - Deployment timeline across all colonies
   - Unified health status

**Configuration Example:**

**Reef config** (`~/.coral/reefs/<reef-id>.yaml`):
```yaml
# Reef identity
reef_id: my-infrastructure-reef-x7y8z9
name: my-infrastructure
description: All my production infrastructure

# Child colonies (Reef peers into each colony's WireGuard mesh)
colonies:
  - colony_id: my-shop-production-a3f2e1
    # Mesh access (via WireGuard tunnel)
    mesh:
      mesh_ip: 10.42.0.1             # Colony's mesh IP
      connect_port: 9000             # Buf Connect port on mesh
      colony_secret: <secret>        # For authentication
    # Reef's assigned mesh IP (from colony)
    reef_mesh_ip: 10.42.0.100        # Assigned by colony after peering

  - colony_id: my-shop-staging-b7c8d2
    mesh:
      mesh_ip: 10.43.0.1
      connect_port: 9000
      colony_secret: <secret>
    reef_mesh_ip: 10.43.0.100

  - colony_id: payments-api-prod-c2d5e8
    mesh:
      mesh_ip: 10.44.0.1
      connect_port: 9000
      colony_secret: <secret>
    reef_mesh_ip: 10.44.0.100

# Reef storage (ClickHouse required)
storage:
  type: clickhouse
  connection:
    host: clickhouse-reef.internal
    port: 9000
    database: coral_reef_my_infrastructure
    user: reef_writer
    password_env: REEF_CLICKHOUSE_PASSWORD
  retention:
    aggregated_metrics: 90d  # Keep 90 days of federated metrics
    correlations: 1y         # Keep correlation patterns for 1 year
    deployment_timeline: 2y  # Keep deployment history for 2 years

# Data collection
collection:
  summary_interval: 60s      # Pull summaries from colonies every 60s
  event_stream: true         # Receive real-time events from colonies

# AI analysis (Genkit LLM service)
ai:
  # Server-side LLM configuration
  llm:
    provider: "anthropic:claude-3-5-sonnet-20241022"  # Enterprise model
    api_key_env: ANTHROPIC_API_KEY
    fallback_provider: "openai:gpt-4o"

  # Automated correlation analysis
  correlation_enabled: true
  correlation_interval: 300s  # Run correlation analysis every 5 minutes

  # Rate limiting for coral reef commands
  rate_limit:
    requests_per_user_per_hour: 100
    max_concurrent_requests: 10

# Dashboard
dashboard:
  enabled: true
  port: 3100  # Different from colony port (3000)
```

## API Changes

### New Buf Connect Service (Reef LLM)

**File: `proto/coral/reef/v1/llm.proto`**

Reef exposes a server-side LLM service via Buf Connect RPC for `coral reef` commands. This provides consistent, enterprise-grade AI analysis without requiring local LLM setup.

```protobuf
syntax = "proto3";
package coral.reef.v1;

import "google/protobuf/timestamp.proto";

option go_package = "github.com/coral-io/coral/proto/reef/v1;reefpb";

// Reef LLM Service - Server-side AI analysis
service ReefLLM {
  // Analyze a question across all colonies in reef
  rpc Analyze(AnalyzeRequest) returns (AnalyzeResponse);

  // Compare environments (prod vs staging, etc.)
  rpc CompareEnvironments(CompareRequest) returns (CompareResponse);

  // Get deployment impact analysis
  rpc AnalyzeDeployment(DeploymentRequest) returns (DeploymentResponse);

  // Stream real-time analysis (for long investigations)
  rpc StreamAnalysis(AnalyzeRequest) returns (stream AnalysisChunk);
}

message AnalyzeRequest {
  string reef_id = 1;
  string question = 2;

  // Optional filters
  repeated string colony_ids = 3;     // Limit to specific colonies
  string time_window = 4;             // "1h", "24h", "7d"
  bool include_correlations = 5;      // Include historical patterns
}

message AnalyzeResponse {
  string answer = 1;                  // Natural language answer

  // Evidence from Reef's analysis
  repeated Evidence evidence = 2;

  // Suggested actions
  repeated Action actions = 3;

  // Metadata
  AnalysisMetadata metadata = 4;
}

message Evidence {
  string type = 1;                    // "metric", "event", "correlation"
  string colony_id = 2;
  string description = 3;
  string query = 4;                   // SQL query that produced this evidence
  map<string, string> data = 5;       // Actual data points
}

message Action {
  string description = 1;
  string command = 2;                 // Optional coral command to run
  bool requires_approval = 3;
}

message AnalysisMetadata {
  google.protobuf.Timestamp analyzed_at = 1;
  string model_used = 2;
  int32 colonies_queried = 3;
  int32 tokens_used = 4;
  float confidence_score = 5;         // 0.0-1.0
}

message AnalysisChunk {
  string content = 1;                 // Partial answer (for streaming UX)
  bool complete = 2;                  // Final chunk
}

message CompareRequest {
  string reef_id = 1;
  string environment_a = 2;           // "production"
  string environment_b = 3;           // "staging"
  string metric = 4;                  // "latency", "error_rate", "throughput"
  string time_window = 5;
}

message CompareResponse {
  string summary = 1;                 // e.g., "Production is 35% slower"
  repeated Difference differences = 2;
  string recommendation = 3;
}

message Difference {
  string metric_name = 1;
  double value_a = 2;
  double value_b = 3;
  double percent_change = 4;
  string significance = 5;            // "critical", "warning", "info"
}

message DeploymentRequest {
  string reef_id = 1;
  string deployment_id = 2;           // From Reef's deployment_timeline table
}

message DeploymentResponse {
  string summary = 1;
  DeploymentImpact impact = 2;
  repeated Evidence evidence = 3;
  string recommendation = 4;
}

message DeploymentImpact {
  string overall_status = 1;          // "success", "degraded", "failed"
  repeated MetricChange changes = 2;
  repeated RelatedIncident incidents = 3;
}

message MetricChange {
  string metric_name = 1;
  double before_value = 2;
  double after_value = 3;
  double percent_change = 4;
}

message RelatedIncident {
  string event_id = 1;
  google.protobuf.Timestamp occurred_at = 2;
  string description = 3;
  float correlation_score = 4;
}
```

### New Protobuf Service (Colony → Reef)

**File: `proto/coral/reef/v1/federation.proto`**

```protobuf
syntax = "proto3";
package coral.reef.v1;

import "google/protobuf/timestamp.proto";

option go_package = "github.com/coral-io/coral/proto/reef/v1;reefpb";

// Colony-side service for Reef to query
service ColonyFederation {
  // Get recent summary (metrics, events, health)
  rpc GetSummary(GetSummaryRequest) returns (GetSummaryResponse);

  // Stream important events to Reef (long-lived connection)
  rpc StreamEvents(StreamEventsRequest) returns (stream Event);

  // Get detailed metrics (on-demand federated query)
  rpc GetMetrics(GetMetricsRequest) returns (GetMetricsResponse);

  // Get service topology
  rpc GetTopology(GetTopologyRequest) returns (GetTopologyResponse);
}

// Summary request
message GetSummaryRequest {
  // Time range for summary
  google.protobuf.Timestamp start_time = 1;
  google.protobuf.Timestamp end_time = 2;

  // Authentication (colony_secret verified via WireGuard peer registration)
  // No explicit auth field needed - reef authenticated during WireGuard peering
}

message GetSummaryResponse {
  // Colony identity
  string colony_id = 1;
  string application_name = 2;
  string environment = 3;

  // Aggregated metrics
  repeated MetricSummary metrics = 4;

  // Important events
  repeated Event events = 5;

  // Health status
  HealthSummary health = 6;

  // Service topology snapshot
  TopologySummary topology = 7;
}

message MetricSummary {
  string service_id = 1;
  string metric_name = 2;
  google.protobuf.Timestamp timestamp = 3;

  // Aggregated values
  double p50 = 4;
  double p95 = 5;
  double p99 = 6;
  double mean = 7;
  double max = 8;
  int64 count = 9;
}

message Event {
  string event_id = 1;
  google.protobuf.Timestamp timestamp = 2;
  string event_type = 3;  // deploy, restart, crash, alert, error_spike
  string service_id = 4;
  string severity = 5;     // info, warning, error, critical

  // Event details
  map<string, string> metadata = 6;
  string description = 7;
}

message HealthSummary {
  string overall_status = 1;  // healthy, degraded, unhealthy
  int32 total_services = 2;
  int32 healthy_services = 3;
  int32 degraded_services = 4;
  int32 unhealthy_services = 5;
}

message TopologySummary {
  repeated ServiceNode services = 1;
  repeated ServiceConnection connections = 2;
}

message ServiceNode {
  string service_id = 1;
  string name = 2;
  string version = 3;
  string status = 4;
}

message ServiceConnection {
  string from_service = 1;
  string to_service = 2;
  string protocol = 3;
  int64 request_count = 4;
}

// Event streaming
message StreamEventsRequest {
  // Authentication via WireGuard mesh peer (no explicit field needed)

  // Filter options
  repeated string event_types = 1;  // Only stream these event types
  string min_severity = 2;          // Only stream events >= this severity
}

// Detailed metrics request (federated query)
message GetMetricsRequest {
  // Authentication via WireGuard mesh peer (no explicit field needed)

  string service_id = 1;
  string metric_name = 2;
  google.protobuf.Timestamp start_time = 3;
  google.protobuf.Timestamp end_time = 4;
  string resolution = 5;  // "1s", "10s", "1m"
}

message GetMetricsResponse {
  repeated MetricPoint points = 1;
}

message MetricPoint {
  google.protobuf.Timestamp timestamp = 1;
  double value = 2;
  map<string, string> labels = 3;
}

// Topology request
message GetTopologyRequest {
  // Authentication via WireGuard mesh peer (no explicit field needed)
}

message GetTopologyResponse {
  TopologySummary topology = 1;
}
```

### Reef ClickHouse Schema

**Federated metrics table:**
```sql
CREATE TABLE federated_metrics (
  colony_id String,           -- Which colony this came from
  application_name String,    -- my-shop, payments-api
  environment String,         -- production, staging, dev
  service_id String,
  metric_name String,
  timestamp DateTime,

  -- Aggregated values (pre-aggregated from colony)
  p50 Float64,
  p95 Float64,
  p99 Float64,
  mean Float64,
  max_value Float64,
  sample_count UInt32
)
ENGINE = MergeTree()
PARTITION BY toYYYYMM(timestamp)
ORDER BY (colony_id, application_name, metric_name, timestamp)
SETTINGS index_granularity = 8192;

-- Materialized view for fast environment comparisons
CREATE MATERIALIZED VIEW env_metric_summary
ENGINE = AggregatingMergeTree()
PARTITION BY toYYYYMM(timestamp)
ORDER BY (application_name, environment, metric_name, toStartOfHour(timestamp))
AS SELECT
  application_name,
  environment,
  metric_name,
  toStartOfHour(timestamp) as hour,
  avgState(p95) as avg_p95,
  maxState(max_value) as max_value,
  sumState(sample_count) as total_samples
FROM federated_metrics
GROUP BY application_name, environment, metric_name, hour;
```

**Cross-colony events:**
```sql
CREATE TABLE federated_events (
  event_id String,
  colony_id String,
  application_name String,
  environment String,
  timestamp DateTime,
  event_type String,      -- deploy, restart, crash, alert, error_spike
  service_id String,
  severity String,        -- info, warning, error, critical
  description String,
  metadata String,        -- JSON-encoded metadata

  -- Correlation tracking
  correlation_group String,  -- AI-assigned group for related events
  correlation_score Float64  -- How strongly correlated (0.0-1.0)
)
ENGINE = MergeTree()
PARTITION BY toYYYYMM(timestamp)
ORDER BY (timestamp, application_name, event_type)
SETTINGS index_granularity = 8192;
```

**Cross-colony correlations:**
```sql
CREATE TABLE correlations (
  correlation_id String,
  correlation_type String,  -- deployment_cascade, error_propagation, latency_correlation

  -- Source event/metric
  source_colony_id String,
  source_service String,
  source_timestamp DateTime,

  -- Target event/metric
  target_colony_id String,
  target_service String,
  target_timestamp DateTime,

  -- Correlation strength
  correlation_score Float64,  -- 0.0 - 1.0
  confidence Float64,          -- Statistical confidence

  -- Time lag
  lag_seconds Int32,  -- How long after source did target occur

  -- AI analysis
  pattern_description String,
  occurrence_count UInt32,  -- How many times this pattern occurred

  first_observed DateTime,
  last_observed DateTime
)
ENGINE = MergeTree()
PARTITION BY toYYYYMM(first_observed)
ORDER BY (correlation_type, source_colony_id, source_service, first_observed)
SETTINGS index_granularity = 8192;
```

**Deployment timeline:**
```sql
CREATE TABLE deployment_timeline (
  deployment_id String,
  colony_id String,
  application_name String,
  environment String,
  service_id String,

  from_version String,
  to_version String,

  started_at DateTime,
  completed_at DateTime,
  status String,  -- success, failed, rolled_back

  -- Impact analysis (AI-generated)
  impact_score Float64,           -- 0.0-1.0 (how much impact did this deploy have)
  issues_detected Array(String),  -- ["latency_increase", "error_spike"]
  related_events Array(String)    -- Event IDs that correlate with this deploy
)
ENGINE = MergeTree()
PARTITION BY toYYYYMM(started_at)
ORDER BY (application_name, environment, started_at)
SETTINGS index_granularity = 8192;
```

### CLI Commands

```bash
# Initialize a reef
coral reef init <reef-name> [flags]
  --description <text>    # Human-readable description

# Example output:
$ coral reef init my-infrastructure --description "All production services"

Initializing Reef...
✓ Created reef ID: my-infrastructure-reef-x7y8z9
✓ Configuration saved to ~/.coral/reefs/my-infrastructure-reef-x7y8z9.yaml

Reef initialized successfully!

To add colonies:
  coral reef add-colony my-shop-production
  coral reef add-colony my-shop-staging

To start the reef:
  coral reef start

---

# Add colony to reef
coral reef add-colony <colony-id> [flags]
  --reef <reef-id>        # Which reef to add to (defaults to current)
  --colony-secret <secret> # Colony secret for WireGuard mesh peering

# Example output:
$ coral reef add-colony my-shop-production --colony-secret <secret>

Adding colony to reef my-infrastructure-reef-x7y8z9...

 ↳ Querying discovery for colony: my-shop-production
 ↳ Colony endpoint: 203.0.113.42:41820 (mesh IP: 10.42.0.1:9000)
 ↳ Establishing WireGuard tunnel...
 ↳ Registering reef as mesh peer...
 ↳ Assigned mesh IP: 10.42.0.100

✓ Colony added to reef configuration
✓ WireGuard tunnel established

---

# Start reef
coral reef start [flags]
  --reef <reef-id>        # Which reef to start (defaults to current)
  --daemon                # Run as background daemon

# Example output:
$ coral reef start

Starting Reef: my-infrastructure-reef-x7y8z9
✓ Loaded 3 colonies
✓ Connecting to colonies...
  - my-shop-production: Connected ✓
  - my-shop-staging: Connected ✓
  - payments-api-prod: Connected ✓

✓ Started event streams
✓ Started summary collection (60s interval)
✓ Started AI correlation engine
✓ Dashboard: http://localhost:3100

Reef is running!

---

# Show reef status
coral reef status [flags]
  --reef <reef-id>
  --json

# Example output:
$ coral reef status

Reef: my-infrastructure-reef-x7y8z9
Status: Running
Uptime: 2h 15m

Colonies (3):
  ✓ my-shop-production     [healthy]    12 services, 45 agents
  ✓ my-shop-staging        [healthy]     8 services, 24 agents
  ✓ payments-api-prod      [degraded]    4 services, 12 agents

Recent Correlations:
  - Staging deploy → Prod error spike (lag: 2h 15m, confidence: 0.89)
  - Payment API latency → Checkout timeout (lag: 15s, confidence: 0.95)

Dashboard: http://localhost:3100

---

# Query across reef (server-side LLM analysis)
coral reef analyze <question> [flags]
  --reef <reef-name>        # Which reef to query
  --colonies <list>         # Optional: limit to specific colonies
  --time-window <duration>  # Optional: time range (1h, 24h, 7d)
  --stream                  # Stream response for long analyses

# Example:
$ coral reef analyze "why is prod slower than staging?" --reef my-infrastructure

Analyzing across 3 colonies (server-side LLM)...
✓ Queried federated metrics (ClickHouse)
✓ Reviewed deployment timeline
✓ Checked correlation patterns

Finding: Production p95 latency is 35% higher than staging

Root cause analysis:
1. Database connection pool exhaustion in production
   - Prod: 95% pool utilization
   - Staging: 60% pool utilization

2. 3x higher traffic in production
   - Prod: 1200 req/s
   - Staging: 400 req/s

3. Correlation detected:
   - Staging showed same pattern 2 weeks ago before scaling event
   - Pattern: pool >90% → latency spike follows in 30 minutes

Recommendation:
  Increase database connection pool size from 100 → 200

Evidence:
  - Colony: my-shop-production
  - Metric: db.pool.utilization (95%)
  - Similar incident: 2024-10-15 in staging (resolved by pool increase)

Metadata:
  Model: anthropic:claude-3-5-sonnet-20241022
  Tokens: 2450 (1800 input, 650 output)
  Colonies queried: 3 (my-shop-production, my-shop-staging, payments-api-prod)
  Confidence: 0.92

---

# Compare environments
coral reef compare <env-a> <env-b> --metric <metric> [flags]
  --reef <reef-name>
  --time-window <duration>

# Example:
$ coral reef compare production staging --metric latency --reef my-infrastructure

Comparing production vs staging (last 24h):

Latency (p95):
  Production: 245ms
  Staging:    175ms
  Difference: +40.0% slower

Recommendation:
  Production shows higher database pool utilization.
  Consider scaling connection pool or investigating query performance.

---

# Deployment impact analysis
coral reef deployment <deployment-id> [flags]
  --reef <reef-name>

# Example:
$ coral reef deployment deploy-abc123 --reef my-infrastructure

Analyzing deployment: my-shop-v2.1.0 (production)
Started: 2024-11-13 14:30:00
Status: Success

Impact:
  ✓ Latency: No significant change (p95: 180ms → 185ms, +2.8%)
  ⚠ Error rate: Slight increase (0.01% → 0.03%, +200%)
  ✓ Throughput: Stable (1150 req/s)

Related incidents:
  - error_spike_xyz789 (15 minutes after deploy, correlation: 0.87)

Recommendation:
  Monitor error rate. Pattern matches staging deploy of same version
  where errors self-resolved after 30 minutes.
```

## Implementation Plan

### Phase 1: Foundation

- [ ] Define Reef configuration structure (ClickHouse connection, LLM config)
- [ ] Create Reef initialization workflow (`coral reef init`)
- [ ] Design ClickHouse schema for federated data (with partitioning)
- [ ] Define protobuf for Colony→Reef API (federation.proto)
- [ ] Define protobuf for ReefLLM Buf Connect service (llm.proto)

### Phase 2: Storage & Data Collection

- [ ] Set up ClickHouse deployment guide and migrations
- [ ] Implement Colony `GetSummary()` gRPC endpoint
- [ ] Implement Reef summary collection loop
- [ ] Implement Colony `StreamEvents()` for real-time events
- [ ] Store federated metrics and events in ClickHouse
- [ ] Create materialized views for fast queries

### Phase 3: Correlation Engine

- [ ] Implement basic cross-colony metric comparison
- [ ] Implement deployment timeline tracking
- [ ] Implement event correlation detection (time-based)
- [ ] Store correlation patterns in ClickHouse
- [ ] Add automated correlation analysis (background jobs)

### Phase 4: Genkit LLM Service

- [ ] Integrate Genkit Go SDK with Reef
- [ ] Implement ReefLLM Buf Connect service (Analyze, CompareEnvironments, AnalyzeDeployment)
- [ ] Build context retrieval from ClickHouse (query builder for LLM context)
- [ ] Implement streaming analysis for long-running queries
- [ ] Add rate limiting and cost tracking
- [ ] Implement MCP server interface for external tools

### Phase 5: CLI & Dashboard

- [ ] Implement `coral reef` CLI commands (analyze, compare, deployment)
- [ ] Implement Buf Connect client in CLI for ReefLLM service
- [ ] Create unified dashboard showing all colonies
- [ ] Add cross-colony correlation visualizations
- [ ] Add LLM analysis results display

### Phase 6: Testing & Documentation

- [ ] Unit tests for ClickHouse queries, LLM service, correlation detection
- [ ] Integration tests for Reef ↔ Colony communication
- [ ] E2E tests for `coral reef analyze` with seeded data
- [ ] Documentation: ClickHouse setup, LLM configuration, deployment guide

## Testing Strategy

### Unit Tests

- Reef configuration loading and validation
- Federated query construction
- Correlation score calculation
- WireGuard mesh peering authentication
- Colony secret validation

### Integration Tests

- Reef collecting summaries from multiple colonies
- Event streaming from colony to reef
- Cross-colony metric aggregation
- Correlation detection across events

### E2E Tests

**Scenario 1: Basic Reef Setup**
```bash
# Initialize reef and add colonies
coral reef init my-infra
coral reef add-colony my-shop-production
coral reef add-colony my-shop-staging
coral reef start

# Verify: Reef collects data from both colonies
# Verify: Dashboard shows both colonies
```

**Scenario 2: Cross-Environment Comparison**
```bash
# Deploy to staging
deploy-to-staging v2.0.0

# Wait and check if issues detected
coral ask "any issues in staging?" --reef my-infra

# Compare with production
coral ask "compare staging vs prod latency" --reef my-infra
```

**Scenario 3: Correlation Detection**
```bash
# Simulate staging deploy followed by prod error
# Verify: Reef detects correlation after pattern repeats
# Verify: AI recommends investigating staging before prod deploy
```

## Security Considerations

### Reef-Colony Authentication

**Problem**: Reef needs access to colony data, but colonies are security-sensitive

**Solution**: WireGuard mesh peering with colony_secret authentication (same as agents/proxies)

**Flow:**
1. User runs `coral reef add-colony my-shop-production --colony-secret <secret>`
2. Reef queries discovery for colony endpoints
3. Reef establishes WireGuard tunnel to colony
4. Reef registers as mesh peer using `colony_secret`
5. Colony assigns mesh IP to reef (e.g., 10.42.0.100)
6. Reef queries colony via Buf Connect over encrypted tunnel

**Security Properties:**
- Authentication via WireGuard peer verification + colony_secret
- Encryption at network layer (no TLS needed)
- Mesh IP identifies reef peer (auditable)
- No additional credentials to manage after peering

### Data Isolation

- Reef stores aggregated data only (no raw sensitive data)
- Each reef has isolated storage (separate DuckDB files)
- Reef peers into multiple isolated meshes (one per colony)
- Compromising one colony mesh does NOT affect other meshes

### Access Control

- Reef must know colony_secret to peer (explicit authorization)
- Colony tracks reef mesh IP in peer registry
- Reef can be unpeer'd by colony (revoke access)
- Future: Read-only reef_secret separate from colony_secret

## Migration Strategy

**Reef is optional - colonies work without it:**

1. Existing colonies continue working standalone
2. Users can add Reef later without migration
3. Reef can be stopped without affecting colonies

**Gradual rollout:**
1. Deploy Reef support to colonies (new gRPC endpoints)
2. Initialize Reef on central infrastructure or laptop
3. Add colonies to Reef one by one
4. Start using `--reef` flag for cross-colony queries

## Future Enhancements

### Multi-Reef Support

Multiple reefs for different scopes:
```bash
# Production-only reef
coral reef init prod-infrastructure
coral reef add-colony my-shop-production
coral reef add-colony payments-prod

# Development reef
coral reef init dev-infrastructure
coral reef add-colony my-shop-dev
coral reef add-colony payments-dev
```

### Reef Federation

Reefs can federate with other reefs (hierarchical):
```
Global Reef (company-wide)
  ├── NA Reef (North America)
  │   ├── Colony: my-shop-us-east
  │   └── Colony: my-shop-us-west
  └── EU Reef (Europe)
      ├── Colony: my-shop-eu-west
      └── Colony: my-shop-eu-central
```

### Automated Actions

Reef can trigger automated responses:
```yaml
# Reef config
automation:
  - trigger: correlation_detected
    condition: "staging_error_spike → prod_error_prediction"
    action: notify_slack

  - trigger: pattern_match
    condition: "deployment_correlation > 0.9"
    action: recommend_rollback
```

### Machine Learning

Train ML models on reef data:
- Anomaly detection across environments
- Deployment success prediction
- Capacity planning across fleet
- Auto-scaling recommendations

## Appendix

### Correlation Detection Algorithms

**Time-based correlation:**
```sql
-- Find events in staging that precede production issues
SELECT
  staging.event_id as source_event,
  prod.event_id as target_event,
  (prod.timestamp - staging.timestamp) as lag
FROM federated_events staging
JOIN federated_events prod
  ON staging.environment = 'staging'
  AND prod.environment = 'production'
  AND prod.timestamp > staging.timestamp
  AND prod.timestamp < staging.timestamp + INTERVAL '4 hours'
WHERE staging.event_type IN ('deploy', 'error_spike')
  AND prod.event_type IN ('error_spike', 'crash')
GROUP BY staging.event_id, prod.event_id
HAVING COUNT(*) > 3  -- Pattern occurred at least 3 times
```

**Metric correlation:**
```sql
-- Find metrics that correlate across environments
SELECT
  s.metric_name,
  CORR(s.p95, p.p95) as correlation_score
FROM federated_metrics s
JOIN federated_metrics p
  ON s.metric_name = p.metric_name
  AND s.timestamp = p.timestamp
  AND s.environment = 'staging'
  AND p.environment = 'production'
WHERE s.timestamp > NOW() - INTERVAL '24 hours'
GROUP BY s.metric_name
HAVING CORR(s.p95, p.p95) > 0.8  -- Strong correlation
```

### Example Reef Queries

**Cross-environment health check:**
```bash
coral ask "compare health across all environments" --reef my-infra

# Returns:
Production: 95% healthy (1 degraded service: payment-processor)
Staging:   100% healthy
Dev:        88% healthy (2 services restarting after code change)
```

**Deployment impact analysis:**
```bash
coral ask "what was the impact of the last staging deploy?" --reef my-infra

# Returns:
Staging deployment v2.1.0 (2 hours ago):
- Latency: +15% increase (p95: 180ms → 207ms)
- Error rate: No change (0.01%)
- Memory: +8% increase

Similar pattern detected before production deploy of v2.0.0
Recommendation: Investigate latency increase before prod deploy
```

**Cross-app dependency analysis:**
```bash
coral ask "is payment-api affecting other services?" --reef my-infra

# Returns:
Payment API latency increase detected:
- Affected: checkout-service (timeout rate +12%)
- Affected: order-service (retry rate +18%)
- Not affected: frontend (cached responses)

Correlation score: 0.94 (high confidence)
Recommendation: Fix payment API latency or increase timeout thresholds
```

---

## Notes

**Design Philosophy:**

- **Reef is optional**: Colonies work standalone, reef adds intelligence
- **Pull-based**: Reef pulls from colonies (not push), works across networks
- **AI-powered**: Reef runs server-side LLM (Genkit) for consistent analysis
- **Scalable**: Uses ClickHouse for distributed time-series storage (not DuckDB)
- **Enterprise-grade**: Single LLM for consistent, audited analysis across organization

**Relationship to other RFDs:**

- RFD 001: Discovery service (unchanged, colonies still use mesh_id)
- RFD 002: Application identity (reef builds on colony concepts)
- RFD 004: MCP server (reef exposes MCP server for external tools like Claude Desktop)
- RFD 014: Abandoned (Colony-embedded LLM approach replaced by separated architecture)
- RFD 030: Coral ask CLI (local Genkit for single-colony analysis vs Reef's server-side LLM)

**LLM Integration Patterns:**

- **`coral ask`** (RFD 030): Local Genkit agent, developer's LLM choice, single colony context
- **`coral reef`** (this RFD): Server-side Genkit service, enterprise LLM, multi-colony federation
- **`coral proxy`** (RFD 004): MCP gateway for external tools (Claude Desktop, IDEs)

**When to use Reef:**

- ✅ Multiple environments (dev/staging/prod)
- ✅ Multiple applications
- ✅ Need cross-environment correlation
- ✅ Want to learn from staging before prod issues

**When NOT to use Reef:**

- ❌ Single colony deployment
- ❌ No cross-environment analysis needed
- ❌ Simple applications with no staging environment
