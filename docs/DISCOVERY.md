# Discovery Service Architecture

The Discovery Service enables dynamic agent-to-Colony connections, NAT
traversal, and multi-region deployments. This document explains how agents find
Colonies, how split-brain scenarios are prevented, and how failover works.

## Purpose

**Problem**: Agents and Colonies may be behind NATs, have dynamic IPs, or span
multiple regions. Hardcoding endpoints doesn't scale.

**Solution**: A lightweight Discovery Service that:

- Maintains a registry of active Colonies
- Enables agents to find their Colony by ID
- Coordinates WireGuard key exchange
- Assists with NAT traversal
- Prevents split-brain scenarios via leases

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│  Discovery Service (discovery.coral.io)                     │
│  ┌────────────────────────────────────────────────────────┐ │
│  │  Colony Registry                                       │ │
│  │  - colony_id → endpoint mapping                        │ │
│  │  - Lease-based (60s TTL)                               │ │
│  │  - Heartbeat required (30s interval)                   │ │
│  └────────────────────────────────────────────────────────┘ │
│  ┌────────────────────────────────────────────────────────┐ │
│  │  WireGuard Coordination                                │ │
│  │  - Public key exchange                                 │ │
│  │  - Endpoint discovery                                  │ │
│  └────────────────────────────────────────────────────────┘ │
│  ┌────────────────────────────────────────────────────────┐ │
│  │  NAT Traversal Helper                                  │ │
│  │  - STUN-like endpoint detection                        │ │
│  │  - Relay fallback (TURN-like)                          │ │
│  └────────────────────────────────────────────────────────┘ │
└──────────┬──────────────────────────────┬──────────────────┘
           │                              │
  ┌────────▼─────────┐          ┌────────▼─────────┐
  │  Colony          │          │  Agent           │
  │  - Registers     │          │  - Queries       │
  │  - Heartbeats    │          │  - Connects      │
  └──────────────────┘          └──────────────────┘
```

## Colony Registration

### Registration Flow

When a Colony starts, it registers with the Discovery Service:

```bash
# Colony starts
$ coral colony start

# Colony configuration
colony:
  id: prod-us-east              # Unique Colony identifier
  discovery:
    url: https://discovery.coral.io
    lease_ttl: 60s              # Lease duration
    heartbeat_interval: 30s     # Heartbeat frequency
```

**Registration Request:**

```http
POST https://discovery.coral.io/v1/colonies/register
Content-Type: application/json

{
  "colony_id": "prod-us-east",
  "region": "us-east-1",
  "public_key": "wg_pubkey_abc123...",
  "endpoint": "203.0.113.5:51820",
  "lease_ttl": 60,
  "metadata": {
    "version": "v1.0.0",
    "capacity": 1000,
    "features": ["ai", "mesh", "rbac"]
  }
}
```

**Registration Response (Success):**

```json
{
    "status": "registered",
    "lease_id": "lease-xyz789",
    "expires_at": "2025-11-01T12:01:00Z",
    "discovery_endpoint": "discovery.coral.io:51821"
}
```

**Registration Response (Conflict):**

```json
{
    "status": "conflict",
    "error": "Colony ID 'prod-us-east' already registered",
    "existing": {
        "endpoint": "203.0.113.5:51820",
        "registered_at": "2025-11-01T12:00:00Z",
        "lease_expires_at": "2025-11-01T12:01:00Z",
        "region": "us-east-1"
    },
    "suggestion": "Use a different colony_id or wait for lease expiration"
}
```

### Heartbeat Mechanism

Colonies must send heartbeats to maintain their lease:

```http
POST https://discovery.coral.io/v1/colonies/heartbeat
Content-Type: application/json

{
  "lease_id": "lease-xyz789",
  "status": "healthy",
  "agent_count": 45,
  "load": 0.35
}
```

**Heartbeat Response:**

```json
{
    "status": "renewed",
    "expires_at": "2025-11-01T12:02:00Z"
}
```

**Heartbeat Failure:**

If heartbeats stop, the lease expires after `lease_ttl` seconds. The Colony is
marked unavailable and removed from the registry.

### Deregistration

Graceful shutdown:

```http
DELETE https://discovery.coral.io/v1/colonies/{colony_id}
Content-Type: application/json

{
  "lease_id": "lease-xyz789",
  "reason": "shutdown"
}
```

## Agent Discovery

### Discovery Flow

When an agent starts, it queries the Discovery Service to find its Colony:

```bash
# Agent starts
$ coral agent start

# Agent configuration
agent:
  colony:
    id: prod-us-east            # Which Colony to connect to
    auto_discover: true         # Use Discovery Service
    discovery_url: https://discovery.coral.io
```

**Discovery Request:**

```http
GET https://discovery.coral.io/v1/colonies/prod-us-east
```

**Discovery Response (Success):**

```json
{
    "colony_id": "prod-us-east",
    "region": "us-east-1",
    "public_key": "wg_pubkey_abc123...",
    "endpoint": "203.0.113.5:51820",
    "lease_expires_at": "2025-11-01T12:01:00Z",
    "nat_traversal": {
        "method": "direct",
        "stun_server": "stun.coral.io:3478",
        "relay_available": true
    }
}
```

**Discovery Response (Not Found):**

```json
{
    "status": "not_found",
    "error": "Colony 'prod-us-east' not registered",
    "available_colonies": [
        "prod-eu-west",
        "staging-us-east",
        "dev-local"
    ]
}
```

### Agent Connection Establishment

After discovering the Colony endpoint, the agent:

1. **Configures WireGuard** with Colony's public key and endpoint
2. **Establishes tunnel** (may use NAT traversal assistance)
3. **Connects via gRPC** over WireGuard tunnel
4. **Registers with Colony** (sends agent metadata)

**Agent Registration with Colony:**

```grpc
// After WireGuard tunnel established
service Colony {
  rpc RegisterAgent(RegisterAgentRequest) returns (RegisterAgentResponse);
}

message RegisterAgentRequest {
  string agent_id = 1;
  string hostname = 2;
  map<string, string> labels = 3;
  repeated string services = 4;
  AgentCapabilities capabilities = 5;
}
```

## When Discovery Service is Needed

| Scenario                       | Colony Location       | Discovery Needed? | Why/Why Not                           |
|--------------------------------|-----------------------|-------------------|---------------------------------------|
| **Local dev (docker-compose)** | `localhost:8080`      | ❌ No              | Agent connects to localhost directly  |
| **Explicit Colony URL**        | Hardcoded in config   | ❌ No              | Agent has endpoint, connects directly |
| **Same network (no NAT)**      | Internal IP           | ❌ No              | Direct IP connectivity works          |
| **Air-gapped environment**     | No Colony             | ❌ No              | Agent-only mode                       |
| **Colony behind NAT**          | Dynamic public IP     | ✅ Yes             | Agent needs to find current endpoint  |
| **Agent behind NAT**           | Both behind NAT       | ✅ Yes             | Needs NAT traversal coordination      |
| **Multi-region mesh**          | Multiple regions      | ✅ Yes             | Agent needs to find correct Colony    |
| **Laptop → Production**        | Remote, both NAT'd    | ✅ Yes             | Full NAT traversal required           |
| **Production (K8s)**           | Load-balanced service | ⚠️ Optional       | Can use K8s DNS or Discovery Service  |

### Configuration: With vs Without Discovery

**Without Discovery Service (Explicit URL):**

```yaml
# Agent config
agent:
    colony:
        url: https://colony.company.internal:8080
        public_key: wg_pubkey_abc123...
        auto_discover: false
```

Agent connects directly to configured URL. Simple but requires manual endpoint
management.

**With Discovery Service (Dynamic):**

```yaml
# Agent config
agent:
    colony:
        id: prod-us-east
        auto_discover: true
        discovery_url: https://discovery.coral.io
```

Agent queries Discovery Service, gets current endpoint. Handles Colony IP
changes, failover, NAT.

## Multi-Colony Scenarios

### Scenario 1: Single Colony (Normal)

**Setup:**

- One Colony: `prod-colony`
- Multiple agents connecting to it

**Discovery Service Registry:**

```
colony_id: prod-colony
endpoint: 203.0.113.5:51820
status: active
lease_expires: 2025-11-01T12:01:00Z
```

**Behavior:**

- All agents query `prod-colony` → get `203.0.113.5`
- Simple, works perfectly

### Scenario 2: Multi-Region (Intentional)

**Setup:**

- Colony US: `prod-us-east`
- Colony EU: `prod-eu-west`
- Agents in each region connect to local Colony

**Discovery Service Registry:**

```
colony_id: prod-us-east
endpoint: 203.0.113.5:51820
region: us-east-1

colony_id: prod-eu-west
endpoint: 198.51.100.7:51820
region: eu-west-1
```

**Behavior:**

- US agents query `prod-us-east` → get US Colony
- EU agents query `prod-eu-west` → get EU Colony
- **This is the correct pattern for multi-region deployments**

**Key:** Each Colony has a **unique ID**. No conflicts.

### Scenario 3: Split-Brain (Accidental Duplicate ID)

**Setup:**

- Colony A running as `prod-colony` at `203.0.113.5`
- Operator accidentally starts Colony B with same ID `prod-colony`

**Timeline:**

```
T0: Colony A registers
    POST /register { colony_id: "prod-colony", endpoint: "203.0.113.5:51820" }
    → Success, lease granted

T1: Colony B tries to register (same ID)
    POST /register { colony_id: "prod-colony", endpoint: "198.51.100.8:51820" }
    → Discovery Service detects conflict

    Response:
    {
      "status": "conflict",
      "error": "Colony ID 'prod-colony' already registered",
      "existing": {
        "endpoint": "203.0.113.5:51820",
        "registered_at": "2025-11-01T12:00:00Z",
        "lease_expires_at": "2025-11-01T12:01:00Z"
      }
    }

T2: Colony B startup fails
    Error: Colony ID conflict. Another Colony with ID 'prod-colony' is running.

    Suggested actions:
      1. Check if Colony A is the intended instance
      2. Use a different colony_id (e.g., "prod-colony-backup")
      3. For HA, see docs on Raft-based high availability
```

**Result:** Split-brain prevented. Only one Colony can hold a given ID at a
time.

### Scenario 4: Failover (Colony Crashes)

**Setup:**

- Colony A running as `prod-colony`
- 100 agents connected
- Colony A crashes (power loss, OOM, etc.)

**Timeline:**

```
T0: Normal operation
    - Colony A sending heartbeats every 30s
    - Lease renewed continuously
    - Agents connected via WireGuard

T1: Colony A crashes
    - Heartbeats stop
    - WireGuard tunnels remain up (for now)
    - Lease still valid for ~30s

T2: 60s after last heartbeat (lease expires)
    - Discovery Service marks "prod-colony" as unavailable
    - Future agent queries return "not_found"

T3: Agents detect disconnect (WireGuard keepalive fails)
    - Agents retry connection every 10s
    - Query Discovery Service: "not_found"
    - Agents buffer data locally, wait for Colony

T4: Colony A restarts (or Colony B takes over)
    - Colony registers as "prod-colony"
    - Discovery Service accepts (old lease expired)

    POST /register { colony_id: "prod-colony", endpoint: "203.0.113.5:51820" }
    → Success

T5: Agents retry, query Discovery Service
    - GET /colonies/prod-colony
    - Get new endpoint (may be same or different IP)
    - Re-establish WireGuard tunnel
    - Reconnect, sync buffered data
```

**Downtime:** ~60s (lease expiration window) + reconnection time.

**For faster failover, see High Availability below.**

### Scenario 5: High Availability (Leader Election)

**Problem with naive approach:** Two Colonies with same ID → split-brain
rejected by Discovery Service.

**Solution:** Use **Raft consensus** for HA, only leader registers.

**Setup:**

- 3 Colony instances: `colony-1`, `colony-2`, `colony-3`
- All configured with `colony_id: prod-colony`
- Raft consensus elects leader

**How it works:**

```yaml
# Colony configuration (all 3 instances)
colony:
    id: prod-colony
    ha:
        enabled: true
        raft:
            node_id: colony-1  # Unique per instance
            peers:
                - colony-2.internal:7001
                - colony-3.internal:7001
    discovery:
        url: https://discovery.coral.io
        register_if_leader: true  # Only leader registers
```

**Timeline:**

```
T0: All 3 Colonies start
    - Raft election occurs
    - Colony-1 elected leader

T1: Colony-1 (leader) registers with Discovery Service
    POST /register { colony_id: "prod-colony", endpoint: "203.0.113.5:51820" }
    → Success

T2: Colony-2 and Colony-3 (followers) do NOT register
    - They standby, sync state from leader
    - Do not attempt Discovery Service registration

T3: Agents connect
    - Query Discovery Service: "prod-colony"
    - Get Colony-1 endpoint
    - All agents connect to leader

T4: Colony-1 crashes
    - Raft detects leader failure
    - Colony-2 elected new leader (~2s)

T5: Colony-2 (new leader) registers with Discovery Service
    POST /register { colony_id: "prod-colony", endpoint: "203.0.113.6:51820" }
    → Success (Colony-1's lease may still be active, but this is the new leader)

    Note: Discovery Service should support "force register if Raft term higher"
    to handle immediate failover without waiting for lease expiration.

T6: Agents detect disconnect, retry
    - Query Discovery Service: "prod-colony"
    - Get Colony-2 endpoint (new leader)
    - Reconnect (~5s total downtime)
```

**Downtime:** ~5s (Raft election + reconnection) vs. 60s without HA.

**Implementation Note:** Raft-based HA is **Phase 2** (not MVP). Initially, only
single-Colony deployments supported.

## NAT Traversal

### NAT Scenarios

| Agent NAT     | Colony NAT | Method                   | Notes                                       |
|---------------|------------|--------------------------|---------------------------------------------|
| No            | No         | **Direct**               | Simplest, both have public IPs              |
| Yes           | No         | **Direct**               | Agent connects to Colony's public IP        |
| No            | Yes        | **STUN**                 | Colony discovers its public IP via STUN     |
| Yes           | Yes        | **STUN + Hole Punching** | Both sides coordinate via Discovery Service |
| Symmetric NAT | Any        | **TURN Relay**           | Fallback when hole punching fails           |

### STUN-like Endpoint Discovery

**Problem:** Colony behind NAT doesn't know its public IP.

**Solution:** Discovery Service acts as STUN server.

**Flow:**

```
1. Colony starts, binds to 0.0.0.0:51820

2. Colony sends UDP packet to Discovery Service
   UDP to discovery.coral.io:51821
   Payload: "STUN-like-request"

3. Discovery Service sees packet from 203.0.113.5:51820
   (Colony's public IP:port as seen by Discovery Service)

4. Discovery Service responds with observed endpoint
   Response: { "public_ip": "203.0.113.5", "public_port": 51820 }

5. Colony registers with discovered public endpoint
   POST /register { endpoint: "203.0.113.5:51820" }
```

**Agent uses this endpoint** to connect directly to Colony.

### Symmetric NAT (TURN-like Relay)

**Problem:** Both sides behind symmetric NAT, hole punching fails.

**Solution:** Discovery Service provides relay.

**Flow:**

```
1. Agent and Colony both connect to Discovery Service relay
   Agent → discovery.coral.io:51822
   Colony → discovery.coral.io:51822

2. Discovery Service proxies WireGuard packets
   Agent ←→ Discovery Relay ←→ Colony

3. WireGuard tunnel established through relay

4. Once tunnel up, data flows through relay
   (Performance impact: 2x latency, relay bandwidth cost)
```

**Fallback only when necessary.** Most deployments avoid this.

## Implementation Phases

### Phase 1: MVP (Simple, No HA)

**Features:**

- Lease-based registration (prevents split-brain)
- Agent discovery by Colony ID
- STUN-like endpoint discovery
- 60s failover window (acceptable for MVP)

**Limitations:**

- No HA (single Colony per ID)
- No Raft (manual failover required)
- No TURN relay (direct/STUN only)

**Config:**

```yaml
# Colony
colony:
    id: prod-colony
    discovery:
        url: https://discovery.coral.io
        lease_ttl: 60s
        heartbeat_interval: 30s

# Agent
agent:
    colony:
        id: prod-colony
        auto_discover: true
        discovery_url: https://discovery.coral.io
```

### Phase 2: High Availability

**Features:**

- Raft consensus for Colony HA
- Leader election (3 or 5 Colony instances)
- Fast failover (<5s instead of 60s)
- Discovery Service respects Raft term for immediate failover

**Config:**

```yaml
colony:
    id: prod-colony
    ha:
        enabled: true
        raft:
            node_id: colony-1
            peers:
                - colony-2.internal:7001
                - colony-3.internal:7001
            election_timeout: 2s
    discovery:
        url: https://discovery.coral.io
        register_if_leader: true
```

### Phase 3: Federation (Multi-Region)

**Features:**

- Reef layer coordinates multiple Colonies
- Cross-region queries route via Reef
- Each Colony has unique ID (`prod-us-east`, `prod-eu-west`)
- Discovery Service returns region hints for latency optimization

**Config:**

```yaml
# Reef
reef:
    id: global-reef
    colonies:
        -   id: prod-us-east
            region: us-east-1
            discovery_url: https://discovery.coral.io
        -   id: prod-eu-west
            region: eu-west-1
            discovery_url: https://discovery.coral.io

# Agent (with region hint)
agent:
    colony:
        id: prod  # Reef resolves to nearest: prod-us-east or prod-eu-west
        region_hint: us-east-1
        auto_discover: true
        discovery_url: https://discovery.coral.io
```

**Discovery Service enhancement:** Can return multiple Colonies for a prefix,
agent selects by region.

## Security Considerations

### Lease Hijacking

**Attack:** Attacker tries to register as existing Colony after lease expires.

**Mitigation:**

- **Short lease TTL** (60s) limits exposure window
- **Heartbeat required** (every 30s) ensures liveness
- **Future:** Cryptographic proof (sign registration with WireGuard private key)

### Denial of Service

**Attack:** Flood Discovery Service with registrations/queries.

**Mitigation:**

- **Rate limiting** per source IP
- **Authentication** for Colony registration (API keys, mTLS)
- **Agent queries are read-only** (lower risk)

### Man-in-the-Middle

**Attack:** Intercept agent-Colony connection via Discovery Service.

**Mitigation:**

- **Discovery Service returns public keys** (WireGuard)
- **Agent verifies Colony public key** matches expected
- **Pin Colony public key** in agent config for high-security envs

**Config:**

```yaml
agent:
    colony:
        id: prod-colony
        auto_discover: true
        public_key_pin: wg_pubkey_abc123...  # Must match or reject
```

## Discovery Service API Reference

### Endpoints

#### Register Colony

```http
POST /v1/colonies/register
Content-Type: application/json

Request:
{
  "colony_id": "string",
  "region": "string",
  "public_key": "string",
  "endpoint": "string",
  "lease_ttl": int,
  "metadata": {}
}

Response (Success):
{
  "status": "registered",
  "lease_id": "string",
  "expires_at": "timestamp"
}

Response (Conflict):
{
  "status": "conflict",
  "error": "string",
  "existing": {}
}
```

#### Heartbeat

```http
POST /v1/colonies/heartbeat
Content-Type: application/json

Request:
{
  "lease_id": "string",
  "status": "healthy|degraded",
  "agent_count": int,
  "load": float
}

Response:
{
  "status": "renewed",
  "expires_at": "timestamp"
}
```

#### Deregister

```http
DELETE /v1/colonies/{colony_id}
Content-Type: application/json

Request:
{
  "lease_id": "string",
  "reason": "string"
}

Response:
{
  "status": "deregistered"
}
```

#### Discover Colony

```http
GET /v1/colonies/{colony_id}

Response (Success):
{
  "colony_id": "string",
  "region": "string",
  "public_key": "string",
  "endpoint": "string",
  "lease_expires_at": "timestamp",
  "nat_traversal": {}
}

Response (Not Found):
{
  "status": "not_found",
  "error": "string",
  "available_colonies": []
}
```

#### List Colonies

```http
GET /v1/colonies

Response:
{
  "colonies": [
    {
      "colony_id": "string",
      "region": "string",
      "endpoint": "string",
      "status": "active|unavailable"
    }
  ]
}
```

## Running Discovery Service

### Self-Hosted (Docker)

```bash
docker run -d \
  --name coral-discovery \
  -p 443:443 \
  -p 51821:51821/udp \
  -e DISCOVERY_DOMAIN=discovery.mycompany.internal \
  -e LEASE_TTL=60s \
  coral-discovery:latest
```

### Kubernetes

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
    name: coral-discovery
spec:
    replicas: 3  # HA for Discovery Service itself
    template:
        spec:
            containers:
                -   name: discovery
                    image: coral-discovery:latest
                    ports:
                        -   containerPort: 443
                        -   containerPort: 51821
                            protocol: UDP
                    env:
                        -   name: DISCOVERY_DOMAIN
                            value: discovery.coral.svc.cluster.local
```

### Configuration

```yaml
# discovery.yaml
discovery:
    listen: 0.0.0.0:443
    stun_listen: 0.0.0.0:51821

    registry:
        lease_ttl: 60s
        heartbeat_grace: 10s  # Allow 10s late heartbeats

    nat_traversal:
        stun_enabled: true
        relay_enabled: false  # TURN-like relay (future)

    security:
        rate_limit:
            registrations_per_minute: 10
            queries_per_minute: 1000
        auth:
            require_api_key: true
            api_keys:
                -   key: "secret-colony-key"
                    name: "prod-colony"
```

## Troubleshooting

### Agent Cannot Find Colony

```bash
$ coral agent start
Error: Colony 'prod-colony' not found

# Check Discovery Service
curl https://discovery.coral.io/v1/colonies/prod-colony

# If not found, check Colony is running and registered
# On Colony machine:
curl -X POST https://discovery.coral.io/v1/colonies/register -d '{...}'
```

### Split-Brain Error

```bash
$ coral colony start
Error: Colony ID conflict. Another Colony with ID 'prod-colony' is running.

# Check if other Colony is running
curl https://discovery.coral.io/v1/colonies/prod-colony

# If duplicate is accidental, use different ID
colony:
  id: prod-colony-backup

# If failover needed, wait for lease expiration (60s) or force deregister
```

### NAT Traversal Failing

```bash
# Agent logs show connection timeout
Agent: Connecting to Colony at 203.0.113.5:51820
Agent: WireGuard handshake timeout

# Check if STUN endpoint detection worked
# On Colony:
curl -X POST https://discovery.coral.io/v1/stun/detect

# If symmetric NAT detected, may need relay (future)
Agent: NAT type: symmetric
Agent: Direct connection not possible, relay required
```

## Future Enhancements

### Geo-Distributed Discovery

- Multiple Discovery Service instances (US, EU, Asia)
- Gossip protocol for registry sync
- Agents query nearest Discovery Service

### Cryptographic Proof

- Colony signs registration with WireGuard private key
- Prevents lease hijacking by verifying ownership

### Metrics & Monitoring

- Discovery Service exports Prometheus metrics
- Track registration rate, query latency, NAT types
- Alert on anomalies (sudden Colony churn)

### TURN-like Relay

- Full TURN protocol implementation
- Fallback for symmetric NAT scenarios
- Load balancing across relay nodes
