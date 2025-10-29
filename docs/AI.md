
## AI Capabilities

### Intelligence Modes

**1. Passive Observation** (Always Running)
- Pattern recognition (traffic, resource usage)
- Anomaly detection (unusual restarts, spikes)
- Baseline learning (what's "normal" for your system)

**2. On-Demand Analysis** (When You Ask)
- Natural language Q&A via `coral ask`
- Root cause analysis
- Impact assessment
- Historical correlation

**3. Proactive Insights** (Periodic Reports)
- Daily/weekly summary
- Trend detection
- Capacity planning recommendations
- Security observations

### AI Architecture

**Two-Tier Approach**:

```
┌─────────────────────────────────────┐
│  Simple Tasks (Local/Fast)          │
│  - Anomaly detection (statistical)  │
│  - Pattern matching                 │
│  - Status summaries                 │
│  - Embeddings/similarity            │
│                                      │
│  Models: ONNX, local inference      │
│  Latency: <100ms                    │
│  Cost: Free                          │
└─────────────────────────────────────┘

┌─────────────────────────────────────┐
│  Complex Tasks (API Calls)          │
│  - Root cause analysis              │
│  - Natural language Q&A             │
│  - Multi-service correlation        │
│  - Recommendations                  │
│                                      │
│  Models: Claude, GPT-4              │
│  Latency: 2-5s                      │
│  Cost: User's API key               │
└─────────────────────────────────────┘
```

### Example AI Prompts (with MCP Orchestration)

**Root Cause Analysis**:
```
System: You are analyzing a distributed system. Your goal is to
        identify root causes of issues and provide actionable
        recommendations.

You have access to multiple data sources via MCP:
- Coral: topology, events, deployment history
- Grafana: metrics, time-series data, dashboards
- Sentry: errors, stack traces, release health
- PagerDuty: incidents, on-call info

Context (from Coral):
- Service: api (version 2.1.0)
- Issue: Response time increased from 50ms to 200ms
- Started: 2 hours ago
- Recent Events:
  * 2.5h ago: worker v1.8.0 deployed (via Coral events)
  * 2h ago: api latency started increasing

Available MCP Tools:
  1. grafana.query_metrics("api_response_time_p95", "2h")
  2. grafana.query_metrics("worker_cpu_usage", "2h")
  3. sentry.query_errors(service="api", time_range="2h")
  4. sentry.query_errors(service="worker", time_range="2h")
  5. coral.get_topology(filter="api")

AI Workflow:
  → Calls grafana.query_metrics for latency data
  → Calls sentry.query_errors to check for exceptions
  → Calls coral.get_topology to understand dependencies
  → Synthesizes: "worker v1.8.0 introduced N+1 query bug"
  → Recommendation: "Rollback worker to v1.7.9 or apply hotfix"

Response: [Claude/GPT analyzes across all sources and responds]
```

**Pattern Recognition**:
```
System: You analyze time-series data to identify patterns and trends.

Data:
- Request volume over 30 days (hourly granularity)
- Resource usage over 30 days
- Deploy events

Question: Are there any recurring patterns or trends that could be
          optimized?

Response: [AI identifies daily spike at 14:00 UTC, suggests pre-scaling]
```

### Cost Control

AI calls can be expensive. Controls:

```yaml
ai:
  # Rate limiting
  max_calls_per_hour: 100
  max_calls_per_day: 500

  # Cost caps
  max_cost_per_day: 5.00  # USD
  alert_at_cost: 4.00     # Alert before hitting cap

  # Caching (avoid duplicate analysis)
  cache_ttl: 300          # 5 minutes

  # Use cheaper models for simple tasks
  simple_model: claude-3-haiku-20240307
  complex_model: claude-3-5-sonnet-20241022

  # User confirmation for expensive operations
  require_confirmation:
    - multi_service_analysis
    - historical_deep_dive
```
