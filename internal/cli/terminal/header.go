package terminal

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
)

var (
	headerStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("235")).
			Foreground(lipgloss.Color("252")).
			Bold(true)

	dotHealthy  = lipgloss.NewStyle().Foreground(lipgloss.Color("40")).Render("●")
	dotWarning  = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Render("⚠")
	dotCritical = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("✗")
)

// HeaderModel renders the top bar showing colony state at a glance.
type HeaderModel struct {
	colonyID  string
	modelName string
	width     int

	// Live data from the sidebar refresh.
	agents   []*colonyv1.Agent
	services []*colonyv1.ServiceSummary
}

// newHeaderModel creates a HeaderModel with the given colony and model name.
func newHeaderModel(colonyID, modelName string) HeaderModel {
	return HeaderModel{colonyID: colonyID, modelName: modelName, width: 80}
}

// Update refreshes live data from a sidebarDataMsg.
func (h HeaderModel) Update(msg sidebarDataMsg) HeaderModel {
	h.agents = msg.agents
	h.services = msg.services
	return h
}

// View renders the header line.
func (h HeaderModel) View() string {
	dot := dotHealthy
	healthy, unhealthy := 0, 0
	for _, a := range h.agents {
		if a.GetStatus() == "unhealthy" {
			unhealthy++
		} else {
			healthy++
		}
	}
	if unhealthy > 0 && healthy == 0 {
		dot = dotCritical
	} else if unhealthy > 0 {
		dot = dotWarning
	}

	agentSummary := fmt.Sprintf("%d agents", len(h.agents))
	if len(h.agents) > 0 {
		agentSummary = fmt.Sprintf("%d agents (%d✓  %d✗)", len(h.agents), healthy, unhealthy)
	}

	svcSummary := fmt.Sprintf("%d services", len(h.services))

	text := fmt.Sprintf(" %s %s  ·  %s  ·  %s  ·  %s ",
		dot, h.colonyID, agentSummary, svcSummary, h.modelName)

	// Pad to full width.
	if h.width > 0 {
		runes := []rune(text)
		if len(runes) < h.width {
			text = text + strings.Repeat(" ", h.width-len(runes))
		}
	}

	return headerStyle.Width(h.width).Render(text)
}
