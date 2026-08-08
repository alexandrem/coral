---
rfd: "108"
title: "PSK-Encrypted Rendezvous for NAT-Traversing Agent Bootstrap"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "023", "048", "049", "087", "088" ]
database_migrations: [ ]
areas: [ "discovery", "agent", "colony", "security" ]
---

# RFD 108 - PSK-Encrypted Rendezvous for NAT-Traversing Agent Bootstrap

**Status:** 🎉 Implemented

## Summary

This RFD lets a Colony and Agent bootstrap mTLS trust even when the Colony has
no inbound connectivity, by using the Discovery Service (`discovery.coralmesh.dev`)
purely as an **encrypted rendezvous point**: whichever side already knows the
Bootstrap PSK (RFD 088) publishes a small PSK-encrypted "how to reach me"
record, the other side decrypts it and dials directly. The certificate
request/response never touches Discovery — only an opaque, PSK-authenticated
endpoint blob does.

## Problem

### Current Limitations

Under **RFD 048 (Agent Certificate Bootstrap)**, an agent requires a valid mTLS
client certificate before joining the WireGuard mesh. Certificate enrollment
follows a **pull-based HTTPS model**: the Agent looks up the Colony's endpoints
via Discovery, then dials the Colony's public HTTPS API directly to present its
CSR and Bootstrap PSK.

This assumes the Colony is dialable. WireGuard mesh data traffic (UDP) works
seamlessly even behind NAT, because RFD 023/029 already solve UDP hole-punching
and Discovery relays STUN-observed endpoints. But the **bootstrap HTTPS
handshake has no such fallback** — if the Colony has no inbound TCP path, the
Agent's dial fails outright:

```text
DBG Skipping unreachable/invalid endpoint error="dial tcp 127.0.0.1:8443: connect: connection refused" endpoint=https://127.0.0.1:8443
ERR Certificate bootstrap failed error="none of the discovered endpoints passed fingerprint validation"
```

### Why This Matters

A common developer workflow is running a **Colony locally on a workstation**
(behind home/office NAT) while observing **remote Agents on cloud VPSs or
Kubernetes clusters**. Today this requires SSH reverse tunnels, third-party
tunnels (ngrok, cloudflared), or router port forwarding — all friction that
blocks local-first debugging.

### Constraint From RFD 088

RFD 088 closed a gap where a Discovery-visible secret (the CA fingerprint) was
enough to join a colony, by introducing the Bootstrap PSK — a secret Discovery
**never sees**. Any fix to the NAT problem must preserve that invariant: the
PSK, the CSR, and the issued certificate must never appear in Discovery, even
in transit.

## Solution

### Key Insight

TCP dial direction and the TLS handshake roles are independent. Nothing
requires the side that presents the server certificate to also be the side
that opened the TCP connection. So instead of relaying certificate data
through Discovery, we let **whichever side is unreachable stay the TLS
server** and simply flip **who dials whom**, using Discovery only to tell the
dialable side where to connect.

### High-Level Approach

1. The Agent tries the existing direct-dial flow against the Colony's
   Discovery-published endpoints (unchanged). If every endpoint is
   loopback/private/unreachable, it falls back to rendezvous mode.
2. The Agent must know its own dialable endpoint. **This is not inferred from
   RFD 023's STUN-observed endpoint** — that observation is a UDP mapping
   for the WireGuard socket, doesn't imply anything about an unrelated TCP
   port, and in any case doesn't exist yet at this point in the lifecycle
   (STUN registration happens via `RegisterAgent`, which requires the mesh
   membership this RFD is trying to bootstrap). Instead, the operator
   explicitly configures a public bootstrap endpoint
   (`--bootstrap-public-endpoint` / `CORAL_BOOTSTRAP_PUBLIC_ENDPOINT`),
   mirroring how the Colony's own dialable endpoint is already configured
   today (`CORAL_PUBLIC_ENDPOINT`, RFD 085). If unset, bootstrap fails fast
   with a clear error — this RFD covers "at least one side has a configured,
   dialable endpoint," not automatic NAT detection or double-NAT (see Future
   Work).
3. The Agent opens a **single-use, short-lived TLS listener** on an ephemeral
   port and publishes a **PSK-encrypted rendezvous record** — `{endpoint,
   session_nonce, write_token, expires_at}` — to Discovery, keyed by
   `mesh_id`. The record is bound to a server-generated `record_id` (public,
   returned to the caller) and a client-generated `write_token` (secret,
   required for every subsequent republish or ack — see Key Design Decisions
   and Security Model). Publish acceptance does **not** gate on reachability
   by default: because `mesh_id` is free for anyone to choose, an
   unauthenticated synchronous TCP-connect probe is a public internet
   scanning primitive, not just an SSRF guard (see Security Model item 9).
   Reachability verification is instead an **opt-in**, quota-limited
   diagnostic (`--verify-reachability`) — when not requested, a
   misconfigured `--bootstrap-public-endpoint` still surfaces as a clear,
   actionable error, just via the existing 120s no-dial-back timeout instead
   of an immediate rejection.
4. The Colony runs a **separate long-polling loop**, independent of its
   `RegisterColony` heartbeat (`internal/discovery/registration/manager.go`,
   default 60s via `constants.DefaultRegisterInterval` /
   `CORAL_DISCOVERY_REGISTER_INTERVAL`) — piggybacking on that cadence would
   mean a record published just after a heartbeat tick waits up to a minute
   before the Colony even looks for it, most of a 90s listener lifetime.
   Instead the Colony holds an open `PollBootstrapRendezvous` call with a
   server-side wait (Discovery blocks up to ~25s and returns immediately if
   a record appears), and re-issues the call immediately on every return —
   giving near-immediate pickup while bounding request volume to about one
   call per ~25s when idle.
5. On successful decryption, the Colony dials the Agent's endpoint directly.
   Once the TCP connection is open, the **Colony still acts as the TLS
   server** — presenting the same certificate chain it always has — so the
   Agent validates the Root CA fingerprint and colony-ID SAN exactly as it
   does today (RFD 048, unchanged).
6. The Agent, now holding a validated TLS session to the real Colony, sends
   the normal `RequestCertificate(csr, referral_ticket, bootstrap_psk)` call
   (RFD 048/049/088, unchanged) over this connection and receives its
   certificate. The Colony then calls `AckBootstrapRendezvous`, presenting
   the `write_token` it recovered by decrypting the record — the only way
   to prove it, and not some third party who merely polled and observed
   `record_id`, is entitled to retire this record (see Key Design Decisions
   #7 and Security Model item 10).

Discovery's only new responsibility is storing and returning an opaque,
PSK-authenticated ciphertext blob with a short TTL. It never sees the PSK
itself, the CSR, the referral ticket, or the certificate.

### Key Design Decisions

1. **Rendezvous, not relay**: Discovery exchanges *reachability information*,
   never bootstrap payload. This keeps the RFD 088 trust boundary intact —
   Discovery compromise cannot forge certificates or harvest PSKs.
2. **PSK as encryption key, not just an auth token**: Derive a symmetric AEAD
   key from the Bootstrap PSK via HKDF-SHA256 with a dedicated info string
   (`coral-bootstrap-rendezvous-v1`), distinct from any other PSK-derived key
   in the system. Only parties who know the PSK can produce or read a valid
   record; Discovery stores ciphertext it cannot interpret or forge.
3. **Dial direction is decoupled from TLS role**: the side that is NAT'd keeps
   acting as the TLS server even when it's the one that opened the TCP
   connection. This means **zero changes** to the existing CA-fingerprint
   validation logic (RFD 048) or the `RequestCertificateRequest`/`Response`
   message schemas and authorization logic (RFD 048/049/088). It does
   require a new transport adapter on both sides to run the existing HTTP/2
   client/server roles over a single already-established connection instead
   of the normal dial/listen paths, plus a connection-scoped nonce header
   (not an RPC field) to bind the call to the specific rendezvous record —
   see Data Flow § Rendezvous Session Binding.
4. **Stateless, ephemeral records**: Rendezvous entries live in the same
   per-`mesh_id` Durable Object Discovery already uses for colony/agent
   registration (RFD 087), with a short TTL (default 90s) and the existing
   alarm-based cleanup pattern. No new storage backend.
5. **Bounded scope**: This RFD only solves the case where at least one side
   has an explicitly configured, dialable endpoint. Both sides behind NAT
   with no public endpoint is explicitly out of scope (Future Work).
6. **Reachability is proven when requested, not inferred**: the Agent's
   dialable endpoint comes from explicit operator configuration. Discovery
   *can* TCP-probe it before accepting a publish, but only when the caller
   opts in — see #8 below and Security Model item 9 for why an
   unconditional, unauthenticated probe is itself a hazard. We deliberately
   do not reuse RFD 023's UDP STUN observation as a reachability signal for
   this TCP listener — different protocol, different NAT mapping, and it
   isn't even available yet at bootstrap time (see High-Level Approach,
   step 2).
7. **Record identity and write authority are separate values**: `record_id`
   (server-assigned, ULID) is returned by every `Poll` response and is
   effectively public — anyone who knows or guesses `mesh_id` and polls can
   observe it. It is therefore unsuitable, on its own, as proof of the right
   to mutate or delete a record. A second, secret `write_token` — generated
   client-side via `crypto/rand` at first publish, 256 bits — is required
   on every republish (upsert) and on `AckBootstrapRendezvous`. The Agent
   holds it because it generated it; the Colony learns it only by
   successfully decrypting the record, since the Agent embeds a copy in the
   encrypted payload (see Appendix). Discovery stores only a hash of it, so
   even a compromised Discovery datastore doesn't leak a value usable to
   forge future writes. This closes the gap where anyone who could poll a
   `record_id` could also overwrite or delete the record it names.
8. **Reachability probing is opt-in and separately abuse-limited**: because
   `mesh_id` is unauthenticated and free to choose, a synchronous
   "Discovery TCP-connects to whatever you tell it" gate is a public
   internet port-scanning primitive regardless of the private-address
   deny-list, which only stops *internal* SSRF, not scanning arbitrary
   public hosts. Publish therefore succeeds by default without probing;
   reachability verification is an explicit, opt-in request field
   (`verify_reachability`) subject to global source-IP quotas, a bounded
   concurrent-probe budget, and per-target caps — independent of, and in
   addition to, the existing per-`mesh_id` limiter (see Security Model
   item 9).

---

## Data Flow & Architecture

### Detailed Sequence Diagram

```mermaid
sequenceDiagram
    autonumber
    participant Agent as Remote Agent (VPS, dialable)
    participant Discovery as Discovery Service (Public HTTPS)
    participant Colony as Local Colony (Workstation, behind NAT)

    Note over Agent,Colony: Phase 1: Direct Dial Fails
    Agent->>Discovery: LookupColony(mesh_id)
    Discovery-->>Agent: endpoints=[127.0.0.1:8443, ...]
    Agent->>Agent: Direct HTTPS dial fails (loopback/unreachable)

    Note over Agent: Phase 2: Configured Endpoint Check
    Agent->>Agent: --bootstrap-public-endpoint configured? -> proceed (else fail fast)

    Note over Agent,Discovery: Phase 3: Publish Encrypted Rendezvous
    Agent->>Agent: listener = Listen(DefaultAgentBootstrapPort=8444)
    Agent->>Agent: write_token = crypto/rand(32 bytes)
    Agent->>Agent: record = Encrypt(HKDF(psk), {endpoint, nonce, write_token, expires_at})
    Agent->>Discovery: PublishBootstrapRendezvous(mesh_id, ciphertext, write_token,<br/>ttl=90s, verify_reachability=false by default)
    Discovery->>Discovery: store SHA-256(write_token) only; raw value discarded after this call
    opt verify_reachability=true (opt-in, quota-limited)
        Discovery->>Agent: TCP-connect probe to claimed endpoint
    end
    Discovery-->>Agent: 202 Accepted, record_id=<ULID><br/>(probe result only relevant if opted in)

    Note over Colony,Discovery: Phase 4: Colony Long-Poll (independent loop,<br/>decoupled from the 60s RegisterColony heartbeat)
    loop Until success or Colony shutdown
        Colony->>Discovery: PollBootstrapRendezvous(mesh_id, wait_seconds=25)
        Discovery-->>Colony: [record_id, ciphertext, ...] (immediate whether<br/>published-while-waiting or already-existing;<br/>empty only after 25s with nothing pending)
        Colony->>Colony: Skip any record_id still inside its<br/>backoff window (2s/4s/8s, capped 15s)
    end
    Colony->>Colony: Decrypt remaining with HKDF(psk) / HKDF(psk_grace)
    Colony->>Colony: Success -> {agent_endpoint, session_nonce, write_token}

    Note over Agent: Listener keeps Accept()-ing (bounded-concurrency,<br/>5s handshake deadline per connection) until success<br/>or TTL expires (discards probes/noise, never blocks)

    Note over Colony,Agent: Phase 5: Colony Dials Agent (TLS roles unchanged)
    Colony->>Agent: TCP connect to agent_endpoint
    Colony->>Agent: TLS ServerHello (Colony runs tls.Server() over the<br/>outbound conn, presents cert chain as always)
    Agent->>Agent: Validate Root CA fingerprint + colony-ID SAN (RFD 048, unchanged)
    Agent->>Agent: TLS failure (e.g. Discovery's own probe, port noise) -> drop, keep listening

    Note over Agent,Colony: Phase 6: Certificate Request + Rendezvous Preface
    Agent->>Colony: RequestCertificate(csr, referral_ticket, bootstrap_psk)<br/>+ header Coral-Rendezvous-Nonce=<session_nonce>
    Colony->>Colony: Validate header == session_nonce decrypted in step 4
    Colony-->>Agent: 200 certificate + ca_chain (nonce matched)
    Colony->>Discovery: AckBootstrapRendezvous(mesh_id, record_id, write_token)
    Discovery->>Discovery: hash(write_token) == stored hash? delete : reject (unchanged)
    Colony--xAgent: reject + close (nonce missing/mismatched;<br/>record left unacked, PSK/ticket not evaluated)

    Note over Agent: Agent saves cert, closes listener, initializes WireGuard mesh
```

### Rendezvous Session Binding

The nonce needs an explicit place to travel and an explicit validator, or it's
unenforceable. This connection is unusual in two ways that both need to be
spelled out, not left implicit:

**1. TLS role and RPC role point in different directions.** Colony keeps the
TLS *server* role (presents its cert chain) even though it's the TCP active
opener, per the Key Insight — Go's `crypto/tls` supports this directly:
whichever side wraps the connection with `tls.Server(conn, cfg)` plays server,
independent of who called `Dial` vs `Accept`. But the RPC direction is
unchanged from today: **Agent is still the buf-connect client that calls
`RequestCertificate`, Colony still serves it.** That means Agent — the TCP
passive acceptor — ends up issuing the outbound HTTP/2 request on a
connection it didn't dial, and Colony — the TCP active opener — ends up
running the HTTP/2 *server* side (its existing ColonyService handler) on a
connection it dialed instead of accepted. This needs an explicit transport
adapter on both ends (see Component Changes) — it is not something the
existing Colony HTTP server or Agent bootstrap client does today by pointing
them at each other.

**2. The nonce rides as a header on the (otherwise unchanged) `RequestCertificate`
call, not inside the RPC message.** Concretely:

- Agent attaches `Coral-Rendezvous-Nonce: <base64 nonce>` as an HTTP header
  on its `RequestCertificate` request over this connection. The
  `RequestCertificateRequest` protobuf message itself is untouched (RFD
  048/088 compatibility is preserved at the schema level); this is
  connection/session metadata, not RPC payload.
- **Colony validates the header, not the Agent.** Colony already knows the
  expected nonce — it's the value it decrypted alongside `agent_endpoint` in
  Phase 4, tied to the specific rendezvous record it chose to dial. On
  receiving the call, Colony compares the header value against that
  in-memory expected nonce for this dial attempt *before* evaluating the
  referral ticket or PSK. Missing or mismatched header → reject
  (`RENDEZVOUS_NONCE_MISMATCH`) and close the connection without touching
  authorization logic.
- This check only applies to connections Colony itself initiated via
  rendezvous dial-back. The normal direct-dial path (Agent → Colony's
  `public_port`) sends no such header and is completely unaffected.
- **What this does and doesn't protect against**: TLS cert validation
  (unchanged, RFD 048) already proves the Agent is genuinely talking to the
  real Colony — that's not what the nonce is for. The nonce instead lets
  Colony confirm the entity calling `RequestCertificate` on this specific
  outbound connection is the same session it decrypted and chose to dial,
  not an unrelated party that happened to occupy `agent_endpoint` (a stale
  record, a race with Discovery's own reachability probe, or port reuse).
  Actual authorization to receive a certificate still comes entirely from
  the referral ticket and PSK, exactly as before — the nonce is a session
  correctness check, not a new authorization boundary.
- **Records are not consumed on read, so re-delivery is deduplicated by
  `record_id`, not by re-polling**: `PollBootstrapRendezvous` does not delete
  a record on decrypt, and — unlike the long-poll's empty-result case —
  Discovery returns an *existing* record immediately, with no wait. Combined
  with the Colony's immediate-reissue poll loop, that would otherwise create
  a tight redial loop: the same still-valid record keeps coming back on
  every poll, gets redialed instantly, and — critically — this doesn't stop
  even after a *successful* bootstrap, since nothing removes the record
  until its 90s TTL. Two mechanisms fix this (see API Specifications and
  Component Changes for the concrete fields/RPC):
  1. **Explicit ack on success**: Discovery assigns each record a
     `record_id` (ULID) on publish, returned in
     `PublishBootstrapRendezvousResponse` and included on every
     `BootstrapRendezvousRecord`. After a successful fulfillment (nonce
     matched, certificate issued), Colony calls
     `AckBootstrapRendezvous(mesh_id, record_id, write_token)` to delete the
     record immediately, instead of waiting out the TTL. `write_token` is
     **not** the `record_id` — see the capability subsection immediately
     below for why a second, secret value is required here.
  2. **Client-side backoff on failure**: Colony keeps an in-memory
     `record_id -> last_attempt_at` map and will not re-attempt (decrypt is
     cheap and still happens, but dial/act is skipped) the same
     unacknowledged `record_id` more often than an exponential backoff
     (2s, 4s, 8s, capped at 15s). When every record returned by a poll is
     still within its backoff window, the Colony sleeps until the soonest
     one elapses (bounded, e.g. `min(remaining, 5s)`) before reissuing
     `PollBootstrapRendezvous`, rather than reissuing instantly — this is
     what actually bounds request/dial rate, since the long-poll itself
     doesn't block when a record already exists.

### Rendezvous Write Capability (`write_token`)

`record_id` is returned by `PollBootstrapRendezvous` to any caller who knows
`mesh_id` — which is not a secret, it's the same public identifier already
used for `LookupColony`. If `record_id` alone authorized republish
(UPSERT) or `AckBootstrapRendezvous`, anyone who polled could overwrite a
real Agent's rendezvous record with garbage (denying the legitimate
bootstrap, or worse, redirecting the Colony to dial an attacker-controlled
endpoint — though TLS cert validation in Phase 5 stops that from becoming a
full MITM) or delete it outright before the real Colony ever picks it up.
`record_id` is an identifier, not a credential, and must not be treated as
one.

- **Generation**: at first publish (`record_id` unset in the request), the
  Agent generates `write_token` itself — 256 bits via `crypto/rand` —
  rather than waiting for Discovery to mint and return one. This avoids a
  chicken-and-egg two-round-trip on the very first publish: the token needs
  to be embedded in the encrypted payload (below), and that payload is
  built before the first `PublishBootstrapRendezvous` call is made.
- **Discovery sees the raw token transiently, on every write call, by
  necessity**: `PublishBootstrapRendezvous` (first publish and every
  republish) and `AckBootstrapRendezvous` all carry `write_token` in
  plaintext as an RPC field — the RPC is over TLS to Discovery, same as
  every other call here, but Discovery necessarily terminates that TLS
  connection and must read the raw value to do the comparison at all. This
  is a *transiting authorization credential*, not a secret Discovery is
  blind to — different from the PSK, CSR, referral ticket, and certificate,
  which this RFD's trust model requires Discovery never see even in
  transit (see Trust Assumptions, Solution § Constraint From RFD 088).
  `write_token` deliberately does not carry that same guarantee: it only
  needs to be hidden from *other pollers*, not from Discovery itself,
  since Discovery is the party enforcing it.
- **What Discovery persists**: on first publish, Discovery computes and
  stores only `SHA-256(write_token)` alongside the record — the raw value
  is used for that one request (to derive the hash to store) and then
  discarded, never written to durable storage, never logged, and never
  included in any response. On every subsequent republish or ack,
  Discovery again receives the raw value transiently, hashes it in memory,
  compares against the stored hash with a constant-time comparison, and
  discards the raw value once the request completes.
- **What this does and doesn't protect against**: hashing at rest protects
  `write_token` from anyone who only ever observes Discovery's storage or
  its Poll responses (which never include `write_token` or its hash) —
  that's the actual third party this mechanism defends against (see the
  P1 scenario above). It is **not** a guarantee that Discovery never
  learns the value; Discovery is trusted to enforce the check honestly and
  not log/exfiltrate it, the same operational trust already placed in it
  for `probe_endpoint` and STUN-observed IPs (RFD 023/087). If Discovery
  itself must never learn `write_token` even transiently, this
  hash-and-compare design doesn't achieve that — it would need a
  different primitive (e.g. a MAC or blind-signature scheme the caller
  evaluates without revealing the token), which was judged unnecessary
  complexity here since Discovery already sees comparable metadata
  (`probe_endpoint`) and the harm model this protects against is a
  third-party poller, not Discovery itself.
- **What Discovery rejects**: a republish (UPSERT) or
  `AckBootstrapRendezvous` whose `write_token` doesn't hash-match the
  stored value is rejected (`PERMISSION_DENIED`) without mutating the
  record. This is enforced independent of, and in addition to, the
  existing per-`mesh_id` rate limit — a mismatch doesn't consume a "record
  now exists" state transition either way.
- **How the Colony gets it**: the Agent embeds `write_token` as a field in
  the plaintext JSON payload it AEAD-encrypts with the PSK-derived key (see
  Appendix). Only a party that can decrypt the record — i.e., knows the
  Bootstrap PSK — ever recovers `write_token` from the ciphertext. `Coral-
  Rendezvous-Nonce` and `write_token` are deliberately different
  mechanisms because they protect different things: the nonce binds a
  *specific connection* to a *specific dial attempt* (§ above), while
  `write_token` gates *who may mutate Discovery's stored record at all*,
  independent of whether a connection was ever established.
- **Scope**: only the Agent (which generates it), Discovery (transiently,
  per write request, as described above), and a Colony that successfully
  decrypts the record ever see `write_token`. Discovery never *persists*
  or *returns* the raw value, and it's distinct per record — a leaked or
  brute-forced token for one bootstrap attempt doesn't affect any other
  `mesh_id` or session.

---

## API Specifications

### Discovery Service — New RPCs

```protobuf
// Added to coral.discovery.v1.DiscoveryService

// Publish a PSK-encrypted rendezvous record so a peer can locate this side.
rpc PublishBootstrapRendezvous(PublishBootstrapRendezvousRequest) returns (PublishBootstrapRendezvousResponse);

// Poll for pending rendezvous records published for this mesh_id.
rpc PollBootstrapRendezvous(PollBootstrapRendezvousRequest) returns (PollBootstrapRendezvousResponse);

// Delete a record after successful fulfillment, instead of waiting out its TTL.
rpc AckBootstrapRendezvous(AckBootstrapRendezvousRequest) returns (AckBootstrapRendezvousResponse);
```

```protobuf
message PublishBootstrapRendezvousRequest {
  // Mesh/Colony ID this record is for.
  string mesh_id = 1;

  // AES-256-GCM ciphertext of the rendezvous payload (see Appendix).
  // Opaque to Discovery.
  bytes ciphertext = 2;

  // AES-256-GCM nonce (12 bytes) used for THIS Seal() call. Not secret, but
  // MUST be freshly random every time this RPC is called (including every
  // republish of the same session) — never reuse a GCM nonce with the same
  // derived key across different Seal() calls. Distinct from the
  // `session_nonce` inside the encrypted payload (see Appendix), which is
  // an opaque application-level session token, not a cryptographic nonce,
  // and is intentionally kept stable across republishes.
  bytes gcm_nonce = 3;

  // Requested TTL in seconds (server may cap, e.g. max 90s).
  int32 ttl_seconds = 4;

  // Endpoint Discovery may TCP-probe before accepting the record, but only
  // if verify_reachability=true (see below). This is the *plaintext* dial
  // target (ip:port) — necessary for Discovery to prove reachability, even
  // though the rendezvous payload itself is encrypted. It reveals no more
  // than the IP:port the Agent operator already explicitly configured for
  // inbound bootstrap traffic. Ignored (may be left unset) when
  // verify_reachability=false.
  string probe_endpoint = 5;

  // Optional: the record_id returned by a prior PublishBootstrapRendezvous
  // call for this same bootstrap attempt. When set, Discovery UPSERTs
  // (replaces ciphertext/gcm_nonce/expires_at on) the existing row instead
  // of inserting a new one, so a republish refreshes the same record in
  // place rather than creating a second, overlapping row for the same
  // session. Left empty on the first publish of a session. When set,
  // write_token below MUST match the hash stored for this record_id, or
  // the request is rejected (PERMISSION_DENIED) — see §Data Flow,
  // Rendezvous Write Capability.
  string record_id = 6;

  // Capability required to mutate this record. On first publish
  // (record_id unset), the Agent generates this client-side (crypto/rand,
  // 32 bytes). Discovery receives the raw value on this call (necessarily —
  // it's the one checking it) but persists only SHA-256(write_token), never
  // the raw value. On every subsequent republish, the same raw value MUST
  // be presented again and is checked against the stored hash. Never
  // returned by any response (Publish, Poll, or Ack); the only other party
  // who ever recovers it is a Colony that decrypts the record (it's
  // embedded in the encrypted payload — see Appendix). See §Data Flow,
  // Rendezvous Write Capability for the full rationale, including what
  // trust this does and doesn't place in Discovery.
  bytes write_token = 7;

  // Opt-in: request Discovery to synchronously TCP-probe probe_endpoint
  // (subject to the private-address deny-list and the global/per-source-IP
  // quotas in Security Model item 9) before accepting the record. Defaults
  // to false — publish succeeds without probing, and a misconfigured
  // endpoint is instead caught by the Agent's existing 120s no-dial-back
  // timeout. Kept opt-in (rather than a mandatory gate) because an
  // unauthenticated "TCP-connect to whatever the caller says" endpoint is
  // itself a public scanning primitive when mesh_id costs nothing to
  // generate — see Key Design Decisions #8.
  bool verify_reachability = 8;
}

message PublishBootstrapRendezvousResponse {
  bool success = 1;
  google.protobuf.Timestamp expires_at = 2;

  // Only meaningful when the request set verify_reachability=true. Set
  // when success=false and the cause was a failed reachability probe, to
  // distinguish from rate-limiting (429) or validation errors. Always
  // false when verify_reachability was false (no probe was attempted).
  bool probe_failed = 3;

  // Server-assigned ID (ULID) on first publish. Stable across the whole
  // bootstrap attempt: the Agent stores it and passes it back as
  // `record_id` on every subsequent republish so Discovery upserts the same
  // row instead of accumulating one row per republish. Colony-side backoff/
  // dedup (§Data Flow, Rendezvous Session Binding) keys on this stable ID.
  string record_id = 4;
}

message PollBootstrapRendezvousRequest {
  string mesh_id = 1;

  // Long-poll wait, in seconds. Discovery blocks up to this long, returning
  // immediately if a record is published while waiting, or with an empty
  // list on timeout. Server caps this (e.g. max 25s) independent of what
  // the caller requests. Does NOT wait if a record already exists — an
  // existing record is always returned immediately (see Data Flow §
  // Rendezvous Session Binding for why the Colony must not treat that as
  // license to redial instantly on every reissue).
  int32 wait_seconds = 2;
}

message PollBootstrapRendezvousResponse {
  repeated BootstrapRendezvousRecord records = 1;

  // True if this response was a timeout (empty records) rather than an
  // actual publish event; lets the Colony log/distinguish idle polling from
  // a real miss.
  bool timed_out = 2;
}

message BootstrapRendezvousRecord {
  string record_id = 1;
  bytes ciphertext = 2;
  bytes gcm_nonce = 3;
  google.protobuf.Timestamp published_at = 4;
}

message AckBootstrapRendezvousRequest {
  string mesh_id = 1;
  string record_id = 2;

  // Required. Must hash (SHA-256) to the value stored for record_id, or
  // the request is rejected (PERMISSION_DENIED) and the record is left
  // untouched. The caller only has this value if it decrypted the record
  // (Colony) or is the original publisher (Agent) — see §Data Flow,
  // Rendezvous Write Capability. Not checked when the record no longer
  // exists (see AckBootstrapRendezvousResponse below).
  bytes write_token = 3;
}

message AckBootstrapRendezvousResponse {
  // True if the record was deleted (write_token matched), or was already
  // gone (e.g. expired or already acked — idempotent, not an error
  // either way, and write_token isn't checked in that case since there's
  // nothing to authorize deleting). False if the record still exists but
  // write_token didn't match — this is a rejection, not an idempotent
  // no-op, and the record is left in place.
  bool success = 1;
}
```

No changes to `RequestCertificate` (RFD 048/088) or `CreateBootstrapToken`
(RFD 049) — the certificate exchange itself is untouched.

---

## Security Model & Cryptographic Verification

### Trust Assumptions

1. **Discovery remains blind to bootstrap secrets, but is a trusted
   authorization enforcer for `write_token`**: it stores and returns
   rendezvous ciphertext it cannot decrypt, keyed only by the already-public
   `mesh_id`. It cannot forge a valid record (would require the PSK) or read
   a published one. When reachability verification is opted into, Discovery
   sees the plain `probe_endpoint` IP:port used for the probe (§ below) —
   consistent with the existing trust model, since Discovery already
   handles endpoint/IP metadata for STUN and relay purposes (RFD 023/087).
   Discovery also receives the raw `write_token` transiently on every write
   RPC (publish/republish/ack), in order to hash and check it — it persists
   only the hash, never the raw value, and never returns it in any response
   (see item 10). This is different from the PSK, CSR, referral ticket, and
   certificate: those must never appear in any Discovery-visible message,
   even in transit, because Discovery compromise must not be able to forge
   a certificate. `write_token` doesn't carry that guarantee — Discovery is
   trusted to check it honestly and not persist/log/leak it, the same
   operational trust already extended to it for endpoint metadata.
2. **PSK reuse is safe via domain separation**: the rendezvous AEAD key is
   `HKDF-SHA256(psk, salt=mesh_id, info="coral-bootstrap-rendezvous-v1")` — a
   distinct derived key from any other PSK-derived material in the system.
   Deriving it does not expose or weaken the PSK's role as the
   `RequestCertificate` authorization secret.
3. **No new certificate-issuance trust surface**: the Colony still only
   issues certificates via the existing `RequestCertificate` flow, gated by
   the referral ticket (RFD 049) and PSK (RFD 088), exactly as before. The
   rendezvous mechanism only affects *how the TCP connection is
   established*, never *who is authorized to get a certificate*.
4. **TLS validation is unchanged**: regardless of dial direction, the Colony
   always presents its certificate chain as the TLS server, and the Agent
   always validates the Root CA fingerprint and colony-ID SPIFFE SAN exactly
   per RFD 048. Flipping the TCP dial direction introduces no new MITM
   surface.
5. **PSK rotation (RFD 088)**: while a PSK is in its grace period, the Colony
   attempts decryption with both the active and grace HKDF-derived keys, the
   same dual-acceptance pattern RFD 088 already uses for `RequestCertificate`.
6. **Garbage/forged records**: an attacker without the PSK can publish
   ciphertext, but the Colony's AEAD decryption will fail the authentication
   tag and the record is discarded. AEAD verification is cheap, so this is
   not a meaningful DoS vector on its own. To prevent unauthenticated
   ciphertext spamming (unbounded storage growth, wasted decrypt cycles on
   the Colony), Discovery MUST rate-limit `PublishBootstrapRendezvous` per
   `mesh_id` — e.g. a small token bucket (default: 5 requests/minute,
   burst 10), consistent with the limiting patterns RFD 086 applies to other
   unauthenticated Discovery endpoints. Requests over the limit get 429 with
   `Retry-After`, matching the existing bootstrap retry behavior in RFD 048.
7. **Session nonce**: transmitted as the `Coral-Rendezvous-Nonce` header on
   the Agent's `RequestCertificate` call and validated by Colony against the
   nonce it decrypted for that dial attempt (see Data Flow § Rendezvous
   Session Binding for the exact mechanism). This binds a specific
   rendezvous record to a specific completed exchange — it does not
   authenticate the Colony (TLS cert validation already does that) or gate
   authorization (the referral ticket and PSK still do that); it only
   prevents a stale record or an unrelated connection on the same endpoint
   from being treated as a completed bootstrap.
8. **Reachability probe is a diagnostics aid, not a trust boundary**: the
   Colony never dials `probe_endpoint` directly and never receives it via
   `PollBootstrapRendezvous` — that field only controls whether Discovery
   *accepts* the publish. The Colony only ever dials the endpoint it
   recovers by successfully decrypting the ciphertext, which requires the
   PSK. Discovery cannot cryptographically verify `probe_endpoint` matches
   the endpoint inside the ciphertext (it can't read the ciphertext), so the
   probe protects honest agents from misconfiguration (broken port-forward,
   typo) — it is not a defense against a malicious publisher, who gains
   nothing from lying about it anyway.
9. **Probe is opt-in, deny-listed, and globally quota-limited — not just
   per-`mesh_id`-limited**: `probe_endpoint` is unauthenticated (no PSK
   needed to set it) and `mesh_id` costs nothing to generate, so a
   synchronous "Discovery TCP-connects to whatever the caller says" gate is
   a public internet TCP scanning primitive, not merely an SSRF risk — a
   per-`mesh_id` limit alone caps nothing globally, since an attacker can
   mint unlimited `mesh_id`s and stay under any per-ID threshold while
   driving unbounded aggregate probe volume against arbitrary public hosts.
   Mitigations, layered:
   - **Opt-in, non-gating by default**: `verify_reachability` must be
     explicitly set true (see API Specifications); publish otherwise
     succeeds without probing at all. This shrinks the default attack
     surface to zero and confines the remaining exposure to callers who
     deliberately invoke the probe path — attacker-controlled callers
     included, which is why the controls below still apply unconditionally
     whenever a probe *is* requested.
   - **Deny-list (unchanged)**: reject RFC1918/loopback/link-local/
     cloud-metadata (`169.254.169.254`) targets, cap connect timeout (e.g.
     2s), perform a bare TCP connect with no data exchange, and close
     Discovery's side of the connection immediately once the handshake
     confirms reachability — it never lingers waiting for anything.
   - **Per-`mesh_id` limit (unchanged)**: same token bucket as
     `PublishBootstrapRendezvous` itself (item 6).
   - **Global per-source-IP quota**: a token bucket keyed on the caller's
     source IP (via Cloudflare's `CF-Connecting-IP`), independent of
     `mesh_id` — e.g. 20 probes/hour, burst 5 — implemented via
     Cloudflare's native Rate Limiting rules rather than a hand-rolled
     Durable Object counter, since the platform already provides
     IP-keyed, edge-enforced limiting and reinventing it in a DO would add
     a consistency/latency cost for no benefit.
   - **Global concurrent-probe cap**: a bounded number of in-flight probes
     across the whole Discovery deployment (e.g. 20 concurrent, via a
     Cloudflare Rate Limiting / WAF rule or a lightweight shared counter);
     requests over the cap are rejected with 429 immediately, without
     attempting a connect — bounds worst-case outbound connection fan-out
     regardless of how the request volume is distributed across
     `mesh_id`s or source IPs.
   - **Per-source-IP distinct-target cap**: cap the number of *distinct*
     `probe_endpoint` values a single source IP may request within a
     rolling window (e.g. 10 distinct targets/hour) — this specifically
     stops a low-and-slow scan that spreads one probe per `mesh_id` widely
     enough to stay under the raw request-rate quota above.
   - **Non-goal**: none of this makes the probe a trust boundary (see item
     8, unchanged) — it remains a reachability diagnostic for honest
     operators. The quotas exist purely to keep an opt-in diagnostic from
     being scalable into an abuse primitive, not to authenticate callers.
10. **`record_id` is public; `write_token` is the actual write/ack
    capability — checked by Discovery, not hidden from it**: `record_id` is
    returned by `PollBootstrapRendezvous` to any caller who knows `mesh_id`
    (itself not a secret), so it alone cannot authorize mutating or deleting
    a record — anyone who polled could otherwise republish garbage over a
    real Agent's record or delete it early. `write_token` (256-bit,
    `crypto/rand`, generated client-side by the Agent at first publish) is
    required on every republish and on `AckBootstrapRendezvous`. Discovery
    necessarily receives the raw value on each of those calls — it's the
    party performing the check — but persists only its SHA-256 hash, never
    the raw value, and never returns it in any response. What `write_token`
    defends against is a third party who only ever observes `mesh_id` and
    polls (they never see it — it's not in Poll responses or storage in
    recoverable form); it is not designed to be hidden from Discovery
    itself, which is trusted to enforce the check honestly, the same
    operational trust already placed in it for `probe_endpoint` and
    STUN-observed endpoints. The Agent has `write_token` because it
    generated it; a Colony only obtains it by successfully decrypting the
    record, since the Agent embeds a copy in the encrypted payload
    (Appendix) — so the same PSK-possession requirement that gates reading
    a record also gates a Colony's ability to ack it. See §Data Flow,
    Rendezvous Write Capability for the full design and rationale.

---

## Component Changes

### 1. Discovery Service (`internal/discovery` / Cloudflare Worker)
- Add `PublishBootstrapRendezvous` / `PollBootstrapRendezvous` /
  `AckBootstrapRendezvous` RPCs to `discovery.proto` and the buf-connect
  handlers.
- On publish, only perform the TCP-connect reachability probe against
  `probe_endpoint` when the request sets `verify_reachability=true` (deny-
  list private/loopback/link-local/metadata ranges, 2s connect timeout, no
  data exchange); reject with `probe_failed=true` if it can't connect. When
  `verify_reachability=false` (the default), skip the probe entirely and
  accept the record without touching `probe_endpoint`. Close the probe
  connection immediately on confirmed connect — don't hold it open.
- Extend the existing per-`mesh_id` Durable Object (RFD 087) with a
  `bootstrap_rendezvous` SQLite table (`record_id` PK (ULID), `mesh_id`,
  `ciphertext`, `gcm_nonce`, `write_token_hash`, `expires_at`), reusing the
  existing alarm-based cleanup batch. On first publish (`record_id` empty),
  generate a new `record_id`, hash the supplied `write_token`
  (SHA-256), and insert. On republish (`record_id` set), require
  `SHA-256(write_token)` to match the stored `write_token_hash`
  (constant-time comparison) before UPSERTing
  (`ciphertext`/`gcm_nonce`/`expires_at`); reject with `PERMISSION_DENIED`
  on mismatch without mutating the row.
- Implement `AckBootstrapRendezvous` as a `DELETE ... WHERE record_id = ?
  AND write_token_hash = SHA256(?)`. If the record doesn't exist, return
  `success=true` (idempotent, no token check needed). If it exists but the
  hash doesn't match, return `success=false` and leave the row in place —
  this is a rejection, not an idempotent no-op.
- Apply per-`mesh_id` rate limiting on publish (covers both storage writes
  and probe attempts, when probing is requested).
- Apply the global reachability-probe abuse controls from Security Model
  item 9 whenever `verify_reachability=true`: a per-source-IP token bucket
  and a global concurrent-probe cap (both via Cloudflare Rate Limiting
  rules, keyed on `CF-Connecting-IP`), plus a per-source-IP distinct-
  `probe_endpoint` cap tracked over a rolling window. These are enforced
  in addition to, not instead of, the per-`mesh_id` limit and the deny-list
  — a request failing any one of them is rejected before a connect is
  attempted.
- Implement `PollBootstrapRendezvous` as a long-poll: if a record already
  exists for the `mesh_id`, return it **immediately, with no wait** —
  the Colony's own backoff/dedup (§Data Flow) is what prevents that from
  becoming a busy-loop, not Discovery withholding data it already has. On
  an empty result, hold the request open (capped at ~25s server-side
  regardless of the caller's `wait_seconds`) and resolve it immediately if
  a matching `PublishBootstrapRendezvous` lands for the same `mesh_id`
  while waiting, otherwise return an empty/`timed_out=true` response at the
  cap. Never include `write_token` or its hash in `BootstrapRendezvousRecord`
  — Poll responses stay identifier-only. Rate-limit poll call volume per
  `mesh_id` too (a tight client retry loop on disconnect shouldn't be
  free), separate from the publish limit.

### 2. Colony (`internal/colony`)
- Derive the rendezvous AEAD key(s) (active + grace) from the Bootstrap PSK
  on startup and on rotation.
- Run a **dedicated long-poll goroutine**, started alongside (not inside)
  the existing `internal/discovery/registration/manager.go` heartbeat
  loop — deliberately not reusing its `RegisterInterval` ticker (default
  60s), since that cadence is too coarse for a 90s-TTL rendezvous record.
  The loop calls `PollBootstrapRendezvous(mesh_id, wait_seconds=25)`. If
  every record in the response is within its per-`record_id` backoff window
  (see below), sleep until the soonest one elapses (capped, e.g. 5s) before
  reissuing; otherwise reissue immediately.
- Maintain an in-memory `record_id -> {last_attempt_at, attempt_count}` map.
  Skip decrypt/dial for any `record_id` still inside its backoff window
  (exponential: 2s, 4s, 8s, capped 15s); this is what actually bounds redial
  rate, since Discovery returns an already-existing record immediately on
  every poll rather than waiting.
- Attempt decryption of each record not currently backed off; keep the
  decrypted `{agent_endpoint, session_nonce, write_token}` in memory per
  pending dial attempt, keyed by `record_id`. `write_token` is only ever
  held in memory, never logged or persisted.
- On success, dial the decrypted endpoint and wrap the outbound `net.Conn`
  with `tls.Server(conn, cfg)` using the existing bootstrap TLS
  configuration (same cert chain/config as the normal listener path — no
  changes to certificate presentation logic).
- **New transport adapter, not a new handler**: the existing ColonyService
  `RequestCertificate` business logic (referral ticket + PSK validation,
  issuance) is unchanged, but it currently runs behind an HTTP server bound
  to a `net.Listener`. Add a thin adapter that instead runs
  `http2.Server{}.ServeConn(tlsConn, ...)` against this single dialed
  connection, routed to the same handler/mux the normal bootstrap listener
  uses. This is new (small) code, not a reuse of the existing `Serve()`
  loop as-is.
- Before the handler evaluates the referral ticket/PSK, validate the
  `Coral-Rendezvous-Nonce` request header against the `session_nonce`
  decrypted for this dial attempt (see Data Flow § Rendezvous Session
  Binding); reject and close on mismatch without falling through to normal
  authorization checks. On mismatch, leave the record unacked (client-side
  backoff handles the retry, per above).
- On a successful `RequestCertificate` exchange over this connection, call
  `AckBootstrapRendezvous(mesh_id, record_id, write_token)` immediately —
  using the `write_token` decrypted for this dial attempt — so the record
  stops being returned by subsequent polls instead of lingering until its
  90s TTL. Colony is the only party that ever calls Ack, so it must have
  successfully decrypted the record (and therefore recovered
  `write_token`) before it can retire it.
- Add structured audit logging for rendezvous decrypt attempts and nonce
  validation results (success/failure counts only — never log plaintext
  endpoints, nonces, or PSKs).

### 3. Agent Bootstrap Client (`internal/agent/bootstrap`)
- Reuse the existing loopback/unreachable detection that already triggers a
  fallback path.
- Require an explicitly configured public bootstrap endpoint
  (`--bootstrap-public-endpoint` / `CORAL_BOOTSTRAP_PUBLIC_ENDPOINT`). If
  unset, fail fast with an actionable error instead of attempting
  rendezvous — do not infer dialability from RFD 023 STUN data (see
  High-Level Approach).
- Before the first publish, generate `write_token` (32 bytes, `crypto/rand`)
  and embed it in the plaintext payload alongside `endpoint`/`session_nonce`/
  `expires_at` (see Appendix) so it travels to the Colony only inside the
  PSK-encrypted record. Also send the raw `write_token` on the
  `PublishBootstrapRendezvousRequest` itself (Discovery stores only its
  hash — see §Data Flow, Rendezvous Write Capability); reuse the same value
  on every republish, since it identifies write authority over this one
  record for the life of the bootstrap attempt.
- Default `verify_reachability=false` on publish — Discovery accepts the
  record without probing, and misconfiguration surfaces via the 120s
  no-dial-back timeout below, not an immediate rejection. Add an opt-in
  `--verify-bootstrap-reachability` flag (default off) that sets
  `verify_reachability=true` and includes `probe_endpoint` for operators
  who want fail-fast validation and are willing to accept it's a
  quota-limited, best-effort diagnostic (§Security Model item 9), not a
  guarantee.
- If configured: open a listener, encrypt and publish the rendezvous
  record with a 90s TTL, and wait for the Colony to connect. **Republish**
  every 30s so the record never lapses while the Agent is still waiting —
  well inside the 90s TTL, giving margin even if a publish call is slow or
  transiently fails. Each republish passes the stored `record_id` and
  `write_token` (upsert, not a new row — see API Specifications) and
  **keeps the same `session_nonce`** (it identifies this bootstrap attempt
  across republishes, by design), but the `expires_at` in the plaintext
  payload changes on every republish, which means the plaintext changes —
  so **every republish MUST generate a fresh random 96-bit `gcm_nonce`**
  via `crypto/rand` and re-`Seal()` from scratch. Reusing a GCM nonce
  across two different plaintexts under the same derived key breaks
  AES-GCM's security guarantees; never reuse one, even across republishes
  of "the same" logical record. Give up after a **total wait budget of
  120s** from the first publish and fail with an actionable error
  distinguishing "no dial-back received" from `probe_failed` (only
  possible when `--verify-bootstrap-reachability` was set); 120s is
  generous relative to the long-poll pickup latency (§Data Flow, Phase 4 —
  typically sub-second once Colony's poll loop is running) rather than the
  old ~60s-heartbeat-driven latency this replaces. When reachability
  verification was requested, surface `probe_failed` responses to the
  operator with a specific error (e.g. "configured endpoint
  203.0.113.10:8444 is not reachable from the internet — check
  firewall/security-group rules").
- **Accept loop, not single-accept-then-close**: "single-use" describes the
  *listener's lifetime* (closes once bootstrap succeeds or its TTL expires),
  not a single accepted socket. It must keep calling `Accept()` and discard
  any connection that fails TLS validation — including Discovery's own
  reachability probe (§Security Model item 9), which connects but sends no
  TLS data by design. Treating the first accepted connection as authoritative
  would let the probe itself consume the listener before the real Colony
  ever dials in.
- **The accept loop must not block on a single connection's handshake.**
  Discovery's probe deliberately never sends a TLS ClientHello/ServerHello —
  if `Accept()` → `tls.Client(conn).Handshake()` runs inline and
  synchronously in the same loop that calls `Accept()`, that probe
  connection (or a port-scanner, or any other silent connection) hangs the
  handshake read forever with no deadline, and the loop never gets back to
  `Accept()` for the real Colony connection. Two concrete requirements:
  1. **Per-connection deadline**: call `conn.SetDeadline(time.Now().Add(5s))`
     immediately after `Accept()`, before starting the TLS handshake. A
     silent/incomplete handshake times out and the connection is closed and
     discarded, independent of whether Discovery's probe closes its side
     promptly (don't rely on the peer's behavior for local liveness).
  2. **Concurrent handling, bounded**: dispatch each accepted connection's
     TLS handshake + validation to its own goroutine (semaphore-bounded,
     e.g. max 8 in flight) rather than processing it inline in the `Accept()`
     loop, so the loop itself is always free to accept the next connection
     immediately. Discovery's probe connection is expected on every publish
     and every republish — this makes it a bounded 5s-and-forgotten cost,
     not a blocker.
- On an inbound connection whose TLS handshake and CA-fingerprint/SAN
  validation succeed (RFD 048, unchanged — Agent wraps the accepted `net.Conn`
  with `tls.Client(conn, cfg)`, per Data Flow § Rendezvous Session Binding),
  clear the deadline set above (the remaining exchange gets its own
  RequestCertificate-call timeout, not the handshake one) and add a thin
  adapter that issues the `RequestCertificate` call as an HTTP/2 client over
  this single already-established connection (rather than dialing a new one
  via the normal client transport), attaching `Coral-Rendezvous-Nonce:
  <nonce>` from the value generated when the record was published.
- **Listener port**: default to a new fixed, well-known port —
  `constants.DefaultAgentBootstrapPort = 8444` — rather than an OS-assigned
  ephemeral one. This mirrors the Colony's own `public_port` convention
  (default 8443, RFD 085): a fixed default means the operator opens one port
  in their security group/firewall once, permanently, instead of having to
  discover and re-open a different random port on every bootstrap attempt.
  A fixed default is safe here (no collision risk across runs) because the
  listener is single-use and TTL-bound — it's gone by the time a second
  bootstrap attempt could start. Allow override via `--bootstrap-listen-port`
  / `CORAL_BOOTSTRAP_LISTEN_PORT` only for operators who need a different
  port (e.g. 8444 already in use for something else). Log the bound port and
  the published endpoint at `INFO` (e.g. `bootstrap rendezvous listening
  port=8444 endpoint=203.0.113.10:8444`) so firewall/security-group issues
  are diagnosable without re-running with debug flags.
- **Not the same as the Agent's existing port 9001**: the Agent already has
  a fixed port (`constants.DefaultAgentPort = 9001`) for its mesh Connect/
  gRPC API (shell, container, debug, status, DuckDB proxy), but it can't be
  reused here — it's bound to the mesh IP (or localhost), never exposed on
  the public interface, and it only comes up once the Agent already has mesh
  membership, which is precisely what this RFD is trying to establish. 8444
  is a distinct, pre-identity, one-shot listener in a different trust
  domain, even though implementation-wise it could later share a mux
  pattern with 9001's server code.
- **`--bootstrap-listen-port` vs. `--bootstrap-public-endpoint`**: these are
  not the same thing and are not auto-derived from each other.
  `--bootstrap-listen-port` is what the Agent process binds locally
  (default 8444). `--bootstrap-public-endpoint` is what's dialable from the
  Colony's side of the internet, and is the value that gets published (and
  probed by Discovery). They only happen to be "the same port" when the
  Agent has a directly-attached public IP with no NAT in front of it — the
  common case (e.g. a bare VPS) this RFD targets:
  ```
  Direct public IP (no NAT):
    --bootstrap-listen-port=8444        # default, no override needed
    --bootstrap-public-endpoint=203.0.113.10:8444   # same port, agent's own public IP

  Agent behind DNAT/port-forward (e.g. private subnet + cloud NAT gateway,
  or a router forwarding an external port to the agent host):
    --bootstrap-listen-port=8444                     # local bind (default)
    --bootstrap-public-endpoint=203.0.113.10:9000     # external side of the
                                                        # operator-configured
                                                        # forwarding rule
  ```
  In the second case the operator is responsible for creating the
  port-forward/DNAT rule themselves (same requirement that exists today for
  the Colony's own `CORAL_PUBLIC_ENDPOINT`); this RFD doesn't attempt to
  configure NAT devices. By default a rule that isn't actually wired up
  correctly is caught by the 120s no-dial-back timeout, not at publish
  time — operators who want it caught earlier can pass
  `--verify-bootstrap-reachability`, which makes Discovery connect to
  whatever `--bootstrap-public-endpoint` says (§Security Model item 9)
  before accepting the publish.
- No changes to CSR generation, fingerprint validation, or the
  `RequestCertificate` call itself.

---

## Implementation Plan

### Phase 1: Protocol & Discovery Rendezvous RPCs

The Discovery Service backend runs as a Cloudflare Worker in the separate
`coral-mesh/discovery` repository (see `docs/DISCOVERY-WORKERS.md`), not in
this monorepo. Items below marked *(discovery repo)* are out of scope for
this repo and are tracked for that repo instead; everything else (the
protocol contract and the Go client) is implemented and tested here.

- [x] Add `PublishBootstrapRendezvous` / `PollBootstrapRendezvous` /
  `AckBootstrapRendezvous` to `discovery.proto` and regenerate code,
  including `write_token` and `verify_reachability` on
  `PublishBootstrapRendezvousRequest` and `write_token` on
  `AckBootstrapRendezvousRequest`. Go client wrapper methods added to
  `internal/discovery/client.go`.
- [ ] *(discovery repo)* Implement the TCP-connect reachability probe
  (private/loopback/link-local/metadata deny-list, 2s timeout,
  connect-only), gated behind `verify_reachability=true`.
- [ ] *(discovery repo)* Implement the Durable Object `bootstrap_rendezvous`
  table (including `write_token_hash`) and TTL cleanup in the Cloudflare
  Worker.
- [ ] *(discovery repo)* Implement `PollBootstrapRendezvous` as a long-poll
  (hold open up to ~25s server-side cap, resolve immediately on a matching
  publish); never include `write_token`/hash in the response.
- [ ] *(discovery repo)* Add per-`mesh_id` rate limiting on
  `PublishBootstrapRendezvous` and a separate limit on poll call volume.
- [ ] *(discovery repo)* Add global reachability-probe abuse controls
  (Security Model item 9).
- [ ] *(discovery repo)* Add `record_id` (ULID) and `write_token_hash` to
  the schema; implement upsert-on-publish and hash-checked, idempotent
  `AckBootstrapRendezvous`.
- [ ] *(discovery repo)* Unit tests for record storage, expiration, rate
  limiting, probe deny-list enforcement, and upsert/ack semantics.
  (This repo's tests exercise the Go client and Colony/Agent logic against
  an in-memory fake implementing the same contract — see Phase 4.)

### Phase 2: Colony Poll & Dial-Back
- [x] Derive rendezvous AEAD key(s) from active/grace PSK
  (`internal/rendezvous.DeriveKey`, `ca.Manager.ListValidPSKs`).
- [x] Implement the dedicated long-poll goroutine (independent of
  `internal/discovery/registration/manager.go`'s 60s `RegisterInterval`
  ticker), calling `PollBootstrapRendezvous(wait_seconds=25)`
  (`internal/colony/rendezvous.Dialer`).
- [x] Implement the per-`record_id` backoff/dedup map (exponential 2s/4s/8s,
  capped 15s) gating which returned records get decrypted/dialed this
  cycle, and the sleep-before-reissue when everything's still backed off.
- [x] Attempt decryption and dial-back to successfully decrypted endpoints,
  wrapping the outbound connection with `tls.Server()`.
- [x] Implement the `http2.Server{}.ServeConn()` transport adapter binding
  the existing `RequestCertificate` handler to a single dialed connection.
- [x] Validate `Coral-Rendezvous-Nonce` header against the decrypted
  `session_nonce` before evaluating referral ticket/PSK; reject and close
  on mismatch, leaving the record unacked.
- [x] Call `AckBootstrapRendezvous(mesh_id, record_id, write_token)`
  immediately on a successful `RequestCertificate` exchange, using the
  `write_token` decrypted for this dial attempt.
- [x] Unit tests for decrypt success/failure, PSK-rotation grace handling,
  nonce match/mismatch cases, backoff timing, and ack-on-success
  (`internal/colony/rendezvous/dialer_test.go`).

### Phase 3: Agent Listener & Publish Fallback
- [x] Add `--bootstrap-public-endpoint` / `CORAL_BOOTSTRAP_PUBLIC_ENDPOINT`
  config to `internal/agent/bootstrap/client.go`; fail fast with an
  actionable error if unset when direct dial fails.
- [x] Add `--verify-bootstrap-reachability` (default off) that sets
  `verify_reachability=true` on publish.
- [x] Add `constants.DefaultAgentBootstrapPort = 8444`.
- [x] Generate `write_token` (32 bytes, `crypto/rand`) before first publish;
  embed it in the encrypted payload and send it (raw) on
  `PublishBootstrapRendezvousRequest`; reuse the same value on every
  republish.
- [x] Implement the listener with a 90s record TTL, 30s republish interval
  (storing and reusing `record_id` and `write_token` for upsert; fresh
  `gcm_nonce` and stable `session_nonce` on every republish), and a 120s
  total wait budget before failing, defaulting to the fixed
  `DefaultAgentBootstrapPort` with `--bootstrap-listen-port` override and
  `INFO`-level port/endpoint logging.
- [x] Implement the accept loop: `SetDeadline(5s)` immediately after
  `Accept()`, TLS handshake + validation dispatched to a bounded pool of
  goroutines (semaphore, max 8), never processed inline. Clear the deadline
  once TLS + CA-fingerprint/SAN validation succeed.
- [x] Implement the HTTP/2 client-transport adapter that issues
  `RequestCertificate` over the already-accepted connection, attaching
  `Coral-Rendezvous-Nonce`.
- [x] Implement rendezvous record encryption and publish call, surfacing
  `probe_failed` (`ProbeFailedError`) as a distinct, actionable error when
  `--verify-bootstrap-reachability` was set.
- [x] Unit tests for listener lifecycle, encryption, nonce validation, the
  missing-config error path, and bounded concurrent connection handling
  (`internal/agent/bootstrap/rendezvous_test.go`).

### Phase 4: Verification & E2E Testing
- [x] Integration test simulating a Colony behind NAT (no discoverable
  endpoints) and a dialable remote Agent completing bootstrap via
  rendezvous end-to-end, using the real `Dialer` and the real
  `bootstrap.Client`, against an in-memory fake Discovery implementing the
  real Publish/Poll/Ack contract
  (`tests/integration/rendezvous/e2e_test.go`).
- [x] Test for PSK rotation grace period during rendezvous decryption
  (`TestDialerDecryptsRecordEncryptedUnderGracePSK`).
- [x] Test for "no public endpoint configured" fast-fail path
  (`TestRendezvousBootstrapFailsFastWithoutPublicEndpoint`).
- [ ] Integration test asserting end-to-end bootstrap latency stays in the
  low single-digit seconds when Colony's long-poll loop is already running
  at publish time. Not implemented — timing-based assertions are flaky in
  CI; the design (25s long-poll, immediate-return-on-existing-record) makes
  this true by construction and is implicitly exercised by the e2e test's
  own timeout margin.
- [x] Test verifying Discovery's own reachability probe (a connect-only,
  no-TLS-data connection) does not consume the Agent's listener before the
  real Colony connects (`TestRendezvousAcceptLoopSurvivesSilentConnection`).
- [x] Test for nonce mismatch: Colony connects with a stale/wrong nonce and
  is rejected without the referral ticket/PSK being evaluated
  (`TestDialerRejectsNonceMismatchWithoutInvokingHandler`).
- [x] Test asserting a successfully-fulfilled record is acked (verified via
  the e2e test's post-condition that the record is gone from the fake
  Discovery store, not left to linger for its TTL).
- [x] Test asserting a failed/undecryptable record does not trigger a dial
  attempt, and backoff timing is exponential and capped
  (`TestDialerDiscardsRecordThatFailsToDecrypt`,
  `TestBackoffDurationExponentialWithCap`,
  `TestDialerSkipsRecordStillInBackoffWindow`).
- [x] Test asserting a slow/silent connection to the Agent's listener
  (simulating Discovery's probe) does not delay the real Colony's
  connection from being accepted and handled
  (`TestRendezvousAcceptLoopSurvivesSilentConnection`).
- [ ] *(discovery repo)* Security regression test: a third party that only
  observes `PollBootstrapRendezvous` output cannot republish or ack a
  record. This repo's fake Discovery test double enforces the same
  hash-check contract, but the authoritative test belongs with the real
  Discovery Worker implementation.
- [ ] *(discovery repo)* Security regression test: `verify_reachability=false`
  never causes Discovery to open a connection to `probe_endpoint`.
- [ ] *(discovery repo)* Load/abuse test for reachability-probe quotas.
- [ ] Verify WireGuard mesh tunnel formation post-bootstrap. Not
  implemented — requires a real WireGuard-capable environment; the RFD's
  Manual Verification section covers this as a manual step.

---

## Verification Plan

### Automated Tests

```bash
# Unit tests for Discovery rendezvous RPCs
go test -v ./internal/discovery/... -run TestBootstrapRendezvous

# End-to-end integration test simulating NAT'd Colony + dialable Agent
go test -v ./tests/e2e/distributed/... -run TestNATBootstrapRendezvous
```

### Manual Verification
1. Run local Colony on workstation: `./bin/coral colony start` (no
   `CORAL_PUBLIC_ENDPOINT`, no port forwarding).
2. Run remote Agent on VPS with a security-group rule allowing the chosen
   port: `coral agent bootstrap --colony <MESH_ID> --fingerprint <FP> --psk <PSK> --bootstrap-public-endpoint <VPS_IP>:<PORT>`.
3. Verify bootstrap succeeds via rendezvous without SSH tunneling or port
   forwarding, and that Discovery logs show only ciphertext and a
   `write_token` hash for the record (PSK, CSR, certificate, and raw
   `write_token` never appear).
4. Verify mesh ping: `coral mesh ping`.
5. Verify the fail-fast paths: run the same Agent command with
   `--bootstrap-public-endpoint` unset (expect an immediate, actionable
   error, no listener opened); then repeat with
   `--verify-bootstrap-reachability` set and an endpoint the security group
   blocks (expect `probe_failed` surfaced clearly, not a silent timeout);
   then repeat without that flag against the same blocked endpoint (expect
   publish to succeed immediately and the failure to instead surface after
   the 120s no-dial-back timeout).
6. Verify the write-capability boundary: using the `mesh_id` from step 2,
   call `PollBootstrapRendezvous` directly (e.g. via `grpcurl`/`buf curl`)
   to observe a `record_id`, then attempt `AckBootstrapRendezvous` with
   that `record_id` and a fabricated `write_token` — expect
   `success=false` and confirm the real bootstrap in step 3 still
   completes normally afterward.

---

## Implementation Status

**Core Capability:** 🎉 Implemented

The PSK-encrypted rendezvous bootstrap flow is implemented end-to-end on the
Go side: proto contract, Discovery client, Colony dial-back, and Agent
listener/publish. Verified with unit tests plus a full in-process
integration test that drives the real `bootstrap.Client` against a real
`colonyrendezvous.Dialer` through an in-memory fake Discovery service
implementing the actual Publish/Poll/Ack contract (no mocks of the
rendezvous crypto or protocol logic itself).

The Discovery Service backend (Cloudflare Worker) that actually stores and
serves `bootstrap_rendezvous` records — including the reachability probe,
rate limiting, and abuse controls from Security Model item 9 — lives in the
separate `coral-mesh/discovery` repository, not this monorepo, and is
tracked there. This repo cannot merge that half of the RFD; it is written
and tested against the documented RPC contract and is ready to integrate
once that service implements it.

**Operational Components:**
- ✅ `PublishBootstrapRendezvous` / `PollBootstrapRendezvous` /
  `AckBootstrapRendezvous` added to `discovery.proto` and the Go Discovery
  client (`internal/discovery/client.go`).
- ✅ `internal/rendezvous`: HKDF-SHA256 key derivation and AES-256-GCM
  seal/open of the rendezvous payload, shared by Colony and Agent.
- ✅ `internal/colony/rendezvous.Dialer`: dedicated long-poll loop,
  per-`record_id` exponential backoff, PSK active/grace dual-key decrypt,
  `tls.Server()` dial-back, HTTP/2 transport adapter, nonce validation, and
  ack-on-success. Wired into Colony startup
  (`internal/cli/colony/server.go`).
- ✅ `internal/agent/bootstrap`: `--bootstrap-public-endpoint` fallback
  trigger, rendezvous listener with republish/backoff/TTL handling, bounded
  concurrent accept loop with per-connection handshake deadlines, and the
  HTTP/2 client-transport adapter for `RequestCertificate` over the
  dialed-back connection.
- ✅ CLI/config: `--bootstrap-public-endpoint`, `--verify-bootstrap-reachability`,
  `--bootstrap-listen-port` (and `CORAL_*` env equivalents) on both
  `coral agent bootstrap` and the `coral agent start` startup path.

**What Works Now:**
- An Agent with a configured `--bootstrap-public-endpoint` automatically
  falls back to PSK-encrypted rendezvous when direct dial to the Colony
  fails, and completes certificate bootstrap once the Colony dials back —
  verified end-to-end in
  `tests/integration/rendezvous/e2e_test.go:TestNATColonyDialableAgentBootstrapViaRendezvous`.
- PSK rotation grace period is honored on the Colony's decrypt path.
- A misconfigured/unreachable `--bootstrap-public-endpoint` fails fast if
  unset, or surfaces `ProbeFailedError` when `--verify-bootstrap-reachability`
  is set and the (not-yet-existing) Discovery probe would reject it.

**Integration Status:**
- Requires the `coral-mesh/discovery` Worker to implement the three new
  RPCs (schema, upsert/ack semantics, long-poll, rate limiting, and the
  opt-in reachability probe with its abuse controls) before this is usable
  against the real hosted Discovery service. Until then, this flow only
  works against a Discovery implementation that speaks the documented
  contract (e.g. the in-repo test fake, or a local development stand-in).

## Future Work

**Discovery Worker Implementation** (`coral-mesh/discovery` repo)
- This repo implements the RPC contract, Go client, and Colony/Agent logic
  against it; the actual Cloudflare Worker storage/rate-limiting/probe
  implementation lives in the separate `coral-mesh/discovery` repository and
  must be built there before this flow works end-to-end against the real
  hosted Discovery service.

**Deferred Test Coverage**
- End-to-end latency assertion (long-poll pickup stays low-single-digit
  seconds) — the design makes this true by construction; a timing-based CI
  assertion was judged more likely to be flaky than valuable.
- WireGuard mesh tunnel formation verification post-bootstrap — requires a
  real WireGuard-capable environment; covered as a manual verification step
  instead.
- Security regression tests for the write-capability boundary and the
  reachability-probe opt-in default belong with the Discovery Worker
  implementation itself, since they test that service's enforcement, not
  this repo's client/Colony/Agent code (which the in-repo fake Discovery
  double already exercises against the same contract).

**Double-NAT Fallback** (Future - separate RFD)
- Neither Colony nor Agent has a dialable endpoint. Requires either UDP
  hole-punching reused from the WireGuard/STUN layer (RFD 023/029) to
  establish connectivity before certificates exist, or a dedicated relay
  (TURN-like) that forwards bytes without seeing plaintext. Deliberately out
  of scope here to keep this RFD shippable.

**Automatic Endpoint Detection** (Future - separate RFD)
- This RFD requires the operator to explicitly set
  `--bootstrap-public-endpoint`. A real TCP-based reachability probe
  triggered from the Agent side (distinct from RFD 023's UDP STUN, which
  doesn't prove TCP dialability) could auto-detect a usable endpoint and
  pre-fill this flag. Deferred since explicit configuration is sufficient to
  ship the primary use case safely.

**Rendezvous Reuse for Certificate Renewal**
- Renewals already use mTLS over an established path and don't need this
  mechanism, but a persistently NAT'd Colony could in principle reuse the
  rendezvous channel for other outbound-only control operations. Not needed
  today.

---

## Appendix

### Rendezvous Payload (pre-encryption)

```json
{
  "endpoint": "203.0.113.10:8444",
  "session_nonce": "b64:9f8e...",
  "write_token": "b64:71c2...",
  "expires_at": "2026-08-08T14:26:00Z"
}
```

`session_nonce` is generated once, when the Agent first opens the listener,
and stays fixed across every republish of the same bootstrap attempt — it's
an application-level session token (later sent as the `Coral-Rendezvous-Nonce`
header, §Data Flow), not a cryptographic nonce, and reusing its *value*
across republishes is intentional and required (it's how Colony recognizes
"same session" across a republish's new `record_id` upsert). `write_token`
is likewise generated once and stays fixed across republishes — it's the
same value the Agent already sent Discovery on `PublishBootstrapRendezvous`
(§Data Flow, Rendezvous Write Capability); embedding it here is what lets a
Colony that successfully decrypts the record later call
`AckBootstrapRendezvous` itself. `expires_at` changes on every republish,
so the payload as a whole is a different plaintext each time.

### Key Derivation

```
rendezvous_key = HKDF-SHA256(
  secret = bootstrap_psk,
  salt   = mesh_id,
  info   = "coral-bootstrap-rendezvous-v1",
  length = 32
)

gcm_nonce = crypto/rand(12 bytes)   // fresh for EVERY Seal() call, no exceptions
ciphertext, tag = AES-256-GCM-Seal(rendezvous_key, gcm_nonce, payload_json)
```

Both Agent and Colony derive `rendezvous_key` independently from the shared
PSK; Discovery never has enough information to compute it. `gcm_nonce` MUST
be freshly random on every single `Seal()` call, including every republish —
never derive it from, or set it equal to, `session_nonce`, and never reuse a
`gcm_nonce` value with the same `rendezvous_key`. These are two unrelated
values that happen to both be called "nonce": `session_nonce` is
application-level and intentionally stable per session; `gcm_nonce` is
AES-GCM's per-encryption nonce and must never repeat under the same key.
