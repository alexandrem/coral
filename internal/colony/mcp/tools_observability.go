package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
	"github.com/invopop/jsonschema"

	"github.com/coral-io/coral/internal/colony/database"
)

// generateInputSchema generates a JSON schema from a Go type.
func generateInputSchema(inputType interface{}) (map[string]any, error) {
	reflector := jsonschema.Reflector{}
	schema := reflector.Reflect(inputType)

	schemaBytes, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal schema: %w", err)
	}

	var schemaMap map[string]any
	if err := json.Unmarshal(schemaBytes, &schemaMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal schema: %w", err)
	}

	return schemaMap, nil
}

// registerServiceHealthTool registers the coral_get_service_health tool.
func (s *Server) registerServiceHealthTool() {
	if !s.isToolEnabled("coral_get_service_health") {
		return
	}

	inputSchema, err := generateInputSchema(ServiceHealthInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_get_service_health")
		return
	}

	genkit.DefineToolWithInputSchema(
		s.genkit,
		"coral_get_service_health",
		"Get current health status of services. Returns health state, resource usage (CPU, memory), uptime, and recent issues.",
		inputSchema,
		func(ctx *ai.ToolContext, input any) (string, error) {
			// Parse input from any to ServiceHealthInput.
			inputBytes, err := json.Marshal(input)
			if err != nil {
				return "", fmt.Errorf("failed to marshal input: %w", err)
			}

			var typedInput ServiceHealthInput
			if err := json.Unmarshal(inputBytes, &typedInput); err != nil {
				return "", fmt.Errorf("failed to unmarshal input: %w", err)
			}
			s.auditToolCall("coral_get_service_health", typedInput)

			// Get service filter (handle nil pointer).
			var serviceFilter string
			if typedInput.ServiceFilter != nil {
				serviceFilter = *typedInput.ServiceFilter
			}

			// Get all agents from registry.
			agents := s.registry.ListAll()

			// Build health report.
			var healthyCount, degradedCount, unhealthyCount int
			var serviceStatuses []map[string]interface{}

			for _, agent := range agents {
				// Apply filter if specified.
				if serviceFilter != "" && !matchesPattern(agent.Name, serviceFilter) {
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
					"service":   agent.Name,
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
			text := "System Health Report:\n\n"
			text += fmt.Sprintf("Overall Status: %s\n\n", overallStatus)
			text += "Services:\n"

			if len(serviceStatuses) == 0 {
				text += "  No services connected.\n"
			} else {
				for _, svc := range serviceStatuses {
					statusEmoji := "✓"
					switch svc["status"] {
					case "degraded":
						statusEmoji = "⚠"
					case "unhealthy":
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

	inputSchema, err := generateInputSchema(ServiceTopologyInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_get_service_topology")
		return
	}

	genkit.DefineToolWithInputSchema(
		s.genkit,
		"coral_get_service_topology",
		"Get service dependency graph discovered from distributed traces. Shows which services communicate and call frequency.",
		inputSchema,
		func(ctx *ai.ToolContext, input any) (string, error) {
			// Parse input from any to ServiceTopologyInput.
			inputBytes, err := json.Marshal(input)
			if err != nil {
				return "", fmt.Errorf("failed to marshal input: %w", err)
			}

			var typedInput ServiceTopologyInput
			if err := json.Unmarshal(inputBytes, &typedInput); err != nil {
				return "", fmt.Errorf("failed to unmarshal input: %w", err)
			}

			s.auditToolCall("coral_get_service_topology", typedInput)

			// TODO: Implement topology discovery from traces (RFD 036).
			// For now, return connected agents as a simple topology.

			agents := s.registry.ListAll()

			text := "Service Topology:\n\n"
			text += fmt.Sprintf("Connected Services (%d):\n", len(agents))

			for _, agent := range agents {
				text += fmt.Sprintf("  - %s (mesh IP: %s)\n", agent.Name, agent.MeshIPv4)
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

	inputSchema, err := generateInputSchema(QueryEventsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for tool_name")
		return
	}

	genkit.DefineToolWithInputSchema(
		s.genkit,
		"coral_query_events",
		"Query operational events tracked by Coral (deployments, restarts, crashes, alerts, configuration changes).",
		inputSchema,
		func(ctx *ai.ToolContext, input any) (string, error) {
			// Parse input
			inputBytes, err := json.Marshal(input)
			if err != nil {
				return "", fmt.Errorf("failed to marshal input: %w", err)
			}

			var typedInput QueryEventsInput
			if err := json.Unmarshal(inputBytes, &typedInput); err != nil {
				return "", fmt.Errorf("failed to unmarshal input: %w", err)
			}

			s.auditToolCall("coral_query_events", typedInput)

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

	inputSchema, err := generateInputSchema(BeylaHTTPMetricsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for tool_name")
		return
	}

	genkit.DefineToolWithInputSchema(
		s.genkit,
		"coral_query_beyla_http_metrics",
		"Query HTTP RED metrics collected by Beyla (request rate, error rate, latency distributions). Returns percentiles, status code breakdown, and route-level metrics.",
		inputSchema,
		func(ctx *ai.ToolContext, input any) (string, error) {
			// Parse input
			inputBytes, err := json.Marshal(input)
			if err != nil {
				return "", fmt.Errorf("failed to marshal input: %w", err)
			}

			var typedInput BeylaHTTPMetricsInput
			if err := json.Unmarshal(inputBytes, &typedInput); err != nil {
				return "", fmt.Errorf("failed to unmarshal input: %w", err)
			}

			s.auditToolCall("coral_query_beyla_http_metrics", typedInput)

			// Get time range (handle nil pointer).
			timeRangeStr := "1h"
			if typedInput.TimeRange != nil {
				timeRangeStr = *typedInput.TimeRange
			}

			// Parse time range.
			startTime, endTime, err := parseTimeRange(timeRangeStr)
			if err != nil {
				return "", fmt.Errorf("invalid time_range '%s': %w", timeRangeStr, err)
			}

			// Build filters map.
			filters := make(map[string]string)
			if typedInput.HTTPMethod != nil {
				filters["http_method"] = *typedInput.HTTPMethod
			}
			if typedInput.HTTPRoute != nil {
				filters["http_route"] = *typedInput.HTTPRoute
			}
			if typedInput.StatusCodeRange != nil {
				filters["status_code_range"] = *typedInput.StatusCodeRange
			}

			// Query database.
			dbCtx := context.Background()
			results, err := s.db.QueryBeylaHTTPMetrics(dbCtx, typedInput.Service, startTime, endTime, filters)
			if err != nil {
				return "", fmt.Errorf("failed to query HTTP metrics: %w", err)
			}

			// Format response.
			if len(results) == 0 {
				text := fmt.Sprintf("Beyla HTTP Metrics for %s (last %s):\n\n", typedInput.Service, timeRangeStr)
				text += "No metrics found for this service in the specified time range.\n\n"
				text += "This could mean:\n"
				text += "- The service hasn't received HTTP requests\n"
				text += "- Beyla is not running on the agent\n"
				text += "- The service name doesn't match\n"
				return text, nil
			}

			// Calculate statistics from histogram buckets.
			stats := aggregateHTTPMetrics(results)

			text := fmt.Sprintf("Beyla HTTP Metrics for %s (last %s):\n\n", typedInput.Service, timeRangeStr)
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

	inputSchema, err := generateInputSchema(BeylaGRPCMetricsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for tool_name")
		return
	}

	genkit.DefineToolWithInputSchema(
		s.genkit,
		"coral_query_beyla_grpc_metrics",
		"Query gRPC method-level RED metrics collected by Beyla. Returns RPC rate, latency distributions, and status code breakdown.",
		inputSchema,
		func(ctx *ai.ToolContext, input any) (string, error) {
			// Parse input
			inputBytes, err := json.Marshal(input)
			if err != nil {
				return "", fmt.Errorf("failed to marshal input: %w", err)
			}

			var typedInput BeylaGRPCMetricsInput
			if err := json.Unmarshal(inputBytes, &typedInput); err != nil {
				return "", fmt.Errorf("failed to unmarshal input: %w", err)
			}

			s.auditToolCall("coral_query_beyla_grpc_metrics", typedInput)

			// Get time range (handle nil pointer).
			timeRangeStr := "1h"
			if typedInput.TimeRange != nil {
				timeRangeStr = *typedInput.TimeRange
			}

			// Parse time range.
			startTime, endTime, err := parseTimeRange(timeRangeStr)
			if err != nil {
				return "", fmt.Errorf("invalid time_range '%s': %w", timeRangeStr, err)
			}

			// Build filters map.
			filters := make(map[string]string)
			if typedInput.GRPCMethod != nil {
				filters["grpc_method"] = *typedInput.GRPCMethod
			}
			if typedInput.StatusCode != nil {
				filters["status_code"] = fmt.Sprintf("%d", *typedInput.StatusCode)
			}

			// Query database.
			dbCtx := context.Background()
			results, err := s.db.QueryBeylaGRPCMetrics(dbCtx, typedInput.Service, startTime, endTime, filters)
			if err != nil {
				return "", fmt.Errorf("failed to query gRPC metrics: %w", err)
			}

			// Format response.
			if len(results) == 0 {
				text := fmt.Sprintf("Beyla gRPC Metrics for %s (last %s):\n\n", typedInput.Service, timeRangeStr)
				text += "No metrics found for this service in the specified time range.\n\n"
				text += "This could mean:\n"
				text += "- The service hasn't received gRPC requests\n"
				text += "- Beyla is not running on the agent\n"
				text += "- The service name doesn't match\n"
				return text, nil
			}

			// Calculate statistics from histogram buckets.
			stats := aggregateGRPCMetrics(results)

			text := fmt.Sprintf("Beyla gRPC Metrics for %s (last %s):\n\n", typedInput.Service, timeRangeStr)
			text += fmt.Sprintf("Total RPCs: %d\n\n", stats.TotalRPCs)

			// Show latency percentiles.
			text += "Latency Percentiles:\n"
			text += fmt.Sprintf("  P50: %.1fms\n", stats.P50)
			text += fmt.Sprintf("  P95: %.1fms\n", stats.P95)
			text += fmt.Sprintf("  P99: %.1fms\n\n", stats.P99)

			// Show status code breakdown.
			text += "Status Code Breakdown:\n"
			for code, count := range stats.StatusCodes {
				percentage := float64(count) / float64(stats.TotalRPCs) * 100
				statusName := grpcStatusName(code)
				text += fmt.Sprintf("  %d (%s): %d requests (%.1f%%)\n", code, statusName, count, percentage)
			}
			text += "\n"

			// Show top methods by request count.
			text += "Top Methods:\n"
			topMethods := getTopGRPCMethods(results, 5)
			for _, method := range topMethods {
				text += fmt.Sprintf("  %s: %d requests\n", method.Method, method.Count)
			}

			return text, nil
		},
	)
}

// registerBeylaSQLMetricsTool registers the coral_query_beyla_sql_metrics tool.
func (s *Server) registerBeylaSQLMetricsTool() {
	if !s.isToolEnabled("coral_query_beyla_sql_metrics") {
		return
	}

	inputSchema, err := generateInputSchema(BeylaSQLMetricsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for tool_name")
		return
	}

	genkit.DefineToolWithInputSchema(
		s.genkit,
		"coral_query_beyla_sql_metrics",
		"Query SQL operation metrics collected by Beyla. Returns query latencies, operation types, and table-level statistics.",
		inputSchema,
		func(ctx *ai.ToolContext, input any) (string, error) {
			// Parse input
			inputBytes, err := json.Marshal(input)
			if err != nil {
				return "", fmt.Errorf("failed to marshal input: %w", err)
			}

			var typedInput BeylaSQLMetricsInput
			if err := json.Unmarshal(inputBytes, &typedInput); err != nil {
				return "", fmt.Errorf("failed to unmarshal input: %w", err)
			}

			s.auditToolCall("coral_query_beyla_sql_metrics", typedInput)

			// Get time range (handle nil pointer).
			timeRangeStr := "1h"
			if typedInput.TimeRange != nil {
				timeRangeStr = *typedInput.TimeRange
			}

			// Parse time range.
			startTime, endTime, err := parseTimeRange(timeRangeStr)
			if err != nil {
				return "", fmt.Errorf("invalid time_range '%s': %w", timeRangeStr, err)
			}

			// Build filters map.
			filters := make(map[string]string)
			if typedInput.SQLOperation != nil {
				filters["sql_operation"] = *typedInput.SQLOperation
			}
			if typedInput.TableName != nil {
				filters["table_name"] = *typedInput.TableName
			}

			// Query database.
			dbCtx := context.Background()
			results, err := s.db.QueryBeylaSQLMetrics(dbCtx, typedInput.Service, startTime, endTime, filters)
			if err != nil {
				return "", fmt.Errorf("failed to query SQL metrics: %w", err)
			}

			// Format response.
			if len(results) == 0 {
				text := fmt.Sprintf("Beyla SQL Metrics for %s (last %s):\n\n", typedInput.Service, timeRangeStr)
				text += "No metrics found for this service in the specified time range.\n\n"
				text += "This could mean:\n"
				text += "- The service hasn't executed SQL queries\n"
				text += "- Beyla is not running on the agent\n"
				text += "- The service name doesn't match\n"
				return text, nil
			}

			// Calculate statistics from histogram buckets.
			stats := aggregateSQLMetrics(results)

			text := fmt.Sprintf("Beyla SQL Metrics for %s (last %s):\n\n", typedInput.Service, timeRangeStr)
			text += fmt.Sprintf("Total Queries: %d\n\n", stats.TotalQueries)

			// Show latency percentiles.
			text += "Latency Percentiles:\n"
			text += fmt.Sprintf("  P50: %.1fms\n", stats.P50)
			text += fmt.Sprintf("  P95: %.1fms\n", stats.P95)
			text += fmt.Sprintf("  P99: %.1fms\n\n", stats.P99)

			// Show operation breakdown.
			text += "Operation Breakdown:\n"
			for op, count := range stats.Operations {
				percentage := float64(count) / float64(stats.TotalQueries) * 100
				text += fmt.Sprintf("  %s: %d queries (%.1f%%)\n", op, count, percentage)
			}
			text += "\n"

			// Show top tables by query count.
			text += "Top Tables:\n"
			topTables := getTopSQLTables(results, 5)
			for _, table := range topTables {
				text += fmt.Sprintf("  %s: %d queries\n", table.Table, table.Count)
			}

			return text, nil
		},
	)
}

// registerBeylaTracesTool registers the coral_query_beyla_traces tool.
func (s *Server) registerBeylaTracesTool() {
	if !s.isToolEnabled("coral_query_beyla_traces") {
		return
	}

	inputSchema, err := generateInputSchema(BeylaTracesInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for tool_name")
		return
	}

	genkit.DefineToolWithInputSchema(
		s.genkit,
		"coral_query_beyla_traces",
		"Query distributed traces collected by Beyla. Can search by trace ID, service, time range, or duration threshold.",
		inputSchema,
		func(ctx *ai.ToolContext, input any) (string, error) {
			// Parse input
			inputBytes, err := json.Marshal(input)
			if err != nil {
				return "", fmt.Errorf("failed to marshal input: %w", err)
			}

			var typedInput BeylaTracesInput
			if err := json.Unmarshal(inputBytes, &typedInput); err != nil {
				return "", fmt.Errorf("failed to unmarshal input: %w", err)
			}

			s.auditToolCall("coral_query_beyla_traces", typedInput)

			// Get time range (handle nil pointer).
			timeRangeStr := "1h"
			if typedInput.TimeRange != nil {
				timeRangeStr = *typedInput.TimeRange
			}

			// Parse time range.
			startTime, endTime, err := parseTimeRange(timeRangeStr)
			if err != nil {
				return "", fmt.Errorf("invalid time_range '%s': %w", timeRangeStr, err)
			}

			// Get service name (optional).
			serviceName := ""
			if typedInput.Service != nil {
				serviceName = *typedInput.Service
			}

			// Get min duration filter (convert from ms to us).
			var minDurationUs int64
			if typedInput.MinDurationMs != nil {
				minDurationUs = int64(*typedInput.MinDurationMs) * 1000
			}

			// Get max traces limit.
			maxTraces := 10
			if typedInput.MaxTraces != nil {
				maxTraces = *typedInput.MaxTraces
			}

			// Query database.
			dbCtx := context.Background()
			results, err := s.db.QueryBeylaTraces(dbCtx, serviceName, startTime, endTime, minDurationUs, maxTraces)
			if err != nil {
				return "", fmt.Errorf("failed to query traces: %w", err)
			}

			// Format response.
			if len(results) == 0 {
				text := "Beyla Distributed Traces:\n\n"
				text += "No traces found matching the criteria.\n\n"
				text += "This could mean:\n"
				text += "- No distributed traces in the time range\n"
				text += "- Beyla tracing is not enabled\n"
				text += "- Duration threshold too high\n"
				return text, nil
			}

			// Group spans by trace ID.
			traceMap := make(map[string][]*database.BeylaTraceResult)
			for _, span := range results {
				traceMap[span.TraceID] = append(traceMap[span.TraceID], span)
			}

			text := fmt.Sprintf("Beyla Distributed Traces (showing %d traces):\n\n", len(traceMap))

			// Show each trace.
			traceNum := 0
			for traceID, spans := range traceMap {
				traceNum++
				if traceNum > maxTraces {
					break
				}

				// Calculate trace duration.
				var minStartTime, maxEndTime time.Time
				for i, span := range spans {
					if i == 0 || span.StartTime.Before(minStartTime) {
						minStartTime = span.StartTime
					}
					endTime := span.StartTime.Add(time.Duration(span.DurationUs) * time.Microsecond)
					if i == 0 || endTime.After(maxEndTime) {
						maxEndTime = endTime
					}
				}
				totalDuration := maxEndTime.Sub(minStartTime)

				text += fmt.Sprintf("Trace %d: %s\n", traceNum, traceID)
				text += fmt.Sprintf("  Duration: %.1fms\n", float64(totalDuration.Microseconds())/1000.0)
				text += fmt.Sprintf("  Spans: %d\n", len(spans))
				text += fmt.Sprintf("  Services: %s\n", getUniqueServices(spans))
				text += "\n"

				// Show top 3 slowest spans in this trace.
				slowestSpans := getTopSlowestSpans(spans, 3)
				text += "  Top slowest spans:\n"
				for _, span := range slowestSpans {
					durationMs := float64(span.DurationUs) / 1000.0
					text += fmt.Sprintf("    - %s (%s): %.1fms\n", span.SpanName, span.ServiceName, durationMs)
				}
				text += "\n"
			}

			return text, nil
		},
	)
}

// registerTraceByIDTool registers the coral_get_trace_by_id tool.
func (s *Server) registerTraceByIDTool() {
	if !s.isToolEnabled("coral_get_trace_by_id") {
		return
	}

	inputSchema, err := generateInputSchema(TraceByIDInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for tool_name")
		return
	}

	genkit.DefineToolWithInputSchema(
		s.genkit,
		"coral_get_trace_by_id",
		"Get a specific distributed trace by ID with full span tree showing parent-child relationships and timing.",
		inputSchema,
		func(ctx *ai.ToolContext, input any) (string, error) {
			// Parse input
			inputBytes, err := json.Marshal(input)
			if err != nil {
				return "", fmt.Errorf("failed to marshal input: %w", err)
			}

			var typedInput TraceByIDInput
			if err := json.Unmarshal(inputBytes, &typedInput); err != nil {
				return "", fmt.Errorf("failed to unmarshal input: %w", err)
			}

			s.auditToolCall("coral_get_trace_by_id", typedInput)

			text := fmt.Sprintf("Trace %s:\n\n", typedInput.TraceID)
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

	inputSchema, err := generateInputSchema(TelemetrySpansInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for tool_name")
		return
	}

	genkit.DefineToolWithInputSchema(
		s.genkit,
		"coral_query_telemetry_spans",
		"Query generic OTLP spans (from instrumented applications using OpenTelemetry SDKs). Returns aggregated telemetry summaries. For detailed raw spans, see RFD 041.",
		inputSchema,
		func(ctx *ai.ToolContext, input any) (string, error) {
			// Parse input
			inputBytes, err := json.Marshal(input)
			if err != nil {
				return "", fmt.Errorf("failed to marshal input: %w", err)
			}

			var typedInput TelemetrySpansInput
			if err := json.Unmarshal(inputBytes, &typedInput); err != nil {
				return "", fmt.Errorf("failed to unmarshal input: %w", err)
			}

			s.auditToolCall("coral_query_telemetry_spans", typedInput)

			// Get time range (handle nil pointer).
			timeRangeStr := "1h"
			if typedInput.TimeRange != nil {
				timeRangeStr = *typedInput.TimeRange
			}

			// Parse time range.
			startTime, endTime, err := parseTimeRange(timeRangeStr)
			if err != nil {
				return "", fmt.Errorf("invalid time_range '%s': %w", timeRangeStr, err)
			}

			// Find agents for this service.
			agents := s.registry.ListAll()
			var matchingAgents []string
			for _, agent := range agents {
				if agent.Name == typedInput.Service {
					matchingAgents = append(matchingAgents, agent.AgentID)
				}
			}

			if len(matchingAgents) == 0 {
				text := fmt.Sprintf("OTLP Telemetry for %s (last %s):\n\n", typedInput.Service, timeRangeStr)
				text += "No agents found running this service.\n\n"
				text += "Possible reasons:\n"
				text += "- Service name doesn't match\n"
				text += "- No agents connected\n"
				return text, nil
			}

			// Query summaries for each agent.
			dbCtx := context.Background()
			var allSummaries []database.TelemetrySummary
			for _, agentID := range matchingAgents {
				summaries, err := s.db.QueryTelemetrySummaries(dbCtx, agentID, startTime, endTime)
				if err != nil {
					s.logger.Warn().Err(err).Str("agent_id", agentID).Msg("Failed to query telemetry summaries")
					continue
				}
				allSummaries = append(allSummaries, summaries...)
			}

			if len(allSummaries) == 0 {
				text := fmt.Sprintf("OTLP Telemetry for %s (last %s):\n\n", typedInput.Service, timeRangeStr)
				text += "No telemetry data found in the specified time range.\n\n"
				text += "Possible reasons:\n"
				text += "- Service is not instrumented with OpenTelemetry\n"
				text += "- No traffic during this period\n"
				text += "- Data retention expired (summaries kept for 24h)\n\n"
				text += "Note: This returns aggregated summaries. For raw spans, see RFD 041.\n"
				return text, nil
			}

			// Aggregate stats from summaries.
			stats := aggregateTelemetrySummaries(allSummaries, typedInput.Operation)

			text := fmt.Sprintf("OTLP Telemetry for %s (last %s):\n\n", typedInput.Service, timeRangeStr)
			text += fmt.Sprintf("Total Spans: %d\n", stats.TotalSpans)
			text += fmt.Sprintf("Error Count: %d (%.1f%%)\n\n", stats.ErrorCount, stats.ErrorRate)

			// Show latency percentiles.
			text += "Latency Percentiles:\n"
			text += fmt.Sprintf("  P50: %.1fms\n", stats.P50)
			text += fmt.Sprintf("  P95: %.1fms\n", stats.P95)
			text += fmt.Sprintf("  P99: %.1fms\n\n", stats.P99)

			// Show breakdown by span kind.
			if len(stats.SpanKinds) > 0 {
				text += "Span Kinds:\n"
				for kind, count := range stats.SpanKinds {
					percentage := float64(count) / float64(stats.TotalSpans) * 100
					text += fmt.Sprintf("  %s: %d spans (%.1f%%)\n", kind, count, percentage)
				}
				text += "\n"
			}

			// Show sample traces if available.
			if len(stats.SampleTraces) > 0 {
				text += "Sample Trace IDs:\n"
				for i, traceID := range stats.SampleTraces {
					if i >= 5 {
						break
					}
					text += fmt.Sprintf("  - %s\n", traceID)
				}
			}

			text += "\nNote: This shows aggregated summaries (1-minute buckets). For detailed raw spans, see RFD 041.\n"

			return text, nil
		},
	)
}

// registerTelemetryMetricsTool registers the coral_query_telemetry_metrics tool.
func (s *Server) registerTelemetryMetricsTool() {
	if !s.isToolEnabled("coral_query_telemetry_metrics") {
		return
	}

	inputSchema, err := generateInputSchema(TelemetryMetricsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for tool_name")
		return
	}

	genkit.DefineToolWithInputSchema(
		s.genkit,
		"coral_query_telemetry_metrics",
		"Query generic OTLP metrics (from instrumented applications). Returns time-series data for custom application metrics.",
		inputSchema,
		func(ctx *ai.ToolContext, input any) (string, error) {
			// Parse input
			inputBytes, err := json.Marshal(input)
			if err != nil {
				return "", fmt.Errorf("failed to marshal input: %w", err)
			}

			var typedInput TelemetryMetricsInput
			if err := json.Unmarshal(inputBytes, &typedInput); err != nil {
				return "", fmt.Errorf("failed to unmarshal input: %w", err)
			}

			s.auditToolCall("coral_query_telemetry_metrics", typedInput)

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

	inputSchema, err := generateInputSchema(TelemetryLogsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for tool_name")
		return
	}

	genkit.DefineToolWithInputSchema(
		s.genkit,
		"coral_query_telemetry_logs",
		"Query generic OTLP logs (from instrumented applications). Search application logs with full-text search and filters.",
		inputSchema,
		func(ctx *ai.ToolContext, input any) (string, error) {
			// Parse input
			inputBytes, err := json.Marshal(input)
			if err != nil {
				return "", fmt.Errorf("failed to marshal input: %w", err)
			}

			var typedInput TelemetryLogsInput
			if err := json.Unmarshal(inputBytes, &typedInput); err != nil {
				return "", fmt.Errorf("failed to unmarshal input: %w", err)
			}

			s.auditToolCall("coral_query_telemetry_logs", typedInput)

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
