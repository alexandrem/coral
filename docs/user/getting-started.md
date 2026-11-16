# Your First Colony

This walkthrough creates one Colony, enrolls one agent, and queries observations
from a running application. At the end, `coral colony agents` lists your agent
and `coral query summary` returns data for your application.

You need three terminals and these prerequisites:

- Coral binaries and [permissions](installation.md) from the installation guide.
- A Linux observation host for the eBPF steps. The Colony can run on the same
  host or on another one that the agent can reach. Both need access to the same
  Discovery service.
- An application listening on a local port, such as `api` on 8080.

| Terminal | Host | Purpose |
| --- | --- | --- |
| 1 | Colony host | Runs the Colony in the foreground |
| 2 | Observation host | Runs the agent in the foreground |
| 3 | Colony host | Runs CLI queries |

## Initialize the environment

On the Colony host, in a directory where Coral may store its local data, run:

```bash
coral init shop --env dev
coral config current-context --verbose
```

`init` generates a Colony ID such as `shop-dev-<suffix>`, WireGuard keys, a
certificate authority, and a Bootstrap PSK. It writes the Colony configuration
under `~/.coral/colonies/<colony-id>/config.yaml` and a project selection in
`.coral/config.yaml`. The printed fingerprint identifies the Colony's CA; the
PSK authorizes new agents and belongs in secret storage. `current-context`
should report the new Colony as selected by the project file.

The default Discovery service needs no separate process. If you operate your own,
pass `--discovery https://discovery.example.com` to `init` and use the same URL
on the agent.

## Export agent enrollment credentials

Still on the Colony host, export the settings an agent needs to join:

```bash
umask 077
coral colony export --format env > "$HOME/.coral/agent-enrollment.env"
```

The file exports `CORAL_COLONY_ID`, `CORAL_CA_FINGERPRINT`,
`CORAL_BOOTSTRAP_PSK`, and `CORAL_DISCOVERY_ENDPOINT` for the selected Colony.
It contains the enrollment PSK, so keep it private and out of source control.
For a remote agent host, transfer it through your secret-provisioning channel.

Export before starting the Colony, because the command opens local Colony state
and can hit a database lock while the server runs. To export another Colony, pass
its ID: `coral colony export <colony-id> --format env`. See
[credentials and access](security.md#provision-an-agent) for other export formats.

## Start the Colony

In terminal 1, from the same project directory, run:

```bash
sudo env CORAL_CONFIG="$HOME" coral colony start
```

Keep this process running. Startup should report a running Colony and its
Discovery registration; a successful `init` alone starts nothing. The `sudo env`
form and its alternatives are explained in [privileged startup](#privileged-startup).

If the agent runs on another host, check [deployment reachability](deployment.md#network-paths)
before continuing, and see [advertised endpoint](#advertised-endpoint) if a load
balancer or port forward is involved.

## Enroll and start an agent

In terminal 2 on the observation host, load the exported credentials and start
the agent. For a remote host, use the path where you placed the file.

```bash
source "$HOME/.coral/agent-enrollment.env"

sudo env \
  CORAL_CONFIG="$HOME" \
  CORAL_COLONY_ID="$CORAL_COLONY_ID" \
  CORAL_CA_FINGERPRINT="$CORAL_CA_FINGERPRINT" \
  CORAL_BOOTSTRAP_PSK="$CORAL_BOOTSTRAP_PSK" \
  CORAL_DISCOVERY_ENDPOINT="$CORAL_DISCOVERY_ENDPOINT" \
  coral agent start
```

Startup verifies the Colony's identity, obtains an agent certificate, and
enrolls the mesh peer. Keep the process running. Enrollment is complete only
when both succeed, so check the startup logs and the Colony's agent inventory
in the next step.

The agent observes all processes it can see by default. To limit it to
[explicit watches](services.md), start it with `--no-monitor-all`.

## Produce and inspect evidence

Send requests through your application on the observation host. In terminal 3 on
the Colony host, run:

```bash
coral colony agents
coral services list
coral query summary
```

The agent should appear in the inventory. Services can take a discovery and
polling cycle to appear, and protocol metrics need application traffic. An empty
result does not show the application is healthy; if traffic never appears, check
the collection logs and [troubleshooting](troubleshooting.md).

To give the application an explicit name and an HTTP health check, run on the
agent host:

```bash
coral services watch api:8080:/health
coral services list --local
```

Use a real health path for your application, or omit `:/health`. Then, from the
Colony host, inspect that service:

```bash
coral query metrics api --since 10m
coral query traces api --since 10m
```

[Services and health](services.md) explains how a heartbeat, a health check, and
observed telemetry differ, and how to watch a worker with no listening port.

## Stop and continue

Press Ctrl+C in each foreground terminal to stop it, or use your process
supervisor for deployed services. Keep the generated configuration, CA, agent
identity, and data if you plan to restart. `coral colony stop` is currently a
placeholder, and there is no `coral agent stop` command.

Continue with [querying telemetry](queries.md), [profiling](profiling.md), or
[AI assistants](ai-assistants.md). An operator on another workstation should
first configure [remote access](configuration.md#connect-a-remote-workstation).

## Notes

### Privileged startup

The Colony and agent need elevated privileges for networking and eBPF, but `sudo`
changes the environment, so Coral would not find the configuration you created as
your own user. `CORAL_CONFIG="$HOME"` points it at that configuration. If `coral`
is outside sudo's `PATH`, use its absolute installed path. For supervised
deployments, see [deployment](deployment.md).

### Advertised endpoint

For remote agents, `CORAL_PUBLIC_ENDPOINT` overrides the WireGuard UDP endpoint
the Colony advertises. Set it when a load balancer or port forward sits between
the agent and the Colony.
