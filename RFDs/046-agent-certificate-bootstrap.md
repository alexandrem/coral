---
rfd: "046"
title: "Agent Certificate Bootstrap"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: [ "022" ]
related_rfds: [ "022" ]
areas: [ "security", "agent" ]
---

# RFD 046 - Agent Certificate Bootstrap

**Status:** 🚧 Draft

## Summary

Implement the agent-side certificate bootstrap flow defined in RFD 022, enabling
agents to automatically obtain mTLS certificates on first connection. Agents
will request bootstrap tokens from Discovery, generate CSRs, exchange them for
certificates from Colony, and store certificates securely for all subsequent
communication. This eliminates manual certificate provisioning while maintaining
strong cryptographic identity.

## Problem

- **Current behavior/limitations**:
    - RFD 022 implemented the server-side infrastructure (CA, token issuance,
      certificate signing)
    - Agents still use shared `colony_secret` for authentication
    - No mechanism exists for agents to request or manage certificates
    - Certificate renewal and rotation are manual processes
    - Agents cannot leverage the mTLS infrastructure built in RFD 022

- **Why this matters**:
    - Shared secrets scale poorly and increase security risk
    - Manual certificate provisioning blocks automated agent deployment
    - Without agent-side implementation, the CA infrastructure remains unused
    - Agents cannot benefit from per-agent certificate revocation

- **Use cases affected**:
    - Automated agent deployment and scaling
    - Zero-touch agent provisioning
    - Agent replacement after compromise
    - Certificate-based access control and audit

## Solution

Implement the agent bootstrap client that integrates with RFD 022's server
infrastructure. Agents will automatically request bootstrap tokens, generate
keypairs, submit CSRs, receive certificates, and establish mTLS connections
without operator intervention.

**Key Design Decisions**

- **Automatic bootstrap on first connect**: Agents detect missing certificates
  and automatically initiate the bootstrap flow before attempting Colony
  connection.
- **Ed25519 keypairs**: Use Ed25519 for client certificates (faster than RSA,
  smaller than ECDSA P-256).
- **Secure storage**: Store certificates and keys in
  `~/.coral/certs/<agent-id>.{crt,key}` with 0600 permissions, owned by the
  agent process user.
- **Graceful degradation**: During rollout, agents fall back to `colony_secret`
  if bootstrap fails, logging warnings for operator visibility.
- **Certificate validation**: Agents verify Colony certificates against embedded
  CA bundle before sending bootstrap tokens.

**Benefits**

- Zero-touch agent provisioning with cryptographic identity
- Per-agent certificate revocation capability
- Audit trail of certificate issuance per agent
- Foundation for mTLS enforcement (RFD 031)
- Eliminates shared secret distribution

**Architecture Overview**

```
┌─────────────────────────────────────────────────────────────┐
│ Agent Startup Flow                                          │
└─────────────────────────────────────────────────────────────┘

Agent Start
    ↓
Check for existing cert at ~/.coral/certs/<agent-id>.crt
    ↓
    ├─ Exists & Valid → Use for mTLS connection
    │
    └─ Missing/Expired → Bootstrap Flow:
           │
           ├─ 1. Request bootstrap token from Discovery
           │      POST /coral.discovery.v1.DiscoveryService/CreateBootstrapToken
           │      Body: {agent_id, colony_id, reef_id, intent: "register"}
           │      → Receives JWT token (5-minute TTL)
           │
           ├─ 2. Generate Ed25519 keypair locally
           │      → Private key: ~/.coral/certs/<agent-id>.key (0600)
           │
           ├─ 3. Create CSR with CN=agent.<agent-id>.<colony-id>
           │
           ├─ 4. Request certificate from Colony
           │      POST /coral.colony.v1.ColonyService/RequestCertificate
           │      Body: {jwt, csr}
           │      → Receives certificate + CA chain
           │
           ├─ 5. Validate certificate matches our public key
           │
           ├─ 6. Store certificate (0644) and key (0600)
           │      ~/.coral/certs/<agent-id>.crt
           │      ~/.coral/certs/<agent-id>.key
           │
           └─ 7. Connect to Colony with mTLS
                  (All subsequent RPCs use client certificate)
```

### Component Changes

1. **Agent Bootstrap Client** (`internal/agent/bootstrap/client.go`)
    - Implements token request logic via Discovery client
    - Generates Ed25519 keypairs using `crypto/ed25519`
    - Creates X.509 CSRs with proper CN and SANs
    - Calls Colony's `RequestCertificate` RPC
    - Validates received certificates against CA bundle
    - Stores certificates securely with proper permissions

2. **Agent Certificate Manager** (`internal/agent/certs/manager.go`)
    - Checks certificate existence and validity on startup
    - Loads certificates for gRPC client TLS configuration
    - Monitors certificate expiry (triggers renewal at 30 days remaining)
    - Handles certificate storage and file permissions
    - Provides certificate metadata (expiry, fingerprint) for status commands

3. **Agent Connection Setup** (`internal/agent/connection.go`)
    - Attempts certificate-based connection first
    - Falls back to `colony_secret` if bootstrap fails (during migration)
    - Configures gRPC client with mTLS transport credentials
    - Validates Colony server certificate against embedded CA bundle

4. **CLI Agent Commands** (`internal/cli/agent/`)
    - `coral agent bootstrap` - Manually trigger bootstrap flow (for
      testing/debugging)
    - `coral agent cert status` - Display certificate info (expiry, fingerprint,
      issuer)
    - `coral agent cert renew` - Manually renew certificate before expiry

5. **Agent Configuration** (`internal/config/agent.go`)
    - Add `security.cert_path` (default: `~/.coral/certs/<agent-id>.crt`)
    - Add `security.bootstrap.enabled` (default: true)
    - Add `security.bootstrap.discovery_url` (default: from global config)

**Configuration Example**

```yaml
# ~/.coral/agents/<agent-id>.yaml
security:
    cert_path: ~/.coral/certs/agent-web-1.crt
    key_path: ~/.coral/certs/agent-web-1.key
    bootstrap:
        enabled: true
        discovery_url: https://discovery.coral.io:8080
        fallback_to_secret: true  # During migration only
```

## Implementation Plan

### Phase 1: Certificate Management Foundation

- [ ] Implement `internal/agent/certs/manager.go`
    - Certificate loading and validation
    - File storage with proper permissions
    - Expiry checking
    - Certificate metadata extraction
- [ ] Add configuration fields for certificate paths
- [ ] Add unit tests for certificate manager

### Phase 2: Bootstrap Client Implementation

- [ ] Implement `internal/agent/bootstrap/client.go`
    - Discovery token request
    - Ed25519 keypair generation
    - CSR creation with proper CN/SAN
    - Colony certificate request
    - Certificate validation
- [ ] Add embedded CA bundle to agent binary (from Colony)
- [ ] Add retry logic with exponential backoff
- [ ] Add unit tests for bootstrap client

### Phase 3: Agent Connection Integration

- [ ] Update `internal/agent/connection.go` to use certificates
- [ ] Implement mTLS transport credentials setup
- [ ] Add fallback to `colony_secret` during migration
- [ ] Add connection establishment logging
- [ ] Add integration tests for mTLS connections

### Phase 4: CLI Commands & Monitoring

- [ ] Implement `coral agent bootstrap` command
- [ ] Implement `coral agent cert status` command
- [ ] Implement `coral agent cert renew` command
- [ ] Add certificate expiry warnings to `coral agent status`
- [ ] Add telemetry for bootstrap success/failure rates

### Phase 5: Testing & Documentation

- [ ] Unit tests for all new components
- [ ] Integration tests for full bootstrap flow
- [ ] E2E tests with Discovery + Colony
- [ ] Test certificate renewal flow
- [ ] Update agent deployment documentation
- [ ] Add troubleshooting guide for bootstrap failures

## API Changes

No new protobuf messages are required - this RFD uses the APIs defined in RFD
022:

- `DiscoveryService.CreateBootstrapToken`
- `ColonyService.RequestCertificate`

### CLI Commands

```bash
# Manually trigger bootstrap (for testing or re-bootstrapping)
coral agent bootstrap --colony colony-prod --agent web-1

# Output:
Requesting bootstrap token from Discovery...
✓ Token received (expires in 5m)
Generating keypair...
✓ Ed25519 keypair generated
Creating certificate signing request...
✓ CSR created (CN=agent.web-1.colony-prod)
Requesting certificate from Colony...
✓ Certificate received (valid until 2025-02-18)
✓ Certificate saved to ~/.coral/certs/web-1.crt
✓ Private key saved to ~/.coral/certs/web-1.key

# Check certificate status
coral agent cert status --agent web-1

# Output:
Certificate Status
==================
Agent ID:          web-1
Colony ID:         colony-prod
Certificate Path:  ~/.coral/certs/web-1.crt
Key Path:          ~/.coral/certs/web-1.key

Certificate Details:
  Issuer:          Coral Intermediate CA - colony-prod
  Subject:         agent.web-1.colony-prod
  Serial Number:   3a4f5e2d1c0b9a8f7e6d5c4b3a2f1e0d
  Not Before:      2024-11-20 10:30:00 UTC
  Not After:       2025-02-18 10:30:00 UTC
  Days Until Expiry: 89

Status:            ✓ Valid

# Manually renew certificate (before expiry)
coral agent cert renew --agent web-1

# Output:
Certificate expires in 25 days, renewing...
✓ Certificate renewed successfully
✓ New certificate valid until 2025-05-19
```

### Configuration Changes

New configuration fields in `~/.coral/agents/<agent-id>.yaml`:

```yaml
security:
    # Certificate file paths (auto-detected if not specified)
    cert_path: ~/.coral/certs/<agent-id>.crt
    key_path: ~/.coral/certs/<agent-id>.key

    # CA bundle for validating Colony certificates
    ca_bundle_path: ~/.coral/ca-bundle.pem  # Embedded in binary if not specified

    bootstrap:
        enabled: true  # Enable automatic bootstrap on first connect
        discovery_url: https://discovery.coral.io:8080  # From global config if not set
        fallback_to_secret: true  # Fall back to colony_secret if bootstrap fails (migration only)
        retry_attempts: 3
        retry_delay: 5s
```

## Testing Strategy

### Unit Tests

- **Certificate Manager**:
    - Load valid certificate from disk
    - Reject expired certificates
    - Reject certificates with wrong CN
    - Check file permissions on load
    - Calculate days until expiry correctly

- **Bootstrap Client**:
    - Generate valid Ed25519 keypairs
    - Create CSR with correct CN format
    - Validate token response format
    - Handle network errors gracefully
    - Retry with exponential backoff

### Integration Tests

- **Full Bootstrap Flow**:
    - Start with no certificate
    - Request token from Discovery
    - Submit CSR to Colony
    - Receive and validate certificate
    - Store certificate securely
    - Verify permissions (0600 for key, 0644 for cert)

- **Certificate Renewal**:
    - Use existing certificate for renewal
    - Request new certificate without bootstrap token
    - Replace old certificate atomically
    - Continue using old cert if renewal fails

- **Fallback Behavior**:
    - Attempt bootstrap first
    - Fall back to `colony_secret` on failure
    - Log warning about using shared secret
    - Retry bootstrap on next restart

### E2E Tests

- **Agent Deployment**:
    - Deploy agent with no pre-configured certificate
    - Agent automatically bootstraps on first start
    - Agent connects to Colony via mTLS
    - Agent successfully sends heartbeats

- **Certificate Revocation**:
    - Colony revokes agent certificate
    - Agent's next RPC fails with authentication error
    - Agent attempts to re-bootstrap
    - Colony issues new certificate with different serial

- **Colony Rotation**:
    - Agent has valid certificate
    - Colony rotates intermediate CA
    - Agent continues using old certificate
    - Agent renews and receives cert from new intermediate

## Security Considerations

- **Private Key Protection**:
    - Private keys stored with 0600 permissions
    - Keys never transmitted over network
    - Keys owned by agent process user only

- **Bootstrap Token Security**:
    - Tokens have 5-minute TTL (from RFD 022)
    - Tokens are single-use (validated by Colony)
    - Token hash stored in database for replay prevention

- **Certificate Validation**:
    - Agents verify Colony certificate against embedded CA bundle
    - Reject connection if Colony cert is invalid
    - Prevents MITM attacks during bootstrap

- **Audit Logging**:
    - Log bootstrap token requests (agent_id, colony_id, timestamp)
    - Log certificate issuance (agent_id, serial_number, expiry)
    - Log certificate renewal attempts
    - Log fallback to `colony_secret` during migration

- **Graceful Degradation**:
    - If bootstrap fails, agent logs error and falls back to shared secret
    - Operator receives alert about failed bootstrap
    - Agent retries bootstrap on next restart
    - No service interruption during rollout

## Migration Strategy

**Rollout Phases**:

1. **Deploy RFD 022 infrastructure** (Already implemented):
    - Colony CA manager running
    - Discovery token issuance available
    - Colony certificate request endpoint active

2. **Deploy agent with bootstrap capability** (This RFD):
    - New agents automatically bootstrap on first connect
    - Existing agents continue using `colony_secret`
    - Feature flag: `security.bootstrap.enabled=true` (default)

3. **Gradual migration**:
    - Monitor bootstrap success rate via telemetry
    - Identify and fix failed bootstrap attempts
    - Existing agents re-bootstrap on restart

4. **Enforcement** (Future):
    - After all agents bootstrapped, enforce mTLS
    - Disable `colony_secret` authentication
    - Reject non-certificate connections

**Backward Compatibility**:

- Agents with `security.bootstrap.enabled=false` continue using `colony_secret`
- Agents with `security.bootstrap.fallback_to_secret=true` fall back on
  bootstrap failure
- No breaking changes to existing agent deployments
- Colony accepts both certificate and `colony_secret` authentication (until
  enforcement)

**Rollback Plan**:

- Set `security.bootstrap.enabled=false` in agent config
- Agents revert to `colony_secret` authentication
- Certificates remain valid for future use
- No data loss or service interruption

## Deferred Features

**Certificate Renewal** (RFD xxx - Certificate Lifecycle Management):

- Automatic renewal at 30 days before expiry
- Renewal failure handling and alerting
- Certificate expiry monitoring and dashboards

**mTLS Enforcement** (RFD xxx - mTLS Enforcement & Migration):

- Disable `colony_secret` authentication entirely
- Reject non-certificate agent connections
- Certificate-based authorization policies

**Advanced Bootstrap** (Future):

- Multi-colony bootstrap (agent connects to multiple colonies)
- Certificate-based agent migration between colonies
- Automated certificate rotation on key compromise

---

## Implementation Status

**Core Capability:** ⏳ Not Started

This RFD depends on RFD 022 (Embedded step-ca) which provides the server-side
infrastructure. Implementation will begin after RFD 022 approval.

**What This Enables:**

- Zero-touch agent deployment with automatic certificate provisioning
- Per-agent cryptographic identity without manual certificate management
- Foundation for mTLS enforcement and `colony_secret` deprecation
- Agent certificate lifecycle automation (renewal, revocation, rotation)
