# Coral Usage Guide

Quick start guide for running Coral components and testing the CLI proxy functionality.

## Prerequisites

Build the binaries:

```bash
make build
```

This creates:
- `bin/coral` - Main CLI with all commands
- `bin/coral-discovery` - Discovery service

## Step 1: Start the Discovery Service

The discovery service enables colonies and proxies to find each other.

```bash
./bin/coral-discovery --port 8080 --ttl 300 --cleanup-interval 60
```

**Options:**
- `--port`: HTTP port for the discovery service (default: 8080)
- `--ttl`: Time-to-live for registrations in seconds (default: 300)
- `--cleanup-interval`: How often to clean up expired entries in seconds (default: 60)
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

---

## Step 2: Initialize a Colony

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

---

## Step 3: Start the Colony

Start the colony with automatic discovery registration:

### Local Development (same machine)

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

### Production Deployment (different machines)

**IMPORTANT:** For agents to connect from different machines, you **MUST** set `CORAL_PUBLIC_ENDPOINT` to your colony's publicly reachable IP or hostname:

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

In another terminal, query the discovery service:

```bash
curl http://localhost:8080/coral.discovery.v1.DiscoveryService/Health
```

You should see `registered_colonies: 1` in the response.

Keep the colony running in this terminal.

---

## Step 4: Start the Proxy

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

---

## Step 5: Validate Everything Works

### Check Discovery Service Health

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

### Query Colony via Discovery

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
  "endpoints": [":41581"],
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

### Test Proxy (when ColonyService is implemented)

Once the colony implements the ColonyService RPC handlers:

```bash
curl -X POST http://localhost:8000/coral.colony.v1.ColonyService/GetStatus \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

## Component Overview

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

## CLI Commands Reference

### Discovery Service

```bash
# Start discovery service
./bin/coral-discovery --port 8080 --ttl 300 --cleanup-interval 60

# With debug logging
./bin/coral-discovery --log-level debug
```

### Colony Management

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

### Proxy Management

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

### General

```bash
# Show version
./bin/coral version

# Show help
./bin/coral --help
./bin/coral proxy --help
./bin/coral colony --help
```

---

## Troubleshooting

### Discovery service not reachable

**Error:** `failed to lookup colony: Post "http://localhost:8080": connection refused`

**Solution:** Start the discovery service first:
```bash
./bin/coral-discovery --port 8080
```

### Colony not found

**Error:** `colony not found: my-app-dev-a1b2c3`

**Solutions:**
1. Verify colony is running: `./bin/coral colony status`
2. Check discovery registration: `curl http://localhost:8080/coral.discovery.v1.DiscoveryService/Health`
3. Wait up to 60 seconds for next heartbeat
4. Check colony logs for registration errors

### No mesh IP configured

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

### Port already in use

**Error:** `failed to listen on localhost:8000: address already in use`

**Solution:** Use a different port:
```bash
./bin/coral proxy start <colony-id> --listen localhost:8001
```

---

## Next Steps

### Implement ColonyService Handlers

The colony proto service is defined but not implemented. To make it functional:

1. Create colony server in `internal/colony/server.go`
2. Implement RPC handlers:
   - `GetStatus()` - Return colony health info
   - `ListAgents()` - Return connected agents
   - `GetTopology()` - Return network topology
3. Start Buf Connect server on `localhost:9000`
4. Register handlers with HTTP server

### Add WireGuard Integration

Currently the proxy doesn't establish actual WireGuard tunnels. To add:

1. Create WireGuard interface management in `internal/proxy/wireguard.go`
2. Configure peer with colony's public key
3. Set up routing to mesh subnet
4. Handle tunnel lifecycle (up/down)

### Add Agent Support

Enable agents to register and report metrics:

1. Implement agent registration in colony
2. Add agent heartbeat mechanism
3. Store agent data in colony database
4. Return agent info in `ListAgents()` RPC

---

## Architecture

This setup implements **RFD 005: CLI Access via Local Proxy**

**Flow:**
1. Colony registers with discovery service (mesh IPs, connect port)
2. Proxy queries discovery to find colony
3. Proxy starts local HTTP/2 server (localhost:8000)
4. CLI tools query proxy instead of colony directly
5. Proxy forwards requests to colony over mesh network (future: via WireGuard tunnel)

**Benefits:**
- CLI tools don't need WireGuard logic
- Centralized access point for multiple colonies
- Works across NATs and firewalls
- Foundation for future features (agent queries, multi-colony federation)
