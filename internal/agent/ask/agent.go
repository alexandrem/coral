// Package ask provides the LLM agent implementation for coral ask.
package ask

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/coral-mesh/coral/internal/agent/llm"
	"github.com/coral-mesh/coral/internal/config"
)

// Agent represents an LLM agent that connects to Colony MCP server.
type Agent struct {
	provider      llm.Provider
	modelName     string
	config        *config.AskConfig
	colonyConfig  *config.ColonyConfig
	conversations map[string]*Conversation
	mcpClient     *client.Client
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
		fmt.Fprintln(os.Stderr, "[DEBUG] Initializing agent")
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

	// Parse model string (format: "provider:model-id" or bare "coral").
	var providerName, modelID string
	if askCfg.DefaultModel == "coral" {
		providerName = "coral"
	} else {
		parts := strings.SplitN(askCfg.DefaultModel, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid model format %q, expected provider:model-id (or bare \"coral\")", askCfg.DefaultModel)
		}
		providerName = parts[0]
		modelID = parts[1]
	}

	// Initialize the LLM provider.
	provider, err := createProvider(ctx, providerName, modelID, askCfg, debug)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize provider: %w", err)
	}

	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Initialized provider=%s with model=%s\n", providerName, modelID)
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
		provider:      provider,
		modelName:     modelID,
		config:        askCfg,
		colonyConfig:  colonyCfg,
		conversations: make(map[string]*Conversation),
		mcpClient:     mcpClient,
		debug:         debug,
	}, nil
}

// SetConversationHistory initializes a conversation with existing history.
func (a *Agent) SetConversationHistory(conversationID string, messages []Message) {
	conv := NewConversation(conversationID)
	for _, msg := range messages {
		conv.AddMessage(msg)
	}
	a.conversations[conversationID] = conv
}

// GetConversationHistory returns the history of a conversation.
func (a *Agent) GetConversationHistory(conversationID string) []Message {
	if conv, exists := a.conversations[conversationID]; exists {
		return conv.GetMessages()
	}
	return nil
}

// createProvider creates an LLM provider based on the provider name.
func createProvider(ctx context.Context, providerName string, modelID string, cfg *config.AskConfig, debug bool) (llm.Provider, error) {
	switch providerName {
	case "google":
		apiKey, err := resolveProviderAPIKey(cfg, "google", "GOOGLE_API_KEY")
		if err != nil {
			return nil, err
		}
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Google AI API key found (length: %d)\n", len(apiKey))
		}
		return llm.NewGoogleProvider(ctx, apiKey, modelID)

	case "openai":
		apiKey, err := resolveProviderAPIKey(cfg, "openai", "OPENAI_API_KEY")
		if err != nil {
			return nil, err
		}
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] OpenAI API key found (length: %d)\n", len(apiKey))
		}
		return llm.NewOpenAIProvider(apiKey, modelID, "")

	case "mock":
		// For mock provider, the modelID is the path to the replay script.
		return llm.NewMockProvider(ctx, modelID)

	case "coral":
		endpoint := cfg.APIKeys["coral_endpoint"]
		if endpoint == "" {
			endpoint = os.Getenv("CORAL_AI_ENDPOINT")
		}
		if endpoint == "" {
			return nil, fmt.Errorf("Coral AI endpoint not configured (set coral_endpoint in api_keys or CORAL_AI_ENDPOINT)") //nolint:staticcheck
		}
		apiToken := cfg.APIKeys["coral"]
		if apiToken == "" {
			// Coral's free tier allows anonymous access, so token is optional.
			apiToken = os.Getenv("CORAL_AI_TOKEN")
		}
		return llm.NewCoralProvider(ctx, endpoint, apiToken)

	// TODO: Add other providers
	// case "openai":
	// TODO: Add other providers.
	// case "anthropic":
	// case "grok", "xai":

	default:
		return nil, fmt.Errorf("unsupported provider: %s (supported: google, openai, coral, mock)",
			providerName)
	}
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
		return "", fmt.Errorf("%s API key not configured: environment variable %s is not set", provider, envVar) // nolint: staticcheck
	}

	// Fall back to the default env var if no key was configured.
	if apiKey == "" {
		apiKey = os.Getenv(defaultEnvVar)
	}

	if apiKey == "" {
		return "", fmt.Errorf("%s API key not configured (set %s)", provider, defaultEnvVar) // nolint: staticcheck
	}

	return apiKey, nil
}

// connectToColonyMCP connects to the Colony's MCP server via stdio subprocess.
func connectToColonyMCP(ctx context.Context, colonyCfg *config.ColonyConfig, debug bool) (*client.Client, error) {
	// Command to launch: coral colony mcp proxy --colony <colony-id>
	command := "coral"
	args := []string{"colony", "mcp", "proxy", "--colony", colonyCfg.ColonyID}
	env := os.Environ() // Use current environment

	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Connecting to Colony MCP server for colony: %s\n", colonyCfg.ColonyID)
		fmt.Fprintf(os.Stderr, "[DEBUG] MCP client command: coral %s\n", strings.Join(args, " "))
	}

	// Create MCP client with timeout to prevent hanging indefinitely.
	const connectionTimeout = 5 * time.Second
	type result struct {
		client *client.Client
		err    error
	}
	resultChan := make(chan result, 1)

	go func() {
		c, err := client.NewStdioMCPClient(command, env, args...)
		resultChan <- result{client: c, err: err}
	}()

	select {
	case res := <-resultChan:
		if res.err != nil {
			return nil, fmt.Errorf("failed to create MCP client: %w", res.err)
		}
		if debug {
			fmt.Fprintln(os.Stderr, "[DEBUG] MCP client connection established")
		}

		// Initialize the MCP client (protocol handshake).
		if debug {
			fmt.Fprintln(os.Stderr, "[DEBUG] Initializing MCP client...")
		}
		initReq := mcp.InitializeRequest{}
		initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
		initReq.Params.ClientInfo = mcp.Implementation{
			Name:    "coral-ask",
			Version: "1.0.0",
		}
		initReq.Params.Capabilities = mcp.ClientCapabilities{
			// Request tools capability from server.
			Experimental: map[string]interface{}{},
		}

		initCtx, initCancel := context.WithTimeout(ctx, connectionTimeout)
		defer initCancel()

		if _, err := res.client.Initialize(initCtx, initReq); err != nil {
			res.client.Close() // nolint:errcheck // cli command will exit anyway
			return nil, fmt.Errorf("failed to initialize MCP client: %w", err)
		}

		if debug {
			fmt.Fprintln(os.Stderr, "[DEBUG] MCP client initialized successfully")
		}

		return res.client, nil
	case <-time.After(connectionTimeout):
		return nil, fmt.Errorf("MCP client connection timed out after %v. Is the colony running? Check with: coral colony list", connectionTimeout)
	}
}

// Close closes the agent and cleans up resources (MCP client connection).
func (a *Agent) Close() error {
	if a.mcpClient != nil {
		return a.mcpClient.Close()
	}
	return nil
}

// buildSystemPrompt builds the system prompt with service context (RFD 054).
func (a *Agent) buildSystemPrompt(ctx context.Context) string {
	// Fetch available services from the colony.
	serviceList := a.fetchServiceList(ctx)

	// Build system prompt with parameter extraction rules.
	prompt := `You are an observability assistant for Coral distributed systems.

PARAMETER EXTRACTION RULES:
1. Service names: Extract exactly as mentioned (e.g., "coral service" → "coral")
   Available services: ` + serviceList + `
2. Time ranges: Convert natural language (e.g., "last hour" → "1h", "30 min" → "30m")
3. HTTP methods: Extract from context (e.g., "GET requests" → "GET")
4. Status codes: Map phrases (e.g., "errors" → "5xx", "success" → "2xx")

Always extract ALL relevant parameters from the user's query before asking for clarification.`

	return prompt
}

// fetchServiceList fetches the list of available services from the colony.
func (a *Agent) fetchServiceList(ctx context.Context) string {
	// Call coral_list_services tool to get available services.
	req := mcp.CallToolRequest{}
	req.Params.Name = "coral_list_services"
	req.Params.Arguments = map[string]interface{}{}

	result, err := a.mcpClient.CallTool(ctx, req)
	if err != nil {
		if a.debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Failed to fetch service list: %v\n", err)
		}
		return "(service list unavailable)"
	}

	// Parse the result to extract service names.
	if len(result.Content) > 0 {
		if textContent, ok := mcp.AsTextContent(result.Content[0]); ok {
			// Parse JSON response.
			var output struct {
				Services []struct {
					Name string `json:"name"`
				} `json:"services"`
			}
			if err := json.Unmarshal([]byte(textContent.Text), &output); err == nil {
				// Build comma-separated list of service names.
				names := make([]string, 0, len(output.Services))
				for _, svc := range output.Services {
					names = append(names, svc.Name)
				}
				if len(names) > 0 {
					return strings.Join(names, ", ")
				}
			}
		}
	}

	return "(no services registered)"
}

// Ask sends a question to the agent and returns the response.
func (a *Agent) Ask(ctx context.Context, question string, conversationID string, stream bool, dryRun bool) (*Response, error) {
	if a.debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Processing question: %q\n", question)
	}

	// Get or create conversation.
	conv, exists := a.conversations[conversationID]
	if !exists {
		conv = NewConversation(conversationID)
		a.conversations[conversationID] = conv
		if a.debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Conversation ID: %s\n", conversationID)
		}
	}

	// Add user message.
	conv.AddMessage(Message{
		Role:    "user",
		Content: question,
	})

	// Build system prompt with service context (RFD 054).
	systemPrompt := a.buildSystemPrompt(ctx)
	if a.debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] System prompt: %s\n", systemPrompt)
	}

	// Get MCP tools from Colony server.
	if a.debug {
		fmt.Fprintln(os.Stderr, "[DEBUG] Fetching MCP tools from Colony server")
	}

	toolsResult, err := a.mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	// Debug: Log the raw ListToolsResult to see what came from the server
	if a.debug {
		rawResultJSON, _ := json.Marshal(toolsResult)
		fmt.Fprintf(os.Stderr, "[DEBUG] Raw ListToolsResult from server: %s\n", string(rawResultJSON))
	}

	if a.debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Retrieved %d MCP tools\n", len(toolsResult.Tools))
		for i, tool := range toolsResult.Tools {
			fmt.Fprintf(os.Stderr, "[DEBUG]   Tool %d: %s\n", i+1, tool.Name)
		}

		// Debug: Inspect first tool's schema to verify it's correct.
		if len(toolsResult.Tools) > 0 {
			firstTool := toolsResult.Tools[0]

			// Show the full tool as received
			fullToolJSON, _ := json.Marshal(firstTool)
			fmt.Fprintf(os.Stderr, "[DEBUG] First tool (full): %s\n", string(fullToolJSON))

			// Show just the InputSchema struct field
			schemaJSON, _ := json.Marshal(firstTool.InputSchema)
			fmt.Fprintf(os.Stderr, "[DEBUG] First tool InputSchema struct: %s\n", string(schemaJSON))

			// Show RawInputSchema if set
			if len(firstTool.RawInputSchema) > 0 {
				fmt.Fprintf(os.Stderr, "[DEBUG] First tool RawInputSchema: %s\n", string(firstTool.RawInputSchema))
			} else {
				fmt.Fprintf(os.Stderr, "[DEBUG] First tool RawInputSchema: <empty>\n")
			}
		}
	}

	// Convert conversation messages to LLM format.
	var llmMessages []llm.Message
	for _, msg := range conv.GetMessages() {
		llmMessages = append(llmMessages, llm.Message{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	// Call LLM with tools.
	streamCallback := func(chunk string) error {
		if stream {
			fmt.Print(chunk)
		}
		return nil
	}

	generateReq := llm.GenerateRequest{
		Messages:     llmMessages,
		Tools:        toolsResult.Tools,
		Stream:       stream,
		SystemPrompt: systemPrompt,
	}

	if a.debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Calling LLM with %d messages and %d tools\n", len(llmMessages), len(toolsResult.Tools))

		// Debug: Show all tool schemas being sent to LLM
		for i, tool := range toolsResult.Tools {
			schemaJSON, _ := json.Marshal(tool.InputSchema)
			fmt.Fprintf(os.Stderr, "[DEBUG] Tool %d (%s): %s\n", i, tool.Name, string(schemaJSON))
		}

		// Debug: Show complete payload being sent to LLM
		fmt.Fprintln(os.Stderr, "\n[DEBUG] ===== LLM REQUEST PAYLOAD =====")
		fmt.Fprintf(os.Stderr, "[DEBUG] System Prompt:\n%s\n\n", generateReq.SystemPrompt)
		fmt.Fprintf(os.Stderr, "[DEBUG] Messages (%d):\n", len(generateReq.Messages))
		for i, msg := range generateReq.Messages {
			fmt.Fprintf(os.Stderr, "[DEBUG]   Message %d [%s]: %s\n", i+1, msg.Role, msg.Content)
			if len(msg.ToolResponses) > 0 {
				fmt.Fprintf(os.Stderr, "[DEBUG]     Tool Responses: %d\n", len(msg.ToolResponses))
			}
		}
		fmt.Fprintf(os.Stderr, "[DEBUG] Tools: %d available\n", len(generateReq.Tools))
		fmt.Fprintf(os.Stderr, "[DEBUG] Stream: %v\n", generateReq.Stream)
		fmt.Fprintln(os.Stderr, "[DEBUG] ==============================")
		fmt.Fprintln(os.Stderr, "")
	}

	// Dry-run mode: prompt user before sending to LLM
	if dryRun {
		fmt.Fprintln(os.Stderr, "\n[DRY-RUN] Ready to send request to LLM provider")
		fmt.Fprintf(os.Stderr, "[DRY-RUN] Model: %s\n", a.modelName)
		fmt.Fprintf(os.Stderr, "[DRY-RUN] System prompt length: %d chars\n", len(generateReq.SystemPrompt))
		fmt.Fprintf(os.Stderr, "[DRY-RUN] Message count: %d\n", len(generateReq.Messages))
		fmt.Fprintf(os.Stderr, "[DRY-RUN] Tool count: %d\n", len(generateReq.Tools))
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprint(os.Stderr, "[DRY-RUN] Send this request to the LLM? (y/N): ")

		var response string
		if _, err := fmt.Fscanln(os.Stdin, &response); err != nil {
			return nil, fmt.Errorf("failed to read response from LLM provider: %w", err)
		}
		if response != "y" && response != "Y" {
			return nil, fmt.Errorf("dry-run aborted by user")
		}
		fmt.Fprintln(os.Stderr, "[DRY-RUN] Proceeding with LLM request...")
		fmt.Fprintln(os.Stderr, "")
	}

	resp, err := a.provider.Generate(ctx, generateReq, streamCallback)
	if err != nil {
		return nil, fmt.Errorf("LLM generation failed: %w", err)
	}

	if a.debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] LLM response: finish_reason=%s, tool_calls=%d, content_length=%d\n",
			resp.FinishReason, len(resp.ToolCalls), len(resp.Content))
	}

	// Handle tool calls.
	var toolCallResults []ToolCall
	if len(resp.ToolCalls) > 0 {
		if a.debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Processing %d tool calls\n", len(resp.ToolCalls))
		}

		// Execute all tool calls.
		var toolResponses []llm.ToolResponse
		for _, tc := range resp.ToolCalls {
			if a.debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] Executing tool: %s\n", tc.Name)
			}

			// Parse arguments.
			var args map[string]interface{}
			if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
				return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
			}

			// Call the tool via MCP.
			req := mcp.CallToolRequest{}
			req.Params.Name = tc.Name
			req.Params.Arguments = args

			toolResult, err := a.mcpClient.CallTool(ctx, req)
			if err != nil {
				return nil, fmt.Errorf("tool call failed: %w", err)
			}

			// Extract text content from tool result.
			var resultContent string
			if len(toolResult.Content) > 0 {
				// MCP returns content as an array of text/image/resource items.
				// For now, just concatenate text content.
				for _, content := range toolResult.Content {
					if textContent, ok := mcp.AsTextContent(content); ok {
						resultContent += textContent.Text
					}
				}
			}

			toolCallResults = append(toolCallResults, ToolCall{
				Name:   tc.Name,
				Input:  args,
				Output: toolResult,
			})

			toolResponses = append(toolResponses, llm.ToolResponse{
				CallID:  tc.ID,
				Name:    tc.Name,
				Content: resultContent,
			})

			if a.debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] Tool %s completed\n", tc.Name)
			}
		}

		// Add assistant's tool call response to conversation.
		conv.AddMessage(Message{
			Role:    "assistant",
			Content: resp.Content,
		})

		// Add tool results to conversation.
		conv.AddMessage(Message{
			Role:          "tool",
			ToolResponses: toolResponses,
		})

		// Send tool results back to LLM for final answer.
		if a.debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Sending tool results back to LLM for final answer\n")
		}

		// Convert conversation messages to LLM format.
		var llmMessages []llm.Message
		for _, msg := range conv.GetMessages() {
			llmMessages = append(llmMessages, llm.Message{
				Role:          msg.Role,
				Content:       msg.Content,
				ToolResponses: msg.ToolResponses,
			})
		}

		// Call LLM again with tool results.
		finalReq := llm.GenerateRequest{
			Messages:     llmMessages,
			Tools:        toolsResult.Tools,
			Stream:       stream,
			SystemPrompt: systemPrompt,
		}

		if a.debug {
			fmt.Fprintln(os.Stderr, "\n[DEBUG] ===== FINAL LLM REQUEST (After Tool Calls) =====")
			fmt.Fprintf(os.Stderr, "[DEBUG] System Prompt:\n%s\n\n", finalReq.SystemPrompt)
			fmt.Fprintf(os.Stderr, "[DEBUG] Messages (%d):\n", len(finalReq.Messages))
			for i, msg := range finalReq.Messages {
				fmt.Fprintf(os.Stderr, "[DEBUG]   Message %d [%s]: %s\n", i+1, msg.Role, msg.Content)
				if len(msg.ToolResponses) > 0 {
					fmt.Fprintf(os.Stderr, "[DEBUG]     Tool Responses: %d\n", len(msg.ToolResponses))
					for j, tr := range msg.ToolResponses {
						// Show first 200 chars of tool response content
						content := tr.Content
						if len(content) > 200 {
							content = content[:200] + "..."
						}
						fmt.Fprintf(os.Stderr, "[DEBUG]       Response %d [%s]: %s\n", j+1, tr.Name, content)
					}
				}
			}
			fmt.Fprintf(os.Stderr, "[DEBUG] Tools: %d available\n", len(finalReq.Tools))
			fmt.Fprintf(os.Stderr, "[DEBUG] Stream: %v\n", finalReq.Stream)
			fmt.Fprintln(os.Stderr, "[DEBUG] ==========================================")
			fmt.Fprintln(os.Stderr, "")
		}

		// Dry-run mode: prompt before final LLM call
		if dryRun {
			fmt.Fprintln(os.Stderr, "\n[DRY-RUN] Ready to send final request to LLM (with tool results)")
			fmt.Fprintf(os.Stderr, "[DRY-RUN] Message count: %d\n", len(finalReq.Messages))
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprint(os.Stderr, "[DRY-RUN] Send final request to the LLM? (y/N): ")

			var response string
			if _, err := fmt.Fscanln(os.Stdin, &response); err != nil {
				return nil, fmt.Errorf("failed to read final response from LLM: %w", err)
			}
			if response != "y" && response != "Y" {
				// Return partial response with tool calls but no final answer
				return &Response{
					Answer:    "(dry-run aborted before final LLM call)",
					ToolCalls: toolCallResults,
				}, nil
			}
			fmt.Fprintln(os.Stderr, "[DRY-RUN] Proceeding with final LLM request...")
			fmt.Fprintln(os.Stderr, "")
		}

		finalResp, err := a.provider.Generate(ctx, finalReq, streamCallback)
		if err != nil {
			return nil, fmt.Errorf("LLM final response failed: %w", err)
		}

		if a.debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Final LLM response: content_length=%d\n", len(finalResp.Content))
		}

		// Add final response to conversation.
		conv.AddMessage(Message{
			Role:    "assistant",
			Content: finalResp.Content,
		})

		return &Response{
			Answer:    finalResp.Content,
			ToolCalls: toolCallResults,
		}, nil
	}

	// No tool calls - just add the response to conversation.
	conv.AddMessage(Message{
		Role:    "assistant",
		Content: resp.Content,
	})

	return &Response{
		Answer:    resp.Content,
		ToolCalls: toolCallResults,
	}, nil
}
