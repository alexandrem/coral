---
rfd: "057"
title: "Agent Status Capability Reporting"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "056" ]
database_migrations: [ ]
areas: [ "agent", "cli" ]
---

# RFD 057 - Agent Status Capability Reporting

**Status:** 🎉 Implemented

## Summary

Enhance `coral agent status` to report Linux capabilities (CAP_SYS_ADMIN,
CAP_NET_ADMIN, etc.) and distinguish between exec modes (nsenter vs CRI). This
provides operators with clear visibility into agent security posture, deployment
configuration, and feature availability.

## Problem

**Current behavior/limitations:**

The `coral agent status` command currently reports high-level feature
availability (`can_exec`, `can_shell`, `can_connect`) but lacks granular detail
about:

1. **Linux capabilities granted to the agent**
    - Users cannot verify if `CAP_SYS_ADMIN` and `CAP_SYS_PTRACE` are available
      for nsenter-based exec
    - No visibility into `CAP_NET_ADMIN` for WireGuard or `CAP_SYS_RESOURCE` for
      eBPF
    - Troubleshooting "permission denied" errors requires manual
      `/proc/self/status` inspection

2. **Exec mode disambiguation**
    - `can_exec: true` doesn't distinguish between:
        - CRI-based exec (limited, no mount namespace access)
        - nsenter-based exec (full container filesystem access)
    - Users cannot determine why `coral exec nginx cat /etc/nginx/nginx.conf`
      fails in restricted mode

3. **Deployment validation**
    - No programmatic way to verify Kubernetes manifests were applied correctly
    - Cannot confirm that `securityContext.capabilities.add` includes required
      capabilities
    - Manual kubectl commands needed to check deployment configuration

**Why this matters:**

- **Troubleshooting**: "Why doesn't `coral exec` work?" requires guessing about
  missing capabilities
- **Security auditing**: Operators need to verify minimal privilege deployment (
  Restricted vs eBPF Full)
- **Documentation alignment**: New K8s deployment docs (
  deployments/k8s/README.md) document capability requirements, but status output
  doesn't reflect them
- **Feature discovery**: Users cannot determine available functionality without
  trial-and-error

**Use cases affected:**

1. **Debugging exec failures**: "coral exec fails with 'permission denied' -
   which capability is missing?"
2. **Security compliance**: "Verify agent has exactly CAP_NET_ADMIN, no other
   capabilities"
3. **Deployment validation**: "Confirm sidecar was deployed with correct
   securityContext"
4. **Feature planning**: "Can I use nsenter-based exec in this cluster?"

## Solution

Extend `coral agent status` to report:

1. **Linux capabilities** - Actual capabilities granted to the agent process
2. **Exec mode detection** - nsenter (full) vs CRI (limited) vs none
3. **Capability-to-feature mapping** - What each capability enables

**Key Design Decisions:**

- **Runtime detection over configuration**: Query `/proc/self/status` rather
  than trusting config files (reflects actual runtime state)
- **Feature-centric display**: Group capabilities by what they enable (
  WireGuard, exec, eBPF) for user clarity
- **Non-breaking addition**: Add new fields to existing
  `RuntimeContextResponse`, maintain backward compatibility
- **Reuse existing RPC**: Extend `GetRuntimeContext` rather than creating new
  endpoint

**Benefits:**

- **Self-documenting deployments**: Status output explains what agent can and
  cannot do
- **Faster troubleshooting**: Immediately see "coral exec: nsenter mode ❌ (
  missing CAP_SYS_ADMIN)" instead of trial-and-error
- **Security visibility**: Audit actual capabilities granted vs expected (detect
  misconfiguration)
- **Deployment validation**: Programmatic verification that K8s manifests match
  requirements

**Architecture Overview:**

```
coral agent status
    ↓
CLI (coral/internal/cli/agent/status.go)
    ↓ GetRuntimeContext() RPC
Agent (coral/internal/agent/service_handler.go)
    ↓ DetectLinuxCapabilities()
Runtime Detector (NEW: coral/internal/runtime/capabilities.go)
    ↓ Read /proc/self/status → Parse Cap* fields
    ↑ Return LinuxCapabilities proto
Agent
    ↓ Populate RuntimeContextResponse with capabilities
CLI
    ↓ Display enhanced status with capability breakdown
User
```

### Component Changes

1. **Protocol** (`proto/coral/agent/v1/agent.proto`):
    - Add `LinuxCapabilities` message with CAP_* boolean fields
    - Add `ExecCapabilities` message with mode enum and requirements
    - Extend `Capabilities` with `exec_capabilities` and `linux_capabilities`
      fields
    - Maintain backward compatibility (new fields are optional)

2. **Runtime Detection** (NEW: `internal/runtime/capabilities.go`):
    - Implement `DetectLinuxCapabilities()` using `/proc/self/status` parsing
    - Detect exec mode based on CAP_SYS_ADMIN + CAP_SYS_PTRACE availability
    - Use `github.com/syndtr/gocapability/capability` library for robust
      detection
    - Cross-platform support (Linux only, graceful degradation on other
      platforms)

3. **Agent** (`internal/agent/service_handler.go`):
    - Call `DetectLinuxCapabilities()` during `GetRuntimeContext()` RPC
    - Populate new capability fields in response
    - Cache detection results (capabilities don't change at runtime)

4. **CLI** (`internal/cli/agent/status.go`):
    - Add "Linux Capabilities" section to status output
    - Enhance "Capabilities" section to show exec mode details
    - Display capability-to-feature mapping (e.g., "CAP_SYS_ADMIN → coral exec (
      nsenter)")
    - Maintain existing output format, append new sections

**Configuration Example:**

No configuration changes required. Feature is automatic and always enabled.

## Implementation Plan

### Phase 1: Protocol Definition ✅

- [x] Define `LinuxCapabilities` message with CAP_* fields
- [x] Define `ExecCapabilities` message with mode enum
- [x] Extend `Capabilities` message with new fields
- [x] Generate protobuf code (`buf generate`)

### Phase 2: Runtime Detection ✅

- [x] Implement `DetectLinuxCapabilities()` in
  `internal/runtime/capabilities.go`
- [x] Parse `/proc/self/status` Cap* fields (CapEff, CapPrm, CapInh, CapBnd,
  CapAmb)
- [x] Use bitmask to detect specific capabilities (CAP_SYS_ADMIN = bit 21, etc.)
- [x] Add unit tests for capability detection with mock /proc data
- [x] Add cross-platform handling (return empty capabilities on non-Linux)

### Phase 3: Agent Integration ✅

- [x] Update `GetRuntimeContext()` RPC handler to call capability detection
- [x] Implement exec mode detection logic (nsenter vs CRI vs none)
- [x] Cache capability detection results (static at runtime)
- [x] Add logging for capability detection failures

### Phase 4: CLI Enhancement ✅

- [x] Add "Linux Capabilities" section to status output
- [x] Update "Capabilities" section with exec mode details
- [x] Add warnings for missing critical capabilities
- [x] Maintain backward compatibility with older agents (gracefully handle
  missing fields)
- [x] Update `--json` output to include new fields

### Phase 5: Testing & Documentation 🔄

- [x] Add unit tests for capability parsing
- [x] Add integration tests with different capability sets
- [x] Test with all deployment modes (Restricted, eBPF Minimal, eBPF Full,
  DaemonSet)
- [ ] Update docs/CLI.md with new status output examples
- [ ] Update deployments/k8s/README.md with validation examples

## API Changes

### New Protobuf Messages

```protobuf
// LinuxCapabilities represents Linux kernel capabilities granted to the agent.
// Mapped from /proc/self/status Cap* fields.
message LinuxCapabilities {
    // WireGuard mesh networking (required for all deployments).
    bool cap_net_admin = 1;

    // Container namespace execution via nsenter (coral exec nsenter mode).
    bool cap_sys_admin = 2;

    // Process inspection via /proc (required for coral exec PID detection).
    bool cap_sys_ptrace = 3;

    // eBPF memory locking (required for eBPF collectors).
    bool cap_sys_resource = 4;

    // Modern eBPF without CAP_SYS_ADMIN (kernel 5.8+).
    bool cap_bpf = 5;

    // Performance monitoring eBPF (kernel 5.8+).
    bool cap_perfmon = 6;

    // Additional capabilities for future use.
    bool cap_dac_override = 7;
    bool cap_setuid = 8;
    bool cap_setgid = 9;
}

// ExecCapabilities describes available container execution modes.
message ExecCapabilities {
    // Exec mode available to the agent.
    enum ExecMode {
        EXEC_MODE_UNKNOWN = 0;  // Not yet detected
        EXEC_MODE_NONE = 1;     // No exec support
        EXEC_MODE_CRI = 2;      // CRI-based exec (limited, no mount namespace)
        EXEC_MODE_NSENTER = 3;  // nsenter-based exec (full container filesystem access)
    }
    ExecMode mode = 1;

    // Can access container mount namespace (nsenter -m).
    bool mount_namespace_access = 2;

    // Can access container PID namespace (nsenter -p).
    bool pid_namespace_access = 3;

    // Requirements for nsenter mode.
    bool has_sys_admin = 4;   // CAP_SYS_ADMIN available
    bool has_sys_ptrace = 5;  // CAP_SYS_PTRACE available
    bool has_shared_pid_ns = 6; // Shared PID namespace or hostPID

    // Fallback to CRI if nsenter not available.
    bool cri_socket_available = 7;
}

// Extend existing Capabilities message.
message Capabilities {
    // Existing fields (unchanged).
    bool can_run = 1;
    bool can_exec = 2;
    bool can_shell = 3;
    bool can_connect = 4;

    // NEW: Detailed exec mode information.
    ExecCapabilities exec_capabilities = 5;

    // NEW: Linux capabilities granted to agent.
    LinuxCapabilities linux_capabilities = 6;
}
```

**Note:** Existing `RuntimeContextResponse` already contains
`Capabilities capabilities = 5;` field. We extend the `Capabilities` message
itself with new fields (backward compatible).

### CLI Output Changes

**Before (existing):**

```
Capabilities:
  ✅ coral connect   Monitor and observe
  ✅ coral exec      Execute in containers
  ✅ coral shell     Interactive shell
  ❌ coral run       Launch new containers
```

**After (enhanced):**

```
Capabilities:
  ✅ coral connect       Monitor and observe
  ✅ coral exec          Execute in containers
     Mode:               nsenter (full container filesystem access)
     Mount Namespace:    ✅ (CAP_SYS_ADMIN + CAP_SYS_PTRACE)
     PID Namespace:      ✅
  ✅ coral shell         Interactive shell
  ❌ coral run           Launch new containers

Linux Capabilities:
  ✅ CAP_NET_ADMIN       WireGuard mesh networking
  ✅ CAP_SYS_ADMIN       Container namespace execution (coral exec)
  ✅ CAP_SYS_PTRACE      Process inspection (/proc)
  ✅ CAP_SYS_RESOURCE    eBPF memory locking
  ❌ CAP_BPF             Modern eBPF (kernel 5.8+)
  ❌ CAP_PERFMON         Performance monitoring
```

**Restricted Mode Example (limited exec):**

```
Capabilities:
  ✅ coral connect       Monitor and observe
  ⚠️ coral exec          Execute in containers (LIMITED)
     Mode:               CRI (no mount namespace access)
     Mount Namespace:    ❌ (requires CAP_SYS_ADMIN + CAP_SYS_PTRACE)
     PID Namespace:      ❌
     Fallback:           CRI socket available
  ✅ coral shell         Interactive shell
  ❌ coral run           Launch new containers

Linux Capabilities:
  ❌ CAP_NET_ADMIN       WireGuard mesh networking
  ❌ CAP_SYS_ADMIN       Container namespace execution (coral exec)
  ❌ CAP_SYS_PTRACE      Process inspection (/proc)
  ❌ CAP_SYS_RESOURCE    eBPF memory locking

⚠️  Deployment: Restricted Mode
    Limited functionality - no nsenter, no eBPF

    For full coral exec (nsenter mode), add capabilities:
      securityContext:
        capabilities:
          add:
            - SYS_ADMIN
            - SYS_PTRACE
```

### JSON Output

**Enhanced JSON structure:**

```json
{
    "runtime_context": {
        "capabilities": {
            "can_exec": true,
            "can_shell": true,
            "can_connect": true,
            "can_run": false,
            "exec_capabilities": {
                "mode": "EXEC_MODE_NSENTER",
                "mount_namespace_access": true,
                "pid_namespace_access": true,
                "has_sys_admin": true,
                "has_sys_ptrace": true,
                "has_shared_pid_ns": true,
                "cri_socket_available": true
            },
            "linux_capabilities": {
                "cap_net_admin": true,
                "cap_sys_admin": true,
                "cap_sys_ptrace": true,
                "cap_sys_resource": true,
                "cap_bpf": false,
                "cap_perfmon": false
            }
        }
    }
}
```

## Testing Strategy

### Unit Tests

**`internal/runtime/capabilities_test.go`:**

- `TestDetectLinuxCapabilities()`: Mock /proc/self/status with various
  capability sets
- `TestParseCapabilityBitmask()`: Verify correct bit extraction for each CAP_*
- `TestExecModeDetection()`: Verify correct mode selection based on capabilities
- `TestCrossplatform()`: Verify graceful degradation on non-Linux systems

**Test cases:**

```go
// Full capabilities (eBPF Full mode)
caps := parseCapabilities("CapEff: 00000000a80435fb")
assert.True(t, caps.CapSysAdmin)
assert.True(t, caps.CapSysPtrace)
assert.True(t, caps.CapNetAdmin)

// Restricted mode (no capabilities)
caps := parseCapabilities("CapEff: 0000000000000000")
assert.False(t, caps.CapSysAdmin)
assert.False(t, caps.CapSysPtrace)

// Modern eBPF (CAP_BPF + CAP_PERFMON, no SYS_ADMIN)
caps := parseCapabilities("CapEff: 0000000000800080")
assert.True(t, caps.CapBpf)
assert.True(t, caps.CapPerfmon)
assert.False(t, caps.CapSysAdmin)
```

### Integration Tests

**`internal/cli/agent/status_e2e_test.go`:**

- Deploy agent with various capability sets (Restricted, eBPF Minimal, eBPF
  Full)
- Run `coral agent status --json` and verify capability fields
- Verify exec mode detection matches deployment configuration
- Test with and without shared PID namespace

**Example test:**

```bash
# Deploy agent in Restricted mode (no capabilities)
kubectl apply -f deployments/k8s/agent-sidecar-restricted.yaml

# Verify status reports correct mode
coral agent status --json | jq -r '.runtime_context.capabilities.exec_capabilities.mode'
# Expected: "EXEC_MODE_CRI"

# Verify no SYS_ADMIN
coral agent status --json | jq -r '.runtime_context.capabilities.linux_capabilities.cap_sys_admin'
# Expected: false
```

### Validation Commands

**Verify capability detection:**

```bash
# Check agent's actual capabilities
coral agent status --agent hostname-api-1

# Compare with manual check
kubectl exec -it <pod> -c coral-agent -- cat /proc/self/status | grep Cap

# Verify exec mode
coral exec nginx cat /etc/nginx/nginx.conf
# If nsenter mode: should succeed
# If CRI mode: should fail with clear error
```

## Security Considerations

**Capability detection security:**

- Reading `/proc/self/status` is non-privileged (always allowed)
- Detection is read-only (no capability elevation risk)
- Capability values reflect kernel enforcement (cannot be spoofed by agent)

**Information disclosure:**

- Capability status is operational metadata (not sensitive)
- Already visible via `kubectl exec ... -- capsh --print`
- Useful for security auditing (verify minimal privilege deployment)

**Audit logging:**

- Log capability detection results at agent startup
- Include in agent registration payload to colony
- Enables centralized capability auditing across fleet

## Future Enhancements

**Colony-Side Capability Tracking** (Future - RFD TBD):

- Store agent capabilities in colony database
- Enable queries like "show all agents with CAP_SYS_ADMIN"
- Alert when agent capabilities change unexpectedly (security event)

**RBAC Integration** (Blocked by RFD 043):

- Restrict `coral exec nsenter` to agents with CAP_SYS_ADMIN
- Enforce exec mode restrictions via policy
- Require approval for nsenter-based exec in production

**Capability Recommendations** (Low Priority):

- Analyze requested operations and recommend minimal capability set
- "You're using coral exec but only have CRI mode - add CAP_SYS_ADMIN?"
- Suggest deployment mode based on feature usage patterns

---

## Implementation Status

**Core Capability:** 🎉 Implemented

Agent status now reports Linux capabilities and exec modes, providing full
visibility into agent security posture and feature availability.

**Operational Components:**

- ✅ Protocol definitions: `LinuxCapabilities`, `ExecCapabilities`, `ExecMode`
  enum
- ✅ Runtime detection: `DetectLinuxCapabilities()` via /proc/self/status parsing
- ✅ Exec mode detection: `DetectExecCapabilities()` determines nsenter vs CRI
- ✅ Agent integration: Capabilities populated in `GetRuntimeContext()` RPC
- ✅ CLI enhancement: Enhanced status output with capability sections
- ✅ Unit tests: Comprehensive test coverage for capability parsing and detection

**What Works Now:**

- `coral agent status` displays Linux capabilities (CAP_SYS_ADMIN, CAP_NET_ADMIN,
  etc.)
- Exec mode detection distinguishes between nsenter (full) and CRI (limited)
  modes
- Mount namespace access clearly indicated with capability requirements
- Warning messages when critical capabilities are missing
- Cross-platform support (graceful degradation on non-Linux systems)
- JSON output includes all capability fields for programmatic access

**Implementation Files:**

- Protocol: `proto/coral/agent/v1/agent.proto` (new messages and enums)
- Runtime: `internal/runtime/capabilities.go` (detection logic)
- Tests: `internal/runtime/capabilities_test.go` (unit tests)
- Detector: `internal/runtime/detector.go` (integration with context detection)
- CLI: `internal/cli/agent/status.go` (enhanced status output)

**Example Output:**

```
Capabilities:
  ✅ coral connect       Monitor and observe
  ✅ coral exec          Execute in containers (nsenter mode - full access)
     Mode:               nsenter (full container filesystem access)
     Mount Namespace:    ✅
  ✅ coral shell         Interactive shell
  ❌ coral run           Launch new containers

Linux Capabilities:
  ✅ CAP_NET_ADMIN       WireGuard mesh networking
  ✅ CAP_SYS_ADMIN       Container namespace execution (coral exec)
  ✅ CAP_SYS_PTRACE      Process inspection (/proc)
  ✅ CAP_SYS_RESOURCE    eBPF memory locking
  ❌ CAP_BPF             Modern eBPF (kernel 5.8+)
  ❌ CAP_PERFMON         Performance monitoring
```

**Testing:**

All tests pass (`make test`). Capability detection tested with various bitmasks
representing different deployment modes (Restricted, eBPF Minimal, eBPF Full).

**Dependencies:**

- RFD 056 (Container Exec via nsenter) provides the exec mode framework this RFD
  reports on

## Deferred Features

**Advanced Capability Analysis** (Future - RFD TBD):

- Capability drift detection (alert when capabilities change at runtime)
- Capability recommendation engine (suggest minimal set for workload)
- Historical capability tracking (audit log of capability changes)

**Cross-Runtime Support** (Low Priority):

- Windows: Report UAC/privilege levels instead of Linux capabilities
- macOS: Report sandbox entitlements
- Graceful degradation ensures Linux-only implementation doesn't break other
  platforms

## Appendix

### Linux Capability Reference

**Capability bitmask positions** (from `include/uapi/linux/capability.h`):

```c
#define CAP_NET_ADMIN        12  // 0x1000
#define CAP_SYS_PTRACE       19  // 0x80000
#define CAP_SYS_ADMIN        21  // 0x200000
#define CAP_SYS_RESOURCE     24  // 0x1000000
#define CAP_PERFMON          38  // 0x4000000000 (kernel 5.8+)
#define CAP_BPF              39  // 0x8000000000 (kernel 5.8+)
```

**`/proc/self/status` format:**

```
CapInh:  0000000000000000
CapPrm:  00000000a80435fb
CapEff:  00000000a80435fb
CapBnd:  00000000a80435fb
CapAmb:  0000000000000000
```

- `CapEff` (effective): Currently active capabilities
- `CapPrm` (permitted): Maximum capabilities process can enable
- `CapInh` (inheritable): Capabilities passed to child processes
- `CapBnd` (bounding): Limit on capabilities process can acquire
- `CapAmb` (ambient): Capabilities preserved across execve

**Detection implementation** uses `CapEff` (what agent can actually do right
now).

### Reference Implementations

- **docker inspect**: Reports container capabilities in JSON output
- **kubectl auth can-i**: Queries RBAC permissions (similar concept)
- **systemd-analyze security**: Audits service capabilities and sandboxing
- **capsh --print**: Standard Linux capability inspection tool
- **github.com/syndtr/gocapability**: Go library for capability detection

### Test Configuration Examples

**Restricted mode (no capabilities):**

```yaml
securityContext:
    runAsNonRoot: true
    allowPrivilegeEscalation: false
    capabilities:
        drop:
            - ALL
# Expected: All CAP_* = false, EXEC_MODE_CRI
```

**eBPF Full mode (SYS_ADMIN + SYS_PTRACE):**

```yaml
securityContext:
    capabilities:
        add:
            - NET_ADMIN
            - SYS_ADMIN
            - SYS_PTRACE
            - SYS_RESOURCE
# Expected: All CAP_* = true, EXEC_MODE_NSENTER
```

**eBPF Minimal mode (BPF + PERFMON):**

```yaml
securityContext:
    capabilities:
        add:
            - NET_ADMIN
            - BPF
            - PERFMON
            - SYS_RESOURCE
# Expected: CAP_BPF = true, CAP_PERFMON = true, CAP_SYS_ADMIN = false, EXEC_MODE_CRI
```
