---
rfd: "030"
title: "Coral Ask - Local Genkit Integration"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: ["002", "004"]
database_migrations: []
areas: ["ai", "cli", "mcp"]
---

# RFD 030 - Coral Ask: Local Genkit Integration

**Status:** 🚧 Draft

## Summary

Implement `coral ask` CLI command using local Genkit-powered LLM agent that
connects to Colony as an MCP client. The LLM runs on the developer's machine (or
cloud via API keys), while Colony provides a stateless MCP server exposing data
access tools. This design offloads LLM compute from Colony, enables flexible
model choice, and maintains cost control at the developer level.

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

Implement `coral ask` as a CLI command that spawns or connects to a local Genkit
agent process. The agent loads the developer's chosen LLM model (local via
Ollama or cloud via API keys) and connects to the current Colony as an MCP
client. Colony provides MCP tools for data access (`coral_query_beyla_traces`,
`coral_get_service_topology`, etc.), and the LLM performs reasoning on the
developer's machine.

**Key Design Decisions:**

- **Local Genkit agent**: Runs on developer machine, not in Colony server
    - Supports both local models (Ollama) and cloud APIs (OpenAI, Anthropic,
      Google)
    - Developer owns compute costs and chooses model quality/cost trade-offs

- **Colony MCP client**: Agent connects to Colony's MCP server (already
  implemented via `coral proxy`)
    - Colony is stateless gateway providing data access tools
    - No LLM inference in Colony (keeps it lightweight)

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
│          ↓                 │        │  MCP Server                   │
│ ┌────────────────────────┐ │        │  ┌───────────────────────────┐│
│ │ Genkit Agent           │ │        │  │ Tools:                    ││
│ │                        │ │  MCP   │  │ - coral_get_service_health││
│ │ LLM (local/cloud):     │◄┼────────┼─►│ - coral_get_service_topol.││
│ │ - GPT-4 (OpenAI API)   │ │ tools  │  │ - coral_query_beyla_*     ││
│ │ - Claude (Anthropic)   │ │        │  │ - coral_query_telemetry_* ││
│ │ - Llama (Ollama local) │ │        │  │ - coral_start_ebpf_coll.  ││
│ │                        │ │        │  │ - coral_exec_command      ││
│ │ Context:               │ │        │  └───────────────────────────┘│
│ │ - Conversation history │ │        │                               │
│ │ - Colony MCP tools     │ │        │  WireGuard Mesh               │
│ └────────────────────────┘ │        │  (via coral proxy)            │
│                            │        │                               │
│ Config (local):            │        └───────────────────────────────┘
│ ~/.coral/config.yaml       │
│   ai.ask:                  │
│   - API keys (env refs)    │
│   - Model preferences      │
└────────────────────────────┘
```

### Component Changes

1. **CLI (`internal/cli/ask`)** (new package):
    - Implement `coral ask <question>` command
    - Spawn or connect to Genkit agent process
    - Stream LLM responses to terminal (progressive output)
    - Handle conversation context (multi-turn via `--continue` flag)

2. **Genkit Agent** (`internal/agent/genkit`)** (new package):
    - Load configuration (model selection, API keys from env)
    - Initialize Genkit runtime with configured providers
    - Connect to Colony MCP server (via WireGuard mesh, discovered via context)
    - Execute LLM reasoning with MCP tool calls
    - Manage conversation context and token budgets

3. **Configuration** (`~/.coral/config.yaml` - local to developer machine):
    - Extends existing `ai` section with new `ask` subsection
    - Config path: `ai.ask` in global config
    - Support for multiple providers with fallbacks
    - Optional per-colony overrides in `colonies.<colony-id>.ask`

4. **MCP Integration** (uses existing `coral proxy` implementation):
    - Colony already exposes MCP server (RFD 004)
    - Agent connects as MCP client
    - No changes needed to Colony MCP server

**Configuration Example:**

```yaml
# ~/.coral/config.yaml (local to developer machine)
version: "1"
default_colony: "my-app-prod"

discovery:
  endpoint: "http://localhost:8080"

# AI configuration (extends existing ai section)
ai:
  provider: "anthropic"  # Existing field
  api_key_source: "env"  # Existing field

  # NEW: coral ask LLM configuration
  ask:
    # Default model (Genkit provider format)
    default_model: "openai:gpt-4o-mini"

    # Fallback models (tried in order if primary fails)
    fallback_models:
      - "anthropic:claude-3-5-sonnet-20241022"
      - "ollama:llama3.2"

    # API keys (reference environment variables - NEVER plain text)
    api_keys:
      openai: "env://OPENAI_API_KEY"
      anthropic: "env://ANTHROPIC_API_KEY"

    # Conversation settings
    conversation:
      max_turns: 10             # Conversation history limit
      context_window: 8192      # Max tokens for context
      auto_prune: true          # Prune old messages when limit reached

    # Agent deployment mode
    agent:
      mode: "embedded"          # "daemon" | "ephemeral" | "embedded"
      daemon_socket: "~/.coral/ask-agent.sock"
      idle_timeout: "10m"       # Shutdown daemon after inactivity

# Per-colony overrides (optional)
colonies:
  my-app-production-xyz:
    ask:
      default_model: "anthropic:claude-3-5-sonnet-20241022"  # Use better model for prod
```

## Implementation Plan

### Phase 1: Foundation

- [ ] Define configuration schema for `ask` section
- [ ] Implement configuration loading with env variable references
- [ ] Add Genkit Go SDK dependency to project
- [ ] Create agent package structure (`internal/agent/genkit`)

### Phase 2: Genkit Agent

- [ ] Implement Genkit runtime initialization with multi-provider support
- [ ] Add MCP client implementation (connect to Colony via discovered mesh IP)
- [ ] Implement conversation context management (history, pruning)

### Phase 3: CLI Integration

- [ ] Implement `coral ask <question>` CLI command
- [ ] Add agent lifecycle management (spawn/connect to daemon)
- [ ] Implement streaming response output to terminal
- [ ] Add `--continue` flag for multi-turn conversations
- [ ] Add `--model` flag for one-off model override

### Phase 4: UX & Resilience

- [ ] Implement graceful fallback when primary model fails
- [ ] Add progress indicators for long-running LLM calls
- [ ] Implement response caching (optional, short TTL)

### Phase 5: Testing & Documentation

- [ ] Unit tests: configuration parsing, context pruning, fallback logic
- [ ] Integration tests: Genkit agent ↔ Colony MCP (mock)
- [ ] E2E tests: `coral ask` against seeded Colony data
- [ ] Documentation: setup guide, model selection, troubleshooting

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

New `ai.ask` subsection in `~/.coral/config.yaml` (extends existing `ai` section):

- `ai.ask.default_model`: Primary model to use (Genkit provider format)
- `ai.ask.fallback_models`: Array of fallback models
- `ai.ask.api_keys`: Map of provider → env variable reference
- `ai.ask.conversation.max_turns`: Conversation history limit
- `ai.ask.agent.mode`: Agent deployment mode (`daemon`|`ephemeral`|`embedded`)
- `colonies.<colony-id>.ask`: Optional per-colony overrides for model selection

**Rationale for global config:**
- LLM runs on developer's machine (not in Colony)
- Extends existing `ai` section (already contains `provider` and `api_key_source`)
- Developer's personal preferences (model choice, API keys)
- Consistent with Coral's configuration hierarchy for user-level settings

**Configuration hierarchy (follows standard Coral precedence):**
1. **Environment variables** (highest priority) - e.g., `CORAL_ASK_MODEL`
2. **Project config** - `<project>/.coral/config.yaml` (if project-specific overrides needed)
3. **Colony overrides** - `colonies.<colony-id>.ask` (for colony-specific model selection)
4. **Global defaults** - `ai.ask` section (developer's default preferences)
5. **CLI flags** - e.g., `--model` (runtime overrides)
6. **Built-in defaults** (lowest priority)

### Genkit Provider Format

Models specified as `<provider>:<model-id>`:

- OpenAI: `openai:gpt-4o`, `openai:gpt-4o-mini`
- Anthropic: `anthropic:claude-3-5-sonnet-20241022`,
  `anthropic:claude-3-5-haiku-20241022`
- Google: `google:gemini-1.5-pro`, `google:gemini-1.5-flash`
- Ollama (local): `ollama:llama3.2`, `ollama:mistral`

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
4. First run prompts for model selection and API key setup (creates/updates `ai.ask` config)

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
model loading is expensive. Embedded mode simplifies implementation significantly
(no socket management, no daemon lifecycle).

### Genkit Go Integration

**Dependencies:**

```go
// go.mod
require (
    github.com/firebase/genkit/go v0.x.x
    github.com/firebase/genkit/go/plugins/openai v0.x.x
    github.com/firebase/genkit/go/plugins/anthropic v0.x.x
    github.com/firebase/genkit/go/plugins/ollama v0.x.x
)
```

**Provider initialization example:**

```go
// Simplified - actual implementation in internal/agent/genkit
import (
    "github.com/firebase/genkit/go/genkit"
    "github.com/firebase/genkit/go/plugins/googlegenai"  // or openai, ollama
)

// Initialize Genkit runtime
ctx := context.Background()
g := genkit.Init(ctx)

// Initialize provider plugin (API key from env)
googlegenai.Init(ctx, g, nil)  // Uses GOOGLE_API_KEY env var

// Get model reference
model := googlegenai.Model(g, "gemini-1.5-flash")

// Generate response with Colony MCP tools
resp, err := genkit.Generate(ctx, g, genkit.GenerateRequest{
    Model:   model,
    Prompt:  "Why is checkout slow?",
    Tools:   colonyMCPTools,  // Tools from Colony MCP server
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
