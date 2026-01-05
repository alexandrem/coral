package query

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/cli/helpers"
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

  # Generate flamegraph
  coral query cpu-profile --service api --since 1h | flamegraph.pl > cpu-historical.svg`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if serviceName == "" {
				return fmt.Errorf("--service is required")
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

			fmt.Fprintf(os.Stderr, "Querying historical CPU profiles for service '%s'\n", serviceName)
			fmt.Fprintf(os.Stderr, "Time range: %s to %s\n", startTime.Format(time.RFC3339), endTime.Format(time.RFC3339))

			// Get colony URL.
			colonyURL, err := helpers.GetColonyURL("")
			if err != nil {
				return fmt.Errorf("failed to get colony URL: %w", err)
			}

			// Create gRPC client.
			client := colonyv1connect.NewColonyDebugServiceClient(
				http.DefaultClient,
				colonyURL,
			)

			// Create request.
			req := connect.NewRequest(&colonypb.QueryHistoricalCPUProfileRequest{
				ServiceName: serviceName,
				StartTime:   timestamppb.New(startTime),
				EndTime:     timestamppb.New(endTime),
			})

			// Call QueryHistoricalCPUProfile RPC.
			ctx := context.Background()
			resp, err := client.QueryHistoricalCPUProfile(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to query colony: %w", err)
			}

			if !resp.Msg.Success {
				return fmt.Errorf("historical CPU profile query failed: %s", resp.Msg.Error)
			}

			if len(resp.Msg.Samples) == 0 {
				fmt.Fprintf(os.Stderr, "No historical CPU profile data found for service '%s' in the specified time range.\n", serviceName)
				fmt.Fprintf(os.Stderr, "The colony polls agents every 30 seconds. Wait a moment and try again.\n")
				return nil
			}

			// Output metadata to stderr.
			fmt.Fprintf(os.Stderr, "Total unique stacks: %d\n", len(resp.Msg.Samples))
			fmt.Fprintf(os.Stderr, "Total samples: %d\n\n", resp.Msg.TotalSamples)

			// Output folded stack format to stdout.
			for _, sample := range resp.Msg.Samples {
				if len(sample.FrameNames) == 0 {
					continue
				}

				// Folded format: frame1;frame2;frame3 count
				// Stack frames from gRPC response are in the correct order (root to leaf).
				// Reverse them for flamegraph.pl compatibility (innermost first).
				for i := len(sample.FrameNames) - 1; i >= 0; i-- {
					fmt.Print(sample.FrameNames[i])
					if i > 0 {
						fmt.Print(";")
					}
				}

				fmt.Printf(" %d\n", sample.Count)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&serviceName, "service", "s", "", "Service name (required)")
	cmd.Flags().StringVar(&since, "since", "1h", "Query from this time ago (e.g., '1h', '30m', '24h')")
	cmd.Flags().StringVar(&until, "until", "", "Query until this time ago (default: now)")
	cmd.Flags().StringVar(&buildID, "build-id", "", "Filter by specific build ID")
	cmd.Flags().StringVar(&format, "format", "folded", "Output format: folded, json")

	cmd.MarkFlagRequired("service") //nolint:errcheck

	return cmd
}
