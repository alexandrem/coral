---
rfd: "109"
title: "Rendezvous Mesh Enrollment for NAT-Local Colonies"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "023", "048", "049", "087", "088", "108" ]
database_migrations: [ "rendezvous_enrollment_state table (record_id-keyed, phases: claimed/authorized/ip_allocated/old_peer_removed/new_peer_added/registry_updated/completed) and jti-consumption tracking in the CA" ]
areas: [ "agent", "colony", "discovery", "wireguard", "security" ]
---

# RFD 109 - Rendezvous Mesh Enrollment for NAT-Local Colonies

**Status:** 🎉 Implemented

**Implementation Status:** 🎉 Implemented. Built:

- ✅ `BootstrapAndRegister` RPC and messages added to `ColonyService`
  (`proto/coral/colony/v1/colony.proto`), reusing `RequestCertificateRequest`/
  `Response` and `mesh.v1.RegisterRequest`/`RegisterResponse`. A
  `capabilities` field was added to `RegisterRequest` (`bootstrap_and_register`)
  per the Resolved Design Decisions.
- ✅ `jti` consumption tracking added to `ca.Manager`
  (`internal/colony/ca/manager.go`: `ConsumeReferralTicketJTI`,
  `IsReferralTicketConsumed`, backed by a new `consumed_referral_tickets`
  table), closing the gap between RFD 048's documented single-use invariant
  and the previously-stateless validation.
- ✅ Durable, `record_id`-keyed enrollment-state store
  (`internal/colony/enrollment`, new `rendezvous_enrollment_state` table):
  atomic-insert claiming, lease-based ownership with compare-and-swap steal,
  delete-and-restart for an expired `claimed` row vs. steal-and-resume for
  `authorized`-or-later, phase-tracked (`claimed` → `authorized` →
  `ip_allocated` → `old_peer_removed` → `new_peer_added` →
  `registry_updated` → `completed`).
- ✅ `enrollment.Enroller` implementing the full Enrollment Processing flow:
  ticket/PSK/identity validation, Discovery endpoint lookup with pubkey
  binding, phased peer replacement (remove-old/add-new/update the new
  `agent_wireguard_keys` registry), jti consumption + certificate issuance,
  completion + replay. `Server.BootstrapAndRegister`
  (`internal/colony/server/server.go`) is a thin adapter.
- ✅ Nonce validation and record-scoping remain in the RFD 108 dialer
  (`internal/colony/rendezvous/dialer.go`), extended to also route
  `/BootstrapAndRegister` and to inject a trusted
  `Coral-Rendezvous-Record-Id` header after its own nonce check succeeds.
  `BootstrapAndRegister` is walled off from the ordinary mesh/public
  listener via `blockBootstrapAndRegister` in `internal/cli/colony/server.go`
  ("No broad rendezvous handler").
- ✅ `wireguard.Device.RemovePeer` made idempotent (no-op on an absent peer)
  and a new `Device.TriggerHandshake` added, so peer replacement is
  restart-safe and the Colony-initiated handshake fires immediately instead
  of waiting for the next keepalive.
- ✅ Agent-side protocol support: `bootstrap.Client` calls
  `BootstrapAndRegister` instead of `RequestCertificate` whenever
  `Config.WireGuardPubkey` is set, advertising the `bootstrap_and_register`
  capability and returning the compound `RegisterResponse` on `Result`.
  Unset (the default), behavior is byte-for-byte the RFD 108 flow.
- ✅ Agent startup initializes WireGuard/STUN/Discovery and runtime detection
  before certificate bootstrap, supplies the full registration payload to
  `bootstrap.Client`, and consumes a compound `RegisterResponse` by installing
  a dynamic Colony peer with no endpoint. The ordinary pre-mesh
  `MeshService.Register` loop is skipped for rendezvous enrollment.
- ✅ Verified Discovery's existing agent-registration TTL (300s default)
  already comfortably covers the RFD 108 rendezvous wait budget (120s); no
  Discovery-side change was needed.

## Summary

RFD 108 lets a local, non-dialable Colony issue an Agent certificate by
reversing the TCP connection: the Colony dials a public Agent using a
PSK-encrypted Discovery rendezvous record. It does not, however, enroll that
Agent into the WireGuard mesh. After certificate issuance the Agent falls back
to the normal, Agent-initiated `MeshService.Register` request, which cannot
reach a Colony advertised only as `127.0.0.1` or a mesh address.

This RFD extends the RFD 108 rendezvous session to perform the initial mesh
enrollment atomically. The successful result includes both the certificate and
the existing registration response. The Colony configures the Agent's
WireGuard peer with the Agent's Discovery-observed public UDP endpoint; the
Agent configures the Colony peer without an endpoint and learns the Colony's
NAT mapping from the Colony-initiated WireGuard handshake.

This makes the following topology work without exposing the Colony to the
Internet:

```text
Local Colony behind NAT  ── outbound TCP + UDP ──>  public VPS Agent
```

## Problem

### The incomplete RFD 108 flow

RFD 108 completes this exchange successfully:

```text
Colony ── TCP/TLS dial-back ──> Agent
Agent  ── RequestCertificate ──> Colony
```

The certificate is saved, but agent startup then runs the ordinary mesh
registration flow:

```text
Agent -> http://127.0.0.1:9000     # Agent's own loopback; cannot work
Agent -> http://100.64.0.1:9000    # Colony mesh address; peer not configured yet
```

The Colony only calls `WireGuard.AddPeer` after receiving
`MeshService.Register`. Therefore the WireGuard tunnel cannot exist until
after registration, while the agent cannot reach the Colony's mesh address
until after the tunnel exists.

### Why the Agent's Discovery endpoint is not sufficient today

An Agent may register its STUN-observed public WireGuard UDP endpoint with
Discovery before attempting mesh registration. This is necessary, but is not
an instruction to configure a peer:

```text
Discovery record: agent public key + public UDP endpoint
                    |
                    | only read by Colony's Register handler today
                    v
MeshService.Register -> allocate agent mesh IP -> add Colony-side peer
```

In the local-Colony topology no inbound registration request reaches that
handler. Discovery is a directory, not a control-plane relay and does not push
the Agent record into the Colony's WireGuard device.

The Agent also cannot use the public Agent endpoint as a Colony endpoint. It
needs the Colony peer's public key and allowed IPs, but does not need a
configured UDP endpoint if the Colony initiates the first handshake. WireGuard
updates a peer's roaming endpoint when it receives an authenticated packet.

## Goals

- Complete first-time certificate issuance and mesh registration when the
  Colony has no inbound TCP or UDP path.
- Preserve RFD 108's rule that Discovery never sees the PSK, CSR, certificate,
  registration payload, or a relayed control-plane request.
- Use the Agent's Discovery-observed UDP endpoint only after verifying its
  registered public key matches the enrolling Agent's WireGuard key — not just
  that the `agent_id` string matches, since Discovery's `RegisterAgent` is
  unsigned and a record can be overwritten by an unrelated caller.
- Make mesh-IP allocation and peer creation idempotent (including replacing,
  not duplicating, an existing peer on key rotation), and defer certificate
  issuance (which consumes the single-use referral ticket) until after those
  steps succeed. Back retries with durable, `record_id`-keyed enrollment
  state rather than assuming ticket single-use alone makes retries safe —
  the current CA validates tickets statelessly, so this RFD cannot rely on
  that guarantee existing on its own.
- Keep normal direct bootstrap and normal post-enrollment registration
  unchanged.

## Non-goals

- Automatic TCP NAT port mapping. For the common public-Agent case, startup
  derives the rendezvous listener as `STUN-observed-IP:8444`; operators still
  configure `CORAL_BOOTSTRAP_PUBLIC_ENDPOINT` when TCP forwarding changes the
  external address or port.
- Solving the case where neither side is dialable and neither a relay nor a
  public endpoint is available.
- Turning Discovery into a TCP, HTTP, or WireGuard relay.
- Changing certificate renewal, which continues to use mTLS and normal mesh
  connectivity.

## Proposed Design

### A single enrollment RPC over the existing rendezvous connection

Add a `BootstrapAndRegister` method to `ColonyService`. It is permitted only
on an RFD 108 Colony-initiated rendezvous connection, and is sent by the Agent
after it validates the Colony TLS certificate and colony-ID SAN.

The request carries the existing certificate-enrollment inputs and the
registration inputs needed by `MeshService.Register`:

```text
BootstrapAndRegisterRequest
  bootstrap: RequestCertificateRequest
    - JWT referral ticket
    - CSR
    - Bootstrap PSK
  registration: RegisterRequest
    - agent ID / colony ID
    - WireGuard public key
    - services, runtime context, capabilities
  Coral-Rendezvous-Nonce HTTP header

BootstrapAndRegisterResponse
  certificate: RequestCertificateResponse
  registration: RegisterResponse
```

This is a compound, server-side transaction rather than two sequential RPCs.
It avoids issuing a certificate and then losing the only usable control-plane
connection before peer setup, and gives the Agent exactly the data it needs to
configure the mesh.

`RequestCertificate` and `MeshService.Register` remain supported for the
direct-dial path. `BootstrapAndRegister` is not exposed on the ordinary public
listener unless a later RFD explicitly enables it.

### Enrollment processing

**Durable, `record_id`-keyed enrollment state is the actual idempotency
mechanism — not ticket single-use.** RFD 048 documents referral tickets as
single-use, tracked by `jti`, but today's `Manager.ValidateReferralTicket`
(`internal/colony/ca/manager.go`) is explicitly stateless — there is no `jti`
consumption anywhere in the current CA code. This RFD cannot assume that
protection exists. It also cannot rely on "the response was written" as proof
the Agent received it: a write can succeed on the Colony's side and still be
lost in transit. Both problems are solved the same way: before touching the
CA or the WireGuard device, the handler durably records enrollment state keyed
by the rendezvous `record_id`, with explicit concurrency and restart
semantics (see the following subsection). The nonce check is always the first
thing that happens on every call, including a replay — a `record_id` alone is
not sufficient authorization to receive a previously-issued certificate:

1. Validate `Coral-Rendezvous-Nonce` against the nonce decrypted from the
   selected RFD 108 record, using constant-time comparison. This runs before
   any state lookup, ticket validation, or replay — an attacker who somehow
   obtains a valid `record_id` but not the nonce (which requires the PSK to
   decrypt) gets nothing, including a replayed response.
2. Atomically look up or claim enrollment state for this `record_id` (see
   next subsection for the exact claim/lease/resume rules). The row's `phase`
   determines what happens next; only `authorized` and later phases represent
   a cryptographically validated attempt, so only those may be resumed
   mid-flow. Specifically:
   - A `completed` row exists: verify its recorded identity is consistent
     with this request's ticket/CSR/`RegisterRequest` (a cheap equality
     check, not re-validation), then replay its stored certificate and
     `RegisterResponse` verbatim and go straight to step 11. No ticket/PSK
     re-validation, no second CA call.
   - A row exists with a live lease held by another in-flight call, in any
     phase: wait (bounded) for it to reach `completed` (replay) or for the
     lease to expire.
   - A row exists in phase `claimed` (i.e., inserted but never reached
     `authorized`) with an expired lease — its previous owner crashed before
     completing ticket/PSK validation: **delete it** and atomically insert a
     fresh `claimed` row owned by this call. This is not a resume; it is a
     restart from validation, because nothing about the deleted row was ever
     authorized.
   - A row exists in phase `authorized` or later with an expired lease — its
     previous owner crashed after validation succeeded: steal the lease
     (atomically claim ownership) and resume from that exact persisted
     `phase`, never from an earlier or later point.
   - No row exists: atomically insert a new row in phase `claimed`, owned by
     this call.
3. *(Only reached as the owner of a `claimed`-phase row — i.e., a fresh
   attempt or a claimed-and-restarted one, never a resumed one.)*
   Cryptographically validate the referral ticket (signature and expiry,
   existing RFD 049/088 code) and the Bootstrap PSK, without yet consuming
   the ticket's `jti`. As part of this RFD (not assumed to pre-exist), extend
   `Manager.ValidateReferralTicket`/`IssueCertificate` to track consumed
   `jti`s, matching RFD 048's documented invariant, which today's code does
   not enforce. A failure here leaves the row in `claimed` phase; it is
   deleted (not advanced) on the next attempt, per step 2.
4. Only once step 3 succeeds — i.e., the ticket's claims are now trustworthy —
   validate identity consistency: the referral ticket's claimed Agent ID and
   Colony ID, the CSR subject's Agent ID and Colony ID (SPIFFE ID, RFD 047),
   and `RegisterRequest`'s Agent ID and Colony ID must all agree with each
   other and with the rendezvous record's Colony ID. (`RequestCertificateRequest`
   itself carries no bare Agent ID/Colony ID fields — the ticket and CSR are
   where that identity actually lives; this replaces the previous, imprecise
   "both nested requests name the Colony ID/Agent ID" description.) A
   mismatch is also a `claimed`-phase failure — deleted, not advanced.
5. Transition the row to phase `authorized` (durable write). This is the
   actual authorization checkpoint referenced in step 2: a lease steal is
   only ever permitted from `authorized` or a later phase, and always resumes
   from that exact phase, never implicitly jumping ahead to peer setup or
   issuance from an unauthorized row.
6. Resolve the Agent's observed WireGuard UDP endpoint from Discovery by
   `(agent_id, colony_id)`. Require `LookupAgent.pubkey` to equal the
   `wireguard_pubkey` in this request's `RegisterRequest` before the endpoint
   may be used; reject a mismatch instead of configuring a peer with a
   poisoned or stale endpoint (see Security Model — Discovery's `RegisterAgent`
   is unsigned, and an unrelated caller can overwrite an Agent's record).
   Select the source-matching or first non-loopback endpoint among the
   pubkey-matching entries using the current endpoint-selection policy. Fail
   here, before any allocation or issuance, if no usable, matching endpoint
   exists.
7. Allocate (or recover) the Agent's stable mesh IP, look up the WireGuard
   public key currently on record for this Agent ID (if any), and write these
   into the row — resolved endpoint, allocated IP, old public key (if any),
   new public key — transitioning to phase `ip_allocated`, *before* mutating
   the WireGuard device. This durable pre-image is what makes step 8's peer
   replacement restart-safe (see next subsection).
8. Apply the peer mutation via the phased, restartable procedure in the next
   subsection: remove the old peer if a different key was on record
   (`old_peer_removed`), add/update the peer under the new key with the
   allocated IP (`new_peer_added`), then update the registry's
   Agent-ID-to-current-key mapping (`registry_updated`). Construct the
   existing `RegisterResponse`.
9. Consume the referral ticket's `jti` and issue the agent certificate using
   the existing CA code. This is the last step that can fail as a fresh
   operation: everything from `authorized` onward is retriable/resumable
   against the same row, and nothing after this step can fail in a way that
   invalidates the certificate.
10. Mark the enrollment-state row `completed`, storing the issued certificate
    and the constructed `RegisterResponse`, then return both in one response.
11. Ack the rendezvous record only after the complete response was
    successfully written. Failure leaves the record available for retry until
    its TTL; the retry re-enters at step 1 (nonce check) and reaches step 2's
    `completed` branch.

### Enrollment state: concurrency and restart semantics

The enrollment-state row is not just a cache — it is the source of truth for
"has this bootstrap attempt already progressed, and how far," and must
tolerate both concurrent deliveries of the same `record_id` (the Colony's
RFD 108 dial loop can, under retry/backoff races, end up with two in-flight
connections attempting the same record) and a Colony process restart
mid-enrollment.

- **The full phase sequence is:** `claimed` -> `authorized` -> `ip_allocated`
  -> `old_peer_removed` (conditional) -> `new_peer_added` -> `registry_updated`
  -> `completed`. `claimed` is set by the atomic insert, before any
  cryptographic validation. `authorized` is set only after the referral
  ticket and PSK are cryptographically validated (signature, expiry) *and*
  identity consistency across ticket/CSR/`RegisterRequest` passes — this is
  the line between "someone claimed this `record_id`" and "this is a
  validated enrollment attempt," and it is the only line that matters for
  what a lease steal is allowed to do.
- **Claiming is an atomic insert, not read-then-write.** The row is created
  with `INSERT ... record_id UNIQUE`, not a read-check-then-insert sequence.
  Exactly one concurrent caller wins the insert for a given `record_id`; every
  other caller's insert fails on the uniqueness constraint and falls into the
  wait/delete-and-restart/steal branches in step 2, rather than each
  proceeding independently through ticket validation and issuance.
- **Ownership is a lease, not a permanent claim — but a lease steal's
  behavior depends entirely on which side of `authorized` the row is on.**
  The row carries `owner_id` (unique to the handling goroutine/request) and
  `lease_expires_at`, refreshed periodically while the owner is actively
  processing. A waiting caller polls (bounded, short interval) until the row
  reaches `completed` (replay) or the lease expires without completion:
  - If the expired-lease row is still in phase `claimed`, its owner crashed
    *before* validation ever succeeded. The row is **deleted**, not resumed —
    an unauthorized row carries no guarantee that its ticket/PSK/identity
    checks would have passed, so treating it as resumable would let a caller
    skip validation entirely (this was the exact defect in an earlier
    revision of this design: an unvalidated row must never be advanced
    straight to peer setup or issuance). The next caller inserts a fresh
    `claimed` row and validates from scratch, using its own request's
    ticket/CSR/PSK.
  - If the expired-lease row is in phase `authorized` or later, its owner
    crashed *after* validation succeeded. Only then is a lease steal
    (atomic compare-and-swap of `owner_id`/`lease_expires_at`) permitted, and
    it always resumes from that exact persisted `phase` — never from
    `claimed`-phase validation again (already done and durably recorded) and
    never implicitly skipping ahead past what the phase says is complete.
- **The row records enough state to resume mid-peer-mutation, not just
  mid-request.** Beyond agent/colony ID, ticket `jti`, and CSR hash, the row
  stores: `resolved_endpoint`, `allocated_ip`, `old_pubkey` (nullable), and
  `new_pubkey`, populated when the row enters `ip_allocated`. Each of the
  three peer-mutation sub-phases is individually idempotent against the
  *values recorded in the row* (not re-derived live state):
  `RemovePeer(old_pubkey)` is a no-op if the peer is already absent
  (`internal/wireguard/device.go` `RemovePeer`), `AddPeer` is already an
  upsert keyed by public key (`device.go` `AddPeer`,
  `d.peers[peerConfig.PublicKey] = peerConfig`), and the registry update is
  keyed by `record_id`. This is necessary because the WireGuard device only
  exposes separate `AddPeer`/`RemovePeer` calls — there is no transactional
  "replace" primitive to lean on, so the phase sequence and its persisted
  pre-image are what make "remove old, add new, update mapping" reconcilable
  after a crash at any point in that sequence, using the row's recorded
  `old_pubkey`/`new_pubkey`/`allocated_ip` rather than whatever the live
  request happens to carry (which may differ on a retry with a rotated key —
  see Rollout for why that case is out of scope for a single `record_id`'s
  retry and instead becomes a fresh enrollment attempt).
- **Remove-before-add, not add-before-remove.** The old peer is removed
  before the new one is added, even though this creates a brief window with
  no peer configured for that Agent ID. The alternative order risks two
  peers simultaneously claiming the same allowed IP, which is the exact
  duplicate-routing hazard this design exists to avoid; a short connectivity
  gap during an already-rare key-rotation event is the safer failure mode.

### WireGuard endpoint establishment

After a successful response, the Agent configures its WireGuard interface with
the assigned mesh IP, Colony public key, and the Colony mesh IPs as allowed
IPs. In this special initial-enrollment mode it deliberately leaves the
Colony peer's `Endpoint` empty.

The Colony has the opposite information: it configures the Agent peer with
the Discovery-observed public UDP endpoint and a persistent keepalive. It
therefore initiates the first authenticated WireGuard handshake. This relies
on the same assumption the rest of the mesh data plane already makes for
NAT'd peers (RFD 023): the Colony's NAT device permits return traffic on the
mapping its own outbound handshake packet creates (true for full-cone and
restricted-cone NATs, the common case for home/office routers and cloud NAT
gateways). The persistent keepalive is configured on the Colony side
specifically because the Colony is the NAT'd party that needs the mapping
kept alive; the Agent, being publicly reachable, needs no keepalive of its
own. A symmetric NAT on the Colony's side would break this, exactly as it
would break any other NAT'd peer's mesh connectivity today — this RFD
introduces no new exposure to that limitation.

```text
1. Colony -> Agent public UDP endpoint: WireGuard initiation
2. Agent verifies Colony public key and replies
3. Agent records the source UDP mapping as the Colony peer endpoint (roaming)
4. Mesh TCP/Connect traffic proceeds over 100.64.0.0/10
```

An empty peer endpoint is already valid in Coral's `wireguard.PeerConfig` for
dynamic peers. The Agent-side configuration path must not reject it or try the
unreachable Discovery endpoint during rendezvous enrollment.

If Discovery has no usable Agent UDP endpoint, enrollment fails with an
actionable error and the rendezvous record remains unacknowledged. Operators
must configure STUN or otherwise make the Agent's WireGuard UDP port publicly
reachable. The RFD 108 TCP bootstrap endpoint (`:8444` by default) is not
assumed to be the WireGuard UDP endpoint.

## Protocol Flow

```mermaid
sequenceDiagram
    autonumber
    participant A as Public Agent
    participant D as Discovery
    participant C as Local Colony

    A->>D: RegisterAgent(agent WG key, observed UDP endpoint)
    A->>D: PublishBootstrapRendezvous(PSK-encrypted TCP endpoint)
    C->>D: PollBootstrapRendezvous
    C->>A: TCP dial to configured bootstrap endpoint
    C->>A: TLS server certificate
    A->>A: Validate fingerprint and Colony-ID SAN
    A->>C: BootstrapAndRegister(CSR, PSK, ticket, WG key) + rendezvous nonce
    C->>C: Validate nonce first (before any state lookup or replay)
    C->>C: Atomically claim/find enrollment state for record_id (completed -> replay and skip ahead)
    C->>C: [claimed-phase only] Validate ticket, PSK (jti not consumed yet)
    C->>C: [claimed-phase only] Validate identity: ticket claims, CSR subject, RegisterRequest all agree
    C->>C: Mark state authorized (only now is a lease steal/resume permitted)
    C->>D: LookupAgent -> verify pubkey matches RegisterRequest before using endpoint
    C->>C: Record allocated IP + old/new pubkey; phase = ip_allocated
    C->>C: Remove old peer (if rotated) -> add new peer -> update registry (phased, restartable)
    C->>C: Consume ticket jti; issue cert (last, irreversible step)
    C->>C: Mark enrollment state completed (cert + RegisterResponse)
    C-->>A: certificate + assigned IP + mesh subnet + peers
    C->>D: AckBootstrapRendezvous
    A->>A: Save certificate; configure Colony WG peer with no endpoint
    C->>A: UDP WireGuard initiation / persistent keepalive
    A-->>C: UDP WireGuard response; learn roaming endpoint
    A->>C: MeshService.Heartbeat over mesh
```

## Security Model

- **Discovery stays opaque.** It stores RFD 108 ciphertext and the Agent's
  normal public WireGuard endpoint only. It does not receive enrollment RPC
  payloads, PSKs, referral tickets, CSRs, certificates, or mesh responses.
- **The PSK remains authorization material.** It is validated solely by the
  Colony, exactly as in RFD 088; it is never used as a WireGuard key.
- **The rendezvous nonce binds the RPC to the chosen record, and gates replay
  too.** It is validated before PSK, ticket, certificate, registration
  processing, *or* enrollment-state lookup (Enrollment Processing step 1, the
  very first thing checked). This matters specifically because of the
  `completed`-state replay path: a `record_id` becomes visible to any caller
  who can poll `PollBootstrapRendezvous` for the `mesh_id` (RFD 108, `record_id`
  is public), so replay must not be authorized by `record_id` possession
  alone. Requiring the nonce first means only a party that could decrypt the
  original rendezvous record (i.e., holds the PSK) can trigger a replay, the
  same trust the nonce already provides for the original exchange.
- **TLS identity is unchanged.** The Agent still verifies the Colony's Root
  CA fingerprint and colony-ID SAN, even though the Colony opened TCP.
- **UDP endpoint authenticity comes from WireGuard, but endpoint *selection*
  must be bound to the enrolling key.** Discovery's `RegisterAgent` is
  documented as unsigned (`docs/BOOTSTRAP_SECURITY.md`, "No Request Signing
  on Discovery") — any caller who knows `agent_id`/`mesh_id` can overwrite an
  Agent's endpoint record. A substituted endpoint still cannot let an
  attacker complete a WireGuard handshake as the Agent (no private key), so
  this isn't a MITM path, but it *is* a reliable enrollment-denial path if the
  Colony blindly uses whatever endpoint Discovery returns: the Colony would
  configure a peer and send its initial handshake to an address nobody is
  listening on, and the real Agent never receives it. The mitigation is
  Enrollment Processing step 6: require `LookupAgent.pubkey` to equal this
  request's `wireguard_pubkey` before the endpoint may be used, and reject a
  mismatch as a distinct, actionable failure rather than silently configuring
  a peer against a poisoned record.
- **Registration identity is bound.** The Agent ID in the referral ticket,
  certificate request, and registration request must match, and — per the
  point above — so must the WireGuard public key used to look up the
  Discovery endpoint. The Colony rejects any mismatch before modifying the
  allocator, registry, or WireGuard device.
- **Idempotency does not depend on referral-ticket replay protection.**
  RFD 048 documents referral tickets as single-use via `jti` tracking, but
  the current CA implementation validates tickets statelessly
  (`internal/colony/ca/manager.go`, `ValidateReferralTicket`). This RFD adds
  `jti` consumption as part of its own Colony component changes (Enrollment
  Processing step 3/9), but does not rely on it alone: the durable,
  `record_id`-keyed enrollment-state record — atomically claimed, lease-owned,
  and phase-tracked (Enrollment Processing step 2, and "Enrollment state:
  concurrency and restart semantics") — is the actual retry-safety mechanism,
  so a lost response or a concurrent duplicate delivery is handled by
  replaying stored state or waiting on the lease, rather than by both
  proceeding through ticket validation independently.
- **An unauthenticated `claimed` row is never treated as authorization.**
  Claiming a `record_id` (the atomic insert in Enrollment Processing step 2)
  only establishes exclusive ownership for concurrency control — it happens
  before ticket/PSK validation, so it must not be resumable or replayable on
  its own. A row that never reaches `authorized` (its owner crashed during
  validation) is deleted and restarted from validation on the next attempt,
  never advanced directly to peer setup or certificate issuance. Only a row
  that reached `authorized` — meaning ticket signature/expiry and identity
  consistency both already passed — may have its lease stolen and resumed
  mid-flow.
- **No broad rendezvous handler.** On an RFD 108 connection, only
  `RequestCertificate` (for unupgraded Agents, during rollout) and
  `BootstrapAndRegister` are allowed. All other paths return `404`.

## API and Component Changes

### Protobuf

Add `BootstrapAndRegister` and its request/response messages to
`coral.colony.v1.ColonyService`. Reuse `mesh.v1.RegisterRequest` and
`mesh.v1.RegisterResponse` as nested fields rather than duplicating service,
runtime, and capability schema.

### Agent

- When direct certificate validation fails and
  `CORAL_BOOTSTRAP_PUBLIC_ENDPOINT` is configured, use the compound RPC over
  the rendezvous connection instead of `RequestCertificate` alone.
- Persist the certificate and pass the returned registration result into mesh
  configuration.
- Add the Colony peer with an empty endpoint in rendezvous enrollment mode.
- Do not run the ordinary pre-mesh `MeshService.Register` retry loop after a
  successful compound enrollment; begin heartbeat/reconnection over the mesh.

### Colony

- Extend the RFD 108 restricted handler to also route `BootstrapAndRegister`,
  alongside the existing `RequestCertificate` route, so an unupgraded Agent's
  plain RFD 108 flow keeps working over the same rendezvous connection (see
  Rollout and Compatibility). All other paths continue to return `404`.
- Factor the existing certificate issuance and registration logic into
  transaction-capable internal operations; preserve the public handlers as
  thin adapters.
- Add a persistent, `record_id`-keyed enrollment-state store, phases
  `claimed` -> `authorized` -> `ip_allocated` -> `old_peer_removed`
  (conditional) -> `new_peer_added` -> `registry_updated` -> `completed` (per
  Enrollment Processing and "Enrollment state: concurrency and restart
  semantics"), so a lost-response retry, or a concurrent duplicate delivery
  of the same `record_id`, replays the stored certificate and
  `RegisterResponse` instead of two calls independently reissuing. Row
  creation is an atomic insert on a unique `record_id` constraint, not
  read-then-write; ownership is a refreshed lease. A row whose lease expires
  while still in `claimed` (validation never completed) is deleted, not
  resumed; only a row that reached `authorized` may have its lease stolen and
  resumed from that exact phase. This is new state, not a reuse of an
  existing table.
- Add `jti` consumption to `ca.Manager.ValidateReferralTicket`/
  `IssueCertificate` (`internal/colony/ca/manager.go`), which today validates
  referral tickets statelessly. This closes the gap between RFD 048's
  documented single-use-ticket invariant and the current code, and is
  required for the enrollment-state design above to have a well-defined
  "was this ticket already used" check during crash recovery.
- Require a usable Discovery Agent endpoint before adding the peer, and use
  the existing endpoint-selection policy — restricted to entries whose
  `LookupAgent.pubkey` matches this request's `wireguard_pubkey`; reject
  otherwise (see Security Model).
- When an Agent ID already has a WireGuard peer under a different public key
  (rotation or reinstall), apply the phased remove-old / add-new / update-
  registry sequence from "Enrollment state: concurrency and restart
  semantics," persisting `phase` and the old/new key and allocated IP in
  the enrollment-state row before each sub-step, so a crash mid-sequence
  resumes from the recorded phase rather than leaving both peers present or
  re-deriving keys from a possibly-different retry request. The WireGuard
  device (`internal/wireguard/device.go`) only exposes separate `AddPeer`/
  `RemovePeer` calls, not a transactional replace — the phase sequence is
  what makes this reconcilable.
- Trigger an immediate WireGuard handshake after successful peer creation;
  do not wait solely for the next keepalive interval.

### Discovery

No new rendezvous storage or secret handling is required. The existing Agent
registration record must retain the observed UDP endpoint long enough to cover
the rendezvous record TTL. Existing endpoint validation and rate limits apply.

## Failure Handling and Observability

| Condition | Result | Operator action |
| --- | --- | --- |
| No explicit bootstrap endpoint and no Discovery-confirmed Agent STUN address | Existing direct-bootstrap failure | Configure STUN/fixed Agent UDP port, or set the Agent TCP endpoint explicitly. |
| No usable Agent observed UDP endpoint | Enrollment rejected before ticket consumption or issuance; record unacked; Agent retries the same ticket on the next dial-back | Configure STUN and allow the Agent WireGuard UDP port. |
| PSK/ticket/nonce invalid | Enrollment rejected; no mesh mutation, no ticket consumed | Correct credentials; investigate security logs. |
| Peer add fails | Roll back a newly allocated IP; ticket not yet consumed; record unacked; Agent retries the same ticket on the next dial-back | Inspect Colony WireGuard/device logs. |
| Ticket consumption/issuance fails | Enrollment rejected; record unacked; Agent must obtain a fresh referral ticket (the consumed `jti` cannot be replayed) | Investigate CA logs; retry bootstrap from scratch. |
| Discovery endpoint's pubkey doesn't match `RegisterRequest.wireguard_pubkey` | Enrollment rejected before peer creation; record unacked; ticket unconsumed | Endpoint record for this `agent_id` was likely overwritten by an unrelated caller (Discovery `RegisterAgent` is unsigned); re-register the Agent with Discovery and retry. |
| Response lost after commit | Idempotent retry replays the stored `completed` enrollment-state record (same certificate, same `RegisterResponse`); no re-issuance | Agent retries during record TTL. |
| Crash between `authorized` and `completed` phase | Next attempt finds the row in an `authorized`-or-later phase and resumes from that exact phase, checking the CA/registry for this `jti`/CSR hash before reissuing rather than assuming no issuance occurred | Investigate Colony logs for the `record_id`; this is expected reconciliation, not blind retry. |
| Crash before `authorized` is ever reached (row stuck in `claimed`) | The row is deleted, not resumed; the next attempt restarts from ticket/PSK validation with its own request data — never advances a never-validated row to peer setup or issuance | None — expected recovery. If this recurs for the same `record_id`, investigate why validation keeps failing/crashing. |
| Two concurrent deliveries of the same `record_id` (dial-loop race) | Exactly one wins the atomic insert and proceeds; the other waits on the lease and replays the winner's `completed` result | None — expected behavior; no duplicate certificate or peer. |
| Enrollment-state lease expires while `authorized` or later (Colony crashed mid-attempt, post-validation) | A subsequent caller steals the lease and resumes from the row's recorded phase, not from the top | Investigate Colony logs if this recurs for the same `record_id`; a single occurrence is expected recovery, not a bug. |
| Crash mid-peer-phase (between `old_peer_removed` and `new_peer_added`) | Next attempt (retry or lease steal) resumes from the recorded phase using the row's stored `old_pubkey`/`new_pubkey`/`allocated_ip`, not live request data | Investigate WireGuard device logs for the `record_id` if the Agent never reaches `registry_updated`. |
| Agent re-enrolls under a new WireGuard key (rotation/reinstall) | Old peer removed, then new peer added, under the same allowed IPs, via the phased procedure; brief connectivity gap during the swap, no duplicate peers left behind | None — expected behavior. |
| No Colony UDP return path | Certificate/enrollment succeed but handshake stays down | Check local outbound UDP/NAT policy. |

Required structured events and metrics:

- `rendezvous_enrollment_started`, `rendezvous_enrollment_succeeded`, and
  `rendezvous_enrollment_failed` with record ID, agent ID, and failure class.
- `rendezvous_wireguard_handshake_started` and elapsed time to first handshake.
- A distinct error for missing Agent UDP endpoint; never report it as a generic
  mesh registration timeout.

## Rollout and Compatibility

1. Add protocol and handler support behind an Agent capability check: the
   Agent's registration/handshake payload carries a capability list, and the
   Agent advertises `bootstrap_and_register` only once it supports the
   compound RPC (see Resolved Design Decisions).
2. Ship the Colony implementation before enabling the Agent path.
3. New Agents use compound enrollment only after direct bootstrap fails and
   rendezvous is configured; all ordinary public-Colony deployments retain the
   current flow.
4. Older Colonies cause a clear unsupported-rendezvous-enrollment error; the
   Agent must not falsely report success after certificate issuance alone.
5. Older Agents that only speak RFD 108's plain `RequestCertificate` over a
   rendezvous connection must keep working against an upgraded Colony: the
   restricted rendezvous handler continues to route both `RequestCertificate`
   and `BootstrapAndRegister` (see API and Component Changes), rather than
   routing only the compound RPC. An older Agent then falls back to the
   existing ordinary `MeshService.Register` retry loop after certificate
   issuance, exactly as it does today — it simply doesn't benefit from atomic
   enrollment until it upgrades. Only once no unupgraded Agents are expected
   in a deployment may an operator narrow the handler to
   `BootstrapAndRegister` only.
6. Once deployed, deprecate the misleading assumption that a certificate-only
   RFD 108 session makes a NAT-local Colony fully usable.

## Testing

- Unit tests for nonce and Agent-ID binding, idempotent allocation, rollback,
  endpoint selection, and empty Agent-side peer endpoint configuration.
- Unit tests for `jti` consumption in `ca.Manager`: a second `IssueCertificate`
  call for an already-consumed `jti` is rejected, distinct from today's
  stateless-validation behavior.
- Unit tests for enrollment-state phase transitions (`claimed` ->
  `authorized` -> ... -> `completed`), and a lookup against a `completed` row
  replays the stored response without calling the CA or WireGuard device
  again.
- Unit test asserting a replay request with a valid `record_id` but an
  invalid/missing nonce is rejected before the `completed`-state lookup runs
  at all — nonce validation must not be bypassable via the replay path.
- **Unauthenticated-row-is-never-resumed test (regression for the specific
  defect this design closes):** simulate a crash while a row is still in
  `claimed` phase (inserted, lease held, but ticket/PSK validation never
  completed). Assert that a subsequent call for the same `record_id` deletes
  the stale row and re-runs ticket/PSK/identity validation from scratch —
  and, critically, does *not* advance directly to peer setup or certificate
  issuance for the unvalidated row.
- Concurrency test: two simultaneous `BootstrapAndRegister` calls for the same
  `record_id` (simulating a dial-loop race); assert exactly one atomic-insert
  winner proceeds through ticket validation and issuance, the other blocks on
  the lease and receives the winner's replayed result, and only one
  certificate/peer/IP is ever created.
- Lease-expiry test (post-authorization): bring a row to `authorized` (or a
  later phase), then hold its lease past expiry without completing it
  (simulated crash); assert a new call for the same `record_id` steals the
  lease and resumes from the recorded phase without re-running ticket/PSK
  validation.
- Phased peer-replacement restart test: crash (or fault-inject) between each
  post-authorization phase transition (`ip_allocated`, `old_peer_removed`,
  `new_peer_added`, `registry_updated`) and assert a subsequent attempt
  resumes deterministically from the row's recorded phase and pre-image
  (`old_pubkey`/`new_pubkey`/`allocated_ip`), ending in exactly one peer for
  the Agent ID with no duplicate allowed-IP claims.
- Integration test: local Colony with no public listener plus public test Agent
  endpoint; assert certificate issuance, stable mesh assignment, Colony-driven
  WireGuard handshake, and heartbeat over the mesh.
- Retry test that drops the compound response after the Colony commits; assert
  no duplicate address/peer, no second certificate issuance, and eventual
  Agent success via replayed state.
- Test for the `authorized`-but-not-`completed` crash window: simulate a
  process restart between ticket consumption and marking the row `completed`;
  assert recovery checks the CA/registry for the `jti` before deciding whether
  to reissue.
- Test for Discovery endpoint/pubkey binding: a Discovery record for
  `agent_id` whose `pubkey` doesn't match the enrolling Agent's
  `wireguard_pubkey` is rejected before peer creation.
- Test for key rotation: a second enrollment for the same Agent ID under a new
  WireGuard public key removes the old peer and adds the new one under the
  same allowed IPs, with no duplicate peer left behind.
- Negative tests for an absent/malformed Discovery UDP endpoint, mismatched
  agent ID, wrong PSK, wrong nonce, and a TCP bootstrap endpoint that is not a
  usable UDP endpoint.
- Regression test that direct public-Colony bootstrap still uses the existing
  `RequestCertificate` plus ordinary registration flow.

## Alternatives Considered

### Reuse `RequestCertificate`, then call `MeshService.Register` on the same connection

This avoids a new protobuf method, but creates an awkward two-RPC state
machine: a certificate may be issued if registration fails, and the handler
must bind request identity across two independent messages. A compound method
makes the authorization and allocation boundary explicit and supports
idempotent retry.

### Have Discovery push the Agent endpoint to the Colony

Discovery should remain a directory/rendezvous service, not a trusted
control-plane relay. A push alone also cannot safely allocate an IP or decide
which authorized enrollment owns the Agent public key.

### Require a public Colony endpoint or an SSH tunnel

This works operationally but defeats the local-first workflow RFD 108 was
introduced to support and adds external infrastructure to every debugging
session.

### Use the Agent TCP bootstrap port for WireGuard

The rendezvous listener is TCP while WireGuard is UDP. Their NAT mappings and
ports are independent, so treating one as proof of the other is incorrect.

## Resolved Design Decisions

- **`BootstrapAndRegister` stays a `ColonyService` method**, not a separate
  internal-only service. The restricted RFD 108 handler already routes only
  `RequestCertificate` and `BootstrapAndRegister` and 404s everything else on
  that connection (Security Model, "No broad rendezvous handler"); it is never
  exposed on the ordinary public listener. A separate service would add
  another schema and routing surface for no additional safety over what the
  restricted handler already guarantees.
- **Discovery endpoint freshness is deferred**, not added to this RFD. The
  pubkey-match check in Enrollment Processing step 6 already closes the
  security gap (a substituted or stale record cannot let an attacker complete
  a WireGuard handshake without the Agent's private key). A stale-but-pubkey-
  matching endpoint degrades to the existing "No Colony UDP return path"
  failure mode (Failure Handling and Observability), which is a reliability
  concern, not a security one. A freshness lease can be added later as a
  hardening follow-up if stale endpoints prove to be a recurring operational
  issue.
- **Version gating uses an Agent capability list, not a single protocol
  version field.** The Agent's registration/handshake payload gains a
  repeated capability-string (or bitset) field; the Agent includes
  `bootstrap_and_register` when it supports the compound RPC, and the Colony
  checks for that capability before offering it. This is chosen over a single
  `protocol_version` integer because it lets future features be gated
  independently instead of conflating unrelated capabilities into one
  monotonically increasing number, and it composes with the existing Rollout
  plan (step 1: "Add protocol and handler support behind an Agent
  capability/version check") without redefining what "version" means later.

## Future Work

- **Full crash-window certificate recovery.** If the Colony crashes between
  consuming the referral ticket's `jti` and marking the enrollment-state row
  `completed`, the current implementation (`Enroller.finish`) detects the gap
  via `IsReferralTicketConsumed` but cannot recover the previously-issued
  certificate bytes — `ca.Manager` has no lookup-by-`jti` — so it returns a
  distinct, actionable error instead of the RFD's originally-envisioned
  "check the CA/registry before reissuing" recovery. The Agent's next
  retry needs a fresh referral ticket in this narrow window. Closing this
  fully requires indexing issued certificates by `jti` (or storing the
  certificate on the enrollment row immediately at issuance instead of only
  at completion) as a follow-up hardening change.
- **IP allocation isn't released if a peer add fails and the Agent never
  retries.** `wireguard.Allocator.Allocate` is idempotent per `agent_id`, so
  a retry reuses the same IP correctly, but an abandoned enrollment (Agent
  gives up after a transient `AddPeer` failure) leaks that allocation rather
  than being explicitly rolled back, as the original Failure Handling table
  describes. Low impact (mesh subnets are large relative to expected churn),
  but worth revisiting if it proves to matter operationally.
