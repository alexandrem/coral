---
rfd: "046"
title: "Discovery Service on Cloudflare Workers"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: false
dependencies: [ "001", "023" ]
database_migrations: [ ]
areas: [ "networking", "discovery", "infrastructure" ]
---

# RFD 046 - Discovery Service on Cloudflare Workers

**Status:** 🚧 Draft

## Summary

Migrate the Coral discovery service from a self-hosted Go-based gRPC server to
Cloudflare Workers with Durable Objects. This leverages Cloudflare's global edge
network for lower latency, automatic scaling, and zero operational overhead while
maintaining API compatibility with existing colonies and agents.

## Problem

**Current behavior/limitations:**

- Discovery service requires self-hosted infrastructure (VPS, K8s cluster, etc.)
- Single-region deployment introduces latency for global users
- Manual scaling and monitoring required
- No built-in DDoS protection or edge caching
- Infrastructure costs and operational burden

**Why this matters:**

- Coral is designed for distributed deployments across multiple regions
- Discovery lookups are latency-sensitive (agent startup time)
- Small teams shouldn't need infrastructure expertise to run discovery
- High availability requires multi-region deployment (complex, expensive)
- STUN endpoint detection benefits from geographic proximity

**Use cases affected:**

- Global colonies connecting to centralized discovery (high latency from distant regions)
- Small teams running self-hosted discovery (operational burden)
- High-traffic deployments needing auto-scaling
- DDoS attacks against discovery service (no built-in protection)

## Solution

Deploy the discovery service on Cloudflare Workers using:

- **Cloudflare Workers**: Serverless edge compute (handles HTTP/gRPC requests)
- **Durable Objects**: Strongly consistent storage for colony/agent registry
- **Workers KV**: Optional caching layer for high-traffic lookups
- **Protobuf + Connect**: Maintain existing gRPC-compatible protocol

**Key Design Decisions:**

- **TypeScript Implementation**: Workers use V8 JavaScript runtime, reimplement
  server in TypeScript
- **Durable Objects for Registry**: Provides strong consistency, automatic
  replication, and transactional updates
- **Connect Protocol Over HTTP/1.1**: Workers support HTTP/1.1 natively, Connect
  protocol works without HTTP/2
- **Separate Relay Infrastructure**: TURN-like relay forwarding requires UDP
  packet handling, keep as separate service (not in Workers)
- **Global Edge Deployment**: Workers automatically deploy to 300+ global edge
  locations

**Benefits:**

- **Zero Infrastructure**: No servers, load balancers, or databases to manage
- **Global Low Latency**: Requests routed to nearest edge location (<50ms
  worldwide)
- **Auto-Scaling**: Handles 0 to 1M requests/sec without configuration
- **DDoS Protection**: Built-in Cloudflare DDoS mitigation
- **Cost-Effective**: Free tier covers most deployments, pay-per-use scales
  linearly
- **High Availability**: Multi-region replication built-in
- **API Compatibility**: Existing colonies/agents work without changes

**Architecture Overview:**

```
┌─────────────────────────────────────────────────────────────┐
│  Cloudflare Global Network (300+ Locations)                 │
│                                                              │
│  ┌────────────────────────────────────────────────────────┐ │
│  │  Workers (Edge Compute)                                │ │
│  │  - HTTP request handling                               │ │
│  │  - Protobuf parsing (Connect protocol)                 │ │
│  │  - Request routing to Durable Objects                  │ │
│  │  - Response serialization                              │ │
│  └───────────────────┬────────────────────────────────────┘ │
│                      │                                       │
│  ┌───────────────────▼────────────────────────────────────┐ │
│  │  Durable Objects (Globally Replicated State)          │ │
│  │  - Registry: colony/agent registrations               │ │
│  │  - TTL-based expiration                                │ │
│  │  - Split-brain detection                               │ │
│  │  - Transactional updates                               │ │
│  └────────────────────────────────────────────────────────┘ │
│                                                              │
│  ┌────────────────────────────────────────────────────────┐ │
│  │  Workers KV (Optional Cache)                           │ │
│  │  - Read-heavy lookup caching                           │ │
│  │  - Eventual consistency (acceptable for lookups)       │ │
│  └────────────────────────────────────────────────────────┘ │
└──────────────────────┬───────────────────┬───────────────────┘
                       │                   │
              ┌────────▼──────┐    ┌──────▼────────┐
              │   Colony      │    │   Agent       │
              │  (unchanged)  │    │  (unchanged)  │
              └───────────────┘    └───────────────┘
```

### Component Changes

1. **Discovery Worker** (new - TypeScript):
   - Handles Connect protocol HTTP requests
   - Parses protobuf messages (same proto definitions)
   - Routes requests to Durable Objects
   - Implements all existing RPC handlers (RegisterColony, LookupColony, etc.)
   - Observed endpoint extraction from HTTP headers

2. **Durable Objects - Registry** (new - TypeScript):
   - Stores colony/agent registrations with TTL
   - Implements split-brain detection (same logic as Go version)
   - Automatic cleanup of expired entries
   - Strongly consistent read/write operations
   - Handles relay session allocation (placeholder)

3. **Protobuf Definitions** (unchanged):
   - Same proto files (`coral/discovery/v1/discovery.proto`)
   - Generate TypeScript code using protobuf-ts or similar
   - Maintain API compatibility

4. **Colony/Agent Clients** (unchanged):
   - Existing Go clients continue to work
   - Discovery endpoint changes to `discovery.coral.workers.dev` or custom domain
   - No code changes required

**Configuration Example:**

```bash
# Colony config (unchanged)
coral colony start --discovery=https://discovery.coral.workers.dev

# Agent config (unchanged)
coral agent start --discovery=https://discovery.coral.workers.dev
```

## Implementation Plan

### Phase 1: Project Setup & Protobuf

- [ ] Create `workers/discovery/` directory structure
- [ ] Set up TypeScript + Wrangler (Cloudflare Workers CLI)
- [ ] Generate TypeScript protobuf code from existing proto files
- [ ] Configure Durable Objects bindings in `wrangler.toml`
- [ ] Set up local development environment (Miniflare)

### Phase 2: Core Worker Implementation

- [ ] Implement HTTP request handler with Connect protocol parsing
- [ ] Add protobuf message serialization/deserialization
- [ ] Implement `RegisterColony` handler (delegates to Durable Object)
- [ ] Implement `LookupColony` handler
- [ ] Implement `Health` handler
- [ ] Add observed endpoint extraction (X-Forwarded-For, CF-Connecting-IP)

### Phase 3: Durable Objects Registry

- [ ] Implement Durable Object class for registry storage
- [ ] Add colony/agent registration with TTL tracking
- [ ] Implement split-brain detection logic
- [ ] Add automatic expiration cleanup (scheduled alarm)
- [ ] Implement relay session allocation (placeholder)

### Phase 4: Agent Registration Support

- [ ] Implement `RegisterAgent` handler
- [ ] Implement `LookupAgent` handler
- [ ] Extend Durable Object to support agent lookups
- [ ] Add agent-specific metadata handling

### Phase 5: Testing & Deployment

- [ ] Add unit tests (Vitest or similar)
- [ ] Add integration tests against local Durable Objects
- [ ] Test with existing Go clients (compatibility check)
- [ ] Deploy to Cloudflare Workers staging environment
- [ ] Load testing (verify auto-scaling)
- [ ] Production deployment with custom domain

### Phase 6: Migration & Monitoring

- [ ] Deploy to production (`discovery.coral.io` custom domain)
- [ ] Update documentation to point to Workers endpoint
- [ ] Set up Cloudflare Analytics dashboards
- [ ] Add structured logging (Logpush to external service)
- [ ] Gradual migration (blue/green or canary deployment)

## API Changes

No API changes required. Existing protobuf definitions remain unchanged:

- `RegisterColony` / `RegisterColonyResponse`
- `LookupColony` / `LookupColonyResponse`
- `RegisterAgent` / `RegisterAgentResponse`
- `LookupAgent` / `LookupAgentResponse`
- `Health` / `HealthResponse`
- `RequestRelay` / `RequestRelayResponse` (placeholder)
- `ReleaseRelay` / `ReleaseRelayResponse` (placeholder)

**Cloudflare-Specific Headers:**

Workers will use Cloudflare-provided headers for enhanced functionality:

```http
CF-Connecting-IP: 203.0.113.45  # Real client IP (more reliable than X-Forwarded-For)
CF-Ray: 7f1a8c2b3d4e5f6g        # Request trace ID (for debugging)
CF-IPCountry: US                 # Client country code (for future region routing)
```

## Technical Deep-Dive

### Durable Objects vs Workers KV

**Why Durable Objects for Registry:**

- **Strong Consistency**: Critical for split-brain detection (prevent duplicate
  mesh_id registrations)
- **Transactional Updates**: Atomic read-modify-write for registration updates
- **Single-Writer Guarantee**: Only one Durable Object instance handles writes
  for a given ID
- **Automatic Replication**: Cloudflare handles global replication

**Why NOT Workers KV:**

- **Eventual Consistency**: KV can return stale data (unacceptable for
  split-brain checks)
- **No Transactions**: Cannot atomically check + update registrations
- **60-second propagation**: Updates take up to 60s to replicate globally

**Optional KV Use Case:**

- Cache lookup responses for high-traffic colonies (1M+ lookups/hour)
- Eventual consistency acceptable for lookups (agent retries if connection fails)
- Reduces Durable Object read load

### Connect Protocol on Workers

Cloudflare Workers support HTTP/1.1 natively. Connect protocol works over
HTTP/1.1 using standard POST requests:

```http
POST /coral.discovery.v1.DiscoveryService/RegisterColony HTTP/1.1
Host: discovery.coral.workers.dev
Content-Type: application/proto
Accept: application/proto

[protobuf binary payload]
```

No HTTP/2 or gRPC-specific transport required. Existing Go clients using Buf
Connect will work without modification.

### TypeScript Protobuf Libraries

Options for TypeScript protobuf code generation:

1. **protobuf-ts**: Full TypeScript support, good Connect protocol integration
2. **protoc-gen-es**: Buf's official TypeScript plugin (modern, type-safe)
3. **ts-proto**: Popular, generates clean TypeScript interfaces

Recommendation: **protoc-gen-es** for consistency with Buf tooling.

### TTL and Expiration Handling

Durable Objects don't have built-in TTL. Implement using:

```typescript
class RegistryDurableObject {
  // Use Durable Object Alarms for periodic cleanup
  async alarm() {
    const now = Date.now();
    // Remove expired entries
    for (const [meshId, entry] of this.entries) {
      if (entry.expiresAt < now) {
        this.entries.delete(meshId);
      }
    }
    // Schedule next cleanup (every 60 seconds)
    await this.storage.setAlarm(Date.now() + 60000);
  }

  async register(meshId: string, ...) {
    const entry = {
      meshId,
      expiresAt: Date.now() + this.ttl,
      ...
    };
    await this.storage.put(`colony:${meshId}`, entry);
  }
}
```

### Observed Endpoint Extraction

Cloudflare Workers provide reliable client IP via `CF-Connecting-IP` header:

```typescript
function extractObservedEndpoint(request: Request): Endpoint | null {
  // Cloudflare-provided header (most reliable)
  const connectingIP = request.headers.get('CF-Connecting-IP');
  if (connectingIP && isValidPublicIPv4(connectingIP)) {
    return { ip: connectingIP, port: 0, protocol: 'udp' };
  }

  // Fallback to X-Forwarded-For (standard proxy header)
  const xForwardedFor = request.headers.get('X-Forwarded-For');
  if (xForwardedFor) {
    const clientIP = xForwardedFor.split(',')[0].trim();
    if (isValidPublicIPv4(clientIP)) {
      return { ip: clientIP, port: 0, protocol: 'udp' };
    }
  }

  return null;
}
```

### Relay Functionality

TURN-like relay forwarding requires UDP packet handling, which Workers cannot do
(HTTP/WebSocket only). Keep relay as separate infrastructure:

- **Option 1**: Self-hosted relay servers (Go binary) in key regions
- **Option 2**: Use existing TURN infrastructure (coturn, etc.)
- **Option 3**: Defer relay to future RFD (not critical for MVP)

Discovery Workers return relay endpoint metadata (IP, port) but actual packet
forwarding happens elsewhere.

## Testing Strategy

### Unit Tests

- Protobuf message parsing and serialization
- Durable Object registration logic
- TTL expiration and cleanup
- Split-brain detection edge cases
- Observed endpoint extraction

### Integration Tests

- Full request/response cycle with local Durable Objects (Miniflare)
- Colony registration → lookup flow
- Agent registration → lookup flow
- Concurrent registration attempts (split-brain)
- TTL expiration and re-registration

### Compatibility Tests

- Existing Go colony client → Workers discovery
- Existing Go agent client → Workers discovery
- Protobuf compatibility (Go client ↔ TypeScript server)
- Connect protocol over HTTP/1.1

### Load Testing

- 10K colonies registering simultaneously
- 100K agent lookups per second
- Verify auto-scaling (no manual intervention)
- Measure p50, p95, p99 latency globally

## Cost Analysis

**Cloudflare Workers Pricing:**

- **Free Tier**: 100K requests/day, 10ms CPU time/request
- **Paid**: $5/month + $0.50 per million requests

**Durable Objects Pricing:**

- **Free Tier**: 1M reads/month, 100K writes/month
- **Paid**: $0.20 per million reads, $1.00 per million writes

**Estimated Costs (1K colonies, 10K agents):**

| Activity                     | Requests/Month | Cost      |
| ---------------------------- | -------------- | --------- |
| Colony registration (60s)    | 432K           | Free      |
| Agent lookups (startup)      | 300K           | Free      |
| Health checks                | 100K           | Free      |
| **Total**                    | **832K**       | **$0/mo** |

**Scale: 100K colonies, 1M agents:**

| Activity                  | Requests/Month | Cost    |
| ------------------------- | -------------- | ------- |
| Colony registration       | 43M            | $21.50  |
| Agent lookups             | 30M            | $15.00  |
| Health checks             | 10M            | $5.00   |
| Durable Object operations | 50M reads      | $10.00  |
| **Total**                 |                | **$51** |

Compare to self-hosted:

- **VPS (DigitalOcean, 4 regions)**: $80/mo (4 × $20 droplets)
- **AWS (multi-region ALB + EC2)**: $200+/mo
- **Cloudflare Workers**: $0-50/mo (scales automatically)

## Security Considerations

- **DDoS Protection**: Cloudflare's built-in DDoS mitigation protects Workers
- **Rate Limiting**: Use Workers rate limiting API to prevent abuse
- **Authentication**: Future enhancement (API keys, mTLS) works same as Go version
- **Observed Endpoints**: CF-Connecting-IP more reliable than X-Forwarded-For (
  harder to spoof)
- **Split-Brain Protection**: Durable Objects strong consistency ensures only one
  colony per mesh_id

## Migration Strategy

**Deployment Steps:**

1. **Deploy Workers to staging** (`discovery-staging.coral.workers.dev`)
2. **Test with internal colonies/agents** (validate compatibility)
3. **Deploy to production custom domain** (`discovery.coral.io` via DNS CNAME)
4. **Gradual rollout**:
   - 10% of colonies → Workers (canary)
   - Monitor latency, error rates
   - 50% traffic split
   - 100% cutover
5. **Decommission Go discovery server** after 30 days

**Rollback Plan:**

- Keep Go-based discovery running in parallel during migration
- DNS switch back to Go server if Workers have issues
- No data loss (stateless service, registrations expire naturally)
- Agents retry on connection failure (transparent to users)

**Blue/Green Deployment:**

- Run Go discovery at `discovery-legacy.coral.io`
- Run Workers at `discovery.coral.io`
- Colonies can specify endpoint via config flag
- Test Workers thoroughly before DNS switchover

## Future Enhancements

**Advanced Routing:**

- Use `CF-IPCountry` header for region-aware colony recommendations
- Return nearest colony when multiple regions available
- Latency-based routing hints

**Analytics Dashboard:**

- Cloudflare Analytics for request volume, latency
- Custom metrics via Workers Analytics Engine
- Alert on error rate spikes

**Multi-Tenant Isolation:**

- Namespace colonies by organization ID
- Per-org rate limiting and quotas
- Billing integration for paid tiers

**Relay on Cloudflare:**

- Investigate Cloudflare Spectrum (TCP/UDP proxying) for relay infrastructure
- Alternative: Workers + WebSockets for relay signaling only

## Appendix

### Example Durable Object Implementation

```typescript
export class RegistryDurableObject {
  state: DurableObjectState;
  env: Env;
  entries: Map<string, Entry> = new Map();
  ttl: number = 300_000; // 5 minutes in milliseconds

  constructor(state: DurableObjectState, env: Env) {
    this.state = state;
    this.env = env;
    // Load entries from storage on initialization
    this.loadEntries();
  }

  async loadEntries() {
    const stored = await this.state.storage.list({ prefix: 'colony:' });
    for (const [key, value] of stored) {
      this.entries.set(key, value as Entry);
    }
  }

  async register(req: RegisterRequest): Promise<RegisterResponse> {
    const now = Date.now();
    const meshId = req.meshId;

    // Split-brain detection
    const existing = this.entries.get(`colony:${meshId}`);
    if (existing && existing.expiresAt > now) {
      if (existing.pubkey !== req.pubkey) {
        throw new Error(
          `Colony ${meshId} already registered with different pubkey`
        );
      }
    }

    const entry: Entry = {
      meshId,
      pubkey: req.pubkey,
      endpoints: req.endpoints,
      lastSeen: now,
      expiresAt: now + this.ttl,
      observedEndpoint: req.observedEndpoint,
    };

    this.entries.set(`colony:${meshId}`, entry);
    await this.state.storage.put(`colony:${meshId}`, entry);

    // Schedule cleanup alarm if not already set
    const currentAlarm = await this.state.storage.getAlarm();
    if (!currentAlarm) {
      await this.state.storage.setAlarm(now + 60_000); // 1 minute
    }

    return {
      success: true,
      ttl: this.ttl / 1000,
      expiresAt: entry.expiresAt,
    };
  }

  async lookup(meshId: string): Promise<Entry> {
    const entry = this.entries.get(`colony:${meshId}`);
    if (!entry) {
      throw new Error(`Colony not found: ${meshId}`);
    }

    if (entry.expiresAt < Date.now()) {
      throw new Error(`Colony registration expired: ${meshId}`);
    }

    return entry;
  }

  async alarm() {
    // Cleanup expired entries
    const now = Date.now();
    let removed = 0;

    for (const [key, entry] of this.entries) {
      if (entry.expiresAt < now) {
        this.entries.delete(key);
        await this.state.storage.delete(key);
        removed++;
      }
    }

    // Schedule next cleanup
    await this.state.storage.setAlarm(now + 60_000);
  }
}
```

### Wrangler Configuration

```toml
# wrangler.toml
name = "coral-discovery"
main = "src/index.ts"
compatibility_date = "2025-01-01"

[durable_objects]
bindings = [
  { name = "REGISTRY", class_name = "RegistryDurableObject" }
]

[[migrations]]
tag = "v1"
new_classes = ["RegistryDurableObject"]

[observability]
enabled = true
head_sampling_rate = 1.0  # Sample 100% of requests (adjust in production)

# Custom domain (after deployment)
routes = [
  { pattern = "discovery.coral.io/*", zone_name = "coral.io" }
]
```

### Project Structure

```
workers/
├── discovery/
│   ├── src/
│   │   ├── index.ts              # Worker entry point
│   │   ├── registry.ts           # Durable Object implementation
│   │   ├── handlers/
│   │   │   ├── colony.ts         # RegisterColony, LookupColony
│   │   │   ├── agent.ts          # RegisterAgent, LookupAgent
│   │   │   ├── health.ts         # Health check
│   │   │   └── relay.ts          # Relay placeholder
│   │   ├── proto/
│   │   │   └── discovery.ts      # Generated TypeScript protobuf
│   │   └── utils/
│   │       ├── endpoint.ts       # Observed endpoint extraction
│   │       └── validation.ts     # IP validation
│   ├── test/
│   │   ├── registry.test.ts
│   │   └── integration.test.ts
│   ├── wrangler.toml
│   ├── package.json
│   └── tsconfig.json
└── README.md
```

---

## Implementation Status

**Core Capability:** ⏳ Not Started

This RFD is in the design phase. Implementation will begin after approval.

## Deferred Features

The following features are deferred as they are not critical for initial Workers deployment:

**Relay Forwarding** (Future - Separate Infrastructure)

- TURN-like packet relay requires UDP handling
- Workers cannot forward arbitrary UDP packets
- Consider Cloudflare Spectrum or self-hosted relay servers

**Workers KV Caching** (Low Priority)

- Optional performance optimization for high-traffic lookups
- Adds complexity without clear benefit for MVP
- Revisit if Durable Object read costs become significant

**Multi-Tenant Features** (Future Enhancement)

- Organization-scoped namespaces
- Per-org rate limiting and quotas
- Billing integration

---

**References:**

- [Cloudflare Workers Docs](https://developers.cloudflare.com/workers/)
- [Durable Objects Guide](https://developers.cloudflare.com/durable-objects/)
- [Connect Protocol Spec](https://connectrpc.com/docs/protocol/)
- [RFD 001 - Discovery Service](./001-discovery-service.md)
- [RFD 023 - STUN NAT Traversal](./023-stun-discovery-nat-traversal.md)
