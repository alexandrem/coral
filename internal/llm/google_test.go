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

// TestJSONSchemaTypeToGeminiType tests all type conversions
func TestJSONSchemaTypeToGeminiType(t *testing.T) {
	tests := []struct {
		name         string
		jsonType     string
		expectedType genai.Type
	}{
		{
			name:         "object type",
			jsonType:     "object",
			expectedType: genai.TypeObject,
		},
		{
			name:         "string type",
			jsonType:     "string",
			expectedType: genai.TypeString,
		},
		{
			name:         "number type",
			jsonType:     "number",
			expectedType: genai.TypeNumber,
		},
		{
			name:         "integer type",
			jsonType:     "integer",
			expectedType: genai.TypeInteger,
		},
		{
			name:         "boolean type",
			jsonType:     "boolean",
			expectedType: genai.TypeBoolean,
		},
		{
			name:         "array type",
			jsonType:     "array",
			expectedType: genai.TypeArray,
		},
		{
			name:         "unknown type",
			jsonType:     "unknown",
			expectedType: genai.TypeUnspecified,
		},
		{
			name:         "empty type",
			jsonType:     "",
			expectedType: genai.TypeUnspecified,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := jsonSchemaTypeToGeminiType(tt.jsonType)
			if result != tt.expectedType {
				t.Errorf("jsonSchemaTypeToGeminiType(%q) = %v, want %v", tt.jsonType, result, tt.expectedType)
			}
		})
	}
}

// TestConvertJSONSchemaToGemini_WithDescription tests description field
func TestConvertJSONSchemaToGemini_WithDescription(t *testing.T) {
	jsonSchema := map[string]interface{}{
		"type":        "string",
		"description": "A user's email address",
	}

	geminiSchema := convertJSONSchemaToGemini(jsonSchema)

	if geminiSchema.Type != genai.TypeString {
		t.Errorf("Expected Type=TypeString, got %v", geminiSchema.Type)
	}

	if geminiSchema.Description != "A user's email address" {
		t.Errorf("Expected Description='A user's email address', got %q", geminiSchema.Description)
	}
}

// TestConvertJSONSchemaToGemini_WithEnum tests enum field
func TestConvertJSONSchemaToGemini_WithEnum(t *testing.T) {
	jsonSchema := map[string]interface{}{
		"type": "string",
		"enum": []interface{}{"red", "green", "blue"},
	}

	geminiSchema := convertJSONSchemaToGemini(jsonSchema)

	if geminiSchema.Type != genai.TypeString {
		t.Errorf("Expected Type=TypeString, got %v", geminiSchema.Type)
	}

	expectedEnum := []string{"red", "green", "blue"}
	if len(geminiSchema.Enum) != len(expectedEnum) {
		t.Errorf("Expected %d enum values, got %d", len(expectedEnum), len(geminiSchema.Enum))
	}

	for i, expected := range expectedEnum {
		if i >= len(geminiSchema.Enum) || geminiSchema.Enum[i] != expected {
			t.Errorf("Expected Enum[%d]=%q, got %q", i, expected, geminiSchema.Enum[i])
		}
	}
}

// TestConvertJSONSchemaToGemini_ComplexObject tests a complex object with all features
func TestConvertJSONSchemaToGemini_ComplexObject(t *testing.T) {
	jsonSchema := map[string]interface{}{
		"type":        "object",
		"description": "User profile",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type":        "string",
				"description": "User's full name",
			},
			"age": map[string]interface{}{
				"type":        "integer",
				"description": "User's age",
			},
			"email": map[string]interface{}{
				"type":        "string",
				"description": "User's email",
			},
			"status": map[string]interface{}{
				"type": "string",
				"enum": []interface{}{"active", "inactive", "suspended"},
			},
		},
		"required": []interface{}{"name", "email"},
	}

	geminiSchema := convertJSONSchemaToGemini(jsonSchema)

	// Verify object type
	if geminiSchema.Type != genai.TypeObject {
		t.Errorf("Expected Type=TypeObject, got %v", geminiSchema.Type)
	}

	// Verify description
	if geminiSchema.Description != "User profile" {
		t.Errorf("Expected Description='User profile', got %q", geminiSchema.Description)
	}

	// Verify all properties exist
	requiredProps := []string{"name", "age", "email", "status"}
	for _, propName := range requiredProps {
		if _, ok := geminiSchema.Properties[propName]; !ok {
			t.Errorf("Property %q not found in converted schema", propName)
		}
	}

	// Verify required fields
	if len(geminiSchema.Required) != 2 {
		t.Errorf("Expected 2 required fields, got %d", len(geminiSchema.Required))
	}

	expectedRequired := map[string]bool{"name": true, "email": true}
	for _, req := range geminiSchema.Required {
		if !expectedRequired[req] {
			t.Errorf("Unexpected required field: %q", req)
		}
	}

	// Verify enum on status
	statusProp := geminiSchema.Properties["status"]
	if len(statusProp.Enum) != 3 {
		t.Errorf("Expected 3 enum values for status, got %d", len(statusProp.Enum))
	}
}

// TestConvertJSONSchemaToGemini_EmptySchema tests handling of empty schema
func TestConvertJSONSchemaToGemini_EmptySchema(t *testing.T) {
	jsonSchema := map[string]interface{}{}

	geminiSchema := convertJSONSchemaToGemini(jsonSchema)

	// Should return a schema with TypeUnspecified
	if geminiSchema.Type != genai.TypeUnspecified {
		t.Errorf("Expected Type=TypeUnspecified for empty schema, got %v", geminiSchema.Type)
	}
}

// TestGoogleProvider_Name tests the Name method
func TestGoogleProvider_Name(t *testing.T) {
	provider := &GoogleProvider{
		client: nil, // We don't need a real client for this test
		model:  "gemini-pro",
	}

	if provider.Name() != "google" {
		t.Errorf("Expected Name()='google', got %q", provider.Name())
	}
}
