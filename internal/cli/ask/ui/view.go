package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	// Styles.
	promptStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("205")).
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("9")).
			Bold(true)

	toolStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("11"))

	hintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))
)

// View renders the UI (Bubbletea interface).
func (m Model) View() string {
	if m.quitting {
		return "Goodbye!\n"
	}

	var b strings.Builder

	// Render prompt line.
	b.WriteString(m.renderPrompt())
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", m.width))
	b.WriteString("\n\n")

	// Render conversation history (last N messages to fit screen).
	b.WriteString(m.renderConversation())

	// Render current state.
	switch m.currentState {
	case stateQuerying:
		if m.currentTool != "" {
			b.WriteString(fmt.Sprintf("\n%s %s Executing tool: %s...\n",
				m.spinner.View(),
				toolStyle.Render("⚙"),
				m.currentTool))
		} else {
			b.WriteString(fmt.Sprintf("\n%s Thinking...\n", m.spinner.View()))
		}

	case stateStreaming:
		if m.streamBuffer != "" {
			rendered, err := m.renderer.Render(m.streamBuffer)
			if err != nil {
				b.WriteString(m.streamBuffer) // Fallback to plain text
			} else {
				b.WriteString(rendered)
			}
		}

	case stateError:
		b.WriteString(errorStyle.Render(fmt.Sprintf("\n✗ Error: %v\n", m.lastError)))
	}

	// Render input line.
	b.WriteString("\n")
	b.WriteString(m.input.View())

	// Render help hint.
	if m.currentState == stateIdle {
		b.WriteString(hintStyle.Render("\n[Ctrl+C to cancel, /help for commands, /exit to quit]"))
	}

	return b.String()
}

// renderPrompt renders the prompt line with colony and model info.
func (m Model) renderPrompt() string {
	return promptStyle.Render(fmt.Sprintf("%s | %s", m.colonyID, m.modelName))
}

// renderConversation renders the conversation history.
func (m Model) renderConversation() string {
	var b strings.Builder

	// Render last N messages (to fit on screen).
	maxMessages := 10
	startIdx := len(m.conversation) - maxMessages
	if startIdx < 0 {
		startIdx = 0
	}

	for _, msg := range m.conversation[startIdx:] {
		switch msg.Role {
		case "user":
			b.WriteString(promptStyle.Render("> "))
			b.WriteString(msg.Content)
			b.WriteString("\n\n")

		case "assistant":
			rendered, err := m.renderer.Render(msg.Content)
			if err != nil {
				b.WriteString(msg.Content) // Fallback
			} else {
				b.WriteString(rendered)
			}
			b.WriteString("\n")

		case "system":
			// Render system messages (like /help output).
			rendered, err := m.renderer.Render(msg.Content)
			if err != nil {
				b.WriteString(msg.Content)
			} else {
				b.WriteString(rendered)
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}
