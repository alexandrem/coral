---
rfd: "002"
title: "Application Identity & Initialization"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "001" ]
database_migrations: [ ]
areas: [ "cli", "security", "configuration" ]
---

# RFD 002 - Application Identity & Initialization

**Status:** 🎉 Implemented

## Summary

Establish a formal application identity model with initialization workflow,
persistent configuration management, and security credentials. This enables
colonies and agents to coordinate across different deployment environments (
local dev, Kubernetes, VMs) while maintaining proper isolation and
authentication between different applications.

## Problem

**Current behavior/limitations:**

- No initialization workflow - users must manually configure colony settings
  every time
- No persistent application identity - each command requires manual `--mesh-id`
  flags
- No secure credential management - mesh IDs from RFD 001 provide discovery but
  no authentication
- No cross-system coordination - deploying agents on Kubernetes requires
  manually copying/distributing credentials
- No isolation guarantees - different applications using Coral could
  accidentally connect to wrong colonies

**Why this matters:**

- **Developer experience**: Running `coral colony start` requires
  knowing/remembering mesh IDs, storage paths, and configuration
- **Security**: RFD 001's mesh_id provides namespace isolation in discovery but
  lacks authentication - anyone with the mesh_id can attempt connections
- **Multi-environment deployments**: Same application deployed in
  dev/staging/prod needs separate identities but shared workflows
- **Kubernetes/distributed deployments**: Agents deployed as sidecars need
  automated config injection without manual setup

**Use cases affected:**

- Developer initializing Coral for first time in their project
- Production deployments where colony runs on separate infrastructure from
  agents
- Kubernetes deployments with agent sidecars across multiple pods
- Teams sharing Coral configurations across development environments
- Preventing cross-application interference when multiple teams use Coral

## Solution

Implement a three-tier configuration system with `coral init` command that
creates persistent application identity:

**Key Design Decisions:**

- **Hierarchical identity**: `application_id` (human-readable) → `environment` →
  `colony_id` (globally unique UUID)
    - Enables same app to have separate dev/staging/prod colonies
    - Colony ID serves as mesh_id (from RFD 001) for discovery service
      registration

- **Three-tier config**: Global user config (`~/.coral/config.yaml`) +
  per-colony config (`~/.coral/colonies/<colony-id>.yaml`) + project-local
  config (`<project>/.coral/config.yaml`)
    - Global: User preferences, default colony, discovery endpoint
    - Per-colony: Identity, credentials, WireGuard keys (can live anywhere)
    - Project-local: Links project directory to a colony (optional)

- **Security-first**: Colony secret + WireGuard keypair generated at init
    - Colony secret: Shared secret for agent authentication (separate from
      discovery)
    - WireGuard keys: Network-level encryption for control mesh
    - Public key registered with discovery service (RFD 001), private key never
      leaves colony

- **Export/import workflow**: `coral colony export` generates env vars for
  remote deployment
    - Supports Kubernetes secrets, systemd environment files, docker-compose
    - Secrets never stored in version control

**Benefits:**

- One-time initialization per application eliminates repeated manual config
- Secure by default - authentication required for agent registration
- Kubernetes-friendly - environment variable injection pattern
- Multi-environment support - same workflow for dev/staging/prod
- Isolation guaranteed - each colony has unique credentials and network
  namespace

**Architecture Overview:**

```
┌─────────────────────────────────────────────────────────────┐
│  Developer Machine                                          │
│                                                             │
│  $ coral init my-shop --env production                      │
│                                                             │
│  ┌────────────────────────────────────────────────────┐    │
│  │  ~/.coral/                                         │    │
│  │  ├── config.yaml (global user config)              │    │
│  │  └── colonies/                                     │    │
│  │      └── my-shop-production-a3f2e1.yaml ──────┐    │    │
│  │          - colony_id: my-shop-production-a3f2e1    │    │
│  │          - colony_secret: <secret>             │    │    │
│  │          - wireguard_private_key: <key>        │    │    │
│  │          - wireguard_public_key: <key>         │    │    │
│  └────────────────────────────────────────────────────┘    │
│                                                      │       │
│  $ coral colony export my-shop-production         │       │
│  > CORAL_COLONY_ID=my-shop-production-a3f2e1        │       │
│  > CORAL_COLONY_SECRET=<secret>                     │       │
│                                                      │       │
└──────────────────────────────────────────────────────┘      │
                                                        │       │
                                                        ▼       │
┌─────────────────────────────────────────────────────────────┐
│  Kubernetes Cluster (uses exported credentials)            │
│                                                             │
│  apiVersion: v1                                             │
│  kind: Secret                                               │
│  metadata:                                                  │
│    name: coral-secrets                                      │
│  data:                                                      │
│    colony_id: <base64>                                      │
│    colony_secret: <base64>  ◄──── from export               │
│                                                             │
│  apiVersion: v1                                             │
│  kind: Pod                                                  │
│  spec:                                                      │
│    containers:                                              │
│    - name: coral-agent                                      │
│      env:                                                   │
│      - name: CORAL_COLONY_ID                                │
│        valueFrom:                                           │
│          secretKeyRef:                                      │
│            name: coral-secrets                              │
│            key: colony_id                                   │
│      - name: CORAL_COLONY_SECRET                            │
│        valueFrom:                                           │
│          secretKeyRef:                                      │
│            name: coral-secrets                              │
│            key: colony_secret                               │
└─────────────────────────────────────────────────────────────┘
```

**Integration with RFD 001 (Discovery Service):**

1. **Initialization** (`coral init`):
    - Generates `colony_id` (UUID-based, globally unique)
    - This colony_id becomes the `mesh_id` used in RFD 001 discovery service
    - Generates WireGuard keypair and colony_secret

2. **Colony startup** (`coral colony start`):
    - Reads colony_id from config
    - Registers with discovery service (RFD 001) using colony_id as mesh_id
    - Publishes WireGuard public key to discovery (for mesh formation)
    - Colony secret remains local (never sent to discovery)

3. **Agent connection** (`coral connect`):
    - Agent receives colony_id and colony_secret (via config or env vars)
    - Queries discovery service using colony_id as mesh_id (RFD 001 lookup flow)
    - Verifies colony's WireGuard public key matches expected
    - Establishes encrypted tunnel, then authenticates using colony_secret

**Security Layers:**

```
Layer 1: Discovery namespace isolation (RFD 001 mesh_id)
         ↓ Prevents accidental cross-application connections

Layer 2: WireGuard public key verification
         ↓ Prevents mesh_id hijacking/impersonation

Layer 3: Colony secret authentication (RFD 002)
         ↓ Prevents unauthorized agents joining mesh
```

### Component Changes

1. **CLI** (new commands):
    - `coral init <app-name>`: Initialize new colony with identity
    - `coral colony list`: List all configured colonies
    - `coral colony use <colony-id>`: Set default colony
    - `coral colony export <colony-id>`: Export credentials for remote
      deployment
    - `coral colony import`: Import colony from credentials

2. **Configuration System** (new):
    - Global config loader (`~/.coral/config.yaml`)
    - Per-colony config loader (`~/.coral/colonies/<colony-id>.yaml`)
    - Project-local config resolver (`.coral/config.yaml` in project dir)
    - Credential management (generate, store, export)

3. **Colony** (initialization integration):
    - Read colony_id from config (via new config system)
    - Load WireGuard keys from per-colony config
    - Register with discovery using colony_id as mesh_id (RFD 001)
    - Accept agent registrations with secret verification

4. **Agent** (authentication integration):
    - Load colony_id and colony_secret from config or env vars
    - Query discovery using colony_id as mesh_id (RFD 001)
    - Verify colony's public key before connecting
    - Present colony_secret during registration handshake

**Configuration Examples:**

**Global config** (`~/.coral/config.yaml`):

```yaml
# User-level settings
default_colony: my-shop-production-a3f2e1

discovery:
    endpoint: https://discovery.coral.io

ai:
    provider: anthropic
    api_key_source: env  # or 'keychain', 'file'
```

**Per-colony config** (`~/.coral/colonies/my-shop-production-a3f2e1.yaml`):

```yaml
# Colony identity
colony_id: my-shop-production-a3f2e1  # Used as mesh_id in RFD 001
application_name: my-shop
environment: production

# Security credentials (never commit to git)
colony_secret: "abc123-secure-random-token-xyz789"
wireguard:
    private_key: "base64-encoded-private-key"
    public_key: "base64-encoded-public-key"  # Registered with discovery
    port: 41820

# Storage location
storage_path: ~/projects/my-shop/.coral

# Discovery settings (references RFD 001)
discovery:
    enabled: true
    mesh_id: my-shop-production-a3f2e1  # Same as colony_id
    auto_register: true
    register_interval: 60s

# Metadata
created_at: 2025-10-28T10:00:00Z
created_by: alex@hostname
```

**Project-local config** (`~/projects/my-shop/.coral/config.yaml`):

```yaml
# Links this project directory to a colony
colony_id: my-shop-production-a3f2e1

# Optional local overrides
dashboard:
    port: 3000

storage:
    path: .coral/colony.duckdb
```

## API Changes

### New Protobuf Messages

**File: `proto/coral/mesh/v1/auth.proto`**

```protobuf
syntax = "proto3";
package coral.mesh.v1;

import "google/protobuf/timestamp.proto";

option go_package = "github.com/coral-io/coral/proto/mesh/v1;meshpb";

// Agent authentication during registration
message RegisterRequest {
    // Agent identification
    string agent_id = 1;
    string component_name = 2;  // "frontend", "api", "database"

    // Authentication (new in RFD 002)
    string colony_id = 3;       // Must match colony's ID
    string colony_secret = 4;   // Shared secret for auth

    // Agent metadata
    string version = 5;
    map<string, string> labels = 6;

    // WireGuard (for mesh coordination)
    string wireguard_pubkey = 7;
}

message RegisterResponse {
    // Authentication result
    bool accepted = 1;
    string reason = 2;  // If rejected: "invalid_secret", "wrong_colony", "unauthorized"

    // Mesh assignment (if accepted)
    string assigned_ip = 3;      // Agent's IP in WireGuard mesh (e.g., "10.100.0.42")
    string mesh_subnet = 4;      // Colony's mesh subnet (e.g., "10.100.0.0/16")
    repeated PeerInfo peers = 5; // Other agents in mesh

    // Colony info
    google.protobuf.Timestamp registered_at = 6;
}

message PeerInfo {
    string agent_id = 1;
    string component_name = 2;
    string mesh_ip = 3;
    string wireguard_pubkey = 4;
}
```

### Configuration File Schema

**Global config schema** (`~/.coral/config.yaml`):

```yaml
# Schema version for compatibility
version: "1"

# Default colony for commands
default_colony: <colony-id>  # Optional

# Discovery service configuration
discovery:
    endpoint: https://discovery.coral.io  # RFD 001 discovery service
    timeout: 10s

# AI provider settings
ai:
    provider: anthropic  # or openai
    api_key_source: env  # env, keychain, file

# User preferences
preferences:
    auto_update_check: true
    telemetry_enabled: false
```

**Per-colony config schema** (`~/.coral/colonies/<colony-id>.yaml`):

```yaml
# Schema version
version: "1"

# Identity
colony_id: <uuid>           # Unique colony identifier (also used as mesh_id in RFD 001)
application_name: <string>  # Human-readable app name
environment: <string>       # dev, staging, production, etc.

# Security credentials
colony_secret: <string>     # Shared secret for agent auth (NEVER in discovery)
wireguard:
    private_key: <base64>     # WireGuard private key (NEVER in discovery)
    public_key: <base64>      # WireGuard public key (published to discovery)
    port: 41820               # WireGuard listen port

# Storage
storage_path: <path>        # Where DuckDB and state files live

# Discovery (RFD 001 integration)
discovery:
    enabled: true
    mesh_id: <colony-id>      # Must match colony_id (used in RFD 001 registration)
    auto_register: true       # Auto-register with discovery on startup
    register_interval: 60s    # Re-register frequency (RFD 001 heartbeat)

# Metadata
created_at: <timestamp>
created_by: <user@host>
last_used: <timestamp>
```

**Project-local config schema** (`<project>/.coral/config.yaml`):

```yaml
# Schema version
version: "1"

# Colony reference
colony_id: <uuid>  # Links this project to a colony

# Local overrides (optional)
dashboard:
    port: 3000
    enabled: true

storage:
    path: .coral/colony.duckdb  # Relative to project root
```

### CLI Commands

```bash
# Initialize a new colony
coral init <app-name> [flags]
  --env <environment>     # Environment name (default: "dev")
  --storage <path>        # Storage directory (default: ".coral")
  --discovery <url>       # Discovery service URL (default: https://discovery.coral.io)

# Example output:
$ coral init my-shop --env production

Initializing Coral colony...
✓ Created colony ID: my-shop-production-a3f2e1
✓ Generated WireGuard keypair
✓ Created colony secret
✓ Configuration saved to ~/.coral/colonies/my-shop-production-a3f2e1.yaml

Colony initialized successfully!

To start the colony:
  coral colony start

To connect agents:
  coral connect <service> --colony my-shop-production-a3f2e1

For remote agents (Kubernetes, VMs), export credentials:
  coral colony export my-shop-production-a3f2e1

---

# List configured colonies
coral colony list [flags]
  --json    # Output as JSON

# Example output:
$ coral colony list

Configured Colonies:
  my-shop-production-a3f2e1 (production) [default]
    Application: my-shop
    Created: 2025-10-28 10:00:00
    Storage: ~/projects/my-shop/.coral

  my-shop-dev-b2c4f3 (dev)
    Application: my-shop
    Created: 2025-10-27 14:30:00
    Storage: ~/projects/my-shop-dev/.coral

---

# Set default colony
coral colony use <colony-id>

# Example output:
$ coral colony use my-shop-production-a3f2e1

✓ Default colony set to: my-shop-production-a3f2e1

---

# Show current default colony
coral colony current

# Example output:
$ coral colony current

Current Colony:
  ID: my-shop-production-a3f2e1
  Application: my-shop
  Environment: production
  Storage: ~/projects/my-shop/.coral
  Discovery: https://discovery.coral.io (mesh_id: my-shop-production-a3f2e1)

---

# Export colony credentials for remote deployment
coral colony export <colony-id> [flags]
  --format <format>    # env (default), yaml, json, k8s

# Example output (env format):
$ coral colony export my-shop-production-a3f2e1 --format env

# Coral Colony Credentials
# Generated: 2025-10-28 15:30:00
# SECURITY: Keep these credentials secure. Do not commit to version control.

export CORAL_COLONY_ID="my-shop-production-a3f2e1"
export CORAL_COLONY_SECRET="abc123-secure-random-token-xyz789"
export CORAL_DISCOVERY_ENDPOINT="https://discovery.coral.io"

# To use in your shell:
#   eval $(coral colony export my-shop-production-a3f2e1)

# Example output (k8s format):
$ coral colony export my-shop-production-a3f2e1 --format k8s

apiVersion: v1
kind: Secret
metadata:
  name: coral-secrets
  namespace: my-shop-prod
type: Opaque
stringData:
  colony-id: my-shop-production-a3f2e1
  colony-secret: abc123-secure-random-token-xyz789
  discovery-endpoint: https://discovery.coral.io

---

# Import colony from credentials (for remote systems)
coral colony import [flags]
  --colony-id <id>      # Colony ID
  --secret <secret>     # Colony secret
  --stdin               # Read from stdin (for piping)

# Example:
$ coral colony import \
    --colony-id my-shop-production-a3f2e1 \
    --secret abc123-secure-random-token-xyz789

✓ Colony configuration imported
✓ Saved to ~/.coral/colonies/my-shop-production-a3f2e1.yaml

Note: The colony's WireGuard public key will be retrieved from discovery service on first connection.
      The colony's private key never leaves the colony and is not needed by agents.
```

### Environment Variable Support

All commands support environment variable overrides:

```bash
# Colony identification
CORAL_COLONY_ID=<colony-id>            # Override colony ID
CORAL_COLONY_SECRET=<secret>           # Agent authentication secret

# Discovery (RFD 001)
CORAL_DISCOVERY_ENDPOINT=<url>         # Discovery service URL
CORAL_DISCOVERY_MESH_ID=<mesh-id>      # Explicit mesh_id (defaults to colony_id)

# Storage
CORAL_STORAGE_PATH=<path>              # Override storage location

# Examples:
export CORAL_COLONY_ID=my-shop-production-a3f2e1
export CORAL_COLONY_SECRET=abc123-secure-token-xyz789
coral colony start  # Uses env vars

# Kubernetes deployment pattern:
apiVersion: v1
kind: Pod
spec:
  containers:
  - name: coral-agent
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

## Implementation Plan

### Phase 1: Configuration System

- [ ] Define configuration file schemas (YAML structures)
- [ ] Implement config loader (global, per-colony, project-local)
- [ ] Add config validation and migration logic
- [ ] Implement credential generation (colony_secret, WireGuard keys)
- [ ] Add config file I/O with proper permissions (0600 for secrets)

### Phase 2: CLI Commands

- [ ] Implement `coral init` command
- [ ] Implement `coral colony list` command
- [ ] Implement `coral colony use` command
- [ ] Implement `coral colony current` command
- [ ] Implement `coral colony export` with multiple formats
- [ ] Implement `coral colony import` command
- [ ] Add environment variable override support

### Phase 3: Colony Integration

- [ ] Update colony startup to load config from new system
- [ ] Integrate colony_id as mesh_id for RFD 001 discovery registration
- [ ] Implement agent authentication using colony_secret
- [ ] Add public key verification in agent handshake
- [ ] Update colony to persist registered agents

### Phase 4: Agent Integration

- [ ] Update agent to load colony_id and secret from config/env vars
- [ ] Implement discovery lookup using colony_id as mesh_id (RFD 001)
- [ ] Add public key verification before WireGuard connection
- [ ] Implement secret-based authentication in RegisterRequest
- [ ] Handle authentication failures with retry logic

### Phase 5: Testing & Documentation

- [ ] Unit tests for config loading and validation
- [ ] Unit tests for credential generation
- [ ] Integration tests for init → colony start → agent connect flow
- [ ] E2E test: Local dev environment (init + start + connect)
- [ ] E2E test: Kubernetes deployment (export + import + connect)
- [ ] Security tests: Wrong secret, wrong colony_id, public key mismatch
- [ ] Documentation: Getting started guide with init workflow
- [ ] Documentation: Kubernetes deployment guide with secret management

## Testing Strategy

### Unit Tests

- Config file parsing and validation (all three tiers)
- Credential generation (colony_secret, WireGuard keys)
- Colony ID generation (uniqueness, format)
- Config resolution priority (env vars > project > per-colony > global)
- Export format generation (env, yaml, json, k8s)

### Integration Tests

- `coral init` creates valid config files
- `coral colony export/import` round-trip
- Colony startup loads correct config from default/specified colony
- Agent authentication with valid/invalid secrets
- Public key verification during handshake
- Multiple colonies with different credentials (isolation)

### E2E Tests

**Scenario 1: Local Development**

```bash
# Initialize and start
coral init my-app --env dev
coral colony start
coral connect frontend --port 3000

# Verify: Agent successfully authenticates and joins mesh
```

**Scenario 2: Kubernetes Deployment**

```bash
# Developer exports credentials
coral colony export my-app-prod --format k8s > secret.yaml

# Apply to cluster
kubectl apply -f secret.yaml

# Agent pod starts with env vars from secret
# Verify: Agent authenticates using injected credentials
```

**Scenario 3: Wrong Credentials**

```bash
# Agent attempts connection with wrong secret
CORAL_COLONY_SECRET=wrong-secret coral connect api

# Verify: Registration rejected with "invalid_secret" error
```

## Security Considerations

### Threat Model

**Threat: Discovery service compromise**

- **Mitigation**: Colony secret never sent through discovery service
- **Additional**: Public key verification prevents impersonation

**Threat: Credential theft from config files**

- **Mitigation**: Config files created with 0600 permissions (user-only
  read/write)
- **Mitigation**: Credentials stored in `~/.coral/colonies/` (not in project
  directory)
- **Recommendation**: Use OS keychain integration (future enhancement)

**Threat: Cross-application interference**

- **Mitigation**: Each colony has unique colony_id and colony_secret
- **Mitigation**: WireGuard mesh isolation (separate subnet per colony)

**Threat: Mesh ID hijacking (RFD 001)**

- **Mitigation**: Agent verifies colony's WireGuard public key matches expected
- **Mitigation**: Colony secret authentication required after WireGuard
  handshake

**Threat: Credential exposure in Kubernetes**

- **Mitigation**: Use Kubernetes Secrets (base64, encrypted at rest if enabled)
- **Recommendation**: External secret managers (Vault, AWS Secrets Manager)
- **Best practice**: RBAC restrictions on secret access

### Authentication Flow

```
1. Agent reads colony_id and colony_secret (from config or env vars)
   ↓
2. Agent queries discovery service: "Where is colony_id X?" (RFD 001)
   ↓
3. Discovery returns: WireGuard public key + endpoints
   ↓
4. Agent verifies public key matches expected
   ↓ (if mismatch: abort with "colony_impersonation" error)
   ↓
5. Agent establishes WireGuard tunnel (encrypted control plane)
   ↓
6. Agent sends RegisterRequest with colony_id + colony_secret
   ↓
7. Colony verifies:
   - colony_id matches
   - colony_secret matches
   ↓ (if invalid: reject with "invalid_secret")
   ↓
8. Colony responds: RegisterResponse with accepted=true
   ↓
9. Agent is now part of mesh, can send/receive data
```

### Credential Management Best Practices

**Development:**

- Use `coral init` to generate credentials (automatic, secure)
- Credentials stored in `~/.coral/colonies/` (outside version control)
- Use `.gitignore` to exclude `.coral/` from project repositories

**Production:**

- Export credentials using `coral colony export`
- Store in secure secret management system (Vault, AWS Secrets Manager)
- Inject into containers/VMs via environment variables
- Rotate colony_secret periodically (manual process initially)

**Kubernetes:**

```yaml
# DO NOT commit credentials to git
# Instead, create secret manually or via CI/CD:
kubectl create secret generic coral-secrets \
--from-literal=colony-id=my-shop-production-a3f2e1 \
--from-literal=colony-secret=abc123-token-xyz789 \
--namespace my-shop-prod
```

## Migration Strategy

**New installations:**

1. Run `coral init <app-name>` (creates all config)
2. Run `coral colony start` (uses new config system)
3. Run `coral connect <service>` (uses new config system)

**Future deprecation of manual flags:**

- Initially: Support both new config system and legacy `--mesh-id` flags
- Warning period: Deprecation warnings when using legacy flags
- Eventually: Remove legacy flags in favor of config-only approach

**Backward compatibility:**

- RFD 001 discovery service unchanged (mesh_id → colony_id)
- Existing mesh_id values can be used as colony_id
- Migration path:
  `coral colony import --colony-id <old-mesh-id> --secret <new-secret>`

## Future Enhancements

### Credential Rotation

- `coral colony rotate-secret <colony-id>`: Generate new colony_secret
- Graceful rotation: Support old + new secret during transition period
- Automated rotation policies (e.g., every 90 days)

### Multi-Colony Queries (See RFD 003)

Enable querying across multiple colonies for cross-environment comparison and unified insights:

```bash
# Compare prod vs staging
coral ask "why is API slower in prod than staging?" \
  --colonies my-shop-production,my-shop-staging

# Query all environments
coral ask "show recent errors" --group my-shop

# Query all managed applications
coral ask "what's unhealthy?" --all-colonies
```

**Implementation:**
- CLI loads multiple colony configs
- Queries each colony's local DuckDB
- Aggregates results and passes to AI for correlation
- No persistent cross-colony storage (stateless)

**Future evolution:**
- See RFD 003 for Reef (multi-colony federation) with persistent correlation
- See RFD 004 for MCP server integration for external tool access

### Colony Groups

Add colony grouping to global config for easier multi-colony queries:

```yaml
# ~/.coral/config.yaml
colony_groups:
  my-shop:
    - my-shop-production-a3f2e1
    - my-shop-staging-b7c8d2
    - my-shop-dev-f9e4a1

  all-prod:
    - my-shop-production-a3f2e1
    - payments-api-prod-c2d5e8
    - frontend-prod-d6f1b3
```

Usage:
```bash
coral ask "compare latency" --group my-shop
coral ask "any errors in prod?" --group all-prod
```

### Multi-Region Colonies

- Colony ID remains same, but multiple WireGuard endpoints per region
- Discovery service returns region-aware endpoints
- Agents connect to nearest/fastest colony endpoint

### OS Keychain Integration

- Store colony_secret in macOS Keychain, Windows Credential Manager, Linux
  Secret Service
- Eliminates plaintext secrets in config files
- `coral init --keychain` option

### Team Sharing

- `coral colony share <colony-id> <user>`: Generate user-specific tokens
- Role-based access control (read-only vs full access)
- Audit logging for credential access

### Secret Backend Plugins

- HashiCorp Vault integration
- AWS Secrets Manager integration
- Azure Key Vault integration
- Custom secret backend interface

## Appendix

### Configuration File Locations

**Priority order** (highest to lowest):

1. Environment variables (`CORAL_COLONY_ID`, etc.)
2. Project-local config (`.coral/config.yaml` in current directory)
3. Explicit `--config` flag
4. Per-colony config (`~/.coral/colonies/<colony-id>.yaml`)
5. Global user config (`~/.coral/config.yaml`)

**Resolution example:**

```bash
# Scenario: User runs "coral colony start" in ~/projects/my-shop

1. Check CORAL_COLONY_ID env var → not set
2. Check .coral/config.yaml → colony_id: my-shop-prod-a3f2e1
3. Load per-colony config: ~/.coral/colonies/my-shop-prod-a3f2e1.yaml
4. Load global config: ~/.coral/config.yaml (for defaults)
5. Merge: per-colony overrides global, project-local overrides both
```

### Colony ID Format

**Format:** `<app-name>-<environment>-<short-uuid>`

**Examples:**

- `my-shop-production-a3f2e1`
- `payments-api-staging-b7d9c2`
- `frontend-dev-f4e1a8`

**Generation algorithm:**

```
colony_id = normalize(app_name) + "-" + normalize(environment) + "-" + random_hex(6)

normalize(s) = lowercase(s).replace(/[^a-z0-9]+/, "-")
random_hex(n) = hex(random_bytes(n/2))  // 6 hex chars = 3 bytes
```

**Uniqueness guarantee:**

- 6 hex chars = 16.7 million combinations
- Collision probability negligible for typical deployments
- Discovery service rejects duplicate registrations

### Integration with RFD 001 Discovery Service

**How colony_id maps to mesh_id:**

RFD 001 defined `mesh_id` as the discovery namespace. RFD 002 defines
`colony_id` as the application identity. These are the same value:

```go
// Colony registration with discovery (RFD 001)
func (c *Colony) registerWithDiscovery() error {
    req := &discoverypb.RegisterColonyRequest{
        MeshID:    c.config.ColonyID, // RFD 002 colony_id used as RFD 001 mesh_id
        PubKey:    c.wireguard.PublicKey,
        Endpoints: c.wireguard.Endpoints,
    }
    return c.discoveryClient.RegisterColony(req)
}

// Agent lookup (RFD 001)
func (a *Agent) lookupColony() error {
    req := &discoverypb.LookupColonyRequest{
        MeshID: a.config.ColonyID, // RFD 002 colony_id used as RFD 001 mesh_id
    }
    resp, err := a.discoveryClient.LookupColony(req)

    // Verify public key (RFD 002 security)
    if !bytes.Equal(resp.PubKey, a.expectedPubKey) {
        return ErrColonyImpersonation
    }

    return nil
}
```

**Why use the same value:**

- Simplifies mental model: One identifier for the colony
- Discovery lookup naturally isolated per colony
- Reduces configuration complexity

**Security difference:**

- RFD 001: `mesh_id` is public (anyone can lookup)
- RFD 002: `colony_secret` is private (required for authentication)

### Example Deployment Scenarios

**Scenario 1: Solo Developer**

```bash
# One-time setup
cd ~/projects/my-app
coral init my-app --env dev

# Daily workflow
coral colony start          # Starts in background
coral connect frontend &    # Connect services
coral connect api &
coral ask "is everything healthy?"
```

**Scenario 2: Team Development**

```bash
# Team lead initializes colony
coral init team-app --env dev
coral colony export team-app-dev > credentials.env

# Team members import (via secure channel, not git)
coral colony import --stdin < credentials.env
coral colony start  # Everyone uses same colony
```

**Scenario 3: Production Kubernetes**

```bash
# DevOps engineer exports credentials
coral colony export my-app-prod --format k8s > k8s-secret.yaml

# Apply via CI/CD (not committed to git)
kubectl apply -f k8s-secret.yaml

# Deployment references secret
apiVersion: v1
kind: Pod
spec:
  containers:
  - name: api
    image: myapp/api:v2.0.0
  - name: coral-agent
    image: coral/agent:latest
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

---

## Notes

**Design Philosophy:**

- **Secure by default**: Credentials generated automatically, stored safely
- **Developer-friendly**: One command (`coral init`) to get started
- **Kubernetes-native**: Environment variable injection pattern
- **Isolation-first**: Each colony completely independent

**Relationship to RFD 001:**

RFD 001 provides discovery infrastructure (mesh_id registration/lookup).
RFD 002 provides application identity and authentication layer on top.

Together they enable:

- Discovery: Colonies and agents find each other (RFD 001)
- Authentication: Only authorized agents can join (RFD 002)
- Isolation: Different applications never cross paths (RFD 001 + RFD 002)
