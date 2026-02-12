# Coral Colony

The Coral Colony is the central coordinator of a Coral deployment. It aggregates
observations from agents, runs AI analysis, manages the WireGuard mesh network,
and provides a unified API for querying and debugging distributed applications.

## Table of Contents

- [Binaries](#binaries)
- [Command Reference](#command-reference)
    - [Lifecycle](#lifecycle)
    - [Querying](#querying)
    - [Debugging and Profiling](#debugging-and-profiling)
    - [DuckDB SQL Access](#duckdb-sql-access)
    - [Agent Management](#agent-management)
    - [Service Discovery](#service-discovery)
    - [Security and Access Control](#security-and-access-control)
    - [MCP Server](#mcp-server)
    - [Colony Context Management](#colony-context-management)
    - [Configuration](#configuration)
- [Deployment](#deployment)
    - [Docker](#docker)
    - [SystemD](#systemd)
- [Port Overview](#port-overview)

---

## Binaries

Coral provides two ways to run a colony:

| Binary         | Description                                           | Use case             |
|----------------|-------------------------------------------------------|----------------------|
| `coral`        | Full CLI with all commands (colony, agent, ask, etc.) | Development, testing |
| `coral-colony` | Colony-only server binary with ops tools              | Server deployments   |

The `coral-colony` binary has a **flat command hierarchy** -- commands are
registered directly at the root level:

```bash
# Server binary (flat)
coral-colony start
coral-colony token create ops --permissions admin

# Full CLI (nested under 'colony' subcommand)
coral colony start
coral colony token create ops --permissions admin
```

---

## Command Reference

### Lifecycle

```bash
# Initialize a new colony (generates ID, CA, PSK, WireGuard keys)
coral-colony init <name> --env <dev|staging|prod> --storage /var/lib/coral

# Start the colony server
coral-colony start [--colony <id>]

# Stop the colony daemon
coral-colony stop

# Show colony status and WireGuard configuration
coral-colony status
```

### Querying

Unified query interface for observability data. All commands connect to the
colony gRPC API.

```bash
# Service health overview
coral-colony query summary                    # List all services with telemetry
coral-colony query summary my-service          # Detailed service summary

# Distributed traces
coral-colony query traces my-service --since 1h
coral-colony query traces my-service --since 1h --format json

# Service metrics (eBPF + OTLP)
coral-colony query metrics my-service --metric http.server.duration
coral-colony query metrics my-service --metric http.server.duration --percentile 99

# Application logs
coral-colony query logs my-service --level error
coral-colony query logs my-service --since 30m

# Historical CPU profiles (folded stack format, pipe to flamegraph.pl)
coral-colony query cpu-profile my-service --since 1h
coral-colony query cpu-profile my-service --since 5m | flamegraph.pl > cpu.svg

# Historical memory profiles
coral-colony query memory-profile my-service --since 1h --show-growth

# Raw SQL queries against colony DuckDB
coral-colony query sql "SELECT service_name, COUNT(*) FROM beyla_http_metrics GROUP BY service_name"
```

### Debugging and Profiling

Function-level debugging using eBPF uprobes and on-demand profiling.

```bash
# Search for functions in a service binary
coral-colony debug search my-service --pattern "handleRequest"

# Get function details (address, parameters, source location)
coral-colony debug info my-service --function "main.handleRequest"

# Attach uprobe to a function
coral-colony debug attach my-service --function "main.handleRequest"

# Auto-profile multiple functions
coral-colony debug profile my-service --pattern "handler.*"

# Trace a request path across services
coral-colony debug trace my-service --request-id abc123

# Manage debug sessions
coral-colony debug session list
coral-colony debug session get <session-id>
coral-colony debug session stop <session-id>

# On-demand CPU profiling (high-frequency, 99Hz)
coral-colony profile cpu --service my-service --duration 30

# On-demand memory profiling
coral-colony profile memory --service my-service --duration 30
```

### DuckDB SQL Access

Direct SQL access to agent and colony DuckDB databases via HTTP remote attach.
Useful for ad-hoc analysis and investigating raw telemetry data.

```bash
# List available databases (colony and agents)
coral-colony duckdb list

# Interactive SQL shell
coral-colony duckdb shell agent-prod-1

# One-shot query
coral-colony duckdb query agent-prod-1 \
  "SELECT service_name, COUNT(*) FROM beyla_http_metrics_local GROUP BY service_name"

# Query with CSV output
coral-colony duckdb query agent-prod-1 "SELECT * FROM otel_spans_local LIMIT 10" --format csv
```

### Agent Management

```bash
# List connected agents with status
coral-colony agents
```

### Service Discovery

```bash
# List all services across agents
coral-colony service list
```

### Security and Access Control

#### API Tokens

Tokens authenticate external clients connecting to the colony's public HTTPS
endpoint.

```bash
# Create a token with specific permissions
coral-colony token create ops-dashboard --permissions status,query
coral-colony token create ci-pipeline --permissions query
coral-colony token create admin-user --permissions admin

# List all tokens
coral-colony token list

# Show token metadata
coral-colony token show ops-dashboard

# Revoke a token (disable without deleting)
coral-colony token revoke ops-dashboard

# Permanently delete a token
coral-colony token delete ops-dashboard
```

**Permission levels:**

| Permission | Access                                   |
|------------|------------------------------------------|
| `status`   | Colony status, agents, topology          |
| `query`    | Metrics, traces, logs                    |
| `analyze`  | AI analysis (may trigger shell commands) |
| `debug`    | Attach live eBPF probes                  |
| `admin`    | Full administrative access               |

#### Certificate Authority

The colony runs an embedded CA for agent mTLS authentication.

```bash
# Show CA status and fingerprint
coral-colony ca status

# Rotate intermediate CA certificate
coral-colony ca rotate-intermediate
```

#### Bootstrap PSK

Pre-shared key used to authorize initial agent certificate issuance.

```bash
# Show current PSK
coral-colony psk show

# Rotate the PSK (existing agents are unaffected)
coral-colony psk rotate
```

### MCP Server

The colony exposes observability and debugging tools via the Model Context
Protocol (MCP) for AI assistant integration.

```bash
# List available MCP tools
coral-colony mcp list-tools

# Test a tool locally
coral-colony mcp test-tool coral_get_service_health

# Generate Claude Desktop configuration
coral-colony mcp generate-config

# Start MCP server proxy (used by Claude Desktop)
coral-colony mcp proxy
```

### Colony Context Management

Manage multiple colony configurations on a single machine.

```bash
# List all configured colonies
coral-colony list

# Set the default colony
coral-colony use <colony-id>

# Show current default
coral-colony current

# Add a remote colony connection
coral-colony add-remote <name> <endpoint> [--insecure]

# Export/import colony credentials
coral-colony export > colony-creds.yaml
coral-colony import < colony-creds.yaml
```

### Configuration

```bash
# View and manage Coral configuration
coral-colony config --help
```

See [CONFIG.md](CONFIG.md) for detailed configuration options.

---

## Deployment

### Docker

The `coral-colony` image is optimized for server deployments:

```bash
# Build the colony image
make docker-build-colony

# Run with docker-compose (see docker-compose.yml)
docker-compose up -d colony
```

The image uses `debian:bookworm-slim` with runtime dependencies (
wireguard-tools,
iptables, iproute2) and runs as root for TUN device creation.

**Default entrypoint:** `coral-colony start`

### SystemD

For bare-metal deployments, a systemd unit is not yet provided for the colony.
Use the agent unit file at `deployments/systemd/coral-agent.service` as a
template.

---

## Port Overview

| Port      | Protocol             | Purpose                      | Access             |
|-----------|----------------------|------------------------------|--------------------|
| **9000**  | HTTP/2 (Connect RPC) | Colony gRPC API              | Agents (mesh), CLI |
| **8443**  | HTTPS                | Public endpoint (API tokens) | External clients   |
| **51820** | UDP                  | WireGuard mesh               | Agents             |

---

## See Also

- [Architecture](ARCHITECTURE.md) - System architecture overview
- [Agent](AGENT.md) - Agent documentation
- [Configuration](CONFIG.md) - Detailed configuration options
- [Security](SECURITY.md) - Security model
- [MCP](MCP.md) - MCP server integration
- [CLI Reference](CLI_REFERENCE.md) - Full CLI command reference
- [Deployment](DEPLOYMENT.md) - Deployment guide
