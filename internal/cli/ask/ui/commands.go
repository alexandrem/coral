// Package ui manages the UI terminal rendering for coral ask interactive command.
package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// UIScriptApproval carries script review data for the "script_review" event.
// ApprovalReply is shared with the agent goroutine: send true to approve,
// false to reject.
type UIScriptApproval struct {
	Name          string
	Content       string
	ApprovalReply chan bool
}

// AgentEvent represents an event from the agent (to avoid import cycle).
type AgentEvent struct {
	Type           string
	Content        string
	ToolName       string
	Command        string // Full CLI command string for tool_start in CLI dispatch mode (RFD 100)
	Duration       float64
	Error          error
	Response       any
	ScriptApproval *UIScriptApproval // Non-nil for "script_review" events.
}

// waitForEventCmd returns a Bubbletea command that reads the next event from ch
// and converts it to a Bubbletea message.  The channel is created by the model
// (in handleKeyMsg) and stored in Model.eventChan so subsequent calls can
// continue listening without restarting the query.
func waitForEventCmd(ch <-chan any) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			// Channel closed — query finished or cancelled.
			return queryCompleteMsg{err: nil}
		}
		e, ok := event.(AgentEvent)
		if !ok {
			return errorMsg{err: fmt.Errorf("unexpected event type %T", event)}
		}
		switch e.Type {
		case "stream":
			return streamChunkMsg{chunk: e.Content}
		case "tool_start":
			return toolStartMsg{toolName: e.ToolName, command: e.Command}
		case "tool_complete":
			return toolCompleteMsg{toolName: e.ToolName, duration: e.Duration}
		case "complete":
			return queryCompleteMsg{response: e.Response, err: e.Error}
		case "script_review":
			if e.ScriptApproval != nil {
				return scriptReviewMsg{
					name:    e.ScriptApproval.Name,
					content: e.ScriptApproval.Content,
					reply:   e.ScriptApproval.ApprovalReply,
				}
			}
		}
		return errorMsg{err: fmt.Errorf("unknown agent event %q", e.Type)}
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
