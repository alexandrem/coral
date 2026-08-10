# Discovery Service & Coordinated Enrollment

The **Discovery Service** acts as the decentralized rendezvous point for the
Coral mesh. While the Colony and Agent handle the actual data processing and
monitoring, the Discovery service ensures they can find and trust each other
across complex network topologies (NATs, firewalls, and dynamic IP
environments).

It is maintained as a standalone, edge-native service in
the [coral-mesh/discovery](https://github.com/coral-mesh/discovery) repository.

## 1. The "Meeting Point" Pattern

In a distributed edge system, components rarely have static, publicly reachable
IP addresses. Coral solves this using the **Meeting Point Pattern**:

1. **Registry**: The Colony periodically registers its current public-facing
   endpoints (IP/Port) with the Discovery service.
2. **Lookup**: When an Agent starts, it queries Discovery using a unique
   `colony_id` to retrieve the current connection details for its target Colony.
3. **Connectivity**: The Agent then establishes a direct WireGuard tunnel to the
   Colony, bypassing the need for central traffic relaying once the connection
   is formed.

## 2. The Trust Relationship: A Triangle of Security

The trust model in Coral is not a simple hierarchy but a triad between the
**Colony**, the **Agent**, and **Discovery**.

### A. Colony ↔ Discovery

- **Registration**: The Colony registers its WireGuard Public Key and endpoints.
- **Verification**: Discovery ensures that a Colony cannot "hijack" another's ID
  by requiring proof of ownership (managed via initial registration tokens or
  signed policy documents).

### B. Agent ↔ Discovery

- **Rendezvous**: Discovery provides the Agent with the "untrusted" list of
  coordinates for the Colony.
- **Referral Tickets**: Discovery acts as the first gatekeeper. It issues a
  signed **Referral Ticket** (JWT) to the Agent. This ticket is a short-lived
  proof of authorization that the Colony requires for the initial certificate
  issuance.

#### Referral Ticket Details (JWT)

The ticket uses the **EdDSA (Ed25519)** signing algorithm and contains specific
claims that bind the ticket to a singular enrollment attempt:

- **colony_id**: Ensures the ticket can only be used with the intended Colony.
- **agent_id**: Binds the ticket to a specific Agent identity; the Colony will
  verify this matches the Common Name (CN) in the Agent's CSR.
- **exp (Expiration)**: Extremely short TTL (typically 60 seconds) to prevent
  replay attacks and minimize the window of misuse.
- **jti (JWT ID)**: A unique identifier for the ticket to allow for optional
  single-use enforcement.

#### Cryptographic Verification (JWKS)

The Colony validates these tickets without needing to communicate back to
Discovery in real-time. It fetches Discovery's public keys via a **JWKS (JSON
Web Key Set)** endpoint (managed at `/.well-known/jwks.json`). This allows for:

1. **Stateless Scale**: The Colony caches the public keys, allowing it to
   validate thousands of enrollment attempts locally.
2. **Key Rotation**: Discovery can rotate its signing keys daily; the Colony
   automatically picks up the new keys from the JWKS as they are published.

### C. Agent ↔ Colony

- **Zero-Trust Validation**: The Agent **does not** trust the endpoint info from
  Discovery blindly. It uses the **Root CA Fingerprint** (
  see [PKI Infrastructure](12_pki_infrastructure_and_trust_model.md)) to verify
  the Colony's identity during the TLS handshake.
- **Enrollment**: The Agent presents the Discovery-issued Referral Ticket to the
  Colony as part of the `RequestCertificate` RPC request. The Colony performs
  the local EdDSA signature verification and ensures the `agent_id` in the
  ticket matches the identity requested in the CSR.

## 3. PSK-Encrypted Bootstrap Rendezvous

Discovery is ordinarily a directory: an Agent retrieves Colony endpoints and
dials the Colony directly. RFD 108 adds a narrow fallback for when that direct
HTTPS path is unavailable because the Colony is behind NAT. It does not turn
Discovery into a certificate relay.

1. After direct dialing fails, an Agent with an explicitly configured public
   bootstrap endpoint opens a short-lived TLS listener and publishes an opaque
   rendezvous record under the `mesh_id`.
2. The record contains the Agent endpoint, a session nonce, a write capability,
   and an expiry. `internal/rendezvous` encrypts and authenticates it with an
   AES-GCM key derived from the Bootstrap PSK using HKDF-SHA256, salted by
   `mesh_id` and domain-separated with `coral-bootstrap-rendezvous-v1`.
3. `internal/colony/rendezvous.Dialer` long-polls
   `PollBootstrapRendezvous`, attempts to decrypt each record with the active
   Bootstrap PSK (and grace PSK during rotation), and dials only a valid
   endpoint.
4. The Colony is still the TLS server over that outbound TCP connection. The
   Agent performs the normal fingerprint and SPIFFE-SAN checks, then uses the
   existing `RequestCertificate` RPC with its CSR, referral ticket, and PSK.
5. Once the connection-scoped nonce matches and enrollment succeeds, the
   Colony acknowledges the record. This removes it immediately rather than
   waiting for its TTL.

Discovery exposes `PublishBootstrapRendezvous`, `PollBootstrapRendezvous`, and
`AckBootstrapRendezvous` for this lifecycle. It stores ciphertext and a
SHA-256 hash of the per-record write token, never the token itself at rest.
The raw write token transits Discovery only so it can authorize a republish or
acknowledgement; it is neither a Bootstrap PSK nor a certificate-issuance
credential.

### Record Lifecycle and Bounded Work

Rendezvous records have a 90-second default TTL. An Agent republishes the same
record every 30 seconds while it waits, retaining a stable server-assigned
`record_id` and write token; each encryption uses a fresh GCM nonce. The Agent
waits up to 120 seconds for the dial-back.

The Colony polls for up to 25 seconds per request in a loop independent of
`RegisterColony`. Failed records are retried with per-`record_id` exponential
backoff (2 seconds, 4 seconds, 8 seconds, capped at 15 seconds). This matters
because polling returns an existing unacknowledged record immediately: without
backoff, a bad endpoint would cause a tight poll-and-dial loop.

The Agent listener continues accepting connections with bounded concurrent TLS
handshakes and a short handshake deadline. A scanner or an optional Discovery
reachability probe therefore cannot occupy the one connection the Colony needs
to complete enrollment.

### Discovery's Deliberate Boundary

Discovery cannot decrypt or forge a valid rendezvous payload without the
Bootstrap PSK. It also never sees the CSR, referral ticket, or certificate.
It may see the plaintext endpoint only when the publisher asks it to perform
the optional `verify_reachability` diagnostic. Probing is disabled by default:
an unauthenticated endpoint probe would otherwise be a public port-scanning
primitive. When enabled, it is separately quota- and concurrency-limited, and
failure is an actionable configuration signal rather than an enrollment
authorization decision.

During `coral agent start`, the fallback derives a dial-back endpoint from the
Agent's Discovery-confirmed STUN IP and bootstrap listen port. Operators use
`CORAL_BOOTSTRAP_PUBLIC_ENDPOINT` when TCP is exposed at a different address
or port. This inference does not perform TCP NAT discovery and does not solve a
topology in which both peers lack an inbound path.

## 4. Discovery Service Characteristics

To maintain high performance and minimize security surface area, the Discovery
service is designed to be **thin and ephemeral**, leveraging Cloudflare's global
edge:

- **Edge-Native Performance**: Hosted as **Cloudflare Workers**, the service
  executes in hundreds of locations globally, placing the "Meeting Point" within
  milliseconds of every Agent.
- **Distributed Persistence**: Current registrations are stored in **Cloudflare
  KV** or **D1**, providing sub-millisecond lookups with global replication.
- **TTL (Time-to-Live)**: Registrations expire automatically if heartbeats
  stop (default 300s). This ensures the registry doesn't point to "dead"
  endpoints.
- **Stateless Verification**: Discovery does not need to store per-agent state.
  It issues Referral Tickets based on real-time policy evaluation (e.g.,
  checking if the Agent's source IP or ID matches an allowlist).

## 5. Discovery Bypassing (Operator Overrides)

While Discovery is essential for automated scaling, it is designed to be
**non-blocking** in disaster recovery or air-gapped scenarios:

### Emergency Tokens

Operators can generate "Emergency Bootstrap Tokens" directly on the Colony.
These tokens bypass the Discovery requirement, allowing an Agent to enroll even
if the Discovery service is unreachable.

### Manual Configuration

Advanced users can manually configure the Agent with a static Colony endpoint.
In this mode, the Agent skips the "Lookup" phase and proceeds directly to the
Fingerprint Handshake, relying on the hardcoded IP/DNS provided by the operator.

## 6. Security Analysis

| Potential Attack            | Coral Defense                                                                                                                                                                                                                          |
|-----------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Discovery Hijacking**     | If the Discovery service is compromised, an attacker could point Agents to a malicious Colony. However, the **Agent will abort** the connection if the malicious Colony cannot provide a certificate matching the Root CA Fingerprint. |
| **Referral Ticket Forgery** | Tickets are signed using Ed25519 keys rotated daily. Even a leaked key only grants a window to _attempt_ enrollment; it does not grant access to the data mesh.                                                                        |
| **Denial of Service**       | Discovery implements aggressive rate-limiting on ticket issuance and lookups to prevent it from being used as an amplification vector against Colonies.                                                                                |
| **Endpoint Spoofing**       | Colonies sign their endpoint updates, ensuring that an attacker cannot redirect legitimate traffic away from a valid Colony.                                                                                                           |
| **Forged rendezvous record** | AEAD validation with a PSK-derived key makes unauthenticated ciphertext unusable; failed decryptions do not reach certificate issuance. |
| **Rendezvous record takeover** | `record_id` is public identity, while an independently random write token authorizes republish and acknowledgement; Discovery stores only its hash. |
| **Probe abuse** | Reachability probing is opt-in and separately rate-, target-, and concurrency-limited. |

## Future Engineering Notes

- **Geographic Proximity Routing**: Enhance the `Lookup` RPC to return Colony
  endpoints sorted by geographic proximity to the Agent, minimizing cross-region
  latency in global mesh deployments.
- **Advanced Policy DSL (RFD 086)**: Move beyond simple allowlists to a
  structured policy DSL, allowing operators to define complex enrollment rules
  (e.g., "Only allow agents with metadata `env=prod` to enroll on Tuesdays if
  they originate from VPC-X").
- **Sequence-Based Checkpoints for Registry**: Implement a polling checkpoint
  mechanism for Discovery, allowing Colonies to "see" if their current
  registration is out of sync without performing a full write heartbeat.
- **NAT Discovery Beyond Rendezvous**: RFD 108 intentionally requires a
  configured public TCP endpoint. Automatic mapping discovery, NAT-PMP/UPnP,
  and a solution for two non-dialable peers require a distinct security and
  connectivity design.

## Related Design Documents (RFDs)

- **[RFD 001](../../RFDs/001-discovery-service.md)**: Discovery Service (Prototype).
- **[RFD 049](../../RFDs/049-discovery-authorization.md)**: Discovery-Based Agent Authorization.
- **[RFD 086](../../RFDs/086-discovery-policy-enforcement.md)**: Advanced Discovery Policy Enforcement.
- **[RFD 088](../../RFDs/088-bootstrap-psk.md)**: Bootstrap PSK authorization and rotation.
- **[RFD 108](../../RFDs/108-psk-rendezvous-agent-bootstrap.md)**: PSK-encrypted rendezvous for NAT-traversing Agent bootstrap.
