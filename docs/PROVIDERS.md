# LLM Provider Support for Coral Ask

This document provides technical details about LLM provider support for the
`coral ask` command, including current status and implementation plans.

## Provider Support Matrix

| Provider      | Status      | MCP Tool Support | Integration Method   | API Key Required | Local/Cloud | Notes                                |
|---------------|-------------|------------------|----------------------|------------------|-------------|--------------------------------------|
| **Google**    | ✅ Supported | ✅ Full           | Direct SDK (`genai`) | Yes              | Cloud       | Currently supported, all models work |
| **OpenAI**    | 🚧 Planned  | ⚠️ Pending       | Direct SDK (TODO)    | Yes              | Cloud       | Implementation needed                |
| **Anthropic** | 🚧 Planned  | ⚠️ Pending       | Direct SDK (TODO)    | Yes              | Cloud       | Native MCP support possible          |
| **Ollama**    | 🚧 Planned  | ⚠️ Pending       | Direct SDK (TODO)    | No               | Local       | Best for air-gapped/offline          |
| **Grok**      | 🚧 Planned  | ⚠️ Pending       | Direct SDK (TODO)    | Yes              | Cloud       | Implementation needed                |

### Quick Recommendations

**Currently Supported (Google only):**

- **Fast/Cost-effective**: `google:gemini-2.0-flash-exp` - Experimental, fast
  responses
- **Production Quality**: `google:gemini-1.5-pro` - Stable, long context window
- **Balanced**: `google:gemini-1.5-flash` - Good balance of speed and quality

**Coming Soon:**

- OpenAI (`gpt-4o`, `gpt-4o-mini`) - Implementation planned
- Anthropic (`claude-3-5-sonnet-20241022`) - Native MCP support possible
- Ollama (local models) - For air-gapped/offline deployments

---

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
`internal/agent/llm/provider.go`:

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

---

## Current Status

### Google (Gemini) - ✅ Fully Supported

**Implementation**: `internal/agent/llm/google.go`
**SDK**: `github.com/google/generative-ai-go/genai`
**Status**: ✅ Production-ready

The Google provider uses the official Gemini SDK and supports:

- Full MCP tool calling integration
- Streaming responses
- Multi-turn conversations
- All Gemini models (1.5 Flash, 1.5 Pro, 2.0 Flash Exp)

**Tool Integration**: MCP tools are converted to Gemini `FunctionDeclaration`
format using JSON schema transformation.

### Other Providers - 🚧 Not Yet Implemented

The following providers are planned but not yet implemented:

#### OpenAI - 🚧 Planned

**Estimated Effort**: Medium
**SDK**: `github.com/openai/openai-go` or similar
**Tool Format**: OpenAI function calling API

Would provide access to:

- GPT-4o (high quality reasoning)
- GPT-4o-mini (cost-effective)
- GPT-4 Turbo

#### Anthropic - 🚧 Planned

**Estimated Effort**: Medium
**SDK**: `github.com/anthropics/anthropic-sdk-go`
**Tool Format**: Anthropic tool use API

Anthropic's Claude models have excellent reasoning capabilities for debugging:

- Claude 3.5 Sonnet (best reasoning)
- Native tool calling support
- Extended thinking mode available
- Prompt caching for efficiency

#### Ollama - 🚧 Planned

**Estimated Effort**: Medium
**SDK**: HTTP API or community SDK
**Tool Format**: Ollama tool calling

Critical for:

- Air-gapped deployments
- Offline development
- Local testing without API costs

Models: llama3.2, mistral, codellama, etc.

#### Grok (xAI) - 🚧 Planned

**Estimated Effort**: Medium-High
**SDK**: OpenAI-compatible API
**Tool Format**: Function calling (if supported)

**Note**: Need to verify Grok's current tool calling support. Earlier
limitations may have been resolved

---

## Google Provider Details

### Supported Models

- `google:gemini-2.0-flash-exp` - Gemini 2.0 Flash (experimental, fastest)
- `google:gemini-1.5-pro` - Gemini 1.5 Pro (long context, most capable)
- `google:gemini-1.5-flash` - Gemini 1.5 Flash (balanced)

### Configuration

```yaml
ai:
    ask:
        default_model: "google:gemini-2.0-flash-exp"
        api_keys:
            google: "env://GOOGLE_API_KEY"
```

### Getting a Google AI API Key

1. Visit [Google AI Studio](https://makersuite.google.com/app/apikey)
2. Create a new API key
3. Set the environment variable:

```bash
export GOOGLE_API_KEY=your-api-key-here
```

### Implementation Notes

The Google provider (`internal/agent/llm/google.go`) implements:

- **Tool Conversion**: JSON Schema → Gemini `FunctionDeclaration`
- **Streaming**: Chunks are streamed via callback as they arrive
- **Conversation History**: Managed via Gemini chat sessions
- **Error Handling**: Proper error propagation from Gemini API

---

## Recommendations

### Current (Google Only)

**For Production:**

- Use `google:gemini-1.5-pro` for best quality and stability
- Use `google:gemini-1.5-flash` for faster responses at lower cost

**For Development/Testing:**

- Use `google:gemini-2.0-flash-exp` for fastest iteration
- Experimental models may have occasional issues

**For Complex Analysis:**

- Use `google:gemini-1.5-pro` for its long context window (up to 2M tokens)
- Helpful for analyzing large traces or log files

### Implementation Priorities

1. **OpenAI Provider** (Priority: High)
    - Most requested alternative to Google
    - GPT-4o has excellent reasoning capabilities
    - GPT-4o-mini is cost-effective for production

2. **Anthropic Provider** (Priority: High)
    - Claude 3.5 Sonnet has best-in-class reasoning
    - Native tool calling support available
    - Extended thinking mode for complex problems

3. **Ollama Provider** (Priority: Medium)
    - Critical for air-gapped deployments
    - Enables offline development and testing
    - No API costs for local inference

4. **Grok Provider** (Priority: Low)
    - Verify tool calling support status
    - Evaluate quality vs. other providers
    - Consider if effort is justified

---

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
