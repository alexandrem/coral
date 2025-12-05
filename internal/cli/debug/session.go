package debug

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
)

func NewSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage debug sessions",
		Long:  "Manage debug sessions, including listing, inspecting, and stopping them.",
	}

	cmd.AddCommand(newSessionListCmd())
	cmd.AddCommand(newSessionGetCmd())
	cmd.AddCommand(newSessionEventsCmd())
	cmd.AddCommand(newSessionStopCmd())

	return cmd
}

func newSessionListCmd() *cobra.Command {
	var (
		serviceName string
		status      string
		format      string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List active debug sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			colonyAddr, err := getColonyURL()
			if err != nil {
				return fmt.Errorf("failed to resolve colony address: %w", err)
			}

			client := colonyv1connect.NewDebugServiceClient(http.DefaultClient, colonyAddr)

			req := &colonypb.ListDebugSessionsRequest{
				ServiceName: serviceName,
				Status:      status,
			}

			resp, err := client.ListDebugSessions(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to list sessions: %w", err)
			}

			formatter := NewFormatter(OutputFormat(format))
			output, err := formatter.FormatSessions(resp.Msg.Sessions)
			if err != nil {
				return fmt.Errorf("failed to format output: %w", err)
			}

			return WriteOutput(os.Stdout, output)
		},
	}

	cmd.Flags().StringVarP(&serviceName, "service", "s", "", "Filter by service name")
	cmd.Flags().StringVar(&status, "status", "", "Filter by status")
	cmd.Flags().StringVar(&format, "format", "text", "Output format (text, json, csv)")

	return cmd
}

func newSessionGetCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "get <session-id>",
		Short: "Get session metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			ctx := context.Background()
			colonyAddr, err := getColonyURL()
			if err != nil {
				return fmt.Errorf("failed to resolve colony address: %w", err)
			}

			client := colonyv1connect.NewDebugServiceClient(http.DefaultClient, colonyAddr)

			// We have to list all sessions and filter because there is no GetSession RPC
			req := &colonypb.ListDebugSessionsRequest{}
			resp, err := client.ListDebugSessions(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to list sessions: %w", err)
			}

			var session *colonypb.DebugSession
			for _, s := range resp.Msg.Sessions {
				if s.SessionId == sessionID {
					session = s
					break
				}
			}

			if session == nil {
				return fmt.Errorf("session not found: %s", sessionID)
			}

			formatter := NewFormatter(OutputFormat(format))
			output, err := formatter.FormatSessions([]*colonypb.DebugSession{session})
			if err != nil {
				return fmt.Errorf("failed to format output: %w", err)
			}

			return WriteOutput(os.Stdout, output)
		},
	}

	cmd.Flags().StringVar(&format, "format", "text", "Output format (text, json, csv)")

	return cmd
}

func newSessionEventsCmd() *cobra.Command {
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
			colonyAddr, err := getColonyURL()
			if err != nil {
				return fmt.Errorf("failed to resolve colony address: %w", err)
			}

			client := colonyv1connect.NewDebugServiceClient(http.DefaultClient, colonyAddr)

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
					if startTime == nil || event.Timestamp.AsTime().After(startTime.AsTime()) {
						startTime = event.Timestamp
					}
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

func newSessionStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop <session-id>",
		Short: "Stop a debug session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			ctx := context.Background()
			colonyAddr, err := getColonyURL()
			if err != nil {
				return fmt.Errorf("failed to resolve colony address: %w", err)
			}

			client := colonyv1connect.NewDebugServiceClient(http.DefaultClient, colonyAddr)

			fmt.Printf("Stopping session %s...\n", sessionID)

			req := &colonypb.DetachUprobeRequest{
				SessionId: sessionID,
			}

			resp, err := client.DetachUprobe(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to stop session: %w", err)
			}

			if !resp.Msg.Success {
				return fmt.Errorf("failed to stop session: %s", resp.Msg.Error)
			}

			fmt.Println("✓ Debug session stopped")
			return nil
		},
	}

	return cmd
}
