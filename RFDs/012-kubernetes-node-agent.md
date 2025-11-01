---
rfd: "012"
title: "Kubernetes Node Agent (Passive Discovery DaemonSet)"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: ["002", "007", "011"]
database_migrations: []
areas: ["kubernetes", "agent", "networking", "observability"]
---

# RFD 012 - Kubernetes Node Agent (Passive Discovery DaemonSet)

**Status:** 🚧 Draft

## Summary

Ship a privileged Coral agent that runs as a Kubernetes DaemonSet and
automatically discovers services on each node, feeding them into the multi-service
agent framework (**RFD 011**). This enables zero-config observability and
operations—profiling, packet capture, traffic sampling—across entire clusters
while keeping all telemetry inside the application intelligence mesh.

**Note:** This RFD expands on RFD 011's Phase 5 (Passive Discovery), adding service
provenance tracking, control operations, and production hardening features.

## Problem

**Current behavior/limitations**

- Coral only monitors workloads where an operator explicitly deploys a sidecar
  (`coral connect ...`) or runs an agent manually on the host.
- Pods without Coral sidecars—legacy workloads, third-party charts, batch jobs,
  DaemonSets—stay invisible to the colony registry, limiting observability and
  control.
- Operators must continuously update manifests when new services spin up,
  leading to drift and blind spots.
- Remote operations (profiling, tap, packet capture) are impossible on nodes
  lacking an agent, undermining the “application intelligence mesh” story.

**Why this matters**

- Platform/SRE teams want cluster-wide coverage without touching every workload.
- Short-lived pods and horizontally autoscaled services need automatic discovery
  to maintain accurate topology and health status.
- Compliance and audit trails rely on the colony’s registry being complete and
  authoritative.

**Use cases affected**

- Monitoring and debugging third-party charts or workloads outside operator
  control.
- Incident response that requires packet inspection or profiling on pods that do
  not ship with Coral.
- Fleet-wide health aggregation (Reef, RFD 003) that assumes consistent colony
  data.

## Solution

Deploy a Coral node agent as a Kubernetes DaemonSet that joins the colony mesh
once per node and enumerates eligible services via the Kubernetes API/Kubelet.
It emits `ServiceInfo` entries (RFD 011) for all discovered workloads, supports
override annotations, and exposes control endpoints for remote operations.

### Key Design Decisions

- **DaemonSet footprint**: One agent per node for uniform coverage; leverages
  host networking and privileged permissions to observe any pod.
- **Passive discovery first, explicit overrides optional**: Label/namespace
  filters and per-pod annotations determine which services register; operators
  can override metadata (component name, health endpoints) without sidecars.
- **Shared multi-service protocol**: Reuse `ServiceInfo` and mesh registration
  changes from RFD 011 to avoid divergent code paths.
- **Observable provenance**: Each service logs whether it was discovered,
  overridden, or explicitly configured, supporting audits and debugging.
- **Support on-demand operations**: Same agent handles packet capture, profiling,
  and other control-plane requests initiated via `coral tap`, MCP, or automation.

### Benefits

- Cluster-wide coverage with a single deployment, no per-pod manifests to
  maintain.
- Immediate visibility for new workloads, autoscaled replicas, and ephemeral
  jobs.
- Unified control surface for profiling/traffic introspection across nodes,
  including air-gapped or hybrid clusters.
- Richer data for Reef and AI insights: complete inventory, provenance-aware
  telemetry, actionable audit logs.

### Architecture Overview

```
┌───────────────────────────────────────────────────────────────────┐
│ Kubernetes Cluster                                                │
│                                                                   │
│ Node ip-10-0-1-23                                                 │
│ ┌───────────────────────────────────────────────────────────────┐ │
│ │ Coral Node Agent (DaemonSet)                                   │ │
│ │  • Joins colony mesh (WireGuard)                               │ │
│ │  • Watches kubelet/API for pods                                │ │
│ │  • Applies include/exclude filters                             │ │
│ │  • Emits ServiceInfo (RFD 011) for matches                     │ │
│ │  • Executes tap/profile requests                               │ │
│ └───────────────────────────────────────────────────────────────┘ │
│    │             │             │                                   │
│    ▼             ▼             ▼                                   │
│ Pod A (app)   Pod B (redis)  Pod C (metrics)   …                   │
└───────────────────────────────────────────────────────────────────┘
            │
            ▼
┌────────────────────────────┐
│ Colony (10.42.0.1)         │
│  • Registry (agent+services)│
│  • Audit log (discovery ops)│
│  • Control plane handlers   │
└────────────────────────────┘
```

### Component Changes

1. **Agent** (`internal/agent/node` – new package):
   - Run as privileged DaemonSet; mount service account token.
   - Connect to colony once per node; maintain single WireGuard tunnel.
   - Implement discovery providers (Kubernetes API/kubelet) with label,
     namespace, and annotation filters.
   - Merge auto-discovered services with explicit overrides (config file).
   - Expose RPC endpoints for control actions (tap, profile, packet capture).

2. **CLI** (`internal/cli/agent`):
   - Add `coral agent node` (alias to `connect --mode=passive`) for local tests.
   - Support passive-mode flags (`--include-label`, `--exclude-namespace`, etc.).

3. **Colony Registry** (`internal/colony/registry`):
   - Store per-service provenance and node metadata.
   - Surface node agent entries separately from pod sidecars.
   - Update status APIs and UI to group services under node agents.

4. **Protocol** (`proto/coral/mesh/v1/mesh.proto`):
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
    includeNamespaces: ["payments", "checkout"]
    includeLabels:
      coral.io/enabled: "true"
    excludeNamespaces: ["kube-system", "istio-system"]
    pollInterval: 30s

overrides:
  - selector:
      podLabel: app=payments-api
    service:
      name: payments-api
      health:
        endpoint: /healthz
        interval: 5s
      type: http
```

## Implementation Plan

### Phase 1: Protocol & Registry Foundation

- [ ] Add `ServiceInfo.provenance` (enum) and optional `node_name`, `node_labels`.
- [ ] Extend `RegisterRequest` with node metadata fields.
- [ ] Update colony registry/storage to persist provenance & node info.
- [ ] Regenerate protobuf/go code; ensure single-service agents remain compatible.

### Phase 2: Discovery Engine

- [ ] Implement Kubernetes discovery provider (list/watch pods via client-go).
- [ ] Parse container ports, labels, annotations into `ServiceInfo`.
- [ ] Support filters (namespace, include/exclude labels) and annotation overrides.
- [ ] Handle watch reconnects and periodic full resyncs.

### Phase 3: Node Agent Runtime

- [ ] Create `coral-agent` DaemonSet container with required capabilities
      (`NET_ADMIN`, `SYS_PTRACE`, hostPID optional).
- [ ] Integrate discovery engine with multi-service agent core (RFD 011).
- [ ] Merge auto-discovered services with explicit config overrides (explicit wins).
- [ ] Implement garbage collection for stale services when pods terminate.
- [ ] Persist last-known services locally to reduce churn on restart.

### Phase 4: Control Operations

- [ ] Add RPC plumbing for `tap`, profiling, traffic sampling requests routed to
      node agents.
- [ ] Enforce guardrails (duration limits, rate limiting, resource ceilings).
- [ ] Stream artefacts/results to colony, include provenance metadata.

### Phase 5: Security & Hardening

- [ ] Document required RBAC roles/service accounts; implement scoped permissions.
- [ ] Offer “observe-only” mode (no ptrace/profiler) for restricted environments.
- [ ] Log discovery events and control actions in colony audit log.
- [ ] Add rate limiting/backoff for repeated discovery errors.

### Phase 6: Testing & Documentation

- [ ] Unit tests: filter evaluation, override precedence, provenance tagging.
- [ ] Integration tests: fake Kubernetes API + service churn.
- [ ] E2E tests: KinD cluster with DaemonSet, autoscaling workloads, control ops.
- [ ] Performance benchmarks: memory/CPU overhead on busy nodes.
- [ ] Documentation updates: README/USAGE, Helm chart values, security guide.

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

- **Privileges**: Document required capabilities (`NET_ADMIN`, `SYS_PTRACE`,
  hostPID) and provide guidance for running in observe-only mode when profiling
  is disallowed.
- **RBAC**: Service account needs `list/watch` on Pods, Nodes; least privilege
  roles must be defined.
- **Isolation**: Ensure agent only accesses namespaces permitted by filters;
  optionally support deny-by-default with explicit allow lists.
- **Auditability**: Log discovery events and control operations with node,
  namespace, service, provenance, and operator identity.
- **Data exposure**: All telemetry remains within WireGuard mesh; optional
  encryption at rest handled by colony storage (DuckDB).

## Future Enhancements

- Support additional discovery sources (e.g., container runtime socket, Istio
  service registry) for clusters with restricted API access.
- Dynamic per-namespace quotas to prevent runaway taps/profiles.
- Integration with Reef to summarize node-level anomalies and surface them in
  federated views.
- Automatic suggestion of observe-only vs full mode based on cluster policies.

## Notes

**Relationship to RFD 011:**

This RFD builds directly on **RFD 011 - Multi-Service Agent Support**, which
established the foundational protocol and agent runtime for monitoring multiple
services. Specifically:

- **Protocol foundation**: Uses the `ServiceInfo` message and
  `repeated ServiceInfo services` field from RFD 011
- **Agent runtime**: Leverages the concurrent health checking and status
  aggregation logic implemented in RFD 011
- **Backward compatibility**: Maintains compatibility with explicit
  (sidecar-based) service specifications from RFD 011

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
        - name: coral-agent
          image: ghcr.io/coral-io/agent:latest
          args:
            - agent
            - node
            - --mode=passive
            - --include-label=coral.io/enabled=true
          securityContext:
            privileged: true
            capabilities:
              add: ["NET_ADMIN"]
          env:
            - name: CORAL_COLONY_ID
              valueFrom:
                secretKeyRef:
                  name: coral-secrets
                  key: colony-id
            - name: CORAL_COLONY_SECRET
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
