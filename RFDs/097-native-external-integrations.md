---
rfd: "097"
title: "Semantic Domain Wrappers — Native Abstraction for External Tooling"
state: "draft"
supersedes: [ "095" ]
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "004" ]
database_migrations: [ ]
areas: [ "mcp", "ai", "cli", "ux" ]
---

# RFD 097 - Semantic Domain Wrappers: Native Abstraction for External Tooling

**Status:** 🚧 Draft

## Summary

This RFD defines the architecture for Coral to act as an intelligent gateway to
external developer tools (GitHub, Sentry, Linear, PagerDuty, etc.) via the Model
Context Protocol (MCP). This RFD introduces **Semantic Domain Wrappers**, which
explicitly **supersede** the direct tool-exposure model in RFD 095. Coral
manages external MCP client connections (stdio/http) but does not expose their
raw tools directly to the LLM. Instead, it groups capabilities into native
domains (Activity, Tickets, On-Call), normalizes fragmented outputs, and
provides the LLM with a high-level, correlated view of the developer's entire
ecosystem in a single session.

## Problem

**Current behavior/limitations:**

- **Context Switching**: Incident response requires developers/LLMs to
  manually join infrastructure signals (Coral traces) with engineering context
  (GitHub commits, Sentry issues).
- **Tool Proliferation**: Exposing raw external MCP tools (the approach in RFD 095) leads to "tool dilution." Adding 3-4 servers can inject 100+ tools into
  the LLM's context, degrading reasoning performance and accuracy.
- **Manual Data Join**: When raw tools are used, the LLM must perform multiple
  request/reponse turns to find and correlate a root cause (e.g., fetch traces
  → fetch matching commits → check Sentry stack traces).
- **Secret Management**: Managing third-party API tokens requires a secure,
  dynamic resolution mechanism that avoids persisting secrets in configuration
  files.

**Why this matters:**

Coral's goal is to be the primary interface for software investigation. If it
merely proxies raw APIs, it forces the LLM to act as a "manual pipe" between
different data schemas. By providing semantic abstractions, Coral moves from
being a "client" to an "orchestrator," allowing the LLM to focus on _investigation_
rather than _API orchestration_.

## Solution

Implement a robust MCP client transport layer (stdio/http) and wrap the
resulting tools behind **Semantic Domain Wrappers**.

**Key Design Decisions:**

- **Supersedes RFD 095**: This RFD contains all necessary MCP transport
  logic. The assembly of a flat, prefixed external tool list is discarded in
  favor of a "Domain Gateway" approach.
- **Domain-Based Orchestration**: The LLM interaction is limited to core
  observability domains:
  - **Activity**: Unified timeline of commits, alerts, and deployments.
  - **Tickets**: Search and context extraction from project management tools.
  - **On-Call**: Current rotation and incident timeline.
- **Normalization Layer**: Coral translates proprietary tool outputs (e.g.,
  GitHub's Commit JSON vs. Sentry's Issue JSON) into a unified internal
  schema before presenting them to the LLM.
- **Secure Resolver**: Secrets are resolved at runtime using the `env://`
  prefix, ensuring GITHUB_TOKEN and others are never written to disk.

**Architecture Overview:**

```
[ LLM Session ]
       │
       └── coral_get_activity(service="api", window="1h")
               │
               ├── Integration Manager (internal/cli/integrations/)
               │   │ 1. Resolves secrets (env://GITHUB_TOKEN)
               │   │ 2. Spawns MCP Clients (stdio/http)
               │   │ 3. Dispatches calls to providers
               │   │
               │   ├── [ GitHub MCP Client ] → list_commits(...)
               │   └── [ Sentry MCP Client ] → list_issues(...)
               │
               └── Domain Wrapper (Normalization)
                   └── Merges, sorts, and summarizes into a single result
```

## Implementation

### 1. MCP Client Transports (self-contained)

Coral implements a generic MCP Client Manager capable of maintaining multiple
simultaneous connections.

- **stdio transport**: The CLI spawns the configured command (e.g., `npx`, `uvx`)
  as a subprocess. JSON-RPC messages are exchanged over pipes.
- **http transport**: Support for Server-Sent Events (SSE) and Streamable HTTP
  transports for connecting to remote/hosted MCP servers.
- **Lifecycle**: Clients are active for the duration of the `coral ask` or
  `coral terminal` session.

### 2. Semantic Data Normalization

The Integration Manager maps raw MCP tool responses to structured Go types:

```go
type ActivityEvent struct {
    Source    string    // "github", "sentry"
    Type      string    // "commit", "alert", "deploy"
    Timestamp time.Time
    Summary   string
    Actor     string
}
```

Built-in **Adapters** handle the transformation for standard servers.

### 3. Native Tool Implementation

The following tools are registered in the LLM's tool list:

- **`coral_get_activity(window, service)`**: Returns a unified timeline.
- **`coral_search_tickets(query, status)`**: Searches project context.
- **`coral_get_oncall(service)`**: Returns current on-call details.

### 4. Secret Resolution (`env://`)

Configuration fields for credentials use the `env://` prefix. The CLI
config loader (`internal/config/ask_resolver.go`) resolves these at runtime.

## API Changes

### Configuration Schema

```yaml
# ~/.coral/config.yaml

integrations:
  # Providers define connection parameters and secrets
  providers:
    - alias: github
      transport: stdio
      command: npx
      args: ["-y", "@modelcontextprotocol/server-github"]
      token: "env://GITHUB_TOKEN"

    - alias: sentry
      transport: stdio
      command: uvx
      args: ["mcp-server-sentry"]
      token: "env://SENTRY_TOKEN"

  # Domains map specific providers to capability tools
  domains:
    activity: [github, sentry]
    tickets: [github, linear]
```

### CLI Commands

```bash
# Add a provider with secret reference
coral integration add github --token "env://GITHUB_TOKEN"

# Test the connection and secret resolution
coral integration test github

# List configured integrations and active domains
coral integration list
```

## Implementation Plan

### Phase 1: Transport & Secrets

- [ ] Implement MCP Client Manager in `internal/cli/integrations/client_manager.go`.
- [ ] Add support for `stdio` subprocess lifecycle management.
- [ ] Implement `env://` secret resolution logic in `internal/config/ask_resolver.go`.
- [ ] Implement `coral integration add|list|test` commands.

### Phase 2: Domain Abstraction & Normalization

- [ ] Define shared domain types (`ActivityEvent`, `TicketItem`) in `internal/cli/integrations/types.go`.
- [ ] Implement Built-in Adapters for GitHub (commits) and Sentry (issues).
- [ ] Create the `Orchestrator` to handle parallel provider calls and result merging.

### Phase 3: Agent & Tool Integration

- [ ] Wire the `Integration Manager` into the `coral ask` agent loop.
- [ ] Implement the `coral_get_activity` and `coral_search_tickets` tools.
- [ ] Update system prompts to guide the LLM on using domain-aware investigation.

### Phase 4: Testing & Documentation

- [ ] Add unit tests for normalization and sorting.
- [ ] Add integration tests with mock MCP servers.
- [ ] Update `DOCS.md` with the new integration model.

## Testing Strategy

### Unit Tests

- **Secret Resolution**: Validate `env://` correctly fetches from host environment and handles missing keys.
- **Normalization**: Verify GitHub/Sentry raw JSON is correctly mapped to `ActivityEvent`.
- **Merging**: Ensure events from different providers are sorted chronologically.

### Integration Tests

- **stdio transport**: Spawn a mock Go-based MCP server and verify JSON-RPC pipe communication.
- **Partial Failure**: Mock a scenario where GitHub succeeds but Sentry fails, ensuring the LLM gets partial data.

### E2E Tests

- **Coral Ask Turn**: Run a full session where the LLM calls `coral_get_activity` and accurately summarizes a recent commit and alert.

## Security Considerations

- **Secret Isolation**: Secrets resolved via `env://` exist only in process memory and are never serialized to configuration files.
- **Narrowed Surface Area**: By using domain wrappers, the LLM is shielded from raw provider data, reducing the risk of prompt injection from third-party tool responses.
- **Audit Trails**: Coral logs semantic intent (`coral_get_activity`) rather than opaque tool-specific calls.

## Implementation Status

**Core Capability:** ⏳ Not Started

The implementation is currently in the design phase. MCP transport logic has been explored in prototype contexts, but the Semantic Domain Wrapper architecture is new as of this RFD.

## Future Work

- **Knowledge Domain**: Adding support for searching Notion, Slack, and Confluence via MCP to provide documentation context.
- **Auto-Discovery**: Automatically mapping services to repositories by scanning git remotes or Service Registry metadata.
- **Live Sidebar Updates**: Extending the `coral terminal` sidebar to show ambient activity status from integrated domains.

## Appendix

### Activity Normalization Schema

The `ActivityEvent` struct is the core contract for the Activity domain:

```go
type ActivityEvent struct {
    Source    string    `json:"source"`    // e.g., "github"
    Provider  string    `json:"provider"`  // e.g., "commits"
    Type      string    `json:"type"`      // e.g., "deployment"
    Timestamp time.Time `json:"timestamp"`
    Summary   string    `json:"summary"`
    Actor     string    `json:"actor"`
    Metadata  map[string]string `json:"metadata,omitempty"`
}
```

### JSON-RPC Framing

Coral uses standard JSON-RPC 2.0 over pipes for `stdio` transport, following the 2024-11-05 MCP Specification.
