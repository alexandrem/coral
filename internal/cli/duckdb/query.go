package duckdb

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// NewQueryCmd creates the query subcommand for one-shot SQL queries.
func NewQueryCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "query <agent-id> <sql>",
		Short: "Execute a one-shot SQL query against an agent database",
		Long: `Executes a SQL query against an agent's DuckDB database and prints the results.

The query command attaches to the agent's Beyla metrics database and executes
the provided SQL query, returning results in the specified format (table, CSV, or JSON).

Examples:
  # Query recent HTTP metrics (table format)
  coral duckdb query agent-prod-1 "SELECT * FROM beyla_http_metrics_local LIMIT 10"

  # Query with aggregation (CSV format)
  coral duckdb query agent-prod-1 "SELECT service_name, COUNT(*) as count FROM beyla_http_metrics_local GROUP BY service_name" --format csv

  # Query with JSON output
  coral duckdb query agent-prod-1 "SELECT * FROM beyla_http_metrics_local WHERE http_status_code >= 500" --format json

Available tables:
  - beyla_http_metrics_local: HTTP request metrics
  - beyla_grpc_metrics_local: gRPC method metrics
  - beyla_sql_metrics_local: SQL query metrics`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentID := args[0]
			sqlQuery := args[1]

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			// Resolve agent ID to mesh IP.
			meshIP, err := resolveAgentAddress(ctx, agentID)
			if err != nil {
				return fmt.Errorf("failed to resolve agent address: %w", err)
			}

			// Create DuckDB connection.
			db, err := createDuckDBConnection(ctx)
			if err != nil {
				return err
			}
			defer db.Close()

			// Attach agent database.
			if err := attachAgentDatabase(ctx, db, agentID, meshIP, "beyla.duckdb"); err != nil {
				return err
			}

			// Execute query.
			rows, err := db.QueryContext(ctx, sqlQuery)
			if err != nil {
				return fmt.Errorf("query failed: %w", err)
			}
			defer rows.Close()

			// Get column names.
			columns, err := rows.Columns()
			if err != nil {
				return fmt.Errorf("failed to get columns: %w", err)
			}

			// Format and print results based on requested format.
			switch format {
			case "table":
				return printResultsAsTable(rows, columns)
			case "csv":
				return printResultsAsCSV(rows, columns)
			case "json":
				return printResultsAsJSON(rows, columns)
			default:
				return fmt.Errorf("invalid format: %s (must be table, csv, or json)", format)
			}
		},
	}

	cmd.Flags().StringVarP(&format, "format", "f", "table", "Output format (table, csv, json)")

	return cmd
}

// printResultsAsTable prints query results in a formatted table.
func printResultsAsTable(rows interface {
	Scan(...interface{}) error
	Next() bool
}, columns []string) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	// Print header.
	for i, col := range columns {
		if i > 0 {
			fmt.Fprint(w, "\t")
		}
		fmt.Fprint(w, col)
	}
	fmt.Fprintln(w)

	// Print separator.
	for i := range columns {
		if i > 0 {
			fmt.Fprint(w, "\t")
		}
		fmt.Fprint(w, "---")
	}
	fmt.Fprintln(w)

	// Print rows.
	rowCount := 0
	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return fmt.Errorf("failed to scan row: %w", err)
		}

		for i, val := range values {
			if i > 0 {
				fmt.Fprint(w, "\t")
			}
			fmt.Fprint(w, formatValue(val))
		}
		fmt.Fprintln(w)
		rowCount++
	}

	w.Flush()

	fmt.Printf("\n(%d rows)\n", rowCount)

	return nil
}

// printResultsAsCSV prints query results in CSV format.
func printResultsAsCSV(rows interface {
	Scan(...interface{}) error
	Next() bool
}, columns []string) error {
	w := csv.NewWriter(os.Stdout)
	defer w.Flush()

	// Write header.
	if err := w.Write(columns); err != nil {
		return fmt.Errorf("failed to write CSV header: %w", err)
	}

	// Write rows.
	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return fmt.Errorf("failed to scan row: %w", err)
		}

		record := make([]string, len(columns))
		for i, val := range values {
			record[i] = formatValue(val)
		}

		if err := w.Write(record); err != nil {
			return fmt.Errorf("failed to write CSV row: %w", err)
		}
	}

	return nil
}

// printResultsAsJSON prints query results in JSON format.
func printResultsAsJSON(rows interface {
	Scan(...interface{}) error
	Next() bool
}, columns []string) error {
	var results []map[string]interface{}

	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return fmt.Errorf("failed to scan row: %w", err)
		}

		row := make(map[string]interface{})
		for i, col := range columns {
			row[col] = values[i]
		}

		results = append(results, row)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(results); err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}

	return nil
}

// formatValue formats a value for display in table or CSV output.
func formatValue(val interface{}) string {
	if val == nil {
		return "NULL"
	}

	switch v := val.(type) {
	case []byte:
		return string(v)
	case time.Time:
		return v.Format(time.RFC3339)
	default:
		return fmt.Sprintf("%v", v)
	}
}
