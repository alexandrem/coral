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

	// Instrumentation
	cmd.AddCommand(NewAttachCmd())
	cmd.AddCommand(NewProfileCmd())
	cmd.AddCommand(NewCPUProfileCmd())

	// Session Management
	cmd.AddCommand(NewSessionCmd())

	// Discovery
	cmd.AddCommand(NewSearchCmd())
	cmd.AddCommand(NewInfoCmd())

	// Other
	cmd.AddCommand(NewTraceCmd())
	cmd.AddCommand(NewQueryCmd())

	return cmd
}
