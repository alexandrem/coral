# Discovery and Enrollment

Mesh Discovery supplies the coordinates an agent needs to contact its Colony
and issues referral tickets used during enrollment. After peers establish their
WireGuard path, telemetry and debugging traffic flows between the agent, Colony,
and CLI; Discovery does not relay that data. This mesh-level directory is
separate from [process discovery](service-discovery.md) inside an agent.

Discovery's server lives outside this repository. The behavior described here
is grounded in Coral's [Discovery client](../../internal/discovery/) and its
bootstrap and enrollment consumers. Server deployment details and operational
policies require verification in that service's own implementation.

## Lookup and authorization

The Colony registers its public key and endpoints with Discovery. An agent
looks up its Colony and uses the returned coordinates to attempt bootstrap.
The agent verifies the Colony's configured Root CA fingerprint and SPIFFE
identity before submitting enrollment credentials. A directory lookup therefore
provides reachability information, not a substitute trust anchor.

A referral ticket binds an agent identity, Colony identity, intent, expiry, and
ticket identifier. The Colony validates the signature through Discovery's JWKS
and checks the request's identity and CSR. Non-renewal enrollment also validates
the Bootstrap PSK. The ticket, PSK, and certificate checks have different roles;
knowing an endpoint or decrypting a rendezvous record is insufficient to obtain
an agent identity.

Implementation: [bootstrap client](../../internal/agent/bootstrap/client.go),
[JWKS client](../../internal/colony/jwks/), and
[enrollment authorization](../../internal/colony/enrollment/enroller.go).
[Identity and trust](identity-and-trust.md) explains the certificate model.

## Reverse-dial rendezvous

Discovery is ordinarily a directory: an Agent retrieves Colony endpoints and
dials the Colony directly. RFD 108 adds a narrow fallback for when that direct
HTTPS path is unavailable because the Colony is behind NAT. It does not turn
Discovery into a certificate relay.

1. After direct dialing fails, the Agent opens a short-lived TLS listener and
   publishes an opaque rendezvous record under the `mesh_id`. During normal
   `coral agent start`, the public endpoint is derived from the
   Discovery-confirmed STUN IP and bootstrap listen port; explicit endpoint
   configuration remains an override.
2. The record contains the Agent endpoint, a session nonce, a write capability,
   and an expiry. `internal/rendezvous` encrypts and authenticates it with an
   AES-GCM key derived from the Bootstrap PSK using HKDF-SHA256, salted by
   `mesh_id` and domain-separated with `coral-bootstrap-rendezvous-v1`.
3. `internal/colony/rendezvous.Dialer` long-polls
   `PollBootstrapRendezvous`, attempts to decrypt each record with the active
   Bootstrap PSK (and grace PSK during rotation), and dials only a valid
   endpoint.
4. The Colony is still the TLS server over that outbound TCP connection. The
   Agent performs the normal fingerprint and SPIFFE-SAN checks. A standalone
   certificate bootstrap uses `RequestCertificate`; startup with registration
   data uses `BootstrapAndRegister` to carry the CSR, referral ticket, PSK, and
   mesh registration in one resumable operation.
5. Once the connection-scoped nonce matches and enrollment succeeds, the
   Colony acknowledges the record. This removes it immediately rather than
   waiting for its TTL.

The Discovery client exposes `PublishBootstrapRendezvous`,
`PollBootstrapRendezvous`, and `AckBootstrapRendezvous` for this lifecycle. The
per-record write token authorizes republishing and acknowledgement; it is
neither a Bootstrap PSK nor a certificate-issuance credential. Discovery owns
record storage and lifecycle enforcement; the agent and Colony retain
responsibility for payload encryption and enrollment authorization.

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

### Colony Logging and Correlation

Every Colony-side reverse-dial log has a structured `event` field. Events that
belong to a particular rendezvous attempt also carry `record_id`. The record ID
is safe to log and is the primary correlation key across Discovery polling,
TCP/TLS setup, RPC routing, compound enrollment, and acknowledgement.

Logging is intentionally staged around authorization:

| Stage | Correlation fields | Representative events |
| --- | --- | --- |
| Startup | `colony_id` or `mesh_id` | `rendezvous_dialer_started`, `rendezvous_poller_started`, `rendezvous_enrollment_ready` |
| Discovery and dial | `record_id`, `attempt` | `rendezvous_records_received`, `rendezvous_dial_started`, `rendezvous_tcp_connected`, `rendezvous_dial_failed` |
| TLS and routing | `record_id`, `procedure` | `rendezvous_tls_established`, `rendezvous_nonce_mismatch`, `rendezvous_request_received`, `rendezvous_request_completed` |
| Authorized enrollment | `record_id`, `agent_id`, `phase`, optionally `mesh_ip` | `rendezvous_enrollment_started`, `rendezvous_enrollment_phase_changed`, `rendezvous_endpoint_selected`, `rendezvous_peer_added` |
| Certificate and completion | `record_id`, `agent_id`, `mesh_ip` | `rendezvous_certificate_issued`, `rendezvous_enrollment_completed`, `rendezvous_bootstrap_register_completed` |
| Record retirement | `record_id` | `rendezvous_record_acknowledged`, `rendezvous_ack_failed`, `rendezvous_ack_rejected` |

The durable enrollment phases are `claimed`, `authorized`, `ip_allocated`,
`old_peer_removed`, `new_peer_added`, `registry_updated`, and `completed`.
`rendezvous_enrollment_failed` reports the last phase and one of these failure
classes:

| Last phase | Failure class |
| --- | --- |
| `claimed` | `authorization` |
| `authorized` | `endpoint_or_ip_allocation` |
| `ip_allocated` | `old_peer_removal` |
| `old_peer_removed` | `new_peer_addition` |
| `new_peer_added` | `registry_update` |
| `registry_updated` | `certificate_issuance` |
| `completed` | `response_replay` |

Normal empty long-poll responses and records skipped during their backoff window
are not logged, preventing an idle Colony from producing continuous noise.
Successful lifecycle milestones use `INFO`, actionable failures and rejections
use `WARN` or `ERROR`, and lower-level decrypt/session/lease details use
`DEBUG`.

The decrypted rendezvous TCP endpoint is not emitted, including inside dial
errors. Nonces, write tokens, Bootstrap PSKs, referral-ticket contents, CSRs,
certificate bodies, and private keys are also excluded. After authorization,
`rendezvous_endpoint_selected` may include the Discovery-selected Agent
WireGuard UDP endpoint because it is the actual mesh peer configuration.

### Discovery's Deliberate Boundary

Discovery cannot decrypt or forge a valid rendezvous payload without the
Bootstrap PSK. The rendezvous RPCs do not carry the enrollment CSR, issued
certificate, or Bootstrap PSK. The referral ticket is issued by Discovery but
is submitted to the Colony over the authenticated TLS connection, separately
from the opaque rendezvous record.

The publisher can also request the optional `verify_reachability` diagnostic,
which discloses the plaintext endpoint to Discovery for a probe. Coral leaves
that option disabled by default. A probe result describes TCP reachability,
not authorization to enroll; server-side probe limits are owned by Discovery.

During `coral agent start`, the fallback derives a dial-back endpoint from the
Agent's Discovery-confirmed STUN IP and bootstrap listen port. Operators use
`CORAL_BOOTSTRAP_PUBLIC_ENDPOINT` when TCP is exposed at a different address
or port. This inference does not perform TCP NAT discovery and does not solve a
topology in which both peers lack an inbound path.

## Durable compound enrollment

On the rendezvous startup path, `BootstrapAndRegister` obtains a certificate and
mesh assignment together. These effects span database state, certificate issuance,
and the running WireGuard device, so the operation is a resumable state machine
rather than one transaction covering every side effect.

The Colony's enrollment `Store` owns a row keyed by rendezvous `record_id`.
A caller claims a lease, validates authorization, allocates an IP, replaces an
old WireGuard peer if needed, adds the new peer, updates the registry, and
persists the completed response. Its phases are `claimed`, `authorized`,
`ip_allocated`, `old_peer_removed`, `new_peer_added`, `registry_updated`, and
`completed`. A request hash binds retries to the same enrollment input.

Concurrent delivery does not grant two callers ownership of an attempt. A
caller observing a live lease waits; an expired lease can be taken over only
from an authorized phase. A row that never reached authorization is discarded
rather than resumed as trusted state. Completed attempts replay their stored
certificate and registration response instead of issuing another identity.

Runtime WireGuard peers still disappear when the Colony restarts. `RestorePeers`
rebuilds them from completed enrollments and current Discovery coordinates,
checking that an enrollment's key is still the agent's authoritative key before
restoring it. An old completed attempt must not reinstall a superseded peer.

On the agent, enrollment state is persisted with the WireGuard identity and
certificate hash. Startup reuses credentials only when that committed local
checkpoint matches. This prevents a certificate written before a crash from
being mistaken for a fully usable mesh assignment.

Implementation: [enroller](../../internal/colony/enrollment/enroller.go),
[durable store](../../internal/colony/enrollment/store.go),
[enrollment tests](../../internal/colony/enrollment/enroller_test.go), and
[agent checkpoint store](../../internal/agent/enrollmentstate/store.go).

Transport and payload implementation:
[agent rendezvous](../../internal/agent/bootstrap/rendezvous.go),
[Colony dialer](../../internal/colony/rendezvous/dialer.go), and
[payload encryption](../../internal/rendezvous/rendezvous.go).

## Reachability limits

The reverse-dial fallback requires a TCP path from the Colony to the agent's
inferred or configured public endpoint. STUN's UDP observation cannot prove
that TCP path exists. Two peers without an inbound path still need a different
connectivity mechanism. Explicit endpoint configuration changes routing; it does
not bypass fingerprint, ticket, or PSK validation.

For deployment diagnosis, use [Bootstrap troubleshooting](../BOOTSTRAP_TROUBLESHOOTING.md)
and [WireGuard troubleshooting](../WIREGUARD_TROUBLESHOOTING.md).

## Design history

- [RFD 001: Discovery Service](../../RFDs/001-discovery-service.md)
- [RFD 049: Discovery Authorization](../../RFDs/049-discovery-authorization.md)
- [RFD 088: Bootstrap PSK](../../RFDs/088-bootstrap-psk.md)
- [RFD 108: PSK Rendezvous](../../RFDs/108-psk-rendezvous-agent-bootstrap.md)
- [RFD 109: Compound Enrollment](../../RFDs/109-rendezvous-mesh-enrollment.md)
