// Package ebpf provides CLI commands for querying eBPF-collected observability data.
package ebpf

import (
	"github.com/spf13/cobra"
)

// NewEBPFCmd creates the 'ebpf' command.
func NewEBPFCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ebpf",
		Short: "Query eBPF-collected metrics and traces",
		Long: `Query observability data collected via eBPF.

Supported data types:
- http: HTTP RED metrics (latency, error rate, throughput)
- grpc: gRPC RED metrics
- sql: SQL query metrics
- traces: Distributed traces

Examples:
  coral query ebpf http my-service
  coral query ebpf traces --trace-id <id>
`,
	}

	cmd.AddCommand(NewHTTPCmd())
	cmd.AddCommand(NewGRPCCmd())
	cmd.AddCommand(NewSQLCmd())
	cmd.AddCommand(NewTracesCmd())

	return cmd
}
