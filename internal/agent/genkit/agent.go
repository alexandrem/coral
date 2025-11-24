package genkit

import (
	"context"
	"fmt"
	"strings"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/core/api"
	"github.com/firebase/genkit/go/genkit"
	"github.com/firebase/genkit/go/plugins/compat_oai"
	"github.com/firebase/genkit/go/plugins/googlegenai"
	"github.com/firebase/genkit/go/plugins/mcp"
	"github.com/firebase/genkit/go/plugins/ollama"

	"github.com/coral-io/coral/internal/config"
)

// Agent represents a Genkit-powered LLM agent that connects to Colony MCP server (RFD 030).
type Agent struct {
	genkit        *genkit.Genkit
	modelName     string
	provider      string
	config        *config.AskConfig
	colonyConfig  *config.ColonyConfig
	conversations map[string]*Conversation
	mcpClient     *mcp.GenkitMCPClient
}

// Response represents an agent response.
type Response struct {
	Answer    string
	ToolCalls []ToolCall
}

// ToolCall represents an MCP tool invocation.
type ToolCall struct {
	Name   string
	Input  any
	Output any
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
	g, modelName, provider, err := initializeGenkitWithModel(ctx, askCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Genkit: %w", err)
	}

	// Connect to Colony's MCP server.
	mcpClient, err := connectToColonyMCP(ctx, colonyCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to colony MCP server: %w", err)
	}

	return &Agent{
		genkit:        g,
		modelName:     modelName,
		provider:      provider,
		config:        askCfg,
		colonyConfig:  colonyCfg,
		conversations: make(map[string]*Conversation),
		mcpClient:     mcpClient,
	}, nil
}

// connectToColonyMCP connects to the Colony's MCP server via stdio.
func connectToColonyMCP(ctx context.Context, colonyCfg *config.ColonyConfig) (*mcp.GenkitMCPClient, error) {
	if colonyCfg.MCP.Disabled {
		return nil, fmt.Errorf("MCP server is disabled for this colony")
	}

	// Connect to Colony MCP server using stdio transport.
	// This launches `coral colony mcp proxy --colony <colony-id>` as a subprocess.
	clientOpts := mcp.MCPClientOptions{
		Name:    fmt.Sprintf("coral-%s", colonyCfg.ColonyID),
		Version: "1.0.0",
		Stdio: &mcp.StdioConfig{
			Command: "coral",
			Args:    []string{"colony", "mcp", "proxy", "--colony", colonyCfg.ColonyID},
		},
	}

	client, err := mcp.NewGenkitMCPClient(clientOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create MCP client: %w", err)
	}

	return client, nil
}

// initializeGenkitWithModel initializes Genkit with the correct provider plugin.
func initializeGenkitWithModel(ctx context.Context, cfg *config.AskConfig) (*genkit.Genkit, string, string, error) {
	// Parse model string (format: "provider:model-id").
	parts := strings.SplitN(cfg.DefaultModel, ":", 2)
	if len(parts) != 2 {
		return nil, "", "", fmt.Errorf("invalid model format %q, expected provider:model-id", cfg.DefaultModel)
	}

	provider := parts[0]
	modelID := parts[1]

	var g *genkit.Genkit
	var modelName string

	switch provider {
	case "openai":
		apiKey := cfg.APIKeys["openai"]
		if apiKey == "" {
			return nil, "", "", fmt.Errorf("OpenAI API key not configured (set OPENAI_API_KEY)")
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

	case "grok", "xai":
		apiKey := cfg.APIKeys["grok"]
		if apiKey == "" {
			// Try xai key as fallback
			apiKey = cfg.APIKeys["xai"]
			if apiKey == "" {
				return nil, "", "", fmt.Errorf("Grok API key not configured (set XAI_API_KEY)")
			}
		}
		// Initialize OpenAI-compatible plugin with Grok base URL.
		oaiPlugin := &compat_oai.OpenAICompatible{
			APIKey:   apiKey,
			BaseURL:  "https://api.x.ai/v1",
			Provider: "grok",
		}
		g = genkit.Init(ctx, genkit.WithPlugins(oaiPlugin))

		// Define and register the model.
		multimodal := compat_oai.Multimodal
		model := oaiPlugin.DefineModel("grok", modelID, ai.ModelOptions{
			Label:    fmt.Sprintf("Grok %s", modelID),
			Supports: &multimodal,
		})

		// Register with Genkit using genkit.DefineModel
		genkit.DefineModel(g, api.NewName("grok", modelID), &ai.ModelOptions{
			Label:    fmt.Sprintf("Grok %s", modelID),
			Supports: &multimodal,
		}, func(ctx context.Context, req *ai.ModelRequest, cb func(context.Context, *ai.ModelResponseChunk) error) (*ai.ModelResponse, error) {
			return model.Generate(ctx, req, cb)
		})
		modelName = "grok/" + modelID

	case "google", "googleai":
		apiKey := cfg.APIKeys["google"]
		if apiKey == "" {
			return nil, "", "", fmt.Errorf("Google AI API key not configured (set GOOGLE_API_KEY)")
		}
		g = genkit.Init(ctx, genkit.WithPlugins(&googlegenai.GoogleAI{APIKey: apiKey}))
		modelName = "googleai/" + modelID

	case "ollama":
		g = genkit.Init(ctx, genkit.WithPlugins(&ollama.Ollama{}))
		modelName = "ollama/" + modelID

	case "anthropic":
		return nil, "", "", fmt.Errorf("anthropic models are not supported for 'coral ask': Genkit's OpenAI-compatible wrapper doesn't properly support Anthropic's native MCP integration\n\nSupported providers:\n  - openai:gpt-4o, openai:gpt-4o-mini\n  - google:gemini-2.0-flash-exp\n  - ollama:llama3.2 (local)\n\nNote: We may implement custom Anthropic MCP provider in the future")

	default:
		return nil, "", "", fmt.Errorf("unsupported provider: %s (supported: openai, google, grok, ollama)", provider)
	}

	return g, modelName, provider, nil
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

	// Get MCP tools from Colony server.
	tools, err := a.mcpClient.GetActiveTools(ctx, a.genkit)
	if err != nil {
		return nil, fmt.Errorf("failed to get MCP tools: %w", err)
	}

	// Convert tools to ToolRef interface for Genkit.
	toolRefs := make([]ai.ToolRef, len(tools))
	for i, t := range tools {
		toolRefs[i] = t
	}

	// Build conversation history for the LLM.
	messages := conv.GetMessages()
	var history []*ai.Message
	for _, msg := range messages {
		role := ai.RoleUser
		if msg.Role == "assistant" {
			role = ai.RoleModel
		}
		history = append(history, &ai.Message{
			Role:    role,
			Content: []*ai.Part{ai.NewTextPart(msg.Content)},
		})
	}

	// Call LLM using Genkit with MCP tools.
	resp, err := genkit.Generate(ctx, a.genkit,
		ai.WithModelName(a.modelName),
		ai.WithMessages(history...),
		ai.WithTools(toolRefs...),
	)
	if err != nil {
		return nil, fmt.Errorf("LLM generation failed: %w", err)
	}

	// Extract answer from response.
	answer := resp.Text()

	// Extract tool calls from response.
	var toolCalls []ToolCall
	for _, toolReq := range resp.ToolRequests() {
		toolCalls = append(toolCalls, ToolCall{
			Name:   toolReq.Name,
			Input:  toolReq.Input,
			Output: nil, // Tool output would be in subsequent turns
		})
	}

	result := &Response{
		Answer:    answer,
		ToolCalls: toolCalls,
	}

	// Add assistant response to conversation.
	conv.AddMessage(Message{
		Role:    "assistant",
		Content: result.Answer,
	})

	return result, nil
}

// Close cleans up agent resources.
func (a *Agent) Close() error {
	if a.mcpClient != nil {
		if err := a.mcpClient.Disconnect(); err != nil {
			return fmt.Errorf("failed to disconnect MCP client: %w", err)
		}
	}
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
