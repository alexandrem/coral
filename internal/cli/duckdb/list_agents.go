package duckdb

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// NewListAgentsCmd creates the list-agents subcommand.
func NewListAgentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list-agents",
		Short: "List agents with Beyla metrics enabled",
		Long: `Lists all agents registered with the colony and indicates which have
Beyla metrics available for querying.

Example:
  coral duckdb list-agents

Output includes:
  - Agent ID
  - Status (healthy/degraded/unhealthy)
  - Last seen timestamp
  - Beyla enabled (yes/no)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Get agents from colony.
			agents, err := listAgents(ctx)
			if err != nil {
				return fmt.Errorf("failed to list agents: %w", err)
			}

			if len(agents) == 0 {
				fmt.Println("No agents registered with colony")
				return nil
			}

			// Print agents in a table.
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "AGENT ID\tSTATUS\tLAST SEEN\tBEYLA ENABLED")
			fmt.Fprintln(w, "--------\t------\t---------\t-------------")

			for _, agent := range agents {
				beylaStatus := "no"
				if agent.BeylaEnabled {
					beylaStatus = "yes"
				}

				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					agent.AgentID,
					agent.Status,
					agent.LastSeen,
					beylaStatus,
				)
			}

			w.Flush()

			// Print summary.
			beylaCount := 0
			for _, agent := range agents {
				if agent.BeylaEnabled {
					beylaCount++
				}
			}

			fmt.Printf("\nTotal: %d agents (%d with Beyla enabled)\n", len(agents), beylaCount)

			return nil
		},
	}

	return cmd
}
