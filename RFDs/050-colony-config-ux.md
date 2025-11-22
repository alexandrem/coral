---
rfd: "050"
title: "Colony Config UX Improvements"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: [ ]
database_migrations: [ ]
areas: [ "cli", "config" ]
---

# RFD 050 - Colony Config UX Improvements

**Status:** 🚧 Draft

## Summary

Enhance the coral CLI config management UX with kubectl-inspired patterns,
including a unified `coral config` command family, improved visual indicators
for the active colony, config validation, and better visibility into the config
resolution path.

## Problem

**Current behavior/limitations:**

- Colony management commands are scattered under `coral colony` (list, use,
  current) with no unified config namespace
- No visual indicator in `colony list` output showing which colony is currently
  active (users must run `colony current` separately)
- Config resolution happens silently—users don't know if the active colony comes
  from env var, project config, or global default
- Missing critical config operations: validate, view merged config,
  rename/delete colonies
- No `CORAL_CONFIG` env var to override config file location (unlike
  `KUBECONFIG`)
- `colony current` shows limited info, doesn't explain *why* this colony was
  selected

**Why this matters:**

- Users managing multiple colonies (dev/staging/prod) need faster context
  switching and better awareness of which colony is active
- Silent config resolution causes confusion when the wrong colony is used (e.g.,
  project config overrides global default unexpectedly)
- No validation command means config errors are only caught at runtime
- Lack of kubectl-familiar patterns increases cognitive load for Kubernetes
  users

**Use cases affected:**

- **Multi-environment workflows**: Developers switching between dev/staging/prod
  colonies need quick visual feedback
- **Debugging connectivity issues**: Users need to understand which config
  source won the resolution priority
- **Config migration**: No way to rename colony IDs or clean up old colonies
  without manual file operations
- **CI/CD pipelines**: Scripts need reliable ways to validate configs before
  deployment

## Solution

Introduce a `coral config` command family alongside existing `coral colony`
commands, inspired by kubectl's battle-tested UX patterns.

**Key Design Decisions:**

- **Keep backward compatibility**: Existing `coral colony` commands remain
  unchanged; `coral config` adds new capabilities
- **Kubectl-inspired but Coral-specific**: Borrow proven patterns (get-contexts,
  current-context) but adapt to Coral's three-tier config model
- **Resolution transparency**: All commands show *why* a colony is active (env
  var vs. project vs. global)
- **Non-destructive by default**: Rename/delete operations require confirmation
  flags

**Benefits:**

- **Faster context switching**: Visual indicators and familiar commands reduce
  cognitive overhead
- **Better debugging**: Explicit resolution path helps diagnose config issues
- **Safer operations**: Validation catches errors before runtime; confirmation
  flags prevent accidents
- **Kubectl user familiarity**: Reduces onboarding friction for Kubernetes
  operators

**Architecture Overview:**

```
Directory Structure (current):
  ~/.coral/
  ├── config.yaml                         # Global config (default_colony, discovery, ai)
  └── colonies/
      └── <colony-id>/
          ├── config.yaml                 # Colony config (wireguard, services, mcp)
          ├── ca/                         # Colony CA infrastructure (RFD 047)
          │   ├── root-ca.crt
          │   └── intermediate/
          └── data/                       # Colony runtime data

Commands:
  coral config get-contexts    → Lists all colonies with current marked (*)
  coral config current-context → Shows active colony ID + resolution source
  coral config use-context     → Alias for 'colony use' (kubectl parity)
  coral config view            → Shows merged config with resolution annotations
  coral config validate        → Validates all colony configs
  coral config delete-context  → Removes colony (interactive prompt, type name to confirm)

Note: No rename-context (colony IDs are cryptographically bound to certificates)

Environment:
  CORAL_CONFIG → Override config directory (default: ~/.coral)
```

### Component Changes

1. **CLI Commands** (`internal/cli/config/`):

    - New `config.go` with command family registration
    - `get-contexts`: Lists colonies with current marked by `*`, shows
      resolution source
    - `current-context`: Prints active colony ID only (scriptable) with
      `--verbose` for details
    - `use-context`: Thin wrapper around existing `colony use` logic
    - `view`: Shows merged config YAML with comments indicating source (
      env/project/global)
    - `validate`: Runs validation on all colony configs, reports errors
    - `delete-context`: Removes entire colony directory (interactive prompt,
      must type colony name)

2. **Config Resolver** (`internal/config/resolver.go`):

    - Add `ResolveWithSource()` method returning
      `(colonyID string, source string, error)`
    - Source values: `"env:CORAL_COLONY_ID"`, `"project:.coral/config.yaml"`,
      `"global:~/.coral/config.yaml"`
    - Expose resolution logic for use in display commands

3. **Config Loader** (`internal/config/loader.go`):

    - Add `CORAL_CONFIG` env var support to override base directory
    - Add `DeleteColonyDir()` method to remove entire colony directory (config,
      CA, data)
    - Add `ValidateAll()` method returning validation errors per colony

4. **Existing Colony Commands** (`internal/cli/colony/colony.go`):
    - Update `colony list` to show `*` marker for current colony
    - Update `colony current` to show resolution source with `--verbose` flag
    - No breaking changes—only additive enhancements

**Configuration Example:**

```bash
# List all colonies with current marked
$ coral config get-contexts
CURRENT   COLONY-ID              APPLICATION   ENVIRONMENT   RESOLUTION
*         myapp-prod-abc123      myapp         production    global
          myapp-dev-xyz789       myapp         development   -
          webapp-staging-def456  webapp        staging       -

# Show why current colony was selected
$ coral config current-context --verbose
myapp-prod-abc123
Resolution: global default (~/.coral/config.yaml)

# With project override
$ coral config current-context --verbose
myapp-dev-xyz789
Resolution: project (.coral/config.yaml)

# View merged config with annotations
$ coral config view
# Colony: myapp-prod-abc123
# Resolution: global default

colony_id: myapp-prod-abc123         # from colony config
application_name: myapp               # from colony config
environment: production               # from colony config
colony_secret: ***                    # from env:CORAL_COLONY_SECRET
discovery:
  endpoint: https://discovery.coral.dev  # from global config
storage:
  path: /data/coral                   # from project config

# Validate all configs
$ coral config validate
✓ myapp-prod-abc123: valid
✓ myapp-dev-xyz789: valid
✗ webapp-staging-def456: invalid mesh subnet "10.100.0.0/8" (must be /16)

# Delete colony (requires typing colony name to confirm)
$ coral config delete-context myapp-dev-xyz789
⚠️  This will permanently delete colony "myapp-dev-xyz789" including:
   - Config, CA certificates, and all colony data
   - Directory: ~/.coral/colonies/myapp-dev-xyz789/

To confirm, type the colony name: myapp-dev-xyz789
✓ Deleted colony: myapp-dev-xyz789
```

### Colony Identity is Immutable

Colony IDs are **cryptographically bound** to the identity infrastructure:

- **X.509 Certificates**: Colony ID embedded in SPIFFE SAN (
  `spiffe://coral/colony/{id}/agent/{agent}`)
- **Discovery Service**: Agents lookup colonies by `mesh_id` which equals
  `colony_id`
- **JWT Bootstrap Tokens**: Tokens include `colony_id` in claims
- **Database Records**: Certificate audit tables keyed by `colony_id`

**Migration Pattern**: Use existing `coral colony export/import` for credential
portability:

```bash
# Export credentials for deployment (supports: env, yaml, json, k8s formats)
coral colony export myapp-prod --format k8s > secret.yaml

# Import on remote system
coral colony import --colony-id myapp-prod --secret <secret>
```

To migrate to a new colony, create one with `coral init` and re-bootstrap
agents.

## Implementation Plan

### Phase 1: Foundation

- [ ] Add `CORAL_CONFIG` env var support to `config.Loader`
- [ ] Add `ResolveWithSource()` method to `config.Resolver`
- [ ] Add `ValidateAll()`, `DeleteColonyDir()` to `config.Loader`
- [ ] Add unit tests for new config methods

### Phase 2: Core Commands

- [ ] Create `internal/cli/config/config.go` with command registration
- [ ] Implement `coral config get-contexts` with current marker
- [ ] Implement `coral config current-context` with resolution source
- [ ] Implement `coral config use-context` (alias to `colony use`)
- [ ] Implement `coral config view` with merged config display

### Phase 3: Advanced Commands

- [ ] Implement `coral config validate` with error reporting
- [ ] Implement `coral config delete-context` with interactive prompt (type name
  to confirm)
- [ ] Add `--verbose` flag to `colony current` for resolution info

### Phase 4: Visual Enhancements

- [ ] Update `colony list` to show `*` marker for current colony
- [ ] Add resolution source column to `colony list` output
- [ ] Ensure consistent table formatting across commands

### Phase 5: Testing & Documentation

- [ ] Add unit tests for all config commands
- [ ] Add integration tests for config resolution priority
- [ ] Add E2E tests for multi-colony workflows
- [ ] Update CLI docs with new commands
- [ ] Add migration guide from `colony` to `config` commands

## API Changes

### CLI Commands

```bash
# Get all colonies with current context marked
coral config get-contexts [--json]

# Example output:
CURRENT   COLONY-ID              APPLICATION   ENVIRONMENT   RESOLUTION
*         myapp-prod-abc123      myapp         production    global
          myapp-dev-xyz789       myapp         development   -

# JSON output:
{
  "current_colony": "myapp-prod-abc123",
  "resolution_source": "global",
  "colonies": [
    {
      "colony_id": "myapp-prod-abc123",
      "application": "myapp",
      "environment": "production",
      "is_current": true,
      "resolution": "global"
    },
    ...
  ]
}

# Show current context with resolution info
coral config current-context [--verbose]

# Example output (default):
myapp-prod-abc123

# Example output (--verbose):
myapp-prod-abc123
Resolution: global default (~/.coral/config.yaml)

# Switch colony (alias for 'colony use')
coral config use-context <colony-id>

# Example output:
✓ Default colony set to: myapp-dev-xyz789

# View merged config
coral config view [--colony <id>] [--raw]

# Example output:
# Colony: myapp-prod-abc123
# Resolution: global default
#
# Config sources (priority order):
#   1. Environment variables (highest)
#   2. Project config (.coral/config.yaml) - not present
#   3. Colony config (~/.coral/colonies/myapp-prod-abc123.yaml)
#   4. Global config (~/.coral/config.yaml)

colony_id: myapp-prod-abc123         # colony
application_name: myapp               # colony
environment: production               # colony
...

# Validate all configs
coral config validate [--json]

# Example output:
✓ myapp-prod-abc123: valid
✓ myapp-dev-xyz789: valid
✗ webapp-staging-def456: error: invalid mesh subnet "10.100.0.0/8" (must be /16)

Validation summary: 2 valid, 1 invalid

# Delete colony (interactive confirmation required)
coral config delete-context <colony-id>

# Example output:
⚠️  This will permanently delete colony "myapp-dev-xyz789" including:
   - Colony directory: ~/.coral/colonies/myapp-dev-xyz789/
   - Config, CA certificates, and all colony data

To confirm, type the colony name: myapp-dev-xyz789
✓ Deleted colony: myapp-dev-xyz789

# Wrong confirmation aborts:
To confirm, type the colony name: wrong-name
✗ Confirmation failed. Colony not deleted.
```

### Configuration Changes

- New env var: `CORAL_CONFIG` (optional, overrides `~/.coral` base directory)
- Modified behavior: `colony list` now shows `*` marker for current colony
- Modified behavior: `colony current --verbose` shows resolution source

## Testing Strategy

### Unit Tests

- `config.Loader`: Test `CORAL_CONFIG` env var override, rename, delete,
  validate methods
- `config.Resolver`: Test `ResolveWithSource()` returns correct priority source
- Config commands: Test output formatting, error handling, flag parsing

### Integration Tests

- Test resolution priority: env var > project > global
- Test rename updates all references (global default, project config)
- Test delete with confirmation flags
- Test validation catches known config errors (invalid subnet, missing fields)

### E2E Tests

- Multi-colony workflow: init two colonies, switch between them, verify `*`
  marker
- Project override: create project config, verify resolution source changes
- Config migration: rename colony, verify commands still work with new ID
- Validation: create invalid config, verify `coral config validate` catches
  error

## Future Enhancements

**Shell Integration** (Future - separate RFD)

- Shell completion: tab-complete colony IDs in `use-context` and `--colony`
  flags
- Auto-switching: detect `.coral/config.yaml` changes and prompt to switch

**Config Templates** (Future - separate RFD)

- `coral config create-context --from-template <name>`: scaffold new colony from
  template
- Built-in templates: `dev`, `staging`, `production` with sensible defaults
- Custom template registry in global config

**Config Diff** (Low Priority)

- `coral config diff <colony-1> <colony-2>`: show config differences between
  colonies
- Useful for debugging environment-specific issues

---

## Implementation Status

**Core Capability:** ⏳ Not Started

This RFD proposes net-new functionality to improve config management UX. Current
state:

**Existing Components:**

- ✅ `coral colony list`: Lists all colonies with details
- ✅ `coral colony use <id>`: Sets default colony
- ✅ `coral colony current`: Shows current colony
- ✅ `coral colony export/import`: Credential portability (env, yaml, json, k8s
  formats)
- ✅ Config resolution: env var > project > global priority

**Gaps vs kubectl UX:**

- ❌ No unified `coral config` command family
- ❌ No visual indicator for current colony in `list` output
- ❌ No resolution source visibility
- ❌ No config validation command
- ❌ No delete CLI command (loader method exists)
- ❌ No `CORAL_CONFIG` env var support

**What Works Now:**

- Users can list colonies, switch between them, and check the current colony
- Config resolution follows documented priority order
- JSON output available for `colony list` and `colony current`

**What This RFD Adds:**

- kubectl-familiar `config` command family
- Visual `*` marker for current colony
- Resolution source transparency (`global`, `project`, `env`)
- Config validation, rename, delete operations
- Better debugging and multi-colony workflows

## Deferred Features

**Shell Integration** (Future - RFD TBD)

- Automatic prompt integration with current colony display
- Auto-switching on directory change (when `.coral/config.yaml` present)
- Requires shell-specific hooks and persistent state management

**Config Templates** (Low Priority)

- Scaffolding new colonies from templates
- Template registry and versioning
- Not critical for initial UX improvements

**Config Diff** (Low Priority)

- Comparing configs between colonies
- Nice-to-have for debugging but not essential
