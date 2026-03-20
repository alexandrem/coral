package startup

// adapters.go bridges the transport-agnostic agent services to the Connect RPC
// handler interfaces. Keeping this translation layer here ensures the agent
// package has no dependency on connectrpc.com/connect.

import (
	"context"

	"connectrpc.com/connect"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/agent"
)

// debugServiceAdapter wraps agent.DebugService to satisfy the Connect RPC handler interface.
type debugServiceAdapter struct {
	service *agent.DebugService
}

func (a *debugServiceAdapter) StartUprobeCollector(
	ctx context.Context,
	req *connect.Request[agentv1.StartUprobeCollectorRequest],
) (*connect.Response[agentv1.StartUprobeCollectorResponse], error) {
	resp, err := a.service.StartUprobeCollector(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *debugServiceAdapter) StopUprobeCollector(
	ctx context.Context,
	req *connect.Request[agentv1.StopUprobeCollectorRequest],
) (*connect.Response[agentv1.StopUprobeCollectorResponse], error) {
	resp, err := a.service.StopUprobeCollector(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *debugServiceAdapter) QueryUprobeEvents(
	ctx context.Context,
	req *connect.Request[agentv1.QueryUprobeEventsRequest],
) (*connect.Response[agentv1.QueryUprobeEventsResponse], error) {
	resp, err := a.service.QueryUprobeEvents(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *debugServiceAdapter) UpdateProbeFilter(
	ctx context.Context,
	req *connect.Request[agentv1.UpdateProbeFilterRequest],
) (*connect.Response[agentv1.UpdateProbeFilterResponse], error) {
	resp, err := a.service.UpdateProbeFilter(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *debugServiceAdapter) ProfileCPU(
	ctx context.Context,
	req *connect.Request[agentv1.ProfileCPUAgentRequest],
) (*connect.Response[agentv1.ProfileCPUAgentResponse], error) {
	resp, err := a.service.ProfileCPU(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *debugServiceAdapter) QueryCPUProfileSamples(
	ctx context.Context,
	req *connect.Request[agentv1.QueryCPUProfileSamplesRequest],
) (*connect.Response[agentv1.QueryCPUProfileSamplesResponse], error) {
	resp, err := a.service.QueryCPUProfileSamples(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *debugServiceAdapter) ProfileMemory(
	ctx context.Context,
	req *connect.Request[agentv1.ProfileMemoryAgentRequest],
) (*connect.Response[agentv1.ProfileMemoryAgentResponse], error) {
	resp, err := a.service.ProfileMemory(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *debugServiceAdapter) QueryMemoryProfileSamples(
	ctx context.Context,
	req *connect.Request[agentv1.QueryMemoryProfileSamplesRequest],
) (*connect.Response[agentv1.QueryMemoryProfileSamplesResponse], error) {
	resp, err := a.service.QueryMemoryProfileSamples(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *debugServiceAdapter) DeployCorrelation(
	ctx context.Context,
	req *connect.Request[agentv1.DeployCorrelationRequest],
) (*connect.Response[agentv1.DeployCorrelationResponse], error) {
	resp, err := a.service.DeployCorrelation(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *debugServiceAdapter) RemoveCorrelation(
	ctx context.Context,
	req *connect.Request[agentv1.RemoveCorrelationRequest],
) (*connect.Response[agentv1.RemoveCorrelationResponse], error) {
	resp, err := a.service.RemoveCorrelation(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *debugServiceAdapter) ListCorrelations(
	ctx context.Context,
	req *connect.Request[agentv1.ListCorrelationsRequest],
) (*connect.Response[agentv1.ListCorrelationsResponse], error) {
	resp, err := a.service.ListCorrelations(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

// runtimeServiceAdapter wraps agent.RuntimeService to satisfy the Connect RPC handler interface.
// Only used by ServiceHandler.GetRuntimeContext via delegation; registered separately here
// for symmetry and to keep all transport adaptation in one place.
type runtimeServiceAdapter struct {
	service *agent.RuntimeService
}

func (a *runtimeServiceAdapter) GetRuntimeContext(
	ctx context.Context,
	req *connect.Request[agentv1.GetRuntimeContextRequest],
) (*connect.Response[agentv1.RuntimeContextResponse], error) {
	resp, err := a.service.GetRuntimeContext(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}
