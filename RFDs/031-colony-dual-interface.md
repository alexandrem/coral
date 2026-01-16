---
rfd: "031"
title: "Colony Dual Interface (Mesh + Optional Public Endpoint)"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "001", "003", "005", "049" ]
database_migrations: [ ]
areas: [ "networking", "security", "cli", "architecture" ]
---

# RFD 031 - Colony Dual Interface (Mesh + Optional Public Endpoint)

**Status:** 🚧 Draft

## Summary

Colony should support optional public HTTPS endpoints in addition to its private
WireGuard mesh interface, matching Reef's dual-interface pattern (RFD 003). This
provides a consistent architecture, simplifies CLI access, and enables external
integrations (Slack bots, CI/CD, IDEs) for single-colony users without requiring
the `coral proxy` workaround (RFD 005).

## Problem

### Current State

RFD 005 introduced `coral proxy` as a solution for CLI access to colonies over
the WireGuard mesh:

```
CLI → localhost:8000 (proxy) → WireGuard mesh → Colony mesh IP
```

This works but has several issues:

**1. Architectural Inconsistency:**

- **Colony**: Mesh-only (requires proxy)
- **Reef**: Dual interface (mesh + public HTTPS) per RFD 003
- Why do these work differently?

**2. CLI Complexity:**

- CLI has special discovery logic for local endpoints (`coral status`)
- Some commands try to detect if colony is reachable directly
- Requires launching `coral proxy` as separate process
- Different behavior for local vs remote colonies

**3. Limited External Integrations:**

- Single-colony users can't integrate with:
    - Slack bots (require public endpoint)
    - GitHub Actions CI/CD
    - IDE extensions (Cursor, VS Code)
    - Mobile apps
    - External monitoring tools
- These use cases require Reef (heavy, centralized)
- Workaround: Use `coral proxy` + ngrok/Cloudflare tunnel (brittle)

**4. User Experience:**

- "Why does Reef have a public endpoint but Colony doesn't?"
- "I need to run `coral proxy` just to use the CLI?"
- "My Slack bot requires Reef even though I have one Colony?"

### Why This Matters

- **Complexity:** Current proxy pattern adds deployment complexity
- **Consistency:** Colony and Reef should use the same dual-interface pattern
- **Integrations:** Single-colony users deserve the same integration
  capabilities as multi-colony (Reef) users
- **DX:** Simpler mental model when both Colony and Reef work the same way

## Solution

### Overview

Colony supports **optional dual interface**:

1. **Private WireGuard mesh** (always enabled)
    - For Agents, other Colonies, Reef
    - Encrypted, application-scoped

2. **Public HTTPS endpoint** (opt-in)
    - For CLI, external integrations, human access
    - Same capabilities as Reef's public endpoint (RFD 003)
    - Supports both Buf Connect RPC and MCP SSE

**Key Principle:** Public endpoint is **optional** and **opt-in**. Colony
remains fully functional with just the mesh interface. This maintains
decentralization - no dependency on public endpoints if not needed.

### Architecture

```
External Clients             Colony (Dual Interface)           Mesh Peers
┌──────────────┐            ┌────────────────────┐          ┌───────────┐
│ coral CLI    │──HTTPS────▶│  Public Endpoint   │          │  Agent    │
└──────────────┘            │  :8443 (opt-in)    │          └─────┬─────┘
                            │  - Buf Connect RPC │                │
┌──────────────┐            │  - MCP SSE         │                │
│ Slack Bot    │──HTTPS────▶│  - Auth/RBAC       │                │
└──────────────┘            │  - TLS             │                │
                            └────────────────────┘                │
┌──────────────┐                     │                            │
│ GitHub       │──HTTPS────▶         │                            │
│ Actions      │                     │                            │
└──────────────┘                     │                            │ WireGuard
                                     │                            │ Mesh
┌──────────────┐            ┌────────▼────────────┐              │
│ IDE          │──HTTPS────▶│  Colony Core        │◄─────────────┘
│ Extension    │            │  - MCP Gateway      │
└──────────────┘            │  - Observability DB │          ┌───────────┐
                            │  - LLM Orchestration│◄─────────│  Reef     │
                            └─────────────────────┘  Mesh    └───────────┘
                                     ▲
                                     │ WireGuard
                                     │ Mesh
                            ┌────────┴─────────┐
                            │  Agent           │
                            └──────────────────┘
```

### Key Design Decisions

#### 1. Consistent with Reef (RFD 003)

**Decision:** Colony uses the exact same dual-interface pattern as Reef.

**Rationale:**

- Simpler mental model
- Same configuration format
- Same security mechanisms (API tokens, JWT, mTLS, RBAC)
- Code reuse between Colony and Reef implementations

#### 2. Public Endpoint is Optional (Opt-In)

**Decision:** Public endpoint is disabled by default, enabled via config.

**Rationale:**

- Maintains zero-config local development workflow
- No security surface if not needed
- Decentralized by default (mesh-only)
- Progressive enhancement when external access needed

**Configuration:**

```yaml
colony:
    id: my-app-dev

    # Private mesh (always enabled)
    mesh:
        port: 41820

    # Public endpoint (optional, disabled by default)
    public_endpoint:
        enabled: false  # opt-in
```

#### 3. Supersedes coral proxy for Direct Access

**Decision:** When public endpoint is enabled, CLI can connect directly.
`coral proxy` becomes optional fallback for special cases.

**Rationale:**

- Simpler: No separate proxy process
- Faster: No additional hop
- Consistent: Same access pattern for local/remote colonies

**Migration Path:**

- RFD 005 `coral proxy` remains valid for:
    - Accessing colonies without public endpoints
    - Tunneling through restricted networks
    - Development/testing scenarios
- But most users will use direct public endpoint access

#### 4. Minimal Auth for CLI Access

**Decision:** Use API tokens for CLI authentication. OIDC/JWT for human users
deferred to separate RFD.

**Rationale:**

- Simple to implement and use
- Sufficient for CLI and bot integrations
- No dependency on external identity providers
- Agent authorization is separate concern (RFD 049)

**Note:** This auth layer is specifically for CLI/external client access to the
public endpoint. Agent-to-colony authorization uses referral tickets via
Discovery (RFD 049).

### Configuration Example

**Colony with Public Endpoint:**

```yaml
# ~/.coral/colonies/my-app-dev.yaml
colony:
    id: my-app-dev-local
    app_name: my-shop
    environment: dev

mesh:
    private_key: <base64>
    public_key: <base64>
    ipv4: 10.42.0.1
    ipv6: fd42::1
    port: 41820

# Public endpoint (optional)
public_endpoint:
    enabled: true
    host: 0.0.0.0  # or 127.0.0.1 for localhost-only
    port: 8443

    # TLS configuration (required if host != 127.0.0.1)
    tls:
        cert: /etc/coral/tls/cert.pem
        key: /etc/coral/tls/key.pem
        # Or use Let's Encrypt / cert-manager

    # MCP server (for AI assistants like Claude Desktop)
    mcp:
        enabled: true
        transport: sse  # or stdio
        path: /mcp/sse

    # Authentication (API tokens for CLI/bots)
    # Note: Agent auth uses referral tickets via Discovery (RFD 049)
    # Note: OIDC for human users deferred to separate RFD
    auth:
        api_tokens:
            -   token_id: cli-dev
                permissions: [ status, query, analyze, debug, admin ]
            -   token_id: ci-cd
                permissions: [ status, query ]
                rate_limit: 100/hour
            -   token_id: slack-bot
                permissions: [ status, query, analyze ]
                rate_limit: 50/hour

storage:
    type: duckdb
    path: ~/.coral/data/my-app-dev.db
```

**CLI Configuration:**

```yaml
# ~/.coral/cli.yaml
default_colony: my-app-dev

colonies:
    my-app-dev:
        # Public endpoint (direct access)
        endpoint: https://localhost:8443
        auth:
            type: api_token
            token: dev-tok-abc123...

    my-app-prod:
        # Public endpoint (remote access)
        endpoint: https://colony.mycompany.com:8443
        auth:
            type: api_token
            token_file: ~/.coral/tokens/prod.token

    my-app-staging:
        # Mesh-only (uses coral proxy fallback)
        endpoint: mesh://10.43.0.1:9000
        # No auth needed for mesh (WireGuard provides auth)
```

### CLI Behavior

**With Public Endpoint:**

```bash
$ coral status

# CLI detects public endpoint in config
# → Direct HTTPS request to https://localhost:8443/coral.colony.v1.ColonyService/GetStatus
# → No proxy needed

Colony: my-app-dev [ONLINE]
  ├─ App: my-shop
  ├─ Environment: dev
  ├─ Uptime: 2h 15m
  └─ Agents: 3 connected
```

**Without Public Endpoint (Fallback to Proxy):**

```bash
$ coral status

# CLI detects mesh-only colony
# → Checks if coral proxy is running on localhost:8000
# → If yes: Use proxy
# → If no: Show helpful error

Error: Colony "my-app-staging" is mesh-only and requires coral proxy.

Run: coral proxy start my-app-staging
Then: coral status
```

### External Integration Examples

#### 1. Slack Bot (Single Colony)

```python
# slack_bot.py
import requests

COLONY_ENDPOINT = "https://colony.mycompany.com:8443"
API_TOKEN = os.environ["CORAL_API_TOKEN"]

@app.command("/coral-status")
def coral_status(ack, command):
    ack()

    # Direct HTTPS call to Colony (no Reef needed!)
    response = requests.post(
        f"{COLONY_ENDPOINT}/coral.colony.v1.ColonyService/GetStatus",
        headers={"Authorization": f"Bearer {API_TOKEN}"},
    )

    status = response.json()
    return f"Colony {status['colony_id']}: {status['agent_count']} agents, {status['status']}"
```

#### 2. GitHub Actions CI/CD

```yaml
# .github/workflows/deploy.yml
-   name: Check Colony Health
    run: |
        curl -H "Authorization: Bearer ${{ secrets.COLONY_TOKEN }}" \
             https://colony-dev.mycompany.com:8443/coral.colony.v1.ColonyService/GetStatus
```

#### 3. IDE Extension (VS Code)

```typescript
// VS Code extension: query local dev colony
const colonyEndpoint = "https://localhost:8443";
const apiToken = vscode.workspace.getConfiguration("coral").get("apiToken");

async function queryColony(question: string) {
    const response = await fetch(`${colonyEndpoint}/coral.colony.v1.ColonyService/Ask`, {
        method: "POST",
        headers: {
            "Authorization": `Bearer ${apiToken}`,
            "Content-Type": "application/json",
        },
        body: JSON.stringify({question}),
    });

    return response.json();
}
```

## API Changes

### Colony Service Extensions

**File: `proto/coral/colony/v1/colony.proto`**

No changes required - existing ColonyService methods work over both mesh and
public endpoint.

The dual interface is a **transport-level decision**, not a protocol change.
Same Buf Connect RPC, two different listeners:

- Mesh listener: `10.42.0.1:9000` (WireGuard)
- Public listener: `0.0.0.0:8443` (HTTPS/TLS)

### MCP Server Support

Colony exposes same MCP tools as Reef (RFD 003):

- `coral_query_metrics`
- `coral_query_traces`
- `coral_agent_exec`
- `coral_get_status`
- `coral_get_topology`

**Configuration:**

```yaml
public_endpoint:
    mcp:
        enabled: true
        transport: sse
        path: /mcp/sse
```

**Claude Desktop Config:**

```json
{
    "mcpServers": {
        "coral-dev": {
            "transport": "sse",
            "url": "https://localhost:8443/mcp/sse",
            "headers": {
                "Authorization": "Bearer dev-tok-abc123..."
            }
        }
    }
}
```

## Security Considerations

### Threat Model

**With Public Endpoint Enabled:**

1. **Exposure:** Colony is reachable from network
2. **Mitigation:** TLS + Authentication (API tokens/JWT/mTLS)
3. **RBAC:** Limit what each token can do

**Attack Vectors:**

- **Unauthorized access:** Mitigated by token auth + rate limiting
- **Token leakage:** Tokens are revocable, scoped permissions
- **DDoS:** Rate limiting per token
- **MITM:** TLS encryption

### Security Model

**Layers:**

1. **TLS:** Encrypted transport (required for public endpoint)
2. **Authentication:** API tokens verify caller identity
3. **Authorization (RBAC):** Check permissions per token
    - `status`: Read colony status
    - `query`: Query metrics/traces
    - `analyze`: AI analysis (may trigger shell/exec, probes)
    - `debug`: Attach live probes
    - `admin`: Change colony config
4. **Rate Limiting:** Prevent abuse (per token)
5. **Audit Logging:** Track who did what

**Note:** Agent-to-colony authorization uses referral tickets via Discovery
(RFD 049). OIDC for human user authentication deferred to separate RFD.

**Comparison:**

| Aspect            | Mesh Interface    | Public Endpoint    |
|-------------------|-------------------|--------------------|
| **Encryption**    | WireGuard         | TLS 1.3            |
| **Auth**          | Referral tickets  | API tokens         |
| **Access**        | Mesh peers only   | Network-reachable  |
| **Rate Limiting** | No (trusted mesh) | Yes (per token)    |
| **Audit**         | Optional          | Recommended        |

### Default Configuration (Secure by Default)

```yaml
public_endpoint:
    enabled: false  # Must opt-in

    # When enabled:
    host: 127.0.0.1  # Default to localhost-only (no network exposure)

    auth:
        require: true  # Always require auth (no anonymous access)

        # No default tokens (user must create)
        api_tokens: [ ]
```

**Progressive Enhancement:**

1. **Local dev:** `host: 127.0.0.1` (localhost-only, no TLS needed)
2. **Remote access:** `host: 0.0.0.0` + TLS + API tokens
3. **Enterprise:** Add OIDC (separate RFD) + audit logging

## Use Cases

### 1. Local Development (Localhost Only)

```yaml
public_endpoint:
    enabled: true
    host: 127.0.0.1  # localhost-only
    port: 8443

    auth:
        api_tokens:
            -   token_id: dev
                permissions: [ status, query, analyze, debug, admin ]
```

**Benefit:** CLI works without proxy, IDE extensions work, no network exposure.

### 2. Remote Team Collaboration

```yaml
public_endpoint:
    enabled: true
    host: 0.0.0.0  # network-accessible
    port: 8443
    domain: dev.mycompany.com

    tls:
        cert: /etc/letsencrypt/live/dev.mycompany.com/fullchain.pem
        key: /etc/letsencrypt/live/dev.mycompany.com/privkey.pem

    auth:
        api_tokens:
            -   token_id: alice-dev
                permissions: [ status, query, analyze, debug ]
            -   token_id: bob-dev
                permissions: [ status, query, analyze ]
```

**Benefit:** Team members query colony from anywhere, no VPN/SSH needed.

### 3. CI/CD Integration

```yaml
public_endpoint:
    auth:
        api_tokens:
            -   token_id: github-actions
                permissions: [ status, query ]
                rate_limit: 100/hour
            -   token_id: deploy-pipeline
                permissions: [ status, query, analyze ]
                rate_limit: 50/hour
```

**Benefit:** GitHub Actions checks colony health before deployment.

### 4. Slack Bot (Single Colony)

```yaml
public_endpoint:
    auth:
        api_tokens:
            -   token_id: slack-bot
                permissions: [ status, query, analyze ]
                rate_limit: 100/hour
```

**Benefit:** Slack bot queries dev colony, no Reef needed for single-colony use
case.

### 5. Claude Desktop Integration

```yaml
public_endpoint:
    mcp:
        enabled: true
        transport: sse
        path: /mcp/sse

    auth:
        api_tokens:
            -   token_id: claude-desktop
                permissions: [ status, query, analyze, debug ]
```

**Benefit:** Claude Desktop queries local dev colony while coding.

## Comparison: Dual Interface vs coral proxy

| Aspect                    | RFD 005 (coral proxy) ✅ Implemented | RFD 031 (Dual Interface)      |
|---------------------------|--------------------------------------|-------------------------------|
| **Architecture**          | CLI → proxy → mesh → Colony          | CLI → HTTPS → Colony          |
| **Deployment**            | Separate proxy process               | Built into Colony             |
| **Performance**           | Extra hop (proxy)                    | Direct connection             |
| **External integrations** | Requires ngrok/tunnel workaround     | Native HTTPS endpoint         |
| **Consistency**           | Colony ≠ Reef                        | Colony = Reef                 |
| **Configuration**         | Proxy config + colony config         | Single colony config          |
| **Local dev**             | Proxy on localhost:8000              | Colony on localhost:8443      |
| **Remote access**         | Proxy per colony                     | Direct HTTPS                  |
| **Security**              | Localhost-only proxy                 | TLS + auth on public endpoint |
| **Status**                | ✅ Implemented (simplified)          | 🚧 Draft                      |

**When to use coral proxy (RFD 005):**

- Colony has no public endpoint (mesh-only)
- Need to tunnel through restricted network
- Development/testing specific proxy behavior
- Already have mesh connectivity via local agent

**When to use dual interface (RFD 031):**

- Enable external integrations (Slack, CI/CD, IDEs)
- Simplify CLI access without proxy setup
- Consistent with Reef architecture
- Single-colony users needing integration capabilities

## Migration Path

RFD 005 (CLI Local Proxy) is now implemented in a simplified form. This RFD
provides an **alternative approach** where Colony exposes a public HTTPS
endpoint directly, eliminating the need for a separate proxy process in most
cases.

**Coexistence Model:**

- **RFD 005 (coral proxy):** For mesh-only colonies, restricted networks, or
  development/testing scenarios
- **RFD 031 (dual interface):** For external integrations, simplified CLI access,
  and Claude Desktop/MCP integration

Both approaches remain valid. Users choose based on their deployment needs:

| Scenario                      | Recommended Approach    |
| ----------------------------- | ----------------------- |
| External integrations needed  | RFD 031 (dual interface)|
| Air-gapped / ultra-secure     | RFD 005 (proxy only)    |
| Local development             | Either (031 is simpler) |
| CI/CD / Slack bot integration | RFD 031 (dual interface)|

## Implementation Plan

### Phase 1: Colony Public Endpoint Server

- [ ] Create `internal/colony/public_endpoint.go`
    - [ ] HTTPS listener with TLS
    - [ ] Buf Connect server (same as mesh interface)
    - [ ] MCP SSE endpoint
    - [ ] Auth middleware (API token verification)
    - [ ] RBAC middleware (permission checks)
    - [ ] Rate limiting middleware
- [ ] Update `internal/colony/server.go`
    - [ ] Dual listener: mesh + public (if enabled)
    - [ ] Shared Buf Connect handlers
- [ ] Add configuration parsing
    - [ ] `public_endpoint` section in colony.yaml
    - [ ] Validate TLS config
    - [ ] Load API tokens

### Phase 2: Authentication & Authorization

- [ ] Create `internal/colony/auth` package
    - [ ] API token verification (Bearer token in Authorization header)
    - [ ] RBAC permission checks per token
    - [ ] Token management (create, revoke, list)
- [ ] Add rate limiting
    - [ ] Per-token rate limits
    - [ ] Sliding window algorithm
    - [ ] Return HTTP 429 on exceed
- [ ] Add audit logging
    - [ ] Log authenticated requests
    - [ ] Include: timestamp, token_id, method, source IP

**Note:** OIDC/JWT for human users deferred to separate RFD. Agent auth uses
referral tickets (RFD 049).

### Phase 3: CLI Updates

- [ ] Update `internal/cli/client.go`
    - [ ] Detect public endpoint in colony config
    - [ ] Prefer public endpoint over proxy
    - [ ] Fallback to proxy if public not available
    - [ ] Add `--endpoint` flag to override
- [ ] Update all CLI commands
    - [ ] `coral status` uses public endpoint
    - [ ] `coral ask` uses public endpoint
    - [ ] `coral exec` uses public endpoint
    - [ ] `coral debug` uses public endpoint
- [ ] Add token management commands
    - [ ] `coral token create <name> --permissions status,query`
    - [ ] `coral token list`
    - [ ] `coral token revoke <token_id>`

### Phase 4: MCP Server Integration

- [ ] Update `internal/colony/mcp_server.go`
    - [ ] Support SSE transport on public endpoint
    - [ ] Reuse existing MCP tools (status, query, exec, debug)
    - [ ] Require auth for MCP endpoint
- [ ] Add MCP-specific RBAC
    - [ ] Some tools require specific permissions
    - [ ] `coral_agent_exec` requires `debug` permission
    - [ ] `coral_query_metrics` requires `query` permission

### Phase 5: Documentation

- [ ] Update IMPLEMENTATION.md with dual interface architecture
- [ ] Create docs/PUBLIC_ENDPOINT.md with:
    - [ ] Configuration examples
    - [ ] Security best practices
    - [ ] External integration examples
    - [ ] Troubleshooting guide
- [ ] Update README.md to mention dual interface
- [ ] Update RFD 005 with note about RFD 031

### Phase 6: Testing

- [ ] Unit tests for auth middleware
- [ ] Unit tests for RBAC enforcement
- [ ] Integration tests: CLI → public endpoint → Colony
- [ ] E2E tests: External client scenarios
- [ ] Security tests: Auth bypass attempts
- [ ] Performance tests: Rate limiting behavior

## Testing Strategy

### Unit Tests

**Auth Middleware:**

- Valid API token → request allowed
- Invalid API token → 401 Unauthorized
- Missing token → 401 Unauthorized
- Malformed Authorization header → 401 Unauthorized

**RBAC Middleware:**

- Token with permission → request allowed
- Token without permission → 403 Forbidden
- Admin token → all requests allowed

**Rate Limiting:**

- Within limit → requests allowed
- Exceed limit → 429 Too Many Requests
- Rate limit resets after window
- Per-token accounting

### Integration Tests

**CLI → Public Endpoint:**

- CLI successfully queries colony status
- CLI receives correct response data
- CLI handles auth failures gracefully
- CLI falls back to proxy when public endpoint unavailable

**External Client → Public Endpoint:**

- Slack bot queries colony
- GitHub Actions queries colony
- Claude Desktop MCP queries colony
- Mobile app queries colony (hypothetical)

### Security Tests

**Penetration Testing:**

- Attempt to bypass API token auth
- Attempt to escalate permissions with limited token
- Attempt to DoS with high request rate
- Attempt TLS downgrade
- Verify token values not logged

**Compliance:**

- TLS 1.3 only (no TLS 1.2 or below)
- Strong cipher suites
- HSTS header sent
- No sensitive data in logs (tokens redacted)

## Implementation Status

**Core Capability:** ⏳ Not Started

This RFD is in draft status. Implementation has not yet begun.

**Dependencies:**

- RFD 003 (Reef) already implements dual interface pattern - can reuse code
- RFD 005 (coral proxy) is implemented and provides fallback mechanism

**What's Needed:**

- Colony server changes to support dual listener
- Auth middleware implementation (can share with Reef)
- CLI updates to prefer public endpoint when available
- MCP SSE endpoint on public interface

## Future Work

The following features are out of scope for this RFD and may be addressed in
future RFDs:

**OIDC/OAuth2 for Human Users** (Separate RFD - High Priority)

- Single sign-on for human users via identity providers
- JWT-based authentication for web dashboards
- Integration with corporate identity providers (Okta, Auth0, etc.)
- Deferred to keep this RFD focused on CLI/bot access via API tokens

**Auto-TLS** (Future Enhancement)

- Integrate with Let's Encrypt / cert-manager
- Automatic cert renewal
- No manual TLS cert management

**Audit Log Export** (Future Enhancement)

- Send audit logs to external SIEM
- Compliance reporting
- Security monitoring

**IP Allowlisting** (Future Enhancement)

- Restrict public endpoint to specific IPs/CIDR ranges
- Additional security layer

## Appendix

### Design Philosophy

**Consistency:**

- Colony and Reef use the same dual-interface pattern
- Same configuration format, same security model
- Code reuse between Colony and Reef

**Progressive Enhancement:**

- Start simple: mesh-only (zero config)
- Add public endpoint when needed (opt-in)
- Add auth/RBAC as security requirements grow

**Decentralization Maintained:**

- Public endpoint is optional, not required
- Colony still runs wherever user wants (laptop, VM, K8s)
- No dependency on Coral-owned servers
- User controls access (their tokens, their TLS certs)

### When to Enable Public Endpoint

**Enable when:**

- Need CLI access without coral proxy
- External integrations (Slack, CI/CD, IDEs)
- Team collaboration (multiple developers)
- Claude Desktop MCP integration
- Mobile/web app integration

**Keep mesh-only when:**

- Ultra-secure environments (air-gapped)
- Don't need external access
- Comfortable using coral proxy
- Security policy prohibits public endpoints

### Related RFDs

- **RFD 001 (Discovery Service):** Discovery can optionally advertise public
  endpoint
- **RFD 003 (Reef Multi-Colony Federation):** Reef uses same dual interface
  pattern; code can be shared
- **RFD 004 (MCP Server Integration):** MCP server exposed on both mesh and
  public endpoint
- **RFD 005 (CLI Access via Local Proxy):** Implemented; provides fallback for
  mesh-only colonies
- **RFD 030 (coral ask Local Genkit):** Can use public endpoint instead of
  mesh/proxy
- **RFD 049 (Discovery-Based Agent Authorization):** Agent-to-colony auth uses
  referral tickets; separate from CLI auth in this RFD
