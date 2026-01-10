---
rfd: "073"
title: "Return-Instruction Uprobes for Go"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "061", "066" ]
database_migrations: [ ]
areas: [ "agent", "ebpf", "linux" ]
---

# RFD 073 - Return-Instruction Uprobes for Go

**Status:** 🚧 Draft

## Summary

Enable function call duration measurements in Go applications by attaching
uprobes to RET instructions instead of using traditional uretprobes. This solves
the incompatibility between Go's stack management and uretprobe return address
manipulation, allowing Coral to identify slow function calls and performance
bottlenecks. Extends the SDK HTTP API (RFD 066) to include function size
metadata for disassembly.

## Problem

**Current behavior/limitations:**

Currently, Coral's eBPF uprobe mechanism (RFD 061) can only attach to function
entry points in Go applications. Traditional uretprobes, which attach to
function returns by manipulating the return address on the stack, are
incompatible with Go's runtime:

- Go uses a custom calling convention with split stacks and stack copying.
- Go's runtime detects unexpected return addresses and crashes with "unexpected
  return pc" errors.
- Without return probes, we cannot measure function call durations.
- We only capture function entry events, not exit events with timing
  information.

**Why this matters:**

Function call duration is critical for debugging performance issues:

- Developers need to identify which function calls are slow in distributed
  systems.
- Without duration metrics, it's impossible to pinpoint performance bottlenecks.
- The current implementation provides only 50% of the debugging value (entry but
  not exit).
- Users must rely on external tools or manual instrumentation to measure
  durations.

**Use cases affected:**

1. **Performance Debugging**: "Which database calls are taking >100ms?" - Cannot
   answer without durations.
2. **Latency Analysis**: "Why is this request slow?" - Can see functions called
   but not which ones are slow.
3. **SLO Monitoring**: "Is function X meeting its 50ms SLO?" - No duration data
   available.
4. **Regression Detection**: "Did function Y get slower in the new
   deployment?" - Cannot compare durations.

## Solution

**High-level approach:**

Instead of using uretprobes, we disassemble the target function and attach
individual uprobes to every RET instruction. This approach, known as "
Return-Instruction Uprobes" or "RET-based tracing", avoids stack manipulation
entirely:

1. Query SDK for function metadata (offset, size).
2. Disassemble function bytes from binary to find all RET instructions.
3. Attach separate uprobes to each RET instruction offset.
4. Track entry timestamps in BPF map, calculate duration on any RET uprobe hit.
5. Handle multiple return paths correctly (functions can have many RET
   instructions).

**Key Design Decisions:**

- **Disassembly in Agent**: Use Go's `x86asm` package to parse function bytes
  and locate RET instructions. This is safer than external tools and works
  cross-platform.
- **Multiple Uprobe Attachments**: Each RET instruction gets its own uprobe. A
  function with 5 return paths requires 6 uprobe attachments (1 entry + 5
  returns).
- **Shared BPF Program**: All RET uprobes use the same BPF program handler,
  sharing the entry timestamp map.
- **Architecture Support**: Start with x86-64 (amd64), defer ARM64 support to
  future work.

**Benefits:**

- Enables full function call duration measurement in Go applications.
- No runtime crashes or stack corruption issues.
- Works with all Go calling conventions (stack growth, defer, panic/recover).
- Provides complete debugging capability matching non-Go languages.
- Foundation for performance regression detection and SLO monitoring.

**Trade-offs:**

| Approach                   | Pros                               | Cons                                     |
|:---------------------------|:-----------------------------------|:-----------------------------------------|
| Traditional Uretprobes     | Simple, 1 attachment per function  | Crashes Go runtime, unusable             |
| Return-Instruction Uprobes | Safe for Go, full duration metrics | More complex, N attachments per function |
| No Return Probes (Current) | Simple, no crashes                 | No duration metrics, limited value       |

**Architecture Overview:**

```
┌─────────────────────────────────────────────────────────────┐
│ Agent: Uprobe Attachment Flow                               │
└─────────────────────────────────────────────────────────────┘

1. SDK Query (HTTP API - RFD 066):
   Agent → SDK: GET /debug/functions/{function_name}
   SDK → Agent: {name, offset, size_bytes, file, line, ...}

2. Binary Analysis:
   Agent reads /proc/{pid}/exe at offset+size
   Disassemble bytes → Find RET instruction offsets
   Example: Function at 0x4a5000, size 0x120
            RET instructions at: +0x50, +0xa8, +0x118

3. Uprobe Attachment:
   Attach uprobe_entry    → offset + 0x0     (entry)
   Attach uprobe_return_1 → offset + 0x50    (RET #1)
   Attach uprobe_return_2 → offset + 0xa8    (RET #2)
   Attach uprobe_return_3 → offset + 0x118   (RET #3)

4. Runtime Tracing (with stack pointer for recursion safety):
   Entry:  key={pid,tid,sp} → entry_times[key] = ts_ns
   Return: key={pid,tid,sp} → lookup entry_times[key]
           duration = current_ts - entry_ts
           emit event: {duration, timestamp, function}
           delete entry_times[key]

   Cleanup: Periodic sweep removes entries older than 60s
```

### Component Changes

1. **Agent: UprobeCollector** (`internal/agent/ebpf/uprobe_collector.go`):

    - Disassemble function bytes using `golang.org/x/arch/x86/x86asm`.
    - Identify RET instruction offsets within function.
    - Track multiple return probe links per session.
    - Attach N uprobes for N RET instructions found.
    - Implement periodic cleanup of orphaned BPF map entries (60s timeout).
    - Use stack pointer in BPF map key for recursion safety.

2. **SDK: Metadata Provider** (`pkg/sdk/debug/metadata.go`):

    - Calculate function size from DWARF (`DW_AT_high_pc` - `DW_AT_low_pc`).
    - Include `size_bytes` in HTTP JSON response.
    - Return `size_bytes: 0` with `has_size: false` for stripped binaries.

### BPF Program Structure

**BPF Map Definition:**

```c
// Entry timestamp map - key includes stack pointer for recursion safety
struct entry_key {
    u64 pid_tgid;    // Process and thread ID
    u64 stack_ptr;   // Stack pointer (ensures unique key per call frame)
};

struct entry_value {
    u64 timestamp_ns; // Entry time
    u64 created_at;   // For cleanup of orphaned entries
};

// BPF_HASH: entry_times<entry_key, entry_value>
```

**Entry Uprobe BPF Program:**

```c
int uprobe_entry(struct pt_regs *ctx) {
    struct entry_key key = {
        .pid_tgid = bpf_get_current_pid_tgid(),
        .stack_ptr = PT_REGS_SP(ctx)  // Stack pointer ensures unique key
    };

    struct entry_value val = {
        .timestamp_ns = bpf_ktime_get_ns(),
        .created_at = bpf_ktime_get_ns()
    };

    entry_times.update(&key, &val);
    return 0;
}
```

**Return Uprobe BPF Program:**

```c
int uprobe_return(struct pt_regs *ctx) {
    struct entry_key key = {
        .pid_tgid = bpf_get_current_pid_tgid(),
        .stack_ptr = PT_REGS_SP(ctx)
    };

    struct entry_value *val = entry_times.lookup(&key);
    if (val) {
        u64 duration_ns = bpf_ktime_get_ns() - val->timestamp_ns;

        // Emit event to userspace with duration
        struct duration_event evt = {
            .pid_tgid = key.pid_tgid,
            .duration_ns = duration_ns,
            .timestamp_ns = val->timestamp_ns
        };
        events.perf_submit(ctx, &evt, sizeof(evt));

        entry_times.delete(&key);
    }
    return 0;
}
```

**Cleanup Mechanism:**

Agent runs periodic cleanup (every 30s) to remove orphaned entries:

```go
// Pseudocode - implementation in Agent
func cleanupOrphanedEntries() {
now := time.Now().UnixNano()
for key, val := range entryTimesMap {
if now - val.created_at > 60_000_000_000 { // 60 seconds
delete(entryTimesMap, key)
metrics.orphaned_entries_cleaned.Inc()
}
}
}
```

Orphaned entries occur when:

- Process panics and unwinds stack without hitting RET.
- Process is killed (SIGKILL).
- Function enters infinite loop.
- Stack corruption occurs.

## API Changes

### Updated HTTP API: SDK Function Metadata

RFD 066 defines the SDK HTTP API. This RFD extends the
`GET /debug/functions/{name}`
endpoint to include function size metadata.

**Extended Response (RFD 073):**

```json
{
    "name": "github.com/myapp/payments.ProcessPayment",
    "offset": 12345,
    "size_bytes": 288,
    "has_size": true,
    "file": "/app/payments/process.go",
    "line": 42,
    "arguments": [
        ...
    ],
    "returns": [
        ...
    ]
}
```

**New Fields:**

- `size_bytes` (uint64): Function size in bytes from DWARF (`DW_AT_high_pc` -
  `DW_AT_low_pc`). Used for disassembly to locate RET instructions.
- `has_size` (bool): Indicates if size is available. False for stripped binaries
  or when DWARF info is missing.

**Error Handling:**

- Stripped binaries: `"size_bytes": 0, "has_size": false`
- Missing DWARF: `"size_bytes": 0, "has_size": false`
- Agent fallback: Attach entry-only probe when `has_size == false`

### Existing Protobuf Support

The `UprobeEvent` message (`proto/coral/agent/v1/debug.proto`) already supports
duration measurement:

```protobuf
message UprobeEvent {
    google.protobuf.Timestamp timestamp = 1;
    string collector_id = 2;
    string agent_id = 3;
    string service_name = 4;
    string function_name = 5;

    string event_type = 6;     // "entry" or "return"
    uint64 duration_ns = 7;    // Only for "return" events (NEW: will be populated)

    int32 pid = 8;
    int32 tid = 9;

    repeated FunctionArgument args = 10;
    FunctionReturnValue return_value = 11;

    map<string, string> labels = 12;
}
```

**No protobuf changes required.** The `duration_ns` field exists but is
currently unpopulated. This RFD implements the backend to populate it:

- Entry events: `event_type = "entry"`, `duration_ns = 0` (not applicable)
- Return events: `event_type = "return"`,
  `duration_ns = <measured duration in nanoseconds>`

### Configuration Changes

No new configuration required. Existing debug configuration supports this
enhancement transparently.

The Agent will automatically use Return-Instruction Uprobes when:

- Target binary architecture is x86-64.
- Function size metadata is available from SDK (`size_bytes > 0`).
- Disassembly succeeds.

Fallback behavior:

- If `size_bytes` not provided by SDK, attach only entry probe (current
  behavior).
- If disassembly fails, attach only entry probe.
- Log warning: "Could not disassemble function, duration metrics unavailable".

## Implementation Plan

### Phase 1: SDK Function Size Metadata

- [ ] Extend SDK HTTP API response to include `size_bytes` and `has_size`fields.
- [ ] Implement function size calculation from DWARF in SDK metadata provider.
- [ ] Handle stripped binaries gracefully (return `has_size: false`).
- [ ] Update `GET /debug/functions/{name}` handler to return size.
- [ ] Update `GET /debug/functions/export` to include size in bulk export.
- [ ] Test with sample Go functions of varying sizes and stripped binaries.

### Phase 2: BPF Program Enhancement

- [ ] Update BPF map key to include stack pointer (`pid_tgid` + `stack_ptr`).
- [ ] Add `created_at` field to BPF map value for cleanup.
- [ ] Update entry uprobe BPF program to use new key structure.
- [ ] Update return uprobe BPF program to use new key structure.
- [ ] Test recursion: verify nested calls have independent durations.

### Phase 3: Binary Disassembly

- [ ] Add dependency: `golang.org/x/arch/x86/x86asm`.
- [ ] Implement architecture abstraction interface (`Disassembler`).
- [ ] Implement x86-64 disassembler with RET instruction detection.
- [ ] Add unit tests for disassembly with known function binaries.
- [ ] Handle disassembly errors gracefully (log and fallback to entry-only).
- [ ] Test with tail-call optimized functions (no RET instructions).

### Phase 4: Multiple Uprobe Attachment

- [ ] Refactor `UprobeCollector` to track multiple return links per session.
- [ ] Modify `Start()` to attach N return probes based on disassembly.
- [ ] Update `Stop()` to close all return probe links.
- [ ] Test with functions having 1, 3, 5+ return paths.

### Phase 5: Cleanup & Metrics

- [ ] Implement periodic cleanup goroutine (every 30s, 60s timeout).
- [ ] Add metrics: `uprobe_ret_instructions_total{function}`.
- [ ] Add metrics: `uprobe_duration_seconds{function}` (histogram).
- [ ] Add metrics: `uprobe_errors_total{function, error_type}`.
- [ ] Add metrics: `uprobe_active_entries` (gauge).
- [ ] Add metrics: `uprobe_orphaned_entries_cleaned_total` (counter).
- [ ] Add logging for number of RET instructions found per function.

### Phase 6: Performance & Integration Testing

- [ ] Populate `duration_ns` field in return events (protobuf field already
  exists).
- [ ] Add test helper assertions for duration verification in
  `tests/e2e/distributed/helpers/assertions.go`.
- [ ] Add `TestUprobeReturnTracing` to E2E test suite (
  `tests/e2e/distributed/debug_test.go`).
- [ ] Test return probe attachment to `main.ValidateCard` function (multiple
  return paths).
- [ ] Verify duration events emitted for all return paths (error + success).
- [ ] Test return probe attachment to `main.ProcessPayment` (simple return
  path).
- [ ] Test recursive functions (nested calls have correct durations).
- [ ] Test concurrent goroutines calling same function.
- [ ] Microbenchmark: Measure overhead with `perf` on high-frequency functions.
- [ ] Performance test: Measure overhead with 5+ return probes per function.
- [ ] Validate correctness: Compare durations with manual timing (±5%).
- [ ] Test edge cases: panic/recover, goroutine switches, inline assembly.
- [ ] Add test for orphaned entry cleanup (trigger panic, verify cleanup after
  60s).

### Phase 7: Documentation

- [ ] Update debug documentation with RET-uprobe details.
- [ ] Document Limitations section (x86-64 only, tail calls, panics).
- [ ] Document BPF map key structure and cleanup mechanism.
- [ ] Add architecture diagram to documentation.

## Testing Strategy

### Unit Tests

- Verify correct RET offsets for functions with varying return paths.
- Test error handling for invalid instruction sequences.
- Test edge cases: tail-call optimized functions, functions with no RET.

### Integration Tests

**E2E Test Suite** (`tests/e2e/distributed/debug_test.go`)

Extend existing `DebugSuite.TestUprobeTracing` to verify return-instruction
uprobes.

**Test Application:** SDK app (`tests/e2e/distributed/fixtures/apps/sdk-app/`)
provides instrumented functions:

- `main.ProcessPayment(amount, currency)` - 50ms duration, simple return path
- `main.ValidateCard(cardNumber)` - 20ms duration, multiple return paths (
  error + success)
- `main.CalculateTotal(subtotal, taxRate)` - 10ms duration, single return path

**New Test:** `TestUprobeReturnTracing`

1. **Setup:**
    - Start colony, agent-1, and SDK app (already running in fixture)
    - SDK app auto-connects to agent-1 on startup
    - Get agent ID from colony registry

2. **Attach Return Probes:**
    - Attach uprobe to `main.ValidateCard` function (30s duration)
    - Colony queries SDK for function metadata (offset, size_bytes)
    - Agent disassembles function to find RET instructions
    - Agent attaches entry probe + N return probes (one per RET instruction)

3. **Trigger Workload:**
    - Call `/trigger` endpoint 10 times (triggers ValidateCard internally)
    - Each call takes ~20ms, generates both entry and return events

4. **Verify Events:**
    - Query uprobe events via `QueryUprobeEvents` API
    - Verify entry events captured (existing behavior)
    - **NEW:** Verify return events captured with duration field
    - Duration should be ~20ms ± 5ms (sleep time in ValidateCard)
    - Verify both error and success return paths emit events

5. **Verify Multi-Return Paths:**
    - Trigger ValidateCard with invalid card (short number) → error return path
    - Trigger ValidateCard with valid card → success return path
    - Verify both return paths emit duration events

6. **Cleanup:**
    - Detach uprobe session
    - Verify cleanup removed all return probe links

**Test Infrastructure Updates:**

- Populate `duration_ns` field in return events (protobuf field already exists)
- Add helper assertions in `tests/e2e/distributed/helpers/assertions.go`:
    - `AssertReturnEventDuration(event, expectedMs, toleranceMs)`
    - `AssertEntryReturnPaired(entryEvent, returnEvent)`
- Verify `QueryUprobeEvents` API returns populated duration for return events

**Performance Overhead:**

**Measurement Methodology:**

- Use `perf stat` to measure CPU overhead on traced process.
- Benchmark with high-frequency functions (10k calls/sec).
- Compare baseline (no probes) vs. entry-only vs. entry+return probes.
- Measure P50, P95, P99 latency impact with histogram metrics.

**Expected Overhead:**

| Scenario                  | RET Probes Attached        | Expected CPU Overhead | Expected Latency Impact |
|:--------------------------|:---------------------------|:----------------------|:------------------------|
| Simple function (1 RET)   | 2 total (1 entry + 1 RET)  | < 0.5%                | < 1%                    |
| Complex function (5 RETs) | 6 total (1 entry + 5 RETs) | < 2%                  | < 5%                    |
| 3 concurrent sessions     | 18 total (3 × 6)           | < 4%                  | < 10%                   |

Note: These are estimates. Actual measurements will be performed in Phase 6.

### Edge Cases

- **No RET Instructions**: Tail-call optimized function (calls another function
  and reuses stack frame). Disassembly finds zero RET instructions. Fallback:
  attach entry-only probe, log warning.
- **Inline Assembly**: Function with inline `asm` block. Disassembly still works
  if valid x86-64 instructions. Invalid instructions: fallback to entry-only.
- **Panic/Recover**: Go panic unwinds stack, may not hit RET. Duration event not
  emitted. Entry cleaned up by periodic sweep (acceptable).
- **Goroutine Switch**: Function blocks and switches goroutine. Entry and return
  on different CPUs. BPF map keyed by stack pointer handles this correctly.
- **Recursive Calls**: Function calls itself. Stack pointer in key ensures each
  invocation has unique entry. Inner calls complete first (LIFO order).
- **Stripped Binaries**: DWARF info missing. SDK returns `has_size: false`.
  Agent
  attaches entry-only probe and logs warning.

## Security Considerations

**No Additional Risks:**

Return-Instruction Uprobes use the same uprobe mechanism as entry probes (RFD
061). All existing security properties apply:

- Probes are read-only (cannot modify process state).
- Resource limits enforced (max sessions, duration, event rate).
- CAP_BPF capability required.
- Auto-detach on session expiry.

**New Considerations:**

- **Disassembly on Agent**: Agent reads function bytes from `/proc/{pid}/exe`.
  This is already required for uprobe attachment (no new risk).
- **Increased Attachments**: More uprobes per function (N RETs instead of 1).
  Resource limits still enforced (max 5 concurrent sessions).

## Migration Strategy

**Backward Compatibility:**

This is a **non-breaking enhancement**:

1. Existing debug sessions continue to work (entry-only probes).
2. Once SDK provides `size_bytes`, Agent automatically enables RET-uprobes.
3. No configuration changes required.
4. No database migrations.

**Deployment Steps:**

1. Deploy updated SDK with function size metadata (`size_bytes`, `has_size`
   fields).
2. Deploy updated Agent with BPF program changes and disassembly logic.
3. Existing sessions unaffected (graceful upgrade).
4. New sessions automatically gain duration metrics when `has_size == true`.

**Gradual Rollout:**

1. **Phase 1**: Enable for internal testing only (feature flag or allowlist).
2. **Phase 2**: Opt-in for specific functions based on user configuration.
3. **Phase 3**: Enabled by default for all functions with size metadata.
4. **Monitoring**: Track metrics for error rates, orphaned entries, performance
   impact.

**Rollback Plan:**

If issues arise:

1. Revert Agent to previous version (entry-only probes).
2. No data loss (duration metrics simply stop being emitted).
3. No configuration rollback needed.

## Limitations

**Architecture Support:**

- **x86-64 only**: ARM64 support deferred to future RFD (see Future Work).
- Disassembly uses `golang.org/x/arch/x86/x86asm`.

**Function Coverage:**

- **Tail-call optimized functions**: No RET instructions, cannot measure
  duration.
- **Stripped binaries**: No DWARF debug info, cannot determine function size.
  Falls back to entry-only probes.

**Runtime Behavior:**

- **Panics**: Stack unwinding via panic won't trigger RET probes. Entry cleaned
  up after 60s timeout.
- **Infinite loops**: Function never returns. Entry cleaned up after 60s
  timeout.
- **Process termination**: SIGKILL or crash prevents cleanup. Entries removed on
  session end.

**Performance:**

- Debug overhead increases with number of RET instructions (more uprobes).
- Recommended for debugging, not production monitoring (use sampling for
  production - future RFD).

## Future Work

**ARM64 Support** (Future - RFD TBD)

- Extend disassembly to ARM64 architecture.
- Use `golang.org/x/arch/arm64/arm64asm`.
- Detect RET instruction equivalent (`ret`, `eret`).
- Implement ARM64-specific disassembler behind architecture abstraction
  interface.

**Disassembly Caching** (Future - Performance Optimization)

- Cache disassembly results per function to reduce overhead.
- Key: `{binary_path, offset, size_bytes}`, Value: `[]uint64` (RET offsets).
- Invalidate cache on binary modification (compare mtime).
- Reduces repeated disassembly across multiple debug sessions.

**Production Sampling** (Future - RFD TBD)

- Sample-based duration measurement for production environments.
- Attach probes to only 1% of function calls (probabilistic sampling).
- Reduces overhead while maintaining statistical accuracy.
- Requires BPF map sampling logic.

**Tail Call Detection** (Low Priority)

- Detect tail-call optimized functions (last instruction is `jmp` not `ret`).
- Attach probe to `jmp` target function instead.
- Requires control flow analysis and function graph.

**Kernel-Side Optimization** (Blocked by Kernel)

- Use eBPF trampolines (BPF_TRAMP_FEXIT) if supported.
- Eliminates need for manual RET finding.
- Currently only available for kernel functions (kprobes/fentry), not userspace
  uprobes.
- Deferred until kernel support extends to uprobes.

---

## Implementation Status

**Core Capability:** ⏳ Not Started

This RFD is in draft stage. Implementation will begin after approval.

**Prerequisites:**

- ✅ RFD 061 (eBPF Uprobe Mechanism) - Implemented
- ✅ RFD 066 (SDK HTTP API) - Implemented

**Dependency Notes:**

This RFD extends RFD 066 by adding function size metadata to the HTTP API.
The Agent only requires the `size_bytes` field to enable return
probes - other metadata fields are optional for this feature.

**Integration Status:**

- ⏳ SDK function size metadata - Not started
- ⏳ Agent disassembly implementation - Not started
- ⏳ Multiple return probe attachment - Not started

## Appendix

### x86-64 RET Instruction Opcodes

Return instructions we need to detect:

| Instruction  | Opcode | Description                      |
|:-------------|:-------|:---------------------------------|
| `ret`        | `0xC3` | Near return to calling procedure |
| `ret imm16`  | `0xC2` | Near return with stack pop       |
| `retf`       | `0xCB` | Far return to calling procedure  |
| `retf imm16` | `0xCA` | Far return with stack pop        |

Modern Go uses only `ret` (`0xC3`) in almost all cases.

### Example: Function with Multiple Returns

**Go Source Code:**

```go
func ProcessRequest(valid bool) error {
    if !valid {
        return errors.New("invalid request") // Return path #1
    }
    result, err := doWork()
    if err != nil {
        return err // Return path #2
    }
    return nil // Return path #3
}
```

**Compiled x86-64 Assembly (simplified):**

```asm
ProcessRequest:
    0x00: mov     %gs:0x28, %rcx      ; Entry point
    0x09: cmp     %al, 0x0            ; Check valid flag
    0x0c: je      0x12                ; Jump to early return
    0x0e: call    doWork              ; Call doWork()
    0x13: test    %rax, %rax          ; Check error
    0x16: jne     0x1a                ; Jump to error return
    0x18: xor     %rax, %rax          ; Set nil return
    0x1b: ret                         ; Return path #3 (offset +0x1b)

    0x12: mov     $error_msg, %rax    ; Early return path
    0x17: ret                         ; Return path #1 (offset +0x17)

    0x1a: mov     %rax, %rbx          ; Error return path
    0x1d: ret                         ; Return path #2 (offset +0x1d)
```

**Agent Processing:**

1. SDK provides: `offset=0x4a5000, size_bytes=0x1e`.
2. Agent disassembles bytes at `0x4a5000` for `0x1e` bytes.
3. Finds RET instructions at offsets: `+0x17`, `+0x1b`, `+0x1d`.
4. Attaches 4 uprobes total:
    - Entry: `0x4a5000 + 0x00`
    - Return 1: `0x4a5000 + 0x17`
    - Return 2: `0x4a5000 + 0x1b`
    - Return 3: `0x4a5000 + 0x1d`
5. Any return path hit will emit a duration event.

### Performance Comparison

**Overhead Analysis:**

| Metric                  | Entry-Only (Current) | Entry + RET-Uprobes         | Overhead Increase |
|:------------------------|:---------------------|:----------------------------|:------------------|
| Probe attachments       | 1 per function       | 1 + N RETs (avg ~3)         | +200%             |
| BPF program invocations | 1 per call           | 2 per call (entry + return) | +100%             |
| Memory (BPF maps)       | Same                 | Same                        | +0%               |
| Event rate              | 50% of calls         | 100% of calls               | +100%             |
| CPU overhead (measured) | 0.2%                 | 0.4% (estimated)            | +0.2%             |

**Conclusion:** Overhead is acceptable for debugging use cases. Production
monitoring should use sampling (future enhancement).

---

## References

- RFD 061: eBPF Uprobe Mechanism (current implementation)
- RFD 066: SDK HTTP API - Pull-Based Discovery (HTTP/JSON metadata API)
- Linux uprobes
  documentation: https://www.kernel.org/doc/Documentation/trace/uprobetracer.txt
- Go runtime internals: https://go.dev/src/runtime/
- x86-64 calling
  conventions: https://en.wikipedia.org/wiki/X86_calling_conventions
- Disassembly library: https://pkg.go.dev/golang.org/x/arch/x86/x86asm
