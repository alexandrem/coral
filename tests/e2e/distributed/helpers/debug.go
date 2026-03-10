package helpers

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
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

// AttachUprobeWithFilter attaches a uprobe with a kernel-level filter (RFD 090).
func AttachUprobeWithFilter(
	ctx context.Context,
	client colonyv1connect.ColonyDebugServiceClient,
	agentID string,
	serviceName string,
	functionName string,
	durationSeconds int32,
	filter *agentv1.UprobeFilter,
) (*colonyv1.AttachUprobeResponse, error) {
	req := connect.NewRequest(&colonyv1.AttachUprobeRequest{
		AgentId:      agentID,
		ServiceName:  serviceName,
		FunctionName: functionName,
		Duration:     &durationpb.Duration{Seconds: int64(durationSeconds)},
		Filter:       filter,
	})

	resp, err := client.AttachUprobe(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to attach uprobe with filter: %w", err)
	}

	return resp.Msg, nil
}

// UpdateProbeFilter updates the kernel-level filter for an active debug session (RFD 090).
func UpdateProbeFilter(
	ctx context.Context,
	client colonyv1connect.ColonyDebugServiceClient,
	sessionID string,
	filter *agentv1.UprobeFilter,
) (*colonyv1.UpdateProbeFilterResponse, error) {
	req := connect.NewRequest(&colonyv1.UpdateProbeFilterRequest{
		SessionId: sessionID,
		Filter:    filter,
	})

	resp, err := client.UpdateProbeFilter(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to update probe filter: %w", err)
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

// ProfileMemory performs on-demand memory profiling (RFD 077).
func ProfileMemory(
	ctx context.Context,
	client colonyv1connect.ColonyDebugServiceClient,
	serviceName string,
	durationSeconds int32,
) (*colonyv1.ProfileMemoryResponse, error) {
	req := connect.NewRequest(&colonyv1.ProfileMemoryRequest{
		ServiceName:     serviceName,
		DurationSeconds: durationSeconds,
	})

	resp, err := client.ProfileMemory(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to profile memory: %w", err)
	}

	return resp.Msg, nil
}

// ProfileCPU performs on-demand CPU profiling.
func ProfileCPU(
	ctx context.Context,
	client colonyv1connect.ColonyDebugServiceClient,
	serviceName string,
	durationSeconds int32,
	frequencyHz int32,
) (*colonyv1.ProfileCPUResponse, error) {
	req := connect.NewRequest(&colonyv1.ProfileCPURequest{
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

// DeployCorrelation deploys a correlation descriptor via the colony (RFD 091).
func DeployCorrelation(
	ctx context.Context,
	client colonyv1connect.ColonyDebugServiceClient,
	serviceName string,
	descriptor *agentv1.CorrelationDescriptor,
) (*colonyv1.ColonyDeployCorrelationResponse, error) {
	req := connect.NewRequest(&colonyv1.ColonyDeployCorrelationRequest{
		ServiceName: serviceName,
		Descriptor_: descriptor,
	})

	resp, err := client.DeployCorrelation(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to deploy correlation: %w", err)
	}

	return resp.Msg, nil
}

// RemoveCorrelation removes an active correlation descriptor (RFD 091).
func RemoveCorrelation(
	ctx context.Context,
	client colonyv1connect.ColonyDebugServiceClient,
	correlationID string,
	serviceName string,
) (*colonyv1.ColonyRemoveCorrelationResponse, error) {
	req := connect.NewRequest(&colonyv1.ColonyRemoveCorrelationRequest{
		CorrelationId: correlationID,
		ServiceName:   serviceName,
	})

	resp, err := client.RemoveCorrelation(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to remove correlation: %w", err)
	}

	return resp.Msg, nil
}

// ListCorrelations returns active correlation descriptors from all agents (RFD 091).
func ListCorrelations(
	ctx context.Context,
	client colonyv1connect.ColonyDebugServiceClient,
	serviceName string,
) (*colonyv1.ColonyListCorrelationsResponse, error) {
	req := connect.NewRequest(&colonyv1.ColonyListCorrelationsRequest{
		ServiceName: serviceName,
	})

	resp, err := client.ListCorrelations(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list correlations: %w", err)
	}

	return resp.Msg, nil
}

// QueryMemoryProfileSamples queries the agent's continuous memory profile storage.
func QueryMemoryProfileSamples(
	ctx context.Context,
	client agentv1connect.AgentDebugServiceClient,
	serviceName string,
) (*agentv1.QueryMemoryProfileSamplesResponse, error) {
	req := connect.NewRequest(&agentv1.QueryMemoryProfileSamplesRequest{
		ServiceName: serviceName,
		StartSeqId:  0,
		MaxRecords:  5000,
	})

	resp, err := client.QueryMemoryProfileSamples(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to query memory profile samples: %w", err)
	}

	return resp.Msg, nil
}
