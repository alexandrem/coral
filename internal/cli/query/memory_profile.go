package query

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/cli/helpers"
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
  coral query memory-profile --service api --build-id abc123 --since 24h`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if serviceName == "" {
				return fmt.Errorf("--service is required")
			}

			now := time.Now()
			var startTime, endTime time.Time

			if since != "" {
				duration, err := time.ParseDuration(since)
				if err != nil {
					return fmt.Errorf("invalid --since duration: %w", err)
				}
				startTime = now.Add(-duration)
			} else {
				startTime = now.Add(-1 * time.Hour)
			}

			if until != "" {
				duration, err := time.ParseDuration(until)
				if err != nil {
					return fmt.Errorf("invalid --until duration: %w", err)
				}
				endTime = now.Add(-duration)
			} else {
				endTime = now
			}

			if startTime.After(endTime) {
				return fmt.Errorf("--since time must be before --until time")
			}

			fmt.Fprintf(os.Stderr, "Querying historical memory profiles for service '%s'\n", serviceName)
			fmt.Fprintf(os.Stderr, "Time range: %s to %s\n", startTime.Format(time.RFC3339), endTime.Format(time.RFC3339))

			colonyURL, err := helpers.GetColonyURL("")
			if err != nil {
				return fmt.Errorf("failed to get colony URL: %w", err)
			}

			client := colonyv1connect.NewColonyDebugServiceClient(
				http.DefaultClient,
				colonyURL,
			)

			req := connect.NewRequest(&colonypb.QueryHistoricalMemoryProfileRequest{
				ServiceName: serviceName,
				StartTime:   timestamppb.New(startTime),
				EndTime:     timestamppb.New(endTime),
			})

			ctx := context.Background()
			resp, err := client.QueryHistoricalMemoryProfile(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to query colony: %w", err)
			}

			if !resp.Msg.Success {
				return fmt.Errorf("historical memory profile query failed: %s", resp.Msg.Error)
			}

			if len(resp.Msg.Samples) == 0 {
				fmt.Fprintf(os.Stderr, "No historical memory profile data found for service '%s' in the specified time range.\n", serviceName)
				return nil
			}

			fmt.Fprintf(os.Stderr, "Total unique stacks: %d\n", len(resp.Msg.Samples))
			fmt.Fprintf(os.Stderr, "Total alloc bytes: %s\n\n", formatQueryBytes(resp.Msg.TotalAllocBytes))

			// Show type breakdown if requested.
			if showTypes {
				printTypeBreakdown(resp.Msg.Samples, resp.Msg.TotalAllocBytes)
			}

			// Output folded stack format to stdout (alloc_bytes as count).
			for _, sample := range resp.Msg.Samples {
				if len(sample.FrameNames) == 0 {
					continue
				}
				for i := len(sample.FrameNames) - 1; i >= 0; i-- {
					fmt.Print(sample.FrameNames[i])
					if i > 0 {
						fmt.Print(";")
					}
				}
				fmt.Printf(" %d\n", sample.AllocBytes)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&serviceName, "service", "s", "", "Service name (required)")
	cmd.Flags().StringVar(&since, "since", "1h", "Query from this time ago (e.g., '1h', '30m', '24h')")
	cmd.Flags().StringVar(&until, "until", "", "Query until this time ago (default: now)")
	cmd.Flags().StringVar(&buildID, "build-id", "", "Filter by specific build ID")
	cmd.Flags().BoolVar(&showGrowth, "show-growth", false, "Show heap growth trends")
	cmd.Flags().BoolVar(&showTypes, "show-types", false, "Show allocation breakdown by type")

	cmd.MarkFlagRequired("service") //nolint:errcheck

	return cmd
}

// printTypeBreakdown computes and displays allocation type breakdown from samples.
func printTypeBreakdown(samples []*agentv1.MemoryStackSample, totalBytes int64) {
	type typeAgg struct {
		bytes   int64
		objects int64
	}
	types := make(map[string]*typeAgg)
	for _, sample := range samples {
		if len(sample.FrameNames) == 0 {
			continue
		}
		// Leaf function is the first frame (innermost allocation site).
		typeName := classifyQueryAllocType(sample.FrameNames[0])
		if agg, ok := types[typeName]; ok {
			agg.bytes += sample.AllocBytes
			agg.objects += sample.AllocObjects
		} else {
			types[typeName] = &typeAgg{bytes: sample.AllocBytes, objects: sample.AllocObjects}
		}
	}

	type typeEntry struct {
		name    string
		bytes   int64
		objects int64
		pct     float64
	}
	var entries []typeEntry
	for name, agg := range types {
		pct := 0.0
		if totalBytes > 0 {
			pct = float64(agg.bytes) / float64(totalBytes) * 100
		}
		entries = append(entries, typeEntry{name: name, bytes: agg.bytes, objects: agg.objects, pct: pct})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].bytes > entries[j].bytes })
	if len(entries) > 10 {
		entries = entries[:10]
	}

	fmt.Fprintf(os.Stderr, "Top Allocation Types:\n")
	for _, e := range entries {
		fmt.Fprintf(os.Stderr, "  %5.1f%%  %s  %s\n", e.pct, formatQueryBytes(e.bytes), e.name)
	}
	fmt.Fprintln(os.Stderr)
}

// classifyQueryAllocType maps a leaf function name to an allocation type category.
func classifyQueryAllocType(funcName string) string {
	switch {
	case strings.Contains(funcName, "makeslice") || strings.Contains(funcName, "growslice"):
		return "slice"
	case strings.Contains(funcName, "makemap") || strings.Contains(funcName, "mapassign"):
		return "map"
	case strings.Contains(funcName, "newobject") || strings.Contains(funcName, "mallocgc"):
		return "object"
	case strings.Contains(funcName, "concatstrings") || strings.Contains(funcName, "slicebytetostring") || strings.Contains(funcName, "stringtoslicebyte"):
		return "string"
	case strings.Contains(funcName, "makechan"):
		return "channel"
	default:
		return funcName
	}
}

// formatQueryBytes formats bytes into human-readable form.
func formatQueryBytes(b int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
