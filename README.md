# Coral

**Unified Operations for Distributed Apps**

One binary. One interface. Observe, debug, and control your distributed app.

```
You: "What's wrong with the API?"

Coral: "API crashed with OOM 3 minutes ago. Memory grew 250MB→512MB
       after v2.3.0 deployed 10 min ago. Connection pool leak (85%
       confidence). Recommend rollback to v2.2.5."

You: "Do it"
Coral: "Rolling back API to v2.2.5... Done. Memory stable at 240MB."

⏱️  <1 second analysis, one command to fix
```

## Status

🚧 **Early Development / Design Phase** - Implementation in progress

## The Problem

Your app runs across laptop, VMs, and Kubernetes. When you need to:

- **Debug an issue** → Check logs, metrics, traces across multiple dashboards
- **Toggle a feature** → Open LaunchDarkly, update config, redeploy
- **Rollback a deployment** → `kubectl rollout undo` or manual ops
- **Profile performance** → Set up profiler, SSH to servers, inspect traffic
- **Understand what's happening** → Piece it together manually from scattered
  tools

You're juggling terminals, dashboards, and tools. Context is fragmented. **Coral
unifies this.**

## The Solution

Coral gives you **one interface for distributed app operations**:

### 🔍 Observe

See health, connections, and resource usage across all services in one place.

### 🐛 Debug

Ask questions in natural language. Get AI-powered insights from your system's
behavior.

### 🎛️ Control

- **Feature flags**: Toggle features across services
- **Traffic inspection**: Sample and inspect live requests
- **Profiling**: Start/stop profilers remotely
- **Rollbacks**: Revert deployments with one command

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

# Ask questions about your system
coral ask "What's happening with the API?"
coral ask "Why is checkout slow?"
coral ask "What changed in the last hour?"
```

**You get:** Process monitoring, connection mapping, AI-powered debugging
insights.

### SDK-Integrated Mode (Full Control)

Integrate the Coral SDK for feature flags, traffic inspection, profiling, and
more.

```go
// In your application
import "github.com/coral-io/coral-go"

func main() {
coral.RegisterService("api", coral.Options{
Port: 8080,
HealthEndpoint: "/health",
})

// Feature flags
if coral.IsEnabled("new-checkout") {
useNewCheckout()
}

// Enable profiling endpoint
coral.EnableProfiling()
}
```

```bash
# Control from CLI
coral flags enable new-checkout
coral flags disable legacy-payment --gradual 10%

coral traffic sample api --rate 0.1 --duration 5m
coral traffic inspect api --filter "path=/checkout"

coral profile start api --type cpu --duration 60s
coral profile start frontend --type heap

coral rollback api
coral rollback api --to-version v2.2.5
```

**You get:** Full operations control + all passive mode capabilities.

## Architecture

**Simple three-tier design:**

```
         ┌─────────────────────┐
         │   Colony (Brain)    │  ← AI analysis, coordination
         │   Aggregates data   │    Control plane orchestration
         │   Answers questions │
         └──┬────────┬─────────┘
            │        │
    ┌───────▼──┐  ┌──▼───────┐
    │ Agent    │  │ Agent    │      ← Local observers
    │ Frontend │  │ API      │        Watch processes, connections
    └────┬─────┘  └─────┬────┘        Coordinate control actions
         │              │
    ┌────▼─────┐   ┌────▼─────┐
    │ Your     │   │ Your     │      ← Your services
    │ Frontend │   │ API      │        Run normally
    │ + SDK    │   │ + SDK    │        (SDK optional)
    └──────────┘   └──────────┘
```

**Key principles:**

- **Control plane only** - Agents never proxy/intercept application traffic
- **Application-scoped** - One colony per app (not infrastructure-wide)
- **Self-sufficient** - Works standalone, no external dependencies
- **SDK optional** - Basic observability works without code changes

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
# Ask questions about your system
coral ask "Why is the API slow?"
coral ask "What changed in the last hour?"
coral ask "Are there any errors in the frontend?"
coral ask "Show me the service dependencies"

# JSON output
coral ask "System status?" --json

# Verbose mode
coral ask "What's happening?" --verbose
```

### Control Operations (SDK-integrated mode)

```bash
# Feature flags
coral flags list
coral flags enable <flag-name>
coral flags disable <flag-name>
coral flags enable <flag-name> --gradual 25%  # Gradual rollout

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

### Feature Flag Management

```bash
coral flags enable new-checkout --gradual 10%
# → Roll out gradually to 10% of traffic
coral ask "Any issues with new-checkout?"
# → AI monitors and reports anomalies
coral flags enable new-checkout --gradual 100%
# → Full rollout after validation
```

### Performance Investigation

```bash
coral ask "Why is checkout slow?"
# → Suggests memory pressure in payment service
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

- **Unified interface** - One tool for observing, debugging, and controlling (
  not another dashboard to check)
- **AI-powered insights** - Ask questions in natural language, get intelligent
  answers (<1s from local data)
- **Two-tier integration** - Works passively (no code changes) or
  SDK-integrated (full control)
- **Self-sufficient** - Standalone intelligence from local data, optionally
  enriched via MCP (Grafana/Sentry)
- **Control plane only** - Can't break your apps, zero performance impact
- **Application-scoped** - One colony per app, scales from laptop to production
- **User-controlled** - Self-hosted, your AI keys, your data stays local

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
