package ui

import (
	"os"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
)

// state represents the current state of the interactive session.
type state int

const (
	stateIdle state = iota
	stateQuerying
	stateStreaming
	stateError
)

// Agent interface to avoid import cycle.
type Agent interface {
	AskWithChannel(ctx any, question, conversationID string, dryRun bool, ch chan<- any) (any, error)
}

// Message represents a conversation message (to avoid import cycle).
type Message struct {
	Role    string
	Content string
}

// Model represents the Bubbletea model for interactive mode (RFD 051).
type Model struct {
	// Configuration
	agent          Agent
	conversationID string
	colonyID       string
	modelName      string
	debug          bool
	dryRun         bool

	// UI state
	currentState state
	input        textinput.Model
	spinner      spinner.Model

	// Conversation state
	conversation []Message

	// Streaming state
	streamBuffer string
	currentTool  string

	// Error state
	lastError error

	// Rendering
	renderer *glamour.TermRenderer
	width    int
	height   int

	// Flags
	quitting bool

	// Callback functions (to avoid import cycle)
	saveConversation func(colonyID, conversationID string, messages []Message) error
}

// NewModel creates a new interactive model.
func NewModel(
	agent Agent,
	conversationID string,
	colonyID string,
	modelName string,
	initialHistory []Message,
	debug bool,
	dryRun bool,
	saveFunc func(string, string, []Message) error,
) (Model, error) {
	// Create text input for user questions.
	ti := textinput.New()
	ti.Placeholder = "Ask a question..."
	ti.Focus()
	ti.CharLimit = 500
	ti.Width = 80

	// Create spinner for tool execution.
	s := spinner.New()
	s.Spinner = spinner.Dot

	// Create markdown renderer with NO_COLOR support.
	rendererOpts := []glamour.TermRendererOption{glamour.WithWordWrap(80)}
	if os.Getenv("NO_COLOR") != "" {
		rendererOpts = append(rendererOpts, glamour.WithStylePath("notty"))
	} else {
		rendererOpts = append(rendererOpts, glamour.WithAutoStyle())
	}

	renderer, err := glamour.NewTermRenderer(rendererOpts...)
	if err != nil {
		return Model{}, err
	}

	return Model{
		agent:            agent,
		conversationID:   conversationID,
		colonyID:         colonyID,
		modelName:        modelName,
		debug:            debug,
		dryRun:           dryRun,
		currentState:     stateIdle,
		input:            ti,
		spinner:          s,
		conversation:     initialHistory,
		streamBuffer:     "",
		renderer:         renderer,
		width:            80,
		height:           24,
		quitting:         false,
		saveConversation: saveFunc,
	}, nil
}

// Init initializes the model (Bubbletea interface).
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		m.spinner.Tick,
	)
}
