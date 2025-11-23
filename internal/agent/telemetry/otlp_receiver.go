package telemetry

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	otlpmetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommon "go.opentelemetry.io/proto/otlp/common/v1"
	otlpmetrics "go.opentelemetry.io/proto/otlp/metrics/v1"
	otlpresource "go.opentelemetry.io/proto/otlp/resource/v1"
	otlptrace "go.opentelemetry.io/proto/otlp/trace/v1"
)

// OTLPReceiver implements the OTLP gRPC and HTTP receivers.
type OTLPReceiver struct {
	config        Config
	filter        *Filter
	storage       *Storage
	metricsBuffer []pmetric.Metrics // In-memory buffer for recent metrics
	metricsMu     sync.RWMutex
	logger        zerolog.Logger
	grpcServer    *grpc.Server
	httpServer    *http.Server
	grpcLis       net.Listener
	httpLis       net.Listener
	running       bool
	mu            sync.Mutex
	wg            sync.WaitGroup
}

// traceServiceWrapper wraps OTLPReceiver to implement TraceServiceServer.
type traceServiceWrapper struct {
	otlptracev1.UnimplementedTraceServiceServer
	receiver *OTLPReceiver
}

// metricsServiceWrapper wraps OTLPReceiver to implement MetricsServiceServer.
type metricsServiceWrapper struct {
	otlpmetricsv1.UnimplementedMetricsServiceServer
	receiver *OTLPReceiver
}

// NewOTLPReceiver creates a new OTLP receiver.
func NewOTLPReceiver(config Config, storage *Storage, logger zerolog.Logger) (*OTLPReceiver, error) {
	if config.Disabled {
		return nil, fmt.Errorf("telemetry is disabled")
	}

	return &OTLPReceiver{
		config:        config,
		filter:        NewFilter(config.Filters),
		storage:       storage,
		metricsBuffer: make([]pmetric.Metrics, 0, 100), // Buffer up to 100 metric batches
		logger:        logger.With().Str("component", "otlp_receiver").Logger(),
	}, nil
}

// Start starts the OTLP gRPC and HTTP receivers.
func (r *OTLPReceiver) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return fmt.Errorf("OTLP receiver already running")
	}

	// Start gRPC server.
	if err := r.startGRPC(); err != nil {
		return fmt.Errorf("failed to start gRPC receiver: %w", err)
	}

	// Start HTTP server.
	if err := r.startHTTP(); err != nil {
		r.grpcServer.Stop()
		return fmt.Errorf("failed to start HTTP receiver: %w", err)
	}

	// Start cleanup goroutine.
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		retentionDuration := time.Duration(r.config.StorageRetentionHours) * time.Hour
		r.storage.RunCleanupLoop(ctx, retentionDuration)
	}()

	r.running = true

	r.logger.Info().
		Str("grpc_endpoint", r.config.GRPCEndpoint).
		Str("http_endpoint", r.config.HTTPEndpoint).
		Msg("OTLP receiver started")

	return nil
}

// startGRPC starts the OTLP gRPC receiver.
func (r *OTLPReceiver) startGRPC() error {
	lis, err := net.Listen("tcp", r.config.GRPCEndpoint)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", r.config.GRPCEndpoint, err)
	}

	r.grpcLis = lis
	r.grpcServer = grpc.NewServer()

	// Register OTLP trace and metrics services using wrappers.
	otlptracev1.RegisterTraceServiceServer(r.grpcServer, &traceServiceWrapper{receiver: r})
	otlpmetricsv1.RegisterMetricsServiceServer(r.grpcServer, &metricsServiceWrapper{receiver: r})

	// Start gRPC server in background.
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		if err := r.grpcServer.Serve(lis); err != nil {
			r.logger.Error().Err(err).Msg("gRPC server error")
		}
	}()

	r.logger.Info().
		Str("address", r.config.GRPCEndpoint).
		Msg("OTLP gRPC receiver listening")

	return nil
}

// startHTTP starts the OTLP HTTP receiver.
func (r *OTLPReceiver) startHTTP() error {
	lis, err := net.Listen("tcp", r.config.HTTPEndpoint)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", r.config.HTTPEndpoint, err)
	}

	r.httpLis = lis

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/traces", r.handleHTTPTraces)
	mux.HandleFunc("/v1/metrics", r.handleHTTPMetrics)

	r.httpServer = &http.Server{
		Handler: mux,
	}

	// Start HTTP server in background.
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		if err := r.httpServer.Serve(lis); err != nil && err != http.ErrServerClosed {
			r.logger.Error().Err(err).Msg("HTTP server error")
		}
	}()

	r.logger.Info().
		Str("address", r.config.HTTPEndpoint).
		Msg("OTLP HTTP receiver listening")

	return nil
}

// Stop stops the OTLP receiver.
func (r *OTLPReceiver) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return nil
	}

	r.logger.Info().Msg("Stopping OTLP receiver")

	// Stop gRPC server.
	if r.grpcServer != nil {
		r.grpcServer.GracefulStop()
	}

	// Stop HTTP server.
	if r.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.httpServer.Shutdown(ctx); err != nil {
			r.logger.Warn().Err(err).Msg("HTTP server shutdown error")
		}
	}

	// Wait for all goroutines.
	r.wg.Wait()

	r.running = false

	r.logger.Info().Msg("OTLP receiver stopped")
	return nil
}

// Export implements the OTLP gRPC TraceService.Export method.
func (w *traceServiceWrapper) Export(
	ctx context.Context,
	req *otlptracev1.ExportTraceServiceRequest,
) (*otlptracev1.ExportTraceServiceResponse, error) {
	if req == nil || len(req.ResourceSpans) == 0 {
		return &otlptracev1.ExportTraceServiceResponse{}, nil
	}

	spansReceived := 0
	spansFiltered := 0

	// Process all resource spans.
	for _, resourceSpans := range req.ResourceSpans {
		// Extract service name from resource attributes.
		serviceName := extractServiceName(resourceSpans.Resource)

		// Process all scope spans.
		for _, scopeSpans := range resourceSpans.ScopeSpans {
			for _, otlpSpan := range scopeSpans.Spans {
				spansReceived++

				// Convert OTLP span to internal format.
				span := w.receiver.convertOTLPSpan(otlpSpan, serviceName)

				// Apply filtering.
				if !w.receiver.filter.ShouldCapture(span) {
					spansFiltered++
					continue
				}

				// Store in local storage.
				if err := w.receiver.storage.StoreSpan(ctx, span); err != nil {
					w.receiver.logger.Warn().
						Err(err).
						Str("trace_id", span.TraceID).
						Msg("Failed to store span")
				}
			}
		}
	}

	w.receiver.logger.Debug().
		Int("received", spansReceived).
		Int("filtered", spansFiltered).
		Int("stored", spansReceived-spansFiltered).
		Msg("Processed OTLP trace export")

	return &otlptracev1.ExportTraceServiceResponse{}, nil
}

// handleHTTPTraces handles HTTP OTLP trace exports.
func (r *OTLPReceiver) handleHTTPTraces(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read request body.
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer func() { _ = req.Body.Close() }() // TODO: errcheck

	// Parse OTLP request.
	var otlpReq otlptracev1.ExportTraceServiceRequest
	if err := proto.Unmarshal(body, &otlpReq); err != nil {
		http.Error(w, "Failed to parse OTLP request", http.StatusBadRequest)
		return
	}

	// Process request using gRPC handler via wrapper.
	wrapper := &traceServiceWrapper{receiver: r}
	resp, err := wrapper.Export(req.Context(), &otlpReq)
	if err != nil {
		http.Error(w, "Failed to process traces", http.StatusInternalServerError)
		return
	}

	// Marshal response.
	respBytes, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "Failed to marshal response", http.StatusInternalServerError)
		return
	}

	// Send response.
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respBytes) // TODO: errcheck
}

// convertOTLPSpan converts an OTLP span to internal Span format.
func (r *OTLPReceiver) convertOTLPSpan(otlpSpan *otlptrace.Span, serviceName string) Span {
	// Convert trace ID and span ID from bytes to hex strings.
	traceID := hex.EncodeToString(otlpSpan.TraceId)
	spanID := hex.EncodeToString(otlpSpan.SpanId)

	// Calculate duration in milliseconds.
	startTime := time.Unix(0, int64(otlpSpan.StartTimeUnixNano))
	endTime := time.Unix(0, int64(otlpSpan.EndTimeUnixNano))
	durationMs := float64(endTime.Sub(startTime).Microseconds()) / 1000.0

	// Determine if span is an error.
	isError := otlpSpan.Status != nil && otlpSpan.Status.Code == otlptrace.Status_STATUS_CODE_ERROR

	// Extract HTTP attributes.
	var httpStatus int
	var httpMethod string
	var httpRoute string
	attributes := make(map[string]string)

	for _, attr := range otlpSpan.Attributes {
		key := attr.Key
		value := getAttributeValue(attr.Value)

		// Store all attributes.
		attributes[key] = value

		// Extract HTTP-specific attributes.
		switch key {
		case "http.status_code":
			if val, err := strconv.Atoi(value); err == nil {
				httpStatus = val
			}
		case "http.method":
			httpMethod = value
		case "http.route":
			httpRoute = value
		}
	}

	// Determine span kind.
	spanKind := spanKindToString(otlpSpan.Kind)

	return Span{
		Timestamp:   startTime,
		TraceID:     traceID,
		SpanID:      spanID,
		ServiceName: serviceName,
		SpanKind:    spanKind,
		DurationMs:  durationMs,
		IsError:     isError,
		HTTPStatus:  httpStatus,
		HTTPMethod:  httpMethod,
		HTTPRoute:   httpRoute,
		Attributes:  attributes,
	}
}

// extractServiceName extracts the service name from resource attributes.
func extractServiceName(resource *otlpresource.Resource) string {
	if resource == nil {
		return "unknown"
	}

	for _, attr := range resource.Attributes {
		if attr.Key == "service.name" {
			return getAttributeValue(attr.Value)
		}
	}

	return "unknown"
}

// getAttributeValue extracts the string value from an OTLP attribute.
func getAttributeValue(value *otlpcommon.AnyValue) string {
	if value == nil {
		return ""
	}

	switch v := value.Value.(type) {
	case *otlpcommon.AnyValue_StringValue:
		return v.StringValue
	case *otlpcommon.AnyValue_IntValue:
		return strconv.FormatInt(v.IntValue, 10)
	case *otlpcommon.AnyValue_DoubleValue:
		return strconv.FormatFloat(v.DoubleValue, 'f', -1, 64)
	case *otlpcommon.AnyValue_BoolValue:
		return strconv.FormatBool(v.BoolValue)
	default:
		return ""
	}
}

// spanKindToString converts OTLP span kind to string.
func spanKindToString(kind otlptrace.Span_SpanKind) string {
	switch kind {
	case otlptrace.Span_SPAN_KIND_INTERNAL:
		return "INTERNAL"
	case otlptrace.Span_SPAN_KIND_SERVER:
		return "SERVER"
	case otlptrace.Span_SPAN_KIND_CLIENT:
		return "CLIENT"
	case otlptrace.Span_SPAN_KIND_PRODUCER:
		return "PRODUCER"
	case otlptrace.Span_SPAN_KIND_CONSUMER:
		return "CONSUMER"
	default:
		return "UNSPECIFIED"
	}
}

// QuerySpans queries filtered spans from local storage.
// This is called by the QueryTelemetry RPC handler (colony → agent).
func (r *OTLPReceiver) QuerySpans(ctx context.Context, startTime, endTime time.Time, serviceNames []string) ([]Span, error) {
	return r.storage.QuerySpans(ctx, startTime, endTime, serviceNames)
}

// Export implements the OTLP gRPC MetricsService.Export method.
func (w *metricsServiceWrapper) Export(
	ctx context.Context,
	req *otlpmetricsv1.ExportMetricsServiceRequest,
) (*otlpmetricsv1.ExportMetricsServiceResponse, error) {
	if req == nil || len(req.ResourceMetrics) == 0 {
		return &otlpmetricsv1.ExportMetricsServiceResponse{}, nil
	}

	// Convert protobuf to pdata.Metrics for easier handling.
	metrics := pmetric.NewMetrics()
	for _, rm := range req.ResourceMetrics {
		destRM := metrics.ResourceMetrics().AppendEmpty()

		// Copy resource attributes.
		if rm.Resource != nil {
			for _, attr := range rm.Resource.Attributes {
				destRM.Resource().Attributes().PutStr(attr.Key, getAttributeValue(attr.Value))
			}
		}

		// Copy scope metrics.
		for _, sm := range rm.ScopeMetrics {
			destSM := destRM.ScopeMetrics().AppendEmpty()

			// Copy metrics.
			for _, metric := range sm.Metrics {
				destMetric := destSM.Metrics().AppendEmpty()
				destMetric.SetName(metric.Name)
				destMetric.SetDescription(metric.Description)
				destMetric.SetUnit(metric.Unit)

				// Copy metric data based on type.
				copyMetricData(metric, destMetric)
			}
		}
	}

	// Store metrics in buffer.
	w.receiver.metricsMu.Lock()
	w.receiver.metricsBuffer = append(w.receiver.metricsBuffer, metrics)

	// Keep buffer size limited (last 100 batches).
	if len(w.receiver.metricsBuffer) > 100 {
		w.receiver.metricsBuffer = w.receiver.metricsBuffer[len(w.receiver.metricsBuffer)-100:]
	}
	w.receiver.metricsMu.Unlock()

	metricsCount := 0
	for _, rm := range req.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			metricsCount += len(sm.Metrics)
		}
	}

	w.receiver.logger.Debug().
		Int("metrics_count", metricsCount).
		Msg("Processed OTLP metrics export")

	return &otlpmetricsv1.ExportMetricsServiceResponse{}, nil
}

// handleHTTPMetrics handles HTTP OTLP metrics exports.
func (r *OTLPReceiver) handleHTTPMetrics(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read request body.
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer func() { _ = req.Body.Close() }() // TODO: errcheck

	// Parse OTLP request.
	var otlpReq otlpmetricsv1.ExportMetricsServiceRequest
	if err := proto.Unmarshal(body, &otlpReq); err != nil {
		http.Error(w, "Failed to parse OTLP request", http.StatusBadRequest)
		return
	}

	// Process request using gRPC handler via wrapper.
	wrapper := &metricsServiceWrapper{receiver: r}
	resp, err := wrapper.Export(req.Context(), &otlpReq)
	if err != nil {
		http.Error(w, "Failed to process metrics", http.StatusInternalServerError)
		return
	}

	// Marshal response.
	respBytes, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "Failed to marshal response", http.StatusInternalServerError)
		return
	}

	// Send response.
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respBytes) // TODO: errcheck
}

// QueryMetrics retrieves recent metrics from the in-memory buffer.
func (r *OTLPReceiver) QueryMetrics(ctx context.Context) []pmetric.Metrics {
	r.metricsMu.RLock()
	defer r.metricsMu.RUnlock()

	// Return a copy of the metrics buffer.
	result := make([]pmetric.Metrics, len(r.metricsBuffer))
	copy(result, r.metricsBuffer)

	return result
}

// ClearMetrics clears the metrics buffer after they've been consumed.
func (r *OTLPReceiver) ClearMetrics() {
	r.metricsMu.Lock()
	defer r.metricsMu.Unlock()
	r.metricsBuffer = make([]pmetric.Metrics, 0, 100)
}

// copyMetricData copies metric data from protobuf to pdata format.
func copyMetricData(src *otlpmetrics.Metric, dest pmetric.Metric) {
	switch data := src.Data.(type) {
	case *otlpmetrics.Metric_Histogram:
		destHist := dest.SetEmptyHistogram()
		destHist.SetAggregationTemporality(pmetric.AggregationTemporality(data.Histogram.AggregationTemporality))

		for _, dp := range data.Histogram.DataPoints {
			destDP := destHist.DataPoints().AppendEmpty()
			destDP.SetTimestamp(pcommon.Timestamp(dp.TimeUnixNano))
			destDP.SetCount(dp.Count)
			if dp.Sum != nil {
				destDP.SetSum(*dp.Sum)
			}

			if len(dp.ExplicitBounds) > 0 {
				destDP.ExplicitBounds().FromRaw(dp.ExplicitBounds)
			}
			if len(dp.BucketCounts) > 0 {
				destDP.BucketCounts().FromRaw(dp.BucketCounts)
			}

			// Copy attributes.
			for _, attr := range dp.Attributes {
				destDP.Attributes().PutStr(attr.Key, getAttributeValue(attr.Value))
			}
		}

	case *otlpmetrics.Metric_Sum:
		destSum := dest.SetEmptySum()
		destSum.SetAggregationTemporality(pmetric.AggregationTemporality(data.Sum.AggregationTemporality))
		destSum.SetIsMonotonic(data.Sum.IsMonotonic)

		for _, dp := range data.Sum.DataPoints {
			destDP := destSum.DataPoints().AppendEmpty()
			destDP.SetTimestamp(pcommon.Timestamp(dp.TimeUnixNano))

			// Set value based on type.
			switch v := dp.Value.(type) {
			case *otlpmetrics.NumberDataPoint_AsInt:
				destDP.SetIntValue(v.AsInt)
			case *otlpmetrics.NumberDataPoint_AsDouble:
				destDP.SetDoubleValue(v.AsDouble)
			}

			// Copy attributes.
			for _, attr := range dp.Attributes {
				destDP.Attributes().PutStr(attr.Key, getAttributeValue(attr.Value))
			}
		}

	case *otlpmetrics.Metric_Gauge:
		destGauge := dest.SetEmptyGauge()

		for _, dp := range data.Gauge.DataPoints {
			destDP := destGauge.DataPoints().AppendEmpty()
			destDP.SetTimestamp(pcommon.Timestamp(dp.TimeUnixNano))

			// Set value based on type.
			switch v := dp.Value.(type) {
			case *otlpmetrics.NumberDataPoint_AsInt:
				destDP.SetIntValue(v.AsInt)
			case *otlpmetrics.NumberDataPoint_AsDouble:
				destDP.SetDoubleValue(v.AsDouble)
			}

			// Copy attributes.
			for _, attr := range dp.Attributes {
				destDP.Attributes().PutStr(attr.Key, getAttributeValue(attr.Value))
			}
		}
	}
}
