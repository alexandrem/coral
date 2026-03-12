# PKI Infrastructure & Trust Model

Coral's security architecture is built on a hierarchical **Public Key
Infrastructure (PKI)** that enables zero-trust communication across the
distributed mesh. Instead of relying on long-lived shared secrets, Coral
establishes identity through cryptographic certificates and a unique bootstrap
mechanism.

## 1. The CA Hierarchy

Coral implements a **Four-Level PKI** structure managed by the Central Colony.
This hierarchy separates concerns and limits the impact of certificate
compromise.

### A. Root CA (The Trust Anchor)

- **Scope**: One per Colony.
- **Validity**: 10 years.
- **Purpose**: Signs all intermediate CAs. The Root CA's SHA256 fingerprint is
  the primary trust value distributed to Agents during deployment.

### B. Intermediate CAs (The Delegators)

- **Validity**: 1 year.
- **Bootstrap Intermediate**: Used strictly during the initial TLS handshake for
  fingerprint validation. It does not sign operational certificates.
- **Server Intermediate**: Signs the Colony's TLS server certificates.
- **Agent Intermediate**: Signs the short-lived client certificates for
  legitimate Agents.

### C. Operational Certificates (The Identities)

- **Validity**: 90 days (Agents), 1 year (Colony Server).
- **SPIFFE ID**: Every certificate includes a **SPIFFE ID** in the Subject
  Alternative Name (SAN), providing a platform-agnostic identity (e.g.,
  `spiffe://coral/colony/{colony-id}/agent/{agent-id}`).

## 2. The Identity Model: SPIFFE IDs

Coral adopts the **SPIFFE** (Secure Production Identity Framework for Everyone)
standard to ensure that every component has a verifiable name.

- **Colony Identity**: `spiffe://coral/colony/{colony-id}`
- **Agent Identity**: `spiffe://coral/colony/{colony-id}/agent/{agent-id}`

This model ensures that an Agent from `colony-A` cannot impersonate an Agent in
`colony-B`, even if they share the same ID string, as the full SPIFFE URI will
differ.

## 3. Trust Establishment: The Fingerprint Bootstrap

Coral avoids the "Initial Secret" problem by using **Root CA Fingerprint
Validation**, similar to SSH host key verification or Kubernetes kubelet
bootstrap.

### The Bootstrap Flow

1. **Implicit Trust**: The operator provides the expected
   **Root CA Fingerprint** (SHA256) to the Agent via environment variables or
   config.
2. **Handshake Verification**: During the first connection to the Colony, the
   Agent receives the full certificate chain. It computes the SHA256 fingerprint
   of the Root CA in that chain.
3. **Identity Confirmation**:
   - If the fingerprint matches the configured value, the Agent trusts the
     Colony's identity.
   - The Agent also validates that the Colony's SPIFFE ID in the server
     certificate matches the expected `colony-id`.
4. **CSR Submission**: Securely trusting the server, the Agent generates a local
   Ed25519 keypair and sends a Certificate Signing Request (CSR) to the Colony.
5. **Referral Tickets**: To prevent "unlimited issuance," the Agent must present
   a short-lived **Referral Ticket** obtained from the Discovery service (which
   implements policy-based authorization).

## 4. Operational Zero-Trust (mTLS)

Once the bootstrap is complete, all subsequent communication uses **Mutual TLS (
mTLS)**.

- **Encryption**: All gRPC/ConnectRPC traffic is encrypted in transit.
- **Authentication**: Both the Colony and the Agent present certificates. The
  Colony verifies the Agent's client certificate against its internal Agent
  Intermediate CA.
- **Revocation**: The Colony maintains a **Certificate Revocation List (CRL)**.
  If an Agent is decommissioned or suspected of compromise, its certificate is
  revoked, immediately blocking all mesh access.

## 5. Certificate Lifecycle & Auto-Renewal

To minimize the impact of a stolen certificate, Agent identities are
short-lived.

- **Graceful Renewal**: Agents monitor their own certificate expiry. At 70% of
  the lifetime (approx. 60 days), the Agent automatically generates a new CSR
  and requests a renewal.
- **Self-Authentication**: Renewal requests are authenticated using the
  _existing_ valid mTLS certificate, meaning an Agent doesn't need to
  re-interact with the Discovery service for routine rotation.
- **Partition Tolerance**: If the Colony is unreachable during the renewal
  window, the Agent continues to retry with exponential backoff until the
  primary certificate expires.

## 6. Security Analysis

| Threat                  | Coral Defense                                                                                                    |
|-------------------------|------------------------------------------------------------------------------------------------------------------|
| **MITM on Discovery**   | Agent ignores Discovery's host info and validates the Colony strictly via Fingerprint.                           |
| **Mesh Impersonation**  | Certificates are scoped to a specific Colony ID via SPIFFE; cross-colony access is cryptographically impossible. |
| **Compromised Agent**   | Individual Agent certificates can be revoked without affecting the rest of the fleet.                            |
| **Long-term Key Theft** | Ed25519 keys are regenerated periodically during the 90-day renewal cycle.                                       |

## Future Engineering Notes

- **TPM & Secure Enclave Integration**: Instead of storing Ed25519 private keys
  in `~/.coral/certs` (even with `0600` permissions), agents should optionally
  support hardware-backed keys via TPM 2.0 or Apple Secure Enclave. This ensures
  the key cannot be extracted even if the host is compromised.
- **External CA Backend (Vault/cert-manager)**: Allow the Colony to delegate
  certificate signing to an industrial-grade PKI like HashiCorp Vault or
  Kubernetes `cert-manager` via a "Signer" interface, rather than managing its
  own Root CA on disk.
- **Ephemeral Session Identities**: For high-privilege interactive tasks (like
  `coral shell`), issue "Just-in-Time" certificates with a TTL equivalent to the
  session duration (e.g., 1 hour), ensuring that stolen session credentials have
  a near-zero window of utility.
- **OCSP Stapling & Short-lived TTLs**: As the fleet grows, CRLs can become a
  performance bottleneck. Shifting to shorter global TTLs (e.g., 24 hours)
  combined with OCSP stapling would move revocation logic from a "list-based"
  check to a "validity-based" proof.
- **Quantum-Safe Transition**: Track the standardization of Post-Quantum
  Cryptography (PQC) algorithms (like ML-KEM/Kyber). The hierarchical CA
  structure should eventually support multi-algorithm chains (Ed25519 + PQC) to
  ensure long-term trust in a post-quantum world.

## Related Design Documents (RFDs)

- **[RFD 047](../../RFDs/047-colony-ca-infrastructure.md)**: Colony CA Infrastructure & Policy Signing.
- **[RFD 048](../../RFDs/048-agent-certificate-bootstrap.md)**: Agent Certificate Bootstrap mechanism.
