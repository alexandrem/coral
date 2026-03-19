package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/coral-mesh/coral/internal/cli/ask"
	"github.com/coral-mesh/coral/internal/cli/ask/ui"
	"github.com/coral-mesh/coral/internal/config"
)

// NewTerminalCmd creates the coral terminal command (RFD 094).
func NewTerminalCmd() *cobra.Command {
	var (
		colonyID    string
		modelName   string
		debug       bool
		autoBrowser bool
	)

	cmd := &cobra.Command{
		Use:   "terminal",
		Short: "Launch the mission-control TUI",
		Long: `Launch coral terminal — a rich multi-pane TUI for Coral sessions.

Provides a live sidebar (services, agent health, past sessions), an embedded
conversation pane powered by coral ask, and a browser dashboard for rich
skill visualisations (latency heatmaps, tables, timeseries charts).

Layout:
  Header  — colony ID · agent counts · service counts
  Sidebar — services · agents · past sessions
  Main    — AI conversation (same as coral ask)
  Footer  — keybinding hints

The browser dashboard is served at http://localhost:<ephemeral-port>.
Open it with the /browser inline command or --auto-browser.

Examples:
  coral terminal
  coral terminal --auto-browser
  coral terminal --colony my-colony`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTerminal(cmd, colonyID, modelName, debug, autoBrowser)
		},
	}

	cmd.Flags().StringVar(&colonyID, "colony", "", "Colony ID to connect to (default: from config)")
	cmd.Flags().StringVar(&modelName, "model", "", "Override LLM model (e.g. anthropic:claude-sonnet-4-6)")
	cmd.Flags().BoolVar(&debug, "debug", false, "Enable debug logging to stderr")
	cmd.Flags().BoolVar(&autoBrowser, "auto-browser", false, "Automatically open the browser dashboard on first render event")

	return cmd
}

// runTerminal sets up the terminal session and launches the bubbletea program.
func runTerminal(cmd *cobra.Command, colonyID, modelOverride string, debug, autoBrowser bool) error {
	// Load configuration (mirrors ask.runInteractive).
	loader, err := config.NewLoader()
	if err != nil {
		return fmt.Errorf("failed to create config loader: %w", err)
	}

	globalCfg, err := loader.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("failed to load global config: %w", err)
	}

	if colonyID == "" {
		colonyID = globalCfg.DefaultColony
		if colonyID == "" {
			return fmt.Errorf("no colony specified and no default colony configured")
		}
	}

	colonyCfg, err := loader.LoadColonyConfig(colonyID)
	if err != nil {
		return fmt.Errorf("failed to load colony config: %w", err)
	}

	askCfg, err := config.ResolveAskConfig(globalCfg, colonyCfg)
	if err != nil {
		return fmt.Errorf("failed to resolve ask config: %w", err)
	}

	if modelOverride != "" {
		askCfg.DefaultModel = modelOverride
	}

	if err := config.ValidateAskConfig(askCfg); err != nil {
		return fmt.Errorf("invalid ask config: %w", err)
	}

	// Extract display model name.
	displayModel := askCfg.DefaultModel
	if parts := strings.SplitN(displayModel, ":", 2); len(parts) == 2 {
		displayModel = parts[1]
	}

	// CLI dispatch mode: the terminal uses coral subprocesses instead of MCP (RFD 100).
	askCfg.Agent.DispatchMode = config.DispatchModeCLI

	// Generate CLI reference from the Cobra command tree for the system prompt.
	cliRef := ask.GenerateCLIReference(cmd.Root())

	// Create the LLM agent.
	agent, err := ask.NewAgentWithCLIReference(askCfg, colonyCfg, debug, cliRef)
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}
	defer func() {
		if err := agent.Close(); err != nil && debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] failed to close agent: %v\n", err)
		}
	}()

	// Start the embedded HTTP server (RFD 094).
	srv, err := StartServer()
	if err != nil {
		// Non-fatal — terminal works without the browser dashboard.
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] failed to start dashboard server: %v\n", err)
		}
		srv = nil
	}
	if srv != nil {
		defer srv.Stop()
	}

	// Build the ask/ui.Model that powers the main conversation pane.
	conversationID := ask.GenerateConversationID()
	saveFunc := func(cID, convID string, msgs []ui.Message) error {
		askMsgs := make([]ask.Message, 0, len(msgs))
		for _, m := range msgs {
			askMsgs = append(askMsgs, ask.Message{Role: m.Role, Content: m.Content})
		}
		return ask.SaveConversationHistory(cID, convID, askMsgs)
	}

	adapter := ask.NewUIAdapter(agent)

	askModel, err := ui.NewModel(adapter, conversationID, colonyID, displayModel, nil, debug, false, saveFunc)
	if err != nil {
		return fmt.Errorf("failed to create conversation model: %w", err)
	}

	// Register /browser handler so the user can type it in the conversation input.
	if srv != nil {
		dashboardURL := fmt.Sprintf("http://localhost:%d?port=%d", srv.Port(), srv.Port())
		askModel.SetCommandHandler(func(c string) tea.Cmd {
			if strings.TrimSpace(c) == "/browser" {
				return openBrowserCmd(dashboardURL)
			}
			return nil
		})
	}

	// Build and run the root terminal model.
	model := newTerminalModel(colonyID, agent, askModel, srv, displayModel)

	// If --auto-browser is set, open the browser when the first render event arrives.
	// We handle this by opening it at startup if the flag is set (simplest path).
	if autoBrowser && srv != nil {
		dashboardURL := fmt.Sprintf("http://localhost:%d?port=%d", srv.Port(), srv.Port())
		if err := openURL(dashboardURL); err != nil && debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] failed to open browser: %v\n", err)
		}
	}

	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("terminal session failed: %w", err)
	}

	return nil
}

// openURL opens url in the user's default browser using the platform command.
func openURL(url string) error {
	var cmd string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "cmd"
	default:
		cmd = "xdg-open"
	}
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.Command(cmd, "/c", "start", url) // #nosec G204
	} else {
		c = exec.Command(cmd, url) // #nosec G204
	}
	return c.Start()
}
