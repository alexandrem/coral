# Live Debugging: The Killer Feature (in development)

**Coral can debug your running code without redeploying.**

Unlike traditional observability (metrics, logs, traces), Coral can **actively
instrument** your code on-demand using eBPF uprobes.

Coral also supports **agentless binary scanning** - you can debug
applications **without SDK integration** (if the binary has debug symbols).

> **Production Note:** Most production Go binaries use `-ldflags="-w -s"` to
> fully strip debug symbols. For these binaries, **SDK integration is required
**.
> Agentless mode is best for development builds and legacy apps with symbols.

## How It Works

Coral supports two modes: **with SDK** (required for production) and **agentless
** (for dev/legacy):

### With SDK Integration

1. **SDK Integration**: `sdk.EnableRuntimeMonitoring()` starts a debug server
   that exposes function metadata

2. **Fast Discovery**: Agent fetches function list via HTTP (~1-2s for 50k
   functions)

3. **On-Demand Probes**: When debugging is needed, the agent attaches eBPF
   uprobes to function entry points

4. **Live Data Collection**: Capture function calls, arguments, execution time,
   call stacks

5. **LLM Orchestration**: The AI decides which functions to probe based on
   metrics analysis

6. **Zero Standing Overhead**: Probes only exist during debugging sessions

### Agentless Mode (No SDK Required)

For legacy apps or binaries where SDK integration isn't possible:

1. **Binary Discovery**: Agent discovers services via process monitoring or
   `coral connect`

2. **DWARF Parsing**: Agent scans binary directly, extracting function
   metadata (~100-200ms)

3. **Semantic Indexing**: Functions are indexed with AI embeddings for
   intelligent search

4. **Uprobe Attachment**: Works identically to SDK mode once functions are
   discovered

5. **Symbol Table Fallback**: If DWARF is stripped, falls back to ELF symbol
   table (same as SDK!)

**Discovery Priority:**

- Priority 1: SDK HTTP API (recommended - optimized bulk export)
- Priority 2: Binary Scanner with symbol table fallback (works with `-w`
  stripped binaries!)
- Priority 3: Direct DWARF parsing (legacy)

**Why SDK is still easier:**

- One line of code vs deploying agent
- Optimized HTTP export endpoint
- Both modes equally robust for discovery

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

## Agentless Binary Scanning Example

Debug a legacy app **without any code changes**:

```bash
# Connect to running process
$ coral connect legacy-app

🔍 Discovering functions via binary scanner...
   ✓ Parsed DWARF symbols (142ms)
   ✓ Indexed 8,347 functions with embeddings
   ✓ Ready for debugging!

# Semantic search works identically
$ coral debug search --service legacy-app database
Found 12 functions matching "database":
  • executeSlowQuery (db/queries.go:145)
  • processTransaction (db/tx.go:89)
  • saveToDatabase (storage/writer.go:234)
  ...

# Attach uprobe - no redeployment needed!
$ coral debug attach legacy-app --function executeSlowQuery
✓ Uprobe attached at offset 0x4a2c0
```

**Requirements:**

- Binary must have **symbols** (DWARF preferred, `-w` stripped works via symbol
  table)
- Agent must have access to binary (same host or namespace)
- **Does NOT work with fully stripped binaries** (`-w -s` - typical production
  builds)

**When agentless works best:**

- **Legacy applications you can't modify**
- Development/debug builds with full symbols
- Rare production binaries built with `-w` only (keeps symbols)

**When SDK is required:**

- **Production deployments** (most use `-w -s` fully stripped binaries)
- Binaries where you control the build and can integrate SDK
- SDK provides metadata API that works even with `-w -s` stripped binaries

## Why This Is Different

| Traditional Tools                     | Coral                                             |
|---------------------------------------|---------------------------------------------------|
| Pre-defined metrics only              | On-demand code instrumentation                    |
| Add logging → redeploy → wait         | Attach probes → get data → detach                 |
| Always-on overhead                    | Zero overhead when not debugging                  |
| Single-process debuggers (delve, gdb) | Distributed debugging across mesh                 |
| Manual investigation                  | LLM orchestrates where to probe                   |
| **Requires code changes**             | **SDK mode or agentless (if binary has symbols)** |

## MCP Integration

The live debugging capability is exposed as MCP tools, so any AI assistant (
Claude Desktop, Cursor, etc.) can trigger debugging sessions:

```json
{
    "tool": "coral_profile_functions",
    "arguments": {
        "service": "payment",
        "query": "checkout",
        "duration": "60s"
    }
}
```
