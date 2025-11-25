# Architecture

## 🏗️ Components Overview

| **Component**         | **Role**                                                                                       | **Key Technology**              |
|-----------------------|------------------------------------------------------------------------------------------------|---------------------------------|
| **Colony**            | Control plane coordinator managing agent registration, telemetry aggregation, and MCP gateway. | Go, DuckDB, MCP (server)        |
| **Agent**             | Local observer collecting eBPF-based observability data with embedded storage.                 | Go, Beyla (eBPF), OTLP, DuckDB  |
| **Coral CLI**         | Developer interface with all Coral commands including MCP proxy and AI assistant.              | Go, Cobra, MCP (client), Ollama |
| **External LLM Apps** | AI assistants (Claude Desktop, IDEs) querying Colony via MCP for observability insights.       | MCP protocol, various LLMs      |
| **Reef**              | Global aggregation service federating multiple colonies with enterprise LLM hosting.           | ClickHouse, LLMs                |

## Component Details

### Colony

**Role:** Control Plane & Coordinator

The Colony acts as the central control plane for the entire mesh, providing:

- **Agent Management:** Handles agent registration, health monitoring, and
  lifecycle management
- **Data Aggregation:** Polls agents for telemetry data (Beyla metrics, OTLP
  spans/metrics/logs) and creates summaries in local DuckDB
- **MCP Gateway:** Exposes MCP server for AI tool calling, enabling external
  LLM applications to query observability data
- **Security Enforcement:** Enforces RBAC and audit logging for all tool calls;
  issues Delegate JWTs for direct agent access
- **Command Routing:** Routes commands to agents for on-demand actions (eBPF
  probes, exec, shell sessions)
- **API Layer:** Exposes Buf Connect APIs for type-safe RPC communication

**Key Rationale:** Centralizes coordination, security enforcement, and data
aggregation while providing a standard MCP gateway for AI assistant integration.

**Technology Stack:**

- Go for core implementation
- DuckDB for embedded analytics database
- Buf Connect for type-safe gRPC APIs
- WireGuard for mesh networking

### Agent

**Role:** Local Observer & Data Collector

Agents run on each service or container to provide edge-based observability:

- **Zero-Config Observability:** Embeds Beyla for automatic eBPF-based
  monitoring of HTTP/gRPC/SQL traffic without code changes
- **Distributed Tracing:** Automatically captures distributed traces from
  application traffic
- **OTLP Collection:** Collects telemetry from instrumented applications using
  OpenTelemetry SDKs
- **Local Storage:** Stores recent data (~1hr rolling window) in embedded DuckDB
  for fast queries and reduced network overhead
- **Health Reporting:** Sends periodic health and telemetry summaries to Colony
- **Command Execution:** Executes commands from Colony (via Delegate JWTs) for
  on-demand eBPF probes, container exec, and shell sessions

**Key Rationale:** Provides zero-configuration observability at the edge with
minimal performance overhead. Local storage enables direct queries for detailed
data without overwhelming the central colony.

**Technology Stack:**

- Go for agent framework
- Beyla (eBPF) for automatic instrumentation
- DuckDB for embedded storage
- OTLP receivers for telemetry collection
- WireGuard for secure mesh communication

### Coral CLI

**Role:** Developer Interface

Single binary providing all Coral commands and operations:

- **Colony Lifecycle:** Manage colony initialization, startup, and shutdown (
  `coral init`, `coral colony start`)
- **Mesh Configuration:** Set up and configure WireGuard mesh networking
- **Service Connection:** Connect services to colonies via agents (
  `coral connect service:port[:health][:type]` - RFD 011)
- **MCP Integration:** Provide MCP proxy command for AI assistant integration (
  `coral colony mcp proxy`)
- **Developer Tools:** Built-in help, documentation, and troubleshooting
  commands
- **AI Assistant:** Interactive `coral ask` command for terminal-based AI
  conversations

**Key Rationale:** Unified command-line interface simplifies developer workflow
by consolidating all Coral operations into a single, well-documented tool.

**Technology Stack:**

- Go with Cobra for CLI framework
- Ollama for local models or API integration for cloud LLMs
- Integrated WireGuard configuration
- Buf Connect client for Colony communication

#### MCP Proxy Command

The `coral colony mcp proxy` command provides a protocol bridge for AI
assistants:

- **Protocol Translation:** Translates between MCP JSON-RPC (stdio) and Buf
  Connect gRPC
- **Stdio Interface:** Reads MCP requests from Claude Desktop/IDEs via stdin,
  writes responses to stdout
- **RPC Forwarding:** Forwards requests to Colony via Buf Connect RPCs (
  `CallTool`, `StreamTool`, `ListTools`)
- **Zero Business Logic:** Pure protocol translator with no database access or
  tool implementation
- **Multi-Client Support:** Compatible with any MCP client (Claude Desktop,
  custom LLM apps, AI agent frameworks)

**Rationale:** Enables external AI assistants to query Colony observability
data using the standard MCP protocol over stdio, while keeping the actual MCP
server implementation internal to the Colony.

**Usage:**

```bash
# Used by Claude Desktop or other MCP clients
coral colony mcp proxy

# Or for specific colony
coral colony mcp proxy --colony my-shop-production
```

#### AI Assistant (`coral ask`)

Interactive command-line AI assistant for terminal-based observability queries:

- **Terminal Integration:** Provides AI-powered observability without leaving
  the command line
- **LLM Flexibility:** Uses local LLM (Ollama) or user's API keys (OpenAI,
  Anthropic)
- **MCP Client:** Connects to Colony as MCP client
- **Unified Tools:** Queries Colony data through the same MCP tools as Claude
  Desktop
- **Investigation Workflows:** Enables rapid incident investigation and system
  health checks via conversational AI
- **Local Privacy:** Can run entirely locally with Ollama for sensitive
  environments

**Rationale:** Provides AI-powered observability in the terminal using local
compute for fast iteration, privacy, and developer productivity.

### External LLM Apps

**Role:** AI Assistants & Tool-calling Clients

Applications that connect to Colony via the MCP proxy:

- **Claude Desktop:** Anthropic's AI assistant with native MCP support
- **IDE Integration:** Cursor, VS Code, and other editors with MCP plugins
- **Custom MCP Clients:** Custom-built applications using MCP client libraries
- **Tool Calling:** Query observability data through MCP tools (service health,
  metrics, traces, events)
- **Local Execution:** Run on user's machine with their chosen LLM provider (
  Anthropic, OpenAI, local Ollama)
- **Natural Language Synthesis:** Convert raw observability data into natural
  language insights and recommendations

**Key Rationale:** Brings AI-powered observability queries to wherever
developers are already working (IDE, desktop), without requiring embedded LLMs
in the infrastructure.

**Technology Stack:**

- Various (Claude Desktop, Cursor, custom apps)
- MCP protocol for communication
- User-provided LLM APIs (Anthropic, OpenAI) or local models (Ollama)

### Reef

**Role:** Global Aggregation & Enterprise LLM Host

Enterprise-grade global observability platform:

- **Multi-Colony Federation:** Aggregates data from multiple colonies across
  regions and environments
- **Long-term Storage:** Central data warehouse (ClickHouse) for historical
  analytics
- **Centralized LLM:** Hosts single enterprise-grade LLM for consistent,
  auditable AI-powered RCA and insights
- **Global Dashboard:** Provides unified view across all colonies and
  environments
- **Cross-Environment Correlation:** Enables comparison between prod, staging,
  and dev environments
- **Deployment Timeline:** Tracks and correlates deployments with incidents
  across the organization
- **Cost Optimization:** Centralized LLM reduces costs compared to per-developer
  LLM subscriptions

**Key Rationale:** Ensures consistency across the organization, controls costs
with centralized LLM infrastructure, and enables global observability insights
that span multiple colonies.

**Technology Stack:**

- ClickHouse or TimescaleDB for time-series storage
- Centralized LLM deployment (self-hosted or managed)
- Multi-colony data aggregation pipeline
- Global visualization dashboard

* * *

## 🔑 Key Features and Data Flows

### 1. Colony as the MCP Gateway

The Colony acts as the **Control Plane** for the mesh. It exposes a standard set
of
tool calls (like `issue_dynamic_probe`, `query_trace_data`) that are consumed by
external LLM
agents. Every request must pass the Colony's **audit and RBAC checks**, making
it the central
security enforcement point.

### 2. Developer Empowerment with CLI AI Assistant

Developers can use their local machine's compute power for AI-powered
observability:

- **Local LLM Integration:** The `coral ask` CLI command enables terminal-based
  AI conversations, hosting an LLM (e.g., Llama 3) on the developer's
  workstation or connecting to cloud LLM APIs.

- **Secure Connection:** The CLI connects to the Colony via the **secure
  WireGuard mesh** and communicates using the Colony's MCP API.

- **Direct Stream:** When the LLM decides to initiate a **live probe** for RCA,
  the Colony issues a **short-lived Delegate JWT**, allowing the developer's
  local CLI to establish a direct, low-latency data stream with the target
  **Agent** (bypassing the Colony for data flow, but not for authorization).

### 3. Reef's Centralized Intelligence

The Reef is the **Global Investigation Hub** and **Enterprise LLM Service**.

- **Consistency:** By hosting a single, specialized LLM, the Reef ensures that
  all global
  investigations and automated RCA reports are generated using the same model
  and proprietary prompt
  engineering, which is crucial for enterprise-wide consistency and
  auditability.

- **High-Level Analysis:** The Reef is responsible for aggregating data from all
  Colonies for
  cross-regional correlation and providing the centralized dashboard view.

* * *

## 🔌 MCP Server Architecture

### Overview

Colony exposes its observability capabilities through the **Model Context
Protocol (MCP)**, enabling AI assistants like Claude Desktop to query
distributed system health, metrics, traces, and telemetry. The implementation
uses a **proxy architecture** where the local `coral mcp proxy` command acts as
the public-facing MCP server, translating between MCP JSON-RPC protocol and Buf
Connect gRPC.

### Architecture

```
┌────────────────────────────────────────┐
│  Claude Desktop / IDE / MCP Client     │
│  (External LLM - Anthropic, OpenAI)    │
└─────────────┬──────────────────────────┘
              │ MCP JSON-RPC (stdio)
              ▼
     ┌────────────────────┐
     │  coral mcp proxy   │  ← Public-facing MCP server
     │  (Protocol Bridge) │     • No business logic
     │  MCP ↔ RPC         │     • No database access
     └────────┬───────────┘     • Pure translator
              │ Buf Connect gRPC
              │ (CallTool, StreamTool, ListTools RPCs)
              ▼
     ┌────────────────────┐
     │  Colony Server     │
     │  • MCP Server      │
     │  • Tool registry   │
     │  • Tool execution  │
     │  • Business logic  │
     │  • RBAC/Audit      │
     └────────┬───────────┘
              │
              ▼
     ┌────────────────────┐
     │  DuckDB + Registry │
     │  • Metrics         │
     │  • Traces          │
     │  • Agent health    │
     └────────────────────┘
```

### Components

#### 1. Colony MCP Server (Internal)

**Location:** `internal/colony/mcp/server.go`

- Registers all MCP tools: health, topology, Beyla metrics, traces, OTLP
  telemetry
- Integrated into colony startup (runs automatically with colony)
- Executes tools by querying DuckDB and agent registry
- **NOT directly exposed to external clients**

**Tools Implemented:**

- `coral_get_service_health` - Service health monitoring
- `coral_get_service_topology` - Service dependency graph
- `coral_query_beyla_http_metrics` - HTTP RED metrics
- `coral_query_beyla_grpc_metrics` - gRPC metrics
- `coral_query_beyla_sql_metrics` - SQL query metrics
- `coral_query_beyla_traces` - Distributed tracing
- `coral_query_telemetry_spans` - OTLP spans
- `coral_query_events` - Operational events

#### 2. Buf Connect RPC Interface

**Location:** `proto/coral/colony/v1/mcp.proto`,
`internal/colony/server/mcp_tools.go`

Colony exposes three RPCs for MCP communication:

```protobuf
service ColonyService {
    rpc CallTool(CallToolRequest) returns (CallToolResponse);
    rpc StreamTool(stream StreamToolRequest) returns (stream StreamToolResponse);
    rpc ListTools(ListToolsRequest) returns (ListToolsResponse);
}
```

**Benefits:**

- Type-safe communication with Protocol Buffers
- Real-time data (no stale snapshots)
- Clean separation: colony handles all business logic
- Scalable: multiple proxies can connect to same colony

#### 3. MCP Proxy (Public-facing)

**Location:** `internal/cli/colony/mcp.go`

The `coral colony mcp proxy` command is the **only public-facing MCP server**:

**Responsibilities:**

- Reads MCP JSON-RPC requests from stdin
- Translates MCP protocol to Buf Connect RPCs
- Calls colony via gRPC
- Translates RPC responses back to MCP JSON-RPC format
- Writes MCP responses to stdout

**What it does NOT do:**

- ❌ No database access
- ❌ No business logic
- ❌ No tool implementation
- ✅ Pure protocol translation

**Usage:**

```bash
# Used by Claude Desktop or other MCP clients
coral colony mcp proxy

# Or for specific colony
coral colony mcp proxy --colony my-shop-production
```

### Data Flow Example

1. **User asks Claude:** "Is production healthy?"

2. **Claude Desktop → Proxy (stdio):**
   ```json
   {
     "jsonrpc": "2.0",
     "id": 1,
     "method": "tools/call",
     "params": {
       "name": "coral_get_service_health",
       "arguments": {}
     }
   }
   ```

3. **Proxy → Colony (gRPC):**
   ```protobuf
   CallToolRequest {
     tool_name: "coral_get_service_health"
     arguments_json: "{}"
   }
   ```

4. **Colony executes tool:**
    - Queries agent registry for service health
    - Aggregates CPU, memory, uptime data
    - Formats results

5. **Colony → Proxy (gRPC):**
   ```protobuf
   CallToolResponse {
     result: "System Health Report: ..."
     success: true
   }
   ```

6. **Proxy → Claude Desktop (stdio):**
   ```json
   {
     "jsonrpc": "2.0",
     "id": 1,
     "result": {
       "content": [
         {"type": "text", "text": "System Health Report: ..."}
       ]
     }
   }
   ```

7. **Claude synthesizes answer** and presents to user

### Configuration

**Colony config** (`colony.yaml`):

```yaml
mcp:
    disabled: false  # MCP enabled by default
    enabled_tools: [ ]  # Empty = all tools enabled
    security:
        require_rbac_for_actions: true
        audit_enabled: true
```

**Claude Desktop config** (`~/.config/claude/claude_desktop_config.json`):

```json
{
    "mcpServers": {
        "coral": {
            "command": "coral",
            "args": [
                "colony",
                "mcp",
                "proxy"
            ]
        }
    }
}
```

### Architecture Benefits

1. **Clean Separation:** Proxy has zero business logic, purely translates
   protocols
2. **No Database Conflicts:** Proxy doesn't access database (previous limitation
   removed)
3. **Real-time Data:** Direct RPC to colony ensures fresh data
4. **Type Safety:** Protocol Buffers ensure correct types
5. **Scalable:** Multiple proxies can connect to same colony
6. **Security:** Colony enforces RBAC and audit for all tool calls
7. **Standard Protocol:** MCP is vendor-neutral, works with any MCP client

### Future Enhancements (Optional)

**HTTP Streamable Transport:** For web-based clients or remote access, HTTP
transport could be added alongside the current RPC implementation. Not required
for current use cases (Claude Desktop, coral ask). See RFD 004 "Deferred
Features."

### Related Documentation

- **RFD 004:** Complete MCP server integration specification
- **docs/MCP.md:** User guide with examples and troubleshooting
- **docs/development/MCP.md:** Manual protocol validation and testing
- **docs/development/MCP-Testing.md:** Automated testing guide (unit,
  integration, E2E)
