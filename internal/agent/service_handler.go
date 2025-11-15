package agent

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-io/coral/coral/agent/v1"
	meshv1 "github.com/coral-io/coral/coral/mesh/v1"
)

// ServiceHandler implements the AgentService gRPC interface for managing service connections.
type ServiceHandler struct {
	agent             *Agent
	runtimeService    *RuntimeService
	telemetryReceiver *TelemetryReceiver
}

// NewServiceHandler creates a new service handler.
func NewServiceHandler(agent *Agent, runtimeService *RuntimeService, telemetryReceiver *TelemetryReceiver) *ServiceHandler {
	return &ServiceHandler{
		agent:             agent,
		runtimeService:    runtimeService,
		telemetryReceiver: telemetryReceiver,
	}
}

// GetRuntimeContext implements the GetRuntimeContext RPC.
func (h *ServiceHandler) GetRuntimeContext(
	ctx context.Context,
	req *connect.Request[agentv1.GetRuntimeContextRequest],
) (*connect.Response[agentv1.RuntimeContextResponse], error) {
	// Delegate to runtime service.
	resp, err := h.runtimeService.GetRuntimeContext(ctx, req.Msg)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(resp), nil
}

// ConnectService implements the ConnectService RPC.
func (h *ServiceHandler) ConnectService(
	ctx context.Context,
	req *connect.Request[agentv1.ConnectServiceRequest],
) (*connect.Response[agentv1.ConnectServiceResponse], error) {
	// Convert request to ServiceInfo.
	serviceInfo := &meshv1.ServiceInfo{
		ComponentName:  req.Msg.ComponentName,
		Port:           req.Msg.Port,
		HealthEndpoint: req.Msg.HealthEndpoint,
		ServiceType:    req.Msg.ServiceType,
		Labels:         req.Msg.Labels,
	}

	// Connect to service.
	err := h.agent.ConnectService(serviceInfo)
	if err != nil {
		return connect.NewResponse(&agentv1.ConnectServiceResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}

	return connect.NewResponse(&agentv1.ConnectServiceResponse{
		Success:     true,
		ServiceName: req.Msg.ComponentName,
	}), nil
}

// DisconnectService implements the DisconnectService RPC.
func (h *ServiceHandler) DisconnectService(
	ctx context.Context,
	req *connect.Request[agentv1.DisconnectServiceRequest],
) (*connect.Response[agentv1.DisconnectServiceResponse], error) {
	err := h.agent.DisconnectService(req.Msg.ServiceName)
	if err != nil {
		return connect.NewResponse(&agentv1.DisconnectServiceResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}

	return connect.NewResponse(&agentv1.DisconnectServiceResponse{
		Success: true,
	}), nil
}

// ListServices implements the ListServices RPC.
func (h *ServiceHandler) ListServices(
	ctx context.Context,
	req *connect.Request[agentv1.ListServicesRequest],
) (*connect.Response[agentv1.ListServicesResponse], error) {
	statuses := h.agent.GetServiceStatuses()

	// Convert to protobuf response.
	serviceStatuses := make([]*agentv1.ServiceStatus, 0, len(statuses))
	for name, status := range statuses {
		// Get the service info from the monitor.
		h.agent.mu.RLock()
		monitor, exists := h.agent.monitors[name]
		h.agent.mu.RUnlock()

		if !exists {
			continue
		}

		serviceInfo := monitor.service

		serviceStatuses = append(serviceStatuses, &agentv1.ServiceStatus{
			ComponentName:  serviceInfo.ComponentName,
			Port:           serviceInfo.Port,
			HealthEndpoint: serviceInfo.HealthEndpoint,
			ServiceType:    serviceInfo.ServiceType,
			Labels:         serviceInfo.Labels,
			Status:         string(status.Status),
			LastCheck:      timestamppb.New(status.LastCheck),
			Error:          status.Error,
		})
	}

	return connect.NewResponse(&agentv1.ListServicesResponse{
		Services: serviceStatuses,
	}), nil
}

// QueryTelemetry retrieves filtered telemetry spans from the agent's local storage.
// This is part of RFD 025 pull-based telemetry model.
// Colony calls this to query filtered spans from agent's local storage.
func (h *ServiceHandler) QueryTelemetry(
	ctx context.Context,
	req *connect.Request[agentv1.QueryTelemetryRequest],
) (*connect.Response[agentv1.QueryTelemetryResponse], error) {
	// If telemetry is disabled, return empty response.
	if h.telemetryReceiver == nil {
		return connect.NewResponse(&agentv1.QueryTelemetryResponse{
			Spans:      []*agentv1.TelemetrySpan{},
			TotalSpans: 0,
		}), nil
	}

	// Convert Unix seconds to time.Time.
	startTime := time.Unix(req.Msg.StartTime, 0)
	endTime := time.Unix(req.Msg.EndTime, 0)

	// Query spans from local storage.
	spans, err := h.telemetryReceiver.QuerySpans(ctx, startTime, endTime, req.Msg.ServiceNames)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Convert internal spans to protobuf spans.
	pbSpans := make([]*agentv1.TelemetrySpan, 0, len(spans))
	for _, span := range spans {
		pbSpan := &agentv1.TelemetrySpan{
			Timestamp:   span.Timestamp.UnixMilli(),
			TraceId:     span.TraceID,
			SpanId:      span.SpanID,
			ServiceName: span.ServiceName,
			SpanKind:    span.SpanKind,
			DurationMs:  span.DurationMs,
			IsError:     span.IsError,
			HttpStatus:  int32(span.HTTPStatus),
			HttpMethod:  span.HTTPMethod,
			HttpRoute:   span.HTTPRoute,
			Attributes:  span.Attributes,
		}
		pbSpans = append(pbSpans, pbSpan)
	}

	return connect.NewResponse(&agentv1.QueryTelemetryResponse{
		Spans:      pbSpans,
		TotalSpans: int32(len(pbSpans)),
	}), nil
}

// QueryBeylaMetrics retrieves Beyla metrics from the agent's local storage (RFD 032).
// This is a stub implementation - full Beyla integration is in progress.
func (h *ServiceHandler) QueryBeylaMetrics(
	ctx context.Context,
	req *connect.Request[agentv1.QueryBeylaMetricsRequest],
) (*connect.Response[agentv1.QueryBeylaMetricsResponse], error) {
	// TODO: Implement Beyla metrics query (RFD 032 Phase 4).
	// For now, return empty response.
	return connect.NewResponse(&agentv1.QueryBeylaMetricsResponse{
		HttpMetrics: []*agentv1.BeylaHttpMetric{},
		GrpcMetrics: []*agentv1.BeylaGrpcMetric{},
		SqlMetrics:  []*agentv1.BeylaSqlMetric{},
	}), nil
}
