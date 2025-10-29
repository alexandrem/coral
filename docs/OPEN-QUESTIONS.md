
## Open Questions

### Product

1. **Is the AI actually useful enough?**
    - Need to validate with real users
    - What's the minimum bar for "useful recommendation"?
    - How often will it be wrong?

2. **Do people want another agent running?**
    - "Agent fatigue" is real
    - Is <10MB overhead acceptable?
    - Can we make it opt-in per service?

3. **Self-hosted vs SaaS first?**
    - Self-hosted: Easier adoption, harder to monetize
    - SaaS: Easier to monetize, trust/compliance hurdles
    - Current plan: Self-hosted first, but is this right?

4. **What's the minimum viable feature set?**
    - Mesh + discovery + basic status? (too basic?)
    - Mesh + discovery + AI insights? (right?)
    - Mesh + discovery + AI + control? (too much?)

### Business

5. **How to monetize without turning evil?**
    - Open core? (often feels like bait-and-switch)
    - Hosting? (commodity, hard to differentiate)
    - AI markup? (fair value exchange?)
    - Support? (doesn't scale well)

6. **Should discovery service be monetized?**
    - Currently: Free, we absorb cost
    - Could charge for SLA/redundancy
    - Or always free to drive adoption?

7. **What's the path to sustainability?**
    - Need revenue before Phase 4
    - But too early monetization kills adoption
    - What's the right timeline?

### User Research Needed

8. **Would you run this?**
    - Interview potential users
    - What's the key value for them?
    - What concerns do they have?

9. **What existing tools would this replace?**
    - If answer is "none, this is additive" → problem
    - Need to replace something to justify adoption

10. **What's your tolerance for AI mistakes?**
    - 90% accuracy good enough?
    - Must be 99%+?
    - Depends on the recommendation?

11. **Where would you run the colony?**
    - Laptop? (not production-safe)
    - Small VPS? (maintenance burden)
    - Kubernetes? (complex)
    - Prefer SaaS? (trust issues)


## Open Questions for Brainstorming

### Local Data Collection (Critical to Define)

**The mesh's intelligence depends on what agents collect locally. Need to refine:**

- [ ] **What metrics exactly?**
    - Just CPU/memory/connections or also I/O, network bytes, syscalls?
    - Do we scrape Prometheus /metrics endpoints by default or only if configured?
    - Which metrics give best signal-to-noise for anomaly detection?

- [ ] **What DuckDB schema for time-series?**
    - Wide table (one row per timestamp, columns per metric)?
    - Narrow table (metric-value pairs)?
    - Hybrid (separate tables per metric type)?
    - How to handle high-cardinality labels (service, host, etc.)?
    - Built-in DuckDB compression should handle 6hr windows efficiently

- [ ] **Collection frequency vs. overhead?**
    - Every 10s? 30s? 60s? Adaptive based on change rate?
    - How much memory per agent (6hr window × metrics × frequency)?
    - Target: <10MB RAM per agent, <0.1% CPU

- [ ] **What granularity for network connections?**
    - Just "service A → service B" or include ports, protocols, byte counts?
    - Track connection lifecycle (new, established, closed)?
    - How to handle high-churn connections (load balancers, CDNs)?

- [ ] **How to detect deployments/version changes?**
    - SDK provides explicit version (best)
    - Passive: parse from process labels, environment vars, binary hashes?
    - What if version info unavailable (how to detect "something changed")?

- [ ] **Baseline learning: what algorithm?**
    - DuckDB has built-in window functions, percentile_cont(), stddev()
    - Simple moving average + stddev?
    - Percentile-based (p50, p95, p99)? ← DuckDB handles this natively
    - Time-aware (weekday vs weekend patterns)?
    - How long to establish baseline (1 day? 1 week?)?
    - Can leverage SQL for statistical analysis (no custom code needed)

- [ ] **Event log structure?**
    - What events: deploys, crashes, restarts, config changes, scale events?
    - How to capture cause/effect (this deploy → that crash)?
    - DuckDB table schema: event_type, timestamp, service_id, metadata (JSON)?
    - Retention: 6hr in-memory by default, optionally persist for longer history?

**This section needs refinement before implementation starts.**

### Product Direction
- [ ] Should we focus on incident response or also proactive optimization?
- [ ] Is "agentic AI" the right positioning or too buzzword-heavy?
- [ ] Do we need a web dashboard or is CLI + MCP export enough?
- [ ] What's the MVP feature set that provides value?

### SDK Integration
- [ ] Is 5-minute integration realistic or will it take longer?
- [ ] How do we message "optional but you should use it"?
- [ ] Should SDK discovery be automatic or require explicit config?
- [ ] What % of users will actually integrate SDK vs. staying passive?
- [ ] Do we build SDK before or after validating passive observation?
- [ ] Should agent poll SDK or use streaming/push?

### Agentic Capabilities
- [ ] How much autonomy is "too much" for initial release?
- [ ] Should agents ever talk to each other or always through colony?
- [ ] Can AI learn from user feedback (approve/reject) to improve?
- [ ] What actions should NEVER be automated (even optionally)?

### MCP Integration (Optional Enhancement)
- [ ] Which MCP servers should we integrate with first? (Grafana, Sentry, PagerDuty?)
- [ ] Should we build MCP servers for common tools or wait for ecosystem?
- [ ] When to trigger MCP queries automatically vs. user-requested?
    - Low confidence threshold? (<80% = query MCPs for more evidence?)
    - Configurable: `mcp.auto_query = true/false`?
    - Always ask user first: "Want me to check Sentry?"
- [ ] How to handle MCP server failures gracefully?
    - Degrade to local-only intelligence (system still works)
    - Cache MCP responses for offline resilience?
- [ ] Can Coral act as a "smart router" between multiple MCP servers?
- [ ] How much value do MCPs really add over local intelligence?
    - Need to measure: does 85% local → 95% with MCPs justify complexity?

### Trust & Safety
- [ ] What metrics prove AI is trustworthy (accuracy, user acceptance, etc.)?
- [ ] How to prevent users from over-trusting AI?
- [ ] What guardrails for graduated autonomy (Phase 5)?
- [ ] How to make decisions auditable for compliance?

### Deployment Model
- [ ] Colony per application (single-tenant) or multi-app colonies?
- [ ] Laptop deployment for dev → VPS/cloud for production workflow?
- [ ] How to handle colony availability (HA needed for production)?
- [ ] Should we offer hosted colonies or self-host only?
- [ ] **Reef architecture**: When do users need multi-colony federation (multiple apps? multiple environments?)

### Differentiation
- [ ] Is "self-sufficient local intelligence + optional MCP enrichment" the right positioning?
- [ ] How to explain "works standalone, air-gap compatible" value?
- [ ] Do we need unique capabilities beyond local intelligence + MCP orchestration?
- [ ] Should we focus on specific use cases (K8s, microservices, edge, air-gapped)?
- [ ] How to explain value to someone happy with Datadog?
    - "Datadog requires their SaaS, Coral works offline with your own AI"
    - "Datadog replaces your tools, Coral enhances them"
- [ ] What's the elevator pitch for "living mesh"?
