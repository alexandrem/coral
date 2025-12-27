package query

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/cli/helpers"
)

func NewSQLCmd() *cobra.Command {
	var maxRows int32

	cmd := &cobra.Command{
		Use:   "sql <query>",
		Short: "Execute raw SQL query",
		Long: `Execute a raw SQL query against the colony DuckDB.

The query is executed in read-only mode with automatic row limits.

Examples:
  coral query sql "SELECT service_name, COUNT(*) FROM ebpf_http_metrics GROUP BY service_name"
  coral query sql "SELECT * FROM ebpf_trace_spans WHERE duration_ns > 1000000 LIMIT 10"
  coral query sql "SELECT AVG(duration_ns) FROM ebpf_http_metrics WHERE service_name = 'api'"
`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sqlQuery := args[0]
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

			// Execute RPC
			req := &colonypb.ExecuteQueryRequest{
				Sql:     sqlQuery,
				MaxRows: maxRows,
			}

			resp, err := client.ExecuteQuery(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to execute query: %w", err)
			}

			// Print result
			if resp.Msg.RowCount == 0 {
				fmt.Println("No rows returned")
				return nil
			}

			// Print header
			if len(resp.Msg.Columns) > 0 {
				fmt.Println(strings.Join(resp.Msg.Columns, " | "))
				fmt.Println(strings.Repeat("-", len(strings.Join(resp.Msg.Columns, " | "))))
			}

			// Print rows
			for _, row := range resp.Msg.Rows {
				fmt.Println(strings.Join(row.Values, " | "))
			}

			fmt.Printf("\n%d row(s) returned", resp.Msg.RowCount)
			if resp.Msg.RowCount == maxRows {
				fmt.Printf(" (limited to %d)", maxRows)
			}
			fmt.Println()

			return nil
		},
	}

	cmd.Flags().Int32Var(&maxRows, "max-rows", 1000, "Maximum rows to return (default: 1000)")
	return cmd
}
