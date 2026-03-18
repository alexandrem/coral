package ui

// streamChunkMsg represents a chunk of streaming LLM response.
type streamChunkMsg struct {
	chunk string
}

// toolStartMsg indicates a tool call has started.
type toolStartMsg struct {
	toolName string
}

// toolCompleteMsg indicates a tool call has completed.
type toolCompleteMsg struct {
	toolName string
	duration float64 // seconds
}

// queryCompleteMsg indicates the full query-response cycle is done.
type queryCompleteMsg struct {
	response any // *ask.Response
	err      error
}

// errorMsg represents an error during processing.
type errorMsg struct {
	err error
}

// conversationSavedMsg indicates conversation was saved successfully.
type conversationSavedMsg struct{}

// LoadConversationMsg asks the model to switch to a different conversation.
// History contains pre-loaded messages; ConversationID is the new ID.
// Sent by the terminal's sidebar when the user selects a past session.
type LoadConversationMsg struct {
	ConversationID string
	History        []Message
}
