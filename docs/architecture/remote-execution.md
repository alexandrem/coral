# Remote Execution

Coral runs commands at an agent through two distinct execution contexts. A host
shell runs in the agent process's environment. Container execution invokes
Linux `nsenter` against a detected process, changing the namespaces in which the
command runs. Resolving a service to an agent selects the node that receives
the request; container-process selection is a separate step inside that agent.

## Host commands and interactive sessions

`ShellExec` starts a command with a bounded execution context and captures stdout
and stderr. The handler assigns a UUID session ID, records request metadata,
and returns output, duration, and exit status. On a deadline, it returns a
`DeadlineExceeded` error with response details containing any captured output.
A caller can therefore retain partial evidence when a command times out.

An interactive shell instead uses a pseudo-terminal and an HTTP/2 bidirectional
stream. Input and output are forwarded while the session is active; terminal
resize and signal operations target the tracked session. The handler keeps
sessions in memory and cleans up their resources when the stream ends. This
state is local to the running agent, not a durable resumable terminal log.

Implementation: [shell handler](../../internal/agent/shell_handler.go),
[PTY session](../../internal/sys/shell/shell.go), and
[CLI shell](../../internal/cli/agent/shell.go).

## Container process selection

`coral exec SERVICE ...` resolves the service or an explicit agent ID to an
agent endpoint, then calls `ContainerExec`. The request's `container_name`
field carries the `--container` selector. The handler scans visible numeric
`/proc` entries and excludes its own PID. Selector forms are explicit:

| Selector | Resolution |
|----------|------------|
| Bare value, such as `nginx` | Exact, case-sensitive match against `/proc/<pid>/comm`; hex-looking values remain process names |
| `id:3f1c9a7e2b4d` | Container ID prefix of any non-empty length, matched against 64-hex IDs in `/proc/<pid>/cgroup` |
| `pid:4242` | That positive PID in the agent's PID namespace |
| Empty | Lowest visible positive PID other than the agent in sidecar mode; rejected in the host PID namespace |

Name matches are grouped by mount namespace, and ID matches by container ID.
Matches spanning multiple containers fail as ambiguous; no matches fail as
not found. A unique container yields its lowest matching PID. This lookup
uses procfs rather than runtime APIs: process names are not Docker or
Kubernetes container names, and service resolution does not select a process.

The handler recognizes the host PID namespace when PID 2 is `kthreadd` and
reads host init's mount namespace. In that mode it requires an explicit
selector and rejects processes sharing host init's mount namespace for every
selector form. Unreadable mount namespaces cannot be selected there either.

An empty selector can select PID 1 in a sidecar. For Docker shared-PID
sidecars this is typically the application; for Kubernetes
`shareProcessNamespace` pods it can be `pause`. Use an explicit selector when
the lowest PID is not the intended application.

Implementation: [container handler](../../internal/agent/container_handler.go)
and [CLI exec](../../internal/cli/agent/exec.go).

## Namespace entry and command availability

The handler builds an argument vector for `nsenter -t <pid>`, adds requested
namespace flags, and appends the command after `--`. The default namespace list
is `mnt`; supported additional names are `pid`, `net`, `ipc`, `uts`, and
`cgroup`. Changing the mount namespace changes the filesystem used to locate
the requested executable. Entering a minimal container does not automatically
supply binaries that its filesystem lacks.

Namespace entry requires a visible target process, the `nsenter` executable,
and sufficient privileges for the chosen namespaces. The deployment guidance
uses `CAP_SYS_ADMIN` and `CAP_SYS_PTRACE` with shared PID visibility or host PID
mode. These requirements are separate from the WireGuard privileges needed to
join the network. The current container handler invokes `nsenter`; it does not
implement a CRI execution fallback.

The command inherits the agent environment plus request-supplied overrides.
It can also receive a working directory. The handler defaults to a 30-second
timeout and caps it at 300 seconds, returning output and a negative exit code
with an error message if the deadline expires.

## Audit metadata and access boundaries

Shell and container commands log session IDs, caller-supplied user IDs,
commands, execution duration, and completion status. Container commands also
record the selected PID and namespace list. The CLI defaults the user label
from its local environment; that label is audit metadata, not proof of an
authenticated operating-system identity.

These logs do not constitute durable keystroke recording or session playback.
Access control also depends on the listener and middleware through which the
agent is reached. [Identity and trust](identity-and-trust.md) explains why mesh
encryption and certificate issuance alone do not establish per-command policy.
For usage and deployment requirements, see [Live debugging](../LIVE_DEBUGGING.md),
[Privileges](../PRIVILEGE.md), and [Agent deployment](../AGENT_DEPLOYMENT.md).

## Design history

- [RFD 026: Interactive Shell](../../RFDs/026-shell-command-implementation.md)
- [RFD 044: Agent ID Routing](../../RFDs/044-agent-id-standardization-and-routing.md)
- [RFD 056: Container Execution](../../RFDs/056-container-exec-nsenter.md)
