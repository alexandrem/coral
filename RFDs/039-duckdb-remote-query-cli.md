---
rfd: "039"
title: "DuckDB Remote Query CLI for Agent Metrics"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "010", "032", "038" ]
database_migrations: [ ]
areas: [ "cli", "observability", "metrics", "duckdb" ]
---

# RFD 039 - DuckDB Remote Query CLI for Agent Metrics

**Status:** 🚧 Draft

**Date:** 2025-11-16

## Summary

Add a `coral duckdb` CLI command that enables interactive SQL querying of agent
metrics by leveraging DuckDB's native HTTP remote attach capability. This
provides operators with direct, ad-hoc access to raw Beyla metrics stored on
agents without requiring API abstraction layers or custom query endpoints.

**Note:** This RFD depends on RFD 038 (CLI-to-Agent Direct Mesh Connectivity)
for establishing direct WireGuard connections from CLI tools to agents.

## Problem

**Current behavior/limitations**

- Colony periodically polls agents for Beyla metrics via `QueryBeylaMetrics`
  RPC (RFD 032), storing aggregated results in colony DuckDB with configurable
  retention (30 days HTTP/gRPC, 14 days SQL).
- Agents retain raw metrics in local DuckDB for ~1 hour before cleanup.
- Operators cannot directly query agent-local metrics for debugging,
  troubleshooting, or exploratory analysis.
- Custom queries require either:
    - Waiting for colony polling cycle to aggregate data
    - Adding new RPC endpoints to the agent API
    - SSH-ing into agent hosts and manually querying DuckDB files
- The gRPC API abstraction adds serialization overhead (DuckDB → protobuf →
  network → protobuf → DuckDB) and requires API versioning for schema changes.

**Why this matters**

- **Incident response**: During outages, operators need immediate access to raw
  agent metrics without waiting for colony aggregation cycles (which may run
  hourly).
- **Debugging**: Exploratory queries like "show me all SQL queries to the users
  table in the last 5 minutes" cannot be expressed through predefined RPC
  endpoints.
- **Efficiency**: For large historical queries or bulk exports, DuckDB-to-DuckDB
  transfer using native formats (Parquet, Arrow) is significantly faster than
  protobuf serialization.
- **Flexibility**: SQL is a universal query interface that doesn't require API
  changes to support new query patterns.

**Use cases affected**

- Ops engineer investigating latency spike: "Show me all HTTP requests with
  p99 > 1s in the last 10 minutes, grouped by endpoint and status code."
- SRE exporting metrics for offline analysis: "Dump all gRPC metrics for service
  X as CSV for the past hour."
- Developer debugging database performance: "Show me all SQL queries with
  latency > 100ms, grouped by table and operation."

## Solution

Expose agent DuckDB files over HTTP and provide a CLI tool that uses DuckDB's
built-in `ATTACH` statement to query remote databases. This leverages DuckDB's
native read-only HTTP attach feature (available via the `httpfs` extension) to
enable direct SQL access without custom query infrastructure.

**Key Design Decisions:**

- **Use DuckDB's native HTTP attach** rather than building a custom query API,
  PostgreSQL wire protocol proxy, or streaming RPC. This minimizes code,
  leverages battle-tested DuckDB networking, and provides read-only safety by
  default.
- **Keep existing API for programmatic access**: The `QueryBeylaMetrics` RPC
  remains the primary interface for colony polling and MCP clients. The DuckDB
  CLI is supplementary for operator ad-hoc queries.
- **Serve entire database file** over HTTP rather than query-level endpoints.
  DuckDB handles range requests efficiently, only fetching needed data pages.
- **No authentication beyond WireGuard mesh**: Access is controlled by mesh
  membership. Any colony/operator on the mesh can query any agent.

**Benefits:**

- **Zero serialization overhead**: DuckDB transfers data in native binary format
  using HTTP range requests.
- **Universal SQL interface**: Operators can use full DuckDB SQL dialect without
  API limitations.
- **No API versioning burden**: Schema changes to agent tables don't break CLI
  queries (callers adapt SQL).
- **Minimal implementation**: ~200 lines of code (HTTP handler + CLI wrapper
  around DuckDB Go driver).
- **Read-only by default**: DuckDB's `ATTACH` over HTTP is inherently read-only,
  preventing accidental writes.

**Architecture Overview:**

```
┌─────────────┐                    ┌──────────────┐                    ┌─────────────┐
│  coral CLI  │                    │    Colony    │                    │    Agent    │
│             │                    │              │                    │             │
│ duckdb cmd  │─────(1)────────────│  Discovery   │                    │  HTTP:9001  │
│             │  Get agent info    │  / Registry  │                    │             │
│             │────────────────────│              │                    │             │
│             │       (2)          │              │                    │             │
│             │  Agent mesh IP     │              │                    │             │
│             │                    └──────────────┘                    │             │
│             │                                                        │             │
│             │─────(3)───────────────────────────────────────────────│  HTTPS      │
│             │  ATTACH 'https://agent:9001/duckdb/metrics.duckdb'    │  /duckdb/*  │
│             │  AS agent_123 (READ_ONLY);                            │             │
│             │                                                        │             │
│  DuckDB Go  │◄────(4)────────────────────────────────────────────────│  Serve DB   │
│  Interactive│  Read-only SQL queries (HTTP range requests)          │  read-only  │
│  Shell      │                                                        │             │
└─────────────┘                                                        └─────────────┘
```

### Component Changes

1. **Agent (HTTP handler)**:
    - Add `/duckdb/<filename>` HTTP endpoint that serves agent DuckDB files.
    - Handler validates read-only access, checks file existence, serves via
      `http.ServeFile`.
    - Integrated into existing agent HTTP server (port 9001) alongside gRPC
      handlers.
    - Only serves `beyla.duckdb` if Beyla is enabled.

2. **CLI (new `duckdb` command)**:
    - `coral duckdb shell <agent-id>`: Opens interactive DuckDB shell attached
      to agent.
    - `coral duckdb query <agent-id> <sql>`: Executes one-shot query and prints
      results.
    - `coral duckdb list-agents`: Shows agents with Beyla metrics enabled.
    - CLI resolves agent IDs to WireGuard mesh IPs via colony registry.
    - Uses DuckDB Go driver with `httpfs` extension to attach remote databases.

3. **Dependencies**:
    - Add `github.com/marcboeker/go-duckdb` (DuckDB Go driver) to CLI binary.
    - Add `github.com/chzyer/readline` for interactive shell support.

**Configuration Example:**

No new configuration required. Feature uses existing agent HTTP server and
DuckDB storage. Operators must have:

- Access to WireGuard mesh (existing requirement for `coral` CLI).
- Agent ID or ability to query colony registry for agent list.

```bash
# Interactive shell
coral duckdb shell agent-prod-1

# One-shot query
coral duckdb query agent-prod-1 "SELECT * FROM beyla_http_metrics_local LIMIT 10"

# Query across multiple agents (multi-attach)
coral duckdb shell --agents agent-1,agent-2,agent-3
```

## Implementation Plan

### Phase 1: Agent HTTP Endpoint

- [ ] Create `internal/agent/duckdb_handler.go` with HTTP handler.
- [ ] Add `/duckdb/` route to agent HTTP server in
  `internal/cli/agent/start.go`.
- [ ] Validate handler only serves DuckDB files (no directory traversal).
- [ ] Ensure handler respects read-only semantics (GET only, no
  POST/PUT/DELETE).

### Phase 2: CLI DuckDB Command

- [ ] Create `internal/cli/duckdb/` package structure.
- [ ] Implement `shell` subcommand with DuckDB Go driver integration.
- [ ] Implement `query` subcommand for one-shot queries.
- [ ] Implement `list-agents` subcommand using colony registry.
- [ ] Add agent address resolution via colony discovery/registry API.

### Phase 3: Interactive Shell Features

- [ ] Add readline support for command history and editing.
- [ ] Implement meta-commands (`.tables`, `.databases`, `.exit`, `.help`).
- [ ] Add multi-agent attach support (`--agents` flag).
- [ ] Support output formats (table, CSV, JSON) for `query` command.

### Phase 4: Testing & Documentation

- [ ] Add unit tests for DuckDB HTTP handler (read-only, 404, method
  validation).
- [ ] Add integration test: start agent, attach via DuckDB, query metrics.
- [ ] Add E2E test: full CLI workflow with real agent.
- [ ] Update CLI documentation with usage examples.
- [ ] Add troubleshooting guide for common issues (mesh connectivity, agent
  down, no metrics).

## API Changes

### New HTTP Endpoint (Agent)

**Path:** `/duckdb/<filename>`

**Method:** `GET`

**Description:** Serves agent DuckDB files for read-only remote attach.

**Authentication:** WireGuard mesh membership (implicit via network access
control).

**Request:**

```http
GET /duckdb/metrics.duckdb HTTP/1.1
Host: agent-123.coral.mesh:9001
Range: bytes=0-16384
```

**Response (success):**

```http
HTTP/1.1 206 Partial Content
Content-Type: application/octet-stream
Content-Range: bytes 0-16384/1048576
Cache-Control: no-cache

<binary DuckDB data>
```

**Response (not found):**

```http
HTTP/1.1 404 Not Found
Content-Type: text/plain

database not found
```

**Response (method not allowed):**

```http
HTTP/1.1 405 Method Not Allowed
Content-Type: text/plain

method not allowed
```

**Security Notes:**

- Only serves files explicitly allowlisted (e.g., `beyla.duckdb`).
- No directory traversal allowed (e.g., `../../../etc/passwd` returns 404).
- Read-only: POST/PUT/DELETE return 405.
- DuckDB files are read-only when served via HTTP (DuckDB limitation).

### CLI Commands

**List available agents:**

```bash
coral duckdb list-agents

# Output:
AGENT ID        STATUS    LAST SEEN           BEYLA ENABLED
agent-prod-1    healthy   2025-11-16 10:30    yes
agent-prod-2    healthy   2025-11-16 10:29    yes
agent-dev-1     degraded  2025-11-16 09:15    yes
```

**Interactive shell (single agent):**

```bash
coral duckdb shell agent-prod-1

# Output:
DuckDB interactive shell. Type '.exit' to quit, '.help' for help.

Attached agent database: agent_agent_prod_1

duckdb> .tables
beyla_http_metrics_local
beyla_grpc_metrics_local
beyla_sql_metrics_local

duckdb> SELECT service_name, COUNT(*) as req_count
        FROM beyla_http_metrics_local
        WHERE timestamp > now() - INTERVAL '5 minutes'
        GROUP BY service_name;

service_name    req_count
api-server      1547
auth-service    892
(2 rows)

duckdb> .exit
```

**Interactive shell (multiple agents):**

```bash
coral duckdb shell --agents agent-prod-1,agent-prod-2

# Output:
DuckDB interactive shell. Type '.exit' to quit, '.help' for help.

Attached databases: agent_agent_prod_1, agent_agent_prod_2

duckdb> SELECT service_name, SUM(count) as total_requests
        FROM (
          SELECT * FROM agent_agent_prod_1.beyla_http_metrics_local
          UNION ALL
          SELECT * FROM agent_agent_prod_2.beyla_http_metrics_local
        )
        WHERE timestamp > now() - INTERVAL '10 minutes'
        GROUP BY service_name;

service_name    total_requests
api-server      3421
auth-service    1987
(2 rows)
```

**One-shot query (table output):**

```bash
coral duckdb query agent-prod-1 "SELECT * FROM beyla_http_metrics_local WHERE http_status_code >= 500 LIMIT 5"

# Output:
timestamp               service_name    http_method  http_route      http_status_code  latency_bucket_ms  count
2025-11-16 10:25:14    api-server      POST         /api/checkout   500               250.0              3
2025-11-16 10:26:42    api-server      GET          /api/products   503               100.0              1
...

(5 rows)
```

**One-shot query (CSV output):**

```bash
coral duckdb query agent-prod-1 \
  "SELECT service_name, http_route, COUNT(*) as error_count FROM beyla_http_metrics_local WHERE http_status_code >= 500 GROUP BY service_name, http_route" \
  --format csv > errors.csv

# Output (errors.csv):
service_name,http_route,error_count
api-server,/api/checkout,15
auth-service,/auth/login,3
```

**Meta-commands (in shell):**

- `.tables` - List all tables in attached databases
- `.databases` - Show attached databases
- `.help` - Show help message
- `.exit` or `.quit` - Exit shell

### Configuration Changes

None. Feature is enabled automatically if:

- Agent has Beyla enabled (RFD 032)
- Agent HTTP server is running (always true)
- Operator has mesh access (existing security model)

## Testing Strategy

### Unit Tests

**Agent DuckDB Handler:**

- `TestDuckDBHandler_ServeFile_Success`: Verify file served with correct
  headers.
- `TestDuckDBHandler_NotFound`: Verify 404 when file doesn't exist.
- `TestDuckDBHandler_MethodNotAllowed`: Verify POST/PUT/DELETE return 405.
- `TestDuckDBHandler_NoDirectoryTraversal`: Verify `../../../etc/passwd` returns
  404.
- `TestDuckDBHandler_ReadOnlyValidation`: Verify only GET requests allowed.

**CLI Agent Resolver:**

- `TestResolveAgentAddress_Success`: Verify agent ID resolves to mesh IP.
- `TestResolveAgentAddress_NotFound`: Verify error when agent doesn't exist.
- `TestResolveAgentAddress_MultipleAgents`: Verify multiple IDs resolve
  correctly.

### Integration Tests

**Agent to CLI:**

- Start agent with Beyla enabled and metrics populated.
- Run
  `coral duckdb query agent-test "SELECT COUNT(*) FROM beyla_http_metrics_local"`.
- Verify query returns expected row count.
- Verify CLI exits cleanly.

**Multi-agent attach:**

- Start two agents with different metrics.
- Run `coral duckdb shell --agents agent-1,agent-2`.
- Execute UNION query across both databases.
- Verify results aggregate correctly.

### E2E Tests

**Full workflow:**

1. Deploy agent with Beyla monitoring a test service.
2. Generate HTTP traffic to populate metrics.
3. Run `coral duckdb list-agents`, verify agent appears.
4. Run `coral duckdb query` to fetch metrics.
5. Verify metrics match expected traffic patterns.

**Error handling:**

- Agent down: Verify CLI reports connection error.
- Beyla disabled: Verify CLI reports "database not found".
- Invalid SQL: Verify DuckDB syntax error returned.

## Security Considerations

**Authentication/Authorization:**

- Access control via WireGuard mesh membership (existing model).
- No additional authentication layer for HTTP endpoint (mesh is trusted
  network).
- Agents must validate requests come from mesh IPs (WireGuard implicit).

**Data Exposure:**

- Entire DuckDB file is accessible to any mesh member (no row-level security).
- Acceptable because: (1) mesh is trusted network, (2) metrics are not PII, (3)
  operators need unrestricted access for debugging.
- Future enhancement: Add token-based authentication for HTTP endpoint if
  needed.

**Read-Only Guarantees:**

- DuckDB's HTTP attach is read-only by design (cannot write over HTTP).
- Agent HTTP handler rejects non-GET methods (defense in depth).
- DuckDB file permissions remain unchanged (agent process can still write).

**Denial of Service:**

- Malicious queries can consume agent CPU/memory (e.g.,
  `SELECT * FROM huge_table`).
- Mitigation: WireGuard mesh limits access to trusted operators.
- Future enhancement: Add query timeout or resource limits if needed.

**Audit Logging:**

- No logging of SQL queries in initial implementation.
- Future enhancement: Log queries to agent logs for auditing.

## Future Enhancements

**Query Templates and Saved Queries:**

- `coral duckdb templates list` - Show common query templates.
- `coral duckdb templates run error-analysis` - Execute predefined query.
- Store templates in `~/.coral/duckdb/templates/`.

**Streaming Export:**

- `coral duckdb export agent-123 --format parquet --output metrics.parquet`.
- Agent exports query results to Parquet, CLI downloads efficiently.
- Useful for large bulk exports (GB-scale data).

**Query Result Visualization:**

- Integrate with CLI charting libraries (e.g., `termui`, `asciigraph`).
- `coral duckdb query agent-123 "SELECT ..." --chart bar`.

**Token-Based Authentication:**

- Add optional `X-Coral-Token` header for HTTP endpoint.
- Generate tokens via `coral token create --agent agent-123`.
- Useful for zero-trust environments where mesh alone insufficient.

**Query Federation (Colony-Side):**

- `coral duckdb query --all "SELECT ..."` - Query across all agents.
- Colony executes query on each agent, merges results.
- Useful for fleet-wide analysis.

## Appendix

### DuckDB HTTP Attach Protocol

DuckDB's `httpfs` extension uses HTTP range requests to fetch database pages on
demand:

1. Client executes
   `ATTACH 'https://example.com/db.duckdb' AS remote (READ_ONLY);`
2. DuckDB sends `GET /db.duckdb` with `Range: bytes=0-16384` to read header.
3. Server responds with `206 Partial Content` and requested bytes.
4. DuckDB parses metadata, determines which pages contain requested data.
5. For each query, DuckDB sends additional `Range` requests for needed pages.
6. Results assembled client-side, no server-side query execution.

**Key properties:**

- Server is stateless (standard HTTP file serving).
- Only required data pages fetched (efficient for selective queries).
- Read-only (HTTP GET has no write semantics).
- Works with standard web servers (nginx, Apache, Go `http.ServeFile`).

### Reference Implementations

**DuckDB over HTTP:**

- Official
  docs: https://duckdb.org/docs/stable/guides/network_cloud_storage/duckdb_over_https_or_s3
- Example:
  `ATTACH 'https://blobs.duckdb.org/databases/stations.duckdb' AS stations;`

**PostgreSQL Wire Protocol Proxy (alternative approach):**

- https://github.com/ybrs/pgduckdb - Exposes DuckDB via Postgres protocol.
- Not chosen because: (1) extra process to manage, (2) protocol translation
  overhead, (3) HTTP attach simpler.

### Example Queries

**HTTP requests by status code:**

```sql
SELECT http_status_code,
       COUNT(*)               as request_count,
       AVG(latency_bucket_ms) as avg_latency_ms
FROM beyla_http_metrics_local
WHERE timestamp > now() - INTERVAL '1 hour'
GROUP BY http_status_code
ORDER BY request_count DESC;
```

**Top 10 slowest endpoints:**

```sql
SELECT http_route,
       MAX(latency_bucket_ms) as max_latency_ms,
       COUNT(*)               as request_count
FROM beyla_http_metrics_local
WHERE timestamp > now() - INTERVAL '30 minutes'
GROUP BY http_route
ORDER BY max_latency_ms DESC
    LIMIT 10;
```

**gRPC error rate by method:**

```sql
SELECT grpc_method,
       COUNT(*)                                                                   FILTER (WHERE grpc_status_code != 0) as error_count, COUNT(*) as total_count,
       (COUNT(*) FILTER (WHERE grpc_status_code != 0)::FLOAT / COUNT(*) * 100) as error_rate_pct
FROM beyla_grpc_metrics_local
WHERE timestamp > now() - INTERVAL '15 minutes'
GROUP BY grpc_method
ORDER BY error_rate_pct DESC;
```

**SQL queries by table and operation:**

```sql
SELECT table_name,
       sql_operation,
       COUNT(*)               as query_count,
       AVG(latency_bucket_ms) as avg_latency_ms
FROM beyla_sql_metrics_local
WHERE timestamp > now() - INTERVAL '10 minutes'
GROUP BY table_name, sql_operation
ORDER BY avg_latency_ms DESC;
```
