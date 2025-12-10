// Package duckdb provides CLI commands for querying DuckDB databases.
//
//nolint:errcheck
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
	var database string
	var colony bool

	cmd := &cobra.Command{
		Use:   "query [agent-id] <sql>",
		Short: "Execute a one-shot SQL query against a database",
		Long: `Executes a SQL query against an agent or colony DuckDB database and prints the results.

The query command attaches to the specified database and executes
the provided SQL query, returning results in the specified format (table, CSV, or JSON).

Examples:
  # Query agent eBPF HTTP metrics (table format)
  coral duckdb query agent-prod-1 "SELECT * FROM beyla_http_metrics_local LIMIT 10" --database metrics.duckdb

  # Query colony historical metrics
  coral duckdb query --colony "SELECT * FROM beyla_http_metrics WHERE timestamp > now() - INTERVAL '7 days' LIMIT 10"

  # Query with aggregation (CSV format)
  coral duckdb query agent-prod-1 "SELECT service_name, COUNT(*) FROM beyla_http_metrics_local GROUP BY service_name" --format csv -d metrics.duckdb

  # Query colony with JSON output
  coral duckdb query --colony "SELECT service_name, AVG(latency_ms) FROM beyla_http_metrics GROUP BY service_name" --format json

If --database is not specified, the first available database will be used.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var agentID, sqlQuery string

			// Parse arguments based on --colony flag.
			if colony {
				// Colony mode: only SQL query argument.
				if len(args) != 1 {
					return fmt.Errorf("--colony mode requires exactly one argument (SQL query)")
				}
				sqlQuery = args[0]
			} else {
				// Agent mode: agent ID + SQL query.
				if len(args) != 2 {
					return fmt.Errorf("agent mode requires two arguments (agent-id and SQL query)")
				}
				agentID = args[0]
				sqlQuery = args[1]
			}

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			// Create DuckDB connection.
			db, err := createDuckDBConnection(ctx)
			if err != nil {
				return err
			}
			defer db.Close()

			// Attach database based on mode.
			if colony {
				// Colony mode: attach colony database.
				dbName := database
				if dbName == "" {
					// Query available databases and use the first one.
					databases, err := listColonyDatabases(ctx)
					if err != nil {
						return fmt.Errorf("failed to query available databases: %w", err)
					}
					if len(databases) == 0 {
						return fmt.Errorf("colony has no available databases")
					}
					dbName = databases[0]
					fmt.Printf("Using database: %s\n", dbName)
				}

				if err := attachColonyDatabase(ctx, db, dbName); err != nil {
					return err
				}
			} else {
				// Agent mode: resolve agent ID and attach agent database.
				meshIP, err := resolveAgentAddress(ctx, agentID)
				if err != nil {
					return fmt.Errorf("failed to resolve agent address: %w", err)
				}

				// Determine which database to query.
				dbName := database
				if dbName == "" {
					// Query available databases and use the first one.
					databases, err := listAgentDatabases(ctx, meshIP)
					if err != nil {
						return fmt.Errorf("failed to query available databases: %w", err)
					}
					if len(databases) == 0 {
						return fmt.Errorf("agent %s has no available databases", agentID)
					}
					dbName = databases[0]
					fmt.Printf("Using database: %s\n", dbName)
				}

				if err := attachAgentDatabase(ctx, db, agentID, meshIP, dbName); err != nil {
					return err
				}
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
	cmd.Flags().StringVarP(&database, "database", "d", "", "Database name (e.g., metrics.duckdb)")
	cmd.Flags().BoolVar(&colony, "colony", false, "Query colony database instead of agent")

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

	_ = w.Flush() // TODO: errcheck

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
