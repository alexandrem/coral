package ask

import (
	"sync"

	"github.com/coral-mesh/coral/internal/llm"
)

// Message represents a conversation message.
type Message struct {
	Role          string
	Content       string
	ToolCalls     []llm.ToolCall
	ToolResponses []llm.ToolResponse
}

// Conversation tracks a conversation history.
type Conversation struct {
	id       string
	messages []Message
	mu       sync.RWMutex
}

// NewConversation creates a new conversation.
func NewConversation(id string) *Conversation {
	return &Conversation{
		id:       id,
		messages: make([]Message, 0),
	}
}

// AddMessage adds a message to the conversation.
func (c *Conversation) AddMessage(msg Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, msg)
}

// GetMessages returns all messages in the conversation.
func (c *Conversation) GetMessages() []Message {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.messages
}

// Clear clears the conversation history.
func (c *Conversation) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = make([]Message, 0)
}
