package debug

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
)

func NewDetachCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "detach <session-id>",
		Short: "Stop a debug session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			ctx := context.Background()

			// Resolve colony URL from config
			colonyAddr, err := getColonyURL()
			if err != nil {
				return fmt.Errorf("failed to resolve colony address: %w", err)
			}

			client := colonyv1connect.NewDebugServiceClient(
				http.DefaultClient,
				colonyAddr,
			)

			fmt.Printf("Detaching session %s...\n", sessionID)

			req := &colonypb.DetachUprobeRequest{
				SessionId: sessionID,
			}

			resp, err := client.DetachUprobe(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to detach session: %w", err)
			}

			if !resp.Msg.Success {
				return fmt.Errorf("failed to detach session: %s", resp.Msg.Error)
			}

			fmt.Println("✓ Debug session detached")
			return nil
		},
	}

	return cmd
}
