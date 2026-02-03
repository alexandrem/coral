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

// Unified Tools (RFD 067)

// registerUnifiedSummaryTool registers the coral_query_summary tool.
func (s *Server) registerUnifiedSummaryTool() {
	s.registerToolWithSchema(
		"coral_query_summary",
		"Get an enriched health summary for a service including system metrics, CPU profiling hotspots, deployment context, and regression indicators. Use this as the FIRST tool when diagnosing performance issues.",
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

			text, err := s.generateSummaryOutput(ctx, input)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to query summary: %v", err)), nil
			}

			return mcp.NewToolResultText(text), nil
		})
}

// generateSummaryOutput generates the text output for query_summary.
func (s *Server) generateSummaryOutput(ctx context.Context, input UnifiedSummaryInput) (string, error) {
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

	profilingConfig := colony.ProfilingEnrichmentConfig{
		Disabled:     s.config.ProfilingEnrichmentDisabled,
		TopKHotspots: s.config.ProfilingTopKHotspots,
	}
	// Override with per-request parameters.
	if input.IncludeProfiling != nil && !*input.IncludeProfiling {
		profilingConfig.Disabled = true
	}
	if input.TopK != nil && *input.TopK > 0 {
		profilingConfig.TopKHotspots = int(*input.TopK)
	}

	ebpfService := colony.NewEbpfQueryServiceWithConfig(s.db, profilingConfig)
	results, err := ebpfService.QueryUnifiedSummary(ctx, serviceName, startTime, endTime)
	if err != nil {
		return "", fmt.Errorf("failed to query summary: %w", err)
	}

	// Format results as text for LLM consumption
	text := "Service Health Summary:\n\n"
	for _, r := range results {
		statusIcon := "✅"
		switch r.Status {
		case colony.ServiceStatusDegraded:
			statusIcon = "⚠️"
		case colony.ServiceStatusCritical:
			statusIcon = "❌"
		case colony.ServiceStatusIdle:
			statusIcon = "💤"
		}

		text += fmt.Sprintf("%s %s (%s)\n", statusIcon, r.ServiceName, r.Source)
		text += fmt.Sprintf("   Status: %s\n", r.Status)
		text += fmt.Sprintf("   Requests: %d\n", r.RequestCount)
		text += fmt.Sprintf("   Error Rate: %.2f%%\n", r.ErrorRate)
		text += fmt.Sprintf("   Avg Latency: %.2fms\n", r.AvgLatencyMs)

		// Display host resources if available (RFD 071)
		if r.HostCPUUtilization > 0 || r.HostMemoryUtilization > 0 {
			text += "   Host Resources:\n"
			if r.HostCPUUtilization > 0 {
				text += fmt.Sprintf("     CPU: %.0f%% (avg: %.0f%%)\n",
					r.HostCPUUtilization,
					r.HostCPUUtilizationAvg)
			}
			if r.HostMemoryUtilization > 0 {
				text += fmt.Sprintf("     Memory: %.1fGB/%.1fGB (%.0f%%)\n",
					r.HostMemoryUsageGB,
					r.HostMemoryLimitGB,
					r.HostMemoryUtilization)
			}
		}

		// Display profiling summary if available (RFD 074).
		if r.ProfilingSummary != nil && len(r.ProfilingSummary.Hotspots) > 0 {
			hotspots := make([]database.ProfilingHotspot, len(r.ProfilingSummary.Hotspots))
			for i, h := range r.ProfilingSummary.Hotspots {
				hotspots[i] = database.ProfilingHotspot{
					Rank:        h.Rank,
					Frames:      h.Frames,
					Percentage:  h.Percentage,
					SampleCount: h.SampleCount,
				}
			}
			text += database.FormatCompactSummary(
				r.ProfilingSummary.SamplingPeriod,
				r.ProfilingSummary.TotalSamples,
				hotspots,
			)
		}

		// Display memory profiling summary if available (RFD 077).
		memSummaries, memErr := s.db.QueryMemoryProfileSummaries(ctx, r.ServiceName, startTime, endTime)
		if memErr == nil && len(memSummaries) > 0 {
			// Aggregate memory hotspots.
			var totalMemBytes int64
			funcBytes := make(map[string]int64)
			for _, ms := range memSummaries {
				totalMemBytes += ms.AllocBytes
				// Use stack hash as key for now; decode frames for top entries.
				if len(ms.StackFrameIDs) > 0 {
					frameNames, err := s.db.DecodeStackFrames(ctx, ms.StackFrameIDs)
					if err == nil && len(frameNames) > 0 {
						leaf := frameNames[len(frameNames)-1]
						funcBytes[leaf] += ms.AllocBytes
					}
				}
			}

			if totalMemBytes > 0 {
				text += "   Memory Profiling (RFD 077):\n"
				text += fmt.Sprintf("     Total alloc: %d bytes\n", totalMemBytes)

				// Show top 3 memory allocators.
				type memEntry struct {
					name  string
					bytes int64
				}
				var memEntries []memEntry
				for name, bytes := range funcBytes {
					memEntries = append(memEntries, memEntry{name, bytes})
				}
				for i := range memEntries {
					for j := i + 1; j < len(memEntries); j++ {
						if memEntries[j].bytes > memEntries[i].bytes {
							memEntries[i], memEntries[j] = memEntries[j], memEntries[i]
						}
					}
				}
				if len(memEntries) > 3 {
					memEntries = memEntries[:3]
				}
				for _, me := range memEntries {
					pct := float64(me.bytes) / float64(totalMemBytes) * 100
					text += fmt.Sprintf("     %.1f%% %s\n", pct, me.name)
				}
			}
		}

		// Display deployment context (RFD 074).
		if r.Deployment != nil {
			text += fmt.Sprintf("   Deployment: %s (deployed %s ago)\n",
				r.Deployment.BuildID, r.Deployment.Age)
		}

		// Display regression indicators (RFD 074).
		if len(r.RegressionIndicators) > 0 {
			text += "   Regressions:\n"
			for _, ind := range r.RegressionIndicators {
				text += fmt.Sprintf("     ⚠️  %s\n", ind.Message)
			}
		}

		text += "\n"
	}

	return text, nil
}

// registerDebugCPUProfileTool registers the coral_debug_cpu_profile tool (RFD 074).
func (s *Server) registerDebugCPUProfileTool() {
	s.registerToolWithSchema(
		"coral_debug_cpu_profile",
		"Collect a high-frequency CPU profile (99Hz) for detailed analysis of specific functions. Use this AFTER coral_query_summary identifies a CPU hotspot that needs line-level investigation. Returns top stacks with sample counts.",
		DebugCPUProfileInput{},
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var input DebugCPUProfileInput
			if request.Params.Arguments != nil {
				argBytes, err := json.Marshal(request.Params.Arguments)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
				}
				if err := json.Unmarshal(argBytes, &input); err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
				}
			}

			s.auditToolCall("coral_debug_cpu_profile", input)

			text, err := s.generateDebugCPUProfileOutput(ctx, input)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to collect CPU profile: %v", err)), nil
			}

			return mcp.NewToolResultText(text), nil
		})
}

// generateDebugCPUProfileOutput generates the text output for coral_debug_cpu_profile (RFD 074).
// This queries stored profiling data rather than triggering a live profile (live profiling via RFD 070).
func (s *Server) generateDebugCPUProfileOutput(ctx context.Context, input DebugCPUProfileInput) (string, error) {
	durationSeconds := int32(30)
	if input.DurationSeconds != nil {
		durationSeconds = *input.DurationSeconds
		if durationSeconds > 300 {
			durationSeconds = 300
		}
		if durationSeconds < 10 {
			durationSeconds = 10
		}
	}

	// Query stored profiling data for the requested duration.
	endTime := time.Now()
	startTime := endTime.Add(-time.Duration(durationSeconds) * time.Second)

	topK := 10 // More detail for debug tool.
	result, err := s.db.GetTopKHotspots(ctx, input.Service, startTime, endTime, topK)
	if err != nil {
		return "", fmt.Errorf("failed to query CPU profile: %w", err)
	}

	if result == nil || result.TotalSamples == 0 {
		return fmt.Sprintf("No CPU profiling data available for service '%s' in the last %ds.\n"+
			"Ensure the service is running and the agent is collecting CPU profiles (RFD 072).\n",
			input.Service, durationSeconds), nil
	}

	format := "json"
	if input.Format != nil {
		format = *input.Format
	}

	if format == "folded" {
		// Folded stack format for flame graphs.
		text := ""
		for _, h := range result.Hotspots {
			line := ""
			for i, frame := range h.Frames {
				if i > 0 {
					line += ";"
				}
				line += frame
			}
			text += fmt.Sprintf("%s %d\n", line, h.SampleCount)
		}
		return text, nil
	}

	// JSON-like text format for LLM consumption.
	text := fmt.Sprintf("CPU Profile for %s (last %ds, %d total samples):\n\n",
		input.Service, durationSeconds, result.TotalSamples)

	for _, h := range result.Hotspots {
		name := "unknown"
		if len(h.Frames) > 0 {
			name = h.Frames[len(h.Frames)-1]
		}
		text += fmt.Sprintf("#%d %.1f%% (%d samples) %s\n", h.Rank, h.Percentage, h.SampleCount, name)

		// Full stack.
		text += "  Stack: "
		for i, frame := range h.Frames {
			if i > 0 {
				text += " → "
			}
			text += frame
		}
		text += "\n\n"
	}

	// Insights.
	if len(result.Hotspots) > 0 {
		text += "Insights:\n"
		h := result.Hotspots[0]
		name := "unknown"
		if len(h.Frames) > 0 {
			name = h.Frames[len(h.Frames)-1]
		}
		text += fmt.Sprintf("  Hottest function: %s (%.1f%%)\n", name, h.Percentage)
		text += fmt.Sprintf("  Total unique stacks: %d\n", len(result.Hotspots))
	}

	return text, nil
}

// executeDebugCPUProfileTool executes the coral_debug_cpu_profile tool (RFD 074).
func (s *Server) executeDebugCPUProfileTool(ctx context.Context, argsJSON string) (string, error) {
	var input DebugCPUProfileInput
	if err := json.Unmarshal([]byte(argsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_debug_cpu_profile", input)
	return s.generateDebugCPUProfileOutput(ctx, input)
}

// QueryMemoryProfileInput defines input for coral_query_memory_profile (RFD 077).
type QueryMemoryProfileInput struct {
	Service         string `json:"service" jsonschema:"description=Service name to profile"`
	DurationSeconds *int32 `json:"duration_seconds,omitempty" jsonschema:"description=Duration in seconds (default 30)"`
}

// registerQueryMemoryProfileTool registers the coral_query_memory_profile tool (RFD 077).
func (s *Server) registerQueryMemoryProfileTool() {
	s.registerToolWithSchema(
		"coral_query_memory_profile",
		"Query historical memory allocation profiles for a service. Shows top allocating functions and stack traces. Use this AFTER coral_query_summary identifies memory issues.",
		QueryMemoryProfileInput{},
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var input QueryMemoryProfileInput
			if request.Params.Arguments != nil {
				argBytes, err := json.Marshal(request.Params.Arguments)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
				}
				if err := json.Unmarshal(argBytes, &input); err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
				}
			}

			s.auditToolCall("coral_query_memory_profile", input)

			text, err := s.generateQueryMemoryProfileOutput(ctx, input)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to query memory profile: %v", err)), nil
			}

			return mcp.NewToolResultText(text), nil
		})
}

// generateQueryMemoryProfileOutput generates the text output for coral_query_memory_profile (RFD 077).
func (s *Server) generateQueryMemoryProfileOutput(ctx context.Context, input QueryMemoryProfileInput) (string, error) {
	durationSeconds := int32(300) // Default to last 5 minutes for memory.
	if input.DurationSeconds != nil {
		durationSeconds = *input.DurationSeconds
	}

	endTime := time.Now()
	startTime := endTime.Add(-time.Duration(durationSeconds) * time.Second)

	summaries, err := s.db.QueryMemoryProfileSummaries(ctx, input.Service, startTime, endTime)
	if err != nil {
		return "", fmt.Errorf("failed to query memory profiles: %w", err)
	}

	if len(summaries) == 0 {
		return fmt.Sprintf("No memory profiling data available for service '%s' in the last %ds.\n"+
			"Ensure the service is running and the agent is collecting memory profiles (RFD 077).\n",
			input.Service, durationSeconds), nil
	}

	// Aggregate by stack hash.
	type stackAgg struct {
		stackHash    string
		frameIDs     []int64
		allocBytes   int64
		allocObjects int64
	}

	aggregated := make(map[string]*stackAgg)
	var totalBytes int64
	for _, s := range summaries {
		totalBytes += s.AllocBytes
		if existing, exists := aggregated[s.StackHash]; exists {
			existing.allocBytes += s.AllocBytes
			existing.allocObjects += s.AllocObjects
		} else {
			aggregated[s.StackHash] = &stackAgg{
				stackHash:    s.StackHash,
				frameIDs:     s.StackFrameIDs,
				allocBytes:   s.AllocBytes,
				allocObjects: s.AllocObjects,
			}
		}
	}

	// Sort by alloc bytes descending and take top 10.
	type sortedEntry struct {
		frames       []string
		allocBytes   int64
		allocObjects int64
		pct          float64
	}

	var entries []sortedEntry
	for _, agg := range aggregated {
		frameNames, err := s.db.DecodeStackFrames(ctx, agg.frameIDs)
		if err != nil {
			continue
		}
		pct := 0.0
		if totalBytes > 0 {
			pct = float64(agg.allocBytes) / float64(totalBytes) * 100
		}
		entries = append(entries, sortedEntry{
			frames:       frameNames,
			allocBytes:   agg.allocBytes,
			allocObjects: agg.allocObjects,
			pct:          pct,
		})
	}

	// Simple sort by allocBytes descending.
	for i := range entries {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].allocBytes > entries[i].allocBytes {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	if len(entries) > 10 {
		entries = entries[:10]
	}

	text := fmt.Sprintf("Memory Profile for %s (last %ds):\n\n", input.Service, durationSeconds)
	text += fmt.Sprintf("Total allocation bytes: %d\n", totalBytes)
	text += fmt.Sprintf("Unique stacks: %d\n\n", len(aggregated))

	text += "Top Memory Allocators:\n"
	for i, e := range entries {
		name := "unknown"
		if len(e.frames) > 0 {
			name = e.frames[len(e.frames)-1]
		}
		text += fmt.Sprintf("#%d %.1f%% (%d bytes, %d objects) %s\n",
			i+1, e.pct, e.allocBytes, e.allocObjects, name)

		text += "  Stack: "
		for j, frame := range e.frames {
			if j > 0 {
				text += " → "
			}
			text += frame
		}
		text += "\n\n"
	}

	return text, nil
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

			text, err := s.generateTracesOutput(ctx, input)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to query traces: %v", err)), nil
			}

			return mcp.NewToolResultText(text), nil
		})
}

// generateTracesOutput generates the text output for query_traces.
func (s *Server) generateTracesOutput(ctx context.Context, input UnifiedTracesInput) (string, error) {
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

	return text, nil
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

			text, err := s.generateMetricsOutput(ctx, input)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to query metrics: %v", err)), nil
			}

			return mcp.NewToolResultText(text), nil
		})
}

// generateMetricsOutput generates the text output for query_metrics.
func (s *Server) generateMetricsOutput(ctx context.Context, input UnifiedMetricsInput) (string, error) {
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

	return text, nil
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
	return s.generateSummaryOutput(ctx, input)
}

// executeUnifiedTracesTool executes the coral_query_traces tool.
func (s *Server) executeUnifiedTracesTool(ctx context.Context, argsJSON string) (string, error) {
	var input UnifiedTracesInput
	if err := json.Unmarshal([]byte(argsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_query_traces", input)
	return s.generateTracesOutput(ctx, input)
}

// executeUnifiedMetricsTool executes the coral_query_metrics tool.
func (s *Server) executeUnifiedMetricsTool(ctx context.Context, argsJSON string) (string, error) {
	var input UnifiedMetricsInput
	if err := json.Unmarshal([]byte(argsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_query_metrics", input)
	return s.generateMetricsOutput(ctx, input)
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
