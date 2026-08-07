package debug

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
)

// NewFilterCmd creates the debug filter command for live probe filter updates (RFD 090).
func NewFilterCmd() *cobra.Command {
	var (
		minDuration time.Duration
		maxDuration time.Duration
		filterRate  uint32
		format      string
	)

	cmd := &cobra.Command{
		Use:   "filter <session-id>",
		Short: "Update kernel-level filter for an active debug session",
		Long: `Update the eBPF filter parameters for an active debug session without
detaching or interrupting event collection.

All filter flags are optional. Unset flags leave the corresponding filter
dimension unchanged (i.e. set to zero = passthrough).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			ctx := context.Background()

			if minDuration == 0 && maxDuration == 0 && filterRate <= 1 {
				return fmt.Errorf("at least one filter flag must be provided (--min-duration, --max-duration, or --filter-rate)")
			}

			client, err := getColonyDebugClient()
			if err != nil {
				return fmt.Errorf("failed to resolve colony address: %w", err)
			}

			req := &colonypb.UpdateProbeFilterRequest{
				SessionId: sessionID,
				Filter: &agentv1.UprobeFilter{
					MinDurationNs: uint64(minDuration.Nanoseconds()),
					MaxDurationNs: uint64(maxDuration.Nanoseconds()),
					SampleRate:    filterRate,
				},
			}

			_, err = client.UpdateProbeFilter(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to update probe filter: %w", err)
			}

			if format == "json" {
				result := map[string]any{
					"session_id":      sessionID,
					"min_duration_ns": minDuration.Nanoseconds(),
					"max_duration_ns": maxDuration.Nanoseconds(),
					"filter_rate":     filterRate,
				}
				data, _ := json.MarshalIndent(result, "", "  ")
				_, err = fmt.Fprintln(os.Stdout, string(data))
				return err
			}

			fmt.Printf("Filter updated for session %s\n", sessionID)
			if minDuration > 0 {
				fmt.Printf("  min-duration: %s\n", minDuration)
			}
			if maxDuration > 0 {
				fmt.Printf("  max-duration: %s\n", maxDuration)
			}
			if filterRate > 1 {
				fmt.Printf("  filter-rate:  1 in %d\n", filterRate)
			}

			return nil
		},
	}

	cmd.Flags().DurationVar(&minDuration, "min-duration", 0, "Only emit events slower than this threshold (e.g. 50ms)")
	cmd.Flags().DurationVar(&maxDuration, "max-duration", 0, "Only emit events faster than this threshold (e.g. 500ms)")
	cmd.Flags().Uint32Var(&filterRate, "filter-rate", 0, "Emit 1 in every N events at kernel level (0 or 1 = all)")
	cmd.Flags().StringVar(&format, "format", "text", "Output format (text, json)")

	return cmd
}
