---
rfd: "XXX"
title: "Feature Name"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: [ ]
database_migrations: [ ]
areas: [ ]
---

# RFD XXX - Feature Name

**Status:** 🚧 Draft

<!--
Status progression:
  🚧 Draft → 👀 Under Review → ✅ Approved → 🔄 In Progress → 🎉 Implemented

Aim for "Implemented" status by scoping the RFD to a shippable capability.
Use "Deferred Features" section for out-of-scope work (avoids perpetual "In Progress").
-->

## Summary

2-3 sentence overview of the feature. State the motivation and the high-level
outcome concisely.

## Problem

Detailed problem description:

- **Current behavior/limitations**: Describe what doesn't work or is missing
- **Why this matters**: Impact on users, operations, or system capabilities
- **Use cases affected**: Specific scenarios that are blocked or difficult
- Reference metrics, incidents, or user feedback where relevant

## Solution

High-level approach to solving the problem:

**Key Design Decisions:**

- Architectural choices and rationale
- Why this approach over alternatives
- Trade-offs considered

**Benefits:**

- What this enables or improves
- Performance/security/usability gains

**Architecture Overview:**

```
[Optional: Include ASCII diagrams showing component interactions]
User → Colony → Agent → SDK → App
```

### Component Changes

Brief description of changes per component:

1. **Component A** (e.g., Colony):

    - Key change and rationale
    - API/data flow updates

2. **Component B** (e.g., Agent):

    - Key change and rationale
    - Dependencies or impacts

3. **Component C** (e.g., CLI):
    - Key change and rationale
    - User-facing changes

**Configuration Example:**

```yaml
# Show YAML config examples where relevant
feature:
    enabled: true
    option: value
```

## Implementation Plan

**IMPORTANT:** Do NOT include time estimates (weeks, hours, days). Focus on
deliverable phases and concrete, testable tasks.

### Phase 1: Foundation/Database/Protocol

- [ ] Define data structures and types
- [ ] Create database migrations (if applicable)
- [ ] Define protobuf messages and generate code

### Phase 2: Core Implementation

- [ ] Implement [Component A] changes
- [ ] Implement [Component B] changes
- [ ] Add error handling and validation

### Phase 3: Integration & CLI

- [ ] Integrate components end-to-end
- [ ] Add CLI commands/flags
- [ ] Update configuration handling

### Phase 4: Testing & Documentation

- [ ] Add unit tests for all components
- [ ] Add integration tests
- [ ] Add E2E tests
- [ ] Update user documentation

## API Changes

**THIS IS WHERE DETAIL IS APPROPRIATE**

### New Protobuf Messages

```protobuf
// Full protobuf definitions go here
message NewFeatureRequest {
    string server_id = 1;
    string parameter = 2;
}

message NewFeatureResponse {
    bool success = 1;
    string message = 2;
}
```

### New RPC Endpoints

```protobuf
service ServiceName {
    rpc NewOperation(NewFeatureRequest) returns (NewFeatureResponse);
}
```

### CLI Commands

```bash
# New command with expected output
cli feature action <args> [--flags]

# Example output:
Feature Result:
  Status: Success
  Details: ...
```

### Configuration Changes

- New config field: `feature.enabled` (optional, default: false)
- Modified field: `existing.field` now supports additional values

## Testing Strategy

**Optional section**

### Unit Tests

- Component-specific test scope
- Key scenarios to validate
- Error handling coverage

### Integration Tests

- Cross-component test scenarios
- Test against mock distributed apps and MCP servers
- Verify data flow end-to-end

### E2E Tests

- Full stack test scenarios
- CLI command validation
- Real-world usage patterns

## Security Considerations

**Optional section**

- Authentication/authorization impacts
- Data exposure risks and mitigations
- Credential handling (never expose actual passwords)
- Audit logging requirements
- TLS/encryption requirements

## Migration Strategy

**Optional section - include if backward compatibility is a concern**

1. **Deployment Steps**:

    - Deploy Colony changes
    - Deploy Agent changes
    - Update SDK (if applicable)
    - Enable feature flag

2. **Rollback Plan**:
    - Disable feature flag
    - Revert database changes (if needed)
    - No breaking changes to existing workflows

## Implementation Status

**Core Capability:** 🎉 Implemented | 🔄 In Progress | ⏳ Not Started

[Describe the current state of implementation. Focus on what works now, not what's missing.]

Example:

```
**Core Capability:** 🎉 Implemented

Agent trace collection implemented with OpenTelemetry support. Agents can
discover apps via service registry and collect traces automatically.

**Operational Components:**
- ✅ OpenTelemetry trace collection
- ✅ Service discovery via WireGuard mesh
- ✅ Static configuration via YAML
- ✅ CLI: `coral agent list|status|traces`

**What Works Now:**
- Automatic app discovery on local networks
- Trace aggregation and storage
- Health monitoring via colony dashboard
```

**Integration Status:**

- List any remaining integration work (dependencies, config, deployment)
- Keep this minimal - only critical items blocking production use

## Future Work

**Optional section - use for features that are out of scope**

The following features are out of scope for this RFD. They may be addressed in
future RFDs or are intentionally deferred:

Example:

```
**Advanced Discovery** (Future - RFD XXX)
- Multi-cluster mesh support
- Cloud provider integration (AWS, GCP, Azure)
- Kubernetes operator deployment

**Enhanced AI Analysis** (Blocked by Dependencies)
- Root cause correlation - Requires RFD YYY (graph database)
- Predictive anomaly detection - Requires RFD ZZZ (ML pipeline)

**Monitoring Enhancements** (Low Priority)
- Custom metric collection
- Real-time alerting integration
- Historical trend analysis
```

**When to Use This Section:**

✅ **DO include** if:

- Features are blocked by other RFDs (creates a dependency chain)
- Features are intentionally out of scope (keeps this RFD focused)
- Features are future enhancements (signals what's next without bloating this
  RFD)

❌ **DO NOT include** if:

- Core functionality is missing (means the RFD isn't complete - narrow the scope
  instead)
- You're listing "nice-to-haves" without clear rationale (bloats the RFD)

## Appendix

**Optional section for technical deep-dives**

### Protocol Details

- Detailed protocol specifications
- Wire formats
- Authentication flows

### Reference Implementations

- Links to similar implementations
- Industry standards (RFCs, specifications)

### Test Configuration Examples

```yaml
# Detailed test setup examples
test:
    config: example
```

---

## RFD Writing Guidelines

### Scope Management & Completion

**Aim for "Complete" Status:**

RFDs should be scoped to be **fully completable**. Avoid perpetually "In
Progress" RFDs by:

✅ **DO:**

- Narrow scope to a **complete, shippable capability**
- Extract large or blocked features into **separate RFDs** (creates clear
  dependency chains)
- Use **Future Work** section for out-of-scope work (with RFD references)
- Mark RFD "Implemented" when core capability works, even if enhancements are
  deferred

❌ **DO NOT:**

- Leave RFD "In Progress" indefinitely with partial implementation
- Try to fit everything into one mega-RFD (split it!)
- List missing features as "TODO" in main sections (move to Future Work)

**Example - Bad Scope (Too Broad):**

```
RFD: Complete Observability Platform
Status: 🔄 In Progress (30% complete)

Phases:
- [ ] Metrics collection
- [x] Basic dashboards
- [ ] Alerting
- [ ] AI-powered analysis
- [ ] Cost optimization
- [ ] Capacity planning
```

→ This will never be "complete" - too many features!

**Example - Good Scope (Focused & Complete):**

```
RFD 010: Metrics Collection & Storage
Status: ✅ Complete

Core: Agents collect metrics, store locally, colony aggregates.
Future Work: AI analysis (RFD 015), Alerting (RFD 020), Capacity planning (RFD 025)
```

→ Clear what's done, clear what's next, RFD is complete!

**When to Split into Multiple RFDs:**

- Feature is **blocked by another RFD** → Create separate RFD with dependency
- Feature adds **significant complexity** → Keep current RFD focused, create
  follow-up RFD
- Feature is **low priority** → Defer to future RFD, ship core functionality now
- You have **>5 phases** → You probably have 2-3 RFDs hidden in one document

### Content Guidelines

**DO:**

- ✅ Focus on WHAT changes, not HOW to implement every detail
- ✅ Include full API specifications (protobuf, REST endpoints)
- ✅ Include configuration examples (YAML, CLI commands)
- ✅ Show expected output for CLI commands
- ✅ Use diagrams for complex architectures
- ✅ Put protocol details in Appendix
- ✅ Reference files by path only (e.g., `colony/pkg/storage/storage.go`)
- ✅ Add Implementation Status section when RFD is implemented
- ✅ Use Future Work section to manage scope

**DO NOT:**

- ❌ Include time estimates (weeks, hours, days)
- ❌ Show complete Go/Python implementations
- ❌ Include verbose code that belongs in actual implementation
- ❌ Reference specific line numbers (e.g., `file.go:123` or `lines 45-67`)
- ❌ Use line number ranges - they become stale as code changes
- ❌ Duplicate RPC handler signatures when protobuf already defines the API
- ❌ Show function signatures for internal implementation details
- ❌ Keep RFD "In Progress" when core capability works (mark Complete, defer
  enhancements)

**What to include in API Changes:**

- ✅ Protobuf messages and service definitions (the actual API contract)
- ✅ Database schema changes (migrations, new columns/tables)
- ✅ CLI command usage examples with expected output
- ✅ Configuration file changes
- ❌ Go handler function signatures (implementation detail)
- ❌ Internal database method signatures (implementation detail)

**Why avoid redundant signatures:**

- Protobuf already defines the API contract completely
