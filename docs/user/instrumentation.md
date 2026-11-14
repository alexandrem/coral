# Application Instrumentation

Automatic eBPF observation, application OpenTelemetry, and the Coral Go runtime
SDK provide different evidence. You can start with automatic observation, add
OpenTelemetry for application-defined spans, and expose runtime metadata when
function or heap investigation needs it. None of these integrations requires
configuring an LLM.

| Evidence needed | Setup |
| --- | --- |
| Supported protocol traffic without application changes | [Automatic observation](services.md#control-automatic-naming-and-observation) on a Linux agent |
| Application-defined spans | [OpenTelemetry exporter and agent receiver](#send-opentelemetry-data) |
| Go function metadata or a runtime heap endpoint | [Coral Go SDK](#expose-go-runtime-metadata) |

## Send OpenTelemetry data

Start with a [running, enrolled agent](getting-started.md#enroll-and-start-an-agent)
and an application whose tracing SDK initializes an OTLP exporter. Configure
the receiver on the agent host and the exporter in the application's launch
environment; they are separate settings.

### Choose the agent receiver address

Default application receivers are gRPC on 4317 and HTTP/protobuf on 4318.
`127.0.0.1` works when the application and agent share a network namespace.
Separate containers may need a reachable agent hostname and a non-loopback bind.

To restrict the receiver to local applications, merge this fragment into the
agent's startup file. It retains the default trace filtering policy:

```yaml
# /etc/coral/agent.yaml (agent settings fragment)
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

### Configure the application's exporter

Choose one protocol below. Set the variables in the environment that starts
the application, then restart it using its normal launcher. These examples
assume the shared network namespace described above.

For gRPC:

```bash
export OTEL_SERVICE_NAME=api
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4317
export OTEL_EXPORTER_OTLP_PROTOCOL=grpc
```

For HTTP/protobuf:

```bash
export OTEL_SERVICE_NAME=api
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
```

The HTTP paths are `/v1/traces` and `/v1/metrics`; signal-specific endpoint
variables may require the full path, depending on the exporter. Use an exporter
SDK that reads these variables; exporting them alone does not initialize tracing.
See the [runnable Go OTEL application](../../examples/otel-go-app/README.md)
for exporter setup, resource attributes, and request generation.

### Generate traffic and verify ingestion

Send requests through the application, then run on a configured operator CLI:

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

On the agent host, register the service with a watch:

```bash
coral services watch api:8080
```

From a configured operator CLI, search for a function in your application:

```bash
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

CPU stack collection depends on unwind support and architecture. If stack
samples are empty, check the actual binary and the collector's output; build tags
such as `framepointer` do not change compiler behavior on their own. See
[profiling](profiling.md) for collection requirements.
