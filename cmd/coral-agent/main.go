// Package main provides the coral-agent server binary.
//
// This is a minimal binary containing only agent-specific commands,
// intended for server-side deployments where colony code is not needed.
// For development and testing, use the full `coral` binary instead.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/coral-mesh/coral/internal/cli/agent"
	"github.com/coral-mesh/coral/internal/cli/config"
	"github.com/coral-mesh/coral/internal/cli/duckdb"
	"github.com/coral-mesh/coral/internal/cli/tunhelper"
	"github.com/coral-mesh/coral/pkg/version"
)

func main() {
	rootCmd := &cobra.Command{
		Use:           "coral-agent",
		Short:         "Coral Agent - local observer for distributed debugging",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Register agent subcommands directly on root for a flat hierarchy
	// (e.g. "coral-agent start" instead of "coral-agent agent start").
	agent.RegisterCommands(rootCmd)

	// Ops command for querying the local agent database.
	rootCmd.AddCommand(duckdb.NewDuckDBCmd())

	rootCmd.AddCommand(config.NewConfigCmd())
	rootCmd.AddCommand(newVersionCmd())
	rootCmd.AddCommand(tunhelper.New())

	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Printf("Coral Agent version %s\n", version.Version)
			cmd.Printf("Git commit: %s\n", version.GitCommit)
			cmd.Printf("Build date: %s\n", version.BuildDate)
			cmd.Printf("Go version: %s\n", version.GoVersion)
		},
	}
}
