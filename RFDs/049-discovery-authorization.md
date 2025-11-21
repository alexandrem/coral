---
rfd: "049"
title: "Discovery-Based Agent Authorization"
state: "draft"
breaking_changes: true
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "048", "047" ]
related_rfds: [ "048", "047" ]
areas: [ "security", "discovery" ]
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
