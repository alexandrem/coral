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

- **Current behavior/limitations**: Coral's investigation loop ends when `coral ask`
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
via the RFD 097 agent endpoint — before the rolling window expires. Each
snapshot is stored as a sidecar bundle: a directory containing the immutable
`data.vx`, a `meta.json` with issue and snapshot metadata, and an
`events.jsonl` for mutable status changes. Colony DuckDB is a queryable index
over these bundles, not the source of truth. A `coral issue` CLI subcommand
manages the lifecycle. New MCP tools expose the registry and snapshot data to
the AI during `coral ask` sessions.

**Key Design Decisions:**

- **Vortex as the snapshot format**: Vortex's columnar layout and late
  materialization mean re-investigation queries only read the columns they
  need (e.g., `duration_ms`, `trace_id`) rather than decompressing full row
  groups. The format is Arrow-native — snapshots open directly in
  Python/Polars/Pandas without Coral tooling.

- **Sidecar bundle layout**: Exploration confirmed that the Vortex format is
  strictly single-table and immutable; Arrow schema metadata is silently
  discarded on write (see `explorations/vortex-issue-bundle/outcome.md`).
  A single `.vx` file cannot carry both telemetry and metadata. Each snapshot
  is therefore stored as a directory bundle:

  ```
  <snapshot-id>/
    data.vx       ← immutable compressed telemetry rows
    meta.json     ← issue + snapshot metadata (written once at ingest)
    events.jsonl  ← append-only status log (open → resolved → regressed)
  ```

  This is semantically correct: telemetry is immutable by nature; mutable
  state (status changes) appends cheaply to `events.jsonl` without touching
  the `.vx` file.

- **DuckDB as a queryable index, not source of truth**: Colony DuckDB is
  rebuilt by scanning `meta.json` files across the snapshot directory. A
  colony reset is safe — bundles survive on disk and are reindexed by
  `coral issue reindex`. The DuckDB Vortex community extension is not
  reliably available across platforms; snapshot data is queried via the
  Arrow in-memory hand-off (`vortex.open("data.vx").to_arrow()` passed
  directly to DuckDB), which requires no extension.

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
  (p50/p95/p99/error_rate) is derived from the `data.vx` file at ingest time
  via the Arrow hand-off, serialised to `meta.json`, and also stored in
  `issue_snapshots` for fast display. The bundle directory is the source of
  truth; DuckDB rows are a cache.

- **Colony-local storage**: Bundles are written under
  `<colony-data-dir>/snapshots/<issue-name>/<snapshot-id>/`. This avoids a
  dependency on object storage for the core capability. S3/GCS storage is a
  future enhancement that reuses RFD 098 cold storage infrastructure.

- **Colony-side, not agent-side**: Issues and snapshots live on colony-local
  disk. The colony is the right durability boundary — agents rotate data every
  hour while the colony is persistent.

**Benefits:**

- Raw telemetry is preserved before the agent window rolls — re-investigation
  is possible weeks later.
- Bundles are self-contained and portable: share a snapshot by copying the
  directory; import it with `coral issue import`. `data.vx` opens directly
  in Python/Polars/Pandas via PyArrow.
- `coral issue query` runs arbitrary SQL against a stored snapshot via the
  Arrow hand-off — same query interface as live data, no DuckDB extension
  needed.
- `coral issue reindex` rebuilds colony DuckDB from bundle directories on
  disk — a colony reset loses no issue data.
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
       agent  → pre-flight: check disk utilization vs threshold
       agent  → 413 if projected write would exceed threshold  ← colony surfaces error, aborts
       agent  → COPY (SELECT ...) TO tmp.vx (FORMAT vortex)
       colony ← .vx streamed over HTTP
       colony → write bundle: snapshots/<issue-name>/<snapshot-id>/
                               ├ data.vx        (streamed from agent)
                               ├ meta.json      (issue + snapshot metadata)
                               └ events.jsonl   (initial "created" event)
       colony → compute metric_summary: vortex.open(data.vx).to_arrow() → DuckDB
       colony → INSERT INTO issue_snapshots (bundle_dir, metric_summary, ...)

coral issue query checkout-latency pre-fix \
  "SELECT * FROM snapshot ORDER BY duration_ms DESC LIMIT 10"
  │
  └─ colony: arrow_tbl = vortex.open("snapshots/.../data.vx").to_arrow()
             SELECT * FROM arrow_tbl ORDER BY duration_ms DESC LIMIT 10

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
     Changes). These are a queryable index; the bundle directories on disk
     are the source of truth.
   - `IssueStore`: CRUD for issues and snapshots; metric summary computation
     by opening `data.vx` via the Arrow hand-off and running aggregation
     queries in DuckDB (no Vortex extension required).
   - Snapshot ingestion: on `create` and `snapshot`, the colony calls the
     agent's `/vortex` endpoint (via the colony proxy from RFD 097), streams
     `data.vx` into a new bundle directory, writes `meta.json` and the
     initial `events.jsonl` entry, then computes `metric_summary` and inserts
     the `issue_snapshots` row.
   - `coral issue reindex`: scans `snapshot_dir` for bundle directories,
     reads each `meta.json` and `events.jsonl`, and rebuilds `issues` and
     `issue_snapshots` tables from scratch.
   - Four new MCP tools: `coral_list_issues`, `coral_create_issue`,
     `coral_snapshot_issue`, `coral_verify_issue`, and
     `coral_query_issue_snapshot`.
   - The `coral ask` system prompt context gains a brief open-issues summary
     (name, service, status) so the AI is aware of known issues without a
     tool call.

2. **CLI**:
   - New `coral issue` subcommand group: `list`, `create`, `snapshot`,
     `deploy`, `verify`, `query`, `show`, `close`, `export`, `import`.
   - `coral issue export` calls a colony HTTP endpoint that tars the bundle
     directory on-demand and streams it; the CLI writes the stream to a
     local file. `coral issue import` is the inverse — uploads a local
     tar to the colony.
   - `coral issue verify` renders a before/after bar chart using the existing
     browser dashboard renderer from RFD 094 when a terminal session is
     active; falls back to plain terminal table otherwise.

3. **Agent**: no new code. The `/vortex` endpoint from RFD 097 handles the
   export, including the disk safety pre-flight that returns `413 Payload Too
Large` when the temp file write would push disk utilization above the
   configured threshold. The colony treats `413` as a recoverable error and
   surfaces a clear message to the user rather than failing silently.

**Configuration Example:**

```yaml
colony:
  issues:
    snapshot_dir: ""         # default: <colony-data-dir>/snapshots
    max_snapshot_size_mb: 500  # per-snapshot limit; export is rejected above this
```

## Implementation Plan

### Phase 1: Foundation — Schema, IssueStore, and Vortex Snapshot Ingestion

- [ ] Add `coral/colony/v1/issues.proto` with `IssueService` and all
      messages defined in API Changes; run `buf generate`.
- [ ] Register `IssueService` handler on the colony's Buf Connect server.
- [ ] Add `issues` DuckDB table migration to the colony schema.
- [ ] Add `issue_snapshots` DuckDB table migration to the colony schema.
- [ ] Implement `IssueStore` in the colony: create, get, list, update
      status, delete; snapshot CRUD; name uniqueness enforcement.
- [ ] Implement snapshot ingestion in `IssueStore`: call the agent's
      `/vortex/{db}?query=<sql>` endpoint via the colony proxy (RFD 097),
      stream `data.vx` into a new bundle directory
      (`snapshot_dir/<issue-name>/<snapshot-id>/`), write `meta.json` with
      issue and snapshot metadata, write the initial `events.jsonl` entry,
      open `data.vx` via the Arrow hand-off to compute `metric_summary`
      (no DuckDB Vortex extension required), and persist the
      `issue_snapshots` row.
- [ ] Implement `coral issue reindex`: scan `snapshot_dir` recursively for
      bundle directories, parse each `meta.json` and replay `events.jsonl`,
      truncate and rebuild `issues` and `issue_snapshots` tables.
- [ ] Implement `coral issue create` auto-snapshot: immediately after
      inserting the issue row, take a `pre_fix` snapshot scoped to the
      issue's service, route, and a configurable lookback window (default:
      last 30 minutes of agent data).
- [ ] Honour `max_snapshot_size_mb`: check `Content-Length` from the agent
      before writing; return a clear error if exceeded.
- [ ] Handle `413 Payload Too Large` from the agent `/vortex` endpoint:
      surface the available/projected byte counts from the response body as a
      human-readable error (e.g., "Agent disk too full to snapshot: 2.1 GB
      projected, 400 MB available. Lower the lookback window or free space on
      the agent host."); do not create the bundle directory or snapshot row.
- [ ] Parse and validate `colony.issues` config block.
- [ ] Unit tests: IssueStore CRUD, name uniqueness, status transitions;
      metric summary computation from a synthetic `.vx` fixture; snapshot
      file written to correct path.

### Phase 2: CLI — Issue Lifecycle and Query Commands

- [ ] Add `coral issue` command group to the CLI root.
- [ ] Implement `coral issue reindex` — truncates and rebuilds `issues` and
      `issue_snapshots` from bundle directories on disk.
- [ ] Implement colony HTTP endpoint `GET /issues/{name}/bundle` (and
      `GET /issues/{name}/snapshots/{id}/bundle` for a single snapshot): tars
      the bundle directory on the colony host and streams it as
      `application/x-tar` with gzip compression. No temp file required —
      stream directly from `archive/tar` + `compress/gzip` into the response
      writer.
- [ ] Implement `coral issue export <name> [--snapshot <label>]
    [--output <file.tar.gz>]` — calls the colony HTTP endpoint and writes
      the stream to a local file; auto-generates filename
      `<name>-<snapshot-label>-<timestamp>.tar.gz` when `--output` is
      omitted.
- [ ] Implement `coral issue import <path>` — streams the local tar.gz to
      the colony via `POST /issues/import`, extracts it under `snapshot_dir`,
      and runs reindex for the imported bundle.
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
- [ ] Implement `coral issue query <name> <snapshot-label> "<sql>"` — opens
      `data.vx` via the Arrow hand-off, registers it as a DuckDB view named
      `snapshot`, executes the user-supplied SQL, and prints results as a
      table.
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
      expression; colony opens `data.vx` via the Arrow hand-off, registers
      it as a DuckDB view, runs the query, and returns rows; enables
      cross-session re-investigation of raw pre-fix data.
- [ ] Implement `coral_export_issue` MCP tool — AI can suggest and initiate
      a bundle download mid-session; returns a download token or direct URL
      the user can retrieve with `coral issue export`.
- [ ] Inject open-issue summary into the `coral ask` system prompt context
      (concise: name, service, status only).
- [ ] Integration test: `coral_verify_issue` returns correct structured
      response through a live colony.
- [ ] Integration test: `coral_query_issue_snapshot` runs SQL against a
      stored `.vx` and returns expected rows.

### Phase 4: Testing and Documentation

- [ ] Integration test: full CLI lifecycle — create (auto pre-fix snapshot)
      → deploy marker → manual post-fix snapshot → verify → close; assert
      status transitions, verdict fields, and bundle directories on disk.
- [ ] Integration test: `coral issue query` returns expected rows from a
      synthetic snapshot fixture.
- [ ] Integration test: `coral issue export` streams a valid tar.gz; extract
      locally and assert `data.vx`, `meta.json`, and `events.jsonl` are
      present and correct.
- [ ] Integration test: `coral issue import` on the exported archive
      re-creates the issue and snapshot rows in a fresh colony.
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
    bundle_dir      VARCHAR,               -- path to bundle directory: <snapshot_dir>/<issue>/<id>/
                                           --   data.vx       immutable telemetry rows
                                           --   meta.json     issue + snapshot metadata
                                           --   events.jsonl  append-only status log
    size_bytes      UBIGINT,               -- size of data.vx
    row_count       UBIGINT,
    metric_summary  JSON                   -- {p50, p95, p99, error_rate, sample_count}
                                           -- computed from data.vx via Arrow hand-off on ingest
                                           -- also written into meta.json; DuckDB row is a cache
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
  Opens data.vx via Arrow hand-off, registers it as a DuckDB view named
  "snapshot", runs the SQL, and returns rows.
  → {columns: [...], rows: [[...], ...], row_count: int}

coral_export_issue
  args: name, snapshot_label?
  Invokes IssueService.ExportBundle and streams the result to a local file.
  Returns the path of the saved file so the user can locate it.
  → {path: "/tmp/checkout-latency-pre-fix-2026-03-16T15:00Z.tar.gz",
     size_bytes: 35651584}
```

### New Protobuf Service

New file: `coral/colony/v1/issues.proto`

```protobuf
syntax = "proto3";

package coral.colony.v1;

import "google/protobuf/struct.proto";
import "google/protobuf/timestamp.proto";

option go_package = "github.com/coral-mesh/coral/proto/colony/v1;colonypb";

// IssueService manages the issue registry: diagnosed problems, Vortex
// snapshots, and the fix verification lifecycle.
service IssueService {
  // Create a new issue and immediately take a pre_fix snapshot.
  rpc CreateIssue(CreateIssueRequest) returns (CreateIssueResponse);

  // List all issues with their latest snapshot summary.
  rpc ListIssues(ListIssuesRequest) returns (ListIssuesResponse);

  // Get a single issue with full snapshot history.
  rpc GetIssue(GetIssueRequest) returns (GetIssueResponse);

  // Take a manual snapshot of the issue's current metric scope.
  rpc SnapshotIssue(SnapshotIssueRequest) returns (SnapshotIssueResponse);

  // Record a deployment event marker (no .vx capture).
  rpc RecordDeployment(RecordDeploymentRequest) returns (RecordDeploymentResponse);

  // Compare pre_fix and post_fix snapshots against live metrics.
  rpc VerifyIssue(VerifyIssueRequest) returns (VerifyIssueResponse);

  // Run SQL against a stored snapshot via Arrow hand-off.
  rpc QuerySnapshot(QuerySnapshotRequest) returns (QuerySnapshotResponse);

  // Mark an issue as resolved.
  rpc CloseIssue(CloseIssueRequest) returns (CloseIssueResponse);

  // Rebuild the DuckDB index from bundle directories on disk.
  rpc ReindexIssues(ReindexIssuesRequest) returns (ReindexIssuesResponse);

  // Export a snapshot bundle as a gzip-compressed tar stream.
  rpc ExportBundle(ExportBundleRequest) returns (stream ExportBundleChunk);

  // Import a snapshot bundle from a gzip-compressed tar stream.
  rpc ImportBundle(stream ImportBundleChunk) returns (ImportBundleResponse);
}

// Issue represents a diagnosed problem tracked through its fix lifecycle.
message Issue {
  // Unique identifier.
  string id = 1;

  // Human-readable unique name (e.g., "checkout-latency").
  string name = 2;

  // Service the issue was diagnosed on.
  string service_name = 3;

  // Metric key (e.g., "http_duration_p99", "http_error_rate").
  string metric = 4;

  // Optional route filter (e.g., "POST /order").
  string route = 5;

  // Optional numeric threshold value.
  double threshold_value = 6;

  // Threshold direction ("above" | "below").
  string threshold_dir = 7;

  // Lifecycle status ("open" | "resolved" | "regressed").
  string status = 8;

  // When the issue was first diagnosed.
  google.protobuf.Timestamp detected_at = 9;

  // When the issue was closed. Zero value if still open.
  google.protobuf.Timestamp resolved_at = 10;

  // Optional free-text notes from the diagnostic session.
  string notes = 11;
}

// MetricSummary holds aggregated metric values for a snapshot.
message MetricSummary {
  // p50 latency in milliseconds (or fraction for error_rate).
  double p50 = 1;

  // p95 latency in milliseconds.
  double p95 = 2;

  // p99 latency in milliseconds.
  double p99 = 3;

  // Error rate as a fraction (0.0–1.0).
  double error_rate = 4;

  // Number of samples the summary was computed from.
  int64 sample_count = 5;
}

// IssueSnapshot represents one point-in-time telemetry capture.
message IssueSnapshot {
  // Unique identifier.
  string id = 1;

  // Parent issue ID.
  string issue_id = 2;

  // Snapshot type ("pre_fix" | "post_fix" | "checkpoint" | "deployment").
  string snapshot_type = 3;

  // Optional human-readable label.
  string label = 4;

  // When the snapshot was taken.
  google.protobuf.Timestamp taken_at = 5;

  // Agent the telemetry was exported from.
  string agent_id = 6;

  // Agent DuckDB table exported (e.g., "beyla_http_metrics_local").
  string source_table = 7;

  // Path to the bundle directory on colony disk.
  // Contains: data.vx (immutable rows), meta.json (metadata), events.jsonl (status log).
  string bundle_dir = 8;

  // Size of data.vx in bytes.
  int64 size_bytes = 9;

  // Number of telemetry rows in data.vx.
  int64 row_count = 10;

  // Aggregated metric values computed from data.vx on ingest.
  MetricSummary metric_summary = 11;
}

message CreateIssueRequest {
  // Unique name for the issue.
  string name = 1;

  // Service to scope the snapshot to.
  string service_name = 2;

  // Metric key to track.
  string metric = 3;

  // Optional route filter.
  string route = 4;

  // Optional numeric threshold (e.g., 500 for 500 ms).
  double threshold_value = 5;

  // Threshold direction ("above" | "below").
  string threshold_dir = 6;

  // Optional free-text notes.
  string notes = 7;
}

message CreateIssueResponse {
  // Created issue.
  Issue issue = 1;

  // Automatically taken pre_fix snapshot.
  IssueSnapshot pre_fix_snapshot = 2;
}

message ListIssuesRequest {
  // Filter by status. Empty returns all issues.
  string status = 1;
}

message ListIssuesResponse {
  repeated Issue issues = 1;
}

message GetIssueRequest {
  // Issue name.
  string name = 1;
}

message GetIssueResponse {
  Issue issue = 1;

  // All snapshots in chronological order.
  repeated IssueSnapshot snapshots = 2;
}

message SnapshotIssueRequest {
  // Issue name.
  string name = 1;

  // Optional label.
  string label = 2;

  // Snapshot type ("pre_fix" | "post_fix" | "checkpoint"). Defaults to "checkpoint".
  string snapshot_type = 3;
}

message SnapshotIssueResponse {
  IssueSnapshot snapshot = 1;
}

message RecordDeploymentRequest {
  // Issue name.
  string name = 1;

  // Optional deployment label (e.g., "v1.4.2: remove advisory lock").
  string label = 2;
}

message RecordDeploymentResponse {
  // Inserted deployment marker.
  IssueSnapshot marker = 1;
}

message VerifyIssueRequest {
  // Issue name.
  string name = 1;
}

message VerifyIssueResponse {
  Issue issue = 1;

  // Most recent pre_fix snapshot. Empty if none exists.
  IssueSnapshot pre_fix = 2;

  // Most recent post_fix or checkpoint snapshot. Empty if none exists.
  IssueSnapshot post_fix = 3;

  // Current live metric values queried at call time.
  MetricSummary current = 4;

  // Verdict ("resolved" | "improved" | "unchanged" | "regressed").
  string verdict = 5;

  // Relative change on the primary metric vs pre_fix (negative = improvement).
  double improvement_pct = 6;

  // How long the metric has stayed within threshold (e.g., "1h 22m").
  // Empty if no threshold is configured or it has not yet been met.
  string stable_since = 7;
}

message QuerySnapshotRequest {
  // Issue name.
  string name = 1;

  // Snapshot label to query (e.g., "pre-fix").
  string snapshot_label = 2;

  // SQL expression. Snapshot data is registered as a DuckDB view named "snapshot".
  string sql = 3;

  // Maximum rows to return. Defaults to 100.
  int32 limit = 4;
}

message QuerySnapshotResponse {
  // Column names in result order.
  repeated string columns = 1;

  // Result rows as JSON-typed value lists.
  repeated google.protobuf.ListValue rows = 2;

  // Total rows returned.
  int64 row_count = 3;
}

message CloseIssueRequest {
  // Issue name.
  string name = 1;
}

message CloseIssueResponse {
  // Updated issue with status "resolved".
  Issue issue = 1;
}

message ReindexIssuesRequest {}

message ReindexIssuesResponse {
  // Number of issues indexed from bundle directories.
  int32 issues_indexed = 1;

  // Number of snapshots indexed.
  int32 snapshots_indexed = 2;

  // Non-fatal errors encountered (e.g., malformed meta.json). Does not fail the RPC.
  repeated string errors = 3;
}

message ExportBundleRequest {
  // Issue name.
  string name = 1;

  // Optional snapshot label. Empty exports all snapshots for the issue.
  string snapshot_label = 2;
}

// ExportBundleChunk carries one chunk of the gzip-compressed tar stream.
message ExportBundleChunk {
  // Raw bytes of the tar.gz stream.
  bytes data = 1;

  // Total size in bytes. Set only in the first chunk for progress reporting.
  int64 total_size_bytes = 2;
}

// ImportBundleChunk carries one chunk of an incoming gzip-compressed tar stream.
message ImportBundleChunk {
  // Raw bytes of the tar.gz stream.
  bytes data = 1;

  // When true, overwrites an existing issue with the same name.
  // Honoured only on the first chunk.
  bool force = 2;
}

message ImportBundleResponse {
  // Name of the imported issue.
  string issue_name = 1;

  // Number of snapshots imported.
  int32 snapshot_count = 2;

  // Total bytes received.
  int64 size_bytes = 3;
}
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

# Rebuild colony DuckDB index from bundle directories on disk
coral issue reindex

# Download the full issue bundle to local disk
coral issue export checkout-latency
# → Saved 34 MB → checkout-latency-2026-03-16T15:00Z.tar.gz

# Download a specific snapshot only
coral issue export checkout-latency --snapshot pre-fix \
  --output checkout-latency-pre-fix.tar.gz
# → Saved 34 MB → checkout-latency-pre-fix.tar.gz

# Import a bundle received from a teammate into a different colony
coral issue import checkout-latency-pre-fix.tar.gz
# → Imported: checkout-latency (1 snapshot, 34 MB)
# → Run 'coral issue list' to confirm.
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

Store snapshot bundle directories in object storage instead of (or in
addition to) colony-local disk, using the RFD 098 cold storage
infrastructure. Enables snapshots that survive colony disk wipes and are
shareable across teams by URL. `bundle_dir` in `issue_snapshots` will
accommodate S3/GCS prefixes.

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

## Appendix

### Vortex Format Exploration

`explorations/vortex-issue-bundle/outcome.md` documents the test results that
informed the sidecar bundle design. Key findings:

- **Arrow schema metadata is silently discarded** by `vortex.io.write()` — it
  cannot be used as a metadata carrier.
- **A `.vx` file is strictly single-table** — multi-table writes raise an
  error; there is no named-section or container-format layer in v0.64.0.
- **`.vx` files are immutable** — no append API exists. Rewriting a 9 MB file
  takes ~184 ms, which is acceptable for rare status mutations, but the
  `events.jsonl` sidecar eliminates the need entirely.
- **DuckDB Vortex community extension is not available on `osx_arm64`** for
  any tested DuckDB version (1.0–1.5). The Arrow in-memory hand-off
  (`vortex.open("data.vx").to_arrow()` passed to DuckDB) is the confirmed
  working alternative and requires no extension.

The sidecar bundle layout (`data.vx` + `meta.json` + `events.jsonl`) emerged
from these constraints and is also semantically correct: immutable telemetry
and mutable lifecycle state are naturally separate concerns.
