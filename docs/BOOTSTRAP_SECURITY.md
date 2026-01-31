# Bootstrap Security Posture

This document describes the security design of agent bootstrap and discovery,
the threat model, known gaps, and mitigations.

**Related RFDs:** 047 (Colony CA), 048 (Agent Certificate Bootstrap),
049 (Discovery Authorization), 085 (Discovery Public Endpoint), 086 (Discovery
Policy Enforcement), 088 (Bootstrap PSK).

## Trust Model

The bootstrap process involves three parties with different trust levels:

| Component     | Trust Level  | Rationale                                   |
|---------------|--------------|---------------------------------------------|
| **Colony**    | Trusted      | Operator-controlled, holds CA private keys. |
| **Agent**     | Semi-trusted | Trusted after certificate issuance.         |
| **Discovery** | Untrusted    | Public service, convenience only.           |

The fundamental principle: **Discovery is a directory, not a trust anchor.**
It distributes endpoints and issues referral tickets, but cannot authorize
agents to join a colony. Trust is established through the Colony's CA hierarchy
and the Bootstrap PSK.

## Security Layers

Agent enrollment is protected by four independent layers. An attacker must
defeat all of them to obtain a valid agent certificate.

### Layer 1: Referral Ticket (Discovery → Colony)

The agent requests a short-lived JWT from Discovery
(`CreateBootstrapToken`). The Colony validates this JWT against Discovery's
JWKS before processing the certificate request.

- **What it prevents:** Unauthenticated CSR flooding against the Colony.
- **What it does NOT prevent:** Anyone can request a token — the endpoint is
  unauthenticated.

### Layer 2: CA Fingerprint Validation (Agent → Colony)

During the TLS handshake, the agent extracts the Colony's Root CA from the
certificate chain and computes its SHA-256 fingerprint. If the fingerprint
does not match the pre-configured value, the connection is aborted.

- **What it prevents:** Man-in-the-middle attacks. An attacker who controls
  Discovery or DNS cannot redirect agents to a fake colony without possessing
  the legitimate Colony's Root CA private key.
- **What it does NOT prevent:** Authorization. The fingerprint is public
  information (see "Discovery Information Disclosure" below).

### Layer 3: Colony ID Verification (Agent → Colony)

The agent validates that the Colony's server certificate contains the expected
colony ID in a SPIFFE SAN (`spiffe://coral/colony/{colony-id}`).

- **What it prevents:** Cross-colony impersonation. Even with a valid CA
  fingerprint, a Colony cannot claim to be a different colony.
- **What it does NOT prevent:** Authorization.

### Layer 4: Bootstrap PSK (Agent → Colony)

The agent presents a pre-shared key in the `RequestCertificate` call. The
Colony validates the PSK before issuing any certificate. The PSK is a 256-bit
random secret generated at colony initialization, distributed out-of-band, and
never sent to Discovery.

- **What it prevents:** Unauthorized enrollment. Without the PSK, an attacker
  cannot obtain a certificate even with knowledge of the colony ID, CA
  fingerprint, and a valid referral ticket.
- **What it does NOT prevent:** An attacker who has obtained the PSK through
  a separate compromise.

## Discovery Service Threat Model

The Discovery service is deliberately treated as untrusted. It runs as a
public Cloudflare Worker with no authentication on its endpoints.

### Information Disclosure

`LookupColony` returns the following to any caller:

- Colony mesh ID, WireGuard public key, endpoints (IP:port).
- Mesh IPv4/IPv6 addresses, connect port, public HTTPS port.
- **CA certificate and CA fingerprint** (via RFD 085 `public_endpoint` field).
- Metadata and observed endpoints.

**Implication:** The CA fingerprint is not a secret. Any attacker who knows a
colony's mesh ID can retrieve the CA certificate and compute the fingerprint.
This is why the Bootstrap PSK (RFD 088) exists — it provides the authorization
secret that the CA fingerprint cannot.

### Registration Attacks

| Attack                         | Current Protection                    | Residual Risk                                     |
|--------------------------------|---------------------------------------|---------------------------------------------------|
| **Colony ID squatting**        | Split-brain check (first pubkey wins) | Attacker registers before legitimate colony.      |
| **Agent entry pollution**      | None                                  | Attacker registers fake agents under any mesh_id. |
| **Endpoint poisoning**         | None (no request signing)             | Attacker overwrites agent endpoints.              |
| **Bootstrap token harvesting** | 60-second TTL                         | Attacker can request tokens for any colony/agent. |
| **Registration flooding**      | Cloudflare platform limits            | No application-level rate limiting.               |

### What Discovery Compromise Enables

If an attacker fully controls the Discovery service:

- **Can** return attacker-controlled endpoints for any colony.
- **Can** issue valid referral tickets for any colony/agent.
- **Can** enumerate all registered colonies and their network topology.
- **Cannot** cause agents to connect to a fake colony (CA fingerprint mismatch).
- **Cannot** obtain valid agent certificates (PSK required, validated by
  Colony).
- **Cannot** decrypt mesh traffic (no WireGuard private keys).

## Post-Bootstrap Security

Once an agent has a valid certificate:

- All communication uses mTLS over WireGuard. The agent authenticates with
  its certificate; the Colony validates the certificate chain.
- Certificate renewal uses the existing mTLS connection — no Discovery
  interaction, no PSK, no referral ticket.
- Individual agent certificates can be revoked without affecting other agents.
- Certificates expire after 90 days and are automatically renewed at 60 days.

## Known Gaps and Future Work

### No Request Signing on Discovery

Registration requests (`RegisterColony`, `RegisterAgent`) do not require a
signature proving ownership of the submitted public key. An attacker can
overwrite existing entries with fake endpoints. This causes a denial-of-service
(agents find unreachable endpoints) but not a security compromise (CA
fingerprint validation catches impersonation).

**Mitigation:** Agents try all returned endpoints and validate each via CA
fingerprint. Fake endpoints are skipped.

**Future:** Add Ed25519 signature over registration requests, verified against
the submitted public key.

### No Rate Limiting on Discovery

All Discovery endpoints lack application-level rate limiting. An attacker can
flood registrations, exhaust storage, or enumerate colonies.

**Mitigation:** Cloudflare platform limits provide baseline protection.
TTL-based
expiration (300 seconds) bounds storage growth.

**Future:** Per-IP and per-mesh-id rate limiting (RFD 086).

### Colony ID Predictability

Colony IDs are human-readable strings (`my-app-prod-a3f2e1`). An attacker can
guess or enumerate them. Combined with public CA certs in Discovery, this means
the PSK is the only barrier to enrollment.

**Mitigation:** The PSK has 256 bits of entropy and is validated over an
already-authenticated TLS connection.

### PSK as Single Authorization Factor

The Bootstrap PSK is the sole authorization secret for enrollment. If it leaks
(e.g., committed to a repository, shared over insecure channel), any attacker
can join the colony.

**Mitigations:**

- PSK rotation with grace period (old + new accepted during transition).
- Existing agents are unaffected by rotation (renewals use mTLS).
- PSK is stored encrypted in Colony DuckDB (AES-256-GCM, key derived from Root
  CA private key).
- PSK is transmitted only over the CA-fingerprint-validated TLS connection.

**Future:** RFD 086 adds defense-in-depth at the Discovery layer (CIDR
allowlists, agent ID patterns, quotas) to complement the PSK.

## Summary of Trust Anchors

| Secret                     | Where Stored              | Who Needs It                   | What It Protects          |
|----------------------------|---------------------------|--------------------------------|---------------------------|
| **Root CA private key**    | Colony filesystem (0600)  | Colony only                    | CA hierarchy integrity.   |
| **Bootstrap PSK**          | Colony DuckDB (encrypted) | Colony + agents (at bootstrap) | Enrollment authorization. |
| **Agent private key**      | Agent filesystem (0600)   | Agent only                     | Agent identity (mTLS).    |
| **WireGuard private keys** | Colony + agent filesystem | Each peer                      | Mesh encryption.          |

| Public Information         | Where Available          | Security Impact                             |
|----------------------------|--------------------------|---------------------------------------------|
| **Colony ID (mesh_id)**    | Discovery, config        | None — public identifier.                   |
| **CA certificate**         | Discovery, TLS handshake | None — public, used for MITM detection.     |
| **CA fingerprint**         | Derived from CA cert     | None — public, not an authorization secret. |
| **WireGuard public keys**  | Discovery                | None — public by design.                    |
| **Referral tickets (JWT)** | Discovery endpoint       | Low — short-lived, PSK still required.      |
