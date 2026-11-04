# Mesh Networking

Coral gives an enrolled agent a stable overlay address that the Colony can use
for polling and control. WireGuard carries traffic between those addresses over
the peers' underlying network endpoints. The mesh address identifies where to
route internal traffic; the public endpoint identifies where the encrypted
packets must be sent now.

## Hub-and-spoke routing

The Colony is the hub for enrolled agent peers. Agents configure the Colony as
their peer and initiate traffic toward its discovered endpoint. Keepalives can
preserve an established NAT mapping, but this arrangement does not guarantee
connectivity through every NAT or firewall. Endpoint discovery and a usable
packet path are both required.

`internal/wireguard` separates device and interface management from higher-level
mesh configuration. Linux and Darwin need different interface handling; the
rest of the agent and Colony uses the shared device abstraction. See the
[WireGuard implementation](../../internal/wireguard/) and
[interface abstraction](../../internal/wireguard/interface.go).

## Persistent address allocation

The default IPv4 mesh range is `100.64.0.0/10`, with the Colony at `100.64.0.1`.
This shared address space reduces overlap with common private subnets, but
operators still need to consider conflicts with existing routes. IPv6 defaults
are defined alongside IPv4 in [constants](../../internal/constants/constants.go).

The Colony persists agent IP allocations so a known agent identity can recover
its assigned address. The lease is a durable allocation decision; the running
WireGuard peer configuration is separate state. Restart recovery must restore
both the authoritative identity-to-address relationship and the device's peer
entry. [Compound enrollment](discovery-and-enrollment.md#durable-compound-enrollment)
explains that recovery boundary.

## Bootstrap before the mesh exists

Initial certificate bootstrap cannot depend on an agent's final overlay route.
The agent first tries the Colony's direct TLS endpoint. If the Colony cannot
accept that connection, reverse-dial rendezvous lets it open TCP to a temporary
agent listener while remaining the TLS server. The certificate identity checks
are unchanged by the direction of TCP establishment.

The fallback derives the agent's public TCP address from Discovery-confirmed
address information and the bootstrap listen port, with an explicit public
endpoint override. UDP STUN discovery does not establish TCP reachability. The
fallback supports a publicly dialable agent; it is not a WireGuard packet relay
and does not solve two peers that both lack an inbound path.

The full protocol, retry bounds, and correlated logging have their canonical
explanation in [Discovery and enrollment](discovery-and-enrollment.md#reverse-dial-rendezvous).
[Identity and trust](identity-and-trust.md) explains the distinction between
network reachability and admission to the mesh.

## Measuring the overlay path

The agent's mesh ping uses the WireGuard interface to measure the overlay path.
This differs from pinging a public endpoint: it includes the routing and
encapsulation path used by internal mesh traffic. A successful ping demonstrates
that path at the time of measurement, not the health of every RPC or collector.

Implementation: [mesh ping](../../internal/agent/mesh_ping.go).
Procedures live in [WireGuard troubleshooting](../WIREGUARD_TROUBLESHOOTING.md).

## Design history

- [RFD 007: WireGuard Mesh](../../RFDs/007-wireguard-mesh-implementation.md)
- [RFD 019: Persistent IP Allocation](../../RFDs/019-persistent-ip-allocation.md)
- [RFD 021: CGNAT Address Space](../../RFDs/021-cgnat-address-space.md)
- [RFD 023: STUN and NAT Traversal](../../RFDs/023-stun-discovery-nat-traversal.md)
- [RFD 029: Colony STUN Server](../../RFDs/029-colony-based-stun.md)
- [RFD 108: PSK Rendezvous](../../RFDs/108-psk-rendezvous-agent-bootstrap.md)
