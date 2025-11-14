# Coral

**Application Intelligence Mesh**

LLM-orchestrated debugging for distributed apps. Observe, analyze, and
instrument your code on-demand.

```
You: "What's wrong with the API?"

Coral: "API crashed with OOM 3 minutes ago. Memory grew 250MB→512MB
       after v2.3.0 deployed 10 min ago. Connection pool leak (85%
       confidence). Recommend rollback to v2.2.5."

You: "Do it"
Coral: "Rolling back API to v2.2.5... Done. Memory stable at 240MB."

⏱️  <1 second analysis using your own LLM (OpenAI/Anthropic/Ollama)
```

## Status

🚧 **Early Development / Design Phase** - Implementation in progress

## The Problem

Your app runs across laptop, VMs, and Kubernetes. When you need to:

- **Debug an issue** → Check logs, metrics, traces across multiple dashboards
- **Find the root cause** → Add logging, redeploy, wait for it to happen again
- **Understand performance** → Set up profilers, SSH to servers, inspect traffic
- **Debug distributed issues** → Correlate data across services manually
- **Profile production** → Either always-on overhead or can't debug when needed

You're juggling terminals, dashboards, and tools. Context is fragmented. **Coral
unifies this.**

## The Solution

Coral gives you **one interface for distributed app operations**:

### 🔍 Observe

See health, connections, and resource usage across all services in one place.

### 🐛 Debug

Ask questions in natural language using your own LLM (OpenAI/Anthropic/Ollama).
Get AI-powered insights from your Colony's observability data.

**Live debugging** with on-demand instrumentation:
- Attach eBPF uprobes to running code without redeploying
- LLM orchestrates where to probe based on analysis
- Zero overhead when not debugging
- Works across your entire distributed app

### 🎛️ Control

- **Traffic inspection**: Sample and inspect live requests
- **Profiling**: Start/stop profilers remotely
- **Rollbacks**: Revert deployments with one command
- **Live probes**: Attach/detach debugging hooks on-demand

All from a single binary. No complex setup. Works on laptop, VMs, or Kubernetes.

## How It Works

Coral offers **two integration levels**:

### Passive Mode (No Code Changes)

Agents observe processes, connections, and health. Get basic observability and
AI debugging without touching your code.

```bash
# Start the colony (central coordinator)
coral colony start

# In another terminal, start the agent daemon
coral agent start

# Connect the agent to observe services
coral connect frontend:3000
coral connect api:8080:/health

# Configure AI (first time only)
coral ask config  # Set up your LLM provider (OpenAI/Anthropic/Ollama)

# Ask questions about your system (uses YOUR LLM)
coral ask "What's happening with the API?"
coral ask "Why is checkout slow?"
coral ask "What changed in the last hour?"
```

**You get:** Process monitoring, connection mapping, AI-powered debugging using
your own LLM.

### SDK-Integrated Mode (Full Control)

Integrate the Coral SDK for live debugging, traffic inspection, profiling, and
runtime monitoring.

```go
// In your application
import "github.com/coral-io/coral-go"

func main() {
	coral.RegisterService("api", coral.Options{
		Port:           8080,
		HealthEndpoint: "/health",
	})

	// Enable runtime monitoring for live debugging
	// Launches goroutine that bridges with agent's eBPF probes
	coral.EnableRuntimeMonitoring()

	// Enable profiling endpoint
	coral.EnableProfiling()
}
```

```bash
# Control from CLI
coral traffic sample api --rate 0.1 --duration 5m
coral traffic inspect api --filter "path=/checkout"

coral profile start api --type cpu --duration 60s
coral profile start frontend --type heap

# Live debugging - attach probes on-demand
coral debug attach api --function handleCheckout --duration 60s
coral debug trace api --path "/api/checkout" --duration 5m

coral rollback api
coral rollback api --to-version v2.2.5
```

**You get:** Full operations control + all passive mode capabilities.

## Live Debugging: The Killer Feature

**Coral can debug your running code without redeploying.**

Unlike traditional observability (metrics, logs, traces), Coral can **actively
instrument** your code on-demand using eBPF uprobes:

### How It Works

1. **SDK Integration**: `coral.EnableRuntimeMonitoring()` launches a goroutine
   that bridges with the agent's eBPF subsystem

2. **On-Demand Probes**: When debugging is needed, the agent attaches eBPF
   uprobes to function entry points in your running process

3. **Live Data Collection**: Capture function calls, arguments, execution time,
   call stacks - all without modifying your code

4. **LLM Orchestration**: The AI decides which functions to probe based on
   metrics analysis. Attach probes → collect data → analyze → detach

5. **Zero Standing Overhead**: Probes only exist during debugging sessions. No
   always-on instrumentation tax.

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

     • ValidateCard(): 12ms avg (normal)

   Root Cause: Missing database index causing slow queries

   Recommendation:
     CREATE INDEX idx_transactions_user_id ON transactions(user_id);

   Detaching probes...
   ✓ Cleanup complete (zero overhead restored)
```

### Why This Is Different

| Traditional Tools | Coral |
|-------------------|-------|
| Pre-defined metrics only | On-demand code instrumentation |
| Add logging → redeploy → wait | Attach probes → get data → detach |
| Always-on overhead | Zero overhead when not debugging |
| Single-process debuggers (delve, gdb) | Distributed debugging across mesh |
| Manual investigation | LLM orchestrates where to probe |

**This doesn't exist in the market.** Coral is the first tool that combines:
- LLM-driven analysis
- On-demand eBPF instrumentation
- Distributed debugging
- Zero standing overhead

### MCP Integration

The live debugging capability is exposed as MCP tools, so any AI assistant (
Claude Desktop, Cursor, etc.) can trigger debugging sessions:

```json
{
  "tool": "coral_debug_attach",
  "arguments": {
    "service": "payment",
    "function": "ProcessPayment",
    "duration": "60s"
  }
}
```

## Architecture

**Three-tier design with separated LLM:**

```
Developer Workstation               Enterprise (Optional)
┌────────────────────┐             ┌──────────────────────┐
│  coral ask         │             │   Reef               │
│  (Local Genkit)    │             │   Multi-colony       │
│                    │             │   Server-side LLM    │
│  Uses your own     │             │   ClickHouse         │
│  LLM API keys      │             │   (Aggregated data)  │
│  (OpenAI/Anthropic │             └──────────┬───────────┘
│   /Ollama)         │                        │
└─────────┬──────────┘                        │ Federation
          │ MCP Client                        │ (WireGuard)
          ▼                                   ▼
         ┌─────────────────────┐    ┌─────────────────────┐
         │   Colony            │◄───┤   Colony            │
         │   MCP Gateway       │    │   MCP Gateway       │
         │   Aggregates data   │    │   (Production)      │
         │   DuckDB/ClickHouse │    │   ClickHouse        │
         └──┬────────┬─────────┘    └─────────────────────┘
            │        │
    ┌───────▼──┐  ┌──▼───────┐
    │ Agent    │  │ Agent    │      ← Local observers
    │ Frontend │  │ API      │        Watch processes, connections
    └────┬─────┘  └─────┬────┘        Coordinate control actions
         │              │              Embedded DuckDB
    ┌────▼─────┐   ┌────▼─────┐
    │ Your     │   │ Your     │      ← Your services
    │ Frontend │   │ API      │        Run normally
    │ + SDK    │   │ + SDK    │        (SDK optional)
    └──────────┘   └──────────┘
```

**Key principles:**

- **Control plane only** - Agents never proxy/intercept application traffic
- **Application-scoped** - One colony per app (not infrastructure-wide)
- **Separated LLM** - Colony is MCP gateway only, AI at developer and Reef layers
- **User-controlled AI** - Developers use their own LLM accounts via local Genkit
- **SDK optional** - Basic observability works without code changes
- **Optional federation** - Reef provides cross-colony analysis for enterprises

## Quick Start

### Build

```bash
make build
```

### Installation & Permissions

Coral creates a WireGuard mesh network for secure communication between colony
and agents. This requires elevated privileges for TUN device creation.

**Choose one installation method:**

#### Option 1: Linux Capabilities (Recommended)

Grant only the `CAP_NET_ADMIN` capability to the binary:

```bash
sudo setcap cap_net_admin+ep ./bin/coral
```

**Why this is preferred:**

- Only grants the specific permission needed (network administration)
- Process runs as your regular user (not root)
- No password prompts after initial setup
- Most secure option (Linux only)

#### Option 2: Run with sudo

Run Coral with sudo when starting the colony:

```bash
sudo ./bin/coral colony start
```

**Trade-offs:**

- ✅ Coral automatically preserves file ownership (configs stay user-owned)
- ⚠️ Entire colony process initially runs as root
- ⚠️ Requires password entry on each start
- Works on all platforms (Linux, macOS)

> **Note:** While the whole process starts as root, Coral detects `SUDO_USER`
> and ensures all config files in `~/.coral/` remain owned by your regular user
> account.

#### Option 3: Setuid Binary (Convenience vs. Security)

**Security: ⭐ Use with caution** | **UX: ⭐⭐⭐⭐⭐ Seamless**

Make the binary setuid root:

```bash
sudo chown root:root ./bin/coral
sudo chmod u+s ./bin/coral
```

**Trade-offs:**

- ✅ No password prompts, seamless experience
- ✅ Config files remain user-owned
- ⚠️ Any vulnerability in the binary could be exploited for privilege escalation
- ⚠️ All users on the system can run it with elevated privileges
- ⚠️ Only recommended for single-user development machines

> **Future Enhancement:** A privileged helper subprocess approach is in
> development (see [RFD 008](RFDs/008-privilege-separation.md)) which will
> provide the UX of Option 3 with security closer to Option 1. The helper will
> spawn only for TUN creation, minimizing the privilege window.

### Run

```bash
# Terminal 1: Start the colony (central brain)
# With capabilities installed (Option 1):
./bin/coral colony start

# Or with sudo (Option 2):
sudo ./bin/coral colony start

# Terminal 2: Start the agent daemon
./bin/coral agent start

# Terminal 3: Connect the agent to observe services
./bin/coral connect frontend:3000
./bin/coral connect api:8080:/health

# Ask questions about your system
./bin/coral ask "Are there any issues?"
```

**Troubleshooting:** If you see a permission error when starting the colony, you
need to grant TUN device creation privileges. See the
[Installation & Permissions](#installation--permissions) section above.

## CLI Commands

### Colony Management

```bash
# Start the colony
coral colony start                    # Start in foreground
coral colony start --daemon           # Start as background daemon
coral colony start --port 3001        # Use custom port

# Check colony status
coral colony status
coral colony status --json            # JSON output

# Stop the colony
coral colony stop
```

### Agent Management

```bash
# Start the agent daemon (required before connecting services)
coral agent start
coral agent start --config /etc/coral/agent.yaml
coral agent start --colony-id my-app-prod

# Check agent status
coral agent status

# Stop the agent
coral agent stop
```

### Service Connections

```bash
# Connect the running agent to observe services
# Format: name:port[:health][:type]
coral connect <service-spec>...

# Single service examples
coral connect frontend:3000
coral connect api:8080:/health:http
coral connect database:5432

# Multiple services at once
coral connect frontend:3000:/health api:8080:/health redis:6379

# Legacy syntax (still supported for single service)
coral connect frontend --port 3000 --health /health

> **Note:**
> - The agent must be running (`coral agent start`) before using `coral connect`
> - Services are dynamically added without restarting the agent
> - The agent uses discovery-provided WireGuard endpoints
> - For local testing, ensure discovery advertises a reachable address (e.g., `127.0.0.1:41580`)
```

### AI Queries

```bash
# Configure your LLM (first time setup)
coral ask config
# Choose provider: OpenAI, Anthropic, or Ollama (local)
# Provide API key (stored locally, never sent to Coral servers)

# Ask questions about your system (uses YOUR LLM account)
coral ask "Why is the API slow?"
coral ask "What changed in the last hour?"
coral ask "Are there any errors in the frontend?"
coral ask "Show me the service dependencies"

# JSON output
coral ask "System status?" --json

# Use specific model
coral ask "What's happening?" --model anthropic:claude-3-5-sonnet-20241022

# Cost tracking
coral ask cost
# Shows your daily LLM usage and estimated costs
```

**How it works:**
- `coral ask` runs a local Genkit agent on your workstation
- Connects to Colony as MCP server to access observability data
- Uses **your own LLM API keys** (OpenAI, Anthropic, or local Ollama)
- You control model choice, costs, and data privacy

### Live Debugging & Control Operations (SDK-integrated mode)

```bash
# Live debugging - attach probes on-demand
coral debug attach <service> --function <func-name> --duration 60s
coral debug trace <service> --path "/api/endpoint" --duration 5m
coral debug list <service>  # Show active probes
coral debug detach <service> --all

# Traffic inspection
coral traffic sample <service> --rate 0.1 --duration 5m
coral traffic inspect <service> --filter "path=/api"

# Profiling
coral profile start <service> --type cpu --duration 60s
coral profile start <service> --type heap
coral profile stop <service>
coral profile download <service> --output profile.pprof

# Rollbacks
coral rollback <service>
coral rollback <service> --to-version v2.2.5
```

### Version

```bash
coral version
```

## Use Cases

### Debug Production Issues

```bash
coral ask "Why are users seeing 500 errors?"
# → Identifies spike in DB connection timeouts after recent deploy
coral rollback api
# → Returns to stable version
```

### LLM-Orchestrated Live Debugging

```bash
coral ask "Why is checkout taking 3 seconds?"

# Coral's LLM analyzes metrics, then decides to attach live probes:
🤖 Analyzing checkout service...
   Metrics unclear - attaching probes to investigate...
   ✓ Uprobe attached: payment.ProcessPayment()
   ✓ Uprobe attached: db.QueryOrders()

   Analysis: db.QueryOrders() taking 2.8s (95% of latency)
   Root cause: Missing index on orders.user_id

   Recommendation: CREATE INDEX idx_orders_user_id ON orders(user_id)

   Detaching probes... ✓ Done (zero overhead restored)
```

### Manual Live Debugging

```bash
# Attach probes manually for investigation
coral debug attach api --function handleCheckout --duration 60s
coral debug trace api --path "/api/checkout" --duration 5m

# View live execution data
coral debug logs api
# → Shows function calls, arguments, execution time

# Cleanup when done
coral debug detach api --all
```

### Performance Investigation

```bash
coral ask "Why is checkout slow?"
# → AI suggests memory pressure in payment service
coral profile start payment --type heap --duration 60s
coral traffic sample payment --rate 0.1
# → Capture data for analysis
```

### Dependency Mapping

```bash
coral ask "Show me service dependencies"
# → Auto-discovered from observed connections
coral ask "What depends on the database?"
# → Impact analysis before changes
```

## What Makes Coral Different?

**The first LLM-orchestrated debugging mesh for distributed apps.**

- **On-demand live debugging** - Attach eBPF uprobes to running code without
  redeploying. **No existing tool does this.** LLM decides where to probe based
  on analysis. Zero overhead when not debugging.

- **Active, not passive** - Coral doesn't just collect metrics - it can
  instrument your code on-demand to find root causes. Like delve for distributed
  apps.

- **Intelligence-driven operations** - Ask questions in natural language using
  **your own LLM** (OpenAI/Anthropic/Ollama). The AI orchestrates debugging
  probes automatically.

- **Unified interface** - One tool for observing, debugging, and controlling (
  not another dashboard to check). CLI, IDE integration, or API.

- **User-controlled AI** - Your API keys, your model choice, your cost control.
  Colony is MCP gateway only - you control the intelligence layer.

- **Control plane only** - Can't break your apps, zero baseline overhead. Probes
  only when debugging.

- **Application-scoped** - One colony per app, scales from laptop to production.
  Not infrastructure-wide monitoring.

- **Data privacy** - Self-hosted, observability data stays in your Colony.

- **Enterprise-ready** - Optional Reef layer for multi-colony federation with
  server-side LLM and policy-based debugging.

## Multi-Colony Federation (Reef)

For enterprises managing multiple environments (dev, staging, prod) or multiple
applications, Coral offers **Reef** - a federation layer that aggregates data
across colonies.

### Architecture

```
Developer/External          Reef (Enterprise)           Colonies
┌──────────────┐          ┌────────────────┐        ┌──────────────┐
│ coral reef   │──HTTPS──▶│  Reef Server   │◄──────▶│ my-app-prod  │
│ CLI          │          │                │ Mesh   │              │
└──────────────┘          │ Server-side    │        └──────────────┘
                          │ LLM (Genkit)   │        ┌──────────────┐
┌──────────────┐          │                │◄──────▶│ my-app-dev   │
│ Slack Bot    │──HTTPS──▶│ ClickHouse     │ Mesh   │              │
└──────────────┘          │                │        └──────────────┘
                          │ Public HTTPS + │        ┌──────────────┐
┌──────────────┐          │ Private Mesh   │◄──────▶│ other-app    │
│ GitHub       │──HTTPS──▶│                │ Mesh   │              │
│ Actions      │          └────────────────┘        └──────────────┘
└──────────────┘
```

### Key Features

- **Dual Interface**: Private WireGuard mesh (colonies) + public HTTPS (
  external integrations)
- **Aggregated Analytics**: Query across all colonies for cross-environment
  analysis
- **Server-side LLM**: Reef hosts its own Genkit service with org-wide LLM
  configuration
- **ClickHouse Storage**: Scalable time-series database for federated metrics
- **External Integrations**: Slack bots, GitHub Actions, mobile apps via public
  API/MCP
- **Authentication**: API tokens, JWT, and mTLS for secure access
- **RBAC**: Role-based permissions for different operations

### Example Usage

```bash
# Cross-environment comparison
coral reef analyze "Compare prod vs staging error rates"
# → Uses Reef's server-side LLM to query all colonies

# Deployment analysis
coral reef deployment-status my-app v2.3.0
# → Shows rollout across dev, staging, prod

# External integration (Slack bot)
# Reef exposes public HTTPS endpoint for ecosystem integrations
# See RFD 003 for API documentation
```

### When to Use Reef

- **Multiple Colonies**: Managing dev, staging, prod environments
- **Cross-environment Analysis**: Compare metrics across all colonies
- **External Integrations**: Slack bots, CI/CD, mobile apps need access
- **Centralized LLM**: Organization prefers managed LLM configuration
- **Enterprise Scale**: ClickHouse for high-volume time-series data

### When Not to Use Reef

- **Single Colony**: Individual developers or single-app deployments
- **Local-only**: If all operations are on your workstation, `coral ask` is
  sufficient
- **No Federation Needed**: Colony-level data is enough for your use case

## Development

### Quick Start (Development Build)

For the best development experience, use `make build-dev` which builds the
binary and automatically grants the necessary TUN device creation privileges:

```bash
# Build with privileges (one command)
make build-dev

# Now run without sudo:
./bin/coral colony start
```

**What it does:**

- **Linux**: Applies `CAP_NET_ADMIN` capability to the binary (secure,
  recommended)
- **macOS**: Applies setuid root (requires password, works seamlessly)

### Development Workflow Options

**Option 1: Build + Capabilities (Recommended)**

```bash
# Initial build with privileges
make build-dev

# Edit code, rebuild (reapplies privileges automatically)
make build-dev

# Run without sudo
./bin/coral colony start
```

**Option 2: Use `go run` with sudo**

```bash
# Quick testing (requires sudo each time)
sudo go run ./cmd/coral colony start

# Note: Capabilities/setuid don't work with go run
# But configs will remain user-owned (SUDO_USER detection)
```

### Standard Development Commands

```bash
# Install dependencies
make mod-tidy

# Build (without privileges)
make build

# Build with privileges (development)
make build-dev

# Run tests
make test

# Format code
make fmt

# Run linter
make lint

# Install to $GOPATH/bin
make install
```

## Documentation

- [CONCEPT.md](CONCEPT.md) - High-level vision and principles
- [CLAUDE.md](CLAUDE.md) - Project instructions
- [docs/IMPLEMENTATION.md](docs/IMPLEMENTATION.md) - Technical implementation
  details
- [docs/STORAGE.md](docs/STORAGE.md) - Storage architecture
- [docs/SDK.md](docs/SDK.md) - SDK integration guide
- [docs/EXAMPLES.md](docs/EXAMPLES.md) - Use case examples
- [RFDs/008-privilege-separation.md](RFDs/008-privilege-separation.md) -
  Privilege separation design (TUN device creation)

## License

TBD
