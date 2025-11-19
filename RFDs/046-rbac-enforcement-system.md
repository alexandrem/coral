---
rfd: "046"
title: "System-Wide RBAC Enforcement"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "001", "006", "007", "014" ]
related_rfds: [ "016", "017", "020", "022", "026", "038", "043", "044" ]
database_migrations: [ "rbac_policies", "rbac_roles", "rbac_approvals", "rbac_audit_log" ]
areas: [ "security", "rbac", "auth", "colony", "cli" ]
---

# RFD 046 - System-Wide RBAC Enforcement

**Status:** 🚧 Draft

## Summary

Implement a comprehensive role-based access control (RBAC) system for all Coral operations (shell, exec, ask, colony admin, agent access) with flexible policy definitions, approval workflows, and centralized enforcement at the Colony level. This provides the security foundation for production deployments with least-privilege access, change control, and compliance requirements.

## Problem

**Current limitations:**

Multiple RFDs define individual RBAC requirements (RFD 016 for operations, RFD 043 for shell) but lack a unified RBAC architecture:

1. **No centralized RBAC engine**: Each feature re-implements permission checks
2. **Inconsistent policy format**: Different RFDs propose different YAML schemas
3. **No user identity integration**: Assumes authenticated users but doesn't define identity source
4. **No approval workflow reuse**: RFD 043 defines shell approvals, but exec/ask need same pattern
5. **Unclear enforcement points**: Should Colony or Agent enforce? When?
6. **No audit trail standard**: Different components log differently

**Why this matters:**

- **Security**: Unrestricted access to production agents/data is a critical gap
- **Compliance**: SOC2, ISO 27001, HIPAA require RBAC and approval workflows
- **Operational safety**: Prevent accidental production changes
- **Multi-tenancy**: Organizations need per-team access isolation
- **Regulatory**: Some industries require separation of duties

**Use cases affected:**

- **Developers**: Should access dev/staging, not production
- **SREs**: Need approved production access for incidents
- **Security team**: Full access without approval (break-glass)
- **Contractors**: Time-limited, environment-scoped access
- **AI operators**: Different permissions than human users
- **Service accounts**: Automated scripts with limited scope

## Solution

Implement a centralized RBAC engine in Colony with policy-based access control, approval workflows, and comprehensive audit logging that works across all Coral operations.

**Key Design Decisions:**

1. **Colony-enforced RBAC**: All permission checks happen at Colony (single enforcement point)
2. **Policy-based model**: Flexible policies map roles → resources → permissions
3. **Resource hierarchy**: Environments > Agents > Operations (inherited permissions)
4. **Approval workflows**: Reusable approval engine for any operation requiring review
5. **Identity integration**: Support multiple identity sources (OIDC, mTLS, API keys)
6. **Audit-first design**: All decisions logged before execution
7. **Fail-secure default**: Deny access if policy evaluation fails

**Benefits:**

- ✅ Single RBAC implementation for all features
- ✅ Consistent policy format across operations
- ✅ Reusable approval workflows
- ✅ Centralized audit trail
- ✅ Identity-agnostic (works with OIDC, mTLS, etc.)
- ✅ Production-ready security for all operations

**Architecture Overview:**

```
┌──────────────────────────────────────────────────────────┐
│ User / AI / Service Account                              │
└────────────────┬─────────────────────────────────────────┘
                 │
                 ▼ (Authenticated)
┌──────────────────────────────────────────────────────────┐
│ CLI / MCP Client                                         │
│ - coral shell --agent=prod-1                             │
│ - coral exec --env=production "df -h"                    │
│ - coral ask "why is checkout slow?"                      │
└────────────────┬─────────────────────────────────────────┘
                 │
                 ▼ RPC with user identity
┌──────────────────────────────────────────────────────────┐
│ Colony: RBAC Enforcement Point                           │
│                                                          │
│  ┌────────────────────────────────────────────────────┐  │
│  │ 1. Extract User Identity                          │  │
│  │    - OIDC: email, groups                          │  │
│  │    - mTLS: certificate CN                         │  │
│  │    - API key: key ID → user mapping               │  │
│  └────────────────────────────────────────────────────┘  │
│                  │                                       │
│                  ▼                                       │
│  ┌────────────────────────────────────────────────────┐  │
│  │ 2. Resolve Resource                               │  │
│  │    - agent ID → environment, region, labels       │  │
│  │    - service name → environment                   │  │
│  │    - colony → admin resource                      │  │
│  └────────────────────────────────────────────────────┘  │
│                  │                                       │
│                  ▼                                       │
│  ┌────────────────────────────────────────────────────┐  │
│  │ 3. Evaluate Policy                                │  │
│  │    PolicyEngine.CheckPermission(                  │  │
│  │      user, operation, resource                    │  │
│  │    ) → Allow | Deny | RequireApproval             │  │
│  └────────────────────────────────────────────────────┘  │
│                  │                                       │
│       ┌──────────┴──────────┐                           │
│       ▼                     ▼                           │
│   ┌──────┐            ┌──────────────┐                  │
│   │Allow │            │RequireApproval│                 │
│   └──┬───┘            └───────┬──────┘                  │
│      │                        │                         │
│      │                        ▼                         │
│      │              ┌─────────────────────┐             │
│      │              │ 4. Approval Workflow│             │
│      │              │  - Create request   │             │
│      │              │  - Notify approvers │             │
│      │              │  - Wait for decision│             │
│      │              └─────────┬───────────┘             │
│      │                        │                         │
│      │              ┌─────────┴─────────┐               │
│      │              │                   │               │
│      │              ▼                   ▼               │
│      │          Approved             Denied             │
│      │              │                   │               │
│      └──────────────┴───────────────────┘               │
│                     │                                   │
│                     ▼                                   │
│  ┌────────────────────────────────────────────────────┐  │
│  │ 5. Audit Log                                      │  │
│  │    - Record decision (allow/deny/approved)        │  │
│  │    - Store: user, operation, resource, timestamp  │  │
│  └────────────────────────────────────────────────────┘  │
│                     │                                   │
│                     ▼                                   │
│              Grant Access                               │
│              (issue token with TTL)                     │
└────────────────┬────────────────────────────────────────┘
                 │
                 ▼ Access token
        ┌────────────────┐
        │ Target Agent   │
        └────────────────┘
```

### Component Changes

1. **RBAC Policy Engine** (`internal/colony/rbac/engine.go`):
   - Load policies from database and config files
   - Evaluate permission requests (user + operation + resource)
   - Return: Allow, Deny, or RequireApproval
   - Support policy hierarchies (environment → agent → operation)
   - Cache policy evaluations for performance

2. **Approval Workflow Engine** (`internal/colony/approval/workflow.go`):
   - Create approval requests with context
   - Notify approvers (CLI, Slack, email)
   - Track approval state (pending, approved, denied, expired)
   - Time-limited approvals (default: 1 hour)
   - Multi-approver support (require N of M approvers)

3. **Identity Integration** (`internal/colony/auth/identity.go`):
   - Extract user identity from request context
   - Support multiple identity sources:
     - OIDC (RFD 020): email, groups, claims
     - mTLS (RFD 022): certificate CN, SANs
     - API keys: key ID → user mapping
   - Normalize to common identity format

4. **Audit Logger** (`internal/colony/audit/logger.go`):
   - Log all RBAC decisions (allow, deny, approval)
   - Store: user, operation, resource, decision, timestamp
   - Export to DuckDB for querying
   - Retention policies and compression

5. **Colony RPC Integration**:
   - Add RBAC checks to all Colony RPCs
   - Shell, exec, ask, admin operations
   - Agent registration (restrict who can register agents)
   - Colony configuration changes

6. **CLI Integration**:
   - Handle approval requests (display status, wait for approval)
   - New commands for approvers: `coral approval list|approve|deny`
   - Display permission errors with policy hints

**Configuration Example:**

```yaml
# ~/.coral/colonies/my-colony.yaml
rbac:
  enabled: true
  policy_file: /etc/coral/rbac-policies.yaml
  approval:
    default_ttl: 1h
    max_ttl: 24h
    notification_channels:
      - slack://approvals-channel
      - email://sre-oncall@company.com

  # Default policy: deny all (fail-secure)
  default_policy: deny
```

## RBAC Policy Format

### Policy Schema

```yaml
# /etc/coral/rbac-policies.yaml
rbac:
  # Role definitions
  roles:
    - name: developer
      description: Software engineers working on applications

    - name: sre
      description: Site reliability engineers managing production

    - name: security
      description: Security team with broad access

    - name: contractor
      description: External contractors with limited access

    - name: admin
      description: Coral administrators with full system access

  # Policy rules (evaluated in order)
  policies:
    # Admin role: full access to everything
    - name: admin-full-access
      roles: [ admin ]
      resources:
        - type: "*"
          environments: [ "*" ]
      operations: [ "*" ]
      effect: allow

    # Security team: full access to agents, requires MFA for shell
    - name: security-agent-access
      roles: [ security ]
      resources:
        - type: agent
          environments: [ dev, staging, production ]
      operations: [ shell, exec, connect, status ]
      effect: allow
      conditions:
        require_mfa:
          - shell  # Only shell requires MFA

    # SRE: dev/staging full access
    - name: sre-nonprod-access
      roles: [ sre ]
      resources:
        - type: agent
          environments: [ dev, staging ]
      operations: [ shell, exec, connect, status ]
      effect: allow

    # SRE: production shell requires approval
    - name: sre-prod-shell-approval
      roles: [ sre ]
      resources:
        - type: agent
          environments: [ production ]
      operations: [ shell ]
      effect: require_approval
      approval:
        approvers:
          roles: [ sre, admin ]
          min_approvals: 1
        ttl: 1h
        require_mfa: true

    # SRE: production exec/status allowed (read-only)
    - name: sre-prod-readonly
      roles: [ sre ]
      resources:
        - type: agent
          environments: [ production ]
      operations: [ exec, status, connect ]
      effect: allow
      conditions:
        # Restrict exec to safe read-only commands
        allowed_commands:
          - "ps aux"
          - "df -h"
          - "netstat -an"
          - "curl localhost/*"  # Health checks only

    # Developer: dev access only
    - name: developer-dev-access
      roles: [ developer ]
      resources:
        - type: agent
          environments: [ dev ]
      operations: [ shell, exec, connect, status ]
      effect: allow

    # Contractor: time-limited dev access
    - name: contractor-limited-access
      roles: [ contractor ]
      resources:
        - type: agent
          environments: [ dev ]
      operations: [ exec, status ]  # No shell access
      effect: allow
      conditions:
        time_window:
          start: "2025-01-01T00:00:00Z"
          end: "2025-06-30T23:59:59Z"

    # AI access: can use 'coral ask' but not shell/exec
    - name: ai-query-access
      users: [ "ai-agent@coral.internal" ]
      resources:
        - type: colony
        - type: agent
          environments: [ "*" ]
      operations: [ ask, status, list ]
      effect: allow

    # Default deny: catches all unmatched requests
    - name: default-deny
      roles: [ "*" ]
      resources:
        - type: "*"
      operations: [ "*" ]
      effect: deny

  # User-to-role assignments
  users:
    - email: alice@company.com
      roles: [ developer ]

    - email: bob@company.com
      roles: [ sre ]

    - email: security@company.com
      roles: [ security ]

    - email: charlie@contractor.com
      roles: [ contractor ]
      metadata:
        contract_end: "2025-06-30"

    - email: admin@company.com
      roles: [ admin ]

    # Service account for AI
    - email: ai-agent@coral.internal
      roles: [ ]  # No standard role, uses specific policy
      type: service_account
```

## Policy Evaluation Logic

### Policy Matching Algorithm

```
For request (user, operation, resource):

1. Extract user roles from user-to-role mapping
2. Resolve resource attributes (environment, labels, etc.)
3. Iterate through policies in order:

   For each policy:
     a. Check if user matches (role or email)
     b. Check if resource matches (type, environment, labels)
     c. Check if operation matches
     d. Evaluate conditions (if any):
        - require_mfa
        - allowed_commands
        - time_window
        - ip_whitelist

     If ALL match:
       - If effect = "allow" → ALLOW
       - If effect = "deny" → DENY
       - If effect = "require_approval" → CREATE APPROVAL REQUEST

       Stop evaluation (first match wins)

4. If no policy matched → DEFAULT DENY
```

### Resource Types

| Resource Type | Description | Examples |
|---------------|-------------|----------|
| `agent` | Individual agents | Specific agent ID, environment, region |
| `colony` | Colony administration | Config changes, user management |
| `approval` | Approval requests | Approve/deny requests |
| `audit` | Audit logs | Query audit history |

### Operations

| Operation | Description | Resource Types |
|-----------|-------------|----------------|
| `shell` | Interactive shell access | agent |
| `exec` | Execute commands | agent |
| `connect` | Attach to processes | agent |
| `status` | Query status | agent, colony |
| `ask` | AI queries | colony, agent |
| `list` | List resources | agent, colony |
| `approve` | Approve requests | approval |
| `deny` | Deny requests | approval |
| `configure` | Change configuration | colony |

### Conditions

```yaml
conditions:
  # Require MFA for specific operations
  require_mfa:
    - shell
    - configure

  # Restrict exec to safe commands
  allowed_commands:
    - "ps aux"
    - "df -h"
    - "curl localhost:*"

  # Time-based access (contractor windows)
  time_window:
    start: "2025-01-01T00:00:00Z"
    end: "2025-06-30T23:59:59Z"

  # IP whitelist (office network only)
  ip_whitelist:
    - "10.0.0.0/8"
    - "192.168.1.0/24"

  # Label matching (agent labels)
  agent_labels:
    team: platform
    region: us-east
```

## Approval Workflows

### Approval Request Lifecycle

```
┌─────────────┐
│  Pending    │ ← Request created
└──────┬──────┘
       │
       ├─── Timeout (default: 5 min) ──→ Expired
       │
       ├─── Approver denies ──────────→ Denied
       │
       └─── Approver approves ────────→ Approved
                                         ↓
                                    ┌──────────┐
                                    │ Active   │ (TTL: default 1h)
                                    └────┬─────┘
                                         │
                                         └─── TTL expires ──→ Expired
```

### Approval Request Schema

```yaml
# Database: rbac_approval_requests
approval_id: "AR-2025-001234"
requested_by: "bob@company.com"
requested_at: "2025-11-19T10:30:00Z"
operation: "shell"
resource:
  type: "agent"
  agent_id: "prod-api-01"
  environment: "production"
status: "pending"  # pending, approved, denied, expired
approved_by: null
approved_at: null
expires_at: "2025-11-19T10:35:00Z"  # Request timeout
approval_ttl: "1h"  # How long approval is valid after granted
justification: "Investigating production latency spike"
approval_token: null  # Generated when approved
```

### Notification Channels

```yaml
approval:
  notification_channels:
    # CLI notification (approver polling)
    - type: cli

    # Slack bot
    - type: slack
      webhook: "https://hooks.slack.com/..."
      channel: "#sre-approvals"
      template: |
        🔐 Approval Request #{{.ID}}
        User: {{.User}}
        Operation: {{.Operation}}
        Resource: {{.Resource.AgentID}} ({{.Resource.Environment}})
        Justification: {{.Justification}}

        Approve: `coral approval approve {{.ID}}`
        Deny: `coral approval deny {{.ID}}`

    # Email notification
    - type: email
      smtp_server: "smtp.company.com"
      from: "coral-approvals@company.com"
      to: "sre-oncall@company.com"
```

### Approver Workflow (CLI)

```bash
# Approver checks pending requests
$ coral approval list
Pending Approval Requests (2):

ID            USER              OPERATION  RESOURCE              REQUESTED    EXPIRES
AR-2025-001   bob@company.com   shell      prod-api-01 (prod)   2m ago       3m
AR-2025-002   alice@company.com exec       prod-db-01 (prod)    5m ago       timeout

# Approver reviews request details
$ coral approval show AR-2025-001
Approval Request: AR-2025-001

User:          bob@company.com
Operation:     shell (interactive debug access)
Resource:      Agent prod-api-01 (environment: production, region: us-east)
Requested:     2025-11-19 10:30:00 UTC (2 minutes ago)
Expires:       2025-11-19 10:35:00 UTC (3 minutes remaining)
Justification: Investigating production latency spike

Policy:        sre-prod-shell-approval
Requires:      1 of [sre, admin] approvers
MFA Required:  Yes (after approval)

Approve this request? This will grant shell access for 1 hour.
  coral approval approve AR-2025-001

# Approver approves
$ coral approval approve AR-2025-001
Approval granted: AR-2025-001

User bob@company.com can now access agent prod-api-01 for shell operations.
Access expires: 2025-11-19 11:30:00 UTC (1 hour from now)

Notification sent to: bob@company.com
```

### Requester Workflow (CLI)

```bash
# Requester attempts shell access
$ coral shell --agent=prod-api-01
⚠️  Access requires approval

Creating approval request...

Approval Request: AR-2025-001
Operation:     shell
Resource:      prod-api-01 (production)
Policy:        sre-prod-shell-approval
Justification: (enter reason for access)
> Investigating production latency spike

Request created. Waiting for approval...

Notified approvers:
  - SRE on-call (Slack: #sre-approvals)
  - security@company.com (Email)

Approval timeout: 5 minutes

[                    ] Waiting... (0:23 elapsed)

# After approval
✓ Approval granted by security@company.com

Access expires in 1 hour.

MFA required. Enter code from authenticator app:
> 123456

✓ MFA verified

Connecting to agent prod-api-01...
```

## Implementation Plan

### Phase 1: RBAC Policy Engine

- [ ] Create `internal/colony/rbac/` package
- [ ] Implement policy parser (YAML → Go structs)
- [ ] Implement policy evaluation engine
- [ ] Add policy caching for performance
- [ ] Unit tests for policy matching logic
- [ ] Database schema for policies and user-role mappings

### Phase 2: Identity Integration

- [ ] Create `internal/colony/auth/identity.go`
- [ ] Extract user identity from request context
- [ ] Support OIDC identity (email, groups) - RFD 020
- [ ] Support mTLS identity (certificate CN) - RFD 022
- [ ] Support API key identity (key ID → user)
- [ ] Normalize to common identity format

### Phase 3: Approval Workflow Engine

- [ ] Create `internal/colony/approval/` package
- [ ] Implement approval request creation
- [ ] Database schema for approval requests
- [ ] Notification system (CLI, Slack, email)
- [ ] Approval state machine (pending → approved/denied/expired)
- [ ] TTL enforcement and cleanup
- [ ] Multi-approver support

### Phase 4: Audit Logging

- [ ] Create `internal/colony/audit/logger.go`
- [ ] Log all RBAC decisions to DuckDB
- [ ] Database schema for audit logs
- [ ] Query interface for audit history
- [ ] Retention policies and compression
- [ ] Export to external SIEM (future)

### Phase 5: Colony RPC Integration

- [ ] Add RBAC middleware to Colony gRPC server
- [ ] Integrate permission checks for:
  - [ ] Shell access (RFD 026)
  - [ ] Exec commands (RFD 017)
  - [ ] Ask queries (RFD 014)
  - [ ] Agent registration
  - [ ] Colony admin operations
- [ ] Return approval-required errors with request ID

### Phase 6: CLI Integration

- [ ] Implement approval request flow in CLI
- [ ] Add `coral approval list|show|approve|deny` commands
- [ ] Handle approval-required errors (wait for approval)
- [ ] Display permission denied errors with policy hints
- [ ] MFA prompt integration (if required)

### Phase 7: Testing and Documentation

- [ ] Unit tests for policy engine
- [ ] Integration tests for approval workflow
- [ ] E2E tests for RBAC enforcement
- [ ] Security audit of policy evaluation
- [ ] Documentation: policy writing guide
- [ ] Example policies for common scenarios

## API Changes

### Colony gRPC API

**New RPCs:**

```protobuf
service ColonyService {
  // Check permission for operation
  rpc CheckPermission(CheckPermissionRequest) returns (CheckPermissionResponse);

  // Approval workflow
  rpc CreateApprovalRequest(CreateApprovalRequestRequest) returns (CreateApprovalRequestResponse);
  rpc ListApprovalRequests(ListApprovalRequestsRequest) returns (ListApprovalRequestsResponse);
  rpc GetApprovalRequest(GetApprovalRequestRequest) returns (GetApprovalRequestResponse);
  rpc ApproveRequest(ApproveRequestRequest) returns (ApproveRequestResponse);
  rpc DenyRequest(DenyRequestRequest) returns (DenyRequestResponse);
}

message CheckPermissionRequest {
  string user_id = 1;
  string operation = 2;  // shell, exec, ask, etc.
  Resource resource = 3;
  string justification = 4;  // Optional: reason for access
}

message Resource {
  string type = 1;  // agent, colony, approval
  string agent_id = 2;  // For agent resources
  string environment = 3;
  map<string, string> labels = 4;
}

message CheckPermissionResponse {
  PermissionResult result = 1;
  string approval_request_id = 2;  // If result = REQUIRE_APPROVAL
  string denial_reason = 3;  // If result = DENY
  string policy_name = 4;  // Policy that matched
}

enum PermissionResult {
  PERMISSION_ALLOW = 0;
  PERMISSION_DENY = 1;
  PERMISSION_REQUIRE_APPROVAL = 2;
}

message CreateApprovalRequestRequest {
  string user_id = 1;
  string operation = 2;
  Resource resource = 3;
  string justification = 4;
}

message CreateApprovalRequestResponse {
  string approval_request_id = 1;
  google.protobuf.Timestamp expires_at = 2;
  repeated string notified_approvers = 3;
}

message ListApprovalRequestsRequest {
  string status_filter = 1;  // pending, approved, denied, expired
  string user_filter = 2;  // Filter by requester
}

message ListApprovalRequestsResponse {
  repeated ApprovalRequest requests = 1;
}

message ApprovalRequest {
  string approval_request_id = 1;
  string requested_by = 2;
  google.protobuf.Timestamp requested_at = 3;
  string operation = 4;
  Resource resource = 5;
  string status = 6;  // pending, approved, denied, expired
  string approved_by = 7;
  google.protobuf.Timestamp approved_at = 8;
  google.protobuf.Timestamp expires_at = 9;
  string justification = 10;
}

message ApproveRequestRequest {
  string approval_request_id = 1;
  string approver_id = 2;
}

message ApproveRequestResponse {
  string approval_token = 1;  // Token to use for accessing resource
  google.protobuf.Timestamp expires_at = 2;  // When approval expires
}

message DenyRequestRequest {
  string approval_request_id = 1;
  string approver_id = 2;
  string reason = 3;
}

message DenyRequestResponse {
  bool success = 1;
}
```

### CLI Commands

```bash
# Check user's permissions (for debugging)
coral rbac check --operation=shell --agent=prod-api-01

# List approval requests (for approvers)
coral approval list
coral approval list --status=pending
coral approval list --user=bob@company.com

# Show approval request details
coral approval show AR-2025-001

# Approve/deny requests
coral approval approve AR-2025-001
coral approval deny AR-2025-001 --reason="Not justified"

# Query RBAC audit logs
coral rbac audit --user=alice@company.com --since=24h
coral rbac audit --operation=shell --environment=production
```

### Database Schema

```sql
-- RBAC policies (can also load from YAML)
CREATE TABLE IF NOT EXISTS rbac_policies (
  policy_id TEXT PRIMARY KEY,
  policy_name TEXT NOT NULL,
  roles TEXT[],  -- Array of role names
  users TEXT[],  -- Array of user emails (for user-specific policies)
  resource_type TEXT NOT NULL,
  resource_environments TEXT[],
  operations TEXT[] NOT NULL,
  effect TEXT NOT NULL CHECK (effect IN ('allow', 'deny', 'require_approval')),
  conditions JSONB,  -- JSON blob for complex conditions
  priority INTEGER NOT NULL DEFAULT 100,  -- Lower = higher priority
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- User-to-role assignments
CREATE TABLE IF NOT EXISTS rbac_user_roles (
  user_email TEXT NOT NULL,
  role_name TEXT NOT NULL,
  granted_by TEXT,
  granted_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP,  -- NULL = no expiration
  PRIMARY KEY (user_email, role_name)
);

-- Approval requests
CREATE TABLE IF NOT EXISTS rbac_approval_requests (
  approval_id TEXT PRIMARY KEY,
  requested_by TEXT NOT NULL,
  requested_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  operation TEXT NOT NULL,
  resource_type TEXT NOT NULL,
  resource_id TEXT NOT NULL,
  resource_environment TEXT,
  resource_labels JSONB,
  status TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'denied', 'expired')),
  approved_by TEXT,
  approved_at TIMESTAMP,
  expires_at TIMESTAMP NOT NULL,  -- Request expiration
  approval_ttl INTERVAL,  -- How long approval is valid
  justification TEXT,
  approval_token TEXT,  -- Generated when approved
  policy_name TEXT  -- Policy that required approval
);

CREATE INDEX idx_approval_status ON rbac_approval_requests(status);
CREATE INDEX idx_approval_requester ON rbac_approval_requests(requested_by);
CREATE INDEX idx_approval_expires ON rbac_approval_requests(expires_at);

-- RBAC audit log
CREATE TABLE IF NOT EXISTS rbac_audit_log (
  audit_id TEXT PRIMARY KEY,
  timestamp TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  user_id TEXT NOT NULL,
  operation TEXT NOT NULL,
  resource_type TEXT NOT NULL,
  resource_id TEXT,
  resource_environment TEXT,
  decision TEXT NOT NULL CHECK (decision IN ('allow', 'deny', 'require_approval')),
  policy_name TEXT,  -- Policy that matched
  approval_id TEXT,  -- If approved access
  denial_reason TEXT,  -- If denied
  client_ip TEXT,
  user_agent TEXT
);

CREATE INDEX idx_audit_timestamp ON rbac_audit_log(timestamp);
CREATE INDEX idx_audit_user ON rbac_audit_log(user_id);
CREATE INDEX idx_audit_operation ON rbac_audit_log(operation);
CREATE INDEX idx_audit_resource ON rbac_audit_log(resource_type, resource_id);
```

## Testing Strategy

### Unit Tests

**Policy Engine:**
- Policy loading from YAML
- Policy matching algorithm (user, resource, operation)
- Condition evaluation (time windows, IP whitelist, etc.)
- Default deny behavior
- Policy priority/ordering

**Approval Workflow:**
- Request creation and storage
- State transitions (pending → approved/denied/expired)
- TTL enforcement
- Multi-approver logic (N of M approvals)
- Notification delivery

### Integration Tests

**RBAC Enforcement:**
- Shell access with approval workflow
- Exec command permission checks
- AI query restrictions
- Colony admin operations
- Identity extraction from different auth methods

**Approval Flow:**
- Create request → notify → approve → grant access
- Create request → deny → access denied
- Create request → timeout → access denied
- Multiple pending requests for same user

### E2E Tests

**Production Scenarios:**
1. Developer attempts production shell → denied
2. SRE requests production shell → approval required → granted → shell access
3. Security team accesses production shell → allowed immediately
4. Contractor accesses dev → allowed (within time window)
5. AI queries production metrics → allowed (read-only)
6. Admin modifies Colony config → allowed

**Security Scenarios:**
- Tampered approval token → rejected
- Expired approval → access denied
- Approval for different resource → rejected
- Policy file syntax error → safe fallback to deny

## Security Considerations

### Fail-Secure Defaults

**Risk:** Policy evaluation failure could allow unauthorized access.

**Mitigations:**
- Default policy: DENY if no policy matches
- Deny access if policy file is missing/corrupt
- Deny access if RBAC engine crashes
- Log all failures as security events

### Approval Token Security

**Risk:** Stolen approval tokens grant unauthorized access.

**Mitigations:**
- Short TTL (default: 1 hour)
- Signed tokens (HMAC with Colony secret)
- Single-use tokens (invalidated after use)
- Bind tokens to user identity (can't be transferred)
- Audit all token usage

### Policy Injection

**Risk:** Malicious policy files could grant unauthorized access.

**Mitigations:**
- Policy files loaded only from trusted paths
- YAML parser with strict schema validation
- Immutable policies in database (require admin role to modify)
- Policy changes audited
- Policy file integrity checks (checksum)

### Privilege Escalation

**Risk:** Users might escalate privileges through role manipulation.

**Mitigations:**
- Only admins can assign roles
- Role changes audited
- Separation of duties (approvers can't approve own requests)
- Regular role reviews (detect stale permissions)

### Audit Log Tampering

**Risk:** Attackers might delete audit logs to hide access.

**Mitigations:**
- Write-only audit log (no delete/update permissions)
- Replicate audit logs to external SIEM (immutable storage)
- Audit log integrity checks (detect gaps)
- Alert on audit log failures

## Migration Strategy

### Deployment

**Phase 1: RBAC engine deployment (RBAC disabled)**

1. Deploy Colony with RBAC code
2. Set `rbac.enabled: false` in config
3. All requests allowed (backward compatible)
4. Test policy loading and evaluation

**Phase 2: Policy testing (dry-run mode)**

1. Enable `rbac.dry_run: true`
2. RBAC evaluates policies but doesn't enforce
3. Log what would be allowed/denied
4. Review audit logs, tune policies

**Phase 3: RBAC enforcement (gradual)**

1. Enable RBAC for non-production environments only
2. Monitor for policy issues
3. Adjust policies based on feedback
4. Enable RBAC for production environments

**Phase 4: Approval workflows**

1. Deploy approval workflow code
2. Enable approval requirements in policies
3. Train approvers on workflow
4. Monitor approval latency

### Rollback Plan

1. **Disable RBAC**: Set `rbac.enabled: false` in Colony config
2. **Revert policies**: Remove policies requiring approval
3. **No data loss**: Audit logs preserved
4. **No breaking changes**: All operations allowed without RBAC

## Relationship to Other RFDs

**RFD 016 (Unified Operations UX):**
- Defines operations (shell, exec, run) that need RBAC
- This RFD provides RBAC enforcement for all operations

**RFD 017 (Exec Command):**
- Exec operations checked by RBAC engine
- Restricted commands enforced via policy conditions

**RFD 020 (OIDC Auth):**
- OIDC provides user identity (email, groups)
- This RFD extracts identity for RBAC evaluation

**RFD 022 (mTLS Auth):**
- mTLS provides certificate-based identity
- This RFD extracts CN/SANs for RBAC evaluation

**RFD 026 (Shell Command):**
- Shell access is highest-risk operation
- This RFD enforces approval workflows for production shell

**RFD 038 (Direct Connectivity):**
- RBAC checks happen before granting AllowedIPs access
- Approval token required to establish direct connection

**RFD 043 (Shell RBAC):**
- RFD 043 is shell-specific implementation
- This RFD provides general RBAC framework
- RFD 043 uses approval workflows defined here

**RFD 044 (Agent ID Routing):**
- Agent ID resolution integrated with RBAC checks
- Resolve agent → check permissions → grant access

## Future Enhancements

### Dynamic Policies from External Sources

Load policies from:
- HashiCorp Vault (dynamic credentials)
- AWS IAM (integrate with cloud provider)
- LDAP/Active Directory (enterprise directory)
- Open Policy Agent (OPA) for advanced policy language

### Attribute-Based Access Control (ABAC)

Extend beyond roles to attributes:
- User attributes: department, seniority, clearance level
- Resource attributes: data classification, compliance zone
- Environmental attributes: time of day, geolocation, device posture

### Risk-Based Access Control

Adjust requirements based on risk score:
- Low risk (dev environment) → no approval
- Medium risk (staging) → approval recommended
- High risk (production) → approval + MFA required
- Calculate risk from: environment, operation, user history

### Just-In-Time (JIT) Access

Automated approval based on:
- On-call rotation (auto-approve on-call SREs)
- Incident response (auto-approve during incident)
- Break-glass (emergency access with post-incident review)

### Session Recording for Compliance

- Record all shell sessions (RFD 042)
- Integrate with approval ID for audit trail
- Replay sessions for security review

---

## Implementation Status

**Core Capability:** ⏳ Not Started

This RFD defines the comprehensive RBAC architecture for Coral. Implementation pending.

**Dependencies:**
- ⏳ RFD 020 (OIDC Auth) - for user identity
- ⏳ RFD 022 (mTLS Auth) - for certificate-based identity
- ✅ RFD 006 (Colony RPC) - foundation for adding RBAC checks

**Integration Points:**
- All Colony RPCs will check permissions before execution
- CLI will handle approval workflows transparently
- Agents will validate approval tokens on access

## Deferred Features

The following features build on the core RBAC foundation but are not required for initial deployment:

**Advanced Policy Sources** (Future)
- HashiCorp Vault integration
- Open Policy Agent (OPA) support
- LDAP/Active Directory sync

**Risk-Based Access Control** (Future)
- Dynamic risk scoring
- Context-aware policy adjustment
- Anomaly detection integration

**Just-In-Time Access** (Blocked by Incident Management - RFD TBD)
- Auto-approval during incidents
- On-call rotation integration
- Break-glass access with review

---

## Notes

**Why This RFD:**

Coral needs a unified RBAC system that works across all operations. Rather than implement RBAC piecemeal in each feature RFD, this provides:

- Single source of truth for authorization
- Consistent policy language
- Reusable approval workflows
- Centralized audit trail

**Relationship to RFD 043:**

RFD 043 focused on shell-specific RBAC. This RFD:
- Generalizes the approach for all operations
- Defines reusable approval workflows
- Provides the RBAC engine RFD 043 can use

RFD 043 can be implemented as a consumer of this RFD's RBAC engine.

**Production Readiness:**

RBAC is critical for production deployments. This RFD provides:
- Compliance readiness (SOC2, ISO 27001, HIPAA)
- Least-privilege access
- Change control for production
- Complete audit trail

Without RBAC, Coral is suitable for development only.
