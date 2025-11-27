package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/coral-mesh/coral/internal/logging"
)

// TestToolSchemaPreservation tests that tool schemas are preserved through
// registration and retrieval.
func TestToolSchemaPreservation(t *testing.T) {
	// Create a test schema (similar to what generateInputSchema produces)
	testSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"filter": map[string]any{
				"type":        "string",
				"description": "Optional filter",
			},
		},
		"additionalProperties": false,
	}

	// Marshal to JSON bytes
	schemaBytes, err := json.Marshal(testSchema)
	if err != nil {
		t.Fatalf("Failed to marshal test schema: %v", err)
	}

	t.Logf("Schema bytes (%d): %s", len(schemaBytes), string(schemaBytes))

	// Create tool using NewToolWithRawSchema (same as our code)
	tool := mcp.NewToolWithRawSchema(
		"test_tool",
		"Test tool description",
		schemaBytes,
	)

	// Verify tool was created correctly
	if len(tool.RawInputSchema) == 0 {
		t.Errorf("Expected RawInputSchema to be set, got empty")
	}
	if len(tool.RawInputSchema) != len(schemaBytes) {
		t.Errorf("RawInputSchema length mismatch: got %d, want %d",
			len(tool.RawInputSchema), len(schemaBytes))
	}

	// Test marshaling the tool
	toolJSON, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("Failed to marshal tool: %v", err)
	}

	t.Logf("Tool marshaled: %s", string(toolJSON))

	// Unmarshal to verify schema is preserved
	var unmarshaled mcp.Tool
	if err := json.Unmarshal(toolJSON, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal tool: %v", err)
	}

	// Check that InputSchema.Type was populated
	if unmarshaled.InputSchema.Type != "object" {
		t.Errorf("Expected InputSchema.Type='object', got '%s'", unmarshaled.InputSchema.Type)
	}

	// Check that properties were preserved
	if unmarshaled.InputSchema.Properties == nil {
		t.Errorf("Expected InputSchema.Properties to be set, got nil")
	}

	// Marshal the unmarshaled tool to see what client would see
	clientJSON, _ := json.Marshal(unmarshaled)
	t.Logf("After unmarshal (client view): %s", string(clientJSON))
}

// TestToolSchemaViaServer tests tool schema preservation through the MCP server.
func TestToolSchemaViaServer(t *testing.T) {
	// Create test schema
	testSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"test_param": map[string]any{
				"type":        "string",
				"description": "Test parameter",
			},
		},
	}

	schemaBytes, err := json.Marshal(testSchema)
	if err != nil {
		t.Fatalf("Failed to marshal test schema: %v", err)
	}

	// Create MCP server
	mcpServer := server.NewMCPServer(
		"test-server",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	// Create tool
	tool := mcp.NewToolWithRawSchema(
		"test_tool",
		"Test tool",
		schemaBytes,
	)

	t.Logf("Tool before AddTool: RawInputSchema len=%d, InputSchema.Type=%q",
		len(tool.RawInputSchema), tool.InputSchema.Type)

	// Register tool (same as our code does)
	mcpServer.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("test result"), nil
	})

	// Test marshaling after registration
	toolJSON, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("Failed to marshal registered tool: %v", err)
	}

	t.Logf("Tool after registration (marshaled): %s", string(toolJSON))

	// Unmarshal to simulate what client receives
	var receivedTool mcp.Tool
	if err := json.Unmarshal(toolJSON, &receivedTool); err != nil {
		t.Fatalf("Failed to unmarshal tool: %v", err)
	}

	t.Logf("Received tool: InputSchema.Type=%q, Properties=%v",
		receivedTool.InputSchema.Type, receivedTool.InputSchema.Properties)

	// Verify schema is preserved
	if receivedTool.InputSchema.Type == "" {
		t.Errorf("Schema lost! InputSchema.Type is empty")
	}
	if receivedTool.InputSchema.Type != "object" {
		t.Errorf("Expected InputSchema.Type='object', got '%s'", receivedTool.InputSchema.Type)
	}
}

// TestGenerateInputSchema tests our schema generation function.
func TestGenerateInputSchema(t *testing.T) {
	// Test with ServiceTopologyInput (the one showing empty schema)
	inputSchema, err := generateInputSchema(ServiceTopologyInput{})
	if err != nil {
		t.Fatalf("Failed to generate schema: %v", err)
	}

	// Should have type field
	schemaType, ok := inputSchema["type"]
	if !ok {
		t.Errorf("Schema missing 'type' field")
	}
	if schemaType != "object" {
		t.Errorf("Expected type='object', got '%v'", schemaType)
	}

	// Should have properties
	_, ok = inputSchema["properties"]
	if !ok {
		t.Errorf("Schema missing 'properties' field")
	}

	// Marshal and check
	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		t.Fatalf("Failed to marshal schema: %v", err)
	}

	t.Logf("Generated schema (%d bytes): %s", len(schemaBytes), string(schemaBytes))

	if len(schemaBytes) == 0 {
		t.Errorf("Schema is empty")
	}
}

// TestGetToolSchemas tests the RPC path that was broken.
// This would have caught the bug where getToolSchemas() used a different
// schema generator than generateInputSchema().
func TestGetToolSchemas(t *testing.T) {
	// Create a minimal MCP server for testing
	srv := &Server{
		logger: testLogger(t),
		config: Config{
			ColonyID: "test-colony",
		},
	}

	// Get schemas via the RPC path
	schemas := srv.getToolSchemas()

	// Verify we got schemas for all tools
	expectedTools := []string{
		"coral_get_service_health",
		"coral_get_service_topology",
		"coral_query_events",
		"coral_query_beyla_http_metrics",
		"coral_query_beyla_grpc_metrics",
		"coral_query_beyla_sql_metrics",
		"coral_query_beyla_traces",
		"coral_get_trace_by_id",
		"coral_query_telemetry_spans",
		"coral_query_telemetry_metrics",
		"coral_query_telemetry_logs",
		"coral_start_ebpf_collector",
		"coral_stop_ebpf_collector",
		"coral_list_ebpf_collectors",
		"coral_shell_exec",
		"coral_container_exec",
		"coral_list_services",
	}

	for _, toolName := range expectedTools {
		schemaJSON, ok := schemas[toolName]
		if !ok {
			t.Errorf("Missing schema for tool: %s", toolName)
			continue
		}

		// Parse the schema JSON
		var schema map[string]any
		if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
			t.Errorf("Invalid JSON schema for %s: %v", toolName, err)
			continue
		}

		// Verify it has the required fields
		schemaType, ok := schema["type"]
		if !ok {
			t.Errorf("Schema for %s missing 'type' field. Schema: %s", toolName, schemaJSON)
			continue
		}
		if schemaType != "object" {
			t.Errorf("Schema for %s has type=%v, expected 'object'", toolName, schemaType)
		}

		// Verify it doesn't have JSON Schema draft-specific fields
		// (these were causing the bug)
		if _, hasSchema := schema["$schema"]; hasSchema {
			t.Errorf("Schema for %s should not have '$schema' field (causes RPC issues)", toolName)
		}
		if _, hasID := schema["$id"]; hasID {
			t.Errorf("Schema for %s should not have '$id' field (causes RPC issues)", toolName)
		}

		// Verify properties exist
		if _, ok := schema["properties"]; !ok {
			t.Errorf("Schema for %s missing 'properties' field", toolName)
		}

		t.Logf("✓ %s: valid schema (%d bytes)", toolName, len(schemaJSON))
	}

	// Test the full GetToolMetadata path (what the RPC handler calls)
	metadata, err := srv.GetToolMetadata()
	if err != nil {
		t.Fatalf("GetToolMetadata failed: %v", err)
	}

	if len(metadata) == 0 {
		t.Error("GetToolMetadata returned no tools")
	}

	for _, meta := range metadata {
		// Verify the schema can be unmarshaled
		var schema map[string]any
		if err := json.Unmarshal([]byte(meta.InputSchemaJSON), &schema); err != nil {
			t.Errorf("Invalid schema JSON for %s: %v", meta.Name, err)
			continue
		}

		// This is what was failing before the fix: type was empty
		if schema["type"] == "" {
			t.Errorf("BUG REPRODUCED: %s has empty type field! Schema: %s",
				meta.Name, meta.InputSchemaJSON)
		}

		t.Logf("✓ %s metadata: type=%v, description=%s",
			meta.Name, schema["type"], meta.Description)
	}
}

// testLogger creates a no-op logger for tests
func testLogger(t *testing.T) logging.Logger {
	// Create a no-op logger that discards output
	return logging.New(logging.Config{
		Level:  "error", // Only log errors
		Pretty: false,
		Output: &testWriter{t},
	})
}

// testWriter discards output but can be used to capture errors
type testWriter struct {
	t *testing.T
}

func (w *testWriter) Write(p []byte) (n int, err error) {
	// Optionally log to test output: w.t.Log(string(p))
	return len(p), nil
}
