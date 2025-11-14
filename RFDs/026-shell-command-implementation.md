---
rfd: "026"
title: "Shell Command Implementation"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "016" ]
related_rfds: [ "017", "011", "012" ]
database_migrations: [ ]
areas: [ "cli", "agent", "execution", "security" ]
---

# RFD 026 - Shell Command Implementation

**Status:** 🚧 Draft

## Summary

Define the implementation for `coral shell` command, which provides an
interactive
debugging shell within the agent's own environment. The agent container is
bundled
with troubleshooting utilities (tcpdump, netcat, curl, etc.) to enable
infrastructure
debugging from the agent's perspective.

**Key concepts:**

- **Agent-scoped shell**: Runs in agent's environment, not application container
- **Bundled toolbox**: Alpine-based image with debugging utilities
- **Simple implementation**: No CRI complexity, just shell in agent process
- **Elevated privileges**: Agent may have access to CRI socket, host network,
  etc.
- **Heavy audit**: Full session recording due to elevated access

## Problem

**Current limitations:**

Developers and SREs need to debug infrastructure issues from the agent's vantage
point, but have no direct access to agent environments:

1. **Network debugging**: Can't test connectivity from agent's perspective
    - Is database reachable from agent?
    - What DNS resolution does agent see?
    - Can agent reach other services?

2. **eBPF data inspection**: Can't query agent's monitoring data
    - What network flows has eBPF captured?
    - What processes is agent tracking?
    - What metrics are stored locally?

3. **System troubleshooting**: Can't inspect agent's environment
    - What processes can agent see?
    - What files are mounted?
    - What's the agent's network configuration?

4. **Distroless app containers**: `coral exec` fails when app has no shell
    - Need alternative debugging approach
    - Can't inspect from inside app

**Why this matters:**

- **Infrastructure debugging**: Network, DNS, connectivity issues require
  agent's
  network perspective
- **eBPF troubleshooting**: Query agent's DuckDB to verify data collection
- **Workaround for distroless**: When app has no shell, need debugging
  environment
- **Agent development**: Test agent behavior in real environments

## Solution

Implement `coral shell` command that opens an interactive shell within the
agent's
own container/process, bundled with common debugging utilities.

### 1. Command Semantics

**Simple shell invocation:**

```bash
# Open shell in agent monitoring myapp
coral shell myapp

# Open shell in specific agent (by agent ID)
coral shell agent-abc123

# Open shell with default/inferred target
coral shell
```

**What you get:**

```bash
$ coral shell myapp
⚠️  Warning: Entering agent debug shell with elevated privileges.
Session will be fully recorded. Continue? [y/N] y

agent $ # Shell prompt in agent container

agent $ whoami
coral

agent $ which tcpdump netcat curl
/usr/sbin/tcpdump
/usr/bin/netcat
/usr/bin/curl

agent $ ps aux
PID   USER     COMMAND
1     coral    /app/coral-agent --connect=container://app
2     app      /app/myapp --serve

agent $ exit
Session ended. Audit ID: sh-abc123
```

### 2. Agent Environment

**Debian Bookworm-based container image** with debugging utilities bundled:

> **Note**: We use Debian Bookworm instead of Alpine due to CGO dependencies
> required by DuckDB. The go-duckdb driver requires a C compiler and glibc,
> which makes Debian a better fit than Alpine's musl libc.

#### Container Image

```dockerfile
FROM debian:bookworm-slim

# Install debugging utilities
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    curl \
    tcpdump \
    dnsutils \
    netcat-openbsd \
    iproute2 \
    procps \
    coreutils \
    vim-tiny \
    ca-certificates \
    wget \
    unzip \
    && rm -rf /var/lib/apt/lists/*

# Install DuckDB CLI
RUN wget -q https://github.com/duckdb/duckdb/releases/download/v1.1.3/duckdb_cli-linux-amd64.zip \
    && unzip duckdb_cli-linux-amd64.zip \
    && mv duckdb /usr/local/bin/duckdb \
    && chmod +x /usr/local/bin/duckdb \
    && rm duckdb_cli-linux-amd64.zip

# Install coral agent binary
COPY coral-agent /app/coral-agent

# Create non-root user
RUN groupadd -g 1000 coral && \
    useradd -m -u 1000 -g coral coral

USER coral
WORKDIR /app

ENTRYPOINT ["/app/coral-agent"]
```

**Bundled utilities:**

| Tool           | Purpose                      | Example Usage                     |
|----------------|------------------------------|-----------------------------------|
| `tcpdump`      | Network packet capture       | `tcpdump -i any port 5432`        |
| `netcat`       | Network connectivity testing | `netcat -zv postgres 5432`        |
| `curl`         | HTTP requests                | `curl http://api/health`          |
| `dig/nslookup` | DNS debugging                | `dig postgres.svc.cluster.local`  |
| `ps`           | Process inspection           | `ps aux` (shared PID namespace)   |
| `ip/ss`        | Network configuration        | `ip addr`, `ss -tulpn`            |
| `duckdb`       | Query agent's DuckDB         | `duckdb /var/lib/coral/agent.db`  |

#### Native Environment

For native (non-containerized) agents:

**Option 1: System shell** (simple)

- Use host's `/bin/bash` or `/bin/sh`
- Tools depend on host OS (may be limited)

**Option 2: Embedded shell** (future enhancement)

- Bundle lightweight shell in agent binary
- Portable across environments
- Consistent tooling

**Initial implementation uses Option 1** (system shell).

> **Implementation Note**: The existing Dockerfile already uses Debian Bookworm
> as the base image (`debian:bookworm-slim`). This RFD aligns with and extends
> that choice by adding debugging utilities to support the shell command.

### 3. Execution Architecture

**Simple direct execution:**

```
┌─────────────────────────────────────────────────────────┐
│ CLI (coral shell myapp)                                 │
└────────────────────┬────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────┐
│ Colony RPC (routes to target agent)                     │
└────────────────────┬────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────┐
│ Agent Shell Handler                                     │
└────────────────────┬────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────┐
│ Fork shell process (/bin/bash or /bin/sh)               │
│ - Allocate PTY                                          │
│ - Set environment (CORAL_APP, etc.)                     │
│ - Stream I/O to/from client                             │
│ - Record transcript for audit                           │
└─────────────────────────────────────────────────────────┘
```

**No CRI integration needed** - shell runs as child process of agent.

### 4. Session Management

**Interactive session lifecycle:**

```
1. Create → 2. Attach → 3. Active → 4. Exit → 5. Cleanup
   ↓           ↓           ↓          ↓          ↓
 Allocate   Connect    Forward    Signal      Release
   PTY        I/O      signals    handler    resources
              Start     Record              Finalize
              shell   transcript             audit
```

**Session state:**

```go
type ShellSession struct {
ID          string
UserID      string
AgentID     string
StartedAt   time.Time
LastActive  time.Time
Status      SessionStatus
ExitCode    *int
Transcript  *TranscriptRecorder
}

type SessionStatus int

const (
SessionActive SessionStatus = iota
SessionExited
)
```

#### TTY and Terminal Resizing

**Terminal size synchronization:**

1. **Client side**: Listen for SIGWINCH (window change) signal
2. **On resize**: Query current terminal size (rows, cols)
3. **Send to agent**: RPC call with new dimensions
4. **Agent updates**: Resize PTY via `ioctl(TIOCSWINSZ)`
5. **Shell updates**: Automatically adjusts to new size

#### Signal Forwarding

**Signal handling:**

1. **Client side**: Catch signals (SIGINT, SIGTERM, SIGTSTP)
2. **Send to agent**: RPC call with signal name
3. **Agent forwards**: Send signal to shell process
4. **Shell handles**: Process receives and responds normally

#### Session Cleanup

**On disconnect or exit:**

1. **Send SIGTERM** to shell process
2. **Wait 5s**, then SIGKILL if still running
3. **Close PTY** and I/O streams
4. **Finalize transcript** (compress and store)
5. **Write audit log** with session details

**Timeout configuration:**

```yaml
agent:
    shell:
        idle_timeout: 30m      # Kill after 30min of no activity
        max_duration: 4h       # Force kill after 4h total
        cleanup_interval: 5m   # Check for stale sessions every 5min
```

### 5. Environment Setup

**Shell environment variables:**

```bash
# Standard shell environment
PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin
HOME=/home/coral
USER=coral
SHELL=/bin/bash

# Coral-specific context
CORAL_AGENT_ID=agent-abc123
CORAL_APP=myapp              # Target application
CORAL_APP_ID=service-xyz789  # Application service ID
CORAL_DATA=/var/lib/coral    # Agent data directory

# Agent configuration access
CORAL_CONFIG=/etc/coral/agent.yaml
```

**Working directory:** `/app` (agent's working directory)

**Files accessible:**

- `/var/lib/coral/agent.db` - Agent's DuckDB storage
- `/etc/coral/agent.yaml` - Agent configuration
- `/app/coral-agent` - Agent binary
- Container filesystem (if containerized)
- Host filesystem (if host namespaces mounted)

### 6. Security Boundaries

**Critical: Shell has agent's privileges, which may be elevated.**

#### Agent Privilege Context

**K8s Sidecar mode:**

```yaml
spec:
    containers:
        -   name: coral-agent
            securityContext:
                runAsUser: 1000
                runAsNonRoot: true
                readOnlyRootFilesystem: true
                allowPrivilegeEscalation: false
                capabilities:
                    drop: [ ALL ]
                    add: [ BPF, NET_RAW ]  # For eBPF
            volumeMounts:
                -   name: cri-sock
                    mountPath: /var/run/containerd/containerd.sock
                    readOnly: true  # Still provides exec capability!
```

**Risks:**

- CRI socket access enables exec into ANY container on node
- BPF capability allows network monitoring
- Shared PID namespace may allow process inspection
- WireGuard mesh access to other agents

#### RBAC Requirements

**Strict access control:**

```yaml
rbac:
    users:
        -   name: developer@company.com
            role: developer
            permissions:
                -   environments: [ dev, staging ]
                    commands: [ exec ]  # Can use exec, NOT shell

        -   name: sre@company.com
            role: sre
            permissions:
                -   environments: [ dev, staging ]
                    commands: [ exec, shell ]  # Can use shell in non-prod
                -   environments: [ production ]
                    commands: [ shell ]
                    require_approval: true      # Production requires approval
                    require_mfa: true           # And MFA
```

#### Session Recording

**Full transcript recording** for all shell sessions:

```go
type ShellAuditLog struct {
SessionID   string
UserID      string
AgentID     string
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

- Record every byte of input/output
- Timestamp each entry
- Compress on session end
- Store in DuckDB locally + send to Colony
- Retention: 90 days default

#### Warning on Entry

**Clear warning before entry:**

```bash
$ coral shell myapp
⚠️  WARNING: Entering agent debug shell with elevated privileges.

This shell runs in the agent's container with access to:
  • CRI socket (can exec into containers)
  • eBPF monitoring data
  • WireGuard mesh network
  • Agent configuration and storage

This session will be fully recorded (input and output).
Audit ID: sh-abc123

Continue? [y/N]
```

### 7. Audit and Compliance

**DuckDB schema:**

```sql
CREATE TABLE shell_audit
(
    session_id  VARCHAR PRIMARY KEY,
    user_id     VARCHAR   NOT NULL,
    agent_id    VARCHAR   NOT NULL,
    started_at  TIMESTAMP NOT NULL,
    finished_at TIMESTAMP,
    duration INTERVAL,
    transcript  BLOB, -- Compressed transcript
    exit_code   INTEGER,
    approved    BOOLEAN,
    approver_id VARCHAR
);
```

**Audit queries:**

```sql
-- Find all shell sessions in production
SELECT session_id, user_id, started_at, duration
FROM shell_audit
WHERE agent_id LIKE 'prod-%'
ORDER BY started_at DESC;

-- Find sessions by specific user
SELECT session_id, agent_id, started_at
FROM shell_audit
WHERE user_id = 'sre@company.com'
ORDER BY started_at DESC;
```

**Replay capability** (future enhancement):

```bash
coral shell replay sh-abc123
# Plays back transcript with timing
```

## API Changes

### Agent gRPC API

```protobuf
service Agent {
    // Shell: Interactive shell session in agent environment
    rpc Shell(stream ShellRequest) returns (stream ShellResponse);

    // Shell session management
    rpc ResizeShellTerminal(ResizeShellTerminalRequest) returns (google.protobuf.Empty);
    rpc SendShellSignal(SendShellSignalRequest) returns (google.protobuf.Empty);
    rpc KillShellSession(KillShellSessionRequest) returns (google.protobuf.Empty);
}

message ShellRequest {
    oneof payload {
        ShellStart start = 1;     // First message from client
        bytes stdin = 2;          // Stdin data from client
        ShellResize resize = 3;   // Terminal resize event
        ShellSignal signal = 4;   // Signal to send to shell
    }
}

message ShellStart {
    string shell = 1;             // /bin/bash, /bin/sh (default: /bin/bash)
    map<string, string> env = 2;  // Additional environment variables
    TerminalSize size = 3;        // Initial terminal size
    string user_id = 4;           // User making request (for audit)
    string approval_id = 5;       // Approval request ID (if required)
}

message ShellResponse {
    oneof payload {
        bytes output = 1;         // Stdout/stderr data from shell
        ShellExit exit = 2;       // Final message with exit code
    }
}

message ShellExit {
    int32 exit_code = 1;
    string session_id = 2;  // Session ID for audit reference
}

message TerminalSize {
    uint32 rows = 1;
    uint32 cols = 2;
}

message ShellResize {
    uint32 rows = 1;
    uint32 cols = 2;
}

message ShellSignal {
    string signal = 1;  // SIGINT, SIGTERM, SIGTSTP, etc.
}

message ResizeShellTerminalRequest {
    string session_id = 1;
    uint32 rows = 2;
    uint32 cols = 3;
}

message SendShellSignalRequest {
    string session_id = 1;
    string signal = 2;
}

message KillShellSessionRequest {
    string session_id = 1;
}
```

### CLI Commands

```bash
# Open shell in agent monitoring target
coral shell <target>

# Open shell with default/inferred target
coral shell

# Examples
coral shell myapp           # Shell in agent monitoring myapp
coral shell agent-abc123    # Shell in specific agent (by ID)
coral shell                 # Shell in current context agent
```

## Implementation Plan

### Phase 1: Basic Shell Implementation

- [ ] Fork shell process (/bin/bash, /bin/sh)
- [ ] PTY allocation and management
- [ ] I/O streaming (stdin, stdout, stderr)
- [ ] Basic session lifecycle
- [ ] Exit code capture
- [ ] Unit tests

### Phase 2: Terminal Management

- [ ] Terminal resize handling (SIGWINCH)
- [ ] Signal forwarding (SIGINT, SIGTERM, SIGTSTP)
- [ ] Raw terminal mode setup
- [ ] Terminal restoration on exit
- [ ] Integration tests

### Phase 3: CLI Implementation

- [ ] `coral shell` command
- [ ] Target resolution (app name → agent ID)
- [ ] Interactive terminal setup
- [ ] Warning message and confirmation
- [ ] Error handling and help text

### Phase 4: Security and Audit

- [ ] Session transcript recording
- [ ] DuckDB audit schema
- [ ] Transcript compression and storage
- [ ] RBAC enforcement (Colony-side)
- [ ] Approval workflow integration
- [ ] Security integration tests

### Phase 5: Agent Container Image

- [ ] Alpine-based Dockerfile
- [ ] Bundle debugging utilities
- [ ] Non-root user setup
- [ ] Multi-arch builds (amd64, arm64)
- [ ] Image publishing

### Phase 6: Session Management

- [ ] Idle timeout enforcement
- [ ] Max duration enforcement
- [ ] Background cleanup task
- [ ] Session listing (future)
- [ ] Session killing (future)

## Testing Strategy

### Unit Tests

- PTY allocation and management
- Shell process lifecycle
- Signal forwarding
- Terminal resizing
- Session cleanup
- Transcript recording

### Integration Tests

- End-to-end shell session
- Interactive terminal features (colors, cursor movement)
- Long-running sessions
- Idle timeout enforcement
- Max duration enforcement
- Transcript playback

### E2E Tests

**Deployment scenarios:**

1. **Containerized agent**: Shell in Debian Bookworm container
2. **Native agent**: Shell in host environment
3. **K8s sidecar**: Shell with CRI socket access

**Session scenarios:**

1. Interactive commands (ls, cat, vi)
2. Network debugging (tcpdump, netcat, curl)
3. Process inspection (ps, top)
4. DuckDB queries (duckdb CLI)
5. Long-running session (> 1 hour)

**Security scenarios:**

- RBAC denial (developer trying to shell into prod)
- Approval workflow (SRE approval for prod)
- Transcript verification
- Audit log completeness

## Security Considerations

### Elevated Privilege Risk

**Risk**: Shell has agent's privileges, which may include:

- CRI socket access (can exec into any container)
- Host network access
- WireGuard mesh access
- eBPF capabilities

**Mitigations:**

- Strict RBAC (limited to SRE role)
- Approval workflow for production
- Full session recording
- Clear warning on entry
- Run agent with minimal necessary privileges

### CRI Socket Access

**Risk**: Read-only CRI socket still allows exec into containers.

**Mitigations:**

- Explicitly document this risk
- Consider separate agent modes (monitoring-only vs debug-enabled)
- RBAC to limit who can use shell
- Audit all CRI operations (separate from shell audit)

### Data Exfiltration

**Risk**: Shell can access agent's DuckDB (sensitive monitoring data).

**Mitigations:**

- Redaction in audit transcripts (passwords, keys)
- Network egress policies (limit outbound access)
- Alerting on suspicious commands (curl to external IPs)

### Session Hijacking

**Risk**: Attacker could attach to existing session.

**Mitigations:**

- Session IDs are UUIDs (unguessable)
- Sessions tied to user identity
- No session sharing between users
- Authentication required for all operations

## Future Enhancements

### App Context Filtering (Separate RFD)

Pre-filter tools and data to target application:

```bash
$ coral shell myapp
myapp@agent $ coral-ps     # Show only myapp processes
myapp@agent $ coral-logs   # Tail myapp logs from eBPF
myapp@agent $ coral-query  # Query DuckDB filtered to myapp
```

### Session Multiplexing

Detach and reattach to shell sessions:

```bash
coral shell myapp --detach
# Session runs in background

coral shell list
# ID       USER    STARTED     STATUS
# s-12345  alice   10:00 AM    active (detached)

coral shell attach s-12345
# Reattach to running session
```

### Embedded Shell for Native Mode

Bundle lightweight shell in agent binary for consistent experience:

```go
// Embed busybox-like shell in binary
//go:embed shell
var embeddedShell []byte
```

### Session Recording Export

```bash
coral shell replay sh-abc123
# Replay session with timing

coral shell export sh-abc123 --format=asciinema
# Export as .cast file
```

---

## Notes

**Relationship to Other RFDs:**

- **RFD 016**: Parent RFD defining command structure
- **RFD 017**: `coral exec` implementation (app container access)
- **RFD 011**: Multi-service agents
- **RFD 012**: K8s node agent

**Why Separate from RFD 017:**

`coral exec` and `coral shell` serve fundamentally different purposes:

- **exec (RFD 017)**: Access application container (kubectl/docker semantics,
  app privileges)
- **shell (RFD 026)**: Agent debug environment (coral-specific, agent
  privileges)

Different purposes, different security models, different implementations -
warrant
separate RFDs for clarity.
