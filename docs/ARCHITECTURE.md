# Architecture

## 🏗️ Components

| **Component**         | **Role**                                                                                                                                                                                                                                                                                                                                                                                                                                  | **Key Rationale**                                                                                                             |
|-----------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------|
| **Colony**            | **Control Plane & Coordinator:** Manages agent registration and health monitoring. Polls agents for telemetry data (Beyla metrics, OTLP spans/metrics/logs). Creates summaries and aggregates in local DuckDB. Exposes MCP server (via Genkit) for AI tool calling. Enforces RBAC/Audit for all tool calls. Issues Delegate JWTs for direct agent access. Routes commands to agents. Exposes Buf Connect APIs for RPC communication.      | Centralizes coordination, security enforcement, data aggregation, and provides MCP gateway for AI assistant integration.      |
| **Agent**             | **Local Observer & Data Collector:** Runs on each service/container. Embeds Beyla for automatic eBPF-based observability (HTTP/gRPC/SQL metrics, distributed traces). Collects OTLP telemetry from instrumented applications. Stores recent data locally (~1hr rolling window) in embedded DuckDB. Reports health and telemetry summaries to Colony. Executes commands from Colony (eBPF probes, exec, shell sessions via Delegate JWTs). | Provides zero-config observability at the edge with minimal overhead. Local storage enables direct queries for detailed data. |
| **Coral CLI**         | **Developer Interface:** Single binary providing all Coral commands (`coral init`, `coral colony start`, `coral connect`, `coral ask`, `coral colony mcp`, etc.). Manages colony lifecycle. Configures WireGuard mesh. Connects agents to colonies. Provides MCP proxy command for AI assistant integration. Built-in help and documentation.                                                                                             | Unified command-line interface for all Coral operations, simplifying developer workflow.                                      |
| **MCP Proxy**         | **Protocol Bridge (Public-facing MCP Server):** The `coral colony mcp proxy` command translates between MCP JSON-RPC (stdio) and Buf Connect gRPC. Reads MCP requests from Claude Desktop/IDEs via stdin. Forwards requests to Colony via Buf Connect RPCs (CallTool, StreamTool, ListTools). Returns responses via stdout. Pure protocol translation with no business logic or database access.                                          | Enables external AI assistants to query Colony observability data using standard MCP protocol over stdio.                     |
| **External LLM Apps** | **AI Assistants & Tool-calling Clients:** Applications like Claude Desktop, Cursor IDE, or custom MCP clients that connect to Colony via the MCP proxy. Query observability data through MCP tools (service health, metrics, traces, events). Run on user's machine with user's LLM provider (Anthropic, OpenAI, local Ollama). Synthesize natural language insights from Coral data.                                                     | Brings AI-powered observability queries to wherever developers are already working (IDE, desktop).                            |
| **`coral ask`**       | **Local AI CLI:** Interactive command-line AI assistant powered by Genkit. Uses local LLM (Ollama) or user's API keys (OpenAI, Anthropic). Connects to Colony as MCP client via Genkit's native MCP plugin. Queries Colony data through same MCP tools as Claude Desktop. Enables terminal-based AI conversations about system health, debugging, and incident investigation without leaving the command line.                            | Provides AI-powered observability in the terminal, using local compute for fast iteration and privacy.                        |
| **Reef**              | **Global Aggregation & Enterprise LLM Host:** Federates multiple colonies across regions/environments. Central data warehouse (ClickHouse/TimescaleDB) for long-term storage and cross-colony analytics. Hosts single enterprise-grade LLM for consistent, auditable AI-powered RCA and insights. Provides global dashboard view. Enables cross-environment correlation (prod vs staging) and deployment timeline analysis.               | Ensures consistency across organization, controls costs with centralized LLM, and enables global observability insights.      |

* * *

## 🔑 Key Features and Data Flows

### 1\. Colony as the MCP Gateway

The Colony now exclusively acts as the **Control Plane** for the mesh. It
exposes a standard set of
tool calls (like `issue_dynamic_probe`, `query_trace_data`) that are consumed by
external LLM
agents. Every request must pass the Colony's **audit and RBAC checks**, making
it the central
security enforcement point.

### 2\. Developer Empowerment (The `coral ask` Command)

The developer uses their local machine's compute power for the LLM's reasoning:

- **Local LLM Agent:** The `coral ask` command launches an agent that can host
  an LLM (e.g., Llama
    3) on the developer's workstation.

- **Secure Connection:** This local agent connects to the Colony via the *
  *secure WireGuard mesh**
  and communicates using the Colony's MCP API.

- **Direct Stream:** When the LLM decides to initiate a **live probe** for RCA,
  the Colony issues a
  **short-lived Delegate JWT**, allowing the developer's local agent to
  establish a direct,
  low-latency data stream with the target **Agent** (bypassing the Colony for
  data flow, but not for
  authorization).

### 3\. Reef's Centralized Intelligence

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
     │  • MCP Server      │  ← Internal implementation (Genkit)
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

- Uses **Genkit Go SDK** (`github.com/firebase/genkit/go/plugins/mcp`) for MCP
  server implementation
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
# Used by Claude Desktop
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
- **internal/colony/mcp/README.md:** Internal architecture details
- **internal/colony/mcp/TESTING.md:** E2E testing guide
