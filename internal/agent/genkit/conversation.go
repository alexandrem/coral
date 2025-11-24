package genkit

import (
	"time"
)

// Conversation represents a multi-turn conversation with history.
type Conversation struct {
	ID            string
	Messages      []Message
	MaxTurns      int
	ContextWindow int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Message represents a single message in the conversation.
type Message struct {
	Role      string // "user", "assistant", "system"
	Content   string
	Timestamp time.Time
}

// NewConversation creates a new conversation.
func NewConversation(maxTurns, contextWindow int) *Conversation {
	now := time.Now()
	return &Conversation{
		Messages:      []Message{},
		MaxTurns:      maxTurns,
		ContextWindow: contextWindow,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

// AddMessage adds a message to the conversation.
func (c *Conversation) AddMessage(msg Message) {
	msg.Timestamp = time.Now()
	c.Messages = append(c.Messages, msg)
	c.UpdatedAt = time.Now()

	// Auto-prune if we exceed max turns.
	if c.MaxTurns > 0 && len(c.Messages) > c.MaxTurns*2 { // *2 for user+assistant pairs
		// Keep system messages + last N turns.
		systemMsgs := []Message{}
		for _, m := range c.Messages {
			if m.Role == "system" {
				systemMsgs = append(systemMsgs, m)
			}
		}

		// Keep last MaxTurns worth of messages.
		keepCount := c.MaxTurns * 2
		startIdx := len(c.Messages) - keepCount
		if startIdx < 0 {
			startIdx = 0
		}

		c.Messages = append(systemMsgs, c.Messages[startIdx:]...)
	}
}

// GetMessages returns all messages in the conversation.
func (c *Conversation) GetMessages() []Message {
	return c.Messages
}

// Clear clears all messages from the conversation.
func (c *Conversation) Clear() {
	c.Messages = []Message{}
	c.UpdatedAt = time.Now()
}
