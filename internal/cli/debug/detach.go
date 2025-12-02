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
	var colonyAddr string

	cmd := &cobra.Command{
		Use:   "detach <session-id>",
		Short: "Stop a debug session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			ctx := context.Background()

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

	cmd.Flags().StringVar(&colonyAddr, "colony-addr", "http://localhost:8081", "Colony address")

	return cmd
}
