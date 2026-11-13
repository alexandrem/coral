# Coral User Guide

Coral is an open-source debugger for distributed applications. It helps you
investigate slow requests, application errors, and resource usage across services
running on hosts, in containers, or in Kubernetes. You can inspect what the
system is doing, narrow a problem to a service or process, and collect further
evidence from the running application.

An investigation might begin with an increase in API latency. You can inspect
recent metrics and traces, follow dependencies to another service, then capture
a CPU profile or attach a probe to a specific function. Coral brings these
operations into a command-line workflow, with retained observations for recent
history and on-demand tools for examining current behavior.

You can drive that workflow yourself, combine queries in TypeScript scripts,
or ask an AI assistant to investigate. The built-in `coral ask` client and
external MCP clients give assistants access to Coral's query and debugging
commands. Their tool results provide evidence you can inspect and use to
evaluate a proposed explanation. An LLM is optional for direct use of the tools.

Coral runs on infrastructure you control. A deployment has three main parts:

- **Agents** run near your applications. They discover processes, collect
  observations, and carry out profiling, probing, and remote commands.
- A **Colony** coordinates the agents for an application environment, gathers
  their observations, and answers queries across the deployment. Agents connect
  to it through an encrypted WireGuard network.
- The **CLI** runs on your workstation or an operations host. It connects you,
  your scripts, and your AI clients to the deployment.

Linux agents use eBPF to observe supported application traffic without changing
application code. You can also send application-defined OpenTelemetry spans to
an agent and expose Go runtime metadata for deeper investigations. Available
profiling and debugging features depend on the host's privileges and the
application's runtime and binary metadata. macOS can run the CLI and Colony;
Linux observation agents run inside the host or VM containing the applications.

Start with [installation](installation.md) and [your first Colony](getting-started.md)
to collect and query your first observations. If Coral is already running,
continue with [services and health](services.md) or
[querying telemetry](queries.md), then choose an investigation tool below.

## Install and configure

- [Installation and permissions](installation.md): build and choose binaries,
  prepare hosts, and grant the privileges needed by each component.
- [Your first Colony](getting-started.md): initialize an environment, enroll an
  agent, generate traffic, and verify that data reaches the CLI.
- [Configuration and remote access](configuration.md): select a Colony, locate
  configuration, adjust collection, and connect an operator workstation.
- [Deployment and lifecycle](deployment.md): run on Linux hosts, containers,
  and Kubernetes; configure reachability and preserve state across restarts.
- [Operating a Colony](colony-operations.md): inspect runtime status and agents,
  apply administrative changes, and preserve identity during maintenance.
- [Credentials and access](security.md): provision agents, manage trust and API
  tokens, and rotate enrollment credentials.

## Observe and investigate

- [Services and health](services.md): understand automatic observation, add
  explicit watches, name workers, and interpret inventory health.
- [Querying telemetry](queries.md): move from service summaries to metrics,
  traces, logs, topology, and SQL.
- [CPU and memory profiling](profiling.md): compare retained samples with a new
  capture and interpret coverage and attribution limits.
- [Live function debugging](live-debugging.md): find functions, attach probes,
  filter events, and manage sessions.
- [Host and container commands](remote-execution.md): choose an execution
  context, select the target, and diagnose namespace access.
- [AI assistants](ai-assistants.md): configure `coral ask`, use the terminal,
  and connect an external MCP client.
- [TypeScript investigations](scripting.md): write, run, and share analysis
  scripts using the Coral SDK.
- [Application instrumentation](instrumentation.md): send OpenTelemetry traces
  and expose Go runtime metadata when an investigation needs it.

## Operate and recover

- [Fleet operations](fleet.md): preview selected targets, run bulk diagnostics
  or service watches, and retry partial failures from saved reports.
- [Troubleshooting](troubleshooting.md): distinguish configuration, enrollment,
  mesh, collection, and query failures, then verify recovery.

## Reference and further reading

The [CLI reference](../CLI_REFERENCE.md) is the single man-style command manual:
syntax, arguments, options, defaults, aliases, environment, and files. Keep it
open when adapting a workflow to your own service names and targets. The
[configuration reference](configuration-reference.md) describes YAML fields,
and the [TypeScript SDK reference](sdk-reference.md) describes analysis interfaces.

The [Kubernetes deployment notes](../../deployments/k8s/README.md) explain the
maintained manifest variants. [Runnable examples](../../examples/) supply
application and script examples. The [architecture book](../architecture/README.md)
explains internal mechanisms; [development notes](../development/) and
[RFDs](../../RFDs/) cover contributor work and design proposals. Proposed features,
including [Reef federation](../REEF.md), are not prerequisites for using Coral.
