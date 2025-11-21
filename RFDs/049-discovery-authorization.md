---
rfd: "049"
title: "Discovery-Based Agent Authorization"
state: "draft"
breaking_changes: true
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "047" ]
related_rfds: [ "047", "048" ]
areas: [ "security", "discovery", "colony" ]
---

# RFD 049 - Discovery-Based Agent Authorization

**Status:** 🚧 Draft

## Summary

This RFD defines the authorization layer for Agent Certificate Bootstrap. While
RFD 048 defines *how* an agent securely connects (using Root CA fingerprint
validation), this RFD defines *who* is allowed to connect.

It introduces **Referral Tickets**, a mechanism where the Discovery service
issues short-lived, signed JWTs to authorized agents. The Colony service
validates these tickets before issuing the initial mTLS certificate. This allows
Discovery to enforce complex, centralized policies (quotas, IP allowlists, agent
ID patterns) without requiring the Colony to maintain global state.

## Problem

- **Unrestricted Issuance**: With only CA fingerprint validation (RFD 048), any
  entity that knows the public fingerprint could potentially request a
  certificate if they can reach the Colony endpoint.
- **Lack of Central Control**: Colonies are independent. There is no central
  place to enforce global policies like "max 1000 agents total" or "only allow
  IPs from 10.0.0.0/8".
- **DoS Risk**: Without an authorization check, a Colony could be flooded with
  CSR signing requests.

## Solution

Implement an authorization layer where **Discovery issues Referral Tickets** and
**Colony consumes them**.

1.  **Agent** authenticates to Discovery (via network location/policy) and
    requests a ticket.
2.  **Discovery** checks policies (quotas, allowlists) and signs a JWT.
3.  **Agent** presents this JWT to **Colony** along with its CSR.
4.  **Colony** validates the JWT against Discovery's public keys and issues the
    certificate.

### Policy-Based Authorization

Colony defines and signs authorization policies during initialization, enabling
Discovery to enforce colony-specific rules.

#### Policy Document

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

1.  **Canonicalization**: Policy is canonicalized using RFC 8785 JSON
    Canonicalization Scheme (JCS) before signing.
2.  **Signing**: Ed25519 signature computed over canonical JSON bytes.
3.  **Verification**: Discovery re-canonicalizes policy and verifies signature
    using public key from policy certificate.

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

1.  Discovery generates new Ed25519 keypair every 30 days.
2.  Old key retained for 7-day grace period (validates existing tickets).
3.  JWKS endpoint publishes both current and previous keys.
4.  Colony fetches JWKS on startup and refreshes hourly.
5.  Colony validates tickets using any key in JWKS.

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
        }
    ]
}
```

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

**Validation Requirements:**

-   Colony MUST verify `exp` is in the future.
-   Colony MUST verify `colony_id` matches its own identity.
-   Colony MUST verify `agent_id` matches CSR subject CN.
-   Colony MUST store `jti` for 60 seconds to prevent replay.
-   Colony MUST verify signature using JWKS public keys.

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

## Security Properties

**Defense in depth:**

1.  **Referral ticket**: Adds authorization layer before certificate issuance.
2.  **Policy enforcement**: Colony-defined rules enforced by Discovery.
3.  **Rate limiting**: Prevents mass registration attacks at Discovery layer.
4.  **Monitoring**: Detects suspicious patterns and alerts operators.

**Attack scenarios:**

| Attack                       | Protection                                                     |
| ---------------------------- | -------------------------------------------------------------- |
| **CA fingerprint leaked**    | Need referral ticket (rate-limited, policy-controlled) ✅      |
| **Fake agent registration**  | Discovery enforces quotas, agent ID policies, IP allowlists ✅ |
| **Mass registration attack** | Per-IP rate limits, per-colony quotas ✅                       |
| **Referral ticket stolen**   | 1-minute TTL, agent_id binding, single-use (tracked by jti) ✅ |
| **Discovery offline**        | Certificate renewals work without Discovery (mTLS auth) ✅     |
| **Policy signature forgery** | RFC 8785 JCS ensures deterministic verification ✅             |

## Component Changes

### 1. Colony - Policy Signing

- **Policy Signer** (`internal/colony/policy/signer.go`):
    - Define `ColonyPolicy` struct (rate limits, agent ID patterns, quotas)
    - Implement RFC 8785 JSON Canonicalization Scheme (JCS)
    - Sign canonical JSON with policy signing key (from RFD 047 CA)
    - Generate policy version and expiration
- **Policy Pusher** (`internal/colony/policy/pusher.go`):
    - Push signed policy to Discovery via `UpsertColonyPolicy` RPC
    - Handle policy versioning and updates
    - Retry logic for Discovery unavailability

### 2. Colony - Referral Ticket Validation

- **JWKS Client** (`internal/colony/jwks/client.go`):
    - Fetch JWKS from Discovery's `/.well-known/jwks.json`
    - Cache JWKS with hourly refresh
    - Support multiple keys for rotation overlap
- **Ticket Validator** (`internal/colony/auth/ticket_validator.go`):
    - Validate JWT signature using cached JWKS
    - Verify claims: `exp`, `colony_id`, `agent_id`, `jti`
    - Track `jti` for 60 seconds to prevent replay
    - Integrate with `RequestCertificate` RPC

### 3. Discovery - Referral Ticket Issuance

- **Ticket Issuer** (`internal/discovery/tickets/issuer.go`):
    - Generate Ed25519 signed JWTs
    - Include claims: `sub`, `aud`, `iss`, `colony_id`, `agent_id`, `source_ip`,
      `jti`, `iat`, `exp`
    - 1-minute TTL
- **Policy Enforcer** (`internal/discovery/policy/enforcer.go`):
    - Load and cache colony policies
    - Verify policy signature against colony's policy certificate
    - Enforce rate limits, quotas, agent ID patterns, IP allowlists
- **Key Manager** (`internal/discovery/keys/manager.go`):
    - Generate and rotate Ed25519 signing keys
    - Publish JWKS at `/.well-known/jwks.json`
    - 30-day rotation with 7-day overlap

### 4. Discovery - Policy Storage

- **Policy Store** (`internal/discovery/policy/store.go`):
    - Store signed policies per colony
    - Verify policy certificate chains to colony Root CA
    - Lock colony ID to Root CA fingerprint on first registration

## API Changes

### Discovery Service (gRPC)

```protobuf
service DiscoveryService {
    // Colony pushes its signed authorization policy.
    rpc UpsertColonyPolicy(UpsertColonyPolicyRequest) returns (UpsertColonyPolicyResponse);

    // Agent requests a referral ticket for certificate bootstrap.
    rpc RequestReferralTicket(RequestReferralTicketRequest) returns (RequestReferralTicketResponse);
}

message UpsertColonyPolicyRequest {
    string colony_id = 1;
    bytes policy_json = 2;       // RFC 8785 canonical JSON
    bytes signature = 3;         // Ed25519 signature over policy_json
    bytes policy_cert = 4;       // Policy signing certificate (from RFD 047)
    bytes root_cert = 5;         // Root CA certificate (for chain verification)
}

message UpsertColonyPolicyResponse {
    int64 policy_version = 1;
    google.protobuf.Timestamp accepted_at = 2;
}

message RequestReferralTicketRequest {
    string colony_id = 1;
    string agent_id = 2;
}

message RequestReferralTicketResponse {
    string referral_ticket = 1;  // Signed JWT (1-minute TTL)
    google.protobuf.Timestamp expires_at = 2;
}
```

### Discovery JWKS Endpoint (HTTP)

```
GET /.well-known/jwks.json

Response:
{
    "keys": [
        {
            "kid": "discovery-2024-11-21",
            "kty": "OKP",
            "crv": "Ed25519",
            "x": "<base64url-encoded-public-key>",
            "use": "sig",
            "alg": "EdDSA"
        }
    ]
}
```

## CLI Commands

### Colony Policy Management

```bash
# Push policy to Discovery
$ coral colony policy push

Signing policy with policy certificate...
  Policy version: 3
  Expires: 2025-12-21

Pushing to Discovery...
✓ Policy accepted (version 3)

# View current policy
$ coral colony policy show

Colony Policy: my-app-prod-a3f2e1
  Version: 3
  Expires: 2025-12-21

Referral Tickets:
  TTL: 60s
  Rate Limits:
    Per agent/hour: 10
    Per IP/hour: 100
    Per colony/hour: 1000

Quotas:
  Max active agents: 10000
  Max new agents/day: 100

Agent ID Policy:
  Allowed prefixes: web-, worker-, db-
  Denied patterns: test-*, dev-*

Allowed CIDRs:
  - 10.0.0.0/8
  - 172.16.0.0/12
```

### Discovery Administration

```bash
# View registered colonies
$ coral-discovery colonies list

COLONY_ID              ROOT_CA_FINGERPRINT    POLICY_VERSION  AGENTS
my-app-prod-a3f2e1     sha256:a3f2e1d4...     3               45
staging-b4c5d6         sha256:b4c5d6e7...     1               12

# View referral ticket stats
$ coral-discovery stats tickets --last-24h

Referral Tickets Issued: 127
  Successful: 120
  Denied (rate limit): 5
  Denied (policy): 2

By Colony:
  my-app-prod-a3f2e1: 89
  staging-b4c5d6: 38
```

## Implementation Plan

### Phase 1: Policy Signing (Colony-Side)

- [ ] Add RFC 8785 JCS library dependency (`github.com/cyberphone/json-canonicalization`)
- [ ] Implement `ColonyPolicy` struct (`internal/colony/policy/types.go`)
- [ ] Implement policy canonicalization and signing (`internal/colony/policy/signer.go`)
- [ ] Implement `UpsertColonyPolicy` client (`internal/colony/policy/pusher.go`)
- [ ] Add `coral colony policy push` command
- [ ] Add `coral colony policy show` command
- [ ] Add unit tests for policy signing

### Phase 2: Policy Verification (Discovery-Side)

- [ ] Implement policy storage (`internal/discovery/policy/store.go`)
- [ ] Implement policy signature verification
- [ ] Implement certificate chain verification (policy cert → root CA)
- [ ] Implement colony ID locking to root CA fingerprint
- [ ] Add `UpsertColonyPolicy` RPC handler
- [ ] Add unit tests for policy verification

### Phase 3: Referral Ticket Issuance (Discovery-Side)

- [ ] Implement Ed25519 key generation and rotation (`internal/discovery/keys/manager.go`)
- [ ] Implement JWKS endpoint (`/.well-known/jwks.json`)
- [ ] Implement ticket issuer (`internal/discovery/tickets/issuer.go`)
- [ ] Implement policy enforcer (rate limits, quotas, agent ID patterns)
- [ ] Add `RequestReferralTicket` RPC handler
- [ ] Add unit tests for ticket issuance

### Phase 4: Referral Ticket Validation (Colony-Side)

- [ ] Implement JWKS client with caching (`internal/colony/jwks/client.go`)
- [ ] Implement ticket validator (`internal/colony/auth/ticket_validator.go`)
- [ ] Implement JTI tracking (60-second replay prevention)
- [ ] Integrate with `RequestCertificate` RPC (from RFD 047)
- [ ] Add unit tests for ticket validation

### Phase 5: Integration & Testing

- [ ] Integration test: full policy push flow
- [ ] Integration test: referral ticket issuance and validation
- [ ] Integration test: rate limiting and quota enforcement
- [ ] E2E test: agent bootstrap with referral ticket
- [ ] E2E test: policy update propagation
- [ ] Documentation updates

## Dependencies

- **RFD 047** (Implemented): Colony CA infrastructure, policy signing certificate
- **RFD 048**: Agent bootstrap flow (consumes referral tickets)

## Migration Strategy

1. **Deploy Discovery**: With referral ticket and policy support
2. **Deploy Colony**: With policy signing and JWKS client
3. **Push Initial Policy**: Colony pushes default policy to Discovery
4. **Gradual Enforcement**: Start with permissive policy, tighten over time
5. **Monitor**: Track ticket issuance rates and policy violations
