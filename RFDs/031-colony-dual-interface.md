---
rfd: "031"
title: "Colony Dual Interface (Mesh + Optional Public Endpoint)"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: ["001", "002", "003", "005"]
database_migrations: []
areas: ["networking", "security", "cli", "architecture"]
---

# RFD 031 - Colony Dual Interface (Mesh + Optional Public Endpoint)

**Status:** 🚧 Draft

## Summary

Colony should support optional public HTTPS endpoints in addition to its private WireGuard mesh interface, matching Reef's dual-interface pattern (RFD 003). This provides a consistent architecture, simplifies CLI access, and enables external integrations (Slack bots, CI/CD, IDEs) for single-colony users without requiring the `coral proxy` workaround (RFD 005).

## Problem

### Current State

RFD 005 introduced `coral proxy` as a solution for CLI access to colonies over the WireGuard mesh:

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
- **Integrations:** Single-colony users deserve the same integration capabilities as multi-colony (Reef) users
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

**Key Principle:** Public endpoint is **optional** and **opt-in**. Colony remains fully functional with just the mesh interface. This maintains decentralization - no dependency on public endpoints if not needed.

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

**Decision:** When public endpoint is enabled, CLI can connect directly. `coral proxy` becomes optional fallback for special cases.

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

#### 4. Same Security Model as Reef

**Decision:** Reuse Reef's auth/RBAC model (API tokens, JWT, mTLS).

**Rationale:**
- Proven design (RFD 003)
- Consistent security model
- Same token management

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

  # Authentication
  auth:
    # API tokens (for bots, CI/CD)
    api_tokens:
      - token_id: ci-cd
        permissions: [status, query, analyze]
        rate_limit: 100/hour
      - token_id: slack-bot
        permissions: [analyze, query]
        rate_limit: 50/hour

    # JWT (for human users)
    jwt:
      enabled: true
      issuer: https://colony.mycompany.com
      audience: coral-colony
      # Verify with JWKS endpoint or shared secret

    # mTLS (for service-to-service)
    mtls:
      enabled: false
      client_ca: /etc/coral/ca.pem

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
      type: jwt
      token_file: ~/.coral/tokens/prod.jwt

  my-app-staging:
    # Mesh-only (uses coral proxy fallback)
    endpoint: mesh://10.43.0.1:9000
    auth:
      type: colony_secret
      secret_file: ~/.coral/colonies/my-app-staging.yaml
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
- name: Check Colony Health
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
    body: JSON.stringify({ question }),
  });

  return response.json();
}
```

## API Changes

### Colony Service Extensions

**File: `proto/coral/colony/v1/colony.proto`**

No changes required - existing ColonyService methods work over both mesh and public endpoint.

The dual interface is a **transport-level decision**, not a protocol change. Same Buf Connect RPC, two different listeners:
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
2. **Authentication:** Verify caller identity
   - API tokens (bots, CI/CD)
   - JWT (human users)
   - mTLS (service-to-service)
3. **Authorization (RBAC):** Check permissions
   - `status`: Read colony status
   - `query`: Query metrics/traces
   - `analyze`: AI analysis (may trigger shell/exec, probes)
   - `debug`: Attach live probes
   - `admin`: Change colony config
4. **Rate Limiting:** Prevent abuse
5. **Audit Logging:** Track who did what

**Comparison:**

| Aspect | Mesh Interface | Public Endpoint |
|--------|----------------|-----------------|
| **Encryption** | WireGuard | TLS 1.3 |
| **Auth** | Colony secret | API token/JWT/mTLS |
| **Access** | Mesh peers only | Network-reachable |
| **Rate Limiting** | No (trusted mesh) | Yes (per token) |
| **Audit** | Optional | Recommended |

### Default Configuration (Secure by Default)

```yaml
public_endpoint:
  enabled: false  # Must opt-in

  # When enabled:
  host: 127.0.0.1  # Default to localhost-only (no network exposure)

  auth:
    require: true  # Always require auth (no anonymous access)

    # No default tokens (user must create)
    api_tokens: []
```

**Progressive Enhancement:**

1. **Local dev:** `host: 127.0.0.1` (localhost-only, no TLS needed)
2. **Remote access:** `host: 0.0.0.0` + TLS + API tokens
3. **Enterprise:** Add JWT + RBAC + audit logging

## Use Cases

### 1. Local Development (Localhost Only)

```yaml
public_endpoint:
  enabled: true
  host: 127.0.0.1  # localhost-only
  port: 8443

  auth:
    api_tokens:
      - token_id: dev
        permissions: [status, query, analyze, debug, admin]
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
      - token_id: alice-dev
        permissions: [status, query, analyze, debug]
      - token_id: bob-dev
        permissions: [status, query, analyze]
```

**Benefit:** Team members query colony from anywhere, no VPN/SSH needed.

### 3. CI/CD Integration

```yaml
public_endpoint:
  auth:
    api_tokens:
      - token_id: github-actions
        permissions: [status, query]
        rate_limit: 100/hour
      - token_id: deploy-pipeline
        permissions: [status, query, analyze]
        rate_limit: 50/hour
```

**Benefit:** GitHub Actions checks colony health before deployment.

### 4. Slack Bot (Single Colony)

```yaml
public_endpoint:
  auth:
    api_tokens:
      - token_id: slack-bot
        permissions: [status, query, analyze]
        rate_limit: 100/hour
```

**Benefit:** Slack bot queries dev colony, no Reef needed for single-colony use case.

### 5. Claude Desktop Integration

```yaml
public_endpoint:
  mcp:
    enabled: true
    transport: sse
    path: /mcp/sse

  auth:
    api_tokens:
      - token_id: claude-desktop
        permissions: [status, query, analyze, debug]
```

**Benefit:** Claude Desktop queries local dev colony while coding.

## Comparison: Dual Interface vs coral proxy

| Aspect | RFD 005 (coral proxy) | RFD 031 (Dual Interface) |
|--------|----------------------|--------------------------|
| **Architecture** | CLI → proxy → mesh → Colony | CLI → HTTPS → Colony |
| **Deployment** | Separate proxy process | Built into Colony |
| **Performance** | Extra hop (proxy) | Direct connection |
| **External integrations** | Requires ngrok/tunnel workaround | Native HTTPS endpoint |
| **Consistency** | Colony ≠ Reef | Colony = Reef |
| **Configuration** | Proxy config + colony config | Single colony config |
| **Local dev** | Proxy on localhost:8000 | Colony on localhost:8443 |
| **Remote access** | Proxy per colony | Direct HTTPS |
| **Security** | Localhost-only proxy | TLS + auth on public endpoint |

**When to still use coral proxy:**
- Colony has no public endpoint (mesh-only)
- Need to tunnel through restricted network
- Development/testing specific proxy behavior

**When to use dual interface:**
- Enable external integrations
- Simplify CLI access
- Consistent with Reef architecture
- Simpler deployment (one process)

## Migration from RFD 005

### Phase 1: Add Public Endpoint Support to Colony

- [ ] Implement dual listener (mesh + public HTTPS)
- [ ] Add auth middleware (API tokens, JWT, mTLS)
- [ ] Add RBAC enforcement
- [ ] Add rate limiting
- [ ] Add MCP SSE endpoint

### Phase 2: Update CLI to Prefer Public Endpoint

- [ ] CLI checks for `public_endpoint.enabled` in colony config
- [ ] If public endpoint available: Direct HTTPS
- [ ] If mesh-only: Fallback to coral proxy (RFD 005 behavior)
- [ ] Clear error messages guide users

### Phase 3: Update Documentation

- [ ] Document dual interface pattern
- [ ] Update CLI examples to use public endpoint
- [ ] Show external integration examples
- [ ] Mark RFD 005 as "Partially superseded by RFD 031"

### Phase 4: Deprecation (Future)

- [ ] Monitor coral proxy usage
- [ ] Consider deprecating proxy for common cases
- [ ] Keep proxy for special cases (mesh-only colonies)

**Note:** RFD 005 is **not fully superseded** - coral proxy remains useful for mesh-only colonies and special networking scenarios. But most users will use dual interface.

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
  - [ ] API token verification
  - [ ] JWT verification (optional)
  - [ ] mTLS verification (optional)
  - [ ] RBAC permission checks
  - [ ] Token management (create, revoke, list)
- [ ] Add rate limiting
  - [ ] Per-token rate limits
  - [ ] Sliding window algorithm
  - [ ] Return HTTP 429 on exceed
- [ ] Add audit logging
  - [ ] Log authenticated requests
  - [ ] Include: timestamp, token_id, method, source IP
  - [ ] Optional: send to external log aggregator

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
- Expired JWT → 401 Unauthorized
- Valid JWT with wrong audience → 403 Forbidden

**RBAC Middleware:**
- Token with permission → request allowed
- Token without permission → 403 Forbidden
- Admin token → all requests allowed
- Verify permission inheritance

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
- Attempt to bypass auth
- Attempt to escalate permissions
- Attempt to DoS with high request rate
- Attempt TLS downgrade
- Verify token leakage doesn't expose sensitive data

**Compliance:**
- TLS 1.3 only (no TLS 1.2 or below)
- Strong cipher suites
- HSTS header sent
- No sensitive data in logs

## Relationship to Other RFDs

**RFD 001 (Discovery Service):**
- Discovery can optionally advertise public endpoint
- Clients can discover colony via public endpoint or mesh IP

**RFD 003 (Reef Multi-Colony Federation):**
- **Reef uses same dual interface pattern**
- Colony and Reef share auth/RBAC implementation
- Consistent architecture across tiers

**RFD 004 (MCP Server Integration):**
- MCP server exposed on both mesh and public endpoint
- Public endpoint enables Claude Desktop without mesh access

**RFD 005 (CLI Access via Local Proxy):**
- **Partially superseded by RFD 031**
- coral proxy remains useful for mesh-only colonies
- Most users will use public endpoint instead
- Proxy becomes opt-in fallback

**RFD 030 (coral ask Local Genkit):**
- `coral ask` connects to Colony's MCP server
- Can use public endpoint instead of mesh/proxy
- Simpler configuration

## Notes

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

**Developer Experience:**
- Local dev: Enable public endpoint on localhost (no TLS needed)
- Remote access: Enable public endpoint with TLS + tokens
- CLI "just works" without proxy setup

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

### Future Enhancements

1. **Auto-TLS:**
   - Integrate with Let's Encrypt / cert-manager
   - Automatic cert renewal
   - No manual TLS cert management

2. **OAuth2/OIDC Integration:**
   - Single sign-on for human users
   - Integrate with corporate identity providers
   - More sophisticated user management

3. **WebAuthn Support:**
   - Hardware key authentication
   - Biometric authentication
   - Phishing-resistant auth

4. **Audit Log Export:**
   - Send audit logs to external SIEM
   - Compliance reporting
   - Security monitoring

5. **IP Allowlisting:**
   - Restrict public endpoint to specific IPs/CIDR ranges
   - Additional security layer

## Conclusion

Colony's dual-interface pattern (mesh + optional public endpoint) provides:

1. **Architectural consistency** with Reef
2. **Simpler CLI access** (no proxy needed)
3. **External integration support** for single-colony users
4. **Progressive enhancement** (opt-in, not required)
5. **Maintains decentralization** (public endpoint is optional)

This supersedes parts of RFD 005 (coral proxy) while keeping proxy as a valid fallback for mesh-only colonies and special networking scenarios.
