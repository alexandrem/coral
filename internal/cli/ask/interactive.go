package ask

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/coral-mesh/coral/internal/cli/ask/ui"
	"github.com/coral-mesh/coral/internal/config"
)

// agentAdapter adapts *Agent to ui.Agent interface.
type agentAdapter struct {
	agent *Agent
}

func (a *agentAdapter) AskWithChannel(ctx any, question, conversationID string, dryRun bool, ch chan<- any) (any, error) {
	// Create a typed channel for AgentEvent.
	eventChan := make(chan AgentEvent, 100)

	// Forward events from typed channel to any channel in goroutine.
	go func() {
		defer close(ch)
		for event := range eventChan {
			ch <- ui.AgentEvent{
				Type:     event.Type,
				Content:  event.Content,
				ToolName: event.ToolName,
				Duration: event.Duration,
				Error:    event.Error,
				Response: event.Response,
			}
		}
	}()

	// Call the real agent with the typed channel.
	return a.agent.AskWithChannel(ctx.(context.Context), question, conversationID, dryRun, eventChan)
}

// runInteractive starts the interactive Bubbletea session (RFD 051).
func runInteractive(
	ctx context.Context,
	colonyID string,
	modelOverride string,
	resumeArg string,
	debug bool,
	dryRun bool,
) error {
	// Load configuration (same as runAsk).
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
	agent, err := NewAgent(askCfg, colonyCfg, debug)
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}
	defer func() {
		if err := agent.Close(); err != nil && debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Failed to close agent: %v\n", err)
		}
	}()

	// Resolve conversation ID.
	var conversationID string
	var history []Message

	if resumeArg != "" {
		// Resume existing conversation.
		conversationID, err = resolveConversationID(colonyID, resumeArg)
		if err != nil {
			return fmt.Errorf("failed to resolve conversation: %w", err)
		}

		// Load history.
		history, err = loadConversationHistory(colonyID, conversationID)
		if err != nil {
			// Warn but continue with empty history.
			fmt.Fprintf(os.Stderr, "Warning: failed to load conversation history: %v\n", err)
			history = nil
		}

		// Set agent history.
		if history != nil {
			agent.SetConversationHistory(conversationID, history)
		}

		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Resumed conversation: %s (%d messages)\n", conversationID, len(history))
		}
	} else {
		// Create new conversation.
		conversationID = generateConversationID()
		history = nil

		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Started new conversation: %s\n", conversationID)
		}
	}

	// Extract model name for display.
	_, modelID := parseModelString(askCfg.DefaultModel)
	if modelID == "" {
		modelID = askCfg.DefaultModel
	}

	// Convert history to UI message format.
	var uiHistory []ui.Message
	for _, msg := range history {
		uiHistory = append(uiHistory, ui.Message{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	// Create save callback that converts UI messages back to ask messages.
	saveFunc := func(colonyID, conversationID string, uiMsgs []ui.Message) error {
		var askMsgs []Message
		for _, msg := range uiMsgs {
			askMsgs = append(askMsgs, Message{
				Role:    msg.Role,
				Content: msg.Content,
			})
		}
		return SaveConversationHistory(colonyID, conversationID, askMsgs)
	}

	// Wrap agent with adapter for UI interface.
	adapter := &agentAdapter{agent: agent}

	// Create Bubbletea model.
	model, err := ui.NewModel(
		adapter,
		conversationID,
		colonyID,
		modelID,
		uiHistory,
		debug,
		dryRun,
		saveFunc,
	)
	if err != nil {
		return fmt.Errorf("failed to create UI model: %w", err)
	}

	// Run Bubbletea program.
	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("interactive session failed: %w", err)
	}

	return nil
}
