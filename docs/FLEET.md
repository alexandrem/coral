# Fleet operations

Agent inspection, mesh diagnostics, and bulk observation use the same selectors:
`--agents`, `--status`, `--service`, `--runtime`, and `--version`. Values within a
selector are ORed; different selectors are ANDed. Names and versions match exactly.
`--status` refers to agent heartbeats, independently of service health checks.

```sh
coral colony agents --service payments --runtime docker
coral mesh ping --service payments --runtime docker --dry-run
coral mesh audit --service payments --runtime docker --format json
```

`mesh ping` and `mesh audit` target all registered agents when no selector is
provided. A positional agent ID still selects one agent. `--all` explicitly selects
the entire colony and cannot be combined with filters. Filtered operations fail
when nothing matches. An explicitly named agent that has disappeared is reported
as a failed target; it is never replaced by another agent.

## Watch services across selected agents

Use the existing service specification syntax to register the same observations on
selected agents. Start with a preview:

```sh
coral services watch api:8080:/health redis:6379 \
  --colony production --service payments --dry-run

coral services watch api:8080:/health redis:6379 \
  --colony production --service payments \
  --concurrency 4 --retries 2 --report watch.json --format json

# Explicitly target every registered agent.
coral services watch api:8080:/health --colony production --all --dry-run

# A single agent can use the same reports and previews.
coral services watch api:8080:/health --agent agent-1 --dry-run --format json
```

Fleet observation requires an explicit target selection or `--all`. Local watch
and the existing single-agent command retain their usual behavior. Adding a bulk
option such as `--dry-run`, `--report`, or `--format` selects the fleet workflow.
`coral connect` supports the same flags as a permanent alias. `--agent-url` and
`--wait` belong to the existing direct/single-agent workflow.

A matching authoritative registration returns `unchanged`. A conflicting name,
port, process pattern, or health configuration is reported as a per-service failure.
Bulk watch does not infer that ConnectService's existing AlreadyExists response
means the requested configuration was updated. Successful registrations are kept
when another service on the same agent fails.

Mesh diagnostics run through the Colony. Observation uses the current direct
agent API, so the CLI must be able to reach the selected agents' mesh addresses.

## Execution and partial failures

All bulk commands resolve and pin agent IDs before beginning their operation.
`--dry-run` performs inventory reads and prints the target set and operation
parameters; it performs no ping, audit, or service registration.

`--concurrency` limits concurrent target operations (default 8, range 1–128).
`--request-timeout` bounds each target attempt (default 10s, maximum 5m).
For ping, `--timeout` bounds each individual ping; set the request timeout high
enough for the chosen count and per-ping timeout.

`--retries` enables up to 10 retries per failed target (default 0) for transient
errors such as unavailability, request timeouts, and resource exhaustion.
Successful targets are not repeated. Bulk observation retains successful service
steps within a failed target. Permission errors and configuration conflicts do
not trigger automatic retries. If the first registration response is lost, a
matching live registration is recognized on retry.

Every target receives a result, including queued targets canceled by Ctrl+C.
Results are printed and saved before the command returns a nonzero exit status
for any failed or canceled target. Other targets continue after an individual
failure. Ping targets fail when no response arrives or packet loss exceeds 50%.
Audit NAT classifications remain findings in the data; RPC and assessment errors
fail the target.

## Save results and retry failed targets

`--report` writes a JSON report atomically, with owner-only permissions. An
unusable report destination is rejected before operations begin. The same report
is printed to stdout with `--format json`; progress and the CLI error message use
stderr, keeping stdout valid JSON on partial failure.

```sh
coral mesh ping --service payments --report ping.json
coral mesh ping --retry-failed ping.json --report ping-retry.json --format json

coral services watch --retry-failed watch.json --report watch-retry.json --format json
```

`--retry-failed` takes only failed/canceled agent IDs from the saved report. It
uses that report's colony and resolves those same identities in the current
registry, without re-evaluating the original filters. It cannot be combined with
new selectors or `--all`. Omitted operation parameters are restored from the
report. Explicit service specifications or ping settings must match the original
parameters; an explicitly different colony is rejected. Dry-run reports cannot be retried.
Each retry report describes the targets of that retry, rather than duplicating
successful results from the previous run.

## JSON contract

Ping, audit, and fleet watch share a versioned envelope:

```json
{
  "version": 1,
  "operation": "mesh ping",
  "colony_id": "production",
  "parameters": {"count": 4, "timeout": "2s"},
  "dry_run": false,
  "started_at": "2026-11-12T18:00:00Z",
  "finished_at": "2026-11-12T18:00:01Z",
  "summary": {"total": 2, "succeeded": 1, "failed": 1, "canceled": 0, "planned": 0},
  "results": [
    {"agent_id": "agent-1", "status": "succeeded", "attempts": 1, "data": {}},
    {"agent_id": "agent-2", "status": "failed", "attempts": 1,
     "error_code": "unavailable", "error": "unavailable: no replies"}
  ]
}
```

Result status is `planned`, `succeeded`, `failed`, or `canceled`. `attempts` counts
attempts in the current invocation. `data` contains the native ping/audit result,
or a `services` array containing each registration's name, status, and error.
Registered services use `connected` or `unchanged`; failed services retain their
error and whether it is transient. Target results are ordered by full agent ID.

Agent inventory keeps its existing JSON array. Mesh audit JSON now uses the shared
envelope: scripts that previously read the result array directly should use
`.results[].data`.
