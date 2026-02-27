---
rfd: "096"
title: "Agent DuckDB Remote Access via Colony Proxy"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: [ "031", "039", "046" ]
database_migrations: [ ]
areas: [ "colony", "cli", "duckdb" ]
---

# RFD 096 - Agent DuckDB Remote Access via Colony Proxy

**Status:** 🎉 Implemented

## Summary

`coral duckdb shell <agent-id>` currently connects directly to the agent's WireGuard mesh IP,
which fails when the CLI is not on the same mesh (the common case when using a remote colony
over its public HTTPS endpoint). This RFD adds an HTTP reverse proxy on the colony's public
endpoint that forwards DuckDB requests to the appropriate agent, enabling remote DuckDB access
for any operator with access to the colony.

## Problem

- **Current behavior**: The CLI resolves the agent ID to its WireGuard mesh IP via the colony
  registry, then connects directly to `http://{meshIP}:9001/duckdb/*`. This only works if the
  CLI is on the WireGuard mesh (same host as colony, or running the deprecated CLI local proxy).

- **Why this matters**: The public HTTPS colony endpoint (RFD 031) is the standard connection
  method for remote operators, containerised setups, and the e2e test environment. All DuckDB
  agent queries fail with a connection timeout when using this mode.

- **Use cases affected**:
  - `coral duckdb shell <agent-id>` — interactive shell
  - `coral duckdb query <agent-id> <sql>` — one-shot query
  - `coral duckdb list-agents --databases` — database enumeration

## Solution

Add an HTTP reverse proxy route on both the colony's internal HTTP server and its public HTTPS
endpoint:

```
/agent/{agentID}/duckdb[/{rest}]
```

The colony looks up the agent's mesh IP from its in-memory registry and reverse-proxies the
request to `http://{meshIP}:9001/{duckdb/rest}`. The CLI detects proxy mode by URL scheme and
selects the appropriate attach URL.

**Key Design Decisions:**

- **Proxy on the colony, not a new protocol**: Agents already serve DuckDB over HTTP on port
  9001 (RFD 039). The colony is already connected to all agents over WireGuard. Proxying is a
  pure HTTP concern with no new agent-side changes required.

- **Proxy registered on both servers**: The proxy handler is registered on the internal HTTP
  server (port 9000) as well as the public HTTPS endpoint (port 8443). This is essential for
  the TLS workaround described below.

- **Auth-exempt on the public endpoint**: DuckDB's `httpfs` extension issues plain HTTP/HTTPS
  requests and cannot attach custom authentication headers. Requiring a Bearer token would make
  the DuckDB `ATTACH` command fail for anyone using the public endpoint. The proxy route is
  read-only (same posture as the colony's own `/duckdb/` endpoint, RFD 046).

- **Mode detection by URL scheme**: The CLI routes through the proxy when the configured colony
  URL is `https://`. Plain `http://` means the internal server, accessible only from the same
  host where the CLI is also on the WireGuard mesh and can reach agents directly.

- **DuckDB httpfs cannot verify self-signed TLS**: DuckDB's `httpfs` extension uses the system
  CA bundle for HTTPS certificate verification. The colony issues its own server certificate
  from an internal CA (RFD 047), which is not in any system trust store. For `ATTACH`, the CLI
  therefore uses the internal HTTP server URL (`http://localhost:{port}/agent/{id}/duckdb/`)
  even when the configured colony URL is HTTPS on localhost. For remote HTTPS deployments with
  a publicly trusted certificate, the HTTPS URL is used directly.

- **No agent-side changes**: Agents continue to serve DuckDB on port 9001 over plain HTTP on
  the WireGuard mesh. The colony is the TLS termination point.

**Benefits:**

- Remote DuckDB access works out of the box with any `CORAL_COLONY_ENDPOINT` configuration.
- No new ports, protocols, or agent deployments required.
- Consistent URL scheme: `{colonyBase}/agent/{id}/duckdb/{db}` is predictable and cacheable.
- `list-agents --databases` works remotely without mesh connectivity.

**Architecture Overview:**

```
Operator CLI (HTTPS mode — public endpoint)
──────────────────────────────────────────────────────────────────────────────────────

  list/gRPC       ──► https://colony:8443/agent/{id}/duckdb  (our TLS-aware HTTP client)
                      │
  DuckDB ATTACH   ──► http://localhost:9000/agent/{id}/duckdb/{db}  (avoids TLS cert issue)
                      │
                      Colony internal HTTP server
                      │  AgentDuckDBProxyHandler
                      ▼
                      http://meshIP:9001/duckdb/{db}  (WireGuard mesh, plain HTTP)
                      │
                      ▼
                      Agent DuckDB server (RFD 039)

Operator CLI (HTTP mode — internal server, mesh member)
──────────────────────────────────────────────────────────────────────────────────────

  DuckDB ATTACH   ──► http://meshIP:9001/duckdb/{db}  (direct, no proxy)
```

### Component Changes

1. **Colony — `internal/colony/httpapi`**:
   - Implement an agent lookup mechanism to resolve agent IDs to mesh IPs.
   - Add an HTTP reverse proxy handler that routes requests from `/agent/{agentID}/duckdb/*`
     to the corresponding agent's DuckDB port on the WireGuard mesh.
   - Exempt the `/agent/` prefix from the public endpoint's authentication
     middleware (read-only access) to support DuckDB's `httpfs` extension.

2. **Colony server — `internal/cli/colony/server.go`**:
   - Wire the agent lookup mechanism to the colony's agent registry.
   - Register the proxy handler on both the internal HTTP server and the public
     HTTPS endpoint.

3. **CLI — `internal/cli/duckdb`**:
   - Add proxy detection logic to determine when to route DuckDB requests through
     the colony instead of connecting directly to agents.
   - Implement a fallback to the internal colony server for DuckDB `ATTACH`
     commands to work around self-signed TLS certificate limitations in some
     environments.
   - Generalize database enumeration and attachment logic to support both direct
     and proxied connection modes.

## API Changes

### New HTTP Proxy Endpoint

| Method         | Path                                   | Description                           |
| -------------- | -------------------------------------- | ------------------------------------- |
| `GET`          | `/agent/{agentID}/duckdb`              | List available databases on the agent |
| `GET` / `HEAD` | `/agent/{agentID}/duckdb/{dbName}`     | Serve agent database file             |
| `GET` / `HEAD` | `/agent/{agentID}/duckdb/{dbName}.wal` | Serve agent WAL file                  |

All other methods return `405 Method Not Allowed` (enforced by the underlying `DuckDBHandler`).

The path `/agent/{agentID}` prefix is stripped before forwarding; the agent receives the
request at its own `/duckdb[/*]` path.

### CLI Usage (unchanged)

```bash
# Works regardless of whether colony URL is local or remote
coral duckdb shell <agent-id>
coral duckdb query <agent-id> "SELECT * FROM beyla_http_metrics_local LIMIT 10"
coral duckdb list-agents --databases
```

## Implementation Plan

### Phase 1: Colony Proxy Handler ✅

- [x] Create `internal/colony/httpapi/agent_duckdb_proxy.go`.
- [x] Implement the agent lookup interface and DuckDB reverse proxy handler.
- [x] Update `httpapi` routing to support the `/agent/` prefix.

### Phase 2: Colony Server Wiring ✅

- [x] Wire the proxy handler to the registry in the colony server.
- [x] Register the proxy handler on both internal and public servers.

### Phase 3: CLI Routing ✅

- [x] Implement proxy detection and URL resolution logic in the CLI.
- [x] Update agent database enumeration and attachment to support proxied mode.
- [x] Apply TLS workaround for `ATTACH` commands.
- [x] Update DuckDB shell and query commands to use the new routing logic.

### Phase 4: Testing

- [ ] Unit test the agent DuckDB proxy handler (path parsing, error states, proxy rewrite).
- [ ] Unit test CLI proxy detection and URL routing logic.
- [ ] E2E test: `coral duckdb shell <agent-id>` via public HTTPS endpoint.

## Security Considerations

- **Read-only**: The agent's `DuckDBHandler` only accepts `GET` and `HEAD`. The proxy does
  not relax this constraint.
- **Allowlisted databases**: The agent only serves explicitly registered database files;
  directory traversal is prevented at the agent (RFD 039).
- **TLS in transit**: CLI ↔ Colony list/gRPC traffic is encrypted via the colony's TLS
  certificate. DuckDB `ATTACH` requests for localhost deployments use the internal HTTP server
  to work around httpfs's inability to verify the colony's self-signed CA. Colony ↔ Agent
  traffic runs over WireGuard (encrypted at the network layer).
- **Auth-exempt**: Consistent with the colony's own `/duckdb/` endpoint (RFD 046). Future
  work could add per-token RBAC for DuckDB access if the DuckDB `httpfs` secret mechanism
  gains reliable custom-header support across versions.
- **No agent identity verification**: The colony trusts its own WireGuard mesh and registry;
  a compromised agent IP entry would redirect queries to the wrong host. This is the same
  trust model used for all existing colony-to-agent communication.

## Implementation Status

**Core Capability:** 🎉 Implemented

Colony proxy handler, server wiring, and CLI routing are complete. `coral duckdb shell <agent-id>`
and `coral duckdb query <agent-id>` work via the public HTTPS endpoint without requiring WireGuard
mesh membership.

**Operational Components:**

- ✅ Colony: `AgentDuckDBProxyHandler` proxies `/agent/{id}/duckdb/*` to agent port 9001
- ✅ Colony: proxy registered on both internal HTTP server and public HTTPS endpoint
- ✅ CLI: scheme-based mode detection (`https://` → proxy, `http://` → direct mesh)
- ✅ CLI: `duckdbAttachBase` falls back to internal HTTP for localhost HTTPS (self-signed cert workaround)
- ✅ CLI: `attachColonyDatabase` uses same workaround for colony DuckDB ATTACH

**What Works Now:**

- Remote DuckDB access via `CORAL_COLONY_ENDPOINT=https://...` (e2e and containerised setups)
- `coral duckdb shell`, `coral duckdb query`, `coral duckdb list-agents --databases` all work remotely
- Local mesh mode unchanged (no regression)

**Remaining:**

- Unit and E2E tests (Phase 4)

## Future Work

**DuckDB httpfs Authentication** (Future — unassigned RFD)

DuckDB's `CREATE SECRET (TYPE HTTP, EXTRA_HTTP_HEADERS MAP {...})` syntax may in the future
allow clients to send a Bearer token with `ATTACH` requests. If this becomes reliable across
DuckDB versions, the `/agent/` proxy could be moved behind the auth middleware, scoped to
tokens with a new `duckdb:read` permission.

**Agent DuckDB for Other Data Sources** (Future)

The `/agent/{id}/duckdb/` proxy could serve additional registered databases beyond `metrics.duckdb`
(e.g., per-service trace databases). This requires no protocol changes — just additional
`RegisterDatabase` calls in the agent startup.
