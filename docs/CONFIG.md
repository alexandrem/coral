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
| ----------- | ------------------------------------ | ----------------------------------- |
| **Global**  | `~/.coral/config.yaml`               | User-level settings and preferences |
| **Colony**  | `~/.coral/colonies/<colony-id>.yaml` | Per-colony identity and credentials |
| **Project** | `<project>/.coral/config.yaml`       | Project-local settings              |
| **Agent**   | `~/.coral/agents/<agent-id>.yaml`    | Agent-specific settings (future)    |

## Default Values

All default values are centralized in `internal/constants/defaults.go`.

### Ports

| Constant               | Value | Description                    |
| ---------------------- | ----- | ------------------------------ |
| `DefaultAgentPort`     | 9001  | Agent gRPC/Connect server port |
| `DefaultOTLPGRPCPort`  | 4317  | OTLP gRPC endpoint port        |
| `DefaultOTLPHTTPPort`  | 4318  | OTLP HTTP endpoint port        |
| `DefaultBeylaGRPCPort` | 4319  | Beyla gRPC endpoint port       |
| `DefaultBeylaHTTPPort` | 4320  | Beyla HTTP endpoint port       |

### Timeouts

| Constant                  | Value | Description               |
| ------------------------- | ----- | ------------------------- |
| `DefaultRPCTimeout`       | 10s   | RPC call timeout          |
| `DefaultQueryTimeout`     | 30s   | Database query timeout    |
| `DefaultHealthTimeout`    | 500ms | Health check timeout      |
| `DefaultSDKAPITimeout`    | 5s    | SDK API call timeout      |
| `DefaultDiscoveryTimeout` | 10s   | Discovery service timeout |

### Intervals

| Constant                       | Value | Description                        |
| ------------------------------ | ----- | ---------------------------------- |
| `DefaultPollInterval`          | 30s   | General polling interval           |
| `DefaultCleanupInterval`       | 1h    | Cleanup task interval              |
| `DefaultRegisterInterval`      | 60s   | Discovery registration interval    |
| `DefaultSystemMetricsInterval` | 15s   | System metrics collection interval |
| `DefaultCPUProfilingInterval`  | 15s   | CPU profiling collection interval  |
| `DefaultServicesPollInterval`  | 5m    | Services polling interval          |
| `DefaultBeylaPollInterval`     | 60s   | Beyla polling interval             |

### Retention Periods

| Constant                               | Value | Description                    |
| -------------------------------------- | ----- | ------------------------------ |
| `DefaultTelemetryRetention`            | 1h    | Telemetry data local retention |
| `DefaultSystemMetricsRetention`        | 1h    | System metrics local retention |
| `DefaultCPUProfilingRetention`         | 1h    | CPU profile sample retention   |
| `DefaultCPUProfilingMetadataRetention` | 7d    | Binary metadata retention      |

### Sampling and Filtering

| Constant                        | Value | Description                  |
| ------------------------------- | ----- | ---------------------------- |
| `DefaultSampleRate`             | 0.10  | Telemetry sample rate (10%)  |
| `DefaultBeylaSampleRate`        | 1.0   | Beyla sample rate (100%)     |
| `DefaultHighLatencyThresholdMs` | 500.0 | High latency threshold in ms |

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
        default_model: "google:gemini-3-fast"
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
| ------------------------------- | -------- | ---------------------------- | -------------------------------------------- |
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

| Provider      | Model Examples                                               | API Key Required | Local/Cloud | MCP Tool Support | Status       |
| ------------- | ------------------------------------------------------------ | ---------------- | ----------- | ---------------- | ------------ |
| **Google**    | `gemini-3-fast`                                              | Yes              | Cloud       | ✅ Full          | ✅ Supported |
| **OpenAI**    | `gpt-4o`, `gpt-4o-mini`                                      | Yes              | Cloud       | ✅ Full          | ✅ Supported |
| **Anthropic** | `claude-3-5-sonnet`, `claude-3-opus`                         | Yes              | Cloud       | ⚠️ Pending       | 🚧 Planned   |
| **Ollama**    | `llama3.2`, `mistral`, `codellama`                           | No               | Local       | ⚠️ Pending       | 🚧 Planned   |
| **Grok**      | `grok-2-1212`, `grok-2-vision-1212`, `grok-beta`             | Yes              | Cloud       | ⚠️ Pending       | 🚧 Planned   |

> **Important:** `coral ask` requires MCP tool calling to access observability
> data from your colony.
>
> **Currently Supported:**
>
> - **Google Gemini**: Uses direct SDK integration with full MCP tool calling support.
> - **OpenAI**: Uses official SDK with full MCP tool calling support. Compatible
>   with any OpenAI-compatible API.
>
> **Planned Providers:**
>
> - **Anthropic**: Native tool calling support available, implementation planned
> - **Ollama**: For air-gapped/offline deployments
> - **Grok**: Evaluate tool calling support and implement if viable
>
> **Recommendation:**
>
> - `google:gemini-3-fast` (fast, recommended)
> - `openai:gpt-4o` (high quality)
> - `openai:gpt-4o-mini` (fast, cost-effective)
>
> See `docs/PROVIDERS.md` for detailed implementation status and roadmap.

#### AI Ask Configuration Fields

| Field                                | Type     | Default    | Description                                      |
| ------------------------------------ | -------- | ---------- | ------------------------------------------------ |
| `ai.ask.default_model`               | string   | -          | Default model (format: `provider:model-id`)      |
| `ai.ask.fallback_models`             | []string | `[]`       | Fallback models if primary fails                 |
| `ai.ask.api_keys`                    | map      | `{}`       | API keys (use `env://VAR_NAME` format)           |
| `ai.ask.conversation.max_turns`      | int      | `10`       | Maximum conversation turns to keep               |
| `ai.ask.conversation.context_window` | int      | `8192`     | Maximum tokens for context                       |
| `ai.ask.conversation.auto_prune`     | bool     | `true`     | Auto-prune old messages when limit reached       |
| `ai.ask.agent.mode`                  | string   | `embedded` | Agent mode: `embedded`, `daemon`, or `ephemeral` |

#### Model Format

Models are specified as `provider:model-id`:

**Google:**

- `google:gemini-3-fast` - Gemini 3 Fast (fast, recommended)

**OpenAI:**

- `openai:gpt-4o` - GPT-4o (high quality)
- `openai:gpt-4o-mini` - GPT-4o-mini (fast, cost-effective)

**Planned Providers (Not Yet Implemented):**

- `anthropic:claude-3-5-sonnet-20241022` - Claude 3.5 Sonnet (planned)
- `ollama:llama3.2` - Local Llama 3.2 (planned for air-gapped deployments)
- `ollama:mistral` - Local Mistral (planned)

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
        - "google:gemini-3-fast"
```

## Colony Configuration

Locations (in priority order):

1. `~/.coral/colonies/<colony-id>/config.yaml` (User-specific)
2. `/etc/coral/colonies/<colony-id>.yaml` (System-wide, multi-colony)
3. `/etc/coral/colony.yaml` (System-wide, single-colony - only if `colony_id`
   matches)

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
| ------------------ | --------- | -------- | -------------------------------------------------- |
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
| -------------------------------- | -------- | --------------- | -------------------------------------------------- |
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
| ------------------------- | ---- | ------- | -------------------------------- |
| `services.connect_port`   | int  | `9000`  | Colony Connect/gRPC service port |
| `services.dashboard_port` | int  | `3000`  | Dashboard web UI port            |

#### Discovery

| Field                         | Type     | Default                      | Description                           |
| ----------------------------- | -------- | ---------------------------- | ------------------------------------- |
| `discovery.enabled`           | bool     | `true`                       | Enable discovery service registration |
| `discovery.mesh_id`           | string   | Same as `colony_id`          | Mesh identifier for discovery         |
| `discovery.auto_register`     | bool     | `true`                       | Auto-register with discovery service  |
| `discovery.register_interval` | duration | `60s`                        | Registration refresh interval         |
| `discovery.stun_servers`      | []string | `[stun.cloudflare.com:3478]` | STUN servers for NAT discovery        |

#### MCP Server (Model Context Protocol)

| Field                                   | Type     | Default    | Description                      |
| --------------------------------------- | -------- | ---------- | -------------------------------- |
| `mcp.disabled`                          | bool     | `false`    | Disable MCP server               |
| `mcp.enabled_tools`                     | []string | `[]` (all) | Restrict available tools         |
| `mcp.security.require_rbac_for_actions` | bool     | `false`    | Require RBAC for exec/shell/eBPF |
| `mcp.security.audit_enabled`            | bool     | `false`    | Enable MCP tool call auditing    |

#### Remote Colony Connection (Client-Side)

For connecting to remote colonies from the CLI without WireGuard mesh access.
Similar to kubectl's cluster configuration in kubeconfig.

| Field                               | Type   | Default | Description                                                      |
| ----------------------------------- | ------ | ------- | ---------------------------------------------------------------- |
| `remote.endpoint`                   | string | -       | Remote colony's public HTTPS endpoint URL                        |
| `remote.certificate_authority`      | string | -       | Path to CA certificate file for TLS verification                 |
| `remote.certificate_authority_data` | string | -       | Base64-encoded CA certificate (takes precedence)                 |
| `remote.insecure_skip_tls_verify`   | bool   | `false` | Skip TLS verification (testing only, never in prod)              |
| `remote.ca_fingerprint.algorithm`   | string | -       | CA fingerprint hash algorithm (e.g., `sha256`)                   |
| `remote.ca_fingerprint.value`       | string | -       | Hex-encoded CA fingerprint for continuous verification (RFD 085) |

**Example - Remote Colony with CA Certificate:**

```yaml
# ~/.coral/colonies/prod-remote/config.yaml
version: "1"
colony_id: "prod-remote"
application_name: "MyApp"
environment: "production"

remote:
    endpoint: https://colony.example.com:8443
    certificate_authority: ~/.coral/ca/prod-ca.crt
```

**Example - Remote Colony with Inline CA (Base64):**

```yaml
remote:
    endpoint: https://colony.example.com:8443
    certificate_authority_data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0t...
```

**Example - Insecure Mode (Testing Only):**

```yaml
remote:
    endpoint: https://localhost:8443
    insecure_skip_tls_verify: true  # Never use in production!
```

#### Beyla Observability (RFD 032, RFD 036)

Beyla metrics and traces are collected from agents and stored in the colony's
DuckDB. Retention policies control how long data is kept.

| Field                         | Type | Default | Description                                              |
| ----------------------------- | ---- | ------- | -------------------------------------------------------- |
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

| Field                           | Type | Default | Description                                      |
| ------------------------------- | ---- | ------- | ------------------------------------------------ |
| `system_metrics.poll_interval`  | int  | `60`    | Interval (seconds) to poll agents for metrics    |
| `system_metrics.retention_days` | int  | `30`    | Retention period for aggregated summaries (days) |

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
| ------------------- | ------ | -------- | --------------------------------- |
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

# Core Agent settings (RFD 025, RFD 048)
agent:
    runtime: "auto"          # auto, native, docker, kubernetes
    colony:
        id: "my-colony"      # Colony ID to connect to
        auto_discover: true   # Auto-discover colony via Discovery Service

    # Bootstrap settings (RFD 048, RFD 088)
    bootstrap:
        enabled: true
        ca_fingerprint: "sha256:7f...a0"  # Required for first bootstrap
        psk: "coral-psk:f1e2d3c4b5a6..."  # Required for enrollment authorization
        certs_dir: "~/.coral/certs"
        retry_attempts: 10
        retry_delay: 1s
        total_timeout: 30m

# Telemetry (OpenTelemetry) configuration
telemetry:
    disabled: false
    grpc_endpoint: "0.0.0.0:4317"
    http_endpoint: "0.0.0.0:4318"
    filters:
        always_capture_errors: true
        high_latency_threshold_ms: 500.0
        sample_rate: 0.10

# Services to monitor at startup
services:
    -   name: "api-gateway"
        port: 8080
        health_endpoint: "/health"
        type: "http"
```

### Agent Configuration Fields

| Field                                         | Type              | Default                      | Description                                                   |
| --------------------------------------------- | ----------------- | ---------------------------- | ------------------------------------------------------------- |
| `version`                                     | string            | `"1"`                        | Configuration schema version                                  |
| `agent.runtime`                               | string            | `auto`                       | Runtime environment: `auto`, `native`, `docker`, `kubernetes` |
| `agent.colony.id`                             | string            | -                            | Colony ID to connect to                                       |
| `agent.colony.auto_discover`                  | bool              | `true`                       | Enable automatic colony discovery                             |
| `agent.nat.stun_servers`                      | []string          | `[stun.cloudflare.com:3478]` | STUN servers for NAT traversal                                |
| `agent.nat.enable_relay`                      | bool              | `false`                      | Enable relay fallback (future)                                |
| `agent.bootstrap.enabled`                     | bool              | `true`                       | Enable automatic certificate bootstrap                        |
| `agent.bootstrap.ca_fingerprint`              | string            | -                            | Root CA fingerprint (sha256:hex) for trust                    |
| `agent.bootstrap.psk`                         | string            | -                            | Bootstrap PSK for enrollment authorization (RFD 088)          |
| `agent.bootstrap.certs_dir`                   | string            | `~/.coral/certs`             | Directory for storing certificates                            |
| `agent.bootstrap.retry_attempts`              | int               | `10`                         | Max bootstrap retry attempts                                  |
| `agent.bootstrap.retry_delay`                 | duration          | `1s`                         | Initial retry delay (exponential)                             |
| `agent.bootstrap.total_timeout`               | duration          | `30m`                        | Total time allowed for bootstrap                              |
| `telemetry.disabled`                          | bool              | `false`                      | Disable OpenTelemetry collection                              |
| `telemetry.grpc_endpoint`                     | string            | `0.0.0.0:4317`               | OTLP gRPC export endpoint                                     |
| `telemetry.http_endpoint`                     | string            | `0.0.0.0:4318`               | OTLP HTTP export endpoint                                     |
| `telemetry.filters.always_capture_errors`     | bool              | `true`                       | Always capture error traces                                   |
| `telemetry.filters.high_latency_threshold_ms` | float             | `500.0`                      | Latency threshold for capture                                 |
| `telemetry.filters.sample_rate`               | float             | `0.10`                       | Sample rate (0.0-1.0)                                         |
| `beyla.disabled`                              | bool              | `false`                      | Disable Beyla eBPF instrumentation                            |
| `beyla.discovery.services`                    | []Service         | `[]`                         | List of services to instrument                                |
| `beyla.protocols.http.enabled`                | bool              | `true`                       | Enable HTTP instrumentation                                   |
| `beyla.protocols.http.route_patterns`         | []string          | `[]`                         | URL patterns for cardinality reduction                        |
| `beyla.protocols.grpc.enabled`                | bool              | `true`                       | Enable gRPC instrumentation                                   |
| `beyla.protocols.sql.enabled`                 | bool              | `true`                       | Enable SQL instrumentation                                    |
| `beyla.protocols.sql.obfuscate_queries`       | bool              | `true`                       | Obfuscate SQL query literals                                  |
| `beyla.attributes`                            | map[string]string | `{}`                         | Custom attributes for metrics/traces                          |
| `beyla.sampling.rate`                         | float             | `1.0`                        | Trace sampling rate (0.0-1.0)                                 |
| `beyla.limits.max_traced_connections`         | int               | `1000`                       | Max concurrent tracked connections                            |
| `beyla.otlp_endpoint`                         | string            | `localhost:4318`             | OTLP export endpoint                                          |
| `debug.enabled`                               | bool              | `true`                       | Enable debug session capability                               |
| `debug.discovery.enable_sdk`                  | bool              | `true`                       | Enable SDK-based function discovery                           |
| `debug.discovery.enable_binary_scanning`      | bool              | `true`                       | Enable binary DWARF scanning                                  |
| `debug.sdk_api.timeout`                       | duration          | `5s`                         | Timeout for SDK communication                                 |
| `debug.limits.max_concurrent_sessions`        | int               | `5`                          | Max concurrent debug sessions                                 |
| `debug.limits.max_session_duration`           | duration          | `10m`                        | Max duration for a debug session                              |
| `debug.limits.max_events_per_second`          | int               | `10000`                      | Rate limit for debug events                                   |
| `system_metrics.disabled`                     | bool              | `false`                      | Disable system metrics collection                             |
| `system_metrics.interval`                     | duration          | `15s`                        | Collection interval                                           |
| `system_metrics.retention`                    | duration          | `1h`                         | Local retention period                                        |
| `system_metrics.cpu_enabled`                  | bool              | `true`                       | Collect CPU metrics                                           |
| `system_metrics.memory_enabled`               | bool              | `true`                       | Collect memory metrics                                        |
| `system_metrics.disk_enabled`                 | bool              | `true`                       | Collect disk I/O metrics                                      |
| `system_metrics.network_enabled`              | bool              | `true`                       | Collect network I/O metrics                                   |
| `continuous_profiling.disabled`               | bool              | `false`                      | Disable continuous profiling (enabled by default)             |
| `continuous_profiling.cpu.disabled`           | bool              | `false`                      | Disable CPU profiling (enabled by default)                    |
| `continuous_profiling.cpu.frequency_hz`       | int               | `19`                         | Sampling frequency (Hz)                                       |
| `continuous_profiling.cpu.interval`           | duration          | `15s`                        | Collection interval                                           |
| `continuous_profiling.cpu.retention`          | duration          | `1h`                         | Local sample retention                                        |
| `continuous_profiling.cpu.metadata_retention` | duration          | `7d`                         | Binary metadata retention                                     |

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

| Field             | Type     | Default | Description                              |
| ----------------- | -------- | ------- | ---------------------------------------- |
| `disabled`        | bool     | `false` | Master switch for system metrics         |
| `interval`        | duration | `15s`   | How often to sample system metrics       |
| `retention`       | duration | `1h`    | How long to keep raw samples locally     |
| `cpu_enabled`     | bool     | `true`  | Collect CPU utilization and time         |
| `memory_enabled`  | bool     | `true`  | Collect memory usage, limit, utilization |
| `disk_enabled`    | bool     | `true`  | Collect disk I/O and usage               |
| `network_enabled` | bool     | `true`  | Collect network I/O and errors           |

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

### Continuous CPU Profiling Configuration

The `continuous_profiling` section configures automatic background CPU profiling
for performance analysis and regression detection. Unlike on-demand profiling,
continuous profiling runs automatically in the background at low overhead.

**IMPORTANT: Continuous profiling is ENABLED BY DEFAULT.** No configuration is
required - it runs automatically when the agent starts.

```yaml
continuous_profiling:
    disabled: false         # Master disable switch (default: false = enabled)
    cpu:
        disabled: false       # Disable CPU profiling (default: false = enabled)
        frequency_hz: 19      # Sampling frequency (default: 19Hz, prime number)
        interval: 15s         # Collection interval (default: 15s)
        retention: 1h         # Local retention (default: 1h)
        metadata_retention: 7d  # Binary metadata retention (default: 7d)
```

**Configuration Fields:**

| Field                    | Type     | Default | Description                                    |
| ------------------------ | -------- | ------- | ---------------------------------------------- |
| `disabled`               | bool     | `false` | Master switch - set `true` to disable entirely |
| `cpu.disabled`           | bool     | `false` | Disable CPU profiling - set `true` to disable  |
| `cpu.frequency_hz`       | int      | `19`    | Sampling frequency (19Hz = low overhead)       |
| `cpu.interval`           | duration | `15s`   | How often to collect and aggregate samples     |
| `cpu.retention`          | duration | `1h`    | How long to keep samples locally on agent      |
| `cpu.metadata_retention` | duration | `7d`    | How long to keep binary metadata (build IDs)   |

**How It Works:**

- **Automatic Collection:** Profiles collected every 15 seconds at 19Hz sampling
- **Low Overhead:** <1% CPU impact, designed for production use
- **Frame Dictionary:** 85% storage compression using integer encoding
- **Build ID Tracking:** Tracks binary versions for correct symbolization
- **Historical Queries:** Query past profiles using
  `coral debug cpu-profile --since 1h`
- **Colony Aggregation:** Colony polls agents and stores 30-day summaries

**Collected Data:**

- Stack traces from CPU samples (user + kernel space)
- Binary build IDs for version tracking
- Sample counts per unique stack trace
- Compatible with flamegraph.pl for visualization

**Storage and Retention:**

- **Agent-side:** Raw samples stored locally for 1 hour
- **Colony-side:** Aggregated 1-minute summaries stored for 30 days
- **Cleanup:** Automatic cleanup runs every 10 minutes on agent
- **Compression:** Frame dictionary encoding reduces storage by 85%

**Performance Impact:**

- **Overhead:** <1% CPU with 19Hz sampling (prime number avoids timer conflicts)
- **Memory:** ~500KB per profiled process (BPF maps)
- **Storage:** ~480KB/hour per service (agent), ~5.8MB/day (colony)
- **Network:** ~1.9MB/hour per service (agent → colony)

**Querying Historical Profiles:**

```bash
# Query last hour of CPU profiles
coral debug cpu-profile --service api --since 1h > profile.folded

# Query specific time range
coral debug cpu-profile --service api \
    --since "2025-12-15 14:00:00" \
    --until "2025-12-15 15:00:00"

# Generate flame graph
coral debug cpu-profile --service api --since 5m | flamegraph.pl > cpu.svg
```

**Disabling Continuous Profiling:**

To disable entirely:

```yaml
continuous_profiling:
    disabled: true
```

To disable only CPU profiling:

```yaml
continuous_profiling:
    cpu:
        disabled: true
```

**Customizing Collection:**

```yaml
continuous_profiling:
    cpu:
        frequency_hz: 49       # Higher frequency = more samples, more overhead
        interval: 30s          # Less frequent collection
        retention: 2h          # Keep samples longer locally
```

**Multi-Version Support:**

Continuous profiling tracks binary build IDs, so historical queries work across
deployments:

```bash
# Query spanning a deployment
coral debug cpu-profile --service api --since 2h

# Output includes build ID annotations when versions change:
# [build_id:abc123] main;processRequest;parseJSON 1200
# [build_id:def456] main;processRequest;parseJSONv2 1500  ← New version
```

**Integration with On-Demand Profiling:**

Continuous profiling (19Hz) runs in the background. You can still trigger
high-frequency on-demand profiling (99Hz) when needed:

```bash
# On-demand high-frequency profiling (overrides continuous profiling temporarily)
coral debug cpu-profile --service api --duration 30s --frequency 99
```

Both modes use the same eBPF infrastructure and are fully compatible.

### Continuous Memory Profiling Configuration

The `continuous_profiling.memory` section configures automatic background memory
profiling for allocation analysis and leak detection. Like CPU profiling, it runs
automatically in the background at low overhead.

**IMPORTANT: Continuous memory profiling is ENABLED BY DEFAULT.** No configuration
is required - it runs automatically when the agent starts.

```yaml
continuous_profiling:
    disabled: false         # Master disable switch (default: false = enabled)
    memory:
        disabled: false       # Disable memory profiling (default: false = enabled)
        interval: 60s         # Collection interval (default: 60s)
        retention: 1h         # Local retention (default: 1h)
        sample_rate_bytes: 4194304  # Sampling rate in bytes (default: 4MB)
```

**Configuration Fields:**

| Field               | Type     | Default   | Description                                     |
|---------------------|----------|-----------|-------------------------------------------------|
| `disabled`          | bool     | `false`   | Master switch - set `true` to disable entirely  |
| `memory.disabled`   | bool     | `false`   | Disable memory profiling - set `true` to disable|
| `memory.interval`   | duration | `60s`     | How often to collect heap snapshots             |
| `memory.retention`  | duration | `1h`      | How long to keep samples locally on agent       |
| `memory.sample_rate_bytes` | int | `4194304` | Allocation sampling rate (4MB = low overhead)  |

**How It Works:**

- **Automatic Collection:** Heap snapshots collected every 60 seconds
- **Low Overhead:** <1% CPU impact with 4MB sampling rate
- **Frame Dictionary:** 85% storage compression using integer encoding
- **Top Allocators:** Pre-computed top functions and types for fast queries
- **Historical Queries:** Query past profiles using
  `coral query memory-profile --since 1h`
- **Colony Aggregation:** Colony polls agents and stores 30-day summaries

**Collected Data:**

- Stack traces from heap allocations
- Allocation bytes and object counts per stack
- Top allocating functions (pre-computed, with shortened names)
- Top allocation types (slice, map, object, string, etc.)
- Compatible with flamegraph.pl for visualization

**Storage and Retention:**

- **Agent-side:** Raw samples stored locally for 1 hour (~120KB/hour)
- **Colony-side:** Aggregated 1-minute summaries stored for 30 days (~174MB/service)
- **Cleanup:** Automatic cleanup runs every 10 minutes on agent
- **Compression:** Frame dictionary encoding reduces storage by 85%

**Performance Impact:**

- **Overhead:** <1% CPU with 4MB sampling rate
- **Memory:** ~2KB per snapshot
- **Storage:** ~120KB/hour per service (agent)
- **Network:** Minimal (profiles polled by colony)

**Querying Historical Profiles:**

```bash
# Query last hour of memory profiles (summary format - default)
coral query memory-profile --service api --since 1h

# Include allocation type breakdown
coral query memory-profile --service api --since 1h --show-types

# Generate flame graph from historical data
coral query memory-profile --service api --since 1h --format folded | flamegraph.pl > memory.svg
```

**Output Format (Summary - Default):**

```
Querying historical memory profiles for service 'api'
Time range: 2026-02-03T18:00:00Z to 2026-02-03T19:00:00Z
Total unique stacks: 42
Total alloc bytes: 2.4 GB

Top Memory Allocators:
  45.2%  1.1 GB   orders.ProcessOrder
  22.1%  530.4 MB json.Marshal
  12.5%  300.0 MB cache.Store

Top Allocation Types:
  55.2%  1.3 GB   slice
  22.8%  547.2 MB object
  12.1%  290.4 MB string
```

Function names are automatically shortened for readability:
- `github.com/myapp/orders.ProcessOrder` → `orders.ProcessOrder`
- `encoding/json.Marshal` → `json.Marshal`

**Disabling Continuous Memory Profiling:**

To disable entirely:

```yaml
continuous_profiling:
    disabled: true
```

To disable only memory profiling:

```yaml
continuous_profiling:
    memory:
        disabled: true
```

**Customizing Collection:**

```yaml
continuous_profiling:
    memory:
        interval: 30s           # More frequent collection
        retention: 2h           # Keep samples longer locally
        sample_rate_bytes: 524288  # 512KB = higher resolution, more overhead
```

## Environment Variables

Environment variables override configuration file values.

### Colony Environment Variables

| Variable                   | Overrides                        | Example                    | Description                                                            |
| -------------------------- | -------------------------------- | -------------------------- | ---------------------------------------------------------------------- |
| `CORAL_COLONY_ID`          | -                                | `my-app-prod`              | Colony to start                                                        |
| `CORAL_DISCOVERY_ENDPOINT` | `discovery.endpoint`             | `http://discovery:8080`    | Discovery service URL                                                  |
| `CORAL_STORAGE_PATH`       | `storage_path`                   | `/var/lib/coral`           | Storage directory path                                                 |
| `CORAL_PUBLIC_ENDPOINT`    | `wireguard.public_endpoints`     | `colony.example.com:41580` | **Production required:** Public WireGuard endpoint(s), comma-separated |
| `CORAL_MESH_SUBNET`        | `wireguard.mesh_network_ipv4`    | `100.64.0.0/10`            | Mesh network subnet                                                    |
| `CORAL_WG_KEEPALIVE`       | `wireguard.persistent_keepalive` | `25`                       | WireGuard keepalive interval (seconds)                                 |
| `CORAL_COLONY_ENDPOINT`    | -                                | `https://colony:8443`      | **Public API:** Public HTTPS endpoint for CLI/SDK access (RFD 031)     |
| `CORAL_API_TOKEN`          | -                                | `cpt_abc123...`            | API token for authenticating to the public endpoint (RFD 031)          |
| `CORAL_DEFAULT_COLONY`     | `default_colony` (Global)        | `my-default-colony`        | Default colony for global config                                       |
| `CORAL_ASK_MODEL`          | `ask.default_model`              | `google:gemini-3-fast`     | Default model for Coral Ask                                            |
| `CORAL_ASK_MAX_TURNS`      | `ask.conversation.max_turns`     | `20`                       | Max conversation turns for Coral Ask                                   |

### Polling Interval Environment Variables

Overrides for various polling intervals (Duration strings, e.g., "30s", "5m"):

| Variable                               | Config Field                         | Default |
| -------------------------------------- | ------------------------------------ | ------- |
| `CORAL_SERVICES_POLL_INTERVAL`         | `services.poll_interval`             | `5m`    |
| `CORAL_DISCOVERY_REGISTER_INTERVAL`    | `discovery.register_interval`        | `60s`   |
| `CORAL_BEYLA_POLL_INTERVAL`            | `beyla.poll_interval`                | `60s`   |
| `CORAL_FUNCTIONS_POLL_INTERVAL`        | `function_registry.poll_interval`    | `5m`    |
| `CORAL_SYSTEM_METRICS_POLLER_INTERVAL` | `system_metrics.poll_interval`       | `60s`   |
| `CORAL_PROFILING_POLLER_INTERVAL`      | `continuous_profiling.poll_interval` | `60s`   |
| `CORAL_TELEMETRY_POLL_INTERVAL`        | `telemetry.poll_interval`            | `60s`   |

**Multiple Endpoints Example:**

```bash
CORAL_PUBLIC_ENDPOINT=192.168.5.2:9000,10.0.0.5:9000,colony.example.com:9000
```

### Agent Environment Variables

| Variable                        | Description                                         |
| ------------------------------- | --------------------------------------------------- |
| `CORAL_AGENT_ID`                | Unique agent identifier (overrides auto-generation) |
| `CORAL_COLONY_ID`               | Colony ID to connect to                             |
| `CORAL_DISCOVERY_ENDPOINT`      | Discovery service URL                               |
| `CORAL_CA_FINGERPRINT`          | Root CA fingerprint for bootstrap (sha256:hex)      |
| `CORAL_BOOTSTRAP_PSK`           | Bootstrap PSK for enrollment authorization          |
| `CORAL_BOOTSTRAP_ENABLED`       | Enable/disable automatic bootstrap (`true`/`false`) |
| `CORAL_CERTS_DIR`               | Directory for storing certificates                  |
| `CORAL_SERVICES`                | Services to monitor (name:port[:health][:type],...) |
| `CORAL_AGENT_RUNTIME`           | Agent runtime (auto, native, docker, kubernetes)    |
| `CORAL_TELEMETRY_DISABLED`      | Disable telemetry (`true`/`false`)                  |
| `CORAL_OTLP_GRPC_ENDPOINT`      | OTLP gRPC endpoint address                          |
| `CORAL_OTLP_HTTP_ENDPOINT`      | OTLP HTTP endpoint address                          |
| `CORAL_SYSTEM_METRICS_DISABLED` | Disable system metrics collection (`true`/`false`)  |
| `CORAL_CPU_PROFILING_DISABLED`  | Disable CPU profiling (`true`/`false`)              |

### CLI Environment Variables

| Variable                | Description                                                            |
| ----------------------- | ---------------------------------------------------------------------- |
| `CORAL_COLONY_ENDPOINT` | Explicit colony endpoint URL (e.g., `https://colony.example.com:8443`) |
| `CORAL_API_TOKEN`       | API token for authenticating to the public endpoint (RFD 031)          |
| `CORAL_CA_FILE`         | Path to CA certificate file for TLS verification                       |
| `CORAL_CA_DATA`         | Base64-encoded CA certificate for TLS verification                     |
| `CORAL_INSECURE`        | Skip TLS verification (`true` or `1`) - testing only, never in prod    |
| `CORAL_COLONY_ID`       | Override default colony ID                                             |

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
| ----------- | ---------------------------------------- |
| `.0`        | Network address (reserved)               |
| `.1`        | Colony address                           |
| `.2` - `.N` | Agent addresses (allocated sequentially) |

### Network Conflict Avoidance

#### Common Conflicts

| Network          | Used By                          | Conflict Risk |
| ---------------- | -------------------------------- | ------------- |
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
        default_model: "google:gemini-3-fast"
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

### Example 8: Remote Colony Connection

For CLI access to a colony running on a remote server without WireGuard mesh.

**Remote Colony Config** (`~/.coral/colonies/prod-remote/config.yaml`):

```yaml
version: "1"
colony_id: "prod-remote"
application_name: "MyApp"
environment: "production"

# Remote connection settings (no WireGuard needed)
remote:
    endpoint: https://colony.example.com:8443
    certificate_authority: ~/.coral/ca/prod-ca.crt
```

**Quick Test with Environment Variables:**

```bash
# Skip TLS verification for testing (never in production!)
export CORAL_COLONY_ENDPOINT=https://localhost:8443
export CORAL_API_TOKEN=cpt_abc123...
export CORAL_INSECURE=true
coral colony status

# With CA certificate file
export CORAL_COLONY_ENDPOINT=https://colony.example.com:8443
export CORAL_API_TOKEN=cpt_abc123...
export CORAL_CA_FILE=~/.coral/ca/prod-ca.crt
coral colony status

# With base64-encoded CA (useful for CI/CD)
export CORAL_COLONY_ENDPOINT=https://colony.example.com:8443
export CORAL_API_TOKEN=cpt_abc123...
export CORAL_CA_DATA=$(base64 < ~/.coral/ca/prod-ca.crt)
coral colony status
```

**Using the Config File:**

```bash
# Set as default colony
coral config use-context prod-remote

# Now all commands use the remote endpoint
export CORAL_API_TOKEN=cpt_abc123...
coral colony status
coral query summary
```

**Importing a Remote Colony:**

Colony administrators can provide connection details that users import:

```bash
# Using Discovery Mode (Recommended - RFD 085)
# Get colony-id and ca-fingerprint from colony owner (coral colony export)
coral colony add-remote prod-remote \
    --from-discovery \
    --colony-id my-app-prod-a3f2e1 \
    --ca-fingerprint sha256:e3b0c44298fc1c149afbf4c8996fb924...

# With custom Discovery endpoint
coral colony add-remote prod-remote \
    --from-discovery \
    --colony-id my-app-prod-a3f2e1 \
    --ca-fingerprint sha256:e3b0c44298fc1c149afbf4c8996fb924... \
    --discovery-endpoint https://discovery.internal:8080

# Manual mode: with CA certificate file
coral colony add-remote prod-remote \
    --endpoint https://colony.example.com:8443 \
    --ca-file ./colony-ca.crt

# Manual mode: with insecure (testing only)
coral colony add-remote dev-remote \
    --endpoint https://dev-colony:8443 \
    --insecure
```

The `--from-discovery` mode provides TOFU (Trust On First Use) security: the CA
fingerprint is verified against the certificate received from Discovery Service,
ensuring you're connecting to the authentic colony.

### Example 9: AI-Powered Diagnostics with Coral Ask

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
        default_model: "google:gemini-3-fast"
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
    default_model: "google:gemini-3-fast"

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
coral ask "complex root cause analysis" --model google:gemini-3-fast

# 6. JSON output for scripting
coral ask "list unhealthy services" --json
```

**Key Features:**

- **MCP Integration:** LLM accesses all Colony MCP tools (service health,
  traces, metrics, logs)
- **Google Gemini:** Supported provider with full tool calling
- **OpenAI:** Supported provider with full tool calling, compatible with any
  OpenAI-compatible API
- **Conversation Context:** Multi-turn conversations with automatic context
  pruning
- **Per-Colony Models:** Use faster models for dev, more capable for production

**Future Support:** Anthropic and Ollama providers are planned but not yet
implemented. See `docs/PROVIDERS.md` for implementation status.

### Validation Rules

- **Mesh subnet:** Minimum `/24`, valid CIDR, IPv4 only
- **Ports:** Valid port numbers (1-65535)
- **Timeouts and Intervals:** Positive duration values
- **Colony ID:** Non-empty, valid identifier
- **Sample Rates:** Between 0.0 and 1.0
- **API keys:** Required when `ai.api_key_source` is `env`
- **Discovery:** Mesh IDs must match colony IDs

### Example Validation Errors

```
validation failed with 3 errors:
  1. agent.colony.id: colony ID is required when auto_discover is false
  2. telemetry.filters.sample_rate: sample rate must be between 0.0 and 1.0
  3. debug.limits.max_concurrent_sessions: max concurrent sessions must be positive
```

## Troubleshooting

### Common Issues

**Issue:** Configuration not loading from environment
**Solution:** Ensure you're using the correct environment variable names (prefixed with `CORAL_`). Note that precedence is Env > Config File > Defaults.

**Issue:** Duration parsing errors
**Solution:** Go's `time.ParseDuration` doesn't support days (`d`). Use hours instead (e.g., `168h` for 7 days).

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
