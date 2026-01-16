// Package query provides the CLI query framework for observability data.
package query

import (
	"github.com/spf13/cobra"
)

// NewQueryCmd creates the 'query' command.
func NewQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query",
		Short: "Query observability data (metrics, traces, logs, profiles)",
		Long: `Query observability data from Coral.

Unified query interface combining eBPF and OTLP data sources.

Commands:
  summary        - Service health overview and discovery
  traces         - Distributed traces
  metrics        - Service metrics (enhanced with --percentile - RFD 076)
  logs           - Application logs
  cpu-profile    - Historical CPU profiles (RFD 072)
  memory-profile - Historical memory profiles (RFD 077 - coming soon)
  sql            - Execute raw SQL queries (RFD 076)

Examples:
  coral query summary                  # List all services with telemetry
  coral query summary my-service       # Detailed service summary
  coral query traces my-service --since 1h
  coral query metrics my-service --metric http.server.duration --percentile 99
  coral query logs my-service --level error
  coral query cpu-profile my-service --since 1h
  coral query memory-profile my-service --since 1h --show-growth
  coral query sql "SELECT service_name, COUNT(*) FROM beyla_http_metrics GROUP BY service_name"
`,
	}

	cmd.AddCommand(NewSummaryCmd())
	cmd.AddCommand(NewTracesCmd())
	cmd.AddCommand(NewMetricsCmd())
	cmd.AddCommand(NewLogsCmd())
	cmd.AddCommand(NewCPUProfileCmd())
	cmd.AddCommand(NewMemoryProfileCmd())
	cmd.AddCommand(NewSQLCmd())

	return cmd
}
