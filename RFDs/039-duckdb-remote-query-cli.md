---
rfd: "039"
title: "DuckDB Remote Query CLI for Agent and Colony Metrics"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "010", "032", "038" ]
database_migrations: [ ]
areas: [ "cli", "observability", "metrics", "duckdb" ]
---

# RFD 039 - DuckDB Remote Query CLI for Agent and Colony Metrics

**Status:** 🚧 Draft

**Date:** 2025-11-16

## Summary

Add a `coral duckdb` CLI command that enables interactive SQL querying of both
agent metrics and colony aggregated data by leveraging DuckDB's native HTTP
remote attach capability. This provides operators with direct, ad-hoc access to
raw Beyla metrics stored on agents and historical aggregated metrics stored in
colony without requiring API abstraction layers or custom query endpoints.

**Note:** This RFD depends on RFD 038 (CLI-to-Agent Direct Mesh Connectivity)
for establishing direct WireGuard connections from CLI tools to agents.

## Problem

**Current behavior/limitations**

- Colony periodically polls agents for Beyla metrics via `QueryBeylaMetrics`
  RPC (RFD 032), storing aggregated results in colony DuckDB with configurable
  retention (30 days HTTP/gRPC, 14 days SQL).
- Agents retain raw metrics in local DuckDB for ~1 hour before cleanup.
- Operators cannot directly query agent-local metrics or colony aggregated data
  for debugging, troubleshooting, or exploratory analysis.
- Colony holds an exclusive lock on its DuckDB database, preventing external
  read-only connections using standard DuckDB clients (e.g.,
  `duckdb colony.duckdb?access_mode=READ_ONLY` fails with "Resource temporarily
  unavailable").
- Custom queries require either:
    - Waiting for colony polling cycle to aggregate data
    - Adding new RPC endpoints to the agent/colony API
    - SSH-ing into agent/colony hosts and manually querying DuckDB files
    - Stopping the colony process to release the database lock (unacceptable)
- The gRPC API abstraction adds serialization overhead (DuckDB → protobuf →
  network → protobuf → DuckDB) and requires API versioning for schema changes.

**Why this matters**

- **Incident response**: During outages, operators need immediate access to raw
  agent metrics without waiting for colony aggregation cycles (which may run
  hourly), as well as access to colony's historical aggregated data for
  trend analysis.
- **Debugging**: Exploratory queries like "show me all SQL queries to the users
  table in the last 5 minutes" (agent) or "show me service baseline deviations
  over the past week" (colony) cannot be expressed through predefined RPC
  endpoints.
- **Efficiency**: For large historical queries or bulk exports, DuckDB-to-DuckDB
  transfer using native formats (Parquet, Arrow) is significantly faster than
  protobuf serialization.
- **Flexibility**: SQL is a universal query interface that doesn't require API
  changes to support new query patterns.
- **Concurrent access**: DuckDB's exclusive lock prevents external read-only
  connections at the file level. HTTP file serving bypasses this limitation by
  serving raw file bytes without requiring a DuckDB connection, enabling
  read-only queries while colony/agent processes maintain write access.

**Use cases affected**

**Agent queries (real-time, recent data ~1 hour):**
- Ops engineer investigating latency spike: "Show me all HTTP requests with
  p99 > 1s in the last 10 minutes, grouped by endpoint and status code."
- SRE exporting metrics for offline analysis: "Dump all gRPC metrics for service
  X as CSV for the past hour."
- Developer debugging database performance: "Show me all SQL queries with
  latency > 100ms, grouped by table and operation."

**Colony queries (historical, aggregated data 14-30 days):**
- Platform engineer analyzing trends: "Show me p95 latency trends for all
  services over the past 7 days, grouped by day."
- SRE investigating baseline drift: "Which services have exceeded their learned
  baseline thresholds in the past week?"
- Data analyst exporting for reporting: "Export all service connection topology
  changes for the past month as CSV."

## Solution

Expose both agent and colony DuckDB files over HTTP and provide a CLI tool that
uses DuckDB's built-in `ATTACH` statement to query remote databases. This
leverages DuckDB's native read-only HTTP attach feature (available via the
`httpfs` extension) to enable direct SQL access without custom query
infrastructure.

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
- **Bypass DuckDB lock via HTTP file serving**: Use `http.ServeFile` to serve
  raw database file bytes without opening a DuckDB connection, enabling
  concurrent read access while colony/agent processes maintain exclusive write
  locks.
- **No authentication beyond WireGuard mesh**: Access is controlled by mesh
  membership. Any colony/operator on the mesh can query any agent or colony
  database.

**Benefits:**

- **Zero serialization overhead**: DuckDB transfers data in native binary format
  using HTTP range requests.
- **Universal SQL interface**: Operators can use full DuckDB SQL dialect without
  API limitations.
- **No API versioning burden**: Schema changes to agent/colony tables don't
  break CLI queries (callers adapt SQL).
- **Minimal implementation**: ~150 lines of code (HTTP handlers + thin CLI wrapper
  that launches `duckdb` CLI).
- **Full REPL features for free**: No need to implement readline, command
  history, auto-completion, or meta-commands - users get the native DuckDB REPL.
- **Read-only by default**: DuckDB's `ATTACH` over HTTP is inherently read-only,
  preventing accidental writes.
- **Solves concurrent access problem**: External clients can query
  agent/colony databases while they maintain exclusive locks for writes.
- **Zero Go dependencies**: No need for DuckDB Go driver or readline libraries -
  just `exec.Command` and standard library.

**Architecture Overview:**

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                                  coral CLI                                          │
│                                                                                     │
│  duckdb command                                                                     │
│  ├── shell <target>    (interactive DuckDB shell)                                  │
│  ├── query <target>    (one-shot query)                                            │
│  └── list-agents       (discover available databases)                              │
│                                                                                     │
│  DuckDB Go Interactive Shell (with httpfs extension)                               │
└─────────────────────────────────────────────────────────────────────────────────────┘
           │                                          │
           │ (1) Query colony for discovery          │
           ▼                                          │
┌──────────────────────────┐                         │
│         Colony           │                         │
│                          │                         │
│  ┌────────────────────┐  │                         │
│  │  Discovery API     │  │                         │
│  │  (agent registry)  │  │                         │
│  └────────────────────┘  │                         │
│                          │                         │
│  ┌────────────────────┐  │                         │
│  │  DuckDB Process    │  │                         │
│  │  (exclusive lock)  │  │                         │
│  │  colony.duckdb     │  │                         │
│  └────────┬───────────┘  │                         │
│           │              │                         │
│  ┌────────▼───────────┐  │                         │
│  │  HTTP Handler      │  │                         │
│  │  GET /duckdb/*     │  │                         │
│  │  (http.ServeFile)  │  │                         │
│  └────────┬───────────┘  │                         │
│           │              │                         │
│      Port 9001           │                         │
└───────────┼──────────────┘                         │
            │                                        │
            │ (2a) ATTACH colony database            │ (2b) ATTACH agent database
            │ http://colony:9001/duckdb/colony.duckdb│ http://agent:9001/duckdb/beyla.duckdb
            │                                        │
            ◄────────────────────────────────────────┼────────────────────┐
            │                                        │                    │
            │ (3) HTTP range requests for           │                    │
            │     database pages (read-only)         │                    │
            │                                        ▼                    │
            │                          ┌──────────────────────┐           │
            │                          │       Agent          │           │
            │                          │                      │           │
            │                          │  ┌────────────────┐  │           │
            │                          │  │ DuckDB Process │  │           │
            │                          │  │ (exclusive     │  │           │
            │                          │  │  lock)         │  │           │
            │                          │  │ beyla.duckdb   │  │           │
            │                          │  └───────┬────────┘  │           │
            │                          │          │           │           │
            │                          │  ┌───────▼────────┐  │           │
            │                          │  │ HTTP Handler   │  │           │
            │                          │  │ GET /duckdb/*  │  │           │
            │                          │  │(http.ServeFile)│  │           │
            │                          │  └───────┬────────┘  │           │
            │                          │          │           │           │
            │                          │     Port 9001        │           │
            │                          └──────────┼───────────┘           │
            │                                     │                       │
            └─────────────────────────────────────┴───────────────────────┘
```

**Flow:**
1. CLI queries colony discovery API to get agent/colony mesh addresses
2. CLI attaches to colony database (historical data) and/or agent database(s) (recent data)
3. DuckDB client sends HTTP range requests to fetch database pages
4. Colony/agent HTTP handlers serve file bytes using `http.ServeFile` (no DuckDB connection needed)
5. CLI executes SQL queries against attached databases

### Component Changes

1. **Agent (HTTP handler)**:
    - Add `/duckdb/<filename>` HTTP endpoint that serves agent DuckDB files.
    - Handler validates read-only access, checks file existence, serves via
      `http.ServeFile`.
    - Integrated into existing agent HTTP server (port 9001) alongside gRPC
      handlers.
    - Only serves `beyla.duckdb` if Beyla is enabled.

2. **Colony (HTTP handler)**:
    - Add `/duckdb/<filename>` HTTP endpoint that serves colony DuckDB file.
    - Handler validates read-only access, checks file existence, serves via
      `http.ServeFile`.
    - Integrated into existing colony HTTP server (port 9001) alongside gRPC
      handlers.
    - Serves `<colony-id>.duckdb` (e.g., `alex-dev-0977e1.duckdb`).
    - Critical: Uses `http.ServeFile` to bypass DuckDB's exclusive lock by
      serving raw file bytes without opening a DuckDB connection.

3. **CLI (new `duckdb` command)**:
    - `coral duckdb shell <target>`: Launches the standard `duckdb` CLI with
      pre-attached remote databases (target can be `agent-id`, `colony`, or
      `colony-id`).
    - `coral duckdb query <target> <sql>`: Executes one-shot query using
      `duckdb` CLI and prints results.
    - `coral duckdb list-agents`: Shows agents with Beyla metrics enabled.
    - `coral duckdb list-colonies`: Shows available colony databases.
    - CLI resolves agent/colony IDs to WireGuard mesh IPs via colony registry.
    - **Implementation approach**: Thin wrapper that constructs ATTACH URLs and
      launches the native `duckdb` CLI binary (not a custom REPL).
    - Benefits: Full DuckDB REPL features (command history, auto-completion,
      syntax highlighting, meta-commands) for free.

4. **Dependencies**:
    - Require `duckdb` CLI binary in PATH (users install via package manager or
      download from duckdb.org).
    - No additional Go dependencies for REPL (standard library only).

**Configuration Example:**

No new configuration required. Feature uses existing agent/colony HTTP servers
and DuckDB storage. Operators must have:

- Access to WireGuard mesh (existing requirement for `coral` CLI).
- Agent/colony ID or ability to query colony registry.
- `duckdb` CLI binary in PATH (install via `brew install duckdb`, `apt install duckdb`, or download from duckdb.org).

```bash
# Interactive shell - agent (recent metrics, ~1 hour)
# Internally runs: duckdb -cmd "ATTACH 'http://10.42.0.5:9001/duckdb/beyla.duckdb' AS agent_prod_1 (READ_ONLY);"
coral duckdb shell agent-prod-1

# Interactive shell - colony (historical metrics, 14-30 days)
# Internally runs: duckdb -cmd "ATTACH 'http://10.42.0.1:9001/duckdb/alex-dev-0977e1.duckdb' AS colony (READ_ONLY);"
coral duckdb shell colony

# One-shot query - agent
# Internally runs: duckdb -cmd "ATTACH ...; SELECT * FROM ..."
coral duckdb query agent-prod-1 "SELECT * FROM beyla_http_metrics_local LIMIT 10"

# One-shot query - colony
coral duckdb query colony "SELECT * FROM metric_summaries WHERE timestamp > now() - INTERVAL '7 days'"

# Query across multiple agents (multi-attach)
# Internally runs: duckdb -cmd "ATTACH ... AS agent_1; ATTACH ... AS agent_2; ATTACH ... AS agent_3;"
coral duckdb shell --agents agent-1,agent-2,agent-3

# Query both colony and agent data (federation)
# Internally runs: duckdb -cmd "ATTACH ... AS colony; ATTACH ... AS agent_1;"
coral duckdb shell --colony --agents agent-1

# Advanced: Use duckdb CLI directly (if you know the mesh IP)
duckdb -cmd "ATTACH 'http://10.42.0.5:9001/duckdb/beyla.duckdb' AS agent (READ_ONLY);"
```

## Implementation Plan

### Phase 1: Agent and Colony HTTP Endpoints

**Agent:**
- [ ] Create `internal/agent/duckdb_handler.go` with HTTP handler.
- [ ] Add `/duckdb/` route to agent HTTP server in
  `internal/cli/agent/start.go`.
- [ ] Validate handler only serves DuckDB files (no directory traversal).
- [ ] Ensure handler respects read-only semantics (GET only, no
  POST/PUT/DELETE).

**Colony:**
- [ ] Create `internal/colony/duckdb_handler.go` with HTTP handler.
- [ ] Add `/duckdb/` route to colony HTTP server in
  `internal/cli/colony/start.go`.
- [ ] Validate handler only serves colony DuckDB file (no directory traversal).
- [ ] Ensure handler respects read-only semantics (GET only, no
  POST/PUT/DELETE).
- [ ] Verify `http.ServeFile` works correctly while colony has exclusive
  DuckDB lock.

### Phase 2: CLI DuckDB Command (Thin Wrapper)

- [ ] Create `internal/cli/duckdb/` package structure.
- [ ] Implement `shell` subcommand:
  - Resolve target ID(s) to mesh IPs.
  - Generate ATTACH statement(s).
  - Execute `duckdb -cmd "ATTACH ..."` using `exec.Command`.
  - Pass through stdin/stdout/stderr for full REPL experience.
- [ ] Implement `query` subcommand:
  - Resolve target ID to mesh IP.
  - Execute `duckdb -cmd "ATTACH ...; <user_query>"` using `exec.Command`.
  - Capture and print output.
- [ ] Implement `list-agents` subcommand using colony registry.
- [ ] Implement `list-colonies` subcommand.
- [ ] Add agent/colony address resolution via colony discovery/registry API.
- [ ] Support target disambiguation (agent vs colony).
- [ ] Add `--check-duckdb` flag to verify `duckdb` binary is in PATH.

### Phase 3: Multi-Attach and Advanced Features

- [ ] Add multi-agent attach support (`--agents` flag).
  - Generate multiple ATTACH statements.
- [ ] Add colony attach support (`--colony` flag).
- [ ] Add combined attach support (`--colony --agents agent-1,agent-2`).
  - Generate ATTACH statements for colony + agents.
- [ ] Support output format passthrough (users can use DuckDB's native `.mode` command).
- [ ] Add `--init-file` flag to pass custom DuckDB initialization script.

### Phase 4: Testing & Documentation

**Unit tests:**
- [ ] Add unit tests for agent DuckDB HTTP handler (read-only, 404, method
  validation).
- [ ] Add unit tests for colony DuckDB HTTP handler (read-only, 404, method
  validation).
- [ ] Test concurrent access: verify colony can write while HTTP serves reads.

**Integration tests:**
- [ ] Add integration test: start agent, attach via DuckDB, query metrics.
- [ ] Add integration test: start colony, attach via DuckDB, query aggregated
  data.
- [ ] Test multi-attach: query across multiple agents.
- [ ] Test federation: query both colony and agent data.

**E2E tests:**
- [ ] Add E2E test: full CLI workflow with real agent.
- [ ] Add E2E test: full CLI workflow with colony.
- [ ] Verify concurrent access: external query while colony writes.

**Documentation:**
- [ ] Update CLI documentation with usage examples.
- [ ] Add troubleshooting guide for common issues (mesh connectivity, agent
  down, no metrics, lock conflicts).
- [ ] Document colony vs agent query use cases.

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

### New HTTP Endpoint (Colony)

**Path:** `/duckdb/<filename>`

**Method:** `GET`

**Description:** Serves colony DuckDB file for read-only remote attach. Uses
`http.ServeFile` to serve raw file bytes without opening a DuckDB connection,
bypassing the exclusive lock held by the colony process.

**Authentication:** WireGuard mesh membership (implicit via network access
control).

**Request:**

```http
GET /duckdb/alex-dev-0977e1.duckdb HTTP/1.1
Host: colony.coral.mesh:9001
Range: bytes=0-16384
```

**Response (success):**

```http
HTTP/1.1 206 Partial Content
Content-Type: application/octet-stream
Content-Range: bytes 0-16384/52428800
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

- Only serves the colony DuckDB file (e.g., `<colony-id>.duckdb`).
- No directory traversal allowed (e.g., `../../../etc/passwd` returns 404).
- Read-only: POST/PUT/DELETE return 405.
- DuckDB files are read-only when served via HTTP (DuckDB limitation).
- **Critical**: `http.ServeFile` reads file bytes directly without requiring a
  DuckDB connection, allowing concurrent access while colony maintains exclusive
  lock.

**Concurrent Access Behavior:**

- Colony process maintains exclusive DuckDB lock for writes.
- HTTP handler serves file bytes using standard file I/O (no DuckDB connection).
- External clients download database pages via HTTP range requests.
- External clients open downloaded bytes in their own DuckDB process.
- Data served may be slightly stale (excludes uncommitted WAL changes).

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

**List available colonies:**

```bash
coral duckdb list-colonies

# Output:
COLONY ID         STATUS    MESH IP         DATABASE SIZE
alex-dev-0977e1   running   10.42.0.1       48 MB
prod-colony-001   running   10.42.0.2       1.2 GB
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

**Interactive shell (colony - historical data):**

```bash
coral duckdb shell colony

# Output:
DuckDB interactive shell. Type '.exit' to quit, '.help' for help.

Attached colony database: colony_alex_dev_0977e1

duckdb> .tables
metric_summaries
baselines
insights
services
service_connections
events
beyla_http_metrics
beyla_grpc_metrics
beyla_sql_metrics

duckdb> SELECT service_name,
        AVG(p95_latency_ms) as avg_p95,
        DATE_TRUNC('day', timestamp) as day
        FROM metric_summaries
        WHERE timestamp > now() - INTERVAL '7 days'
        GROUP BY service_name, day
        ORDER BY day DESC, avg_p95 DESC
        LIMIT 10;

service_name    avg_p95    day
api-server      127.4      2025-11-16
auth-service    89.2       2025-11-16
api-server      134.1      2025-11-15
...
(10 rows)

duckdb> .exit
```

**Interactive shell (federation - colony + agents):**

```bash
coral duckdb shell --colony --agents agent-prod-1

# Output:
DuckDB interactive shell. Type '.exit' to quit, '.help' for help.

Attached databases: colony_alex_dev_0977e1, agent_agent_prod_1

duckdb> -- Recent spikes (agent) vs historical baseline (colony)
        SELECT
          a.service_name,
          AVG(a.latency_bucket_ms) as current_p95,
          c.baseline_p95,
          ((AVG(a.latency_bucket_ms) - c.baseline_p95) / c.baseline_p95 * 100) as deviation_pct
        FROM agent_agent_prod_1.beyla_http_metrics_local a
        JOIN colony_alex_dev_0977e1.baselines c
          ON a.service_name = c.service_name
        WHERE a.timestamp > now() - INTERVAL '5 minutes'
        GROUP BY a.service_name, c.baseline_p95
        HAVING deviation_pct > 50
        ORDER BY deviation_pct DESC;

service_name    current_p95   baseline_p95   deviation_pct
api-server      245.3         87.2           181.3
checkout-svc    412.1         156.4          163.5
(2 rows)

duckdb> .exit
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

**One-shot query (colony - historical trends):**

```bash
coral duckdb query colony \
  "SELECT service_name, COUNT(*) as baseline_violations
   FROM baselines
   WHERE last_violation > now() - INTERVAL '7 days'
   GROUP BY service_name
   ORDER BY baseline_violations DESC"

# Output:
service_name        baseline_violations
api-server          23
checkout-service    15
auth-service        8
(3 rows)
```

**Meta-commands (in shell):**

- `.tables` - List all tables in attached databases
- `.databases` - Show attached databases
- `.help` - Show help message
- `.exit` or `.quit` - Exit shell

### Configuration Changes

None. Feature is enabled automatically if:

- Agent has Beyla enabled (RFD 032) - for agent queries
- Agent/colony HTTP server is running (always true)
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

**Colony DuckDB Handler:**

- `TestColonyDuckDBHandler_ServeFile_Success`: Verify colony file served with
  correct headers.
- `TestColonyDuckDBHandler_ConcurrentAccess`: Verify HTTP handler can serve file
  while colony process maintains exclusive DuckDB lock.
- `TestColonyDuckDBHandler_NotFound`: Verify 404 when colony database doesn't
  exist.
- `TestColonyDuckDBHandler_MethodNotAllowed`: Verify POST/PUT/DELETE return 405.
- `TestColonyDuckDBHandler_NoDirectoryTraversal`: Verify `../../../etc/passwd`
  returns 404.

**CLI Target Resolver:**

- `TestResolveAgentAddress_Success`: Verify agent ID resolves to mesh IP.
- `TestResolveAgentAddress_NotFound`: Verify error when agent doesn't exist.
- `TestResolveAgentAddress_MultipleAgents`: Verify multiple IDs resolve
  correctly.
- `TestResolveColonyAddress_Success`: Verify colony ID resolves to mesh IP.
- `TestResolveColonyAddress_NotFound`: Verify error when colony doesn't exist.

### Integration Tests

**Agent to CLI:**

- Start agent with Beyla enabled and metrics populated.
- Run
  `coral duckdb query agent-test "SELECT COUNT(*) FROM beyla_http_metrics_local"`.
- Verify query returns expected row count.
- Verify CLI exits cleanly.

**Colony to CLI:**

- Start colony with aggregated metrics.
- Run `coral duckdb query colony "SELECT COUNT(*) FROM metric_summaries"`.
- Verify query returns expected row count.
- Verify CLI exits cleanly.
- Verify colony can continue writing while CLI queries (concurrent access test).

**Multi-agent attach:**

- Start two agents with different metrics.
- Run `coral duckdb shell --agents agent-1,agent-2`.
- Execute UNION query across both databases.
- Verify results aggregate correctly.

**Federation (colony + agent):**

- Start colony and agent with overlapping time ranges.
- Run `coral duckdb shell --colony --agents agent-1`.
- Execute JOIN query between colony baselines and agent current metrics.
- Verify results combine both data sources correctly.

### E2E Tests

**Full workflow (agent):**

1. Deploy agent with Beyla monitoring a test service.
2. Generate HTTP traffic to populate metrics.
3. Run `coral duckdb list-agents`, verify agent appears.
4. Run `coral duckdb query` to fetch metrics.
5. Verify metrics match expected traffic patterns.

**Full workflow (colony):**

1. Deploy colony with historical aggregated data.
2. Run `coral duckdb list-colonies`, verify colony appears.
3. Run `coral duckdb query colony` to fetch historical metrics.
4. Verify metrics match expected historical patterns.
5. Verify concurrent writes: colony inserts new data while CLI queries.

**Error handling:**

- Agent down: Verify CLI reports connection error.
- Colony down: Verify CLI reports connection error.
- Beyla disabled: Verify CLI reports "database not found".
- Invalid SQL: Verify DuckDB syntax error returned.
- Concurrent access: Verify no lock conflicts when querying colony database.

## Security Considerations

**Authentication/Authorization:**

- Access control via WireGuard mesh membership (existing model).
- No additional authentication layer for HTTP endpoint (mesh is trusted
  network).
- Agents and colony must validate requests come from mesh IPs (WireGuard
  implicit).

**Data Exposure:**

- Entire DuckDB file is accessible to any mesh member (no row-level security).
- Acceptable because: (1) mesh is trusted network, (2) metrics are not PII, (3)
  operators need unrestricted access for debugging.
- Colony database may contain aggregated data from multiple services across the
  fleet; mesh membership provides appropriate access control.
- Future enhancement: Add token-based authentication for HTTP endpoint if
  needed.

**Read-Only Guarantees:**

- DuckDB's HTTP attach is read-only by design (cannot write over HTTP).
- Agent/colony HTTP handlers reject non-GET methods (defense in depth).
- DuckDB file permissions remain unchanged (agent/colony process can still
  write).
- `http.ServeFile` serves raw file bytes without DuckDB connection, preventing
  write operations.

**Concurrent Access Safety:**

- Colony maintains exclusive DuckDB lock for writes (ACID guarantees).
- HTTP handler uses `http.ServeFile` to serve file bytes via standard file I/O
  (no DuckDB connection needed).
- External clients receive point-in-time snapshot (may exclude uncommitted WAL
  changes).
- No risk of corruption: file serving is read-only at OS level.

**Denial of Service:**

- Malicious queries can consume agent/colony CPU/memory (e.g.,
  `SELECT * FROM huge_table`).
- Mitigation: WireGuard mesh limits access to trusted operators.
- Colony queries may be more expensive (larger datasets, 14-30 days retention).
- Future enhancement: Add query timeout or resource limits if needed.

**Audit Logging:**

- No logging of SQL queries in initial implementation.
- Future enhancement: Log queries to agent/colony logs for auditing.
- Particularly important for colony queries (access to fleet-wide data).

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

### Why Use Native `duckdb` CLI Instead of Custom REPL?

**Design Decision:** The `coral duckdb` command is a **thin wrapper** that
launches the native `duckdb` CLI binary, rather than implementing a custom REPL
using the DuckDB Go driver.

**Rationale:**

1. **Full REPL features for free**: The native DuckDB CLI includes:
   - Command history (readline/linenoise integration)
   - Tab completion for SQL keywords, table names, column names
   - Syntax highlighting
   - Multi-line editing
   - Meta-commands (`.tables`, `.schema`, `.mode`, `.output`, `.timer`, etc.)
   - Output formatting (table, CSV, JSON, markdown, HTML, etc.)
   - All continually improved by DuckDB maintainers

2. **Minimal implementation**: Using `exec.Command` to launch `duckdb`:
   ```go
   cmd := exec.Command("duckdb", "-cmd", attachStmt)
   cmd.Stdin = os.Stdin
   cmd.Stdout = os.Stdout
   cmd.Stderr = os.Stderr
   return cmd.Run()
   ```
   This is ~20 lines of code vs ~500+ lines for a custom REPL.

3. **No Go dependencies**: Avoids:
   - `github.com/marcboeker/go-duckdb` (DuckDB Go driver, ~50MB compiled)
   - `github.com/chzyer/readline` (readline library)
   - Managing DuckDB version compatibility in Go

4. **User familiarity**: Operators already familiar with `duckdb` CLI can use
   the same commands and workflows.

5. **Advanced features**: Users can leverage DuckDB's full CLI capabilities:
   - `.read script.sql` to execute SQL files
   - `.output file.csv` to redirect output
   - `.timer on` to measure query performance
   - `.explain` for query plans

**Trade-offs:**

- **Dependency**: Requires `duckdb` CLI binary in PATH.
  - Mitigation: Easy to install via package managers (`brew install duckdb`,
    `apt install duckdb`, `dnf install duckdb`).
  - Fallback: Provide download instructions in error message if binary not
    found.

- **Version compatibility**: Different DuckDB versions may have different
  features.
  - Mitigation: Document minimum required DuckDB version (e.g., ≥1.0.0).
  - Check version with `coral duckdb --check-duckdb` flag.

**Implementation:**

```go
// Simplified example
func shellCommand(target string, colonyClient *client.Colony) error {
    // 1. Resolve target to mesh IP
    meshIP, err := resolveTarget(target, colonyClient)
    if err != nil {
        return err
    }

    // 2. Construct ATTACH URL
    attachURL := fmt.Sprintf("http://%s:9001/duckdb/beyla.duckdb", meshIP)
    attachStmt := fmt.Sprintf("ATTACH '%s' AS %s (READ_ONLY);", attachURL, sanitize(target))

    // 3. Launch native duckdb CLI
    cmd := exec.Command("duckdb", "-cmd", attachStmt)
    cmd.Stdin = os.Stdin
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr

    return cmd.Run()
}
```

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
