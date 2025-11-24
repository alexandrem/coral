# LLM Provider Support for Coral Ask

This document provides technical details about LLM provider support for the
`coral ask` command, including current limitations and future implementation
plans.

## Provider Support Matrix

| Provider      | Status           | MCP Tool Support | Integration Method   | API Key Required | Local/Cloud | Notes                                         |
|---------------|------------------|------------------|----------------------|------------------|-------------|-----------------------------------------------|
| **OpenAI**    | ✅ Supported      | ✅ Full           | Genkit `compat_oai`  | Yes              | Cloud       | Recommended for production                    |
| **Google**    | ✅ Supported      | ✅ Full           | Native Genkit plugin | Yes              | Cloud       | Good alternative, experimental models         |
| **Ollama**    | ✅ Supported      | ✅ Full           | Native Genkit plugin | No               | Local       | Best for air-gapped/offline deployments       |
| **Grok**      | ⚠️ Not Supported | ❌ None           | Genkit `compat_oai`  | Yes              | Cloud       | Grammar constraint limitation (see below)     |
| **Anthropic** | ⚠️ Not Supported | ❌ None           | Genkit `compat_oai`  | Yes              | Cloud       | Wrapper doesn't expose native MCP (see below) |

### Quick Recommendations

**Production (Cloud):**

- Primary: `openai:gpt-4o-mini` (cost-effective, reliable)
- Alternative: `google:gemini-2.0-flash-exp` (fast, experimental)

**Production (High Quality):**

- Primary: `openai:gpt-4o` (most capable)
- Alternative: `google:gemini-1.5-pro` (long context)

**Development/Testing:**

- Local: `ollama:llama3.2` (no API key, runs locally)
- Cloud: `openai:gpt-4o-mini` (cheap, fast)

**Air-gapped/Offline:**

- Only: `ollama:llama3.2` or other Ollama models

---

## Current Limitations

### Grok (xAI) - Not Supported

**Issue:** Grok's API rejects structured output/grammar constraints

**Root Cause:**

- openai-go library v1.8.2 automatically adds grammar constraints when tools are
  present
- Grok API responds with `400 Bad Request: Invalid grammar request`
- The JSON schema generated has validation issues (`"type":""` empty fields)

**Technical Details:**

```
Error: failed to create completion: POST "https://api.x.ai/v1/chat/completions": 400 Bad Request
"Invalid grammar request: req.grammar_key=('structural_pattern', '{\"begin\":\"<function_call>\"...
```

**Resolution:** Command fails fast with clear error message directing users to
supported providers.

**Potential Future Solutions:**

1. xAI adds grammar constraint support to Grok API
2. openai-go library adds option to disable grammar constraints
3. We implement custom Grok provider without grammar constraints

**Related Code:**

- Error handling: `internal/cli/ask/ask.go:130-132`
- Provider initialization: `internal/agent/genkit/agent.go:136-167`

---

### Anthropic (Claude) - Not Supported

**Issue:** Genkit's OpenAI-compatible wrapper doesn't integrate with Anthropic's
native MCP support

**Root Cause:**

- Anthropic models natively support MCP and tool calling
- Genkit uses `compat_oai` plugin which tries to wrap Anthropic as
  OpenAI-compatible
- This wrapper doesn't expose Anthropic's native tool calling capabilities
- MCP integration requires proper tool definition and execution flow

**Why This Matters:**
Anthropic's Claude models (especially Claude 3.5 Sonnet) have excellent
reasoning capabilities that would be valuable for complex debugging and root
cause analysis tasks. However, the current Genkit abstraction prevents us from
using these capabilities with MCP tools.

**Future Solution: Custom Anthropic MCP Provider**

We should consider implementing a native Anthropic provider that bypasses
Genkit's abstraction:

```go
// Future: internal/agent/anthropic/provider.go
package anthropic

import (
    "github.com/anthropics/anthropic-sdk-go"
    "github.com/mark3labs/mcp-go/client"
)

// AnthropicMCPProvider directly integrates Anthropic SDK with MCP.
type AnthropicMCPProvider struct {
    client    *anthropic.Client
    mcpClient *client.Client
}

// Ask implements native tool calling with Anthropic's API.
func (p *AnthropicMCPProvider) Ask(ctx context.Context, question string, tools []mcp.Tool) (*Response, error) {
    // Convert MCP tools to Anthropic tool format.
    anthropicTools := convertMCPToAnthropicTools(tools)

    // Use Anthropic's native tool calling.
    resp, err := p.client.Messages.Create(ctx, anthropic.MessageCreateParams{
        Model:    "claude-3-5-sonnet-20241022",
        Messages: []anthropic.Message{{Role: "user", Content: question}},
        Tools:    anthropicTools,
    })

    // Execute tool calls via MCP.
    for _, toolUse := range resp.ToolUses {
        result, err := p.mcpClient.CallTool(ctx, toolUse.Name, toolUse.Input)
        // ... handle result
    }

    return &Response{...}, nil
}
```

**Benefits of Custom Implementation:**

1. Direct access to Anthropic's superior reasoning capabilities
2. Native MCP support without wrapper limitations
3. Better error handling and debugging
4. Access to Anthropic-specific features (extended thinking, prompt caching)
5. Full control over conversation flow and context management

**Implementation Effort:** Medium

- Requires bypassing Genkit for Anthropic provider only
- Need to implement tool calling loop manually
- Must handle conversation state and context
- Testing with various MCP tool configurations
- Integration with existing agent interface

---

## Supported Providers (Implementation Details)

### OpenAI

**Integration:** Genkit `compat_oai` plugin
**Tool Calling:** Native OpenAI function calling API
**Status:** ✅ Fully supported

**Models:**

- `openai:gpt-4o` - Latest GPT-4 Omni (most capable)
- `openai:gpt-4o-mini` - Faster, cheaper variant (recommended)
- `openai:gpt-4-turbo` - GPT-4 Turbo

**Configuration:**

```yaml
ai:
    ask:
        default_model: "openai:gpt-4o-mini"
        api_keys:
            openai: "env://OPENAI_API_KEY"
```

---

### Google

**Integration:** Native Genkit `googlegenai` plugin
**Tool Calling:** Native Gemini function calling
**Status:** ✅ Fully supported

**Models:**

- `google:gemini-2.0-flash-exp` - Gemini 2.0 Flash (experimental, fast)
- `google:gemini-1.5-pro` - Gemini 1.5 Pro (long context, stable)
- `google:gemini-1.5-flash` - Gemini 1.5 Flash (cost-effective)

**Configuration:**

```yaml
ai:
    ask:
        default_model: "google:gemini-2.0-flash-exp"
        api_keys:
            google: "env://GOOGLE_API_KEY"
```

---

### Ollama

**Integration:** Native Genkit `ollama` plugin
**Tool Calling:** Ollama tool calling support
**Status:** ✅ Fully supported

**Models:**

- `ollama:llama3.2` - Meta's Llama 3.2
- `ollama:mistral` - Mistral AI model
- `ollama:codellama` - Code-specialized Llama

**Configuration:**

```yaml
ai:
    ask:
        default_model: "ollama:llama3.2"
        # No API key needed for Ollama
```

**Prerequisites:**

```bash
# Install Ollama
curl -fsSL https://ollama.ai/install.sh | sh

# Pull model
ollama pull llama3.2

# Verify Ollama is running
ollama list
```

---

## Recommendations

### Short-term (Current Implementation)

**Production Deployments:**

- Primary: `openai:gpt-4o-mini` - Best balance of cost, speed, and quality
- Fallback: `google:gemini-2.0-flash-exp` - Fast alternative

**High-Stakes Debugging:**

- Primary: `openai:gpt-4o` - Most capable reasoning
- Fallback: `google:gemini-1.5-pro` - Long context for complex traces

**Air-gapped/Offline:**

- Only: `ollama:llama3.2` or other Ollama models

### Long-term (Future Development)

1. **Implement Custom Anthropic MCP Provider** (Priority: High)
    - Would unlock Claude's superior reasoning for complex debugging
    - Native MCP integration without wrapper limitations

2. **Monitor Grok API Updates** (Priority: Low)
    - Watch for grammar constraint support in xAI API
    - Track openai-go library for configuration options
    - May become viable without custom implementation

3. **Evaluate Additional Providers** (Priority: Medium)
    - Mistral AI (native API, not just Ollama)
    - Cohere Command R+
    - Other providers with strong tool calling support

4. **Upstream Contributions** (Priority: Low)
    - Consider contributing grammar constraint fixes to openai-go
    - Improve Genkit's OpenAI-compatible wrapper
    - Document MCP integration patterns for other developers

---

## Related Files

- **Provider initialization:** `internal/agent/genkit/agent.go`
- **Model validation:** `internal/cli/ask/ask.go`
- **User documentation:** `docs/CONFIG.md`
- **Design document:** `RFDs/030-coral-ask-local.md`
- **Configuration:** `internal/config/ask.go`

---

## Testing Provider Support

To test if a provider works with your setup:

```bash
# Set API key
export OPENAI_API_KEY=sk-proj-your-key

# Test with a simple query
coral ask "list all services" --model openai:gpt-4o-mini

# Test with fallback
coral ask "what is the current status?" \
  --model openai:gpt-4o-mini

# Test multi-turn conversation
coral ask "show me HTTP latency"
coral ask "what are the slowest endpoints?" --continue
```

If the provider fails with MCP tool calling issues, check:

1. Provider is listed as ✅ Supported in the matrix above
2. API key is correctly configured in environment
3. Model ID format matches `provider:model-id`
4. Colony MCP server is running (`coral colony start`)
