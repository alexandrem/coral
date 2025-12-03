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
	// cmd.AddCommand(NewGRPCCmd()) // Phase 3
	// cmd.AddCommand(NewSQLCmd())  // Phase 3
	// cmd.AddCommand(NewTracesCmd()) // Phase 4

	return cmd
}
