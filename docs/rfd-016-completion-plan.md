# RFD 016 Completion Plan

## Goal
Mark RFD 016 as "Implemented" by narrowing scope to what's actually done, moving unimplemented features to dedicated RFDs.

## Current Status Analysis

### ✅ What's Implemented
1. **Runtime Context Detection** (`internal/runtime/detector.go`)
   - Auto-detects: Native, Docker, K8s Sidecar, K8s DaemonSet
   - Sidecar mode detection (CRI, Shared NS, Passive)
   - Capability matrix per runtime
   - Platform detection

2. **`coral shell` Command** (`internal/cli/agent/shell.go`)
   - Interactive debug shell
   - Agent ID routing (RFD 044)
   - Direct agent connectivity

3. **`coral connect` Command** (`internal/cli/agent/connect.go`)
   - Multi-service monitoring (RFD 011)
   - Local agent discovery

4. **Agent-First Design**
   - Agents work standalone (local DuckDB)
   - CLI connects directly to agents
   - No Colony dependency for local ops

5. **Configuration Schema** (`internal/config/schema.go`)
   - GlobalConfig, ColonyConfig
   - Discovery settings

### ❌ What's NOT Implemented (Extract to Other RFDs)

1. **`coral run` and `coral exec`**
   - Status: Draft spec only (RFD 017)
   - Action: Keep in RFD 017, remove from 016 scope

2. **RBAC Enforcement**
   - Status: Now RFD 046 (comprehensive)
   - Action: Remove Phase 6 details, reference RFD 046

3. **Approval Workflows**
   - Status: Now RFD 046
   - Action: Remove from 016, reference 046

4. **Colony Routing RPCs** (`RouteCommand`, etc.)
   - Status: Not implemented, architecture shifted to direct connectivity (RFD 038)
   - Action: Mark as "Deferred - Direct connectivity preferred"

5. **Multi-Target Operations** (`--all`, `--label`)
   - Status: Not implemented
   - Action: Create RFD 047 "Fleet Operations" or defer

6. **Complete CLI Discovery Hierarchy**
   - Status: Partial (localhost works, unix socket doesn't)
   - Action: Mark current state as "good enough", defer complete hierarchy

## Proposed Changes to RFD 016

### 1. Update Title and Summary
```markdown
# RFD 016 - Runtime-Adaptive Agent Architecture and Command Foundations

**Status:** ✅ Implemented

## Summary

Establish runtime-adaptive agent architecture that automatically detects deployment context (native, container, K8s sidecar/DaemonSet) and provides foundational command structure for Coral operations. This RFD defines the agent runtime detection system, basic command taxonomy, and agent-first design that enables future operational commands.
```

### 2. Move to "Deferred Features" Section
```markdown
## Deferred Features

The following capabilities build on this foundation but are implemented in dedicated RFDs:

**Operational Commands** (RFD 017)
- `coral run` - Launch long-running processes with monitoring
- `coral exec` - Execute one-off commands in containers
- CRI integration for container execution
- See RFD 017 for complete implementation

**RBAC and Security** (RFD 046)
- Role-based access control for all operations
- Approval workflows for production access
- Audit logging and compliance
- See RFD 046 for complete RBAC system

**Direct Remote Connectivity** (RFD 038)
- CLI-to-agent direct mesh connections for remote colony
- AllowedIPs orchestration
- Ephemeral mesh IPs for CLI tools
- See RFD 038 for architecture

**Fleet Operations** (Future - RFD 047)
- Multi-target commands (`--all`, `--label=key=value`)
- Service-based targeting across agents
- Parallel execution and result aggregation
- Deferred pending core command implementation

**Colony Routing RPCs** (Deferred - Architecture Shift)
- Original design included Colony-proxied command routing
- Architecture shifted to direct connectivity (more efficient)
- Colony provides control plane (resolution, RBAC, AllowedIPs)
- CLI connects directly to agents over WireGuard mesh
- Colony routing RPCs deferred in favor of RFD 038 approach
```

### 3. Update "Implementation Status"
```markdown
## Implementation Status

**Core Capability:** ✅ Complete

Runtime-adaptive agent architecture fully implemented. Agents automatically detect deployment context and adjust capabilities accordingly. Command foundations established with `coral shell` and `coral connect` operational.

**Operational Components:**
- ✅ Runtime context detection (Native, Docker, K8s Sidecar, K8s DaemonSet)
- ✅ Sidecar mode detection (CRI socket, Shared namespace, Passive)
- ✅ Platform support matrix (Linux full, macOS/Windows container-only)
- ✅ Capability matrix per runtime (can_run, can_exec, can_shell)
- ✅ `coral shell` - Interactive debug environment (RFD 026)
- ✅ `coral connect` - Multi-service monitoring (RFD 011)
- ✅ Agent-first design (works without Colony)
- ✅ Direct agent connectivity (local and via RFD 044 resolution)
- ✅ Configuration schema foundations

**What Works Now:**
- Agent detects runtime on startup and adjusts behavior
- `coral shell --agent=<id>` provides debug access to agents
- `coral connect` monitors multiple services per agent
- Agents store data locally (DuckDB) without Colony dependency
- CLI auto-discovers local agents
- Platform-specific defaults (container mode forced on macOS/Windows)

**Integration Status:**
- ✅ Integrated with RFD 011 (multi-service agents)
- ✅ Integrated with RFD 026 (shell command)
- ✅ Integrated with RFD 044 (agent ID routing)
- ⏳ Awaiting RFD 017 (exec/run commands)
- ⏳ Awaiting RFD 038 (remote direct connectivity)
- ⏳ Awaiting RFD 046 (RBAC enforcement)
```

### 4. Update Dependencies
```yaml
dependencies: [ "007", "011" ]  # Only truly required dependencies
related_rfds: [ "006", "012", "013", "017", "026", "038", "044", "046" ]
```

### 5. Remove/Simplify Phases
Delete detailed implementation phases for unimplemented features. Replace with:

```markdown
## Implementation Plan

### ✅ Phase 1: Runtime Detection (Complete)
- [x] Implement `DetectRuntime()` in agent
- [x] Platform detection (Linux, macOS, Windows)
- [x] Capability matrix per context
- [x] Sidecar mode detection

### ✅ Phase 2: Command Foundations (Complete)
- [x] `coral shell` implementation (RFD 026)
- [x] `coral connect` implementation (RFD 011)
- [x] Agent-first design (local operations)
- [x] Direct agent connectivity (RFD 044)

### ⏳ Phase 3: Operational Commands (RFD 017)
See RFD 017 for `coral run` and `coral exec` implementation.

### ⏳ Phase 4: Security & RBAC (RFD 046)
See RFD 046 for comprehensive RBAC enforcement.

### ⏳ Phase 5: Remote Connectivity (RFD 038)
See RFD 038 for direct CLI-to-agent mesh connections.
```

## Next Steps

1. **Update RFD 016** with changes above
   - Narrow scope to implemented features
   - Add "Deferred Features" section
   - Update "Implementation Status" to "Complete"
   - Simplify Implementation Plan

2. **Verify RFD 017** has exec/run details
   - Already drafted, keep as-is
   - Mark as dependency for RFD 016 completion

3. **Verify RFD 046** has RBAC details
   - ✅ Created in this session
   - Mark as dependency for production readiness

4. **Optional: Create RFD 047** for Fleet Operations
   - Multi-target commands (`--all`, `--label`)
   - Can be deferred if not priority

5. **Update related RFDs** to reference 016 as implemented
   - RFD 026 (shell) - depends on 016 runtime detection ✅
   - RFD 017 (exec) - depends on 016 command structure ✅
   - RFD 011 (connect) - implements 016 command ✅

## Benefits of This Approach

### ✅ Clear Scope
- RFD 016 focuses on **architecture foundations** (implemented)
- Operational details in dedicated RFDs (017, 046)

### ✅ Completable
- Can mark 016 as "Implemented" immediately
- No lingering "in progress" state

### ✅ Maintainable
- Each RFD has single responsibility
- Easier to track implementation status

### ✅ Clear Dependencies
- RFD 017 depends on 016 (command structure)
- RFD 046 depends on 016 (operations to secure)
- RFD 038 depends on 016 (connectivity pattern)

## Summary

**Before:** RFD 016 tried to define everything (architecture + commands + RBAC + routing)
**After:** RFD 016 defines architecture foundations (runtime detection + command taxonomy)

**Mark as Implemented:** Yes, immediately after updates
**Unfinished Work:** Moved to RFD 017, 046, 047 (clear ownership)
