# Coral - Examples and Use Cases

**Version**: 0.1 (Design Phase)
**Last Updated**: 2025-10-27

---

## Overview

This document provides concrete examples of how Coral works in practice, including:
- **SDK Integration** - Before/after comparison showing value of SDK
- **MCP Orchestration** - How Coral queries multiple specialized tools
- **AI Analysis** - Root cause identification and recommendations

**Key Concept**: Coral doesn't try to do everything itself. Instead, it acts as an **intelligence orchestrator** that:
1. Gathers topology and events from its own agents (passive or SDK-enhanced)
2. Queries specialized tools via MCP (Grafana, Sentry, PagerDuty, etc.)
3. Synthesizes insights using AI
4. Provides actionable recommendations

---

## Example 0: SDK Integration - Before and After

This example shows the same memory leak scenario analyzed with and without SDK integration, highlighting the value of SDK.

### Scenario
Your API service crashes with OOM errors. You ask Coral: **"Why does the API keep crashing?"**

---

### Version A: Without SDK (Passive Observation)

**What Agent Can See:**
- Process state (via /proc, ps)
- Network connections (via netstat)
- HTTP health endpoint (if it exists)
- Container labels (for version guess)

**Coral's Investigation:**

```bash
$ coral ask "why does the API keep crashing?"

🤖 Analyzing API crashes...
⚠️  Limited data available (app not using SDK)

PASSIVE OBSERVATION FINDINGS:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Detected Issues:
  ⚠️ api process restarted 3 times in last hour
  ⚠️ Exit codes: 137 (OOMKilled) each time

Version Detection (Best-Effort):
  📦 Container label: version=2.3.0
  ⚠️ May be stale (label not always updated)
  ❌ Can't determine when deployed
  ❌ Can't verify build metadata

Timeline:
  14:10 UTC: api restarted (OOMKilled)
  14:42 UTC: api restarted (OOMKilled)
  15:15 UTC: api restarted (OOMKilled)
  ⚠️ Can't correlate with exact deployment time

Metrics (Attempted):
  ❌ Tried :8080/metrics - not found
  ❌ Tried :9090/metrics - not found
  ⚠️ Unknown where Prometheus endpoint is

ANALYSIS (Low Confidence - 60%):
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Possible causes:
  1. Memory leak (OOM suggests this)
  2. Increased traffic (can't verify - no metrics)
  3. Configuration change (can't verify)

RECOMMENDATIONS:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Manual investigation needed:
  1. Check when v2.3.0 was actually deployed
  2. Find and check Prometheus metrics manually
  3. Look at container logs for OOM details
  4. Compare with previous version behavior

💡 For better analysis, consider integrating Coral SDK:
   - Accurate version tracking
   - Component health visibility
   - Metrics endpoint discovery
   - 5-minute integration: https://coral.io/docs/sdk
```

**Problems with Passive Approach:**
- ❌ Can't determine exact deployment time (version label may be stale)
- ❌ Can't find metrics endpoint (unknown location)
- ❌ No component-level health (can't see DB/cache status)
- ❌ Low confidence analysis (60%)
- ⚠️ Requires manual investigation

---

### Version B: With SDK (Enhanced Integration)

**What Agent Can See (via SDK + MCP):**
- SDK GetInfo(): Exact version, git commit, build time, deployment time
- SDK GetHealth(): Component status (database, cache)
- SDK GetConfig(): Metrics at `http://localhost:8080/metrics`
- Grafana MCP: Memory usage over time
- Sentry MCP: Error logs and stack traces

**Coral's Investigation:**

```bash
$ coral ask "why does the API keep crashing?"

🤖 Analyzing API crashes across multiple data sources...

SDK-ENHANCED ANALYSIS:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Detected Issues:
  ✓ api restarted 3 times in last hour (via SDK)
  ✓ All exits: OOMKilled (memory limit reached)

Version Detection (SDK):
  ✓ Version: 2.3.0
  ✓ Git commit: abc123def456
  ✓ Built: 2025-10-27 10:30 UTC
  ✓ Deployed: 2025-10-27 13:55 UTC
  ✓ Uptime before crash: ~15min each time

Component Health (SDK):
  ✓ database: healthy (5ms response)
  ✓ cache: healthy (1ms response)
  → External dependencies are fine

Timeline Correlation:
  13:55 UTC: api v2.3.0 deployed (commit abc123def)
  14:10 UTC: First OOM crash (+15min after deploy)
  14:42 UTC: Second OOM crash (+15min after restart)
  15:15 UTC: Third OOM crash (+15min after restart)
  → Pattern: Crashes exactly 15min after start

Metrics Analysis (Grafana MCP + SDK config):
  📊 Memory usage (from http://localhost:8080/metrics):
      Start: 250MB
      +5min: 350MB
      +10min: 450MB
      +15min: 512MB (limit reached → OOM)

  → Linear memory growth: ~17MB/minute
  → Reaching 512MB limit in exactly 15 minutes

Error Analysis (Sentry MCP):
  ❌ 47 OutOfMemoryError exceptions
  ❌ Stack trace: Memory allocation in request handler
  ❌ Only in v2.3.0 (not in v2.2.5)

Previous Version Comparison (SDK history):
  ✓ v2.2.5: Stable for 5 days (no OOM crashes)
  ✓ v2.2.5: Memory stable at 280MB
  → Problem introduced in v2.3.0

ROOT CAUSE IDENTIFIED (High Confidence - 95%):
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Memory leak in api v2.3.0

EVIDENCE:
  ✓ OOM crashes started immediately after v2.3.0 deploy (13:55 UTC)
  ✓ Linear memory growth (17MB/min) reaching 512MB limit
  ✓ 47 OutOfMemoryError exceptions (via Sentry)
  ✓ Pattern repeats consistently (15min to crash)
  ✓ Previous version v2.2.5 was stable (no memory growth)
  ✓ Dependencies healthy (DB, cache both responding)

Git Diff Analysis:
  📝 commit abc123def: "Add caching layer to request handler"
  ⚠️ Likely culprit: Cache not evicting entries (memory leak)

RECOMMENDATIONS (Prioritized):
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

1. [IMMEDIATE] Rollback to v2.2.5
   Impact: Restore stability immediately
   Command: coral rollback api --to-version 2.2.5

2. [SHORT-TERM] Increase memory limit (temporary workaround)
   Note: Delays problem, doesn't fix it (will crash at 1GB in 30min)
   Command: kubectl set resources deployment api --limits=memory=1Gi

3. [LONG-TERM] Fix memory leak in v2.3.0
   Root cause: Caching layer (commit abc123def)
   Fix: Add cache eviction policy (LRU, TTL, or size limit)
   Files to check: request_handler.go:47 (where cache is used)

Want me to execute the rollback? [y/N]
```

**Benefits of SDK Approach:**
- ✅ Exact deployment time correlated with crashes
- ✅ Found metrics endpoint automatically
- ✅ Component health ruled out dependencies
- ✅ High confidence analysis (95%)
- ✅ Specific commit identified as likely culprit
- ✅ Actionable recommendations with commands

---

### Data Source Comparison

| Data Point | Passive (Without SDK) | Enhanced (With SDK) | Source |
|------------|----------------------|---------------------|--------|
| **Version** | "2.3.0" (maybe stale) | "2.3.0" (confirmed current) | SDK GetInfo() |
| **Deployment Time** | ❌ Unknown | ✓ 13:55 UTC | SDK GetInfo().start_time |
| **Build Metadata** | ❌ None | ✓ Commit abc123def, built 10:30 UTC | SDK GetInfo() |
| **Memory Metrics** | ❌ Endpoint not found | ✓ Linear growth 250MB→512MB | SDK GetConfig() → Grafana |
| **Component Health** | ❌ Unknown | ✓ DB healthy, cache healthy | SDK GetHealth() |
| **Error Details** | ⚠️ Check logs manually | ✓ 47 OOM errors, stack traces | Sentry MCP |
| **Correlation** | ⚠️ Manual work | ✓ Automated across all sources | AI + SDK + MCP |
| **Confidence** | 60% | 95% | |
| **Time to Insight** | ~30 min manual work | ~30 seconds | |

---

### Integration Effort

**Adding SDK to the API** (5 minutes):

```go
// main.go - Before (no SDK)
package main

func main() {
    // App code...
    http.ListenAndServe(":8080", handler)
}

// main.go - After (with SDK)
package main

import coral "github.com/coral-mesh/sdk-go"

func main() {
    // Add 4 lines for SDK
    coral.Initialize(coral.Config{
        ServiceName: "api",
        Version:     "2.3.0",
        Endpoints: coral.Endpoints{
            Metrics: "http://localhost:8080/metrics",
        },
    })
    defer coral.Shutdown()

    // Existing app code unchanged
    http.ListenAndServe(":8080", handler)
}
```

**Result**: 4 lines of code → 95% confidence root cause analysis in 30 seconds

---

## Example 1: Memory Leak Root Cause Analysis (Detailed MCP Orchestration)

### Scenario
Your API service keeps crashing with OOM errors. You ask Coral: **"Why does the API keep crashing?"**

### Coral's Orchestration

```
┌─────────────────────────────────────────────────────┐
│ User Question: "Why does the API keep crashing?"    │
└──────────────────┬──────────────────────────────────┘
                   │
                   ▼
         ┌─────────────────────┐
         │  Coral Orchestrator │
         └──────────┬──────────┘
                    │
    ┌───────────────┼───────────────┬───────────────┐
    │               │               │               │
    ▼               ▼               ▼               ▼
┌────────┐    ┌─────────┐    ┌─────────┐    ┌──────────┐
│ Coral  │    │ Grafana │    │ Sentry  │    │PagerDuty │
│   DB   │    │   MCP   │    │   MCP   │    │   MCP    │
└────┬───┘    └────┬────┘    └────┬────┘    └────┬─────┘
     │             │              │              │
     ▼             ▼              ▼              ▼
```

**Step 1: Query Coral's Events**
```bash
# Coral checks its local event database
Events found:
  - 14:05 UTC: api restarted (OOMKilled)
  - 14:42 UTC: api restarted (OOMKilled)
  - 15:15 UTC: api restarted (OOMKilled)
  - 13:55 UTC: api deployed v2.3.0

Pattern: 3 restarts in last 1.5 hours, all after v2.3.0 deployment
```

**Step 2: Call Grafana MCP for Metrics**
```go
// Coral calls Grafana MCP
result := mcpClient.CallTool("grafana", "query_metrics", {
    "service": "api",
    "metrics": ["memory_usage", "memory_limit"],
    "time_range": "2h"
})

// Result from Grafana:
{
  "memory_usage": [
    {"time": "13:55", "value": "250MB"},  // deployment
    {"time": "14:00", "value": "350MB"},
    {"time": "14:05", "value": "512MB"},  // crash
    {"time": "14:05", "value": "220MB"},  // restart
    {"time": "14:40", "value": "510MB"},  // crash
    {"time": "14:42", "value": "230MB"},  // restart
    {"time": "15:15", "value": "512MB"}   // crash
  ],
  "memory_limit": "512MB"
}

Observation: Memory grows from ~250MB to 512MB limit in 10-15 minutes
```

**Step 3: Call Sentry MCP for Errors**
```go
// Coral calls Sentry MCP
result := mcpClient.CallTool("sentry", "query_errors", {
    "service": "api",
    "time_range": "2h",
    "event_type": "error"
})

// Result from Sentry:
{
  "errors": [
    {
      "type": "OutOfMemoryError",
      "count": 47,
      "first_seen": "14:05 UTC",
      "message": "java.lang.OutOfMemoryError: Java heap space",
      "release": "api@2.3.0"
    }
  ]
}

Correlation: OOM errors only appear in v2.3.0, not in previous version
```

**Step 4: Call PagerDuty MCP for Incident Context**
```go
// Coral calls PagerDuty MCP
result := mcpClient.CallTool("pagerduty", "get_incidents", {
    "service": "api",
    "time_range": "2h"
})

// Result from PagerDuty:
{
  "incidents": [
    {
      "id": "INC-12345",
      "status": "resolved",
      "created_at": "14:05 UTC",
      "resolved_at": "14:05 UTC",
      "auto_resolved": true
    },
    {
      "id": "INC-12346",
      "status": "resolved",
      "created_at": "14:42 UTC",
      "resolved_at": "14:43 UTC",
      "auto_resolved": true
    }
  ]
}

Note: Incidents auto-resolved after restart (self-healing)
```

**Step 5: AI Synthesis**
```
Coral sends all collected data to Claude/GPT:

Context:
- Coral Events: 3 OOM crashes after v2.3.0 deploy
- Grafana: Memory grows 250MB → 512MB in ~15 min (linear growth)
- Sentry: 47 OutOfMemoryError exceptions in v2.3.0
- PagerDuty: 2 incidents, both auto-resolved after restart

AI Analysis:
→ Root Cause: Memory leak introduced in api v2.3.0
→ Confidence: High (consistent pattern, only in new version)
→ Evidence: Linear memory growth, OOM errors, auto-recovery
```

**Step 6: User Response**
```bash
$ coral ask "Why does the API keep crashing?"

🤖 Analyzing API crashes across multiple data sources...

ROOT CAUSE IDENTIFIED (High Confidence)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Memory leak in api v2.3.0

EVIDENCE:
  ✓ 3 OOM crashes in 1.5 hours (via Coral events)
  ✓ Memory grows linearly from 250MB → 512MB limit (via Grafana)
  ✓ 47 OutOfMemoryError exceptions (via Sentry)
  ✓ Pattern started immediately after v2.3.0 deploy (via Coral)
  ✓ Previous version v2.2.5 was stable for 5 days (via Coral)

TIMELINE:
  13:55 UTC: api v2.3.0 deployed
  14:05 UTC: First OOM crash (10 min after deploy)
  14:42 UTC: Second OOM crash
  15:15 UTC: Third OOM crash

RECOMMENDATIONS:

  1. [IMMEDIATE] Rollback to v2.2.5
     Command: coral rollback api --to-version 2.2.5
     Impact: Restore stability, users unaffected

  2. [SHORT-TERM] Increase memory limit (temporary workaround)
     Note: This only delays the problem, doesn't fix it
     Command: kubectl set resources deployment api --limits=memory=1Gi

  3. [LONG-TERM] Fix memory leak in v2.3.0
     Hint: Run memory profiler, check recent commits in v2.3.0
     Related commits:
       - a3b7c9d "Add caching layer" (likely culprit - no cache eviction?)

IMPACT:
  - Current: ~3 crashes/hour, ~30s downtime each
  - After rollback: Expected 0 crashes (v2.2.5 was stable)

Want me to execute the rollback? [y/N]
```

### MCP Servers Used
1. **Coral's own DB**: Events, topology, deployment history
2. **Grafana MCP**: Memory metrics, time-series data
3. **Sentry MCP**: Error tracking, stack traces
4. **PagerDuty MCP**: Incident timeline, on-call context

---

## Example 2: Performance Degradation Investigation

### Scenario
Users report the API is slow. You ask: **"Why is the API slow?"**

### Coral's Orchestration

**Step 1: Coral's Events**
```
Recent events for 'api':
  - 2h ago: worker v1.8.0 deployed
  - 1.5h ago: api latency started increasing (anomaly detected)
```

**Step 2: Grafana MCP - Response Time**
```go
mcpClient.CallTool("grafana", "query_metrics", {
    "service": "api",
    "metric": "response_time_p95",
    "time_range": "3h"
})

Result:
  Before: 45ms average
  After worker deploy: 180ms average
  Spike correlation: +300% latency after worker deploy
```

**Step 3: Grafana MCP - Database Queries**
```go
mcpClient.CallTool("grafana", "query_metrics", {
    "service": "database",
    "metric": "query_count",
    "time_range": "3h"
})

Result:
  Before: ~1000 queries/min
  After: ~4500 queries/min
  Finding: 4.5x increase in DB queries (N+1 query problem?)
```

**Step 4: Coral Topology**
```
Topology changes:
  Before: api → worker (RPC)
  After: api → worker → database (new connection!)

Analysis: worker now queries DB directly (didn't before)
```

**Step 5: Sentry MCP - Warnings**
```go
mcpClient.CallTool("sentry", "query_issues", {
    "service": "worker",
    "level": "warning",
    "time_range": "3h"
})

Result:
  Warning: "Query performance degraded" (127 occurrences)
  Message: "SELECT * FROM users WHERE id IN (...)" taking 200ms
```

**AI Synthesis & Response**
```bash
$ coral ask "Why is the API slow?"

🤖 Analyzing API performance across services...

ROOT CAUSE IDENTIFIED
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

worker v1.8.0 introduced N+1 query problem affecting API

EVIDENCE:
  ✓ API latency increased 45ms → 180ms (via Grafana)
  ✓ Started immediately after worker v1.8.0 deploy (via Coral)
  ✓ Database queries increased 4.5x (via Grafana)
  ✓ New connection: worker → database (via Coral topology)
  ✓ Slow query warnings in worker (via Sentry)

ROOT CAUSE CHAIN:
  1. worker v1.8.0 changed data fetching logic
  2. worker now queries database for each API request
  3. worker makes N separate queries instead of 1 batch query
  4. Database is overwhelmed (4.5x query load)
  5. API waits for worker, causing 180ms latency

RECOMMENDATIONS:

  1. [IMMEDIATE] Rollback worker to v1.7.9
     Impact: Restore API performance to 45ms
     Command: coral rollback worker --to-version 1.7.9

  2. [ALTERNATIVE] Add database query caching
     Impact: Reduce DB load by ~70%
     Note: Doesn't fix root cause, but can buy time

  3. [LONG-TERM] Fix N+1 query in worker v1.8.0
     Location: worker/src/api_handler.go:127
     Fix: Use batch query instead of loop

IMPACT ANALYSIS:
  - Affected users: ~40% of API requests go through worker
  - Current: P95 latency 180ms (users notice slowness)
  - After rollback: P95 latency 45ms (normal)

Run rollback? [y/N]
```

### MCP Servers Used
1. **Coral's own DB**: Deployment events, topology changes
2. **Grafana MCP**: Response time, database query metrics
3. **Sentry MCP**: Performance warnings, slow queries

---

## Example 3: Using Coral from Claude Desktop

### Scenario
A developer using Claude Desktop wants to understand their production system.

**Claude Desktop Config** (`~/.config/claude/mcp.json`):
```json
{
  "mcpServers": {
    "coral": {
      "command": "coral",
      "args": ["mcp", "server"],
      "env": {
        "CORAL_CONFIG": "/Users/dev/.coral/config.yaml"
      }
    },
    "grafana": {
      "command": "npx",
      "args": ["-y", "@grafana/mcp-server"],
      "env": {
        "GRAFANA_URL": "https://grafana.company.com",
        "GRAFANA_TOKEN": "..."
      }
    }
  }
}
```

**User Conversation with Claude Desktop**:

```
User: "What's the current state of my production services?"

Claude Desktop:
  → Calls coral_get_topology()
  → Result: 8 services, 2 regions, api in us-east showing warning

Claude: "You have 8 services running across 2 regions. I see a warning
         on your API service in us-east. Let me get more details..."

  → Calls coral_query_events(service="api", time_range="1h")
  → Result: 3 restarts in last hour

Claude: "Your API has restarted 3 times in the past hour, which is
         unusual. Let me check metrics..."

  → Calls grafana MCP: query_metrics(service="api", metric="memory")
  → Result: Memory at 95% before each restart

Claude: "I found the issue! Your API is hitting memory limits and
         getting OOM-killed. This started after deploying v2.1.0.

         I recommend either:
         1. Rollback to v2.0.9 (was stable for 5 days)
         2. Increase memory limit to 1GB

         Would you like me to help with the rollback?"
```

### Benefits
- Developer doesn't need to switch contexts
- Claude Desktop has access to both real-time topology (Coral) AND metrics (Grafana)
- Natural language interface to production data
- Coral provides the correlation intelligence

---

## Example 4: Custom MCP Server Integration

### Scenario
Your company has internal deployment tooling. You create a custom MCP server for it.

**Custom MCP Server** (`company-deploy-mcp-server`):
```go
// Internal company MCP server for deployment info
package main

import "github.com/mark3labs/mcp-go/server"

func main() {
    s := server.NewStdioMCPServer("company-deploy", "1.0.0")

    s.AddTool(server.Tool{
        Name: "get_deployment_info",
        Description: "Get detailed deployment information from internal systems",
        InputSchema: map[string]interface{}{
            "type": "object",
            "properties": map[string]interface{}{
                "service": map[string]string{"type": "string"},
                "version": map[string]string{"type": "string"},
            },
        },
        Handler: func(args map[string]interface{}) (interface{}, error) {
            // Query internal deployment database
            service := args["service"].(string)
            version := args["version"].(string)

            return map[string]interface{}{
                "deployed_by": "alice@company.com",
                "deployed_at": "2025-10-27T13:55:00Z",
                "git_commit": "a3b7c9d",
                "jenkins_build": "https://jenkins.company.com/build/12345",
                "change_ticket": "CHG-98765",
                "approval": {
                    "approved_by": "bob@company.com",
                    "approved_at": "2025-10-27T13:45:00Z",
                },
                "tests": {
                    "unit_tests": "passed",
                    "integration_tests": "passed",
                    "performance_tests": "skipped",  // Uh oh!
                },
            }, nil
        },
    })

    s.Serve()
}
```

**Coral Config**:
```yaml
mcp:
  servers:
    company-deploy:
      command: "/usr/local/bin/company-deploy-mcp-server"
      timeout: 30s
```

**Coral Usage**:
```bash
$ coral ask "Who deployed api v2.3.0 and why is it breaking?"

🤖 Analyzing deployment for api v2.3.0...

DEPLOYMENT INFORMATION (via company-deploy MCP):
  Deployed by: alice@company.com
  Time: 13:55 UTC today
  Git commit: a3b7c9d
  Change ticket: CHG-98765
  Approved by: bob@company.com

ISSUE DETECTED:
  ⚠️  Performance tests were SKIPPED for this deployment!

CURRENT PROBLEMS (via Grafana + Sentry):
  - Memory leak causing OOM crashes
  - 3 crashes in last 1.5 hours

ANALYSIS:
  If performance tests had run, they likely would have caught
  the memory leak before production deployment.

RECOMMENDATION:
  1. Rollback to v2.2.5 immediately
  2. Contact alice@company.com about the deployment
  3. Require performance tests for future deploys
  4. Reference change ticket CHG-98765 in post-mortem

Would you like me to create a Slack message to alert the team?
```

### Benefits
- Coral integrates seamlessly with internal tools
- Rich deployment context helps with incident response
- AI can correlate deployment info with runtime issues
- No need to build custom integrations - just MCP

---

## Example 5: Multi-Region Incident Correlation

### Scenario
Services are failing in multiple regions. Pattern recognition across global infrastructure.

**User Query**: `coral ask "Why are services failing in multiple regions?"`

**Coral's Global Analysis**:

```
Step 1: Query Coral topology
  → 8 services across 3 regions (us-east, eu-west, ap-south)
  → Failures detected in: us-east (api, worker), eu-west (api)

Step 2: Call Grafana MCP for each region
  → us-east: Database connection timeouts (started 10 min ago)
  → eu-west: Database connection timeouts (started 10 min ago)
  → ap-south: Normal (no issues)

Step 3: Check Coral events
  → 11 min ago: database-proxy v3.1.0 deployed globally
  → 10 min ago: Connection errors started

Step 4: Call Sentry MCP
  → Error: "connection pool exhausted" (both regions)
  → Only in services using database-proxy

Step 5: Check database-proxy git diff
  → v3.1.0 changed connection pool size from 100 → 10 (typo!)
```

**Response**:
```bash
🤖 MULTI-REGION INCIDENT DETECTED
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

ROOT CAUSE: database-proxy v3.1.0 configuration error

AFFECTED REGIONS:
  ✗ us-east (2 services failing)
  ✗ eu-west (1 service failing)
  ✓ ap-south (normal - proxy not deployed yet)

PROBLEM:
  database-proxy v3.1.0 reduced connection pool:
    Before: 100 connections
    After: 10 connections (TYPO IN CONFIG)

  Result: Connection pool exhausted under normal load

EVIDENCE:
  ✓ Both regions failing exactly 1 min after proxy deploy
  ✓ Same error: "connection pool exhausted" (via Sentry)
  ✓ Database itself is healthy (via Grafana)
  ✓ ap-south unaffected (proxy not deployed yet)

IMMEDIATE ACTION REQUIRED:
  1. STOP ap-south deployment (prevent spreading to 3rd region)
  2. Rollback database-proxy in us-east and eu-west
  3. Fix configuration typo in v3.1.0

Commands:
  $ coral pause-deployment database-proxy --region ap-south
  $ coral rollback database-proxy --regions us-east,eu-west --to-version 3.0.8

This will restore service in ~2 minutes.

Execute these commands? [y/N]
```

### MCP Servers Used
1. **Coral**: Global topology, deployment timeline
2. **Grafana MCP**: Per-region metrics, database health
3. **Sentry MCP**: Error patterns across regions
4. **Internal Git MCP**: Code diff analysis (custom)

---

## Summary

These examples demonstrate how Coral:

1. **Doesn't Reinvent the Wheel**
   - Uses Grafana for metrics (not building another metrics DB)
   - Uses Sentry for errors (not building error tracking)
   - Uses PagerDuty for incidents (not building incident management)

2. **Adds Intelligence via Orchestration**
   - Correlates data from multiple sources
   - Identifies patterns humans might miss
   - Provides actionable recommendations
   - Explains the "why" behind issues

3. **Is Composable**
   - Works with any MCP server
   - Custom internal tools can integrate easily
   - Other AI assistants can query Coral
   - Standard protocol (MCP) for everything

4. **Focuses on Core Competency**
   - Topology discovery (via agents)
   - Event correlation
   - AI-powered root cause analysis
   - Intelligent orchestration

---

## Related Documents

- **[CONCEPT.md](./CONCEPT.md)** - High-level concept and key ideas
- **[DESIGN.md](./DESIGN.md)** - Design philosophy and architecture
- **[IMPLEMENTATION.md](./IMPLEMENTATION.md)** - Technical implementation details
- **[ROADMAP.md](./ROADMAP.md)** - Development phases and milestones
