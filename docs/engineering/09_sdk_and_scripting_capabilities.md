# SDK and Scripting Capabilities

Coral provides two distinct SDK tiers: a **Go SDK** for application-level
observability enrichment and a **TypeScript SDK** for automated analysis and
scripting.

## Go SDK: Application Enrichment (`pkg/sdk`)

The Go SDK is designed to be embedded directly into target applications to
facilitate deep observability.

### Runtime Monitoring

Unlike pure eBPF which treats the binary as a black box, the SDK allows the
application to cooperate with the agent.

- **`EnableRuntimeMonitoring()`**: Starts an internal HTTP debug server (default
  `:9002`).
- **Function Metadata**: Automatically discovers and exposes function symbols,
  entry offsets, and titles. The eBPF agent queries this endpoint to accurately
  attach probes to Go functions, even in binaries with stripped symbols or
  complex layouts.
- **Zero-Config Discovery**: By enabling this, the agent can "auto-discover"
  which functions are worth monitoring without manual configuration of offsets.

## TypeScript SDK: Automated Analysis (`pkg/sdk/typescript`)

The TypeScript SDK enables "Observability-as-Code." It allows SREs and
developers to write scripts that query, correlate, and act upon telemetry data.

### Sandboxed Execution (`coral run`)

Scripts are executed via the Coral CLI using a **Deno-based sandbox** (RFD 076).

- **Security**: The sandbox restricts the script to only communicate with the
  Colony API. It has no write access to the filesystem and cannot execute
  arbitrary shell commands.
- **Zero-Install**: The SDK is bundled with the CLI; scripts can simply import
  `coral` and run immediately.

### Key Scripting APIs

- **`coral.services`**: List discovered services, instance counts, and
  namespaces.
- **`coral.metrics`**: High-level API for querying latency percentiles (P50,
  P95, P99) over specific time windows.
- **`coral.db`**: Raw SQL access to the Colony's central DuckDB. This is the
  most powerful tool for custom analysis, allowing complex JOINs across metrics,
  traces, and service metadata.

## Use Cases for Scripting

1. **Automated Health Checks**: Scripts that run in CI/CD or as k8s CronJobs to
   verify P99 latency regressions after a deployment.
2. **Custom Dashboards**: Generating Markdown or JSON reports for service health
   that combine metrics from multiple sources.
3. **Drift Detection**: Correlating latency spikes across different services to
   identify the root cause in a distributed call chain.
4. **SRE Automation**: Automatically detaching "forgotten" eBPF probes or
   cleaning up old debug sessions based on custom logic.

## Engineering Note: SDK-to-Colony Communication

The TypeScript SDK communicates with the Colony via **Connect RPC over HTTP/JSON
**. This choice ensures that scripts remain lightweight and can run in any
environment where the CLI can reach the Colony API, while maintaining type
safety through generated TypeScript interfaces.

## Related Design Documents (RFDs)

- [**RFD 060
  **: SDK Runtime Monitoring](../../RFDs/060-sdk-runtime-monitoring.md)
- [**RFD 066**: SDK HTTP API](../../RFDs/066-sdk-http-api.md)
- [**RFD 076
  **: Sandboxed TypeScript Execution](../../RFDs/076-sandboxed-typescript-execution.md)
- [**RFD 093**: Skills TypeScript SDK](../../RFDs/093-skills-typescript-sdk.md)
