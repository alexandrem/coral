
## Agentic Architecture

Coral is an **agentic AI system** that autonomously observes, analyzes, and recommends - but requires human approval for actions. This "trust-first" design balances autonomous intelligence with operational safety.

### What Makes Coral Agentic?

**Autonomous Observation**:
```
Coral Agents (Continuous Operation)
  ↓
Monitor applications 24/7
  ↓
Detect changes without human intervention
  ↓
Report to colony automatically
```

**Autonomous Analysis**:
```
Colony receives events
  ↓
Detects anomalies using pattern recognition
  ↓
Orchestrates multiple MCP servers
  ↓
Synthesizes insights using AI
  ↓
Generates recommendations proactively
```

**Human-in-the-Loop Execution**:
```
User reviews AI recommendations
  ↓
User approves/rejects/modifies
  ↓
System executes with explicit consent
  ↓
Feedback loop improves future recommendations
```


### Architecture Layers

```
┌─────────────────────────────────────────────────────────────┐
│         AGENTIC INTELLIGENCE LAYER (Autonomous)             │
│                                                             │
│  ┌────────────────────────────────────────────────────┐   │
│  │  1. Continuous Observation (Coral Agents)          │   │
│  │     • Monitor apps 24/7                            │   │
│  │     • Detect deploys, crashes, restarts            │   │
│  │     • Track network topology changes               │   │
│  │     • No human intervention required               │   │
│  └────────────────────────────────────────────────────┘   │
│                          ↓                                  │
│  ┌────────────────────────────────────────────────────┐   │
│  │  2. Proactive Analysis (AI Orchestrator)           │   │
│  │     • Anomaly detection (restarts, latency)        │   │
│  │     • Multi-tool orchestration via MCP             │   │
│  │     • Pattern recognition across time/services     │   │
│  │     • Root cause correlation                       │   │
│  └────────────────────────────────────────────────────┘   │
│                          ↓                                  │
│  ┌────────────────────────────────────────────────────┐   │
│  │  3. Recommendation Generation (AI Synthesis)       │   │
│  │     • Explain "why" in natural language            │   │
│  │     • Suggest actionable next steps                │   │
│  │     • Rank by confidence and impact                │   │
│  │     • Provide runnable commands                    │   │
│  └────────────────────────────────────────────────────┘   │
│                                                             │
└──────────────────────────┬──────────────────────────────────┘
                           │
                ┏━━━━━━━━━━▼━━━━━━━━━━┓
                ┃    TRUST GATE        ┃  ← Human decision point
                ┃ (Human-in-the-Loop)  ┃
                ┗━━━━━━━━━━┬━━━━━━━━━━┛
                           │
┌──────────────────────────▼───────────────────────────────────┐
│         PASSIVE EXECUTION LAYER (User-Controlled)            │
│                                                              │
│  ┌────────────────────────────────────────────────────┐    │
│  │  4. User Review                                     │    │
│  │     • Read AI analysis and recommendations          │    │
│  │     • Understand evidence and confidence level      │    │
│  │     • Ask follow-up questions                       │    │
│  │     • Validate against own knowledge                │    │
│  └────────────────────────────────────────────────────┘    │
│                          ↓                                   │
│  ┌────────────────────────────────────────────────────┐    │
│  │  5. Explicit Approval                               │    │
│  │     • Approve: Execute recommendation               │    │
│  │     • Reject: Dismiss and explain why               │    │
│  │     • Modify: Adjust and then execute               │    │
│  │     • Defer: Save for later consideration           │    │
│  └────────────────────────────────────────────────────┘    │
│                          ↓                                   │
│  ┌────────────────────────────────────────────────────┐    │
│  │  6. Controlled Execution                            │    │
│  │     • Run commands with user's approval             │    │
│  │     • Track what was executed and results           │    │
│  │     • Learn from outcomes (future: feedback loop)   │    │
│  └────────────────────────────────────────────────────┘    │
│                                                              │
└──────────────────────────────────────────────────────────────┘
```

### Agentic Capabilities

| Capability | Current (Phase 1-4) | Future (Phase 5+) |
|------------|---------------------|-------------------|
| **Observe** | ✅ Autonomous | ✅ Autonomous |
| **Detect Anomalies** | ✅ Autonomous | ✅ Autonomous |
| **Analyze Root Cause** | ✅ Autonomous (via MCP orchestration) | ✅ Autonomous |
| **Generate Insights** | ✅ Autonomous (proactive) | ✅ Autonomous |
| **Recommend Actions** | ✅ Autonomous | ✅ Autonomous |
| **Execute Actions** | ❌ Human approval required | ⚠️ Optional autonomy (if trusted) |
| **Learn from Feedback** | ❌ Future | ⚠️ Planned (reinforcement) |
| **Adapt Goals** | ❌ User-defined only | ⚠️ Maybe (with constraints) |

### Example: Agentic Intelligence in Action

**Scenario**: API deployment causes memory leak

**Autonomous Detection** (no human involved):
```
14:00 UTC - Agent detects: api v2.3.0 deployed
14:10 UTC - Agent detects: api restarted (OOM)
14:10 UTC - Colony detects: anomaly (restart within 10 min of deploy)
14:11 UTC - AI orchestrates:
              ├─ Query Coral: deployment events
              ├─ Query Grafana MCP: memory metrics
              ├─ Query Sentry MCP: error logs
              └─ Synthesize via Claude
14:11 UTC - Insight generated: "Memory leak in v2.3.0" (90% confidence)
14:11 UTC - Recommendation: "Rollback to v2.2.5"
```

**Human Decision Point**:
```bash
$ coral insights

⚠️  NEW INSIGHT (detected 30 seconds ago)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Memory leak in api v2.3.0 (90% confidence)

EVIDENCE:
  ✓ OOM crash 10 minutes after deploy
  ✓ Memory grew 250MB → 512MB linearly
  ✓ 47 OutOfMemoryError exceptions

RECOMMENDATION:
  Rollback api to v2.2.5

Execute now? [y/N] █
```

User reviews, approves, action executes. **AI is agentic in analysis, human in execution.**

### Trust Evolution Path

Coral's autonomy can increase as user trust builds:

**Phase 1-2 (Current)**: Trust in Observation
- ✅ Users trust agents to observe accurately
- ✅ Users trust events are recorded correctly
- Goal: Build confidence in data quality

**Phase 3-4**: Trust in Analysis
- ✅ Users trust AI root cause analysis
- ✅ Users trust recommendations are reasonable
- Goal: AI accuracy >90%, users act on insights regularly

**Phase 5+ (Future)**: Trust in Execution (Optional)
- ⚠️ Users opt-in to autonomous actions for specific scenarios
- ⚠️ Constraints and guardrails prevent dangerous operations
- ⚠️ Always with rollback capability and audit logging

**Graduated Autonomy**:
```yaml
# Example future config: graduated autonomy
autonomy:
  level: supervised  # manual | supervised | autonomous

  # What AI can do without asking
  auto_execute:
    # Safe operations only
    - action: scale_up
      conditions:
        - cpu_threshold: 80%
        - max_instances: 10
        - within_budget: true

    # Never auto-execute dangerous operations
    - action: rollback
      auto_execute: false  # always ask
      reason: "impacts production"

  # Safety constraints
  safety:
    require_approval_if:
      - affects_production: true
      - cost_impact: "> $100/day"
      - data_loss_risk: true

    never_auto_execute:
      - delete_*
      - drop_*
      - force_*
```

### Why This Hybrid Approach?

**Operational Safety**:
- AI can be wrong - human oversight prevents disasters
- Complex systems have context AI might miss
- Regulations may require human decision-makers

**Trust Building**:
- Users need to see AI is reliable before granting autonomy
- Gradual capability increase reduces adoption friction
- Explicit approval creates feedback loop for learning

**Flexibility**:
- Power users can enable more autonomy
- Risk-averse users stay in manual mode
- Different autonomy levels per action type

**Auditability**:
- Every action has clear human authorization
- Compliance requirements met (SOC2, HIPAA, etc.)
- Post-incident analysis shows decision chain

### Comparison to Other Agentic Systems

| System | Observation | Analysis | Execution | Category |
|--------|-------------|----------|-----------|----------|
| **Coral** | Autonomous | Autonomous | Human-approved | Agentic intelligence, supervised execution |
| **Kubernetes HPA** | Autonomous | Rule-based | Autonomous | Automated, not agentic (no reasoning) |
| **PagerDuty** | Manual alerts | Manual | Manual | Traditional monitoring (not agentic) |
| **GitHub Copilot** | Context-aware | Autonomous | Human-approved | Agentic for code, supervised |
| **Self-driving (L4)** | Autonomous | Autonomous | Autonomous (with limits) | Fully agentic within constraints |

Coral is similar to GitHub Copilot's model: **agentic intelligence with human-in-the-loop execution**.
