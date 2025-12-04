//nolint:errcheck
package duckdb

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/chzyer/readline"
	"github.com/spf13/cobra"
)

// NewShellCmd creates the shell subcommand for interactive DuckDB queries.
func NewShellCmd() *cobra.Command {
	var agents []string
	var database string

	cmd := &cobra.Command{
		Use:   "shell <agent-id>",
		Short: "Open an interactive DuckDB shell attached to an agent database",
		Long: `Opens an interactive SQL shell attached to one or more agent databases.

The shell provides a REPL (Read-Eval-Print Loop) for executing SQL queries
against agent databases (telemetry, eBPF metrics, etc.). Supports command history,
multi-line queries, and meta-commands.

Meta-commands:
  .tables     - List all tables in attached databases
  .databases  - Show attached databases
  .help       - Show help message
  .exit       - Exit shell (or Ctrl+D)
  .quit       - Exit shell

Examples:
  # Single agent shell (auto-detect database)
  coral duckdb shell agent-prod-1

  # Single agent shell with specific database
  coral duckdb shell agent-prod-1 --database metrics.duckdb

  # Query telemetry data
  coral duckdb shell agent-prod-1 -d telemetry.duckdb

  # Multi-agent shell (same database across all agents)
  coral duckdb shell --agents agent-prod-1,agent-prod-2 -d metrics.duckdb

  # Example query in shell
  duckdb> SELECT service_name, COUNT(*) as count
      ..> FROM beyla_http_metrics_local
      ..> WHERE timestamp > now() - INTERVAL '5 minutes'
      ..> GROUP BY service_name;

If --database is not specified, the first available database will be used.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var agentIDs []string

			if len(agents) > 0 {
				// Multi-agent mode via --agents flag.
				agentIDs = agents
			} else if len(args) == 1 {
				// Single agent mode via positional argument.
				agentIDs = []string{args[0]}
			} else {
				return fmt.Errorf("must specify agent ID as argument or use --agents flag")
			}

			ctx := context.Background()

			// Create DuckDB connection.
			db, err := createDuckDBConnection(ctx)
			if err != nil {
				return err
			}
			defer db.Close()

			// Attach databases.
			attachedDBs, err := attachDatabases(ctx, db, agentIDs, database)
			if err != nil {
				return err
			}

			// Start interactive shell.
			return runInteractiveShell(ctx, db, agentIDs, database, attachedDBs)
		},
	}

	cmd.Flags().StringSliceVar(&agents, "agents", nil, "Comma-separated list of agent IDs for multi-agent queries")
	cmd.Flags().StringVarP(&database, "database", "d", "", "Database name (e.g., metrics.duckdb)")

	return cmd
}

// attachDatabases resolves agent IPs and attaches their databases.
// Returns a list of attached database aliases.
func attachDatabases(ctx context.Context, db *sql.DB, agentIDs []string, databaseName string) ([]string, error) {
	var attachedDBs []string

	for _, agentID := range agentIDs {
		meshIP, err := resolveAgentAddress(ctx, agentID)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve agent %s: %w", agentID, err)
		}

		// Determine which database to attach.
		dbName := databaseName
		if dbName == "" {
			// Query available databases and use the first one.
			databases, err := listAgentDatabases(ctx, meshIP)
			if err != nil {
				return nil, fmt.Errorf("failed to query available databases for agent %s: %w", agentID, err)
			}
			if len(databases) == 0 {
				return nil, fmt.Errorf("agent %s has no available databases", agentID)
			}
			dbName = databases[0]
			fmt.Printf("Using database: %s (agent: %s)\n", dbName, agentID)
		}

		if err := attachAgentDatabase(ctx, db, agentID, meshIP, dbName); err != nil {
			return nil, fmt.Errorf("failed to attach database for agent %s: %w", agentID, err)
		}

		alias := fmt.Sprintf("agent_%s", sanitizeAgentID(agentID))
		attachedDBs = append(attachedDBs, alias)
	}

	return attachedDBs, nil
}

// runInteractiveShell runs an interactive DuckDB shell with readline support.
func runInteractiveShell(ctx context.Context, db *sql.DB, agentIDs []string, databaseName string, attachedDBs []string) error {
	// Create readline instance with history.
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "duckdb> ",
		HistoryFile:     os.ExpandEnv("$HOME/.coral/duckdb_history"),
		InterruptPrompt: "^C",
		EOFPrompt:       ".exit",
	})
	if err != nil {
		return fmt.Errorf("failed to initialize readline: %w", err)
	}
	defer func(rl *readline.Instance) {
		_ = rl.Close() // TODO: errcheck
	}(rl)

	// Print welcome message.
	fmt.Println("DuckDB interactive shell. Type '.exit' to quit, '.help' for help.")
	fmt.Println()
	printAttachedDBs(attachedDBs)

	// REPL loop.
	var queryBuffer strings.Builder

	for {
		line, err := rl.Readline()
		if err != nil {
			if err == readline.ErrInterrupt {
				// Ctrl+C: Clear current query buffer.
				queryBuffer.Reset()
				rl.SetPrompt("duckdb> ")
				continue
			} else if err == io.EOF {
				// Ctrl+D: Exit.
				fmt.Println()
				break
			}
			return fmt.Errorf("readline error: %w", err)
		}

		line = strings.TrimSpace(line)

		// Handle empty lines.
		if line == "" {
			continue
		}

		// Handle meta-commands.
		if strings.HasPrefix(line, ".") {
			if err := handleMetaCommand(ctx, db, line, agentIDs, databaseName, &attachedDBs); err != nil {
				if err.Error() == "exit" {
					break
				}
				fmt.Printf("Error: %v\n", err)
			}
			continue
		}

		// Accumulate query lines.
		if queryBuffer.Len() > 0 {
			queryBuffer.WriteString(" ")
		}
		queryBuffer.WriteString(line)

		// Check if query is complete (ends with semicolon).
		if strings.HasSuffix(strings.TrimSpace(queryBuffer.String()), ";") {
			query := queryBuffer.String()
			queryBuffer.Reset()
			rl.SetPrompt("duckdb> ")

			// Execute query.
			if err := executeQuery(ctx, db, query); err != nil {
				fmt.Printf("Error: %v\n", err)
			}
		} else {
			// Multi-line query continues.
			rl.SetPrompt("    ..> ")
		}
	}

	return nil
}

func printAttachedDBs(attachedDBs []string) {
	if len(attachedDBs) == 1 {
		fmt.Printf("Attached agent database: %s\n\n", attachedDBs[0])
	} else {
		fmt.Printf("Attached databases: %s\n\n", strings.Join(attachedDBs, ", "))
	}
}

// handleMetaCommand handles shell meta-commands like .tables, .help, etc.
func handleMetaCommand(ctx context.Context, db *sql.DB, command string, agentIDs []string, databaseName string, attachedDBs *[]string) error {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return nil
	}

	switch parts[0] {
	case ".exit", ".quit":
		return fmt.Errorf("exit")

	case ".help":
		fmt.Println("Meta-commands:")
		fmt.Println("  .tables     - List all tables in attached databases")
		fmt.Println("  .databases  - Show attached databases")
		fmt.Println("  .refresh    - Detach and re-attach databases to refresh data")
		fmt.Println("  .help       - Show this help message")
		fmt.Println("  .exit       - Exit shell")
		fmt.Println("  .quit       - Exit shell")
		fmt.Println()
		fmt.Println("Query syntax:")
		fmt.Println("  - End queries with semicolon (;)")
		fmt.Println("  - Use Ctrl+C to cancel current query")
		fmt.Println("  - Use Ctrl+D or .exit to quit")
		return nil

	case ".databases":
		fmt.Println("Attached databases:")
		for _, dbName := range *attachedDBs {
			fmt.Printf("  - %s\n", dbName)
		}
		return nil

	case ".refresh":
		fmt.Println("Refreshing databases...")
		// Detach all currently attached databases.
		for _, alias := range *attachedDBs {
			if _, err := db.ExecContext(ctx, fmt.Sprintf("DETACH %s", alias)); err != nil {
				return fmt.Errorf("failed to detach %s: %w", alias, err)
			}
		}

		// Re-attach databases.
		newAttachedDBs, err := attachDatabases(ctx, db, agentIDs, databaseName)
		if err != nil {
			return fmt.Errorf("failed to re-attach databases: %w", err)
		}
		*attachedDBs = newAttachedDBs
		fmt.Println("Successfully refreshed databases.")
		printAttachedDBs(*attachedDBs)
		return nil

	case ".tables":
		// Query to list all tables across all attached databases.
		query := "SELECT database_name, table_name FROM duckdb_tables() WHERE database_name != 'system' ORDER BY database_name, table_name;"
		return executeQuery(ctx, db, query)

	default:
		return fmt.Errorf("unknown meta-command: %s (try .help)", parts[0])
	}
}

// executeQuery executes a SQL query and prints results in table format.
func executeQuery(ctx context.Context, db *sql.DB, query string) error {
	start := time.Now()

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()

	// Get column names.
	columns, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("failed to get columns: %w", err)
	}

	// Print results.
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

	duration := time.Since(start)
	fmt.Printf("\n(%d rows in %s)\n\n", rowCount, duration.Round(time.Millisecond))

	return nil
}
