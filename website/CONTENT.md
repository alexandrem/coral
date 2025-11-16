# Coral Landing Page - Content

This file contains all the textual content from the landing page for easy editing and iteration.

---

## Hero Section

### Badge
🚧 Early Development

### Headline
LLM-orchestrated debugging for **distributed apps**

### Subtitle
Turn fragmented infrastructure into one intelligent system. Natural language queries, AI-powered analysis, live debugging across your entire mesh.

### Terminal Demo - Basic Example

```
$ coral ask "What's wrong with the API?"

🤖 Analyzing...

API latency spiked 3 minutes ago. P95 went from 150ms to 2.3s.
95% of time spent in db.QueryOrders()
Query doing sequential scan of 234k rows.
Missing index on orders.user_id (85% confidence)

Recommendation:
CREATE INDEX idx_orders_user_id ON orders(user_id);

⏱️  <1 second analysis using your own LLM
```

---

## Stats Section

| Metric | Label |
|--------|-------|
| <1s | Root cause analysis |
| Zero | Code changes required |
| 100% | Your infrastructure, your AI |
| ∞ | Environments supported |

---

## Problem Section

### Heading
The Problem

### Subtitle
Your app runs across fragmented infrastructure: laptop, VMs, Kubernetes clusters, multiple clouds, VPCs, on-prem.

### Pain Points

**Debug an issue**
Check logs, metrics, traces across multiple dashboards

**Find the root cause**
Add logging, redeploy, wait for it to happen again

**Debug across environments**
Can't correlate laptop dev with prod K8s cluster

**Run diagnostics**
SSH to different networks, navigate firewalls, VPN chaos

### Solution Banner
Coral unifies this with an **Application Intelligence mesh**
One CLI to observe, debug, and control your distributed app

---

## Features Section

### Heading
One Interface for Everything

### Feature 1: Observe
👁️ **Observe**

Four complementary mechanisms provide complete visibility:
- **eBPF probes:** Zero-config RED metrics (no code changes)
- **OTLP ingestion:** For apps using OpenTelemetry
- **Shell/exec:** Run diagnostic tools
- **Connection mapping:** Auto-discovered service dependencies

### Feature 2: Debug
🐛 **Debug**

Ask questions in natural language using your own LLM:
- **Live debugging** with on-demand instrumentation
- **Attach eBPF uprobes** to running code without redeploying
- **LLM orchestrates** where to probe based on analysis
- **Zero overhead** when not debugging

### Feature 3: Control
🎛️ **Control**

Act on insights from a single interface:
- **Traffic inspection:** Sample and inspect live requests
- **Profiling:** On-demand CPU/memory profiling
- **Live probes:** Attach/detach debugging hooks on-demand
- **All from a single binary** - no complex setup

---

## Differentiators Section

### Heading
What Makes Coral Different?

### Subtitle
The first LLM-orchestrated debugging mesh for distributed apps

### Differentiator Cards

**01 - Unified Mesh Across Infrastructure**
Debug apps running on laptop ↔ AWS VPC ↔ GKE cluster ↔ on-prem VM with the same commands. No VPN config, no firewall rules, no per-environment tooling.

**02 - On-Demand Live Debugging**
Attach eBPF uprobes to running code without redeploying. LLM decides where to probe based on analysis. Zero overhead when not debugging.

**03 - You Own the AI**
Use your own LLM API keys (OpenAI/Anthropic/Ollama). We never see your data or telemetry. You control the model, costs, and data.

**04 - Decentralized Architecture**
No Coral servers to depend on. Colony runs wherever you want: laptop, VM, Kubernetes. Your observability data stays local.

**05 - Control Plane Only**
Can't break your apps, zero baseline overhead. Probes only when debugging. Mesh is for orchestration, never touches data plane.

**06 - Application-Scoped**
One mesh per app (not infrastructure-wide monitoring). Scales from single laptop to multi-cloud production.

---

## How It Works Section

### Heading
How It Works

### Subtitle
From observability to insights - a complete journey through Coral's architecture

### Flow Step 1: Observe Everywhere

**Heading:** Observe Everywhere

**Description:** Progressive integration levels - start with zero-config, add capabilities as needed

**Level 0 - eBPF Probes**
📡 eBPF Probes
Zero-config RED metrics · No code changes required

**Level 1 - OTLP Ingestion**
🔭 OTLP Ingestion
Rich traces if using OpenTelemetry · Optional

**Level 2 - Shell/Exec**
⚡ Shell/Exec
LLM-orchestrated diagnostic commands · Auto-enabled

**Level 3 - SDK Live Probes**
🎯 SDK Live Probes
On-demand instrumentation · Full control

**Connector:** Agents collect locally

### Flow Step 2: Aggregate Intelligently

**Heading:** Aggregate Intelligently

**Description:** Colony receives and stores data from all agents across your distributed infrastructure

**Features:**
- → **DuckDB storage** for fast analytical queries
- → **Cross-agent correlation** discovers dependencies
- → **Encrypted mesh** connects fragmented infrastructure

**Connector:** MCP Server exposes tools

### Flow Step 3: Analyze with AI

**Heading:** Analyze with AI

**Description:** Colony exposes MCP tools for AI-powered analysis using your own LLM

**Features:**
- → **MCP Server** exposes topology, metrics, and diagnostic tools
- → **Your LLM** (OpenAI/Anthropic/Ollama) - you own the AI
- → **Claude Desktop** or custom AI clients via MCP protocol

**Connector:** Insights delivered

### Flow Step 4: Act on Insights

**Heading:** Act on Insights

**Description:** Get actionable recommendations in natural language, execute with approval

**Features:**
- → **Root cause analysis** in <1 second
- → **Actionable recommendations** with evidence
- → **Human-approved execution** for safety

### Live Debugging Example

**Heading:** See It In Action: Live Debugging with SDK

**Subtitle:** When basic metrics aren't enough, Coral automatically escalates to live instrumentation

**Terminal Demo - Advanced Example:**

```
$ coral ask "Why is the payment API slow?"

🤖 Analyzing payment service metrics...
   P95 latency: 2.3s (baseline: 150ms)

   Root cause unclear from metrics. Attaching live probes...

   ✓ Uprobe attached: payment.ProcessPayment() [offset 0x4a20]
   ✓ Uprobe attached: payment.ValidateCard() [offset 0x4c80]
   ✓ Uprobe attached: db.QueryTransactions() [offset 0x3f10]

   Collecting traces for 30 seconds...

   Analysis:
     • ProcessPayment(): 2.1s avg (2,847 calls)
       └─ db.QueryTransactions(): 2.0s (95% of time)
          └─ Query plan: Sequential scan (234,891 rows)
          └─ Missing index on transactions.user_id

     • ValidateCard(): 12ms avg (normal)

   Root Cause: Missing database index causing slow queries

   Recommendation:
   CREATE INDEX idx_transactions_user_id ON transactions(user_id);

   Detaching probes...
   ✓ Cleanup complete (zero overhead restored)
```

**Explanation Note:**
**What just happened?** Coral used eBPF metrics to detect the issue, then automatically attached live uprobes to running code (Level 3 integration). After collecting data, it identified the exact bottleneck and recommended a fix—all without redeploying or restarting anything.

### Architecture Summary

**Heading:** Three-Tier Architecture

**Colony (🧠)**
Central coordinator with MCP server, DuckDB storage, and AI orchestration

**Agents (👁️)**
Local observers using eBPF, OTLP, and shell commands to gather telemetry

**SDK (⚙️) - Optional**
Advanced features like live probes and runtime instrumentation

**Note:** All connected via an encrypted mesh that works across any network boundary.

---

## Get Started Section

### Heading
Get Started in Minutes

### Step 1: Install Coral
```
# macOS / Linux
brew install coral

# Or download from GitHub Releases
```

### Step 2: Start the Colony
```
coral colony start
```

### Step 3: Start Agent & Connect Services
```
coral agent start
coral connect frontend:3000 api:8080
```

### Step 4: Ask Questions
```
coral ask "What's happening with the API?"
```

### CTA Banner

**Heading:** Ready to get started?

**Description:** Coral is open source and in early development. Join us in building the future of distributed operations.

**Buttons:**
- Star on GitHub
- Read the Docs

---

## Footer

**Tagline:** Application Intelligence Mesh

**Navigation Links:**
- Features
- How It Works
- Docs
- GitHub
- Concept
- Issues
- License

**Copyright:** © 2024 Coral

---

## Content Guidelines

### Voice & Tone
- **Direct and confident** - No hedging, clear value propositions
- **Technical but accessible** - Explain complex concepts simply
- **Actionable** - Focus on what users can do
- **Honest** - Acknowledge "Early Development" status

### Key Messages
1. **Zero-config starting point** - Works without code changes
2. **Progressive integration** - Add capabilities as needed (Levels 0-3)
3. **User owns the AI** - Your LLM keys, your data, your infrastructure
4. **Unified across fragmented infrastructure** - Works everywhere via WireGuard mesh
5. **Live debugging without redeployment** - Unique value proposition

### Terminology
- **Colony** - Central coordinator (not "server" or "controller")
- **Agents** - Local observers (not "daemons" or "workers")
- **Mesh** - WireGuard network connecting everything
- **MCP** - Model Context Protocol (for AI integration)
- **OTLP** - OpenTelemetry Protocol
- **eBPF** - Extended Berkeley Packet Filter
- **Uprobes** - User-space probes for live debugging

### Content Principles
- Lead with benefits, not features
- Use concrete examples (terminal demos)
- Show progression (Level 0 → Level 3)
- Emphasize control and ownership
- Contrast with existing solutions (no VPN, no complex setup)
