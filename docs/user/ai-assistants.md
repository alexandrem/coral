# AI Assistants

Coral's AI clients combine model reasoning with queries and operational tools.
The model runs through your selected provider; the Colony supplies observations
and debugging operations. Configure ordinary [CLI query access](queries.md)
first so model failures and Colony connectivity failures can be diagnosed
separately.

## Configure the built-in client

```bash
coral ask config
coral ask config show
coral ask config validate
coral ask list-providers --models
```

The wizard saves global settings in `~/.coral/config.yaml`, or Colony-specific
settings when given `--colony`. Providers implemented in this checkout include
Anthropic, Google, OpenAI, and Ollama. The provider/model catalog describes
Coral's integration, not a guarantee that an account can access every listed
model. Use a model available through your provider account.

For example, this global configuration fragment references an environment key:

```yaml
ai:
  ask:
    default_model: anthropic:claude-sonnet-4-6
    api_keys:
      anthropic: env://ANTHROPIC_API_KEY
```

Set the key in the environment that runs the CLI. Keep API keys out of project
files and source control. Per-query `--model provider:model` overrides the
configured model. Provider keys, model settings, and optional base URLs are
documented in the [configuration reference](configuration-reference.md).

The common provider key variables are `ANTHROPIC_API_KEY`, `GOOGLE_API_KEY`,
and `OPENAI_API_KEY`. Configure the provider you intend to use, then validate
from the same account and environment that will run the client. A provider
model list describes Coral's catalog; account access and tool-calling support
still depend on the chosen model and endpoint.

To select a model for one environment, use the wizard's `--colony` option or
place an `ask` fragment at the top level of that Colony's configuration:

```yaml
ask:
  default_model: anthropic:claude-sonnet-4-6
  conversation:
    max_turns: 10
    context_window: 8192
```

The global file uses `ai.ask`; the Colony file uses `ask`. Changing model
settings does not change the Colony selected for data access. Retained
conversation turns and context limits also affect how much earlier evidence
is available to a later model call.

## Use a local Ollama model

Run an Ollama service with an installed model that supports the tool calls
needed for your investigation. Select that model with the `ollama:` provider
prefix; replace `my-local-model` with its installed name:

```bash
coral ask "Inspect api latency during the last 10 minutes" --model ollama:my-local-model
```

Coral defaults to `http://localhost:11434/v1`. Set `OLLAMA_HOST` when the service
runs at another address. The provider also accepts a persistent URL in
`ai.ask.api_keys.ollama`; despite the field name, its value for Ollama is an
endpoint, not a secret:

```yaml
ai:
  ask:
    default_model: ollama:my-local-model
    api_keys:
      ollama: http://127.0.0.1:11434
```

The configured URL takes precedence over `OLLAMA_HOST`; Coral adds `/v1` when
needed. No cloud provider key is required for this integration. Verify that
the selected service and model are reachable before investigating Colony access.

## Ask a bounded question

```bash
coral ask "Inspect api errors and latency during the last 10 minutes"
coral ask --continue "Compare those results with payments"
coral ask
```

The last form starts an interactive conversation. `--resume` selects a saved
conversation; `--debug` shows more diagnostic information on stderr.
`--dry-run` shows the constructed model payload before sending it. Inspect
such output as potentially sensitive because it can contain system context.

Model responses are interpretations of collected evidence. Check the invoked
commands and their results before treating a hypothesis as an established
cause. Debugging and execution tools can affect a running system; the client
has the access of its local process and supplied credentials. An LLM provider
receives the prompts and tool results sent to it. Using a local Ollama endpoint
keeps model calls on that endpoint; it does not configure an otherwise offline
mesh or download models for you.

## Use the terminal

```bash
coral terminal
coral terminal --colony '<colony-id>' --auto-browser
```

The terminal combines an interactive investigation with session output and
can display supported rendered script results in a browser dashboard.
`--auto-browser` opens it when a render event first occurs. Check underlying
query access if the terminal starts but receives no observations.

## Connect an external MCP client

Run the stdio proxy on the same machine as the external assistant. Generate a
configuration snippet:

```bash
coral mcp configure
```

Add the resulting server entry to your client's MCP settings. Use an absolute
binary path if the client cannot resolve your shell's `PATH`. An example entry
with an explicit Colony is:

```json
{
  "mcpServers": {
    "coral": {
      "command": "/absolute/path/to/coral",
      "args": ["mcp", "proxy", "--colony", "<colony-id>"]
    }
  }
}
```

Supply the same configuration directory, trust configuration, and
`CORAL_API_TOKEN` that make a normal CLI query succeed. Desktop applications
may not inherit terminal environment variables; use the client's environment
configuration when needed. Restart or reconnect the MCP client after editing
its settings. The client launches and owns the proxy process.

The local proxy exposes `coral_cli`, which accepts a command argument array:

```json
{"args": ["query", "traces", "api", "--since", "10m"]}
```

The proxy executes a local Coral subprocess and adds `--format json` when
needed. Commands differ in format support; a command without JSON output is
not made JSON-capable by the proxy. `coral mcp list-tools` inspects the running
Colony's tool registry, which is distinct from the local proxy's `coral_cli`
interface. Keep stdio free for protocol traffic; proxy logs use stderr.

For multiple environments, configure separate entries with explicit Colony
selection and credentials. Verify which environment a client uses before
requesting a live probe or a remote command.

## Use an OpenAI-compatible endpoint

The OpenAI provider accepts a configured base URL. This fragment routes that
provider to a compatible server:

```yaml
ai:
  ask:
    default_model: openai:my-model
    api_keys:
      openai: env://OPENAI_API_KEY
    base_urls:
      openai: https://inference.example.com/v1
```

`base_urls.openai` takes precedence over `OPENAI_BASE_URL`, then the provider's
default endpoint. Compatibility depends on the server's model, chat, and tool
calling support; do not assume every vendor-specific authentication or request
format works merely because it offers an HTTP API.

If tools fail, reproduce the corresponding CLI command outside the assistant.
If model validation fails, check the provider key, base URL, and requested model.
See [troubleshooting](troubleshooting.md#ai-or-mcp-cannot-query-the-colony) and
[MCP and LLM architecture](../architecture/mcp-and-llm.md) for further diagnosis.
