---
rfd: "046"
title: "Colony DuckDB Remote Query for Historical Metrics"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: ["039"]
database_migrations: []
areas: ["cli", "colony", "observability", "metrics", "duckdb"]
---

# RFD 046 - Colony DuckDB Remote Query for Historical Metrics

**Status:** 🚧 Draft

## Summary

Extend RFD 039's DuckDB HTTP remote attach feature to support querying the
colony's aggregated historical metrics database. This enables operators to
analyze long-term trends, perform fleet-wide queries, and access historical
data beyond agents' 1-hour retention window.

## Problem

**Current behavior/limitations**

- RFD 039 enables querying agent-local DuckDB databases containing ~1 hour of
  Beyla metrics.
- Colony stores aggregated metrics from all agents with longer retention (30
  days HTTP/gRPC, 14 days SQL per RFD 032).
- Colony's DuckDB database contains valuable historical data for trend analysis,
  capacity planning, and incident investigation.
- Operators cannot directly query colony's database - must use predefined MCP
  tools or custom RPC endpoints.
- Historical queries require either:
    - Waiting for MCP tool development
    - Adding new colony RPC endpoints
    - SSH-ing to colony host and manually querying DuckDB
- No way to perform ad-hoc SQL analysis across the entire fleet's historical
  data.

**Why this matters**

- **Historical analysis**: Operators need to analyze trends over days/weeks for
  capacity planning and SLA reporting.
- **Fleet-wide queries**: "Show me p99 latency for all services across all
  agents over the past 7 days."
- **Incident investigation**: During post-mortems, operators need to query
  historical data that's no longer on agents.
- **Cost efficiency**: Colony database contains pre-aggregated data that's more
  efficient to query than individual agent databases.
- **Unified access**: Same SQL interface and tools (DuckDB shell, query CLI) for
  both real-time (agents) and historical (colony) data.

**Use cases affected**

- SRE generating weekly latency reports: "Show me p99 HTTP latency by service
  for the past 30 days, grouped by week."
- Ops engineer investigating last week's incident: "Show me all 5xx errors from
  the auth service between Nov 10-12."
- Capacity planner analyzing growth trends: "Show me request volume growth rate
  by service over the past 3 months."
- Developer debugging intermittent issues: "Show me all SQL queries with
  latency > 1s over the past 14 days."

## Solution

Add a DuckDB HTTP endpoint to the colony server (port 9000) that serves its
aggregated metrics database. Extend the `coral duckdb` CLI commands to support
querying the colony database in addition to agent databases.

**Key Design Decisions:**

- **Reuse RFD 039 infrastructure**: Same DuckDB HTTP handler pattern, same CLI
  query/shell commands.
- **Single unified CLI**: `coral duckdb query` and `coral duckdb shell` support
  both agents and colony via target selection.
- **Read-only access**: Colony database served read-only over HTTP, no write
  operations.
- **No authentication beyond mesh**: Same trust model as RFD 039 - access
  controlled by WireGuard mesh membership.
- **Serve entire database**: Expose full colony DuckDB file over HTTP, not
  individual tables (DuckDB handles efficient range requests).

**Benefits:**

- **Zero new RPC endpoints**: Leverage HTTP file serving, no protobuf
  serialization.
- **Full SQL power**: Operators can use entire DuckDB SQL dialect for complex
  queries.
- **Consistent UX**: Same CLI commands and workflow as RFD 039 agent queries.
- **Efficient queries**: DuckDB's columnar format and aggregation optimizations
  for historical analysis.
- **No data duplication**: Query colony database directly, no need to export/ETL
  for analysis.

**Architecture Overview:**

```
┌─────────────┐                    ┌──────────────┐
│  coral CLI  │                    │    Colony    │
│             │                    │              │
│ duckdb cmd  │────────(1)─────────│  HTTP:9000   │
│             │  coral duckdb      │              │
│             │  query --colony    │  /duckdb/    │
│             │  <sql>             │  metrics.db  │
│             │                    │              │
│  DuckDB Go  │◄───────(2)─────────│  Serve DB   │
│  Driver     │  HTTP range reqs   │  read-only  │
│             │  (native DuckDB)   │              │
└─────────────┘                    └──────────────┘

                    ┌─────────────┐
                    │   Agents    │
                    │             │
                    │ HTTP:9001   │
                    │ /duckdb/    │
                    │ beyla.db    │
                    │             │
                    │ (RFD 039)   │
                    └─────────────┘
```

### Component Changes

1. **Colony (HTTP handler)**:
    - Reuse `internal/agent/duckdb_handler.go` (move to shared package).
    - Add `/duckdb/<filename>` endpoint to colony HTTP server.
    - Register colony database path with handler.
    - Handler validates read-only access, serves via `http.ServeFile`.

2. **CLI (extend existing duckdb commands)**:
    - Add `--colony` flag to `coral duckdb query` command.
    - Add `--colony` flag to `coral duckdb shell` command.
    - Extend `coral duckdb list-agents` to show colony database availability.
    - CLI resolves colony address from config (`~/.coral/config.yaml`).
    - Same DuckDB Go driver with `httpfs` extension.

3. **Configuration**:
    - Colony database path must be a real file (not `:memory:`) for HTTP
      serving.
    - Default path: `~/.coral/colony/metrics.duckdb` or
      `/var/lib/coral/colony/metrics.duckdb`.

**Configuration Example:**

```yaml
# Colony config
colony:
  database:
    path: ~/.coral/colony/metrics.duckdb
    # HTTP endpoint automatically enabled if path is set
```

```bash
# Query colony historical data
coral duckdb query --colony "SELECT service_name, AVG(latency_ms) FROM beyla_http_metrics WHERE timestamp > now() - INTERVAL '7 days' GROUP BY service_name"

# Query specific agent (real-time data, ~1 hour)
coral duckdb query agent-prod-1 "SELECT * FROM beyla_http_metrics_local LIMIT 10"

# Interactive shell (colony)
coral duckdb shell --colony

# List available databases
coral duckdb list
# Output:
# DATABASE      TYPE      RETENTION  TABLES  SIZE
# colony        colony    30d        12      4.2GB
# agent-prod-1  agent     1h         3       120MB
# agent-prod-2  agent     1h         3       115MB
```

## Implementation Plan

### Phase 1: Shared DuckDB Handler

- [ ] Move `internal/agent/duckdb_handler.go` to
  `internal/duckdb/http_handler.go` (shared package).
- [ ] Update agent code to use shared handler.
- [ ] Update agent tests.

### Phase 2: Colony HTTP Endpoint

- [ ] Add `/duckdb/` route to colony HTTP server.
- [ ] Ensure colony database uses file path (not `:memory:`).
- [ ] Register colony database with DuckDB handler.
- [ ] Add integration test: start colony, attach via DuckDB, query metrics.

### Phase 3: CLI Command Extensions

- [ ] Add `--colony` flag to `query` subcommand.
- [ ] Add `--colony` flag to `shell` subcommand.
- [ ] Add `list` subcommand (shows colony + agents).
- [ ] Add colony address resolution from config.
- [ ] Update CLI help text with colony examples.

### Phase 4: Testing & Documentation

- [ ] Add unit tests for colony DuckDB handler.
- [ ] Add E2E test: query colony database via CLI.
- [ ] Update RFD 039 documentation to reference colony support.
- [ ] Add example queries for common historical analysis patterns.

## API Changes

### New HTTP Endpoint (Colony)

**Path:** `/duckdb/<filename>`

**Method:** `GET`

**Description:** Serves colony DuckDB database for read-only remote attach.

**Authentication:** WireGuard mesh membership (same as RFD 039).

**Request:**

```http
GET /duckdb/metrics.duckdb HTTP/1.1
Host: colony.coral.mesh:9000
Range: bytes=0-16384
```

**Response (success):**

```http
HTTP/1.1 206 Partial Content
Content-Type: application/octet-stream
Content-Range: bytes 0-16384/4294967296
Cache-Control: no-cache

<binary DuckDB data>
```

**Security Notes:**

- Same security model as RFD 039 (allowlist, read-only, no directory traversal).
- Only serves files explicitly registered (e.g., `metrics.duckdb`).

### CLI Commands

**List available databases:**

```bash
coral duckdb list

# Output:
DATABASE      TYPE      LOCATION       RETENTION  TABLES  SIZE     BEYLA
colony        colony    10.0.1.1:9000  30d        12      4.2GB    yes
agent-prod-1  agent     10.0.2.10:9001 1h         3       120MB    yes
agent-prod-2  agent     10.0.2.11:9001 1h         3       115MB    yes
agent-dev-1   agent     10.0.2.20:9001 1h         0       0B       no
```

**Query colony database (one-shot):**

```bash
coral duckdb query --colony "SELECT * FROM beyla_http_metrics WHERE timestamp > now() - INTERVAL '7 days' LIMIT 10"

# Output (table format):
timestamp           agent_id      service_name  http_method  http_route  http_status_code  latency_ms  count
2025-11-13 10:25   agent-prod-1  api-server    POST         /checkout   200               45.2        1547
2025-11-13 10:25   agent-prod-1  api-server    GET          /products   200               12.5        3421
...

(10 rows)
```

**Query colony database (CSV export):**

```bash
coral duckdb query --colony \
  "SELECT service_name, AVG(latency_ms) as avg_latency, SUM(count) as total_requests \
   FROM beyla_http_metrics \
   WHERE timestamp > now() - INTERVAL '30 days' \
   GROUP BY service_name" \
  --format csv > monthly_report.csv
```

**Interactive shell (colony):**

```bash
coral duckdb shell --colony

# Output:
DuckDB interactive shell. Type '.exit' to quit, '.help' for help.

Attached colony database: colony

duckdb> .tables
beyla_http_metrics
beyla_grpc_metrics
beyla_sql_metrics
telemetry_summaries
...

duckdb> SELECT service_name, COUNT(DISTINCT agent_id) as agent_count, SUM(count) as total_requests
    ..> FROM beyla_http_metrics
    ..> WHERE timestamp > now() - INTERVAL '7 days'
    ..> GROUP BY service_name
    ..> ORDER BY total_requests DESC;

service_name     agent_count  total_requests
api-server       4            12547891
auth-service     2            3421087
payment-gateway  2            892341
(3 rows in 145ms)

duckdb> .exit
```

**Query both colony and agent (multi-database):**

```bash
coral duckdb shell --colony --agents agent-prod-1

# Output:
DuckDB interactive shell. Type '.exit' to quit, '.help' for help.

Attached databases: colony, agent_agent_prod_1

duckdb> -- Compare real-time agent data (last hour) with colony historical data
    ..> SELECT 'agent' as source, COUNT(*) as request_count
    ..> FROM agent_agent_prod_1.beyla_http_metrics_local
    ..> UNION ALL
    ..> SELECT 'colony' as source, COUNT(*) as request_count
    ..> FROM colony.beyla_http_metrics
    ..> WHERE agent_id = 'agent-prod-1';

source   request_count
agent    15478          -- Real-time data (last hour)
colony   345782         -- Historical data (30 days)
(2 rows in 89ms)
```

### Configuration Changes

**Colony database must use file path:**

```yaml
# Colony config (~/.coral/colony/config.yaml)
colony:
  database:
    # REQUIRED: Must be file path (not :memory:) for HTTP serving
    path: ~/.coral/colony/metrics.duckdb

    # Optional: Retention periods (from RFD 032)
    retention:
      http_days: 30
      grpc_days: 30
      sql_days: 14
```

**CLI config (existing):**

```yaml
# ~/.coral/config.yaml
colony:
  url: http://localhost:9000  # Used for colony database queries
```

## Testing Strategy

### Unit Tests

**Colony DuckDB Handler:**

- Reuse tests from RFD 039 (moved to shared package).
- Verify colony-specific database registration.

**CLI Target Resolution:**

- `TestResolveColonyAddress_Success`: Verify colony URL from config.
- `TestResolveColonyAddress_NoConfig`: Verify error when config missing.
- `TestQueryTarget_Colony`: Verify `--colony` flag routing.
- `TestQueryTarget_Agent`: Verify agent ID routing.

### Integration Tests

**Colony HTTP Endpoint:**

- Start colony with file-based DuckDB.
- Attach via DuckDB HTTP from test client.
- Execute query: `SELECT COUNT(*) FROM beyla_http_metrics`.
- Verify results returned correctly.

**CLI End-to-End:**

- Start colony with test data.
- Run `coral duckdb query --colony "SELECT * FROM beyla_http_metrics LIMIT 5"`.
- Verify query results match expected data.
- Run `coral duckdb shell --colony` and execute `.tables`.
- Verify tables listed correctly.

### E2E Tests

**Historical Query Workflow:**

1. Start colony with 7 days of synthetic historical data.
2. Start 2 agents with 1 hour of real-time data.
3. Run `coral duckdb list` - verify colony + agents shown.
4. Run `coral duckdb query --colony` - query historical data.
5. Run `coral duckdb query agent-1` - query real-time data.
6. Verify data from both sources accessible.

**Multi-database Query:**

1. Start colony and agent.
2. Run `coral duckdb shell --colony --agents agent-1`.
3. Execute UNION query joining colony and agent tables.
4. Verify query results combine both data sources.

## Security Considerations

**Authentication/Authorization:**

- Same model as RFD 039: WireGuard mesh membership controls access.
- Colony database accessible to any mesh member (operators, CLI tools).
- No additional authentication layer for HTTP endpoint.

**Data Exposure:**

- Entire colony database accessible to mesh members (no row-level security).
- Acceptable because:
    - Mesh is trusted network
    - Colony data is aggregated metrics, not raw PII
    - Operators need unrestricted access for analysis
- Future enhancement: Token-based authentication if needed (same as RFD 039
  future work).

**Read-Only Guarantees:**

- DuckDB HTTP attach is read-only by design.
- Colony HTTP handler rejects non-GET methods.
- Database file permissions unchanged (colony process can still write).

**Denial of Service:**

- Large queries can consume colony CPU/memory (e.g., full table scans).
- Mitigation: Mesh limits access to trusted operators.
- Colony database typically pre-aggregated (faster queries than raw data).
- Future enhancement: Query timeout or resource limits if needed.

## Future Enhancements

**Query Result Caching:**

- Cache common query results on colony (e.g., daily aggregations).
- Serve cached results via HTTP to reduce repeated computation.
- TTL-based cache invalidation.

**Federated Queries:**

- `coral duckdb query --all "SELECT ..."` - Query colony + all agents in single
  command.
- CLI fans out query, merges results, performs final aggregation.
- Useful for real-time fleet-wide analysis.

**Query Templates:**

- Predefined query templates for common analysis patterns.
- `coral duckdb templates list` - Show available templates.
- `coral duckdb templates run slo-report --days 7` - Execute template with
  parameters.

**Export to Data Warehouses:**

- `coral duckdb export --colony --format parquet --output s3://bucket/metrics/`.
- Stream colony database to external systems (S3, BigQuery, Snowflake).
- Useful for integration with existing BI tools.

## Appendix

### Colony Database Schema

**Tables in colony DuckDB (from RFD 032):**

```sql
-- Aggregated HTTP metrics from all agents
CREATE TABLE beyla_http_metrics (
    timestamp        TIMESTAMP NOT NULL,
    agent_id         VARCHAR NOT NULL,
    service_name     VARCHAR NOT NULL,
    http_method      VARCHAR(10),
    http_route       VARCHAR(255),
    http_status_code SMALLINT,
    latency_ms       DOUBLE PRECISION NOT NULL,
    count            BIGINT NOT NULL,
    attributes       JSON,
    created_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Aggregated gRPC metrics from all agents
CREATE TABLE beyla_grpc_metrics (
    timestamp        TIMESTAMP NOT NULL,
    agent_id         VARCHAR NOT NULL,
    service_name     VARCHAR NOT NULL,
    grpc_method      VARCHAR(255),
    grpc_status_code SMALLINT,
    latency_ms       DOUBLE PRECISION NOT NULL,
    count            BIGINT NOT NULL,
    attributes       JSON,
    created_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Aggregated SQL metrics from all agents
CREATE TABLE beyla_sql_metrics (
    timestamp        TIMESTAMP NOT NULL,
    agent_id         VARCHAR NOT NULL,
    service_name     VARCHAR NOT NULL,
    sql_operation    VARCHAR(50),
    table_name       VARCHAR(255),
    latency_ms       DOUBLE PRECISION NOT NULL,
    count            BIGINT NOT NULL,
    attributes       JSON,
    created_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

### Example Queries

**Fleet-wide SLO report (7 days):**

```sql
SELECT
    service_name,
    COUNT(*) as total_requests,
    AVG(latency_ms) as avg_latency_ms,
    PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY latency_ms) as p99_latency_ms,
    SUM(CASE WHEN http_status_code >= 500 THEN 1 ELSE 0 END) as error_count,
    (SUM(CASE WHEN http_status_code >= 500 THEN 1 ELSE 0 END)::FLOAT / COUNT(*) * 100) as error_rate_pct
FROM beyla_http_metrics
WHERE timestamp > now() - INTERVAL '7 days'
GROUP BY service_name
ORDER BY total_requests DESC;
```

**Service dependency graph (gRPC calls):**

```sql
SELECT
    service_name as source_service,
    grpc_method as target_method,
    COUNT(*) as call_count,
    AVG(latency_ms) as avg_latency_ms
FROM beyla_grpc_metrics
WHERE timestamp > now() - INTERVAL '24 hours'
GROUP BY service_name, grpc_method
ORDER BY call_count DESC;
```

**Latency trend analysis (30 days, daily aggregation):**

```sql
SELECT
    DATE_TRUNC('day', timestamp) as day,
    service_name,
    AVG(latency_ms) as avg_latency_ms,
    PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY latency_ms) as p99_latency_ms
FROM beyla_http_metrics
WHERE timestamp > now() - INTERVAL '30 days'
GROUP BY DATE_TRUNC('day', timestamp), service_name
ORDER BY day DESC, service_name;
```

**Database query hotspots (14 days):**

```sql
SELECT
    table_name,
    sql_operation,
    COUNT(*) as query_count,
    AVG(latency_ms) as avg_latency_ms,
    MAX(latency_ms) as max_latency_ms
FROM beyla_sql_metrics
WHERE timestamp > now() - INTERVAL '14 days'
GROUP BY table_name, sql_operation
HAVING COUNT(*) > 100
ORDER BY avg_latency_ms DESC
LIMIT 20;
```

### Comparison with RFD 039

| Feature               | RFD 039 (Agents)      | RFD 046 (Colony)        |
|-----------------------|-----------------------|-------------------------|
| **Data Retention**    | ~1 hour               | 30 days (HTTP/gRPC)     |
| **Data Scope**        | Single agent          | All agents (aggregated) |
| **HTTP Endpoint**     | `agent:9001/duckdb/*` | `colony:9000/duckdb/*`  |
| **CLI Target**        | `agent-id`            | `--colony`              |
| **Use Case**          | Real-time debugging   | Historical analysis     |
| **Tables**            | `*_local` (agent)     | `beyla_*` (all agents)  |
| **Query Performance** | Fast (small data)     | Slower (large data)     |
| **Agent Tracking**    | N/A                   | `agent_id` column       |
| **Typical Query**     | Last 5 minutes        | Last 7-30 days          |
| **Data Freshness**    | Real-time             | Delayed (poll interval) |
