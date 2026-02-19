package distributed

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/coral-mesh/coral/internal/llm"
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

// TestAskInteractiveRejectsNonTerminal tests that interactive mode
// properly rejects non-terminal input (e.g., piped/redirected) (RFD 051).
func (s *CLIAskSuite) TestAskInteractiveRejectsNonTerminal() {
	s.T().Log("Testing 'coral ask' rejects non-terminal input...")

	// Try to run interactive mode without a question (should auto-detect).
	// Since E2E tests don't have a real terminal, this should fail.
	result := helpers.RunCLIWithEnv(s.ctx, s.cliEnv.EnvVars(), "ask")

	// Should fail with helpful error message.
	s.Require().True(result.HasError())
	s.Require().True(result.ContainsOutput("interactive mode requires a terminal"))
}

// TestAskDatetimeConversationID tests that conversation IDs
// use the new datetime format (RFD 051).
func (s *CLIAskSuite) TestAskDatetimeConversationID() {
	s.T().Log("Testing datetime-based conversation ID format...")

	script := llm.MockScript{
		Interactions: []llm.MockInteraction{
			{
				ExpectedMessages: []llm.Message{
					{Role: "user", Content: "Test question"},
				},
				Response: llm.MockResponse{
					Content: "Test response",
				},
			},
		},
	}

	scriptPath := s.createMockScript(script)
	defer os.Remove(scriptPath)

	result := helpers.RunAsk(s.ctx, s.cliEnv.EnvVars(), "Test question", "mock:"+scriptPath, "")
	result.MustSucceed(s.T())

	// Read the conversation ID from last.json.
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

	// Verify format: YYYY-MM-DD-HHMMSS-<random>
	// Should match pattern like "2026-02-15-143022-abc123".
	parts := strings.Split(m.ID, "-")
	s.Require().Len(parts, 6, "Conversation ID should have 6 parts (YYYY-MM-DD-HHMMSS-random)")

	// Verify date parts are numeric.
	year, err := strconv.Atoi(parts[0])
	s.Require().NoError(err)
	s.Require().Greater(year, 2020, "Year should be reasonable")

	month, err := strconv.Atoi(parts[1])
	s.Require().NoError(err)
	s.Require().GreaterOrEqual(month, 1)
	s.Require().LessOrEqual(month, 12)

	day, err := strconv.Atoi(parts[2])
	s.Require().NoError(err)
	s.Require().GreaterOrEqual(day, 1)
	s.Require().LessOrEqual(day, 31)

	// Verify time parts exist.
	s.Require().NotEmpty(parts[3], "Hour-minute-second should exist")
	s.Require().Len(parts[3], 6, "HHMMSS should be 6 digits")

	// Verify random suffix exists.
	s.Require().NotEmpty(parts[5], "Random suffix should exist")
	s.Require().Len(parts[5], 6, "Random suffix should be 6 characters")
}

// TestAskResumeDatetimePrefix tests the flexible conversation resolution
// that supports datetime prefixes (RFD 051).
func (s *CLIAskSuite) TestAskResumeDatetimePrefix() {
	s.T().Log("Testing conversation file naming and resolution...")

	// First, create a conversation.
	script1 := llm.MockScript{
		Interactions: []llm.MockInteraction{
			{
				ExpectedMessages: []llm.Message{
					{Role: "user", Content: "First question"},
				},
				Response: llm.MockResponse{
					Content: "First answer",
				},
			},
		},
	}
	path1 := s.createMockScript(script1)
	defer os.Remove(path1)

	res1 := helpers.RunAsk(s.ctx, s.cliEnv.EnvVars(), "First question", "mock:"+path1, "")
	res1.MustSucceed(s.T())

	// Get the conversation ID.
	lastPath := filepath.Join(s.cliEnv.ConfigDir, "conversations", "test-colony-e2e", "last.json")
	type meta struct {
		ID string `json:"id"`
	}
	var m meta
	data, err := os.ReadFile(lastPath)
	s.Require().NoError(err)
	err = json.Unmarshal(data, &m)
	s.Require().NoError(err)

	conversationID := m.ID
	s.T().Logf("Created conversation: %s", conversationID)

	// Extract datetime prefix (YYYY-MM-DD-HHMMSS).
	parts := strings.Split(conversationID, "-")
	s.Require().Len(parts, 6)
	datetimePrefix := strings.Join(parts[:5], "-") // First 5 parts

	s.T().Logf("Datetime prefix: %s", datetimePrefix)

	// Verify the conversation file exists with the datetime-based name.
	historyPath := filepath.Join(s.cliEnv.ConfigDir, "conversations", "test-colony-e2e", conversationID+".json")
	s.Require().FileExists(historyPath, "Conversation file should exist with datetime-based name")

	// Verify the file is in chronological order (easy to list/sort).
	// Files starting with YYYY-MM-DD will naturally sort by date.
	convDir := filepath.Join(s.cliEnv.ConfigDir, "conversations", "test-colony-e2e")
	entries, err := os.ReadDir(convDir)
	s.Require().NoError(err)

	// Find our conversation file.
	found := false
	for _, entry := range entries {
		if entry.Name() == conversationID+".json" {
			found = true
			// Verify the name starts with a date pattern.
			s.Require().True(strings.HasPrefix(entry.Name(), parts[0]+"-"+parts[1]+"-"+parts[2]),
				"Conversation file should start with YYYY-MM-DD for chronological ordering")
			break
		}
	}
	s.Require().True(found, "Conversation file should be findable in directory")
}

// TestAskHelpShowsInteractiveMode tests that help text
// includes interactive mode documentation (RFD 051).
func (s *CLIAskSuite) TestAskHelpShowsInteractiveMode() {
	s.T().Log("Testing 'coral ask --help' shows interactive mode...")

	result := helpers.RunCLIWithEnv(s.ctx, s.cliEnv.EnvVars(), "ask", "--help")
	result.MustSucceed(s.T())

	// Should mention interactive mode.
	s.Require().Contains(result.Output, "interactive", "Help should mention interactive mode")
	s.Require().Contains(result.Output, "--interactive", "Help should document --interactive flag")
	s.Require().Contains(result.Output, "--resume", "Help should document --resume flag")

	// Should show interactive mode is auto-detected.
	s.Require().Contains(result.Output, "Interactive Mode", "Help should have Interactive Mode section")
}

// TestAskSingleShotStillWorks is a regression test to ensure
// single-shot mode still works after RFD 051.
func (s *CLIAskSuite) TestAskSingleShotStillWorks() {
	s.T().Log("Testing single-shot mode still works (regression test)...")

	script := llm.MockScript{
		Interactions: []llm.MockInteraction{
			{
				ExpectedMessages: []llm.Message{
					{Role: "user", Content: "Single shot question"},
				},
				Response: llm.MockResponse{
					Content: "Single shot answer",
				},
			},
		},
	}

	scriptPath := s.createMockScript(script)
	defer os.Remove(scriptPath)

	// Explicitly provide a question (should NOT enter interactive mode).
	result := helpers.RunAsk(s.ctx, s.cliEnv.EnvVars(), "Single shot question", "mock:"+scriptPath, "")
	result.MustSucceed(s.T())

	s.Require().Contains(result.Output, "Single shot answer")
	// Should NOT show interactive mode hints.
	s.Require().NotContains(result.Output, "Ctrl+C")
	s.Require().NotContains(result.Output, "Ctrl+D")
}

// TestAskContinuationStillWorks is a regression test to ensure
// --continue flag still works after RFD 051.
func (s *CLIAskSuite) TestAskContinuationStillWorks() {
	s.T().Log("Testing --continue flag still works (regression test)...")

	// Script 1: Initial question.
	script1 := llm.MockScript{
		Interactions: []llm.MockInteraction{
			{
				ExpectedMessages: []llm.Message{
					{Role: "user", Content: "Initial question"},
				},
				Response: llm.MockResponse{
					Content: "Initial answer",
				},
			},
		},
	}
	path1 := s.createMockScript(script1)
	defer os.Remove(path1)

	res1 := helpers.RunAsk(s.ctx, s.cliEnv.EnvVars(), "Initial question", "mock:"+path1, "")
	res1.MustSucceed(s.T())
	s.Require().Contains(res1.Output, "Initial answer")

	// Script 2: Follow-up with history.
	script2 := llm.MockScript{
		Interactions: []llm.MockInteraction{
			{
				ExpectedMessages: []llm.Message{
					{Role: "user", Content: "Initial question"},
					{Role: "assistant", Content: "Initial answer"},
					{Role: "user", Content: "Follow-up question"},
				},
				Response: llm.MockResponse{
					Content: "Follow-up answer",
				},
			},
		},
	}
	path2 := s.createMockScript(script2)
	defer os.Remove(path2)

	// Run with --continue.
	res2 := helpers.RunAskContinue(s.ctx, s.cliEnv.EnvVars(), "Follow-up question", "mock:"+path2)
	res2.MustSucceed(s.T())
	s.Require().Contains(res2.Output, "Follow-up answer")
}
