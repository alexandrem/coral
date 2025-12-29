package debug

import (
	"github.com/spf13/cobra"
)

// NewDebugCmd creates the root debug command.
func NewDebugCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "debug",
		Short: "Debug running services",
		Long: `Debug running services using eBPF uprobes and other tools.

Function-level debugging commands:
  attach   - Attach uprobe to function
  profile  - Auto-profile multiple functions
  search   - Search for functions
  info     - Get function details
  trace    - Trace request path
  session  - Manage debug sessions
  query    - Query debug results

For CPU and memory profiling, use 'coral profile' and 'coral query' commands.`,
	}

	// Instrumentation
	cmd.AddCommand(NewAttachCmd())
	cmd.AddCommand(NewProfileCmd())

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
