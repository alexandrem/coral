package agent

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/agent/profiler"
	"github.com/rs/zerolog"
)

// DebugService implements debug-related RPC handlers for the agent.
type DebugService struct {
	agent  *Agent
	logger zerolog.Logger
}

// NewDebugService creates a new debug service.
func NewDebugService(agent *Agent, logger zerolog.Logger) *DebugService {
	return &DebugService{
		agent:  agent,
		logger: logger.With().Str("component", "debug_service").Logger(),
	}
}

// StartUprobeCollector handles requests to start uprobe collectors.
func (s *DebugService) StartUprobeCollector(
	ctx context.Context,
	req *agentv1.StartUprobeCollectorRequest,
) (*agentv1.StartUprobeCollectorResponse, error) {
	s.logger.Info().
		Str("service", req.ServiceName).
		Str("function", req.FunctionName).
		Msg("Starting uprobe collector")

	// Get SDK address from service registry or config
	// For now, we'll require it in the request config
	sdkAddr := req.SdkAddr
	if sdkAddr == "" {
		// Attempt to resolve using agent discovery
		resolved, err := s.agent.ResolveSDK(req.ServiceName)
		if err != nil {
			return &agentv1.StartUprobeCollectorResponse{
				Supported: false,
				Error:     fmt.Sprintf("failed to resolve sdk_addr: %v", err),
			}, nil
		}
		sdkAddr = resolved
	}

	// Build config map for eBPF manager
	config := map[string]string{
		"service_name":  req.ServiceName,
		"function_name": req.FunctionName,
		"sdk_addr":      sdkAddr,
	}

	if req.Config != nil {
		if req.Config.CaptureArgs {
			config["capture_args"] = "true"
		}
		if req.Config.CaptureReturn {
			config["capture_return"] = "true"
		}
		if req.Config.SampleRate > 0 {
			config["sample_rate"] = fmt.Sprintf("%d", req.Config.SampleRate)
		}
		if req.Config.MaxEvents > 0 {
			config["max_events"] = fmt.Sprintf("%d", req.Config.MaxEvents)
		}
	}

	// Forward to eBPF manager
	ebpfReq := &meshv1.StartEbpfCollectorRequest{
		AgentId:     req.AgentId,
		ServiceName: req.ServiceName,
		Kind:        agentv1.EbpfCollectorKind_EBPF_COLLECTOR_KIND_UPROBE,
		Duration:    req.Duration,
		Config:      config,
	}

	resp, err := s.agent.ebpfManager.StartCollector(ctx, ebpfReq)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to start uprobe collector")
		return &agentv1.StartUprobeCollectorResponse{
			Supported: true,
			Error:     fmt.Sprintf("failed to start collector: %v", err),
		}, nil
	}

	return &agentv1.StartUprobeCollectorResponse{
		CollectorId: resp.CollectorId,
		ExpiresAt:   resp.ExpiresAt,
		Supported:   resp.Supported,
		Error:       resp.Error,
	}, nil
}

// StopUprobeCollector handles requests to stop uprobe collectors.
func (s *DebugService) StopUprobeCollector(
	ctx context.Context,
	req *agentv1.StopUprobeCollectorRequest,
) (*agentv1.StopUprobeCollectorResponse, error) {
	s.logger.Info().
		Str("collector_id", req.CollectorId).
		Msg("Stopping uprobe collector")

	err := s.agent.ebpfManager.StopCollector(req.CollectorId)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to stop uprobe collector")
		return &agentv1.StopUprobeCollectorResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return &agentv1.StopUprobeCollectorResponse{
		Success: true,
	}, nil
}

// QueryUprobeEvents handles requests to query collected uprobe events.
func (s *DebugService) QueryUprobeEvents(
	ctx context.Context,
	req *agentv1.QueryUprobeEventsRequest,
) (*agentv1.QueryUprobeEventsResponse, error) {
	s.logger.Debug().
		Str("collector_id", req.CollectorId).
		Msg("Querying uprobe events")

	// Get events from eBPF manager
	events, err := s.agent.ebpfManager.GetEvents(req.CollectorId)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to get uprobe events")
		return nil, fmt.Errorf("failed to get events: %w", err)
	}

	// Filter events by time range if specified
	var filteredEvents []*agentv1.UprobeEvent
	for _, event := range events {
		// Extract UprobeEvent from EbpfEvent payload
		uprobeEvent, ok := event.Payload.(*meshv1.EbpfEvent_UprobeEvent)
		if !ok {
			continue // Skip non-uprobe events
		}

		// Check time range
		if req.StartTime != nil && uprobeEvent.UprobeEvent.Timestamp.AsTime().Before(req.StartTime.AsTime()) {
			continue
		}
		if req.EndTime != nil && uprobeEvent.UprobeEvent.Timestamp.AsTime().After(req.EndTime.AsTime()) {
			continue
		}

		filteredEvents = append(filteredEvents, uprobeEvent.UprobeEvent)

		// Check max events limit
		if req.MaxEvents > 0 && len(filteredEvents) >= int(req.MaxEvents) {
			break
		}
	}

	return &agentv1.QueryUprobeEventsResponse{
		Events:  filteredEvents,
		HasMore: len(events) > len(filteredEvents),
	}, nil
}

// ProfileCPU handles requests to collect CPU profile samples (RFD 070).
func (s *DebugService) ProfileCPU(
	ctx context.Context,
	req *agentv1.ProfileCPUAgentRequest,
) (*agentv1.ProfileCPUAgentResponse, error) {
	s.logger.Info().
		Str("service", req.ServiceName).
		Int32("pid", req.Pid).
		Int32("duration_seconds", req.DurationSeconds).
		Int32("frequency_hz", req.FrequencyHz).
		Msg("Starting CPU profiling")

	// Import the debug package to use CPU profiler
	profiler := s.agent.debugManager
	if profiler == nil {
		return &agentv1.ProfileCPUAgentResponse{
			Success: false,
			Error:   "debug manager not initialized",
		}, nil
	}

	// Use the ProfileCPU method from the SessionManager
	result, err := profiler.ProfileCPU(int(req.Pid), int(req.DurationSeconds), int(req.FrequencyHz))
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to collect CPU profile")
		return &agentv1.ProfileCPUAgentResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to profile CPU: %v", err),
		}, nil
	}

	// Convert result to protobuf response
	var samples []*agentv1.StackSample
	for _, sample := range result.Samples {
		samples = append(samples, &agentv1.StackSample{
			FrameNames: sample.FrameNames,
			Count:      sample.Count,
		})
	}

	return &agentv1.ProfileCPUAgentResponse{
		Samples:      samples,
		TotalSamples: result.TotalSamples,
		LostSamples:  result.LostSamples,
		Success:      true,
	}, nil
}

// QueryCPUProfileSamples handles requests to query historical CPU profile samples (RFD 072).
func (s *DebugService) QueryCPUProfileSamples(
	ctx context.Context,
	req *agentv1.QueryCPUProfileSamplesRequest,
) (*agentv1.QueryCPUProfileSamplesResponse, error) {
	s.logger.Debug().
		Str("service", req.ServiceName).
		Str("pod", req.PodName).
		Msg("Querying CPU profile samples")

	// Get the continuous profiler from the agent
	cpuProfiler, ok := s.agent.continuousProfiler.(*profiler.ContinuousCPUProfiler)
	if !ok || cpuProfiler == nil {
		return &agentv1.QueryCPUProfileSamplesResponse{
			Error: "continuous profiling not enabled",
		}, nil
	}

	storageIface := cpuProfiler.GetStorage()
	storage, ok := storageIface.(*profiler.Storage)
	if !ok {
		return &agentv1.QueryCPUProfileSamplesResponse{
			Error: "invalid storage type",
		}, nil
	}

	// Query samples from storage
	startTime := req.StartTime.AsTime()
	endTime := req.EndTime.AsTime()

	samples, err := storage.QuerySamples(ctx, startTime, endTime, req.ServiceName)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to query CPU profile samples")
		return &agentv1.QueryCPUProfileSamplesResponse{
			Error: fmt.Sprintf("failed to query samples: %v", err),
		}, nil
	}

	// Convert samples to protobuf response
	var pbSamples []*agentv1.CPUProfileSample
	totalSamples := uint64(0)

	for _, sample := range samples {
		// Decode stack frame IDs to frame names
		frameNames, err := storage.DecodeStackFrames(ctx, sample.StackFrameIDs)
		if err != nil {
			s.logger.Warn().Err(err).Msg("Failed to decode stack frames")
			continue
		}

		pbSamples = append(pbSamples, &agentv1.CPUProfileSample{
			Timestamp:   timestamppb.New(sample.Timestamp),
			BuildId:     sample.BuildID,
			StackFrames: frameNames,
			SampleCount: sample.SampleCount, // uint32 -> uint32
			ServiceName: sample.ServiceID,   // RFD 072: Include service name
		})

		totalSamples += uint64(sample.SampleCount)
	}

	return &agentv1.QueryCPUProfileSamplesResponse{
		Samples:      pbSamples,
		TotalSamples: totalSamples,
	}, nil
}

// DebugServiceAdapter adapts DebugService to the Connect RPC handler interface.
type DebugServiceAdapter struct {
	service *DebugService
}

// NewDebugServiceAdapter creates a new adapter for the debug service.
func NewDebugServiceAdapter(service *DebugService) *DebugServiceAdapter {
	return &DebugServiceAdapter{service: service}
}

// StartUprobeCollector implements the Connect RPC handler interface.
func (a *DebugServiceAdapter) StartUprobeCollector(
	ctx context.Context,
	req *connect.Request[agentv1.StartUprobeCollectorRequest],
) (*connect.Response[agentv1.StartUprobeCollectorResponse], error) {
	resp, err := a.service.StartUprobeCollector(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

// StopUprobeCollector implements the Connect RPC handler interface.
func (a *DebugServiceAdapter) StopUprobeCollector(
	ctx context.Context,
	req *connect.Request[agentv1.StopUprobeCollectorRequest],
) (*connect.Response[agentv1.StopUprobeCollectorResponse], error) {
	resp, err := a.service.StopUprobeCollector(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

// QueryUprobeEvents implements the Connect RPC handler interface.
func (a *DebugServiceAdapter) QueryUprobeEvents(
	ctx context.Context,
	req *connect.Request[agentv1.QueryUprobeEventsRequest],
) (*connect.Response[agentv1.QueryUprobeEventsResponse], error) {
	resp, err := a.service.QueryUprobeEvents(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

// ProfileCPU implements the Connect RPC handler interface.
func (a *DebugServiceAdapter) ProfileCPU(
	ctx context.Context,
	req *connect.Request[agentv1.ProfileCPUAgentRequest],
) (*connect.Response[agentv1.ProfileCPUAgentResponse], error) {
	resp, err := a.service.ProfileCPU(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

// QueryCPUProfileSamples implements the Connect RPC handler interface.
func (a *DebugServiceAdapter) QueryCPUProfileSamples(
	ctx context.Context,
	req *connect.Request[agentv1.QueryCPUProfileSamplesRequest],
) (*connect.Response[agentv1.QueryCPUProfileSamplesResponse], error) {
	resp, err := a.service.QueryCPUProfileSamples(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}
