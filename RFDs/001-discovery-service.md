---
rfd: "001"
title: "Discovery Service (Prototype)"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ ]
database_migrations: [ ]
areas: [ "networking", "discovery", "infrastructure" ]
---

# RFD 001 - Discovery Service (Prototype)

**Status:** 🚧 Draft

## Summary

A lightweight HTTP-based discovery service that enables colonies and agents to
find each other across NATs and different networks. This prototype
implementation focuses on core registration and lookup functionality without
authentication, providing the foundation for agent-colony connectivity.

**Important**: This is a **separate binary** (`coral-discovery`), not a subcommand
of the `coral` CLI. It's deployed independently from the main Coral application.

## Problem

**Current behavior/limitations:**

- Colonies and agents cannot discover each other across different networks
- NAT traversal requires manual configuration of IP addresses and ports
- No central coordination point for mesh formation
- Hard-coded endpoints make dynamic environments difficult to support

**Why this matters:**

- Development environments need dynamic discovery (laptops, home networks)
- Production deployments span multiple networks and regions
- Manual IP configuration doesn't scale beyond trivial deployments
- Agents need to find their colony even when colony IP changes (VPS restart,
  DHCP)

**Use cases affected:**

- Developer starting colony on laptop, connecting agents from other machines
- Production colony behind NAT needing to accept agent connections
- Multiple agents distributed across different networks/regions
- Colony failover or migration scenarios

## Solution

Build a simple gRPC service using Buf Connect that acts as a rendezvous point
for colonies and agents, similar to how Tailscale's coordination server works
but much simpler.

**Key Design Decisions:**

- **Buf Connect (gRPC)**: Type-safe, consistent with agent-colony protocol
    - Same protocol stack as rest of Coral architecture
    - Type-safe code generation from protobuf
    - HTTP/1.1 and HTTP/2 support (Connect protocol)
    - Easy to test with buf curl or grpcurl

- **In-memory storage**: Fast lookups, simple implementation for prototype
    - Single-process server sufficient for thousands of colonies
    - Can add Redis later for multi-instance redundancy
    - TTL-based expiration handles stale entries

- **No authentication (prototype)**: Security deferred to enable rapid
  prototyping
    - Mesh IDs provide namespace isolation
    - Authentication will be added in production-ready version
    - Focus on proving core functionality first

**Benefits:**

- Enables automatic discovery without manual configuration
- Works across NATs and firewalls (HTTP-friendly)
- Minimal operational complexity (separate, standalone binary)
- Low resource requirements ($5/mo VPS handles thousands of colonies)
- Uses non-standard WireGuard port (41820) to avoid conflicts with other WG solutions
- Independent deployment: Can be updated/scaled without affecting coral CLI

**Architecture Overview:**

```
┌─────────────────────────────────────────────┐
│         Discovery Service                   │
│         (discovery.coral.io)                │
│                                             │
│  In-Memory Registry:                        │
│  ┌────────────────────────────────────┐     │
│  │ mesh-123 → Colony Info             │     │
│  │   - pubkey: abc123...              │     │
│  │   - endpoints: [1.2.3.4:41820]     │     │
│  │   - last_seen: 2025-10-28T10:00    │     │
│  │   - ttl: 300                       │     │
│  └────────────────────────────────────┘     │
│                                             │
└────────────┬───────────────────┬────────────┘
             │                   │
    register │                   │ lookup
             │                   │
      ┌──────▼──────┐     ┌─────▼──────┐
      │   Colony    │     │   Agent    │
      │  mesh-123   │     │  mesh-123  │
      └─────────────┘     └────────────┘
```

### Component Changes

1. **Discovery Service** (new - separate binary):
    - **Separate binary**: `coral-discovery` (not part of `coral` CLI)
    - HTTP server listening on port 8080
    - Registration endpoints for colonies and agents
    - Lookup endpoints for finding colonies
    - In-memory map with TTL-based cleanup
    - Health check endpoint

2. **Colony** (integration):
    - Registers with discovery on startup
    - Sends periodic heartbeats to maintain registration
    - Publishes public key and endpoint(s)
    - Handles registration failures gracefully

3. **Agent** (integration):
    - Queries discovery for colony information
    - Retries with exponential backoff on failures
    - Caches colony information locally
    - Re-queries on connection failures

**Configuration Example:**

```yaml
# Colony config
discovery:
    endpoint: https://discovery.coral.io
    mesh_id: my-app-prod
    register_interval: 60s  # Heartbeat frequency
    timeout: 10s

# Agent config
discovery:
    endpoint: https://discovery.coral.io
    mesh_id: my-app-prod
    lookup_interval: 300s  # Re-lookup if colony changes
    timeout: 10s
    cache_ttl: 300s
```

## API Changes

### New Protobuf Service

**File: `proto/coral/discovery/v1/discovery.proto`**

```protobuf
syntax = "proto3";
package coral.discovery.v1;

import "google/protobuf/timestamp.proto";

// Note: While this is a separate binary, the proto package is still in the coral monorepo
option go_package = "github.com/coral-io/coral/proto/discovery/v1;discoverypb";

// Discovery service for colony and agent coordination
service DiscoveryService {
  // Register or update a colony's information
  rpc RegisterColony(RegisterColonyRequest) returns (RegisterColonyResponse);

  // Lookup colony information by mesh ID
  rpc LookupColony(LookupColonyRequest) returns (LookupColonyResponse);

  // Health check
  rpc Health(HealthRequest) returns (HealthResponse);
}

// Registration request
message RegisterColonyRequest {
  // Unique mesh identifier for this colony
  string mesh_id = 1;

  // WireGuard public key (base64 encoded)
  string pubkey = 2;

  // Colony endpoints (IP:port pairs)
  repeated string endpoints = 3;

  // Optional metadata
  map<string, string> metadata = 4;
}

message RegisterColonyResponse {
  // Registration successful
  bool success = 1;

  // TTL for this registration in seconds
  int32 ttl = 2;

  // When this registration expires
  google.protobuf.Timestamp expires_at = 3;
}

// Lookup request
message LookupColonyRequest {
  // Mesh ID to look up
  string mesh_id = 1;
}

message LookupColonyResponse {
  // Mesh ID
  string mesh_id = 1;

  // WireGuard public key
  string pubkey = 2;

  // Colony endpoints
  repeated string endpoints = 3;

  // Metadata
  map<string, string> metadata = 4;

  // Last seen timestamp
  google.protobuf.Timestamp last_seen = 5;
}

// Health check
message HealthRequest {}

message HealthResponse {
  // Service status
  string status = 1;

  // Service version
  string version = 2;

  // Uptime in seconds
  int64 uptime_seconds = 3;

  // Number of registered colonies
  int32 registered_colonies = 4;
}
```

### CLI Commands

```bash
# Discovery service (separate binary)
coral-discovery --port 8080

# Example output:
Discovery Service v0.1.0
gRPC server listening on :8080
Ready to accept registrations

# Colony registration (automatic)
coral colony start --mesh-id my-app-prod

# Example output:
Colony started
Registered with discovery service via gRPC
Mesh ID: my-app-prod
Endpoints: [203.0.113.42:41820]

# Agent lookup (automatic)
coral connect api --mesh-id my-app-prod

# Example output:
Looking up colony my-app-prod...
Found colony at 203.0.113.42:41820
Connecting...
Connected successfully

# Manual testing with buf curl
buf curl --protocol grpc \
  --data '{"mesh_id": "my-app-prod"}' \
  http://localhost:8080/coral.discovery.v1.DiscoveryService/LookupColony
```

## Implementation Plan

### Phase 1: Protocol Definition & Code Generation

- [ ] Create proto file structure (`proto/coral/discovery/v1/`)
- [ ] Define protobuf messages and service
- [ ] Configure buf.yaml for code generation
- [ ] Generate Go code with Buf Connect
- [ ] Commit generated code

### Phase 2: Core Service (Separate Binary)

- [ ] Create separate `cmd/coral-discovery` package structure
- [ ] Implement gRPC server using Buf Connect
- [ ] Add in-memory registry with mutex protection
- [ ] Implement RegisterColony RPC handler
- [ ] Implement LookupColony RPC handler
- [ ] Add Health RPC handler
- [ ] Build separate `coral-discovery` binary

### Phase 3: TTL & Cleanup

- [ ] Add TTL tracking to registry entries
- [ ] Implement background cleanup goroutine
- [ ] Add configurable TTL values
- [ ] Handle registration updates (refresh TTL)

### Phase 4: Integration

- [ ] Add discovery client library (shared by colony/agent)
- [ ] Integrate registration in colony startup
- [ ] Implement periodic heartbeat in colony
- [ ] Integrate lookup in agent connection flow
- [ ] Add retry logic with exponential backoff

### Phase 5: Testing & Documentation

- [ ] Add unit tests for registry operations
- [ ] Add integration tests for gRPC endpoints
- [ ] Add end-to-end test (colony register → agent lookup)
- [ ] Document protobuf API
- [ ] Add deployment guide for discovery service

## Testing Strategy

### Unit Tests

- Registry operations (add, get, delete, cleanup)
- TTL expiration logic
- Concurrent access (multiple goroutines)
- Edge cases (empty mesh_id, invalid pubkey)

### Integration Tests

- gRPC endpoint validation
- Protobuf request/response serialization
- Error handling and gRPC status codes
- Concurrent registrations from multiple colonies
- Connect protocol compatibility (HTTP/1.1 and HTTP/2)

### E2E Tests

- Full workflow: colony registers → agent looks up → connection succeeds
- TTL expiration and re-registration
- Multiple colonies with different mesh IDs
- Colony endpoint updates

## Migration Strategy

**Deployment Steps:**

1. Deploy discovery service on public VPS
2. Update colony to include discovery configuration
3. Update agent to query discovery service
4. Test with development environment first
5. Roll out to production colonies

**Rollback Plan:**

- Discovery service is optional in this prototype
- Colonies can still accept direct connections
- Fall back to manual endpoint configuration
- No data loss (stateless service)

## Future Enhancements

**Security (post-prototype):**

- API key authentication for registration
- Rate limiting per mesh_id
- TLS/HTTPS enforcement
- Mesh-ID ownership verification (prevent hijacking)

**Reliability:**

- Redis backend for multi-instance redundancy
- Prometheus metrics export
- Structured logging
- Geographic distribution (regional discovery services)

**Features:**

- Agent registration (for colony → agent queries)
- Peer-to-peer agent discovery
- Endpoint quality metrics (latency, success rate)
- WebSocket for real-time updates

## Appendix

### Protocol Details

**Registration Lifecycle:**

1. Colony starts up, generates Wireguard keypair
2. Colony POSTs to `/v1/colony/register` with pubkey and endpoints
3. Discovery service stores entry with TTL=300s
4. Colony sends heartbeat every 60s to refresh TTL
5. If colony stops sending heartbeats, entry expires after 300s

**Lookup Flow:**

1. Agent needs to connect to mesh-id "my-app-prod"
2. Agent GETs `/v1/colony/lookup/my-app-prod`
3. Discovery returns colony pubkey and endpoints
4. Agent caches response (5 minutes)
5. Agent establishes Wireguard connection to colony

**Endpoint Priority:**

- Multiple endpoints support dual-stack (IPv4/IPv6)
- Agents try all endpoints in order
- First successful connection wins

### Data Structure

```go
// In-memory registry
type Registry struct {
    mu      sync.RWMutex
    entries map[string]*Entry
}

type Entry struct {
    MeshID    string    `json:"mesh_id"`
    PubKey    string    `json:"pubkey"`
    Endpoints []string  `json:"endpoints"`
    Metadata  map[string]string `json:"metadata"`
    LastSeen  time.Time `json:"last_seen"`
    ExpiresAt time.Time `json:"expires_at"`
}
```

### Example Deployment

```bash
# Run discovery service
./coral-discovery --port 8080 --ttl 300

# Or with Docker
docker run -p 8080:8080 coral/discovery

# Or with systemd
systemctl start coral-discovery
```

---

## Notes

**Design Philosophy:**

- Start simple, add complexity only when needed
- Optimize for developer experience (easy to understand, debug)
- Fail gracefully (cached lookups, retries)
- No single point of failure (colonies work without discovery if needed)
- Separate binary for operational independence (deploy, scale, update independently)

**Why Not Use Existing Solutions:**

- Tailscale coordination server: Too complex, proprietary protocol
- Consul: Heavyweight, requires its own cluster
- etcd: Similar weight, more than we need
- DNS: Doesn't handle dynamic endpoint updates well

**Why Buf Connect instead of plain HTTP REST:**

- Type safety from protobuf (compile-time validation)
- Consistent with agent-colony protocol (same tooling)
- Better performance than JSON over HTTP
- Built-in support for streaming (future enhancement)
- Easier to generate clients for other languages

**Port Selection:**

- **Discovery service**: 8080 (gRPC/HTTP, configurable)
- **Colony WireGuard**: 41820 (default, avoids conflict with standard WireGuard port 51820)
  - Standard WireGuard uses 51820, which conflicts with Talos Linux KubeSpan and other solutions
  - 41820 is clearly different but still recognizable as WireGuard-related
  - Can be overridden via configuration if needed

**Prototype Limitations:**

- No authentication (anyone can register/lookup)
- No persistence (restart = lost registrations)
- Single instance only (no redundancy)
- No rate limiting (vulnerable to abuse)

These limitations are acceptable for prototyping and will be addressed in the
production version.
