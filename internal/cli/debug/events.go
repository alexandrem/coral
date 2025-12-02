package debug

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
)

func NewEventsCmd() *cobra.Command {
	var (
		maxEvents int32
		follow    bool
		since     time.Duration
	)

	cmd := &cobra.Command{
		Use:   "events <session-id>",
		Short: "Query events from a debug session",
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

			var startTime *timestamppb.Timestamp
			if since > 0 {
				startTime = timestamppb.New(time.Now().Add(-since))
			}

			for {
				req := &colonypb.QueryUprobeEventsRequest{
					SessionId: sessionID,
					MaxEvents: maxEvents,
					StartTime: startTime,
				}

				resp, err := client.QueryUprobeEvents(ctx, connect.NewRequest(req))
				if err != nil {
					return fmt.Errorf("failed to query events: %w", err)
				}

				for _, event := range resp.Msg.Events {
					// Update start time for next poll to avoid duplicates
					// In a real implementation, we'd use a cursor or ID
					if startTime == nil || event.Timestamp.AsTime().After(startTime.AsTime()) {
						startTime = event.Timestamp
					}

					// Print event
					data, _ := json.Marshal(event)
					fmt.Println(string(data))
				}

				if !follow {
					break
				}

				time.Sleep(1 * time.Second)
			}

			return nil
		},
	}

	cmd.Flags().Int32Var(&maxEvents, "max", 100, "Max events to retrieve")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow new events")
	cmd.Flags().DurationVar(&since, "since", 0, "Show events since duration (e.g. 5m)")

	return cmd
}
