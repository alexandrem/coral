---
rfd: "094"
title: "Coral Terminal — Rich Mission-Control TUI with Browser Visualization"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "051", "076", "093" ]
database_migrations: [ ]
areas: [ "cli", "ux", "sdk" ]
---

# RFD 094 - Coral Terminal: Rich Mission-Control TUI with Browser Visualization

**Status:** 🎉 Implemented

## Summary

Introduce `coral terminal`, a purpose-built rich TUI that serves as the
mission-control entry point for a Coral session. It embeds the existing
`coral ask` conversation UI as its primary pane alongside a live sidebar
(services, agent health, past sessions) and a persistent header (colony
context, token usage). For data-rich skill output that exceeds what a
terminal can render well — topology graphs, latency heatmaps, timeseries
charts — `coral terminal` starts an embedded HTTP server and forwards
`SkillResult` render events to a browser dashboard over WebSocket.

## Problem

**Current behavior/limitations:**

- `coral ask` (RFD 051) is a focused REPL with a single conversational
  pane. It shows no ambient system state: no service health, no agent
  counts, no conversation history browser, no token usage.
- During an investigation the developer must interrupt the conversation to
  run `coral colony agents`, `coral colony status`, or `coral colony list`
  in a separate shell to answer basic context questions ("which agents are
  up?", "what colony am I connected to?").
- Rich skill output (RFD 093) is returned as JSON to the LLM and
  summarised in prose. Tabular summaries are readable; topology graphs,
  latency heatmaps, and multi-service timeseries are not. Terminal
  rendering hits a hard ceiling for two-dimensional data structures.
- `coral run <script.ts>` is a fire-and-forget CLI invocation. There is no
  persistent visual surface that receives and accumulates skill output
  across a session.

**Why this matters:**

Effective incident response is a parallel activity: the developer is
reading LLM answers, watching service health, and correlating across
multiple skill runs simultaneously. Forcing that workflow through a single
scrolling REPL creates unnecessary context switching and loses ambient
awareness.

Rich visualizations (memory growth over time, cascading error timelines,
call-graph heat) are the natural output of the skills defined in RFD 093,
but their value is lost when rendered as prose summaries.

**Use cases affected:**

- **Active incident response**: developer needs service health visible
  while asking questions, without alt-tabbing to run status commands.
- **Skill-driven investigations**: `latency-report`, `error-correlation`,
  `memory-leak-detector` produce structured data that warrants charts, not
  bullet points.
- **Session continuity**: no way to browse and resume past conversations
  without exiting the current session.

## Solution

`coral terminal` is the rich entry point. `coral ask` is unchanged and
remains the focused, scriptable, pipe-safe REPL it is. The two commands
serve different workflows.

**Key Design Decisions:**

- **Separate command, not a flag on `coral ask`**: the interaction model
  of a multi-pane TUI is fundamentally different from a single-pane REPL.
  Flagging it onto `coral ask` delays the design decision and couples two
  distinct UX surfaces.

- **Reuse `ask/ui.Model` as the main pane**: the existing bubbletea model
  (streaming, markdown rendering, conversation storage, tool spinners) is
  embedded wholesale as the main pane of the terminal layout. No
  duplication; the conversation logic lives in one place.

- **Browser renderer for rich visualization, not a second TUI pane**:
  terminal rendering of charts and graphs requires either a large
  third-party dependency or significant hand-written cell math. A browser
  page gives full SVG/Canvas capability with a small, self-contained JS
  bundle embedded in the CLI binary. The browser is complementary to the
  terminal, not a replacement: the TUI stays the interaction surface; the
  browser is the visualization pane.

- **Data flows through the CLI executor, not through Deno**: the Deno
  sandbox requires no new network permissions. Skills write a `RenderSpec`
  field in their `SkillResult` JSON to stdout as they do today. The Go
  executor reads it and forwards a render event to the WebSocket. The
  sandbox boundary is unchanged.

- **Embedded HTTP server, ephemeral port**: the server starts when
  `coral terminal` launches, binds to a random localhost port, and stops
  when the terminal exits. No persistent service, no configuration
  required, no port conflicts.

- **`embed.FS` for the browser bundle**: the HTML/JS dashboard is a
  single-file SPA compiled at build time and embedded in the CLI binary.
  It works fully offline. No external CDN dependencies.

**Architecture Overview:**

```
coral terminal (Go process)
│
├── bubbletea TUI
│   ├── HeaderModel     colony ID · connected agents · colony health
│   ├── SidebarModel    service health · agent summary · session list
│   │   └── tea.Tick    periodic poll (30s) → colony status API
│   ├── MainModel       ask/ui.Model (RFD 051, reused verbatim)
│   └── FooterModel     text input (left) · provider/model/tokens (right)
│
└── HTTP server (localhost:EPHEMERAL)
    ├── GET /           embedded HTML/JS dashboard (embed.FS)
    └── WebSocket /ws   receives RenderEvents from executor
            ↑
            │  RenderEvent{type, payload}
            │
    coral_run / coral run executor
            │  reads SkillResult.render from stdout
            │  forwards if present
            │
    Deno sandbox (unchanged permissions)
            └── script stdout: SkillResult JSON
```

**Browser dashboard:**

```
Browser (opened via /browser command or auto on first render event)
┌──────────────────────────────────────────────────────────────────┐
│ Coral Dashboard  ●  prod-us-east                    [live]        │
├──────────────────────────────────────────────────────────────────┤
│  [latency-report]              2026-02-22 14:31                   │
│  Service       P99     Status                                     │
│  payment-api  892ms   ● critical                                  │
│  checkout      201ms  ● healthy                                   │
│  frontend      145ms  ● healthy                                   │
│  notifications  88ms  ● healthy                                   │
├──────────────────────────────────────────────────────────────────┤
│  [memory-leak-detector]        2026-02-22 14:28                   │
│  payment-api  ████████████░░  +412MB over 1h  ● warning           │
│  checkout     ██░░░░░░░░░░░░   +88MB over 1h  ● healthy           │
└──────────────────────────────────────────────────────────────────┘
```

**TUI layout:**

```
┌────────────────────────────────────────────────────────────────────────┐
│ HEADER: ● prod-us-east  ·  12 agents (11 ✓  1 ✗)  ·  8 services        │
├──────────────────────┬─────────────────────────────────────────────────┤
│ Services (8)         │ > what's causing high latency?                  │
│ ─────────────────────│                                                  │
│ ● frontend    3/3    │ ✓ Queried traces (2.1s)                          │
│ ● payment-api 5/5    │ ✓ Ran latency-report skill (1.4s)               │
│ ⚠ checkout    4/4    │   → see browser for full table                  │
│ ✗ notif-svc   0/1    │                                                  │
│                      │ payment-api p99 is 892ms, 3× above threshold.   │
│ Agents (12)          │ DB connection pool at 94% utilisation.           │
│ ─────────────────────│                                                  │
│ ✓ 11 healthy         │ > show me the slow queries                      │
│ ✗  1 unhealthy       │                                                  │
│                      │ ⠋ Fetching logs... (payment-api)                │
│ Sessions             │                                                  │
│ ─────────────────────│                                                  │
│ ▶ 14:30 (current)    │                                                  │
│   09:15 yesterday    │                                                  │
│   16:42 Mon          │                                                  │
├──────────────────────┴─────────────────────────────────────────────────┤
│ > ______________________________  Anthropic · sonnet-4-6 · 12,450 tok  │
│ [Tab] switch pane  [/browser] open dashboard  [/help]  [Ctrl+D] quit   │
└────────────────────────────────────────────────────────────────────────┘
```

### Component Changes

1. **CLI — new `coral terminal` command** (`internal/cli/terminal/`):

   - New cobra command registered in `internal/cli/root.go`.
   - Initialises a three-region bubbletea layout: header, body (sidebar +
     main), footer.
   - `HeaderModel`: renders colony ID, connected agent count
     (healthy/unhealthy split), and discovered service count. Updated via
     `ColonyStatusMsg` emitted by the sidebar's periodic refresh. Colony
     health is indicated by a leading colour dot (green/yellow/red).
   - `SidebarModel`: scrollable list of services with health indicators,
     agent healthy/unhealthy summary, and past session list. Refreshes on a
     `tea.Tick` every 30 seconds by calling the colony status and agents
     APIs. Selecting a past session sends a `LoadSessionMsg` to the main
     pane.
   - `MainModel`: thin wrapper around the existing `ask/ui.Model` (RFD 051).
     Forwards all messages to the inner model; translates `LoadSessionMsg`
     into `ask/ui.ResumeConversationMsg`.
   - `FooterModel`: houses the text input on the left and a right-aligned
     status segment showing LLM provider, model name, and accumulated token
     count for the session. Token count updated via `TokensMsg` emitted by
     the main pane on each LLM response. Tab key cycles focus between
     sidebar and main pane. When the sidebar has focus, arrow keys navigate
     the list.
   - Inline command `/browser` opens the browser dashboard at the server
     URL. Auto-opens on the first render event if the terminal was launched
     with `--auto-browser`.

2. **CLI — embedded HTTP server** (`internal/cli/terminal/server.go`):

   - Starts on `coral terminal` launch, binds to `localhost:0` (OS-assigned
     ephemeral port), stops on process exit.
   - Serves the dashboard bundle at `GET /` from `embed.FS`.
   - Manages WebSocket connections at `/ws`. Broadcasts `RenderEvent` JSON
     to all connected clients.
   - Exposes a `Push(event RenderEvent)` method called by the executor after
     each skill run.

3. **CLI — executor bridge** (`internal/cli/run/` and
   `internal/colony/mcp/`):

   - After a skill script exits, the executor inspects the captured stdout
     for a `render` field in the `SkillResult` JSON.
   - If present, constructs a `RenderEvent` and calls `server.Push()`.
   - Applies to both `coral run` (user path) and `coral_run` MCP tool (LLM
     path). Both share the same `executeInline` function introduced in RFD
     093.
   - When `coral terminal` is not running (i.e., no server is registered),
     `Push()` is a no-op. The executor does not error; it degrades silently.

4. **TypeScript SDK — `RenderSpec` type** (`pkg/sdk/typescript/types.ts`):

   - Add optional `render` field to `SkillResult`.
   - Add `RenderSpec` interface with `type` discriminator and `payload`.
   - Add `RenderType` union for the built-in renderer types shipped in v1.
   - Skills opt in explicitly. Omitting `render` leaves behaviour identical
     to RFD 093.

5. **Browser dashboard** (`internal/cli/terminal/web/`):

   - Single-file SPA compiled to `dist/dashboard.html` and embedded via
     `embed.FS`.
   - Connects to `ws://localhost:PORT/ws` on load.
   - Renders incoming `RenderEvent` payloads as typed panels appended to the
     page (most recent at top).
   - v1 renderer types: `table` (any `SkillResult.data` as sortable table),
     `bar` (single numeric series), `timeseries` (time-indexed numeric
     series). Specialized renderers (topology graph, heatmap) are future
     work.
   - No external runtime dependencies. Uses only browser-native APIs (Web
     Components, SVG, Fetch, WebSocket). Build toolchain: `esbuild` (single
     binary, no Node.js required at runtime).

### RenderSpec contract

```typescript
// Addition to pkg/sdk/typescript/types.ts

export type RenderType = "table" | "bar" | "timeseries";

export interface RenderSpec {
  /**
   * Renderer type. Determines how payload is visualised in the browser
   * dashboard. Unknown types are rendered as a formatted JSON block.
   */
  type: RenderType | string;
  /** Title shown in the dashboard panel header. */
  title?: string;
  /** Renderer-specific payload. Shape is documented per RenderType. */
  payload: unknown;
}

// Extended SkillResult (backwards compatible — render is optional)
export interface SkillResult {
  summary: string;
  status: "healthy" | "warning" | "critical" | "unknown";
  data: Record<string, unknown>;
  recommendations?: string[];
  /** Optional. If present, a RenderEvent is pushed to the browser dashboard. */
  render?: RenderSpec;
}
```

**`table` payload shape:**

```typescript
interface TablePayload {
  columns: string[];
  rows: (string | number)[][];
  /** Column index used for status colour coding. Values: healthy/warning/critical. */
  statusColumn?: number;
}
```

**`bar` payload shape:**

```typescript
interface BarPayload {
  labels: string[];
  values: number[];
  unit?: string;      // e.g. "ms", "MB"
  threshold?: number; // rendered as a reference line
}
```

**`timeseries` payload shape:**

```typescript
interface TimeseriesPayload {
  series: Array<{
    label: string;
    points: Array<{ ts: number; value: number }>; // ts: Unix ms
  }>;
  unit?: string;
}
```

**Example skill using the contract:**

```typescript
import { coral } from "@coral/sdk";
import type { SkillResult, SkillFn } from "@coral/sdk";

export const latencyReport: SkillFn<{ threshold_ms: number }> = async ({ threshold_ms }) => {
  const services = await coral.services.list();
  const rows = await Promise.all(services.map(async svc => {
    const p99 = await coral.metrics.getP99(svc.name, "http.server.duration");
    const status = p99 > threshold_ms * 1e6 ? "critical" : "healthy";
    console.error(`${svc.name}: p99=${p99 / 1e6}ms`);
    return [svc.name, Math.round(p99 / 1e6), status];
  }));

  const result: SkillResult = {
    summary: `${rows.filter(r => r[2] === "critical").length} services above ${threshold_ms}ms threshold.`,
    status: rows.some(r => r[2] === "critical") ? "critical" : "healthy",
    data: { rows },
    render: {
      type: "table",
      title: "Latency Report",
      payload: {
        columns: ["Service", "P99 (ms)", "Status"],
        rows,
        statusColumn: 2,
      },
    },
  };

  console.log(JSON.stringify(result));
  return result;
};
```

### WebSocket protocol

Events are newline-delimited JSON objects sent from server to client.

```typescript
// Server → client
interface RenderEvent {
  id: string;           // UUID, stable per skill run
  ts: number;           // Unix ms
  skillName?: string;   // populated from executor context
  spec: RenderSpec;     // forwarded verbatim from SkillResult.render
}

// Client → server (future: interactions from browser back to terminal)
// Reserved; not used in v1.
```

## Implementation Plan

### Phase 1: TUI layout skeleton

- [x] Create `internal/cli/terminal/` package with cobra command
- [x] Register `coral terminal` in `internal/cli/root.go`
- [x] Implement `TerminalModel` with header, sidebar, main, footer regions
  using lipgloss for layout
- [x] Embed existing `ask/ui.Model` as main pane — pass-through all
  messages when main pane has focus
- [x] Implement Tab focus cycling between sidebar and main pane
- [x] `HeaderModel`: renders colony ID, agent counts, service counts, model name
- [x] Footer: keybinding hint bar with server URL

### Phase 2: Sidebar live data

- [x] `SidebarModel`: service list with health indicators, polled via
  colony status API on `tea.Tick` (30s interval)
- [x] `SidebarModel`: agent healthy/unhealthy summary count
- [x] `SidebarModel`: past session list loaded from
  `~/.coral/conversations/<colony>/`, selectable to resume in main pane
- [x] Sidebar arrow-key navigation when sidebar has focus
- [x] Session selection → `ui.LoadConversationMsg` → ask/ui.Model updates

### Phase 3: Embedded HTTP server and WebSocket

- [x] Implement `internal/cli/terminal/server.go`: HTTP server on
  `localhost:0`, WebSocket hub, `Push(RenderEvent)` method
- [x] Implement global server registry so the executor can call `Push()`
  without a direct dependency on the terminal command
- [x] `/browser` inline command: open browser to dashboard URL
- [x] `--auto-browser` flag: open browser on launch

### Phase 4: Browser dashboard bundle

- [x] Create `internal/cli/terminal/web/dashboard.html` — single-file SPA
- [x] WebSocket client: connect on load, reconnect on disconnect
- [x] Panel rendering: `table`, `bar`, `timeseries` renderer components
- [x] Unknown type fallback: formatted JSON block
- [x] Embed via `//go:embed` in `server.go` (no build step — vanilla HTML/JS)

### Phase 5: SkillResult.render bridge

- [x] Add `RenderSpec`, `RenderType`, `SkillResult`, `SkillFn` to
  `pkg/sdk/typescript/types.ts`
- [x] Extend `coral run` stdout capture with `io.MultiWriter` to inspect
  output for `render` field after script exit
- [x] Call `server.Push()` when `render` is present and server is running
- [x] Ensure no-op behaviour when `coral terminal` is not running

### Phase 6: Testing and documentation

- [x] Unit tests: `TerminalModel` message routing, pane focus transitions
- [x] Unit tests: `server.Push()` fan-out to multiple WebSocket clients
- [x] Unit tests: executor render bridge, no-op when server absent
- [x] Update `docs/CLI_REFERENCE.md` with `coral terminal` command

## API Changes

### CLI Commands

```bash
# Launch rich terminal (interactive, requires a terminal)
coral terminal

# Launch and auto-open browser dashboard on first render event
coral terminal --auto-browser

# Expected terminal layout on launch:
# ┌─ ● prod-us-east  ·  12 agents (11 ✓  1 ✗)  ·  8 services ────────────┐
# │ Services (8)     │ Coral Terminal — type a question to begin            │
# │ ● frontend  3/3  │                                                      │
# │ ● payment   5/5  │                                                      │
# │ ...              │                                                      │
# │ Agents (12)      │                                                      │
# │ ✓ 11  ✗ 1        │                                                      │
# │ Sessions         │                                                      │
# │ ▶ (new)          │                                                      │
# ├──────────────────┴──────────────────────────────────────────────────── │
# │ > ____________________________  Anthropic · sonnet-4-6 · 0 tok         │
# │ [Tab] pane  [/browser] dashboard  [/help]  [Ctrl+D] quit               │
# └────────────────────────────────────────────────────────────────────────┘
```

```bash
# Inline commands available within coral terminal:
/help           # show available commands and keybindings
/browser        # open browser dashboard (localhost:PORT)
/clear          # clear conversation in main pane
/exit, /quit    # exit terminal
```

### Keybindings

```
Tab             Cycle focus: main pane ↔ sidebar
↑ / ↓           Navigate sidebar list when sidebar has focus
Enter           Load selected session (sidebar focus)
                Submit question (main pane focus)
Ctrl+C          Cancel current LLM query (stay in session)
Ctrl+D          Exit terminal
```

### TypeScript SDK additions

```typescript
// pkg/sdk/typescript/types.ts — additions only, backwards compatible

export type RenderType = "table" | "bar" | "timeseries";

export interface RenderSpec {
  type: RenderType | string;
  title?: string;
  payload: unknown;
}

export interface SkillResult {
  summary: string;
  status: "healthy" | "warning" | "critical" | "unknown";
  data: Record<string, unknown>;
  recommendations?: string[];
  render?: RenderSpec; // new, optional
}
```

### Configuration Changes

```yaml
# ~/.coral/config.yaml — new optional section

ai:
  terminal:
    # Automatically open browser dashboard on first render event.
    # Default: false (use /browser command manually).
    auto_browser: false

    # Sidebar refresh interval in seconds. Default: 30.
    sidebar_refresh_seconds: 30
```

## Testing Strategy

### Unit Tests

- `TerminalModel.Update()`: Tab key toggles focus between sidebar and main;
  all other keys are forwarded to the focused pane only.
- `SidebarModel`: `RefreshMsg` updates service list; `tea.Tick` fires at
  configured interval; session selection emits `LoadSessionMsg`.
- `HeaderModel`: `ColonyStatusMsg` updates agent counts and service count; colony health dot reflects unhealthy agents correctly.
- `FooterModel`: `TokensMsg` accumulates token count correctly; right-aligned segment renders provider, model, and count.
- `server.Push()`: event is broadcast to all connected WebSocket clients;
  clients that disconnect mid-broadcast do not panic.
- Executor render bridge: `SkillResult` with `render` field calls
  `server.Push()`; `SkillResult` without `render` does not; `server` is
  nil (terminal not running) → no-op.

### Integration Tests

- `coral terminal` launches, sidebar populates with mocked colony status
  API responses.
- Past session list loads from `~/.coral/conversations/` fixture.
- Selecting a past session loads it into the main pane conversation history.
- `executeInline` with a skill that emits `render` in stdout → WebSocket
  client receives corresponding `RenderEvent`.

### E2E Tests

- Full `coral terminal` session: sidebar refreshes twice, LLM question
  answered, skill run triggers browser event.
- `--auto-browser` flag: browser is opened automatically when first
  `RenderEvent` arrives.
- No-op path: `coral run skill.ts` outside `coral terminal` completes
  without error even though no server is running.

## Security Considerations

- **Localhost-only server**: the HTTP server binds exclusively to
  `127.0.0.1`. It is not accessible from other machines on the network.
- **Short-lived**: the server exists only for the duration of the
  `coral terminal` process. No persistent attack surface.
- **No new Deno permissions**: the Deno sandbox network permission remains
  `--allow-net=<colony-address>`. The render data path goes through Go
  stdout capture, not a Deno network call.
- **WebSocket origin check**: the server validates that WebSocket upgrade
  requests originate from `localhost`. Non-localhost origins are rejected.
- **No sensitive data in render events**: `RenderSpec.payload` is
  skill-authored. The SDK reference resource documents that payloads must
  not include credentials, tokens, or raw environment variables.
- **Dashboard bundle integrity**: the HTML/JS bundle is embedded at build
  time from the repository source. It is not fetched at runtime. Supply
  chain risk is identical to any other Go dependency.

## Future Work

**Specialized browser renderer components** (Future RFD)

The v1 browser dashboard renders `table`, `bar`, and `timeseries` types.
Specialized visualizations — call-graph heatmaps, service topology graphs,
memory allocation flame-trees — require per-skill renderer components
tightly coupled to their `payload` schema. These warrant a dedicated design
once the generic renderer is validated in production.

**In-TUI script pane via `tea.ExecProcess`** (Future RFD)

For scripts that produce a native bubbletea TUI (e.g., a live-updating
metrics dashboard written in Go or a Deno Ink component), bubbletea's
`tea.ExecProcess` can suspend the parent terminal, hand off the screen, and
restore on exit. This is an alternative rendering path to the browser for
authors who prefer a pure-terminal experience. The design depends on the
scripting runtime supporting interactive output mode, which is not in scope
for RFD 093.

**Browser-to-terminal interaction** (Future)

The WebSocket protocol reserves the client→server direction. A future RFD
could use it to allow the user to click a data point in the browser and
have the terminal automatically ask a follow-up question about that data
point (e.g., click a latency spike → terminal sends "show traces for
payment-api around 14:31").

**Community skill renderer registry** (Future — depends on RFD 093 skills
marketplace)

Once a community skills marketplace exists, skill authors can publish
renderer components alongside their skills. The dashboard would load
renderer components on demand from the registry for installed community
skills.

## Appendix

### Why not extend `coral ask`?

`coral ask` is a focused, scriptable, pipe-safe REPL. It has a clear
semantics: ask a question, get an answer, optionally continue. It can be
used in scripts (`coral ask "status?" --continue >> report.txt`), piped,
and non-interactively. Adding ambient panels, a persistent server, and
browser integration to `coral ask` would break those guarantees and muddy
its identity.

`coral terminal` is explicitly an interactive-only, terminal-required
command. Launching it from a pipe or a script is a user error, not a
supported workflow. The separation keeps both commands honest about what
they are.

### Bubbletea sub-model pattern

The existing `ask/ui.Model` is embedded in `MainModel` using bubbletea's
standard sub-model composition pattern: `MainModel.Update()` forwards
`tea.Msg` to `ask/ui.Model.Update()` when the main pane has focus and
collects the returned `tea.Cmd`. `MainModel.View()` calls
`ask/ui.Model.View()` and clips the output to the available pane
dimensions using lipgloss. No changes to `ask/ui.Model` are required.

### esbuild for the dashboard bundle

The browser dashboard is built with `esbuild` (a single Go binary, no
Node.js required). The Makefile target runs `esbuild --bundle
--outfile=dist/dashboard.html src/dashboard.ts`. The output is a single
HTML file with inlined JS and CSS, embedded into the Go binary via
`go:embed`. The build target only runs when dashboard source files change
(Makefile file dependency). CI runs the build and checks the embedded
bundle is up to date.
