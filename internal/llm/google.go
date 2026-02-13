package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/coral-mesh/coral/internal/config"
	"github.com/google/generative-ai-go/genai"
	"github.com/mark3labs/mcp-go/mcp"
	"google.golang.org/api/option"
)

// GoogleProvider implements the Provider interface for Google AI (Gemini).
type GoogleProvider struct {
	client *genai.Client
	model  string
}

// NewGoogleProvider creates a new Google AI provider.
func NewGoogleProvider(ctx context.Context, apiKey string, modelName string) (*GoogleProvider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("Google AI API key is required") // nolint: staticcheck
	}

	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, fmt.Errorf("failed to create Google AI client: %w", err)
	}

	return &GoogleProvider{
		client: client,
		model:  modelName,
	}, nil
}

// Name returns the provider name.
func (p *GoogleProvider) Name() string {
	return "google"
}

// Generate sends a request to Google AI and returns the response.
func (p *GoogleProvider) Generate(ctx context.Context, req GenerateRequest, streamCallback StreamCallback) (*GenerateResponse, error) {
	model := p.client.GenerativeModel(p.model)

	// Set system instruction if provided (RFD 054).
	if req.SystemPrompt != "" {
		model.SystemInstruction = &genai.Content{
			Parts: []genai.Part{genai.Text(req.SystemPrompt)},
		}
	}

	// Convert MCP tools to Gemini function declarations.
	if len(req.Tools) > 0 {
		tools, err := convertMCPToolsToGemini(req.Tools)
		if err != nil {
			return nil, fmt.Errorf("failed to convert tools: %w", err)
		}
		model.Tools = tools
	}

	// Build chat session from message history.
	chat := model.StartChat()

	// Convert messages to Gemini format.
	// Note: Gemini chat expects alternating user/model messages.
	// We'll handle the conversation history and current message separately.
	var history []*genai.Content
	var currentParts []genai.Part

	for i, msg := range req.Messages {
		role := "user"
		if msg.Role == "assistant" || msg.Role == "model" {
			role = "model"
		}
		// Tool responses are sent as "user" role with FunctionResponse parts.
		if msg.Role == "tool" {
			role = "user"
		}

		var parts []genai.Part

		// Add text content if present.
		if msg.Content != "" {
			parts = append(parts, genai.Text(msg.Content))
		}

		// Add tool responses if present (for tool role).
		if len(msg.ToolResponses) > 0 {
			for _, tr := range msg.ToolResponses {
				// Parse tool response content as JSON map.
				var responseData map[string]any
				if err := json.Unmarshal([]byte(tr.Content), &responseData); err != nil {
					// If not JSON, wrap as plain text in a map.
					responseData = map[string]any{
						"result": tr.Content,
					}
				}

				parts = append(parts, genai.FunctionResponse{
					Name:     tr.Name,
					Response: responseData,
				})
			}
		}

		content := &genai.Content{
			Role:  role,
			Parts: parts,
		}

		// Last message is the current message, everything else is history.
		if i == len(req.Messages)-1 {
			currentParts = parts
		} else {
			history = append(history, content)
		}
	}

	chat.History = history

	// Send the current message.
	if req.Stream && streamCallback != nil {
		// Streaming response.
		iter := chat.SendMessageStream(ctx, currentParts...)
		var fullResponse string
		var toolCalls []ToolCall

		for {
			resp, err := iter.Next()
			if err != nil {
				if err.Error() == "iterator exhausted" || err.Error() == "no more items in iterator" {
					break
				}
				return nil, fmt.Errorf("stream error: %w", err)
			}

			// Process candidates.
			if len(resp.Candidates) > 0 {
				candidate := resp.Candidates[0]
				if candidate.Content != nil {
					for _, part := range candidate.Content.Parts {
						if txt, ok := part.(genai.Text); ok {
							chunk := string(txt)
							fullResponse += chunk
							if err := streamCallback(chunk); err != nil {
								return nil, fmt.Errorf("stream callback error: %w", err)
							}
						} else if fc, ok := part.(genai.FunctionCall); ok {
							// Tool call in stream.
							argsJSON, _ := json.Marshal(fc.Args)
							toolCalls = append(toolCalls, ToolCall{
								ID:        fc.Name, // Gemini doesn't provide separate IDs
								Name:      fc.Name,
								Arguments: string(argsJSON),
							})
						}
					}
				}
			}
		}

		finishReason := "stop"
		if len(toolCalls) > 0 {
			finishReason = "tool_calls"
		}

		return &GenerateResponse{
			Content:      fullResponse,
			ToolCalls:    toolCalls,
			FinishReason: finishReason,
		}, nil
	}

	// Non-streaming response.
	resp, err := chat.SendMessage(ctx, currentParts...)
	if err != nil {
		return nil, fmt.Errorf("generate error: %w", err)
	}

	// Extract response content and tool calls.
	var content string
	var toolCalls []ToolCall

	if len(resp.Candidates) > 0 {
		candidate := resp.Candidates[0]
		if candidate.Content != nil {
			for _, part := range candidate.Content.Parts {
				if txt, ok := part.(genai.Text); ok {
					content += string(txt)
				} else if fc, ok := part.(genai.FunctionCall); ok {
					argsJSON, _ := json.Marshal(fc.Args)
					toolCalls = append(toolCalls, ToolCall{
						ID:        fc.Name,
						Name:      fc.Name,
						Arguments: string(argsJSON),
					})
				}
			}
		}
	}

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}

	return &GenerateResponse{
		Content:      content,
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
	}, nil
}

// convertMCPToolsToGemini converts MCP tools to Gemini function declarations.
func convertMCPToolsToGemini(mcpTools []mcp.Tool) ([]*genai.Tool, error) {
	var declarations []*genai.FunctionDeclaration

	for _, mcpTool := range mcpTools {
		// Get the tool's input schema as JSON.
		// Prefer RawInputSchema if available (set by NewToolWithRawSchema),
		// otherwise marshal InputSchema struct.
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

		// Parse into a generic map.
		var schemaMap map[string]interface{}
		if err := json.Unmarshal(schemaJSON, &schemaMap); err != nil {
			return nil, fmt.Errorf("failed to unmarshal schema for tool %s: %w", mcpTool.Name, err)
		}

		// Convert to Gemini schema format.
		geminiSchema := convertJSONSchemaToGemini(schemaMap)

		declarations = append(declarations, &genai.FunctionDeclaration{
			Name:        mcpTool.Name,
			Description: mcpTool.Description,
			Parameters:  geminiSchema,
		})
	}

	return []*genai.Tool{{FunctionDeclarations: declarations}}, nil
}

// convertJSONSchemaToGemini converts a JSON Schema map to Gemini's Schema format.
func convertJSONSchemaToGemini(jsonSchema map[string]interface{}) *genai.Schema {
	schema := &genai.Schema{}

	// Type.
	if typ, ok := jsonSchema["type"].(string); ok {
		schema.Type = jsonSchemaTypeToGeminiType(typ)
	}

	// Description.
	if desc, ok := jsonSchema["description"].(string); ok {
		schema.Description = desc
	}

	// Properties.
	if props, ok := jsonSchema["properties"].(map[string]interface{}); ok {
		schema.Properties = make(map[string]*genai.Schema)
		for propName, propValue := range props {
			if propMap, ok := propValue.(map[string]interface{}); ok {
				schema.Properties[propName] = convertJSONSchemaToGemini(propMap)
			}
		}
	}

	// Required fields.
	if required, ok := jsonSchema["required"].([]interface{}); ok {
		for _, r := range required {
			if reqStr, ok := r.(string); ok {
				schema.Required = append(schema.Required, reqStr)
			}
		}
	}

	// Enum values.
	if enum, ok := jsonSchema["enum"].([]interface{}); ok {
		for _, e := range enum {
			if enumStr, ok := e.(string); ok {
				schema.Enum = append(schema.Enum, enumStr)
			}
		}
	}

	// Items (for arrays).
	// Google AI requires this field for array types.
	if items, ok := jsonSchema["items"].(map[string]interface{}); ok {
		schema.Items = convertJSONSchemaToGemini(items)
	}

	return schema
}

// Close closes the Google AI client.
func (p *GoogleProvider) Close() error {
	return p.client.Close()
}

// jsonSchemaTypeToGeminiType converts a JSON Schema type string to genai.Type.
func jsonSchemaTypeToGeminiType(typ string) genai.Type {
	switch typ {
	case "object":
		return genai.TypeObject
	case "string":
		return genai.TypeString
	case "number":
		return genai.TypeNumber
	case "integer":
		return genai.TypeInteger
	case "boolean":
		return genai.TypeBoolean
	case "array":
		return genai.TypeArray
	default:
		return genai.TypeUnspecified
	}
}

func init() {
	Register(ProviderMetadata{
		Name:           "google",
		DisplayName:    "Google AI (Gemini)",
		Description:    "Google's Gemini models via Google AI API",
		DefaultEnvVar:  "GOOGLE_API_KEY",
		RequiresAPIKey: true,
		SupportedModels: []ModelMetadata{
			{
				ID:           "gemini-2.0-flash",
				DisplayName:  "Gemini 2.0 Flash",
				Description:  "Fast, versatile model for most tasks",
				Capabilities: []string{"tools", "streaming", "vision"},
			},
			{
				ID:           "gemini-2.0-flash-thinking-exp-01-21",
				DisplayName:  "Gemini 2.0 Flash Thinking (Experimental)",
				Description:  "Extended reasoning model",
				Capabilities: []string{"tools", "streaming", "reasoning"},
			},
			{
				ID:           "gemini-2.0-flash-thinking-exp",
				DisplayName:  "Gemini 2.0 Flash Thinking (Experimental - Latest)",
				Description:  "Latest extended reasoning model",
				Capabilities: []string{"tools", "streaming", "reasoning"},
			},
			{
				ID:           "gemini-1.5-pro",
				DisplayName:  "Gemini 1.5 Pro",
				Description:  "Advanced model with large context window",
				Capabilities: []string{"tools", "streaming", "vision", "large-context"},
			},
			{
				ID:           "gemini-1.5-flash",
				DisplayName:  "Gemini 1.5 Flash",
				Description:  "Previous generation fast model",
				Capabilities: []string{"tools", "streaming", "vision"},
			},
		},
	}, func(ctx context.Context, modelID string, cfg *config.AskConfig, debug bool) (Provider, error) {
		apiKey, err := resolveProviderAPIKey(cfg, "google", "GOOGLE_API_KEY")
		if err != nil {
			return nil, err
		}
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Google AI API key found (length: %d)\n", len(apiKey))
		}
		return NewGoogleProvider(ctx, apiKey, modelID)
	})
}
