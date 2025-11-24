package genkit

import (
	"context"
	"fmt"
	"strings"

	"github.com/coral-io/coral/internal/config"
)

// Provider represents an LLM provider interface.
type Provider interface {
	// Generate generates a response for the given prompt.
	Generate(ctx context.Context, messages []Message, tools []Tool) (*ProviderResponse, error)

	// Name returns the provider name.
	Name() string

	// Model returns the model identifier.
	Model() string
}

// ProviderResponse represents a provider's response.
type ProviderResponse struct {
	Content   string
	ToolCalls []ToolCall
	Usage     Usage
}

// Usage tracks token usage.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Tool represents an available tool for the LLM.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]interface{}
}

// NewProvider creates a provider based on the configured model.
func NewProvider(cfg *config.AskConfig) (Provider, error) {
	parts := strings.SplitN(cfg.DefaultModel, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid model format: %s (expected provider:model)", cfg.DefaultModel)
	}

	providerName := parts[0]
	modelID := parts[1]

	switch providerName {
	case "openai":
		return NewOpenAIProvider(modelID, cfg.APIKeys["openai"])
	case "anthropic":
		return NewAnthropicProvider(modelID, cfg.APIKeys["anthropic"])
	case "ollama":
		return NewOllamaProvider(modelID)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", providerName)
	}
}

// OpenAIProvider implements the OpenAI provider.
type OpenAIProvider struct {
	model  string
	apiKey string
}

// NewOpenAIProvider creates a new OpenAI provider.
func NewOpenAIProvider(model, apiKey string) (Provider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required (set OPENAI_API_KEY)")
	}
	return &OpenAIProvider{model: model, apiKey: apiKey}, nil
}

func (p *OpenAIProvider) Generate(ctx context.Context, messages []Message, tools []Tool) (*ProviderResponse, error) {
	// TODO: Implement actual OpenAI API call.
	return &ProviderResponse{
		Content: "OpenAI integration pending",
	}, fmt.Errorf("OpenAI provider not yet implemented")
}

func (p *OpenAIProvider) Name() string  { return "openai" }
func (p *OpenAIProvider) Model() string { return p.model }

// AnthropicProvider implements the Anthropic provider.
type AnthropicProvider struct {
	model  string
	apiKey string
}

// NewAnthropicProvider creates a new Anthropic provider.
func NewAnthropicProvider(model, apiKey string) (Provider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("Anthropic API key is required (set ANTHROPIC_API_KEY)")
	}
	return &AnthropicProvider{model: model, apiKey: apiKey}, nil
}

func (p *AnthropicProvider) Generate(ctx context.Context, messages []Message, tools []Tool) (*ProviderResponse, error) {
	// TODO: Implement actual Anthropic API call.
	return &ProviderResponse{
		Content: "Anthropic integration pending",
	}, fmt.Errorf("Anthropic provider not yet implemented")
}

func (p *AnthropicProvider) Name() string  { return "anthropic" }
func (p *AnthropicProvider) Model() string { return p.model }

// OllamaProvider implements the Ollama provider (local models).
type OllamaProvider struct {
	model string
}

// NewOllamaProvider creates a new Ollama provider.
func NewOllamaProvider(model string) (Provider, error) {
	return &OllamaProvider{model: model}, nil
}

func (p *OllamaProvider) Generate(ctx context.Context, messages []Message, tools []Tool) (*ProviderResponse, error) {
	// TODO: Implement Ollama API call.
	return &ProviderResponse{
		Content: "Ollama integration pending",
	}, fmt.Errorf("Ollama provider not yet implemented")
}

func (p *OllamaProvider) Name() string  { return "ollama" }
func (p *OllamaProvider) Model() string { return p.model }
