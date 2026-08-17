# Coral Agent VPS Deployment Guide

This guide describes deploying the **Coral Agent** (`coral-agent`) on a Linux
Virtual Private Server (VPS) or bare-metal host.

## Overview

The Coral Agent is a lightweight, high-performance observer daemon that runs
alongside your applications. It collects eBPF kernel probes, system metrics,
error log traces, and establishes a secure WireGuard mesh connection back to
your Coral Colony.

## System Prerequisites

- **Operating System**: Linux kernel 5.8+ recommended (Ubuntu 20.04+, Debian
  11+, RHEL 8+, Alpine 3.16+).
- **Architecture**: `x86_64` (`amd64`) or `aarch64` (`arm64`).
- **Privileges**: Root or `sudo` access to set up Linux capabilities and systemd
  units.
- **Required Capabilities**:
    - `CAP_NET_ADMIN`: WireGuard mesh interface creation (`coral-wg0`).
    - `CAP_SYS_ADMIN`, `CAP_SYS_PTRACE`, `CAP_SYS_RESOURCE`, `CAP_BPF`: eBPF
      tracepoints, uprobes, and system monitoring.

---

## Pre-Deployment: Information Needed from Colony

Before deploying an agent to a VPS, ensure you have the following information
from your Colony administrator:

1. **Colony ID** (`CORAL_COLONY_ID`): Unique identifier of the Colony (e.g.
   `prod-colony`).
2. **CA Fingerprint** (`CORAL_CA_FINGERPRINT`): The Root CA fingerprint (format:
   `sha256:...`), obtained via `coral colony ca status` on the colony server.
3. **Bootstrap PSK** (`CORAL_BOOTSTRAP_PSK`): Pre-shared key for initial mTLS
   bootstrap authentication.
4. **Discovery Service URL** (`CORAL_DISCOVERY_ENDPOINT`): Address of the
   Discovery service (e.g., `https://discovery.coral.io`).

---

## Deployment Methods

### Method 1: Systemd Service Installation (Recommended)

Systemd installation runs `coral-agent` directly on the VPS host with minimal
overhead and full eBPF kernel visibility.

#### Quick One-Liner Installation

Run the following command on your VPS:

```bash
curl -fsSL https://raw.githubusercontent.com/coral-mesh/coral/main/scripts/install-agent.sh | sudo bash -s -- \
  --colony <COLONY_ID> \
  --fingerprint <CA_FINGERPRINT> \
  --psk <BOOTSTRAP_PSK> \
  --discovery <DISCOVERY_URL>
```

#### Manual Installation Steps

1. **Download the Agent Binary**:
   ```bash
   ARCH=$(uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/')
   curl -fsSL "https://github.com/coral-mesh/coral/releases/latest/download/coral-agent-linux-${ARCH}" -o /usr/local/bin/coral-agent
   chmod 755 /usr/local/bin/coral-agent
   ```

2. **Configure Linux Capabilities**:
   ```bash
   sudo setcap 'cap_net_admin,cap_sys_admin,cap_sys_ptrace,cap_sys_resource,cap_bpf+ep' /usr/local/bin/coral-agent
   ```

3. **Create User and Directories**:
   ```bash
   sudo groupadd -r coral 2>/dev/null || true
   sudo useradd -r -g coral -d /var/lib/coral -s /sbin/nologin coral 2>/dev/null || true
   sudo mkdir -p /var/lib/coral /var/log/coral /etc/coral
   sudo chown -R coral:coral /var/lib/coral /var/log/coral /etc/coral
   ```

4. **Bootstrap mTLS Certificates**:
   ```bash
   sudo coral-agent bootstrap \
     --colony <COLONY_ID> \
     --fingerprint <CA_FINGERPRINT> \
     --psk <BOOTSTRAP_PSK> \
     --discovery <DISCOVERY_URL>
   ```

5. **Install and Start Systemd Unit**:
   Copy `deployments/systemd/coral-agent.service` to
   `/etc/systemd/system/coral-agent.service`:
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl enable --now coral-agent
   ```

---

### Method 2: Docker Container Deployment

If you prefer containerized workloads, `coral-agent` is available as a
multi-arch container image (`ghcr.io/coral-mesh/coral-agent:latest`).

#### Docker Compose Example (`docker-compose.yml`)

```yaml
version: '3.8'

services:
    coral-agent:
        image: ghcr.io/coral-mesh/coral-agent:latest
        container_name: coral-agent
        restart: always
        network_mode: host
        cap_add:
            - NET_ADMIN
            - SYS_ADMIN
            - SYS_PTRACE
            - SYS_RESOURCE
            - BPF
            - PERFMON
        security_opt:
            - seccomp:unconfined
        volumes:
            - /var/lib/coral:/var/lib/coral
            - /var/log/coral:/var/log/coral
            - /etc/coral:/etc/coral
            - /sys/fs/bpf:/sys/fs/bpf
            - /sys/kernel/debug:/sys/kernel/debug:ro
            - /lib/modules:/lib/modules:ro
        environment:
            - CORAL_COLONY_ID=<COLONY_ID>
            - CORAL_CA_FINGERPRINT=<CA_FINGERPRINT>
            - CORAL_BOOTSTRAP_PSK=<BOOTSTRAP_PSK>
            - CORAL_DISCOVERY_ENDPOINT=<DISCOVERY_URL>
```

Start the container:

```bash
docker compose up -d
```

---

## Discovering Docker Compose Applications

A host-installed, privileged Coral Agent (Method 1) automatically discovers
TCP listeners inside ordinary Docker Compose / Podman containers on the same
host, without `network_mode: host` on the application, a Beyla sidecar per
container, or Docker socket access (RFD 112). This requires only the process
visibility `coral-agent` already needs for eBPF/Beyla operation:

- A host PID namespace (containers must not run with a separate PID
  namespace hiding them from the host).
- Read access to `/proc/<pid>/ns/net`, `/proc/<pid>/net`, and `/proc/<pid>/fd`
  for the containerized processes. Running as root is sufficient; a
  `hidepid` mount policy on `/proc` can prevent this and the agent reports it
  as a capability warning rather than silently reporting successful
  discovery.

Notes:

- Compose service names are **not** automatically inferred. Coral names the
  discovered process using `OTEL_SERVICE_NAME`/`SERVICE_NAME` if set, or
  falls back to the executable name. A Docker metadata naming provider may be
  added separately.
- The reported listener is the **application's namespace-local port**, not
  the host-published port. For a Compose mapping such as `9090:8080`, Coral
  and Beyla observe and report `8080`.
- This discovery path does not apply to the containerized Docker deployment
  in Method 2, since that container's agent only sees its own network
  namespace unless it is itself run with `network_mode: host`.

---

## Verification & Operations

### Checking Agent Status

From the VPS host:

```bash
coral-agent status
```

Or view systemd logs:

```bash
journalctl -u coral-agent -f
```

### Verifying Connection from Operator Workstation

From your local machine with `coral` CLI connected to the Colony:

```bash
# Check colony agent list
coral status

# List discovered services on the VPS
coral services list
```

---

## Troubleshooting

### Capability / Permission Denied

If the agent fails to initialize WireGuard interfaces or eBPF probes:

- Verify kernel version (`uname -r`).
- Ensure `setcap` was applied or `AmbientCapabilities` are enabled in systemd
  unit.
- If running under SELinux / AppArmor, verify eBPF policies permit probe
  attachment.

### WireGuard Connection Timeout

- Ensure UDP port `41580` (or configured Colony endpoint port) is open on Colony
  security groups / firewalls.
- Check WireGuard status via `coral-agent status`.
