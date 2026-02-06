# Coral

**Root cause in seconds, not hours.**

The open-source nervous system for your distributed apps.

[![CI](https://github.com/alexandrem/coral/actions/workflows/ci.yml/badge.svg)](https://github.com/alexandrem/coral/actions/workflows/ci.yml)
[![Golang](https://img.shields.io/github/go-mod/go-version/alexandrem/coral?color=7fd5ea)](https://golang.org/)
[![Go Report Card](https://goreportcard.com/badge/github.com/alexandrem/coral)](https://goreportcard.com/report/github.com/alexandrem/coral)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

> 🚧 **Very early & experimental** — APIs will change, but the vision is solid.

> TL;DR: Ask your running system “why is it slow?” in plain English → get the exact
> function and line blocking it, without redeploying.

## Overview

Coral is the **Application Intelligence Mesh**. It brings the "observe
everything" philosophy of DTrace to distributed systems, replacing esoteric
scripts with natural language and sandboxed TypeScript. It unites the deep,
kernel-level visibility of eBPF with the reasoning power of Large Language
Models.

Unlike traditional tools that just show you dashboards, Coral is
building a Programmable Observability Engine capable of writing and deploying
its own safe, ephemeral diagnostics to solve problems faster than any human can
type.

## The Problem: Observability is Fragmented and Passive

Modern distributed applications run across a "chaos of environments" — laptops,
Kubernetes clusters, edge nodes, and multiple clouds. Current tools fail this
reality in three ways:

1. **The Context Gap**: Metrics tell you _that_ something is wrong, but not
   _where_ in the code. You’re forced to jump between dashboards, traces, and
   source code, manually trying to correlate timestamps.
2. **The "Observer Effect"**: To get deeper data, you often have to add logging,
   redeploy, and pray the issue happens again. This is slow, risky, and often
   changes the very behavior you’re trying to debug.
3. **Passive Data, Active Toil**: Traditional tools are passive collectors. They
   wait for you to ask the right question. In a distributed mesh, finding the "
   right question" is 90% of the work.

**Coral turns this upside down.** We provide the **depth of a kernel debugger**
with the **reasoning of an AI**, unified into a single intelligence mesh.

## One Interface for Everything

Coral integrates four layers of data collection to provide complete visibility:

| Level | Feature                 | Description                                                                           |
| ----- | ----------------------- | ------------------------------------------------------------------------------------- |
| **0** | **Passive RED Metrics** | Zero-config service metrics (Rate, Errors, Duration) via eBPF. No code changes.       |
| **1** | **External Telemetry**  | Ingests traces/metrics from apps already using OpenTelemetry/OTLP.                    |
| **2** | **Continuous Intel**    | Always-on host metrics (CPU/Mem/Disk) and low-overhead continuous CPU/memory profiling. |
| **3** | **Deep Introspection**  | On-demand CPU/memory profiling, function-level tracing, and active investigation.     |

### 👁️ Observe

**Passive, always-on data collection.**

Coral automatically gathers telemetry from your applications and infrastructure
without any configuration.

- **Zero-config eBPF**: Metrics for every service, instantly.
- **Host Health**: Continuous monitoring of CPU, memory, disk, and network.
- **Continuous Profiling**: Low-overhead background CPU and memory profiling to identify
  hot paths and allocation hotspots over time (<1% overhead).
- **Dependency Mapping**: Automatically discovers how services connect.

### 🔍 Explore

**Deep introspection and investigation tools.**

When you need to dig deeper, Coral gives you the tools to investigate actively
or automate the discovery of hotspots.

- **Remote Execution**: Run standard tools like `netstat`, `curl`, and `grep` on
  any agent.
- **Remote Shell**: Jump into any agent's shell.
- **On-Demand Profiling**: High-frequency CPU and memory profiling with Flame Graphs for
  line-level analysis. Track allocation hotspots, memory leaks, and GC pressure.
- **Live Debugging**: Attach eBPF uprobes (SDK) to specific functions to capture
  args and return values.
- **Traffic Capture**: Sample live requests to understand payload structures.

### 🤖 Diagnose

**AI-powered insights for intelligent Root Cause Analysis (RCA).**

Coral's killer app is its ability to pre-correlate metrics and profiling data
into structured summaries that LLMs can understand instantly.

- **Profiling-Enriched Summaries**: AI gets metrics + code-level hotspots in one
  call.
- **Regression Detection**: Automatically identifies performance shifts across
  deployment versions.
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
instrument** your code on-demand using eBPF uprobes, high-frequency CPU
profiling, and memory allocation tracking.

> [!NOTE]
> Detailed function-level tracing requires integrating the **Coral Language
> Runtime SDK**, while CPU/memory profiling and system metrics work
> **agentlessly** on any binary.

### CPU Profiling Example

```bash
$ coral ask "Why is the payment API slow?"

🤖 Analyzing host metrics and continuous profiles...
   Host: api-v1-pod-abc (CPU: 12%, Mem: 45%)
   Service: payment-api (P95: 2.3s)

   Initial findings: High "Off-CPU" wait time detected in process.
   Executing coral_profile_functions (strategy: critical_path)...

   Analysis of 30s capture:
     • ProcessPayment() total: 2.1s
       └─ Mutex Contention: 1.8s (Blocked by Logger.Write)
       └─ VFS Write (Disk I/O): 1.7s (Wait on /var/log/app.log)

   Root Cause: Synchronous logging to a slow disk volume is blocking the main execution thread.
```

### Memory Profiling Example

```bash
$ coral ask "Why is the order-processor using 10GB of RAM?"

🤖 Analyzing host metrics and continuous memory profiles...
   Host: worker-node-5 (CPU: 18%, Mem: 85% - 10.2GB)
   Service: order-processor (Heap growth: +200MB/hour)

   Memory leak detected. Analyzing allocation patterns...
   Executing coral_profile_memory...

   Top Memory Allocators (30s sample):
     • cache.Store:       45.2% (523 MB/s)
       └─ Allocation type: map[string]interface{}
       └─ No TTL-based eviction detected
     • json.Marshal:      22.1% (256 MB/s)
     • http.(*conn).serve: 12.3% (143 MB/s)

   GC Correlation: High GC CPU overhead (28%) caused by cache allocation rate.

   Root Cause: cache.Store retains entries indefinitely, causing unbounded memory growth.
   Recommendation: Add TTL-based eviction or size-based LRU policy.
```

## What Makes Coral Different?

| Feature          | Coral                                                        | Traditional Tools             |
| ---------------- | ------------------------------------------------------------ | ----------------------------- |
| **Network**      | **Unified WireGuard Mesh** (Laptop ↔ Cloud ↔ On-prem)        | VPNs, Firewalls, Fragmented   |
| **Debugging**    | **Continuous & On-demand eBPF** (CPU/Memory Profiling & Probes) | Logs, Metrics, Profiling.     |
| **AI Model**     | **Bring Your Own LLM** (You own the data)                    | Vendor-hosted, Privacy risks  |
| **Architecture** | **Decentralized** (No central SaaS)                          | Centralized SaaS / Data Silos |
| **Analysis**     | **LLM-Driven RCA** (Pre-correlated hotspots)                 | Manual Dashboard Diving       |

**Nothing else does this yet.**

Coral is the first tool that combines:

- LLM-driven analysis with pre-correlated CPU and memory hotspots
- On-demand eBPF instrumentation
- Continuous CPU and memory profiling with <1% overhead
- Distributed debugging across any environment
- Zero standing overhead with intelligent sampling

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

## Documentation

- **[Installation & Permissions](docs/INSTALLATION.md)**: Setup guide and
  security options.
- **[CLI](docs/CLI.md)**: Command-line interface guide.
- **[CLI Reference](docs/CLI_REFERENCE.md)**: Complete command reference.
- **[Architecture](docs/ARCHITECTURE.md)**: Deep dive into the system
  architecture.
- **[Config](docs/CONFIG.md)**: Configuration guide.
- **[Design](docs/DESIGN.md)**: High-level design principles.
- **[Live Debugging](docs/LIVE_DEBUGGING.md)**: How the on-demand
  instrumentation works.
- **[Instrumentation](docs/INSTRUMENTATION.md)**: How to instrument your code.

## License

Apache 2.0
