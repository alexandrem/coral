package ui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// spyAgent records calls for test assertions.
type spyAgent struct {
	resetCalled         bool
	resetConversationID string
}

func (s *spyAgent) AskWithChannel(_ any, _, _ string, _ bool, _ chan<- any) (any, error) {
	return nil, nil
}

func (s *spyAgent) ResetConversation(conversationID string) {
	s.resetCalled = true
	s.resetConversationID = conversationID
}

func newTestModel(t *testing.T, agent Agent, history []Message) Model {
	t.Helper()
	m, err := NewModel(
		agent,
		"conv-abc",
		"test-colony",
		"test-model",
		history,
		false,
		false,
		func(_, _ string, _ []Message) error { return nil },
	)
	require.NoError(t, err)
	return m
}

// TestClear_ResetsConversationSlice verifies that /clear empties the displayed
// conversation history.
func TestClear_ResetsConversationSlice(t *testing.T) {
	agent := &spyAgent{}
	history := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	m := newTestModel(t, agent, history)

	require.Len(t, m.conversation, 2, "precondition: history should be loaded")

	m.input.SetValue("/clear")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := updated.(Model)

	assert.Empty(t, result.conversation, "/clear should empty the conversation slice")
}

// TestClear_CallsAgentReset verifies that /clear calls ResetConversation on the
// agent with the current conversation ID so the LLM context is also cleared.
func TestClear_CallsAgentReset(t *testing.T) {
	agent := &spyAgent{}
	m := newTestModel(t, agent, []Message{{Role: "user", Content: "hello"}})

	m.input.SetValue("/clear")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	assert.True(t, agent.resetCalled, "ResetConversation should be called on the agent")
	assert.Equal(t, "conv-abc", agent.resetConversationID, "ResetConversation should receive the active conversation ID")
}

// TestClear_ReturnsClearScreenCmd verifies that /clear returns a ClearScreen
// command so the terminal buffer is wiped.
func TestClear_ReturnsClearScreenCmd(t *testing.T) {
	agent := &spyAgent{}
	m := newTestModel(t, agent, nil)

	m.input.SetValue("/clear")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	require.NotNil(t, cmd, "/clear should return a command")
	// tea.clearScreenMsg is unexported; compare by executing both commands and
	// checking the message types match.
	assert.True(t,
		reflect.TypeOf(cmd()) == reflect.TypeOf(tea.ClearScreen()),
		"/clear command should produce the same message type as tea.ClearScreen()",
	)
}

// TestClear_ResetsInput verifies the input field is empty after /clear.
func TestClear_ResetsInput(t *testing.T) {
	agent := &spyAgent{}
	m := newTestModel(t, agent, nil)

	m.input.SetValue("/clear")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	assert.Empty(t, updated.(Model).input.Value(), "input should be cleared after /clear")
}

// TestHelp_AppendSystemMessage verifies that /help adds a system message to the
// conversation without triggering any agent call.
func TestHelp_AppendSystemMessage(t *testing.T) {
	agent := &spyAgent{}
	m := newTestModel(t, agent, nil)

	m.input.SetValue("/help")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := updated.(Model)

	require.Len(t, result.conversation, 1, "/help should append exactly one message")
	msg := result.conversation[0]
	assert.Equal(t, "system", msg.Role)
	assert.True(t, strings.Contains(msg.Content, "/clear"), "help text should mention /clear")
	assert.False(t, agent.resetCalled, "/help should not reset the agent")
}

// TestUnknownCommand_SetsErrorState verifies that an unrecognised slash command
// puts the model into the error state.
func TestUnknownCommand_SetsErrorState(t *testing.T) {
	agent := &spyAgent{}
	m := newTestModel(t, agent, nil)

	m.input.SetValue("/notacommand")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := updated.(Model)

	assert.Equal(t, stateError, result.currentState, "unknown command should set stateError")
	require.NotNil(t, result.lastError)
	assert.Contains(t, result.lastError.Error(), "unknown command")
}

// TestUserMessage_AppendsToConversation verifies that a plain text message is
// appended as a user role message and a query is started.
func TestUserMessage_AppendsToConversation(t *testing.T) {
	agent := &spyAgent{}
	m := newTestModel(t, agent, nil)

	m.input.SetValue("what is cpu usage?")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := updated.(Model)

	require.Len(t, result.conversation, 1)
	assert.Equal(t, "user", result.conversation[0].Role)
	assert.Equal(t, "what is cpu usage?", result.conversation[0].Content)
	assert.Equal(t, stateQuerying, result.currentState)
	assert.NotNil(t, cmd, "a query command should be returned")
}

// TestScriptReview_TransitionsToReviewState verifies that receiving a
// scriptReviewMsg puts the model into stateScriptReview with the script data.
func TestScriptReview_TransitionsToReviewState(t *testing.T) {
	m := newTestModel(t, &spyAgent{}, nil)
	// Simulate an in-flight query by giving the model an event channel.
	ch := make(chan any, 10)
	m.eventChan = ch

	reply := make(chan bool, 1)
	updated, cmd := m.Update(scriptReviewMsg{
		name:    "my-script",
		content: "console.log('hello')",
		reply:   reply,
	})
	result := updated.(Model)

	assert.Equal(t, stateScriptReview, result.currentState)
	assert.Equal(t, "my-script", result.reviewName)
	assert.Equal(t, "console.log('hello')", result.reviewContent)
	assert.Nil(t, result.eventChan, "eventChan should be cleared during review")
	assert.Equal(t, ch, result.reviewEventChan, "reviewEventChan should hold the old channel")
	assert.Nil(t, cmd, "no cmd while waiting for user input")
}

// TestScriptReview_ApproveResumesQuery verifies that pressing 'y' sends true
// to the reply channel and resumes listening on the event channel.
func TestScriptReview_ApproveResumesQuery(t *testing.T) {
	m := newTestModel(t, &spyAgent{}, nil)
	ch := make(chan any, 10)
	reply := make(chan bool, 1)

	m.currentState = stateScriptReview
	m.reviewEventChan = ch
	m.reviewReply = reply
	m.reviewName = "s"
	m.reviewContent = "x"

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	result := updated.(Model)

	approved := <-reply
	assert.True(t, approved, "reply channel should receive true on 'y'")
	assert.Equal(t, stateQuerying, result.currentState)
	assert.Equal(t, ch, result.eventChan, "eventChan should be restored")
	assert.NotNil(t, cmd, "a listen cmd should be returned")
}

// TestScriptReview_RejectResumesQuery verifies that pressing 'n' sends false
// to the reply channel and resumes the query.
func TestScriptReview_RejectResumesQuery(t *testing.T) {
	m := newTestModel(t, &spyAgent{}, nil)
	ch := make(chan any, 10)
	reply := make(chan bool, 1)

	m.currentState = stateScriptReview
	m.reviewEventChan = ch
	m.reviewReply = reply
	m.reviewName = "s"
	m.reviewContent = "x"

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	result := updated.(Model)

	approved := <-reply
	assert.False(t, approved, "reply channel should receive false on 'n'")
	assert.Equal(t, stateQuerying, result.currentState)
	assert.Equal(t, ch, result.eventChan, "eventChan should be restored")
	assert.NotNil(t, cmd, "a listen cmd should be returned")
}
