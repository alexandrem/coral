package duckdb

import (
	"github.com/spf13/cobra"
)

// NewDuckDBCmd creates the duckdb command (RFD 039).
func NewDuckDBCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "duckdb",
		Short: "Query agent DuckDB databases remotely",
		Long: `Query agent DuckDB databases using SQL via HTTP remote attach.

This command provides direct SQL access to agent-local DuckDB databases
containing Beyla metrics (HTTP, gRPC, SQL). Uses DuckDB's native HTTP attach
feature for read-only, zero-serialization queries.

Examples:
  # List agents with Beyla metrics
  coral duckdb list-agents

  # Interactive shell (single agent)
  coral duckdb shell agent-prod-1

  # One-shot query
  coral duckdb query agent-prod-1 "SELECT * FROM beyla_http_metrics_local LIMIT 10"

  # Query with CSV output
  coral duckdb query agent-prod-1 "SELECT service_name, COUNT(*) FROM beyla_http_metrics_local GROUP BY service_name" --format csv

For more information, see: https://github.com/coral-io/coral/blob/main/RFDs/039-duckdb-remote-query-cli.md`,
	}

	// Add subcommands.
	cmd.AddCommand(NewListAgentsCmd())
	cmd.AddCommand(NewQueryCmd())
	cmd.AddCommand(NewShellCmd())

	return cmd
}
