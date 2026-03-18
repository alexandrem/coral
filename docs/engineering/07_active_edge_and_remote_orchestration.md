# Remote Execution & Container Orchestration

Coral provides first-class support for interactive debugging and remote
execution, allowing operators to "jump into" running services without SSH, VPNs,
or manual kubectl context switching.

## 1. Interaction Models

Coral distinguishes between two primary modes of remote interaction:

### A. Host Shell (`coral shell`)

- **Target**: The Agent process environment (Host OS or Agent Container).
- **Use Case**: Inspecting node-level resources, checking agent logs, or running
  diagnostic tools available on the host.
- **Mechanism**: Establishes an encrypted stream to the Agent's shell handler.

### B. Container Exec (`coral exec`)

- **Target**: A specific service container's namespace.
- **Use Case**: Reading container-local configs (`/etc/myapp/config.yaml`),
  inspecting mounted volumes, or checking process-local state.
- **Mechanism**: Uses `nsenter` to switch into the target container's
  namespaces (mount, PID, network, etc.) before executing the command.

## 2. Container Namespace Entry (nsenter)

The `coral exec` command leverages the Linux `nsenter` utility to bypass
container boundaries. This is particularly powerful for debugging "distroless"
or minimal containers that lack a shell.

### PID Detection Heuristic

Since Coral agents often run as sidecars or node-level collectors, they must
identify the target container's PID from the host perspective:

1. **Shared PID Namespace**: In sidecar mode (e.g., K8s
   `shareProcessNamespace: true` or Docker Compose shared PIDs), the agent scans
   `/proc` for child processes.
2. **Lowest PID Heuristic**: In sidecars, the application container usually
   starts before the agent. The agent identifies the lowest available PID (
   excluding itself and PID 1) as the likely entry point for the target service.
3. **Explicit Targeting**: Users can specify `--container <name>` to
   disambiguate in multi-container environments.

### Supported Namespaces

By default, Coral enters the **mount (`mnt`)** namespace to provide access to
the container's filesystem. Users can optionally enter others:

- `pid`: See processes from the container's perspective.
- `net`: Inspect container-local networking (interfaces, sockets).
- `ipc/uts/cgroup`: For advanced low-level debugging.

## 3. Security & Capabilities

Remote execution is a high-privilege operation. Coral performs automatic
**capability detection** to determine available execution modes:

### Linux Capabilities

For `nsenter`-based execution, the Coral Agent requires:

- `CAP_SYS_ADMIN`: To perform `setns` system calls (switching namespaces).
- `CAP_SYS_PTRACE`: To resolve PIDs and access `/proc` files in target
  namespaces.
- `CAP_NET_ADMIN` (Optional): Required if manually configuring network
  interfaces via shell.

### Execution Modes

The agent selects the best mode based on its environment:

1. **`EXEC_MODE_NSENTER`**: Used when the agent has the necessary caps and
   shares a PID namespace with the application (e.g., sidecar with
   `shareProcessNamespace: true` or Host PID mode). This is the preferred mode
   as it bypasses container engines.
2. **`EXEC_MODE_CRI`**: A fallback mode (upcoming) that uses the Container
   Runtime Interface (CRI) socket (`docker`, `containerd`) to execute commands.
3. **`EXEC_MODE_NONE`**: If neither capability nor socket access is available,
   remote execution is disabled.

## 4. Session Auditing & Reliability

### Bidirectional Streaming

Interactive sessions (`coral shell`) utilize **HTTP/2 bidirectional streaming
** (via Buf Connect) to forward `stdin`/`stdout`/`stderr` in real-time. This
ensures that Ctrl+C signals and terminal resizes (SIGWINCH) are propagated
instantly to the remote process.

### Audit Trails

All interactive sessions (shell and exec) are assigned a unique global **Session
ID**. The agent logs:

- The executing user ID (resolved from CLI environment).
- The exact command and arguments.
- The target PID and namespaces entered.
- The duration and exit status.

This ensures that "hands-on" debugging remains observable and auditable, even in
production environments.

---

## Future Engineering Notes

- **Asciinema-Style Recording**: Move beyond raw text logs to capture terminal
  timing data, allowing for full visual playback of debugging sessions for
  training and post-mortem analysis.
- **Ephemeral Debug Sidecars**: Implement the ability to spin up a dedicated
  "toolbox" container on-the-fly (see following section), attached to the
  namespaces of a minimal production container. This provides a full suite of
  debugging tools (gdb, strace, lsof) without bloating production images.
- **Input-Stream Auditing**: Stream keystrokes to the Colony registry in
  real-time (write-ahead) rather than logging results post-execution. This
  prevents an attacker from "cleaning up" their audit trail if they manage to
  kill the shell process manually.
- **CRI Socket Proxying**: For `EXEC_MODE_CRI`, implement a secure,
  authenticated proxy for the Docker/Containerd socket rather than requiring the
  agent to have direct host-level socket access.
- **Interactive Policy Layer**: Add the ability to restrict specific commands (
  e.g., `rm`, `mkfs`, `kill`) or network egress during an interactive session
  based on the operator's role.

### Tooling Portability & Namespace Hybridity

A common challenge in modern "distroless" or minimal production images is the
total absence of debugging binaries (no `ls`, `ps`, or even `sh`). Coral can
solve this through **Namespace Hybridity**:

1. **Selective Entry**: Instead of entering all namespaces, Coral can enter only
   the `net` and `pid` namespaces of the target while retaining the \*
   \*Mount (`mnt`)\*\* namespace of the Agent.

- **Binary Mapping**: This allows the operator to execute an Agent-local
  binary (like `tcpdump`, `lsof`, or a custom DuckDB build) _against_ the
  target container's network stack and process tree.

3. **Execution Context**:
    - The **Binary** comes from the Agent/Host.
    - The **Context** (IPs, Ports, PIDs) comes from the Target.

By bundling a curated set of diagnostic tools (e.g., `net-tools`, `sysstat`,
`strace`, `duckdb-cli`) directly into the Coral Agent image, the agent becomes a
portable, remote-accessible debugging station. This eliminates the need to:

- Mutate production nodes by installing packages.
- Bloat specialized application images with debugging utilities.
- Manage consistent tool versions across a heterogeneous fleet.

Every node in the Coral mesh is effectively "pre-equipped" for deep
troubleshooting, regardless of the underlying OS or container distribution.

This model ensures that debugging capabilities are independent of how the
target application was packaged.
