# TypeScript Investigations

Use a script when an investigation needs several queries, comparisons, or a
repeatable report. `coral run` executes a local TypeScript file with Deno and
the `@coral/sdk` interface. The full CLI distribution includes a Deno companion;
the standalone agent binary does not run scripts.

## Prepare the runtime and connection

Run scripts from a directory containing their imports and a `deno.json` import
map. The file-based `coral run` path does not automatically install the SDK or
create this map. The repository's [script examples](../../examples/scripts/)
include one:

```bash
cd examples/scripts
coral run latency-report.ts
```

For a script at the repository root, use this `deno.json`:

```json
{
  "imports": {
    "@coral/sdk": "./pkg/sdk/typescript/mod.ts"
  }
}
```

Adjust the relative path if you copy the SDK and script elsewhere. Run from
their common directory: filesystem reads are restricted to the current tree.
The sandbox allows network calls to the resolved Colony host, and access to
`CORAL_COLONY_ADDR` and `CORAL_MODE`. It does not grant subprocess execution,
filesystem writes, or arbitrary environment access.

The current TypeScript client constructs an HTTP URL from the resolved
host/port and does not attach `CORAL_API_TOKEN`. Use a trusted, reachable
internal Colony endpoint for these scripts. Configuring the CLI for token-based
public HTTPS access does not give this SDK equivalent TLS or authentication
support. Keep the internal API within its intended access boundary.

## Write and verify a report

Save this as `inventory.ts` beside the import map:

```typescript
import * as coral from "@coral/sdk";

const services = await coral.services.list();
console.log(JSON.stringify({
  serviceCount: services.length,
  services: services.map((service) => ({
    name: service.name,
    instances: service.instanceCount,
  })),
}, null, 2));
```

```bash
coral run inventory.ts
coral run inventory.ts --watch
coral run inventory.ts --timeout 120 > inventory.json
```

Timeout is in seconds and defaults to 60. Watch mode reruns on file changes;
it is not a managed background scheduler. Redirecting stdout is done by the
calling shell, which can save output even though the script itself cannot
write files. Use stderr for progress messages when stdout must stay valid JSON.

The SDK service list derives from registered agent services and can differ
from the CLI's merged observed/watched inventory. Compare the intended scope
before interpreting an empty list. Add metric or trace queries using the
[SDK reference](sdk-reference.md), preserving the documented units and time
window. Catch failures per service when a report should continue after an
individual query fails, and make partial results visible in the output.

## Store reusable investigations

```bash
coral script write --name inventory --file inventory.ts
coral script list
coral run "$HOME/.coral/scripts/inventory.ts"
coral script remove --name inventory
```

Stored scripts are plain TypeScript under `~/.coral/scripts/`. Keep the import
map and SDK readable from the working directory used when running them. The
`run` command takes the filename positionally; it does not accept `--file`.
Removing a script removes the local file, not a previously deployed runtime
correlation.

## Stateful correlations

The SDK can deploy an agent-side descriptor that evaluates probe events and
triggers an action. This modifies the running investigation; sandboxed local
execution does not make every RPC read-only. Prepare a
[probe-capable watched service](live-debugging.md#prepare-a-target) and a real
function name before adapting this example:

```typescript
import * as coral from "@coral/sdk";

const result = await coral.correlation.deploy("api", {
  strategy: "percentile_alarm",
  source: { probe: "main.ProcessOrder" },
  field: "duration_ns",
  threshold: 500_000_000,
  percentile: 0.99,
  window: "30s",
  action: { kind: "cpu_profile", profileDurationMs: 10_000 },
  cooldownMs: 60_000,
});
if (!result.success) throw new Error(result.error ?? "Deployment failed");
console.log(JSON.stringify(result));
```

This requests a CPU profile when the duration percentile exceeds 500 ms, with
a 60-second cooldown between actions. Save the returned correlation ID, inspect
agent and Colony logs for firings, then remove it when the investigation ends:

```bash
coral debug correlations list --service api
coral debug correlations remove '<correlation-id>'
```

Descriptor behavior depends on attached probe events and available action
capabilities; returning from the deployment script does not prove a trigger
has fired. The maintained [latency-trap skill](../../pkg/sdk/typescript/skills/latency-trap.ts)
shows the same workflow packaged as a reusable investigation recipe.

`SkillResult` values can also include structured data, recommendations, and
render metadata for the terminal dashboard. These runtime investigation
recipes differ from the repository's documentation-authoring skills. See the
[SDK reference](sdk-reference.md) for the result contract and built-in
recipes, and [scripting architecture](../architecture/typescript-investigations.md)
for execution boundaries.
