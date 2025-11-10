---
rfd: "027"
title: "Client-Side Kubernetes Installation Tool"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "012-kubernetes-node-agent", "017-exec-command" ]
related_rfds: [ "025-basic-otel-ingestion" ]
database_migrations: [ ]
areas: [ "cli", "kubernetes", "deployment" ]
---

# RFD 027 - Client-Side Kubernetes Installation Tool

**Status:** 🚧 Draft

## Summary

A CLI tool that injects Coral agent sidecars into Kubernetes manifests without
requiring cluster-admin or namespace-admin privileges. Users pipe manifests
through `coral inject` or use the kubectl plugin, enabling self-service Coral
deployment while respecting cluster PodSecurity policies.

**Scope**: This RFD covers sidecar injection for **exec/shell capabilities**.
For OpenTelemetry ingestion in Kubernetes, use a cluster-wide collector service
(RFD 025) instead of per-pod sidecars.

## Problem

**Current Limitations:**

- **DaemonSet mode** requires cluster-admin privileges (`hostPID`,
  `hostNetwork`, privileged containers). Not suitable for:
    - Multi-tenant clusters
    - Managed Kubernetes (GKE Autopilot, AWS Fargate)
    - Environments with strict security policies

- **Manual sidecar mode** is tedious and error-prone:
    - Users must manually edit every Deployment/StatefulSet YAML
    - Easy to forget `shareProcessNamespace: true`
    - Inconsistent configuration across workloads
    - No validation of privilege requirements vs available features
    - Difficult to maintain as Coral evolves

- **Namespace controller/operator approaches** require:
    - Namespace-admin privileges (RBAC to patch Deployments)
    - Cannot grant privileges the namespace doesn't have
    - Limited value in restrictive environments (GKE Autopilot)
    - Additional operational complexity (controller lifecycle, CRDs)

**Why This Matters:**

Most users deploying to managed Kubernetes environments have:

- No cluster-admin access
- Limited or no namespace-admin access
- Strict PodSecurity policies enforced by platform teams
- Need for Coral observability and debugging features (exec, monitoring)

Without a low-friction installation method that works within these constraints,
Coral adoption is blocked for the majority of Kubernetes users.

**Use Cases Affected:**

1. Developer deploying to GKE Autopilot (Restricted PodSecurity, no privileges)
2. SRE team in multi-tenant cluster (Baseline PodSecurity, namespace-scoped)
3. Platform team needing consistent Coral integration across hundreds of
   microservices
4. GitOps workflows where all manifests must be declarative

## Solution

Provide a **client-side CLI tool** that transforms Kubernetes manifests to
inject Coral agent sidecars. The tool analyzes the target cluster's capabilities
and injects the appropriate configuration.

**Key Design Decisions:**

1. **Client-side transformation** (not server-side mutating webhook):
    - Works without any cluster privileges
    - Users maintain full visibility (can inspect output)
    - GitOps-friendly (output is regular YAML)
    - No operational burden (no controllers to manage)

2. **Environment-aware injection**:
    - Detects cluster PodSecurity policy (Restricted/Baseline/Privileged)
    - Injects only capabilities that cluster allows
    - Warns users when requested features unavailable
    - Graceful degradation of feature set

3. **Multiple integration modes**:
    - Standalone CLI: `coral inject deployment.yaml | kubectl apply -f -`
    - kubectl plugin: `kubectl coral apply -f deployment.yaml`
    - Library: Import as Go package for custom tooling

**Benefits:**

- **Zero privileges required**: Uses user's existing kubectl credentials
- **Universal compatibility**: Works on any Kubernetes cluster
- **Full visibility**: Users see exactly what's being deployed
- **GitOps-friendly**: Output can be committed to git
- **Progressive enhancement**: Automatically uses best available features
- **Consistent configuration**: Centralized injection logic ensures uniformity

**Architecture Overview:**

```
┌───────────────────────────────────────────────────────────┐
│                      User Workflow                        │
├───────────────────────────────────────────────────────────┤
│                                                           │
│  kubectl apply -f deployment.yaml                         │
│             ↓ (replaced with)                             │
│  coral inject deployment.yaml | kubectl apply -f -        │
│             ↓                                             │
│  ┌───────────────────────────────────────────────┐        │
│  │          Coral CLI (coral inject)             │        │
│  ├───────────────────────────────────────────────┤        │
│  │ 1. Parse Kubernetes manifest                  │        │
│  │ 2. Detect cluster capabilities                │        │
│  │ 3. Inject sidecar + configuration             │        │
│  │ 4. Validate against PodSecurity               │        │
│  │ 5. Output modified YAML                       │        │
│  └───────────────────────────────────────────────┘        │
│             ↓                                             │
│  Modified YAML → kubectl apply → Kubernetes API           │
│                                                           │
└───────────────────────────────────────────────────────────┘

┌───────────────────────────────────────────────────────────┐
│                  Feature Negotiation                      │
├───────────────────────────────────────────────────────────┤
│                                                           │
│  Cluster PodSecurity Policy Detection:                    │
│                                                           │
│  ┌─────────────────┐  ┌──────────────────┐  ┌──────────┐  │
│  │   Restricted    │  │    Baseline      │  │Privileged│  │
│  ├─────────────────┤  ├──────────────────┤  ├──────────┤  │
│  │ Features:       │  │ Features:        │  │Features: │  │
│  │ - Monitoring ✓  │  │ - Monitoring ✓   │  │ - All ✓  │  │
│  │ - Exec ✗        │  │ - Exec ✓         │  │          │  │
│  │ - eBPF ✗        │  │ - eBPF ✗         │  │          │  │
│  │                 │  │                  │  │          │  │
│  │ Injection:      │  │ Injection:       │  │Injection:│  │
│  │ - Sidecar       │  │ - Sidecar        │  │- Sidecar │  │
│  │ - shareNS: true │  │ - shareNS: true  │  │- Full    │  │
│  │ - No caps       │  │ - CAP_SYS_PTRACE │  │- caps    │  │
│  └─────────────────┘  └──────────────────┘  └──────────┘  │
│                                                           │
└───────────────────────────────────────────────────────────┘
```

### Component Changes

1. **CLI Tool (coral inject)**:

    - New subcommand under `coral` CLI
    - Parses Kubernetes YAML (Deployment, StatefulSet, DaemonSet, etc.)
    - Injects Coral agent as long-running init container
    - Adds `shareProcessNamespace: true`
    - Adds capabilities based on policy detection
    - Validates output against PodSecurity standards

2. **kubectl Plugin (kubectl-coral)**:

    - Wrapper that mimics `kubectl apply/create` semantics
    - Automatically pipes through `coral inject`
    - Provides `--dry-run` and `--diff` modes
    - Detects cluster context for policy awareness

3. **Policy Detector**:
    - Queries cluster for PodSecurity admission configuration
    - Falls back to heuristics (cluster type detection)
    - User override via `--policy` flag
    - Caches detection results per cluster context

4. **Manifest Transformer**:
    - Kubernetes API-aware YAML parser (preserves comments, order)
    - Handles multiple documents in single file
    - Supports kustomize output
    - Template-aware (Helm chart detection with warning)

**Configuration Example:**

```yaml
# User creates: deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
    name: myapp
spec:
    template:
        spec:
            containers:
                -   name: app
                    image: myapp:latest
```

```bash
# Transform with Coral injection
coral inject deployment.yaml > deployment-with-coral.yaml
```

```yaml
# Output: deployment-with-coral.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
    name: myapp
    annotations:
        coral.io/injected: "true"
        coral.io/version: "0.1.0"
        coral.io/features: "monitoring,exec"
spec:
    template:
        spec:
            shareProcessNamespace: true  # ADDED

            initContainers: # ADDED
                -   name: coral-agent
                    image: coral-io/agent:latest
                    restartPolicy: Always
                    command: [ "coral", "agent", "start", "--monitor-all" ]
                    env:
                        -   name: CORAL_DISCOVERY_ENDPOINT
                            value: "https://discovery.coral.io"
                        -   name: CORAL_COLONY_ID
                            value: "prod-us-east"
                        -   name: CORAL_BOOTSTRAP_TOKEN
                            valueFrom:
                                secretKeyRef:
                                    name: coral-bootstrap
                                    key: token
                        -   name: POD_NAME
                            valueFrom:
                                fieldRef:
                                    fieldPath: metadata.name
                        -   name: POD_NAMESPACE
                            valueFrom:
                                fieldRef:
                                    fieldPath: metadata.namespace
                    securityContext:
                        capabilities:
                            add: [ "SYS_PTRACE" ]  # Based on policy detection

            containers:
                -   name: app
                    image: myapp:latest
```

## Implementation Plan

### Phase 1: Foundation

- [ ] Create `coral inject` CLI command structure
- [ ] Implement Kubernetes YAML parser with comment preservation
- [ ] Define injection configuration schema
- [ ] Handle multi-document YAML files

### Phase 2: Policy Detection

- [ ] Implement PodSecurity policy detection via Kubernetes API
- [ ] Add cluster type heuristics (GKE Autopilot, EKS, AKS detection)
- [ ] Create policy override mechanism (`--policy` flag)
- [ ] Cache detection results per kubeconfig context

### Phase 3: Injection Logic

- [ ] Implement sidecar container injection
- [ ] Add `shareProcessNamespace: true` transformation
- [ ] Inject appropriate capabilities based on policy
- [ ] Add discovery service endpoint and colony ID configuration injection
- [ ] Inject required environment variables (discovery endpoint, colony ID, bootstrap token)
- [ ] Handle existing initContainers and volumes

### Phase 4: Validation & Safety

- [ ] Validate output against PodSecurity standards
- [ ] Detect and warn about unsupported workload types
- [ ] Check for injection conflicts (already injected, incompatible configs)
- [ ] Add dry-run mode
- [ ] Add diff mode (show changes without applying)

### Phase 5: kubectl Plugin

- [ ] Create kubectl-coral plugin binary
- [ ] Implement `kubectl coral apply` command
- [ ] Implement `kubectl coral create` command
- [ ] Add plugin to krew package repository

### Phase 6: Advanced Features

- [ ] Kustomize integration (detect and inject into kustomization.yaml)
- [ ] Helm integration (detect charts, provide guidance)
- [ ] GitOps workflow documentation
- [ ] Injection profiles (minimal, standard, full)
- [ ] Custom injection configuration file

### Phase 7: Testing & Documentation

- [ ] Unit tests for parser and transformer
- [ ] Integration tests against real clusters (kind, minikube)
- [ ] Test against GKE Autopilot, EKS, AKS
- [ ] CLI usage documentation
- [ ] GitOps workflow examples

## API Changes

### CLI Commands

#### `coral inject` - Transform Kubernetes Manifests

```bash
# Basic usage - inject and output to stdout
coral inject deployment.yaml

# Output to file
coral inject deployment.yaml -o deployment-coral.yaml

# Process multiple files
coral inject -f app/ -R -o manifests-coral/

# Dry run - show what would be injected
coral inject deployment.yaml --dry-run

# Show diff
coral inject deployment.yaml --diff

# Override policy detection
coral inject deployment.yaml --policy=baseline

# Specify colony ID
coral inject deployment.yaml --colony-id=prod-us-east

# Specify discovery service endpoint
coral inject deployment.yaml --discovery=https://discovery.coral.io

# Disable specific features
coral inject deployment.yaml --no-exec

# Use minimal profile (monitoring only)
coral inject deployment.yaml --profile=minimal

# Verbose output (show detection results)
coral inject deployment.yaml --verbose

# Example output:
---
# Coral agent injected
# Features enabled: monitoring, exec
# PodSecurity policy: baseline
# Discovery endpoint: https://discovery.coral.io
# Colony ID: prod-us-east
apiVersion: apps/v1
kind: Deployment
metadata:
  name: myapp
  annotations:
    coral.io/injected: "true"
...
```

#### `kubectl coral apply` - kubectl Plugin

```bash
# Apply with automatic injection
kubectl coral apply -f deployment.yaml

# Equivalent to: coral inject deployment.yaml | kubectl apply -f -

# Dry run
kubectl coral apply -f deployment.yaml --dry-run=client

# Show diff before applying
kubectl coral apply -f deployment.yaml --diff

# Apply with custom colony ID
kubectl coral apply -f deployment.yaml --colony-id=staging

# Example output:
Injecting Coral agent...
  Policy detected: baseline
  Features enabled: monitoring, exec
  Discovery endpoint: https://discovery.coral.io
  Colony ID: prod-us-east

deployment.apps/myapp created

Coral agent deployed. Monitor with: coral colony pods
```

### Configuration File

Users can create `.coral-inject.yaml` in their project:

```yaml
# .coral-inject.yaml
version: 1

# Discovery service configuration
discovery:
    endpoint: "https://discovery.coral.io"

# Colony configuration
colony:
    id: "prod-us-east"

# Feature configuration
features:
    exec: true
    ebpf: false  # Explicitly disable

# Injection mode
mode: auto  # auto | restricted | baseline | privileged

# Custom agent image
agent:
    image: "my-registry/coral-agent:v1.2.3"
    pullPolicy: IfNotPresent

# Additional environment variables
env:
    CORAL_LOG_LEVEL: "debug"

# Workload selectors (only inject matching workloads)
selector:
    labels:
        coral.io/monitor: "true"
```

### Injection Profiles

Three built-in profiles for common scenarios:

#### Profile: `minimal` (Restricted PodSecurity)

```yaml
# Service discovery only, no exec, no eBPF, no OTel
spec:
    shareProcessNamespace: true
    initContainers:
        -   name: coral-agent
            securityContext: { }  # No capabilities
```

**Features**:
- Service discovery within pod
- Basic connectivity monitoring
- AI context reporting to Colony

**NOT included**:
- ❌ exec/shell (requires CAP_SYS_PTRACE or CRI socket)
- ❌ eBPF introspection (requires CAP_BPF, CAP_NET_ADMIN)
- ❌ OpenTelemetry ingestion (use cluster-wide collector from RFD 025)

**Use case**: GKE Autopilot, maximum security

**Note**: For OpenTelemetry ingestion in Kubernetes, deploy a cluster-wide OTel
collector service instead of per-pod sidecars. See RFD 025 for OTel architecture.

#### Profile: `standard` (Baseline PodSecurity)

```yaml
# Monitoring + exec, no eBPF
spec:
    shareProcessNamespace: true
    initContainers:
        -   name: coral-agent
            securityContext:
                capabilities:
                    add: [ "SYS_PTRACE" ]
```

**Features**: Monitoring, exec, shell
**Use case**: Standard managed Kubernetes

#### Profile: `full` (Privileged PodSecurity)

```yaml
# All features enabled
spec:
    shareProcessNamespace: true
    initContainers:
        -   name: coral-agent
            securityContext:
                capabilities:
                    add: [ "SYS_PTRACE", "BPF", "NET_ADMIN", "PERFMON" ]
            volumeMounts:
                -   name: cri-socket
                    mountPath: /var/run/containerd/containerd.sock
    volumes:
        -   name: cri-socket
            hostPath:
                path: /var/run/containerd/containerd.sock
```

**Features**: Monitoring, exec, shell, eBPF introspection
**Use case**: Self-managed clusters, development

## Testing Strategy

### Unit Tests

**Parser Tests:**

- Parse single-document YAML
- Parse multi-document YAML
- Preserve comments and formatting
- Handle malformed YAML gracefully
- Detect workload type correctly

**Transformer Tests:**

- Inject sidecar into Deployment
- Inject sidecar into StatefulSet, DaemonSet, Job
- Handle existing initContainers
- Handle existing `shareProcessNamespace: true`
- Merge capabilities correctly
- Preserve existing pod annotations/labels

**Policy Detection Tests:**

- Mock Kubernetes API responses
- Test GKE Autopilot detection
- Test EKS detection
- Test fallback heuristics
- Test user override (`--policy` flag)

### Integration Tests

**Against Real Clusters:**

- Set up kind cluster with different PodSecurity modes
- Inject and deploy to Restricted namespace
- Inject and deploy to Baseline namespace
- Verify pod starts successfully
- Verify Coral agent connects to colony
- Verify features work as expected per policy

**Cluster-Specific Tests:**

- GKE Autopilot: Verify Restricted injection works
- EKS: Verify Baseline injection works
- Minikube: Verify Privileged injection works

**kubectl Plugin Tests:**

- Install plugin via krew
- Test `kubectl coral apply`
- Test `kubectl coral create`
- Verify plugin respects kubectl config

### E2E Tests

**Developer Workflow:**

1. User runs `coral inject deployment.yaml`
2. Output is valid YAML
3. User applies with `kubectl apply -f -`
4. Pod starts with Coral agent
5. User runs `coral exec myapp ps aux`
6. Exec works or fails gracefully based on policy

**GitOps Workflow:**

1. User commits injected manifests to git
2. ArgoCD/Flux deploys to cluster
3. Coral agents connect to colony
4. User verifies connectivity with `coral colony pods`

## Security Considerations

**No Privilege Escalation:**

- Tool uses user's existing kubectl credentials
- Cannot grant privileges user doesn't have
- Respects cluster PodSecurity policies
- Fails safely when policies reject configuration

**Transparency:**

- All changes visible in output YAML
- No hidden modifications
- Users can inspect before applying
- Annotations document injection version and features

**Injection Safety:**

- Detects already-injected workloads (idempotent)
- Warns on potential conflicts
- Validates output against PodSecurity before user applies
- Provides `--dry-run` and `--diff` modes

**Discovery Service & Colony Configuration:**

- Discovery service endpoint configurable (defaults to https://discovery.coral.io)
- Colony ID specified per environment
- Bootstrap token stored in Kubernetes secret (not in manifest)
- Discovery service provides colony mesh endpoint dynamically
- WireGuard mesh established via discovery service (RFD 001/023)

**Supply Chain:**

- Pin agent image versions in production
- Support custom registries for air-gapped environments
- Verify image signatures (future enhancement)

## Future Enhancements

**Smart Feature Detection:**

- Analyze application to recommend features (e.g., detect HTTP servers → enable
  http_latency eBPF)
- Suggest minimal profile based on workload characteristics

**Helm Integration:**

- Helm plugin for chart-level injection
- Values file generation for Coral configuration

**Policy Reporting:**

- Generate report of cluster capabilities
- Suggest policy changes to enable features
- Cost-benefit analysis of different policies

**IDE Integration:**

- VS Code extension for inline injection
- Preview injected YAML in editor
- Validation and autocomplete

**Template Support:**

- Inject into Helm templates (detect templates, warn user)
- Kustomize component for Coral injection
- Jsonnet library

## Appendix

### PodSecurity Standard Reference

**Restricted Policy:**

```yaml
# Allowed in Restricted
shareProcessNamespace: true  ✓
capabilities: [ ]             ✓
hostPath volumes: ✗
privileged: false            ✓ (required)
```

**Baseline Policy:**

```yaml
# Allowed in Baseline
shareProcessNamespace: true        ✓
capabilities: [ "SYS_PTRACE", ... ]  ✓ (specific caps only)
hostPath volumes: ✗
privileged: false                  ✓ (required)
```

**Privileged Policy:**

```yaml
# Allowed in Privileged
Everything                         ✓
```

### Detection Heuristics

When PodSecurity API is unavailable, use these heuristics:

```go
// Cluster type detection
func detectClusterType(clientset *kubernetes.Clientset) ClusterType {
    // GKE Autopilot: Check for autopilot.gke.io label on nodes
    // EKS: Check for eks.amazonaws.com label on nodes
    // AKS: Check for kubernetes.azure.com label on nodes
    // Default: Assume Baseline
}
```

### Supported Workload Types

- ✓ Deployment
- ✓ StatefulSet
- ✓ DaemonSet
- ✓ Job
- ✓ CronJob
- ✓ ReplicaSet (direct, not recommended)
- ✗ Pod (direct) - Manual sidecar required
- ⚠️ Helm Charts - Inject into rendered output
- ⚠️ Kustomize - Inject into base or overlay

---

## Relationship to OpenTelemetry Ingestion (RFD 025)

**Important**: This RFD (027) focuses on sidecar injection for **exec/shell
capabilities**. It does NOT cover OpenTelemetry ingestion.

### Why Separate?

**Per-pod sidecars (RFD 027)** are needed for:
- ✅ `coral exec` - Execute commands in target containers
- ✅ `coral shell` - Interactive shell in containers
- ✅ Process-level visibility (with `shareProcessNamespace`)

**Cluster-wide OTel collector (RFD 025)** is better for:
- ✅ OpenTelemetry traces/metrics ingestion
- ✅ Lower overhead (150MB total vs 50MB × N pods)
- ✅ High availability (3 replicas with load balancing)
- ✅ Centralized filtering and aggregation

### Deployment Recommendation

For a complete Coral deployment in Kubernetes:

1. **Deploy cluster-wide OTel collector** (RFD 025):
   ```bash
   kubectl apply -f coral-otel-collector.yaml
   ```
   Applications export to: `coral-otel.coral-system:4317`

2. **Inject sidecars ONLY for exec/shell** (RFD 027):
   ```bash
   coral inject deployment.yaml --profile=standard | kubectl apply -f -
   ```

This hybrid approach provides full Coral functionality with minimal overhead.

---

## Related RFDs

- RFD 012 - Kubernetes Node Agent (DaemonSet mode comparison)
- RFD 017 - Exec Command (Feature requiring injection)
- RFD 025 - Basic OpenTelemetry Ingestion (OTel architecture for K8s)
- RFD 026 - Shell Command (Feature requiring injection)
