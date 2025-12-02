package debug

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/durationpb"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
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
		format        string
	)

	cmd := &cobra.Command{
		Use:   "attach <service>",
		Short: "Attach uprobe to function",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			serviceName := args[0]
			ctx := context.Background()

			// Resolve colony URL from config
			colonyAddr, err := getColonyURL()
			if err != nil {
				return fmt.Errorf("failed to resolve colony address: %w\n\nMake sure the colony is configured and running", err)
			}

			// Create Colony client
			client := colonyv1connect.NewDebugServiceClient(
				http.DefaultClient,
				colonyAddr,
			)

			// Show progress indicator (simple version without spinner library for now)
			if format == "text" {
				fmt.Printf("🔍 Attaching uprobe to %s/%s...\n", serviceName, functionName)
			}

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
				// Check if this is a connection error (colony not running)
				if connect.CodeOf(err) == connect.CodeUnavailable {
					return fmt.Errorf("colony is not reachable at %s\n"+
						"Please ensure the colony is running with: bin/coral colony start\n"+
						"Original error: %w", colonyAddr, err)
				}
				return fmt.Errorf("failed to attach uprobe: %w", err)
			}

			if !resp.Msg.Success {
				return fmt.Errorf("failed to attach uprobe: %s", resp.Msg.Error)
			}

			// Format and print output
			formatter := NewFormatter(OutputFormat(format))
			output, err := formatter.FormatAttachResponse(resp.Msg)
			if err != nil {
				return fmt.Errorf("failed to format output: %w", err)
			}

			if err := WriteOutput(os.Stdout, output); err != nil {
				return fmt.Errorf("failed to write output: %w", err)
			}

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
	cmd.Flags().StringVar(&format, "format", "text", "Output format (text, json, csv)")

	if err := cmd.MarkFlagRequired("function"); err != nil {
		fmt.Printf("failed to mark flag as required: %v\n", err)
	}

	return cmd
}
