package llm

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/openai/openai-go"
)

func TestOpenAIProvider_Name(t *testing.T) {
	provider := &OpenAIProvider{
		client: nil,
		model:  "gpt-4o",
	}

	if provider.Name() != "openai" {
		t.Errorf("Expected Name()='openai', got %q", provider.Name())
	}
}

func TestMCPToolToFunctionParameters(t *testing.T) {
	tool := mcp.Tool{
		Name:        "get_services",
		Description: "List all running services",
	}
	tool.InputSchema.Type = "object"
	tool.InputSchema.Properties = map[string]interface{}{
		"filter": map[string]interface{}{
			"type":        "string",
			"description": "Filter expression",
		},
		"limit": map[string]interface{}{
			"type":    "integer",
			"default": 10,
		},
	}

	params, err := mcpToolToFunctionParameters(tool)
	if err != nil {
		t.Fatalf("mcpToolToFunctionParameters() error: %v", err)
	}

	// Verify it's a valid map.
	if params == nil {
		t.Fatal("expected non-nil parameters")
	}

	// Verify type field.
	if typ, ok := params["type"].(string); !ok || typ != "object" {
		t.Errorf("expected type='object', got %v", params["type"])
	}

	// Verify properties exist.
	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("expected properties map")
	}

	if _, ok := props["filter"]; !ok {
		t.Error("expected 'filter' property")
	}
	if _, ok := props["limit"]; !ok {
		t.Error("expected 'limit' property")
	}
}

func TestMCPToolToFunctionParameters_RawSchema(t *testing.T) {
	rawSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "array",
				"items": {"type": "string"},
				"description": "Command and arguments"
			}
		},
		"required": ["command"]
	}`)

	tool := mcp.Tool{
		Name:           "exec_command",
		Description:    "Execute a command",
		RawInputSchema: rawSchema,
	}

	params, err := mcpToolToFunctionParameters(tool)
	if err != nil {
		t.Fatalf("mcpToolToFunctionParameters() error: %v", err)
	}

	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("expected properties map")
	}

	cmdProp, ok := props["command"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'command' property map")
	}

	if cmdProp["type"] != "array" {
		t.Errorf("expected command type='array', got %v", cmdProp["type"])
	}
}

func TestMCPToolToFunctionParameters_EmptySchema(t *testing.T) {
	tool := mcp.Tool{
		Name:        "ping",
		Description: "Ping the service",
	}
	tool.InputSchema.Type = "object"

	params, err := mcpToolToFunctionParameters(tool)
	if err != nil {
		t.Fatalf("mcpToolToFunctionParameters() error: %v", err)
	}

	if params == nil {
		t.Fatal("expected non-nil parameters")
	}

	if typ, ok := params["type"].(string); !ok || typ != "object" {
		t.Errorf("expected type='object', got %v", params["type"])
	}
}

func TestOpenAIProvider_MessageConversion(t *testing.T) {
	// Test that our message types map correctly to OpenAI types.
	// This is a compile-time verification that the types are compatible.

	// User message.
	userMsg := openai.UserMessage("hello")
	if userMsg.OfUser == nil {
		t.Error("expected OfUser to be set")
	}

	// System message.
	sysMsg := openai.SystemMessage("you are helpful")
	if sysMsg.OfSystem == nil {
		t.Error("expected OfSystem to be set")
	}

	// Tool message.
	toolMsg := openai.ToolMessage("result", "call_123")
	if toolMsg.OfTool == nil {
		t.Error("expected OfTool to be set")
	}
	if toolMsg.OfTool.ToolCallID != "call_123" {
		t.Errorf("expected ToolCallID='call_123', got %q", toolMsg.OfTool.ToolCallID)
	}

	// Assistant message with tool calls.
	asst := openai.ChatCompletionAssistantMessageParam{}
	asst.Content.OfString = openai.String("thinking...")
	asst.ToolCalls = []openai.ChatCompletionMessageToolCallParam{
		{
			ID: "call_456",
			Function: openai.ChatCompletionMessageToolCallFunctionParam{
				Name:      "get_services",
				Arguments: `{"filter":"active"}`,
			},
		},
	}
	asstMsg := openai.ChatCompletionMessageParamUnion{OfAssistant: &asst}
	if asstMsg.OfAssistant == nil {
		t.Error("expected OfAssistant to be set")
	}
	if len(asstMsg.OfAssistant.ToolCalls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(asstMsg.OfAssistant.ToolCalls))
	}
}
