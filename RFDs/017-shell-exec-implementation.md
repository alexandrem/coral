---
rfd: "017"
title: "Shell and Exec Command Implementation"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "016" ]
related_rfds: [ "011", "012", "013" ]
database_migrations: [ ]
areas: [ "cli", "agent", "execution", "security" ]
---

# RFD 017 - Shell and Exec Command Implementation

**Status:** 🚧 Draft

## Summary

Define the detailed implementation for `coral shell` and `coral exec` commands,
covering runtime-specific execution strategies, CRI integration, session
management, security boundaries, and audit capabilities. This RFD provides the
technical depth referenced by RFD 016's high-level command structure.

**Key concepts:**

- **Runtime-adaptive execution**: Different backends (native, CRI, shared
  namespace, K8s API)
- **Session management**: Interactive shell lifecycle, multiplexing, cleanup
- **Security boundaries**: Container isolation, RBAC enforcement, audit logging
- **CRI integration**: Direct container runtime API usage for K8s sidecar mode
- **Exec vs Shell semantics**: One-off commands vs interactive sessions

## Problem

**Current limitations:**

RFD 016 defines `coral shell` and `coral exec` as core commands but defers
implementation details. Without this specification:

1. **Execution strategy unclear**: How do commands actually run in different
   contexts (native, container, K8s sidecar, DaemonSet)?

2. **CRI integration undefined**: RFD 016 proposes CRI socket approach for K8s
   sidecars, but implementation details missing:
    - Which CRI API calls to use?
    - How to detect runtime (containerd vs CRI-O vs Docker)?
    - Error handling and fallback strategies?

3. **Session management unspecified**: Interactive shells need:
    - TTY allocation and terminal resizing
    - Signal forwarding (Ctrl+C, Ctrl+Z)
    - Session cleanup on disconnect
    - Multiplexing for concurrent sessions

4. **Security model incomplete**:
    - What can `exec`/`shell` commands do?
    - How to prevent privilege escalation?
    - Container escape prevention?
    - Audit requirements?

5. **No standardized exit behavior**:
    - When does `exec` timeout?
    - How do long-running commands in `exec` behave?
    - Shell session cleanup on network failure?

**Why this matters:**

- **Implementation risk**: Without clear spec, implementation may vary across
  runtime contexts.
- **Security gaps**: Unclear boundaries enable privilege escalation or container
  escape.
- **User confusion**: Inconsistent behavior between native and container modes.
- **Debugging difficulty**: No audit trail for troubleshooting production
  access.

## Solution

Define comprehensive implementation specification with runtime-adaptive
execution backends, clear security boundaries, and robust session management.

### 1. Execution Architecture

**Four execution backends** based on runtime context:

```
┌─────────────────────────────────────────────────────────┐
│ CLI (coral shell / coral exec)                         │
└────────────────────┬────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────┐
│ Agent Execution Router                                  │
│ (selects backend based on runtime context)              │
└──┬──────────┬──────────┬──────────────┬─────────────────┘
   │          │          │              │
   ▼          ▼          ▼              ▼
┌─────┐  ┌────────┐  ┌──────────┐  ┌──────────────┐
│Native│  │CRI API │  │Shared NS │  │Kubernetes API│
│Exec  │  │        │  │(nsenter) │  │(kubectl exec)│
└─────┘  └────────┘  └──────────┘  └──────────────┘
```

**Backend selection matrix:**

| Runtime Context          | `shell` Backend       | `exec` Backend        | Fallback               |
|--------------------------|-----------------------|-----------------------|------------------------|
| Native on host           | Native exec           | Native exec           | N/A                    |
| Container (isolated)     | CRI Exec              | CRI Exec              | Native if CRI fails    |
| Container (host NS)      | Native exec           | Native exec           | N/A                    |
| K8s Sidecar (CRI socket) | CRI Exec              | CRI Exec              | Shared NS if CRI fails |
| K8s Sidecar (shared NS)  | Shared NS exec        | Shared NS exec        | Error if fails         |
| K8s Sidecar (passive)    | ❌ Not supported       | ❌ Not supported       | Show error             |
| K8s DaemonSet            | CRI Exec (target pod) | CRI Exec (target pod) | K8s API if CRI fails   |

### 2. CRI Integration

**Container Runtime Interface (CRI) support** for K8s sidecar and DaemonSet modes.

#### CRI Socket Detection

Agent auto-detects CRI socket on startup by testing common paths in order:

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

#### CRI API Usage Strategy

**For `coral exec`** (one-off command):

1. Connect to CRI runtime via socket
2. Find target container by ID
3. Create exec process with command, env, working dir
4. Wait for completion with timeout
5. Capture stdout/stderr output
6. Return exit code and output

**For `coral shell`** (interactive session):

1. Allocate pseudo-TTY (PTY)
2. Connect to CRI runtime
3. Find target container
4. Create exec process with TTY enabled
5. Attach I/O streams (stdin, stdout, stderr)
6. Start process and handle terminal resizing
7. Wait for user exit
8. Cleanup PTY and return exit code

#### CRI Runtime Compatibility

**API Differences:**

| Runtime    | Socket                                | API Style             | Notes                     |
|------------|---------------------------------------|-----------------------|---------------------------|
| containerd | `/var/run/containerd/containerd.sock` | Native containerd API | Preferred for K8s 1.24+   |
| CRI-O      | `/var/run/crio/crio.sock`             | gRPC CRI v1           | OpenShift default         |
| Docker     | `/var/run/docker.sock`                | Docker API (not CRI)  | Legacy, deprecated in K8s |

**Abstraction layer** provides unified interface across runtimes:

```go
type RuntimeClient interface {
    Exec(ctx context.Context, containerID string, cmd []string) (ExecResult, error)
    Shell(ctx context.Context, containerID string, shell string) (Session, error)
    ListContainers(ctx context.Context) ([]Container, error)
}

// Implementations: ContainerdClient, CRIOClient, DockerClient (via shim)
```

### 3. Session Management

**For `coral shell`** (interactive sessions):

#### Session Lifecycle

```
1. Create → 2. Attach → 3. Active → 4. Detach/Exit → 5. Cleanup
   ↓           ↓           ↓            ↓                ↓
 Allocate   Connect    Forward      Signal          Release
   TTY       I/O       signals      handler         resources
```

**Session state:**

```go
type Session struct {
    ID          string
    UserID      string
    AgentID     string
    ContainerID string
    StartedAt   time.Time
    LastActive  time.Time
    Status      SessionStatus // ACTIVE, DETACHED, EXITED
    ExitCode    *int
    Audit       *AuditLog
}

type SessionStatus int

const (
    SessionActive SessionStatus = iota
    SessionDetached
    SessionExited
)
```

#### TTY and Terminal Resizing

**Terminal size synchronization strategy:**

1. **Client side**: Listen for SIGWINCH (window change) signal
2. **On resize**: Query current terminal size (rows, cols)
3. **Send to agent**: RPC call with new dimensions
4. **Agent forwards**: Update process PTY size via CRI API
5. **Container updates**: Terminal automatically adjusts

#### Signal Forwarding

**Signal handling strategy** (Ctrl+C, Ctrl+Z, etc.):

1. **Client side**: Catch signals (SIGINT, SIGTERM, SIGTSTP)
2. **Send to agent**: RPC call with signal name
3. **Agent forwards**: Send signal to container process
4. **Container handles**: Process receives and responds to signal

#### Session Cleanup

**Cleanup on disconnect:**

1. **Kill process**: Send SIGTERM, wait 5s, then SIGKILL if needed
2. **Close I/O streams**: Release PTY and socket resources
3. **Finalize audit log**: Record end time and exit code
4. **Remove from registry**: Delete session from active sessions map

**Idle timeout configuration:**

```yaml
agent:
    shell:
        idle_timeout: 30m      # Kill session after 30min idle
        max_duration: 4h       # Force kill after 4h regardless
        cleanup_interval: 5m   # Check for stale sessions every 5min
```

**Background cleanup** runs periodically to:
- Kill idle sessions (no activity for `idle_timeout`)
- Kill long-running sessions (exceeded `max_duration`)
- Remove orphaned sessions (process exited but not cleaned)

### 4. Exec vs Shell Semantics

**Clear behavioral differences:**

| Aspect        | `coral exec`              | `coral shell`               |
|---------------|---------------------------|-----------------------------|
| **Purpose**   | One-off command           | Interactive session         |
| **Duration**  | Exits on completion       | Until user exits            |
| **Timeout**   | Default 30s, configurable | Idle timeout only           |
| **TTY**       | No (unless `--tty` flag)  | Yes (always)                |
| **Signals**   | SIGTERM on timeout        | Forwarded to shell          |
| **Output**    | Buffered, returned        | Streamed live               |
| **Exit code** | Returned to CLI           | Shown but not propagated    |
| **Audit**     | Command + output logged   | Session + transcript logged |

**Example usage:**

```bash
# Exec: Run health check (exits immediately)
coral exec "curl http://localhost/health"
# Output: {"status": "ok"}
# Exit code: 0

# Shell: Interactive debugging session
coral shell
# Drops into shell, waits for user input
app $ curl http://localhost/health
{"status": "ok"}
app $ ps aux
...
app $ exit
# Session ends, transcript saved
```

### 5. Security Boundaries

#### Container Isolation

**Exec/shell operations are scoped to container:**

```yaml
# K8s sidecar with explicit targeting
spec:
    initContainers:
        -   name: coral-agent
            args:
                - --connect=container://app  # Agent can ONLY exec into 'app' container
            volumeMounts:
                -   name: cri-sock
                    mountPath: /var/run/containerd/containerd.sock
                    readOnly: true              # Read-only socket prevents runtime modification
```

**Enforcement strategy:**
- Agent validates container ID against allowed targets list
- Reject exec/shell requests to non-allowed containers
- Return error with allowed container IDs for debugging

#### Privilege Escalation Prevention

**No root access by default:**

Exec processes inherit container's UID/GID:
1. Query container's primary process UID/GID
2. Get container spec for user configuration
3. Create exec process with same UID/GID
4. No additional capabilities granted
5. Prevent privilege escalation to root

**Override protection via RBAC:**

```yaml
# RBAC config
rbac:
    users:
        -   name: alice@company.com
            role: developer
            permissions:
                -   environments: [ dev, staging ]
                    commands: [ shell, exec ]
                    allow_root: false       # Cannot run as root

        -   name: bob@company.com
            role: sre
            permissions:
                -   environments: [ production ]
                    commands: [ shell, exec ]
                    allow_root: true        # Can run privileged commands
                    require_approval: true  # But needs approval
```

#### RBAC Enforcement

**Permission checks before execution:**

1. **Authenticate user**: Validate token and extract user identity
2. **Resolve target**: Find agent matching target spec
3. **Check RBAC permissions**: Query policy for (user, environment, command)
4. **Approval workflow** (if required):
   - Request approval from designated approver
   - Wait for approval with timeout
   - Reject if denied or timeout
5. **Forward to agent**: Route command to agent with user context

### 6. Audit and Logging

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
    Stdout      string // Captured output
    Stderr      string
    Approved    bool
    ApproverID  *string
}
```

**Storage strategy:**
- Store locally in agent's DuckDB (immediate persistence)
- Send copy to Colony (centralized audit aggregation)
- Apply redaction rules before storage
- Compress large outputs to save space

#### Shell Session Transcript

**Fields captured:**

```go
type ShellAuditLog struct {
    SessionID   string
    UserID      string
    AgentID     string
    ContainerID string
    StartedAt   time.Time
    FinishedAt  time.Time
    Duration    time.Duration
    Transcript  []TranscriptEntry // Full I/O recording
    ExitCode    *int
    Approved    bool
    ApproverID  *string
}

type TranscriptEntry struct {
    Timestamp time.Time
    Direction string // "input" or "output"
    Data      []byte
}
```

**Recording strategy:**
- Record all I/O (input/output) with timestamps
- Append to transcript buffer as data flows
- Compress transcript on session end
- Store in DuckDB with retention policy

**Replay capability:**

```bash
# Replay session for audit review
coral session replay AR-12345

# Output (plays back in real-time):
[2024-03-15 10:23:45] alice@company.com started session on prod-api-01
[2024-03-15 10:23:47] $ curl http://localhost/admin
[2024-03-15 10:23:48] {"users": [...]}
[2024-03-15 10:23:52] $ vi /etc/config.yaml
...
[2024-03-15 10:25:10] Session ended (exit code: 0)
```

#### Storage and Retention

```yaml
agent:
    audit:
        enabled: true
        store_exec: true          # Log exec commands
        store_shell_transcript: true  # Record shell sessions
        retention_days: 90        # Keep for 90 days

        # Redaction rules
        redact_env_vars:
            - PASSWORD
            - API_KEY
            - SECRET

        # Selective recording
        record_production_only: false  # Record all environments
```

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
    duration     INTERVAL,
    approved     BOOLEAN,
    approver_id  VARCHAR
);

CREATE TABLE shell_audit
(
    session_id   VARCHAR PRIMARY KEY,
    user_id      VARCHAR   NOT NULL,
    agent_id     VARCHAR   NOT NULL,
    container_id VARCHAR,
    started_at   TIMESTAMP NOT NULL,
    finished_at  TIMESTAMP,
    duration     INTERVAL,
    transcript   BLOB,     -- Compressed transcript
    exit_code    INTEGER,
    approved     BOOLEAN,
    approver_id  VARCHAR
);
```

## API Changes

### Agent gRPC API

**Shell and Exec RPCs:**

```protobuf
service Agent {
    // Exec: One-off command execution
    rpc Exec(ExecRequest) returns (stream ExecOutput);

    // Shell: Interactive shell session
    rpc Shell(stream ShellIO) returns (stream ShellIO);

    // Session management
    rpc ResizeTerminal(ResizeRequest) returns (Empty);
    rpc SendSignal(SignalRequest) returns (Empty);
    rpc GetSession(GetSessionRequest) returns (Session);
    rpc ListSessions(ListSessionsRequest) returns (ListSessionsResponse);
    rpc KillSession(KillSessionRequest) returns (Empty);
}

message ExecRequest {
    string container_id = 1;      // Target container (or empty for native)
    repeated string command = 2;  // Command and args
    map<string, string> env = 3;  // Environment variables
    string working_dir = 4;       // Working directory
    bool tty = 5;                 // Allocate TTY
    int32 timeout_seconds = 6;    // Timeout (0 = no timeout)
    string user_id = 7;           // User making request (for audit)
    string approval_id = 8;       // Approval request ID (if required)
}

message ExecOutput {
    bytes stdout = 1;
    bytes stderr = 2;
    int32 exit_code = 3;  // Set on final message
    bool finished = 4;
}

message ShellIO {
    oneof payload {
        ShellStart start = 1;     // First message from client
        bytes input = 2;          // Stdin data from client
        bytes output = 3;         // Stdout/stderr data from agent
        ShellExit exit = 4;       // Final message from agent
    }
}

message ShellStart {
    string container_id = 1;
    string shell = 2;           // /bin/bash, /bin/sh, etc.
    map<string, string> env = 3;
    string working_dir = 4;
    TerminalSize size = 5;
    string user_id = 6;
    string approval_id = 7;
}

message ShellExit {
    int32 exit_code = 1;
    string session_id = 2;  // For audit reference
}

message TerminalSize {
    uint32 rows = 1;
    uint32 cols = 2;
}

message ResizeRequest {
    string session_id = 1;
    uint32 rows = 2;
    uint32 cols = 3;
}

message SignalRequest {
    string session_id = 1;
    string signal = 2;  // SIGINT, SIGTERM, etc.
}

message Session {
    string id = 1;
    string user_id = 2;
    string agent_id = 3;
    string container_id = 4;
    google.protobuf.Timestamp started_at = 5;
    google.protobuf.Timestamp last_active = 6;
    SessionStatus status = 7;
    int32 exit_code = 8;  // Only set if status == EXITED
}

enum SessionStatus {
    SESSION_ACTIVE = 0;
    SESSION_DETACHED = 1;
    SESSION_EXITED = 2;
}
```

## Configuration Schema

### Agent Configuration

```yaml
# /etc/coral/agent.yaml
agent:
    # Execution backends
    exec:
        default_timeout: 30s      # Default timeout for exec commands
        max_timeout: 600s         # Maximum allowed timeout
        buffer_size: 32768        # Output buffer size (bytes)

    shell:
        default_shell: /bin/bash  # Default shell
        default_image: alpine:latest  # Image for container shell
        idle_timeout: 30m         # Kill idle sessions
        max_duration: 4h          # Max session duration
        cleanup_interval: 5m      # Cleanup check interval

    # CRI integration
    kubernetes:
        cri_socket: auto          # Auto-detect or explicit path
        cri_runtime: auto         # containerd, crio, docker, auto
        sidecar_mode: auto        # auto, cri, shared-namespace, passive

    # Audit
    audit:
        enabled: true
        store_exec: true
        store_shell_transcript: true
        retention_days: 90
        redact_env_vars:
            - PASSWORD
            - API_KEY
            - TOKEN
```

## Implementation Plan

### Phase 1: Native Execution Backend

- [ ] Implement native executor for host-level exec
- [ ] Basic `coral exec` support (native mode)
- [ ] Basic `coral shell` with TTY allocation
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

### Phase 3: Session Management

- [ ] Session registry and lifecycle
- [ ] TTY allocation and management
- [ ] Terminal resize handling
- [ ] Signal forwarding implementation
- [ ] Idle timeout and cleanup
- [ ] Max duration enforcement
- [ ] Session listing and inspection

### Phase 4: K8s Sidecar Support

- [ ] Sidecar mode detection (CRI vs shared namespace)
- [ ] CRI-based exec for sidecar
- [ ] Fallback to shared namespace mode
- [ ] Container targeting validation
- [ ] Error messages for passive mode
- [ ] Integration tests with K8s pods

### Phase 5: Security and RBAC

- [ ] Container isolation enforcement
- [ ] UID/GID inheritance from container
- [ ] Privilege escalation prevention
- [ ] RBAC permission checks (Colony-side)
- [ ] Approval workflow integration
- [ ] Security integration tests

### Phase 6: Audit and Logging

- [ ] Exec audit log schema (DuckDB)
- [ ] Shell session transcript recording
- [ ] Audit log storage and retention
- [ ] Environment variable redaction
- [ ] Session replay functionality
- [ ] Colony audit aggregation

### Phase 7: CLI Commands

- [ ] `coral exec` command implementation
- [ ] `coral shell` command implementation
- [ ] `coral session list` command
- [ ] `coral session replay <id>` command
- [ ] `coral session kill <id>` command
- [ ] Rich terminal output and progress

## Testing Strategy

### Unit Tests

**Execution backends:**

- Native executor (fork/exec)
- CRI client (mocked containerd API)
- Session lifecycle (create, attach, cleanup)
- Signal forwarding
- Terminal resizing

**Security:**

- Container targeting validation
- UID/GID inheritance
- Privilege escalation prevention

### Integration Tests

**CRI integration:**

- Exec into containerd container
- Shell with CRI-O
- Docker API compatibility

**K8s sidecar:**

- CRI socket mode
- Shared namespace mode
- Passive mode error handling

**Session management:**

- Concurrent sessions
- Idle timeout enforcement
- Cleanup after disconnect

### E2E Tests

**Deployment scenarios:**

1. **Native agent**: `coral shell` on host
2. **Container agent**: `coral exec` in sibling container
3. **K8s sidecar (CRI)**: `coral shell` into app container
4. **K8s sidecar (shared NS)**: `coral exec` with shared PID namespace
5. **K8s DaemonSet**: `coral shell --pod=xyz` remote pod access

**Security scenarios:**

- RBAC denial (unprivileged user → production)
- Approval workflow (SRE approval required)
- Container escape attempt (blocked)
- Audit log verification

## Security Considerations

### Container Escape Prevention

**Risk**: Shell/exec could enable container escape if improperly implemented.

**Mitigations:**

- Inherit container's UID/GID (no root by default)
- No additional capabilities granted
- CRI socket mounted read-only
- Explicit container targeting (no wildcard)
- Audit all exec/shell operations

### Sensitive Data Exposure

**Risk**: Exec output or shell transcripts contain passwords, API keys.

**Mitigations:**

- Environment variable redaction (`PASSWORD`, `API_KEY`, etc.)
- Configurable redaction patterns
- Secure storage for audit logs (DuckDB encryption)
- RBAC for audit log access

### Session Hijacking

**Risk**: Attacker could attach to existing session.

**Mitigations:**

- Session IDs are UUIDs (unguessable)
- Authentication required for all operations
- Sessions tied to user identity
- No session sharing between users

### Audit Log Tampering

**Risk**: User could delete audit logs to hide actions.

**Mitigations:**

- Agent-side logs write-protected (filesystem permissions)
- Colony receives copy (centralized audit)
- Immutable append-only log structure
- RBAC for audit log deletion

## Future Enhancements

### Session Multiplexing (tmux/screen-like)

Detach and reattach to shell sessions:

```bash
coral shell --detach
# Session runs in background

coral session list
# ID       USER    STARTED     STATUS
# s-12345  alice   10:00 AM    active (detached)

coral session attach s-12345
# Reattach to running session
```

### Collaborative Sessions (Pair Debugging)

Multiple users share same shell session:

```bash
# User 1 starts session
coral shell --share

# User 2 joins
coral session join s-12345
# Both see same terminal, can type
```

### AI-Assisted Exec

AI suggests commands based on intent:

```bash
coral exec --ai "check disk usage"
# AI: Running: df -h
# Output: ...
```

### Session Recording Export

Export session transcripts for training:

```bash
coral session export s-12345 --format=asciinema
# Outputs .cast file for asciinema player
```

## Appendix

### Example: CRI Socket Detection

```bash
# Manually detect CRI socket
ls -l /var/run/containerd/containerd.sock
# srw-rw---- 1 root root 0 Mar 15 10:00 containerd.sock

# Agent logs CRI detection
coral agent start
# INFO  Detected CRI runtime: containerd (socket: /var/run/containerd/containerd.sock)
# INFO  Sidecar mode: CRI (explicit targeting)
# INFO  Monitoring containers: [app, nginx]
```

### Example: Exec Audit Query

```sql
-- Find all exec commands by user in production
SELECT
    timestamp, command, exit_code, approved, approver_id
FROM exec_audit
WHERE user_id = 'alice@company.com'
  AND agent_id LIKE 'prod-%'
ORDER BY timestamp DESC
    LIMIT 10;
```

### Example: Shell Session Replay

```bash
# List recorded sessions
coral session list --user=alice

# Replay session with timing
coral session replay s-12345
# [2024-03-15 10:23:45] alice@company.com started session on prod-api-01
# [2024-03-15 10:23:47] $ ls -la
# total 48
# drwxr-xr-x  8 app  app   256 Mar 15 10:00 .
# ...

# Export for offline review
coral session export s-12345 --format=txt > session.log
```

---

## Notes

**Why Separate RFD from RFD 016:**

RFD 016 defines the **high-level UX architecture** (command structure, runtime
contexts, agent modes). RFD 017 provides the **implementation depth** (CRI APIs,
session management, security boundaries) needed for actual development.

**Relationship to Other RFDs:**

- **RFD 016**: Parent RFD defining command structure and runtime contexts
- **RFD 011**: Multi-service agents (exec/shell operates on services)
- **RFD 012**: K8s node agent (DaemonSet mode exec/shell implementation)
- **RFD 013**: eBPF introspection (exec/shell operations are monitored)

**Implementation Priority:**

Phase 1-3 (native + CRI + session mgmt) are **MVP** for sidecar mode. Phase
4-6 (K8s + RBAC + audit) required for **production readiness**.
