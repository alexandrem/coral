// Package query provides the CLI query framework for observability data.
package query

import (
	"github.com/spf13/cobra"
)

// NewQueryCmd creates the 'query' command.
func NewQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query",
		Short: "Query observability data (metrics, traces, logs)",
		Long: `Query observability data from Coral.

Unified query interface combining eBPF and OTLP data sources.

Commands:
  services - List discovered services (NEW - RFD 076)
  summary  - Quick health overview
  traces   - Distributed traces
  metrics  - Service metrics (enhanced with --percentile - RFD 076)
  logs     - Application logs
  sql      - Execute raw SQL queries (NEW - RFD 076)

Examples:
  coral query services
  coral query summary my-service
  coral query traces my-service --since 1h
  coral query metrics my-service --metric http.server.duration --percentile 99
  coral query logs my-service --level error
  coral query sql "SELECT service_name, COUNT(*) FROM ebpf_http_metrics GROUP BY service_name"
`,
	}

	cmd.AddCommand(NewServicesCmd())
	cmd.AddCommand(NewSummaryCmd())
	cmd.AddCommand(NewTracesCmd())
	cmd.AddCommand(NewMetricsCmd())
	cmd.AddCommand(NewLogsCmd())
	cmd.AddCommand(NewSQLCmd())

	return cmd
}
