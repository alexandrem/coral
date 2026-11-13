# Your First Colony

This walkthrough creates one Colony, enrolls one agent, and queries observations
from a running application. Install the binaries and prepare their
[permissions](installation.md) first. Use a Linux observation host for the eBPF
steps. The Colony and agent may run on separate hosts; they need access to the
same Discovery service and a usable network path to each other.

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
PSK authorizes new agents and belongs in secret storage.

The default Discovery service comes from Coral's configuration. If you operate
a different service, pass `--discovery https://discovery.example.com` to `init`
and configure that same URL on the agent. You do not need to start a separate
Discovery process for the default setup.

## Export agent enrollment credentials

On the Colony host, export the settings an agent needs to join:

```bash
umask 077
coral colony export --format env > "$HOME/.coral/agent-enrollment.env"
```

The generated shell file exports `CORAL_COLONY_ID`, `CORAL_CA_FINGERPRINT`,
`CORAL_BOOTSTRAP_PSK`, and `CORAL_DISCOVERY_ENDPOINT` for the selected Colony.
This also carries your configured Discovery URL, avoiding manual transcription
of the values printed by `init`. To export another Colony, pass its ID as a
positional argument: `coral colony export <colony-id> --format env`.

Export before starting the Colony: this command opens local Colony state and
can encounter a database lock while the server is running. The file contains
the enrollment PSK. Keep it private, transfer it to a remote agent host through
your secret-provisioning channel, and keep it out of source control. See
[credentials and access](security.md#provision-an-agent) for other export formats.

## Start the Colony

In the first terminal, from the same project directory, run:

```bash
sudo env CORAL_CONFIG="$HOME" coral colony start
```

This explicitly selects the configuration created by your user even when `sudo`
changes the environment. If `coral` is outside sudo's `PATH`, use its absolute
installed path. Keep this process in the foreground. Startup must report a
running Colony and Discovery registration; a successful `init` alone does not
start any service.

For remote agents, check [deployment reachability](deployment.md#network-paths)
before continuing. `CORAL_PUBLIC_ENDPOINT` overrides the advertised WireGuard
UDP endpoint when a load balancer or forwarding rule requires it.

## Enroll and start an agent

On the observation host, load the exported credentials in a second terminal.
For a remote host, use the path where you securely placed the file instead of
the original path below:

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
enrolls the mesh peer. Keep the process running. A certificate without completed
mesh enrollment is not a
usable connection; check both startup logs and the Colony's agent inventory.

Automatic process observation is enabled by default. You can opt out with
`--no-monitor-all` and add only [explicit watches](services.md). Initial
collection still depends on the agent's privileges and the processes it can see.

## Produce and inspect evidence

Run your application on the observation host and send requests through it. On
the Colony host, use a third terminal with the full CLI:

```bash
coral colony agents
coral services list
coral query summary
```

The agent should appear in the inventory. Services can take a discovery and
polling cycle to appear, and protocol metrics need application traffic. Empty
results do not establish that an application is healthy. Check collection logs
and [troubleshooting](troubleshooting.md) if traffic never appears.

To give a local application on port 8080 an explicit name and an HTTP health
check, run on the agent host:

```bash
coral services watch api:8080:/health
coral services list --local
```

Use a real health path for your application, or omit `:/health`. From the
Colony host, inspect that service:

```bash
coral query metrics api --since 10m
coral query traces api --since 10m
```

See [services and health](services.md) to distinguish a heartbeat, an explicit
health check, and observed telemetry. A worker without a listening port can be
watched by executable pattern.

## Stop and continue

Press Ctrl+C in each foreground server terminal to stop it. Use your process
supervisor to stop deployed services. `coral colony stop` is currently a
placeholder, and there is no `coral agent stop` command. Keep the generated
configuration, CA, agent identity, and data if you plan to restart this setup.

Continue with [querying telemetry](queries.md),
[profiling](profiling.md), or [AI assistants](ai-assistants.md). An operator on
another workstation should first configure [remote access](configuration.md#connect-a-remote-workstation).
