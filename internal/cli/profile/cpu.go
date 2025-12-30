package profile

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

// NewCPUCmd creates the cpu profiling command.
func NewCPUCmd() *cobra.Command {
	var (
		serviceName     string
		podName         string
		durationSeconds int32
		frequencyHz     int32
		format          string
		agentID         string
	)

	cmd := &cobra.Command{
		Use:   "cpu",
		Short: "Collect CPU profile on-demand",
		Long: `Collect CPU profile samples for a target service using eBPF perf_event sampling.

This command profiles CPU usage by sampling stack traces at a specified frequency
(default 99Hz). The output can be used to generate flame graphs showing where
CPU time is being spent.

For historical CPU profiles, use 'coral query cpu-profile --since 1h'.

Examples:
  # Capture 30s CPU profile
  coral profile cpu --service api --duration 30

  # Generate flamegraph (requires flamegraph.pl)
  coral profile cpu --service api --duration 30 --format folded | flamegraph.pl > cpu.svg

  # Profile specific pod with custom frequency
  coral profile cpu --service api --pod api-7d8f9c --frequency 49

  # JSON output for processing
  coral profile cpu --service api --duration 10 --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if serviceName == "" {
				return fmt.Errorf("--service is required")
			}

			// Validate duration.
			if durationSeconds <= 0 {
				durationSeconds = 30 // Default 30 seconds
			}
			if durationSeconds > 300 {
				return fmt.Errorf("duration cannot exceed 300 seconds")
			}

			// Validate frequency.
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
