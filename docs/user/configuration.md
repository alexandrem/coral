# Configuration and Remote Access

Coral separates the operator's selected Colony, the Colony server's settings,
and the agent's collectors. Changing the CLI's current context does not move
an already running agent into another Colony.

## Select a Colony

```bash
coral config get-contexts
coral config current-context --verbose
coral config use-context '<colony-id>'
coral config view
coral config validate
```

For commands that expose `--colony`, the explicit flag selects the target.
Otherwise resolution uses `CORAL_COLONY_ID`, the current directory's project
`.coral/config.yaml`, then the global default.
`use-context` changes that global default; a project selection or environment
override can still take precedence. Check `current-context --verbose` when a
command uses an unexpected Colony.

`CORAL_COLONY_ENDPOINT` separately overrides the connection URL. Clear stale
endpoint and token overrides when switching environments; choosing a context
does not clear them. Flags are command-specific: `--colony` is not a root-level
global flag, and `coral duckdb ... --colony` is a boolean selecting the Colony
database. See the [CLI manual](../CLI_REFERENCE.md).

## Locate settings and state

| Location | Purpose |
| --- | --- |
| `~/.coral/config.yaml` | Global Discovery, AI settings, and default Colony |
| `~/.coral/colonies/<id>/config.yaml` | Colony identity, networking, collection settings, or a remote connection |
| `<project>/.coral/config.yaml` | Project Colony selection and local settings |
| `./agent.yaml`, then `/etc/coral/agent.yaml` | Agent startup searches these in order unless `--config` specifies a file |
| `~/.coral/certs/` | Default agent certificate state; override with `CORAL_CERTS_DIR` |
| `~/.coral/agent/metrics.duckdb` | Agent shared observation database in the agent process's home |

`CORAL_CONFIG` replaces the base directory to which the loader appends `.coral`.
For example, `CORAL_CONFIG=/srv/coral-config` reads global configuration from
`/srv/coral-config/.coral/config.yaml`. Use the parent directory, even though
some command help describes this variable as the configuration directory.
It does not relocate every state file: agent storage, certificates,
conversation history, and scripts have their own paths. Pin the service account
and its home directory when deploying, and inspect startup logs for the actual
database and identity paths.

`init --storage` sets Colony storage, with `.coral` as its default. A project
storage setting can affect the resolved path. Use `config view` before moving
or backing up an installation. The [configuration reference](configuration-reference.md)
contains the full field definitions.

## Configure an agent

Agent startup loads defaults, then its YAML file, then supported `CORAL_*`
environment overrides. Explicit startup options such as `--colony` and
`--no-monitor-all` take precedence for their settings. `CORAL_SERVICES` replaces
the file's service list rather than appending to it.

This YAML fragment adds one watch and turns off observation of all other
processes. Merge it into the agent's file alongside its enrollment settings:

```yaml
agent:
  monitor_all: false
services:
  - name: api
    port: 8080
    health_endpoint: /health
system_metrics:
  interval: 15s
continuous_profiling:
  cpu:
    frequency_hz: 19
```

```bash
coral-agent start --config /etc/coral/agent.yaml
```

Use the privileges described in [installation](installation.md). Restart the
agent after editing startup configuration; this guide does not assume hot
reload. To add a watch to a running agent, use [services watch](services.md).
Turning off `monitor_all` does not disable every collector; telemetry, host
metrics, profiling, and Beyla each have separate controls.

## Choose collection and retention settings

Adjust collection to the evidence you need and the resources available on the
agent. Sampling frequency, collection interval, and retention solve different
problems: frequency affects how much CPU evidence is captured, interval controls
how often samples are gathered into a batch, and retention controls how long
stored evidence remains available.

For example, this agent fragment keeps recent raw profiles for two hours and
collects host metrics every 30 seconds:

```yaml
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

Heap collection still needs a compatible runtime endpoint. To disable a
collector, use its `disabled` switch; turning off automatic process discovery
alone does not disable host metrics or all profiling.

The Colony has its own polling and summary-retention settings. Apply this
separate fragment to the Colony file, not `agent.yaml`:

```yaml
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

Changing central retention does not change agent retention. Choose agent
retention long enough for the outages you expect to bridge, and check actual
disk use after changing either side. These example periods are choices, not
capacity or delivery guarantees.

Use duration strings such as `30s`, `2h`, and `168h` for duration fields. YAML
duration parsing does not accept `7d`; integer `_days` and `_hours` fields take
counts instead. See the [configuration reference](configuration-reference.md) for field scope
and defaults, and [profiling](profiling.md) for collection prerequisites.

## Avoid conflicting mesh routes

Choose a mesh subnet that does not overlap routes on the Colony, agents, or
any workstation participating in the mesh. The default `100.64.0.0/10` can
also be used by other VPNs; compare your actual routes instead of assuming a
private address range is unused.

For a new deployment using a different range, merge both fields into the
Colony configuration:

```yaml
wireguard:
  mesh_network_ipv4: 10.42.0.0/24
  mesh_ipv4: 10.42.0.1
```

Use this example only where that range is available. Coral requires an IPv4
subnet of `/24` or larger. Set the Colony address inside the new subnet; an
existing `mesh_ipv4` value is not automatically recalculated merely because
the subnet changes. Changing an enrolled deployment also affects its assigned
addresses and routes, so plan coordinated network and enrollment changes.
Adding public endpoint addresses is a separate connectivity setting and does
not create additional Colony replicas.

## Verify a configuration change

```bash
coral config view --colony '<colony-id>'
coral config validate
```

`config view` explains context selection and displays Colony settings. It is
not a dump of a running agent's merged settings or every resolved project
override. `config validate` checks saved Colony configurations; inspect its
reported failures, especially in JSON output, rather than using it as an agent
configuration validator. Agent startup reports its own file, environment, and
capability errors.

After an agent change, restart it and check local inventory and fresh telemetry.
After a Colony change, follow [Colony operations](colony-operations.md) to
restart and verify the intended effect. Avoid publishing raw configuration
output: it can contain WireGuard keys and other credentials.

## Connect a remote workstation

The operator HTTPS endpoint is separate from WireGuard and agent enrollment.
The Colony administrator must [enable it](deployment.md#operator-https-access)
and supply an API token and trusted CA information. On the workstation, add a
connection using a CA file obtained from the administrator:

```bash
coral colony add-remote production \
  --endpoint https://colony.example.com:8443 \
  --ca-file ./colony-ca.crt \
  --set-default
export CORAL_API_TOKEN='<operator-api-token>'
coral query summary
```

`production` is a local connection name. Alternatively, use Discovery to fetch
the advertised HTTPS endpoint and verify its CA against a fingerprint obtained
from the Colony owner:

```bash
coral colony add-remote production \
  --from-discovery \
  --colony-id '<actual-colony-id>' \
  --ca-fingerprint 'sha256:<root-ca-fingerprint>' \
  --set-default
```

Endpoint discovery does not issue an API token. Supply it separately through
`CORAL_API_TOKEN`. Use `--discovery-endpoint` for a custom Discovery service.
Avoid `--insecure` outside
an isolated test: it disables the CA verification used by the manual connection
mode.

For a server certificate signed by the Colony CA, its public root certificate
is normally `~/.coral/colonies/<colony-id>/ca/root-ca.crt` on the Colony host.
The administrator can distribute that certificate and independently confirm
the fingerprint with `coral colony ca status`. Distribute the public certificate,
keeping the adjacent CA private keys on the Colony host. If the HTTPS endpoint
uses another certificate authority, supply that authority's CA instead.

Verify query access before configuring an AI client. Some commands use direct
agent APIs and need mesh reachability; HTTPS access to the Colony alone does not
give the workstation a route to every agent. The relevant workflow chapters
identify these requirements.
