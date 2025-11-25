package llm

import (
	"encoding/json"
	"testing"

	"github.com/google/generative-ai-go/genai"
)

// TestConvertJSONSchemaToGemini_ArrayWithItems tests that array schemas
// with items field are correctly converted to Gemini format.
// This test would have caught the bug where items field was missing.
func TestConvertJSONSchemaToGemini_ArrayWithItems(t *testing.T) {
	// Create a JSON schema with an array property (like ExecCommandInput.Command)
	jsonSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": map[string]interface{}{
				"type":        "array",
				"description": "Command and arguments",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
			"timeout": map[string]interface{}{
				"type":    "integer",
				"default": 30,
			},
		},
		"required": []interface{}{"command"},
	}

	// Convert to Gemini format
	geminiSchema := convertJSONSchemaToGemini(jsonSchema)

	// Verify top-level schema
	if geminiSchema.Type != genai.TypeObject {
		t.Errorf("Expected Type=TypeObject, got %v", geminiSchema.Type)
	}

	// Verify command property exists
	commandProp, ok := geminiSchema.Properties["command"]
	if !ok {
		t.Fatal("command property not found in converted schema")
	}

	// Verify command is an array
	if commandProp.Type != genai.TypeArray {
		t.Errorf("Expected command Type=TypeArray, got %v", commandProp.Type)
	}

	// Verify command has Items (THIS WAS THE BUG)
	if commandProp.Items == nil {
		t.Error("BUG: command.Items is nil! Google AI will reject this schema")
		t.Logf("Full command schema: %+v", commandProp)
	} else {
		if commandProp.Items.Type != genai.TypeString {
			t.Errorf("Expected command.Items.Type=TypeString, got %v", commandProp.Items.Type)
		}
		t.Logf("✓ command.Items correctly set to TypeString")
	}

	// Verify required fields
	if len(geminiSchema.Required) != 1 || geminiSchema.Required[0] != "command" {
		t.Errorf("Expected Required=[command], got %v", geminiSchema.Required)
	}

	// Verify other property
	timeoutProp, ok := geminiSchema.Properties["timeout"]
	if !ok {
		t.Error("timeout property not found")
	} else if timeoutProp.Type != genai.TypeInteger {
		t.Errorf("Expected timeout Type=TypeInteger, got %v", timeoutProp.Type)
	}

	// Marshal to verify structure
	schemaJSON, _ := json.MarshalIndent(geminiSchema, "", "  ")
	t.Logf("Converted Gemini schema:\n%s", string(schemaJSON))
}

// TestConvertJSONSchemaToGemini_NestedArrays tests nested array handling
func TestConvertJSONSchemaToGemini_NestedArrays(t *testing.T) {
	jsonSchema := map[string]interface{}{
		"type": "array",
		"items": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"tags": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "string",
					},
				},
			},
		},
	}

	geminiSchema := convertJSONSchemaToGemini(jsonSchema)

	if geminiSchema.Type != genai.TypeArray {
		t.Errorf("Expected Type=TypeArray, got %v", geminiSchema.Type)
	}

	if geminiSchema.Items == nil {
		t.Fatal("Items is nil for top-level array")
	}

	if geminiSchema.Items.Type != genai.TypeObject {
		t.Errorf("Expected Items.Type=TypeObject, got %v", geminiSchema.Items.Type)
	}

	tagsProp := geminiSchema.Items.Properties["tags"]
	if tagsProp == nil {
		t.Fatal("tags property not found in nested object")
	}

	if tagsProp.Type != genai.TypeArray {
		t.Errorf("Expected tags Type=TypeArray, got %v", tagsProp.Type)
	}

	if tagsProp.Items == nil {
		t.Error("BUG: tags.Items is nil")
	} else if tagsProp.Items.Type != genai.TypeString {
		t.Errorf("Expected tags.Items.Type=TypeString, got %v", tagsProp.Items.Type)
	}
}
