# Coral - Development Roadmap

**Version**: 0.1 (Design Phase)
**Last Updated**: 2025-10-27

---

## Overview

This document outlines the development roadmap for Coral, organized into phases with clear deliverables and success criteria. Each phase builds on the previous one, with opportunities to validate assumptions before moving forward.

---

## Phase 1: Core Mesh (MVP) - 6-8 weeks

### Goal
Prove the basic concept works - establish a reliable control plane mesh for agent connectivity and basic observation.

### Deliverables
- [x] Discovery service (HTTP server, registration/lookup)
- [x] Coordinator with Wireguard mesh
- [x] Agent connects and sends heartbeats
- [x] Basic CLI: `coordinator start`, `connect`, `status`
- [x] Simple event tracking (deploys, restarts)
- [x] SQLite storage
- [x] **SDK design** (protobuf spec, gRPC interface)
- [x] **Go SDK implementation** (Tier 1: Health, Info, Config)
- [x] **Agent SDK discovery** (tries SDK, falls back to passive)

### Success Criteria
- Can connect 10+ apps across different networks
- Mesh stays stable for 24+ hours
- Agent overhead <10MB RAM, <0.1% CPU
- Setup time <5 minutes
- **SDK integration takes <5 minutes**
- **Passive observation works without SDK**

### Not in Phase 1
- No AI yet (just data collection)
- No web dashboard (CLI only)
- No advanced correlation
- **No eBPF** (passive + SDK only)

---

## Phase 2: Basic Intelligence - 8-10 weeks

### Goal
Add AI insights that are actually useful - demonstrate value beyond basic monitoring.

### Deliverables
- [x] AI integration (Anthropic/OpenAI)
- [x] `coral ask` natural language queries
- [x] Basic anomaly detection (restart frequency, resource spikes)
- [x] Simple recommendations (rollback, scale, investigate)
- [x] Event correlation (deploy → crash)
- [x] Daily summary insights
- [x] **SDK adoption metrics** (% of apps using SDK)
- [x] **Compare passive vs. SDK insights** (measure value)

### Success Criteria
- AI correctly identifies root cause 70%+ of time
- Recommendations are actionable
- Responses in <5 seconds
- Cost <$1/day per user on average
- **SDK users get 90%+ confidence** (vs. 60% passive)
- **Users see clear value in SDK integration**

### Phase 2a: Pattern Learning
- Traffic patterns (daily, weekly)
- Normal resource usage baselines
- Deployment success patterns

---

## Phase 3: Rich Experience - 6-8 weeks

### Goal
Make it delightful to use - turn a useful tool into something people love.

### Deliverables
- [x] Web dashboard with topology visualization
- [x] Real-time updates (WebSocket)
- [x] Better CLI with TUI (Bubble Tea)
- [x] Dependency graph from network connections
- [x] Timeline view of events
- [x] Export capabilities (JSON, metrics)
- [x] **SDK Tier 2 features** (custom health checks, enhanced metadata)
- [x] **Python/Java SDKs** (if demand validated in Phase 2)

### Success Criteria
- Dashboard loads in <1s
- Topology graph auto-updates
- CLI feels polished and responsive
- **30%+ apps using SDK** (adoption target)
- **Python/Java SDKs available** (if community wants them)

---

## Phase 4: Advanced Intelligence - 8-10 weeks

### Goal
Proactive and predictive - anticipate problems before they occur.

### Deliverables
- [x] Multi-service correlation (cascading failures)
- [x] Predictive insights (capacity planning)
- [x] Impact analysis ("what if I deploy this?")
- [x] Historical pattern matching
- [x] Security observations (unusual connections)
- [x] Local models for fast, cheap analysis
- [x] **Optional: Agent eBPF introspection** (Tier 2 - privileged, opt-in)

### Success Criteria
- Can predict common issues before they happen
- Identifies cascading failures across services
- Suggests capacity changes before hitting limits
- **eBPF provides value without security concerns** (if enabled)

---

## Phase 5: Graduated Autonomy (Future) - TBD

### Goal
**Enable optional autonomous execution** for trusted scenarios - transitioning from "agentic intelligence with human approval" to "graduated autonomy with constraints."

### Critical Decision Point

**This phase only proceeds if Phase 2-4 demonstrates:**
- ✅ AI recommendation accuracy >90%
- ✅ Users regularly accept and act on recommendations
- ✅ No major incidents caused by following AI advice
- ✅ Strong user demand for automation

**If these criteria are NOT met**: Stay in supervised mode (Phase 1-4) indefinitely. There's no shame in being a great assistant that requires human approval.

### Graduated Autonomy Model

Instead of "all or nothing" automation, enable **granular autonomy levels**:

```yaml
# Users control autonomy per action type
autonomy:
  level: supervised  # manual | supervised | autonomous

  # Safe operations that can run without approval
  auto_execute:
    - action: scale_up
      max_instances: 10
      cost_limit: "$50/day"
      auto_approve: true

    - action: restart_unhealthy_service
      max_restarts: 3
      requires_confirmation_after: 1
      auto_approve: true

  # Dangerous operations always require human
  never_auto_execute:
    - rollback  # impacts production traffic
    - scale_down  # could cause outages
    - delete_*  # data loss risk
    - force_*  # override safety checks

  # Safety constraints
  guardrails:
    - no_changes_during_incidents
    - require_rollback_capability
    - production_requires_approval
    - cost_impact_over_100_needs_approval
```

### Deliverables

**5.1: Supervised Automation (Trust Building)**
- [ ] User can approve actions via CLI/dashboard
- [ ] Executed actions are logged with full audit trail
- [ ] AI learns from user approvals/rejections (feedback loop)
- [ ] "Suggested automation" based on frequently approved actions

**5.2: Constrained Autonomy (Opt-In)**
- [ ] Users can enable auto-execution for specific action types
- [ ] Extensive guardrails and safety checks
- [ ] Always with rollback capability
- [ ] Automatic pause if error rate increases

**5.3: Advanced Features (If Trust Continues)**
- [ ] Feature flags (progressive rollouts)
- [ ] Canary deployment automation
- [ ] Predictive scaling (scale before traffic arrives)
- [ ] Self-healing capabilities (restart, rollback)
- [ ] **SDK Tier 3: eBPF self-introspection** (Go/Rust only, optional)
- [ ] **SDK Tier 3: Circuit breakers, retries** (Dapr-like patterns)

### Success Criteria

**Phase 5.1** (Supervised Automation):
- Users execute 50%+ of AI recommendations
- 90%+ of executed recommendations solve the problem
- Zero incidents caused by executing AI recommendations
- Positive user feedback: "I wish this could run automatically"

**Phase 5.2** (Constrained Autonomy):
- 100+ users opt-in to autonomous actions
- Auto-executed actions have 95%+ success rate
- Users report time savings (>5 hours/week)
- No rollback of autonomy features due to incidents

**Phase 5.3** (Advanced Features):
- 1000+ users with autonomy enabled
- Feature flags used for 80%+ of deployments
- Self-healing resolves 70%+ of common issues
- Trust level: Users sleep soundly with Coral watching

### Trust Evolution Path

```
Phase 1-2: "I trust Coral to tell me what's happening"
  ↓
Phase 3-4: "I trust Coral to tell me what to do"
  ↓
Phase 5.1: "I approve Coral's suggestions most of the time"
  ↓
Phase 5.2: "I trust Coral to handle routine operations autonomously"
  ↓
Phase 5.3: "I trust Coral to manage my system proactively"
```

### Safety Mechanisms

**Before any autonomous execution**:
1. **Dry-run mode**: Test automation in read-only mode for 30 days
2. **Gradual rollout**: Enable for 1% users, then 10%, then 50%
3. **Circuit breaker**: Auto-disable autonomy if error rate >5%
4. **Audit logging**: Every action logged with full context
5. **Instant rollback**: One command to disable all automation
6. **Human override**: User can always stop/reverse autonomous actions

### What Could Go Wrong?

**Risk: AI makes wrong decision autonomously**
- Mitigation: Extensive testing, gradual rollout, circuit breakers
- Fallback: Instant disable, manual recovery procedures

**Risk: Users over-trust AI and stop paying attention**
- Mitigation: Regular "human in the loop" checks, require periodic review
- Fallback: Automatic escalation for high-impact decisions

**Risk: Regulations prohibit autonomous operations**
- Mitigation: Make autonomy optional, maintain supervised mode
- Fallback: Stay in Phase 4 (perfectly viable product)

**Risk: Bad actor compromises coordinator, runs malicious actions**
- Mitigation: Strong auth, audit logs, action signing
- Fallback: Incident response, security review, strengthen auth

### Alternative: Stay Supervised Forever

**There's a strong case for NEVER building Phase 5:**

**Pros of staying supervised:**
- ✅ No risk of autonomous mistakes
- ✅ Compliance friendly (human always in control)
- ✅ Users always understand what's happening
- ✅ Clear responsibility (human approved it)

**Cons:**
- ❌ Users must be available to respond
- ❌ Slower response to incidents
- ❌ Doesn't realize full AI potential

**Decision**: Let Phase 2-4 adoption and user feedback guide this. If users are happy with supervised mode, there's no need to push autonomy.

### Decision Timeline

- **End of Phase 2**: Assess AI recommendation quality
- **End of Phase 3**: Measure user acceptance rate
- **End of Phase 4**: Decide go/no-go for Phase 5
- **Phase 5 is optional**: Success = users trust the supervised assistant

---

## Immediate Next Steps (This Week)

### 1. User Interviews
- Find 5-10 potential users
- Validate problem space
- Get feedback on proposed solution
- Questions to ask:
  - What tools do you currently use for observability?
  - What's your biggest pain point with distributed systems?
  - Would you run another agent on your servers?
  - Where would you run the coordinator?

### 2. Technical Proof of Concept
- Build minimal Wireguard mesh (coordinator + 2 agents)
- Prove networking works across NATs
- Measure actual agent overhead
- Validate core assumptions

### 3. Refine Scope
- Based on interviews, cut non-essential features
- Define clear MVP (what's the minimum useful product?)
- Set success criteria
- Determine go/no-go decision points

---

## Short Term (Next Month)

### 4. Build Phase 1 MVP
- Discovery service implementation
- Basic coordinator with mesh management
- Simple agent with heartbeat
- CLI tools for setup and status

### 5. Alpha Testing
- Deploy on own infrastructure
- Run for 2+ weeks
- Identify stability issues
- Measure performance characteristics

### 6. Community Building
- Set up Discord/GitHub
- Write initial docs
- Create demo videos
- Start building awareness

---

## Medium Term (2-3 Months)

### 7. Phase 2: Add Intelligence
- Integrate AI (start with Anthropic)
- Build recommendation engine
- Test with real scenarios
- Iterate on prompt engineering

### 8. Beta Program
- Invite 20-50 early users
- Gather feedback
- Iterate quickly
- Build case studies

### 9. Polish & Document
- Better error messages
- Comprehensive docs
- Tutorial content
- Getting started guides

---

## Long Term (6+ Months)

### 10. Public Launch
- Open source release
- HN/Reddit launch
- Content marketing
- Conference talks

### 11. Feature Expansion
- Phase 3: Web dashboard
- Phase 4: Advanced intelligence
- Based on user feedback
- Prioritize by value

### 12. Sustainability
- Figure out monetization
- Build business around it
- Or keep as OSS project with sponsors
- Determine long-term viability

---

## Milestones & Gates

### Milestone 1: Working Mesh (End of Phase 1)
**Gate**: Can we reliably connect agents across different networks?
- If NO: Reconsider architecture or networking approach
- If YES: Proceed to Phase 2

### Milestone 2: Useful AI (End of Phase 2)
**Gate**: Do users find the AI insights valuable?
- If NO: Reconsider AI approach or focus on other features
- If YES: Invest in richer experience (Phase 3)

### Milestone 3: Product-Market Fit (End of Phase 3)
**Gate**: Are users actively using Coral and recommending it?
- If NO: Pivot or reassess value proposition
- If YES: Proceed with advanced features and scaling

### Milestone 4: Advanced Intelligence (End of Phase 4)
**Gate**: Is predictive intelligence accurate and trusted?
- If NO: Keep passive, don't build control features
- If YES: Consider control layer (Phase 5)

---

## Resource Requirements

### Phase 1 (MVP)
- **Time**: 6-8 weeks full-time
- **Team**: 1-2 engineers
- **Infrastructure**: Minimal (local dev + small test VPS)
- **Budget**: ~$50/month

### Phase 2 (Intelligence)
- **Time**: 8-10 weeks
- **Team**: 2 engineers
- **Infrastructure**: Test servers + AI API costs
- **Budget**: ~$200/month (AI testing)

### Phase 3 (Experience)
- **Time**: 6-8 weeks
- **Team**: 2-3 engineers (add frontend specialist)
- **Infrastructure**: Same as Phase 2
- **Budget**: ~$200/month

### Phase 4 (Advanced)
- **Time**: 8-10 weeks
- **Team**: 2-3 engineers
- **Infrastructure**: Larger test environment
- **Budget**: ~$500/month (more AI usage)

---

## Success Metrics

### Early Stage (Phase 1-2)
- **Adoption**: 50+ active users
- **Retention**: 30-day retention >40%
- **Reliability**: 99.5% uptime for coordinator
- **Performance**: <5s for AI responses

### Growth Stage (Phase 3-4)
- **Adoption**: 500+ active users
- **Retention**: 30-day retention >60%
- **Engagement**: >50% weekly active users
- **Value**: Users report saving >2 hours/week

### Maturity (Phase 5+)
- **Adoption**: 5000+ active users
- **Community**: Active Discord/GitHub community
- **Revenue**: Sustainable through sponsorships or paid tier
- **Trust**: Users enable control features

---

## Risk Management

### Technical Risks
| Risk | Impact | Mitigation |
|------|--------|------------|
| NAT traversal fails | High | Build fallback relay mechanism |
| Agent overhead too high | High | Aggressive optimization, profile early |
| AI quality insufficient | High | Validate in Phase 2, have fallback plan |
| Scale limits hit early | Medium | Design for scale from start |

### Product Risks
| Risk | Impact | Mitigation |
|------|--------|------------|
| Users don't trust AI | High | Start passive, build trust gradually |
| Agent fatigue | Medium | Make value obvious, minimize overhead |
| Monetization fails | Medium | Open source first, multiple revenue options |
| Too complex vs existing tools | High | Focus on simplicity, clear value prop |

### Market Risks
| Risk | Impact | Mitigation |
|------|--------|------------|
| Big player enters space | Medium | Move fast, differentiate on UX |
| Privacy concerns | High | User-controlled from day 1 |
| Compliance blockers | Medium | Design for compliance (GDPR, SOC2) |

---

## Decision Log

### Decisions Made
- **2025-10-27**: Control plane only architecture (never in data path)
- **2025-10-27**: User-controlled coordinator (no SaaS initially)
- **2025-10-27**: Passive by default (recommend, don't act)
- **2025-10-27**: Go for implementation (networking strengths)

### Decisions Needed
- Which AI provider to prioritize (Anthropic vs OpenAI)?
- Should we support both SQLite and PostgreSQL from day 1?
- What's the minimum viable feature set for MVP?
- When to start monetization discussions?

### Decisions Deferred
- Control layer features (wait for Phase 4 results)
- Self-hosted discovery service (wait for scale issues)
- Multi-coordinator HA (wait for user demand)
- RBAC and multi-user (wait for team use cases)

---

## Open Questions

### Product Questions
1. **Is the AI actually useful enough?** - Need to validate with real users
2. **Do people want another agent running?** - Agent fatigue is real
3. **Self-hosted vs SaaS first?** - Impacts go-to-market strategy
4. **What's the minimum viable feature set?** - Need to define MVP clearly

### Technical Questions
1. **Wireguard vs alternatives?** - Need to test NAT traversal reliability
2. **Local AI models vs API-only?** - Cost vs capability tradeoff
3. **Agent authentication at scale?** - Need scalable security model
4. **Direct connection vs gateway?** - Impacts scaling architecture

### Business Questions
1. **How to monetize without turning evil?** - Must avoid bait-and-switch
2. **Should discovery service be monetized?** - Free vs paid SLA
3. **What's the path to sustainability?** - Timing of monetization
4. **What existing tools would this replace?** - Need clear value prop

---

## User Research Needed

### Key Questions
1. **Would you run this?** - Validate core value proposition
2. **What existing tools would this replace?** - Understand competitive position
3. **What's your tolerance for AI mistakes?** - Set quality bar
4. **Where would you run the coordinator?** - Inform deployment strategy

### Target Interview Candidates
- DevOps engineers at mid-size companies (10-50 services)
- Platform engineers running Kubernetes
- SREs managing distributed systems
- Tech leads responsible for reliability

---

## Related Documents

- **[CONCEPT.md](./CONCEPT.md)** - High-level concept and key ideas
- **[DESIGN.md](./DESIGN.md)** - Design philosophy and architecture
- **[IMPLEMENTATION.md](./IMPLEMENTATION.md)** - Technical implementation details
- **[EXAMPLES.md](./EXAMPLES.md)** - Concrete use cases with MCP orchestration
