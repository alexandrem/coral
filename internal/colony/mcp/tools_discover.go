package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/mark3labs/mcp-go/mcp"

	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
)

// executeDiscoverFunctionsTool executes the coral_discover_functions tool (RFD 069).
func (s *Server) executeDiscoverFunctionsTool(ctx context.Context, argumentsJSON string) (string, error) {
	if s.debugService == nil {
		return "", fmt.Errorf("debug service not available")
	}

	var input DiscoverFunctionsInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_discover_functions", input)

	// Set defaults
	maxResults := int32(20)
	if input.MaxResults != nil {
		maxResults = *input.MaxResults
	}
	includeMetrics := true
	if input.IncludeMetrics != nil {
		includeMetrics = *input.IncludeMetrics
	}
	prioritizeSlow := false
	if input.PrioritizeSlow != nil {
		prioritizeSlow = *input.PrioritizeSlow
	}
	serviceName := ""
	if input.Service != nil {
		serviceName = *input.Service
	}

	// Call DebugService.QueryFunctions
	req := connect.NewRequest(&debugpb.QueryFunctionsRequest{
		ServiceName:    serviceName,
		Query:          input.Query,
		MaxResults:     maxResults,
		IncludeMetrics: includeMetrics,
		PrioritizeSlow: prioritizeSlow,
	})

	resp, err := s.debugService.QueryFunctions(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to query functions: %w", err)
	}

	// Format response
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d function(s) matching '%s'", len(resp.Msg.Results), input.Query))
	if serviceName != "" {
		sb.WriteString(fmt.Sprintf(" in service '%s'", serviceName))
	}
	sb.WriteString(fmt.Sprintf("\nData coverage: %d%%\n\n", resp.Msg.DataCoveragePct))

	for i, result := range resp.Msg.Results {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, result.Function.Name))
		if result.Function.Package != "" {
			sb.WriteString(fmt.Sprintf("   Package: %s\n", result.Function.Package))
		}
		if result.Function.File != "" {
			sb.WriteString(fmt.Sprintf("   Location: %s:%d\n", result.Function.File, result.Function.Line))
		}
		if result.Search != nil {
			sb.WriteString(fmt.Sprintf("   Relevance: %.2f - %s\n", result.Search.Score, result.Search.Reason))
		}
		if result.Instrumentation != nil {
			sb.WriteString(fmt.Sprintf("   Probeable: %v, Has DWARF: %v\n",
				result.Instrumentation.IsProbeable, result.Instrumentation.HasDwarf))
		}
		if result.Metrics != nil {
			sb.WriteString(fmt.Sprintf("   Metrics [%s]:\n", result.Metrics.Source))
			if result.Metrics.P95 != nil {
				sb.WriteString(fmt.Sprintf("     P95: %s\n", result.Metrics.P95.AsDuration().String()))
			}
			if result.Metrics.CallsPerMin > 0 {
				sb.WriteString(fmt.Sprintf("     Calls/min: %.1f\n", result.Metrics.CallsPerMin))
			}
		}
		if result.Suggestion != "" {
			sb.WriteString(fmt.Sprintf("   → %s\n", result.Suggestion))
		}
		sb.WriteString("\n")
	}

	if resp.Msg.Suggestion != "" {
		sb.WriteString(fmt.Sprintf("💡 %s\n", resp.Msg.Suggestion))
	}

	return sb.String(), nil
}

// registerDiscoverFunctionsTool registers the coral_discover_functions tool (RFD 069).
func (s *Server) registerDiscoverFunctionsTool() {
	if !s.isToolEnabled("coral_discover_functions") {
		return
	}

	inputSchema, err := generateInputSchema(DiscoverFunctionsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_discover_functions")
		return
	}

	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_discover_functions")
		return
	}

	tool := mcp.NewToolWithRawSchema(
		"coral_discover_functions",
		"Unified function discovery with semantic search. Replaces coral_search_functions, coral_list_probeable_functions, and coral_get_function_context. Returns functions with embedded metrics, instrumentation info, and actionable suggestions. Use this for all function discovery needs (RFD 069).",
		schemaBytes,
	)

	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if s.debugService == nil {
			return mcp.NewToolResultError("debug service not available"), nil
		}

		var input DiscoverFunctionsInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_discover_functions", input)

		// Set defaults
		maxResults := int32(20)
		if input.MaxResults != nil {
			maxResults = *input.MaxResults
		}
		includeMetrics := true
		if input.IncludeMetrics != nil {
			includeMetrics = *input.IncludeMetrics
		}
		prioritizeSlow := false
		if input.PrioritizeSlow != nil {
			prioritizeSlow = *input.PrioritizeSlow
		}
		serviceName := ""
		if input.Service != nil {
			serviceName = *input.Service
		}

		// Call DebugService.QueryFunctions
		req := connect.NewRequest(&debugpb.QueryFunctionsRequest{
			ServiceName:    serviceName,
			Query:          input.Query,
			MaxResults:     maxResults,
			IncludeMetrics: includeMetrics,
			PrioritizeSlow: prioritizeSlow,
		})

		resp, err := s.debugService.QueryFunctions(ctx, req)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to query functions: %v", err)), nil
		}

		// Format response
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Found %d function(s) matching '%s'", len(resp.Msg.Results), input.Query))
		if serviceName != "" {
			sb.WriteString(fmt.Sprintf(" in service '%s'", serviceName))
		}
		sb.WriteString(fmt.Sprintf("\nData coverage: %d%%\n\n", resp.Msg.DataCoveragePct))

		for i, result := range resp.Msg.Results {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, result.Function.Name))
			if result.Function.Package != "" {
				sb.WriteString(fmt.Sprintf("   Package: %s\n", result.Function.Package))
			}
			if result.Function.File != "" {
				sb.WriteString(fmt.Sprintf("   Location: %s:%d\n", result.Function.File, result.Function.Line))
			}
			if result.Search != nil {
				sb.WriteString(fmt.Sprintf("   Relevance: %.2f - %s\n", result.Search.Score, result.Search.Reason))
			}
			if result.Instrumentation != nil {
				sb.WriteString(fmt.Sprintf("   Probeable: %v, Has DWARF: %v\n",
					result.Instrumentation.IsProbeable, result.Instrumentation.HasDwarf))
			}
			if result.Metrics != nil {
				sb.WriteString(fmt.Sprintf("   Metrics [%s]:\n", result.Metrics.Source))
				if result.Metrics.P95 != nil {
					sb.WriteString(fmt.Sprintf("     P95: %s\n", result.Metrics.P95.AsDuration().String()))
				}
				if result.Metrics.CallsPerMin > 0 {
					sb.WriteString(fmt.Sprintf("     Calls/min: %.1f\n", result.Metrics.CallsPerMin))
				}
			}
			if result.Suggestion != "" {
				sb.WriteString(fmt.Sprintf("   → %s\n", result.Suggestion))
			}
			sb.WriteString("\n")
		}

		if resp.Msg.Suggestion != "" {
			sb.WriteString(fmt.Sprintf("💡 %s\n", resp.Msg.Suggestion))
		}

		return mcp.NewToolResultText(sb.String()), nil
	})
}
