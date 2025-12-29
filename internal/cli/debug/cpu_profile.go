package debug

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"

	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/coral/mesh/v1/meshv1connect"
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
		since           string // RFD 072: Historical query start time (e.g., "1h", "30m")
		until           string // RFD 072: Historical query end time (default: now)
	)

	cmd := &cobra.Command{
		Use:   "cpu-profile",
		Short: "Collect CPU profile samples using eBPF",
		Long: `Collect CPU profile samples for a target service using eBPF perf_event sampling.

This command profiles CPU usage by sampling stack traces at a specified frequency
(default 99Hz). The output can be used to generate flame graphs showing where
CPU time is being spent.

The command supports two modes:

1. On-demand profiling (RFD 070): Captures a new profile for the specified duration.
2. Historical profiling (RFD 072): Queries pre-collected continuous profiles from
   the colony database using --since and --until flags.

Examples:
  # On-demand: Capture 30s CPU profile and output folded format
  coral debug cpu-profile --service api --duration 30

  # On-demand: Generate flamegraph (requires flamegraph.pl installed)
  coral debug cpu-profile --service api --duration 30 | flamegraph.pl > cpu.svg

  # Historical: Query profiles from the last hour
  coral debug cpu-profile --service api --since 1h

  # Historical: Query profiles from 2 hours ago to 1 hour ago
  coral debug cpu-profile --service api --since 2h --until 1h

  # Historical: Generate flame graph from last 30 minutes
  coral debug cpu-profile --service api --since 30m | flamegraph.pl > cpu-historical.svg

  # On-demand: Profile specific pod instance with JSON output
  coral debug cpu-profile --service api --pod api-7d8f9c --duration 10 --format json

  # On-demand: Custom sampling frequency
  coral debug cpu-profile --service api --duration 30 --frequency 49
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if serviceName == "" {
				return fmt.Errorf("--service is required")
			}

			// RFD 072: Check if this is a historical query.
			if since != "" || until != "" {
				return runHistoricalCPUProfile(serviceName, since, until, format)
			}

			// On-demand profiling (existing RFD 070 behavior).
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

	// RFD 072: Historical query flags.
	cmd.Flags().StringVar(&since, "since", "", "Query historical profiles from this time ago (e.g., '1h', '30m', '2h30m')")
	cmd.Flags().StringVar(&until, "until", "", "Query historical profiles until this time ago (default: now)")

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

// runHistoricalCPUProfile queries historical CPU profile samples via Colony gRPC API (RFD 072).
func runHistoricalCPUProfile(serviceName, since, until, format string) error {
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
	colonyURL, err := getColonyURL()
	if err != nil {
		return fmt.Errorf("failed to get colony URL: %w", err)
	}

	// Create gRPC client.
	client := colonyv1connect.NewDebugServiceClient(
		http.DefaultClient,
		colonyURL,
	)

	// Create request.
	req := connect.NewRequest(&debugpb.QueryHistoricalCPUProfileRequest{
		ServiceName: serviceName,
		StartTime:   timestamppb.New(startTime),
		EndTime:     timestamppb.New(endTime),
	})

	// Call QueryHistoricalCPUProfile RPC.
	ctx := context.Background()
	resp, err := client.QueryHistoricalCPUProfile(ctx, req)
	if err != nil {
		// Fallback: Try querying agent directly if colony is unavailable.
		// This supports e2e testing and direct agent queries.
		fmt.Fprintf(os.Stderr, "Colony query failed, trying agent directly...\n")
		return queryAgentDirectly(serviceName, startTime, endTime, format)
	}

	if !resp.Msg.Success {
		return fmt.Errorf("historical CPU profile query failed: %s", resp.Msg.Error)
	}

	if len(resp.Msg.Samples) == 0 {
		// Fallback: If colony has no data, try querying agent directly.
		// This handles the case where the colony exists but hasn't polled yet,
		// or for e2e tests without a colony.
		fmt.Fprintf(os.Stderr, "Colony has no data, trying agent directly...\n")
		return queryAgentDirectly(serviceName, startTime, endTime, format)
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
}

// queryAgentDirectly queries the agent's continuous profiling storage directly.
// This is a fallback when the colony is unavailable (e.g., e2e tests, local development).
func queryAgentDirectly(serviceName string, startTime, endTime time.Time, format string) error {
	// Try common agent addresses
	agentURLs := []string{
		"http://localhost:9001", // Default agent port
		"http://127.0.0.1:9001", // Explicit localhost
	}

	var lastErr error
	for _, agentURL := range agentURLs {
		err := tryQueryAgent(agentURL, serviceName, startTime, endTime, format)
		if err == nil {
			return nil
		}
		lastErr = err
	}

	return fmt.Errorf("failed to query agent: %w", lastErr)
}

// tryQueryAgent attempts to query a specific agent URL.
func tryQueryAgent(agentURL, serviceName string, startTime, endTime time.Time, format string) error {
	// Create agent debug service client.
	client := meshv1connect.NewDebugServiceClient(
		http.DefaultClient,
		agentURL,
	)

	// Create request.
	req := connect.NewRequest(&meshv1.QueryCPUProfileSamplesRequest{
		ServiceName: serviceName,
		StartTime:   timestamppb.New(startTime),
		EndTime:     timestamppb.New(endTime),
	})

	// Call QueryCPUProfileSamples RPC.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.QueryCPUProfileSamples(ctx, req)
	if err != nil {
		return fmt.Errorf("agent RPC call failed: %w", err)
	}

	if resp.Msg.Error != "" {
		return fmt.Errorf("agent query error: %s", resp.Msg.Error)
	}

	if len(resp.Msg.Samples) == 0 {
		fmt.Fprintf(os.Stderr, "No profile data found for the specified time range\n")
		return nil
	}

	// Aggregate samples by stack (sum counts across time).
	type stackKey string
	aggregated := make(map[stackKey]uint64)
	stackFrames := make(map[stackKey][]string)

	for _, sample := range resp.Msg.Samples {
		// Create stack key from frames.
		key := stackKey(fmt.Sprintf("%v", sample.StackFrames))
		aggregated[key] += uint64(sample.SampleCount)
		if _, exists := stackFrames[key]; !exists {
			stackFrames[key] = sample.StackFrames
		}
	}

	// Output metadata to stderr.
	fmt.Fprintf(os.Stderr, "Total unique stacks: %d\n", len(aggregated))
	fmt.Fprintf(os.Stderr, "Total samples: %d\n\n", resp.Msg.TotalSamples)

	// Output folded stack format to stdout.
	for key, count := range aggregated {
		frames := stackFrames[key]
		if len(frames) == 0 {
			continue
		}

		// Folded format: frame1;frame2;frame3 count
		// Reverse frames for flamegraph.pl compatibility (innermost first).
		for i := len(frames) - 1; i >= 0; i-- {
			fmt.Print(frames[i])
			if i > 0 {
				fmt.Print(";")
			}
		}

		fmt.Printf(" %d\n", count)
	}

	return nil
}
