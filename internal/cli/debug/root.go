package debug

import (
	"github.com/spf13/cobra"
)

// NewDebugCmd creates the root debug command.
func NewDebugCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "debug",
		Short: "Debug running services",
		Long:  `Debug running services using eBPF uprobes and other tools.`,
	}

	cmd.AddCommand(NewAttachCmd())
	cmd.AddCommand(NewDetachCmd())
	cmd.AddCommand(NewListCmd())
	cmd.AddCommand(NewEventsCmd())
	cmd.AddCommand(NewTraceCmd())
	cmd.AddCommand(NewQueryCmd())

	return cmd
}
