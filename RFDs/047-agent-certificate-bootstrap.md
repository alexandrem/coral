---
rfd: "047"
title: "Agent Certificate Bootstrap"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: []
related_rfds: [ "022" ]
areas: [ "security", "agent" ]
---

# RFD 047 - Agent Certificate Bootstrap

**Status:** 🚧 Draft

## Summary

Implement agent-side certificate bootstrap using **Root CA fingerprint validation**,
enabling agents to automatically obtain mTLS certificates on first connection. Agents
use the colony's Root CA fingerprint (distributed via configuration) to validate the
colony's identity, generate CSRs, request certificates from Colony's auto-issuance
endpoint, and store certificates securely for all subsequent communication. This
eliminates the need for per-agent bootstrap tokens while maintaining MITM protection,
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

Implement agent bootstrap using **Root CA fingerprint validation** instead of JWT
tokens. Colony generates a hierarchical CA during initialization (Root → Intermediates),
and agents validate the colony's identity by comparing the Root CA fingerprint from the
TLS handshake against the expected value from configuration.

**Key Design Decisions**

- **Root CA fingerprint validation**: Agents validate colony identity using SHA256
  fingerprint of Root CA (like SSH host key fingerprints or Kubernetes
  `--discovery-token-ca-cert-hash`).
- **No bootstrap tokens**: Colony auto-issues certificates on valid CSRs, eliminating
  per-agent token generation and tracking.
- **Hierarchical CA**: Three-level PKI (Root → Bootstrap Intermediate → Server cert,
  Root → Agent Intermediate → Client certs) enables transparent intermediate rotation.
- **Generic binary**: Same `coral` binary works with any colony (no embedded trust
  anchors).
- **Auto-issuance**: Colony automatically signs CSRs without token validation,
  rate-limited to prevent abuse.
- **Graceful degradation**: During rollout, agents fall back to `colony_secret` if
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
    │   └─ Colony TLS Server Certificate
    ├─ Agent Intermediate CA (1-year)
    │   └─ Signs agent client certificates
    └─ Policy Signing Certificate (10-year)
        └─ Signs policy documents

Root CA Fingerprint (distribute to agents):
  sha256:a3f2e1d4c5b6a7f8e9d0c1b2a3f4e5d6c7b8a9f0e1d2c3b4a5f6e7d8c9b0a1f2

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
           │      If match → Trust established
           │
           ├─ 6. Validate certificate chain integrity
           │      Verify: Server cert → Intermediates → Root CA
           │
           ├─ 7. Save validated Root CA to ~/.coral/certs/root-ca.crt
           │
           ├─ 8. Generate Ed25519 keypair locally
           │      → Private key: ~/.coral/certs/<agent-id>.key (0600)
           │
           ├─ 9. Create CSR with CN=<agent-id>, O=<colony-id>
           │
           ├─ 10. Request certificate from Colony with referral ticket
           │       POST /coral.colony.v1.ColonyService/RequestCertificate
           │       Body: {csr, referral_ticket}
           │       → Colony validates JWT ticket
           │       → Colony issues certificate
           │       → Returns: certificate + CA chain
           │
           ├─ 11. Store certificate (0644) and key (0600)
           │       ~/.coral/certs/<agent-id>.crt
           │       ~/.coral/certs/<agent-id>.key
           │
           └─ 12. Connect to Colony with mTLS
                   (All subsequent RPCs use client certificate)
```

## Colony CA Hierarchy

### Three-Level PKI Structure

```
Root CA (10-year validity, offline/HSM)
  ├─ Bootstrap Intermediate CA (1-year, rotatable)
  │   └─ Colony TLS Server Certificate
  │       └─ Used for HTTPS endpoint (agents validate this chain)
  │
  ├─ Agent Intermediate CA (1-year, rotatable)
  │   └─ Agent Client Certificates
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
- **Flexibility**: Can issue new intermediates/certificates for different purposes
- **Colony ID reservation**: Policy cert chains to Root CA, locking colony IDs
- **Best Practice**: Follows X.509/RFC 5280 standards

### Colony Initialization

```bash
$ coral colony init my-app-prod

Initializing colony: my-app-prod...

Generated Certificate Authority:
  Root CA:                ~/.coral/colonies/my-app-prod/ca/root-ca.crt
  Root CA Key:            ~/.coral/colonies/my-app-prod/ca/root-ca.key (SECRET)
  Bootstrap Intermediate: ~/.coral/colonies/my-app-prod/ca/bootstrap-intermediate.crt
  Agent Intermediate:     ~/.coral/colonies/my-app-prod/ca/agent-intermediate.crt

Root CA Fingerprint (distribute to agents):
  sha256:a3f2e1d4c5b6a7f8e9d0c1b2a3f4e5d6c7b8a9f0e1d2c3b4a5f6e7d8c9b0a1f2

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

ca:
  root:
    certificate: ~/.coral/colonies/my-app-prod/ca/root-ca.crt
    private_key: ~/.coral/colonies/my-app-prod/ca/root-ca.key
    fingerprint: sha256:a3f2e1d4c5b6a7f8e9d0c1b2a3f4e5d6c7b8a9f0e1d2c3b4a5f6e7d8c9b0a1f2

  bootstrap_intermediate:
    certificate: ~/.coral/colonies/my-app-prod/ca/bootstrap-intermediate.crt
    private_key: ~/.coral/colonies/my-app-prod/ca/bootstrap-intermediate.key
    expires_at: 2025-11-21

  agent_intermediate:
    certificate: ~/.coral/colonies/my-app-prod/ca/agent-intermediate.crt
    private_key: ~/.coral/colonies/my-app-prod/ca/agent-intermediate.key
    expires_at: 2025-11-21

tls:
  certificate: ~/.coral/colonies/my-app-prod/ca/server.crt
  private_key: ~/.coral/colonies/my-app-prod/ca/server.key

certificate_issuance:
  auto_issue: true
  rate_limits:
    per_agent_per_hour: 10
    per_colony_per_hour: 1000

policy:
  signing_key: ~/.coral/colonies/my-app-prod/ca/policy-key.key
  signing_key_id: policy-key-a3f2e1
```

## Policy-Based Authorization

### Problem: Unrestricted Certificate Issuance

With only CA fingerprint validation, any entity with the fingerprint can request unlimited certificates:

```
Attacker has CA fingerprint
→ Submits CSRs to colony
→ Colony auto-issues certificates
→ ⚠️ No authorization layer, only rate limiting at colony
```

### Solution: Discovery Referral Tickets

Add an authorization layer where Discovery issues short-lived **referral tickets** that Colony validates before issuing certificates. Colony stores signed authorization policies at Discovery during initialization, enabling Discovery to enforce colony-specific rules.

### Policy Document

Colony defines and signs authorization policies during initialization:

```yaml
# Policy pushed to Discovery (signed by colony)
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
    allowed_prefixes: ["web-", "worker-", "db-"]
    denied_patterns: ["test-*", "dev-*"]
    max_length: 64
    regex: "^[a-z0-9][a-z0-9-]*[a-z0-9]$"

  allowed_cidrs:
    - "10.0.0.0/8"
    - "172.16.0.0/12"

csr_policies:
  allowed_key_types: ["ed25519", "ecdsa-p256"]
  max_validity_days: 90

signature: "base64-encoded-ed25519-signature"
```

### Bootstrap Flow with Referral Tickets

```
Agent → Discovery: RequestReferralTicket(colony_id, agent_id)
           Discovery loads colony policy
           Discovery checks: rate limits, quotas, agent_id policy, IP allowlists
           Discovery → Agent: JWT ticket (1-minute TTL)

Agent → Colony: RequestCertificate(CSR, referral_ticket)
           Colony validates JWT signature (Discovery public key)
           Colony validates ticket expiry and claims
           Colony issues certificate

Agent → Colony: RegisterAgent (mTLS)
```

### Security Properties

**Defense in depth:**
1. **CA fingerprint**: Prevents MITM attacks during bootstrap
2. **Referral ticket**: Adds authorization layer before certificate issuance
3. **Policy enforcement**: Colony-defined rules enforced by Discovery
4. **Rate limiting**: Prevents mass registration attacks at Discovery layer
5. **Monitoring**: Detects suspicious patterns and alerts operators

**Attack scenarios:**

| Attack | Protection |
|--------|-----------|
| **Discovery MITM** | Agent validates Root CA fingerprint, aborts on mismatch ✅ |
| **CA fingerprint leaked** | Need referral ticket (rate-limited, policy-controlled) ✅ |
| **Fake agent registration** | Discovery enforces quotas, agent ID policies, IP allowlists ✅ |
| **Mass registration attack** | Per-IP rate limits, per-colony quotas ✅ |
| **Referral ticket stolen** | 1-minute TTL, agent_id binding, single-use (tracked by jti) ✅ |

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
  colony_id: <base64: my-app-prod-a3f2e1>
  ca_fingerprint: <base64: sha256:a3f2e1d4c5b6...>
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  template:
    spec:
      containers:
      - name: coral-agent
        image: coral/agent:latest
        env:
        - name: CORAL_COLONY_ID
          valueFrom:
            secretKeyRef:
              name: coral-colony-ca
              key: colony_id
        - name: CORAL_CA_FINGERPRINT
          valueFrom:
            secretKeyRef:
              name: coral-colony-ca
              key: ca_fingerprint
        volumeMounts:
        - name: coral-certs
          mountPath: /var/lib/coral/certs
      volumes:
      - name: coral-certs
        emptyDir: {}  # Or persistentVolumeClaim for daemonsets
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
    - Generate Bootstrap Intermediate CA (1-year validity)
    - Generate Agent Intermediate CA (1-year validity)
    - Generate policy signing certificate (signed by Root CA, 10-year validity)
    - Generate policy signing Ed25519 keypair
    - Generate colony TLS server certificate
    - Compute and display Root CA fingerprint
    - Save CA hierarchy with proper permissions

2. **Colony Policy Management** (`internal/colony/policy/`)
    - Define default authorization policies
    - Sign policies with Ed25519 policy signing key
    - Push signed policies to Discovery via `UpsertColonyPolicy` RPC
    - Update policies and re-push to Discovery
    - Validate policy structure and constraints

3. **Discovery Policy Storage** (`internal/discovery/policy/`)
    - Accept and validate signed colony policies
    - Verify policy certificate chain (Policy Cert → Root CA)
    - Register new colonies (lock colony_id to Root CA fingerprint)
    - Verify Root CA fingerprint matches for existing colonies
    - Detect and prevent colony impersonation attempts
    - Verify Ed25519 signatures on policies using public key from validated certificate
    - Store policies in database with versioning and certificates
    - Store colony registrations (colony_id → Root CA mapping)
    - Retrieve policies for referral ticket issuance
    - Expire old policies based on `expires_at`

4. **Discovery Referral Tickets** (`internal/discovery/referral/`)
    - Issue short-lived JWT referral tickets (1-minute TTL)
    - Enforce rate limits (per-agent, per-IP, per-colony)
    - Track quotas (max active agents, new agents per day)
    - Validate agent IDs against policy (regex, prefixes, deny patterns)
    - Check IP allowlists/denylists
    - Monitor and alert on suspicious patterns
    - Sign tickets with Discovery's Ed25519 key

5. **CA Fingerprint Validator** (`internal/agent/bootstrap/ca_validator.go`)
    - Extract Root CA from TLS certificate chain
    - Compute SHA256 fingerprint of Root CA
    - Compare against expected fingerprint from config
    - Validate certificate chain integrity (intermediates → root)
    - Abort connection on mismatch (MITM detection)

6. **Agent Bootstrap Client** (`internal/agent/bootstrap/client.go`)
    - Request referral ticket from Discovery first
    - Handle rate limit and permission errors gracefully
    - Query Discovery for colony endpoints
    - Connect to colony with `InsecureSkipVerify` (manual validation)
    - Validate Root CA fingerprint using CA validator
    - Save validated Root CA to disk
    - Generate Ed25519 keypairs using `crypto/ed25519`
    - Create X.509 CSRs with CN=agent_id, O=colony_id
    - Call Colony's `RequestCertificate` RPC with referral ticket
    - Validate received certificate matches our public key
    - Store certificates securely with proper permissions

7. **Agent Certificate Manager** (`internal/agent/certs/manager.go`)
    - Check certificate existence and validity on startup
    - Load certificates for gRPC client TLS configuration
    - Load Root CA for colony server validation
    - Monitor certificate expiry (trigger renewal at 30 days)
    - Handle certificate storage and file permissions
    - Provide certificate metadata for status commands

8. **Agent Connection Setup** (`internal/agent/connection.go`)
    - Attempt certificate-based connection first
    - Fall back to `colony_secret` if bootstrap fails (during migration)
    - Configure gRPC client with mTLS transport credentials
    - Validate Colony server certificate against pinned Root CA

9. **Colony Certificate Issuance** (`internal/colony/ca/issuer.go`)
    - Validate referral ticket (JWT signature and claims)
    - Verify ticket signature with Discovery public key
    - Verify agent_id and colony_id match ticket claims
    - Check ticket expiration (should be within 1 minute)
    - Auto-issue certificates on valid CSRs + tickets
    - Validate CSR signature and structure
    - Extract agent_id from CN field
    - Sign with Agent Intermediate CA (90-day validity)
    - Store certificate metadata + ticket JTI in database
    - Monitor and alert on invalid tickets
    - Return certificate + full CA chain

10. **CLI Agent Commands** (`internal/cli/agent/`)
    - `coral agent bootstrap` - Manually trigger bootstrap flow
    - `coral agent cert status` - Display certificate info
    - `coral agent cert renew` - Manually renew certificate

11. **CLI Colony Commands** (`internal/cli/colony/`)
    - `coral colony ca status` - Display CA hierarchy info
    - `coral colony ca rotate-intermediate` - Rotate intermediate CAs
    - `coral colony policy show` - Display current policy
    - `coral colony policy update` - Update and push new policy
    - `coral colony policy push` - Push policy to Discovery

## Implementation Plan

### Phase 1: Colony CA Infrastructure

- [ ] Implement `internal/colony/ca/init.go`
    - Root CA generation (10-year validity)
    - Bootstrap Intermediate CA generation
    - Agent Intermediate CA generation
    - Policy signing certificate generation (signed by Root CA, 10-year validity)
    - Policy signing Ed25519 keypair generation
    - Root CA fingerprint computation
    - Save CA hierarchy with proper permissions
- [ ] Update `coral colony init` to generate CA hierarchy
- [ ] Add `coral colony ca status` command
- [ ] Add `coral colony ca rotate-intermediate` command
- [ ] Add unit tests for CA generation

### Phase 2: Colony Policy Management

- [ ] Implement `internal/colony/policy/policy.go`
    - Define policy structures
    - Implement default policies
    - Policy signing with Ed25519
    - Policy serialization and validation
- [ ] Implement `internal/colony/policy/push.go`
    - Push policies to Discovery
    - Handle policy updates
    - Version management
- [ ] Add `coral colony policy show` command
- [ ] Add `coral colony policy update` command
- [ ] Add `coral colony policy push` command
- [ ] Add unit tests for policy signing and validation

### Phase 3: Discovery Service Policy Storage

- [ ] Implement `internal/discovery/policy/store.go`
    - Accept and validate signed policies with certificates
    - Verify policy certificate chain (Policy Cert → Root CA)
    - Implement colony registration (colony_id → Root CA fingerprint locking)
    - Verify Root CA fingerprint for existing colonies
    - Detect and log colony impersonation attempts
    - Verify Ed25519 signatures using public key from validated certificate
    - Store policies in database with certificates
    - Policy expiration handling
- [ ] Add `UpsertColonyPolicy` RPC endpoint with certificate validation
- [ ] Add `GetColonyPolicy` RPC endpoint
- [ ] Add database schema for colony registrations
- [ ] Add database schema for policy storage with certificates
- [ ] Add unit tests for certificate chain validation
- [ ] Add unit tests for colony registration and impersonation detection

### Phase 4: Discovery Service Referral Tickets

- [ ] Implement `internal/discovery/referral/ticket.go`
    - JWT ticket issuance with Ed25519
    - Rate limiting (per-agent, per-IP, per-colony)
    - Quota tracking
    - Agent ID validation
    - IP allowlist/denylist checks
- [ ] Add `RequestReferralTicket` RPC endpoint
- [ ] Add database schema for rate limit tracking
- [ ] Add monitoring and alerting
- [ ] Add unit tests for ticket issuance

### Phase 5: CA Fingerprint Validation

- [ ] Implement `internal/agent/bootstrap/ca_validator.go`
    - Extract Root CA from TLS connection
    - Compute SHA256 fingerprint
    - Compare against expected value
    - Validate certificate chain integrity
    - Log detailed errors on mismatch
- [ ] Add `CORAL_CA_FINGERPRINT` environment variable support
- [ ] Add configuration field `security.ca_fingerprint`
- [ ] Add unit tests for fingerprint validation

### Phase 6: Agent Bootstrap Implementation

- [ ] Implement `internal/agent/bootstrap/referral.go`
    - Request referral ticket from Discovery
    - Handle rate limit errors
    - Handle permission denied errors
- [ ] Implement `internal/agent/bootstrap/client.go`
    - Query Discovery for endpoints
    - CA fingerprint validation before CSR submission
    - Ed25519 keypair generation
    - CSR creation with proper CN/SAN
    - Certificate request with referral ticket
    - Certificate storage
- [ ] Add retry logic with exponential backoff
- [ ] Add unit tests for bootstrap client

### Phase 7: Colony Certificate Issuance

- [ ] Update `internal/colony/ca/issuer.go`
    - Validate referral ticket (JWT)
    - Verify ticket signature with Discovery public key
    - Verify agent_id and colony_id match ticket
    - Auto-issue on valid CSR + ticket
    - Store ticket JTI in certificate record
    - Certificate tracking in database
- [ ] Fetch Discovery public key on startup
- [ ] Add monitoring/alerting for invalid tickets
- [ ] Add unit tests for ticket validation and issuance

### Phase 8: Agent Connection Integration

- [ ] Update `internal/agent/connection.go`
    - Use certificates for mTLS
    - Root CA validation for colony server
    - Fallback to `colony_secret` during migration
- [ ] Implement certificate manager
- [ ] Add integration tests for mTLS connections

### Phase 9: CLI Commands & Monitoring

- [ ] Implement `coral agent bootstrap` command
- [ ] Implement `coral agent cert status` command
- [ ] Implement `coral agent cert renew` command
- [ ] Implement `coral colony policy show` command
- [ ] Implement `coral colony policy update` command
- [ ] Implement `coral colony policy push` command
- [ ] Add certificate expiry warnings to `coral agent status`
- [ ] Add telemetry for bootstrap success/failure rates
- [ ] Add telemetry for referral ticket issuance rates

### Phase 10: Testing & Documentation

- [ ] Unit tests for all new components
- [ ] Integration test: full bootstrap flow
- [ ] Integration test: intermediate rotation
- [ ] E2E test: MITM detection (wrong fingerprint)
- [ ] E2E test: certificate renewal
- [ ] Update agent deployment documentation
- [ ] Add troubleshooting guide for bootstrap failures

## API Changes

### New Discovery Service APIs

```protobuf
// New: Colony pushes signed authorization policy to Discovery
message UpsertColonyPolicyRequest {
    string colony_id = 1;
    bytes policy = 2;              // JSON-encoded policy document
    bytes signature = 3;           // Ed25519 signature over policy
    bytes policy_certificate = 4;  // Policy signing certificate (signed by Root CA)
    bytes root_ca_certificate = 5; // Root CA certificate (for chain validation)
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
    string ticket = 1;          // JWT referral ticket
    google.protobuf.Timestamp expires_at = 2;
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

### Modified Colony Service API

```protobuf
// Updated: Requires referral ticket from Discovery
message RequestCertificateRequest {
    bytes csr = 1;              // Certificate Signing Request (PEM format)
    string referral_ticket = 2; // JWT from Discovery (1-minute TTL)
}

message RequestCertificateResponse {
    bytes certificate = 1;      // Signed X.509 client certificate (PEM)
    bytes ca_chain = 2;         // Full CA chain (Agent Intermediate + Root CA)
    google.protobuf.Timestamp expires_at = 3;
}
```

### API Summary

**Discovery Service:**
- `UpsertColonyPolicy` (new) - Colony pushes signed policy
- `RequestReferralTicket` (new) - Agent requests authorization ticket
- `LookupColony` (existing) - Agent queries colony endpoints

**Colony Service:**
- `RequestCertificate` (modified) - Agent requests certificate with ticket

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

Validating certificate chain...
✓ Certificate chain verified (Server → Bootstrap Intermediate → Root CA)

Generating keypair...
✓ Ed25519 keypair generated

Creating certificate signing request...
✓ CSR created (CN=web-1, O=my-app-prod)

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

**The Root CA fingerprint is NOT a secret** - it's like an SSH host key fingerprint:

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

| Attack | Protection |
|--------|-----------|
| **Discovery MITM** | Agent validates Root CA fingerprint, aborts on mismatch ✅ |
| **Colony ID leaked** | Cannot push policies without Root CA private key ✅ |
| **Colony ID impersonation** | Discovery locks colony ID to Root CA fingerprint on first registration ✅ |
| **Policy forgery** | Policy must be signed by certificate chaining to registered Root CA ✅ |
| **CA fingerprint leaked** | Need referral ticket (rate-limited, policy-controlled) ✅ |
| **Fake agent registration** | Discovery enforces quotas, agent ID policies, IP allowlists ✅ |
| **Agent certificate stolen** | Individual revocation, expires in 90 days ✅ |
| **Intermediate compromised** | Rotate intermediate, Root CA remains trusted ✅ |
| **Root CA compromised** | Re-initialize colony, new fingerprint (nuclear option) ⚠️ |
| **Referral ticket stolen** | 1-minute TTL, agent_id binding, single-use (tracked by jti) ✅ |
| **Mass registration attack** | Per-IP rate limits, per-colony quotas, monitoring/alerting ✅ |

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
- **Agent private keys**: 0600 permissions, never transmitted, owned by agent user

### Audit Logging

**Colony:**
- Log certificate issuance (agent_id, serial_number, ticket_jti, expiry, timestamp)
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
    - Existing agents re-bootstrap on restart

4. **Enforcement** (Future):
    - After all agents bootstrapped, enforce mTLS-only
    - Disable `colony_secret` authentication
    - Reject non-certificate connections

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

## Deferred Features

**Certificate Renewal** (RFD xxx):
- Automatic renewal at 30 days before expiry
- Renewal failure handling and alerting
- Certificate expiry monitoring and dashboards

**mTLS Enforcement** (RFD xxx):
- Disable `colony_secret` authentication entirely
- Reject non-certificate agent connections
- Certificate-based authorization policies

**Advanced Bootstrap**:
- Multi-colony bootstrap (agent connects to multiple colonies)
- Certificate-based agent migration between colonies
- Automated certificate rotation on key compromise

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
- Generic `coral` binary for all colonies
