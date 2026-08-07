---
rfd: "101"
title: "Investigation Gateway — Correlated External Tooling"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "004", "051", "094", "100" ]
database_migrations: []
areas: [ "mcp", "ai", "cli", "ux" ]
---

# RFD 101 - Investigation Gateway: Correlated External Tooling

**Status:** 🚧 Draft

<!--
Supersedes the two earlier RFD 095 drafts:
  - 095-mcp-client-integrations.md     (raw tool passthrough approach)
  - 095-semantic-domain-wrappers.md    (domain wrapper approach, external-only)
-->

## Summary

Coral connects outward as an MCP client to external developer tools (GitHub,
Sentry, Linear, PagerDuty, and any user-configured server) and exposes the
combined result as a small set of **investigation tools** — not as raw
passthrough tools. Each investigation tool joins external data with native Coral
telemetry internally before returning a single correlated response to the LLM.
The correlation work happens inside Coral; the LLM focuses on reasoning about
findings, not on orchestrating API calls. A static `service_mappings`
configuration block resolves Coral service names (e.g. `"payments"`) to
provider-specific identifiers (GitHub repo slug, Sentry project, Linear team
prefix) so the gateway can route calls correctly without guessing or asking the
LLM to infer external slugs.

## Problem

**Current behavior/limitations:**

- `coral ask` and `coral terminal` can only call tools backed by the colony's
  own MCP server: traces, metrics, eBPF probes, service topology. Engineering
  context — which commit triggered a regression, whether Sentry has already
  grouped the errors, who is on-call — requires manual context-switching out of
  the session.
- Exposing raw external MCP tools (GitHub, Sentry, etc.) directly to the LLM,
  as a flat prefixed list, makes the tool count explode (100+ tools with 3-4
  servers) and degrades reasoning accuracy.
- Even with a manageable tool count, raw tool exposure pushes the correlation
  work onto the LLM: fetch traces → separately fetch matching commits → check
  Sentry stack traces. Multiple round-trips, multiple LLM inference steps, and
  the LLM is acting as a join engine rather than an investigator.
- External context and native telemetry have never been in the same response.
  The root cause of a regression lives at the intersection of both; current
  tooling forces a manual join.

**Why this matters:**

The highest-value use of an LLM in incident response is reasoning about a
correlated picture — "the p99 spike started 2 minutes after commit a3f91b2
landed and Sentry has grouped 847 matching errors since then." That sentence
requires three data sources to be joined. If each join is a separate tool call,
the LLM is doing data engineering, not investigation. Coral owns the telemetry
side and can drive the external calls; it is the natural place to do the join.

**Use cases affected:**

- **Incident correlation**: "What changed around the time of this spike?" —
  today requires leaving Coral to check GitHub.
- **Error triage**: Sentry may have already grouped and analysed the errors.
  Today the LLM re-derives what Sentry already knows.
- **On-call handoff**: An engineer arrives at an incident with fragmented
  context across PagerDuty, Sentry, and Coral. There is no single interface
  that holds all three.
- **Deploy safety**: "Is it safe to deploy now?" requires checking service
  health (colony) and CI/review status (GitHub) in the same turn.

## Solution

Introduce an **Investigation Gateway** inside the Coral CLI agent. The gateway
manages outbound MCP client connections to configured external servers and
exposes a fixed set of investigation tools to the LLM. Each tool is responsible
for calling the relevant external MCP tools *and* the relevant colony tools,
then normalizing and joining the results before returning a single structured
response.

**Key Design Decisions:**

- **No raw external tool exposure.** External MCP tools are never added
  directly to the LLM's tool list. The gateway is the only consumer of external
  tool results.
- **Correlation at the gateway layer.** Each investigation tool drives parallel
  calls to external providers and the colony, then joins the results. The LLM
  receives one coherent response, not fragmented raw data.
- **Providers are infrastructure; investigation tools are the contract.**
  Configuring a GitHub provider does not automatically add GitHub tools to the
  LLM. Providers must be mapped to investigation tools (via `domains`
  configuration) to surface in a session.
- **Escape hatch for uncovered providers.** A `coral_integration_query` tool
  provides raw access to a specific provider's tools when no investigation
  wrapper covers the needed capability yet. This is explicitly a stopgap —
  each use case that proves common should be promoted to a proper investigation
  tool.
- **Credentials stay local.** External MCP server processes run as subprocesses
  of the CLI process with credentials resolved from the user's shell environment
  at connection time. Nothing passes through Coral infrastructure.
- **Dependency on RFD 100.** The investigation tools exposed here complement
  the CLI-native dispatch introduced in RFD 100: Coral's own native tool count
  is reduced by RFD 100; external tool count is controlled by this RFD. Together
  they deliver the full investigation gateway.

**Architecture Overview:**

```
LLM calls coral_get_activity(service="payments", window="1h")
    │
    └── Investigation Gateway (internal/cli/integrations/)
        │  1. Resolve providers mapped to "activity" domain
        │  2. Resolve service identifiers via service_mappings config:
        │       payments → github:"org/payments-api", sentry:"pay-stack"
        │  3. Parallel dispatch
        │
        ├── Colony (native)    coral query traces --service payments --since 1h
        │                      coral query metrics --service payments --since 1h
        │
        ├── GitHub MCP client  list_commits(repo="org/payments-api", since=1h)
        │                      get_pull_request(...)
        │
        └── Sentry MCP client  list_issues(project="pay-stack", since=1h)
        │
        └── Normalizer → joins, sorts chronologically, computes deltas
            └── Returns: unified ActivityResult{events[], trace_delta, error_rate_delta}
```

```
External clients (Claude Desktop) — unchanged:
    Claude Desktop → MCP stdio → coral colony mcp proxy → Colony MCP Server
```

### Component Changes

1. **Integration Gateway** (`internal/cli/integrations/`):

    - `ProviderManager`: manages outbound MCP client connections (stdio and HTTP
      transports), one connection per configured provider per session. Reuses
      `mark3labs/mcp-go` client, the same library used for the colony MCP
      server.
    - `Orchestrator`: for each investigation tool call, dispatches parallel
      requests to the relevant providers and the colony, collects results,
      runs the appropriate normalizer, and returns the joined response.
      Two timeout tiers apply: a per-provider `timeout_seconds` (from
      `ProviderConfig`, default 30s) cancels an individual slow provider
      early, and a gateway-level `timeout_seconds` (from
      `Integrations`, default 10s) is a hard wall-clock ceiling on the entire
      parallel fan-out. When the gateway deadline fires, any still-running
      providers are cancelled and their results are replaced with
      `ProviderWarning{status: "timeout"}` entries. The orchestrator always
      returns within the gateway deadline with whatever data arrived in time.
      A result with zero events but no warnings means the sources are healthy
      and genuinely quiet; a result with warnings means the LLM must treat
      the data as potentially incomplete and say so.
    - `Adapters`: provider-specific normalizers that map raw MCP tool responses
      (GitHub commit JSON, Sentry issue JSON) to shared domain types
      (`ActivityEvent`, `TicketItem`, `OnCallStatus`).

2. **Investigation tools** (`internal/cli/integrations/tools_*.go`):

    - `coral_get_activity`: unified timeline of deployments, alerts, and
      anomalies. Sources: GitHub (commits, PRs) + Sentry (issues) + Colony
      (trace/metric deltas). Returns events sorted chronologically, with Coral
      telemetry deltas annotated at the relevant timestamps.
    - `coral_search_tickets`: search across project management tools for context
      relevant to a service or error. Sources: Linear, GitHub Issues. Returns
      ranked matching tickets with summaries.
    - `coral_get_oncall`: current on-call rotation and active incident timeline
      for a service. Sources: PagerDuty + Colony (correlated traces for the
      incident window).
    - `coral_integration_query` (escape hatch): direct call to a named
      provider's raw tool. Not shown in the LLM's tool list unless explicitly
      enabled; intended for development and for providers without a domain
      wrapper.

3. **Configuration schema** (`internal/config/schema.go`):

    - `Integrations.Providers []ProviderConfig`: connection parameters for each
      external server (alias, transport, command/url, env, timeout).
    - `Integrations.Domains`: maps domains (`activity`, `tickets`, `oncall`) to
      provider aliases. Controls which providers contribute to which
      investigation tools.
    - `Integrations.ServiceMappings`: maps each Coral service name to a set of
      provider-specific identifiers (repo slug, project key, team prefix). The
      gateway's `ServiceResolver` looks up these identifiers before dispatching
      to each provider. If a mapping is absent for a required provider, the
      resolver produces a `ProviderWarning{status: "missing_mapping", message:
      "service 'payments' has no GitHub mapping — add it under
      integrations.service_mappings.payments.github in ~/.coral/config.yaml"}`
      and skips that provider. The warning surfaces in the result's `warnings`
      field so the LLM can relay the gap to the user rather than silently
      returning an incomplete timeline.
    - `env://VAR` syntax for credential references — resolved at connection time
      from the process environment, never written to disk as literal values.

4. **Agent integration** (`internal/cli/ask/agent.go`):

    - At session init, construct `ProviderManager` and connect to enabled
      providers. Inject investigation tools into the agent's tool set alongside
      the CLI-dispatch tools from RFD 100.
    - Investigation tool calls are routed to the `Orchestrator`. Colony calls
      within the orchestrator use the same CLI dispatch path as RFD 100.
    - On session teardown, close all provider connections.

5. **CLI management commands** (`internal/cli/integration/`):

    - `coral integration list`: show configured providers and their connection
      status and the domain mapping.
    - `coral integration test <alias>`: connect to the named provider, list its
      available tools, and disconnect. Used to validate credentials and
      configuration before a session.
    - `coral integration add`: interactive wizard with presets for GitHub,
      Sentry, Linear, and PagerDuty.

6. **`coral terminal` sidebar** (RFD 094):

    - Optional section showing provider connection status (connected / failed /
      disabled) when integrations are configured. Updated once at session start.

## Implementation Plan

### Phase 1: Provider transport and configuration

- [ ] Define `ProviderConfig` struct in `internal/config/schema.go`: `alias`,
      `transport`, `command`, `args`, `env`, `url`, `auth`, `enabled`,
      `timeout_seconds`.
- [ ] Define `Integrations.Domains` config: map of domain name → `[]alias`.
- [ ] Define `Integrations.ServiceMappings` config: map of Coral service name →
      map of provider alias → external identifier string (repo slug, project
      key, etc.).
- [ ] Implement `ServiceResolver` in `internal/cli/integrations/resolver.go`:
      given a service name and provider alias, return the configured external
      identifier; if the mapping is absent, return a
      `ProviderWarning{status: "missing_mapping"}` with a message pointing to
      the exact config key the user needs to add. The resolver never errors —
      it always returns either an identifier or a warning, so the orchestrator
      can include it in the result and continue.
- [ ] `coral integration list` output includes a mapping coverage summary: which
      services have mappings and which providers are covered per service.
- [ ] Implement `env://VAR` resolution in config loader; warn on literal
      credential values.
- [ ] Validate: unique aliases, `transport: stdio` requires `command`,
      `transport: http` requires `url`, alias must not start with `coral`.
- [ ] Implement `ProviderManager` in `internal/cli/integrations/provider.go`:
      connect stdio (subprocess + pipes) and HTTP (SSE / Streamable HTTP)
      transports using `mark3labs/mcp-go` client.
- [ ] Session lifecycle: `Connect()` on session start, `Close()` on teardown;
      failed connections log a warning and are skipped.
- [ ] Implement `coral integration list`, `coral integration test <alias>`, and
      `coral integration add` (preset wizard for GitHub, Sentry, Linear,
      PagerDuty).
- [ ] Unit tests: provider connect/disconnect, `env://` resolution, config
      validation.

### Phase 2: Normalization types and adapters

- [ ] Define shared domain types in `internal/cli/integrations/types.go`:
      `ActivityEvent`, `TicketItem`, `OnCallStatus`.
- [ ] Implement GitHub adapter: map `list_commits` and `get_pull_request`
      responses to `ActivityEvent{type: "commit" | "deploy"}`.
- [ ] Implement Sentry adapter: map `list_issues` response to
      `ActivityEvent{type: "alert"}`.
- [ ] Implement Linear adapter: map `list_issues` to `TicketItem`.
- [ ] Implement PagerDuty adapter: map incidents and schedules to
      `OnCallStatus`.
- [ ] `Orchestrator`: parallel provider dispatch, result collection, chronological
      merge.
- [ ] Each provider call in the orchestrator is wrapped with `timeout_seconds`
      from its `ProviderConfig`; timeouts and non-2xx/error responses produce a
      `ProviderWarning` entry rather than aborting the result.
- [ ] Implement gateway-level `timeout_seconds` (default 10s) as a
      `context.WithTimeout` wrapping the entire parallel fan-out. When the
      deadline fires, cancel all in-flight provider contexts and collect
      whichever results have already arrived; append `ProviderWarning{status:
      "timeout"}` for each provider that did not complete. The orchestrator
      must always return within this deadline.
- [ ] Colony calls (traces, metrics) follow the same pattern: a failed colony
      call produces a `ProviderWarning{provider: "colony", ...}` rather than
      returning an error to the LLM.
- [ ] Unit tests: adapter mapping correctness; merge ordering; partial failure
      (one provider times out — result contains remaining events plus warning);
      403 from GitHub produces `status: "error"` warning with message quoting
      the HTTP status; gateway deadline fires before all providers complete —
      result is returned immediately with arrived data and timeout warnings for
      laggards; verify wall-clock duration of the call does not exceed
      `gateway_timeout_seconds + small epsilon`.

### Phase 3: Investigation tools and agent integration

- [ ] Implement `coral_get_activity`: dispatch to colony (traces, metrics) and
      activity-domain providers in parallel; annotate trace/metric deltas at
      event timestamps; return `ActivityResult`.
- [ ] Adapters populate `Raw` only when the `verbose` flag is set; in default
      mode `Raw` is nil and omitted from serialization. The adapter extraction
      logic (key fields) runs unconditionally in both modes.
- [ ] Implement `coral_search_tickets`: dispatch to ticket-domain providers,
      rank and summarise results.
- [ ] Implement `coral_get_oncall`: dispatch to on-call-domain providers and
      colony (traces for the incident window).
- [ ] Implement `coral_integration_query` escape hatch (disabled by default).
- [ ] Wire investigation tools into `agent.go` session init alongside RFD 100
      CLI-dispatch tools.
- [ ] Update agent system prompt to describe when to use investigation tools vs
      CLI-dispatch tools.
- [ ] Integration tests: full orchestration with mock providers; correct tool
      routing; mid-session provider disconnect.

### Phase 4: Testing and documentation

- [ ] E2E test: `coral ask` session with mock GitHub and Sentry servers; LLM
      calls `coral_get_activity`; response includes both external events and
      colony trace delta.
- [ ] E2E test: session with no integrations configured — behaviour identical
      to pre-RFD baseline.
- [ ] Add `coral terminal` sidebar provider status section.
- [ ] Update `docs/MCP.md` with integration gateway architecture.
- [ ] Update `docs/CLI_REFERENCE.md` with `coral integration` commands.
- [ ] Update `docs/AGENT.md` with investigation tools reference and
      `coral_integration_query` escape hatch.
- [ ] Update `docs/CONFIG.md` with `integrations` config block.

## API Changes

### Configuration

```yaml
# ~/.coral/config.yaml

integrations:
  providers:
    - alias: github
      transport: stdio
      command: npx
      args: ["-y", "@modelcontextprotocol/server-github@0.6.2"]
      env:
        GITHUB_PERSONAL_ACCESS_TOKEN: "env://GITHUB_TOKEN"

    - alias: sentry
      transport: stdio
      command: uvx
      args: ["mcp-server-sentry==0.5.1"]
      env:
        SENTRY_AUTH_TOKEN: "env://SENTRY_TOKEN"
        SENTRY_ORGANIZATION: my-org

    - alias: linear
      transport: http
      url: "https://mcp.linear.app/sse"
      auth:
        type: bearer
        token: "env://LINEAR_TOKEN"

    - alias: pagerduty
      transport: stdio
      command: npx
      args: ["-y", "@modelcontextprotocol/server-pagerduty@0.3.0"]
      env:
        PAGERDUTY_API_KEY: "env://PAGERDUTY_KEY"

  # Hard wall-clock ceiling on the entire parallel provider fan-out.
  # The orchestrator returns with whatever arrived in time; slow providers
  # produce a ProviderWarning rather than blocking the result.
  # Per-provider timeout_seconds (on each provider entry) is a tighter
  # knob for cancelling individual slow servers before this deadline fires.
  timeout_seconds: 10

  domains:
    activity: [github, sentry]   # feeds coral_get_activity
    tickets:  [github, linear]   # feeds coral_search_tickets
    oncall:   [pagerduty]        # feeds coral_get_oncall

  # Maps Coral service names to provider-specific external identifiers.
  # Without a mapping entry, the gateway skips that provider for the service
  # and logs a warning rather than guessing the external slug.
  service_mappings:
    payments:
      github:    "org/payments-api"
      sentry:    "pay-stack"
      linear:    "PAY"
      pagerduty: "payments-prod"
    api-gateway:
      github:    "org/api-gateway"
      sentry:    "api-gw"
      pagerduty: "api-gateway-prod"
    worker:
      github:    "org/background-worker"
      sentry:    "worker"
```

### Investigation tools (LLM-facing)

```
coral_get_activity(service: string, window: string, verbose?: bool) → ActivityResult
  Sources: colony traces+metrics + activity-domain providers
  verbose:  false (default) — each event contains only normalized fields
            true            — each event also includes Raw (full provider output)
  Returns: {
    events:           [{source, type, timestamp, summary, actor, raw?}],
    trace_delta:      {p99_before_ms, p99_after_ms, inflection_at},
    error_rate_delta: {before, after, inflection_at},
    warnings:         [{provider, status, message}]
  }

coral_search_tickets(query: string, service?: string, verbose?: bool) → TicketResult
  Sources: ticket-domain providers
  Returns: {
    tickets:  [{source, id, title, status, url, summary, raw?}],
    warnings: [{provider, status, message}]
  }

coral_get_oncall(service: string, verbose?: bool) → OnCallResult
  Sources: oncall-domain providers + colony traces for active incident window
  Returns: {
    oncall:            [{name, since}],
    active_incidents:  [{id, title, severity, started_at, correlated_traces, raw?}],
    warnings:          [{provider, status, message}]
  }

coral_integration_query(alias: string, tool: string, args: object) → raw result
  Escape hatch — disabled by default, enabled per-session via --enable-raw-integration
```

### CLI commands

```bash
# List providers and domain mapping
coral integration list

# Example output:
Providers:
  github     stdio   connected   activity, tickets
  sentry     stdio   connected   activity
  linear     http    connected   tickets
  pagerduty  stdio   failed      oncall     (PAGERDUTY_KEY not set)

# Test a provider (connect, list tools, disconnect)
coral integration test github

# Add a provider via wizard
coral integration add
coral integration add --preset github --token "env://GITHUB_TOKEN"
```

### Shared domain types

```go
// ProviderWarning records a failed or degraded provider call within an
// investigation tool result. Its presence signals to the LLM that the
// corresponding data may be missing and the result should not be treated
// as a complete picture.
type ProviderWarning struct {
    Provider string // provider alias, e.g. "sentry", "github", "colony"
    Status   string // "timeout", "error", "missing_mapping", "partial"
    Message  string // human-readable description, e.g. "Sentry MCP server
                    // did not respond within 30s — activity timeline may be
                    // incomplete" or "GitHub returned 403 — check GITHUB_TOKEN"
}

// ActivityEvent is the normalized representation of a deployment, commit, or alert.
// Raw is only populated when the investigation tool is called with verbose: true.
// In default mode Raw is nil, keeping response size and LLM token cost low.
type ActivityEvent struct {
    Source    string         // "github", "sentry"
    Type      string         // "commit", "deploy", "alert"
    Timestamp time.Time
    Summary   string
    Actor     string
    Raw       map[string]any `json:"raw,omitempty"` // full provider output, verbose only
}

// TicketItem is the normalized representation of an issue or ticket.
type TicketItem struct {
    Source  string // "github", "linear"
    ID      string
    Title   string
    Status  string
    URL     string
    Summary string
}

// OnCallStatus is the normalized on-call rotation and incident summary.
type OnCallStatus struct {
    Oncall           []OncallEntry
    ActiveIncidents  []Incident
}
```

## Security Considerations

- **No raw tool exposure to LLM.** External provider tools are called only by
  the gateway orchestrator, never surfaced to the LLM directly. This limits the
  prompt injection surface from third-party tool responses.
- **Credentials stay in process memory.** `env://VAR` references are resolved
  at connection time and exist only in the subprocess environment or HTTP
  headers. They are never serialised, logged, or sent to the colony.
- **Escape hatch is off by default.** `coral_integration_query` is not in the
  LLM's tool list unless explicitly enabled, preventing inadvertent use of raw
  external tools.
- **Subprocess isolation.** stdio provider processes are spawned via
  `os/exec` with explicit `env` overrides only, killed via SIGTERM on session
  end (SIGKILL after 5s grace).
- **External tool write operations.** Providers may expose write tools (create
  issue, comment on PR). The LLM may call these via `coral_integration_query`.
  Users should scope credentials to minimum required permissions.

## Implementation Status

**Core Capability:** ⏳ Not Started

The Investigation Gateway — provider transport layer, normalization adapters,
and investigation tools — is not yet implemented. This RFD supersedes the two
earlier RFD 095 drafts (raw tool passthrough and domain-wrapper-only approach).

## Future Work

**Auto-discovery of service-to-provider mappings** (Future RFD)

This RFD requires explicit `service_mappings` configuration. Auto-discovery
would infer these mappings automatically — by scanning git remotes in the
service registry, matching Coral service names against Sentry project slugs, or
reading Linear team prefixes from repository metadata. This would remove the
static maintenance burden while keeping the resolver behaviour identical at
runtime. Deferred because the static map is sufficient for v1 and auto-discovery
requires heuristics that vary across organisations.

**Ambient session context from MCP Resources** (Future RFD)

MCP servers expose Resources (read-only data) in addition to Tools. Injecting
a compact summary of relevant resources — open Sentry issues for the target
service, recent PRs — into the session context at startup would give every
session ambient awareness without an explicit tool call. Requires policy
decisions around resource selection and context window budget.

**Per-colony integration overrides** (Future)

The current design applies integrations globally. A production colony may
warrant different providers (production Sentry org, production GitHub repo) than
a staging colony. Per-colony override blocks would address this without changing
the core architecture.

**Promotion of `coral_integration_query` uses to proper tools** (Ongoing)

Every repeated use of the escape hatch for a particular capability is a signal
that a proper investigation tool is warranted. Usage of
`coral_integration_query` should be monitored and common patterns promoted to
first-class tools with normalization and colony correlation.

**Knowledge domain** (Future RFD)

Notion, Confluence, and internal wikis accessed via MCP to provide
documentation and runbook context during investigation. Requires a new domain
type and normalization schema.
