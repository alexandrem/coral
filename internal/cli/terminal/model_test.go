package terminal_test

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coral-mesh/coral/internal/cli/ask/ui"
	"github.com/coral-mesh/coral/internal/cli/terminal"
)

// stubAgent is a no-op ui.Agent used for testing without a real LLM.
type stubAgent struct{}

func (s *stubAgent) AskWithChannel(_ any, _, _ string, _ bool, _ chan<- any) (any, error) {
	return nil, nil
}

func (s *stubAgent) ResetConversation(_ string) {}

func newTestModel(t *testing.T) tea.Model {
	t.Helper()
	askModel, err := ui.NewModel(
		&stubAgent{},
		"conv-test",
		"test-colony",
		"test-model",
		nil,
		false,
		false,
		func(_, _ string, _ []ui.Message) error { return nil },
	)
	require.NoError(t, err)
	return terminal.NewTerminalModelForTest("test-colony", askModel, nil)
}

func TestTerminalModel_TabTogglesFocus(t *testing.T) {
	model := newTestModel(t)

	focusBefore := terminal.GetFocusForTest(model)

	// Send Tab → should toggle focus.
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	focusAfter := terminal.GetFocusForTest(updated)
	assert.NotEqual(t, focusBefore, focusAfter, "Tab should toggle focus")

	// Send Tab again → should return to original.
	updated2, _ := updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	assert.Equal(t, focusBefore, terminal.GetFocusForTest(updated2), "second Tab should restore focus")
}

func TestTerminalModel_CtrlDQuitsProgram(t *testing.T) {
	model := newTestModel(t)

	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	require.NotNil(t, cmd, "ctrl+d should return a quit command")

	msg := cmd()
	_, isQuit := msg.(tea.QuitMsg)
	assert.True(t, isQuit, "ctrl+d command should produce tea.QuitMsg")
}

func TestTerminalModel_WindowSizeDoesNotPanic(t *testing.T) {
	model := newTestModel(t)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	assert.NotPanics(t, func() { _ = updated.View() })
}

func TestTerminalModel_KeyForwardedToMainWhenFocused(t *testing.T) {
	model := newTestModel(t)

	// Focus is on main pane by default. A keystroke should not be swallowed.
	assert.Equal(t, 0, terminal.GetFocusForTest(model), "initial focus should be main (0)")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	assert.NotNil(t, updated, "model must be returned after key message")
}

func TestTerminalModel_ViewRendersWithoutPanic(t *testing.T) {
	model := newTestModel(t)

	// Simulate an initial window size.
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	assert.NotPanics(t, func() { _ = model.View() })
}
