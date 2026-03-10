# MCP Proxying and LLM Interfacing

Coral integrates AI into the observability workflow using the **Model Context
Protocol (MCP)**. This allows LLMs to interact directly with the distributed
system's telemetry and debugging tools in a standardized way.

## Architecture Overview

The architecture follows a client-server pattern where the **Colony** acts as
the Tool Provider (MCP Server) and the **CLI (`coral ask`)** acts as the
Orchestrator (MCP Client).

### 1. The MCP Server (`internal/colony/mcp`)

The Colony hosts a full MCP server that bridges the LLM to Coral's internal
capabilities.

- **Tool Registry**: Statically registers tools like `coral_query_summary`,
  `coral_shell_exec`, `coral_attach_uprobe`, and `coral_deploy_correlation`.
- **Schema Generation**: Automatically generates JSON Schemas from Go structs
  using reflection. This ensures that the LLM always receives accurate type
  information for tool arguments.
- **Transports**:
  - **Stdio**: Used for local proxying where the CLI spawns a Colony
    subprocess.
  - **SSE (Server-Sent Events)**: Provided via
    `internal/colony/httpapi/mcp_sse.go` for web-based or remote LLM
    integrations.

### 2. The MCP Proxy Model

When a user runs `coral ask` locally:

1. The CLI starts a background subprocess: `coral colony mcp proxy`.
2. This proxy establishes a Stdio-based MCP connection.
3. The CLI (Client) can now discover and execute tools hosted by the Colony (
   Server) over this secure pipe.

## LLM Interfacing (`internal/llm`)

Coral abstracts LLM providers to remain vendor-agnostic.

### Provider Abstraction

- **Interface**: The `Provider` interface defines a `Generate` method that
  handles messages, tool definitions, and streaming.
- **Registry**: Supports multiple backends including **Google (Gemini)**, \*
  \*OpenAI (GPT-4)**, and **Ollama\*\* for local execution.
- **Tool Translation**: Translates MCP tool definitions into the specific format
  required by the provider (e.g., Gemini's `FunctionDeclaration` or OpenAI's
  `tools`).

### The Agent Loop (`internal/cli/ask/agent.go`)

The `Agent` manages the high-level reasoning loop:

1. **Context Discovery**: Before the first user prompt, the Agent calls
   `coral_list_services` to populate the system prompt with "live" knowledge of
   the environment.
2. **System Prompting**: Injects "Parameter Extraction Rules" and current
   healthy/unhealthy snapshots to guide the LLM's tool selection.
3. **Execution Loop**:
   - LLM requests one or more tool calls.
   - Agent executes them via the MCP client.
   - Results are fed back into the LLM for final synthesis.
4. **Persistence**: Conversation history is stored locally in
   `~/.coral/conversations/` to support multi-turn debugging sessions (
   `--continue`).

## Security and RBAC

- **Token Scoping**: MCP tool execution over SSE is protected by API tokens.
- **Permission Mapping**: Every MCP tool is mapped to a specific permission (
  e.g., `PermissionMCPToolShellExec`). This ensures that an LLM with a "
  readonly" token cannot execute shell commands on the agents.

## Future Engineering Note

### Agentic MCP

As the system evolves, agents might be deployed _inside_ the Colony to perform
autonomous remediation. This would move the "Agent Loop" from the CLI to a
background service in the Colony, using the same MCP infrastructure but with
long-running execution context.

### Stateful MCP Loops (RFD 091)

With the introduction of the agent-side `CorrelationEngine`, the MCP server
now supports "watching" tools. The LLM can deploy a correlation and then wait
for a `TriggerEvent` async notification, rather than polling the `debug`
service repeatedly.

### Integrated Investigation Skills (RFD 093)

The Correlation Engine acts as the "sensor" for the high-level **Skills**
framework. Instead of the LLM writing raw correlations, it can invoke a named
Skill (e.g., `latency-watch`) which abstracts the descriptor creation and
result synthesis into a single, automated workflow.

## Related Design Documents (RFDs)

- [**RFD 004**: MCP Server](../../RFDs/004-mcp-server.md)
- [**RFD 005**: CLI Local Proxy](../../RFDs/005-cli-local-proxy.md)
- [**RFD 014
  **: Colony LLM Integration](../../RFDs/014-colony-llm-integration.md)
- [**RFD 030**: Coral Ask Local Agent](../../RFDs/030-coral-ask-local.md)
- [**RFD 031**: Colony Dual Interface](../../RFDs/031-colony-dual-interface.md)
- [**RFD 051
  **: Coral Ask Interactive Terminal](../../RFDs/051-coral-ask-interactive-terminal.md)
- [**RFD 054
  **: Smart Parameter Extraction](../../RFDs/054-coral-ask-smart-parameter-extraction.md)
- [**RFD 055**: Coral Ask Configuration](../../RFDs/055-coral-ask-config.md)
- [**RFD 091**: Probe Correlation DSL](../../RFDs/091-probe-correlation-dsl.md)
