# Fleet Operations

Use fleet operations to check mesh connectivity or add the same service watches
across several agents. Select the agents, preview the operation, then save a
report so you can inspect failures and retry the affected targets. Each agent
receives its own result; a failure on one agent does not stop the others.

## Prepare access to the Colony and agents

Run these commands from an operator workstation or operations host with the
Coral CLI and a configured connection to a running Colony. Complete
[your first Colony](getting-started.md) if you have not enrolled agents, or
[configure remote access](configuration.md#connect-a-remote-workstation) for an
existing deployment. Remote HTTPS access requires the Colony's API token and
trusted CA information.

Mesh ping and audit run through the Colony. Fleet service watches contact the
agents directly, so the CLI host must also have a route to their mesh addresses
and access to the agent API on TCP port 9001. HTTPS access to the Colony alone
does not provide that route.

Replace `<colony-id>` with your Colony ID throughout this guide. The examples
select agents already associated with a service named `payments` and running
in Docker; adapt those filters to your inventory.

## Select and preview the agents

1. Inspect the agents you intend to operate on:

   ```sh
   coral colony agents --colony '<colony-id>' --service payments --runtime docker
   ```

   Both filters must match: each selected agent has a service named `payments`
   and the `docker` runtime. To include several service names, use a
   comma-separated value such as `--service payments,api`; an agent matching
   either name qualifies. Names and versions match exactly. The same selectors
   work for agent inventory, mesh diagnostics, and fleet watches.

   Use `--status degraded,unhealthy` when investigating missed agent heartbeats.
   Heartbeat status describes agent liveness independently of service health
   checks. See [services and health](services.md#read-health-and-freshness-separately)
   to interpret the inventory.

2. Preview the operation with the same filters:

   ```sh
   coral mesh ping --colony '<colony-id>' \
     --service payments --runtime docker --dry-run
   ```

   Check the Colony ID, target IDs, and parameters in the output. Registered
   targets appear as `planned`, with zero attempts. A dry run reads inventory
   without sending pings, auditing endpoints, or registering services. An empty
   filtered inventory means you should check the names and filters; a filtered
   bulk operation fails when nothing matches.

Each invocation fixes its target IDs before execution. A later command,
including the command after a preview, resolves the filters again. Use
`--agents '<agent-id-1>,<agent-id-2>'` when you need to select exact identities.
An explicitly selected ID missing from the registry is reported as a failed
target.

For a whole-Colony operation, replace the filters with `--all`. It cannot be
combined with filters. Mesh ping and audit also target all registered agents
when you omit selectors; fleet watches require an explicit selection or
`--all`.

## Check mesh connectivity

After reviewing the preview, run ping and save its results:

```sh
coral mesh ping --colony '<colony-id>' \
  --service payments --runtime docker --report ping.json
```

Read each target's status, packet loss, average round-trip time, and error. Ping
tests encrypted UDP echo through the mesh. A target fails if it receives no
reply, packet loss exceeds 50%, or the request fails. A successful ping confirms
mesh reachability for that test; inspect service health separately.

If connectivity fails or behaves inconsistently, audit the same selection:

```sh
coral mesh audit --colony '<colony-id>' \
  --service payments --runtime docker --report audit.json
```

Compare handshake freshness and the endpoints reported by the agent and
observed by the Colony. NAT classifications such as `symmetric` are findings;
an audit can succeed while reporting them. Request failures and an inability
to assess an agent fail that target. Use the
[mesh troubleshooting procedure](troubleshooting.md#mesh-traffic-fails-after-enrollment)
to interpret classifications and check keys, routes, and UDP reachability.

To allow retries for transient failures, add `--retries 2`. Successful targets
are not repeated. If you increase ping count or per-ping `--timeout`, increase
`--request-timeout` enough to cover the whole attempt for one agent. Reduce
`--concurrency` when you need fewer simultaneous target operations. Defaults
and limits are in the [CLI reference](../CLI_REFERENCE.md#fleet-selection-and-reports).

## Register and verify service watches

A watch adds an explicit service name and optional health check on each selected
agent. In this example, the agents running `payments` also host an API listening
on port 8080 with a `/health` endpoint. Confirm the service specification is
appropriate for every target before registering it. For other specification
forms, see [adding a watch](services.md#add-an-explicit-watch).

1. Preview the registrations:

   ```sh
   coral services watch api:8080:/health --colony '<colony-id>' \
     --service payments --runtime docker --dry-run
   ```

   Check the target IDs and service parameters. Fleet watch uses the same
   selectors as mesh diagnostics. You can supply several service specifications
   when every selected agent needs the same watches.

2. Register the watches and save a report:

   ```sh
   coral services watch api:8080:/health --colony '<colony-id>' \
     --service payments --runtime docker \
     --concurrency 4 --retries 2 --report watch.json
   ```

   This changes the agents' service registrations and can trigger a managed
   Beyla restart. A matching existing watch returns `unchanged`; a new watch
   returns `connected`. Conflicting names, ports, process patterns, or health
   settings fail instead of updating the existing configuration. Successful
   watches remain registered even when another watch on the same agent fails.

3. Inspect each selected agent's current watched inventory. Replace `<agent-id>`
   with an ID from the report:

   ```sh
   coral services list --colony '<colony-id>' \
     --agent '<agent-id>' --source watched
   ```

   Confirm the service name and port, then inspect `HEALTH CHECK` and
   `LAST CHECK`. Registration success alone does not establish service health.
   Fleet watch does not support `--wait`; inspect the inventory again after
   the agent has performed its checks. Use `--format json` on this inventory
   command to inspect the health endpoint and other registration details.

To use previews and reports for one agent, select it explicitly:

```sh
coral services watch api:8080:/health --colony '<colony-id>' \
  --agent '<agent-id>' --dry-run
```

Adding `--dry-run`, `--report`, or `--format` selects the fleet workflow even
for one agent, so it requires an explicit target. For local watches or direct
agent URLs, follow [services and health](services.md); `--agent-url` cannot be
combined with fleet operations.

## Inspect failures and retry affected targets

The commands above save JSON reports while printing a table. Add `--format json`
to print the report to stdout instead. Progress and command errors use stderr,
so operation results remain valid JSON even when some targets fail.

If any target fails or is canceled, the command prints its results and attempts
to save the report before returning a nonzero exit status. Ctrl+C also records
queued targets as canceled. Successful operations remain in place.

1. Open `ping.json`, `audit.json`, or `watch.json` and inspect `summary` and
   `results`. If you have `jq`, list failed or canceled targets with:

   ```sh
   jq '.results[] | select(.status == "failed" or .status == "canceled") |
     {agent_id, status, error_code, error, data}' ping.json
   ```

   For watch reports, `data.services` records each service's registration status
   and error. This lets you distinguish a failed target with some completed
   watches from one where no watch succeeded.

2. Resolve the reported cause before retrying. For mesh failures, follow
   [mesh troubleshooting](troubleshooting.md#mesh-traffic-fails-after-enrollment).
   If ping works but watch cannot reach an agent, check the CLI host's route to
   the agent mesh address and access to TCP port 9001. For a registration
   conflict, inspect that agent's watched inventory and correct its service
   configuration. Permission errors and configuration conflicts do not trigger
   automatic retries.

3. Retry the failed or canceled targets, saving a separate report:

   ```sh
   coral mesh ping --retry-failed ping.json --report ping-retry.json
   coral services watch --retry-failed watch.json --report watch-retry.json
   ```

   Run the command corresponding to the report you saved; audit reports use
   `coral mesh audit --retry-failed audit.json`. The report restores the Colony
   and operation parameters. Retry selects the original failed or canceled IDs
   without reapplying the filters. It retains successful service steps within
   failed agents. If a registration reply was lost, a matching live watch is
   recognized on retry.

   You cannot combine retry with new selectors or `--all`, change the original
   service specifications or ping settings, or select a different Colony.
   Dry-run reports cannot be retried.

4. Check the retry summary and remaining errors. Each retry report contains
   only that invocation's targets. For watches, verify the affected agents'
   inventory and health checks again as described above.

The [CLI reference](../CLI_REFERENCE.md#fleet-selection-and-reports) describes
report fields and storage behavior. Mesh diagnostic details are under
`.results[].data`; agent inventory JSON remains an array. Scripts that consumed
the older mesh audit array must use the report envelope.
