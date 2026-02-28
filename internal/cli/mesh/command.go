// Package mesh provides CLI commands for managing and troubleshooting the Coral control mesh.
package mesh

import (
	"github.com/spf13/cobra"
)

// NewMeshCmd creates the mesh command and its subcommands.
func NewMeshCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mesh",
		Short: "Manage and troubleshoot the Coral control mesh",
		Long: `Troubleshoot the WireGuard-based control mesh that connects agents to the colony.
Verify user-space cryptography and routing independently of kernel ICMP filtering.`,
	}

	cmd.AddCommand(newPingCmd())

	return cmd
}
