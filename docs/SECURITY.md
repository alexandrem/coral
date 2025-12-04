## Security Model

### Trust Boundaries

**1. Discovery Service** (Untrusted)

- Can see: mesh IDs, IP addresses, public keys
- Cannot see: application data, encrypted traffic
- Cannot: impersonate colony (no private keys)
- Risk: Could redirect to fake colony (mitigated by pubkey pinning)

**2. Colony** (Trusted - User Controls)

- Has: application data, telemetry, its own WireGuard private key
- Does NOT have: AI API keys (stored in user's local config), agent private keys
- User's responsibility to secure
- Recommendations:
    - Run on trusted infrastructure
    - Encrypt storage at rest
    - Use strong colony secrets
    - Only expose WireGuard port (not HTTP/HTTPS)
    - Optional: Enable TLS for public-facing dashboard

**3. Agents** (Trusted - User Controls)

- Connect only to verified colony (pubkey pinning)
- Encrypted tunnel (WireGuard)
- Minimal privileges (observe only, don't control)

**4. Proxy** (Trusted - User Controls)

- Peers into mesh like agents
- Bound to localhost by default (no remote access)
- Authenticates to colony with colony secret
- Enables CLI access without exposing colony HTTP ports

**5. Reef** (Trusted - User Controls) - Future Development

- Peers into multiple colony meshes (one WireGuard interface per colony)
- Same security model as proxies (WireGuard + colony secret)
- Stores aggregated data only (not raw agent data)
- Enables cross-colony federation without public endpoints

### Authentication Flow

```
1. User starts colony
   → Generates Wireguard keypair
   → Registers pubkey with discovery service

2. User starts agent with mesh ID
   → Agent contacts discovery: "Where is mesh-abc?"
   → Discovery returns: colony endpoints + pubkey

3. Agent verifies pubkey
   → Compares with expected pubkey (from initial setup)
   → If mismatch: refuses to connect

4. Agent establishes Wireguard tunnel
   → Encrypted connection to colony

5. Colony challenges agent (optional)
   → Mesh password authentication
   → Agent responds with password

6. Connected!
   → Agent can send events
   → Colony can query agent
```

#### What WireGuard Provides

**Network-Layer Encryption:**

- All traffic encrypted at network layer (ChaCha20-Poly1305 or AES-GCM)
- Encryption happens in kernel space (high performance)
- Provides secure tunnel for all mesh communication
- Additional defense layer below application-level security

**Mesh Authentication:**

- Cryptographic peer verification using WireGuard public keys
- Ensures only authorized peers can join the mesh
- Mesh IP address = verified peer identity
- Colony tracks which peer made each request

**Defense in Depth: WireGuard + TLS/mTLS**

Coral uses **both** WireGuard (network layer) and TLS/mTLS (application layer):

| Layer           | Technology       | Purpose                           |
|-----------------|------------------|-----------------------------------|
| **Network**     | WireGuard        | Mesh encryption & peer auth       |
| **Application** | TLS + mTLS       | Service identity & per-agent auth |
| **Result**      | Defense in depth | Multiple security layers ✅        |

- **Colony**: Uses TLS server certificates (issued by embedded CA)
- **Agents**: Use mTLS client certificates for per-agent identity
- **Benefits**: Network-level encryption + application-level cryptographic
  identity

#### Security Architecture

```
┌─────────────────────────────────────────────────────────┐
│  PUBLIC INTERNET (Untrusted)                            │
│                                                         │
│  Attacker with network access:                          │
│  ❌ Cannot decrypt traffic (WireGuard encryption)       │
│  ❌ Cannot impersonate peers (needs WireGuard key)      │
│  ❌ Cannot join mesh (needs colony secret)              │
│  ❌ Cannot access services (no public HTTP ports)       │
│  ❌ Cannot impersonate agents (needs mTLS cert)         │
└─────────────────────────────────────────────────────────┘
                          │
                    Only WireGuard port exposed (41820/udp)
                          │
┌─────────────────────────────────────────────────────────┐
│  WIREGUARD MESH (Encrypted Network Layer)               │
│                                                         │
│  ┌──────────┐  TLS + colony_secret    ┌──────────┐      │
│  │  Proxy   │◄───────────────────────►│ Colony   │      │
│  │10.42.0.15│                         │10.42.0.1 │      │
│  └──────────┘                         └────┬─────┘      │
│                                            │            │
│  ┌──────────┐                         ┌────▼─────┐      │
│  │ Agent 1  │◄───────────────────────►│ Agent 2  │      │
│  │10.42.0.10│   mTLS (per-agent cert) │10.42.0.11│      │
│  └──────────┘                         └──────────┘      │
│                                                         │
│  Defense in Depth:                                      │
│  - WireGuard: Network-layer encryption                  │
│  - TLS/mTLS: Application-layer identity & auth          │
│  - CA hierarchy: Root → Server/Agent Intermediates      │
└─────────────────────────────────────────────────────────┘
```

#### What You DON'T Need

Because of WireGuard mesh + embedded CA:

1. ❌ **No external Certificate Authority** - Coral generates its own CA hierarchy
2. ❌ **No purchased certificates** - All certs issued by embedded CA
3. ❌ **No manual certificate provisioning** - Agents bootstrap automatically
4. ❌ **No API keys or tokens** for agent authentication (mTLS certs instead)
5. ❌ **No service mesh** (Istio, Linkerd) - WireGuard + TLS provides security
6. ❌ **No VPN** for secure access (WireGuard IS the VPN)
7. ❌ **No complex cert rotation** - Intermediates rotate automatically
8. ❌ **No reverse proxy** (nginx, Caddy) for TLS termination
9. ❌ **No public HTTP/HTTPS ports** exposed

#### What You GET

1. ✅ **Defense in depth** - WireGuard (network) + TLS/mTLS (application)
2. ✅ **End-to-end encryption** at both network and application layers
3. ✅ **Per-agent identity** via mTLS client certificates
4. ✅ **Automatic certificate bootstrap** (zero-touch agent provisioning)
5. ✅ **Embedded CA hierarchy** (Root → Server/Agent Intermediates)
6. ✅ **Certificate-based authentication** (no shared secrets for agents)
7. ✅ **Individual agent revocation** (granular access control)
8. ✅ **Audit trail** (mesh IP + certificate identity)
9. ✅ **Simplified operations** (automatic cert rotation, no external CA)
10. ✅ **Air-gap compatibility** (self-contained CA, no external dependencies)

#### CLI Access Security

The local proxy extends this security model to CLI tools:

```
Developer's Machine:
  coral CLI → localhost:8000 (proxy) → [WireGuard tunnel] → Colony

Security Properties:
  ✅ Proxy bound to localhost (only local processes)
  ✅ Proxy authenticates to colony (colony secret + WireGuard)
  ✅ All CLI traffic encrypted (WireGuard tunnel)
  ✅ No credentials in CLI (security via proxy)
  ✅ Mesh IP tracking (colony knows which proxy made requests)
```

#### TLS/mTLS Architecture

**Coral uses TLS/mTLS for all control plane communication**, providing defense
in depth with WireGuard:

**Embedded Certificate Authority:**

- **Root CA** (10-year validity): Root of trust for the colony
- **Server Intermediate** (1-year): Issues Colony's TLS server certificates
- **Agent Intermediate** (1-year): Issues agent mTLS client certificates
- **Policy Signing Certificate** (10-year): Signs authorization policies

**Why Both WireGuard AND TLS/mTLS:**

- **WireGuard**: Network-layer encryption, mesh peer authentication
- **TLS**: Server identity verification, CA fingerprint validation
- **mTLS**: Per-agent cryptographic identity, individual revocation
- **Result**: Defense in depth - compromise of one layer doesn't break security

**Certificate-Based Authentication:**

- Agents use mTLS client certificates (no shared secrets)
- Automatic certificate bootstrap with Root CA fingerprint validation
- Per-agent identity enables fine-grained access control
- Individual certificate revocation (without affecting other agents)
- 90-day certificate validity with automatic renewal

**Public-Facing Components:**

- Web dashboard (if exposed outside mesh)
- Public APIs (future feature)
- Third-party integrations

### Unified WireGuard Architecture

**All control plane components use WireGuard mesh with TLS/mTLS:**

```
Component         Network Access           Authentication
──────────────────────────────────────────────────────────────────
Agents        →   WireGuard mesh peer  →  mTLS client certificates
Proxies       →   WireGuard mesh peer  →  TLS + colony_secret
Reef          →   WireGuard mesh peer  →  TLS + colony_secret
                  (multiple meshes)

Web Dashboard →   HTTPS (TLS required) →  TLS + User auth
                  (browser requirement)
```

**Benefits of Unified Architecture:**

1. **Defense in depth** - WireGuard (network) + TLS/mTLS (application) layers
2. **Per-agent identity** - mTLS certificates enable fine-grained access control
3. **Automatic provisioning** - Agents bootstrap certificates automatically
4. **Consistent audit trail** - Mesh IP + certificate identity
5. **Simplified operations** - Embedded CA, automatic cert rotation
6. **Air-gap compatible** - Self-contained CA, no external dependencies
7. **Performance** - Kernel-space WireGuard + efficient TLS

**Component Isolation:**

```
Colony Production (Mesh: 10.42.0.0/16)
  ├─ Agents: 10.42.0.10-10.42.0.50
  ├─ Proxy: 10.42.0.100
  └─ Reef: 10.42.0.200

Colony Staging (Mesh: 10.43.0.0/16)  ← Completely isolated
  ├─ Agents: 10.43.0.10-10.43.0.50
  ├─ Proxy: 10.43.0.100
  └─ Reef: 10.43.0.200

Reef peers into both meshes (separate WireGuard interfaces)
Compromising one mesh does NOT affect the other
```

### AI Security Model (`coral ask`)

The `coral ask` command uses a **client-side AI architecture** where the LLM
agent runs on your local machine, not on the colony.

**Architecture:**

```
Developer's Machine:
  ┌─────────────────────────────────────────────────┐
  │ coral ask "Why is service slow?"                │
  │   ├─ AI API Keys (local config)                 │
  │   ├─ LLM Agent (Google Gemini SDK)              │
  │   └─ MCP Client                                 │
  └──────────────────┬──────────────────────────────┘
                     │ WireGuard tunnel
                     │ (encrypted)
                     ▼
  ┌─────────────────────────────────────────────────┐
  │ Colony (MCP Server)                             │
  │   ├─ Exposes MCP tools                          │
  │   ├─ Returns observability data                 │
  │   └─ NO AI API keys stored                      │
  └─────────────────────────────────────────────────┘
```

**Security Properties:**

1. **API Keys Stay Local**: AI API keys are referenced in `~/.coral/config.yaml`
   on your machine, never sent to colony
2. **Colony Has No AI Access**: Colony only exposes MCP tools, doesn't call AI
   APIs
3. **Data Minimization**: Only requested observability data is sent to AI
   provider
4. **User Control**: You control which AI provider and model to use
5. **Encrypted Transit**: All data flows through WireGuard tunnel

**Configuration:**

```yaml
# ~/.coral/config.yaml (local machine only)
ai:
    ask:
        default_model: "google:gemini-2.0-flash-exp"
        api_keys:
            google: "env://GOOGLE_API_KEY"  # Environment variable reference
```

**Threat Model:**

- ✅ **Colony compromise**: Doesn't expose AI API keys (not stored there)
- ✅ **Network eavesdropping**: WireGuard encrypts all MCP traffic
- ⚠️ **AI provider trust**: You trust Google/OpenAI with observability data
- ⚠️ **Local machine compromise**: Attacker could access API keys from config

**Best Practices:**

1. Use environment variables for API keys (never hardcode)
2. Rotate API keys regularly
3. Use least-privilege API keys (read-only if possible)
4. Review what data is sent to AI providers
5. Consider using local models (Ollama) for sensitive environments

---

### Threat Model

**What we protect against:**

- ✅ Eavesdropping (WireGuard + TLS encryption)
- ✅ MITM attacks (CA fingerprint validation, public key pinning)
- ✅ Unauthorized agent access (mTLS client certificates)
- ✅ Agent impersonation (per-agent cryptographic identity)
- ✅ Unauthorized proxy/reef access (colony_secret)
- ✅ Data leakage (all data stays on colony)

**What we DON'T protect against:**

- ❌ Compromised colony (user's responsibility)
- ❌ Compromised agent (physical access to server)
- ❌ Malicious user with valid credentials
- ❌ Malicious discovery service (mitigated by pubkey pinning)

**Future Security Features:**

- Encrypted storage at rest (SQLCipher for DuckDB)
- Comprehensive audit logging with certificate identity attribution
- RBAC for multi-user colonies (per-user permissions)
- Certificate-based authentication for proxies and reefs
- Token-based authentication for remote proxy access
- Certificate pinning for discovery service (HTTPS)
- Agent-side data encryption (encrypt before sending to colony)
- Secrets management integration (Vault, AWS Secrets Manager)
