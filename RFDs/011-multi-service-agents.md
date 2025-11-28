---
rfd: "011"
title: "Multi-Service Agent Support"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "002", "007" ]
related_rfds: [ "012" ]
database_migrations: [ ]
areas: [ "cli", "agent", "kubernetes", "deployment" ]
---

# RFD 011 - Multi-Service Agent Support

**Status:** 🎉 Implemented (Phases 1-4, 6)
**Note:** Phase 5 (Passive Discovery) expanded into **RFD 012 - Kubernetes Node Agent**

## Summary

Enhance the Coral agent to support monitoring multiple services within a single
agent process, enabling efficient Kubernetes sidecar deployments and reducing
resource overhead in multi-container environments. This maintains backward
compatibility with single-service agents while introducing a
`service:port[:health][:type]` syntax for explicit multi-service configuration.

**Note:** The passive discovery mode for DaemonSet/node-level agents has been
expanded into RFD 012, which builds on this foundation.

## Problem

**Current behavior/limitations:**

- `coral connect` creates one agent process per service
- Each agent establishes a separate WireGuard tunnel to the colony
- Multi-container Kubernetes pods require multiple agent sidecars or separate
  agents per container
- Resource overhead scales linearly: N services = N agents = N WireGuard
  tunnels = N gRPC connections
- No way to logically group co-located services (e.g., app + cache + metrics in
  same pod)

**Why this matters:**

- **Kubernetes inefficiency**: A pod with 3 containers (app, Redis, metrics)
  currently needs either:
    - 3 separate agent sidecars (wasteful: 3x memory, 3x network overhead)
    - 3 agents outside the pod (loses pod-level isolation and lifecycle
      coupling)
    - Manual workarounds that don't fit Coral's architecture

- **Resource waste**: Each agent process consumes ~10-20 MB memory, and each
  WireGuard tunnel adds protocol overhead. At scale (100+ pods), this becomes
  significant.

- **Operational complexity**: Managing many small agent processes is harder than
  managing fewer consolidated agents. Logs are scattered, metrics are
  fragmented.

- **Lost context**: Multiple agents for the same logical unit (pod) can't share
  context. A single agent watching all containers in a pod can correlate
  failures and provide better insights.

**Use cases affected:**

- **Kubernetes multi-container pods**: Primary use case. Pods commonly have main
  app + sidecar cache + metrics exporter.

- **VM co-location**: Multiple services running on the same VM (e.g., web
  server + background worker + Redis).

- **Development environments**: Developers running entire stack locally (
  frontend + API + database + queue) want single command to observe all
  services.

- **High-density deployments**: Hundreds of microservices with auxiliary
  containers create thousands of agent processes unnecessarily.

## Solution

Extend `coral connect` to accept multiple service specifications in a single
command, with each service specification using the format
`name:port[:health][:type]`. Complement this with a passive discovery mode that
allows privileged agents (e.g., Kubernetes DaemonSets) to auto-detect all
eligible services on a node. In both modes the agent operates as a single
process with one WireGuard tunnel but monitors multiple services concurrently.

**Key Design Decisions:**

- **Dual service enumeration modes**:
    - **Explicit mode**: Operators pass `service:port[:health][:type]` entries via CLI args or config files (sidecars, VMs, local dev).
    - **Passive mode**: Privileged agents (DaemonSet/node installations) auto-discover services using platform metadata (initial focus: Kubernetes pods and annotations).

- **Service specification syntax (explicit mode)**: `service:port[:health][:type]`
    - Required: service name and port number
    - Optional: health endpoint path (e.g., `/health`, `/metrics`)
    - Optional: service type hint (e.g., `http`, `redis`, `postgres`,
      `prometheus`)
    - Optional: inline labels via `#key=value,key2=value2`
    - Examples: `api:8080`, `frontend:3000:/health`, `redis:6379::redis`,
      `metrics:9090:/metrics:prometheus`, `checkout:8081/http#team=checkout`

- **Single agent, multiple services**: One agent process handles all specified
  or auto-discovered services
    - Single WireGuard tunnel (one mesh IP for the agent/pod)
    - Multiple concurrent health check loops (one per service)
    - Shared agent ID (e.g., `myapp-pod-xyz-agent`)
    - Services identified by component names in telemetry data

- **Backward compatibility**: Auto-detect single vs multi-service mode
    - `coral connect frontend --port 3000` → single-service mode (legacy)
    - `coral connect frontend:3000` → single-service mode (new syntax)
    - `coral connect frontend:3000 redis:6379` → multi-service mode (new)
    - Legacy flags (--port, --health-endpoint) supported when single service
      specified

- **Protocol extension**: `RegisterRequest` supports `repeated ServiceInfo`
  field
    - Existing single-service deployments send one ServiceInfo entry
    - Multi-service deployments send multiple ServiceInfo entries
    - Colony tracks services per agent in registry

- **Agent identity**: Single agent ID representing the pod/host
    - Agent appears once in colony registry
    - Services are properties of the agent
    - Simplifies colony UI/API (one entry, not N entries)
    - Health status tracked per-service within agent entry

**Benefits:**

- **Resource efficiency**: Kubernetes pod with 3 containers uses 1 agent instead
  of 3 (66% reduction in agent processes, WireGuard tunnels, gRPC connections)

- **Simplified deployment**: Single sidecar container in Kubernetes manifest,
  single command for local development

- **Better context**: Agent sees all containers in pod, can correlate failures (
  e.g., "Redis crashed before API failed")

- **Backward compatible**: Existing single-service usage patterns unchanged

- **Kubernetes-native**: Aligns with sidecar pattern and pod-level observability

**Architecture Overview:**

```
┌────────────────────────────────────────────────────────────┐
│  Kubernetes Pod: myapp-pod-xyz                             │
│                                                            │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐    │
│  │ Container:   │  │ Container:   │  │ Container:   │    │
│  │ frontend     │  │ redis        │  │ metrics      │    │
│  │ Port: 3000   │  │ Port: 6379   │  │ Port: 9090   │    │
│  └──────────────┘  └──────────────┘  └──────────────┘    │
│         │                  │                  │           │
│         └──────────────────┴──────────────────┘           │
│                            │                              │
│                            ▼                              │
│  ┌────────────────────────────────────────────────────┐   │
│  │  Coral Agent Sidecar (Single Process)             │   │
│  │                                                    │   │
│  │  Agent ID: myapp-pod-xyz-agent                     │   │
│  │  Mesh IP: 10.42.0.15 (single WireGuard tunnel)    │   │
│  │                                                    │   │
│  │  Services being monitored:                         │   │
│  │    - frontend:3000:/health:http                    │   │
│  │    - redis:6379::redis                             │   │
│  │    - metrics:9090:/metrics:prometheus              │   │
│  │                                                    │   │
│  │  Health Checks (concurrent loops):                 │   │
│  │    - Thread 1: Poll http://localhost:3000/health   │   │
│  │    - Thread 2: Redis PING localhost:6379           │   │
│  │    - Thread 3: Poll http://localhost:9090/metrics  │   │
│  └────────────────────────────────────────────────────┘   │
│                            │                              │
└────────────────────────────┼──────────────────────────────┘
                             │ WireGuard tunnel (encrypted)
                             ▼
                  ┌────────────────────────┐
                  │  Colony (10.42.0.1)    │
                  │                        │
                  │  Registry Entry:       │
                  │    agent_id: myapp...  │
                  │    mesh_ip: 10.42.0.15 │
                  │    services:           │
                  │      - frontend (3000) │
                  │      - redis (6379)    │
                  │      - metrics (9090)  │
                  └────────────────────────┘
```

### DaemonSet Passive Discovery Mode

For Kubernetes environments, a privileged Coral agent can run as a DaemonSet
and monitor every pod on its node without explicit service arguments:

```
┌────────────────────────────────────────────────────────────┐
│  Kubernetes Node (ip-10-0-1-23)                            │
│                                                            │
│  Pods:                                                     │
│    • payments-abc (payments-api, metrics)                  │
│    • redis-xyz (redis)                                     │
│    • checkout-123 (checkout-api)                           │
│                                                            │
│  ┌────────────────────────────────────────────────────┐    │
│  │ Coral Node Agent (DaemonSet)                       │    │
│  │  - Joins colony mesh once                          │    │
│  │  - Queries kubelet / API for pods + ports          │    │
│  │  - Applies allow/deny filters (labels/annotations) │    │
│  │  - Emits ServiceInfo for each discovered service   │    │
│  │  - Merges explicit overrides if provided           │    │
│  └────────────────────────────────────────────────────┘    │
└────────────────────────────────────────────────────────────┘
```

- Discovery pipeline gathers candidate services from Kubernetes (pod specs,
  annotations such as `coral.io/service` or `coral.io/disable`).
- Operators can mix auto-discovered services with explicit overrides (e.g., to
  add health endpoints or rename components) using config files.
- Audit logs and colony registry indicate whether a service was auto-discovered
  or explicitly configured.

### Component Changes

1. **CLI** (`internal/cli/agent/connect.go`):
    - Parse service specifications: `name:port[:health][:type]`
    - Validate service names (alphanumeric + hyphens)
    - Validate port numbers (1-65535)
    - Support optional per-service labels via `name:port#key=value,key2=value2`
      or `--label name.key=value` syntax
    - Support legacy flags (`--port`, `--health-endpoint`) when exactly one
      service specified
    - Auto-detect single vs multi-service mode based on argument count and
      syntax

2. **Protobuf** (`proto/coral/mesh/v1/mesh.proto`):
    - Add `ServiceInfo` message type
    - Extend `RegisterRequest` with `repeated ServiceInfo services`
    - Maintain backward compatibility by supporting both single service (legacy)
      and multi-service (new)

3. **Agent** (`internal/agent/agent.go`):
    - Accept list of service configurations on startup (explicit mode)
    - Add discovery providers (initial: Kubernetes node inspector) to gather
      services automatically in passive mode
    - Merge explicit configurations with discovered services (explicit overrides
      win)
    - Create concurrent health check goroutines (one per service)
    - Aggregate service health into single agent status
    - Report all services in `RegisterRequest`
    - Single WireGuard tunnel (no changes to networking layer)

4. **Colony Registry** (`internal/colony/registry/registry.go`):
    - Update `Entry` type to include `[]ServiceInfo` field
    - Store and track multiple services per agent
    - Expose per-service health status in status endpoints
    - Support querying by service name across agents

**Configuration Examples:**

**CLI Usage:**

```bash
# Single service (backward compatible)
coral connect frontend --port 3000

# Single service (new syntax)
coral connect frontend:3000

# Single service with health endpoint
coral connect frontend:3000:/health

# Multi-service (pod with 3 containers)
coral connect frontend:3000:/health redis:6379 metrics:9090:/metrics

# Multi-service with types for better observability
coral connect \
  frontend:3000:/health:http \
  redis:6379::redis \
  metrics:9090:/metrics:prometheus
```

**Kubernetes Manifest:**

```yaml
apiVersion: v1
kind: Pod
metadata:
    name: myapp
spec:
    containers:
        # Application containers
        -   name: frontend
            image: myapp/frontend:v2.0.0
            ports:
                -   containerPort: 3000

        -   name: redis
            image: redis:7
            ports:
                -   containerPort: 6379

        -   name: metrics
            image: prometheus/node-exporter
            ports:
                -   containerPort: 9090

        # Single Coral agent sidecar monitoring all 3 containers
        -   name: coral-agent
            image: coral/agent:latest
            args:
                - connect
                - frontend:3000:/health
                - redis:6379
                - metrics:9090:/metrics
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

**Helm Values:**

```yaml
coral:
    agent:
        enabled: true
        services:
            -   name: frontend
                port: 3000
                healthEndpoint: /health
                type: http

            -   name: redis
                port: 6379
                type: redis

            -   name: metrics
                port: 9090
                healthEndpoint: /metrics
                type: prometheus

# Generates sidecar container with args:
# ["connect", "frontend:3000:/health:http", "redis:6379::redis", "metrics:9090:/metrics:prometheus"]
```

**DaemonSet Auto-Discovery Config (Kubernetes):**

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
            containers:
                -   name: coral-agent
                    image: coral/agent:latest
                    securityContext:
                        privileged: true   # Required for host-level capture/tun
                        capabilities:
                            add: ["NET_ADMIN", "SYS_PTRACE"]
                    args:
                        - connect
                        - --mode=passive
                        - --discovery=kubernetes
                        - --include-label=coral.io/enabled=true
                        - --exclude-namespace=kube-system
                    volumeMounts:
                        - name: pod-info
                          mountPath: /var/run/secrets/kubernetes.io/serviceaccount
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
            volumes:
                - name: pod-info
                  projected:
                      sources:
                          - serviceAccountToken:
                                path: token
                                expirationSeconds: 3600
```

## API Changes

### New Protobuf Messages

**File: `proto/coral/mesh/v1/mesh.proto`**

```protobuf
syntax = "proto3";
package coral.mesh.v1;

import "google/protobuf/timestamp.proto";

option go_package = "github.com/coral-mesh/coral/proto/mesh/v1;meshpb";

// Service information for multi-service agents
message ServiceInfo {
    string component_name = 1;     // "frontend", "redis", "metrics"
    int32 port = 2;                // Service port number
    string health_endpoint = 3;    // Optional: "/health", "/metrics", ""
    string service_type = 4;       // Optional: "http", "redis", "postgres", "prometheus"
    map<string, string> labels = 5; // Additional metadata
}

// Agent registration request (updated for multi-service support)
message RegisterRequest {
    // Agent identification
    string agent_id = 1;

    // DEPRECATED: Legacy single-service field (for backward compatibility)
    // Use 'services' field for new deployments
    string component_name = 2 [deprecated = true];

    // NEW: Multi-service support
    repeated ServiceInfo services = 10;  // One or more services

    // Authentication (RFD 002)
    string colony_id = 3;
    string colony_secret = 4;

    // Agent metadata
    string version = 5;
    map<string, string> labels = 6;

    // WireGuard (RFD 007)
    string wireguard_pubkey = 7;
}

message RegisterResponse {
    // Authentication result
    bool accepted = 1;
    string reason = 2;

    // Mesh assignment (if accepted)
    string assigned_ip = 3;
    string mesh_subnet = 4;
    repeated PeerInfo peers = 5;

    // Registration metadata
    google.protobuf.Timestamp registered_at = 6;
}

message PeerInfo {
    string agent_id = 1;
    string mesh_ip = 2;
    string wireguard_pubkey = 3;
}
```

### CLI Commands

```bash
# Multi-service agent connection
coral connect <service-spec>... [flags]

# Service spec format: name:port[:health][:type]
# - name: Service/component name (alphanumeric + hyphens)
# - port: TCP port number (1-65535)
# - health: Optional health check endpoint path
# - type: Optional service type hint

# Legacy flags (supported when exactly one service specified):
#   --port <number>           Service port (deprecated, use service:port syntax)
#   --health-endpoint <path>  Health check path (deprecated, use service:port:health syntax)

# Examples:
coral connect frontend:3000
coral connect frontend:3000:/health
coral connect api:8080:/health:http redis:6379::redis
coral connect frontend:3000 api:8080 worker:9000 cache:6379

# Output example:
$ coral connect frontend:3000:/health redis:6379 metrics:9090

Connecting agent to colony...
✓ Discovered colony: my-app-production-a3f2e1
✓ Established WireGuard tunnel (mesh IP: 10.42.0.42)
✓ Registered with colony

Agent ID: my-app-pod-xyz-agent
Monitoring services:
  • frontend (port 3000, health: /health)
  • redis (port 6379)
  • metrics (port 9090)

Status: All services healthy
Press Ctrl+C to disconnect
```

### Colony Status Output

```bash
$ coral colony agents

Connected Agents:
  myapp-pod-xyz-agent (10.42.0.42) - Healthy
    Services:
      • frontend:3000 ✓ Healthy (last check: 2s ago)
      • redis:6379 ✓ Healthy (last check: 1s ago)
      • metrics:9090 ✓ Healthy (last check: 3s ago)

  api-pod-abc-agent (10.42.0.43) - Degraded
    Services:
      • api:8080 ✓ Healthy
      • cache:6379 ✗ Unhealthy (connection refused)
```

### Configuration File Support

**File: `.coral/agent-config.yaml`** (optional, for complex configurations)

```yaml
# Agent configuration for multi-service monitoring
agent:
    id: custom-agent-name  # Optional: override auto-generated ID
    mode: explicit         # explicit | passive

services:
    -   name: frontend
        port: 3000
        health:
            endpoint: /health
            interval: 10s
            timeout: 2s
        type: http
        labels:
            tier: frontend
            framework: react

    -   name: redis
        port: 6379
        type: redis
        health:
            interval: 5s
            timeout: 1s
        labels:
            tier: cache

    -   name: metrics
        port: 9090
        health:
            endpoint: /metrics
            interval: 15s
        type: prometheus

# Optional passive discovery overrides (only used when mode=passive)
discovery:
    kubernetes:
        includeNamespaces: ["payments", "checkout"]
        includeLabels:
            coral.io/enabled: "true"
        excludeLabels:
            coral.io/skip: "true"

overrides:
    -   selector:
            podLabel: app=payments-api
        service:
            name: payments-api
            port: 8080
            health:
                endpoint: /healthz
                interval: 5s
            type: http

# Usage: coral connect --config .coral/agent-config.yaml
```

## Implementation Plan

### Phase 1: Protocol Extension ✅ COMPLETED

- [x] Add `ServiceInfo` message to `proto/coral/mesh/v1/mesh.proto`
- [x] Add `repeated ServiceInfo services` field to `RegisterRequest`
- [x] Mark `component_name` field as deprecated (maintain for backward
  compatibility)
- [x] Run `buf generate` to regenerate protobuf code
- [x] Verify backward compatibility with existing single-service agents

### Phase 2: CLI Argument Parsing ✅ COMPLETED

- [x] Implement service spec parser:
  `parseServiceSpec(spec string) (ServiceInfo, error)`
- [x] Add validation: service name format, port range, health path syntax
- [x] Parse per-service label syntax and merge into `ServiceInfo.labels`
- [x] Support multiple service specs in command arguments
- [x] Maintain legacy flag support (--port, --health-endpoint) for
  single-service
- [x] Add unit tests for all parsing scenarios and edge cases

### Phase 3: Agent Multi-Service Support ✅ COMPLETED

- [x] Introduce service enumeration interface (explicit & passive providers)
- [x] Update agent initialization to accept list of service configurations
- [x] Implement concurrent health check loops (one goroutine per service)
- [x] Aggregate service health into overall agent status
- [x] Build `RegisterRequest` with multiple `ServiceInfo` entries
- [x] Handle partial failures gracefully (some services healthy, others not)

### Phase 4: Colony Registry Updates ✅ COMPLETED

- [x] Extend `registry.Entry` to include `[]ServiceInfo` field
- [x] Update agent registration handler to store multiple services
- [x] Implement per-service health tracking
- [x] Update status endpoints to expose service-level details
- [x] Add query support: find agents by service name

### Phase 5: Passive Discovery (Kubernetes) → **Superseded by RFD 012**

**Note:** This phase has been expanded into a dedicated RFD for better scope management.
See **RFD 012 - Kubernetes Node Agent (Passive Discovery DaemonSet)** for the complete
specification of passive discovery, node-level agents, and control operations.

Original scope (now in RFD 012):
- Kubernetes discovery provider (DaemonSet/node agent)
- Label/namespace filters and opt-in annotations
- Merge discovered services with explicit overrides
- Expose discovery provenance in colony registry/audit logs
- Document security requirements (privileged container, capabilities)

### Phase 6: Testing & Documentation ✅ COMPLETED

- [x] Unit tests: Service spec parsing and validation
- [x] Unit tests: Multi-service agent initialization
- [x] Integration test: Single-service backward compatibility
- [x] Integration test: Multi-service registration and health checks
- [ ] E2E test: Kubernetes pod with 3 containers + single agent sidecar (future)
- [ ] Update CLI help documentation (future)
- [ ] Add Kubernetes deployment examples to docs (future)

## Testing Strategy

### Unit Tests

- Service specification parsing:
    - Valid specs: `api:8080`, `redis:6379::redis`, `app:3000:/health:http`
    - Specs with labels: `api:8080#team=payments,tier=backend`
    - Invalid specs: `invalid:99999`, `no-port:`, `:8080`, `app:port`
    - Edge cases: empty strings, whitespace, special characters

- Agent initialization:
    - Single service configuration
    - Multiple service configurations (2, 5, 10 services)
    - Duplicate service names (should error)
    - Duplicate ports (should warn but allow)

- Registry operations:
    - Store agent with multiple services
    - Query agents by service name
    - Update service health status
    - Remove agent and all associated services

### Integration Tests

**Test 1: Backward Compatibility**

```bash
# Old syntax still works
coral connect frontend --port 3000 --health-endpoint /health

# Verify:
# - Agent registers with single service
# - Legacy RegisterRequest format accepted
# - Health checks work correctly
```

**Test 2: Multi-Service Registration**

```bash
# New multi-service syntax
coral connect frontend:3000:/health redis:6379 metrics:9090

# Verify:
# - Single agent process created
# - Single WireGuard tunnel established
# - All 3 services registered with colony
# - Colony registry shows 3 services under one agent
# - Health checks running for all 3 services
```

**Test 3: Service Health Aggregation**

```bash
# Start agent with 3 services
# Stop one service (e.g., Redis)

# Verify:
# - Agent status shows "Degraded" (not "Healthy", not "Unhealthy")
# - Specific service shows unhealthy
# - Other services still show healthy
# - Colony UI displays per-service status
```

### E2E Tests

**Scenario: Kubernetes Multi-Container Pod**

```yaml
# Apply manifest with 3-container pod + agent sidecar
kubectl apply -f test-pod.yaml

# Manifest contains:
# - frontend container (port 3000)
# - redis container (port 6379)
# - metrics container (port 9090)
# - coral-agent sidecar: connect frontend:3000 redis:6379 metrics:9090

# Verify:
# - Agent pod starts successfully
# - Agent registers with colony
# - All 3 services appear in colony status
# - Health checks pass for all services
# - Query colony: coral colony agents --filter service=redis
# - Result includes this agent
```

**Scenario: Configuration File**

```bash
# Create .coral/agent-config.yaml with 5 services
coral connect --config .coral/agent-config.yaml

# Verify:
# - All 5 services loaded from config
# - Custom health intervals respected
# - Labels propagated to colony
# - Service types recorded correctly
```

## Security Considerations

### Service Isolation

- **No change to security model**: Multi-service agents still use same
  authentication (colony_secret) and encryption (WireGuard) as single-service
  agents
- **Health check credentials**: Health endpoints assumed to be accessible on
  localhost without authentication (same as single-service mode)
- **Service type validation**: Service type field is optional metadata, not used
  for access control

### Attack Vectors

**Threat: Service name collision**

- **Scenario**: Malicious agent registers with service name matching legitimate
  service
- **Mitigation**: Colony tracks services by (agent_id, service_name) tuple, not
  just service_name
- **Recommendation**: Future enhancement: Warn if multiple agents register same
  service name

**Threat: Port exhaustion**

- **Scenario**: Agent claims to monitor thousands of services
- **Mitigation**: Add limit on services per agent (e.g., max 20 services)
- **Validation**: Enforce at CLI and colony registration handler

**Threat: Health check amplification**

- **Scenario**: Attacker uses Coral agent to DDoS service by configuring
  aggressive health checks
- **Mitigation**: Enforce minimum health check interval (e.g., 5 seconds)
- **Current state**: Health checks are against localhost only (not external
  targets)

## Migration Strategy

**Deployment approach:**

1. **Immediate backward compatibility**: Deploy protocol changes with both
   single and multi-service support
2. **No breaking changes**: Existing `coral connect frontend --port 3000`
   continues working
3. **Gradual adoption**: Teams can migrate to multi-service syntax at their own
   pace

**Migration examples:**

**Before (single-service per container):**

```yaml
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
                -   name: coral-agent-app
                    image: coral:latest
                    args: [ "connect", "app", "--port", "8080" ]

                -   name: redis
                    image: redis:7
                -   name: coral-agent-redis
                    image: coral:latest
                    args: [ "connect", "redis", "--port", "6379" ]
```

**After (multi-service single sidecar):**

```yaml
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

                -   name: redis
                    image: redis:7

                # Single agent for both services
                -   name: coral-agent
                    image: coral:latest
                    args: [ "connect", "app:8080", "redis:6379" ]
```

**Rollback plan:**

- If issues arise, revert to single-service sidecars
- No data migration needed (agents are stateless)
- Colony continues accepting both registration formats indefinitely

## Future Enhancements

### Configuration File Auto-Discovery

- Agent automatically finds `.coral/agent-config.yaml` in current directory
- Eliminates need for `--config` flag in common case
- Similar to how Git finds `.git/config`

### Service Dependency Tracking

- Declare service dependencies: `frontend depends on redis`
- Agent delays frontend health checks until redis is healthy
- Prevents false alarms during pod startup

### Service-Level Labels and Metadata

- Attach labels per service (not just per agent)
- Query colony: "show all Redis instances" across all agents
- Enable service-centric views in dashboard

### Dynamic Service Discovery

- Agent watches localhost for new services starting on ports
- Auto-registers newly discovered services with colony
- Use case: Development environments with dynamic service startup

### Health Check Plugins

- Support custom health check logic per service type
- Built-in: HTTP, gRPC, Redis PING, Postgres query, TCP connect
- Extensible: User-defined health check scripts

### Kubernetes Node Agent and Passive Discovery

**See RFD 012 - Kubernetes Node Agent (Passive Discovery DaemonSet)**

RFD 012 expands on the passive discovery concepts sketched in this RFD's Phase 5,
adding:
- DaemonSet-based node agents for cluster-wide coverage
- Automatic service discovery via Kubernetes API
- Service provenance tracking (explicit vs discovered vs overridden)
- Control operations (profiling, packet capture, traffic sampling)
- Advanced features: garbage collection, watch reconnects, audit logging

RFD 012 builds on the multi-service agent foundation established in this RFD and
provides a production-ready specification for zero-config Kubernetes observability.

## Appendix

### Service Specification Grammar

**Format:** `<name>:<port>[:<health>][:<type>]`

**EBNF Grammar:**

```ebnf
service-spec = name ":" port [ ":" health ] [ ":" type ]
name         = alpha (alphanum | "-")*
port         = digit{1,5}  (* 1-65535 *)
health       = "/" path-segment ("/" path-segment)*
type         = alpha alphanum*
alpha        = "a-zA-Z"
alphanum     = "a-zA-Z0-9"
digit        = "0-9"
path-segment = (alphanum | "-" | "_" | ".")+
```

**Valid Examples:**

- `api:8080`
- `frontend:3000:/health`
- `redis:6379::redis`
- `metrics:9090:/metrics:prometheus`
- `app:8080:/api/v1/health:http`

**Invalid Examples:**

- `api` (missing port)
- `:8080` (missing name)
- `api:99999` (port out of range)
- `api:port` (port not numeric)
- `my service:8080` (space in name)

### Comparison: Single-Service vs Multi-Service

| Aspect                       | Single-Service                          | Multi-Service                   |
|------------------------------|-----------------------------------------|---------------------------------|
| **Agent processes**          | N services = N processes                | N services = 1 process          |
| **WireGuard tunnels**        | N tunnels                               | 1 tunnel                        |
| **Mesh IPs**                 | N IPs allocated                         | 1 IP allocated                  |
| **Colony registry entries**  | N entries                               | 1 entry (with N services)       |
| **Memory overhead**          | ~15 MB × N                              | ~15 MB + (1 MB × N)             |
| **Health check parallelism** | N processes                             | N goroutines (cheaper)          |
| **Deployment complexity**    | N sidecars in K8s pod                   | 1 sidecar in K8s pod            |
| **Failure isolation**        | One agent crash = one service           | One agent crash = all services  |
| **Use case**                 | Independent services, maximum isolation | Co-located services, efficiency |

### Reference Implementations

**Kubernetes Patterns:**

- **Sidecar**: Standard pattern for auxiliary containers (logging, proxying,
  monitoring)
- **Ambassador**: Similar pattern used by Linkerd, Istio for service mesh
- **Adapter**: Pattern for normalizing interfaces (e.g., metrics aggregation)

**Similar Multi-Service Monitors:**

- **Prometheus Node Exporter**: Exposes multiple system metrics from single
  exporter
- **Datadog Agent**: Monitors multiple services on a host with single agent
  process
- **Telegraf**: Collects metrics from multiple inputs with single agent

### Example: Helm Chart Integration

```yaml
# values.yaml
coral:
    agent:
        enabled: true
        image: coral/agent:latest

        # Multi-service configuration
        services:
            -   name: app
                port: 8080
                health: /health
                type: http

            -   name: redis
                port: 6379
                type: redis

# templates/deployment.yaml
    { { - if .Values.coral.agent.enabled } }
containers:
    -   name: coral-agent
        image: { { .Values.coral.agent.image } }
        args:
            - connect
        { { - range .Values.coral.agent.services } }
        - { { .name } }:{{ .port }}{{- with .health }}:{{ . }}{{- end }}{{- with .type }}:{{ . }}{{- end }}
        {{- end }}
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
    { { - end } }
```

---

## Notes

**Design Philosophy:**

- **Efficiency without complexity**: Multi-service support is natural extension,
  not architectural change
- **Kubernetes-first**: Designed primarily for multi-container pod pattern
- **Backward compatible**: Zero breaking changes, adoption is optional
- **Progressive enhancement**: Start with single service, add more as needed

**Relationship to Other RFDs:**

- **RFD 002 (Identity)**: Agent identity unchanged, services are properties
- **RFD 007 (WireGuard)**: Network layer unchanged, still single tunnel per
  agent
- **RFD 012 (Kubernetes Node Agent)**: Builds on this RFD's multi-service
  foundation to enable passive discovery and node-level consolidation in
  Kubernetes environments

**Why This Matters:**

- Kubernetes adoption is primary driver for Coral growth
- Multi-container pods are standard practice (sidecar pattern)
- Competitors (Datadog, New Relic) all support multi-service agents
- Resource efficiency becomes critical at scale (hundreds of pods)
