---
rfd: "045"
title: "MCP Shell Exec Tool - One-Off Command Execution"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "004", "026", "044" ]
related_rfds: [ "041", "042", "043" ]
database_migrations: [ ]
areas: [ "mcp", "shell", "debugging" ]
---

# RFD 045 - MCP Shell Exec Tool - One-Off Command Execution

**Status:** 🎉 Implemented

## Summary

Implement the `coral_shell_exec` MCP tool to enable AI assistants to execute
one-off commands in the agent's host environment. Unlike interactive shell
sessions (which require bidirectional streaming), one-off execution fits
perfectly with MCP's request-response model: send command, receive output and
exit code. This enables AI-powered debugging workflows where the assistant can
run diagnostic commands and analyze results without requiring user context
switching.

## Problem

**Current behavior/limitations:**

While `coral shell` (RFD 026) provides interactive debugging access to agent
hosts, there's no way for AI assistants to execute diagnostic commands
programmatically. This creates several problems:

1. **Context switching required**: AI assistants can identify issues but cannot
   run diagnostic commands - users must manually switch to terminal and execute
   commands
2. **Broken AI debugging workflow**: AI says "run `ps aux | grep nginx`" but
   cannot execute it itself, interrupting the flow
3. **Incomplete MCP debugging toolset**: Other observability tools (metrics,
   traces) are available via MCP, but host-level diagnostics require manual CLI
   usage
4. **Limited automation**: AI-powered runbooks cannot include automated
   diagnostic steps that run on agent hosts

**Why this matters:**

- **Seamless AI debugging**: AI assistants should execute diagnostic commands
  and analyze output without user intervention
- **Host-level diagnostics**: Agent hosts have network tools (tcpdump, netcat),
  process inspection (ps, top), and direct access to agent data that aren't
  available through other MCP tools
- **Complete observability**: Complement existing metrics/traces with on-demand
  host diagnostics
- **Workflow continuity**: Keep users in the AI conversation while running
  commands and analyzing results

**Use cases affected:**

- **AI-driven debugging**: "Check if nginx is running" → AI executes `ps aux |
  grep nginx` and analyzes output
- **Network diagnostics**: "Is port 8080 listening?" → AI runs `ss -tlnp | grep
  8080`
- **Process inspection**: "What's consuming CPU?" → AI executes `top -bn1` and
  identifies culprit

**Interactive vs One-Off Execution:**

MCP tools use a **request-response model** (send request, receive response).
This matches perfectly with **one-off command execution**:

- ✅ **One-off execution**: Send command → Execute → Return
  stdout/stderr/exit_code
- ❌ **Interactive shell**: Requires bidirectional streaming, PTY, signals,
  resize events (not feasible with MCP)

This RFD focuses on one-off execution for MCP integration.

## Solution

Implement `coral_shell_exec` as a **one-off command execution tool** that
executes commands in the agent's host environment and returns the output. This
requires two components:

1. **Extend `coral shell` CLI** to support command arguments (like `kubectl
   exec`)
2. **Create `coral_shell_exec` MCP tool** that leverages this CLI functionality

**Key Design Decisions:**

- **One-off execution mode**: Execute command and return results (no interactive
  session)
- **Reuse shell infrastructure**: Extend existing `coral shell` command and
  agent handler
- **Command array format**: Commands as string array (like `kubectl exec`) to
  avoid shell injection
- **Registry-based routing**: Use RFD 044 agent resolution (agent_id or service
  name)
- **Timeout protection**: Commands have configurable timeout (default: 30s, max:
  5min)
- **Output capture**: Return stdout, stderr, and exit code separately

**Why this approach:**

- **Protocol-aligned**: Request-response model fits one-off execution perfectly
- **Leverages existing code**: Reuses shell RPC infrastructure from RFD 026
- **Security-conscious**: Command arrays prevent shell injection, timeouts
  prevent runaway commands
- **Kubernetes-familiar**: Same pattern as `kubectl exec pod -- command args`
- **AI-friendly**: Structured output (stdout/stderr/exit_code) is easy for AI to
  parse

**Benefits:**

- AI assistants can execute diagnostic commands without user intervention
- Host-level debugging complements existing observability tools
- Workflow stays in AI conversation (no context switching)
- Enables AI-powered runbooks with automated diagnostic steps

**Architecture Overview:**

```
┌──────────────────────────────────────────────────────┐
│  Claude Desktop / coral ask (MCP Client)             │
└────────────────────┬─────────────────────────────────┘
                     │
                     │ MCP: coral_shell_exec(service: "nginx", command: ["ps", "aux"])
                     ▼
┌──────────────────────────────────────────────────────┐
│  Colony MCP Server                                   │
│  ┌────────────────────────────────────────────────┐  │
│  │ executeShellExecTool()                         │  │
│  │  1. Resolve agent via registry (RFD 044)       │  │
│  │  2. Create gRPC client to agent                │  │
│  │  3. Call ShellExec RPC with command array      │  │
│  │  4. Wait for completion (with timeout)         │  │
│  │  5. Return stdout/stderr/exit_code             │  │
│  └────────────────────────────────────────────────┘  │
└────────────────────┬─────────────────────────────────┘
                     │
                     │ gRPC: ShellExec(command: ["ps", "aux"])
                     ▼
┌──────────────────────────────────────────────────────┐
│  Agent ShellHandler                                  │
│  ┌────────────────────────────────────────────────┐  │
│  │ ShellExec()                                    │  │
│  │  1. Validate command (whitelist check)        │  │
│  │  2. Execute with exec.Command() - no PTY      │  │
│  │  3. Capture stdout/stderr to buffers          │  │
│  │  4. Apply timeout (30s default, 5min max)     │  │
│  │  5. Return output + exit code                 │  │
│  └────────────────────────────────────────────────┘  │
└────────────────────┬─────────────────────────────────┘
                     │
                     │ Response: {stdout: "...", stderr: "...", exit_code: 0}
                     ▼
┌──────────────────────────────────────────────────────┐
│  AI analyzes output and presents to user             │
└──────────────────────────────────────────────────────┘
```

**Interaction flow:**

```
User: "Check if nginx is running on the api-server agent"
  ↓
AI calls: coral_shell_exec(service: "api-server", command: ["ps", "aux"])
  ↓
MCP Tool resolves agent: api-server → agent-api-1 (10.100.0.5)
  ↓
gRPC to agent: ShellExec(["ps", "aux"])
  ↓
Agent executes: ps aux (with 30s timeout)
  ↓
Returns: stdout="PID  CMD\n1    nginx...", stderr="", exit_code=0
  ↓
AI analyzes: "Yes, nginx is running with PID 1234"
```

### Component Changes

**1. Protocol Definition** (`coral/agent/v1/agent.proto`):

Add new RPC method and messages:

```protobuf
message ShellExecRequest {
  repeated string command = 1;       // Command as array (no shell interpretation)
  string user_id = 2;                // For audit
  uint32 timeout_seconds = 3;        // Default: 30, max: 300
}

message ShellExecResponse {
  bytes stdout = 1;
  bytes stderr = 2;
  int32 exit_code = 3;
  string session_id = 4;             // For audit logging
  uint32 duration_ms = 5;            // Execution time
}

service AgentService {
  // Existing Shell RPC (interactive)
  rpc Shell(stream ShellRequest) returns (stream ShellResponse);

  // New: One-off command execution
  rpc ShellExec(ShellExecRequest) returns (ShellExecResponse);
}
```

**2. Agent Handler** (`internal/agent/shell_handler.go`):

Add `ShellExec()` method:

- Parse and validate command array
- Create `exec.Command()` (no PTY needed)
- Set timeout context (default 30s, max 300s)
- Capture stdout/stderr to separate buffers
- Execute and wait for completion
- Log execution for audit
- Return structured response

**3. MCP Tool** (`internal/colony/mcp/tools_exec.go`):

Implement new `executeShellExecTool()`:

- Parse `ShellExecInput` (service/agent_id, command array, timeout)
- Resolve agent via RFD 044 (reuse existing `resolveAgent()`)
- Create gRPC client to agent
- Call `ShellExec()` RPC
- Format output for AI consumption
- Handle errors (timeout, non-zero exit, agent unreachable)

**2. Response Format**:

The tool returns formatted text with:

- Agent identification (ID, service name, mesh IP)
- Agent status and health information
- CLI command to execute: `coral shell --agent-addr <ip>:9001`
- Optional shell preference (`--shell /bin/sh` if specified)
- Security warnings about elevated privileges
- Available utilities in agent environment

**Configuration Example:**

MCP client usage:

```json
{
    "tool": "coral_shell_start",
    "arguments": {
        "service": "api-server",
        "shell": "/bin/bash"
    }
}
```

Response:

```
Shell Access Available: api-server

Agent Details:
  - Agent ID: agent-abc123
  - Service: api-server
  - Mesh IP: 10.100.0.5
  - Status: healthy (last seen 3 seconds ago)
  - Uptime: 2h 34m

Connection Command:
  coral shell --agent-addr 10.100.0.5:9001

⚠️  Warning: Agent shells have elevated privileges:
  • CRI socket access (can exec into containers)
  • eBPF monitoring data access
  • WireGuard mesh network access
  • All sessions are fully recorded for audit

Available Utilities:
  tcpdump, netcat, curl, duckdb, dig/nslookup, ps, ip, ss, vim

Shell: /bin/bash (default)
```

## Implementation Plan

### Phase 1: Core Tool Implementation

- [ ] Implement `executeShellStartTool()` in `tools_exec.go`
- [ ] Integrate RFD 044's agent resolution logic (agent_id or service)
- [ ] Add agent status validation
- [ ] Format shell-specific response with connection details and CLI command
- [ ] Handle error cases (delegation to RFD 044 for routing, local for
  shell-specific)

### Phase 2: Response Formatting

- [ ] Create helper function for agent connection info formatting
- [ ] Include security warnings in response
- [ ] Add agent status and uptime details
- [ ] List available utilities in agent environment

### Phase 3: Testing

- [ ] Unit tests for service lookup and filtering
- [ ] Unit tests for agent status validation
- [ ] Unit tests for error cases (not found, unhealthy)
- [ ] Integration test with registry
- [ ] MCP tool execution test

### Phase 4: Documentation

- [ ] Update tool description in `registerShellStartTool()`
- [ ] Add usage examples in tool documentation
- [ ] Update MCP server documentation with shell tool usage

## API Changes

No new API endpoints required. Changes are limited to MCP tool implementation.

### MCP Tool Interface

**Tool Name:** `coral_shell_start`

**Description:** "Start an interactive debug shell in the agent's environment.
Returns connection information and CLI command to access the agent shell for the
specified service."

**Input Schema** (updated per RFD 044):

```go
type ShellStartInput struct {
    Service string  `json:"service" jsonschema:"description=Service whose agent to connect to (use agent_id for disambiguation)"`
    AgentID *string `json:"agent_id,omitempty" jsonschema:"description=Target agent ID (overrides service lookup, from RFD 044)"`
    Shell   *string `json:"shell,omitempty" jsonschema:"description=Shell to use,enum=/bin/bash,enum=/bin/sh,default=/bin/bash"`
}
```

**Output:** Text string with formatted connection information and CLI command.

### CLI Command Reference

The tool returns instructions to use the existing CLI command:

```bash
# Connect to agent for service "myapp"
coral shell --agent-addr 10.100.0.5:9001

# With specific shell
coral shell --agent-addr 10.100.0.5:9001 --shell /bin/sh

# With user ID for audit
coral shell --agent-addr 10.100.0.5:9001 --user-id developer@example.com
```

## Testing Strategy

### Unit Tests

**Agent Resolution (delegated to RFD 044):**

- Use RFD 044's agent resolution logic (agent_id or service)
- Verify disambiguation errors are properly formatted
- Test both direct agent_id lookup and service-based lookup
- Verify error messages include agent IDs for disambiguation

**Agent Status Validation:**

- Accept healthy agents
- Accept degraded agents with warning
- Reject unhealthy agents with error message
- Include "last seen" timestamp in response

**Response Formatting:**

- Verify CLI command format with mesh IP and port
- Include shell preference in command if specified
- Include all required security warnings
- Format agent details correctly

**Error Handling:**

- Agent ID invalid or not found
- Service name empty or no matching agents
- Agent exists but is unhealthy (warn user)
- Disambiguation errors from RFD 044 (multiple agents, list IDs)

### Integration Tests

**With Registry:**

- Register mock agents in registry
- Test agent_id lookup (direct)
- Test service name lookup (filtered by Services[])
- Verify agent details in response
- Test disambiguation scenarios (RFD 044)

**MCP Tool Execution:**

- Execute tool via MCP server test client
- Verify JSON input parsing
- Verify text output format
- Test audit logging

### E2E Tests

**Full Workflow:**

1. Deploy test agent with known service name
2. Call `coral_shell_exec` via MCP
3. Parse returned CLI command
4. Execute CLI command to verify shell access
5. Validate shell session works as expected

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

**No Direct Shell Access:**

**Security benefit:** Tool only provides connection information, not direct
shell access. Users must use the CLI with their own authentication, maintaining
the security model defined in RFD 026 (and future RBAC in RFD 043).

## Future Enhancements

**Note:** Agent selection and disambiguation is handled by RFD 044 (Agent ID
Standardization). This RFD focuses on shell-specific enhancements.

**Enhanced Discovery:**

- List all available agents (no service filter)
- Filter by agent capabilities (shell support, eBPF, CRI)
- Show agent metadata (version, uptime, resource usage)

**Session Management:**

While this tool doesn't create shell sessions, future enhancements could:

- Return links to session audit logs (RFD 042)
- Check if user has RBAC permissions for shell access (RFD 043)
- Integrate with approval workflows for production access (RFD 043)

---

## Implementation Status

**Core Capability:** ✅ Complete

The `coral_shell_start` MCP tool is fully implemented and operational. The tool
provides agent discovery and connection information for shell access through the
CLI.

**Implemented Components:**

- ✅ Agent resolution via agent_id or service name (RFD 044 integration)
- ✅ Agent status validation (healthy/degraded/unhealthy)
- ✅ Response formatting with connection details and CLI command
- ✅ Error handling for edge cases (not found, disambiguation)
- ✅ Comprehensive unit tests (TestExecuteShellStartTool)
- ✅ Integration with existing MCP server framework

**What Works Now:**

- AI assistants can query agent information by service name or agent ID
- Tool returns formatted connection details including:
    - Agent identification (ID, services, mesh IP)
    - Agent health status with warnings for degraded/unhealthy agents
    - CLI command to execute: `coral shell --agent-addr <ip>:9001`
    - Security warnings about elevated privileges
    - Available utilities in agent environment
- Disambiguation handling when multiple agents match a service
- Custom shell preference support (`/bin/bash` or `/bin/sh`)

**Integration Status:**

The tool is immediately available via:

- ✅ Claude Desktop MCP integration
- ✅ `coral ask` CLI (RFD 030)
- ✅ Any MCP-compatible client

**Files Modified:**

- `internal/colony/mcp/tools_exec.go`: Implemented `executeShellStartTool()` and
  `formatShellStartResponse()`
- `internal/colony/mcp/tools_debugging.go`: Updated `registerShellStartTool()`
  to call execute method
- `internal/colony/mcp/tools_exec_test.go`: Added comprehensive test suite

No deployment or configuration changes required.

## Deferred Features

**Session Management via MCP** (RFD 042, 043)

Future RFDs may add MCP tools for:

- Listing active shell sessions
- Viewing session audit logs
- Checking RBAC permissions before connecting
- Requesting approval for production access

These would be separate tools building on this foundation.

---

## Notes

**Relationship to Other RFDs:**

- **RFD 004**: MCP Server Integration - provides framework for tool registration
- **RFD 026**: Shell Command Implementation - the CLI command this tool provides
  instructions for
- **RFD 041**: MCP Agent Direct Queries - similar pattern of querying agents via
  MCP
- **RFD 042**: Shell Session Audit - future session management integration
- **RFD 043**: Shell RBAC and Approval - future permission checking integration
- **RFD 044**: Agent ID Standardization and Routing - **dependency**, provides
  agent resolution logic (agent_id parameter, service filtering, disambiguation)

**Design Philosophy:**

This RFD follows the principle of **working within protocol constraints**.
Rather than trying to force bidirectional streaming into MCP's request-response
model, it embraces the tool's role as a discovery helper that bridges the AI
assistant interface (MCP) with the CLI-based shell implementation (RFD 026).

**AI Assistant Workflow:**

Typical interaction flow:

1. User asks Claude: "How do I debug network issues from the api-server agent?"
2. Claude calls `coral_shell_start` with `service: "api-server"`
3. Tool queries registry and returns connection details
4. Claude presents the `coral shell --agent-addr X:9001` command to user
5. User runs command in their terminal for interactive shell access

This workflow provides the best of both worlds: AI-assisted discovery with full
CLI interactivity.
