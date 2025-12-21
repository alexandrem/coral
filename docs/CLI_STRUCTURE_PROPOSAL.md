# Coral CLI Structure Proposal - Consistent Command Layout

## Problem Statement

The current CLI and recent RFDs have inconsistent command structure:

**Existing:**
- `coral debug cpu-profile` (RFD 070, 072) - active profiling
- `coral query summary/traces/metrics/logs` (RFD 067) - historical queries

**New RFDs propose:**
- `coral profile memory` (RFD 076) - conflicts with `coral debug cpu-profile`
- `coral query memory-profile` (RFD 076) - inconsistent with `coral profile memory`
- `coral query profile --trace-id` (RFD 077) - confusing, `profile` is both command and subject
- `coral profile trace-trigger` (RFD 077) - OK pattern
- `coral query logs --severity error` (RFD 078) vs CLI_REFERENCE shows `--level error`

## Proposed Consistent Structure

### Principle: Separate by Intent

**Active Collection (creates new data):**
```bash
coral profile <type> [flags]
```

**Historical Queries (reads existing data):**
```bash
coral query <resource> [flags]
```

**Live Debugging (attach probes, trace requests):**
```bash
coral debug <action> [flags]
```

---

## Complete Command Structure

### 1. Profile Commands (Active Collection)

**Purpose:** Create new profiling data on-demand

```bash
# CPU profiling (on-demand, high-frequency)
coral profile cpu --service <name> [--duration <sec>] [--frequency <hz>]

# Memory profiling (on-demand, heap snapshots)
coral profile memory --service <name> [--duration <sec>] [--sample-rate <bytes>]

# Trace-triggered profiling (collect profiles for matching requests)
coral profile trace-trigger --service <name> --min-duration <ms> [--count <n>]

# Get trigger status
coral profile trigger-status <trigger-id>
```

**Flags (common across profile commands):**
- `--service <name>` - Service to profile (required)
- `--duration <sec>` - Profiling duration (default: 30s, max: 300s)
- `--frequency <hz>` - Sampling frequency for CPU (default: 99Hz)
- `--sample-rate <bytes>` - Sampling rate for memory (default: 512KB)
- `--format <type>` - Output format: flamegraph, folded, json (default: flamegraph)
- `--output <file>` - Write output to file instead of stdout

**Examples:**
```bash
# CPU profiling
coral profile cpu --service payment-svc --duration 30
coral profile cpu --service payment-svc --duration 30 --format folded > cpu.folded

# Memory profiling
coral profile memory --service payment-svc --duration 30
coral profile memory --service payment-svc --sample-rate 4MB

# Trace-triggered profiling (profile next 10 slow requests)
coral profile trace-trigger --service payment-svc --min-duration 1000 --count 10
coral profile trigger-status trigger-abc123
```

---

### 2. Query Commands (Historical Data)

**Purpose:** Query existing observability data

```bash
# Service health summary (RFD 067)
coral query summary [service] [--since <duration>]

# Distributed traces (RFD 036, 067)
coral query traces [service] [--trace-id <id>] [--since <duration>] [--min-duration <ms>]

# Service metrics (RFD 032, 067)
coral query metrics [service] [--since <duration>] [--protocol http|grpc|sql]

# Application logs (RFD 078)
coral query logs [service] [--since <duration>] [--level error|warn|info]

# CPU profiles (historical, RFD 072)
coral query cpu-profile [service] [--since <duration>] [--build-id <id>]

# Memory profiles (historical, RFD 076)
coral query memory-profile [service] [--since <duration>] [--build-id <id>]

# Trace-correlated profiles (RFD 077)
coral query trace-profile <trace-id> [--profile-type cpu|memory]

# Compare profiles between cohorts (RFD 077)
coral query profile-compare --service <name> --cohort-a <filter> --cohort-b <filter>
```

**Common flags:**
- `--since <duration>` - Time range: 5m, 1h, 24h (default: 1h)
- `--until <time>` - End time (default: now)
- `--format <type>` - Output format: table, json, csv (default: table)
- `--limit <n>` - Max results to return

**Query-specific flags:**

**Traces:**
- `--trace-id <id>` - Filter by specific trace ID
- `--min-duration <ms>` - Only traces slower than threshold
- `--status <code>` - Filter by HTTP status (200, 5xx, etc.)
- `--source ebpf|otlp|all` - Data source (default: all)

**Logs:**
- `--level error|warn|info|debug` - Log level (default: all)
- `--trace-id <id>` - Logs for specific trace
- `--search <text>` - Pattern match in message (not full-text search)
- `--group-by-pattern` - Group identical errors into patterns

**Profiles:**
- `--build-id <id>` - Filter by specific build/deployment
- `--profile-type cpu|memory` - Type of profile (for trace-profile)

**Examples:**
```bash
# Query recent errors
coral query logs --service payment-svc --level error --since 1h

# Query specific trace with correlated logs
coral query traces --trace-id abc123def456
coral query logs --trace-id abc123def456

# Query trace-correlated CPU profile
coral query trace-profile abc123def456 --profile-type cpu

# Query historical CPU profiles for service
coral query cpu-profile --service payment-svc --since 24h

# Compare slow vs fast requests
coral query profile-compare \
  --service payment-svc \
  --cohort-a "duration > 1000ms" \
  --cohort-b "duration < 100ms"
```

---

### 3. Debug Commands (Live Debugging)

**Purpose:** Attach probes, trace requests, inspect running processes

```bash
# Function-level debugging (RFD 059-062)
coral debug attach --service <name> --function <fn> [--duration <time>]
coral debug detach <session-id>

# Request path tracing (RFD 059)
coral debug trace --service <name> --path <path> [--duration <time>]

# Function discovery (RFD 063, 069)
coral debug search --service <name> <pattern>
coral debug info --service <name> --function <fn>

# Session management
coral debug sessions [service]
coral debug session-get <session-id>
coral debug session-stop <session-id>
```

**Examples:**
```bash
# Attach uprobe to function
coral debug attach --service payment-svc --function validateSignature --duration 5m

# Search for functions
coral debug search --service payment-svc checkout
coral debug info --service payment-svc --function processCheckout

# List active debug sessions
coral debug sessions payment-svc
coral debug session-stop session-abc123
```

---

### 4. Unified Query (Simple Aliases)

**Purpose:** Simplified commands for common patterns

```bash
# Shortcuts for common queries (map to coral query commands)
coral summary [service]              # = coral query summary
coral traces [service]                # = coral query traces
coral metrics [service]               # = coral query metrics
coral logs [service] --level error    # = coral query logs
```

---

## Migration from Current CLI

### Changes Required

**1. Rename `coral debug cpu-profile` → `coral profile cpu`**

**Before (RFD 070, 072):**
```bash
coral debug cpu-profile --service api --duration 30
coral debug cpu-profile --service api --since 1h  # Historical query
```

**After (consistent):**
```bash
# Active profiling (create new data)
coral profile cpu --service api --duration 30

# Historical query (read existing data)
coral query cpu-profile --service api --since 1h
```

**2. Standardize log level flag to `--level`**

**Before (RFD 078):**
```bash
coral query logs --severity error  # WRONG
```

**After (consistent with CLI_REFERENCE.md):**
```bash
coral query logs --level error     # CORRECT
```

**3. Consolidate profile queries under `coral query`**

**Before (RFD 076, 077):**
```bash
coral query memory-profile          # OK
coral query profile --trace-id ...  # CONFUSING
coral query trace-profile ...       # OK
coral query profile-compare ...     # OK
```

**After (consistent):**
```bash
coral query memory-profile --service api --since 1h
coral query trace-profile abc123def456
coral query profile-compare --service api --cohort-a ... --cohort-b ...
```

---

## Complete Reference

### Profile (Active Collection)

| Command | Purpose | RFD |
|---------|---------|-----|
| `coral profile cpu` | On-demand CPU profiling | 070, 072 |
| `coral profile memory` | On-demand memory profiling | 076 |
| `coral profile trace-trigger` | Triggered profiling for matching requests | 077 |
| `coral profile trigger-status <id>` | Get trigger status | 077 |

### Query (Historical Data)

| Command | Purpose | RFD |
|---------|---------|-----|
| `coral query summary` | Service health overview | 067, 074 |
| `coral query traces` | Distributed traces | 036, 067 |
| `coral query metrics` | RED metrics (HTTP/gRPC/SQL) | 032, 067 |
| `coral query logs` | Application error logs | 078 |
| `coral query cpu-profile` | Historical CPU profiles | 072 |
| `coral query memory-profile` | Historical memory profiles | 076 |
| `coral query trace-profile <trace-id>` | Trace-correlated profiles | 077 |
| `coral query profile-compare` | Comparative profile analysis | 077 |

### Debug (Live Debugging)

| Command | Purpose | RFD |
|---------|---------|-----|
| `coral debug attach` | Attach uprobe to function | 059-062 |
| `coral debug detach` | Detach uprobe session | 059-062 |
| `coral debug trace` | Trace request path | 059 |
| `coral debug search` | Search for functions | 063, 069 |
| `coral debug info` | Get function info | 063, 069 |
| `coral debug sessions` | List active sessions | 059 |
| `coral debug session-get` | Get session details | 059 |
| `coral debug session-stop` | Stop session | 059 |

---

## Implementation Checklist

**Phase 1: Update RFDs (Documentation)**
- [ ] RFD 070/072: Change `coral debug cpu-profile` → `coral profile cpu`
- [ ] RFD 070/072: Add `coral query cpu-profile` for historical queries
- [ ] RFD 076: Confirm `coral profile memory` (already correct)
- [ ] RFD 076: Confirm `coral query memory-profile` (already correct)
- [ ] RFD 077: Remove `coral query profile --trace-id`, use `coral query trace-profile`
- [ ] RFD 078: Change `--severity` → `--level` for consistency with CLI_REFERENCE.md
- [ ] All RFDs: Add cross-references to this CLI structure document

**Phase 2: Update CLI_REFERENCE.md**
- [ ] Add new `## Profiling Commands` section with `coral profile cpu|memory|trace-trigger`
- [ ] Update `## Unified Query Commands` section with profile queries
- [ ] Add `## Debug Commands` consolidation
- [ ] Add migration notes for `coral debug cpu-profile` users

**Phase 3: Update Implementation (Code)**
- [ ] Add `coral profile` command group (currently missing)
- [ ] Move `coral debug cpu-profile` → `coral profile cpu` (keep old command as deprecated alias)
- [ ] Add `coral query cpu-profile` for historical queries
- [ ] Implement `coral query memory-profile`, `coral query trace-profile`, etc.
- [ ] Add deprecation warnings for old commands

---

## Benefits of This Structure

1. **Clear Intent Separation:**
   - `profile` = I want to collect new profiling data now
   - `query` = I want to view existing data
   - `debug` = I want to attach probes or inspect live processes

2. **Consistent Patterns:**
   - All active profiling: `coral profile <type>`
   - All historical queries: `coral query <resource>`
   - All live debugging: `coral debug <action>`

3. **Discoverable:**
   - `coral profile --help` shows all profiling types
   - `coral query --help` shows all queryable resources
   - `coral debug --help` shows all debug actions

4. **Future-Proof:**
   - New profile types: `coral profile goroutines`, `coral profile heap-dump`
   - New queries: `coral query dependencies`, `coral query anomalies`
   - New debug actions: `coral debug snapshot`, `coral debug inspect`

5. **Aligns with Industry Standards:**
   - kubectl: `get`, `describe`, `logs`, `exec`, `debug`
   - gcloud: `compute`, `sql`, `storage`
   - Coral: `profile`, `query`, `debug`
