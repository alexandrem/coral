# Providers Development

## Adding a New Provider

To implement a new provider, follow these steps:

### 1. Create Provider Implementation

Create a new file in `internal/agent/llm/` (e.g., `openai.go`):

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

- **Provider interface:** `internal/agent/llm/provider.go`
- **Google implementation:** `internal/agent/llm/google.go`
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
coral ask "list all services" --model google:gemini-2.0-flash-exp

# Test streaming
coral ask "what is the current status?" --model google:gemini-1.5-flash

# Test multi-turn conversation
coral ask "show me HTTP latency"
coral ask "what are the slowest endpoints?" --continue

# Test with debug output
coral ask "analyze error rates" --debug
```

### Troubleshooting

If queries fail, check:

1. **API Key**: Ensure `GOOGLE_API_KEY` is set correctly
2. **Model Format**: Use format `provider:model-id` (e.g.,
   `google:gemini-2.0-flash-exp`)
3. **Colony Running**: Colony MCP server must be running (`coral colony list`)
4. **Network**: Ensure you can reach Google AI API endpoints
5. **Debug Mode**: Use `--debug` flag to see detailed error messages

### Common Errors

**"Google AI API key not configured"**

- Solution: Set `GOOGLE_API_KEY` environment variable

**"unsupported provider: openai"**

- Solution: Provider not yet implemented, use `google:` models for now

**"failed to connect to colony MCP server"**

- Solution: Start colony with `coral colony start` or check `coral colony list`

**"MCP client connection timed out"**

- Solution: Ensure colony is running and responsive
