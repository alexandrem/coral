package debug

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/durationpb"
)

func NewAttachCmd() *cobra.Command {
	var (
		functionName  string
		duration      time.Duration
		captureArgs   bool
		captureReturn bool
		sampleRate    uint32
		agentID       string
		sdkAddr       string
		colonyAddr    string
	)

	cmd := &cobra.Command{
		Use:   "attach <service>",
		Short: "Attach uprobe to function",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			serviceName := args[0]
			ctx := context.Background()

			// Create Colony client
			client := colonyv1connect.NewDebugServiceClient(
				http.DefaultClient,
				colonyAddr,
			)

			fmt.Printf("Attaching uprobe to %s/%s...\n", serviceName, functionName)

			req := &colonypb.AttachUprobeRequest{
				ServiceName:  serviceName,
				FunctionName: functionName,
				Duration:     durationpb.New(duration),
				Config: &meshv1.UprobeConfig{
					CaptureArgs:   captureArgs,
					CaptureReturn: captureReturn,
					SampleRate:    sampleRate,
				},
				AgentId: agentID,
				SdkAddr: sdkAddr,
			}

			resp, err := client.AttachUprobe(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to attach uprobe: %w", err)
			}

			if !resp.Msg.Success {
				return fmt.Errorf("failed to attach uprobe: %s", resp.Msg.Error)
			}

			fmt.Printf("✓ Debug session started\n")
			fmt.Printf("  Session ID: %s\n", resp.Msg.SessionId)
			fmt.Printf("  Expires at: %s\n", resp.Msg.ExpiresAt.AsTime().Format(time.RFC3339))

			return nil
		},
	}

	cmd.Flags().StringVarP(&functionName, "function", "f", "", "Function name to trace (required)")
	cmd.Flags().DurationVarP(&duration, "duration", "d", 60*time.Second, "Duration of the debug session")
	cmd.Flags().BoolVar(&captureArgs, "capture-args", false, "Capture function arguments")
	cmd.Flags().BoolVar(&captureReturn, "capture-return", false, "Capture return values")
	cmd.Flags().Uint32Var(&sampleRate, "sample-rate", 0, "Sample rate (0 = all calls)")
	cmd.Flags().StringVar(&agentID, "agent-id", "", "Agent ID (manual override)")
	cmd.Flags().StringVar(&sdkAddr, "sdk-addr", "", "SDK address (manual override)")
	cmd.Flags().StringVar(&colonyAddr, "colony-addr", "http://localhost:8081", "Colony address")

	cmd.MarkFlagRequired("function")

	return cmd
}
