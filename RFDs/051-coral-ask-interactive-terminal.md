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

## Summary

Extend `coral ask` (RFD 030) with an interactive terminal mode that provides a
REPL-style conversational interface. Instead of single-shot queries, developers
get a persistent session with rich terminal UI (markdown rendering, syntax
highlighting, tables), streaming responses, and conversation context maintained
during the active session. Advanced features like session persistence, dynamic
prompts, and runtime configuration are deferred to future RFDs to keep the
initial implementation focused and shippable.

## Problem

**Current behavior/limitations:**

- RFD 030 describes `coral ask "question"` as a single-shot command
- Multi-turn conversations require `--continue` flag and re-running the command
- No persistent session for iterative debugging workflows
- Each invocation has startup overhead (agent connection, context loading)
- No rich terminal UI (no syntax highlighting, tables, or interactive elements)

**Why this matters:**

- Real debugging sessions are iterative: "What's slow?" → "Show traces" → "
  Filter by errors" → "Compare to yesterday"
- Single-shot commands break flow: re-typing `coral ask` and `--continue` is
  tedious
- Developers expect REPL-like UX: type question, get answer, ask follow-up
  immediately
- Modern AI tools (Claude Code, Cursor, Copilot) use persistent interactive
  sessions
- Rich output (tables, code blocks, syntax highlighting) improves readability

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
with a conversational interface. When invoked without a question (`coral ask` or
`coral ask --interactive`), the CLI enters a REPL-style loop with:

- **Persistent context**: Conversation history maintained across all queries in
  the session
- **Rich terminal UI**: Streaming responses, syntax highlighting, tables,
  progress indicators
- **Inline commands**: Session management commands (e.g., `/help`, `/clear`,
  `/save`, `/exit`)
- **Smart input handling**: Multiline prompts, history navigation, tab
  completion
- **Fast iteration**: No startup overhead between questions (agent process stays
  warm)

**Key Design Decisions:**

- **REPL-style interface**: Similar to `python`, `node`, or `psql` interactive
  modes
    - Prompt shows session context (current colony, model, token usage)
    - Commands prefixed with `/` (e.g., `/help`, `/context`) to distinguish from
      natural language
    - Standard readline bindings (Ctrl+A/E, arrow keys, history search)

- **Streaming output**: LLM responses stream to terminal character-by-character
    - Visual feedback that processing is happening (not stuck)
    - Users can start reading before full response completes
    - Interruptible (Ctrl+C cancels current query, stays in session)

- **Rich rendering**: Structured output formatted for readability
    - Markdown rendering (headers, lists, code blocks with syntax highlighting)
    - Tables for metrics/trace data (auto-formatted columns)
    - Progress bars for long-running MCP tool calls
    - Color-coded severity (errors in red, warnings in yellow)

- **Session persistence**: Save/restore conversations
    - Sessions auto-saved to `~/.coral/ask-sessions/<session-id>.json`
    - Resume previous session with `coral ask --resume <session-id>`
    - Export session for sharing: `coral ask --export session.md`

- **Context awareness**: Display current context in prompt
    - Show active colony, model, token count, session duration
    - Warning when approaching token limits (context window full)

**Benefits:**

- **Faster iteration**: No startup overhead, instant follow-up questions
- **Better UX**: Familiar REPL interface, rich output formatting
- **Improved productivity**: Maintain flow during debugging, no context
  switching
- **Session continuity**: Resume work after breaks, share debugging sessions
  with team
- **Lower cognitive load**: Don't need to remember previous queries, history is
  visible

**Architecture Overview:**

```
Terminal (Interactive Mode)
┌────────────────────────────────────────────────────────────┐
│ coral ask (interactive session)                            │
│                                                             │
│ Colony: my-app-prod-xyz | Model: gpt-4o-mini | Tokens: 450│
│ ──────────────────────────────────────────────────────────│
│                                                             │
│ > what's causing high latency?                             │
│                                                             │
│ ✓ Analyzing traces (3 services)... 2.1s                   │
│ ✓ Queried metrics (last 1h)... 0.8s                       │
│                                                             │
│ ## Root Cause Analysis                                     │
│                                                             │
│ The payment-api service shows elevated p95 latency:        │
│                                                             │
│ Service       | p50    | p95     | p99     | Change        │
│ ──────────────┼────────┼─────────┼─────────┼───────────   │
│ payment-api   | 120ms  | 1400ms  | 2800ms  | +85% (1h)    │
│ checkout      | 80ms   | 200ms   | 350ms   | +12% (1h)    │
│                                                             │
│ Evidence:                                                   │
│ • DB connection pool at 94% utilization (threshold: 80%)   │
│ • 127 slow query warnings in last hour                     │
│                                                             │
│ > show me the slow queries                                 │
│                                                             │
│ ⠋ Fetching logs... (payment-api)                           │
│                                                             │
│ [Ctrl+C to cancel, /help for commands, /exit to quit]      │
└────────────────────────────────────────────────────────────┘
         ↕ (readline, streaming, formatting)
┌────────────────────────────────────────────────────────────┐
│ Genkit Agent (daemon mode from RFD 030)                    │
│                                                             │
│ • Maintains conversation history (all turns)               │
│ • Executes MCP tool calls                                  │
│ • Streams LLM responses back to terminal                   │
│ • Tracks token usage for prompt display                    │
└────────────────────────────────────────────────────────────┘
         ↕ MCP
┌────────────────────────────────────────────────────────────┐
│ Colony MCP Server (RFD 004)                                │
│                                                             │
│ • query_trace_data, query_metrics, search_logs, etc.       │
└────────────────────────────────────────────────────────────┘
```

### Component Changes

1. **CLI (`internal/cli/ask`)** (extend existing):
    - Detect interactive mode: no arguments OR `--interactive` flag
    - Initialize interactive session with rich terminal UI library
    - Implement REPL loop: read → send to agent → stream response → repeat
    - Add inline command handlers (`/help`, `/clear`, `/save`, `/exit`, etc.)
    - Implement session persistence (save/load conversation history)

2. **Terminal UI (`internal/cli/ask/ui`)** (new package):
    - **Input handling**: Multiline prompt with readline support
        - Use `github.com/chzyer/readline` or similar for line editing
        - Command history (up/down arrows) persisted across sessions
        - Tab completion for inline commands and service names
    - **Output rendering**: Stream LLM responses with rich formatting
        - Markdown rendering with syntax highlighting (
          `github.com/charmbracelet/glamour`)
        - Table formatting for structured data (
          `github.com/olekukonko/tablewriter`)
        - Progress indicators for tool calls (`github.com/briandowns/spinner`)
        - Color support with fallback for no-color terminals
    - **Context display**: Dynamic prompt showing session metadata
        - Current colony, model, token count, cost estimate
        - Warning indicators (high token usage, cost limits)

3. **Session Manager (`internal/cli/ask/session`)** (new package):
    - Persist conversation history to local storage
    - Auto-save after each turn (append-only for crash safety)
    - Session listing: `coral ask --list-sessions`
    - Resume session: `coral ask --resume <id>`
    - Export session: `coral ask --export <id> --format markdown|json`

4. **Agent Integration** (extend existing from RFD 030):
    - Streaming API: agent sends response chunks as they arrive
    - Tool call progress: emit events when calling MCP tools (for progress bars)
    - Interruptibility: support cancellation mid-response (Ctrl+C handling)
    - Token usage reporting: include in response metadata for prompt display

**Configuration Example:**

```yaml
# ~/.coral/config.yaml
ask:
    # Existing config from RFD 030...
    default_model: "openai:gpt-4o-mini"

    # Interactive mode settings (core features only)
    interactive:
        # Enable rich terminal features (disable for basic terminals)
        rich_output: true

        # Markdown rendering in responses
        render_markdown: true

        # Syntax highlighting for code blocks
        syntax_highlighting: true

        # Show progress indicators for MCP tool calls
        show_progress: true

        # Rendering options
        render:
            table_style: "rounded"        # "ascii" | "rounded" | "bold"
            code_theme: "dracula"         # Syntax highlighting theme
            max_table_width: 120          # Wrap tables at this width
```

**Note:** Advanced configuration (dynamic prompts, session persistence, etc.)
deferred to future RFDs.

## Implementation Plan

### Phase 1: Basic REPL Loop

- [ ] Implement interactive mode detection (no args or `--interactive`)
- [ ] Add readline library dependency (`github.com/chzyer/readline`)
- [ ] Create basic REPL loop: prompt → read → send to agent → display → repeat
- [ ] Implement `/exit` and `/help` inline commands
- [ ] Handle Ctrl+C gracefully (cancel query, stay in session)
- [ ] Handle Ctrl+D (exit session)
- [ ] Basic prompt display (static format showing current colony)

### Phase 2: Rich Output Rendering

- [ ] Add markdown rendering library (`github.com/charmbracelet/glamour`)
- [ ] Implement streaming response display (character-by-character or chunked)
- [ ] Add syntax highlighting for code blocks
- [ ] Implement table formatting for structured data
- [ ] Add progress indicators for MCP tool calls
- [ ] Implement color support with NO_COLOR env variable fallback

### Phase 3: Testing & Documentation

- [ ] Unit tests: REPL loop, inline commands, streaming output
- [ ] Integration tests: markdown rendering, table formatting
- [ ] E2E tests: basic interactive session workflow
- [ ] Terminal compatibility tests (basic vs rich terminals)
- [ ] Documentation: interactive mode guide, basic commands reference

## API Changes

### CLI Commands

```bash
# Enter interactive mode (no question provided)
coral ask

# Expected output:
Welcome to Coral Ask (interactive mode)
Colony: my-app-prod-xyz
Type /help for commands, /exit to quit

my-app-prod-xyz> what services are running?

✓ Querying service topology... 0.8s

Found 12 services in production:

Service         | Status  | Instances | CPU    | Memory
────────────────┼─────────┼───────────┼────────┼────────
frontend        | healthy | 3         | 24%    | 512MB
payment-api     | healthy | 5         | 67%    | 1.2GB
checkout        | healthy | 4         | 45%    | 890MB
...

my-app-prod-xyz> show metrics for payment-api

⠋ Fetching metrics... (payment-api, last 1h)

---

# Explicit interactive flag (same as above)
coral ask --interactive

# Single-shot mode still works (for scripting)
coral ask "what's the current status?"
```

### Inline Commands (Core)

Commands available within interactive session (prefixed with `/`):

```
/help                   Show available commands and usage examples
/exit, /quit            Exit interactive session
```

**Note:** Additional commands (session management, runtime config changes) are
deferred to future RFDs. See "Deferred Features" section.

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

New `ask.interactive` section in `~/.coral/config.yaml`:

```yaml
# ~/.coral/config.yaml
ask:
    # Existing config from RFD 030...
    default_model: "openai:gpt-4o-mini"

    # Interactive mode settings (core features only)
    interactive:
        # Enable rich terminal features (disable for basic terminals)
        rich_output: true

        # Markdown rendering in responses
        render_markdown: true

        # Syntax highlighting for code blocks
        syntax_highlighting: true

        # Show progress indicators for MCP tool calls
        show_progress: true

        # Rendering options
        render:
            table_style: "rounded"        # "ascii" | "rounded" | "bold"
            code_theme: "dracula"         # Syntax highlighting theme
            max_table_width: 120          # Wrap tables at this width
```

**Note:** Session persistence, dynamic prompts, and advanced configuration
options are deferred. See "Deferred Features" section.

## Testing Strategy

### Unit Tests

- **REPL loop**: Command parsing, inline command routing, exit handling
- **Input handling**: Basic readline integration, history navigation (up/down
  arrows)
- **Output rendering**: Markdown parsing, syntax highlighting, table formatting

### Integration Tests

- **Streaming output**: Verify responses render progressively (not all at once)
- **Markdown rendering**: Code blocks, tables, lists formatted correctly
- **Session continuity**: Context maintained across multiple turns within active
  session
- **Terminal compatibility**: Graceful degradation in non-rich terminals (
  NO_COLOR support)

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

## Security Considerations

### Terminal Escape Sequences

**Injection risk:**

- Malicious log messages containing ANSI escape sequences
- Could manipulate terminal display or execute commands (in theory)

**Mitigations:**

- Sanitize all data received from Colony MCP before rendering
- Use terminal library's built-in escaping (`glamour`, `tablewriter` handle
  this)
- Strip unknown escape sequences from untrusted input

## Deferred Features

The following features are deferred to keep the core implementation focused and
shippable. These will be addressed in follow-up RFDs once the basic interactive
mode is proven and stable.

**Session Persistence & Management** (Future - RFD TBD)

The core interactive mode maintains context only for the current session
lifetime. Full session persistence requires additional complexity:

- Session storage format and lifecycle management
- Save/resume across CLI invocations (`coral ask --resume <id>`)
- Session listing and cleanup (`coral ask --list-sessions`)
- Export to markdown/JSON for sharing
- Auto-save and crash recovery

**Rationale:** Session persistence adds significant complexity (storage format,
migrations, cleanup) and isn't required for basic iterative debugging.
Developers can still use the interactive mode for active sessions; persistence
is an enhancement.

**Enhanced Prompt & Context Display** (Future - RFD TBD)

The core implementation uses a static prompt format. Dynamic context display
requires:

- Real-time token usage tracking and display
- Cost estimation and warning thresholds
- Context window utilization indicators
- Configurable prompt templates
- Warning displays (cost limits, token limits)

**Rationale:** While helpful, these are UX enhancements that don't affect core
functionality. The basic prompt shows colony name, which is sufficient for MVP.

**Advanced Input Features** (Future - RFD TBD)

Basic readline support is included, but advanced features are deferred:

- Persistent command history across sessions
- Tab completion for service names and commands
- Fuzzy history search (Ctrl+R)
- Multiline input with continuation (`\` line endings)
- Smart completion based on context

**Rationale:** readline provides basic history (up/down arrows) out of the box.
Advanced features require custom completion logic and context awareness.

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

## Future Enhancements

- **Collaborative sessions**: Multiple users in same session (shared debugging)
- **Session replay**: Step through previous session turn-by-turn
- **Visual mode**: TUI with split panes (conversation + context viewer)
- **Voice input**: Dictate questions instead of typing (accessibility)
- **Smart suggestions**: Auto-suggest next questions based on context
- **Session branching**: Fork conversation to explore alternative debugging
  paths
- **Shortcuts/aliases**: User-defined shortcuts for common queries
- **Plugin system**: Custom renderers for domain-specific data (e.g., Kubernetes
  resources)

## Appendix

### Terminal UI Libraries (Go)

**Input handling:**

- [`github.com/chzyer/readline`](https://github.com/chzyer/readline): Full
  readline implementation (history, editing, completion)
- [`github.com/peterh/liner`](https://github.com/peterh/liner): Alternative with
  simpler API

**Output rendering:**

- [
  `github.com/charmbracelet/glamour`](https://github.com/charmbracelet/glamour):
  Markdown rendering with syntax highlighting
- [
  `github.com/charmbracelet/lipgloss`](https://github.com/charmbracelet/lipgloss):
  Style definitions for terminal output
- [
  `github.com/olekukonko/tablewriter`](https://github.com/olekukonko/tablewriter):
  ASCII table formatting
- [`github.com/briandowns/spinner`](https://github.com/briandowns/spinner):
  Progress spinners

**Full TUI frameworks** (for future visual mode):

- [
  `github.com/charmbracelet/bubbletea`](https://github.com/charmbracelet/bubbletea):
  Elm-inspired TUI framework
- [`github.com/rivo/tview`](https://github.com/rivo/tview): Rich TUI
  components (tables, forms, etc.)

### Prompt Design

**Information hierarchy:**

```
<colony> | <model> | Tokens: <count> [| Cost: <usd>] [| ⚠ warnings]
> <user input>
```

**Examples:**

```
# Basic prompt
my-app-prod-xyz | gpt-4o-mini | Tokens: 0
>

# With cost tracking
my-app-prod-xyz | gpt-4o-mini | Tokens: 2,450 | Cost: $0.04
>

# Warning: approaching token limit
my-app-prod-xyz | gpt-4o-mini | Tokens: 7,890/8,192 | ⚠ Context 96% full
>

# Warning: cost limit
my-app-prod-xyz | claude-3-5-sonnet | Tokens: 4,200 | Cost: $1.89 | ⚠ $2.00 limit
>
```

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

### Session Storage Format

**File structure:**

```
~/.coral/ask-sessions/
├── abc123.json          # Session data
├── def456.json
└── index.json           # Session metadata index (for fast listing)
```

**index.json** (for fast session listing without reading all files):

```json
{
    "sessions": [
        {
            "id": "abc123",
            "started_at": "2024-01-15T14:30:00Z",
            "last_activity": "2024-01-15T15:15:00Z",
            "turns": 12,
            "colony": "my-app-prod-xyz",
            "model": "gpt-4o-mini"
        }
    ]
}
```

### Multiline Input Handling

**Approach:**

- Default: Single-line input (Enter submits)
- Multiline trigger: Line ending with `\` continues to next line
- Alternative: Detect incomplete markdown (e.g., opening code fence without
  closing)

**Example:**

```
> show me a query that \
... finds slow traces \
... from the last hour

(submitted as single query: "show me a query that finds slow traces from the last hour")
```

### Tab Completion

**Completion sources:**

- Inline commands: `/help`, `/exit`, `/model`, `/colony`, etc.
- Service names: `payment-api`, `checkout`, `frontend` (from Colony)
- Model names: `gpt-4o-mini`, `claude-3-5-sonnet`, etc. (from config)

**Example:**

```
> show metrics for pay<TAB>
> show metrics for payment-api
```

---

## Notes

**Design Philosophy:**

- **Familiar UX**: Borrow from successful REPLs (`psql`, `python`, `node`)
- **Non-intrusive**: Rich features when available, graceful degradation for
  basic terminals
- **Fast feedback**: Streaming output, progress indicators, immediate responses
- **Session context**: Maintain conversation context during active session (no
  persistence across restarts)
- **Completable scope**: Focus on core REPL experience, defer advanced features
  to follow-up RFDs

**Relationship to RFD 030:**

- RFD 030 defines the architecture (Genkit agent, MCP integration, model
  selection)
- RFD 051 focuses on core UX layer (interactive terminal REPL, rich output
  rendering)
- Both RFDs combine to deliver basic interactive `coral ask` experience
- Single-shot mode (`coral ask "question"`) remains supported for
  scripting/automation
- Interactive mode is additive, no breaking changes to RFD 030
- Advanced features (session persistence, dynamic prompts, etc.) deferred to
  future RFDs

**When to use interactive vs single-shot:**

- **Interactive mode**: Active debugging, exploration, learning, iterative
  analysis
- **Single-shot mode**: Scripting, automation, CI/CD integration, simple status
  checks
