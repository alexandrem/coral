---
rfd: "023"
title: "STUN-Based Endpoint Discovery for NAT Traversal"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "001" ]
database_migrations: [ ]
areas: [ "networking", "discovery" ]
---

# RFD 023 - STUN-Based Endpoint Discovery for NAT Traversal

**Status:** ✅ Implemented

## Summary

Enable colonies and agents behind NAT to discover their public IP addresses and
ports using external STUN servers (Cloudflare, Google). The Discovery service
tracks these observed endpoints and provides them to peers attempting to
establish WireGuard connections. This allows direct peer-to-peer connectivity
for cone NAT scenarios without requiring port forwarding or VPN tunnels. For
symmetric NAT environments, see RFD 029 (colony-based STUN) as the next
enhancement.

## Problem

- **Current behavior/limitations**:
    - Discovery only records static endpoints supplied by colonies/agents
    - When a colony or agent is behind NAT with only private addresses, remote
      peers cannot reach it
    - Operators must manually configure port forwarding or use VPN to enable
      connectivity
    - No automatic discovery of public-facing endpoints

- **Why this matters**:
    - Self-hosted colonies on home networks or laptops are a primary use case
    - Requiring manual port forwarding increases deployment complexity
    - Many users operate behind NAT without public IP addresses (CGNAT,
      corporate networks)
    - Dynamic IPs change frequently, breaking static endpoint configurations

- **Use cases affected**:
    - Developer running colony on laptop behind home router NAT
    - Cloud agents connecting to colonies behind residential ISP NAT
    - Colonies with dynamic IP addresses that change on reconnection

## Solution

Use external STUN servers to discover public endpoints, then track them in the
Discovery service:

1. **STUN Query**: Colonies and agents query public STUN servers (Cloudflare,
   Google) from their WireGuard port to discover their public IP:port
2. **Endpoint Registration**: Discovered endpoints are sent to Discovery service
   during registration
3. **Endpoint Lookup**: Peers query Discovery to retrieve observed endpoints for
   connection targets
4. **Direct Connection**: WireGuard uses observed endpoints for direct
   peer-to-peer connectivity

**Key Design Decisions**

- **External STUN Servers**: Use well-known public STUN infrastructure (
  Cloudflare: `stun.cloudflare.com:3478`) instead of self-hosting
- **No Authentication Required**: STUN is a read-only protocol that only reveals
  public IP/port; no sensitive data exposed
- **Configurable STUN Servers**: Allow operators to specify custom STUN servers
  via CLI flags or environment variables
- **TTL-Based Expiration**: Observed endpoints expire after configurable TTL (
  default 5 minutes) to handle dynamic IPs
- **Split-Brain Detection**: Prevent duplicate registrations with different
  WireGuard public keys using the same mesh ID
- **Endpoint Validation**: Reject invalid endpoint configurations (e.g., port 0
  with no static endpoints)

**Benefits**

- Zero-config NAT traversal for cone NAT environments (most home/small office
  routers)
- No port forwarding required for colonies behind NAT
- Automatic handling of dynamic IP addresses
- Works with existing WireGuard implementation (no protocol changes)
- Simple deployment (uses public STUN infrastructure)

**Architecture Overview**

```
┌─────────────┐                    ┌──────────────────┐
│   Colony    │                    │  STUN Server     │
│ (behind NAT)│                    │ (Cloudflare)     │
└──────┬──────┘                    └────────┬─────────┘
       │                                    │
       │ 1. STUN Binding Request            │
       │────────────────────────────────────>│
       │                                    │
       │ 2. STUN Response                   │
       │    (Your public IP: 203.0.113.45   │
       │<────  port: 41820)─────────────────┘
       │
       │         ┌──────────────────┐
       │         │   Discovery      │
       │         │    Service       │
       │         └────────┬─────────┘
       │                  │
       │ 3. RegisterColony│
       │    (mesh_id, pubkey,
       │     observed: 203.0.113.45:41820)
       ├──────────────────>│
       │                  │
       │ 4. Success       │
       │<──────────────────┤
       │                  │
                          │
              ┌───────────┴────────┐
              │                    │
              │                    │  5. LookupColony(mesh_id)
              │                    │<───────────────────┐
              │                    │                    │
              │ 6. Colony Info:    │                ┌───┴────┐
              │    endpoints:      │                │ Agent  │
              │    observed: 203.0.113.45:41820     └───┬────┘
              │────────────────────>│                    │
                                   │                    │
                                   │ 7. WireGuard       │
                                   │    Handshake       │
                                   │<────────────────────┤
                                   │    (to observed     │
                                   │     endpoint)       │
```

## Component Changes

1. **WireGuard STUN Client** (`internal/wireguard/stun.go`)
    - Implements RFC 5389 STUN Binding Request/Response
    - Queries configurable STUN servers (default: Cloudflare)
    - Discovers public IP and port from WireGuard socket
    - Includes basic NAT type classification (cone vs symmetric)

2. **Discovery Registry** (`internal/discovery/registry/`)
    - Stores observed endpoints with TTL-based expiration
    - Implements split-brain detection (prevents duplicate mesh_id with
      different pubkeys)
    - Validates endpoint configurations (rejects port 0 without static
      endpoints)
    - Returns observed endpoints in `LookupColony` and `LookupAgent` responses

3. **Discovery Proto** (`proto/coral/discovery/v1/discovery.proto`)
    - Added `Endpoint` message with `ip`, `port`, `protocol` fields
    - Added `observed_endpoints` field to registration requests/responses
    - Added `NatHint` enum for NAT type classification
    - Added placeholder APIs for future relay support (`RequestRelay`,
      `ReleaseRelay`)

4. **Colony STUN Integration** (`internal/cli/colony/`)
    - Discovers public endpoint before registering with Discovery
    - Includes observed endpoint in `RegisterColony` request
    - Uses observed endpoints when available for agent connections

5. **Agent STUN Integration** (`internal/cli/agent/`)
    - Discovers public endpoint before registering with Discovery
    - Includes observed endpoint in `RegisterAgent` request
    - Retrieves colony observed endpoints via `LookupColony`
    - Attempts connection to observed endpoints first, falls back to static
      endpoints

## Implementation

Implementation completed in branch `feat/discovery-nat`:

- [x] STUN client library (`internal/wireguard/stun.go`)
- [x] Discovery service registry with observed endpoint tracking
- [x] Proto definitions for observed endpoints and NAT hints
- [x] Colony STUN discovery and registration
- [x] Agent STUN discovery and registration
- [x] Split-brain detection in registry
- [x] Endpoint validation logic
- [x] Comprehensive test coverage (registry, server, STUN)
- [x] Configuration via CLI flags and environment variables
- [x] Documentation in code comments

## API Changes

### New Protobuf Messages (discovery/v1)

```protobuf
// Endpoint representation for observed public addresses
message Endpoint {
    string ip = 1;        // IPv4 address (IPv6 not yet supported)
    uint32 port = 2;      // UDP port number
    string protocol = 3;  // Always "udp" currently
}

// NAT type classification hint
enum NatHint {
    NAT_UNKNOWN = 0;      // Not classified
    NAT_CONE = 1;         // Port-preserving NAT
    NAT_RESTRICTED = 2;   // Port-restricted cone NAT
    NAT_SYMMETRIC = 3;    // Symmetric NAT (destination-dependent ports)
}

// Updated registration responses to include observed endpoints
message RegisterColonyResponse {
    // ... existing fields ...
    Endpoint observed_endpoint = 10;  // Public IP:port seen by Discovery
    NatHint nat_hint = 11;            // NAT type classification
}

message RegisterAgentResponse {
    // ... existing fields ...
    Endpoint observed_endpoint = 10;  // Public IP:port seen by Discovery
    NatHint nat_hint = 11;            // NAT type classification
}

// Updated lookup responses to include observed endpoints
message LookupColonyResponse {
    // ... existing fields ...
    repeated Endpoint observed_endpoints = 10;  // Public endpoints
    NatHint nat = 11;                           // NAT type
}

message LookupAgentResponse {
    // ... existing fields ...
    repeated Endpoint observed_endpoints = 10;  // Public endpoints
    NatHint nat = 11;                           // NAT type
}
```

### CLI Configuration

```bash
# Configure STUN servers (default: stun.cloudflare.com:3478)
coral colony start --stun-servers=stun.cloudflare.com:3478,stun.l.google.com:19302

# Or via environment variable
export CORAL_STUN_SERVERS=stun.cloudflare.com:3478,stun.l.google.com:19302

# Configure WireGuard port for reliable STUN (recommended)
coral agent start --wg-port=51821
```

## Testing Strategy

### Unit Tests (Implemented)

- STUN Binding Request/Response parsing (RFC 5389)
- NAT type classification logic
- Registry split-brain detection
- Endpoint validation (port 0, missing endpoints)
- TTL-based expiration

### Integration Tests (Implemented)

- Registry operations: register, lookup, update, expiration
- Discovery service RPCs: RegisterColony, RegisterAgent, LookupColony,
  LookupAgent
- Split-brain scenarios (duplicate mesh_id with different pubkeys)
- Endpoint validation edge cases

### Manual Testing

- Colony behind home NAT connecting to cloud agents
- Agents behind NAT connecting to public colonies
- Dynamic IP scenarios (reconnection after IP change)

## Security Considerations

- **Public STUN Servers**: Use external third-party STUN servers (Cloudflare,
  Google) which are public by design and require no authentication. STUN is a
  read-only protocol that only reveals the requester's public IP:port.
- **Information Disclosure**: STUN necessarily reveals public endpoints, but
  this contains no sensitive application data. Only IP addresses and UDP ports
  are exposed.
- **Third-Party Trust**: Reliance on external STUN servers means trusting
  Cloudflare/Google infrastructure. Operators can configure alternative STUN
  servers if desired.
- **Split-Brain Protection**: Registry prevents duplicate mesh_id registrations
  with different WireGuard public keys, protecting against identity conflicts.
- **TTL-Based Expiration**: Observed endpoints expire after 5 minutes by
  default, limiting exposure of stale endpoint data.
- **WireGuard Security Unchanged**: STUN is pre-authentication; actual WireGuard
  connections still require valid cryptographic keys.

## Known Limitations and Future Work

### 1. Symmetric NAT Compatibility

**Issue**: Public STUN servers discover destination-specific ports that don't
work for other peers.

With symmetric NAT (common in corporate networks, mobile carriers, CGNAT):

- STUN query to `stun.cloudflare.com` discovers port `54321`
- But when colony connects from different IP, NAT assigns port `54322`
- Direct connections fail because colony tries the wrong port

**Solution**: See **RFD 029** for colony-based STUN that discovers the correct
destination-specific port.

### 2. IPv6 Not Supported

**Issue**: Implementation is IPv4-only.

- IPv6 addresses discovered by STUN are currently ignored
- No dual-stack support (IPv4 + IPv6)
- Most deployments use IPv4, but IPv6-only networks won't work

**Future Work**: Add IPv6 STUN support and dual-stack endpoint handling.

### 3. NAT Type Detection Underutilized

**Issue**: NAT classification exists but isn't used for optimization.

- `ClassifyNAT()` function detects cone vs symmetric NAT
- Requires querying multiple STUN servers
- Discovery service stores NAT hint but doesn't use it for routing decisions

**Future Work**: Use NAT classification to skip STUN for full-cone NAT or
suggest relay for symmetric NAT.

### 4. No Relay Fallback

**Issue**: When direct connection fails (firewall, symmetric NAT), no fallback
mechanism exists.

- Placeholder relay APIs (`RequestRelay`, `ReleaseRelay`) exist in proto but
  return dummy data
- No actual relay server implementation
- Users must manually configure VPN or port forwarding

**Future Work**: See **RFD 029** for relay architecture and implementation plan.

### 5. NAT Mapping Timeout Handling

**Issue**: No explicit keepalive before WireGuard connection established.

- STUN-discovered NAT mappings expire after 30-60 seconds typically
- If peer lookup or connection is delayed, mapping may be lost
- WireGuard's own keepalive (25 seconds) only works after handshake completes

**Future Work**: Add lightweight UDP keepalive packets to maintain NAT holes
during connection setup.
