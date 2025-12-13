# Coral Configuration Guide

This guide explains all configuration options for Coral colonies, agents, and
global settings.

## Table of Contents

- [Configuration Files](#configuration-files)
- [Global Configuration](#global-configuration)
- [Colony Configuration](#colony-configuration)
- [Project Configuration](#project-configuration)
- [Agent Configuration](#agent-configuration)
- [Environment Variables](#environment-variables)
- [Network Configuration Deep Dive](#network-configuration-deep-dive)
- [Examples](#examples)

## Configuration Files

Coral uses YAML configuration files stored in different locations:

| Config Type | Location                             | Purpose                             |
|-------------|--------------------------------------|-------------------------------------|
| **Global**  | `~/.coral/config.yaml`               | User-level settings and preferences |
| **Colony**  | `~/.coral/colonies/<colony-id>.yaml` | Per-colony identity and credentials |
| **Project** | `<project>/.coral/config.yaml`       | Project-local settings              |
| **Agent**   | `~/.coral/agents/<agent-id>.yaml`    | Agent-specific settings (future)    |

## Global Configuration

Location: `~/.coral/config.yaml`

```yaml
version: "1"
default_colony: "my-app-prod"  # Default colony to use

discovery:
    endpoint: "http://localhost:8080"
    timeout: 10s
    stun_servers:
        - "stun.cloudflare.com:3478"

ai:
    provider: "google"  # Currently only "google" is supported
    api_key_source: "env"  # or "keychain", "file"

    # Coral Ask configuration (RFD 030)
    ask:
        default_model: "google:gemini-2.0-flash-exp"
        api_keys:
            google: "env://GOOGLE_API_KEY"
        conversation:
            max_turns: 10
            context_window: 8192
            auto_prune: true
        agent:
            mode: "embedded"

preferences:
    auto_update_check: true
    telemetry_enabled: false
```

### Global Configuration Fields

| Field                           | Type     | Default                      | Description                                  |
|---------------------------------|----------|------------------------------|----------------------------------------------|
| `version`                       | string   | `"1"`                        | Configuration schema version                 |
| `default_colony`                | string   | -                            | Default colony ID to use when not specified  |
| `discovery.endpoint`            | string   | `http://localhost:8080`      | Discovery service URL                        |
| `discovery.timeout`             | duration | `10s`                        | Discovery request timeout                    |
| `discovery.stun_servers`        | []string | `[stun.cloudflare.com:3478]` | STUN servers for NAT traversal               |
| `ai.provider`                   | string   | `google`                     | AI provider: currently only `google`         |
| `ai.api_key_source`             | string   | `env`                        | API key source: `env`, `keychain`, or `file` |
| `preferences.auto_update_check` | bool     | `true`                       | Check for updates on startup                 |
| `preferences.telemetry_enabled` | bool     | `false`                      | Enable anonymous telemetry                   |

### AI Configuration (RFD 030)

The `ai.ask` section configures the local LLM agent for `coral ask` command. The
agent runs on your machine and connects to Colony's MCP server to access
observability data.

#### Supported Providers for `coral ask`

| Provider      | Model Examples                                               | API Key Required | Local/Cloud | MCP Tool Support | Status      |
|---------------|--------------------------------------------------------------|------------------|-------------|------------------|-------------|
| **Google**    | `gemini-2.0-flash-exp`, `gemini-1.5-pro`, `gemini-1.5-flash` | Yes              | Cloud       | ✅ Full           | ✅ Supported |
| **OpenAI**    | `gpt-4o`, `gpt-4o-mini`, `gpt-4-turbo`                       | Yes              | Cloud       | ⚠️ Pending       | 🚧 Planned  |
| **Anthropic** | `claude-3-5-sonnet`, `claude-3-opus`                         | Yes              | Cloud       | ⚠️ Pending       | 🚧 Planned  |
| **Ollama**    | `llama3.2`, `mistral`, `codellama`                           | No               | Local       | ⚠️ Pending       | 🚧 Planned  |
| **Grok**      | `grok-2-1212`, `grok-2-vision-1212`, `grok-beta`             | Yes              | Cloud       | ⚠️ Pending       | 🚧 Planned  |

> **Important:** `coral ask` requires MCP tool calling to access observability
> data from your colony.
>
> **Currently Supported:**
> - **Google Gemini**: Only provider currently implemented. Uses direct SDK
    integration with full MCP tool calling support.
>
> **Planned Providers:**
> - **OpenAI**: Implementation needed for GPT-4o and GPT-4o-mini support
> - **Anthropic**: Native tool calling support available, implementation planned
> - **Ollama**: For air-gapped/offline deployments
> - **Grok**: Evaluate tool calling support and implement if viable
>
> **Recommendation:** Use Google models for `coral ask`:
> - Production: `google:gemini-1.5-pro` (stable, long context)
> - Development: `google:gemini-2.0-flash-exp` (fast, experimental)
> - Cost-effective: `google:gemini-1.5-flash` (balanced)
>
> See `docs/PROVIDERS.md` for detailed implementation status and roadmap.

#### AI Ask Configuration Fields

| Field                                | Type     | Default    | Description                                      |
|--------------------------------------|----------|------------|--------------------------------------------------|
| `ai.ask.default_model`               | string   | -          | Default model (format: `provider:model-id`)      |
| `ai.ask.fallback_models`             | []string | `[]`       | Fallback models if primary fails                 |
| `ai.ask.api_keys`                    | map      | `{}`       | API keys (use `env://VAR_NAME` format)           |
| `ai.ask.conversation.max_turns`      | int      | `10`       | Maximum conversation turns to keep               |
| `ai.ask.conversation.context_window` | int      | `8192`     | Maximum tokens for context                       |
| `ai.ask.conversation.auto_prune`     | bool     | `true`     | Auto-prune old messages when limit reached       |
| `ai.ask.agent.mode`                  | string   | `embedded` | Agent mode: `embedded`, `daemon`, or `ephemeral` |

#### Model Format

Models are specified as `provider:model-id`:

**Google (Currently Supported):**

- `google:gemini-2.0-flash-exp` - Gemini 2.0 Flash (fast, experimental)
- `google:gemini-1.5-pro` - Gemini 1.5 Pro (long context window, most capable)
- `google:gemini-1.5-flash` - Gemini 1.5 Flash (balanced, cost-effective)

**Planned Providers (Not Yet Implemented):**

- `openai:gpt-4o` - Latest GPT-4 Omni (planned)
- `openai:gpt-4o-mini` - Faster, cheaper GPT-4 Omni (planned)
- `anthropic:claude-3-5-sonnet-20241022` - Claude 3.5 Sonnet (planned)
- `ollama:llama3.2` - Local Llama 3.2 (planned for air-gapped deployments)
- `ollama:mistral` - Local Mistral (planned)

**Current Limitation:**

If you specify a non-Google provider, you'll receive an error:
```
provider "openai" is not yet implemented

Currently supported:
  - google:gemini-2.0-flash-exp (fast, experimental)
  - google:gemini-1.5-pro (high quality, stable)
  - google:gemini-1.5-flash (balanced)
```

#### API Key Configuration

**IMPORTANT:** Never store API keys in plain text. Use environment variable
references:

```yaml
ai:
    ask:
        api_keys:
            openai: "env://OPENAI_API_KEY"       # ✅ Correct
            google: "env://GOOGLE_API_KEY"
            # openai: "sk-proj-abc123..."        # ❌ NEVER do this
```

**Setting API Keys:**

```bash
# In your shell profile (~/.bashrc, ~/.zshrc, etc.)
export OPENAI_API_KEY=sk-proj-your-key-here
export GOOGLE_API_KEY=your-google-key-here

# Or for specific session
export OPENAI_API_KEY=sk-proj-your-key-here
coral ask "your question"
```

**Getting API Keys:**

- **OpenAI**: https://platform.openai.com/api-keys
- **Google AI**: https://aistudio.google.com/app/apikey
- **Ollama**: No API key needed (runs locally)

#### Per-Colony Overrides

Override AI settings per colony for environment-specific models:

```yaml
# ~/.coral/colonies/my-app-prod.yaml
version: "1"
colony_id: "my-app-prod"

ask:
    default_model: "openai:gpt-4o"  # Use better model for production
    fallback_models:
        - "google:gemini-2.0-flash-exp"
```

## Colony Configuration

Locations (in priority order):
1. `~/.coral/colonies/<colony-id>/config.yaml` (User-specific)
2. `/etc/coral/colonies/<colony-id>.yaml` (System-wide, multi-colony)
3. `/etc/coral/colony.yaml` (System-wide, single-colony - only if `colony_id` matches)

```yaml
version: "1"
colony_id: "my-app-prod"
application_name: "MyApp"
environment: "production"
colony_secret: "your-secret-here"  # Keep this secure!

wireguard:
    private_key: "base64-encoded-key"
    public_key: "base64-encoded-key"
    port: 41580
    public_endpoints: # Optional: configure multiple public endpoints
        - "colony.example.com:9000"
        - "192.168.5.2:9000"
        - "10.0.0.5:9000"
    mesh_ipv4: "100.64.0.1"
    mesh_network_ipv4: "100.64.0.0/10"
    mesh_ipv6: "fd42::1"
    mesh_network_ipv6: "fd42::/48"
    mtu: 1420
    persistent_keepalive: 25

services:
    connect_port: 9000
    dashboard_port: 3000

storage_path: "~/.coral"

discovery:
    enabled: true
    mesh_id: "my-app-prod"  # Usually matches colony_id
    auto_register: true
    register_interval: 60s
    stun_servers:
        - "stun.cloudflare.com:3478"

mcp:
    disabled: false  # Enable MCP server by default
    enabled_tools: [ ]  # Empty = all tools enabled
    security:
        require_rbac_for_actions: false
        audit_enabled: false

created_at: "2025-01-15T10:30:00Z"
created_by: "user@example.com"
last_used: "2025-01-15T14:22:00Z"
```

### Colony Configuration Fields

#### Core Identity

| Field              | Type      | Required | Description                                        |
|--------------------|-----------|----------|----------------------------------------------------|
| `version`          | string    | Yes      | Configuration schema version                       |
| `colony_id`        | string    | Yes      | Unique identifier for this colony                  |
| `application_name` | string    | Yes      | Application name                                   |
| `environment`      | string    | Yes      | Environment (e.g., `production`, `staging`, `dev`) |
| `colony_secret`    | string    | Yes      | Secret for agent authentication                    |
| `storage_path`     | string    | No       | Path for colony data storage                       |
| `created_at`       | timestamp | Auto     | Colony creation timestamp                          |
| `created_by`       | string    | Auto     | Creator identifier                                 |
| `last_used`        | timestamp | Auto     | Last usage timestamp                               |

#### WireGuard Mesh Network

| Field                            | Type     | Default         | Description                                        |
|----------------------------------|----------|-----------------|----------------------------------------------------|
| `wireguard.private_key`          | string   | Auto-generated  | WireGuard private key (base64)                     |
| `wireguard.public_key`           | string   | Auto-generated  | WireGuard public key (base64)                      |
| `wireguard.port`                 | int      | `41580`         | WireGuard UDP listen port                          |
| `wireguard.public_endpoints`     | []string | `[]`            | Public endpoints for agent connections (see below) |
| `wireguard.interface_name`       | string   | `wg0`           | Network interface name                             |
| `wireguard.mesh_ipv4`            | string   | `100.64.0.1`    | Colony's IPv4 address in mesh                      |
| `wireguard.mesh_network_ipv4`    | string   | `100.64.0.0/10` | IPv4 mesh subnet (CIDR)                            |
| `wireguard.mesh_ipv6`            | string   | `fd42::1`       | Colony's IPv6 address in mesh                      |
| `wireguard.mesh_network_ipv6`    | string   | `fd42::/48`     | IPv6 mesh subnet (CIDR)                            |
| `wireguard.mtu`                  | int      | `1420`          | Interface MTU (1500 - 80 overhead)                 |
| `wireguard.persistent_keepalive` | int      | `25`            | Keepalive interval in seconds                      |

#### Services

| Field                     | Type | Default | Description                      |
|---------------------------|------|---------|----------------------------------|
| `services.connect_port`   | int  | `9000`  | Colony Connect/gRPC service port |
| `services.dashboard_port` | int  | `3000`  | Dashboard web UI port            |

#### Discovery

| Field                         | Type     | Default                      | Description                           |
|-------------------------------|----------|------------------------------|---------------------------------------|
| `discovery.enabled`           | bool     | `true`                       | Enable discovery service registration |
| `discovery.mesh_id`           | string   | Same as `colony_id`          | Mesh identifier for discovery         |
| `discovery.auto_register`     | bool     | `true`                       | Auto-register with discovery service  |
| `discovery.register_interval` | duration | `60s`                        | Registration refresh interval         |
| `discovery.stun_servers`      | []string | `[stun.cloudflare.com:3478]` | STUN servers for NAT discovery        |

#### MCP Server (Model Context Protocol)

| Field                                   | Type     | Default    | Description                      |
|-----------------------------------------|----------|------------|----------------------------------|
| `mcp.disabled`                          | bool     | `false`    | Disable MCP server               |
| `mcp.enabled_tools`                     | []string | `[]` (all) | Restrict available tools         |
| `mcp.security.require_rbac_for_actions` | bool     | `false`    | Require RBAC for exec/shell/eBPF |
| `mcp.security.audit_enabled`            | bool     | `false`    | Enable MCP tool call auditing    |

#### Beyla Observability (RFD 032, RFD 036)

Beyla metrics and traces are collected from agents and stored in the colony's
DuckDB. Retention policies control how long data is kept.

| Field                         | Type | Default | Description                                              |
|-------------------------------|------|---------|----------------------------------------------------------|
| `beyla.poll_interval`         | int  | `60`    | Interval (seconds) to poll agents for Beyla data         |
| `beyla.retention.http_days`   | int  | `30`    | Retention period for HTTP metrics (days)                 |
| `beyla.retention.grpc_days`   | int  | `30`    | Retention period for gRPC metrics (days)                 |
| `beyla.retention.sql_days`    | int  | `14`    | Retention period for SQL metrics (days)                  |
| `beyla.retention.traces_days` | int  | `7`     | Retention period for distributed traces (days) (RFD 036) |

**Example Configuration:**

```yaml
beyla:
    poll_interval: 60  # Poll agents every 60 seconds
    retention:
        http_days: 30   # Keep HTTP metrics for 30 days
        grpc_days: 30   # Keep gRPC metrics for 30 days
        sql_days: 14    # Keep SQL metrics for 14 days
        traces_days: 7  # Keep traces for 7 days
```

**Storage Considerations:**

- **Traces are high-volume:** Distributed traces generate significantly more
  data than RED metrics. A 7-day retention is recommended for production
  workloads.
- **Adjust based on throughput:** High-traffic services may need shorter
  retention (3-5 days) to manage storage size.
- **Metrics vs Traces:** HTTP/gRPC metrics are aggregated histograms (lower
  storage), while traces store individual request spans (higher storage).

**Defaults:** If not specified in config, the following defaults are used:

- Poll interval: 60 seconds
- HTTP/gRPC retention: 30 days
- SQL retention: 14 days
- Trace retention: 7 days

#### System Metrics (RFD 071)

System metrics are collected from agents and aggregated by the colony for
infrastructure observability. The colony polls agents for host-level metrics
(CPU, memory, disk, network) and stores aggregated summaries.

| Field                              | Type | Default | Description                                      |
|------------------------------------|------|---------|--------------------------------------------------|
| `system_metrics.poll_interval`     | int  | `60`    | Interval (seconds) to poll agents for metrics   |
| `system_metrics.retention_days`    | int  | `30`    | Retention period for aggregated summaries (days) |

**Example Configuration:**

```yaml
system_metrics:
    poll_interval: 60      # Poll agents every 60 seconds
    retention_days: 30     # Keep summaries for 30 days
```

**How It Works:**

- **Agent-side:** Agents collect system metrics every 15 seconds and store
  locally for 1 hour
- **Colony-side:** Colony polls agents every 60 seconds, aggregates into
  1-minute summaries (min/max/avg/p95)
- **Storage:** Summaries stored for 30 days (75% reduction vs raw data)
- **Query:** Integrated into `coral query summary` for infrastructure context

**Defaults:** If not specified in config:

- Poll interval: 60 seconds
- Retention: 30 days

## Project Configuration

Location: `<project>/.coral/config.yaml`

```yaml
version: "1"
colony_id: "my-app-prod"

dashboard:
    port: 3000
    enabled: true

storage:
    path: ".coral"  # Relative to project root
```

### Project Configuration Fields

| Field               | Type   | Default  | Description                       |
|---------------------|--------|----------|-----------------------------------|
| `version`           | string | `"1"`    | Configuration schema version      |
| `colony_id`         | string | Required | Links project to specific colony  |
| `dashboard.port`    | int    | `3000`   | Dashboard port override           |
| `dashboard.enabled` | bool   | `true`   | Enable dashboard for this project |
| `storage.path`      | string | `.coral` | Storage path relative to project  |

## Agent Configuration

Locations (in priority order):
1. `agent.yaml` (Local)
2. `/etc/coral/agent.yaml` (System-wide)

```yaml
version: "1"
agent_id: "my-service-agent"

telemetry:
    enabled: false
    endpoint: "127.0.0.1:4317"
    filters:
        always_capture_errors: true
        latency_threshold_ms: 500.0
        sample_rate: 0.10
```

### Agent Configuration Fields

| Field                                     | Type              | Default          | Description                            |
|-------------------------------------------|-------------------|------------------|----------------------------------------|
| `version`                                 | string            | `"1"`            | Configuration schema version           |
| `agent_id`                                | string            | Required         | Unique agent identifier                |
| `telemetry.enabled`                       | bool              | `false`          | Enable OpenTelemetry collection        |
| `telemetry.endpoint`                      | string            | `127.0.0.1:4317` | OTLP endpoint                          |
| `telemetry.filters.always_capture_errors` | bool              | `true`           | Always capture error traces            |
| `telemetry.filters.latency_threshold_ms`  | float             | `500.0`          | Latency threshold for capture          |
| `telemetry.filters.sample_rate`           | float             | `0.10`           | Sample rate (0.0-1.0)                  |
| `beyla.disabled`                          | bool              | `false`          | Disable Beyla eBPF instrumentation     |
| `beyla.discovery.services`                | []Service         | `[]`             | List of services to instrument         |
| `beyla.protocols.http.enabled`            | bool              | `true`           | Enable HTTP instrumentation            |
| `beyla.protocols.http.route_patterns`     | []string          | `[]`             | URL patterns for cardinality reduction |
| `beyla.protocols.grpc.enabled`            | bool              | `true`           | Enable gRPC instrumentation            |
| `beyla.protocols.sql.enabled`             | bool              | `true`           | Enable SQL instrumentation             |
| `beyla.protocols.sql.obfuscate_queries`   | bool              | `true`           | Obfuscate SQL query literals           |
| `beyla.attributes`                        | map[string]string | `{}`             | Custom attributes for metrics/traces   |
| `beyla.sampling.rate`                     | float             | `1.0`            | Trace sampling rate (0.0-1.0)          |
| `beyla.limits.max_traced_connections`     | int               | `1000`           | Max concurrent tracked connections     |
| `beyla.otlp_endpoint`                     | string            | `localhost:4318` | OTLP export endpoint                   |
| `debug.enabled`                           | bool              | `true`           | Enable debug session capability        |
| `debug.sdk_api.timeout`                   | duration          | `5s`             | Timeout for SDK communication          |
| `debug.limits.max_concurrent_sessions`    | int               | `5`              | Max concurrent debug sessions          |
| `debug.limits.max_session_duration`       | duration          | `10m`            | Max duration for a debug session       |
| `debug.limits.max_events_per_second`      | int               | `10000`          | Rate limit for debug events            |
| `system_metrics.disabled`                 | bool              | `false`          | Disable system metrics collection      |
| `system_metrics.interval`                 | duration          | `15s`            | Collection interval                    |
| `system_metrics.retention`                | duration          | `1h`             | Local retention period                 |
| `system_metrics.cpu_enabled`              | bool              | `true`           | Collect CPU metrics                    |
| `system_metrics.memory_enabled`           | bool              | `true`           | Collect memory metrics                 |
| `system_metrics.disk_enabled`             | bool              | `true`           | Collect disk I/O metrics               |
| `system_metrics.network_enabled`          | bool              | `true`           | Collect network I/O metrics            |

### Beyla Integration Configuration

The `beyla` section configures the eBPF-based auto-instrumentation for the
agent. This allows for zero-code observability of HTTP, gRPC, SQL, and other
protocols.

```yaml
beyla:
    disabled: false  # Enabled by default


    # Discovery: which processes to instrument
    discovery:
        services:
            # Instrument service listening on port 8080
            -   name: "checkout-api"
                open_port: 8080

            # Instrument service by Kubernetes pod name pattern
            -   name: "payments-api"
                k8s_pod_name: "payments-*"
                k8s_namespace: "prod"

            # Instrument service by Kubernetes labels
            -   name: "inventory-svc"
                k8s_namespace: "prod"
                k8s_pod_label:
                    app: "inventory"
                    version: "v2"

    # Protocol-specific configuration
    protocols:
        http:
            enabled: true
            capture_headers: false  # Privacy: don't store header values
            route_patterns: # Cardinality reduction
                - "/api/v1/users/:id"
                - "/api/v1/orders/:id"
                - "/api/v1/products/:id"

        grpc:
            enabled: true

        sql:
            enabled: true
            obfuscate_queries: true  # Replace literals with "?"

        kafka:
            enabled: false

        redis:
            enabled: false

    # Attributes to add to all metrics/traces
    attributes:
        environment: "production"
        cluster: "us-west-2"
        colony_id: "colony-abc123"
        region: "us-west-2"

    # Performance tuning
    sampling:
        rate: 1.0  # 100% sampling (adjust if overhead too high)

    # Resource limits
    limits:
        max_traced_connections: 1000  # Prevent memory exhaustion
        ring_buffer_size: 65536

    # OTLP endpoint (local receiver from RFD 025)
    otlp_endpoint: "localhost:4318"
```

### Debug Configuration (RFD 061)

The `debug` section configures the live debugging capability, which allows
attaching eBPF uprobes to running services to trace function execution.

```yaml
debug:
    enabled: true

    # SDK communication settings
    sdk_api:
        timeout: 5s
        retry_attempts: 3

    # Safety limits
    limits:
        max_concurrent_sessions: 5      # Max active sessions per agent
        max_session_duration: 10m       # Auto-detach after 10 minutes
        max_events_per_second: 10000    # Rate limit to prevent overhead
        max_memory_mb: 256              # Max memory for BPF maps
```

### System Metrics Configuration (RFD 071)

The `system_metrics` section configures host-level metrics collection (CPU,
memory, disk, network). Metrics are collected locally and polled by the colony
for aggregation and long-term storage.

```yaml
system_metrics:
    disabled: false         # Disable system metrics collection (enabled by default)
    interval: 15s           # Collection interval
    retention: 1h           # Local retention (agent-side)
    cpu_enabled: true       # Collect CPU utilization and time
    memory_enabled: true    # Collect memory usage and limits
    disk_enabled: true      # Collect disk I/O and usage
    network_enabled: true   # Collect network I/O and errors
```

**Configuration Fields:**

| Field              | Type     | Default | Description                                |
|--------------------|----------|---------|--------------------------------------------|
| `disabled`         | bool     | `false` | Master switch for system metrics           |
| `interval`         | duration | `15s`   | How often to sample system metrics         |
| `retention`        | duration | `1h`    | How long to keep raw samples locally       |
| `cpu_enabled`      | bool     | `true`  | Collect CPU utilization and time           |
| `memory_enabled`   | bool     | `true`  | Collect memory usage, limit, utilization   |
| `disk_enabled`     | bool     | `true`  | Collect disk I/O and usage                 |
| `network_enabled`  | bool     | `true`  | Collect network I/O and errors             |

**Collected Metrics:**

- **CPU:**
    - `system.cpu.utilization` - CPU usage percentage (0-100)
    - `system.cpu.time` - Cumulative CPU time (seconds)

- **Memory:**
    - `system.memory.usage` - Memory used (bytes)
    - `system.memory.limit` - Total memory available (bytes)
    - `system.memory.utilization` - Memory usage percentage (0-100)

- **Disk:**
    - `system.disk.io` - Disk I/O operations (reads/writes)
    - `system.disk.usage` - Disk space used (bytes)

- **Network:**
    - `system.network.io` - Network I/O (bytes sent/received)
    - `system.network.errors` - Network errors (packet loss, errors)

**Storage and Retention:**

- **Agent-side:** Raw samples stored locally for 1 hour (configurable)
- **Colony-side:** Aggregated into 1-minute summaries, stored for 30 days
- **Cleanup:** Automatic cleanup runs every 10 minutes on agent

**Performance Impact:**

- **Overhead:** \<1% CPU, minimal memory (\~10KB per hour)
- **Sampling:** 15-second intervals balance precision with overhead
- **Cardinality:** \~10-15 unique metric names per agent

**Disabling Collection:**

To disable system metrics entirely:

```yaml
system_metrics:
    disabled: true
```

To disable specific collectors:

```yaml
system_metrics:
    disabled: false
    cpu_enabled: true
    memory_enabled: true
    disk_enabled: false      # Disable disk metrics
    network_enabled: false   # Disable network metrics
```

## Environment Variables

Environment variables override configuration file values.

### Colony Environment Variables

| Variable                   | Overrides                     | Example                    | Description                                                            |
|----------------------------|-------------------------------|----------------------------|------------------------------------------------------------------------|
| `CORAL_COLONY_ID`          | -                             | `my-app-prod`              | Colony to start                                                        |
| `CORAL_COLONY_SECRET`      | `colony_secret`               | `secret123`                | Colony authentication secret                                           |
| `CORAL_DISCOVERY_ENDPOINT` | `discovery.endpoint`          | `http://discovery:8080`    | Discovery service URL                                                  |
| `CORAL_STORAGE_PATH`       | `storage_path`                | `/var/lib/coral`           | Storage directory path                                                 |
| `CORAL_PUBLIC_ENDPOINT`    | `wireguard.public_endpoints`  | `colony.example.com:41580` | **Production required:** Public WireGuard endpoint(s), comma-separated |
| `CORAL_MESH_SUBNET`        | `wireguard.mesh_network_ipv4` | `100.64.0.0/10`            | Mesh network subnet                                                    |

**Multiple Endpoints Example:**

```bash
CORAL_PUBLIC_ENDPOINT=192.168.5.2:9000,10.0.0.5:9000,colony.example.com:9000
```

### Agent Environment Variables

| Variable                   | Description           |
|----------------------------|-----------------------|
| `CORAL_AGENT_ID`           | Agent identifier      |
| `CORAL_COLONY_ID`          | Colony to connect to  |
| `CORAL_DISCOVERY_ENDPOINT` | Discovery service URL |

### Configuration Precedence

For each setting, Coral uses the following priority order:

1. **Environment variable** (highest priority)
2. **Project configuration** (`<project>/.coral/config.yaml`)
3. **Colony configuration** (`~/.coral/colonies/<colony-id>.yaml`)
4. **Global configuration** (`~/.coral/config.yaml`)
5. **Default values** (lowest priority)

## Network Configuration Deep Dive

### Mesh Network Subnets

The mesh network subnet determines the IP address space for your WireGuard mesh.

#### Default: CGNAT Address Space (Recommended)

```yaml
wireguard:
    mesh_network_ipv4: "100.64.0.0/10"
    mesh_ipv4: "100.64.0.1"  # Auto-calculated if omitted
```

- **Address Range:** `100.64.0.0` - `100.127.255.255`
- **Capacity:** 4,194,304 addresses
- **Why CGNAT?** Defined by RFC 6598 for carrier-grade NAT, rarely used in
  enterprise networks
- **Conflicts:** Minimal - avoids common corporate/home router ranges

#### Alternative Subnets

```yaml
# RFC 1918 Private - Small deployment
wireguard:
    mesh_network_ipv4: "10.42.0.0/16"
    # Capacity: 65,534 addresses

# RFC 1918 Private - Medium deployment
wireguard:
    mesh_network_ipv4: "172.16.0.0/12"
    # Capacity: 1,048,574 addresses

# RFC 1918 Private - Home lab
wireguard:
    mesh_network_ipv4: "192.168.100.0/24"
    # Capacity: 254 addresses
```

#### Subnet Requirements

- **Minimum size:** `/24` (254 usable addresses)
- **Format:** Valid CIDR notation
- **Type:** IPv4 only (IPv6 in separate field)
- **Validation:** Automatically validated on colony startup

#### IP Allocation

| Address     | Purpose                                  |
|-------------|------------------------------------------|
| `.0`        | Network address (reserved)               |
| `.1`        | Colony address                           |
| `.2` - `.N` | Agent addresses (allocated sequentially) |

### Network Conflict Avoidance

#### Common Conflicts

| Network          | Used By                          | Conflict Risk |
|------------------|----------------------------------|---------------|
| `10.0.0.0/8`     | Corporate networks, VPNs, Docker | **High**      |
| `172.16.0.0/12`  | Docker, cloud providers          | **Medium**    |
| `192.168.0.0/16` | Home routers                     | **Medium**    |
| `100.64.0.0/10`  | CGNAT (rarely used)              | **Low**       |

#### Choosing a Subnet

**For Production:**

```yaml
wireguard:
    mesh_network_ipv4: "100.64.0.0/10"  # Best choice - minimal conflicts
```

**For Development (avoiding Docker):**

```yaml
wireguard:
    mesh_network_ipv4: "100.64.0.0/16"  # Subset of CGNAT
```

**For Isolated Environments:**

```yaml
wireguard:
    mesh_network_ipv4: "10.42.0.0/16"  # Safe if no 10.x networks
```

### NAT Traversal and Public Endpoints

#### Production Deployment

For agents to connect from different machines, you **must** set the public
endpoint. You can configure a single endpoint or multiple endpoints for
redundancy and failover.

**Single Endpoint:**

```bash
CORAL_PUBLIC_ENDPOINT=colony.example.com:41580 coral colony start
```

**Multiple Endpoints (Recommended for Production):**

```bash
CORAL_PUBLIC_ENDPOINT=192.168.5.2:9000,10.0.0.5:9000,colony.example.com:9000 coral colony start
```

**Or via Config File:**

```yaml
wireguard:
    port: 51820
    public_endpoints:
        - 192.168.5.2:9000
        - 10.0.0.5:9000
        - colony.example.com:9000
```

**Why Multiple Endpoints?**

- **Redundancy**: Failover capability: If one connection or endpoint becomes
  unresponsive, the system can automatically switch to another active one,
  ensuring high availability.
- **Multi-homing**: Support for different network types or protocols (e.g.,
  IPv4, IPv6, or specialized interfaces like VPNs) on the same host or device.
- **Local Container Networks**: To allow services (like your "colony" process)
  running directly on the host machine to be reliably accessed by containerized
  applications within a Docker network. This typically involves using a specific
  bridge or host-IP address that Docker exposes.
- **Network path diversity**: Utilizing connections from multiple distinct
  network providers or paths (e.g., different ISPs) to reduce the risk of a
  single point of failure affecting connectivity.

**Priority Order:**

1. `CORAL_PUBLIC_ENDPOINT` environment variable (highest)
2. `wireguard.public_endpoints` in colony YAML config
3. `127.0.0.1:<port>` localhost fallback (development only)

**Note:** The ports you specify in endpoints are informational; the actual
WireGuard connection uses the port configured in `wireguard.port`. The endpoint
host portion is extracted and combined with the WireGuard port.

Or configure STUN for automatic NAT discovery:

```yaml
discovery:
    stun_servers:
        - "stun.cloudflare.com:3478"
        - "stun.l.google.com:19302"
```

#### Local Development

For local development (all processes on same machine):

```bash
coral colony start  # Uses 127.0.0.1:<port>
```

### Mesh IP vs Public Endpoint

Understanding the difference:

- **Mesh IP** (`mesh_ipv4`): Address **inside** the WireGuard tunnel
    - Used for service-to-service communication
    - Example: `100.64.0.1`

- **Public Endpoint(s)**: Address(es) **outside** the tunnel
    - Used to establish WireGuard connection
    - Can specify multiple for redundancy
    - Examples:
        - Single: `colony.example.com:41580`
        - Multiple: `192.168.5.2:9000,10.0.0.5:9000,colony.example.com:9000`

The public endpoints are registered to the discovery service when Colony starts
up.

```yaml
# Mesh configuration (internal)
wireguard:
    mesh_ipv4: "100.64.0.1"          # Internal tunnel address
    mesh_network_ipv4: "100.64.0.0/10"

    # Public endpoints (external) - multiple for redundancy
    public_endpoints:
        - "colony.example.com:9000"
        - "192.168.5.2:9000"

# Or set via environment (overrides config file)
# CORAL_PUBLIC_ENDPOINT=colony.example.com:41580
# CORAL_PUBLIC_ENDPOINT=192.168.5.2:9000,10.0.0.5:9000,colony.example.com:9000
```

## Examples

### Example 1: Development Setup

**Global Config** (`~/.coral/config.yaml`):

```yaml
version: "1"
default_colony: "myapp-dev"

discovery:
    endpoint: "http://localhost:8080"

ai:
    provider: "google"
    api_key_source: "env"
    ask:
        default_model: "google:gemini-2.0-flash-exp"
        api_keys:
            google: "env://GOOGLE_API_KEY"
```

**Colony Config** (`~/.coral/colonies/myapp-dev.yaml`):

```yaml
version: "1"
colony_id: "myapp-dev"
application_name: "MyApp"
environment: "development"
colony_secret: "dev-secret-123"

wireguard:
    port: 41580
    mesh_network_ipv4: "100.64.0.0/16"  # Smaller subnet for dev

services:
    connect_port: 9000
    dashboard_port: 3000
```

**Start Colony:**

```bash
coral colony start
```

### Example 2: Production Setup with Custom Subnet

**Colony Config** (`~/.coral/colonies/myapp-prod.yaml`):

```yaml
version: "1"
colony_id: "myapp-prod"
application_name: "MyApp"
environment: "production"
colony_secret: "prod-secret-xyz"

wireguard:
    port: 41580
    mesh_network_ipv4: "100.64.0.0/10"  # Full CGNAT range

services:
    connect_port: 9000
    dashboard_port: 3000

discovery:
    enabled: true
    auto_register: true
    register_interval: 30s
    stun_servers:
        - "stun.cloudflare.com:3478"
        - "stun.l.google.com:19302"
```

**Start Colony:**

```bash
CORAL_PUBLIC_ENDPOINT=colony.example.com:41580 coral colony start
```

### Example 3: Environment Variable Overrides

```bash
# Override mesh subnet for testing
CORAL_MESH_SUBNET=172.16.0.0/12 \
CORAL_PUBLIC_ENDPOINT=192.168.1.100:41580 \
CORAL_DISCOVERY_ENDPOINT=http://discovery.internal:8080 \
coral colony start --colony-id myapp-test
```

### Example 4: Multi-Colony Setup

**Production Colony:**

```yaml
# ~/.coral/colonies/myapp-prod.yaml
colony_id: "myapp-prod"
environment: "production"
wireguard:
    port: 41580
    mesh_network_ipv4: "100.64.0.0/16"
```

**Staging Colony:**

```yaml
# ~/.coral/colonies/myapp-staging.yaml
colony_id: "myapp-staging"
environment: "staging"
wireguard:
    port: 41581
    mesh_network_ipv4: "100.65.0.0/16"
```

Each colony gets its own isolated mesh network with no IP conflicts.

### Example 5: Multiple Public Endpoints for High Availability

**Colony Config with Redundant Endpoints:**

```yaml
version: "1"
colony_id: "myapp-prod"
application_name: "MyApp"
environment: "production"

wireguard:
    port: 51820
    # Configure multiple endpoints for redundancy
    public_endpoints:
        - "colony-primary.example.com:9000"    # Primary DNS
        - "203.0.113.10:9000"                  # Primary IP (fallback)
        - "colony-secondary.example.com:9000"  # Secondary DNS
        - "198.51.100.20:9000"                 # Secondary IP (failover)
    mesh_network_ipv4: "100.64.0.0/10"

discovery:
    enabled: true
    auto_register: true
    stun_servers:
        - "stun.cloudflare.com:3478"
        - "stun.l.google.com:19302"
```

**Start Colony:**

```bash
coral colony start
```

Agents will attempt to connect using all configured endpoints, providing
automatic
failover if any endpoint becomes unavailable.

**Or Override with Environment Variable:**

```bash
CORAL_PUBLIC_ENDPOINT=192.168.1.10:9000,10.0.0.5:9000 coral colony start
```

### Example 6: High Security Setup

**Colony Config with MCP Security:**

```yaml
version: "1"
colony_id: "myapp-secure"
environment: "production"

wireguard:
    mesh_network_ipv4: "100.64.0.0/10"

mcp:
    disabled: false
    security:
        require_rbac_for_actions: true  # Require auth for exec/shell
        audit_enabled: true             # Log all MCP calls
```

### Example 7: Observability with Custom Retention

**Colony Config with Beyla Observability:**

```yaml
version: "1"
colony_id: "myapp-prod"
application_name: "MyApp"
environment: "production"

wireguard:
    mesh_network_ipv4: "100.64.0.0/10"
    public_endpoints:
        - "colony.example.com:9000"

services:
    connect_port: 9000
    dashboard_port: 3000

# Beyla metrics and distributed tracing (RFD 032, RFD 036)
beyla:
    poll_interval: 30  # Poll agents every 30 seconds (more frequent)
    retention:
        http_days: 90     # Keep HTTP metrics for 90 days
        grpc_days: 90     # Keep gRPC metrics for 90 days
        sql_days: 30      # Keep SQL metrics for 30 days
        traces_days: 14   # Keep traces for 14 days (extended retention)
```

**Start Colony:**

```bash
CORAL_PUBLIC_ENDPOINT=colony.example.com:9000 coral colony start
```

**Storage Impact:**

For a high-traffic service (1000 req/s):

- **Metrics:** ~50 MB/day (aggregated histograms)
- **Traces:** ~5 GB/day (individual request spans)
- **14-day trace retention:** ~70 GB total

Consider shorter trace retention (7 days) or sampling for very high throughput
services.

### Example 8: AI-Powered Diagnostics with Coral Ask

**Global Config** (`~/.coral/config.yaml`):

```yaml
version: "1"
default_colony: "myapp-prod"

discovery:
    endpoint: "http://localhost:8080"

ai:
    provider: "google"  # Currently only Google is supported
    api_key_source: "env"

    # Configure coral ask (RFD 030)
    ask:
        default_model: "google:gemini-2.0-flash-exp"
        api_keys:
            google: "env://GOOGLE_API_KEY"
        conversation:
            max_turns: 10
            context_window: 8192
            auto_prune: true
        agent:
            mode: "embedded"
```

**Production Colony Override** (`~/.coral/colonies/myapp-prod.yaml`):

```yaml
version: "1"
colony_id: "myapp-prod"
application_name: "MyApp"
environment: "production"

# Use more stable model for production troubleshooting
ask:
    default_model: "google:gemini-1.5-pro"  # More capable, stable model

wireguard:
    mesh_network_ipv4: "100.64.0.0/10"
    public_endpoints:
        - "colony.example.com:9000"

mcp:
    disabled: false  # MCP server required for coral ask
```

**Setup:**

```bash
# 1. Set API key
export GOOGLE_API_KEY=your-google-key-here

# 2. Start colony
coral colony start

# 3. Ask questions about your application
coral ask "what services are currently running?"
coral ask "show me HTTP latency for the API service"
coral ask "why is checkout slow?"

# 4. Multi-turn conversations
coral ask "what's the p95 latency?"
coral ask "show me the slowest endpoints" --continue

# 5. Override model for specific queries
coral ask "complex root cause analysis" --model google:gemini-1.5-pro

# 6. JSON output for scripting
coral ask "list unhealthy services" --json

# 7. Use different Gemini models
coral ask "quick status check" --model google:gemini-1.5-flash  # Faster
coral ask "deep analysis" --model google:gemini-1.5-pro  # More capable
```

**Key Features:**

- **MCP Integration:** LLM accesses all Colony MCP tools (service health,
  traces, metrics, logs)
- **Google Gemini:** Currently supported provider with full tool calling
- **Conversation Context:** Multi-turn conversations with automatic context
  pruning
- **Per-Colony Models:** Use faster models for dev, more capable for production
- **Model Selection:** Choose between speed (Flash) and quality (Pro)

**Future Support:** OpenAI, Anthropic, and Ollama providers are planned but not
yet implemented. See `docs/PROVIDERS.md` for implementation status.

## Configuration Validation

Coral validates configuration on startup and provides clear error messages:

```bash
$ coral colony start
Error: failed to resolve mesh subnet: invalid mesh_network_ipv4 in config:
mesh subnet "10.42.0.0/25" is too small (/25), minimum is /24
```

### Validation Rules

- **Mesh subnet:** Minimum `/24`, valid CIDR, IPv4 only
- **Ports:** Valid port numbers (1-65535)
- **Timeouts:** Positive duration values
- **Colony ID:** Non-empty, valid identifier
- **API keys:** Required when `ai.api_key_source` is `env`

## Troubleshooting

### Common Issues

**Issue:** Agents can't connect to colony

```yaml
# Solution: Check public endpoint
```

```bash
CORAL_PUBLIC_ENDPOINT=your-public-ip:41580 coral colony start
```

**Issue:** IP address conflicts

```yaml
# Solution: Use CGNAT address space
wireguard:
    mesh_network_ipv4: "100.64.0.0/10"
```

**Issue:** Colony won't start with custom subnet

```bash
# Check validation error and adjust subnet size
wireguard:
  mesh_network_ipv4: "10.42.0.0/24"  # Must be /24 or larger
```
