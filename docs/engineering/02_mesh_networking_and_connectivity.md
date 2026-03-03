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

## Multi-Platform Abstraction

Managing network interfaces varies significantly across operating systems.

- **Linux**: Direct integration with the kernel's WireGuard module via `link`
  and `addr` syscalls.
- **Darwin (macOS)**: Uses `wireguard-go` (userspace implementation) combined
  with `utun` devices. The abstraction in `internal/wireguard/interface.go`
  hides these complexities from the rest of the application.

## Note for the Future: STUN and P2P

For high-bandwidth inter-agent data transfer, the system could be enhanced with
**STUN/ICE** protocols to establish direct peer-to-peer tunnels, bypassing the
Colony hub and reducing latency.

## Related Design Documents (RFDs)

- [**RFD 007
  **: WireGuard Mesh Implementation](../../RFDs/007-wireguard-mesh-implementation.md)
- [**RFD 019
  **: Persistent IP Allocation](../../RFDs/019-persistent-ip-allocation.md)
- [**RFD 021**: CGNAT Address Space](../../RFDs/021-cgnat-address-space.md)
- [**RFD 023
  **: STUN Discovery & NAT Traversal](../../RFDs/023-stun-discovery-nat-traversal.md)
- [**RFD 029**: Colony-based STUN Server](../../RFDs/029-colony-based-stun.md)
- [**RFD 088**: Bootstrap PSK](../../RFDs/088-bootstrap-psk.md)
