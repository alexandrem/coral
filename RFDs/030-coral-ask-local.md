---
rfd: "030"
title: "Coral Ask - Local LLM Integration"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "002", "004" ]
database_migrations: [ ]
areas: [ "ai", "cli", "mcp" ]
---

# RFD 030 - Coral Ask: Local LLM Integration

**Status:** 🎉 Implemented

<!--
Status progression:
  🚧 Draft → 👀 Under Review → ✅ Approved → 🔄 In Progress → 🎉 Implemented
-->

## Summary

Implement `coral ask` CLI command using local LLM agent that connects to Colony
as an MCP client via buf Connect RPC. The LLM runs on the developer's machine
using direct API integrations, while Colony provides a stateless MCP server
exposing data access tools. This design offloads LLM compute from Colony,
enables flexible model choice, and maintains cost control at the developer
level.

**Implementation Note:** Originally designed with Genkit (Firebase's AI
framework), the final implementation uses direct LLM API clients for simplicity
and better control. Currently, only Google AI (Gemini) is supported, with
additional providers (OpenAI, Anthropic) planned for future iterations.

## Problem

**Current behavior/limitations:**

- No implemented `coral ask` command for conversational diagnostics
- Developers need fast, iterative debugging without deploying to Reef's
  enterprise LLM
- Colony should remain lightweight control plane, not bear LLM inference costs

**Why this matters:**

- Developers expect AI-assisted debugging: "Why is checkout slow?" should return
  grounded analysis
- Colony hosting LLM creates operational overhead (model management, compute
  costs, scaling)
- Different developers have different model preferences (GPT-4, Claude, local
  Llama)
- Cost control requires user-level LLM ownership (developer's API keys, not
  colony's budget)

**Use cases affected:**

- Quick debugging during active incidents:
  `coral ask "what's causing 500 errors?"`
- Iterative exploration: `coral ask "show me top 5 slowest endpoints"`
- Personal investigations: using local models (Ollama) for air-gapped
  environments
- Multi-turn conversations: follow-up questions maintaining context

## Solution

Implement `coral ask` as a CLI command with an embedded LLM agent. The agent
uses direct API clients for LLM providers (currently Google AI/Gemini) and
connects to Colony's MCP server via buf Connect RPC. Colony provides MCP tools
for data access (`coral_query_beyla_traces`, `coral_get_service_topology`,
etc.), and the LLM performs reasoning on the developer's machine.

**Key Design Decisions:**

- **Direct LLM API Integration**: Uses native API clients instead of Genkit
  framework
    - Currently supports Google AI (Gemini models)
    - Simpler implementation with better control over API interactions
    - Future: Add OpenAI and Anthropic providers

- **Colony RPC Connection**: Agent connects to Colony via buf Connect RPC, not
  stdio
    - Colony MCP server serves tool definitions and handles tool execution
    - Tool schemas marshaled via protobuf for efficient transport
    - Colony remains stateless gateway for data access

- **Flexible deployment**: Agent can run as:
    - Ephemeral process (spawned per `coral ask` invocation)
    - Long-running daemon (amortizes model loading overhead)
    - In-process (embedded in CLI binary for simple cases)

- **Context management**: Agent maintains conversation history locally
    - Multi-turn interactions without re-querying Colony
    - Context pruning based on token limits

- **Configuration-driven**: Developer configures preferred models, API keys,
  fallbacks
    - Support for multiple providers (primary + fallbacks)
    - Developer owns costs via their own API keys

**Benefits:**

- Colony remains lightweight (no LLM runtime, simpler deployment)
- Developer flexibility (choose GPT-4 for complex analysis, Llama for quick
  queries)
- Cost control (developer's API keys = clear cost ownership)
- Offline support (Ollama for air-gapped environments)
- Fast iteration (no round-trip to Reef for simple questions)

**Architecture Overview:**

```
Developer Machine                      Colony (Control Plane)
┌────────────────────────────┐        ┌───────────────────────────────┐
│ coral ask "why slow?"      │        │                               │
│          ↓                 │        │  Colony gRPC Server           │
│ ┌────────────────────────┐ │        │  ┌───────────────────────────┐│
│ │ Ask Agent (Embedded)   │ │        │  │ MCP Tools (RPC):          ││
│ │                        │ │  buf   │  │ - ListTools()             ││
│ │ LLM Provider:          │◄┼────────┼─►│ - CallTool()              ││
│ │ - Gemini (Google AI)   │ │Connect │  │                           ││
│ │   [Currently supported]│ │  RPC   │  │ Available Tools:          ││
│ │                        │ │        │  │ - coral_get_service_health││
│ │ Future:                │ │        │  │ - coral_query_beyla_*     ││
│ │ - GPT-4 (OpenAI)       │ │        │  │ - coral_query_telemetry_* ││
│ │ - Claude (Anthropic)   │ │        │  │ - coral_exec_command      ││
│ │                        │ │        │  └───────────────────────────┘│
│ │ Context:               │ │        │                               │
│ │ - Conversation history │ │        │  MCP Server (Internal)        │
│ │ - Tool schemas (RPC)   │ │        │  - Generates JSON schemas     │
│ └────────────────────────┘ │        │  - Serves via protobuf        │
│                            │        │                               │
│ Config: ~/.coral/config    │        │  WireGuard Mesh               │
│   ai.ask:                  │        └───────────────────────────────┘
│   - API keys (env refs)    │
│   - Model: gemini-1.5-flash│
└────────────────────────────┘
```

### Component Changes

1. **CLI (`internal/cli/ask`)** (new package):
    - Implement `coral ask <question>` command
    - Create embedded agent with LLM provider
    - Stream LLM responses to terminal
    - Handle conversation context (multi-turn via `--continue` flag)
    - Persist conversations to `~/.coral/conversations/`

2. **Ask Agent** (`internal/agent/ask`)** (new package):
    - Load configuration (model selection, API keys from env)
    - Connect to Colony via buf Connect RPC client
    - Fetch tool definitions from Colony's MCP server
    - Execute LLM reasoning with tool calls
    - Manage conversation context and token budgets

3. **LLM Provider** (`internal/llm`)** (new package):
    - Provider abstraction for multi-LLM support
    - Google AI provider (Gemini models) - **currently supported**
    - Convert MCP tool schemas to provider-specific formats
    - Handle streaming and non-streaming responses
    - Future: OpenAI and Anthropic providers

4. **Configuration** (`~/.coral/config.yaml` - local to developer machine):
    - Extends existing `ai` section with new `ask` subsection
    - Config path: `ai.ask` in global config
    - Currently: Single provider (Google AI)
    - Future: Multiple providers with fallbacks
    - Optional per-colony overrides in `colonies.<colony-id>.ask`

5. **MCP Integration** (uses existing Colony MCP server):
    - Colony exposes MCP tools via buf Connect RPC
    - Agent calls `ListTools()` RPC to get tool definitions
    - Agent calls `CallTool()` RPC to execute tools
    - Schemas marshaled via protobuf (fixed to preserve array `items` field)

**Configuration Example:**

```yaml
# ~/.coral/config.yaml (local to developer machine)
version: "1"
default_colony: "my-app-prod"

discovery:
    endpoint: "http://localhost:8080"

# AI configuration (extends existing ai section)
ai:
    provider: "google"        # For coral ask (currently only Google AI supported)
    api_key_source: "env"     # API keys from environment variables

    # coral ask LLM configuration
    ask:
        # Default model (currently only Google AI models supported)
        default_model: "gemini-1.5-flash"   # Fast, cost-effective
        # Alternative: "gemini-1.5-pro" for more complex analysis

        # API key reference (NEVER plain text)
        google_api_key: "env://GOOGLE_API_KEY"

        # Conversation settings
        conversation:
            max_turns: 10             # Conversation history limit
            context_window: 8192      # Max tokens for context
            auto_prune: true          # Prune old messages when limit reached

# Per-colony overrides (optional)
colonies:
    my-app-production-xyz:
        ask:
            default_model: "gemini-1.5-pro"  # Use more capable model for production
```

**Future Configuration (when multi-provider support is added):**

```yaml
ai:
    ask:
        # Multiple providers with fallbacks
        default_model: "openai:gpt-4o-mini"
        fallback_models:
            - "google:gemini-1.5-flash"
            - "anthropic:claude-3-5-sonnet"

        api_keys:
            openai: "env://OPENAI_API_KEY"
            google: "env://GOOGLE_API_KEY"
            anthropic: "env://ANTHROPIC_API_KEY"
```

## Implementation Plan

### Phase 1: Foundation

- [x] Define configuration schema for `ask` section
- [x] Implement configuration loading with env variable references
- [x] Create agent package structure (`internal/agent/genkit`)

### Phase 2: Core Agent Implementation

- [x] Implement provider abstraction for multi-provider support
- [x] Implement Google AI (Gemini) provider with direct API client
- [x] Add Colony RPC client (buf Connect) for MCP tool access
- [x] Implement conversation context management (history, pruning)
- [x] Fix schema generation: Use consistent `generateInputSchema()` for RPC and
  MCP paths
- [x] Fix array schema conversion: Add `items` field support for Google AI API

### Phase 3: CLI Integration

- [x] Implement `coral ask <question>` CLI command
- [x] Add agent lifecycle management (embedded mode)
- [x] Add `--continue` flag for multi-turn conversations
- [x] Add `--model` flag for one-off model override
- [x] Implement response output to terminal (basic streaming supported)

### Phase 4: Testing & Documentation

- [x] Unit tests: All existing tests pass
- [ ] Integration tests: Genkit agent ↔ Colony MCP (deferred to future work)
- [ ] E2E tests: `coral ask` against seeded Colony data (deferred to future
  work)
- [x] Documentation: RFD updated with implementation status

## API Changes

### CLI Commands

```bash
# Basic usage (uses default model from config)
coral ask "why is checkout slow?"

# Expected output:
Analyzing...
✓ Queried 3 services (checkout, payment, database)
✓ Reviewed last 1h of metrics and traces

Finding: Checkout p95 latency increased 45% in last 30 minutes

Root cause:
1. Payment API latency spike (p95: 800ms → 1400ms)
   - Evidence: traces show timeout retries increased 3x
   - Source: coral_query_beyla_traces(service="payment", time_range="1h")

2. Database connection pool exhaustion
   - Pool utilization: 92% (threshold: 80%)
   - Source: coral_query_beyla_sql_metrics(service="database", time_range="1h")

Recommendation:
  Investigate payment API (possible downstream issue)
  Consider increasing connection pool size

---

# Override model for this query
coral ask "complex root cause analysis" --model anthropic:claude-3-5-sonnet-20241022

# Continue previous conversation
coral ask "show me the actual traces" --continue

# Use local model (offline/air-gapped)
coral ask "what's the current status?" --model ollama:llama3.2

# Stream output for long responses
coral ask "summarize last 24h" --stream

# JSON output for scripting
coral ask "list unhealthy services" --json
{
  "answer": "3 services are unhealthy...",
  "citations": [
    {"tool": "coral_get_service_health", "data": {...}}
  ]
}
```

### Configuration Changes

New `ai.ask` subsection in `~/.coral/config.yaml` (extends existing `ai`
section):

- `ai.ask.default_model`: Primary model to use (Genkit provider format)
- `ai.ask.fallback_models`: Array of fallback models
- `ai.ask.api_keys`: Map of provider → env variable reference
- `ai.ask.conversation.max_turns`: Conversation history limit
- `ai.ask.agent.mode`: Agent deployment mode (`daemon`|`ephemeral`|`embedded`)
- `colonies.<colony-id>.ask`: Optional per-colony overrides for model selection

**Rationale for global config:**

- LLM runs on developer's machine (not in Colony)
- Extends existing `ai` section (already contains `provider` and
  `api_key_source`)
- Developer's personal preferences (model choice, API keys)
- Consistent with Coral's configuration hierarchy for user-level settings

**Configuration hierarchy (follows standard Coral precedence):**

1. **Environment variables** (highest priority) - e.g., `CORAL_ASK_MODEL`
2. **Project config** - `<project>/.coral/config.yaml` (if project-specific
   overrides needed)
3. **Colony overrides** - `colonies.<colony-id>.ask` (for colony-specific model
   selection)
4. **Global defaults** - `ai.ask` section (developer's default preferences)
5. **CLI flags** - e.g., `--model` (runtime overrides)
6. **Built-in defaults** (lowest priority)

### Supported LLM Providers

**Currently Supported:**

- **Google AI (Gemini)**: `gemini-1.5-flash`, `gemini-1.5-pro`,
  `gemini-2.0-flash-exp`
    - Fastest implementation: Direct API integration via
      `google.golang.org/genai`
    - Full tool calling support with proper schema conversion
    - Best for: Quick queries (flash), complex analysis (pro)

**Planned (Future Implementation):**

- **OpenAI**: `gpt-4o`, `gpt-4o-mini`, `gpt-4-turbo`
    - Provider abstraction ready, needs API client implementation
    - Full tool calling support expected

- **Anthropic (Claude)**: `claude-3-5-sonnet`, `claude-3-5-haiku`
    - Provider abstraction ready, needs API client implementation
    - Full tool calling support expected

- **Ollama (Local)**: `llama3.2`, `mistral`, `codellama`
    - For air-gapped/offline environments
    - Requires local Ollama installation

> **Implementation Note:** Originally planned with Genkit framework, we switched
> to direct API clients for better control and simpler implementation. Genkit
> dependency only remains in debug tools (`cmd/debug-schema`).

## Testing Strategy

### Unit Tests

- Configuration parsing (API key env references, model selection)
- Context pruning (max turns, token window)
- Fallback model selection (primary fails → try fallback)

### Integration Tests

- Genkit agent connects to mock MCP server
- LLM tool call execution (mock Colony responses)
- Conversation context maintained across turns
- Fallback model switching on provider errors

### E2E Tests

**Scenario 1: Basic Query**

```bash
# Setup: Colony with seeded metrics (high latency)
coral ask "why is the API slow?"
# Verify: Response mentions latency spike with citations
```

**Scenario 2: Multi-turn Conversation**

```bash
coral ask "what services are unhealthy?"
coral ask "show details for payment service" --continue
# Verify: Second query uses context from first
```

**Scenario 3: Fallback Model**

```bash
# Setup: Primary model API key invalid
coral ask "status"
# Verify: Falls back to secondary model, user warned
```

## Security Considerations

### API Key Management

**Requirements:**

- NEVER store API keys in plain text config files
- Support environment variable references: `env://VAR_NAME`
- Support system keyring: `keyring://coral/openai_api_key`
- Validate API keys on startup (detect misconfiguration early)

**Configuration validation:**

```yaml
# ~/.coral/config.yaml
ai:
    ask:
        # GOOD: Environment variable reference
        api_keys:
            openai: "env://OPENAI_API_KEY"

        # BAD: Plain text (rejected by config validator)
        api_keys:
            openai: "sk-proj-abc123..."  # ERROR: Plain text API keys not allowed
```

### Data Privacy

**Threat:** Telemetry data sent to cloud LLM providers (OpenAI, Anthropic, etc.)

**Mitigations:**

- Display warning when using cloud models (first run)
- Document data residency implications in setup guide
- Recommend local models (Ollama) for sensitive environments
- Support air-gapped mode (Ollama only, no internet required)

**Warning message (first cloud model use):**

```
⚠️  Using cloud model: openai:gpt-4o-mini

Telemetry data (metrics, logs, traces) will be sent to OpenAI's API
for processing. Do not use cloud models for sensitive/regulated data.

For local-only processing, use Ollama models:
  coral config set ask.default_model ollama:llama3.2

Continue? [y/N]
```

### Prompt Injection Prevention

**Threat:** Malicious logs/metrics containing LLM instructions

**Mitigations:**

- Structured context format (JSON-encoded data prevents interpretation as
  instructions)
- System prompt guardrails instructing LLM to ignore embedded commands
- Content sanitization for suspicious patterns (optional, may have false
  positives)

## Migration Strategy

**From RFD 014 (if partially implemented):**

1. RFD 014 is marked as abandoned (already done)
2. Any Colony-embedded LLM code is removed (Colony becomes MCP gateway only)
3. Developers install local Genkit agent via updated CLI

**Rollout:**

1. Deploy Colony MCP server updates (if needed, likely already implemented via
   `coral proxy`)
2. Release CLI with `coral ask` command
3. Users configure API keys in `~/.coral/config.yaml` under `ai.ask` section
4. First run prompts for model selection and API key setup (creates/updates
   `ai.ask` config)

**No breaking changes:**

- Existing Colony deployments unaffected (no LLM removal needed, it was never
  added)
- `coral proxy` MCP server already exists (RFD 004)

## Deferred Features

The following features are deferred to future RFDs:

### Cost Controls (Future RFD)

Token usage tracking and spend limits are a significant feature warranting
dedicated design. Scope includes:

- Per-request token limits to prevent runaway costs
- Daily spend tracking with warning/blocking thresholds
- Usage logging and cost estimation per provider
- Budget allocation per colony or user
- Cost visualization and reporting CLI commands

**Rationale for deferral:** Cost control requires careful design around storage
(usage tracking), UX (warnings vs blocking), and provider-specific cost models.
Developer API key ownership provides natural cost boundary for v1.

### Additional Future Enhancements

- **Cached insights**: Short-lived cache (1-5min TTL) for repeated questions
- **Tool calling extensions**: Custom MCP tools via plugins
- **Shared context**: Multi-user conversations on shared incidents
- **Proactive alerts**: Agent monitors Colony and suggests investigations
- **Fine-tuned models**: User-trained models for domain-specific analysis
- **Daemon mode**: Long-running agent process for local models (Ollama)

## Appendix

### Agent Deployment Modes

**Ephemeral Mode:**

- Spawn new process per `coral ask` invocation
- ✅ Simple (no daemon management)
- ❌ Slow (model loading overhead each time)
- Use case: Infrequent queries, simple deployments

**Daemon Mode:**

- Long-running agent process (Unix socket communication)
- ✅ Fast (model loaded once, reused)
- ✅ Maintains conversation context across CLI invocations
- ❌ Requires daemon lifecycle management
- Use case: Active debugging sessions, frequent queries

**Embedded Mode:**

- Genkit runtime embedded in CLI binary
- ✅ No separate process
- ❌ Slower CLI startup (library loading)
- Use case: Single-turn queries, minimal setup

**Recommendation:** Default to **embedded mode** for initial implementation.
Cloud API latency dominates model loading time, so daemon overhead is not
justified for v1. Daemon mode can be added later for local models (Ollama) where
model loading is expensive. Embedded mode simplifies implementation
significantly
(no socket management, no daemon lifecycle).

### Genkit Go Integration

**Dependencies:**

```go
// go.mod
require (
github.com/firebase/genkit/go v0.x.x
github.com/firebase/genkit/go /plugins/openai v0.x.x
github.com/firebase/genkit/go /plugins/anthropic v0.x.x
github.com/firebase/genkit/go /plugins/ollama v0.x.x
)
```

**Provider initialization example:**

```go
// Simplified - actual implementation in internal/agent/genkit
import (
"github.com/firebase/genkit/go/genkit"
"github.com/firebase/genkit/go/plugins/googlegenai" // or openai, ollama
)

// Initialize Genkit runtime
ctx := context.Background()
g := genkit.Init(ctx)

// Initialize provider plugin (API key from env)
googlegenai.Init(ctx, g, nil) // Uses GOOGLE_API_KEY env var

// Get model reference
model := googlegenai.Model(g, "gemini-1.5-flash")

// Generate response with Colony MCP tools
resp, err := genkit.Generate(ctx, g, genkit.GenerateRequest{
Model:   model,
Prompt:  "Why is checkout slow?",
Tools:   colonyMCPTools, // Tools from Colony MCP server
})
```

> **Note**: Genkit Go SDK API is evolving. Verify imports and patterns against
> current documentation at https://firebase.google.com/docs/genkit-go before
> implementation.

### MCP Tool Reference

Tools exposed by Colony MCP server (consumed by Genkit agent). All tools use the
`coral_` prefix for namespacing.

#### Observability Tools

| Tool                         | Description                                                                         |
|------------------------------|-------------------------------------------------------------------------------------|
| `coral_get_service_health`   | Get health status of services (healthy/degraded/unhealthy based on agent heartbeat) |
| `coral_get_service_topology` | Get service dependency graph discovered from distributed traces                     |
| `coral_query_events`         | Query operational events (deployments, restarts, crashes, alerts, config changes)   |

#### Beyla Metrics Tools (eBPF-based auto-instrumentation)

| Tool                             | Description                                                                      |
|----------------------------------|----------------------------------------------------------------------------------|
| `coral_query_beyla_http_metrics` | Query HTTP RED metrics (rate, errors, duration) with route/method/status filters |
| `coral_query_beyla_grpc_metrics` | Query gRPC method-level RED metrics with status code breakdown                   |
| `coral_query_beyla_sql_metrics`  | Query SQL operation metrics with table-level statistics                          |
| `coral_query_beyla_traces`       | Query distributed traces by service, time range, or duration threshold           |
| `coral_get_trace_by_id`          | Get specific trace with full span tree and parent-child relationships            |

#### OTLP Telemetry Tools (OpenTelemetry SDK instrumentation)

| Tool                            | Description                                                            |
|---------------------------------|------------------------------------------------------------------------|
| `coral_query_telemetry_spans`   | Query OTLP spans from instrumented applications (aggregated summaries) |
| `coral_query_telemetry_metrics` | Query OTLP metrics (custom application metrics)                        |
| `coral_query_telemetry_logs`    | Query OTLP logs with full-text search and filters                      |

#### Live Debugging Tools (Phase 3)

| Tool                         | Description                                                                            |
|------------------------------|----------------------------------------------------------------------------------------|
| `coral_start_ebpf_collector` | Start on-demand eBPF collector (cpu_profile, syscall_stats, http_latency, tcp_metrics) |
| `coral_stop_ebpf_collector`  | Stop a running eBPF collector before its duration expires                              |
| `coral_list_ebpf_collectors` | List currently active eBPF collectors with status and remaining duration               |
| `coral_exec_command`         | Execute command in application container (kubectl/docker exec semantics)               |
| `coral_shell_start`          | Start interactive debug shell in agent's environment                                   |

#### Example Tool Schemas

```json
{
    "coral_query_beyla_http_metrics": {
        "service": "string (required)",
        "time_range": "string (e.g., '1h', '30m'), default: '1h'",
        "http_route": "optional string (e.g., '/api/v1/users/:id')",
        "http_method": "optional enum: GET|POST|PUT|DELETE|PATCH",
        "status_code_range": "optional enum: 2xx|3xx|4xx|5xx"
    },
    "coral_query_beyla_traces": {
        "trace_id": "optional string (32-char hex)",
        "service": "optional string",
        "time_range": "string, default: '1h'",
        "min_duration_ms": "optional int",
        "max_traces": "optional int, default: 10"
    },
    "coral_start_ebpf_collector": {
        "collector_type": "enum: cpu_profile|syscall_stats|http_latency|tcp_metrics",
        "service": "string (required)",
        "agent_id": "optional string (for disambiguation)",
        "duration_seconds": "optional int, default: 30, max: 300",
        "config": "optional object (collector-specific settings)"
    },
    "coral_exec_command": {
        "service": "string (required)",
        "agent_id": "optional string (recommended for multi-agent scenarios)",
        "command": "array of strings (e.g., ['ls', '-la', '/app'])",
        "timeout_seconds": "optional int, default: 30",
        "working_dir": "optional string"
    }
}
```

---

## Implementation Status

**Core Capability:** ✅ Fully Implemented

The `coral ask` command is fully functional with MCP tool integration via buf
Connect RPC. The command connects to a running Colony's MCP server and enables
LLMs to access observability data, metrics, traces, and logs through tool
calling.

**What Works Now:**

- ✅ CLI command: `coral ask <question>` with all flags (`--model`, `--continue`,
  `--json`, `--stream`)
- ✅ Configuration: `ai.ask` section in `~/.coral/config.yaml` with per-colony
  overrides
- ✅ Config resolution: Full hierarchy (env vars → colony → global → defaults)
- ✅ Google AI (Gemini) integration: Direct API client with full tool calling
  support
- ✅ Colony RPC client: Connects to Colony MCP server via buf Connect RPC
- ✅ Tool calling: LLM can access all Colony MCP tools (coral_get_service_health,
  coral_query_beyla_traces, etc.)
- ✅ Conversation management: Full multi-turn conversations with context tracking
  and auto-pruning
- ✅ Conversation persistence: `--continue` flag loads previous conversation for
  follow-up questions
- ✅ JSON output: `--json` flag for structured output
- ✅ Build verification: Compiles successfully, all tests pass
- ✅ Schema fixes: Consistent schema generation across RPC and MCP paths
- ✅ Array schema support: Proper `items` field conversion for Google AI API

**Example Usage:**

```bash
# 1. Configure API key and model in ~/.coral/config.yaml
# ai:
#   provider: "google"
#   ask:
#     default_model: "gemini-1.5-flash"
#     google_api_key: "env://GOOGLE_API_KEY"

# 2. Set API key in environment
export GOOGLE_API_KEY=your-api-key-here

# 3. Start colony (required for MCP tools)
coral colony start

# 4. Ask questions about your application
coral ask "what services are currently running?"
coral ask "show me HTTP latency for the API service"
coral ask "why is checkout slow?" --model gemini-1.5-pro

# 5. Multi-turn conversations
coral ask "what's the p95 latency?"
coral ask "show me the slowest endpoints" --continue
```

**Files Implemented:**

- `internal/config/schema.go` - AskConfig structs
- `internal/config/ask_resolver.go` - Config resolution logic with hierarchy
  support
- `internal/cli/ask/ask.go` - CLI command with conversation persistence and
  output formatting
- `internal/agent/ask/agent.go` - Ask agent with Colony RPC client integration
- `internal/agent/ask/conversation.go` - Multi-turn conversation management
- `internal/llm/provider.go` - LLM provider abstraction
- `internal/llm/google.go` - Google AI (Gemini) provider implementation
- `internal/colony/mcp/server.go` - MCP server with schema generation fixes
- `internal/colony/mcp/tools_observability.go` - Tool registration with
  consistent schema generation

## Key Implementation Challenges & Fixes

During implementation, we encountered and resolved several critical issues:

### 1. Schema Generation Inconsistency (RPC vs MCP Paths)

**Problem:** Tool schemas were generated differently for RPC API vs MCP stdio
paths, causing empty schemas to be sent to clients.

- RPC path (`getToolSchemas()`): Used default `jsonschema.Reflector{}` with
  `$schema` and `$id` fields
- MCP path (`generateInputSchema()`): Used `DoNotReference: true` and removed
  `$schema`/`$id`

**Fix:** Unified both paths to use `generateInputSchema()`, ensuring consistent
schema generation across all transports.

**Files:** `internal/colony/mcp/server.go:214-265`

### 2. Array Schema Conversion for Google AI

**Problem:** Google AI API rejected tool schemas with array parameters,
reporting:

```
properties[command].items: missing field
```

The `convertJSONSchemaToGemini()` function wasn't converting the `items` field
for array types, which Google AI requires.

**Fix:** Added `items` field support in schema converter:

```go
// Items (for arrays). Google AI requires this field for array types.
if items, ok := jsonSchema["items"].(map[string]interface{}); ok {
schema.Items = convertJSONSchemaToGemini(items)
}
```

**Files:** `internal/llm/google.go:251-255`

**Tests:** `internal/llm/google_test.go` - Added comprehensive tests for
array schema conversion

### 3. MCP Client Initialization

**Problem:** MCP client wasn't being initialized before use, causing "client not
initialized" errors.

**Fix:** Added explicit `Initialize()` call with protocol handshake after client
creation.

**Files:** `internal/agent/ask/agent.go:138-162`

## Future Enhancements

The core `coral ask` functionality is complete. The following features are
deferred to future work:

### Multi-Provider Support (High Priority)

**Current:** Only Google AI (Gemini) is supported
**Planned:** Add OpenAI and Anthropic providers with fallback support

- Implement OpenAI provider (`internal/llm/openai.go`)
- Implement Anthropic provider (`internal/llm/anthropic.go`)
- Add provider fallback logic (try primary, fall back on errors)
- Update configuration schema to support multiple API keys
- Add Ollama provider for local/offline use

**Rationale:** Different models excel at different tasks. GPT-4o is strong for
complex reasoning, Claude for code analysis, and Gemini for cost-effectiveness.
Provider fallbacks improve reliability.

### Enhanced UX (Future RFD)

- Progressive streaming output with syntax highlighting and live rendering
- Colored terminal output with better formatting
- Progress indicators for long-running LLM calls
- Better error messages with troubleshooting hints
- Rich terminal UI with conversation history browser

### Testing (Future Work)

- Integration tests: Genkit agent ↔ Colony MCP with mocked tools
- E2E tests: `coral ask` against seeded Colony data
- Performance tests: Latency and token usage benchmarks

### Production Features (Future RFD - Cost Controls)

**Cost Controls:**

- Token usage tracking per query and per day
- Daily/monthly spend limits with configurable thresholds
- Budget warnings and blocking thresholds
- Cost estimation before execution
- Usage reporting CLI commands

**Advanced Agent Features:**

- Model fallback implementation (try primary, fall back to secondary on errors)
- Response caching with short TTL (1-5min) for repeated questions
- Daemon mode for local models (Ollama) to amortize model loading
- Multi-agent conversations with shared context

**Monitoring & Observability:**

- Query logging and audit trail (who asked what, when)
- Performance metrics (latency, token usage, tool calls)
- Error rate tracking and alerting
- Usage analytics dashboard

## Notes

**Design Philosophy:**

- **Developer-centric**: Flexibility and control over model choice
- **Colony stays lightweight**: No LLM runtime, simpler operations
- **Cost ownership**: User's API keys = user owns costs (no shared budget)
- **Offline-capable**: Ollama support for air-gapped environments

**Relationship to other components:**

- **RFD 003 (Reef)**: For cross-colony analysis, use `coral reef` (server-side
  LLM)
- **RFD 004 (MCP server)**: Colony already exposes MCP tools via `coral proxy`
- **RFD 014 (abandoned)**: This RFD replaces the Colony-embedded approach

**When to use `coral ask` vs `coral reef`:**

- **`coral ask`**: Quick debugging, single colony, developer iteration, personal
  investigations
- **`coral reef`**: Cross-environment analysis, formal RCA, enterprise
  consistency, historical patterns
