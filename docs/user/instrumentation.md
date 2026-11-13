# Application Instrumentation

Automatic eBPF observation, application OpenTelemetry, and the Coral Go runtime
SDK provide different evidence. You can start with automatic observation, add
OpenTelemetry for application-defined spans, and expose runtime metadata when
function or heap investigation needs it. None of these integrations requires
configuring an LLM.

## Send OpenTelemetry data

Point an application's existing OTLP exporter at its agent. Default application
receivers are gRPC on 4317 and HTTP/protobuf on 4318. Set a distinct service
name so observations can be grouped correctly:

```bash
export OTEL_SERVICE_NAME=api
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4317
export OTEL_EXPORTER_OTLP_PROTOCOL=grpc
```

Use an exporter SDK that reads these variables and initializes tracing in your
application. For HTTP/protobuf exporters, use the HTTP receiver instead:

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
```

The HTTP paths are `/v1/traces` and `/v1/metrics`; signal-specific endpoint
variables may require the full path, depending on the exporter. `127.0.0.1`
works only when the application can reach the receiver in that network
namespace. Separate containers may need a reachable agent hostname instead.
See the [runnable Go OTEL application](../../examples/otel-go-app/README.md)
for exporter setup, resource attributes, and request generation.

In agent configuration, receiver bind addresses and capture filters can be
set independently. This fragment keeps local application access and the
default trace filtering policy:

```yaml
telemetry:
  grpc_endpoint: 127.0.0.1:4317
  http_endpoint: 127.0.0.1:4318
  filters:
    always_capture_errors: true
    high_latency_threshold_ms: 500
    sample_rate: 0.1
```

Adjust bind addresses to the deployment and restart the agent. Restrict
receiver reachability to intended applications; OTLP ingestion is not the
public operator API and does not inherit its token policy. Ports 4319/4320
belong to the internal managed Beyla receiver; do not point application
exporters at them.

After generating traffic, verify:

```bash
coral query traces api --source telemetry --since 10m
coral query metrics api --source telemetry --since 10m
```

Errors and slow spans can be retained while other spans are sampled. Trace
attributes and resource naming affect what appears in a query. Standard OTLP
metrics enter a bounded in-memory buffer; receipt of a custom metric is not a
promise that it becomes a persisted, queryable protocol series. Beyla has a
separate transformation and persistence path. If traces arrive but expected
metrics do not, inspect the source and supported metric mapping before changing
the network setup. The [OTLP architecture](../architecture/otlp-ingestion.md)
explains that distinction.

## Expose Go runtime metadata

Add the Coral Go SDK when the agent needs an application's metadata or heap
endpoint. In the application's startup path:

```go
import "github.com/coral-mesh/coral/pkg/sdk"

// Handle this error according to your application's startup policy.
err := sdk.EnableRuntimeMonitoring(sdk.Options{
    DebugAddr: "127.0.0.1:9002",
})
```

This is a fragment, not a complete program. The [Go SDK example](../../examples/sdk-go/README.md)
contains a runnable service. Bind to an address the agent can reach, keeping
runtime metadata within a trusted network boundary. A loopback bind works for
an agent in the same network namespace; a separate agent container may need a
different bind address and network policy.

Register the service with a watch and inspect available function metadata:

```bash
coral services watch api:8080
coral debug search ProcessOrder --service api
```

The runtime endpoint and the application's service port serve different
purposes. If discovery fails, inspect agent logs for the resolved process and
SDK address rather than sending application requests to port 9002.

## Preserve the metadata the investigation needs

Ordinary `go build` retains debug information. Removing DWARF with `-w` or
symbols with `-s` reduces what scanners and metadata providers can discover.
The SDK reads available binary/runtime metadata; it does not restore complete
argument, source, and function-size information from an arbitrary stripped
binary. Verify the exact release artifact, not just a local development build.

Inlining can remove a standalone target even when its source exists. For
development diagnosis, a build using `-gcflags='all=-N -l'` disables optimization
and inlining, but changes application performance; use it deliberately. For one
small function you control, `//go:noinline` is a narrower option. Follow
[live debugging](live-debugging.md) for attachment and duration limitations.

CPU stack collection depends on unwind support and architecture. An arbitrary
Go build tag such as `framepointer` does not itself change compiler behavior
unless the application or toolchain defines that behavior. Check the actual
binary and collector output when stack samples are empty, rather than assuming
a tag fixes them. See [profiling](profiling.md) for collection requirements.
