// Package main provides the coral-colony server binary.
//
// This is a minimal binary containing only colony-specific commands,
// intended for server-side deployments where agent code is not needed.
// For development and testing, use the full `coral` binary instead.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/coral-mesh/coral/internal/cli/colony"
	"github.com/coral-mesh/coral/internal/cli/config"
	"github.com/coral-mesh/coral/internal/cli/debug"
	"github.com/coral-mesh/coral/internal/cli/duckdb"
	initcmd "github.com/coral-mesh/coral/internal/cli/init"
	"github.com/coral-mesh/coral/internal/cli/profile"
	"github.com/coral-mesh/coral/internal/cli/query"
	"github.com/coral-mesh/coral/internal/cli/tunhelper"
	"github.com/coral-mesh/coral/pkg/version"
)

func main() {
	rootCmd := &cobra.Command{
		Use:           "coral-colony",
		Short:         "Coral Colony - central coordinator for distributed debugging",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Register colony subcommands directly on root for a flat hierarchy
	// (e.g. "coral-colony start" instead of "coral-colony colony start").
	colony.RegisterCommands(rootCmd)

	// Ops commands for querying and debugging from the colony host.
	rootCmd.AddCommand(query.NewQueryCmd())
	rootCmd.AddCommand(duckdb.NewDuckDBCmd())
	rootCmd.AddCommand(debug.NewDebugCmd())
	rootCmd.AddCommand(profile.NewProfileCmd())

	rootCmd.AddCommand(initcmd.NewInitCmd())
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
			cmd.Printf("Coral Colony version %s\n", version.Version)
			cmd.Printf("Git commit: %s\n", version.GitCommit)
			cmd.Printf("Build date: %s\n", version.BuildDate)
			cmd.Printf("Go version: %s\n", version.GoVersion)
		},
	}
}
