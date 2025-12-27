# RFD 076 Implementation Status

**Feature:** CLI-Side TypeScript Execution with Deno
**Branch:** `claude/sandboxed-typescript-execution-A8eH6`
**Status:** 🔄 Refocusing from Agent-Side to CLI-Side

## Summary

RFD 076 has been **refocused to CLI-side execution only**. Users write TypeScript locally that queries colony DuckDB summaries via `coral run script.ts`. Agent-side execution is deferred to future work.

**Previous work** implemented agent-side execution infrastructure. This document identifies what can be **reused**, **refactored**, or **cleaned up** for the new CLI-focused plan.

---

## Architecture Shift

### Before: Agent-Side Execution
```
User → Colony → Agent → Deno Executor → Local DuckDB
```
Scripts deployed to agents, query local agent data via HTTP/UDS.

### After: CLI-Side Execution
```
User → coral run → Deno (embedded) → Colony gRPC API → Colony DuckDB
```
Scripts run locally, query colony aggregated data.

---

## Code Reusability Analysis

### ✅ **Reusable Components** (Minimal/No Changes)

These components can be reused for CLI with little to no modification:

| Component | Location | Reusability | Notes |
|-----------|----------|-------------|-------|
| **Deno Executor** | `internal/agent/script/executor.go` | 🟢 **High** | Core logic reusable, just change context |
| **TypeScript SDK Interface** | `pkg/sdk/typescript/*.ts` | 🟢 **High** | High-level helpers (metrics, traces) are perfect |
| **TypeScript Types** | `pkg/sdk/typescript/types.d.ts` | 🟢 **High** | Type definitions applicable to colony queries |
| **Example Scripts** | `examples/scripts/*.ts` | 🟡 **Medium** | Examples valid, just update imports/data source |
| **Deno Sandboxing Logic** | `executor.go` | 🟢 **High** | Permissions model same, just different allow-net |

### 🔄 **Refactoring Required**

These components need adaptation for CLI use:

| Component | Location | Changes Needed | Priority |
|-----------|----------|----------------|----------|
| **SDK Transport Layer** | `pkg/sdk/typescript/mod.ts` | Replace HTTP→Agent with gRPC→Colony | **High** |
| **Query Execution** | All SDK `*.ts` files | Query colony summaries, not agent local DB | **High** |
| **Executor Integration** | `executor.go` | Integrate with `coral run` CLI command | **High** |
| **Permissions** | `executor.go` | Change `--allow-net=localhost:9003` to `--allow-net=<colony-addr>` | **Medium** |
| **Script Lifecycle** | `executor.go` | Remove deployment tracking, use simple execution model | **Medium** |

### 🗑️ **Cleanup/Remove**

Agent-side specific code that's not needed for CLI:

| Component | Location | Action | Reason |
|-----------|----------|--------|--------|
| **Agent SDK Server (HTTP)** | `internal/agent/script/sdk_server.go` | ❌ **Remove** | CLI connects to colony gRPC, not agent HTTP |
| **Agent SDK Server (gRPC)** | `internal/agent/script/sdk_server_grpc.go` | ❌ **Remove** | Agent-side only |
| **Agent Script RPCs** | `proto/coral/agent/v1/script.proto` | ❌ **Remove** | DeployScript, StopScript not needed for CLI |
| **Agent Script Protobuf** | `proto/coral/sdk/v1/sdk.proto` | ❌ **Remove** | Agent SDK protocol not needed |
| **Script Deployment Logic** | References in agent code | ❌ **Remove** | No deployment, just local execution |
| **Demo Environment** | `examples/scripts/demo/*` | ❌ **Remove** | Agent-side demo not relevant |
| **Agent Tests** | `internal/agent/script/*_test.go` | 🔄 **Refactor** | Remove agent-specific, keep executor tests |

### 📝 **Documentation to Update**

| File | Action | Changes Needed |
|------|--------|----------------|
| `RFDs/076-sandboxed-typescript-execution.md` | ✅ **Updated** | Already refocused to CLI |
| `internal/agent/script/CONCURRENCY.md` | ❌ **Remove** | Agent DuckDB concurrency not relevant |
| `internal/agent/script/ARCHITECTURE_COMPARISON.md` | ❌ **Remove** | HTTP proxy comparison not relevant |
| `pkg/sdk/typescript/README.md` | 🔄 **Refactor** | Update for CLI usage, colony queries |
| `examples/scripts/README.md` | 🔄 **Refactor** | Update for `coral run` usage |

---

## New Implementation Plan (CLI-Focused)

Aligned with updated RFD 076.

### Phase 1: Colony Query API ⏳ **Not Started**

- [ ] Define `proto/coral/colony/v1/query.proto`
  - ListServices, GetPercentile, GetErrorRate, FindSlowTraces, ExecuteQuery RPCs
- [ ] Implement `internal/colony/api/query_service.go`
  - Service discovery from colony DuckDB
  - Metrics/traces/system queries
  - SQL executor with guardrails
- [ ] Add read-only connection pool
- [ ] Generate protobuf code

### Phase 2: Interactive CLI Commands ⏳ **Not Started**

- [ ] Implement `coral query services` command
- [ ] Implement `coral query metrics` command
- [ ] Implement `coral query traces` command
- [ ] Implement `coral query sql` command
- [ ] Add output formatting (table, JSON, CSV)

### Phase 3: Scripting Runtime 🔄 **Partial - Needs Refactoring**

**Reuse:**
- [ ] ✅ Adapt `internal/agent/script/executor.go` for CLI integration
- [ ] ✅ Reuse Deno process management logic
- [ ] ✅ Reuse sandboxing permissions model

**New:**
- [ ] Embed Deno binary in Coral CLI build
- [ ] Implement `coral run` command in CLI
- [ ] Create gRPC client in TypeScript SDK (replaces HTTP client)

**Refactor:**
- [ ] Update `pkg/sdk/typescript/mod.ts` to detect CLI mode and use gRPC
- [ ] Update SDK modules to call colony gRPC instead of agent HTTP
- [ ] Remove agent-side UDS/HTTP transport code

**Cleanup:**
- [ ] Remove `internal/agent/script/sdk_server*.go`
- [ ] Remove `proto/coral/sdk/v1/sdk.proto`
- [ ] Remove agent script deployment RPCs

### Phase 4: Examples & Documentation ✅ **Partial - Needs Updates**

**Reuse:**
- [ ] ✅ Adapt existing example scripts to use colony queries

**New:**
- [ ] Create CLI-focused examples
- [ ] Write `coral query` documentation
- [ ] Write `coral run` documentation
- [ ] Add SDK reference for colony queries

**Cleanup:**
- [ ] Remove agent demo environment

---

## Detailed Refactoring Tasks

### 1. Executor Adaptation

**File:** `internal/agent/script/executor.go`

**Changes:**
```go
// Before (Agent mode)
func (e *Executor) Execute(ctx context.Context, script *Script) error {
    cmd := exec.Command(e.denoPath,
        "run",
        "--allow-net=localhost:9003",  // Agent SDK server
        script.Path,
    )
}

// After (CLI mode)
func (e *Executor) Execute(ctx context.Context, script *Script) error {
    colonyAddr := os.Getenv("CORAL_COLONY_ADDR")
    cmd := exec.Command(e.denoPath,
        "run",
        fmt.Sprintf("--allow-net=%s", colonyAddr),  // Colony gRPC
        "--allow-read=./",
        script.Path,
    )
}
```

**Status:** 🔄 Refactor

### 2. TypeScript SDK Transport

**File:** `pkg/sdk/typescript/mod.ts`

**Changes:**
```typescript
// Before (Agent mode)
const SDK_SERVER = "http://localhost:9003";
async function query(sql: string) {
    const res = await fetch(`${SDK_SERVER}/query`, { ... });
}

// After (CLI mode)
import { createChannel, createClient } from "@grpc/grpc-js";
const colonyAddr = Deno.env.get("CORAL_COLONY_ADDR");
const client = createClient(QueryServiceDefinition, colonyAddr);
async function query(sql: string) {
    return await client.ExecuteQuery({ sql });
}
```

**Status:** 🔄 Major refactor

### 3. Example Scripts

**File:** `examples/scripts/high-latency-alert.ts`

**Changes:**
```typescript
// Before (queries agent local DB)
const spans = await coral.db.query(`
  SELECT * FROM otel_spans_local
  WHERE service_name = 'payments'
`);

// After (queries colony summaries)
const metrics = await coral.metrics.getPercentile(
  "payments",
  "http.server.duration",
  0.99
);
```

**Status:** 🔄 Minor refactor

---

## Files to Clean Up

### Remove Completely

```bash
# Agent-side SDK servers
rm internal/agent/script/sdk_server.go
rm internal/agent/script/sdk_server_grpc.go
rm internal/agent/script/sdk_server_test.go

# Agent-side protobuf
rm proto/coral/sdk/v1/sdk.proto
rm proto/coral/agent/v1/script.proto  # Or strip deployment RPCs

# Agent-side demo
rm -rf examples/scripts/demo/

# Agent-specific docs
rm internal/agent/script/CONCURRENCY.md
rm internal/agent/script/ARCHITECTURE_COMPARISON.md
```

### Refactor/Adapt

```bash
# Keep but refactor
internal/agent/script/executor.go → cmd/coral/script/executor.go (move to CLI)
pkg/sdk/typescript/*.ts  # Update transport layer
examples/scripts/*.ts    # Update to use colony queries
```

---

## Migration Checklist

### Step 1: Cleanup ✅ **COMPLETED**
- [x] Remove agent SDK server files
- [x] Remove agent script protobuf
- [x] Remove demo environment
- [x] Remove agent-specific documentation
- [x] Fix agent executor.go to remove SDK server references
- [x] Fix agent executor_test.go to remove SDK server references

### Step 2: Move Executor to CLI 🔄 **Pending**
- [ ] Move `executor.go` to `cmd/coral/script/`
- [ ] Update imports and dependencies
- [ ] Remove agent-specific code
- [ ] Adapt for CLI context

### Step 3: Refactor SDK ⏳ **Pending**
- [ ] Add gRPC client to TypeScript SDK
- [ ] Update transport layer for colony
- [ ] Remove HTTP/UDS transport code
- [ ] Update type definitions

### Step 4: Implement Colony API ✅ **COMPLETED (Phase 1)**
- [x] Define `proto/coral/colony/v1/query.proto`
- [x] Generate protobuf code
- [x] Implement `internal/colony/server/query_service.go` with basic query handlers:
  - [x] ListServices
  - [x] GetService
  - [x] GetPercentile
  - [x] GetErrorRate
  - [x] FindSlowTraces
  - [x] FindErrorTraces
  - [x] GetSystemMetrics
  - [x] ExecuteQuery
- [ ] Add read-only connection pool (future enhancement)
- [ ] Add comprehensive SQL validation (future enhancement)

### Step 5: Implement CLI Commands ⏳ **Not Started**
- [ ] Follow Phase 2 & 3 plans (see above)

---

## Current Branch Status

**Files to Keep (with refactoring):**
- ✅ `internal/agent/script/executor.go` - Core Deno execution logic
- ✅ `pkg/sdk/typescript/*.ts` - SDK interface (change transport)
- ✅ `pkg/sdk/typescript/types.d.ts` - Type definitions
- ✅ `examples/scripts/*.ts` - Example patterns (update queries)

**Files to Remove:**
- ❌ `internal/agent/script/sdk_server*.go` - Not needed
- ❌ `proto/coral/sdk/v1/sdk.proto` - Not needed
- ❌ `proto/coral/agent/v1/script.proto` - Deployment RPCs not needed
- ❌ `examples/scripts/demo/*` - Agent demo not relevant
- ❌ `internal/agent/script/CONCURRENCY.md` - Agent-specific
- ❌ `internal/agent/script/ARCHITECTURE_COMPARISON.md` - Agent-specific

**Tests to Refactor:**
- 🔄 `internal/agent/script/executor_test.go` - Keep executor tests, remove agent context
- ❌ `internal/agent/script/sdk_server_test.go` - Remove
- ❌ `internal/agent/script/integration_test.go` - Remove agent tests

---

## Next Immediate Actions

1. **Cleanup Branch** (removes ~2000 lines of agent-specific code):
   ```bash
   # Remove agent SDK servers
   git rm internal/agent/script/sdk_server*.go

   # Remove agent protobuf
   git rm proto/coral/sdk/v1/sdk.proto
   git rm proto/coral/agent/v1/script.proto  # Or keep only agent RPCs, remove script RPCs

   # Remove demo
   git rm -rf examples/scripts/demo/

   # Remove agent docs
   git rm internal/agent/script/{CONCURRENCY,ARCHITECTURE_COMPARISON}.md
   ```

2. **Start Phase 1** (Colony Query API):
   - Define `proto/coral/colony/v1/query.proto`
   - Implement query service in colony

3. **Prepare for SDK Refactor**:
   - Document current SDK transport for reference
   - Design gRPC client interface for TypeScript

---

## Success Metrics

### Cleanup Complete ✅
- [x] Agent-side SDK server code removed
- [x] Agent-side protobuf removed
- [x] Demo environment removed
- [x] Agent-specific docs removed
- [x] Branch reduced by ~2000 lines
- [x] Agent executor code updated to compile without SDK server

### Phase 1 Complete ✅
- [x] Colony gRPC Query API implemented (proto/coral/colony/v1/query.proto)
- [x] Protobuf generated and integrated
- [x] Basic query service handlers implemented (internal/colony/server/query_service.go)
- [ ] Full integration testing with DuckDB (pending)

### Phase 2 Complete ✅
- [ ] `coral query` commands work
- [ ] Can explore data interactively
- [ ] API validated via CLI usage

### Phase 3 Complete ✅
- [ ] `coral run` command works
- [ ] Deno embedded in CLI
- [ ] SDK queries colony successfully
- [ ] Example scripts run locally

---

## Latest Progress (2025-12-27)

### Completed:
1. **Cleanup phase** - Removed all agent-side SDK server code, protobuf files, demo environment, and agent-specific documentation
2. **Phase 1 (Colony Query API)** - Defined protobuf API, generated code, and implemented basic query service handlers
3. **Fixed compilation** - Updated agent executor to compile without SDK server dependencies

### Files Added:
- `proto/coral/colony/v1/query.proto` - Query service protobuf definition
- `internal/colony/server/query_service.go` - Query service implementation
- `coral/colony/v1/query.pb.go` - Generated protobuf code
- `coral/colony/v1/colonyv1connect/query.connect.go` - Generated connect RPC code

### Files Modified:
- `proto/coral/agent/v1/agent.proto` - Removed script RPCs and imports
- `internal/agent/script/executor.go` - Removed SDK server references
- `internal/agent/script/executor_test.go` - Fixed tests to compile without SDK server

### Files Removed:
- `internal/agent/script/sdk_server.go` - Agent SDK HTTP server (3 files)
- `proto/coral/sdk/v1/sdk.proto` - Agent SDK protobuf
- `proto/coral/agent/v1/script.proto` - Agent script deployment protobuf
- `examples/scripts/demo/` - Agent demo environment (2 files)
- `internal/agent/script/CONCURRENCY.md` - Agent concurrency docs
- `internal/agent/script/ARCHITECTURE_COMPARISON.md` - Agent architecture docs

### Next Steps:
1. **Phase 2** - Implement CLI commands (`coral query services`, `coral query metrics`, etc.)
2. **Phase 3** - Implement `coral run` command with embedded Deno
3. **Phase 4** - Create TypeScript SDK with gRPC client
4. **Testing** - Integration tests for query service with real DuckDB data

---

**Status:** Phase 1 (Colony Query API) complete. Ready to proceed with Phase 2 (CLI commands) and Phase 3 (scripting runtime).
