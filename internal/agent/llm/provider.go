// Package llm provides LLM provider abstractions for the agent.
package llm

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

// Message represents a chat message.
type Message struct {
	Role    string // "user", "assistant", "system"
	Content string
}

// ToolCall represents a tool call made by the LLM.
type ToolCall struct {
	ID        string // Unique identifier for this tool call
	Name      string // Tool name
	Arguments string // JSON-encoded arguments
}

// GenerateRequest contains the parameters for LLM generation.
type GenerateRequest struct {
	Messages []Message
	Tools    []mcp.Tool // MCP tools available to the LLM
	Stream   bool       // Whether to stream the response
}

// GenerateResponse contains the LLM's response.
type GenerateResponse struct {
	Content      string     // Text content of the response
	ToolCalls    []ToolCall // Tool calls requested by the LLM, if any
	FinishReason string     // Why generation stopped: "stop", "tool_calls", "length", etc.
}

// StreamCallback is called for each chunk when streaming.
type StreamCallback func(chunk string) error

// Provider defines the interface that LLM providers must implement.
type Provider interface {
	// Name returns the provider name (e.g., "google", "openai", "anthropic").
	Name() string

	// Generate sends a request to the LLM and returns the response.
	// If streaming is enabled, it calls the callback for each chunk.
	Generate(ctx context.Context, req GenerateRequest, streamCallback StreamCallback) (*GenerateResponse, error)
}
