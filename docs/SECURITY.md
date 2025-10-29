## Security Model

### Trust Boundaries

**1. Discovery Service** (Untrusted)

- Can see: mesh IDs, IP addresses, public keys
- Cannot see: application data, encrypted traffic
- Cannot: impersonate colony (no private keys)
- Risk: Could redirect to fake colony (mitigated by pubkey pinning)

**2. Colony** (Trusted - User Controls)

- Has: all application data, AI API keys, private keys
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

**4. Proxy** (Trusted - User Controls, RFD 005)

- Peers into mesh like agents
- Bound to localhost by default (no remote access)
- Authenticates to colony with colony secret
- Enables CLI access without exposing colony HTTP ports

**5. Reef** (Trusted - User Controls, RFD 003)

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

### Network-Layer Security: Why We Don't Need TLS

Coral's WireGuard mesh architecture provides **network-layer encryption and
authentication**, eliminating the need for traditional TLS certificates and API
tokens for control plane communication.

#### What WireGuard Provides

**Encryption (Instead of TLS):**

- All traffic encrypted at network layer (ChaCha20-Poly1305 or AES-GCM)
- Encryption happens in kernel space (high performance)
- No TLS handshake overhead
- No certificate authority or cert rotation needed
- Services use plain HTTP/2 inside the encrypted tunnel

**Authentication (Instead of API Tokens):**

- Cryptographic peer verification using public keys
- Mutual authentication (both peers verify each other)
- No credentials to steal or rotate
- Mesh IP address = authenticated identity
- Colony tracks which peer made each request

**Benefits Over Traditional HTTPS:**

| Aspect              | Traditional HTTPS    | WireGuard Mesh                |
|---------------------|----------------------|-------------------------------|
| **Encryption**      | TLS 1.3              | WireGuard (ChaCha20) ✅ Faster |
| **Authentication**  | API tokens/JWT       | Public key crypto ✅ Simpler   |
| **Cert management** | CA, rotation, expiry | None needed ✅                 |
| **Attack surface**  | Public HTTPS port    | Private mesh only ✅           |
| **Configuration**   | Per-service certs    | Per-peer keys ✅               |
| **Performance**     | TLS overhead         | Minimal (kernel space) ✅      |

#### Security Architecture

```
┌─────────────────────────────────────────────────────────┐
│  PUBLIC INTERNET (Untrusted)                            │
│                                                          │
│  Attacker with network access:                          │
│  ❌ Cannot decrypt traffic (WireGuard encryption)       │
│  ❌ Cannot impersonate peers (needs private key)        │
│  ❌ Cannot join mesh (needs colony secret)              │
│  ❌ Cannot access services (no public HTTP ports)       │
└─────────────────────────────────────────────────────────┘
                          │
                    Only WireGuard port exposed (41820/udp)
                          │
┌─────────────────────────────────────────────────────────┐
│  WIREGUARD MESH (Encrypted Network Layer)               │
│                                                         │
│  ┌──────────┐    Plain HTTP/2    ┌──────────┐           │
│  │  Proxy   │◄──────────────────►│ Colony   │           │
│  │10.42.0.15│                    │10.42.0.1 │           │
│  └──────────┘                    └────┬─────┘           │
│                                       │                 │
│  ┌──────────┐                    ┌────▼─────┐           │
│  │ Agent 1  │◄──────────────────►│ Agent 2  │           │
│  │10.42.0.10│    Plain HTTP/2    │10.42.0.11│           │
│  └──────────┘                    └──────────┘           │
│                                                         │
│  Inside mesh: All traffic already encrypted             │
│  No TLS needed for service-to-service communication     │
└─────────────────────────────────────────────────────────┘
```

#### What You DON'T Need

Because of WireGuard mesh:

1. ❌ **No TLS certificates** for colony/agent/proxy communication
2. ❌ **No API keys or tokens** for control plane requests
3. ❌ **No service mesh** (Istio, Linkerd) for encryption
4. ❌ **No VPN** for secure access (WireGuard IS the VPN)
5. ❌ **No certificate rotation** automation
6. ❌ **No reverse proxy** (nginx, Caddy) for TLS termination
7. ❌ **No public HTTP/HTTPS ports** exposed

#### What You GET

1. ✅ **End-to-end encryption** at network layer
2. ✅ **Mutual authentication** via public key cryptography
3. ✅ **Zero-config security** (WireGuard handles it automatically)
4. ✅ **Audit trail** (mesh IP = authenticated peer identity)
5. ✅ **Performance** (no TLS handshakes, kernel-space crypto)
6. ✅ **Simplicity** (fewer security components to manage)
7. ✅ **Air-gap compatibility** (no external CA required)

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

**Remote Access Pattern:**

```bash
# SSH tunnel provides secure access (no TLS needed)
ssh -L 8000:localhost:8000 production-server

# Now local CLI connects through tunnel
coral status  # Encrypted via SSH + WireGuard
```

#### Defense in Depth: Optional TLS

**WireGuard provides sufficient security for control plane communication.**
However, you may add TLS for:

**Compliance Reasons:**

- Regulatory requirements for TLS everywhere
- Security audits requiring application-layer encryption
- Corporate policies mandating mTLS

**Defense in Depth:**

- Extra layer if WireGuard ever compromised
- Application-layer authentication logs
- Protocol-level security controls

**Public-Facing Components:**

- Web dashboard (if exposed outside mesh)
- Public APIs (future feature)
- Third-party integrations

**Implementation Note:** If adding TLS, use it for specific components (e.g.,
dashboard), not for agent↔colony control plane where WireGuard already provides
superior security.

### Unified WireGuard Architecture

**All control plane components use WireGuard mesh:**

```
Component         Network Access           Authentication
──────────────────────────────────────────────────────────
Agents        →   WireGuard mesh peer  →  colony_secret
Proxies       →   WireGuard mesh peer  →  colony_secret
Reef          →   WireGuard mesh peer  →  colony_secret
                  (multiple meshes)

Web Dashboard →   HTTPS (TLS required) →  User auth
                  (browser requirement)
```

**Benefits of Unified Architecture:**

1. **Single security model** - All components use same WireGuard + colony_secret
   pattern
2. **No TLS complexity** - Zero certificate management for control plane
3. **Consistent audit trail** - Mesh IP = authenticated peer identity
4. **Simpler operations** - One pattern to understand and deploy
5. **Air-gap compatible** - No external dependencies or CAs
6. **Performance** - Kernel-space encryption, no TLS handshakes

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

### Threat Model

**What we protect against:**

- ✅ Eavesdropping (Wireguard encryption)
- ✅ MITM attacks (public key pinning)
- ✅ Unauthorized access (mesh passwords)
- ✅ Data leakage (all data stays on colony)

**What we DON'T protect against:**

- ❌ Compromised colony (user's responsibility)
- ❌ Compromised agent (physical access to server)
- ❌ Malicious user with valid credentials
- ❌ Side-channel attacks on AI API calls

**Future Security Features:**

- Encrypted storage at rest (SQLCipher for DuckDB)
- Comprehensive audit logging with mesh IP attribution
- RBAC for multi-user colonies (per-user permissions)
- Hardware security module (HSM) support for AI API keys
- Optional TLS for compliance/defense-in-depth (not required)
- Token-based authentication for remote proxy access
- Certificate pinning for discovery service (HTTPS)
