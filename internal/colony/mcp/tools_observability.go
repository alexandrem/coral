package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/mark3labs/mcp-go/mcp"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/colony"
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

			// Format results as text for LLM consumption
			text := "Service Health Summary:\n\n"
			for _, r := range results {
				statusIcon := "✅"
				switch r.Status {
				case "degraded":
					statusIcon = "⚠️"
				case "critical":
					statusIcon = "❌"
				}

				text += fmt.Sprintf("%s %s (%s)\n", statusIcon, r.ServiceName, r.Source)
				text += fmt.Sprintf("   Status: %s\n", r.Status)
				text += fmt.Sprintf("   Requests: %d\n", r.RequestCount)
				text += fmt.Sprintf("   Error Rate: %.2f%%\n", r.ErrorRate)
				text += fmt.Sprintf("   Avg Latency: %.2fms\n\n", r.AvgLatencyMs)
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

			// Count unique traces
			traceGroups := make(map[string][]*agentv1.EbpfTraceSpan)
			for _, span := range spans {
				traceGroups[span.TraceId] = append(traceGroups[span.TraceId], span)
			}

			// Format results as text for LLM consumption
			text := fmt.Sprintf("Found %d spans across %d traces:\n\n", len(spans), len(traceGroups))

			for traceID, traceSpans := range traceGroups {
				text += fmt.Sprintf("Trace: %s (%d spans)\n", traceID, len(traceSpans))
				for _, span := range traceSpans {
					durationMs := float64(span.DurationUs) / 1000.0
					sourceIcon := "📍" // Default eBPF
					if span.ServiceName != "" && len(span.ServiceName) > 6 {
						if span.ServiceName[len(span.ServiceName)-6:] == "[OTLP]" {
							sourceIcon = "📊" // OTLP data
						}
					}

					text += fmt.Sprintf("  %s %s: %s (%.2fms)\n",
						sourceIcon, span.ServiceName, span.SpanName, durationMs)

					// Show OTLP attributes if present
					if source, ok := span.Attributes["source"]; ok && source == "OTLP" {
						text += fmt.Sprintf("     Aggregated: %s spans, %s errors\n",
							span.Attributes["total_spans"], span.Attributes["error_count"])
					}
				}
				text += "\n"
			}

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

			// Format results as text for LLM consumption
			text := fmt.Sprintf("Metrics for %s:\n\n", serviceName)

			if len(metrics.HttpMetrics) > 0 {
				text += "HTTP Metrics:\n"
				for _, m := range metrics.HttpMetrics {
					text += fmt.Sprintf("  %s %s %s\n", m.HttpMethod, m.HttpRoute, m.ServiceName)
					// Calculate percentiles from buckets if available
					p50, p95, p99 := "-", "-", "-"
					if len(m.LatencyBuckets) >= 3 {
						p50 = fmt.Sprintf("%.2fms", m.LatencyBuckets[0])
						p95 = fmt.Sprintf("%.2fms", m.LatencyBuckets[1])
						p99 = fmt.Sprintf("%.2fms", m.LatencyBuckets[2])
					}
					text += fmt.Sprintf("    Requests: %d | P50: %s | P95: %s | P99: %s\n",
						m.RequestCount, p50, p95, p99)
				}
				text += "\n"
			}

			if len(metrics.GrpcMetrics) > 0 {
				text += fmt.Sprintf("gRPC Metrics: %d\n", len(metrics.GrpcMetrics))
			}

			if len(metrics.SqlMetrics) > 0 {
				text += fmt.Sprintf("SQL Metrics: %d\n", len(metrics.SqlMetrics))
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

			// Format not implemented message for LLM consumption
			text := "Log querying not yet implemented. Coral doesn't have log ingestion infrastructure yet.\n"
			text += "See RFD 067 for future work.\n"
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
