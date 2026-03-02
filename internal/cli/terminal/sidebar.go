package terminal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/cli/helpers"
)

// sidebarDataMsg carries the result of a periodic sidebar refresh.
type sidebarDataMsg struct {
	agents   []*colonyv1.Agent
	services []*colonyv1.ServiceSummary
	sessions []string // conversation IDs, newest first
	err      error
}

// selectSessionMsg is sent when the user presses Enter on a session in the sidebar.
type selectSessionMsg struct {
	conversationID string
}

var (
	sidebarStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderRight(true).
			BorderForeground(lipgloss.Color("237"))

	sidebarHeaderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("244")).
				Bold(true)

	sidebarItemStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	sidebarSelectedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("39")).
				Bold(true)

	sidebarDimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	svcDotHealthy  = lipgloss.NewStyle().Foreground(lipgloss.Color("40")).Render("●")
	svcDotWarning  = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Render("⚠")
	svcDotCritical = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("✗")
)

const refreshInterval = 30 * time.Second

// SidebarModel displays services, agent counts, and past sessions in the left pane.
type SidebarModel struct {
	colonyID string
	width    int
	height   int

	// Data (populated on refresh).
	agents   []*colonyv1.Agent
	services []*colonyv1.ServiceSummary
	sessions []string // conversation IDs, newest first

	// Navigation.
	selectedSession int // index into sessions

	lastErr error
}

// newSidebarModel creates a SidebarModel for the given colony.
func newSidebarModel(colonyID string) SidebarModel {
	return SidebarModel{
		colonyID: colonyID,
		width:    26,
		height:   24,
	}
}

// Init returns the initial sidebar tick command.
func (s SidebarModel) Init() tea.Cmd {
	return tea.Batch(
		sidebarRefreshCmd(s.colonyID),
		scheduleRefresh(),
	)
}

// scheduleRefresh schedules the next sidebar refresh after refreshInterval.
func scheduleRefresh() tea.Cmd {
	return tea.Tick(refreshInterval, func(_ time.Time) tea.Msg {
		return sidebarTickMsg{}
	})
}

// sidebarTickMsg triggers the next refresh.
type sidebarTickMsg struct{}

// sidebarRefreshCmd fetches agents, services, and sessions from disk.
func sidebarRefreshCmd(colonyID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()

		var agents []*colonyv1.Agent
		var services []*colonyv1.ServiceSummary

		client, _, err := helpers.GetColonyClientWithFallback(ctx, colonyID)
		if err == nil {
			if resp, err2 := client.ListAgents(ctx, connect.NewRequest(&colonyv1.ListAgentsRequest{})); err2 == nil {
				agents = resp.Msg.GetAgents()
			}
			if resp, err2 := client.ListServices(ctx, connect.NewRequest(&colonyv1.ListServicesRequest{})); err2 == nil {
				services = resp.Msg.GetServices()
			}
		}

		sessions := listSessions(colonyID)

		return sidebarDataMsg{agents: agents, services: services, sessions: sessions, err: err}
	}
}

// listSessions returns conversation IDs for the colony, newest first.
func listSessions(colonyID string) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(home, ".coral", "conversations", colonyID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() || e.Name() == "last.json" {
			continue
		}
		ids = append(ids, strings.TrimSuffix(e.Name(), ".json"))
	}
	// Sort newest first (IDs start with date).
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	return ids
}

// Update handles messages for the sidebar.
func (s SidebarModel) Update(msg tea.Msg) (SidebarModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height

	case sidebarTickMsg:
		return s, sidebarRefreshCmd(s.colonyID)

	case sidebarDataMsg:
		s.agents = msg.agents
		s.services = msg.services
		s.sessions = msg.sessions
		s.lastErr = msg.err
		// Clamp selection.
		if s.selectedSession >= len(s.sessions) && len(s.sessions) > 0 {
			s.selectedSession = len(s.sessions) - 1
		}
		return s, scheduleRefresh()

	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if s.selectedSession > 0 {
				s.selectedSession--
			}
		case "down", "j":
			if s.selectedSession < len(s.sessions)-1 {
				s.selectedSession++
			}
		case "enter":
			if len(s.sessions) > 0 {
				id := s.sessions[s.selectedSession]
				return s, func() tea.Msg { return selectSessionMsg{conversationID: id} }
			}
		}
	}

	return s, nil
}

// View renders the sidebar.
func (s SidebarModel) View() string {
	innerW := s.width - 2 // subtract border
	if innerW < 4 {
		innerW = 4
	}

	var b strings.Builder

	// Services section.
	b.WriteString(sidebarHeaderStyle.Render(pad(fmt.Sprintf("Services (%d)", len(s.services)), innerW)))
	b.WriteString("\n")
	b.WriteString(sidebarDimStyle.Render(strings.Repeat("─", innerW)))
	b.WriteString("\n")

	if len(s.services) == 0 {
		b.WriteString(sidebarDimStyle.Render(pad("─", innerW)))
		b.WriteString("\n")
	}
	for i, svc := range s.services {
		if i >= 5 {
			b.WriteString(sidebarDimStyle.Render(pad(fmt.Sprintf("  +%d more", len(s.services)-i), innerW)))
			b.WriteString("\n")
			break
		}
		dot := svcDotHealthy
		if svc.Status != nil {
			switch svc.GetStatus() {
			case colonyv1.ServiceStatus_SERVICE_STATUS_UNHEALTHY,
				colonyv1.ServiceStatus_SERVICE_STATUS_DISCONNECTED:
				dot = svcDotCritical
			case colonyv1.ServiceStatus_SERVICE_STATUS_OBSERVED_ONLY:
				dot = svcDotWarning
			}
		}
		label := truncSidebar(svc.GetName(), innerW-4)
		b.WriteString(sidebarItemStyle.Render(fmt.Sprintf(" %s %s", dot, pad(label, innerW-4))))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	// Agents section.
	b.WriteString(sidebarHeaderStyle.Render(pad(fmt.Sprintf("Agents (%d)", len(s.agents)), innerW)))
	b.WriteString("\n")
	b.WriteString(sidebarDimStyle.Render(strings.Repeat("─", innerW)))
	b.WriteString("\n")
	healthy, unhealthy := 0, 0
	for _, a := range s.agents {
		if a.GetStatus() == "unhealthy" {
			unhealthy++
		} else {
			healthy++
		}
	}
	if len(s.agents) == 0 {
		b.WriteString(sidebarDimStyle.Render(pad("no agents", innerW)))
	} else {
		b.WriteString(sidebarItemStyle.Render(pad(
			fmt.Sprintf(" ✓ %d  ✗ %d", healthy, unhealthy), innerW)))
	}
	b.WriteString("\n\n")

	// Sessions section.
	b.WriteString(sidebarHeaderStyle.Render(pad(fmt.Sprintf("Sessions (%d)", len(s.sessions)), innerW)))
	b.WriteString("\n")
	b.WriteString(sidebarDimStyle.Render(strings.Repeat("─", innerW)))
	b.WriteString("\n")
	if len(s.sessions) == 0 {
		b.WriteString(sidebarDimStyle.Render(pad("no sessions", innerW)))
		b.WriteString("\n")
	}
	for i, id := range s.sessions {
		if i >= 8 {
			break
		}
		label := truncSidebar(id, innerW-2)
		prefix := "  "
		style := sidebarItemStyle
		if i == s.selectedSession {
			prefix = "▶ "
			style = sidebarSelectedStyle
		}
		b.WriteString(style.Render(prefix + pad(label, innerW-2)))
		b.WriteString("\n")
	}

	return sidebarStyle.Width(s.width).Height(s.height).Render(b.String())
}

// pad truncates or right-pads s to exactly n runes.
func pad(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n])
	}
	return s + strings.Repeat(" ", n-len(runes))
}

// truncSidebar truncates s to n chars, appending … if needed.
func truncSidebar(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 1 {
		return string(runes[:n])
	}
	return string(runes[:n-1]) + "…"
}
