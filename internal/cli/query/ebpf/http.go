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

// NewHTTPCmd creates the 'http' command for querying eBPF HTTP metrics.
func NewHTTPCmd() *cobra.Command {
	var (
		timeFlags helpers.TimeFlags
		format    string
		route     string
		status    string // TODO: Implement status filtering
	)

	cmd := &cobra.Command{
		Use:   "http <service>",
		Short: "Query HTTP RED metrics",
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
				MetricTypes:  []agentv1.EbpfMetricType{agentv1.EbpfMetricType_EBPF_METRIC_TYPE_HTTP},
			}

			// Execute query
			resp, err := client.QueryEbpfMetrics(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to query metrics: %w", err)
			}

			// Process and aggregate metrics
			rows := processHTTPMetrics(resp.Msg.HttpMetrics, route)

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
	cmd.Flags().StringVar(&route, "route", "", "Filter by route pattern")
	cmd.Flags().StringVar(&status, "status", "", "Filter by status code (e.g. 2xx, 500)")

	return cmd
}

type HTTPMetricRow struct {
	Service  string `header:"Service"`
	Method   string `header:"Method"`
	Route    string `header:"Route"`
	Requests uint64 `header:"Requests"`
	Errors   uint64 `header:"Errors"`
	P50      string `header:"P50"`
	P95      string `header:"P95"`
	P99      string `header:"P99"`
}

func processHTTPMetrics(metrics []*agentv1.EbpfHttpMetric, routeFilter string) []HTTPMetricRow {
	// Map key: method + route
	type key struct {
		Method string
		Route  string
	}

	type agg struct {
		Requests       uint64
		Errors         uint64
		LatencyCounts  []uint64
		LatencyBuckets []float64
	}

	// We assume all metrics share the same bucket boundaries for simplicity.
	// In practice, we might need to handle mismatched buckets, but for now we take the first non-empty one.

	aggregated := make(map[key]*agg)
	var buckets []float64

	for _, m := range metrics {
		if routeFilter != "" && m.HttpRoute != routeFilter {
			continue
		}

		k := key{Method: m.HttpMethod, Route: m.HttpRoute}
		a, ok := aggregated[k]
		if !ok {
			a = &agg{}
			aggregated[k] = a
		}

		a.Requests += m.RequestCount
		if m.HttpStatusCode >= 500 {
			a.Errors += m.RequestCount // Assuming count represents this specific status code instance
		}

		// Initialize buckets if needed
		if len(buckets) == 0 && len(m.LatencyBuckets) > 0 {
			buckets = m.LatencyBuckets
		}

		// Merge histograms
		// This is simplified; assumes buckets match.
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

	var rows []HTTPMetricRow
	for k, a := range aggregated {
		rows = append(rows, HTTPMetricRow{
			Service:  metrics[0].ServiceName, // Assuming single service query
			Method:   k.Method,
			Route:    k.Route,
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

func calculatePercentile(buckets []float64, counts []uint64, p float64) string {
	if len(buckets) == 0 || len(counts) == 0 {
		return "-"
	}

	var total uint64
	for _, c := range counts {
		total += c
	}

	if total == 0 {
		return "-"
	}

	target := float64(total) * p
	var current uint64

	for i, count := range counts {
		current += count
		if float64(current) >= target {
			if i < len(buckets) {
				return fmt.Sprintf("%.2fms", buckets[i])
			}
			// Last bucket (infinity/overflow)
			return fmt.Sprintf(">%.2fms", buckets[len(buckets)-1])
		}
	}

	return "-"
}
