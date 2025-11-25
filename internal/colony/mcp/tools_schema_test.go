package mcp

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToolSchemaServerToClient tests the complete flow of schema generation,
// marshaling on the server, and unmarshaling on the client.
func TestToolSchemaServerToClient(t *testing.T) {
	tests := []struct {
		name        string
		inputType   interface{}
		wantType    string
		wantProps   []string
		checkSchema func(*testing.T, map[string]interface{})
	}{
		{
			name:      "ServiceHealthInput",
			inputType: ServiceHealthInput{},
			wantType:  "object",
			wantProps: []string{"service_filter"},
			checkSchema: func(t *testing.T, schema map[string]interface{}) {
				props, ok := schema["properties"].(map[string]interface{})
				require.True(t, ok, "properties should be a map")

				serviceFilter, ok := props["service_filter"].(map[string]interface{})
				require.True(t, ok, "service_filter property should exist")

				assert.Equal(t, "string", serviceFilter["type"])
				assert.Contains(t, serviceFilter["description"], "Filter by service name pattern")
			},
		},
		{
			name:      "ServiceTopologyInput",
			inputType: ServiceTopologyInput{},
			wantType:  "object",
			wantProps: []string{"filter", "format"},
			checkSchema: func(t *testing.T, schema map[string]interface{}) {
				props, ok := schema["properties"].(map[string]interface{})
				require.True(t, ok)

				format, ok := props["format"].(map[string]interface{})
				require.True(t, ok)

				// Check enum values.
				enum, ok := format["enum"].([]interface{})
				require.True(t, ok, "format should have enum")
				assert.Contains(t, enum, "graph")
				assert.Contains(t, enum, "list")
				assert.Contains(t, enum, "json")
			},
		},
		{
			name:      "BeylaHTTPMetricsInput",
			inputType: BeylaHTTPMetricsInput{},
			wantType:  "object",
			wantProps: []string{"service", "time_range"},
			checkSchema: func(t *testing.T, schema map[string]interface{}) {
				// Check required fields.
				required, ok := schema["required"].([]interface{})
				require.True(t, ok, "should have required fields")
				assert.Contains(t, required, "service")

				// Check properties.
				props, ok := schema["properties"].(map[string]interface{})
				require.True(t, ok)

				service, ok := props["service"].(map[string]interface{})
				require.True(t, ok)
				assert.Equal(t, "string", service["type"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Step 1: Generate schema on server (using DoNotReference).
			schema, err := generateInputSchema(tt.inputType)
			require.NoError(t, err, "schema generation should succeed")

			// Verify schema has type at root level (not nested in $ref).
			schemaType, ok := schema["type"].(string)
			require.True(t, ok, "schema should have type field at root")
			assert.Equal(t, tt.wantType, schemaType, "schema type should be %s", tt.wantType)

			// Verify properties exist.
			props, ok := schema["properties"].(map[string]interface{})
			require.True(t, ok, "schema should have properties")
			for _, propName := range tt.wantProps {
				assert.Contains(t, props, propName, "schema should have property %s", propName)
			}

			// Step 2: Marshal schema to JSON bytes (as server does).
			schemaBytes, err := json.Marshal(schema)
			require.NoError(t, err, "schema marshal should succeed")

			// Step 3: Create MCP tool with raw schema (as server does).
			tool := mcp.NewToolWithRawSchema(
				"test_tool",
				"Test tool",
				schemaBytes,
			)

			// Step 4: Marshal tool to JSON (simulates sending over MCP protocol).
			toolJSON, err := json.Marshal(tool)
			require.NoError(t, err, "tool marshal should succeed")

			// Step 5: Unmarshal tool from JSON (simulates client receiving tool).
			var receivedTool mcp.Tool
			err = json.Unmarshal(toolJSON, &receivedTool)
			require.NoError(t, err, "tool unmarshal should succeed")

			// Step 6: Verify received tool has correct InputSchema.
			assert.Equal(t, "test_tool", receivedTool.Name)
			assert.Equal(t, "Test tool", receivedTool.Description)

			// Step 7: Check InputSchema.Type is populated correctly.
			assert.Equal(t, tt.wantType, receivedTool.InputSchema.Type,
				"received tool InputSchema.Type should be %s", tt.wantType)

			// Step 8: Verify InputSchema.Properties is populated.
			assert.NotNil(t, receivedTool.InputSchema.Properties,
				"received tool InputSchema.Properties should not be nil")

			for _, propName := range tt.wantProps {
				assert.Contains(t, receivedTool.InputSchema.Properties, propName,
					"received tool should have property %s", propName)
			}

			// Step 9: Marshal the received InputSchema and verify full schema.
			receivedSchemaBytes, err := json.Marshal(receivedTool.InputSchema)
			require.NoError(t, err)

			var receivedSchema map[string]interface{}
			err = json.Unmarshal(receivedSchemaBytes, &receivedSchema)
			require.NoError(t, err)

			// Verify type is present in marshaled schema.
			assert.Equal(t, tt.wantType, receivedSchema["type"],
				"marshaled schema should have type=%s", tt.wantType)

			// Run custom schema checks.
			if tt.checkSchema != nil {
				tt.checkSchema(t, receivedSchema)
			}
		})
	}
}

// TestSchemaWithoutDollarRefDefs verifies that schemas don't use $ref/$defs.
func TestSchemaWithoutDollarRefDefs(t *testing.T) {
	schema, err := generateInputSchema(ServiceHealthInput{})
	require.NoError(t, err)

	// Schema should NOT have $ref or $defs at the root level.
	_, hasRef := schema["$ref"]
	assert.False(t, hasRef, "schema should not have $ref at root level")

	_, hasDefs := schema["$defs"]
	assert.False(t, hasDefs, "schema should not have $defs at root level")

	// Type and properties should be at root level.
	assert.Equal(t, "object", schema["type"])
	assert.NotNil(t, schema["properties"])
}

// TestAllToolInputTypes validates schemas for all tool input types.
func TestAllToolInputTypes(t *testing.T) {
	toolTypes := map[string]interface{}{
		"ServiceHealthInput":      ServiceHealthInput{},
		"ServiceTopologyInput":    ServiceTopologyInput{},
		"QueryEventsInput":        QueryEventsInput{},
		"BeylaHTTPMetricsInput":   BeylaHTTPMetricsInput{},
		"BeylaGRPCMetricsInput":   BeylaGRPCMetricsInput{},
		"BeylaSQLMetricsInput":    BeylaSQLMetricsInput{},
		"BeylaTracesInput":        BeylaTracesInput{},
		"TraceByIDInput":          TraceByIDInput{},
		"TelemetrySpansInput":     TelemetrySpansInput{},
		"TelemetryMetricsInput":   TelemetryMetricsInput{},
		"TelemetryLogsInput":      TelemetryLogsInput{},
		"StartEBPFCollectorInput": StartEBPFCollectorInput{},
		"StopEBPFCollectorInput":  StopEBPFCollectorInput{},
		"ListEBPFCollectorsInput": ListEBPFCollectorsInput{},
		"ExecCommandInput":        ExecCommandInput{},
		"ShellStartInput":         ShellStartInput{},
	}

	for name, inputType := range toolTypes {
		t.Run(name, func(t *testing.T) {
			// Generate schema.
			schema, err := generateInputSchema(inputType)
			require.NoError(t, err, "schema generation should succeed for %s", name)

			// Verify type at root level.
			schemaType, ok := schema["type"].(string)
			require.True(t, ok, "%s schema should have type at root", name)
			assert.Equal(t, "object", schemaType, "%s schema type should be object", name)

			// Verify properties exist.
			_, ok = schema["properties"]
			assert.True(t, ok, "%s schema should have properties", name)

			// Marshal and unmarshal to verify JSON round-trip.
			schemaBytes, err := json.Marshal(schema)
			require.NoError(t, err, "%s schema marshal should succeed", name)

			var unmarshaled map[string]interface{}
			err = json.Unmarshal(schemaBytes, &unmarshaled)
			require.NoError(t, err, "%s schema unmarshal should succeed", name)

			assert.Equal(t, "object", unmarshaled["type"], "%s unmarshaled schema should have type", name)
		})
	}
}

// BenchmarkSchemaGeneration benchmarks schema generation performance.
func BenchmarkSchemaGeneration(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = generateInputSchema(BeylaHTTPMetricsInput{})
	}
}

// BenchmarkToolMarshalUnmarshal benchmarks the full marshal/unmarshal cycle.
func BenchmarkToolMarshalUnmarshal(b *testing.B) {
	schema, _ := generateInputSchema(BeylaHTTPMetricsInput{})
	schemaBytes, _ := json.Marshal(schema)
	tool := mcp.NewToolWithRawSchema("bench_tool", "Benchmark tool", schemaBytes)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		toolJSON, _ := json.Marshal(tool)
		var receivedTool mcp.Tool
		_ = json.Unmarshal(toolJSON, &receivedTool)
	}
}
