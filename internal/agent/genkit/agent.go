package genkit

import (
	"context"
	"fmt"
	"strings"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
	"github.com/firebase/genkit/go/plugins/compat_oai"
	"github.com/firebase/genkit/go/plugins/googlegenai"
	"github.com/firebase/genkit/go/plugins/ollama"

	"github.com/coral-io/coral/internal/config"
)

// Agent represents a Genkit-powered LLM agent that connects to Colony MCP server (RFD 030).
type Agent struct {
	genkit        *genkit.Genkit
	modelName     string
	config        *config.AskConfig
	colonyConfig  *config.ColonyConfig
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

	ctx := context.Background()

	// Initialize Genkit with the appropriate plugin based on model.
	g, modelName, err := initializeGenkitWithModel(ctx, askCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Genkit: %w", err)
	}

	return &Agent{
		genkit:        g,
		modelName:     modelName,
		config:        askCfg,
		colonyConfig:  colonyCfg,
		conversations: make(map[string]*Conversation),
	}, nil
}

// initializeGenkitWithModel initializes Genkit with the correct provider plugin.
func initializeGenkitWithModel(ctx context.Context, cfg *config.AskConfig) (*genkit.Genkit, string, error) {
	// Parse model string (format: "provider:model-id").
	parts := strings.SplitN(cfg.DefaultModel, ":", 2)
	if len(parts) != 2 {
		return nil, "", fmt.Errorf("invalid model format %q, expected provider:model-id", cfg.DefaultModel)
	}

	provider := parts[0]
	modelID := parts[1]

	var g *genkit.Genkit
	var modelName string

	switch provider {
	case "openai":
		apiKey := cfg.APIKeys["openai"]
		if apiKey == "" {
			return nil, "", fmt.Errorf("OpenAI API key not configured (set OPENAI_API_KEY)")
		}
		// Initialize OpenAI-compatible plugin.
		oaiPlugin := &compat_oai.OpenAICompatible{
			APIKey: apiKey,
		}
		g = genkit.Init(ctx, genkit.WithPlugins(oaiPlugin))

		// Define the model.
		multimodal := compat_oai.Multimodal
		oaiPlugin.DefineModel("openai", modelID, ai.ModelOptions{
			Label:    fmt.Sprintf("OpenAI %s", modelID),
			Supports: &multimodal,
		})
		modelName = "openai/" + modelID

	case "google", "googleai":
		apiKey := cfg.APIKeys["google"]
		if apiKey == "" {
			return nil, "", fmt.Errorf("Google AI API key not configured (set GOOGLE_API_KEY)")
		}
		g = genkit.Init(ctx, genkit.WithPlugins(&googlegenai.GoogleAI{APIKey: apiKey}))
		modelName = "googleai/" + modelID

	case "ollama":
		g = genkit.Init(ctx, genkit.WithPlugins(&ollama.Ollama{}))
		modelName = "ollama/" + modelID

	default:
		return nil, "", fmt.Errorf("unsupported provider: %s (supported: openai, google, ollama)", provider)
	}

	return g, modelName, nil
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

	// Call LLM using Genkit.
	// TODO: Add MCP tools integration.
	text, err := genkit.GenerateText(ctx, a.genkit,
		ai.WithModelName(a.modelName),
		ai.WithPrompt(question),
	)
	if err != nil {
		return nil, fmt.Errorf("LLM generation failed: %w", err)
	}

	resp := &Response{
		Answer:    text,
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
