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
	stateScriptReview  // waiting for user to approve or reject a script write
	stateHistorySearch // reverse incremental search through input history (Ctrl+R)
)

// Agent interface to avoid import cycle.
type Agent interface {
	AskWithChannel(ctx any, question, conversationID string, dryRun bool, ch chan<- any) (any, error)
	ResetConversation(conversationID string)
	// SwitchModel replaces the active LLM provider for subsequent turns and
	// returns the display model name. Backs the /model inline command.
	SwitchModel(modelSpec string) (string, error)
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
	streamBuffer   string
	currentTool    string
	currentCommand string // CLI command string for the active tool (RFD 100)

	// Active query channel — nil when no query is in flight.
	// Created in handleKeyMsg and consumed by waitForEventCmd.
	eventChan chan any

	// Script review state — set when a script_review event is received.
	reviewEventChan chan any // eventChan saved during review; restored after
	reviewName      string
	reviewContent   string
	reviewReply     chan bool

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

	// commandHandler is called for inline commands not handled by the model itself.
	// Set by coral terminal to handle /browser and other terminal-level commands.
	commandHandler func(cmd string) tea.Cmd

	// Input history (persistent Up/Down navigation and Ctrl+R search).
	history       []string
	historyIdx    int    // -1 = not navigating
	historyDraft  string // input saved before navigating into history
	appendHistory func(entry string)

	// History search state (Ctrl+R reverse incremental search).
	searchQuery    string
	searchMatchIdx int // index into history of the current match, -1 = none
}

// SetHistory configures persistent input history for Up/Down navigation and
// Ctrl+R search. entries are ordered oldest-first. appendFn is called with
// each submitted line for persistence; it may be nil to keep in-session
// navigation without persisting to disk.
func (m *Model) SetHistory(entries []string, appendFn func(string)) {
	m.history = append([]string(nil), entries...)
	m.historyIdx = -1
	m.appendHistory = appendFn
}

// SetInputValue pre-fills the input box with text without submitting the
// query, so the user can review or edit before pressing Enter. Used by
// coral terminal to populate a follow-up question generated from a browser
// dashboard click.
func (m Model) SetInputValue(v string) Model {
	m.input.SetValue(v)
	m.input.CursorEnd()
	return m
}

// SetCommandHandler sets a handler for unrecognised inline commands (e.g. /browser).
// When set, unrecognised commands are forwarded to fn instead of showing an error.
func (m *Model) SetCommandHandler(fn func(cmd string) tea.Cmd) {
	m.commandHandler = fn
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
		historyIdx:       -1,
		searchMatchIdx:   -1,
	}, nil
}

// Init initializes the model (Bubbletea interface).
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		m.spinner.Tick,
	)
}
