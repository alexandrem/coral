---
rfd: "019"
title: "Persistent IP Allocation and Elimination of Temporary IP Pattern"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: false
dependencies: [ ]
related_rfds: [ "007" ]
database_migrations: [ "001-agent-ip-allocations" ]
areas: [ "networking", "colony", "agent" ]
---

# RFD 019 - Persistent IP Allocation and Elimination of Temporary IP Pattern

**Status:** 🚧 Draft

## Summary

Eliminate the hardcoded temporary IP pattern (`10.42.255.254`) used during agent
WireGuard mesh setup. Replace with persistent, centralized IP allocation where
the colony assigns permanent IPs during initial registration, stored in DuckDB.

This removes race conditions, platform-specific route manipulation, and timing
dependencies while improving registration reliability and speed. IP allocations
persist across colony restarts, and agents reconnecting with the same ID receive
their previously allocated IP.

**Note**: This RFD does not address the security vulnerability of plaintext
`colony_secret` transmission during registration. Authentication improvements
will be addressed in a future RFD.

## Problem

### Current Behavior

When agents connect to a colony, they follow this sequence:

1. Query discovery service over regular internet to get colony endpoint.
2. Create WireGuard device without an IP address.
3. Add colony as WireGuard peer with its public endpoint and allowed IPs.
4. Assign hardcoded temporary IP `10.42.255.254` to enable route creation (OS
   requires IP on interface before routes can be added).
5. Register with colony **over regular internet** (not through mesh) and receive
   permanent IP assignment.
6. Manually flush all routes using platform-specific commands to clear cached
   source IP.
7. Reassign permanent IP to the interface.
8. Refresh peer routes with new source IP.
9. Test mesh connectivity to colony's mesh IP.

### Issues

1. **Unnecessary Temporary IP**: The temporary IP (`10.42.255.254`) exists only
   because the current code adds the colony as a WireGuard peer BEFORE
   registration. Since registration happens over regular internet (not the
   mesh), the peer could be added AFTER registration with the real mesh IP,
   eliminating the need for a temporary IP entirely.

2. **IP Collision Risk**: Multiple agents starting simultaneously receive
   identical temporary IPs, potentially causing routing conflicts during the
   setup window.

3. **Platform-Specific Route Manipulation**: Requires kernel-level route
   flushing with platform-specific commands:
   ```bash
   route -n delete -host <peer-ip>  # macOS
   ```
   The kernel caches the temporary IP as the source address, necessitating
   explicit deletion.

4. **Timing Dependencies**: Hardcoded sleep delays (200ms, 300ms, 500ms) to
   allow kernel operations to complete.

5. **No Persistence**: IP allocations exist only in colony's in-memory
   registry. Colony restart loses all IP assignments, requiring full mesh
   reconfiguration.

6. **Race Condition Window**: Between temporary IP assignment and permanent IP
   allocation, agents are in an inconsistent state vulnerable to setup failures.

**Note on Security**: Agent registration currently sends `colony_secret` over
plain HTTP, creating a man-in-the-middle vulnerability. This RFD does not
address authentication security, which will be handled in a separate RFD focused
on secure agent authentication.

### Impact

- **Slow Registration**: Agent setup takes 500ms+ longer than necessary due to
  delays and route manipulation.
- **Colony Restarts Break Mesh**: All existing mesh connections lost on colony
  restart, requiring all agents to re-register.
- **Platform Lock-in**: Platform-specific code limits portability.
- **Concurrent Setup Issues**: Multiple agents starting simultaneously may
  experience routing conflicts during the temporary IP window.
- **Operational Complexity**: Route flushing and timing delays make the setup
  process fragile and hard to debug.

## Solution

### Key Design Decisions

1. **Persistent Storage in Colony**: Store IP allocations in DuckDB, enabling
   recovery after restarts and providing an audit trail.

2. **Permanent IP Assignment During Registration**: Colony allocates and returns
   the permanent IP in the initial `RegisterAgent` response. Agent receives its
   mesh IP before any WireGuard peer configuration.

3. **Reorder WireGuard Setup**: Create WireGuard interface and register BEFORE
   adding the colony as a peer. This eliminates the need for a temporary IP
   since the interface can be configured with its permanent mesh IP before any
   routes are created.

4. **No Temporary IPs**: Agents configure WireGuard interfaces with permanent
   IPs immediately, eliminating the need for route flushing.

5. **Atomic Allocation**: Mutex-protected IP allocation prevents concurrent
   registration conflicts.

6. **IP Reuse on Reconnection**: Agents reconnecting with the same ID receive
   their previously allocated IP.

### Benefits

- **Faster Registration**: Remove 500ms+ of delays and route operations.
- **Platform Independence**: Eliminate macOS/Linux route command dependencies.
- **Zero IP Conflicts**: Atomic allocation prevents collisions.
- **Persistent Allocations**: 100% recovery after colony restarts.
- **Simpler Code**: Remove ~160 lines of complex cleanup code.
- **Better Reliability**: No race conditions or timing dependencies.
- **Improved Debugging**: Simpler flow makes issues easier to diagnose.

### Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                     Colony (Public IP)                          │
│                                                                 │
│  ┌────────────────┐         ┌───────────────────────────────┐  │
│  │ Registration   │         │  IP Allocator Service         │  │
│  │ Handler        │────────▶│                               │  │
│  │ (Port 9000)    │         │  - In-memory allocator        │  │
│  │                │         │  - Mutex protection           │  │
│  │ - Validate     │         │  - DuckDB persistence         │  │
│  │   credentials  │         │  - Subnet: 10.42.0.0/16       │  │
│  │ - Allocate IP  │         └───────────────────────────────┘  │
│  └────────────────┘                       │                    │
│         ▲                                 ▼                    │
│         │                   ┌────────────────────────────┐    │
│         │                   │  DuckDB                    │    │
│         │                   │                            │    │
│         │                   │  agent_ip_allocations      │    │
│         │                   │  ├─ agent_id (PK)          │    │
│         │                   │  ├─ ip_address (UNIQUE)    │    │
│         │                   │  ├─ allocated_at           │    │
│         │                   │  └─ last_seen              │    │
│         │                   └────────────────────────────┘    │
│         │                                                      │
│         │ (1) RegisterAgent(colony_secret, agent_id, pubkey)  │
│         │                                                      │
│         │ (2) RegisterResponse(accepted, assigned_ip)         │
│         ▼                                                      │
└─────────────────────────────────────────────────────────────────┘
          │
          │
          ▼
┌─────────────────────────────────────────────────────────────────┐
│                         Agent                                   │
│                                                                 │
│  NEW Flow:                                                      │
│  1. Create WireGuard interface (no IP, no peers yet)            │
│  2. RegisterAgent(colony_secret) over HTTP                      │
│  3. Receive permanent mesh IP (10.42.0.2)                       │
│  4. Assign IP to WireGuard interface                            │
│  5. Add colony as WireGuard peer (routes created correctly)     │
│  6. Test mesh connectivity to colony's mesh IP                  │
│                                                                 │
│  ✅ No temporary IP, no route flushing, no delays               │
│  ✅ Persistent IPs survive colony restarts                      │
└─────────────────────────────────────────────────────────────────┘
```

### Component Changes

#### Colony: Persistent IP Allocator

Add database-backed IP allocator with the following capabilities:

- **Atomic Allocation**: Thread-safe IP assignment with mutex protection.
- **Database Persistence**: All allocations written to DuckDB immediately.
- **Allocation Recovery**: Load existing allocations from database on startup.
- **IP Reuse Detection**: Lookup existing allocation for reconnecting agents.
- **Sequential Assignment**: Continue using sequential allocation from
  `10.42.0.0/16` subnet.

#### Colony: Database Schema

**Migration**: `001-agent-ip-allocations.sql`

```sql
CREATE TABLE IF NOT EXISTS agent_ip_allocations
(
    agent_id
    TEXT
    PRIMARY
    KEY,
    ip_address
    TEXT
    NOT
    NULL
    UNIQUE,
    allocated_at
    TIMESTAMP
    NOT
    NULL
    DEFAULT
    CURRENT_TIMESTAMP,
    last_seen
    TIMESTAMP
    NOT
    NULL
    DEFAULT
    CURRENT_TIMESTAMP
);

CREATE INDEX idx_ip_address ON agent_ip_allocations (ip_address);
CREATE INDEX idx_last_seen ON agent_ip_allocations (last_seen);
```

**Schema Details**:

- `agent_id`: Primary key, unique identifier for each agent.
- `ip_address`: Assigned IP (unique constraint prevents duplicates).
- `allocated_at`: Timestamp of initial allocation.
- `last_seen`: Updated on each reconnection (for future lease management).

#### Colony: Registry Integration

The colony registry will integrate the persistent allocator:

- Replace in-memory allocator with persistent version.
- Load allocations from database during startup.
- Use persistent allocator for all IP assignments.
- Maintain backward compatibility with existing allocation logic.

#### Agent: Simplified Registration Flow

Agents register with colony and receive permanent IP before configuring
WireGuard:

- **Create WireGuard Interface**: No IP or peers configured yet.

- **Register with Colony**:
    - Send `RegisterAgent(colony_secret, agent_id, wireguard_pubkey)` over HTTP
    - Colony validates credentials and allocates IP from database

- **Configure Mesh**:
    - Receive permanent mesh IP (10.42.0.x) in response
    - Assign IP to WireGuard interface
    - Add colony as peer (routes created with correct source IP)
    - Test mesh connectivity

- **Benefits**:
    - No temporary IP assignment
    - No route flushing required
    - No platform-specific commands
    - No timing delays
    - IP persists across colony restarts

## Implementation Plan

### Phase 1: Add Persistent Storage (Non-Breaking)

- [ ] Create database migration `001-agent-ip-allocations.sql`.
- [ ] Implement persistent IP allocator with database backing.
- [ ] Add database storage methods (allocate, release, lookup, load).
- [ ] Add unit tests for persistent allocator.
- [ ] Integration: Store allocations in DB alongside existing in-memory
  allocator.
- [ ] No behavior changes yet - dual write for validation.

### Phase 2: Colony Startup Recovery

- [ ] Integrate persistent allocator into colony registry.
- [ ] Load allocations on colony startup.
- [ ] Add logging for IP recovery metrics.
- [ ] Test colony restart with pre-existing allocations.
- [ ] Add unit tests for concurrent allocation safety.

### Phase 3: Modify Registration Protocol

- [ ] Update colony registration handler to return IP in initial response.
- [ ] Ensure `RegisterAgentResponse` includes `assigned_ip` field (already
  present in proto).
- [ ] Add allocation conflict detection and retry logic.
- [ ] Integration test: concurrent agent registrations.

### Phase 4: Remove Temporary IP Code

- [ ] Update agent connect command to use permanent IP from registration.
- [ ] Update agent start command similarly.
- [ ] Remove temporary IP assignment logic.
- [ ] Remove route flushing code (platform-specific commands).
- [ ] Remove sleep delays.
- [ ] Remove temporary IP constant.

### Phase 5: Testing and Documentation

- [ ] Integration tests for multi-agent registration.
- [ ] E2E test: colony restart with active agents.
- [ ] E2E test: agent reconnection preserves IP.
- [ ] Performance comparison: registration latency before/after.
- [ ] Update architecture documentation.
- [ ] Remove unused route manipulation utilities.
- [ ] Update RFD 007 (WireGuard Mesh) to reference this change.

## Testing Strategy

### Unit Tests

Test persistent IP allocator behavior:

- IP allocation persistence and recovery from database.
- Concurrent allocation safety with multiple goroutines.
- IP reuse for reconnecting agents with same agent ID.
- Allocation exhaustion handling (subnet full).
- Database transaction failure scenarios.
- Mutex contention under high concurrency.

### Integration Tests

Test colony registry with persistent allocations:

- Multiple agents registering simultaneously without conflicts.
- Colony restart preserves all allocations.
- Agent reconnection receives same IP as previous session.
- Allocation conflict detection and resolution.
- Database migration application and rollback.

### E2E Tests

Test complete agent lifecycle:

- Register 10 agents concurrently, verify no IP conflicts.
- Restart colony, verify agents can reconnect with original IPs.
- Simulate agent deregistration and IP reuse.
- Measure registration latency improvement (expect ~500ms reduction).
- Verify no route flushing or timing delays in new flow.

## Security Considerations

### Authentication Security (Out of Scope)

This RFD does not address the current security vulnerability where
`colony_secret`
is transmitted in plaintext during agent registration. Authentication
improvements,
including token-based authentication and TLS for the registration endpoint, will
be addressed in a separate RFD.

### IP Exhaustion

**Threat**: Malicious agents exhaust IP pool.

**Mitigation**:

- Current `/16` subnet provides 65,536 addresses.
- Add monitoring for allocation percentage.
- Future: Add IP lease TTL and garbage collection for inactive agents.

### IP Spoofing

**Threat**: Agent claims existing agent ID to steal its IP.

**Mitigation**: Agent authentication is handled by existing `colony_secret`
validation. Improved authentication will be addressed in a future RFD.

### Database Access

**Threat**: Direct database modification assigns conflicting IPs.

**Mitigation**: Colony is single authority for database access. No external
access expected. Unique constraint on `ip_address` column prevents accidental
duplicates.

## Migration Strategy

### Deployment

Deploy in this specific order to maintain backward compatibility:

1. **Deploy Colony Update**:
    - Run database migration `001-agent-ip-allocations.sql`.
    - Colony starts with persistent allocator.
    - Colony returns IP in `RegisterAgentResponse` (field already exists in
      proto).
    - Existing agents continue working (backward compatible - they ignore the IP
      field).

2. **Deploy Agent Updates**:
    - New agents use IP from registration response before configuring WireGuard.
    - Old agents with temporary IP pattern continue working.
    - Gradual rollout possible.

3. **Verification**:
    - Monitor registration success rate.
    - Check database for IP allocation records.
    - Verify colony restart preserves allocations.
    - Measure registration latency improvement.

### Rollback Plan

1. **If IP Allocation Issues Detected**:
    - Revert agent binary to previous version.
    - Colony continues working with database (no downside).
    - Database migration is additive (no data loss).

2. **Full Rollback** (extreme scenario):
    - Revert agents to temporary IP pattern.
    - Drop `agent_ip_allocations` table if needed.
    - Revert to in-memory allocator.

### Backward Compatibility

- **Colony**: Returns IP in registration response. Old agents ignore this field
  and use temporary IP pattern. New agents use the IP immediately.
- **Agent**: Old agents use temporary IP pattern. New agents use permanent IP
  from registration.
- **Database**: Additive schema change, no breaking changes.
- **Gradual Migration**: Mixed deployments with old and new agents work
  correctly.

## Future Enhancements

### Secure Agent Authentication

Address the plaintext `colony_secret` transmission vulnerability with
token-based
authentication or TLS for the registration endpoint (separate RFD).

### IP Lease Management

Add TTL and renewal mechanism for inactive agents:

```sql
ALTER TABLE agent_ip_allocations
    ADD COLUMN lease_expires_at TIMESTAMP;
```

Colony garbage collects IPs after lease expiration.

### CGNAT Address Space Migration

Migrate from `10.42.0.0/16` to `100.64.0.0/10` (RFC 6598 CGNAT) to avoid
conflicts with existing RFC 1918 networks (separate RFD).

### Dynamic Subnet Expansion

Support subnet growth without reconfiguration:

- Colony manages multiple subnets.
- Agents receive subnet assignment in registration response.

## Appendix

### Comparison with Netbird

Netbird's approach inspired this design:

| Aspect                 | Coral (Current)           | Coral (Proposed)             | Netbird               |
|------------------------|---------------------------|------------------------------|-----------------------|
| **IP Allocation**      | In-memory, sequential     | Database-backed              | Management service DB |
| **Persistence**        | None                      | DuckDB                       | PostgreSQL/SQLite     |
| **Temporary IPs**      | Yes (`10.42.255.254`)     | No                           | No                    |
| **Address Space**      | `10.42.0.0/16` (RFC 1918) | `10.42.0.0/16` (RFC 1918)    | `100.64.0.0/10`       |
| **Authentication**     | Plaintext `colony_secret` | Plaintext `colony_secret`    | API keys / OAuth      |
| **Registration**       | HTTP (insecure)           | HTTP (still insecure)        | HTTPS with auth       |
| **Bootstrap Security** | ❌ Vulnerable (MITM)       | ❌ Vulnerable (future work)   | ✅ Protected (TLS)     |
| **Route Management**   | Manual flush              | Automatic                    | Automatic             |
| **Collision Handling** | None                      | Mutex + DB unique constraint | Management service    |

### Database Schema Details

**Indexes**:

- Primary key on `agent_id` for fast lookups during reconnection.
- Unique constraint on `ip_address` prevents allocation conflicts.
- Index on `last_seen` for future lease management.

**Size Estimates** (per allocation: ~50 bytes):

- 1,000 agents ≈ 50 KB.
- 10,000 agents ≈ 500 KB.
- 65,536 agents (`/16` subnet max) ≈ 3.2 MB.

Negligible storage impact even at maximum scale.

### Registration Flow Comparison

**Before (Current)**:

```
Agent                                    Colony (Public IP + 10.42.0.1 mesh)
  |                                           |
  |--- Query discovery service -------------->|
  |<-- Colony endpoint, WireGuard config -----|
  |                                           |
  |--- Create WireGuard interface ------------|
  |--- Add colony as peer (requires IP) ------|
  |--- Assign temp IP: 10.42.255.254 ---------|
  |                                           |
  |--- RegisterAgent(colony_secret) HTTP --->|  ❌ SECRET IN PLAINTEXT!
  |                            [Allocate 10.42.0.2 in-memory]
  |<-- Response (IP: 10.42.0.2) --------------|
  |                                           |
  [Flush all routes - 200ms delay]            |
  [Delete routes via shell cmd (macOS)]       |
  [Reassign IP: 10.42.0.2 - 300ms delay]      |
  [Refresh peers - 500ms delay]               |
  |                                           |
  |--- Test mesh connectivity to 10.42.0.1 -->|
```

**After (Proposed)**:

```
Agent                                    Colony (Public IP + 10.42.0.1 mesh)
  |                                           |
  |--- Query discovery service -------------->|
  |<-- Colony endpoint, WireGuard config -----|
  |                                           |
  |--- Create WireGuard interface ------------|
  |    (no IP, no peers yet)                  |
  |                                           |
  |--- RegisterAgent(colony_secret) HTTP --->|  ⚠️  SECRET STILL IN PLAINTEXT
  |                            [Allocate 10.42.0.2 in DB]
  |<-- Response (IP: 10.42.0.2) --------------|
  |                                           |
  |--- Assign IP: 10.42.0.2 ------------------|
  |--- Add colony as peer --------------------|
  |    (routes created with correct source)   |
  |                                           |
  |--- Test mesh connectivity to 10.42.0.1 -->|
  |                                           |
  [Done - no delays, no route flushing]       |
```

**Key Improvements**:

- **Time savings**: ~500ms per registration (no route manipulation delays)
- **Setup order**: WireGuard peer added AFTER IP assignment
- **No temporary IP**: Interface configured with permanent IP from start
- **Persistent IPs**: Colony restart preserves all allocations (DuckDB)
- **Platform independence**: No platform-specific route commands
- **Better reliability**: No race conditions or timing dependencies

**Not Addressed** (future work):

- **Security**: `colony_secret` still transmitted in plaintext (separate RFD)
- **Address space**: Still using `10.42.0.0/16` (RFC 1918) - CGNAT migration in
  separate RFD
