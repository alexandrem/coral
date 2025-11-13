---
rfd: "014"
title: "Colony LLM Integration for `coral ask`"
state: "abandoned"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "002", "010", "012", "013" ]
database_migrations: [ ]
areas: [ "ai", "cli", "storage", "observability" ]
---

# RFD 014 - Colony LLM Integration for `coral ask`

**Status:** ❌ Abandoned

> **DEPRECATION NOTICE**: This RFD has been abandoned in favor of a separated LLM architecture. The design proposed here (embedding LLM orchestration inside the colony) has been superseded by an approach where Colony acts as an MCP gateway only, with LLM functionality separated into developer-side agents (RFD 030) and Reef's server-side service (RFD 003).
>
> **Replacement**: See RFD 030 for the new `coral ask` CLI design using local Genkit integration, and updates to RFD 003 for Reef's server-side LLM service.
>
> **Rationale for abandonment**:
> - Colony should remain lightweight and focused on control plane operations
> - LLM compute is offloaded to developer machines (via `coral ask`) or Reef (via `coral reef`)
> - Colony provides MCP server interface for external LLM tools to query data
> - This separation improves cost control, flexibility, and scalability

## Summary

Embed a first-class LLM orchestration layer inside the colony so `coral ask`
queries run locally against DuckDB-backed telemetry and cached insights, using
Genkit to abstract across model providers (OpenAI, Anthropic, local models). The
feature turns the colony into the authoritative intelligence hub: questions are
grounded in current system state, responses are reproducible, and audit trails
capture every prompt, context slice, and recommendation.

## Problem

**Current behavior/limitations**

- The README envisions conversational diagnostics (“Coral: Do it”) but there is
  no implemented pipeline connecting CLI questions to LLM reasoning.
- Ad hoc scripts or external AI tools require manual data copy/paste, losing
  context and exposing sensitive telemetry outside the colony’s trust boundary.
- Without caching or grounding, repeated questions waste tokens and risk stale
  answers that drift from actual system metrics in DuckDB.
- Existing telemetry (logs, metrics, eBPF summaries) isn’t automatically
  synthesized into actionable explanations.

**Why this matters**

- Operators expect Coral to explain incidents in seconds; without on-box AI, the
  promise is unfulfilled.
- Regulated teams want AI-assisted operations while keeping data local and
  audited.
- The `ask` command is the gateway for future automation (auto-remediation,
  MCP toolchain). A robust foundation is required now to avoid bolted-on hacks
  later.

**Use cases affected**

- `coral ask "Why is checkout slow?"` returning a ranked diagnosis with attached
  evidence (graphs, telemetry slices).
- `coral ask "Summarize last hour in staging"` producing structured status
  reports saved to storage.
- MCP clients using the same interface (`coral_get_insight`) to power IDE/LLM
  workflows.

## Solution

Introduce a Genkit-powered LLM service within the colony that orchestrates data
retrieval from DuckDB, composes prompts with structured context, executes model
calls, and stores results (and embeddings) for reuse. The CLI and MCP endpoints
invoke this service; responses include citations to specific DuckDB records and
are cached with TTLs to avoid repetitive inference.

### Key Design Decisions

- **Genkit abstraction**: Use Genkit as the model router to swap providers and
  support local/offline models; configuration-driven selection.
- **Grounded prompt builder**: Queries fetch relevant context (metrics, logs,
  eBPF summaries) via SQL templates before hitting the model, ensuring answers
  cite fresh data.
- **DuckDB-backed cache**: Store question, context hash, model output, and
  metadata in DuckDB for replay, audit, and warm caches; embed vector indexes
  for
  semantic retrieval.
- **Replayable prompts**: Persist full prompt + model parameters to satisfy
  audit
  requirements and support “re-run with different model” workflows.
- **Extensible tool invocation**: LLM agent can call sub-tools (SQL, summary,
  anomaly detection) via Genkit tool interface, enabling stepwise reasoning.

### Benefits

- Delivers the product promise: fast, AI-generated explanations grounded in real
  telemetry, all within the colony boundary.
- Supports multiple deployment modes (cloud API keys, self-hosted models) with a
  single runtime.
- Enhances automation: cached insights feed into control-plane decisions,
  notifications, and Reef federation.
- Provides complete transparency and traceability for every `ask` invocation.

### Architecture Overview

```
   CLI / MCP
      │ `coral ask ...`
      ▼
┌──────────────────────────────┐
│ Colony LLM Service           │
│  ├─ Request Router           │
│  ├─ Context Builder (DuckDB) │
│  ├─ Genkit Orchestrator      │
│  └─ Cache Manager (DuckDB)   │
└────────────┬─────────────────┘
             │
    ┌────────▼──────────┐
    │ DuckDB Storage    │
    │  • metrics/events │
    │  • eBPF summaries │
    │  • ask cache      │
    └────────┬──────────┘
             │
      ┌──────▼───────┐
      │ LLM Providers│  (OpenAI, Anthropic, local)
      └──────────────┘
```

### Component Changes

1. **Colony (`internal/colony/ask` new package)**
    - Genkit pipeline definitions (prompt templates, tool registry).
    - Context builders executing parameterized DuckDB queries (per use case:
      performance, errors, feature flags).
    - Cache manager storing/retrieving responses keyed by (question, scope,
      context hash).
    - Audit log integration: record requestor, prompt, response, tokens, tools.

2. **DuckDB Storage (`docs/STORAGE.md` + schema)**
    - New tables: `ask_cache`, `ask_context_chunks`, optional `ask_embeddings`.
    - Views optimizing retrieval (e.g., last hour metrics per service).

3. **CLI (`internal/cli/ask`)**
    - Update `coral ask` to support flags (`--refresh`, `--model`, `--json`).
    - Display citations referencing specific colony datasets.

4. **MCP Server (`docs/MCP.md`)**
    - Expose `coral_get_insight` tool that mirrors `ask`.

**Configuration Example**

```yaml
# ~/.coral/config.yaml
ai:
    provider: "openai:gpt-4o-mini"   # Genkit provider id
    fallbackProviders:
        - "anthropic:claude-3-opus"
        - "local:llama-3.1-8b"
    cache:
        enabled: true
        ttl: 10m
        maxEntries: 1000
    context:
        defaultWindow: 1h
        maxTokens: 4096
    guardrails:
        maxExecuteTools: 3
        requireCitations: true
```

## Implementation Plan

### Phase 1: Foundations

- [ ] Define DuckDB schema additions (`ask_cache`, `ask_context_chunks`).
- [ ] Integrate Genkit runtime; add provider configuration handling.
- [ ] Implement context builder interfaces and initial SQL templates.

### Phase 2: LLM Service

- [ ] Build colony service module (context → genkit → response).
- [ ] Implement caching layer with TTL invalidation and manual refresh.
- [ ] Add audit logging hooks (request metadata, prompt, response).
- [ ] Provide evaluation harness to compare provider outputs.

### Phase 3: CLI/MCP Integration

- [ ] Update `coral ask` command to call the colony service via RPC.
- [ ] Add `--refresh`, `--model`, `--scope`, `--json` flags.
- [ ] Return printable summaries + structured JSON for tooling.
- [ ] Add MCP tool definition (`coral_get_insight`) with schema.

### Phase 4: Tooling & Guardrails

- [ ] Register built-in Genkit tools: `sql_query`, `metrics_summary`,
  `event_search`.
- [ ] Enforce policy (max tool invocations, token budgets).
- [ ] Support streaming responses for long outputs (CLI progress).
- [ ] Add optional human-in-the-loop approval for actions suggested by LLM.

### Phase 5: Testing & Documentation

- [ ] Unit tests: context builders, cache hits/misses, config parsing.
- [ ] Integration tests: deterministic prompts with fixture DuckDB data.
- [ ] E2E tests: `coral ask` against seeded scenarios (performance regression,
  failed deploy).
- [ ] Documentation update (README, USAGE, STORAGE, security notes).

## API Changes

### Protobuf (`proto/coral/ask/v1/ask.proto`)

```protobuf
syntax = "proto3";
package coral.ask.v1;

import "google/protobuf/duration.proto";
import "google/protobuf/timestamp.proto";

message AskRequest {
    string question = 1;
    string scope = 2;              // service/environment filter
    bool force_refresh = 3;
    string model_override = 4;
    bool json_output = 5;
}

message AskResponse {
    string answer = 1;
    repeated Citation citations = 2;
    CacheStatus cache = 3;
    repeated SuggestedAction actions = 4;
}

message Citation {
    string dataset = 1;            // e.g., "metrics_http_latency"
    string reference = 2;          // SQL query or row identifier
}

message CacheStatus {
    bool hit = 1;
    google.protobuf.Timestamp generated_at = 2;
    google.protobuf.Duration ttl = 3;
}

message SuggestedAction {
    string description = 1;
    string command = 2;
    bool requires_confirmation = 3;
}

service AskService {
    rpc Ask(AskRequest) returns (AskResponse);
}
```

### Configuration Changes

- New `ai` section in global config (provider, caching, guardrails).
- Optional service-level overrides (`.coral/colonies/<id>.yaml`).
- Environment variables to inject API keys (`CORAL_AI_OPENAI_KEY`, etc.).

## Testing Strategy

### Unit Tests

- Context selection logic (windowing, scope filtering).
- Cache behavior (hit/miss, invalidation on refresh).
- Prompt template rendering with edge cases (missing telemetry, empty datasets).

### Integration Tests

- Mock Genkit provider returning deterministic output for fixture data.
- Verify citations correspond to actual DuckDB rows/queries.
- Ensure JSON output is schema-compliant and round-trippable.

### E2E Tests

- CLI scenario: degrade service metrics → `coral ask` → verify summary
  highlights
  anomaly and suggests rollback.
- MCP scenario: `coral_get_insight` request from simulated IDE, ensuring same
  cache/shared context.
- Failure cases: provider unavailable (fallback works), no data available (
  graceful
  response).

## Security Considerations

### Data Residency & Privacy

**Default**: Local execution preferred

- Colony stores data locally in DuckDB (user's infrastructure)
- LLM calls go to user-specified provider (their API keys, their account)
- No telemetry sent to Coral developers

**Cloud provider warnings**: Display warning when using cloud LLM providers,
informing users that telemetry data will be sent to the provider's API. Provide
guidance on switching to local models (Ollama).

**Table allowlist**: Prevent secret leakage by maintaining allowlist of safe
tables (`metrics`, `events`, `deployments`, `topology`, `health_checks`,
`error_logs`) and blocklist of sensitive tables (`api_keys`, `secrets`,
`credentials`, `encryption_keys`). Parse SQL queries to verify only allowed
tables are accessed.

### Prompt Injection Prevention

**Threat**: Malicious logs/metrics containing LLM instructions (e.g., "IGNORE
PREVIOUS INSTRUCTIONS").

**Mitigation**:

- Content sanitization: Detect and redact suspicious patterns (
  `"ignore previous"`, `"new instructions"`, `"developer mode"`, etc.)
- Structured context format: Use JSON encoding for context data to prevent
  interpretation as instructions
- System prompt guardrails: Instruct LLM to ignore embedded instructions and
  only use provided context data

### Cost Control & Rate Limiting

**Threat**: Unbounded API costs from excessive queries.

**Mitigation**:

- Per-user rate limits (configurable requests per minute/day)
- Token budget enforcement (estimate cost before sending, block if exceeds
  threshold)
- Daily spend tracking with warning and blocking thresholds

**Configuration**:

```yaml
ai:
    cost_controls:
        max_requests_per_minute: 10
        max_requests_per_day: 1000
        max_cost_per_request_usd: 0.50
        warn_at_daily_spend_usd: 10.00
        block_at_daily_spend_usd: 50.00
```

### Hallucination Prevention

**Requirement**: All LLM claims must cite actual DuckDB queries and results.

**Approach**:

- System prompt requires citation format for every claim
- Response validation ensures citations reference executed queries
- User-facing output displays evidence with query references

### Audit Logging

**Requirement**: Track all `ask` requests for compliance and debugging.

**Data captured**: User ID, question, context hash, model used, tokens consumed,
cost, response time, success/failure status.

**Schema**: See Appendix C for `ask_audit_log` table definition.

**Properties**: Append-only (no updates or deletes), indexed by user and
timestamp.

### Secrets Management

**API key storage options**:

- Environment variables (recommended): `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`
- System keyring (encrypted): `keyring://coral/anthropic_api_key`
- Config file references: `env://ANTHROPIC_API_KEY`

**Never** store API keys in plain text config files.

## Future Enhancements

- Fine-tuned small models hosted alongside the colony for offline inference.
- Automatic summarization of recurring incidents with scheduled `ask` jobs.
- Reinforcement from human feedback (score answers, improve prompts).
- Shared cache across colonies using Reef for cross-environment insights.
- Guardrail policies integrated with control plane (action approvals).

## Appendix

### A. Caching Considerations

**Current Decision**: Phase 1 defers response caching (see schema note in
Appendix B).

**Rationale for deferral**:

- Staleness risk: Cached responses may not reflect current system state
- Invalidation complexity: Determining when cache entries are outdated requires
  tracking data dependencies
- User expectations: Operators expect real-time answers during incidents

**Future caching strategy** (if implemented):

**Cache key components**:

- Question hash: Normalized question text
- Scope hash: Services and time window
- Context hash: Hash of actual data used (metrics, logs)
- Schema version: Invalidate on schema changes

**Invalidation triggers**:

- TTL expiration (config: `ai.cache.ttl`, default: 10m)
- New deployments affecting scoped services
- Metric anomalies detected (error rate spike, latency change >20%)
- Manual refresh via `--force-refresh` flag

**Cache hit behavior**:

```
$ coral ask "Why slow?"
✓ Using cached answer from 3m ago
  (metric changes since cache: CPU +2%, Latency +5ms)
  [Refresh now?]
```

**Tradeoffs**:

- ✅ Reduced cost: Avoid redundant LLM calls for identical questions
- ✅ Faster responses: Sub-second retrieval vs. 2-5s LLM latency
- ❌ Staleness: User may act on outdated analysis
- ❌ Storage overhead: ~1-10KB per cached response

**Recommendation**: Implement caching only after user feedback confirms value
exceeds staleness risk. Start with short TTLs (1-5m) and clear cache-hit
indicators.

### B. Genkit Framework Selection

**Why an LLM abstraction framework?**

Coral needs to support multiple LLM providers (cloud APIs and local models)
without implementing provider-specific integration code for each. A framework
provides:

- Unified API across providers (OpenAI, Anthropic, Google, Ollama)
- Consistent interfaces for completion, streaming, tool calling
- Provider-agnostic prompt templates
- Built-in error handling and retries

**Framework Evaluation:**

| Framework              | Language  | Provider Support                  | Maturity                   | Trade-offs                                                                             |
|------------------------|-----------|-----------------------------------|----------------------------|----------------------------------------------------------------------------------------|
| **Genkit**             | Go (+ TS) | OpenAI, Anthropic, Google, Ollama | Production (Google-backed) | ➕ Official Google support<br>➕ Active development<br>➕ Good Go SDK<br>➖ Relatively new |
| **LangChain Go**       | Go        | OpenAI, Anthropic, Ollama         | Community                  | ➕ Well-known brand<br>➖ Go port less mature than Python<br>➖ Heavy dependency tree     |
| **Custom Abstraction** | Go        | Any                               | N/A                        | ➕ Full control<br>➖ Maintenance burden<br>➖ Reinventing wheel                          |
| **Direct SDKs**        | Go        | Per-provider                      | Varies                     | ➕ No abstraction overhead<br>➖ Provider-specific code<br>➖ 5+ SDKs to maintain         |

**Decision: Genkit**

Rationale:

- **Production-ready**: Backed by Google, used in Firebase and GCP
- **Go-native**: Pure Go implementation (no Node.js runtime required)
- **Provider breadth**: Supports cloud (OpenAI, Anthropic) and local (Ollama)
  out of box
- **Feature completeness**: Streaming, tool calling, prompt management included
- **Active maintenance**: Regular updates, responsive community

**Genkit Go Specifics:**

- Pure Go library (imports as standard Go module)
- No external runtime dependencies
- Minimal binary size impact (~2-3 MB)
- Well-documented: https://firebase.google.com/docs/genkit-go

**Limitations:**

- Relatively new (released 2024) - less battle-tested than LangChain
- Google ecosystem focus - some integrations favor GCP
- Framework lock-in - migration to alternatives requires code changes

**Mitigation:**

- Abstract Genkit behind internal interfaces (`internal/colony/llm/provider.go`)
- Keep provider logic isolated for future swappability
- Monitor Genkit development and community health

### C. DuckDB Schema Additions (Proposed)

```sql
CREATE TABLE ask_cache
(
    id            UUID PRIMARY KEY,
    question      TEXT      NOT NULL,
    scope         TEXT,
    context_hash  TEXT      NOT NULL,
    answer        TEXT      NOT NULL,
    model         TEXT      NOT NULL,
    generated_at  TIMESTAMP NOT NULL,
    ttl_seconds   INTEGER,
    cache_hit     BOOLEAN DEFAULT FALSE,
    requestor     TEXT,
    tokens_input  INTEGER,
    tokens_output INTEGER
);

CREATE TABLE ask_context_chunks
(
    cache_id   UUID REFERENCES ask_cache (id),
    dataset    TEXT,
    query      TEXT,
    data       JSON,
    created_at TIMESTAMP DEFAULT now()
);

CREATE TABLE ask_audit_log
(
    id                 UUID PRIMARY KEY,
    timestamp          TIMESTAMPTZ NOT NULL,
    user_id            VARCHAR     NOT NULL,
    session_id         VARCHAR,
    question           TEXT        NOT NULL,
    context_hash       TEXT        NOT NULL,
    context_size_bytes INTEGER,
    model              VARCHAR     NOT NULL,
    provider           VARCHAR     NOT NULL,
    tokens_input       INTEGER,
    tokens_output      INTEGER,
    cost_usd           DECIMAL(10, 4),
    response_time_ms   INTEGER,
    success            BOOLEAN     NOT NULL,
    error_message      TEXT,
    ip_address         INET,
    user_agent         VARCHAR
);

CREATE INDEX idx_audit_user_time ON ask_audit_log (user_id, timestamp DESC);
CREATE INDEX idx_audit_session ON ask_audit_log (session_id);
```

**Note**: Caching implementation is deferred to future RFD (see Appendix A).
Phase 1 will not include response caching to avoid staleness risks and
complexity. The cache schema above is included for completeness but may be
revised based on real-world usage patterns.

---

**Implementation Details**: For detailed implementation guidance including
context builders, prompt engineering, security implementations, and air-gap
support, see `docs/LLM_IMPLEMENTATION_GUIDE.md`.
