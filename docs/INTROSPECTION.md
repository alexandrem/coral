
## eBPF Introspection (Advanced, Optional)

eBPF (Extended Berkeley Packet Filter) can provide deep introspection into application behavior. Coral supports eBPF as an **optional advanced feature** with multiple approaches based on security and capability needs.

### Why eBPF?

**What eBPF Enables:**
- Function-level tracing (which functions are slow?)
- Network packet inspection (TCP connections, latency)
- Memory profiling (allocation patterns, leaks)
- System call tracing (file I/O, network calls)
- Custom kernel-level instrumentation

**Limitations:**
- Requires Linux kernel 5.8+
- Needs elevated privileges (CAP_BPF or CAP_SYS_ADMIN)
- Language support varies (best in Go/Rust)
- Performance overhead (usually <1%, but measurable)

### eBPF Integration Tiers

**Tier 1: No eBPF (Default)**
```
Works everywhere, zero privileges needed

Agent observes via:
├─> netstat/ss (network connections)
├─> /proc (process stats, memory, CPU)
├─> HTTP health endpoints
└─> SDK gRPC (if integrated)

Use when:
✓ Standard deployment (no special privileges)
✓ Covers 90% of use cases
✓ Security-conscious environments
```

**Tier 2: Privileged Agent eBPF (Opt-In)**
```
Agent runs with CAP_BPF, loads eBPF programs

Architecture:
┌─────────────────────┐
│  Privileged Agent   │
│  CAP_BPF enabled    │
│                     │
│  Loads eBPF:        │
│  ├─> TCP tracing    │
│  ├─> Function trace │
│  └─> Memory profile │
└──────────┬──────────┘
           │ Attaches to
           ▼
    ┌──────────────┐
    │  App Process │
    │  (unprivileged)
    └──────────────┘

Use when:
✓ Need deep introspection
✓ Platform team controls agents
✓ Can grant CAP_BPF to DaemonSet
✓ Want uniform instrumentation

Risks:
⚠️ Privileged agent = security boundary
⚠️ Compromised agent has kernel access
⚠️ Deployment complexity (K8s privileged DS)
```

**Tier 3: SDK eBPF (Future, Go/Rust Only)**
```
App SDK loads eBPF for self-introspection

Architecture:
┌──────────────────────┐
│  App Process         │
│  CAP_BPF enabled     │
│                      │
│  ┌────────────────┐ │
│  │ App Code       │ │
│  ├────────────────┤ │
│  │ Coral SDK      │ │
│  │  └─> eBPF      │ │
│  │      loader    │ │
│  └────────────────┘ │
└──────────────────────┘

Use when:
✓ App written in Go/Rust (good eBPF support)
✓ Fine-grained control needed
✓ Security-conscious (scoped privileges)
✓ Can grant CAP_BPF per-app

Pros:
✓ Blast radius limited to single app
✓ App controls what's instrumented
✓ Easier security audit

Cons:
❌ Every app needs CAP_BPF
❌ Language constraints (Go/Rust only)
❌ Can't introspect other processes
```

### eBPF Configuration

**Enabling Agent eBPF (Tier 2):**

```yaml
# ~/.coral/config.yaml
colony:
  mesh_id: coral-xyz

agent:
  ebpf:
    enabled: true           # Opt-in to eBPF introspection
    programs:
      - tcp_connect         # Trace TCP connections
      - function_timing     # Function-level latency
      - memory_alloc        # Memory allocation tracking

    # Safety limits
    max_overhead_percent: 1.0  # Max 1% CPU overhead
    auto_disable_if_slow: true # Disable if slows app
```

**Kubernetes Deployment (Privileged Agent):**

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: coral-agent
spec:
  template:
    spec:
      hostPID: true
      containers:
      - name: coral-agent
        image: coral/agent:latest
        securityContext:
          capabilities:
            add:
            - BPF
            - SYS_ADMIN  # May be needed for older kernels
        volumeMounts:
        - name: bpf
          mountPath: /sys/fs/bpf
      volumes:
      - name: bpf
        hostPath:
          path: /sys/fs/bpf
```

### eBPF Security Considerations

**Privileged Agent Risks:**
- Agent compromise = kernel-level access
- Can read sensitive data from any process
- Cluster-wide privilege escalation risk
- Must harden agent extensively

**Mitigations:**
- Audit logs for all eBPF operations
- Network policies (agent can't egress)
- Regular security scans
- Minimal eBPF program set
- Auto-disable on errors

**When NOT to Use eBPF:**
- Highly regulated environments (finance, healthcare)
- Multi-tenant clusters (privilege concerns)
- Windows/Mac (eBPF is Linux-only)
- Standard use cases (passive observation sufficient)

### eBPF vs. Passive Observation

| Capability | Passive | Agent eBPF | SDK eBPF |
|------------|---------|------------|----------|
| **Privilege Required** | None | CAP_BPF (agent) | CAP_BPF (per-app) |
| **Function Tracing** | ❌ | ✅ All processes | ✅ Self only |
| **Network Latency** | ⚠️ netstat | ✅ Per-connection | ✅ Per-connection |
| **Memory Profiling** | ⚠️ /proc total | ✅ Allocation tracking | ✅ Allocation tracking |
| **Security Risk** | ✅ Low | ⚠️ High | ⚠️ Medium |
| **Deployment** | ✅ Easy | ❌ Complex | ⚠️ Per-app |
| **Language Support** | ✅ Any | ✅ Any | ❌ Go/Rust only |

**Recommendation**: Start without eBPF (Tier 1), add agent eBPF (Tier 2) only if critical need, consider SDK eBPF (Tier 3) for Go/Rust apps wanting maximum control.
