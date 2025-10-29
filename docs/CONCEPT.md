# Coral - Core Concept

**Vision**: Unified operations for distributed apps - observe, debug, and control from one interface.

**Tagline**: One binary. One interface. Your distributed app becomes understandable and controllable.

---

## The Unified Operations Interface

**Stop juggling tools. Get one interface for everything.**

Right now, you have scattered components - a frontend, an API, a database, maybe some workers - all running independently across your laptop, VMs, and Kubernetes. When you need to:

- **Debug an issue** → Check multiple dashboards, correlate logs, search metrics
- **Toggle a feature** → Update LaunchDarkly, change configs, redeploy
- **Investigate performance** → SSH to servers, start profilers, capture traffic
- **Rollback a deployment** → kubectl commands, manual verification
- **Understand dependencies** → Piece together from tribal knowledge

**You're the intelligence connecting the dots. You're juggling tools.**

**What if instead, you had one unified interface:**
- **Observe**: Health, connections, resource usage across all services
- **Debug**: Ask questions in natural language, get AI-powered insights
- **Control**: Feature flags, traffic inspection, profiling, rollbacks - all from one place
- **Intelligence**: AI that understands your app, learns patterns, explains what's happening
- **Agency**: Autonomous investigation, human-approved execution

**This is what Coral creates: unified operations for distributed apps.**

Think of Coral as a **coral colony** - distributed sensors (agents) coordinating through a central colony, creating collective intelligence. Like a coral's nerve net, agents observe locally and coordinate centrally, but you stay in control.

---

## From Tool-Hopping to Conversation

### Today: You Are the Intelligence

```
🔥 Alert: API service down

[You, frantically]
→ Open Grafana → memory spike? CPU? Network?
→ Open Sentry → any errors? Stack traces?
→ Open Logs → search for "error" → filter by service → try timestamp correlation
→ Check deploy history → was there a recent release?
→ Check dependencies → is the database OK? Cache? Message queue?
→ Correlate in your head → form hypothesis → test → iterate

⏱️  30-60 minutes later, you find it: OOM in v2.3.0 due to connection pool leak
```

**You are the correlator. You connect the dots. Every. Single. Time.**

### With Coral: Unified Operations Interface

```
🔥 Alert: API service down

You: "What's wrong with the API?"

Coral: "The API service crashed with OutOfMemoryError 3 minutes ago.
       This is happening in v2.3.0 which deployed 14 minutes ago.

       I checked your app:
       • API memory: grew 250MB→512MB over 10 minutes (linear growth)
       • Sentry: 47 OutOfMemoryError exceptions, all in connection pooling code
       • Database: Healthy, connection count normal from DB side
       • Frontend: Unaffected, serving cached data

       Root cause: Connection pool leak in v2.3.0 (95% confidence)

       Recommendation: Rollback API to v2.2.5

       Execute rollback now? [y/N]"

You: "y"

Coral: "Rolling back API to v2.2.5... Done.
       Memory stabilized at 240MB. Monitoring for 5 minutes...

       All green. API healthy on v2.2.5."

⏱️  <1 second analysis, 30 seconds to rollback and verify
```

**Observe → Debug → Control. One interface. One command.**

---

## What Makes Coral Unique?

### 1. Unified Interface (Observe + Debug + Control)

Unlike traditional monitoring tools that only show you what's happening, Coral lets you **act**:

**Observe:**
- Health, connections, resource usage across all services
- Auto-discovered topology and dependencies
- Learned behavioral baselines

**Debug:**
- Natural language queries: "Why is checkout slow?"
- AI-powered root cause analysis
- Multi-source investigation (local + optional MCP enrichment)

**Control** (SDK-integrated mode):
- **Feature flags**: Toggle features across services from one interface
- **Traffic inspection**: Sample and inspect live requests without SSH
- **Profiling**: Start/stop profilers remotely (CPU, heap, goroutine)
- **Rollbacks**: Revert deployments with one command
- **All coordinated through AI insights**: "This looks bad, want to rollback?"

**Key differentiator**: One interface for the full operations cycle, not just observability.

### 2. It Knows Itself
- **Topology awareness**: Every service, every dependency, automatically discovered
- **Baseline understanding**: What's "normal" for each component - latency, memory, traffic patterns
- **Version tracking**: What's running where, when it deployed, what changed
- **Relationship mapping**: How services depend on each other, data flows

### 2. It Feels Pain
- **Continuous observation**: Agents on every host, watching 24/7
- **Anomaly detection**: Spots when behavior deviates from learned patterns
- **Impact awareness**: Understands which failures are critical vs. minor
- **Multi-signal correlation**: Connects symptoms across metrics, logs, traces, errors

### 3. It Communicates
- **Natural language interface**: Ask questions like you'd ask an engineer
- **Explains reasoning**: Not just "what" but "why" - shows evidence
- **Proactive notifications**: "I noticed memory climbing, might OOM soon"
- **Conversational debugging**: Interactive investigation, not static dashboards

### 4. It Learns
- **Pattern recognition**: Remembers previous incidents, successful fixes
- **Behavior modeling**: Learns your system's personality over time
- **Feedback loops**: Gets smarter from your approvals/rejections
- **Context accumulation**: Builds knowledge graph of your infrastructure

### 5. It Connects Everything
- **MCP orchestration**: Queries Grafana, Sentry, PagerDuty, your custom tools
- **Multi-source synthesis**: Combines signals from disparate systems
- **Universal interface**: One conversation touches all your observability tools
- **Bidirectional**: You can query Coral from Claude Desktop, other AI assistants

### 6. It Acts (With Permission)
- **Autonomous investigation**: Explores on its own when anomalies detected
- **Recommendation generation**: Suggests exact commands to execute
- **Supervised execution**: You review and approve before action
- **Graduated autonomy**: Over time, can handle routine operations (optional)

---

## The Transformation

### Before Coral: Scattered Components
```
[Service A] ─┐            ┌─ [Grafana Dashboards]
[Service B] ─┼─ [Network] ┼─ [Sentry Logs]
[Service C] ─┘            ├─ [PagerDuty Alerts]
                          └─ [ELK Stack]

Intelligence: YOU (manual correlation)
```

### After Coral: Living Mesh
```
           ┌─────────────────────────────┐
           │    CORAL INTELLIGENCE       │
           │   (The Brain & Nervous      │
           │        System)              │
           └──┬────────┬─────────┬───────┘
              │        │         │
         ┌────▼──┐ ┌──▼────┐ ┌─▼──────┐
         │Agent A│ │Agent B│ │Agent C │  ← Nerves (observe locally)
         └───┬───┘ └───┬───┘ └───┬────┘
             │         │         │
         ┌───▼───┐ ┌───▼───┐ ┌──▼─────┐
         │Svc A  │ │Svc B  │ │Svc C   │  ← Your apps (run normally)
         └───────┘ └───────┘ └────────┘
              ↓         ↓         ↓
         [Prometheus] [Logs] [Traces]    ← Existing tools (still work)
                     ↓
              ┌──────▼────────┐
              │  Grafana MCP  │            ← MCP integration
              │  Sentry MCP   │               (orchestrates tools)
              │  Custom MCPs  │
              └───────────────┘

Intelligence: THE SYSTEM (autonomous + supervised)
```

The mesh **IS** your infrastructure, but now it can think.

---

## The Potential: Emergent Intelligence

This isn't just better monitoring. **It's infrastructure that evolves.**

### Short-Term: Autonomous Operations Assistant
- Watches continuously, you sleep soundly
- Investigates incidents in seconds, not minutes
- Explains problems in plain language
- Recommends solutions with evidence
- Executes with your approval

### Medium-Term: Proactive System Health
- **Predicts issues before they occur**: "Memory trend suggests OOM in 2 hours"
- **Capacity planning**: "Traffic growing 15%/week, scale horizontally next month"
- **Deployment intelligence**: "v2.4.0 looks risky - integration tests slower, memory +30MB"
- **Security awareness**: "Unusual connection pattern: service-x talking to external IP"

### Long-Term: Self-Healing Infrastructure
- **Pattern learning**: Recognizes "seen this before" and auto-suggests proven fix
- **Graduated autonomy**: Handle routine operations automatically (with strict guardrails)
- **Adaptive optimization**: Adjusts resource allocation based on learned patterns
- **Collective intelligence**: Systems learn from each other (federated, privacy-preserving)

### Moonshot: Infrastructure as Organism
- **Self-awareness**: System understands its own architecture better than documentation
- **Resilience**: Automatically routes around failures, heals degraded components
- **Evolution**: Suggests architectural improvements based on observed bottlenecks
- **Collaboration**: Works alongside human engineers as peer, not just tool

---

## The Problem We're Solving

**Operating distributed systems shouldn't require a team of humans doing correlation work.**

Today's reality:
1. **Too many tools** - Grafana, Sentry, PagerDuty, logs, traces... all disconnected
2. **Too much data** - Dashboards show "what" but not "why"
3. **Too slow to diagnose** - Hours of manual correlation across tools
4. **Too reactive** - Alerts fire after users are impacted
5. **Too manual** - Same investigation playbook every time, but you repeat it

**Current solution**: Hire more SREs, build more dashboards, accept the toil.

**Coral's solution**: Make the infrastructure intelligent enough to understand and explain itself.

---

## Core Principles

### 1. Agentic Intelligence, Supervised Execution
- **Autonomous**: Observes, detects, analyzes, recommends without human intervention
- **Supervised**: Requires human approval before executing any action
- **Trust-first**: Build confidence in AI before granting autonomy

```
AI does: Observe → Detect → Analyze → Recommend
Human does: Review → Approve → Execute
```

### 2. Self-Sufficient Intelligence, Optionally Enhanced via MCP
- **Local-first intelligence** - Agents collect rich data locally, mesh is self-aware
- **No external dependencies** - Works standalone, air-gap compatible
- **MCP for depth** - External tools (Grafana, Sentry) add historical context when available
- **Standard protocol** - MCP for extensions (in and out)

```
Tier 1 (Core): Local agents + colony = self-sufficient intelligence
Tier 2 (Optional): Query Grafana MCP, Sentry MCP, etc. for enrichment
Export: Others query Coral MCP (topology, events, correlations)
```

### 2a. Layered Storage for Scale
- **Distributed intelligence** - Agents maintain recent high-resolution data locally
- **Colony aggregation** - Colony stores summaries and cross-agent correlations
- **On-demand detail retrieval** - Colony queries agents directly when investigating
- **Graceful degradation** - Operates on stale summaries when agents offline
- **Scales horizontally** - Storage distributes naturally with app complexity

### 3. Control Plane Only
- **Never in data path** - Can't break application traffic
- **Observe, don't proxy** - Agents watch apps locally, don't intercept
- **If Coral fails, apps keep running** - Zero dependency

```
Your apps communicate: via existing VPC/service mesh/load balancers
Coral communicates: via separate encrypted control mesh (Wireguard)
```

### 4. User-Controlled & Private
- **You run the colony** - On your laptop (dev), VPS (staging), or cluster (prod)
- **Your data stays local** - No SaaS, no cloud, no telemetry to us
- **Your AI keys** - Use your own Anthropic/OpenAI accounts
- **Open source** - Auditable, forkable, extendable

### 5. Simple by Default
- **5 minute setup** - Not days of YAML and configuration
- **Works anywhere** - VMs, containers, K8s, edge, bare metal
- **No prerequisites** - No Kubernetes, no service mesh required

---

## SDK Integration: Two-Tier Operations Model

### Philosophy: Passive Works, SDK Enables Control

Coral works in **two modes**:

1. **Passive mode** (no SDK): Basic observability - process monitoring, connection mapping, AI debugging
2. **SDK-integrated mode** (full control): Feature flags, traffic inspection, profiling, rollbacks + all passive capabilities

**Key Principle**: Don't reinvent standards, enable control where it matters
- ❌ Don't create custom metrics protocol → Use Prometheus /metrics
- ❌ Don't create custom tracing → Use OpenTelemetry
- ❌ Don't replace existing instrumentation → Integrate with it
- ✅ SDK enables control APIs (feature flags, traffic, profiling) where no standards exist
- ✅ SDK provides discovery + structured metadata for better observability

### What SDK Enables

**1. Control Capabilities (Primary Value)**
```go
import coral "github.com/coral-io/coral-go"

// Feature flags
if coral.IsEnabled("new-checkout") {
    useNewCheckout()
}

// Traffic sampling (automatic via middleware)
coral.EnableTrafficSampling(coral.TrafficOptions{
    SampleRate: 0.1,  // 10% of requests
})

// Profiling endpoints
coral.EnableProfiling(coral.ProfilingOptions{
    Types: []string{"cpu", "heap", "goroutine"},
})

// Rollback coordination
coral.OnRollback(func(toVersion string) error {
    // Custom rollback logic
    return deployVersion(toVersion)
})
```

**2. Enhanced Observability (Secondary Value)**
```go
// Component-level health
coral.RegisterHealthCheck("database", func() coral.Health {
    return coral.Healthy // or coral.Degraded, coral.Unhealthy
})

// Version and build metadata
coral.RegisterService("api", coral.Options{
    Version: "2.1.0",
    GitCommit: "abc123",
    BuildTime: time.Now(),
})

// Endpoint discovery (tells agent where to find standard endpoints)
coral.Configure(coral.Config{
    MetricsEndpoint: "http://localhost:8080/metrics",  // Prometheus
    TracesEndpoint: "http://localhost:9411/traces",     // OTEL
    PprofEndpoint: "http://localhost:6060/debug/pprof", // Go profiling
})
```

### Integration Example (5 lines)

```go
import coral "github.com/coral-io/sdk-go"

func main() {
    coral.Initialize(coral.Config{
        ServiceName: "api",
        Version:     "2.1.0",
        Endpoints: coral.Endpoints{
            Metrics: "http://localhost:8080/metrics",  // Existing Prometheus
        },
    })

    // App continues normally
    http.ListenAndServe(":8080", handler)
}
```

### What SDK Unlocks

| Feature               | Without SDK (Passive)                        | With SDK (Integrated)                                        |
|-----------------------|----------------------------------------------|--------------------------------------------------------------|
| **Feature Flags**     | ❌ Not available                              | ✅ Toggle features remotely<br>Gradual rollouts               |
| **Traffic Inspection**| ❌ Not available                              | ✅ Sample & inspect requests<br>No SSH required               |
| **Profiling**         | ❌ Not available                              | ✅ Remote profiling triggers<br>CPU, heap, goroutine          |
| **Rollbacks**         | ❌ Manual (kubectl/SSH)                       | ✅ One-command rollback<br>Coordinated with AI insights      |
| **Version Detection** | ⚠️ Best-effort from labels<br>May be stale   | ✅ Accurate from binary<br>Git commit, build time             |
| **Health Status**     | ⚠️ Binary (up/down)<br>HTTP endpoint polling | ✅ Structured components<br>DB, cache, etc. + degraded states |
| **Metrics Discovery** | ⚠️ Guess/probe<br>May miss endpoints         | ✅ Configured explicitly<br>Agent knows where to scrape       |

### Two-Tier Integration Model

```
Tier 0 (Passive - No SDK, No Code Changes)
  ┌──────────────────────────────────────┐
  │ What you get:                        │
  │ • Process monitoring (CPU, memory)   │
  │ • Connection mapping (netstat/ss)    │
  │ • HTTP health checks                 │
  │ • AI-powered debugging               │
  │ • Auto-discovered topology           │
  └──────────────────────────────────────┘
  └─> Use case: Quick setup, no integration work

Tier 1 (SDK-Integrated - 5-30 minutes)
  ┌──────────────────────────────────────┐
  │ Control capabilities (PRIMARY):      │
  │ ✅ Feature flags (toggle remotely)   │
  │ ✅ Traffic inspection (sample/view)  │
  │ ✅ Profiling (remote triggers)       │
  │ ✅ Rollbacks (one command)           │
  │                                      │
  │ Enhanced observability (BONUS):      │
  │ ✅ Accurate version tracking         │
  │ ✅ Component-level health            │
  │ ✅ Build metadata (git, timestamp)   │
  │ ✅ Endpoint discovery                │
  └──────────────────────────────────────┘
  └─> Use case: Full operations control
```

### Why This Approach Works

**Respects Existing Investments:**
- Already using Prometheus? SDK points to it, doesn't replace it
- Already using OTEL? SDK discovers it, doesn't duplicate it
- Already have health checks? SDK enhances them

**Low Friction:**
- 5 minutes to integrate (Tier 1)
- No behavioral changes to app
- Works with existing tooling

**Progressive Enhancement:**
- Start passive (zero work)
- Add SDK when you want more (incremental value)
- Enable advanced features optionally (power users)

---

## How It Works: Building the Living Mesh

### The Nervous System Architecture

Think of Coral as giving your distributed system **sensory nerves, a spinal cord, and a brain**:

```
┌──────────────────────────────────────────────────────┐
│          THE BRAIN (Colony)                          │
│                                                      │
│  Layered Intelligence (Colony Storage):              │
│  • Summaries & aggregations across all agents        │
│  • Historical data (beyond agent retention window)   │
│  • Cross-agent correlations and patterns             │
│  • Topology knowledge graph (auto-discovered)        │
│  • Learned behavioral baselines                      │
│  • AI synthesis & reasoning (Claude/GPT)             │
│  • Can query agents on-demand for recent details     │
│                                                      │
└──────┬──────────┬──────────┬────────────────────────┘
       │          │          │
       │ Encrypted control mesh (WireGuard)
       │          │          │
       │   (push summaries + bidirectional queries)
       │          │          │
   ┌───▼─────┐ ┌──▼─────┐ ┌──▼─────┐
   │ Agent   │ │ Agent  │ │ Agent  │  ← Nerve endings
   │Frontend │ │  API   │ │   DB   │     (rich local sensing
   │         │ │        │ │        │      + recent raw data)
   │ Local   │ │ Local  │ │ Local  │  ← Agent storage:
   │ Store   │ │ Store  │ │ Store  │     Recent high-res data
   └───┬─────┘ └──┬─────┘ └──┬─────┘     (~1 hour raw metrics)
       │          │          │
   ┌───▼─────┐ ┌──▼─────┐ ┌──▼─────┐
   │Frontend │ │  API   │ │   DB   │  ← Your app components
   │(React)  │ │(Node)  │ │(Postgres)   (run normally)
   └──┬──────┘ └──┬─────┘ └──┬─────┘
      │           │          │
   [Metrics] [Health] [Connections]  ← Local observation

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
TIER 1: ↑ Self-sufficient (works standalone, air-gapped)
TIER 2: ↓ Optional enrichment (when available)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

   Optional: External MCP Integrations (for depth)
   ┌────────────────────────────────────────┐
   │  [Grafana MCP]  - Long-term metrics    │
   │  [Sentry MCP]   - Error grouping       │
   │  [PagerDuty]    - Incident history     │
   │  [Custom MCPs]  - Your internal tools  │
   └────────────────────────────────────────┘
```

**Key insight**: The mesh is **self-aware from local data alone**. External MCPs add depth, not core intelligence.

### How the System Becomes Sentient

**1. Continuous Sensing (The Nerves)**

Agents run on every host, collecting **rich local data** (exact details TBD, see Open Questions):

```
Agent observes locally (every 10-30s):
├─ Process metrics (from /proc or equivalent)
│  ├─ CPU, memory, file descriptors, threads
│  ├─ Process restarts, crashes, exit codes
│  └─ Version info (SDK or binary inspection)
│
├─ Network connections (netstat/ss)
│  ├─ Active connections → builds dependency graph
│  ├─ Connection rates, errors, bandwidth
│  └─ Discover service topology automatically
│
├─ Application health
│  ├─ Passive: HTTP health endpoint polling
│  └─ SDK: Component-level health (DB, cache, etc.)
│
└─ Metrics collection (optional)
   ├─ Scrape Prometheus /metrics endpoints
   ├─ Send to colony for DuckDB storage
   └─> To be refined: What metrics? Which format?
```

**Sent to colony**: Compressed summaries every 10-60s depending on change rate.
**Agent stores locally**: Recent raw data (~1 hour window) for on-demand queries.

**2. Layered Storage (The Brain + Distributed Memory)**

The system uses a **layered storage architecture** for scalability:

```
┌─────────────────────────────────────────────┐
│  COLONY LAYER (Summaries + History)         │
│  • Cross-agent aggregations                 │
│  • Historical trends and patterns           │
│  • Topology graph (auto-discovered)         │
│  • Learned behavioral baselines             │
│  • Event correlations across services       │
│  • Can query agent layer on-demand          │
└─────────────────────────────────────────────┘
                      ↕
        (Push summaries + pull details)
                      ↕
┌─────────────────────────────────────────────┐
│  AGENT LAYER (Recent Raw Data)              │
│  • High-resolution metrics (~1 hour)        │
│  • Detailed event logs (local services)     │
│  • Process-level observations               │
│  • Network connection details               │
│  • Responds to colony queries               │
└─────────────────────────────────────────────┘
```

**Why Layered Storage?**

- **Scalability** - Colony doesn't store all raw data from every agent
- **Performance** - Recent high-resolution data available on-demand from agents
- **Efficiency** - Agents push compressed summaries, full data stays local
- **Resilience** - Colony operates on stale summaries if agents temporarily offline
- **Natural distribution** - Storage grows with application complexity

**The colony can answer** (from summaries):
- "What crashed and when?" (from summary events)
- "What changed recently?" (from deployment summaries)
- "Is memory/CPU trending up?" (from aggregated time-series)
- "What depends on what?" (from topology graph)
- "Which service is causing cascading failures?" (from correlation analysis)
- "Is this normal behavior?" (from learned baselines)

**When deeper investigation needed**, colony queries relevant agents directly:
- "Show me all errors from API service in the last 10 minutes"
- "What's the exact memory pattern before the crash?"
- "Give me detailed connection lifecycle for this service"

**Speed**: <1 second for summary queries, +network latency for agent detail queries

**3. AI Reasoning (The Consciousness)**

LLM synthesis creates emergent understanding from local data:
- Correlates events: deploy → crash → error spike
- Recognizes patterns: "crash 10min after deploy = likely deployment issue"
- Generates hypotheses, validates with local evidence
- Explains reasoning in natural language
- **No external dependencies** for core intelligence

**4. Optional: Multi-Tool Orchestration (Extended Senses)**

When deeper investigation needed, Coral **optionally** queries external tools via MCP:

```
MCP queries triggered by:
├─ User request: "Check Grafana too"
├─ Low confidence: Need more evidence (future: configurable)
└─> Configuration: mcp.auto_query = true (future feature)

Available MCPs:
├─ Grafana MCP: Long-term metrics (weeks/months), complex queries
├─ Sentry MCP: Error grouping, stack traces, release tracking
├─ PagerDuty MCP: Historical incidents, on-call context
├─ OTEL MCP: Distributed traces (future)
└─ Custom MCPs: Your internal knowledge bases, runbooks
```

**MCPs provide**:
- Historical depth beyond 6hr window
- Specialized analysis (trace waterfalls, error grouping)
- Business context (incident history, team knowledge)
- Enrichment, not replacement of local intelligence

**Speed**: +5-10 seconds (network calls to external services)

**5. Conversational Interface (The Voice)**

Natural language replaces dashboard hunting:

```
You: "Why did checkout fail at 2pm?"

Coral: [investigates autonomously using local data]
       ✓ Checked event log → Stripe SDK v2.0 deployed 13:58
       ✓ Checked topology → Checkout → Stripe connector
       ✓ Checked time-series → Error rate spike at 13:58
       ✓ Checked other services → All healthy

       "Stripe connector started failing at 13:58 (same time as
       v2.0 deployment). Error rate jumped 0.5% → 15%.

       Local evidence suggests deployment issue (85% confidence).

       Want me to check Sentry for error details? [y/N]"

[If you say yes]
       → Queries Sentry MCP

       "Confirmed: Stripe API returns 429 (rate limit). Your v2.0
       added retry logic with exponential backoff, amplifying load.

       Recommendation: Rollback to v1.9 or disable auto-retry."
```

**Key difference**: Initial diagnosis from local data (<1s), MCP enrichment optional (+5-10s).

### Emergent Intelligence: More Than the Sum of Parts

What makes the mesh "alive" isn't any single component - it's **emergent properties** from their combination:

**Emergent Property 1: System-Wide Awareness**
- Single agent: "My service crashed"
- Living mesh: "Payment service crashed 3 min after auth service deployed, causing 15% checkout failure rate, Stripe is healthy, likely connection pool exhaustion"

**Emergent Property 2: Pattern Learning**
- First occurrence: 60% confidence guess
- After 5 similar incidents: 90% confidence, instant recognition
- After 20 deployments: "This deployment looks risky based on pre-deploy metrics"

**Emergent Property 3: Proactive Awareness**
- Traditional: Alert fires when failure already happened
- Living mesh: "Memory trend suggests OOM in 90 minutes, scale now or expect downtime"

**Emergent Property 4: Contextual Understanding**
- Dashboard: "Error rate: 5%"
- Living mesh: "5% error rate is normal for this service during batch jobs (happens 2am daily), but unusual at 2pm - investigating"

### Example: The System Investigates Itself

**Scenario**: API service crashes with OOM

**Phase 1: Local Intelligence (Immediate, <1 second)**

```
[14:10:15] Agent API: "API process died (exit code 137 - OOM killed)"
           └─> Colony: "Anomaly detected"

[14:10:16] Colony queries LOCAL DATA (DuckDB in-memory):
           ├─> Event log: v2.3.0 deployed at 14:00 (10 min ago)
           ├─> Time-series: Memory 250MB→512MB (linear growth, 6hr window)
           ├─> Topology graph: API → DB (healthy), API → Cache (healthy)
           ├─> Other agents: No other services showing issues
           └─> Historical data: v2.2.5 ran stable for 3 days before this

[14:10:16] AI synthesis (from local data):
           "High confidence (85%): Memory leak in v2.3.0

            Evidence (all local):
            • API crashed (OOM) exactly 10 min after v2.3.0 deploy
            • Memory grew linearly 250→512MB over 10 minutes
            • v2.2.5 showed stable 250MB for 3 days
            • Other services: memory stable
            • Dependencies: DB and cache both healthy

            Recommendation: Rollback to v2.2.5"
```

**You interact** (conversational):
```
$ coral ask "What happened to the API?"

💬 Coral: "The API service crashed with OutOfMemoryError 3 minutes ago.
           This is caused by a memory leak in v2.3.0 (85% confidence).

           I've already investigated (from local data):
           ✓ Event log → v2.3.0 deployed 10 min ago
           ✓ Time-series → linear memory growth 250→512MB
           ✓ History → v2.2.5 stable at 250MB for 3 days
           ✓ Topology → DB and cache healthy
           ✓ Other services → all stable

           Recommendation: Rollback to v2.2.5

           Want me to check external tools for more context? [y/N]"
```

**Total time: <1 second** from crash to diagnosis (all local data).

---

**Phase 2: MCP Enrichment (Optional, +5-10 seconds)**

If you want more detail:

```
You: "y"

[14:10:17] Coral queries external MCPs (optional enrichment):
           ├─> Sentry MCP: "Exceptions in api service"
           │   └─> Returns: 47 OutOfMemoryError exceptions
           │       └─> Stack traces point to connection pooling code
           │
           ├─> PagerDuty MCP: "Similar incidents?"
           │   └─> Returns: Similar issue 3 months ago in v2.1.0
           │       └─> Also connection pool, fixed in commit abc123
           │
           └─> Grafana MCP: "Compare to last week's deploy metrics"
               └─> Returns: v2.2.5 deploy showed no memory growth

[14:10:18] Enhanced synthesis:
           "Confirmed (95% confidence): Connection pool leak in v2.3.0

            Additional evidence from external tools:
            • Sentry: 47 OOM exceptions, all in connection pool code
            • PagerDuty: Similar incident 3 months ago (also pool leak)
            • Grafana: Last week's deploy (v2.2.5) showed normal pattern

            This looks like a regression of the v2.1.0 bug."

💬 Coral: "Updated diagnosis: Connection pool leak (95% confidence).

           This is likely a regression - happened before in v2.1.0,
           fixed in commit abc123. Recommend reviewing that fix.

           Execute rollback? [y/N]"
```

**Total time: ~6 seconds** with MCP enrichment.

---

**Key Insight**:

**Local intelligence gave 85% confidence in <1 second** - enough to act on.

**MCP enrichment raised it to 95% and added context** - nice to have, but not required.

**The mesh investigated itself autonomously from local data alone.** External MCPs added depth, not core intelligence.

---

## What Makes Coral Different

| Aspect              | Traditional Monitoring     | Coral                                     |
|---------------------|----------------------------|-------------------------------------------|
| **Data Collection** | Manual setup, many agents  | Rich local sensing (auto-discovery)       |
| **Intelligence**    | You correlate manually     | Mesh is self-aware from local data        |
| **Dependencies**    | Requires full tool stack   | Self-sufficient (air-gap compatible)      |
| **Speed**           | Multi-tool querying (slow) | Local inference (<1s via DuckDB)          |
| **Root Cause**      | You investigate for hours  | AI explains in seconds                    |
| **Storage**         | Separate systems           | DuckDB (in-memory + optional persistence) |
| **Recommendations** | You decide what to do      | AI suggests with evidence                 |
| **Actions**         | You execute commands       | AI prepares commands, you approve         |
| **Learning**        | You remember patterns      | AI recognizes patterns automatically      |
| **Proactive**       | Reactive alerts            | Proactive detection from baselines        |

**Analogy**:
- Traditional monitoring = Raw sensor data + you as correlator
- Coral = Living infrastructure that understands itself

**Key Differentiator**: Local-first intelligence means Coral works standalone. External MCPs add depth, not core value.

---

## What Makes Coral Different

### Competitive Landscape

The observability and operations tooling space is crowded, but Coral occupies a unique position:

**Existing Tools**:
- **Dynatrace Davis AI**: Advanced AI, but vendor lock-in and proprietary platform
- **Datadog**: Comprehensive, but SaaS-only and expensive at scale
- **Service Meshes (Istio, Linkerd)**: Traffic management, but IN data path (can break apps)
- **AIOps (Moogsoft, BigPanda)**: Alert correlation, but proprietary integrations
- **Grafana**: Multi-source visualization, but no AI

### Coral's Unique Combination

**No existing tool combines**:
1. ✅ **Self-sufficient intelligence** (local-first, air-gap compatible)
2. ✅ **Agentic AI** (autonomous analysis + supervised execution)
3. ✅ **Optional MCP enrichment** (works standalone, MCPs add depth)
4. ✅ **Optional SDK** (passive works, SDK enhances)
5. ✅ **Control plane only** (can't impact app performance)
6. ✅ **User-controlled** (self-hosted, your AI keys, your data)

### Why This Matters

| What Users Want | Dynatrace | Datadog | Service Mesh | Grafana | **Coral** |
|-----------------|-----------|---------|--------------|---------|-----------|
| **AI-powered insights** | ✅ Advanced | ⚠️ Basic | ❌ None | ❌ None | ✅ **Agentic** |
| **Works with existing tools** | ❌ Replaces | ❌ Replaces | ⚠️ Adds to | ✅ Queries | ✅ **Orchestrates** |
| **User controls data/AI** | ❌ SaaS | ❌ SaaS | ✅ Self-host | ✅ Self-host | ✅ **Your infrastructure** |
| **Can't break apps** | ⚠️ Agent risk | ⚠️ Agent risk | ❌ **In data path** | ✅ Query only | ✅ **Control plane only** |
| **Standards-based** | ❌ Proprietary | ⚠️ Some OTEL | ⚠️ Some | ✅ Open | ✅ **MCP + OTEL + Prometheus** |

### Positioning

**"GitHub Copilot for Distributed Systems Operations"**

Like GitHub Copilot:
- AI assistant, not replacement
- Suggests solutions, human approves
- Works with existing tools
- Transparent recommendations

Unlike monitoring tools:
- Proactive (not waiting for dashboards)
- Natural language (ask questions)
- Multi-source (orchestrates tools)
- Agentic (investigates automatically)

---

## Key Innovations

### 1. Self-Sufficient Local Intelligence with Layered Storage
- Rich local data collection (agents observe deeply, not just metrics)
- **Layered storage architecture** for horizontal scalability
  - Agents: Recent raw data (~1 hour) locally stored
  - Colony: Summaries and historical patterns across agents
  - On-demand detail retrieval via bidirectional queries
  - Graceful degradation when agents temporarily offline
- Fast analytical queries from colony summaries (<1s)
- Deep investigation via direct agent queries (as needed)
- Auto-discovered topology from network connections
- Learned baselines for anomaly detection
- **Works standalone, air-gap compatible** - no external dependencies required

### 2. Local-First, MCP-Optional Architecture
- **Tier 1 (Core)**: Self-sufficient from local data alone (<1s inference)
- **Tier 2 (Optional)**: MCP enrichment when needed (Grafana, Sentry, etc.)
- Coral IS an MCP client (queries external tools when configured)
- Coral IS an MCP server (exports topology/events to Claude Desktop, etc.)
- **Key principle**: Local intelligence first, MCP for depth

### 3. Agentic Intelligence Layer
- Not passive monitoring (shows dashboards)
- Not full automation (blind execution)
- **Agentic with supervision** (intelligent assistant that asks permission)

### 4. Control Plane Separation
- Agents observe locally (netstat, /proc, health checks)
- Never proxy/intercept app traffic
- Separate encrypted control mesh for coordination
- Zero impact on application performance

### 5. Trust Evolution
- Start: "Tell me what's happening" (observation)
- Then: "Tell me what to do" (recommendations)
- Later: "Handle routine stuff autonomously" (graduated autonomy - optional)

## Design Constraints

**Must Have:**
- ✅ **Self-sufficient intelligence** from local data alone (no external dependencies)
- ✅ **Air-gap compatible** - works offline, no SaaS required
- ✅ Control plane only (never in data path)
- ✅ User-controlled colony (your infrastructure, your AI keys)
- ✅ Fast inference (<1s from local data)
- ✅ Human-in-the-loop for actions
- ✅ Works anywhere (VMs, containers, K8s, edge, bare metal, **laptops**)
- ✅ **Application-scoped** - one colony per app, not infrastructure-wide

**Must NOT:**
- ❌ Require external tools/MCPs to function (MCPs are optional enhancements)
- ❌ Become another full observability platform (DuckDB for local intelligence, MCPs for depth)
- ❌ Replace existing tools (Grafana, Sentry, etc. - we enhance, not replace)
- ❌ Require service mesh or sidecar proxies
- ❌ Auto-execute dangerous operations without approval
- ❌ Send user data to our cloud

**Nice to Have:**
- ⚠️ Web dashboard
- ⚠️ Local AI models (cost savings)
- ⚠️ Multi-colony federation (**Reef architecture**)
- ⚠️ Graduated autonomy (Phase 5+)

## What This Isn't

**Infrastructure:**
- ❌ Not a metrics database (use Prometheus/OTEL)
- ❌ Not a log aggregator (use ELK/Loki)
- ❌ Not a service mesh (use Istio/Linkerd)
- ❌ Not a full automation platform (too risky)
- ❌ Not a SaaS (user-controlled)
- ❌ Not K8s-only (works everywhere)

**SDK:**
- ❌ Not a replacement for existing instrumentation
- ❌ Not a custom metrics protocol (use Prometheus)
- ❌ Not a tracing system (use OpenTelemetry)
- ❌ Not required to use Coral (passive works)
- ❌ Not tightly coupled to app logic (discovery + structured data only)

**Coral is**: The intelligence layer that ties everything together.
