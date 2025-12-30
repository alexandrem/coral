package query

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewMemoryProfileCmd creates the memory-profile query command.
func NewMemoryProfileCmd() *cobra.Command {
	var (
		serviceName string
		since       string
		until       string
		buildID     string
		showGrowth  bool
		showTypes   bool
	)

	cmd := &cobra.Command{
		Use:   "memory-profile",
		Short: "Query historical memory profiles",
		Long: `Query historical memory profiles from continuous profiling.

This command retrieves aggregated memory allocation profiles collected by
the continuous profiling system (RFD 077).

For on-demand memory profiling, use 'coral profile memory --duration 30'.

Examples:
  coral query memory-profile --service api --since 1h
  coral query memory-profile --service api --since 1h --show-growth --show-types
  coral query memory-profile --service api --build-id abc123 --since 24h

Note: Memory profiling is planned for RFD 077 and not yet implemented.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if serviceName == "" {
				return fmt.Errorf("--service is required")
			}

			// TODO: Implement historical memory profiling query (RFD 077).
			return fmt.Errorf("historical memory profiling not yet implemented (RFD 077)")
		},
	}

	cmd.Flags().StringVarP(&serviceName, "service", "s", "", "Service name (required)")
	cmd.Flags().StringVar(&since, "since", "1h", "Query from this time ago")
	cmd.Flags().StringVar(&until, "until", "", "Query until this time ago")
	cmd.Flags().StringVar(&buildID, "build-id", "", "Filter by specific build ID")
	cmd.Flags().BoolVar(&showGrowth, "show-growth", false, "Show heap growth trends")
	cmd.Flags().BoolVar(&showTypes, "show-types", false, "Show allocation breakdown by type")

	cmd.MarkFlagRequired("service") //nolint:errcheck

	return cmd
}
