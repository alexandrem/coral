package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/coral-mesh/coral/internal/colony"
	"github.com/coral-mesh/coral/internal/colony/database"
)

// generateInputSchema generates a JSON schema from a Go type.
func generateInputSchema(inputType interface{}) (map[string]any, error) {
	// Use reflector without $ref/$defs to get inline schema that LLMs can understand.
	reflector := jsonschema.Reflector{
		DoNotReference: true, // Inline all schemas instead of using $ref/$defs
	}
	schema := reflector.Reflect(inputType)

	schemaBytes, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal schema: %w", err)
	}

	var schemaMap map[string]any
	if err := json.Unmarshal(schemaBytes, &schemaMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal schema: %w", err)
	}

	// Remove JSON Schema draft-specific fields that MCP clients don't expect.
	// The MCP protocol expects a simpler schema format with just:
	// type, properties, required, and property-level constraints.
	delete(schemaMap, "$schema")
	delete(schemaMap, "$id")

	// Note: We keep additionalProperties as it's useful for validation.

	return schemaMap, nil
}

// registerToolWithSchema is a helper that generates schema, creates tool, and registers it with logging.
func (s *Server) registerToolWithSchema(
	name string,
	description string,
	inputType interface{},
	handler func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error),
) {
	if !s.isToolEnabled(name) {
		return
	}

	// Generate JSON schema.
	inputSchema, err := generateInputSchema(inputType)
	if err != nil {
		s.logger.Error().Err(err).Str("tool", name).Msg("Failed to generate input schema")
		return
	}

	// Marshal schema to JSON bytes for MCP.
	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Str("tool", name).Msg("Failed to marshal schema")
		return
	}

	// Create tool with raw schema.
	tool := mcp.NewToolWithRawSchema(name, description, schemaBytes)

	// Debug: Verify tool IMMEDIATELY after creation
	s.logger.Info().
		Str("tool", name).
		Int("schemaBytes_len", len(schemaBytes)).
		Int("tool.RawInputSchema_len", len(tool.RawInputSchema)).
		Str("tool.InputSchema.Type", tool.InputSchema.Type).
		Msg("Tool created")

	// Debug: Marshal the tool to see what it would look like when serialized
	testMarshal, _ := json.Marshal(tool)
	s.logger.Info().
		Str("tool", name).
		RawJSON("marshaledTool", testMarshal).
		Msg("Tool after marshal test")

	// Register tool handler.
	s.mcpServer.AddTool(tool, handler)

	// Debug: Try to retrieve the tool from the server and marshal it
	// This will help us see if the tool is corrupted after AddTool
	s.logger.Info().
		Str("tool", name).
		Msg("Tool registered with MCP server")
}

func matchesPattern(s, pattern string) bool {
	if pattern == "" {
		return true
	}
	// Simple wildcard matching - just check if pattern is a prefix/suffix.
	if pattern == "*" {
		return true
	}
	if len(pattern) > 0 && pattern[len(pattern)-1] == '*' {
		prefix := pattern[:len(pattern)-1]
		return len(s) >= len(prefix) && s[:len(prefix)] == prefix
	}
	if len(pattern) > 0 && pattern[0] == '*' {
		suffix := pattern[1:]
		return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
	}
	return s == pattern
}

// formatDuration formats a duration in human-readable form.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	days := int(d.Hours() / 24)
	return fmt.Sprintf("%dd", days)
}

// parseTimeRange parses a time range string like "1h", "30m", "24h" into start and end times.
func parseTimeRange(timeRange string) (time.Time, time.Time, error) {
	duration, err := time.ParseDuration(timeRange)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid duration: %w", err)
	}

	endTime := time.Now()
	startTime := endTime.Add(-duration)

	return startTime, endTime, nil
}

// HTTPMetricsStats represents aggregated HTTP metrics statistics.
type HTTPMetricsStats struct {
	TotalRequests int64
	P50           float64
	P95           float64
	P99           float64
	StatusCodes   map[int]int64
}

// aggregateHTTPMetrics calculates statistics from HTTP metric histogram buckets.
func aggregateHTTPMetrics(results []*database.BeylaHTTPMetricResult) HTTPMetricsStats {
	stats := HTTPMetricsStats{
		StatusCodes: make(map[int]int64),
	}

	// Collect all latency data points weighted by count.
	var totalCount int64
	var allLatencies []float64
	var allWeights []int64

	for _, r := range results {
		totalCount += r.Count
		stats.StatusCodes[r.HTTPStatusCode] += r.Count

		// Add this bucket's data points.
		allLatencies = append(allLatencies, r.LatencyBucketMs)
		allWeights = append(allWeights, r.Count)
	}

	stats.TotalRequests = totalCount

	// Calculate percentiles from weighted histogram.
	if len(allLatencies) > 0 {
		stats.P50 = calculatePercentile(allLatencies, allWeights, 0.50)
		stats.P95 = calculatePercentile(allLatencies, allWeights, 0.95)
		stats.P99 = calculatePercentile(allLatencies, allWeights, 0.99)
	}

	return stats
}

// calculatePercentile calculates a percentile from weighted data.
func calculatePercentile(values []float64, weights []int64, percentile float64) float64 {
	if len(values) == 0 || len(values) != len(weights) {
		return 0
	}

	// Calculate total weight.
	var totalWeight int64
	for _, w := range weights {
		totalWeight += w
	}

	if totalWeight == 0 {
		return 0
	}

	// Find the target cumulative weight.
	targetWeight := float64(totalWeight) * percentile

	// Find the bucket where we cross the target.
	var cumulativeWeight int64
	for i, v := range values {
		cumulativeWeight += weights[i]
		if float64(cumulativeWeight) >= targetWeight {
			return v
		}
	}

	// Return the last value if we didn't find it.
	return values[len(values)-1]
}

// RouteStats represents statistics for a single route.
type RouteStats struct {
	Route string
	Count int64
}

// getTopRoutes returns the top N routes by request count.
func getTopRoutes(results []*database.BeylaHTTPMetricResult, topN int) []RouteStats {
	routeCounts := make(map[string]int64)

	for _, r := range results {
		routeCounts[r.HTTPRoute] += r.Count
	}

	// Convert to slice and sort.
	routes := make([]RouteStats, 0, len(routeCounts))
	for route, count := range routeCounts {
		routes = append(routes, RouteStats{Route: route, Count: count})
	}

	// Sort by count descending.
	for i := 0; i < len(routes); i++ {
		for j := i + 1; j < len(routes); j++ {
			if routes[j].Count > routes[i].Count {
				routes[i], routes[j] = routes[j], routes[i]
			}
		}
	}

	// Return top N.
	if len(routes) > topN {
		routes = routes[:topN]
	}

	return routes
}

// GRPCMetricsStats represents aggregated gRPC metrics statistics.
type GRPCMetricsStats struct {
	TotalRPCs   int64
	P50         float64
	P95         float64
	P99         float64
	StatusCodes map[int]int64
}

// aggregateGRPCMetrics calculates statistics from gRPC metric histogram buckets.
func aggregateGRPCMetrics(results []*database.BeylaGRPCMetricResult) GRPCMetricsStats {
	stats := GRPCMetricsStats{
		StatusCodes: make(map[int]int64),
	}

	var totalCount int64
	var allLatencies []float64
	var allWeights []int64

	for _, r := range results {
		totalCount += r.Count
		stats.StatusCodes[r.GRPCStatusCode] += r.Count

		allLatencies = append(allLatencies, r.LatencyBucketMs)
		allWeights = append(allWeights, r.Count)
	}

	stats.TotalRPCs = totalCount

	if len(allLatencies) > 0 {
		stats.P50 = calculatePercentile(allLatencies, allWeights, 0.50)
		stats.P95 = calculatePercentile(allLatencies, allWeights, 0.95)
		stats.P99 = calculatePercentile(allLatencies, allWeights, 0.99)
	}

	return stats
}

// grpcStatusName returns the human-readable name for a gRPC status code.
func grpcStatusName(code int) string {
	names := map[int]string{
		0:  "OK",
		1:  "CANCELLED",
		2:  "UNKNOWN",
		3:  "INVALID_ARGUMENT",
		4:  "DEADLINE_EXCEEDED",
		5:  "NOT_FOUND",
		6:  "ALREADY_EXISTS",
		7:  "PERMISSION_DENIED",
		8:  "RESOURCE_EXHAUSTED",
		9:  "FAILED_PRECONDITION",
		10: "ABORTED",
		11: "OUT_OF_RANGE",
		12: "UNIMPLEMENTED",
		13: "INTERNAL",
		14: "UNAVAILABLE",
		15: "DATA_LOSS",
		16: "UNAUTHENTICATED",
	}
	if name, ok := names[code]; ok {
		return name
	}
	return "UNKNOWN"
}

// MethodStats represents statistics for a single gRPC method.
type MethodStats struct {
	Method string
	Count  int64
}

// getTopGRPCMethods returns the top N gRPC methods by request count.
func getTopGRPCMethods(results []*database.BeylaGRPCMetricResult, topN int) []MethodStats {
	methodCounts := make(map[string]int64)

	for _, r := range results {
		methodCounts[r.GRPCMethod] += r.Count
	}

	methods := make([]MethodStats, 0, len(methodCounts))
	for method, count := range methodCounts {
		methods = append(methods, MethodStats{Method: method, Count: count})
	}

	// Sort by count descending.
	for i := 0; i < len(methods); i++ {
		for j := i + 1; j < len(methods); j++ {
			if methods[j].Count > methods[i].Count {
				methods[i], methods[j] = methods[j], methods[i]
			}
		}
	}

	if len(methods) > topN {
		methods = methods[:topN]
	}

	return methods
}

// SQLMetricsStats represents aggregated SQL metrics statistics.
type SQLMetricsStats struct {
	TotalQueries int64
	P50          float64
	P95          float64
	P99          float64
	Operations   map[string]int64
}

// aggregateSQLMetrics calculates statistics from SQL metric histogram buckets.
func aggregateSQLMetrics(results []*database.BeylaSQLMetricResult) SQLMetricsStats {
	stats := SQLMetricsStats{
		Operations: make(map[string]int64),
	}

	var totalCount int64
	var allLatencies []float64
	var allWeights []int64

	for _, r := range results {
		totalCount += r.Count
		stats.Operations[r.SQLOperation] += r.Count

		allLatencies = append(allLatencies, r.LatencyBucketMs)
		allWeights = append(allWeights, r.Count)
	}

	stats.TotalQueries = totalCount

	if len(allLatencies) > 0 {
		stats.P50 = calculatePercentile(allLatencies, allWeights, 0.50)
		stats.P95 = calculatePercentile(allLatencies, allWeights, 0.95)
		stats.P99 = calculatePercentile(allLatencies, allWeights, 0.99)
	}

	return stats
}

// TableStats represents statistics for a single SQL table.
type TableStats struct {
	Table string
	Count int64
}

// getTopSQLTables returns the top N tables by query count.
func getTopSQLTables(results []*database.BeylaSQLMetricResult, topN int) []TableStats {
	tableCounts := make(map[string]int64)

	for _, r := range results {
		tableCounts[r.TableName] += r.Count
	}

	tables := make([]TableStats, 0, len(tableCounts))
	for table, count := range tableCounts {
		tables = append(tables, TableStats{Table: table, Count: count})
	}

	// Sort by count descending.
	for i := 0; i < len(tables); i++ {
		for j := i + 1; j < len(tables); j++ {
			if tables[j].Count > tables[i].Count {
				tables[i], tables[j] = tables[j], tables[i]
			}
		}
	}

	if len(tables) > topN {
		tables = tables[:topN]
	}

	return tables
}

// getUniqueServices returns a comma-separated list of unique service names from trace spans.
func getUniqueServices(spans []*database.BeylaTraceResult) string {
	serviceSet := make(map[string]bool)
	for _, span := range spans {
		serviceSet[span.ServiceName] = true
	}

	services := make([]string, 0, len(serviceSet))
	for service := range serviceSet {
		services = append(services, service)
	}

	result := ""
	for i, service := range services {
		if i > 0 {
			result += ", "
		}
		result += service
	}

	return result
}

// getTopSlowestSpans returns the N slowest spans sorted by duration.
func getTopSlowestSpans(spans []*database.BeylaTraceResult, topN int) []*database.BeylaTraceResult {
	// Sort by duration descending.
	sorted := make([]*database.BeylaTraceResult, len(spans))
	copy(sorted, spans)

	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].DurationUs > sorted[i].DurationUs {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	if len(sorted) > topN {
		sorted = sorted[:topN]
	}

	return sorted
}

// TelemetryStats represents aggregated OTLP telemetry statistics.
type TelemetryStats struct {
	TotalSpans   int32
	ErrorCount   int32
	ErrorRate    float64
	P50          float64
	P95          float64
	P99          float64
	SpanKinds    map[string]int32
	SampleTraces []string
}

// aggregateTelemetrySummaries aggregates multiple telemetry summaries into stats.
func aggregateTelemetrySummaries(summaries []database.TelemetrySummary, operationFilter *string) TelemetryStats {
	stats := TelemetryStats{
		SpanKinds: make(map[string]int32),
	}

	var totalSpans int32
	var totalErrors int32
	var p50Sum, p95Sum, p99Sum float64
	var summaryCount int

	traceSet := make(map[string]bool)

	for _, summary := range summaries {
		// Apply operation filter if specified.
		if operationFilter != nil && *operationFilter != "" {
			// SpanKind is used as a proxy for operation filtering in summaries.
			if summary.SpanKind != *operationFilter {
				continue
			}
		}

		totalSpans += summary.TotalSpans
		totalErrors += summary.ErrorCount

		// Accumulate percentiles for averaging.
		p50Sum += summary.P50Ms
		p95Sum += summary.P95Ms
		p99Sum += summary.P99Ms
		summaryCount++

		// Count span kinds.
		stats.SpanKinds[summary.SpanKind] += summary.TotalSpans

		// Collect sample traces.
		for _, traceID := range summary.SampleTraces {
			if !traceSet[traceID] {
				traceSet[traceID] = true
				stats.SampleTraces = append(stats.SampleTraces, traceID)
			}
		}
	}

	stats.TotalSpans = totalSpans
	stats.ErrorCount = totalErrors

	if totalSpans > 0 {
		stats.ErrorRate = float64(totalErrors) / float64(totalSpans) * 100
	}

	// Calculate average percentiles across summaries.
	if summaryCount > 0 {
		stats.P50 = p50Sum / float64(summaryCount)
		stats.P95 = p95Sum / float64(summaryCount)
		stats.P99 = p99Sum / float64(summaryCount)
	}

	return stats
}

// Unified Tools (RFD 067)

// registerUnifiedSummaryTool registers the coral_query_summary tool.
func (s *Server) registerUnifiedSummaryTool() {
	s.registerToolWithSchema(
		"coral_query_summary",
		"Get a high-level health summary for services, combining eBPF and OTLP data.",
		UnifiedSummaryInput{},
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var input UnifiedSummaryInput
			if request.Params.Arguments != nil {
				argBytes, err := json.Marshal(request.Params.Arguments)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
				}
				if err := json.Unmarshal(argBytes, &input); err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
				}
			}

			s.auditToolCall("coral_query_summary", input)

			timeRangeStr := "5m"
			if input.TimeRange != nil {
				timeRangeStr = *input.TimeRange
			}
			startTime, endTime, err := parseTimeRange(timeRangeStr)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid time_range: %v", err)), nil
			}

			serviceName := ""
			if input.Service != nil {
				serviceName = *input.Service
			}

			ebpfService := colony.NewEbpfQueryService(s.db)
			results, err := ebpfService.QueryUnifiedSummary(ctx, serviceName, startTime, endTime)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to query summary: %v", err)), nil
			}

			text := "Service Health Summary:\n\n"
			for _, r := range results {
				text += fmt.Sprintf("Service: %s, Status: %s\n", r.ServiceName, r.Status)
			}

			return mcp.NewToolResultText(text), nil
		})
}

// registerUnifiedTracesTool registers the coral_query_traces tool.
func (s *Server) registerUnifiedTracesTool() {
	s.registerToolWithSchema(
		"coral_query_traces",
		"Query distributed traces from all sources (eBPF + OTLP).",
		UnifiedTracesInput{},
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var input UnifiedTracesInput
			if request.Params.Arguments != nil {
				argBytes, err := json.Marshal(request.Params.Arguments)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
				}
				if err := json.Unmarshal(argBytes, &input); err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
				}
			}

			s.auditToolCall("coral_query_traces", input)

			timeRangeStr := "1h"
			if input.TimeRange != nil {
				timeRangeStr = *input.TimeRange
			}
			startTime, endTime, err := parseTimeRange(timeRangeStr)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid time_range: %v", err)), nil
			}

			serviceName := ""
			if input.Service != nil {
				serviceName = *input.Service
			}

			traceID := ""
			if input.TraceID != nil {
				traceID = *input.TraceID
			}

			ebpfService := colony.NewEbpfQueryService(s.db)
			spans, err := ebpfService.QueryUnifiedTraces(ctx, traceID, serviceName, startTime, endTime, 0, 10)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to query traces: %v", err)), nil
			}

			text := fmt.Sprintf("Found %d spans.\n", len(spans))
			return mcp.NewToolResultText(text), nil
		})
}

// registerUnifiedMetricsTool registers the coral_query_metrics tool.
func (s *Server) registerUnifiedMetricsTool() {
	s.registerToolWithSchema(
		"coral_query_metrics",
		"Query metrics from all sources (eBPF + OTLP).",
		UnifiedMetricsInput{},
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var input UnifiedMetricsInput
			if request.Params.Arguments != nil {
				argBytes, err := json.Marshal(request.Params.Arguments)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
				}
				if err := json.Unmarshal(argBytes, &input); err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
				}
			}

			s.auditToolCall("coral_query_metrics", input)

			timeRangeStr := "1h"
			if input.TimeRange != nil {
				timeRangeStr = *input.TimeRange
			}
			startTime, endTime, err := parseTimeRange(timeRangeStr)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid time_range: %v", err)), nil
			}

			serviceName := ""
			if input.Service != nil {
				serviceName = *input.Service
			}

			ebpfService := colony.NewEbpfQueryService(s.db)
			metrics, err := ebpfService.QueryUnifiedMetrics(ctx, serviceName, startTime, endTime)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to query metrics: %v", err)), nil
			}

			text := fmt.Sprintf("Metrics for %s:\n", serviceName)
			if len(metrics.HttpMetrics) > 0 {
				text += fmt.Sprintf("HTTP Metrics: %d\n", len(metrics.HttpMetrics))
			}
			return mcp.NewToolResultText(text), nil
		})
}

// registerUnifiedLogsTool registers the coral_query_logs tool.
func (s *Server) registerUnifiedLogsTool() {
	s.registerToolWithSchema(
		"coral_query_logs",
		"Query logs from OTLP.",
		UnifiedLogsInput{},
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var input UnifiedLogsInput
			if request.Params.Arguments != nil {
				argBytes, err := json.Marshal(request.Params.Arguments)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
				}
				if err := json.Unmarshal(argBytes, &input); err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
				}
			}

			s.auditToolCall("coral_query_logs", input)

			text := "Logs query not implemented yet."
			return mcp.NewToolResultText(text), nil
		})
}

// executeUnifiedSummaryTool executes the coral_query_summary tool.
func (s *Server) executeUnifiedSummaryTool(ctx context.Context, argsJSON string) (string, error) {
	var input UnifiedSummaryInput
	if err := json.Unmarshal([]byte(argsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_query_summary", input)

	timeRangeStr := "5m"
	if input.TimeRange != nil {
		timeRangeStr = *input.TimeRange
	}
	startTime, endTime, err := parseTimeRange(timeRangeStr)
	if err != nil {
		return "", fmt.Errorf("invalid time_range: %w", err)
	}

	serviceName := ""
	if input.Service != nil {
		serviceName = *input.Service
	}

	ebpfService := colony.NewEbpfQueryService(s.db)
	results, err := ebpfService.QueryUnifiedSummary(ctx, serviceName, startTime, endTime)
	if err != nil {
		return "", fmt.Errorf("failed to query summary: %w", err)
	}

	text := "Service Health Summary:\n\n"
	for _, r := range results {
		text += fmt.Sprintf("Service: %s, Status: %s\n", r.ServiceName, r.Status)
	}

	return text, nil
}

// executeUnifiedTracesTool executes the coral_query_traces tool.
func (s *Server) executeUnifiedTracesTool(ctx context.Context, argsJSON string) (string, error) {
	var input UnifiedTracesInput
	if err := json.Unmarshal([]byte(argsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_query_traces", input)

	timeRangeStr := "1h"
	if input.TimeRange != nil {
		timeRangeStr = *input.TimeRange
	}
	startTime, endTime, err := parseTimeRange(timeRangeStr)
	if err != nil {
		return "", fmt.Errorf("invalid time_range: %w", err)
	}

	serviceName := ""
	if input.Service != nil {
		serviceName = *input.Service
	}

	traceID := ""
	if input.TraceID != nil {
		traceID = *input.TraceID
	}

	ebpfService := colony.NewEbpfQueryService(s.db)
	spans, err := ebpfService.QueryUnifiedTraces(ctx, traceID, serviceName, startTime, endTime, 0, 10)
	if err != nil {
		return "", fmt.Errorf("failed to query traces: %w", err)
	}

	text := fmt.Sprintf("Found %d spans.\n", len(spans))
	return text, nil
}

// executeUnifiedMetricsTool executes the coral_query_metrics tool.
func (s *Server) executeUnifiedMetricsTool(ctx context.Context, argsJSON string) (string, error) {
	var input UnifiedMetricsInput
	if err := json.Unmarshal([]byte(argsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_query_metrics", input)

	timeRangeStr := "1h"
	if input.TimeRange != nil {
		timeRangeStr = *input.TimeRange
	}
	startTime, endTime, err := parseTimeRange(timeRangeStr)
	if err != nil {
		return "", fmt.Errorf("invalid time_range: %w", err)
	}

	serviceName := ""
	if input.Service != nil {
		serviceName = *input.Service
	}

	ebpfService := colony.NewEbpfQueryService(s.db)
	metrics, err := ebpfService.QueryUnifiedMetrics(ctx, serviceName, startTime, endTime)
	if err != nil {
		return "", fmt.Errorf("failed to query metrics: %w", err)
	}

	text := fmt.Sprintf("Metrics for %s:\n", serviceName)
	if len(metrics.HttpMetrics) > 0 {
		text += fmt.Sprintf("HTTP Metrics: %d\n", len(metrics.HttpMetrics))
	}
	return text, nil
}

// executeUnifiedLogsTool executes the coral_query_logs tool.
func (s *Server) executeUnifiedLogsTool(ctx context.Context, argsJSON string) (string, error) {
	var input UnifiedLogsInput
	if err := json.Unmarshal([]byte(argsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_query_logs", input)

	text := "Logs query not implemented yet."
	return text, nil
}
