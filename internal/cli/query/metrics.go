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

func NewMetricsCmd() *cobra.Command {
	var (
		since           string
		source          string
		protocol        string
		httpRoute       string
		httpMethod      string
		statusCodeRange string
	)

	cmd := &cobra.Command{
		Use:   "metrics [service]",
		Short: "Query metrics",
		Long: `Query metrics from all sources (eBPF + OTLP).

Returns unified metrics with source annotations.

Examples:
  coral query metrics api                          # All metrics for api service
  coral query metrics api --protocol http          # Only HTTP metrics
  coral query metrics api --source ebpf            # Only eBPF metrics
  coral query metrics api --http-route /api/v1/*   # Filter by route
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
			req := &colonypb.QueryUnifiedMetricsRequest{
				Service:         service,
				TimeRange:       since,
				Source:          source,
				Protocol:        protocol,
				HttpRoute:       httpRoute,
				HttpMethod:      httpMethod,
				StatusCodeRange: statusCodeRange,
			}

			resp, err := client.QueryUnifiedMetrics(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to query metrics: %w", err)
			}

			// Print result
			fmt.Println(resp.Msg.Result)
			return nil
		},
	}

	cmd.Flags().StringVar(&since, "since", "1h", "Time range (e.g., 1h, 30m, 24h)")
	cmd.Flags().StringVar(&source, "source", "all", "Data source: ebpf, telemetry, or all")
	cmd.Flags().StringVar(&protocol, "protocol", "auto", "Protocol: http, grpc, sql, or auto")
	cmd.Flags().StringVar(&httpRoute, "http-route", "", "HTTP route pattern filter")
	cmd.Flags().StringVar(&httpMethod, "http-method", "", "HTTP method filter (GET, POST, etc.)")
	cmd.Flags().StringVar(&statusCodeRange, "status-code-range", "", "Status code range (2xx, 3xx, 4xx, 5xx)")

	return cmd
}
