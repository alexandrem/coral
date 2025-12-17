package debug

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
)

// NewCPUProfileCmd creates the cpu-profile command.
func NewCPUProfileCmd() *cobra.Command {
	var (
		serviceName     string
		podName         string
		durationSeconds int32
		frequencyHz     int32
		format          string
		agentID         string
	)

	cmd := &cobra.Command{
		Use:   "cpu-profile",
		Short: "Collect CPU profile samples using eBPF",
		Long: `Collect CPU profile samples for a target service using eBPF perf_event sampling.

This command profiles CPU usage by sampling stack traces at a specified frequency
(default 99Hz). The output can be used to generate flame graphs showing where
CPU time is being spent.

Examples:
  # Capture 30s CPU profile and output folded format
  coral debug cpu-profile --service api --duration 30

  # Generate flamegraph (requires flamegraph.pl installed)
  coral debug cpu-profile --service api --duration 30 | flamegraph.pl > cpu.svg

  # Profile specific pod instance with JSON output
  coral debug cpu-profile --service api --pod api-7d8f9c --duration 10 --format json

  # Custom sampling frequency
  coral debug cpu-profile --service api --duration 30 --frequency 49
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if serviceName == "" {
				return fmt.Errorf("--service is required")
			}

			if durationSeconds <= 0 {
				durationSeconds = 30 // Default 30 seconds
			}
			if durationSeconds > 300 {
				return fmt.Errorf("duration cannot exceed 300 seconds")
			}

			if frequencyHz <= 0 {
				frequencyHz = 99 // Default 99Hz
			}
			if frequencyHz > 1000 {
				return fmt.Errorf("frequency cannot exceed 1000Hz")
			}

			// Get colony URL.
			colonyURL, err := getColonyURL()
			if err != nil {
				return fmt.Errorf("failed to get colony URL: %w", err)
			}

			// Create client.
			client := colonyv1connect.NewDebugServiceClient(
				http.DefaultClient,
				colonyURL,
			)

			// Show progress message.
			fmt.Fprintf(os.Stderr, "Profiling CPU for service '%s' (%ds at %dHz)...\n",
				serviceName, durationSeconds, frequencyHz)

			// Create request.
			req := connect.NewRequest(&debugpb.ProfileCPURequest{
				ServiceName:     serviceName,
				PodName:         podName,
				DurationSeconds: durationSeconds,
				FrequencyHz:     frequencyHz,
				AgentId:         agentID,
			})

			// Call ProfileCPU RPC with extended timeout.
			ctx, cancel := context.WithTimeout(context.Background(),
				time.Duration(durationSeconds+60)*time.Second)
			defer cancel()

			resp, err := client.ProfileCPU(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to collect CPU profile: %w", err)
			}

			if !resp.Msg.Success {
				return fmt.Errorf("CPU profiling failed: %s", resp.Msg.Error)
			}

			// Output results based on format.
			switch format {
			case "json":
				return printCPUProfileJSON(resp.Msg)
			case "folded":
				fallthrough
			default:
				return printCPUProfileFolded(resp.Msg)
			}
		},
	}

	cmd.Flags().StringVarP(&serviceName, "service", "s", "", "Service name (required)")
	cmd.Flags().StringVar(&podName, "pod", "", "Pod name (optional, specific instance)")
	cmd.Flags().Int32VarP(&durationSeconds, "duration", "d", 30, "Profiling duration in seconds (default: 30s, max: 300s)")
	cmd.Flags().Int32Var(&frequencyHz, "frequency", 99, "Sampling frequency in Hz (default: 99Hz, max: 1000Hz)")
	cmd.Flags().StringVar(&format, "format", "folded", "Output format: folded (default), json")
	cmd.Flags().StringVar(&agentID, "agent-id", "", "Agent ID (optional, auto-discovered if not provided)")

	cmd.MarkFlagRequired("service") //nolint:errcheck

	return cmd
}

// printCPUProfileFolded prints the profile in folded stack format.
func printCPUProfileFolded(profile *debugpb.ProfileCPUResponse) error {
	// Print summary to stderr.
	fmt.Fprintf(os.Stderr, "Total samples: %d\n", profile.TotalSamples)
	if profile.LostSamples > 0 {
		fmt.Fprintf(os.Stderr, "Warning: Lost %d samples due to map overflow\n", profile.LostSamples)
	}
	fmt.Fprintf(os.Stderr, "Unique stacks: %d\n", len(profile.Samples))
	fmt.Fprintf(os.Stderr, "\n")

	// Print folded stacks to stdout (for piping to flamegraph.pl).
	for _, sample := range profile.Samples {
		if len(sample.FrameNames) == 0 {
			continue
		}

		// Folded format: frame1;frame2;frame3 count
		// Stack frames should be from outermost (root) to innermost (leaf).
		// Reverse the order since BPF captures innermost first.
		for i := len(sample.FrameNames) - 1; i >= 0; i-- {
			fmt.Print(sample.FrameNames[i])
			if i > 0 {
				fmt.Print(";")
			}
		}
		fmt.Printf(" %d\n", sample.Count)
	}

	return nil
}

// printCPUProfileJSON prints the profile in JSON format.
func printCPUProfileJSON(profile *debugpb.ProfileCPUResponse) error {
	// Simple JSON output without external dependencies.
	fmt.Println("{")
	fmt.Printf("  \"total_samples\": %d,\n", profile.TotalSamples)
	fmt.Printf("  \"lost_samples\": %d,\n", profile.LostSamples)
	fmt.Printf("  \"unique_stacks\": %d,\n", len(profile.Samples))
	fmt.Println("  \"samples\": [")

	for i, sample := range profile.Samples {
		fmt.Println("    {")
		fmt.Println("      \"frames\": [")
		for j, frame := range sample.FrameNames {
			fmt.Printf("        %q", frame)
			if j < len(sample.FrameNames)-1 {
				fmt.Println(",")
			} else {
				fmt.Println()
			}
		}
		fmt.Println("      ],")
		fmt.Printf("      \"count\": %d\n", sample.Count)
		if i < len(profile.Samples)-1 {
			fmt.Println("    },")
		} else {
			fmt.Println("    }")
		}
	}

	fmt.Println("  ]")
	fmt.Println("}")

	return nil
}
