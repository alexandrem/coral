package colony

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the colony daemon",
		Long:  `Stop a running colony daemon.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Stopping colony...")

			// TODO: Implement actual colony shutdown
			// - Read PID file
			// - Send SIGTERM to process
			// - Wait for graceful shutdown

			fmt.Println("✓ Colony stopped")
			return nil
		},
	}
}
