---
rfd: "049"
title: "Discovery-Based Agent Authorization"
state: "implemented"
breaking_changes: true
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "047" ]
related_rfds: [ "047", "048", "086" ]
areas: [ "security", "discovery", "colony" ]
---

# RFD 049 - Discovery-Based Agent Authorization

**Status:** ✅ Implemented

## Summary

This RFD defines the authorization layer for Agent Certificate Bootstrap. While
RFD 048 defines _how_ an agent securely connects (using Root CA fingerprint
validation), this RFD defines _who_ is allowed to connect.

It introduces **Referral Tickets**, a mechanism where the Discovery service
issues short-lived, signed JWTs to authorized agents. The Colony service
validates these tickets before issuing the initial mTLS certificate. This allows
Discovery to serve as an initial gatekeeper for the bootstrap process.

> [!NOTE]
> Advanced per-colony policy enforcement (quotas, IP allowlists, etc.) is
> deferred to RFD 086.

## Problem

- **Unrestricted Issuance**: With only CA fingerprint validation (RFD 048), any
  entity that knows the public fingerprint could potentially request a
  certificate if they can reach the Colony endpoint.
- **DoS Risk**: Without an authorization check, a Colony could be flooded with
  CSR signing requests.

## Solution

Implement an authorization layer where **Discovery issues Referral Tickets** and
**Colony consumes them**.

1. **Agent** requests a ticket from Discovery.
2. **Discovery** signs a JWT using Ed25519 keys.
3. **Agent** presents this JWT to **Colony** along with its CSR.
4. **Colony** validates the JWT against Discovery's public keys (via JWKS) and
   issues the certificate.

### Discovery JWT Key Management

Discovery uses Ed25519 keys for signing referral tickets with automatic rotation
and JWKS publication for Colony validation.

**Key Configuration:**

Discovery generates new Ed25519 keypairs and publishes them via a JWKS endpoint.
Colonies fetch this JWKS to validate tickets.

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
        "jti": "a3f2e1d4-c5b6-a7f8-e9d0-c1b2a3f4e5d6",
        "iat": 1700000000,
        "exp": 1700000060
    }
}
```

**Validation Requirements:**

- Colony MUST verify `exp` is in the future.
- Colony MUST verify `colony_id` matches its own identity.
- Colony MUST verify `agent_id` matches CSR subject CN.
- Colony MUST verify signature using JWKS public keys.

### Bootstrap Flow with Referral Tickets

**First-Time Bootstrap** (requires referral ticket):

1. **Agent → Discovery**: `CreateBootstrapToken(colony_id, agent_id)`
2. **Discovery → Agent**: JWT ticket (1-minute TTL)
3. **Agent → Colony**: `RequestCertificate(CSR, referral_ticket)`
4. **Colony → Discovery**: Fetch JWKS (if not cached)
5. **Colony**: Validate JWT signature and claims
6. **Colony → Agent**: Issued certificate

## Component Changes

### 1. Colony - Referral Ticket Validation

- **JWKS Client** (`internal/colony/jwks/client.go`):
    - Fetch JWKS from Discovery's `/.well-known/jwks.json`.
    - Cache JWKS with refresh logic.
- **Ticket Validator** (`internal/colony/ca/policy.go`):
    - Validate JWT signature and claims using cached JWKS.
    - Integrated with `RequestCertificate` flow.

### 2. Discovery - Referral Ticket Issuance

- **Token Manager** (`internal/discovery/tokens.go`):
    - Generate Ed25519 signed JWTs with 1-minute TTL.
- **Key Manager** (`internal/discovery/keys/manager.go`):
    - Generate and rotate Ed25519 signing keys.
    - Publish JWKS at `/.well-known/jwks.json`.
- **RPC Handler** (`internal/discovery/server/server.go`):
    - Implement `CreateBootstrapToken` RPC.

## API Changes

### Discovery Service (gRPC)

```protobuf
service DiscoveryService {
    // Create a single-use bootstrap token for agent certificate issuance.
    rpc CreateBootstrapToken(CreateBootstrapTokenRequest) returns (CreateBootstrapTokenResponse);
}
```

## Implementation Plan (Completed)

- [x] Ed25519 key generation and rotation (`internal/discovery/keys/manager.go`)
- [x] JWKS endpoint (`/.well-known/jwks.json`)
- [x] Ticket issuer (`internal/discovery/tokens.go`)
- [x] `CreateBootstrapToken` RPC handler
- [x] JWKS client with caching (`internal/colony/jwks/client.go`)
- [x] Ticket validator (`internal/colony/ca/policy.go`)
- [x] Integration with `RequestCertificate` RPC (from RFD 047)
- [x] E2E test: agent bootstrap with referral ticket (
  `tests/e2e/distributed/discovery_auth_test.go`)
- [x] Documentation updates

## Dependencies

- **RFD 047** (Implemented): Colony CA infrastructure.
- **RFD 048**: Agent bootstrap flow (consumes referral tickets).
- **RFD 086**: Advanced Policy Enforcement (extension of this RFD).
