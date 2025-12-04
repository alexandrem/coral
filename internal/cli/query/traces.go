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
)

func NewTracesCmd() *cobra.Command {
	var (
		since     string
		traceID   string
		source    string
		minDurMs  int
		maxTraces int
	)

	cmd := &cobra.Command{
		Use:   "traces [service]",
		Short: "Query distributed traces",
		Long: `Query distributed traces from all sources (eBPF + OTLP).

Returns unified trace data with source annotations.

Examples:
  coral query traces api                           # All traces for api service
  coral query traces --trace-id abc123             # Specific trace
  coral query traces api --source ebpf             # Only eBPF traces
  coral query traces api --min-duration-ms 500     # Only slow traces
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
			req := &colonypb.QueryUnifiedTracesRequest{
				Service:       service,
				TimeRange:     since,
				Source:        source,
				TraceId:       traceID,
				MinDurationMs: int32(minDurMs),
				MaxTraces:     int32(maxTraces),
			}

			resp, err := client.QueryUnifiedTraces(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to query traces: %w", err)
			}

			// Print result
			fmt.Println(resp.Msg.Result)
			return nil
		},
	}

	cmd.Flags().StringVar(&since, "since", "1h", "Time range (e.g., 1h, 30m, 24h)")
	cmd.Flags().StringVar(&traceID, "trace-id", "", "Specific trace ID")
	cmd.Flags().StringVar(&source, "source", "all", "Data source: ebpf, telemetry, or all")
	cmd.Flags().IntVar(&minDurMs, "min-duration-ms", 0, "Minimum trace duration in milliseconds")
	cmd.Flags().IntVar(&maxTraces, "max-traces", 10, "Maximum number of traces to return")

	return cmd
}
