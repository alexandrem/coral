---
rfd: "095"
title: "MCP Client Integrations — External Tool Context in Coral Sessions"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "004", "051", "094" ]
database_migrations: [ ]
areas: [ "mcp", "ai", "cli", "ux" ]
---

# RFD 095 - MCP Client Integrations: External Tool Context in Coral Sessions

**Status:** 🚧 Draft

## Summary

Coral already acts as an MCP server, exposing colony observability and
debugging tools to external LLM clients (RFD 004). This RFD adds the
inverse: Coral connects outward as an MCP client to external MCP servers
(GitHub, Sentry, Linear, PagerDuty, and any user-configured server) and
makes their tools available within `coral ask` and `coral terminal`
sessions. The LLM gains a unified, merged tool set — colony observability
plus the developer's full external toolchain — in a single session, with
all credentials staying on the user's machine.

## Problem

**Current behavior/limitations:**

- `coral ask` and `coral terminal` can only call tools exposed by the
  connected colony's own MCP server (RFD 004): traces, metrics, eBPF
  probes, service topology, etc.
- Cross-cutting investigation context — which commit triggered a
  regression, whether a Sentry issue group already exists, who is on-call
  in PagerDuty, what the related Linear ticket says — requires the
  developer to context-switch out of the Coral session, look things up
  manually, and paste answers back.
- External LLM clients like Claude Desktop can configure multiple MCP
  servers and get a merged tool view, but they have no access to colony
  observability data without Coral. Coral has the observability data but
  no access to external tooling context.
- No single AI interface holds both signals simultaneously.

**Why this matters:**

Incident response and performance investigation are cross-tool activities.
The root cause of a p99 regression is usually at the intersection of what
the infrastructure signals (traces, CPU profiles) and what the engineering
context explains (recent commit, PR, Sentry issue, on-call rotation). When
those two signals live in different sessions the LLM cannot reason across
them — the developer becomes the manual join.

**Strategic position:**

Coral is installed on the developer's machine. It controls the LLM API key
(the user's own key, no SaaS intermediary). `coral terminal` (RFD 094)
gives it a first-class interactive surface. The missing ingredient to make
Coral a developer's primary AI interface — rather than a complement to
Claude Desktop or Cursor — is the ability to consume the rest of the
developer's toolchain the same way Claude Desktop does: via configurable
MCP client connections.

**Use cases affected:**

- **Incident correlation**: "What changed 40 minutes ago?" → GitHub MCP
  returns the exact commit and diff that hit `payment-api`; Coral traces
  show the regression onset. The LLM correlates them without the developer
  fetching either manually.
- **Error triage**: Sentry has already grouped the errors and identified
  the stack frame. The LLM can pull that context alongside Coral's raw
  telemetry to confirm and expand on the Sentry analysis.
- **On-call handoff**: PagerDuty alert fired at 03:00. By 09:00 the
  on-call engineer opens `coral terminal`, asks "what happened?", and the
  LLM combines trace replay with the PagerDuty incident timeline and any
  Linear tickets opened overnight.
- **Deployment safety**: Before deploying, the LLM checks Coral service
  health (colony MCP) and then checks whether the open PR has passing CI
  and no review blockers (GitHub MCP), in the same turn.

## Solution

Introduce a `mcp_clients` configuration block in `~/.coral/config.yaml`.
At session start (`coral ask` or `coral terminal`), Coral reads this
configuration, establishes connections to each configured external MCP
server, fetches their tool lists, prefixes tool names with the server
alias, and merges them into the tool set available to the LLM alongside
the colony's own tools.

**Key Design Decisions:**

- **Configuration mirrors Claude Desktop**: The `mcp_clients` config
  schema is deliberately close to Claude Desktop's `mcpServers` format.
  Users who already have GitHub or Sentry configured in Claude Desktop can
  transfer that configuration with minimal changes.

- **Session-scoped connections**: External MCP server processes are started
  when a `coral ask` or `coral terminal` session begins and stopped when
  it ends. No persistent background daemon manages these connections. stdio
  servers are subprocesses of the CLI process; they are cleaned up
  automatically on exit.

- **Namespaced tool names**: Every external tool is prefixed with its
  server alias to prevent collisions with colony tools and with each other.
  A GitHub `search_code` tool becomes `github/search_code`; a Sentry
  `list_issues` becomes `sentry/list_issues`. Colony tools retain their
  existing `coral_*` names unchanged.

- **Transparent to the LLM**: The LLM sees a single flat tool list. It
  does not need to know which server backs which tool. The tool
  description makes the data source clear through natural language; the
  prefix is only for the dispatch layer's routing.

- **Credentials stay local**: External MCP servers run as subprocesses of
  the user's CLI process with environment variables injected from the
  user's shell or from `~/.coral/config.yaml`. Nothing goes through Coral
  infrastructure. The user is responsible for the same credentials they
  would pass to Claude Desktop.

- **Colony tools are always primary**: The external tool set is additive.
  Colony tools are never disabled by this RFD. If no external servers are
  configured, behaviour is identical to today.

- **Graceful degradation**: If an external MCP server fails to start or
  drops its connection mid-session, that server's tools are removed from
  the available set and the LLM is notified via a brief system message.
  The session continues with colony tools intact.

- **Controlled tool surface**: External MCP servers can expose dozens of
  tools each; loading all of them inflates the LLM's context window and
  degrades tool selection accuracy. Two complementary mechanisms bound
  this: per-server `allowed_tools` allowlists in config (permanent scoping)
  and `--with`/`--without` CLI flags (per-session scoping). Servers with
  no `allowed_tools` entry expose all their tools, preserving the
  zero-configuration path while giving users an explicit escape valve.

**Architecture Overview:**

```
coral ask / coral terminal session
│
├── MCPClientManager (new, internal/cli/ask/mcp_clients.go)
│   │  reads ~/.coral/config.yaml mcp_clients section
│   │  on session start: connects to each configured server
│   │  on session end:   closes all connections
│   │
│   ├── colony MCP server (existing, RFD 004)
│   │     tools: coral_query_summary, coral_attach_uprobe, ...
│   │
│   ├── [alias: github] stdio → npx @modelcontextprotocol/server-github
│   │     tools (prefixed): github/create_issue, github/get_pull_request,
│   │                        github/list_commits, github/search_code, ...
│   │
│   ├── [alias: sentry] stdio → uvx mcp-server-sentry
│   │     tools (prefixed): sentry/list_issues, sentry/get_stacktrace,
│   │                        sentry/list_releases, ...
│   │
│   └── [alias: linear] http → https://mcp.linear.app/sse
│         tools (prefixed): linear/list_issues, linear/create_issue, ...
│
└── LLM agent (existing coral ask agent)
      sees merged, flat tool list:
        coral_query_summary
        coral_attach_uprobe
        ...
        github/list_commits
        github/get_pull_request
        sentry/list_issues
        linear/list_issues
        ...
```

**Incident investigation example with external clients:**

```
coral terminal session  (colony: prod-us-east, github + sentry configured)

> payments p99 spiked at 14:23. what happened?

✓ Queried colony traces (2.1s)
✓ github/list_commits (payments-service, last 2h) (0.8s)
✓ sentry/list_issues (payments, since=14:00) (0.6s)

## Root Cause

Commit a3f91b2 (deployed 14:19 by @marc) changed connection pool
sizing in payments-service. Coral traces show the p99 spike begins
exactly at 14:21 (2 minutes after the deploy completed).

Sentry has grouped 847 TimeoutError events starting at 14:21, all
originating from DatabasePool.acquire() — matching the pool change.

The commit diff shows the pool max_connections was reduced from 50
to 20. Restoring it should resolve the issue.

Relevant:
- PR #2341 (merged 14:18): "Reduce DB pool for cost savings"
- Sentry issue PAYMENTS-1829 (847 events, open)
```

### Component Changes

1. **Configuration schema** (`internal/config/schema.go`):

   - Add `MCPClients []MCPClientConfig` field to the root config struct.
   - `MCPClientConfig` holds: `alias` (string, unique identifier),
     `transport` (`stdio` or `http`), `command` + `args` (for stdio),
     `url` (for HTTP), `env` (map of env var overrides), `enabled`
     (default true), `timeout_seconds` (default 30), `allowed_tools`
     (optional list of tool names to expose; empty means all tools).
   - Environment values in `env` may reference shell variables using
     `${VAR}` syntax, expanded at connection time from the process
     environment.

2. **MCP client manager** (`internal/cli/ask/mcp_clients.go`):

   - `MCPClientManager` manages the lifecycle of all configured external
     MCP connections for a single session.
   - `Connect(active []string)`: iterates configured servers, skipping any
     not in `active` (populated from `--with`/`--without` flags; empty
     means all enabled servers), starts or connects to each, fetches their
     tool lists via `tools/list`, applies the server's `allowed_tools`
     filter if set, prefixes remaining names with the alias, and registers
     them in an internal dispatch table.
   - `Dispatch(toolName, args)`: routes a prefixed tool call to the correct
     backing server and returns the result.
   - `MergedToolList()`: returns the full flat list of colony + external
     tools for injection into the LLM's tool set.
   - `Close()`: gracefully terminates all stdio subprocesses and HTTP
     connections.
   - Failed connections log a warning and are skipped; they do not abort
     the session.

3. **Agent integration** (`internal/cli/ask/agent.go`):

   - At session initialisation, construct `MCPClientManager` from config
     and call `Connect()`.
   - Pass the merged tool list to the LLM's tool set alongside the existing
     colony MCP tools.
   - Route tool calls whose names contain `/` to
     `MCPClientManager.Dispatch()` instead of the colony MCP server.
   - On session teardown, call `MCPClientManager.Close()`.
   - No changes to the colony MCP call path.

4. **CLI commands** (`internal/cli/mcp/`):

   - `coral mcp clients list`: show configured external MCP servers and
     their connection status.
   - `coral mcp clients test <alias>`: connect to the named server, list
     its tools, and print a summary. Useful for validating configuration.
   - `coral mcp clients add`: interactive wizard to append a new server to
     `~/.coral/config.yaml`. Supports common presets (github, sentry,
     linear, pagerduty).

5. **Session flags** (`coral ask` and `coral terminal`):

   - `--with <alias,...>`: activate only the named external servers for
     this session, regardless of the `enabled` field in config. Servers
     not listed are not connected.
   - `--without <alias,...>`: activate all enabled servers except the
     named ones. Useful for temporarily suppressing a server without
     editing config.
   - The two flags are mutually exclusive. `--with` takes precedence if
     both are supplied (the CLI reports an error).

5. **`coral terminal` sidebar** (`internal/cli/terminal/`, RFD 094):

   - When external MCP clients are configured, the sidebar gains an
     optional **External** section below Agents showing the alias and
     connection status (green dot = connected, grey = disabled, red =
     failed) of each configured server.
   - No automatic data polling from external servers in the sidebar v1.
     The LLM fetches external data on demand during conversation.

### Transport support

**stdio transport:**

The MCP client manager spawns the configured command as a subprocess with
pipes connected to its stdin/stdout. The MCP JSON-RPC protocol runs over
these pipes. The subprocess inherits the user's environment with any
additional `env` overrides applied. The subprocess is killed when the
session ends.

```yaml
mcp_clients:
  - alias: github
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
    env:
      GITHUB_PERSONAL_ACCESS_TOKEN: "${GITHUB_TOKEN}"
```

**HTTP (SSE / Streamable HTTP) transport:**

For servers that expose an HTTP endpoint (Linear, some hosted MCP
servers), the client connects via HTTP. The 2024-11-05 MCP spec SSE
transport and the 2025-03-26 Streamable HTTP transport are both
supported. The `url` field identifies the base endpoint.

```yaml
mcp_clients:
  - alias: linear
    transport: http
    url: "https://mcp.linear.app/sse"
    auth:
      type: bearer
      token: "${LINEAR_TOKEN}"
```

### Preset configurations

`coral mcp clients add` offers a guided flow with presets for the most
common integrations. Presets reduce configuration to providing only the
credential:

```
$ coral mcp clients add

Select a preset (or choose Custom):
  1. GitHub
  2. Sentry
  3. Linear
  4. PagerDuty
  5. Custom

> 1

GitHub MCP server requires a Personal Access Token.
Token (or env var reference like ${GITHUB_TOKEN}): ${GITHUB_TOKEN}

Added github to ~/.coral/config.yaml.
Run: coral mcp clients test github
```

The generated configuration for each preset:

| Preset     | Transport | Command / URL                                  |
|------------|-----------|------------------------------------------------|
| GitHub     | stdio     | `npx -y @modelcontextprotocol/server-github`   |
| Sentry     | stdio     | `uvx mcp-server-sentry`                        |
| Linear     | http      | `https://mcp.linear.app/sse`                   |
| PagerDuty  | stdio     | `npx -y @modelcontextprotocol/server-pagerduty`|

Preset commands are pinned at generation time (e.g.,
`@modelcontextprotocol/server-github@0.6.2`) to avoid unexpected
behaviour from upstream updates. The user can update the version
themselves.

## Implementation Plan

### Phase 1: Configuration schema

- [ ] Add `MCPClientConfig` struct to `internal/config/schema.go`:
      `alias`, `transport`, `command`, `args`, `env`, `url`, `auth`,
      `enabled`, `timeout_seconds`, `allowed_tools`
- [ ] Add `MCPClients []MCPClientConfig` to root config struct
- [ ] Validate aliases are unique; reject reserved prefix `coral`
- [ ] Validate that `transport: stdio` has `command`, `transport: http`
      has `url`
- [ ] Expand `${VAR}` references in `env` values at config load time
- [ ] Validate `allowed_tools` entries are non-empty strings; warn if a
      listed name does not appear in the server's `tools/list` response
      at connect time

### Phase 2: MCP client manager

- [ ] Implement `MCPClientManager` in `internal/cli/ask/mcp_clients.go`
- [ ] stdio transport: subprocess management using `os/exec`, pipe-based
      JSON-RPC framing using `mark3labs/mcp-go` client (same library
      already used for the MCP server)
- [ ] HTTP transport: SSE client for 2024-11-05 transport; Streamable
      HTTP client for 2025-03-26 transport; detect from server response
- [ ] `tools/list` call on connect; apply `allowed_tools` filter if set;
      prefix remaining tool names with `<alias>/`
- [ ] Internal dispatch table: `map[string]backingServer`
- [ ] `Dispatch()`: look up server, forward JSON-RPC `tools/call`, return
      result
- [ ] `MergedToolList()`: union of colony tools and all external tools
- [ ] `Close()`: SIGTERM to stdio subprocesses, close HTTP connections
- [ ] Connection failure handling: log warning, continue without that
      server's tools

### Phase 3: Agent integration

- [ ] Add `--with` and `--without` flags to `coral ask` and
      `coral terminal` cobra commands; validate mutual exclusivity
- [ ] Resolve the active server set from flags and config `enabled` field;
      pass to `MCPClientManager.Connect(active)`
- [ ] Construct `MCPClientManager` in `internal/cli/ask/agent.go` on
      session init
- [ ] Inject `MergedToolList()` into the LLM tool set
- [ ] Route `tools/call` for names containing `/` to
      `MCPClientManager.Dispatch()`
- [ ] Propagate mid-session server disconnects: emit system message to
      conversation, remove tools from available set
- [ ] Call `MCPClientManager.Close()` on session teardown

### Phase 4: CLI management commands

- [ ] Create `internal/cli/mcp/` package with cobra subcommands
- [ ] `coral mcp clients list`: read config, attempt ping to each
      server, display alias / transport / status / tool count
- [ ] `coral mcp clients test <alias>`: connect, list tools, print table
      of tool names and descriptions, disconnect
- [ ] `coral mcp clients add`: interactive preset wizard appending to
      `~/.coral/config.yaml`
- [ ] Register `coral mcp` in `internal/cli/root.go`

### Phase 5: `coral terminal` sidebar extension

- [ ] Add External section to `SidebarModel` (RFD 094) when
      `mcp_clients` is non-empty
- [ ] Show alias + connection status dot for each configured server
- [ ] Status is updated once at session start when `MCPClientManager`
      finishes connecting

### Phase 6: Testing and documentation

- [ ] Unit tests: `MCPClientManager.Connect()` with mock stdio server;
      tool name prefixing; dispatch routing; connection failure handling
- [ ] Unit tests: config validation (duplicate aliases, missing fields,
      reserved prefix)
- [ ] Integration tests: `coral mcp clients test github` against a mock
      stdio MCP server (no real GitHub token required)
- [ ] Integration tests: mid-session server disconnect removes tools and
      emits system message
- [ ] E2E tests: full `coral ask` session with mocked external server;
      LLM receives merged tool list; external tool call dispatched
      correctly
- [ ] Update `docs/MCP.md` with client configuration section
- [ ] Update `docs/CLI_REFERENCE.md` with `coral mcp clients` commands

## API Changes

### Configuration

```yaml
# ~/.coral/config.yaml

mcp_clients:
  # GitHub — source code, PRs, commits, issues
  - alias: github
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github@0.6.2"]
    env:
      GITHUB_PERSONAL_ACCESS_TOKEN: "${GITHUB_TOKEN}"
    # Expose only the tools needed for investigation workflows.
    # Omit allowed_tools to expose all tools from this server.
    allowed_tools:
      - list_commits
      - get_pull_request
      - search_code
      - get_file_contents

  # Sentry — error groups, stack traces, releases
  - alias: sentry
    transport: stdio
    command: uvx
    args: ["mcp-server-sentry==0.5.1"]
    env:
      SENTRY_AUTH_TOKEN: "${SENTRY_TOKEN}"
      SENTRY_ORGANIZATION: my-org
    allowed_tools:
      - list_issues
      - get_stacktrace
      - list_releases

  # Linear — issues, projects, cycles
  - alias: linear
    transport: http
    url: "https://mcp.linear.app/sse"
    auth:
      type: bearer
      token: "${LINEAR_TOKEN}"

  # PagerDuty — incidents, schedules, on-call
  - alias: pagerduty
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-pagerduty@0.3.0"]
    env:
      PAGERDUTY_API_KEY: "${PAGERDUTY_KEY}"

  # Custom / internal MCP server
  - alias: internal-runbook
    transport: http
    url: "http://runbook-mcp.internal:8080/sse"
    enabled: false   # opt out without deleting the entry
    timeout_seconds: 10
```

**Configuration fields:**

| Field             | Type     | Required | Default | Description |
|-------------------|----------|----------|---------|-------------|
| `alias`           | string   | yes      | —       | Unique name. Used as tool name prefix. Must match `[a-z][a-z0-9-]*`. Reserved: any alias starting with `coral`. |
| `transport`       | string   | yes      | —       | `stdio` or `http` |
| `command`         | string   | stdio    | —       | Executable to spawn |
| `args`            | []string | no       | []      | Arguments to `command` |
| `env`             | map      | no       | {}      | Env overrides. `${VAR}` expanded from shell env. |
| `url`             | string   | http     | —       | Base URL of HTTP MCP server |
| `auth.type`       | string   | no       | —       | `bearer` (adds `Authorization: Bearer <token>` header) |
| `auth.token`      | string   | no       | —       | Token value or `${VAR}` reference |
| `enabled`         | bool     | no       | true    | Set false to disable without removing the entry |
| `timeout_seconds` | int      | no       | 30      | Per-call timeout for tools from this server |
| `allowed_tools`   | []string | no       | []      | Subset of tool names to expose. Empty means all tools from this server are exposed. Names are matched against the upstream server's tool names before prefixing. |

### CLI Commands

```bash
# List configured external MCP servers and their status
coral mcp clients list

# Example output:
External MCP Clients:
  github       stdio   connected   34 tools
  sentry       stdio   connected   12 tools
  linear       http    connected    8 tools
  pagerduty    stdio   failed       —       (exit 1: PAGERDUTY_KEY not set)
  internal-kb  http    disabled     —

# Test a specific client (connect, list tools, disconnect)
coral mcp clients test github

# Example output:
Connecting to github (stdio: npx -y @modelcontextprotocol/server-github@0.6.2)...
Connected. 34 tools available.

Tool                             Description
────────────────────────────────────────────────────────────────────────
github/create_issue              Create a new GitHub issue
github/get_pull_request          Get details of a pull request
github/list_commits              List commits in a repository
github/search_code               Search code across repositories
github/get_file_contents         Get the contents of a file
... (34 total)

# Add a new server via interactive wizard
coral mcp clients add

# Non-interactive add with a known preset
coral mcp clients add --preset github --token "${GITHUB_TOKEN}"

# Session-level server selection
coral ask --with github,sentry "why is payments p99 high?"
coral ask --without pagerduty "general question"
coral terminal --with github
```

### Tool naming in LLM sessions

Tools from external servers appear in the LLM tool list with their alias
prefix. The tool description is passed through verbatim from the upstream
server — no rewriting.

```
# LLM tool list (abbreviated) in a coral ask session
# with github and sentry configured:

coral_query_summary          (colony)
coral_attach_uprobe          (colony)
coral_list_services          (colony)
...
github/list_commits          (external: github)
github/get_pull_request      (external: github)
github/search_code           (external: github)
sentry/list_issues           (external: sentry)
sentry/get_stacktrace        (external: sentry)
...
```

## Testing Strategy

### Unit Tests

- Tool name prefixing: `list_commits` from alias `github` →
  `github/list_commits`. Collision with another server registered first
  does not overwrite.
- Dispatch routing: call to `github/list_commits` reaches the GitHub
  client, not the colony server.
- Config validation: duplicate aliases rejected; alias `coral-something`
  rejected (reserved prefix); `transport: stdio` without `command`
  rejected.
- `${VAR}` expansion in `env`: present var expanded; missing var
  substituted as empty string with a warning.
- Connection failure: one server fails to start; other servers and colony
  tools remain available; `MergedToolList()` excludes the failed server's
  tools.
- `allowed_tools` filtering: server exposes 34 tools; `allowed_tools`
  lists 4; `MergedToolList()` contains exactly those 4 prefixed tools.
- `allowed_tools` with unknown name: name not present in server's
  `tools/list` response emits a warning but does not abort the session.
- `--with` flag: only the listed servers are connected; others are skipped
  regardless of `enabled` value in config.
- `--without` flag: all enabled servers except the listed ones connect.
- `--with` and `--without` together: CLI exits with a validation error.

### Integration Tests

- Mock stdio MCP server (in-process subprocess) exposes 3 tools. Client
  manager connects, fetches tool list, calls one tool, verifies result
  forwarded verbatim.
- Mid-session disconnect: mock server exits unexpectedly; next tool call
  from that server returns an error message; colony tools unaffected.
- `coral mcp clients test <alias>` against a mock server prints tool
  table and exits cleanly.

### E2E Tests

- `coral ask` session with GitHub and Sentry mock servers; LLM receives
  merged tool list containing `github/*` and `sentry/*` tools.
- LLM calls `github/list_commits`; dispatch reaches mock GitHub server;
  result injected into LLM context.
- Session with no `mcp_clients` config: behaviour identical to today,
  no regression.

## Security Considerations

**External server trust:**

External MCP servers are third-party processes running on the user's
machine with the user's credentials. Coral treats them the same way
Claude Desktop does: as trusted by the user. Users are responsible for
auditing the MCP servers they configure, particularly those installed
via `npx` or `uvx` without version pinning.

Coral mitigates supply chain risk through:
- Preset configurations pin package versions at the time of `add`.
- The `coral mcp clients test` command shows exactly what tools a server
  exposes before any session uses them.
- No MCP server binary or package is bundled in the Coral binary. Coral
  only manages connections; it does not supply the server implementations.

**Credential handling:**

- Credentials in `env` values are expanded from the user's shell
  environment at runtime, not stored in `~/.coral/config.yaml`.
- If a token is written literally into `config.yaml`, Coral emits a
  warning recommending the `${VAR}` form instead.
- Credentials are passed only to the subprocess environment or HTTP
  headers of the corresponding server. They are not logged, not sent to
  the colony, and not visible to other configured MCP servers.

**Tool isolation:**

- External MCP tool calls are dispatched directly from the CLI process to
  the external server. They do not pass through the colony. Colony RBAC
  and audit logs do not cover external tool calls.
- External tools can perform write operations (create issues, comment on
  PRs) depending on the server's capabilities and the credential's
  permissions. The LLM may invoke these tools; users should scope
  credentials to the minimum permissions required (e.g., read-only GitHub
  tokens for investigation-only workflows).

**Subprocess security:**

- stdio server processes are spawned with `os/exec`. They inherit the
  user's environment only after explicit `env` overrides are applied —
  no additional privileges are granted.
- Processes are killed via SIGTERM when the session ends, followed by
  SIGKILL after a 5-second grace period.

## Future Work

**System prompt enrichment from external MCP Resources** (Future RFD)

MCP servers can expose Resources (read-only data) in addition to Tools.
A GitHub server exposes recent open PRs as a Resource; Sentry exposes
active incident groups. Injecting a compact summary of these resources
into the system prompt at session start — without the LLM needing to
call a tool — would give every session ambient awareness of the current
external context.

This requires a defined policy for which resources to fetch, how to
summarise them to avoid bloating the context window, and how to refresh
them during long sessions. It is a meaningful capability addition and
warrants its own RFD.

**`coral terminal` sidebar ambient external data** (Extension to RFD 094)

The sidebar currently shows colony services and agents. Once external MCP
clients are established, the sidebar can show live external context: open
Sentry issues for services visible in the service list, recent GitHub
PRs for those services, PagerDuty on-call status. This requires the
external servers to expose resources or tools that can be polled
efficiently on the sidebar's refresh interval, and a mapping from colony
service names to the corresponding external identifiers (GitHub repo
name, Sentry project slug). Deferred until the core client integration is
proven stable.

**Shared external client config with Claude Desktop** (Future)

Users who already have GitHub, Sentry, and other servers configured in
Claude Desktop's `claude_desktop_config.json` should not need to
duplicate configuration. A `coral mcp clients import-claude-desktop`
command could read the Claude Desktop config and populate
`~/.coral/config.yaml`. Straightforward but non-trivial because the
credential model differs (Claude Desktop uses env vars passed in the
config; Coral prefers `${VAR}` references).

**Per-colony external client overrides** (Future)

The current design applies `mcp_clients` globally across all colonies and
sessions. A production colony might warrant different external context
(production Sentry org, production GitHub repo) than a staging colony.
Per-colony override blocks in the config can address this without
changing the core architecture.

**Community MCP client presets registry** (Future)

The preset list is currently hardcoded. As the MCP ecosystem grows, a
community-maintained registry of verified presets (similar to
Homebrew formulae) would let users discover and add integrations without
knowing the underlying command syntax. This depends on ecosystem maturity.

## Appendix

### Why not reuse the colony MCP proxy?

The colony MCP proxy (RFD 004) bridges external LLM clients to the
colony. It runs server-side (inside the colony process) and is not
involved in outbound tool calls from the CLI agent. The CLI agent is the
MCP client for both colony tools and external tools; extending it to
manage multiple upstream connections is the natural place for this
feature. Adding external client management to the colony would couple
server-side infrastructure to developer-specific third-party credentials,
which is contrary to the colony's role as a shared infrastructure service.

### Tool count and context window budget

Each additional MCP server adds tools to the LLM's tool list. Most LLMs
accept tool lists of 50–100+ tools without significant degradation, but
very large lists do consume context window tokens and can dilute tool
selection accuracy.

Mitigations built into v1:
- `enabled: false` opts a server out of all sessions without removing it
  from config.
- `allowed_tools` per server limits the exposed tool surface permanently.
  A GitHub server with 34 tools can be scoped to 4 investigation-relevant
  tools without losing the ability to expand later.
- `--with <alias,...>` activates only the named servers for a single
  session, ignoring all others.
- `--without <alias,...>` suppresses specific servers for a single
  session.

Users with 5+ external servers configured should use `allowed_tools` to
keep the merged tool list under ~20 external tools. This preserves tool
selection accuracy without restricting which servers are available.

**Future: meta-tool dispatch**

For cases where `allowed_tools` is insufficient — unknown internal MCP
servers whose full tool surface cannot be predicted — a meta-tool pattern
can replace direct tool exposure for a given server:

```
<alias>/list_tools()          → returns the server's tool catalog
<alias>/call(tool, args)      → dispatches an arbitrary call
```

This collapses N tools from a server into 2, at the cost of an extra LLM
turn for tool discovery. This is most useful for internal/custom MCP
servers; well-known servers (GitHub, Sentry) are better served by
`allowed_tools` since the LLM already knows their tool names from
training data. Meta-tool dispatch is deferred until a concrete need
emerges.

### `mark3labs/mcp-go` client support

The colony MCP server already uses `mark3labs/mcp-go` as its server
library. The same library provides an MCP client implementation, making
it the natural choice for the client manager. Both stdio and HTTP
transports are supported. No new MCP library dependency is introduced.
