# RFD 059 Phase 3: Implementation Status

## Overview

Phase 3 implements Colony and Agent integration for debug session orchestration. This document describes what's implemented vs. what's needed for production.

## ✅ Implemented (MVP)

### Agent Side
- **Debug Service** (`internal/agent/debug_service.go`)
  - `StartUprobeCollector` RPC handler
  - `StopUprobeCollector` RPC handler
  - `QueryUprobeEvents` RPC handler with time filtering
  - Integration with eBPF manager

### Colony Side
- **Debug Orchestrator** (`internal/colony/debug/orchestrator.go`)
  - `AttachUprobe` RPC handler (session creation)
  - `DetachUprobe` RPC handler (session termination)
  - `QueryUprobeEvents` RPC handler (stub)
  - `ListDebugSessions` RPC handler
  - In-memory session storage

### Protobuf
- Updated `coral/mesh/v1/ebpf.proto` with:
  - `sdk_addr` field in StartUprobeCollectorRequest
  - StopUprobeCollectorRequest/Response
  - QueryUprobeEventsRequest/Response
- Colony debug service defined in `coral/colony/v1/debug.proto`

## ⏳ Not Yet Implemented (Production Requirements)

### Service Discovery Integration
**Status:** TODO  
**Required for:** Automatic SDK address resolution

**What's needed:**
```go
// Extend service registry to include SDK metadata
type ServiceInfo struct {
    // ... existing fields
    SDKEnabled bool
    SDKAddr    string
    SDKVersion string
}

// Query service registry in orchestrator
service, err := o.serviceRegistry.GetService(req.ServiceName)
sdkAddr := service.SDKAddr
agentID := service.AgentID
```

**Files to modify:**
- `internal/colony/registry/registry.go` - Add SDK fields
- `internal/agent/service_handler.go` - Report SDK info during registration

### Agent Client Pool
**Status:** TODO  
**Required for:** Colony → Agent RPC communication

**What's needed:**
```go
// Agent client management in orchestrator
type Orchestrator struct {
    agentClients map[string]AgentClient
}

// Send RPC to agent
agentClient := o.getAgentClient(session.AgentID)
resp, err := agentClient.StartUprobeCollector(ctx, req)
```

**Files to create:**
- `internal/colony/agent_pool.go` - Agent client pool management

### DuckDB Session Storage
**Status:** TODO  
**Required for:** Persistent session tracking and audit trail

**What's needed:**
```sql
CREATE TABLE debug_sessions (
    session_id VARCHAR PRIMARY KEY,
    collector_id VARCHAR,
    service_name VARCHAR NOT NULL,
    function_name VARCHAR NOT NULL,
    agent_id VARCHAR NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    status VARCHAR NOT NULL,
    requested_by VARCHAR
);
```

**Files to create:**
- `internal/colony/database/debug_sessions.go` - Session CRUD operations
- `internal/colony/database/schema.go` - Update with debug_sessions table

### Session Lifecycle Management
**Status:** TODO  
**Required for:** Auto-cleanup of expired sessions

**What's needed:**
```go
// Background goroutine to cleanup expired sessions
func (o *Orchestrator) cleanupExpiredSessions() {
    ticker := time.NewTicker(1 * time.Minute)
    for range ticker.C {
        o.cleanupExpired()
    }
}
```

### Audit Logging
**Status:** TODO  
**Required for:** Security and compliance

**What's needed:**
- Log all debug session operations with user identity
- Store in DuckDB for audit trail
- Include: who, what, when, which service/function

## Current Limitations

### 1. Manual Configuration Required
**Impact:** Users must provide `agent_id` and `sdk_addr` in requests

**Workaround:**
```bash
# User must know these values
grpcurl -d '{
  "service_name": "my-service",
  "function_name": "main.ProcessPayment",
  "agent_id": "agent-123",        # ← Manual
  "sdk_addr": "127.0.0.1:50051"   # ← Manual
}' ...
```

**Production fix:** Service discovery integration

### 2. No Agent Communication
**Impact:** Colony doesn't actually start collectors on agents

**Workaround:** Use agent debug service directly

**Production fix:** Agent client pool implementation

### 3. In-Memory Sessions
**Impact:** Sessions lost on Colony restart

**Workaround:** Don't restart Colony during debug sessions

**Production fix:** DuckDB persistence

### 4. No Event Retrieval
**Impact:** `QueryUprobeEvents` returns empty

**Workaround:** Query agent directly

**Production fix:** Agent client pool + event forwarding

## Testing the MVP

### What Works
```bash
# 1. Create a debug session (Colony)
grpcurl -d '{
  "service_name": "my-service",
  "function_name": "main.ProcessPayment",
  "agent_id": "agent-1",
  "sdk_addr": "127.0.0.1:50051",
  "duration": "60s"
}' localhost:8081 coral.colony.v1.DebugService/AttachUprobe

# Response: { "session_id": "...", "success": true }

# 2. List sessions
grpcurl localhost:8081 coral.colony.v1.DebugService/ListDebugSessions

# 3. Detach session
grpcurl -d '{"session_id": "..."}' \
  localhost:8081 coral.colony.v1.DebugService/DetachUprobe
```

### What Requires Direct Agent Access
```bash
# Start collector (bypass Colony, go directly to Agent)
grpcurl -d '{
  "service_name": "my-service",
  "function_name": "main.ProcessPayment",
  "sdk_addr": "127.0.0.1:50051",
  "duration": "60s"
}' localhost:8080 coral.mesh.v1.DebugService/StartUprobeCollector

# Query events (Agent)
grpcurl -d '{"collector_id": "..."}' \
  localhost:8080 coral.mesh.v1.DebugService/QueryUprobeEvents
```

## Roadmap to Production

### Priority 1: Service Discovery
- [ ] Add SDK fields to service registry
- [ ] Update agent registration to report SDK info
- [ ] Update orchestrator to query registry

### Priority 2: Agent Communication
- [ ] Implement agent client pool
- [ ] Wire up orchestrator to send RPCs to agents
- [ ] Test end-to-end flow

### Priority 3: Persistence
- [ ] Add DuckDB schema for debug_sessions
- [ ] Implement session CRUD operations
- [ ] Migrate from in-memory to DB storage

### Priority 4: Production Features
- [ ] Session lifecycle management (auto-cleanup)
- [ ] Audit logging
- [ ] Concurrent session limits
- [ ] User authentication/authorization

## Conclusion

**Phase 3 Status:** Functionally Complete for Development Testing

The MVP implementation provides:
- ✅ Complete RPC API surface
- ✅ Session management (in-memory)
- ✅ Agent-side functionality (fully working)
- ⏳ Colony-side orchestration (stub implementation)

**Next Steps:**
- Phase 4: CLI integration (can work with current MVP)
- Production hardening: Service discovery, agent pool, persistence
