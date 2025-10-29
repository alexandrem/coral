
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
    - Use strong mesh passwords
    - Enable TLS for dashboard

**3. Agents** (Trusted - User Controls)
- Connect only to verified colony (pubkey pinning)
- Encrypted tunnel (Wireguard)
- Minimal privileges (observe only, don't control)

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
- Mutual TLS between agents and colony
- Encrypted storage at rest (SQLCipher)
- Audit logging
- RBAC for multi-user colonys
- Hardware security module (HSM) support