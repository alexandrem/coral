---
rfd: "028"
title: "CLI-Hosted Intelligence with Ephemeral Mesh Access"
state: "draft"
breaking_changes: true
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "002", "004", "010", "012", "013", "014", "023" ]
database_migrations: [ ]
areas: [ "ai", "cli", "colony", "mcp", "architecture" ]
---

# RFD 028 - CLI-Hosted Intelligence with Ephemeral Mesh Access

**Status:** 🚧 Draft

## Summary

Shift the intelligence layer from colony-hosted LLM services to CLI-hosted
runtimes, where developers run `coral ask` with their own LLM provider (cloud
API with personal tokens or local models). The CLI establishes ephemeral agent
identity, joins the mesh temporarily, and spins up an LLM client that queries
colony-hosted MCP tools. Colony remains the central coordinator for RBAC,
auditing, mesh control, and data aggregation, but no longer hosts or manages LLM
services. This eliminates colony-side LLM hosting costs, enables per-developer
model flexibility, distributes compute/cost to users, and preserves
organizational governance through centralized tool invocation logging.

## Problem

**Current behavior (RFD 014 architecture):**

- Colony hosts LLM service with Genkit orchestration.
- Colony manages AI provider configuration (API keys, model selection).
- Colony runs LLM inference for every `coral ask` invocation.
- Teams must standardize on a single LLM provider/model configured in colony.
- Colony incurs ongoing LLM hosting costs and operational complexity.
- Developers cannot choose their preferred LLM or use personal API keys.

**Why this matters:**

- **Operational burden**: Colony must host, configure, and maintain LLM
  services (Genkit runtime, API key management, cost controls).
- **Single model lock-in**: Organization forced onto one LLM provider;
  developers lose flexibility to use preferred models.
- **Centralized cost**: Organization pays for all LLM usage; no per-developer
  cost ownership.
- **Hosting requirement**: Colony becomes a heavyweight service requiring
  permanent hosting for LLM capabilities.
- **Air-gap complexity**: Running local models on colony requires additional
  infrastructure instead of leveraging developer laptops.

**Use cases affected:**

- Developer wants to use Claude Desktop with their own Anthropic API key for
  `coral ask` queries.
- Team wants some developers using GPT-4, others using local Llama models,
  without colony configuration changes.
- Organization wants to avoid hosting colony-side LLM services but still enable
  AI-powered operations.
- Developer working air-gapped wants to use locally hosted models on their
  laptop.
- Security-conscious team wants LLM inference to run on developer machines, not
  centralized servers.

## Solution

**Invert the intelligence architecture**: Move LLM hosting from colony to CLI,
while keeping colony as the authoritative MCP server for data access and
governance.

### Key Design Decisions

**1. CLI as Intelligence Layer:**

- `coral ask` command spins up LLM runtime locally on developer's machine.
- Developer configures their own LLM provider (cloud API key or local model
  path).
- CLI manages LLM lifecycle (start, prompt, stream, cleanup) per invocation.

**2. Ephemeral Agent Identity:**

- CLI requests short-lived agent identity from colony when running `coral ask`.
- Colony issues ephemeral WireGuard identity with limited TTL (e.g., 5 minutes).
- CLI joins mesh temporarily, tears down after query completes.
- Colony tracks ephemeral agents separately from permanent agents for auditing.

**3. Colony as MCP Server:**

- Colony exposes MCP tools for data access: `coral_query`, `coral_fetch_live`,
  `coral_get_topology`.
- LLM running in CLI acts as MCP client, calling colony tools to retrieve
  context.
- Colony validates RBAC on every tool invocation, logs all access, returns
  structured data.
- Colony aggregates DuckDB data from agents, performs remote attaches for live
  queries.

**4. Per-Developer Model Flexibility:**

- Each developer configures their own LLM in `~/.coral/config.yaml`.
- Supports cloud APIs (OpenAI, Anthropic, Google) with personal API keys.
- Supports local models (Ollama, Llama.cpp) running on developer laptop.
- No colony-side configuration required for LLM providers.

**5. Governance Preserved:**

- Colony logs every MCP tool invocation: who, what query, when, which data
  accessed.
- Colony enforces RBAC on all queries (developer permissions checked per
  request).
- Optional: Colony stores "insight transcripts" (LLM conversation summaries) for
  compliance.
- Colony maintains audit trail of all intelligence operations without hosting
  LLM.

**Benefits:**

- **No colony-side LLM hosting**: Eliminates Genkit dependency, API key
  management, LLM cost controls from colony.
- **Developer flexibility**: Each developer chooses their own LLM provider and
  model without team coordination.
- **Cost distribution**: Developers pay for their own API usage via personal
  accounts.
- **Lightweight colony**: Colony remains pure coordination/governance layer,
  easier to deploy and maintain.
- **Air-gap friendly**: Local models run on developer laptops, no colony-side AI
  infrastructure needed.
- **Governance intact**: All queries logged and audited by colony; organization
  retains visibility and control.

**Architecture Overview:**

```
┌─────────────────────────────────────────────────────────┐
│  Developer Laptop                                       │
│                                                         │
│  $ coral ask "Why is checkout slow?"                    │
│         ↓                                               │
│  ┌──────────────────────────────────────────┐          │
│  │  Coral CLI (Intelligence Layer)          │          │
│  │  ├─ Request ephemeral mesh access        │          │
│  │  ├─ Join WireGuard mesh (short-lived)    │          │
│  │  ├─ Spin up LLM runtime (user config)    │          │
│  │  └─ LLM acts as MCP client               │          │
│  └───────────────┬──────────────────────────┘          │
│                  │                                      │
│  ┌───────────────▼──────────────────────────┐          │
│  │  LLM Runtime (User's Choice)             │          │
│  │  • Cloud API (user's key): GPT-4, Claude │          │
│  │  • Local model: Ollama, Llama.cpp        │          │
│  └───────────────┬──────────────────────────┘          │
│                  │ MCP Client                           │
└──────────────────┼──────────────────────────────────────┘
                   │
                   │ WireGuard Mesh (ephemeral)
                   │
       ┌───────────▼────────────┐
       │  Colony                │
       │  (MCP Server)          │
       │                        │
       │  ├─ Validate RBAC      │
       │  ├─ Issue ephemeral ID │
       │  ├─ Expose MCP tools   │
       │  ├─ Query agents       │
       │  ├─ Aggregate DuckDB   │
       │  └─ Log all access     │
       └────────┬───────────────┘
                │
        ┌───────┼───────┐
        │       │       │
        ▼       ▼       ▼
    ┌────────┬────────┬────────┐
    │ Agent  │ Agent  │ Agent  │
    │ (API)  │ (DB)   │ (Web)  │
    │ DuckDB │ DuckDB │ DuckDB │
    └────────┴────────┴────────┘
```

**The Flow (Detailed):**

```
1. Developer runs: coral ask "Why is checkout slow?"

2. CLI checks config: ~/.coral/config.yaml
   ai:
     provider: "anthropic"
     api_key: "env://ANTHROPIC_API_KEY"

3. CLI → Colony: RequestEphemeralAccess()
   - Colony validates developer credentials (existing auth)
   - Colony issues ephemeral agent ID (TTL: 5m)
   - Colony generates WireGuard keypair for ephemeral agent
   - Colony returns: {agent_id, wireguard_config, colony_mcp_endpoint}

4. CLI joins mesh:
   - Configure WireGuard interface with ephemeral identity
   - Connect to colony via WireGuard tunnel
   - Establish gRPC connection to colony MCP server

5. CLI spins up LLM:
   - Initialize LLM client (Anthropic SDK with user's API key)
   - Set up MCP client pointing to colony endpoint
   - Prepare system prompt with available MCP tools

6. LLM reasoning:
   - LLM receives question: "Why is checkout slow?"
   - LLM decides to call: coral_query("checkout service latency, last 1h")

7. CLI → Colony MCP: coral_query(...)
   - Colony validates RBAC (developer can query checkout service?)
   - Colony logs: {user, query, timestamp, tool}
   - Colony fans out to relevant agents (checkout, payment, db)
   - Colony aggregates DuckDB data + remote attach for live metrics
   - Colony returns: {latency_p95: 450ms, normal: 150ms, spike_at: 14:32}

8. LLM synthesizes:
   - LLM receives data from colony
   - LLM decides to call: coral_query("checkout deployments, last 4h")
   - (Repeat step 7 for deployment query)
   - LLM correlates: "Deployment at 14:30, latency spike at 14:32"

9. CLI displays answer:
   "Checkout is slow due to deployment at 14:30 introducing a database
    query regression. Latency increased from 150ms → 450ms (p95).
    Recommend rollback to previous version."

10. CLI cleanup:
    - Disconnect from mesh
    - Remove ephemeral WireGuard config
    - Shutdown LLM runtime
    - Colony expires ephemeral agent ID after TTL
```

### Component Changes

**1. Colony (MCP Server)**

New capabilities:

- Ephemeral agent identity management:
    - RPC: `RequestEphemeralAccess(user_id) → EphemeralAccessToken`
    - Generate short-lived WireGuard configs (5-15 minute TTL)
    - Track ephemeral agents separately from permanent agents
    - Auto-expire ephemeral identities after TTL

- MCP server implementation (extends RFD 004):
    - Expose MCP tools for data access:
        - `coral_query(query, scope, time_range)` - Query aggregated metrics
        - `coral_fetch_live(agent_id, query)` - Remote attach to agent DuckDB
        - `coral_get_topology()` - Service dependency graph
        - `coral_query_events(event_type, time_range)` - Search events
        - `coral_get_health(service)` - Current health status
    - Validate RBAC on every tool invocation
    - Log all tool calls with user attribution
    - Aggregate data from agent DuckDBs before returning

- Audit logging:
    - Log ephemeral agent access requests
    - Log MCP tool invocations with query details
    - Optional: Store insight transcripts (LLM conversation summaries)

**What colony NO LONGER does** (removes from RFD 014):

- ❌ Host LLM service (Genkit)
- ❌ Manage LLM provider configuration
- ❌ Store LLM API keys
- ❌ Run LLM inference
- ❌ Implement LLM cost controls
- ❌ Cache LLM responses

**2. CLI (`coral ask` command)**

New implementation (replaces RFD 014's simple RPC approach):

```bash
coral ask [question] [flags]
  --model <provider>       # Override config LLM provider
  --stream                 # Stream LLM response
  --transcript <file>      # Save conversation transcript
  --ephemeral-ttl <dur>    # Ephemeral access TTL (default: 5m)
  --offline                # Use local model only (no mesh access)
```

Flow:

1. Load LLM config from `~/.coral/config.yaml`
2. Request ephemeral mesh access from colony
3. Join WireGuard mesh with ephemeral identity
4. Initialize LLM runtime (cloud API or local model)
5. Set up MCP client pointing to colony
6. Send question to LLM with MCP tools available
7. Stream LLM response to terminal
8. Cleanup: disconnect mesh, shutdown LLM
9. Optional: Upload transcript to colony for auditing

**3. MCP Client Library (`internal/cli/mcp/client`)**

New package for CLI to act as MCP client:

- Connect to colony MCP server via WireGuard mesh
- Call MCP tools programmatically
- Handle streaming responses
- Retry logic for transient failures

**4. LLM Runtime Manager (`internal/cli/llm/runtime`)**

New package for managing LLM lifecycle:

- Initialize LLM clients:
    - Cloud APIs: OpenAI, Anthropic, Google (using user's API keys)
    - Local models: Ollama, Llama.cpp (subprocess management)
- Prompt management: system prompts with MCP tool descriptions
- Streaming response handling
- Token counting and basic cost estimation (for user visibility)

**5. Ephemeral Agent Manager (`internal/colony/ephemeral`)**

New package in colony:

- Issue ephemeral agent identities with TTL
- Generate WireGuard keypairs for ephemeral agents
- Track active ephemeral agents (for auditing and debugging)
- Auto-expire and cleanup after TTL
- Differentiate ephemeral vs permanent agents in logs

**Configuration Example:**

Developer config (`~/.coral/config.yaml`):

```yaml
# User-specific LLM configuration (not shared with colony)
ai:
    provider: "anthropic"           # anthropic, openai, google, ollama
    api_key: "env://ANTHROPIC_API_KEY"  # Reference env var
    model: "claude-sonnet-4.5"      # Model selection

    # Optional overrides
    max_tokens: 4096
    temperature: 0.7
    stream: true

    # Local model config (alternative to cloud API)
    # provider: "ollama"
    # model: "llama3.1:8b"
    # endpoint: "http://localhost:11434"

# Colony connection (existing config)
colonies:
    -   id: "my-shop-production"
        endpoint: "coral.mycompany.com:41820"

# Ephemeral access settings
ephemeral:
    default_ttl: "5m"
    auto_cleanup: true
```

Colony config (`colony.yaml`) - **LLM config removed**:

```yaml
# Colony config NO LONGER includes AI/LLM settings
# (those are now in user config)

# MCP server settings
mcp_server:
    enabled: true
    tools:
        - coral_query
        - coral_fetch_live
        - coral_get_topology
        - coral_query_events
        - coral_get_health

    # RBAC for MCP tools
    rbac:
        coral_query:
            required_permission: "read:metrics"
        coral_fetch_live:
            required_permission: "read:live_data"

# Ephemeral agent settings
ephemeral_agents:
    enabled: true
    default_ttl: "5m"
    max_ttl: "15m"
    max_concurrent_per_user: 3

# Audit logging (enhanced for ephemeral access)
audit:
    log_ephemeral_access: true
    log_mcp_tool_calls: true
    store_transcripts: false  # Optional: store LLM conversation logs
```

## Implementation Plan

### Phase 1: Ephemeral Agent Infrastructure

- [ ] Design ephemeral agent identity lifecycle
- [ ] Implement `RequestEphemeralAccess` RPC in colony
- [ ] Generate WireGuard configs with TTL
- [ ] Track ephemeral agents in colony registry
- [ ] Auto-expire ephemeral agents after TTL
- [ ] Add ephemeral agent logging/auditing

### Phase 2: Colony MCP Server

- [ ] Implement MCP server in colony (extends RFD 004)
- [ ] Expose MCP tools: `coral_query`, `coral_fetch_live`, etc.
- [ ] Validate RBAC on MCP tool invocations
- [ ] Query agent DuckDBs and aggregate results
- [ ] Log all MCP tool calls with user attribution
- [ ] Handle remote attach for live data queries

### Phase 3: CLI LLM Runtime

- [ ] Implement LLM runtime manager (`internal/cli/llm/runtime`)
- [ ] Support cloud APIs: OpenAI, Anthropic, Google
- [ ] Support local models: Ollama, Llama.cpp
- [ ] Load user LLM config from `~/.coral/config.yaml`
- [ ] Prompt engineering: system prompt with MCP tool descriptions
- [ ] Handle streaming responses to terminal

### Phase 4: CLI MCP Client

- [ ] Implement MCP client library (`internal/cli/mcp/client`)
- [ ] Connect to colony MCP server over mesh
- [ ] Call MCP tools from LLM runtime
- [ ] Handle tool responses and return to LLM
- [ ] Error handling and retries

### Phase 5: CLI `coral ask` Integration

- [ ] Refactor `coral ask` to use new architecture
- [ ] Request ephemeral mesh access from colony
- [ ] Join mesh with ephemeral WireGuard config
- [ ] Initialize LLM runtime with user config
- [ ] Wire up MCP client to LLM
- [ ] Display streaming responses
- [ ] Cleanup: disconnect mesh, shutdown LLM
- [ ] Optional: Upload transcript to colony

### Phase 6: Testing & Documentation

- [ ] Unit tests: Ephemeral agent lifecycle
- [ ] Unit tests: MCP client/server interaction
- [ ] Integration tests: End-to-end `coral ask` flow
- [ ] E2E tests: Multiple concurrent ephemeral agents
- [ ] Security tests: RBAC enforcement, audit logging
- [ ] Performance tests: Ephemeral agent startup latency
- [ ] Documentation: User guide for LLM configuration
- [ ] Documentation: Migration guide from RFD 014 architecture

## API Changes

### New Protobuf: Ephemeral Agent Access

```protobuf
// proto/coral/colony/v1/ephemeral.proto
syntax = "proto3";
package coral.colony.v1;

import "google/protobuf/duration.proto";
import "google/protobuf/timestamp.proto";

message RequestEphemeralAccessRequest {
    string user_id = 1;
    google.protobuf.Duration ttl = 2;  // Requested TTL (max: 15m)
    string purpose = 3;                 // "coral ask", "coral proxy", etc.
}

message RequestEphemeralAccessResponse {
    string agent_id = 1;
    string wireguard_config = 2;        // Full WireGuard config
    string colony_mcp_endpoint = 3;     // gRPC endpoint for MCP server
    google.protobuf.Timestamp expires_at = 4;
    string access_token = 5;            // Token for MCP authentication
}

message ReleaseEphemeralAccessRequest {
    string agent_id = 1;
}

message ReleaseEphemeralAccessResponse {
    bool success = 1;
}

service EphemeralAgentService {
    rpc RequestEphemeralAccess(RequestEphemeralAccessRequest)
        returns (RequestEphemeralAccessResponse);

    rpc ReleaseEphemeralAccess(ReleaseEphemeralAccessRequest)
        returns (ReleaseEphemeralAccessResponse);
}
```

### MCP Tools Specification

Colony exposes these MCP tools (JSON Schema):

```json
{
    "tools": [
        {
            "name": "coral_query",
            "description": "Query aggregated metrics and telemetry from agents",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "query": {
                        "type": "string",
                        "description": "Natural language query or SQL-like expression"
                    },
                    "scope": {
                        "type": "string",
                        "description": "Service or agent scope (e.g., 'checkout', 'api')"
                    },
                    "time_range": {
                        "type": "string",
                        "description": "Time range: '1h', '24h', '7d'",
                        "default": "1h"
                    }
                },
                "required": [
                    "query"
                ]
            }
        },
        {
            "name": "coral_fetch_live",
            "description": "Fetch live data from specific agent's DuckDB",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "agent_id": {
                        "type": "string",
                        "description": "Target agent ID"
                    },
                    "sql_query": {
                        "type": "string",
                        "description": "SQL query to execute on agent's DuckDB"
                    }
                },
                "required": [
                    "agent_id",
                    "sql_query"
                ]
            }
        },
        {
            "name": "coral_get_topology",
            "description": "Get service dependency graph and topology",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "format": {
                        "type": "string",
                        "enum": [
                            "json",
                            "graphviz"
                        ],
                        "default": "json"
                    }
                }
            }
        },
        {
            "name": "coral_query_events",
            "description": "Search events (deploys, restarts, crashes, errors)",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "event_type": {
                        "type": "string",
                        "description": "Event type filter",
                        "enum": [
                            "deploy",
                            "restart",
                            "crash",
                            "error_spike",
                            "alert"
                        ]
                    },
                    "time_range": {
                        "type": "string",
                        "default": "24h"
                    },
                    "service": {
                        "type": "string",
                        "description": "Optional service filter"
                    }
                }
            }
        },
        {
            "name": "coral_get_health",
            "description": "Get current health status of services",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "service_filter": {
                        "type": "string",
                        "description": "Optional service name pattern"
                    }
                }
            }
        }
    ]
}
```

### CLI Commands

```bash
# Primary command (new implementation)
coral ask [question] [flags]
  --model <provider>       # Override LLM provider
  --stream                 # Stream response (default: true)
  --transcript <file>      # Save transcript locally
  --ephemeral-ttl <dur>    # Ephemeral access TTL
  --offline                # Use local model, no mesh access

# Examples:
coral ask "Why is checkout slow?"
coral ask "Compare prod vs staging latency" --model gpt-4
coral ask "Show me recent deployments" --transcript ./chat.txt

# LLM configuration
coral llm configure [flags]
  --provider <name>        # anthropic, openai, google, ollama
  --api-key <key>          # API key (or env var reference)
  --model <name>           # Model name
  --test                   # Test configuration

# Examples:
coral llm configure --provider anthropic --api-key env://ANTHROPIC_API_KEY
coral llm configure --provider ollama --model llama3.1:8b --test

# List active ephemeral agents (for debugging)
coral colony ephemeral list [flags]
  --user <user-id>         # Filter by user

# Example output:
Agent ID                 User        Created   Expires   Purpose
eph-abc123               alex        2m ago    3m        coral ask
eph-def456               jordan      30s ago   4m30s     coral proxy
```

## Testing Strategy

### Unit Tests

**Ephemeral Agent Lifecycle:**

- Request ephemeral access with valid credentials
- Generate WireGuard config with correct TTL
- Auto-expire ephemeral agents after TTL
- Reject requests exceeding max TTL
- Enforce max concurrent ephemeral agents per user

**MCP Client/Server:**

- Call MCP tools from CLI client
- Validate tool schemas
- Handle tool responses
- Error handling: tool not found, invalid arguments

**LLM Runtime:**

- Initialize cloud API clients (mock API)
- Initialize local model clients (mock Ollama)
- Handle streaming responses
- Error handling: API key invalid, model not found

### Integration Tests

**End-to-End `coral ask` Flow:**

1. CLI requests ephemeral access
2. Colony issues ephemeral identity
3. CLI joins mesh
4. CLI initializes LLM
5. LLM calls MCP tools
6. Colony returns data
7. LLM synthesizes answer
8. CLI displays result
9. CLI disconnects and cleans up

**RBAC Enforcement:**

- User without `read:metrics` permission calls `coral_query` → denied
- User with permission calls `coral_query` → succeeds
- Audit log captures both attempts

**Multiple Concurrent Ephemeral Agents:**

- Multiple developers run `coral ask` simultaneously
- Each gets separate ephemeral identity
- Colony tracks all concurrent sessions
- All sessions expire correctly after TTL

### E2E Tests

**Scenario 1: Cloud API (Anthropic)**

1. Developer configures Anthropic API key
2. Run `coral ask "Is production healthy?"`
3. Verify: CLI joins mesh, LLM calls colony MCP tools, answer displayed
4. Verify: Colony audit log shows MCP tool calls from ephemeral agent

**Scenario 2: Local Model (Ollama)**

1. Developer configures Ollama with local model
2. Run `coral ask "Show recent deploys"`
3. Verify: CLI uses local model, no external API calls
4. Verify: MCP tool calls still logged by colony

**Scenario 3: Ephemeral Agent Expiry**

1. Developer runs `coral ask` (long-running query)
2. Ephemeral agent TTL expires mid-query
3. Verify: CLI handles gracefully, reconnects if needed, or fails with clear
   error
4. Verify: Colony cleans up expired ephemeral agent

## Security Considerations

### Ephemeral Agent Identity

**Threat**: Malicious user obtains ephemeral identity and retains mesh access
beyond TTL.

**Mitigation**:

- Colony enforces TTL strictly; WireGuard peer removed after expiry
- Ephemeral agent ID embedded in WireGuard pubkey for traceability
- Colony monitors ephemeral agents for suspicious activity (rate limiting,
  unusual queries)

**Threat**: Ephemeral agent impersonation.

**Mitigation**:

- Ephemeral access token signed by colony with short expiry
- Colony validates token on every MCP tool call
- Token includes user ID, agent ID, permissions, expires_at

### MCP Tool RBAC

**Threat**: User without permissions calls restricted MCP tools.

**Mitigation**:

- Colony validates permissions on every MCP tool call
- RBAC configured per-tool (e.g., `coral_query` requires `read:metrics`)
- Audit log captures all access attempts (allowed and denied)

**Example RBAC enforcement**:

```yaml
# colony.yaml
mcp_server:
    rbac:
        coral_query:
            required_permission: "read:metrics"
        coral_fetch_live:
            required_permission: "read:live_data"
            additional_check: "agent_owner_match"  # User must own agent
```

### User API Key Security

**Threat**: User's LLM API key exposed in CLI config.

**Mitigation**:

- Config file supports env var references: `env://ANTHROPIC_API_KEY`
- Config file permissions: `chmod 600 ~/.coral/config.yaml`
- Documentation emphasizes never committing API keys to git

**Threat**: User's API key sent to colony.

**Mitigation**:

- API keys stay local; never transmitted to colony
- LLM runtime runs on developer machine only
- Colony never sees or logs LLM API keys

### Data Exposure

**Threat**: Sensitive telemetry data exposed to LLM cloud API.

**Mitigation**:

- User chooses LLM provider; understands data leaves machine
- Documentation warns about cloud API data transmission
- Option to use local models (Ollama) for air-gapped/sensitive environments
- Colony MCP tools filter sensitive fields before returning data

**Example data filtering**:

```yaml
# colony.yaml
mcp_server:
    data_filtering:
        redact_fields:
            - "*.password"
            - "*.api_key"
            - "*.secret"
        max_response_size: "1MB"  # Prevent excessive data exfiltration
```

### Audit Logging

**Requirement**: Organization must audit all intelligence operations for
compliance.

**Approach**:

- Colony logs every ephemeral agent access request
- Colony logs every MCP tool invocation with query details
- Optional: CLI uploads conversation transcript to colony
- Audit logs immutable (append-only)

**Audit log schema**:

```sql
CREATE TABLE ephemeral_agent_audit
(
    id          UUID PRIMARY KEY,
    timestamp   TIMESTAMPTZ NOT NULL,
    user_id     VARCHAR     NOT NULL,
    agent_id    VARCHAR     NOT NULL,
    action      VARCHAR     NOT NULL, -- "request_access", "release_access", "expired"
    ttl_seconds INTEGER,
    ip_address  INET,
    user_agent  VARCHAR
);

CREATE TABLE mcp_tool_audit
(
    id                  UUID PRIMARY KEY,
    timestamp           TIMESTAMPTZ NOT NULL,
    user_id             VARCHAR     NOT NULL,
    agent_id            VARCHAR, -- Ephemeral agent ID
    tool_name           VARCHAR     NOT NULL,
    tool_args           JSON,
    response_size_bytes INTEGER,
    success             BOOLEAN     NOT NULL,
    error_message       TEXT,
    execution_time_ms   INTEGER
);

-- Optional: Store LLM conversation transcripts
CREATE TABLE ask_transcripts
(
    id            UUID PRIMARY KEY,
    timestamp     TIMESTAMPTZ NOT NULL,
    user_id       VARCHAR     NOT NULL,
    agent_id      VARCHAR,
    question      TEXT        NOT NULL,
    answer        TEXT,
    llm_provider  VARCHAR, -- "anthropic", "openai", "ollama"
    llm_model     VARCHAR,
    tool_calls    JSON,    -- Array of MCP tool calls made
    tokens_input  INTEGER,
    tokens_output INTEGER
);
```

## Migration Strategy

**Breaking Changes from RFD 014:**

- Colony no longer hosts LLM services (Genkit removed).
- Colony config no longer includes AI/LLM settings.
- `coral ask` requires user LLM configuration in `~/.coral/config.yaml`.
- Developers must configure their own LLM provider/API keys.

**Migration Path:**

**Phase 1: Prepare (RFD 014 still active)**

1. Implement ephemeral agent infrastructure in colony.
2. Implement colony MCP server (parallel to existing LLM service).
3. Allow both architectures to coexist temporarily.

**Phase 2: CLI Transition**

1. Release new `coral ask` implementation using CLI-hosted LLM.
2. Document user migration: configure LLM in `~/.coral/config.yaml`.
3. Old `coral ask` (calling colony LLM service) still works during transition.

**Phase 3: Deprecate Colony LLM**

1. Add deprecation warning to colony LLM service.
2. Announce sunset timeline (e.g., 90 days).
3. Encourage all users to migrate to CLI-hosted LLM.

**Phase 4: Remove Colony LLM**

1. Remove Genkit dependency from colony.
2. Remove LLM service code from colony.
3. Remove AI/LLM config from colony config schema.
4. Release colony as pure coordination/MCP server.

**User Communication:**

```
Subject: Action Required: Coral AI Configuration Migration

We're improving Coral's AI architecture to give you more flexibility!

**What's Changing:**
- AI/LLM now runs on your machine (not colony)
- You choose your own LLM provider (OpenAI, Anthropic, or local models)
- Your API keys, your cost, your control

**Action Required:**
1. Configure your preferred LLM in ~/.coral/config.yaml:

   ai:
     provider: "anthropic"
     api_key: "env://ANTHROPIC_API_KEY"
     model: "claude-sonnet-4.5"

2. Test: coral ask "Is production healthy?"

**Timeline:**
- Now: New architecture available, opt-in
- [Date]: Old architecture deprecated
- [Date + 90d]: Old architecture removed

**Questions?** See docs: https://coral-io.dev/docs/cli-ai-migration
```

## Future Enhancements

### Reef Integration

Extend CLI-hosted intelligence to query across multiple colonies via Reef:

```bash
coral reef ask "Compare latency across all environments"
  ↓
CLI requests ephemeral reef access
  ↓
Reef MCP server aggregates data from all colonies
  ↓
LLM receives unified view
```

### Shared Context Cache

While LLM runs client-side, colony could cache frequently accessed context:

```yaml
# colony.yaml
mcp_server:
    context_cache:
        enabled: true
        ttl: "5m"
        max_size: "100MB"
```

Colony caches aggregated query results to speed up repeated `coral_query` calls.

### Conversation History

CLI maintains local conversation history for multi-turn interactions:

```bash
coral ask "Show checkout latency"
  → "Checkout p95 latency: 250ms"

coral ask "How does that compare to yesterday?"
  → (CLI includes previous context in prompt)
  → "Yesterday was 180ms; +39% increase since deploy at 14:30"
```

### Collaborative Insights

Optional: Developers share insights with team via colony:

```bash
coral ask "Why slow?" --share
  ↓
CLI uploads conversation transcript to colony
  ↓
Other developers can view: coral colony insights list
```

## Appendix

### Example User Configurations

**Cloud API (Anthropic):**

```yaml
# ~/.coral/config.yaml
ai:
    provider: "anthropic"
    api_key: "env://ANTHROPIC_API_KEY"
    model: "claude-sonnet-4.5"
    max_tokens: 4096
    stream: true
```

**Cloud API (OpenAI):**

```yaml
ai:
    provider: "openai"
    api_key: "env://OPENAI_API_KEY"
    model: "gpt-4-turbo"
    temperature: 0.7
```

**Local Model (Ollama):**

```yaml
ai:
    provider: "ollama"
    model: "llama3.1:8b"
    endpoint: "http://localhost:11434"
    context_length: 8192
```

**Air-Gapped (Llama.cpp):**

```yaml
ai:
    provider: "llamacpp"
    model_path: "/usr/local/models/llama-3.1-8b.gguf"
    threads: 8
    context_length: 8192
```

### Comparison: Old vs New Architecture

| Aspect                | RFD 014 (Colony-Hosted)     | RFD 028 (CLI-Hosted)            |
|-----------------------|-----------------------------|---------------------------------|
| **LLM Location**      | Colony server               | Developer laptop                |
| **LLM Config**        | Colony config (shared)      | User config (personal)          |
| **API Keys**          | Colony manages              | User manages                    |
| **Model Choice**      | Single (team decision)      | Per-user (flexibility)          |
| **Cost**              | Organization pays           | User pays (own API key)         |
| **Colony Complexity** | High (Genkit, API keys)     | Low (MCP server only)           |
| **Air-Gap Support**   | Requires colony-side model  | Laptop-side model               |
| **Governance**        | Colony logs all prompts     | Colony logs MCP tool calls      |
| **Privacy**           | Prompts stored in colony    | Prompts local (optional upload) |
| **Latency**           | Network RTT to colony       | Local LLM startup               |
| **Scalability**       | Colony handles all LLM load | Distributed to users            |

### Relationship to Other RFDs

- **RFD 004 (MCP Server)**: This RFD implements colony MCP server for CLI to
  consume. RFD 004 remains valid; this RFD extends it with ephemeral agent
  access.

- **RFD 014 (Colony LLM)**: This RFD **supersedes** RFD 014. Colony no longer
  hosts LLM; intelligence moves to CLI.

- **RFD 023 (NAT Traversal)**: Ephemeral agents use same discovery/NAT traversal
  as permanent agents.

- **Reef (RFD 003)**: Reef can also expose MCP server for cross-colony queries.
  CLI can connect to reef MCP for unified intelligence.

### Frequently Asked Questions

**Q: What if I don't want to configure an LLM?**

A: `coral ask` requires LLM configuration. However, you can use free local
models (Ollama) with no API key needed.

**Q: Does this work air-gapped?**

A: Yes! Use local models (Ollama, Llama.cpp). Colony MCP server works over
WireGuard mesh without internet.

**Q: Can I still use Claude Desktop with Coral?**

A: Yes! RFD 004 (MCP Server for Claude Desktop) remains valid. Claude Desktop
connects to colony MCP server directly (no CLI needed).

**Q: What about teams that want centralized LLM?**

A: Organizations can deploy Reef with centralized LLM if desired. This RFD makes
it optional, not mandatory.

**Q: How do I switch LLM providers?**

A: Edit `~/.coral/config.yaml` and change `ai.provider` + `ai.model`. No colony
configuration needed.

**Q: What happens if my ephemeral agent expires during a long query?**

A: CLI detects expiry, requests new ephemeral access, rejoins mesh, and retries.
User sees brief "Reconnecting..." message.

**Q: Does colony still log my questions?**

A: Colony logs MCP tool calls (what data you accessed), not your full LLM
conversation. Optionally upload transcript for compliance.

**Q: Can I use multiple LLMs simultaneously?**

A: Not in single `coral ask` invocation, but you can run multiple `coral ask`
commands concurrently with different `--model` flags.

---

**Dependencies:**

- RFD 002: Application identity (colony validates user permissions)
- RFD 004: MCP server integration (colony exposes MCP tools)
- RFD 010: Mesh networking (ephemeral agents join mesh)
- RFD 012: Authentication (colony validates ephemeral access requests)
- RFD 013: RBAC (colony enforces permissions on MCP tool calls)
- RFD 014: Colony LLM integration (this RFD supersedes it)
- RFD 023: NAT traversal (ephemeral agents use discovery service)

**Breaking Changes:**

- ✅ Colony LLM service removed (Genkit dependency dropped)
- ✅ Colony AI config removed from `colony.yaml`
- ✅ `coral ask` requires user LLM config in `~/.coral/config.yaml`
- ✅ Migration path provided (see Migration Strategy section)
