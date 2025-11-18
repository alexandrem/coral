---
rfd: "042"
title: "SDK-Integrated eBPF Uprobes for Live Debugging"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: ["013", "018"]
database_migrations: []
areas: ["sdk", "ebpf", "debugging", "observability"]
---

# RFD 042 - SDK-Integrated eBPF Uprobes for Live Debugging

**Status:** 🚧 Draft

## Summary

Enable on-demand live debugging of applications via SDK-integrated eBPF uprobes, allowing developers to dynamically attach probes to specific functions without redeployment. The Coral SDK discovers function offsets at runtime and coordinates with the agent to inject eBPF uprobes, providing deep visibility into application behavior for time-limited debugging sessions orchestrated by the colony.

## Problem

**Current behavior/limitations:**

- RFD 013 provides passive eBPF collection (HTTP latency, CPU profiling, syscall stats) but cannot trace application-specific functions.
- Debugging production issues requires either adding extensive logging (redeploy), using SDK profiling (overhead), or ssh + manual debugging (security risk).
- No way to dynamically instrument specific functions like `handleCheckout`, `processPayment`, or `validateCard` without code changes.
- Traditional debuggers (gdb, delve) require stopping the process or significant overhead, unsuitable for production.
- Distributed tracing shows high-level spans but not function-level execution details.

**Why this matters:**

- Production incidents need quick root cause analysis: "Why is `handleCheckout` slow?" requires function-level timing, argument inspection, and call counts.
- Deploying debug builds or adding extensive logging for one-off investigations is wasteful and slow.
- SSH access to production systems violates security policies and creates audit risks.
- AI-driven diagnostics need precise function-level data to make actionable recommendations.

**Use cases affected:**

- Debugging slow checkout transactions: "Attach probe to `handleCheckout`, show latency distribution and arguments for 60 seconds."
- Finding which function in a call chain is slow: "Trace all functions in `/api/checkout` request path for 5 minutes."
- Counting how often error paths execute: "Attach probe to `handlePaymentError`, count calls, capture error codes."
- Validating optimization impact: "Before/after comparison of `validateCard` execution time."

## Solution

Extend Coral with SDK-integrated runtime monitoring that bridges application code with agent eBPF capabilities. The SDK discovers function offsets using runtime reflection and DWARF debug info, then coordinates with the agent to attach uprobes dynamically via colony-orchestrated debugging sessions.

### Key Design Decisions

- **SDK as bridge**: Go SDK embeds in application, exposes function metadata, communicates with local agent.
- **Automatic offset discovery**: SDK uses runtime reflection (Go) and DWARF parsing to find function entry points.
- **Colony orchestration**: CLI sends debug requests to colony, colony coordinates agent + SDK.
- **Time-limited sessions**: All debug attachments expire after duration (default 60s, max 600s).
- **Zero code changes**: SDK integration requires only `coral.EnableRuntimeMonitoring()` call, no instrumentation in business logic.
- **eBPF safety**: Uprobes are read-only (no modification), automatically detach on expiry, enforced resource limits.
- **Language-specific bridges**: Initial Go support via runtime reflection, future: Python (inspect module), Node.js (V8 debugging API).

### Benefits

- **Fast debugging**: Attach probes in seconds without redeployment or SSH.
- **Function-level precision**: See exactly which function is slow, not just endpoint-level metrics.
- **Production-safe**: Time-limited, read-only, minimal overhead (<1% per probe).
- **AI-enhanced troubleshooting**: `coral ask "Why is checkout slow?"` automatically attaches relevant probes and analyzes results.
- **Audit-friendly**: All debug sessions logged with who, what, when, results.

### Architecture Overview

```
┌───────────────────────────────────────────────────────────┐
│ Application Process                                       │
│                                                           │
│  ┌─────────────────────────────────────────────────────┐ │
│  │ Business Logic                                      │ │
│  │                                                     │ │
│  │  func handleCheckout(ctx, req) {                   │ │
│  │    // No instrumentation needed                    │ │
│  │  }                                                  │ │
│  │                                                     │ │
│  │  func processPayment(ctx, amount) {                │ │
│  │    // No instrumentation needed                    │ │
│  │  }                                                  │ │
│  └─────────────────────────────────────────────────────┘ │
│                                                           │
│  ┌─────────────────────────────────────────────────────┐ │
│  │ Coral SDK (imported as library)                    │ │
│  │                                                     │ │
│  │  coral.RegisterService("api", ...)                 │ │
│  │  coral.EnableRuntimeMonitoring() ← runs goroutine  │ │
│  │                                                     │ │
│  │  - Discovers function offsets via reflection       │ │
│  │  - Parses DWARF debug info from binary             │ │
│  │  - Maintains symbol table                          │ │
│  │  - Listens for agent debug requests (gRPC)         │ │
│  │  - Returns offset map to agent                     │ │
│  └─────────────────────────────────────────────────────┘ │
│                         ▲                                 │
│                         │ gRPC (localhost)                │
└─────────────────────────┼─────────────────────────────────┘
                          │
                          ▼
┌───────────────────────────────────────────────────────────┐
│ Coral Agent (sidecar or node-level)                      │
│                                                           │
│  ┌─────────────────────────────────────────────────────┐ │
│  │ Debug Session Manager                               │ │
│  │  - Receives debug requests from colony             │ │
│  │  - Queries SDK for function offsets                │ │
│  │  - Attaches eBPF uprobes to process                │ │
│  │  - Collects events from BPF maps                   │ │
│  │  - Streams results to colony                       │ │
│  │  - Auto-detaches on expiry                         │ │
│  └─────────────────────────────────────────────────────┘ │
│                         │                                 │
│                         ▼                                 │
│  ┌─────────────────────────────────────────────────────┐ │
│  │ eBPF Uprobes (kernel level)                         │ │
│  │                                                     │ │
│  │  uprobe:/proc/1234/exe:0x12af0 {                   │ │
│  │    // handleCheckout entry                         │ │
│  │    record timestamp, args, PID                     │ │
│  │  }                                                  │ │
│  │                                                     │ │
│  │  uretprobe:/proc/1234/exe:0x12af0 {                │ │
│  │    // handleCheckout return                        │ │
│  │    record duration, return value                   │ │
│  │  }                                                  │ │
│  └─────────────────────────────────────────────────────┘ │
└───────────────────────────────────────────────────────────┘
                          │
                          ▼
┌───────────────────────────────────────────────────────────┐
│ Colony (orchestrator)                                     │
│                                                           │
│  - Receives debug requests from CLI                      │
│  - Resolves target agents with SDK capability            │
│  - Sends AttachUprobe RPC to agent(s)                    │
│  - Streams results back to CLI                           │
│  - Stores debug session history in DuckDB                │
│  - Provides data to AI for analysis                      │
└───────────────────────────────────────────────────────────┘
                          ▲
                          │
                          ▼
┌───────────────────────────────────────────────────────────┐
│ CLI / AI                                                  │
│                                                           │
│  $ coral debug attach api --function handleCheckout      │
│  $ coral debug trace api --path "/api/checkout"          │
│  $ coral ask "Why is checkout slow?"                     │
│    → AI automatically attaches probes, analyzes results  │
└───────────────────────────────────────────────────────────┘
```

## Component Changes

### 1. Coral Go SDK (New Package: `github.com/coral-io/coral-go`)

**New SDK for application integration:**

```go
package coral

// RegisterService registers the application with Coral agent.
func RegisterService(name string, opts Options) error

// EnableRuntimeMonitoring starts background goroutine that:
// - Discovers function offsets via runtime reflection
// - Parses DWARF debug info from executable
// - Serves gRPC API for agent queries
// - Handles debug session lifecycle
func EnableRuntimeMonitoring() error

// Options for service registration.
type Options struct {
    Port           int    // Application listen port
    HealthEndpoint string // Health check endpoint
    AgentAddr      string // Agent gRPC address (default: localhost:9091)
    LogLevel       string // SDK log level
}
```

**Function offset discovery:**

- Parse executable DWARF debug info using `debug/dwarf` and `debug/elf`.
- Use runtime reflection to map function names to addresses.
- Build symbol table with function name → offset mapping.
- Handle inlined functions and optimized builds (best-effort).
- Refresh symbol table on SIGHUP (for hot-reload scenarios).

**SDK gRPC API** (served on localhost, agent-only):

```protobuf
service RuntimeMonitoring {
    // Query available functions for uprobe attachment
    rpc ListFunctions(ListFunctionsRequest) returns (ListFunctionsResponse);

    // Get offset for specific function
    rpc GetFunctionOffset(GetFunctionOffsetRequest) returns (GetFunctionOffsetResponse);

    // Notify SDK of active debug session (for logging)
    rpc NotifyDebugSession(NotifyDebugSessionRequest) returns (NotifyDebugSessionResponse);
}

message ListFunctionsRequest {
    string pattern = 1; // Regex pattern to filter functions (e.g., "handle.*")
}

message ListFunctionsResponse {
    repeated FunctionInfo functions = 1;
}

message FunctionInfo {
    string name = 1;           // Full function name (e.g., "main.handleCheckout")
    uint64 offset = 2;         // Offset from executable base
    string file = 3;           // Source file path
    uint32 line = 4;           // Source line number
    repeated string params = 5; // Parameter names (best-effort)
}

message GetFunctionOffsetRequest {
    string function_name = 1; // Exact function name
}

message GetFunctionOffsetResponse {
    uint64 offset = 1;
    bool found = 2;
}

message NotifyDebugSessionRequest {
    string session_id = 1;
    string function_name = 2;
    google.protobuf.Duration duration = 3;
}

message NotifyDebugSessionResponse {}
```

### 2. Agent (Extended - Depends on RFD 013)

**New debug session manager** (`internal/agent/debug/`):

- Manages active debug session lifecycle
- Coordinates with SDK via gRPC to query function offsets
- Attaches/detaches eBPF uprobes using libbpf
- Collects events from BPF perf buffers
- Streams results to colony
- Enforces resource limits (max sessions, duration, memory)
- Auto-detaches probes on session expiry

**eBPF uprobe programs**:

- Entry probe: Captures function entry timestamp, stores in BPF map
- Exit probe: Captures function exit timestamp, calculates duration, emits event
- Compiled to BPF bytecode using libbpf/CO-RE
- Attached to `/proc/<pid>/exe` at function offset
- See Appendix for detailed BPF program examples

**Capability detection** (extends RFD 018):

Agent reports new capability in runtime context:
- `CanDebugUprobes`: SDK-integrated uprobe debugging available
- Requires: DWARF debug symbols in target binary
- Requires: Kernel 4.7+ with uprobe support

### 3. Colony (Extended)

**New RPC endpoints** (`proto/coral/colony/v1/debug.proto`):

```protobuf
service DebugService {
    // Attach uprobe to function in target service
    rpc AttachUprobe(AttachUprobeRequest) returns (stream UprobeEvent);

    // Detach active debug session
    rpc DetachUprobe(DetachUprobeRequest) returns (DetachUprobeResponse);

    // List active debug sessions
    rpc ListDebugSessions(ListDebugSessionsRequest) returns (ListDebugSessionsResponse);

    // Trace all functions in request path
    rpc TraceRequestPath(TraceRequestPathRequest) returns (stream TraceEvent);
}

message AttachUprobeRequest {
    string service_name = 1;        // Target service (e.g., "api")
    string function_name = 2;       // Function to probe (e.g., "handleCheckout")
    google.protobuf.Duration duration = 3; // Session duration (max 600s)
    UprobeConfig config = 4;
}

message UprobeConfig {
    bool capture_args = 1;          // Capture function arguments (best-effort)
    bool capture_return = 2;        // Capture return value (best-effort)
    uint32 sample_rate = 3;         // Sample every Nth call (default: 1 = all)
    map<string, string> filters = 4; // Optional filters (e.g., user_id=123)
}

message UprobeEvent {
    google.protobuf.Timestamp timestamp = 1;
    string agent_id = 2;
    string service_name = 3;
    string function_name = 4;
    UprobeEventType type = 5;       // ENTRY or EXIT
    uint64 duration_ns = 6;         // Function duration (exit events only)
    uint32 pid = 7;
    uint32 tid = 8;
    map<string, string> labels = 9;
}

enum UprobeEventType {
    UPROBE_EVENT_TYPE_UNSPECIFIED = 0;
    UPROBE_EVENT_TYPE_ENTRY = 1;
    UPROBE_EVENT_TYPE_EXIT = 2;
}

message DetachUprobeRequest {
    string session_id = 1;
}

message DetachUprobeResponse {
    bool success = 1;
}

message ListDebugSessionsRequest {
    string service_name = 1; // Optional filter
}

message ListDebugSessionsResponse {
    repeated DebugSession sessions = 1;
}

message DebugSession {
    string session_id = 1;
    string service_name = 2;
    string function_name = 3;
    string agent_id = 4;
    google.protobuf.Timestamp started_at = 5;
    google.protobuf.Timestamp expires_at = 6;
    string status = 7; // "active", "expired", "detached"
    uint64 event_count = 8;
}

message TraceRequestPathRequest {
    string service_name = 1;
    string http_path = 2;           // HTTP path to trace (e.g., "/api/checkout")
    google.protobuf.Duration duration = 3;
}

message TraceEvent {
    google.protobuf.Timestamp timestamp = 1;
    string function_name = 2;
    uint64 duration_ns = 3;
    uint32 depth = 4;               // Call depth for visualization
    string span_id = 5;             // Correlate with distributed tracing
}
```

**DuckDB storage** (debug session history):

```sql
CREATE TABLE debug_sessions (
    session_id VARCHAR PRIMARY KEY,
    service_name VARCHAR NOT NULL,
    function_name VARCHAR NOT NULL,
    agent_id VARCHAR NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    detached_at TIMESTAMPTZ,
    status VARCHAR NOT NULL,
    event_count BIGINT DEFAULT 0,
    requested_by VARCHAR NOT NULL,  -- Audit: who started session
    INDEX idx_debug_sessions_service (service_name, started_at DESC)
);

CREATE TABLE uprobe_events (
    timestamp TIMESTAMPTZ NOT NULL,
    session_id VARCHAR NOT NULL,
    agent_id VARCHAR NOT NULL,
    service_name VARCHAR NOT NULL,
    function_name VARCHAR NOT NULL,
    event_type VARCHAR NOT NULL,    -- 'entry' or 'exit'
    duration_ns BIGINT,             -- NULL for entry events
    pid INTEGER NOT NULL,
    tid INTEGER NOT NULL,
    labels MAP(VARCHAR, VARCHAR),
    PRIMARY KEY (timestamp, session_id, pid, tid)
);

-- Retention: 7 days for debug sessions, 24 hours for events
```

### 4. CLI (New `coral debug` Command Group)

**New CLI commands:**

```bash
# Attach uprobe to specific function
coral debug attach <service> --function <name> --duration <duration>

# Trace all functions in HTTP request path
coral debug trace <service> --path <http-path> --duration <duration>

# List active debug sessions
coral debug list [service]

# Detach debug session
coral debug detach <service> [--session-id <id> | --all]

# Query historical debug session data
coral debug query <service> --function <name> --since <duration>
```

**Example usage:**

```bash
# Attach to handleCheckout function for 60 seconds
$ coral debug attach api --function handleCheckout --duration 60s

🔍 Debug session started (id: dbg-01H...)
📊 Function: main.handleCheckout
⏱️  Duration: 60 seconds
🎯 Target: api-001, api-002 (2 agents)

Collecting events... (Ctrl+C to stop early)

[Live tail of events...]

Function: handleCheckout
  Calls:        342
  P50 duration: 12.4ms
  P95 duration: 45.2ms
  P99 duration: 89.1ms
  Max duration: 234.5ms

Top slow calls:
  1. 234.5ms - user_id=u_12345 (api-001)
  2. 198.3ms - user_id=u_67890 (api-002)
  3. 156.7ms - user_id=u_54321 (api-001)

✓ Session completed. Full data saved to: ./debug-sessions/dbg-01H.../

# Trace entire request path
$ coral debug trace api --path "/api/checkout" --duration 5m

🔍 Tracing /api/checkout for 5 minutes...
📊 Auto-discovering functions in request path...

Discovered call chain:
  handleCheckout (entry)
    → validateCart (12.3ms)
      → checkInventory (8.1ms)
    → processPayment (142.5ms) ← SLOW
      → validateCard (135.2ms) ← SLOW
        → callExternalAPI (130.1ms) ← SLOW
      → recordTransaction (5.8ms)
    → sendConfirmation (23.4ms)

Analysis:
  Total: 178.2ms
  Slowest: callExternalAPI (130.1ms, 73% of total)
  Recommendation: External API is bottleneck

# List active sessions
$ coral debug list

Active Debug Sessions:
┌───────────┬──────────┬─────────────────┬──────────┬───────────┬────────────┐
│ SESSION   │ SERVICE  │ FUNCTION        │ AGENT    │ STARTED   │ EXPIRES    │
├───────────┼──────────┼─────────────────┼──────────┼───────────┼────────────┤
│ dbg-01H.. │ api      │ handleCheckout  │ api-001  │ 2m ago    │ in 58m     │
│ dbg-02K.. │ worker   │ processJob      │ work-001 │ 15s ago   │ in 4m 45s  │
└───────────┴──────────┴─────────────────┴──────────┴───────────┴────────────┘

# Detach specific session
$ coral debug detach api --session-id dbg-01H...

✓ Debug session dbg-01H... detached
  Total events collected: 342
  Data saved to: ./debug-sessions/dbg-01H.../

# Detach all sessions for service
$ coral debug detach api --all

✓ Detached 3 debug sessions for service: api
```

### 5. AI Integration

**Automatic probe attachment via `coral ask`:**

**Example 1: Performance Analysis with Automatic Probe Selection**

```bash
$ coral ask "Why is checkout slow on api service?"

🤖 Analyzing checkout performance...
📊 Attaching debug probes to relevant functions...
   - handleCheckout (entry point)
   - processPayment (likely bottleneck based on metrics)
   - validateCard (payment validation)
⏱️  Collecting data for 30 seconds...

[Progress bar: 30s countdown]

Analysis:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Function Performance:
  handleCheckout: P95 = 245ms (baseline: 80ms) ⚠️  +206%
    └─ processPayment: P95 = 230ms (94% of total) ⚠️
         └─ validateCard: P95 = 225ms (98% of processPayment) ⚠️
              └─ External API call: P95 = 220ms ⚠️

Root Cause:
  External card validation API responding slowly (220ms vs 50ms baseline).
  This accounts for 90% of checkout latency increase.

Evidence:
  - Debug session: dbg-ai-03K... (342 samples)
  - Function call tree: ./evidence/checkout-trace-2025-11-17-14-30.json
  - Duration histogram: ./evidence/checkout-histogram-2025-11-17-14-30.svg

Recommendations:
  1. Check external API health (card validation service)
  2. Consider adding timeout (current: none detected)
  3. Implement circuit breaker for resilience
  4. Add caching for repeat validations (30% cache hit potential)

Next steps:
  Run: coral debug attach api --function validateCard --duration 5m
       (for deeper investigation of validation logic)
```

**Example 2: Database Performance Investigation**

```bash
$ coral ask "Why is the payment API slow?"

🤖 Analyzing payment service metrics...
📊 Current metrics:
   P95 latency: 2.3s (baseline: 150ms) ⚠️  +1433%
   Error rate: 0.2% (normal)

Root cause unclear from metrics. Attaching live probes...

✓ Uprobe attached: payment.ProcessPayment() [offset 0x4a20]
✓ Uprobe attached: payment.ValidateCard() [offset 0x4c80]
✓ Uprobe attached: db.QueryTransactions() [offset 0x3f10]

⏱️  Collecting traces for 30 seconds...
[████████████████████████████████████] 30s

Analysis:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Function Performance Breakdown:
  • ProcessPayment(): 2.1s avg (2,847 calls)
    └─ db.QueryTransactions(): 2.0s avg (95% of time) ⚠️  BOTTLENECK
       └─ Database query plan: Sequential scan (234,891 rows)
       └─ Missing index detected on: transactions.user_id
    └─ ValidateCard(): 12ms avg (normal) ✓

Root Cause Analysis:
  Missing database index causing slow queries in QueryTransactions().
  Every payment lookup performs a full table scan of 234K rows.

  Query pattern detected:
    SELECT * FROM transactions WHERE user_id = ?

  Current: Sequential scan (2.0s avg)
  Expected with index: Index scan (~5ms)

  Performance impact: 400x slower than expected

Immediate Fix:
  CREATE INDEX idx_transactions_user_id ON transactions(user_id);

  Expected improvement: P95 latency 2.3s → 150ms (baseline restored)

Evidence:
  - Debug session: dbg-ai-04M... (2,847 samples)
  - Query execution times: ./evidence/db-queries-2025-11-17-15-00.json
  - Function trace: ./evidence/payment-trace-2025-11-17-15-00.svg

🧹 Detaching probes...
✓ Cleanup complete (zero overhead restored)

Would you like me to:
  1. Generate the index creation SQL script
  2. Analyze other slow queries in the payment service
  3. Create a performance monitoring dashboard
```

**AI Decision Logic:**

The AI analyzes the query and current metrics to determine which functions to probe:

1. **Parse query intent**: Identify service (payment), metric type (latency).
2. **Check baseline metrics**: Compare current vs baseline (2.3s vs 150ms = major regression).
3. **Select candidate functions**:
   - Entry point: `ProcessPayment` (top-level function)
   - Known bottlenecks: `ValidateCard`, `QueryTransactions` (from historical data)
4. **Attach probes**: Request debug session for 30-60s.
5. **Analyze results**: Build call tree, identify slowest leaf function.
6. **Generate recommendations**: Based on function profiling and context.
7. **Cleanup**: Detach probes to restore zero overhead.

## API Changes

### SDK API (Go)

See Component Changes section 1 for full SDK API definition.

### Agent RPC (Extended)

```protobuf
// In agent.proto
service Agent {
    // ... existing RPCs from RFD 016/017

    // NEW: Debug session management
    rpc AttachUprobe(AttachUprobeRequest) returns (stream UprobeEvent);
    rpc DetachUprobe(DetachUprobeRequest) returns (DetachUprobeResponse);
}

message AttachUprobeRequest {
    string function_name = 1;
    google.protobuf.Duration duration = 2;
    UprobeConfig config = 3;
}
```

### Colony RPC

See Component Changes section 3 for full Colony API definition.

### CLI Commands

```bash
coral debug attach <service> --function <name> [--duration <dur>] [--sample-rate <N>]
coral debug trace <service> --path <http-path> [--duration <dur>]
coral debug list [service]
coral debug detach <service> [--session-id <id> | --all]
coral debug query <service> --function <name> [--since <dur>]
```

## Configuration Changes

### SDK Configuration (Application)

```go
// In application code
import "github.com/coral-io/coral-go"

func main() {
    // Register service with Coral
    coral.RegisterService("api", coral.Options{
        Port:           8080,
        HealthEndpoint: "/health",
        AgentAddr:      "localhost:9091", // Default
    })

    // Enable runtime monitoring (starts background goroutine)
    coral.EnableRuntimeMonitoring()

    // ... application code
}
```

### Agent Configuration

```yaml
# agent-config.yaml
agent:
    debug:
        enabled: true

        # SDK communication
        sdk_api:
            listen_addr: "127.0.0.1:9092"  # SDK queries this
            timeout: 5s

        # Uprobe limits (safety)
        limits:
            max_concurrent_sessions: 5
            max_session_duration: 600s      # 10 minutes hard limit
            max_events_per_second: 10000
            max_memory_mb: 256

        # Require debug info
        require_dwarf: true  # Reject if binary has no debug symbols
```

### Colony Configuration

```yaml
# colony-config.yaml
colony:
    debug:
        enabled: true

        # Session management
        sessions:
            default_duration: 60s
            max_duration: 600s
            auto_cleanup_after: 24h  # Clean up expired sessions

        # Storage retention
        storage:
            events_retention: 24h
            sessions_retention: 7d

        # AI integration
        ai:
            auto_attach_probes: true  # Let AI attach probes automatically
            max_probes_per_query: 5   # Limit AI to 5 functions per query
```

## Implementation Plan

### Phase 1: SDK Foundation

- [ ] Create `coral-go` SDK repository
- [ ] Implement `RegisterService` and `EnableRuntimeMonitoring`
- [ ] DWARF debug info parser (using `debug/dwarf`, `debug/elf`)
- [ ] Runtime reflection for function offset discovery
- [ ] SDK gRPC server for agent queries
- [ ] Symbol table builder and cache
- [ ] Unit tests for offset discovery

### Phase 2: Agent Integration

- [ ] Debug session manager implementation
- [ ] eBPF uprobe programs (C code, compiled to BPF bytecode)
- [ ] Uprobe attach/detach logic using libbpf
- [ ] Event collection from BPF perf buffers
- [ ] SDK client in agent (queries SDK for offsets)
- [ ] Auto-detach on session expiry
- [ ] Resource limit enforcement
- [ ] Unit and integration tests

### Phase 3: Colony Orchestration

- [ ] Implement DebugService RPC handlers
- [ ] Route debug requests to agents with SDK capability
- [ ] Stream events from agent to CLI
- [ ] DuckDB schema for debug sessions and events
- [ ] Session lifecycle management (expiry, cleanup)
- [ ] Audit logging for debug sessions
- [ ] Unit tests for orchestration logic

### Phase 4: CLI Commands

- [ ] `coral debug attach` command
- [ ] `coral debug trace` command (request path tracing)
- [ ] `coral debug list` command
- [ ] `coral debug detach` command
- [ ] `coral debug query` command (historical data)
- [ ] Live event streaming UI
- [ ] Export to JSON/CSV/SVG formats

### Phase 5: AI Integration

- [ ] Pattern matching for debug-related queries
- [ ] Automatic function selection based on metrics
- [ ] Auto-attach probes on AI queries
- [ ] Analysis of debug session results
- [ ] Recommendation generation
- [ ] Evidence packaging (traces, histograms)

### Phase 6: Testing & Documentation

- [ ] E2E tests with sample Go application
- [ ] Performance testing (overhead measurement)
- [ ] Security testing (uprobe safety, resource limits)
- [ ] SDK documentation and examples
- [ ] CLI user guide
- [ ] Troubleshooting guide
- [ ] Video demos

## Testing Strategy

### Unit Tests

- **SDK**: Function offset discovery, DWARF parsing, symbol table building
- **Agent**: Uprobe attach/detach, event collection, session management
- **Colony**: RPC handlers, session routing, storage

### Integration Tests

- **SDK + Agent**: Full debug session lifecycle
- **Agent + Colony**: Event streaming, session expiry
- **CLI + Colony**: All debug commands

### E2E Tests

- **Sample application**: Go web service with debug-enabled SDK
- **Attach scenario**: Attach probe, verify events collected
- **Trace scenario**: Trace request path, verify call chain
- **AI scenario**: `coral ask` triggers auto-probe, verify analysis
- **Stress test**: Multiple concurrent sessions, verify resource limits
- **Security test**: Verify read-only, no process modification

### Performance Tests

**Overhead measurement:**

- Baseline: Application without SDK
- SDK only: Application with SDK, no active probes
- Active probes: 1, 3, 5 concurrent uprobe sessions
- Metrics: CPU overhead, memory overhead, latency impact

**Target overhead:**

- SDK (idle): <0.1% CPU, <10 MB memory
- SDK (1 probe): <0.5% CPU, <20 MB memory
- SDK (5 probes): <2% CPU, <100 MB memory

## Security Considerations

### Uprobe Safety

- **Read-only**: Uprobes only observe, never modify process memory.
- **Kernel-level safety**: eBPF verifier ensures programs are safe.
- **Time-limited**: All sessions expire automatically.
- **Resource limits**: CPU, memory, event rate limits enforced.

### Authentication & Authorization

- **CLI authentication**: Requires valid Coral credentials.
- **RBAC**: Separate permission for debug operations (`debug:attach`, `debug:detach`).
- **Audit logging**: All debug sessions logged with user identity.
- **Approval workflow (future)**: Require manager approval for production debug sessions.

### Data Privacy

- **No PII capture by default**: Arguments/return values not captured unless explicitly enabled.
- **Sampling**: High sample rates (e.g., 1 in 100) reduce data volume.
- **Short retention**: Events retained for 24 hours only.
- **Access controls**: Debug data requires same permissions as service access.

### Debug Info Requirement

- **Require DWARF symbols**: Agent rejects binaries without debug info.
- **Symbol validation**: Verify function offsets before attachment.
- **Fallback**: If no debug info, SDK reports error, session fails gracefully.

## Migration Strategy

### Rollout

1. **Deploy SDK**: Add `coral-go` dependency to applications, deploy with `EnableRuntimeMonitoring()`.
2. **Deploy agent**: Update agents with debug session manager (backward compatible).
3. **Deploy colony**: Update colony with DebugService RPC handlers.
4. **Deploy CLI**: Update CLI with `coral debug` commands.
5. **Enable AI**: Configure AI to use debug capabilities.

### Backward Compatibility

- **Agents without SDK capability**: CLI shows clear error ("service does not have SDK integration").
- **Applications without SDK**: Agent detects missing SDK gRPC server, reports capability as unavailable.
- **Legacy agents**: Colony filters agents by `CanDebugUprobes` capability.

## Future Enhancements

### Multi-Language Support

- **Python**: Use `inspect` module + DWARF for offset discovery.
- **Node.js**: Use V8 debugging API for function interception.
- **Rust**: Similar to Go (DWARF + runtime reflection).
- **Java**: Experimental support via JVMTI + async-profiler integration.

### Advanced Debugging Features

- **Conditional breakpoints**: Attach probe only when condition met (e.g., `user_id == "u_12345"`).
- **Argument capture**: Capture function arguments (requires parsing function signatures).
- **Return value capture**: Capture return values for analysis.
- **Call stacks**: Capture full call stacks for each uprobe hit.
- **Watchpoints**: Track when specific variables are read/written (complex, low priority).

### Distributed Tracing Integration

- **Span correlation**: Link uprobe events to distributed trace spans.
- **Trace-driven debugging**: Attach probes to slow spans automatically.
- **Flamegraph generation**: Build flamegraphs from uprobe data.

### IDE Integration

- **VSCode extension**: Attach probes directly from IDE.
- **Breakpoint sync**: Set breakpoint in IDE, attach uprobe in production.
- **Live debugging**: Stream uprobe events to IDE debugging console.

## Appendix

### Example: Function Offset Discovery (Go)

```go
// SDK internal implementation
func discoverFunctionOffsets() (map[string]uint64, error) {
    // 1. Get executable path
    exePath, err := os.Executable()
    if err != nil {
        return nil, err
    }

    // 2. Open ELF file
    elfFile, err := elf.Open(exePath)
    if err != nil {
        return nil, err
    }
    defer elfFile.Close()

    // 3. Parse DWARF debug info
    dwarfData, err := elfFile.DWARF()
    if err != nil {
        return nil, fmt.Errorf("no debug symbols: %w", err)
    }

    offsets := make(map[string]uint64)

    // 4. Iterate over compilation units
    reader := dwarfData.Reader()
    for {
        entry, err := reader.Next()
        if entry == nil || err != nil {
            break
        }

        // 5. Find function entries
        if entry.Tag == dwarf.TagSubprogram {
            name := entry.Val(dwarf.AttrName).(string)
            lowPC := entry.Val(dwarf.AttrLowpc).(uint64)

            offsets[name] = lowPC
        }
    }

    return offsets, nil
}
```

### Example: eBPF Uprobe Program (Simplified)

```c
// uprobe_monitor.bpf.c
#include <linux/bpf.h>
#include <bpf/bpf_helpers.h>

struct uprobe_event {
    __u64 timestamp;
    __u32 pid;
    __u32 tid;
    __u64 duration_ns;
};

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u64);
    __type(value, __u64);
    __uint(max_entries, 10240);
} start_times SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_PERF_EVENT_ARRAY);
} events SEC(".maps");

SEC("uprobe")
int probe_entry(struct pt_regs *ctx) {
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    __u64 ts = bpf_ktime_get_ns();

    bpf_map_update_elem(&start_times, &pid_tgid, &ts, BPF_ANY);
    return 0;
}

SEC("uretprobe")
int probe_exit(struct pt_regs *ctx) {
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    __u64 end_ts = bpf_ktime_get_ns();
    __u64 *start_ts = bpf_map_lookup_elem(&start_times, &pid_tgid);

    if (start_ts) {
        struct uprobe_event event = {
            .timestamp = end_ts,
            .pid = pid_tgid >> 32,
            .tid = (__u32)pid_tgid,
            .duration_ns = end_ts - *start_ts,
        };

        bpf_perf_event_output(ctx, &events, BPF_F_CURRENT_CPU,
                             &event, sizeof(event));
        bpf_map_delete_elem(&start_times, &pid_tgid);
    }

    return 0;
}

char LICENSE[] SEC("license") = "GPL";
```

### Example: SDK Usage in Application

```go
// main.go - Sample application with Coral SDK
package main

import (
    "context"
    "log"
    "net/http"
    "time"

    "github.com/coral-io/coral-go"
)

func main() {
    // Register with Coral
    coral.RegisterService("checkout-api", coral.Options{
        Port:           8080,
        HealthEndpoint: "/health",
    })

    // Enable runtime monitoring (starts background goroutine)
    coral.EnableRuntimeMonitoring()

    // Set up HTTP handlers
    http.HandleFunc("/api/checkout", handleCheckout)
    http.HandleFunc("/health", healthCheck)

    // Start server
    log.Println("Starting server on :8080")
    log.Fatal(http.ListenAndServe(":8080", nil))
}

// Business logic - no instrumentation needed!
func handleCheckout(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()

    // Validate cart
    if err := validateCart(ctx); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    // Process payment
    if err := processPayment(ctx); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    w.WriteHeader(http.StatusOK)
    w.Write([]byte("OK"))
}

func validateCart(ctx context.Context) error {
    time.Sleep(10 * time.Millisecond) // Simulated work
    return nil
}

func processPayment(ctx context.Context) error {
    time.Sleep(50 * time.Millisecond) // Simulated external API call
    return nil
}

func healthCheck(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
}
```

Usage:

```bash
# Build with debug symbols (required for uprobes)
$ go build -gcflags="all=-N -l" -o checkout-api main.go

# Run application
$ ./checkout-api
Starting server on :8080
[Coral SDK] Runtime monitoring enabled
[Coral SDK] Discovered 3 functions: handleCheckout, validateCart, processPayment

# In another terminal, attach probe
$ coral debug attach checkout-api --function handleCheckout --duration 60s

🔍 Debug session started
📊 Function: main.handleCheckout
⏱️  Collecting for 60 seconds...

[Real-time stats appear as requests come in]
```

## Dependencies

- **RFD 013**: eBPF-Based Application Introspection (provides eBPF infrastructure)
- **RFD 018**: Agent Runtime Context Reporting (capability detection)

## References

- Go DWARF documentation: https://pkg.go.dev/debug/dwarf
- eBPF uprobes: https://www.kernel.org/doc/html/latest/trace/uprobetracer.html
- libbpf: https://github.com/libbpf/libbpf
- BCC uprobes: https://github.com/iovisor/bcc/blob/master/docs/reference_guide.md#4-uprobes
