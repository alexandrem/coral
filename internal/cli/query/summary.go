package query

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/cli/helpers"
	"github.com/coral-mesh/coral/internal/colony/database"
)

// summaryJSON is the JSON-serializable representation of a service summary.
type summaryJSON struct {
	ServiceName   string             `json:"service_name"`
	Source        string             `json:"source,omitempty"`
	Status        string             `json:"status"`
	RequestCount  int64              `json:"request_count"`
	ErrorRate     float64            `json:"error_rate"`
	AvgLatencyMs  float64            `json:"avg_latency_ms"`
	HostResources *hostResourcesJSON `json:"host_resources,omitempty"`
	Profiling     *profilingJSON     `json:"profiling_summary,omitempty"`
	Deployment    *deploymentJSON    `json:"deployment,omitempty"`
	Regressions   []regressionJSON   `json:"regression_indicators,omitempty"`
	Issues        []string           `json:"issues,omitempty"`
}

type hostResourcesJSON struct {
	CPUUtilization    float64 `json:"cpu_utilization_pct"`
	CPUUtilizationAvg float64 `json:"cpu_utilization_avg_pct"`
	MemoryUsageGB     float64 `json:"memory_usage_gb"`
	MemoryLimitGB     float64 `json:"memory_limit_gb"`
	MemoryUtilization float64 `json:"memory_utilization_pct"`
}

type profilingJSON struct {
	SamplingPeriod    string               `json:"sampling_period"`
	TotalSamples      uint64               `json:"total_samples"`
	BuildID           string               `json:"build_id,omitempty"`
	HotPath           []string             `json:"hot_path"`
	SamplesByFunction []functionSampleJSON `json:"samples_by_function"`
	// Memory profiling (RFD 077).
	MemoryHotPath     []string                   `json:"memory_hot_path,omitempty"`
	MemoryByFunction  []memoryFunctionSampleJSON `json:"memory_by_function,omitempty"`
	TotalAllocBytes   int64                      `json:"total_alloc_bytes,omitempty"`
	TotalAllocObjects int64                      `json:"total_alloc_objects,omitempty"`
}

type functionSampleJSON struct {
	Function   string  `json:"function"`
	Percentage float64 `json:"percentage"`
}

type memoryFunctionSampleJSON struct {
	Function   string  `json:"function"`
	Percentage float64 `json:"percentage"`
	AllocBytes int64   `json:"alloc_bytes"`
}

type deploymentJSON struct {
	BuildID string `json:"build_id"`
	Age     string `json:"age"`
}

type regressionJSON struct {
	Type               string  `json:"type"`
	Message            string  `json:"message"`
	BaselinePercentage float64 `json:"baseline_percentage"`
	CurrentPercentage  float64 `json:"current_percentage"`
	Delta              float64 `json:"delta"`
}

func NewSummaryCmd() *cobra.Command {
	var since string
	var format string

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
  coral query summary --format json      # JSON output
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			service := ""
			if len(args) > 0 {
				service = args[0]
			}

			ctx := context.Background()

			// Resolve colony URL.
			colonyAddr, err := helpers.GetColonyURL("")
			if err != nil {
				return fmt.Errorf("failed to resolve colony address: %w", err)
			}

			// Create colony client.
			client := colonyv1connect.NewColonyServiceClient(
				http.DefaultClient,
				colonyAddr,
			)

			// Execute RPC.
			req := &colonypb.QueryUnifiedSummaryRequest{
				Service:   service,
				TimeRange: since,
			}

			resp, err := client.QueryUnifiedSummary(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to query summary: %w", err)
			}

			if len(resp.Msg.Summaries) == 0 {
				if format == "json" {
					fmt.Println("[]")
				} else {
					fmt.Println("No data found for the specified service and time range")
				}
				return nil
			}

			if format == "json" {
				return printSummaryJSON(resp.Msg.Summaries)
			}
			return printSummaryText(resp.Msg.Summaries)
		},
	}

	cmd.Flags().StringVar(&since, "since", "5m", "Time range (e.g., 5m, 1h, 24h)")
	cmd.Flags().StringVar(&format, "format", "text", "Output format (text, json)")
	return cmd
}

func printSummaryJSON(summaries []*colonypb.UnifiedSummaryResult) error {
	out := make([]summaryJSON, 0, len(summaries))

	for _, s := range summaries {
		entry := summaryJSON{
			ServiceName:  s.ServiceName,
			Source:       s.Source,
			Status:       s.Status,
			RequestCount: s.RequestCount,
			ErrorRate:    s.ErrorRate,
			AvgLatencyMs: s.AvgLatencyMs,
			Issues:       s.Issues,
		}

		if s.HostCpuUtilization > 0 || s.HostMemoryUtilization > 0 {
			entry.HostResources = &hostResourcesJSON{
				CPUUtilization:    s.HostCpuUtilization,
				CPUUtilizationAvg: s.HostCpuUtilizationAvg,
				MemoryUsageGB:     s.HostMemoryUsageGb,
				MemoryLimitGB:     s.HostMemoryLimitGb,
				MemoryUtilization: s.HostMemoryUtilization,
			}
		}

		if ps := s.ProfilingSummary; ps != nil {
			hasCPU := len(ps.TopCpuHotspots) > 0 && ps.TotalSamples >= database.MinSamplesForSummary
			hasMemory := len(ps.TopMemoryHotspots) > 0 && ps.TotalAllocBytes >= database.MinAllocBytesForSummary

			if hasCPU || hasMemory {
				profiling := &profilingJSON{
					SamplingPeriod: ps.SamplingPeriod,
					TotalSamples:   ps.TotalSamples,
					BuildID:        ps.BuildId,
				}

				// CPU profiling.
				if hasCPU {
					// Build hot path from the hottest stack (reverse to caller→callee).
					frames := ps.TopCpuHotspots[0].Frames
					hotPath := make([]string, len(frames))
					for i, f := range frames {
						hotPath[len(frames)-1-i] = f
					}
					profiling.HotPath = hotPath

					// Build samples by function (deduped leaf names).
					seen := make(map[string]float64)
					var order []string
					for _, h := range ps.TopCpuHotspots {
						if len(h.Frames) == 0 {
							continue
						}
						name := database.ShortFunctionName(h.Frames[0])
						if _, ok := seen[name]; !ok {
							order = append(order, name)
						}
						seen[name] += h.Percentage
					}
					for _, name := range order {
						profiling.SamplesByFunction = append(profiling.SamplesByFunction, functionSampleJSON{
							Function:   name,
							Percentage: seen[name],
						})
					}
				}

				// Memory profiling (RFD 077).
				if hasMemory {
					profiling.TotalAllocBytes = ps.TotalAllocBytes
					profiling.TotalAllocObjects = ps.TotalAllocObjects

					// Build memory hot path from the hottest stack (reverse to caller→callee).
					memFrames := ps.TopMemoryHotspots[0].Frames
					memHotPath := make([]string, len(memFrames))
					for i, f := range memFrames {
						memHotPath[len(memFrames)-1-i] = f
					}
					profiling.MemoryHotPath = memHotPath

					// Build memory allocations by function (deduped leaf names).
					type memEntry struct {
						percentage float64
						bytes      int64
					}
					memSeen := make(map[string]*memEntry)
					var memOrder []string
					for _, h := range ps.TopMemoryHotspots {
						if len(h.Frames) == 0 {
							continue
						}
						name := database.ShortFunctionName(h.Frames[0])
						if _, ok := memSeen[name]; !ok {
							memOrder = append(memOrder, name)
							memSeen[name] = &memEntry{}
						}
						memSeen[name].percentage += h.Percentage
						memSeen[name].bytes += h.AllocBytes
					}
					for _, name := range memOrder {
						profiling.MemoryByFunction = append(profiling.MemoryByFunction, memoryFunctionSampleJSON{
							Function:   name,
							Percentage: memSeen[name].percentage,
							AllocBytes: memSeen[name].bytes,
						})
					}
				}

				entry.Profiling = profiling
			}
		}

		if d := s.Deployment; d != nil && d.BuildId != "" {
			entry.Deployment = &deploymentJSON{
				BuildID: d.BuildId,
				Age:     d.Age,
			}
		}

		for _, r := range s.Regressions {
			entry.Regressions = append(entry.Regressions, regressionJSON{
				Type:               r.Type.String(),
				Message:            r.Message,
				BaselinePercentage: r.BaselinePercentage,
				CurrentPercentage:  r.CurrentPercentage,
				Delta:              r.Delta,
			})
		}

		out = append(out, entry)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func printSummaryText(summaries []*colonypb.UnifiedSummaryResult) error {
	fmt.Println("Service Health Summary:")
	for _, summary := range summaries {
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

		// Display memory profiling summary (RFD 077).
		if ps := summary.ProfilingSummary; ps != nil && len(ps.TopMemoryHotspots) > 0 {
			hotspots := make([]database.MemoryProfilingHotspot, len(ps.TopMemoryHotspots))
			for i, h := range ps.TopMemoryHotspots {
				hotspots[i] = database.MemoryProfilingHotspot{
					Rank:         h.Rank,
					Frames:       h.Frames,
					Percentage:   h.Percentage,
					AllocBytes:   h.AllocBytes,
					AllocObjects: h.AllocObjects,
				}
			}
			fmt.Print(database.FormatCompactMemorySummary(ps.SamplingPeriod, ps.TotalAllocBytes, hotspots))
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
}
