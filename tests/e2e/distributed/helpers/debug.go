package helpers

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
)

// AttachUprobe attaches a uprobe to a function for debugging.
func AttachUprobe(
	ctx context.Context,
	client colonyv1connect.ColonyDebugServiceClient,
	agentID string,
	serviceName string,
	functionName string,
	durationSeconds int32,
) (*colonyv1.AttachUprobeResponse, error) {
	req := connect.NewRequest(&colonyv1.AttachUprobeRequest{
		AgentId:      agentID,
		ServiceName:  serviceName,
		FunctionName: functionName,
		Duration:     &durationpb.Duration{Seconds: int64(durationSeconds)},
	})

	resp, err := client.AttachUprobe(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to attach uprobe: %w", err)
	}

	return resp.Msg, nil
}

// DetachUprobe detaches a uprobe and retrieves final events.
func DetachUprobe(
	ctx context.Context,
	client colonyv1connect.ColonyDebugServiceClient,
	sessionID string,
) (*colonyv1.DetachUprobeResponse, error) {
	req := connect.NewRequest(&colonyv1.DetachUprobeRequest{
		SessionId: sessionID,
	})

	resp, err := client.DetachUprobe(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to detach uprobe: %w", err)
	}

	return resp.Msg, nil
}

// GetDebugResults retrieves aggregated debug results including call trees.
func GetDebugResults(
	ctx context.Context,
	client colonyv1connect.ColonyDebugServiceClient,
	sessionID string,
) (*colonyv1.GetDebugResultsResponse, error) {
	req := connect.NewRequest(&colonyv1.GetDebugResultsRequest{
		SessionId: sessionID,
	})

	resp, err := client.GetDebugResults(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get debug results: %w", err)
	}

	return resp.Msg, nil
}

// ProfileCPU performs on-demand CPU profiling.
func ProfileCPU(
	ctx context.Context,
	client colonyv1connect.ColonyDebugServiceClient,
	agentID string,
	serviceName string,
	durationSeconds int32,
	frequencyHz int32,
) (*colonyv1.ProfileCPUResponse, error) {
	req := connect.NewRequest(&colonyv1.ProfileCPURequest{
		AgentId:         agentID,
		ServiceName:     serviceName,
		DurationSeconds: durationSeconds,
		FrequencyHz:     frequencyHz,
	})

	resp, err := client.ProfileCPU(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to profile CPU: %w", err)
	}

	return resp.Msg, nil
}
