---
rfd: "088"
title: "Bootstrap Pre-Shared Key"
state: "in-progress"
breaking_changes: true
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "047", "048", "049" ]
related_rfds: [ "085", "086", "087" ]
areas: [ "security", "colony", "agent", "discovery" ]
---

# RFD 088 - Bootstrap Pre-Shared Key

**Status:** 🔄 In Progress

## Summary

Introduce a Bootstrap Pre-Shared Key (PSK) as a required authorization secret
for agent certificate issuance. The PSK is a random secret generated during
colony initialization, distributed out-of-band to agents, and validated by the
Colony before issuing certificates. This closes a gap where knowledge of the
colony ID alone is sufficient to join a colony.

## Problem

The current bootstrap security model relies on the CA fingerprint as the primary
trust anchor. However, the CA fingerprint is **not a secret**:

- **RFD 085** stores the colony's CA certificate in the Discovery service via
  `RegisterColony`'s `public_endpoint.ca_cert` field.
- **`LookupColony` is unauthenticated** — anyone can query it and retrieve the
  CA certificate.
- The CA fingerprint can be trivially computed from the public CA certificate.

This means an attacker who knows a colony's mesh ID (a short, human-readable
string like `my-app-prod-a3f2e1`) can:

1. Call `LookupColony(mesh_id)` to get the CA certificate.
2. Compute the CA fingerprint from the certificate.
3. Call `CreateBootstrapToken(colony_id, agent_id)` to get a valid referral
   ticket (unauthenticated endpoint).
4. Connect to the colony, pass fingerprint validation, present the referral
   ticket, and receive a valid agent certificate.

The referral ticket mechanism (RFD 049) gates certificate issuance, but
`CreateBootstrapToken` itself requires no authentication. RFD 086's proposed
policy enforcement (CIDR allowlists, agent ID patterns) provides weak protection
since CIDRs are unpredictable and agent ID patterns are guessable.

**The fundamental gap**: there is no secret that an attacker cannot obtain from
publicly available information.

## Solution

Add a **Bootstrap PSK** — a high-entropy random secret generated during colony
initialization, completely independent of the PKI hierarchy. The Colony validates
the PSK during certificate issuance. Without it, no certificate is issued.

**Key Design Decisions:**

- **Independent of PKI**: The PSK is not derived from any CA key or certificate.
  It cannot be reverse-engineered from public information.
- **Colony-side validation**: The PSK is validated by the Colony during
  `RequestCertificate`, not by Discovery. Discovery never sees the PSK.
- **Out-of-band distribution**: Distributed the same way as the colony ID —
  via environment variables, Kubernetes Secrets, configuration management, etc.
- **Rotatable**: The PSK can be rotated without touching the CA hierarchy or
  invalidating existing agent certificates.

**Benefits:**

- Closes the "public CA cert in Discovery" authorization gap.
- Simple to implement — single field addition to bootstrap flow.
- No Discovery service changes required.
- Existing agents with valid certificates are unaffected (renewals use mTLS,
  not PSK).

**Architecture Overview:**

```
Colony Initialization:
  $ coral colony init my-app-prod
  ...
  Bootstrap PSK: coral-psk:a3f2e1d4c5b6a7f8e9d0c1b2a3f4e5d6 (distribute to agents)

Agent Bootstrap:
  Agent → Discovery: CreateBootstrapToken(colony_id, agent_id)
  Agent → Discovery: LookupColony(colony_id) → endpoints
  Agent → Colony:    TLS handshake + fingerprint validation
  Agent → Colony:    RequestCertificate(CSR, referral_ticket, bootstrap_psk)  ← NEW
                     Colony validates: ticket ✓, PSK ✓ → issues certificate
```

The trust model becomes:

- **Colony ID** — public, used for discovery lookup.
- **CA fingerprint** — public, used for MITM protection (validates you're
  talking to the right colony).
- **Bootstrap PSK** — **secret**, proves authorization to join the colony.

### PSK Generation

The PSK is generated during `coral colony init` using 32 bytes of
cryptographically secure random data, encoded as hex with a `coral-psk:` prefix
for easy identification:

```
coral-psk:a3f2e1d4c5b6a7f8e9d0c1b2a3f4e5d6c7b8a9f0e1d2c3b4a5f6e7d8c9b0a1f2
```

The PSK is stored in the Colony's configuration alongside the CA hierarchy. It is
never sent to Discovery or embedded in certificates.

### PSK Rotation

Operators can rotate the PSK without disrupting existing agents:

```bash
$ coral colony psk rotate

New Bootstrap PSK: coral-psk:f1e2d3c4b5a6f7e8d9c0b1a2f3e4d5c6...
Previous PSK remains valid for: 24h (grace period)

Update agents with the new PSK before the grace period expires.
```

During the grace period, the Colony accepts both the old and new PSK. After the
grace period, only the new PSK is accepted. Existing agents with valid
certificates are unaffected since renewals use mTLS authentication (no PSK
required).

### Component Changes

1. **Colony Initialization** (`internal/colony/ca/init.go`):
    - Generate PSK during `coral colony init`.
    - Encrypt PSK with key derived from Root CA private key (HKDF-SHA256 +
      AES-256-GCM) and store in `bootstrap_psks` table.
    - Display PSK in initialization output for operator distribution.

5. **Colony Database** (`internal/colony/database/schema.go`):
    - Add `bootstrap_psks` table to schema DDL.
    - Add accessor methods for PSK CRUD and validation.

2. **Colony Certificate Issuance** (`internal/colony/ca/issuer.go`):
    - Validate PSK on bootstrap `RequestCertificate` requests.
    - Reject requests with missing or invalid PSK.
    - Renewal requests (mTLS-authenticated) do not require PSK.

3. **Agent Bootstrap Client** (`internal/agent/bootstrap/client.go`):
    - Include PSK in `RequestCertificate` call.
    - Read PSK from configuration or environment variable.

4. **CLI** (`internal/cli/`):
    - `coral colony init` — display PSK in output.
    - `coral colony psk rotate` — rotate PSK with grace period.
    - `coral colony psk show` — display current PSK.

## Data Model

### DuckDB Schema

```sql
CREATE TABLE IF NOT EXISTS bootstrap_psks (
    id TEXT PRIMARY KEY,                    -- ULID
    encrypted_psk BLOB NOT NULL,            -- AES-256-GCM encrypted PSK
    encryption_nonce BLOB NOT NULL,         -- GCM nonce (12 bytes)
    status TEXT NOT NULL DEFAULT 'active',   -- 'active', 'grace', 'revoked'
    created_at TIMESTAMP NOT NULL,
    grace_expires_at TIMESTAMP,             -- set during rotation, NULL otherwise
    revoked_at TIMESTAMP
);
```

**Encryption**: The PSK plaintext is encrypted with AES-256-GCM. The encryption
key is derived from the colony's Root CA private key using HKDF-SHA256 with info
string `coral-psk-encryption`. This ensures the PSK is recoverable via
`coral colony psk show` as long as the Root CA key is available, without
introducing a new trust boundary.

**Invariants**:

- At most one row has `status = 'active'` at any time.
- At most one row has `status = 'grace'` at any time (during rotation).
- On rotation: the current active PSK moves to `grace` with a
  `grace_expires_at`, and a new active PSK is inserted.
- A background check (or lazy check on validation) revokes grace PSKs past
  their `grace_expires_at`.

**Validation logic**: Query rows where `status IN ('active', 'grace')` and
`grace_expires_at IS NULL OR grace_expires_at > now()`. Decrypt each and compare
against the provided PSK using constant-time comparison.

## API Changes

### Colony Service (gRPC)

```protobuf
// Updated: bootstrap_psk required for first-time bootstrap.
message RequestCertificateRequest {
    bytes csr = 1;
    string referral_ticket = 2;
    string bootstrap_psk = 3;  // NEW: required for bootstrap, ignored for renewal
}
```

No changes to Discovery Service APIs.

### Configuration Changes

```yaml
# Agent configuration (~/.coral/agents/<agent-id>.yaml)
security:
    bootstrap:
        psk: "coral-psk:a3f2e1d4c5b6..."  # Or via CORAL_BOOTSTRAP_PSK env var
```

### CLI Commands

```bash
# Colony initialization (updated output)
$ coral colony init my-app-prod

Generated Certificate Authority (Hierarchical):
  Root CA (10-year validity)
    ├─ Bootstrap Intermediate CA (1-year)
    ├─ Server Intermediate CA (1-year)
    ├─ Agent Intermediate CA (1-year)
    └─ Policy Signing Certificate (10-year)

Root CA Fingerprint:
  sha256:a3f2e1d4c5b6a7f8e9d0c1b2a3f4e5d6c7b8a9f0e1d2c3b4a5f6e7d8c9b0a1f2

Bootstrap PSK:
  coral-psk:f1e2d3c4b5a697f8e9d0c1b2a3f4e5d6c7b8a9f0e1d2c3b4a5f6e7d8c9b0a1f2

Deploy agents with:
  export CORAL_COLONY_ID=my-app-prod-a3f2e1
  export CORAL_CA_FINGERPRINT=sha256:a3f2e1d4c5b6...
  export CORAL_BOOTSTRAP_PSK=coral-psk:f1e2d3c4b5a6...


# Rotate PSK
$ coral colony psk rotate
  Previous PSK valid until: 2026-01-30T12:00:00Z (24h grace period)
  New PSK: coral-psk:aabbccdd...

# Show current PSK (decrypted from DuckDB using Root CA key)
$ coral colony psk show
  PSK: coral-psk:f1e2d3c4b5a6...
  Created: 2026-01-15T10:00:00Z
  Grace PSK: (none)
```

### Environment Variables

```bash
# New (required for bootstrap)
CORAL_BOOTSTRAP_PSK=coral-psk:f1e2d3c4b5a697f8e9d0c1b2a3f4e5d6...
```

### Kubernetes Deployment

```yaml
apiVersion: v1
kind: Secret
metadata:
    name: coral-colony-bootstrap
data:
    colony_id: <base64: my-app-prod-a3f2e1>
    ca_fingerprint: <base64: sha256:a3f2e1d4c5b6...>
    bootstrap_psk: <base64: coral-psk:f1e2d3c4b5a6...>
---
apiVersion: apps/v1
kind: Deployment
spec:
    template:
        spec:
            containers:
                -   name: coral-agent
                    env:
                        -   name: CORAL_COLONY_ID
                            valueFrom:
                                secretKeyRef:
                                    name: coral-colony-bootstrap
                                    key: colony_id
                        -   name: CORAL_CA_FINGERPRINT
                            valueFrom:
                                secretKeyRef:
                                    name: coral-colony-bootstrap
                                    key: ca_fingerprint
                        -   name: CORAL_BOOTSTRAP_PSK
                            valueFrom:
                                secretKeyRef:
                                    name: coral-colony-bootstrap
                                    key: bootstrap_psk
```

## Security Considerations

### Updated Attack Scenarios

| Attack                                 | Protection                               |
|----------------------------------------|------------------------------------------|
| **Attacker knows colony ID**           | Cannot compute PSK from public info ✅    |
| **Attacker has CA cert + fingerprint** | Still needs PSK to get certificate ✅     |
| **Attacker has referral ticket**       | Colony rejects without valid PSK ✅       |
| **PSK leaked**                         | Rotate PSK, existing agents unaffected ✅ |
| **PSK brute force**                    | 256-bit entropy + rate limiting ✅        |
| **Discovery compromise**               | PSK never sent to Discovery ✅            |

### PSK Storage

- The PSK is stored in DuckDB **encrypted at rest** using AES-256-GCM with a key
  derived from the colony's Root CA private key (HKDF-SHA256, info: `coral-psk-encryption`).
- Since the Root CA private key is already the highest-trust secret in the colony
  (whoever has it owns the colony), encrypting the PSK with it adds no new trust
  assumptions.
- `coral colony psk show` decrypts and displays the plaintext PSK.
- The PSK is never sent to the Discovery service.
- The PSK is never embedded in certificates or referral tickets.
- The PSK is transmitted only over the TLS connection to the Colony (already
  validated via CA fingerprint).

### Relationship to RFD 086

RFD 086 (Discovery-Based Policy Enforcement) provides defense-in-depth at the
Discovery layer — rate limits, quotas, CIDR allowlists. The Bootstrap PSK
provides authorization at the Colony layer. They are complementary:

- **PSK** answers: "Is this agent allowed to join this colony?"
- **RFD 086** answers: "Should Discovery issue a referral ticket for this
  request?"

Both can be implemented independently. The PSK alone is sufficient to prevent
unauthorized agent enrollment.

## Migration Strategy

1. **Colony upgrade**: Colony generates a PSK during the first startup after
   upgrade. PSK is stored in colony configuration. A flag
   `bootstrap.psk_required` defaults to `false` during migration.

2. **Operator enables PSK**: Operator sets `bootstrap.psk_required=true` and
   distributes the PSK to agent deployments.

3. **Agent upgrade**: Agents include `CORAL_BOOTSTRAP_PSK` in configuration.
   Agents without the PSK fail bootstrap when enforcement is enabled.

4. **Enforcement**: After all agents are configured with the PSK, enforcement
   is mandatory.

**Rollback**: Set `bootstrap.psk_required=false` to allow PSK-less bootstrap
during migration.

## Implementation Status

**Core Capability:** 🔄 In Progress

PSK generation, encrypted storage, colony-side validation, agent-side integration,
CLI commands, and unit/integration tests are implemented. E2E testing pending.

**Operational Components:**
- ✅ PSK generation (32-byte entropy, `coral-psk:` prefix)
- ✅ PSK encrypted storage (AES-256-GCM, HKDF-SHA256 from Root CA key)
- ✅ Colony-side validation in `RequestCertificate`
- ✅ Agent-side PSK inclusion in bootstrap flow
- ✅ CLI commands (`coral colony psk show`, `coral colony psk rotate`)
- ✅ Grace period support for PSK rotation
- ✅ E2E test: docker-compose agents receive PSK, CLI test helpers pass PSK

## Implementation Plan

### Phase 1: PSK Generation and Storage

- [x] Add PSK generation to `coral colony init`
- [x] Store PSK encrypted in DuckDB (AES-256-GCM, key from Root CA via HKDF)
- [x] Add `coral colony psk show` and `coral colony psk rotate` commands
- [ ] Add `bootstrap.psk_required` configuration flag (enforcement toggle)

### Phase 2: Colony-Side Validation

- [x] Add `bootstrap_psk` field to `RequestCertificateRequest` protobuf
- [x] Validate PSK in certificate issuance flow (bootstrap requests only)
- [x] Support grace period for PSK rotation (accept old + new)
- [x] Add audit logging for PSK validation failures

### Phase 3: Agent-Side Integration

- [x] Add `CORAL_BOOTSTRAP_PSK` environment variable support
- [x] Add `bootstrap_psk` configuration field in BootstrapConfig
- [x] Include PSK in `RequestCertificate` call during bootstrap
- [x] Update `coral agent bootstrap` command with `--psk` flag

### Phase 4: Testing

- [x] Unit tests for PSK generation, encryption, and format validation
- [x] Unit tests for PSK rotation with grace period
- [x] Integration test: PSK store, validate, and rotate via Manager
- [x] Integration test: bootstrap PSK import from filesystem
- [x] Integration test: PSK rotation grace period expiry
- [x] E2E test: docker-compose PSK sharing, CLI helpers pass `--psk`
