package ask

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/llm"
)

// TestCLIDispatchMode verifies that an agent created in CLI dispatch mode:
//   - Does not attempt an MCP connection during construction.
//   - Has exactly one tool registered: coral_cli.
//   - Routes a simple question through the mock LLM without error when the
//     LLM returns a direct answer (no tool calls required).
func TestCLIDispatchMode(t *testing.T) {
	// Mock LLM script: single interaction, no tool calls (direct answer).
	script := llm.MockScript{
		Interactions: []llm.MockInteraction{
			{
				Response: llm.MockResponse{
					Content: "The coral CLI is your debugging companion.",
				},
			},
		},
	}

	scriptData, err := json.Marshal(script)
	require.NoError(t, err)

	scriptPath := filepath.Join(t.TempDir(), "cli_dispatch_direct.json")
	require.NoError(t, os.WriteFile(scriptPath, scriptData, 0o600))

	askCfg := &config.AskConfig{
		DefaultModel: "mock:" + scriptPath,
		Agent: config.AskAgentConfig{
			DispatchMode: config.DispatchModeCLI,
		},
	}
	colonyCfg := &config.ColonyConfig{
		ColonyID: "test-colony",
	}

	// Creating the agent must succeed without contacting the MCP server.
	agent, err := NewAgentWithCLIReference(askCfg, colonyCfg, false, "# cli reference stub")
	require.NoError(t, err, "CLI dispatch mode must not attempt MCP connection")

	// Internal dispatch mode must be set correctly.
	assert.Equal(t, config.DispatchModeCLI, agent.dispatchMode)

	// Ask a simple question — the mock LLM answers directly without tool calls.
	resp, err := agent.Ask(t.Context(), "What is coral?", "conv-cli", false, false)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, strings.Contains(resp.Answer, "coral CLI"), "answer should match mock content")

	// No tool calls were made (direct answer path).
	assert.Empty(t, resp.ToolCalls)
}

// TestCLIDispatchToolsContract verifies that CLI dispatch mode registers exactly
// the coral_cli tool with the correct schema (not the 21 MCP tools).
func TestCLIDispatchToolsContract(t *testing.T) {
	tools := buildCLITools()
	require.Len(t, tools, 1, "CLI dispatch mode must expose exactly one tool")
	assert.Equal(t, "coral_cli", tools[0].Name)
	assert.Contains(t, tools[0].Description, "coral CLI command")

	var schema map[string]any
	require.NoError(t, json.Unmarshal(tools[0].RawInputSchema, &schema))

	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok, "schema must have properties")
	_, hasArgs := props["args"]
	assert.True(t, hasArgs, "schema must define the args property")
}
