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
		Use:     "list-agents",
		Aliases: []string{"list"},
		Short:   "List agents with available databases",
		Long: `Lists all agents registered with the colony and shows available databases
for querying. This includes telemetry databases, Beyla metrics, and any other
DuckDB databases registered by agents.

Examples:
  coral duckdb list-agents
  coral duckdb list

Output includes:
  - Agent ID
  - Status (healthy/degraded/unhealthy)
  - Last seen timestamp
  - Available databases (comma-separated)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Get agents from colony with database information.
			agents, err := listAgents(ctx, true) // Fetch databases for each agent.
			if err != nil {
				return fmt.Errorf("failed to list agents: %w", err)
			}

			if len(agents) == 0 {
				fmt.Println("No agents registered with colony")
				return nil
			}

			// Print agents in a table.
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "AGENT ID\tSTATUS\tLAST SEEN\tDATABASES")
			fmt.Fprintln(w, "--------\t------\t---------\t---------")

			for _, agent := range agents {
				databases := "-"
				if len(agent.Databases) > 0 {
					databases = strings.Join(agent.Databases, ", ")
				}

				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					agent.AgentID,
					agent.Status,
					agent.LastSeen,
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

			fmt.Printf("\nTotal: %d agents (%d with databases, %d total databases)\n", len(agents), agentsWithDBs, totalDBs)

			return nil
		},
	}

	return cmd
}
