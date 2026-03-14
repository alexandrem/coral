---
rfd: "099"
title: "Issue Lifecycle"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "094", "097" ]
database_migrations: [ "add_issues_table", "add_issue_snapshots_table" ]
areas: [ "colony", "cli", "mcp", "observability" ]
---

# RFD 099 - Issue Lifecycle

**Status:** 🚧 Draft

## Summary

Add a lightweight issue registry to the colony so that diagnosed problems
persist beyond the current investigation session. When an issue is created,
the colony immediately snapshots the relevant agent telemetry as a Vortex
`.vx` file — preserving the precise raw data before the agent's rolling
retention window expires. Users can record deployment events, take additional
snapshots post-fix, query snapshot data directly, and verify whether a fix
held with AI-guided before/after comparison.

## Problem

- **Current behavior**: Coral's investigation loop ends when `coral ask`
  closes. There is no way to record what was diagnosed, preserve the raw
  telemetry that surfaced the problem, or compare metrics before and after a
  fix is deployed.

- **Why this matters**: The agent's rolling 1-hour retention window means
  pre-fix telemetry is permanently gone by the time a fix is deployed and
  verified. Even a short delay — finishing the diagnostic session, opening a
  ticket, deploying the fix — is enough for the window to roll and the
  evidence to disappear. Aggregated statistics alone are insufficient: teams
  need to re-query raw spans and request records to understand whether a fix
  addressed the root cause or merely shifted the problem.

- **Use cases affected**:
  - An SRE diagnoses a P99 latency spike on `checkout`, ships a fix, and
    has no way to confirm it is resolved other than eyeballing live metrics.
    The pre-fix request records are gone before the fix even deploys.
  - A multi-day slow-burn regression is discovered after the 1-hour agent
    window has rolled; there is no pre-regression snapshot to compare against
    or re-query.
  - A previously resolved issue regresses after a deployment; Coral has no
    record of the original issue signature to detect the recurrence.
  - A team wants to share evidence of a fix with stakeholders — "P99 dropped
    from 2.3s to 180ms" — but has to reconstruct this from memory or notes.

## Solution

Add a colony-side issue registry: two new DuckDB tables (`issues` and
`issue_snapshots`) that persist diagnosed issues and point-in-time telemetry
snapshots. When an issue is created, the colony immediately captures a
pre-fix snapshot — exporting relevant agent telemetry as a Vortex `.vx` file
via the RFD 097 agent endpoint — before the rolling window expires. Snapshots
are stored on colony-local disk and are queryable via DuckDB's
`read_vortex()`. A `coral issue` CLI subcommand manages the lifecycle. New
MCP tools expose the registry and snapshot data to the AI during `coral ask`
sessions.

**Key Design Decisions:**

- **Vortex as the snapshot format**: Vortex's columnar layout and late
  materialization mean re-investigation queries only read the columns they
  need (e.g., `duration_ms`, `trace_id`) rather than decompressing full row
  groups. The format is Arrow-native — snapshots open directly in
  Python/Polars/Pandas without Coral tooling. Since the colony already runs
  DuckDB, stored `.vx` files are queryable immediately via `read_vortex()`
  with no additional tooling.

- **RFD 097 as the snapshot mechanism**: The agent's `/vortex/{db}?query=<sql>`
  endpoint (RFD 097) accepts a filtered SQL query and streams the result as
  a `.vx` file. The colony passes a query scoped to the issue's service,
  route, and time window. No new agent-side mechanism is required.

- **Auto-snapshot on issue create**: When `coral issue create` runs, the
  colony immediately takes a `pre_fix` snapshot. Since issues are created
  during or immediately after a diagnostic session, the agent data is still
  within the retention window. This removes the manual step and eliminates
  the race against the rolling window.

- **Metric summary computed from the snapshot on ingest**: `metric_summary`
  (p50/p95/p99/error_rate) is derived from the `.vx` file at ingest time and
  stored as JSON for fast display in `coral issue list` and `verify`. The
  `.vx` file remains the source of truth for re-queries.

- **Colony-local storage**: Snapshots are written to
  `<colony-data-dir>/snapshots/<issue-id>/<snapshot-id>.vx`. This avoids a
  dependency on object storage for the core capability. S3/GCS storage is a
  future enhancement that reuses RFD 098 cold storage infrastructure.

- **Colony-side, not agent-side**: Issues and snapshots live in the colony
  DuckDB and on colony-local disk. The colony is the right durability
  boundary — agents rotate data every hour while the colony is persistent.

**Benefits:**

- Raw telemetry is preserved before the agent window rolls — re-investigation
  is possible weeks later.
- Snapshots are Arrow-native and shareable: a teammate opens the `.vx` in
  Python without needing Coral.
- `coral issue query` runs arbitrary SQL against a stored snapshot via
  `read_vortex()` — same query interface as live data.
- `coral issue verify` gives a percentage-based verdict instead of eyeballing
  live metrics.
- The AI can query snapshot data during later `coral ask` sessions via
  `coral_query_issue_snapshot`, enabling cross-session re-investigation.

**Architecture Overview:**

```
coral issue create checkout-latency
  │
  ├─ INSERT INTO issues (name, service, metric, route, threshold, status)
  │
  └─ auto pre_fix snapshot:
       colony → GET /agent/{id}/vortex/beyla?query=<scoped SQL>  (RFD 097)
       agent  → COPY (SELECT ...) TO tmp.vx (FORMAT vortex)
       colony ← .vx streamed over HTTP
       colony → write to <data-dir>/snapshots/<issue-id>/pre-fix.vx
       colony → compute metric_summary from .vx
       colony → INSERT INTO issue_snapshots (object_key, metric_summary, ...)

coral issue query checkout-latency pre-fix \
  "SELECT * FROM snapshot ORDER BY duration_ms DESC LIMIT 10"
  │
  └─ colony: SELECT * FROM read_vortex('<data-dir>/snapshots/.../pre-fix.vx')
             ORDER BY duration_ms DESC LIMIT 10

coral issue verify checkout-latency
  │
  ├─ load pre_fix metric_summary from issue_snapshots
  ├─ load post_fix metric_summary from issue_snapshots
  ├─ query live metrics via existing MCP query handlers
  └─ compute verdict + improvement % + stability duration
```

### Component Changes

1. **Colony**:
   - Two new DuckDB tables: `issues` and `issue_snapshots` (schema in API
     Changes).
   - `IssueStore`: CRUD for issues and snapshots; metric summary computation
     from a `.vx` file via an in-process DuckDB `read_vortex()` query.
   - Snapshot ingestion: on `create` and `snapshot`, the colony calls the
     agent's `/vortex` endpoint (via the colony proxy from RFD 097), streams
     the `.vx` to `<data-dir>/snapshots/<issue-id>/<snapshot-id>.vx`, and
     computes `metric_summary` on ingest.
   - Four new MCP tools: `coral_list_issues`, `coral_create_issue`,
     `coral_snapshot_issue`, `coral_verify_issue`, and
     `coral_query_issue_snapshot`.
   - The `coral ask` system prompt context gains a brief open-issues summary
     (name, service, status) so the AI is aware of known issues without a
     tool call.

2. **CLI**:
   - New `coral issue` subcommand group: `list`, `create`, `snapshot`,
     `deploy`, `verify`, `query`, `show`, `close`.
   - `coral issue verify` renders a before/after bar chart using the existing
     browser dashboard renderer from RFD 094 when a terminal session is
     active; falls back to plain terminal table otherwise.

3. **Agent**: no changes (uses the `/vortex` endpoint from RFD 097).

**Configuration Example:**

```yaml
colony:
  issues:
    snapshot_dir: ""         # default: <colony-data-dir>/snapshots
    max_snapshot_size_mb: 500  # per-snapshot limit; export is rejected above this
```

## Implementation Plan

### Phase 1: Foundation — Schema, IssueStore, and Vortex Snapshot Ingestion

- [ ] Add `issues` DuckDB table migration to the colony schema.
- [ ] Add `issue_snapshots` DuckDB table migration to the colony schema.
- [ ] Implement `IssueStore` in the colony: create, get, list, update
      status, delete; snapshot CRUD; name uniqueness enforcement.
- [ ] Implement snapshot ingestion in `IssueStore`: call the agent's
      `/vortex/{db}?query=<sql>` endpoint via the colony proxy (RFD 097),
      stream the `.vx` file to `snapshot_dir/<issue-id>/<snapshot-id>.vx`,
      run `SELECT percentile_cont(...) FROM read_vortex(path)` to compute
      `metric_summary`, persist the snapshot row.
- [ ] Implement `coral issue create` auto-snapshot: immediately after
      inserting the issue row, take a `pre_fix` snapshot scoped to the
      issue's service, route, and a configurable lookback window (default:
      last 30 minutes of agent data).
- [ ] Honour `max_snapshot_size_mb`: check `Content-Length` from the agent
      before writing; return a clear error if exceeded.
- [ ] Parse and validate `colony.issues` config block.
- [ ] Unit tests: IssueStore CRUD, name uniqueness, status transitions;
      metric summary computation from a synthetic `.vx` fixture; snapshot
      file written to correct path.

### Phase 2: CLI — Issue Lifecycle and Query Commands

- [ ] Add `coral issue` command group to the CLI root.
- [ ] Implement `coral issue create <name> --service <s> --metric <m>
      [--route <r>] [--threshold <expr>] [--notes <text>]` — creates the
      issue and prints progress of the automatic pre-fix snapshot.
- [ ] Implement `coral issue list` — tabular output: name, service, metric,
      route, threshold, status, detected_at, snapshot count.
- [ ] Implement `coral issue show <name>` — full event history (snapshots,
      deploy markers, status changes) in chronological order with snapshot
      file size.
- [ ] Implement `coral issue snapshot <name> [--label <l>]
      [--snapshot-type <pre_fix|post_fix|checkpoint>]` — takes an additional
      manual snapshot.
- [ ] Implement `coral issue deploy <name> [--label <l>]` — inserts a
      `deployment` marker with current timestamp; no `.vx` capture.
- [ ] Implement `coral issue query <name> <snapshot-label> "<sql>"` — runs
      arbitrary SQL against the stored `.vx` via
      `SELECT ... FROM read_vortex('<path>')`, prints results as a table.
- [ ] Implement `coral issue close <name>` — sets status to `resolved`.
- [ ] Implement `coral issue verify <name>` — compares pre-fix snapshot
      metric summary to post-fix (or latest checkpoint) and current live
      metrics; prints structured verdict; renders bar chart via RFD 094
      browser dashboard when available.
- [ ] Unit tests: flag parsing for all subcommands; verify verdict logic
      (improvement %, stability duration, regression detection).

### Phase 3: MCP Tools and AI Integration

- [ ] Implement `coral_list_issues` MCP tool.
- [ ] Implement `coral_create_issue` MCP tool — AI can register an issue
      mid-investigation; triggers auto pre-fix snapshot.
- [ ] Implement `coral_snapshot_issue` MCP tool — AI can take a manual
      snapshot checkpoint.
- [ ] Implement `coral_verify_issue` MCP tool — returns structured verdict
      comparing pre-fix summary, post-fix summary, and current live metrics.
- [ ] Implement `coral_query_issue_snapshot` MCP tool — AI passes a SQL
      expression; colony runs it against the stored `.vx` via
      `read_vortex()` and returns rows; enables cross-session re-investigation
      of raw pre-fix data.
- [ ] Inject open-issue summary into the `coral ask` system prompt context
      (concise: name, service, status only).
- [ ] Integration test: `coral_verify_issue` returns correct structured
      response through a live colony.
- [ ] Integration test: `coral_query_issue_snapshot` runs SQL against a
      stored `.vx` and returns expected rows.

### Phase 4: Testing and Documentation

- [ ] Integration test: full CLI lifecycle — create (auto pre-fix snapshot)
      → deploy marker → manual post-fix snapshot → verify → close; assert
      status transitions, verdict fields, and `.vx` files on disk.
- [ ] Integration test: `coral issue query` returns expected rows from a
      synthetic snapshot fixture.
- [ ] Integration test: `coral ask "is checkout fixed?"` triggers
      `coral_verify_issue` and surfaces the verdict in the AI response.
- [ ] Update `docs/CLI.md` and `docs/CLI_REFERENCE.md` with `coral issue`
      subcommands and all flags.
- [ ] Update `docs/MCP.md` and `docs/CLI_MCP_MAPPING.md` with the five new
      MCP tools.
- [ ] Update `docs/COLONY.md` with issue registry behaviour, auto-snapshot
      semantics, and snapshot storage path.
- [ ] Update `docs/STORAGE.md` with `issues` and `issue_snapshots` schema
      and the `colony.issues` config block.

## API Changes

### New Database Tables

```sql
CREATE TABLE issues (
    id              UUID        DEFAULT gen_random_uuid() PRIMARY KEY,
    name            VARCHAR     NOT NULL UNIQUE,
    service_name    VARCHAR     NOT NULL,
    metric          VARCHAR     NOT NULL,   -- e.g. http_duration_p99, http_error_rate
    route           VARCHAR,                -- optional, e.g. POST /order
    threshold_value DOUBLE,                 -- optional numeric threshold (ms or ratio)
    threshold_dir   VARCHAR,                -- 'above' | 'below'
    status          VARCHAR     NOT NULL DEFAULT 'open',  -- open | resolved | regressed
    detected_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at     TIMESTAMPTZ,
    notes           TEXT
);

CREATE TABLE issue_snapshots (
    id              UUID        DEFAULT gen_random_uuid() PRIMARY KEY,
    issue_id        UUID        NOT NULL REFERENCES issues(id),
    snapshot_type   VARCHAR     NOT NULL,   -- pre_fix | post_fix | checkpoint | deployment
    label           VARCHAR,
    taken_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    agent_id        VARCHAR,
    source_table    VARCHAR,                -- agent table exported, e.g. beyla_http_metrics_local
    object_key      VARCHAR,               -- colony-local path or S3 key (RFD 098)
    size_bytes      UBIGINT,
    row_count       UBIGINT,
    metric_summary  JSON                   -- {p50, p95, p99, error_rate, sample_count}
                                           -- computed from .vx on ingest
);

CREATE INDEX idx_issue_snapshots_lookup
    ON issue_snapshots (issue_id, taken_at);
```

### New MCP Tools

```
coral_list_issues
  Returns all issues with their latest snapshot summary.
  → [{name, service_name, metric, route, threshold_value, threshold_dir,
      status, detected_at, snapshot_count, latest_summary}]

coral_create_issue
  args: name, service_name, metric, route?, threshold_value?,
        threshold_dir?, notes?
  Creates the issue and immediately takes a pre_fix snapshot.
  → {id, name, status: "open", snapshot: {id, size_bytes, metric_summary}}

coral_snapshot_issue
  args: name, label?, snapshot_type?  (default: "checkpoint")
  Takes a Vortex snapshot of current agent telemetry for the issue's scope.
  → {snapshot_id, taken_at, size_bytes, metric_summary: {p50, p95, p99,
     error_rate, sample_count}}

coral_verify_issue
  args: name
  Compares pre_fix snapshot against post_fix (or latest checkpoint) and
  current live metrics.
  → {
      issue:           {name, service_name, metric, route, threshold_value, status},
      pre_fix:         {taken_at, metric_summary} | null,
      post_fix:        {taken_at, metric_summary} | null,
      current:         {metric_summary},
      verdict:         "resolved" | "improved" | "unchanged" | "regressed",
      improvement_pct: float,   -- relative change on primary metric vs pre_fix
      stable_since:    string   -- duration current metric has stayed within threshold
    }

coral_query_issue_snapshot
  args: name, snapshot_label, sql
  Runs SQL against the stored .vx for the named snapshot via read_vortex().
  sql may reference the snapshot as "snapshot" (aliased by the colony).
  → {columns: [...], rows: [[...], ...], row_count: int}
```

### CLI Commands

```bash
# List all tracked issues
coral issue list

# Example output:
NAME                SERVICE    METRIC              ROUTE          STATUS    SNAPSHOTS  DETECTED
checkout-latency    checkout   http_duration_p99   POST /order    open      2          2h ago
payments-errors     payments   http_error_rate                    resolved  3          3d ago

# Create a new issue — auto-snapshot fires immediately
coral issue create checkout-latency \
  --service checkout \
  --metric http_duration_p99 \
  --route "POST /order" \
  --threshold ">500ms" \
  --notes "P99 spiking to 2.3s since 14:00 UTC; correlates with DB lock contention."

# Example output:
Created issue: checkout-latency
Snapshotting pre-fix state from agent agent-abc123... done (34 MB, 842,301 rows)

# Query raw data from the pre-fix snapshot
coral issue query checkout-latency pre-fix \
  "SELECT * FROM snapshot ORDER BY duration_ms DESC LIMIT 5"

# Example output:
TRACE_ID          DURATION_MS  STATUS  ROUTE
abc123...         2341         500     POST /order
def456...         2180         200     POST /order
...

# Record a deployment event
coral issue deploy checkout-latency --label "v1.4.2: remove advisory lock on checkout"

# Take a post-fix snapshot
coral issue snapshot checkout-latency --label post-fix --snapshot-type post_fix

# Verify the fix
coral issue verify checkout-latency

# Example output:
Issue: checkout-latency  (checkout / http_duration_p99 / POST /order)
Threshold: >500ms  |  Status: open

              Pre-fix    Post-fix   Now
  p50         420ms      80ms       75ms
  p95         1.1s       140ms      130ms
  p99         2.3s       180ms      165ms
  error_rate  0.8%       0.1%       0.1%

Improvement:  -93% on p99 since pre-fix snapshot (2026-03-13 14:00)
Stable for:   1h 22m below 500ms threshold
Verdict:      ✓ Resolved — recommend closing this issue

# Show full event history
coral issue show checkout-latency

# Example output:
2026-03-13 14:00  snapshot    pre-fix    34 MB  842,301 rows  p99=2.3s  error_rate=0.8%
2026-03-13 15:12  deployment  v1.4.2 fix
2026-03-13 15:20  snapshot    post-fix    8 MB  201,044 rows  p99=180ms  error_rate=0.1%

# Close a resolved issue
coral issue close checkout-latency
```

### Configuration Changes

```yaml
colony:
  issues:
    snapshot_dir: ""            # default: <colony-data-dir>/snapshots
    max_snapshot_size_mb: 500   # per-snapshot limit; export is rejected above this
```

## Testing Strategy

### Unit Tests

- `IssueStore`: create, get, list, update status, name uniqueness constraint,
  status transitions (open → resolved, resolved → regressed).
- Snapshot ingestion: mock agent `/vortex` response with a synthetic `.vx`
  fixture; assert file written to correct path, `metric_summary` computed
  correctly, `size_bytes` and `row_count` populated.
- Verify verdict logic: improvement percentage calculation, stability
  duration, regression detection when re-snapshot is worse than pre-fix.
- `max_snapshot_size_mb` enforcement: mock oversized `Content-Length` and
  assert rejection before write.

### Integration Tests

- Full lifecycle: create issue (assert auto pre-fix snapshot file exists)
  → deploy marker → manual post-fix snapshot → verify; assert verdict fields
  match expected deltas and `.vx` files are present on disk.
- `coral issue query` against a stored `.vx` returns expected rows.
- `coral_query_issue_snapshot` MCP tool returns correct rows when called
  through a live colony.
- `coral ask "is checkout fixed?"` triggers `coral_verify_issue` and
  surfaces the verdict in the AI response.

## Security Considerations

- Snapshot `.vx` files are stored on colony-local disk with the same access
  controls as the colony data directory. No new network surface.
- `metric_summary` JSON contains only aggregated metric values (latency
  percentiles, error rates). The `.vx` files contain raw telemetry including
  URL paths, trace IDs, and timing data — treat with the same sensitivity as
  the colony DuckDB files.
- `coral_create_issue`, `coral_snapshot_issue`, and
  `coral_query_issue_snapshot` are write/read operations subject to the
  existing colony RBAC enforcement (RFD 058).
- SQL passed to `coral issue query` and `coral_query_issue_snapshot` is
  executed against a single read-only `.vx` file via `read_vortex()`. The
  colony validates the snapshot path from the manifest (not from user input)
  to prevent path traversal.

## Implementation Status

**Core Capability:** ⏳ Not Started

Issue registry tables, auto-snapshot-on-create via the RFD 097 Vortex
endpoint, `coral issue` CLI subcommand group, and five MCP tools
(`coral_list_issues`, `coral_create_issue`, `coral_snapshot_issue`,
`coral_verify_issue`, `coral_query_issue_snapshot`). The AI gains issue
context and raw snapshot query access during `coral ask` sessions and can
answer "is X fixed?" with a structured verdict.

## Future Work

**S3/GCS Snapshot Storage** (Future — depends on RFD 098)

Store snapshot `.vx` files in object storage instead of (or in addition to)
colony-local disk, using the RFD 098 cold storage infrastructure. The
`object_key` column in `issue_snapshots` already accommodates S3/GCS paths.
Enables snapshots that survive colony disk wipes and are shareable across
teams by URL.

**Multi-week Trend in Verify Output** (Future — depends on RFD 098)

When cold storage is enabled, `coral issue verify` can extend the timeseries
back weeks or months via the hot+cold federation shim, showing the full
trajectory of the metric from before the issue through resolution.

**Auto-created Issues from Correlation Triggers** (Future — depends on RFD 091)

When a correlation trigger fires (percentile alarm, causal pair),
automatically open an issue with the triggering metric as the signature and
take an immediate pre-fix snapshot. Closes the gap between anomaly detection
and the fix verification lifecycle.

**Regression Alerting** (Future)

Periodically re-evaluate resolved issues against live metrics. Surface a
warning in `coral ask` context and `coral issue list` when a resolved issue's
metric crosses its original threshold again. Requires a background goroutine
in the colony.
