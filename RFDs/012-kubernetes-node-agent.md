---
rfd: "012"
title: "Kubernetes Agent Deployment Patterns"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "002", "007", "011", "016" ]
database_migrations: [ ]
areas: [ "kubernetes", "agent", "networking", "observability", "ux" ]
---

# RFD 012 - Kubernetes Agent Deployment Patterns

**Status:** 🚧 Draft

## Summary

Define two Kubernetes deployment patterns for Coral agents: **DaemonSet** (
node-wide discovery) and **Sidecar** (pod-scoped monitoring). Both enable
zero-config observability but serve different security and operational needs.

**DaemonSet mode**: Privileged agent per node, discovers all pods on the node,
supports remote operations (`coral exec`, `coral shell`) targeting any pod.

**Sidecar mode**: Long-running init container per pod with
`shareProcessNamespace`, monitors all containers within the pod, enables
interactive debugging (`coral shell`, `coral exec`) within the pod.

**Note:** This RFD expands on RFD 011's Phase 5 (Passive Discovery) and
integrates with RFD 016's runtime-adaptive agent architecture.

## Problem

**Current behavior/limitations**

- Coral only monitors workloads where an operator explicitly deploys a sidecar
  (`coral connect ...`) or runs an agent manually on the host.
- Pods without Coral sidecars—legacy workloads, third-party charts, batch jobs,
  DaemonSets—stay invisible to the colony registry, limiting observability and
  control.
- Operators must continuously update manifests when new services spin up,
  leading to drift and blind spots.
- Remote operations (profiling, `coral shell`, `coral exec`) are impossible on
  pods
  lacking an agent, undermining the "application intelligence mesh" story.
- **No clear guidance on deployment patterns**: When to use DaemonSet vs.
  sidecar?
  What are the trade-offs?
- **Multi-tenant clusters need pod-scoped isolation**: DaemonSet with node-wide
  access doesn't fit security models where tenants must be isolated.
- **Interactive debugging undefined**: How do developers get a shell into pods
  for debugging in Kubernetes environments?

**Why this matters**

- Platform/SRE teams want cluster-wide coverage without touching every workload.
- Short-lived pods and horizontally autoscaled services need automatic discovery
  to maintain accurate topology and health status.
- Compliance and audit trails rely on the colony's registry being complete and
  authoritative.
- **Multi-tenant platforms** (GKE Autopilot, AWS Fargate, shared clusters)
  restrict
  privileged DaemonSets but allow pod-scoped agents.
- **Developer productivity** suffers without interactive debugging tools (
  `coral shell`)
  in Kubernetes environments.

**Use cases affected**

- **Single-tenant clusters**: Want cluster-wide coverage with minimal overhead
  (DaemonSet).
- **Multi-tenant clusters**: Need pod-scoped isolation, no node-wide access
  (Sidecar).
- **Interactive debugging**: Developers need `coral shell` to debug running
  pods.
- **Remote operations**: SRE teams need `coral exec` for fleet-wide diagnostics.
- **Incident response**: Packet inspection or profiling on pods without
  pre-installed
  agents.
- **Fleet-wide health aggregation** (Reef, RFD 003) that assumes consistent
  colony
  data.

## Solution

Support **two deployment patterns** for Kubernetes agents, each optimized for
different security and operational requirements:

### 1. DaemonSet Mode (Node-Wide Discovery)

Deploy one privileged agent per node that discovers all pods on the node via
Kubernetes API/Kubelet. Emits `ServiceInfo` entries (RFD 011) for discovered
workloads and supports remote operations targeting any pod on the node.

**Best for**: Single-tenant clusters, self-managed Kubernetes, full cluster
control.

### 2. Sidecar Mode (Pod-Scoped Monitoring)

Deploy agent as long-running init container (`restartPolicy: Always`) with
`shareProcessNamespace: true`. Monitors all containers within the pod and
enables interactive debugging within pod boundaries.

**Best for**: Multi-tenant clusters, GKE Autopilot, AWS Fargate, restricted
environments.

### Key Design Decisions

**Common to both modes:**

- **Passive discovery first, explicit overrides optional**: Label/namespace
  filters and per-pod annotations determine which services register.
- **Shared multi-service protocol**: Reuse `ServiceInfo` and mesh registration
  from RFD 011 to avoid divergent code paths.
- **Observable provenance**: Each service logs whether it was discovered,
  overridden, or explicitly configured.
- **Runtime context detection** (RFD 016): Agent detects whether it's running as
  DaemonSet or Sidecar and adjusts capabilities accordingly.
- **Remote operations support** (RFD 016): Both modes support `coral shell` and
  `coral exec` for interactive debugging and diagnostics.

**DaemonSet-specific:**

- **One agent per node**: Uniform coverage with minimal overhead.
- **Privileged permissions**: `hostPID`, `hostNetwork`, `NET_ADMIN`,
  `SYS_PTRACE`.
- **Node-wide visibility**: Discovers all pods on node via Kubernetes API.
- **Control operations**: Packet capture, profiling, eBPF tracing across node.
- **Target selection**: `coral shell --pod=<name>` to exec into specific pod.

**Sidecar-specific:**

- **One agent per pod**: Pod-scoped isolation, multi-tenant safe.
- **Long-running init container**: `restartPolicy: Always` keeps agent running.
- **Shared process namespace**: `shareProcessNamespace: true` enables pod-wide
  visibility.
- **Pod-scoped operations**: `coral shell` execs into app container within same
  pod.
- **Limited privileges**: No `hostPID`, minimal capabilities (`SYS_PTRACE`,
  `BPF`).
- **Auto-injection**: Mutating webhook can inject sidecar automatically.

### Benefits

**DaemonSet mode:**

- **Cluster-wide coverage**: Single deployment covers all nodes and pods.
- **Low overhead**: One agent per node (vs. per pod).
- **Full control**: Packet capture, profiling, eBPF tracing across all pods.
- **Immediate visibility**: Auto-discovers new workloads, autoscaled replicas.
- **Fleet operations**: `coral exec --all` runs diagnostics across cluster.

**Sidecar mode:**

- **Multi-tenant safe**: Pod-scoped isolation, no cross-pod visibility.
- **Restrictive environment compatible**: Works in GKE Autopilot, Fargate.
- **Interactive debugging**: `coral shell` for developers without
  `kubectl exec`.
- **Auto-injection**: Mutating webhook deploys automatically per namespace.
- **Pod lifecycle coupling**: Agent starts/stops with pod.

**Common benefits:**

- Richer data for Reef and AI insights: complete inventory, provenance-aware
  telemetry.
- Remote operations via mesh (RFD 016):
  `coral shell --service=X --env=production`.
- Unified UX across local, Docker, and Kubernetes (RFD 016).

### Architecture Overview

#### DaemonSet Mode (Node-Wide)

```
┌───────────────────────────────────────────────────────────────────┐
│ Kubernetes Cluster                                                │
│                                                                   │
│ Node ip-10-0-1-23                                                 │
│ ┌───────────────────────────────────────────────────────────────┐ │
│ │ Coral Node Agent (DaemonSet)                                   │ │
│ │  Runtime: RuntimeK8sDaemonSet                                  │ │
│ │  • Joins colony mesh (WireGuard)                               │ │
│ │  • Watches kubelet/API for pods                                │ │
│ │  • Applies include/exclude filters                             │ │
│ │  • Emits ServiceInfo (RFD 011) for matches                     │ │
│ │  • Handles coral shell/exec --pod=<name>                       │ │
│ └───────────────────────────────────────────────────────────────┘ │
│    │             │             │                                   │
│    ▼             ▼             ▼                                   │
│ Pod A (app)   Pod B (redis)  Pod C (metrics)   …                   │
└───────────────────────────────────────────────────────────────────┘
            │
            ▼
┌────────────────────────────┐
│ Colony                     │
│  • Registry (agents+svcs)  │
│  • Routes remote commands  │
│  • RBAC & approvals        │
└────────────────────────────┘
```

#### Sidecar Mode (Pod-Scoped)

```
┌───────────────────────────────────────────────────────────────────┐
│ Pod: my-app (shareProcessNamespace: true)                         │
│                                                                   │
│ ┌───────────────────────────────────────────────────────────────┐ │
│ │ Init Container: coral-agent (restartPolicy: Always)            │ │
│ │  Runtime: RuntimeK8sSidecar                                    │ │
│ │  • Joins colony mesh (WireGuard)                               │ │
│ │  • Monitors all containers in pod                              │ │
│ │  • Emits ServiceInfo for each container                        │ │
│ │  • Handles coral shell (execs into app container)              │ │
│ │  • Handles coral exec (runs in app container)                  │ │
│ └───────────────────────────────────────────────────────────────┘ │
│    │                                                               │
│    ▼ (shared PID namespace)                                       │
│ ┌─────────────────┐  ┌─────────────────┐  ┌──────────────────┐   │
│ │ Container: app  │  │ Container: redis│  │ Container: nginx │   │
│ │ PID 10-50       │  │ PID 51-80       │  │ PID 81-100       │   │
│ └─────────────────┘  └─────────────────┘  └──────────────────┘   │
└───────────────────────────────────────────────────────────────────┘
            │
            ▼
┌────────────────────────────┐
│ Colony                     │
│  • Registry (agents+svcs)  │
│  • Routes remote commands  │
│  • RBAC & approvals        │
└────────────────────────────┘
```

## Deployment Pattern Details

### DaemonSet Deployment

**Pod spec:**

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
    name: coral-agent
    namespace: coral-system
spec:
    template:
        spec:
            serviceAccountName: coral-agent
            hostNetwork: true
            hostPID: true
            containers:
                -   name: coral-agent
                    image: coral-agent:latest
                    command: [ "coral", "agent", "start" ]
                    securityContext:
                        privileged: true
                        capabilities:
                            add: [ "NET_ADMIN", "SYS_PTRACE", "BPF" ]
                    env:
                        -   name: CORAL_AGENT_MODE
                            value: "daemonset"
                        -   name: NODE_NAME
                            valueFrom:
                                fieldRef:
                                    fieldPath: spec.nodeName
```

**Agent detects `RuntimeK8sDaemonSet`**:

- Sees `/var/run/secrets/kubernetes.io` (in K8s)
- Has `hostPID: true` (sees all node PIDs)
- Not in shared PID namespace (no other containers)

**Capabilities**:

- ✅ Passive discovery (all pods on node)
- ✅ `coral shell --pod=<name>` (exec into any pod)
- ✅ `coral exec --pod=<name>` (run command in any pod)
- ✅ `coral connect --all` (monitor all node pods)
- ❌ `coral run` (passive only, can't launch processes)

### Sidecar Deployment (Long-Running Init Container)

**Pod spec:**

```yaml
apiVersion: v1
kind: Pod
metadata:
    name: my-app
spec:
    shareProcessNamespace: true  # CRITICAL: Enables pod-wide visibility

    initContainers:
        -   name: coral-agent
            image: coral-agent:latest
            command: [ "coral", "agent", "start", "--monitor-all" ]
            restartPolicy: Always  # CRITICAL: Keeps init container running
            securityContext:
                capabilities:
                    add: [ "SYS_PTRACE", "BPF" ]
            env:
                -   name: CORAL_AGENT_MODE
                    value: "sidecar"

    containers:
        -   name: app
            image: my-app:latest
            ports:
                -   containerPort: 8080

        -   name: redis
            image: redis:latest
            ports:
                -   containerPort: 6379
```

**Key features**:

1. **`restartPolicy: Always`** (Kubernetes 1.28+):
    - Init container doesn't block pod startup
    - Keeps running like a regular sidecar
    - Starts before app containers (sees their startup)

2. **`shareProcessNamespace: true`**:
    - All containers in pod share PID namespace
    - Agent can see PIDs from app, redis, etc.
    - Enables `exec` into app container

3. **Agent auto-discovery**:
    - `--monitor-all` flag tells agent to discover all containers in pod
    - Parses `/proc` to find container processes
    - Emits `ServiceInfo` for each container

**Agent detects `RuntimeK8sSidecar`**:

- Sees `/var/run/secrets/kubernetes.io` (in K8s)
- Sees multiple processes in `/proc` (shared namespace)
- No `hostPID` (can't see node PIDs)

**Capabilities**:

- ✅ Passive discovery (all containers in pod)
- ✅ `coral shell` (execs into app container)
- ✅ `coral exec "cmd"` (runs in app container)
- ✅ `coral connect container://app` (monitors app container)
- ❌ `coral run` (passive only)
- ❌ Cross-pod visibility (pod-scoped only)

### Auto-Injection via Mutating Webhook

**Enable auto-injection per namespace**:

```yaml
apiVersion: v1
kind: Namespace
metadata:
    name: production
    labels:
        coral.io/inject: "true"
```

**Webhook adds init container to all pods in namespace**:

```yaml
# Original pod
apiVersion: v1
kind: Pod
metadata:
    name: my-app
spec:
    containers:
        -   name: app
            image: my-app:latest

# After webhook mutation
apiVersion: v1
kind: Pod
metadata:
    name: my-app
    annotations:
        coral.io/injected: "true"
spec:
    shareProcessNamespace: true  # Added by webhook

    initContainers: # Added by webhook
        -   name: coral-agent
            image: coral-agent:latest
            command: [ "coral", "agent", "start", "--monitor-all" ]
            restartPolicy: Always

    containers:
        -   name: app
            image: my-app:latest
```

**Developer experience**:

```bash
# Deploy without changes
kubectl apply -f app.yaml -n production

# Sidecar automatically injected
kubectl get pods -n production
NAME       READY   STATUS    RESTARTS   AGE
my-app     2/2     Running   0          10s
#          ^^^ app + coral-agent

# Shell into app from laptop
coral shell --service=my-app --env=production
# → Colony routes to sidecar agent
# → Agent execs into app container
my-app $ curl localhost:8080/health
```

### Comparison Table

| Aspect                | DaemonSet               | Sidecar                      |
|-----------------------|-------------------------|------------------------------|
| **Deployment**        | One per node            | One per pod                  |
| **Overhead**          | Low (~50MB per node)    | Higher (~50MB per pod)       |
| **Isolation**         | Node-wide               | Pod-scoped                   |
| **Visibility**        | All pods on node        | Pod containers only          |
| **Use `coral shell`** | `--pod=<name>` required | Direct (no flag)             |
| **Use `coral exec`**  | `--pod=<name>` required | Direct (no flag)             |
| **Auto-injection**    | N/A (cluster-wide)      | Via mutating webhook         |
| **Multi-tenant**      | ❌ No (sees all pods)    | ✅ Yes (isolated)             |
| **Restrictive envs**  | ❌ Requires privileges   | ✅ Works (Autopilot, Fargate) |
| **eBPF capabilities** | Full (node-level)       | Limited (pod-level)          |

### Component Changes

1. **Agent** (`internal/agent/kubernetes` – new package):
    - **Runtime detection** (RFD 016): Detect `RuntimeK8sDaemonSet` vs
      `RuntimeK8sSidecar`.
    - **DaemonSet mode**:
        - Run as privileged DaemonSet with service account token.
        - Connect to colony once per node; maintain single WireGuard tunnel.
        - Implement discovery via Kubernetes API/kubelet with filters.
        - Handle `coral shell/exec --pod=<name>` (exec into target pod).
    - **Sidecar mode**:
        - Run as long-running init container with `restartPolicy: Always`.
        - Requires `shareProcessNamespace: true` on pod.
        - Discover containers via `/proc` parsing (shared namespace).
        - Handle `coral shell/exec` (exec into app container in same pod).
    - **Common**:
        - Merge auto-discovered services with explicit overrides.
        - Emit `ServiceInfo` (RFD 011) for discovered services.
        - Support remote operations via Colony routing (RFD 016).

2. **CLI** (`internal/cli`):
    - **Remote operations** (RFD 016):
        - `coral shell --pod=<name>` (DaemonSet mode: exec into specific pod)
        - `coral shell --service=<name>` (routes to sidecar or DaemonSet agent)
        - `coral exec --pod=<name> "cmd"` (DaemonSet mode)
        - `coral exec "cmd"` (Sidecar mode: runs in app container)
    - **Agent commands**:
        - `coral agent start --monitor-all` (sidecar mode flag)
        - Support passive-mode flags (`--include-label`, `--exclude-namespace`).

3. **Mutating Webhook** (`internal/webhook` – new package):
    - Watch namespace labels (`coral.io/inject: "true"`).
    - Mutate pod specs to add init container + `shareProcessNamespace`.
    - Support per-pod annotations for override/disable.

4. **Colony** (`internal/colony`):
    - **Registry**: Store per-service provenance, distinguish DaemonSet vs
      Sidecar.
    - **Command routing** (RFD 016): Route `shell`/`exec` to correct agent type.
    - **RBAC**: Enforce permissions for pod access (DaemonSet) vs service
      access (Sidecar).

5. **Protocol** (`proto/coral/mesh/v1/mesh.proto`):
    - Extend `ServiceInfo` with `provenance` enum and optional node metadata.
    - Extend `RegisterRequest` to include node name/labels.

**Configuration Example**

```yaml
# .coral/agent-config.yaml for passive node agent
agent:
    mode: passive
    id: node-agent-{{ NODE_NAME }}

discovery:
    kubernetes:
        includeNamespaces: [ "payments", "checkout" ]
        includeLabels:
            coral.io/enabled: "true"
        excludeNamespaces: [ "kube-system", "istio-system" ]
        pollInterval: 30s

overrides:
    -   selector:
            podLabel: app=payments-api
        service:
            name: payments-api
            health:
                endpoint: /healthz
                interval: 5s
            type: http
```

## Implementation Plan

### Phase 1: Protocol & Registry Foundation (Common)

- [ ] Add `ServiceInfo.provenance` (enum) and optional `node_name`,
  `node_labels`.
- [ ] Extend `RegisterRequest` with node metadata and `RuntimeContext`.
- [ ] Update colony registry to persist provenance, runtime type (DaemonSet vs
  Sidecar).
- [ ] Regenerate protobuf/go code; ensure single-service agents remain
  compatible.

### Phase 2: Runtime Detection (RFD 016)

- [ ] Implement `DetectRuntime()` to distinguish DaemonSet vs Sidecar.
- [ ] Add `GetRuntimeContext` RPC showing capabilities per runtime.
- [ ] Update agent startup to log detected runtime and capabilities.

### Phase 3: DaemonSet Discovery Engine

- [ ] Implement Kubernetes discovery provider (list/watch pods via client-go).
- [ ] Parse container ports, labels, annotations into `ServiceInfo`.
- [ ] Support filters (namespace, include/exclude labels) and annotation
  overrides.
- [ ] Handle watch reconnects and periodic full resyncs.

### Phase 4: DaemonSet Agent Runtime

- [ ] Create `coral-agent` DaemonSet manifest with required capabilities.
- [ ] Integrate discovery engine with multi-service agent core (RFD 011).
- [ ] Merge auto-discovered services with explicit config overrides.
- [ ] Implement garbage collection for stale services when pods terminate.
- [ ] Persist last-known services locally to reduce churn on restart.

### Phase 5: Sidecar Discovery Engine

- [ ] Implement `/proc` parsing for container discovery (shared PID namespace).
- [ ] Detect container boundaries via mount namespace, cgroup paths.
- [ ] Parse container metadata (ports, env vars) from `/proc/<pid>/environ`.
- [ ] Emit `ServiceInfo` for each discovered container in pod.

### Phase 6: Sidecar Agent Runtime

- [ ] Create pod manifest template with long-running init container.
- [ ] Validate `shareProcessNamespace: true` requirement.
- [ ] Implement `--monitor-all` flag for sidecar mode.
- [ ] Handle container lifecycle (discover new containers dynamically).

### Phase 7: Remote Operations (RFD 016)

**DaemonSet mode:**

- [ ] Implement `coral shell --pod=<name>` (exec into target pod via CRI).
- [ ] Implement `coral exec --pod=<name> "cmd"` (run command in target pod).
- [ ] Support pod selection (by name, label, namespace).

**Sidecar mode:**

- [ ] Implement `coral shell` (exec into app container via shared namespace).
- [ ] Implement `coral exec "cmd"` (run command in app container).
- [ ] Support multi-container pods (`--container` flag).

**Common:**

- [ ] Colony routing for remote operations.
- [ ] RBAC enforcement (who can access which pods/services).

### Phase 8: Mutating Webhook (Sidecar Auto-Injection)

- [ ] Implement webhook server (watches pod create events).
- [ ] Mutate pod spec to add init container + `shareProcessNamespace`.
- [ ] Support namespace-level enable (`coral.io/inject: "true"`).
- [ ] Support pod-level override annotations (`coral.io/inject: "false"`).
- [ ] Certificate management for webhook TLS.

### Phase 9: Security & Hardening

- [ ] Document RBAC roles for DaemonSet and Sidecar modes.
- [ ] Implement "observe-only" mode (no ptrace/profiler).
- [ ] Log discovery events and remote operations in colony audit log.
- [ ] Add rate limiting for remote operations.
- [ ] Document `shareProcessNamespace` security implications.

### Phase 10: Testing & Documentation

**Unit tests:**

- [ ] Runtime detection (DaemonSet vs Sidecar).
- [ ] Filter evaluation, override precedence.
- [ ] `/proc` parsing for sidecar discovery.

**Integration tests:**

- [ ] Fake Kubernetes API + pod churn (DaemonSet).
- [ ] Shared namespace container discovery (Sidecar).
- [ ] Remote operations routing.

**E2E tests:**

- [ ] KinD cluster with DaemonSet deployment.
- [ ] KinD cluster with Sidecar injection.
- [ ] `coral shell/exec` from laptop to both modes.
- [ ] Multi-tenant isolation (sidecar mode).

**Documentation:**

- [ ] Deployment guide (when to use DaemonSet vs Sidecar).
- [ ] Helm charts for both modes.
- [ ] Security guide (RBAC, PodSecurityStandards).
- [ ] Troubleshooting (shared namespace issues, privilege errors).

## API Changes

### Protobuf Updates (`proto/coral/mesh/v1/mesh.proto`)

```protobuf
enum ServiceProvenance {
    SERVICE_PROVENANCE_UNKNOWN = 0;
    SERVICE_PROVENANCE_EXPLICIT = 1;   // Provided via CLI/config
    SERVICE_PROVENANCE_DISCOVERED = 2; // Auto-discovered by node agent
    SERVICE_PROVENANCE_OVERRIDDEN = 3; // Auto-discovered + explicit override applied
}

message ServiceInfo {
    string component_name = 1;
    int32 port = 2;
    string health_endpoint = 3;
    string service_type = 4;
    map<string, string> labels = 5;
    ServiceProvenance provenance = 6;
    string node_name = 7;              // Optional: Kubernetes node
    map<string, string> node_labels = 8;
}

message RegisterRequest {
    string agent_id = 1;
    string component_name = 2 [deprecated = true];
    repeated ServiceInfo services = 10;

    string colony_id = 3;
    string colony_secret = 4;
    string version = 5;
    map<string, string> labels = 6;

    string wireguard_pubkey = 7;
    string node_name = 11;             // Name of Kubernetes node (if applicable)
    map<string, string> node_labels = 12;
}
```

### CLI Additions

- `coral agent node [flags]`
    - `--mode=passive` (default)
    - `--include-namespace <name>`
    - `--exclude-namespace <name>`
    - `--include-label key=value`
    - `--exclude-label key=value`
    - `--override-file <path>` (explicit metadata overrides)

### Configuration Changes

- New config section `discovery.kubernetes` with fields:
    - `includeNamespaces` (list, default empty → opt-in)
    - `includeLabels` / `excludeLabels`
    - `pollInterval`, `resyncInterval`
- Optional `overrides[]` entries for per-pod metadata adjustments.

## Testing Strategy

### Unit Tests

- Service filtering combinations (namespace + label include/exclude).
- Annotation overrides (health endpoint, type) precedence vs defaults.
- Provenance tagging logic (explicit vs discovered vs overridden).
- Local cache persistence/reload on agent restart.

### Integration Tests

- Fake Kubernetes API server with pod churn; validate registration updates and
  garbage collection.
- Multi-node scenario ensuring each agent reports correct node metadata.
- Control operation mock verifying tap/profile requests route to correct node.

### E2E Tests

- KinD-based scenario deploying the DaemonSet, spinning up sample workloads,
  validating discovery and health reporting.
- Trigger `coral tap` and profiling commands targeting auto-discovered services.
- Autoscaling test: scale deployments up/down and confirm registry accuracy.

## Security Considerations

### DaemonSet Mode

- **Privileges**: Requires privileged pod with `NET_ADMIN`, `SYS_PTRACE`,
  `hostPID`, `hostNetwork`.
  Document risks and provide "observe-only" mode when profiling disallowed.
- **RBAC**: Service account needs `list/watch` on Pods, Nodes cluster-wide.
  Define least-privilege roles.
- **Isolation**: Agent can access all pods on node. Use namespace filters to
  limit scope.
  Consider deny-by-default with explicit allow lists for sensitive environments.
- **Multi-tenant risk**: DaemonSet sees all tenant pods on node. NOT recommended
  for hard multi-tenancy.

### Sidecar Mode

- **Shared Process Namespace**: `shareProcessNamespace: true` means agent sees
  all processes in pod.
    - Agent can read `/proc/<pid>` for any container in pod
    - Agent can potentially signal or ptrace app processes
    - **Mitigation**: Agent runs with minimal capabilities, limited to
      `SYS_PTRACE` and `BPF`
- **Pod Security Standards**: Sidecar compatible with `restricted` profile (no
  `hostPID`/`hostNetwork`).
  Document required capabilities for compliance.
- **RBAC**: No cluster-wide permissions needed. Agent only monitors its own pod.
- **Multi-tenant safe**: Pod-scoped isolation. One tenant can't see another
  tenant's pods.
- **Init container risk**: Long-running init container (`restartPolicy: Always`)
  is Kubernetes 1.28+ feature.
  Document version requirement.

### Common

- **Auditability**: Log all discovery events and remote operations with:
    - Agent ID and runtime type (DaemonSet/Sidecar)
    - Service, namespace, provenance
    - Remote operation requester (user identity)
    - Command executed, duration, exit code
- **Data exposure**: All telemetry stays within WireGuard mesh. Optional
  encryption at rest via Colony DuckDB.
- **Remote operations**: `coral shell` and `coral exec` execute arbitrary
  commands. Enforce RBAC and approval
  workflows for production access.
- **Network security**: All agent-Colony communication over encrypted WireGuard
  tunnel. No plaintext telemetry.

## Future Enhancements

- Support additional discovery sources (e.g., container runtime socket, Istio
  service registry) for clusters with restricted API access.
- Dynamic per-namespace quotas to prevent runaway taps/profiles.
- Integration with Reef to summarize node-level anomalies and surface them in
  federated views.
- Automatic suggestion of observe-only vs full mode based on cluster policies.

## Notes

**Major Updates from Original Draft:**

This RFD was significantly expanded to support **two deployment patterns** (
DaemonSet and Sidecar) based on insights from **RFD 016 - Unified Operations UX
Architecture**:

- **Sidecar mode added**: Long-running init container with
  `shareProcessNamespace` for multi-tenant environments
- **Runtime context detection**: Agents detect DaemonSet vs Sidecar mode and
  adjust capabilities
- **Remote operations**: Both modes support `coral shell` and `coral exec` for
  interactive debugging
- **Mutating webhook**: Auto-inject sidecar per namespace for zero-config
  deployment


**Relationship to RFD 016:**

This RFD implements the Kubernetes-specific deployment patterns described in *
*RFD 016 - Unified Operations UX Architecture**:

- **Runtime contexts**: Implements `RuntimeK8sDaemonSet` and `RuntimeK8sSidecar`
  detection
- **Command support**: Implements `coral shell` and `coral exec` for both modes
- **Capability boundaries**: Defines what works in each mode (visibility,
  execution, remote ops)
- **Unified UX**: Provides consistent experience across local dev, Docker, and
  Kubernetes

**Relationship to RFD 017 and RFD 026:**

This RFD enables both execution commands in K8s environments:

- **`coral exec` (RFD 017)**: Execs into application containers via CRI API for
  app-scoped debugging
- **`coral shell` (RFD 026)**: Opens debug shell in agent environment with
  bundled troubleshooting tools

Both DaemonSet and Sidecar modes support these commands with CRI integration.

This RFD expands RFD 011's Phase 5 (Passive Discovery) into a full-featured
specification with:

- Service provenance tracking (explicit vs discovered vs overridden)
- Control operations (profiling, packet capture, traffic sampling)
- Production hardening (garbage collection, RBAC, audit logging)
- DaemonSet deployment model for cluster-wide coverage

**Design Philosophy:**

- **Zero-config observability**: Operators deploy once (DaemonSet), coverage is
  automatic
- **Explicit overrides when needed**: Annotations and config files provide
  escape hatches for special cases
- **Auditable and observable**: Every discovery decision and control operation
  is logged with full provenance
- **Progressive enhancement**: Start with basic discovery, add profiling/tap
  capabilities as trust increases

## Appendix

### DaemonSet Example (Observe-Only Mode)

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
    name: coral-node-agent
spec:
    selector:
        matchLabels:
            app: coral-node-agent
    template:
        metadata:
            labels:
                app: coral-node-agent
        spec:
            serviceAccountName: coral-node-agent
            hostPID: true
            containers:
                -   name: coral-agent
                    image: ghcr.io/coral-io/agent:latest
                    args:
                        - agent
                        - node
                        - --mode=passive
                        - --include-label=coral.io/enabled=true
                    securityContext:
                        privileged: true
                        capabilities:
                            add: [ "NET_ADMIN" ]
                    env:
                        -   name: CORAL_COLONY_ID
                            valueFrom:
                                secretKeyRef:
                                    name: coral-secrets
                                    key: colony-id
                        -   name: CORAL_COLONY_SECRET
                            valueFrom:
                                secretKeyRef:
                                    name: coral-secrets
                                    key: colony-secret
```

### Control Operation Flow

1. Operator runs `coral tap payments-api --capture`.
2. Colony identifies service provenance → node agent ID.
3. Colony sends tap RPC to node agent over WireGuard.
4. Node agent attaches eBPF/tcpdump to target pod namespace.
5. Results stream back to colony, stored with provenance metadata.
6. Audit log records action (operator, node, service, duration).
