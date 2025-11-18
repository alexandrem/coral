---
rfd: "045"
title: "MCP Agent Shell Exec Tool"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: [ "004", "026", "044" ]
related_rfds: [ "041", "042", "043" ]
database_migrations: [ ]
areas: [ "mcp", "shell", "debugging" ]
---

# RFD 045 - MCP Agent Shell Exec Tool

**Status:** 🚧 Draft

## Summary

Implement the `agent_shell_exec` MCP tool to enable AI assistants (Claude
Desktop, `coral ask`) to execute commands directly in the agent's environment.
The tool executes one-off commands and returns their output, working within
MCP's request-response model. For interactive shell sessions, users should use
the `coral shell` CLI command.

## Problem

**Current behavior/limitations:**

The `coral shell` command (RFD 026) provides interactive shell access via CLI,
but there's no MCP tool for executing commands in the agent environment. The
`coral_exec_command` tool executes in application containers, but cannot access
agent-level utilities and data. This creates a gap in the MCP server's
debugging capabilities:

1. **No agent command execution via MCP**: AI assistants cannot execute commands
   in the agent environment (tcpdump, DuckDB queries, agent logs)
2. **Missing agent-level debugging**: Tools like eBPF and container exec don't
   provide access to agent's own utilities
3. **Incomplete MCP debugging toolset**: Cannot query agent's local DuckDB,
   check agent logs, or run network diagnostics from agent perspective
4. **Awkward workflow**: AI must provide CLI instructions instead of directly
   executing and returning results

**Why this matters:**

- **AI-assisted debugging**: Developers using Claude Desktop should be able to
  ask "query the agent's DuckDB for HTTP metrics" and get results directly
- **Agent-level access**: Agent environment has unique capabilities (tcpdump,
  agent logs, local DuckDB) not available in application containers
- **Complete MCP interface**: RFD 004 defines MCP as the primary integration
  point - agent command execution completes the debugging toolset
- **Better UX**: Direct execution and results via MCP, no context-switching to
  CLI for one-off commands

**Use cases affected:**

- Developer in Claude Desktop: "Run tcpdump on the api-server agent to capture
  HTTP traffic"
- SRE investigating incident: "Query agent's DuckDB for recent HTTP 500 errors"
- AI-powered runbooks: "Check agent logs for the payment service agent"
- `coral ask` CLI: Needs to execute agent commands and return results via MCP
  tools

**MCP Protocol Consideration:**

MCP tools use a **request-response model** (tool receives JSON input, returns
text output). Interactive shells require **bidirectional streaming** (stdin,
stdout, resize events, signals). This means MCP tools work well for **one-off
command execution** (run command, return output) but cannot support **interactive
sessions**. For interactive debugging, users should use the `coral shell` CLI
command.

## Solution

Implement `agent_shell_exec` as a **command execution tool** that runs commands
in the agent's environment and returns output. The tool queries the colony
registry to find agents, executes the specified command via agent RPC, and
returns stdout/stderr. For interactive sessions, users should use the `coral
shell` CLI command.

**Key Design Decisions:**

- **Direct execution approach**: Tool executes command and returns output, not
  just connection info
- **Registry-based routing**: Use RFD 044's agent resolution logic (agent_id or
  service)
- **Request-response model**: Command completes and returns output (no streaming)
- **Timeout support**: Commands have configurable timeout (default 30s)
- **Security enforcement**: Audit logging and future RBAC integration (RFD 043)
- **Agent environment**: Executes in agent container, not application container

**Why this approach:**

- **Protocol-aligned**: One-off command execution fits MCP's request-response
  model perfectly
- **Direct value**: AI gets results immediately, no need to instruct user to run
  CLI commands
- **Leverages existing infrastructure**: Uses agent gRPC API and registry
- **Clear separation**: `agent_shell_exec` for one-off commands, `coral shell`
  for interactive sessions
- **Complements exec**: `coral_exec_command` for app containers,
  `agent_shell_exec` for agent environment

**Benefits:**

- AI assistants can execute agent commands and get results directly
- Agent-level debugging capabilities exposed via MCP
- Completes the debugging tool suite (eBPF, container exec, agent exec)
- No additional infrastructure needed (uses existing agent gRPC API)

**Architecture Overview:**

```
┌──────────────────────────────────────────────────────┐
│  Claude Desktop / coral ask (MCP Client)             │
└────────────────────┬─────────────────────────────────┘
                     │
                     │ MCP: agent_shell_exec(service: "api-server", command: ["ps", "aux"])
                     ▼
┌──────────────────────────────────────────────────────┐
│  Colony MCP Server                                   │
│  ┌────────────────────────────────────────────────┐  │
│  │ executeAgentShellExecTool()                    │  │
│  │  1. Resolve agent (RFD 044 logic)              │  │
│  │  2. Check agent status (healthy/degraded)      │  │
│  │  3. Call agent.ExecuteCommand(cmd) via gRPC    │  │
│  │  4. Return stdout/stderr + exit code           │  │
│  └────────────────────────────────────────────────┘  │
└────────────────────┬─────────────────────────────────┘
                     │
                     │ Response: "Exit code: 0\n\nOutput:\nroot  1  ... /sbin/init\n..."
                     ▼
┌──────────────────────────────────────────────────────┐
│  Claude Desktop presents results to user             │
└──────────────────────────────────────────────────────┘
```

**Interaction with existing components:**

```
agent_shell_exec (MCP Tool)
  ↓
Registry (resolve agent via RFD 044)
  ↓
Agent gRPC API: ExecuteCommand()
  ↓
Agent executes command in its environment
  ↓
Return stdout/stderr/exit code
  ↓
MCP tool returns formatted output
```

### Component Changes

**1. MCP Tool Execution** (`internal/colony/mcp/tools_exec.go`):

Replace placeholder in `executeAgentShellExecTool()` with:

- Parse `AgentShellExecInput` (service/agent_id, command, timeout, working_dir)
- **Use agent resolution from RFD 044** (Agent ID Standardization):
    - If `agent_id` specified: Direct lookup via `registry.Get(agent_id)`
    - If `service` specified: Filter by `Services[]` array (RFD 044)
    - Handle disambiguation errors per RFD 044 (multiple agents → list agent
      IDs)
- Validate agent status using `registry.DetermineStatus()`
- Execute command via agent gRPC API: `agent.ExecuteCommand()`
- Capture stdout, stderr, exit code
- Handle error cases (agent not found, unhealthy, timeout, command failure)

**2. Response Format**:

The tool returns formatted text with:

- Command execution status (exit code, duration)
- Standard output (stdout)
- Standard error (stderr) if non-empty
- Agent identification (ID, service name)
- Security note about elevated privileges
- Error messages if command failed or timed out

**Configuration Example:**

MCP client usage:

```json
{
    "tool": "agent_shell_exec",
    "arguments": {
        "service": "api-server",
        "command": ["ps", "aux"],
        "timeout_seconds": 10
    }
}
```

Response:

```
Command Execution: ps aux

Agent: agent-abc123 (api-server)
Status: Completed successfully
Exit Code: 0
Duration: 0.12s

Output:
USER       PID %CPU %MEM    VSZ   RSS TTY      STAT START   TIME COMMAND
root         1  0.0  0.1  18376  3428 ?        Ss   10:23   0:00 /sbin/init
coral       42  0.2  1.5 845320 31456 ?        Sl   10:23   0:05 /usr/bin/coral-agent
...

Note: Agent commands run with elevated privileges (CRI socket, eBPF, mesh access).
All executions are audited.
```

## Implementation Plan

### Phase 1: Core Tool Implementation

- [ ] Implement `executeAgentShellExecTool()` in `tools_exec.go`
- [ ] Integrate RFD 044's agent resolution logic (agent_id or service)
- [ ] Add agent status validation
- [ ] Implement command execution via agent gRPC API
- [ ] Capture stdout, stderr, exit code
- [ ] Handle error cases (agent not found, timeout, command failure)

### Phase 2: Response Formatting

- [ ] Create helper function for command output formatting
- [ ] Include exit code and execution duration
- [ ] Format stdout/stderr clearly
- [ ] Include security notes in response
- [ ] Add agent identification details

### Phase 3: Testing

- [ ] Unit tests for agent resolution (RFD 044 integration)
- [ ] Unit tests for command execution and output capture
- [ ] Unit tests for timeout handling
- [ ] Unit tests for error cases (not found, unhealthy, command failure)
- [ ] Integration test with agent gRPC API
- [ ] MCP tool execution test

### Phase 4: Documentation

- [ ] Update tool description in `registerAgentShellExecTool()`
- [ ] Add usage examples in tool documentation
- [ ] Document agent vs container exec distinction
- [ ] Update MCP server documentation with agent_shell_exec usage

## API Changes

No new API endpoints required. Changes are limited to MCP tool implementation.

### MCP Tool Interface

**Tool Name:** `agent_shell_exec`

**Description:** "Execute a command in the agent's environment (not the
application container). Provides access to agent-level debugging tools (tcpdump,
DuckDB queries, agent logs). Returns command output (stdout/stderr) and exit
code."

**Input Schema** (updated per RFD 044):

```go
type AgentShellExecInput struct {
    Service        string   `json:"service" jsonschema:"description=Service whose agent to execute command on (use agent_id for disambiguation)"`
    AgentID        *string  `json:"agent_id,omitempty" jsonschema:"description=Target agent ID (overrides service lookup, from RFD 044)"`
    Command        []string `json:"command" jsonschema:"description=Command and arguments to execute in agent environment (e.g. ['ps' 'aux'])"`
    TimeoutSeconds *int     `json:"timeout_seconds,omitempty" jsonschema:"description=Command timeout in seconds,default=30,maximum=300"`
    WorkingDir     *string  `json:"working_dir,omitempty" jsonschema:"description=Optional working directory for command execution"`
}
```

**Output:** Text string with formatted command output, exit code, and execution metadata.

### Comparison with Other Tools

**Tool Comparison:**

| Tool | Environment | Usage | Interactivity |
|------|-------------|-------|---------------|
| `coral_exec_command` | Application container (via CRI) | One-off commands in app | Request-response |
| `agent_shell_exec` | Agent container | One-off commands in agent | Request-response |
| `coral shell` (CLI) | Agent container | Interactive debugging | Bidirectional streaming |

**Example Commands:**

```bash
# MCP: Execute in agent environment
agent_shell_exec(service="api", command=["duckdb", "-c", "SELECT * FROM http_metrics LIMIT 10"])

# MCP: Execute in application container
coral_exec_command(service="api", command=["ps", "aux"])

# CLI: Interactive agent shell (for extended debugging)
coral shell --agent-id hostname-api
```

## Testing Strategy

### Unit Tests

**Agent Resolution (delegated to RFD 044):**

- Use RFD 044's agent resolution logic (agent_id or service)
- Verify disambiguation errors are properly formatted
- Test both direct agent_id lookup and service-based lookup
- Verify error messages include agent IDs for disambiguation

**Command Execution:**

- Test command with simple output (e.g., `echo "hello"`)
- Test command with stderr output
- Test command with non-zero exit code
- Test command timeout handling
- Test command with large output (>1MB)

**Agent Status Validation:**

- Accept healthy agents
- Accept degraded agents with warning
- Reject unhealthy agents with error message

**Response Formatting:**

- Verify stdout/stderr are clearly separated
- Include exit code in response
- Include execution duration
- Format agent details correctly
- Include security notes

**Error Handling:**

- Agent ID invalid or not found
- Service name empty or no matching agents
- Agent exists but is unhealthy
- Command timeout exceeded
- Command not found or permission denied
- Disambiguation errors from RFD 044 (multiple agents, list IDs)

### Integration Tests

**With Registry and Agent:**

- Register mock agents in registry
- Test agent_id lookup (direct)
- Test service name lookup (filtered by Services[])
- Mock agent gRPC ExecuteCommand() response
- Verify command output in tool response
- Test disambiguation scenarios (RFD 044)

**MCP Tool Execution:**

- Execute tool via MCP server test client
- Verify JSON input parsing (command array, timeout)
- Verify text output format (stdout, stderr, exit code)
- Test audit logging

### E2E Tests

**Full Workflow:**

1. Deploy test agent with known service name
2. Call `agent_shell_exec` via MCP with simple command
3. Verify command executed successfully
4. Verify output matches expected result
5. Test with various commands (ps, ls, duckdb query)

## Security Considerations

**Agent Discovery Information Exposure:**

**Risk:** Tool exposes agent mesh IP addresses and internal topology
information.

**Mitigations:**

- MCP server already requires authentication (RFD 004)
- Agent mesh IPs are only routable within WireGuard mesh
- Response includes security warnings about elevated privileges
- Audit logging captures all tool invocations

**Shell Access Privilege Warnings:**

The tool response must clearly communicate that agent shells have elevated
privileges:

- CRI socket access (can exec into any container)
- eBPF capabilities (can monitor network traffic)
- Host network access (can reach internal services)
- WireGuard mesh access (can reach all agents)

**Service Name Enumeration:**

**Risk:** Attackers could enumerate service names by trying different inputs.

**Mitigations:**

- Authentication required for all MCP tool access
- Rate limiting on MCP endpoints
- Audit logging of all discovery attempts
- Service names are not considered secrets (visible in traces/metrics)

**Command Execution Auditing:**

**Security requirement:** All command executions must be audited:

- Log command being executed
- Log agent ID and service name
- Log user/AI making the request
- Log exit code and execution duration
- Integration with future RBAC (RFD 043)

## Future Enhancements

**Note:** Agent selection and disambiguation is handled by RFD 044 (Agent ID
Standardization). This RFD focuses on agent command execution.

**Enhanced Command Execution:**

- Stream output for long-running commands (requires protocol changes)
- Support for interactive commands (requires bidirectional streaming)
- Command history and replay
- Command templates and snippets

**Security Enhancements:**

- Command whitelisting/blacklisting (RFD 043)
- RBAC permissions for specific commands (RFD 043)
- Approval workflows for sensitive commands (RFD 043)
- Command output redaction for sensitive data

---

## Implementation Status

**Core Capability:** ⏳ Not Started

This RFD defines the implementation of the `agent_shell_exec` MCP tool. The
underlying infrastructure is complete (agent gRPC API, RFD 004 MCP server,
colony registry), but the MCP tool integration is pending.

**Dependencies Completed:**

- ✅ Agent gRPC API with command execution
- ✅ MCP server framework (RFD 004)
- ✅ Colony agent registry
- ✅ Agent mesh networking

**What Needs Implementation:**

- ⏳ Replace placeholder in `executeAgentShellExecTool()`
- ⏳ Agent resolution logic (RFD 044 integration)
- ⏳ Command execution via agent gRPC API
- ⏳ Response formatting with stdout/stderr/exit code
- ⏳ Error handling for edge cases
- ⏳ Unit and integration tests

**Integration Status:**

Once implemented, the tool will be immediately available via:

- Claude Desktop MCP integration
- `coral ask` CLI (RFD 030)
- Any MCP-compatible client

No deployment or configuration changes required beyond code changes.

## Deferred Features

**Interactive Shell Access via MCP** (Not Feasible)

Direct interactive shell access via MCP is not feasible due to protocol
limitations:

- MCP uses request-response model (single request → single response)
- Interactive shells require bidirectional streaming (stdin, stdout, resize,
  signals)
- Terminal features (raw mode, PTY, ANSI escapes) require real-time I/O
- No standard way to maintain long-lived streaming connections in MCP

This is a protocol limitation, not a missing feature. For interactive sessions,
use the `coral shell` CLI command (RFD 026).

**Streaming Command Output** (Future)

Currently, command must complete before output is returned. Future enhancements
could support streaming output for long-running commands, but this would require
MCP protocol extensions for server-sent events or similar streaming mechanisms.

**Command History and Audit Viewing** (RFD 042, 043)

Future RFDs may add MCP tools for:

- Viewing command execution history
- Checking RBAC permissions before executing
- Requesting approval for sensitive commands
- Querying audit logs

These would be separate tools building on this foundation.

---

## Notes

**Relationship to Other RFDs:**

- **RFD 004**: MCP Server Integration - provides framework for tool registration
- **RFD 017**: Exec Command - similar tool for app containers (`coral_exec_command`)
- **RFD 026**: Shell Command Implementation - CLI for interactive agent shells
- **RFD 041**: MCP Agent Direct Queries - similar pattern of querying agents via
  MCP
- **RFD 042**: Shell Session Audit - future audit log integration
- **RFD 043**: Shell RBAC and Approval - future permission checking and command
  restrictions
- **RFD 044**: Agent ID Standardization and Routing - **dependency**, provides
  agent resolution logic (agent_id parameter, service filtering, disambiguation)

**Design Philosophy:**

This RFD follows the principle of **working within protocol constraints**.
Rather than trying to force bidirectional streaming into MCP's request-response
model, it provides one-off command execution that fits the protocol perfectly.
For interactive debugging sessions, users should use the `coral shell` CLI
command (RFD 026).

**AI Assistant Workflow:**

Typical interaction flow:

1. User asks Claude: "Query the agent's DuckDB for recent HTTP 500 errors"
2. Claude calls `agent_shell_exec` with appropriate DuckDB query command
3. Tool executes command on agent and captures output
4. Claude presents the query results directly to user
5. User can request follow-up queries or switch to CLI for interactive debugging

This workflow provides direct value: AI executes commands and returns results,
no context-switching required for simple queries.
