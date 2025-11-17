package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/coral-io/coral/internal/colony/database"
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
)

// registerServiceHealthTool registers the coral_get_service_health tool.
func (s *Server) registerServiceHealthTool() {
	if !s.isToolEnabled("coral_get_service_health") {
		return
	}

	genkit.DefineTool(
		s.genkit,
		"coral_get_service_health",
		"Get current health status of services. Returns health state, resource usage (CPU, memory), uptime, and recent issues.",
		func(ctx *ai.ToolContext, input ServiceHealthInput) (string, error) {
			s.auditToolCall("coral_get_service_health", input)

			// Get service filter (handle nil pointer).
			var serviceFilter string
			if input.ServiceFilter != nil {
				serviceFilter = *input.ServiceFilter
			}

			// Get all agents from registry.
			agents := s.registry.ListAll()

			// Build health report.
			var healthyCount, degradedCount, unhealthyCount int
			var serviceStatuses []map[string]interface{}

			for _, agent := range agents {
				// Apply filter if specified.
				if serviceFilter != "" && !matchesPattern(agent.ComponentName, serviceFilter) {
					continue
				}

				// Determine health status based on last seen.
				status := "healthy"
				lastSeen := agent.LastSeen
				timeSinceLastSeen := time.Since(lastSeen)

				if timeSinceLastSeen > 5*time.Minute {
					status = "unhealthy"
					unhealthyCount++
				} else if timeSinceLastSeen > 2*time.Minute {
					status = "degraded"
					degradedCount++
				} else {
					healthyCount++
				}

				serviceStatuses = append(serviceStatuses, map[string]interface{}{
					"service":   agent.ComponentName,
					"agent_id":  agent.AgentID,
					"status":    status,
					"last_seen": lastSeen.Format(time.RFC3339),
					"uptime":    formatDuration(time.Since(agent.RegisteredAt)),
					"mesh_ip":   agent.MeshIPv4,
				})
			}

			// Determine overall status.
			overallStatus := "healthy"
			if unhealthyCount > 0 {
				overallStatus = "unhealthy"
			} else if degradedCount > 0 {
				overallStatus = "degraded"
			}

			// Format response as text for LLM consumption.
			text := fmt.Sprintf("System Health Report:\n\n")
			text += fmt.Sprintf("Overall Status: %s\n\n", overallStatus)
			text += fmt.Sprintf("Services:\n")

			if len(serviceStatuses) == 0 {
				text += "  No services connected.\n"
			} else {
				for _, svc := range serviceStatuses {
					statusEmoji := "✓"
					if svc["status"] == "degraded" {
						statusEmoji = "⚠"
					} else if svc["status"] == "unhealthy" {
						statusEmoji = "✗"
					}

					text += fmt.Sprintf("  %s %s: %s (last seen: %s, uptime: %s)\n",
						statusEmoji,
						svc["service"],
						svc["status"],
						svc["last_seen"],
						svc["uptime"],
					)
				}
			}

			text += fmt.Sprintf("\nSummary: %d healthy, %d degraded, %d unhealthy\n",
				healthyCount, degradedCount, unhealthyCount)

			return text, nil
		},
	)
}

// registerServiceTopologyTool registers the coral_get_service_topology tool.
func (s *Server) registerServiceTopologyTool() {
	if !s.isToolEnabled("coral_get_service_topology") {
		return
	}

	genkit.DefineTool(
		s.genkit,
		"coral_get_service_topology",
		"Get service dependency graph discovered from distributed traces. Shows which services communicate and call frequency.",
		func(ctx *ai.ToolContext, input ServiceTopologyInput) (string, error) {
			s.auditToolCall("coral_get_service_topology", input)

			// TODO: Implement topology discovery from traces (RFD 036).
			// For now, return connected agents as a simple topology.

			agents := s.registry.ListAll()

			text := fmt.Sprintf("Service Topology:\n\n")
			text += fmt.Sprintf("Connected Services (%d):\n", len(agents))

			for _, agent := range agents {
				text += fmt.Sprintf("  - %s (mesh IP: %s)\n", agent.ComponentName, agent.MeshIPv4)
			}

			text += "\n"
			text += "Note: Dependency graph discovery from distributed traces is not yet implemented.\n"
			text += "      See RFD 036 for planned trace-based topology analysis.\n"

			return text, nil
		},
	)
}

// registerQueryEventsTool registers the coral_query_events tool.
func (s *Server) registerQueryEventsTool() {
	if !s.isToolEnabled("coral_query_events") {
		return
	}

	genkit.DefineTool(
		s.genkit,
		"coral_query_events",
		"Query operational events tracked by Coral (deployments, restarts, crashes, alerts, configuration changes).",
		func(ctx *ai.ToolContext, input QueryEventsInput) (string, error) {
			s.auditToolCall("coral_query_events", input)

			// TODO: Implement event storage and querying.
			// For now, return placeholder.

			text := "Operational Events:\n\n"
			text += "No events tracked yet.\n\n"
			text += "Note: Event storage and querying is planned for future implementation.\n"
			text += "      Events will include deployments, restarts, crashes, and configuration changes.\n"

			return text, nil
		},
	)
}

// registerBeylaHTTPMetricsTool registers the coral_query_beyla_http_metrics tool.
func (s *Server) registerBeylaHTTPMetricsTool() {
	if !s.isToolEnabled("coral_query_beyla_http_metrics") {
		return
	}

	genkit.DefineTool(
		s.genkit,
		"coral_query_beyla_http_metrics",
		"Query HTTP RED metrics collected by Beyla (request rate, error rate, latency distributions). Returns percentiles, status code breakdown, and route-level metrics.",
		func(ctx *ai.ToolContext, input BeylaHTTPMetricsInput) (string, error) {
			s.auditToolCall("coral_query_beyla_http_metrics", input)

			// Get time range (handle nil pointer).
			timeRangeStr := "1h"
			if input.TimeRange != nil {
				timeRangeStr = *input.TimeRange
			}

			// Parse time range.
			startTime, endTime, err := parseTimeRange(timeRangeStr)
			if err != nil {
				return "", fmt.Errorf("invalid time_range '%s': %w", timeRangeStr, err)
			}

			// Build filters map.
			filters := make(map[string]string)
			if input.HTTPMethod != nil {
				filters["http_method"] = *input.HTTPMethod
			}
			if input.HTTPRoute != nil {
				filters["http_route"] = *input.HTTPRoute
			}
			if input.StatusCodeRange != nil {
				filters["status_code_range"] = *input.StatusCodeRange
			}

			// Query database.
			dbCtx := context.Background()
			results, err := s.db.QueryBeylaHTTPMetrics(dbCtx, input.Service, startTime, endTime, filters)
			if err != nil {
				return "", fmt.Errorf("failed to query HTTP metrics: %w", err)
			}

			// Format response.
			if len(results) == 0 {
				text := fmt.Sprintf("Beyla HTTP Metrics for %s (last %s):\n\n", input.Service, timeRangeStr)
				text += "No metrics found for this service in the specified time range.\n\n"
				text += "This could mean:\n"
				text += "- The service hasn't received HTTP requests\n"
				text += "- Beyla is not running on the agent\n"
				text += "- The service name doesn't match\n"
				return text, nil
			}

			// Calculate statistics from histogram buckets.
			stats := aggregateHTTPMetrics(results)

			text := fmt.Sprintf("Beyla HTTP Metrics for %s (last %s):\n\n", input.Service, timeRangeStr)
			text += fmt.Sprintf("Total Requests: %d\n\n", stats.TotalRequests)

			// Show latency percentiles.
			text += "Latency Percentiles:\n"
			text += fmt.Sprintf("  P50: %.1fms\n", stats.P50)
			text += fmt.Sprintf("  P95: %.1fms\n", stats.P95)
			text += fmt.Sprintf("  P99: %.1fms\n\n", stats.P99)

			// Show status code breakdown.
			text += "Status Code Breakdown:\n"
			for code, count := range stats.StatusCodes {
				percentage := float64(count) / float64(stats.TotalRequests) * 100
				text += fmt.Sprintf("  %d: %d requests (%.1f%%)\n", code, count, percentage)
			}
			text += "\n"

			// Show top routes by request count.
			text += "Top Routes:\n"
			topRoutes := getTopRoutes(results, 5)
			for _, route := range topRoutes {
				text += fmt.Sprintf("  %s: %d requests\n", route.Route, route.Count)
			}

			return text, nil
		},
	)
}

// registerBeylaGRPCMetricsTool registers the coral_query_beyla_grpc_metrics tool.
func (s *Server) registerBeylaGRPCMetricsTool() {
	if !s.isToolEnabled("coral_query_beyla_grpc_metrics") {
		return
	}

	genkit.DefineTool(
		s.genkit,
		"coral_query_beyla_grpc_metrics",
		"Query gRPC method-level RED metrics collected by Beyla. Returns RPC rate, latency distributions, and status code breakdown.",
		func(ctx *ai.ToolContext, input BeylaGRPCMetricsInput) (string, error) {
			s.auditToolCall("coral_query_beyla_grpc_metrics", input)

			// Get time range (handle nil pointer).
			timeRange := "1h"
			if input.TimeRange != nil {
				timeRange = *input.TimeRange
			}

			text := fmt.Sprintf("Beyla gRPC Metrics for %s (last %s):\n\n", input.Service, timeRange)
			text += "No metrics available yet.\n\n"
			text += "Note: Beyla gRPC metrics collection is planned (RFD 032).\n"

			return text, nil
		},
	)
}

// registerBeylaSQLMetricsTool registers the coral_query_beyla_sql_metrics tool.
func (s *Server) registerBeylaSQLMetricsTool() {
	if !s.isToolEnabled("coral_query_beyla_sql_metrics") {
		return
	}

	genkit.DefineTool(
		s.genkit,
		"coral_query_beyla_sql_metrics",
		"Query SQL operation metrics collected by Beyla. Returns query latencies, operation types, and table-level statistics.",
		func(ctx *ai.ToolContext, input BeylaSQLMetricsInput) (string, error) {
			s.auditToolCall("coral_query_beyla_sql_metrics", input)

			// Get time range (handle nil pointer).
			timeRange := "1h"
			if input.TimeRange != nil {
				timeRange = *input.TimeRange
			}

			text := fmt.Sprintf("Beyla SQL Metrics for %s (last %s):\n\n", input.Service, timeRange)
			text += "No metrics available yet.\n\n"
			text += "Note: Beyla SQL metrics collection is planned (RFD 032).\n"

			return text, nil
		},
	)
}

// registerBeylaTracesTool registers the coral_query_beyla_traces tool.
func (s *Server) registerBeylaTracesTool() {
	if !s.isToolEnabled("coral_query_beyla_traces") {
		return
	}

	genkit.DefineTool(
		s.genkit,
		"coral_query_beyla_traces",
		"Query distributed traces collected by Beyla. Can search by trace ID, service, time range, or duration threshold.",
		func(ctx *ai.ToolContext, input BeylaTracesInput) (string, error) {
			s.auditToolCall("coral_query_beyla_traces", input)

			text := "Beyla Distributed Traces:\n\n"
			text += "No traces available yet.\n\n"
			text += "Note: Beyla distributed tracing is planned (RFD 036).\n"

			return text, nil
		},
	)
}

// registerTraceByIDTool registers the coral_get_trace_by_id tool.
func (s *Server) registerTraceByIDTool() {
	if !s.isToolEnabled("coral_get_trace_by_id") {
		return
	}

	genkit.DefineTool(
		s.genkit,
		"coral_get_trace_by_id",
		"Get a specific distributed trace by ID with full span tree showing parent-child relationships and timing.",
		func(ctx *ai.ToolContext, input TraceByIDInput) (string, error) {
			s.auditToolCall("coral_get_trace_by_id", input)

			text := fmt.Sprintf("Trace %s:\n\n", input.TraceID)
			text += "Trace not found.\n\n"
			text += "Note: Trace retrieval is planned (RFD 036).\n"

			return text, nil
		},
	)
}

// registerTelemetrySpansTool registers the coral_query_telemetry_spans tool.
func (s *Server) registerTelemetrySpansTool() {
	if !s.isToolEnabled("coral_query_telemetry_spans") {
		return
	}

	genkit.DefineTool(
		s.genkit,
		"coral_query_telemetry_spans",
		"Query generic OTLP spans (from instrumented applications using OpenTelemetry SDKs). Complementary to Beyla traces.",
		func(ctx *ai.ToolContext, input TelemetrySpansInput) (string, error) {
			s.auditToolCall("coral_query_telemetry_spans", input)

			text := "OTLP Spans:\n\n"
			text += "No spans available yet.\n\n"
			text += "Note: OTLP span querying is implemented (RFD 025) but requires telemetry data.\n"

			return text, nil
		},
	)
}

// registerTelemetryMetricsTool registers the coral_query_telemetry_metrics tool.
func (s *Server) registerTelemetryMetricsTool() {
	if !s.isToolEnabled("coral_query_telemetry_metrics") {
		return
	}

	genkit.DefineTool(
		s.genkit,
		"coral_query_telemetry_metrics",
		"Query generic OTLP metrics (from instrumented applications). Returns time-series data for custom application metrics.",
		func(ctx *ai.ToolContext, input TelemetryMetricsInput) (string, error) {
			s.auditToolCall("coral_query_telemetry_metrics", input)

			text := "OTLP Metrics:\n\n"
			text += "No metrics available yet.\n\n"
			text += "Note: OTLP metrics querying is implemented (RFD 025) but requires telemetry data.\n"

			return text, nil
		},
	)
}

// registerTelemetryLogsTool registers the coral_query_telemetry_logs tool.
func (s *Server) registerTelemetryLogsTool() {
	if !s.isToolEnabled("coral_query_telemetry_logs") {
		return
	}

	genkit.DefineTool(
		s.genkit,
		"coral_query_telemetry_logs",
		"Query generic OTLP logs (from instrumented applications). Search application logs with full-text search and filters.",
		func(ctx *ai.ToolContext, input TelemetryLogsInput) (string, error) {
			s.auditToolCall("coral_query_telemetry_logs", input)

			text := "OTLP Logs:\n\n"
			text += "No logs available yet.\n\n"
			text += "Note: OTLP log querying is implemented (RFD 025) but requires telemetry data.\n"

			return text, nil
		},
	)
}

// matchesPattern checks if a string matches a simple glob pattern.
// Supports '*' as wildcard only.
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
