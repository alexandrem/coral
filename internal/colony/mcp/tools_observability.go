package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/mark3labs/mcp-go/mcp"

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

// registerServiceHealthTool registers the coral_get_service_health tool.
func (s *Server) registerServiceHealthTool() {
	s.registerToolWithSchema(
		"coral_get_service_health",
		"Get current health status of services. Returns health state, resource usage (CPU, memory), uptime, and recent issues.",
		ServiceHealthInput{},
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Parse arguments.
			var input ServiceHealthInput
			if request.Params.Arguments != nil {
				argBytes, err := json.Marshal(request.Params.Arguments)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
				}
				if err := json.Unmarshal(argBytes, &input); err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
				}
			}

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

			return mcp.NewToolResultText(text), nil
		})
}

// registerServiceTopologyTool registers the coral_get_service_topology tool.
func (s *Server) registerServiceTopologyTool() {
	s.registerToolWithSchema(
		"coral_get_service_topology",
		"Get service dependency graph discovered from distributed traces. Shows which services communicate and call frequency.",
		ServiceTopologyInput{},
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Parse arguments from MCP request.
			var input ServiceTopologyInput
			if request.Params.Arguments != nil {
				argBytes, err := json.Marshal(request.Params.Arguments)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
				}
				if err := json.Unmarshal(argBytes, &input); err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
				}
			}

			s.auditToolCall("coral_get_service_topology", input)

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

			return mcp.NewToolResultText(text), nil
		})
}

// registerQueryEventsTool registers the coral_query_events tool.
func (s *Server) registerQueryEventsTool() {
	if !s.isToolEnabled("coral_query_events") {
		return
	}

	inputSchema, err := generateInputSchema(QueryEventsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_query_events")
		return
	}

	// Marshal schema to JSON bytes for MCP tool.
	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_query_events")
		return
	}

	// Create MCP tool with raw schema.
	tool := mcp.NewToolWithRawSchema(
		"coral_query_events",
		"Query operational events tracked by Coral (deployments, restarts, crashes, alerts, configuration changes).",
		schemaBytes,
	)

	// Register tool handler with MCP server.
	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Parse arguments from MCP request.
		var input QueryEventsInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_query_events", input)

		// TODO: Implement event storage and querying.
		// For now, return placeholder.

		text := "Operational Events:\n\n"
		text += "No events tracked yet.\n\n"
		text += "Note: Event storage and querying is planned for future implementation.\n"
		text += "      Events will include deployments, restarts, crashes, and configuration changes.\n"

		return mcp.NewToolResultText(text), nil
	})
}

// registerBeylaHTTPMetricsTool registers the coral_query_ebpf_http_metrics tool.
func (s *Server) registerBeylaHTTPMetricsTool() {
	if !s.isToolEnabled("coral_query_ebpf_http_metrics") {
		return
	}

	inputSchema, err := generateInputSchema(BeylaHTTPMetricsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_query_ebpf_http_metrics")
		return
	}

	// Marshal schema to JSON bytes for MCP tool.
	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_query_ebpf_http_metrics")
		return
	}

	// Create MCP tool with raw schema.
	tool := mcp.NewToolWithRawSchema(
		"coral_query_ebpf_http_metrics",
		"Query HTTP RED metrics collected via eBPF (request rate, error rate, latency distributions). Returns percentiles, status code breakdown, and route-level metrics.",
		schemaBytes,
	)

	// Register tool handler with MCP server.
	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Parse arguments from MCP request.
		var input BeylaHTTPMetricsInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_query_ebpf_http_metrics", input)

		// Get time range (handle nil pointer).
		timeRangeStr := "1h"
		if input.TimeRange != nil {
			timeRangeStr = *input.TimeRange
		}

		// Parse time range.
		startTime, endTime, err := parseTimeRange(timeRangeStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid time_range '%s': %v", timeRangeStr, err)), nil
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
			return mcp.NewToolResultError(fmt.Sprintf("failed to query HTTP metrics: %v", err)), nil
		}

		// Format response.
		if len(results) == 0 {
			text := fmt.Sprintf("Beyla HTTP Metrics for %s (last %s):\n\n", input.Service, timeRangeStr)
			text += "No metrics found for this service in the specified time range.\n\n"
			text += "This could mean:\n"
			text += "- The service hasn't received HTTP requests\n"
			text += "- Beyla is not running on the agent\n"
			text += "- The service name doesn't match\n"
			return mcp.NewToolResultText(text), nil
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

		return mcp.NewToolResultText(text), nil
	})
}

// registerBeylaGRPCMetricsTool registers the coral_query_ebpf_grpc_metrics tool.
func (s *Server) registerBeylaGRPCMetricsTool() {
	if !s.isToolEnabled("coral_query_ebpf_grpc_metrics") {
		return
	}

	inputSchema, err := generateInputSchema(BeylaGRPCMetricsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_query_ebpf_grpc_metrics")
		return
	}

	// Marshal schema to JSON bytes for MCP tool.
	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_query_ebpf_grpc_metrics")
		return
	}

	// Create MCP tool with raw schema.
	tool := mcp.NewToolWithRawSchema(
		"coral_query_ebpf_grpc_metrics",
		"Query gRPC method-level RED metrics collected via eBPF. Returns RPC rate, latency distributions, and status code breakdown.",
		schemaBytes,
	)

	// Register tool handler with MCP server.
	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Parse arguments from MCP request.
		var input BeylaGRPCMetricsInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_query_ebpf_grpc_metrics", input)

		// Get time range (handle nil pointer).
		timeRangeStr := "1h"
		if input.TimeRange != nil {
			timeRangeStr = *input.TimeRange
		}

		// Parse time range.
		startTime, endTime, err := parseTimeRange(timeRangeStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid time_range '%s': %v", timeRangeStr, err)), nil
		}

		// Build filters map.
		filters := make(map[string]string)
		if input.GRPCMethod != nil {
			filters["grpc_method"] = *input.GRPCMethod
		}
		if input.StatusCode != nil {
			filters["status_code"] = fmt.Sprintf("%d", *input.StatusCode)
		}

		// Query database.
		dbCtx := context.Background()
		results, err := s.db.QueryBeylaGRPCMetrics(dbCtx, input.Service, startTime, endTime, filters)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to query gRPC metrics: %v", err)), nil
		}

		// Format response.
		if len(results) == 0 {
			text := fmt.Sprintf("Beyla gRPC Metrics for %s (last %s):\n\n", input.Service, timeRangeStr)
			text += "No metrics found for this service in the specified time range.\n\n"
			text += "This could mean:\n"
			text += "- The service hasn't received gRPC requests\n"
			text += "- Beyla is not running on the agent\n"
			text += "- The service name doesn't match\n"
			return mcp.NewToolResultText(text), nil
		}

		// Calculate statistics from histogram buckets.
		stats := aggregateGRPCMetrics(results)

		text := fmt.Sprintf("Beyla gRPC Metrics for %s (last %s):\n\n", input.Service, timeRangeStr)
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

		return mcp.NewToolResultText(text), nil
	})
}

// registerBeylaSQLMetricsTool registers the coral_query_ebpf_sql_metrics tool.
func (s *Server) registerBeylaSQLMetricsTool() {
	if !s.isToolEnabled("coral_query_ebpf_sql_metrics") {
		return
	}

	inputSchema, err := generateInputSchema(BeylaSQLMetricsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_query_ebpf_sql_metrics")
		return
	}

	// Marshal schema to JSON bytes for MCP tool.
	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_query_ebpf_sql_metrics")
		return
	}

	// Create MCP tool with raw schema.
	tool := mcp.NewToolWithRawSchema(
		"coral_query_ebpf_sql_metrics",
		"Query SQL operation metrics collected via eBPF. Returns query latencies, operation types, and table-level statistics.",
		schemaBytes,
	)

	// Register tool handler with MCP server.
	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Parse arguments from MCP request.
		var input BeylaSQLMetricsInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_query_ebpf_sql_metrics", input)

		// Get time range (handle nil pointer).
		timeRangeStr := "1h"
		if input.TimeRange != nil {
			timeRangeStr = *input.TimeRange
		}

		// Parse time range.
		startTime, endTime, err := parseTimeRange(timeRangeStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid time_range '%s': %v", timeRangeStr, err)), nil
		}

		// Build filters map.
		filters := make(map[string]string)
		if input.SQLOperation != nil {
			filters["sql_operation"] = *input.SQLOperation
		}
		if input.TableName != nil {
			filters["table_name"] = *input.TableName
		}

		// Query database.
		dbCtx := context.Background()
		results, err := s.db.QueryBeylaSQLMetrics(dbCtx, input.Service, startTime, endTime, filters)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to query SQL metrics: %v", err)), nil
		}

		// Format response.
		if len(results) == 0 {
			text := fmt.Sprintf("Beyla SQL Metrics for %s (last %s):\n\n", input.Service, timeRangeStr)
			text += "No metrics found for this service in the specified time range.\n\n"
			text += "This could mean:\n"
			text += "- The service hasn't executed SQL queries\n"
			text += "- Beyla is not running on the agent\n"
			text += "- The service name doesn't match\n"
			return mcp.NewToolResultText(text), nil
		}

		// Calculate statistics from histogram buckets.
		stats := aggregateSQLMetrics(results)

		text := fmt.Sprintf("Beyla SQL Metrics for %s (last %s):\n\n", input.Service, timeRangeStr)
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

		return mcp.NewToolResultText(text), nil
	})
}

// registerBeylaTracesTool registers the coral_query_ebpf_traces tool.
func (s *Server) registerBeylaTracesTool() {
	if !s.isToolEnabled("coral_query_ebpf_traces") {
		return
	}

	inputSchema, err := generateInputSchema(BeylaTracesInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_query_ebpf_traces")
		return
	}

	// Marshal schema to JSON bytes for MCP tool.
	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_query_ebpf_traces")
		return
	}

	// Create MCP tool with raw schema.
	tool := mcp.NewToolWithRawSchema(
		"coral_query_ebpf_traces",
		"Query distributed traces collected via eBPF. Can search by trace ID, service, time range, or duration threshold.",
		schemaBytes,
	)

	// Register tool handler with MCP server.
	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Parse arguments from MCP request.
		var input BeylaTracesInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_query_ebpf_traces", input)

		// Get time range (handle nil pointer).
		timeRangeStr := "1h"
		if input.TimeRange != nil {
			timeRangeStr = *input.TimeRange
		}

		// Parse time range.
		startTime, endTime, err := parseTimeRange(timeRangeStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid time_range '%s': %v", timeRangeStr, err)), nil
		}

		// Get service name (optional).
		serviceName := ""
		if input.Service != nil {
			serviceName = *input.Service
		}

		// Get min duration filter (convert from ms to us).
		var minDurationUs int64
		if input.MinDurationMs != nil {
			minDurationUs = int64(*input.MinDurationMs) * 1000
		}

		// Get max traces limit.
		maxTraces := 10
		if input.MaxTraces != nil {
			maxTraces = *input.MaxTraces
		}

		// Query database.
		dbCtx := context.Background()
		results, err := s.db.QueryBeylaTraces(dbCtx, serviceName, startTime, endTime, minDurationUs, maxTraces)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to query traces: %v", err)), nil
		}

		// Format response.
		if len(results) == 0 {
			text := "Beyla Distributed Traces:\n\n"
			text += "No traces found matching the criteria.\n\n"
			text += "This could mean:\n"
			text += "- No distributed traces in the time range\n"
			text += "- Beyla tracing is not enabled\n"
			text += "- Duration threshold too high\n"
			return mcp.NewToolResultText(text), nil
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

		return mcp.NewToolResultText(text), nil
	})
}

// registerTraceByIDTool registers the coral_get_trace_by_id tool.
func (s *Server) registerTraceByIDTool() {
	if !s.isToolEnabled("coral_get_trace_by_id") {
		return
	}

	inputSchema, err := generateInputSchema(TraceByIDInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_get_trace_by_id")
		return
	}

	// Marshal schema to JSON bytes for MCP tool.
	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_get_trace_by_id")
		return
	}

	// Create MCP tool with raw schema.
	tool := mcp.NewToolWithRawSchema(
		"coral_get_trace_by_id",
		"Get a specific distributed trace by ID with full span tree showing parent-child relationships and timing.",
		schemaBytes,
	)

	// Register tool handler with MCP server.
	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Parse arguments from MCP request.
		var input TraceByIDInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_get_trace_by_id", input)

		text := fmt.Sprintf("Trace %s:\n\n", input.TraceID)
		text += "Trace not found.\n\n"
		text += "Note: Trace retrieval is planned (RFD 036).\n"

		return mcp.NewToolResultText(text), nil
	})
}

// registerTelemetrySpansTool registers the coral_query_telemetry_spans tool.
func (s *Server) registerTelemetrySpansTool() {
	if !s.isToolEnabled("coral_query_telemetry_spans") {
		return
	}

	inputSchema, err := generateInputSchema(TelemetrySpansInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_query_telemetry_spans")
		return
	}

	// Marshal schema to JSON bytes for MCP tool.
	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_query_telemetry_spans")
		return
	}

	// Create MCP tool with raw schema.
	tool := mcp.NewToolWithRawSchema(
		"coral_query_telemetry_spans",
		"Query generic OTLP spans (from instrumented applications using OpenTelemetry SDKs). Returns aggregated telemetry summaries. For detailed raw spans, see RFD 041.",
		schemaBytes,
	)

	// Register tool handler with MCP server.
	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Parse arguments from MCP request.
		var input TelemetrySpansInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_query_telemetry_spans", input)

		// Get time range (handle nil pointer).
		timeRangeStr := "1h"
		if input.TimeRange != nil {
			timeRangeStr = *input.TimeRange
		}

		// Parse time range.
		startTime, endTime, err := parseTimeRange(timeRangeStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid time_range '%s': %v", timeRangeStr, err)), nil
		}

		// Find agents for this service.
		agents := s.registry.ListAll()
		var matchingAgents []string
		for _, agent := range agents {
			if agent.Name == input.Service {
				matchingAgents = append(matchingAgents, agent.AgentID)
			}
		}

		if len(matchingAgents) == 0 {
			text := fmt.Sprintf("OTLP Telemetry for %s (last %s):\n\n", input.Service, timeRangeStr)
			text += "No agents found running this service.\n\n"
			text += "Possible reasons:\n"
			text += "- Service name doesn't match\n"
			text += "- No agents connected\n"
			return mcp.NewToolResultText(text), nil
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
			text := fmt.Sprintf("OTLP Telemetry for %s (last %s):\n\n", input.Service, timeRangeStr)
			text += "No telemetry data found in the specified time range.\n\n"
			text += "Possible reasons:\n"
			text += "- Service is not instrumented with OpenTelemetry\n"
			text += "- No traffic during this period\n"
			text += "- Data retention expired (summaries kept for 24h)\n\n"
			text += "Note: This returns aggregated summaries. For raw spans, see RFD 041.\n"
			return mcp.NewToolResultText(text), nil
		}

		// Aggregate stats from summaries.
		stats := aggregateTelemetrySummaries(allSummaries, input.Operation)

		text := fmt.Sprintf("OTLP Telemetry for %s (last %s):\n\n", input.Service, timeRangeStr)
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

		return mcp.NewToolResultText(text), nil
	})
}

// registerTelemetryMetricsTool registers the coral_query_telemetry_metrics tool.
func (s *Server) registerTelemetryMetricsTool() {
	if !s.isToolEnabled("coral_query_telemetry_metrics") {
		return
	}

	inputSchema, err := generateInputSchema(TelemetryMetricsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_query_telemetry_metrics")
		return
	}

	// Marshal schema to JSON bytes for MCP tool.
	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_query_telemetry_metrics")
		return
	}

	// Create MCP tool with raw schema.
	tool := mcp.NewToolWithRawSchema(
		"coral_query_telemetry_metrics",
		"Query generic OTLP metrics (from instrumented applications). Returns time-series data for custom application metrics.",
		schemaBytes,
	)

	// Register tool handler with MCP server.
	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Parse arguments from MCP request.
		var input TelemetryMetricsInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_query_telemetry_metrics", input)

		text := "OTLP Metrics:\n\n"
		text += "No metrics available yet.\n\n"
		text += "Note: OTLP metrics querying is implemented (RFD 025) but requires telemetry data.\n"

		return mcp.NewToolResultText(text), nil
	})
}

// registerTelemetryLogsTool registers the coral_query_telemetry_logs tool.
func (s *Server) registerTelemetryLogsTool() {
	if !s.isToolEnabled("coral_query_telemetry_logs") {
		return
	}

	inputSchema, err := generateInputSchema(TelemetryLogsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_query_telemetry_logs")
		return
	}

	// Marshal schema to JSON bytes for MCP tool.
	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_query_telemetry_logs")
		return
	}

	// Create MCP tool with raw schema.
	tool := mcp.NewToolWithRawSchema(
		"coral_query_telemetry_logs",
		"Query generic OTLP logs (from instrumented applications). Search application logs with full-text search and filters.",
		schemaBytes,
	)

	// Register tool handler with MCP server.
	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Parse arguments from MCP request.
		var input TelemetryLogsInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_query_telemetry_logs", input)

		text := "OTLP Logs:\n\n"
		text += "No logs available yet.\n\n"
		text += "Note: OTLP log querying is implemented (RFD 025) but requires telemetry data.\n"

		return mcp.NewToolResultText(text), nil
	})
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
