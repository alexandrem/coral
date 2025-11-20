package cli

import (
	"github.com/spf13/cobra"

	"github.com/coral-io/coral/internal/cli/agent"
	"github.com/coral-io/coral/internal/cli/ask"
	"github.com/coral-io/coral/internal/cli/colony"
	"github.com/coral-io/coral/internal/cli/duckdb"
	initcmd "github.com/coral-io/coral/internal/cli/init"
	"github.com/coral-io/coral/internal/cli/proxy"
	"github.com/coral-io/coral/internal/cli/tun_helper"
	"github.com/coral-io/coral/pkg/version"
)

var rootCmd = &cobra.Command{
	Use:   "coral",
	Short: "Coral - LLM-orchestrated debugging for distributed apps",
	Long: `Turn fragmented infrastructure into one intelligent system.
Debug across laptop, VMs, K8s, and clouds with natural language queries.

Three-tier architecture:
- Colony: Central coordinator with MCP server for universal AI integration
- Agents: Local observers with zero-config eBPF instrumentation
- CLI: Developer interface (coral ask, coral connect, etc.)

Key capabilities:
- Natural language debugging: Ask questions, get root cause analysis
- Universal AI: Works with Claude Desktop, IDEs, any MCP client
- Zero-config observability: eBPF metrics without code changes
- Live debugging: On-demand instrumentation across your mesh
- Your LLM: Use OpenAI/Anthropic/Ollama - you control the AI`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	// Add subcommands
	rootCmd.AddCommand(initcmd.NewInitCmd())
	rootCmd.AddCommand(newStatusCmd())
	rootCmd.AddCommand(colony.NewColonyCmd())
	rootCmd.AddCommand(agent.NewAgentCmd())
	rootCmd.AddCommand(agent.NewConnectCmd())
	rootCmd.AddCommand(ask.NewAskCmd())
	rootCmd.AddCommand(proxy.Command())
	rootCmd.AddCommand(agent.NewShellCmd())
	rootCmd.AddCommand(duckdb.NewDuckDBCmd())
	rootCmd.AddCommand(newVersionCmd())

	// Add internal commands (hidden from help)
	rootCmd.AddCommand(tun_helper.New())
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
