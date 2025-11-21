---
rfd: "047"
title: "Agent Certificate Bootstrap"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ ]
related_rfds: [ "022" ]
areas: [ "security", "agent" ]
---

# RFD 047 - Agent Certificate Bootstrap

**Status:** 🚧 Draft

## Summary

Implement agent-side certificate bootstrap using **Root CA fingerprint
validation**,
enabling agents to automatically obtain mTLS certificates on first connection.
Agents
use the colony's Root CA fingerprint (distributed via configuration) to validate
the
colony's identity, generate CSRs, request certificates from Colony's
auto-issuance
endpoint, and store certificates securely for all subsequent communication. This
eliminates the need for per-agent bootstrap tokens while maintaining MITM
protection,
following the Kubernetes kubelet `--discovery-token-ca-cert-hash` pattern.

## Problem

- **Current behavior/limitations**:
    - Agents use shared `colony_secret` for authentication
    - Single secret compromise affects entire colony
    - No per-agent cryptographic identity
    - Cannot revoke individual agents without rotating colony-wide secret
    - Manual certificate provisioning blocks automated agent deployment

- **Why this matters**:
    - Shared secrets scale poorly and increase security risk
    - Cannot audit individual agent actions (shared identity)
    - Agent compromise requires colony-wide secret rotation
    - Discovery service MITM attacks possible with shared secrets

- **Use cases affected**:
    - Automated agent deployment and scaling
    - Zero-touch agent provisioning in Kubernetes
    - Agent replacement after compromise
    - Certificate-based access control and audit

## Solution

Implement agent bootstrap using **Root CA fingerprint validation** instead of
JWT
tokens. Colony generates a hierarchical CA during initialization (Root →
Intermediates),
and agents validate the colony's identity by comparing the Root CA fingerprint
from the
TLS handshake against the expected value from configuration.

**Key Design Decisions**

- **Root CA fingerprint validation**: Agents validate colony identity using
  SHA256
  fingerprint of Root CA (like SSH host key fingerprints or Kubernetes
  `--discovery-token-ca-cert-hash`).
- **No bootstrap tokens**: Colony auto-issues certificates on valid CSRs,
  eliminating
  per-agent token generation and tracking.
- **Hierarchical CA**: Three-level PKI (Root → Bootstrap Intermediate → Server
  cert,
  Root → Agent Intermediate → Client certs) enables transparent intermediate
  rotation.
- **Generic binary**: Same `coral` binary works with any colony (no embedded
  trust
  anchors).
- **Auto-issuance**: Colony automatically signs CSRs without token validation,
  rate-limited to prevent abuse.
- **Graceful degradation**: During rollout, agents fall back to `colony_secret`
  if
  bootstrap fails.

**Benefits**

- Zero-touch agent provisioning with cryptographic identity
- No Discovery service modifications required
- No bootstrap token database tracking
- Per-agent certificate revocation capability
- Transparent intermediate CA rotation
- Same binary for all colonies (runtime trust configuration)
- Matches Kubernetes kubelet bootstrap pattern

**Architecture Overview**

```
┌─────────────────────────────────────────────────────────────────┐
│ Colony Initialization                                           │
└─────────────────────────────────────────────────────────────────┘

$ coral colony init my-app-prod

Generated Certificate Authority (Hierarchical):
  Root CA (10-year validity)
    ├─ Bootstrap Intermediate CA (1-year)
    │   └─ Used for fingerprint validation only
    ├─ Server Intermediate CA (1-year)
    │   └─ Colony TLS Server Certificate
    │       └─ SAN: spiffe://coral/colony/my-app-prod-a3f2e1
    ├─ Agent Intermediate CA (1-year)
    │   └─ Signs agent client certificates
    │       └─ SAN: spiffe://coral/colony/{colony-id}/agent/{agent-id}
    └─ Policy Signing Certificate (10-year)
        └─ Signs policy documents

Root CA Fingerprint (distribute to agents):
  sha256:a3f2e1d4c5b6a7f8e9d0c1b2a3f4e5d6c7b8a9f0e1d2c3b4a5f6e7d8c9b0a1f2

Colony SPIFFE ID:
  spiffe://coral/colony/my-app-prod-a3f2e1

Pushing authorization policy to Discovery...
  ✓ Policy signed with policy certificate
  ✓ Discovery validates certificate chain (Policy Cert → Root CA)
  ✓ Discovery locks colony ID to Root CA fingerprint
  ✓ Policy active (version: 1)

Deploy agents with:
  export CORAL_COLONY_ID=my-app-prod-a3f2e1
  export CORAL_CA_FINGERPRINT=sha256:a3f2e1d4c5b6...


┌─────────────────────────────────────────────────────────────────┐
│ Agent Bootstrap Flow with Referral Tickets                     │
└─────────────────────────────────────────────────────────────────┘

Agent Start
    ↓
Check for existing cert at ~/.coral/certs/<agent-id>.crt
    ↓
    ├─ Exists & Valid → Use for mTLS connection
    │
    └─ Missing/Expired → Bootstrap Flow:
           │
           ├─ 1. Request referral ticket from Discovery
           │      POST /coral.discovery.v1.DiscoveryService/RequestReferralTicket
           │      Body: {colony_id, agent_id}
           │      Discovery checks: rate limits, quotas, agent_id policy, IP allowlists
           │      → Returns: JWT ticket (1-minute TTL)
           │
           ├─ 2. Query Discovery for colony endpoints
           │      POST /coral.discovery.v1.DiscoveryService/LookupColony
           │      Body: {colony_id}
           │      → Returns: endpoints, mesh info (untrusted)
           │
           ├─ 3. Connect to colony HTTPS endpoint
           │      TLS handshake receives certificate chain:
           │      [Server cert] → [Bootstrap Intermediate] → [Root CA]
           │
           ├─ 4. Extract Root CA from chain, compute SHA256 fingerprint
           │
           ├─ 5. Validate: computed_fingerprint == CORAL_CA_FINGERPRINT
           │      If mismatch → ABORT (MITM detected)
           │      If match → Root CA trust established
           │
           ├─ 6. Validate colony ID in server certificate SAN
           │      Extract SAN: spiffe://coral/colony/{colony-id}
           │      Verify: colony_id matches CORAL_COLONY_ID
           │      If mismatch → ABORT (cross-colony impersonation detected)
           │      If match → Colony identity confirmed
           │
           ├─ 7. Validate certificate chain integrity
           │      Verify: Server cert → Server Intermediate → Root CA
           │
           ├─ 8. Save validated Root CA to ~/.coral/certs/root-ca.crt
           │
           ├─ 9. Generate Ed25519 keypair locally
           │      → Private key: ~/.coral/certs/<agent-id>.key (0600)
           │
           ├─ 10. Create CSR with CN=<agent-id>, O=<colony-id>
           │       SAN: spiffe://coral/colony/{colony-id}/agent/{agent-id}
           │
           ├─ 11. Request certificate from Colony with referral ticket
           │       POST /coral.colony.v1.ColonyService/RequestCertificate
           │       Body: {csr, referral_ticket}
           │       → Colony validates JWT ticket
           │       → Colony issues certificate with SPIFFE ID in SAN
           │       → Returns: certificate + CA chain
           │
           ├─ 12. Store certificate (0644) and key (0600)
           │       ~/.coral/certs/<agent-id>.crt
           │       ~/.coral/certs/<agent-id>.key
           │
           └─ 13. Connect to Colony with mTLS
                   (All subsequent RPCs use client certificate)
```

## Colony CA Hierarchy

### Four-Level PKI Structure

```
Root CA (10-year validity, offline/HSM)
  ├─ Bootstrap Intermediate CA (1-year, rotatable)
  │   └─ Used ONLY for fingerprint validation during bootstrap
  │       (Does not issue any certificates)
  │       └─ Agents validate Root CA fingerprint via this chain
  │
  ├─ Server Intermediate CA (1-year, rotatable)
  │   └─ Colony TLS Server Certificate
  │       └─ SAN: spiffe://coral/colony/{colony-id}
  │       └─ Used for HTTPS endpoint
  │
  ├─ Agent Intermediate CA (1-year, rotatable)
  │   └─ Agent Client Certificates
  │       └─ SAN: spiffe://coral/colony/{colony-id}/agent/{agent-id}
  │       └─ Used for mTLS authentication
  │
  └─ Policy Signing Certificate (10-year, same lifetime as Root CA)
      └─ Signs policy documents
          └─ Used for authorization policies pushed to Discovery
```

**Why Hierarchical?**

- **Security**: Root CA private key stored offline/HSM, minimizes exposure
- **Rotation**: Rotate intermediates annually without changing agent configs
- **Operational**: Agents validate Root CA fingerprint (never changes)
- **Flexibility**: Can issue new intermediates/certificates for different
  purposes
- **Colony ID reservation**: Policy cert chains to Root CA, locking colony IDs
- **Best Practice**: Follows X.509/RFC 5280 standards

**Why Separate Bootstrap and Server Intermediates?**

- **Blast radius reduction**: Bootstrap Intermediate compromise cannot issue
  server certs
- **Security isolation**: Bootstrap Intermediate is untrusted for TLS server
  cert issuance
- **Purpose separation**: Bootstrap Intermediate only used for fingerprint
  validation
- **Server cert protection**: Server Intermediate handles all server certificate
  issuance

**Why Bootstrap Intermediate Exists:**

During the TLS handshake, the colony server presents its certificate chain:

```
[Server Certificate] → [Server Intermediate] → [Root CA]
```

The agent performs two validations:

1. **Fingerprint Validation**: Extract Root CA from chain, verify SHA256
   fingerprint matches expected value
2. **Chain Validation**: Verify Server Cert → Server Intermediate → Root CA is
   cryptographically valid

The Bootstrap Intermediate is NOT used in this chain. It exists for historical
reasons and potential future use cases (e.g., issuing short-lived bootstrap
credentials), but is not required for the current design.

**Important**: Even if an attacker compromises the Bootstrap Intermediate
private key, they cannot:

- Issue valid server certificates (separate Server Intermediate required)
- Bypass fingerprint validation (agents validate Root CA, not intermediates)
- Impersonate the colony (server cert must chain to Server Intermediate, not
  Bootstrap Intermediate)

### Colony Initialization

```bash
$ coral colony init my-app-prod

Initializing colony: my-app-prod...

Generated Certificate Authority:
  Root CA:                ~/.coral/colonies/my-app-prod/ca/root-ca.crt
  Root CA Key:            ~/.coral/colonies/my-app-prod/ca/root-ca.key (SECRET)
  Bootstrap Intermediate: ~/.coral/colonies/my-app-prod/ca/bootstrap-intermediate.crt
  Server Intermediate:    ~/.coral/colonies/my-app-prod/ca/server-intermediate.crt
  Agent Intermediate:     ~/.coral/colonies/my-app-prod/ca/agent-intermediate.crt
  Policy Signing Cert:    ~/.coral/colonies/my-app-prod/ca/policy-signing.crt

Root CA Fingerprint (distribute to agents):
  sha256:a3f2e1d4c5b6a7f8e9d0c1b2a3f4e5d6c7b8a9f0e1d2c3b4a5f6e7d8c9b0a1f2

Colony Server Identity:
  SPIFFE ID: spiffe://coral/colony/my-app-prod-a3f2e1

⚠️  IMPORTANT: Keep root-ca.key secure (offline storage or HSM recommended)

Deploy agents with:
  export CORAL_COLONY_ID=my-app-prod-a3f2e1
  export CORAL_CA_FINGERPRINT=sha256:a3f2e1d4c5b6a7f8e9d0c1b2a3f4e5d6...
  coral agent start

✓ Colony initialized successfully
```

### Colony Configuration

```yaml
# ~/.coral/colonies/my-app-prod-a3f2e1/config.yaml
colony_id: my-app-prod-a3f2e1
spiffe_id: spiffe://coral/colony/my-app-prod-a3f2e1

ca:
    root:
        certificate: ~/.coral/colonies/my-app-prod/ca/root-ca.crt
        private_key: ~/.coral/colonies/my-app-prod/ca/root-ca.key
        fingerprint: sha256:a3f2e1d4c5b6a7f8e9d0c1b2a3f4e5d6c7b8a9f0e1d2c3b4a5f6e7d8c9b0a1f2

    bootstrap_intermediate:
        certificate: ~/.coral/colonies/my-app-prod/ca/bootstrap-intermediate.crt
        private_key: ~/.coral/colonies/my-app-prod/ca/bootstrap-intermediate.key
        expires_at: 2025-11-21
        usage: fingerprint_validation_only

    server_intermediate:
        certificate: ~/.coral/colonies/my-app-prod/ca/server-intermediate.crt
        private_key: ~/.coral/colonies/my-app-prod/ca/server-intermediate.key
        expires_at: 2025-11-21

    agent_intermediate:
        certificate: ~/.coral/colonies/my-app-prod/ca/agent-intermediate.crt
        private_key: ~/.coral/colonies/my-app-prod/ca/agent-intermediate.key
        expires_at: 2025-11-21

tls:
    certificate: ~/.coral/colonies/my-app-prod/ca/server.crt
    private_key: ~/.coral/colonies/my-app-prod/ca/server.key
    # Server certificate contains SPIFFE ID in SAN

certificate_issuance:
    auto_issue: true
    rate_limits:
        per_agent_per_hour: 10
        per_colony_per_hour: 1000
    renewal_without_discovery: true  # Allow renewals without referral tickets

policy:
    signing_certificate: ~/.coral/colonies/my-app-prod/ca/policy-signing.crt
    signing_key: ~/.coral/colonies/my-app-prod/ca/policy-signing.key
    signing_key_id: policy-key-a3f2e1
    canonicalization: rfc8785-jcs  # Use RFC 8785 JSON Canonicalization Scheme
```

## Policy-Based Authorization

### Problem: Unrestricted Certificate Issuance

With only CA fingerprint validation, any entity with the fingerprint can request
unlimited certificates:

```
Attacker has CA fingerprint
→ Submits CSRs to colony
→ Colony auto-issues certificates
→ ⚠️ No authorization layer, only rate limiting at colony
```

### Solution: Discovery Referral Tickets

Add an authorization layer where Discovery issues short-lived **referral tickets
** that Colony validates before issuing certificates. Colony stores signed
authorization policies at Discovery during initialization, enabling Discovery to
enforce colony-specific rules.

### Policy Document

Colony defines and signs authorization policies during initialization:

```yaml
# Policy pushed to Discovery (signed by colony)
# IMPORTANT: Policy must be canonicalized using RFC 8785 (JCS) before signing
colony_id: my-app-prod-a3f2e1
policy_version: 1
expires_at: "2025-11-21T10:30:00Z"

referral_tickets:
    enabled: true
    ttl: 60  # seconds

    rate_limits:
        per_agent_per_hour: 10
        per_source_ip_per_hour: 100
        per_colony_per_hour: 1000

    quotas:
        max_active_agents: 10000
        max_new_agents_per_day: 100

    agent_id_policy:
        allowed_prefixes: [ "web-", "worker-", "db-" ]
        denied_patterns: [ "test-*", "dev-*" ]
        max_length: 64
        regex: "^[a-z0-9][a-z0-9-]*[a-z0-9]$"

    allowed_cidrs:
        - "10.0.0.0/8"
        - "172.16.0.0/12"

csr_policies:
    allowed_key_types: [ "ed25519", "ecdsa-p256" ]
    max_validity_days: 90

# Signature is computed over RFC 8785 canonical JSON
signature: "base64-encoded-ed25519-signature-over-canonical-json"
signature_algorithm: "Ed25519-RFC8785-JCS"
```

**Policy Signature Process:**

1. **Canonicalization**: Policy is canonicalized using RFC 8785 JSON
   Canonicalization Scheme (JCS) before signing
2. **Signing**: Ed25519 signature computed over canonical JSON bytes
3. **Verification**: Discovery re-canonicalizes policy and verifies signature
   using public key from policy certificate

**Why RFC 8785 JCS?**

- **Deterministic**: Same JSON always produces same canonical form
- **Cross-platform**: Works across different JSON libraries and Go versions
- **Standard**: RFC 8785 is a well-defined standard
- **Prevention**: Avoids signature verification failures due to map ordering or
  whitespace differences

### Discovery JWT Key Management

Discovery uses Ed25519 keys for signing referral tickets with automatic rotation
and JWKS publication for Colony validation.

**Key Configuration:**

```yaml
# Discovery service configuration
jwt_signing:
    key_type: "Ed25519"
    current_key_id: "discovery-2024-11-21"
    rotation_period: "30d"  # Rotate every 30 days
    jwks_endpoint: "/.well-known/jwks.json"

    # Keys are stored securely
    storage:
        type: "database"  # Or "hsm" for production
        encryption: "age"
```

**Key Rotation:**

1. Discovery generates new Ed25519 keypair every 30 days
2. Old key retained for 7-day grace period (validates existing tickets)
3. JWKS endpoint publishes both current and previous keys
4. Colony fetches JWKS on startup and refreshes hourly
5. Colony validates tickets using any key in JWKS

**JWKS Cache Behavior:**

- **On Startup**: Colony fetches JWKS from Discovery, caches in memory
- **Refresh**: Colony refreshes JWKS every 60 minutes
- **Cache Miss**: If JWKS unavailable on startup, Colony retries every 60
  seconds
- **Temporary Unavailability**: If JWKS refresh fails, Colony continues using
  cached keys
- **Stale Cache**: If JWKS cache older than 24 hours, Colony rejects bootstrap
  requests (renewals still work)
- **Validation**: Colony validates JWT using any key in cached JWKS (supports
  rotation)
- **No Disk Cache**: JWKS stored in memory only (security-sensitive)

**JWKS Endpoint:**

```json
GET /.well-known/jwks.json

{
    "keys": [
        {
            "kid": "discovery-2024-11-21",
            "kty": "OKP",
            "crv": "Ed25519",
            "x": "base64-encoded-public-key",
            "use": "sig",
            "alg": "EdDSA"
        },
        {
            "kid": "discovery-2024-10-21",
            "kty": "OKP",
            "crv": "Ed25519",
            "x": "base64-encoded-public-key-old",
            "use": "sig",
            "alg": "EdDSA"
        }
    ]
}
```

**Why Ed25519?**

- **Performance**: Faster signing and verification than RSA
- **Security**: 128-bit security level with smaller keys (32 bytes)
- **Simplicity**: No parameter choices (unlike ECDSA curve selection)
- **Modern**: Industry standard for new systems

### Referral Ticket JWT Claims

**JWT Structure:**

```json
{
  "header": {
    "alg": "EdDSA",
    "typ": "JWT",
    "kid": "discovery-2024-11-21"
  },
  "payload": {
    "sub": "agent:web-prod-1",
    "aud": "coral-colony",
    "iss": "coral-discovery",
    "colony_id": "my-app-prod-a3f2e1",
    "agent_id": "web-prod-1",
    "source_ip": "10.0.1.42",
    "jti": "a3f2e1d4-c5b6-a7f8-e9d0-c1b2a3f4e5d6",
    "iat": 1700000000,
    "exp": 1700000060
  }
}
```

**Claim Definitions:**

- `sub`: Subject identifier (`agent:<agent-id>`)
- `aud`: Audience (always `coral-colony`)
- `iss`: Issuer (always `coral-discovery`)
- `colony_id`: Target colony for this ticket
- `agent_id`: Agent identity requesting certificate
- `source_ip`: Client IP address (for audit and policy enforcement)
- `jti`: JWT ID (unique identifier for replay prevention)
- `iat`: Issued at timestamp (Unix epoch)
- `exp`: Expiration timestamp (iat + 60 seconds)

**Validation Requirements:**

- Colony MUST verify `exp` is in the future
- Colony MUST verify `colony_id` matches its own identity
- Colony MUST verify `agent_id` matches CSR subject CN
- Colony MUST store `jti` for 60 seconds to prevent replay
- Colony MUST verify signature using JWKS public keys

### Bootstrap Flow with Referral Tickets

**First-Time Bootstrap** (requires referral ticket):

```
Agent → Discovery: RequestReferralTicket(colony_id, agent_id)
           Discovery loads colony policy
           Discovery checks: rate limits, quotas, agent_id policy, IP allowlists
           Discovery signs JWT with Ed25519 key
           Discovery → Agent: JWT ticket (1-minute TTL)

Agent → Colony: RequestCertificate(CSR, referral_ticket)
           Colony fetches JWKS from Discovery (if not cached)
           Colony validates JWT signature using JWKS public keys
           Colony validates ticket expiry and claims
           Colony issues certificate (90-day validity)

Agent → Colony: RegisterAgent (mTLS)
```

**Certificate Renewal** (no referral ticket required):

```
Agent → Colony: RequestCertificate(CSR)
           Agent authenticates with existing mTLS certificate
           Colony validates existing certificate is not revoked
           Colony issues new certificate (90-day validity)
           NO Discovery interaction required

Agent → Colony: Continue operations (mTLS with new certificate)
```

**Benefits:**

- **High availability**: Certificate renewals work even if Discovery is offline
- **Reduced load**: Discovery not bottleneck for routine renewals
- **Security**: First bootstrap still requires Discovery authorization
- **Revocation**: Colony checks CRL/revocation list before renewal

## Certificate Lifecycle

### Renewal Schedule

**Agent Certificates (90-day validity):**

- **Renewal window**: Starts at 30 days before expiry (70% of lifetime)
- **Grace period**: 7 days before expiry (agent shows warnings)
- **Expiration behavior**: Agent falls back to `colony_secret` if enabled,
  otherwise connection fails

**Intermediate Certificates (1-year validity):**

- **Renewal window**: 30 days before expiry
- **Rotation process**: Colony generates new intermediate, keeps old for 7-day
  overlap
- **Agent impact**: None (agents validate Root CA, not intermediates)

### Renewal Process

**Automatic Renewal (Agent-Initiated):**

1. Agent monitors certificate expiry (checks every hour)
2. At 30 days before expiry, agent initiates renewal
3. Agent generates new CSR with same agent_id
4. Agent authenticates to Colony using existing mTLS certificate
5. Colony validates certificate is not revoked
6. Colony issues new certificate (90-day validity)
7. Agent stores new certificate, continues using old until cutover
8. Agent switches to new certificate after validation

**No Discovery Required**: Renewals use existing mTLS authentication, no
referral ticket needed.

**Failure Handling:**

- Renewal failure logged and retried (exponential backoff)
- At 7 days before expiry, alerts sent to monitoring
- At expiry, agent falls back to `colony_secret` (if enabled) or fails

### Intermediate CA Rotation

**Planned Rotation:**

1. Colony generates new intermediate CA (30 days before expiry)
2. Colony configures both old and new intermediates as valid
3. New certificates signed by new intermediate
4. Old certificates remain valid (signed by old intermediate)
5. After 7-day overlap, old intermediate retired
6. Root CA validates both chains during overlap

**Emergency Rotation (Compromise):**

1. Operator generates new intermediate CA immediately
2. Old intermediate added to revocation list
3. All agent certificates must be renewed (Discovery referral tickets required)
4. Colony rejects certificates signed by old intermediate

### Security Properties

**Defense in depth:**

1. **CA fingerprint**: Prevents MITM attacks during bootstrap
2. **Referral ticket**: Adds authorization layer before certificate issuance
3. **Policy enforcement**: Colony-defined rules enforced by Discovery
4. **Rate limiting**: Prevents mass registration attacks at Discovery layer
5. **Monitoring**: Detects suspicious patterns and alerts operators

**Attack scenarios:**

| Attack                            | Protection                                                    |
|-----------------------------------|---------------------------------------------------------------|
| **Discovery MITM**                | Agent validates Root CA fingerprint, aborts on mismatch ✅     |
| **Cross-colony impersonation**    | Agent validates colony ID in server cert SAN (SPIFFE) ✅       |
| **Bootstrap intermediate leaked** | Cannot issue server certs (separate Server Intermediate) ✅    |
| **CA fingerprint leaked**         | Need referral ticket (rate-limited, policy-controlled) ✅      |
| **Fake agent registration**       | Discovery enforces quotas, agent ID policies, IP allowlists ✅ |
| **Mass registration attack**      | Per-IP rate limits, per-colony quotas ✅                       |
| **Referral ticket stolen**        | 1-minute TTL, agent_id binding, single-use (tracked by jti) ✅ |
| **Discovery offline**             | Certificate renewals work without Discovery (mTLS auth) ✅     |
| **Policy signature forgery**      | RFC 8785 JCS ensures deterministic verification ✅             |
| **JWT signing key compromised**   | 30-day rotation, JWKS with grace period for rollover ✅        |

### Bootstrap Failures & Offline Environments

**Problem**: Production environments often have unreliable connectivity to
Discovery, especially in edge deployments, manufacturing facilities, on-premises
clusters, or during network partitions.

**Retry Behavior:**

```yaml
# Agent bootstrap configuration
bootstrap:
    retry_policy:
        # Exponential backoff with jitter
        initial_delay: 1s
        max_delay: 5m
        multiplier: 2.0
        jitter: 0.2

        # Retry limits
        max_attempts: 10  # Per bootstrap attempt
        total_timeout: 30m  # Total time before giving up

        # After exhausting retries
        fallback_action: "use_colony_secret"  # Or "fail"
```

**Failure Scenarios:**

1. **Discovery Unreachable (Network)**:
    - Agent retries with exponential backoff (1s, 2s, 4s, 8s, ..., 5m)
    - After 10 attempts or 30 minutes, falls back to `colony_secret` (if
      enabled)
    - Logs clear error: "Bootstrap failed: Discovery unreachable after 10
      attempts"
    - Agent continues attempting bootstrap in background (hourly)

2. **Discovery Rate Limiting**:
    - Discovery returns 429 (Too Many Requests) with `Retry-After` header
    - Agent respects `Retry-After` delay (overrides exponential backoff)
    - Does not count toward max_attempts
    - Logs: "Bootstrap delayed: Rate limited by Discovery (retry after 60s)"

3. **Discovery Denies Agent**:
    - Discovery returns 403 (Forbidden) with rejection reason
    - Agent does NOT retry (permanent failure)
    - Logs: "Bootstrap failed: Agent denied by policy (reason: agent_id pattern
      mismatch)"
    - Operator intervention required

4. **Colony Unreachable**:
    - Agent successfully gets referral ticket from Discovery
    - Cannot reach Colony endpoint (network issue, Colony down)
    - Agent retries Colony connection with exponential backoff
    - Referral ticket expires after 1 minute (must request new ticket)

**Operator Override Mechanisms:**

For disaster recovery or air-gapped deployments:

```bash
# Generate emergency bootstrap token (Colony side)
coral colony tokens create-emergency \
    --agent-id web-1 \
    --validity 24h \
    --reason "DR: Discovery unreachable"

# Output:
# Emergency Token: emergency_a3f2e1d4c5b6a7f8...
# Valid until: 2025-11-22 10:30:00 UTC

# Agent uses emergency token (bypasses Discovery)
export CORAL_EMERGENCY_TOKEN=emergency_a3f2e1d4c5b6a7f8...
coral agent bootstrap --use-emergency-token
```

**Multi-Region Discovery:**

```yaml
# Support multiple Discovery instances
discovery:
    endpoints:
        - https://discovery-us-west.coral.io:8080
        - https://discovery-us-east.coral.io:8080
        - https://discovery-eu.coral.io:8080

    selection_strategy: "round_robin"  # Or "geolocation", "latency"
    failover_timeout: 5s
```

**Observability:**

- Metric: `coral_agent_bootstrap_attempts_total{result="success|failure|timeout"}`
- Metric: `coral_agent_bootstrap_duration_seconds`
- Metric: `coral_agent_discovery_reachable{endpoint="..."}`
- Alert: Bootstrap failure rate > 10% for 5 minutes

## Agent Identity Model

**Agent ID Definition:**

Agent IDs are the primary identity for agents in the Coral system. They drive
SPIFFE IDs, policy enforcement, rate limits, and certificate subjects.

**How Agent IDs Are Chosen:**

1. **Operator-specified** (preferred):
    ```bash
    coral agent start --agent-id web-prod-1
    ```

2. **Auto-generated** (fallback):
    ```bash
    # Format: {hostname}-{short-uuid}
    # Example: ip-10-0-1-42-a3f2e1d4
    coral agent start  # No --agent-id specified
    ```

3. **Kubernetes Pod Name** (recommended for K8s):
    ```yaml
    env:
        - name: CORAL_AGENT_ID
          valueFrom:
              fieldRef:
                  fieldPath: metadata.name
    # Results in agent_id like: my-app-deployment-7d4f8b9c-xk2pm
    ```

**Format Constraints:**

- **Pattern**: `^[a-z0-9][a-z0-9-]*[a-z0-9]$` (lowercase alphanumeric and
  hyphens)
- **Length**: 3-64 characters
- **Start/End**: Must start and end with alphanumeric (not hyphen)
- **Case**: Lowercase only (enforced at validation)

**Uniqueness Guarantees:**

- **Within Colony**: Agent IDs MUST be unique within a colony
    - Colony rejects certificate requests for duplicate agent_id if active cert
      exists
    - Allows re-use after certificate expiry or revocation
- **Across Colonies**: Agent IDs CAN be reused across different colonies
    - `colony-A/web-1` and `colony-B/web-1` are distinct identities
    - SPIFFE IDs include colony_id to ensure global uniqueness

**Identity Persistence:**

- **First Bootstrap**: Agent generates/receives agent_id, stores in
  `~/.coral/agent-id`
- **Subsequent Starts**: Agent reads agent_id from `~/.coral/agent-id`
- **Certificate**: Agent_id embedded in certificate CN and SPIFFE SAN
- **Immutability**: Agent_id cannot change after first bootstrap (requires
  re-bootstrap)

**SPIFFE ID Mapping:**

```
Agent ID: web-prod-1
Colony ID: my-app-prod-a3f2e1

SPIFFE ID: spiffe://coral/colony/my-app-prod-a3f2e1/agent/web-prod-1
```

**Revocation & CRL:**

- CRL includes agent_id in extension field for easier debugging
- Operators can revoke by agent_id: `coral colony certs revoke --agent-id
  web-1`

## Agent Deployment

### Environment Variables

```bash
# Required
CORAL_COLONY_ID=my-app-prod-a3f2e1
CORAL_CA_FINGERPRINT=sha256:a3f2e1d4c5b6a7f8e9d0c1b2a3f4e5d6...

# Optional (has defaults)
CORAL_DISCOVERY_ENDPOINT=https://discovery.coral.io:8080
```

### Kubernetes Deployment

```yaml
apiVersion: v1
kind: Secret
metadata:
    name: coral-colony-ca
data:
    colony_id:
        <base64: my-app-prod-a3f2e1>
    ca_fingerprint:
        <base64: sha256:a3f2e1d4c5b6...>
---
apiVersion: apps/v1
kind: Deployment
metadata:
    name: my-app
spec:
    template:
        spec:
            containers:
                -   name: coral-agent
                    image: coral/agent:latest
                    env:
                        -   name: CORAL_COLONY_ID
                            valueFrom:
                                secretKeyRef:
                                    name: coral-colony-ca
                                    key: colony_id
                        -   name: CORAL_CA_FINGERPRINT
                            valueFrom:
                                secretKeyRef:
                                    name: coral-colony-ca
                                    key: ca_fingerprint
                    volumeMounts:
                        -   name: coral-certs
                            mountPath: /var/lib/coral/certs
            volumes:
                -   name: coral-certs
                    emptyDir: { }  # Or persistentVolumeClaim for daemonsets
```

### Configuration File

```yaml
# ~/.coral/agents/<agent-id>.yaml
security:
    # Certificate file paths (auto-detected if not specified)
    cert_path: ~/.coral/certs/<agent-id>.crt
    key_path: ~/.coral/certs/<agent-id>.key
    root_ca_path: ~/.coral/certs/root-ca.crt

    # Root CA fingerprint for validation
    ca_fingerprint: sha256:a3f2e1d4c5b6a7f8e9d0c1b2a3f4e5d6...

    bootstrap:
        enabled: true  # Enable automatic bootstrap on first connect
        discovery_url: https://discovery.coral.io:8080
        fallback_to_secret: true  # Fall back to colony_secret if bootstrap fails (migration only)
        retry_attempts: 3
        retry_delay: 5s
```

## Component Changes

1. **Colony CA Initialization** (`internal/colony/ca/init.go`)
    - Generate Root CA (10-year validity)
    - Generate Bootstrap Intermediate CA (1-year validity, fingerprint
      validation
      only)
    - Generate Server Intermediate CA (1-year validity, for server certificates)
    - Generate Agent Intermediate CA (1-year validity, for client certificates)
    - Generate policy signing certificate (signed by Root CA, 10-year validity)
    - Generate policy signing Ed25519 keypair
    - Generate colony TLS server certificate with SPIFFE ID in SAN
      (`spiffe://coral/colony/{colony-id}`)
    - Compute and display Root CA fingerprint and Colony SPIFFE ID
    - Save CA hierarchy with proper permissions

2. **Colony Policy Management** (`internal/colony/policy/`)
    - Define default authorization policies
    - Canonicalize policies using RFC 8785 JSON Canonicalization Scheme (JCS)
    - Sign canonical policies with Ed25519 policy signing key
    - Push signed policies to Discovery via `UpsertColonyPolicy` RPC
    - Update policies and re-push to Discovery
    - Validate policy structure and constraints

3. **Discovery Policy Storage** (`internal/discovery/policy/`)
    - Accept and validate signed colony policies
    - Verify policy certificate chain (Policy Cert → Root CA)
    - Register new colonies (lock colony_id to Root CA fingerprint)
    - Verify Root CA fingerprint matches for existing colonies
    - Detect and prevent colony impersonation attempts
    - Canonicalize policies using RFC 8785 JCS before signature verification
    - Verify Ed25519 signatures on canonical policies using public key from
      validated certificate
    - Store policies in database with versioning and certificates
    - Store colony registrations (colony_id → Root CA mapping)
    - Retrieve policies for referral ticket issuance
    - Expire old policies based on `expires_at`

4. **Discovery JWT Key Management** (`internal/discovery/jwt/`)
    - Generate and manage Ed25519 keypairs for JWT signing
    - Implement automatic key rotation (30-day default, 7-day grace period)
    - Publish JWKS endpoint (`/.well-known/jwks.json`)
    - Store current and previous keys in database
    - Support HSM integration for key storage (optional)
    - Provide key metadata (kid, algorithm, creation date)

5. **Discovery Referral Tickets** (`internal/discovery/referral/`)
    - Issue short-lived JWT referral tickets (1-minute TTL)
    - Enforce rate limits (per-agent, per-IP, per-colony)
    - Track quotas (max active agents, new agents per day)
    - Validate agent IDs against policy (regex, prefixes, deny patterns)
    - Check IP allowlists/denylists
    - Monitor and alert on suspicious patterns
    - Sign tickets with Discovery's Ed25519 key (using current key_id)
    - Include key ID (kid) in JWT header for validation

6. **CA Fingerprint Validator** (`internal/agent/bootstrap/ca_validator.go`)
    - Extract Root CA from TLS certificate chain
    - Compute SHA256 fingerprint of Root CA
    - Compare against expected fingerprint from config
    - Validate colony ID in server certificate SAN (SPIFFE URI)
    - Verify SAN matches expected colony ID from config
    - Validate certificate chain integrity (Server cert → Server Intermediate →
      Root)
    - Abort connection on mismatch (MITM or cross-colony impersonation
      detection)

7. **Agent Bootstrap Client** (`internal/agent/bootstrap/client.go`)
    - Request referral ticket from Discovery first
    - Handle rate limit and permission errors gracefully
    - Query Discovery for colony endpoints
    - Connect to colony with `InsecureSkipVerify` (manual validation)
    - Validate Root CA fingerprint using CA validator
    - Validate colony ID in server certificate SAN
    - Save validated Root CA to disk
    - Generate Ed25519 keypairs using `crypto/ed25519`
    - Create X.509 CSRs with CN=agent_id, O=colony_id, SAN=spiffe URI
    - Call Colony's `RequestCertificate` RPC with referral ticket
    - Validate received certificate matches our public key and includes SPIFFE
      SAN
    - Store certificates securely with proper permissions

8. **Agent Certificate Manager** (`internal/agent/certs/manager.go`)
    - Check certificate existence and validity on startup
    - Load certificates for gRPC client TLS configuration
    - Load Root CA for colony server validation
    - Monitor certificate expiry (trigger renewal at 30 days)
    - Handle certificate storage and file permissions
    - Provide certificate metadata for status commands
    - Implement automatic renewal without Discovery (using existing mTLS cert)

9. **Agent Connection Setup** (`internal/agent/connection.go`)
    - Attempt certificate-based connection first
    - Fall back to `colony_secret` if bootstrap fails (during migration)
    - Configure gRPC client with mTLS transport credentials
    - Validate Colony server certificate against pinned Root CA
    - Validate colony ID in server certificate SAN

10. **Colony Certificate Issuance** (`internal/colony/ca/issuer.go`)
    - Distinguish between first-time bootstrap and renewal requests
    - **For bootstrap requests:**
        - Validate referral ticket (JWT signature and claims)
        - Fetch JWKS from Discovery (cache for 1 hour)
        - Verify ticket signature using JWKS public keys
        - Verify agent_id and colony_id match ticket claims
        - Check ticket expiration (should be within 1 minute)
        - Store ticket JTI to prevent reuse
    - **For renewal requests:**
        - Validate existing mTLS certificate from client
        - Check certificate is not revoked (CRL/revocation list)
        - No referral ticket required
    - Validate CSR signature and structure
    - Extract agent_id from CN field
    - Sign with Agent Intermediate CA (90-day validity)
    - Include SPIFFE ID in SAN (
      `spiffe://coral/colony/{colony-id}/agent/{agent-id}`)
    - Store certificate metadata in database
    - Monitor and alert on invalid tickets or suspicious patterns
    - Return certificate + full CA chain

11. **CLI Agent Commands** (`internal/cli/agent/`)
    - `coral agent bootstrap` - Manually trigger bootstrap flow
    - `coral agent cert status` - Display certificate info (including SPIFFE ID)
    - `coral agent cert renew` - Manually renew certificate

12. **CLI Colony Commands** (`internal/cli/colony/`)
    - `coral colony ca status` - Display CA hierarchy info (including Server
      Intermediate)
    - `coral colony ca rotate-intermediate` - Rotate intermediate CAs
    - `coral colony policy show` - Display current policy
    - `coral colony policy update` - Update and push new policy
    - `coral colony policy push` - Push policy to Discovery

13. **Colony JWKS Client** (`internal/colony/jwks/`)
    - Fetch JWKS from Discovery on startup
    - Cache JWKS with 1-hour TTL
    - Refresh JWKS periodically
    - Parse and validate JWKS format
    - Provide JWT validation using cached keys
    - Handle key rotation gracefully

## Implementation Plan

### Phase 1: Colony CA Infrastructure

- [ ] Implement `internal/colony/ca/init.go`
    - Root CA generation (10-year validity)
    - Bootstrap Intermediate CA generation (fingerprint validation only)
    - **Server Intermediate CA generation (for server certificates)**
    - Agent Intermediate CA generation (for client certificates)
    - Policy signing certificate generation (signed by Root CA, 10-year
      validity)
    - Policy signing Ed25519 keypair generation
    - **Colony TLS server certificate with SPIFFE ID in SAN**
    - Root CA fingerprint computation
    - **Display Colony SPIFFE ID**
    - Save CA hierarchy with proper permissions
- [ ] Update `coral colony init` to generate CA hierarchy
- [ ] Add `coral colony ca status` command
- [ ] Add `coral colony ca rotate-intermediate` command
- [ ] **Add RFC 8785 JCS canonicalization library dependency**
- [ ] Add unit tests for CA generation
- [ ] **Add unit tests for SPIFFE ID generation and validation**

### Phase 2: Colony Policy Management

- [ ] Implement `internal/colony/policy/policy.go`
    - Define policy structures
    - Implement default policies
    - **Policy canonicalization using RFC 8785 JCS**
    - Policy signing with Ed25519 over canonical JSON
    - Policy serialization and validation
- [ ] Implement `internal/colony/policy/push.go`
    - Push policies to Discovery
    - Handle policy updates
    - Version management
- [ ] Add `coral colony policy show` command
- [ ] Add `coral colony policy update` command
- [ ] Add `coral colony policy push` command
- [ ] **Add unit tests for RFC 8785 JCS canonicalization**
- [ ] Add unit tests for policy signing and validation
- [ ] **Add cross-platform policy signature verification tests**

### Phase 3: Discovery Service Policy Storage

- [ ] Implement `internal/discovery/policy/store.go`
    - Accept and validate signed policies with certificates
    - Verify policy certificate chain (Policy Cert → Root CA)
    - Implement colony registration (colony_id → Root CA fingerprint locking)
    - Verify Root CA fingerprint for existing colonies
    - Detect and log colony impersonation attempts
    - **Canonicalize policies using RFC 8785 JCS before verification**
    - Verify Ed25519 signatures over canonical JSON using public key from
      validated certificate
    - Store policies in database with certificates
    - Policy expiration handling
- [ ] Add `UpsertColonyPolicy` RPC endpoint with certificate validation
- [ ] Add `GetColonyPolicy` RPC endpoint
- [ ] Add database schema for colony registrations
- [ ] Add database schema for policy storage with certificates
- [ ] Add unit tests for certificate chain validation
- [ ] Add unit tests for colony registration and impersonation detection
- [ ] **Add unit tests for canonical policy verification**

### Phase 4: Discovery Service JWT Key Management

- [ ] Implement `internal/discovery/jwt/keys.go`
    - Ed25519 keypair generation
    - Automatic key rotation (30-day period, 7-day grace)
    - Key storage in database with metadata
    - Current and previous key tracking
    - Optional HSM integration
- [ ] Implement `internal/discovery/jwt/jwks.go`
    - JWKS endpoint (`/.well-known/jwks.json`)
    - JWKS format generation (RFC 7517)
    - Key metadata (kid, alg, use)
- [ ] Add database schema for JWT keys
- [ ] Add background task for key rotation
- [ ] Add unit tests for key generation and rotation
- [ ] **Add unit tests for JWKS format compliance**

### Phase 5: Discovery Service Referral Tickets

- [ ] Implement `internal/discovery/referral/ticket.go`
    - JWT ticket issuance with Ed25519
    - **Include key ID (kid) in JWT header**
    - Rate limiting (per-agent, per-IP, per-colony)
    - Quota tracking
    - Agent ID validation
    - IP allowlist/denylist checks
- [ ] Add `RequestReferralTicket` RPC endpoint
- [ ] Add database schema for rate limit tracking
- [ ] Add monitoring and alerting
- [ ] Add unit tests for ticket issuance
- [ ] **Add unit tests for JWT validation with key rotation**

### Phase 6: CA Fingerprint Validation

- [ ] Implement `internal/agent/bootstrap/ca_validator.go`
    - Extract Root CA from TLS connection
    - Compute SHA256 fingerprint
    - Compare against expected value
    - **Validate colony ID in server certificate SAN (SPIFFE URI)**
    - **Verify SAN matches expected colony ID**
    - Validate certificate chain integrity (Server cert → Server Intermediate →
      Root)
    - Log detailed errors on mismatch
- [ ] Add `CORAL_CA_FINGERPRINT` environment variable support
- [ ] Add configuration field `security.ca_fingerprint`
- [ ] **Add unit tests for fingerprint validation**
- [ ] **Add unit tests for SAN validation (cross-colony impersonation detection)
  **

### Phase 7: Agent Bootstrap Implementation

- [ ] Implement `internal/agent/bootstrap/referral.go`
    - Request referral ticket from Discovery
    - Handle rate limit errors
    - Handle permission denied errors
- [ ] Implement `internal/agent/bootstrap/client.go`
    - Query Discovery for endpoints
    - CA fingerprint validation before CSR submission
    - **Colony ID SAN validation before CSR submission**
    - Ed25519 keypair generation
    - **CSR creation with SPIFFE ID in SAN**
    - Certificate request with referral ticket
    - **Validate received certificate includes SPIFFE SAN**
    - Certificate storage
- [ ] Add retry logic with exponential backoff
- [ ] Add unit tests for bootstrap client
- [ ] **Add unit tests for SPIFFE ID validation**

### Phase 8: Colony Certificate Issuance

- [ ] Implement `internal/colony/jwks/client.go`
    - **Fetch JWKS from Discovery on startup**
    - **Cache JWKS with 1-hour TTL**
    - **Periodic JWKS refresh**
    - **JWT validation using JWKS keys**
- [ ] Update `internal/colony/ca/issuer.go`
    - **Distinguish between bootstrap and renewal requests**
    - **For bootstrap:** Validate referral ticket (JWT)
    - **For renewal:** Validate existing mTLS certificate (no ticket required)
    - **Verify JWT signature using JWKS**
    - Verify agent_id and colony_id match ticket
    - **Issue certificates with SPIFFE ID in SAN**
    - Auto-issue on valid CSR + ticket (or valid mTLS cert for renewal)
    - Store ticket JTI in certificate record (bootstrap only)
    - Certificate tracking in database
- [ ] Add monitoring/alerting for invalid tickets
- [ ] Add unit tests for ticket validation and issuance
- [ ] **Add unit tests for renewal without Discovery**
- [ ] **Add unit tests for JWKS-based JWT validation**

### Phase 9: Agent Connection Integration

- [ ] Update `internal/agent/connection.go`
    - Use certificates for mTLS
    - Root CA validation for colony server
    - **Colony ID SAN validation for colony server**
    - Fallback to `colony_secret` during migration
- [ ] Implement certificate manager
    - **Automatic renewal using existing mTLS cert (no Discovery required)**
    - Certificate expiry monitoring
- [ ] Add integration tests for mTLS connections
- [ ] **Add integration tests for renewal without Discovery**

### Phase 10: CLI Commands & Monitoring

- [ ] Implement `coral agent bootstrap` command
- [ ] Implement `coral agent cert status` command **(include SPIFFE ID)**
- [ ] Implement `coral agent cert renew` command
- [ ] Implement `coral colony policy show` command
- [ ] Implement `coral colony policy update` command
- [ ] Implement `coral colony policy push` command
- [ ] Add certificate expiry warnings to `coral agent status`
- [ ] Add telemetry for bootstrap success/failure rates
- [ ] Add telemetry for referral ticket issuance rates
- [ ] **Add telemetry for renewal success/failure rates**

### Phase 11: Testing & Documentation

- [ ] Unit tests for all new components
- [ ] Integration test: full bootstrap flow **(with SPIFFE ID validation)**
- [ ] Integration test: intermediate rotation
- [ ] E2E test: MITM detection (wrong fingerprint)
- [ ] **E2E test: Cross-colony impersonation detection (wrong colony ID in SAN)
  **
- [ ] E2E test: certificate renewal **(without Discovery)**
- [ ] **E2E test: Discovery offline during renewal (should succeed)**
- [ ] **E2E test: Bootstrap intermediate compromised (cannot issue server certs)
  **
- [ ] **E2E test: Policy signature verification across platforms (RFC 8785 JCS)
  **
- [ ] **E2E test: JWT key rotation (JWKS with grace period)**
- [ ] Update agent deployment documentation
- [ ] Add troubleshooting guide for bootstrap failures

## API Changes

### New Discovery Service APIs

```protobuf
// New: Colony pushes signed authorization policy to Discovery
message UpsertColonyPolicyRequest {
    string colony_id = 1;
    bytes policy = 2;              // JSON-encoded policy document (RFC 8785 JCS canonical)
    bytes signature = 3;           // Ed25519 signature over canonical JSON
    string signature_algorithm = 4; // "Ed25519-RFC8785-JCS"
    bytes policy_certificate = 5;  // Policy signing certificate (signed by Root CA)
    bytes root_ca_certificate = 6; // Root CA certificate (for chain validation)
}

message UpsertColonyPolicyResponse {
    bool success = 1;
    int32 policy_version = 2;
}

// New: Agent requests referral ticket before certificate request
message RequestReferralTicketRequest {
    string colony_id = 1;
    string agent_id = 2;
}

message RequestReferralTicketResponse {
    string ticket = 1;          // JWT referral ticket (signed with Ed25519, includes kid)
    google.protobuf.Timestamp expires_at = 2;
}

// New: Colony fetches JWKS from Discovery
message GetJWKSRequest {}

message GetJWKSResponse {
    string jwks = 1;  // JSON Web Key Set (RFC 7517 format)
}

// Existing: Lookup colony endpoints
message LookupColonyRequest {
    string colony_id = 1;
}

message LookupColonyResponse {
    repeated string endpoints = 1;
    int32 connect_port = 2;
    // ... other fields
}
```

**JWKS HTTP Endpoint:**

```
GET /.well-known/jwks.json

Response (application/json):
{
  "keys": [
    {
      "kid": "discovery-2024-11-21",
      "kty": "OKP",
      "crv": "Ed25519",
      "x": "base64-encoded-public-key",
      "use": "sig",
      "alg": "EdDSA"
    }
  ]
}
```

### Modified Colony Service API

```protobuf
// Updated: Referral ticket required for bootstrap, optional for renewal
message RequestCertificateRequest {
    bytes csr = 1;              // Certificate Signing Request (PEM format)
    // CSR includes SPIFFE ID in SAN
    string referral_ticket = 2; // JWT from Discovery (1-minute TTL)
    // REQUIRED for first-time bootstrap
    // OPTIONAL for renewal (uses mTLS auth)
}

message RequestCertificateResponse {
    bytes certificate = 1;      // Signed X.509 client certificate (PEM)
    // Includes SPIFFE ID in SAN
    bytes ca_chain = 2;         // Full CA chain (Agent Intermediate + Root CA)
    google.protobuf.Timestamp expires_at = 3;
}
```

### API Summary

**Discovery Service:**

- `UpsertColonyPolicy` (new) - Colony pushes signed policy (RFC 8785 JCS
  canonical)
- `RequestReferralTicket` (new) - Agent requests authorization ticket (JWT with
  Ed25519)
- `GetJWKS` (new) - Colony fetches JWT public keys for validation
- `LookupColony` (existing) - Agent queries colony endpoints
- `/.well-known/jwks.json` (HTTP endpoint) - Public JWKS endpoint for key
  discovery

**Colony Service:**

- `RequestCertificate` (modified) - Agent requests certificate with optional
  referral ticket
    - First-time bootstrap: Requires referral ticket
    - Renewal: Uses mTLS authentication, no ticket required

## CLI Commands

```bash
# Manually trigger bootstrap (for testing or re-bootstrapping)
coral agent bootstrap --colony my-app-prod --agent web-1

# Output:
Querying Discovery for colony endpoints...
✓ Found colony at https://colony.example.com:9000

Connecting to colony...
Validating Root CA fingerprint...
  Expected: sha256:a3f2e1d4c5b6...
  Received: sha256:a3f2e1d4c5b6...
✓ Root CA fingerprint verified - trust established

Validating colony identity...
  Expected colony ID: my-app-prod
  Server cert SAN: spiffe://coral/colony/my-app-prod
✓ Colony identity confirmed - no cross-colony impersonation

Validating certificate chain...
✓ Certificate chain verified (Server → Server Intermediate → Root CA)

Generating keypair...
✓ Ed25519 keypair generated

Creating certificate signing request...
✓ CSR created (CN=web-1, O=my-app-prod)
  SAN: spiffe://coral/colony/my-app-prod/agent/web-1

Requesting certificate from Colony...
✓ Certificate received (valid until 2025-02-18)

Saving credentials...
✓ Root CA saved to ~/.coral/certs/root-ca.crt
✓ Certificate saved to ~/.coral/certs/web-1.crt
✓ Private key saved to ~/.coral/certs/web-1.key (0600)

✓ Bootstrap complete


# Check certificate status
coral agent cert status --agent web-1

# Output:
Certificate Status
==================
Agent ID:          web-1
Colony ID:         my-app-prod-a3f2e1
Certificate Path:  ~/.coral/certs/web-1.crt
Key Path:          ~/.coral/certs/web-1.key
Root CA Path:      ~/.coral/certs/root-ca.crt

Root CA:
  Fingerprint:     sha256:a3f2e1d4c5b6a7f8e9d0c1b2a3f4e5d6...
  Subject:         Coral Root CA - my-app-prod
  Valid Until:     2034-11-21

Certificate Details:
  Issuer:          Coral Agent Intermediate CA - my-app-prod
  Subject:         CN=web-1, O=my-app-prod
  Serial Number:   3a4f5e2d1c0b9a8f7e6d5c4b3a2f1e0d
  Not Before:      2024-11-21 10:30:00 UTC
  Not After:       2025-02-18 10:30:00 UTC
  Days Until Expiry: 89

Status:            ✓ Valid


# Manually renew certificate
coral agent cert renew --agent web-1

# Output:
Certificate expires in 25 days, renewing...
Using existing certificate for authentication...
✓ Certificate renewed successfully
✓ New certificate valid until 2025-05-19


# Check colony CA status
coral colony ca status

# Output:
Colony CA Status
================
Colony ID:         my-app-prod-a3f2e1

Root CA:
  Fingerprint:     sha256:a3f2e1d4c5b6a7f8e9d0c1b2a3f4e5d6...
  Valid Until:     2034-11-21 10:30:00 UTC
  Days Remaining:  3652

Bootstrap Intermediate CA:
  Valid Until:     2025-11-21 10:30:00 UTC
  Days Remaining:  365
  Status:          Active

Agent Intermediate CA:
  Valid Until:     2025-11-21 10:30:00 UTC
  Days Remaining:  365
  Status:          Active

Issued Certificates:
  Total:           45
  Active:          43
  Revoked:         2
  Expired:         0
```

## Testing Strategy

### Unit Tests

- **CA Generation**:
    - Generate valid Root CA (10-year validity)
    - Generate valid intermediate CAs (1-year validity)
    - Sign server and client certificates
    - Compute correct SHA256 fingerprints

- **Fingerprint Validation**:
    - Extract Root CA from TLS chain correctly
    - Compute fingerprint matches expected format
    - Detect fingerprint mismatch (MITM scenario)
    - Validate certificate chain integrity

- **Bootstrap Client**:
    - Generate valid Ed25519 keypairs
    - Create CSR with correct CN/O format
    - Handle network errors gracefully
    - Retry with exponential backoff

### Integration Tests

- **Full Bootstrap Flow**:
    - Start with no certificate
    - Query Discovery for endpoints
    - Validate Root CA fingerprint
    - Submit CSR to Colony
    - Receive and store certificate
    - Verify file permissions (0600 for key, 0644 for cert)

- **MITM Detection**:
    - Colony with different Root CA
    - Agent detects fingerprint mismatch
    - Connection aborted with clear error

- **Intermediate Rotation**:
    - Rotate Bootstrap Intermediate CA
    - Existing agents continue working
    - New bootstrap uses new intermediate
    - Root CA fingerprint unchanged

### E2E Tests

- **Agent Deployment**:
    - Deploy agent with only CORAL_COLONY_ID + CORAL_CA_FINGERPRINT
    - Agent automatically bootstraps on first start
    - Agent connects to Colony via mTLS
    - Agent successfully sends heartbeats

- **Certificate Revocation**:
    - Colony revokes agent certificate
    - Agent's next RPC fails with authentication error
    - Agent attempts to re-bootstrap
    - Colony issues new certificate

## Security Considerations

### Root CA Fingerprint Security

**The Root CA fingerprint is NOT a secret** - it's like an SSH host key
fingerprint:

```bash
# Similar security model:
ssh user@server
# The authenticity of host 'server (192.168.1.100)' can't be established.
# ED25519 key fingerprint is SHA256:a3f2e1d4c5b6a7f8e9d0c1b2a3f4e5d6.

coral agent start
# Validates colony using Root CA fingerprint:
# SHA256:a3f2e1d4c5b6a7f8e9d0c1b2a3f4e5d6...
```

**Public distribution is acceptable:**

- Can be in ConfigMaps, not Secrets (but Secrets OK too)
- Can be logged, documented, shared
- Only validates "talking to correct colony"
- Cannot be used to join colony without certificate

### Attack Scenarios

| Attack                       | Protection                                                               |
|------------------------------|--------------------------------------------------------------------------|
| **Discovery MITM**           | Agent validates Root CA fingerprint, aborts on mismatch ✅                |
| **Colony ID leaked**         | Cannot push policies without Root CA private key ✅                       |
| **Colony ID impersonation**  | Discovery locks colony ID to Root CA fingerprint on first registration ✅ |
| **Policy forgery**           | Policy must be signed by certificate chaining to registered Root CA ✅    |
| **CA fingerprint leaked**    | Need referral ticket (rate-limited, policy-controlled) ✅                 |
| **Fake agent registration**  | Discovery enforces quotas, agent ID policies, IP allowlists ✅            |
| **Agent certificate stolen** | Individual revocation, expires in 90 days ✅                              |
| **Intermediate compromised** | Rotate intermediate, Root CA remains trusted ✅                           |
| **Root CA compromised**      | Re-initialize colony, new fingerprint (nuclear option) ⚠️                |
| **Referral ticket stolen**   | 1-minute TTL, agent_id binding, single-use (tracked by jti) ✅            |
| **Mass registration attack** | Per-IP rate limits, per-colony quotas, monitoring/alerting ✅             |
| **CSR replay attack**        | JTI uniqueness enforcement, 60-second tracking window ✅                  |

### CSR Replay Protection

Even if a referral ticket is single-use, the CSR + ticket pair could potentially
be replayed. Colony MUST enforce JTI uniqueness to prevent replay attacks.

**Implementation:**

- Colony stores used `jti` values in a time-limited cache (60 seconds)
- When validating referral ticket, Colony checks if `jti` has been used
- If `jti` already exists, request is rejected as replay attempt
- After 60 seconds (ticket TTL), `jti` is removed from cache

**Example:**

```
First Request:
  CSR + Token(jti=abc123) → Colony validates, stores jti=abc123 → Issues cert

Replay Attempt (within 60s):
  Same CSR + Same Token(jti=abc123) → Colony checks cache, finds jti=abc123 → Rejects as replay

After 60s:
  jti=abc123 expired from cache (token already expired anyway)
```

**Storage**: In-memory cache with LRU eviction, no disk persistence needed.

### Compromise Scenarios

**If Root CA fingerprint leaks:**

```
Attacker has fingerprint → requests referral ticket from Discovery
Discovery enforces: rate limits, quotas, agent_id policy, IP allowlists
Discovery issues ticket (if authorized)
Attacker submits CSR + ticket → Colony validates ticket and issues certificate
Colony/Discovery can: rate limit, alert, monitor, revoke, block by IP/agent_id
✅ Multiple layers of defense
```

**Much better than colony_secret:**

```
Current: colony_secret leaked → unlimited access, no audit trail
New:     CA fingerprint public → referral ticket required (rate-limited, policy-controlled, audited)
         + Per-agent certificates (revocable, expire in 90 days)
         + Defense in depth (fingerprint + ticket + policy + monitoring)
```

### Private Key Protection

- **Root CA private key**: Offline/HSM storage, only for intermediate issuance
- **Intermediate CA keys**: Used day-to-day, rotated annually
- **Policy signing key**: Stored with colony config, used for policy updates
- **Discovery ticket signing key**: Stored at Discovery, used for JWT issuance
- **Agent private keys**: 0600 permissions, never transmitted, owned by agent
  user

### Audit Logging

**Colony:**

- Log certificate issuance (agent_id, serial_number, ticket_jti, expiry,
  timestamp)
- Log certificate renewal attempts
- Log certificate revocations
- Log invalid referral tickets (signature, expiry, claim mismatches)
- Log policy updates and pushes to Discovery
- Log fallback to `colony_secret` during migration
- Alert on high certificate issuance rates
- Alert on invalid ticket patterns

**Discovery:**

- Log referral ticket issuance (colony_id, agent_id, source_ip, timestamp)
- Log rate limit violations (agent_id, source_ip, limit type)
- Log quota violations (colony_id, quota type)
- Log denied requests (agent_id validation, IP blocklist)
- Log policy updates (colony_id, policy_version, timestamp)
- Alert on high ticket issuance rates from single IP
- Alert on suspicious agent_id patterns

## Migration Strategy

**Rollout Phases**:

1. **Deploy Colony with Hierarchical CA**:
    - Run `coral colony ca migrate-to-hierarchical` (if needed)
    - Generate Root + Intermediate CAs
    - Display Root CA fingerprint

2. **Deploy Agents with Bootstrap Capability**:
    - New agents use `CORAL_CA_FINGERPRINT`
    - Existing agents continue using `colony_secret`
    - Feature flag: `security.bootstrap.enabled=true` (default)

3. **Gradual Migration**:
    - Monitor bootstrap success rate via telemetry
    - Identify and fix failed bootstrap attempts
    - **Existing agents**: Two migration paths
        - **Restart-based**: Agent re-bootstraps automatically on restart
        - **Online migration**: Agent detects new CA, initiates bootstrap without
          restart
    - Rolling restart strategy for Kubernetes deployments

4. **Enforcement** (Future):
    - After all agents bootstrapped, enforce mTLS-only
    - Disable `colony_secret` authentication
    - Reject non-certificate connections

### Migration Strategy for Existing Agents

**Scenario 1: Agent Restart (Preferred)**

1. Agent starts, detects no certificate at `~/.coral/certs/<agent-id>.crt`
2. Agent attempts bootstrap flow (referral ticket → CSR → certificate)
3. If bootstrap succeeds, agent uses certificate
4. If bootstrap fails, agent falls back to `colony_secret` (if
   `fallback_to_secret=true`)
5. Agent logs migration status for monitoring

**Scenario 2: Online Migration (No Restart)**

1. Running agent detects Colony now supports certificate authentication (via
   heartbeat response)
2. Agent initiates background bootstrap process
3. Agent continues using `colony_secret` during bootstrap
4. After successful bootstrap, agent switches to certificate authentication
5. `colony_secret` remains as fallback

**Monitoring Migration Progress:**

```bash
# Check migration status across all agents
coral colony agents list --show-auth-method

# Output:
# AGENT_ID          AUTH_METHOD    CERT_EXPIRES
# web-prod-1        certificate    2025-02-18
# web-prod-2        colony_secret  -
# worker-1          certificate    2025-02-17
# worker-2          colony_secret  -

# Migration progress: 50% (2/4 agents using certificates)
```

**Backward Compatibility**:

- Agents with `security.bootstrap.enabled=false` continue using `colony_secret`
- Agents with `security.bootstrap.fallback_to_secret=true` fall back on failure
- No breaking changes to existing agent deployments
- Colony accepts both certificate and `colony_secret` authentication

**Rollback Plan**:

- Set `security.bootstrap.enabled=false` in agent config
- Agents revert to `colony_secret` authentication
- Certificates remain valid for future use
- No data loss or service interruption

## Operational Diagnostics

### Debug Commands

**Agent-Side Debugging:**

```bash
# Display certificate chain and validation details
coral agent debug-ca

# Output:
# Root CA Fingerprint:
#   Expected: sha256:a3f2e1d4c5b6...
#   Received: sha256:a3f2e1d4c5b6...
#   Status: ✓ Match
#
# Colony Identity:
#   Expected: my-app-prod-a3f2e1
#   Server SAN: spiffe://coral/colony/my-app-prod-a3f2e1
#   Status: ✓ Match
#
# Certificate Chain:
#   [Server Cert] CN=my-app-prod-a3f2e1
#     ↓ signed by
#   [Server Intermediate] CN=Coral Server Intermediate CA
#     ↓ signed by
#   [Root CA] CN=Coral Root CA
#   Status: ✓ Valid

# Test bootstrap without actually bootstrapping
coral agent test-bootstrap --dry-run

# Output:
# ✓ Discovery reachable (https://discovery.coral.io:8080)
# ✓ Referral ticket obtained (expires in 60s)
# ✓ Colony reachable (https://colony.example.com:9000)
# ✓ Root CA fingerprint matches
# ✓ Colony ID matches server certificate
# ✗ Bootstrap would succeed (dry-run, not executed)
```

**Colony-Side Debugging:**

```bash
# List certificate issuance history
coral colony certs list --last-24h

# Output:
# TIMESTAMP            AGENT_ID      SERIAL            TYPE     JTI
# 2025-11-21 10:30:00  web-prod-1    a3f2e1d4...       bootstrap abc123...
# 2025-11-21 11:45:00  worker-1      c5b6a7f8...       bootstrap def456...
# 2025-11-21 14:20:00  web-prod-1    e9d0c1b2...       renewal   -

# Check referral ticket validation failures
coral colony logs --filter "invalid_referral_ticket" --last-1h

# Display JWKS cache status
coral colony jwks status

# Output:
# JWKS Cache Status:
#   Last Fetch: 2025-11-21 15:30:00 (5 minutes ago)
#   Keys Cached: 2
#     - discovery-2024-11-21 (current)
#     - discovery-2024-10-21 (grace period)
#   Next Refresh: 2025-11-21 16:30:00 (in 55 minutes)
#   Status: ✓ Healthy
```

### Sample Log Messages

**Successful Bootstrap:**

```
INFO  agent bootstrap starting agent_id=web-prod-1 colony_id=my-app-prod-a3f2e1
INFO  referral ticket obtained from Discovery jti=abc123... expires_in=60s
INFO  colony connection established endpoint=https://colony.example.com:9000
INFO  root CA fingerprint validated expected=sha256:a3f2e1d4... received=sha256:a3f2e1d4... status=match
INFO  colony identity validated expected=my-app-prod-a3f2e1 san=spiffe://coral/colony/my-app-prod-a3f2e1 status=match
INFO  certificate received serial=a3f2e1d4... expires=2025-02-18
INFO  bootstrap complete auth_method=certificate
```

**Failed Bootstrap (Fingerprint Mismatch):**

```
ERROR agent bootstrap failed agent_id=web-prod-1 reason=fingerprint_mismatch
ERROR root CA fingerprint mismatch expected=sha256:a3f2e1d4... received=sha256:ffffffff...
ERROR possible MITM attack detected aborting_connection=true
WARN  falling back to colony_secret auth_method=colony_secret
```

**Failed Bootstrap (Discovery Unreachable):**

```
WARN  agent bootstrap failed agent_id=web-prod-1 reason=discovery_unreachable attempt=1/10
INFO  retrying bootstrap in 2s backoff=exponential
ERROR agent bootstrap failed after 10 attempts reason=discovery_unreachable total_time=5m30s
WARN  falling back to colony_secret auth_method=colony_secret
INFO  continuing bootstrap attempts in background retry_interval=1h
```

### Error Codes

**Bootstrap Errors:**

- `FINGERPRINT_MISMATCH`: Root CA fingerprint doesn't match expected value
- `COLONY_ID_MISMATCH`: Colony ID in server certificate SAN doesn't match
  expected
- `DISCOVERY_UNREACHABLE`: Cannot connect to Discovery service
- `DISCOVERY_DENIED`: Discovery rejected agent (policy violation)
- `REFERRAL_EXPIRED`: Referral ticket expired before use
- `COLONY_UNREACHABLE`: Cannot connect to Colony service
- `INVALID_CERTIFICATE`: Colony returned invalid certificate

**Validation Errors:**

- `INVALID_JTI`: Duplicate JTI detected (replay attack)
- `INVALID_SIGNATURE`: JWT signature validation failed
- `JWKS_UNAVAILABLE`: Cannot fetch JWKS from Discovery
- `EXPIRED_TOKEN`: Referral ticket expired
- `CLAIM_MISMATCH`: JWT claims don't match CSR or colony identity

## Deferred Features

The following features are important but deferred to separate RFDs to keep this
RFD focused on the critical security improvements. Each deserves its own design
and implementation consideration.

### **Storage Security Enhancements** (RFD xxx)

**Problem**: Current storage paths (`~/.coral/`) may be accidentally committed
to git or backed up to cloud storage.

**Proposed Solution**:

- Migrate to XDG Base Directory specification
    - `~/.local/share/coral/` for private keys (chmod 700)
    - `~/.config/coral/` for non-sensitive config
- Auto-generated `.gitignore` files in sensitive directories
- Support for PKCS#11 HSMs (YubiHSM 2, SoftHSM)
- Support for age-encrypted private keys
- Better permission handling and validation

**Benefits**: Reduces risk of accidental key leakage, production-ready HSM
integration.

### **Discovery Policy Caching** (RFD xxx)

**Problem**: Every bootstrap requires Discovery interaction, creating a
bottleneck and single point of failure.

**Proposed Solution**:

- Option 1: Cached micro-policies
    - Discovery issues signed policy fragments (10-60 minute TTL)
    - Colony caches and validates locally
    - Example: "Allow agent_id matching pattern ^web-.* from 10.0.0.0/8"
- Option 2: Batch ticket pre-authorization
    - Colony requests batch authorization from Discovery
    - Colony issues individual tickets locally
    - Discovery provides revocation lists

**Benefits**: Reduces Discovery load, increases resiliency, better scaling to
10k+ agents.

### **Colony Registration Confirmation** (RFD xxx)

**Problem**: If attacker compromises workstation with root-ca.key, they can
push corrupted policy.

**Proposed Solution**:

- Manual confirmation on first colony registration
    - Display confirmation code during `coral colony init`
    - Require verification via Discovery UI or API
    - 5-minute timeout for confirmation
- Or: Signed "colony ownership proof" using separate bootstrap key
- Optional: HSM-backed root CA for production environments

**Benefits**: Prevents silent colony hijacking, defense against compromised
workstations.

### **Full SPIFFE Integration** (RFD xxx)

**Problem**: Currently only using SPIFFE IDs in certificate SANs. Full SPIFFE
integration enables service mesh compatibility.

**Proposed Solution**:

- SPIFFE Workload API implementation
- Integration with Envoy, Istio, Cilium Service Mesh
- SPIFFE Federation for cross-colony trust
- Workload attestation and validation
- SPIRE-compatible trust domain

**Benefits**: Seamless integration with modern service mesh platforms, broader
ecosystem compatibility.

### **Certificate Revocation (CRL/OCSP)** (RFD xxx)

**Problem**: No mechanism for agents to check certificate revocation status.

**Proposed Solution**:

- Colony publishes CRL at well-known URL
    - `https://colony.example.com/.well-known/crl.pem`
    - Updated every hour or on revocation events
- Optional: OCSP stapling support
    - Colony provides OCSP responses
    - Agents validate during TLS handshake
- Agents check CRL before trusting colony certificates
- Integration with certificate renewal flow

**Benefits**: Proper revocation checking, compliance with PKI best practices.

### **Enhanced Audit Logging & Monitoring** (RFD xxx)

**Problem**: Current logging is basic. Production needs structured logs,
metrics, and anomaly detection.

**Proposed Solution**:

- Structured logging with standard fields:
    - Fingerprints, policy versions, CSR subjects
    - Rate limit counters, ticket JTIs
    - Certificate serial numbers, lifetimes
- Metrics and dashboards:
    - Bootstrap success/failure rates by colony
    - Referral ticket issuance rates
    - Certificate renewal rates
    - Anomaly detection (unusual agent_id patterns, IP changes)
- Integration with observability platforms:
    - Prometheus metrics export
    - OpenTelemetry tracing
    - Grafana dashboards

**Benefits**: Production-grade observability, security monitoring, compliance
auditing.

### **mTLS Enforcement** (RFD xxx)

**Problem**: During migration, both certificates and `colony_secret` are
accepted. Need eventual enforcement.

**Proposed Solution**:

- Phase 1: Monitor certificate adoption rate
- Phase 2: Deprecation warnings for `colony_secret` usage
- Phase 3: Enforcement mode (reject non-certificate connections)
- Phase 4: Remove `colony_secret` authentication code
- Certificate-based authorization policies (replace agent_id allowlists)

**Benefits**: Complete migration to per-agent cryptographic identity, improved
security posture.

### **Advanced Bootstrap Features**

**Multi-Colony Bootstrap**:

- Agent connects to multiple colonies simultaneously
- Separate certificates for each colony
- Colony affinity and routing

**Certificate-Based Agent Migration**:

- Migrate agents between colonies without re-bootstrapping
- Transfer cryptographic identity
- Colony approval workflow

**Automated Certificate Rotation on Compromise**:

- Detect compromised certificates
- Automatic revocation and re-issuance
- Alerting and incident response integration

---

## Implementation Status

**Core Capability:** ⏳ Not Started

**Dependencies:**

- RFD 022 updates (hierarchical CA generation)
- See: `docs/CA-FINGERPRINT-DESIGN.md` for detailed design

**What This Enables:**

- Zero-touch agent deployment with automatic certificate provisioning
- Per-agent cryptographic identity without manual certificate management
- Foundation for mTLS enforcement and `colony_secret` deprecation
- Transparent intermediate CA rotation
- Generic `coral` binary for all colonies~~
