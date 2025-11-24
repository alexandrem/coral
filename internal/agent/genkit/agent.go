package genkit

import (
	"context"
	"fmt"

	"github.com/coral-io/coral/internal/config"
)

// Agent represents a Genkit-powered LLM agent that connects to Colony MCP server (RFD 030).
type Agent struct {
	config        *config.AskConfig
	colonyConfig  *config.ColonyConfig
	provider      Provider
	conversations map[string]*Conversation
}

// Response represents an agent response.
type Response struct {
	Answer    string
	ToolCalls []ToolCall
}

// ToolCall represents an MCP tool invocation.
type ToolCall struct {
	Name   string
	Input  map[string]interface{}
	Output interface{}
}

// NewAgent creates a new LLM agent with the given configuration.
func NewAgent(askCfg *config.AskConfig, colonyCfg *config.ColonyConfig) (*Agent, error) {
	if askCfg == nil {
		return nil, fmt.Errorf("ask config is required")
	}
	if colonyCfg == nil {
		return nil, fmt.Errorf("colony config is required")
	}

	// Initialize provider based on model.
	provider, err := NewProvider(askCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize provider: %w", err)
	}

	return &Agent{
		config:        askCfg,
		colonyConfig:  colonyCfg,
		provider:      provider,
		conversations: make(map[string]*Conversation),
	}, nil
}

// Ask sends a question to the LLM and returns the response.
func (a *Agent) Ask(ctx context.Context, question, conversationID string) (*Response, error) {
	// Get or create conversation.
	conv := a.getOrCreateConversation(conversationID)

	// Add user message to conversation.
	conv.AddMessage(Message{
		Role:    "user",
		Content: question,
	})

	// TODO: Implement actual LLM call with MCP tool integration.
	// For now, return a placeholder response.
	resp := &Response{
		Answer:    fmt.Sprintf("Received question: %s\n\n(LLM integration pending - RFD 030 implementation in progress)", question),
		ToolCalls: []ToolCall{},
	}

	// Add assistant response to conversation.
	conv.AddMessage(Message{
		Role:    "assistant",
		Content: resp.Answer,
	})

	return resp, nil
}

// Close cleans up agent resources.
func (a *Agent) Close() error {
	// TODO: Cleanup resources (e.g., close MCP connection).
	return nil
}

// getOrCreateConversation retrieves or creates a conversation.
func (a *Agent) getOrCreateConversation(id string) *Conversation {
	if conv, exists := a.conversations[id]; exists {
		return conv
	}

	conv := NewConversation(a.config.Conversation.MaxTurns, a.config.Conversation.ContextWindow)
	a.conversations[id] = conv
	return conv
}
