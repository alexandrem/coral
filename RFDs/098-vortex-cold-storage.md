---
rfd: "098"
title: "Vortex Cold Storage for Colony Telemetry"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "032", "039", "046", "097" ]
database_migrations: [ "add_cold_storage_manifest_table" ]
areas: [ "colony", "cli", "duckdb", "observability" ]
---

# RFD 098 - Vortex Cold Storage for Colony Telemetry

**Status:** 🚧 Draft

## Summary

Add a cold storage tier to the colony: aged telemetry is exported to S3/GCS
as partitioned Vortex (`.vx`) segments before the local retention window
expires, then deleted from colony DuckDB. A query federation layer makes hot
(DuckDB) and cold (object storage) data transparently queryable together via
`coral duckdb query`, the MCP server, and TypeScript Skills — no awareness of
the storage tier required by callers.

## Problem

- **Current behavior**: Colony DuckDB enforces fixed retention windows (30
  days for HTTP/gRPC metrics, 7 days for traces). Data older than the
  retention limit is deleted permanently. There is no archive tier.

- **Why this matters**:
  - **Incident retrospectives** require weeks or months of history; the 7-day
    trace window is frequently insufficient after a slow-burn regression is
    discovered.
  - **Trend analysis** across releases or deployments requires months of
    HTTP error-rate and latency data; 30 days is a floor, not a ceiling.
  - **Compliance** requirements in some environments mandate retention of
    access logs and trace data for 90 days or more.
  - **Colony disk pressure**: extending local retention to satisfy these needs
    would grow colony DuckDB to tens or hundreds of GB, creating memory and
    backup burden. Object storage is an order of magnitude cheaper per GB and
    unbounded in capacity.

- **Use cases affected**:
  - `coral ask` / Skills query historical error rates across a multi-month
    window and receive an empty result because the data was deleted.
  - An operator running `coral duckdb query` to investigate a two-week-old
    incident finds no rows in `beyla_http_metrics`.
  - A compliance team requests an export of all HTTP traces for service X
    over the past 60 days.

## Solution

A background export job on the colony scans each telemetry table for rows
approaching the local retention limit, exports them as partitioned Vortex
segments to a configured object storage bucket, records the exported ranges in
a `cold_storage_manifest` table, and deletes the local rows. A query federation
shim transparently unions hot DuckDB tables with cold Vortex segments at query
time using DuckDB's native `httpfs` and Vortex extension.

**Key Design Decisions:**

- **Vortex over Parquet for cold segments**: Parquet requires decompressing
  full row groups to read a single column. Vortex's late materialization
  fetches only the columns referenced in a query, making selective reads
  (e.g., `WHERE service_name = 'payments'`) fast without downloading the full
  segment from S3. This is the primary performance advantage for LLM
  investigation workloads that issue many narrow queries.

- **DuckDB httpfs + Vortex extension for federation**: DuckDB's `httpfs`
  extension (already loaded for VSS) supports S3-compatible object storage.
  Combined with the Vortex extension, `SELECT * FROM read_vortex('s3://...')`
  pushes column and predicate filters to the S3 read path. No new query engine
  is required.

- **Partition layout by service + date**: Segments are stored at
  `<bucket>/<colony-id>/<table>/<service_name>/<date>/segment.vx`. This
  layout aligns with the most common query predicates (`service_name`,
  `timestamp` range) and lets DuckDB's partition pruning skip irrelevant
  segments entirely.

- **Manifest table for federation**: The colony maintains a
  `cold_storage_manifest` DuckDB table tracking every exported segment
  (table, service, date range, object key, row count). The federation shim
  reads the manifest to build the correct `UNION ALL` query across hot and
  cold sources rather than listing the object store at query time.

- **Transparent to callers**: The MCP server, Skills SDK, and CLI route all
  telemetry queries through a federation helper that automatically includes
  cold segments within the requested time range. No caller API changes.

**Benefits:**

- Unlimited retention without colony disk growth.
- Historical queries (weeks, months) work identically to recent queries.
- Object storage costs ≈10× less per GB than local NVMe.
- Vortex selective reads minimize egress charges for narrow queries.
- Exported segments are self-describing Arrow data, openable in Python/Polars
  without Coral tooling.

**Architecture Overview:**

```
Colony DuckDB (hot: 30d metrics, 7d traces)
  │
  │  Background export job (nightly / on threshold)
  ▼
Vortex Exporter
  COPY (SELECT ... WHERE timestamp < cutoff) TO 's3://.../segment.vx'
  INSERT INTO cold_storage_manifest (table, service, date, object_key, rows)
  DELETE FROM <table> WHERE timestamp < cutoff
  │
  ▼
Object Storage (S3/GCS)
  <bucket>/<colony-id>/beyla_http_metrics/payments/2026-01-15/segment.vx

             ┌─────────────┐
Query time:  │ Federation  │ ◄── coral ask / Skills / CLI
             │    Shim     │     SELECT ... FROM beyla_http_metrics
             └──────┬──────┘     WHERE timestamp > now() - 90d
                    │
         ┌──────────┴──────────┐
         ▼                     ▼
  Hot DuckDB table      Cold Vortex segments
  (last 30 days)        (manifest lookup → S3)
```

### Component Changes

1. **Colony**:
   - Background goroutine (`cold_storage_exporter.go`): runs on a configurable
     schedule, iterates each telemetry table, identifies rows older than the
     configured cold-storage threshold, exports per-service-per-day Vortex
     segments, updates the manifest, deletes exported rows.
   - New `cold_storage_manifest` DuckDB table (schema below).
   - Federation shim (`cold_storage_federation.go`): given a table name and
     time range, returns a DuckDB SQL expression that `UNION ALL`s the hot
     table with relevant cold segments via `read_vortex('s3://...')`.
   - Load the Vortex DuckDB extension at colony startup (same opt-in pattern
     as RFD 097; disable with config if extension unavailable).
   - MCP server and Skills SDK query helpers pass all telemetry queries through
     the federation shim.

2. **CLI**:
   - New `cold-storage` subcommand group under `coral colony`, consistent with
     the existing colony admin surface (`agents`, `ca`, `psk`, `service`).
   - `coral colony cold-storage list`: shows exported segment inventory (table,
     service, date range, size, row count) from the manifest.
   - `coral colony cold-storage export --force`: triggers an immediate export
     run outside the scheduled window.
   - `coral colony cold-storage restore <object-key>`: re-imports a cold
     segment into hot DuckDB for intensive local analysis.
   - `coral duckdb query` transparently federates — no new flag needed.

3. **Agent**: no changes.

**Configuration Example:**

```yaml
colony:
  cold_storage:
    enabled: true
    provider: s3                    # s3 | gcs | r2
    bucket: my-coral-cold-storage
    prefix: ""                      # optional path prefix within bucket
    region: us-east-1
    credentials:
      access_key_id: ""             # or use IAM role / workload identity
      secret_access_key: ""
    thresholds:
      beyla_http_metrics: 7d        # export rows older than 7d (keep 7d hot)
      beyla_grpc_metrics: 7d
      beyla_sql_metrics:  7d
      beyla_traces:       3d
      otel_spans:         3d
      system_metrics:     7d
    schedule: "0 3 * * *"          # cron: nightly at 03:00
    segment_max_rows: 1000000       # rows per .vx segment file
```

## Implementation Plan

### Phase 1: Foundation — Schema, Extension, Config

- [ ] Add `cold_storage_manifest` table migration to colony schema (see
      Database Changes below).
- [ ] Load the Vortex DuckDB extension at colony startup; log warning if
      unavailable, set `cold_storage.enabled = false` automatically.
- [ ] Load the `httpfs` extension with S3/GCS credential configuration from
      `colony.cold_storage` config block (the extension is already loaded for
      other features; extend credential init).
- [ ] Parse and validate `colony.cold_storage` config block; surface clear
      errors for missing bucket or credentials.

### Phase 2: Colony — Vortex Exporter

- [ ] Implement `cold_storage_exporter.go`: for each configured table,
      query rows older than the threshold partitioned by `(service_name, date)`,
      run `COPY (...) TO 's3://<bucket>/<prefix>/<table>/<service>/<date>/segment.vx'
      (FORMAT vortex)`, insert manifest row, delete exported rows — all in a
      single DuckDB transaction.
- [ ] Handle partial exports: if the COPY succeeds but the DELETE fails,
      detect the duplicate on next run via manifest and skip re-export.
- [ ] Implement scheduled runner using the configured cron expression.
- [ ] Expose export metrics: segments exported, rows exported, bytes written,
      last run timestamp (visible in `coral colony status`).

### Phase 3: Colony — Query Federation Shim

- [ ] Implement `cold_storage_federation.go`: given `(table, time_range)`,
      query the manifest for matching segments, return a DuckDB SQL fragment:
      ```sql
      SELECT * FROM hot_table WHERE ...
      UNION ALL
      SELECT * FROM read_vortex('s3://...') WHERE ...
      UNION ALL
      ...
      ```
- [ ] Integrate the shim into the colony's MCP query handlers
      (`QueryEbpfMetrics`, `QueryTelemetry`, etc.) when the requested time
      range extends beyond the hot retention window.
- [ ] Integrate into the Skills SDK `db.query()` helper for transparent
      federation.
- [ ] Ensure federation is skipped entirely when `cold_storage.enabled` is
      false (no performance impact on deployments without cold storage).

### Phase 4: CLI Commands

- [ ] Add `coral colony cold-storage list [--table <t>] [--service <s>]` — tabular
      output of manifest entries with size and row count.
- [ ] Add `coral colony cold-storage export --force` — triggers immediate export run.
- [ ] Add `coral colony cold-storage restore <object-key> [--database <db>]` — runs
      `INSERT INTO <table> SELECT * FROM read_vortex('<s3-key>')`.
- [ ] `coral duckdb query` already federates transparently via the shim;
      add a `--cold` flag to force inclusion of cold segments even within the
      hot window (for verification).

### Phase 5: Testing and Documentation

- [ ] Unit tests: exporter correctly partitions rows by service/date, skips
      already-exported ranges, handles partial export idempotently.
- [ ] Unit tests: federation shim generates correct `UNION ALL` SQL for a
      given time range; returns hot-only query when no cold segments overlap.
- [ ] Integration test: colony exports rows to a local MinIO instance; query
      spanning hot+cold returns the correct union of rows.
- [ ] Integration test: `coral colony cold-storage list` shows manifest entries after
      export.
- [ ] Integration test: `coral colony cold-storage restore` re-imports a segment and
      rows appear in the hot table.
- [ ] Update `docs/STORAGE.md` with cold storage architecture, partition
      layout, and manifest schema.
- [ ] Update `docs/CONFIG.md` with `colony.cold_storage` config block.
- [ ] Update `docs/CLI.md` and `docs/CLI_REFERENCE.md` with `coral
      cold-storage` subcommands.
- [ ] Update `docs/COLONY.md` with cold storage export behaviour and metrics.

## API Changes

### New Database Table

```sql
CREATE TABLE cold_storage_manifest (
    id           UUID DEFAULT gen_random_uuid() PRIMARY KEY,
    table_name   VARCHAR NOT NULL,
    service_name VARCHAR NOT NULL,
    date         DATE    NOT NULL,
    object_key   VARCHAR NOT NULL,  -- full S3/GCS object path
    row_count    UBIGINT NOT NULL,
    size_bytes   UBIGINT NOT NULL,
    min_seq_id   UBIGINT NOT NULL,
    max_seq_id   UBIGINT NOT NULL,
    min_ts       TIMESTAMPTZ NOT NULL,
    max_ts       TIMESTAMPTZ NOT NULL,
    exported_at  TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_cold_manifest_lookup
    ON cold_storage_manifest (table_name, service_name, date);
```

### CLI Commands

```bash
# List cold storage segments
coral colony cold-storage list
coral colony cold-storage list --table beyla_http_metrics --service payments

# Example output:
TABLE                  SERVICE   DATE        ROWS      SIZE
beyla_http_metrics     payments  2026-01-15  842,301   34 MB
beyla_http_metrics     payments  2026-01-16  901,102   37 MB
beyla_traces           checkout  2026-01-10  120,044    8 MB

# Trigger immediate export
coral colony cold-storage export --force

# Restore a segment to hot storage for intensive local analysis
coral colony cold-storage restore beyla_http_metrics/payments/2026-01-15/segment.vx

# Query spanning hot + cold (federation is automatic)
coral duckdb query agent-abc123 \
  "SELECT date_trunc('day', timestamp), avg(duration_ms)
   FROM beyla_http_metrics
   WHERE service_name = 'payments'
     AND timestamp > now() - INTERVAL 60 DAYS
   GROUP BY 1 ORDER BY 1"
```

### Configuration Changes

- New config block: `colony.cold_storage` (see Configuration Example in
  Solution section).
- New fields: `colony.cold_storage.enabled` (boolean, default: `false`),
  `colony.cold_storage.provider`, `colony.cold_storage.bucket`,
  `colony.cold_storage.thresholds.*`, `colony.cold_storage.schedule`.

## Testing Strategy

### Unit Tests

- Exporter: partition logic (correct service/date grouping), idempotency on
  re-run, transaction rollback on DELETE failure.
- Federation shim: SQL generation for overlapping and non-overlapping time
  ranges, hot-only path when cold storage disabled.

### Integration Tests

- Requires a local MinIO instance (S3-compatible); added to the test
  `docker-compose.yml`.
- Full export cycle: seed colony DuckDB, run exporter, verify manifest rows
  and S3 objects, verify local rows deleted.
- Query federation: insert rows spanning 60 days (synthetic), export old
  portion, run `SELECT ... WHERE timestamp > now() - 60d`, verify full result.
- Restore: restore a segment, verify rows reappear in hot table.

## Security Considerations

- S3/GCS credentials are stored in the colony config file (same sensitivity
  as database credentials). Recommend IAM roles / workload identity in
  production; document this in `docs/SECURITY.md`.
- Exported Vortex files contain raw telemetry including URL paths, service
  names, and trace IDs. Bucket access should be restricted to colony IAM
  roles; document recommended bucket policies.
- The federation shim constructs S3 object paths from the manifest (not from
  user input), preventing path traversal.
- `coral colony cold-storage restore` is gated on colony authentication (same as
  other colony-mutating CLI commands).

## Migration Strategy

Cold storage is opt-in (`enabled: false` by default). Existing deployments are
unaffected until a bucket is configured. No existing data or schema is modified
on upgrade; the `cold_storage_manifest` table is added as a new migration.

## Implementation Status

**Core Capability:** ⏳ Not Started

Background export job, federation shim, and `coral colony cold-storage` CLI
subcommands. Transparent to all existing callers once enabled.

## Future Work

**Vortex Encoding over Arrow Flight** (Future RFD)

Once RFD 099 (Arrow Flight transport) is stable, record batches flowing from
agent to colony can be Vortex-encoded before writing to the cold storage
segment, bypassing the intermediate DuckDB hot table for high-volume data
types. This creates a direct Beyla → Vortex → S3 path for archival-only data.

**Query Pushdown to Object Storage** (Future)

AWS S3 Select and GCS XML API both support server-side predicate evaluation.
DuckDB's httpfs extension can leverage these to push `WHERE service_name = ?`
filters to the storage layer, reducing egress for large segments. This is
enabled automatically when the object storage provider supports it; no
explicit Coral changes required.

**Multi-Colony Federation** (Future — RFD 003)

When multiple colonies are federated (RFD 003), cold storage segments from
different colonies could share a common bucket prefix, enabling cross-colony
historical queries from a single `coral duckdb query` invocation.

## Appendix

### S3 Object Layout

```
<bucket>/
  <colony-id>/
    beyla_http_metrics/
      payments/
        2026-01-15/
          segment.vx        ← up to segment_max_rows rows
          segment-001.vx    ← overflow if > segment_max_rows
      checkout/
        2026-01-15/
          segment.vx
    beyla_traces/
      payments/
        2026-01-10/
          segment.vx
```

Partition pruning: a query `WHERE service_name = 'payments' AND timestamp
BETWEEN '2026-01-10' AND '2026-01-20'` only reads objects under
`beyla_traces/payments/2026-01-10/` through `2026-01-20/`, skipping all other
services and dates entirely.

### Vortex vs Parquet for Cold Storage

Parquet encodes data in row groups with dictionary and RLE compression applied
per column chunk. Reading a single column (e.g., `http.status_code` over 1M
rows) decompresses the entire row group for that column. Vortex uses nested
encodings with late materialization: each column is independently addressable
and only fetched when referenced by the query. Combined with S3 byte-range
requests, a query for `WHERE service_name = 'payments' AND http_status_code >=
500` fetches only the two relevant column ranges from the segment, not the
full file.
