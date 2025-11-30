package debug

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/durationpb"
)

func NewTraceCmd() *cobra.Command {
	var (
		path       string
		duration   time.Duration
		colonyAddr string
	)

	cmd := &cobra.Command{
		Use:   "trace <service>",
		Short: "Trace request path",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			serviceName := args[0]
			ctx := context.Background()

			client := colonyv1connect.NewDebugServiceClient(
				http.DefaultClient,
				colonyAddr,
			)

			fmt.Printf("Tracing %s on %s for %s...\n", path, serviceName, duration)

			req := &colonypb.TraceRequestPathRequest{
				ServiceName: serviceName,
				Path:        path,
				Duration:    durationpb.New(duration),
			}

			resp, err := client.TraceRequestPath(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to start trace: %w", err)
			}

			if !resp.Msg.Success {
				return fmt.Errorf("failed to start trace: %s", resp.Msg.Error)
			}

			fmt.Printf("✓ Trace session started (id: %s)\n", resp.Msg.SessionId)
			fmt.Println("  Use 'coral debug list' to check status.")
			return nil
		},
	}

	cmd.Flags().StringVarP(&path, "path", "p", "", "HTTP path to trace (required)")
	cmd.Flags().DurationVarP(&duration, "duration", "d", 60*time.Second, "Duration of the trace session")
	cmd.Flags().StringVar(&colonyAddr, "colony-addr", "http://localhost:8081", "Colony address")

	cmd.MarkFlagRequired("path")

	return cmd
}
