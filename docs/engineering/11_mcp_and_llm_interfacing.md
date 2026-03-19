# MCP Proxying and LLM Interfacing

Coral integrates AI into the observability workflow using the **Model Context
Protocol (MCP)**. LLMs interact with the distributed system through a single
`coral_cli` tool that runs standard coral CLI commands as subprocesses.

## Architecture Overview

All AI clients — `coral terminal`, `coral ask`, and external clients like
Claude Desktop — use **CLI dispatch mode**. The agent composes `coral` commands
and executes them as subprocesses, returning JSON output to the LLM.

```
LLM → coral_cli(["query", "summary", "--service", "api"])
    → subprocess: coral query summary --service api --format json
    → JSON returned to LLM
```

### 1. The MCP Proxy (`internal/cli/colony/mcp.go`)

The `coral colony mcp proxy` command is the only public-facing MCP interface
for external clients. It exposes a single `coral_cli` tool and handles all
calls locally as subprocesses — no colony-side MCP server is involved.

- **One tool**: `coral_cli` accepts an `args` array of coral subcommand tokens.
- **Local execution**: The proxy forks `coral <args> --format json` and returns
  stdout to the LLM.
- **Auditable**: Every LLM action produces a human-readable, reproducible
  command in the session log.

### 2. CLI Dispatch Mode (`internal/cli/ask/agent.go`)

When a user runs `coral ask` or `coral terminal`:

1. The agent initializes in CLI dispatch mode (no MCP connection).
2. It registers a single `coral_cli` tool backed by `executeCLITool`.
3. It builds a compact CLI reference from the Cobra command tree
   (`GenerateCLIReference`) and includes it in the system prompt.
4. The LLM reads the reference and composes `coral_cli` calls using standard
   coral subcommands.

## LLM Interfacing (`internal/llm`)

Coral abstracts LLM providers to remain vendor-agnostic.

### Provider Abstraction

- **Interface**: The `Provider` interface defines a `Generate` method that
  handles messages, tool definitions, and streaming.
- **Registry**: Supports multiple backends including **Anthropic (Claude)**,
  **Google (Gemini)**, **OpenAI (GPT-4)**, and **Ollama** for local execution.
- **Tool Translation**: Translates `coral_cli` tool definitions into the
  specific format required by the provider.

### The Agent Loop (`internal/cli/ask/agent.go`)

The `Agent` manages the high-level reasoning loop:

1. **Context Discovery**: Before the first user prompt, the agent calls
   `coral service list --format json` (via `coral_cli`) to populate the system
   prompt with live service knowledge.
2. **System Prompting**: Injects the CLI reference and current
   healthy/unhealthy snapshots to guide the LLM's command composition.
3. **Execution Loop**:
   - LLM requests `coral_cli` calls with an `args` array.
   - Agent forks the subprocess and captures stdout.
   - Results are fed back into the LLM for synthesis.
4. **Persistence**: Conversation history is stored locally in
   `~/.coral/conversations/` for multi-turn sessions (`--continue`).

## Future Engineering Note

### Stateful MCP Loops (RFD 091)

With the introduction of the `CorrelationEngine`, agents can deploy a
correlation and wait for a `TriggerEvent` async notification, rather than
polling the `debug` service repeatedly.

### Integrated Investigation Skills (RFD 093)

The Correlation Engine acts as the "sensor" for the high-level **Skills**
framework. Instead of the LLM writing raw correlations, it can invoke a named
Skill (e.g., `latency-watch`) which abstracts descriptor creation and result
synthesis into a single automated workflow.

## Related Design Documents (RFDs)

- [**RFD 030**: Coral Ask Local Agent](../../RFDs/030-coral-ask-local.md)
- [**RFD 051**: Coral Ask Interactive Terminal](../../RFDs/051-coral-ask-interactive-terminal.md)
- [**RFD 054**: Smart Parameter Extraction](../../RFDs/054-coral-ask-smart-parameter-extraction.md)
- [**RFD 055**: Coral Ask Configuration](../../RFDs/055-coral-ask-config.md)
- [**RFD 091**: Probe Correlation DSL](../../RFDs/091-probe-correlation-dsl.md)
- [**RFD 100**: CLI-Native Agent Tool Dispatch](../../RFDs/100-cli-native-agent-dispatch.md)
