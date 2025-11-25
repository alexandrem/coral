package mcp

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// TestSchemaGeneration verifies that JSON schemas are properly generated and preserved.
func TestSchemaGeneration(t *testing.T) {
	tests := []struct {
		name      string
		inputType interface{}
		wantType  string
		wantProps bool // Should have properties
	}{
		{
			name:      "ServiceHealthInput",
			inputType: ServiceHealthInput{},
			wantType:  "object",
			wantProps: true,
		},
		{
			name:      "ServiceTopologyInput",
			inputType: ServiceTopologyInput{},
			wantType:  "object",
			wantProps: true,
		},
		{
			name:      "BeylaHTTPMetricsInput",
			inputType: BeylaHTTPMetricsInput{},
			wantType:  "object",
			wantProps: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Generate schema using the same function as the tool registration.
			schema, err := generateInputSchema(tt.inputType)
			if err != nil {
				t.Fatalf("generateInputSchema() error = %v", err)
			}

			// Verify schema has correct type.
			schemaType, ok := schema["type"].(string)
			if !ok {
				t.Errorf("schema type is not a string, got %T: %v", schema["type"], schema["type"])
			}
			if schemaType != tt.wantType {
				t.Errorf("schema type = %q, want %q", schemaType, tt.wantType)
			}

			// Verify properties exist if expected.
			if tt.wantProps {
				props, ok := schema["properties"]
				if !ok {
					t.Errorf("schema missing properties field, schema: %+v", schema)
				}
				if props == nil {
					t.Errorf("schema properties is nil")
				}
			}

			// Marshal schema to JSON bytes (as done in tool registration).
			schemaBytes, err := json.Marshal(schema)
			if err != nil {
				t.Fatalf("json.Marshal(schema) error = %v", err)
			}

			// Verify schema bytes are not empty.
			if len(schemaBytes) == 0 {
				t.Error("schema bytes are empty")
			}

			// Create MCP tool with raw schema.
			tool := mcp.NewToolWithRawSchema(
				"test_tool",
				"Test tool description",
				schemaBytes,
			)

			// Verify tool was created.
			if tool.Name != "test_tool" {
				t.Errorf("tool.Name = %q, want %q", tool.Name, "test_tool")
			}

			// Verify RawInputSchema was set.
			if tool.RawInputSchema == nil {
				t.Error("tool.RawInputSchema is nil")
			}

			// Marshal the tool to JSON (simulating what happens when sent over MCP protocol).
			toolJSON, err := json.Marshal(tool)
			if err != nil {
				t.Fatalf("json.Marshal(tool) error = %v", err)
			}

			// Unmarshal back to verify the schema is preserved.
			var unmarshaledTool map[string]interface{}
			if err := json.Unmarshal(toolJSON, &unmarshaledTool); err != nil {
				t.Fatalf("json.Unmarshal(toolJSON) error = %v", err)
			}

			// Verify inputSchema field exists in the marshaled JSON.
			inputSchema, ok := unmarshaledTool["inputSchema"]
			if !ok {
				t.Errorf("unmarshaled tool missing inputSchema field")
				t.Logf("Tool JSON: %s", string(toolJSON))
			}

			// Verify inputSchema is a map (not a string or empty).
			inputSchemaMap, ok := inputSchema.(map[string]interface{})
			if !ok {
				t.Errorf("inputSchema is not a map, got %T: %v", inputSchema, inputSchema)
				t.Logf("Tool JSON: %s", string(toolJSON))
			}

			// Verify inputSchema has a type field.
			inputSchemaType, ok := inputSchemaMap["type"].(string)
			if !ok {
				t.Errorf("inputSchema.type is not a string, got %T: %v", inputSchemaMap["type"], inputSchemaMap["type"])
			}
			if inputSchemaType == "" {
				t.Errorf("inputSchema.type is empty")
				t.Logf("Tool JSON: %s", string(toolJSON))
			}
			if inputSchemaType != tt.wantType {
				t.Errorf("inputSchema.type = %q, want %q", inputSchemaType, tt.wantType)
			}

			// Verify properties exist in the marshaled schema.
			if tt.wantProps {
				props, ok := inputSchemaMap["properties"]
				if !ok {
					t.Errorf("inputSchema missing properties field")
					t.Logf("Tool JSON: %s", string(toolJSON))
				}
				if props == nil {
					t.Errorf("inputSchema.properties is nil")
				}
			}
		})
	}
}
