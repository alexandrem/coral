package query

import (
	"context"
	"fmt"
	"os"
	"sort"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/cli/helpers"
)

// NewTraceProfileCmd creates the trace-profile query command (RFD 078).
func NewTraceProfileCmd() *cobra.Command {
	var (
		serviceName string
		profileType string
		topK        int
	)

	cmd := &cobra.Command{
		Use:   "trace-profile <trace-id>",
		Short: "Correlate a distributed trace with CPU profiles (RFD 078)",
		Long: `Correlate a distributed trace with CPU/memory profiles sampled during the trace.

This command joins beyla_traces with cpu_profile_summaries on (process_pid, time_window)
to surface which functions consumed the most CPU during a specific request.

Examples:
  # Show CPU hotspots for a trace
  coral query trace-profile abc123def456...

  # Filter to a specific service in a multi-service trace
  coral query trace-profile abc123def456... --service payment-svc

  # Show top 5 hotspots per service (default: 10)
  coral query trace-profile abc123def456... --top 5`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			traceID := args[0]

			client, err := helpers.GetColonyClient("")
			if err != nil {
				return fmt.Errorf("failed to create colony client: %w", err)
			}

			profileTypeEnum := colonypb.ProfileType_PROFILE_TYPE_CPU
			if profileType == "memory" {
				profileTypeEnum = colonypb.ProfileType_PROFILE_TYPE_MEMORY
			}

			req := connect.NewRequest(&colonypb.QueryTraceProfileRequest{
				TraceId:     traceID,
				ServiceName: serviceName,
				ProfileType: profileTypeEnum,
			})

			ctx := context.Background()
			resp, err := client.QueryTraceProfile(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to query trace profile: %w", err)
			}

			// Print trace metadata.
			if meta := resp.Msg.TraceMetadata; meta != nil {
				fmt.Fprintf(os.Stderr, "Trace: %s\n", resp.Msg.TraceId)
				if meta.StartTime != nil {
					fmt.Fprintf(os.Stderr, "Start:     %s\n", meta.StartTime.AsTime().Format("15:04:05.000"))
				}
				fmt.Fprintf(os.Stderr, "Duration:  %dms\n", meta.TotalDurationMs)
				fmt.Fprintf(os.Stderr, "Services:  %v\n", meta.Services)
				fmt.Fprintf(os.Stderr, "Spans:     %d\n\n", meta.SpanCount)
			}

			if len(resp.Msg.ServiceProfiles) == 0 {
				fmt.Fprintf(os.Stderr, "No CPU profile data found for trace %s.\n", traceID)
				fmt.Fprintf(os.Stderr, "Possible reasons:\n")
				fmt.Fprintf(os.Stderr, "  - process.pid not reported in OTLP spans (Beyla >= 1.7 required)\n")
				fmt.Fprintf(os.Stderr, "  - Trace too short (<50ms) to capture profile samples at 19Hz\n")
				fmt.Fprintf(os.Stderr, "  - Continuous CPU profiling not enabled on this agent\n")
				return nil
			}

			// Sort profiles by total samples descending.
			profiles := resp.Msg.ServiceProfiles
			sort.Slice(profiles, func(i, j int) bool {
				return profiles[i].TotalSamples > profiles[j].TotalSamples
			})

			for _, svcProfile := range profiles {
				fmt.Printf("=== %s (pid:%d) — span: %s (%dms) ===\n",
					svcProfile.ServiceName,
					svcProfile.ProcessPid,
					svcProfile.SpanName,
					svcProfile.SpanDurationMs,
				)

				if svcProfile.CoverageWarning != "" {
					fmt.Printf("  ⚠  %s\n", svcProfile.CoverageWarning)
				}

				if len(svcProfile.TopHotspots) == 0 {
					fmt.Printf("  (no hotspot data)\n\n")
					continue
				}

				limit := topK
				if limit <= 0 || limit > len(svcProfile.TopHotspots) {
					limit = len(svcProfile.TopHotspots)
				}

				for _, hotspot := range svcProfile.TopHotspots[:limit] {
					// Print stack as root→leaf on one line.
					if len(hotspot.Frames) == 0 {
						continue
					}
					frames := hotspot.Frames
					if len(frames) > 5 {
						frames = frames[len(frames)-5:]
					}
					stackStr := ""
					for i, f := range frames {
						if i > 0 {
							stackStr += " → "
						}
						stackStr += f
					}
					fmt.Printf("  %5.1f%%  %s\n", hotspot.Percentage, stackStr)
				}

				fmt.Printf("  (%d total samples, ~%dms CPU time)\n\n",
					svcProfile.TotalSamples, svcProfile.CpuTimeMs)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&serviceName, "service", "s", "", "Filter to specific service name")
	cmd.Flags().StringVar(&profileType, "type", "cpu", "Profile type: cpu, memory")
	cmd.Flags().IntVar(&topK, "top", 10, "Number of top hotspots to display per service")

	return cmd
}
