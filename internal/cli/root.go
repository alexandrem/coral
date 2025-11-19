package cli

import (
	"github.com/spf13/cobra"

	"github.com/coral-io/coral/internal/cli/agent"
	"github.com/coral-io/coral/internal/cli/ask"
	"github.com/coral-io/coral/internal/cli/colony"
	initcmd "github.com/coral-io/coral/internal/cli/init"
	"github.com/coral-io/coral/internal/cli/proxy"
	"github.com/coral-io/coral/internal/cli/tun_helper"
	"github.com/coral-io/coral/pkg/version"
)

var rootCmd = &cobra.Command{
	Use:   "coral",
	Short: "Coral - Unified Operations for Distributed Apps",
	Long: `Coral is an application intelligence mesh for debugging distributed apps
across fragmented infrastructure (laptop, VMs, K8s, clouds).

Three-tier architecture:
- Colony: Control plane coordinator and MCP gateway for AI integration
- Agents: Local observers with zero-config eBPF instrumentation
- CLI: Developer interface with AI assistant and service management

Key capabilities:
- WireGuard mesh unifies fragmented infrastructure
- Zero-config observability via eBPF (no code changes)
- On-demand live debugging and instrumentation
- AI-powered insights using your LLM (OpenAI/Anthropic/Ollama)`,
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
