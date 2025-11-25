package genkit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

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
	debug         bool
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
func NewAgent(askCfg *config.AskConfig, colonyCfg *config.ColonyConfig, debug bool) (*Agent, error) {
	if debug {
		fmt.Fprintln(os.Stderr, "[DEBUG] Initializing Genkit agent")
	}

	if askCfg == nil {
		return nil, fmt.Errorf("ask config is required")
	}
	if colonyCfg == nil {
		return nil, fmt.Errorf("colony config is required")
	}

	ctx := context.Background()

	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Model configuration: %s\n", askCfg.DefaultModel)
	}

	// Initialize Genkit with the appropriate plugin based on model.
	g, modelName, provider, err := initializeGenkitWithModel(ctx, askCfg, debug)
	if err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Failed to initialize Genkit: %v\n", err)
		}
		return nil, fmt.Errorf("failed to initialize Genkit: %w", err)
	}

	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Initialized Genkit with provider=%s, model=%s\n", provider, modelName)
	}

	// Connect to Colony's MCP server.
	mcpClient, err := connectToColonyMCP(ctx, colonyCfg, debug)
	if err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Failed to connect to MCP server: %v\n", err)
		}
		return nil, fmt.Errorf("failed to connect to colony MCP server: %w", err)
	}

	if debug {
		fmt.Fprintln(os.Stderr, "[DEBUG] Successfully connected to Colony MCP server")
	}

	return &Agent{
		genkit:        g,
		modelName:     modelName,
		provider:      provider,
		config:        askCfg,
		colonyConfig:  colonyCfg,
		conversations: make(map[string]*Conversation),
		mcpClient:     mcpClient,
		debug:         debug,
	}, nil
}

// connectToColonyMCP connects to the Colony's MCP server via stdio.
func connectToColonyMCP(ctx context.Context, colonyCfg *config.ColonyConfig, debug bool) (*mcp.GenkitMCPClient, error) {
	if colonyCfg.MCP.Disabled {
		return nil, fmt.Errorf("MCP server is disabled for this colony")
	}

	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Connecting to Colony MCP server for colony: %s\n", colonyCfg.ColonyID)
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

	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] MCP client command: coral colony mcp proxy --colony %s\n", colonyCfg.ColonyID)
	}

	// Create MCP client with timeout to prevent hanging indefinitely.
	const connectionTimeout = 5 * time.Second
	type result struct {
		client *mcp.GenkitMCPClient
		err    error
	}
	resultChan := make(chan result, 1)

	go func() {
		client, err := mcp.NewGenkitMCPClient(clientOpts)
		resultChan <- result{client: client, err: err}
	}()

	select {
	case res := <-resultChan:
		if res.err != nil {
			return nil, fmt.Errorf("failed to create MCP client: %w", res.err)
		}
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] MCP client connection established\n")
		}
		return res.client, nil
	case <-time.After(connectionTimeout):
		return nil, fmt.Errorf("MCP client connection timed out after %v. Is the colony running? Check with: coral colony list", connectionTimeout)
	}
}

// initializeGenkitWithModel initializes Genkit with the correct provider plugin.
func initializeGenkitWithModel(ctx context.Context, cfg *config.AskConfig, debug bool) (*genkit.Genkit, string, string, error) {
	// Parse model string (format: "provider:model-id").
	parts := strings.SplitN(cfg.DefaultModel, ":", 2)
	if len(parts) != 2 {
		return nil, "", "", fmt.Errorf("invalid model format %q, expected provider:model-id", cfg.DefaultModel)
	}

	provider := parts[0]
	modelID := parts[1]

	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Initializing provider=%s with model=%s\n", provider, modelID)
	}

	var g *genkit.Genkit
	var modelName string

	switch provider {
	case "openai":
		apiKey := cfg.APIKeys["openai"]
		if apiKey == "" {
			return nil, "", "", fmt.Errorf("OpenAI API key not configured (set OPENAI_API_KEY)")
		}
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] OpenAI API key found (length: %d)\n", len(apiKey))
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
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Defined OpenAI model: %s\n", modelName)
		}

	case "grok", "xai":
		apiKey := cfg.APIKeys["grok"]
		if apiKey == "" {
			// Try xai key as fallback
			apiKey = cfg.APIKeys["xai"]
			if apiKey == "" {
				return nil, "", "", fmt.Errorf("Grok API key not configured (set XAI_API_KEY)")
			}
		}
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Grok/XAI API key found (length: %d)\n", len(apiKey))
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
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Defined Grok model: %s\n", modelName)
		}

	case "google", "googleai":
		apiKey := cfg.APIKeys["google"]
		if apiKey == "" {
			return nil, "", "", fmt.Errorf("Google AI API key not configured (set GOOGLE_API_KEY)")
		}
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Google AI API key found (length: %d)\n", len(apiKey))
		}
		g = genkit.Init(ctx, genkit.WithPlugins(&googlegenai.GoogleAI{APIKey: apiKey}))
		modelName = "googleai/" + modelID
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Defined Google AI model: %s\n", modelName)
		}

	case "ollama":
		if debug {
			fmt.Fprintln(os.Stderr, "[DEBUG] Initializing Ollama (no API key required)")
		}
		g = genkit.Init(ctx, genkit.WithPlugins(&ollama.Ollama{}))
		modelName = "ollama/" + modelID
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Defined Ollama model: %s\n", modelName)
		}

	case "anthropic":
		return nil, "", "", fmt.Errorf("anthropic models are not supported for 'coral ask': Genkit's OpenAI-compatible wrapper doesn't properly support Anthropic's native MCP integration\n\nSupported providers:\n  - openai:gpt-4o, openai:gpt-4o-mini\n  - google:gemini-2.0-flash-exp\n  - ollama:llama3.2 (local)\n\nNote: We may implement custom Anthropic MCP provider in the future")

	default:
		return nil, "", "", fmt.Errorf("unsupported provider: %s (supported: openai, google, grok, ollama)", provider)
	}

	return g, modelName, provider, nil
}

// Ask sends a question to the LLM and returns the response.
func (a *Agent) Ask(ctx context.Context, question, conversationID string) (*Response, error) {
	if a.debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Processing question: %q\n", question)
		fmt.Fprintf(os.Stderr, "[DEBUG] Conversation ID: %s\n", conversationID)
	}

	// Get or create conversation.
	conv := a.getOrCreateConversation(conversationID)

	// Add user message to conversation.
	conv.AddMessage(Message{
		Role:    "user",
		Content: question,
	})

	// Get MCP tools from Colony server.
	if a.debug {
		fmt.Fprintln(os.Stderr, "[DEBUG] Fetching MCP tools from Colony server")
	}

	tools, err := a.mcpClient.GetActiveTools(ctx, a.genkit)
	if err != nil {
		if a.debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Failed to get MCP tools: %v\n", err)
		}
		return nil, fmt.Errorf("failed to get MCP tools: %w", err)
	}

	if a.debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Retrieved %d MCP tools\n", len(tools))
		for i, tool := range tools {
			fmt.Fprintf(os.Stderr, "[DEBUG]   Tool %d: %s\n", i+1, tool.Name())
		}
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

	if a.debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Calling LLM Generate with:\n")
		fmt.Fprintf(os.Stderr, "[DEBUG]   Model: %s\n", a.modelName)
		fmt.Fprintf(os.Stderr, "[DEBUG]   Messages: %d\n", len(history))

		// Dump full message content.
		for i, msg := range history {
			fmt.Fprintf(os.Stderr, "[DEBUG]   Message[%d]:\n", i)
			fmt.Fprintf(os.Stderr, "[DEBUG]     Role: %s\n", msg.Role)
			for j, part := range msg.Content {
				if part.IsText() {
					fmt.Fprintf(os.Stderr, "[DEBUG]     Part[%d] (text): %q\n", j, part.Text)
				} else {
					fmt.Fprintf(os.Stderr, "[DEBUG]     Part[%d]: (non-text)\n", j)
				}
			}
		}

		fmt.Fprintf(os.Stderr, "[DEBUG]   Tools: %d\n", len(tools))

		// Dump tool schemas.
		for i, tool := range tools {
			fmt.Fprintf(os.Stderr, "[DEBUG]   Tool[%d]:\n", i)
			fmt.Fprintf(os.Stderr, "[DEBUG]     Name: %s\n", tool.Name())

			// Try to dump the schema if available.
			if def := tool.Definition(); def != nil {
				fmt.Fprintf(os.Stderr, "[DEBUG]     Description: %s\n", def.Description)

				if def.InputSchema != nil {
					schemaJSON, err := json.MarshalIndent(def.InputSchema, "      ", "  ")
					if err == nil {
						fmt.Fprintf(os.Stderr, "[DEBUG]     InputSchema:\n%s\n", string(schemaJSON))
					} else {
						fmt.Fprintf(os.Stderr, "[DEBUG]     InputSchema: (error marshaling: %v)\n", err)
					}
				} else {
					fmt.Fprintf(os.Stderr, "[DEBUG]     InputSchema: nil\n")
				}

				if def.OutputSchema != nil {
					schemaJSON, err := json.MarshalIndent(def.OutputSchema, "      ", "  ")
					if err == nil {
						fmt.Fprintf(os.Stderr, "[DEBUG]     OutputSchema:\n%s\n", string(schemaJSON))
					} else {
						fmt.Fprintf(os.Stderr, "[DEBUG]     OutputSchema: (error marshaling: %v)\n", err)
					}
				} else {
					fmt.Fprintf(os.Stderr, "[DEBUG]     OutputSchema: nil\n")
				}
			}
		}
	}

	// Convert tools to ToolRef interface for Genkit.
	toolRefs := make([]ai.ToolRef, len(tools))
	for i, t := range tools {
		toolRefs[i] = t
	}

	// Call LLM using Genkit with MCP tools.
	resp, err := genkit.Generate(ctx, a.genkit,
		ai.WithModelName(a.modelName),
		ai.WithMessages(history...),
		ai.WithTools(toolRefs...),
	)
	if err != nil {
		if a.debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] LLM generation failed: %v\n", err)
		}
		return nil, fmt.Errorf("LLM generation failed: %w", err)
	}

	if a.debug {
		fmt.Fprintln(os.Stderr, "[DEBUG] LLM generation successful")
	}

	// Extract answer from response.
	answer := resp.Text()

	if a.debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Response text length: %d characters\n", len(answer))
	}

	// Extract tool calls from response.
	var toolCalls []ToolCall
	for _, toolReq := range resp.ToolRequests() {
		toolCalls = append(toolCalls, ToolCall{
			Name:   toolReq.Name,
			Input:  toolReq.Input,
			Output: nil, // Tool output would be in subsequent turns
		})
	}

	if a.debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Tool requests in response: %d\n", len(toolCalls))
		for i, tc := range toolCalls {
			fmt.Fprintf(os.Stderr, "[DEBUG]   Tool request %d: %s\n", i+1, tc.Name)
		}
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
