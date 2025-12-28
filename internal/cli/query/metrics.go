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
		metric          string
		percentile      float64
	)

	cmd := &cobra.Command{
		Use:   "metrics [service]",
		Short: "Query metrics",
		Long: `Query metrics from all sources (eBPF + OTLP).

Returns unified metrics with source annotations.

RFD 076 Enhancement: Use --metric and --percentile for focused percentile queries.

Examples:
  coral query metrics api                                             # All metrics
  coral query metrics api --protocol http                             # Only HTTP metrics
  coral query metrics api --metric http.server.duration --percentile 99  # P99 latency (RFD 076)
  coral query metrics api --metric http.server.duration --percentile 50  # P50 latency (RFD 076)
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

			// RFD 076: Focused percentile query if --metric and --percentile are specified
			if metric != "" && percentile > 0 {
				return executePercentileQuery(ctx, client, service, metric, percentile, since)
			}

			// Original unified metrics query
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
			if resp.Msg.TotalMetrics == 0 {
				fmt.Println("No metrics found for the specified criteria")
				return nil
			}

			fmt.Printf("Metrics for %s:\n\n", service)

			if len(resp.Msg.HttpMetrics) > 0 {
				fmt.Println("HTTP Metrics:")
				for _, m := range resp.Msg.HttpMetrics {
					fmt.Printf("  %s %s %s\n", m.HttpMethod, m.HttpRoute, m.ServiceName)
					// Calculate percentiles from buckets if available
					p50, p95, p99 := "-", "-", "-"
					if len(m.LatencyBuckets) >= 3 {
						p50 = fmt.Sprintf("%.2fms", m.LatencyBuckets[0])
						p95 = fmt.Sprintf("%.2fms", m.LatencyBuckets[1])
						p99 = fmt.Sprintf("%.2fms", m.LatencyBuckets[2])
					}
					fmt.Printf("    Requests: %d | P50: %s | P95: %s | P99: %s\n",
						m.RequestCount, p50, p95, p99)
				}
				fmt.Println()
			}

			if len(resp.Msg.GrpcMetrics) > 0 {
				fmt.Printf("gRPC Metrics: %d\n", len(resp.Msg.GrpcMetrics))
				for _, m := range resp.Msg.GrpcMetrics {
					fmt.Printf("  %s\n", m.ServiceName)
					fmt.Printf("    Requests: %d\n", m.RequestCount)
				}
				fmt.Println()
			}

			if len(resp.Msg.SqlMetrics) > 0 {
				fmt.Printf("SQL Metrics: %d\n", len(resp.Msg.SqlMetrics))
				for _, m := range resp.Msg.SqlMetrics {
					fmt.Printf("  %s\n", m.ServiceName)
					fmt.Printf("    Queries: %d\n", m.QueryCount)
				}
				fmt.Println()
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&since, "since", "1h", "Time range (e.g., 1h, 30m, 24h)")
	cmd.Flags().StringVar(&source, "source", "all", "Data source: ebpf, telemetry, or all")
	cmd.Flags().StringVar(&protocol, "protocol", "auto", "Protocol: http, grpc, sql, or auto")
	cmd.Flags().StringVar(&httpRoute, "http-route", "", "HTTP route pattern filter")
	cmd.Flags().StringVar(&httpMethod, "http-method", "", "HTTP method filter (GET, POST, etc.)")
	cmd.Flags().StringVar(&statusCodeRange, "status-code-range", "", "Status code range (2xx, 3xx, 4xx, 5xx)")

	// RFD 076: Focused percentile query flags
	cmd.Flags().StringVar(&metric, "metric", "", "Metric name for focused query (e.g., http.server.duration)")
	cmd.Flags().Float64Var(&percentile, "percentile", 0, "Percentile to query (0-100, e.g., 99 for P99)")

	return cmd
}

// executePercentileQuery executes a focused percentile query (RFD 076).
func executePercentileQuery(ctx context.Context, client colonyv1connect.ColonyServiceClient, service, metric string, percentile float64, timeRange string) error {
	if service == "" {
		return fmt.Errorf("service name is required for percentile queries")
	}

	// Convert percentile from 0-100 to 0.0-1.0
	percentileValue := percentile / 100.0
	if percentileValue < 0 || percentileValue > 1 {
		return fmt.Errorf("percentile must be between 0 and 100")
	}

	// Parse time range to milliseconds
	timeRangeMs := int64(3600000) // Default 1 hour
	if timeRange != "" {
		// Simple parsing for common cases
		switch timeRange {
		case "5m":
			timeRangeMs = 5 * 60 * 1000
		case "15m":
			timeRangeMs = 15 * 60 * 1000
		case "30m":
			timeRangeMs = 30 * 60 * 1000
		case "1h":
			timeRangeMs = 60 * 60 * 1000
		case "2h":
			timeRangeMs = 2 * 60 * 60 * 1000
		case "24h":
			timeRangeMs = 24 * 60 * 60 * 1000
		}
	}

	req := &colonypb.GetMetricPercentileRequest{
		Service:     service,
		Metric:      metric,
		Percentile:  percentileValue,
		TimeRangeMs: timeRangeMs,
	}

	resp, err := client.GetMetricPercentile(ctx, connect.NewRequest(req))
	if err != nil {
		return fmt.Errorf("failed to query percentile: %w", err)
	}

	// Print focused result
	fmt.Printf("P%.0f %s for %s: ", percentile, metric, service)

	// Format value based on unit
	switch resp.Msg.Unit {
	case "nanoseconds":
		fmt.Printf("%.2f ms\n", resp.Msg.Value/1_000_000)
	case "microseconds":
		fmt.Printf("%.2f ms\n", resp.Msg.Value/1_000)
	case "milliseconds":
		fmt.Printf("%.2f ms\n", resp.Msg.Value)
	default:
		fmt.Printf("%.2f %s\n", resp.Msg.Value, resp.Msg.Unit)
	}

	return nil
}
