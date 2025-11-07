---
rfd: "021"
title: "CGNAT Address Space Migration"
state: "draft"
breaking_changes: true
testing_required: true
database_changes: false
api_changes: false
dependencies: [ "019" ]
related_rfds: [ "007", "019" ]
database_migrations: [ ]
areas: [ "networking", "colony", "agent" ]
---

# RFD 021 - CGNAT Address Space Migration

**Status:** 🚧 Draft

## Summary

Migrate from RFC 1918 private address space (`10.42.0.0/16`) to CGNAT address
space (`100.64.0.0/10`, RFC 6598) for the WireGuard mesh network. This avoids
routing conflicts when agents run on networks already using 10.x.x.x addresses,
which is common in corporate networks, home routers, and VPNs.

CGNAT space is specifically designed for carrier-grade NAT and is rarely used in
enterprise networks, making it ideal for overlay networks like Coral's WireGuard
mesh.

## Problem

### Current Behavior

Coral's WireGuard mesh currently uses the `10.42.0.0/16` subnet (RFC 1918
private address space) for mesh IP allocation. The colony assigns IPs
sequentially from this range (starting at `10.42.0.2`).

### Issues

1. **RFC 1918 Address Conflict**: Current subnet `10.42.0.0/16` is RFC 1918
   private address space, commonly used by:
    - Corporate networks and VPNs
    - Home routers (10.0.0.0/8 is very common)
    - Docker default networks
    - Kubernetes cluster networks
    - Cloud provider private networks (AWS, GCP, Azure)

2. **Routing Conflicts**: When agents run on networks already using 10.x.x.x
   addresses, routing becomes ambiguous:
    - Kernel doesn't know whether 10.42.0.x traffic should go to local network
      or WireGuard mesh
    - Requires complex routing table manipulation
    - Can break existing network connectivity

3. **Limited Address Space**: `/16` subnet provides only 65,536 addresses, which
   may be insufficient for large-scale deployments.

### Impact

- **Network Conflicts**: Agents cannot run on networks using 10.x.x.x
  addresses (common in enterprises).
- **Complex Workarounds**: Users must manually adjust routing tables or change
  local network addressing.
- **Deployment Blockers**: Cannot deploy on many corporate networks without
  network changes.
- **Scale Limitations**: 65K address limit may be insufficient for future
  growth.

## Solution

### Key Design Decisions

1. **Migrate to CGNAT Address Space**: Switch from RFC 1918 (`10.42.0.0/16`) to
   CGNAT (`100.64.0.0/10`, RFC 6598). CGNAT is designed for carrier-grade NAT
   and rarely used in enterprise environments.

2. **Configuration-Driven Subnet Selection**: Make subnet configurable to
   support both RFC 1918 (legacy) and CGNAT (new default).

3. **Backward Compatibility**: Existing colonies can continue using
   `10.42.0.0/16`. Only new colonies automatically use CGNAT.

4. **Coordinated Migration**: Provide clear migration path for existing
   deployments with documented downtime requirements.

### Benefits

- **No Network Conflicts**: CGNAT address space avoids conflicts with corporate
  networks, home routers, and VPNs using RFC 1918 addresses.
- **Larger Address Space**: CGNAT `/10` provides 4,194,304 addresses vs `/16`
  with 65,536.
- **Industry Standard**: CGNAT is the recommended approach for overlay
  networks (used by Tailscale, Netbird, ZeroTier).
- **Future-Proof**: Massive address space supports large-scale growth.

### Architecture Overview

No architectural changes required. This is purely a configuration change to the
subnet used for mesh IP allocation.

```
Colony IP Allocator:
  - Current: 10.42.0.0/16 (RFC 1918)
  - New:     100.64.0.0/10 (CGNAT, RFC 6598)

Agent WireGuard Interface:
  - Assigned IP from colony's configured subnet
  - Routing automatically uses the assigned subnet
```

### Component Changes

#### Colony: Configurable Subnet

Add configuration option for mesh subnet:

```yaml
# colony.yaml
mesh:
    subnet: "100.64.0.0/10"  # Default for new colonies
    # subnet: "10.42.0.0/16"  # Legacy option
```

- **Default for New Colonies**: `100.64.0.0/10`
- **Existing Colonies**: Continue using configured subnet (backward compatible)
- **Validation**: Ensure subnet is valid CIDR and has sufficient address space

#### Colony: IP Allocator

Update IP allocator to use configured subnet:

- Parse subnet from configuration
- Allocate IPs from the configured range
- Validate allocated IPs are within subnet
- Store subnet in database with allocations (for recovery)

#### Agent: Dynamic Subnet Support

Agents receive their mesh IP from colony during registration and use it
directly. No changes required - agents already work with any subnet.

## Implementation Plan

### Phase 1: Add Subnet Configuration

- [ ] Add `mesh.subnet` configuration option to colony config.
- [ ] Default new colonies to `100.64.0.0/10`.
- [ ] Validate subnet configuration on colony startup.
- [ ] Add unit tests for subnet parsing and validation.

### Phase 2: Update IP Allocator

- [ ] Update IP allocator to use configured subnet.
- [ ] Ensure allocated IPs are within configured range.
- [ ] Store subnet in database with allocations.
- [ ] Add unit tests for IP allocation from different subnets.

### Phase 3: Testing and Validation

- [ ] Integration test: colony with CGNAT subnet.
- [ ] Integration test: colony with RFC 1918 subnet (backward compat).
- [ ] E2E test: agents register and communicate using CGNAT IPs.
- [ ] E2E test: migration from RFC 1918 to CGNAT.
- [ ] Verify no routing conflicts with CGNAT addresses.

### Phase 4: Documentation and Migration Guide

- [ ] Document CGNAT migration procedure.
- [ ] Document rollback procedure.
- [ ] Update default configuration examples.
- [ ] Update architecture documentation.

## Testing Strategy

### Unit Tests

- Parse and validate CIDR subnets.
- IP allocation from different subnet sizes.
- Subnet configuration validation (reject invalid CIDRs).

### Integration Tests

- Colony startup with CGNAT subnet.
- Colony startup with RFC 1918 subnet.
- IP allocation stays within configured subnet.
- Database recovery loads correct subnet.

### E2E Tests

- Agents register and receive CGNAT IPs.
- Agents communicate over WireGuard mesh using CGNAT.
- Mixed deployment: some colonies use RFC 1918, some use CGNAT.
- Migration test: change subnet, verify agents receive new IPs.

## Security Considerations

### Address Space Exhaustion

**Threat**: Malicious agents exhaust IP pool.

**Mitigation**:

- CGNAT `/10` subnet provides 4,194,304 addresses (massive capacity).
- Rate limiting on agent registration prevents mass registration attacks.
- Monitoring for allocation percentage.

### Routing Conflicts

**Threat**: CGNAT addresses conflict with existing networks.

**Mitigation**:

- CGNAT (100.64.0.0/10) is rarely used in enterprise networks.
- If conflict occurs, colony can be configured to use different subnet.
- Document how to check for CGNAT conflicts before deployment.

## Migration Strategy

### For New Deployments

**Automatically use CGNAT (`100.64.0.0/10`) from the start.**

No migration required. Default configuration uses CGNAT.

### For Existing Deployments

**Option 1: Continue with RFC 1918** (No migration required)

- Existing colonies can continue using `10.42.0.0/16`.
- Only migrate if experiencing network conflicts.
- Configuration-driven subnet selection.

**Option 2: Migrate to CGNAT** (Recommended for conflict-free environments)

**Prerequisites**:

- All agents must be updated to support subnet change.
- Maintenance window required (all agents will reconnect).
- Backup current configuration and database.

**Migration Procedure**:

1. **Preparation**:
    - Verify no conflicts with `100.64.0.0/10` on agent networks.
    - Schedule maintenance window (agents will disconnect during migration).
    - Backup colony database and configuration.

2. **Colony Update**:
    - Stop colony service.
    - Update colony configuration to use CGNAT subnet:
      ```yaml
      mesh:
        subnet: "100.64.0.0/10"
      ```
    - Clear existing IP allocations in database:
      ```sql
      DELETE FROM agent_ip_allocations;
      ```
    - Start colony service with new subnet.

3. **Agent Reconnection**:
    - Agents reconnect automatically (using existing `colony_secret`).
    - Colony assigns new CGNAT IPs from `100.64.0.0/10` range.
    - Agents configure WireGuard interface with new IPs.
    - Old routes automatically replaced.

4. **Verification**:
    - Verify all agents reconnected successfully.
    - Check agent logs for connectivity issues.
    - Test mesh communication between agents.

5. **Downtime**: Brief interruption while agents reconnect (~30 seconds per
   agent).

**Migration Complexity**: Medium - requires coordination but straightforward.

### Rollback Plan

If migration fails:

1. **Stop Colony**: Stop colony service.
2. **Restore Configuration**: Revert colony config to use `10.42.0.0/16`.
3. **Restore Database**: Restore database backup with old IP allocations.
4. **Restart Colony**: Start colony service.
5. **Agent Reconnection**: Agents reconnect and receive original RFC 1918 IPs.

### Backward Compatibility

- **Colony**: Configurable subnet supports both RFC 1918 and CGNAT.
- **Agent**: No changes required - agents work with any subnet.
- **Mixed Deployments**: Different colonies can use different subnets (isolated
  meshes).

## Future Enhancements

### Dynamic Subnet Expansion

Support subnet growth without reconfiguration:

- Colony manages multiple subnets.
- Agents receive subnet assignment in registration response.
- Automatic failover to new subnet when primary is exhausted.

### Subnet Conflict Detection

Automatic detection of subnet conflicts:

- Agent checks local routing table before connecting.
- Reports conflicts to colony for visibility.
- Colony suggests alternative subnet if conflicts detected.

### Multi-Region Subnet Allocation

Allocate different subnets per region/datacenter:

- Improves routing efficiency.
- Easier to identify agent location by IP.
- Supports future inter-region mesh routing optimizations.

## Appendix

### CGNAT (RFC 6598) Overview

**RFC 6598**: IANA-Reserved IPv4 Prefix for Shared Address Space

- **Address Range**: `100.64.0.0/10`
- **Total Addresses**: 4,194,304
- **Purpose**: Carrier-grade NAT (CGNAT) for ISPs
- **Usage**: Rarely used in enterprise or home networks

**Why CGNAT for Overlay Networks**:

- Designed for NAT scenarios (perfect for mesh overlays).
- Minimal conflict risk with existing networks.
- Industry standard for overlay networks:
    - **Tailscale**: Uses `100.64.0.0/10`
    - **Netbird**: Uses `100.64.0.0/10`
    - **ZeroTier**: Uses custom address space but recommends CGNAT for conflicts

### Comparison with Netbird

| Aspect              | Coral (Current)           | Coral (Proposed)          | Netbird         |
|---------------------|---------------------------|---------------------------|-----------------|
| **Address Space**   | `10.42.0.0/16` (RFC 1918) | `100.64.0.0/10` (CGNAT)   | `100.64.0.0/10` |
| **Total Addresses** | 65,536                    | 4,194,304                 | 4,194,304       |
| **Conflict Risk**   | High (10.x very common)   | Low (CGNAT rarely used)   | Low             |
| **Configurable**    | No                        | Yes (backward compatible) | Yes             |

### Subnet Size Comparison

| CIDR | Address Space | Total Addresses | Use Case                         |
|------|---------------|-----------------|----------------------------------|
| /16  | 10.42.0.0     | 65,536          | Small deployments                |
| /10  | 100.64.0.0    | 4,194,304       | Large deployments (recommended)  |
| /8   | 10.0.0.0      | 16,777,216      | Massive scale (conflicts likely) |

### Migration Downtime Estimate

Based on agent reconnection speed:

- **Per Agent**: ~30 seconds (register + configure WireGuard)
- **10 Agents**: ~30 seconds (parallel reconnection)
- **100 Agents**: ~30 seconds (parallel reconnection)
- **1000 Agents**: ~1-2 minutes (rate limiting may slow registration)

Downtime is primarily the time for agents to detect colony restart and
reconnect.

### Pre-Migration Checklist

Before migrating to CGNAT:

- [ ] Verify no agent networks use `100.64.0.0/10` (run `ip route` on agent
  hosts).
- [ ] Backup colony database and configuration.
- [ ] Update all agents to latest version (supports subnet change).
- [ ] Schedule maintenance window with stakeholders.
- [ ] Prepare rollback plan and test restore procedure.
- [ ] Document expected downtime (~30 seconds to 2 minutes).
- [ ] Set up monitoring for agent reconnection status.
