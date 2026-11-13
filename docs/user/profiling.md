# CPU and Memory Profiling

A profile samples work performed by a process. Use retained profiles to inspect
an earlier interval, or request a new capture while reproducing a problem. Start
with a [watched service](services.md#add-an-explicit-watch) so the agent has a
process to target; automatically observed traffic alone does not activate all
continuous profilers.

## Check collection requirements

CPU profiling uses Linux eBPF and perf events. The agent needs process
visibility, BPF/performance permissions, and kernel support. Stack attribution
also depends on the application's unwind information and symbols. Native macOS
agents cannot supply Linux eBPF samples; run the agent inside the Linux host or
VM that runs the application.

Memory profiling fetches heap profiles from a compatible runtime endpoint,
including the [Go SDK](instrumentation.md#expose-go-runtime-metadata). It is not
a language-independent heap tracer supplied by CPU eBPF. Verify that runtime
discovery succeeds before expecting allocation data.

The continuous CPU defaults are 19 Hz and a 15-second collection interval.
On-demand CPU captures default to 99 Hz for 30 seconds. These are sampling
settings, not workload-independent overhead guarantees. Collection and Colony
polling introduce delay, and retained data has a finite lifetime.

## Query an earlier interval

```bash
coral query cpu-profile --service api --since 1h > api-cpu.folded
coral query memory-profile --service api --since 1h
coral query memory-profile --service api --since 1h --show-growth --show-types
```

CPU output defaults to folded stacks. Each line represents a stack and its
sample count, suitable for a flame graph. Memory output defaults to a summary
and can also emit folded data. `--build-id` separates observations from
different builds when that metadata is available.

Growth and allocation categories help form a hypothesis, but do not establish
a leak by themselves. Type grouping can be inferred from function names rather
than a complete census of runtime object types. Compare equal time windows and
account for traffic changes and process restarts.

## Capture current activity

Run while the service receives representative traffic:

```bash
coral profile cpu --service api --duration 30 > api-cpu.folded
coral profile cpu --service api --duration 30 --frequency 199 --format json
coral profile memory --service api --duration 30 > api-memory.folded
```

CPU `--duration` is seconds, with a maximum of 300; `--frequency` is samples
per second, capped at 1000. A higher rate can improve coverage while increasing
collection cost. CPU `--pod` selects an instance when the service has several.
See the [CLI reference](../CLI_REFERENCE.md) for each command's options.

Generate an SVG with the repository's FlameGraph script:

```bash
./scripts/flamegraph.pl api-cpu.folded > api-cpu.svg
```

Open the SVG in your browser. Wide frames represent more captured samples,
not necessarily the latency of a particular request. An empty capture under
load needs investigation: check target process, perf permissions, startup
warnings, and stack unwinding before interpreting it as idle CPU.

## Compare a trace with profiles

```bash
coral query trace-profile '<trace-id>' --service api --top 5
```

This reports CPU stacks associated with the span's service, process PID, and
surrounding time window. It uses retained samples, so run continuous collection
before the request of interest. Short requests can have little sample coverage.

Concurrent requests in the same process also contribute samples. The current
matching uses a padded time interval and does not isolate a goroutine; matching
service names and PIDs also has attribution limits across hosts. Treat the
report as process activity around the request, not proof of the request's exact
call path. The current implementation does not provide equivalent memory
correlation merely because `--type memory` is accepted by the CLI.

For exact function-event investigation, use [live probes](live-debugging.md).
For missing samples, see [troubleshooting](troubleshooting.md#profiles-or-functions-are-missing).
The [profiling architecture](../architecture/profiling.md) explains retention,
aggregation, and correlation limits in detail.
