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
