---
rfd: "114"
title: "Coral Triage: Composite Diagnosis Command and CLI Dispatch Unification"
state: "draft"
breaking_changes: true
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "094", "100" ]
database_migrations: [ ]
areas: [ "ask", "tui", "cli", "debug", "query", "auth" ]
---

# RFD 114 - Coral Triage: Composite Diagnosis Command and CLI Dispatch Unification

**Status:** 🚧 Draft

## Summary

`coral ask` and `coral terminal` can identify a degraded service, but locating a
useful function still requires the agent to compose several commands. This RFD
adds `coral triage`, a bounded composite diagnosis command that combines the
existing health summary, profiling data, and function registry into one
structured result. Attaching an eBPF probe is explicit through `--attach`;
triage is read-only by default.

This RFD also fixes a dispatch regression introduced by RFD 100. `coral ask`
still defaults to MCP dispatch and its prompt bootstrap calls retired
per-operation MCP tools. The proxy now exposes only `coral_cli`, so service and
health context silently disappear. CLI dispatch becomes the default, both
dispatch modes fetch bootstrap context through `coral_cli`, and the returned
JSON is parsed as JSON rather than as the retired tools' text output.

Finally, this RFD corrects stale HTTP RBAC procedure mappings for the existing
`QueryFunctions` and `AttachUprobe` RPCs. The current map names old procedures,
causing both real procedures to fall through to `PermissionStatus`.

## Problem

### Composite diagnosis is missing

`coral query summary` reports service health, including status, error rate,
latency, regressions, and optional profiling data. It does not resolve that
evidence to a registered, probeable function. An agent must currently inspect
the summary, search the function registry, and optionally attach a probe in
separate tool calls. RFD 100 identified this repeated workflow as a candidate
for a composite command but did not define or implement one.

The function registry does not currently provide general historical P95 data.
`QueryFunctions` only populates `Metrics` for functions with an active debug
session, and `PrioritizeSlow` is not implemented by the server. Triage therefore
must not claim it can rank unprobed functions by P95. Its candidate selection
must use evidence that exists today: profiling hot paths first, then semantic
registry ordering based on the summary's reported issues.

### `coral ask` bootstrap context is broken

When `AskAgentConfig.DispatchMode` is unset, `NewAgent` selects MCP dispatch.
`fetchServiceContext` and `fetchHealthAlerts` then call
`coral_list_services` and `coral_query_summary`. RFD 100 removed those tools;
`coral colony mcp proxy` now exposes only `coral_cli`. Failures are swallowed,
so conversations start with an unavailable service list and no alert block.

Changing only the MCP tool name is insufficient. `coral_cli` appends
`--format json`, while the current health parser expects the old text output
with emoji-prefixed service blocks. The bootstrap path must parse the actual
JSON produced by `coral services --format json` and
`coral query summary --format json`.

CLI dispatch currently bootstraps topology only. Making it the default without
also loading services and health would preserve the same blind-start problem in
a different code path. Both dispatch modes therefore need the same JSON-based
service and health bootstrap behavior.

### Existing debug RPC permissions are mapped under stale names

The HTTP RBAC table maps `ListFunctions` and `AttachProbe`, while the generated
Connect procedures are named `QueryFunctions` and `AttachUprobe`. Unknown
procedures default to `PermissionStatus`, so the current behavior does not
enforce the intended permissions. Triage must not be added until these existing
mappings are corrected:

- `QueryFunctions` requires `PermissionQuery`.
- `AttachUprobe` requires `PermissionDebug`.

This is marked as a breaking change because credentials that only have
`PermissionStatus` may currently reach those procedures due to the stale map
and will be denied after the correction.

## Goals

- Provide one agent-facing command that identifies the worst degraded service
  and resolves available evidence to a concrete function candidate.
- Keep the default command read-only and require `--attach` for instrumentation.
- Return a stable structured result even when candidate resolution or optional
  attachment cannot be completed.
- Make CLI dispatch the default for `coral ask` while preserving MCP dispatch as
  an explicit opt-in.
- Give both dispatch modes equivalent service and health bootstrap context.
- Enforce the intended permissions on the existing function-query and uprobe
  attachment procedures.

## Non-goals

- Adding a new protobuf RPC or server-side composite operation.
- Inventing function latency metrics that the registry does not currently have.
- Automatically profiling every function to manufacture ranking data.
- Cross-service root-cause analysis or dependency-cause attribution.
- Removing MCP dispatch mode.

## Solution

### `coral triage`

Add a top-level `coral triage [service]` command that composes existing RPCs.
It runs diagnosis by default and only calls `AttachUprobe` when the caller passes
`--attach`.

The command performs these stages:

1. Call `QueryUnifiedSummary` for the requested service, or for all services
   when the positional argument is omitted.
2. If the response contains no summaries, return `outcome: no_data` without
   querying functions or attaching.
3. If no service was specified, select the worst service using the total order
   `critical > degraded > healthy > unknown-or-empty`. Within the same status,
   sort by error rate, then average latency, then service name for deterministic
   output.
4. For an all-services request, if the selected service is neither `critical`
   nor `degraded`, return `outcome: nothing_degraded`. For a named service,
   return `outcome: healthy` or `outcome: unknown` when applicable. None of
   these outcomes queries functions or attaches a probe.
5. Resolve a candidate using the rules below.
6. If `--attach` was passed and a candidate was resolved, call `AttachUprobe`
   with the requested bounded duration. Otherwise leave the system unchanged.

The service argument scopes diagnosis; it does not require that the named
service be degraded. A healthy named service returns its summary with outcome
`healthy` and does not attach; an unknown status behaves the same way but uses
outcome `unknown`.

### Candidate resolution

Candidate selection uses only signals available in the current API:

1. If `profiling_summary.hot_path` is present, walk it from leaf to caller and
   query `QueryFunctions` for each exact function name within the selected
   service. Select the first exact registry match that is probeable and not
   already probed. Function-name normalization must handle a fully qualified
   registry name and an unqualified profile frame without allowing a match from
   another service.
2. Otherwise, build a semantic query from the summary's `issues` and regression
   messages and call `QueryFunctions` scoped to the service. Preserve the
   registry's result order and select the first result that is probeable and not
   already probed.
3. If there is no profiling hot path and no issue/regression text, or no
   probeable registry match is found, return `candidate_status: not_found`.
   Do not select an arbitrary function from an unfiltered registry listing.

The response records the source of the choice as `profiling_hot_path` or
`semantic_issue_match`. It does not expose a P95 value unless a future API
provides one for that actual candidate.

### Optional attachment and failure semantics

`--attach` is explicit because attaching a uprobe changes a running target and
can add overhead. `--attach-duration` defaults to `30s` and is capped at the
same server-enforced maximum as `AttachUprobe`. Supplying
`--attach-duration` without `--attach` is an argument error.

Summary acquisition is required: argument, connection, or summary failures
return a non-zero exit. Candidate lookup and explicit attachment are later
stages, so their failures are returned as structured partial results:

- `candidate_status: unavailable` with `candidate_error`,
- `candidate_status: not_found` when the lookup completed without a match, or
- `attach.status: failed` with `attach.error`.

These partial outcomes exit successfully so `coral_cli` preserves the JSON for
the agent. Failure fields must be prominent in text output and must not be
described as a successful attachment. Permission denial remains visible as
`attach.status: failed`; it is never silently downgraded.

### Dispatch and prompt bootstrap

When `AskAgentConfig.DispatchMode` is empty, `NewAgent` selects
`config.DispatchModeCLI`. Explicit `mcp` remains supported.

Refactor service and health bootstrap behind a small command runner with two
implementations:

- CLI mode invokes the existing local `executeCLITool` path.
- MCP mode calls the proxy's sole `coral_cli` tool with an `args` array.

Both modes run and parse:

- `coral services --format json`, whose response is an object containing a
  `services` array.
- `coral query summary --since 5m --format json`, whose response is an array of
  summary objects.

The health parser includes only `degraded` and `critical` services. Tests must
use fixtures matching the real CLI JSON shape, not the retired MCP tool output.
CLI mode continues to include topology context. The shared prompt instructions
recommend `triage` for combined health-and-code-location questions.

### RBAC correction

Update `internal/colony/httpapi/rbac.go` to use the generated procedure names:

- `/coral.colony.v1.ColonyDebugService/QueryFunctions` →
  `PermissionQuery`
- `/coral.colony.v1.ColonyDebugService/AttachUprobe` →
  `PermissionDebug`

Replace the stale mappings rather than adding parallel aliases unless an older
RPC is still present in the generated service. Add route-level tests using the
generated constants so future proto renames cannot silently drift from RBAC.

### CLI reference

`GenerateCLIReference` currently traverses selected command groups. Extend its
allowlist to include the top-level leaf `triage` explicitly. The existing root
service command is named `services`, not `service`; correct that allowlist while
making this change. Do not emit every top-level leaf automatically, because that
would expose unrelated operational commands to the agent.

## User Interface

```bash
# Diagnose the named service; read-only.
coral triage api

# Diagnose the worst currently degraded service; read-only.
coral triage

# Diagnose and attach a 30-second probe to a resolved candidate.
coral triage api --attach

# Override the bounded attachment duration.
coral triage api --attach --attach-duration 15s

# Structured output for agents and scripts.
coral triage api --format json
```

Example text output:

```text
Triage: api
  Outcome: degraded
  Status: critical (error_rate=4.2% avg_latency=892ms)
  Candidate: api.handleCheckout (service/checkout.go:118)
    Source: profiling hot path
  Attach: not requested
```

Example JSON output:

```json
{
  "service": "api",
  "outcome": "degraded",
  "summary": {
    "status": "critical",
    "error_rate": 4.2,
    "avg_latency_ms": 892
  },
  "candidate_status": "found",
  "candidate_function": {
    "name": "api.handleCheckout",
    "file": "service/checkout.go",
    "line": 118,
    "source": "profiling_hot_path"
  },
  "attach": {
    "status": "not_requested"
  }
}
```

Stable outcome values are `degraded`, `healthy`, `unknown`,
`nothing_degraded`, and `no_data`.
Stable candidate statuses are `found`, `not_found`, and `unavailable`. Stable
attach statuses are `not_requested`, `attached`, `skipped`, and `failed`.

## Configuration

```yaml
ai:
  ask:
    agent:
      # "cli" is the default after RFD 114.
      # Set "mcp" only when the Agent must dispatch through an MCP proxy.
      dispatch_mode: cli
```

Existing configurations that explicitly set `dispatch_mode: mcp` retain MCP
dispatch. Only the empty-string fallback changes.

## Implementation Plan

### Phase 1: Correct RPC authorization

- [ ] Replace stale debug procedure keys in the HTTP RBAC map with
      `QueryFunctions` and `AttachUprobe`.
- [ ] Reference generated Connect procedure constants where practical.
- [ ] Test that status-only credentials are denied, query credentials can call
      `QueryFunctions`, and only debug credentials can call `AttachUprobe`.

### Phase 2: Unify dispatch and bootstrap context

- [ ] Change the unset dispatch fallback in `NewAgent` from MCP to CLI.
- [ ] Introduce a shared JSON command runner for prompt bootstrap, backed by
      local CLI execution in CLI mode and `coral_cli` MCP calls in MCP mode.
- [ ] Parse the real JSON shapes returned by `services` and `query summary`.
- [ ] Include service and degraded-health context in both dispatch modes.
- [ ] Update the CLI system prompt to recommend `triage` for combined
      health-and-location questions.
- [ ] Update configuration comments and engineering documentation.
- [ ] Test the unset default, explicit MCP mode, both runners, malformed JSON,
      command failures, and real service/summary JSON fixtures.

### Phase 3: Implement `coral triage`

- [ ] Add `internal/cli/triage/` with an internal client interface so selection
      and partial-result behavior can be unit tested without a live colony.
- [ ] Implement deterministic service selection using
      `critical > degraded > healthy > unknown`, error rate, average latency,
      and name.
- [ ] Implement exact hot-path resolution and semantic issue fallback without
      relying on `Metrics.P95` or `PrioritizeSlow`.
- [ ] Add `--since` (default `5m`), `--attach`, `--attach-duration` (default
      `30s`), and `--format text|json`.
- [ ] Validate that `--attach-duration` requires `--attach`.
- [ ] Implement stable full and partial result schemas.
- [ ] Register `triage` in `internal/cli/root.go`.

### Phase 4: Agent reference and documentation

- [ ] Add the top-level `triage` leaf explicitly to `GenerateCLIReference`.
- [ ] Correct the relevant command-group name from `service` to `services`.
- [ ] Test that `triage` and `services` appear and unrelated top-level commands
      remain excluded.
- [ ] Update `docs/CLI.md`, `docs/CLI_REFERENCE.md`, and
      `docs/engineering/11_mcp_and_llm_interfacing.md`.

## Testing Strategy

### Unit tests

- Service ranking covers every known status, unknown/empty status, all
  tie-breakers, empty responses, and stable ordering.
- A healthy explicit service and an all-healthy fleet never query functions or
  attach a probe.
- Hot-path selection resolves an exact service-scoped, probeable function and
  skips unregistered, unprobeable, and already-probed frames.
- Semantic fallback uses issue/regression text, preserves registry ordering,
  and never selects from an unfiltered arbitrary list.
- Default invocation never calls `AttachUprobe`; `--attach` does.
- Candidate and attach failures produce the documented partial JSON result.
- Bootstrap parsers consume real `services` and `query summary` JSON shapes in
  both CLI and MCP dispatch modes.
- RBAC tests use generated procedure constants and verify the required
  permissions.

### Integration tests

- End-to-end JSON output for a degraded service with a profiling-resolved
  candidate, with and without `--attach`.
- All-services invocation selects the expected service deterministically.
- Explicit attachment denial returns a structured partial result containing
  `attach.status: failed` and no claimed session ID.
- `coral ask` with no configured dispatch mode uses CLI dispatch and receives
  service and health bootstrap context.
- Explicit MCP dispatch issues only `coral_cli` calls and receives equivalent
  bootstrap context.

## Security Considerations

Triage is read-only unless `--attach` is explicitly supplied. Summary and
function lookup require `PermissionQuery`; attachment requires
`PermissionDebug`. The RBAC correction closes an existing authorization gap in
which the real debug procedure names fall through to `PermissionStatus`.

An attached probe has a bounded lifetime, and triage skips functions that are
already probed. The result always distinguishes requested, attached, skipped,
and failed attachment states so an agent cannot mistake a partial diagnosis for
successful instrumentation.

## Rollout and Compatibility

The CLI dispatch default changes only configurations where `dispatch_mode` is
unset. Explicit MCP configurations continue to work through `coral_cli`.

The RBAC correction is intentionally behavior-changing: status-only tokens that
could reach `QueryFunctions` or `AttachUprobe` because of stale mappings will no
longer be authorized. Release notes must call out this security correction.

## Implementation Status

**Core Capability:** ⏳ Not Started

`coral triage`, dispatch unification, JSON bootstrap parsing, and the RBAC
correction are unimplemented.

## Future Work

### Registry-backed function performance ranking

A future RFD may persist function-level metrics and implement
`QueryFunctions.PrioritizeSlow`. Once that data exists for unprobed functions,
triage can add latency-based ranking without manufacturing a signal.

### Cross-service triage

Correlating a local candidate with upstream and downstream dependencies requires
topology-aware evidence and is intentionally outside this RFD.

### Additional composite commands

Further composites should be derived from observed agent session logs rather
than designed speculatively.

### Removing MCP dispatch mode

If usage shows no need for explicit MCP dispatch inside `Agent`, a future RFD
can remove it. External MCP clients would continue to use the proxy's
`coral_cli` tool.
