package ebpf

import (
	"context"
	"fmt"
	"os"
	"sort"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/cli/helpers"
)

// NewGRPCCmd creates the 'grpc' command for querying eBPF gRPC metrics.
func NewGRPCCmd() *cobra.Command {
	var (
		timeFlags helpers.TimeFlags
		format    string
		method    string
	)

	cmd := &cobra.Command{
		Use:   "grpc <service>",
		Short: "Query gRPC RED metrics",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			serviceName := args[0]
			ctx := context.Background()

			// Parse time range
			timeRange, err := timeFlags.Parse()
			if err != nil {
				return err
			}

			// Get colony client.
			client, err := helpers.GetColonyClient("")
			if err != nil {
				return err
			}

			// Prepare request
			req := &agentv1.QueryEbpfMetricsRequest{
				StartTime:    timeRange.Start.Unix(),
				EndTime:      timeRange.End.Unix(),
				ServiceNames: []string{serviceName},
				MetricTypes:  []agentv1.EbpfMetricType{agentv1.EbpfMetricType_EBPF_METRIC_TYPE_GRPC},
			}

			// Execute query
			resp, err := client.QueryEbpfMetrics(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to query metrics: %w", err)
			}

			// Process and aggregate metrics
			rows := processGRPCMetrics(resp.Msg.GrpcMetrics, method)

			// Format output
			formatter, err := helpers.NewFormatter(helpers.OutputFormat(format))
			if err != nil {
				return err
			}

			return formatter.Format(rows, os.Stdout)
		},
	}

	timeFlags.AddFlags(cmd.Flags())
	cmd.Flags().StringVarP(&format, "format", "o", "table", "Output format (table, json, csv)")
	cmd.Flags().StringVar(&method, "method", "", "Filter by gRPC method")

	return cmd
}

type GRPCMetricRow struct {
	Service  string `header:"Service"`
	Method   string `header:"Method"`
	Requests uint64 `header:"Requests"`
	Errors   uint64 `header:"Errors"`
	P50      string `header:"P50"`
	P95      string `header:"P95"`
	P99      string `header:"P99"`
}

func processGRPCMetrics(metrics []*agentv1.EbpfGrpcMetric, methodFilter string) []GRPCMetricRow {
	// Map key: method
	type agg struct {
		Requests      uint64
		Errors        uint64
		LatencyCounts []uint64
	}

	aggregated := make(map[string]*agg)
	var buckets []float64

	for _, m := range metrics {
		if methodFilter != "" && m.GrpcMethod != methodFilter {
			continue
		}

		a, ok := aggregated[m.GrpcMethod]
		if !ok {
			a = &agg{}
			aggregated[m.GrpcMethod] = a
		}

		a.Requests += m.RequestCount
		// gRPC status code 0 = OK, anything else is an error
		if m.GrpcStatusCode != 0 {
			a.Errors += m.RequestCount
		}

		// Initialize buckets if needed
		if len(buckets) == 0 && len(m.LatencyBuckets) > 0 {
			buckets = m.LatencyBuckets
		}

		// Merge histograms
		if len(m.LatencyCounts) > 0 {
			if len(a.LatencyCounts) == 0 {
				a.LatencyCounts = make([]uint64, len(m.LatencyCounts))
			}
			for i, c := range m.LatencyCounts {
				if i < len(a.LatencyCounts) {
					a.LatencyCounts[i] += c
				}
			}
		}
	}

	var rows []GRPCMetricRow
	for method, a := range aggregated {
		serviceName := ""
		if len(metrics) > 0 {
			serviceName = metrics[0].ServiceName
		}
		rows = append(rows, GRPCMetricRow{
			Service:  serviceName,
			Method:   method,
			Requests: a.Requests,
			Errors:   a.Errors,
			P50:      calculatePercentile(buckets, a.LatencyCounts, 0.50),
			P95:      calculatePercentile(buckets, a.LatencyCounts, 0.95),
			P99:      calculatePercentile(buckets, a.LatencyCounts, 0.99),
		})
	}

	// Sort by requests desc
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Requests > rows[j].Requests
	})

	return rows
}
