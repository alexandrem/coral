---
rfd: "017"
title: "Exec Command Implementation"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "016" ]
related_rfds: [ "011", "012", "026" ]
database_migrations: [ ]
areas: [ "cli", "agent", "execution", "security" ]
---

# RFD 017 - Exec Command Implementation

**Status:** ❌ Superseded by RFD 056

**Reason:** This RFD proposed depending on CRI socket mount. `nsenter`
approach described in RFD 056 is simpler and more portable.

## Summary

Define the implementation for `coral exec` command, which provides
`kubectl`/`docker-style` exec access into application containers. Uses CRI (
Container Runtime Interface) to execute commands in target containers,
supporting both interactive sessions and one-off commands.

**Key concepts:**

- **kubectl/docker semantics**: Follows industry-standard exec conventions
- **CRI integration**: Direct container runtime API usage for K8s environments
- **Interactive and one-off modes**: Single command with dual behavior
- **App-scoped security**: Inherits application container's privileges only

## Problem

**Current limitations:**

RFD 016 defines high-level command structure but defers implementation details
for accessing application containers. Without this specification:

1. **No application access**: Can't inspect application container internals
   (config files, environment variables, mounted volumes)

2. **CRI integration undefined**:
    - Which CRI API calls to use?
    - How to detect runtime (containerd vs CRI-O vs Docker)?
    - Error handling when container has no shell (distroless images)?

3. **Interactive vs one-off unclear**:
    - How does `coral exec myapp` differ from `coral exec myapp ls`?
    - TTY allocation strategy?
    - Terminal resizing for interactive sessions?

4. **Security boundaries incomplete**:
    - What privileges does exec inherit?
    - How to prevent privilege escalation?
    - Audit requirements?

**Why this matters:**

- **Developer productivity**: Common debugging task is checking app config,
  logs,
  environment
- **Production debugging**: Need to inspect running containers without kubectl
- **Consistent UX**: Following kubectl/docker conventions reduces learning curve
- **Security**: Must maintain container isolation boundaries

## Solution

Implement `coral exec` command that mirrors kubectl/docker exec behavior, using
CRI to access application containers with proper security boundaries.

### 1. Command Semantics

**Single command with dual behavior** based on arguments:

```bash
# Interactive mode (no command arguments)
coral exec myapp
# → Attempts to exec /bin/sh or /bin/bash in myapp container
# → Allocates TTY for interactive session
# → Error if container has no shell

# One-off mode (with command arguments)
coral exec myapp ls -la /app
coral exec myapp cat /app/config.yaml
coral exec myapp curl localhost:8080/health
# → Executes command in myapp container
# → Captures and returns output
# → Exits with command's exit code
```

**Familiar to users of:**

| Existing Tool                | Coral Equivalent      | Behavior                 |
|------------------------------|-----------------------|--------------------------|
| `kubectl exec -it pod -- sh` | `coral exec myapp`    | Interactive shell in app |
| `kubectl exec pod -- ls`     | `coral exec myapp ls` | One-off command in app   |
| `docker exec -it app sh`     | `coral exec myapp`    | Interactive shell in app |
| `docker exec app ls`         | `coral exec myapp ls` | One-off command in app   |

### 2. Execution Architecture

**CRI-based execution** for containerized environments:

```
┌─────────────────────────────────────────────────────────┐
│ CLI (coral exec myapp [command...])                     │
└────────────────────┬────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────┐
│ Colony RPC (routes to target agent)                     │
└────────────────────┬────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────┐
│ Agent Execution Router                                  │
│ (selects backend based on runtime context)              │
└──┬──────────┬──────────────────────────────────────────┘
   │          │
   ▼          ▼
┌─────┐  ┌────────────┐
│Native│  │CRI Exec    │
│Exec  │  │(containerd,│
│      │  │CRI-O, etc.)│
└─────┘  └────────────┘
```

**Backend selection:**

| Runtime Context      | Backend     | Notes                            |
|----------------------|-------------|----------------------------------|
| Native on host       | Native exec | Direct process execution         |
| K8s Sidecar          | CRI exec    | Exec into sibling container      |
| K8s DaemonSet        | CRI exec    | Exec into target pod's container |
| Container (isolated) | CRI exec    | Exec into target container       |

### 3. CRI Integration

**Container Runtime Interface (CRI) support** for accessing application
containers.

#### CRI Socket Detection

Agent auto-detects CRI socket on startup by testing common paths:

1. `/var/run/containerd/containerd.sock` (containerd)
2. `/var/run/crio/crio.sock` (CRI-O)
3. `/var/run/docker.sock` (Docker)
4. `/run/containerd/containerd.sock` (alternative containerd path)

**Config override:**

```yaml
agent:
    kubernetes:
        cri_socket: /var/run/containerd/containerd.sock  # Explicit path
        cri_runtime: containerd  # containerd, crio, docker, auto
```

#### CRI API Usage

**For one-off commands:**

1. Connect to CRI runtime via socket
2. Find target container by ID
3. Create exec process with command, env, working dir
4. Wait for completion with timeout
5. Capture stdout/stderr output
6. Return exit code and output

**For interactive sessions:**

1. Allocate pseudo-TTY (PTY)
2. Connect to CRI runtime
3. Find target container
4. Create exec process with TTY enabled
5. Attach I/O streams (stdin, stdout, stderr)
6. Start process and handle terminal resizing
7. Wait for user exit
8. Cleanup PTY and return exit code

#### CRI Runtime Compatibility

| Runtime    | Socket                                | API Style             | Notes                     |
|------------|---------------------------------------|-----------------------|---------------------------|
| containerd | `/var/run/containerd/containerd.sock` | Native containerd API | Preferred for K8s 1.24+   |
| CRI-O      | `/var/run/crio/crio.sock`             | gRPC CRI v1           | OpenShift default         |
| Docker     | `/var/run/docker.sock`                | Docker API            | Legacy, deprecated in K8s |

**Abstraction layer** provides unified interface:

```go
type RuntimeClient interface {
Exec(ctx context.Context, containerID string, cmd []string, opts ExecOptions) (ExecResult, error)
ListContainers(ctx context.Context) ([]Container, error)
}

type ExecOptions struct {
Env        map[string]string
WorkingDir string
TTY        bool
Stdin      io.Reader
Stdout     io.Writer
Stderr     io.Writer
}

type ExecResult struct {
ExitCode int
Stdout   string
Stderr   string
Duration time.Duration
}
```

### 4. Interactive Mode Details

**TTY Allocation and Terminal Resizing:**

1. **Client side**: Listen for SIGWINCH (window change) signal
2. **On resize**: Query current terminal size (rows, cols)
3. **Send to agent**: RPC call with new dimensions
4. **Agent forwards**: Update process PTY size via CRI API
5. **Container updates**: Terminal automatically adjusts

**Signal Forwarding:**

1. **Client side**: Catch signals (SIGINT, SIGTERM, SIGTSTP)
2. **Send to agent**: RPC call with signal name
3. **Agent forwards**: Send signal to container process
4. **Container handles**: Process receives and responds to signal

**Session Cleanup:**

- On disconnect: Send SIGTERM to process
- Wait 5s, then SIGKILL if still running
- Close I/O streams and release PTY
- Record audit log

### 5. One-Off Command Mode

**Timeout and Output Handling:**

```yaml
agent:
    exec:
        default_timeout: 30s      # Default timeout for one-off commands
        max_timeout: 600s         # Maximum allowed timeout
        buffer_size: 32768        # Output buffer size (bytes)
```

**Command execution:**

- Runs command with timeout
- Buffers stdout/stderr during execution
- Returns output and exit code when complete
- Kills process if timeout exceeded

**CLI usage:**

```bash
# Default 30s timeout
coral exec myapp curl localhost/health

# Custom timeout
coral exec myapp --timeout=5m long-running-task

# Specify working directory
coral exec myapp --workdir=/app ls -la

# Set environment variables
coral exec myapp --env KEY=value printenv KEY
```

### 6. Security Boundaries

**Exec operations are scoped to target container's security context:**

#### Container Isolation

```yaml
# K8s sidecar with explicit targeting
spec:
    containers:
        -   name: app
            image: myapp:latest
        -   name: coral-agent
            image: coral-agent:latest
            env:
                -   name: CORAL_CONNECT
                    value: container://app  # Agent can ONLY exec into 'app'
            volumeMounts:
                -   name: cri-sock
                    mountPath: /var/run/containerd/containerd.sock
                    readOnly: true  # Read-only prevents runtime modification
```

**Enforcement:**

- Agent validates container ID against allowed targets
- Reject exec requests to non-allowed containers
- Return error with allowed container list

#### Privilege Inheritance

**Exec processes inherit container's UID/GID:**

1. Query container's security context
2. Get container's primary process UID/GID
3. Create exec process with same UID/GID
4. No additional capabilities granted
5. Cannot escalate to root if app runs as non-root

**Example:**

```bash
# App container runs as UID 1000
$ coral exec myapp id
uid=1000(app) gid=1000(app) groups=1000(app)

# Exec inherits same UID - cannot become root
$ coral exec myapp whoami
app
```

#### RBAC Enforcement

**Permission checks before execution:**

1. **Authenticate user**: Validate token and extract user identity
2. **Resolve target**: Find agent monitoring target application
3. **Check RBAC permissions**: Query policy for (user, environment, command)
4. **Forward to agent**: Route command to agent with user context

**RBAC config example:**

```yaml
rbac:
    users:
        -   name: alice@company.com
            role: developer
            permissions:
                -   environments: [ dev, staging ]
                    commands: [ exec ]

        -   name: bob@company.com
            role: sre
            permissions:
                -   environments: [ production ]
                    commands: [ exec ]
                    require_approval: true  # Production requires approval
```

### 7. Audit and Logging

**Comprehensive audit trail for compliance:**

#### Exec Audit Log

**Fields captured:**

```go
type ExecAuditLog struct {
ID          string
Timestamp   time.Time
UserID      string
AgentID     string
ContainerID string
Command     []string
Environment map[string]string
WorkingDir  string
ExitCode    int
Duration    time.Duration
Stdout      string // Captured output (one-off mode only)
Stderr      string
Interactive bool // True for interactive mode
Approved    bool
ApproverID  *string
}
```

**Storage strategy:**

- Store locally in agent's DuckDB (immediate persistence)
- Send copy to Colony (centralized audit aggregation)
- Apply redaction rules before storage
- Compress large outputs to save space

**DuckDB schema:**

```sql
CREATE TABLE exec_audit
(
    id           VARCHAR PRIMARY KEY,
    timestamp    TIMESTAMP NOT NULL,
    user_id      VARCHAR   NOT NULL,
    agent_id     VARCHAR   NOT NULL,
    container_id VARCHAR,
    command      VARCHAR[] NOT NULL,
    exit_code    INTEGER,
    duration INTERVAL,
    interactive  BOOLEAN   NOT NULL,
    approved     BOOLEAN,
    approver_id  VARCHAR
);
```

**Redaction rules:**

```yaml
agent:
    audit:
        enabled: true
        retention_days: 90
        redact_env_vars:
            - PASSWORD
            - API_KEY
            - SECRET
            - TOKEN
```

## API Changes

### Agent gRPC API

**Exec RPC:**

```protobuf
service Agent {
    // Exec: Execute command in target container
    rpc Exec(stream ExecRequest) returns (stream ExecResponse);

    // Terminal management for interactive mode
    rpc ResizeTerminal(ResizeTerminalRequest) returns (google.protobuf.Empty);
    rpc SendSignal(SendSignalRequest) returns (google.protobuf.Empty);
}

message ExecRequest {
    oneof payload {
        ExecStart start = 1;      // First message from client
        bytes stdin = 2;          // Stdin data (interactive mode)
        ExecResize resize = 3;    // Terminal resize event
        ExecSignal signal = 4;    // Signal to send to process
    }
}

message ExecStart {
    string container_id = 1;      // Target container (or empty for native)
    repeated string command = 2;  // Command and args (empty = default shell)
    map<string, string> env = 3;  // Environment variables
    string working_dir = 4;       // Working directory
    bool tty = 5;                 // Allocate TTY (auto-detected if not set)
    int32 timeout_seconds = 6;    // Timeout for one-off commands (0 = no timeout)
    string user_id = 7;           // User making request (for audit)
    string approval_id = 8;       // Approval request ID (if required)
}

message ExecResponse {
    oneof payload {
        bytes stdout = 1;         // Stdout data
        bytes stderr = 2;         // Stderr data
        ExecExit exit = 3;        // Final message with exit code
    }
}

message ExecExit {
    int32 exit_code = 1;
    string audit_id = 2;  // Audit log ID for reference
}

message ExecResize {
    uint32 rows = 1;
    uint32 cols = 2;
}

message ExecSignal {
    string signal = 1;  // SIGINT, SIGTERM, SIGTSTP, etc.
}

message ResizeTerminalRequest {
    string exec_id = 1;
    uint32 rows = 2;
    uint32 cols = 3;
}

message SendSignalRequest {
    string exec_id = 1;
    string signal = 2;
}
```

### CLI Commands

```bash
# Interactive mode
coral exec <target>
coral exec <target> --tty  # Force TTY allocation

# One-off command mode
coral exec <target> <command> [args...]
coral exec <target> --timeout=30s <command>
coral exec <target> --workdir=/app <command>
coral exec <target> --env KEY=value <command>

# Examples
coral exec myapp                          # Interactive shell
coral exec myapp ls -la /app              # List app directory
coral exec myapp cat /app/config.yaml     # View config
coral exec myapp curl localhost/health    # Health check
coral exec myapp --timeout=5m long-task   # Long-running command
```

## Implementation Plan

### Phase 1: Native Execution Backend

- [ ] Implement native executor for host-level exec
- [ ] Basic one-off command support
- [ ] Interactive mode with TTY allocation
- [ ] Signal forwarding (Ctrl+C, Ctrl+Z)
- [ ] Terminal resizing support
- [ ] Unit tests for native execution

### Phase 2: CRI Integration

- [ ] CRI socket detection and auto-detection
- [ ] Containerd client implementation
- [ ] CRI-O client implementation
- [ ] Docker API shim (legacy support)
- [ ] Runtime abstraction layer
- [ ] Integration tests with containerd

### Phase 3: CLI Implementation

- [ ] `coral exec` command parsing
- [ ] Interactive vs one-off mode detection
- [ ] TTY allocation and terminal handling
- [ ] Progress indicators for long-running commands
- [ ] Error messages and help text
- [ ] CLI integration tests

### Phase 4: Security and RBAC

- [ ] Container targeting validation
- [ ] UID/GID inheritance from container
- [ ] Privilege escalation prevention
- [ ] RBAC permission checks (Colony-side)
- [ ] Approval workflow integration
- [ ] Security integration tests

### Phase 5: Audit and Logging

- [ ] Exec audit log schema (DuckDB)
- [ ] Audit log storage and retention
- [ ] Environment variable redaction
- [ ] Colony audit aggregation
- [ ] Audit query tools

## Testing Strategy

### Unit Tests

- Native executor (fork/exec)
- CRI client (mocked containerd API)
- Signal forwarding
- Terminal resizing
- Container targeting validation
- UID/GID inheritance

### Integration Tests

- Exec into containerd container
- Exec with CRI-O
- Docker API compatibility
- Interactive session lifecycle
- One-off command timeout

### E2E Tests

**Deployment scenarios:**

1. **Native agent**: `coral exec` on host process
2. **K8s sidecar**: `coral exec` into app container
3. **K8s DaemonSet**: `coral exec` into remote pod

**Command scenarios:**

1. Interactive shell (bash/sh)
2. One-off commands (ls, cat, curl)
3. Long-running commands with timeout
4. Distroless containers (no shell error handling)

**Security scenarios:**

- RBAC denial (unprivileged user → production)
- Approval workflow (SRE approval required)
- Container escape attempt (blocked)
- Audit log verification

## Security Considerations

### Container Escape Prevention

**Risk**: Exec could enable container escape if improperly implemented.

**Mitigations:**

- Inherit container's UID/GID (no root by default)
- No additional capabilities granted
- CRI socket mounted read-only
- Explicit container targeting (no wildcard)
- Audit all exec operations

### Distroless Container Handling

**Risk**: Many production containers have no shell (distroless, FROM scratch).

**Handling:**

```bash
$ coral exec myapp
Error: Container has no shell (/bin/sh, /bin/bash not found)

Tip: For debugging distroless containers, use 'coral shell myapp' to access
agent's debug environment with troubleshooting tools.
```

### Sensitive Data Exposure

**Risk**: Command output may contain passwords, API keys.

**Mitigations:**

- Environment variable redaction (`PASSWORD`, `API_KEY`, etc.)
- Configurable redaction patterns
- Secure storage for audit logs
- RBAC for audit log access

## Future Enhancements

### Multi-Container Targeting

Support specifying container in multi-container pods:

```bash
coral exec myapp/sidecar ls
coral exec myapp --container=nginx cat /etc/nginx/nginx.conf
```

### File Upload/Download

```bash
coral exec myapp --upload=local.txt:/app/config.txt
coral exec myapp --download=/app/logs/error.log:./error.log
```

### Session Recording Export

```bash
coral exec replay <audit-id>  # Replay interactive session
coral exec export <audit-id> --format=asciinema  # Export for sharing
```

---

## Notes

**Relationship to Other RFDs:**

- **RFD 016**: Parent RFD defining command structure
- **RFD 026**: `coral shell` implementation (agent debug environment)
- **RFD 011**: Multi-service agents (exec operates on services)
- **RFD 012**: K8s node agent (DaemonSet mode exec)

**Why Separate from RFD 026:**

`coral exec` and `coral shell` serve different purposes:

- **exec**: Access application container (kubectl/docker semantics)
- **shell**: Agent debug environment (coral-specific feature)

Separate RFDs allow independent implementation and clearer focus.
