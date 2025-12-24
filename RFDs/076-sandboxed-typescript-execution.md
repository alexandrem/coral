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

Enable natural language-driven debugging and custom observability logic by deploying sandboxed TypeScript scripts to agents via Deno runtime. Scripts execute locally on agents with controlled access to Coral data (metrics, traces, functions, host state), replacing esoteric eBPF programs and shell scripts with familiar TypeScript and a curated SDK.

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

## Solution

Deploy **agent-side Deno runtime** that executes sandboxed TypeScript scripts orchestrated by Colony. Scripts access Coral data via a TypeScript SDK and run with explicit Deno permissions (no filesystem write, no network, read-only DuckDB access).

**Key Design Decisions:**

1. **Deno over Node.js**:
   - Built-in sandboxing with granular permissions (--allow-read, --allow-net)
   - Native TypeScript support (no transpilation)
   - Secure by default (denies all I/O unless explicitly allowed)
   - Single binary distribution (simplifies agent deployment)

2. **Agent-side Execution** (not Colony):
   - Scales horizontally (each agent executes scripts for its services)
   - Low latency access to local DuckDB (metrics, traces, spans)
   - Reduces colony load and network traffic
   - Follows pull-based architecture (RFD 025, 046)

3. **Colony-Orchestrated Deployment**:
   - Colony stores scripts in DuckDB (versioned, immutable)
   - AI deploys scripts via MCP tools (e.g., `coral_deploy_script`)
   - Agents pull scripts on-demand or via periodic sync
   - Script lifecycle: draft → deployed → running → stopped → archived

4. **Read-Only SDK** (Phase 1):
   - Scripts can query DuckDB (metrics, traces, spans, system metrics)
   - Scripts can read function metadata (RFD 063, 075)
   - Scripts CANNOT write to DuckDB or execute commands
   - Scripts CANNOT modify agent state (read-only observability)

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

**Architecture Overview:**

```
┌─────────────────────────────────────────────────────────────┐
│ User: "Alert when payments service P99 > 500ms"            │
└───────────────────┬─────────────────────────────────────────┘
                    │ Natural Language
                    ▼
┌─────────────────────────────────────────────────────────────┐
│ Claude Desktop (MCP Client)                                 │
│  - Translates intent to TypeScript                          │
│  - Calls coral_deploy_script(name, code, targets)           │
└───────────────────┬─────────────────────────────────────────┘
                    │ MCP JSON-RPC (stdio)
                    ▼
┌─────────────────────────────────────────────────────────────┐
│ Colony (Script Registry)                                    │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ DuckDB: scripts table                                │   │
│  │  - id, name, code, version, targets, created_at      │   │
│  │  - Immutable: updates create new version             │   │
│  └──────────────────────────────────────────────────────┘   │
│  - Validates TypeScript syntax                              │
│  - Stores script with metadata                              │
│  - Notifies target agents (gRPC)                            │
└───────────────────┬─────────────────────────────────────────┘
                    │ DeployScript RPC
                    ▼
┌─────────────────────────────────────────────────────────────┐
│ Agent (Deno Script Executor)                                │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ Deno Runtime (sandboxed)                             │   │
│  │  - Permissions: --allow-read=/var/run/coral-sdk.sock │   │
│  │  - No filesystem write, no network                   │   │
│  │  - Import Coral SDK (@coral/sdk)                     │   │
│  │  - Resource limits: CPU 10%, Memory 512MB total      │   │
│  └──────────────────────────────────────────────────────┘   │
│                                                             │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ Coral TypeScript SDK (@coral/sdk + types.d.ts)       │   │
│  │  LEVEL 1 (Passive - Read-Only):                      │   │
│  │  - metrics.getP99(), getErrorRate() [preferred]      │   │
│  │  - traces.findSlow(), correlate()                    │   │
│  │  - db.query(sql) [fallback for complex logic]        │   │
│  │  - system.getCPU(), getMemory()                      │   │
│  │  LEVEL 2 (Active - Event Emission):                  │   │
│  │  - emit(event): Send alerts/metrics                  │   │
│  │  LEVEL 3 (Dynamic Instrumentation - Opt-in):         │   │
│  │  - trace.uprobe(fn, handler): Attach uprobe          │   │
│  │  - trace.kprobe(fn, handler): Attach kprobe          │   │
│  └─────────────────┬────────────────────────────────────┘   │
│                    │ Protobuf over UDS                      │
│                    │ /var/run/coral-sdk.sock                │
│                    ▼                                         │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ SDK Server (UDS Listener + Protobuf Handler)         │   │
│  │  - Read-only connection pool: 20 max                 │   │
│  │  - Query timeout: 60s (ad-hoc), 24h (daemon)         │   │
│  │  - Semantic guardrails: Auto-inject LIMIT & filters  │   │
│  │  - Resource quotas: CPU 10%, Memory 512MB aggregate  │   │
│  │  - Dry-run validation before production deploy       │   │
│  │  - Centralized logging & monitoring                  │   │
│  └─────────────────┬────────────────────────────────────┘   │
│                    │ sql.DB.QueryContext(ctx, sql)          │
│                    ▼                                         │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ Local DuckDB (read-only connection pool)             │   │
│  │  - agent.db?access_mode=read_only&threads=4          │   │
│  │  - otel_spans_local (~1hr retention)                 │   │
│  │  - beyla_http_metrics, beyla_grpc_metrics            │   │
│  │  - system_metrics_local (RFD 071)                    │   │
│  │  - cpu_profiles (RFD 072)                            │   │
│  └──────────────────────────────────────────────────────┘   │
│                                                             │
│  - Executes script in isolated Deno worker                  │
│  - Captures stdout/stderr and events                        │
│  - Reports execution status to colony                       │
└───────────────────┬─────────────────────────────────────────┘
                    │ ScriptExecutionResult RPC
                    ▼
┌─────────────────────────────────────────────────────────────┐
│ Colony (Result Aggregation)                                 │
│  - Stores execution results in DuckDB                       │
│  - Aggregates results from multiple agents                  │
│  - Exposes via MCP tools (coral_script_status)              │
└─────────────────────────────────────────────────────────────┘
```

### Component Changes

1. **Agent** (`internal/agent/`):

   - **New**: `script/executor.go` - Deno process manager, executes scripts in isolated workers
   - **New**: `script/sdk_server.go` - HTTP server exposing SDK functions to Deno scripts (local only)
   - **Modified**: `service_handler.go` - Add `DeployScript`, `StopScript`, `GetScriptStatus` RPCs
   - **Modified**: `agent.go` - Initialize script executor on startup

2. **Colony** (`internal/colony/`):

   - **New**: `script/registry.go` - Script storage, versioning, validation
   - **New**: `script/deployer.go` - Orchestrates script deployment to target agents
   - **Modified**: `database/schema.go` - Add `scripts`, `script_executions` tables
   - **Modified**: `mcp/server.go` - Register new MCP tools for script management
   - **New**: `mcp/tools_script.go` - MCP tools: `coral_deploy_script`, `coral_list_scripts`, `coral_script_status`

3. **SDK** (`pkg/sdk/typescript/`):

   - **New**: TypeScript SDK package `@coral/sdk`
   - **New**: `db.ts` - DuckDB query interface
   - **New**: `metrics.ts` - High-level metrics query helpers
   - **New**: `traces.ts` - Trace/span query helpers
   - **New**: `functions.ts` - Function metadata access (RFD 063)
   - **New**: `system.ts` - System metrics helpers
   - **New**: `emit.ts` - Send events/results to colony

4. **CLI** (`cmd/coral/`):

   - **New**: `coral script deploy` - Deploy script to agents
   - **New**: `coral script list` - List deployed scripts
   - **New**: `coral script status <id>` - Show script execution status
   - **New**: `coral script logs <id>` - Stream script logs (stdout/stderr)
   - **New**: `coral script stop <id>` - Stop running script

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

### CLI Commands

```bash
# Deploy a script
coral script deploy --name "high-latency-alert" --file alert.ts --targets "payments-service"

# Example output:
Script deployed: high-latency-alert
  ID: 550e8400-e29b-41d4-a716-446655440000
  Version: 1
  Deployed to: 3 agents (payments-service-1, payments-service-2, payments-service-3)
  Status: RUNNING

# List scripts
coral script list

# Example output:
ID                                   NAME                  STATUS    TARGETS              DEPLOYED
550e8400-e29b-41d4-a716-446655440000 high-latency-alert    RUNNING   payments-service     2m ago
7c9e6679-7425-40de-944b-e07fc1f90ae7 cache-miss-detector   RUNNING   redis-service        1h ago
9f8b2c5e-4a3b-4c7d-8e6f-1b2c3d4e5f6a sql-slow-query        STOPPED   postgres-service     3d ago

# Show script status and logs
coral script status 550e8400-e29b-41d4-a716-446655440000

# Example output:
Script: high-latency-alert (v1)
Status: RUNNING
Deployed to: 3 agents

Recent Executions:
  Agent: payments-service-1 | Status: RUNNING | Started: 2m ago
  Agent: payments-service-2 | Status: RUNNING | Started: 2m ago
  Agent: payments-service-3 | Status: RUNNING | Started: 2m ago

Recent Logs (last 10 lines):
[payments-service-1] 2025-12-23T10:15:23Z [INFO] Checking P99 latency...
[payments-service-1] 2025-12-23T10:15:23Z [INFO] P99: 450ms (OK)
[payments-service-2] 2025-12-23T10:15:24Z [WARN] P99: 520ms (THRESHOLD EXCEEDED)
[payments-service-2] 2025-12-23T10:15:24Z [ALERT] High latency detected on payments-service-2

# Stream logs in real-time
coral script logs 550e8400-e29b-41d4-a716-446655440000 --follow

# Stop a script
coral script stop 550e8400-e29b-41d4-a716-446655440000

# Example output:
Script stopped: high-latency-alert
  Stopped on: 3 agents
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

### Phase 1: Core Infrastructure

- [ ] Define protobuf messages in `proto/coral/agent/v1/script.proto`
- [ ] Generate Go code from proto definitions
- [ ] Create database migrations for `scripts` and `script_executions` tables
- [ ] Add Deno binary distribution to agent build (embed or auto-download)

### Phase 2: Agent-Side Execution

- [ ] Implement `internal/agent/script/executor.go` - Deno process manager
- [ ] Implement `internal/agent/script/sdk_server.go` - Local HTTP server for SDK
- [ ] Add RPCs to `internal/agent/service_handler.go` (DeployScript, StopScript, GetScriptStatus)
- [ ] Implement sandboxing with Deno permissions (--allow-read for DuckDB only)

### Phase 3: Colony-Side Registry

- [ ] Implement `internal/colony/script/registry.go` - Script storage and versioning
- [ ] Implement `internal/colony/script/deployer.go` - Deployment orchestration
- [ ] Add database queries for script CRUD operations
- [ ] Implement script validation (TypeScript syntax checking via Deno)

### Phase 4: TypeScript SDK

- [ ] Create `pkg/sdk/typescript/` directory structure
- [ ] Implement `db.ts` - DuckDB query interface
- [ ] Implement `metrics.ts` - Metrics helpers
- [ ] Implement `traces.ts` - Trace query helpers
- [ ] Implement `functions.ts` - Function metadata access
- [ ] Implement `system.ts` - System metrics helpers
- [ ] Implement `emit.ts` - Event emission to colony
- [ ] Publish `@coral/sdk` package (local registry or npm)

### Phase 5: MCP Integration

- [ ] Implement `internal/colony/mcp/tools_script.go`
- [ ] Add `coral_deploy_script` tool
- [ ] Add `coral_list_scripts` tool
- [ ] Add `coral_script_status` tool
- [ ] Register tools in `internal/colony/mcp/server.go`

### Phase 6: CLI Commands

- [ ] Add `coral script deploy` command
- [ ] Add `coral script list` command
- [ ] Add `coral script status` command
- [ ] Add `coral script logs` command
- [ ] Add `coral script stop` command

### Phase 7: Testing & Documentation

- [ ] Unit tests for script executor
- [ ] Unit tests for script registry
- [ ] Integration tests (deploy → execute → verify results)
- [ ] E2E tests with MCP tools
- [ ] Example scripts (alerts, correlation, custom metrics)
- [ ] Update ARCHITECTURE.md with script execution flow

## Security Considerations

**Deno Sandboxing:**

- Scripts run with minimal permissions:
  - `--allow-read=/var/lib/coral/duckdb` - Read-only access to local DuckDB
  - No `--allow-write`, `--allow-net`, `--allow-env`, `--allow-run`
- Scripts cannot execute shell commands or access filesystem outside DuckDB
- Scripts cannot make network requests (prevents data exfiltration)
- Memory limits enforced via Deno flags (`--v8-flags=--max-old-space-size=512`)
- CPU limits enforced via cgroups (optional, platform-dependent)

**Script Validation:**

- Colony validates TypeScript syntax before deployment (via `deno check`)
- Colony rejects scripts that import non-whitelisted modules
- Allowed imports: `@coral/sdk`, `@std` (Deno standard library)
- Blocked imports: External URLs, file:// URIs, npm: packages (Phase 1)

**Audit Logging:**

- All script deployments logged with `created_by` field (user or AI)
- Script execution logs captured (stdout, stderr, events)
- Script code stored immutably (versioned, cannot be modified)
- Colony exposes audit trail via MCP tools

**RBAC Integration** (Future - RFD 058):

- Script deployment requires `scripts:deploy` permission
- Script execution results filtered by user's service access
- Sensitive data (e.g., traces with PII) masked based on RBAC policy

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

**Core Capability:** ⏳ Not Started

This RFD defines a new capability for agent-side sandboxed TypeScript execution. Implementation will proceed in phases as outlined above.

## Future Work

**Advanced Triggers** (Future - RFD TBD)
- Event-driven scripts: trigger on metric threshold, trace event, log pattern
- Distributed triggers: trigger script on ALL agents when condition met
- Conditional uprobes: attach uprobe only when script detects anomaly

**Write Operations** (Future - RFD TBD)
- Scripts can write custom metrics to agent DuckDB
- Scripts can trigger actions (e.g., restart service, scale pod, trigger debug session)
- Requires enhanced RBAC and audit logging

**NPM Package Support** (Future - RFD TBD)
- Allow scripts to import npm packages (with security review)
- Whitelist popular libraries (lodash, date-fns, etc.)
- Block packages with known vulnerabilities or native code

**Multi-Language Support** (Low Priority)
- Python scripts via Pyodide (WASM)
- Lua scripts via gopher-lua
- WASM modules for custom languages

**Script Marketplace** (Low Priority)
- Community-contributed scripts for common use cases
- Script templates for AI to customize (e.g., "alert template")
- Version control integration (Git-backed script storage)

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
