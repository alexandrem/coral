
## User Experience

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

? Which AI provider? [Anthropic / OpenAI / Local]
  > Anthropic
? API Key: sk-ant-api03-...
? Dashboard port: [3000]

✓ Colony initialized: my-shop-dev
✓ Config saved to .coral/config.yaml
✓ Storage: .coral/colony.duckdb

Start the colony:
  coral colony start

Connect your app components:
  coral connect frontend --port 3000
  coral connect api --port 8080
  coral connect database --port 5432
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

# Or ask questions
$ coral ask "what's my app's topology?"

Coral: "Your application has 3 components:

  frontend (React) → api (Node.js) → database (PostgreSQL)

  All components healthy. No issues detected."
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

**How topology is discovered**: Agents observe network connections locally (via netstat/ss) and report them to the colony. For example, if the API agent sees connections to `10.100.0.6:5000`, and the worker agent is known to be at that IP, Coral infers "api → worker". This is all observation-based - Coral is never in the request path.

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
