// Package terminal - test exports (compiled only during testing).
package terminal

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/coral-mesh/coral/internal/cli/ask/ui"
)

// NewTerminalModelForTest creates a minimal TerminalModel for unit tests
// without requiring a real ask.Agent or active HTTP server.
func NewTerminalModelForTest(colonyID string, askModel ui.Model, srv *Server) tea.Model {
	return TerminalModel{
		header:   newHeaderModel(colonyID, "test-model"),
		sidebar:  newSidebarModel(colonyID),
		main:     askModel,
		focus:    focusMain,
		colonyID: colonyID,
		server:   srv,
		width:    120,
		height:   40,
	}
}

// GetFocusForTest returns the current focus area as an int for assertions.
func GetFocusForTest(model tea.Model) int {
	if tm, ok := model.(TerminalModel); ok {
		return int(tm.focus)
	}
	return -1
}
