---
rfd: "093"
title: "Skills — LLM-Invocable Investigation Recipes for the TypeScript SDK"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "076" ]
database_migrations: [ ]
areas: [ "cli", "mcp", "sdk" ]
---

# RFD 093 - Skills — LLM-Invocable Investigation Recipes for the TypeScript SDK

**Status:** 🎉 Implemented

## Implementation Status

🎉 Implemented. All four phases complete.

**What was built:**

- ✅ `SkillResult` and `SkillFn` types in `pkg/sdk/typescript/types.ts` (pre-existing)
- ✅ `skills` namespace exported from `pkg/sdk/typescript/mod.ts`
- ✅ Three built-in skills in `pkg/sdk/typescript/skills/`:
  - `latency-report.ts` — P99 latency + error rate check across services
  - `error-correlation.ts` — cascading failure detection via error spike correlation
  - `memory-leak-detector.ts` — sustained heap growth detection via DuckDB query
- ✅ `pkg/sdk/typescript/embed.go` — embeds all TypeScript SDK files in the Go binary
- ✅ `ExecuteInline` in `internal/cli/run/run.go` — runs inline TypeScript via Deno with an auto-generated import map resolving `@coral/sdk` to the embedded SDK
- ✅ `coral_run` MCP tool in `internal/colony/mcp/tools_run.go` — executes inline TypeScript, relays stderr to user terminal, returns captured stdout as tool result
- ✅ `coral://sdk/reference` MCP Resource — compact plain-text SDK index served on demand
- ✅ Unit and integration tests in `tools_run_test.go` and `tools_schema_test.go`
- ✅ `docs/SDK_REFERENCE.md` updated with full Skills section

**Deferred to Future Work:**
- E2E test for full LLM `coral_run` invocation via orchestrator (deferred — requires live colony + agent)

## Summary

RFD 076 introduced sandboxed TypeScript execution (`coral run script.ts`) and the
`@coral/sdk` primitive library. This RFD extends that foundation to make the
scripting runtime directly usable by the LLM during investigations: a single
`coral_run` MCP tool executes inline TypeScript, an MCP Resource exposes a
compact SDK index for on-demand discovery, and a `skills/` directory in the SDK
ships curated, importable investigation helpers the LLM can call without writing
logic from scratch.

## Problem

**Current behavior:**

RFD 076's scripting runtime is a CLI-first workflow: the LLM generates TypeScript,
writes it to a file, and the user runs `coral run script.ts`. This is the right
model for operator scripts. For LLM-driven investigations it introduces friction:

- The LLM has no direct execution path — it must instruct the user to run a file.
- The SDK has no discoverable index; the LLM must guess what functions exist or
  be told via the system prompt.
- Scripts write to `console.log`; there is no structured output contract for the
  LLM to parse reliably.
- Every investigation requires the LLM to regenerate boilerplate (list services,
  loop over metrics) rather than calling a named pattern.

**Why this matters:**

The MCP tool list already gives the LLM coarse-grained primitives (query summary,
traces, topology). TypeScript scripts are the right escape hatch for cross-service
correlation, threshold-based anomaly detection, and composite investigations that
combine multiple data sources. Without a direct execution path and a discoverable
SDK, that escape hatch is only reachable via the user.

**The tool-proliferation constraint:**

Adding one MCP tool per skill would pollute the tool list and dilute the LLM's
attention. The solution must add exactly one tool regardless of how many skills
exist.

## Solution

Three changes on top of the existing RFD 076 infrastructure:

1. **`coral_run` MCP tool** — executes inline TypeScript via the embedded Deno
   runtime. One tool, one parameter (`code`). The LLM writes and runs TypeScript
   directly without user involvement.

2. **`coral://sdk/reference` MCP Resource** — a compact, plain-text SDK index
   served by the MCP server. The LLM reads it on demand before writing scripts.
   Not in the tool list; not in the system prompt. Fetched only when needed.

3. **`skills/` directory in `@coral/sdk`** — pre-written TypeScript functions
   with a defined `SkillResult` output contract. The LLM imports and calls them
   from `coral_run` code instead of reimplementing common patterns.

**Key Design Decisions:**

- **One tool, not N:** `coral_run` is generic. Skill discoverability comes from
  the Resource, not from individual MCP tool registrations.

- **Resource over system prompt:** The SDK index is not pre-loaded into every
  conversation. It is fetched via a Resource read when the LLM decides it needs
  to write a script. This keeps baseline context lean. Because most LLMs will not
  proactively read a resource unless prompted, the `coral_run` tool description
  includes an explicit instruction: "Before writing code, you MUST read
  coral://sdk/reference." The nudge is in the tool, not the system prompt, so it
  is only incurred when the LLM is already about to use the scripting path.

- **stdout for results, stderr for progress:** Scripts write their final JSON
  result to stdout (`console.log`). Progress messages and intermediate output are
  written to stderr (`console.error`). The `coral_run` executor relays stderr
  lines to the user's terminal in real time while the script runs, then returns
  the captured stdout as the MCP tool result. The LLM sees only the final result;
  the user sees live progress. The `SkillResult` type defines the expected stdout
  shape; free-form scripts may use any JSON structure.

- **Import map injected by executor:** The Deno sandbox runs with
  `--no-remote`. The executor generates a `deno.json` import map alongside the
  temp script that resolves `@coral/sdk` and `@coral/sdk/skills/*` to the SDK
  files embedded in the CLI binary. The LLM never needs to know the physical
  paths; it imports by package name as documented in the SDK reference.

- **Same sandbox as `coral run`:** The Deno runtime, permissions, and timeout
  model from RFD 076 apply unchanged. `coral_run` reuses the existing executor.

**Architecture Overview:**

```
LLM investigation session
│
├── reads  coral://sdk/reference      (MCP Resource, on demand)
│          └── compact index of SDK primitives + available skills
│
└── calls  coral_run { code: "..." }  (MCP Tool)
           │
           └── coral_run executor
                   │  1. writes code to temp file
                   │  2. generates deno.json import map:
                   │       @coral/sdk        → <embedded sdk>/mod.ts
                   │       @coral/sdk/skills → <embedded sdk>/skills/
                   │  3. spawns Deno with --no-remote --import-map=deno.json
                   │
                   └── Embedded Deno (RFD 076 sandbox)
                           │  --allow-net=colony, --allow-read=./
                           │
                           ├── stderr → relayed to user terminal (live progress)
                           │
                           └── stdout → captured, returned as MCP tool result
                                   │
                                   └── JSON SkillResult { summary, data, recommendations? }
```

### Component Changes

1. **MCP server** (`internal/colony/mcp/`):

   - **New tool**: `coral_run` — accepts `{ code: string, timeout?: number }`,
     writes code to a temp file, generates a `deno.json` import map, invokes the
     Deno executor, relays stderr to the user's terminal in real time, captures
     stdout, and returns it as the MCP tool result.
   - **New resource**: `coral://sdk/reference` — static text resource listing
     SDK modules, their public functions with signatures, and available skills
     with one-line descriptions. Registered at server init alongside tools.

2. **TypeScript SDK** (`pkg/sdk/typescript/`):

   - **New directory**: `skills/` — each skill is a `.ts` file exporting a named
     async function with the `SkillFn` signature.
   - **New types** in `types.ts`: `SkillResult`, `SkillFn`.
   - **Updated `mod.ts`**: re-exports `skills/` as `coral.skills` namespace.

3. **CLI** (`internal/cli/run/`):

   - `coral run <script.ts>` is unaffected.
   - **New**: `executeInline(code string)` function extracted from the existing
     executor, shared by both `coral run` and `coral_run`. Handles temp file
     creation, import map generation, and stdout/stderr routing.

### Skill Output Contract

```typescript
// types.ts (addition)

/** Structured output returned by all Skills. Written to stdout as JSON. */
export interface SkillResult {
  /** One-sentence finding, suitable for direct use in LLM reasoning. */
  summary: string;
  /**
   * Severity of the finding. Allows the LLM to triage multiple parallel skill
   * results without reading all data fields first.
   *
   * - "healthy"  — no anomaly detected, system is within normal bounds.
   * - "warning"  — anomaly detected but not yet service-impacting.
   * - "critical" — active degradation or failure requiring immediate attention.
   * - "unknown"  — insufficient data to make a determination.
   */
  status: "healthy" | "warning" | "critical" | "unknown";
  /** Structured data supporting the finding. Shape is skill-specific. */
  data: Record<string, unknown>;
  /** Optional next investigation steps. */
  recommendations?: string[];
}

/** Signature all Skills must implement. */
export type SkillFn<P = Record<string, unknown>> =
  (params: P) => Promise<SkillResult>;
```

**stdout / stderr convention:**

```typescript
// Progress messages → stderr (visible to user in terminal while script runs)
console.error("Checking 3 services...");
console.error("payments: p99=892ms");

// Final result → stdout (returned to LLM as the MCP tool result)
const result: SkillResult = { summary: "...", data: { ... } };
console.log(JSON.stringify(result));
```

The executor captures stdout and stderr on separate streams. Stderr lines are
forwarded to the user's terminal as they are emitted. Stdout is buffered and
returned as the tool result once the script exits. Scripts must not write
non-JSON content to stdout; doing so causes a parse error in the tool response.
Free-form scripts may return any valid JSON object on stdout.

### SDK Reference Resource (content)

The `coral://sdk/reference` resource is a compact plain-text document. It is
generated at build time from the SDK source and embedded in the MCP server
binary. Approximate content:

```
@coral/sdk — Coral TypeScript SDK Reference

PRIMITIVES
  import * as coral from "@coral/sdk";

  coral.services.list()                          → Service[]
  coral.services.get(name)                       → Service

  coral.metrics.getP99(svc, metric)              → MetricValue  (ns)
  coral.metrics.getErrorRate(svc, windowMs)      → number  (0–1)

  coral.traces.findSlow(svc, minDurationNs, windowMs) → Trace[]
  coral.traces.findErrors(svc, windowMs)         → Trace[]

  coral.activity.getServiceActivity(svc)         → ActivitySummary

  coral.topology.getGraph(svc?)                  → TopologyGraph

  coral.system.getMetrics(svc)                   → SystemMetrics

  coral.db.query(sql)                            → Row[]

SKILLS  (import from "@coral/sdk/skills/<name>")
  latency-report       Check P99 latency and error rates across services
  error-correlation    Detect cascading failures via cross-service error spike correlation
  memory-leak-detector Identify services with sustained heap growth over a window

OUTPUT
  stderr → progress logs, relayed to user terminal in real time.
  stdout → final JSON result, returned to LLM as the MCP tool result.
  Skills return { summary, status, data, recommendations? }.
  status: "healthy" | "warning" | "critical" | "unknown"
```

### Built-in Skills (MVP)

Three skills ship with this RFD, sufficient to validate the pattern:

**`skills/latency-report.ts`** — lists all services with P99 latency and error
rate, flags those above a configurable threshold.

**`skills/error-correlation.ts`** — detects cascading failures by finding
services with simultaneous error-rate spikes within a time window.

**`skills/memory-leak-detector.ts`** — queries system metrics over a window and
returns services with a monotonically increasing heap, sorted by growth rate.

## API Changes

### New MCP Tool

```
coral_run

Description:
  Execute TypeScript using the Coral SDK and return structured output.
  IMPORTANT: Before writing code, you MUST read coral://sdk/reference to
  discover available SDK primitives and built-in skills.
  Write progress messages to stderr (console.error) and the final JSON
  result to stdout (console.log). Only stdout is returned to you.

Input schema:
  {
    "code":    string   // TypeScript source. Must write JSON to stdout last.
    "timeout": integer  // Execution timeout in seconds. Default: 60. Max: 300.
  }

Output:
  Captured stdout from the script (expected to be a JSON SkillResult or
  any valid JSON object). Stderr is relayed to the user's terminal in real
  time and is not included in the tool result.
  On error: tool error with exit code and last stderr lines for context.
```

### New MCP Resource

```
URI:      coral://sdk/reference
MimeType: text/plain
Content:  Compact SDK index (see above). Embedded at build time.
```

### SDK Layout (additions only)

```
pkg/sdk/typescript/
  skills/
    latency-report.ts
    error-correlation.ts
    memory-leak-detector.ts
  types.ts          (SkillResult, SkillFn added)
  mod.ts            (skills namespace re-exported)
```

### CLI Usage (unchanged)

```bash
# Unchanged — file-based invocation still works.
coral run analysis.ts

# New — LLM uses coral_run MCP tool with inline code, e.g.:
# coral_run { "code": "
#   import { latencyReport } from '@coral/sdk/skills/latency-report';
#   const r = await latencyReport({ threshold_ms: 500 });
#   console.log(JSON.stringify(r));
# " }
```

## Implementation Plan

### Phase 1: Output contract and skill types

- [x] Add `SkillResult` and `SkillFn` types to `pkg/sdk/typescript/types.ts`
- [x] Update `mod.ts` to export `skills` namespace

### Phase 2: Built-in skills

- [x] Implement `skills/latency-report.ts`
- [x] Implement `skills/error-correlation.ts`
- [x] Implement `skills/memory-leak-detector.ts`

### Phase 3: MCP integration

- [x] Extract `executeInline` from existing executor in `internal/cli/run/run.go`
- [x] Import map generation: write `deno.json` mapping `@coral/sdk` to embedded SDK paths
- [x] Implement `coral_run` tool in `internal/colony/mcp/tools_run.go` with
      stdout capture and stderr real-time relay
- [x] Register `coral_run` in `server.go` tool list
- [x] Implement `coral://sdk/reference` resource registration in `server.go`
- [x] Embed SDK reference text in MCP server binary

### Phase 4: Testing and documentation

- [x] Unit tests for each skill (mock SDK client)
- [x] Integration test for `coral_run` tool execution and output capture
- [ ] E2E test: LLM invokes `coral_run` with a skill, verifies `SkillResult` shape
- [x] Update `docs/SDK_REFERENCE.md` with skills section

## Security Considerations

- `coral_run` uses the identical Deno sandbox as `coral run`: network access
  restricted to the colony address, no filesystem writes, no env access, no shell
  execution. No new permissions are introduced.
- Inline code is written to a temp file with a random name and deleted after
  execution. The temp file is not user-accessible between write and execution.
- The SDK reference resource is read-only and contains no sensitive data.
- Skills shipped in the SDK are reviewed as part of the normal code review process.
  User-authored skills executed via `coral_run` carry the same risk profile as
  any `coral run` invocation.

## Future Work

**TUI Dashboard Renderer** (Future — requires separate execution model)

Skills are LLM-facing: stdout is captured as JSON and returned to the model.
A TUI dashboard is user-facing: it writes ANSI control sequences to the terminal,
runs a blocking event loop, and exits on user input. These two execution models
are incompatible within `coral_run`.

The right design is a rendering layer that consumes `SkillResult` output rather
than producing it:

```
coral_run (LLM path)                coral run (user path)
────────────────────────────        ──────────────────────────────
skills/latency-report.ts        →   render-dashboard.ts
  produces SkillResult JSON              reads SkillResult JSON
  returned to LLM                        renders live TUI to terminal
```

`render-dashboard.ts` would be a CLI-only script (not a skill) invoked by the
user after the LLM completes its investigation. Skills can surface it via
`recommendations`:

```json
{
  "summary": "payments p99 892ms, 3x above threshold",
  "status": "critical",
  "recommendations": ["Run: coral run render-dashboard.ts --service payments"]
}
```

Because `SkillResult.data` is structured and typed per-skill, the renderer can
produce a specialized layout for each skill type (latency heatmap, memory growth
chart, error cascade graph) without coupling the dashboard logic to the
investigation logic.

This is a meaningful capability addition and warrants its own RFD covering: the
TUI library choice (Deno-native, compatible with `--no-remote`), the renderer
invocation contract, and how the CLI distinguishes interactive from captured
execution mode.

**Community Skills Marketplace** (Future RFD)

- A registry of community-contributed skills installable via `coral skill install`.
- Skills pinned by version, verified against a manifest hash.
- `coral://sdk/reference` dynamically includes installed community skills.

**`coral run` Skill Invocation Shorthand** (Future)

- `coral run skill:latency-report --param threshold_ms=500` to invoke a named
  skill directly from the CLI without writing a wrapper script.

**Streaming Partial Results** (Future)

- For long-running skills, emit intermediate `SkillResult` updates to the MCP
  client rather than buffering all output until the script exits.
