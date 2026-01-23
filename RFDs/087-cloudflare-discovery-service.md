---
rfd: "087"
title: "Discovery Service on Cloudflare Workers"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: [ "001", "023" ]
database_migrations: [ ]
areas: [ "networking", "discovery", "infrastructure" ]
---

# RFD 087 - Discovery Service on Cloudflare Workers

**Status:** 🚧 Draft

## Summary

Migrate the Coral discovery service from a self-hosted Go-based server to
**Cloudflare Workers** using **Durable Objects**. This shift replaces custom
STUN/TURN infrastructure with Cloudflare-native utilities, significantly
reducing operational costs while providing out-of-the-box High Availability (HA)
and global scalability.

**API Compatibility:** The Cloudflare Workers implementation exposes the existing
`DiscoveryService` API via **buf connect protocol** (HTTP/JSON + HTTP/2 binary).
No changes to `proto/coral/discovery/v1/discovery.proto` are required. Existing
Go clients using buf connect will work without modification.

**Dependencies:**

- **RFD 001** (Coral Architecture): Defines the discovery service role in the mesh.
- **RFD 023** (NAT Traversal): Establishes STUN/relay patterns this RFD builds upon.

## Problem

- **Current behavior/limitations**: Running a globally distributed discovery
  service requires managing multiple VPS instances, load balancers, and custom
  STUN/TURN binaries to assist NAT traversal.

- **Why this matters**: Self-hosting HA infrastructure is expensive and
  operationally complex. For small teams, the "maintenance tax" of discovery
  often outweighs its utility. Custom STUN implementations are also prone to
  regional performance inconsistencies.

- **Use cases affected**: Low-budget bootstrap deployments, global fleets
  requiring <50ms discovery latency, and restricted NAT environments where
  custom STUN packets are often dropped by aggressive firewalls.

## Solution

A serverless discovery architecture that leverages Cloudflare's edge for compute
and its native STUN capabilities for endpoint detection.

**Key Design Decisions:**

- **Durable Objects for HA State**: Instead of a central SQL database, each
  `mesh_id` is routed to a specific Durable Object instance. This provides
  **strong consistency** for registrations with zero-config HA.

- **Cloudflare Native STUN Integration**: Leverage Cloudflare's globally
  distributed STUN endpoints (e.g., via `stun.cloudflare.com`) instead of
  maintaining a custom Go-based STUN listener.

- **Cost-Optimized Leases**: Use Durable Object **Alarms** to handle registry
  cleanups. This ensures we only pay for compute when a colony is actually
  active, avoiding the "idle server" costs of a VPS.

- **Wasm Cryptographic Bridge**: Compile the Go identity logic to Wasm to ensure
  Referral Tickets are signed identically at the edge as they are on the Colony.
  Keep the Wasm module **stateless** and either re-instantiate per request or use
  a global instance pool to avoid memory leaks across thousands of DO invocations.
  While Workers support `FinalizationRegistry` for Wasm cleanup, stateless design
  is safer for high-throughput discovery workloads.

**Benefits:**

- **Extreme Cost Efficiency**: The Free/Paid tier of Workers (\$5/mo) typically
  covers millions of discovery requests, costing ~90% less than a multi-region
  VPS setup.

- **Simplified NAT Traversal**: Using Cloudflare's native STUN infrastructure
  ensures high-reliability endpoint detection that is often "whitelisted" by
  corporate firewalls.

- **Automatic Scalability**: Handles sudden bursts (e.g., 10k agents starting
  simultaneously) without manual infrastructure scaling.

**Architecture Overview:**

```
Agent/Colony → [ Cloudflare Edge Worker ]
                     ↓          ↳ [ Cloudflare Native STUN ] (IP Detection)
             [ TinyGo Wasm ]      (Signing Logic)
                     ↓
             [ Durable Object ]   (Strongly Consistent Registry)
```

### Component Changes

1. **Discovery Worker** (TypeScript):

    - Replaces the Go gRPC listener.

    - Uses `CF-Connecting-IP` and `stun.cloudflare.com` hints to return the most
      accurate "Observed Endpoint" to the colony.

    - **Observed Address Priority**: The Worker treats `CF-Connecting-IP` as the
      authoritative source. If the agent's self-reported STUN address is a
      private/LAN IP (RFC 1918), the Worker overrides it with `CF-Connecting-IP`.

2. **Registry Store** (Durable Objects):

    - Acts as the stateful anchor.

    - Implements **Split-Brain protection**: Ensures only one Colony can
      register a specific `mesh_id` across the entire global network.

    - **Storage**: Use `this.ctx.storage.sql.exec()` SQLite API (GA) instead of
      legacy Key-Value storage. SQLite enables efficient queries like
      `SELECT * FROM agents WHERE mesh_id = ?` for multi-agent lookups.

    - **Alarm Batching**: Each DO supports only one scheduled alarm. For
      high-density registries, use a single periodic alarm that triggers a
      cleanup batch query: `DELETE FROM registrations WHERE expires_at < ?`.

3. **Go Agent/Colony**:

    - Updated to use the Cloudflare STUN endpoint for local NAT discovery before
      hitting the Discovery service.

4. **Buf Connect Protocol Layer** (TypeScript):

    - Implement connect protocol handlers for `DiscoveryService` RPCs using
      `@connectrpc/connect`.

    - Generate TypeScript types from `proto/coral/discovery/v1/discovery.proto`.

    - Support both JSON (Connect) and binary (gRPC-Web) content types for client
      flexibility.

    - **Runtime Requirement**: Enable `nodejs_compat` flag (v2 or later) in
      `wrangler.toml` for `Buffer` and advanced stream handling required by
      Connect's binary gRPC-Web compatibility.

    - **Zone Configuration**: Enable "gRPC" in Cloudflare Network settings to
      allow Workers to handle `application/grpc` content-types for full gRPC
      support (not just Connect/gRPC-Web).

**Scope Clarification - Relay RPCs:**

The `RequestRelay` and `ReleaseRelay` RPCs require actual relay infrastructure
(TURN-like servers) which cannot run on Cloudflare Workers. This RFD covers:

- ✅ `RegisterColony`, `LookupColony`, `RegisterAgent`, `LookupAgent`, `Health`
- ✅ `CreateBootstrapToken` (stateless JWT issuance)
- ❌ `RequestRelay`, `ReleaseRelay` (deferred - requires separate relay infrastructure)

Relay functionality may be addressed in a future RFD or continue using existing
infrastructure.

## Implementation Plan

### Phase 1: Infrastructure Setup

- [ ] Configure `wrangler.toml` with Durable Object bindings, Alarms, and
  compatibility flags.

- [ ] Set up a custom domain with Cloudflare Proxy enabled for the Discovery
  endpoint.

- [ ] Enable "gRPC" in Cloudflare Zone Network settings.

**Example `wrangler.toml`:**

```toml
name = "coral-discovery"
compatibility_date = "2026-01-01"
compatibility_flags = ["nodejs_compat_v2"]

[[durable_objects.bindings]]
name = "COLONY_REGISTRY"
class_name = "ColonyRegistry"

[[migrations]]
tag = "v1"
new_sqlite_classes = ["ColonyRegistry"]
```

### Phase 2: Core Registry Logic

- [ ] Implement the `ColonyRegistry` class with transactional `register()` and
  `lookup()` methods.

- [ ] Integrate the TinyGo Wasm module for JWS/JWKS parity.

### Phase 3: NAT Traversal Simplification

- [ ] Update the Discovery API to accept STUN-discovered addresses from the
  agent.

- [ ] Implement fallback to `CF-Connecting-IP` when STUN is blocked.

### Phase 4: Testing & Validation

- [ ] Add unit tests for Durable Object state management (register/lookup/expiry).

- [ ] Add integration tests for buf connect protocol compliance.

- [ ] Test STUN fallback behavior when primary STUN is blocked.

- [ ] Validate Go client compatibility with Workers endpoint.

- [ ] Load test with simulated burst registration (1k+ concurrent requests).

## API Changes

### CLI Commands

```bash
# No change to existing CLI, only the endpoint updates
coral colony start --discovery=https://discovery.coralmesh.dev
```

### Configuration Changes

- `discovery.provider`: Set to `cloudflare-workers`.

- `discovery.stun_server`: Defaults to `stun.cloudflare.com:3478`.

## Security Considerations

- **OOB Trust Anchor**: As established in previous discussions, the agent MUST
  use an **Out-of-Band (OOB) Fingerprint** to validate the Colony CA. This
  ensures that even if a Cloudflare Worker account is compromised, the attacker
  cannot steer traffic to a malicious colony.

- **DDoS Mitigation**: By moving to Workers, we gain Cloudflare's L3/L4 DDoS
  protection automatically, which is critical for a public-facing Discovery
  service.

## Implementation Status

**Core Capability:** ⏳ Not Started (Design Phase)

## Repository Structure

The discovery service lives in a **separate repository** (`coral-discovery-workers`)
to maintain clean separation between the Go monorepo and Cloudflare-specific tooling.

**Rationale:**

- **Different toolchains**: TypeScript/Wrangler vs Go/Make
- **Independent deployment**: Workers deploy via `wrangler publish`, decoupled from
  coral release cycles
- **Cloudflare-specific CI**: Preview deployments, secrets management, zone
  configuration

### Cryptographic Code Synchronization

To ensure signature/verification parity between the Go colony and the TinyGo Wasm
module, extract shared crypto logic into a dedicated module:

```
github.com/coral-mesh/coral-crypto   # Shared module
├── go.mod
├── identity/
│   ├── jwt.go          # JWS signing/verification
│   ├── jwks.go         # JWKS handling
│   └── fingerprint.go  # Certificate fingerprints
└── wasm/
    └── main.go         # TinyGo entrypoint for Wasm build
```

Both repositories import the same pinned version:

```go
// coral/go.mod
require github.com/coral-mesh/coral-crypto v0.3.0

// coral-discovery-workers/wasm/go.mod
require github.com/coral-mesh/coral-crypto v0.3.0
```

### Wasm Build Process

```makefile
# coral-discovery-workers/Makefile
.PHONY: wasm
wasm:
	cd wasm && tinygo build -o ../src/crypto.wasm -target wasm -no-debug ./main.go
```

### CI Parity Testing

To detect version drift, coral's CI publishes cryptographic test vectors that the
workers repo validates against:

```typescript
// coral-discovery-workers/test/crypto-parity.test.ts
import { describe, test, expect } from 'vitest';
import { loadWasmModule } from '../src/wasm-loader';

describe('Wasm crypto parity', () => {
  test('signature matches Go reference vectors', async () => {
    const vectors = await fetch(
      'https://coral-test-vectors.dev/jwt-sign-v1.json'
    ).then((r) => r.json());

    const wasm = await loadWasmModule();
    for (const v of vectors.cases) {
      const result = wasm.sign(v.privateKey, v.payload);
      expect(result).toBe(v.expectedSignature);
    }
  });
});
```

**Test vector generation** (coral CI):

```yaml
# coral/.github/workflows/test-vectors.yml
- name: Generate crypto test vectors
  run: go run ./cmd/gen-test-vectors -out vectors/
- name: Publish vectors
  uses: actions/upload-artifact@v4
  with:
    name: crypto-test-vectors
    path: vectors/
```

This ensures any change to `coral-crypto` is validated against both implementations
before release.

## Future Work

**Multi-Tenant Org Isolation**

- Namespacing Durable Objects by `org_id` to allow per-customer rate limits and
  custom trust anchors.

**Relay Infrastructure**

- Separate RFD for TURN-like relay servers to support `RequestRelay`/`ReleaseRelay`
  RPCs for symmetric NAT traversal.
