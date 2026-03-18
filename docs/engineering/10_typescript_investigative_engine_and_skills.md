# TypeScript Investigative Engine and Skills

The **TypeScript SDK** and its embedded execution engine represent the
"Reasoning Engine" of the Coral platform. While traditional monitoring tools
provide static dashboards, Coral enables autonomous, code-driven investigations
tailored for LLM agents.

## The "Escape Hatch" for Autonomous Diagnostics

Standard observability primitives (metrics, traces, logs) often fail to capture
the nuances of complex, cross-service anomalies. The TypeScript Investigative
Engine provides an "escape hatch" that allows an LLM to:

- **Correlate Data**: Perform complex JOINS across eBPF-derived traces, system
  metrics, and service metadata using the underlying DuckDB backbone.
- **Dynamic Introspection**: Write and execute one-off logic to test a
  hypothesis (e.g., "Find all services whose memory grew by >20% exactly 5
  minutes after a spike in TCP retransmissions").
- **Closed-Loop Reasoning**: Execute logic, process the result, and iterate on
  the next step of the investigation without human intervention.

## Execution Models

### 1. LLM Investigator (`coral_run` MCP Tool)

This is the primary architectural driver for the engine. The LLM generates and
executes **inline TypeScript** directly via the Model Context Protocol. This
enables a conversational investigation where the model can "write its own
tools" on the fly.

### 2. Human Operator (`coral run`)

SREs can execute pre-written scripts stored on disk. This mode is typically used
for scheduled health checks, CI/CD regression testing, or complex cleanup tasks.

- **Security**: Both models share the same **Deno-based sandbox** (RFD 076).
  The sandbox restricts communication to the Colony API, prevents filesystem
  writes, and forbids arbitrary shell execution.
- **Zero-Install**: The SDK is bundled with the CLI; scripts (and the LLM) can
  simply import `@coral/sdk` and run immediately.

## Skills: LLM-Invocable Recipes

The SDK includes a library of **Skills** (located in `@coral/sdk/skills/*`).
These are pre-authored investigation patterns that the LLM can import to avoid
"reinventing the wheel" for common diagnostic tasks.

### The `SkillResult` Contract

All skills follow a structured output contract designed for LLM consumption:

- **`summary`**: A one-sentence finding (e.g., "Memory leak detected in
  `auth-svc`").
- **`status`**: A severity flag (`healthy`, `warning`, `critical`, `unknown`).
- **`data`**: The raw, structured telemetry supporting the finding.
- **`recommendations`**: A list of suggested next steps (e.g., "Run the
  `heap-dump` skill on this instance").

### Built-in Skills

- **`latency-report`**: Scans the fleet for P99 regressions and error spikes.
- **`error-correlation`**: Detects cascading failures by finding simultaneous
  error-rate spikes across the topology.
- **`memory-leak-detector`**: Identifies services with monotonically increasing
  heap growth sorted by velocity.

## Engineering Note: SDK-to-Colony Communication

The TypeScript SDK communicates with the Colony via **Connect RPC over HTTP/JSON**.
This ensures that scripts remain lightweight and can run in any environment
where the CLI can reach the Colony API, while maintaining strict type safety
through generated TypeScript interfaces.

## Future Engineering Notes

- **Community Skills Marketplace**: Develop a registry where users can publish
  and discover community-contributed investigative skills. This would allow an
  `error-correlation` skill optimized for specific stacks (e.g., PostgreSQL or
  Redis) to be shared globally.
- **TUI Dashboard Renderer**: Create a client-side rendering layer in the CLI
  that can consume a `SkillResult.data` payload and render an interactive,
  live-updating TUI dashboard for the human operator.
- **Streaming Partial Results**: For long-running investigations, enable the
  SDK to stream partial `SkillResult` updates back to the LLM as they are
  generated, rather than waiting for the entire script to complete.

## Related Design Documents (RFDs)

- [**RFD 076**: Sandboxed TypeScript Execution](../../RFDs/076-sandboxed-typescript-execution.md)
- [**RFD 093**: Skills TypeScript SDK](../../RFDs/093-skills-typescript-sdk.md)
