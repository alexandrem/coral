---
rfd: "039"
title: "CLI-to-Agent Direct Mesh Connectivity"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: ["007", "019"]
database_migrations: ["cli_peer_allocations"]
areas: ["cli", "networking", "wireguard", "security"]
---

# RFD 039 - CLI-to-Agent Direct Mesh Connectivity

**Status:** 🚧 Draft

**Date:** 2025-11-16

## Summary

Enable CLI tools to establish direct WireGuard mesh connections to agents for commands requiring high-bandwidth data transfer or low-latency access (e.g., `coral duckdb`, `coral agent debug`, `coral agent logs`). This requires colony-orchestrated peer configuration updates to agents' WireGuard AllowedIPs and persistent mesh IP allocation for CLI tools.

## Problem

**Current behavior/limitations**

- **Existing pattern (RFD 005)**: CLI tools connect to colonies via local proxy, which forwards gRPC requests over the mesh. This works for colony RPC APIs but not for direct agent access.
- **Agent isolation**: Agents only peer with colony in star topology (RFD 007). Agents' WireGuard config has `AllowedIPs = 10.42.0.1/32` (colony only).
- **Commands needing direct agent access**:
  - `coral duckdb shell agent-123` (RFD 038) - Query agent DuckDB via HTTP
  - `coral agent debug agent-123` (future) - Direct debugging tools
  - `coral agent logs agent-123 --follow` (future) - Streaming logs
  - `coral agent exec agent-123 <cmd>` (RFD 017) - Could bypass colony relay for efficiency

**Why existing patterns don't work**

1. **Colony relay is inefficient for data-plane operations**:
   - DuckDB queries can transfer GB-scale data (agent metrics exports)
   - Colony becomes bottleneck for large transfers
   - Extra hop adds latency
   - Colony must proxy HTTP/WebSocket streams

2. **WireGuard AllowedIPs prevents direct access**:
   ```
   CLI (10.42.0.99) ──X──> Agent (10.42.0.15)
                           ↑
                           Agent only allows: 10.42.0.1/32 (colony)
   ```
   Even if CLI peers with colony and gets mesh IP, agent's WireGuard config blocks CLI's packets.

3. **No CLI mesh identity**:
   - CLI tools don't have persistent mesh IPs
   - Each invocation would need new IP allocation
   - No mechanism for colony to track CLI peers

**Why this matters**

- **Performance**: Direct agent access eliminates colony bottleneck for data-heavy operations.
- **Scalability**: Colony doesn't proxy GB-scale data transfers.
- **Latency**: Single hop instead of two (CLI → Colony → Agent).
- **Feature enablement**: Commands like `coral duckdb` cannot work without direct connectivity.

**Use cases affected**

- Operator querying agent metrics: `coral duckdb shell agent-prod-1`
- SRE debugging agent: `coral agent debug agent-prod-1` (captures network traffic, queries eBPF maps)
- DevOps streaming logs: `coral agent logs agent-prod-1 --follow`
- Developer running exec commands: `coral agent exec agent-prod-1 tcpdump`

## Solution

Introduce **transient mesh peers** for CLI tools with colony-orchestrated peer configuration. CLI tools join the mesh with ephemeral IPs, and colony dynamically updates target agents' WireGuard AllowedIPs to permit CLI access for the session duration.

**Key Design Decisions:**

1. **Transient CLI Mesh Peers** (not persistent):
   - CLI tools get temporary mesh IPs for command duration
   - IPs allocated from ephemeral pool (e.g., `10.42.255.0/24`)
   - Peer configuration torn down after command completes
   - Rationale: CLI tools are short-lived, don't need persistent IPs

2. **Colony Orchestrates AllowedIPs Updates**:
   - CLI requests direct access to agent via colony RPC
   - Colony updates agent's WireGuard config to add CLI's mesh IP to AllowedIPs
   - Agent's WireGuard reloads config (no connection interruption)
   - CLI can now directly connect to agent over mesh

3. **Security via Colony Authorization**:
   - CLI must authenticate with colony first (existing auth)
   - Colony verifies CLI has permission to access target agent
   - Colony issues time-limited "mesh access token" for agent
   - Agent validates token on first connection (out of scope for this RFD - assumes trusted mesh)

4. **Graceful Cleanup**:
   - CLI notifies colony on exit to remove its AllowedIP from agent
   - Colony also garbage collects stale CLI peers (timeout-based)
   - Prevents AllowedIPs list from growing indefinitely

**Benefits:**

- ✅ Direct agent connectivity for high-bandwidth operations
- ✅ No colony bottleneck for data transfers
- ✅ Reuses existing WireGuard mesh infrastructure
- ✅ Minimal changes to agents (dynamic config reload)
- ✅ Secure: Colony mediates all mesh access

**Architecture Overview:**

```
┌────────────────────────────────────────────────────────────────┐
│  CLI Tool (Developer Machine)                                  │
│                                                                │
│  1. coral duckdb shell agent-prod-1                            │
│                                                                │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  CLI Mesh Client                                         │  │
│  │  - Generate WireGuard keypair                            │  │
│  │  - Request mesh access from colony                       │  │
│  │  - Receive ephemeral mesh IP (10.42.255.5)               │  │
│  │  - Create WireGuard interface                            │  │
│  │  - Peer with colony (10.42.0.1)                          │  │
│  │  - Add agent to AllowedIPs (10.42.0.15/32)               │  │
│  └──────────────────────────────────────────────────────────┘  │
│                    │                                           │
│                    │ WireGuard mesh                            │
└────────────────────┼───────────────────────────────────────────┘
                     │
                     │
    ┌────────────────┼─────────────────┐
    │                │                 │
    ▼                ▼                 ▼
┌─────────┐   ┌──────────────┐   ┌──────────────┐
│ Colony  │   │  Agent       │   │  Agent       │
│         │   │  (prod-1)    │   │  (prod-2)    │
│ Mesh IP:│   │  Mesh IP:    │   │  Mesh IP:    │
│10.42.0.1│   │  10.42.0.15  │   │  10.42.0.16  │
│         │   │              │   │              │
│  Peers: │   │  Peers:      │   │  Peers:      │
│  - All  │   │  - Colony    │   │  - Colony    │
│  agents │   │    10.42.0.1 │   │    10.42.0.1 │
│  - CLI  │   │  - CLI ✨    │   │              │
│  peers  │   │    10.42.255.5│  │              │
└─────────┘   └──────────────┘   └──────────────┘
                     ▲
                     │
                     └─ Colony updates agent's AllowedIPs
                        to permit CLI mesh IP
```

### Component Changes

1. **CLI: Mesh Client Library**:
   - Add `internal/cli/mesh/client.go` - Creates transient WireGuard peers
   - Generate ephemeral WireGuard keypair on demand
   - Request mesh access from colony via new RPC
   - Configure WireGuard interface with assigned ephemeral IP
   - Cleanup on exit or SIGINT

2. **Colony: Mesh Access Coordinator**:
   - New RPC: `RequestAgentAccess(agent_id, cli_pubkey) → (cli_mesh_ip, access_token)`
   - Allocate ephemeral mesh IP from reserved pool (`10.42.255.0/24`)
   - Update target agent's WireGuard config via gRPC
   - Track active CLI peers in database
   - Garbage collect stale CLI peers (timeout after 1 hour)

3. **Colony: Database Schema**:
   - New table: `cli_peer_allocations`
   - Columns: `cli_id`, `mesh_ip`, `target_agent_id`, `allocated_at`, `expires_at`
   - Track which CLI tools have access to which agents
   - Cleanup on expiry

4. **Agent: Dynamic Peer Management**:
   - New RPC: `UpdatePeerAllowedIPs(peer_pubkey, allowed_ips)` (called by colony)
   - Reload WireGuard config without restarting device
   - Add/remove AllowedIPs for CLI peers dynamically
   - No interruption to existing connections

**Configuration Example:**

```yaml
# Colony config
mesh:
  ephemeral_ip_pool: "10.42.255.0/24"  # Reserved for CLI tools
  cli_peer_ttl: 3600  # 1 hour

# Agent config (no changes)
# Agents receive peer updates via RPC from colony
```

## Implementation Plan

### Phase 1: Colony Mesh Access Coordinator

- [ ] Create database table `cli_peer_allocations`
- [ ] Implement ephemeral IP allocator (separate pool from agent IPs)
- [ ] Add `RequestAgentAccess` RPC to colony service
- [ ] Add garbage collection for expired CLI peers

### Phase 2: Agent Dynamic Peer Management

- [ ] Add `UpdatePeerAllowedIPs` RPC to agent service
- [ ] Implement WireGuard config reload without restart
- [ ] Add validation for colony-initiated peer updates

### Phase 3: CLI Mesh Client Library

- [ ] Create `internal/cli/mesh/client.go`
- [ ] Implement WireGuard keypair generation
- [ ] Implement mesh access request flow
- [ ] Add WireGuard interface creation and configuration
- [ ] Add cleanup handlers (exit, SIGINT, SIGTERM)

### Phase 4: Integration and Testing

- [ ] Update `coral duckdb` to use CLI mesh client (RFD 038)
- [ ] Add E2E test: CLI connects to agent, queries DuckDB
- [ ] Add E2E test: Multiple CLI tools access same agent
- [ ] Add E2E test: CLI cleanup on exit
- [ ] Add E2E test: Garbage collection of stale peers

## API Changes

### New Colony RPC

```protobuf
service ColonyService {
  // Request direct mesh access to an agent.
  rpc RequestAgentAccess(RequestAgentAccessRequest) returns (RequestAgentAccessResponse);

  // Release agent access (cleanup).
  rpc ReleaseAgentAccess(ReleaseAgentAccessRequest) returns (ReleaseAgentAccessResponse);
}

message RequestAgentAccessRequest {
  // Target agent ID.
  string agent_id = 1;

  // CLI tool's WireGuard public key.
  string cli_pubkey = 2;

  // CLI identifier (for tracking/audit).
  string cli_id = 3;

  // Access duration (optional, default: 1 hour).
  int32 duration_seconds = 4;
}

message RequestAgentAccessResponse {
  // Assigned ephemeral mesh IP for CLI.
  string cli_mesh_ip = 1;

  // Agent's mesh IP (for convenience).
  string agent_mesh_ip = 2;

  // Access token (future: for agent-side validation).
  string access_token = 3;

  // Expiry timestamp.
  google.protobuf.Timestamp expires_at = 4;
}

message ReleaseAgentAccessRequest {
  // Agent ID to release access to.
  string agent_id = 1;

  // CLI mesh IP to remove.
  string cli_mesh_ip = 2;
}

message ReleaseAgentAccessResponse {
  bool success = 1;
}
```

### New Agent RPC

```protobuf
service AgentService {
  // Update WireGuard peer AllowedIPs (colony-only).
  rpc UpdatePeerAllowedIPs(UpdatePeerAllowedIPsRequest) returns (UpdatePeerAllowedIPsResponse);
}

message UpdatePeerAllowedIPsRequest {
  // Peer public key (CLI's WireGuard pubkey).
  string peer_pubkey = 1;

  // New AllowedIPs to set.
  repeated string allowed_ips = 2;

  // Action: "add" or "remove".
  string action = 3;
}

message UpdatePeerAllowedIPsResponse {
  bool success = 1;
}
```

### CLI Usage

```bash
# CLI automatically handles mesh connection
coral duckdb shell agent-prod-1

# Internal flow:
# 1. Generate WireGuard keypair
# 2. colony.RequestAgentAccess(agent_id="agent-prod-1", cli_pubkey="...")
# 3. Receive cli_mesh_ip=10.42.255.5, agent_mesh_ip=10.42.0.15
# 4. Create WireGuard interface with IP 10.42.255.5
# 5. Peer with colony (10.42.0.1)
# 6. DuckDB ATTACH 'http://10.42.0.15:9001/duckdb/metrics.duckdb'
# 7. Interactive shell
# 8. On exit: colony.ReleaseAgentAccess(agent_id, cli_mesh_ip)
```

### Database Schema

```sql
-- CLI peer allocations
CREATE TABLE IF NOT EXISTS cli_peer_allocations (
    cli_id TEXT NOT NULL,
    mesh_ip TEXT NOT NULL UNIQUE,
    target_agent_id TEXT NOT NULL,
    cli_pubkey TEXT NOT NULL,
    allocated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NOT NULL,
    PRIMARY KEY (cli_id, target_agent_id)
);

CREATE INDEX idx_cli_mesh_ip ON cli_peer_allocations(mesh_ip);
CREATE INDEX idx_cli_expires_at ON cli_peer_allocations(expires_at);
CREATE INDEX idx_cli_target_agent ON cli_peer_allocations(target_agent_id);
```

## Testing Strategy

### Unit Tests

**Colony Mesh Access Coordinator:**
- Ephemeral IP allocation from reserved pool
- Concurrent allocation safety
- IP reuse after release
- Expiry-based garbage collection

**Agent Dynamic Peer Management:**
- WireGuard config update without restart
- Add/remove AllowedIPs correctly
- Validation rejects non-colony requests

### Integration Tests

**CLI Mesh Connection:**
- CLI successfully requests agent access
- WireGuard interface created with ephemeral IP
- Direct connectivity to agent verified
- Cleanup removes peer configuration

**Multi-CLI Scenario:**
- Multiple CLI tools access same agent simultaneously
- Each gets unique ephemeral IP
- No IP conflicts
- AllowedIPs accumulate correctly

### E2E Tests

**End-to-end DuckDB query:**
- Start agent with Beyla metrics
- CLI runs `coral duckdb shell agent-123`
- CLI queries metrics successfully
- CLI exits cleanly
- Agent's AllowedIPs cleaned up

**Garbage collection:**
- CLI crashes without cleanup
- Colony garbage collector removes stale peer after timeout
- Agent's AllowedIPs cleaned up automatically

## Security Considerations

**Authentication:**
- CLI must authenticate with colony first (existing auth)
- Colony verifies CLI identity before granting mesh access
- Colony controls which agents CLI can access (RBAC in future)

**Authorization:**
- Colony mediates all mesh access (no rogue CLI peers)
- Agents only accept `UpdatePeerAllowedIPs` RPC from colony
- Future: Agent validates access tokens from colony

**Audit:**
- All mesh access requests logged in colony
- Track which CLI accessed which agent and when
- Useful for security forensics

**IP Exhaustion:**
- Ephemeral pool (`10.42.255.0/24`) provides 254 IPs
- Garbage collection prevents exhaustion
- Alert when pool >80% utilized

**Denial of Service:**
- Rate limit `RequestAgentAccess` RPC per CLI identity
- Limit active CLI peers per agent (e.g., max 10)
- Prevent malicious CLI from exhausting AllowedIPs

## Migration Strategy

### Deployment

**Phase 1: Colony update (backward compatible)**
1. Deploy colony with new RPC endpoints
2. No impact to existing agents or CLI tools
3. New endpoints available but unused

**Phase 2: Agent update (backward compatible)**
1. Deploy agents with `UpdatePeerAllowedIPs` RPC
2. Agents continue normal operation
3. New RPC available but unused

**Phase 3: CLI update (feature activation)**
1. Update CLI tools with mesh client library
2. Commands like `coral duckdb` now use direct connectivity
3. Existing proxy-based commands continue working

### Rollback Plan

1. **Revert CLI binaries** to previous version
2. CLI tools fall back to proxy-based access
3. No data loss or configuration changes needed
4. Colony/agent updates are backward compatible

## Relationship to RFD 019

**RFD 019 (Persistent IP Allocation)** addresses persistent IPs for **agents**.

**RFD 039 (this RFD)** addresses **ephemeral IPs for CLI tools**.

**Key differences:**

| Aspect | RFD 019 (Agents) | RFD 039 (CLI Tools) |
|--------|------------------|---------------------|
| **Lifetime** | Persistent (survive restarts) | Ephemeral (per-session) |
| **IP Pool** | `10.42.0.0/24` | `10.42.255.0/24` |
| **Storage** | DuckDB (permanent) | DuckDB (TTL-based) |
| **Reuse** | Same agent = same IP | Each session = new IP |
| **Cleanup** | Manual deregistration | Automatic (timeout) |

**Dependency:** This RFD depends on RFD 019 for the persistent allocator infrastructure, which is extended to support ephemeral allocation.

## Future Enhancements

**Persistent CLI IPs (optional):**
- Operators' workstations could get persistent mesh IPs
- Stored in `~/.coral/mesh_identity`
- Useful for frequently-used CLI tools
- Reduces allocation overhead

**Direct Agent-to-Agent Connectivity:**
- Extend pattern to allow agents to peer with each other
- Useful for distributed tracing, service mesh features
- Colony orchestrates AllowedIPs for agent pairs

**RBAC for Mesh Access:**
- Colony enforces role-based access control
- E.g., developers can only access dev agents
- SREs can access prod agents
- Integrated with future auth system (RFD 020, RFD 022)

**Mesh Access Audit Logs:**
- Export audit events to SIEM
- Track all mesh access requests
- Compliance requirements

## Appendix

### Ephemeral vs Persistent IPs

**Why ephemeral for CLI tools?**
- CLI sessions are short-lived (minutes)
- Persistent IPs would accumulate over time
- Cleanup is simpler with TTL-based expiry
- No state to manage across CLI invocations

**When to use persistent CLI IPs:**
- Long-running CLI tools (e.g., dashboards)
- Operators' workstations (identity-based)
- Future enhancement, not in scope for this RFD

### WireGuard AllowedIPs Limits

WireGuard has no hard limit on AllowedIPs count, but practical limits:
- Linux: Tested up to 10,000+ AllowedIPs per peer (minimal performance impact)
- Colony: Typically <1,000 agents
- CLI peers: Typically <50 concurrent (ephemeral pool size: 254)
- Total AllowedIPs per agent: Colony (1) + CLI peers (max 10) = ~11

No scaling issues expected.

### Alternative Approaches Considered

**1. Proxy everything through colony (current)**
- Pros: Simple, no mesh complexity in CLI
- Cons: Colony bottleneck, latency, inefficient for data transfers
- Verdict: Not suitable for DuckDB, logs, large data

**2. VPN bastion pattern**
- Pros: Industry standard (SSH bastion)
- Cons: Extra infrastructure, doesn't leverage WireGuard mesh
- Verdict: Adds complexity without benefit

**3. Agent-side HTTP proxy**
- Pros: No mesh changes
- Cons: Exposes agent data without authentication
- Verdict: Security risk

**Chosen approach (transient mesh peers):**
- Leverages existing WireGuard infrastructure
- Colony mediates access (secure)
- Direct connectivity (efficient)
- Clean lifecycle (ephemeral)
