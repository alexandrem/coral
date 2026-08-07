package llm

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/coral-mesh/coral/internal/config"
)

// defaultOllamaBaseURL is the default Ollama API endpoint.
const defaultOllamaBaseURL = "http://localhost:11434/v1"

// OllamaProvider implements the Provider interface for locally-running Ollama models.
// It reuses the OpenAI-compatible API that Ollama exposes at /v1.
type OllamaProvider struct {
	inner *OpenAIProvider
}

// NewOllamaProvider creates a new Ollama provider targeting the given base URL.
func NewOllamaProvider(baseURL string, modelName string) (*OllamaProvider, error) {
	// Ollama's OpenAI-compatible endpoint requires a non-empty API key value,
	// but ignores its contents.
	inner, err := NewOpenAIProvider("ollama", modelName, baseURL)
	if err != nil {
		return nil, err
	}
	return &OllamaProvider{inner: inner}, nil
}

// Name returns the provider name.
func (p *OllamaProvider) Name() string {
	return "ollama"
}

// Generate sends a request to Ollama and returns the response.
func (p *OllamaProvider) Generate(ctx context.Context, req GenerateRequest, streamCallback StreamCallback) (*GenerateResponse, error) {
	return p.inner.Generate(ctx, req, streamCallback)
}

// resolveOllamaBaseURL resolves the Ollama API base URL from the config or
// environment. Users can override via api_keys.ollama in coral config or the
// OLLAMA_HOST environment variable. Defaults to http://localhost:11434/v1.
func resolveOllamaBaseURL(cfg *config.AskConfig) string {
	// Config takes highest precedence (api_keys.ollama stores the base URL for Ollama).
	if url := cfg.APIKeys["ollama"]; url != "" {
		return normalizeOllamaURL(url)
	}

	// Fall back to OLLAMA_HOST env var (same var Ollama itself uses).
	if host := os.Getenv("OLLAMA_HOST"); host != "" {
		return normalizeOllamaURL(host)
	}

	return defaultOllamaBaseURL
}

// normalizeOllamaURL ensures the URL has a scheme and /v1 path suffix.
func normalizeOllamaURL(url string) string {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "http://" + url
	}
	url = strings.TrimRight(url, "/")
	if !strings.HasSuffix(url, "/v1") {
		url += "/v1"
	}
	return url
}

func init() {
	Register(ProviderMetadata{
		Name:           "ollama",
		DisplayName:    "Ollama",
		Description:    "Locally-running models via Ollama",
		DefaultEnvVar:  "", // No API key required.
		RequiresAPIKey: false,
		// Empty SupportedModels: any locally-installed model is accepted.
		SupportedModels: []ModelMetadata{
			{
				ID:            "llama3.2",
				DisplayName:   "Llama 3.2",
				Description:   "Meta's Llama 3.2 (3B/11B)",
				Capabilities:  []string{"tools", "streaming"},
				UseCase:       "fast",
				ContextWindow: 128_000,
				Recommended:   true,
			},
			{
				ID:            "llama3.1",
				DisplayName:   "Llama 3.1",
				Description:   "Meta's Llama 3.1 (8B/70B/405B)",
				Capabilities:  []string{"tools", "streaming"},
				UseCase:       "balanced",
				ContextWindow: 128_000,
			},
			{
				ID:            "qwen2.5-coder",
				DisplayName:   "Qwen 2.5 Coder",
				Description:   "Alibaba's code-focused model",
				Capabilities:  []string{"tools", "streaming"},
				UseCase:       "balanced",
				ContextWindow: 128_000,
			},
			{
				ID:            "mistral",
				DisplayName:   "Mistral",
				Description:   "Mistral 7B",
				Capabilities:  []string{"tools", "streaming"},
				UseCase:       "fast",
				ContextWindow: 32_000,
			},
			{
				ID:            "codellama",
				DisplayName:   "Code Llama",
				Description:   "Meta's code-focused Llama variant",
				Capabilities:  []string{"streaming"},
				UseCase:       "balanced",
				ContextWindow: 16_000,
			},
		},
	}, func(ctx context.Context, modelID string, cfg *config.AskConfig, debug bool) (Provider, error) {
		baseURL := resolveOllamaBaseURL(cfg)
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Ollama base URL: %s\n", baseURL)
		}
		return NewOllamaProvider(baseURL, modelID)
	})
}
