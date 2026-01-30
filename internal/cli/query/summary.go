package query

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/cli/helpers"
	"github.com/coral-mesh/coral/internal/colony/database"
)

func NewSummaryCmd() *cobra.Command {
	var since string

	cmd := &cobra.Command{
		Use:   "summary [service]",
		Short: "Get a high-level health summary",
		Long: `Get a high-level health summary for services.

Shows service health status, error rates, latency issues, and recent errors.
Combines data from eBPF and OTLP sources by default.

Examples:
  coral query summary                    # All services
  coral query summary api                # Specific service
  coral query summary api --since 10m    # Custom time range
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			service := ""
			if len(args) > 0 {
				service = args[0]
			}

			ctx := context.Background()

			// Resolve colony URL
			colonyAddr, err := helpers.GetColonyURL("")
			if err != nil {
				return fmt.Errorf("failed to resolve colony address: %w", err)
			}

			// Create colony client
			client := colonyv1connect.NewColonyServiceClient(
				http.DefaultClient,
				colonyAddr,
			)

			// Execute RPC
			req := &colonypb.QueryUnifiedSummaryRequest{
				Service:   service,
				TimeRange: since,
			}

			resp, err := client.QueryUnifiedSummary(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to query summary: %w", err)
			}

			// Print result
			if len(resp.Msg.Summaries) == 0 {
				fmt.Println("No data found for the specified service and time range")
				return nil
			}

			fmt.Println("Service Health Summary:")
			for _, summary := range resp.Msg.Summaries {
				statusIcon := "✅"
				switch summary.Status {
				case "degraded":
					statusIcon = "⚠️"
				case "critical":
					statusIcon = "❌"
				}

				fmt.Printf("%s %s (%s)\n", statusIcon, summary.ServiceName, summary.Source)
				fmt.Printf("   Status: %s\n", summary.Status)
				fmt.Printf("   Requests: %d\n", summary.RequestCount)
				fmt.Printf("   Error Rate: %.2f%%\n", summary.ErrorRate)
				fmt.Printf("   Avg Latency: %.2fms\n", summary.AvgLatencyMs)

				// Display host resources if available (RFD 071).
				if summary.HostCpuUtilization > 0 || summary.HostMemoryUtilization > 0 {
					fmt.Println("   Host Resources:")
					if summary.HostCpuUtilization > 0 {
						fmt.Printf("     CPU: %.0f%% (avg: %.0f%%)\n",
							summary.HostCpuUtilization,
							summary.HostCpuUtilizationAvg)
					}
					if summary.HostMemoryUtilization > 0 {
						fmt.Printf("     Memory: %.1fGB/%.1fGB (%.0f%%)\n",
							summary.HostMemoryUsageGb,
							summary.HostMemoryLimitGb,
							summary.HostMemoryUtilization)
					}
				}

				// Display CPU profiling summary (RFD 074).
				if ps := summary.ProfilingSummary; ps != nil && len(ps.TopCpuHotspots) > 0 {
					hotspots := make([]database.ProfilingHotspot, len(ps.TopCpuHotspots))
					for i, h := range ps.TopCpuHotspots {
						hotspots[i] = database.ProfilingHotspot{
							Rank:        h.Rank,
							Frames:      h.Frames,
							Percentage:  h.Percentage,
							SampleCount: h.SampleCount,
						}
					}
					fmt.Print(database.FormatCompactSummary(ps.SamplingPeriod, ps.TotalSamples, hotspots))
				}

				// Display deployment context (RFD 074).
				if d := summary.Deployment; d != nil && d.BuildId != "" {
					fmt.Printf("   Deployment: %s (deployed %s ago)\n", d.BuildId, d.Age)
				}

				// Display regression indicators (RFD 074).
				if len(summary.Regressions) > 0 {
					fmt.Println("   Regressions:")
					for _, r := range summary.Regressions {
						fmt.Printf("     ⚠️  %s\n", r.Message)
					}
				}

				if len(summary.Issues) > 0 {
					fmt.Println("   Issues:")
					for _, issue := range summary.Issues {
						fmt.Printf("     - %s\n", issue)
					}
				}
				fmt.Println()
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&since, "since", "5m", "Time range (e.g., 5m, 1h, 24h)")
	return cmd
}
