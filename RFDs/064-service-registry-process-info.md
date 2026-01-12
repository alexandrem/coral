---
rfd: "064"
title: "Service Registry Process Information"
state: "completed"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "059", "060", "061" ]
database_migrations: [ ]
areas: [ "agent", "colony", "debugging", "ebpf" ]
---

# RFD 064 - Service Registry Process Information

**Status:** 🎉 Implemented

**Note:** This RFD should be implemented **before RFD 065** (Agentless Binary
Scanning) and **RFD 066** (SDK HTTP API), as both depend on `process_id` and
`binary_path` being tracked in the service registry. This RFD establishes these
fields as standard service registry metadata.

## Summary

Enhance the service registry and service status to include process ID (PID) and
binary path information. This enables improved eBPF debugging capabilities by
providing direct access to the process metadata required for uprobe attachment,
binary analysis, and symbol resolution.

## Problem

The current service registry tracks services by name, port, and health status,
but lacks critical process-level metadata needed for advanced debugging
operations:

**Current limitations:**

- **No PID tracking**: When the Colony or CLI needs to debug a service, there's
  no direct way to identify which process is running it without additional
  queries or heuristics.
- **Missing binary path**: eBPF uprobe debugging (RFD 059-061) requires knowing
  the exact binary path to:
    - Parse DWARF symbols for function offsets
    - Attach uprobes to the correct executable
    - Validate binary compatibility with debug sessions
- **Inefficient discovery**: The agent must perform additional process discovery
  steps when initiating debug sessions, adding latency.
- **Inconsistent state**: The PID and binary path are collected during SDK
  registration (ServiceSdkCapabilities) but not exposed in standard service
  listings.

**Why this matters:**

1. **eBPF debugging efficiency**: Reduces overhead when attaching uprobes by
   having process metadata readily available.
2. **Better observability**: Enables users to see which binary version is
   running for each service.
3. **Process lifecycle tracking**: Allows detection of service restarts (PID
   changes) and binary updates.
4. **Simplified AI debugging**: LLM-driven debugging (RFD 062) can make better
   decisions with complete process context.

**Use cases affected:**

- `coral debug attach <service>` - Currently must query agent for PID before
  attaching uprobes
- `coral agent status` - Shows services but not their process information
- MCP debugging tools - Need PID for process inspection commands
- Service topology visualization - Can't show process-level relationships

## Solution

Extend `ServiceInfo` and `ServiceStatus` protobuf messages to include optional
PID and binary path fields. This information will be populated by agents during
service discovery and registration, and exposed through all service listing
APIs.

**Key Design Decisions:**

1. **Optional fields**: PID and binary path are optional since not all services
   may have this information (e.g., external services, non-SDK services).
2. **Agent-populated**: The agent is responsible for discovering and updating
   this information during service monitoring.
3. **SDK integration**: For SDK-enabled services, this information comes from
   `ServiceSdkCapabilities` (RFD 060).
4. **Non-SDK services**: For services without SDK integration, the agent can
   discover PID through process inspection (e.g., parsing `/proc`, netstat).

**Benefits:**

- **Lower debug latency**: PID and binary path are immediately available without
  additional agent queries.
- **Better UX**: Users can see complete service information including process
  details in `coral agent status`.
- **Simplified code**: Removes need for separate PID discovery logic in debug
  orchestrator.
- **Foundation for future features**: Enables process-level metrics correlation,
  binary version tracking, and automatic debug session recovery after restarts.

### Component Changes

1. **Protobuf (mesh/v1/auth.proto)**:
    - Add `process_id` and `binary_path` fields to `ServiceInfo`

2. **Protobuf (agent/v1/agent.proto)**:
    - Add `process_id` and `binary_path` fields to `ServiceStatus`

3. **Agent (internal/agent)**:
    - Update service monitor to track and report PID and binary path
    - Integrate with SDK capabilities (ServiceSdkCapabilities)
    - Implement process discovery for non-SDK services

4. **Colony (internal/colony)**:
    - Store PID and binary path in service registry
    - Update debug orchestrator to use PID directly from service info
    - Expose in MCP tools and API responses

5. **CLI (internal/cli)**:
    - Display PID and binary path in `coral agent status`
    - Display PID and binary path in `coral debug tree`
    - Use in debug attach commands

## API Changes

### Protobuf Messages

```protobuf
// proto/coral/mesh/v1/auth.proto

message ServiceInfo {
    string name = 1;
    int32 port = 2;
    string health_endpoint = 3;
    string service_type = 4;
    map<string, string> labels = 5;

    // Process information (RFD 066).
    int32 process_id = 6;        // Process ID running the service (0 if unknown)
    string binary_path = 7;      // Path to service executable (empty if unknown)
    string binary_hash = 8;      // Hash of binary for cache invalidation (optional)
}
```

```protobuf
// proto/coral/agent/v1/agent.proto

message ServiceStatus {
    string name = 1;
    int32 port = 2;
    string health_endpoint = 3;
    string service_type = 4;
    map<string, string> labels = 5;
    string status = 6;
    google.protobuf.Timestamp last_check = 7;
    string error = 8;

    // Process information (RFD 066).
    int32 process_id = 9;        // Process ID running the service (0 if unknown)
    string binary_path = 10;     // Path to service executable (empty if unknown)
    string binary_hash = 11;     // Hash of binary for cache invalidation (optional)
}
```

### CLI Commands

```bash
# Enhanced agent status showing process information
coral agent status

# Example output:
Agent: agent-xyz (Connected)

Services:
  payment-service
    Port:         8080
    PID:          12345
    Binary:       /app/payment-service
    Status:       healthy
    SDK:          enabled (v1.0.0)

  user-service
    Port:         8081
    PID:          12346
    Binary:       /app/user-service
    Status:       healthy
    SDK:          enabled (v1.0.0)
```

```bash
# Debug tree showing process IDs
coral debug tree

# Example output:
Debug Sessions:
  session-abc123
    Service:      payment-service
    PID:          12345
    Binary:       /app/payment-service
    Function:     ProcessPayment
    Status:       active
```

## Implementation Plan

### Phase 1: Protobuf & Core Types

- [x] Add `process_id`, `binary_path`, `binary_hash` to `ServiceInfo` in
  auth.proto
- [x] Add `process_id`, `binary_path`, `binary_hash` to `ServiceStatus` in
  agent.proto
- [x] Regenerate protobuf code (`buf generate`)
- [x] Update Go struct mappings in internal types

### Phase 2: Agent Implementation

- [x] Update `Monitor` struct to track PID and binary path
- [x] Integrate `ServiceSdkCapabilities` into service status reporting
- [x] Implement process discovery for non-SDK services (parse `/proc/net/tcp` +
  `/proc/<pid>/exe`)
- [x] Update `ListServices` RPC handler to include process info
- [x] Update service registration to propagate process info to colony

### Phase 3: Colony Integration

- [x] Update service registry to store PID and binary path
- [x] Update debug orchestrator to use PID from service info
- [x] Remove separate PID discovery logic from debug session creation
- [x] Update MCP tools to expose process information

### Phase 4: CLI & Testing

- [x] Update `coral agent status` to display PID and binary path
- [x] Update `coral debug tree` to show process information
- [x] Add integration tests for process info propagation
- [x] Add tests for PID change detection (service restart scenarios)
- [x] Update documentation

## Security Considerations

**Process Information Exposure:**

- PID and binary path are process-level metadata, not sensitive credentials
- However, binary paths may reveal deployment structure or internal naming
- Recommendation: Ensure service registry access is properly authenticated (
  already the case with WireGuard mesh + colony auth)

**Process Hijacking Prevention:**

- PID alone should not be used as authentication/authorization token
- Debug session authorization should continue to validate:
    - Agent identity (WireGuard peer)
    - Service ownership (agent monitors this service)
    - User permissions (RFD 042 audit requirements)

## Future Enhancements

**Process Lifecycle Tracking:**

- Detect service restarts by monitoring PID changes
- Auto-reattach debug sessions after service restart
- Alert on unexpected process termination

**Binary Version Tracking:**

- Use `binary_hash` to track deployments
- Detect binary updates during service uptime
- Invalidate debug session caches on binary change

**Process Metrics Correlation:**

- Correlate eBPF metrics with specific process versions
- Track performance regressions across binary updates
- Link telemetry to specific binary builds

---

## Implementation Status

**Core Capability:** ✅ Implemented

This RFD has been fully implemented. Process information is now available in the
service registry.

**What This Enables:**

- Direct PID access for eBPF uprobe attachment
- Binary path visibility in service listings
- Simplified debug session orchestration
- Foundation for process lifecycle management
