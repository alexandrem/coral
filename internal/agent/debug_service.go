package agent

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
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
	req *meshv1.StartUprobeCollectorRequest,
) (*meshv1.StartUprobeCollectorResponse, error) {
	s.logger.Info().
		Str("service", req.ServiceName).
		Str("function", req.FunctionName).
		Msg("Starting uprobe collector")

	// Get SDK address from service registry or config
	// For now, we'll require it in the request config
	sdkAddr := req.SdkAddr
	if sdkAddr == "" {
		return &meshv1.StartUprobeCollectorResponse{
			Supported: false,
			Error:     "sdk_addr is required in request",
		}, nil
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
		return &meshv1.StartUprobeCollectorResponse{
			Supported: true,
			Error:     fmt.Sprintf("failed to start collector: %v", err),
		}, nil
	}

	return &meshv1.StartUprobeCollectorResponse{
		CollectorId: resp.CollectorId,
		ExpiresAt:   resp.ExpiresAt,
		Supported:   resp.Supported,
		Error:       resp.Error,
	}, nil
}

// StopUprobeCollector handles requests to stop uprobe collectors.
func (s *DebugService) StopUprobeCollector(
	ctx context.Context,
	req *meshv1.StopUprobeCollectorRequest,
) (*meshv1.StopUprobeCollectorResponse, error) {
	s.logger.Info().
		Str("collector_id", req.CollectorId).
		Msg("Stopping uprobe collector")

	err := s.agent.ebpfManager.StopCollector(req.CollectorId)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to stop uprobe collector")
		return &meshv1.StopUprobeCollectorResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return &meshv1.StopUprobeCollectorResponse{
		Success: true,
	}, nil
}

// QueryUprobeEvents handles requests to query collected uprobe events.
func (s *DebugService) QueryUprobeEvents(
	ctx context.Context,
	req *meshv1.QueryUprobeEventsRequest,
) (*meshv1.QueryUprobeEventsResponse, error) {
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
	var filteredEvents []*meshv1.EbpfEvent
	for _, event := range events {
		// Check time range
		if req.StartTime != nil && event.Timestamp.AsTime().Before(req.StartTime.AsTime()) {
			continue
		}
		if req.EndTime != nil && event.Timestamp.AsTime().After(req.EndTime.AsTime()) {
			continue
		}

		filteredEvents = append(filteredEvents, event)

		// Check max events limit
		if req.MaxEvents > 0 && len(filteredEvents) >= int(req.MaxEvents) {
			break
		}
	}

	return &meshv1.QueryUprobeEventsResponse{
		Events:  filteredEvents,
		HasMore: len(events) > len(filteredEvents),
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
	req *connect.Request[meshv1.StartUprobeCollectorRequest],
) (*connect.Response[meshv1.StartUprobeCollectorResponse], error) {
	resp, err := a.service.StartUprobeCollector(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

// StopUprobeCollector implements the Connect RPC handler interface.
func (a *DebugServiceAdapter) StopUprobeCollector(
	ctx context.Context,
	req *connect.Request[meshv1.StopUprobeCollectorRequest],
) (*connect.Response[meshv1.StopUprobeCollectorResponse], error) {
	resp, err := a.service.StopUprobeCollector(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

// QueryUprobeEvents implements the Connect RPC handler interface.
func (a *DebugServiceAdapter) QueryUprobeEvents(
	ctx context.Context,
	req *connect.Request[meshv1.QueryUprobeEventsRequest],
) (*connect.Response[meshv1.QueryUprobeEventsResponse], error) {
	resp, err := a.service.QueryUprobeEvents(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}
