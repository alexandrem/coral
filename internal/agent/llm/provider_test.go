package llm

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestMessage(t *testing.T) {
	tests := []struct {
		name    string
		message Message
	}{
		{
			name: "user message with content",
			message: Message{
				Role:    "user",
				Content: "Hello, how are you?",
			},
		},
		{
			name: "assistant message",
			message: Message{
				Role:    "assistant",
				Content: "I'm doing well, thank you!",
			},
		},
		{
			name: "system message",
			message: Message{
				Role:    "system",
				Content: "You are a helpful assistant.",
			},
		},
		{
			name: "tool message with responses",
			message: Message{
				Role: "tool",
				ToolResponses: []ToolResponse{
					{
						CallID:  "call-123",
						Name:    "get_weather",
						Content: `{"temperature": 72, "condition": "sunny"}`,
					},
				},
			},
		},
		{
			name: "empty message",
			message: Message{
				Role:          "",
				Content:       "",
				ToolResponses: nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Just verify the message can be created and fields are accessible
			if tt.message.Role == "tool" && len(tt.message.ToolResponses) == 0 {
				t.Error("Expected tool responses for tool role")
			}
		})
	}
}

func TestToolResponse(t *testing.T) {
	tests := []struct {
		name     string
		response ToolResponse
	}{
		{
			name: "JSON content",
			response: ToolResponse{
				CallID:  "call-456",
				Name:    "search",
				Content: `{"results": ["item1", "item2"]}`,
			},
		},
		{
			name: "plain text content",
			response: ToolResponse{
				CallID:  "call-789",
				Name:    "echo",
				Content: "Hello, World!",
			},
		},
		{
			name: "empty content",
			response: ToolResponse{
				CallID:  "call-empty",
				Name:    "noop",
				Content: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.response.CallID == "" {
				t.Error("CallID should not be empty")
			}
			if tt.response.Name == "" {
				t.Error("Name should not be empty")
			}
		})
	}
}

func TestToolCall(t *testing.T) {
	tests := []struct {
		name string
		call ToolCall
	}{
		{
			name: "tool call with arguments",
			call: ToolCall{
				ID:        "tc-123",
				Name:      "get_temperature",
				Arguments: `{"city": "San Francisco", "units": "celsius"}`,
			},
		},
		{
			name: "tool call without arguments",
			call: ToolCall{
				ID:        "tc-456",
				Name:      "get_time",
				Arguments: "{}",
			},
		},
		{
			name: "complex arguments",
			call: ToolCall{
				ID:        "tc-789",
				Name:      "analyze_data",
				Arguments: `{"filters": [{"field": "date", "op": ">=", "value": "2024-01-01"}], "limit": 100}`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.call.ID == "" {
				t.Error("ID should not be empty")
			}
			if tt.call.Name == "" {
				t.Error("Name should not be empty")
			}
		})
	}
}

func TestGenerateRequest(t *testing.T) {
	tests := []struct {
		name    string
		request GenerateRequest
	}{
		{
			name: "basic request",
			request: GenerateRequest{
				Messages: []Message{
					{Role: "user", Content: "Hello"},
				},
				Stream: false,
			},
		},
		{
			name: "request with tools",
			request: GenerateRequest{
				Messages: []Message{
					{Role: "user", Content: "What's the weather?"},
				},
				Tools: []mcp.Tool{
					{
						Name:        "get_weather",
						Description: "Get weather for a location",
					},
				},
				Stream: false,
			},
		},
		{
			name: "streaming request",
			request: GenerateRequest{
				Messages: []Message{
					{Role: "user", Content: "Tell me a story"},
				},
				Stream: true,
			},
		},
		{
			name: "request with system prompt",
			request: GenerateRequest{
				Messages: []Message{
					{Role: "user", Content: "Help me debug this code"},
				},
				SystemPrompt: "You are an expert debugger.",
				Stream:       false,
			},
		},
		{
			name: "multi-turn conversation",
			request: GenerateRequest{
				Messages: []Message{
					{Role: "user", Content: "Hello"},
					{Role: "assistant", Content: "Hi there!"},
					{Role: "user", Content: "How are you?"},
				},
				Stream: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.request.Messages) == 0 {
				t.Error("Request should have at least one message")
			}
		})
	}
}

func TestGenerateResponse(t *testing.T) {
	tests := []struct {
		name     string
		response GenerateResponse
	}{
		{
			name: "text response",
			response: GenerateResponse{
				Content:      "This is the response content.",
				FinishReason: "stop",
			},
		},
		{
			name: "response with tool calls",
			response: GenerateResponse{
				Content: "I'll check the weather for you.",
				ToolCalls: []ToolCall{
					{
						ID:        "tc-1",
						Name:      "get_weather",
						Arguments: `{"city": "Boston"}`,
					},
				},
				FinishReason: "tool_calls",
			},
		},
		{
			name: "length limited response",
			response: GenerateResponse{
				Content:      "This response was cut off because...",
				FinishReason: "length",
			},
		},
		{
			name: "empty response",
			response: GenerateResponse{
				Content:      "",
				FinishReason: "stop",
			},
		},
		{
			name: "multiple tool calls",
			response: GenerateResponse{
				Content: "I'll do several things for you.",
				ToolCalls: []ToolCall{
					{ID: "tc-1", Name: "tool1", Arguments: `{}`},
					{ID: "tc-2", Name: "tool2", Arguments: `{}`},
				},
				FinishReason: "tool_calls",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.response.FinishReason == "" {
				t.Error("FinishReason should not be empty")
			}

			if len(tt.response.ToolCalls) > 0 && tt.response.FinishReason != "tool_calls" {
				t.Error("FinishReason should be 'tool_calls' when tool calls are present")
			}
		})
	}
}
