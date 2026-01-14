package distributed

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/coral-mesh/coral/internal/agent/llm"
	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// CLIAskSuite tests the 'coral ask' command.
type CLIAskSuite struct {
	E2EDistributedSuite

	cliEnv *helpers.CLITestEnv
}

// SetupSuite runs once before all tests in the suite.
func (s *CLIAskSuite) SetupSuite() {
	s.E2EDistributedSuite.SetupSuite()

	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	colonyID := "test-colony-e2e"

	s.cliEnv, err = helpers.SetupCLIEnv(s.ctx, colonyID, colonyEndpoint)
	s.Require().NoError(err, "Failed to setup CLI environment")

	// Ensure ~/.coral/conversations/test-colony-e2e exists for continuation tests
	// We must use the ConfigDir from the isolated CLI environment, not the real user home.
	convDir := filepath.Join(s.cliEnv.ConfigDir, "conversations", colonyID)
	err = os.MkdirAll(convDir, 0755)
	s.Require().NoError(err, "Failed to create conversation directory")
}

// TearDownSuite cleans up after all tests.
func (s *CLIAskSuite) TearDownSuite() {
	if s.cliEnv != nil {
		_ = s.cliEnv.Cleanup()
	}
	s.E2EDistributedSuite.TearDownSuite()
}

// TestAskBasicFlow tests a simple question using the mock provider.
func (s *CLIAskSuite) TestAskBasicFlow() {
	s.T().Log("Testing 'coral ask' basic flow...")

	// Create a mock script
	script := llm.MockScript{
		Interactions: []llm.MockInteraction{
			{
				ExpectedMessages: []llm.Message{
					{Role: "user", Content: "Hello, world!"},
				},
				Response: llm.MockResponse{
					Content: "Hello! How can I help you today?",
				},
			},
		},
	}

	scriptPath := s.createMockScript(script)
	defer os.Remove(scriptPath)

	// Run ask command
	result := helpers.RunAsk(s.ctx, s.cliEnv.EnvVars(), "Hello, world!", "mock:"+scriptPath, "")
	result.MustSucceed(s.T())

	s.Require().Contains(result.Output, "Hello! How can I help you today?")
}

// TestAskWithTools tests the agent executing a tool and returning an answer.
func (s *CLIAskSuite) TestAskWithTools() {
	s.T().Log("Testing 'coral ask' with tools...")

	// Create a mock script that simulates tool usage
	// 1. User asks to list services
	// 2. Assistant calls coral_list_services
	// 3. System returns tool result
	// 4. Assistant interprets result
	script := llm.MockScript{
		Interactions: []llm.MockInteraction{
			{
				// Step 1: LLM decides to call a tool
				ExpectedMessages: []llm.Message{
					{Role: "user", Content: "List all services"},
				},
				Response: llm.MockResponse{
					Content: "", // Empty content when calling tool
					ToolCalls: []llm.ToolCall{
						{id("call_1"), "coral_list_services", "{}"},
					},
				},
			},
			{
				// Step 2: LLM receives tool result and provides final answer
				// We expect to see User message, Assistant (tool call), and Tool result in history
				ExpectedMessages: []llm.Message{
					{Role: "user", Content: "List all services"},
					{Role: "assistant", Content: "", ToolResponses: nil}, // Previous step response
				},
				Response: llm.MockResponse{
					Content: "There seem to be no services registered currently.",
				},
			},
		},
	}

	// Adjust expectation for second interaction based on Agent implementation
	script.Interactions[1].ExpectedMessages = []llm.Message{
		{Role: "user", Content: "List all services"},
		{Role: "assistant", Content: ""},
		{Role: "tool", ToolResponses: []llm.ToolResponse{
			{Name: "coral_list_services", Content: ""},
		}},
	}

	scriptPath := s.createMockScript(script)
	defer os.Remove(scriptPath)

	// Run ask command
	// Note: Validation of tool execution happens implicitly because if the tool isn't called,
	// the agent won't loop back to the provider for the second interaction.
	result := helpers.RunAsk(s.ctx, s.cliEnv.EnvVars(), "List all services", "mock:"+scriptPath, "")
	result.MustSucceed(s.T())

	s.Require().Contains(result.Output, "There seem to be no services registered currently.")

	// Also ensure sources are printed
	s.Require().Contains(result.Output, "Sources:")
	s.Require().Contains(result.Output, "coral_list_services")
}

// TestAskContinuation tests multi-turn conversations.
func (s *CLIAskSuite) TestAskContinuation() {
	s.T().Log("Testing 'coral ask' continuation...")

	// We need two scripts, one for each invocation, OR one script that handles both if the state is preserved purely by history passed to "NewMockProvider" every time?
	// Wait, `coral ask` CLI spins up a NEW agent every time.
	// The "continuation" happens by loading history from disk and passing it to the new agent.
	// So the persistence is on disk.
	// The "mock provider" is re-initialized every CLI run.
	// So we need:
	// Run 1: Script A.
	// Run 2: Script B.

	// Script 1: Answers "I am Coral."
	script1 := llm.MockScript{
		Interactions: []llm.MockInteraction{
			{
				ExpectedMessages: []llm.Message{
					{Role: "user", Content: "Who are you?"},
				},
				Response: llm.MockResponse{
					Content: "I am Coral.",
				},
			},
		},
	}
	path1 := s.createMockScript(script1)
	defer os.Remove(path1)

	// Run 1
	res1 := helpers.RunAsk(s.ctx, s.cliEnv.EnvVars(), "Who are you?", "mock:"+path1, "")
	res1.MustSucceed(s.T())
	s.Require().Contains(res1.Output, "I am Coral.")

	// Verify that conversation history was saved
	// We need to find the conversation ID from the last.json file
	lastPath := filepath.Join(s.cliEnv.ConfigDir, "conversations", "test-colony-e2e", "last.json")
	s.Require().FileExists(lastPath)

	type meta struct {
		ID string `json:"id"`
	}
	var m meta
	data, err := os.ReadFile(lastPath)
	s.Require().NoError(err)
	err = json.Unmarshal(data, &m)
	s.Require().NoError(err)

	// Check history file
	historyPath := filepath.Join(s.cliEnv.ConfigDir, "conversations", "test-colony-e2e", m.ID+".json")
	s.Require().FileExists(historyPath)

	// Dump content for debugging
	histData, _ := os.ReadFile(historyPath)
	s.Require().NotEmpty(histData, "History file should not be empty")
	s.Require().Contains(string(histData), "Who are you?", "History should contain the first question")
	s.Require().Contains(string(histData), "I am Coral", "History should contain the first answer")

	// Script 2: Expects history + new question.
	script2 := llm.MockScript{
		Interactions: []llm.MockInteraction{
			{
				ExpectedMessages: []llm.Message{
					{Role: "user", Content: "Who are you?"},
					{Role: "assistant", Content: "I am Coral."},
					{Role: "user", Content: "What can you do?"},
				},
				Response: llm.MockResponse{
					Content: "I can help you debug.",
				},
			},
		},
	}
	path2 := s.createMockScript(script2)
	defer os.Remove(path2)

	// Run 2 with --continue
	res2 := helpers.RunAskContinue(s.ctx, s.cliEnv.EnvVars(), "What can you do?", "mock:"+path2)
	res2.MustSucceed(s.T())
	s.Require().Contains(res2.Output, "I can help you debug.")
}

func (s *CLIAskSuite) createMockScript(script llm.MockScript) string {
	data, err := json.Marshal(script)
	s.Require().NoError(err)

	f, err := os.CreateTemp("", "mock_script_*.json")
	s.Require().NoError(err)
	defer f.Close()

	_, err = f.Write(data)
	s.Require().NoError(err)

	return f.Name()
}

func id(s string) string { return s }
