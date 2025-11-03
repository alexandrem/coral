package agent

import (
	"github.com/spf13/cobra"
)

// NewAgentCmd creates the agent command with subcommands.
func NewAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage and query Coral agents",
		Long: `Manage and query Coral agents.

Agents are local observers that monitor services and report to the colony.
Use these commands to check agent status, connect agents to services, and more.`,
	}

	// Add subcommands.
	cmd.AddCommand(NewConnectCmd())
	cmd.AddCommand(NewStatusCmd())

	return cmd
}
