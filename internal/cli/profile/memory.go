package profile

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
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
  coral profile memory --service api --duration 10 --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if serviceName == "" {
				return fmt.Errorf("--service is required")
			}

			if duration <= 0 {
				duration = 30
			}
			if duration > 300 {
				return fmt.Errorf("duration cannot exceed 300 seconds")
			}

			colonyURL, err := getColonyURL()
			if err != nil {
				return fmt.Errorf("failed to get colony URL: %w", err)
			}

			client := colonyv1connect.NewColonyDebugServiceClient(
				http.DefaultClient,
				colonyURL,
			)

			fmt.Fprintf(os.Stderr, "Profiling memory for service '%s' (%ds)...\n",
				serviceName, duration)

			req := connect.NewRequest(&debugpb.ProfileMemoryRequest{
				ServiceName:     serviceName,
				DurationSeconds: duration,
				SampleRateBytes: sampleRate,
			})

			ctx, cancel := context.WithTimeout(context.Background(),
				time.Duration(duration+60)*time.Second)
			defer cancel()

			resp, err := client.ProfileMemory(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to collect memory profile: %w", err)
			}

			if !resp.Msg.Success {
				return fmt.Errorf("memory profiling failed: %s", resp.Msg.Error)
			}

			switch format {
			case "json":
				return printMemoryProfileJSON(resp.Msg)
			default:
				printMemoryProfileFolded(resp.Msg)
				return nil
			}
		},
	}

	cmd.Flags().StringVarP(&serviceName, "service", "s", "", "Service name (required)")
	cmd.Flags().Int32VarP(&duration, "duration", "d", 30, "Profiling duration in seconds")
	cmd.Flags().Int32Var(&sampleRate, "sample-rate", 512, "Sampling rate in KB (default: 512KB)")
	cmd.Flags().StringVar(&format, "format", "folded", "Output format: folded, json")

	cmd.MarkFlagRequired("service") //nolint:errcheck

	return cmd
}

// printMemoryProfileFolded outputs memory profile in folded stack format.
func printMemoryProfileFolded(resp *debugpb.ProfileMemoryResponse) {
	// Print top allocators to stderr.
	if len(resp.TopFunctions) > 0 {
		fmt.Fprintf(os.Stderr, "\nTop Memory Allocators:\n")
		for _, tf := range resp.TopFunctions {
			fmt.Fprintf(os.Stderr, "  %5.1f%%  %s  %s\n",
				tf.Pct, formatBytes(tf.Bytes), tf.Function)
		}
		fmt.Fprintln(os.Stderr)
	}

	// Print top allocation types to stderr.
	if len(resp.TopTypes) > 0 {
		fmt.Fprintf(os.Stderr, "Top Allocation Types:\n")
		for _, tt := range resp.TopTypes {
			fmt.Fprintf(os.Stderr, "  %5.1f%%  %s  %s\n",
				tt.Pct, formatBytes(tt.Bytes), tt.TypeName)
		}
		fmt.Fprintln(os.Stderr)
	}

	// Print heap stats to stderr.
	if resp.Stats != nil {
		fmt.Fprintf(os.Stderr, "Heap Stats:\n")
		fmt.Fprintf(os.Stderr, "  Alloc: %s\n", formatBytes(resp.Stats.AllocBytes))
		fmt.Fprintf(os.Stderr, "  Sys:   %s\n", formatBytes(resp.Stats.SysBytes))
		fmt.Fprintf(os.Stderr, "  GC:    %d cycles\n\n", resp.Stats.NumGc)
	}

	// Print folded stacks to stdout.
	for _, sample := range resp.Samples {
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
}

// printMemoryProfileJSON outputs memory profile in JSON format.
func printMemoryProfileJSON(resp *debugpb.ProfileMemoryResponse) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(resp)
}

// formatBytes formats bytes into human-readable form.
func formatBytes(b int64) string {
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
