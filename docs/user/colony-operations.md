# Operating a Colony

A Colony coordinates one application environment: it admits agents, tracks
their connectivity, collects observations, and serves investigation requests.
Operate separate Colonies for environments that need separate identities,
credentials, or retained data. Selecting a different Colony in your CLI does
not change where an existing agent reports.

Follow [your first Colony](getting-started.md) for initialization and agent
enrollment. This chapter covers inspecting and maintaining a deployment after
it is running. Use [deployment](deployment.md) for host preparation, network
paths, and supervisor setup.

## Choose where to run commands

Use the full `coral` CLI on an operator workstation. The `coral-colony` server
binary also includes tools for investigation from the Colony host. Its
administration commands omit the `colony` prefix, while investigation groups
keep their names:

```bash
# On an operator workstation with the full CLI.
coral colony status --colony '<colony-id>'
coral query summary api --since 10m

# On the Colony host with the server binary.
coral-colony status --colony '<colony-id>'
coral-colony query summary api --since 10m
```

Status and query commands contact a server. CA, PSK, export, and token-management
commands instead need access to the Colony host's local configuration and state.
A remote connection profile and an API token do not supply that local state.
Run administration under the account and paths used by the server. Stop the
server before a local database operation if it encounters a writer lock.

Client context commands such as `config use-context` and `colony add-remote`
belong to the full CLI. The single [CLI manual](../CLI_REFERENCE.md) documents
syntax and the supported server-binary command mappings.

## Check the environment, then the Colony

```bash
coral config current-context --verbose
coral status
coral colony status --colony '<colony-id>' --detail
```

`coral status` surveys configured Colonies and Discovery reachability.
`coral colony status` inspects one Colony's configuration and attempts to fetch
runtime information such as uptime, registered agents, and live network
endpoints. A result of `configured` means configuration was found but runtime
status could not be obtained. It does not prove a running server or establish
that the server stopped.

For connectivity diagnosis, compare the running endpoint with your intended
public HTTPS URL and WireGuard UDP endpoint. The configured mesh address exists
inside the tunnel; advertising it externally will not establish that tunnel.
Use [remote access](configuration.md#connect-a-remote-workstation) to correct an
operator connection, or [network paths](deployment.md#network-paths) to correct
agent reachability.

## Inspect agents and service coverage

```bash
coral colony agents --colony '<colony-id>' --verbose
coral colony agents --colony '<colony-id>' --status degraded,unhealthy
coral services list
coral query summary
```

Agent inventory includes registrations with stale heartbeats. Compare heartbeat
age, runtime details, and live versus cached service data before deciding which
host needs attention. Service checks and recent telemetry are separate from
agent liveness: an agent can be connected while its application is unhealthy
or its collector is unable to observe traffic.

Use [services and health](services.md) to interpret those fields. Use
[fleet operations](fleet.md) to select affected agents, preview diagnostics,
and save partial results for retry. An unavailable agent can leave older
observations in the Colony; check the time range before treating those as new
activity.

## Provision access and apply changes

New agents use the enrollment bundle produced by `coral colony export`.
Operators use public API tokens after HTTPS access has been configured.
Exported enrollment credentials are not a backup, and the legacy `colony import`
command does not restore the current bundle. See
[credentials and access](security.md) for provisioning and rotation procedures.

After changing the token file, send SIGHUP to the running Colony process or
restart it, then verify the intended token can query the server. SIGHUP reloads
the token store; it does not apply arbitrary network, TLS, or collector changes.
For those settings, plan a controlled restart under the existing supervisor.

## Preserve identity and data during maintenance

Before stopping a Colony, record its ID, service account, configuration base,
working directory, and resolved storage path. Keep a consistent backup of:

- Its configuration and WireGuard identity.
- Its `ca/` directory, including private keys and provisioning material.
- Its token file, including a custom `tokens_file` path if configured.
- Its storage directory, including the Colony database and associated files.

The main observation database is named `<colony-id>.duckdb` under the resolved
storage directory. That directory can differ from the configuration directory,
and project settings can override the stored `storage_path`. Stop the process
before copying its state for a consistent filesystem backup. Preserve private
permissions and restore the same account's access to the restored files.

Restart with the same identity and verify runtime status, mesh connectivity,
agent heartbeats, and newly arriving observations. Reinitializing with `init`
creates a new identity; use it when creating an environment, rather than as a
repair step for an existing Colony. Database schema downgrade and concurrent
writers to one state directory are not supported maintenance procedures.

Agent-local retention bounds how much can be recovered after an outage. Keep
the agent running and its data directory persistent when the Colony is down,
then check [freshness and missing data](queries.md#freshness-and-missing-data)
after reconnection. Recovery does not reproduce data that has already expired.

Stop foreground processes with Ctrl+C or use their supervisor. The registered
`colony stop` command is a placeholder, and `--daemon` is not a complete
process-management service. For a failed restart, use
[troubleshooting](troubleshooting.md) before replacing credentials or data.
