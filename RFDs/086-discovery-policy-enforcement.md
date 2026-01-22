---
rfd: "086"
title: "Discovery-Based Policy Enforcement"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "049" ]
related_rfds: [ "047", "048", "049" ]
areas: [ "security", "discovery", "colony" ]
---

# RFD 086 - Discovery-Based Policy Enforcement

**Status:** 🚧 Draft

## Summary

This RFD extends the authorization layer defined in RFD 049 by introducing
**Colony-Defined Policies**. While RFD 049 implements the mechanism for
referral tickets, this RFD enables Colonies to define and push specific
authorization rules (quotas, IP allowlists, agent ID patterns) that the
Discovery service must enforce before issuing those tickets.

## Problem

Currently, the Discovery service issues referral tickets based on a global
configuration. There is no way for:

- Individual colonies to restrict which agents can join.
- Enforcing specific agent ID patterns per colony.
- Restricting agent registration to specific IP ranges (CIDRs) per colony.
- Setting per-colony quotas for active agents.

## Solution

Implement a policy pushing and enforcement layer where **Colony defines and
signs policies** and **Discovery enforces them**.

### Policy-Based Authorization

Colony defines and signs authorization policies during initialization or
updates,
enabling Discovery to enforce colony-specific rules.

#### Policy Document

Colony defines and signs authorization policies:

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
   Canonicalization Scheme (JCS) before signing.
2. **Signing**: Ed25519 signature computed over canonical JSON bytes using
   the colony's policy signing key (defined in RFD 047).
3. **Verification**: Discovery re-canonicalizes policy and verifies signature
   using the public key from the provided policy certificate.

## Component Changes

### 1. Colony - Policy Signing & Pushing

- **Policy Signer** (`internal/colony/policy/signer.go`):
    - Define `ColonyPolicy` struct.
    - Implement RFC 8785 JCS canonicalization.
    - Sign policies using the `policy-signing` key from the CA.
- **Policy Pusher** (`internal/colony/policy/pusher.go`):
    - Push signed policy to Discovery via `UpsertColonyPolicy` RPC.
    - Handle retries and versioning.

### 2. Discovery - Policy Enforcement

- **Policy Store** (`internal/discovery/policy/store.go`):
    - Persistent storage for signed policies per colony.
    - Verify policy certificate chains to colony Root CA.
    - Lock colony ID to Root CA fingerprint on first registration.
- **Policy Enforcer** (`internal/discovery/policy/enforcer.go`):
    - Enforce rate limits, quotas, agent ID patterns, and IP allowlists defined
      in the policy.
    - Integrate with the `RequestReferralTicket` (or `CreateBootstrapToken`)
      flow.

## API Changes

### Discovery Service (gRPC)

```protobuf
service DiscoveryService {
    // Colony pushes its signed authorization policy.
    rpc UpsertColonyPolicy(UpsertColonyPolicyRequest) returns (UpsertColonyPolicyResponse);
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
...
```

## Implementation Plan

### Phase 1: Policy Signing (Colony-Side)

- [ ] Add RFC 8785 JCS library dependency (
  `github.com/cyberphone/json-canonicalization`)
- [ ] Implement `ColonyPolicy` struct
- [ ] Implement policy canonicalization and signing
- [ ] Implement `UpsertColonyPolicy` client
- [ ] Add `coral colony policy push` and `show` commands

### Phase 2: Policy Verification & Enforcement (Discovery-Side)

- [ ] Implement policy storage
- [ ] Implement policy signature and chain verification
- [ ] Implement colony ID locking to root CA fingerprint
- [ ] Implement policy enforcement in the ticket issuance flow
- [ ] Add `UpsertColonyPolicy` RPC handler
