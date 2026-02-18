# Coral

**Root cause in seconds, not hours.**

The open-source AI debugger for distributed apps.

[![CI](https://github.com/alexandrem/coral/actions/workflows/ci.yml/badge.svg)](https://github.com/alexandrem/coral/actions/workflows/ci.yml)
[![Golang](https://img.shields.io/github/go-mod/go-version/alexandrem/coral?color=7fd5ea)](https://golang.org/)
[![Go Report Card](https://goreportcard.com/badge/github.com/alexandrem/coral)](https://goreportcard.com/report/github.com/alexandrem/coral)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

> 🚧 **Very early & experimental** — The vision is solid, the APIs are shifting.

> **TL;DR**: Coral gives AI assistants "hands" inside your running code.
> Ask: "Why is the payment service slow?" — Coral deploys eBPF probes, profiles
> live code, and returns the exact bottleneck, down to the line.

## What is Coral?

Coral gives your AI assistant direct access to your running distributed system
— without instrumentation, redeployment, or hunting through dashboards.

Connect any AI (Claude, Cursor, or the built-in `coral ask` CLI) to your mesh, and it can investigate your system in real time.

### AI-Powered Investigation

Coral agents collect eBPF metrics and profiles across every host and expose them
as tools the AI can call:

- Profile this service
- Trace this function
- Capture this request

The AI reasons over live, correlated data and returns a root cause — not a list
of graphs to interpret.

It also understands your code. Coral indexes service binaries so the AI can
locate *the function that handles Stripe webhooks* even if you don’t know
the exact symbol name.

### Direct Tool Access (No AI Required)

Because the AI calls real tools under the hood, you can use them directly.

From your terminal, you can:

- Run commands inside a service’s namespace (via nsenter)
- Inspect processes or read files as the container sees them
- Open a shell on the agent host
- Capture live traffic
- Trigger on-demand CPU or memory profiles

No redeploys. No dashboard spelunking. No AI required.

## See It in Action

### Diagnosing a latency spike

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

   Root Cause: Synchronous logging to a slow disk volume is blocking the main
   execution thread.
```

### Tracking down a memory leak

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

   Root Cause: cache.Store retains entries indefinitely, causing unbounded
   memory growth.
   Recommendation: Add TTL-based eviction or size-based LRU policy.
```

## How It Works

Agents run alongside your services and collect telemetry continuously via eBPF
— zero code changes required. The Colony aggregates data across all agents and
exposes it as tools over MCP. Your AI uses those tools to investigate, correlate,
and reason its way to a root cause.

**👁️ Observe** — Zero-config RED metrics, host health, continuous CPU and memory
profiling (<1% overhead), and automatic dependency mapping.

**🔍 Explore** — On-demand profiling with flame graphs, remote execution, shell
access, and live traffic capture across any agent.

**🤖 Diagnose** — Pre-correlated metrics and profiling summaries for instant LLM
reasoning, with automatic regression detection across deployments.

Each agent keeps a rolling window of high-resolution data (a few hours by
default). The Colony aggregates summaries across the mesh for historical
comparisons and regression detection. Unlike tools built around infinite
retention, Coral is specialized for live investigation — full fidelity where
it matters, near-zero storage cost everywhere else.

> [!NOTE]
> Function-level argument tracing requires the **Coral SDK**. CPU/memory
> profiling and system metrics work agentlessly on any binary.

## What Makes Coral Different?

| Traditional Observability                       | Coral                                             |
|-------------------------------------------------|---------------------------------------------------|
| Dashboards, alerts, and log search              | On-demand investigation tools                     |
| You interpret metrics and traces                | AI (or you) invoke profiling and tracing directly |
| Requires pre-instrumentation                    | Works on running services via eBPF                |
| Optimized for retention and historical analysis | Optimized for live debugging                      |
| You ask “what looks wrong?”                     | You ask “why is this happening?”                  |

## Architecture

Colony acts as an MCP server — any AI assistant can query your observability
data in real-time.

```
┌─────────────────────────────────────────────────────────────────┐
│  Your AI Assistant                                              │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐           │
│  │ Claude       │  │ VS Code /    │  │ coral ask    │           │
│  │ Desktop      │  │ Cursor       │  │ (terminal)   │           │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘           │
│         └─────────────────┴─────────────────┘                   │
└──────────────────────────┬──────────────────────────────────────┘
                           │ MCP Protocol
                           ▼
                  ┌────────────────────┐
                  │  Colony            │
                  │  (Control Plane)   │
                  └─────────┬──────────┘
                            │ Encrypted Mesh (WireGuard + gRPC)
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

- **Decentralized**: You run the Colony on your own infrastructure — laptop, VM,
  or Kubernetes. No central Coral cloud service.
- **Bring Your Own LLM**: Your API keys (OpenAI, Anthropic, Google) stay on your
  machine. Or use Ollama for a fully air-gapped setup.
- **Encrypted Mesh**: All traffic between hosts is secured via WireGuard.

## Quick Start

```bash
# Build
make build

# Initialize and run
bin/coral init my-colony
bin/coral colony start   # terminal 1
bin/coral agent start    # terminal 2

# Ask
bin/coral ask config     # configure your LLM (first time)
bin/coral ask "Why is the API slow?"
```

To observe specific services explicitly (default: all services on the host):

```bash
bin/coral connect frontend:3000 api:8080:/health
```

## Documentation

- **[Installation & Permissions](docs/INSTALLATION.md)**: Setup guide and security options.
- **[CLI Reference](docs/CLI_REFERENCE.md)**: Complete command reference.
- **[Architecture](docs/ARCHITECTURE.md)**: Deep dive into the system design.
- **[Live Debugging](docs/LIVE_DEBUGGING.md)**: How on-demand instrumentation works.
- **[Instrumentation](docs/INSTRUMENTATION.md)**: How to instrument your code with the SDK.

## License

Apache 2.0
