package query

import (
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/cli/helpers"
	"github.com/coral-mesh/coral/internal/flamegraph"
)

// NewCPUProfileCmd creates the cpu-profile query command.
func NewCPUProfileCmd() *cobra.Command {
	var (
		serviceName string
		since       string
		until       string
		buildID     string
		format      string
	)

	cmd := &cobra.Command{
		Use:   "cpu-profile",
		Short: "Query historical CPU profiles",
		Long: `Query historical CPU profiles from continuous profiling.

This command retrieves aggregated CPU profiles collected by the continuous
profiling system (RFD 072). Profiles are sampled every 15 seconds and retained
for up to 30 days.

For on-demand profiling, use 'coral profile cpu --duration 30'.

Examples:
  # Query last hour of CPU profiles
  coral query cpu-profile --service api --since 1h

  # Query specific time range
  coral query cpu-profile --service api --since 2h --until 1h

  # Filter by build ID
  coral query cpu-profile --service api --build-id abc123 --since 24h

  # Generate interactive SVG flame graph
  coral query cpu-profile --service api --since 1h --format svg > cpu.svg`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if serviceName == "" {
				return fmt.Errorf("--service is required")
			}

			if format != "folded" && format != "svg" {
				return fmt.Errorf("unknown format: %s (use 'folded' or 'svg')", format)
			}

			// Parse time range.
			now := time.Now()
			var startTime, endTime time.Time

			if since != "" {
				duration, err := time.ParseDuration(since)
				if err != nil {
					return fmt.Errorf("invalid --since duration: %w", err)
				}
				startTime = now.Add(-duration)
			} else {
				// Default to last 1 hour if not specified.
				startTime = now.Add(-1 * time.Hour)
			}

			if until != "" {
				duration, err := time.ParseDuration(until)
				if err != nil {
					return fmt.Errorf("invalid --until duration: %w", err)
				}
				endTime = now.Add(-duration)
			} else {
				// Default to now.
				endTime = now
			}

			if startTime.After(endTime) {
				return fmt.Errorf("--since time must be before --until time")
			}

			cmd.PrintErrf("Querying historical CPU profiles for service '%s'\n", serviceName)
			cmd.PrintErrf("Time range: %s to %s\n", startTime.Format(time.RFC3339), endTime.Format(time.RFC3339))

			// Create gRPC client.
			client, err := helpers.GetColonyDebugClient("")
			if err != nil {
				return fmt.Errorf("failed to create colony debug client: %w", err)
			}

			// Create request.
			req := connect.NewRequest(&colonypb.QueryHistoricalCPUProfileRequest{
				ServiceName: serviceName,
				StartTime:   timestamppb.New(startTime),
				EndTime:     timestamppb.New(endTime),
			})

			// Call QueryHistoricalCPUProfile RPC.
			ctx := cmd.Context()
			resp, err := client.QueryHistoricalCPUProfile(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to query colony: %w", err)
			}

			if !resp.Msg.Success {
				return fmt.Errorf("historical CPU profile query failed: %s", resp.Msg.Error)
			}

			if len(resp.Msg.Samples) == 0 {
				cmd.PrintErrf("No historical CPU profile data found for service '%s' in the specified time range.\n", serviceName)
				cmd.PrintErrf("The colony polls agents every 30 seconds. Wait a moment and try again.\n")
				return nil
			}

			// Output metadata to stderr.
			cmd.PrintErrf("Total unique stacks: %d\n", len(resp.Msg.Samples))
			cmd.PrintErrf("Total samples: %d\n\n", resp.Msg.TotalSamples)

			switch format {
			case "svg":
				stacks := make([]flamegraph.FoldedStack, 0, len(resp.Msg.Samples))
				for _, s := range resp.Msg.Samples {
					if len(s.FrameNames) == 0 {
						continue
					}
					// Reverse frames: decoded from storage in the raw eBPF order
					// (innermost first), same convention as cpuSamplesToFolded.
					frames := make([]string, len(s.FrameNames))
					for i, f := range s.FrameNames {
						frames[len(s.FrameNames)-1-i] = f
					}
					stacks = append(stacks, flamegraph.FoldedStack{
						Frames: frames,
						Value:  int64(s.Count),
					})
				}
				return flamegraph.Render(cmd.OutOrStdout(), stacks, flamegraph.Options{
					Title:     "CPU Flame Graph (Historical)",
					CountName: "samples",
					Colors:    flamegraph.PaletteHot,
				})
			default:
				// Output folded stack format to stdout.
				for _, sample := range resp.Msg.Samples {
					if len(sample.FrameNames) == 0 {
						continue
					}
					for i := len(sample.FrameNames) - 1; i >= 0; i-- {
						if _, err := fmt.Fprint(cmd.OutOrStdout(), sample.FrameNames[i]); err != nil {
							return err
						}
						if i > 0 {
							if _, err := fmt.Fprint(cmd.OutOrStdout(), ";"); err != nil {
								return err
							}
						}
					}
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), " %d\n", sample.Count); err != nil {
						return err
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&serviceName, "service", "s", "", "Service name (required)")
	cmd.Flags().StringVar(&since, "since", "1h", "Query from this time ago (e.g., '1h', '30m', '24h')")
	cmd.Flags().StringVar(&until, "until", "", "Query until this time ago (default: now)")
	cmd.Flags().StringVar(&buildID, "build-id", "", "Filter by specific build ID")
	cmd.Flags().StringVar(&format, "format", "folded", "Output format: folded, svg")

	cmd.MarkFlagRequired("service") //nolint:errcheck

	return cmd
}
