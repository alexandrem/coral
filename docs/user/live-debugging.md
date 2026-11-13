# Live Function Debugging

Use a function probe when metrics or profiles have identified a part of a
service that needs closer observation. A Linux agent attaches uprobes to the
running executable for a bounded session. You can manage those sessions
directly through the CLI without an AI assistant.

## Prepare a target

The agent needs the [required privileges](installation.md#grant-server-privileges),
a visible target process, and usable function metadata. Add an explicit
[service watch](services.md) if the service only appears as observed traffic.
The [Go runtime SDK](instrumentation.md#expose-go-runtime-metadata) can expose
metadata; binary scanning can also discover functions when the agent can read
the executable and its symbols.

Stripping and compiler optimization change what is discoverable. DWARF supplies
richer size, argument, and source information; symbol-only fallbacks can have
less detail. An SDK endpoint does not reconstruct all metadata removed from a
fully stripped executable. A function inlined away has no independent entry
point to probe. Preserve suitable symbols for the features you intend to use.

## Find a function

```bash
coral debug search checkout --service api
coral debug info --service api --function 'main.ProcessOrder'
```

Use the function identifier actually returned by search rather than assuming
the example exists in your binary. First-time indexing and Colony polling can
delay search visibility. Search compares identifier vocabulary; a matching name
is a lead for inspection, not evidence of what the function does.

## Attach, inspect, and stop

```bash
coral debug attach api --function 'main.ProcessOrder' --duration 60s
coral debug session list --service api
```

Use the returned session ID in subsequent commands:

```bash
coral debug session events '<session-id>' --follow
coral debug session query api --session-id '<session-id>'
coral debug session get '<session-id>'
coral debug session stop '<session-id>'
```

The configured duration bounds attachment. Stop a session early when you have
enough evidence. `events --follow` observes the event stream; querying a session
reads retained results. Empty output can mean the function was never called,
the session has no retained events in that interval, or attachment lacked the
necessary metadata or permissions.

Argument and return capture options depend on metadata and the probe path.
Their presence in the command interface does not promise complete values for
every compiler, type, or optimized binary. Check the actual event fields before
depending on them in an investigation.

## Limit event volume

Filter a hot function when attaching, or adjust an existing session:

```bash
coral debug attach api --function 'main.ProcessOrder' \
  --duration 60s --min-duration 50ms --filter-rate 10
coral debug filter '<session-id>' --min-duration 100ms
coral debug filter '<session-id>' --min-duration 0 --filter-rate 1
```

Duration thresholds and `--filter-rate` are applied in the kernel before events
reach userspace. A filter rate of 10 emits one in ten events; 0 or 1 emits all.
Filtering changes the sample you inspect, so counts are not a complete record
of function calls. Updating a filter does not require detaching the session.

## Interpret function duration

For Go, Coral observes return instructions rather than rewriting return
addresses with ordinary uretprobes. Duration requires a usable function extent
and attachable return instructions. A successful entry probe can still have
zero duration when return metadata is unavailable. Panics, process termination,
in-flight calls at session boundaries, and stack changes can also leave entry
and return counts unmatched.

Do not assume that a zero duration means a fast call. Inspect agent warnings
for entry-only fallback. For a tiny function you control, `//go:noinline` can
preserve a standalone target; verify the compiled binary afterward. It does not
restore stripped DWARF or guarantee every other probe prerequisite.

## Profile a group of functions

```bash
coral debug profile --service api --query checkout \
  --strategy critical-path --duration 60s
```

This selects functions from search results and starts bounded probe sessions.
Inspect the selection and the resulting sessions; the strategy name is not a
guarantee that every request path is covered. Stateful correlation descriptors
can be deployed through the TypeScript SDK, then inspected with
`coral debug correlations list` and removed with
`coral debug correlations remove <correlation-id>`.
See [TypeScript investigations](scripting.md#stateful-correlations) for a
concrete deployment example and its cleanup.

## Start from a request path

```bash
coral debug trace api --path /checkout --duration 30s --wait
```

This convenience command guesses a function name from the path and starts a
probe session; for example, `/checkout` maps to `main.ProcessCheckout`. It does
not discover an arbitrary router's handler or trace a complete request across
services. Use it when the inferred name matches your application. Otherwise,
find the actual symbol with `debug search` and attach explicitly. `--wait`
waits for the capture and displays its results; inspect the session and agent
warnings when the guessed target cannot be attached.

Use the [CLI manual](../CLI_REFERENCE.md) for session syntax and formats, and
[troubleshooting](troubleshooting.md#profiles-or-functions-are-missing) when
discovery or attachment fails.
