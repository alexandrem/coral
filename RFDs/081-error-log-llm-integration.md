---
rfd: "081"
title: "Error Log LLM Integration"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "067", "074", "079" ]
database_migrations: [ ]
areas: [ "colony", "observability", "logging", "ai", "mcp" ]
---

# RFD 081 - Error Log LLM Integration

**Status:** 🚧 Draft

## Summary

Integrate error log querying capabilities into Coral's LLM-driven diagnostic
workflows via MCP tools. This enables Claude to automatically query error logs,
analyze error patterns, and provide root cause analysis during incident
response, building on the error log aggregation foundation from RFD 079.

Key capabilities:

- **MCP Tools**: `coral_query_logs` and `coral_query_error_patterns` for LLM access
- **Summary Integration**: Extend `coral_query_summary` with recent error context
- **Diagnostic Workflows**: LLM can correlate errors with metrics and profiling data
- **Pattern Analysis**: AI-powered identification of common failure modes

## Problem

### Current Gaps

With RFD 079 implemented, Coral can collect and query error logs via CLI, but
**LLMs cannot access this data**:

- ❌ Claude cannot query error logs during incident diagnosis
- ❌ No automatic correlation between errors and performance issues
- ❌ Operators must manually run CLI commands and paste results
- ❌ Error context missing from unified service health summaries

**Example Problem:**

```
User: "Why is payment-svc slow?"

Current LLM workflow (with RFD 079 only):
1. Query metrics: p99 latency is 2.5s (elevated)
2. Query profiling: CPU usage normal
3. Missing: What errors are being logged?

Gap: LLM cannot access error logs, operator must run:
  $ coral query patterns --service payment-svc --since 1h
  Then paste results back to LLM
```

### What's Needed

Enable LLMs to:

1. **Query error logs** directly during diagnosis
2. **Analyze error patterns** to identify common failures
3. **Correlate errors** with metrics and profiling data
4. **Provide context** in unified service summaries

## Solution

### Architecture Overview

Extend Coral's MCP server (RFD 067) with error log query tools:

```
┌─────────────────────────────────────────────────────────────────┐
│ Claude Desktop (MCP Client)                                     │
│                                                                 │
│  User: "Why is payment-svc failing?"                            │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         │ MCP protocol
                         ↓
┌────────────────────────────────────────────────────────────────┐
│ Colony: MCP Server (RFD 067)                                   │
│                                                                │
│  NEW TOOLS:                                                    │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ coral_query_logs(service, level, since, limit)           │  │
│  │   → Returns individual error logs with attributes        │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ coral_query_error_patterns(service, since, sort_by)      │  │
│  │   → Returns aggregated error patterns with counts        │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                │
│  ENHANCED TOOL:                                                │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ coral_query_summary(service, since)                      │  │
│  │   → Now includes recent_errors section                   │  │
│  └──────────────────────────────────────────────────────────┘  │
└────────────────────────┬───────────────────────────────────────┘
                         │
                         │ Internal RPC (from RFD 079)
                         ↓
┌─────────────────────────────────────────────────────────────────┐
│ Colony Storage: error_logs, error_log_patterns                  │
└─────────────────────────────────────────────────────────────────┘
```

### Component Changes

#### 1. MCP Tools (New)

**Tool: `coral_query_logs`**

```json
{
    "name": "coral_query_logs",
    "description": "Query individual error and warning logs. Use this to: (1) See recent errors with full context and attributes, (2) Debug specific services, (3) Get detailed error messages. Only ERROR and WARN level logs are stored (INFO/DEBUG are not available).",
    "inputSchema": {
        "type": "object",
        "properties": {
            "service": {
                "type": "string",
                "description": "Service name (required)"
            },
            "level": {
                "type": "string",
                "enum": ["error", "warn", "both"],
                "default": "both",
                "description": "Log level to filter"
            },
            "since": {
                "type": "string",
                "default": "1h",
                "description": "Time range (e.g., 5m, 1h, 24h)"
            },
            "limit": {
                "type": "integer",
                "default": 20,
                "description": "Max logs to return (max: 100)"
            }
        },
        "required": ["service"]
    }
}
```

**Tool: `coral_query_error_patterns`**

```json
{
    "name": "coral_query_error_patterns",
    "description": "Query aggregated error patterns to identify most common errors. Use this to: (1) Find which errors occur most frequently, (2) Understand error trends, (3) Get a high-level view of service health. Patterns group identical errors together (e.g., 'Database timeout after ? seconds').",
    "inputSchema": {
        "type": "object",
        "properties": {
            "service": {
                "type": "string",
                "description": "Service name (required)"
            },
            "level": {
                "type": "string",
                "enum": ["error", "warn", "both"],
                "default": "error",
                "description": "Log level to filter"
            },
            "since": {
                "type": "string",
                "default": "1h",
                "description": "Filter patterns by last occurrence (e.g., 1h, 24h, 7d)"
            },
            "limit": {
                "type": "integer",
                "default": 10,
                "description": "Max patterns to return (max: 50)"
            },
            "sort_by": {
                "type": "string",
                "enum": ["count", "recent"],
                "default": "count",
                "description": "Sort by occurrence count or most recent"
            }
        },
        "required": ["service"]
    }
}
```

#### 2. Enhanced Summary Tool

**Extend `coral_query_summary` Response:**

```protobuf
message QueryUnifiedSummaryResponse {
    ServiceHealthSummary health = 1;
    ProfilingSummary profiling = 2;
    DeploymentContext deployment = 3;
    repeated RegressionIndicator regressions = 4;

    // NEW: Recent error context
    RecentErrorsSummary recent_errors = 5;
}

message RecentErrorsSummary {
    int32 error_count_last_5m = 1;
    int32 warn_count_last_5m = 2;
    repeated ErrorLogPattern top_error_patterns = 3;  // Top 3 patterns
    repeated ErrorLog recent_errors = 4;              // Last 5 errors
}
```

### LLM Diagnostic Workflow

**Example: AI-powered incident diagnosis**

```
User: "Why is payment-svc failing?"

LLM Step 1: Get comprehensive summary
Tool: coral_query_summary
Input: { "service": "payment-svc", "since": "5m" }
Output: {
  "health": {
    "error_rate": 0.052,
    "p99_latency_ms": 1200
  },
  "recent_errors": {
    "error_count_last_5m": 23,
    "top_error_patterns": [
      {
        "message_template": "Database connection timeout after ? seconds",
        "occurrence_count": 15,
        "last_seen": "2025-01-10T14:32:15Z"
      }
    ]
  }
}

LLM Step 2: Get detailed error patterns
Tool: coral_query_error_patterns
Input: { "service": "payment-svc", "level": "error", "since": "1h" }
Output: {
  "patterns": [
    {
      "message_template": "Database connection timeout after ? seconds",
      "occurrence_count": 15,
      "severity": "ERROR",
      "first_seen": "2025-01-10T14:00:00Z",
      "last_seen": "2025-01-10T14:32:15Z",
      "example_attributes": [
        {"pool_size": 10, "database": "postgres-primary"}
      ]
    }
  ]
}

LLM Step 3: Get sample error details
Tool: coral_query_logs
Input: { "service": "payment-svc", "level": "error", "since": "5m", "limit": 3 }
Output: {
  "logs": [
    {
      "timestamp": "2025-01-10T14:32:15Z",
      "severity": "ERROR",
      "message": "Database connection timeout after 5 seconds",
      "attributes": {
        "pool_size": 10,
        "database": "postgres-primary",
        "active_connections": 10
      }
    }
  ]
}

LLM Response:
"payment-svc is experiencing a 5.2% error rate primarily due to database
connection pool exhaustion. Analysis:

**Root Cause:** Database connection pool too small
- Connection pool size: 10
- All connections active when timeout occurs
- 15 timeout errors in the last hour

**Evidence:**
- Error pattern: 'Database connection timeout after ? seconds' (15 occurrences)
- Pool at capacity: active_connections = pool_size = 10
- P99 latency elevated to 1.2s (connections waiting)

**Recommendation:**
1. Increase database connection pool from 10 to 25-30
2. Investigate slow queries holding connections
3. Consider read replicas if high read load"
```

## Implementation Plan

### Phase 1: MCP Tool Implementation

**Goals:** Add error log tools to MCP server

- [ ] Implement `coral_query_logs` MCP tool in `internal/colony/mcp`
- [ ] Implement `coral_query_error_patterns` MCP tool
- [ ] Wire tools to RFD 079 storage queries
- [ ] Add input validation and error handling
- [ ] Add tool usage metrics

**Deliverable:** LLMs can query error logs via MCP

### Phase 2: Summary Integration

**Goals:** Enrich unified summary with error context

- [ ] Add `RecentErrorsSummary` message to protobuf
- [ ] Extend `QueryUnifiedSummary` RPC handler
- [ ] Query top 3 error patterns for time window
- [ ] Query last 5 individual errors
- [ ] Add error count metrics (5m window)

**Deliverable:** Summaries include error context

### Phase 3: LLM Prompt Enhancement

**Goals:** Update system prompts with error querying guidance

- [ ] Document when to use `coral_query_logs` vs `coral_query_error_patterns`
- [ ] Add error log analysis to diagnostic workflows
- [ ] Update example prompts with error correlation
- [ ] Add best practices for error interpretation

**Deliverable:** LLM knows how to effectively use error log tools

### Phase 4: Testing & Documentation

**Goals:** Validate LLM diagnostic workflows

- [ ] Integration test: LLM queries error logs
- [ ] E2E test: Full diagnostic workflow with errors
- [ ] Performance test: MCP tool response times
- [ ] Documentation: LLM error log integration guide
- [ ] Example workflows for common scenarios

**Deliverable:** Production-ready LLM error log integration

## Testing Strategy

### Integration Tests

**MCP Tool Invocation:**

1. Call `coral_query_logs` via MCP
2. Verify correct data returned
3. Test filtering and limits
4. Verify error handling

**Summary Integration:**

1. Service with recent errors
2. Query summary via MCP
3. Verify `recent_errors` populated
4. Verify top patterns included

### E2E Tests

**Diagnostic Workflow:**

1. Simulate service with errors
2. User asks: "Why is service failing?"
3. LLM queries summary (sees error count)
4. LLM queries patterns (sees top errors)
5. LLM queries logs (gets details)
6. LLM provides diagnosis
7. Verify diagnosis mentions errors

## Security Considerations

**Same as RFD 079:**

- Log data may contain PII
- RBAC controls on MCP tool access
- Audit logging for MCP tool calls
- Rate limiting on tool invocations

## Future Work

**Advanced LLM Features (Future RFDs):**

- Automatic error clustering and anomaly detection
- Historical error trend analysis
- Cross-service error correlation
- Proactive error alerting with LLM diagnosis

---

## Dependencies

**Pre-requisites:**

- ✅ RFD 067 (MCP Tools) - Infrastructure for LLM tool integration
- ✅ RFD 074 (LLM-Driven RCA) - Diagnostic workflow framework
- ✅ RFD 079 (Error Log Aggregation) - Error log storage and query APIs

**Enables:**

- Complete LLM-driven incident diagnosis with error context
- Automated root cause analysis using error patterns
- Reduced time to resolution via AI-powered insights
