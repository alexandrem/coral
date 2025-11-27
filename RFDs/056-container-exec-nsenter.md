---
rfd: "056"
title: "Container Exec via nsenter"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "045" ]
database_migrations: [ ]
areas: [ "agent", "colony", "mcp" ]
---

# RFD 056 - Container Exec via nsenter

**Status:** 🚧 Draft

**Supersedes:** RFD 017 (Exec Command - CRI-based approach)

## Summary

Enable execution of commands within application container namespaces using
`nsenter` for sidecar and node agent deployments. This provides filesystem
namespace isolation, allowing access to container-mounted configs, logs, and
volumes that are not visible from the agent's host filesystem. Works with
docker-compose sidecars, Kubernetes sidecars (shareProcessNamespace), and
Kubernetes DaemonSets (hostPID).

## Problem

**Current behavior/limitations:**

RFD 045 implemented `coral_shell_exec` which executes commands on the agent's
host system. However, in sidecar deployments where the agent shares PID
namespace with application containers (docker-compose `pid: "service:app"`,
Kubernetes `shareProcessNamespace: true`) or node agent deployments with access
to all host processes (Kubernetes `hostPID: true`):

- Agent can see application processes (shared/host PID namespace) ✅
- Agent can access application's localhost in sidecar mode (shared network
  namespace) ✅
- Agent **cannot** see application's filesystem view (mount namespace not
  shared) ❌

This means we cannot:

- Read application config files as mounted in the container
- Inspect application logs in container-specific paths
- Verify mounted volumes and their permissions
- Debug "What config is the app actually reading?"

**Why this matters:**

Filesystem isolation is critical for container debugging. Applications often
have:

- Configs mounted at `/app/config.yaml` visible only in container
- Environment-specific secrets in `/run/secrets/`
- Volumes mounted at paths not accessible from host
- Different filesystem layouts than the host

**Use cases affected:**

1. **Config Verification**: "Is the database password correctly mounted?"
2. **Volume Inspection**: "What files are in the shared volume?"
3. **Troubleshooting**: "Why can't the app find its config file?"
4. **Compliance Audits**: "What's the actual content of production configs?"

## Solution

Use `nsenter` to enter the application container's mount namespace and execute
commands in that context. This works across multiple deployment modes:

1. **Docker-compose sidecar**: Agent shares PID namespace with application
   (`pid: "service:app"`)
2. **Kubernetes native sidecar** (K8s 1.28+): Agent as initContainer with
   `restartPolicy: Always` and `shareProcessNamespace: true`
3. **Kubernetes regular sidecar**: Agent as regular container with
   `shareProcessNamespace: true`
4. **Node agent mode** (K8s DaemonSet): Agent has `hostPID: true` to see all
   node processes

**Key Design Decisions:**

- **nsenter over CRI**: Simpler than CRI socket integration, works with any OCI
  runtime
- **Flexible namespace entry**: Mount-only for sidecars, full isolation for node
  agents
- **PID auto-detection**: Find container PID via /proc scanning
- **New MCP tool**: `coral_container_exec` complements `coral_shell_exec`
- **Deployment-agnostic**: Works in docker-compose, K8s sidecars, K8s DaemonSets

**Benefits:**

- Simple implementation (single binary, no runtime dependencies)
- Works across docker-compose and Kubernetes deployments
- No CRI socket required
- Leverages existing capabilities (CAP_SYS_ADMIN, CAP_SYS_PTRACE)
- Clear separation: host exec vs container exec
- Unified API across deployment modes

**Architecture Overview:**

```
Claude Desktop (MCP Client)
    ↓ coral_container_exec(service="demo-app", command=["cat", "/app/config.yaml"])
Colony MCP Server
    ↓ gRPC: ContainerExec(service, command)
Agent (sidecar OR node agent with hostPID)
    ↓ detectContainerPID() → scan /proc → finds demo-app PID
    ↓ nsenter -t <PID> -m -- cat /app/config.yaml
    ↑ stdout: [config contents from container's filesystem]
Claude Desktop
    ← Formatted response with config contents
```

**Deployment Modes:**

| Mode                         | PID Access              | Configuration                                          |
|------------------------------|-------------------------|--------------------------------------------------------|
| Docker-compose sidecar       | `pid: "service:app"`    | Sidecar shares PID namespace with app                  |
| Kubernetes sidecar (native)  | `shareProcessNamespace` | initContainer with `restartPolicy: Always` (K8s 1.28+) |
| Kubernetes sidecar (regular) | `shareProcessNamespace` | Regular container in same Pod (all K8s versions)       |
| Kubernetes node agent        | `hostPID: true`         | DaemonSet sees all containers on the node              |
| Kubernetes multi-tenant      | `hostPID: true` + RBAC  | DaemonSet with namespace/pod filtering                 |

### Component Changes

1. **Agent** (`internal/agent/`):
    - New `container_handler.go` with `ContainerExec()` RPC handler
    - Container PID detection via /proc scanning
    - nsenter command construction and execution
    - Follows pattern from `shell_handler.go` (RFD 045)

2. **Colony MCP Server** (`internal/colony/mcp/`):
    - New MCP tool: `coral_container_exec`
    - Input type: `ContainerExecInput` (service, command, timeout, etc.)
    - Tool executor: `executeContainerExecTool()` in `tools_exec.go`
    - Agent resolution and gRPC client creation

3. **Protocol** (`proto/coral/agent/v1/`):
    - New RPC:
      `rpc ContainerExec(ContainerExecRequest) returns (ContainerExecResponse)`
    - Request: command array, optional container name, timeout, working dir
    - Response: stdout, stderr, exit code, container PID, namespaces entered

**Configuration Examples:**

**Docker-compose sidecar:**

```yaml
services:
    demo-app:
        image: nginx:alpine

    coral-agent:
        image: coral-agent:latest
        pid: "service:demo-app" # Share PID namespace with demo-app
        network_mode: "service:demo-app" # Share network namespace
        cap_add:
            - SYS_ADMIN # Required for nsenter
            - SYS_PTRACE # Required for /proc inspection
```

**Kubernetes sidecar (same Pod):**

**Two approaches:**

1. **Native sidecar (initContainer with restartPolicy: Always)** - K8s 1.28+
   (recommended)
2. **Regular sidecar container** - works on all K8s versions

**Approach 1: Native sidecar (recommended for K8s 1.28+):**

```yaml
apiVersion: v1
kind: Pod
metadata:
    name: demo-app
spec:
    shareProcessNamespace: true # Enable PID namespace sharing
    initContainers:
        - name: coral-agent # Native sidecar using initContainer
          image: coral-agent:latest
          restartPolicy: Always # Makes initContainer long-running (K8s 1.28+)
          securityContext:
              capabilities:
                  add:
                      - SYS_ADMIN # For nsenter
                      - SYS_PTRACE # For /proc inspection
    containers:
        - name: app
          image: nginx:alpine
```

**Approach 2: Regular sidecar container (all K8s versions):**

```yaml
apiVersion: v1
kind: Pod
metadata:
    name: demo-app
spec:
    shareProcessNamespace: true # Enable PID namespace sharing
    containers:
        - name: app
          image: nginx:alpine
        - name: coral-agent # Regular sidecar container
          image: coral-agent:latest
          securityContext:
              capabilities:
                  add:
                      - SYS_ADMIN # For nsenter
                      - SYS_PTRACE # For /proc inspection
```

**Note:** Traditional initContainers (without `restartPolicy: Always`) run and
complete before the app starts, so they cannot use nsenter on running containers.

**Kubernetes node agent (DaemonSet):**

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
    name: coral-agent
spec:
    template:
        spec:
            hostPID: true # See all processes on the node
            hostNetwork: true # Access node network
            containers:
                - name: agent
                  image: coral-agent:latest
                  securityContext:
                      capabilities:
                          add:
                              - SYS_ADMIN # For nsenter
                              - SYS_PTRACE # For /proc inspection
```

## Implementation Plan

### Phase 1: Protocol Definition

- [ ] Add `ContainerExecRequest` message to `agent.proto`
- [ ] Add `ContainerExecResponse` message to `agent.proto`
- [ ] Add `rpc ContainerExec()` to `AgentService`
- [ ] Run `make proto` to generate Go code

### Phase 2: Agent Implementation

- [ ] Create `internal/agent/container_handler.go`
- [ ] Implement `NewContainerHandler()` constructor
- [ ] Implement `ContainerExec()` RPC handler
- [ ] Implement `detectContainerPID()` helper (scan /proc)
- [ ] Implement `buildNsenterCommand()` helper (construct nsenter args)
- [ ] Implement `executeCommand()` helper (run and capture output)
- [ ] Register handler in `internal/agent/server.go`
- [ ] Add unit tests for PID detection and command construction

### Phase 3: Colony MCP Tool

- [ ] Add `ContainerExecInput` struct to `internal/colony/mcp/types.go`
- [ ] Create `executeContainerExecTool()` in `internal/colony/mcp/tools_exec.go`
- [ ] Create `formatContainerExecResponse()` for output formatting
- [ ] Register tool in `internal/colony/mcp/server.go` (5 integration points)
- [ ] Add tool description and JSON schema

### Phase 4: Testing & Documentation

- [ ] Add unit tests for agent handler
- [ ] Add integration tests with docker-compose example
- [ ] Test edge cases (timeout, missing capabilities, errors)
- [ ] Document in `examples/docker-compose/README.md`
- [ ] Add MCP tool usage examples
- [ ] Update CHANGELOG.md

## API Changes

### New Protobuf Messages

```protobuf
// ContainerExecRequest executes command in container's namespace.
message ContainerExecRequest {
    // Container name (optional in sidecar mode - defaults to main container).
    string container_name = 1;

    // Command as array (no shell interpretation).
    // Example: ["cat", "/app/config.yaml"] or ["ls", "-la", "/etc/nginx"]
    repeated string command = 2;

    // User making request (for audit).
    string user_id = 3;

    // Timeout in seconds (default: 30, max: 300).
    uint32 timeout_seconds = 4;

    // Working directory (optional, uses container's default).
    string working_dir = 5;

    // Additional environment variables.
    map<string, string> env = 6;

    // Namespaces to enter (default: ["mnt"] for sidecar mode).
    // Options: "mnt", "pid", "net", "ipc", "uts", "cgroup"
    repeated string namespaces = 7;
}

// ContainerExecResponse contains command execution results.
message ContainerExecResponse {
    // Standard output from command.
    bytes stdout = 1;

    // Standard error from command.
    bytes stderr = 2;

    // Exit code from command.
    int32 exit_code = 3;

    // Session ID for audit reference.
    string session_id = 4;

    // Execution duration in milliseconds.
    uint32 duration_ms = 5;

    // Error message if execution failed.
    string error = 6;

    // Container PID used for nsenter (for debugging).
    int32 container_pid = 7;

    // Namespaces that were entered.
    repeated string namespaces_entered = 8;
}
```

### New RPC Endpoints

```protobuf
service AgentService {
    // Existing methods...
    rpc ShellExec(ShellExecRequest) returns (ShellExecResponse);

    // New method for container namespace execution.
    rpc ContainerExec(ContainerExecRequest) returns (ContainerExecResponse);
}
```

### MCP Tool

**Tool Name:** `coral_container_exec`

**Input Schema:**

```json
{
    "service": "demo-app",
    "command": [
        "cat",
        "/etc/nginx/nginx.conf"
    ],
    "timeout_seconds": 30
}
```

**Optional Parameters:**

```json
{
    "service": "api-server",
    "agent_id": "agent-xyz-123",
    "container_name": "nginx",
    "command": [
        "ls",
        "-la",
        "/app/config"
    ],
    "working_dir": "/app",
    "env": {
        "DEBUG": "true"
    },
    "namespaces": [
        "mnt"
    ],
    "timeout_seconds": 60
}
```

**Example Output:**

```
Executed in container namespace (PID 42) on agent agent-docker-1
Service: demo-app
Container PID: 42
Namespaces: mnt
Duration: 45ms
Exit Code: 0

=== STDOUT ===
user www-data;
worker_processes auto;
error_log /var/log/nginx/error.log notice;
pid /var/run/nginx.pid;

events {
    worker_connections 1024;
}
```

### Configuration Changes

No configuration changes required. Uses existing docker-compose capabilities:

- `pid: "service:demo-app"` (already configured)
- `cap_add: [SYS_ADMIN, SYS_PTRACE]` (already configured)

## Testing Strategy

### Unit Tests

**`internal/agent/container_handler_test.go`:**

- `TestDetectContainerPID()`: Mock /proc filesystem, verify PID detection
- `TestBuildNsenterCommand()`: Verify command construction with various
  namespace combinations
- `TestNamespaceMapping()`: Verify namespace string to flag conversion
- `TestInputValidation()`: Invalid commands, empty arrays, long timeouts

### Integration Tests

**Docker-compose example tests:**

```bash
cd examples/docker-compose
docker-compose up -d

# Test 1: Read nginx config (container filesystem)
colony mcp test-tool coral_container_exec \
  --service demo-app \
  --command '["cat", "/etc/nginx/nginx.conf"]'

# Verify output shows nginx.conf contents from container's filesystem

# Test 2: List mounted files
colony mcp test-tool coral_container_exec \
  --service demo-app \
  --command '["ls", "-la", "/usr/share/nginx/html"]'

# Test 3: Verify isolation - agent's host filesystem is different
colony mcp test-tool coral_shell_exec \
  --service demo-app \
  --command '["cat", "/etc/nginx/nginx.conf"]'

# Expected: "No such file or directory" (proves namespace isolation)

# Test 4: Timeout enforcement
colony mcp test-tool coral_container_exec \
  --service demo-app \
  --command '["sleep", "100"]' \
  --timeout_seconds 5

# Expected: Timeout error after 5 seconds

# Test 5: Working directory parameter
colony mcp test-tool coral_container_exec \
  --service demo-app \
  --command '["pwd"]' \
  --working_dir /usr/share/nginx

# Expected: /usr/share/nginx

# Test 6: Environment variables
colony mcp test-tool coral_container_exec \
  --service demo-app \
  --command '["env"]' \
  --env '{"DEBUG": "true"}'

# Verify DEBUG=true appears in output
```

### Edge Cases

- **nsenter not available**: Check at agent startup, log warning
- **Insufficient capabilities**: Clear error message referencing CAP_SYS_ADMIN
- **Container not running**: PID detection fails with helpful error
- **Invalid namespace**: Ignore unknown namespaces, log warning
- **Command not found**: Pass through exit code 127 and stderr
- **Large output**: Handle binary and text output, buffer size limits
- **Concurrent executions**: Thread-safe handler implementation

## Security Considerations

**Required Capabilities:**

- `CAP_SYS_ADMIN`: For nsenter to enter mount namespace
- `CAP_SYS_PTRACE`: For /proc filesystem inspection

Already configured in example deployments (docker-compose, K8s manifests).

**UID/GID Handling (Phase 1):**

Commands execute as root within the container's namespace. Container filesystem
permissions still apply. This is acceptable for sidecar deployments where the
agent and application share a trust boundary.

**Future Enhancement:**

- Detect container's main process UID/GID
- Use nsenter `--setuid` and `--setgid` flags to match container's security
  context

**Input Validation:**

- Command array: non-empty, reasonable length (< 100 args)
- Timeout: enforce maximum of 300 seconds
- Namespaces: whitelist ["mnt", "pid", "net", "ipc", "uts", "cgroup"]
- Container name: sanitize, prevent path traversal
- Working directory: validate path format

**Audit Logging:**

All executions logged to DuckDB with:

- session_id, timestamp, user_id
- agent_id, service_name, container_pid
- command array (sanitized, no secrets)
- namespaces_entered, exit_code, duration_ms
- stdout/stderr size (not full content for large outputs)
- error messages

**Redaction:**
Follow same rules as RFD 045 (ShellExec):

- Redact environment variables matching: `PASSWORD`, `SECRET`, `TOKEN`, `KEY`,
  `API_KEY`
- Redact command arguments containing common secret patterns

## Future Enhancements

**Advanced Container Detection** (RFD TBD):

- Parse cgroup paths to identify specific containers
- Use container labels/environment variables for validation
- Support explicit PID override parameter
- Handle multi-container sidecars (select by name)

**UID/GID Preservation** (Low Priority):

```go
// Execute as container's user instead of root
uid, gid := getProcessUID(containerPID)
nsenterArgs = append(nsenterArgs, "--setuid", uid, "--setgid", gid)
```

**CRI Integration** (Optional Enhancement):

While nsenter works for all deployment modes (sidecars and node agents), CRI
integration could provide additional benefits:

- Container name resolution (map names to PIDs automatically)
- Runtime-agnostic container metadata
- Standard Kubernetes exec semantics

However, nsenter is simpler and sufficient for current needs.

**Interactive TTY Sessions** (Deferred to RFD 051 enhancement):

- Allocate PTY for interactive debugging
- Combine with shell session audit (RFD 042)
- Integrate with RBAC and approval (RFD 043)

---

## Implementation Status

**Core Capability:** ⏳ Not Started

This RFD supersedes RFD 017's CRI-based approach with a simpler nsenter-based
implementation that works across multiple deployment modes.

**Why nsenter over CRI:**

- Simpler: no runtime-specific API complexity
- Available: works with Docker, containerd, any OCI runtime
- Portable: single approach for docker-compose, K8s sidecars, K8s DaemonSets
- Sufficient: provides filesystem isolation (the primary need)

**What Will Work:**

- Execute commands in container's mount namespace
- Access container-mounted configs, logs, volumes
- Automatic container PID detection via /proc scanning
- Works in sidecar mode (shared PID namespace)
- Works in node agent mode (hostPID: true)
- MCP tool integration for Claude Desktop
- Audit logging and timeout enforcement

**Deployment Requirements:**

**For sidecars** (docker-compose, K8s with shareProcessNamespace):

- Shared PID namespace with application container
- CAP_SYS_ADMIN and CAP_SYS_PTRACE capabilities
- nsenter binary in agent container

**For node agents** (K8s DaemonSet):

- `hostPID: true` to see all node processes
- CAP_SYS_ADMIN and CAP_SYS_PTRACE capabilities
- nsenter binary in agent container
- Optional: namespace/pod filtering for multi-tenancy

## Deferred Features

**Multi-Container Support** (Future):

Explicit container selection when multiple containers exist:

```json
{
    "service": "demo-app",
    "container_name": "nginx",
    "command": [
        "ls",
        "/app"
    ]
}
```

Requires cgroup parsing to map container names to PIDs.

**Pod/Namespace Filtering for Multi-Tenancy** (Future):

In Kubernetes DaemonSet mode with `hostPID: true`, the agent can see all
containers on the node. For multi-tenant clusters, add filtering:

- Only exec into containers in authorized namespaces
- Check pod labels/annotations for opt-in/opt-out
- Implement RBAC at the agent level
- Audit all cross-namespace access

This prevents unauthorized access to neighbor tenant containers.

**User Context Preservation** (Low Priority):

Execute commands as the container's non-root user:

- Improves security (matches app's permission model)
- Requires UID/GID detection via /proc/<pid>/status
- Uses nsenter --setuid/--setgid flags

## Appendix

### Container PID Detection Algorithm

**Basic approach** (works for sidecar mode):

```go
// detectContainerPID finds the main container process.
// Works in:
// - Docker-compose sidecar: shared PID namespace with app container
// - K8s sidecar: shareProcessNamespace: true
// - K8s DaemonSet: hostPID: true (sees all node containers)
func detectContainerPID() (int, error) {
    // 1. Scan /proc for numeric directories (PIDs)
    entries, err := os.ReadDir("/proc")
    if err != nil {
        return 0, fmt.Errorf("failed to read /proc: %w", err)
    }

    var pids []int
    for _, entry := range entries {
        if !entry.IsDir() {
            continue
        }

        // Parse PID from directory name
        pid, err := strconv.Atoi(entry.Name())
        if err != nil {
            continue // Not a numeric directory
        }

        // Skip init (PID 1) and our own process
        if pid <= 1 || pid == os.Getpid() {
            continue
        }

        pids = append(pids, pid)
    }

    // 2. Sort PIDs (lowest first)
    sort.Ints(pids)

    if len(pids) == 0 {
        return 0, errors.New("no container PID found")
    }

    // 3. Return lowest PID (container starts before agent in sidecar mode)
    return pids[0], nil
}
```

**Enhanced approach** for K8s DaemonSet (multi-container selection):

In DaemonSet mode with `hostPID: true`, you see ALL containers on the node. To
select a specific container:

1. Parse `/proc/<pid>/cgroup` to extract container ID
2. Read `/proc/<pid>/cmdline` to identify the process
3. Match against service name or container name from request
4. Optionally filter by namespace/pod labels via K8s API

This enables targeting specific containers: "exec into the nginx container in
the frontend pod".

### nsenter Command Construction

```bash
# Minimum required for filesystem access (sidecar mode)
nsenter -t <container_pid> -m -- <command> [args...]

# With working directory
nsenter -t <container_pid> -m --wd /app -- ls -la

# Multiple namespaces (non-sidecar mode)
nsenter -t <container_pid> -m -p -n -i -u -- <command>

# Namespace flag mapping:
# mnt    → -m  (mount namespace - filesystem)
# pid    → -p  (PID namespace)
# net    → -n  (network namespace)
# ipc    → -i  (IPC namespace)
# uts    → -u  (UTS namespace - hostname)
# cgroup → -C  (cgroup namespace)
```

### Comparison: coral_shell_exec vs coral_container_exec

| Aspect                | coral_shell_exec (RFD 045) | coral_container_exec (RFD 056)            |
|-----------------------|----------------------------|-------------------------------------------|
| **Execution context** | Agent's host environment   | Container's namespace                     |
| **Filesystem view**   | Agent's filesystem         | Container's mounted volumes/configs       |
| **Process view**      | Agent's processes          | Container's processes (if pid ns entered) |
| **Network view**      | Agent's network            | Container's network (if net ns entered)   |
| **Implementation**    | Direct exec.Command()      | nsenter + exec.Command()                  |
| **Use case**          | Host diagnostics           | Container debugging                       |
| **Examples**          | ps aux, ss -tulpn, tcpdump | cat /app/config.yaml, ls /data            |
| **Capabilities**      | None required              | CAP_SYS_ADMIN, CAP_SYS_PTRACE             |

**When to use coral_shell_exec:**

- Network diagnostics: `ss -tulpn`, `tcpdump -i any`
- Process inspection: `ps aux`, `top`, `pgrep nginx`
- Host filesystem: `ls /var/log/coral`, `cat /etc/os-release`
- System commands: `uptime`, `free -h`, `df -h`

**When to use coral_container_exec:**

- App configs: `cat /app/config.yaml`, `cat /etc/nginx/nginx.conf`
- Mounted volumes: `ls -la /data`, `du -sh /uploads`
- Container environment: `env`, `pwd`, `id`
- App-specific files: `ls /usr/share/nginx/html`

### Reference Implementations

- **nsenter(1)**: Part of util-linux package, standard on all Linux systems
- **kubectl exec**: Uses CRI RuntimeService.Exec() - more complex but
  runtime-agnostic
- **docker exec**: Uses Docker API - runtime-specific
- **Kubernetes shareProcessNamespace**: Similar PID namespace sharing pattern

### Test Configuration Examples

**Docker-compose sidecar:**

```yaml
# examples/docker-compose/docker-compose.yml
services:
    demo-app:
        image: nginx:alpine
        volumes:
            - ./html:/usr/share/nginx/html:ro
            - ./nginx.conf:/etc/nginx/nginx.conf:ro

    coral-agent:
        image: coral-agent:latest
        pid: "service:demo-app" # Enable PID namespace sharing
        network_mode: "service:demo-app" # Enable network namespace sharing
        cap_add:
            - NET_ADMIN # For WireGuard
            - SYS_ADMIN # For nsenter (RFD 056)
            - SYS_PTRACE # For /proc inspection
            - SYS_RESOURCE # For eBPF memlock
        devices:
            - /dev/net/tun:/dev/net/tun
        environment:
            - COLONY_URL=http://colony:8080
            - SERVICE_NAME=demo-app
```

**Kubernetes native sidecar (K8s 1.28+):**

```yaml
apiVersion: v1
kind: Pod
metadata:
    name: demo-app
    namespace: default
spec:
    shareProcessNamespace: true # Enable PID sharing between containers
    initContainers:
        - name: coral-agent # Native sidecar (starts before app, keeps running)
          image: coral-agent:latest
          restartPolicy: Always # Makes this a long-running sidecar
          env:
              - name: COLONY_URL
                value: "http://colony.coral-system:8080"
              - name: SERVICE_NAME
                value: "demo-app"
          securityContext:
              capabilities:
                  add:
                      - NET_ADMIN # For WireGuard
                      - SYS_ADMIN # For nsenter (RFD 056)
                      - SYS_PTRACE # For /proc inspection
                      - SYS_RESOURCE # For eBPF
    containers:
        - name: nginx
          image: nginx:alpine
          volumeMounts:
              - name: config
                mountPath: /etc/nginx/nginx.conf
                subPath: nginx.conf
              - name: html
                mountPath: /usr/share/nginx/html

    volumes:
        - name: config
          configMap:
              name: nginx-config
        - name: html
          emptyDir: {}
```

**Kubernetes DaemonSet (node agent):**

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
    name: coral-agent
    namespace: coral-system
spec:
    selector:
        matchLabels:
            app: coral-agent
    template:
        metadata:
            labels:
                app: coral-agent
        spec:
            hostPID: true # See all processes on the node
            hostNetwork: true # Access node network for WireGuard
            serviceAccountName: coral-agent
            containers:
                - name: agent
                  image: coral-agent:latest
                  env:
                      - name: COLONY_URL
                        value: "http://colony.coral-system:8080"
                      - name: NODE_NAME
                        valueFrom:
                            fieldRef:
                                fieldPath: spec.nodeName
                  securityContext:
                      capabilities:
                          add:
                              - NET_ADMIN # For WireGuard
                              - SYS_ADMIN # For nsenter (RFD 056)
                              - SYS_PTRACE # For /proc inspection
                              - SYS_RESOURCE # For eBPF
                  volumeMounts:
                      - name: proc
                        mountPath: /host/proc
                        readOnly: true
            volumes:
                - name: proc
                  hostPath:
                      path: /proc
                      type: Directory
```
