package beyla

import (
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	ebpfpb "github.com/coral-io/coral/proto/coral/mesh/v1"
)

// Transformer converts OTLP metrics and traces to Coral's internal format (RFD 032).
type Transformer struct {
	logger zerolog.Logger
}

// NewTransformer creates a new OTLP to Coral transformer.
func NewTransformer(logger zerolog.Logger) *Transformer {
	return &Transformer{
		logger: logger.With().Str("component", "beyla_transformer").Logger(),
	}
}

// TransformMetrics converts OTLP metrics to Coral BeylaHttpMetrics, BeylaGrpcMetrics, etc.
func (t *Transformer) TransformMetrics(otlpMetrics pmetric.Metrics) ([]*ebpfpb.EbpfEvent, error) {
	var events []*ebpfpb.EbpfEvent

	// Iterate through resource metrics.
	for i := 0; i < otlpMetrics.ResourceMetrics().Len(); i++ {
		rm := otlpMetrics.ResourceMetrics().At(i)
		serviceName := getStringAttribute(rm.Resource().Attributes(), "service.name", "unknown")

		// Iterate through scope metrics.
		for j := 0; j < rm.ScopeMetrics().Len(); j++ {
			sm := rm.ScopeMetrics().At(j)

			// Iterate through metrics.
			for k := 0; k < sm.Metrics().Len(); k++ {
				metric := sm.Metrics().At(k)

				// Route metric to appropriate transformer based on name.
				switch metric.Name() {
				case "http.server.request.duration", "http.server.duration":
					httpEvents := t.transformHTTPMetric(metric, serviceName)
					events = append(events, httpEvents...)

				case "rpc.server.duration":
					grpcEvents := t.transformGRPCMetric(metric, serviceName)
					events = append(events, grpcEvents...)

				case "db.client.operation.duration":
					sqlEvents := t.transformSQLMetric(metric, serviceName)
					events = append(events, sqlEvents...)

				default:
					t.logger.Debug().
						Str("metric_name", metric.Name()).
						Msg("Skipping unknown metric")
				}
			}
		}
	}

	t.logger.Debug().Int("event_count", len(events)).Msg("Transformed OTLP metrics")
	return events, nil
}

// TransformTraces converts OTLP traces to Coral BeylaTraceSpan.
func (t *Transformer) TransformTraces(otlpTraces ptrace.Traces) ([]*ebpfpb.EbpfEvent, error) {
	var events []*ebpfpb.EbpfEvent

	// Iterate through resource spans.
	for i := 0; i < otlpTraces.ResourceSpans().Len(); i++ {
		rs := otlpTraces.ResourceSpans().At(i)
		serviceName := getStringAttribute(rs.Resource().Attributes(), "service.name", "unknown")

		// Iterate through scope spans.
		for j := 0; j < rs.ScopeSpans().Len(); j++ {
			ss := rs.ScopeSpans().At(j)

			// Iterate through spans.
			for k := 0; k < ss.Spans().Len(); k++ {
				span := ss.Spans().At(k)

				// Create BeylaTraceSpan.
				traceSpan := &ebpfpb.BeylaTraceSpan{
					TraceId:      span.TraceID().String(),
					SpanId:       span.SpanID().String(),
					ParentSpanId: span.ParentSpanID().String(),
					ServiceName:  serviceName,
					SpanName:     span.Name(),
					SpanKind:     spanKindToString(span.Kind()),
					StartTime:    timestamppb.New(span.StartTimestamp().AsTime()),
					Duration:     durationpb.New(span.EndTimestamp().AsTime().Sub(span.StartTimestamp().AsTime())),
					StatusCode:   extractStatusCode(span.Attributes()),
					Attributes:   attributesToMap(span.Attributes()),
				}

				// Wrap in EbpfEvent.
				event := &ebpfpb.EbpfEvent{
					Timestamp:   timestamppb.New(span.StartTimestamp().AsTime()),
					ServiceName: serviceName,
					Payload: &ebpfpb.EbpfEvent_BeylaTrace{
						BeylaTrace: traceSpan,
					},
				}

				events = append(events, event)
			}
		}
	}

	t.logger.Debug().Int("event_count", len(events)).Msg("Transformed OTLP traces")
	return events, nil
}

// transformHTTPMetric transforms HTTP duration metrics to BeylaHttpMetrics.
func (t *Transformer) transformHTTPMetric(metric pmetric.Metric, serviceName string) []*ebpfpb.EbpfEvent {
	var events []*ebpfpb.EbpfEvent

	// Handle histogram metrics.
	if metric.Type() == pmetric.MetricTypeHistogram {
		hist := metric.Histogram()
		for i := 0; i < hist.DataPoints().Len(); i++ {
			dp := hist.DataPoints().At(i)

			// Extract HTTP attributes.
			route := getStringAttribute(dp.Attributes(), "http.route", getStringAttribute(dp.Attributes(), "url.path", "/"))
			method := getStringAttribute(dp.Attributes(), "http.request.method", getStringAttribute(dp.Attributes(), "http.method", "GET"))
			statusCode := uint32(getIntAttribute(dp.Attributes(), "http.response.status_code", getIntAttribute(dp.Attributes(), "http.status_code", 200)))

			// Extract histogram buckets and counts.
			buckets := make([]float64, dp.ExplicitBounds().Len())
			for j := 0; j < dp.ExplicitBounds().Len(); j++ {
				buckets[j] = dp.ExplicitBounds().At(j)
			}

			counts := make([]uint64, dp.BucketCounts().Len())
			for j := 0; j < dp.BucketCounts().Len(); j++ {
				counts[j] = dp.BucketCounts().At(j)
			}

			// Create BeylaHttpMetrics.
			httpMetric := &ebpfpb.BeylaHttpMetrics{
				Timestamp:       timestamppb.New(dp.Timestamp().AsTime()),
				ServiceName:     serviceName,
				HttpRoute:       route,
				HttpMethod:      method,
				HttpStatusCode:  statusCode,
				LatencyBuckets:  buckets,
				LatencyCounts:   counts,
				RequestCount:    dp.Count(),
				Attributes:      attributesToMap(dp.Attributes()),
			}

			// Wrap in EbpfEvent.
			event := &ebpfpb.EbpfEvent{
				Timestamp:   timestamppb.New(dp.Timestamp().AsTime()),
				ServiceName: serviceName,
				Payload: &ebpfpb.EbpfEvent_BeylaHttp{
					BeylaHttp: httpMetric,
				},
			}

			events = append(events, event)
		}
	}

	return events
}

// transformGRPCMetric transforms gRPC duration metrics to BeylaGrpcMetrics.
func (t *Transformer) transformGRPCMetric(metric pmetric.Metric, serviceName string) []*ebpfpb.EbpfEvent {
	var events []*ebpfpb.EbpfEvent

	// Handle histogram metrics.
	if metric.Type() == pmetric.MetricTypeHistogram {
		hist := metric.Histogram()
		for i := 0; i < hist.DataPoints().Len(); i++ {
			dp := hist.DataPoints().At(i)

			// Extract gRPC attributes.
			method := getStringAttribute(dp.Attributes(), "rpc.method", "unknown")
			statusCode := uint32(getIntAttribute(dp.Attributes(), "rpc.grpc.status_code", 0))

			// Extract histogram buckets and counts.
			buckets := make([]float64, dp.ExplicitBounds().Len())
			for j := 0; j < dp.ExplicitBounds().Len(); j++ {
				buckets[j] = dp.ExplicitBounds().At(j)
			}

			counts := make([]uint64, dp.BucketCounts().Len())
			for j := 0; j < dp.BucketCounts().Len(); j++ {
				counts[j] = dp.BucketCounts().At(j)
			}

			// Create BeylaGrpcMetrics.
			grpcMetric := &ebpfpb.BeylaGrpcMetrics{
				Timestamp:       timestamppb.New(dp.Timestamp().AsTime()),
				ServiceName:     serviceName,
				GrpcMethod:      method,
				GrpcStatusCode:  statusCode,
				LatencyBuckets:  buckets,
				LatencyCounts:   counts,
				RequestCount:    dp.Count(),
				Attributes:      attributesToMap(dp.Attributes()),
			}

			// Wrap in EbpfEvent.
			event := &ebpfpb.EbpfEvent{
				Timestamp:   timestamppb.New(dp.Timestamp().AsTime()),
				ServiceName: serviceName,
				Payload: &ebpfpb.EbpfEvent_BeylaGrpc{
					BeylaGrpc: grpcMetric,
				},
			}

			events = append(events, event)
		}
	}

	return events
}

// transformSQLMetric transforms SQL duration metrics to BeylaSqlMetrics.
func (t *Transformer) transformSQLMetric(metric pmetric.Metric, serviceName string) []*ebpfpb.EbpfEvent {
	var events []*ebpfpb.EbpfEvent

	// Handle histogram metrics.
	if metric.Type() == pmetric.MetricTypeHistogram {
		hist := metric.Histogram()
		for i := 0; i < hist.DataPoints().Len(); i++ {
			dp := hist.DataPoints().At(i)

			// Extract SQL attributes.
			operation := getStringAttribute(dp.Attributes(), "db.operation", "QUERY")
			table := getStringAttribute(dp.Attributes(), "db.sql.table", "")

			// Extract histogram buckets and counts.
			buckets := make([]float64, dp.ExplicitBounds().Len())
			for j := 0; j < dp.ExplicitBounds().Len(); j++ {
				buckets[j] = dp.ExplicitBounds().At(j)
			}

			counts := make([]uint64, dp.BucketCounts().Len())
			for j := 0; j < dp.BucketCounts().Len(); j++ {
				counts[j] = dp.BucketCounts().At(j)
			}

			// Create BeylaSqlMetrics.
			sqlMetric := &ebpfpb.BeylaSqlMetrics{
				Timestamp:      timestamppb.New(dp.Timestamp().AsTime()),
				ServiceName:    serviceName,
				SqlOperation:   operation,
				TableName:      table,
				LatencyBuckets: buckets,
				LatencyCounts:  counts,
				QueryCount:     dp.Count(),
				Attributes:     attributesToMap(dp.Attributes()),
			}

			// Wrap in EbpfEvent.
			event := &ebpfpb.EbpfEvent{
				Timestamp:   timestamppb.New(dp.Timestamp().AsTime()),
				ServiceName: serviceName,
				Payload: &ebpfpb.EbpfEvent_BeylaSql{
					BeylaSql: sqlMetric,
				},
			}

			events = append(events, event)
		}
	}

	return events
}

// Helper functions for attribute extraction.

// getStringAttribute gets a string attribute from OTLP attributes with a default value.
func getStringAttribute(attrs pcommon.Map, key string, defaultValue string) string {
	if val, ok := attrs.Get(key); ok {
		return val.Str()
	}
	return defaultValue
}

// getIntAttribute gets an int attribute from OTLP attributes with a default value.
func getIntAttribute(attrs pcommon.Map, key string, defaultValue int64) int64 {
	if val, ok := attrs.Get(key); ok {
		return val.Int()
	}
	return defaultValue
}

// attributesToMap converts OTLP attributes to map[string]string.
func attributesToMap(attrs pcommon.Map) map[string]string {
	result := make(map[string]string)
	attrs.Range(func(k string, v pcommon.Value) bool {
		result[k] = v.AsString()
		return true
	})
	return result
}

// spanKindToString converts OTLP span kind to string.
func spanKindToString(kind ptrace.SpanKind) string {
	switch kind {
	case ptrace.SpanKindUnspecified:
		return "unspecified"
	case ptrace.SpanKindInternal:
		return "internal"
	case ptrace.SpanKindServer:
		return "server"
	case ptrace.SpanKindClient:
		return "client"
	case ptrace.SpanKindProducer:
		return "producer"
	case ptrace.SpanKindConsumer:
		return "consumer"
	default:
		return "unspecified"
	}
}

// extractStatusCode extracts HTTP/gRPC status code from span attributes.
func extractStatusCode(attrs pcommon.Map) uint32 {
	// Try HTTP status code first.
	if val, ok := attrs.Get("http.response.status_code"); ok {
		return uint32(val.Int())
	}
	if val, ok := attrs.Get("http.status_code"); ok {
		return uint32(val.Int())
	}

	// Try gRPC status code.
	if val, ok := attrs.Get("rpc.grpc.status_code"); ok {
		return uint32(val.Int())
	}

	return 0
}
