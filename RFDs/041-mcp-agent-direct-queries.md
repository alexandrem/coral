---
rfd: "041"
title: "MCP Agent Direct Queries for Detailed Telemetry"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "004", "025" ]
database_migrations: [ ]
areas: [ "mcp", "telemetry", "agents" ]
---

# RFD 041 - MCP Agent Direct Queries for Detailed Telemetry

**Status:** 🚧 Draft

## Summary

This RFD proposes enhancing MCP server tools to query agent databases directly
for detailed, raw telemetry data (OTLP spans, metrics, logs) instead of relying
solely on colony's aggregated summaries. This enables deep-dive analysis, full
trace reconstruction, and access to all telemetry attributes.

## Problem

**Current behavior/limitations:**

Current implementation (RFD 004 + RFD 025) provides:

- Colony stores aggregated telemetry summaries (P50/P95/P99, error counts,
  sample traces)
- Summaries are space-efficient and fast to query
- Agents store raw telemetry locally (~1 hour retention) in agent DuckDB

**Limitations:**

1. **Loss of detail:** Cannot see individual spans, only percentiles
2. **No full traces:** Can't reconstruct complete distributed traces
3. **Limited attributes:** Aggregation discards most span attributes
4. **No log access:** Logs aren't aggregated, only stored on agents
5. **Coarse time buckets:** Summaries use 1-minute buckets, losing sub-minute
   resolution

**Why this matters:**

AI assistants need access to detailed telemetry for effective debugging and
analysis. Current summaries cannot answer queries like "Show me the slowest
requests in the last 5 minutes" or "Debug this specific slow request" because
they only provide aggregated statistics.

**Use cases affected:**

- "Show me the slowest requests in the last 5 minutes" (need individual spans)
- "Find all requests that touched service X and Y" (need full trace
  reconstruction)
- "Show logs for trace abc123" (logs only on agents)
- "What attributes were set on this span?" (attributes lost in aggregation)
- "Debug this specific slow request" (need exact span data, not averages)

## Solution

Enable MCP tools to query agent databases directly for raw telemetry while
maintaining backward compatibility with existing summary-based queries.

**Key Design Decisions:**

- **Hybrid approach:** Use colony summaries for common queries, agent queries
  for deep dives
- **Automatic agent selection:** Colony orchestrates queries based on service
  name → agent mapping
- **Federated queries:** Support querying multiple agents to reconstruct
  distributed traces
- **Pull-based model:** MCP tools pull data on-demand rather than streaming
- **Agent-side retention:** Leverage existing 1-hour local storage on agents

**Why this approach:**

- Maintains RFD 025 architecture (agents store raw data, colony stores
  summaries)
- Avoids 100x storage explosion in colony from storing all raw telemetry
- Provides best of both worlds: fast summaries + detailed data when needed
- Agents already have the data with proper indexes

**Benefits:**

- AI assistants can perform deep-dive analysis on individual requests
- Full distributed trace reconstruction across all services
- Access to all span attributes and logs for debugging
- No additional storage costs (uses existing agent databases)
- Backward compatible with existing summary-based queries

**Architecture Overview:**

```
┌─────────────────────────────────────────────────────────────────┐
│                        Claude Desktop / MCP Client               │
└─────────────────────────────────────────────────────────────────┘
                                   │
                                   │ MCP Protocol
                                   ▼
┌─────────────────────────────────────────────────────────────────┐
│                          Colony MCP Server                       │
│                                                                  │
│  ┌──────────────────────┐     ┌─────────────────────────────┐  │
│  │  Tool: Query Spans   │────▶│  Agent Query Orchestrator   │  │
│  └──────────────────────┘     └─────────────────────────────┘  │
│                                        │                         │
│                                        │ gRPC Agent API          │
└────────────────────────────────────────┼─────────────────────────┘
                                         │
                    ┌────────────────────┼────────────────────┐
                    │                    │                    │
                    ▼                    ▼                    ▼
            ┌───────────────┐    ┌───────────────┐    ┌───────────────┐
            │   Agent 1     │    │   Agent 2     │    │   Agent 3     │
            │               │    │               │    │               │
            │  ┌─────────┐  │    │  ┌─────────┐  │    │  ┌─────────┐  │
            │  │ DuckDB  │  │    │  │ DuckDB  │  │    │  │ DuckDB  │  │
            │  │ (spans) │  │    │  │ (spans) │  │    │  │ (metrics)│  │
            │  └─────────┘  │    │  └─────────┘  │    │  └─────────┘  │
            └───────────────┘    └───────────────┘    └───────────────┘
```

### Component Changes

1. **Colony MCP Server** (`internal/colony/mcp/`):
    - Add new MCP tools: `coral_query_raw_spans`, `coral_query_raw_logs`,
      `coral_reconstruct_trace`
    - Add `mode` parameter to existing `coral_query_telemetry_spans` tool
    - Implement Agent Query Orchestrator for coordinating multi-agent queries

2. **Agent gRPC API** (`internal/agent/grpc/`):
    - Implement `TelemetryQuery` service with QuerySpans, QueryMetrics,
      QueryLogs RPCs
    - Add DuckDB query handlers with filtering support
    - Implement pagination and result limiting

3. **Colony Registry** (`internal/colony/registry/`):
    - Add service → agent mapping for intelligent query routing
    - Track which agents host which services

**Agent Query Orchestrator:**

Located in `internal/colony/mcp/agent_query_orchestrator.go`, responsible for:

- Query single agent for raw spans/logs via gRPC
- Query multiple agents in parallel for federated queries
- Aggregate and sort results from multiple agents
- Reconstruct complete distributed traces by querying all agents
- Handle agent failures gracefully (continue with available agents)

### Performance & Caching

**Challenge:** Querying agent databases for every MCP request could be slow.

**Mitigation Strategies:**

1. **Short default time ranges:** Default to `5m` instead of `1h`
2. **Result limits:** Cap at 1000 spans per query
3. **Intelligent routing:** Only query agents that host the service
4. **Parallel queries:** Query multiple agents concurrently
5. **Colony-side caching:** Cache frequent queries for 30s
6. **Agent-side indexing:** Agents have indexes on trace_id, service_name,
   timestamp

**Expected Performance:**

- Single agent query: 10-50ms
- Multi-agent federated query: 50-200ms
- Full trace reconstruction (5 services): 100-300ms

Still acceptable for interactive AI assistant usage.

## API Changes

### New Protobuf Messages

```protobuf
// coral/agent/v1/telemetry_query.proto

service TelemetryQuery {
    // Query raw OTLP spans from agent's local DuckDB.
    rpc QuerySpans(QuerySpansRequest) returns (QuerySpansResponse);

    // Query raw OTLP metrics from agent's local DuckDB.
    rpc QueryMetrics(QueryMetricsRequest) returns (QueryMetricsResponse);

    // Query raw OTLP logs from agent's local DuckDB.
    rpc QueryLogs(QueryLogsRequest) returns (QueryLogsResponse);

    // Check data availability (time range, service names, data size).
    rpc GetDataInfo(GetDataInfoRequest) returns (GetDataInfoResponse);
}

message QuerySpansRequest {
    string service_name = 1;
    google.protobuf.Timestamp start_time = 2;
    google.protobuf.Timestamp end_time = 3;

    // Optional filters.
    optional string trace_id = 4;
    optional string operation_name = 5;
    optional int64 min_duration_ms = 6;
    optional bool errors_only = 7;

    // Pagination.
    int32 limit = 8;  // Max spans to return (default 1000).
    int32 offset = 9;
}

message QuerySpansResponse {
    repeated Span spans = 1;
    int32 total_count = 2;  // Total spans matching query (for pagination).
    bool has_more = 3;
}

message Span {
    string trace_id = 1;
    string span_id = 2;
    string parent_span_id = 3;
    string service_name = 4;
    string operation_name = 5;
    google.protobuf.Timestamp start_time = 6;
    int64 duration_us = 7;
    bool is_error = 8;
    map<string, string> attributes = 9;
    repeated Event events = 10;
}
```

### MCP Tools

**New Tools:**

```
coral_query_raw_spans
  Description: Query raw OTLP spans from agent databases for detailed analysis
  Inputs:
    - service_name (required): Service to query
    - time_range (default: "5m"): Recent time window
    - trace_id (optional): Specific trace ID
    - operation_name (optional): Filter by operation
    - min_duration_ms (optional): Only slow requests
    - errors_only (optional): Only failed requests
  Returns: List of individual spans with full details

coral_query_raw_logs
  Description: Query raw OTLP logs from agent databases
  Inputs:
    - service_name (required)
    - time_range (default: "5m")
    - trace_id (optional): Logs for specific trace
    - level (optional): ERROR, WARN, INFO, DEBUG
    - query (optional): Full-text search
  Returns: List of log entries with timestamps and attributes

coral_reconstruct_trace
  Description: Fetch and reconstruct a complete distributed trace across all agents
  Inputs:
    - trace_id (required): Trace ID to reconstruct
  Returns: Full trace tree with all spans from all services
```

**Modified Tools:**

```
coral_query_telemetry_spans
  - Add mode parameter: "summary" (default, use colony) or "detailed" (query agents)
  - "summary" mode: Fast, uses existing QueryTelemetrySummaries()
  - "detailed" mode: Slow, queries agents directly for raw spans
```

## Implementation Plan

### Phase 1: Agent gRPC API

- [ ] Define protobuf for `TelemetryQuery` service
- [ ] Implement `QuerySpans()` in agent
- [ ] Implement `QueryLogs()` in agent
- [ ] Add DuckDB queries for filtering spans/logs
- [ ] Add integration tests

### Phase 2: Colony Orchestrator

- [ ] Implement `AgentQueryOrchestrator`
- [ ] Add service → agent mapping in registry
- [ ] Implement parallel agent queries
- [ ] Add error handling and timeouts
- [ ] Add unit tests

### Phase 3: MCP Tools

- [ ] Implement `coral_query_raw_spans`
- [ ] Implement `coral_query_raw_logs`
- [ ] Implement `coral_reconstruct_trace`
- [ ] Add `mode` parameter to existing telemetry tools
- [ ] Update documentation

### Phase 4: Optimization & Testing

- [ ] Add colony-side caching
- [ ] Optimize DuckDB queries with better indexes
- [ ] Add E2E tests with multiple agents
- [ ] Performance testing and tuning
- [ ] Write user guide

## Security Considerations

1. **Agent Authorization:** Colony must authenticate to agents using mTLS
2. **Query Limits:** Prevent queries that could overload agents
3. **Data Isolation:** Agents should only return data for their own services
4. **Sensitive Attributes:** Filter out PII from span attributes (configurable)

## Testing Strategy

### Unit Tests

- Orchestrator query logic and result aggregation
- Agent selection and service mapping
- Error handling for unavailable agents

### Integration Tests

- Colony → agent gRPC calls
- Multi-agent parallel queries
- Query filtering and pagination

### E2E Tests

- MCP tools with multi-agent setup
- Full trace reconstruction across services
- Fallback to summaries when agents unavailable

### Performance Tests

- Query latency with 10+ agents
- Large result set handling
- Cache effectiveness

## Future Enhancements

- **Smart caching:** Learn which queries are frequent, pre-cache results
- **Query push-down:** Push filters to agents for better performance
- **Real-time alerts:** Trigger on specific span patterns across agents
- **Cross-region queries:** Federate queries across multiple colonies

---

## Implementation Status

**Core Capability:** ⏳ Not Started

This RFD is in draft state. No implementation work has begun.

**Next Steps:**

- Review and approve RFD
- Begin Phase 1: Agent gRPC API implementation

## References

- RFD 004: MCP Server Integration (summary-based queries)
- RFD 025: Basic OTLP Ingestion (agent local storage)
- RFD 032: Beyla RED Metrics Integration (Beyla data flow)
- RFD 036: Beyla Distributed Tracing (trace storage)
