package profile

import "github.com/spf13/cobra"

// NewProfileCmd creates the root profile command.
func NewProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Collect performance profiles on-demand",
		Long: `Collect performance profiles for running services.

This command group provides on-demand profiling capabilities:
- CPU profiling: Statistical sampling to identify hotspots
- Memory profiling: Allocation tracking and heap analysis

For historical profile queries, use 'coral query cpu-profile' or 'coral query memory-profile'.

Examples:
  coral profile cpu --service api --duration 30
  coral profile memory --service api --duration 30`,
	}

	// Add subcommands.
	cmd.AddCommand(NewCPUCmd())
	cmd.AddCommand(NewMemoryCmd())

	return cmd
}
