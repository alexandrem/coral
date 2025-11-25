package ask

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	askagent "github.com/coral-io/coral/internal/agent/ask"
	"github.com/coral-io/coral/internal/config"
)

// NewAskCmd creates the ask command (RFD 030).
func NewAskCmd() *cobra.Command {
	var (
		colonyID string
		model    string
		stream   bool
		jsonFlag bool
		cont     bool // --continue flag for multi-turn conversations
		debug    bool // --debug flag for verbose logging
	)

	cmd := &cobra.Command{
		Use:   "ask <question>",
		Short: "Ask questions about your application using AI",
		Long: `Ask questions about your application using AI-powered analysis.

The LLM agent runs locally on your machine and connects to the colony's
MCP server to access observability data, metrics, traces, and logs.

Examples:
  # Ask a question about current system state
  coral ask "why is checkout slow?"

  # Override model for this query
  coral ask "complex root cause analysis" --model anthropic:claude-3-5-sonnet-20241022

  # Continue previous conversation
  coral ask "show me the actual traces" --continue

  # Use local model (offline)
  coral ask "what's the current status?" --model ollama:llama3.2

  # JSON output for scripting
  coral ask "list unhealthy services" --json`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			question := strings.Join(args, " ")
			return runAsk(cmd.Context(), question, colonyID, model, stream, jsonFlag, cont, debug)
		},
	}

	cmd.Flags().StringVarP(&colonyID, "colony-id", "c", "", "Colony ID to query (defaults to current colony)")
	cmd.Flags().StringVar(&model, "model", "", "Override model for this query (e.g., openai:gpt-4o-mini)")
	cmd.Flags().BoolVar(&stream, "stream", true, "Stream output progressively (default: true)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "Output JSON format for scripting")
	cmd.Flags().BoolVar(&cont, "continue", false, "Continue previous conversation")
	cmd.Flags().BoolVar(&debug, "debug", false, "Enable debug logging to stderr")

	return cmd
}

// runAsk executes the ask command.
func runAsk(ctx context.Context, question, colonyID, modelOverride string, stream, jsonOutput, continueConv, debug bool) error {
	// Configure debug logger.
	if debug {
		fmt.Fprintln(os.Stderr, "[DEBUG] Debug mode enabled")
	}

	// Load configuration.
	loader, err := config.NewLoader()
	if err != nil {
		return fmt.Errorf("failed to create config loader: %w", err)
	}

	globalCfg, err := loader.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("failed to load global config: %w", err)
	}

	// Determine colony ID.
	if colonyID == "" {
		colonyID = globalCfg.DefaultColony
		if colonyID == "" {
			return fmt.Errorf("no colony specified and no default colony configured")
		}
	}

	// Load colony config.
	colonyCfg, err := loader.LoadColonyConfig(colonyID)
	if err != nil {
		return fmt.Errorf("failed to load colony config: %w", err)
	}

	// Resolve ask configuration.
	askCfg, err := config.ResolveAskConfig(globalCfg, colonyCfg)
	if err != nil {
		return fmt.Errorf("failed to resolve ask config: %w", err)
	}

	// Apply model override if specified.
	if modelOverride != "" {
		askCfg.DefaultModel = modelOverride
	}

	// Validate configuration.
	if err := config.ValidateAskConfig(askCfg); err != nil {
		return fmt.Errorf("invalid ask config: %w", err)
	}

	// Create agent.
	agent, err := askagent.NewAgent(askCfg, colonyCfg, debug)
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}
	defer func() {
		if err := agent.Close(); err != nil && debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Failed to close agent: %v\n", err)
		}
	}()

	// Load or create conversation.
	var conversationID string
	if continueConv {
		conversationID, err = loadLastConversationID(colonyID)
		if err != nil {
			return fmt.Errorf("failed to load conversation: %w", err)
		}
	} else {
		conversationID = generateConversationID()
	}

	// Validate model compatibility with MCP tool calling.
	if strings.HasPrefix(askCfg.DefaultModel, "grok:") || strings.HasPrefix(askCfg.DefaultModel, "xai:") {
		return fmt.Errorf("grok models are not supported for 'coral ask': they don't support MCP tool calling due to grammar constraint limitations in openai-go library v1.8.2\n\nSupported providers:\n  - openai:gpt-4o, openai:gpt-4o-mini\n  - google:gemini-2.0-flash-exp\n  - ollama:llama3.2 (local, no API key needed)\n\nSee docs/CONFIG.md for configuration details")
	}

	// Execute query.
	resp, err := agent.Ask(ctx, question, conversationID, stream)
	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}

	// Save conversation ID for --continue.
	if err := saveConversationID(colonyID, conversationID); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save conversation ID: %v\n", err)
	}

	// Output response.
	if jsonOutput {
		return outputJSON(resp)
	}
	return outputTerminal(resp, stream)
}

// generateConversationID generates a new conversation ID.
func generateConversationID() string {
	// Generate random ID.
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback to simple ID if random fails.
		return fmt.Sprintf("conv-%d", os.Getpid())
	}
	return hex.EncodeToString(b)
}

// conversationMetadata stores metadata about a conversation.
type conversationMetadata struct {
	ID        string `json:"id"`
	ColonyID  string `json:"colony_id"`
	CreatedAt string `json:"created_at"`
}

// loadLastConversationID loads the last conversation ID for a colony.
func loadLastConversationID(colonyID string) (string, error) {
	path := getConversationMetadataPath(colonyID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no previous conversation found for colony %s (use without --continue to start a new conversation)", colonyID)
		}
		return "", fmt.Errorf("failed to read conversation metadata: %w", err)
	}

	var meta conversationMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return "", fmt.Errorf("failed to parse conversation metadata: %w", err)
	}

	return meta.ID, nil
}

// saveConversationID saves the conversation ID for future --continue use.
func saveConversationID(colonyID, conversationID string) error {
	path := getConversationMetadataPath(colonyID)

	// Ensure directory exists.
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create conversation directory: %w", err)
	}

	meta := conversationMetadata{
		ID:        conversationID,
		ColonyID:  colonyID,
		CreatedAt: fmt.Sprintf("%d", os.Getpid()), // Simple timestamp proxy for now
	}

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal conversation metadata: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write conversation metadata: %w", err)
	}

	return nil
}

// getConversationMetadataPath returns the path to the conversation metadata file.
func getConversationMetadataPath(colonyID string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".coral", "conversations", colonyID, "last.json")
}

// outputJSON outputs the response in JSON format.
func outputJSON(resp *askagent.Response) error {
	output := map[string]interface{}{
		"answer": resp.Answer,
		"tool_calls": func() []map[string]interface{} {
			calls := make([]map[string]interface{}, len(resp.ToolCalls))
			for i, call := range resp.ToolCalls {
				calls[i] = map[string]interface{}{
					"name":   call.Name,
					"input":  call.Input,
					"output": call.Output,
				}
			}
			return calls
		}(),
	}

	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	fmt.Println(string(data))
	return nil
}

// outputTerminal outputs the response to the terminal.
func outputTerminal(resp *askagent.Response, stream bool) error {
	fmt.Println(resp.Answer)

	// Show tool usage citations.
	if len(resp.ToolCalls) > 0 {
		fmt.Println("\n---")
		fmt.Println("\nSources:")
		for _, tool := range resp.ToolCalls {
			fmt.Printf("- %s\n", tool.Name)
		}
	}

	return nil
}
