package colony

import (
	"context"
	"time"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/safe"
)

func (s *EbpfQueryService) queryHTTPMetrics(ctx context.Context, req *agentv1.QueryEbpfMetricsRequest, startTime, endTime time.Time) ([]*agentv1.EbpfHttpMetric, error) {
	// If no service names specified, query all services.
	serviceNames := req.ServiceNames
	if len(serviceNames) == 0 {
		serviceNames = []string{""} // Empty string queries all services
	}

	// Map to aggregate metrics by service+method+route+status.
	type metricKey struct {
		serviceName string
		method      string
		route       string
		statusCode  int
	}
	aggregated := make(map[metricKey]*agentv1.EbpfHttpMetric)

	for _, serviceName := range serviceNames {
		filters := make(map[string]string)
		results, err := s.db.QueryBeylaHTTPMetrics(ctx, serviceName, startTime, endTime, filters)
		if err != nil {
			return nil, err
		}

		// Aggregate bucket data into histograms.
		for _, r := range results {
			key := metricKey{
				serviceName: r.ServiceName,
				method:      r.HTTPMethod,
				route:       r.HTTPRoute,
				statusCode:  r.HTTPStatusCode,
			}

			metric, exists := aggregated[key]
			if !exists {
				statusCode, clamped := safe.IntToUint32(r.HTTPStatusCode)
				if clamped {
					s.logger.Warn().Str("service", r.ServiceName).Int("status_code", r.HTTPStatusCode).Msg("unexpected HTTP status code value, clamped")
				}
				metric = &agentv1.EbpfHttpMetric{
					Timestamp:      r.LastSeen.UnixMilli(),
					ServiceName:    r.ServiceName,
					HttpMethod:     r.HTTPMethod,
					HttpRoute:      r.HTTPRoute,
					HttpStatusCode: statusCode,
					LatencyBuckets: []float64{},
					LatencyCounts:  []uint64{},
					RequestCount:   0,
				}
				aggregated[key] = metric
			}

			// Add bucket and count.
			count, clamped := safe.Int64ToUint64(r.Count)
			if clamped {
				s.logger.Warn().Str("service", r.ServiceName).Int64("count", r.Count).Msg("negative HTTP metric count in database, clamped to zero")
			}
			metric.LatencyBuckets = append(metric.LatencyBuckets, r.LatencyBucketMs)
			metric.LatencyCounts = append(metric.LatencyCounts, count)
			metric.RequestCount += count
		}
	}

	// Convert map to slice.
	allMetrics := make([]*agentv1.EbpfHttpMetric, 0, len(aggregated))
	for _, metric := range aggregated {
		allMetrics = append(allMetrics, metric)
	}

	return allMetrics, nil
}

func (s *EbpfQueryService) queryGRPCMetrics(ctx context.Context, req *agentv1.QueryEbpfMetricsRequest, startTime, endTime time.Time) ([]*agentv1.EbpfGrpcMetric, error) {
	serviceNames := req.ServiceNames
	if len(serviceNames) == 0 {
		serviceNames = []string{""}
	}

	// Map to aggregate metrics by service+method+status.
	type metricKey struct {
		serviceName string
		method      string
		statusCode  int
	}
	aggregated := make(map[metricKey]*agentv1.EbpfGrpcMetric)

	for _, serviceName := range serviceNames {
		filters := make(map[string]string)
		results, err := s.db.QueryBeylaGRPCMetrics(ctx, serviceName, startTime, endTime, filters)
		if err != nil {
			return nil, err
		}

		// Aggregate bucket data into histograms.
		for _, r := range results {
			key := metricKey{
				serviceName: r.ServiceName,
				method:      r.GRPCMethod,
				statusCode:  r.GRPCStatusCode,
			}

			metric, exists := aggregated[key]
			if !exists {
				statusCode, clamped := safe.IntToUint32(r.GRPCStatusCode)
				if clamped {
					s.logger.Warn().Str("service", r.ServiceName).Int("status_code", r.GRPCStatusCode).Msg("unexpected gRPC status code value, clamped")
				}
				metric = &agentv1.EbpfGrpcMetric{
					Timestamp:      r.LastSeen.UnixMilli(),
					ServiceName:    r.ServiceName,
					GrpcMethod:     r.GRPCMethod,
					GrpcStatusCode: statusCode,
					LatencyBuckets: []float64{},
					LatencyCounts:  []uint64{},
					RequestCount:   0,
				}
				aggregated[key] = metric
			}

			// Add bucket and count.
			count, clamped := safe.Int64ToUint64(r.Count)
			if clamped {
				s.logger.Warn().Str("service", r.ServiceName).Int64("count", r.Count).Msg("negative gRPC metric count in database, clamped to zero")
			}
			metric.LatencyBuckets = append(metric.LatencyBuckets, r.LatencyBucketMs)
			metric.LatencyCounts = append(metric.LatencyCounts, count)
			metric.RequestCount += count
		}
	}

	// Convert map to slice.
	allMetrics := make([]*agentv1.EbpfGrpcMetric, 0, len(aggregated))
	for _, metric := range aggregated {
		allMetrics = append(allMetrics, metric)
	}

	return allMetrics, nil
}

func (s *EbpfQueryService) querySQLMetrics(ctx context.Context, req *agentv1.QueryEbpfMetricsRequest, startTime, endTime time.Time) ([]*agentv1.EbpfSqlMetric, error) {
	serviceNames := req.ServiceNames
	if len(serviceNames) == 0 {
		serviceNames = []string{""}
	}

	// Map to aggregate metrics by service+operation+table.
	type metricKey struct {
		serviceName string
		operation   string
		tableName   string
	}
	aggregated := make(map[metricKey]*agentv1.EbpfSqlMetric)

	for _, serviceName := range serviceNames {
		filters := make(map[string]string)
		results, err := s.db.QueryBeylaSQLMetrics(ctx, serviceName, startTime, endTime, filters)
		if err != nil {
			return nil, err
		}

		// Aggregate bucket data into histograms.
		for _, r := range results {
			key := metricKey{
				serviceName: r.ServiceName,
				operation:   r.SQLOperation,
				tableName:   r.TableName,
			}

			metric, exists := aggregated[key]
			if !exists {
				metric = &agentv1.EbpfSqlMetric{
					Timestamp:      r.LastSeen.UnixMilli(),
					ServiceName:    r.ServiceName,
					SqlOperation:   r.SQLOperation,
					TableName:      r.TableName,
					LatencyBuckets: []float64{},
					LatencyCounts:  []uint64{},
					QueryCount:     0,
				}
				aggregated[key] = metric
			}

			// Add bucket and count.
			count, clamped := safe.Int64ToUint64(r.Count)
			if clamped {
				s.logger.Warn().Str("service", r.ServiceName).Int64("count", r.Count).Msg("negative SQL metric count in database, clamped to zero")
			}
			metric.LatencyBuckets = append(metric.LatencyBuckets, r.LatencyBucketMs)
			metric.LatencyCounts = append(metric.LatencyCounts, count)
			metric.QueryCount += count
		}
	}

	// Convert map to slice.
	allMetrics := make([]*agentv1.EbpfSqlMetric, 0, len(aggregated))
	for _, metric := range aggregated {
		allMetrics = append(allMetrics, metric)
	}

	return allMetrics, nil
}

func (s *EbpfQueryService) queryTraceSpans(ctx context.Context, req *agentv1.QueryEbpfMetricsRequest, startTime, endTime time.Time) ([]*agentv1.EbpfTraceSpan, error) {
	var allSpans []*agentv1.EbpfTraceSpan

	// If trace ID is specified, query by trace ID only (ignore service filter for efficiency).
	if req.TraceId != "" {
		maxTraces := int(req.MaxTraces)
		if maxTraces == 0 {
			maxTraces = 100
		}

		results, err := s.db.QueryBeylaTraces(ctx, req.TraceId, "", startTime, endTime, 0, maxTraces)
		if err != nil {
			return nil, err
		}

		for _, r := range results {
			allSpans = append(allSpans, &agentv1.EbpfTraceSpan{
				TraceId:      r.TraceID,
				SpanId:       r.SpanID,
				ParentSpanId: r.ParentSpanID,
				ServiceName:  r.ServiceName,
				SpanName:     r.SpanName,
				SpanKind:     r.SpanKind,
				StartTime:    r.StartTime.UnixMilli(),
				DurationUs:   r.DurationUs,
				StatusCode: func() uint32 {
					v, clamped := safe.IntToUint32(r.StatusCode)
					if clamped {
						s.logger.Warn().Str("trace_id", r.TraceID).Int("status_code", r.StatusCode).Msg("unexpected trace status code value, clamped")
					}
					return v
				}(),
			})
		}

		return allSpans, nil
	}

	// Otherwise, query by service names.
	serviceNames := req.ServiceNames
	if len(serviceNames) == 0 {
		serviceNames = []string{""}
	}

	for _, serviceName := range serviceNames {
		// Use max traces from request, default to 100.
		maxTraces := int(req.MaxTraces)
		if maxTraces == 0 {
			maxTraces = 100
		}

		results, err := s.db.QueryBeylaTraces(ctx, "", serviceName, startTime, endTime, 0, maxTraces)
		if err != nil {
			return nil, err
		}

		for _, r := range results {
			allSpans = append(allSpans, &agentv1.EbpfTraceSpan{
				TraceId:      r.TraceID,
				SpanId:       r.SpanID,
				ParentSpanId: r.ParentSpanID,
				ServiceName:  r.ServiceName,
				SpanName:     r.SpanName,
				SpanKind:     r.SpanKind,
				StartTime:    r.StartTime.UnixMilli(),
				DurationUs:   r.DurationUs,
				StatusCode: func() uint32 {
					v, clamped := safe.IntToUint32(r.StatusCode)
					if clamped {
						s.logger.Warn().Str("trace_id", r.TraceID).Int("status_code", r.StatusCode).Msg("unexpected trace status code value, clamped")
					}
					return v
				}(),
			})
		}
	}

	return allSpans, nil
}
