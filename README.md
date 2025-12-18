# Coral

**Application Intelligence Mesh**

LLM-orchestrated debugging for **distributed apps**. Turn fragmented
infrastructure into one intelligent system.

[![CI](https://github.com/alexandrem/coral/actions/workflows/ci.yml/badge.svg)](https://github.com/alexandrem/coral/actions/workflows/ci.yml)
[![Golang](https://img.shields.io/github/go-mod/go-version/alexandrem/coral?color=7fd5ea)](https://golang.org/)
[![Go Report Card](https://goreportcard.com/badge/github.com/alexandrem/coral)](https://goreportcard.com/report/github.com/alexandrem/coral)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

> [!NOTE]
> 🚧 **Early Development** - Implementation in progress

## The Problem

Your app runs across fragmented infrastructure: laptop, VMs, Kubernetes
clusters, multiple clouds, VPCs, on-prem.

- **Debug an issue?** Check logs, metrics, traces across multiple dashboards.
- **Find the root cause?** Add logging, redeploy, wait for it to happen again.
- **Debug across environments?** Can't correlate laptop dev with prod K8s
  cluster.
- **Run diagnostics?** SSH to different networks, navigate firewalls, VPN chaos.

**Coral unifies this with an Application Intelligence Mesh.** One CLI to
observe, debug, and control your distributed app.

## One Interface for Everything

Coral integrates four layers of data collection to provide complete visibility:

| Level | Feature                 | Description                                                                     |
| ----- | ----------------------- | ------------------------------------------------------------------------------- |
| **0** | **Passive RED Metrics** | Zero-config service metrics (Rate, Errors, Duration) via eBPF. No code changes. |
| **1** | **External Telemetry**  | Ingests traces/metrics from apps already using OpenTelemetry/OTLP.              |
| **2** | **Continuous Intel**    | Always-on host metrics (CPU/Mem/Disk) and low-overhead continuous profiling.    |
| **3** | **Deep Introspection**  | On-demand profiling, function-level tracing, and active investigation.          |

### 👁️ Observe

**Passive, always-on data collection.**

Coral automatically gathers telemetry from your applications and infrastructure without any
configuration.

- **Zero-config eBPF**: Metrics for every service, instantly.
- **Host Health**: Continuous monitoring of CPU, memory, disk, and network.
- **Continuous Profiling**: Low-overhead background CPU profiling to identify hot paths over time.
- **Dependency Mapping**: Automatically discovers how services connect.

### 🔍 Explore

**Deep introspection and investigation tools.**

When you need to dig deeper, Coral gives you the tools to investigate actively or automate the discovery of hotspots.

- **Remote Execution**: Run standard tools like `netstat`, `curl`, and `grep` on any agent.
- **Remote Shell**: Jump into any agent's shell.
- **On-Demand Profiling**: High-frequency CPU profiling with Flame Graphs for line-level analysis.
- **Live Debugging**: Attach eBPF uprobes (SDK) to specific functions to capture args and return values.
- **Traffic Capture**: Sample live requests to understand payload structures.

### 🤖 Diagnose

**AI-powered insights for intelligent Root Cause Analysis (RCA).**

Coral's killer app is its ability to pre-correlate metrics and profiling data into structured summaries that LLMs can understand instantly.

- **Profiling-Enriched Summaries**: AI gets metrics + code-level hotspots in one call.
- **Regression Detection**: Automatically identifies performance shifts across deployment versions.
- **Built-in Assistant**: Use `coral ask` directly from your terminal.
- **Universal AI integration**: Works with Claude Desktop, IDEs, any MCP client.

## Architecture: Universal AI Integration via MCP

Colony acts as an MCP server - any AI assistant can query your observability
data in real-time.

```
┌─────────────────────────────────────────────────────────────────┐
│  External AI Assistants / coral ask                             │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐           │
│  │ Claude       │  │ VS Code /    │  │ coral ask    │           │
│  │ Desktop      │  │ Cursor       │  │ (terminal)   │           │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘           │
│         │ Anthropic       │ OpenAI          │ Ollama            │
│         └─────────────────┴─────────────────┘                   │
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
└─────┬─────┘                             └─────┬─────┘
      │                                         │
┌─────▼─────┐                             ┌─────▼─────┐
│ Service A │                             │ Service B │
│ (+ SDK)   │                             │ (No SDK)  │
└───────────┘                             └───────────┘
```

## 🔒 Privacy & Sovereignty

Coral is designed for **complete data sovereignty**.

- **Decentralized**: You run the Colony (control plane) on your own
  infrastructure—laptop, VM, or Kubernetes.
- **No SaaS Dependency**: There is no central Coral cloud service. You don't
  send us any data.
- **Bring Your Own LLM**: Your API keys (OpenAI, Anthropic, Google) stay on your
  machine. Or use local models (Ollama) for an air-gapped experience.
- **Encrypted Mesh**: All traffic between your laptop, colony, and agents is
  secured via WireGuard.

## How It Works

1. **Observe Everywhere**: Agents collect telemetry via eBPF (zero-config) and
   OTLP.
2. **Aggregate Intelligently**: Colony receives data, stores it in DuckDB, and
   correlates dependencies.
3. **Query with AI**: Connect any MCP client (Claude, IDE) to ask questions in
   natural language.
4. **Act on Insights**: Get root cause analysis and recommendations.

## Live Debugging & Profiling

**Coral can debug your running code without redeploying.**

Unlike traditional observability (metrics, logs, traces), Coral can **actively
instrument** your code on-demand using eBPF uprobes and high-frequency CPU
profiling.

> [!NOTE]
> Detailed function-level tracing requires integrating the **Coral Language
> Runtime SDK**, while CPU profiling and system metrics work
> **agentlessly** on any binary.

```bash
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

   Root Cause: Missing database index causing slow queries

   Recommendation:
     CREATE INDEX idx_transactions_user_id ON transactions(user_id);

   Detaching probes...
   ✓ Cleanup complete (zero overhead restored)
```

## Quick Start

### 1. Build

```bash
make build-dev
```

### 2. Initialize

```bash
# Initialize the colony configuration
bin/coral init my-colony
```

### 3. Run

```bash
# Start the colony (central coordinator)
bin/coral colony start

# In another terminal, start the agent
bin/coral agent start
```

### 4. Connect (optional)

Connect the agent explicitly to your services to observe them.

By default, the agent will observe all services it can find on the system

```bash
# Connect agent to observe services
bin/coral connect frontend:3000 api:8080:/health
```

### 5. Ask

```bash
# Configure your LLM (first time only)
bin/coral ask config

# Ask questions
bin/coral ask "Why is the API slow?"
```

## What Makes Coral Different?

| Feature          | Coral                                                 | Traditional Tools             |
| ---------------- | ----------------------------------------------------- | ----------------------------- |
| **Network**      | **Unified WireGuard Mesh** (Laptop ↔ Cloud ↔ On-prem) | VPNs, Firewalls, Fragmented   |
| **Debugging**    | **Continuous & On-demand eBPF** (Profiling & Probes)  | Logs, Metrics, Profiling.     |
| **AI Model**     | **Bring Your Own LLM** (You own the data)             | Vendor-hosted, Privacy risks  |
| **Architecture** | **Decentralized** (No central SaaS)                   | Centralized SaaS / Data Silos |
| **Analysis**     | **LLM-Driven RCA** (Pre-correlated hotspots)          | Manual Dashboard Diving       |

**This doesn't exist in the market.**

Coral is the first tool that combines:

- LLM-driven analysis
- On-demand eBPF instrumentation
- Distributed debugging
- Zero standing overhead

## Documentation

- **[Installation & Permissions](docs/INSTALLATION.md)**: Setup guide and
  security options.
- **[CLI Reference](docs/CLI_REFERENCE.md)**: Complete command reference.
- **[Architecture](docs/ARCHITECTURE.md)**: Deep dive into the system
  architecture.
- **[Design](docs/DESIGN.md)**: High-level design principles.
- **[Live Debugging](docs/LIVE_DEBUGGING.md)**: How the on-demand
  instrumentation works.

## License

Apache 2.0
