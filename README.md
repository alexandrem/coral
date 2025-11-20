# Coral

**Application Intelligence Mesh**

LLM-orchestrated debugging for distributed apps. Observe, analyze, and
instrument your code on-demand.

[![CI](https://github.com/alexandrem/coral-mesh/actions/workflows/ci.yml/badge.svg)](https://github.com/alexandrem/coral-mesh/actions/workflows/ci.yml)

🚧 **Early Development / Design Phase** - Implementation in progress

## Overview

Coral turns fragmented infrastructure into one intelligent system. It provides a
unified interface to observe, debug, and control your distributed applications,
whether they run on your laptop, in Kubernetes, or across multiple clouds.

- **Unified Mesh**: Connects laptop ↔ AWS ↔ On-prem via WireGuard.
- **AI-Powered**: Natural language queries using **your own LLM** (
  OpenAI/Anthropic/Ollama).
- **Live Debugging**: On-demand eBPF instrumentation without redeploying.
- **Decentralized**: No vendor lock-in, no telemetry sent to us.

## The 4 Levels of Observability

Coral integrates four layers of data collection to provide complete visibility:

| Level | Feature             | Description                                                                           |
|-------|---------------------|---------------------------------------------------------------------------------------|
| **0** | **eBPF Probes**     | Zero-config RED metrics (Rate, Errors, Duration). No code changes.                    |
| **1** | **OTLP Ingestion**  | Ingests traces/metrics from apps already using OpenTelemetry.                         |
| **2** | **Shell/Exec**      | LLM-orchestrated diagnostic tools (`netstat`, `curl`, `grep`) for deep investigation. |
| **3** | **SDK Live Probes** | On-demand dynamic instrumentation (uprobes) attached to running code.                 |

```
You: "What's wrong with the API?"

Coral: "API latency spiked 3 minutes ago. P95 went from 150ms to 2.3s.
       95% of time spent in db.QueryOrders(). Query doing sequential
       scan of 234k rows. Missing index on orders.user_id (85% confidence).

       Recommendation: CREATE INDEX idx_orders_user_id ON orders(user_id);"

⏱️  <1 second analysis using your own LLM (OpenAI/Anthropic/Ollama)
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

### 4. Connect

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

## Live Debugging: The Killer Feature

**Coral can debug your running code without redeploying.**

Unlike traditional observability (metrics, logs, traces), Coral can **actively
instrument** your code on-demand using eBPF uprobes.

### Example: LLM-Orchestrated Debugging

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

### How It Works

1. **SDK Integration**: `coral.EnableRuntimeMonitoring()` bridges with the
   agent.
2. **On-Demand Probes**: Agent attaches eBPF uprobes to function entry points.
3. **Live Data**: Captures args, execution time, and call stacks.
4. **Zero Overhead**: Probes only exist during debugging sessions.

See [Live Debugging Docs](docs/LIVE_DEBUGGING.md) for details.

## CLI Examples

> NOTE: The CLI is currently in development and subject to change.
> Some of the following commands are not yet implemented or may not work as
> expected.

### Colony & Agent

```bash
coral colony start --daemon           # Start colony in background
coral agent start --colony-id prod    # Start agent for specific colony
coral connect api:8080 redis:6379     # Connect multiple services
```

### AI Queries

```bash
coral ask "Show me the service dependencies"
coral ask "Are there any errors in the frontend?"
coral ask "What changed in the last hour?"
```

### Manual Diagnostics

```bash
# Run tools on remote agents
coral exec api "netstat -an | grep ESTABLISHED"
coral exec database "iostat -x 5 3"

# Manual live debugging
coral debug attach api --function handleCheckout --duration 60s
```

See [CLI Reference](docs/CLI_REFERENCE.md) for all commands.

## What Makes Coral Different?

| Feature          | Coral                                                 | Traditional Tools             |
|------------------|-------------------------------------------------------|-------------------------------|
| **Network**      | **Unified WireGuard Mesh** (Laptop ↔ Cloud ↔ On-prem) | VPNs, Firewalls, Fragmented   |
| **Debugging**    | **On-demand eBPF** (Live instrumentation)             | Logs, Metrics, Redeploying    |
| **AI Model**     | **Bring Your Own LLM** (You own the data)             | Vendor-hosted, Privacy risks  |
| **Architecture** | **Decentralized** (No central SaaS)                   | Centralized SaaS / Data Silos |

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
