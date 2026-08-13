---
rfd: "110"
title: "Relay-Free WireGuard Hole-Punch Bootstrap"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "023", "048", "049", "087", "088", "108", "109" ]
database_migrations: [ "udp_bootstrap_sessions table (session_nonce-keyed, phases: requested/allocated/connected/completed)" ]
areas: [ "agent", "colony", "discovery", "wireguard", "security" ]
---

# RFD 110 - Relay-Free WireGuard Hole-Punch Bootstrap

**Status:** Proposed

## Summary

RFD 110 makes bootstrap work for a Colony and Agent that cannot accept an
inbound TCP connection by relaying the bootstrap TLS stream through Discovery.
This RFD proposes a preferred direct alternative: establish a provisional
WireGuard path first, using coordinated UDP hole punching, then run the normal
bootstrap and enrollment RPCs over that path.

Discovery remains a control-plane rendezvous service. It never forwards
WireGuard, TLS, certificate, or application bytes. It returns the caller's
observed public **IP address** and stores only PSK-encrypted rendezvous
records. Each side uses an explicitly configured public UDP port (or an
explicit opt-in assumption that its NAT preserves the WireGuard listen port)
to construct candidates, and both sides send WireGuard handshakes at the same
time.

This avoids a relay when the two NATs permit UDP hole punching. It deliberately
does not claim to work through symmetric NAT, CGNAT, or UDP-blocking firewalls;
RFD 110 remains the fallback where a guaranteed bootstrap path is required.

## Problem

The RFD 108 direct rendezvous requires one party to accept an inbound TCP
connection. RFD 109 then completes mesh enrollment over that connection. When
both Colony and Agent are NAT-local, a TCP listener may not be reachable even
though both hosts can send outbound UDP traffic.

A fixed local WireGuard port does not by itself solve this. A NAT can map it to
a different public port, and an HTTPS request to Discovery observes a TCP
mapping rather than the WireGuard UDP mapping. Nevertheless, deployments with
a static UDP port forward or a port-preserving, endpoint-independent NAT have
enough information to attempt a direct path:

```text
public candidate = Discovery-observed public IP + known public UDP port
```

If both peers send authenticated WireGuard initiation packets to the other
candidate, their outbound packets create the NAT state needed for the reply to
arrive. WireGuard then learns the peer's actual endpoint from the authenticated
packet.

## Goals

- Bootstrap and enroll directly without relaying any byte stream through
  Discovery when UDP hole punching succeeds.
- Keep the Bootstrap PSK, referral ticket, CSR, certificate, and mesh control
  traffic opaque to Discovery.
- Use native WireGuard authentication for UDP packets; introduce no custom
  PSK-bearing UDP listener and no source-IP allowlist.
- Make candidate publication explicit and safe: Discovery may report an IP,
  but never invents or infers a public UDP port.
- Keep failure bounded and fall back cleanly to RFD 110 relay mode when the
  operator enabled it.

## Non-goals

- Guaranteed direct connectivity through symmetric NAT, CGNAT, or an
  enterprise firewall that blocks UDP.
- Learning an unknown UDP port from an HTTPS/Connect registration request.
- Running STUN in Cloudflare Workers or treating Discovery as a UDP relay.
- Replacing WireGuard's cryptographic peer validation with a PSK protocol or
  IP-based filtering.
- Long-lived relayed WireGuard data-plane connectivity.

## Design

### Candidate source and configuration

Add a small Discovery RPC:

```text
GetObservedAddress() -> { public_ip }
```

The service derives `public_ip` from the trusted edge-provided client-IP value.
For the Workers deployment this is the platform-provided client address (for
example, the validated `CF-Connecting-IP` value), never a user-supplied
forwarding header. The response intentionally has no port: it was observed on
an HTTP transport and is not evidence about UDP.

Each participant calls this RPC before publishing a UDP rendezvous record and
combines the returned IP with one of these explicit configurations:

| Configuration                                  | Candidate port meaning                                                                                |
| ---------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `CORAL_WIREGUARD_PUBLIC_PORT`                  | The operator knows this external UDP port is forwarded to the local WireGuard listener.               |
| `CORAL_HOLE_PUNCH_ASSUME_PORT_PRESERVING=true` | The local WireGuard listen port is used as a best-effort candidate; this is an opt-in NAT assumption. |

Without either setting, UDP hole punching is skipped. Discovery does not echo a
TCP source port, and the implementation must not silently assume that a local
listen port is public.

Candidates are `{ip, port, protocol: "udp"}`. Private, loopback, unspecified,
and multicast addresses are rejected before publication and before use.

### PSK-encrypted two-record rendezvous

RFD 108's encrypted record format gains `mode = "udp_hole_punch"`. The Agent
first obtains its observed IP, generates a random `session_nonce` and
`write_token`, and publishes this encrypted request record:

```text
{
  mode: "udp_hole_punch",
  role: "request",
  session_nonce,
  write_token,
  agent_id,
  agent_wireguard_public_key,
  agent_candidate: { ip, port, protocol: "udp" },
  referral_ticket,
  expires_at
}
```

The Colony's existing RFD 108 long-poll decrypts and authenticates the record
with the Bootstrap PSK. It validates the referral ticket and its Agent/Colony
claims against `agent_id` before allocating any state. The CSR remains on the
post-punch TLS enrollment request, where its identity is bound to those same
claims before a certificate is issued. The Colony obtains its own observed IP and
publishes a second, PSK-encrypted response record bound to the same nonce:

```text
{
  mode: "udp_hole_punch",
  role: "response",
  session_nonce,
  colony_wireguard_public_key,
  colony_candidate: { ip, port, protocol: "udp" },
  assigned_mesh_ip,
  mesh_subnet,
  expires_at
}
```

The Agent also polls Discovery while waiting for this response. It accepts only
a response whose `session_nonce` matches its request in constant time and whose
contents decrypt under the same PSK. Discovery sees neither payload.

The request and response use fresh record IDs and write tokens. Each token is
only valid for its own record; it must not be reused as a WireGuard or packet
secret. Both records expire quickly (default 90 seconds) and are acknowledged
only after the enrollment completes or the attempt is abandoned.

### Provisional enrollment state

The response needs to give the Agent an IP before the mesh is usable. Therefore
the RFD 109 order changes only for this mode:

1. Colony validates the PSK, referral ticket, expected Colony ID, Agent ID,
   and Agent WireGuard public key from the request. It does not consume the
   ticket or issue a certificate at this point.
2. Colony creates or resumes durable state keyed by `session_nonce`; it
   allocates a stable mesh IP but does **not** issue a certificate or consume
   the referral ticket yet.
3. Colony configures a provisional WireGuard peer using the Agent public key,
   allocated `/32`, candidate endpoint, and a short persistent keepalive.
4. Colony publishes the encrypted response with its peer key, candidate, mesh
   subnet, and allocated IP.
5. Agent configures its WireGuard address and a provisional Colony peer with
   the response values.

The durable state records the public keys, candidate values, allocation, and
phase. A retry with inconsistent identity or key material fails; a retry with
identical material resumes. Expired incomplete rows are reclaimed by the same
lease/phase rules as RFD 109.

This pre-allocation is authorized by the PSK and validated referral ticket but
is not mesh membership. The resulting peer is restricted to the assigned Agent
IP and Colony mesh/control addresses until the normal post-connect enrollment
finishes.

### Coordinated WireGuard handshakes

After the response is available, both peers start a bounded punch window:

1. At a rendezvous time in the response (with a small randomized jitter), both
   send WireGuard initiation packets to the other's candidate.
2. They retry at a short interval for 10 seconds and configure
   `PersistentKeepalive = 5` during the attempt.
3. A valid WireGuard response proves both the configured public key and an
   inbound UDP path. WireGuard updates the peer endpoint from the authenticated
   packet, so no separate endpoint-whitelisting mechanism is required.
4. On the first successful handshake, each side restores the normal keepalive
   policy and the Agent opens the existing mesh control-plane connection to the
   Colony over the tunnel.

The Agent calls `BootstrapAndRegister` over that mesh connection. The Colony
checks the durable session state and completes RFD 109's peer/registry update,
consumes the referral ticket, issues the certificate last, stores the completed
response, and acknowledges the rendezvous records.

WireGuard packets themselves are the probes. A second protocol sharing the
WireGuard UDP port is neither necessary nor desirable: it complicates socket
demultiplexing and duplicates WireGuard's authentication.

## Protocol Flow

```mermaid
sequenceDiagram
    participant A as Agent behind NAT
    participant D as Discovery (HTTPS only)
    participant C as Colony behind NAT

    A->>D: GetObservedAddress()
    D-->>A: Agent public IP
    A->>D: Publish encrypted UDP request (Agent IP + declared UDP port)
    C->>D: Poll encrypted rendezvous records
    D-->>C: UDP request
    C->>C: Validate PSK/ticket; allocate provisional mesh IP and peer
    C->>D: GetObservedAddress()
    D-->>C: Colony public IP
    C->>D: Publish encrypted UDP response (Colony candidate + allocated IP)
    A->>D: Poll response
    D-->>A: UDP response
    par Coordinated punch window
        A->>C: WireGuard initiation to Colony candidate
        C->>A: WireGuard initiation to Agent candidate
    end
    A-->>C: Authenticated WireGuard handshake succeeds
    A->>C: BootstrapAndRegister over mesh control plane
    C-->>A: Certificate and RegisterResponse over mesh
    C->>D: Acknowledge rendezvous records
```

## NAT Compatibility and Fallback

This is an opportunistic path, not a connectivity guarantee.

| Network condition                           | Expected result                                    |
| ------------------------------------------- | -------------------------------------------------- |
| Public IP or static UDP port forward        | Direct handshake should work.                      |
| Port-preserving, endpoint-independent NAT   | Direct handshake commonly works.                   |
| Restricted/cone NAT                         | Simultaneous outbound handshakes may work.         |
| Symmetric NAT or unknown external UDP ports | Candidate can be wrong; do not rely on this path.  |
| UDP blocked by policy                       | Fails; use relay or an operator-provided endpoint. |

If no handshake succeeds within the punch window, both sides remove the
provisional peer, preserve only retry-safe session state until its TTL, and
report `udp_hole_punch_failed`. The Agent may then publish a fresh RFD 110
relay-mode record when `--allow-relay-bootstrap` is enabled. The direct and
relay records are never mutated into one another.

## Security Considerations

- Discovery is untrusted for confidentiality and may lie about availability;
  it cannot read or forge PSK-encrypted records, TLS traffic, or WireGuard
  handshakes.
- Discovery's observed IP is a candidate hint, not authority to add a peer.
  The Colony adds a provisional peer only after PSK and referral-ticket
  validation; the Agent verifies the Colony WireGuard key against its trusted
  colony identity before sending bootstrap credentials.
- A known `session_nonce` is insufficient to inject a response: its encrypted
  body must validate under the PSK and its nonce must match.
- WireGuard public-key authentication, rather than source-address filtering,
  protects the UDP listener from spoofed probes. Rate-limit handshake attempts
  and retain WireGuard's normal anti-DoS behavior.
- Candidate publication can reveal public IP/port to the PSK holder. This is
  consistent with RFD 108 endpoint publication, but it must be documented and
  gated by explicit opt-in.
- Do not accept arbitrary `X-Forwarded-For` values at Discovery. Only the
  platform-authenticated client-IP signal is trusted.

## Configuration

```yaml
bootstrap:
  udp_hole_punch:
    enabled: true
    public_port: 51820              # Known external UDP forwarding port
    # Or, only when knowingly relying on NAT behavior:
    assume_port_preserving: false
    attempt_timeout: 10s
    relay_fallback: true
```

Equivalent environment variables:

```text
CORAL_UDP_HOLE_PUNCH_ENABLED=true
CORAL_WIREGUARD_PUBLIC_PORT=51820
CORAL_HOLE_PUNCH_ASSUME_PORT_PRESERVING=false
CORAL_UDP_HOLE_PUNCH_TIMEOUT=10s
CORAL_ALLOW_RELAY_BOOTSTRAP=true
```

`CORAL_WIREGUARD_PUBLIC_PORT` is an externally reachable port, not merely the
local bind port. Supplying it without a port-forward or a known port-preserving
NAT is a configuration error likely to result in timeout.

## Testing

- Unit-test record encryption, nonce binding, candidate validation, trusted
  client-IP extraction, state resumption, and provisional-peer restrictions.
- Run integration tests with two network namespaces and NAT fixtures for
  static forwarding, port-preserving NAT, restricted NAT, and intentional
  timeout.
- Verify that Discovery logs and storage never contain PSKs, tickets, CSRs,
  certificates, or raw WireGuard packets.
- Verify successful direct bootstrap issues exactly one certificate and leaves
  no provisional peer or rendezvous record behind.
- Verify failure removes the provisional peer and falls back to RFD 110 only
  when configured.

## Alternatives Considered

### Discovery byte relay (RFD 110)

The relay supports cases this proposal cannot, including hostile or symmetric
NATs, at the cost of operating a bounded byte-forwarding service. It remains
the fallback rather than the first choice when direct UDP is plausible.

### Embedded STUN responder on each peer

An embedded responder can report a source mapping only after a packet already
reaches the other peer. It cannot generate the initial candidate for two
unreachable hosts. Native WireGuard handshakes are better probes once
candidates exist, since they also authenticate the peer and establish the
needed mapping.

### Infer the UDP port from Discovery registration

The observed source port belongs to HTTP/TCP (or QUIC), not necessarily the
WireGuard UDP socket. Using it as a WireGuard endpoint would be unsound.

## Open Questions

- Whether a port-preservation assumption should be a configuration flag as
  proposed, or a separately detected/recorded NAT capability.
- Whether the Discovery IP-observation RPC should be a standalone method or a
  common field returned by all authenticated rendezvous methods.
- The exact temporary AllowedIPs required for the mesh control endpoint before
  full RFD 109 enrollment completes.
- Whether direct-punch metrics should record coarse NAT outcomes without
  retaining public endpoint history longer than the rendezvous TTL.
