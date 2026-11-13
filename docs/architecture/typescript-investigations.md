# TypeScript Investigations

TypeScript scripts combine Coral queries into an investigation without adding a
new Go command for every analysis. The CLI runs a script in Deno; the script
uses the SDK to call the Colony and writes structured output. Built-in
investigation skills are reusable TypeScript functions following a result
contract, rather than a separate agent-side collection mechanism.

## Local execution

`coral run <script.ts>` resolves a Deno executable and the selected Colony
address, builds a permission-limited invocation, and executes the script locally.
The runtime can come from an embedded binary, a companion binary, or the system
path. The default command timeout is 60 seconds; watch mode asks Deno to rerun
on changes.

The file execution path permits network access to the resolved Colony host,
reads under the current directory, and access to `CORAL_COLONY_ADDR` and
`CORAL_MODE`. It does not grant filesystem writes or subprocess execution.
These are the flags supplied by the CLI, not a general promise that any
script-loading context has identical permissions or import resolution.

`ExecuteInline` handles code supplied as text. It creates a temporary directory,
extracts the embedded SDK there, writes a script and import map, and invokes
Deno with `--no-remote`. The map resolves `@coral/sdk` and built-in skill imports
without a remote module fetch. The process runs in that directory and captures
stdout for its caller while forwarding stderr. Context cancellation or a
configured timeout bounds execution.

These are distinct paths: the inline helper creates its SDK import map; file
execution does not automatically reproduce that setup. File-based scripts use
their applicable Deno configuration and import resolution. The
[scripting guide](../user/scripting.md) owns usage details.

Implementation: [CLI run and inline execution](../../internal/cli/run/run.go)
and [inline execution tests](../../internal/cli/run/execute_inline_test.go).

## SDK requests and the Colony boundary

The SDK's `ColonyClient` constructs HTTP requests using the resolved Colony
host and Connect-style JSON RPC paths. Modules expose service, metric, trace,
system, database, and correlation operations. A script composes those operations
and computes results locally; it does not gain direct ownership of the Colony
database or kernel collectors.

For example, a latency report can list services, query their summaries, compare
latencies with a threshold, and return the supporting measurements. The
measurements retain their sampling, aggregation, and freshness limits. Calling
them through a script does not turn a partial telemetry view into a complete
record of the application's behavior.

Implementation: [client](../../pkg/sdk/typescript/client.ts),
[SDK exports](../../pkg/sdk/typescript/mod.ts), and
[latency report](../../pkg/sdk/typescript/skills/latency-report.ts).

## Reusable skills and result interpretation

`SkillResult` has a short `summary`, a status (`healthy`, `warning`, `critical`,
or `unknown`), structured `data`, optional recommendations, and an optional
`render` specification. The status is the skill's interpretation of its input;
it is not an independently verified health assertion.

Built-ins include latency reporting, error correlation, memory-growth analysis,
and a latency trap. They can be imported independently, and their implementation
defines the queries and thresholds each uses. Correlation orchestration depends
on the agent-side event and action wiring described in
[eBPF instrumentation](ebpf-instrumentation.md#local-correlation-and-action-delivery).
The presence of an SDK function alone does not establish an end-to-end trigger
delivery guarantee.

Implementation: [result types](../../pkg/sdk/typescript/types.ts),
[skill exports](../../pkg/sdk/typescript/skills/mod.ts), and
[correlation API](../../pkg/sdk/typescript/correlation.ts).

## Rendering and failure behavior

File execution relays stdout to the terminal while capturing it. When
`coral terminal` has an active server, the run command looks for the last JSON
output line and an optional `render` field, then pushes a render event to that
server. Without an active server or a usable render specification, command
output remains available without a dashboard event.

Rendering is best effort and separate from script success. A script can return
useful JSON without rendering a panel, or exit with an error after producing
partial stdout. The inline helper likewise returns captured stdout and an exit
code to its caller. Consumers should distinguish execution failure from a
skill's reported application-health status.

Implementation: [run output bridge](../../internal/cli/run/run.go) and
[terminal server](../../internal/cli/terminal/).
[MCP and LLM integration](mcp-and-llm.md) explains how an AI client reaches the
CLI operations that launch an investigation.

## Design history

- [RFD 076: TypeScript Execution](../../RFDs/076-sandboxed-typescript-execution.md)
- [RFD 093: Skills SDK](../../RFDs/093-skills-typescript-sdk.md)
