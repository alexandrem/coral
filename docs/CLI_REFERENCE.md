# coral(1) — Command-Line Reference

## Name

`coral` — inspect and debug applications through a Coral Colony and its agents.

## Synopsis

```text
coral [--verbose] COMMAND [SUBCOMMAND] [ARGUMENTS] [OPTIONS]
coral COMMAND --help
```

This is the single command manual for the full CLI. For installation and
end-to-end workflows, start with the [user guide](user/README.md).
Command and option entries below follow the registered Cobra command tree in
this checkout. Descriptions are qualified where a handler is incomplete.
Use the help supplied with your installed version when it differs from this
checkout.

Angle brackets indicate required values; square brackets indicate optional
arguments. Duration values such as `30s` differ from integer seconds accepted
by some profiling options. Options apply to their listed command, except for
inherited global options. Required flags are marked in the option table.
Defaults shown as `empty` are unset; environment-backed options can obtain a
value at startup. Slice options accept comma-separated values; array options
such as `exec --env` can be repeated.

## Global options

| Option | Meaning |
| --- | --- |
| `--help`, `-h` | Show help for the selected command |
| `--verbose`, `-v` | Verbose output; individual commands may define their own verbose behavior |

`--colony` and `--format` are **command-specific**, not global flags. Their
availability, shorthand, accepted values, and defaults differ. In particular,
`duckdb --colony` selects the Colony database with a boolean; it does not take
an ID. Commands without `--format` cannot be forced to emit JSON by adding it.

## Command index

- [`coral agent`](#coral-agent) — Manage Coral agents
- [`coral ask`](#coral-ask) — Ask questions about your application using AI
- [`coral colony`](#coral-colony) — Manage the Coral colony (central brain)
- [`coral completion`](#coral-completion) — Generate the autocompletion script for the specified shell
- [`coral config`](#coral-config) — Manage Coral configuration
- [`coral debug`](#coral-debug) — Debug running services
- [`coral duckdb`](#coral-duckdb) — Query agent DuckDB databases remotely
- [`coral exec`](#coral-exec) — Execute command in a service's container
- [`coral help`](#coral-help) — Help about any command
- [`coral init`](#coral-init) — Initialize a new Coral colony
- [`coral mcp`](#coral-mcp) — Integrate Coral with MCP clients
- [`coral mesh`](#coral-mesh) — Manage and troubleshoot the Coral control mesh
- [`coral profile`](#coral-profile) — Collect performance profiles on-demand
- [`coral proxy`](#coral-proxy) — Manage local proxy for colony access
- [`coral query`](#coral-query) — Query observability data (metrics, traces, logs, profiles)
- [`coral run`](#coral-run) — Execute TypeScript script with Deno
- [`coral script`](#coral-script) — Manage investigation scripts
- [`coral services`](#coral-services) — List and manage services
- [`coral shell`](#coral-shell) — Open interactive shell or execute command in agent environment
- [`coral status`](#coral-status) — Show global Coral environment status
- [`coral terminal`](#coral-terminal) — Launch the mission-control TUI
- [`coral version`](#coral-version) — Show version information

## Commands

### coral agent

Manage Coral agents.

```text
coral agent [flags]
```

Server-only `coral-agent` exposes the agent children directly, for example `coral-agent start`. It does not expose the operator query, shell, or scripting groups.

#### coral agent bootstrap

Bootstrap agent certificate for mTLS authentication.

```text
coral agent bootstrap [flags]
```

Requires a Colony ID, trusted CA fingerprint, and valid Bootstrap PSK for initial enrollment. These can also come from the corresponding environment variables. Standalone bootstrap does not perform startup network discovery; supply a reverse-dial endpoint explicitly when needed. Certificate issuance alone does not establish a matching mesh enrollment checkpoint.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--agent` | string | empty | Agent ID (default: auto-generated) |
| `--bootstrap-listen-port` | int | `0` | Local port for the rendezvous listener (default: 8444) |
| `--bootstrap-public-endpoint` | string | empty | This agent's dialable ip:port, used for PSK-encrypted rendezvous bootstrap when the colony has no inbound connectivity |
| `--certs-dir` | string | empty | Directory for storing certificates |
| `--colony` / `-c` | string | empty | Colony ID (overrides auto-detection) |
| `--discovery` | string | `https://discovery.coralmesh.dev` | Discovery service URL |
| `--fingerprint` | string | empty | Expected Root CA fingerprint (sha256:hex) |
| `--force` | bool | `false` | Force re-bootstrap even if certificate exists |
| `--psk` | string | empty | Bootstrap pre-shared key (coral-psk:hex) |
| `--verify-bootstrap-reachability` | bool | `false` | Ask Discovery to TCP-probe --bootstrap-public-endpoint before accepting the rendezvous publish (opt-in, quota-limited diagnostic, not a guarantee) |

#### coral agent cert

Manage agent certificates.

```text
coral agent cert [flags]
```

##### coral agent cert renew

Renew agent certificate.

```text
coral agent cert renew [flags]
```

Direct `--colony-endpoint` renewal uses the existing mTLS identity. Without it, the command can use Discovery/bootstrap configuration. Use the same certificate directory and account as the service.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--certs-dir` | string | empty | Directory containing certificates |
| `--colony` / `-c` | string | empty | Colony ID (overrides auto-detection) |
| `--colony-endpoint` | string | empty | Colony HTTPS endpoint for direct mTLS renewal |
| `--discovery` | string | empty | Discovery service URL (fallback) |
| `--fingerprint` | string | empty | Expected Root CA fingerprint |
| `--force` | bool | `false` | Force renewal even if not near expiry |
| `--psk` | string | empty | Bootstrap pre-shared key (fallback to bootstrap) |

##### coral agent cert status

Display certificate status.

```text
coral agent cert status [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--certs-dir` | string | empty | Directory containing certificates |

#### coral agent start

Start a Coral agent as a daemon.

```text
coral agent start [flags]
```

Runs in the foreground. Automatic process observation is enabled by default. `--no-monitor-all` opts out; `--monitor-all` is a deprecated no-op. `--daemon` is registered but is not a complete background service implementation. Use a process supervisor or Ctrl+C/SIGTERM. There is no `agent stop` command.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--colony` / `-c` | string | empty | Colony ID (overrides auto-detection) |
| `--config` | string | empty | Path to agent configuration file (default: /etc/coral/agent.yaml) |
| `--daemon` | bool | `false` | Run in background (requires PID file support) |
| `--monitor-all` | bool | `false` | Deprecated: monitoring all processes is now the default; this flag is a no-op |
| `--no-monitor-all` | bool | `false` | Disable automatic eBPF instrumentation of all processes (for resource-constrained hosts) |

#### coral agent status

Show agent status and runtime context.

```text
coral agent status [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--agent` | string | empty | Agent ID (resolves via colony registry) |
| `--agent-url` | string | empty | Agent URL (default: http://localhost:9001) |
| `--cert` | bool | `false` | Show certificate status |
| `--certs-dir` | string | empty | Directory containing certificates |
| `--colony` / `-c` | string | empty | Colony ID (overrides auto-detection) |
| `--format` / `-o` | string | `table` | Output format (table, json, yaml) |

### coral ask

Ask questions about your application using AI.

```text
coral ask [question] [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--colony` / `-c` | string | empty | Colony ID (overrides auto-detection) |
| `--continue` | bool | `false` | Continue previous conversation |
| `--debug` | bool | `false` | Enable debug logging to stderr |
| `--dry-run` | bool | `false` | Show LLM payload and prompt before sending (saves tokens) |
| `--format` / `-o` | string | `table` | Output format (table, json) |
| `--interactive` | bool | `false` | Force interactive mode (auto-detected when no question provided) |
| `--model` | string | empty | Override model for this query (e.g., openai:gpt-4o-mini) |
| `--resume` | string | empty | Resume a previous conversation by ID or datetime prefix |
| `--stream` | bool | `true` | Stream output progressively (default: true) |

#### coral ask config

Configure LLM provider for coral ask.

```text
coral ask config [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--api-key-env` | string | empty | Environment variable containing the API key |
| `--colony` | string | empty | Configure specific colony instead of global defaults |
| `--dry-run` | bool | `false` | Show what would be changed without saving |
| `--model` | string | empty | Model ID (e.g., gemini-2.0-flash) |
| `--provider` | string | empty | Provider name (e.g., google, openai, coral) |
| `--yes` / `-y` | bool | `false` | Skip confirmation prompts |

##### coral ask config show

Show the current LLM configuration.

```text
coral ask config show [flags]
```

##### coral ask config validate

Validate the current LLM configuration.

```text
coral ask config validate [flags]
```

#### coral ask list-providers

List available LLM providers and their status.

```text
coral ask list-providers [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--format` / `-o` | string | `table` | Output format (table, json, yaml) |
| `--models` | bool | `false` | Show supported models for each provider |

### coral colony

Manage the Coral colony (central brain).

```text
coral colony [flags]
```

Server-only `coral-colony` exposes server management children directly. Client context commands (`list`, `use`, `current`, `add-remote`) belong to the full CLI. Initialize with `coral init` or the initialization command in the server binary before starting.

#### coral colony add-remote

Add a remote colony connection (similar to kubectl config set-cluster).

```text
coral colony add-remote <colony-name> [flags]
```

Requires either `--from-discovery` with `--colony-id` and `--ca-fingerprint`, or `--endpoint` with CA material (or testing-only `--insecure`). This saves a local connection; it does not issue an API token. Supply `CORAL_API_TOKEN` separately.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--ca-data` | string | empty | Base64-encoded CA certificate for TLS verification (manual mode) |
| `--ca-file` | string | empty | Path to CA certificate file for TLS verification (manual mode) |
| `--ca-fingerprint` | string | empty | CA fingerprint for verification (required with --from-discovery, format: sha256:hex) |
| `--colony-id` | string | empty | Colony ID (required with --from-discovery) |
| `--discovery-endpoint` | string | empty | Override Discovery Service URL |
| `--endpoint` | string | empty | Colony's public HTTPS endpoint URL (manual mode) |
| `--from-discovery` | bool | `false` | Fetch endpoint and CA from Discovery Service |
| `--insecure` | bool | `false` | Skip TLS verification (testing only, manual mode only) |
| `--set-default` | bool | `false` | Set this colony as the default |

#### coral colony agents

List registered agents.

```text
coral colony agents [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--agents` | stringSlice | `[]` | Target exact agent IDs (comma-separated) |
| `--colony` / `-c` | string | empty | Colony ID (overrides auto-detection) |
| `--format` / `-o` | string | `table` | Output format (table, json, yaml) |
| `--runtime` | stringSlice | `[]` | Filter runtime: native,docker,k8s-sidecar,k8s-daemonset,unknown (comma-separated) |
| `--service` | stringSlice | `[]` | Filter exact service names (comma-separated) |
| `--sort` | string | `id` | Sort by id,status,last-seen,runtime,version; prefix with - to reverse |
| `--status` | stringSlice | `[]` | Filter heartbeat status: healthy,degraded,unhealthy (comma-separated) |
| `--version` | stringSlice | `[]` | Filter exact agent versions (comma-separated) |
| `--wide` | bool | `false` | Show mesh addresses and full service lists |

#### coral colony ca

Manage colony certificate authority.

```text
coral colony ca [flags]
```

##### coral colony ca rotate-intermediate

Rotate an intermediate CA certificate.

```text
coral colony ca rotate-intermediate [flags]
```

Requires `--type server` or `--type agent` and `--confirm`. This changes local CA state; preserve a consistent backup before rotation.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--colony` | string | empty | Colony ID (defaults to current colony) |
| `--confirm` | bool | `false` | Confirm rotation operation |
| `--type` | string | empty | **Required.** Type of intermediate to rotate (server, agent) |

##### coral colony ca status

Show CA status and fingerprint.

```text
coral colony ca status [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--colony` | string | empty | Colony ID (defaults to current colony) |

#### coral colony current

Show the current default colony.

```text
coral colony current [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--format` / `-o` | string | `table` | Output format (table, json, yaml) |

#### coral colony export

Export agent enrollment credentials.

```text
coral colony export [colony-id] [flags]
```

Exports enrollment credentials including the active Bootstrap PSK. Output is secret provisioning material, not a backup of Colony private keys or data. Requires access to local Colony configuration and state.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--format` | string | `env` | Output format (env, yaml, json, k8s) |

#### coral colony import

Import colony credentials.

```text
coral colony import [flags]
```

Legacy colony-secret import. `--stdin` is currently rejected as unimplemented. This is not the inverse of the current PSK enrollment export; provision agents with the exported enrollment settings and agent startup instead.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--colony` / `-c` | string | empty | Colony ID (overrides auto-detection) |
| `--secret` | string | empty | Colony secret |
| `--stdin` | bool | `false` | Read from stdin |

#### coral colony list

List all configured colonies.

```text
coral colony list [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--format` / `-o` | string | `table` | Output format (table, json, csv, yaml) |

#### coral colony psk

Manage bootstrap pre-shared key.

```text
coral colony psk [flags]
```

##### coral colony psk rotate

Rotate the bootstrap PSK.

```text
coral colony psk rotate [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--colony` / `-c` | string | empty | Colony ID (overrides auto-detection) |
| `--grace-period` | duration | `24h0m0s` | Grace period during which both old and new PSKs are accepted |

##### coral colony psk show

Display the current bootstrap PSK.

```text
coral colony psk show [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--colony` / `-c` | string | empty | Colony ID (overrides auto-detection) |

#### coral colony start

Start the colony.

```text
coral colony start [flags]
```

Runs the selected Colony in the foreground. `--port` changes the configured dashboard port, not the WireGuard UDP or Connect API port; the dashboard startup remains incomplete. `--daemon` does not implement full process supervision.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--colony` | string | empty | Colony ID (overrides auto-detection) |
| `--daemon` | bool | `false` | Run as background daemon |
| `--port` | int | `0` | Dashboard port (overrides config) |

#### coral colony status

Show colony status and WireGuard configuration.

```text
coral colony status [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--colony` / `-c` | string | empty | Colony ID (overrides auto-detection) |
| `--detail` | bool | `false` | Show detailed mesh configuration including peer endpoints |
| `--format` / `-o` | string | `table` | Output format (table, json, yaml) |

#### coral colony stop

Stop the colony daemon.

```text
coral colony stop [flags]
```

Placeholder: currently prints a stop message without terminating the running Colony. Stop the foreground process or use its supervisor.

#### coral colony token

Manage API tokens for public endpoint access.

```text
coral colony token [flags]
```

Local token-file administration for the public HTTPS API. Permissions are `status`, `query`, `analyze`, `debug`, and `admin`. Send SIGHUP to the running Colony or restart it after changing tokens to reload the in-memory store.

##### coral colony token create

Create a new API token.

```text
coral colony token create <token-id> [flags]
```

The raw token is displayed only on creation. `--recreate` replaces the named token, invalidating the old value after the running server reloads it.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--colony` | string | empty | Colony ID (defaults to current colony) |
| `--permissions` | string | `status,query` | Comma-separated permissions (status,query,analyze,debug,admin) |
| `--rate-limit` | string | empty | Rate limit (e.g., 100/hour, 50/minute) |
| `--recreate` | bool | `false` | Recreate token if it already exists (invalidates old token) |

##### coral colony token delete

Permanently delete an API token.

```text
coral colony token delete <token-id> [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--colony` | string | empty | Colony ID (defaults to current colony) |
| `--force` | bool | `false` | Skip confirmation prompt |

##### coral colony token list

List all API tokens.

```text
coral colony token list [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--colony` | string | empty | Colony ID (defaults to current colony) |

##### coral colony token revoke

Revoke an API token.

```text
coral colony token revoke <token-id> [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--colony` | string | empty | Colony ID (defaults to current colony) |
| `--force` | bool | `false` | Skip confirmation prompt |

##### coral colony token show

Show API token metadata.

```text
coral colony token show <token-id> [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--colony` | string | empty | Colony ID (defaults to current colony) |

#### coral colony use

Set the default colony.

```text
coral colony use <colony-id> [flags]
```

### coral completion

Generate the autocompletion script for the specified shell.

```text
coral completion [flags]
```

#### coral completion bash

Generate the autocompletion script for bash.

```text
coral completion bash
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--no-descriptions` | bool | `false` | disable completion descriptions |

#### coral completion fish

Generate the autocompletion script for fish.

```text
coral completion fish [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--no-descriptions` | bool | `false` | disable completion descriptions |

#### coral completion powershell

Generate the autocompletion script for powershell.

```text
coral completion powershell [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--no-descriptions` | bool | `false` | disable completion descriptions |

#### coral completion zsh

Generate the autocompletion script for zsh.

```text
coral completion zsh [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--no-descriptions` | bool | `false` | disable completion descriptions |

### coral config

Manage Coral configuration.

```text
coral config [flags]
```

#### coral config current-context

Show the current active colony.

```text
coral config current-context [flags]
```

#### coral config delete-context

Delete a colony configuration.

```text
coral config delete-context <colony-id> [flags]
```

Removes the local context configuration. Review its confirmation prompt and affected state before proceeding.

#### coral config get-contexts

List all colonies with current context marked.

```text
coral config get-contexts [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--format` / `-o` | string | `table` | Output format (table, json, yaml) |

#### coral config use-context

Set the default colony.

```text
coral config use-context <colony-id> [flags]
```

Changes the global default. An environment override or current-directory project configuration can still select another Colony.

#### coral config validate

Validate all colony configurations.

```text
coral config validate [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--format` / `-o` | string | `table` | Output format (table, json) |

#### coral config view

Show merged configuration.

```text
coral config view [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--colony` | string | empty | Colony ID (defaults to current) |
| `--raw` | bool | `false` | Output raw YAML without annotations |

### coral debug

Debug running services.

```text
coral debug [flags]
```

#### coral debug attach

Attach uprobe to function.

```text
coral debug attach <service> [flags]
```

Requires `--function`. Duration and filter thresholds use duration strings. `--filter-rate N` emits one in N events in the kernel (0 or 1 means all). Capture fields depend on runtime and binary metadata; a registered option is not a guarantee of complete argument or return values.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--agent-id` | string | empty | Agent ID (manual override) |
| `--capture-args` | bool | `false` | Capture function arguments |
| `--capture-return` | bool | `false` | Capture return values |
| `--duration` / `-d` | duration | `1m0s` | Duration of the debug session |
| `--filter-rate` | uint32 | `0` | Emit 1 in every N events at kernel level (0 or 1 = all) |
| `--format` | string | `text` | Output format (text, json, csv) |
| `--function` / `-f` | string | empty | **Required.** Function name to trace (required) |
| `--max-duration` | duration | `0s` | Only emit events faster than this threshold (e.g. 500ms) |
| `--min-duration` | duration | `0s` | Only emit events slower than this threshold (e.g. 50ms) |
| `--sample-rate` | uint32 | `0` | Sample rate (0 = all calls) |

#### coral debug correlations

Manage active correlation descriptors.

```text
coral debug correlations [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--format` | string | `table` | Output format: table or json |
| `--service` / `-s` | string | empty | Filter by service name |

##### coral debug correlations list

List active correlation descriptors.

```text
coral debug correlations list [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--format` | string | `table` | Output format: table or json |
| `--service` / `-s` | string | empty | Filter by service name |

##### coral debug correlations remove

Remove an active correlation descriptor.

```text
coral debug correlations remove <correlation-id> [flags]
```

#### coral debug filter

Update kernel-level filter for an active debug session.

```text
coral debug filter <session-id> [flags]
```

Adjusts active-session kernel filters. A zero duration clears that threshold; filter rate 0 or 1 captures all events.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--filter-rate` | uint32 | `0` | Emit 1 in every N events at kernel level (0 or 1 = all) |
| `--format` | string | `text` | Output format (text, json) |
| `--max-duration` | duration | `0s` | Only emit events faster than this threshold (e.g. 500ms) |
| `--min-duration` | duration | `0s` | Only emit events slower than this threshold (e.g. 50ms) |

#### coral debug info

Get function details.

```text
coral debug info [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--format` | string | `text` | Output format (text, json) |
| `--function` / `-f` | string | empty | **Required.** Function name (required) |
| `--service` / `-s` | string | empty | **Required.** Service name (required) |

#### coral debug profile

Auto-profile functions.

```text
coral debug profile [flags]
```

Requires `--service` and `--query`. This probes selected functions; statistical CPU sampling is the separate `profile cpu` command.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--async` | bool | `false` | Return immediately without waiting |
| `--duration` / `-d` | string | `60s` | Duration of profiling session |
| `--format` | string | `text` | Output format (text, json) |
| `--max-functions` / `-n` | int32 | `20` | Maximum functions to profile (max: 50) |
| `--query` / `-q` | string | empty | **Required.** Search query (required) |
| `--sample-rate` | float64 | `1` | Event sampling rate (0.1-1.0) |
| `--service` / `-s` | string | empty | **Required.** Service name (required) |
| `--strategy` | string | `critical-path` | Selection strategy (critical-path, all, entry-points, leaf-functions) |

#### coral debug search

Search for functions.

```text
coral debug search <query> [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--format` | string | `text` | Output format (text, json) |
| `--max-results` / `-n` | int32 | `20` | Maximum number of results (max: 50) |
| `--service` / `-s` | string | empty | Filter by service name |

#### coral debug session

Manage debug sessions.

```text
coral debug session [flags]
```

##### coral debug session events

Query events from a debug session.

```text
coral debug session events <session-id> [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--follow` / `-f` | bool | `false` | Follow new events |
| `--format` | string | `json` | Output format (text, json) |
| `--max` | int32 | `100` | Max events to retrieve |
| `--since` | duration | `0s` | Show events since duration (e.g. 5m) |

##### coral debug session get

Get session metadata.

```text
coral debug session get <session-id> [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--format` | string | `text` | Output format (text, json, csv) |

##### coral debug session list

List active debug sessions.

```text
coral debug session list [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--format` | string | `text` | Output format (text, json, csv) |
| `--service` / `-s` | string | empty | Filter by service name |
| `--status` | string | empty | Filter by status |

##### coral debug session query

Query debug session results.

```text
coral debug session query <service> [flags]
```

Select retained results with a function name or session ID. Inspect session metadata and time range when results are empty.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--format` | string | `text` | Output format (text, json, csv) |
| `--function` / `-f` | string | empty | Function name to query |
| `--session-id` | string | empty | Session ID to query |
| `--since` | duration | `1h0m0s` | Time range to query |

##### coral debug session stop

Stop a debug session.

```text
coral debug session stop <session-id> [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--format` | string | `text` | Output format (text, json) |

#### coral debug trace

Trace request path.

```text
coral debug trace <service> [flags]
```

Requires `--path`. Guesses a function name from the path and attaches one bounded probe session, for example `/checkout` to `main.ProcessCheckout`. It does not discover arbitrary router handlers or establish a complete distributed request trace. Use explicit search and attachment when the guessed symbol does not match the application.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--duration` / `-d` | duration | `1m0s` | Duration of the trace session |
| `--format` | string | `text` | Output format (text, json, csv) |
| `--path` / `-p` | string | empty | **Required.** HTTP path to trace (required) |
| `--wait` | bool | `false` | Wait for trace to complete and display results |

### coral duckdb

Query agent DuckDB databases remotely.

```text
coral duckdb [flags]
```

Use the configured context to select the environment. `--colony` on child commands is a boolean selecting Colony storage; its absence selects agent storage. Discover available databases with `duckdb list`.

#### coral duckdb list

List available databases (colony and agents).

```text
coral duckdb list [flags]
```

Aliases: `list-agents`.

#### coral duckdb query

Execute a one-shot SQL query against a database.

```text
coral duckdb query [agent-id] <sql> [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--colony` | bool | `false` | Query colony database instead of agent |
| `--database` / `-d` | string | empty | Database name (e.g., metrics.duckdb) |
| `--format` / `-f` | string | `table` | Output format (table, csv, json) |

#### coral duckdb shell

Open an interactive DuckDB shell attached to an agent database.

```text
coral duckdb shell <agent-id> [flags]
```

Uses embedded DuckDB with the `httpfs` extension, which may need an initial download. Use `--colony` without an agent ID for Colony storage, or provide an agent ID for agent storage. Multiple-agent selections use `--agents`. Meta-commands include `.tables`, `.databases`, `.refresh`, `.help`, `.exit`, and `.quit`.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--agents` | stringSlice | `[]` | Comma-separated list of agent IDs for multi-agent queries |
| `--colony` | bool | `false` | Connect to colony database instead of agent |
| `--database` / `-d` | string | empty | Database name (e.g., metrics.duckdb) |

### coral exec

Execute command in a service's container.

```text
coral exec SERVICE [command...] [flags]
```

Requires a service and command. Selectors: bare exact process name, `id:<container-id-prefix>`, or `pid:<agent-visible-pid>`. Host PID mode requires a selector and refuses host-mount-namespace targets. Ambiguous matches fail. Default namespace is `mnt`; the selected container must contain the requested executable. See [host and container commands](user/remote-execution.md).

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--agent` | string | empty | Agent ID (resolves via colony registry) |
| `--agent-addr` | string | empty | Agent address (default: auto-discover) |
| `--colony` | string | empty | Colony ID (default: auto-detect) |
| `--container` | string | empty | Target container: process name, id:<container ID prefix>, or pid:<n>; required with hostPID |
| `--env` | stringArray | `[]` | Environment variables (KEY=VALUE) |
| `--namespaces` | stringSlice | `[mnt]` | Namespaces to enter (mnt,pid,net,ipc,uts,cgroup) |
| `--timeout` | uint32 | `30` | Timeout in seconds (max 300) |
| `--user-id` | string | empty | User ID for audit (default: $USER) |
| `--working-dir` | string | empty | Working directory in container |

### coral help

Help about any command.

```text
coral help [command] [flags]
```

### coral init

Initialize a new Coral colony.

```text
coral init <app-name> [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--discovery` | string | `https://discovery.coralmesh.dev` | Discovery service URL |
| `--env` | string | `dev` | Environment name (dev, staging, production, etc.) |
| `--storage` | string | `.coral` | Storage directory path |

### coral mcp

Integrate Coral with MCP clients.

```text
coral mcp [flags]
```

#### coral mcp configure

Generate MCP client configuration (Claude Desktop format).

```text
coral mcp configure [flags]
```

Prints a client configuration snippet; install it in your MCP client settings. A single-Colony snippet can rely on default context selection, so add explicit selection when the client needs a pinned environment.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--all-colonies` | bool | `false` | Generate config for all colonies |
| `--colony` | string | empty | Colony ID (overrides default) |

#### coral mcp list-tools

List available MCP tools.

```text
coral mcp list-tools [flags]
```

Queries the running Colony tool registry. This is distinct from the local stdio proxy exposing the single `coral_cli` tool.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--colony` / `-c` | string | empty | Colony ID (overrides auto-detection) |
| `--format` / `-o` | string | `table` | Output format (table, json) |

#### coral mcp proxy

Serve the local MCP stdio proxy.

```text
coral mcp proxy [flags]
```

Runs the local stdio MCP server and dispatches `coral_cli` calls as local Coral subprocesses. It verifies Colony connectivity at startup. The proxy appends `--format json`; commands without that flag can fail. Supply the client environment and API token to the launched proxy process.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--colony` | string | empty | Colony ID (overrides auto-detection) |

### coral mesh

Manage and troubleshoot the Coral control mesh.

```text
coral mesh [flags]
```

Diagnostics run through the Colony. Unfiltered ping/audit target registered agents; a positional agent ID selects one. `--all` cannot be combined with selectors. Filtered selections that match nothing fail.

#### coral mesh audit

Audit selected agents for NAT and mesh issues.

```text
coral mesh audit [agent-id] [flags]
```

Reports use the shared fleet envelope; NAT details live under `.results[].data`. NAT classifications are findings, while RPC/assessment failures fail the target.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--agents` | stringSlice | `[]` | Target exact agent IDs (comma-separated) |
| `--all` | bool | `false` | Target all registered agents explicitly |
| `--colony` / `-c` | string | empty | Colony ID (overrides auto-detection) |
| `--concurrency` | int | `8` | Maximum concurrent target operations (1-128) |
| `--dry-run` | bool | `false` | Preview the pinned target set without performing the operation |
| `--format` / `-o` | string | `table` | Output format (table, json) |
| `--report` | string | empty | Save a JSON result report for inspection or --retry-failed |
| `--request-timeout` | duration | `10s` | Timeout per target attempt (maximum 5m) |
| `--retries` | int | `0` | Retries per failed target for transient errors (0-10) |
| `--retry-failed` | string | empty | Retry only failed/canceled targets from a saved JSON report |
| `--runtime` | stringSlice | `[]` | Filter runtime: native,docker,k8s-sidecar,k8s-daemonset,unknown (comma-separated) |
| `--service` | stringSlice | `[]` | Filter exact service names (comma-separated) |
| `--status` | stringSlice | `[]` | Filter heartbeat status: healthy,degraded,unhealthy (comma-separated) |
| `--version` | stringSlice | `[]` | Filter exact agent versions (comma-separated) |

#### coral mesh ping

Ping selected agents through the Colony mesh.

```text
coral mesh ping [agent-id] [flags]
```

Encrypted UDP echo through the mesh, not ICMP or application health. A target fails when no response arrives or packet loss exceeds 50%. `--timeout` bounds an individual ping; `--request-timeout` must allow the complete target attempt.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--agents` | stringSlice | `[]` | Target exact agent IDs (comma-separated) |
| `--all` | bool | `false` | Target all registered agents explicitly |
| `--colony` / `-c` | string | empty | Colony ID (overrides auto-detection) |
| `--concurrency` | int | `8` | Maximum concurrent target operations (1-128) |
| `--count` / `-n` | int | `4` | Number of pings per agent (1-100) |
| `--dry-run` | bool | `false` | Preview the pinned target set without performing the operation |
| `--format` / `-o` | string | `table` | Output format (table, json) |
| `--report` | string | empty | Save a JSON result report for inspection or --retry-failed |
| `--request-timeout` | duration | `10s` | Timeout per target attempt (maximum 5m) |
| `--retries` | int | `0` | Retries per failed target for transient errors (0-10) |
| `--retry-failed` | string | empty | Retry only failed/canceled targets from a saved JSON report |
| `--runtime` | stringSlice | `[]` | Filter runtime: native,docker,k8s-sidecar,k8s-daemonset,unknown (comma-separated) |
| `--service` | stringSlice | `[]` | Filter exact service names (comma-separated) |
| `--status` | stringSlice | `[]` | Filter heartbeat status: healthy,degraded,unhealthy (comma-separated) |
| `--timeout` / `-W` | duration | `2s` | Timeout for each ping (1ms-1m); --request-timeout bounds the whole attempt |
| `--version` | stringSlice | `[]` | Filter exact agent versions (comma-separated) |

### coral profile

Collect performance profiles on-demand.

```text
coral profile [flags]
```

#### coral profile cpu

Collect CPU profile on-demand.

```text
coral profile cpu [flags]
```

Requires `--service`. Duration is integer seconds (maximum 300); frequency is Hz (maximum 1000). Default output is folded stacks. Needs a Linux observation agent with working perf/BPF collection.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--duration` / `-d` | int32 | `30` | Profiling duration in seconds (default: 30s, max: 300s) |
| `--format` | string | `folded` | Output format: folded (default), json |
| `--frequency` | int32 | `99` | Sampling frequency in Hz (default: 99Hz, max: 1000Hz) |
| `--pod` | string | empty | Pod name (optional, specific instance) |
| `--service` / `-s` | string | empty | **Required.** Service name (required) |

#### coral profile memory

Collect memory profile on-demand.

```text
coral profile memory [flags]
```

Requires `--service` and a compatible heap-profile runtime endpoint. CPU eBPF support alone does not provide heap profiles for every language.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--duration` / `-d` | int32 | `30` | Profiling duration in seconds |
| `--format` | string | `folded` | Output format: folded, json |
| `--sample-rate` | int32 | `512` | Sampling rate in KB (default: 512KB) |
| `--service` / `-s` | string | empty | **Required.** Service name (required) |

### coral proxy

Manage local proxy for colony access.

```text
coral proxy [flags]
```

Legacy HTTP forwarding proxy, distinct from `mcp proxy`. Do not make it a prerequisite for normal local CLI or configured public HTTPS access.

#### coral proxy start

Start a proxy for a colony.

```text
coral proxy start <colony-mesh-id> [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--discovery` | string | `https://discovery.coralmesh.dev` | Discovery service endpoint |
| `--listen` | string | `localhost:8000` | Local address to bind to |

#### coral proxy status

Show status of running proxies.

```text
coral proxy status [flags]
```

Placeholder: does not inspect a real managed proxy process.

#### coral proxy stop

Stop a running proxy.

```text
coral proxy stop [colony-mesh-id] [flags]
```

Placeholder: does not terminate a managed proxy process. Stop the foreground proxy or its supervisor.

### coral query

Query observability data (metrics, traces, logs, profiles).

```text
coral query [flags]
```

Commands use positional service names where shown. `--since` support, defaults, and formats vary by child command.

#### coral query cpu-profile

Query historical CPU profiles.

```text
coral query cpu-profile [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--build-id` | string | empty | Filter by specific build ID |
| `--format` | string | `folded` | Output format: folded, json |
| `--service` / `-s` | string | empty | **Required.** Service name (required) |
| `--since` | string | `1h` | Query from this time ago (e.g., '1h', '30m', '24h') |
| `--until` | string | empty | Query until this time ago (default: now) |

#### coral query logs

Query logs.

```text
coral query logs [service] [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--format` | string | `text` | Output format (text, json) |
| `--level` | string | empty | Log level filter: debug, info, warn, error |
| `--max-logs` | int | `100` | Maximum number of logs to return |
| `--search` | string | empty | Full-text search query |
| `--since` | string | `1h` | Time range (e.g., 1h, 30m, 24h) |

#### coral query memory-profile

Query historical memory profiles.

```text
coral query memory-profile [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--build-id` | string | empty | Filter by specific build ID |
| `--format` / `-f` | string | `summary` | Output format: 'summary' (human/LLM readable) or 'folded' (flamegraph compatible) |
| `--service` / `-s` | string | empty | **Required.** Service name (required) |
| `--show-growth` | bool | `false` | Show heap growth trends |
| `--show-types` | bool | `false` | Show allocation breakdown by type |
| `--since` | string | `1h` | Query from this time ago (e.g., '1h', '30m', '24h') |
| `--until` | string | empty | Query until this time ago (default: now) |

#### coral query metrics

Query metrics.

```text
coral query metrics [service] [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--format` | string | `text` | Output format (text, json) |
| `--http-method` | string | empty | HTTP method filter (GET, POST, etc.) |
| `--http-route` | string | empty | HTTP route pattern filter |
| `--metric` | string | empty | Metric name for focused query (e.g., http.server.duration) |
| `--percentile` | float64 | `0` | Percentile to query (0-100, e.g., 99 for P99) |
| `--protocol` | string | `auto` | Protocol: http, grpc, sql, or auto |
| `--since` | string | `1h` | Time range (e.g., 1h, 30m, 24h) |
| `--source` | string | `all` | Data source: ebpf, telemetry, or all |
| `--status-code-range` | string | empty | Status code range (2xx, 3xx, 4xx, 5xx) |

#### coral query sql

Execute raw SQL query.

```text
coral query sql <query> [flags]
```

Executes a guarded, bounded SQL query and prints a text table. It has no `--format` option. Inspect schemas before relying on table names from older examples.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--max-rows` | int32 | `1000` | Maximum rows to return (default: 1000) |

#### coral query summary

Get a high-level health summary.

```text
coral query summary [service] [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--format` | string | `text` | Output format (text, json) |
| `--since` | string | `5m` | Time range (e.g., 5m, 1h, 24h) |

#### coral query topology

Show the service dependency graph.

```text
coral query topology [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--format` | string | `text` | Output format (text, json) |
| `--include-l4` | bool | `true` | Include L4 network edges |

#### coral query trace-profile

Correlate a distributed trace with CPU profiles.

```text
coral query trace-profile <trace-id> [flags]
```

Current correlation supplies CPU evidence matched by service, process, and padded time window, not exact request/goroutine attribution. The accepted `--type memory` does not establish implemented memory correlation. This command has no JSON format option.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--service` / `-s` | string | empty | Filter to specific service name |
| `--top` | int | `10` | Number of top hotspots to display per service |
| `--type` | string | `cpu` | Profile type: cpu, memory |

#### coral query traces

Query distributed traces.

```text
coral query traces [service] [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--format` | string | `text` | Output format (text, json) |
| `--max-traces` | int | `10` | Maximum number of traces to return |
| `--min-duration-ms` | int | `0` | Minimum trace duration in milliseconds |
| `--since` | string | `1h` | Time range (e.g., 1h, 30m, 24h) |
| `--source` | string | `all` | Data source: ebpf, telemetry, or all |
| `--trace-id` | string | empty | Specific trace ID |

### coral run

Execute TypeScript script with Deno.

```text
coral run <script.ts> [flags]
```

Takes a positional script filename; there is no `--file` option. File-based execution needs a readable import map/SDK and restricts filesystem reads to the working tree. The current TypeScript client uses internal HTTP without the CLI public-API token/TLS support. See [TypeScript investigations](user/scripting.md).

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--timeout` | int | `60` | Script timeout in seconds (default: 60) |
| `--watch` | bool | `false` | Re-run script on file changes |

### coral script

Manage investigation scripts.

```text
coral script [flags]
```

#### coral script list

List scripts in ~/.coral/scripts/.

```text
coral script list [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--format` / `-o` | string | `table` | Output format (table, json) |

#### coral script remove

Remove a script from ~/.coral/scripts/.

```text
coral script remove [flags]
```

Requires `--name`; removes the local source file. It does not undeploy runtime correlations created by that script.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--name` | string | empty | **Required.** Script name (required) |

#### coral script write

Write a TypeScript script to ~/.coral/scripts/<name>.ts.

```text
coral script write [flags]
```

Requires `--name` and nonempty `--content` or `--file`. Names use letters, digits, hyphens, and underscores. Writing an existing name replaces the stored source.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--content` | string | empty | Script content (TypeScript source) |
| `--file` | string | empty | Read content from this file instead of --content |
| `--name` | string | empty | **Required.** Script name (required) |

### coral services

List and manage services.

```text
coral services [flags]
```

With no subcommand, lists services. `--local` and `--agent` select agent inventory; the default is merged Colony inventory. `--source auto|watched` applies to local/agent inventories. `--since` controls the Colony telemetry discovery window and is rejected for local/agent scope.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--agent` | string | empty | Agent ID (resolves via colony registry) |
| `--colony` / `-c` | string | empty | Colony ID (overrides auto-detection) |
| `--format` / `-o` | string | `table` | Output format (table, json) |
| `--local` | bool | `false` | List services on the local agent |
| `--name` | stringSlice | `[]` | Filter by exact service name (comma-separated) |
| `--since` | string | `1h` | Telemetry discovery window for colony inventory (e.g. 10m, 24h) |
| `--sort` | string | `name` | Sort by name,instances,last-seen (colony) or name,port,pid (agent); prefix with - to reverse |
| `--source` | string | empty | Filter by source: auto or watched |
| `--wide` | bool | `false` | Show all agent IDs in colony output |

#### coral services list

List services.

```text
coral services list [flags]
```

Agent inventory uses direct agent access after target resolution; the CLI must reach that agent. Health-check state, heartbeat liveness, registration, and telemetry activity are separate. Cached or missing check data is not evidence of health.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--agent` | string | empty | Agent ID (resolves via colony registry) |
| `--colony` / `-c` | string | empty | Colony ID (overrides auto-detection) |
| `--format` / `-o` | string | `table` | Output format (table, json) |
| `--local` | bool | `false` | List services on the local agent |
| `--name` | stringSlice | `[]` | Filter by exact service name (comma-separated) |
| `--since` | string | `1h` | Telemetry discovery window for colony inventory (e.g. 10m, 24h) |
| `--sort` | string | `name` | Sort by name,instances,last-seen (colony) or name,port,pid (agent); prefix with - to reverse |
| `--source` | string | empty | Filter by source: auto or watched |
| `--wide` | bool | `false` | Show all agent IDs in colony output |

#### coral services watch

Watch and enrich one or more services.

```text
coral services watch [service-spec...] [flags]
```

Specifications use `name:port[:health][:type]`. Single-name syntax accepts `--port` or `--exe-pattern`, never both. Direct watch defaults to the local agent; `--colony` requires `--agent` or fleet selection. Fleet watch needs explicit selectors or `--all`; `--agent-url` and `--wait` do not apply to the fleet path. Matching existing registrations are unchanged; conflicts are not silently updated.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--agent` | string | empty | Agent ID (resolves via colony registry) |
| `--agent-url` | string | empty | Agent URL (default: auto-discover) |
| `--agents` | stringSlice | `[]` | Target exact agent IDs (comma-separated) |
| `--all` | bool | `false` | Target all registered agents explicitly |
| `--colony` / `-c` | string | empty | Colony ID (overrides auto-detection) |
| `--concurrency` | int | `8` | Maximum concurrent target operations (1-128) |
| `--dry-run` | bool | `false` | Preview the pinned target set without performing the operation |
| `--exe-pattern` | string | empty | Executable pattern for a portless process, matched against /proc/<pid>/comm and cmdline (only works with single service, mutually exclusive with --port) |
| `--format` / `-o` | string | `table` | Output format (table, json) |
| `--health` | string | empty | Health check endpoint (legacy, only works with single service) |
| `--port` / `-p` | int | `0` | Service port (legacy, only works with single service) |
| `--report` | string | empty | Save a JSON result report for inspection or --retry-failed |
| `--request-timeout` | duration | `10s` | Timeout per target attempt (maximum 5m) |
| `--retries` | int | `0` | Retries per failed target for transient errors (0-10) |
| `--retry-failed` | string | empty | Retry only failed/canceled targets from a saved JSON report |
| `--runtime` | stringSlice | `[]` | Filter runtime: native,docker,k8s-sidecar,k8s-daemonset,unknown (comma-separated) |
| `--service` | stringSlice | `[]` | Filter exact service names (comma-separated) |
| `--status` | stringSlice | `[]` | Filter heartbeat status: healthy,degraded,unhealthy (comma-separated) |
| `--version` | stringSlice | `[]` | Filter exact agent versions (comma-separated) |
| `--wait` | bool | `false` | Wait for initial health checks and display status (recommended for interactive use) |

### coral shell

Open interactive shell or execute command in agent environment.

```text
coral shell [command...] [flags]
```

Executes in the agent environment; it does not enter an application container. Omit the command for an interactive PTY, or use `--` followed by a command and arguments. User labels are audit metadata, not OS-user impersonation.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--agent` | string | empty | Agent ID (resolves via colony registry) |
| `--agent-addr` | string | empty | Agent address (default: auto-discover) |
| `--colony` | string | empty | Colony ID (default: auto-detect) |
| `--user-id` | string | empty | User ID for audit (default: $USER) |

### coral status

Show global Coral environment status.

```text
coral status [flags]
```

Queries configured Colonies with their endpoint, TLS trust, and current token. Request failure means unreachable; invalid configuration or mismatched identity can mean unknown. Neither implies a stopped Colony.

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--format` / `-o` | string | `table` | Output format (table, json) |

### coral terminal

Launch the mission-control TUI.

```text
coral terminal [flags]
```

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--auto-browser` | bool | `false` | Automatically open the browser dashboard on first render event |
| `--colony` | string | empty | Colony ID to connect to (default: from config) |
| `--debug` | bool | `false` | Enable debug logging to stderr |
| `--model` | string | empty | Override LLM model (e.g. anthropic:claude-sonnet-4-6) |

### coral version

Show version information.

```text
coral version [flags]
```

## Fleet selection and reports

`colony agents`, mesh diagnostics, and fleet service watches share exact-match
selectors: `--agents`, `--status`, `--service`, `--runtime`, and `--version`.
Values within one selector are ORed; different selectors are ANDed. Agent
status refers to heartbeat liveness, not service health checks. Runtime names
include `native`, `docker`, `k8s-sidecar`, `k8s-daemonset`, and `unknown`.

Bulk `--dry-run` reads inventory and prints planned targets without pinging,
auditing, or registering services. Target IDs are pinned before execution.
`--concurrency` defaults to 8 (range 1–128), `--request-timeout` to 10s (maximum
5m), and `--retries` to zero (maximum 10). Only transient failures are retried;
successful service steps are retained.

Reports contain `version`, `operation`, `colony_id`, `parameters`, `dry_run`,
timestamps, `summary`, and ordered `results`. Target status is `planned`,
`succeeded`, `failed`, or `canceled`. `--retry-failed` selects only failed or
canceled IDs from a saved report; it cannot combine with new selectors or
`--all`. Omitted operation parameters are restored from the report; conflicting
parameters or a different Colony are rejected. Dry-run reports cannot be retried.

`--report` saves JSON atomically with owner-only permissions. An unusable report
destination is rejected before operations begin. `--format json` prints the
same report to stdout; progress and command errors use stderr. Failed or canceled
targets cause a nonzero exit status after results are printed and the report
save is attempted. Successful targets are not rolled back.

### Fleet report format

Ping, audit, and fleet watch share a version 1 envelope. This structural example
shows a partial failure; operation-specific `data` fields are omitted:

```json
{
  "version": 1,
  "operation": "mesh ping",
  "colony_id": "production",
  "parameters": {"count": 4, "timeout": "2s"},
  "dry_run": false,
  "started_at": "2026-11-12T18:00:00Z",
  "finished_at": "2026-11-12T18:00:01Z",
  "summary": {"total": 2, "succeeded": 1, "failed": 1, "canceled": 0, "planned": 0},
  "results": [
    {"agent_id": "agent-1", "status": "succeeded", "attempts": 1},
    {"agent_id": "agent-2", "status": "failed", "attempts": 1,
     "error_code": "unavailable", "error": "unavailable: no replies"}
  ]
}
```

Results are ordered by full agent ID. `attempts` counts attempts in the current
invocation. Optional fields include `error_code`, `error`, a human-readable
`message`, and operation-specific `data`:

- Ping and audit store their native diagnostic result in `data`.
- Fleet watch stores a `data.services` array with each registration's `name`
  and `status` (`connected`, `unchanged`, or `failed`). Failed services retain
  their `error` and a `retryable` flag when the error is transient.

Agent inventory JSON remains an array. Scripts that read the former mesh audit
result array must instead read `.results[].data` from the shared envelope.

See [fleet operations](user/fleet.md) for selection, verification, and recovery
procedures.

## Compatibility names

- `coral connect` is a permanent hidden alias for `coral services watch`, with
  the same arguments and options.
- `coral colony service list` is a hidden legacy service-list entry point.
- `coral colony mcp` retains legacy MCP children. Prefer `coral mcp configure`,
  `coral mcp list-tools`, and `coral mcp proxy`. Legacy `generate-config`
  corresponds to client configuration; `test-tool` belongs to that legacy
  interface rather than the local proxy's single `coral_cli` vocabulary.
- `--monitor-all` on agent startup is a deprecated no-op; use the default or
  opt out with `--no-monitor-all`.

Internal commands such as `_tun-helper` are implementation details and are
excluded from the user command inventory.

## Environment

| Variable | Purpose |
| --- | --- |
| `CORAL_CONFIG` | Replaces the loader's base directory: `<base>/.coral/config.yaml`; default base is the user's home. Does not relocate every state file |
| `CORAL_COLONY_ID` | Colony selection, ahead of project selection and global default |
| `CORAL_COLONY_ENDPOINT` | Explicit operator URL; also an optional direct agent bootstrap URL |
| `CORAL_API_TOKEN` | Bearer token sent by supported Colony clients to the public API |
| `CORAL_CA_FILE`, `CORAL_CA_DATA` | Client CA file or base64-encoded CA data |
| `CORAL_INSECURE` | Disable client TLS verification for isolated testing |
| `CORAL_CA_FINGERPRINT` | Expected Colony Root CA fingerprint for bootstrap |
| `CORAL_BOOTSTRAP_PSK` | Secret authorizing initial agent enrollment |
| `CORAL_CERTS_DIR` | Agent certificate and enrollment state directory |
| `CORAL_DISCOVERY_ENDPOINT` | Discovery service override |
| `CORAL_PUBLIC_ENDPOINT` | Colony advertised WireGuard UDP endpoint override, `host:port`, comma-separated for multiple |
| `CORAL_MESH_SUBNET` | Colony IPv4 mesh subnet override |
| `CORAL_BOOTSTRAP_PUBLIC_ENDPOINT` | Agent dialable TCP endpoint for reverse-dial bootstrap |
| `CORAL_BOOTSTRAP_LISTEN_PORT` | Local reverse-dial listener port, default 8444 |
| `CORAL_VERIFY_BOOTSTRAP_REACHABILITY` | Opt-in Discovery reachability diagnostic |
| `CORAL_SERVICES` | Agent service specifications; replaces configured service list |
| `CORAL_MONITOR_ALL` | Default-on process observation; set false to opt out |
| `CORAL_LOG_LEVEL`, `CORAL_LOG_FORMAT` | Agent logging configuration |
| `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GOOGLE_API_KEY` | Provider credentials when configured as environment key sources |
| `OPENAI_BASE_URL` | OpenAI-compatible endpoint fallback, after configured `base_urls.openai` |

Additional collection environment settings and YAML field definitions live in
[the configuration reference](user/configuration-reference.md). Settings are consumed by their corresponding component;
exporting a value in an operator shell does not update an already running server.

## Files

| Path | Purpose |
| --- | --- |
| `~/.coral/config.yaml` | Global defaults, Discovery and AI settings |
| `~/.coral/colonies/<id>/config.yaml` | Per-Colony server or remote connection settings |
| `<project>/.coral/config.yaml` | Project context and local settings |
| `./agent.yaml`, `/etc/coral/agent.yaml` | Agent startup file search order, unless `--config` is explicit |
| `~/.coral/colonies/<id>/ca/` | Colony CA material; protect private keys |
| `~/.coral/colonies/<id>/tokens.yaml` | Public API token store, unless configured elsewhere |
| `~/.coral/certs/` | Default agent credentials and `enrollment.json` checkpoint |
| `~/.coral/agent/metrics.duckdb` | Shared agent observation database in its process home |
| `~/.coral/scripts/` | Stored investigation script sources |

Colony data uses its resolved storage path. Inspect configuration and startup
logs before backups or relocation; the configuration directory alone is not a
complete state backup.

## Exit status and output

The full CLI exits 0 when a handler returns success and 1 when it returns an
error. Some status commands print missing or unhealthy state while returning
success; do not interpret the exit code alone as a health verdict. Registered
placeholders are called out above.

Bulk operations return a nonzero status for any failed or canceled target after
printing/saving results. With JSON format, their reports remain valid JSON on
stdout while progress and command errors use stderr. Other commands have their
own output contracts; inspect their documented formats before piping output.
Remote execution can translate a returned command failure into a CLI error;
it does not universally preserve the child's numeric exit code.

## Examples

```bash
coral init shop --env dev
coral config current-context --verbose
coral colony agents --service api
coral services list --format json
coral services watch api:8080:/health
coral query summary api --since 10m
coral profile cpu --service api --duration 30 > api.folded
coral debug session list --service api
coral mesh ping --service api --dry-run
coral script remove --name old-investigation
```

Server startup, privilege setup, credentials, and verification are explained in
[the user guide](user/README.md). Configuration fields live in the
[configuration reference](user/configuration-reference.md); programmatic analysis lives in the
[TypeScript SDK reference](user/sdk-reference.md).
