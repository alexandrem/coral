---
rfd: "085"
title: "Discovery Service Public Endpoint Registry"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "001", "031", "047", "048", "049" ]
database_migrations: [ ]
areas: [ "discovery", "cli", "security", "ux" ]
---

# RFD 085 - Discovery Service Public Endpoint Registry

**Status:** 🎉 Implemented

## Summary

Extend the Discovery Service to store and distribute colony public endpoint
information, including CA certificates. CLI users connect to remote colonies
using colony credentials (colony ID + CA fingerprint) shared out-of-band by the
colony owner. The CLI fetches the CA certificate from Discovery and verifies it
against the known fingerprint before trusting it.

This approach provides cryptographic verification of the colony's identity
(similar to SSH host key fingerprints or Kubernetes
`--discovery-token-ca-cert-hash`) while using Discovery as a convenient
distribution mechanism for CA certificates.

## Problem

### Current State

RFD 031 introduced the colony public HTTPS endpoint, enabling CLI access without
WireGuard. However, connecting to a remote colony currently requires manual
configuration:

```bash
# User must manually:
# 1. Get the endpoint URL from colony admin
# 2. Get the CA certificate file from colony admin
# 3. Create local config with both pieces

coral colony add-remote prod \
    --endpoint https://colony.example.com:8443 \
    --ca-file ./ca.crt  # Must obtain this file out-of-band
```

**Problems:**

1. **Manual CA distribution:** Colony admins must share full CA certificate
   files
   out-of-band (email, Slack, etc.) - large and error-prone
2. **Error-prone:** Users may use wrong CA, wrong endpoint, or misconfigure TLS
3. **No standardized trust verification:** No consistent way to verify CA
   authenticity
4. **Inconsistent with WireGuard flow:** WireGuard setup uses Discovery for
   automatic key exchange, but public endpoint doesn't
5. **Inconsistent with agent bootstrap:** Agents use CA fingerprint verification
   (RFD 048), but CLI has no equivalent

### Why This Matters

- **UX friction:** Multiple steps to connect to a remote colony
- **Security gap:** No trusted source for CA certificates
- **Adoption barrier:** Complex setup discourages public endpoint use
- **Inconsistency:** Discovery helps with mesh setup but not public endpoint
  setup

## Solution

### Overview

Extend the Discovery Service to act as a registry for colony public endpoint
information, with mandatory CA fingerprint verification:

1. **Colony registration:** When a colony starts with a public endpoint enabled,
   it registers the endpoint URL and CA certificate with Discovery
2. **Credential sharing:** Colony owner shares colony ID + CA fingerprint with
   CLI users (via `coral colony export` output)
3. **CLI lookup:** Users run `coral colony add-remote` with the colony ID and CA
   fingerprint; CLI fetches the CA certificate from Discovery and verifies it
4. **Trust model:** Out-of-band fingerprint is the trust anchor (not Discovery).
   Discovery only provides CA certificate distribution. This matches the agent
   bootstrap trust model (RFD 048)

### Architecture

```
Colony Registration Flow:
┌──────────────┐    RegisterColony    ┌───────────────────┐
│    Colony    │─────────────────────▶│ Discovery Service │
│              │  + PublicEndpoint:   │                   │
│  :8443 HTTPS │    - URL             │  Stores:          │
│  + CA cert   │    - CA cert         │  - mesh_id        │
└──────────────┘    - CA fingerprint  │  - pubkey         │
                                      │  - endpoints      │
                                      │  - public_endpoint│
                                      └───────────────────┘

Credential Sharing Flow (out-of-band):
┌──────────────┐                      ┌──────────────┐
│ Colony Owner │  coral colony export │   CLI User   │
│              │─────────────────────▶│              │
│              │  Shares:             │  Receives:   │
│              │  - colony_id         │  - colony_id │
│              │  - ca_fingerprint    │  - ca_fp     │
└──────────────┘  (email, Slack, etc) └──────────────┘

CLI Connection Flow:
┌──────────────┐   LookupColony       ┌───────────────────┐
│   CLI User   │─────────────────────▶│ Discovery Service │
│              │   {colony_id}        │                   │
│ Has:         │                      │  Returns:         │
│ - colony_id  │◀─────────────────────│  - URL            │
│ - ca_fp      │   PublicEndpoint:    │  - CA cert        │
└──────────────┘   - URL, CA cert     │  - fingerprint    │
       │                              └───────────────────┘
       │
       ▼
┌──────────────────────────────────────┐
│ Fingerprint Verification             │
│                                      │
│ computed_fp = sha256(received_ca)    │
│ if computed_fp != expected_ca_fp:    │
│     REJECT (possible MITM)           │
│ else:                                │
│     TRUST CA cert                    │
└──────────────────────────────────────┘
       │
       ▼
┌──────────────┐
│ Local Config │  ~/.coral/colonies/prod/config.yaml
│              │    remote:
│              │      endpoint: https://...
│              │      certificate_authority: ca.crt
└──────────────┘
```

### Key Design Decisions

#### 1. Discovery as CA Certificate Distribution (Not Trust Anchor)

**Decision:** Discovery Service stores and distributes CA certificates, but the
trust anchor is the CA fingerprint shared out-of-band by the colony owner.

**Rationale:**

- Consistent with agent bootstrap trust model (RFD 048)
- Discovery compromise cannot lead to MITM attacks (fingerprint verification
  catches tampered CAs)
- Similar to Kubernetes `kubeadm join --discovery-token-ca-cert-hash`
- Out-of-band fingerprint sharing is already standard practice (SSH host keys)

**Security property:** If Discovery is compromised, CLI users detect the
fingerprint mismatch and reject the connection.

#### 2. Mandatory Fingerprint Verification

**Decision:** CA fingerprint is required when using `--from-discovery`. There is
no "trust Discovery blindly" mode.

**Rationale:**

- Fingerprint + colony ID form the "colony connection credentials" shared by
  colony owner
- Matches security model of agent bootstrap (RFD 048) where fingerprint is
  always verified
- Prevents class of attacks where Discovery compromise leads to MITM
- Small UX cost (one extra parameter) for significant security benefit

```bash
# Fingerprint is REQUIRED - no automatic trust mode
coral colony add-remote prod \
    --from-discovery \
    --colony-id my-app-prod-a3f2e1 \
    --ca-fingerprint sha256:e3b0c44298fc1c149afbf4c8996fb924...
```

#### 3. CA Certificate Storage in Discovery

**Decision:** Store full CA certificate (PEM, base64-encoded) in Discovery, not
just fingerprint.

**Rationale:**

- CLI needs the actual certificate to configure TLS
- Fingerprint alone is insufficient for TLS verification
- Storage cost is minimal (~2KB per colony)
- Similar to kubeconfig's `certificate-authority-data` field

#### 4. Optional Public Endpoint Registration

**Decision:** Colonies only register public endpoint info if
`public_endpoint.enabled: true`.

**Rationale:**

- Maintains backward compatibility
- Mesh-only colonies don't expose unnecessary info
- Opt-in for public endpoint exposure

### Configuration

**Colony config (registers with Discovery):**

```yaml
# ~/.coral/colonies/my-colony/config.yaml
public_endpoint:
    enabled: true
    host: 0.0.0.0
    port: 8443

    # New: control Discovery registration
    discovery:
        register: true  # Default: true when public_endpoint.enabled
        advertise_url: https://colony.example.com:8443  # Optional override
```

**CLI usage:**

```bash
# Connect using colony credentials from `coral colony export` (recommended)
coral colony add-remote prod \
    --from-discovery \
    --colony-id my-app-prod-a3f2e1 \
    --ca-fingerprint sha256:e3b0c44298fc1c149afbf4c8996fb924...

# Override Discovery endpoint (for private Discovery servers)
coral colony add-remote prod \
    --from-discovery \
    --colony-id my-app-prod-a3f2e1 \
    --ca-fingerprint sha256:e3b0c44298fc1c149afbf4c8996fb924... \
    --discovery-endpoint https://discovery.internal:8080

# Still supported: manual configuration (no Discovery)
coral colony add-remote prod \
    --endpoint https://colony.example.com:8443 \
    --ca-file ./ca.crt
```

**Enhanced `coral colony export` output:**

The existing `coral colony export` command is enhanced to include the CA
fingerprint and show the `add-remote` command for CLI users:

```bash
$ coral colony export my-app-prod-a3f2e1

# Coral Colony Credentials
# Generated: 2026-01-17 12:16:52
# SECURITY: Keep these credentials secure. Do not commit to version control.

export CORAL_COLONY_ID="my-app-prod-a3f2e1"
export CORAL_CA_FINGERPRINT="sha256:e3b0c44298fc1c149afbf4c8996fb924..."
export CORAL_DISCOVERY_ENDPOINT="https://discovery.coral.dev:8080"

# To deploy agents:
#   eval $(coral colony export my-app-prod-a3f2e1)

# To connect CLI users (share colony ID + CA fingerprint):
#   coral colony add-remote <name> \
#       --from-discovery \
#       --colony-id my-app-prod-a3f2e1 \
#       --ca-fingerprint sha256:e3b0c44298fc1c149afbf4c8996fb924...
```

## API Changes

### Discovery Proto Extensions

**File: `proto/coral/discovery/v1/discovery.proto`**

```protobuf
// Extend RegisterColonyRequest
message RegisterColonyRequest {
    // ... existing fields (mesh_id, pubkey, endpoints, etc.) ...

    // Public HTTPS endpoint information (RFD 031, RFD 085)
    PublicEndpointInfo public_endpoint = 10;
}

// Extend LookupColonyResponse
message LookupColonyResponse {
    // ... existing fields ...

    // Public HTTPS endpoint information (RFD 031, RFD 085)
    PublicEndpointInfo public_endpoint = 13;
}

// Public endpoint information for non-WireGuard CLI access.
message PublicEndpointInfo {
    // Whether the public endpoint is enabled and advertised.
    bool enabled = 1;

    // Public HTTPS endpoint URL (e.g., "https://colony.example.com:8443").
    string url = 2;

    // CA certificate for TLS verification (PEM-encoded, then base64).
    // Clients use this to verify the colony's TLS certificate.
    string ca_cert = 3;

    // CA certificate fingerprint for TOFU verification.
    CertificateFingerprint ca_fingerprint = 4;

    // When the public endpoint info was last updated.
    google.protobuf.Timestamp updated_at = 5;
}

// Algorithm used for certificate fingerprint computation.
enum FingerprintAlgorithm {
    FINGERPRINT_ALGORITHM_UNSPECIFIED = 0;
    FINGERPRINT_ALGORITHM_SHA256 = 1;
    // Reserved for future use:
    // FINGERPRINT_ALGORITHM_SHA384 = 2;
    // FINGERPRINT_ALGORITHM_SHA512 = 3;
    // FINGERPRINT_ALGORITHM_BLAKE3 = 4;
}

// Certificate fingerprint with explicit algorithm specification.
message CertificateFingerprint {
    // Hash algorithm used to compute the fingerprint.
    FingerprintAlgorithm algorithm = 1;

    // Raw fingerprint bytes (not hex-encoded).
    // For SHA256, this is exactly 32 bytes.
    bytes value = 2;
}
```

### CLI Commands

**New flags for `coral colony add-remote`:**

```bash
coral colony add-remote <name> [flags]

Flags:
      --endpoint string          Colony's public HTTPS endpoint URL (manual mode)
      --ca-file string           Path to CA certificate file (manual mode)
      --ca-data string           Base64-encoded CA certificate (manual mode)
      --ca-fingerprint string    CA fingerprint for verification (required with --from-discovery,
                                 optional with manual mode for continuous verification)
      --insecure                 Skip TLS verification (manual mode only, testing only,
                                 mutually exclusive with --from-discovery)
      --from-discovery           Fetch endpoint and CA from Discovery Service
      --colony-id string         Colony ID (required with --from-discovery)
      --discovery-endpoint       Override Discovery Service URL
      --set-default              Set this colony as the default
```

**Example outputs:**

```bash
$ coral colony add-remote prod \
    --from-discovery \
    --colony-id my-app-prod-a3f2e1 \
    --ca-fingerprint sha256:e3b0c44298fc1c149afbf4c8996fb924...

Fetching colony info from Discovery Service...
  Colony ID: my-app-prod-a3f2e1
  Public Endpoint: https://colony.example.com:8443

Verifying CA certificate...
  Expected fingerprint: sha256:e3b0c44298fc1c149afbf4c8996fb924...
  Received fingerprint: sha256:e3b0c44298fc1c149afbf4c8996fb924...
  ✓ Fingerprint verified

Remote colony "prod" added successfully!
Config: ~/.coral/colonies/prod/config.yaml
CA cert: ~/.coral/colonies/prod/ca.crt

Usage:
  export CORAL_API_TOKEN=<your-token>
  coral colony status
```

```bash
$ coral colony add-remote prod \
    --from-discovery \
    --colony-id my-app-prod-a3f2e1 \
    --ca-fingerprint sha256:wrong...

Fetching colony info from Discovery Service...
  Colony ID: my-app-prod-a3f2e1
  Public Endpoint: https://colony.example.com:8443

Verifying CA certificate...
  Expected fingerprint: sha256:wrong...
  Received fingerprint: sha256:e3b0c44298fc1c149afbf4c8996fb924...

Error: CA fingerprint mismatch!

The CA certificate from Discovery does not match the expected fingerprint.
This could indicate:
  - A man-in-the-middle attack
  - Compromised Discovery Service
  - Incorrect fingerprint provided by colony owner
  - Colony CA was rotated (get new fingerprint from colony owner)

Connection aborted. Verify the fingerprint with the colony owner.
```

```bash
$ coral colony add-remote prod --from-discovery

Error: --colony-id and --ca-fingerprint are required with --from-discovery

Usage:
  coral colony add-remote prod \
      --from-discovery \
      --colony-id <colony-id> \
      --ca-fingerprint <sha256:...>

Get these values from the colony owner (coral colony export).
```

```bash
$ coral colony add-remote prod \
    --from-discovery \
    --colony-id my-app-prod-a3f2e1 \
    --ca-fingerprint sha256:e3b0c44298fc1c149afbf4c8996fb924... \
    --insecure

Error: --insecure cannot be used with --from-discovery

The --from-discovery flow requires fingerprint verification for security.
Use --insecure only with manual mode (--endpoint + --ca-file) for testing.
```

### Continuous Fingerprint Verification

The CLI stores the expected CA fingerprint in the local colony config. On every
subsequent command, the CLI verifies that the CA certificate on disk still
matches the stored fingerprint. This protects against local file tampering.

**Stored config (`~/.coral/colonies/prod/config.yaml`):**

```yaml
remote:
    endpoint: https://colony.example.com:8443
    certificate_authority: ca.crt
    ca_fingerprint:
        algorithm: sha256
        value: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
```

**Verification on every command:**

```bash
$ coral colony status

# CLI internally:
# 1. Load CA cert from ~/.coral/colonies/prod/ca.crt
# 2. Compute fingerprint: sha256(ca_cert)
# 3. Compare against stored ca_fingerprint
# 4. If mismatch → abort with tamper warning
# 5. If match → proceed with TLS connection using CA

Colony: my-app-prod-a3f2e1 [ONLINE]
  Agents: 5 connected
```

**Tamper detection:**

```bash
$ coral colony status

Error: CA certificate fingerprint mismatch!

The CA certificate at ~/.coral/colonies/prod/ca.crt does not match
the expected fingerprint stored in config.

  Expected: sha256:e3b0c44298fc1c149afbf4c8996fb924...
  Computed: sha256:ffffffff00000000ffffffff00000000...

This could indicate:
  - Local file tampering
  - Accidental file corruption
  - Unauthorized modification

To resolve:
  1. Re-run 'coral colony add-remote' with the correct fingerprint
  2. Or contact the colony owner for the current CA fingerprint
```

### Colony Registration

**Colony startup (in `internal/colony/server.go`):**

```go
// When registering with Discovery, include public endpoint info
if cfg.PublicEndpoint.Enabled {
fingerprint := sha256.Sum256(caCertPEM)
req.PublicEndpoint = &discoverypb.PublicEndpointInfo{
Enabled: true,
Url:     cfg.PublicEndpoint.AdvertiseURL(),
CaCert:  base64.StdEncoding.EncodeToString(caCertPEM),
CaFingerprint: &discoverypb.CertificateFingerprint{
Algorithm: discoverypb.FingerprintAlgorithm_FINGERPRINT_ALGORITHM_SHA256,
Value:     fingerprint[:],
},
UpdatedAt: timestamppb.Now(),
}
}
```

## Security Considerations

### Trust Model

**Out-of-Band Fingerprint as Trust Anchor (Not Discovery):**

The trust model for CLI connections mirrors the agent bootstrap model (RFD 048):

| Component         | Role                              | Trusted?                      |
|-------------------|-----------------------------------|-------------------------------|
| Colony owner      | Shares colony ID + CA fingerprint | Yes (out-of-band)             |
| Discovery Service | Distributes CA certificate        | No (verified via fingerprint) |
| CA fingerprint    | Cryptographic verification        | Trust anchor                  |

**Key security property:** Discovery is NOT trusted for identity. It only
provides convenience (CA certificate distribution). The fingerprint shared
out-of-band by the colony owner is the actual trust anchor.

**Threat: Compromised Discovery Service**

- Attacker could distribute malicious CA certs
- **Protection:** CLI verifies fingerprint before trusting CA
- **Result:** Attack detected, connection rejected

**Threat: Man-in-the-Middle between CLI and Discovery**

- Attacker intercepts CLI → Discovery communication
- **Protection:** Fingerprint verification catches tampered CAs
- **Result:** Attack detected, connection rejected

**Threat: Fingerprint shared insecurely**

- If fingerprint is intercepted during sharing, attacker could substitute their
  own
- **Mitigation:** Use secure channels for sharing (encrypted messaging,
  in-person)
- **Note:** This is the same trust model as SSH host key fingerprints

### Consistent Trust Model Across Clients

This design ensures CLI and agent bootstrap use the same trust model:

```
Agent Bootstrap (RFD 048):
  Trust anchor: CA fingerprint in deployment config (env var, K8s secret)
  CA source: Discovery Service
  Verification: Fingerprint must match

CLI Connection (RFD 085):
  Trust anchor: CA fingerprint from colony owner (coral colony export)
  CA source: Discovery Service
  Verification: Fingerprint must match
```

### Credential Sharing Flow

```
Colony Owner                           CLI User
      │                                   │
      │  coral colony export              │
      │  → colony_id + ca_fingerprint     │
      │                                   │
      │  Shares via secure channel:       │
      │  - Encrypted message              │
      │  - Internal docs                  │
      │  - In-person                      │
      │───────────────────────────────────▶│
      │                                   │
      │                                   ▼
      │                       coral colony add-remote prod \
      │                         --from-discovery \
      │                         --colony-id ... \
      │                         --ca-fingerprint sha256:...
      │                                   │
      │                                   ▼
      │                       CLI fetches CA from Discovery
      │                       CLI computes sha256(CA)
      │                       CLI verifies fingerprint match
      │                                   │
      │                                   ▼
      │                       ✓ CA trusted, config created
```

### Attack Scenario Summary

| Attack                           | Protection               | Result                                             |
|----------------------------------|--------------------------|----------------------------------------------------|
| Discovery compromised            | Fingerprint verification | Detected, rejected                                 |
| Discovery MITM                   | Fingerprint verification | Detected, rejected                                 |
| DNS hijacking to fake Discovery  | Fingerprint verification | Detected, rejected                                 |
| Wrong colony (typo in colony ID) | Fingerprint won't match  | Detected, rejected                                 |
| Colony impersonation             | Fingerprint won't match  | Detected, rejected                                 |
| CA rotation without notification | Fingerprint mismatch     | Detected (user contacts owner for new fingerprint) |
| Local CA file tampering          | Continuous verification  | Detected on next CLI command                       |
| Discovery enumeration            | RFD 049 authorization    | Rate limited, requires colony_id knowledge         |

### Discovery Enumeration Risk

**Threat:** If Discovery allows unauthenticated `LookupColony` calls, attackers
could scrape the service to discover public endpoint URLs of colonies.

**Mitigations (via RFD 049):**

1. **Colony ID as capability URL:** Colony IDs are high-entropy UUIDs (e.g.,
   `my-app-prod-a3f2e1`). Knowing the colony ID is a prerequisite for lookup,
   making blind enumeration impractical.

2. **Rate limiting:** Discovery enforces per-IP rate limits on lookup requests
   to prevent brute-force enumeration attempts.

3. **Authorization policies:** RFD 049 allows colonies to configure Discovery
   authorization policies that can restrict which IPs or authenticated clients
   can perform lookups.

4. **Audit logging:** All lookup attempts are logged with source IP, enabling
   detection of enumeration attempts.

**Note:** The public endpoint URL alone is not sensitive—an attacker still needs
valid API credentials to interact with the colony. However, URL exposure may be
undesirable for some deployments. Colonies requiring strict privacy should use
WireGuard mesh access only (disable `public_endpoint.discovery.register`).

### CA Certificate Handling

- CA certificates stored in Discovery are PEM-encoded, then base64
- CLI writes CA to local file with restricted permissions (0600)
- CA certificates are validated before use (must be valid X.509)
- Expired or invalid CAs are rejected

## Implementation Plan

### Phase 1: Discovery Proto Extensions

- [x] Add `PublicEndpointInfo` message to `discovery.proto`
- [x] Add `public_endpoint` field to `RegisterColonyRequest`
- [x] Add `public_endpoint` field to `LookupColonyResponse`
- [x] Regenerate protobuf code

### Phase 2: Discovery Service Updates

- [x] Update Discovery storage schema to include public endpoint info
- [x] Update `RegisterColony` handler to store public endpoint info
- [x] Update `LookupColony` handler to return public endpoint info
- [x] Add validation for CA certificate format

### Phase 3: Colony Registration Updates

- [x] Update colony startup to include public endpoint in Discovery registration
- [x] Add `discovery.register` config option (default: true)
- [x] Add `discovery.advertise_url` config option for URL override
- [x] Compute and include CA fingerprint

### Phase 4: CLI Updates

- [x] Enhance `coral colony export` to include CA fingerprint
- [x] Enhance `coral colony export` to show `add-remote` command example
- [x] Add `--from-discovery` flag to `coral colony add-remote`
- [x] Add `--colony-id` flag (required with `--from-discovery`)
- [x] Add `--ca-fingerprint` flag (required with `--from-discovery`)
- [x] Enforce `--insecure` mutually exclusive with `--from-discovery`
- [x] Implement Discovery lookup for public endpoint info
- [x] Implement mandatory CA fingerprint verification
- [x] Add `--discovery-endpoint` flag for Discovery URL override
- [x] Write CA certificate to colony config directory
- [x] Store CA fingerprint in local config for continuous verification
- [x] Implement continuous fingerprint verification on every CLI command
- [x] Error if `--from-discovery` used without `--colony-id` and
  `--ca-fingerprint`

### Phase 5: Testing & Documentation

- [x] Unit tests for Discovery proto changes
- [x] Integration tests for colony registration with public endpoint
- [x] Integration tests for CLI `--from-discovery` flow
- [x] E2E test: full flow from colony start to CLI connection
- [x] Security tests: fingerprint verification, invalid CA rejection
- [x] Security tests: `--insecure` / `--from-discovery` mutual exclusivity
- [x] Security tests: continuous verification detects local CA tampering
- [x] Update CONFIG.md with new options
- [x] Update CLI.md with `--from-discovery` usage

## Testing Strategy

### Unit Tests

**Discovery Service:**

- RegisterColony with public endpoint info stores correctly
- RegisterColony without public endpoint info works (backward compat)
- LookupColony returns public endpoint info when present
- LookupColony returns empty public endpoint for mesh-only colonies

**CLI:**

- `--from-discovery` requires `--colony-id` and `--ca-fingerprint`
- `--insecure` and `--from-discovery` are mutually exclusive
- Error message shown if `--from-discovery` used without required flags
- `--from-discovery` fetches and parses public endpoint info
- CA fingerprint verification passes with matching fingerprint
- CA fingerprint verification fails with mismatched fingerprint (connection
  rejected)
- Invalid CA certificate is rejected
- Clear error messages for Discovery unavailable
- `coral colony export` includes CA fingerprint in output
- `coral colony export` shows `add-remote` command example
- Stored config includes CA fingerprint for continuous verification
- Subsequent commands verify CA file against stored fingerprint
- Tampered CA file is detected and command aborted with clear error

### Integration Tests

**Colony → Discovery:**

- Colony with public endpoint registers info correctly
- Colony without public endpoint doesn't register public info
- Colony re-registration updates public endpoint info

**CLI → Discovery → Local Config:**

- Full flow: lookup colony, fetch CA, create config
- Config is valid and CLI can connect to colony

### Security Tests

- Verify CA fingerprint mismatch is detected and connection rejected
- Verify `--from-discovery` without `--ca-fingerprint` is rejected (no bypass)
- Verify `--insecure` with `--from-discovery` is rejected (mutually exclusive)
- Verify invalid CA certificates are rejected
- Verify CA file is written with correct permissions (0600)
- Verify Discovery communication uses TLS
- Verify compromised Discovery (returns wrong CA) is detected via fingerprint
- Verify fingerprint computation matches expected format (sha256:hex)
- Verify continuous verification: tampering with local CA file is detected on
  next command
- Verify stored fingerprint in config matches the CA file after add-remote

## Migration Strategy

**Backward Compatibility:**

- Existing colonies (without public endpoint) continue to work unchanged
- Discovery Service accepts registrations with or without `public_endpoint`
- CLI `coral colony add-remote` manual flags still work
- `--from-discovery` is additive, not replacing manual flow

**Rollout:**

1. Deploy Discovery Service with proto extensions (backward compatible)
2. Deploy Colony with public endpoint registration
3. Update CLI with `--from-discovery` flag
4. Document new flow in user guides

## Implementation Status

**Core Capability:** ✅ Complete

Discovery Service Public Endpoint Registry is fully implemented. Colonies can
register their public endpoint information (URL, CA certificate, CA fingerprint)
with the Discovery Service, and CLI users can connect to remote colonies using
the TOFU (Trust On First Use) security model via `--from-discovery`.

**Operational Components:**

- ✅ Proto: `PublicEndpointInfo`, `CertificateFingerprint` messages
- ✅ Discovery Service: storage and retrieval of public endpoint info
- ✅ Colony registration: automatic registration with Discovery on startup
- ✅ CLI: `--from-discovery`, `--colony-id`, `--ca-fingerprint` flags
- ✅ CLI: CA fingerprint verification on connect
- ✅ CLI: Continuous fingerprint verification on every command
- ✅ Config: `public_endpoint.discovery.register`, `public_endpoint.discovery.advertise_url`
- ✅ Config: `remote.ca_fingerprint` for stored fingerprint

**What Works Now:**

- Colony registers public endpoint info with Discovery on startup
- `coral colony export` shows `add-remote --from-discovery` command example
- `coral colony add-remote --from-discovery` fetches CA from Discovery
- CA fingerprint is verified against user-provided fingerprint (TOFU security)
- CA certificate and fingerprint are stored locally for continuous verification
- Subsequent CLI commands verify CA file hasn't been tampered with
- Clear error messages for fingerprint mismatch or missing credentials

**Key Files Modified:**

- `proto/coral/discovery/v1/discovery.proto` - PublicEndpointInfo message
- `internal/discovery/registry/registry.go` - Storage schema
- `internal/discovery/server/server.go` - Registration/lookup handlers
- `internal/discovery/client/client.go` - Client lookup with public endpoint
- `internal/cli/colony/cmd_start.go` - Colony registration with public endpoint
- `internal/cli/colony/cmd_add_remote.go` - `--from-discovery` flow
- `internal/cli/colony/cmd_data.go` - Export shows add-remote example
- `internal/cli/helpers/client.go` - Continuous fingerprint verification
- `internal/config/schema.go` - Config types for fingerprint

## Future Work

**CA Rotation Handling** (Future RFD)

- When colony CA is rotated, Discovery gets updated
- CLI could detect CA changes and prompt user to update
- Automatic CA refresh for long-running CLI sessions

**Multi-Colony Discovery** (Future Enhancement)

- List all colonies user has access to from Discovery
- `coral colony list --from-discovery`

**Discovery Service Federation** (Future Enhancement)

- Multiple Discovery servers with replicated data
- CLI can use any Discovery server for lookup

## Appendix

### Comparison with Cloud Provider Patterns

| Provider  | Cluster Config Command                      | CA Distribution        | Fingerprint Verification                    |
|-----------|---------------------------------------------|------------------------|---------------------------------------------|
| **GKE**   | `gcloud container clusters get-credentials` | Fetched from GCP API   | None (trusts API)                           |
| **EKS**   | `aws eks update-kubeconfig`                 | Fetched from AWS API   | None (trusts API)                           |
| **AKS**   | `az aks get-credentials`                    | Fetched from Azure API | None (trusts API)                           |
| **K8s**   | `kubeadm join`                              | Fetched from API       | Required (`--discovery-token-ca-cert-hash`) |
| **Coral** | `coral colony add-remote --from-discovery`  | Fetched from Discovery | Required (`--ca-fingerprint`)               |

**Coral provides stronger security than cloud providers:** Cloud providers trust
their APIs completely for CA distribution. If the API is compromised, users get
malicious CAs with no detection. Coral requires fingerprint verification,
detecting compromised Discovery services.

Coral follows the Kubernetes `kubeadm join` pattern, which also requires CA
fingerprint verification for secure cluster joining.

### Example: Full Connection Flow

```bash
# 1. Colony admin starts colony with public endpoint
$ coral colony start
Colony started: my-app-prod-a3f2e1
  Mesh: 10.42.0.1:41820
  Public: https://colony.example.com:8443
  Registered with Discovery: discovery.coral.dev

# 2. Colony admin exports credentials to share with users
$ coral colony export my-app-prod-a3f2e1

# Coral Colony Credentials
# Generated: 2026-01-17 12:16:52
# SECURITY: Keep these credentials secure. Do not commit to version control.

export CORAL_COLONY_ID="my-app-prod-a3f2e1"
export CORAL_CA_FINGERPRINT="sha256:e3b0c44298fc1c149afbf4c8996fb924..."
export CORAL_DISCOVERY_ENDPOINT="https://discovery.coral.dev:8080"

# To deploy agents:
#   eval $(coral colony export my-app-prod-a3f2e1)

# To connect CLI users (share colony ID + CA fingerprint):
#   coral colony add-remote <name> \
#       --from-discovery \
#       --colony-id my-app-prod-a3f2e1 \
#       --ca-fingerprint sha256:e3b0c44298fc1c149afbf4c8996fb924...

# 3. Colony admin creates API token and shares with CLI user
$ coral colony token create cli-user --permissions status,query
Token: cpt_abc123...

# 4. Admin shares with CLI user (via secure channel):
#    - Colony ID: my-app-prod-a3f2e1
#    - CA fingerprint: sha256:e3b0c44298fc1c149afbf4c8996fb924...
#    - API token: cpt_abc123...

# 5. CLI user connects to colony
$ coral colony add-remote prod \
    --from-discovery \
    --colony-id my-app-prod-a3f2e1 \
    --ca-fingerprint sha256:e3b0c44298fc1c149afbf4c8996fb924... \
    --set-default

Fetching colony info from Discovery Service...
  Colony ID: my-app-prod-a3f2e1
  Public Endpoint: https://colony.example.com:8443

Verifying CA certificate...
  ✓ Fingerprint verified

Remote colony "prod" added successfully!

# 6. CLI user sets token and uses CLI
$ export CORAL_API_TOKEN=cpt_abc123...
$ coral colony status
Colony: my-app-prod-a3f2e1 [ONLINE]
  Agents: 5 connected
  Uptime: 3d 12h
```

### Related RFDs

- **RFD 001 (Discovery Service):** Base Discovery Service implementation
- **RFD 031 (Colony Dual Interface):** Public endpoint implementation
- **RFD 047 (Colony CA Infrastructure):** CA certificate generation and
  management
- **RFD 048 (Agent Certificate Bootstrap):** Agent trust model using CA
  fingerprint verification - CLI follows the same pattern
- **RFD 049 (Discovery Authorization):** Discovery Service security model
