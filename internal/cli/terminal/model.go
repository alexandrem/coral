package terminal

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/coral-mesh/coral/internal/cli/ask"
	"github.com/coral-mesh/coral/internal/cli/ask/ui"
)

// focusArea tracks which pane currently has keyboard focus.
type focusArea int

const (
	focusMain    focusArea = iota // main pane (ask/ui.Model)
	focusSidebar                  // left sidebar
)

const (
	sidebarWidth = 26
	headerHeight = 1
	footerHeight = 1
)

var (
	footerStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("235")).
			Foreground(lipgloss.Color("240"))

	focusIndicatorMain    = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Render("[main]")
	focusIndicatorSidebar = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Render("[sidebar]")
)

// TerminalModel is the root bubbletea model for coral terminal (RFD 094).
// It composes a header, sidebar, main pane, and footer.
type TerminalModel struct {
	header  HeaderModel
	sidebar SidebarModel
	main    ui.Model
	focus   focusArea

	width  int
	height int

	// Used to load conversation history when a session is selected.
	colonyID string
	agent    *ask.Agent
	server   *Server
}

// newTerminalModel constructs the root model.
func newTerminalModel(
	colonyID string,
	agent *ask.Agent,
	askModel ui.Model,
	server *Server,
	modelName string,
) TerminalModel {
	return TerminalModel{
		header:   newHeaderModel(colonyID, modelName),
		sidebar:  newSidebarModel(colonyID),
		main:     askModel,
		colonyID: colonyID,
		agent:    agent,
		server:   server,
	}
}

// Init initialises all sub-models.
func (m TerminalModel) Init() tea.Cmd {
	return tea.Batch(
		m.main.Init(),
		m.sidebar.Init(),
	)
}

// Update handles messages and routes them to sub-models.
func (m TerminalModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.propagateSize()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+d":
			return m, tea.Quit

		case "tab":
			// Toggle focus between main and sidebar.
			if m.focus == focusMain {
				m.focus = focusSidebar
			} else {
				m.focus = focusMain
			}
			return m, nil
		}

		// Forward key to the focused pane.
		if m.focus == focusSidebar {
			newSidebar, cmd := m.sidebar.Update(msg)
			m.sidebar = newSidebar
			cmds = append(cmds, cmd)
		} else {
			newMain, cmd := m.main.Update(msg)
			m.main = newMain.(ui.Model)
			cmds = append(cmds, cmd)
		}

	case sidebarDataMsg:
		// Update sidebar and header with fresh colony data.
		newSidebar, cmd := m.sidebar.Update(msg)
		m.sidebar = newSidebar
		cmds = append(cmds, cmd)
		m.header = m.header.Update(msg)

	case sidebarTickMsg:
		newSidebar, cmd := m.sidebar.Update(msg)
		m.sidebar = newSidebar
		cmds = append(cmds, cmd)

	case selectSessionMsg:
		// Load history asynchronously, then forward LoadConversationMsg to main.
		cmds = append(cmds, m.loadSessionCmd(msg.conversationID))

	case ui.LoadConversationMsg:
		// Forward pre-loaded conversation to the main pane.
		newMain, cmd := m.main.Update(msg)
		m.main = newMain.(ui.Model)
		cmds = append(cmds, cmd)

	default:
		// Forward everything else to the main model (spinner ticks, stream chunks, etc.).
		newMain, cmd := m.main.Update(msg)
		m.main = newMain.(ui.Model)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// View renders the full terminal layout.
func (m TerminalModel) View() string {
	w := m.width
	if w < 40 {
		w = 40
	}
	bodyH := m.height - headerHeight - footerHeight
	if bodyH < 1 {
		bodyH = 1
	}
	mainW := w - sidebarWidth
	if mainW < 20 {
		mainW = 20
	}

	m.header.width = w
	headerView := m.header.View()

	sidebarView := lipgloss.NewStyle().
		Width(sidebarWidth).
		Height(bodyH).
		Render(m.sidebar.View())

	mainView := lipgloss.NewStyle().
		Width(mainW).
		Height(bodyH).
		Render(m.main.View())

	body := lipgloss.JoinHorizontal(lipgloss.Top, sidebarView, mainView)

	footerView := m.renderFooter(w)

	return lipgloss.JoinVertical(lipgloss.Left, headerView, body, footerView)
}

// renderFooter renders the keybinding hint bar.
func (m TerminalModel) renderFooter(w int) string {
	focus := focusIndicatorMain
	if m.focus == focusSidebar {
		focus = focusIndicatorSidebar
	}
	srv := ""
	if m.server != nil {
		srv = fmt.Sprintf("  [/browser] http://localhost:%d", m.server.Port())
	}
	hints := fmt.Sprintf(" [Tab] switch pane%s  [Ctrl+D] quit  %s", srv, focus)

	runes := []rune(hints)
	if w > 0 && len(runes) < w {
		hints = hints + strings.Repeat(" ", w-len(runes))
	}

	return footerStyle.Width(w).Render(hints)
}

// propagateSize sends constrained WindowSizeMsg to sub-models.
func (m TerminalModel) propagateSize() TerminalModel {
	bodyH := m.height - headerHeight - footerHeight
	if bodyH < 1 {
		bodyH = 1
	}
	mainW := m.width - sidebarWidth
	if mainW < 20 {
		mainW = 20
	}

	newMain, _ := m.main.Update(tea.WindowSizeMsg{Width: mainW, Height: bodyH})
	m.main = newMain.(ui.Model)

	newSidebar, _ := m.sidebar.Update(tea.WindowSizeMsg{Width: sidebarWidth, Height: bodyH})
	m.sidebar = newSidebar

	m.header.width = m.width

	return m
}

// loadSessionCmd loads conversation history from disk and updates the agent,
// then sends a LoadConversationMsg to the main pane.
func (m TerminalModel) loadSessionCmd(conversationID string) tea.Cmd {
	colonyID := m.colonyID
	agent := m.agent
	return func() tea.Msg {
		history, err := ask.LoadConversationHistory(colonyID, conversationID)
		if err != nil {
			// Return a no-op; the conversation display stays unchanged.
			return nil
		}

		// Update the agent's in-memory conversation history.
		if agent != nil {
			agent.SetConversationHistory(conversationID, history)
		}

		// Convert ask.Message → ui.Message (strip ToolCalls, ToolResponses).
		uiHistory := make([]ui.Message, 0, len(history))
		for _, msg := range history {
			uiHistory = append(uiHistory, ui.Message{
				Role:    msg.Role,
				Content: msg.Content,
			})
		}

		return ui.LoadConversationMsg{
			ConversationID: conversationID,
			History:        uiHistory,
		}
	}
}

// openBrowserCmd returns a tea.Cmd that opens the dashboard URL in the default browser.
func openBrowserCmd(url string) tea.Cmd {
	return func() tea.Msg {
		_ = openURL(url) // best-effort; ignores errors.
		return nil
	}
}

// contextKey is unexported to avoid collisions in context values.
type contextKey struct{}

// withColonyID attaches a colony ID to a context (used internally).
func withColonyID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// colonyIDFromCtx retrieves the colony ID from a context.
func colonyIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(contextKey{}).(string)
	return v
}
