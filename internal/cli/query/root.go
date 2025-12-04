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
  summary  - Quick health overview
  traces   - Distributed traces
  metrics  - Service metrics
  logs     - Application logs

Examples:
  coral query summary my-service
  coral query traces my-service --since 1h
  coral query metrics my-service --protocol http
  coral query logs my-service --level error
`,
	}

	cmd.AddCommand(NewSummaryCmd())
	cmd.AddCommand(NewTracesCmd())
	cmd.AddCommand(NewMetricsCmd())
	cmd.AddCommand(NewLogsCmd())

	return cmd
}
