// Package llm provides LLM provider abstractions for the agent.
package llm

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/coral-mesh/coral/internal/config"
)

// Message represents a chat message.
type Message struct {
	Role          string         // "user", "assistant", "system", "tool"
	Content       string         // Text content (for user/assistant messages)
	ToolCalls     []ToolCall     // Tool calls made by the assistant (for assistant role)
	ToolResponses []ToolResponse // Tool responses (for tool role)
}

// ToolResponse represents the result of a tool call.
type ToolResponse struct {
	CallID  string // ID of the tool call this responds to
	Name    string // Tool name
	Content string // Tool output (text or JSON)
}

// ToolCall represents a tool call made by the LLM.
type ToolCall struct {
	ID        string // Unique identifier for this tool call
	Name      string // Tool name
	Arguments string // JSON-encoded arguments
}

// GenerateRequest contains the parameters for LLM generation.
type GenerateRequest struct {
	Messages     []Message
	Tools        []mcp.Tool // MCP tools available to the LLM
	Stream       bool       // Whether to stream the response
	SystemPrompt string     // System instructions for parameter extraction and behavior guidance
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

// ProviderMetadata contains metadata about an LLM provider.
type ProviderMetadata struct {
	Name            string          // Provider identifier (e.g., "google", "openai")
	DisplayName     string          // Human-readable name (e.g., "Google AI")
	Description     string          // Short description
	DefaultEnvVar   string          // Default API key env var (e.g., "GOOGLE_API_KEY")
	SupportedModels []ModelMetadata // Models supported by this provider
	RequiresAPIKey  bool            // Whether API key is required
}

// ModelMetadata contains metadata about a specific model.
type ModelMetadata struct {
	ID           string   // Model identifier (e.g., "gemini-2.0-flash")
	DisplayName  string   // Human-readable name
	Description  string   // Model description
	Capabilities []string // e.g., ["tools", "streaming", "vision"]
	Deprecated   bool     // Whether model is deprecated
}

// ProviderFactory creates a Provider instance.
type ProviderFactory func(ctx context.Context, modelID string, cfg *config.AskConfig, debug bool) (Provider, error)

// Registry manages available LLM providers.
type Registry struct {
	providers map[string]*registeredProvider
	mu        sync.RWMutex
}

type registeredProvider struct {
	metadata ProviderMetadata
	factory  ProviderFactory
}

var globalRegistry = NewRegistry()

// NewRegistry creates a new provider registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]*registeredProvider),
	}
}

// Register registers a provider with the global registry.
func Register(metadata ProviderMetadata, factory ProviderFactory) {
	globalRegistry.RegisterProvider(metadata, factory)
}

// RegisterProvider registers a provider.
func (r *Registry) RegisterProvider(metadata ProviderMetadata, factory ProviderFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[metadata.Name] = &registeredProvider{
		metadata: metadata,
		factory:  factory,
	}
}

// Get returns the global registry.
func Get() *Registry {
	return globalRegistry
}

// GetProvider creates a provider instance by name.
func (r *Registry) GetProvider(ctx context.Context, providerName string, modelID string, cfg *config.AskConfig, debug bool) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	provider, exists := r.providers[providerName]
	if !exists {
		return nil, fmt.Errorf("unsupported provider: %s (run 'coral ask list-providers' to see available providers)", providerName)
	}

	return provider.factory(ctx, modelID, cfg, debug)
}

// ListProviders returns all registered provider metadata.
func (r *Registry) ListProviders() []ProviderMetadata {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]ProviderMetadata, 0, len(r.providers))
	for _, p := range r.providers {
		if p.metadata.Name == "mock" {
			continue
		}
		result = append(result, p.metadata)
	}

	// Sort by name for consistent output.
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result
}

// ValidateModel validates a model ID for a specific provider.
func (r *Registry) ValidateModel(providerName string, modelID string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	provider, exists := r.providers[providerName]
	if !exists {
		return fmt.Errorf("unknown provider: %s", providerName)
	}

	// Empty model list means any model is accepted (e.g., OpenAI-compatible endpoints).
	if len(provider.metadata.SupportedModels) == 0 {
		return nil
	}

	for _, model := range provider.metadata.SupportedModels {
		if model.ID == modelID {
			if model.Deprecated {
				fmt.Fprintf(os.Stderr, "Warning: Model %s is deprecated\n", modelID)
			}
			return nil
		}
	}

	// Provide helpful error with model suggestions.
	var modelIDs []string
	for _, model := range provider.metadata.SupportedModels {
		if !model.Deprecated {
			modelIDs = append(modelIDs, model.ID)
		}
	}
	return fmt.Errorf("unsupported model %q for provider %s. Supported models: %s",
		modelID, providerName, strings.Join(modelIDs, ", "))
}

// GetProviderStatus checks if a provider is configured with API keys.
func (r *Registry) GetProviderStatus(providerName string, cfg *config.AskConfig) string {
	r.mu.RLock()
	provider, exists := r.providers[providerName]
	r.mu.RUnlock()

	if !exists {
		return "unknown"
	}

	if !provider.metadata.RequiresAPIKey {
		return "available"
	}

	// Check if API key is configured.
	apiKey := cfg.APIKeys[providerName]
	if apiKey != "" && !strings.HasPrefix(apiKey, "env://") {
		return "configured"
	}

	// Check default env var.
	if provider.metadata.DefaultEnvVar != "" && os.Getenv(provider.metadata.DefaultEnvVar) != "" {
		return "configured"
	}

	return "not-configured"
}

// resolveProviderAPIKey resolves the API key for a provider. It checks the
// config map first, falling back to the given default environment variable.
// It returns an error if the key is missing or still contains an unresolved
// env:// reference.
func resolveProviderAPIKey(cfg *config.AskConfig, provider string, defaultEnvVar string) (string, error) {
	apiKey := cfg.APIKeys[provider]

	// Detect unresolved env:// references (env var was not set).
	if strings.HasPrefix(apiKey, "env://") {
		envVar := strings.TrimPrefix(apiKey, "env://")
		return "", fmt.Errorf("%s API key not configured: environment variable %s is not set", provider, envVar) //nolint:staticcheck
	}

	// Fall back to the default env var if no key was configured.
	if apiKey == "" {
		apiKey = os.Getenv(defaultEnvVar)
	}

	if apiKey == "" {
		return "", fmt.Errorf("%s API key not configured (set %s)", provider, defaultEnvVar) //nolint:staticcheck
	}

	return apiKey, nil
}
