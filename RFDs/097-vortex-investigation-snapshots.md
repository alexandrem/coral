---
rfd: "097"
title: "Vortex-Encoded Investigation Snapshots"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "039", "096" ]
database_migrations: [ ]
areas: [ "agent", "cli", "duckdb", "observability" ]
---

# RFD 097 - Vortex-Encoded Investigation Snapshots

**Status:** 🚧 Draft

## Summary

Add a `coral duckdb export` CLI command that exports agent telemetry data as
[Vortex](https://vortex.rs) (`.vx`) files — a next-generation columnar format
with faster random-access reads than Parquet and native Apache Arrow
compatibility. Exported snapshots are self-contained and can be opened
offline in any Arrow-compatible tool (Python, Polars, Pandas) without
requiring a live agent connection, enabling investigation sharing and
post-incident analysis.

## Problem

- **Current behavior**: Querying agent telemetry with `coral duckdb shell`
  and `coral duckdb query` (RFD 039 / RFD 096) requires the agent to be
  online and reachable via the colony proxy. There is no way to export a
  point-in-time snapshot of agent data for offline analysis or sharing with
  teammates not connected to the colony.

- **Why this matters**: LLM-driven investigation sessions (Skills) and
  post-incident reviews often need to slice through gigabytes of Beyla
  telemetry (HTTP metrics, distributed traces, gRPC metrics) with random
  column access patterns — e.g., "find all trace spans for service X in the
  last 10 minutes where `http.status_code >= 500`". Protobuf serialization
  and JSON intermediaries are ill-suited for this: the current gRPC poll loop
  converts each DuckDB row to a Protobuf struct and back, doubling
  serialization cost. Parquet — the common export format — requires
  decompressing large column chunks even when only one or two columns are
  needed.

- **Use cases affected**:
  - **Post-incident review**: An incident responder wants to hand off a
    snapshot of the last 30 minutes of HTTP metrics to a colleague running
    Jupyter offline.
  - **Skills / LLM investigation**: A sandboxed TypeScript skill needs to
    perform hundreds of random-access lookups across wide Beyla trace tables
    in under a second.
  - **Reproducible analysis**: A team wants to reproduce an investigation from
    a past alert without needing the agent data to still be within the
    one-hour local retention window.

## Solution

Enable the DuckDB Vortex extension on the agent's local databases, expose a
new `/vortex` HTTP endpoint on the agent for on-demand segment generation, and
add `coral duckdb export` as a CLI command (proxied through the colony, like
the existing DuckDB proxy from RFD 096).

**Key Design Decisions:**

- **Vortex via DuckDB extension, not a separate pipeline**: DuckDB's community
  Vortex extension (`LOAD vortex`) enables `COPY (SELECT ...) TO 'file.vx'
  (FORMAT vortex)` with no additional dependencies. This keeps the
  implementation self-contained and avoids a new binary dependency on the
  agent host.

- **Agent-side generation**: The agent generates the `.vx` segment and streams
  it over HTTP. The CLI receives a complete, portable file rather than having
  to run a local DuckDB with Vortex installed. This ensures consistency with
  the existing HTTP-based DuckDB proxy architecture (RFD 096).

- **Opt-in initially**: The Vortex DuckDB extension is community-maintained
  and newer than core DuckDB. Load it conditionally; if the extension is
  unavailable the agent falls back gracefully and the `/vortex` endpoint
  returns `501 Not Implemented`.

- **Arrow compatibility over zero-copy transport**: "Zero-copy" network
  streaming is a common framing but misleading — data is always copied over
  the wire. The real gain is eliminating serialization transforms between
  DuckDB's columnar layout and external tools. The correct approach for
  transport optimization is Apache Arrow Flight, which is deferred to a
  future RFD.

**Benefits:**

- Shareable, offline-capable investigation snapshots.
- Significantly faster selective column reads than Parquet for LLM workloads
  (random access by trace ID, service name, status code).
- Snapshots open natively in Python/Polars/Pandas via the Arrow/Vortex
  readers — no Coral tooling needed on the recipient's machine.
- Foundation for a future cold-storage strategy (S3 export as Vortex instead
  of Parquet).

**Architecture Overview:**

```
CLI (coral duckdb export)
  │
  │  HTTPS (colony proxy)
  ▼
Colony  /agent/{agentID}/vortex/{db}/{table}
  │
  │  HTTP (WireGuard mesh)
  ▼
Agent  /vortex/{db}/{table}
  │
  │  COPY ... TO tmp.vx (FORMAT vortex)
  ▼
Agent DuckDB (beyla.duckdb / telemetry.duckdb)
  │
  ▼
.vx file streamed to CLI → saved to disk
```

### Component Changes

1. **Agent**:
   - Load the Vortex DuckDB extension at startup (after existing extension
     initialization), logging a warning if unavailable.
   - New HTTP handler registered at `/vortex/<db>/<table>` (and
     `/vortex/<db>` with a `?query=` parameter for custom SQL). The handler
     runs `COPY (...) TO <tmpfile> (FORMAT vortex)`, streams the file, and
     removes the temp file.
   - **Disk safety pre-flight**: before running `COPY`, the handler estimates
     the output size (via `SELECT count(*) * avg_row_size` or a DuckDB
     `EXPLAIN`) and checks available disk space on the temp directory's
     filesystem. If the projected write would push utilization above a
     configurable threshold (default: 80%), the handler returns
     `413 Payload Too Large` with a JSON body describing available and
     projected bytes, and does not run `COPY`. This prevents ENOSPC on
     constrained or edge agent hosts.
   - **Streaming investigation**: if a future Vortex extension version
     supports writing to a FIFO or stdout, the handler should prefer that
     path to eliminate the temp file entirely. The current implementation
     uses a temp file; the design should isolate the write path to make this
     swap straightforward.
   - The existing `/duckdb` discovery endpoint gains a `vortex_enabled` field
     to let the CLI skip the export command if unavailable.

2. **Colony**:
   - Extend the existing DuckDB reverse proxy (RFD 096) to also proxy
     `/agent/{agentID}/vortex[/*]` to `http://{meshIP}:9001/vortex/*`.
   - No new gRPC or protobuf changes required.

3. **CLI**:
   - New `coral duckdb export <agent-id> <table> [--query <sql>]
     [--database <db>] --output <file.vx>` subcommand.
   - Omitting `--output` prints a suggested filename based on agent ID, table,
     and timestamp.
   - Prints a short usage hint after export (e.g., how to open in Python).

**Configuration Example:**

```yaml
agent:
  storage:
    vortex_enabled: true          # default: true; set false to disable /vortex endpoint
    vortex_disk_threshold: 0.80   # refuse export if temp dir utilization would exceed this
```

## Implementation Plan

### Phase 1: Agent — Vortex Extension and HTTP Endpoint

- [ ] Load the `vortex` DuckDB extension in agent database initialization;
      log a warning (not fatal) if the extension is missing.
- [ ] Add `vortex_enabled bool` to the `/duckdb` discovery JSON response.
- [ ] Implement `/vortex/<db>/<table>` HTTP handler: validate the requested
      database is registered, run `COPY (SELECT * FROM <table>) TO <tmpfile>
      (FORMAT vortex)`, stream the file, delete temp file.
- [ ] Implement `/vortex/<db>` handler with `?query=<sql>` parameter for
      custom SQL exports; enforce the same registered-database allowlist as
      the existing DuckDB handler.
- [ ] Implement disk safety pre-flight: estimate projected output size before
      `COPY`, check available bytes on the temp directory filesystem, return
      `413 Payload Too Large` with `{"available_bytes": N, "projected_bytes": M}`
      if projected utilization would exceed `agent.storage.vortex_disk_threshold`
      (default: `0.80`).
- [ ] Add `vortex_disk_threshold` config key under `agent.storage`
      (float 0–1, default: `0.80`).
- [ ] Return `501 Not Implemented` when `vortex_enabled` is false.
- [ ] Add `vortex_enabled` config key under `agent.storage` (default: `true`).

### Phase 2: Colony — Proxy Extension

- [ ] Extend the colony DuckDB reverse proxy to forward
      `/agent/{agentID}/vortex[/*]` to the agent's `/vortex/*` endpoint,
      using the same mesh-IP resolution and auth model as RFD 096.
- [ ] Propagate the agent's HTTP status (including `501`) unchanged to the
      CLI so the CLI can surface a clear "Vortex not available on this agent"
      message.

### Phase 3: CLI — Export Command

- [ ] Add `coral duckdb export <agent-id> <table>` subcommand under the
      existing `coral duckdb` command tree.
- [ ] Support `--database <db>` (default: `beyla`), `--query <sql>` (custom
      SQL overrides `<table>`), and `--output <file.vx>`.
- [ ] Auto-generate filename `<agent-id>-<table>-<timestamp>.vx` when
      `--output` is omitted.
- [ ] After successful export, print file size and a usage hint:
      ```
      Saved 42 MB → payments-http-2026-03-11T14:30Z.vx

      Open in Python:
        import vortex
        tbl = vortex.read("payments-http-2026-03-11T14:30Z.vx").to_arrow()
      ```
- [ ] Return a non-zero exit code and clear error message when the agent
      reports `501` (Vortex extension unavailable).

### Phase 4: Testing and Documentation

- [ ] Unit tests: Vortex handler validates allowlist, returns 501 when
      disabled, streams a valid file for a known table.
- [ ] Unit tests: CLI export command parses flags, auto-generates filename,
      handles 501 gracefully.
- [ ] Integration test: agent with Vortex extension loaded exports
      `beyla_http_metrics_local`; result is a valid Vortex file.
- [ ] Integration test: colony proxy correctly forwards `/vortex/*` requests.
- [ ] Update `docs/CLI.md` and `docs/CLI_REFERENCE.md` with `coral duckdb
      export` command and flags.
- [ ] Update `docs/STORAGE.md` with Vortex extension requirement and
      `agent.storage.vortex_enabled` config key.

## API Changes

### New HTTP Endpoints (Agent)

```
GET /vortex/<db>/<table>
```

Exports the full contents of `<table>` from registered database `<db>` as a
Vortex `.vx` file.

- **Response**: `200 OK`, `Content-Type: application/octet-stream`,
  `Content-Disposition: attachment; filename="<table>.vx"`
- **Errors**: `400` bad table name, `404` database not registered, `501`
  Vortex extension unavailable.

```
GET /vortex/<db>?query=<sql>
```

Exports the result of a custom SQL query as a Vortex file. The query must
reference only tables within the registered `<db>` database.

- **Response**: same as above with `filename="query-export.vx"`.
- **Errors**: same as above; `400` on invalid SQL.

### Colony Proxy Route (Extension of RFD 096)

```
GET /agent/{agentID}/vortex/{db}/{table}
GET /agent/{agentID}/vortex/{db}?query=<sql>
```

Proxied transparently to the agent's `/vortex/*` endpoint.

### CLI Commands

```bash
# Export a full table as a Vortex file
coral duckdb export <agent-id> <table> [--database <db>] [--output <file.vx>]

# Export a custom SQL query result
coral duckdb export <agent-id> --query "SELECT * FROM beyla_http_metrics_local
  WHERE timestamp > now() - INTERVAL 15 MINUTES" --output recent.vx

# Examples:
coral duckdb export agent-abc123 beyla_http_metrics_local
# → Saved 18 MB → agent-abc123-beyla_http_metrics_local-2026-03-11T14:30Z.vx

coral duckdb export agent-abc123 beyla_traces_local \
  --database beyla --output traces.vx
# → Saved 7 MB → traces.vx
```

### Discovery Response Change

The `/duckdb` discovery endpoint (JSON) gains one new field:

```json
{
  "databases": ["beyla", "telemetry"],
  "vortex_enabled": true
}
```

### Configuration Changes

- New key: `agent.storage.vortex_enabled` (boolean, default: `true`). Set
  `false` to disable the `/vortex` endpoint and skip loading the DuckDB
  extension.

## Testing Strategy

### Unit Tests

- Agent handler: allowlist enforcement, 501 on disabled, valid `.vx` output
  byte stream for a synthetic table.
- CLI: flag parsing, auto-filename generation, graceful 501 error message.

### Integration Tests

- Agent initializes with Vortex extension; `/vortex/beyla/beyla_http_metrics_local`
  returns a parseable Vortex file.
- Colony proxy forwards `/agent/{id}/vortex/*` and preserves HTTP status codes.
- `coral duckdb export` end-to-end: CLI exports a table, file is written to
  disk, file is a valid Vortex container (check magic bytes / header).

## Security Considerations

- The `/vortex` endpoint enforces the same registered-database allowlist as
  `/duckdb` (RFD 039). Arbitrary file paths are rejected.
- SQL queries via `?query=` are executed in read-only mode against a single
  registered database; cross-database joins are disallowed.
- The colony proxy inherits the existing mTLS / token auth from RFD 096. No
  new auth surface.
- Exported `.vx` files contain raw telemetry; operators should treat them with
  the same sensitivity as DuckDB files.

## Implementation Status

**Core Capability:** ⏳ Not Started

The Vortex DuckDB extension will be loaded at agent startup, enabling
on-demand export of any registered agent database table to a portable `.vx`
file. The `coral duckdb export` CLI command will download the snapshot via the
colony proxy and save it locally, printing an open-in-Python hint for
immediate offline analysis.

## Future Work

**Colony-Side Vortex Encoding** (Future RFD)

Encoding the colony's aggregated Beyla tables (`beyla_http_metrics`,
`beyla_traces`) with Vortex within DuckDB could accelerate the selective
column reads that TypeScript Skills perform during LLM investigations. Depends
on the Vortex extension maturing in the community DuckDB ecosystem.

## Appendix

### Why Not Parquet?

Parquet encodes data in row groups; reading a single column (e.g.,
`http.status_code` across 10 million rows) requires decompressing the entire
row group. Vortex uses late materialization: only the columns referenced by
the query are fetched from disk or memory, making selective lookups
significantly faster. For the wide Beyla tables (30+ columns, histogram
buckets, attributes), this is a meaningful advantage in LLM investigation
loops that issue many narrow queries.

### Vortex DuckDB Extension

The Vortex extension is loaded via:

```sql
INSTALL vortex FROM community;
LOAD vortex;
```

Export syntax:

```sql
COPY (SELECT * FROM beyla_http_metrics_local) TO '/tmp/export.vx' (FORMAT vortex);
```

The resulting `.vx` file is readable by the `vortex` Python package and any
Arrow-compatible reader.
