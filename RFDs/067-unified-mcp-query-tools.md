---
rfd: "067"
title: "Unified MCP Query Tools for LLM Efficiency"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "035", "032", "025", "004" ]
database_migrations: [ ]
areas: [ "mcp", "observability", "llm", "query" ]
---

# RFD 067 - Unified MCP Query Tools for LLM Efficiency

**Status:** 🚧 Draft

## Summary

Introduce unified MCP query tools that abstract data sources (eBPF vs OTLP) to
optimize LLM efficiency during root cause analysis. Following a "plumbing vs
porcelain" architecture (similar to git), protocol-specific tools remain
available for advanced users while new unified tools (`coral_query_metrics`,
`coral_query_traces`) provide simplified interfaces for AI-driven investigation.
Additionally, standardize naming from "beyla" to "ebpf" to better represent
capabilities rather than implementation details.

## Problem

**Current behavior/limitations:**

- **Tool proliferation**: 6+ separate MCP query tools exist (HTTP/gRPC/SQL
  metrics × eBPF/OTLP sources), plus trace-specific tools
- **LLM cognitive overhead**: AI assistants must understand data source
  differences (eBPF vs OTLP) and choose the appropriate tool for each query
- **Suboptimal RCA workflow**: During root cause analysis, LLMs query eBPF
  metrics separately from OTLP metrics, missing the unified view operators need
- **Naming confusion**: Tools use "beyla" (the Grafana implementation) rather
  than "ebpf" (the capability), leaking implementation details into the API
- **Incomplete coverage**: Querying only eBPF misses instrumented service
  details; querying only OTLP misses uninstrumented service data

**Why this matters:**

- **Investigation efficiency**: LLMs spend extra tokens and time selecting tools
  and correlating results from multiple sources
- **User experience**: Users asking "show me HTTP metrics" shouldn't need to
  specify data source
- **Maintainability**: Adding new data sources (e.g., service mesh sidecars)
  requires updating LLM prompts to select yet another tool
- **Accuracy**: Unified views prevent incomplete analysis when data exists in
  multiple sources

**Use cases affected:**

- "Why is the API slow?" → LLM must query eBPF metrics, then OTLP metrics, then
  correlate
- "Find slow traces" → LLM must choose between eBPF traces and OTLP spans,
  potentially missing data
- "Compare HTTP latency before/after deploy" → Must query both sources
  independently
- "Debug uninstrumented service" → Must remember to use eBPF-specific tools

## Solution

Create a two-tier MCP tool architecture inspired by git's plumbing vs porcelain
design:

**Plumbing tools** (protocol-specific, source-specific):

- Low-level, precise tools for advanced users
- Examples: `coral_query_ebpf_http_metrics`, `coral_query_telemetry_spans`
- Remain available but deprioritized for LLM consumption

**Porcelain tools** (unified, source-agnostic):

- High-level, LLM-optimized tools for common workflows
- Examples: `coral_query_metrics`, `coral_query_traces`
- Automatically query both eBPF and OTLP, merge results, annotate sources

**Key Design Decisions:**

1. **Unified tool design**: Single `coral_query_metrics` tool accepts union of
   all protocol parameters (HTTP, gRPC, SQL), routes to appropriate backends
2. **Automatic source merging**: Query both eBPF and OTLP in parallel without
   exposing `source` parameter to LLMs
3. **Source annotation**: Results clearly indicate data origin (`[eBPF]`,
   `[OTLP]`) for transparency
4. **Protocol auto-detection**: `protocol: "auto"` returns all available
   metrics; specific protocol values filter accordingly
5. **Naming standardization**: Rename "beyla" → "ebpf" to abstract
   implementation details and improve clarity

**Benefits:**

- **Reduced LLM token usage**: Single tool call instead of multiple
  source-specific calls
- **Improved analysis accuracy**: Unified view prevents missing data from either
  source
- **Simpler mental model**: LLMs think about protocols (HTTP, gRPC) not data
  sources (eBPF, OTLP)
- **Future-proof**: Adding new data sources doesn't change LLM-facing API
- **Better naming**: "ebpf" describes what it does, "beyla" describes how it's
  implemented

**Architecture Overview:**

```
LLM makes single query:
  coral_query_metrics(service="api", protocol="http", time_range="1h")
          ↓
    Unified Handler
          ↓
    ┌─────────┴─────────┐
    ↓                   ↓
eBPF Backend      OTLP Backend
(plumbing tool)   (plumbing tool)
    ↓                   ↓
QueryEBPFHTTP()   QueryTelemetryMetrics()
    ↓                   ↓
    └─────────┬─────────┘
              ↓
        Merge Results
    (union by route, annotate)
              ↓
      Format & Return
   "HTTP Metrics [eBPF]: ...
    HTTP Metrics [OTLP]: ..."
```

### Component Changes

#### 1. Naming Standardization (Phase 1)

**MCP Tools** (`internal/colony/mcp/tools_observability.go`):

- Rename all "beyla" tool names to "ebpf" to reflect capability rather than implementation
- Update tool descriptions to reference "eBPF-based metrics" instead of "Beyla metrics"
- Mark renamed tools as "Advanced" and recommend unified tools for most use cases

**Type Definitions** (`internal/colony/mcp/types.go`):

- Rename input structs from `Beyla*Input` to `EBPF*Input` for consistency
- Update struct documentation to use "eBPF" terminology

**Tool Registration** (`internal/colony/mcp/server.go`):

- Update tool registration to use new naming convention
- Add tool categorization (plumbing vs porcelain) in descriptions

#### 2. Unified Metrics Tool (Phase 2)

**New Tool: `coral_query_metrics`**

A unified metrics query tool that:

- Accepts union of all protocol parameters (HTTP, gRPC, SQL)
- Automatically queries both eBPF and OTLP sources in parallel
- Merges results by route/method and annotates with data source
- Supports protocol auto-detection or specific protocol filtering

**Key capabilities:**

- Protocol routing: Routes queries to appropriate backends based on protocol parameter
- Source merging: Combines eBPF and OTLP results into unified view
- Source annotation: Clearly marks data origin ([eBPF], [OTLP]) for transparency
- Graceful degradation: Works with single source if other is unavailable

#### 3. Unified Traces Tool (Phase 3)

**New Tool: `coral_query_traces`**

A unified trace query tool that:

- Queries both eBPF traces and OTLP spans
- Builds unified span tree by merging trace_id and span_id
- Deduplicates spans (prefers OTLP for richer attributes)
- Annotates each span with its data source

**Key capabilities:**

- Parallel querying: Fetches from both sources simultaneously
- Span tree merging: Combines spans into coherent trace hierarchy
- Smart deduplication: Keeps OTLP spans when available, falls back to eBPF
- Source transparency: Annotates each span with [eBPF] or [OTLP] tag

### Configuration Example

MCP tool usage via `coral ask`:

```bash
# Unified metrics query (recommended for LLMs)
coral ask "What's the HTTP error rate for the API service?"
# → Uses coral_query_metrics internally
# → Returns merged eBPF + OTLP results

# Unified trace query (recommended for LLMs)
coral ask "Show me slow traces for checkout"
# → Uses coral_query_traces internally
# → Returns merged eBPF + OTLP span tree

# Advanced: Protocol-specific query (power users)
# LLM can still use plumbing tools if explicitly needed
coral ask "Show me only eBPF HTTP metrics for API"
# → Uses coral_query_ebpf_http_metrics directly
```

CLI command mapping (from RFD 035):

```bash
# CLI remains protocol-specific
coral query ebpf http api --since 1h
coral query telemetry spans api --since 1h

# MCP tools provide unified layer
# (CLI may adopt unified commands in future RFD)
```

## Implementation Plan

### Phase 1: Rename Beyla → eBPF

- [ ] Rename tool registration functions and tool names
- [ ] Rename input structs in type definitions
- [ ] Update tool descriptions to reference "eBPF" instead of "Beyla"
- [ ] Update server registration calls
- [ ] Run `make test` to verify no breakage

### Phase 2: Implement Unified Metrics Tool

- [ ] Create unified metrics input struct with union schema
- [ ] Implement unified metrics tool registration
- [ ] Add protocol routing logic (auto, http, grpc, sql)
- [ ] Implement parallel querying of eBPF and OTLP sources
- [ ] Implement result merging logic (union by route/method)
- [ ] Add source annotations to output format
- [ ] Write unit tests for protocol routing and result merging
- [ ] Add integration test for merged output

### Phase 3: Implement Unified Traces Tool

- [ ] Create unified traces input struct
- [ ] Implement unified traces tool registration
- [ ] Implement parallel querying of eBPF and OTLP sources
- [ ] Implement span tree merging by trace_id
- [ ] Implement span deduplication logic (prefer OTLP)
- [ ] Add source annotations to span output
- [ ] Write unit tests for span tree building and deduplication
- [ ] Add integration test for merged span trees

### Phase 4: Tool Prioritization & Documentation

- [ ] Update unified tool descriptions: Mark as "Recommended"
- [ ] Update plumbing tool descriptions: Mark as "Advanced"
- [ ] Add usage examples to tool descriptions
- [ ] Update RFD 035 to use "ebpf" terminology throughout
- [ ] Update `docs/CLI_MCP_MAPPING.md` with ebpf tool names
- [ ] Add unified tools section to CLI_MCP_MAPPING
- [ ] Document migration path for old "beyla" names

### Phase 5: Testing & Validation

- [ ] Manual test: Query metrics with protocol="auto"
- [ ] Manual test: Query metrics with protocol="http"
- [ ] Manual test: Query traces from mixed sources
- [ ] Verify source annotations appear correctly
- [ ] Test with missing eBPF data (OTLP only)
- [ ] Test with missing OTLP data (eBPF only)
- [ ] Validate schema generation for union input
- [ ] Run full integration test suite


## API Changes

### New MCP Tools

#### `coral_query_metrics`

**Description:** Recommended tool for querying service metrics from both eBPF
and OTLP sources. Returns unified view with source annotations.

**Input Parameters:**

```json
{
    "service": "api",
    "time_range": "1h",
    "protocol": "auto",
    "http_route": "/api/payments",
    "http_method": "GET",
    "status_code_range": "5xx"
}
```

**Example Output:**

```
Metrics for service 'api' (last 1h):

HTTP Metrics [eBPF]:
Route: /api/payments (GET), Requests: 1,234, P50: 23ms, P95: 45ms, P99: 89ms, Errors: 2.1%
Route: /api/checkout (POST), Requests: 567, P50: 34ms, P95: 67ms, P99: 120ms, Errors: 0.5%

HTTP Metrics [OTLP]:
Route: /api/payments (GET), Requests: 1,200, P50: 24ms, P95: 46ms, P99: 90ms, Errors: 2.0%
(Note: OTLP data may differ from eBPF due to sampling)

Analysis: 2.1% error rate detected on /api/payments endpoint.
```

#### `coral_query_traces`

**Description:** Recommended tool for querying distributed traces from both eBPF
and OTLP sources. Returns unified span tree.

**Input Parameters:**

```json
{
    "service": "api",
    "time_range": "1h",
    "min_duration_ms": 500,
    "max_traces": 10
}
```

**Example Output:**

```
Slow Traces for service 'api' (>500ms):

Trace: abc123def456 (Duration: 1,234ms, Start: 2025-12-02T10:30:15Z)
├─ [OTLP] api-gateway: GET /api/payments (1,234ms)
│  ├─ [eBPF] payment-service: ProcessPayment (800ms)
│  │  ├─ [OTLP] fraud-service: CheckFraud (300ms)
│  │  └─ [eBPF] database: SELECT payments (450ms) ← SLOW
│  └─ [OTLP] notification-service: SendEmail (200ms)

Trace: def789ghi012 (Duration: 890ms, Start: 2025-12-02T10:31:22Z)
├─ [eBPF] api-gateway: POST /api/checkout (890ms)
   └─ [eBPF] payment-service: Charge (850ms) ← SLOW
      └─ [eBPF] external-api: ChargeCard (820ms)

Analysis: Database query and external API calls are primary bottlenecks.
```

### Renamed MCP Tools

| Old Name                         | New Name                        | Status             |
|----------------------------------|---------------------------------|--------------------|
| `coral_query_beyla_http_metrics` | `coral_query_ebpf_http_metrics` | Renamed (plumbing) |
| `coral_query_beyla_grpc_metrics` | `coral_query_ebpf_grpc_metrics` | Renamed (plumbing) |
| `coral_query_beyla_sql_metrics`  | `coral_query_ebpf_sql_metrics`  | Renamed (plumbing) |
| `coral_query_beyla_traces`       | `coral_query_ebpf_traces`       | Renamed (plumbing) |

**Migration:** Old tool names deprecated but remain functional for 3 months,
then removed.

## Testing Strategy

### Unit Tests

**Protocol routing tests** (`tools_unified_test.go`):

- Parse `protocol: "auto"` → queries all protocols
- Parse `protocol: "http"` → queries only HTTP
- Parse `protocol: "grpc"` → queries only gRPC
- Parse `protocol: "sql"` → queries only SQL
- Invalid protocol value → returns error

**Result merging tests**:

- Merge eBPF + OTLP metrics with same routes → union both
- Merge eBPF-only metrics → includes eBPF data only
- Merge OTLP-only metrics → includes OTLP data only
- Merge empty results → returns "No data found"

**Span tree merging tests**:

- Merge spans with same trace_id → unified tree
- Deduplicate spans with same span_id → prefer OTLP
- Merge disjoint span sets → all spans included
- Annotate spans correctly → [eBPF] or [OTLP] labels

### Integration Tests

**End-to-end unified metrics query**:

1. Insert eBPF HTTP metrics into DuckDB
2. Insert OTLP HTTP metrics into DuckDB
3. Call `coral_query_metrics(service="test", protocol="http")`
4. Verify output contains both eBPF and OTLP sections
5. Verify source annotations correct

**End-to-end unified traces query**:

1. Insert eBPF trace spans into DuckDB
2. Insert OTLP trace spans into DuckDB (overlapping trace_id)
3. Call `coral_query_traces(service="test")`
4. Verify unified span tree built correctly
5. Verify duplicates deduplicated (OTLP preferred)
6. Verify source annotations correct

**Missing source handling**:

- Query with eBPF data but no OTLP → succeeds, shows only eBPF
- Query with OTLP data but no eBPF → succeeds, shows only OTLP
- Query with no data in either → returns "No metrics found"

### Manual Testing

- Query real production service with both eBPF and OTLP data
- Verify merged view makes sense to human operator
- Test with `coral ask` natural language queries
- Verify LLM selects unified tools over plumbing tools
- Test edge cases: services with only eBPF, only OTLP

## Migration Strategy

Since the project is still experimental, we won't worry about breaking changes
and migration strategy. Tools will be renamed.

## Future Enhancements

### Phase 6: Advanced Merging Strategies (Future)

- **Correlation by tags**: Match eBPF and OTLP data by service.name,
  deployment.environment
- **Sampling awareness**: Adjust eBPF data when OTLP uses sampling
- **Anomaly detection**: Highlight discrepancies between eBPF and OTLP metrics
- **Performance optimization**: Cache merged results for repeated queries

### Phase 7: Unified CLI Commands (Future - separate RFD)

Extend CLI to support unified queries:

```bash
coral query metrics api --protocol http --since 1h
# → Queries both eBPF and OTLP, shows merged view

coral query traces api --min-duration 500ms
# → Queries both eBPF and OTLP, shows unified span tree
```

Currently CLI remains protocol-specific (`coral query ebpf`,
`coral query telemetry`) - unified commands require separate RFD to design CLI
UX.

### Phase 8: Additional Data Sources (Future)

Support for additional observability backends:

- Service mesh sidecars (Envoy, Istio)
- APM agents (Datadog, New Relic exporters)
- Custom exporters (Prometheus, StatsD)

Unified tools abstract source complexity, making integration seamless.

## Appendix

### Implementation Details

This section contains detailed implementation examples for reference during development.

#### Unified Metrics Input Struct

```go
// UnifiedMetricsInput is the input for coral_query_metrics.
// This tool queries both eBPF and OTLP sources automatically.
type UnifiedMetricsInput struct {
    Service         string  `json:"service" jsonschema:"description=Service name (required)"`
    TimeRange       *string `json:"time_range,omitempty" jsonschema:"description=Time range (e.g. '1h' '30m' '24h'),default=1h"`
    Protocol        *string `json:"protocol,omitempty" jsonschema:"description=Protocol to query (auto queries all),enum=auto,enum=http,enum=grpc,enum=sql,default=auto"`

    // HTTP-specific parameters
    HTTPRoute       *string `json:"http_route,omitempty" jsonschema:"description=Optional: Filter by HTTP route pattern (e.g. '/api/v1/users/:id')"`
    HTTPMethod      *string `json:"http_method,omitempty" jsonschema:"description=Optional: Filter by HTTP method,enum=GET,enum=POST,enum=PUT,enum=DELETE,enum=PATCH"`
    StatusCodeRange *string `json:"status_code_range,omitempty" jsonschema:"description=Optional: Filter by status code range,enum=2xx,enum=3xx,enum=4xx,enum=5xx"`

    // gRPC-specific parameters
    GRPCMethod      *string `json:"grpc_method,omitempty" jsonschema:"description=Optional: Filter by gRPC method (e.g. '/payments.PaymentService/Charge')"`
    GRPCStatusCode  *int    `json:"grpc_status_code,omitempty" jsonschema:"description=Optional: Filter by gRPC status code (0=OK 1=CANCELLED etc.)"`

    // SQL-specific parameters
    SQLOperation    *string `json:"sql_operation,omitempty" jsonschema:"description=Optional: Filter by SQL operation,enum=SELECT,enum=INSERT,enum=UPDATE,enum=DELETE"`
    TableName       *string `json:"table_name,omitempty" jsonschema:"description=Optional: Filter by table name"`
}
```

#### Unified Traces Input Struct

```go
// UnifiedTracesInput is the input for coral_query_traces.
// This tool queries both eBPF and OTLP sources and builds a unified span tree.
type UnifiedTracesInput struct {
    TraceID       *string `json:"trace_id,omitempty" jsonschema:"description=Specific trace ID (32-char hex string)"`
    Service       *string `json:"service,omitempty" jsonschema:"description=Filter traces involving this service"`
    TimeRange     *string `json:"time_range,omitempty" jsonschema:"description=Time range (e.g. '1h' '30m' '24h'),default=1h"`
    MinDurationMs *int    `json:"min_duration_ms,omitempty" jsonschema:"description=Optional: Only return traces longer than this duration (milliseconds)"`
    MaxTraces     *int    `json:"max_traces,omitempty" jsonschema:"description=Maximum number of traces to return,default=10"`
}
```

#### Unified Metrics Tool Registration

```go
func (s *Server) registerUnifiedMetricsTool() {
    if !s.isToolEnabled("coral_query_metrics") {
        return
    }

    tool := mcp.NewToolWithRawSchema(
        "coral_query_metrics",
        "Recommended: Query service metrics (HTTP/gRPC/SQL) from both eBPF and OTLP sources. Returns unified view with source annotations. Use this for efficient root cause analysis.",
        schemaBytes,
    )

    s.mcpServer.AddTool(tool, func (ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        // 1. Parse input
        // 2. Determine protocol(s) to query
        // 3. Query eBPF backend (call existing plumbing tools)
        // 4. Query OTLP backend (call existing plumbing tools)
        // 5. Merge results by protocol/route
        // 6. Format with source annotations
        // 7. Return unified result
    })
}
```

#### Unified Traces Tool Registration

```go
func (s *Server) registerUnifiedTracesTool() {
    if !s.isToolEnabled("coral_query_traces") {
        return
    }

    tool := mcp.NewToolWithRawSchema(
        "coral_query_traces",
        "Recommended: Query distributed traces from both eBPF and OTLP sources. Returns unified span tree with source annotations. Use this for distributed tracing analysis.",
        schemaBytes,
    )

    s.mcpServer.AddTool(tool, func (ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        // 1. Parse input
        // 2. Query eBPF traces (call existing plumbing tool)
        // 3. Query OTLP spans (call existing plumbing tool)
        // 4. Merge span trees by trace_id/span_id
        // 5. Deduplicate spans (prefer OTLP, keep eBPF if no OTLP)
        // 6. Format with [eBPF] or [OTLP] annotations
        // 7. Return unified span tree
    })
}
```

### Plumbing vs Porcelain Philosophy

**Inspiration: Git's two-tier design**

Git separates low-level plumbing commands (`git hash-object`, `git write-tree`)
from high-level porcelain commands (`git commit`, `git push`). Power users can
access plumbing for precise control, while most users benefit from porcelain's
simplicity.

**Application to Coral MCP tools:**

- **Plumbing**: Protocol-specific, source-specific tools for precise queries
    - Use case: "Give me exact eBPF HTTP metrics without OTLP correlation"
    - Users: Advanced operators, debugging tool developers

- **Porcelain**: Unified tools optimized for common workflows
    - Use case: "Show me all HTTP metrics for this service"
    - Users: LLMs, operators during incident response

**Benefits:**

- **Flexibility**: Experts aren't constrained by high-level abstractions
- **Simplicity**: LLMs and casual users get straightforward interfaces
- **Maintainability**: Porcelain layer can evolve without breaking plumbing
- **Extensibility**: New data sources integrate at plumbing layer, porcelain
  automatically benefits

### eBPF vs Beyla Naming Rationale

**Why rename?**

1. **Implementation detail leak**: "Beyla" is the specific Grafana project used
   internally
2. **Capability description**: "eBPF" describes what the tool provides (
   kernel-level observability)
3. **Future-proofing**: If Coral replaces Beyla with custom eBPF, tool names
   remain accurate
4. **Industry standard**: "eBPF metrics" is widely understood; "Beyla metrics"
   requires explanation

**Precedent:**

- Kubernetes: Exposes "container" API, not "Docker" or "containerd"
- Prometheus: Exposes "metrics" API, not "OpenMetrics" or "Prometheus format"
- OpenTelemetry: Exposes "traces", not "Jaeger format" or "Zipkin spans"
