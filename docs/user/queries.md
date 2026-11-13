# Querying Telemetry

Begin with an accessible Colony and [services in its inventory](services.md).
Use a summary to choose a service, then narrow the time window and evidence
source. Commands below run on the operator workstation; they do not require an
LLM or elevated workstation privileges.

## Move from a summary to individual requests

```bash
coral query summary
coral query summary api --since 10m
coral query metrics api --protocol http --since 10m
coral query traces api --since 10m --min-duration-ms 500
coral query logs api --level error --since 10m
```

The summary combines available observations; absence of data is not a clean
health check. Metrics help locate protocol-level changes. Traces expose retained
spans around individual operations. Logs expose collected log evidence, not an
automatic tail of every file on the host.

Use the service as a positional argument for these commands. Options and
formats vary by subcommand; [the CLI reference](../CLI_REFERENCE.md) is the
canonical syntax source. For example, query spans use a millisecond threshold,
whereas a live debug duration filter accepts `50ms`.

```bash
coral query traces api --trace-id '<trace-id>'
coral query metrics api --http-method POST --status-code-range 5xx
coral query logs api --search 'timeout' --format json
```

If you only want a source you have configured, use
`--source ebpf` or `--source telemetry` on metrics and traces. Protocol detection
and standard application OTLP ingestion have different storage paths; successful
receipt of arbitrary OTLP metrics does not imply that every metric name is
available through the protocol query interface. See
[instrumentation](instrumentation.md#send-opentelemetry-data).

## Inspect dependencies

```bash
coral query topology
coral query topology --include-l4=false
coral query topology --format json
```

Topology combines relationships inferred from spans with observed TCP
connections. Connection layers distinguish `L7`, `L4`, and `BOTH`. Excluding L4
shows only trace-derived relationships. A TCP edge proves an observed network
connection, not an application-level request or a specific failure cause.

Topology is a derived view and can lag recent activity. Names and process
visibility affect attribution. For its inference rules, see
[service topology](../architecture/service-topology.md).

## Choose a time range and output

Most telemetry commands accept relative `--since` values such as `5m`, `1h`,
and `24h`; some also accept `--until`. Defaults differ: summary uses five
minutes, while common trace and metric queries use one hour. Consult the
command's manual entry instead of assuming every query accepts identical time
expressions.

Use `--format json` where supported when saving evidence for another tool.
CPU profiles default to folded stacks, memory summaries have their own formats,
and `trace-profile` currently prints a textual report. There is no universal
CSV or JSON flag shared by every command.

## Query SQL when a built-in view is insufficient

Use the Colony's guarded SQL interface for a bounded read:

```bash
coral query sql "SELECT table_name FROM information_schema.tables ORDER BY table_name" --max-rows 100
```

Discover current tables before writing a query; schemas differ between the
Colony and an agent, and evolve with the software. `coral duckdb` also exposes
database discovery and remote SQL:

```bash
coral duckdb list
coral duckdb query --colony "SELECT table_name FROM information_schema.tables"
coral duckdb query '<agent-id>' "SELECT table_name FROM information_schema.tables"
coral duckdb shell --colony
```

Here `--colony` is a boolean selecting the Colony database, rather than a Colony
ID. Agent queries can additionally select a database with `--database`. Use
the normal [context selection](configuration.md#select-a-colony) to choose the
environment first. The shell embeds DuckDB locally and loads its `httpfs`
extension for remote reads, which can require an initial extension download.
Use `.tables`, `.databases`, and SQL against `information_schema.columns` to
inspect attached data. `.refresh` reattaches databases to refresh the snapshot.

For example, query an agent's retained application spans to find routes with
slow captured requests:

```bash
coral duckdb query '<agent-id>' --database metrics.duckdb "
  SELECT service_name, http_route, count(*) AS captured_spans,
         quantile_cont(duration_ms, 0.95) AS captured_p95_ms
  FROM otel_spans_local
  WHERE timestamp >= now() - INTERVAL '10 minutes'
  GROUP BY service_name, http_route
  ORDER BY captured_p95_ms DESC
  LIMIT 20
" --format csv
```

This table contains accepted application OTLP spans on that agent. It does not
include all Beyla observations or every application request; filtering and
sampling can bias its percentile. Use the built-in metric queries for their
supported protocol aggregates, and raw SQL when the stored rows are the evidence
you intend to inspect. Check `.databases` and qualify tables with their attached
database alias when using several databases in the interactive shell.

Keep SQL reads bounded by time and row count. Agent raw observations and Colony
aggregates have different retention and detail, so counts from the two do not
necessarily match. These commands are investigation interfaces, not a supported
way to edit Coral's operational state. See [data ownership](../architecture/data-ownership.md)
for the storage model.

## Freshness and missing data

Agents buffer or retain observations and the Colony polls many streams
asynchronously. Discovery, collection, polling, and derived-view caching can all
delay visibility. Raw samples may expire before an extended outage recovers;
not every OTLP buffer is durable or replayable.

When a result is empty, confirm the selected environment and window, that the
service received traffic, and that its collector is enabled and authorized.
Then compare agent-local evidence with Colony results. Sampling can omit fast
or successful requests while keeping errors or high-latency spans, so retained
traces should not be read as a complete request census.

Use [profiling](profiling.md) for code-level CPU or heap evidence,
[live debugging](live-debugging.md) for specific function calls, and
[troubleshooting](troubleshooting.md#telemetry-is-missing-or-stale) to investigate
collection or transport gaps.
