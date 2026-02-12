// Package agent provides CLI commands for agent management.
package agent

import (
	"github.com/spf13/cobra"
)

// NewAgentCmd creates the agent command with subcommands.
func NewAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage Coral agents",
		Long: `Manage Coral agents.

Agents are local observers that monitor services and report to the colony.
Use these commands to start, stop, and check agent status.`,
	}

	addAgentCommands(cmd)

	return cmd
}

// RegisterCommands adds all agent subcommands directly to the given parent
// command. This is used by the coral-agent server binary to flatten the
// command hierarchy (e.g. "coral-agent start" instead of "coral-agent agent start").
func RegisterCommands(parent *cobra.Command) {
	addAgentCommands(parent)
}

func addAgentCommands(cmd *cobra.Command) {
	cmd.AddCommand(NewStartCmd())
	cmd.AddCommand(NewStatusCmd())
	cmd.AddCommand(NewBootstrapCmd()) // RFD 048
	cmd.AddCommand(NewCertCmd())      // RFD 048
}
