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
inventory from the operator workstation:

```bash
coral services list --agent '<agent-id>'
```

`--agent` resolves the target through the Colony and then uses the agent API,
so the workstation needs a route to that agent. On the agent host, use local
discovery instead:

```bash
coral services list --local
coral services list --local --source auto
coral services list --local --source watched
```

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

## Find the watched process

`coral exec` and CPU profiling act on a watched service's process. The agent
looks up the process listening on the watched port as soon as the watch is
added, in every network namespace it can see. Check the `PID` column:

```bash
coral services list --local --source watched
```

The PID appears before the application serves any request and changes when
the application restarts. It stays `0` while nothing listens on the port.

### When several containers use the same port

One host often runs several containers that listen on the same internal port,
such as production and staging copies of one image. When one of them shares
the agent's network namespace, as a sidecar does, the agent picks that one.
Otherwise the port alone does not identify the service, so the PID stays `0`
and the agent log warns once:

```text
Several containers listen on port 8080; add process_env or docker_labels to the service, or set OTEL_SERVICE_NAME=api in its container, to identify its process
```

Tell the agent which container is the service in one of three ways:

| Option | The agent compares | Requires |
| --- | --- | --- |
| `process_env` | Variables in the process's environment, such as `APP_ENV` | Nothing beyond the agent's usual process access |
| `docker_labels` | Labels of the Docker container, such as Compose or Swarm service names | Access to the Docker API |
| `OTEL_SERVICE_NAME` | The variable's value with the watch name | Setting the variable in the application's container and redeploying it |

The first two are part of the watch and need no change to the application.

#### Select by environment or Docker labels

1. Find a value that only the service's container has. Docker Compose labels
   each container with `com.docker.compose.project` and
   `com.docker.compose.service`, and Swarm labels each task with
   `com.docker.swarm.service.name`. To list a container's labels:

   ```bash
   docker inspect --format '{{json .Config.Labels}}' '<container-name>'
   ```

2. Add the selectors to the watch. This fragment of the agent configuration
   file watches the production copy of `api` deployed as the Swarm service
   `shop_api`:

   ```yaml
   services:
     - name: api
       port: 8080
       health_endpoint: /health
       docker_labels:
         com.docker.swarm.service.name: shop_api
   ```

   Use `process_env` the same way, with variable names and values. To add the
   watch at runtime instead:

   ```bash
   coral services watch api:8080:/health --docker-label com.docker.swarm.service.name=shop_api
   coral services watch api:8080:/health --process-env APP_ENV=production
   ```

3. Check that the `PID` column of `coral services list --local --source
   watched` is no longer `0`.

A listener must match every entry under `process_env` and `docker_labels`.
Unlike the automatic choice, selectors also apply when only one container
listens on the port: if none matches, the PID stays `0` and the log reports
that no listener matches. Values are compared exactly and are not logged.

`process_env` sees the environment the process started with, which is what
the `environment` section of a Compose or stack file provides. Changes the
application makes to its own environment later are not visible.

`docker_labels` needs the Docker Engine API. The agent uses `DOCKER_HOST` when
it is a `unix://` or `tcp://` address, and `/var/run/docker.sock` otherwise.
An agent running in a container needs the socket mounted, for example
`/var/run/docker.sock:/var/run/docker.sock`. Access to this socket amounts to
root on the host, and mounting it read-only does not restrict the API. The
agent only inspects containers, but use `process_env` where granting that
access is not acceptable. Containers run by other runtimes, such as containerd
under Kubernetes, have no Docker labels.

#### Select by service name

Without selectors, the agent also accepts the one container whose
`OTEL_SERVICE_NAME`, or `SERVICE_NAME` when that is absent, equals the watch
name. Set the variable in the service's container, for example in its
Compose or stack file:

```yaml
services:
  api:
    environment:
      OTEL_SERVICE_NAME: api
```

Redeploy the container, give the other containers on that port different
values or none, and check the `PID` column again.

#### One watched service per port

An agent holds one watched service per port, so the other containers on that
port cannot be watched by the same agent. Watch them from an agent that shares
their network namespace instead.

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
