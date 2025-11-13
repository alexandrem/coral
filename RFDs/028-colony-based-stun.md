---
rfd: "028"
title: "Colony-Based STUN for Symmetric NAT Traversal"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: ["023"]
database_migrations: []
areas: ["networking", "discovery", "security"]
---

# RFD 028 - Colony-Based STUN for Symmetric NAT Traversal

**Status:** 🚧 Draft

## Summary

Enhance NAT traversal by embedding a STUN server within colonies to discover destination-specific port mappings created by symmetric NAT. This solves the fundamental limitation of RFD 023's public STUN approach, where the discovered port only works for the STUN server's IP, not the colony's IP. By having agents query the actual colony they'll connect to, we obtain the correct NAT mapping that will route WireGuard packets successfully.

## Problem

- **Current behavior/limitations**:
  - RFD 023 Phase 1 uses public STUN servers (Cloudflare, Google) to discover agent public endpoints
  - With symmetric NAT (very common in corporate networks, mobile carriers, CGNAT), each destination receives a different source port mapping
  - Agent discovers port `54321` when talking to `stun.cloudflare.com`, but symmetric NAT assigns port `54322` when talking to the colony's IP
  - Colony attempts to connect to the STUN-discovered port `54321`, but packets are dropped because the NAT has no mapping for that combination
  - Current code acknowledges this with `SO_REUSEPORT` to share ports, but this doesn't solve symmetric NAT and creates race conditions (documented in RFD 023)

- **Why this matters**:
  - Symmetric NAT is increasingly prevalent: AWS VPCs, corporate networks, mobile carriers (4G/5G), and CGNAT all commonly use symmetric NAT
  - Without solving this, RFD 023's STUN implementation only works reliably with cone NAT (less common today)
  - Users experience intermittent or complete connection failures in production environments
  - Current workaround requires VPN or port forwarding, negating the self-hosted value proposition

- **Use cases affected**:
  - Agent on AWS EC2 (symmetric NAT) connecting to home colony behind residential ISP
  - Mobile agents on cellular networks (CGNAT + symmetric NAT) connecting to cloud colony
  - Corporate network agents (symmetric NAT + firewall) connecting to remote colonies
  - Any scenario where agent is behind symmetric NAT and attempts direct connection

## Solution

Embed a lightweight STUN responder in every colony and optionally in the discovery service itself. Agents query the specific colony they intend to connect to, discovering the exact port mapping that symmetric NAT created for that destination. This eliminates the destination-mismatch problem and opens the NAT hole as a side effect.

**Key Design Decisions:**

- **Colony-side STUN**: Each colony runs a STUN server on the same UDP port as WireGuard, eliminating port-restricted cone NAT issues
- **Single socket with SO_REUSEPORT on colony only**: Colony needs port sharing between STUN and WireGuard; agents don't need SO_REUSEPORT, simplifying agent code
- **Fast handshake path**: Agent immediately sends WireGuard handshake after STUN to prevent NAT mapping expiration (30-60 second timeout)
- **Discovery service STUN optional**: Discovery can also answer STUN for general IP discovery, but colony STUN is the critical path
- **Backwards compatible**: Falls back to public STUN if colony doesn't support embedded STUN

**Benefits:**

- Correct port discovery for symmetric NAT environments (majority of production networks)
- NAT hole punching as side effect (STUN request itself opens the hole)
- Eliminates race conditions on agent side (no SO_REUSEPORT needed)
- Reduces external dependencies (no reliance on public STUN servers)
- Lower latency (single STUN round-trip to actual destination vs. multiple to public servers)
- Enables direct connections in environments where RFD 023 currently fails

**Architecture Overview:**

```
┌─────────────────────────────────────────────────────────────────┐
│ Symmetric NAT                                                   │
│                                                                 │
│  Agent (local :51821)                                           │
│       │                                                         │
│       │ 1. STUN Request to Colony IP:41820                      │
│       ├─────────────────────────────────────────────────┐       │
│       │  NAT creates mapping:                           │       │
│       │  Agent:51821 → Colony:41820 = Public:54322      │       │
│       │                                                 │       │
└───────┼─────────────────────────────────────────────────┼───────┘
        │                                                 │
        │                                                 ▼
        │                                         ┌──────────────┐
        │                                         │   Colony     │
        │                                         │              │
        │                                         │ STUN Server  │
        │ 2. STUN Response:                       │ (port 41820) │
        │    "I see you at 203.0.113.45:54322" ◄──┤    +         │
        │                                         │ WireGuard    │
        │                                         │ (port 41820) │
        │ 3. WireGuard Handshake (immediate)      │              │
        ├─────────────────────────────────────────►              │
        │    NAT hole still open (< 30s)          │              │
        │                                         │              │
        │ 4. WireGuard Response                   │              │
        ◄─────────────────────────────────────────┤              │
        │    ✅ Symmetric NAT routes correctly    │              │
        │                                         └──────────────┘

Public STUN (stun.cloudflare.com) would have discovered wrong port (54321)
Colony STUN discovers correct port (54322) for Colony's IP
```

## Component Changes

1. **Colony**
   - Add STUN responder that listens on same UDP port as WireGuard (requires SO_REUSEPORT)
   - Parse incoming UDP packets: WireGuard handshakes go to WireGuard, STUN requests go to STUN handler
   - Respond to STUN Binding Requests with XOR-MAPPED-ADDRESS containing observed source IP:port
   - Configuration: `--enable-stun-server` (default true), `--stun-on-separate-port` (default false)
   - Metrics: STUN requests received, STUN responses sent, malformed packets

2. **Agent**
   - Remove SO_REUSEPORT from agent side (simplification)
   - Add colony STUN discovery phase before WireGuard connection
   - Query colony's IP:port with STUN Binding Request from WireGuard socket (no separate socket needed)
   - Immediately send WireGuard handshake initiation after receiving STUN response (< 5 seconds to prevent NAT timeout)
   - Fallback: if colony STUN fails, try public STUN servers (current behavior)
   - Configuration: `--prefer-colony-stun` (default true)

3. **Discovery Service (Optional Enhancement)**
   - Add STUN responder for general IP discovery
   - Agents can query discovery before knowing specific colony to connect to
   - Helps with initial registration but doesn't solve symmetric NAT for colony connections

4. **WireGuard Bind Layer**
   - Colony bind needs packet demultiplexing: check first byte to distinguish STUN (0x00 or 0x01) from WireGuard (0x01-0x04)
   - STUN packets dispatched to STUN handler
   - WireGuard packets handled by existing bind
   - Agent bind simplified (remove SO_REUSEPORT logic)

**Configuration Example:**

```yaml
# Colony configuration
colony:
  wireguard:
    port: 41820
    stun_server:
      enabled: true              # Embed STUN on WireGuard port
      separate_port: false       # Don't use separate port (avoids port-restricted NAT issues)
      max_requests_per_min: 60   # Rate limiting

# Agent configuration
agent:
  wireguard:
    port: 51821
    prefer_colony_stun: true     # Try colony STUN before public STUN
    stun_timeout: 3s             # Timeout per STUN server
    handshake_delay: 2s          # Max delay after STUN before WireGuard handshake
```

## Implementation Plan

### Phase 1: Colony STUN Responder

- [ ] Implement STUN Binding Request parser in `internal/wireguard/stun_server.go`
- [ ] Add packet demultiplexer to colony bind (STUN vs WireGuard)
- [ ] Implement STUN response with XOR-MAPPED-ADDRESS
- [ ] Add colony configuration flags for STUN server
- [ ] Unit tests for STUN parsing and response generation

### Phase 2: Agent Colony-STUN Client

- [ ] Modify agent STUN discovery to query colony IP first
- [ ] Remove SO_REUSEPORT from agent bind implementation
- [ ] Add immediate WireGuard handshake after STUN response
- [ ] Implement fallback to public STUN if colony STUN fails
- [ ] Add timeout and retry logic

### Phase 3: Discovery Service STUN (Optional)

- [ ] Add STUN responder to discovery service
- [ ] Update `RegisterAgent` flow to use discovery STUN for initial IP discovery
- [ ] Keep colony STUN as primary for actual connections

### Phase 4: Testing & Validation

- [ ] Unit tests for packet demultiplexing
- [ ] Integration tests with simulated symmetric NAT (Docker + iptables)
- [ ] E2E tests with agent behind symmetric NAT connecting to colony
- [ ] Performance tests (STUN overhead on WireGuard throughput)
- [ ] Verify NAT mapping preservation (handshake within timeout)

### Phase 5: Documentation & Migration

- [ ] Update NAT traversal documentation with symmetric NAT support
- [ ] Migration guide from SO_REUSEPORT agent implementation
- [ ] Configuration best practices (when to use separate STUN port)
- [ ] Troubleshooting guide for NAT-related connection failures

## API Changes

### New Protobuf Messages (Optional Enhancement)

```protobuf
// Extension to LookupColonyResponse to advertise STUN capability
message LookupColonyResponse {
    // ... existing fields ...
    bool stun_enabled = 20;
    uint32 stun_port = 21;  // If different from WireGuard port (rare)
}

// Extension to RegisterColonyRequest to advertise STUN capability
message RegisterColonyRequest {
    // ... existing fields ...
    bool stun_enabled = 20;
}
```

### CLI Commands

```bash
# Colony with embedded STUN (default)
coral colony start --wg-port=41820 --enable-stun

# Colony with STUN on separate port (if port-restricted NAT not a concern)
coral colony start --wg-port=41820 --stun-port=3478

# Agent with colony-based STUN (default)
coral agent start --wg-port=51821

# Agent forcing public STUN (fallback mode)
coral agent start --wg-port=51821 --prefer-public-stun

# Discovery service with STUN
coral discovery start --enable-stun --stun-port=3478
```

### Configuration Changes

- New colony config: `wireguard.stun_server.enabled` (bool, default true)
- New colony config: `wireguard.stun_server.separate_port` (bool, default false)
- New colony config: `wireguard.stun_server.rate_limit` (int, default 60/min)
- New agent config: `wireguard.prefer_colony_stun` (bool, default true)
- New agent config: `wireguard.handshake_delay` (duration, default 2s)

## Testing Strategy

### Unit Tests

- STUN Binding Request parsing (RFC 5389 compliance)
- XOR-MAPPED-ADDRESS encoding (RFC 5389 Section 15.2)
- Packet demultiplexing (STUN vs WireGuard identification)
- Rate limiting enforcement
- Timeout and retry logic

### Integration Tests

- **Symmetric NAT simulation**: Use Docker containers with iptables SNAT rules to create symmetric NAT
  - Verify different ports allocated for different destinations
  - Confirm colony STUN discovers correct port
  - Validate WireGuard connection succeeds
- **Public STUN fallback**: Disable colony STUN, verify agent falls back to Cloudflare STUN
- **NAT timeout handling**: Delay WireGuard handshake by 40 seconds (typical NAT timeout), verify failure, verify success with < 30s delay
- **Port-restricted cone NAT**: Verify STUN and WireGuard on same port avoids restriction

### E2E Tests

- Agent on AWS (symmetric NAT) connecting to local colony
- Agent on simulated mobile network (CGNAT) connecting to cloud colony
- Colony behind symmetric NAT (VPC) with agent on public internet
- Measure connection success rates: current implementation vs colony STUN

## Security Considerations

- **STUN as amplification vector**: STUN responses are larger than requests; rate limiting per source IP prevents amplification attacks
- **Information disclosure**: STUN reveals observed IP:port to requester, but this is necessary for NAT traversal; no authentication required (by design)
- **DoS on colony**: Rate limiting (default 60 requests/min per source IP) prevents STUN floods from impacting WireGuard
- **Malformed packet handling**: Strict parsing with early rejection of invalid STUN messages to prevent CPU exhaustion
- **WireGuard authentication unchanged**: STUN is pre-authentication; actual connection still requires valid WireGuard keys
- **No encryption on STUN**: STUN messages are plaintext (per RFC 5389), but contain no sensitive data

## Performance Considerations

- **Latency**: Colony STUN adds one round-trip (typically 20-200ms) before WireGuard handshake begins
- **CPU overhead**: STUN parsing is lightweight (~10µs per request); packet demultiplexing adds ~1µs per packet
- **Memory overhead**: No state maintained for STUN requests (stateless responses)
- **Bandwidth**: STUN request ~28 bytes, response ~32 bytes; negligible compared to WireGuard handshake
- **Concurrency**: STUN handler shares goroutine pool with WireGuard; rate limiting prevents resource exhaustion

## Migration Strategy

### For New Deployments

1. Deploy colonies with `--enable-stun` (default behavior)
2. Deploy agents with default configuration (colony STUN automatically used)
3. No additional configuration needed

### For Existing Deployments

1. **Backwards compatible**: Colonies without STUN support continue working with public STUN
2. **Phased rollout**:
   - Update colonies first (adds STUN capability but doesn't break existing agents)
   - Update agents to use colony STUN (automatically detects capability)
   - Monitor metrics: STUN success rates, connection success rates
3. **Rollback**: Disable colony STUN with `--enable-stun=false` to revert to public STUN behavior

### Configuration Migration

**Before (RFD 023 - Public STUN only):**
```bash
coral agent start --wg-port=51821 --stun-servers=stun.cloudflare.com:3478
```

**After (RFD 028 - Colony STUN preferred):**
```bash
coral agent start --wg-port=51821
# Colony STUN automatically discovered via LookupColony response
# Falls back to public STUN if colony doesn't support it
```

## Alternatives Considered

### Alternative 1: Coordinated Hole Punching (RFD 023 Phase 2)

- **Approach**: Discovery service coordinates simultaneous sends from both peers
- **Pros**: Works without colony STUN server; distributed relay not needed
- **Cons**:
  - Requires complex coordination (both sides must send at exact same time)
  - Doesn't work if colony is offline during coordination
  - Adds latency (coordination round-trip)
  - Symmetric NAT port still discovered via public STUN (wrong port problem remains)
- **Decision**: Colony STUN is simpler and more reliable; pursue as primary solution

### Alternative 2: Always Use Relay (RFD 023 Phase 3)

- **Approach**: Skip NAT traversal, always route through TURN relay
- **Pros**: Guaranteed connectivity; no NAT detection needed
- **Cons**:
  - Adds relay infrastructure cost and complexity
  - Increased latency (extra hop)
  - Bandwidth costs for relay operator
  - Defeats purpose of peer-to-peer mesh
- **Decision**: Use relay as fallback only; optimize for direct connections

### Alternative 3: Separate STUN Port on Colony

- **Approach**: Run STUN on port 3478, WireGuard on port 41820
- **Pros**: No SO_REUSEPORT complexity; no packet demultiplexing
- **Cons**:
  - Fails with port-restricted cone NAT (different source port breaks NAT mapping)
  - Requires opening two ports on firewall/NAT
  - Doesn't solve the core symmetric NAT problem as elegantly
- **Decision**: Same-port STUN is superior; offer separate-port as configuration option

### Alternative 4: Remove SO_REUSEPORT Entirely

- **Approach**: Use ephemeral port for STUN, configured port for WireGuard
- **Pros**: Simplest implementation; no race conditions
- **Cons**:
  - STUN discovers IP only (port unknown)
  - Symmetric NAT assigns different port to WireGuard traffic
  - Back to original problem (wrong port discovered)
- **Decision**: Colony STUN with same-port binding is necessary for symmetric NAT

## Future Enhancements

1. **ICE-like Candidate Gathering**: Collect multiple candidate endpoints (direct, STUN-discovered, relay) and try in parallel
2. **IPv6 Support**: Extend colony STUN to handle IPv6 addresses and dual-stack scenarios
3. **TURN Integration**: Embed TURN relay in colonies with public IPs, allowing them to volunteer bandwidth
4. **NAT-Aware Routing**: Use NAT type classification to skip STUN for full-cone NAT
5. **Keepalive Optimization**: Send lightweight UDP keepalives before WireGuard handshake to extend NAT mapping lifetime
6. **Cross-Colony STUN**: Allow agents to use any reachable colony for STUN, not just target colony

## Appendix

### STUN Packet Format (RFC 5389)

**Binding Request:**
```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|0 0|     Type (Binding=0x0001)  |      Message Length           |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                         Magic Cookie (0x2112A442)              |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
|                     Transaction ID (96 bits)                  |
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

**Binding Response with XOR-MAPPED-ADDRESS:**
```
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|0 1|     Type (Success=0x0101)  |      Message Length           |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                         Magic Cookie (0x2112A442)              |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                     Transaction ID (same as request)          |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|     Type (XOR-MAPPED=0x0020)  |          Length               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|x|    Family   |   X-Port      |                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+                               +
|                 X-Address (XOR'd with magic cookie)           |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

### Packet Demultiplexing Logic

**WireGuard Message Types (First Byte):**
- `0x01`: Handshake Initiation
- `0x02`: Handshake Response
- `0x03`: Cookie Reply
- `0x04`: Transport Data

**STUN Message Types (First 2 Bits):**
- `00`: STUN Message (followed by message type)

**Demux Decision:**
```go
func demuxPacket(buf []byte) PacketType {
    if len(buf) < 20 {
        return Invalid
    }

    // STUN messages have first 2 bits = 00
    if buf[0] & 0xC0 == 0x00 {
        // Additional check: magic cookie at offset 4-7
        if binary.BigEndian.Uint32(buf[4:8]) == 0x2112A442 {
            return STUN
        }
    }

    // WireGuard message types
    switch buf[0] {
    case 0x01, 0x02, 0x03, 0x04:
        return WireGuard
    }

    return Invalid
}
```

### Symmetric NAT Test Setup

```bash
# Create symmetric NAT using iptables
iptables -t nat -A POSTROUTING -o eth0 -p udp -j MASQUERADE --random-fully

# Verify symmetric behavior
# Send from same source to different destinations:
nc -u stun1.example.com 3478  # Observe port A
nc -u stun2.example.com 3478  # Observe port B (different from A)

# Verify colony STUN discovers correct port
coral agent start --colony-url=<colony_ip> --debug
# Should log: "Colony STUN discovered: <ip>:<correct_port>"
```

### Related RFCs

- **RFC 5389**: Session Traversal Utilities for NAT (STUN)
- **RFC 5769**: Test Vectors for STUN
- **RFC 5766**: Traversal Using Relays around NAT (TURN)
- **RFC 8445**: Interactive Connectivity Establishment (ICE)
- **RFC 8489**: STUN Extensions for NAT Traversal
