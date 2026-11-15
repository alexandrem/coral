# OTLP Ingestion

The agent receives OpenTelemetry traces and metrics from applications and from
its managed Beyla process. Both use OTLP, but the receiver's wiring determines
where spans are stored. Standard application spans and Beyla spans do not
become the same storage stream simply because they use the same protocol.

## Receivers and source boundaries

`OTLPReceiver` implements gRPC trace and metric services and HTTP/protobuf
endpoints at `/v1/traces` and `/v1/metrics`. The user-facing defaults are gRPC
port 4317 and HTTP port 4318; the actual bind addresses come from receiver
configuration. Both transports enter the same transformation logic.

Beyla has a separate loopback receiver on default ports 4319/4320. Startup
installs `beyla.Manager.HandleSpan` as that receiver's span handler, routing its
output to the Beyla store. This callback is an architectural boundary: a
receiver with a custom handler can have a different persistence path from one
using standard telemetry storage.

The older `Receiver` type remains for testing compatibility and is marked
deprecated. Its placeholder transport code is not the production listener
implementation.

Implementation: [OTLPReceiver](../../internal/agent/telemetry/otlp_receiver.go)
and [startup wiring](../../internal/cli/agent/startup/services.go).
[eBPF instrumentation](ebpf-instrumentation.md) explains the managed subprocess.

## Transforming and storing a span

For a received trace batch, the receiver extracts the resource's service name
and process PID, converts trace/span IDs to hexadecimal strings, and computes
duration from start and end times. Selected HTTP fields and status information
become queryable fields; additional attributes remain attached to the span.
A missing service name falls back to `unknown`.

The standard span path applies capture filtering and stores accepted spans in
`otel_spans_local`. DuckDB assigns `seq_id` using `seq_otel_spans` during insert.
That sequence orders stored rows for polling, independently of the span's
observation timestamp and independently of Beyla's trace sequence.

| Source information | Internal use |
| --- | --- |
| Trace ID, span ID, parent span ID | Trace identity and parentage |
| `service.name` | Service attribution and query grouping |
| `process.pid` | Process-scoped profile correlation |
| Start/end timestamps | Observation time and duration |
| Error status and HTTP attributes | Error and protocol fields |
| Remaining attributes | Additional query context |

For the custom Beyla path, `HandleSpan` also performs service-observation
feedback before writing the local trace. That feedback and storage can succeed
or fail separately. See [service discovery](service-discovery.md) for the
consequence of observing a service before the corresponding span is durable.

Implementation: [span transformation](../../internal/agent/telemetry/otlp_receiver.go),
[span storage](../../internal/agent/telemetry/storage.go), and
[receiver tests](../../internal/agent/telemetry/otlp_receiver_test.go).

## Metric batches have a different lifetime

The receiver converts OTLP metric protobufs to `pdata` metrics, including
histograms, sums, and gauges, and appends them to a mutex-protected memory
buffer. It retains at most 100 batches, evicting the oldest on overflow.
`QueryMetrics` copies the current batch list; `ClearMetrics` clears it after a
consumer processes it.

This is neither durable storage nor an atomic consume-and-acknowledge log.
A process restart loses the buffer, and separate query/clear calls do not
provide the sequence replay semantics of stored spans. The Beyla consumer
transforms these metrics into its own locally persisted protocol records;
standard OTLP metric receipt alone does not establish that same storage path.

[Data ownership](data-ownership.md) distinguishes the retained streams from
memory buffers. [Telemetry polling](telemetry-polling.md) explains how stored
span sequences are consumed by the Colony. User-facing setup lives in
[OTLP ingestion](../user/instrumentation.md).

## Design history

- [RFD 024: OTEL Integration](../../RFDs/024-otel-integration.md)
- [RFD 025: Basic Ingestion](../../RFDs/025-basic-otel-ingestion.md)
- [RFD 034: Serverless Forwarding](../../RFDs/034-serverless-otlp-forwarding.md)
