# Providers Development

## Architecture

### Direct SDK Integration

Each provider is implemented directly using its native SDK.

This provides:

- **Full Control**: Direct access to provider-specific features
- **Better Performance**: No wrapper overhead
- **Native Tool Calling**: Direct integration with each provider's function
  calling API
- **Simpler Debugging**: Clearer error messages and stack traces

### Provider Interface

All providers implement a simple `Provider` interface defined in
`internal/llm/provider.go`:

```go
type Provider interface {
    Name() string
    Generate(ctx context.Context, req GenerateRequest, streamCallback StreamCallback) (*GenerateResponse, error)
}
```

Each provider is responsible for:

1. Converting MCP tools to its native function calling format
2. Handling streaming responses
3. Managing conversation history
4. Converting tool calls back to a standard format

## Adding a New Provider

To implement a new provider, follow these steps:

### 1. Create Provider Implementation

Create a new file in `internal/llm/` (e.g., `openai.go`):

```go
package llm

import (
    "context"
    "github.com/mark3labs/mcp-go/mcp"
)

type OpenAIProvider struct {
    client *openai.Client
    model  string
}

func NewOpenAIProvider(ctx context.Context, apiKey string, modelName string) (*OpenAIProvider, error) {
    // Initialize SDK client
}

func (p *OpenAIProvider) Name() string {
    return "openai"
}

func (p *OpenAIProvider) Generate(ctx context.Context, req GenerateRequest, streamCallback StreamCallback) (*GenerateResponse, error) {
    // 1. Convert MCP tools to provider's tool format
    // 2. Call provider API with messages and tools
    // 3. Handle streaming if enabled
    // 4. Convert response back to standard format
    // 5. Return GenerateResponse with tool calls
}
```

### 2. Add to Agent Factory

Update `internal/agent/ask/agent.go` in the `createProvider` function:

```go
func createProvider(ctx context.Context, providerName string, modelID string, cfg *config.AskConfig, debug bool) (llm.Provider, error) {
    switch providerName {
    case "google":
        // existing code...
    case "openai": // Add new case
        apiKey := cfg.APIKeys["openai"]
        if apiKey == "" {
            return nil, fmt.Errorf("OpenAI API key not configured")
        }
    return llm.NewOpenAIProvider(ctx, apiKey, modelID)
// ...
}
```

### 3. Update Validation

Update `internal/cli/ask/ask.go` to remove or update any provider-specific
validation logic.

### 4. Test

```bash
export OPENAI_API_KEY=sk-...
coral ask "test question" --model openai:gpt-4o-mini
```

---

## Related Files

- **Provider interface:** `internal/llm/provider.go`
- **Google implementation:** `internal/llm/google.go`
- **Agent initialization:** `internal/agent/ask/agent.go`
- **CLI command:** `internal/cli/ask/ask.go`
- **Configuration:** `internal/config/ask.go`
- **User documentation:** `docs/CONFIG.md`
- **Design document:** `RFDs/030-coral-ask-local.md`

---

## Testing Provider Support

### Testing Google Provider

```bash
# Set API key
export GOOGLE_API_KEY=your-key-here

# Test with a simple query
coral ask "list all services" --model google:gemini-3-fast

# Test streaming
coral ask "what is the current status?" --model google:gemini-3-fast

# Test multi-turn conversation
coral ask "show me HTTP latency"
coral ask "what are the slowest endpoints?" --continue

# Test with debug output
coral ask "analyze error rates" --debug
```

### Testing OpenAI Provider

```bash
# Set API key
export OPENAI_API_KEY=your-key-here

# Test with a simple query
coral ask "list all services" --model openai:gpt-4o-mini

# Test with high quality model
coral ask "analyze error rates" --model openai:gpt-4o

# Test with debug output
coral ask "what is the current status?" --model openai:gpt-4o-mini --debug
```

### Troubleshooting

If queries fail, check:

1. **API Key**: Ensure `GOOGLE_API_KEY` or `OPENAI_API_KEY` is set correctly
2. **Model Format**: Use format `provider:model-id` (e.g.,
   `google:gemini-3-fast`, `openai:gpt-4o-mini`)
3. **Colony Running**: Colony MCP server must be running (`coral colony list`)
4. **Network**: Ensure you can reach the provider's API endpoints
5. **Debug Mode**: Use `--debug` flag to see detailed error messages

### Common Errors

**"Google AI API key not configured"**

- Solution: Set `GOOGLE_API_KEY` environment variable

**"OpenAI API key not configured"**

- Solution: Set `OPENAI_API_KEY` environment variable

**"failed to connect to colony MCP server"**

- Solution: Start colony with `coral colony start` or check `coral colony list`

**"MCP client connection timed out"**

- Solution: Ensure colony is running and responsive
