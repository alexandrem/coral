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

	"github.com/coral-io/coral/internal/agent/llm"
	"github.com/coral-io/coral/internal/config"
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

	// Parse model string (format: "provider:model-id").
	parts := strings.SplitN(askCfg.DefaultModel, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid model format %q, expected provider:model-id", askCfg.DefaultModel)
	}

	providerName := parts[0]
	modelID := parts[1]

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

// createProvider creates an LLM provider based on the provider name.
func createProvider(ctx context.Context, providerName string, modelID string, cfg *config.AskConfig, debug bool) (llm.Provider, error) {
	switch providerName {
	case "google":
		apiKey := cfg.APIKeys["google"]
		if apiKey == "" {
			return nil, fmt.Errorf("Google AI API key not configured (set GOOGLE_API_KEY)") // nolint: staticcheck
		}
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Google AI API key found (length: %d)\n", len(apiKey))
		}
		return llm.NewGoogleProvider(ctx, apiKey, modelID)

	// TODO: Add other providers
	// case "openai":
	// case "anthropic":
	// case "grok", "xai":

	default:
		return nil, fmt.Errorf("unsupported provider: %s (supported: google)", providerName)
	}
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

		if _, err := res.client.Initialize(ctx, initReq); err != nil {
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

// Ask sends a question to the agent and returns the response.
func (a *Agent) Ask(ctx context.Context, question string, conversationID string, stream bool) (*Response, error) {
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
		Messages: llmMessages,
		Tools:    toolsResult.Tools,
		Stream:   stream,
	}

	if a.debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Calling LLM with %d messages and %d tools\n", len(llmMessages), len(toolsResult.Tools))

		// Debug: Show all tool schemas being sent to LLM
		for i, tool := range toolsResult.Tools {
			schemaJSON, _ := json.Marshal(tool.InputSchema)
			fmt.Fprintf(os.Stderr, "[DEBUG] Tool %d (%s): %s\n", i, tool.Name, string(schemaJSON))
		}
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
			Messages: llmMessages,
			Tools:    toolsResult.Tools,
			Stream:   stream,
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
