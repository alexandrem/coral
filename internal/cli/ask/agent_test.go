package ask

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coral-mesh/coral/internal/config"
)

func TestParseHealthAlerts(t *testing.T) {
	// buildSummary constructs a coral_query_summary-style text block.
	buildSummary := func(blocks ...string) string {
		return "Service Health Summary:\n\n" + strings.Join(blocks, "\n\n") + "\n\n"
	}
	healthy := "✅ api-gateway (ebpf)\n   Status: OK\n   Requests: 500\n   Error Rate: 0.00%\n   Avg Latency: 12.50ms\n"
	idle := "💤 worker (ebpf)\n   Status: IDLE\n   Requests: 0\n   Error Rate: 0.00%\n   Avg Latency: 0.00ms\n"
	degraded := "⚠️ user-service (registered+ebpf)\n   Status: DEGRADED\n   Requests: 50\n   Error Rate: 23.50%\n   Avg Latency: 1200.00ms\n"
	critical := "❌ db-proxy (registered)\n   Status: CRITICAL\n   Requests: 12\n   Error Rate: 98.00%\n   Avg Latency: 5000.00ms\n"

	t.Run("empty input", func(t *testing.T) {
		assert.Equal(t, "", parseHealthAlerts(""))
	})

	t.Run("all healthy services", func(t *testing.T) {
		assert.Equal(t, "", parseHealthAlerts(buildSummary(healthy, idle)))
	})

	t.Run("single degraded service", func(t *testing.T) {
		result := parseHealthAlerts(buildSummary(healthy, degraded))
		assert.Contains(t, result, "⚠️ user-service")
		assert.Contains(t, result, "Status: DEGRADED")
		assert.Contains(t, result, "Error Rate: 23.50%")
		assert.Contains(t, result, "Avg Latency: 1200.00ms")
		assert.Contains(t, result, "Requests: 50")
		assert.NotContains(t, result, "api-gateway")
	})

	t.Run("single critical service", func(t *testing.T) {
		result := parseHealthAlerts(buildSummary(critical))
		assert.Contains(t, result, "❌ db-proxy")
		assert.Contains(t, result, "Status: CRITICAL")
		assert.Contains(t, result, "Error Rate: 98.00%")
	})

	t.Run("mixed: healthy and degraded and critical", func(t *testing.T) {
		result := parseHealthAlerts(buildSummary(healthy, degraded, idle, critical))
		assert.Contains(t, result, "⚠️ user-service")
		assert.Contains(t, result, "❌ db-proxy")
		assert.NotContains(t, result, "api-gateway")
		assert.NotContains(t, result, "worker")
		// Each alert is on its own line.
		lines := strings.Split(strings.TrimSpace(result), "\n")
		assert.Equal(t, 2, len(lines))
	})

	t.Run("regression and host resource lines are excluded", func(t *testing.T) {
		withExtras := "⚠️ svc (ebpf)\n   Status: DEGRADED\n   Requests: 10\n   Error Rate: 50.00%\n   Avg Latency: 800.00ms\n   Host Resources:\n     CPU: 95%\n   Regressions:\n     ⚠️  error rate spike\n"
		result := parseHealthAlerts(buildSummary(withExtras))
		assert.Contains(t, result, "⚠️ svc")
		assert.Contains(t, result, "Error Rate: 50.00%")
		assert.NotContains(t, result, "Host Resources")
		assert.NotContains(t, result, "CPU:")
		assert.NotContains(t, result, "error rate spike")
	})

	t.Run("header-only input (no service blocks)", func(t *testing.T) {
		assert.Equal(t, "", parseHealthAlerts("Service Health Summary:\n\n"))
	})
}

func TestCompactServiceAlert(t *testing.T) {
	t.Run("header only", func(t *testing.T) {
		result := compactServiceAlert("⚠️ svc (ebpf)", nil)
		assert.Equal(t, "⚠️ svc (ebpf)", result)
	})

	t.Run("includes all key fields", func(t *testing.T) {
		lines := []string{
			"   Status: DEGRADED",
			"   Requests: 50",
			"   Error Rate: 23.50%",
			"   Avg Latency: 1200.00ms",
		}
		result := compactServiceAlert("⚠️ user-service (ebpf)", lines)
		assert.Equal(t, "⚠️ user-service (ebpf) | Status: DEGRADED | Requests: 50 | Error Rate: 23.50% | Avg Latency: 1200.00ms", result)
	})

	t.Run("ignores non-key lines", func(t *testing.T) {
		lines := []string{
			"   Status: CRITICAL",
			"   Host Resources:",
			"     CPU: 99%",
			"   Regressions:",
			"     ⚠️  latency spike",
		}
		result := compactServiceAlert("❌ db-proxy (registered)", lines)
		assert.Equal(t, "❌ db-proxy (registered) | Status: CRITICAL", result)
	})

	t.Run("handles leading whitespace in lines", func(t *testing.T) {
		lines := []string{"   Error Rate: 5.00%", "   Avg Latency: 200.00ms"}
		result := compactServiceAlert("⚠️ svc (ebpf)", lines)
		assert.Contains(t, result, "Error Rate: 5.00%")
		assert.Contains(t, result, "Avg Latency: 200.00ms")
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
