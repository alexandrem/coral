---
rfd: "053"
title: "Beyla Dynamic Service Discovery Integration"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "032", "033" ]
database_migrations: [ ]
areas: [ "observability", "ebpf", "service-discovery" ]
---

# RFD 053 - Beyla Dynamic Service Discovery Integration

**Status:** 🚧 Draft

## Summary

Enable automatic synchronization between Coral's service discovery mechanisms
(`coral connect`, static configuration) and Beyla's eBPF instrumentation
configuration. When services are connected to an agent, Beyla should
automatically instrument them for RED metrics and distributed tracing without
manual reconfiguration or agent restart.

## Problem

**Current behavior/limitations**

- Services connected via `coral connect frontend:3000` are added to the agent's
  service registry and health monitoring, but Beyla is NOT notified about these
  new services.
- Beyla's discovery configuration (`Discovery.OpenPorts`) is set at agent
  startup and never updated dynamically.
- Current workaround uses a catch-all discovery rule (`open_ports: "1-65535"`)
  which instruments ALL listening processes, causing unnecessary overhead and
  noise.
- Static configuration in YAML requires knowing all service ports ahead of time,
  which doesn't match dynamic deployment patterns (containers, autoscaling).

**Why this matters**

- **Incomplete observability**: Services are monitored for health (TCP/HTTP
  checks) but lack RED metrics and distributed traces, defeating Coral's
  zero-configuration observability promise.
- **Resource waste**: The catch-all workaround instruments irrelevant processes
  (SSH, system services), increasing eBPF overhead and metric cardinality.
- **Operational friction**: Users expect `coral connect` to provide full
  observability, not just basic health checks. Currently, they must manually
  configure Beyla or restart the agent.
- **Dynamic environments**: Kubernetes, Docker Compose, and autoscaling
  environments add/remove services frequently. Static port lists don't scale.

**Use cases affected**

- `coral connect <service>:<port>` should immediately enable eBPF instrumentation
- Dynamic service registration via gRPC `ConnectService` RPC
- Container orchestration adding new service instances
- AI queries like "Why is frontend slow?" fail without RED metrics from
  dynamically connected services

## Solution

Implement bidirectional integration between Coral's service registry and Beyla's
discovery configuration:

1. **Startup sync**: Pass known service ports to Beyla at agent initialization
2. **Dynamic sync**: Update Beyla configuration when services are connected or
   disconnected
3. **Graceful reload**: Restart Beyla with updated configuration without
   disrupting in-flight metrics collection

### Architecture Overview

```
                         coral connect frontend:3000
                                    │
                                    ▼
                         ┌──────────────────┐
                         │   Agent Server   │
                         │  (gRPC Handler)  │
                         └────────┬─────────┘
                                  │
                    ConnectService() RPC
                                  │
            ┌─────────────────────┼─────────────────────┐
            │                     │                     │
            ▼                     ▼                     ▼
   ┌─────────────────┐   ┌─────────────────┐   ┌─────────────────┐
   │ Service Monitor │   │ Service Registry│   │  Beyla Manager  │
   │ (Health Checks) │   │   (In-Memory)   │   │  (eBPF Metrics) │
   └─────────────────┘   └─────────────────┘   └────────┬────────┘
                                                        │
                                              UpdateDiscovery()
                                                        │
                                                        ▼
                                               ┌─────────────────┐
                                               │  Beyla Process  │
                                               │ (Restart w/new  │
                                               │    config)      │
                                               └─────────────────┘
```

### Key Design Decisions

**1. Configuration-based reload (not runtime API)**

Beyla does not support runtime configuration updates. The only way to change
discovery rules is to restart Beyla with a new configuration file. This is
acceptable because:

- Beyla startup is fast (~1-2 seconds)
- Metrics collection resumes immediately after restart
- Service connects are infrequent compared to request traffic

**2. Debounced restarts**

Multiple rapid `ConnectService` calls (e.g., during deployment) should not
trigger multiple Beyla restarts. Implement a debounce window (e.g., 5 seconds)
to batch configuration changes.

**3. Port-based discovery (not process name)**

Use `open_ports` as the primary discovery method because:

- Port numbers are explicitly provided by users via `coral connect`
- Process names can be ambiguous (multiple services using same runtime)
- Port-based rules are more predictable and debuggable

**4. Graceful degradation**

If Beyla restart fails, continue with existing instrumentation. Log the error
and retry on next configuration change. Never leave the agent without any
Beyla coverage.

### Component Changes

1. **Beyla Manager** (`internal/agent/beyla/manager.go`):

   - Add `UpdateDiscovery(ports []int)` method to update port list
   - Implement debounced Beyla restart logic
   - Track configured ports separately from running Beyla state
   - Add metrics for restart count and configuration sync latency

2. **Agent Server** (`internal/agent/server/server.go`):

   - Modify `ConnectService` handler to notify Beyla manager of new ports
   - Modify `DisconnectService` handler to remove ports from Beyla

3. **Agent Startup** (`internal/cli/agent/start.go`):

   - Extract ports from initial `serviceInfos` and pass to `BeylaConfig`
   - Ensure Beyla starts with correct ports from static configuration

**Configuration Example:**

```yaml
# coral.yaml - Static service configuration
agent:
  services:
    - name: api
      port: 8080
    - name: frontend
      port: 3000
  beyla:
    enabled: true
    # Discovery.OpenPorts is automatically populated from services[].port
    # No need to duplicate port configuration
```

## Implementation Plan

### Phase 1: Startup Port Synchronization

- [ ] Modify `start.go` to extract ports from `serviceInfos`
- [ ] Pass extracted ports to `BeylaConfig.Discovery.OpenPorts`
- [ ] Remove catch-all `1-65535` fallback when specific ports are available
- [ ] Add tests for port extraction logic

### Phase 2: Dynamic Discovery Updates

- [ ] Add `UpdateDiscovery(ports []int)` method to Beyla Manager
- [ ] Implement debounced restart with configurable window (default: 5s)
- [ ] Add graceful shutdown of old Beyla process before starting new one
- [ ] Track port set changes to avoid unnecessary restarts

### Phase 3: Agent Integration

- [ ] Modify `ConnectService` RPC handler to call `UpdateDiscovery`
- [ ] Modify `DisconnectService` RPC handler to remove ports
- [ ] Add Beyla manager reference to agent server
- [ ] Handle concurrent service connects safely

### Phase 4: Testing & Observability

- [ ] Add unit tests for debounce logic
- [ ] Add integration tests for connect → Beyla restart flow
- [ ] Add metrics: `beyla_restarts_total`, `beyla_discovery_ports_count`
- [ ] Add debug logging for configuration changes

## API Changes

### Modified Beyla Manager Interface

```go
// Manager handles Beyla lifecycle within Coral agent.
type Manager interface {
    // Start initializes and starts Beyla with current configuration.
    Start() error

    // Stop gracefully shuts down Beyla.
    Stop() error

    // UpdateDiscovery updates the port discovery configuration.
    // If ports differ from current config, triggers debounced Beyla restart.
    // Thread-safe: can be called concurrently from multiple goroutines.
    UpdateDiscovery(ports []int) error

    // GetDiscoveryPorts returns the currently configured discovery ports.
    GetDiscoveryPorts() []int
}
```

### Modified ConnectService Flow

```protobuf
// Existing RPC - no proto changes needed
service AgentService {
    rpc ConnectService(ConnectServiceRequest) returns (ConnectServiceResponse);
}
```

The handler implementation changes to:

1. Add service to registry (existing)
2. Start health monitor (existing)
3. **NEW**: Notify Beyla manager of new port

### CLI Commands

No CLI changes required. Existing `coral connect` command automatically
benefits from this integration:

```bash
# Before: Service monitored but not instrumented
$ coral connect frontend:3000
Connected service frontend on port 3000
Health monitoring: enabled
eBPF metrics: NOT AVAILABLE  # <-- Problem

# After: Full observability
$ coral connect frontend:3000
Connected service frontend on port 3000
Health monitoring: enabled
eBPF metrics: enabled (Beyla restart scheduled)
```

## Testing Strategy

### Unit Tests

- Port set diff detection (add/remove/unchanged)
- Debounce timer behavior (single restart for rapid changes)
- Configuration file generation with dynamic ports
- Graceful handling of Beyla restart failures

### Integration Tests

- Connect service → verify Beyla config file updated
- Connect multiple services rapidly → verify single restart
- Disconnect service → verify port removed from config
- Agent restart → verify ports restored from registry

## Security Considerations

- No new security concerns. Port discovery is local to the agent.
- Beyla already runs with elevated privileges (CAP_SYS_ADMIN for eBPF).
- Configuration file is written to temp directory with restricted permissions.

## Future Enhancements

**Process name discovery** (deferred)

Allow services to specify process name patterns for discovery:

```bash
coral connect --exe-pattern "python.*gunicorn" api:8080
```

**Kubernetes label discovery** (RFD 033 dependency)

When running as DaemonSet, discover services via pod labels instead of ports:

```yaml
beyla:
  discovery:
    k8s_namespace: production
    k8s_pod_labels:
      app.kubernetes.io/monitored: "true"
```

**Hot reload without restart** (blocked by upstream Beyla)

If Beyla adds support for SIGHUP-based config reload, implement hot reload
instead of process restart. Track upstream issue for this feature.

---

## Implementation Status

**Core Capability:** ⏳ Not Started

Current state: Beyla runs with catch-all discovery (`open_ports: "1-65535"`)
which instruments all listening processes. Service-specific discovery is not
yet integrated.

## Deferred Features

**Kubernetes-native discovery** (Future - depends on RFD 033)

- Pod label-based service discovery
- Namespace filtering
- Service mesh integration (Istio, Linkerd)

**Multi-runtime support** (Future)

- Process name patterns for interpreted languages
- Container runtime detection
- Serverless function instrumentation
