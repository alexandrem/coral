---
rfd: "020"
title: "Token-Based Authentication for Agent Registration"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "019" ]
related_rfds: [ "001", "002", "019" ]
database_migrations: [ ]
areas: [ "security", "discovery", "colony", "agent" ]
---

# RFD 020 - Token-Based Authentication for Agent Registration

**Status:** 🚧 Draft

## Summary

Fix the current vulnerability where agent registration happens over plain HTTP,
exposing `colony_secret` credentials in cleartext. Enhance the discovery service
to act as a secure authentication broker that issues time-limited,
cryptographically signed registration tokens. This enables secure agent
registration without TLS certificate management complexity.

This eliminates credential exposure during registration while maintaining
backward compatibility with existing deployments during the transition period.

## Problem

### Current Behavior

Agent registration currently sends `colony_secret` over plain HTTP to the
colony's connect service (port 9000). The colony connect service has no TLS
configuration. This creates a critical security vulnerability during the
bootstrap phase.

### Issues

1. **Plaintext Credential Transmission**: Agent registration sends
   `colony_secret` over plain HTTP (no TLS). Man-in-the-middle attackers can
   intercept the initial registration request and steal the `colony_secret`,
   enabling them to register rogue agents into the mesh.

2. **No TLS on Colony**: Colony connect service (port 9000) has no TLS
   configuration, making credential interception trivial for network attackers.

3. **Long-lived Credentials**: `colony_secret` is a long-lived credential. If
   compromised, attackers can register agents indefinitely until the secret is
   rotated.

4. **WireGuard Only Protects Post-Registration**: WireGuard encryption only
   protects traffic AFTER successful registration, leaving the bootstrap phase
   vulnerable.

### Impact

- **Security Breach Risk**: `colony_secret` exposed to network eavesdropping
  during agent registration. Compromised credential allows attackers to join
  mesh and access all agent communications.
- **Compliance Issues**: Plaintext credential transmission violates security
  best practices and compliance requirements.
- **No Audit Trail**: Cannot track which registration tokens were used by which
  agents.

## Solution

### Key Design Decisions

1. **Enhanced Discovery Service as Secure Authentication Broker**: Upgrade
   discovery service from untrusted registry to trusted authentication provider.
   Discovery service issues time-limited, cryptographically signed registration
   tokens after authenticating agents. Tokens are single-use and expire
   quickly (60s TTL). Colony validates token signature instead of requiring
   direct `colony_secret` transmission.

2. **Cryptographic Token Signing**: Use Ed25519 keypair for token signing.
   Discovery service signs tokens with private key, colonies verify with public
   key. This eliminates the need for shared secrets between discovery and
   colonies.

3. **Time-Limited Single-Use Tokens**: Tokens expire after 60 seconds and can
   only be used once. This limits the attack window and prevents replay attacks.

4. **No TLS Required Initially**: Token-based auth provides security without
   requiring TLS certificate management. TLS can be added later for
   defense-in-depth.

5. **Backward Compatibility**: Support both token-based and legacy
   `colony_secret` authentication during transition period.

### Benefits

- **Secure Bootstrap**: Eliminates `colony_secret` exposure during registration
  with colony. Tokens are time-limited, single-use, and cryptographically
  signed.
- **No TLS Complexity**: No TLS certificate management required for colony
  servers initially.
- **Audit Trail**: Token issuance and consumption can be logged for security
  auditing.
- **Limited Blast Radius**: Compromised token is single-use and time-limited (
  60s).
- **Gradual Migration**: Existing agents continue working during rollout.

### Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│               Discovery Service (Centralized)                   │
│                                                                 │
│  ┌──────────────────────┐      ┌────────────────────────────┐  │
│  │ Token Issuer         │      │ Token Store (Redis)        │  │
│  │                      │─────▶│ - Token → (colony, exp)    │  │
│  │ - Validate creds     │      │ - TTL: 60s                 │  │
│  │ - Issue JWT token    │      │ - Single-use enforcement   │  │
│  │ - Sign with priv key │      └────────────────────────────┘  │
│  └──────────────────────┘                                       │
│         ▲                                                        │
│         │ (1) RequestToken(colony_id, colony_secret)            │
│         │ ◄─── Agent authenticates                              │
│         │                                                        │
│         │ (2) RegistrationToken(token, colony_endpoint)         │
│         │ ───▶ Signed JWT, valid 60s                            │
└─────────┼──────────────────────────────────────────────────────┘
          │
          │
          │ (3) RegisterAgent(token, agent_id, pubkey)
          ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Colony (Public IP)                          │
│                                                                 │
│  ┌────────────────┐         ┌───────────────────────────────┐  │
│  │ Registration   │         │  Discovery Public Key Cache   │  │
│  │ Handler        │────────▶│                               │  │
│  │ (Port 9000)    │         │  - Ed25519 public key         │  │
│  │                │         │  - Fetched on startup         │  │
│  │ - Verify token │         │  - Cached for verification    │  │
│  │   signature    │         └───────────────────────────────┘  │
│  │ - Check expiry │                                            │
│  │ - Consume once │         ┌───────────────────────────────┐  │
│  │                │         │  Consumed Token Cache         │  │
│  │                │────────▶│  - JTI → timestamp            │  │
│  │                │         │  - TTL: 90s (token TTL + grace)│
│  └────────────────┘         └───────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
          │
          │ (4) RegisterResponse(accepted, assigned_ip)
          │
          ▼
┌─────────────────────────────────────────────────────────────────┐
│                         Agent                                   │
│                                                                 │
│  SECURE Flow (NEW):                                             │
│  1. Authenticate with discovery → Get signed token              │
│  2. Create WireGuard interface (no IP, no peers yet)            │
│  3. RegisterAgent(token) over HTTP ──▶ Token validated          │
│  4. Receive permanent mesh IP                                   │
│  5. Assign IP to WireGuard interface                            │
│  6. Add colony as WireGuard peer                                │
│  7. Test mesh connectivity to colony's mesh IP                  │
│                                                                 │
│  ✅ colony_secret only sent to discovery (can add TLS later)    │
│  ✅ Token exposed on wire, but single-use + time-limited        │
└─────────────────────────────────────────────────────────────────┘
```

### Component Changes

#### Discovery Service: Token-Based Authentication

**Upgrade from untrusted registry to secure authentication broker**:

- **New RPC: `RequestRegistrationToken`**
    - Accepts: `colony_id`, `colony_secret` (or agent credentials)
    - Validates credentials against colony registry
    - Issues JWT token signed with discovery service private key
    - Token payload: `{colony_id, agent_id, exp, jti (nonce)}`
    - TTL: 60 seconds

- **Token Store (Redis or in-memory)**:
    - Store issued tokens for single-use enforcement
    - Automatic expiration after TTL
    - Track token consumption to prevent replay

- **Colony Configuration Storage**:
    - Store colony credentials (securely hashed `colony_secret`)
    - Colonies register with discovery using `colony_secret`
    - Discovery validates agent tokens against stored credentials

- **JWT Signing**:
    - Discovery service generates Ed25519 keypair on startup
    - Public key distributed to colonies via discovery metadata endpoint
    - Colonies cache public key for token verification

- **Backward Compatibility**:
    - Support legacy direct colony lookup (deprecated)
    - Gradual migration path for existing deployments

#### Colony: Token Validation

Registration handler validates tokens instead of direct credentials:

- **Fetch Discovery Public Key**: On startup, colony fetches discovery service's
  public key for JWT verification.
- **Token Verification**: Validate JWT signature, expiration, and colony_id
  claim.
- **Single-Use Enforcement**: Track consumed token JTIs to prevent replay (can
  use in-memory cache with TTL).
- **Backward Compatibility**: Support legacy `colony_secret` auth during
  transition period (deprecated).

#### Agent: Secure Token-Based Registration

Agents authenticate with discovery service first, then register with colony:

- **Request Token from Discovery**:
    - Send `RequestRegistrationToken(colony_id, colony_secret)`
    - Receive signed JWT token + colony endpoint
    - Token valid for 60 seconds, single-use

- **Register with Colony**:
    - Send `RegisterAgent(token, agent_id, wireguard_pubkey)` over HTTP
    - Token sent instead of `colony_secret`
    - Colony validates token signature and claims

- **Security Properties**:
    - `colony_secret` only sent to discovery (centralized, can add TLS)
    - Token intercepted on wire is single-use and time-limited
    - No shared secrets between agent and colony after registration

## Implementation Plan

### Phase 1: Enhanced Discovery Service with Token Authentication

- [ ] Design token authentication API (`RequestRegistrationToken` RPC).
- [ ] Implement JWT token generation and signing (Ed25519).
- [ ] Add token store (Redis or in-memory with TTL).
- [ ] Implement colony credential storage (hashed secrets).
- [ ] Add public key distribution endpoint for colonies.
- [ ] Implement single-use token enforcement.
- [ ] Add unit tests for token issuance and validation.
- [ ] Integration tests for authentication flow.
- [ ] Document token flow and security model.

### Phase 2: Colony Token Validation

- [ ] Implement discovery public key fetching on colony startup.
- [ ] Add JWT verification to colony registration handler.
- [ ] Implement token JTI tracking for replay prevention.
- [ ] Support both token and legacy `colony_secret` auth.
- [ ] Add unit tests for token validation logic.
- [ ] Integration tests with discovery service.

### Phase 3: Agent Token-Based Registration

- [ ] Update agent to request token from discovery before registration.
- [ ] Modify `RegisterAgent` to send token instead of `colony_secret`.
- [ ] Handle token expiration and retry logic.
- [ ] Add unit tests for token request flow.
- [ ] E2E tests for secure registration flow.

### Phase 4: Testing and Validation

- [ ] E2E test: agent successfully registers with token-based flow.
- [ ] E2E test: agent fails registration with expired token.
- [ ] E2E test: agent fails registration with replayed token.
- [ ] E2E test: agent retries registration if token expires during setup.
- [ ] Security audit: verify `colony_secret` never sent to colony.
- [ ] Test graceful fallback to legacy auth during transition.

### Phase 5: Deprecation and Cleanup

- [ ] Update RFD 001 (Discovery Service) to reference token authentication.
- [ ] Update RFD 002 (Application Identity) to reference token flow.
- [ ] Document migration path for existing deployments.
- [ ] Deprecate legacy `colony_secret` direct authentication.
- [ ] (After full migration) Remove legacy auth support.

## API Changes

### New Protobuf Messages

```protobuf
// Request a registration token from discovery service
message RequestRegistrationTokenRequest {
    string colony_id = 1;
    string colony_secret = 2;
    string agent_id = 3;
}

message RequestRegistrationTokenResponse {
    string token = 1;              // JWT token signed by discovery service
    string colony_endpoint = 2;     // Colony endpoint for registration
    int64 expires_at = 3;          // Unix timestamp when token expires
}

// Discovery service public key endpoint
message GetDiscoveryPublicKeyRequest {}

message GetDiscoveryPublicKeyResponse {
    string public_key = 1;         // Ed25519 public key (base64)
    string key_id = 2;             // Key identifier for rotation
}
```

### Modified Protobuf Messages

```protobuf
// Update RegisterAgent to accept token OR colony_secret
message RegisterAgentRequest {
    // New field (preferred)
    string registration_token = 1;  // JWT token from discovery service

    // Existing fields (legacy, deprecated)
    string colony_secret = 2;       // Direct secret (deprecated)

    // Existing fields
    string agent_id = 3;
    string wireguard_pubkey = 4;
    // ... other fields
}
```

### New RPC Endpoints

```protobuf
service DiscoveryService {
    // Existing RPCs...

    // New: Request registration token
    rpc RequestRegistrationToken(RequestRegistrationTokenRequest)
        returns (RequestRegistrationTokenResponse);

    // New: Get discovery service public key for token verification
    rpc GetDiscoveryPublicKey(GetDiscoveryPublicKeyRequest)
        returns (GetDiscoveryPublicKeyResponse);
}
```

## Testing Strategy

### Unit Tests

Test token authentication:

- JWT token generation and signing (Ed25519).
- Token expiration validation.
- Token signature verification with invalid keys.
- Single-use token enforcement.
- Token replay attack prevention.
- Colony credential validation (hashed secret comparison).

### Integration Tests

Test discovery service and token flow:

- Agent requests token with valid credentials.
- Agent requests token with invalid credentials (rejected).
- Colony validates valid token (accepts).
- Colony rejects expired token.
- Colony rejects token with invalid signature.
- Colony rejects replayed token (single-use enforcement).
- Discovery service handles concurrent token requests.

### E2E Tests

Test security properties:

- Agent successfully registers with token-based flow.
- Agent fails registration with expired token.
- Agent fails registration with replayed token.
- Agent retries registration if token expires during setup.
- Verify `colony_secret` never sent to colony (only discovery).
- Test graceful fallback to legacy auth during transition.

## Security Considerations

### Token Interception (MITM)

**Threat**: Attacker intercepts registration token via man-in-the-middle attack.

**Mitigation**:

- **Time-Limited Tokens**: 60-second TTL limits exposure window.
- **Single-Use Tokens**: Token consumed after first use, replay attacks blocked.
- **Binding to Agent ID**: Token includes agent ID claim, colony verifies match.
- **Future: Add TLS to Discovery**: Once discovery has TLS, `colony_secret` is
  fully protected.
- **Risk Assessment**: Even if intercepted, attacker must use token within 60s
  and cannot reuse it. Lower risk than current plaintext `colony_secret` which
  is long-lived.

### Discovery Service Compromise

**Threat**: Attacker compromises discovery service, issues malicious tokens.

**Mitigation**:

- **Key Rotation**: Discovery service keypair rotated periodically.
- **Colony Public Key Pinning**: Colonies can pin expected discovery public key.
- **Monitoring**: Track anomalous token issuance patterns.
- **Rate Limiting**: Limit token requests per agent/IP.
- **Future: Mutual TLS**: Discovery authenticates colonies, colonies
  authenticate discovery.

**Risk Assessment**: Discovery compromise is high impact but requires server
breach. Defense-in-depth with monitoring and rate limiting reduces risk.

### Token Replay Attacks

**Threat**: Attacker captures valid token and replays it.

**Mitigation**:

- **JTI (JWT ID) Tracking**: Colony tracks consumed token IDs.
- **Time-Based Cache**: JTI cache expires after token TTL (60s + grace period).
- **Single-Use Enforcement**: First use consumes token, subsequent attempts
  rejected.

### Colony Secret Exposure to Discovery

**Threat**: Discovery service stores colony secrets, creating centralized
target.

**Mitigation**:

- **Hashed Storage**: Colony secrets stored as bcrypt/argon2 hashes.
- **TLS for Discovery**: Add TLS to discovery service (future enhancement).
- **Rate Limiting**: Limit failed authentication attempts.
- **Audit Logging**: Log all token issuance for security monitoring.

## Migration Strategy

### Deployment

**Critical**: Deploy in this specific order to maintain backward compatibility.

1. **Deploy Discovery Service Update**:
    - Add token authentication endpoints.
    - Generate Ed25519 keypair for token signing.
    - Enable token store (Redis or in-memory).
    - Support both legacy and token-based flows.
    - Publish public key endpoint for colonies.

2. **Deploy Colony Update**:
    - Fetch discovery public key for token verification.
    - Support both token-based and legacy `colony_secret` authentication.
    - Existing agents continue working (backward compatible).

3. **Deploy Agent Updates**:
    - Agents request token from discovery before registration.
    - Use token-based registration flow.
    - Old agents with legacy auth continue working.
    - Gradual rollout possible.

4. **Verification**:
    - Monitor registration success rate (both legacy and token-based).
    - Audit token issuance and validation logs.
    - Verify token replay prevention working.

5. **Deprecation** (after all agents migrated):
    - Disable legacy `colony_secret` direct authentication in colonies.
    - Remove support for legacy flow in future versions.

### Rollback Plan

1. **If Token Authentication Issues Detected**:
    - Agents fall back to legacy `colony_secret` authentication automatically.
    - Colony continues accepting both auth methods.
    - Discovery service continues serving legacy endpoints.
    - Investigate token validation or issuance bugs.

2. **Full Rollback** (extreme scenario):
    - Revert discovery service to remove token endpoints.
    - Revert colony to legacy auth only.
    - Revert agents to direct `colony_secret` transmission.

### Backward Compatibility

- **Discovery Service**: Supports both legacy colony lookup and new token
  authentication endpoints simultaneously.
- **Colony**: Accepts both token-based auth and legacy `colony_secret` auth
  during transition.
- **Agent**: Old agents use legacy `colony_secret` authentication. New agents
  use token-based auth.
- **Gradual Migration**: Mixed deployments with old and new agents/colonies work
  correctly.

## Future Enhancements

### TLS for Discovery Service

Add TLS encryption to discovery service for complete end-to-end security:

- **Let's Encrypt Integration**: Automatic certificate provisioning for public
  discovery services.
- **Self-Signed Certificates**: For air-gapped or private network deployments.
- **Certificate Pinning**: Agents pin discovery service certificate or public
  key.
- **Complete Protection**: With TLS + token auth, `colony_secret` never exposed
  in cleartext anywhere.

**Priority**: HIGH - Completes security model.

### Key Rotation

Implement automatic key rotation for discovery service keypair:

- Periodic rotation (e.g., every 30 days).
- Gradual key rollover (support old and new keys during transition).
- Colonies automatically fetch new public key.

### Mutual TLS

Add mutual TLS between discovery and colonies:

- Discovery authenticates colonies.
- Colonies authenticate discovery.
- Prevents rogue discovery services from issuing malicious tokens.

## Appendix

### JWT Token Format

```json
{
    "iss": "coral-discovery",
    "sub": "agent-id-123",
    "aud": "colony-abc",
    "exp": 1234567890,
    "iat": 1234567830,
    "jti": "unique-token-id-xyz",
    "colony_id": "colony-abc"
}
```

### Token Validation Flow

```
Colony receives RegisterAgent(token)
  ↓
Parse JWT token
  ↓
Verify signature with discovery public key
  ↓
Check expiration (exp claim)
  ↓
Verify colony_id matches this colony
  ↓
Check JTI not in consumed cache (replay prevention)
  ↓
Add JTI to consumed cache (TTL: 90s)
  ↓
Process registration
```
