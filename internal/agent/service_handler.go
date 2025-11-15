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
				Timestamp:     metric.Timestamp.AsTime().UnixMilli(),
				ServiceName:   metric.ServiceName,
				SqlOperation:  metric.SqlOperation,
				TableName:     metric.TableName,
				LatencyBuckets: metric.LatencyBuckets,
				LatencyCounts: metric.LatencyCounts,
				QueryCount:    metric.QueryCount,
				Attributes:    metric.Attributes,
			})
		}
	}

	// Calculate total metrics.
	response.TotalMetrics = int32(len(response.HttpMetrics) + len(response.GrpcMetrics) + len(response.SqlMetrics))

	return connect.NewResponse(response), nil
}
