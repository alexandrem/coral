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

func NewSummaryCmd() *cobra.Command {
	var since string

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
			req := &colonypb.QueryUnifiedSummaryRequest{
				Service:   service,
				TimeRange: since,
			}

			resp, err := client.QueryUnifiedSummary(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to query summary: %w", err)
			}

			// Print result
			fmt.Println(resp.Msg.Result)
			return nil
		},
	}

	cmd.Flags().StringVar(&since, "since", "5m", "Time range (e.g., 5m, 1h, 24h)")
	return cmd
}
