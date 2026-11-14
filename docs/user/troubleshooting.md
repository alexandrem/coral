# Troubleshooting

Diagnose the failing boundary before resetting state. A working model provider
does not prove Colony access, an issued certificate does not prove mesh
enrollment, and a healthy agent heartbeat does not prove telemetry collection.
Run operator commands with the same context, trust settings, and token as the
failing client.

Start with the symptom closest to the failure:

| Symptom | Procedure |
| --- | --- |
| An agent never joins the Colony | [Enrollment](#an-agent-cannot-enroll) |
| Certificates expired or startup lost its identity | [Certificate and enrollment recovery](#certificate-and-enrollment-recovery) |
| Enrollment works but peers cannot communicate | [Mesh connectivity](#mesh-traffic-fails-after-enrollment) |
| Services or observations are absent or old | [Missing or stale telemetry](#telemetry-is-missing-or-stale) |
| Profiles are empty or functions cannot be found | [Profiles and function metadata](#profiles-or-functions-are-missing) |
| An assistant cannot query the deployment | [AI and MCP access](#ai-or-mcp-cannot-query-the-colony) |

## Establish the current state

```bash
coral version
coral config current-context --verbose
coral config validate
coral status
coral colony agents
coral services list --format json
```

Check the expected Colony ID and endpoint first. `CORAL_COLONY_ENDPOINT`,
`CORAL_COLONY_ID`, and project configuration can override the connection you
thought you selected. A remote connection failure means unreachable or unknown,
not necessarily that the Colony stopped. Read timestamps, provenance, and
fallback warnings before treating a cached response as current.

Inspect server logs in their running terminal or supervisor. For systemd:

```bash
journalctl -u coral-agent -n 100
```

Agent startup can use `CORAL_LOG_LEVEL=debug` for additional diagnostics. Keep
logs containing identities, endpoints, query context, or credentials out of
public issue reports until reviewed.

## An agent cannot enroll

| Symptom | Check | Recovery |
| --- | --- | --- |
| CA fingerprint mismatch | Compare with `coral colony ca status` on the intended Colony host | Obtain the correct fingerprint from its owner; keep verification enabled |
| Colony ID mismatch | Compare the requested ID with the selected environment and certificate identity | Correct the target configuration before retrying |
| Invalid Bootstrap PSK | Check the current provisioning secret and any rotation grace period | Distribute the active PSK securely and retry startup |
| Discovery lookup fails | Check the actual Discovery URL, DNS, and reachability from both hosts | Correct the URL or network path; both peers must use the intended service |
| Certificate exists but startup reenrolls | Check matching enrollment checkpoint, account, and certificate directory | Restore the consistent state or allow fresh enrollment with valid credentials |
| Reverse-dial timeout | Check the agent's advertised TCP endpoint and forwarding rule | Make the temporary listener reachable from the Colony, or supply a corrected override |
| Permission denied writing identity | Check the running account and ownership of its certificate directory | Restore service-account write access without making private keys public |

For reverse dial, verify the external TCP endpoint separately from the
WireGuard UDP mapping. `CORAL_BOOTSTRAP_PUBLIC_ENDPOINT` controls the advertised
address; `CORAL_BOOTSTRAP_LISTEN_PORT` controls the local listener. A successful
UDP STUN result does not establish TCP reachability. The opt-in Discovery probe
can diagnose reachability but is not an enrollment or connectivity guarantee.

## Certificate and enrollment recovery

Inspect certificate state under the same account and path as the service:

```bash
coral-agent cert status --certs-dir /var/lib/coral/.coral/certs
```

Use direct renewal while an existing identity is valid and a TLS endpoint is
reachable. If it has expired, fresh bootstrap needs the current fingerprint
and PSK. Check host time when validity appears inconsistent.

Before a forced reset, stop the agent and back up its certificate directory,
including `enrollment.json`, with private permissions. Keep the certificate,
WireGuard key, and checkpoint together. Do not delete the Colony CA or raw
observation database merely to correct an agent credential error. Startup can
recover incomplete enrollment by performing a fresh compound enrollment;
deleting individual files can create a new mismatch instead of fixing one.

If you intentionally force standalone bootstrap, pass `--force` and the same
`--certs-dir`, then start the service and verify both agent inventory and mesh
connectivity. A successful certificate command alone is insufficient.

## Mesh traffic fails after enrollment

```bash
coral mesh ping '<agent-id>'
coral mesh audit '<agent-id>'
```

These operations run through the Colony and inspect the WireGuard data path.
Ping tests encrypted UDP echo, rather than ICMP or application health. Audit
compares STUN registration with the peer endpoint observed by the Colony:

| Classification | Interpretation |
| --- | --- |
| `direct` | Mapping is preserved or the peer has a direct public endpoint |
| `symmetric` | Port differs by destination; test actual ping before changing it |
| `roaming` | Endpoint learned from incoming peer traffic without a registered STUN endpoint |
| `no_handshake` | No successful handshake evidence; check keys, endpoints, and packet paths |
| `unexpected` | Observed IP differs; investigate multiple NAT layers or changed routing |

A symmetric mapping is a finding, not automatically an RPC failure. Check
packet loss and handshake freshness. A UDP `nc` check alone does not prove a
WireGuard handshake or application connectivity.

Confirm Colony UDP 41580 or its configured replacement, agent outbound/return
traffic, forwarding rules, and mesh-subnet conflicts. Use a public endpoint
override for an intentional load-balancer address; do not advertise a mesh
address as the external endpoint. See [network paths](deployment.md#network-paths).

For fleet ping and audit, read NAT findings from `.results[].data` in the JSON
report. [Fleet operations](fleet.md) explains retries and partial failures.

## Telemetry is missing or stale

First verify a running process and representative traffic. On the agent host,
inspect the local inventory:

```bash
coral services list --local
```

Then compare with Colony inventory and query sources from the operator CLI:

```bash
coral services list
coral query traces api --source ebpf --since 10m
coral query traces api --source telemetry --since 10m
```

If automatic services are absent, inspect process visibility,
`monitor_all`, Beyla startup warnings, and readable `/proc` namespaces. If a
watch is present but unhealthy, confirm its port and health path inside the
application's network namespace. A host-published container port may differ
from the observed application port.

For OTLP, check the application's exporter protocol, service name, receiver
address, and HTTP signal path. Application ports are 4317/4318 by default;
4319/4320 are the internal Beyla receiver. Successful receipt of an arbitrary
metric does not establish its persistence or support in protocol queries.

If local data exists but Colony results are stale, inspect poller logs, mesh
access, and the requested time window. Allow the configured polling interval.
Check retention before expecting an old outage to replay completely. If the
agent reports an unwritable database directory and in-memory fallback, fix the
service home and persistence mounts before expecting restart durability.

## Profiles or functions are missing

Check that the service is watched and resolves to the intended PID. Automatic
traffic observation alone does not start every profiler or index functions.
For functions, verify readable executable metadata, SDK address, and whether
the compiled function still exists after stripping and inlining. First-time
indexing can be asynchronous; retry after the relevant polling cycle.

For CPU samples, check Linux perf/BPF capabilities, host security policy, and
the actual target under load. Inspect the value of
`/proc/sys/kernel/perf_event_paranoid` where applicable; choose host-policy
changes deliberately rather than disabling restrictions as a universal fix.
Unresolved frames and empty stacks are collection problems to investigate,
not proof of idle code.

Heap profiles need a compatible runtime endpoint. Function duration needs
attachable return instructions and adequate metadata; entry-only fallback
produces zero durations. Review [profiling](profiling.md),
[instrumentation](instrumentation.md), and [live debugging](live-debugging.md)
for the relevant prerequisites.

## AI or MCP cannot query the Colony

Reproduce a simple `coral query summary` using the same executable, configuration
directory, and environment as the assistant. Desktop clients may not inherit
your shell's API token or `PATH`; set those in the client's launch configuration.
The MCP proxy needs stdout for protocol messages, so inspect stderr for startup
and connection failures.

If CLI access succeeds, validate the provider with `coral ask config validate`.
Check key source, base URL, and model availability separately from Colony
credentials. An operator API token does not authenticate to an LLM provider.
Use [AI assistants](ai-assistants.md) for setup and the
[CLI reference](../CLI_REFERENCE.md) for actual command syntax.

For a report to maintainers, include Coral version, platform, deployment mode,
the command and sanitized error, relevant timestamps, and which checks above
succeeded. Remove private keys, PSKs, tokens, and sensitive application data.
