package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/coral-io/coral/coral/agent/v1"
	"github.com/coral-io/coral/coral/agent/v1/agentv1connect"
	"github.com/coral-io/coral/internal/colony/database"
	"github.com/coral-io/coral/internal/colony/registry"
)

// Tool execution wrappers for direct RPC calls.
// These methods parse JSON arguments and execute tool logic,
// enabling the test-tool CLI command and direct RPC access.

// executeServiceHealthTool executes coral_get_service_health.
func (s *Server) executeServiceHealthTool(ctx context.Context, argumentsJSON string) (string, error) {
	var input ServiceHealthInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_get_service_health", input)

	// Get service filter.
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
		// Apply filter if specified (RFD 044: use Services[] array, not ComponentName).
		if serviceFilter != "" {
			matchFound := false
			for _, svc := range agent.Services {
				if matchesPattern(svc.Name, serviceFilter) {
					matchFound = true
					break
				}
			}
			if !matchFound {
				continue
			}
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

		// Build service names list from Services[] array.
		serviceNames := make([]string, 0, len(agent.Services))
		for _, svc := range agent.Services {
			serviceNames = append(serviceNames, svc.Name)
		}
		servicesStr := strings.Join(serviceNames, ", ")
		if servicesStr == "" {
			servicesStr = agent.Name // Fallback for backward compatibility
		}

		serviceStatuses = append(serviceStatuses, map[string]interface{}{
			"service":   servicesStr,
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

	// Format response.
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
}

// executeServiceTopologyTool executes coral_get_service_topology.
func (s *Server) executeServiceTopologyTool(ctx context.Context, argumentsJSON string) (string, error) {
	var input ServiceTopologyInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_get_service_topology", input)

	agents := s.registry.ListAll()

	text := "Service Topology:\n\n"
	text += fmt.Sprintf("Connected Agents (%d):\n", len(agents))

	for _, agent := range agents {
		// Build service names list from Services[] array (RFD 044).
		serviceNames := make([]string, 0, len(agent.Services))
		for _, svc := range agent.Services {
			serviceNames = append(serviceNames, svc.Name)
		}
		servicesStr := strings.Join(serviceNames, ", ")
		if servicesStr == "" {
			servicesStr = agent.Name // Fallback for backward compatibility
		}

		text += fmt.Sprintf("  - %s (services: %s, mesh IP: %s)\n", agent.AgentID, servicesStr, agent.MeshIPv4)
	}

	text += "\n"
	text += "Note: Dependency graph discovery from distributed traces is not yet implemented.\n"
	text += "      See RFD 036 for planned trace-based topology analysis.\n"

	return text, nil
}

// executeQueryEventsTool executes coral_query_events.
func (s *Server) executeQueryEventsTool(ctx context.Context, argumentsJSON string) (string, error) {
	var input QueryEventsInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_query_events", input)

	text := "Operational Events:\n\n"
	text += "No events tracked yet.\n\n"
	text += "Note: Event storage and querying is planned for future implementation.\n"
	text += "      Events will include deployments, restarts, crashes, and configuration changes.\n"

	return text, nil
}

// executeBeylaHTTPMetricsTool executes coral_query_beyla_http_metrics.
func (s *Server) executeBeylaHTTPMetricsTool(ctx context.Context, argumentsJSON string) (string, error) {
	var input BeylaHTTPMetricsInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_query_beyla_http_metrics", input)

	// Get time range.
	timeRangeStr := "1h"
	if input.TimeRange != nil {
		timeRangeStr = *input.TimeRange
	}

	// Parse time range.
	startTime, endTime, err := parseTimeRange(timeRangeStr)
	if err != nil {
		return "", fmt.Errorf("invalid time_range '%s': %w", timeRangeStr, err)
	}

	// Build filters.
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

	// Query HTTP metrics.
	metrics, err := s.db.QueryBeylaHTTPMetrics(ctx, input.Service, startTime, endTime, filters)
	if err != nil {
		return "", fmt.Errorf("failed to query HTTP metrics: %w", err)
	}

	// Format result.
	return formatBeylaHTTPMetrics(metrics, input.Service, timeRangeStr), nil
}

// executeBeylaGRPCMetricsTool executes coral_query_beyla_grpc_metrics.
func (s *Server) executeBeylaGRPCMetricsTool(ctx context.Context, argumentsJSON string) (string, error) {
	var input BeylaGRPCMetricsInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_query_beyla_grpc_metrics", input)

	timeRangeStr := "1h"
	if input.TimeRange != nil {
		timeRangeStr = *input.TimeRange
	}

	startTime, endTime, err := parseTimeRange(timeRangeStr)
	if err != nil {
		return "", fmt.Errorf("invalid time_range '%s': %w", timeRangeStr, err)
	}

	filters := make(map[string]string)
	if input.GRPCMethod != nil {
		filters["grpc_method"] = *input.GRPCMethod
	}

	metrics, err := s.db.QueryBeylaGRPCMetrics(ctx, input.Service, startTime, endTime, filters)
	if err != nil {
		return "", fmt.Errorf("failed to query gRPC metrics: %w", err)
	}

	return formatBeylaGRPCMetrics(metrics, input.Service, timeRangeStr), nil
}

// executeBeylaSQLMetricsTool executes coral_query_beyla_sql_metrics.
func (s *Server) executeBeylaSQLMetricsTool(ctx context.Context, argumentsJSON string) (string, error) {
	var input BeylaSQLMetricsInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_query_beyla_sql_metrics", input)

	timeRangeStr := "1h"
	if input.TimeRange != nil {
		timeRangeStr = *input.TimeRange
	}

	startTime, endTime, err := parseTimeRange(timeRangeStr)
	if err != nil {
		return "", fmt.Errorf("invalid time_range '%s': %w", timeRangeStr, err)
	}

	filters := make(map[string]string)
	if input.SQLOperation != nil {
		filters["sql_operation"] = *input.SQLOperation
	}
	if input.TableName != nil {
		filters["table_name"] = *input.TableName
	}

	metrics, err := s.db.QueryBeylaSQLMetrics(ctx, input.Service, startTime, endTime, filters)
	if err != nil {
		return "", fmt.Errorf("failed to query SQL metrics: %w", err)
	}

	return formatBeylaSQLMetrics(metrics, input.Service, timeRangeStr), nil
}

// executeBeylaTracesTool executes coral_query_beyla_traces.
func (s *Server) executeBeylaTracesTool(ctx context.Context, argumentsJSON string) (string, error) {
	var input BeylaTracesInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_query_beyla_traces", input)

	timeRangeStr := "1h"
	if input.TimeRange != nil {
		timeRangeStr = *input.TimeRange
	}

	startTime, endTime, err := parseTimeRange(timeRangeStr)
	if err != nil {
		return "", fmt.Errorf("invalid time_range '%s': %w", timeRangeStr, err)
	}

	serviceName := ""
	if input.Service != nil {
		serviceName = *input.Service
	}

	minDurationUs := int64(0)
	if input.MinDurationMs != nil {
		minDurationUs = int64(*input.MinDurationMs * 1000) // Convert ms to microseconds
	}

	maxTraces := 10
	if input.MaxTraces != nil {
		maxTraces = *input.MaxTraces
	}

	traces, err := s.db.QueryBeylaTraces(ctx, serviceName, startTime, endTime, minDurationUs, maxTraces)
	if err != nil {
		return "", fmt.Errorf("failed to query traces: %w", err)
	}

	return formatBeylaTraces(traces, timeRangeStr), nil
}

// executeTraceByIDTool executes coral_get_trace_by_id.
func (s *Server) executeTraceByIDTool(ctx context.Context, argumentsJSON string) (string, error) {
	var input TraceByIDInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_get_trace_by_id", input)

	format := "tree"
	if input.Format != nil {
		format = *input.Format
	}

	// Query traces to find the one with matching trace ID.
	// Note: This is a workaround since there's no direct GetTraceByID method yet.
	startTime := time.Now().Add(-24 * time.Hour) // Look back 24 hours
	endTime := time.Now()
	traces, err := s.db.QueryBeylaTraces(ctx, "", startTime, endTime, 0, 100)
	if err != nil {
		return "", fmt.Errorf("failed to query traces: %w", err)
	}

	// Find the trace with the matching ID.
	for _, trace := range traces {
		if trace.TraceID == input.TraceID {
			return formatTraceByID(trace, format), nil
		}
	}

	return "", fmt.Errorf("trace not found: %s", input.TraceID)
}

// executeTelemetrySpansTool executes coral_query_telemetry_spans.
func (s *Server) executeTelemetrySpansTool(ctx context.Context, argumentsJSON string) (string, error) {
	var input TelemetrySpansInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_query_telemetry_spans", input)

	text := fmt.Sprintf("OTLP Telemetry Spans (Service: %s):\n\n", input.Service)
	text += "No span data available yet.\n\n"
	text += "Note: OTLP span queries are not yet implemented.\n"
	text += "      See RFD 025 for planned OTLP ingestion.\n"
	text += "      For detailed raw span queries, see RFD 041 (agent direct queries).\n"

	return text, nil
}

// executeTelemetryMetricsTool executes coral_query_telemetry_metrics.
func (s *Server) executeTelemetryMetricsTool(ctx context.Context, argumentsJSON string) (string, error) {
	var input TelemetryMetricsInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_query_telemetry_metrics", input)

	text := "OTLP Telemetry Metrics:\n\n"
	text += "No metric data available yet.\n\n"
	text += "Note: OTLP metric queries are not yet implemented.\n"
	text += "      See RFD 025 for planned OTLP ingestion.\n"

	return text, nil
}

// executeTelemetryLogsTool executes coral_query_telemetry_logs.
func (s *Server) executeTelemetryLogsTool(ctx context.Context, argumentsJSON string) (string, error) {
	var input TelemetryLogsInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_query_telemetry_logs", input)

	text := "OTLP Telemetry Logs:\n\n"
	text += "No log data available yet.\n\n"
	text += "Note: OTLP log queries are not yet implemented.\n"
	text += "      See RFD 025 for planned OTLP ingestion.\n"

	return text, nil
}

// Phase 3: Live Debugging Tool Executions

// executeStartEBPFCollectorTool executes coral_start_ebpf_collector.
func (s *Server) executeStartEBPFCollectorTool(ctx context.Context, argumentsJSON string) (string, error) {
	var input StartEBPFCollectorInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_start_ebpf_collector", input)

	// Resolve target agent (RFD 044: agent ID or service name with disambiguation).
	agent, err := s.resolveAgent(input.AgentID, input.Service)
	if err != nil {
		return "", err
	}

	text := fmt.Sprintf("eBPF Collector: %s\n\n", input.CollectorType)
	text += "Status: Not yet implemented\n\n"
	text += fmt.Sprintf("Target Service: %s (agent: %s)\n", input.Service, agent.AgentID)

	if input.DurationSeconds != nil {
		text += fmt.Sprintf("Duration: %d seconds\n", *input.DurationSeconds)
	} else {
		if input.ConfigJSON != nil {
			text += fmt.Sprintf("Config: %s\n", *input.ConfigJSON)
		}
	}

	text += "\n"
	text += "Implementation Status:\n"
	text += "  - RFD 013 (eBPF framework) is in partial implementation\n"
	text += "  - Agent eBPF manager has capability detection but no real collectors yet\n"
	text += "  - Colony integration and data storage are pending\n"
	text += "\n"
	text += "Once implemented, this tool will:\n"
	text += "  1. Start eBPF collector on target agent\n"
	text += "  2. Collect profiling data for specified duration\n"
	text += "  3. Stream results to colony for analysis\n"
	text += "  4. Return collector ID for querying results\n"

	return text, nil
}

// executeStopEBPFCollectorTool executes coral_stop_ebpf_collector.
func (s *Server) executeStopEBPFCollectorTool(ctx context.Context, argumentsJSON string) (string, error) {
	var input StopEBPFCollectorInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_stop_ebpf_collector", input)

	text := fmt.Sprintf("eBPF Collector Stop: %s\n\n", input.CollectorID)
	text += "Status: Not yet implemented\n\n"
	text += "Implementation Status:\n"
	text += "  - RFD 013 (eBPF framework) is in partial implementation\n"
	text += "  - Collector lifecycle management is pending\n"
	text += "\n"
	text += "Once implemented, this tool will:\n"
	text += "  1. Send stop signal to running eBPF collector\n"
	text += "  2. Flush remaining data to colony\n"
	text += "  3. Clean up eBPF programs and maps\n"
	text += "  4. Return final collector status and data summary\n"

	return text, nil
}

// executeListEBPFCollectorsTool executes coral_list_ebpf_collectors.
func (s *Server) executeListEBPFCollectorsTool(ctx context.Context, argumentsJSON string) (string, error) {
	var input ListEBPFCollectorsInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_list_ebpf_collectors", input)

	// Optionally filter by agent (RFD 044: agent ID or service name with disambiguation).
	var targetAgentID string
	if input.AgentID != nil || (input.Service != nil && *input.Service != "") {
		serviceName := ""
		if input.Service != nil {
			serviceName = *input.Service
		}
		agent, err := s.resolveAgent(input.AgentID, serviceName)
		if err != nil {
			return "", err
		}
		targetAgentID = agent.AgentID
	}

	text := "Active eBPF Collectors:\n\n"
	if targetAgentID != "" {
		text = fmt.Sprintf("Active eBPF Collectors (agent: %s):\n\n", targetAgentID)
	}
	text += "No active collectors.\n\n"
	text += "Implementation Status:\n"
	text += "  - RFD 013 (eBPF framework) is in partial implementation\n"
	text += "  - Collector registry and status tracking are pending\n"
	text += "\n"
	text += "Once implemented, this tool will show:\n"
	text += "  - Collector ID and type (cpu_profile, syscall_stats, etc.)\n"
	text += "  - Target service name\n"
	text += "  - Start time and remaining duration\n"
	text += "  - Data collection status (active, stopping, completed)\n"
	text += "  - Samples collected so far\n"

	return text, nil
}

// executeShellExecTool executes coral_shell_exec (RFD 045).
func (s *Server) executeShellExecTool(ctx context.Context, argumentsJSON string) (string, error) {
	var input ShellExecInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_shell_exec", input)

	// Validate command.
	if len(input.Command) == 0 {
		return "", fmt.Errorf("command cannot be empty")
	}

	// Resolve target agent (RFD 044: agent ID or service name with disambiguation).
	agent, err := s.resolveAgent(input.AgentID, input.Service)
	if err != nil {
		return "", err
	}

	// Validate agent status.
	status := registry.DetermineStatus(agent.LastSeen, time.Now())
	if status == registry.StatusUnhealthy {
		return "", fmt.Errorf("agent %s is unhealthy (last seen %s ago) - command execution may fail",
			agent.AgentID, formatDuration(time.Since(agent.LastSeen)))
	}

	// Create gRPC client to agent.
	agentURL := fmt.Sprintf("http://%s:9001", agent.MeshIPv4)
	client := agentv1connect.NewAgentServiceClient(http.DefaultClient, agentURL)

	// Prepare request.
	timeout := uint32(30)
	if input.TimeoutSeconds != nil {
		timeout = *input.TimeoutSeconds
		if timeout > 300 {
			timeout = 300
		}
	}

	req := &agentv1.ShellExecRequest{
		Command:        input.Command,
		UserId:         "mcp-server", // TODO: Get from MCP context
		TimeoutSeconds: timeout,
	}

	if input.WorkingDir != nil {
		req.WorkingDir = *input.WorkingDir
	}

	if input.Env != nil {
		req.Env = input.Env
	}

	// Execute command with timeout.
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout+5)*time.Second)
	defer cancel()

	resp, err := client.ShellExec(execCtx, connect.NewRequest(req))
	if err != nil {
		return "", fmt.Errorf("failed to execute command on agent %s: %w", agent.AgentID, err)
	}

	// Format response.
	return formatShellExecResponse(agent, resp.Msg), nil
}

// executeContainerExecTool executes coral_container_exec (RFD 056).
func (s *Server) executeContainerExecTool(ctx context.Context, argumentsJSON string) (string, error) {
	var input ContainerExecInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_container_exec", input)

	// Validate command.
	if len(input.Command) == 0 {
		return "", fmt.Errorf("command cannot be empty")
	}

	// Resolve target agent (RFD 044: agent ID or service name with disambiguation).
	agent, err := s.resolveAgent(input.AgentID, input.Service)
	if err != nil {
		return "", err
	}

	// Validate agent status.
	status := registry.DetermineStatus(agent.LastSeen, time.Now())
	if status == registry.StatusUnhealthy {
		return "", fmt.Errorf("agent %s is unhealthy (last seen %s ago) - command execution may fail",
			agent.AgentID, formatDuration(time.Since(agent.LastSeen)))
	}

	// Create gRPC client to agent.
	agentURL := fmt.Sprintf("http://%s:9001", agent.MeshIPv4)
	client := agentv1connect.NewAgentServiceClient(http.DefaultClient, agentURL)

	// Prepare request.
	timeout := uint32(30)
	if input.TimeoutSeconds != nil {
		timeout = *input.TimeoutSeconds
		if timeout > 300 {
			timeout = 300
		}
	}

	req := &agentv1.ContainerExecRequest{
		Command:        input.Command,
		UserId:         "mcp-server", // TODO: Get from MCP context
		TimeoutSeconds: timeout,
	}

	if input.ContainerName != nil {
		req.ContainerName = *input.ContainerName
	}

	if input.WorkingDir != nil {
		req.WorkingDir = *input.WorkingDir
	}

	if input.Env != nil {
		req.Env = input.Env
	}

	if len(input.Namespaces) > 0 {
		req.Namespaces = input.Namespaces
	}

	// Execute command with timeout.
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout+5)*time.Second)
	defer cancel()

	resp, err := client.ContainerExec(execCtx, connect.NewRequest(req))
	if err != nil {
		return "", fmt.Errorf("failed to execute command in container on agent %s: %w", agent.AgentID, err)
	}

	// Format response.
	return formatContainerExecResponse(agent, resp.Msg), nil
}

// Helper functions for agent resolution and disambiguation (RFD 044).

// resolveAgent resolves an agent by either agent ID or service name.
// If agent_id is specified, it takes precedence and must match exactly one agent.
// If only service is specified, it filters by service name and requires unique match.
// Returns error with agent ID list if multiple agents match the service.
func (s *Server) resolveAgent(agentID *string, serviceName string) (*registry.Entry, error) {
	// Agent ID lookup takes precedence (unambiguous).
	if agentID != nil && *agentID != "" {
		agent, err := s.registry.Get(*agentID)
		if err != nil {
			return nil, fmt.Errorf("agent not found: %s", *agentID)
		}
		return agent, nil
	}

	// Fallback: Service-based lookup (with disambiguation).
	agents := s.registry.ListAll()
	var matchedAgents []*registry.Entry

	for _, agent := range agents {
		// Check Services[] array, not ComponentName (RFD 044).
		for _, svc := range agent.Services {
			if matchesPattern(svc.Name, serviceName) {
				matchedAgents = append(matchedAgents, agent)
				break
			}
		}
	}

	if len(matchedAgents) == 0 {
		return nil, fmt.Errorf("no agents found for service '%s'", serviceName)
	}

	// Disambiguation requirement (RFD 044).
	if len(matchedAgents) > 1 {
		var agentIDs []string
		for _, a := range matchedAgents {
			agentIDs = append(agentIDs, a.AgentID)
		}
		return nil, fmt.Errorf(
			"multiple agents found for service '%s': %s\nPlease specify agent_id parameter to disambiguate",
			serviceName,
			strings.Join(agentIDs, ", "),
		)
	}

	return matchedAgents[0], nil
}

// Formatting helper functions (referenced by execution methods).

func formatBeylaHTTPMetrics(metrics []*database.BeylaHTTPMetricResult, service string, timeRange string) string {
	text := fmt.Sprintf("HTTP Metrics for %s (last %s):\n\n", service, timeRange)
	if len(metrics) == 0 {
		text += "No HTTP metrics available.\n"
		return text
	}

	text += fmt.Sprintf("Found %d HTTP metric entries:\n\n", len(metrics))
	for _, m := range metrics {
		text += fmt.Sprintf("Route: %s %s\n", m.HTTPMethod, m.HTTPRoute)
		text += fmt.Sprintf("  Status: %d\n", m.HTTPStatusCode)
		text += fmt.Sprintf("  Count: %d\n", m.Count)
		text += fmt.Sprintf("  Latency bucket: %.1fms\n\n", m.LatencyBucketMs)
	}

	return text
}

func formatBeylaGRPCMetrics(metrics []*database.BeylaGRPCMetricResult, service string, timeRange string) string {
	text := fmt.Sprintf("gRPC Metrics for %s (last %s):\n\n", service, timeRange)
	if len(metrics) == 0 {
		text += "No gRPC metrics available.\n"
		return text
	}

	text += fmt.Sprintf("Found %d gRPC metric entries:\n\n", len(metrics))
	for _, m := range metrics {
		text += fmt.Sprintf("Method: %s\n", m.GRPCMethod)
		text += fmt.Sprintf("  Status: %d\n", m.GRPCStatusCode)
		text += fmt.Sprintf("  Count: %d\n", m.Count)
		text += fmt.Sprintf("  Latency bucket: %.1fms\n\n", m.LatencyBucketMs)
	}

	return text
}

func formatBeylaSQLMetrics(metrics []*database.BeylaSQLMetricResult, service string, timeRange string) string {
	text := fmt.Sprintf("SQL Metrics for %s (last %s):\n\n", service, timeRange)
	if len(metrics) == 0 {
		text += "No SQL metrics available.\n"
		return text
	}

	text += fmt.Sprintf("Found %d SQL metric entries:\n\n", len(metrics))
	for _, m := range metrics {
		text += fmt.Sprintf("Operation: %s on %s\n", m.SQLOperation, m.TableName)
		text += fmt.Sprintf("  Count: %d\n", m.Count)
		text += fmt.Sprintf("  Latency bucket: %.1fms\n\n", m.LatencyBucketMs)
	}

	return text
}

func formatBeylaTraces(traces []*database.BeylaTraceResult, timeRange string) string {
	text := fmt.Sprintf("Distributed Traces (last %s):\n\n", timeRange)
	if len(traces) == 0 {
		text += "No traces found.\n"
		return text
	}

	// Group spans by trace ID for better display.
	traceMap := make(map[string][]*database.BeylaTraceResult)
	for _, trace := range traces {
		traceMap[trace.TraceID] = append(traceMap[trace.TraceID], trace)
	}

	text += fmt.Sprintf("Found %d traces:\n\n", len(traceMap))
	for traceID, spans := range traceMap {
		text += fmt.Sprintf("  Trace ID: %s\n", traceID)
		text += fmt.Sprintf("    Spans: %d\n", len(spans))
		if len(spans) > 0 {
			totalDuration := float64(spans[0].DurationUs) / 1000.0 // Convert to ms
			text += fmt.Sprintf("    Duration: %.1fms\n", totalDuration)
		}
		text += "\n"
	}

	return text
}

func formatTraceByID(trace *database.BeylaTraceResult, format string) string {
	text := fmt.Sprintf("Trace: %s\n\n", trace.TraceID)
	text += fmt.Sprintf("Span: %s (%s)\n", trace.SpanName, trace.SpanKind)
	text += fmt.Sprintf("Service: %s\n", trace.ServiceName)
	text += fmt.Sprintf("Duration: %.1fms\n", float64(trace.DurationUs)/1000.0)
	text += fmt.Sprintf("Status: %d\n", trace.StatusCode)
	if trace.ParentSpanID != "" {
		text += fmt.Sprintf("Parent Span: %s\n", trace.ParentSpanID)
	}

	return text
}

// formatShellExecResponse formats the response for coral_shell_exec tool (RFD 045).
// Returns formatted command output with exit code and execution details.
func formatShellExecResponse(agent *registry.Entry, resp *agentv1.ShellExecResponse) string {
	// Build service names list.
	serviceNames := make([]string, 0, len(agent.Services))
	for _, svc := range agent.Services {
		serviceNames = append(serviceNames, svc.Name)
	}
	servicesStr := strings.Join(serviceNames, ", ")
	if servicesStr == "" {
		servicesStr = agent.Name
	}

	var text string

	// Header with execution details.
	text += fmt.Sprintf("Command executed on agent %s (%s)\n", agent.AgentID, servicesStr)
	text += fmt.Sprintf("Duration: %dms | Exit Code: %d | Session: %s\n\n",
		resp.DurationMs, resp.ExitCode, resp.SessionId)

	// Show error if present.
	if resp.Error != "" {
		text += fmt.Sprintf("❌ Error: %s\n\n", resp.Error)
	}

	// Show stdout.
	if len(resp.Stdout) > 0 {
		text += "STDOUT:\n"
		text += "```\n"
		text += string(resp.Stdout)
		text += "\n```\n\n"
	} else {
		text += "STDOUT: (empty)\n\n"
	}

	// Show stderr if present.
	if len(resp.Stderr) > 0 {
		text += "STDERR:\n"
		text += "```\n"
		text += string(resp.Stderr)
		text += "\n```\n\n"
	}

	// Add status summary.
	if resp.ExitCode == 0 && resp.Error == "" {
		text += "✅ Command completed successfully\n"
	} else if resp.ExitCode != 0 {
		text += fmt.Sprintf("⚠️  Command exited with non-zero code: %d\n", resp.ExitCode)
	}

	return text
}

// formatContainerExecResponse formats the response for coral_container_exec tool (RFD 056).
// Returns formatted command output with exit code, container PID, and execution details.
func formatContainerExecResponse(agent *registry.Entry, resp *agentv1.ContainerExecResponse) string {
	// Build service names list.
	serviceNames := make([]string, 0, len(agent.Services))
	for _, svc := range agent.Services {
		serviceNames = append(serviceNames, svc.Name)
	}
	servicesStr := strings.Join(serviceNames, ", ")
	if servicesStr == "" {
		servicesStr = agent.Name
	}

	var text string

	// Header with execution details.
	text += fmt.Sprintf("Command executed in container namespace on agent %s (%s)\n", agent.AgentID, servicesStr)
	text += fmt.Sprintf("Container PID: %d | Namespaces: %s\n",
		resp.ContainerPid, strings.Join(resp.NamespacesEntered, ", "))
	text += fmt.Sprintf("Duration: %dms | Exit Code: %d | Session: %s\n\n",
		resp.DurationMs, resp.ExitCode, resp.SessionId)

	// Show error if present.
	if resp.Error != "" {
		text += fmt.Sprintf("❌ Error: %s\n\n", resp.Error)
	}

	// Show stdout.
	if len(resp.Stdout) > 0 {
		text += "STDOUT:\n"
		text += "```\n"
		text += string(resp.Stdout)
		text += "\n```\n\n"
	} else {
		text += "STDOUT: (empty)\n\n"
	}

	// Show stderr if present.
	if len(resp.Stderr) > 0 {
		text += "STDERR:\n"
		text += "```\n"
		text += string(resp.Stderr)
		text += "\n```\n\n"
	}

	// Add status summary.
	if resp.ExitCode == 0 && resp.Error == "" {
		text += "✅ Command completed successfully\n"
	} else if resp.ExitCode != 0 {
		text += fmt.Sprintf("⚠️  Command exited with non-zero code: %d\n", resp.ExitCode)
	}

	return text
}
