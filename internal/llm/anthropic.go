package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/mark3labs/mcp-go/mcp"
)

// AnthropicProvider implements the Provider interface for Anthropic Claude models.
type AnthropicProvider struct {
	client *anthropic.Client
	model  string
}

// NewAnthropicProvider creates a new Anthropic provider.
func NewAnthropicProvider(apiKey string, modelName string) (*AnthropicProvider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("Anthropic API key is required") // nolint: staticcheck
	}

	client := anthropic.NewClient(option.WithAPIKey(apiKey))

	return &AnthropicProvider{
		client: &client,
		model:  modelName,
	}, nil
}

// Name returns the provider name.
func (p *AnthropicProvider) Name() string {
	return "anthropic"
}

// Generate sends a request to Anthropic and returns the response.
func (p *AnthropicProvider) Generate(ctx context.Context, req GenerateRequest, streamCallback StreamCallback) (*GenerateResponse, error) {
	messages, err := convertMessagesToAnthropic(req.Messages)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	tools, err := convertMCPToolsToAnthropic(req.Tools)
	if err != nil {
		return nil, fmt.Errorf("failed to convert tools: %w", err)
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: 8096,
		Messages:  messages,
	}

	if req.SystemPrompt != "" {
		params.System = []anthropic.TextBlockParam{
			{Text: req.SystemPrompt},
		}
	}

	if len(tools) > 0 {
		params.Tools = tools
	}

	if req.Stream && streamCallback != nil {
		return p.generateStreaming(ctx, params, streamCallback)
	}

	return p.generateNonStreaming(ctx, params)
}

// generateNonStreaming sends a non-streaming request.
func (p *AnthropicProvider) generateNonStreaming(ctx context.Context, params anthropic.MessageNewParams) (*GenerateResponse, error) {
	msg, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("generate error: %w", err)
	}

	return extractAnthropicResponse(*msg)
}

// generateStreaming sends a streaming request, accumulating the full response
// while passing text deltas to the callback as they arrive.
func (p *AnthropicProvider) generateStreaming(ctx context.Context, params anthropic.MessageNewParams, streamCallback StreamCallback) (*GenerateResponse, error) {
	stream := p.client.Messages.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()

	var acc anthropic.Message

	for stream.Next() {
		event := stream.Current()
		if err := acc.Accumulate(event); err != nil {
			return nil, fmt.Errorf("stream accumulation error: %w", err)
		}

		// Stream text deltas immediately.
		if delta, ok := event.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
			if text, ok := delta.Delta.AsAny().(anthropic.TextDelta); ok {
				if err := streamCallback(text.Text); err != nil {
					return nil, fmt.Errorf("stream callback error: %w", err)
				}
			}
		}
	}

	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("stream error: %w", err)
	}

	return extractAnthropicResponse(acc)
}

// extractAnthropicResponse converts an Anthropic message to a GenerateResponse.
func extractAnthropicResponse(msg anthropic.Message) (*GenerateResponse, error) {
	var content string
	var toolCalls []ToolCall

	for _, block := range msg.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			content += b.Text
		case anthropic.ToolUseBlock:
			argsJSON, err := json.Marshal(b.Input)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal tool arguments: %w", err)
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:        b.ID,
				Name:      b.Name,
				Arguments: string(argsJSON),
			})
		}
	}

	finishReason := "stop"
	switch msg.StopReason {
	case anthropic.StopReasonToolUse:
		finishReason = "tool_calls"
	case anthropic.StopReasonMaxTokens:
		finishReason = "length"
	}

	return &GenerateResponse{
		Content:      content,
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
	}, nil
}

// convertMessagesToAnthropic converts the generic Message slice to Anthropic's
// message format. Anthropic uses content blocks for tool calls and results.
func convertMessagesToAnthropic(msgs []Message) ([]anthropic.MessageParam, error) {
	var result []anthropic.MessageParam

	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			result = append(result, anthropic.NewUserMessage(anthropic.NewTextBlock(msg.Content)))

		case "assistant":
			if len(msg.ToolCalls) > 0 {
				var blocks []anthropic.ContentBlockParamUnion
				if msg.Content != "" {
					blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
				}
				for _, tc := range msg.ToolCalls {
					var input any
					if err := json.Unmarshal([]byte(tc.Arguments), &input); err != nil {
						return nil, fmt.Errorf("failed to parse tool call arguments for %s: %w", tc.Name, err)
					}
					blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, input, tc.Name))
				}
				result = append(result, anthropic.MessageParam{
					Role:    anthropic.MessageParamRoleAssistant,
					Content: blocks,
				})
			} else {
				result = append(result, anthropic.NewAssistantMessage(anthropic.NewTextBlock(msg.Content)))
			}

		case "tool":
			// Tool results become a user message with ToolResultBlock content.
			var blocks []anthropic.ContentBlockParamUnion
			for _, tr := range msg.ToolResponses {
				blocks = append(blocks, anthropic.NewToolResultBlock(tr.CallID, tr.Content, false))
			}
			result = append(result, anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleUser,
				Content: blocks,
			})

		case "system":
			// Anthropic only supports a single top-level system prompt, so inline
			// system messages are injected as user messages with a clear prefix.
			result = append(result, anthropic.NewUserMessage(anthropic.NewTextBlock("[System] "+msg.Content)))
		}
	}

	return result, nil
}

// convertMCPToolsToAnthropic converts MCP tools to Anthropic's ToolUnionParam slice.
func convertMCPToolsToAnthropic(mcpTools []mcp.Tool) ([]anthropic.ToolUnionParam, error) {
	var tools []anthropic.ToolUnionParam

	for _, mcpTool := range mcpTools {
		var schemaJSON []byte
		var err error
		if len(mcpTool.RawInputSchema) > 0 {
			schemaJSON = mcpTool.RawInputSchema
		} else {
			schemaJSON, err = json.Marshal(mcpTool.InputSchema)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal schema for tool %s: %w", mcpTool.Name, err)
			}
		}

		// Use param.Override to pass the raw JSON schema directly, preserving all
		// JSON Schema fields without manual mapping.
		inputSchema := param.Override[anthropic.ToolInputSchemaParam](json.RawMessage(schemaJSON))

		toolParam := anthropic.ToolParam{
			Name:        mcpTool.Name,
			Description: anthropic.String(mcpTool.Description),
			InputSchema: inputSchema,
		}
		tools = append(tools, anthropic.ToolUnionParam{OfTool: &toolParam})
	}

	return tools, nil
}

func init() {
	Register(ProviderMetadata{
		Name:           "anthropic",
		DisplayName:    "Anthropic",
		Description:    "Anthropic's Claude models",
		DefaultEnvVar:  "ANTHROPIC_API_KEY",
		RequiresAPIKey: true,
		SupportedModels: []ModelMetadata{
			{
				ID:              "claude-sonnet-4-6",
				DisplayName:     "Claude Sonnet 4.6",
				Description:     "Best for everyday tasks, fast and capable",
				Capabilities:    []string{"tools", "streaming", "vision"},
				UseCase:         "balanced",
				CostPer1MTokens: 3.00,
				ContextWindow:   200_000,
				Recommended:     true,
			},
			{
				ID:              "claude-opus-4-6",
				DisplayName:     "Claude Opus 4.6",
				Description:     "Most capable model for complex tasks",
				Capabilities:    []string{"tools", "streaming", "vision"},
				UseCase:         "quality",
				CostPer1MTokens: 15.00,
				ContextWindow:   200_000,
			},
			{
				ID:              "claude-haiku-4-5-20251001",
				DisplayName:     "Claude Haiku 4.5",
				Description:     "Fastest and most compact model",
				Capabilities:    []string{"tools", "streaming", "vision"},
				UseCase:         "fast",
				CostPer1MTokens: 0.80,
				ContextWindow:   200_000,
			},
			{
				ID:              "claude-3-5-sonnet-20241022",
				DisplayName:     "Claude 3.5 Sonnet",
				Description:     "Previous generation balanced model",
				Capabilities:    []string{"tools", "streaming", "vision"},
				UseCase:         "balanced",
				CostPer1MTokens: 3.00,
				ContextWindow:   200_000,
			},
			{
				ID:              "claude-3-opus-20240229",
				DisplayName:     "Claude 3 Opus",
				Description:     "Previous generation most capable model",
				Capabilities:    []string{"tools", "streaming", "vision"},
				UseCase:         "quality",
				CostPer1MTokens: 15.00,
				ContextWindow:   200_000,
				Deprecated:      true,
			},
		},
	}, func(ctx context.Context, modelID string, cfg *config.AskConfig, debug bool) (Provider, error) {
		apiKey, err := resolveProviderAPIKey(cfg, "anthropic", "ANTHROPIC_API_KEY")
		if err != nil {
			return nil, err
		}
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Anthropic API key found (length: %d)\n", len(apiKey))
		}
		return NewAnthropicProvider(apiKey, modelID)
	})
}
