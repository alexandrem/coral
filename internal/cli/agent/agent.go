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

	// Add subcommands.
	cmd.AddCommand(NewStartCmd())
	cmd.AddCommand(NewStatusCmd())

	return cmd
}
