---
rfd: "072"
title: "Continuous CPU Profiling"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "070", "071" ]
database_migrations: [ "cpu_profile_samples_local", "cpu_profile_summaries", "binary_metadata_local", "binary_metadata_registry", "profile_frame_dictionary_local", "profile_frame_dictionary" ]
areas: [ "agent", "ebpf", "profiling", "colony", "storage" ]
---

# RFD 072 - Continuous CPU Profiling

**Status:** 🚧 Draft

## Summary

Extend RFD 070's on-demand CPU profiling to support continuous low-overhead
background profiling. This enables historical flame graph generation, CPU usage
trend analysis, and automatic detection of performance regressions without
manual profiling intervention.

## Problem

- **Current limitations**: RFD 070 provides one-shot CPU profiling that requires
  manual invocation. Users must proactively profile services when they suspect
  performance issues, but by then the problem may have passed. There's no
  historical data to analyze past CPU spikes or compare current behavior against
  baselines.
- **Why this matters**: Production performance issues are often transient and
  difficult to reproduce. Without continuous profiling, teams miss critical data
  during incidents and cannot perform retroactive analysis. Developers cannot
  identify gradual performance regressions that accumulate over time.
- **Use cases affected**: Performance regression detection across deployments,
  root cause analysis of past incidents, capacity planning based on historical
  CPU patterns, automated anomaly detection for CPU-intensive code paths.

## Solution

Implement continuous CPU profiling using RFD 070's eBPF infrastructure with
reduced sampling frequency (1Hz instead of 99Hz) and intelligent aggregation
for storage efficiency.

**Key Design Decisions:**

- **Low-Overhead Sampling**: Use 19Hz sampling frequency (prime number to avoid
  timer conflicts, following Parca's proven approach) to minimize production
  impact while maintaining useful signal. This provides ~68,400 samples per hour
  per service with <1% CPU overhead.
- **Tiered Storage**: Follow RFD 071's pattern with 15-second aggregation
  intervals stored in agent DuckDB (1-hour retention) and 1-minute summaries in
  colony DuckDB (30-day retention).
- **Stack Aggregation**: Store collapsed stack traces with sample counts rather
  than individual samples. This dramatically reduces storage while preserving
  flame graph generation capability.
- **On-Demand Detail**: Preserve ability to temporarily switch to high-frequency
  (99Hz) profiling via RFD 070's existing API when detailed diagnosis is needed.
- **Incremental Collection**: Use existing RFD 070 BPF program with modified
  perf event frequency, reusing symbolization and flame graph infrastructure.

**Benefits:**

- **Retroactive Analysis**: Investigate CPU patterns from hours or days ago
  during post-incident reviews.
- **Regression Detection**: Compare flame graphs before/after deployments to
  identify performance degradation.
- **Baseline Learning**: Establish normal CPU usage patterns for anomaly
  detection.
- **Cost Efficiency**: 19Hz sampling provides <1% CPU overhead while maintaining
  high-quality profiling data.
- **Zero Configuration**: Enabled by default, no manual profiling required.

**Architecture Overview:**

```
┌─────────────────────────────────────────────────────────────────┐
│  Agent: Continuous CPU Profiler                                 │
│                                                                  │
│  [15-Second Collection Loop]                                    │
│         ↓ (19Hz sampling via perf_event)                        │
│     eBPF Stack Traces                                           │
│         ↓                                                        │
│  [Symbolization & Aggregation]                                  │
│         ↓                                                        │
│  Collapsed Stacks with Counts                                   │
│         ↓                                                        │
│  [Local DuckDB Storage]                                         │
│  Table: cpu_profile_samples_local                               │
│  Retention: 1 hour (~240 samples)                               │
│  Cleanup: Every 10 minutes                                      │
└─────────────────────────────────────────────────────────────────┘
                              │
                              │ RPC Query (every minute)
                              ↓
┌─────────────────────────────────────────────────────────────────┐
│  Colony: Profile Aggregator                                     │
│                                                                  │
│  [Profile Poller - 1 minute interval]                           │
│         ↓                                                        │
│  Query 4 × 15s samples from agent                               │
│         ↓                                                        │
│  [Merge Stack Counts]                                           │
│  Combine identical stacks                                       │
│  Calculate percentages                                          │
│         ↓                                                        │
│  [Colony DuckDB Storage]                                        │
│  Table: cpu_profile_summaries                                   │
│  Retention: 30 days                                             │
│  Cleanup: Daily                                                 │
└─────────────────────────────────────────────────────────────────┘
                              │
                              │ CLI Query
                              ↓
┌─────────────────────────────────────────────────────────────────┐
│  CLI: Historical Flame Graph Generation                         │
│                                                                  │
│  coral debug cpu-profile --service api --since 1h               │
│         ↓                                                        │
│  Aggregate stacks from time range                               │
│         ↓                                                        │
│  Output folded stack format                                     │
│         ↓                                                        │
│  flamegraph.pl > historical_cpu.svg                             │
└─────────────────────────────────────────────────────────────────┘
```

### Component Changes

1. **Agent (eBPF)**:
    - Reuse `cpu_profile.bpf.c` from RFD 070 with modified perf event
      frequency.
    - No BPF program changes required - frequency controlled from userspace.

2. **Agent (Go)**:
    - New `ContinuousCPUProfiler` in
      `internal/agent/profiler/continuous_cpu.go`.
    - Background goroutine collecting 15-second profile samples.
    - Extract build ID from binary on each collection (detect deployments).
    - Symbolize and aggregate stacks into collapsed format.
    - Store in local DuckDB table: `cpu_profile_samples_local` with build_id.
    - Track binary metadata in `binary_metadata_local`.
    - Symbol cache keyed by build_id (automatic version handling).
    - Automatic cleanup: 1-hour retention (profiles), 7-day retention (metadata).

3. **Agent (Storage)**:
    - New table: `cpu_profile_samples_local`.
    - Columns: `timestamp`, `service_id`, `build_id` (TEXT), `stack_trace` (
      TEXT), `sample_count` (INTEGER).
    - Index on timestamp, service_id, and build_id.
    - New table: `binary_metadata_local` for build ID registry.

4. **Colony (Poller)**:
    - New `CPUProfilePoller` in `internal/colony/cpu_profile_poller.go`.
    - Every minute, query agents for new profile samples.
    - Aggregate 4 × 15s samples into 1-minute summaries.
    - Merge identical stack traces per build_id, sum sample counts.
    - Track build IDs in `binary_metadata_registry`.
    - Store in colony DuckDB table: `cpu_profile_summaries`.

5. **Colony (Storage)**:
    - New table: `cpu_profile_summaries`.
    - Columns: `bucket_time`, `service_id`, `pod_name`, `build_id` (TEXT),
      `stack_trace` (TEXT), `sample_count` (INTEGER), `sample_percentage` (
      DOUBLE).
    - New table: `binary_metadata_registry` for build ID tracking.
    - 30-day retention (profiles), 90-day retention (metadata).

6. **CLI**:
    - Extend `coral debug cpu-profile` to support historical queries.
    - New flags: `--since`, `--until`, `--compare-with`.
    - Generate flame graphs from historical data.
    - Annotate multi-version profiles with build_id when spanning deployments.
    - Handle missing binaries gracefully (display raw addresses with build_id).

## Implementation Plan

### Phase 1: Agent-Side Collection

- [ ] Create `internal/agent/profiler/continuous_cpu.go`.
- [ ] Implement 15-second collection loop using RFD 070's `ProfileCPU`
  infrastructure.
- [ ] Configure perf events for 19Hz sampling (prime number).
- [ ] Implement build ID extraction from ELF binaries (`/proc/<pid>/exe`).
- [ ] Add local DuckDB table schema: `cpu_profile_samples_local` with build_id
  column.
- [ ] Add local DuckDB table schema: `binary_metadata_local` for build ID
  tracking.
- [ ] Implement cleanup goroutine for 1-hour retention (profiles) and 7-day
  retention (metadata).

### Phase 2: Agent Integration

- [ ] Add configuration section: `continuous_profiling.cpu`.
- [ ] Implement symbol cache keyed by build_id (not just binary path).
- [ ] Update symbolization to use build_id for cache lookups.
- [ ] Start profiler goroutine in agent initialization (
  `internal/cli/agent/start.go`).
- [ ] Add RPC handler: `QueryCPUProfileSamples` to return historical samples
  with build_id.
- [ ] Add unit tests for collection, aggregation, build ID tracking, and
  cleanup.

### Phase 3: Colony-Side Aggregation

- [ ] Create `cpu_profile_summaries` table in colony DuckDB schema with
  build_id column.
- [ ] Create `binary_metadata_registry` table for tracking build IDs across
  agents.
- [ ] Implement `CPUProfilePoller` in `internal/colony/cpu_profile_poller.go`.
- [ ] Add 1-minute aggregation logic: merge stacks by build_id, sum counts.
- [ ] Add database methods in `internal/colony/database/cpu_profiles.go`.
- [ ] Implement 30-day retention cleanup (profiles) and 90-day retention (
  metadata).
- [ ] Add unit tests for aggregation and merge logic.

### Phase 4: CLI Historical Queries

- [ ] Add `--since` and `--until` flags to `coral debug cpu-profile`.
- [ ] Query colony database for time-range profiles.
- [ ] Aggregate stacks across time range, preserving build_id associations.
- [ ] Output folded stack format with build_id annotations (when multiple
  versions).
- [ ] Add `--compare-with` flag for differential flame graphs.
- [ ] Handle multi-version profiles (annotate stacks with build_id when needed).

### Phase 5: Testing & Validation

- [ ] Add integration tests for continuous collection.
- [ ] Validate storage overhead is acceptable (<10MB per service per day).
- [ ] Verify 1Hz sampling overhead is <0.1% CPU.
- [ ] Test historical flame graph generation matches on-demand profiles.
- [ ] Test retention cleanup works correctly.

## API Changes

### Agent Configuration

```yaml
# agent.yaml - Continuous profiling configuration
continuous_profiling:
    enabled: true                # Master switch (default: true)
    cpu:
        enabled: true              # CPU profiling (default: true)
        frequency_hz: 19           # Sampling frequency (default: 19Hz, prime number)
        interval: 15s              # Collection interval (default: 15s)
        retention: 1h              # Local retention (default: 1h)
```

### New RPC Endpoint

```protobuf
service DebugService {
    // Existing RPCs from RFD 069/070...

    // Query historical CPU profile samples from agent's local storage
    rpc QueryCPUProfileSamples(QueryCPUProfileSamplesRequest)
        returns (QueryCPUProfileSamplesResponse);
}

message QueryCPUProfileSamplesRequest {
    string service_name = 1;
    string pod_name = 2;           // Optional
    google.protobuf.Timestamp start_time = 3;
    google.protobuf.Timestamp end_time = 4;
}

message CPUProfileSample {
    google.protobuf.Timestamp timestamp = 1;
    string stack_trace = 2;         // Collapsed stack format
    uint32 sample_count = 3;
}

message QueryCPUProfileSamplesResponse {
    repeated CPUProfileSample samples = 1;
    uint64 total_samples = 2;
    string error = 3;
}
```

### Colony Database Schema

```sql
-- Agent-side: Frame dictionary for compression (shared across all profiles)
CREATE TABLE IF NOT EXISTS profile_frame_dictionary_local
(
    frame_id    INTEGER PRIMARY KEY,
    frame_name  TEXT UNIQUE NOT NULL,
    frame_count BIGINT NOT NULL DEFAULT 0  -- Reference count for cleanup
);
CREATE INDEX IF NOT EXISTS idx_profile_frame_dictionary_name
    ON profile_frame_dictionary_local (frame_name);

-- Agent-side: Raw 15-second profile samples with integer-encoded stacks
CREATE TABLE IF NOT EXISTS cpu_profile_samples_local
(
    timestamp        TIMESTAMP  NOT NULL,
    service_id       TEXT       NOT NULL,
    build_id         TEXT       NOT NULL, -- Binary build ID (ELF build-id or hash)
    stack_frame_ids  INTEGER[]  NOT NULL, -- Stack as frame IDs: [1, 2, 3]
    sample_count     INTEGER    NOT NULL,
    PRIMARY KEY (timestamp, service_id, build_id, stack_frame_ids)
);
CREATE INDEX IF NOT EXISTS idx_cpu_profile_samples_timestamp
    ON cpu_profile_samples_local (timestamp);
CREATE INDEX IF NOT EXISTS idx_cpu_profile_samples_service
    ON cpu_profile_samples_local (service_id);
CREATE INDEX IF NOT EXISTS idx_cpu_profile_samples_build_id
    ON cpu_profile_samples_local (build_id);
-- Note: DuckDB uses columnar storage with automatic compression
-- No explicit array index needed - scans are efficient on compressed columns

-- Agent-side: Binary metadata for symbol resolution
CREATE TABLE IF NOT EXISTS binary_metadata_local
(
    build_id       TEXT PRIMARY KEY,
    service_id     TEXT      NOT NULL,
    binary_path    TEXT      NOT NULL,
    first_seen     TIMESTAMP NOT NULL,
    last_seen      TIMESTAMP NOT NULL,
    has_debug_info BOOLEAN   NOT NULL DEFAULT false
);
CREATE INDEX IF NOT EXISTS idx_binary_metadata_service
    ON binary_metadata_local (service_id);

-- Colony-side: Frame dictionary for compression (shared across all services)
CREATE TABLE IF NOT EXISTS profile_frame_dictionary
(
    frame_id    INTEGER PRIMARY KEY,
    frame_name  TEXT UNIQUE NOT NULL,
    frame_count BIGINT NOT NULL DEFAULT 0  -- Reference count for cleanup
);
CREATE INDEX IF NOT EXISTS idx_profile_frame_dictionary_name
    ON profile_frame_dictionary (frame_name);

-- Colony-side: Aggregated 1-minute profile summaries with integer-encoded stacks
CREATE TABLE IF NOT EXISTS cpu_profile_summaries
(
    bucket_time        TIMESTAMP  NOT NULL,
    service_id         TEXT       NOT NULL,
    pod_name           TEXT       NOT NULL,
    build_id           TEXT       NOT NULL,
    stack_frame_ids    INTEGER[]  NOT NULL, -- Stack as frame IDs for compression
    sample_count       INTEGER    NOT NULL,
    sample_percentage  DOUBLE     NOT NULL,
    PRIMARY KEY (bucket_time, service_id, pod_name, build_id, stack_frame_ids)
);
CREATE INDEX IF NOT EXISTS idx_cpu_profile_summaries_bucket
    ON cpu_profile_summaries (bucket_time);
CREATE INDEX IF NOT EXISTS idx_cpu_profile_summaries_service
    ON cpu_profile_summaries (service_id);
CREATE INDEX IF NOT EXISTS idx_cpu_profile_summaries_build_id
    ON cpu_profile_summaries (build_id);
-- Note: DuckDB's columnar storage handles array scans efficiently
-- Dictionary encoding provides automatic compression without explicit indexes

-- Colony-side: Binary metadata registry
CREATE TABLE IF NOT EXISTS binary_metadata_registry
(
    build_id       TEXT PRIMARY KEY,
    service_id     TEXT      NOT NULL,
    binary_path    TEXT      NOT NULL,  -- Path where binary was found
    first_seen     TIMESTAMP NOT NULL,
    last_seen      TIMESTAMP NOT NULL,
    has_debug_info BOOLEAN   NOT NULL DEFAULT false,
    symbol_storage TEXT                 -- Optional: path to stored debug symbols
);
CREATE INDEX IF NOT EXISTS idx_binary_metadata_registry_service
    ON binary_metadata_registry (service_id);
```

### CLI Commands

```bash
# Continuous profiling enabled by default - no manual intervention needed

# Query historical CPU profile (last hour)
coral debug cpu-profile --service api --since 1h > profile_last_hour.folded
flamegraph.pl profile_last_hour.folded > cpu_last_hour.svg

# Query specific time range
coral debug cpu-profile --service api \
    --since "2025-12-15 14:00:00" \
    --until "2025-12-15 15:00:00" \
    > incident_profile.folded

# Compare two time periods (differential flame graph)
coral debug cpu-profile --service api \
    --since 1h \
    --compare-with "2025-12-14 14:00:00 to 2025-12-14 15:00:00" \
    > regression.folded

# On-demand high-frequency profiling (RFD 070, still available)
coral debug cpu-profile --service api --duration 30s --frequency 99 \
    > detailed_profile.folded

# Expected output (folded format):
# main;processRequest;parseJSON;unmarshal 847
# main;processRequest;validateData 623
# main;processRequest;saveToDatabase;executeQuery 1523
# kernel`entry_SYSCALL_64;do_syscall_64;sys_read;vfs_read 234
# ...
```

### Configuration Details

**Sampling Frequency Rationale:**

- **19Hz (default)**: Captures 19 samples per second, ~68,400 samples/hour
    - Overhead: <1% CPU, negligible memory
    - Prime number avoids timer conflicts with system interrupts
    - Proven approach used by Parca and other production profilers
    - Sufficient for identifying hot code paths and trends
    - Suitable for continuous production profiling

- **On-demand 99Hz** (RFD 070): Captures 99 samples per second
    - Overhead: ~1-2% CPU during profiling period
    - Higher precision for debugging specific issues
    - Use for short-duration detailed analysis

**Why 15-Second Aggregation Intervals:**

- Balances storage efficiency with granularity
- Aligns with RFD 071 system metrics collection pattern
- Provides 4 samples per minute for colony aggregation
- Enables sub-minute precision for incident correlation

## Storage & Retention

### Storage Impact

**Agent-Side (Local DuckDB):**

- 15-second intervals: 240 samples/hour
- 19Hz sampling: ~285 samples per 15s period (19 × 15)
- Typical stack depth: 10-20 frames
- Unique stacks: ~100-200 per service (after aggregation)

**Storage with Integer Encoding:**

- Stack frame as integer: 4 bytes (vs 20-50 bytes for string)
- Average stack: 15 frames × 4 bytes = 60 bytes (vs 400 bytes for strings)
- **Compression: 85% savings per stack**
- Frame dictionary overhead: ~1,000 unique frames × 30 bytes = ~30KB (
  amortized)
- Storage per 15s sample: ~2KB (integer arrays + metadata)
- **Agent storage: ~480KB/hour per service** (240 × 2KB)
- 1-hour retention: **~480KB per service**
- **Reduction vs string arrays: 75% (1.9MB → 480KB)**

**Colony-Side (Aggregated DuckDB):**

- 1-minute bucket aggregation (4 × 15s samples merged)

**Storage with Integer Encoding + DuckDB Dictionary Compression:**

- Integer arrays: 60 bytes per stack (15 frames × 4 bytes)
- DuckDB dictionary encoding: Additional 2-3x compression on repeated integer
  sequences
- Effective storage: ~20 bytes per unique stack (after DuckDB compression)
- Storage per 1-minute summary: ~4KB (100 stacks × 20 bytes + metadata)
- 60 minutes/hour × 24 hours/day = 1,440 summaries/day
- **Colony storage: ~5.8MB/day per service** (1,440 × 4KB)
- 30-day retention: **~174MB per service**
- 10 services: **~1.74GB for 30 days**
- **Reduction vs string arrays: 72% (6.3GB → 1.74GB)**
- **Total reduction vs uncompressed: 3.6x (as predicted)**

**Storage Comparison:**

- System Metrics (RFD 071): ~90MB/month for 10 services
- Beyla HTTP: ~500MB/month (30-day retention)
- **Continuous CPU Profiling (with frame dictionary): ~1.74GB/month for 10
  services**
- Previous estimate (string arrays): ~6.3GB/month
- **Savings from frame dictionary: 72% (4.56GB saved)**

**Optimization Strategies:**

- Compress old data (DuckDB automatic compression: ~5-10x reduction)
- Implement aggressive stack pruning (drop stacks with <1% samples)
- Offer configurable retention (7d/14d/30d tiers)
- Optional: Store only top-N hot stacks per time bucket

### Retention Policy

**Agent:**

- Raw 15-second samples: **1 hour retention**
- Cleanup: Every 10 minutes
- Purpose: High-resolution debugging for recent activity

**Colony:**

- Aggregated 1-minute summaries: **30 days retention (default)**
- Cleanup: Daily at 02:00 UTC
- Configuration: `retention.cpu_profiles: 30d` (user-configurable)
- Purpose: Historical analysis, trend detection, regression comparison

**Cleanup Queries:**

```sql
-- Agent cleanup (runs every 10 minutes)
DELETE FROM cpu_profile_samples_local
WHERE timestamp < NOW() - INTERVAL 1 HOUR;

-- Agent binary metadata cleanup (runs daily, 7-day retention)
DELETE FROM binary_metadata_local
WHERE last_seen < NOW() - INTERVAL 7 DAYS;

-- Colony cleanup (runs daily)
DELETE FROM cpu_profile_summaries
WHERE bucket_time < NOW() - INTERVAL 30 DAYS;

-- Colony binary metadata cleanup (runs weekly, 90-day retention)
DELETE FROM binary_metadata_registry
WHERE last_seen < NOW() - INTERVAL 90 DAYS;
```

## Performance Considerations

### Sampling Overhead

**19Hz Continuous Profiling:**

- CPU overhead: <1% (19 perf events per second)
- Memory overhead: ~500KB per profiled process (BPF maps)
- Symbolization: Amortized across 15s interval (not per-sample)
- Network: ~8KB/15s sent to colony (~1.9MB/hour per service)

**Comparison to On-Demand (99Hz):**

| Metric              | 19Hz (Continuous) | 99Hz (On-Demand) | Overhead Reduction |
|---------------------|-------------------|------------------|--------------------|
| CPU Impact          | <1%               | 1-2%             | ~50%               |
| Samples/Second      | 19                | 99               | 81%                |
| Storage/Hour        | 1.9MB             | ~120MB           | 98%                |
| Network/Hour        | 1.9MB             | N/A (not stored) | N/A                |

### Symbolization Strategy

**Build ID Tracking:**

The agent tracks the binary build ID (ELF NT_GNU_BUILD_ID note or SHA-256 hash
of binary) for each profiled service. This enables correct symbolization of
historical profiles across deployments:

1. **Build ID Extraction**:
    - On profile collection, read build ID from `/proc/<pid>/exe`
    - Use ELF parser to extract NT_GNU_BUILD_ID note (20-byte SHA-1 hash)
    - Fallback: Compute SHA-256 hash of binary if build ID not present
    - Store build ID with each profile sample

2. **Binary Metadata Registration**:
    - When new build ID detected, record in `binary_metadata_local` table
    - Track: build_id, service_id, binary_path, has_debug_info
    - Update last_seen timestamp on each profile collection
    - Agent retains metadata for 7 days (longer than profile samples)

3. **Symbol Resolution**:
    - Cache DWARF/ELF symbols keyed by build_id (not just binary path)
    - When symbolizing: `symbols = cache.Get(build_id)` → ensures correct
      version
    - If binary no longer on disk: attempt to use cached symbols
    - If no cached symbols: store raw instruction pointers

4. **Historical Symbolization**:
    - When querying old profiles, look up build_id in metadata registry
    - If binary still available: re-symbolize using correct version
    - If binary gone but debug info stored: use stored symbols
    - If neither available: display raw addresses with build ID annotation

**Lazy Symbolization:**

- Symbolize stacks once per 15-second collection (not per sample)
- Cache symbol tables in memory keyed by build_id (reuse from RFD 070)
- Symbolize kernel stacks only if configured (reduce overhead)

**Symbol Caching by Build ID:**

- Cache structure: `map[buildID]*SymbolTable`
- Reuse DWARF/ELF parsing results across collection intervals
- Cache symbol lookups for frequently-seen instruction pointers
- Automatically handle binary updates: new build_id → new cache entry
- No manual cache invalidation needed (build ID uniquely identifies version)

**Deployment Handling:**

When a service is redeployed with a new binary:

1. Agent detects new build_id on next profile collection
2. Creates new entry in `binary_metadata_local`
3. Symbolizes new stacks using new binary's symbols
4. Old profiles remain associated with old build_id
5. Queries spanning deployment show both versions (annotated with build_id)

**Example: Multi-Version Flame Graph**

```
# Query spanning deployment (build_id changes midway)
coral debug cpu-profile --service api --since 2h

# Output includes build ID annotations:
# [build_id:abc123] main;processRequest;parseJSON 1200
# [build_id:abc123] main;processRequest;validateData 800
# [build_id:def456] main;processRequest;parseJSONv2 1500  ← New version
# [build_id:def456] main;processRequest;validateDataFast 400  ← Optimized
```

**Fallback for Missing Symbols:**

- If binary no longer available and no cached symbols:
    - Store raw instruction pointers with build_id reference
    - Display in flame graphs: `[build_id:abc123] 0x55d9a12c4580`
    - User can provide debug symbols later for offline symbolization
- Enable delayed symbolization: `coral debug symbolize --build-id abc123
  --symbols /path/to/debug`

### Aggregation Efficiency

**Critical: Agent-Side Aggregation:**

The agent **must aggregate samples before storage** to avoid overwhelming the
database:

1. **BPF Map Aggregation** (every 15 seconds):
    - BPF hash map counts identical stack traces over 285 samples (19Hz × 15s)
    - Each unique stack stored once with count (not 285 individual records)
    - Typical result: 100-200 unique stacks per 15s interval (instead of 285
      samples)
    - **Data reduction: ~60% at collection time**

2. **Example Without Aggregation** (anti-pattern):
    ```
    19Hz × 15s = 285 samples
    → 285 separate DB inserts every 15 seconds
    → 68,400 inserts/hour per service
    → Overwhelms OTLP exporter and DuckDB
    ```

3. **Example With Aggregation** (correct approach):
    ```
    19Hz × 15s = 285 samples
    → Aggregate in BPF map by unique stack
    → ~100 unique stacks with counts
    → 100 DB inserts every 15 seconds
    → 24,000 inserts/hour per service (71% reduction)
    ```

**Stack Merging Algorithm:**

- Use BPF hash map: `map[stackID]count` where stackID is hash of stack frames
- Identical stacks automatically merged in kernel space (zero-copy)
- O(1) increment per sample, O(N) final read for N unique stacks
- Typical N: 100-200 unique stacks per 15s interval

**Storage Format - Frame Dictionary Encoding:**

Instead of storing frame names repeatedly, use a frame dictionary:

1. **Frame ID Assignment**:
    - On first encounter of frame name: assign next available frame_id
    - Insert into `profile_frame_dictionary_local`: `(frame_id, frame_name)`
    - Increment reference count: `frame_count++`
    - Cache mapping in memory: `map[frameName]frameID`

2. **Stack Encoding**:
    - Convert `['main', 'foo', 'bar']` → `[2, 3, 4]` using dictionary lookup
    - Store integer array in profile table
    - Integer arrays: 4 bytes per frame (vs 20-50 bytes for strings)
    - **Compression: 85% per stack**

3. **Base-Heavy Optimization**:
    - Common base frames (e.g., `libc_start_main`, `main`) stored once in
      dictionary
    - Shared across millions of stacks
    - Example: If 10,000 stacks start with `[1, 2]` (libc_start_main, main):
        - String storage: 10,000 × 60 bytes = 600KB
        - Integer storage: 10,000 × 8 bytes = 80KB
        - Dictionary overhead: 1 × 60 bytes = 60 bytes (amortized to 0.006 bytes
          per stack)
        - **Savings: 87% (600KB → 80KB)**

4. **DuckDB Columnar Storage & Compression**:
    - DuckDB stores arrays in columnar format with automatic dictionary encoding
    - Repeated integer patterns (e.g., base frames `[1, 2, ...]`) compressed
      automatically
    - **Additional 2-3x compression on top of frame dictionary**
    - Array containment queries (`= ANY(array)`) scan compressed columns
      efficiently
    - No explicit indexes needed - columnar storage is naturally fast for
      analytics

**Colony Aggregation:**

- Query 4 × 15s samples from agent (4 separate DB rows)
- Merge into single 1-minute summary by summing counts per stack (array
  equality)
- Calculate percentages: `(stack_count / total_samples) * 100`
- Store only stacks with >0.5% of total samples (optional pruning)
- **Further reduction: 75% (4 intervals → 1 summary)**

## Security Considerations

### Privileges

- **CAP_BPF / CAP_PERFMON**: Already required by RFD 070, no additional
  privileges needed.
- **CAP_SYSLOG**: Required for kernel symbolization (optional, can disable).

### Data Exposure

- **Stack Traces**: Expose function names, file paths, and code structure
    - Risk: Leak intellectual property or internal architecture details
    - Mitigation: RBAC controls at colony level, encrypted storage at rest
- **Kernel Stacks**: May expose kernel internals
    - Risk: Reveal kernel vulnerabilities or security mechanisms
    - Mitigation: Optional kernel stack collection, disabled by default in
      multi-tenant environments

### Resource Limits

**Agent-Side Limits:**

- Max stack trace depth: 127 frames (BPF limit)
- Max unique stacks per interval: 10,000 (prevent memory exhaustion)
- Max sample count per stack: 2^32 (uint32 limit)
- Sampling frequency cap: 100Hz (prevent DoS via config manipulation)

**Colony-Side Limits:**

- Max profile size per query: 100MB (prevent memory exhaustion)
- Max time range per query: 7 days (prevent expensive aggregations)
- Rate limiting: 10 profile queries per minute per user

### Storage Security

- Database files stored in colony storage path (same permissions as metrics)
- No sensitive data in profiles (only function names and counts)
- Encrypt profiles at rest if required (use OS-level encryption)

## Integration with Existing Features

### RFD 070: On-Demand CPU Profiling

- **Complementary Use Cases**:
    - Continuous: Always-on background profiling for trend analysis
    - On-Demand: High-frequency detailed profiling for active debugging
- **Shared Infrastructure**:
    - Same BPF program (`cpu_profile.bpf.c`)
    - Same symbolization logic
    - Same CLI command with different flags
    - **Same aggregation strategy**: Both use BPF map to aggregate before
      storage
- **Switching Between Modes**:
    - Background profiling runs at 19Hz by default
    - On-demand profiling temporarily switches to 99Hz for duration
    - After on-demand profile completes, revert to 19Hz continuous mode

**Important: RFD 070 Aggregation Requirement**

RFD 070's on-demand profiling (99Hz for 30s = 2,970 samples) **must also
aggregate** before sending to colony:

- **Without aggregation**: 2,970 individual sample records → overwhelms
  database
- **With aggregation**: ~200-300 unique stacks with counts → manageable
- **Data reduction**: ~90% (2,970 samples → 300 unique stacks)

This applies to RFD 070's existing implementation and should be verified/added
if not already present.

### RFD 071: System Metrics

- **Correlation**:
    - Correlate CPU usage spikes (system metrics) with hot code paths (profiles)
    - Example: "CPU 89% → Top stack: database query loop"
- **Query Integration**:
    - `coral query summary` could show top CPU-consuming stacks alongside system
      metrics
    - Automatic drill-down from system metrics to profile data
- **Storage Pattern Consistency**:
    - Both use 15s agent collection, 1-minute colony aggregation
    - Both use 1-hour agent retention, 30-day colony retention
    - Both use DuckDB for storage

### RFD 067: Unified Query Interface

- **Profile Queries**:
    - Add CPU profiling to unified query CLI
    - `coral query cpu-profile --service api --since 1h`
- **Cross-Cutting Analysis**:
    - Combine profiles with traces, metrics, and logs
    - Example: "Show CPU profile during P95 latency spike"

## Testing Strategy

### Unit Tests

**Agent-Side:**

- Test continuous profiler collection loop (mock BPF program)
- Test build ID extraction from ELF binaries
- Test binary metadata registration and updates
- Test stack aggregation and collapsing logic
- Test local DuckDB storage and cleanup
- Test RPC handler for historical sample queries
- Test symbol cache keyed by build_id

**Colony-Side:**

- Test profile poller aggregation logic
- Test stack merging across 4 × 15s samples
- Test percentage calculations
- Test 30-day retention cleanup

### Integration Tests

**End-to-End Collection:**

- Start agent with continuous profiling enabled
- Verify samples appear in local DuckDB every 15 seconds
- Verify colony polls and aggregates samples every minute
- Verify historical queries return correct data

**Storage Tests:**

- Run continuous profiling for 2 hours
- Verify agent retention cleanup (only last 1 hour remains)
- Verify colony storage grows predictably (4 × 15s → 1 × 1m)

**Performance Tests:**

- Measure CPU overhead of 19Hz sampling (should be <1%)
- Measure memory overhead (should be <1MB per service)
- Measure symbolization latency (should be <100ms per 15s interval)

### E2E Tests

**Historical Flame Graph:**

- Run test workload with known CPU pattern
- Wait 15 minutes (collect sufficient samples)
- Query historical profile: `--since 15m`
- Generate flame graph and verify hot functions match expected pattern

**Regression Detection:**

- Deploy service with fast code path
- Collect profile for 5 minutes
- Deploy service with slow code path (artificial regression)
- Collect profile for 5 minutes
- Compare profiles: `--compare-with`
- Verify differential flame graph highlights regression

**Build ID Version Tracking:**

- Deploy service v1, collect profiles for 10 minutes
- Deploy service v2 (different binary, new build_id)
- Collect profiles for 10 minutes
- Query spanning deployment: `--since 20m`
- Verify both build_ids present in results
- Verify stacks correctly associated with their binary versions
- Verify flame graph annotates multiple versions

## Future Work

The following features are out of scope for this RFD and may be addressed in
future enhancements:

**Integrated Flame Graph Rendering** (Future RFD)

- Generate SVG flame graphs directly in CLI (without `flamegraph.pl`)
- Embed interactive flame graphs in Colony UI
- Clickable flame graphs with drill-down to source code

**Debug Symbol Storage** (Enhancement)

- Upload debug symbols to colony for long-term retention
- Store stripped binaries in object storage (S3, MinIO)
- Enable offline symbolization of historical profiles
- `coral debug upload-symbols --build-id abc123 --symbols /path/to/debug`
- Automatic symbol extraction during deployment (CI/CD integration)
- Benefits: Symbolize profiles even after binary deleted from agents

**Differential Flame Graphs** (Enhancement)

- Automatic regression detection across deployments
- Visual diff showing which code paths increased CPU usage
- Statistical significance testing (identify real regressions vs noise)
- Leverage build_id tracking to compare specific versions

**Profile Comparison API** (Enhancement)

- Compare profiles from different time ranges programmatically
- API: `CompareCPUProfiles(baseline, current) → PerformanceReport`
- Output: Top N regressions with percentage increases

**Adaptive Sampling** (Advanced Optimization)

- Automatically increase sampling frequency during detected anomalies
- Switch from 19Hz → 99Hz when CPU usage exceeds threshold
- Reduces overhead during normal operation, increases detail during incidents

**Multi-Language Symbolization** (Enhancement)

- Extend symbolization to support Go, Python, Node.js, Java
- Use language-specific symbol sources (Go runtime, DWARF, JIT maps)
- Requires per-language symbol resolution logic

**Off-CPU Profiling** (Future RFD)

- Continuous off-CPU profiling using scheduler tracepoints
- Complement CPU profiles with I/O/lock wait analysis
- Requires separate eBPF program and storage

**GPU Profiling** (ML/GPU Workloads)

- Extend continuous profiling to GPU kernels
- Requires NVIDIA/AMD GPU tracing APIs
- Out of scope for general observability

**Profile-Based Alerts** (Future RFD)

- Alert when specific stack traces exceed threshold percentage
- Example: "Database query loop >50% of CPU samples"
- Requires alerting infrastructure (RFD TBD)

**Profile Compression** (Storage Optimization)

- Implement trie-based stack compression (store common prefixes once)
- Reduce storage by 50-70% for deep stack traces
- Trade-off: More complex query logic

## Appendix

### ELF Build ID Extraction

**NT_GNU_BUILD_ID Note:**

ELF binaries compiled with `--build-id` contain a unique identifier in the
`.note.gnu.build-id` section. This is a 20-byte SHA-1 hash of the binary
contents.

**Extraction Method:**

```go
// Example: Extract build ID from ELF binary
import (
    "debug/elf"
    "encoding/hex"
)

func extractBuildID(binaryPath string) (string, error) {
    f, err := elf.Open(binaryPath)
    if err != nil {
        return "", err
    }
    defer f.Close()

    // Read NT_GNU_BUILD_ID note section
    section := f.Section(".note.gnu.build-id")
    if section == nil {
        return "", fmt.Errorf("no build-id found")
    }

    data, err := section.Data()
    if err != nil {
        return "", err
    }

    // Parse ELF note structure (skip 12-byte header)
    // Format: namesz(4) + descsz(4) + type(4) + name(align) + desc
    if len(data) < 16 {
        return "", fmt.Errorf("invalid note section")
    }

    // Build ID starts at offset 16 (after header + "GNU\0" name)
    buildID := data[16:36] // 20 bytes
    return hex.EncodeToString(buildID), nil
}
```

**Fallback: Content Hash:**

If binary lacks NT_GNU_BUILD_ID (e.g., Go binaries built without `-buildid`),
compure binary hash.

**Build ID from /proc:**

For running processes, read from procfs:

```bash
# Extract build ID from running process
$ readelf -n /proc/12345/exe | grep "Build ID"
Build ID: abc123def456...

# Or use eu-readelf (faster)
$ eu-readelf -n /proc/12345/exe
```

**Compiler Flags:**

Ensure binaries are compiled with build IDs:

```bash
# GCC/Clang (default in modern versions)
gcc -Wl,--build-id=sha1 main.c -o main

# Go (built-in since Go 1.18)
go build -buildid=true ./...

# Rust
rustc --emit=link -C link-arg=-Wl,--build-id main.rs
```

### Stack Trace Format

**Collapsed Stack Format (Brendan Gregg Standard):**

```
main;processRequest;parseJSON;unmarshal 847
main;processRequest;validateData 623
main;processRequest;saveToDatabase;executeQuery 1523
kernel`entry_SYSCALL_64;do_syscall_64;sys_read;vfs_read 234
```

Each line: `<frame1>;<frame2>;...;<frameN> <sample_count>`

- Frames separated by semicolons
- Deepest frame last (leaf function)
- Sample count at end of line
- Compatible with `flamegraph.pl` tool

**Kernel Stack Notation:**

- Kernel frames prefixed with `kernel` backtick
- Example: `kernel`entry_SYSCALL_64`

### Flame Graph Generation

**Using flamegraph.pl:**

```bash
# Generate flame graph from historical profile
coral debug cpu-profile --service api --since 1h > profile.folded
flamegraph.pl profile.folded > cpu.svg

# Open in browser
open cpu.svg
```

**Differential Flame Graph:**

```bash
# Baseline (before deployment)
coral debug cpu-profile --service api \
    --since "2025-12-14 10:00:00" \
    --until "2025-12-14 11:00:00" \
    > baseline.folded

# Current (after deployment)
coral debug cpu-profile --service api \
    --since "2025-12-15 10:00:00" \
    --until "2025-12-15 11:00:00" \
    > current.folded

# Generate differential flame graph
difffolded.pl baseline.folded current.folded | flamegraph.pl > diff.svg
```

### Efficient Stack Queries with Frame Dictionary

**Frame Dictionary Benefits:**

Using `INTEGER[]` with a frame dictionary enables powerful queries with massive
storage savings:

**1. Find Stacks Containing Specific Function:**

```sql
-- Find all stacks that call parseJSON (at any depth)
-- First, get frame_id for parseJSON
WITH target_frame AS (
    SELECT frame_id
    FROM profile_frame_dictionary_local
    WHERE frame_name = 'parseJSON'
)
SELECT
    p.timestamp,
    p.service_id,
    p.stack_frame_ids,
    p.sample_count,
    -- Decode stack for display
    ARRAY(
        SELECT d.frame_name
        FROM unnest(p.stack_frame_ids) WITH ORDINALITY AS u(frame_id, ord)
        JOIN profile_frame_dictionary_local d ON d.frame_id = u.frame_id
        ORDER BY u.ord
    ) as stack_frames
FROM cpu_profile_samples_local p
WHERE (SELECT frame_id FROM target_frame) = ANY(p.stack_frame_ids)
ORDER BY p.sample_count DESC;

-- Performance: DuckDB's columnar storage + dictionary encoding makes this fast
```

**2. Prefix Matching (Call Path Analysis):**

```sql
-- Find stacks starting with main → processRequest
-- First, get frame_ids
WITH frame_ids AS (
    SELECT frame_id, frame_name
    FROM profile_frame_dictionary_local
    WHERE frame_name IN ('main', 'processRequest')
)
SELECT
    p.timestamp,
    p.service_id,
    p.stack_frame_ids,
    p.sample_count
FROM cpu_profile_samples_local p
WHERE
    p.stack_frame_ids[1] = (SELECT frame_id FROM frame_ids WHERE frame_name =
'main')
    AND p.stack_frame_ids[2] = (SELECT frame_id FROM frame_ids WHERE frame_name
= 'processRequest')
ORDER BY p.timestamp DESC;

-- Performance: Array indexing on integers is O(1) per row, smaller memory
footprint
```

**3. Call Depth Analysis:**

```sql
-- Find deep call stacks (potential recursion issues)
SELECT
    service_id,
    stack_frames,
    array_length(stack_frames, 1) as depth,
    SUM(sample_count) as total_samples
FROM cpu_profile_samples_local
WHERE array_length(stack_frames, 1) > 50  -- Stacks deeper than 50 frames
GROUP BY service_id, stack_frames, depth
ORDER BY total_samples DESC;
```

**4. Leaf Function Analysis:**

```sql
-- Find most expensive leaf functions (bottom of stack)
SELECT
    d.frame_name as leaf_function,
    SUM(p.sample_count) as total_samples,
    COUNT(DISTINCT p.stack_frame_ids) as unique_call_paths
FROM cpu_profile_samples_local p
JOIN profile_frame_dictionary_local d
    ON d.frame_id = p.stack_frame_ids[array_length(p.stack_frame_ids, 1)]
WHERE p.timestamp > NOW() - INTERVAL 1 HOUR
GROUP BY d.frame_name
ORDER BY total_samples DESC
LIMIT 20;
```

**5. Hotpath Detection:**

```sql
-- Find common call path prefixes (hotpaths)
SELECT
    stack_frames[1:5] as call_prefix,  -- First 5 frames
    SUM(sample_count) as total_samples,
    COUNT(*) as stack_variants
FROM cpu_profile_samples_local
WHERE timestamp > NOW() - INTERVAL 1 HOUR
GROUP BY call_prefix
HAVING SUM(sample_count) > 100
ORDER BY total_samples DESC;
```

**6. Performance Regression Detection:**

```sql
-- Compare function costs across deployments
WITH before AS (
    SELECT
        unnest(stack_frames) as function_name,
        SUM(sample_count) as samples
    FROM cpu_profile_summaries
    WHERE build_id = 'abc123'  -- Old version
    GROUP BY function_name
),
after AS (
    SELECT
        unnest(stack_frames) as function_name,
        SUM(sample_count) as samples
    FROM cpu_profile_summaries
    WHERE build_id = 'def456'  -- New version
    GROUP BY function_name
)
SELECT
    COALESCE(before.function_name, after.function_name) as function_name,
    COALESCE(before.samples, 0) as samples_before,
    COALESCE(after.samples, 0) as samples_after,
    (COALESCE(after.samples, 0) - COALESCE(before.samples, 0)) as delta,
    CASE
        WHEN before.samples > 0 THEN
            ((after.samples - before.samples) * 100.0 / before.samples)
        ELSE NULL
    END as pct_change
FROM before
FULL OUTER JOIN after ON before.function_name = after.function_name
WHERE ABS(COALESCE(after.samples, 0) - COALESCE(before.samples, 0)) > 50
ORDER BY ABS(delta) DESC
LIMIT 20;
```

**Output Format Conversion:**

When generating flame graphs, decode integer arrays and convert to collapsed
format:

```sql
-- Convert integer-encoded stacks to Brendan Gregg collapsed stack format
SELECT
    (
        SELECT array_to_string(
            ARRAY(
                SELECT d.frame_name
                FROM unnest(p.stack_frame_ids) WITH ORDINALITY AS u(frame_id,
ord)
                JOIN profile_frame_dictionary_local d ON d.frame_id = u.frame_id
                ORDER BY u.ord
            ),
            ';'
        )
    ) || ' ' || p.sample_count as folded
FROM cpu_profile_samples_local p
WHERE p.timestamp > NOW() - INTERVAL 1 HOUR
ORDER BY p.timestamp;

-- Performance: Dictionary lookup cached in memory, minimal overhead
```

**Optimized Query with Materialized Frame Names:**

For better performance, create a view that pre-joins frame names:

```sql
-- Create view with decoded stacks (cached by DuckDB)
CREATE VIEW cpu_profile_samples_decoded AS
SELECT
    p.timestamp,
    p.service_id,
    p.build_id,
    p.sample_count,
    ARRAY(
        SELECT d.frame_name
        FROM unnest(p.stack_frame_ids) WITH ORDINALITY AS u(frame_id, ord)
        JOIN profile_frame_dictionary_local d ON d.frame_id = u.frame_id
        ORDER BY u.ord
    ) as stack_frames
FROM cpu_profile_samples_local p;

-- Then queries become simpler:
SELECT
    array_to_string(stack_frames, ';') || ' ' || sample_count as folded
FROM cpu_profile_samples_decoded
WHERE timestamp > NOW() - INTERVAL 1 HOUR;
```

### Storage Optimization Techniques

**1. Stack Pruning:**

```sql
-- Store only stacks with >0.5% of total samples
INSERT INTO cpu_profile_summaries (...)
SELECT ...
FROM cpu_profile_samples_local
WHERE (sample_count * 100.0 / total_samples) > 0.5;
```

**2. Top-N Limitation:**

```sql
-- Store only top 100 hottest stacks per time bucket
INSERT INTO cpu_profile_summaries (...)
SELECT ...
FROM (
    SELECT *, ROW_NUMBER() OVER (
        PARTITION BY bucket_time, service_id
        ORDER BY sample_count DESC
    ) as rank
    FROM cpu_profile_samples_local
)
WHERE rank <= 100;
```

**3. Common Prefix Compression:**

Store stack traces in trie structure:

```
main                         (stored once)
├── processRequest           (stored once)
│   ├── parseJSON → unmarshal
│   ├── validateData
│   └── saveToDatabase → executeQuery
```

References stored as node IDs instead of full strings.

### Performance Benchmarks (Expected)

| Metric                  | Target      | Acceptable Ceiling |
|-------------------------|-------------|--------------------|
| CPU Overhead            | <1%         | <2%                |
| Memory per Service      | ~500KB      | <2MB               |
| Storage per Service/Day | ~21MB       | <50MB              |
| Symbolization Latency   | <50ms       | <200ms             |
| Query Latency (1h)      | <100ms      | <500ms             |
| Query Latency (30d)     | <2s         | <10s               |

### Related Tools

**Production Profilers:**

- **Google Cloud Profiler**: Continuous profiling for GCP services
- **Datadog Continuous Profiler**: SaaS continuous profiling
- **Parca**: Open-source continuous profiling (similar to this RFD)
- **Pyroscope**: Continuous profiling with flame graph UI

**Key Differences:**

- Coral integrates profiling with distributed tracing and metrics
- eBPF-based (no language-specific instrumentation)
- Local-first storage (no mandatory cloud upload)
- Built into existing debugging workflow

---

## Implementation Notes

**Reuse from RFD 070:**

- BPF program: `cpu_profile.bpf.c` (only frequency parameter changes)
- Symbolization: `Symbolizer` component with DWARF/ELF parsing
- CLI: Extend existing `coral debug cpu-profile` command
- RPC: Add new endpoint to existing `DebugService`

**New Components:**

- `ContinuousCPUProfiler` (agent background loop)
- `CPUProfilePoller` (colony aggregator)
- Database tables (agent + colony)
- Historical query logic (CLI)

**Configuration Migration:**

- Default: Continuous profiling enabled
- Users can disable: `continuous_profiling.cpu.enabled: false`
- On-demand profiling (RFD 070) remains available regardless of this setting
