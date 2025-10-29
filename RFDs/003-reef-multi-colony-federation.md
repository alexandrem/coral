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
multiple colonies and provides holistic insights that single colonies cannot.

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
  - Uses same DuckDB architecture as colonies (proven, performant)
  - Queries child colonies via gRPC for detailed data
  - Stores summaries and cross-colony correlations

- **Pull-based federation**: Reef periodically pulls summaries from colonies
  - Colonies push event streams to Reef (important events only)
  - Reef queries colonies on-demand for detailed data (federated queries)
  - No always-on connection required (works across networks)

- **AI-powered correlation**: Reef runs cross-colony correlation queries
  - "API latency in staging predicts prod issues 2 hours later"
  - "Database restarts in dev correlate with memory leaks in prod"
  - "Version X deployment shows consistent pattern across environments"

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
│  Location: Central infrastructure or developer laptop      │
│                                                            │
│  ┌──────────────────────────────────────────────────────┐ │
│  │  DuckDB: Federated Storage                           │ │
│  │                                                       │ │
│  │  - Aggregated metrics from all colonies              │ │
│  │  - Cross-colony correlations                         │ │
│  │  - Historical deployment timeline                    │ │
│  │  - Cross-app dependency graph                        │ │
│  └──────────────────────────────────────────────────────┘ │
│                                                            │
│  ┌──────────────────────────────────────────────────────┐ │
│  │  AI Engine: Cross-Colony Analysis                    │ │
│  │                                                       │ │
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
        gRPC   │       gRPC   │       gRPC   │
               │              │              │
      ┌────────▼────┐  ┌──────▼──────┐  ┌───▼──────────┐
      │  Colony     │  │  Colony     │  │  Colony      │
      │  my-shop    │  │  my-shop    │  │  payments    │
      │  prod       │  │  staging    │  │  prod        │
      │             │  │             │  │              │
      │ - Agents    │  │ - Agents    │  │ - Agents     │
      │ - Local DB  │  │ - Local DB  │  │ - Local DB   │
      └─────────────┘  └─────────────┘  └──────────────┘
```

**How Reef differs from Colonies:**

| Feature | Colony (RFD 001/002) | Reef (RFD 003) |
|---------|---------------------|----------------|
| **Scope** | Single application + environment | Multiple colonies (all envs/apps) |
| **Agents** | Connects to application agents | Connects to colonies (no agents) |
| **Storage** | Recent + summarized app data | Aggregated cross-colony data |
| **AI Analysis** | Single-colony insights | Cross-colony correlation |
| **Use Case** | "Is my API healthy?" | "Why does prod differ from staging?" |

### Component Changes

1. **Reef** (new component):
   - Runs as a separate process (can be on same host as a colony)
   - Manages list of child colonies (with credentials)
   - Periodically pulls summaries from colonies
   - Stores federated data in DuckDB
   - Runs cross-colony AI correlation
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

# Child colonies
colonies:
  - colony_id: my-shop-production-a3f2e1
    access:
      endpoint: https://colony-prod.example.com:3000
      api_key: reef-access-token-1  # Generated by colony for reef

  - colony_id: my-shop-staging-b7c8d2
    access:
      endpoint: https://colony-staging.example.com:3000
      api_key: reef-access-token-2

  - colony_id: payments-api-prod-c2d5e8
    access:
      endpoint: https://payments-colony.example.com:3000
      api_key: reef-access-token-3

# Reef storage
storage:
  path: ~/.coral/reefs/my-infrastructure/reef.duckdb
  retention:
    aggregated_metrics: 90d  # Keep 90 days of federated metrics
    correlations: 1y         # Keep correlation patterns for 1 year

# Data collection
collection:
  summary_interval: 60s      # Pull summaries from colonies every 60s
  event_stream: true         # Receive real-time events from colonies

# AI analysis
ai:
  correlation_enabled: true
  correlation_interval: 300s  # Run correlation analysis every 5 minutes

# Dashboard
dashboard:
  enabled: true
  port: 3100  # Different from colony port (3000)
```

## API Changes

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

  // Authentication
  string reef_api_key = 3;
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
  string reef_api_key = 1;

  // Filter options
  repeated string event_types = 2;  // Only stream these event types
  string min_severity = 3;          // Only stream events >= this severity
}

// Detailed metrics request (federated query)
message GetMetricsRequest {
  string reef_api_key = 1;

  string service_id = 2;
  string metric_name = 3;
  google.protobuf.Timestamp start_time = 4;
  google.protobuf.Timestamp end_time = 5;
  string resolution = 6;  // "1s", "10s", "1m"
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
  string reef_api_key = 1;
}

message GetTopologyResponse {
  TopologySummary topology = 1;
}
```

### Reef DuckDB Schema

**Federated metrics table:**
```sql
CREATE TABLE federated_metrics (
  colony_id TEXT,           -- Which colony this came from
  application_name TEXT,    -- my-shop, payments-api
  environment TEXT,         -- production, staging, dev
  service_id TEXT,
  metric_name TEXT,
  timestamp TIMESTAMP,

  -- Aggregated values (pre-aggregated from colony)
  p50 DOUBLE,
  p95 DOUBLE,
  p99 DOUBLE,
  mean DOUBLE,
  max_value DOUBLE,
  sample_count INTEGER,

  PRIMARY KEY (colony_id, service_id, metric_name, timestamp)
);

CREATE INDEX idx_federated_app_metric ON federated_metrics(application_name, metric_name, timestamp);
CREATE INDEX idx_federated_env_metric ON federated_metrics(environment, metric_name, timestamp);
```

**Cross-colony events:**
```sql
CREATE TABLE federated_events (
  event_id TEXT PRIMARY KEY,
  colony_id TEXT,
  application_name TEXT,
  environment TEXT,
  timestamp TIMESTAMP,
  event_type TEXT,      -- deploy, restart, crash, alert, error_spike
  service_id TEXT,
  severity TEXT,        -- info, warning, error, critical
  description TEXT,
  metadata JSONB,

  -- Correlation tracking
  correlation_group TEXT,  -- AI-assigned group for related events
  correlation_score DOUBLE -- How strongly correlated (0.0-1.0)
);

CREATE INDEX idx_federated_events_time ON federated_events(timestamp);
CREATE INDEX idx_federated_events_app ON federated_events(application_name, timestamp);
CREATE INDEX idx_federated_events_correlation ON federated_events(correlation_group);
```

**Cross-colony correlations:**
```sql
CREATE TABLE correlations (
  correlation_id TEXT PRIMARY KEY,
  correlation_type TEXT,  -- deployment_cascade, error_propagation, latency_correlation

  -- Source event/metric
  source_colony_id TEXT,
  source_service TEXT,
  source_timestamp TIMESTAMP,

  -- Target event/metric
  target_colony_id TEXT,
  target_service TEXT,
  target_timestamp TIMESTAMP,

  -- Correlation strength
  correlation_score DOUBLE,  -- 0.0 - 1.0
  confidence DOUBLE,          -- Statistical confidence

  -- Time lag
  lag_seconds INTEGER,  -- How long after source did target occur

  -- AI analysis
  pattern_description TEXT,
  occurrence_count INTEGER,  -- How many times this pattern occurred

  first_observed TIMESTAMP,
  last_observed TIMESTAMP
);

CREATE INDEX idx_correlations_source ON correlations(source_colony_id, source_service);
CREATE INDEX idx_correlations_target ON correlations(target_colony_id, target_service);
```

**Deployment timeline:**
```sql
CREATE TABLE deployment_timeline (
  deployment_id TEXT PRIMARY KEY,
  colony_id TEXT,
  application_name TEXT,
  environment TEXT,
  service_id TEXT,

  from_version TEXT,
  to_version TEXT,

  started_at TIMESTAMP,
  completed_at TIMESTAMP,
  status TEXT,  -- success, failed, rolled_back

  -- Impact analysis (AI-generated)
  impact_score DOUBLE,      -- 0.0-1.0 (how much impact did this deploy have)
  issues_detected TEXT[],   -- ["latency_increase", "error_spike"]
  related_events TEXT[]     -- Event IDs that correlate with this deploy
);

CREATE INDEX idx_deployment_timeline ON deployment_timeline(application_name, started_at);
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
  --endpoint <url>        # Colony API endpoint
  --generate-key          # Generate API key for reef access

# Example output:
$ coral reef add-colony my-shop-production

Adding colony to reef my-infrastructure-reef-x7y8z9...

To authorize this reef, run on the colony host:
  coral colony grant-reef-access \
    --reef my-infrastructure-reef-x7y8z9 \
    --permissions read

Or manually add this API key to the colony config:
  reef_access:
    my-infrastructure-reef-x7y8z9: <generated-api-key>

✓ Colony added to reef configuration

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

# Query across reef
coral ask <question> --reef <reef-name>

# Example:
$ coral ask "why is prod slower than staging?" --reef my-infrastructure

Analyzing across 3 colonies...

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
```

## Implementation Plan

### Phase 1: Foundation

- [ ] Define Reef configuration structure
- [ ] Create Reef initialization workflow (`coral reef init`)
- [ ] Design DuckDB schema for federated data
- [ ] Define protobuf for Colony→Reef API

### Phase 2: Data Collection

- [ ] Implement Colony `GetSummary()` gRPC endpoint
- [ ] Implement Reef summary collection loop
- [ ] Implement Colony `StreamEvents()` for real-time events
- [ ] Store federated metrics and events in Reef DuckDB

### Phase 3: Correlation Engine

- [ ] Implement basic cross-colony metric comparison
- [ ] Implement deployment timeline tracking
- [ ] Implement event correlation detection (time-based)
- [ ] Store correlation patterns in DuckDB

### Phase 4: CLI & Dashboard

- [ ] Implement `coral reef` CLI commands
- [ ] Implement `coral ask --reef` for multi-colony queries
- [ ] Create unified dashboard showing all colonies
- [ ] Add cross-colony correlation visualizations

### Phase 5: AI Analysis

- [ ] Integrate AI for correlation analysis
- [ ] Implement pattern detection (staging → prod)
- [ ] Generate predictive insights
- [ ] Cache AI analysis results

## Testing Strategy

### Unit Tests

- Reef configuration loading and validation
- Federated query construction
- Correlation score calculation
- API key authentication for reef access

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

**Solution**: API key-based authentication with explicit grants

```yaml
# Colony config grants reef access
reef_access:
  my-infrastructure-reef-x7y8z9:
    api_key: <generated-key>
    permissions: [read]  # read-only access
    expires_at: 2026-01-01T00:00:00Z
```

**Flow:**
1. User runs `coral reef add-colony my-shop-production`
2. Reef generates API key
3. User manually adds key to colony config (or uses `coral colony grant-reef-access`)
4. Reef uses key in all gRPC requests
5. Colony validates key before returning data

### Data Isolation

- Reef stores aggregated data only (no raw sensitive data)
- Each reef has isolated storage (separate DuckDB files)
- Reef API key has limited permissions (read-only by default)

### Access Control

- Reef dashboard requires authentication (separate from colonies)
- API keys can be scoped to specific data types (metrics only, no events)
- API keys can be revoked without affecting colony operation

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
- **AI-powered**: Reef runs correlation analysis that single colonies can't
- **Lightweight**: Reef is just another process, uses proven DuckDB storage

**Relationship to other RFDs:**

- RFD 001: Discovery service (unchanged, colonies still use mesh_id)
- RFD 002: Application identity (reef builds on colony concepts)
- RFD 004: MCP server (reef can expose MCP server for unified access)

**When to use Reef:**

- ✅ Multiple environments (dev/staging/prod)
- ✅ Multiple applications
- ✅ Need cross-environment correlation
- ✅ Want to learn from staging before prod issues

**When NOT to use Reef:**

- ❌ Single colony deployment
- ❌ No cross-environment analysis needed
- ❌ Simple applications with no staging environment
