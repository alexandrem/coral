---
rfd: "007"
title: "WireGuard Mesh Implementation"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: [ "001", "002" ]
database_migrations: [ ]
areas: [ "networking", "security", "infrastructure" ]
---

# RFD 007 - WireGuard Mesh Implementation

**Status:** 🎉 Implemented

## Summary

Implement the WireGuard-based encrypted mesh network layer using wireguard-go to
provide secure control plane connectivity between colonies and agents. This
fulfills the architectural design established in RFDs 001 and 002 by creating
the actual tunnel infrastructure for agent-colony communication.

## Problem

**Current behavior/limitations:**

- WireGuard mesh networking is architecturally designed but not implemented
- Only cryptographic primitives exist (key generation in
  `internal/auth/wireguard.go`)
- No tunnel creation, interface management, or peer coordination
- Configuration schema exists but unused (`WireGuardConfig` in
  `internal/config/schema.go`)
- Discovery service (RFD 001) and authentication layer (RFD 002) are blocked
  waiting for transport

**Why this matters:**

- **No actual connectivity**: Agents and colonies cannot communicate without the
  mesh layer
- **Architecture incomplete**: Control plane design assumes WireGuard but falls
  back to direct connections
- **Security gap**: Without WireGuard tunnels, traffic between agents and
  colonies is unencrypted
- **NAT traversal blocked**: WireGuard's built-in hole-punching and STUN
  integration needed for real-world deployments
- **Testing bottleneck**: Integration tests require manual network setup instead
  of automated mesh creation

**Use cases affected:**

- Agent-to-colony registration and telemetry reporting (blocked completely)
- Cross-environment deployments (agents in Kubernetes, colony on developer
  machine)
- Multi-region deployments where direct connectivity is impossible
- Security compliance requiring encrypted control plane traffic

## Solution

Implement userland WireGuard mesh networking using `wireguard-go` (pure Go
implementation) with automatic peer management, IP allocation, and integration
with existing discovery/auth layers.

**Key Design Decisions:**

- **wireguard-go over kernel module**: Pure Go implementation for portability
    - No kernel dependencies or elevated privileges required
    - Works consistently across macOS, Linux, Windows
    - Easier debugging and integration with Go codebase
    - Trade-off: Slightly lower performance than kernel WireGuard (acceptable
      for control plane)

- **Userland TUN interface via wireguard-conn**: Use wireguard-go's `conn.Bind`
  interface
    - Creates virtual network interface in userspace
    - Routes mesh traffic through WireGuard tunnel
    - Handles UDP/IP packet encapsulation automatically

- **Peer-to-peer mesh topology**: Every agent is a WireGuard peer of the colony
    - Colony maintains AllowedIPs for all registered agents
    - Agents only peer with their colony (star topology, not full mesh)
    - Simplifies routing and firewall rules
    - Colony can optionally relay traffic between agents if needed

- **Dynamic IP allocation**: Colony assigns mesh IPs from configured CIDR block
    - Default: `10.42.0.0/16` (IPv4), `fd42::/48` (IPv6)
    - Colony reserves `10.42.0.1` and `fd42::1` for itself
    - Agents receive assignments during registration (RFD 002
      `RegisterResponse`)
    - DuckDB tracks IP allocations to prevent conflicts

- **Integration with existing layers**:
    - Discovery service (RFD 001): Publishes WireGuard endpoints and public keys
    - Authentication (RFD 002): Colony secret verified after tunnel
      establishment
    - Proxy layer (`internal/proxy`): Routes gRPC calls over mesh IPs

**Benefits:**

- Enables end-to-end encrypted control plane communication
- NAT traversal using WireGuard's UDP hole-punching
- Automatic peer discovery and connection via RFD 001 integration
- Network isolation per colony (separate mesh subnet per colony_id)
- Production-ready: Same WireGuard protocol used by Tailscale, Mullvad VPN

**Architecture Overview:**

```
┌─────────────────────────────────────────────────────────────────┐
│  Agent (Kubernetes Pod: 10.0.1.42)                              │
│                                                                 │
│  1. Load colony_id, colony_secret from env (RFD 002)            │
│  2. Query discovery for colony endpoint (RFD 001)               │
│     └─> Returns: colony WireGuard pubkey + UDP endpoints        │
│  3. Create WireGuard interface (wg0)                            │
│     └─> Local port: 41580, Peer: colony pubkey                  │
│  4. Establish tunnel via UDP (NAT traversal)                    │
│  5. Send RegisterRequest to colony mesh IP (10.42.0.1:9090)     │
│     └─> Include: colony_secret, agent WireGuard pubkey          │
│  6. Receive RegisterResponse with assigned mesh IP (10.42.0.15) │
│  7. Add assigned IP to wg0 interface                            │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  WireGuard Interface (wg0)                               │   │
│  │  - Address: 10.42.0.15/16                                │   │
│  │  - Private key: [agent key]                              │   │
│  │  - Peer: [colony pubkey]                                 │   │
│  │    - Endpoint: colony-host:41580                         │   │
│  │    - AllowedIPs: 10.42.0.1/32 (colony only)              │   │
│  └──────────────────────────────────────────────────────────┘   │
│                 │                                               │
│                 │ Encrypted tunnel                              │
└─────────────────┼───────────────────────────────────────────────┘
                  │ UDP (port 41580)
                  │ NAT traversal via STUN
                  ▼
┌─────────────────────────────────────────────────────────────────┐
│  Colony (Developer Machine: 192.168.1.100)                      │
│                                                                  │
│  1. Load colony_id, WireGuard keys from config (RFD 002)        │
│  2. Create WireGuard interface (wg0)                             │
│     └─> Address: 10.42.0.1/16, Listen: 0.0.0.0:41580           │
│  3. Register with discovery (RFD 001)                            │
│     └─> Publish: pubkey, UDP endpoint (192.168.1.100:41580)    │
│  4. Accept RegisterRequest from agents                           │
│     └─> Verify colony_secret, agent pubkey                      │
│  5. Allocate mesh IP (10.42.0.15) for agent                      │
│  6. Add agent as WireGuard peer                                  │
│     └─> Peer pubkey, AllowedIPs: 10.42.0.15/32                  │
│  7. Send RegisterResponse with mesh assignment                   │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  WireGuard Interface (wg0)                               │   │
│  │  - Address: 10.42.0.1/16                                 │   │
│  │  - Private key: [colony key]                             │   │
│  │  - Listen: 0.0.0.0:41580                                 │   │
│  │  - Peers:                                                │   │
│  │    - Agent 1: [pubkey], AllowedIPs: 10.42.0.15/32       │   │
│  │    - Agent 2: [pubkey], AllowedIPs: 10.42.0.16/32       │   │
│  │    - Agent N: [pubkey], AllowedIPs: 10.42.0.X/32        │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

### Component Changes

1. **New Package: `internal/wireguard`**

    - **`device.go`**: WireGuard device lifecycle management
        - `NewDevice(config *WireGuardConfig)`: Create wireguard-go device
        - `Start()`: Bring up interface and start packet routing
        - `Stop()`: Tear down interface gracefully
        - `AddPeer(peerConfig *PeerConfig)`: Add/update WireGuard peer
        - `RemovePeer(publicKey string)`: Remove peer from mesh

    - **`interface.go`**: Network interface management
        - `CreateTUN(name string)`: Create userland TUN device
        - `AssignIP(ip net.IP, subnet *net.IPNet)`: Configure interface IP
        - `SetMTU(mtu int)`: Configure packet size (default: 1420 for WireGuard
          overhead)

    - **`peer.go`**: Peer management utilities
        - `PeerConfig`: Struct for peer metadata (pubkey, endpoint, allowedIPs)
        - `ParsePeerConfig()`: Validation and normalization

    - **`allocator.go`**: Mesh IP allocation
        - `NewIPAllocator(subnet *net.IPNet)`: Create allocator for CIDR block
        - `Allocate() net.IP`: Assign next available IP
        - `Release(ip net.IP)`: Return IP to pool
        - Uses DuckDB to persist allocations across restarts

2. **Colony** (`internal/colony`):

    - Add WireGuard device initialization in `Start()`
    - Implement peer management in agent registration handler
    - Update `RegisterAgent()` to allocate mesh IP and add WireGuard peer
    - Add peer removal when agent deregisters or times out
    - Publish WireGuard public key and endpoints to discovery service (RFD 001)

3. **Agent** (`internal/agent`):

    - Add WireGuard device initialization in agent startup
    - Query discovery for colony WireGuard info before connecting (RFD 001)
    - Create WireGuard tunnel with colony as peer
    - Include agent WireGuard public key in `RegisterRequest` (RFD 002)
    - Configure assigned mesh IP from `RegisterResponse`

4. **Discovery Integration** (`internal/discovery`):

    - Already implemented: Colony registers `pubkey` and `endpoints`
    - Update client to fetch WireGuard metadata during colony lookup
    - Add validation: Ensure pubkey is valid base64-encoded Curve25519 key

5. **Proxy** (`internal/proxy`):

    - Already configured to route to mesh IPs
    - Verify traffic flows over WireGuard tunnel (no changes needed)

**Configuration Example:**

```yaml
# ~/.coral/colonies/my-shop-production.yaml (from RFD 002)
wireguard:
    private_key: "gM2C9q1vZ... (base64)"
    public_key: "kL8fF3eYu... (base64)"
    port: 41580
    mesh_ipv4: "10.42.0.1"
    mesh_ipv6: "fd42::1"
    mesh_network_ipv4: "10.42.0.0/16"
    mesh_network_ipv6: "fd42::/48"
    mtu: 1420
    persistent_keepalive: 25  # Seconds, for NAT hole-punching
```

## Implementation Plan

### Phase 1: WireGuard Device Abstraction

- [ ] Add wireguard-go dependency to `go.mod`
- [ ] Create `internal/wireguard` package structure
- [ ] Implement `device.go`: Device creation and lifecycle
- [ ] Implement `interface.go`: TUN interface management
- [ ] Add unit tests for device initialization

### Phase 2: Peer Management

- [ ] Implement `peer.go`: Peer configuration structs
- [ ] Add peer add/remove/update operations in `device.go`
- [ ] Implement `allocator.go`: IP address allocation with DuckDB persistence
- [ ] Add unit tests for peer operations and IP allocation
- [ ] Add conflict detection for duplicate IPs

### Phase 3: Colony Integration

- [ ] Update colony startup to initialize WireGuard device
- [ ] Add mesh IP allocation during agent registration
- [ ] Implement peer addition in `RegisterAgent` handler
- [ ] Add peer removal on agent deregistration
- [ ] Update discovery registration to include WireGuard endpoints

### Phase 4: Agent Integration

- [ ] Update agent to query discovery for colony WireGuard info
- [ ] Implement agent WireGuard device initialization
- [ ] Add tunnel establishment before registration
- [ ] Include agent public key in `RegisterRequest`
- [ ] Configure assigned mesh IP from `RegisterResponse`

### Phase 5: NAT Traversal & Resilience

- [ ] Add UDP hole-punching support (WireGuard built-in)
- [ ] Implement persistent keepalive for NAT traversal
- [ ] Add endpoint discovery (STUN integration for public IP detection)
- [ ] Handle endpoint changes (colony moves behind different NAT)
- [ ] Add connection recovery logic

### Phase 6: Testing & Validation

- [ ] Unit tests for all wireguard package components
- [ ] Integration test: Colony + Agent on same host
- [ ] Integration test: Colony + Agent across NAT
- [ ] Integration test: Multiple agents with IP allocation
- [ ] E2E test: Full registration flow with mesh communication
- [ ] Performance test: Tunnel throughput and latency
- [ ] Stress test: 100+ agents connecting to single colony

## Testing Strategy

### Unit Tests

- WireGuard device creation and teardown (no network I/O)
- Peer configuration parsing and validation
- IP allocator: allocation, release, conflict detection
- Configuration loading and key validation
- DuckDB persistence of IP allocations

### Integration Tests

**Test 1: Local Mesh Formation**

```bash
# Start colony with WireGuard on localhost
coral colony start --test-mode

# Connect agent, verify tunnel established
coral connect test-agent

# Verify:
# - WireGuard interfaces created (wg0)
# - Agent assigned mesh IP (10.42.0.15)
# - Ping succeeds: agent -> colony mesh IP (10.42.0.1)
# - gRPC works over mesh
```

**Test 2: NAT Traversal Simulation**

```bash
# Use network namespaces to simulate NAT
# Colony in namespace A, agent in namespace B
# Verify UDP hole-punching establishes tunnel
```

**Test 3: Peer Lifecycle**

```bash
# Start colony
# Connect 3 agents
# Verify each gets unique mesh IP
# Disconnect agent 2
# Verify peer removed from colony
# Reconnect agent 2
# Verify same IP reused
```

### E2E Tests

**Scenario: Multi-Agent Registration**

1. Start colony
2. Connect 5 agents in parallel
3. Verify:
    - All agents get unique IPs
    - All tunnels established
    - Each agent can reach colony via mesh IP
    - Colony can list all connected peers

**Scenario: Cross-Region Connection**

1. Deploy colony on cloud VM (public IP)
2. Register with discovery service
3. Connect agent from local development machine (behind NAT)
4. Verify tunnel established via UDP hole-punching
5. Send telemetry data over tunnel

### Performance Tests

- **Throughput**: Measure gRPC request/response over WireGuard vs direct
    - Target: <5% overhead compared to direct connection
- **Latency**: Ping time through tunnel
    - Target: <2ms added latency on local network
- **Scale**: 100 concurrent agents
    - Target: All tunnels establish within 10 seconds
    - Target: <100MB memory overhead for colony

## Security Considerations

### WireGuard Security Properties

- **Encryption**: ChaCha20-Poly1305 authenticated encryption
- **Key exchange**: Noise protocol framework with Curve25519 DH
- **Perfect forward secrecy**: Keys rotated automatically
- **Identity hiding**: Minimal protocol metadata exposure
- **Resistance to attacks**: No known practical attacks against WireGuard
  protocol

### Colony-Specific Threats

**Threat: Unauthorized peer addition**

- **Mitigation**: Agent must provide valid colony_secret (RFD 002) before peer
  is added
- **Implementation**: Peer addition only after successful `RegisterRequest`
  authentication

**Threat: Mesh IP exhaustion**

- **Mitigation**: /16 subnet provides 65,534 IPs (exceeds realistic agent count)
- **Future**: Implement IP reclamation for long-disconnected agents

**Threat: Peer impersonation**

- **Mitigation**: WireGuard cryptographic identity based on public key
- **Verification**: Colony verifies agent public key matches registration

**Threat: Endpoint spoofing**

- **Mitigation**: WireGuard verifies all packets with HMAC
- **Additional**: Colony only accepts connections from registered peers

**Threat: DDoS via discovery service**

- **Mitigation**: Discovery publishes colony endpoints, but colony_secret still
  required
- **Rate limiting**: Colony limits registration attempts per source IP

### Operational Security

- **Private keys never transmitted**: Colony and agent private keys stay local
- **Public keys in discovery**: Safe to publish (RFD 001), only used for tunnel
  establishment
- **Mesh IPs non-routable**: RFC 1918 addresses (10.42.0.0/16) not exposed to
  internet
- **Firewall rules**: Only UDP port 41580 needs to be accessible (outbound
  typically sufficient)

## Migration Strategy

**New installations:**

1. `coral init` generates WireGuard keys (already implemented)
2. `coral colony start` creates WireGuard device automatically
3. `coral connect` establishes tunnel before registration

**Testing during development:**

- Feature flag: `--enable-wireguard` (default: false initially)
- Allows A/B testing: direct connection vs WireGuard mesh
- Fallback mechanism: If tunnel fails, attempt direct connection (with warning)

**Production rollout:**

1. **Phase 1**: WireGuard optional, direct connection default
2. **Phase 2**: WireGuard default, direct connection fallback
3. **Phase 3**: WireGuard required, remove direct connection support

**Backward compatibility:**

- RFD 001 discovery service unchanged (already includes `pubkey` field)
- RFD 002 `RegisterRequest` already includes `wireguard_pubkey` field
- Configuration schema already defined in `internal/config/schema.go`

## Future Enhancements

### Multi-Region Colonies

- Colony operates WireGuard endpoints in multiple regions (AWS us-east-1,
  eu-west-1)
- Discovery service returns region-aware endpoints
- Agents connect to nearest endpoint (latency-based selection)
- Backend storage remains centralized (mesh only for control plane)

### Agent-to-Agent Communication

- Currently: Star topology (agent ↔ colony only)
- Future: Full mesh (agent ↔ agent direct)
- Use case: Distributed tracing, cross-service debugging
- Implementation: Colony pushes full peer list to all agents

### WireGuard Config Export

```bash
# Export standard WireGuard config for manual setup
coral colony export-wireguard my-shop-prod > colony.conf

# Standard WireGuard tools can then connect
wg-quick up colony.conf
```

### Automatic MTU Discovery

- Detect path MTU to optimize packet size
- Prevents fragmentation on constrained networks
- Increases throughput on high-MTU networks (jumbo frames)

### QUIC over WireGuard

- Replace gRPC/HTTP2 with QUIC protocol
- Better performance on high-latency links
- Improved connection migration (important for mobile agents)

### IPv6-Only Mesh

- Default to IPv6 mesh addresses (`fd42::/48`)
- IPv4 as fallback for compatibility
- Prepares for IPv6-native deployments

## Appendix

### wireguard-go Package Details

**Library:** `golang.zx2c4.com/wireguard`

**Key Types:**

- `device.Device`: Main WireGuard device instance
- `conn.Bind`: Network binding interface (UDP sockets)
- `tun.Device`: TUN interface abstraction
- `ipc.UAPIListener`: Configuration API (compatible with wg(8) tool)

**Platform Support:**

- **Linux**: Full support, integrates with netlink
- **macOS**: Full support via utun interfaces
- **Windows**: Full support via Wintun driver
- **FreeBSD**: Full support

**Example Device Creation:**

```go
package wireguard

import (
    "golang.zx2c4.com/wireguard/device"
    "golang.zx2c4.com/wireguard/tun"
)

func NewDevice(config *WireGuardConfig) (*device.Device, error) {
    // Create TUN interface
    tunDev, err := tun.CreateTUN("wg0", device.DefaultMTU)
    if err != nil {
        return nil, fmt.Errorf("create TUN: %w", err)
    }

    // Create WireGuard device
    logger := device.NewLogger(device.LogLevelError, "wg0: ")
    wgDevice := device.NewDevice(tunDev, conn.NewDefaultBind(), logger)

    // Configure via UAPI (same format as wg(8) tool)
    if err := configureDevice(wgDevice, config); err != nil {
        return nil, fmt.Errorf("configure device: %w", err)
    }

    return wgDevice, nil
}

func configureDevice(dev *device.Device, config *WireGuardConfig) error {
    uapi := &strings.Builder{}
    fmt.Fprintf(uapi, "private_key=%s\n", config.PrivateKey)
    fmt.Fprintf(uapi, "listen_port=%d\n", config.Port)

    // Apply configuration
    return dev.IpcSet(uapi.String())
}
```

### IP Allocation Algorithm

**Subnet:** `10.42.0.0/16` (65,536 addresses)

**Reserved:**

- `10.42.0.0`: Network address (unusable)
- `10.42.0.1`: Colony address (fixed)
- `10.42.255.255`: Broadcast address (unusable)

**Available:** `10.42.0.2` - `10.42.255.254` (65,533 addresses)

**Allocation Strategy:**

1. **Sequential allocation**: Simplest, predictable
    - Agents get IPs in order: .2, .3, .4, ...
    - DuckDB tracks next available IP

2. **Released IP reuse**: Prevent fragmentation
    - Track released IPs in separate table
    - Allocate from released pool before incrementing counter

3. **Lease-based reclamation**: Handle disconnected agents
    - Mark IP as "inactive" after 24 hours of no heartbeat
    - Reclaim after 7 days inactive
    - Prevents exhaustion from abandoned agents

**DuckDB Schema:**

```sql
CREATE TABLE mesh_ip_allocations
(
    ip_address   VARCHAR PRIMARY KEY,
    agent_id     VARCHAR NOT NULL,
    allocated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_seen    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    status       VARCHAR   DEFAULT 'active' -- active, inactive, released
);

CREATE INDEX idx_agent_id ON mesh_ip_allocations (agent_id);
CREATE INDEX idx_status ON mesh_ip_allocations (status);
```

### NAT Traversal Details

**WireGuard NAT Traversal Mechanisms:**

1. **UDP Hole Punching**:
    - Colony listens on fixed port (41580)
    - Agent sends initial handshake from random source port
    - NAT creates mapping: agent:random → colony:41580
    - Colony's response travels back through same mapping

2. **Persistent Keepalive**:
    - Agent sends empty packets every 25 seconds
    - Keeps NAT mapping alive
    - Prevents connection timeout
    - Configured via `persistent_keepalive` parameter

3. **Endpoint Discovery**:
    - Agent queries STUN server for public IP
    - Reports endpoint to discovery service (RFD 001)
    - Colony receives endpoint, attempts direct connection
    - Falls back to relay if direct fails

**Example NAT Scenarios:**

**Scenario 1: Agent behind NAT, colony with public IP**

```
Agent (NAT: 192.168.1.100) → Router (Public: 203.0.113.42:12345)
                                          ↓
                              Colony (Public: 198.51.100.10:41580)

1. Agent sends handshake: 192.168.1.100:51234 → Colony
2. NAT translates: 203.0.113.42:12345 → Colony
3. Colony responds to: 203.0.113.42:12345
4. NAT routes back to: 192.168.1.100:51234
5. Tunnel established
```

**Scenario 2: Both behind NAT (symmetric NAT)**

```
Agent NAT (203.0.113.42) ←→ STUN Server (Public)
                      ↓
             Relay Server (if needed)
                      ↓
Colony NAT (198.51.100.10) ←→ STUN Server (Public)

1. Both query STUN for public endpoints
2. Both report endpoints to discovery service
3. Both attempt simultaneous connection (UDP hole punching)
4. If successful: Direct tunnel
5. If failed: Fall back to relay (future enhancement)
```

### Performance Characteristics

**Overhead:**

- **CPU**: ~1-2% at 100 Mbps throughput (userland vs kernel)
- **Memory**: ~5 MB per device + ~1 KB per peer
- **Latency**: <1ms added latency on local network, <5ms on internet
- **Throughput**: Line-rate on most connections (limited by CPU, not protocol)

**Comparison with Alternatives:**

| Solution             | Setup        | NAT Traversal | Encryption | Performance |
|----------------------|--------------|---------------|------------|-------------|
| WireGuard (this RFD) | Automatic    | Yes           | ChaCha20   | Excellent   |
| Direct TLS           | Manual       | No            | TLS 1.3    | Excellent   |
| OpenVPN              | Complex      | Yes           | AES        | Good        |
| IPsec                | Very complex | Difficult     | AES        | Good        |
| SSH Tunnel           | Manual       | Yes           | ChaCha20   | Fair        |

**Why WireGuard:**

- Best combination of security, performance, and ease of use
- Minimal configuration (public key exchange only)
- Modern cryptography (Noise protocol)
- Battle-tested (Linux kernel inclusion, widespread VPN adoption)

### Reference Implementations

- **Tailscale**: Commercial mesh VPN built on WireGuard
    - Inspiration: Coordination server (similar to Coral's discovery)
    - Reference: https://github.com/tailscale/tailscale

- **Netmaker**: Open-source WireGuard network manager
    - Reference: https://github.com/gravitl/netmaker

- **Headscale**: Open-source Tailscale control server
    - Reference: https://github.com/juanfont/headscale

### Testing Configuration Example

```yaml
# Test configuration for integration tests
wireguard:
    private_key: "test-private-key-base64"
    public_key: "test-public-key-base64"
    port: 51820  # Use different port for tests to avoid conflicts
    mesh_ipv4: "10.42.0.1"
    mesh_network_ipv4: "10.42.0.0/16"
    mtu: 1420
    persistent_keepalive: 5  # Shorter for faster test feedback

# Test-specific overrides
test:
    skip_interface_creation: false  # Set true for unit tests
    mock_discovery: true            # Use mock discovery server
    fast_keepalive: true            # 5s instead of 25s
```

---

## Notes

**Why Now:**

- Discovery service (RFD 001) and authentication (RFD 002) are complete
- Blocking: Cannot test end-to-end flows without mesh connectivity
- Risk: The longer we wait, the more we build workarounds (direct connections)
  that need to be removed later

**Implementation Complexity:**

- **High**: Network programming, platform-specific TUN interfaces, NAT traversal
- **Mitigated by**: wireguard-go handles most complexity, well-documented APIs

**Alternative Considered: Direct TLS Connections**

- **Pros**: Simpler, no WireGuard dependency
- **Cons**: No NAT traversal, no mesh abstraction, manual certificate management
- **Decision**: WireGuard aligns with original architecture, worth the
  complexity

**Relationship to Other RFDs:**

- **RFD 001**: Provides discovery for WireGuard endpoints
- **RFD 002**: Authentication happens after tunnel establishment
- **RFD 005**: Proxy routes to mesh IPs (implementation ready, waiting for mesh)
