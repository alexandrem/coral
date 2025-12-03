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

// NewSQLCmd creates the 'sql' command for querying eBPF SQL metrics.
func NewSQLCmd() *cobra.Command {
	var (
		timeFlags helpers.TimeFlags
		format    string
		operation string
		table     string
	)

	cmd := &cobra.Command{
		Use:   "sql <service>",
		Short: "Query SQL query metrics",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			serviceName := args[0]
			ctx := context.Background()

			// Parse time range
			timeRange, err := timeFlags.Parse()
			if err != nil {
				return err
			}

			// Get client
			client, err := helpers.GetAgentClient("")
			if err != nil {
				return err
			}

			// Prepare request
			req := &agentv1.QueryEbpfMetricsRequest{
				StartTime:    timeRange.Start.Unix(),
				EndTime:      timeRange.End.Unix(),
				ServiceNames: []string{serviceName},
				MetricTypes:  []agentv1.EbpfMetricType{agentv1.EbpfMetricType_EBPF_METRIC_TYPE_SQL},
			}

			// Execute query
			resp, err := client.QueryEbpfMetrics(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to query metrics: %w", err)
			}

			// Process and aggregate metrics
			rows := processSQLMetrics(resp.Msg.SqlMetrics, operation, table)

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
	cmd.Flags().StringVar(&operation, "operation", "", "Filter by SQL operation (SELECT, INSERT, UPDATE, DELETE)")
	cmd.Flags().StringVar(&table, "table", "", "Filter by table name")

	return cmd
}

type SQLMetricRow struct {
	Service   string `header:"Service"`
	Operation string `header:"Operation"`
	Table     string `header:"Table"`
	Queries   uint64 `header:"Queries"`
	P50       string `header:"P50"`
	P95       string `header:"P95"`
	P99       string `header:"P99"`
}

func processSQLMetrics(metrics []*agentv1.EbpfSqlMetric, operationFilter, tableFilter string) []SQLMetricRow {
	// Map key: operation + table
	type key struct {
		Operation string
		Table     string
	}

	type agg struct {
		Queries       uint64
		LatencyCounts []uint64
	}

	aggregated := make(map[key]*agg)
	var buckets []float64

	for _, m := range metrics {
		if operationFilter != "" && m.SqlOperation != operationFilter {
			continue
		}
		if tableFilter != "" && m.TableName != tableFilter {
			continue
		}

		k := key{Operation: m.SqlOperation, Table: m.TableName}
		a, ok := aggregated[k]
		if !ok {
			a = &agg{}
			aggregated[k] = a
		}

		a.Queries += m.QueryCount

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

	var rows []SQLMetricRow
	for k, a := range aggregated {
		serviceName := ""
		if len(metrics) > 0 {
			serviceName = metrics[0].ServiceName
		}
		rows = append(rows, SQLMetricRow{
			Service:   serviceName,
			Operation: k.Operation,
			Table:     k.Table,
			Queries:   a.Queries,
			P50:       calculatePercentile(buckets, a.LatencyCounts, 0.50),
			P95:       calculatePercentile(buckets, a.LatencyCounts, 0.95),
			P99:       calculatePercentile(buckets, a.LatencyCounts, 0.99),
		})
	}

	// Sort by queries desc
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Queries > rows[j].Queries
	})

	return rows
}
