package colony

import (
	"github.com/spf13/cobra"
)

// NewColonyCmd creates the colony command and its subcommands
func NewColonyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "colony",
		Short: "Manage the Coral colony (central brain)",
		Long: `The colony is the central brain of your Coral deployment.
It aggregates observations from agents, runs AI analysis, and provides insights.`,
	}

	cmd.AddCommand(newStartCmd())
	cmd.AddCommand(newStopCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newAgentsCmd())
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newUseCmd())
	cmd.AddCommand(newCurrentCmd())
	cmd.AddCommand(newExportCmd())
	cmd.AddCommand(newImportCmd())
	cmd.AddCommand(newMCPCmd())
	cmd.AddCommand(newServiceCmd())   // RFD 052 - Service-centric CLI.
	cmd.AddCommand(NewCACmd())        // RFD 047 - CA management commands.
	cmd.AddCommand(NewPSKCmd())       // RFD 088 - Bootstrap PSK management.
	cmd.AddCommand(newTokenCmd())     // RFD 031 - API token management for public endpoint.
	cmd.AddCommand(newAddRemoteCmd()) // RFD 031 - Add remote colony connection.

	return cmd
}
