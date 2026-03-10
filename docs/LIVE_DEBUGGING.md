# Live Debugging: The Killer Feature (in development)

**Coral can debug your running code without redeploying.**

Unlike traditional observability (metrics, logs, traces), Coral can **actively
instrument** your code on-demand using eBPF uprobes.

Coral also supports **agentless binary scanning** - you can debug
applications **without SDK integration** (if the binary has debug symbols).

> **Production Note:** Most production Go binaries use `-ldflags="-w -s"` to
> fully strip debug symbols. For these binaries, **SDK integration is required
> **.
> Agentless mode is best for development builds and legacy apps with symbols.
>
> **See also**: [Go Function Inlining & Tracing](GO_INLINING_AND_TRACING.md) for
> details on how compiler optimizations affect discovery.

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

## CPU Profiling Requirements

Coral includes continuous and on-demand CPU profiling using eBPF. This requires
**frame pointers** for stack unwinding.

### ARM64 (Apple Silicon, AWS Graviton)

On ARM64, Go does **not** enable frame pointers by default. You must explicitly
enable them:

```bash
# Build with frame pointers for CPU profiling
go build -tags=framepointer -o myapp main.go
```

### AMD64 (x86_64)

Frame pointers are enabled by default on AMD64 (Go 1.7+). No special flags
needed.

### Why Frame Pointers Matter

- **eBPF Limitation**: The eBPF profiler uses `bpf_get_stackid()` for
  kernel-side stack unwinding
- **No DWARF Access**: Unlike userspace profilers (`perf`), eBPF cannot use
  DWARF symbols
- **Frame Pointers Required**: The kernel's BPF stack walker needs frame
  pointers to traverse call stacks

### Symptoms Without Frame Pointers

If you see these symptoms, frame pointers are likely missing:

- CPU profiler returns 0 samples even under heavy load
- Continuous profiling logs show `total_samples=0`
- On-demand profiling succeeds but captures no stack traces

### Platform Matrix

| Platform              | Frame Pointers Default | Build Flag Required  |
|-----------------------|------------------------|----------------------|
| AMD64 (x86_64)        | ✅ Yes (Go 1.7+)        | None                 |
| ARM64 (Apple Silicon) | ❌ No                   | `-tags=framepointer` |
| ARM64 (AWS Graviton)  | ❌ No                   | `-tags=framepointer` |
| ARM32                 | ❌ No                   | `-tags=framepointer` |

### System Requirements

In addition to frame pointers, the host/VM must allow perf events:

```bash
# Check current setting (4 = completely disabled, -1 = enabled)
cat /proc/sys/kernel/perf_event_paranoid

# Enable perf events for eBPF profiling
sudo sysctl -w kernel.perf_event_paranoid=-1

# For Colima users (macOS)
colima ssh -- sudo sysctl -w kernel.perf_event_paranoid=-1
```

**Note**: This setting resets on reboot. For permanent configuration, add to
`/etc/sysctl.conf`:

```bash
kernel.perf_event_paranoid=-1
```

## Return-Instruction Uprobes

Coral measures function call duration using **RET-instruction uprobes** instead
of traditional Linux `uretprobes`. Standard uretprobes work by rewriting the
return address on the stack, which crashes the Go runtime ("unexpected return
pc"). Coral avoids this by attaching individual uprobes directly to every `RET`
instruction inside the function body.

### How Duration is Measured

1. **Entry probe fires** → BPF records `{tgid, stack_ptr} → timestamp_ns`.
2. **One of N return probes fires** → BPF computes `duration_ns = now - entry_ts`
   and emits a `UprobeEvent{event_type="return", duration_ns=...}`.
3. The entry map record is deleted to free memory.

The `(TGID, StackPointer)` key makes the tracking **goroutine-migration-safe**:
Go's scheduler moves goroutines between OS threads frequently (e.g., after
`time.Sleep`), so the thread ID (TID) changes, but the goroutine's stack pointer
stays stable.

### Architecture Support

| Architecture    | RET Detection                 | Status      |
|-----------------|-------------------------------|-------------|
| x86-64 (amd64)  | `0xC3` / `0xC2` opcodes       | ✅ Supported |
| ARM64 (aarch64) | Fixed-width `RET Xn` encoding | ✅ Supported |

Architecture is auto-detected at runtime from the ELF `e_machine` header.

### Limitations

| Limitation                        | Behavior                                                              |
|-----------------------------------|-----------------------------------------------------------------------|
| **Stripped binaries** (`-w -s`)   | No DWARF size info → entry probe only, no duration                    |
| **Tail-call optimized functions** | No `RET` instructions → entry probe only                              |
| **Panics / SIGKILL**              | Stack unwind skips `RET` probes → orphaned entry cleaned up after 60s |
| **Inline assembly**               | Unsupported instructions → disassembly falls back to entry-only       |
| **Fully inlined functions**       | No symbol address → function invisible to Coral                       |

When return probes cannot be attached, the agent logs a warning and falls back
to entry-only mode. Duration fields in events will be zero.

**See also**: [Go Function Inlining & Tracing](GO_INLINING_AND_TRACING.md) for
details on inlining, goroutine migration, and duration tracing edge cases.

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
