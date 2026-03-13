# LLM Provider Support for Coral Ask

This document provides technical details about LLM provider support for the
`coral ask` command, including current status and implementation plans.

## Provider Support Matrix

| Provider      | Status      | MCP Tool Support | Integration Method   | API Key Required | Local/Cloud | Notes                                |
|---------------|-------------|------------------|----------------------|------------------|-------------|--------------------------------------|
| **Google**    | ✅ Supported | ✅ Full           | Direct SDK (`genai`) | Yes              | Cloud       | Currently supported, all models work |
| **OpenAI**    | ✅ Supported | ✅ Full           | Direct SDK (`openai-go`) | Yes          | Cloud       | OpenAI-compatible API support        |
| **Anthropic** | ✅ Supported | ✅ Full           | Direct SDK (`anthropic-sdk-go`) | Yes   | Cloud       | Claude Sonnet, Opus, Haiku           |
| **Ollama**    | ✅ Supported | ✅ Full           | OpenAI-compatible API (`openai-go`) | No  | Local       | Any locally-installed model          |
| **Grok**      | 🚧 Planned  | ⚠️ Pending       | Direct SDK (TODO)    | Yes              | Cloud       | Implementation needed                |

### Quick Recommendations

**Currently Supported:**

- **Anthropic**: `anthropic:claude-sonnet-4-6` - Best for everyday tasks (recommended)
- **Anthropic**: `anthropic:claude-opus-4-6` - Most capable for complex tasks
- **Google**: `google:gemini-2.0-flash` - Fast responses
- **OpenAI**: `openai:gpt-4o` - High quality reasoning
- **OpenAI**: `openai:gpt-4o-mini` - Fast, cost-effective
- **Ollama**: `ollama:llama3.2` - Local models (no API key, no data leaves your machine)

## Current Status

### Google (Gemini) - ✅ Fully Supported

**Implementation**: `internal/llm/google.go`
**SDK**: `github.com/google/generative-ai-go/genai`
**Status**: ✅ Production-ready

The Google provider uses the official Gemini SDK and supports:

- Full MCP tool calling integration
- Streaming responses
- Multi-turn conversations
- Gemini 3 Fast

**Tool Integration**: MCP tools are converted to Gemini `FunctionDeclaration`
format using JSON schema transformation.

### OpenAI - ✅ Fully Supported

**Implementation**: `internal/llm/openai.go`
**SDK**: `github.com/openai/openai-go`
**Status**: ✅ Production-ready

The OpenAI provider uses the official OpenAI Go SDK and supports:

- Full MCP tool calling integration
- Streaming responses
- Multi-turn conversations
- Any OpenAI-compatible API endpoint (configurable base URL)

**Supported Models:**

- `openai:gpt-4o` - GPT-4o (high quality reasoning)
- `openai:gpt-4o-mini` - GPT-4o-mini (fast, cost-effective)

### Anthropic - ✅ Fully Supported

**Implementation**: `internal/llm/anthropic.go`
**SDK**: `github.com/anthropics/anthropic-sdk-go`
**Status**: ✅ Production-ready

The Anthropic provider uses the official Anthropic Go SDK and supports:

- Full MCP tool calling integration
- Streaming responses
- Multi-turn conversations with tool call correlation
- System prompt support

**Supported Models:**

- `anthropic:claude-sonnet-4-6` - Best for everyday tasks (recommended)
- `anthropic:claude-opus-4-6` - Most capable for complex tasks
- `anthropic:claude-haiku-4-5-20251001` - Fastest and most compact
- `anthropic:claude-3-5-sonnet-20241022` - Previous generation balanced model

**Configuration:**

```yaml
ai:
    ask:
        default_model: "anthropic:claude-sonnet-4-6"
        api_keys:
            anthropic: "env://ANTHROPIC_API_KEY"
```

**Getting an Anthropic API Key:**

1. Visit [Anthropic Console](https://console.anthropic.com/settings/keys)
2. Create a new API key
3. Set the environment variable:

```bash
export ANTHROPIC_API_KEY=your-api-key-here
```

**Tool Integration**: MCP tools are converted to Anthropic's tool use format via
`ToolUnionParam`, passing the raw JSON Schema directly using `param.Override`.

---

### Ollama - ✅ Fully Supported

**Implementation**: `internal/llm/ollama.go`
**SDK**: OpenAI-compatible API via `openai-go` (Ollama exposes `/v1`)
**Status**: ✅ Production-ready

The Ollama provider runs entirely on your machine — no API key, no data sent to
the cloud. It wraps Ollama's OpenAI-compatible endpoint using the existing
OpenAI provider with a configurable base URL.

**Supported Models (any locally-installed model works):**

- `ollama:llama3.2` - Meta's Llama 3.2 (recommended)
- `ollama:llama3.1` - Meta's Llama 3.1
- `ollama:qwen2.5-coder` - Alibaba's code-focused model
- `ollama:mistral` - Mistral 7B
- `ollama:codellama` - Meta's Code Llama

**Configuration:**

```yaml
ai:
    ask:
        default_model: "ollama:llama3.2"
        api_keys:
            ollama: "http://localhost:11434"  # Optional: override base URL
```

**Base URL resolution** (highest to lowest precedence):

1. `api_keys.ollama` in coral config
2. `OLLAMA_HOST` environment variable
3. Default: `http://localhost:11434/v1`

**Getting started:**

```bash
# Install Ollama from https://ollama.com
ollama pull llama3.2
coral ask "why is my service slow?" --model ollama:llama3.2
```

**Tool Integration**: Passes through to OpenAI-compatible tool calling. Tool
support depends on the specific model; `llama3.2` and `qwen2.5-coder` have
solid tool calling support.

---

### Other Providers - 🚧 Not Yet Implemented

The following providers are planned but not yet implemented:

#### Grok (xAI) - 🚧 Planned

**Estimated Effort**: Medium-High
**SDK**: OpenAI-compatible API
**Tool Format**: Function calling (if supported)

**Note**: Need to verify Grok's current tool calling support. Earlier
limitations may have been resolved

---

## Google Provider Details

### Supported Models

- `google:gemini-3-fast` - Gemini 3 Fast (recommended)

### Configuration

```yaml
ai:
    ask:
        default_model: "google:gemini-3-fast"
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

The Google provider (`internal/llm/google.go`) implements:

- **Tool Conversion**: JSON Schema → Gemini `FunctionDeclaration`
- **Streaming**: Chunks are streamed via callback as they arrive
- **Conversation History**: Managed via Gemini chat sessions
- **Error Handling**: Proper error propagation from Gemini API

---

## OpenAI Provider Details

### Supported Models

- `openai:gpt-4o` - GPT-4o (high quality)
- `openai:gpt-4o-mini` - GPT-4o-mini (fast, cost-effective)

Any OpenAI-compatible model ID can be used.

### Configuration

```yaml
ai:
    ask:
        default_model: "openai:gpt-4o-mini"
        api_keys:
            openai: "env://OPENAI_API_KEY"
```

### Getting an OpenAI API Key

1. Visit [OpenAI Platform](https://platform.openai.com/api-keys)
2. Create a new API key
3. Set the environment variable:

```bash
export OPENAI_API_KEY=your-api-key-here
```

### Implementation Notes

The OpenAI provider (`internal/llm/openai.go`) implements:

- **Tool Conversion**: MCP JSON Schema → OpenAI `FunctionParameters` (direct passthrough)
- **Streaming**: Uses `ChatCompletionAccumulator` for reliable stream aggregation
- **Conversation History**: Full support for multi-turn with tool call correlation
- **Error Handling**: Proper error propagation from OpenAI API
- **Compatible APIs**: Supports any OpenAI-compatible endpoint via configurable base URL

---

## Recommendations

- Use `anthropic:claude-sonnet-4-6` for everyday debugging (recommended default)
- Use `anthropic:claude-opus-4-6` for complex multi-step analysis
- Use `google:gemini-2.0-flash` for fast, cost-effective queries
- Use `openai:gpt-4o-mini` for a balance of speed and quality
- Use `ollama:llama3.2` for air-gapped or offline deployments
