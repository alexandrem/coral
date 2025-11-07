---
rfd: "020"
title: "mTLS Authentication for Agent Registration"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "019" ]
related_rfds: [ "019" ]
database_migrations: [ "002-agent-certificates" ]
areas: [ "security", "colony", "agent" ]
---

# RFD 020 - mTLS Authentication for Agent Registration

**Status:** 🚧 Draft

## Summary

Fix the current vulnerability where agent registration happens over plain HTTP,
exposing `colony_secret` credentials in cleartext, and replace the shared secret
model with per-agent certificate-based authentication using mutual TLS (mTLS).

Colony acts as a Certificate Authority (CA), issuing unique client certificates
to each agent during initial bootstrap. After bootstrap, all agent-colony
communication uses mTLS, providing strong authentication and encryption without
requiring shared secrets. Each agent has a unique cryptographic identity that
can be individually revoked.

This eliminates credential exposure, enables per-agent identity, and provides a
foundation for fine-grained access control.

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

2. **Shared Secret Model**: `colony_secret` is shared by all agents in a
   colony. Compromising one agent exposes the credential for the entire mesh.
   Cannot revoke access for a single agent without rotating the secret for all
   agents.

3. **No Per-Agent Identity**: Colony cannot distinguish between agents based on
   authentication alone. Agent identity relies on self-asserted `agent_id`,
   which can be spoofed.

4. **Large Compromise Blast Radius**: Single stolen `colony_secret` allows
   unlimited rogue agent registrations. No way to revoke individual agents
   without affecting all agents.

5. **WireGuard Only Protects Post-Registration**: WireGuard encryption only
   protects traffic AFTER successful registration, leaving the bootstrap phase
   vulnerable.

### Impact

- **Security Breach Risk**: `colony_secret` exposed to network eavesdropping
  during agent registration. Compromised credential allows attackers to join
  mesh and access all agent communications.
- **No Individual Revocation**: Cannot revoke compromised agents without
  rotating secret for entire colony and updating all agents.
- **Weak Identity Model**: Cannot cryptographically verify agent identity.
  Agent can impersonate others by claiming their `agent_id`.
- **Compliance Issues**: Plaintext credential transmission and shared secrets
  violate security best practices and compliance requirements.
- **No Audit Trail**: Cannot reliably track which agent performed which action
  (identity is self-asserted).

## Solution

### Key Design Decisions

1. **Colony as Certificate Authority (CA)**: Colony generates and manages its
   own root CA certificate for issuing agent client certificates. No external
   PKI infrastructure required.

2. **One-Time Bootstrap with Shared Secret**: Agent uses `colony_secret` ONCE
   during initial bootstrap over HTTPS to obtain a unique client certificate.
   Colony's TLS certificate can be self-signed with CA pinning.

3. **Per-Agent Client Certificates**: Each agent receives a unique X.509 client
   certificate signed by the colony's CA. Certificate includes `agent_id` in
   the Common Name (CN) field, providing cryptographic proof of identity.

4. **mTLS for All Subsequent Communication**: After bootstrap, all agent-colony
   communication uses mutual TLS. Colony validates client certificates, agents
   validate colony's TLS certificate. No shared secrets transmitted after
   bootstrap.

5. **Certificate Lifecycle Management**: Certificates have configurable TTL (
   default: 90 days). Support for certificate renewal, revocation, and rotation.

6. **Backward Compatibility**: Support legacy `colony_secret` authentication
   during transition period. Agents without certificates fall back to legacy
   auth.

### Benefits

- **Strong Authentication**: mTLS provides cryptographic proof of identity for
  both colony and agents.
- **Encryption by Default**: All agent-colony communication encrypted via TLS,
  protecting bootstrap phase.
- **Per-Agent Identity**: Each agent has unique certificate with `agent_id` in
  CN. Cannot be spoofed.
- **Individual Revocation**: Colony can revoke individual agent certificates
  without affecting others.
- **Limited Shared Secret Exposure**: `colony_secret` used only once during
  bootstrap over HTTPS.
- **Industry Standard**: mTLS is the standard approach for service mesh
  authentication (Kubernetes, Istio, Linkerd).
- **Audit Trail**: All requests authenticated with certificate identity,
  enabling reliable audit logs.
- **No External Dependencies**: Colony manages its own CA, no external PKI
  required.

### Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                     Colony (Public IP)                          │
│                                                                 │
│  ┌────────────────┐         ┌───────────────────────────────┐  │
│  │ Certificate    │         │  Colony CA (Certificate Auth) │  │
│  │ Authority      │────────▶│                               │  │
│  │ Service        │         │  - Root CA cert + private key │  │
│  │                │         │  - Issues client certificates │  │
│  │ - Issue cert   │         │  - Signs with CA private key  │  │
│  │ - Validate CSR │         │  - Configurable TTL (90d)     │  │
│  │ - Revoke cert  │         └───────────────────────────────┘  │
│  └────────────────┘                       │                    │
│         ▲                                 ▼                    │
│         │                   ┌────────────────────────────┐    │
│         │                   │  DuckDB                    │    │
│         │                   │                            │    │
│         │                   │  agent_certificates        │    │
│         │                   │  ├─ agent_id (PK)          │    │
│         │                   │  ├─ serial_number (UNIQUE) │    │
│         │                   │  ├─ issued_at              │    │
│         │                   │  ├─ expires_at             │    │
│         │                   │  └─ revoked (bool)         │    │
│         │                   └────────────────────────────┘    │
│         │                                                      │
│         │ (1) Bootstrap: RequestCertificate(colony_secret)    │
│         │     HTTPS (validates colony's TLS cert)             │
│         │                                                      │
│  ┌────────────────┐                                            │
│  │ Registration   │                                            │
│  │ Handler        │         (2) Subsequent: RegisterAgent()   │
│  │ (mTLS enabled) │◀────────   mTLS (validates client cert)   │
│  │                │                                            │
│  │ - Verify cert  │                                            │
│  │ - Check CRL    │                                            │
│  │ - Extract ID   │                                            │
│  └────────────────┘                                            │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                         Agent                                   │
│                                                                 │
│  BOOTSTRAP Flow (ONE-TIME):                                     │
│  1. Generate RSA key pair (2048-bit)                            │
│  2. Create Certificate Signing Request (CSR) with agent_id      │
│  3. RequestCertificate(colony_secret, CSR) over HTTPS           │
│  4. Receive signed X.509 client certificate                     │
│  5. Store certificate + private key locally (~/.coral/cert.pem) │
│                                                                 │
│  REGISTRATION Flow (WITH CERTIFICATE):                          │
│  1. Load client certificate + private key from disk             │
│  2. Create WireGuard interface (no IP, no peers yet)            │
│  3. RegisterAgent() over mTLS (presents client cert)            │
│  4. Colony validates cert, extracts agent_id from CN            │
│  5. Receive permanent mesh IP                                   │
│  6. Assign IP to WireGuard interface                            │
│  7. Add colony as WireGuard peer                                │
│  8. Test mesh connectivity                                      │
│                                                                 │
│  ✅ colony_secret only used once during bootstrap (HTTPS)       │
│  ✅ All subsequent communication uses mTLS (no shared secrets)  │
│  ✅ Per-agent unique identity (certificate CN = agent_id)       │
└─────────────────────────────────────────────────────────────────┘
```

### Component Changes

#### Colony: Certificate Authority (CA)

**Colony acts as its own CA for issuing agent client certificates**:

- **CA Initialization**:
    - Generate root CA certificate + private key on first startup (RSA 4096-bit)
    - Store CA cert and key securely in colony data directory
    - CA cert is self-signed with long TTL (10 years)
    - Load existing CA on subsequent startups

- **Certificate Issuance Service**:
    - New endpoint: `RequestCertificate(colony_secret, CSR)` over HTTPS
    - Validates `colony_secret` (only used during bootstrap)
    - Validates Certificate Signing Request (CSR)
    - Extracts `agent_id` from CSR Common Name
    - Issues X.509 client certificate signed by colony CA
    - Certificate fields:
        - Subject CN: `agent_id`
        - Organization: `colony_id`
        - Valid for: 90 days (configurable)
        - Key Usage: Digital Signature, Key Encipherment
        - Extended Key Usage: Client Authentication

- **Certificate Storage**:
    - Store issued certificates in DuckDB (`agent_certificates` table)
    - Track serial number, issuance date, expiration, revocation status
    - Enable certificate lifecycle management and audit trail

- **Certificate Revocation**:
    - API endpoint: `RevokeCertificate(agent_id)` (admin only)
    - Mark certificate as revoked in database
    - Maintain Certificate Revocation List (CRL)
    - Colony checks CRL during mTLS handshake

#### Colony: mTLS-Enabled Registration Handler

**Registration handler validates client certificates via mTLS**:

- **TLS Configuration**:
    - Enable TLS on registration endpoint (port 9000)
    - Configure mTLS with `tls.RequireAndVerifyClientCert`
    - Trust pool: Colony's own CA certificate
    - Server certificate: Colony's TLS cert (can be self-signed)

- **Certificate Validation**:
    - TLS library validates certificate signature, chain, expiration
    - Extract `agent_id` from certificate CN field
    - Check certificate not revoked (query database)
    - Use CN as authenticated identity (cannot be spoofed)

- **Backward Compatibility**:
    - Support legacy `colony_secret` authentication (HTTP, no TLS)
    - Gradually migrate agents to certificate-based auth
    - Eventually deprecate legacy auth

#### Agent: Certificate Bootstrap and Storage

**Agents obtain and store client certificates during initial setup**:

- **Bootstrap Flow (One-Time)**:
    - Generate RSA key pair (2048-bit) using `crypto/rsa`
    - Create CSR with `agent_id` in Common Name field
    - Call `RequestCertificate(colony_secret, CSR)` over HTTPS
    - Verify colony's TLS certificate (CA pinning recommended)
    - Receive signed client certificate
    - Store certificate + private key in `~/.coral/cert.pem` and
      `~/.coral/key.pem`
    - Secure file permissions (0600)

- **Certificate Loading**:
    - On startup, load certificate and private key from disk
    - If certificate not found, trigger bootstrap flow
    - If certificate expired, trigger renewal flow

- **mTLS Registration**:
    - Configure TLS client with agent's certificate and private key
    - Call `RegisterAgent()` over mTLS (no `colony_secret` sent)
    - Colony extracts `agent_id` from certificate CN
    - Agent authenticated via certificate, not shared secret

- **Certificate Renewal**:
    - Check certificate expiration on startup
    - If expires within 30 days, request renewal
    - Renewal uses existing certificate for mTLS auth (no `colony_secret`)

## Implementation Plan

### Phase 1: Colony CA Infrastructure

- [ ] Create database migration `002-agent-certificates.sql`.
- [ ] Implement CA initialization (generate root CA cert + key).
- [ ] Implement certificate issuance service (CSR validation, signing).
- [ ] Store issued certificates in DuckDB.
- [ ] Add certificate revocation support.
- [ ] Unit tests for CA operations (cert generation, signing, revocation).

### Phase 2: Colony mTLS Support

- [ ] Add TLS configuration to colony registration endpoint.
- [ ] Implement `RequestCertificate` endpoint (HTTPS with `colony_secret`).
- [ ] Configure mTLS with `RequireAndVerifyClientCert`.
- [ ] Extract `agent_id` from client certificate CN.
- [ ] Check certificate revocation status during validation.
- [ ] Support both mTLS and legacy `colony_secret` auth (backward compat).
- [ ] Unit tests for mTLS validation logic.

### Phase 3: Agent Certificate Bootstrap

- [ ] Implement agent bootstrap flow (generate keypair, create CSR).
- [ ] Call `RequestCertificate` to obtain client certificate.
- [ ] Store certificate and private key securely on disk.
- [ ] Implement certificate loading on startup.
- [ ] Handle certificate expiration and renewal.
- [ ] Unit tests for certificate bootstrap and storage.

### Phase 4: Agent mTLS Registration

- [ ] Configure TLS client with agent certificate and key.
- [ ] Update `RegisterAgent` to use mTLS instead of `colony_secret`.
- [ ] Gracefully fall back to legacy auth if certificate unavailable.
- [ ] E2E tests for mTLS registration flow.

### Phase 5: Testing and Validation

- [ ] E2E test: agent bootstrap obtains certificate.
- [ ] E2E test: agent registers via mTLS (no `colony_secret` sent).
- [ ] E2E test: colony rejects revoked certificate.
- [ ] E2E test: certificate renewal before expiration.
- [ ] E2E test: colony extracts correct `agent_id` from certificate CN.
- [ ] Security audit: verify `colony_secret` only sent during bootstrap over
  HTTPS.
- [ ] Test graceful fallback to legacy auth during transition.

### Phase 6: Certificate Lifecycle Management

- [ ] Implement automatic certificate renewal (30 days before expiration).
- [ ] Add CLI command for manual certificate revocation (`coral revoke-agent`).
- [ ] Add certificate rotation support (new CA cert).
- [ ] Implement certificate monitoring and alerts.
- [ ] Update documentation for certificate management.

### Phase 7: Deprecation and Cleanup

- [ ] Document migration path for existing deployments.
- [ ] Deprecate legacy `colony_secret` direct authentication.
- [ ] (After full migration) Remove legacy auth support.
- [ ] Update architecture documentation.

## API Changes

### New Protobuf Messages

```protobuf
// Request client certificate from colony (bootstrap only)
message RequestCertificateRequest {
    string colony_secret = 1;        // Shared secret (one-time use)
    string agent_id = 2;             // Agent identifier
    bytes csr = 3;                   // Certificate Signing Request (PEM format)
}

message RequestCertificateResponse {
    bytes certificate = 1;           // Signed X.509 client certificate (PEM format)
    bytes ca_certificate = 2;        // Colony CA certificate for validation
    int64 expires_at = 3;           // Unix timestamp when certificate expires
}

// Revoke agent certificate (admin only)
message RevokeCertificateRequest {
    string agent_id = 1;             // Agent whose certificate to revoke
    string reason = 2;               // Revocation reason (optional)
}

message RevokeCertificateResponse {
    bool success = 1;
    string message = 2;
}

// Get colony CA certificate (for agent bootstrap)
message GetCACertificateRequest {}

message GetCACertificateResponse {
    bytes ca_certificate = 1;        // Colony CA certificate (PEM format)
}
```

### Modified Protobuf Messages

```protobuf
// Update RegisterAgent to support both mTLS and legacy auth
message RegisterAgentRequest {
    // NOTE: agent_id and colony_secret become optional
    // When using mTLS, agent_id extracted from client certificate CN
    // When using legacy auth, colony_secret required

    // Legacy field (deprecated)
    string colony_secret = 1;        // Shared secret (deprecated, legacy only)

    // Existing fields
    string agent_id = 2;             // Required for legacy auth, optional for mTLS
    string wireguard_pubkey = 3;
    // ... other fields
}
```

### New RPC Endpoints

```protobuf
service ColonyService {
    // Existing RPCs...

    // New: Request client certificate during bootstrap
    // Called over HTTPS with colony_secret
    rpc RequestCertificate(RequestCertificateRequest)
        returns (RequestCertificateResponse);

    // New: Revoke agent certificate (admin only)
    rpc RevokeCertificate(RevokeCertificateRequest)
        returns (RevokeCertificateResponse);

    // New: Get colony CA certificate (public endpoint)
    rpc GetCACertificate(GetCACertificateRequest)
        returns (GetCACertificateResponse);
}
```

### Database Schema

**Migration**: `002-agent-certificates.sql`

```sql
CREATE TABLE IF NOT EXISTS agent_certificates
(
    agent_id
    TEXT
    PRIMARY
    KEY,
    serial_number
    TEXT
    NOT
    NULL
    UNIQUE,
    certificate
    TEXT
    NOT
    NULL, -- PEM-encoded certificate
    issued_at
    TIMESTAMP
    NOT
    NULL
    DEFAULT
    CURRENT_TIMESTAMP,
    expires_at
    TIMESTAMP
    NOT
    NULL,
    revoked
    BOOLEAN
    NOT
    NULL
    DEFAULT
    FALSE,
    revoked_at
    TIMESTAMP,
    revocation_reason
    TEXT
);

CREATE INDEX idx_serial_number ON agent_certificates (serial_number);
CREATE INDEX idx_expires_at ON agent_certificates (expires_at);
CREATE INDEX idx_revoked ON agent_certificates (revoked);
```

## Testing Strategy

### Unit Tests

Test certificate authority operations:

- CA initialization (generate root CA cert + key).
- Certificate issuance (CSR validation, signing).
- Certificate revocation (mark as revoked in database).
- Certificate validation (signature, expiration, revocation check).
- Agent identity extraction from certificate CN.

Test agent certificate management:

- RSA keypair generation (2048-bit).
- CSR creation with agent_id in CN.
- Certificate and key storage (secure file permissions).
- Certificate loading from disk.
- Certificate expiration detection and renewal.

### Integration Tests

Test colony mTLS configuration:

- Colony serves TLS with self-signed certificate.
- Colony requires and validates client certificates.
- Colony rejects connections without client certificate.
- Colony rejects connections with invalid certificate.
- Colony rejects connections with revoked certificate.
- Colony rejects connections with expired certificate.
- Colony extracts correct agent_id from certificate CN.

Test agent bootstrap flow:

- Agent generates keypair and CSR.
- Agent requests certificate with valid `colony_secret`.
- Agent requests certificate with invalid `colony_secret` (rejected).
- Agent receives and stores certificate securely.
- Agent loads certificate on next startup.

### E2E Tests

Test complete mTLS authentication flow:

- Agent bootstrap: obtain certificate with `colony_secret`.
- Agent registration: authenticate via mTLS (no `colony_secret` sent).
- Colony extracts agent_id from certificate, assigns mesh IP.
- Agent reconnects using same certificate (no bootstrap needed).
- Certificate renewal: agent renews before expiration using mTLS.
- Certificate revocation: colony rejects revoked agent.
- Verify `colony_secret` only sent during bootstrap over HTTPS.
- Test graceful fallback to legacy auth during transition.

## Security Considerations

### Bootstrap Secret Interception (MITM)

**Threat**: Attacker intercepts `colony_secret` during initial bootstrap.

**Mitigation**:

- **HTTPS for Bootstrap**: `RequestCertificate` endpoint uses HTTPS/TLS.
- **CA Certificate Pinning**: Agents pin colony's CA certificate to prevent
  MITM.
- **One-Time Use**: `colony_secret` only sent once during bootstrap, then never
  again.
- **Certificate Replaces Secret**: After bootstrap, agent uses certificate for
  all communication.

**Risk Assessment**: Even if `colony_secret` intercepted during bootstrap,
attacker only gets single agent's identity (that specific certificate). Cannot
register additional agents or impersonate other agents. Much lower blast radius
than current shared secret model.

### Certificate Private Key Compromise

**Threat**: Attacker steals agent's private key from disk.

**Mitigation**:

- **File Permissions**: Certificate and key stored with 0600 permissions (
  owner-only read/write).
- **Individual Revocation**: Colony can revoke compromised agent's certificate
  without affecting others.
- **Limited Scope**: Stolen key only allows impersonation of single agent, not
  entire colony.
- **Certificate Expiration**: Certificates expire after 90 days, limiting
  long-term compromise.
- **Future: Hardware Security Modules (HSM)**: Store keys in TPM/secure enclave.

**Risk Assessment**: Impact limited to single agent. Revocation mechanism
provides response path.

### Colony CA Private Key Compromise

**Threat**: Attacker steals colony's CA private key.

**Mitigation**:

- **Secure Storage**: CA key stored with strict file permissions in colony data
  directory.
- **Access Control**: Only colony process has access to CA key.
- **CA Rotation**: Support for CA certificate rotation (agents update trust
  store).
- **Monitoring**: Alert on unusual certificate issuance patterns.

**Risk Assessment**: HIGH impact - attacker can issue valid certificates for any
agent. Requires colony host compromise. Defense-in-depth with monitoring and
access control.

### Certificate Revocation Delay

**Threat**: Revoked certificate continues working until colony checks revocation
status.

**Mitigation**:

- **Real-Time CRL Check**: Colony checks certificate revocation on every mTLS
  handshake.
- **Database-Backed CRL**: Revocation status stored in DuckDB, fast lookup.
- **No Caching**: Colony does not cache certificate validation results.

**Risk Assessment**: Minimal delay (milliseconds). Revocation effective
immediately on next connection attempt.

### Agent Identity Spoofing

**Threat**: Agent attempts to claim different `agent_id`.

**Mitigation**:

- **Certificate CN Binding**: Agent identity cryptographically bound to
  certificate CN.
- **Colony Extracts ID**: Colony extracts `agent_id` from certificate, ignores
  self-asserted ID.
- **Cannot Be Spoofed**: Attacker cannot forge certificate without colony's CA
  private key.

**Risk Assessment**: mTLS eliminates identity spoofing. Agent identity is
cryptographically guaranteed.

## Migration Strategy

### Deployment

**Critical**: Deploy in this specific order to maintain backward compatibility.

1. **Deploy Colony Update**:
    - Run database migration `002-agent-certificates.sql`.
    - Initialize colony CA (generate root CA cert + key on first startup).
    - Add HTTPS/TLS support to registration endpoint (port 9000).
    - Add `RequestCertificate` endpoint (HTTPS only).
    - Configure mTLS support (`RequireAndVerifyClientCert`).
    - Support BOTH mTLS and legacy `colony_secret` authentication.
    - Existing agents continue working (backward compatible).

2. **Deploy Agent Updates**:
    - New agents bootstrap: obtain certificate on first connection.
    - New agents use mTLS for all subsequent registrations.
    - Old agents without certificates use legacy `colony_secret` auth.
    - Gradual rollout possible (agents migrate as they restart).

3. **Verification**:
    - Monitor registration success rate (both mTLS and legacy).
    - Check database for issued certificates.
    - Verify certificate revocation working.
    - Test agent bootstrap flow.
    - Audit mTLS authentication logs.

4. **Migration Period**:
    - Allow 90 days for all agents to obtain certificates.
    - Monitor percentage of agents using mTLS vs legacy auth.
    - Alert operators when agents fail to bootstrap.

5. **Deprecation** (after all agents migrated):
    - Disable legacy `colony_secret` HTTP authentication.
    - Require mTLS for all agent connections.
    - Remove legacy auth code in future versions.

### Rollback Plan

1. **If mTLS Issues Detected**:
    - Agents automatically fall back to legacy `colony_secret` authentication.
    - Colony continues accepting both auth methods.
    - Investigate certificate issuance or validation bugs.
    - Agents can re-bootstrap to obtain new certificates.

2. **Full Rollback** (extreme scenario):
    - Revert colony to legacy auth only (disable mTLS).
    - Revert agents to direct `colony_secret` transmission.
    - Drop `agent_certificates` table if needed (certificates lost).
    - Delete colony CA certificate and key.

### Backward Compatibility

- **Colony**: Accepts BOTH mTLS (with client cert) and legacy HTTP auth (with
  `colony_secret`) during transition.
- **Agent**: Old agents use legacy `colony_secret` authentication. New agents
  obtain certificate and use mTLS.
- **Automatic Bootstrap**: New agents automatically detect missing certificate
  and trigger bootstrap flow.
- **Gradual Migration**: Mixed deployments with old and new agents work
  correctly.

### Agent Bootstrap Process

For users deploying new agents after colony update:

1. **Initial Setup**:
   ```bash
   # Agent detects no certificate on first run
   coral connect --colony-id abc --colony-secret <secret>
   ```

2. **Automatic Bootstrap**:
    - Agent generates keypair
    - Creates CSR with agent_id
    - Calls `RequestCertificate` over HTTPS
    - Stores certificate locally

3. **Subsequent Connections**:
   ```bash
   # No colony_secret needed - uses certificate
   coral connect --colony-id abc
   ```

## Future Enhancements

### Hardware Security Module (HSM) Support

Store agent private keys in hardware security modules:

- **TPM Integration**: Use Trusted Platform Module for key storage.
- **Secure Enclave**: Use macOS/iOS secure enclave for key operations.
- **YubiKey**: Support for hardware tokens with client certificates.
- **Benefits**: Private keys never exposed to filesystem, resistant to theft.

**Priority**: MEDIUM - Significant security improvement for high-security
environments.

### Short-Lived Certificates with Automatic Renewal

Reduce certificate TTL and automate renewal:

- **Short TTL**: Reduce from 90 days to 24 hours or 7 days.
- **Automatic Renewal**: Agent renews certificate automatically before
  expiration.
- **Graceful Rollover**: Agent obtains new cert while old cert still valid.
- **Benefits**: Limits compromise window, forces regular re-authentication.

**Priority**: LOW - Current 90-day TTL is reasonable, but shorter is more
secure.

### Certificate Transparency (CT) Log

Maintain audit log of all issued certificates:

- **Public CT Log**: All issued certificates logged for transparency.
- **Monitoring**: Detect unauthorized certificate issuance.
- **Compliance**: Meet certificate transparency requirements.

**Priority**: LOW - Useful for large deployments and compliance.

### OCSP (Online Certificate Status Protocol)

Replace database CRL checks with OCSP:

- **Real-Time Validation**: Query OCSP responder for certificate status.
- **Scalability**: Offload revocation checking from colony.
- **Standard Protocol**: Industry-standard revocation mechanism.

**Priority**: LOW - Current database-backed CRL is sufficient for most
deployments.

### Let's Encrypt Integration for Colony TLS

Automatic TLS certificate provisioning for colony public endpoints:

- **Let's Encrypt ACME**: Automatic certificate issuance and renewal.
- **Benefits**: No need for self-signed certificates, trusted by all agents.
- **Fallback**: Continue supporting self-signed certificates for air-gapped
  deployments.

**Priority**: MEDIUM - Improves user experience for public colonies.

## Appendix

### Certificate Structure

**Colony CA Certificate** (Self-Signed):

```
Subject: CN=Coral Colony CA, O=colony-abc
Issuer: CN=Coral Colony CA, O=colony-abc (self-signed)
Validity: 10 years
Key Usage: Certificate Sign, CRL Sign
```

**Agent Client Certificate** (Signed by Colony CA):

```
Subject: CN=agent-id-123, O=colony-abc
Issuer: CN=Coral Colony CA, O=colony-abc
Validity: 90 days
Key Usage: Digital Signature, Key Encipherment
Extended Key Usage: TLS Web Client Authentication
```

### mTLS Handshake Flow

```
Agent initiates connection to Colony
  ↓
TLS ClientHello (includes SNI)
  ↓
Colony sends TLS ServerHello + Server Certificate + CertificateRequest
  ↓
Agent validates colony's TLS certificate (checks signature, expiration)
  ↓
Agent sends Client Certificate + CertificateVerify
  ↓
Colony validates client certificate:
  - Signature (signed by colony CA?)
  - Expiration (not expired?)
  - Revocation (not in CRL?)
  ↓
Colony extracts agent_id from certificate CN
  ↓
TLS handshake complete - encrypted connection established
  ↓
Colony processes RegisterAgent RPC with authenticated agent_id
```

### Certificate File Locations

```
Colony:
  ~/.coral/colonies/<colony-id>/ca-cert.pem    # Colony CA certificate (public)
  ~/.coral/colonies/<colony-id>/ca-key.pem     # Colony CA private key (secret)
  ~/.coral/colonies/<colony-id>/tls-cert.pem   # Colony TLS server cert
  ~/.coral/colonies/<colony-id>/tls-key.pem    # Colony TLS server key

Agent:
  ~/.coral/agents/<agent-id>/cert.pem          # Agent client certificate
  ~/.coral/agents/<agent-id>/key.pem           # Agent private key (secret)
  ~/.coral/agents/<agent-id>/ca-cert.pem       # Colony CA cert (for validation)
```

### Comparison with Service Mesh mTLS

| Aspect              | Coral (Proposed)       | Istio / Linkerd       | Kubernetes (cert-manager) |
|---------------------|------------------------|-----------------------|---------------------------|
| **CA Model**        | Colony self-signed CA  | Citadel / Identity CA | Let's Encrypt / CA        |
| **Certificate TTL** | 90 days (configurable) | 24 hours              | 90 days                   |
| **Auto Renewal**    | Yes (before expiry)    | Yes (automatic)       | Yes (cert-manager)        |
| **Revocation**      | Database CRL           | OCSP / CRL            | CRL / OCSP                |
| **Identity Format** | CN = agent_id          | SAN = spiffe://...    | CN = service name         |
| **Bootstrap**       | colony_secret (HTTPS)  | K8s service account   | ACME challenge            |
| **Key Storage**     | Filesystem (0600)      | K8s secrets           | K8s secrets / HSM         |
