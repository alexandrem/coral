---
rfd: "076"
title: "Sandboxed TypeScript Execution"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ ]
database_migrations: [ ]
areas: [ "cli", "colony", "sdk" ]
---

# RFD 076 - Sandboxed TypeScript Execution with Deno

**Status:** ✅ Implemented

## Summary

Enable custom observability analysis and debugging through sandboxed TypeScript
execution on the CLI. Users write TypeScript locally that queries colony DuckDB
summaries for custom analysis, dashboards, and integrations - executed via
embedded Deno in Coral CLI.

This replaces esoteric shell scripts with familiar TypeScript and a curated SDK,
making observability data accessible through local scripting.

## Problem

**Current Limitations:**

- **No Custom Logic**: Operators cannot easily write custom analysis logic that
  combines multiple Coral data sources (e.g., "alert when P99 latency exceeds
  500ms AND error rate > 1% AND heap usage > 80%").
- **Limited Correlation**: Ad-hoc queries via MCP can't easily correlate
  metrics, traces, profiles, and host state for root cause analysis.
- **No Local Analysis**: Users must rely on MCP tools for every query instead of
  writing reusable analysis scripts.

**Why This Matters:**

Coral's vision is "DTrace for distributed systems with natural language." To
enable powerful local analysis, Coral needs a sandboxed runtime for custom
debugging logic that:

1. **AI can generate**: Claude writes TypeScript based on user's natural
   language intent
2. **Operators can understand**: TypeScript is familiar and can be
   version-controlled
3. **Accesses Coral data**: Scripts query metrics, traces, profiles, function
   metadata via SDK
4. **Runs safely**: Deno's permission model prevents destructive actions

**Use Cases:**

- **Anomaly Detection**: "Alert me when any service shows high latency with
  increased memory usage"
- **Correlation Analysis**: "Find traces where SQL query time correlates with
  Redis cache misses"
- **Custom Dashboards**: "Generate a daily report of P99 latency for all
  services"
- **Integration Scripts**: "Export metrics to external systems or alert
  channels"
- **Live Validation**: "Verify that all pods have the same config hash"

## Solution: CLI-Side TypeScript Execution

### Architecture Overview

Users write TypeScript locally that queries colony DuckDB summaries for custom
analysis:

- **Local execution**: Runs via **embedded Deno** in Coral CLI (no external
  dependencies)
- **Queries colony**: Accesses **colony DuckDB summaries** via gRPC
- **Use cases**: Ad-hoc queries, custom dashboards, cross-service correlation,
  integrations

### Why CLI-Side?

Most observability analysis queries **aggregated data**:

- "Show me P99 latency for all services" → Queries colony summaries
- "Find slow traces across services" → Queries colony aggregated traces
- "Correlate errors with system metrics" → Queries colony summaries
- "Generate daily reports" → Queries historical colony data

CLI-side execution provides:

- ✅ **No deployment complexity** - Just `coral run script.ts`
- ✅ **Easy debugging** - Local stdout/stderr, no distributed logging
- ✅ **Version control** - Scripts live with your code
- ✅ **Community sharing** - Just copy files
- ✅ **IDE support** - Full TypeScript autocomplete

### Shared Query API Architecture

```
┌─────────────────────────────────────────────────────────────┐
│ Colony gRPC Query API                                       │
│  - ListServices, GetPercentile, FindSlowTraces, etc.       │
└────────────┬────────────────────────────┬───────────────────┘
             │                            │
             ▼                            ▼
┌────────────────────────┐   ┌──────────────────────────────┐
│ coral query (CLI)      │   │ TypeScript SDK (@coral/sdk)  │
│                        │   │                              │
│ $ coral query services │   │ coral.services.list()        │
│ $ coral query metrics  │   │ coral.metrics.getPercentile()│
│ $ coral query traces   │   │ coral.traces.findSlow()      │
└────────────────────────┘   └─────────────┬────────────────┘
                                           │
                                           ▼
                             ┌──────────────────────────────┐
                             │ coral run (CLI)              │
                             │                              │
                             │ $ coral run script.ts        │
                             └──────────────────────────────┘
```

**Benefits:**

- Same data, multiple interfaces
- CLI validates SDK behavior
- Single implementation to test and maintain

### Key Design Decisions

1. **Embed Deno in Coral Binary** (not external dependency):

    - Coral CLI bundles Deno runtime (~100MB additional)
    - No user installation required (`coral run` just works)
    - Version consistency (Coral controls exact Deno version)
    - Platform-specific builds (Linux, macOS, Windows, ARM)
    - Trade-off: Larger binary, better UX

2. **CLI-Side Execution** (local-first):

    - Users write scripts locally (version controlled with their code)
    - Easy debugging (local stdout/stderr, no distributed logging)
    - No deployment complexity (just `coral run script.ts`)
    - Queries colony DuckDB summaries (already aggregated)
    - Perfect for community script sharing (just copy files)

3. **Read-Only Query Model**:

    - Scripts query metrics, traces, system metrics (aggregated from colony)
    - Scripts CANNOT write to DuckDB or modify state
    - Scripts CANNOT execute shell commands (sandboxed by Deno)
    - Future: Event emission, custom metrics (Phase 2+)

4. **gRPC API for Colony Queries** (not HTTP/JSON):

    - SDK connects to colony via gRPC
    - Type-safe, efficient serialization
    - Supports streaming for large result sets
    - Centralized query monitoring and timeouts

5. **Hybrid Query Model** (intent over raw SQL):

    - **High-level helpers** (preferred): `metrics.getP99()`,
      `traces.findSlow()`
    - **Raw SQL** (fallback): `db.query()` for complex custom logic
    - **Benefits**: Schema evolution resilience, easier for AI to generate
      correct code
    - **Semantic guardrails**: Auto-inject `LIMIT` clauses and time-range
      filters

6. **Script Timeouts and Resource Limits**:
    - **Default timeout**: 60 seconds for ad-hoc analysis
    - **Memory limit**: 512MB max per script
    - **Semantic SQL guardrails**: Automatic `LIMIT 10000` and
      `WHERE timestamp > now() - INTERVAL '1 hour'`

### Benefits

- **Natural Language → Code**: AI translates "find slow queries" into TypeScript
  that queries DuckDB
- **Democratizes Analysis**: Operators write familiar TypeScript instead of
  complex shell scripts
- **Safe Execution**: Deno sandboxing prevents destructive actions
- **Composable**: Scripts combine metrics, traces, profiles, host state for
  correlation
- **Version Controlled**: Scripts live with your code and can be shared

### Architecture Diagram

```
┌────────────────────────────────────────────────────────────┐
│ User writes TypeScript locally                             │
│  ┌───────────────────────────────────────────────────┐     │
│  │ analysis.ts                                       │     │
│  │                                                   │     │
│  │ import * as coral from "@coral/sdk";              │     │
│  │                                                   │     │
│  │ const svcs = await coral.services.list();         │     │
│  │ for (const svc of svcs) {                         │     │
│  │   const p99 = await svc.metrics.getP99(...);      │     │
│  │   if (p99 > threshold) {                          │     │
│  │     console.log(`⚠️ ${svc.name}: ${p99}ms`);      │     │
│  │   }                                               │     │
│  │ }                                                 │     │
│  └───────────────────────────────────────────────────┘     │
│                                                            │
│  $ coral run analysis.ts  ←── Embedded Deno                │
└───────────────────┬────────────────────────────────────────┘
                    │
                    ▼ gRPC queries
┌─────────────────────────────────────────────────────────────┐
│ Coral CLI Binary (~140MB)                                   │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ Embedded Deno Runtime (~100MB)                       │   │
│  │  - Executes user TypeScript                          │   │
│  │  - Sandboxed (--allow-net=colony-addr)               │   │
│  │  - Mode: CORAL_MODE=cli                              │   │
│  └──────────────────────────────────────────────────────┘   │
│                                                             │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ TypeScript SDK (@coral/sdk)                          │   │
│  │  - Auto-detects CLI mode                             │   │
│  │  - Connects to colony gRPC API                       │   │
│  │  - services.list(), metrics.getP99(), etc.           │   │
│  └───────────────────┬──────────────────────────────────┘   │
└────────────────────┬─┴──────────────────────────────────────┘
                     │ gRPC
                     ▼
┌─────────────────────────────────────────────────────────────┐
│ Colony                                                      │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ DuckDB (Aggregated Summaries)                        │   │
│  │  - service_summary (P50, P99, error rates)           │   │
│  │  - trace_summary (slow traces, errors)               │   │
│  │  - system_metrics_rollup (1min aggregates)           │   │
│  └──────────────────────────────────────────────────────┘   │
│                                                             │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ gRPC Query API (NEW)                                 │   │
│  │  - services.List()                                   │   │
│  │  - metrics.GetPercentile(svc, metric, p)             │   │
│  │  - traces.FindSlow(svc, threshold)                   │   │
│  │  - system.GetMetrics()                               │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

### Component Changes

1. **CLI** (`cmd/coral/`):

    - **New**: Embed Deno binary in Coral CLI (~100MB additional)
    - **New**: `coral run <script.ts>` - Execute TypeScript locally via embedded
      Deno
    - **New**: Deno executor wrapper for CLI mode
    - **Modified**: Build system to bundle platform-specific Deno binaries

2. **Colony** (`internal/colony/`):

    - **New**: `api/query_service.go` - gRPC API for querying colony data
    - **New**: RPCs: `ListServices`, `GetPercentile`, `FindSlowTraces`, etc.
    - **Modified**: Expose colony DuckDB summaries via gRPC
    - **Integration**: Powers both `coral query` CLI commands AND TypeScript SDK
    - No script registry needed (scripts are local files)
    - No deployment orchestration needed (local execution only)

3. **SDK** (`pkg/sdk/typescript/`):

    - **New**: TypeScript SDK package `@coral/sdk`
    - **New**: `services.ts` - Service discovery and queries
    - **New**: `metrics.ts` - High-level metrics query helpers
    - **New**: `traces.ts` - Trace/span query helpers
    - **New**: `system.ts` - System metrics helpers
    - **New**: gRPC client for colony queries

### CLI Configuration

No configuration needed - `coral run` works out of the box. Optional environment
variables:

```bash
# Optional: Override colony address
export CORAL_COLONY_ADDR=colony.example.com:9090

# Optional: Script timeout (default: 60s)
export CORAL_SCRIPT_TIMEOUT=120

# Optional: Memory limit (default: 512MB)
export CORAL_SCRIPT_MEMORY=1024
```

## API Changes

### Colony gRPC Query API

New unified query service that powers both `coral query` CLI commands and
TypeScript SDK:

**Dual Purpose:**

- **CLI Commands**: `coral query services`, `coral query metrics`, etc.
- **TypeScript SDK**: `coral.services.list()`, `coral.metrics.getPercentile()`,
  etc.

**API Definition:**

```protobuf
// proto/coral/colony/v1/query.proto

service QueryService {
    // Service discovery
    rpc ListServices(ListServicesRequest) returns (ListServicesResponse);
    rpc GetService(GetServiceRequest) returns (GetServiceResponse);

    // Metrics queries
    rpc GetPercentile(GetPercentileRequest) returns (GetPercentileResponse);
    rpc GetErrorRate(GetErrorRateRequest) returns (GetErrorRateResponse);

    // Trace queries
    rpc FindSlowTraces(FindSlowTracesRequest) returns (FindSlowTracesResponse);
    rpc FindErrorTraces(FindErrorTracesRequest) returns (FindErrorTracesResponse);

    // System metrics
    rpc GetSystemMetrics(GetSystemMetricsRequest) returns (GetSystemMetricsResponse);

    // Raw SQL (for advanced use cases)
    rpc ExecuteQuery(ExecuteQueryRequest) returns (ExecuteQueryResponse);
}

// Service discovery
message ListServicesRequest {
    string namespace = 1;  // Optional filter
}

message ListServicesResponse {
    repeated Service services = 1;
}

message Service {
    string name = 1;
    string namespace = 2;
    string region = 3;
    int32 instance_count = 4;
}

// Metrics
message GetPercentileRequest {
    string service = 1;
    string metric = 2;
    double percentile = 3;  // 0.0-1.0
    int64 time_range_ms = 4;  // Lookback window
}

message GetPercentileResponse {
    double value = 1;
    string unit = 2;  // "nanoseconds", "bytes", etc.
}

message GetErrorRateRequest {
    string service = 1;
    int64 time_range_ms = 2;
}

message GetErrorRateResponse {
    double rate = 1;  // 0.0-1.0
    int64 error_count = 2;
    int64 total_count = 3;
}

// Traces
message FindSlowTracesRequest {
    string service = 1;
    int64 min_duration_ns = 2;
    int64 time_range_ms = 3;
    int32 limit = 4;
}

message FindSlowTracesResponse {
    repeated Trace traces = 1;
    int64 total_count = 2;
}

message Trace {
    string trace_id = 1;
    int64 duration_ns = 2;
    google.protobuf.Timestamp timestamp = 3;
    string service = 4;
}
```

### Usage Examples

**Same API, Two Interfaces:**

```bash
# CLI: Direct query
coral query services
# Returns: payments, orders, inventory

coral query metrics payments --metric http.server.duration --percentile 99
# Returns: 892.1ms

coral query traces payments --slow --threshold 500ms
# Returns: trace-abc123 (1523ms), trace-def456 (1205ms), ...
```

```typescript
// TypeScript SDK: Programmatic access to same API
import * as coral from "@coral/sdk";

const services = await coral.services.list();
// Same data as `coral query services`

const p99 = await coral.metrics.getPercentile("payments", "http.server.duration", 0.99);
// Same data as `coral query metrics payments --percentile 99`

const slowTraces = await coral.traces.findSlow("payments", 500_000_000, 3600_000);
// Same data as `coral query traces payments --slow`
```

**Benefits of Shared API:**

- CLI commands provide instant validation of SDK behavior
- Changes to query logic benefit both interfaces
- Consistent results between manual queries and scripts
- Single implementation to maintain and test

### CLI Commands

**Direct Queries** (uses gRPC Query API):

```bash
# Query services
coral query services

# Query metrics
coral query metrics payments --metric http.server.duration --percentile 99
coral query metrics payments --error-rate --window 5m

# Query traces
coral query traces payments --slow --threshold 500ms --limit 10
coral query traces payments --errors --window 1h

# Raw SQL
coral query sql "SELECT service_name, AVG(p99_duration_ns) FROM service_summary GROUP BY service_name"
```

**Script Execution** (scripts use same gRPC Query API):

```bash
# Run TypeScript locally
coral run analysis.ts

# Example output:
Service Latency Report

payments:
  P50: 45.2ms
  P99: 892.1ms
  Errors: 0.12%

orders:
  P50: 23.1ms
  P99: 456.3ms
  Errors: 0.03%

# Run with parameters
coral run latency-check.ts --param threshold=500

# Watch mode (re-run on file changes)
coral run --watch analysis.ts

# Run with custom timeout
coral run --timeout 120 long-analysis.ts
```

### TypeScript SDK API

```typescript
// @coral/sdk - Available to all Deno scripts

import * as coral from "@coral/sdk";

// Service discovery
const services = await coral.services.list();
for (const svc of services) {
    console.log(`${svc.name} (${svc.namespace})`);
}

// High-level metrics helpers
const p99 = await coral.metrics.getPercentile("payments", "http.server.duration", 0.99);
const errorRate = await coral.metrics.getErrorRate("payments", 300_000);  // 5 minutes

// Query traces
const slowTraces = await coral.traces.findSlow("payments", 500_000_000, 3600_000);  // >500ms, last hour
const errorTraces = await coral.traces.findErrors("payments", 3600_000);  // last hour

// System metrics (aggregated from agents)
const systemMetrics = await coral.system.getMetrics("payments");
console.log(`CPU: ${systemMetrics.cpu_percent}%`);
console.log(`Memory: ${systemMetrics.memory_percent}%`);

// Raw SQL queries (for advanced use cases)
const customQuery = await coral.db.query(`
  SELECT service_name, AVG(p99_duration_ns) as avg_p99
  FROM service_summary
  WHERE timestamp > now() - INTERVAL '1 hour'
  GROUP BY service_name
  ORDER BY avg_p99 DESC
`);

// Logging (displayed locally)
console.log("Script started");
console.error("Something went wrong");
```

### Example Scripts

**Example 1: Service Latency Report**

```typescript
// latency-report.ts
import * as coral from "@coral/sdk";

const services = await coral.services.list();

console.log("Service Latency Report\n");

for (const svc of services) {
    const p99 = await coral.metrics.getPercentile(svc.name, "http.server.duration", 0.99);
    const errorRate = await coral.metrics.getErrorRate(svc.name, 3600_000);  // last hour

    console.log(`${svc.name}:`);
    console.log(`  P99: ${(p99 / 1_000_000).toFixed(1)}ms`);
    console.log(`  Error Rate: ${(errorRate * 100).toFixed(2)}%`);

    if (p99 > 500_000_000) {
        console.log(`  ⚠️  High latency detected!`);
    }
}
```

**Example 2: Cross-Service Correlation**

```typescript
// correlation-analysis.ts
import * as coral from "@coral/sdk";

const services = ["payments", "orders", "inventory"];
const results = [];

for (const svc of services) {
    const errorRate = await coral.metrics.getErrorRate(svc, 300_000);  // 5 min
    const systemMetrics = await coral.system.getMetrics(svc);

    results.push({
        service: svc,
        errorRate,
        memoryPercent: systemMetrics.memory_percent,
        cpuPercent: systemMetrics.cpu_percent
    });
}

// Detect potential correlations
const unhealthy = results.filter(r => r.errorRate > 0.01);

if (unhealthy.length >= 2) {
    console.log("⚠️  Cascading failure detected!");
    for (const svc of unhealthy) {
        console.log(`  ${svc.service}: ${(svc.errorRate * 100).toFixed(2)}% errors, CPU: ${svc.cpuPercent}%, Memory: ${svc.memoryPercent}%`);
    }
}
```

## Implementation Plan

### Phase 1: Colony Query API ✅ Complete

- [x] Define `proto/coral/colony/v1/query.proto` with service discovery,
  metrics, traces, and SQL query RPCs
  → Implemented in `proto/coral/colony/v1/queries.proto`
- [x] Implement `internal/colony/api/query_service.go` with DuckDB query
  handlers
  → Implemented in `internal/colony/server/query_service.go`
- [x] Add read-only connection pool with timeout and size limit enforcement
  → DuckDB queries with safety validation
- [x] Generate protobuf code and integrate with Colony
  → Integrated into ColonyService gRPC API

### Phase 2: Interactive CLI Commands ✅ Complete

- [x] Implement `coral query services` command with namespace/region filters
  → `internal/cli/query/services.go`
- [x] Implement `coral query metrics` command with percentile and error-rate
  flags
  → `internal/cli/query/metrics.go`
- [x] Implement `coral query traces` command with slow/error filters
  → `internal/cli/query/traces.go`
- [x] Implement `coral query sql` command for raw SQL queries
  → `internal/cli/query/sql.go`
- [x] Add output formatting (table, JSON, CSV) with `--output` flag
  → Table and JSON formatting implemented

### Phase 3: Scripting Runtime ✅ Complete

- [x] Embed Deno binary in Coral CLI build system
  → `internal/cli/run/embed*.go` with platform-specific builds
- [x] Implement `coral run` command with script execution and parameter support
  → `internal/cli/run/run.go` with timeout and watch mode
- [x] Create TypeScript SDK (`pkg/sdk/typescript/`) with gRPC client
  → Complete SDK with client.ts and all modules
- [x] Implement SDK modules: `services.ts`, `metrics.ts`, `traces.ts`,
  `system.ts`, `db.ts`
  → All modules implemented + bonus `activity.ts` module
- [ ] Package and publish SDK to JSR as `@coral/sdk`
  → SDK code complete, JSR publishing not yet done

### Phase 4: Examples & Documentation ✅ Complete

- [x] Create example scripts (latency reports, error correlation, trace
  analysis)
  → 5 example scripts in `examples/scripts/`
- [x] Write user documentation for `coral query` and `coral run` commands
  → CLI_REFERENCE.md updated with both sections
- [x] Add SDK reference documentation
  → docs/SDK_REFERENCE.md (comprehensive API reference)
- [x] Add TypeScript scripting guide
  → docs/TYPESCRIPT_SCRIPTING.md (patterns, examples, deployment)
- [x] Implement `--watch` mode for `coral run`
  → Implemented in run.go
- [ ] Design community script template repository structure
  → Deferred to future work

## Security Considerations

**Deno Sandboxing**:

- Scripts run with minimal permissions:
    - `--allow-net=<colony-address>` - Only connect to colony gRPC
    - `--allow-read=./` - Read local files only (for imports)
    - No `--allow-write`, `--allow-env`, `--allow-run`
- Scripts cannot execute shell commands
- Scripts cannot access filesystem outside current directory
- Scripts query colony summaries (already aggregated, no PII)
- Memory limits enforced via Deno flags (`--v8-flags=--max-old-space-size=512`)

**Local Execution**:

- Scripts run in user's context (same permissions as user)
- No elevated privileges required
- Stdout/stderr visible to user (easy debugging)
- No remote code execution (runs locally)

**Module Imports**:

- Allowed imports: `@coral/sdk`, Deno standard library (`jsr:@std`)
- Blocked imports: External URLs, `file://` URIs (outside current dir)
- Future: Whitelist npm: packages for popular libraries

## Migration Strategy

**Deployment Steps:**

1. Embed Deno binary in Coral CLI build
2. Implement `coral run` command
3. Deploy Colony with new gRPC Query API
4. Publish `@coral/sdk` TypeScript package
5. Update documentation with examples

**Rollback Plan:**

- CLI scripts are opt-in (no impact on existing tools)
- No breaking changes to existing MCP tools or RPCs
- No database migrations needed (queries existing tables)

**Compatibility:**

- Requires Deno 2.0+ (bundled with Coral CLI)
- No external dependencies required

## Implementation Status

**Core Capability:** ✅ Implemented (Production Ready)

**Overall Status:** All phases complete. Feature is production-ready with
comprehensive documentation.

### Summary

- **Phase 1 (Colony Query API):** ✅ Complete
- **Phase 2 (CLI Commands):** ✅ Complete
- **Phase 3 (Scripting Runtime):** ✅ Complete
- **Phase 4 (Examples & Docs):** ✅ Complete

### Detailed Status

**Phase 1: Colony Query API** ✅ Complete

- Protobuf schema: `proto/coral/colony/v1/queries.proto`
- Implementation: `internal/colony/server/query_service.go`
- All RPCs functional: ListServices, GetMetricPercentile, GetServiceActivity,
  ListServiceActivity, ExecuteQuery
- Integrated into ColonyService gRPC API
- DuckDB read-only queries with safety guardrails

**Phase 2: Interactive CLI Commands** ✅ Complete

- `coral query services` - List discovered services (
  internal/cli/query/services.go)
- `coral query metrics` - Enhanced with --percentile flag (
  internal/cli/query/metrics.go)
- `coral query traces` - Trace queries (internal/cli/query/traces.go)
- `coral query sql` - Raw SQL execution (internal/cli/query/sql.go)
- Output formatting: table, JSON (configured per command)

**Phase 3: Scripting Runtime** ✅ Complete

- Deno binary embedding system (internal/cli/run/embed*.go)
- `coral run` command fully functional (internal/cli/run/run.go)
- TypeScript SDK complete (pkg/sdk/typescript/):
    - Core modules: services.ts, metrics.ts, traces.ts, system.ts, db.ts
    - Additional: activity.ts (service activity queries)
    - gRPC client: client.ts
    - Type definitions: types.ts
- Sandboxing: --allow-net (colony only), --allow-read (local), no write/run
  permissions
- Watch mode: `coral run --watch script.ts` implemented

**Phase 4: Examples & Documentation** ✅ Complete

- ✅ Example scripts (examples/scripts/):
    - latency-report.ts - Service latency monitoring
    - correlation-analysis.ts - Cross-service correlation
    - high-latency-alert.ts - Anomaly detection
    - service-activity.ts - Activity metrics
    - sdk-demo-monitor.ts - Full SDK demonstration
- ✅ Documentation complete:
    - CLI_REFERENCE.md updated with `coral run` and focused query commands
    - SDK reference: docs/SDK_REFERENCE.md (comprehensive API documentation)
    - Scripting guide: docs/TYPESCRIPT_SCRIPTING.md (patterns, examples, best
      practices)
    - Community script repository: Deferred to future work

### Next Steps

1. ✅ All core features implemented and documented
2. Consider JSR package publishing for `@coral/sdk`
3. Community script repository/marketplace (future work)
4. Performance monitoring and optimization
5. User feedback and iteration

## Future Work

### CLI Enhancements

**NPM Package Support**:

- Allow scripts to import npm packages (with security review)
- Whitelist popular libraries (lodash, date-fns, etc.)
- Block packages with known vulnerabilities or native code

**Script Marketplace**:

- Community-contributed CLI scripts for common use cases
- Script templates (latency analysis, error correlation, custom dashboards)
- Version control integration (Git-backed script storage)
- Rating and discovery system

**Watch Mode & Live Reload**:

- `coral run --watch script.ts` - Re-run on file changes
- Live reload for iterative development
- Error highlighting and debugging

### Agent-Side Script Execution

**Status**: Deferred to future implementation when Colony/Reef AI orchestration
is ready.

**Why Deferred**:

- **95% of use cases** are satisfied by CLI-side execution (querying aggregated
  colony summaries)
- Agent-side only needed for **specific high-frequency processing** (eBPF
  filtering, real-time sampling)
- **Colony/Reef AI orchestration** not yet built (required for safe agent
  deployment)
- **Infrastructure already complete** - executor, SDK server, protobuf schemas
  ready to use

**Agent-Side Use Cases** (when implemented in Phase 2+):

- **eBPF Event Filtering**: Process 10k+ events/sec locally, emit only
  exceptions to colony
- **High-Frequency Sampling**: 100Hz CPU/memory sampling with local buffering
- **Real-Time Aggregation**: Cases where streaming raw data to colony is
  impractical

**Requirements for Phase 2+**:

- [ ] Colony/Reef AI orchestration system (AI-driven deployment, not
  user-facing)
- [ ] Script registry in colony DuckDB (versioned, immutable storage)
- [ ] Deployment orchestration (semantic targeting, health checks, rollback)
- [ ] eBPF integration (Level 3 capabilities, RFD 063 function metadata)
- [ ] Audit logging and RBAC integration
- [ ] MCP tools for AI-driven deployment

**Important**: Agent-side execution will be **AI-orchestrated only**. Users will
not directly deploy scripts to agents - they will use CLI-side execution (
`coral run`) for their custom analysis needs.

---

## Appendix

### Example: AI-Generated CLI Script

**User**: "Show me which services have high latency"

**Claude (via Chat)**:

1. Translates intent to TypeScript
2. Creates local script file:

```typescript
// check-latency.ts
import * as coral from "@coral/sdk";

const services = await coral.services.list();
const threshold = 500_000_000;  // 500ms

console.log("Services with high latency (>500ms):\n");

for (const svc of services) {
    const p99 = await coral.metrics.getPercentile(svc.name, "http.server.duration", 0.99);

    if (p99 > threshold) {
        console.log(`${svc.name}: ${(p99 / 1_000_000).toFixed(1)}ms`);
    }
}
```

3. User runs script locally:

```bash
$ coral run check-latency.ts

Services with high latency (>500ms):

payments: 892.1ms
legacy-api: 1523.4ms
```

### SDK Implementation Notes

**CLI Mode Architecture**:

- Scripts run locally via embedded Deno in Coral CLI
- SDK connects to colony via gRPC
- Queries aggregated summaries (not raw data)

**SDK Package Structure**:

```
@coral/sdk/
  ├── mod.ts          # Main entry point
  ├── services.ts     # Service discovery
  ├── metrics.ts      # Metrics queries
  ├── traces.ts       # Trace queries
  ├── system.ts       # System metrics
  └── db.ts           # Raw SQL queries
```

### Deno Permissions

CLI mode permissions:

- `--allow-net=<colony-address>` - Connect to colony gRPC only
- `--allow-read=./` - Read local files for imports
- No write, env, or run permissions

All permissions must be explicitly granted; Deno denies by default.
