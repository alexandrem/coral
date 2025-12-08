# Privilege Model & Architecture

This document explains Coral's privilege requirements, architecture decisions,
and security model in detail.

## Overview

Coral components require elevated privileges throughout their lifetime for
continuous network management and observability operations. Unlike traditional
daemons that drop privileges after initialization, Coral maintains elevated
access due to its operational requirements.

## Why Elevated Privileges Throughout Lifetime

### Colony: Dynamic Network Management

**Required throughout:**

- **Route management** - As agents connect, colony dynamically adds routes for
  their AllowedIPs
- **Network configuration** - Ongoing mesh topology changes require network
  operations

**Capability needed:**

- `CAP_NET_ADMIN` - TUN device creation, route management, network configuration

**Why no privilege dropping:**

```go
// When agent connects:
colony.AddPeer(agent)
→ wireguard.AddRoutesForPeer(allowedIPs)
→ exec("route add -host 100.64.0.2 -interface utun11") // Requires CAP_NET_ADMIN
```

The `route` command on macOS and Linux requires network administration
privileges (CAP_NET_ADMIN on Linux, root on macOS). Since agents can connect
at any time, colony must maintain these privileges throughout its lifetime.

### Agent: Continuous eBPF Operations

**Required throughout:**

- **eBPF program management** - Continuous telemetry collection via eBPF
- **Process tracing** - Ongoing attachment to application processes
- **Memory operations** - eBPF map management and memory locking

**Capabilities needed:**

- `CAP_NET_ADMIN` - TUN device, network configuration
- `CAP_SYS_ADMIN` - eBPF program loading and management
- `CAP_SYS_PTRACE` - Process attachment for tracing
- `CAP_SYS_RESOURCE` - Memory locking for eBPF maps
- `CAP_BPF` - eBPF operations (Linux 5.8+, fallback to CAP_SYS_ADMIN)
- `CAP_PERFMON` - Performance monitoring (optional)

**Why no privilege dropping:**
Beyla continuously collects telemetry from application processes, requiring
persistent eBPF capabilities.

## Platform Differences

### Linux: Capability-Based Security

Linux capabilities allow fine-grained privilege control without full root:

```bash
# Colony - minimal networking capabilities
sudo setcap 'cap_net_admin+ep' /path/to/coral

# Agent - networking + eBPF capabilities
sudo setcap 'cap_net_admin,cap_sys_admin,cap_sys_ptrace,cap_sys_resource,cap_bpf+ep' /path/to/coral
```

**Advantages:**

- Least privilege principle
- No password prompts after setup
- Process doesn't run as UID 0

**Detection:**
Agent automatically detects available capabilities and reports which features
are available:

```
✓ CAP_NET_ADMIN: TUN device, network config
✓ CAP_SYS_ADMIN: eBPF program loading
✗ CAP_BPF: eBPF operations (Linux 5.8+) - falls back to CAP_SYS_ADMIN
```

### macOS: All-or-Nothing Root

macOS lacks a capability system, requiring full root privileges:

```bash
sudo coral colony start
sudo coral agent start
```

**Trade-offs:**

- Must use `sudo` (password prompt on each start)
- Full root privileges (no fine-grained control)
- File ownership preserved via `$SUDO_USER` detection

## Graceful Degradation

### Agent Preflight Checks

The agent performs capability detection and allows operation with reduced
functionality:

**Full capabilities:**

```
✓ All required capabilities available
```

**Partial capabilities:**

```
⚠️  Missing CAP_SYS_PTRACE - Process tracing unavailable
⚠️  Starting in degraded mode with available capabilities
```

**Restricted environments:**

- Container without eBPF → Can still do mesh networking
- No CAP_SYS_PTRACE → Can't trace processes but can monitor network
- No root at all → Warnings issued, some features unavailable

This allows deployment in restricted environments (containers, restricted hosts)
where full capabilities aren't available.

## Helper Subprocess Architecture

### Purpose

The helper subprocess (`_tun-helper`) enables privileged TUN device creation
from non-root processes:

```
Non-root process → spawn _tun-helper (with sudo) → create TUN → pass FD back
```

### How It Works

1. **Non-root process** needs TUN device
2. **Spawns helper** with `sudo` (prompts for password once)
3. **Helper creates TUN** device as root
4. **Passes file descriptor** to parent via Unix socket (SCM_RIGHTS)
5. **Helper exits** after FD transfer
6. **Parent uses TUN** device without root

### Current Usage

**Colony & Agent:**

- Use direct TUN creation (already running as root)
- Helper serves as fallback if direct creation fails

**Future Scenarios:**

- CLI tools needing temporary mesh connectivity
- Embedded applications with occasional TUN requirements
- Docker containers with capability-based security
- Development tools requiring mesh access

### Implementation Details

**File descriptor passing:**

```go
// Helper subprocess (runs as root via sudo)
tunDevice, _ := tun.CreateTUN("utun", mtu)
fd := tunDevice.File().Fd()
SendFDOverSocket(fd, socketPath)  // SCM_RIGHTS

// Parent process (non-root)
fd := receiveFDFromSocket(listener)
tunDevice := CreateTUNFromFD(fd, mtu) // Now owns the FD
```

**FD lifecycle:**

- Helper uses `unix.Dup()` to duplicate FD before sending
- Parent receives duplicated FD (independent of helper)
- Helper can exit after transfer
- Parent maintains FD ownership

## Security Considerations

### Running as Root

**Risks:**

- Process compromise → full system access
- Vulnerabilities can be exploited for privilege escalation

**Mitigations:**

- Linux: Use capabilities instead of full root
- Minimize attack surface (colony/agent are infrastructure components)
- File ownership preservation via `$SUDO_USER`
- Security-focused code review

### Linux Capabilities

**Best practice:**

```bash
# Install binary
sudo cp coral /usr/local/bin/

# Set capabilities (one-time)
sudo setcap 'cap_net_admin,cap_sys_admin,cap_sys_ptrace,cap_sys_resource,cap_bpf+ep' /usr/local/bin/coral

# Run without sudo
coral agent start  # No password needed
```

**Verification:**

```bash
getcap /usr/local/bin/coral
# Shows: cap_bpf,cap_net_admin,cap_sys_admin,cap_sys_ptrace,cap_sys_resource+ep
```

### Setuid (Not Recommended)

Setuid makes the binary always run as root:

```bash
sudo chown root:root /path/to/coral
sudo chmod u+s /path/to/coral
```

**⚠️ Warning:**

- Any user can run with root privileges
- Vulnerability = system-wide compromise
- Only for single-user development environments

## Commands Without Privileges

### Proxy Command

The `coral proxy` command is an HTTP reverse proxy and requires no privileges:

```bash
coral proxy start my-app-prod  # No sudo needed
```

**Why:**

- No TUN device creation
- No route management
- Just HTTP forwarding over existing mesh
- Assumes mesh connectivity already exists (via agent or RFD 031 public
  endpoint)

## Summary

| Component | Privileges (Linux)     | Privileges (macOS) | Why Throughout             | Degradation |
|-----------|------------------------|--------------------|----------------------------|-------------|
| Colony    | `CAP_NET_ADMIN` only   | Root               | Dynamic route management   | No          |
| Agent     | Multiple capabilities* | Root               | Continuous eBPF operations | Yes         |
| Proxy     | None                   | None               | HTTP forwarding only       | N/A         |

\* Agent capabilities: `CAP_NET_ADMIN`, `CAP_SYS_ADMIN`, `CAP_SYS_PTRACE`, `CAP_SYS_RESOURCE`, `CAP_BPF`

**Key Principle:** Coral follows least-privilege design. Colony requires only
`CAP_NET_ADMIN` on Linux (most restrictive), while macOS requires root due to
lack of capability system. Infrastructure components maintain necessary
privileges for their core functionality.
