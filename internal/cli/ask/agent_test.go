package ask

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coral-mesh/coral/internal/config"
)

// fakeCommandRunner is a test double for commandRunner (RFD 114), letting
// bootstrap parsing be tested independently of both dispatch-mode transports.
type fakeCommandRunner struct {
	output string
	err    error
}

func TestBootstrapTransportParity(t *testing.T) {
	const servicesJSON = `{"services":[{"name":"api"},{"name":"checkout"}]}`
	const summaryJSON = `[{"service_name":"api","status":"critical","error_rate":50,"avg_latency_ms":900,"request_count":10}]`

	// The subprocess fixture verifies JSON format is appended on the CLI path.
	script := `#!/bin/sh
test "$CORAL_COLONY_ID" = selected || { echo 'wrong colony' >&2; exit 1; }
case "$*" in
  'services --format json') printf '%s' '` + servicesJSON + `' ;;
  'query summary --since 5m --format json') printf '%s' '` + summaryJSON + `' ;;
  'query topology --format json') printf '%s' '{"connections":[]}' ;;
  *) echo 'unexpected command' >&2; exit 1 ;;
esac
`
	path := filepath.Join(t.TempDir(), "coral")
	require.NoError(t, os.WriteFile(path, []byte(script), 0o700))
	previous := coralExecutable
	coralExecutable = func() (string, error) { return path, nil }
	t.Cleanup(func() { coralExecutable = previous })

	mcpServer := server.NewMCPServer("bootstrap-test", "1.0", server.WithToolCapabilities(false))
	var calls [][]string
	mcpServer.AddTool(buildCLITools()[0], func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		assert.Equal(t, "coral_cli", req.Params.Name)
		var args []string
		for _, arg := range req.GetArguments()["args"].([]any) {
			args = append(args, arg.(string))
		}
		calls = append(calls, args)
		switch strings.Join(args, " ") {
		case "services":
			return mcp.NewToolResultText(servicesJSON), nil
		case "query summary --since 5m":
			return mcp.NewToolResultText(summaryJSON), nil
		default:
			return mcp.NewToolResultError("unexpected command"), nil
		}
	})
	mcpClient, err := client.NewInProcessClient(mcpServer)
	require.NoError(t, err)
	t.Cleanup(func() { _ = mcpClient.Close() })
	require.NoError(t, mcpClient.Start(t.Context()))
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "1.0"}
	_, err = mcpClient.Initialize(t.Context(), initReq)
	require.NoError(t, err)

	t.Setenv("CORAL_COLONY_ID", "other-colony")
	cliPrompt := (&Agent{dispatchMode: config.DispatchModeCLI, colonyConfig: &config.ColonyConfig{ColonyID: "selected"}}).buildSystemPrompt(t.Context())
	mcpPrompt := (&Agent{dispatchMode: config.DispatchModeMCP, mcpClient: mcpClient}).buildSystemPrompt(t.Context())
	for _, prompt := range []string{cliPrompt, mcpPrompt} {
		assert.Contains(t, prompt, "Available services: api, checkout")
		assert.Contains(t, prompt, "❌ api | Status: critical")
		assert.Contains(t, prompt, `["triage", "<service>", "--attach"] only when the user asked to instrument`)
	}
	assert.Equal(t, [][]string{{"services"}, {"query", "summary", "--since", "5m"}}, calls)

	// A subprocess exit failure is an MCP IsError result with a nil Go error.
	_, err = (&cliCommandRunner{colonyID: "selected"}).run(t.Context(), []string{"invalid"})
	require.ErrorContains(t, err, "unexpected command")
	_, err = (&mcpCommandRunner{client: mcpClient}).run(t.Context(), []string{"invalid"})
	require.ErrorContains(t, err, "unexpected command")
	assert.Equal(t, "other-colony", os.Getenv("CORAL_COLONY_ID"), "subprocess scoping must not change the parent environment")
}

func TestCommandTextContentFailures(t *testing.T) {
	for _, result := range []*mcp.CallToolResult{nil, {}, mcp.NewToolResultError("permission denied")} {
		_, err := commandTextContent(result)
		require.Error(t, err, fmt.Sprintf("result: %v", result))
	}
}

func (f *fakeCommandRunner) run(_ context.Context, _ []string) (string, error) {
	return f.output, f.err
}

func TestResolveDispatchMode(t *testing.T) {
	t.Run("unset defaults to cli", func(t *testing.T) {
		assert.Equal(t, config.DispatchModeCLI, resolveDispatchMode(""))
	})

	t.Run("explicit mcp is preserved", func(t *testing.T) {
		assert.Equal(t, config.DispatchModeMCP, resolveDispatchMode(config.DispatchModeMCP))
	})

	t.Run("explicit cli is preserved", func(t *testing.T) {
		assert.Equal(t, config.DispatchModeCLI, resolveDispatchMode(config.DispatchModeCLI))
	})
}

func TestBootstrapServiceContext(t *testing.T) {
	t.Run("real services JSON fixture", func(t *testing.T) {
		runner := &fakeCommandRunner{output: `{"services":[{"name":"api"},{"name":"checkout"}]}`}
		result := bootstrapServiceContext(t.Context(), runner, false)
		assert.Equal(t, "api, checkout", result)
	})

	t.Run("command failure", func(t *testing.T) {
		runner := &fakeCommandRunner{err: errors.New("coral services: exit status 1")}
		result := bootstrapServiceContext(t.Context(), runner, false)
		assert.Equal(t, "(service list unavailable)", result)
	})

	t.Run("malformed JSON", func(t *testing.T) {
		runner := &fakeCommandRunner{output: "not json"}
		result := bootstrapServiceContext(t.Context(), runner, false)
		assert.Equal(t, "(no services registered)", result)
	})
}

func TestBootstrapHealthAlerts(t *testing.T) {
	t.Run("real query summary JSON fixture", func(t *testing.T) {
		runner := &fakeCommandRunner{
			output: `[{"service_name":"api","status":"critical","error_rate":50,"avg_latency_ms":900,"request_count":10}]`,
		}
		result := bootstrapHealthAlerts(t.Context(), runner, false)
		assert.Contains(t, result, "❌ api")
		assert.Contains(t, result, "Status: critical")
	})

	t.Run("command failure", func(t *testing.T) {
		runner := &fakeCommandRunner{err: errors.New("coral query summary: exit status 1")}
		result := bootstrapHealthAlerts(t.Context(), runner, false)
		assert.Equal(t, "", result)
	})

	t.Run("malformed JSON", func(t *testing.T) {
		runner := &fakeCommandRunner{output: "not json"}
		result := bootstrapHealthAlerts(t.Context(), runner, false)
		assert.Equal(t, "", result)
	})
}

func TestFormatHealthAlerts(t *testing.T) {
	// buildSummaryJSON constructs a "coral query summary --format json" fixture:
	// an array of summary objects, matching printSummaryJSON's real output shape
	// (RFD 114), not the retired MCP tool's text output.
	buildSummaryJSON := func(entries ...string) string {
		return "[" + strings.Join(entries, ",") + "]"
	}
	healthy := `{"service_name":"api-gateway","status":"healthy","request_count":500,"error_rate":0,"avg_latency_ms":12.5}`
	degraded := `{"service_name":"user-service","status":"degraded","request_count":50,"error_rate":23.5,"avg_latency_ms":1200}`
	critical := `{"service_name":"db-proxy","status":"critical","request_count":12,"error_rate":98,"avg_latency_ms":5000}`

	t.Run("empty input", func(t *testing.T) {
		assert.Equal(t, "", formatHealthAlerts(""))
	})

	t.Run("invalid JSON", func(t *testing.T) {
		assert.Equal(t, "", formatHealthAlerts("not json"))
	})

	t.Run("empty array", func(t *testing.T) {
		assert.Equal(t, "", formatHealthAlerts("[]"))
	})

	t.Run("all healthy services", func(t *testing.T) {
		assert.Equal(t, "", formatHealthAlerts(buildSummaryJSON(healthy)))
	})

	t.Run("single degraded service", func(t *testing.T) {
		result := formatHealthAlerts(buildSummaryJSON(healthy, degraded))
		assert.Contains(t, result, "⚠️ user-service")
		assert.Contains(t, result, "Status: degraded")
		assert.Contains(t, result, "Error Rate: 23.50%")
		assert.Contains(t, result, "Avg Latency: 1200.00ms")
		assert.Contains(t, result, "Requests: 50")
		assert.NotContains(t, result, "api-gateway")
	})

	t.Run("single critical service", func(t *testing.T) {
		result := formatHealthAlerts(buildSummaryJSON(critical))
		assert.Contains(t, result, "❌ db-proxy")
		assert.Contains(t, result, "Status: critical")
		assert.Contains(t, result, "Error Rate: 98.00%")
	})

	t.Run("mixed: healthy and degraded and critical", func(t *testing.T) {
		result := formatHealthAlerts(buildSummaryJSON(healthy, degraded, critical))
		assert.Contains(t, result, "⚠️ user-service")
		assert.Contains(t, result, "❌ db-proxy")
		assert.NotContains(t, result, "api-gateway")
		// Each alert is on its own line.
		lines := strings.Split(strings.TrimSpace(result), "\n")
		assert.Equal(t, 2, len(lines))
	})
}

func TestFormatServiceContext(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		assert.Equal(t, "(no services registered)", formatServiceContext(""))
	})

	t.Run("invalid JSON", func(t *testing.T) {
		assert.Equal(t, "(no services registered)", formatServiceContext("not json"))
	})

	t.Run("no services registered", func(t *testing.T) {
		assert.Equal(t, "(no services registered)", formatServiceContext(`{"services":[]}`))
	})

	t.Run("lists service names", func(t *testing.T) {
		result := formatServiceContext(`{"services":[{"name":"api"},{"name":"checkout"}]}`)
		assert.Equal(t, "api, checkout", result)
	})
}

func TestFormatCompactCallGraph(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		assert.Equal(t, "", formatCompactCallGraph(""))
	})

	t.Run("header only — no edges", func(t *testing.T) {
		assert.Equal(t, "", formatCompactCallGraph("Service call graph (last 1h):"))
	})

	t.Run("no cross-service calls", func(t *testing.T) {
		// The tool returns this when there are no connections.
		input := "Service call graph (last 1h):\n(no cross-service calls observed)"
		assert.Equal(t, "", formatCompactCallGraph(input))
	})

	t.Run("single edge", func(t *testing.T) {
		input := "Service call graph (last 1h):\napi-gateway → user-service (HTTP, 2341 calls, last: 2s ago)"
		result := formatCompactCallGraph(input)
		assert.Equal(t, "Call graph: api-gateway→user-service (HTTP)", result)
	})

	t.Run("multiple edges", func(t *testing.T) {
		input := strings.Join([]string{
			"Service call graph (last 1h):",
			"api-gateway → user-service (HTTP, 2341 calls, last: 2s ago)",
			"user-service → postgres (SQL, 1823 calls, last: 5s ago)",
			"worker → queue (gRPC, 234 calls, last: 1m ago)",
		}, "\n")
		result := formatCompactCallGraph(input)
		assert.Equal(t, "Call graph: api-gateway→user-service (HTTP), user-service→postgres (SQL), worker→queue (gRPC)", result)
	})

	t.Run("service names with hyphens and dots", func(t *testing.T) {
		input := "Service call graph (last 30m):\nmy-svc.v2 → db.primary (HTTP, 5 calls, last: 10s ago)"
		result := formatCompactCallGraph(input)
		assert.Equal(t, "Call graph: my-svc.v2→db.primary (HTTP)", result)
	})

	t.Run("blank lines are ignored", func(t *testing.T) {
		input := "Service call graph (last 1h):\n\napi → db (HTTP, 1 calls, last: 1s ago)\n\n"
		result := formatCompactCallGraph(input)
		assert.Equal(t, "Call graph: api→db (HTTP)", result)
	})
}

func TestFormatCompactCallGraphFromJSON(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		assert.Equal(t, "", formatCompactCallGraphFromJSON(""))
	})

	t.Run("invalid JSON", func(t *testing.T) {
		assert.Equal(t, "", formatCompactCallGraphFromJSON("not json"))
	})

	t.Run("empty connections array", func(t *testing.T) {
		assert.Equal(t, "", formatCompactCallGraphFromJSON(`{"colony_id":"c1","connections":[]}`))
	})

	t.Run("single connection", func(t *testing.T) {
		input := `{"colony_id":"c1","connections":[{"from":"api","to":"db","protocol":"HTTP"}]}`
		assert.Equal(t, "Call graph: api→db (HTTP)", formatCompactCallGraphFromJSON(input))
	})

	t.Run("multiple connections", func(t *testing.T) {
		input := `{"colony_id":"c1","connections":[` +
			`{"from":"api-gateway","to":"user-service","protocol":"HTTP"},` +
			`{"from":"worker","to":"queue","protocol":"gRPC"}` +
			`]}`
		assert.Equal(t, "Call graph: api-gateway→user-service (HTTP), worker→queue (gRPC)", formatCompactCallGraphFromJSON(input))
	})

	t.Run("service names with hyphens and dots", func(t *testing.T) {
		input := `{"connections":[{"from":"my-svc.v2","to":"db.primary","protocol":"HTTP"}]}`
		assert.Equal(t, "Call graph: my-svc.v2→db.primary (HTTP)", formatCompactCallGraphFromJSON(input))
	})

	t.Run("missing connections key", func(t *testing.T) {
		assert.Equal(t, "", formatCompactCallGraphFromJSON(`{"colony_id":"c1"}`))
	})
}

func TestConversationPersistence(t *testing.T) {
	// Setup minimalist agent
	askCfg := &config.AskConfig{
		DefaultModel: "mock:script",
	}
	colonyCfg := &config.ColonyConfig{
		ColonyID: "p-test",
	}

	// We can't easily use NewAgent because it tries to connect to MCP.
	// However, SetConversationHistory and GetConversationHistory only depend on the struct fields.
	// So we can instantiate the struct directly for THIS specific unit test content.
	// If the methods grew to depend on other things, we'd need a proper constructor or mocks.
	agent := &Agent{
		config:        askCfg,
		colonyConfig:  colonyCfg,
		conversations: make(map[string]*Conversation),
		debug:         true,
	}

	t.Run("SetConversationHistory", func(t *testing.T) {
		messages := []Message{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
		}
		conversationID := "conv-123"

		agent.SetConversationHistory(conversationID, messages)

		// Verify internal state
		require.Contains(t, agent.conversations, conversationID)
		conv := agent.conversations[conversationID]
		// ID is private, but checking map key presence confirms it's stored correctly.
		msgs := conv.GetMessages()
		assert.Equal(t, 2, len(msgs))
		assert.Equal(t, "user", msgs[0].Role)
		assert.Equal(t, "Hello", msgs[0].Content)
	})

	t.Run("GetConversationHistory", func(t *testing.T) {
		conversationID := "conv-456"
		expectedMessages := []Message{
			{Role: "user", Content: "Question"},
			{Role: "assistant", Content: "Answer"},
		}

		// Pre-populate
		agent.SetConversationHistory(conversationID, expectedMessages)

		// Retrieve
		history := agent.GetConversationHistory(conversationID)

		assert.NotNil(t, history)
		assert.Equal(t, len(expectedMessages), len(history))
		assert.Equal(t, expectedMessages[0].Content, history[0].Content)
	})

	t.Run("GetNonExistentContracts", func(t *testing.T) {
		history := agent.GetConversationHistory("non-existent")
		assert.Nil(t, history)
	})
}
