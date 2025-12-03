// Package query provides the CLI query framework for observability data.
package query

import (
	"github.com/spf13/cobra"

	"github.com/coral-mesh/coral/internal/cli/query/ebpf"
)

// NewQueryCmd creates the 'query' command.
func NewQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query",
		Short: "Query observability data (metrics, traces, events)",
		Long: `Query observability data from Coral.

Supported data types:
- ebpf: eBPF-collected metrics (HTTP, gRPC, SQL) and traces
- telemetry: OTLP spans, metrics, and logs
- events: Operational events (deployments, restarts)

Examples:
  coral query ebpf http my-service --since 1h
  coral query telemetry spans my-service --error
`,
	}

	cmd.AddCommand(ebpf.NewEBPFCmd())

	return cmd
}
