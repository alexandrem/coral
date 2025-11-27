# Coral Kubernetes Deployments

This directory contains Kubernetes manifests for deploying Coral agents in
various configurations.


## Deployment Requirements

### Docker-compose Sidecar

```yaml
services:
  app:
    image: nginx:alpine

  coral-agent:
    image: coral-agent:latest
    pid: "service:app"           # Share PID namespace with app
    network_mode: "service:app"  # Share network namespace
    cap_add:
      - NET_ADMIN                # For WireGuard mesh
      - SYS_ADMIN                # For nsenter (coral exec)
      - SYS_PTRACE               # For /proc inspection (coral exec)
      - SYS_RESOURCE             # For eBPF memlock
```

### Kubernetes Sidecar (Native - K8s 1.28+)

```yaml
apiVersion: v1
kind: Pod
spec:
  shareProcessNamespace: true
  initContainers:
    - name: coral-agent
      image: coral-agent:latest
      restartPolicy: Always      # Makes initContainer long-running
      securityContext:
        capabilities:
          add:
            - NET_ADMIN          # For WireGuard mesh
            - SYS_ADMIN          # For nsenter (coral exec)
            - SYS_PTRACE         # For /proc inspection (coral exec)
            - SYS_RESOURCE       # For eBPF memlock
  containers:
    - name: app
      image: nginx:alpine
```

### Kubernetes Sidecar (Regular)

```yaml
apiVersion: v1
kind: Pod
spec:
  shareProcessNamespace: true
  containers:
    - name: app
      image: nginx:alpine
    - name: coral-agent
      image: coral-agent:latest
      securityContext:
        capabilities:
          add:
            - NET_ADMIN          # For WireGuard mesh
            - SYS_ADMIN          # For nsenter (coral exec)
            - SYS_PTRACE         # For /proc inspection (coral exec)
            - SYS_RESOURCE       # For eBPF memlock
```

### Kubernetes DaemonSet (Node Agent)

```yaml
apiVersion: apps/v1
kind: DaemonSet
spec:
  template:
    spec:
      hostPID: true              # See all processes on node
      hostNetwork: true
      containers:
        - name: agent
          image: coral-agent:latest
          securityContext:
            capabilities:
              add:
                - NET_ADMIN        # For WireGuard mesh
                - SYS_ADMIN        # For nsenter (coral exec)
                - SYS_PTRACE       # For /proc inspection (coral exec)
                - SYS_RESOURCE     # For eBPF memlock
```

### Port Configuration

The Coral agent exposes the following ports which should be configured in your Pod/DaemonSet specs:

| Port     | Protocol | Purpose                            | Access              |
|----------|----------|------------------------------------|---------------------|
| **4317** | TCP      | OTLP/gRPC trace ingestion          | Applications, Beyla |
| **4318** | TCP      | OTLP/HTTP trace ingestion          | Applications, Beyla |
| **4319** | TCP      | Internal Beyla trace ingestion     | Localhost only      |
| **9001** | TCP      | Agent API for colony communication | Mesh IP + localhost |

**Note**: Port 9001 is NOT exposed as a Kubernetes Service. The colony accesses the agent directly via the WireGuard mesh IP.

---

## Capabilities Reference

Understanding which capabilities enable which features is critical for security and functionality.

### Capability to Feature Mapping

| Capability                | Feature Enabled                                           | Required For                                                 | Alternative                                         |
|---------------------------|-----------------------------------------------------------|--------------------------------------------------------------|-----------------------------------------------------|
| **NET_ADMIN**             | WireGuard mesh networking                                 | Mesh connectivity between agents and colony                  | None - required for WireGuard                       |
| **SYS_ADMIN**             | Container namespace execution (`coral exec` nsenter mode) | Accessing container-mounted configs, volumes, and filesystem | CRI-based exec (limited, no mount namespace access) |
| **SYS_PTRACE**            | Process inspection via /proc                              | Container PID detection for `coral exec`                     | None - required for nsenter PID detection           |
| **SYS_RESOURCE**          | eBPF memory locking                                       | eBPF collector initialization                                | Use non-eBPF deployment mode                        |
| **BPF** (kernel 5.8+)     | Modern eBPF without SYS_ADMIN                             | eBPF collectors on modern kernels                            | Use SYS_ADMIN on older kernels                      |
| **PERFMON** (kernel 5.8+) | Performance monitoring eBPF                               | eBPF performance collectors                                  | Use SYS_ADMIN on older kernels                      |

### Feature to Capability Mapping

| Feature                      | Required Capabilities        | Deployment Modes Supporting This      |
|------------------------------|------------------------------|---------------------------------------|
| **WireGuard mesh**           | NET_ADMIN                    | All modes                             |
| **`coral connect`**          | (CRI socket access)          | All modes                             |
| **`coral shell`**            | (none - basic exec)          | All modes                             |
| **`coral exec` (nsenter)**   | SYS_ADMIN + SYS_PTRACE       | eBPF Full, eBPF Privileged, DaemonSet |
| **`coral exec` (CRI)**       | (CRI socket access)          | Restricted, eBPF Minimal              |
| **eBPF collectors (modern)** | BPF + PERFMON + SYS_RESOURCE | eBPF Minimal                          |
| **eBPF collectors (legacy)** | SYS_ADMIN + SYS_RESOURCE     | eBPF Full, eBPF Privileged, DaemonSet |

### Deployment Mode Capability Summary

| Mode                | Capabilities                                             | `coral exec` nsenter | eBPF Support       | PodSecurity Level |
|---------------------|----------------------------------------------------------|----------------------|--------------------|-------------------|
| **Restricted**      | None                                                     | ❌ (CRI only)         | ❌                  | `restricted`      |
| **eBPF Minimal**    | BPF, PERFMON, NET_ADMIN, SYS_RESOURCE                    | ❌ (CRI only)         | ✅ (modern kernels) | `baseline`        |
| **eBPF Full**       | SYS_ADMIN, SYS_PTRACE, NET_ADMIN, SYS_RESOURCE           | ✅                    | ✅ (all kernels)    | `baseline`        |
| **eBPF Privileged** | privileged: true                                         | ✅                    | ✅ (maximum)        | `privileged`      |
| **DaemonSet**       | SYS_ADMIN, SYS_PTRACE, NET_ADMIN, SYS_RESOURCE + hostPID | ✅                    | ✅                  | `privileged`      |

**Key Insights**:

- **For full `coral exec` functionality**: Use eBPF Full, eBPF Privileged, or DaemonSet modes
- **For eBPF without SYS_ADMIN**: Use eBPF Minimal mode (requires kernel 5.8+)
- **For maximum security**: Use Restricted mode (no nsenter, no eBPF)
- **For production observability**: Use DaemonSet mode (best visibility and performance)

---

## Deployment Modes

### DaemonSet (Node-Level)

**File**: `agent-daemonset.yaml`

Deploys one agent per Kubernetes node for full node-level observability.

**Features**:

- ✅ Full eBPF support (all collectors)
- ✅ Node-wide visibility (all pods, all containers)
- ✅ Most efficient resource usage
- ✅ Best security isolation

**Use Cases**:

- Production observability
- Cluster-wide monitoring
- Performance profiling
- Security monitoring

**Security**: Requires privileged mode and hostPID/hostNetwork.

---

### Sidecar (Pod-Level)

Deploy Coral agent alongside your application containers.

#### 1. **Restricted Mode** (Recommended for Production)

**File**: `agent-sidecar-restricted.yaml`

Most secure deployment with no eBPF support.

**Features**:

- ✅ `coral connect` - Monitor containers via CRI
- ✅ `coral shell` - Interactive debugging (agent host only)
- ⚠️ `coral exec` - **LIMITED**: CRI-based exec only (not nsenter)
  - ❌ Cannot access container mount namespace
  - ❌ Cannot read container-mounted configs/volumes
  - ✅ Can execute commands via CRI runtime
- ❌ eBPF collectors - Not supported
- ✅ PodSecurity: `restricted` compatible

**Requirements**:

- None (works on any Kubernetes cluster)
- CRI runtime socket access for limited exec functionality

**Limitations**:

- **No `coral exec` nsenter mode**: Requires CAP_SYS_ADMIN + CAP_SYS_PTRACE (not available in restricted mode)
- For full `coral exec` functionality (container filesystem access), use eBPF Full or Privileged modes

**Use Cases**:

- Production workloads with strict security policies
- Multi-tenant environments
- Compliance-heavy industries (finance, healthcare, government)
- When CRI-based exec is sufficient (no need for container filesystem access)

---

#### 2. **eBPF Minimal Mode** (Modern Kubernetes)

**File**: `agent-sidecar-ebpf-minimal.yaml`

Basic eBPF support for modern infrastructure.

**Features**:

- ✅ `coral connect`, `shell`, `exec`
- ✅ eBPF `http_latency` - HTTP request histograms
- ✅ eBPF `tcp_metrics` - TCP retransmits, RTT
- ✅ eBPF `syscall_stats` - Syscall counts
- ⚠️ eBPF `cpu_profile` - May require SYS_ADMIN
- ✅ PodSecurity: `baseline` compatible

**Requirements**:

- Linux kernel 5.8+
- BTF enabled (`/sys/kernel/btf/vmlinux` exists)
- CAP_BPF, CAP_PERFMON, CAP_NET_ADMIN capabilities

**Use Cases**:

- Modern Kubernetes clusters (1.20+)
- GKE Autopilot, EKS (Bottlerocket 1.9+), AKS (Ubuntu 20.04+)
- Development/staging environments
- Detailed observability with minimal security impact

**Validation**:

```bash
# Check kernel version and BTF support
kubectl debug node/<node-name> -it --image=busybox -- \
  sh -c "uname -r; ls -la /sys/kernel/btf/vmlinux"
```

---

#### 3. **eBPF Full Mode** (Legacy Kubernetes)

**File**: `agent-sidecar-ebpf-full.yaml`

Full eBPF support for older kernels.

**Features**:

- ✅ All operations from Minimal Mode
- ✅ eBPF `cpu_profile` - Full CPU profiling
- ✅ Works on older kernels (4.7+)
- ⚠️ PodSecurity: `baseline` (requires CAP_SYS_ADMIN)

**Requirements**:

- Linux kernel 4.7+
- CAP_SYS_ADMIN (broad privilege)
- Must run as root (UID 0)

**Use Cases**:

- Legacy Kubernetes clusters
- RHEL 7/CentOS 7 (kernel 3.10 with backports)
- Ubuntu 18.04 (kernel 4.15)
- When full eBPF capabilities required

**Security Considerations**:

- ⚠️ Grants CAP_SYS_ADMIN (powerful capability)
- ⚠️ Must run as root
- Consider using DaemonSet mode instead

---

#### 4. **eBPF Privileged Mode** (Maximum Compatibility)

**File**: `agent-sidecar-ebpf-privileged.yaml`

Maximum eBPF support with full privileges.

**Features**:

- ✅ All eBPF collectors
- ✅ Maximum compatibility
- ✅ Works on any kernel 4.1+
- ❌ PodSecurity: `privileged` (bypasses most controls)

**Requirements**:

- Linux kernel 4.1+
- `privileged: true`
- Full host access

**Use Cases**:

- Development/testing environments
- Proof-of-concept deployments
- Legacy systems with custom patches

**⚠️ SECURITY WARNING**:

- Grants nearly unrestricted host access
- Bypasses most security controls
- DO NOT use in production unless security policy explicitly allows
- **Recommended alternative**: Use DaemonSet mode instead

---

## Quick Start

### 1. Create Namespace and Secrets

```bash
# Create dedicated namespace
kubectl create namespace coral-system

# Create colony credentials
kubectl create secret generic coral-colony-secret \
  --namespace=coral-system \
  --from-literal=colony_id=<your-colony-id> \
  --from-literal=colony_secret=<your-colony-secret>
```

### 2. Deploy DaemonSet (Recommended)

```bash
kubectl apply -f agent-daemonset.yaml
```

### 3. Or Deploy Sidecar

Choose the appropriate variant based on your requirements:

```bash
# Most secure (no eBPF)
kubectl apply -f agent-sidecar-restricted.yaml

# Modern clusters (kernel 5.8+)
kubectl apply -f agent-sidecar-ebpf-minimal.yaml

# Legacy clusters (kernel 4.7+)
kubectl apply -f agent-sidecar-ebpf-full.yaml

# Development only (privileged)
kubectl apply -f agent-sidecar-ebpf-privileged.yaml
```

---

## Decision Matrix

| Requirement               | Recommended Deployment                         |
|---------------------------|------------------------------------------------|
| Production observability  | **DaemonSet**                                  |
| Strict security policies  | **Sidecar: Restricted**                        |
| Modern K8s (1.20+) + eBPF | **Sidecar: eBPF Minimal**                      |
| Legacy K8s + eBPF         | **Sidecar: eBPF Full** or **DaemonSet**        |
| Multi-tenant isolation    | **Sidecar: Restricted**                        |
| Maximum visibility        | **DaemonSet**                                  |
| Development/testing       | **Sidecar: eBPF Privileged** (short-term only) |

---

## Validation

### Check Agent Status

```bash
# DaemonSet
kubectl get pods -n coral-system -l app=coral-agent
kubectl logs -n coral-system -l app=coral-agent --tail=50

# Sidecar
kubectl logs <pod-name> -c coral-agent --tail=50
```

### Verify eBPF Support

```bash
# Check kernel version
kubectl exec -it <pod-name> -c coral-agent -- uname -r

# Check eBPF capabilities
kubectl exec -it <pod-name> -c coral-agent -- sh -c "
  echo 'Kernel:' \$(uname -r);
  echo 'BTF:' \$(ls /sys/kernel/btf/vmlinux 2>/dev/null && echo YES || echo NO);
  echo 'BPF FS:' \$(ls /sys/fs/bpf 2>/dev/null && echo YES || echo NO);
  echo 'Capabilities:' \$(grep Cap /proc/self/status | head -3)
"
```

### Test Operations

```bash
# Test coral connect
kubectl exec -it <pod-name> -c coral-agent -- \
  coral agent status

# Test eBPF (if enabled)
kubectl exec -it <pod-name> -c coral-agent -- \
  coral tap <service> --http-latency --duration 10s
```

---

## Security Best Practices

1. **Use NetworkPolicy** to restrict agent egress
2. **Deploy in dedicated namespace** (e.g., `coral-system`)
3. **Enable PodSecurity standards** enforcement
4. **Audit privileged containers** regularly
5. **Rotate secrets** periodically
6. **Monitor agent behavior** for anomalies
7. **Use RBAC** to limit service account permissions

Example NetworkPolicy:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
    name: coral-agent-egress
    namespace: coral-system
spec:
    podSelector:
        matchLabels:
            app: coral-agent
    policyTypes:
        - Egress
    egress:
        # Allow DNS
        -   to:
                -   namespaceSelector:
                        matchLabels:
                            kubernetes.io/metadata.name: kube-system
            ports:
                -   protocol: UDP
                    port: 53
        # Allow colony connection
        -   to:
                -   podSelector:
                        matchLabels:
                            app: coral-colony
            ports:
                -   protocol: TCP
                    port: 9000
```

---

## Troubleshooting

### eBPF Collectors Failing

**Symptom**: eBPF collectors fail with "permission denied" or "verifier
rejected"

**Solutions**:

1. Check kernel version: `kubectl exec ... -- uname -r`
2. Verify BTF support: `kubectl exec ... -- ls /sys/kernel/btf/vmlinux`
3. Check capabilities: `kubectl exec ... -- capsh --print`
4. Review securityContext in manifest
5. Check kernel config: `grep CONFIG_BPF /boot/config-$(uname -r)`

### Agent Not Connecting to Colony

**Symptom**: Agent logs show "failed to register with colony"

**Solutions**:

1. Verify colony secret: `kubectl get secret coral-colony-secret -o yaml`
2. Check colony is running: `coral colony status`
3. Verify network connectivity: `kubectl exec ... -- curl http://colony:9000`
4. Check WireGuard mesh: `kubectl exec ... -- wg show`

### Container Runtime Socket Not Found

**Symptom**: Agent logs show "CRI socket not found"

**Solutions**:

1. Identify CRI runtime: `kubectl get nodes -o wide`
2. Update volume mount in manifest:
    - containerd: `/var/run/containerd/containerd.sock`
    - CRI-O: `/var/run/crio/crio.sock`
    - Docker: `/var/run/docker.sock`

---

## References

- [RFD 016: Unified Operations UX](../../RFDs/016-unified-operations-ux.md)
- [RFD 056: Container Exec via nsenter](../../RFDs/056-container-exec-nsenter.md) (supersedes RFD 017)
- [RFD 026: Shell Command Implementation](../../RFDs/026-shell-command-implementation.md)
- [RFD 013: eBPF-Based Introspection](../../RFDs/013-ebpf-introspection.md)
- [RFD 012: Kubernetes Node Agent](../../RFDs/012-kubernetes-node-agent.md)
- [Kubernetes PodSecurity Standards](https://kubernetes.io/docs/concepts/security/pod-security-standards/)
- [Linux Capabilities](https://man7.org/linux/man-pages/man7/capabilities.7.html)
