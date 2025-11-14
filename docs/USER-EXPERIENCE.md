# Coral User Experience

This document covers both the **current implementation** (what works today) and
the **vision** (planned features) for Coral's user experience.

---

## Getting Started (Current Implementation)

This section describes Coral as it exists today. Follow these steps to run Coral
components and test the functionality.

### Prerequisites

Build the binaries:

```bash
make build
```

This creates:

- `bin/coral` - Main CLI with all commands
- `bin/coral-discovery` - Discovery service

### Step 1: Start the Discovery Service

The discovery service enables colonies to announce themselves and be discovered
by proxies or external integrations.

```bash
./bin/coral-discovery --port 8080 --ttl 300 --cleanup-interval 60
```

**Options:**

- `--port`: HTTP port for the discovery service (default: 8080)
- `--ttl`: Time-to-live for registrations in seconds (default: 300)
- `--cleanup-interval`: How often to clean up expired entries in seconds (
  default: 60)
- `--log-level`: Logging level: debug, info, warn, error (default: info)

**What it does:**

- Provides a registry for colonies to announce themselves
- Stores colony metadata: mesh IPs, WireGuard public keys, endpoints
- Automatically expires stale registrations

**Expected output:**

```
INF Starting discovery service port=8080 ttl=300s version=dev
INF Discovery service listening addr=:8080
```

Keep this running in a terminal.

### Step 2: Initialize a Colony

In a new terminal, initialize a colony configuration:

```bash
./bin/coral init
```

**Prompts:**

- Application name (e.g., `my-app`)
- Environment (e.g., `dev`, `prod`)

**What it does:**

- Generates WireGuard keypair
- Creates `~/.coral/colonies/<colony-id>.yaml`
- Configures discovery settings

**Example output:**

```
✓ Colony initialized: my-app-dev-a1b2c3
  Config: /Users/you/.coral/colonies/my-app-dev-a1b2c3.yaml
```

### Step 3: Start the Colony

Start the colony with automatic discovery registration.

#### Local Development (same machine)

```bash
./bin/coral colony start
```

**What it does:**

- Loads colony configuration
- Registers with discovery service every 60 seconds
- Advertises WireGuard endpoint: `127.0.0.1:41580` (localhost)
- Automatically sets default values:
    - `mesh_ipv4: 10.42.0.1`
    - `mesh_ipv6: fd42::1`
    - `connect_port: 9000`

**Expected output:**

```
INF Starting registration manager mesh_id=my-app-dev-a1b2c3
INF Successfully registered with discovery service ttl_seconds=300
INF Colony started successfully colony_id=my-app-dev-a1b2c3

Press Ctrl+C to stop
```

#### Production Deployment (different machines)

**IMPORTANT:** For agents to connect from different machines, you **MUST** set
`CORAL_PUBLIC_ENDPOINT` to your colony's publicly reachable IP or hostname:

```bash
# With public IP
CORAL_PUBLIC_ENDPOINT=203.0.113.5:41580 ./bin/coral colony start

# With hostname (recommended)
CORAL_PUBLIC_ENDPOINT=colony.example.com:41580 ./bin/coral colony start
```

**Why this is required:**

- WireGuard endpoints must be reachable addresses (public IP or hostname)
- The mesh IPs (10.42.0.1) only work **inside** the tunnel
- Without `CORAL_PUBLIC_ENDPOINT`, agents can't establish the initial connection

**Cloud deployments:**

- AWS EC2: Use Elastic IP or public IP from instance metadata
- GCP: Use external IP from instance metadata
- Azure: Use public IP address
- Behind NAT: Configure port forwarding and use public IP
- Docker: Use host machine's public IP

**Verify registration:**

```bash
curl http://localhost:8080/coral.discovery.v1.DiscoveryService/Health
```

You should see `registered_colonies: 1` in the response.

Keep the colony running in this terminal.

### Step 4: Start the Proxy (Optional)

> **Note:** With RFD 031 (Colony dual interface), colonies can optionally expose
> a public HTTPS endpoint like Reef does. When enabled, the proxy is only needed
> for mesh-only access scenarios. This step describes the current proxy-based
> approach.

In a new terminal, start the proxy to access the colony:

```bash
./bin/coral proxy start my-app-dev-a1b2c3
```

Replace `my-app-dev-a1b2c3` with your actual colony ID from step 2.

**What it does:**

- Queries discovery service to find the colony
- Retrieves colony's mesh IPs and connect port
- Starts HTTP/2 reverse proxy on `localhost:8000`
- Forwards requests to colony over mesh network

**Expected output:**

```
INF Starting coral proxy mesh_id=my-app-dev-a1b2c3
INF Looking up colony in discovery service
INF Colony found mesh_ipv4=10.42.0.1 mesh_ipv6=fd42::1 connect_port=9000
INF Proxy server started listen_addr=localhost:8000
✓ Proxy running on localhost:8000
  Forwarding to colony at 10.42.0.1:9000 (over mesh)

Press Ctrl+C to stop proxy
```

**Options:**

- `--listen`: Local address to bind to (default: `localhost:8000`)
- `--discovery`: Discovery service endpoint (default: `http://localhost:8080`)

Keep the proxy running in this terminal.

### Step 5: Validate Everything Works

#### Check Discovery Service Health

```bash
curl -X POST http://localhost:8080/coral.discovery.v1.DiscoveryService/Health \
  -H "Content-Type: application/json" \
  -d '{}'
```

**Expected response:**

```json
{
    "status": "ok",
    "version": "dev",
    "uptimeSeconds": "123",
    "registeredColonies": 1
}
```

#### Query Colony via Discovery

```bash
curl -X POST http://localhost:8080/coral.discovery.v1.DiscoveryService/LookupColony \
  -H "Content-Type: application/json" \
  -d '{"mesh_id": "my-app-dev-a1b2c3"}'
```

**Expected response:**

```json
{
    "meshId": "my-app-dev-a1b2c3",
    "pubkey": "...",
    "endpoints": [
        ":41581"
    ],
    "meshIpv4": "10.42.0.1",
    "meshIpv6": "fd42::1",
    "connectPort": 9000,
    "metadata": {
        "application": "my-app",
        "environment": "dev"
    },
    "lastSeen": "2025-10-29T20:50:00Z"
}
```

#### Test Proxy (when ColonyService is implemented)

Once the colony implements the ColonyService RPC handlers:

```bash
curl -X POST http://localhost:8000/coral.colony.v1.ColonyService/GetStatus \
  -H "Content-Type: application/json" \
  -d '{}'
```

### Component Overview

```
┌─────────────────────────────────────────────────────────┐
│  Developer Machine                                       │
│                                                          │
│  Terminal 1: Discovery Service (port 8080)              │
│  Terminal 2: Colony (registers every 60s)               │
│  Terminal 3: Proxy (localhost:8000)                     │
│  Terminal 4: CLI commands / curl tests                  │
│                                                          │
└─────────────────────────────────────────────────────────┘
```

### CLI Commands Reference

#### Discovery Service

```bash
# Start discovery service
./bin/coral-discovery --port 8080 --ttl 300 --cleanup-interval 60

# With debug logging
./bin/coral-discovery --log-level debug
```

#### Colony Management

```bash
# Initialize new colony
./bin/coral init

# Start colony
./bin/coral colony start

# Start in background (daemon mode)
./bin/coral colony start --daemon

# List colonies
./bin/coral colony list

# Show current colony
./bin/coral colony current

# Stop colony
./bin/coral colony stop
```

#### Proxy Management

```bash
# Start proxy for a colony
./bin/coral proxy start <colony-id>

# Custom listen address
./bin/coral proxy start <colony-id> --listen localhost:8001

# Custom discovery endpoint
./bin/coral proxy start <colony-id> --discovery http://remote-host:8080

# Show proxy status (placeholder)
./bin/coral proxy status

# Stop proxy (placeholder)
./bin/coral proxy stop <colony-id>
```

#### General

```bash
# Show version
./bin/coral version

# Show help
./bin/coral --help
./bin/coral proxy --help
./bin/coral colony --help
```

### Troubleshooting

#### Discovery service not reachable

**Error:**
`failed to lookup colony: Post "http://localhost:8080": connection refused`

**Solution:** Start the discovery service first:

```bash
./bin/coral-discovery --port 8080
```

#### Colony not found

**Error:** `colony not found: my-app-dev-a1b2c3`

**Solutions:**

1. Verify colony is running: `./bin/coral colony status`
2. Check discovery registration:
   `curl http://localhost:8080/coral.discovery.v1.DiscoveryService/Health`
3. Wait up to 60 seconds for next heartbeat
4. Check colony logs for registration errors

#### No mesh IP configured

**Error:** `failed to start proxy server: no colony mesh IP configured`

**Solutions:**

1. Colony is running older code without mesh IP defaults
2. Restart colony with latest binary: `./bin/coral colony start`
3. Manually add to colony config file:
   ```yaml
   wireguard:
     mesh_ipv4: "10.42.0.1"
     mesh_ipv6: "fd42::1"
   services:
     connect_port: 9000
   ```

#### Port already in use

**Error:** `failed to listen on localhost:8000: address already in use`

**Solution:** Use a different port:

```bash
./bin/coral proxy start <colony-id> --listen localhost:8001
```

### Architecture (Current)

The current implementation follows **RFD 005: CLI Access via Local Proxy**:

**Flow:**

1. Colony registers with discovery service (mesh IPs, connect port)
2. Proxy queries discovery to find colony
3. Proxy starts local HTTP/2 server (localhost:8000)
4. CLI tools query proxy instead of colony directly
5. Proxy forwards requests to colony over mesh network (future: via WireGuard
   tunnel)

**Benefits:**

- CLI tools don't need WireGuard logic
- Centralized access point for multiple colonies
- Works across NATs and firewalls
- Foundation for future features (agent queries, multi-colony federation)

**Future:** RFD 031 introduces optional public endpoints for Colony (like Reef),
making the proxy optional for many scenarios.

---

## Vision: Future User Experience

This section describes Coral's planned user experience with full agent support,
LLM integration, and observability features. These capabilities are under active
development.

### Setup Flow

**Step 1: Install Coral**

```bash
# Single command install
$ curl -fsSL coral.io/install.sh | sh
✓ Coral CLI installed to /usr/local/bin/coral
✓ Version: 0.1.0
```

**Step 2: Initialize Colony for Your App**

```bash
$ cd ~/projects/my-shop
$ coral colony init

Welcome to Coral!

Creating colony for: my-shop

? Colony ID: [my-shop-dev]
? Storage: [DuckDB (embedded) / ClickHouse (external)]
  > DuckDB
? Dashboard port: [3000]

✓ Colony initialized: my-shop-dev
✓ Config saved to .coral/config.yaml
✓ Storage: .coral/colony.duckdb (DuckDB)

Start the colony:
  coral colony start

Connect your app components:
  coral connect frontend --port 3000
  coral connect api --port 8080
  coral connect database --port 5432

Configure AI for debugging (optional):
  coral ask config
```

**Step 3: Start Colony (Runs Locally)**

```bash
$ coral colony start

Coral Colony Starting...
✓ Application: my-shop-dev
✓ Database: .coral/colony.duckdb (DuckDB)
✓ Wireguard: listening on :41820
✓ Dashboard: http://localhost:3000

Ready to connect your app components!
```

**Step 4: Connect Your App Components**

```bash
# Terminal 1: Start your frontend
$ npm run dev
> Frontend running on http://localhost:3000

# Terminal 2: Connect it to Coral
$ coral connect frontend --port 3000
✓ Connected: frontend (localhost:3000)
✓ Agent observing: React app
Agent running. Press Ctrl+C to disconnect.

# Terminal 3: Start your API
$ node server.js
> API listening on port 8080

# Terminal 4: Connect it to Coral
$ coral connect api --port 8080
✓ Connected: api (localhost:8080)
✓ Agent observing: Node.js server
✓ Discovered connection: frontend → api
Agent running. Press Ctrl+C to disconnect.

# Your database is already running
$ coral connect database --port 5432
✓ Connected: database (localhost:5432)
✓ Agent observing: PostgreSQL
✓ Discovered connection: api → database
Agent running. Press Ctrl+C to disconnect.
```

**Now Your App is Alive!**

```bash
# Open the dashboard
$ open http://localhost:3000

# Or ask questions (requires AI configuration - see next section)
$ coral ask "what's my app's topology?"

Coral: "Your application has 3 components:

  frontend (React) → api (Node.js) → database (PostgreSQL)

  All components healthy. No issues detected."
```

### AI Configuration (Optional)

**Configure Your LLM for `coral ask`**

The `coral ask` command uses a local Genkit agent on your workstation with
**your own LLM API keys**. This gives you full control over model choice, costs,
and data privacy.

```bash
# First-time setup
$ coral ask config

🤖 Coral AI Configuration

? Choose your LLM provider:
  1. OpenAI (GPT-4, GPT-3.5)
  2. Anthropic (Claude)
  3. Ollama (local models)
  > 2

? Anthropic API Key: sk-ant-api03-...
✓ API key validated

? Default model: [claude-3-5-sonnet-20241022]
  > claude-3-5-sonnet-20241022

? Fallback models (optional):
  > claude-3-5-haiku-20241022

? Cost control - warn at daily cost (USD): [5.00]
  > 10.00

✓ Configuration saved to ~/.coral/ask.yaml
✓ Your API key is stored locally (never sent to Coral servers)

Ready to use:
  coral ask "Why is the API slow?"
```

**How it works:**

- Runs a local Genkit agent on your workstation
- Connects to Colony as MCP server to fetch observability data
- Uses **your own LLM account** (you pay, you control)
- Configuration stored in `~/.coral/ask.yaml`
- Switch models anytime: `coral ask config --model openai:gpt-4o`

**Cost tracking:**

```bash
$ coral ask cost

CORAL ASK - USAGE & COSTS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Provider: Anthropic
Model: claude-3-5-sonnet-20241022

Today (2024-01-15):
  Requests: 23
  Input tokens: 45,203
  Output tokens: 12,891
  Estimated cost: $2.43 USD

This month:
  Requests: 156
  Total cost: $18.67 USD

Warning threshold: $10.00/day (not exceeded today)
```

### Daily Operations

**View Application Status**

```bash
$ coral status

APPLICATION: my-shop (dev)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Colony: my-shop-dev (running locally)
Connected: 3 components

COMPONENT    STATUS    VERSION   UPTIME    PORT     RESTARTS
frontend     ●  up     1.9.2     3h 24m    3000     3
api          ●  up     2.1.0     5h 15m    8080     0
database     ●  up     14.2      2d 8h     5432     0

🤖 AI Insights (1):
  1. ⚠️  frontend has restarted 3x - possible memory leak

View details: coral insights
Open dashboard: coral dashboard
```

**Ask Questions**

```bash
$ coral ask "why did frontend restart?"

🤖 Analyzing frontend events...

Found 3 restarts in last 4 hours:
  - 11:15 UTC: OOMKilled (memory: 512MB → 890MB)
  - 12:42 UTC: OOMKilled (memory: 512MB → 925MB)
  - 14:05 UTC: OOMKilled (memory: 512MB → 960MB)

Correlation Analysis:
  ✓ Started after frontend v1.9.2 deployed (4h ago)
  ✓ No corresponding changes in api or worker
  ✓ Memory usage trending upward (likely memory leak)
  ✓ Previous version (v1.9.1) was stable for 5 days

Root Cause (Confidence: High):
  Memory leak introduced in frontend v1.9.2

Recommendations:
  1. [Immediate] Increase memory limit to 1024MB
     Command: docker update --memory 1024m frontend

  2. [Short-term] Rollback to v1.9.1
     Command: docker pull myapp/frontend:1.9.1 && docker restart frontend

  3. [Long-term] Investigate memory leak in v1.9.2
     Hint: Check recent commits, run memory profiler

Similar incidents: 1 (frontend v1.7.0, 3 months ago - similar pattern)
```

**View Insights**

```bash
$ coral insights

AI INSIGHTS (3 active)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

⚠️  ANOMALY DETECTED                       [High Priority]
────────────────────────────────────────────────────────
Created: 2 hours ago
Service: api
Issue: Response time degraded 2.3x (avg 45ms → 105ms)

Pattern Detected:
  - api v2.1.0 deployed 2.5 hours ago
  - worker v1.8.0 started processing jobs 40% slower
  - New connection pattern: worker → database (wasn't there before)

Root Cause:
  worker v1.8.0 incompatible with api v2.1.0 response format
  Worker doing expensive DB lookup to compensate

Recommendation:
  → Upgrade worker to v1.8.1 (compatible with api v2.1.0)
  → Or rollback api to v2.0.9
  → Or add caching layer between worker and database

Apply: coral apply-recommendation insight-001
Dismiss: coral dismiss insight-001


ℹ️  TRAFFIC PATTERN                        [Medium Priority]
────────────────────────────────────────────────────────
Created: 1 day ago
Service: all

Observation:
  Daily traffic spike at 14:00 UTC (+120% requests)
  CPU reaches 85% during peak
  Currently: 3 instances each (api, worker)

Recommendation:
  → Schedule scale-up to 5 instances at 13:45 UTC
  → Estimated cost: +$12/day during peak hours
  → Estimated improvement: 50ms faster response time

Note: This is a recurring pattern (30 days observed)


✓  DEPLOYMENT SUCCESS                      [Low Priority]
────────────────────────────────────────────────────────
Created: 5 hours ago
Service: api

api v2.1.0 deployment successful
  - Rolled out smoothly over 2 hours
  - Error rate: normal (0.08%)
  - No user-facing issues detected
  - All health checks passing

Great job! 🎉
```

**View Topology**

```bash
$ coral topology

SERVICE TOPOLOGY
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

frontend (10.100.0.7)
  → api (10.100.0.5)              [45 req/min]
  → cdn.cloudflare.com            [static assets]

api (10.100.0.5)
  → worker (10.100.0.6)           [18 req/min]
  → cache (10.100.0.9)            [156 ops/min]
  → db-proxy (10.100.0.8)         [42 queries/min]

worker (10.100.0.6)
  → db-proxy (10.100.0.8)         [12 queries/min]
  → queue (10.100.0.10)           [8 jobs/min]
  → s3.amazonaws.com              [3 uploads/min]

db-proxy (10.100.0.8)
  → postgres.internal.db          [54 queries/min]

queue (10.100.0.10)
  → redis.internal.cache          [persistent queue]

Detected Dependencies: 10
External Services: 3 (CDN, S3, internal DB)

View visual map: coral dashboard
Export graph: coral topology --export topology.dot
```

**How topology is discovered**: Agents observe network connections locally (via
netstat/ss) and report them to the colony. For example, if the API agent sees
connections to `10.100.0.6:5000`, and the worker agent is known to be at that
IP, Coral infers "api → worker". This is all observation-based - Coral is never
in the request path.

**Web Dashboard**

```bash
$ coral dashboard
✓ Dashboard available at http://localhost:3000
✓ Opening in browser...
```

Dashboard features:

- Visual topology map (interactive graph)
- Timeline of deploys and events
- AI insight cards (with "Apply" buttons)
- Version history across services
- Real-time status updates
- Natural language search

---

## Enterprise: Multi-Colony Federation (Reef)

For organizations managing multiple colonies (dev, staging, prod, multiple
apps), Coral offers **Reef** - a federation layer that aggregates data and
provides cross-colony analysis.

### Setup Reef

**Step 1: Initialize Reef Server**

```bash
$ coral reef init

Welcome to Coral Reef!

Creating reef for: my-organization

? Reef ID: [my-org-reef]
? Storage backend: [ClickHouse]
  > ClickHouse

? ClickHouse host: clickhouse.internal
? ClickHouse port: [9000]
? ClickHouse database: [coral_reef]

? LLM Provider (server-side): [OpenAI / Anthropic / Ollama]
  > Anthropic
? API Key: sk-ant-api03-...
  ✓ API key validated

? Enable public HTTPS endpoint? [yes / no]
  > yes
? Domain: reef.mycompany.com
? TLS cert path: /etc/reef/tls/cert.pem
? TLS key path: /etc/reef/tls/key.pem

✓ Reef initialized: my-org-reef
✓ Config saved to /etc/coral/reef.yaml
✓ Storage: ClickHouse (coral_reef database)
✓ Private mesh: :41820
✓ Public endpoint: https://reef.mycompany.com

Start the reef:
  coral reef start
```

**Step 2: Connect Colonies to Reef**

```bash
# On each colony machine
$ coral colony config --reef-endpoint reef.internal:41820

✓ Colony configured to federate with reef
✓ Mesh peer added: reef.internal:41820

# Restart colony to apply
$ coral colony restart

✓ Colony connected to reef: my-org-reef
✓ Starting data sync...
```

### Reef Operations

**Cross-Environment Analysis**

```bash
# Compare environments
$ coral reef analyze "Compare error rates: prod vs staging"

🤖 Analyzing across 3 colonies (prod, staging, dev)...

CROSS-ENVIRONMENT COMPARISON
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Error Rate (last 24h):
  prod:    0.12% (45 errors / 37,500 requests)
  staging: 0.08% (8 errors / 10,200 requests)
  dev:     2.43% (124 errors / 5,100 requests)

Key Differences:
  ✓ prod and staging error rates within normal range
  ⚠️  dev error rate 20x higher than prod

Root Cause (dev):
  - 89% of errors: "Database connection timeout"
  - Started 6 hours ago (correlates with dev DB maintenance)
  - Not present in staging or prod

Recommendation:
  - Check dev database connection pool configuration
  - Verify dev DB is accessible and not under maintenance
```

**Deployment Tracking**

```bash
# Track deployment across all environments
$ coral reef deployment-status my-app v2.5.0

DEPLOYMENT STATUS: my-app v2.5.0
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

dev:      ✓ Deployed  (2 days ago)
          Error rate: 0.05% (normal)

staging:  ✓ Deployed  (1 day ago)
          Error rate: 0.08% (normal)
          Currently: Load testing in progress

prod:     ⏳ Rolling out (25% complete)
          Started: 15 minutes ago
          Error rate: 0.11% (normal)
          ETA: 30 minutes

Overall: On track, no issues detected
```

**Correlation Analysis**

```bash
# Find patterns across all colonies
$ coral reef correlations "slow database queries"

🤖 Searching for patterns across all colonies...

CORRELATION ANALYSIS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Found pattern in 2 colonies:

prod (my-app-prod):
  - DB query latency increased 3x (avg 45ms → 135ms)
  - Started 2 hours ago
  - Affects: user_orders table queries

staging (my-app-staging):
  - Same pattern observed 1 day ago
  - Resolved after adding index on user_orders.created_at

Recommendation:
  → Apply same fix to prod: CREATE INDEX idx_orders_created ON user_orders(created_at)
  → Estimated improvement: 3x query speedup
  → Similar pattern previously fixed in staging
```

### External Integrations

Reef exposes a public HTTPS endpoint for external integrations (Slack bots,
GitHub Actions, mobile apps, etc.)

**Slack Bot Example**

```bash
# Configure Slack integration
$ coral reef integration add slack

? Slack workspace: mycompany.slack.com
? Bot token: xoxb-...
? Channel for notifications: #coral-alerts

✓ Slack bot configured
✓ API token generated: reef-tok-abc123...

Test it:
  In Slack: @coral what's the prod error rate?
```

**GitHub Actions Integration**

```yaml
# .github/workflows/deploy.yml
-   name: Check Reef Status
    run: |
        curl -H "Authorization: Bearer ${{ secrets.REEF_TOKEN }}" \
             https://reef.mycompany.com/api/v1/analyze \
             -d '{"question": "Is prod healthy for deployment?"}'
```

**API Access**

```bash
# Generate API token for external clients
$ coral reef token create --name "mobile-app" --permissions analyze,compare

✓ Token created: reef-tok-def456...
✓ Permissions: analyze, compare
✓ Rate limit: 100 requests/hour

Use in API calls:
  curl -H "Authorization: Bearer reef-tok-def456..." \
       https://reef.mycompany.com/api/v1/analyze
```

### MCP Server (Reef)

Reef also exposes an MCP server for AI assistants like Claude Desktop:

```bash
# Generate MCP credentials
$ coral reef mcp-token create

✓ MCP endpoint: https://reef.mycompany.com/mcp/sse
✓ Token: mcp-tok-789xyz...

Add to Claude Desktop config (~/.config/claude/claude_desktop_config.json):
{
  "mcpServers": {
    "coral-reef": {
      "transport": "sse",
      "url": "https://reef.mycompany.com/mcp/sse",
      "headers": {
        "Authorization": "Bearer mcp-tok-789xyz..."
      }
    }
  }
}
```

Now Claude Desktop can query your entire Coral infrastructure:

```
You (in Claude Desktop): "Compare API performance across all environments"

Claude: [Uses coral-reef MCP server to query all colonies]
        "Based on data from your Coral Reef:

        prod: 45ms avg (p95: 120ms) - healthy
        staging: 52ms avg (p95: 145ms) - healthy
        dev: 380ms avg (p95: 890ms) - degraded

        dev environment shows significant performance degradation..."
```

### When to Use Reef

Use Reef when you need:

- **Multiple environments**: dev, staging, prod management
- **Cross-colony analysis**: Compare metrics and deployments
- **External integrations**: Slack bots, CI/CD, mobile apps
- **Centralized LLM**: Organization-wide AI configuration
- **Enterprise scale**: ClickHouse for high-volume data

### When NOT to Use Reef

Skip Reef if you have:

- **Single colony**: One developer, one environment
- **Local-only**: All operations on your workstation
- **No federation needs**: Colony-level data is sufficient

For single-colony use, `coral ask` (local Genkit) is simpler and more
cost-effective.
