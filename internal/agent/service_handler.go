package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/pkg/sdk/debug"
)

// ServiceHandler implements the AgentService gRPC interface for managing service connections.
type ServiceHandler struct {
	agent             *Agent
	runtimeService    *RuntimeService
	telemetryReceiver *TelemetryReceiver
	shellHandler      *ShellHandler
	containerHandler  *ContainerHandler
	functionCache     *FunctionCache
}

// NewServiceHandler creates a new service handler.
func NewServiceHandler(agent *Agent, runtimeService *RuntimeService, telemetryReceiver *TelemetryReceiver, shellHandler *ShellHandler, containerHandler *ContainerHandler, functionCache *FunctionCache) *ServiceHandler {
	return &ServiceHandler{
		agent:             agent,
		runtimeService:    runtimeService,
		telemetryReceiver: telemetryReceiver,
		shellHandler:      shellHandler,
		containerHandler:  containerHandler,
		functionCache:     functionCache,
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

	// Update SDK capabilities
	var caps *agentv1.ServiceSdkCapabilities

	if req.Msg.SdkCapabilities != nil {
		// Push model (legacy or explicit registration)
		caps = req.Msg.SdkCapabilities
	} else {
		// Pull model (RFD 066): Attempt discovery
		// Default to localhost:9002, but could be configurable or derived
		discoveryAddr := "localhost:9002"
		if discovered := h.discoverSDK(ctx, discoveryAddr); discovered != nil {
			caps = discovered
			caps.ServiceName = req.Msg.Name
			h.agent.logger.Info().
				Str("service", req.Msg.Name).
				Str("sdk_version", caps.SdkVersion).
				Int("functions", int(caps.FunctionCount)).
				Msg("Discovered SDK via HTTP")
		}
	}

	if caps != nil {
		h.agent.mu.RLock()
		monitor, exists := h.agent.monitors[req.Msg.Name]
		h.agent.mu.RUnlock()

		if exists {
			monitor.SetSdkCapabilities(caps)
		}
	}

	return connect.NewResponse(&agentv1.ConnectServiceResponse{
		Success:     true,
		ServiceName: req.Msg.Name,
	}), nil
}

// discoverSDK attempts to discover SDK capabilities via HTTP.
func (h *ServiceHandler) discoverSDK(ctx context.Context, addr string) *agentv1.ServiceSdkCapabilities {
	// Simple HTTP GET request with short timeout
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/debug/capabilities", nil)
	if err != nil {
		return nil
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// SDK not present or not reachable (expected for non-SDK apps)
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var capsResp debug.CapabilitiesResponse
	if err := json.NewDecoder(resp.Body).Decode(&capsResp); err != nil {
		h.agent.logger.Warn().Err(err).Msg("Invalid SDK capabilities response")
		return nil
	}

	return &agentv1.ServiceSdkCapabilities{
		ProcessId:       capsResp.ProcessID,
		SdkEnabled:      true,
		SdkVersion:      capsResp.SdkVersion,
		SdkAddr:         addr,
		HasDwarfSymbols: capsResp.HasDwarfSymbols,
		BinaryPath:      capsResp.BinaryPath,
		FunctionCount:   uint32(capsResp.FunctionCount),
		BinaryHash:      capsResp.BinaryHash,
	}
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
			ProcessId:      status.ProcessID,
			BinaryPath:     status.BinaryPath,
			BinaryHash:     status.BinaryHash,
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

// QueryEbpfMetrics retrieves eBPF metrics from the agent's local storage (RFD 032).
// Colony calls this to query filtered eBPF metrics from agent's local DuckDB.
func (h *ServiceHandler) QueryEbpfMetrics(
	ctx context.Context,
	req *connect.Request[agentv1.QueryEbpfMetricsRequest],
) (*connect.Response[agentv1.QueryEbpfMetricsResponse], error) {
	// If Beyla is disabled, return empty response.
	if h.agent.beylaManager == nil {
		return connect.NewResponse(&agentv1.QueryEbpfMetricsResponse{
			HttpMetrics:  []*agentv1.EbpfHttpMetric{},
			GrpcMetrics:  []*agentv1.EbpfGrpcMetric{},
			SqlMetrics:   []*agentv1.EbpfSqlMetric{},
			TotalMetrics: 0,
		}), nil
	}

	// Convert Unix seconds to time.Time.
	startTime := time.Unix(req.Msg.StartTime, 0)
	endTime := time.Unix(req.Msg.EndTime, 0)

	response := &agentv1.QueryEbpfMetricsResponse{
		HttpMetrics: []*agentv1.EbpfHttpMetric{},
		GrpcMetrics: []*agentv1.EbpfGrpcMetric{},
		SqlMetrics:  []*agentv1.EbpfSqlMetric{},
	}

	// Determine which metric types to query.
	queryAll := len(req.Msg.MetricTypes) == 0
	queryHTTP := queryAll
	queryGRPC := queryAll
	querySQL := queryAll

	if !queryAll {
		for _, metricType := range req.Msg.MetricTypes {
			switch metricType {
			case agentv1.EbpfMetricType_EBPF_METRIC_TYPE_HTTP:
				queryHTTP = true
			case agentv1.EbpfMetricType_EBPF_METRIC_TYPE_GRPC:
				queryGRPC = true
			case agentv1.EbpfMetricType_EBPF_METRIC_TYPE_SQL:
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
			response.HttpMetrics = append(response.HttpMetrics, &agentv1.EbpfHttpMetric{
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
			response.GrpcMetrics = append(response.GrpcMetrics, &agentv1.EbpfGrpcMetric{
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
			response.SqlMetrics = append(response.SqlMetrics, &agentv1.EbpfSqlMetric{
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
			response.TraceSpans = append(response.TraceSpans, &agentv1.EbpfTraceSpan{
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
				errCh <- fmt.Errorf("failed to stop debug session: %w", err)
			}
		}
	}
}

// GetFunctions implements the GetFunctions RPC (RFD 063 - function discovery).
// Colony calls this periodically - returns cached functions from local DuckDB.
func (h *ServiceHandler) GetFunctions(
	ctx context.Context,
	req *connect.Request[agentv1.GetFunctionsRequest],
) (*connect.Response[agentv1.GetFunctionsResponse], error) {
	h.agent.logger.Debug().
		Str("service_filter", req.Msg.ServiceName).
		Msg("Received GetFunctions request")

	var allFunctions []*agentv1.FunctionInfo

	// Get all monitored services.
	h.agent.mu.RLock()
	monitors := make(map[string]*ServiceMonitor)
	for name, monitor := range h.agent.monitors {
		// Filter by service name if specified.
		if req.Msg.ServiceName != "" && name != req.Msg.ServiceName {
			continue
		}
		monitors[name] = monitor
	}
	h.agent.mu.RUnlock()

	// Get cached functions for each service.
	for serviceName := range monitors {
		// Check if cache needs update (binary hash changed).
		// This is a lightweight check that happens every time.
		h.tryUpdateCacheIfNeeded(ctx, serviceName)

		// Get cached functions.
		functions, err := h.functionCache.GetCachedFunctions(ctx, serviceName)
		if err != nil {
			h.agent.logger.Warn().
				Err(err).
				Str("service", serviceName).
				Msg("Failed to get cached functions")
			continue
		}

		allFunctions = append(allFunctions, functions...)
	}

	h.agent.logger.Debug().
		Int("function_count", len(allFunctions)).
		Int("service_count", len(monitors)).
		Msg("Returned cached functions")

	return connect.NewResponse(&agentv1.GetFunctionsResponse{
		Functions:      allFunctions,
		TotalFunctions: int32(len(allFunctions)),
	}), nil
}

// tryUpdateCacheIfNeeded checks if the cache needs updating and triggers discovery if so.
// This is called during GetFunctions to ensure cache is up-to-date.
func (h *ServiceHandler) tryUpdateCacheIfNeeded(ctx context.Context, serviceName string) {
	h.agent.mu.RLock()
	monitor, exists := h.agent.monitors[serviceName]
	h.agent.mu.RUnlock()

	if !exists {
		return
	}

	status := monitor.GetStatus()
	if status.BinaryPath == "" {
		return
	}

	// Check if cache needs update (non-blocking check).
	needsUpdate, err := h.functionCache.NeedsUpdate(ctx, serviceName, status.BinaryPath)
	if err != nil {
		h.agent.logger.Warn().
			Err(err).
			Str("service", serviceName).
			Msg("Failed to check if cache needs update")
		return
	}

	if needsUpdate {
		h.agent.logger.Info().
			Str("service", serviceName).
			Msg("Binary hash changed, triggering function re-discovery")

		// Trigger async discovery (don't block the RPC).
		go func() {
			if err := h.functionCache.DiscoverAndCache(context.Background(), serviceName, status.BinaryPath); err != nil {
				h.agent.logger.Error().
					Err(err).
					Str("service", serviceName).
					Msg("Failed to discover and cache functions")
			}
		}()
	}
}
