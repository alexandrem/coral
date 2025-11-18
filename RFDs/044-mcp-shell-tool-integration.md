---
rfd: "044"
title: "MCP Shell Tool Integration"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: [ "004", "026" ]
related_rfds: [ "041", "042", "043" ]
database_migrations: [ ]
areas: [ "mcp", "shell", "debugging" ]
---

# RFD 044 - MCP Shell Tool Integration

**Status:** 🚧 Draft

## Summary

Implement the `coral_shell_start` MCP tool to enable AI assistants (Claude
Desktop, `coral ask`) to discover agent shell access information and provide
users with connection instructions. The tool acts as a discovery helper rather
than providing direct interactive shell access, since MCP's request-response
model cannot support bidirectional streaming required for interactive terminals.

## Problem

**Current behavior/limitations:**

The `coral shell` command (RFD 026) is fully implemented for CLI access, but the
corresponding MCP tool `coral_shell_start` is a placeholder returning static
text. This creates a gap in the MCP server's debugging capabilities:

1. **No agent discovery via MCP**: AI assistants cannot help users find which
   agent to connect to for a given service
2. **Missing connection information**: Users cannot get mesh IP addresses and
   connection details through MCP
3. **Incomplete MCP debugging toolset**: Other debugging tools (eBPF, exec) have
   MCP placeholders, but shell is commonly needed for infrastructure debugging
4. **User friction**: Users must manually discover agent addresses before using
   `coral shell`, when the colony registry already has this information

**Why this matters:**

- **AI-assisted debugging**: Developers using Claude Desktop should be able to
  ask "how do I debug the network from myapp's agent?" and get actionable
  instructions
- **Service discovery**: Colony registry maps services to agents, but this
  mapping isn't exposed via MCP
- **Complete MCP interface**: RFD 004 defines MCP as the primary integration
  point - shell access should be discoverable through it
- **Workflow continuity**: Users working in AI assistants shouldn't need to
  context-switch to CLI just to discover connection details

**Use cases affected:**

- Developer in Claude Desktop: "I need to debug network connectivity for the
  api-server service"
- SRE investigating incident: "Show me how to access the agent monitoring the
  payment service"
- AI-powered runbooks: "Get shell access information for all agents running
  service X"
- `coral ask` CLI: Needs to provide shell connection instructions via MCP tools

**MCP Protocol Limitation:**

MCP tools use a **request-response model** (tool receives JSON input, returns
text output). The `coral shell` command requires **bidirectional streaming** for
interactive terminal I/O (stdin/stdout, resize events, signals). This
fundamental protocol mismatch means MCP tools cannot provide direct shell
access - they can only provide discovery and connection instructions.

## Solution

Implement `coral_shell_start` as an **agent discovery and connection helper tool
**. The tool queries the colony registry to find agents by service name and
returns connection information for the CLI-based `coral shell` command.

**Key Design Decisions:**

- **Discovery-only approach**: Tool provides information to connect via CLI, not
  direct shell access
- **Registry-based lookup**: Query colony registry to map service names to agent
  mesh addresses
- **Connection instructions**: Return formatted CLI commands users can run
- **Status validation**: Check agent health before returning connection info
- **Security warnings**: Include privilege warnings in the response

**Why this approach:**

- **Protocol-aligned**: Works within MCP's request-response model
- **Leverages existing infrastructure**: Uses implemented shell (RFD 026) and
  registry
- **AI-friendly output**: Text response format is ideal for AI assistants to
  present to users
- **No new gRPC APIs**: No protocol changes needed, just registry queries
- **Security-aware**: Can include context-appropriate warnings in responses

**Benefits:**

- AI assistants can help users discover and access agent shells
- Colony registry becomes queryable via MCP for agent discovery
- Completes the debugging tool suite in MCP (alongside future eBPF, exec tools)
- No additional infrastructure needed (uses existing components)

**Architecture Overview:**

```
┌──────────────────────────────────────────────────────┐
│  Claude Desktop / coral ask (MCP Client)             │
└────────────────────┬─────────────────────────────────┘
                     │
                     │ MCP: coral_shell_start(service: "api-server")
                     ▼
┌──────────────────────────────────────────────────────┐
│  Colony MCP Server                                   │
│  ┌────────────────────────────────────────────────┐  │
│  │ executeShellStartTool()                        │  │
│  │  1. Query registry.ListAll()                   │  │
│  │  2. Filter by service name                     │  │
│  │  3. Check agent status (healthy/degraded)      │  │
│  │  4. Format CLI command + agent details         │  │
│  └────────────────────────────────────────────────┘  │
└────────────────────┬─────────────────────────────────┘
                     │
                     │ Response: "Agent found: 10.100.0.5\nRun: coral shell --agent-addr 10.100.0.5:9001"
                     ▼
┌──────────────────────────────────────────────────────┐
│  Claude Desktop presents to user                     │
└──────────────────────────────────────────────────────┘
```

**Interaction with existing components:**

```
coral_shell_start (MCP Tool)
  ↓
Registry (find agent by service name)
  ↓
Return connection details
  ↓
User runs: coral shell --agent-addr <mesh-ip>:9001
  ↓
Shell Handler (RFD 026 - already implemented)
```

### Component Changes

**1. MCP Tool Execution** (`internal/colony/mcp/tools_exec.go`):

Replace placeholder in `executeShellStartTool()` with:

- Parse `ShellStartInput` (service name, optional shell preference)
- Query `s.registry.ListAll()` to get all agents
- Filter agents by `ComponentName` matching `input.Service`
- Validate agent status using `registry.DetermineStatus()`
- Format response with connection details
- Handle error cases (service not found, agent unhealthy, multiple matches)

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
- [ ] Add registry query and service name filtering
- [ ] Add agent status validation
- [ ] Format response with connection details and CLI command
- [ ] Handle error cases (not found, unhealthy, multiple matches)

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

**Input Schema** (already defined in `types.go`):

```go
type ShellStartInput struct {
    Service string  `json:"service" jsonschema:"description=Service whose agent to connect to"`
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

**Service Discovery:**

- Query registry and find agent by service name
- Handle multiple agents with same service (return newest/healthy)
- Handle service not found error
- Handle wildcard/pattern matching in service names

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

- Service name empty or invalid
- No agents found for service
- Agent exists but is unhealthy
- Multiple agents found (disambiguation)

### Integration Tests

**With Registry:**

- Register mock agents in registry
- Query for agents by service name
- Verify agent details in response
- Test with multiple agents and status filters

**MCP Tool Execution:**

- Execute tool via MCP server test client
- Verify JSON input parsing
- Verify text output format
- Test audit logging

### E2E Tests

**Full Workflow:**

1. Deploy test agent with known service name
2. Call `coral_shell_start` via MCP
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

**Agent Selection Strategies:**

If multiple agents match a service name:

- Return the most recently registered agent
- Return all matching agents with disambiguation
- Support regex/glob patterns in service names
- Allow filtering by agent health status

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

**Core Capability:** ⏳ Not Started

This RFD defines the implementation of the `coral_shell_start` MCP tool. The
underlying infrastructure is complete (RFD 026 shell implementation, RFD 004 MCP
server, colony registry), but the MCP tool integration is pending.

**Dependencies Completed:**

- ✅ Shell command and handler (RFD 026)
- ✅ MCP server framework (RFD 004)
- ✅ Colony agent registry
- ✅ Agent mesh networking

**What Needs Implementation:**

- ⏳ Replace placeholder in `executeShellStartTool()`
- ⏳ Registry query and service filtering logic
- ⏳ Response formatting with connection details
- ⏳ Error handling for edge cases
- ⏳ Unit and integration tests

**Integration Status:**

Once implemented, the tool will be immediately available via:

- Claude Desktop MCP integration
- `coral ask` CLI (RFD 030)
- Any MCP-compatible client

No deployment or configuration changes required beyond code changes.

## Deferred Features

**Direct Shell Access via MCP** (Not Feasible)

Direct interactive shell access via MCP is not feasible due to protocol
limitations:

- MCP uses request-response model (single request → single response)
- Interactive shells require bidirectional streaming (stdin, stdout, resize,
  signals)
- Terminal features (raw mode, PTY, ANSI escapes) require real-time I/O
- No standard way to maintain long-lived streaming connections in MCP

This is a protocol limitation, not a missing feature. The CLI-based approach
defined in this RFD is the correct design.

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
