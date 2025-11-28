// Package ask implements the "coral ask" CLI command for AI-powered application analysis.
//
// The ask command allows users to query their application's state using natural language.
// An LLM agent runs locally and connects to the colony's MCP server to access observability
// data including metrics, traces, logs, and other telemetry. The agent supports multi-turn
// conversations, multiple AI providers (OpenAI, Google, Ollama), and both streaming and
// JSON output modes.
//
// See RFD 030 for the complete design and implementation details.
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

	askagent "github.com/coral-mesh/coral/internal/agent/ask"
	"github.com/coral-mesh/coral/internal/config"
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
		dryRun   bool // --dry-run flag to inspect payload without sending to LLM
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
			return runAsk(cmd.Context(), question, colonyID, model, stream, jsonFlag, cont, debug, dryRun)
		},
	}

	cmd.Flags().StringVarP(&colonyID, "colony-id", "c", "", "Colony ID to query (defaults to current colony)")
	cmd.Flags().StringVar(&model, "model", "", "Override model for this query (e.g., openai:gpt-4o-mini)")
	cmd.Flags().BoolVar(&stream, "stream", true, "Stream output progressively (default: true)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "Output JSON format for scripting")
	cmd.Flags().BoolVar(&cont, "continue", false, "Continue previous conversation")
	cmd.Flags().BoolVar(&debug, "debug", false, "Enable debug logging to stderr")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show LLM payload and prompt before sending (saves tokens)")

	return cmd
}

// runAsk executes the ask command.
func runAsk(ctx context.Context, question, colonyID, modelOverride string, stream, jsonOutput, continueConv, debug, dryRun bool) error {
	// Configure debug logger.
	if debug {
		fmt.Fprintln(os.Stderr, "[DEBUG] Debug mode enabled")
	}
	if dryRun {
		fmt.Fprintln(os.Stderr, "[DRY-RUN] Dry-run mode enabled - will show payload and prompt before sending")
		debug = true // Enable debug mode for dry-run
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

	// Validate model provider is implemented.
	// Currently only Google is supported, other providers are planned.
	providerName := strings.SplitN(askCfg.DefaultModel, ":", 2)[0]
	if providerName != "google" {
		return fmt.Errorf("provider %q is not yet implemented\n\nCurrently supported:\n  - google:gemini-2.0-flash-exp (fast, experimental)\n  - google:gemini-1.5-pro (high quality, stable)\n  - google:gemini-1.5-flash (balanced)\n\nPlanned providers:\n  - openai (gpt-4o, gpt-4o-mini)\n  - anthropic (claude-3-5-sonnet)\n  - ollama (local models)\n\nSee docs/PROVIDERS.md for implementation status", providerName)
	}

	// Execute query.
	resp, err := agent.Ask(ctx, question, conversationID, stream, dryRun)
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
