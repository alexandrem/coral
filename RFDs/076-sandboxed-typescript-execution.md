---
rfd: "076"
title: "Sandboxed TypeScript Execution with Deno"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: ["RFD 063", "RFD 066"]
database_migrations: ["scripts table", "script_executions table"]
areas: ["agent", "colony", "mcp", "sdk"]
---

# RFD 076 - Sandboxed TypeScript Execution with Deno

**Status:** 🚧 Draft

## Summary

Enable custom observability analysis and debugging through sandboxed TypeScript execution. **Primary use case**: Users write TypeScript locally that queries colony DuckDB summaries for custom analysis, dashboards, and integrations - executed via embedded Deno in Coral CLI. **Secondary use case**: Agent-side execution for high-frequency eBPF filtering and real-time sampling (managed by Colony/future Reef AI, not user-facing).

This replaces esoteric eBPF programs and shell scripts with familiar TypeScript and a curated SDK.

## Problem

**Current Limitations:**

- **eBPF Complexity**: Writing eBPF programs requires specialized kernel knowledge. While powerful for low-level introspection (RFD 061, 072), it's inaccessible for most operators debugging distributed systems.
- **Shell Script Brittleness**: MCP shell tools (RFD 045, 026) execute arbitrary commands but lack access to structured Coral data (DuckDB metrics, traces, function metadata).
- **No Custom Logic**: Operators cannot deploy persistent custom analysis logic that combines multiple Coral data sources (e.g., "alert when P99 latency exceeds 500ms AND error rate > 1% AND heap usage > 80%").
- **Limited Correlation**: Ad-hoc queries via MCP can't easily correlate metrics, traces, profiles, and host state for root cause analysis.

**Why This Matters:**

Coral's vision is "DTrace for distributed systems with natural language." DTrace succeeds because of D language - a sandboxed scripting language that makes dynamic instrumentation accessible. Similarly, Coral needs a sandboxed runtime for custom debugging logic that:

1. **AI can deploy**: Claude writes TypeScript based on user's natural language intent
2. **Operators can understand**: TypeScript is familiar, unlike eBPF or esoteric shell commands
3. **Accesses Coral data**: Scripts query metrics, traces, profiles, function metadata via SDK
4. **Runs safely**: Deno's permission model prevents destructive actions

**Use Cases:**

- **Anomaly Detection**: "Alert me when any service shows high latency with increased memory usage"
- **Correlation Analysis**: "Find traces where SQL query time correlates with Redis cache misses"
- **Custom Metrics**: "Track the ratio of successful payments to failed ones, grouped by payment provider"
- **Conditional Debugging**: "Attach uprobe to function X only when error rate exceeds threshold"
- **Live Validation**: "Verify that all pods have the same config hash"

## Solution: Dual Execution Model

### Execution Modes

**Mode 1: CLI-Side Scripting (PRIMARY - 95% of use cases)**
- Users write TypeScript **locally** for custom analysis
- Runs via **embedded Deno** in Coral binary (no external dependencies)
- Queries **colony DuckDB summaries** via gRPC
- Use cases: Ad-hoc queries, custom dashboards, cross-service correlation, integrations

**Mode 2: Agent-Side Scripting (SECONDARY - 5% of use cases)**
- Scripts run **on agents** for high-frequency processing
- Queries **local agent DuckDB** via UDS
- Use cases: eBPF filtering (10k+ events/sec), real-time sampling
- **Managed by Colony/Reef AI**, not user-deployed

### Why CLI-Primary?

Looking at actual use cases, **most scripts query aggregated data**:
- "Show me P99 latency for all services" → Queries colony summaries
- "Find slow traces across services" → Queries colony aggregated traces
- "Correlate errors with system metrics" → Queries colony summaries

**Agent-side only needed when**:
- Processing 10k+ events/second (eBPF filtering)
- High-frequency local sampling (100Hz CPU monitoring)
- Streaming raw data to colony is impractical

### Architecture

Same Deno executor, different data sources:
- **CLI mode**: Embedded in Coral binary, queries colony via gRPC
- **Agent mode**: Queries local DuckDB via UDS for high-frequency processing

**Key Design Decisions:**

1. **Embed Deno in Coral Binary** (not external dependency):
   - Coral CLI bundles Deno runtime (~100MB additional)
   - No user installation required (`coral run` just works)
   - Version consistency (Coral controls exact Deno version)
   - Platform-specific builds (Linux, macOS, Windows, ARM)
   - Trade-off: Larger binary, better UX

2. **CLI-Side Primary** (not agent-side):
   - Users write scripts locally (version controlled with their code)
   - Easy debugging (local stdout/stderr, no distributed logging)
   - No deployment complexity (just `coral run script.ts`)
   - Queries colony DuckDB summaries (already aggregated)
   - Perfect for community script sharing (just copy files)

3. **Agent-Side Secondary** (deferred to Phase 2+):
   - Only for high-frequency eBPF filtering and real-time sampling
   - Colony/Reef AI orchestrates deployment (not user-facing)
   - Same executor infrastructure, different data source
   - **Phase 1 focus: CLI-side only**

4. **Read-Only Query Model** (Phase 1):
   - Scripts query metrics, traces, system metrics (aggregated)
   - Scripts CANNOT write to DuckDB or modify state
   - Scripts CANNOT execute shell commands
   - Future: Event emission, custom metrics (Phase 2+)

5. **Unix Domain Socket + Protobuf for SDK Communication** (not HTTP/JSON):
   - SDK server listens on `/var/run/coral-sdk.sock` (not TCP)
   - Protobuf for serialization (type-safe, low overhead)
   - Read-only DuckDB connection pool (20 max concurrent queries)
   - Centralized query monitoring, timeouts, and automatic guardrails
   - **Why UDS**: Better security isolation, ~50% lower latency than TCP, no port conflicts
   - **Why Protobuf**: Type safety, efficient for high-frequency eBPF events, AI can generate typed code
   - **Alternative considered**: HTTP/JSON (Phase 1 prototype), Direct DuckDB access
   - **See**: `internal/agent/script/ARCHITECTURE_COMPARISON.md` for detailed analysis

6. **Hybrid Query Model** (intent over raw SQL):
   - **High-level helpers** (preferred): `metrics.getP99()`, `traces.findSlow()`
   - **Raw SQL** (fallback): `db.query()` for complex custom logic
   - **Benefits**: Schema evolution resilience, easier for AI to generate correct code
   - **Semantic guardrails**: Auto-inject `LIMIT` clauses and time-range filters

7. **Active SDK** (Level 3 capabilities):
   - Scripts can attach eBPF programs dynamically: `trace.uprobe()`, `trace.kprobe()`
   - Stream eBPF events in real-time for targeted debugging
   - Requires elevated permissions (opt-in,審reviewed by operator)
   - Enables "conditional debugging": attach probe only when anomaly detected

8. **Dual-TTL Resource Model**:
   - **Ad-hoc scripts**: 60-second default timeout (one-time analysis)
   - **Daemon scripts**: 24-hour max with heartbeat requirement (continuous monitoring)
   - **Resource quotas**: CPU 10% max, Memory 512MB max (entire Deno subsystem)
   - **Semantic SQL guardrails**: Automatic `LIMIT 10000` and `WHERE timestamp > now() - INTERVAL '1 hour'`

9. **Semantic Targeting** (not just agent IDs):
   - Deploy with predicates: `target: "service=payments AND region=us-east"`
   - Colony evaluates and deploys only where needed
   - Supports dynamic re-targeting as topology changes

**Benefits:**

- **Natural Language → Code**: AI translates "find slow queries" into TypeScript that queries DuckDB
- **Democratizes Debugging**: Operators write familiar TypeScript instead of eBPF
- **Safe Execution**: Deno sandboxing prevents destructive actions
- **Composable**: Scripts combine metrics, traces, profiles, host state for correlation
- **Persistent**: Scripts run continuously (e.g., anomaly detection) vs one-off shell commands

**Architecture Overview (Phase 1 - CLI-Side):**

```
┌─────────────────────────────────────────────────────────────┐
│ User writes TypeScript locally                              │
│  ┌───────────────────────────────────────────────────┐     │
│  │ analysis.ts                                       │     │
│  │                                                   │     │
│  │ import * as coral from "@coral/sdk";             │     │
│  │                                                   │     │
│  │ const svcs = await coral.services.list();        │     │
│  │ for (const svc of svcs) {                        │     │
│  │   const p99 = await svc.metrics.getP99(...);     │     │
│  │   if (p99 > threshold) {                         │     │
│  │     console.log(`⚠️ ${svc.name}: ${p99}ms`);      │     │
│  │   }                                               │     │
│  │ }                                                 │     │
│  └───────────────────────────────────────────────────┘     │
│                                                             │
│  $ coral run analysis.ts  ←── Embedded Deno                │
└───────────────────┬─────────────────────────────────────────┘
                    │
                    ▼ gRPC queries
┌─────────────────────────────────────────────────────────────┐
│ Coral CLI Binary (~140MB)                                   │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ Embedded Deno Runtime (~100MB)                       │   │
│  │  - Executes user TypeScript                         │   │
│  │  - Sandboxed (--allow-net=colony-addr)              │   │
│  │  - Mode: CORAL_MODE=cli                             │   │
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
│  │  - metrics.GetPercentile(svc, metric, p)            │   │
│  │  - traces.FindSlow(svc, threshold)                   │   │
│  │  - system.GetMetrics()                               │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ PHASE 2+ (Future): Agent-Side for High-Frequency Only       │
│  - eBPF filtering (10k+ events/sec)                         │
│  - Real-time sampling                                       │
│  - Colony/Reef AI orchestrated (not user-facing)            │
│  - Same executor, queries local agent DuckDB                │
└─────────────────────────────────────────────────────────────┘
```

### Component Changes (Phase 1 - CLI Focus)

1. **CLI** (`cmd/coral/`):

   - **New**: Embed Deno binary in Coral CLI (~100MB additional)
   - **New**: `coral run <script.ts>` - Execute TypeScript locally via embedded Deno
   - **New**: Deno executor wrapper (reuses `internal/agent/script/executor.go`)
   - **Modified**: Build system to bundle platform-specific Deno binaries

2. **Colony** (`internal/colony/`):

   - **New**: `api/query_service.go` - gRPC API for script queries
   - **New**: RPCs: `ListServices`, `GetPercentile`, `FindSlowTraces`, etc.
   - **Modified**: Expose colony DuckDB summaries via gRPC
   - No script registry needed (Phase 1 - scripts are local files)
   - No deployment orchestration needed (Phase 1 - local execution only)

3. **SDK** (`pkg/sdk/typescript/`):

   - **New**: TypeScript SDK package `@coral/sdk`
   - **New**: `services.ts` - Service discovery and queries
   - **New**: `metrics.ts` - High-level metrics query helpers
   - **New**: `traces.ts` - Trace/span query helpers
   - **New**: `system.ts` - System metrics helpers
   - **New**: SDK auto-detects CLI mode (CORAL_MODE=cli)
   - **New**: gRPC client for colony queries (CLI mode)

4. **Agent** (`internal/agent/`) - DEFERRED TO PHASE 2+:

   - **Existing**: `script/executor.go` - Already built (reusable for CLI)
   - **Existing**: `script/sdk_server_grpc.go` - Already built (for agent mode)
   - **Deferred**: Agent-side deployment (Colony/Reef AI orchestration)
   - **Deferred**: eBPF filtering, high-frequency sampling

**Configuration Example:**

```yaml
# agent.yaml
script:
  enabled: true
  deno_path: /usr/local/bin/deno  # Auto-download if missing
  max_concurrent: 5                # Max concurrent script executions
  memory_limit_mb: 512             # Per-script memory limit
  timeout_seconds: 300             # Max execution time
  sdk_server_port: 9003            # Local HTTP server for SDK (localhost only)
```

## API Changes

### New Protobuf Messages

```protobuf
// proto/coral/agent/v1/script.proto

// Script metadata
message Script {
    string id = 1;              // UUID
    string name = 2;            // Human-readable name
    string code = 3;            // TypeScript source code
    int32 version = 4;          // Incremental version
    repeated string targets = 5; // Agent IDs or service names
    ScriptTrigger trigger = 6;  // How script is triggered
    google.protobuf.Timestamp created_at = 7;
    string created_by = 8;      // User or AI
}

// Trigger types
message ScriptTrigger {
    oneof trigger {
        ManualTrigger manual = 1;     // Explicit trigger via API
        ScheduleTrigger schedule = 2;  // Cron schedule
        EventTrigger event = 3;        // On metric threshold, trace event, etc.
    }
}

message ManualTrigger {
    // Empty - script runs once when deployed
}

message ScheduleTrigger {
    string cron = 1;  // Cron expression: "*/5 * * * *" (every 5 min)
}

message EventTrigger {
    string condition = 1;  // SQL condition: "SELECT 1 FROM beyla_http_metrics WHERE error_rate > 0.01"
    int32 check_interval_seconds = 2;  // How often to check condition
}

// Deploy script to agents
message DeployScriptRequest {
    Script script = 1;
}

message DeployScriptResponse {
    string script_id = 1;
    int32 version = 2;
    repeated string deployed_to = 3;  // Agent IDs
}

// Stop script
message StopScriptRequest {
    string script_id = 1;
}

message StopScriptResponse {
    bool success = 1;
}

// Get script status
message GetScriptStatusRequest {
    string script_id = 1;
}

message GetScriptStatusResponse {
    string script_id = 1;
    ScriptStatus status = 2;
    repeated ScriptExecution executions = 3;  // Recent executions
}

enum ScriptStatus {
    SCRIPT_STATUS_UNSPECIFIED = 0;
    SCRIPT_STATUS_PENDING = 1;      // Deployed, not yet running
    SCRIPT_STATUS_RUNNING = 2;      // Currently executing
    SCRIPT_STATUS_STOPPED = 3;      // Manually stopped
    SCRIPT_STATUS_FAILED = 4;       // Execution failed
    SCRIPT_STATUS_COMPLETED = 5;    // One-time script finished
}

// Script execution result
message ScriptExecution {
    string execution_id = 1;
    string script_id = 2;
    string agent_id = 3;
    ScriptStatus status = 4;
    google.protobuf.Timestamp started_at = 5;
    google.protobuf.Timestamp completed_at = 6;
    int32 exit_code = 7;
    string stdout = 8;              // Captured stdout
    string stderr = 9;              // Captured stderr
    repeated ScriptEvent events = 10;  // Custom events emitted via sdk.emit()
}

// Custom events from scripts
message ScriptEvent {
    string name = 1;     // Event type: "alert", "metric", "log", etc.
    string data = 2;     // JSON payload
    google.protobuf.Timestamp timestamp = 3;
}
```

### New RPC Endpoints

```protobuf
// proto/coral/agent/v1/agent.proto

service AgentService {
    // ... existing RPCs ...

    // Script execution
    rpc DeployScript(DeployScriptRequest) returns (DeployScriptResponse);
    rpc StopScript(StopScriptRequest) returns (StopScriptResponse);
    rpc GetScriptStatus(GetScriptStatusRequest) returns (GetScriptStatusResponse);
    rpc StreamScriptLogs(StreamScriptLogsRequest) returns (stream ScriptLogEntry);
}

message StreamScriptLogsRequest {
    string script_id = 1;
    bool follow = 2;  // If true, stream logs in real-time
}

message ScriptLogEntry {
    google.protobuf.Timestamp timestamp = 1;
    string stream = 2;  // "stdout" or "stderr"
    string line = 3;
}
```

### MCP Tools

**coral_deploy_script**
```json
{
  "name": "coral_deploy_script",
  "description": "Deploy a TypeScript script to agents for custom observability logic",
  "inputSchema": {
    "type": "object",
    "properties": {
      "name": {
        "type": "string",
        "description": "Human-readable script name"
      },
      "code": {
        "type": "string",
        "description": "TypeScript source code"
      },
      "targets": {
        "type": "array",
        "items": {"type": "string"},
        "description": "Target agent IDs or service names (empty = all agents)"
      },
      "trigger": {
        "type": "object",
        "properties": {
          "type": {"enum": ["manual", "schedule", "event"]},
          "cron": {"type": "string"},
          "condition": {"type": "string"}
        }
      }
    },
    "required": ["name", "code"]
  }
}
```

**coral_list_scripts**
```json
{
  "name": "coral_list_scripts",
  "description": "List deployed scripts",
  "inputSchema": {
    "type": "object",
    "properties": {
      "status": {
        "enum": ["all", "running", "stopped", "failed"],
        "description": "Filter by status"
      }
    }
  }
}
```

**coral_script_status**
```json
{
  "name": "coral_script_status",
  "description": "Get execution status and logs for a script",
  "inputSchema": {
    "type": "object",
    "properties": {
      "script_id": {"type": "string"},
      "tail": {"type": "number", "description": "Show last N log lines"}
    },
    "required": ["script_id"]
  }
}
```

### CLI Commands (Phase 1)

```bash
# Run TypeScript locally (primary use case)
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

# Future (Phase 2+): Agent deployment
# coral script deploy alert.ts --agents "payments-*"
# coral script list
# coral script logs <id>
# coral script stop <id>
```

### Database Schema

```sql
-- Colony DuckDB

-- Scripts registry
CREATE TABLE scripts (
    id UUID PRIMARY KEY,
    name VARCHAR NOT NULL,
    code TEXT NOT NULL,              -- TypeScript source
    version INTEGER NOT NULL,
    targets VARCHAR[],               -- Agent IDs or service names
    trigger_type VARCHAR,            -- 'manual', 'schedule', 'event'
    trigger_config JSON,             -- Cron expression, event condition, etc.
    created_at TIMESTAMP NOT NULL,
    created_by VARCHAR,              -- User or 'ai'
    INDEX idx_scripts_name (name)
);

-- Script execution history
CREATE TABLE script_executions (
    execution_id UUID PRIMARY KEY,
    script_id UUID NOT NULL,
    agent_id VARCHAR NOT NULL,
    status VARCHAR NOT NULL,         -- 'pending', 'running', 'completed', 'failed'
    started_at TIMESTAMP NOT NULL,
    completed_at TIMESTAMP,
    exit_code INTEGER,
    stdout TEXT,
    stderr TEXT,
    events JSON[],                   -- Custom events from sdk.emit()
    INDEX idx_executions_script (script_id),
    INDEX idx_executions_agent (agent_id),
    INDEX idx_executions_status (status)
);
```

### TypeScript SDK API

```typescript
// @coral/sdk - Available to all Deno scripts

import * as coral from "@coral/sdk";

// Query local DuckDB
const spans = await coral.db.query(`
  SELECT trace_id, span_id, duration_ns
  FROM otel_spans_local
  WHERE service_name = 'payments'
    AND duration_ns > 500000000
  ORDER BY start_time DESC
  LIMIT 100
`);

// High-level metrics helpers
const p99 = await coral.metrics.getPercentile("payments", "http.server.duration", 0.99);
const errorRate = await coral.metrics.getErrorRate("payments", "5m");

// Query traces
const slowTraces = await coral.traces.query({
  service: "payments",
  minDuration: "500ms",
  timeRange: "1h"
});

// Function metadata (RFD 063, 075)
const functions = await coral.functions.list("payments");
const fnMeta = await coral.functions.get("payments", "ProcessPayment");

// System metrics (RFD 071)
const cpu = await coral.system.getCPU();
const memory = await coral.system.getMemory();

// Emit custom events (sent to colony)
await coral.emit("alert", {
  severity: "warning",
  message: "P99 latency exceeded 500ms",
  service: "payments",
  p99: p99,
  timestamp: new Date().toISOString()
});

// Logging (captured by agent, sent to colony)
console.log("Script started");
console.error("Something went wrong");
```

**Example Script: High Latency Alert**

```typescript
// alert-high-latency.ts
import * as coral from "@coral/sdk";

async function checkLatency() {
  const p99 = await coral.metrics.getPercentile("payments", "http.server.duration", 0.99);
  const errorRate = await coral.metrics.getErrorRate("payments", "5m");

  if (p99 > 500_000_000) {  // 500ms in nanoseconds
    await coral.emit("alert", {
      severity: "warning",
      message: `P99 latency (${p99 / 1_000_000}ms) exceeded threshold`,
      service: "payments",
      p99_ms: p99 / 1_000_000,
      error_rate: errorRate
    });

    console.log(`[ALERT] High latency: ${p99 / 1_000_000}ms`);
  } else {
    console.log(`[OK] P99 latency: ${p99 / 1_000_000}ms`);
  }
}

// Run every 60 seconds
while (true) {
  await checkLatency();
  await new Promise(resolve => setTimeout(resolve, 60_000));
}
```

**Example Script: Correlation Analysis**

```typescript
// correlate-errors-memory.ts
import * as coral from "@coral/sdk";

async function analyzeCorrelation() {
  // Find traces with errors
  const errorTraces = await coral.db.query(`
    SELECT trace_id, service_name, duration_ns
    FROM otel_spans_local
    WHERE status_code = 'ERROR'
      AND start_time > now() - INTERVAL '5 minutes'
  `);

  // Get memory usage for services with errors
  const memory = await coral.system.getMemory();

  for (const trace of errorTraces) {
    const memUsagePct = (memory.used / memory.total) * 100;

    if (memUsagePct > 80) {
      await coral.emit("correlation", {
        type: "error_high_memory",
        trace_id: trace.trace_id,
        service: trace.service_name,
        memory_usage_pct: memUsagePct,
        duration_ms: trace.duration_ns / 1_000_000
      });

      console.log(`[CORRELATION] ${trace.service_name}: Error + High Memory (${memUsagePct}%)`);
    }
  }
}

await analyzeCorrelation();
```

## Implementation Plan

### Phase 1: CLI-Side Scripting (CURRENT FOCUS)

**Goal**: Users run TypeScript locally via `coral run script.ts`

- [x] Deno executor infrastructure (`internal/agent/script/executor.go`) - DONE
- [x] TypeScript type definitions (`pkg/sdk/typescript/types.d.ts`) - DONE
- [ ] Embed Deno binary in Coral CLI
  - [ ] Download platform-specific Deno binaries (Linux, macOS, Windows, ARM)
  - [ ] Add to build system (embed via go:embed or extract on first run)
  - [ ] ~100MB binary size increase (compressed: ~45MB)
- [ ] Implement `coral run` command
  - [ ] Parse script file path and parameters
  - [ ] Set CORAL_MODE=cli environment variable
  - [ ] Execute via embedded Deno with colony connection
  - [ ] Stream stdout/stderr to terminal
- [ ] Colony gRPC Query API
  - [ ] Define `proto/coral/colony/v1/query.proto`
  - [ ] Implement `ListServices`, `GetPercentile`, `FindSlowTraces` RPCs
  - [ ] Expose colony DuckDB summaries (read-only)
- [ ] TypeScript SDK CLI Mode
  - [ ] Auto-detect CLI mode (CORAL_MODE=cli)
  - [ ] gRPC client for colony queries
  - [ ] Implement `services.ts`, `metrics.ts`, `traces.ts`
- [ ] Examples & Documentation
  - [ ] Example CLI scripts (latency analysis, error correlation)
  - [ ] Update README with `coral run` usage
  - [ ] Community script templates

**Success Criteria**:
- Users can run `coral run analysis.ts` without any setup
- No external dependencies (Deno bundled)
- Scripts query colony DuckDB summaries
- Full TypeScript flexibility with IDE autocomplete

### Phase 2: Agent-Side Execution (FUTURE - DEFERRED)

**Goal**: Colony/Reef AI deploys scripts to agents for eBPF filtering

- [ ] Agent deployment orchestration
- [ ] Colony script registry (DuckDB storage)
- [ ] eBPF probe integration (Level 3 capabilities)
- [ ] High-frequency sampling use cases
- [ ] MCP tools for AI deployment

**Deferred because**:
- Most use cases don't need agent-side (query aggregated data)
- Colony/Reef AI orchestration not yet built
- CLI-side provides 95% of value immediately

## Security Considerations

### Phase 1: CLI-Side Security

**Deno Sandboxing (CLI Mode)**:

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

**Module Imports** (Phase 1):

- Allowed imports: `@coral/sdk`, Deno standard library (`jsr:@std`)
- Blocked imports: External URLs, `file://` URIs (outside current dir)
- Future: Whitelist npm: packages for popular libraries

### Phase 2+: Agent-Side Security (Future)

**Additional Sandboxing**:

- More restrictive permissions (UDS only, no network)
- Scripts deployed by Colony/Reef AI (not users directly)
- Audit logging for all deployments
- RBAC integration for sensitive data

**Deployment Control**:

- Only Colony/Reef AI can deploy to agents
- Script validation before deployment
- Immutable script storage (versioned)
- Automatic cleanup and lifecycle management

## Migration Strategy

**Deployment Steps:**

1. Deploy Colony with new database migrations (`scripts`, `script_executions` tables)
2. Deploy Agents with Deno runtime and script executor
3. Publish `@coral/sdk` TypeScript package to local registry
4. Register MCP tools for script management
5. Enable script execution via feature flag (optional)

**Rollback Plan:**

- Scripts are opt-in (no impact on existing observability)
- Disable script execution by stopping Deno workers on agents
- No breaking changes to existing MCP tools or RPCs
- Database migrations are additive (new tables, no schema changes to existing tables)

**Compatibility:**

- Requires Deno 2.0+ (for stable TypeScript support)
- Agents auto-download Deno binary if missing (or fail gracefully)
- Colony validates Deno version before deploying scripts

## Implementation Status

**Phase 1 (CLI-Side):** 🚧 In Progress

- ✅ Deno executor infrastructure complete
- ✅ TypeScript type definitions complete
- ✅ gRPC SDK server complete (for future agent-side)
- ✅ Protobuf schemas complete
- ⏳ Embed Deno in CLI binary (next)
- ⏳ Implement `coral run` command (next)
- ⏳ Colony gRPC Query API (next)
- ⏳ SDK CLI mode implementation (next)

**Phase 2 (Agent-Side):** ⏳ Deferred

- Agent-side deployment orchestration deferred
- Will be implemented when Colony/Reef AI orchestration is ready
- Infrastructure already built and ready to use

## Future Work

### Agent-Side Script Execution (DEFERRED - Phase 2+)

**Status**: Deferred to future implementation when Colony/Reef AI orchestration is ready.

**Why Deferred**:
- **95% of use cases** are satisfied by CLI-side execution (querying aggregated colony summaries)
- Agent-side only needed for **specific high-frequency processing** (eBPF filtering, real-time sampling)
- **Colony/Reef AI orchestration** not yet built (required for safe agent deployment)
- **Infrastructure already complete** - executor, SDK server, protobuf schemas ready to use

**Agent-Side Use Cases** (when implemented in Phase 2+):
- **eBPF Event Filtering**: Process 10k+ events/sec locally, emit only exceptions to colony
- **High-Frequency Sampling**: 100Hz CPU/memory sampling with local buffering
- **Real-Time Aggregation**: Cases where streaming raw data to colony is impractical

**Requirements for Phase 2+**:
- [ ] Colony/Reef AI orchestration system (AI-driven deployment, not user-facing)
- [ ] Script registry in colony DuckDB (versioned, immutable storage)
- [ ] Deployment orchestration (semantic targeting, health checks, rollback)
- [ ] eBPF integration (Level 3 capabilities, RFD 063 function metadata)
- [ ] Audit logging and RBAC integration
- [ ] MCP tools for AI-driven deployment

**Important**: Agent-side execution will be **AI-orchestrated only**. Users will not directly deploy scripts to agents - they will use CLI-side execution (`coral run`) for their custom analysis needs.

---

### CLI Enhancements (Phase 1 Continuation)

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

---

### Advanced Features (Low Priority)

**Multi-Language Support**:
- Python scripts via Pyodide (WASM)
- Lua scripts via gopher-lua
- WASM modules for custom languages

**Write Operations** (Requires careful design):
- Scripts can write custom metrics to colony DuckDB
- Scripts can trigger actions (e.g., alerts, notifications)
- Requires enhanced RBAC and audit logging

---

## Appendix

### Example: AI-Driven Script Deployment

**User**: "Alert me when the payments service has high latency and increased error rate"

**Claude (via MCP)**:
1. Translates intent to TypeScript
2. Calls `coral_deploy_script`:

```typescript
import * as coral from "@coral/sdk";

async function checkHealthMetrics() {
  const p99 = await coral.metrics.getPercentile("payments", "http.server.duration", 0.99);
  const errorRate = await coral.metrics.getErrorRate("payments", "5m");

  const latencyThreshold = 500_000_000;  // 500ms
  const errorRateThreshold = 0.01;       // 1%

  if (p99 > latencyThreshold && errorRate > errorRateThreshold) {
    await coral.emit("alert", {
      severity: "critical",
      message: "Payments service degraded: high latency AND high error rate",
      p99_ms: p99 / 1_000_000,
      error_rate_pct: errorRate * 100,
      timestamp: new Date().toISOString()
    });
  }
}

// Run every 30 seconds
while (true) {
  await checkHealthMetrics();
  await new Promise(resolve => setTimeout(resolve, 30_000));
}
```

3. Colony deploys to agents running `payments` service
4. Agents execute script, emit alerts when conditions met
5. Claude surfaces alerts to user: "🚨 Payments service degraded on payments-service-2: P99=650ms, Error Rate=2.3%"

### SDK Implementation Notes

**SDK Server Architecture**:
- Agent runs local HTTP server on `localhost:9003` (not exposed externally)
- Deno scripts call SDK via HTTP (no gRPC in scripts for simplicity)
- SDK server translates HTTP requests to DuckDB queries

**Example SDK Server API**:
```
GET /metrics/percentile?service=payments&metric=http.server.duration&p=0.99
GET /metrics/error-rate?service=payments&window=5m
GET /traces?service=payments&minDuration=500ms
POST /emit {"name": "alert", "data": {...}}
```

**SDK Client (TypeScript)**:
```typescript
// @coral/sdk/metrics.ts
export async function getPercentile(service: string, metric: string, p: number): Promise<number> {
  const url = `http://localhost:9003/metrics/percentile?service=${service}&metric=${metric}&p=${p}`;
  const res = await fetch(url);
  const data = await res.json();
  return data.value;
}
```

### Deno Permissions Reference

Phase 1 allows:
- `--allow-read=/var/lib/coral/duckdb` - Read DuckDB files only
- No network, no write, no env, no run

Future phases may allow:
- `--allow-net=localhost:9003` - Call SDK server (explicit localhost only)
- `--allow-write=/var/lib/coral/script-storage` - Persistent script state (opt-in)

All permissions must be explicitly granted; Deno denies by default.
