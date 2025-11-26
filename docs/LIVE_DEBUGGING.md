# Live Debugging: The Killer Feature (in development)

**Coral can debug your running code without redeploying.**

Unlike traditional observability (metrics, logs, traces), Coral can **actively
instrument** your code on-demand using eBPF uprobes:

## How It Works

1. **SDK Integration**: `coral.EnableRuntimeMonitoring()` launches a goroutine
   that bridges with the agent's eBPF subsystem

2. **On-Demand Probes**: When debugging is needed, the agent attaches eBPF
   uprobes to function entry points in your running process

3. **Live Data Collection**: Capture function calls, arguments, execution time,
   call stacks - all without modifying your code

4. **LLM Orchestration**: The AI decides which functions to probe based on
   metrics analysis. Attach probes → collect data → analyze → detach

5. **Zero Standing Overhead**: Probes only exist during debugging sessions. No
   always-on instrumentation tax.

## Example: LLM-Orchestrated Debugging

```bash
$ coral ask "Why is the payment API slow?"

🤖 Analyzing payment service metrics...
   P95 latency: 2.3s (baseline: 150ms)

   Root cause unclear from metrics. Attaching live probes...

   ✓ Uprobe attached: payment.ProcessPayment() [offset 0x4a20]
   ✓ Uprobe attached: payment.ValidateCard() [offset 0x4c80]
   ✓ Uprobe attached: db.QueryTransactions() [offset 0x3f10]

   Collecting traces for 30 seconds...

   Analysis:
     • ProcessPayment(): 2.1s avg (2,847 calls)
       └─ db.QueryTransactions(): 2.0s (95% of time)
          └─ Query plan: Sequential scan (234,891 rows)
          └─ Missing index on transactions.user_id

     • ValidateCard(): 12ms avg (normal)

   Root Cause: Missing database index causing slow queries

   Recommendation:
     CREATE INDEX idx_transactions_user_id ON transactions(user_id);

   Detaching probes...
   ✓ Cleanup complete (zero overhead restored)
```

## Why This Is Different

| Traditional Tools | Coral |
|-------------------|-------|
| Pre-defined metrics only | On-demand code instrumentation |
| Add logging → redeploy → wait | Attach probes → get data → detach |
| Always-on overhead | Zero overhead when not debugging |
| Single-process debuggers (delve, gdb) | Distributed debugging across mesh |
| Manual investigation | LLM orchestrates where to probe |

## MCP Integration

The live debugging capability is exposed as MCP tools, so any AI assistant (
Claude Desktop, Cursor, etc.) can trigger debugging sessions:

```json
{
  "tool": "coral_debug_attach",
  "arguments": {
    "service": "payment",
    "function": "ProcessPayment",
    "duration": "60s"
  }
}
```
