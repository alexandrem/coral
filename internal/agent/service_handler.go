package agent

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/constants"
	"github.com/coral-mesh/coral/internal/wireguard"
)

// MeshInfoProvider is a callback that fetches live WireGuard/mesh statistics for parity with the /status endpoint.
type MeshInfoProvider func() map[string]interface{}

// ServiceHandler implements the AgentService gRPC interface for managing service connections.
type ServiceHandler struct {
	agent                *Agent
	runtimeService       *RuntimeService
	telemetryReceiver    *TelemetryReceiver
	shellHandler         *ShellHandler
	containerHandler     *ContainerHandler
	functionCache        *FunctionCache
	systemMetricsHandler *SystemMetricsHandler
	sessionID            string // Database session UUID for checkpoint tracking (RFD 089).
	meshInfoProvider     MeshInfoProvider
}

// NewServiceHandler creates a new service handler.
func NewServiceHandler(agent *Agent, runtimeService *RuntimeService, telemetryReceiver *TelemetryReceiver, shellHandler *ShellHandler, containerHandler *ContainerHandler, functionCache *FunctionCache, systemMetricsHandler *SystemMetricsHandler) *ServiceHandler {
	return &ServiceHandler{
		agent:                agent,
		runtimeService:       runtimeService,
		telemetryReceiver:    telemetryReceiver,
		shellHandler:         shellHandler,
		containerHandler:     containerHandler,
		functionCache:        functionCache,
		systemMetricsHandler: systemMetricsHandler,
	}
}

// SetSessionID sets the database session UUID for checkpoint tracking (RFD 089).
func (h *ServiceHandler) SetSessionID(sessionID string) {
	h.sessionID = sessionID
}

// SetMeshInfoProvider sets the callback used for providing mesh metrics dynamically in GetRuntimeContext.
func (h *ServiceHandler) SetMeshInfoProvider(provider MeshInfoProvider) {
	h.meshInfoProvider = provider
}

// GetRuntimeContext implements the GetRuntimeContext RPC.
func (h *ServiceHandler) GetRuntimeContext(
	ctx context.Context,
	req *connect.Request[agentv1.GetRuntimeContextRequest],
) (*connect.Response[agentv1.RuntimeContextResponse], error) {
	// Delegate to runtime service.
	cachedResp, err := h.runtimeService.GetRuntimeContext(ctx, req.Msg)
	if err != nil {
		return nil, err
	}

	resp := proto.Clone(cachedResp).(*agentv1.RuntimeContextResponse)

	// Fetch dynamic mesh telemetry and map to strictly typed Protobuf struct
	if h.meshInfoProvider != nil {
		if meshInfo := h.meshInfoProvider(); meshInfo != nil {
			resp.Wireguard = wireguard.MapToMeshTelemetryProto(meshInfo)
		}
	}

	return connect.NewResponse(resp), nil
}

// ConnectService implements the ConnectService RPC.
func (h *ServiceHandler) ConnectService(
	ctx context.Context,
	req *connect.Request[agentv1.ConnectServiceRequest],
) (*connect.Response[agentv1.ConnectServiceResponse], error) {
	// Exactly one of port or exe_pattern must be set (RFD 111): a service is
	// identified either by the socket it listens on or by its executable
	// path, never both, never neither.
	hasPort := req.Msg.Port != 0
	hasPattern := req.Msg.ExePattern != ""
	if hasPort && hasPattern {
		return connect.NewResponse(&agentv1.ConnectServiceResponse{
			Success: false,
			Error:   fmt.Sprintf("service %q cannot specify both a port and exe_pattern", req.Msg.Name),
		}), nil
	}
	if !hasPort && !hasPattern {
		return connect.NewResponse(&agentv1.ConnectServiceResponse{
			Success: false,
			Error:   fmt.Sprintf("service %q needs either a port or exe_pattern", req.Msg.Name),
		}), nil
	}

	// Convert request to ServiceInfo.
	serviceInfo := &meshv1.ServiceInfo{
		Name:           req.Msg.Name,
		Port:           req.Msg.Port,
		HealthEndpoint: req.Msg.HealthEndpoint,
		ServiceType:    req.Msg.ServiceType,
		Labels:         req.Msg.Labels,
		ExePattern:     req.Msg.ExePattern,
	}

	// Connect to service. Treat AlreadyExists as a soft error so that
	// subsequent calls can still update SDK capabilities (e.g. when a second
	// suite connects the same service with an SdkAddr that the first suite omitted).
	err := h.agent.ConnectService(serviceInfo)
	alreadyConnected := status.Code(err) == codes.AlreadyExists
	if err != nil && !alreadyConnected {
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
		// Default to localhost:9002, but could be configurable or derived.
		discoveryAddr := constants.DefaultSDKDiscoveryAddress

		sdkCaps := h.discoverSDK(ctx, discoveryAddr)
		if sdkCaps != nil {
			caps = sdkCaps
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
		entry, exists := h.agent.findByNameLocked(req.Msg.Name)
		h.agent.mu.RUnlock()

		if exists && entry.Monitor != nil {
			entry.Monitor.SetSdkCapabilities(caps)
		}
	}

	return connect.NewResponse(&agentv1.ConnectServiceResponse{
		Success:     true,
		ServiceName: req.Msg.Name,
	}), nil
}

// discoverSDK attempts to discover SDK capabilities via HTTP.
func (h *ServiceHandler) discoverSDK(ctx context.Context, addr string) *agentv1.ServiceSdkCapabilities {
	return discoverSDKCapabilities(ctx, addr, h.agent.logger)
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
	// Snapshot the unified service map so both auto-observed and watched
	// entries are represented. The agent lock protects entry membership and
	// metadata; ServiceMonitor.GetStatus takes its own lock for monitor state.
	h.agent.mu.RLock()
	serviceStatuses := make([]*agentv1.ServiceStatus, 0, len(h.agent.services))
	for _, entry := range h.agent.services {
		namingSource := serviceNamingSource(entry.NamingSource)
		if req.Msg.SourceFilter != nil && namingSource != req.Msg.GetSourceFilter() {
			continue
		}

		serviceStatus := &agentv1.ServiceStatus{
			Name:              entry.Name(),
			Port:              entry.Port,
			AutoName:          entry.AutoName,
			AuthoritativeName: entry.AuthoritativeName,
			NamingSource:      namingSource,
			HasMonitor:        entry.Monitor != nil,
			ObservationTier:   uint32(entry.Tier),
			ProcessId:         entry.PID,
			BinaryPath:        entry.BinaryPath,
			BinaryHash:        entry.BinaryHash,
			ExePattern:        entry.ExePattern,
		}

		if entry.Monitor != nil {
			serviceInfo := entry.Monitor.service
			monitorStatus := entry.Monitor.GetStatus()
			serviceStatus.HealthEndpoint = serviceInfo.HealthEndpoint
			serviceStatus.ServiceType = serviceInfo.ServiceType
			serviceStatus.Labels = serviceInfo.Labels
			serviceStatus.Status = string(monitorStatus.Status)
			serviceStatus.LastCheck = timestamppb.New(monitorStatus.LastCheck)
			serviceStatus.Error = monitorStatus.Error
			serviceStatus.ProcessId = monitorStatus.ProcessID
			serviceStatus.BinaryPath = monitorStatus.BinaryPath
			serviceStatus.BinaryHash = monitorStatus.BinaryHash
		}

		serviceStatuses = append(serviceStatuses, serviceStatus)
	}
	h.agent.mu.RUnlock()

	return connect.NewResponse(&agentv1.ListServicesResponse{
		Services: serviceStatuses,
	}), nil
}

func serviceNamingSource(source NamingSource) agentv1.ServiceNamingSource {
	switch source {
	case NamingSourceAuto:
		return agentv1.ServiceNamingSource_SERVICE_NAMING_SOURCE_AUTO
	case NamingSourceAuthoritative:
		return agentv1.ServiceNamingSource_SERVICE_NAMING_SOURCE_AUTHORITATIVE
	default:
		return agentv1.ServiceNamingSource_SERVICE_NAMING_SOURCE_UNSPECIFIED
	}
}

// QueryTelemetry retrieves filtered telemetry spans from the agent's local storage.
// This is part of RFD 025 pull-based telemetry model.
// Colony calls this to query filtered spans from agent's local storage using sequence-based polling.
func (h *ServiceHandler) QueryTelemetry(
	ctx context.Context,
	req *connect.Request[agentv1.QueryTelemetryRequest],
) (*connect.Response[agentv1.QueryTelemetryResponse], error) {
	// If telemetry is disabled, return empty response.
	if h.telemetryReceiver == nil {
		return connect.NewResponse(&agentv1.QueryTelemetryResponse{
			Spans:      []*agentv1.TelemetrySpan{},
			TotalSpans: 0,
			SessionId:  h.sessionID,
		}), nil
	}

	spans, maxSeqID, err := h.telemetryReceiver.QuerySpansBySeqID(ctx, req.Msg.StartSeqId, req.Msg.MaxRecords, req.Msg.ServiceNames)
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
			SeqId:       span.SeqID,
		}
		pbSpans = append(pbSpans, pbSpan)
	}

	return connect.NewResponse(&agentv1.QueryTelemetryResponse{
		Spans:      pbSpans,
		TotalSpans: int32(len(pbSpans)),
		MaxSeqId:   maxSeqID,
		SessionId:  h.sessionID,
	}), nil
}

// QueryEbpfMetrics retrieves eBPF metrics from the agent's local storage (RFD 032).
// Colony calls this to query filtered eBPF metrics from agent's local DuckDB using sequence-based polling.
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
			SessionId:    h.sessionID,
		}), nil
	}

	response := &agentv1.QueryEbpfMetricsResponse{
		HttpMetrics: []*agentv1.EbpfHttpMetric{},
		GrpcMetrics: []*agentv1.EbpfGrpcMetric{},
		SqlMetrics:  []*agentv1.EbpfSqlMetric{},
		SessionId:   h.sessionID,
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

	if queryHTTP {
		httpMetrics, maxSeqID, err := h.agent.beylaManager.QueryHTTPMetricsBySeqID(ctx, req.Msg.HttpStartSeqId, req.Msg.MaxRecords, req.Msg.ServiceNames)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
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
		response.HttpMaxSeqId = maxSeqID
	}

	if queryGRPC {
		grpcMetrics, maxSeqID, err := h.agent.beylaManager.QueryGRPCMetricsBySeqID(ctx, req.Msg.GrpcStartSeqId, req.Msg.MaxRecords, req.Msg.ServiceNames)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
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
		response.GrpcMaxSeqId = maxSeqID
	}

	if querySQL {
		sqlMetrics, maxSeqID, err := h.agent.beylaManager.QuerySQLMetricsBySeqID(ctx, req.Msg.SqlStartSeqId, req.Msg.MaxRecords, req.Msg.ServiceNames)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
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
		response.SqlMaxSeqId = maxSeqID
	}

	// Query traces by seq_id if requested.
	if req.Msg.IncludeTraces {
		traceSpans, maxSeqID, err := h.agent.beylaManager.QueryTracesBySeqID(ctx, req.Msg.TracesStartSeqId, req.Msg.MaxRecords, req.Msg.ServiceNames)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
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
				SeqId:        span.SeqId,
			})
		}
		response.TotalTraces = int32(len(response.TraceSpans))
		response.TracesMaxSeqId = maxSeqID
	}

	// Calculate total metrics.
	// #nosec G115
	response.TotalMetrics = int32(len(response.HttpMetrics) + len(response.GrpcMetrics) + len(response.SqlMetrics))

	return connect.NewResponse(response), nil
}

// QuerySystemMetrics retrieves system metrics from the agent's local storage (RFD 071).
// Colony calls this to query system metrics from agent's local DuckDB.
func (h *ServiceHandler) QuerySystemMetrics(
	ctx context.Context,
	req *connect.Request[agentv1.QuerySystemMetricsRequest],
) (*connect.Response[agentv1.QuerySystemMetricsResponse], error) {
	// If system metrics handler is not initialized, return empty response.
	if h.systemMetricsHandler == nil {
		return connect.NewResponse(&agentv1.QuerySystemMetricsResponse{
			Metrics:      []*agentv1.SystemMetric{},
			TotalMetrics: 0,
		}), nil
	}

	// Delegate to system metrics handler.
	return h.systemMetricsHandler.QuerySystemMetrics(ctx, req)
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

	// Get all watched (Tier 1) services.
	h.agent.mu.RLock()
	entries := make(map[string]*ServiceEntry)
	for _, entry := range h.agent.services {
		if entry.Monitor == nil {
			continue
		}
		name := entry.Name()
		// Filter by service name if specified.
		if req.Msg.ServiceName != "" && name != req.Msg.ServiceName {
			continue
		}
		entries[name] = entry
	}
	h.agent.mu.RUnlock()

	// Get cached functions for each service.
	for serviceName, entry := range entries {
		// Ensure functions are indexed for this service, discovering lazily
		// on first need rather than eagerly at connect time (RFD 104).
		h.ensureIndexedAsync(entry)

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
		Int("service_count", len(entries)).
		Msg("Returned cached functions")

	return connect.NewResponse(&agentv1.GetFunctionsResponse{
		Functions:      allFunctions,
		TotalFunctions: int32(len(allFunctions)),
	}), nil
}

// ensureIndexedAsync triggers FunctionCache.EnsureIndexed for entry's binary
// without blocking the caller (RFD 104). It is a no-op if the binary path
// isn't known yet.
func (h *ServiceHandler) ensureIndexedAsync(entry *ServiceEntry) {
	status := entry.Monitor.GetStatus()
	if status.BinaryPath == "" {
		return
	}

	serviceName := entry.Name()

	sdkAddr := ""
	if caps := entry.Monitor.GetSdkCapabilities(); caps != nil && caps.SdkEnabled {
		sdkAddr = caps.SdkAddr
	}

	go func() {
		if err := h.functionCache.EnsureIndexed(context.Background(), serviceName, status.BinaryPath, sdkAddr); err != nil {
			h.agent.logger.Error().
				Err(err).
				Str("service", serviceName).
				Msg("Failed to ensure functions indexed")
		}
	}()
}
