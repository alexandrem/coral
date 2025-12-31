package agent

import (
	"github.com/coral-mesh/coral/internal/cli/agent/startup"
	"github.com/spf13/cobra"
)

// NewStartCmd creates the start command for agents.
// This is a re-export from the startup package for backwards compatibility.
func NewStartCmd() *cobra.Command {
	return startup.NewStartCmd()
}
