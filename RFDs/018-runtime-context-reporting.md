---
rfd: "018"
title: "Agent Runtime Context Reporting"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "006", "007", "016", "017" ]
related_rfds: [ "011", "012", "013" ]
database_migrations: [ ]
areas: [ "agent", "colony", "cli", "observability", "routing" ]
---

# RFD 018 - Agent Runtime Context Reporting

**Status:** 🚧 Draft

## Summary

Define comprehensive runtime context reporting for Coral agents, enabling full
visibility into platform characteristics, runtime type, deployment mode,
capabilities, and operational constraints. This information is critical for
troubleshooting, routing decisions, and understanding agent behavior across
heterogeneous deployments (Linux/macOS/Windows, native/container/K8s).

**Key concepts:**

- **Runtime detection**: Agent identifies platform, runtime context, and
  capabilities on startup
- **Reporting architecture**: Agent → Colony → CLI flow for runtime visibility
- **Status command integration**: Display runtime info in all status commands
- **Capability-aware routing**: Colony routes commands only to compatible agents
- **Troubleshooting UX**: Clear errors when runtime incompatible with requested
  operation

## Problem

**Current limitations:**

RFD 016 defines runtime-adaptive agents with different capabilities based on
deployment context, but doesn't specify how this information is surfaced to
operators:

1. **No visibility into agent runtime**:
    - Can't see if agent is native, container, or K8s sidecar
    - Don't know which sidecar mode (CRI, SharedNS, Passive)
    - Can't tell why certain commands fail

2. **Limited troubleshooting information**:
    - Error messages don't explain runtime limitations
    - Can't identify agents with degraded functionality
    - No way to audit platform/runtime distribution

3. **No capability awareness in routing**:
    - Colony can't pre-filter incompatible agents
    - Commands sent to agents that can't execute them
    - Poor error messages when capabilities missing

4. **Cross-platform deployment confusion**:
    - Can't identify macOS/Windows agents (forced container mode)
    - Don't know which CRI runtime in use
    - No visibility into container/PID namespace visibility

5. **Operational blind spots**:
    - SREs can't quickly identify passive sidecar agents
    - Can't find agents with CRI socket issues
    - No dashboard showing runtime distribution

**Why this matters:**

- **Troubleshooting**: "Why doesn't `coral shell` work?" → "Agent in passive
  mode"
- **Operations**: "Show me all K8s sidecar agents" → filter by runtime
- **Routing**: Colony rejects incompatible commands early with helpful errors
- **Planning**: "What percentage of agents have exec/shell capability?"
- **Security**: RBAC policies can restrict by runtime type (e.g., no shell in
  passive mode)

## Solution

Comprehensive runtime context reporting with detection, storage, display, and
routing integration.

### 1. Runtime Detection and Reporting

**Agent detects runtime context on startup** and caches in memory.

**Key data structures:**

```go
type AgentRuntimeContext struct {
    // Platform information
    Platform PlatformInfo

    // Runtime context
    RuntimeType  RuntimeContext // NATIVE, DOCKER, K8S_SIDECAR, K8S_DAEMONSET
    SidecarMode  *SidecarMode   // CRI, SHARED_NS, PASSIVE (K8s only)

    // Container runtime
    CRISocket *CRISocketInfo

    // Capabilities (what commands work)
    Capabilities Capabilities

    // Visibility scope
    Visibility VisibilityScope

    // Detection metadata
    DetectedAt time.Time
    Version    string // Agent version
}

type PlatformInfo struct {
    OS        string // "linux", "darwin", "windows"
    Arch      string // "amd64", "arm64"
    OSVersion string // "Ubuntu 22.04", "macOS 14.2"
    Kernel    string // "6.5.0-35-generic"
}

type CRISocketInfo struct {
    Path    string // "/var/run/containerd/containerd.sock"
    Type    string // "containerd", "crio", "docker"
    Version string // "1.7.0"
}

type VisibilityScope struct {
    AllPIDs        bool // Can see all host PIDs
    AllContainers  bool // Can see all containers
    PodScope       bool // Limited to pod
    ContainerIDs   []string // Explicit targets (sidecar CRI mode)
    Namespace      string   // "host", "container", "pod"
}
```

**Detection strategy** (extends RFD 016 Decision 2):

1. **Platform detection**: Use Go runtime + OS-specific files (
   `/etc/os-release`, `uname`)
2. **Runtime type detection**: Check K8s environment variables, Docker socket,
   process namespace
3. **Sidecar mode detection**: Probe CRI socket availability, check
   `shareProcessNamespace`
4. **CRI socket probing**: Test common paths, query version via CRI API
5. **Container discovery** (CRI mode): Use CRI `ListContainers` API to query
   runtime for containers in pod namespace, filter by config targets if
   specified, fallback to config on error
6. **Capability determination**: Map runtime type + sidecar mode to command
   support
7. **Visibility calculation**: Determine scope based on runtime context and CRI
   availability

**Capability mapping:**

| Runtime Type     | Sidecar Mode | run | exec | shell | connect |
|------------------|--------------|-----|------|-------|---------|
| Native           | -            | ✅   | ✅    | ✅     | ✅       |
| Docker Container | -            | ✅   | ✅    | ✅     | ✅       |
| K8s Sidecar      | CRI          | ❌   | ✅    | ✅     | ✅       |
| K8s Sidecar      | Shared NS    | ❌   | ✅    | ✅     | ✅       |
| K8s Sidecar      | Passive      | ❌   | ❌    | ❌     | ✅       |
| K8s DaemonSet    | -            | ❌   | ✅    | ✅     | ✅       |

### 2. Reporting Architecture

**Data flow: Agent → Colony → CLI**

```
┌─────────────────────────────────────────────────────┐
│ 1. Agent Startup                                    │
│    - Call DetectRuntimeContext()                    │
│    - Cache in memory (a.runtimeContext)             │
│    - Log detection results                          │
└──────────────────┬──────────────────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────────────────┐
│ 2. Agent Registration (MeshService.Register)        │
│    - Include runtimeContext in RegisterRequest      │
│    - Colony receives and stores in agent registry   │
│    - One-time send (not on heartbeats)              │
└──────────────────┬──────────────────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────────────────┐
│ 3. Colony Storage (Agent Registry)                  │
│    - Store AgentRuntimeContext per agent            │
│    - Include in ListAgents response                 │
│    - Use for routing decisions                      │
└──────────────────┬──────────────────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────────────────┐
│ 4. CLI Status Commands                              │
│    - coral agent status (query local agent)         │
│    - coral colony status (query colony for all)     │
│    - coral agent list (detailed per-agent view)     │
└─────────────────────────────────────────────────────┘
```

**Optimization: Minimize network overhead**

- Runtime context sent **once** during registration
- Heartbeats only update `last_seen` timestamp
- CLI can query agent directly (`GetRuntimeContext` RPC) or via Colony

#### Runtime Context Refresh

**Problem addressed**: Environment changes after startup (CRI socket mounted,
pod restart, CRI version upgrade) aren't detected.

**Solution**: Periodic refresh with change detection and re-registration.

**Refresh strategy:**

1. **Periodic refresh** (default 5 minutes): Re-run detection logic
2. **Change detection**: Compare new context with cached version
3. **Re-registration**: Send updated context to Colony if changed
4. **Config reload trigger**: Explicit refresh when config changes
5. **Manual trigger**: CLI command `coral agent refresh-context`

**Changes that trigger re-registration:**

- Runtime type changed (unlikely, but possible in dynamic environments)
- Sidecar mode changed (CRI socket mounted/unmounted)
- CRI version changed (containerd upgraded)
- Capabilities changed (new features available)
- Container list changed (pod containers added/removed)

**Performance optimization:**

- Refresh is local only (no network if no changes)
- Re-registration only on actual changes (not every interval)
- Configurable interval balances freshness vs overhead
- Colony deduplicates rapid re-registrations (1/minute max)

**Configuration:**

```yaml
agent:
    runtime:
        refresh_interval: 5m      # Periodic refresh
        refresh_on_config: true   # Trigger on config reload
```

### 3. API Changes

#### Agent gRPC API (New RPCs)

```protobuf
service Agent {
    // Existing RPCs from RFD 016/017
    rpc Connect(ConnectRequest) returns (ConnectResponse);
    rpc Exec(ExecRequest) returns (stream ExecOutput);
    rpc Shell(stream ShellIO) returns (stream ShellIO);

    // NEW: Runtime context query
    rpc GetRuntimeContext(GetRuntimeContextRequest) returns (RuntimeContextResponse);
}

message GetRuntimeContextRequest {
    // Empty - returns current runtime context
}

message RuntimeContextResponse {
    // Platform
    PlatformInfo platform = 1;

    // Runtime type
    RuntimeContext runtime_type = 2;

    // Sidecar mode (if K8s)
    SidecarMode sidecar_mode = 3;

    // CRI socket info
    CRISocketInfo cri_socket = 4;

    // Capabilities
    Capabilities capabilities = 5;

    // Visibility
    VisibilityScope visibility = 6;

    // Detection metadata
    google.protobuf.Timestamp detected_at = 7;
    string version = 8;  // Agent version
}

message PlatformInfo {
    string os = 1;          // "linux", "darwin", "windows"
    string arch = 2;        // "amd64", "arm64"
    string os_version = 3;  // "Ubuntu 22.04"
    string kernel = 4;      // "6.5.0-35-generic"
}

message CRISocketInfo {
    string path = 1;     // "/var/run/containerd/containerd.sock"
    string type = 2;     // "containerd", "crio", "docker"
    string version = 3;  // "1.7.0"
}

message VisibilityScope {
    bool all_pids = 1;
    bool all_containers = 2;
    bool pod_scope = 3;
    repeated string container_ids = 4;  // Explicit targets
    string namespace = 5;                // "host", "container", "pod", "node"
}

enum SidecarMode {
    SIDECAR_MODE_UNKNOWN = 0;
    SIDECAR_MODE_CRI = 1;
    SIDECAR_MODE_SHARED_NS = 2;
    SIDECAR_MODE_PASSIVE = 3;
}

// RuntimeContext and Capabilities already defined in RFD 016
```

#### Mesh Service (Registration - References RFD 007)

**Extend `RegisterRequest`** (additive, doesn't modify implemented RFD):

```protobuf
// In auth.proto (RFD 007)
message RegisterRequest {
    string agent_id = 1;
    string component_name = 2;
    string colony_id = 3;
    string colony_secret = 4;
    bytes wireguard_pubkey = 5;

    // NEW: Runtime context (RFD 018)
    RuntimeContextResponse runtime_context = 6;
}

// RegisterResponse unchanged
```

#### Colony Service (Agent Registry - References RFD 006)

**Extend `Agent` message** in `ListAgents` response (additive):

```protobuf
// In colony.proto (RFD 006)
message Agent {
    string agent_id = 1;
    string component_name = 2;
    string mesh_ipv4 = 3;
    string mesh_ipv6 = 4;
    google.protobuf.Timestamp last_seen = 5;
    string status = 6;  // "healthy", "degraded", "unhealthy"

    // NEW: Runtime context (RFD 018)
    RuntimeContextResponse runtime_context = 7;
}

// GetStatus, GetTopology unchanged
```

### 4. Status Command UX

#### `coral agent status` (Local Agent)

```bash
$ coral agent status

Agent: frontend-001
Status: ✅ Running (connected to colony)

Platform:
  OS:           Linux (Ubuntu 22.04)
  Architecture: amd64
  Kernel:       6.5.0-35-generic
  Agent:        v2.1.0

Runtime:
  Type:         Kubernetes Sidecar
  Mode:         CRI (recommended)
  CRI Socket:   /var/run/containerd/containerd.sock
  CRI Runtime:  containerd v1.7.0
  Detected:     2024-03-15 10:23:45 UTC

Capabilities:
  ✅ coral connect   Monitor containers in pod
  ✅ coral exec      Execute in target containers
  ✅ coral shell     Interactive shell in containers
  ❌ coral run       Not supported (sidecar is passive for launching)

Visibility:
  Scope:            Pod only
  Namespace:        pod
  Target Containers: [app, nginx]

Colony:
  Connected:    ✅ Yes
  Colony:       prod-us-east
  URL:          colony.company.internal:9090
  Mesh IP:      10.42.0.5

Storage:
  Path:         /var/lib/coral
  DuckDB:       45.2 MB (1.2M events)
  Retention:    7 days
```

#### `coral colony status` (Colony Overview + All Agents)

```bash
$ coral colony status

Colony: prod-us-east
Status: ✅ Running
Uptime: 5d 12h 45m
Agents: 8 connected (7 healthy, 1 degraded)

Environment: production
Region:      us-east-1
Storage:     245.8 MB
Dashboard:   http://colony.company.internal:3000

Agents:
┌──────────────┬───────────┬────────┬──────────────┬────────────┬───────────┐
│ AGENT ID     │ COMPONENT │ STATUS │ RUNTIME      │ MODE       │ LAST SEEN │
├──────────────┼───────────┼────────┼──────────────┼────────────┼───────────┤
│ frontend-001 │ frontend  │ ✅      │ K8s Sidecar  │ CRI        │ 2s ago    │
│ frontend-002 │ frontend  │ ✅      │ K8s Sidecar  │ CRI        │ 5s ago    │
│ api-001      │ api       │ ✅      │ K8s Sidecar  │ Shared NS  │ 3s ago    │
│ api-002      │ api       │ ⚠️      │ K8s Sidecar  │ Passive    │ 45s ago   │
│ worker-001   │ worker    │ ✅      │ K8s DaemonSet│ -          │ 1s ago    │
│ db-proxy-001 │ db-proxy  │ ✅      │ Native       │ -          │ 4s ago    │
│ cache-001    │ cache     │ ✅      │ Container    │ Host NS    │ 2s ago    │
│ dev-local    │ dev       │ ✅      │ Native       │ -          │ 8s ago    │
└──────────────┴───────────┴────────┴──────────────┴────────────┴───────────┘

Platform Distribution:
  Linux:   7 agents (Ubuntu: 5, Alpine: 2)
  macOS:   1 agent  (forced container mode)

Runtime Distribution:
  K8s Sidecar:  3 agents (CRI: 2, Shared NS: 1, Passive: 1)
  K8s DaemonSet: 1 agent
  Native:       2 agents
  Container:    1 agent

⚠️  Warning: api-002 in passive mode (no exec/shell support)
    Fix: Mount CRI socket or enable shareProcessNamespace
    See: coral agent list api-002 --verbose
```

#### `coral agent list --verbose` (Detailed Agent Info)

```bash
$ coral agent list --verbose

8 agents connected to colony: prod-us-east

┌─ frontend-001 ────────────────────────────────────┐
│ Component:    frontend                            │
│ Status:       ✅ healthy (2s ago)                  │
│ Version:      v2.1.0                              │
│                                                   │
│ Platform:     Linux (Ubuntu 22.04) amd64          │
│               Kernel 6.5.0-35-generic             │
│                                                   │
│ Runtime:      Kubernetes Sidecar (CRI mode)       │
│ CRI:          containerd v1.7.0                   │
│ Socket:       /var/run/containerd/containerd.sock │
│ Detected:     2024-03-15 10:23:45 UTC             │
│                                                   │
│ Capabilities:                                     │
│   ✅ connect  ✅ exec  ✅ shell  ❌ run            │
│                                                   │
│ Visibility:                                       │
│   Scope:      Pod only                            │
│   Namespace:  pod                                 │
│   Containers: [app, nginx]                        │
│                                                   │
│ Mesh:         10.42.0.5                           │
└───────────────────────────────────────────────────┘

┌─ api-002 ─────────────────────────────────────────┐
│ Component:    api                                 │
│ Status:       ⚠️  degraded (45s ago)               │
│ Version:      v2.1.0                              │
│                                                   │
│ Platform:     Linux (Ubuntu 22.04) amd64          │
│               Kernel 6.5.0-35-generic             │
│                                                   │
│ Runtime:      Kubernetes Sidecar (PASSIVE mode)   │
│ CRI:          ❌ Not available                     │
│ Detected:     2024-03-15 08:15:22 UTC             │
│                                                   │
│ Capabilities:                                     │
│   ✅ connect  ❌ exec  ❌ shell  ❌ run            │
│                                                   │
│ Visibility:                                       │
│   Scope:      Pod only                            │
│   Namespace:  pod                                 │
│   Containers: (auto-discovered)                   │
│                                                   │
│ ⚠️  Limited functionality in passive mode         │
│    To enable exec/shell, update pod spec:         │
│                                                   │
│    Option 1 (Recommended): Mount CRI socket       │
│      volumes:                                     │
│        - name: cri-sock                           │
│          hostPath:                                │
│            path: /var/run/containerd/...          │
│      volumeMounts:                                │
│        - name: cri-sock                           │
│          mountPath: /var/run/containerd/...       │
│                                                   │
│    Option 2: Enable shareProcessNamespace         │
│      spec:                                        │
│        shareProcessNamespace: true                │
│                                                   │
│ Mesh:         10.42.0.8                           │
└───────────────────────────────────────────────────┘

┌─ dev-local ───────────────────────────────────────┐
│ Component:    dev                                 │
│ Status:       ✅ healthy (8s ago)                  │
│ Version:      v2.1.0                              │
│                                                   │
│ Platform:     macOS 14.2 arm64                    │
│               Kernel Darwin 23.3.0                │
│                                                   │
│ Runtime:      Native (forced container mode)      │
│ CRI:          Docker Desktop v25.0.3              │
│ Socket:       /var/run/docker.sock                │
│ Detected:     2024-03-15 09:42:10 UTC             │
│                                                   │
│ Capabilities:                                     │
│   ✅ connect  ✅ exec  ✅ shell  ✅ run            │
│   (run forced to container mode on macOS)         │
│                                                   │
│ Visibility:                                       │
│   Scope:      Host (user processes only)          │
│   Namespace:  host                                │
│   Containers: All (via Docker)                    │
│                                                   │
│ Mesh:         10.42.0.12                          │
└───────────────────────────────────────────────────┘
```

### 5. Capability-Aware Routing

**Colony routes commands only to compatible agents.**

**Routing logic:**

1. **Resolve target**: Query agent registry for agents matching target spec
2. **Filter by capability**: Check each agent's runtime context capabilities
3. **Handle incompatibility**: If no compatible agents, return detailed error
   with suggestions
4. **Warn partial**: If some agents incompatible, log warning and route to
   compatible subset
5. **Route**: Send command to compatible agents only

**Capability checking:**

- **Legacy agents** (no runtime context): Assume compatible (backward
  compatibility)
- **New agents**: Check `Capabilities` field based on command type:
    - `run` → `CanRun`
    - `exec` → `CanExec`
    - `shell` → `CanShell`
    - `connect` → `CanConnect`

**Error messages:**

Errors include:

- List of agents with compatibility status
- Runtime-specific reasons for incompatibility
- Actionable suggestions (e.g., mount CRI socket, enable shareProcessNamespace)
- Links to documentation

**Example error:**

```bash
$ coral shell --service=api --env=production

Error: Command 'shell' not supported by any target agents:

  api-001: ✅ supported
  api-002: ❌ not supported (passive mode - no CRI socket, no shared namespace)
  api-003: ❌ not supported (passive mode - no CRI socket, no shared namespace)

Suggestion:
  Agents in passive mode don't support exec/shell.
  Update pod spec to mount CRI socket (recommended) or enable shareProcessNamespace.
  See: https://coral.io/docs/k8s-sidecar-modes

Exit code: 2
```

### 6. CLI Pre-Flight Checks

**Problem addressed**: CLI behavior undefined for local vs remote commands.

**Solution**: Different strategies based on command target.

#### Pre-Flight Strategy

**Local commands** (target: local agent):

- **Method**: Direct agent RPC (`GetRuntimeContext`)
- **Latency**: ~10ms
- **Freshness**: Always current
- **Use case**: Commands without target spec

**Remote commands** (target: fleet of agents):

- **Method**: Colony registry query (`ResolveTarget`)
- **Latency**: ~20ms
- **Freshness**: May be stale (<5min)
- **Use case**: Commands with target spec (service, env, etc.)
- **Defense in depth**: Colony re-validates before routing

**Decision flow:**

```
User runs: coral shell --service=api

CLI PreflightCheck:
  ├─ Is target "local"? NO
  ├─ Parse target: service=api
  └─ Query Colony registry
       ├─ Colony returns: [api-001, api-002, api-003]
       ├─ Check each agent's runtime context
       ├─ Filter: api-001 ✅, api-002 ❌ (passive), api-003 ✅
       └─ Return: 2 compatible (proceed)

Colony.RouteCommand:
  ├─ Re-validates compatibility (catches stale data)
  ├─ Routes to: [api-001, api-003]
  └─ Skips: [api-002] (passive mode)
```

**Why registry query for remote:**

- **Fast**: Single Colony RPC vs N agent RPCs (O(1) vs O(N))
- **Sufficient**: Registry refreshed every 5min (acceptable staleness)
- **Defense**: Colony re-validates before actual routing (catches staleness)
- **Scalable**: Works for fleet-wide commands

**Staleness handling:**

If registry is stale, worst case is command fails at routing time with error.
Agent re-registers on next refresh cycle (5min). Acceptable trade-off for
performance.

#### Error Messages

**Example error (local):**

```
Error: 'shell' not supported in K8s Sidecar (Passive) mode

Agent: frontend-001
Runtime: Kubernetes Sidecar
Mode: Passive (no CRI socket, no shared namespace)

To enable shell, update pod spec:

  Option 1 (Recommended): Mount CRI socket
    See: https://coral.io/docs/k8s-sidecar-cri

  Option 2: Enable shareProcessNamespace
    See: https://coral.io/docs/k8s-sidecar-shared-ns

Exit code: 2
```

**Example error (remote):**

```
Error: Command 'shell' not supported by any target agents

Target: service=api (3 agents resolved)

Agent compatibility:
  ✅ api-001: Kubernetes Sidecar (CRI mode)
  ❌ api-002: Kubernetes Sidecar (Passive mode)
  ✅ api-003: Kubernetes Sidecar (Shared NS mode)

2 of 3 agents support this command.
Command will be routed to compatible agents only.

Skipped agents:
  api-002: Passive mode (no exec/shell support)

To fix api-002:
  Mount CRI socket or enable shareProcessNamespace
  See: https://coral.io/docs/k8s-sidecar-modes

Exit code: 0 (partial success)
```

## Protocol Compatibility and Migration

**Problem addressed**: Mixing old and new agents/colonies during rollout.

### Versioning Strategy

**Protocol version field** added to `RegisterRequest`:

```protobuf
message RegisterRequest {
    string agent_id = 1;
    string component_name = 2;
    string colony_id = 3;
    string colony_secret = 4;
    bytes wireguard_pubkey = 5;
    RuntimeContextResponse runtime_context = 6;  // NEW in v2.0

    // NEW: Protocol version
    string protocol_version = 7;  // "2.0.0"
}
```

**Version negotiation logic:**

1. **Parse agent version** from `protocol_version` field
2. **Check minimum version** (if enforcement enabled)
3. **Reject if too old** (configurable enforcement date)
4. **Accept legacy agents** (log warning, mark as legacy)
5. **Store runtime context** (if provided)

### Backward Compatibility

**Old Colony + New Agent:**

- Agent sends `runtime_context` field
- Old Colony ignores unknown field (protobuf backward compatibility)
- Registration succeeds
- No runtime reporting (Colony doesn't store/use context)

**New Colony + Old Agent:**

- Agent doesn't send `runtime_context` field
- Colony receives `nil` for runtime context
- Colony marks agent as "legacy" in registry
- Registration succeeds (no rejection)
- Colony logs warning
- Status commands show "unknown" for runtime info

### Migration Strategy

**3-Phase rollout** balances compatibility with tech debt:

#### Phase 1: Grace Period (Weeks 1-4)

**Configuration:**

```yaml
# Colony config
registry:
    legacy_agent_policy:
        warn_after: "2024-06-01"    # Start date
        reject_after: ""            # Empty = no enforcement yet
        enabled: true
```

**Behavior:**

- Both legacy and new agents accepted
- Legacy agents logged with WARN level
- Metrics track legacy agent count
- Dashboard shows legacy agent badge

**Metrics:**

```
coral_agents_legacy_total{colony="prod-us-east"} 15
coral_agents_total{colony="prod-us-east"} 80
coral_agents_legacy_pct{colony="prod-us-east"} 18.75
```

#### Phase 2: Transition Period (Weeks 5-8)

**Configuration:**

```yaml
# Colony config
registry:
    legacy_agent_policy:
        warn_after: "2024-06-01"
        reject_after: "2024-09-01"  # Set deadline
        enabled: true
```

**Behavior:**

- Legacy agents still accepted (no rejection yet)
- Warnings escalated to ERROR level
- Dashboard highlights legacy agents in red
- Weekly email report to ops team
- Status commands show upgrade instructions

**CLI output:**

```bash
$ coral colony status

⚠️  15 legacy agents detected (18.75%)
    These agents will be rejected after 2024-09-01

    Upgrade instructions:
      1. Update agent binary: curl -L coral.io/install.sh | sh
      2. Restart agent: systemctl restart coral-agent
      3. Verify: coral agent status (should show runtime info)

    Legacy agents:
      - api-002 (v1.9.0)
      - worker-005 (v1.8.5)
      ...
```

#### Phase 3: Enforcement (Week 9+)

**Configuration:**

```yaml
# Colony config
registry:
    legacy_agent_policy:
        warn_after: "2024-06-01"
        reject_after: "2024-09-01"  # Past deadline
        enabled: true               # Enforcement active
```

**Behavior:**

- Legacy agents **rejected** on registration
- Registration returns error with upgrade instructions
- Existing legacy agents continue working (not disconnected)
- New registrations must include runtime context

**Rejection error:**

```
Error: Agent registration rejected

Agent: api-002
Version: v1.9.0 (legacy)
Reason: Protocol version too old (runtime context required)

Your agent version is no longer supported. Please upgrade:

  1. Update agent:
     curl -L coral.io/install.sh | sh

  2. Restart:
     systemctl restart coral-agent

  3. Verify:
     coral agent status

Minimum required version: v2.0.0
Current version: v1.9.0

See: https://coral.io/docs/upgrade-guide
```

### Minimum Version Requirements

**Agent minimum versions:**

| Feature                   | Minimum Agent | Minimum Colony |
|---------------------------|---------------|----------------|
| Runtime context reporting | v2.0.0        | v2.0.0         |
| Runtime context refresh   | v2.1.0        | v2.0.0         |
| Container discovery (CRI) | v2.0.0        | v2.0.0         |

**Colony minimum versions:**

| Feature                 | Minimum Colony | Minimum Agent     |
|-------------------------|----------------|-------------------|
| Runtime context storage | v2.0.0         | v1.0.0 (optional) |
| Capability routing      | v2.0.0         | v2.0.0 (required) |
| Legacy agent warnings   | v2.0.0         | v1.0.0            |

### Rollout Recommendations

**Recommended rollout order:**

1. **Update Colony first** (v2.0.0)
    - Accepts both legacy and new agents
    - Enables runtime reporting for new agents
    - Logs warnings for legacy agents

2. **Update agents gradually** (v2.0.0)
    - Start with dev/staging environments
    - Roll out to production in batches
    - Monitor metrics for legacy agent count

3. **Enable enforcement** (after all agents upgraded)
    - Set `reject_after` date
    - Wait for grace period
    - Enable enforcement

**Rollback plan:**

If issues arise during rollout:

1. **Disable enforcement**:
   ```yaml
   registry:
     legacy_agent_policy:
       enabled: false  # Temporarily disable
   ```

2. **Roll back Colony** (if needed):
   ```bash
   # Colony v2.0.0 → v1.9.0
   kubectl set image deployment/coral-colony \
     colony=coral/colony:v1.9.0
   ```

3. **Investigate and fix**:
    - Review error logs
    - Check compatibility matrix
    - Test in staging environment

4. **Resume rollout** when ready

## Configuration Schema

### Agent Configuration

```yaml
# /etc/coral/agent.yaml
agent:
    # Runtime reporting
    runtime:
        # Report runtime context on startup
        reporting:
            enabled: true
            log_detection: true  # Log runtime detection results

        # Periodic refresh (addresses review finding #2)
        refresh_interval: 5m      # Re-detect runtime context
        refresh_on_config: true   # Trigger on config reload

        # Force specific runtime (for testing)
        override:
            runtime_type: ""     # Empty = auto-detect
            sidecar_mode: ""     # Empty = auto-detect
```

### Colony Configuration

```yaml
# Colony config
colony:
    # Agent registry
    registry:
        # Require runtime context in registration
        require_runtime_context: false  # Default: accept legacy agents

        # Warn on capability issues
        warn_passive_sidecars: true

        # Legacy agent policy (addresses review question #2)
        legacy_agent_policy:
            enabled: true
            warn_after: "2024-06-01"    # Start warning on this date
            reject_after: ""            # Empty = no enforcement
            # Example with enforcement:
            # reject_after: "2024-09-01"  # Reject after this date
```

## Implementation Plan

### Phase 1: Runtime Detection

- [ ] Implement runtime context detection in agent
- [ ] Platform detection (OS, arch, version)
- [ ] Runtime type detection (reuse RFD 016 logic)
- [ ] Sidecar mode detection (CRI vs SharedNS vs Passive)
- [ ] CRI socket probing
- [ ] Container discovery via CRI API
- [ ] Capability determination logic
- [ ] Visibility scope calculation
- [ ] Periodic refresh background task
- [ ] Change detection for re-registration
- [ ] Re-registration when context changes
- [ ] Unit tests for all detection paths

### Phase 2: Agent API

- [ ] Add `GetRuntimeContext` RPC to agent service
- [ ] Implement RPC handler
- [ ] Cache runtime context in agent struct
- [ ] Return context on RPC call
- [ ] Integration tests

### Phase 3: Registration Integration

- [ ] Extend `RegisterRequest` with runtime context
- [ ] Agent sends runtime context on registration
- [ ] Colony stores in agent registry
- [ ] Heartbeats don't resend (optimization)
- [ ] Handle legacy agents without runtime context

### Phase 4: Colony Storage and API

- [ ] Extend `Agent` message with runtime context
- [ ] Update agent registry to store runtime info
- [ ] Include in `ListAgents` response
- [ ] Query optimization (index by runtime type)
- [ ] Unit tests for registry operations

### Phase 5: Status Commands

- [ ] `coral agent status` - display runtime context
- [ ] `coral colony status` - show all agents + distribution
- [ ] `coral agent list --verbose` - detailed per-agent view
- [ ] Formatting and colors for terminal output
- [ ] JSON output mode (`--json` flag)

### Phase 6: Routing Integration

- [ ] Implement capability checking in Colony routing
- [ ] Filter agents by capability before routing
- [ ] Build detailed error messages with suggestions
- [ ] CLI pre-flight checks
- [ ] Integration tests for capability routing

## Testing Strategy

### Unit Tests

**Runtime detection:**

- Platform detection (Linux, macOS, Windows)
- Runtime type detection (mock filesystem)
- Sidecar mode detection (CRI socket, shared namespace)
- Capability determination (all runtime types)
- Visibility scope calculation

**Capability routing:**

- Filter compatible agents
- Build error messages
- Suggestion generation
- Legacy agent handling (no runtime context)

### Integration Tests

**Registration flow:**

- Agent registers with runtime context
- Colony stores in registry
- Query via `ListAgents` returns context
- Legacy agent registration (no context)

**Status commands:**

- `coral agent status` displays full context
- `coral colony status` shows all agents
- Distribution statistics correct
- Warnings for passive sidecars

**Routing:**

- Command sent only to compatible agents
- Incompatible agents skipped
- Error messages accurate
- Suggestions actionable

### E2E Tests

**Deployment scenarios:**

1. **K8s Sidecar (CRI mode)**:
    - Agent detects CRI socket
    - Registers with SidecarModeCRI
    - Status shows full capabilities
    - Commands route successfully

2. **K8s Sidecar (Passive mode)**:
    - Agent detects no CRI, no shared namespace
    - Registers with SidecarModePassive
    - Status warns limited functionality
    - Shell/exec commands fail with helpful error

3. **macOS Developer**:
    - Agent detects macOS platform
    - Forced container mode visible
    - Status explains limitations
    - Run commands work (via container)

4. **Mixed fleet**:
    - Colony with multiple runtime types
    - Commands route to compatible agents only
    - Distribution statistics accurate

## Security Considerations

### Information Disclosure

**Risk**: Runtime context reveals deployment architecture.

**Mitigations:**

- RBAC controls who can query runtime context
- Status commands require authentication
- Audit log includes runtime context queries
- Sanitize version strings (no build hashes)

### Capability Spoofing

**Risk**: Malicious agent reports false capabilities.

**Mitigations:**

- Colony validates capabilities against runtime type
- Inconsistencies logged and alerted
- Commands fail-safe (agent enforces capabilities)
- RBAC prevents unauthorized agent registration

### Legacy Agent Handling

**Risk**: Old agents without runtime reporting bypass checks.

**Mitigations:**

- Assume legacy agents have full capabilities
- Log legacy agent connections
- Optional: Reject legacy agents (config flag)
- Migration period before enforcement

## Future Enhancements

### Runtime Context History

Track runtime context changes over time:

```sql
CREATE TABLE agent_runtime_history
(
    agent_id      VARCHAR,
    timestamp     TIMESTAMP,
    runtime_type  VARCHAR,
    sidecar_mode  VARCHAR,
    cri_version   VARCHAR,
    change_reason VARCHAR -- "registration", "upgrade", "redeployment"
);
```

### Alerting on Capability Degradation

Alert when agent capabilities degrade:

```yaml
alerts:
    -   name: sidecar_passive_mode
        condition: agent.sidecar_mode == "passive"
        severity: warning
        message: "Agent {{agent_id}} in passive mode (no exec/shell)"
```

### Dashboard Runtime Visualization

Dashboard showing runtime distribution:

```
Runtime Distribution:
┌─────────────────┬────────┬─────────┐
│ K8s Sidecar     │ 45%    │ ████████│
│ Native          │ 30%    │ █████   │
│ Container       │ 15%    │ ███     │
│ K8s DaemonSet   │ 10%    │ ██      │
└─────────────────┴────────┴─────────┘

Capability Coverage:
  run:     85% (68/80 agents)
  exec:    92% (74/80 agents)
  shell:   92% (74/80 agents)
  connect: 100% (80/80 agents)
```

### Capability-Based RBAC

Restrict by runtime context:

```yaml
rbac:
    policies:
        -   name: no_shell_in_passive_mode
            condition: agent.sidecar_mode == "passive"
            deny: [ shell, exec ]
            message: "Shell/exec not allowed in passive sidecar mode"
```

## Integration with Existing RFDs

### RFD 006 - Colony RPC Handler Implementation (Implemented)

**Reference only** (no modifications to implemented RFD):

- RFD 018 extends `Agent` message returned by `ListAgents`
- Runtime context stored in agent registry (existing pattern)
- Status calculations can use runtime info for health

**Integration:**

- Colony registry stores `AgentRuntimeContext` per agent
- `ListAgents` includes runtime context in response
- Dashboard queries `ListAgents` to show runtime distribution

### RFD 007 - WireGuard Mesh Implementation (Implemented)

**Reference only** (no modifications to implemented RFD):

- RFD 018 extends `RegisterRequest` in mesh service
- Runtime context sent during initial registration
- Heartbeats don't resend (bandwidth optimization)

**Integration:**

- Agent includes runtime context in `MeshService.Register` call
- Colony validates and stores context
- Subsequent heartbeats only update `last_seen`

### RFD 016 - Unified Operations UX Architecture (Draft)

**Coordination** (both in draft, can reference each other):

- RFD 016 defines runtime contexts and capabilities
- RFD 018 defines how to report and display them
- Runtime detection logic shared

**Integration:**

- RFD 018 implements `GetRuntimeContext()` defined in RFD 016
- Status commands (RFD 016) display info from RFD 018
- Error messages (RFD 016 Decision 7) use runtime context from RFD 018

**Cross-references:**

- RFD 016: "Runtime reporting specified in RFD 018"
- RFD 018: "Runtime contexts defined in RFD 016"

### RFD 017 - Shell and Exec Command Implementation (Draft)

**Coordination** (both in draft, can reference each other):

- RFD 017 implements execution backends
- RFD 018 reports which backend is active
- Capability determination shared

**Integration:**

- RFD 018 detects CRI socket (RFD 017 uses for execution)
- Sidecar mode detection determines execution backend
- Audit logs include runtime context from RFD 018

**Cross-references:**

- RFD 017: "Runtime context reporting in RFD 018"
- RFD 018: "Execution backends detailed in RFD 017"

## Appendix

### Example: Runtime Detection Output

```bash
# Agent startup logs
INFO  Detecting runtime context
INFO  Platform: Linux (Ubuntu 22.04) amd64, Kernel 6.5.0-35-generic
INFO  Runtime type: Kubernetes Sidecar
INFO  Checking CRI socket: /var/run/containerd/containerd.sock
INFO  CRI detected: containerd v1.7.0
INFO  Sidecar mode: CRI (explicit targeting)
INFO  Target containers: [app, nginx]
INFO  Capabilities: connect=true, exec=true, shell=true, run=false
INFO  Visibility: pod scope, namespace=pod
INFO  Runtime context cached
```

### Example: Colony Registry Query

```sql
-- DuckDB query for runtime distribution
SELECT runtime_type,
       sidecar_mode,
       COUNT(*)                                        as agent_count,
       AVG(CASE WHEN can_exec THEN 1 ELSE 0 END) * 100 as exec_pct
FROM agent_registry
GROUP BY runtime_type, sidecar_mode
ORDER BY agent_count DESC;

-- Output:
-- runtime_type    | sidecar_mode | agent_count | exec_pct
-- K8s Sidecar     | CRI          | 35          | 100
-- Native          | NULL         | 25          | 100
-- K8s Sidecar     | Passive      | 12          | 0
-- Container       | NULL         | 10          | 100
-- K8s DaemonSet   | NULL         | 8           | 100
```

### Example: Routing Decision Log

```
DEBUG RouteCommand: shell, target=service:api, env=production
DEBUG Resolved target: 3 agents [api-001, api-002, api-003]
DEBUG Filtering by capability: shell
DEBUG   api-001: ✅ compatible (K8s Sidecar, CRI mode)
DEBUG   api-002: ❌ incompatible (K8s Sidecar, Passive mode)
DEBUG   api-003: ✅ compatible (K8s Sidecar, Shared NS mode)
DEBUG Compatible agents: 2/3
WARN  Skipping incompatible agents: [api-002]
INFO  Routing to: [api-001, api-003]
```

---

## Notes

**Why a Separate RFD:**

- **Scope**: Runtime reporting is substantial enough to warrant dedicated spec
- **Dependencies**: Builds on implemented RFDs (006, 007) without modifying them
- **Coordination**: Works with draft RFDs (016, 017) through cross-references
- **Clarity**: Separates concerns (detection vs execution, reporting vs routing)

**Relationship to Other RFDs:**

- **RFD 016**: Defines runtime contexts → RFD 018 reports them
- **RFD 017**: Implements execution → RFD 018 shows which backend active
- **RFD 006**: Provides agent registry → RFD 018 extends with runtime info
- **RFD 007**: Handles registration → RFD 018 adds runtime context to it

**Implementation Priority:**

Phase 1-3 (detection, API, registration) are **MVP** for runtime visibility.
Phase 4-6 (storage, status, routing) required for **production operations**.

à envoyer

- quittance
- paie du 31 dec 2024 environ
- dernier relevé paie avant fin


