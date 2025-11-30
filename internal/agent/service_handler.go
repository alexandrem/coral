package agent

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
)

// ServiceHandler implements the AgentService gRPC interface for managing service connections.
type ServiceHandler struct {
	agent             *Agent
	runtimeService    *RuntimeService
	telemetryReceiver *TelemetryReceiver
	shellHandler      *ShellHandler
	containerHandler  *ContainerHandler
}

// NewServiceHandler creates a new service handler.
func NewServiceHandler(agent *Agent, runtimeService *RuntimeService, telemetryReceiver *TelemetryReceiver, shellHandler *ShellHandler, containerHandler *ContainerHandler) *ServiceHandler {
	return &ServiceHandler{
		agent:             agent,
		runtimeService:    runtimeService,
		telemetryReceiver: telemetryReceiver,
		shellHandler:      shellHandler,
		containerHandler:  containerHandler,
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
		Name:           req.Msg.Name,
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

	// Update SDK capabilities if provided (RFD 060).
	if req.Msg.SdkCapabilities != nil {
		h.agent.mu.RLock()
		monitor, exists := h.agent.monitors[req.Msg.Name]
		h.agent.mu.RUnlock()

		if exists {
			monitor.SetSdkCapabilities(req.Msg.SdkCapabilities)
		}
	}

	return connect.NewResponse(&agentv1.ConnectServiceResponse{
		Success:     true,
		ServiceName: req.Msg.Name,
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
			Name:           serviceInfo.Name,
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
// Colony calls this to query filtered Beyla metrics from agent's local DuckDB.
func (h *ServiceHandler) QueryBeylaMetrics(
	ctx context.Context,
	req *connect.Request[agentv1.QueryBeylaMetricsRequest],
) (*connect.Response[agentv1.QueryBeylaMetricsResponse], error) {
	// If Beyla is disabled, return empty response.
	if h.agent.beylaManager == nil {
		return connect.NewResponse(&agentv1.QueryBeylaMetricsResponse{
			HttpMetrics:  []*agentv1.BeylaHttpMetric{},
			GrpcMetrics:  []*agentv1.BeylaGrpcMetric{},
			SqlMetrics:   []*agentv1.BeylaSqlMetric{},
			TotalMetrics: 0,
		}), nil
	}

	// Convert Unix seconds to time.Time.
	startTime := time.Unix(req.Msg.StartTime, 0)
	endTime := time.Unix(req.Msg.EndTime, 0)

	response := &agentv1.QueryBeylaMetricsResponse{
		HttpMetrics: []*agentv1.BeylaHttpMetric{},
		GrpcMetrics: []*agentv1.BeylaGrpcMetric{},
		SqlMetrics:  []*agentv1.BeylaSqlMetric{},
	}

	// Determine which metric types to query.
	queryAll := len(req.Msg.MetricTypes) == 0
	queryHTTP := queryAll
	queryGRPC := queryAll
	querySQL := queryAll

	if !queryAll {
		for _, metricType := range req.Msg.MetricTypes {
			switch metricType {
			case agentv1.BeylaMetricType_BEYLA_METRIC_TYPE_HTTP:
				queryHTTP = true
			case agentv1.BeylaMetricType_BEYLA_METRIC_TYPE_GRPC:
				queryGRPC = true
			case agentv1.BeylaMetricType_BEYLA_METRIC_TYPE_SQL:
				querySQL = true
			}
		}
	}

	// Query HTTP metrics if requested.
	if queryHTTP {
		httpMetrics, err := h.agent.beylaManager.QueryHTTPMetrics(ctx, startTime, endTime, req.Msg.ServiceNames)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		// Convert internal protobuf format to API format.
		for _, metric := range httpMetrics {
			response.HttpMetrics = append(response.HttpMetrics, &agentv1.BeylaHttpMetric{
				Timestamp:      metric.Timestamp.AsTime().UnixMilli(),
				ServiceName:    metric.ServiceName,
				HttpMethod:     metric.HttpMethod,
				HttpRoute:      metric.HttpRoute,
				HttpStatusCode: metric.HttpStatusCode,
				LatencyBuckets: metric.LatencyBuckets,
				LatencyCounts:  metric.LatencyCounts,
				RequestCount:   metric.RequestCount,
				Attributes:     metric.Attributes,
			})
		}
	}

	// Query gRPC metrics if requested.
	if queryGRPC {
		grpcMetrics, err := h.agent.beylaManager.QueryGRPCMetrics(ctx, startTime, endTime, req.Msg.ServiceNames)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		// Convert internal protobuf format to API format.
		for _, metric := range grpcMetrics {
			response.GrpcMetrics = append(response.GrpcMetrics, &agentv1.BeylaGrpcMetric{
				Timestamp:      metric.Timestamp.AsTime().UnixMilli(),
				ServiceName:    metric.ServiceName,
				GrpcMethod:     metric.GrpcMethod,
				GrpcStatusCode: metric.GrpcStatusCode,
				LatencyBuckets: metric.LatencyBuckets,
				LatencyCounts:  metric.LatencyCounts,
				RequestCount:   metric.RequestCount,
				Attributes:     metric.Attributes,
			})
		}
	}

	// Query SQL metrics if requested.
	if querySQL {
		sqlMetrics, err := h.agent.beylaManager.QuerySQLMetrics(ctx, startTime, endTime, req.Msg.ServiceNames)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		// Convert internal protobuf format to API format.
		for _, metric := range sqlMetrics {
			response.SqlMetrics = append(response.SqlMetrics, &agentv1.BeylaSqlMetric{
				Timestamp:      metric.Timestamp.AsTime().UnixMilli(),
				ServiceName:    metric.ServiceName,
				SqlOperation:   metric.SqlOperation,
				TableName:      metric.TableName,
				LatencyBuckets: metric.LatencyBuckets,
				LatencyCounts:  metric.LatencyCounts,
				QueryCount:     metric.QueryCount,
				Attributes:     metric.Attributes,
			})
		}
	}

	// Calculate total metrics.
	response.TotalMetrics = int32(len(response.HttpMetrics) + len(response.GrpcMetrics) + len(response.SqlMetrics))

	// Query traces if requested (RFD 036).
	if req.Msg.IncludeTraces {
		// Apply default max_traces if not specified.
		maxTraces := req.Msg.MaxTraces
		if maxTraces == 0 {
			maxTraces = 100 // Default limit.
		} else if maxTraces > 1000 {
			maxTraces = 1000 // Max limit.
		}

		traceSpans, err := h.agent.beylaManager.QueryTraces(ctx, startTime, endTime, req.Msg.ServiceNames, req.Msg.TraceId, maxTraces)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		// Convert internal protobuf format (mesh.v1) to API format (agent.v1).
		for _, span := range traceSpans {
			response.TraceSpans = append(response.TraceSpans, &agentv1.BeylaTraceSpan{
				TraceId:      span.TraceId,
				SpanId:       span.SpanId,
				ParentSpanId: span.ParentSpanId,
				ServiceName:  span.ServiceName,
				SpanName:     span.SpanName,
				SpanKind:     span.SpanKind,
				StartTime:    span.StartTime.AsTime().UnixMilli(),
				DurationUs:   span.Duration.AsDuration().Microseconds(),
				StatusCode:   span.StatusCode,
				Attributes:   span.Attributes,
			})
		}

		response.TotalTraces = int32(len(response.TraceSpans))
	}

	return connect.NewResponse(response), nil
}

// Shell implements the Shell RPC (RFD 026).
func (h *ServiceHandler) Shell(
	ctx context.Context,
	stream *connect.BidiStream[agentv1.ShellRequest, agentv1.ShellResponse],
) error {
	return h.shellHandler.Shell(ctx, stream)
}

// ShellExec implements the ShellExec RPC (RFD 045).
func (h *ServiceHandler) ShellExec(
	ctx context.Context,
	req *connect.Request[agentv1.ShellExecRequest],
) (*connect.Response[agentv1.ShellExecResponse], error) {
	return h.shellHandler.ShellExec(ctx, req)
}

// ContainerExec implements the ContainerExec RPC (RFD 056).
func (h *ServiceHandler) ContainerExec(
	ctx context.Context,
	req *connect.Request[agentv1.ContainerExecRequest],
) (*connect.Response[agentv1.ContainerExecResponse], error) {
	return h.containerHandler.ContainerExec(ctx, req)
}

// ResizeShellTerminal implements the ResizeShellTerminal RPC (RFD 026).
func (h *ServiceHandler) ResizeShellTerminal(
	ctx context.Context,
	req *connect.Request[agentv1.ResizeShellTerminalRequest],
) (*connect.Response[agentv1.ResizeShellTerminalResponse], error) {
	return h.shellHandler.ResizeShellTerminal(ctx, req)
}

// SendShellSignal implements the SendShellSignal RPC (RFD 026).
func (h *ServiceHandler) SendShellSignal(
	ctx context.Context,
	req *connect.Request[agentv1.SendShellSignalRequest],
) (*connect.Response[agentv1.SendShellSignalResponse], error) {
	return h.shellHandler.SendShellSignal(ctx, req)
}

// KillShellSession implements the KillShellSession RPC (RFD 026).
func (h *ServiceHandler) KillShellSession(
	ctx context.Context,
	req *connect.Request[agentv1.KillShellSessionRequest],
) (*connect.Response[agentv1.KillShellSessionResponse], error) {
	return h.shellHandler.KillShellSession(ctx, req)
}

// StreamDebugEvents implements the StreamDebugEvents RPC (RFD 061).
func (h *ServiceHandler) StreamDebugEvents(
	ctx context.Context,
	stream *connect.BidiStream[agentv1.DebugCommand, agentv1.DebugEvent],
) error {
	// Subscribe to debug events
	eventCh := h.agent.debugManager.Subscribe()

	// Goroutine to send events
	errCh := make(chan error, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-eventCh:
				if err := stream.Send(event); err != nil {
					errCh <- err
					return
				}
			}
		}
	}()

	// Loop to receive commands
	for {
		cmd, err := stream.Receive()
		if err != nil {
			return err
		}

		// Handle command
		if cmd.Command == "detach" {
			if err := h.agent.StopDebugSession(cmd.SessionId); err != nil {
				// Log error but continue
				// We might want to send an error event back?
			}
		}
	}
}
