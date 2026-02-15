---
rfd: "051"
title: "Coral Ask - Interactive Terminal Mode"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "030" ]
database_migrations: [ ]
areas: [ "ai", "cli", "ux" ]
---

# RFD 051 - Coral Ask: Interactive Terminal Mode

**Status:** 🚧 Draft

<!--
Status progression:
  🚧 Draft → 👀 Under Review → ✅ Approved → 🔄 In Progress → 🎉 Implemented
-->

## Summary

Extend `coral ask` (RFD 030) with an interactive terminal mode using
`github.com/charmbracelet/bubbletea` to provide a REPL-style conversational
interface. Developers enter a persistent session with rich terminal UI (markdown
rendering, syntax highlighting, tables) and streaming responses instead of
repeatedly running `coral ask "question" --continue` for each query.

## Problem

**Current behavior/limitations:**

- RFD 030 implemented `coral ask "question"` as a single-shot command
- Multi-turn conversations exist via `--continue` flag, but require re-running
  `coral ask` for each follow-up question
- No persistent interactive session for iterative debugging workflows
- Each invocation has startup overhead (agent initialization, colony connection)
- Terminal output is basic text with no rich formatting (markdown, syntax
  highlighting, tables)
- Conversation history exists (`~/.coral/conversations/`) but no way to browse
  or resume old sessions interactively

**Why this matters:**

- Real debugging sessions are iterative: "What's slow?" → "Show traces" → "
  Filter by errors" → "Compare to yesterday"
- Typing `coral ask "question" --continue` repeatedly breaks flow and feels
  archaic compared to modern REPL interfaces
- Developers expect REPL-like UX: type question, get answer, ask follow-up
  immediately without re-running commands
- Modern AI tools (Claude Code, Cursor, Copilot) use persistent interactive
  sessions with rich terminal UI
- Current plain text output makes it hard to read code blocks, tables, and
  structured data

**Use cases affected:**

- **Active incident response**:
  ```
  coral ask
  > what's causing the 500 errors?
  > show me the actual error logs
  > which service is the root cause?
  > restart that service
  ```

- **Exploratory analysis**:
  ```
  coral ask
  > what services are deployed?
  > show me metrics for payment-api
  > compare to last week
  > show the deployment timeline
  ```

- **Long debugging sessions**: Maintaining context across 10+ queries without
  re-running commands

- **Learning/onboarding**: New developers exploring system state interactively

## Solution

Add an interactive terminal mode to `coral ask` that spawns a persistent session
with a conversational interface. When invoked without a question (`coral ask`),
the CLI detects terminal mode and enters a REPL-style loop with:

- **Persistent context**: Conversation history maintained across all queries in
  the session, using existing `~/.coral/conversations/` storage
- **Rich terminal UI**: Streaming responses with markdown rendering, syntax
  highlighting, tables, and progress indicators using
  `github.com/charmbracelet/bubbletea`
- **Inline commands**: Session management commands (`/help`, `/clear`, `/exit`)
- **Smart input handling**: Command history navigation, Ctrl+C to cancel queries
- **Fast iteration**: No startup overhead between questions (agent stays warm,
  colony connection persists)
- **Session resumption**: Resume previous conversations with `coral ask --resume
  <id>` or select from list in interactive mode

**Key Design Decisions:**

- **Bubbletea TUI framework**: Use Elm architecture (model-update-view) for
  unified state management
    - Simplifies coordination between input, streaming responses, and UI updates
    - Built-in terminal handling (resize, colors, alternate screen)
    - Future-proof for visual mode (split panes, conversation browser)
    - Integrates naturally with Charm ecosystem (glamour for markdown, lipgloss
      for styling)

- **Reuse existing conversation storage**: Interactive sessions use same
  `Message` types and `~/.coral/conversations/` storage as `--continue`
    - No separate "session" concept - interactive mode is just a nicer interface
      to conversations
    - Conversations auto-save after each turn (crash-safe)
    - Can resume any conversation interactively or via `--continue`

- **Simplified v1 prompt**: Focus on usability over metrics
    - Show: colony name, model name
    - Defer: token count, cost tracking, context window % (future RFD)
    - Example: `my-app-prod | gemini-1.5-flash >`

- **Terminal detection**: Only enter interactive mode when stdin is a terminal
    - Prevents hanging when piped/scripted
    - Explicit `--interactive` flag can force mode (for testing)

- **REPL-style interface**: Familiar UX similar to `python`, `node`, or `psql`
    - Commands prefixed with `/` (e.g., `/help`, `/clear`, `/exit`)
    - Natural language queries entered directly (no prefix)
    - Standard keybindings (Ctrl+C cancel, Ctrl+D exit)

- **Streaming output**: LLM responses stream to terminal in real-time
    - Visual feedback that processing is happening (not stuck)
    - Users can start reading before full response completes
    - Interruptible (Ctrl+C cancels current query, stays in session)

- **Rich rendering**: Structured output formatted for readability
    - Markdown rendering (headers, lists, code blocks with syntax highlighting)
    - Tables for metrics/trace data (auto-formatted columns)
    - Spinners for long-running MCP tool calls
    - Color-coded severity (errors in red, warnings in yellow)

- **Conversation persistence**: Reuses existing `~/.coral/conversations/`
  storage
    - Auto-saved after each turn (crash-safe, same as `--continue`)
    - Datetime-based file naming for natural chronological ordering
    - Resume any conversation with `coral ask --resume <id>`
    - Easy cleanup: delete old conversations by date

- **Simple context display**: Focus on essential information
    - Show: colony name, model name
    - Deferred to future RFD: token count, cost, session duration, warnings

**Benefits:**

- **Faster iteration**: No startup overhead, instant follow-up questions
- **Better UX**: Familiar REPL interface, rich output formatting
- **Improved productivity**: Maintain flow during debugging, no context
  switching
- **Session continuity**: Conversations auto-save and can be resumed
- **Lower cognitive load**: Don't need to remember previous queries, history is
  visible

**Design Philosophy:**

- **Bubbletea-first**: Use Elm architecture for clean state management and
  future extensibility
- **Reuse existing infrastructure**: Conversations, storage, agent code from RFD
  030
- **Familiar UX**: Borrow from successful REPLs (`psql`, `python`, `node`)
- **Graceful degradation**: Rich features when available, basic output when
  NO_COLOR is set

**Architecture Overview:**

```
Terminal (Interactive Mode - Bubbletea)
┌────────────────────────────────────────────────────────────┐
│ coral ask (interactive session)                            │
│                                                            │
│ my-app-prod | gemini-1.5-flash                             │
│ ───────────────────────────────────────────────────────────│
│                                                            │
│ > what's causing high latency?                             │
│                                                            │
│ ✓ Analyzing traces (3 services)... 2.1s                    │
│ ✓ Queried metrics (last 1h)... 0.8s                        │
│                                                            │
│ ## Root Cause Analysis                                     │
│                                                            │
│ The payment-api service shows elevated p95 latency:        │
│                                                            │
│ Service       | p50    | p95     | p99     | Change        │
│ ──────────────┼────────┼─────────┼─────────┼───────────    │
│ payment-api   | 120ms  | 1400ms  | 2800ms  | +85% (1h)     │
│ checkout      | 80ms   | 200ms   | 350ms   | +12% (1h)     │
│                                                            │
│ Evidence:                                                  │
│ • DB connection pool at 94% utilization (threshold: 80%)   │
│ • 127 slow query warnings in last hour                     │
│                                                            │
│ > show me the slow queries                                 │
│                                                            │
│ ⠋ Fetching logs... (payment-api)                           │
│                                                            │
│ [Ctrl+C to cancel, /help for commands, /exit to quit]      │
└────────────────────────────────────────────────────────────┘
         ↕ (bubbletea Elm architecture)
┌────────────────────────────────────────────────────────────┐
│ Bubbletea Model (internal/cli/ask/ui/model.go)             │
│                                                            │
│ State:                                                     │
│ • currentQuestion: string                                  │
│ • conversation: []Message (from existing storage)          │
│ • streamingResponse: string (accumulated chunks)           │
│ • toolCalls: []ToolCall (for citations)                    │
│ • state: idle | querying | streaming | error               │
│                                                            │
│ Update():                                                  │
│ • UserInput → send to agent, transition to querying        │
│ • StreamChunk → append to response, re-render              │
│ • ToolStart → show spinner                                 │
│ • ToolComplete → hide spinner, show checkmark              │
│ • Error → show error, return to idle                       │
│                                                            │
│ View():                                                    │
│ • Render prompt with colony/model                          │
│ • Use glamour for markdown formatting                      │
│ • Use lipgloss for styling                                 │
│ • Use spinner component while querying                     │
└────────────────────────────────────────────────────────────┘
         ↕ (calls agent.Ask with streaming)
┌────────────────────────────────────────────────────────────┐
│ Ask Agent (internal/agent/ask - from RFD 030)              │
│                                                            │
│ • Maintains conversation history (reuse existing code)     │
│ • Executes MCP tool calls                                  │
│ • Streams LLM responses as bubbletea messages              │
│ • Saves to ~/.coral/conversations/ after each turn         │
└────────────────────────────────────────────────────────────┘
         ↕ MCP (buf Connect RPC)
┌────────────────────────────────────────────────────────────┐
│ Colony MCP Server (RFD 004)                                │
│                                                            │
│ • coral_query_beyla_*, coral_get_service_*, etc.           │
└────────────────────────────────────────────────────────────┘
```

### Component Changes

1. **CLI (`internal/cli/ask/ask.go`)** (extend existing):
    - Update command args validation from `MinimumNArgs(1)` to `MaximumNArgs(1)`
    - Detect interactive mode: no arguments + terminal detected (isatty check)
    - Add `--interactive` flag to force interactive mode (useful for testing)
    - Add `--resume <id>` flag to resume previous conversation (alternative to
      `--continue`)
    - Initialize bubbletea program when in interactive mode
    - Reuse existing conversation storage (`~/.coral/conversations/`)

2. **Bubbletea UI (`internal/cli/ask/ui`)** (new package):
    - **Model** (`model.go`): State management using Elm architecture
        - State: `idle | querying | streaming | error`
        - Fields: `currentQuestion`, `conversation` (reuse existing `Message`
          type), `streamingResponse`, `toolCalls`
        - Manages conversation ID (new or resumed)
    - **Update** (`update.go`): Message handling and state transitions
        - `tea.KeyMsg`: Handle user input, inline commands (`/help`, `/clear`,
          `/exit`)
        - `StreamChunkMsg`: Append to streaming response, re-render
        - `ToolStartMsg`: Show spinner for tool execution
        - `ToolCompleteMsg`: Hide spinner, show checkmark
        - `ErrorMsg`: Display error, return to idle
        - `tea.Quit`: Save conversation and exit
    - **View** (`view.go`): Rendering logic
        - Render prompt: `<colony> | <model> >`
        - Use `glamour` for markdown rendering (code blocks, tables, lists)
        - Use `lipgloss` for styling and colors
        - Use `spinner` component from `bubbles` library for tool calls
        - Handle NO_COLOR environment variable for basic terminals
    - **Commands** (`commands.go`): Side effects (async operations)
        - `askQuestionCmd`: Call agent.Ask() and stream responses back as
          messages
        - `saveConversationCmd`: Save to `~/.coral/conversations/` (reuse
          existing function)

3. **Agent Integration** (extend existing from RFD 030):
    - Modify `agent.Ask()` to support bubbletea message channel for streaming
    - Emit `StreamChunkMsg` as LLM generates response
    - Emit `ToolStartMsg` / `ToolCompleteMsg` for MCP tool executions
    - Emit `ErrorMsg` on failures
    - Reuse existing conversation management (no changes needed)

**Configuration:**

Interactive mode uses existing `coral ask` configuration from RFD 030. No new
configuration required for v1.

- Terminal detection is automatic (isatty check)
- Rich output respects `NO_COLOR` environment variable
- Conversations saved to existing `~/.coral/conversations/<colony>/<id>.json`
- Model selection uses existing `ai.ask.default_model` setting

**Deferred configuration (future RFD):**

- Rendering options (table styles, syntax themes, color schemes)
- Interactive-specific settings (auto-save intervals, history limits)
- Visual mode preferences (split panes, conversation browser)

## Implementation Plan (Optional)

### Phase 1: Foundation & Bubbletea Setup

- [ ] Add bubbletea dependencies:
    - `github.com/charmbracelet/bubbletea` (TUI framework)
    - `github.com/charmbracelet/bubbles` (UI components)
    - `github.com/charmbracelet/glamour` (markdown rendering)
    - `github.com/charmbracelet/lipgloss` (styling)
- [ ] Update CLI args validation: `Args: cobra.MaximumNArgs(1)`
- [ ] Add terminal detection function (isatty check)
- [ ] Add `--interactive` flag for forcing interactive mode
- [ ] Add `--resume <id>` flag for resuming conversations
- [ ] Create `internal/cli/ask/ui` package structure

### Phase 2: Bubbletea Model Implementation

- [ ] Implement `Model` struct with state management:
    - State: `idle | querying | streaming | error`
    - Fields: conversation ID, messages, current input, streaming buffer
- [ ] Implement `Init()`: Initialize with new or resumed conversation
- [ ] Implement `Update()`: Handle messages and state transitions
    - User input (text entry, Enter to submit)
    - Inline commands: `/help`, `/clear`, `/exit`
    - Ctrl+C (cancel query), Ctrl+D (exit)
    - Stream chunks from agent
    - Tool execution events (start/complete)
    - Error handling
- [ ] Implement `View()`: Render UI
    - Simple prompt: `<colony> | <model> >`
    - Input area with current question
    - Conversation history (scrollable viewport)
    - Loading spinner during queries
    - Error display

### Phase 3: Rich Rendering & Agent Integration

- [ ] Integrate glamour for markdown rendering in View()
- [ ] Add lipgloss styling for colors and formatting
- [ ] Add spinner component from bubbles for tool calls
- [ ] Implement NO_COLOR environment variable support
- [ ] Modify agent.Ask() to emit bubbletea messages:
    - Create `StreamChunkMsg`, `ToolStartMsg`, `ToolCompleteMsg`, `ErrorMsg`
    - Run agent in goroutine, send messages via channel
    - Convert channel messages to bubbletea messages
- [ ] Integrate with existing conversation storage:
    - Load conversation on resume
    - Save after each turn (reuse existing functions)

### Phase 4: Testing & Documentation

- [ ] Unit tests: Model state transitions, command handling
- [ ] Integration tests: Bubbletea message flow, markdown rendering
- [ ] E2E tests: Full interactive session workflow
- [ ] Terminal compatibility tests (NO_COLOR support)
- [ ] Documentation: Interactive mode guide, keyboard shortcuts

## API Changes

### CLI Commands

```bash
# Enter interactive mode (no question provided, auto-detected terminal)
coral ask

# Expected output:
Welcome to Coral Ask (interactive mode)
Type /help for commands, Ctrl+D to exit

my-app-prod | gemini-1.5-flash
> what services are running?

✓ Querying service topology... 0.8s

Found 12 services in production:

Service         | Status  | Instances | CPU    | Memory
────────────────┼─────────┼───────────┼────────┼────────
frontend        | healthy | 3         | 24%    | 512MB
payment-api     | healthy | 5         | 67%    | 1.2GB
checkout        | healthy | 4         | 45%    | 890MB
...

my-app-prod | gemini-1.5-flash
> show metrics for payment-api

⠋ Fetching metrics... (payment-api, last 1h)

---

# Explicit interactive flag (force interactive mode, useful for testing)
coral ask --interactive

# Resume a previous conversation by full filename
coral ask --resume 2026-02-15-143022-abc123

# Or by datetime prefix (finds matching conversation)
coral ask --resume 2026-02-15-143022

# Or by random suffix only (for compatibility)
coral ask --resume abc123

# Single-shot mode still works (for scripting)
coral ask "what's the current status?"

# Cannot use interactive mode in non-terminal context
echo "what services are running?" | coral ask
# Error: interactive mode requires a terminal
```

### New Flags

- `--interactive`: Force interactive mode (auto-detected by default when no
  args + terminal)
- `--resume <conversation-id>`: Resume a previous conversation in interactive
  mode
    - Accepts full filename: `2026-02-15-143022-abc123`
    - Or datetime prefix: `2026-02-15-143022` (must uniquely identify one
      conversation)
    - Or random suffix: `abc123` (for backward compatibility)

**Note:** `--continue` flag still works for single-shot mode to continue the
last conversation. `--resume` is specifically for interactive mode with explicit
conversation ID selection.

### Inline Commands (Core)

Commands available within interactive session (prefixed with `/`):

```
/help                   Show available commands and usage examples
/clear                  Clear the screen
/exit, /quit            Exit interactive session
```

**Keyboard Shortcuts:**

```
Ctrl+C                  Cancel current query (stay in session)
Ctrl+D                  Exit interactive session
Up/Down arrows          (Future) Navigate command history
```

**Note:** Additional commands (runtime model/colony switching, history search,
session export) are deferred to future RFDs. See "Deferred Features" section.

**Example usage:**

```
my-app-prod-xyz>
> /help

Coral Ask - Interactive Mode

Available commands:
  /help      - Show this help message
  /exit      - Exit interactive session

Natural language queries:
  Just type your question (no prefix needed)

Examples:
  > what's causing high latency?
  > show me error logs for payment-api
  > compare current metrics to yesterday

Keyboard shortcuts:
  Ctrl+C     - Cancel current query (stay in session)
  Ctrl+D     - Exit interactive session
  Up/Down    - Navigate command history

my-app-prod-xyz>
```

### Configuration Changes

**No configuration changes required.**

Interactive mode reuses existing `ai.ask` configuration from RFD 030. Behavior
is controlled by:

- **Terminal detection**: Automatic (isatty check)
- **Rich output**: Respects `NO_COLOR` environment variable
- **Conversation storage**: Uses existing `~/.coral/conversations/` directory
- **Model selection**: Uses existing `ai.ask.default_model` setting

**Future configuration (deferred to separate RFD):**

- Rendering preferences (table styles, syntax themes)
- Interactive-specific behavior (auto-save intervals, scrollback limits)
- Visual mode settings (layout, pane configuration)

## Testing Strategy (Optional)

### Unit Tests

- **Model state transitions**: Test Update() function with various message types
  (`tea.KeyMsg`, `StreamChunkMsg`, `ToolStartMsg`, etc.)
- **Inline command handling**: `/help`, `/clear`, `/exit` parse and execute
  correctly
- **View rendering**: Verify prompt format, markdown conversion, color
  application
- **Conversation integration**: Reuse existing Message types, save/load from
  storage

### Integration Tests

- **Bubbletea message flow**: Send messages to Update(), verify state changes
  and commands returned
- **Streaming accumulation**: Multiple `StreamChunkMsg` properly accumulate in
  buffer
- **Markdown rendering**: glamour correctly formats code blocks, tables, lists
- **NO_COLOR support**: Output degrades gracefully when NO_COLOR=1
- **Agent communication**: agent.Ask() emits correct message sequence

### E2E Tests

**Scenario 1: Basic Interactive Session**

```bash
# Setup: Start interactive mode, ask 3 questions, verify context maintained
coral ask <<EOF
what services are running?
show metrics for payment-api
compare to yesterday
/exit
EOF

# Verify: All 3 questions answered with context from previous turns
```

**Scenario 2: Rich Output Rendering**

```bash
# Setup: Query that returns table data
coral ask <<EOF
show service metrics
/exit
EOF

# Verify: Output contains formatted table (not raw JSON)
```

**Scenario 3: Interruption Handling**

```bash
# Setup: Send Ctrl+C during LLM response
# (simulated in test via context cancellation)

# Verify: Current query cancelled, session remains active, can ask new question
```

**Scenario 4: Basic Terminal Compatibility**

```bash
# Setup: Run with NO_COLOR environment variable
NO_COLOR=1 coral ask <<EOF
what's the current status?
/exit
EOF

# Verify: Output has no ANSI color codes, still readable
```

## Security Considerations (Optional)

### Terminal Escape Sequences

**Injection risk:**

- Malicious log messages containing ANSI escape sequences
- Could manipulate terminal display or execute commands (in theory)

**Mitigations:**

- Sanitize all data received from Colony MCP before rendering
- Use terminal library's built-in escaping (`glamour`, `tablewriter` handle
  this)
- Strip unknown escape sequences from untrusted input

## Future Work

The following features are out of scope for this RFD to keep the core
implementation focused and shippable. These will be addressed in follow-up RFDs
once the basic interactive mode is proven and stable.

**Enhanced Prompt & Context Display** (Future - RFD TBD)

The v1 implementation uses a simplified prompt showing colony and model name.
Dynamic context display requires:

- Real-time token usage tracking and display
- Cost estimation and warning thresholds (per query, per session)
- Context window utilization indicators
- Configurable prompt templates
- Warning displays (cost limits, token limits approaching)

**Rationale:** While helpful for power users, these are UX enhancements that
don't affect core functionality. The basic prompt shows essential information
(where you're querying, which model) which is sufficient for MVP. Token/cost
tracking requires LLM provider integration work across all providers.

**Advanced Input Features** (Future - RFD TBD)

Bubbletea provides basic text input. Advanced features are deferred:

- Persistent command history across sessions (up/down to navigate)
- Tab completion for service names and inline commands
- Fuzzy history search (Ctrl+R style)
- Multiline input with continuation (`\` line endings or Shift+Enter)
- Smart completion based on MCP tool schemas

**Rationale:** Basic text input is sufficient for v1. Advanced input features
require significant bubbletea component development and careful UX design
(multiline handling, completion UI).

**Conversation Management UI** (Future - RFD TBD)

v1 supports `--resume <id>` to resume conversations by explicit ID. Advanced
management deferred:

- Interactive conversation browser (list all, filter, search)
- Session listing command (`coral ask --list-sessions`)
- Export conversations to markdown/JSON for sharing
- Conversation cleanup and archiving
- Session metadata (tags, notes, timestamps in UI)

**Rationale:** Conversations already persist via existing storage. Advanced
management (browsing, searching, exporting) is useful but not critical for
iterative debugging workflow.

**Runtime Configuration Changes** (Future - RFD TBD)

The core implementation uses configuration from `~/.coral/config.yaml`. Runtime
changes require:

- `/model <name>` command to switch models mid-session
- `/colony <name>` command to switch target colony
- In-session configuration overrides
- Validation and error handling for invalid switches

**Rationale:** For MVP, developers can exit and restart with different config.
Runtime switching adds complexity and error cases.

**Collaborative Features** (Future - RFD TBD)

Multi-user debugging sessions and sharing:

- Session sharing (export with sensitive data sanitization)
- `/copy` command to copy responses to clipboard
- Session branching for parallel investigations
- Real-time collaborative sessions

**Rationale:** These are advanced collaborative features that require UX design
and security consideration (data sanitization). Single-user sessions are
sufficient for initial release.

## Appendix

### Bubbletea Architecture & Dependencies

**Core Framework:**

- [`github.com/charmbracelet/bubbletea`](https://github.com/charmbracelet/bubbletea):
  Elm-inspired TUI framework with model-update-view pattern
    - Handles terminal events (keyboard, mouse, resize)
    - Manages concurrent updates via message passing
    - Provides clean separation of state, logic, and rendering

**UI Components:**

- [`github.com/charmbracelet/bubbles`](https://github.com/charmbracelet/bubbles):
  Reusable TUI components
    - `spinner`: Loading indicators for tool calls
    - `textinput`: User input field for questions
    - `viewport`: Scrollable conversation history (future)

**Rendering:**

- [`github.com/charmbracelet/glamour`](https://github.com/charmbracelet/glamour):
  Markdown rendering with syntax highlighting
- [`github.com/charmbracelet/lipgloss`](https://github.com/charmbracelet/lipgloss):
  Styling and layout (colors, borders, alignment)

**Why Bubbletea:**

- **Unified architecture**: Single framework instead of coordinating readline +
  rendering + spinners
- **Natural streaming**: Message-driven updates handle LLM streaming elegantly
- **State management**: Elm architecture prevents race conditions and state bugs
- **Future-proof**: Easy to extend to visual mode (split panes, conversation
  browser) without rewriting
- **Charm ecosystem**: Integrates seamlessly with glamour and lipgloss already
  in use

### Prompt Design

**v1 (Simplified):**

```
<colony> | <model>
> <user input>
```

**Examples:**

```
# Standard prompt
my-app-prod | gemini-1.5-flash
>

# Different model
my-app-prod | claude-3-5-sonnet-20241022
>

# Short colony name
prod | gemini-1.5-pro
>
```

**Future enhancements (deferred):**

```
# With token and cost tracking (future RFD)
my-app-prod | gemini-1.5-flash | Tokens: 2,450 | Cost: $0.04
>

# With warnings (future RFD)
my-app-prod | gemini-1.5-flash | Tokens: 7,890/8,192 | ⚠ Context 96% full
>
```

**Rationale for simplified v1:**

- Focus on essential information: where (colony) and how (model)
- Token/cost tracking requires provider-specific integration work
- Warnings require thresholds and user configuration
- Can add complexity later without breaking UX

### Streaming Response Format

**Progressive rendering:**

```
> what's causing high latency?

⠋ Analyzing... (query_trace_data)     ← Spinner while tool executes
✓ Analyzed 3 services (2.1s)          ← Tool complete

The payment-api service shows...       ← Streaming starts (word-by-word or chunk)
                                        ← Continues streaming
Evidence:                               ← Markdown formatted as it arrives
- DB connection pool at 94%...         ←

```

**Technical approach:**

```go
// Simplified streaming implementation
for chunk := range agent.StreamResponse(ctx, query) {
    switch chunk.Type {
    case "tool_start":
        ui.ShowSpinner(chunk.ToolName)
    case "tool_complete":
        ui.HideSpinner()
        ui.Printf("✓ %s (%.1fs)\n", chunk.ToolName, chunk.Duration)
    case "content":
        ui.WriteMarkdown(chunk.Content) // Renders incrementally
    case "error":
        ui.PrintError(chunk.Error)
    }
}
```

### Conversation Storage Format

**Reuses existing storage from RFD 030 with datetime-based naming:**

```
~/.coral/conversations/
├── <colony-id>/
│   ├── last.json                        # Metadata for --continue
│   ├── 2026-02-15-143022-abc123.json   # YYYY-MM-DD-HHMMSS-<random>
│   ├── 2026-02-15-150315-def456.json   # Naturally ordered by time
│   └── 2026-02-14-091234-789abc.json   # Older conversation
└── <another-colony>/
    └── ...
```

**File naming format:** `YYYY-MM-DD-HHMMSS-<random>.json`

- **Timestamp prefix**: Natural chronological ordering when listing (`ls`, file browsers)
- **Random suffix**: Handles same-second collisions, provides unique ID
- **Benefits**: Easy to find recent conversations, simple cleanup by date, human-readable

**Conversation file format** (existing `Message` type):

```json
[
  {
    "role": "user",
    "content": "what services are running?",
    "timestamp": "2024-01-15T14:30:00Z"
  },
  {
    "role": "assistant",
    "content": "Found 12 services...",
    "timestamp": "2024-01-15T14:30:15Z",
    "tool_calls": [...]
  }
]
```

**Key points:**

- Interactive mode reuses existing conversation storage (no migration needed)
- `--resume <id>` accepts either full filename or just the random suffix
- Auto-saves after each turn (same as `--continue` behavior)
- Datetime naming makes conversations easy to browse and manage
- No separate session metadata index in v1 (deferred to future RFD)

### Input Handling (v1)

**v1 behavior:**

- **Single-line input**: Enter key submits question to LLM
- **No multiline support**: Users must phrase questions as single line (
  sufficient for most queries)
- **No tab completion**: Type inline commands and service names manually
- **Basic bubbletea textinput**: Standard text entry with cursor, backspace, etc.

**Deferred to future RFD:**

- Multiline input (Shift+Enter for newline, Enter to submit)
- Tab completion for inline commands and service names
- Command history navigation (up/down arrows)
- Fuzzy history search (Ctrl+R)

**Rationale:**

Single-line input is sufficient for 95% of queries ("show me errors in
payment-api", "what services are slow?"). Advanced input features require
significant bubbletea component work and careful UX design (completion UI,
multiline handling). Can be added later without breaking changes.

### Integration with Existing Code

This section documents how interactive mode integrates with the existing RFD 030
implementation.

### Existing Code to Reuse

1. **Conversation Storage** (`internal/cli/ask/ask.go`):
    - `loadConversationHistory()` - Load previous messages
    - `saveConversationHistory()` - Save after each turn
    - `getConversationHistoryPath()` - Path construction (update for datetime naming)
    - `generateConversationID()` - Update to generate datetime-based IDs
    - `Message` type - Already defined, reuse as-is

2. **Agent Interface** (`internal/agent/ask/agent.go`):
    - `Agent.Ask()` - Main query function (extend for streaming)
    - `Agent.GetConversationHistory()` - Retrieve messages
    - `Agent.SetConversationHistory()` - Load previous conversation
    - `Agent.Close()` - Cleanup resources

3. **Configuration** (`internal/config/ask_resolver.go`):
    - `ResolveAskConfig()` - Get merged config (global + colony)
    - `ValidateAskConfig()` - Ensure valid config
    - Existing `AskConfig` struct - No changes needed

### Required Changes

1. **CLI Command** (`internal/cli/ask/ask.go`):
    - Change: `Args: cobra.MinimumNArgs(1)` → `cobra.MaximumNArgs(1)`
    - Add: Terminal detection using `golang.org/x/term.IsTerminal()`
    - Add: `--interactive` flag (force interactive mode)
    - Add: `--resume <id>` flag (load conversation by ID or datetime prefix)
    - Add: Route to `runInteractive()` when no args + terminal detected
    - Update: `generateConversationID()` to use datetime format: `YYYY-MM-DD-HHMMSS-<random>`

2. **Agent Streaming** (`internal/agent/ask/agent.go`):
    - Add: Optional `chan tea.Msg` parameter to `Ask()` method
    - Emit: `StreamChunkMsg` as LLM generates text
    - Emit: `ToolStartMsg` / `ToolCompleteMsg` for MCP tool executions
    - Emit: `ErrorMsg` on failures
    - Backward compatible: Existing streaming to stdout still works

### New Code to Write

1. **Bubbletea UI** (`internal/cli/ask/ui/`):
    - `model.go`: State struct and Init()
    - `update.go`: Message handling and state transitions
    - `view.go`: Rendering logic with glamour/lipgloss
    - `commands.go`: Side effects (agent calls, save)
    - `messages.go`: Custom message types

2. **Interactive Entry Point** (`internal/cli/ask/interactive.go`):
    - `runInteractive()`: Initialize bubbletea program
    - Setup: Create or resume conversation
    - Teardown: Save conversation on exit

### Example Integration Flow

```go
// in internal/cli/ask/ask.go

// generateConversationID creates datetime-based conversation ID
func generateConversationID() string {
    // Format: YYYY-MM-DD-HHMMSS-<random>
    timestamp := time.Now().Format("2006-01-02-150405")

    // Add random suffix for uniqueness
    b := make([]byte, 6)
    if _, err := rand.Read(b); err != nil {
        return timestamp + "-" + fmt.Sprintf("%d", os.Getpid())
    }
    return timestamp + "-" + hex.EncodeToString(b)[:6]
}

func NewAskCmd() *cobra.Command {
    cmd := &cobra.Command{
        Args: cobra.MaximumNArgs(1), // Changed from MinimumNArgs(1)
        RunE: func(cmd *cobra.Command, args []string) error {
            // Detect interactive mode
            if len(args) == 0 && !interactive && !resume {
                if !term.IsTerminal(int(os.Stdin.Fd())) {
                    return fmt.Errorf("interactive mode requires a terminal")
                }
                interactive = true // Auto-detect
            }

            if interactive || resume != "" {
                return runInteractive(cmd.Context(), colonyID, resume)
            }

            question := strings.Join(args, " ")
            return runAsk(cmd.Context(), question, ...)
        },
    }
    // Add flags...
}
```
