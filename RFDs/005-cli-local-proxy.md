---
rfd: "005"
title: "CLI Access via Local Proxy"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "001", "002" ]
database_migrations: [ ]
areas: [ "cli", "networking", "security" ]
---

# RFD 005 - CLI Access via Local Proxy

**Status:** 🎉 Implemented (Simplified)

## Implementation Status (December 2025)

**Current implementation:** Simplified HTTP reverse proxy (no WireGuard peer).

**Changes from original RFD:**

1. **No WireGuard device in proxy** - Proxy doesn't create its own TUN device or peer into mesh
2. **Assumes existing connectivity** - Either:
   - Host already has mesh connectivity (via local agent), OR
   - Will use colony's public HTTPS endpoint (see RFD 031)
3. **No elevated privileges** - Runs as regular user (just HTTP forwarding)

**Rationale:**
- Simpler implementation for current use cases
- RFD 031 (Colony Dual Interface) provides alternative with public HTTPS endpoint
- Original design (proxy as mesh peer) available for future if needed

---

## Summary

Coral CLI tools need to query colony status and issue commands to colonies that
communicate via a private WireGuard mesh network. This RFD introduces a local
proxy component that peers into the mesh and forwards Buf Connect HTTP/2
requests from CLI tools to colonies, eliminating the need for CLI tools to
implement WireGuard logic directly.

## Problem

### Current State

Coral's architecture uses a WireGuard mesh for control plane communication
between colonies and agents (per RFD 001 and design docs). Colonies expose their
services via Buf Connect (gRPC over HTTP/2) on their mesh IP addresses (e.g.,
`10.42.0.1:9000`).

### The Challenge

CLI tools like `coral status`, `coral ask`, and `coral topology` need to query
colonies for real-time information. However:

1. **Mesh IPs are not directly routable**: Colonies listen on private mesh IPs (
   10.42.0.0/16 range), which are only accessible to WireGuard mesh peers.

2. **CLI complexity**: Requiring every CLI invocation to establish WireGuard
   tunnels would add significant complexity:
    - WireGuard key management on developer machines
    - Network interface creation per command
    - Discovery lookups and peer configuration
    - Cleanup and tear down logic

3. **Remote access**: Developers and operators need to query colonies from
   various locations:
    - Local development machines
    - Jump hosts / bastion servers
    - CI/CD pipelines
    - Production servers running application components

4. **Multi-colony access**: Users often work with multiple colonies (dev,
   staging, prod) and need seamless switching between them.

### Why This Matters

Without proper CLI access:

- Operators cannot debug production issues efficiently
- Developers cannot query local or remote colonies easily
- Troubleshooting requires SSH access to colony hosts
- The vision of unified observability (`coral ask "what's wrong?"`) cannot be
  realized

## Solution

### Overview

Introduce a **local proxy component** that acts as a gateway between CLI tools
and the WireGuard mesh. The proxy:

1. Peers into the WireGuard mesh (gets assigned a mesh IP)
2. Maintains persistent connections to colonies
3. Forwards HTTP/2 requests from localhost to colony mesh IPs
4. Handles discovery lookups transparently

**Key Design Decision:** The proxy is a **transparent HTTP/2 reverse proxy**,
not a protocol translator. Both CLI and colony use Buf Connect natively; the
proxy simply forwards requests over the WireGuard tunnel.

### Architecture

```
┌─────────────────────────────────────────────────────────┐
│  Developer Machine / Production Server                   │
│                                                          │
│  ┌──────────────┐                                       │
│  │  coral CLI   │  (Buf Connect HTTP/2 client)          │
│  └──────┬───────┘                                       │
│         │ POST /coral.colony.v1.ColonyService/GetStatus │
│         │ HTTP/2 to localhost:8000                      │
│         ↓                                                │
│  ┌──────────────────────────────────────────────┐        │
│  │  Coral Proxy                                 │        │
│  │  - HTTP/2 reverse proxy                      │        │
│  │  - WireGuard mesh peer                       │        │
│  │  - Discovery client                          │        │
│  │  - Mesh IPv4: 10.42.0.X (assigned)           │        │
│  │  - Mesh IPv6: fd42::X (assigned)             │        │
│  │  - Listens: localhost:8000                   │        │
│  └──────────┬───────────────────────────────────┘        │
│             │ Forward to http://10.42.0.1:9000           │
│             │        or http://[fd42::1]:9000            │
│             │ over WireGuard tunnel                      │
└─────────────┼────────────────────────────────────────────┘
              │ Encrypted control plane traffic
              ↓
┌─────────────────────────────────────────────────────────┐
│  Colony (WireGuard Mesh Hub)                            │
│  - Mesh IPv4: 10.42.0.1, IPv6: fd42::1                  │
│  - Buf Connect server on :9000                          │
│  - Services: ColonyService, AgentService, etc.          │
│  - Accepts connections from mesh peers only             │
└─────────────────────────────────────────────────────────┘
```

### Connection Flow

```
1. Proxy Initialization
   ├─ User runs: coral proxy start my-app-prod
   ├─ Proxy queries discovery for colony endpoints
   │  └─ discovery.LookupColony(mesh_id="my-app-prod")
   │     Returns: {pubkey, public_endpoints, mesh_ip, connect_port}
   │
   ├─ Proxy establishes WireGuard tunnel to colony
   │  └─ Uses public_endpoints for NAT traversal
   │
   └─ Proxy registers with colony as mesh peer
      └─ Colony assigns mesh IPs: 10.42.0.15 / fd42::f

2. CLI Request
   ├─ User runs: coral status
   ├─ CLI sends HTTP/2 request to localhost:8000
   │  └─ POST /coral.colony.v1.ColonyService/GetStatus
   │
   ├─ Proxy forwards request to colony
   │  └─ Forward to http://10.42.0.1:9000 (or http://[fd42::1]:9000) over WireGuard
   │
   ├─ Colony processes request, returns response
   └─ Proxy forwards response back to CLI

3. Multi-Colony Access
   ├─ Option A: Path-based routing
   │  └─ http://localhost:8000/my-app-prod/...
   │
   ├─ Option B: Header-based routing
   │  └─ Colony-ID: my-app-prod
   │
   └─ Option C: Multiple proxy instances (RECOMMENDED)
      └─ coral proxy start my-app-prod --port 8000
          coral proxy start my-app-dev --port 8001
```

**Multi-Colony Architecture Detail:**

When connecting to multiple colonies, each proxy instance requires:

```
Developer Machine:

  Proxy 1 (for prod colony):
    ├─ WireGuard interface: coral-prod0
    ├─ Colony's mesh IPs: 10.42.0.1:9000 / [fd42::1]:9000
    ├─ Proxy's assigned mesh IPs: 10.42.0.100 / fd42::64
    ├─ HTTP listener: localhost:8000
    └─ Queries: POST http://localhost:8000 → 10.42.0.1:9000 (via WG)

  Proxy 2 (for staging colony):
    ├─ WireGuard interface: coral-staging0
    ├─ Colony's mesh IPs: 10.43.0.1:9000 / [fd43::1]:9000
    ├─ Proxy's assigned mesh IPs: 10.43.0.100 / fd43::64
    ├─ HTTP listener: localhost:8001
    └─ Queries: POST http://localhost:8001 → 10.43.0.1:9000 (via WG)
```

**Key Points:**

- Each colony has its own isolated WireGuard mesh (separate subnets)
- Proxy generates **ephemeral WireGuard keypair** per colony connection
- Colony assigns mesh IP to proxy (like it assigns to agents)
- Multiple WireGuard interfaces on same machine (coral-prod0, coral-staging0)
- IPv6 ULA space scales to thousands of colonies without subnet exhaustion
- This is the **same pattern Reef uses** for federation (RFD 003)

### IPv6 Mesh Addressing

Coral's WireGuard mesh uses **dual-stack addressing** to support both IPv4 and
IPv6 for internal mesh communication. This provides better scalability and
future-proofing for larger deployments.

**Addressing Scheme:**

- **IPv4 mesh range:** `10.42.0.0/16` (current default for colony mesh)
- **IPv6 mesh range:** `fd42::/64` (IPv6 ULA - Unique Local Address)
- Each mesh peer receives **both** an IPv4 and IPv6 address

**Example Dual-Stack Assignment:**

```
Colony mesh peer:
  ├─ IPv4: 10.42.0.1
  └─ IPv6: fd42::1

Proxy mesh peer:
  ├─ IPv4: 10.42.0.100
  └─ IPv6: fd42::64

Agent mesh peer:
  ├─ IPv4: 10.42.0.10
  └─ IPv6: fd42::a
```

**WireGuard Configuration:**

WireGuard natively supports dual-stack configurations. The `AllowedIPs` setting
includes both IPv4 and IPv6 ranges:

```
[Peer]
PublicKey = <colony-pubkey>
Endpoint = 203.0.113.42:41820
AllowedIPs = 10.42.0.0/16, fd42::/64
```

**Rationale:**

- **Larger address space:** IPv6 provides 2^64 addresses per mesh vs 2^16 for
  IPv4
- **Multi-colony scalability:** Each colony needs an isolated subnet. IPv4
  private address space (RFC 1918: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)
  and CGNAT space (100.64.0.0/10) are limited. With IPv6 ULA (fd00::/8), we
  can allocate unique /64 subnets for thousands of colonies without exhaustion
  (e.g., fd42::/64, fd43::/64, fd44::/64, etc.)
- **Future-proofing:** Prepares for IPv6-only environments
- **Minimal overhead:** WireGuard handles dual-stack transparently
- **No public endpoint changes:** IPv6 is **only for mesh IPs**, not public
  endpoints
- **Gradual adoption:** Services can use either IPv4 or IPv6 mesh addresses

**Important Note:**

IPv6 addressing in Coral is **limited to the WireGuard mesh network only**.
Public endpoints used for NAT traversal and discovery remain protocol-agnostic
(typically IPv4). This keeps the discovery service simple while providing IPv6
benefits for internal mesh scaling.

### Key Design Decisions

#### 1. HTTP/2 Reverse Proxy (Not Protocol Translation)

**Decision:** Proxy forwards HTTP/2 requests without translation.

**Rationale:**

- Both CLI and colony already use Buf Connect (HTTP/2)
- No marshaling/unmarshaling overhead
- Simpler implementation (standard `httputil.ReverseProxy`)
- Transparent to both client and server

**Alternative Considered:** HTTP → gRPC translator

- **Rejected:** Unnecessary complexity, performance overhead

#### 2. Proxy as Mesh Peer (Not HTTP Tunnel)

**Decision:** Proxy peers into WireGuard mesh like agents do.

**Rationale:**

- Consistent security model
- Works across NATs and networks
- Leverages existing mesh infrastructure
- Enables future features (e.g., direct agent queries)

**Alternative Considered:** Colony exposes separate HTTP endpoint

- **Rejected:** Breaks mesh-only security model, requires port forwarding

#### 3. Standalone Component (With Optional Embedding)

**Decision:** Proxy is a separate binary (`coral-proxy`) that can optionally be
embedded in agents.

**Rationale:**

- **Standalone mode:** For developer machines without agents
- **Embedded mode:** For production servers with agents (dual-purpose)
- Flexibility for different deployment scenarios

#### 4. Local Localhost Binding (Not Remote Access)

**Decision:** Proxy binds to `localhost` only by default.

**Rationale:**

- Security: Only local processes can access proxy
- Remote access via SSH tunneling (standard pattern)
- Avoids authentication complexity in initial version

**Future Enhancement:** Optional authentication for remote binding

### Component Changes

1. **Discovery Service (RFD 001 Extension)**
    - Add `mesh_ip` field to colony registrations
    - Add `connect_port` field (Buf Connect HTTP/2 port)
    - Discovery returns both public endpoints (for peering) and mesh IPs (for
      communication)

2. **Colony**
    - Accept proxy peers (treat like agents)
    - Assign mesh IPs to proxies from peer pool
    - Expose Buf Connect services on mesh IP:9000

3. **New: Proxy Component**
    - HTTP/2 server on localhost:8000
    - WireGuard mesh client
    - Discovery client for colony lookup
    - Reverse proxy logic
    - Optional: Multi-colony routing

4. **CLI Commands**
    - Update all commands to use localhost:8000
    - Add `coral proxy start/stop/status/list` commands
    - Graceful fallback when proxy not running

5. **Agent (Optional Enhancement)**
    - Add `--enable-proxy` flag to `coral connect`
    - Embed proxy server when enabled
    - Agent becomes both monitor and gateway

## API Changes

### Discovery Service Proto Updates

**File: `proto/coral/discovery/v1/discovery.proto`**

```protobuf
// Existing service - updated messages
service DiscoveryService {
    rpc RegisterColony(RegisterColonyRequest) returns (RegisterColonyResponse);
    rpc LookupColony(LookupColonyRequest) returns (LookupColonyResponse);
    rpc Health(HealthRequest) returns (HealthResponse);
}

// Updated: Add mesh IPs (dual-stack) and connect_port
message RegisterColonyRequest {
    string mesh_id = 1;
    string wireguard_pubkey = 2;
    repeated string public_endpoints = 3;  // Public IPs for WireGuard NAT traversal
    string mesh_ipv4 = 4;                  // NEW: Private mesh IPv4 (e.g., "10.42.0.1")
    string mesh_ipv6 = 5;                  // NEW: Private mesh IPv6 (e.g., "fd42::1")
    uint32 connect_port = 6;               // NEW: Buf Connect HTTP/2 port (e.g., 9000)
    map<string, string> metadata = 7;
}

// Updated: Return mesh IPs (dual-stack) and connect_port
message LookupColonyResponse {
    string wireguard_pubkey = 1;
    repeated string public_endpoints = 2;
    string mesh_ipv4 = 3;                  // NEW: Mesh IPv4 for communication
    string mesh_ipv6 = 4;                  // NEW: Mesh IPv6 for communication
    uint32 connect_port = 5;               // NEW: Port for Buf Connect
    map<string, string> metadata = 6;
    google.protobuf.Timestamp last_seen = 7;
}
```

### Colony Service Proto (New)

**File: `proto/coral/colony/v1/colony.proto`**

```protobuf
syntax = "proto3";

package coral.colony.v1;

import "google/protobuf/timestamp.proto";

// Colony management service exposed on mesh network
service ColonyService {
    // Get colony status and health
    rpc GetStatus(GetStatusRequest) returns (GetStatusResponse);

    // List connected agents
    rpc ListAgents(ListAgentsRequest) returns (ListAgentsResponse);

    // Get network topology
    rpc GetTopology(GetTopologyRequest) returns (GetTopologyResponse);
}

message GetStatusRequest {}

message GetStatusResponse {
    string colony_id = 1;
    string app_name = 2;
    string environment = 3;
    string status = 4;                          // "running", "degraded", "unhealthy"
    google.protobuf.Timestamp started_at = 5;
    int64 uptime_seconds = 6;
    int32 agent_count = 7;
    string dashboard_url = 8;
    int64 storage_bytes = 9;
}

message ListAgentsRequest {}

message ListAgentsResponse {
    repeated Agent agents = 1;
}

message Agent {
    string agent_id = 1;
    string component_name = 2;
    string mesh_ipv4 = 3;
    string mesh_ipv6 = 4;
    google.protobuf.Timestamp last_seen = 5;
    string status = 6;                          // "healthy", "degraded", "unhealthy"
}

message GetTopologyRequest {}

message GetTopologyResponse {
    string colony_id = 1;
    repeated Agent agents = 2;
    repeated Connection connections = 3;
}

message Connection {
    string source_id = 1;
    string target_id = 2;
    string connection_type = 3;                 // "http", "grpc", "database", etc.
}
```

### CLI Commands

```bash
# Start proxy for a colony
$ coral proxy start my-app-prod
Resolving colony: my-app-prod
 ↳ Querying discovery service at http://localhost:8080...
 ↳ Colony endpoint: 203.0.113.42:41820 (mesh IPs: 10.42.0.1 / fd42::1)
 ↳ Establishing WireGuard tunnel...
 ↳ Registering as proxy peer...
 ↳ Assigned mesh IPs: 10.42.0.15 / fd42::f
✓ Proxy running on http://localhost:8000
  Forwarding to colony at 10.42.0.1:9000 (over mesh)

# Start proxy in background
$ coral proxy start my-app-prod --daemon
✓ Proxy started (PID: 12345)

# Check proxy status
$ coral proxy status
Active Proxies:
  my-app-prod
    ├─ Listening: localhost:8000
    ├─ Colony: 10.42.0.1:9000 / [fd42::1]:9000
    ├─ Mesh IPs: 10.42.0.15 / fd42::f
    ├─ Status: Connected
    └─ Uptime: 2h 15m

# List all proxies
$ coral proxy list
my-app-prod    localhost:8000    connected    2h 15m
my-app-dev     localhost:8001    offline      -

# Stop proxy
$ coral proxy stop my-app-prod
Stopping proxy for: my-app-prod
 ↳ Closing WireGuard tunnel...
 ↳ Unregistering from colony...
✓ Proxy stopped

# CLI commands use proxy automatically
$ coral status
Discovery Service: ✓ Online (3 colonies)

Colony: my-app-prod [ONLINE via proxy]
  ├─ App: my-shop
  ├─ Environment: production
  ├─ Agents: 5 connected
  ├─ Uptime: 4d 2h 15m
  └─ Dashboard: http://10.42.0.1:3000

$ coral ask "what's causing high latency?"
[AI query sent to colony via proxy...]
```

### Configuration

**Colony Config (`~/.coral/colonies/my-app-prod.yaml`)**

```yaml
colony_id: my-app-prod-a3f2e1
app_name: my-shop
environment: production

wireguard:
    private_key: <base64>
    public_key: <base64>
    mesh_ipv4: 10.42.0.1
    mesh_ipv6: fd42::1
    listen_port: 41820
    mesh_network_ipv4: 10.42.0.0/16
    mesh_network_ipv6: fd42::/64

services:
    connect_port: 9000          # NEW: Buf Connect HTTP/2 port
    dashboard_port: 3000

discovery:
    endpoint: http://localhost:8080
    mesh_id: my-app-prod
    register_interval: 60s
    auto_register: true
```

**Proxy Config (Runtime State)**

```yaml
# Managed by coral proxy commands
proxies:
    my-app-prod:
        colony_id: my-app-prod-a3f2e1
        listen_port: 8000
        mesh_ipv4: 10.42.0.15         # Assigned by colony
        mesh_ipv6: fd42::f            # Assigned by colony
        colony_mesh_ipv4: 10.42.0.1
        colony_mesh_ipv6: fd42::1
        colony_connect_port: 9000
        status: connected
        pid: 12345
```

## Implementation Plan

### Phase 1: Discovery Service Updates

- [x] Update `proto/coral/discovery/v1/discovery.proto`
    - [x] Add `mesh_ipv4` field to `RegisterColonyRequest`
    - [x] Add `mesh_ipv6` field to `RegisterColonyRequest`
    - [x] Add `connect_port` field to `RegisterColonyRequest`
    - [x] Add corresponding fields to `LookupColonyResponse`
- [x] Regenerate Go code with `buf generate`
- [x] Update discovery registry to store new fields (dual-stack mesh IPs)
- [x] Update discovery client to handle new fields
- [x] Add unit tests for new fields (test both IPv4 and IPv6)

### Phase 2: Colony Service Definition

- [x] Create `proto/coral/colony/v1/colony.proto`
    - [x] Define `ColonyService` with GetStatus, ListAgents, GetTopology
    - [x] Define request/response message types (Agent message with dual-stack
      IPs)
- [x] Regenerate Go code with `buf generate`
- [x] Implement `ColonyService` server in colony
    - [x] GetStatus: query local state, return colony info
    - [x] ListAgents: query agent registry (return dual-stack mesh IPs)
    - [x] GetTopology: return agent connections
- [x] Start Buf Connect server on mesh IP:9000 (accessible via both IPv4 and
  IPv6)
- [x] Update colony startup to register with new dual-stack mesh IP fields

### Phase 3: Proxy Implementation

- [x] Create `internal/proxy` package
    - [x] `server.go`: HTTP/2 server on localhost
    - [x] `wireguard.go`: WireGuard peer setup (dual-stack IPv4+IPv6 support)
    - [x] `discovery.go`: Colony lookup via discovery
    - [x] `forwarder.go`: HTTP/2 reverse proxy logic (handle both IPv4 and IPv6
      targets)
    - [x] `registry.go`: Track active colony connections
- [x] Create `cmd/coral-proxy/main.go` standalone binary
- [x] Implement proxy lifecycle
    - [x] Discovery lookup
    - [x] WireGuard tunnel establishment (AllowedIPs: IPv4 + IPv6 ranges)
    - [x] Colony peer registration (receive dual-stack mesh IPs)
    - [x] HTTP/2 forwarding (support both IPv4 and IPv6 mesh targets)
- [x] Add multi-colony routing (path-based or header-based)
- [x] Add graceful shutdown and cleanup

### Phase 4: CLI Integration

- [x] Create `internal/cli/proxy.go`
    - [x] `coral proxy start <colony-id>` command
    - [x] `coral proxy stop <colony-id>` command
    - [x] `coral proxy status` command
    - [x] `coral proxy list` command
- [x] Update CLI client configuration
    - [x] Default to localhost:8000 if proxy running
    - [x] Fallback behavior when proxy not available
- [x] Update existing commands to use proxy
    - [x] `coral status`
    - [x] `coral ask`
    - [x] `coral topology` (future)
- [x] Add proxy state management (PID files, config)

### Phase 5: Agent Embedding (Optional)

- [x] Update `internal/agent/connect.go`
    - [x] Add `--enable-proxy` flag
    - [x] Embed proxy server when enabled
    - [x] Share WireGuard connection with monitoring
- [x] Agent becomes dual-purpose: monitor + gateway

### Phase 6: Testing

- [x] Unit tests for proxy components
- [x] Integration tests: proxy ↔ discovery ↔ colony
- [x] E2E tests: CLI → proxy → colony
- [x] Test multi-colony scenarios
- [x] Test error handling and reconnection
- [x] Test graceful degradation (proxy offline)

### Phase 7: Documentation

- [x] Update IMPLEMENTATION.md with proxy architecture and IPv6 mesh addressing
- [x] Update CLI reference docs (include dual-stack mesh IP examples)
- [x] Add proxy setup guide (document IPv6 mesh configuration)
- [x] Add troubleshooting guide (IPv6 connectivity issues)
- [x] Update architecture diagrams (show dual-stack mesh IPs)

## Testing Strategy

### Unit Tests

**Proxy Package:**

- HTTP/2 request forwarding logic
- Discovery client integration
- WireGuard peer setup
- Multi-colony routing logic

**Colony Service:**

- GetStatus returns correct data
- ListAgents queries agent registry
- GetTopology builds graph correctly

**Discovery Service:**

- New fields stored and returned correctly
- Backward compatibility with existing clients

### Integration Tests

**Proxy ↔ Discovery:**

- Proxy successfully looks up colony
- Handles discovery service failures gracefully
- Retries on transient errors

**Proxy ↔ Colony:**

- Establishes WireGuard tunnel
- Forwards HTTP/2 requests correctly
- Handles colony disconnection and reconnection

**CLI ↔ Proxy:**

- CLI commands reach colony via proxy
- Graceful fallback when proxy not running
- Error messages are clear and actionable

### E2E Tests

**Full Stack:**

1. Start colony
2. Start proxy
3. Run `coral status` → Verify correct output
4. Stop proxy
5. Run `coral status` → Verify fallback behavior
6. Restart proxy
7. Verify automatic reconnection

**Multi-Colony:**

1. Start two colonies (dev, prod)
2. Start two proxies
3. Switch between colonies via CLI
4. Verify correct routing

**IPv6 Mesh Tests:**

1. Verify WireGuard tunnel configured with dual-stack AllowedIPs
2. Test connectivity using both IPv4 and IPv6 mesh addresses
3. Verify proxy can forward requests to `http://10.42.0.1:9000` (IPv4)
4. Verify proxy can forward requests to `http://[fd42::1]:9000` (IPv6)
5. Test mesh peer assignment with dual-stack IPs
6. Verify config files correctly store both IPv4 and IPv6 mesh addresses
7. Test that CLI displays both mesh IPs in status output

## Security Considerations

### Proxy Authentication

**Initial Version:** Proxy binds to `localhost` only.

- Only processes on the same machine can access proxy
- No network exposure
- Standard Unix file permissions protect proxy state

**Future Enhancement:** Remote proxy access with authentication:

- Mutual TLS for proxy ↔ CLI communication
- Token-based authentication
- Integration with colony secrets

### Mesh Access Control

**Proxy as Mesh Peer:**

- Proxy must prove colony membership (via colony secret)
- Colony assigns mesh IP to proxy (tracks in peer registry)
- Colony can revoke proxy access

**Security Model:**

```
Layer 1: Discovery (untrusted)
         ↓ Returns public info (endpoints, pubkeys)

Layer 2: WireGuard (peer verification)
         ↓ Proves identity via pubkey

Layer 3: Colony registration (authorization)
         ↓ Colony secret required

Layer 4: Request authorization (future)
         ↓ Per-RPC permissions
```

### Credential Storage

**Proxy Needs:**

- Colony secret (for peer registration)
- WireGuard private key (generated per proxy instance)

**Storage:**

- Colony secret: Read from `~/.coral/colonies/<id>.yaml`
- Proxy private key: Ephemeral (generated on start, destroyed on stop)
- No long-term credential storage for proxy itself

### Audit Logging

**Colony Side:**

- Log proxy peer registrations
- Log requests from proxy peers
- Track proxy mesh IPs in peer registry

**Proxy Side:**

- Log CLI requests (optional, can be verbose)
- Log connection events (tunnel up/down)

## Migration Strategy

### Deployment Order

1. **Update discovery service:**
    - Deploy updated discovery service with new fields
    - Backward compatible: old clients ignore new fields
    - New colonies register with mesh_ip + connect_port

2. **Update colonies:**
    - Deploy colonies with Buf Connect server on mesh IP
    - Colonies register with updated discovery schema
    - Existing agent connections unaffected

3. **Deploy proxy:**
    - Install `coral-proxy` binary
    - CLI users opt-in with `coral proxy start`
    - CLI continues to work without proxy (fallback mode)

4. **Update agents (optional):**
    - Deploy agents with embedded proxy support
    - Operators opt-in with `--enable-proxy` flag

### Rollback Plan

If issues arise:

1. Stop proxies: `coral proxy stop <colony>`
2. CLI falls back to config-only mode
3. Colonies continue operating normally
4. Discovery service backward compatible (new fields optional)

No database migrations required; all changes are additive.

## Future Enhancements

### 1. Public Proxy Endpoints

Allow colonies to expose proxies with authentication:

- Proxy accepts remote connections with mTLS
- CLI authenticates with API tokens
- Enables remote debugging without SSH

### 2. Proxy Clustering

Multiple proxies for HA and load distribution:

- Proxy registers with colony as ephemeral node
- CLI round-robins across available proxies
- Improves reliability for production access

### 3. Intelligent Routing

Proxy makes smart routing decisions:

- Route agent-specific queries directly to agents
- Cache frequently accessed data
- Aggregate multi-agent queries

### 4. Proxy Mesh

Proxies peer with each other:

- Enables cross-colony queries via single proxy
- `coral ask --all-colonies "what's broken?"`
- Federation use case (related to RFD 003)

### 5. Web Dashboard via Proxy

Serve web dashboard through proxy:

- `http://localhost:8000/dashboard` → Colony dashboard
- Eliminates need for dashboard port forwarding
- Unified access point for CLI and web

## Appendix

### A. Protocol Flow Details

**Complete Connection Sequence:**

```
1. User runs: coral proxy start my-app-prod

2. Proxy loads colony config
   ├─ Read ~/.coral/colonies/my-app-prod.yaml
   ├─ Extract: colony_id, colony_secret, discovery_endpoint
   └─ Generate ephemeral WireGuard keypair

3. Proxy queries discovery
   ├─ Request: LookupColony(mesh_id="my-app-prod")
   └─ Response: {
        wireguard_pubkey: "...",
        public_endpoints: ["203.0.113.42:41820"],
        mesh_ipv4: "10.42.0.1",
        mesh_ipv6: "fd42::1",
        connect_port: 9000
      }

4. Proxy establishes WireGuard tunnel
   ├─ Configure WireGuard interface
   ├─ Add peer: colony pubkey + endpoints
   ├─ Set allowed IPs: 10.42.0.0/16, fd42::/64
   └─ Bring interface up

5. Proxy registers with colony
   ├─ Send RegisterProxyRequest over tunnel
   │  └─ To: http://10.42.0.1:9000/coral.mesh.v1.MeshService/Register
   │  └─ Body: {colony_secret, proxy_pubkey, component_name: "proxy"}
   └─ Colony assigns mesh IPs: 10.42.0.15 / fd42::f

6. Proxy starts HTTP/2 server
   ├─ Listen on localhost:8000
   └─ Ready to accept CLI requests

7. User runs: coral status

8. CLI sends request
   ├─ POST http://localhost:8000/coral.colony.v1.ColonyService/GetStatus
   └─ Content-Type: application/grpc-web+proto

9. Proxy forwards request
   ├─ Forward to http://10.42.0.1:9000/coral.colony.v1.ColonyService/GetStatus
   └─ Over WireGuard tunnel

10. Colony processes request
    ├─ Query local state (uptime, agent count, etc.)
    └─ Return GetStatusResponse

11. Proxy forwards response
    └─ Return to CLI unchanged

12. CLI renders output
    └─ Display colony status
```

### B. Comparison to kubectl Pattern

Coral's proxy architecture closely mirrors `kubectl`:

| Aspect             | kubectl                     | Coral                          |
|--------------------|-----------------------------|--------------------------------|
| **Client tool**    | kubectl CLI                 | coral CLI                      |
| **Target**         | Kubernetes API server       | Colony (Buf Connect)           |
| **Protocol**       | HTTP/2 (gRPC)               | HTTP/2 (Buf Connect)           |
| **Discovery**      | kubeconfig file             | Discovery service              |
| **Access pattern** | Direct or via proxy         | Via local proxy                |
| **Authentication** | Tokens, certs in kubeconfig | Colony secret (future: tokens) |
| **Multi-cluster**  | Context switching           | Multi-colony routing           |

**Key similarity:** Both use HTTP/2 forwarding to a remote control plane over
secure channels.

### C. Comparison to KubeSPAN

Our earlier research on KubeSPAN's discovery pattern applies here:

| Component          | KubeSPAN               | Coral                            |
|--------------------|------------------------|----------------------------------|
| **Discovery**      | Ephemeral coordination | Ephemeral coordination ✓         |
| **State storage**  | Kubernetes API / etcd  | Colony DuckDB ✓                  |
| **Client access**  | kubectl → API server   | coral CLI → proxy → colony ✓     |
| **Discovery data** | Endpoints + pubkeys    | Endpoints + pubkeys + mesh IPs ✓ |

**Key insight:** Discovery is stateless coordination; colony is stateful source
of truth. Proxy bridges the two.

### D. Alternative Architectures Considered

#### Alternative 1: Direct WireGuard in CLI

**Approach:** CLI establishes WireGuard tunnel directly.

**Pros:**

- No separate proxy component
- Simpler deployment (one binary)

**Cons:**

- CLI must handle WireGuard setup/teardown per invocation
- Requires root/admin privileges on some platforms
- Slow startup for every command
- Complex state management (cleanup on Ctrl+C, crashes)
- Hard to maintain persistent connections

**Verdict:** Rejected due to complexity and performance overhead.

#### Alternative 2: Colony Dual Endpoint (Mesh + Public)

**Approach:** Colony exposes both mesh IP and public HTTP endpoint.

**Pros:**

- CLI can connect directly without proxy
- Simpler for developers (no proxy setup)

**Cons:**

- Breaks mesh-only security model
- Requires firewall rules / port forwarding
- Increases attack surface
- Public endpoint needs separate authentication
- Doesn't work in air-gapped environments

**Verdict:** Rejected due to security concerns and operational complexity.

#### Alternative 3: SSH Tunnel

**Approach:** Users SSH to colony host and run CLI locally.

**Pros:**

- No new components needed
- Standard SSH authentication
- Works today without changes

**Cons:**

- Requires SSH access to colony hosts
- Doesn't work for multi-host colonies
- Poor developer experience (extra hop)
- Doesn't support agent-embedded proxy use case
- Breaks in containerized / orchestrated environments

**Verdict:** Valid as a fallback, but not primary solution.

#### Alternative 4: VPN Access

**Approach:** Use corporate VPN to access mesh network.

**Pros:**

- Leverages existing infrastructure
- No new authentication needed

**Cons:**

- Assumes VPN infrastructure exists
- VPN may not have routes to mesh network
- Doesn't work for public cloud deployments
- Doesn't help with local development
- Not portable across environments

**Verdict:** Complementary, not a replacement for proxy.

### E. Example Output

**Proxy Startup:**

```
$ coral proxy start my-shop-prod --verbose
[2025-10-29 10:23:15] Loading colony config: ~/.coral/colonies/my-shop-prod.yaml
[2025-10-29 10:23:15] Colony ID: my-shop-prod-a3f2e1
[2025-10-29 10:23:15] Generating ephemeral WireGuard keypair...
[2025-10-29 10:23:15] Querying discovery: http://localhost:8080
[2025-10-29 10:23:15] Discovery lookup: mesh_id=my-shop-prod
[2025-10-29 10:23:16] Colony found:
  ├─ Public endpoint: 203.0.113.42:41820
  ├─ WireGuard pubkey: 7vH3k9mP...
  ├─ Mesh IPv4: 10.42.0.1
  ├─ Mesh IPv6: fd42::1
  └─ Connect port: 9000
[2025-10-29 10:23:16] Configuring WireGuard interface: coral0
[2025-10-29 10:23:16] Adding peer: 7vH3k9mP... → 203.0.113.42:41820
[2025-10-29 10:23:16] AllowedIPs: 10.42.0.0/16, fd42::/64
[2025-10-29 10:23:17] WireGuard tunnel established
[2025-10-29 10:23:17] Registering with colony...
[2025-10-29 10:23:17] POST http://10.42.0.1:9000/coral.mesh.v1.MeshService/Register
[2025-10-29 10:23:18] Colony assigned mesh IPs: 10.42.0.15 / fd42::f
[2025-10-29 10:23:18] Starting HTTP/2 server on localhost:8000
[2025-10-29 10:23:18] ✓ Proxy running

Press Ctrl+C to stop proxy
```

**CLI Status via Proxy:**

```
$ coral status --verbose
[2025-10-29 10:25:30] Checking for local proxy...
[2025-10-29 10:25:30] Found proxy at localhost:8000
[2025-10-29 10:25:30] Sending GetStatus request...
[2025-10-29 10:25:30] POST http://localhost:8000/coral.colony.v1.ColonyService/GetStatus
[2025-10-29 10:25:31] Response received (234 bytes)

Colony: my-shop-prod [ONLINE via proxy]
  ├─ App: my-shop
  ├─ Environment: production
  ├─ Status: healthy
  ├─ Uptime: 4d 2h 15m
  ├─ Agents: 5 connected
  ├─ Dashboard: http://10.42.0.1:3000
  └─ Storage: 1.2 GB

Agents:
  ├─ api-server (10.42.0.10) - healthy, last seen 5s ago
  ├─ worker-1 (10.42.0.11) - healthy, last seen 3s ago
  ├─ worker-2 (10.42.0.12) - healthy, last seen 2s ago
  ├─ cache (10.42.0.13) - healthy, last seen 8s ago
  └─ db-proxy (10.42.0.14) - healthy, last seen 1s ago
```

## Notes

### Design Philosophy

**Separation of Concerns:**

- Discovery: Ephemeral coordination (how to reach?)
- Colony: Persistent state (what's happening?)
- Proxy: Gateway (bridge mesh and localhost)

**Transparency:**

- Proxy is transparent to both CLI and colony
- No protocol translation or data transformation
- Standard HTTP/2 reverse proxy pattern

**Developer Experience:**

- Single command to enable mesh access: `coral proxy start`
- CLI commands work the same with or without proxy
- Clear error messages guide setup

**Operational Flexibility:**

- Standalone proxy for dev machines
- Embedded proxy in agents for production
- Multi-colony support for complex environments

### Relationship to Other RFDs

**RFD 001 (Discovery Service):**

- Extends discovery schema with mesh_ipv4, mesh_ipv6, and connect_port
- Proxy uses discovery to locate colonies (returns dual-stack mesh IPs)
- Discovery remains ephemeral (no state changes)
- Public endpoints remain protocol-agnostic (IPv6 only for mesh)

**RFD 002 (Application Identity):**

- Colony identity (colony_id, mesh_id) used for discovery lookups
- Colony secret used for proxy authentication
- Application-scoped access (proxies for specific colonies)

**RFD 003 (Reef Multi-Colony Federation):**

- Reef uses **same WireGuard mesh peering pattern** as proxy
- Reef generates ephemeral keys and peers into each colony's mesh
- Unified architecture: Agents, Proxies, and Reef all use WireGuard mesh
- No TLS needed for any control plane communication
- Security model: WireGuard encryption + colony_secret authentication

**RFD 004 (MCP Server Integration):**

- MCP server on developer machine uses proxy
- Claude Desktop queries colonies via local proxy
- Unified access for both CLI and MCP

### When to Use Proxy

**Use proxy when:**

- Running CLI commands from developer machine
- Colony is remote or on different network
- Need persistent connection for multiple commands
- Working with multiple colonies (multi-proxy setup)

**Don't need proxy when:**

- Running CLI on same host as colony
- Colony exposes HTTP endpoint on localhost (dev mode)
- Using SSH tunnel to colony host
- Only need config-based commands (no live queries)

### Security Model Evolution

**Current (RFD 005):**

- Proxy bound to localhost only
- Colony secret for proxy authentication
- No per-user authentication

**Future (Post-MVP):**

- Token-based authentication for remote proxy access
- Per-user authorization for RPC methods
- Audit logging with user attribution
- Integration with identity providers (OIDC)

This RFD establishes the foundation for authenticated access without building
the full auth system upfront.
