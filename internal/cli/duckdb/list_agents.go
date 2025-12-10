//nolint:errcheck
package duckdb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// NewListAgentsCmd creates the list-agents subcommand (alias: list).
func NewListAgentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"list-agents"},
		Short:   "List available databases (colony and agents)",
		Long: `Lists all available DuckDB databases for querying, including the colony
database and agent databases.

The colony database contains aggregated historical metrics from all agents
(30 days HTTP/gRPC, 14 days SQL). Agent databases contain real-time metrics
(~1 hour retention).

Examples:
  coral duckdb list

Output includes:
  - Database name (colony or agent ID)
  - Type (colony or agent)
  - Status
  - Available databases`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Print header.
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			_, _ = fmt.Fprintln(w, "DATABASE\tTYPE\tSTATUS\tDATABASES")
			_, _ = fmt.Fprintln(w, "--------\t----\t------\t---------")

			// Try to get colony database information.
			colonyDatabases, colonyErr := listColonyDatabases(ctx)
			if colonyErr == nil && len(colonyDatabases) > 0 {
				fmt.Fprintf(w, "colony\tcolony\tavailable\t%s\n",
					strings.Join(colonyDatabases, ", "))
			} else if colonyErr != nil {
				fmt.Fprintf(w, "colony\tcolony\tunavailable\t-\n")
			}

			// Get agents from colony with database information.
			agents, err := listAgents(ctx, true) // Fetch databases for each agent.
			if err != nil {
				// If we got colony info, continue with just that.
				if colonyErr == nil {
					w.Flush()
					fmt.Printf("\nWarning: Failed to list agents: %v\n", err)
					fmt.Printf("\nTotal: 1 colony database\n")
					return nil
				}
				return fmt.Errorf("failed to list databases: %w", err)
			}

			// Print agents.
			for _, agent := range agents {
				databases := "-"
				if len(agent.Databases) > 0 {
					databases = strings.Join(agent.Databases, ", ")
				}

				status := agent.Status
				if status == "" {
					status = "unknown"
				}

				fmt.Fprintf(w, "%s\tagent\t%s\t%s\n",
					agent.AgentID,
					status,
					databases,
				)
			}

			w.Flush()

			// Print summary.
			agentsWithDBs := 0
			totalDBs := 0
			for _, agent := range agents {
				if len(agent.Databases) > 0 {
					agentsWithDBs++
					totalDBs += len(agent.Databases)
				}
			}

			colonyDBCount := 0
			if colonyErr == nil && len(colonyDatabases) > 0 {
				colonyDBCount = len(colonyDatabases)
			}

			fmt.Printf("\nTotal: %d colony database(s), %d agents (%d with databases, %d total agent databases)\n",
				colonyDBCount, len(agents), agentsWithDBs, totalDBs)

			return nil
		},
	}

	return cmd
}
