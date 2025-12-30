package profile

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewMemoryCmd creates the memory profiling command.
func NewMemoryCmd() *cobra.Command {
	var (
		serviceName string
		duration    int32
		sampleRate  int32
		format      string
	)

	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Collect memory profile on-demand",
		Long: `Collect memory allocation profile for a target service.

This command profiles memory allocations by sampling heap allocations
(default sampling rate: 512KB). The output shows allocation flame graphs.

For historical memory profiles, use 'coral query memory-profile --since 1h'.

Examples:
  coral profile memory --service api --duration 30
  coral profile memory --service api --sample-rate 4096
  coral profile memory --service api --duration 10 --format json

Note: Memory profiling is planned for RFD 077 and not yet implemented.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if serviceName == "" {
				return fmt.Errorf("--service is required")
			}

			// TODO: Implement memory profiling (RFD 077).
			return fmt.Errorf("memory profiling not yet implemented (RFD 077)")
		},
	}

	cmd.Flags().StringVarP(&serviceName, "service", "s", "", "Service name (required)")
	cmd.Flags().Int32VarP(&duration, "duration", "d", 30, "Profiling duration in seconds")
	cmd.Flags().Int32Var(&sampleRate, "sample-rate", 512, "Sampling rate in KB (default: 512KB)")
	cmd.Flags().StringVar(&format, "format", "folded", "Output format: folded, json")

	cmd.MarkFlagRequired("service") //nolint:errcheck

	return cmd
}
