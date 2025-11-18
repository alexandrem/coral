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
| Any | AI assistant via MCP |
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

Passive, always-on data collection:
- **Zero-config eBPF metrics:** Rate, Errors, Duration (RED)
- **OTLP ingestion:** For apps using OpenTelemetry
- **Auto-discovered dependencies:** Service connection mapping
- **Efficient storage:** Recent data local, summaries centralized

### Feature 2: Explore
🔍 **Explore**

Human-driven investigation and control:
- **Query data:** Metrics and traces across all services
- **Remote execution:** Run diagnostics (netstat, tcpdump, lsof)
- **Manual probes:** Attach/detach eBPF hooks on-demand
- **Traffic capture:** Sample and inspect live requests
- **On-demand profiling:** CPU/memory analysis in production

### Feature 3: Diagnose
🤖 **Diagnose**

AI-powered insights through standard MCP protocol:
- **Universal AI integration:** Works with Claude Desktop, IDEs, any MCP client
- **Bring your own LLM:** Use your API keys or local models (Ollama)
- **Natural language queries:** Ask questions in plain English, not query languages
- **Real-time data access:** AI queries live observability data, not dashboards
- **Built-in assistant:** coral ask command for terminal-based AI

### Architecture Diagram

**Heading:** Architecture: Universal AI Integration via MCP

**Subtitle:** Colony acts as an MCP server - any AI assistant can query your observability data in real-time

**Diagram:**
```
┌─────────────────────────────────────────────────────────────────┐
│  External AI Assistants / coral ask                            │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐          │
│  │ Claude       │  │ VS Code /    │  │ coral ask    │          │
│  │ Desktop      │  │ Cursor       │  │ (terminal)   │          │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘          │
│         │ Anthropic       │ OpenAI          │ Ollama           │
│         └─────────────────┴─────────────────┘                  │
└─────────────────────────┬───────────────────────────────────────┘
                          │ MCP Protocol (stdio)
                          │ Natural language queries
                          ▼
                 ┌────────────────────┐
                 │  MCP Proxy         │
                 │  (Protocol Bridge) │
                 └─────────┬──────────┘
                           │ gRPC
                           ▼
                 ┌────────────────────┐
                 │  Colony Server     │
                 │  • MCP Server      │
                 │  • Tool Registry   │
                 │  • DuckDB          │
                 └─────────┬──────────┘
                           │ Mesh Network
                           ▼
      ┌────────────────────┴────────────────────┐
      │                                         │
      ▼                                         ▼
┌───────────┐                             ┌───────────┐
│  Agent    │                             │  Agent    │
│  • eBPF   │        ...more agents...    │  • eBPF   │
│  • OTLP   │                             │  • OTLP   │
└───────────┘                             └───────────┘
   (your services)                           (your services)
```

**Diagram Features:**

🔌 **Any MCP Client**
Claude Desktop, IDEs, or custom apps via standard MCP protocol

🔑 **Your LLM, Your Keys**
Use Anthropic, OpenAI, Ollama - you control the AI and costs

⚡ **Real-time Queries**
AI queries live data from Colony's DuckDB, not stale snapshots

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

**03 - Universal AI via MCP**
Works with any AI assistant through standard MCP protocol. Claude Desktop, VS Code, Cursor, or custom apps. Bring your own LLM (Anthropic/OpenAI/Ollama). Your data stays in your infrastructure.

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

### Flow Step 3: Query with AI

**Heading:** Query with AI

**Description:** Colony exposes MCP server for universal AI integration

**Features:**
- → **Works with any MCP client:** Claude Desktop, VS Code, Cursor, custom apps
- → **Bring your own LLM:** Anthropic, OpenAI, or local Ollama
- → **Natural language queries:** "Why is checkout slow?" instead of PromQL
- → **AI orchestrates tool calls:** Queries metrics, traces, topology automatically
- → **Real-time data:** Live observability, not stale dashboards

**Connector:** Insights delivered

### Flow Step 4: Act on Insights

**Heading:** Act on Insights

**Description:** Get actionable recommendations in natural language, execute with approval

**Features:**
- → **Root cause analysis** in <1 second
- → **Actionable recommendations** with evidence
- → **Human-approved execution** for safety

### Architecture Diagram

**Heading:** Architecture: Universal AI Integration via MCP

**Subtitle:** Colony acts as an MCP server - any AI assistant can query your observability data in real-time

**Diagram:**
```
┌─────────────────────────────────────────────────────────────────┐
│  External AI Assistants / coral ask                            │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐          │
│  │ Claude       │  │ VS Code /    │  │ coral ask    │          │
│  │ Desktop      │  │ Cursor       │  │ (terminal)   │          │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘          │
│         │ Anthropic       │ OpenAI          │ Ollama           │
│         └─────────────────┴─────────────────┘                  │
└─────────────────────────┬───────────────────────────────────────┘
                          │ MCP Protocol (stdio)
                          │ Natural language queries
                          ▼
                 ┌────────────────────┐
                 │  MCP Proxy         │
                 │  (Protocol Bridge) │
                 └─────────┬──────────┘
                           │ gRPC
                           ▼
                 ┌────────────────────┐
                 │  Colony Server     │
                 │  • MCP Server      │
                 │  • Tool Registry   │
                 │  • DuckDB          │
                 └─────────┬──────────┘
                           │ Mesh Network
                           ▼
      ┌────────────────────┴────────────────────┐
      │                                         │
      ▼                                         ▼
┌───────────┐                             ┌───────────┐
│  Agent    │                             │  Agent    │
│  • eBPF   │        ...more agents...    │  • eBPF   │
│  • OTLP   │                             │  • OTLP   │
└───────────┘                             └───────────┘
   (your services)                           (your services)
```

**Diagram Features:**

🔌 **Any MCP Client**
Claude Desktop, IDEs, or custom apps via standard MCP protocol

🔑 **Your LLM, Your Keys**
Use Anthropic, OpenAI, Ollama - you control the AI and costs

⚡ **Real-time Queries**
AI queries live data from Colony's DuckDB, not stale snapshots

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
