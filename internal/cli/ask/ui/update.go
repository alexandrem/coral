package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// Update handles messages and updates the model (Bubbletea interface).
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case spinner.TickMsg:
		if m.currentState == stateQuerying {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case streamChunkMsg:
		m.streamBuffer += msg.chunk
		m.currentState = stateStreaming
		return m, nil

	case toolStartMsg:
		m.currentTool = msg.toolName
		m.currentState = stateQuerying
		return m, m.spinner.Tick

	case toolCompleteMsg:
		m.currentTool = ""
		return m, nil

	case queryCompleteMsg:
		if msg.err != nil {
			m.lastError = msg.err
			m.currentState = stateError
			m.input.Reset()
			return m, nil
		}

		// Add assistant message to conversation.
		// response is an any type containing *ask.Response
		// We use the streamBuffer which accumulated during streaming
		if m.streamBuffer != "" {
			m.conversation = append(m.conversation, Message{
				Role:    "assistant",
				Content: m.streamBuffer,
			})
		}

		// Reset state.
		m.currentState = stateIdle
		m.streamBuffer = ""
		m.input.Reset()
		m.input.Focus()

		// Save conversation.
		return m, saveConversationCmd(m)

	case conversationSavedMsg:
		// Conversation saved successfully, nothing to do.
		return m, nil

	case errorMsg:
		m.lastError = msg.err
		m.currentState = stateError
		return m, nil
	}

	// Update text input.
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// handleKeyMsg handles keyboard input.
func (m Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.currentState == stateQuerying || m.currentState == stateStreaming {
			// Cancel current query.
			m.currentState = stateIdle
			m.streamBuffer = ""
			m.input.Reset()
			return m, nil
		}
		// Exit application.
		m.quitting = true
		return m, tea.Quit

	case "ctrl+d":
		// Exit application.
		m.quitting = true
		return m, tea.Quit

	case "enter":
		if m.currentState != stateIdle {
			// Ignore enter while processing.
			return m, nil
		}

		question := m.input.Value()
		if question == "" {
			return m, nil
		}

		// Handle inline commands.
		if strings.HasPrefix(question, "/") {
			return m.handleInlineCommand(question)
		}

		// Add user message to conversation.
		m.conversation = append(m.conversation, Message{
			Role:    "user",
			Content: question,
		})

		// Start query.
		m.currentState = stateQuerying
		m.streamBuffer = ""

		return m, askQuestionCmd(m.agent, question, m.conversationID, m.debug, m.dryRun)
	}

	// Pass through to text input.
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// handleInlineCommand processes inline commands like /help, /clear, /exit.
func (m Model) handleInlineCommand(cmd string) (tea.Model, tea.Cmd) {
	switch strings.ToLower(strings.TrimSpace(cmd)) {
	case "/help":
		m.showHelp()
		m.input.Reset()
		return m, nil

	case "/clear":
		// Clear screen by resetting conversation display.
		// (actual conversation history remains intact)
		m.input.Reset()
		return m, tea.ClearScreen

	case "/exit", "/quit":
		m.quitting = true
		return m, tea.Quit

	default:
		m.lastError = fmt.Errorf("unknown command: %s (try /help)", cmd)
		m.currentState = stateError
		m.input.Reset()
		return m, nil
	}
}

// showHelp displays help information in the conversation.
func (m *Model) showHelp() {
	helpText := `## Coral Ask - Interactive Mode

**Available commands:**
- /help      - Show this help message
- /clear     - Clear the screen
- /exit      - Exit interactive session

**Natural language queries:**
Just type your question (no prefix needed)

**Examples:**
> what's causing high latency?
> show me error logs for payment-api
> compare current metrics to yesterday

**Keyboard shortcuts:**
- Ctrl+C     - Cancel current query (or exit if idle)
- Ctrl+D     - Exit interactive session
- Enter      - Submit question`

	// Add help as a system message (won't be sent to LLM).
	m.conversation = append(m.conversation, Message{
		Role:    "system",
		Content: helpText,
	})
}
