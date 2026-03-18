package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock Colony Server for SDK tests
type mockColonyServer struct {
	lastRequest map[string]any
}

func (m *mockColonyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	m.lastRequest = body

	// Default success response for DeployCorrelation
	resp := map[string]any{
		"correlationId": "test-corr-123",
		"agentId":       "test-agent-1",
		"success":       true,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// TestLatencyTrapSkillIntegration verifies that the latencyTrap skill in the SDK
// correctly calls the DeployCorrelation RPC on the colony.
func TestLatencyTrapSkillIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Deno integration test in short mode")
	}

	mock := &mockColonyServer{}
	ts := httptest.NewServer(mock)
	defer ts.Close()

	// Use CORAL_COLONY_ENDPOINT to override resolver in ExecuteInline
	os.Setenv("CORAL_COLONY_ENDPOINT", ts.URL)
	defer os.Unsetenv("CORAL_COLONY_ENDPOINT")

	srv := &Server{
		logger: testLogger(t),
		config: Config{ColonyID: "test"},
	}

	// Code that uses the latencyTrap skill.
	code := `
import { latencyTrap } from "@coral/sdk/skills/latency-trap";

const result = await latencyTrap({
  service: "test-service",
  function: "main.handleRequest",
  threshold_ns: 100000000
});

console.log(JSON.stringify(result));
`

	result, err := srv.executeRunTool(context.Background(), RunInput{Code: code})
	require.NoError(t, err)
	require.False(t, result.IsError, "Tool error: %s", result.Content)

	// Verify the script's output
	require.NotEmpty(t, result.Content, "Tool result has no content")
	textContent, ok := result.Content[0].(mcpgo.TextContent)
	require.True(t, ok, "Tool result is not text content: %T", result.Content[0])

	var skillResult map[string]any
	err = json.Unmarshal([]byte(textContent.Text), &skillResult)
	require.NoError(t, err)
	assert.Equal(t, "healthy", skillResult["status"])
	assert.Contains(t, skillResult["summary"], "successfully")

	// Verify the RPC call made by the SDK to the mock colony
	require.NotNil(t, mock.lastRequest, "No RPC call recorded by mock server")

	// Check the descriptor sent to the colony
	desc, ok := mock.lastRequest["descriptor"].(map[string]any)
	require.True(t, ok, "Descriptor missing in RPC call")
	assert.Equal(t, "percentile_alarm", desc["strategy"])
	assert.Equal(t, "duration_ns", desc["field"])

	source, ok := desc["source"].(map[string]any)
	require.True(t, ok, "Source missing in descriptor")
	assert.Equal(t, "main.handleRequest", source["probe"])
}

// TestDeployCorrelationToolSchema verifies the schema for the coral_deploy_correlation tool.
func TestDeployCorrelationToolSchema(t *testing.T) {
	srv := &Server{
		logger: testLogger(t),
		config: Config{ColonyID: "test"},
	}

	schemas := srv.getToolSchemas()
	schemaJSON, ok := schemas["coral_deploy_correlation"]
	require.True(t, ok, "coral_deploy_correlation missing from schemas")

	var schema map[string]any
	err := json.Unmarshal([]byte(schemaJSON), &schema)
	require.NoError(t, err)

	props := schema["properties"].(map[string]any)
	assert.Contains(t, props, "action")
}

// TestDeployCorrelationToolDirect verifies that the coral_deploy_correlation tool
// correctly processes input and would (in a real scenario) call the Colony API.
func TestDeployCorrelationToolDirect(t *testing.T) {
	srv := &Server{
		logger: testLogger(t),
		config: Config{ColonyID: "test"},
	}

	input := DeployCorrelationInput{
		Service:    "test-service",
		Strategy:   "rate_gate",
		Action:     "emit_event",
		Probe:      stringPtr("http.request"),
		FilterExpr: stringPtr("request.path == '/api'"),
	}

	// We can't easily test the full RPC path here without a real registry/client,
	// but we can verify it doesn't panic and handles missing services.
	result, err := srv.executeDeployCorrelation(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcpgo.TextContent).Text, "debug service not available")
}

func stringPtr(s string) *string {
	return &s
}
