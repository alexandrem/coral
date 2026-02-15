package ui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

// AgentEvent represents an event from the agent (to avoid import cycle).
type AgentEvent struct {
	Type     string
	Content  string
	ToolName string
	Duration float64
	Error    error
	Response any
}

// askQuestionCmd executes an agent query in a goroutine.
func askQuestionCmd(agent Agent, question, conversationID string, debug, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		eventChan := make(chan any, 100)

		// Execute query in goroutine.
		go func() {
			defer close(eventChan)

			resp, err := agent.AskWithChannel(ctx, question, conversationID, dryRun, eventChan)

			// Send completion message.
			eventChan <- AgentEvent{
				Type:     "complete",
				Response: resp,
				Error:    err,
			}
		}()

		// Convert agent events to Bubbletea messages.
		// This function will be called repeatedly by Bubbletea,
		// returning one message at a time from the channel.
		for event := range eventChan {
			// Type assert to AgentEvent
			if agentEvent, ok := event.(AgentEvent); ok {
				switch agentEvent.Type {
				case "stream":
					return streamChunkMsg{chunk: agentEvent.Content}
				case "tool_start":
					return toolStartMsg{toolName: agentEvent.ToolName}
				case "tool_complete":
					return toolCompleteMsg{toolName: agentEvent.ToolName, duration: agentEvent.Duration}
				case "complete":
					return queryCompleteMsg{response: agentEvent.Response, err: agentEvent.Error}
				}
			}
		}

		// Channel closed without completion message.
		return errorMsg{err: context.Canceled}
	}
}

// saveConversationCmd saves conversation to disk.
func saveConversationCmd(m Model) tea.Cmd {
	return func() tea.Msg {
		// Use the callback function to save conversation.
		if err := m.saveConversation(m.colonyID, m.conversationID, m.conversation); err != nil {
			return errorMsg{err: err}
		}
		return conversationSavedMsg{}
	}
}
