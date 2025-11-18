---
rfd: "046"
title: "Coral Ask - Interactive Terminal Mode"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: ["030"]
database_migrations: []
areas: ["ai", "cli", "ux"]
---

# RFD 046 - Coral Ask: Interactive Terminal Mode

**Status:** 🚧 Draft

## Summary

Extend `coral ask` (RFD 030) with an interactive terminal mode that provides a long-running conversational interface similar to Claude Code or GitHub Codex. Instead of single-shot queries, developers get a persistent REPL-style session with rich terminal UI, streaming responses, conversation history, and inline commands for session management.

## Problem

**Current behavior/limitations:**

- RFD 030 describes `coral ask "question"` as a single-shot command
- Multi-turn conversations require `--continue` flag and re-running the command
- No persistent session for iterative debugging workflows
- Each invocation has startup overhead (agent connection, context loading)
- No rich terminal UI (no syntax highlighting, tables, or interactive elements)

**Why this matters:**

- Real debugging sessions are iterative: "What's slow?" → "Show traces" → "Filter by errors" → "Compare to yesterday"
- Single-shot commands break flow: re-typing `coral ask` and `--continue` is tedious
- Developers expect REPL-like UX: type question, get answer, ask follow-up immediately
- Modern AI tools (Claude Code, Cursor, Copilot) use persistent interactive sessions
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

- **Long debugging sessions**: Maintaining context across 10+ queries without re-running commands

- **Learning/onboarding**: New developers exploring system state interactively

## Solution

Add an interactive terminal mode to `coral ask` that spawns a persistent session with a conversational interface. When invoked without a question (`coral ask` or `coral ask --interactive`), the CLI enters a REPL-style loop with:

- **Persistent context**: Conversation history maintained across all queries in the session
- **Rich terminal UI**: Streaming responses, syntax highlighting, tables, progress indicators
- **Inline commands**: Session management commands (e.g., `/help`, `/clear`, `/save`, `/exit`)
- **Smart input handling**: Multiline prompts, history navigation, tab completion
- **Fast iteration**: No startup overhead between questions (agent process stays warm)

**Key Design Decisions:**

- **REPL-style interface**: Similar to `python`, `node`, or `psql` interactive modes
  - Prompt shows session context (current colony, model, token usage)
  - Commands prefixed with `/` (e.g., `/help`, `/context`) to distinguish from natural language
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
- **Improved productivity**: Maintain flow during debugging, no context switching
- **Session continuity**: Resume work after breaks, share debugging sessions with team
- **Lower cognitive load**: Don't need to remember previous queries, history is visible

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
     - Markdown rendering with syntax highlighting (`github.com/charmbracelet/glamour`)
     - Table formatting for structured data (`github.com/olekukonko/tablewriter`)
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

  # Interactive mode settings
  interactive:
    # Enable rich terminal features (disable for basic terminals)
    rich_output: true

    # Markdown rendering in responses
    render_markdown: true

    # Syntax highlighting for code blocks
    syntax_highlighting: true

    # Show progress indicators for MCP tool calls
    show_progress: true

    # Prompt customization
    prompt:
      show_colony: true
      show_model: true
      show_token_count: true
      show_cost: true
      format: "{{.Colony}} | {{.Model}} | Tokens: {{.Tokens}}"

    # History settings
    history:
      max_entries: 1000             # Max commands in history
      file: "~/.coral/ask-history"  # History file location
      search_mode: "fuzzy"          # "fuzzy" | "prefix"

    # Session persistence
    sessions:
      auto_save: true
      directory: "~/.coral/ask-sessions"
      max_age_days: 30              # Auto-delete old sessions
      export_include_metadata: true

    # Rendering options
    render:
      table_style: "rounded"        # "ascii" | "rounded" | "bold"
      code_theme: "dracula"         # Syntax highlighting theme
      max_table_width: 120          # Wrap tables at this width

    # Behavior
    auto_continue: true             # Don't need --continue, session is persistent
    confirm_destructive: true       # Confirm commands like /clear, /exit
```

## Implementation Plan

### Phase 1: Basic REPL Loop

- [ ] Implement interactive mode detection (no args or `--interactive`)
- [ ] Add readline library dependency (`github.com/chzyer/readline`)
- [ ] Create basic REPL loop: prompt → read → send to agent → display → repeat
- [ ] Implement `/exit` and `/help` inline commands
- [ ] Handle Ctrl+C gracefully (cancel query, stay in session)
- [ ] Handle Ctrl+D (exit session)

### Phase 2: Rich Output Rendering

- [ ] Add markdown rendering library (`github.com/charmbracelet/glamour`)
- [ ] Implement streaming response display (character-by-character or chunked)
- [ ] Add syntax highlighting for code blocks
- [ ] Implement table formatting for structured data
- [ ] Add progress indicators for MCP tool calls
- [ ] Implement color support with NO_COLOR env variable fallback

### Phase 3: Session Management

- [ ] Implement session persistence (JSON storage)
- [ ] Auto-save conversation after each turn
- [ ] Implement `/save`, `/load`, `/clear` commands
- [ ] Add `--resume <session-id>` CLI flag
- [ ] Implement session listing: `coral ask --list-sessions`
- [ ] Add session export: `coral ask --export <id> --format md|json`

### Phase 4: Enhanced UX

- [ ] Implement dynamic prompt (show colony, model, tokens)
- [ ] Add command history (up/down arrows) with persistence
- [ ] Implement tab completion for inline commands
- [ ] Add multiline input support (e.g., `\` for continuation)
- [ ] Implement `/context` command (show conversation history summary)
- [ ] Add token usage warnings in prompt (approaching limits)
- [ ] Implement cost tracking display in prompt

### Phase 5: Advanced Features

- [ ] Add `/model <name>` command to switch models mid-session
- [ ] Implement `/colony <name>` command to switch target colony
- [ ] Add `/export` inline command (export current session)
- [ ] Implement fuzzy history search (Ctrl+R)
- [ ] Add `/copy` command to copy last response to clipboard
- [ ] Implement session sharing (export with sanitization)

### Phase 6: Testing & Documentation

- [ ] Unit tests: REPL loop, inline commands, session persistence
- [ ] Integration tests: streaming output, markdown rendering, table formatting
- [ ] E2E tests: full interactive session workflow
- [ ] Terminal compatibility tests (basic vs rich terminals)
- [ ] Documentation: interactive mode guide, inline commands reference
- [ ] Video demo: screencast showing interactive debugging workflow

## API Changes

### CLI Commands

```bash
# Enter interactive mode (no question provided)
coral ask

# Expected output:
Welcome to Coral Ask (interactive mode)
Type /help for commands, /exit to quit

Colony: my-app-prod-xyz | Model: gpt-4o-mini | Tokens: 0
> what services are running?

✓ Querying service topology... 0.8s

Found 12 services in production:

Service         | Status  | Instances | CPU    | Memory
────────────────┼─────────┼───────────┼────────┼────────
frontend        | healthy | 3         | 24%    | 512MB
payment-api     | healthy | 5         | 67%    | 1.2GB
checkout        | healthy | 4         | 45%    | 890MB
...

Colony: my-app-prod-xyz | Model: gpt-4o-mini | Tokens: 285
> show metrics for payment-api

⠋ Fetching metrics... (payment-api, last 1h)

---

# Explicit interactive flag (same as above)
coral ask --interactive

# Resume previous session
coral ask --resume abc123

# List all saved sessions
coral ask --list-sessions

# Output:
Session ID  | Started             | Turns | Last Activity
────────────┼─────────────────────┼───────┼──────────────────────
abc123      | 2024-01-15 14:30    | 12    | 2 hours ago
def456      | 2024-01-14 09:15    | 8     | 1 day ago
ghi789      | 2024-01-12 16:45    | 23    | 3 days ago

# Export session to markdown
coral ask --export abc123 --format markdown > debug-session.md

# Export session to JSON (includes metadata)
coral ask --export abc123 --format json > session-data.json
```

### Inline Commands

Commands available within interactive session (prefixed with `/`):

```
/help                   Show available commands
/exit, /quit            Exit interactive session
/clear                  Clear conversation history (start fresh)
/save [name]            Save session with optional name
/load <session-id>      Load a previous session
/context                Show conversation history summary
/model <name>           Switch to different LLM model
/colony <name>          Switch to different colony
/export [format]        Export session (markdown|json)
/copy                   Copy last response to clipboard
/tokens                 Show detailed token usage breakdown
/cost                   Show cost summary for current session
/config                 Show current configuration
/history                Show command history
```

**Example usage:**

```
Colony: my-app-prod-xyz | Model: gpt-4o-mini | Tokens: 450
> /help

Available commands:
  /help      - Show this help message
  /exit      - Exit interactive session
  /clear     - Clear conversation history
  /context   - Show conversation summary
  /model     - Switch LLM model
  /colony    - Switch target colony
  /export    - Export session to file
  /copy      - Copy last response to clipboard

Natural language queries:
  Just type your question (no prefix needed)

Examples:
  > what's causing high latency?
  > show me error logs for payment-api
  > compare current metrics to yesterday

Colony: my-app-prod-xyz | Model: gpt-4o-mini | Tokens: 450
> /model anthropic:claude-3-5-sonnet-20241022

✓ Switched to model: anthropic:claude-3-5-sonnet-20241022
  Note: Higher quality, increased cost ($0.015/1k tokens vs $0.0015/1k)

Colony: my-app-prod-xyz | Model: claude-3-5-sonnet | Tokens: 450
>
```

### Session Export Format

**Markdown export** (for sharing/documentation):

```markdown
# Coral Ask Session: abc123

**Started:** 2024-01-15 14:30:00 UTC
**Colony:** my-app-prod-xyz
**Model:** openai:gpt-4o-mini
**Duration:** 45 minutes
**Turns:** 12
**Total Tokens:** 8,450 (input: 5,200, output: 3,250)
**Estimated Cost:** $0.12

---

## Turn 1

**User:**
what's causing high latency?

**Assistant:**
Analyzing traces (3 services)... ✓

The payment-api service shows elevated p95 latency:

| Service     | p50   | p95    | p99    | Change    |
|-------------|-------|--------|--------|-----------|
| payment-api | 120ms | 1400ms | 2800ms | +85% (1h) |
| checkout    | 80ms  | 200ms  | 350ms  | +12% (1h) |

Evidence:
- DB connection pool at 94% utilization
- 127 slow query warnings in last hour

---

## Turn 2

**User:**
show me the slow queries

**Assistant:**
[... next response ...]

---

[... continues for all turns ...]
```

**JSON export** (for programmatic analysis):

```json
{
  "session_id": "abc123",
  "metadata": {
    "started_at": "2024-01-15T14:30:00Z",
    "ended_at": "2024-01-15T15:15:00Z",
    "duration_seconds": 2700,
    "colony": "my-app-prod-xyz",
    "model": "openai:gpt-4o-mini"
  },
  "usage": {
    "total_tokens": 8450,
    "input_tokens": 5200,
    "output_tokens": 3250,
    "estimated_cost_usd": 0.12
  },
  "turns": [
    {
      "turn_number": 1,
      "timestamp": "2024-01-15T14:30:15Z",
      "user_input": "what's causing high latency?",
      "assistant_response": "Analyzing traces...",
      "tool_calls": [
        {
          "tool": "query_trace_data",
          "parameters": {"service_id": "payment-api", "window": "1h"},
          "duration_ms": 850
        }
      ],
      "tokens": {
        "input": 450,
        "output": 230
      }
    }
  ]
}
```

## Testing Strategy

### Unit Tests

- **REPL loop**: Command parsing, inline command routing, exit handling
- **Session persistence**: Save/load conversation, JSON serialization
- **Prompt formatting**: Token count display, cost calculation, dynamic fields
- **Input handling**: Multiline support, history navigation, tab completion

### Integration Tests

- **Streaming output**: Verify responses render progressively (not all at once)
- **Markdown rendering**: Code blocks, tables, lists formatted correctly
- **Session continuity**: Context maintained across multiple turns
- **Model switching**: `/model` command updates agent, prompt reflects change
- **Terminal compatibility**: Graceful degradation in non-rich terminals

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

**Scenario 2: Session Resume**

```bash
# Setup: Start session, save, exit, resume
coral ask <<EOF
what's causing high latency?
/save debug-session
/exit
EOF

coral ask --resume debug-session <<EOF
show me the slow queries
/exit
EOF

# Verify: Second session has context from first session
```

**Scenario 3: Rich Output Rendering**

```bash
# Setup: Query that returns table data
coral ask <<EOF
show service metrics
/exit
EOF

# Verify: Output contains formatted table (not raw JSON)
```

**Scenario 4: Interruption Handling**

```bash
# Setup: Send Ctrl+C during LLM response
# (simulated in test via context cancellation)

# Verify: Current query cancelled, session remains active, can ask new question
```

**Scenario 5: Export Session**

```bash
# Setup: Interactive session → export to markdown
coral ask <<EOF
what's the system status?
/export markdown
/exit
EOF

# Verify: Export contains conversation in markdown format
```

## Security Considerations

### Session Data Storage

**Sensitive data exposure:**

- Sessions stored locally may contain production telemetry data (logs, traces, metrics)
- Session files should have restrictive permissions (0600, user-only read/write)
- Session directory (`~/.coral/ask-sessions/`) should be mode 0700

**Mitigations:**

```go
// When creating session directory
os.MkdirAll(sessionDir, 0700)

// When writing session files
os.WriteFile(sessionPath, data, 0600)
```

### Session Sharing/Export

**Data leakage risks:**

- Exported sessions may contain sensitive information (API keys in logs, PII, secrets)
- Markdown exports designed for sharing, need sanitization option

**Mitigations:**

- Add `--sanitize` flag to export command (redact known secret patterns)
- Display warning when exporting: "Review for sensitive data before sharing"
- Document best practices for session sharing

**Example:**

```bash
# Export with sanitization (redacts API keys, tokens, etc.)
coral ask --export abc123 --sanitize --format markdown > session-safe.md

⚠️  Warning: Review exported file for sensitive data before sharing
    Automatic sanitization applied, but manual review recommended.
```

### Terminal Escape Sequences

**Injection risk:**

- Malicious log messages containing ANSI escape sequences
- Could manipulate terminal display or execute commands (in theory)

**Mitigations:**

- Sanitize all data received from Colony MCP before rendering
- Use terminal library's built-in escaping (`glamour`, `tablewriter` handle this)
- Strip unknown escape sequences from untrusted input

## Future Enhancements

- **Collaborative sessions**: Multiple users in same session (shared debugging)
- **Session replay**: Step through previous session turn-by-turn
- **Visual mode**: TUI with split panes (conversation + context viewer)
- **Voice input**: Dictate questions instead of typing (accessibility)
- **Smart suggestions**: Auto-suggest next questions based on context
- **Session branching**: Fork conversation to explore alternative debugging paths
- **Shortcuts/aliases**: User-defined shortcuts for common queries
- **Plugin system**: Custom renderers for domain-specific data (e.g., Kubernetes resources)

## Appendix

### Terminal UI Libraries (Go)

**Input handling:**

- [`github.com/chzyer/readline`](https://github.com/chzyer/readline): Full readline implementation (history, editing, completion)
- [`github.com/peterh/liner`](https://github.com/peterh/liner): Alternative with simpler API

**Output rendering:**

- [`github.com/charmbracelet/glamour`](https://github.com/charmbracelet/glamour): Markdown rendering with syntax highlighting
- [`github.com/charmbracelet/lipgloss`](https://github.com/charmbracelet/lipgloss): Style definitions for terminal output
- [`github.com/olekukonko/tablewriter`](https://github.com/olekukonko/tablewriter): ASCII table formatting
- [`github.com/briandowns/spinner`](https://github.com/briandowns/spinner): Progress spinners

**Full TUI frameworks** (for future visual mode):

- [`github.com/charmbracelet/bubbletea`](https://github.com/charmbracelet/bubbletea): Elm-inspired TUI framework
- [`github.com/rivo/tview`](https://github.com/rivo/tview): Rich TUI components (tables, forms, etc.)

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
        ui.WriteMarkdown(chunk.Content)  // Renders incrementally
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
- Alternative: Detect incomplete markdown (e.g., opening code fence without closing)

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
- **Non-intrusive**: Rich features when available, graceful degradation for basic terminals
- **Fast feedback**: Streaming output, progress indicators, immediate responses
- **Persistent context**: No mental overhead remembering previous queries
- **Easy sharing**: Export sessions for collaboration and documentation

**Relationship to RFD 030:**

- RFD 030 defines the architecture (Genkit agent, MCP integration, model selection)
- RFD 046 focuses on UX layer (interactive terminal, session management, rich output)
- Both RFDs combine to deliver complete `coral ask` experience
- Single-shot mode (`coral ask "question"`) remains supported for scripting/automation
- Interactive mode is additive, no breaking changes to RFD 030

**When to use interactive vs single-shot:**

- **Interactive mode**: Active debugging, exploration, learning, iterative analysis
- **Single-shot mode**: Scripting, automation, CI/CD integration, simple status checks
