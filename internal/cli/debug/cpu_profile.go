package debug

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/config"
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

// runHistoricalCPUProfile queries historical CPU profile samples from colony database (RFD 072).
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

	// Create resolver and get colony ID.
	resolver, err := config.NewResolver()
	if err != nil {
		return fmt.Errorf("failed to create config resolver: %w", err)
	}

	colonyID, err := resolver.ResolveColonyID()
	if err != nil {
		return fmt.Errorf("failed to resolve colony ID: %w", err)
	}

	// Load colony configuration to get storage path.
	loader := resolver.GetLoader()
	cfg, err := loader.LoadColonyConfig(colonyID)
	if err != nil {
		return fmt.Errorf("failed to load colony config: %w", err)
	}

	// Open colony database in read-only mode.
	logger := zerolog.Nop() // Use no-op logger for CLI.
	db, err := database.NewReadOnly(cfg.StoragePath, cfg.ColonyID, logger)
	if err != nil {
		return fmt.Errorf("failed to open colony database: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Query CPU profile summaries.
	ctx := context.Background()
	summaries, err := db.QueryCPUProfileSummaries(ctx, serviceName, startTime, endTime)
	if err != nil {
		return fmt.Errorf("failed to query CPU profile summaries: %w", err)
	}

	if len(summaries) == 0 {
		fmt.Fprintf(os.Stderr, "No profile data found for the specified time range\n")
		return nil
	}

	// Aggregate samples by stack (sum counts across time).
	type stackKey struct {
		stackHash string
	}

	aggregated := make(map[stackKey]struct {
		frameIDs    []int64
		sampleCount int32
		buildIDs    map[string]bool // Track which build IDs contributed to this stack.
	})

	for _, summary := range summaries {
		key := stackKey{stackHash: summary.StackHash}

		if existing, exists := aggregated[key]; exists {
			// Merge: sum sample counts.
			existing.sampleCount += summary.SampleCount
			existing.buildIDs[summary.BuildID] = true
			aggregated[key] = existing
		} else {
			// New stack.
			aggregated[key] = struct {
				frameIDs    []int64
				sampleCount int32
				buildIDs    map[string]bool
			}{
				frameIDs:    summary.StackFrameIDs,
				sampleCount: summary.SampleCount,
				buildIDs:    map[string]bool{summary.BuildID: true},
			}
		}
	}

	// Decode frame IDs to frame names and output.
	fmt.Fprintf(os.Stderr, "Total unique stacks: %d\n", len(aggregated))
	fmt.Fprintf(os.Stderr, "Total samples: %d\n\n", len(summaries))

	totalSamples := uint64(0)
	for _, agg := range aggregated {
		totalSamples += uint64(agg.sampleCount)

		// Decode stack frames.
		frameNames, err := db.DecodeStackFrames(ctx, agg.frameIDs)
		if err != nil {
			return fmt.Errorf("failed to decode stack frames: %w", err)
		}

		// Output folded stack format.
		// Stack frames are stored root-to-leaf, which is the correct order for folded format.
		for i := len(frameNames) - 1; i >= 0; i-- {
			fmt.Print(frameNames[i])
			if i > 0 {
				fmt.Print(";")
			}
		}

		// Annotate with build ID if multiple versions were involved.
		if len(agg.buildIDs) > 1 {
			fmt.Print(";[multi-version]")
		}

		fmt.Printf(" %d\n", agg.sampleCount)
	}

	return nil
}
