---
rfd: "107"
title: "Discovery Introspection & Admin API"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: false
dependencies: [ "001", "023" ]
database_migrations: [ ]
areas: [ "networking", "discovery", "infrastructure" ]
---

# RFD 107 - Discovery Introspection & Admin API

**Status:** 🚧 Draft

## Summary

As the Discovery service moves to a distributed "sharded" model on Cloudflare
Workers, global visibility becomes a challenge. This RFD defines an **Admin RPC
layer** and an **Enhanced Status Handler** to allow operators to introspect the
state of specific colony registries, verify lease health, and debug
cryptographic Wasm failures in real-time without managing global database
clusters.

## Problem

- **Visibility Gap**: In a Durable Object (DO) architecture, state is
  partitioned. There is no single central database to query; data for a specific
  colony lives in a DO instance that could be physically located in any
  Cloudflare data center.

- **Debugging Complexity**: If an agent fails to join, it is difficult to
  determine if the failure occurred at the Wasm signature layer, the DO storage
  layer, or due to a specific policy violation in RFD 086.

- **Operational Recovery**: Operators lack tools to manually evict stale leases
  or "unlock" a colony ID if a Root CA fingerprint needs to be rotated or a
  security incident occurs.

## Solution

The solution utilizes **Cloudflare Workers RPC** (released in 2024/2025) to
create a secure, typed communication channel between an administrative CLI and
the specific stateful shards.

### 1\. Registry-Aware `/status` Handler

The public `/status` endpoint is enhanced to provide a hierarchical health
check:

- **Global Tier**: Returns Wasm module health (heap usage, initialization
  status) and Worker version.

- **Shard Tier**: When invoked with a `colony_id` query parameter, the Worker
  identifies the correct Durable Object and fetches its specific shard health (
  active lease count, database size, and "pinned" CA fingerprint).

### 2\. Admin RPC Entrypoints

We will define a `DiscoveryAdmin` class that extends `WorkerEntrypoint`. This
allows the `coral-admin` CLI to call methods directly on the Worker over a
secure, authenticated bridge.

**Key Design Decisions:**

- **Secret-Based Auth**: Admin RPCs are gated by a `DISCOVERY_ADMIN_KEY` stored
  in Cloudflare Secrets.

- **Direct DO Introspection**: The Admin RPC can "reach into" any Durable Object
  to call internal diagnostic methods like `getDebugMetadata()` which are not
  exposed to the public internet.

- **Wasm Diagnostic Hooks**: Exported functions from the Go-Wasm module to
  report the current version of the security logic being executed.

## Architecture Overview

```
Operator (CLI)  → [ Discovery Admin RPC ] (Authenticated)
                        ↓
User (Agent)    → [ Discovery Public API ]
                        ↓
                  [ Durable Object Shard ] ← [ SQLite / Storage ]
                        ↳ [ Admin Debug Methods ]
```

## Implementation Plan

### Phase 1: Enhanced Status Handler

- [ ] Implement a Hono-based `/status` router.

- [ ] Integrate Wasm memory metrics (e.g., `exports.get_memory_stats()`).

- [ ] Implement shard-routing for `/status?colony_id=...`.

### Phase 2: Core Admin RPCs

- [ ] Define `DiscoveryAdmin` entrypoint in `src/index.ts`.

- [ ] **RPC: `listActiveLeases(colony_id)`**: Returns a list of agents and their
  TTLs from the specific DO.

- [ ] **RPC: `getColonySecurity(colony_id)`**: Returns the pinned Root CA
  fingerprint and policy version.

- [ ] **RPC: `evictLease(colony_id, agent_id)`**: Force-expires a lease and
  triggers an immediate DO Alarm for cleanup.

### Phase 3: Observability Integration

- [ ] Enable **Cloudflare Workers Logs** (Invocation-based grouping).

- [ ] Implement **Tail Worker** for security auditing (logging all signature
  failures to a central sink).

## API Changes

### Admin RPC Interface (TypeScript)

TypeScript

```
import { WorkerEntrypoint } from "cloudflare:workers";

export class DiscoveryAdmin extends WorkerEntrypoint {
  // Admin-only: Fetch summary for a specific colony shard
  async getColonySummary(colonyId: string) {
    const id = this.env.REGISTRY.idFromName(colonyId);
    const stub = this.env.REGISTRY.get(id);
    return await stub.getInternalDiagnostics(); // Internal RPC to DO
  }
}
```

### CLI Commands

Bash

```
# Inspect a specific colony registry
$ coral-admin discovery inspect --colony=prod-us-west

Colony: prod-us-west
  Status: Healthy
  Pinned Fingerprint: sha256:e3b0c4...
  Active Leases: 1,245
  Storage Usage: 4.2 MB (SQLite)
  Last Policy Sync: 2026-01-20
```

## Future Work

- **Admin Dashboard UI**: A Cloudflare Pages-hosted React app that visualizes
  the "Global Colony Map" by querying the Admin RPCs.

- **Historical Analytics**: Exporting lease counts to **Cloudflare Analytics
  Engine** to track growth trends over time.
