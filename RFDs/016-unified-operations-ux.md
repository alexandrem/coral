---
rfd: "016"
title: "Unified Operations UX Architecture"
state: "draft"
breaking_changes: true
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "001", "007", "010", "011", "014" ]
related_rfds: [ "006", "012", "013", "015" ]
database_migrations: [ ]
areas: [ "cli", "agent", "colony", "ux", "deployment" ]
---

# RFD 016 - Unified Operations UX Architecture

**Status:** 🚧 Draft

## Summary

Establish a comprehensive UX architecture for Coral that provides unified
operations across all deployment contexts (local dev, Docker, Kubernetes) with
runtime-adaptive behavior. This RFD defines the complete command structure (
`run`, `connect`, `shell`, `exec`), agent runtime contexts, Colony connectivity
modes, and mesh-enabled remote operations that together form Coral's "unified
operations mesh with AI co-pilot."

**Key concepts:**

- **Runtime-adaptive agents**: Agent behavior adapts to deployment context (
  native, container, K8s sidecar, DaemonSet)
- **Agent-first design**: Core operations work without Colony (air-gapped mode)
- **Four command types**: `run` (long-running), `exec` (one-off), `shell` (
  interactive), `connect` (attach)
- **Mesh-enabled operations**: Any command can target any agent in the mesh
- **Smart discovery**: CLI auto-discovers local Colony, falls back gracefully

## Problem

**Current limitations:**

1. **Unclear command semantics**: `coral connect` used for both launching
   processes and attaching to existing ones. No distinction between long-running
   vs. one-off commands.

2. **Deployment context confusion**: Agent capabilities undefined for different
   contexts (sidecar vs. DaemonSet vs. native). No guidance on what works where.

3. **Colony dependency unclear**: Which commands need Colony? Can agent work
   standalone? How does failover work?

4. **No interactive debugging**: Can't get a shell into monitored environments
   without SSH or kubectl exec.

5. **Remote operations undefined**: How do you target remote agents? How does
   routing work? What about RBAC?

6. **Multi-service agents (RFD 011)** focus on `coral connect` with service
   specs, but don't address:
    - What if you want to launch a new service?
    - What about K8s sidecar where you can't launch?
    - How do one-off diagnostics work?

7. **K8s node agent (RFD 012)** describes DaemonSet discovery but doesn't
   clarify:
    - Can DaemonSet agents run user commands?
    - How do remote operations work?
    - What about sidecar mode for multi-tenant?

**Why this matters:**

- **Developer confusion**: Unclear which command to use when
- **Operational gaps**: Can't do interactive debugging in K8s sidecars
- **Deployment friction**: No clear guidance on agent deployment patterns
- **Missed use cases**: Remote debugging, fleet-wide diagnostics, multi-env
  comparison

## Solution

Define a complete UX architecture with four foundational pillars:

### 1. Command Structure (Purpose-Built)

Four commands with distinct purposes:

| Command               | Purpose                              | Duration          | Use Case                                  |
|-----------------------|--------------------------------------|-------------------|-------------------------------------------|
| `coral run <cmd>`     | Launch long-running process          | Until stopped     | Web servers, workers, dev servers         |
| `coral exec <cmd>`    | Execute one-off command              | Exits immediately | Health checks, diagnostics, fleet queries |
| `coral shell`         | Interactive monitored shell          | Until user exits  | Debugging, exploration                    |
| `coral connect <uri>` | Attach to existing process/container | Ongoing           | Monitor running services                  |

**Rationale**: Different operational needs require different commands. `kubectl`
has `run` vs `exec`, `docker` has `run` vs `exec`, Coral follows this pattern
but adds monitoring to all operations.

### 2. Agent Runtime Contexts (Adaptive Behavior)

Agent capabilities **bounded by deployment context**:

| **Runtime Context**      | **How Agent Runs**                       | **`run`**             | **`exec`**         | **`shell`**     | **`connect`**              |
|--------------------------|------------------------------------------|-----------------------|--------------------|-----------------|----------------------------|
| **Native on host**       | Binary/systemd                           | ✅ Native or container | ✅ Yes              | ✅ Yes           | ✅ All PIDs, all containers |
| **Container (isolated)** | Docker service                           | ✅ Sibling containers  | ✅ Yes              | ✅ Yes           | ✅ Containers via CRI       |
| **Container (host NS)**  | Docker with `pid: host`                  | ✅ Sibling containers  | ✅ Yes              | ✅ Yes           | ✅ All PIDs, all containers |
| **K8s Sidecar**          | Init container + `shareProcessNamespace` | ❌ Disabled            | ✅ In app container | ✅ Exec into app | ✅ Pod PIDs only            |
| **K8s DaemonSet**        | Privileged per-node                      | ❌ Disabled            | ✅ In target pod    | ✅ Exec into pod | ✅ Node PIDs, all pods      |

**Key principle**: Agent **detects its runtime** and adjusts behavior. Same
`coral agent start` command works everywhere.

**Platform Support Matrix:**

| **Platform** | **Supported Runtimes**       | **Default `run` Mode** | **Notes**                                                       |
|--------------|------------------------------|------------------------|-----------------------------------------------------------------|
| **Linux**    | All (native, container, K8s) | `auto` (config-driven) | Full support for all operations                                 |
| **macOS**    | Container only               | `container` (forced)   | Native process monitoring limited by OS security model          |
| **Windows**  | Container only (via WSL2)    | `container` (forced)   | Native execution not supported; requires Docker Desktop or WSL2 |

**Platform-specific behavior:**

- **Linux**: All runtime contexts supported. Agent can monitor native processes,
  launch containers, or run in K8s.
- **macOS**: Agent forces container mode for `coral run`. Native process
  monitoring restricted due to macOS SIP (System Integrity Protection) and
  security model. `coral connect pid://` limited to user's own processes.
- **Windows**: Agent requires WSL2 or Docker Desktop. Native Windows process
  monitoring not implemented. Container operations work via Docker/containerd in
  WSL2.

**Error handling**: If user attempts unsupported mode, agent shows clear error:

```
Error: coral run --mode=native not supported on macOS

macOS security model restricts native process introspection.
Use container mode instead (default):
  coral run <command>

Or run agent on Linux for full native support.

See: https://coral.io/docs/platform-support
```

**Implications for RFD 011/012:**

- **RFD 011** (multi-service agents): `coral connect` still works for attaching
  to multiple services
- **RFD 012** (K8s DaemonSet): Add `coral exec` and `coral shell` support for
  remote operations
- K8s sidecar mode is **passive** for `run` (can't launch) but **active** for
  `shell` and `exec`

### 3. Colony Connectivity (Agent-First, Colony-Enhanced)

**Core principle**: Agent is **self-sufficient** for local operations. Colony *
*optional but enables AI, remote mesh, RBAC**.

**Commands by dependency:**

| Command               | Local (No Colony)       | Remote (Needs Colony) |
|-----------------------|-------------------------|-----------------------|
| `coral run <cmd>`     | ✅ Direct to local agent | ✅ Via Colony routing  |
| `coral exec <cmd>`    | ✅ Direct to local agent | ✅ Via Colony routing  |
| `coral shell`         | ✅ Direct to local agent | ✅ Via Colony routing  |
| `coral connect <uri>` | ✅ Direct to local agent | ✅ Via Colony routing  |
| `coral ask "<q>"`     | ❌ Needs Colony for AI   | ✅ Always via Colony   |
| `coral agent list`    | ❌ Needs Colony registry | ✅ Always via Colony   |

**CLI auto-discovery** (tries in order):

1. Local Colony (unix socket, `localhost:8080`, `colony:8080`)
2. Environment variable (`CORAL_COLONY_URL`)
3. Config file (`~/.coral/config.yaml`)
4. Discovery Service (auto-discover via mesh)
5. Fallback to agent-only (graceful degradation)

**`coral proxy` command** creates local tunnel to remote Colony:

```bash
coral proxy start https://colony.company.internal
# Now CLI auto-discovers proxy at localhost:8080
# All commands work transparently via proxy
```

**Implications for RFD 001 (Discovery Service)**:

- Discovery Service integration is **optional** (local dev doesn't need it)
- Agent config includes `auto_discover: true/false`
- CLI tries local endpoints before Discovery Service

### 4. Mesh-Enabled Remote Operations

**Any command can target remote agents**:

```bash
# Local (default)
coral shell

# Remote by agent ID
coral shell --agent=prod-server-01

# Remote by service name
coral shell --service=checkout-api --env=production

# Remote by labels
coral exec --label=region=us-east "df -h"

# Multi-target
coral exec --env=production --all "systemctl status"
```

**Colony acts as router**:

- Resolves target (service name → agent ID)
- Enforces RBAC (can user access this?)
- Routes via WireGuard mesh
- Streams response back to CLI

**RBAC & Approvals**:

```yaml
rbac:
    users:
        -   name: alice@company.com
            role: sre
            permissions:
                -   environments: [ production ]
                    commands: [ shell, exec ]
                    require_approval: true
```

Production access triggers approval workflow before execution.

## Key Design Decisions

### Decision 1: `run` vs `exec` Semantics

**Problem**: Original design used `coral run` for both launching apps and
running one-off commands.

**Decision**: Split into two commands:

- `coral run <cmd>` - Launch **long-running** process (web server, worker)
- `coral exec <cmd>` - Execute **one-off** command (health check, diagnostic)

**Rationale**:

- Follows `kubectl` and `docker` conventions
- Prevents misuse (running `df -h` with `coral run`)
- Enables different behavior (run starts monitoring, exec returns output and
  exits)

**Impact on RFD 011**: No change. `coral connect` remains for attaching to
existing services.

### Decision 2: Runtime Context Detection

**Problem**: Agent doesn't know where it's running or what it can do.

**Decision**: Agent **auto-detects runtime** on startup:

```go
func DetectRuntime() RuntimeContext {
// Check Kubernetes
if _, err := os.Stat("/var/run/secrets/kubernetes.io"); err == nil {
if isSidecar() {
return RuntimeK8sSidecar
}
return RuntimeK8sDaemonSet
}

// Check container
if inContainer() {
return RuntimeDockerContainer
}

return RuntimeNative
}

// Platform-specific run mode defaults (addresses Q1 from review)
func GetDefaultRunMode(config *Config) RunMode {
platform := runtime.GOOS

// Force container mode on macOS/Windows
if platform == "darwin" || platform == "windows" {
return RunModeContainer
}

// Linux: honor config
return config.Run.DefaultMode // Can be "auto", "native", or "container"
}
```

Agent adjusts capabilities based on runtime:

- **Visibility**: What PIDs/containers can it see?
- **Execution**: Can it launch processes? Run commands?
- **Shell**: Where does interactive shell run?

**Impact on RFD 012**: DaemonSet agents detect `RuntimeK8sDaemonSet` and enable:

- `coral exec --pod=<name>` - Execute in target pod
- `coral shell --pod=<name>` - Shell into target pod
- Disable `coral run` (passive monitoring only)

### Decision 3: Sidecar Gets `shell` and `exec` via CRI Socket

**Problem**: RFD 011/012 focus on passive monitoring. No way to interact with
K8s sidecar environments. Using `shareProcessNamespace: true` works but violates
`restricted` PodSecurity policy.

**Decision**: K8s sidecar mode uses **two approaches** (auto-detected):

#### Primary: CRI Socket + Explicit Container Targeting

Mount CRI socket and use explicit `--connect` targeting:

```yaml
# K8s pod with CRI-based sidecar (RECOMMENDED)
spec:
    # No shareProcessNamespace needed - works with 'restricted' PodSecurity!

    initContainers:
        -   name: coral-agent
            image: coral/agent
            command: [ "coral", "agent", "start" ]
            args:
                - --connect=container://app      # Explicit targeting
                - --connect=container://nginx    # Multi-container support
            restartPolicy: Always
            volumeMounts:
                -   name: cri-sock
                    mountPath: /var/run/containerd/containerd.sock
                    readOnly: true
            securityContext:
                readOnlyRootFilesystem: true     # Restricted policy compatible

    containers:
        -   name: app
            image: my-app
        -   name: nginx
            image: nginx:alpine

    volumes:
        -   name: cri-sock
            hostPath:
                path: /var/run/containerd/containerd.sock  # Auto-detects: containerd.sock, crio.sock, docker.sock
                type: Socket
```

**Advantages:**

- ✅ **Security**: Works with `restricted` PodSecurity (no shared PID namespace)
- ✅ **Explicit**: Pod manifest declares monitored containers (declarative)
- ✅ **Portable**: Works across CRI runtimes (containerd, CRI-O, Docker)
- ✅ **Scoped**: Agent only sees explicitly targeted containers
- ✅ **Exec via CRI**: `coral shell`/`exec` use CRI Exec API (same as
  `kubectl exec`)

#### Fallback: Shared Process Namespace (Auto-Discovery)

For clusters allowing `shareProcessNamespace` (baseline PodSecurity):

```yaml
# K8s pod with shared namespace sidecar (FALLBACK)
spec:
    shareProcessNamespace: true  # Requires 'baseline' or 'privileged' PodSecurity

    initContainers:
        -   name: coral-agent
            command: [ "coral", "agent", "start", "--monitor-all" ]
            restartPolicy: Always

    containers:
        -   name: app
            image: my-app
```

**Advantages:**

- ✅ **Auto-discovery**: Agent discovers all pod containers automatically
- ✅ **No CRI socket**: Works without mounting host socket
- ⚠️ **Security tradeoff**: Requires `baseline` PodSecurity (shared PID
  namespace)

#### Agent Detection Logic

Agent auto-detects which mode to use:

```go
func (a *Agent) detectK8sSidecarMode() SidecarMode {
// Check if CRI socket available
if criSocket, err := detectCRISocket(); err == nil {
return SidecarModeCRI // Primary
}

// Check if shareProcessNamespace enabled
if hasSharedPIDNamespace() {
return SidecarModeSharedNS // Fallback
}

// Neither available - passive monitoring only
return SidecarModePassive
}
```

**Capabilities by mode:**

| Operation       | CRI Socket           | Shared Namespace     | Passive (neither)    |
|-----------------|----------------------|----------------------|----------------------|
| `coral connect` | ✅ Explicit targets   | ✅ Auto-discovered    | ✅ Auto-discovered    |
| `coral shell`   | ✅ CRI Exec API       | ✅ Shared namespace   | ❌ Not supported      |
| `coral exec`    | ✅ CRI Exec API       | ✅ Shared namespace   | ❌ Not supported      |
| `coral run`     | ❌ Disabled (sidecar) | ❌ Disabled (sidecar) | ❌ Disabled (sidecar) |

**Error handling (passive mode):**

```
Error: coral shell not supported in sidecar mode

Sidecar is running in passive mode (no CRI socket, no shared namespace).

To enable interactive debugging, update your pod spec:

Option 1 (Recommended): Mount CRI socket
  See: https://coral.io/docs/k8s-sidecar-cri

Option 2: Enable shareProcessNamespace
  See: https://coral.io/docs/k8s-sidecar-shared-ns

Or use DaemonSet mode for full node-level operations.
```

**Rationale**:

- CRI socket approach is more secure and works with strict PodSecurity policies.
- Shared namespace is simpler but requires relaxed security.
- Agent auto-detects and uses best available mode.
- Passive mode still allows monitoring via `coral connect` (SDK integration or
  auto-discovery).

**Impact on RFD 012**: DaemonSet and sidecar both support `shell`/`exec`, but:

- Sidecar (CRI): Executes via CRI Exec API in targeted containers
- Sidecar (Shared NS): Executes in same pod via shared namespace
- DaemonSet: Executes in **target pod** via CRI or K8s API (via `--pod` flag)

**Note**: Implementation details for `exec` command (CRI integration, app
container access) are covered in **RFD 017**. Implementation details for
`shell` command (agent debug environment, session management, audit) are covered
in **RFD 026**.

### Decision 4: Agent-First, Colony-Enhanced

**Problem**: Unclear what requires Colony vs works standalone.

**Decision**: Agent is **self-sufficient** for core operations:

- Local `run`, `exec`, `shell`, `connect` → Direct to agent
- Data buffered in agent's local DuckDB
- Agent works **offline** (air-gapped mode)

Colony **adds**:

- AI queries (`coral ask`)
- Remote operations (mesh routing)
- Cross-service correlation
- RBAC and approvals

**Rationale**:

- Developer can use Coral without Colony (lightweight local dev)
- Production adds Colony for AI and mesh
- Graceful degradation (CLI shows helpful error if Colony needed but
  unavailable)

**Impact on RFD 014 (LLM integration)**: Colony is **required** for AI. CLI
detects no Colony → show error with instructions to start Colony or use
`coral proxy`.

### Decision 5: CLI Smart Discovery

**Problem**: CLI needs to find Colony (local or remote).

**Decision**: CLI tries endpoints in order:

1. Unix socket: `/var/run/coral-colony.sock`
2. HTTP: `localhost:8080`
3. Docker network: `colony:8080`
4. Environment: `$CORAL_COLONY_URL`
5. Config: `~/.coral/config.yaml`
6. Discovery Service (if enabled)

If all fail, CLI **falls back to agent-only** mode (commands that need Colony
show helpful error).

**Rationale**:

- Zero-config works for `docker-compose up`
- Explicit config works for production
- Discovery Service handles NAT/multi-region
- Graceful degradation preserves core functionality

**Impact on RFD 001 (Discovery Service)**: Discovery Service is **last resort**,
not required for local dev.

### Decision 6: Configuration-Driven Behavior

**Problem**: Agent behavior shouldn't be hardcoded.

**Decision**: Agent config controls behavior:

```yaml
agent:
    runtime: auto  # auto-detect or explicit

    run:
        default_mode: container  # container, native, auto
        container_runtime: auto  # docker, containerd, cri-o

    shell:
        default_image: alpine:latest
        enable_in_sidecar: true

    colony:
        id: prod-us-east
        auto_discover: true
        discovery_url: https://discovery.coral.io
```

Per-command overrides:

```bash
coral run --mode=native ./my-app
coral shell --image=nicolaka/netshoot
```

**Rationale**:

- Same agent binary works in all contexts
- Operators control behavior via config
- Developers can override for specific cases

### Decision 7: Standardized Error UX

**Problem**: Inconsistent error messages make troubleshooting difficult. Users
need actionable guidance when commands fail.

**Decision**: All CLI errors follow a **standardized format** with
context-specific troubleshooting:

```
Error: <short error description>

<context about what went wrong>

<actionable troubleshooting steps>

See: <documentation link>
```

**Exit codes** (follows standard POSIX conventions):

| Exit Code | Meaning           | Example                                            |
|-----------|-------------------|----------------------------------------------------|
| `0`       | Success           | Command completed successfully                     |
| `1`       | General error     | Invalid arguments, config parse error              |
| `2`       | Misuse            | Wrong command for context (e.g., `run` in sidecar) |
| `3`       | Connection error  | Cannot reach agent or Colony                       |
| `4`       | Permission denied | RBAC check failed, approval required               |
| `5`       | Timeout           | Command exceeded timeout, no response              |
| `130`     | User interrupt    | Ctrl+C (SIGINT)                                    |

#### Error Templates

**Colony unavailable (commands requiring AI):**

```bash
$ coral ask "why is checkout slow?"

Error: Colony connection required for AI queries

No Colony found via auto-discovery or config.

Troubleshooting:
  1. Start local Colony:
     coral colony start

  2. Connect to remote Colony:
     coral proxy start https://colony.company.internal

  3. Check Colony status:
     coral colony status

  4. Verify Colony URL in config:
     cat ~/.coral/config.yaml

Current discovery attempts:
  ✗ Unix socket: /var/run/coral-colony.sock (not found)
  ✗ HTTP: localhost:8080 (connection refused)
  ✗ Docker: colony:8080 (not found)
  ✗ Config: ~/.coral/config.yaml (no colony URL)

See: https://coral.io/docs/colony-setup
Exit code: 3
```

**Unsupported operation for runtime context:**

```bash
$ coral run npm run dev
# (running in K8s sidecar mode)

Error: 'coral run' not supported in sidecar mode

Agent detected runtime: Kubernetes Sidecar (passive)

Sidecar agents cannot launch new processes. They monitor existing
containers in the pod.

Supported operations in sidecar mode:
  ✅ coral connect container://app    (monitor containers)
  ✅ coral exec "curl localhost"      (run commands in containers)
  ✅ coral shell                      (interactive shell in containers)
  ❌ coral run <command>              (not supported)

To launch processes, deploy as:
  - DaemonSet (node-level operations)
  - Native agent (host-level operations)
  - Container agent with host PID namespace

See: https://coral.io/docs/k8s-deployment-modes
Exit code: 2
```

**Platform limitation:**

```bash
$ coral run --mode=native ./my-app
# (running on macOS)

Error: Native mode not supported on macOS

Platform: darwin (macOS 14.2)
Requested mode: native
Default mode: container (auto-forced on macOS)

macOS security model restricts native process introspection.
Coral requires container mode on macOS/Windows.

Options:
  1. Use container mode (default):
     coral run ./my-app

  2. Run agent on Linux for native support

  3. Use Docker Desktop with Linux VM

See: https://coral.io/docs/platform-support
Exit code: 2
```

**RBAC / Approval required:**

```bash
$ coral shell --service=checkout-api --env=production

Error: Approval required for production access

User: alice@company.com
Environment: production
Service: checkout-api
Operation: shell (interactive)

Your request requires approval from an SRE.

Approval request #AR-12345 created.
Notified approvers:
  - bob@company.com (SRE Lead)
  - carol@company.com (SRE On-Call)

Waiting for approval... (timeout: 5 minutes)

To check status:
  coral approval status AR-12345

To cancel:
  Ctrl+C

See: https://coral.io/docs/rbac-approvals
Exit code: 4
```

**Timeout:**

```bash
$ coral exec --service=worker "long-running-task"

Error: Command timed out after 30s

Command: long-running-task
Target: worker (agent-id: worker-01)
Started: 2024-03-15 10:23:45 UTC
Timeout: 30s (default)

Command was terminated due to timeout. The process may still be
running on the remote agent.

Options:
  1. Increase timeout:
     coral exec --timeout=60s "long-running-task"

  2. Use 'coral run' for long-running processes:
     coral run long-running-task

  3. Run in background:
     coral exec "long-running-task &"

See: https://coral.io/docs/exec-vs-run
Exit code: 5
```

**Rationale**:

- Users know **what** went wrong (error message)
- Users know **why** it failed (context)
- Users know **how** to fix it (actionable steps)
- Consistent exit codes enable script automation
- Documentation links provide deep-dive help

**Implementation**: CLI uses error template system:

```go
// Example error construction
func ErrColonyUnavailable(discoveryAttempts []DiscoveryAttempt) error {
return &CLIError{
Code:    ExitCodeConnection,
Summary: "Colony connection required for AI queries",
Context: "No Colony found via auto-discovery or config.",
Troubleshooting: []string{
"Start local Colony:\n  coral colony start",
"Connect to remote Colony:\n  coral proxy start https://colony.company.internal",
"Check Colony status:\n  coral colony status",
},
Details: formatDiscoveryAttempts(discoveryAttempts),
DocLink: "https://coral.io/docs/colony-setup",
}
}
```

## API Changes

### Agent gRPC API

**New RPCs for `exec` and `shell`:**

```protobuf
service Agent {
    // Existing (from RFD 011)
    rpc Connect(ConnectRequest) returns (ConnectResponse);
    rpc RegisterServices(RegisterServicesRequest) returns (RegisterServicesResponse);

    // New for RFD 016
    rpc Run(RunRequest) returns (stream RunOutput);
    rpc Exec(ExecRequest) returns (stream ExecOutput);
    rpc Shell(ShellRequest) returns (stream ShellIO);

    rpc GetRuntimeContext(Empty) returns (RuntimeContextResponse);
}

message RunRequest {
    string command = 1;
    RunMode mode = 2;  // CONTAINER, NATIVE, AUTO
    map<string, string> env = 3;
    repeated string args = 4;
}

message ExecRequest {
    string command = 1;
    ExecTarget target = 2;  // LOCAL, POD, CONTAINER
    string target_id = 3;   // pod name or container ID
    map<string, string> env = 4;
}

message ShellRequest {
    ShellTarget target = 1;  // LOCAL, POD, CONTAINER
    string target_id = 2;
    string shell = 3;        // /bin/bash, /bin/sh
    string image = 4;        // For container shell
}

message RuntimeContextResponse {
    RuntimeContext runtime = 1;
    Capabilities capabilities = 2;
    repeated string visible_containers = 3;
    repeated int32 visible_pids = 4;
}

enum RuntimeContext {
    RUNTIME_NATIVE = 0;
    RUNTIME_DOCKER_ISOLATED = 1;
    RUNTIME_DOCKER_HOST_NS = 2;
    RUNTIME_K8S_SIDECAR = 3;
    RUNTIME_K8S_DAEMONSET = 4;
}

message Capabilities {
    bool can_run = 1;
    bool can_exec = 2;
    bool can_shell = 3;
    bool can_connect = 4;
    PIDVisibility pid_visibility = 5;
}
```

### Colony gRPC API

**Enhanced routing for remote operations:**

**Note**: This extends RFD 006 (Colony RPC Handler Implementation) which defines
observability RPCs (`GetStatus`, `ListAgents`, `GetTopology`). RFD 016 adds
operational routing RPCs for command execution across the mesh.

**Relationship to RFD 006:**

- **RFD 006**: Observability and registry (read-only queries about colony state)
- **RFD 016**: Operations and routing (write operations, command execution,
  RBAC)
- Both share the same `ColonyService` gRPC service definition

```protobuf
service Colony {
    // Existing (from RFD 006 - observability)
    rpc GetStatus(GetStatusRequest) returns (GetStatusResponse);
    rpc ListAgents(ListAgentsRequest) returns (ListAgentsResponse);
    rpc GetTopology(GetTopologyRequest) returns (GetTopologyResponse);

    // Existing (from RFD 014 - LLM integration)
    rpc Ask(AskRequest) returns (AskResponse);

    // Existing (from RFD 007 - mesh registration)
    rpc RegisterAgent(RegisterAgentRequest) returns (RegisterAgentResponse);

    // New for RFD 016 (operational routing)
    rpc RouteCommand(RouteCommandRequest) returns (stream RouteCommandOutput);
    rpc ResolveTarget(ResolveTargetRequest) returns (ResolveTargetResponse);
    rpc CheckPermission(CheckPermissionRequest) returns (CheckPermissionResponse);
    rpc RequestApproval(RequestApprovalRequest) returns (RequestApprovalResponse);
}

message RouteCommandRequest {
    CommandType type = 1;  // RUN, EXEC, SHELL, CONNECT
    TargetSpec target = 2;
    string command = 3;
    map<string, string> options = 4;
    UserContext user = 5;
}

message TargetSpec {
    oneof spec {
        string agent_id = 1;
        ServiceTarget service = 2;
        LabelSelector labels = 3;
    }
}

message ServiceTarget {
    string service_name = 1;
    string environment = 2;
    string region = 3;
}

message ResolveTargetResponse {
    repeated AgentEndpoint agents = 1;
    ResolutionMethod method = 2;
}

message CheckPermissionRequest {
    UserContext user = 1;
    CommandType command = 2;
    TargetSpec target = 3;
}

message CheckPermissionResponse {
    bool allowed = 1;
    bool requires_approval = 2;
    repeated string approvers = 3;
}
```

## Configuration Schema

### Agent Configuration

```yaml
# /etc/coral/agent.yaml
agent:
    # Runtime detection
    runtime: auto  # auto, native, docker, containerd, kubernetes

    # Behavior for `coral run`
    run:
        default_mode: container
        container_runtime: auto
        network_mode: bridge
        auto_remove: true

    # Behavior for `coral exec`
    exec:
        default_target: local
        timeout: 30s

    # Behavior for `coral shell`
    shell:
        default_image: alpine:latest
        default_shell: /bin/sh
        enable_in_sidecar: true
        auto_cleanup: true

    # Behavior for `coral connect`
    connect:
        auto_discover: true
        filter_labels: { }

    # Colony connection
    colony:
        id: prod-us-east
        url: ""  # Empty = auto-discover
        auto_discover: true
        discovery_url: https://discovery.coral.io
        fallback_offline: true

    # Storage
    storage:
        path: /var/lib/coral
        duckdb: coral.db
```

### CLI Configuration

```yaml
# ~/.coral/config.yaml
colony:
    auto_discover: true

    proxy:
        enabled: false
        listen: localhost:8080
        remote: ""
        auto_start: false

contexts:
    -   name: local
        colony: http://localhost:8080
        default_environment: dev

    -   name: company
        colony: https://colony.company.internal
        default_environment: staging
        auth:
            method: oidc
            issuer: https://auth.company.com

current_context: local

preferences:
    default_shell: /bin/bash
    confirm_production: true
    auto_approve_dev: true
```

## Implementation Plan

### Phase 1: Command Structure

- [ ] Implement `coral run` with long-running semantics
- [ ] Implement `coral exec` for one-off commands
- [ ] Implement `coral shell` for interactive sessions
- [ ] Update `coral connect` to work with runtime contexts
- [ ] Add command validation (error if wrong command for use case)

### Phase 2: Runtime Context Detection

- [ ] Implement `DetectRuntime()` in agent
- [ ] Add `GetRuntimeContext` RPC
- [ ] Implement capability restrictions per context
- [ ] Add visibility checks (PID, container access)
- [ ] Error messages show context and suggestions

### Phase 3: Agent Local Operations

- [ ] `coral run` via local agent (native and container modes)
- [ ] `coral exec` via local agent
- [ ] `coral shell` via local agent (container and native)
- [ ] Update `coral connect` for multi-service (RFD 011 compatibility)
- [ ] All commands work **without Colony** (air-gapped mode)

### Phase 4: Colony Connectivity

- [ ] CLI auto-discovery (unix socket, localhost, docker network)
- [ ] Environment variable and config file support
- [ ] Discovery Service integration (RFD 001)
- [ ] Graceful fallback to agent-only
- [ ] Helpful error messages when Colony needed but unavailable

### Phase 5: Remote Operations (Colony Routing)

- [ ] `RouteCommand` RPC in Colony
- [ ] Target resolution (agent ID, service name, labels)
- [ ] Command routing via WireGuard mesh
- [ ] Stream proxying (Colony → Agent → CLI)
- [ ] Multi-agent operations (`--all` flag)

### Phase 6: RBAC & Approvals

- [ ] `CheckPermission` RPC in Colony
- [ ] RBAC config schema (user roles, environment permissions)
- [ ] **MVP**: CLI-based approval workflow
    - Requester runs `coral shell --env=production`
    - Colony creates approval request
    - Approver runs `coral approval approve <request-id>`
    - CLI polls for approval status
- [ ] Audit logging (who, what, when, approved by)
- [ ] Production safeguards (timeouts, session limits)
- [ ] **Future**: External approval channels (Slack, email, UI) - deferred to
  Phase 6.1

**Note**: Phase 6 MVP uses CLI for both requesting and approving. External
integrations (Slack bot, email notifications, web UI) are valuable but deferred
to reduce initial complexity. See Future Enhancements for approval channel
expansion plans.

### Phase 7: `coral proxy` Command

- [ ] `coral proxy start <url>` command
- [ ] WireGuard tunnel establishment
- [ ] Local gRPC server (forwards to remote Colony)
- [ ] CLI auto-discovers proxy
- [ ] `coral proxy status` and `coral proxy stop`

### Phase 8: K8s Sidecar Enhancement

- [ ] Sidecar mode detection (`RuntimeK8sSidecar`)
- [ ] `shareProcessNamespace` validation
- [ ] `coral shell` exec into app container
- [ ] `coral exec` runs in app container
- [ ] Multi-container pod support (`--container` flag)

### Phase 9: K8s DaemonSet Enhancement (RFD 012 Update)

- [ ] DaemonSet mode detection (`RuntimeK8sDaemonSet`)
- [ ] `coral exec --pod=<name>` support
- [ ] `coral shell --pod=<name>` support
- [ ] Pod auto-discovery (existing RFD 012 feature)
- [ ] Remote operations via Colony routing

## Impact on Existing RFDs

### RFD 001 - Discovery Service

**Changes:**

- Discovery Service is **optional** (not required for local dev)
- CLI tries local endpoints **before** Discovery Service
- Agent config includes `auto_discover: true/false`

**Additions:**

- Document CLI auto-discovery hierarchy
- Clarify when Discovery Service needed vs not needed

**Status**: ✅ Compatible, clarifications needed

### RFD 011 - Multi-Service Agents

**Changes:**

- `coral connect` semantics **unchanged** (still for attaching to existing
  services)
- Add `coral run` for launching new services
- Add `coral exec` for one-off diagnostics
- Add `coral shell` for interactive debugging

**Additions:**

- Runtime context detection affects service visibility
- K8s sidecar mode enables `shell`/`exec` even when `run` disabled

**Status**: ✅ Compatible, enhancements added

### RFD 012 - Kubernetes Node Agent

**Changes:**

- DaemonSet agents **detect runtime context** (`RuntimeK8sDaemonSet`)
- Enable `coral exec --pod=<name>` and `coral shell --pod=<name>`
- Disable `coral run` (passive monitoring only)

**Additions:**

- Sidecar mode as alternative to DaemonSet (multi-tenant)
- Remote operations via Colony routing
- RBAC for pod access

**Status**: ⚠️ Requires updates, compatible

### RFD 013 - eBPF Introspection

**No changes required.** eBPF operates at agent level regardless of runtime
context.

**Status**: ✅ Compatible

### RFD 014 - Colony LLM Integration

**Changes:**

- `coral ask` **requires Colony** (document clearly)
- CLI shows helpful error if Colony unavailable
- `coral proxy` enables remote AI queries

**Status**: ✅ Compatible, clarifications needed

### RFD 015 - LLM Context Schema

**No changes required.** Context schema used by Colony regardless of how data
arrives.

**Status**: ✅ Compatible

## Testing Strategy

### Unit Tests

**CLI command parsing:**

- `coral run`, `exec`, `shell`, `connect` with various flags
- Validation errors for incorrect usage
- Config file parsing

**Agent runtime detection:**

- Mock filesystem for K8s detection
- Mock cgroup for container detection
- Capability matrix for each runtime

**Colony routing:**

- Target resolution (service → agent ID)
- RBAC checks
- Approval workflows

### Integration Tests

**Local agent operations (no Colony):**

- `coral run npm run dev` → agent monitors
- `coral exec "curl localhost"` → returns output
- `coral shell` → interactive session
- `coral connect pid://1234` → attaches

**Colony-enabled operations:**

- `coral ask "question"` → AI query
- `coral shell --service=api` → remote shell
- `coral exec --env=production --all "df -h"` → fleet-wide

**Auto-discovery:**

- CLI finds Colony at localhost:8080
- CLI falls back to agent-only
- `coral proxy` creates tunnel, CLI finds it

### E2E Tests

**Deployment scenarios:**

1. **Local dev (docker-compose)**:
    - Agent + Colony running locally
    - All commands work
    - AI queries functional

2. **Agent-only (air-gapped)**:
    - Only agent running
    - `run`, `exec`, `shell`, `connect` work
    - `ask` shows helpful error

3. **K8s sidecar**:
    - Sidecar agent deployed
    - `shell` execs into app container
    - `run` disabled (shows error)

4. **K8s DaemonSet**:
    - DaemonSet deployed
    - `exec --pod=xyz` works
    - `shell --pod=xyz` works
    - Remote operations via Colony

5. **Remote via proxy**:
    - `coral proxy start <url>`
    - All commands work transparently
    - CLI auto-discovers proxy

## Migration Path

**For users of current implementation:**

### 1. Command Changes (Breaking)

**`coral run` semantics changed** from "run any command" to "launch long-running
process":

| Old Usage                         | New Command                          | Migration                    |
|-----------------------------------|--------------------------------------|------------------------------|
| `coral run curl localhost/health` | `coral exec "curl localhost/health"` | One-off command → use `exec` |
| `coral run npm test`              | `coral exec "npm test"`              | Test execution → use `exec`  |
| `coral run ./server`              | `coral run ./server`                 | ✅ No change (long-running)   |
| `coral run npm run dev`           | `coral run npm run dev`              | ✅ No change (dev server)     |

**Migration detection**: CLI warns if detecting likely one-off usage:

```bash
# User runs one-off command
$ coral run df -h

⚠️  Warning: 'coral run' is now for long-running processes only

   Did you mean 'coral exec' for one-off commands?
     coral exec "df -h"

   'coral run' will wait until the command exits and keep it monitored.
   Use --no-warn to suppress this message.

Continue with 'coral run'? [y/N]
```

**Detection heuristics** (triggers warning):

- Command exits in <5 seconds
- Common one-off commands: `curl`, `wget`, `ls`, `df`, `ps`, `netstat`, `ping`
- Test frameworks: `pytest`, `jest`, `go test`, `npm test`

**Legacy compatibility** (optional, deprecated in v2.0):

```bash
# Add to ~/.coral/config.yaml
legacy:
  enable_run_compat: true  # coral run works like v1.x (one-off allowed)
  warn_on_compat: true     # Show deprecation warning
```

### 2. Config Changes (Additive)

**Agent config extended** (`/etc/coral/agent.yaml`):

```yaml
agent:
    # NEW: Runtime detection
    runtime: auto  # auto, native, docker, kubernetes

    # NEW: Platform-specific defaults
    platform:
        force_container_mode: auto  # auto (detects macOS/Windows), true, false

    # NEW: K8s sidecar mode
    kubernetes:
        sidecar_mode: auto  # auto, cri, shared-namespace, passive
        cri_socket: auto    # auto-detect or explicit path

    # EXISTING: Colony connection (unchanged)
    colony:
        id: my-colony
        auto_discover: true
```

**CLI config extended** (`~/.coral/config.yaml`):

```yaml
# NEW: Context management
contexts:
    -   name: local
        colony: http://localhost:8080
        default_environment: dev

    -   name: company
        colony: https://colony.company.internal
        default_environment: production
        auth:
            method: oidc

current_context: local

# NEW: Preferences
preferences:
    default_shell: /bin/bash
    confirm_production: true  # Prompt before exec/shell in prod
    auto_approve_dev: true    # Skip approvals in dev env
```

### 3. K8s Deployment Updates

**DaemonSet**: No breaking changes, new features additive:

```yaml
# Existing DaemonSet continues to work
# NEW: Enable exec/shell via CRI socket (optional)
spec:
    template:
        spec:
            containers:
                -   name: coral-agent
                    volumeMounts:
                        -   name: cri-sock  # NEW: Optional for exec/shell
                            mountPath: /var/run/containerd/containerd.sock
            volumes:
                -   name: cri-sock
                    hostPath:
                        path: /var/run/containerd/containerd.sock
```

**Sidecar**: Update recommended for `shell`/`exec` support:

```diff
# Before (passive monitoring only)
spec:
  initContainers:
  - name: coral-agent
    command: ["coral", "agent", "start", "--monitor-all"]
    restartPolicy: Always

# After (Option 1: CRI socket - RECOMMENDED)
spec:
  initContainers:
  - name: coral-agent
    command: ["coral", "agent", "start"]
+   args:
+     - --connect=container://app
+   volumeMounts:
+     - name: cri-sock
+       mountPath: /var/run/containerd/containerd.sock
+       readOnly: true
+ volumes:
+ - name: cri-sock
+   hostPath:
+     path: /var/run/containerd/containerd.sock

# After (Option 2: Shared namespace - FALLBACK)
spec:
+ shareProcessNamespace: true
  initContainers:
  - name: coral-agent
    command: ["coral", "agent", "start", "--monitor-all"]
    restartPolicy: Always
```

### 4. Upgrade Procedure

**Step-by-step upgrade:**

1. **Review usage**: Check if any scripts use `coral run` for one-off commands
   ```bash
   # Audit existing usage
   grep -r "coral run" scripts/ ci/
   ```

2. **Update CLI**: Upgrade to v2.x
   ```bash
   curl -L coral.io/install.sh | sh
   coral version  # Should show v2.x
   ```

3. **Test with warnings enabled**: Run existing commands, note warnings
   ```bash
   # Legacy mode during transition
   export CORAL_LEGACY_RUN=1
   ```

4. **Migrate commands**: Update scripts to use `coral exec` for one-off commands

5. **Update K8s manifests** (if using sidecars): Add CRI socket or
   shareProcessNamespace

6. **Disable legacy mode**: Remove `CORAL_LEGACY_RUN` after migration complete

**Rollback**: Downgrade to v1.x if issues arise

```bash
coral version  # Note current version
curl -L coral.io/install.sh?version=1.9 | sh
```

### 5. CHANGELOG Template

```markdown
# Coral v2.0.0

## Breaking Changes

### `coral run` Semantics Changed

`coral run` is now for **long-running processes only** (web servers, workers,
dev servers).

**Migration Required**: Use `coral exec` for one-off commands instead.

- Before: `coral run curl localhost/health`
- After:  `coral exec "curl localhost/health"`

**Migration Tools**:

- CLI shows warnings for likely one-off usage
- Optional legacy compatibility mode (deprecated)
- See: https://coral.io/docs/migration-v2

### CLI Auto-Discovery Order Changed

Colony discovery now tries local endpoints before remote:

1. Unix socket → localhost → docker network
2. Environment variable → config file
3. Discovery Service (last resort)

**Impact**: Local Colony may be preferred over configured remote.

**Fix**: Set explicit `CORAL_COLONY_URL` or use `coral context use <name>`.

## New Features

- ✨ `coral exec` - Execute one-off commands
- ✨ `coral shell` - Interactive monitored shell
- ✨ `coral proxy` - Tunnel to remote Colony
- ✨ Runtime-adaptive agents (auto-detect deployment context)
- ✨ K8s sidecar exec/shell support (CRI socket or shared namespace)
- ✨ Platform support (macOS, Windows via containers)

## Compatibility

- ✅ `coral connect` unchanged
- ✅ Agent registration protocol unchanged
- ✅ WireGuard mesh unchanged
- ✅ DuckDB schema unchanged
- ⚠️ Agent config schema extended (backward compatible)
```

**Backward compatibility**:

- `coral connect` unchanged (RFD 011)
- Agent registration unchanged
- WireGuard mesh unchanged (RFD 007)
- DuckDB schema unchanged (RFD 010)

## Security Considerations

### Command Execution Boundaries

**Risk**: `coral exec` and `coral shell` execute code in production
environments.

**Mitigations**:

- RBAC enforced by Colony (who can exec where)
- Approval workflows for production
- Audit logging (all commands logged)
- Session time limits
- `exec` timeout (default 30s)

### Shared Process Namespace (K8s Sidecar)

**Risk**: Sidecar with `shareProcessNamespace` can see all pod processes.

**Mitigations**:

- Document security implications
- RBAC for sidecar deployment
- Pod Security Standards compatibility
- Read-only filesystem for agent container

### Remote Operations (Mesh Routing)

**Risk**: Commands routed via Colony could be intercepted.

**Mitigations**:

- All traffic over WireGuard (encrypted)
- Colony validates user identity (OIDC, mTLS)
- Audit log includes full command
- Commands can't modify agent behavior

### Air-Gapped Mode

**Benefit**: Agent works without Colony = attack surface reduced.

**Consideration**: Local DuckDB contains observability data. Protect with
filesystem permissions.

## Future Enhancements

### Session Replay

Record `coral shell` sessions for audit/training:

```bash
coral shell --record session-123
# All commands recorded
coral session replay session-123
```

### Multi-Agent Shell

Shell across multiple agents simultaneously:

```bash
coral shell --env=production --all
# Commands broadcast to all agents
# Responses aggregated
```

### Script Mode

Execute local scripts on remote agents:

```bash
coral exec --service=api < diagnostic-script.sh
```

### AI-Assisted Shell

Shell with AI suggestions:

```bash
coral shell --ai
app $ # Slow request
💡 Suggestion: Check database connections
   Try: coral exec "netstat -an | grep 5432"
```

## Appendix

### Command Quick Reference

```bash
# Launch long-running application
coral run npm run dev
coral run --mode=native ./my-binary
coral run --agent=staging ./load-test

# Execute one-off command
coral exec "curl http://localhost/health"
coral exec --service=api "systemctl status"
coral exec --env=production --all "df -h"

# Interactive shell
coral shell
coral shell --service=checkout-api --env=production
coral shell --pod=my-app --container=nginx
coral shell --image=nicolaka/netshoot

# Attach to existing service
coral connect pid://1234
coral connect container://abc123
coral connect --service=api,worker,db

# AI queries
coral ask "why is checkout slow?"
coral ask "what changed in the last hour?"

# Proxy to remote Colony
coral proxy start https://colony.company.internal
coral proxy status
coral proxy stop

# Agent management
coral agent start
coral agent stop
coral agent status
```

### Runtime Context Examples

**Native on host (Linux)**:

```bash
# Install and start
curl -L coral.io/install.sh | sh
systemctl enable --now coral-agent

# All commands work
coral run npm run dev          # Launches in container
coral exec "curl localhost"    # Executes natively or in container
coral shell                    # Opens shell in container
coral connect pid://1234       # Attaches to any host PID
```

**Docker Compose**:

```yaml
services:
    coral-agent:
        image: coral-agent
        command: [ "coral", "agent", "start" ]
        privileged: true
        volumes:
            - /var/run/docker.sock:/var/run/docker.sock
        network_mode: host
        pid: host
```

```bash
# All commands work
coral run npm run dev          # Launches sibling container
coral exec "df -h"             # Executes in ephemeral container
coral shell                    # Opens shell in sibling container
coral connect container://app  # Attaches to app container
```

**Kubernetes Sidecar**:

```yaml
spec:
    shareProcessNamespace: true

    initContainers:
        -   name: coral-agent
            command: [ "coral", "agent", "start", "--monitor-all" ]
            restartPolicy: Always

    containers:
        -   name: app
            image: my-app
```

```bash
# Limited but functional
coral run npm run dev          # ❌ Error: sidecar is passive
coral exec "curl localhost"    # ✅ Runs in app container
coral shell                    # ✅ Execs into app container
coral connect container://app  # ✅ Attaches to app container
```

**Kubernetes DaemonSet**:

```yaml
kind: DaemonSet
spec:
    template:
        spec:
            hostNetwork: true
            hostPID: true
            containers:
                -   name: coral-agent
                    command: [ "coral", "agent", "start" ]
                    securityContext:
                        privileged: true
```

```bash
# Node-wide operations
coral run npm run dev              # ❌ Error: DaemonSet is passive
coral exec --pod=my-app "df -h"    # ✅ Executes in target pod
coral shell --pod=my-app           # ✅ Shells into target pod
coral connect --all                # ✅ Monitors all node pods
```

---

## Notes

**Why This RFD Matters**:

This is the **foundational UX RFD** that ties together:

- CLI commands (what developers type)
- Agent behavior (how it adapts to deployment)
- Colony integration (when/how it's needed)
- Remote operations (mesh-enabled debugging)

**Without this**, we have:

- Unclear command semantics
- Undefined deployment patterns
- No remote operations story
- Confusion about Colony dependency

**With this**, we have:

- Clear, purpose-built commands
- Runtime-adaptive agents
- Agent-first design (works offline)
- Mesh-enabled remote ops
- Production-safe RBAC

**This enables the "unified operations mesh with AI co-pilot" vision.**
