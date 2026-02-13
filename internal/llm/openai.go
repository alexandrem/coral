package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/coral-mesh/coral/internal/config"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
)

// OpenAIProvider implements the Provider interface for OpenAI-compatible APIs.
type OpenAIProvider struct {
	client *openai.Client
	model  string
}

// NewOpenAIProvider creates a new OpenAI provider. If baseURL is empty, the
// public OpenAI API is used. A non-empty baseURL allows targeting any
// OpenAI-compatible endpoint.
func NewOpenAIProvider(apiKey string, modelName string, baseURL string) (*OpenAIProvider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required") // nolint: staticcheck
	}

	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}

	client := openai.NewClient(opts...)

	return &OpenAIProvider{
		client: &client,
		model:  modelName,
	}, nil
}

// Name returns the provider name.
func (p *OpenAIProvider) Name() string {
	return "openai"
}

// Generate sends a request to OpenAI and returns the response.
func (p *OpenAIProvider) Generate(ctx context.Context, req GenerateRequest, streamCallback StreamCallback) (*GenerateResponse, error) {
	// Build messages.
	var messages []openai.ChatCompletionMessageParamUnion

	// Add system prompt if provided.
	if req.SystemPrompt != "" {
		messages = append(messages, openai.SystemMessage(req.SystemPrompt))
	}

	// Convert conversation messages.
	for _, msg := range req.Messages {
		switch msg.Role {
		case "user":
			messages = append(messages, openai.UserMessage(msg.Content))

		case "assistant":
			if len(msg.ToolCalls) > 0 {
				// Assistant message with tool calls.
				asst := openai.ChatCompletionAssistantMessageParam{}
				if msg.Content != "" {
					asst.Content.OfString = openai.String(msg.Content)
				}
				for _, tc := range msg.ToolCalls {
					asst.ToolCalls = append(asst.ToolCalls, openai.ChatCompletionMessageToolCallParam{
						ID: tc.ID,
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      tc.Name,
							Arguments: tc.Arguments,
						},
					})
				}
				messages = append(messages, openai.ChatCompletionMessageParamUnion{OfAssistant: &asst})
			} else {
				messages = append(messages, openai.ChatCompletionMessageParamOfAssistant[string](msg.Content))
			}

		case "tool":
			for _, tr := range msg.ToolResponses {
				messages = append(messages, openai.ToolMessage(tr.Content, tr.CallID))
			}

		case "system":
			messages = append(messages, openai.SystemMessage(msg.Content))
		}
	}

	// Convert MCP tools to OpenAI function tools.
	var tools []openai.ChatCompletionToolParam
	for _, mcpTool := range req.Tools {
		params, err := mcpToolToFunctionParameters(mcpTool)
		if err != nil {
			return nil, fmt.Errorf("failed to convert tool %s: %w", mcpTool.Name, err)
		}
		tools = append(tools, openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        mcpTool.Name,
				Description: openai.String(mcpTool.Description),
				Parameters:  params,
			},
		})
	}

	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(p.model),
		Messages: messages,
	}
	if len(tools) > 0 {
		params.Tools = tools
	}

	// Streaming response.
	if req.Stream && streamCallback != nil {
		return p.generateStreaming(ctx, params, streamCallback)
	}

	// Non-streaming response.
	return p.generateNonStreaming(ctx, params)
}

// generateNonStreaming sends a non-streaming request.
func (p *OpenAIProvider) generateNonStreaming(ctx context.Context, params openai.ChatCompletionNewParams) (*GenerateResponse, error) {
	completion, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("generate error: %w", err)
	}

	if len(completion.Choices) == 0 {
		return &GenerateResponse{FinishReason: "stop"}, nil
	}

	choice := completion.Choices[0]

	var toolCalls []ToolCall
	for _, tc := range choice.Message.ToolCalls {
		toolCalls = append(toolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	finishReason := choice.FinishReason
	if finishReason == "" {
		finishReason = "stop"
	}

	return &GenerateResponse{
		Content:      choice.Message.Content,
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
	}, nil
}

// generateStreaming sends a streaming request.
func (p *OpenAIProvider) generateStreaming(ctx context.Context, params openai.ChatCompletionNewParams, streamCallback StreamCallback) (*GenerateResponse, error) {
	stream := p.client.Chat.Completions.NewStreaming(ctx, params)

	acc := openai.ChatCompletionAccumulator{}

	for stream.Next() {
		chunk := stream.Current()
		acc.AddChunk(chunk)

		// Stream text content deltas.
		if len(chunk.Choices) > 0 {
			delta := chunk.Choices[0].Delta
			if delta.Content != "" {
				if err := streamCallback(delta.Content); err != nil {
					return nil, fmt.Errorf("stream callback error: %w", err)
				}
			}
		}
	}

	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("stream error: %w", err)
	}

	// Extract accumulated result.
	if len(acc.Choices) == 0 {
		return &GenerateResponse{FinishReason: "stop"}, nil
	}

	choice := acc.Choices[0]

	var toolCalls []ToolCall
	for _, tc := range choice.Message.ToolCalls {
		toolCalls = append(toolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	finishReason := choice.FinishReason
	if finishReason == "" {
		finishReason = "stop"
	}

	return &GenerateResponse{
		Content:      choice.Message.Content,
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
	}, nil
}

// mcpToolToFunctionParameters converts an MCP tool's input schema to OpenAI
// FunctionParameters.
func mcpToolToFunctionParameters(tool mcp.Tool) (openai.FunctionParameters, error) {
	var schemaJSON []byte
	var err error
	if len(tool.RawInputSchema) > 0 {
		schemaJSON = tool.RawInputSchema
	} else {
		schemaJSON, err = json.Marshal(tool.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal schema: %w", err)
		}
	}

	var params openai.FunctionParameters
	if err := json.Unmarshal(schemaJSON, &params); err != nil {
		return nil, fmt.Errorf("failed to unmarshal schema: %w", err)
	}

	return params, nil
}

func init() {
	Register(ProviderMetadata{
		Name:           "openai",
		DisplayName:    "OpenAI",
		Description:    "OpenAI models and compatible APIs",
		DefaultEnvVar:  "OPENAI_API_KEY",
		RequiresAPIKey: true,
		SupportedModels: []ModelMetadata{
			{
				ID:           "gpt-4o",
				DisplayName:  "GPT-4o",
				Description:  "Most capable GPT-4 model",
				Capabilities: []string{"tools", "streaming", "vision"},
			},
			{
				ID:           "gpt-4o-mini",
				DisplayName:  "GPT-4o Mini",
				Description:  "Fast, affordable model for most tasks",
				Capabilities: []string{"tools", "streaming", "vision"},
			},
			{
				ID:           "gpt-4-turbo",
				DisplayName:  "GPT-4 Turbo",
				Description:  "Previous generation advanced model",
				Capabilities: []string{"tools", "streaming", "vision"},
			},
			{
				ID:           "gpt-3.5-turbo",
				DisplayName:  "GPT-3.5 Turbo",
				Description:  "Legacy fast model",
				Capabilities: []string{"tools", "streaming"},
				Deprecated:   true,
			},
		},
	}, func(ctx context.Context, modelID string, cfg *config.AskConfig, debug bool) (Provider, error) {
		apiKey, err := resolveProviderAPIKey(cfg, "openai", "OPENAI_API_KEY")
		if err != nil {
			return nil, err
		}
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] OpenAI API key found (length: %d)\n", len(apiKey))
		}
		return NewOpenAIProvider(apiKey, modelID, "")
	})
}
