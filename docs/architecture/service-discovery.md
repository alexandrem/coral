# Service Discovery and Naming

An agent must identify processes before it can assign their observations to
services. Coral does this in two stages. Process discovery produces candidates
for Beyla instrumentation. The agent's service map records observed and
explicitly connected services, including their current names and monitoring
state. Neither stage is the mesh [Discovery and enrollment](discovery-and-enrollment.md)
protocol that brings an agent into a Colony.

Naming affects more than presentation. Topology materialization excludes calls
between spans with the same normalized service name. If unrelated processes
share a name, their calls can disappear into a single apparent service.

## Process candidates and provider precedence

`ProcessDiscoveryProvider` supplies a name, a capability probe, and a `Discover`
operation returning `ProcessCandidate` records. A candidate can include a PID,
ports, a name, source labels, and a client-only marker or executable pattern.
`DiscoveryManager` periodically polls usable providers, merges their candidates,
and applies changes to the Beyla manager. The configured discovery interval
defaults to 30 seconds.

Static candidates from explicit connections and configured services take
precedence over provider discoveries. They are reapplied on each cycle, so a
process temporarily absent from `/proc` does not erase a pinned declaration.
`EnvVarProvider` supplies names from `OTEL_SERVICE_NAME`, falling back to
`SERVICE_NAME`. `ProcFSProvider` supplies listening ports and process names,
including candidates for processes with no listening socket.

The manager merges observations primarily by PID. This lets an environment name
and a socket-derived port list describe one process. It also merges a static
port candidate whose PID is not yet known with a later provider candidate for
that port. Otherwise the same process could receive conflicting instrumentation
rules merely because it was declared before it started.

Implementation: [provider contract](../../internal/agent/beyla/discovery/discovery.go),
[manager](../../internal/agent/beyla/discovery/manager.go), and
[environment provider](../../internal/agent/beyla/discovery/envvar.go).

## Namespace-aware socket discovery

A host's `/proc/net/tcp` does not list listening sockets in every container's
network namespace. `ProcFSProvider` therefore groups visible PIDs by the target
of `/proc/<pid>/ns/net` and reads socket tables once per namespace. It chooses
a representative PID in ascending order, trying another member when that PID
has exited or its table cannot be read. Failure in one namespace does not
abort discovery of the others.

The provider joins socket tables to per-process file descriptors using
`socketKey{ns, inode}`. For example, two namespace tables can contain the same
inode value; including the namespace prevents the discovery join from
attributing one table's listening port to a process in the other. Namespace and
PID ordering is deterministic so map iteration alone does not trigger a changed
candidate set and an unnecessary Beyla restart.

Unreadable namespace links produce an aggregated capability warning per cycle.
This path requires visibility into the relevant `/proc` entries, but does not
require a Docker socket. It discovers containerized listeners; it does not
supply Compose service names or Kubernetes labels.

Implementation and fixtures: [ProcFS provider](../../internal/agent/beyla/discovery/procfs.go)
and [namespace discovery tests](../../internal/agent/beyla/discovery/procfs_test.go).

## Turning candidates into instrumentation

The Beyla manager generates named discovery rules from the merged candidates.
Listening services can be matched by their ports; client-only or explicitly
portless services use executable matching. Named rules preserve distinct service
attribution rather than grouping all processes behind one catch-all rule.

The broad `open_ports: "1-65535"` rule is used only when monitor-all is enabled
and no candidate has produced a named rule. Mixing that fallback with named
rules can instrument the same process twice, so generation keeps them mutually
exclusive. Candidate changes feed the manager's debounced configuration and
restart path. Collection can therefore lag discovery while reconfiguration is
pending or Beyla is restarting.

Implementation: [Beyla manager](../../internal/agent/beyla/manager.go).
[eBPF instrumentation](ebpf-instrumentation.md) explains process supervision
and the OTLP bridge.

## Default observation and traffic feedback

Agent configuration enables monitor-all by default. `--no-monitor-all` disables
it; the older `--monitor-all` flag is retained as a deprecated compatibility
option. Automatic observation still depends on platform capabilities and
successful Beyla startup, rather than the configuration default alone.

The discovery timer is not the only source of awareness. Beyla's span handler
extracts `server.port`, falling back to `net.host.port`, and notifies the agent
when a port is first observed. The manager deduplicates notifications with an
in-memory observed-port set. This callback runs before the local trace write,
so a service entry proves observation of traffic, not successful persistence
of that particular span.

`onBeylaServiceObserved` resolves process metadata and inserts or updates the
service entry without overwriting an authoritative name. This feedback is
span-driven: metric batches alone do not fire the callback. Different sampling
or protocol coverage can therefore affect when an automatically observed
service becomes visible.

Implementation: [startup storage](../../internal/cli/agent/startup/storage.go),
[Beyla feedback](../../internal/agent/beyla/manager.go), and
[agent service handling](../../internal/agent/agent.go).

## Service identity and monitoring tiers

`Agent.services` is a `map[string]*ServiceEntry`. `serviceKey` uses `port:<N>`
for a port-based entry and `name:<service-name>` for a portless entry. A
`ServiceEntry` has a port or executable pattern, automatic and authoritative
names, process metadata, and an optional `ServiceMonitor`.

This is a different identity boundary from the namespace-aware socket join.
The discovery provider can distinguish namespace/inode pairs, but the agent's
port-based service key does not include a network namespace. Do not infer
namespace-qualified service identities from namespace-aware process scanning.

An observed entry (`TierObserved`) has no active monitor. An explicit connection
pins its authoritative name and creates a watched entry (`TierWatched`) when it
includes a health endpoint or an executable pattern. Portless services use
process-liveness monitoring; a bare port connection with neither condition stays
observed even though its name is authoritative.

For example, an auto-observed process on port 3000 can be promoted in place by
an explicit service connection with a health check. The same entry keeps its
process metadata while acquiring a monitor and an authoritative name. Profiling
callbacks are attached to that monitor; merely seeing traffic is not equivalent
to starting continuous profiling or indexing every observed binary.

Portless connections require an executable pattern instead of a port. The
handler rejects a request containing both or neither. They let a worker that
never binds a socket receive an explicit service identity and liveness check.

Implementation: [ServiceEntry](../../internal/agent/service_entry.go),
[`serviceKey` and promotion](../../internal/agent/agent.go),
[connection validation](../../internal/agent/service_handler.go), and
[service-entry tests](../../internal/agent/service_entry_test.go).

## Name precedence and conflicts

`ServiceEntry.Name()` returns the authoritative name if present, otherwise the
automatic name. Explicit naming therefore takes precedence over subsequent
traffic feedback. Automatic naming uses a `ServiceNameAdaptor` chain that takes
the first successful result.

The built-in process-name adaptor uses the binary's base name. When another
port has already claimed that name, the new port gets a suffix such as
`node-3001`; the earlier entry keeps its bare name. Without binary metadata the
fallback is `port-<N>`. Conflict handling does not retroactively rename all
entries or obtain container labels from an external runtime.

Implementation: [naming adaptors](../../internal/agent/naming/).
For how names affect downstream evidence, see [service topology](service-topology.md).
For how watched services become eligible for symbol indexing, see
[function indexing](function-indexing.md).

## Design history

- [RFD 102: Discovery Providers](../../RFDs/102-pluggable-service-discovery-providers.md)
- [RFD 103: Default-On Observation](../../RFDs/103-default-on-observation.md)
- [RFD 104: Unified Service Map](../../RFDs/104-service-name-adaptor.md)
- [RFD 111: Executable-Pattern Connections](../../RFDs/111-connect-by-exe-pattern.md)
- [RFD 112: Namespace-Aware Discovery](../../RFDs/112-container-network-namespace-discovery.md)
