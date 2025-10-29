package cli

import (
	"github.com/spf13/cobra"

	"github.com/coral-io/coral/internal/cli/agent"
	"github.com/coral-io/coral/internal/cli/ask"
	"github.com/coral-io/coral/internal/cli/colony"
	"github.com/coral-io/coral/pkg/version"
)

var rootCmd = &cobra.Command{
	Use:   "coral",
	Short: "Coral - Agentic AI for Distributed Systems",
	Long: `Coral is an AI assistant that observes, analyzes, and recommends actions
for distributed systems operations.

Coral provides application-scoped intelligence through:
- Colony: Central brain for AI analysis and historical patterns
- Agents: Local observers for processes and health monitoring
- Ask: Interactive AI queries about your system`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	// Add subcommands
	rootCmd.AddCommand(colony.NewColonyCmd())
	rootCmd.AddCommand(agent.NewConnectCmd())
	rootCmd.AddCommand(ask.NewAskCmd())
	rootCmd.AddCommand(newVersionCmd())
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Printf("Coral version %s\n", version.Version)
			cmd.Printf("Git commit: %s\n", version.GitCommit)
			cmd.Printf("Build date: %s\n", version.BuildDate)
			cmd.Printf("Go version: %s\n", version.GoVersion)
		},
	}
}

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}
