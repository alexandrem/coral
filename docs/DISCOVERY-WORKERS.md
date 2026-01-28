# Coral Discovery Workers Architecture

This document describes the design and implementation of the new Discovery
Service built on Cloudflare Workers.

## Overview

The Discovery Service handles coordination between Coral colonies and agents. It
provides a registry where colonies advertise their presence and agents can
discover connectivity information.

By leveraging Cloudflare Workers, the service achieves global distribution and
low latency without the overhead of managing specialized infrastructure.

## Component Architecture

- **Worker Core (`index.ts`)**: The entry point that routes incoming HTTP
  requests to specific handlers.
- **RPC Handlers (`src/handlers/`)**: Implements individual RPC methods like
  `RegisterColony`, `LookupColony`, and `CreateBootstrapToken`.
- **Colony Registry (`src/registry.ts`)**: A Cloudflare Durable Object that
  manages state for a specific mesh.
- **Crypto Module (`src/crypto.ts`)**: Handles cryptographic operations using
  the Web Crypto API.

## API Design

The service implements the `coral.discovery.v1.DiscoveryService` protocol using
the **Buf Connect** protocol over HTTP.

### Protocol Details

- **Encoding**: Currently supports **JSON encoding** (
  `connect.WithProtoJSON()`).
- **Endpoints**: Standard Connect paths (e.g.,
  `/coral.discovery.v1.DiscoveryService/RegisterColony`).
- **Protobuf Types**:
    - `Timestamp`: Expected as RFC 3339 strings.
    - `bytes`: Expected as Base64 encoded strings.

## Storage Model: Durable Objects & SQLite

The service uses **Durable Objects (DO)** to ensure strong consistency and
provide persistent storage via **SQLite**.

### Routing Logic

- Requests are routed to a specific Durable Object based on the `mesh_id`.
- `env.COLONY_REGISTRY.idFromName(meshId)` is used to ensure all operations for
  a given mesh happen in the same instance.

### Schema

The Durable Object maintains two primary tables in its SQLite storage:

1. **`colonies`**: Stores colony metadata, public keys, and connectivity
   endpoints.
2. **`agents`**: Stores agent registration details and observed endpoints.

Internal cleanup is handled via the DO **Alarm API**, which periodically removes
expired registrations based on TTL.

## Cryptography & Dependencies

### Web Crypto vs. Wasm (`coral-crypto`)

Currently, the service **exclusively uses the Web Crypto API in production**.

- **Active: Web Crypto API**: Handles all Ed25519 signing and JWT operations.
  This provides the best performance and integrates with Cloudflare's secure key
  management.
- **Reference: `coral-crypto` (Wasm)**: A TinyGo-compiled module that shares the
  exact same cryptographic code as the Go agents. It acts as a reference
  implementation to ensure that signatures are byte-for-byte compatible between
  the TypeScript worker and Go agents.

#### Toggling Cryptographic Implementations

The service includes a feature toggle to switch between these implementations.
This is primarily used as a consistency safeguard.

| Environment Variable | Value             | Description                                             |
|:---------------------|:------------------|:--------------------------------------------------------|
| `USE_WASM_CRYPTO`    | `true`            | Enables the `coral-crypto` Wasm module for JWT signing. |
| `USE_WASM_CRYPTO`    | `false` (default) | Uses the native Web Crypto API.                         |

**Key Point:** When running on Cloudflare, the service defaults to native Web
Crypto for speed. The `coral-crypto` Wasm module eliminates potential
implementation drift if native APIs vary.

### Key Management

- **Signing Keys**: Configured via the `DISCOVERY_SIGNING_KEY` secret.
- **JWK Format**: Keys are imported into the Web Crypto runtime using the JWK (
  JSON Web Key) format for compatibility.
- **JWKS**: The service exposes a `/.well-known/jwks.json` endpoint for public
  key distribution and token verification.

#### Generating a Signing Key

Use the `gen-signing-key` tool from `coral-crypto` to generate a new Ed25519
signing key:

```bash
# From the coral-discovery-workers directory:
go run ../coral-crypto/cmd/gen-signing-key -format json 2>/dev/null > signing-key

```

This outputs a JSON object with `id` (ULID) and `privateKey` (base64-encoded
64-byte Ed25519 key). To deploy it as a Cloudflare secret:

```bash
cat signing-key | npx wrangler secret put DISCOVERY_SIGNING_KEY
```

## Local Development & E2E

For local testing in the E2E environment, the service runs using **Wrangler Dev
** within a Docker container.

- **Secret Simulation**: Secrets are injected via the `.dev.vars` file.
- **Connectivity**: Local agents communicate with the worker via JSON-encoded
  Connect requests.

## Reference Implementation

See [Discovery](https://github.com/coral-mesh/discovery/)
