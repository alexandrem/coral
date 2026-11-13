# Services and Health

An agent can observe traffic from a process without an explicit service watch.
Watching adds an operator-chosen identity, process monitoring, and an optional
health check. These states supply different evidence: traffic shows activity;
an agent heartbeat shows liveness; a configured check tests the selected
application endpoint.

## Inspect the right inventory

From an operator CLI connected to the Colony:

```bash
coral colony agents
coral services list
coral services list --name api --wide
```

The Colony combines registered services and recently observed telemetry. The
default telemetry discovery window is one hour; `--since` changes that window
while retaining registered entries. To inspect a specific agent's current
inventory instead:

```bash
coral services list --agent '<agent-id>'
coral services list --local
coral services list --local --source auto
coral services list --local --source watched
```

Run `--local` on the agent host. `--agent` resolves the target through the
Colony and then uses the agent API, so the CLI needs a route to that agent.
`--since` is rejected for local and single-agent inventories.

## Add an explicit watch

Run on the agent host to register an application listening on port 8080:

```bash
coral services watch api:8080:/health:http
coral services list --local --source watched
```

The service specification is `name:port[:health][:type]`. Omit the health path
when the application does not provide one. You can watch several services in
one operation, or select a remote agent:

```bash
coral services watch api:8080:/health redis:6379
coral services watch api:8080:/health --agent '<agent-id>' --colony '<colony-id>'
```

The agent must already be running. Registration adds the watch dynamically and
can trigger a managed Beyla restart; it does not restart the application. Do
not treat an existing conflicting registration as an update. Verify the
resulting inventory and check configuration, especially when using automation.
Use agent YAML `services` entries for reproducible startup configuration.

`coral connect` remains a permanent compatibility alias for
`coral services watch`. New procedures use the canonical spelling. Fleet
registration, previews, and conflict handling are explained in
[fleet operations](fleet.md).

## Watch a worker without a port

A queue consumer or batch process can use an executable-pattern watch:

```bash
coral services watch worker --exe-pattern 'python.*consumer.py'
```

Use the single-name form with either `--port` or `--exe-pattern`, never both.
The pattern identifies a process by its executable or invocation. Choose a
pattern specific enough to identify the intended workload; matching another
Python or Java process would attach the wrong service identity. Inspect its
PID and process status in the agent inventory before profiling or debugging.
This watch does not create a listening port or imply HTTP health checking.

## Control automatic naming and observation

Automatic observation is on by default. Explicit service names take priority
over inferred names. Environment-based naming uses `OTEL_SERVICE_NAME`, then
`SERVICE_NAME`, with executable naming as a fallback. Set distinct application
names when several instances would otherwise all appear as `python` or `node`.

A host agent can scan listener tables in visible process network namespaces.
For Compose containers, the reported port is the application's internal port,
not the host-published port. Discovery depends on readable `/proc` descriptors
and namespaces; `hidepid` or container isolation can restrict it. Compose
service names are not inferred automatically from runtime metadata.

To observe only explicit targets, set `agent.monitor_all: false` or start with
`--no-monitor-all`. The old `--monitor-all` flag is accepted as a deprecated
no-op because automatic observation is now the default. See
[configuration](configuration.md#configure-an-agent) for collection controls.

## Read health and freshness separately

| Output | What it tells you |
| --- | --- |
| `HEARTBEAT` / `LAST HEARTBEAT` | Agent liveness, independently of service checks |
| `HEALTH CHECK` / `CHECKS` | Results of explicitly configured checks; `not-configured` means there is no monitor |
| `LAST CHECK` | Age of the oldest result contributing to the check summary |
| `LAST SEEN` | Registry heartbeat or telemetry activity, depending on source |
| `SERVICE DATA` / `DATA` | Live, cached, mixed, telemetry-derived, or unknown provenance |

Cached snapshots preserve their original timestamps. A failed query or an old
snapshot does not establish that a service is healthy. Inspect JSON output for
check counts, provenance, and fallback warnings:

```bash
coral services list --format json
coral colony agents --format json
```

An observed-only service can have useful RED metrics while lacking the monitor
needed by continuous profiling or lazy function indexing. Add a watch when an
investigation needs those paths. For protocol evidence continue with
[queries](queries.md); for missing services use [troubleshooting](troubleshooting.md).
