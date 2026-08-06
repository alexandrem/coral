package ui

import (
	"context"
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
		// Keep listening for the next agent event.
		return m, waitForEventCmd(m.eventChan)

	case toolStartMsg:
		m.currentTool = msg.toolName
		m.currentCommand = msg.command
		m.currentState = stateQuerying
		return m, tea.Batch(m.spinner.Tick, waitForEventCmd(m.eventChan))

	case toolCompleteMsg:
		m.currentTool = ""
		m.currentCommand = ""
		// Keep listening for the next agent event.
		return m, waitForEventCmd(m.eventChan)

	case scriptReviewMsg:
		// Pause the agent event loop; wait for user y/n input.
		m.reviewEventChan = m.eventChan
		m.eventChan = nil
		m.reviewName = msg.name
		m.reviewContent = msg.content
		m.reviewReply = msg.reply
		m.currentState = stateScriptReview
		return m, nil

	case queryCompleteMsg:
		m.eventChan = nil
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

	case LoadConversationMsg:
		// Switch to the selected conversation (sent by coral terminal sidebar).
		m.conversation = msg.History
		m.conversationID = msg.ConversationID
		m.currentState = stateIdle
		m.input.Reset()
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
	// Script review state: only y/n/escape are meaningful.
	if m.currentState == stateScriptReview {
		return m.handleReviewKey(msg)
	}

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

		// Start query: create the event channel, launch the agent goroutine,
		// and return a cmd that reads the first event.
		eventChan := make(chan any, 100)
		m.eventChan = eventChan
		m.currentState = stateQuerying
		m.streamBuffer = ""

		go func() {
			defer close(eventChan)
			resp, err := m.agent.AskWithChannel(
				context.Background(), question, m.conversationID, m.dryRun, eventChan,
			)
			// Send the completion event directly; the adapter may have already
			// closed the channel, but we send before that can happen.
			eventChan <- AgentEvent{Type: "complete", Response: resp, Error: err}
		}()

		return m, waitForEventCmd(eventChan)
	}

	// Pass through to text input.
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// handleReviewKey handles key input during the script_review state.
// y/Y approves the script write; any other key (n, N, esc, ctrl+c) rejects it.
func (m Model) handleReviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var approved bool
	switch msg.String() {
	case "y", "Y":
		approved = true
	default:
		approved = false
	}

	// Unblock the agent goroutine.
	m.reviewReply <- approved

	// Restore the event channel and resume listening.
	m.eventChan = m.reviewEventChan
	m.reviewEventChan = nil
	m.reviewReply = nil
	m.reviewName = ""
	m.reviewContent = ""
	m.currentState = stateQuerying

	return m, waitForEventCmd(m.eventChan)
}

// handleInlineCommand processes inline commands like /help, /clear, /exit.
func (m Model) handleInlineCommand(cmd string) (tea.Model, tea.Cmd) {
	switch strings.ToLower(strings.TrimSpace(cmd)) {
	case "/help":
		m.showHelp()
		m.input.Reset()
		return m, nil

	case "/clear":
		// Clear the displayed conversation and reset the agent's in-memory
		// context so the next query starts fresh.
		m.conversation = nil
		m.agent.ResetConversation(m.conversationID)
		m.input.Reset()
		return m, tea.ClearScreen

	case "/exit", "/quit":
		m.quitting = true
		return m, tea.Quit

	default:
		// Delegate to the external command handler (e.g. /browser in coral terminal).
		if m.commandHandler != nil {
			m.input.Reset()
			return m, m.commandHandler(strings.ToLower(strings.TrimSpace(cmd)))
		}
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
- /clear     - Clear conversation history and start fresh
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
