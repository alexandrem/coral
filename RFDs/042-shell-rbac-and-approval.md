---
rfd: "042"
title: "Shell RBAC and Approval Workflows"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "026", "041" ]
related_rfds: [ "020" ]
database_migrations: [ "shell_approvals", "rbac_policies" ]
areas: [ "security", "rbac", "auth" ]
---

# RFD 042 - Shell RBAC and Approval Workflows

**Status:** 🚧 Draft

## Summary

Implement role-based access control (RBAC) and approval workflows for
`coral agent shell` to restrict privileged shell access based on user roles,
environments, and approval requirements. This ensures that only authorized users
can access agent shells, and production access requires explicit approval from
authorized approvers.

## Problem

**Current limitations:**

RFD 026 implemented `coral agent shell`, providing powerful debugging
capabilities with elevated privileges. However, access control is currently
missing:

- **No RBAC**: Any authenticated user can shell into any agent
- **No environment-based restrictions**: Cannot restrict prod access while
  allowing dev/staging
- **No approval workflows**: Production access has no review/approval gate
- **No MFA requirement**: High-risk operations don't require multi-factor
  authentication
- **No time-limited access**: Approvals don't expire, no temporary access grants

**Why this matters:**

- **Security risk**: Unrestricted shell access to production agents is a
  critical security gap
- **Compliance**: SOC2, ISO 27001 require least-privilege access and approval
  workflows
- **Insider threats**: Need to prevent unauthorized or malicious shell access
- **Change control**: Production access should follow change management
  processes
- **Audit requirements**: Need to track who approved each shell session

**Use cases affected:**

- **Developer access**: Should only access dev/staging, not production
- **SRE production access**: Needs approval + MFA for emergency debugging
- **Security team**: May need unrestricted access for incident response
- **Contractors**: Need time-limited, environment-scoped access

## Solution

Implement a flexible RBAC system with approval workflows, environment-based
policies, and optional MFA enforcement.

**Key Design Decisions:**

1. **Policy-based RBAC**: Define policies mapping roles → environments →
   permissions
2. **Colony-enforced**: Colony checks permissions before allowing shell access
3. **Approval workflow**: Requested → Approved/Denied → Time-limited access
4. **MFA integration**: Optional MFA check before granting shell access (future:
   integrate with OIDC)
5. **Audit trail**: All approval requests/decisions logged (leverages RFD 034)

**Benefits:**

- **Least privilege**: Users only get access to environments they need
- **Change control**: Production access requires explicit approval
- **Compliance**: Meets regulatory requirements for privileged access
- **Flexibility**: Policies adapt to different roles and environments
- **Audit trail**: Complete record of who requested, who approved, when

**Architecture Overview:**

```
┌─────────────────────────────────────────────────────────┐
│ CLI: coral agent shell (RFD 026)                        │
└────────────┬────────────────────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────────────────────┐
│ Colony: Check RBAC Policy                                │
│ - User role: developer, sre, admin                       │
│ - Target environment: dev, staging, prod                 │
│ - Required permission: shell                             │
└────────────┬────────────────────────────────────────────┘
             │
             ▼
      ┌──────┴──────┐
      │             │
      ▼             ▼
   Allowed      Requires Approval
      │             │
      │             ▼
      │    ┌─────────────────────┐
      │    │ Approval Workflow   │
      │    │ - Request created   │
      │    │ - Notify approvers  │
      │    │ - Wait for decision │
      │    └─────────┬───────────┘
      │              │
      │              ▼
      │         ┌────┴────┐
      │         │         │
      │         ▼         ▼
      │      Approved   Denied
      │         │         │
      └─────────┴─────────┘
                │
                ▼ (optional)
        ┌───────────────┐
        │ MFA Challenge │
        └───────┬───────┘
                │
                ▼
        ┌───────────────┐
        │ Grant Shell   │
        │ Access        │
        └───────────────┘
```

### Component Changes

1. **Colony RBAC Engine** (`internal/colony/rbac`):
    - Policy evaluation: user + agent → allowed/denied/approval_required
    - Role definitions: developer, sre, security, admin
    - Environment detection from agent metadata

2. **Approval Workflow** (`internal/colony/approval`):
    - Request creation and storage
    - Notification to approvers (Slack, email)
    - Approval/denial tracking
    - Time-limited approval grants (e.g., 1 hour)

3. **CLI Integration** (`internal/cli/agent/shell.go`):
    - Check Colony for permission before shell start
    - If approval required: create request, wait for approval
    - If MFA required: prompt for code
    - Display approval status to user

4. **Agent Shell Handler** (`internal/agent/shell_handler.go`):
    - Validate approval token before starting shell
    - Record approval_id in audit log (RFD 034)

## RBAC Policy Format

### Policy Configuration (YAML)

```yaml
rbac:
    roles:
        -   name: developer
            permissions:
                -   environments: [ dev, staging ]
                    commands: [ connect, exec ]       # Can use exec, NOT shell

        -   name: sre
            permissions:
                -   environments: [ dev, staging ]
                    commands: [ connect, exec, shell ]  # Can use shell in non-prod
                -   environments: [ production ]
                    commands: [ shell ]
                    require_approval: true               # Production requires approval
                    require_mfa: true                    # And MFA
                    approval_ttl: 1h                     # Approval valid for 1 hour

        -   name: security
            permissions:
                -   environments: [ dev, staging, production ]
                    commands: [ connect, exec, shell ]   # Full access, no approval
                    require_mfa: true                    # But requires MFA for prod

        -   name: admin
            permissions:
                -   environments: [ "*" ]                # All environments
                    commands: [ "*" ]                    # All commands
                    require_approval: false              # No approval needed

    # Map users to roles
    users:
        -   email: alice@company.com
            role: developer

        -   email: bob@company.com
            role: sre

        -   email: security@company.com
            role: security

    # Define who can approve shell requests
    approvers:
        -   role: sre
            can_approve:
                -   environments: [ dev, staging ]

        -   role: security
            can_approve:
                -   environments: [ production ]

        -   role: admin
            can_approve:
                -   environments: [ "*" ]
```

## Database Schema

### Colony DuckDB Schema

```sql
-- Approval requests
CREATE TABLE shell_approval_requests
(
    request_id     VARCHAR PRIMARY KEY,
    user_id        VARCHAR   NOT NULL,
    agent_id       VARCHAR   NOT NULL,
    environment    VARCHAR   NOT NULL,
    status         VARCHAR   NOT NULL, -- pending, approved, denied, expired
    justification  VARCHAR,            -- User-provided reason
    requested_at   TIMESTAMP NOT NULL,
    approved_at    TIMESTAMP,
    approved_by    VARCHAR,            -- Approver user_id
    denial_reason  VARCHAR,            -- If denied
    expires_at     TIMESTAMP,          -- When approval expires
    approval_token VARCHAR,            -- Token for agent to validate
    created_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_approval_user ON shell_approval_requests (user_id);
CREATE INDEX idx_approval_status ON shell_approval_requests (status);
CREATE INDEX idx_approval_requested ON shell_approval_requests (requested_at DESC);

-- RBAC policies (cached from YAML config)
CREATE TABLE rbac_policies
(
    policy_id            VARCHAR PRIMARY KEY,
    role                 VARCHAR NOT NULL,
    environment          VARCHAR, -- NULL means "*" (all environments)
    command              VARCHAR NOT NULL,
    require_approval     BOOLEAN   DEFAULT false,
    require_mfa          BOOLEAN   DEFAULT false,
    approval_ttl_seconds INTEGER,
    created_at           TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_policy_role ON rbac_policies (role);

-- User role assignments
CREATE TABLE user_roles
(
    user_id     VARCHAR PRIMARY KEY,
    role        VARCHAR NOT NULL,
    assigned_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    assigned_by VARCHAR
);

CREATE INDEX idx_user_role ON user_roles (role);
```

## API Changes

### New Protobuf Messages

```protobuf
// Shell authorization request (before starting shell)
message AuthorizeShellRequest {
    string user_id = 1;
    string agent_id = 2;
    string environment = 3;     // e.g., "production", "staging"
    string justification = 4;   // Optional reason for access
}

message AuthorizeShellResponse {
    enum Status {
        STATUS_UNSPECIFIED = 0;
        STATUS_ALLOWED = 1;                // Immediate access granted
        STATUS_DENIED = 2;                 // Access denied
        STATUS_APPROVAL_REQUIRED = 3;      // Approval workflow needed
        STATUS_MFA_REQUIRED = 4;           // MFA challenge required
    }

    Status status = 1;
    string message = 2;                  // Human-readable message
    string approval_request_id = 3;      // If approval required
    string approval_token = 4;           // If approved, token to validate
    google.protobuf.Timestamp expires_at = 5;  // When approval expires
}

// Approval workflow
message CreateApprovalRequest {
    string user_id = 1;
    string agent_id = 2;
    string environment = 3;
    string justification = 4;
}

message CreateApprovalResponse {
    string request_id = 1;
    string status = 2;                   // "pending"
    string message = 3;
}

message ApproveShellRequest {
    string request_id = 1;
    string approver_id = 2;
    bool approved = 3;                   // true = approve, false = deny
    string reason = 4;                   // Approval/denial reason
}

message ApproveShellResponse {
    bool success = 1;
    string message = 2;
    string approval_token = 3;           // Token for user to start shell
    google.protobuf.Timestamp expires_at = 4;
}

message GetApprovalStatusRequest {
    string request_id = 1;
}

message GetApprovalStatusResponse {
    string status = 1;                   // pending, approved, denied, expired
    string approver_id = 2;
    google.protobuf.Timestamp approved_at = 3;
    google.protobuf.Timestamp expires_at = 4;
    string approval_token = 5;
}
```

### New Colony RPC Endpoints

```protobuf
service ColonyService {
    // Check if user is authorized for shell access
    rpc AuthorizeShell(AuthorizeShellRequest) returns (AuthorizeShellResponse);

    // Approval workflow
    rpc CreateApprovalRequest(CreateApprovalRequest) returns (CreateApprovalResponse);
    rpc ApproveShell(ApproveShellRequest) returns (ApproveShellResponse);
    rpc GetApprovalStatus(GetApprovalStatusRequest) returns (GetApprovalStatusResponse);
    rpc ListPendingApprovals(ListPendingApprovalsRequest) returns (ListPendingApprovalsResponse);
}
```

## CLI Commands

### Shell with Authorization Check

```bash
# Request shell access (automatic authorization check)
$ coral agent shell

# If allowed immediately:
⚠️  WARNING: Entering agent debug shell with elevated privileges.
...
Continue? [y/N] y

# If approval required:
⚠️  Shell access to production requires approval.
Justification: Debugging memory leak in payment service
Approval request created: req-abc123
Waiting for approval... (request expires in 15 minutes)

# Poll for approval or wait
[Approved by security@company.com]
Approval expires in: 1h 0m

⚠️  WARNING: Entering agent debug shell with elevated privileges.
...
Continue? [y/N] y
```

### Approval Management (Approvers)

```bash
# List pending approval requests
$ coral colony approvals list

Pending Shell Access Approvals:
  ID            User              Agent           Environment  Requested
  req-abc123    alice@company.com prod-agent-01  production   5m ago
    Justification: Debugging memory leak in payment service

# Approve a request
$ coral colony approvals approve req-abc123 --reason "Approved for incident response"
✓ Approval granted
  Approval expires: 2025-11-14 19:45:00 UTC (1h from now)
  Token: tok-xyz789

# Deny a request
$ coral colony approvals deny req-abc123 --reason "Insufficient justification"
✓ Request denied
```

## Implementation Plan

### Phase 1: RBAC Policy Engine

- [ ] Define RBAC policy data structures
- [ ] Implement policy evaluation logic
- [ ] Load policies from YAML configuration
- [ ] Store policies in Colony DuckDB
- [ ] Add user role assignment

### Phase 2: Authorization Check

- [ ] Add `AuthorizeShell` RPC to Colony
- [ ] Integrate authorization check into `coral agent shell` CLI
- [ ] Return allowed/denied/approval_required status
- [ ] Handle denial gracefully (show message to user)

### Phase 3: Approval Workflow

- [ ] Create approval request database table
- [ ] Implement `CreateApprovalRequest` RPC
- [ ] Implement `ApproveShell` RPC (approve/deny)
- [ ] Implement `GetApprovalStatus` RPC (polling)
- [ ] Add CLI commands for approvers

### Phase 4: Time-Limited Access

- [ ] Generate approval tokens with expiration
- [ ] Agent validates token before starting shell
- [ ] Automatic expiration (background cleanup task)
- [ ] CLI displays remaining time for approval

### Phase 5: Notifications (Future)

- [ ] Slack integration (notify approvers)
- [ ] Email notifications
- [ ] Webhook support for custom integrations

## Testing Strategy

### Unit Tests

- Policy evaluation (role + environment → permission)
- Approval workflow state machine (pending → approved/denied)
- Token generation and validation
- Expiration logic

### Integration Tests

- Full flow: request → approval → shell access
- Denial flow: request → denied → no shell access
- Expiration: approved → expired → no shell access
- RBAC policy enforcement (dev can't access prod)

### E2E Tests

- Developer tries production (denied)
- SRE requests production (approval required)
- Security approves request (SRE gets shell)
- Admin gets immediate access (no approval)

## Security Considerations

### Token Security

- **Approval tokens**: One-time use, short-lived (1 hour default)
- **Token generation**: Cryptographically secure random (use `crypto/rand`)
- **Token validation**: Agent verifies with Colony before starting shell
- **Revocation**: Approvals can be revoked (future enhancement)

### Approval Abuse

- **Audit logging**: All approval requests/decisions logged (RFD 034)
- **Rate limiting**: Limit approval requests per user (e.g., 10/hour)
- **Justification required**: Users must provide reason for production access
- **Approver accountability**: Approvers recorded in audit log

### Privilege Escalation

- **Role assignment**: Only admins can assign roles
- **Approver authorization**: Only authorized roles can approve
- **Self-approval**: Users cannot approve their own requests
- **Environment detection**: Agent reports environment, Colony validates

## Migration Strategy

1. **Deployment**:
    - Deploy Colony with RBAC engine (default: all users allowed)
    - Gradually enable policies per environment (dev → staging → prod)
    - Train SREs on approval workflow

2. **Rollout phases**:
    - Phase 1: Audit mode (log denials, but allow access)
    - Phase 2: Enforce dev/staging restrictions
    - Phase 3: Enforce production approval requirements
    - Phase 4: Add MFA enforcement (future)

3. **Backward compatibility**:
    - Old CLI without authorization check: Colony returns "allowed" (permissive
      mode)
    - New CLI with old Colony: Skip authorization check, proceed directly

## Future Enhancements

**Deferred to later work:**

- **MFA integration**: Integrate with OIDC provider for MFA challenge
- **Temporary access grants**: Time-limited role assignments (e.g., "grant SRE
  access for 2 hours")
- **Break-glass access**: Emergency access bypass (requires strong audit trail)
- **Slack/PagerDuty integration**: Notify approvers via chat/pages
- **Approval delegation**: Approvers can delegate to others
- **Audit compliance reports**: Generate reports for SOC2/ISO 27001 audits
- **Just-in-time access**: Request access only when needed, auto-revoke after
  use

---

## Example Scenarios

### Scenario 1: Developer Access (Immediate Allow)

```bash
# Alice (developer) tries to shell into dev agent
$ coral agent shell

# Colony checks RBAC:
# - User: alice@company.com → role: developer
# - Agent: dev-agent-01 → environment: dev
# - Policy: developer + dev + shell → ALLOWED

⚠️  WARNING: Entering agent debug shell with elevated privileges.
Continue? [y/N] y

agent $ # Shell starts immediately
```

### Scenario 2: Developer Denied Production

```bash
# Alice (developer) tries to shell into production agent
$ coral agent shell

# Colony checks RBAC:
# - User: alice@company.com → role: developer
# - Agent: prod-agent-01 → environment: production
# - Policy: developer + production + shell → DENIED

❌ Access denied: Developers cannot access production agents.
   Contact your SRE team for production access.
```

### Scenario 3: SRE Production with Approval

```bash
# Bob (SRE) requests production shell
$ coral agent shell
⚠️  Shell access to production requires approval.
Justification: Investigating high memory usage in payment service

Approval request created: req-abc123
Waiting for approval... (request expires in 15 minutes)

# Meanwhile, security team sees notification:
# [Slack] New shell approval request from bob@company.com
#   Agent: prod-payment-01 (production)
#   Reason: Investigating high memory usage in payment service
#   Approve: coral colony approvals approve req-abc123

# Security approves:
$ coral colony approvals approve req-abc123 --reason "Approved for incident INC-12345"

# Bob's CLI continues:
✓ Approved by security@company.com
  Approval expires in: 1h 0m
  Reason: Approved for incident INC-12345

⚠️  WARNING: Entering agent debug shell with elevated privileges.
Continue? [y/N] y

agent $ # Shell starts with approval logged
```
