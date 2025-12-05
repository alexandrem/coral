---
rfd: "068"
title: "Debug Command UX Revamp"
state: "implemented"
breaking_changes: true
testing_required: true
database_changes: false
api_changes: false
dependencies: [ "059" ]
database_migrations: [ ]
areas: [ "cli", "ux", "debugging" ]
---

# RFD 068 - Debug Command UX Revamp

**Status:** 🎉 Implemented

## Summary

Reorganize `coral debug` commands into a cleaner, more intuitive structure by
grouping session-related operations under a `session` subcommand. This
refactoring improves discoverability and scales better as we add new debugging
features (RFD 069). Includes stub commands for future function discovery and
profiling features.

## Problem

**Current command structure is flat and unclear:**

```bash
coral debug attach --function <name>     # Attach probe
coral debug list                         # List sessions (unclear what we're listing)
coral debug events <session-id>          # Get session events (unclear relationship)
# No command to get session metadata
# No clear grouping of session operations
```

**Issues:**

- **Ambiguous commands:** `debug list` - listing what? Sessions? Functions?
  Services?
- **No metadata command:** Can't get info ABOUT a session (status, duration, who
  started it) without fetching all events
- **Poor scalability:** As we add function discovery (`search`, `info`,
  `profile` in RFD 069), the flat namespace becomes crowded
- **Inconsistent with industry patterns:** Tools like `kubectl`, `docker`, `gh`
  use subcommands to group related operations

**Why this matters:**

- **User confusion:** New users don't understand command relationships
- **Poor discoverability:** Hard to find the right command for the task
- **Difficult to extend:** Adding new features (RFD 069) would make flat
  structure worse

## Solution

Reorganize into clear command groups with **session operations under a
subcommand**:

### Current vs New Structure

**Before:**

```bash
coral debug attach --function <name>    # Attach probe
coral debug list                        # List sessions
coral debug events <session-id>         # Get events
# (no command for session metadata)
```

**After:**

```bash
# Discovery (stubs for RFD 069)
coral debug search <query>              # [STUB] Find functions
coral debug info --function <name>      # [STUB] Get function details

# Instrumentation
coral debug attach --function <name>    # Attach probe (existing, unchanged)
coral debug profile --query <query>     # [STUB] Auto-profile multiple functions

# Session management (grouped under "session")
coral debug session list                # List sessions (renamed from "debug list")
coral debug session get <id>            # Get session metadata (NEW)
coral debug session events <id>         # Get session data (renamed from "debug events")
coral debug session stop <id>           # Stop session (existing or new)
```

### Key Design Decisions

- **Group session operations:** All session commands under `coral debug session`
  subcommand
- **Clear separation:** Discovery vs Instrumentation vs Session management
- **Preserve existing behavior:** `attach` stays the same, only organizational
  changes
- **Add stubs for future:** `search`, `info`, `profile` commands return "not
  implemented" until RFD 069
- **Session metadata command:** New `session get` provides status without
  downloading events

### Benefits

- **Clear mental model:** Commands grouped by purpose (discovery,
  instrumentation, sessions)
- **Better discoverability:** `coral debug session --help` shows all session
  operations
- **Scales cleanly:** Room for future commands (RFD 069) without namespace
  pollution
- **Consistent with industry:** Follows kubectl/docker/gh patterns
- **Separates concerns:** Session metadata vs session data (get vs events)

## Command Specification

### Discovery Commands (Stubs for RFD 069)

```bash
# Search for functions (returns list)
coral debug search [OPTIONS] <query>
  # Current: Returns "Not implemented. Will be available in a future release."
  # Future (RFD 069): Searches function registry

# Get function details (metrics)
coral debug info [OPTIONS]
  -f, --function <name>      Function name (required)
  # Current: Returns "Not implemented. Will be available in a future release."
  # Future (RFD 069): Returns function metadata from registry
```

### Instrumentation Commands

```bash
# Attach uprobe to single function (EXISTING - no changes)
coral debug attach [OPTIONS]
  -s, --service <name>       Service name (required)
  -f, --function <name>      Function name (required)
  -d, --duration <seconds>   Duration (default: 60, max: 300)
  --sample-rate <float>      Event sampling rate (default: 1.0)
  --async                    Return immediately, don't wait
  # Implementation: Existing command, behavior unchanged

# Auto-profile multiple functions (STUB for RFD 069)
coral debug profile [OPTIONS]
  # Current: Returns "Not implemented. Will be available in a future release."
  # Future (RFD 069): Batch profiling with automatic analysis
```

### Session Management Commands

All session operations are scoped under `coral debug session`:

```bash
# List all debug sessions
coral debug session list [OPTIONS]
  -s, --service <name>       Filter by service (optional)
  --active-only              Show only active sessions
  --format <table|json>      Output format
  # Migration: Renamed from "coral debug list"

# Get session metadata
coral debug session get <session-id> [OPTIONS]
  --format <table|json>      Output format
  # NEW command
  # Returns: session_id, status (active/completed/stopped), start_time,
  #          end_time, duration, service, function, user, event_count

# Get captured events/data from session
coral debug session events <session-id> [OPTIONS]
  --format <table|json>      Output format
  --limit <num>              Max events to return (default: 100)
  --follow                   Stream events in real-time (for active sessions)
  # Migration: Renamed from "coral debug events <session-id>"

# Stop session early
coral debug session stop <session-id>
  # Migration: Renamed from "coral debug detach <session-id>" OR new command
```

## Migration Guide

### Breaking Changes

**Renamed commands:**

```bash
# Old → New
coral debug list               → coral debug session list
coral debug events <id>        → coral debug session events <id>
coral debug detach <id>        → coral debug session stop <id>  # (if detach exists)
```

**Unchanged commands:**

```bash
coral debug attach             # No changes
```

**New commands:**

```bash
coral debug session get <id>   # NEW: Get session metadata
coral debug search <query>     # STUB: For RFD 069
coral debug info --function    # STUB: For RFD 069
coral debug profile            # STUB: For RFD 069
```

### Backward Compatibility

Since Coral is experimental, **no backward compatibility** is provided:

- Old commands will be removed immediately
- Users must update to new command structure
- CLI will show helpful error messages pointing to new commands

**Example error message:**

```bash
$ coral debug list
Error: Command "coral debug list" has been removed.
Use "coral debug session list" instead.

Run "coral debug session --help" for more information.
```

## Implementation Plan

### Phase 1: Refactor Session Commands

- [x] Rename `coral debug list` → `coral debug session list`
- [x] Rename `coral debug events` → `coral debug session events`
- [x] Rename `coral debug detach` → `coral debug session stop` (if detach
  exists)
- [x] Implement `coral debug session get` (new metadata command)
- [x] Update help text and documentation

### Phase 2: Add Stub Commands

- [x] Implement `coral debug search` stub (returns "not implemented")
- [x] Implement `coral debug info` stub (returns "not implemented")
- [x] Implement `coral debug profile` stub (returns "not implemented")
- [x] Add help text explaining these will be available in future release

### Phase 3: Testing

- [x] Update all CLI tests to use new command structure
- [x] Test migration error messages
- [x] Verify `attach` command unchanged
- [x] Test session subcommand grouping works correctly

### Phase 4: Documentation

- [x] Update CLI reference docs
- [x] Update user guides with new command structure

## Example Workflows

### Before (Current)

```bash
# Attach probe
coral debug attach -s api -f processPayment -d 60s

# List sessions
coral debug list

# Get events
coral debug events abc123
```

### After (New Structure)

```bash
# Attach probe (unchanged)
coral debug attach -s api -f processPayment -d 60s

# Check session status (NEW)
coral debug session get abc123
# → Session abc123: ACTIVE, 45s/60s elapsed, 187 events captured

# List sessions (renamed)
coral debug session list

# Get events (renamed)
coral debug session events abc123

# Stop session (renamed)
coral debug session stop abc123
```

### With Future Features (RFD 069)

```bash
# Search for functions (will work after RFD 069)
coral debug search -s api checkout

# Get function info (will work after RFD 069)
coral debug info -s api -f handleCheckout

# Auto-profile (will work after RFD 069)
coral debug profile -s api -q checkout --strategy critical-path -d 60s
```

## Testing Strategy

### Unit Tests

- Command routing to correct handlers
- Session subcommand grouping
- Stub commands return appropriate "not implemented" messages
- Migration error messages show correct new commands

### Integration Tests

- End-to-end workflow with new command structure
- Session list/get/events/stop operations work correctly
- Attach command behavior unchanged
- Old commands show helpful error messages

### Manual Testing

- Test user experience with new grouping
- Verify help text is clear
- Check that tab completion works with new structure

## Future Work (RFD 069)

Once this UX refactoring is complete, RFD 069 will implement the stub commands:

- `coral debug search` - Function discovery with semantic search
- `coral debug info` - Function details from registry (e.g. metrics)
- `coral debug profile` - Automatic batch profiling with bottleneck analysis

These will integrate cleanly into the new command structure.

## Dependencies

- **RFD 059**: Live Debugging Architecture (provides current debug commands
  we're refactoring)

## References

- RFD 059: Live Debugging Architecture (current debug implementation)
- RFD 069: Function Discovery and Profiling Tools (future features using this
  structure)
