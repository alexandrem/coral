---
rfd: "035"
title: "CLI Query Framework for Observability Data"
state: "in-progress"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "025", "030", "032" ]
database_migrations: [ ]
areas: [ "cli", "observability", "query" ]
---

# RFD 035 - CLI Query Framework for Observability Data

**Status:** 🚧 In Progress

## Summary

Create a unified CLI query framework that provides intuitive commands for
querying
observability data from Coral, including Beyla RED metrics, OTLP traces, custom
eBPF events, and service topology. This framework builds on the pull-based gRPC
APIs established in RFD 025 (OTLP) and RFD 032 (Beyla) to deliver a consistent
user experience for exploring distributed system behavior.

## Problem

**Current behavior/limitations:**

- RFD 032 implemented Beyla metrics storage and gRPC APIs, but no user-facing
  query interface exists
- Users cannot easily retrieve HTTP/gRPC latency distributions, error rates, or
  trace data
- No unified command structure for observability queries across different data
  types (metrics vs traces vs events)
- Each new observability feature requires ad-hoc CLI implementation

**Why this matters:**

- CLI is the primary interface for operators and developers debugging production
  issues
- Consistent query syntax reduces cognitive load and enables muscle memory
- Integration with `coral tap` and `coral ask` requires foundational query
  capabilities
- Future observability features (custom eBPF, service mesh metrics) need
  extensible framework

**Use cases affected:**

- "Show me HTTP P95 latency for payments-api over the last hour"
- "Find the trace for request ID abc123"
- "List all services with error rate > 5% in the last 10 minutes"
- "Compare gRPC latency before and after deployment"

## Solution

Create `coral query` command framework with subcommands for different data
types,
following patterns from `kubectl`, `aws`, and other cloud CLIs.

**Key Design Decisions:**

- **Unified `coral query` namespace**: All observability queries use `coral query
  <data-type>` pattern for consistency
- **Time-range expressions**: Support natural language time ranges (
  `--since 1h`,
  `--from 2025-11-15T10:00:00`, `--last 30m`)
- **Service-centric filtering**: Default to filtering by service name, with
  optional attribute filters
- **Multiple output formats**: Table (default), JSON, CSV for programmatic
  consumption
- **Progressive disclosure**: Simple queries by default, advanced filters via
  flags
- **Colony-aware**: Query multiple colonies and merge results transparently

**Benefits:**

- Operators can quickly diagnose issues without writing SQL or understanding
  internal schemas
- Consistent UX across all observability data types
- Extensible framework supports future data sources without CLI redesign
- Output formats enable integration with scripts and dashboards

**Architecture Overview:**

```
coral query ebpf http <service>
          ↓
    CLI Parser (cobra)
          ↓
    Query Builder
          ↓
    Colony Client (gRPC)
          ↓
    QueryBeylaMetrics RPC → Colony DuckDB
          ↓
    Format & Display
```

### Component Changes

1. **CLI Framework** (`internal/cli/query/`):
    - Create query command structure using cobra
    - Implement time range parsing (`--since`, `--from/--to`)
    - Build output formatters (table, JSON, CSV)
    - Add service name resolution and filtering

2. **eBPF Query Commands** (`internal/cli/query/ebpf/`):
    - `coral query ebpf http <service>` - HTTP RED metrics
    - `coral query ebpf grpc <service>` - gRPC RED metrics
    - `coral query ebpf sql <service>` - SQL query metrics
    - `coral query ebpf traces --trace-id <id>` - Trace lookup
    - Percentile calculations from histogram buckets
    - Error rate calculations and filtering

3. **OTLP Query Commands** (`internal/cli/query/telemetry/`):
    - `coral query telemetry spans <service>` - OTLP span lookup
    - Integration with RFD 025 QueryTelemetry RPC
    - Span tree visualization

4. **Output Formatting** (`internal/cli/format/`):
    - Table formatter with column alignment
    - JSON formatter for API consumption
    - CSV formatter for spreadsheet import
    - Histogram visualization (ASCII bar charts)

**Configuration Example:**

```bash
# Query HTTP metrics for a service
coral query ebpf http payments-api --since 1h

# Query with advanced filters
coral query ebpf http payments-api \
  --since 1h \
  --route "/api/v1/payments" \
  --status 5xx \
  --output json

# Query traces
coral query ebpf traces --trace-id abc123def456 --format tree

# Query multiple colonies
coral query ebpf http payments-api --colony prod-us,prod-eu --since 30m
```

## Implementation Plan

### Phase 1: Core Query Framework ✅

- [x] Create `internal/cli/query/` package structure
- [x] Implement time range parser (`ParseTimeRange("1h")` → start/end
  timestamps) - `internal/cli/helpers/time.go`
- [x] Create output formatter interface (`Formatter.Format(data) → string`)
- [x] Implement table formatter with column alignment - `internal/cli/helpers/formatter.go`
- [x] Add JSON and CSV formatters - `internal/cli/helpers/formatter.go`
- [x] Create colony client wrapper for gRPC queries - `internal/cli/helpers/agent_client.go`

### Phase 2: eBPF HTTP Metrics ✅

- [x] Implement `coral query ebpf http <service>` command - `internal/cli/query/ebpf/http.go`
- [x] Parse histogram buckets into percentiles (P50, P95, P99)
- [x] Calculate error rates from status code distributions
- [x] Add route filtering (`--route <pattern>`)
- [x] Add status code filtering (`--status 2xx|4xx|5xx`) - placeholder flag
- [x] Format output as table with columns: Route, Requests, P50, P95, P99,
  Errors

### Phase 3: eBPF gRPC & SQL Metrics ✅

- [x] Implement `coral query ebpf grpc <service>` command - `internal/cli/query/ebpf/grpc.go`
- [x] Implement `coral query ebpf sql <service>` command - `internal/cli/query/ebpf/sql.go`
- [x] Add method/operation filtering
- [x] Reuse percentile and formatting logic from HTTP

### Phase 4: Trace Queries

- [ ] Implement `coral query ebpf traces --trace-id <id>` command
- [ ] Fetch trace spans from Colony via gRPC
- [ ] Build span tree from parent-child relationships
- [ ] Implement tree visualization formatter
- [ ] Add span filtering by service, operation, duration

### Phase 5: Advanced Features

- [ ] Multi-colony query and result merging
- [ ] Comparison mode (`--compare <time-range>`)
- [ ] ASCII histogram visualization
- [ ] Saved queries and aliases
- [ ] Integration with `coral tap` for live queries

## Testing Strategy

**Unit Tests:**

- Time range parser correctness (`1h`, `30m`, ISO timestamps)
- Percentile calculations from histogram buckets
- Output formatter correctness (table alignment, JSON schema)
- Service name filtering and pattern matching

**Integration Tests:**

- End-to-end query: `coral query ebpf http test-service --since 5m`
- Verify output format matches expected schema
- Test error handling (service not found, no data in time range)
- Multi-colony query merging correctness

**Manual Testing:**

- Query real Beyla data from running services
- Verify CLI usability with production-scale datasets
- Test edge cases (empty results, very large result sets)

## Future Work

- Integration with `coral ask` (RFD 030) for AI-driven queries
- Live streaming queries (`coral query ebpf http <service> --follow`)
- Query result caching for faster re-queries
- Export to external systems (Prometheus, Grafana Cloud)
- Query templates and saved queries

## Dependencies

- **RFD 025**: OTLP ingestion and QueryTelemetry RPC
- **RFD 032**: Beyla integration and QueryBeylaMetrics RPC
- Colony storage implementation (Phase 4 of RFD 032)

## References

- `kubectl` CLI patterns for inspiration
- `aws cloudwatch` query commands
- Prometheus `promtool query` interface
