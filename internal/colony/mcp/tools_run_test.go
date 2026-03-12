package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestRunToolSchema verifies that coral_run is present in getToolSchemas.
func TestRunToolSchema(t *testing.T) {
	srv := &Server{
		logger: testLogger(t),
		config: Config{ColonyID: "test"},
	}

	schemas := srv.getToolSchemas()

	schemaJSON, ok := schemas["coral_run"]
	if !ok {
		t.Fatal("coral_run missing from getToolSchemas()")
	}

	var schema map[string]any
	if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
		t.Fatalf("coral_run schema is invalid JSON: %v", err)
	}

	if schema["type"] != "object" {
		t.Errorf("expected type=object, got %v", schema["type"])
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema missing properties")
	}

	if _, ok := props["code"]; !ok {
		t.Error("schema missing 'code' property")
	}

	t.Logf("coral_run schema: %s", schemaJSON)
}

// TestRunToolDescription verifies that coral_run has a description.
func TestRunToolDescription(t *testing.T) {
	srv := &Server{
		logger: testLogger(t),
		config: Config{ColonyID: "test"},
	}

	descriptions := srv.getToolDescriptions()
	desc, ok := descriptions["coral_run"]
	if !ok {
		t.Fatal("coral_run missing from getToolDescriptions()")
	}
	if !strings.Contains(desc, "coral://sdk/reference") {
		t.Errorf("description should mention coral://sdk/reference, got: %s", desc)
	}
}

// TestRunToolInListNames verifies that coral_run appears in listToolNames.
func TestRunToolInListNames(t *testing.T) {
	srv := &Server{
		logger: testLogger(t),
		config: Config{ColonyID: "test"},
	}

	names := srv.listToolNames()
	found := false
	for _, n := range names {
		if n == "coral_run" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("coral_run not in listToolNames(): %v", names)
	}
}

// TestSDKReferenceContent verifies the SDK reference text is non-empty and
// contains the expected sections.
func TestSDKReferenceContent(t *testing.T) {
	if sdkReferenceText == "" {
		t.Fatal("sdkReferenceText is empty")
	}

	required := []string{
		"@coral/sdk",
		"PRIMITIVES",
		"SKILLS",
		"latency-report",
		"error-correlation",
		"memory-leak-detector",
		"OUTPUT",
	}
	for _, s := range required {
		if !strings.Contains(sdkReferenceText, s) {
			t.Errorf("sdkReferenceText missing expected string: %q", s)
		}
	}
}

// TestExecuteRunToolEmptyCode verifies that an empty code string returns an
// error tool result.
func TestExecuteRunToolEmptyCode(t *testing.T) {
	srv := &Server{
		logger: testLogger(t),
		config: Config{ColonyID: "test"},
	}

	result, err := srv.executeRunTool(context.Background(), RunInput{Code: "   "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for empty code")
	}
}

// TestExecuteRunToolSimple runs a trivial script and verifies stdout capture.
// This test requires the Deno binary to be available (embedded or on PATH).
// It is skipped when Deno is not available.
func TestExecuteRunToolSimple(t *testing.T) {
	// This is an integration test that requires Deno.
	// Skip in short mode to keep CI fast.
	if testing.Short() {
		t.Skip("skipping Deno execution test in short mode")
	}

	srv := &Server{
		logger: testLogger(t),
		config: Config{ColonyID: "test"},
	}

	code := `console.log(JSON.stringify({ summary: "ok", status: "healthy", data: {} }));`
	result, err := srv.executeRunTool(context.Background(), RunInput{Code: code})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}

	if len(result.Content) == 0 {
		t.Fatal("no content returned")
	}
}
