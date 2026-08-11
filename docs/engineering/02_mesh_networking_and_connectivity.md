# Mesh Networking: The WireGuard Overlay

Coral leverages a Software-Defined Network (SDN) approach to unify heterogeneous
environments into a single, secure L3 address space.

## WireGuard Overlay (`internal/wireguard`)

The system builds an encrypted **Overlay Network** using WireGuard. Overlaying
allows the Colony to maintain a stable connection to Agents regardless of their
underlying physical network (e.g., AWS, On-prem, Home office).

- **NAT Traversal**: By having Agents (the "spokes") initiate connections to the
  Colony (the "hub"), the mesh naturally traverses NAT and stateful firewalls
  without requiring inbound port forwarding on the target nodes.

## IP Address Management (IPAM)

A critical requirement for distributed communication is stable identification.

- **CGNAT Address Space**: Coral allocates IPs from the `100.64.0.0/10` range (
  Shared Address Space, RFC 6598). This avoids collisions with common private
  network ranges like `192.168.x.x` or `10.x.x.x`.
- **Persistent Lease**: The Colony's `ip_allocations` table ensures that an
  Agent ID always receives the same Mesh IP. This persistence is vital for
  long-running telemetry sessions and debugging, where the Colony must
  re-establish contact with the same logical node after an agent restart.

## Connectivity & Mesh Topology

- **Hub-and-Spoke**: Currently, the architecture is a Hub-and-Spoke model where
  the Colony is the primary router.
- **Mesh Ping (`internal/agent/mesh_ping.go`)**: To measure "real" inter-node
  latency, Coral implements a specialized ping that routes traffic precisely
  over the WireGuard interface, providing a true reflection of the overlay's
  performance overhead.

## Bootstrap Connectivity When the Colony Is Not Dialable

WireGuard NAT traversal does not solve the initial HTTPS certificate bootstrap:
at that point an Agent has no mesh identity and cannot rely on its UDP
WireGuard mapping to reach a Colony. RFD 108 adds a reverse-dial fallback for
the common local-first topology: a Colony behind NAT and an Agent with a
publicly reachable TCP endpoint.

At startup, both peers bind fixed WireGuard UDP ports, discover their observed
addresses through STUN, and register those addresses with Discovery. The Agent
first attempts ordinary direct bootstrap using the Colony record. If that
fails, `internal/agent/bootstrap` enters rendezvous mode and derives the
Agent's temporary TCP endpoint from the Discovery-confirmed public IP plus
port `8444`. `--bootstrap-public-endpoint` /
`CORAL_BOOTSTRAP_PUBLIC_ENDPOINT` remains an override for load balancers and
NAT rules whose external TCP address or port differs from that default. A UDP
STUN observation cannot prove that TCP `8444` is reachable.

`internal/colony/rendezvous` independently long-polls Discovery and dials the
decrypted Agent endpoint. This loop is deliberately separate from Colony
registration heartbeats: a 60-second registration cadence would consume most
of a 90-second rendezvous-record lifetime. The TCP initiator changes, but TLS
roles do not: the Colony runs `tls.Server` over its outbound connection and
the Agent remains the TLS client. Consequently, the existing Root-CA
fingerprint and Colony SPIFFE-SAN validation are unchanged.

This is a bootstrap-only transport adaptation, not a WireGuard relay or a
general reverse proxy. It covers deployments where the Agent is publicly
dialable on the inferred or explicitly configured TCP endpoint. Two NAT-bound
peers without an inbound path remain out of scope.

### Reverse-Dial Observability

Colony logs model reverse dial as one correlated lifecycle. Before the Agent is
authorized, `record_id` is the only request identity. After referral-ticket,
PSK, CSR, and registration identities agree, enrollment logs also include
`agent_id`, the durable RFD 109 `phase`, and the assigned `mesh_ip`.

The principal milestones are Discovery record receipt, dial attempt, TCP and
TLS establishment, authenticated RPC routing, endpoint selection, WireGuard
peer mutation, certificate issuance, enrollment completion, and Discovery
acknowledgement. Failures retain the same `record_id`; enrollment failures add
a `failure_class` derived from the last durable phase. This makes a retry or
crash-resume distinguishable from a new attempt.

The decrypted rendezvous TCP endpoint, session nonce, write token, Bootstrap
PSK, CSR, certificate contents, and private keys are never logged. Dial errors
redact the decrypted endpoint. The Agent WireGuard UDP endpoint selected from
Discovery may be logged after authorization because it is operational peer
configuration, not rendezvous plaintext.

## Multi-Platform Abstraction

Managing network interfaces varies significantly across operating systems.

- **Linux**: Direct integration with the kernel's WireGuard module via `link`
  and `addr` syscalls.
- **Darwin (macOS)**: Uses `wireguard-go` (userspace implementation) combined
  with `utun` devices. The abstraction in `internal/wireguard/interface.go`
  hides these complexities from the rest of the application.

## Future Engineering Note

For high-bandwidth inter-agent data transfer, the system could be enhanced with
**STUN/ICE** protocols to establish direct peer-to-peer tunnels, bypassing the
Colony hub and reducing latency.

## Related Design Documents (RFDs)

- [**RFD 007**: WireGuard Mesh Implementation](../../RFDs/007-wireguard-mesh-implementation.md)
- [**RFD 019**: Persistent IP Allocation](../../RFDs/019-persistent-ip-allocation.md)
- [**RFD 021**: CGNAT Address Space](../../RFDs/021-cgnat-address-space.md)
- [**RFD 023**: STUN Discovery & NAT Traversal](../../RFDs/023-stun-discovery-nat-traversal.md)
- [**RFD 029**: Colony-based STUN Server](../../RFDs/029-colony-based-stun.md)
- [**RFD 088**: Bootstrap PSK](../../RFDs/088-bootstrap-psk.md)
- [**RFD 108**: PSK-Encrypted Rendezvous for NAT-Traversing Agent Bootstrap](../../RFDs/108-psk-rendezvous-agent-bootstrap.md)
