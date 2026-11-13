# Host and Container Commands

Choose the execution context before running a command. `coral shell` executes
in the agent's environment. `coral exec` enters namespaces of a selected
container process through Linux `nsenter`. Resolving a service to an agent
chooses the node; it does not itself select the container process.

These operations run with the agent's privileges. Use a configured Colony and
an agent reachable from the CLI for direct agent operations. A connection to
the Colony's public HTTPS endpoint alone does not create workstation mesh
routes. See [deployment](deployment.md) for process visibility and capabilities.

## Inspect the agent environment

```bash
coral shell --agent '<agent-id>' -- ps aux
coral shell --agent '<agent-id>'
```

The second form starts an interactive shell. Without an explicit target, the
CLI attempts local-agent discovery. Use `--agent-addr` only when you intend to
connect to an explicit address instead of resolving the agent through the
Colony. Use `exit` to close an interactive session.

The available commands and files belong to the agent's host or container.
Coral does not supply every diagnostic executable mentioned in examples. For
pipelines, invoke a shell explicitly:

```bash
coral shell --agent '<agent-id>' -- sh -c 'ps aux | head'
```

Quoting here makes the pipeline execute in the remote shell. The local shell
still processes unquoted redirections and substitutions, so check where an
output file would be written before running a complex command.

## Select a container process

```bash
coral exec api --container nginx -- cat /etc/nginx/nginx.conf
coral exec api --container id:3f1c9a7e2b4d -- ls /app
coral exec api --container pid:4242 -- ls /app
```

Replace the service, selector, and file paths with values from your deployment.
Selectors are explicit:

| Selector | Meaning |
| --- | --- |
| `nginx` | Exact case-sensitive process name from `/proc/<pid>/comm`, not a Docker or Kubernetes container name |
| `id:<prefix>` | Container ID prefix found in the process's cgroup |
| `pid:<number>` | Positive PID as seen by the agent |
| Omitted | Lowest visible PID other than the agent in sidecar mode; refused in host PID mode |

Name and ID matches must identify one container. A collision fails instead of
choosing arbitrarily; use a longer ID prefix or an explicit PID. Processes in
the host init mount namespace are refused. In shared-PID Kubernetes pods the
lowest PID can be `pause`, so select the application explicitly.

The default namespace is `mnt`, which changes the filesystem used by the
command. Additional supported namespaces are `pid`, `net`, `ipc`, `uts`, and
`cgroup`. Entering the container does not install missing tools in its image.

## Set command options deliberately

```bash
coral exec api --container pid:4242 --working-dir /app -- ls -la
coral exec api --container pid:4242 --env DEBUG=true -- env
coral exec api --container pid:4242 --namespaces mnt,net -- ip addr
```

The agent must have `nsenter`, a visible target, and sufficient privileges
for the chosen namespaces. The command inherits the agent environment plus
explicit overrides; it does not automatically reproduce the application's
original environment. Container execution defaults to a 30-second timeout and
caps it at 300 seconds. A timeout can return partial output with an error.

Session logs include command, duration, status, and caller-supplied user label.
`--user-id` changes that audit label, not the operating-system execution user.
These logs are not durable keystroke playback. Access boundaries are described
in [credentials and access](security.md#understand-the-access-boundary).

If selection fails, inspect the agent-visible process name, PID, and cgroup.
If `nsenter` fails, check capabilities, PID sharing, and host security policy.
Use the [CLI reference](../CLI_REFERENCE.md) for all targeting and timeout
options, and [remote execution architecture](../architecture/remote-execution.md)
for exact selection behavior.
