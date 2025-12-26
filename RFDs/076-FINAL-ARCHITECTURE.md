# RFD 076: Final Architecture - Sandboxed TypeScript Execution

**Status**: Design Finalized
**Last Updated**: 2025-01-XX

## Core Insight

After careful analysis, we've identified the **primary use case** for TypeScript scripting in Coral:

> **Users write TypeScript locally that queries colony DuckDB summaries for custom analysis, dashboards, and integrations.**

Agent-side execution is **secondary** - only for specific high-frequency processing cases (eBPF filtering, real-time sampling).

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│ PRIMARY: CLI-Side Scripting (95% of use cases)              │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  User writes TypeScript locally:                           │
│  ┌───────────────────────────────────────────────────┐     │
│  │ analysis.ts                                       │     │
│  │                                                   │     │
│  │ import * as coral from "@coral/sdk";             │     │
│  │                                                   │     │
│  │ const svcs = await coral.services.list();        │     │
│  │ for (const svc of svcs) {                        │     │
│  │   const p99 = await svc.metrics.getP99(...);     │     │
│  │   if (p99 > threshold) { ... }                   │     │
│  │ }                                                 │     │
│  └───────────────────────────────────────────────────┘     │
│                                                             │
│  $ coral run analysis.ts  ←── Runs via embedded Deno       │
│                                                             │
│  ┌───────────────────────────────────────────────────┐     │
│  │ Coral Binary                                      │     │
│  │  - Go code                                        │     │
│  │  - Embedded Deno (~100MB)                         │     │
│  │  - TypeScript SDK                                 │     │
│  └───────────────────────────────────────────────────┘     │
│                                                             │
│                      │                                      │
│                      ▼ gRPC queries                         │
└──────────────────────┼──────────────────────────────────────┘
                       │
┌──────────────────────▼──────────────────────────────────────┐
│ Colony (Central Data Store)                                 │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌───────────────────────────────────────────────────┐     │
│  │ DuckDB                                            │     │
│  │  - Aggregated metrics (1min rollups)             │     │
│  │  - Trace summaries                                │     │
│  │  - Service topology                               │     │
│  │  - System metrics                                 │     │
│  └───────────────────────────────────────────────────┘     │
│                                                             │
│  ┌───────────────────────────────────────────────────┐     │
│  │ gRPC Query API                                    │     │
│  │  - services.List()                                │     │
│  │  - metrics.GetPercentile()                        │     │
│  │  - traces.FindSlow()                              │     │
│  └───────────────────────────────────────────────────┘     │
│                                                             │
└─────────────────────────────────────────────────────────────┘
                       │
                       ▼ Pulls data from agents
┌─────────────────────────────────────────────────────────────┐
│ SECONDARY: Agent-Side Scripting (5% of use cases)          │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  Only for specific cases:                                  │
│   - High-frequency eBPF filtering (10k+ events/sec)        │
│   - Real-time local sampling                               │
│   - Cases where streaming to colony is impractical         │
│                                                             │
│  ┌───────────────────────────────────────────────────┐     │
│  │ Same Deno Executor                                │     │
│  │  - Runs TypeScript                                │     │
│  │  - Queries local agent DuckDB                     │     │
│  │  - Emits filtered results to colony               │     │
│  └───────────────────────────────────────────────────┘     │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

## Design Decisions

### 1. Embed Deno in Coral Binary

**Decision**: Bundle Deno runtime (~100MB) directly in Coral binary.

**Rationale**:
- ✅ **No external dependencies** - Users don't need to install Deno
- ✅ **Version consistency** - Coral controls exact Deno version
- ✅ **Simpler deployment** - Single binary
- ✅ **Just works** - Better user experience

**Trade-off**:
- ❌ Larger binary size (~100MB additional)
- ✅ Acceptable for modern systems
- ✅ Can use platform-specific builds (Linux, macOS, Windows, ARM)

**Implementation**:
```go
// Embed Deno binary for current platform
//go:embed deno-linux-x64
var denoBinary []byte

func extractDeno() (string, error) {
    // Extract to ~/.coral/bin/deno on first run
    denoPath := filepath.Join(os.UserHomeDir(), ".coral", "bin", "deno")

    if !fileExists(denoPath) {
        os.WriteFile(denoPath, denoBinary, 0755)
    }

    return denoPath, nil
}
```

### 2. CLI-Side as Primary Use Case

**Decision**: Design and document for CLI-side scripting first.

**User Story**:
> "I want to write custom analysis scripts that query my Coral data. I should be able to run them locally without deploying anything."

**Example**:
```bash
# Write TypeScript locally
cat > latency_report.ts <<EOF
import * as coral from "@coral/sdk";

const services = await coral.services.list();
console.log("Service Latency Report\n");

for (const svc of services) {
  const p50 = await svc.metrics.getPercentile("http.duration", 0.50);
  const p99 = await svc.metrics.getPercentile("http.duration", 0.99);
  const errorRate = await svc.metrics.getErrorRate();

  console.log(`${svc.name}:`);
  console.log(`  P50: ${p50/1e6}ms`);
  console.log(`  P99: ${p99/1e6}ms`);
  console.log(`  Errors: ${errorRate*100}%`);
}
EOF

# Run locally (no deployment!)
coral run latency_report.ts
```

**Output**:
```
Service Latency Report

payments:
  P50: 45.2ms
  P99: 892.1ms
  Errors: 0.12%

orders:
  P50: 23.1ms
  P99: 456.3ms
  Errors: 0.03%
```

### 3. Agent-Side for Specific Cases Only

**Decision**: Keep agent-side execution for edge cases requiring local processing.

**When to use agent-side**:
1. **eBPF filtering** - 10k+ events/sec, filter locally before sending to colony
2. **High-frequency sampling** - 100Hz CPU/memory sampling with local buffering
3. **Real-time aggregation** - Cases where streaming raw data is impractical

**Example** (eBPF filtering):
```typescript
// Colony deploys this automatically based on user intent
// User says: "Alert me when ProcessPayment takes >500ms"
// Colony generates and deploys this to agents

for await (const event of trace.uprobe("ProcessPayment")) {
  // Processes 10k events/sec locally
  if (event.durationNs > 500_000_000) {
    // Only send 1% to colony
    await coral.emit("slow_payment", {
      traceId: event.traceId,
      durationMs: event.durationNs / 1e6
    });
  }
}
```

**User never sees this script** - it's generated and managed by colony (or future Reef AI).

### 4. Same Executor, Different Modes

**Decision**: Reuse Deno executor for both CLI and agent.

**CLI Mode**:
```go
executor.Run(&Config{
    Mode: "cli",
    DataSource: "colony",  // Queries colony DuckDB
    ColonyAddr: "colony.local:9090",
    Permissions: []string{
        "--allow-net=colony.local",  // gRPC to colony
        "--allow-read=./",            // Read local files
    },
})
```

**Agent Mode**:
```go
executor.Run(&Config{
    Mode: "agent",
    DataSource: "local",  // Queries local agent DuckDB
    SDKSocket: "/var/run/coral-sdk.sock",
    Permissions: []string{
        "--allow-read=/var/run/coral-sdk.sock",  // UDS only
    },
})
```

### 5. TypeScript SDK Auto-Detection

**Decision**: SDK automatically detects CLI vs agent mode.

```typescript
// @coral/sdk - works in both modes

const mode = Deno.env.get("CORAL_MODE"); // "cli" or "agent"

if (mode === "cli") {
  // Connect to colony gRPC
  const client = new ColonyClient(Deno.env.get("CORAL_COLONY_ADDR"));

  export const metrics = {
    async getPercentile(service, metric, p) {
      return await client.getPercentile(service, metric, p);
    }
  };
} else if (mode === "agent") {
  // Connect to local agent SDK server
  const client = new AgentClient(Deno.env.get("CORAL_SDK_SOCKET"));

  export const metrics = {
    async getPercentile(service, metric, p) {
      return await client.getPercentile(service, metric, p);
    }
  };
}
```

## User Experience

### CLI-Side Scripting (Primary)

**Write**:
```typescript
// analysis.ts
import * as coral from "@coral/sdk";

const payments = await coral.services.get("payments");
const p99 = await payments.metrics.getPercentile("http.duration", 0.99);

if (p99 > 500_000_000) {
  console.log(`⚠️  Payments P99: ${p99/1e6}ms (threshold: 500ms)`);

  // Find slow traces
  const slow = await payments.traces.findSlow(500, 3600_000);
  console.log(`Found ${slow.count} slow traces:`);
  for (const trace of slow.traces.slice(0, 5)) {
    console.log(`  ${trace.traceId}: ${trace.durationNs/1e6}ms`);
  }
}
```

**Run**:
```bash
# No deployment, no configuration
coral run analysis.ts

# Output:
# ⚠️  Payments P99: 892.1ms (threshold: 500ms)
# Found 342 slow traces:
#   trace-abc123: 1523.2ms
#   trace-def456: 1205.8ms
#   ...
```

**Share** (Community Scripts):
```bash
# Search community
coral scripts search "latency analysis"

# Install
coral scripts install latency-analyzer

# Run with parameters
coral run latency-analyzer --threshold 500 --service payments
```

### Agent-Side Scripting (Secondary)

**Invisible to user** - Colony/Reef manages deployment:

```bash
# User intent (natural language or CLI)
coral alert add "notify me when ProcessPayment takes >500ms"

# Colony/Reef:
# 1. Generates eBPF filtering script
# 2. Deploys to payment service agents
# 3. Collects filtered results
# 4. Shows alerts to user

# User sees:
# ✅ Alert configured: slow_payment_calls
# 📊 Monitoring 12 agents across payments service
```

## Implementation Roadmap

### Phase 1: Foundation (DONE)
- ✅ Deno executor
- ✅ Sandboxing and permissions
- ✅ Resource quotas
- ✅ TypeScript SDK types
- ✅ gRPC SDK server (agents)
- ✅ UDS + Protobuf
- ✅ Semantic guardrails

### Phase 2: CLI-Side Scripting (NEXT)
- 🎯 Embed Deno in Coral binary
- 🎯 `coral run` command
- 🎯 Colony gRPC query API
- 🎯 SDK CLI mode (queries colony)
- 🎯 Documentation and examples

### Phase 3: Community & Polish
- 🎯 Community script templates
- 🎯 `coral scripts search/install`
- 🎯 Integration examples (Slack, PagerDuty, etc.)
- 🎯 VS Code extension (autocomplete)

### Phase 4: Advanced (Future)
- ⏳ Reef AI generates agent scripts
- ⏳ Natural language to script compilation
- ⏳ Automatic optimization suggestions

## What We Keep vs What We Change

### ✅ Keep (Already Built)
- Deno executor (`internal/agent/script/executor.go`)
- Sandboxing model
- Resource quotas (dual-TTL, memory, CPU)
- TypeScript type definitions (`pkg/sdk/typescript/types.d.ts`)
- gRPC SDK server (`internal/agent/script/sdk_server_grpc.go`)
- Protobuf schemas
- Semantic SQL guardrails

### 🎯 Add (CLI Focus)
- Embed Deno in Coral binary
- `coral run <script.ts>` command
- Colony gRPC query API
- SDK auto-detection (CLI vs agent mode)
- CLI-focused documentation

### 📝 Clarify (Documentation)
- Agent-side is **secondary** use case
- Primary story: CLI-side scripting
- Agent scripts managed by Colony/Reef, not users
- Examples focus on CLI usage

## Examples

### Example 1: Daily Latency Report
```typescript
// daily_report.ts
import * as coral from "@coral/sdk";

console.log(`Coral Daily Report - ${new Date().toDateString()}\n`);

const services = await coral.services.list();

for (const svc of services) {
  const [p99, errors] = await Promise.all([
    svc.metrics.getPercentile("http.duration", 0.99),
    svc.metrics.getErrorRate(24 * 3600 * 1000), // Last 24h
  ]);

  console.log(`${svc.name}:`);
  console.log(`  P99: ${p99/1e6}ms`);
  console.log(`  Error Rate: ${errors*100}%`);

  if (errors > 0.01) {
    const errorTraces = await svc.traces.findErrors(3600_000, 10);
    console.log(`  Recent errors: ${errorTraces.count}`);
  }
}
```

**Run**:
```bash
# Schedule via cron
0 9 * * * coral run daily_report.ts | mail -s "Coral Report" team@company.com
```

### Example 2: Cross-Service Correlation
```typescript
// correlation.ts
import * as coral from "@coral/sdk";

const payments = await coral.services.get("payments");
const orders = await coral.services.get("orders");

const [paymentsErrors, ordersErrors] = await Promise.all([
  payments.metrics.getErrorRate(300_000), // Last 5min
  orders.metrics.getErrorRate(300_000),
]);

if (paymentsErrors > 0.01 && ordersErrors > 0.01) {
  console.log("🚨 Cascading failure detected!");

  // Find correlated traces
  const paymentsTraces = await payments.traces.findErrors(300_000);

  for (const trace of paymentsTraces.traces.slice(0, 10)) {
    const correlated = await coral.traces.correlate(trace.traceId, ["orders"]);
    if (correlated.relatedTraces.length > 1) {
      console.log(`Correlated: ${trace.traceId}`);
    }
  }
}
```

### Example 3: Slack Integration
```typescript
// slack_alerts.ts
import * as coral from "@coral/sdk";

const WEBHOOK = Deno.env.get("SLACK_WEBHOOK");

const services = await coral.services.list();

for (const svc of services) {
  const p99 = await svc.metrics.getPercentile("http.duration", 0.99);

  if (p99 > 1_000_000_000) { // >1s
    await fetch(WEBHOOK, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        text: `⚠️ ${svc.name} P99 latency: ${p99/1e6}ms`,
      }),
    });
  }
}
```

**Run**:
```bash
# Continuous monitoring
while true; do
  coral run slack_alerts.ts
  sleep 60
done
```

## Binary Size Impact

| Platform | Go Binary | + Deno | Total | Notes |
|----------|-----------|--------|-------|-------|
| Linux x64 | ~40MB | ~100MB | **~140MB** | Compressed: ~45MB |
| macOS x64 | ~40MB | ~100MB | **~140MB** | Compressed: ~45MB |
| macOS ARM | ~40MB | ~95MB | **~135MB** | Compressed: ~43MB |
| Windows x64 | ~40MB | ~105MB | **~145MB** | Compressed: ~47MB |

**Mitigation**:
- Use compression (gzip reduces to ~30% of size)
- Platform-specific downloads
- Optional: `coral-lite` without embedded Deno (users install Deno separately)

## Success Metrics

**Phase 2 Success**:
- [ ] Users can `coral run script.ts` without any setup
- [ ] No external dependencies required
- [ ] Scripts query colony DuckDB summaries
- [ ] Full TypeScript flexibility
- [ ] Community script templates available

## References

- [RFD 076: Original Design](076-sandboxed-typescript-execution.md)
- [RFD 076: Implementation Status](076-IMPLEMENTATION-STATUS.md)
- [RFD 076: Execution Models](076-EXECUTION-MODELS.md)
- [Protobuf Schema](../proto/coral/sdk/v1/sdk.proto)
- [TypeScript Types](../pkg/sdk/typescript/types.d.ts)

---

**Status**: Architecture finalized, ready for Phase 2 implementation (CLI-side scripting).
