# Configuration and Remote Access

Coral has separate settings for your CLI, the Colony server, and each agent.
Start with the task you want to perform:

| I want to… | Go to |
| --- | --- |
| Find the file to edit | [Settings and state](#locate-settings-and-state) |
| Choose the Colony my CLI uses | [Select a Colony](#select-a-colony) |
| Choose which processes an agent observes | [Configure an agent](#configure-an-agent) |
| Adjust collection or data retention | [Collection and retention](#choose-collection-and-retention-settings) |
| Change the mesh address range | [Mesh routes](#avoid-conflicting-mesh-routes) |
| Check a change | [Verification](#verify-a-configuration-change) |
| Query a Colony from another workstation | [Remote access](#connect-a-remote-workstation) |

For a new deployment, follow [your first Colony](getting-started.md) first.
For individual YAML fields and defaults, use the
[configuration reference](configuration-reference.md).

## Locate settings and state

Choose the file for the component you are changing. Paths below use the home
directory of the account running that component.

| File | What belongs here | How to create it |
| --- | --- | --- |
| `~/.coral/config.yaml` | CLI default Colony, Discovery, and global AI settings | `coral init`, `coral config use-context`, or `coral ask config` |
| `~/.coral/colonies/<id>/config.yaml` on the server | Colony identity, networking, polling, and central retention | `coral init` |
| `~/.coral/colonies/<name>/config.yaml` on a workstation | Remote endpoint and trust settings | `coral colony add-remote` |
| `<project>/.coral/config.yaml` | This project's Colony selection and local settings | `coral init` in the project directory |
| Agent YAML | Enrollment, service watches, and local collection | Create a file and pass it to `coral agent start --config` |

Without `--config`, an agent loads the first file it finds: `./agent.yaml`,
then `/etc/coral/agent.yaml`. Colony configuration also has
[system-wide fallback paths](configuration-reference.md#configuration-files).

### Use a different configuration base

`CORAL_CONFIG` sets the **parent of `.coral`** for global and per-Colony files:

```bash
export CORAL_CONFIG=/srv/coral-config
```

This selects `/srv/coral-config/.coral/config.yaml` and
`/srv/coral-config/.coral/colonies/<id>/config.yaml`. Project configuration
still comes from the current directory; agent YAML still uses its own file
search or `--config`.

### Locate persistent data

Stored data lives apart from configuration. `CORAL_CONFIG` moves only the
`.coral` configuration files; these paths stay where listed:

| State | Default location | Control |
| --- | --- | --- |
| Colony database | `.coral` | `coral init --storage`, or a project `storage.path` |
| Agent observation database | `~/.coral/agent/metrics.duckdb` in the agent process's home | The agent's home directory |
| Agent certificates | `~/.coral/certs/` | `CORAL_CERTS_DIR` |

Run each component under the same account, home, and working directory every
time, or it will not find its data. Before moving or backing up data, read the
startup logs for the paths in use; [Colony operations](colony-operations.md)
covers maintenance.

## Select a Colony

A CLI context selects the Colony a command targets. It does not change where
an already running agent reports.

List saved contexts and check the active selection:

```bash
coral config get-contexts
coral config current-context --verbose
```

To change the global default, use an ID or local connection name from that list:

```bash
coral config use-context '<colony-id-or-connection-name>'
coral config current-context --verbose
```

The CLI selects a Colony in this order, using the first available value:

1. An explicit target option, on commands that support one.
2. `CORAL_COLONY_ID` in the command's environment.
3. `colony_id` in the current directory's `.coral/config.yaml`.
4. `default_colony` in the global `~/.coral/config.yaml`.

For example, to keep a project pointed at one Colony, set its project file to:

```yaml
# <project>/.coral/config.yaml
version: "1"
colony_id: '<colony-id>'
```

That project selection takes precedence over `use-context`, and an exported
`CORAL_COLONY_ID` takes precedence over both. If a command targets an unexpected
Colony, run `coral config current-context --verbose` to see which source won.

The connection URL is resolved separately: `CORAL_COLONY_ENDPOINT` overrides
the saved remote endpoint. Switching contexts does not change environment
variables, so unset a stale `CORAL_COLONY_ENDPOINT` or `CORAL_API_TOKEN` when
you switch environments.

Target options vary by command. For example, `--colony` is not a global flag, and
`coral duckdb ... --colony` is a boolean that selects the Colony database. Check
the [CLI manual](../CLI_REFERENCE.md) for the command you are using.

## Configure an agent

An agent can observe all processes automatically or use explicit service watches.
To watch only an API on port 8080, merge this fragment into its YAML file,
keeping its existing enrollment settings:

```yaml
# /etc/coral/agent.yaml (agent settings)
agent:
  monitor_all: false
services:
  - name: api
    port: 8080
    health_endpoint: /health
```

Replace the port and health path with your application's values; omit
`health_endpoint` if it has no HTTP health check. For a new agent, obtain
[enrollment credentials](getting-started.md#export-agent-enrollment-credentials)
first. This fragment only configures observation.

Start the agent with that file, using the account, enrollment environment, and
[privileges](installation.md) prepared for the deployment:

```bash
coral agent start --config /etc/coral/agent.yaml
```

With the agent-only binary, use `coral-agent start --config /etc/coral/agent.yaml`.
For a supervised service, edit the file used by its supervisor and restart the
service. To add a watch without restarting, use [services watch](services.md).

### Understand agent overrides

Agent settings use this precedence, from highest to lowest:

1. Explicit startup options for the settings they control, such as `--colony`
   and `--no-monitor-all`.
2. Supported `CORAL_*` environment variables in the agent process.
3. The selected agent YAML file.
4. Built-in defaults.

For example, `CORAL_MONITOR_ALL=true` overrides `monitor_all: false` in YAML;
`--no-monitor-all` forces it off. `CORAL_SERVICES` replaces the file's service
list instead of appending to it. See the
[environment variable reference](configuration-reference.md#environment-variables)
for common collector overrides.

Turning off `monitor_all` controls automatic process observation. Telemetry,
host metrics, profiling, and Beyla have separate collector switches.

## Choose collection and retention settings

Agents collect observations and retain local data. The Colony polls agents
and retains central summaries. Configure each side in its own file.

| Setting | What it controls |
| --- | --- |
| Agent CPU `frequency_hz` | How often CPU samples are taken |
| Agent collector `interval` | How often observations are collected or aggregated |
| Agent `retention` | How long raw observations remain on that agent |
| Colony `poll_interval` | How often the Colony fetches observations from agents |
| Colony `retention_days` or `retention.*_days` | How long central data remains available |

### On the agent

Merge this fragment into the agent file to collect host metrics every 30 seconds
and keep local metrics and profiles for two hours:

```yaml
# /etc/coral/agent.yaml (agent settings)
system_metrics:
  interval: 30s
  retention: 2h
continuous_profiling:
  cpu:
    frequency_hz: 19
    interval: 15s
    retention: 2h
    metadata_retention: 168h
  memory:
    interval: 60s
    retention: 2h
```

Merge into existing sections rather than adding duplicate YAML keys. Restart
the agent after editing. CPU and heap collection still need the platform and
runtime support described in [profiling](profiling.md).

To disable a collector, set its `disabled` switch to `true`, for example
`system_metrics.disabled` or `continuous_profiling.cpu.disabled`. Setting
`continuous_profiling.disabled: true` disables both continuous profilers.

### On the Colony

Merge this separate fragment into the **server's Colony file**:

```yaml
# ~/.coral/colonies/<colony-id>/config.yaml (Colony server settings)
beyla:
  poll_interval: 60s
  retention:
    http_days: 14
    grpc_days: 14
    sql_days: 7
    traces_days: 3
system_metrics:
  poll_interval: 60s
  retention_days: 14
continuous_profiling:
  poll_interval: 30s
  retention_days: 14
```

Restart the Colony to apply these changes. Central and agent retention are
independent. Choose local retention long enough for expected outages, then check
actual disk use. These periods are choices, not capacity or delivery guarantees.

Duration fields accept strings such as `30s`, `2h`, and `168h`. Use `168h` for
seven days; `7d` is not accepted. Fields ending in `_days` or `_hours` take integer
counts instead. The [configuration reference](configuration-reference.md) lists
defaults and field types.

## Avoid conflicting mesh routes

Choose a mesh subnet that does not overlap routes on the Colony, agents, or
participating workstations. The default `100.64.0.0/10` is also used by some VPNs;
compare your actual routes before selecting a range.

For a new deployment, merge both fields into the Colony server's configuration.
Use this example only if `10.42.0.0/24` is available in your network:

```yaml
# ~/.coral/colonies/<colony-id>/config.yaml (Colony server settings)
wireguard:
  mesh_network_ipv4: 10.42.0.0/24
  mesh_ipv4: 10.42.0.1
```

Coral requires an IPv4 subnet of `/24` or larger, and `mesh_ipv4` must lie inside
it: changing the subnet does not recalculate an existing `mesh_ipv4`.

Choose the subnet before enrolling agents. Coral has no documented migration for
an enrolled deployment, and a later change alters mesh addresses and routes, so
rehearse it on a non-production Colony first. Public endpoint addresses are a
separate [connectivity setting](deployment.md#network-paths).

## Verify a configuration change

Use the checks for the component you changed:

| Changed component | Check |
| --- | --- |
| CLI selection | Run `coral config current-context --verbose` to confirm the Colony and selection source. |
| Saved Colony settings | Run `coral config view --colony '<colony-id>'` and `coral config validate`. Inspect all reported failures, including those in JSON output. |
| Agent | Restart it, inspect startup logs, then run `coral services list --local` on the agent host and check fresh telemetry. |
| Colony server | Restart it, then follow the runtime checks in [Colony operations](colony-operations.md). |

Keep configuration output private: it can contain WireGuard keys and other
credentials. Two limits to know:

- `config view` shows Colony settings and context information, not a running
  agent's merged settings or every project override.
- `config validate` checks saved Colony configurations only. Agent configuration
  errors appear in the agent's startup log, and a validation run can report
  several failures, so read all of its output, including JSON.

## Connect a remote workstation

Remote operators query the Colony through its HTTPS endpoint. The administrator
must first [enable operator HTTPS access](deployment.md#operator-https-access)
and supply an API token plus the trust information for one of these methods.

### Add the connection

For a manual connection, obtain the HTTPS endpoint URL and a trusted CA
certificate file from the administrator. Run on the workstation:

```bash
coral colony add-remote production \
  --endpoint https://colony.example.com:8443 \
  --ca-file ./colony-ca.crt \
  --set-default
```

Alternatively, obtain the actual Colony ID and an independently confirmed root
CA fingerprint. Use Discovery to fetch the advertised endpoint and verify its CA:

```bash
coral colony add-remote production \
  --from-discovery \
  --colony-id '<actual-colony-id>' \
  --ca-fingerprint 'sha256:<root-ca-fingerprint>' \
  --set-default
```

Choose one method. In both examples, `production` is a local connection name.
For custom Discovery, add `--discovery-endpoint <url>` to the second command.
Manual mode's `--insecure` disables CA verification; reserve it for isolated tests.

### Authenticate and verify access

```bash
export CORAL_API_TOKEN='<operator-api-token>'
coral config current-context --verbose
coral query summary
```

Discovery does not issue an API token; supply it separately. If the selected
context is not `production`, check the [project and environment overrides](#select-a-colony).
Also check for a stale `CORAL_COLONY_ENDPOINT` overriding the saved URL.

Verify query access before configuring an [AI client](ai-assistants.md). Some
commands call agents directly and require mesh reachability; Colony HTTPS access
alone does not give the workstation a route to every agent. Each workflow chapter
identifies its connectivity requirements.

For HTTPS certificates signed by the Colony CA, the administrator can distribute
`~/.coral/colonies/<colony-id>/ca/root-ca.crt` from the Colony host and confirm
its fingerprint with `coral colony ca status`. Keep the adjacent CA private keys
on the Colony host. If HTTPS uses another certificate authority, supply that
authority's CA instead.
