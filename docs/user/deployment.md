# Deployment and Lifecycle

Deploy one Colony for an application environment and agents where its processes
are visible. Keep the operator CLI separate from the server account when using
supervised services. Complete [initialization](getting-started.md) before
installing an agent, and obtain its enrollment credentials through a trusted
channel.

## Network paths

A WireGuard **endpoint** is the host and UDP port used to establish a tunnel.
A **mesh address** exists inside that tunnel. Coral defaults to Colony mesh
addresses `100.64.0.1` and `fd42::1`; neither address is a substitute for a
reachable public endpoint.

| Path | Default | Deployment requirement |
| --- | --- | --- |
| Colony WireGuard | UDP 41580 | Reachable directly or through a reusable NAT mapping |
| Agent WireGuard | UDP 51820 | Allow outbound traffic and return packets; account for NAT |
| Colony internal API | TCP 9000 | Keep within the intended local/mesh access boundary |
| Agent API | TCP 9001 | Local/mesh access for agent operations |
| Agent mesh ping | UDP 9003 | Used inside WireGuard for diagnostics |
| Operator HTTPS API | TCP 8443 when enabled | Expose deliberately with TLS and API authentication |
| Agent reverse-dial bootstrap | TCP 8444 during fallback | Colony must be able to dial this agent endpoint |
| Application OTLP | TCP 4317 / 4318 | Allow only the applications intended to submit telemetry |

Both components use Discovery and STUN to find endpoints. The Colony publishes
its STUN-observed UDP mapping automatically. Set `CORAL_PUBLIC_ENDPOINT` or
`wireguard.public_endpoints` when DNS, load-balancing, or port forwarding requires
a different advertised address:

```bash
CORAL_PUBLIC_ENDPOINT=colony.example.com:41580 coral-colony start
```

Run with the [required privileges](installation.md#grant-server-privileges).
Forward UDP to the actual Colony listener, preserving the advertised port or
specifying the rewritten port. STUN observation is not a guarantee that every
remote peer can reuse a NAT mapping. Inspect both logs and
`coral mesh ping` after enrollment.

If the agent cannot reach the Colony's bootstrap TLS endpoint, startup can
publish an encrypted rendezvous request so the Colony dials the agent instead.
The default inferred address combines the agent's STUN-observed IP with TCP
8444. For a different forwarding rule, configure:

```bash
export CORAL_BOOTSTRAP_PUBLIC_ENDPOINT=agent.example.com:18444
export CORAL_BOOTSTRAP_LISTEN_PORT=8444
```

This example requires external TCP 18444 to forward to local TCP 8444. The
temporary listener is for bootstrap, separate from WireGuard and OTLP. The
standalone `agent bootstrap` command needs an explicit public endpoint for this
path because it does not perform startup's network discovery. Optional
`--verify-bootstrap-reachability` requests a quota-limited Discovery diagnostic;
it does not make an unreachable host reachable. Reverse dial also requires an
inbound TCP path to one peer; it cannot connect two hosts with no inbound path.

## Use your own Discovery service

Discovery supplies endpoint coordinates and enrollment referral information.
It is a separate service from the Colony and is not an observation database.
For an existing installation, set its URL in the Colony account's global
configuration:

```yaml
discovery:
  endpoint: https://discovery.example.com
  stun_servers:
    - stun.cloudflare.com:3478
```

For a new Colony, `coral init shop --discovery https://discovery.example.com`
sets that global URL during initialization. Configure agents with the same
`CORAL_DISCOVERY_ENDPOINT`, or regenerate their enrollment bundle after the
change. Restart the affected processes and verify registration and enrollment
through the intended service.

The Discovery server implementation is maintained separately; this checkout
does not build a `coral-discovery` binary. Its
[service implementation notes](../DISCOVERY-WORKERS.md) identify the separate
project and contributor setup. Hosting that server requires its own deployment
configuration and signing credentials. Pointing Coral at a new URL does not
deploy it or replace the Colony fingerprint and Bootstrap PSK checks.

## Linux hosts with systemd

Use a locally built `coral-agent` or a binary from a release you have selected.
The [installer](../../scripts/install-agent.sh) can copy a local binary with
`--binary-path` and prepare accounts and directories. Its `--skip-service`
option lets you prepare the configuration before enabling the service:

```bash
sudo ./scripts/install-agent.sh \
  --binary-path ./bin/linux_amd64/coral-agent \
  --skip-service
```

Choose the build path matching the target machine. This command still installs
dependencies and copies files; it does not configure an enrolled running agent.
The installer has a legacy Discovery default, so pass your actual
`--discovery` URL when using its bootstrap options. Configure service startup
explicitly rather than relying on a certificate created under root's home.

Create `/etc/coral/agent.yaml`, owned by the service account with permissions
that protect the PSK. Replace the following placeholders:

```yaml
agent:
  colony:
    id: '<colony-id>'
  bootstrap:
    ca_fingerprint: 'sha256:<root-ca-fingerprint>'
    bootstrap_psk: 'coral-psk:<bootstrap-key>'
    certs_dir: /var/lib/coral/.coral/certs
```

Use the [supplied systemd unit](../../deployments/systemd/coral-agent.service).
Its `coral` account uses `/var/lib/coral` as home; make that directory writable
so `~/.coral/agent/metrics.duckdb` persists. Add an override if you use a custom
Discovery service:

```ini
[Service]
Environment=CORAL_DISCOVERY_ENDPOINT=https://discovery.example.com
```

After copying the unit to `/etc/systemd/system/coral-agent.service` and writing
any override, start and verify it:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now coral-agent
sudo systemctl status coral-agent
journalctl -u coral-agent -f
```

From a configured operator CLI, run `coral colony agents`, then
`coral services list`. `coral-agent status` is also available locally. Use
`systemctl stop coral-agent` to stop the supervised agent.

For the Colony, run `coral-colony start` under your supervisor with an explicit
configuration directory, working directory, service account, and network
privileges. Preserve the initialized CA directory and resolved storage path.
The CLI's `--daemon` option does not provide a complete supervisor or a working
PID-based stop operation.

## Containers and Kubernetes

The repository provides [Docker build targets](../../Makefile) and
[Dockerfiles](../../build/). Build the agent image with `make docker-build-agent`
and select a fixed image tag for deployment. Mount persistent state for the
actual container user's home and certificate directory, provide enrollment
secrets, expose `/dev/net/tun`, and grant the capabilities required by the
features you enable. Host observation also needs host PID visibility and
readable process namespaces; host networking alone does not provide that.

For Kubernetes, start with the [manifest selection guide](../../deployments/k8s/README.md).
It owns the full sidecar and DaemonSet variants, volume mounts, security
contexts, and deployment commands. A restricted sidecar has different
capabilities from a privileged host agent. Use shared PID visibility and an
explicit application selector for [container execution](remote-execution.md).
Exporting enrollment credentials in `k8s` format produces a Secret manifest;
handle it as a secret rather than committing it with deployment configuration.

A privileged host agent can discover ordinary Compose container listeners
through procfs without a Docker socket. It reports the namespace-local port:
for `9090:8080`, use 8080 when referring to the observed listener. It does not
automatically derive a Compose service name; use application environment naming
or an explicit watch. Separate container PID namespaces do not hide those
processes from the host; a containerized agent needs visibility into host PIDs
to perform equivalent discovery. See [services](services.md).

## Operator HTTPS access

Enable the optional endpoint in the Colony configuration to serve operators
outside the mesh. This fragment uses an explicitly supplied certificate and
requires tokens:

```yaml
public_endpoint:
  enabled: true
  host: 0.0.0.0
  port: 8443
  tls:
    cert: /etc/coral/colony-server.crt
    key: /etc/coral/colony-server.key
  auth:
    require: true
  discovery:
    advertise_url: https://colony.example.com:8443
```

Use a certificate valid for the advertised hostname and distribute its trusted
CA to clients. Restart the Colony, create an [operator token](security.md#operator-api-tokens),
then configure [remote workstation access](configuration.md#connect-a-remote-workstation).
Keep internal RPC and application OTLP listeners within their intended network
boundary; the public API's token checks do not apply automatically to them.

## Restarts, upgrades, and retention

Preserve Colony identity, CA keys, token storage, and its DuckDB data together.
On agents, preserve certificate state and the enrollment checkpoint, including
the WireGuard identity; keeping the certificate alone is insufficient for
startup to reuse an enrollment. Run only one writer for a given local database.

Before an upgrade, stop the relevant process, back up its state consistently,
replace the binary, verify privileges, and restart under the same account and
paths. Validate agent inventory, mesh connectivity, and fresh telemetry after
restart. The guide does not promise automatic schema downgrade or restoration
from an arbitrary live copy of DuckDB files.

Agent raw-data retention and Colony summary retention are separate and differ
by stream. Configure them in the [configuration reference](configuration-reference.md) and
plan disk usage for your workload. An outage longer than local retention can
leave permanent gaps. See [query interpretation](queries.md#freshness-and-missing-data)
and [fleet operations](fleet.md) for routine checks across agents.
