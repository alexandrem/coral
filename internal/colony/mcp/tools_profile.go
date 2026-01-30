package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/mark3labs/mcp-go/mcp"
	"google.golang.org/protobuf/types/known/durationpb"

	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
)

// executeProfileFunctionsTool executes the coral_profile_functions tool (RFD 069).
func (s *Server) executeProfileFunctionsTool(ctx context.Context, argumentsJSON string) (string, error) {
	if s.debugService == nil {
		return "", fmt.Errorf("debug service not available")
	}

	var input ProfileFunctionsInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_profile_functions", input)

	// Set defaults
	strategy := "critical_path"
	if input.Strategy != nil {
		strategy = *input.Strategy
	}
	maxFunctions := int32(20)
	if input.MaxFunctions != nil {
		maxFunctions = *input.MaxFunctions
	}
	async := false
	if input.Async != nil {
		async = *input.Async
	}
	sampleRate := 1.0
	if input.SampleRate != nil {
		sampleRate = *input.SampleRate
	}

	// Parse duration
	duration := time.Duration(60 * time.Second)
	if input.Duration != nil {
		d, err := time.ParseDuration(*input.Duration)
		if err != nil {
			return "", fmt.Errorf("invalid duration format: %w", err)
		}
		duration = d
	}

	// Call DebugService.ProfileFunctions
	req := connect.NewRequest(&debugpb.ProfileFunctionsRequest{
		ServiceName:  input.Service,
		Query:        input.Query,
		Strategy:     strategy,
		MaxFunctions: maxFunctions,
		Duration:     durationpb.New(duration),
		Async:        async,
		SampleRate:   sampleRate,
	})

	resp, err := s.debugService.ProfileFunctions(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to profile functions: %w", err)
	}

	// Format response
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Profiling Session: %s\n", resp.Msg.SessionId))
	sb.WriteString(fmt.Sprintf("Status: %s\n\n", resp.Msg.Status))

	if resp.Msg.Summary != nil {
		sb.WriteString("Summary:\n")
		sb.WriteString(fmt.Sprintf("  Functions Selected: %d\n", resp.Msg.Summary.FunctionsSelected))
		sb.WriteString(fmt.Sprintf("  Functions Probed:   %d\n", resp.Msg.Summary.FunctionsProbed))
		if resp.Msg.Summary.ProbesFailed > 0 {
			sb.WriteString(fmt.Sprintf("  Probes Failed:      %d\n", resp.Msg.Summary.ProbesFailed))
		}
		if resp.Msg.Summary.Duration != nil {
			sb.WriteString(fmt.Sprintf("  Duration:           %s\n", resp.Msg.Summary.Duration.AsDuration().String()))
		}
		sb.WriteString("\n")
	}

	if len(resp.Msg.Bottlenecks) > 0 {
		sb.WriteString("🔥 Bottlenecks Identified:\n\n")
		for i, b := range resp.Msg.Bottlenecks {
			sb.WriteString(fmt.Sprintf("%d. %s [%s]\n", i+1, b.Function, b.Severity))
			sb.WriteString(fmt.Sprintf("   P95: %s (%d%% contribution)\n",
				b.P95.AsDuration().String(), b.ContributionPct))
			sb.WriteString(fmt.Sprintf("   Impact: %s\n", b.Impact))
			sb.WriteString(fmt.Sprintf("   → %s\n\n", b.Recommendation))
		}
	}

	if resp.Msg.Recommendation != "" {
		sb.WriteString(fmt.Sprintf("💡 Recommendation: %s\n\n", resp.Msg.Recommendation))
	}

	if len(resp.Msg.NextSteps) > 0 {
		sb.WriteString("Next Steps:\n")
		for _, step := range resp.Msg.NextSteps {
			sb.WriteString(fmt.Sprintf("  • %s\n", step))
		}
	}

	return sb.String(), nil
}

// registerProfileFunctionsTool registers the coral_profile_functions tool (RFD 069).
func (s *Server) registerProfileFunctionsTool() {
	if !s.isToolEnabled("coral_profile_functions") {
		return
	}

	inputSchema, err := generateInputSchema(ProfileFunctionsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_profile_functions")
		return
	}

	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_profile_functions")
		return
	}

	tool := mcp.NewToolWithRawSchema(
		"coral_profile_functions",
		"Intelligent batch profiling with automatic analysis. Discovers functions via semantic search, applies selection strategy, attaches probes to multiple functions simultaneously, waits and collects data, analyzes bottlenecks automatically, and returns actionable recommendations. Reduces 7+ tool calls to 1. Use this for performance investigation (RFD 069).",
		schemaBytes,
	)

	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if s.debugService == nil {
			return mcp.NewToolResultError("debug service not available"), nil
		}

		var input ProfileFunctionsInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_profile_functions", input)

		// Set defaults
		strategy := "critical_path"
		if input.Strategy != nil {
			strategy = *input.Strategy
		}
		maxFunctions := int32(20)
		if input.MaxFunctions != nil {
			maxFunctions = *input.MaxFunctions
		}
		async := false
		if input.Async != nil {
			async = *input.Async
		}
		sampleRate := 1.0
		if input.SampleRate != nil {
			sampleRate = *input.SampleRate
		}

		// Parse duration
		duration := time.Duration(60 * time.Second)
		if input.Duration != nil {
			d, err := time.ParseDuration(*input.Duration)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid duration format: %v", err)), nil
			}
			duration = d
		}

		// Call DebugService.ProfileFunctions
		req := connect.NewRequest(&debugpb.ProfileFunctionsRequest{
			ServiceName:  input.Service,
			Query:        input.Query,
			Strategy:     strategy,
			MaxFunctions: maxFunctions,
			Duration:     durationpb.New(duration),
			Async:        async,
			SampleRate:   sampleRate,
		})

		resp, err := s.debugService.ProfileFunctions(ctx, req)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to profile functions: %v", err)), nil
		}

		// Format response
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Profiling Session: %s\n", resp.Msg.SessionId))
		sb.WriteString(fmt.Sprintf("Status: %s\n\n", resp.Msg.Status))

		if resp.Msg.Summary != nil {
			sb.WriteString("Summary:\n")
			sb.WriteString(fmt.Sprintf("  Functions Selected: %d\n", resp.Msg.Summary.FunctionsSelected))
			sb.WriteString(fmt.Sprintf("  Functions Probed:   %d\n", resp.Msg.Summary.FunctionsProbed))
			if resp.Msg.Summary.ProbesFailed > 0 {
				sb.WriteString(fmt.Sprintf("  Probes Failed:      %d\n", resp.Msg.Summary.ProbesFailed))
			}
			if resp.Msg.Summary.Duration != nil {
				sb.WriteString(fmt.Sprintf("  Duration:           %s\n", resp.Msg.Summary.Duration.AsDuration().String()))
			}
			sb.WriteString("\n")
		}

		if len(resp.Msg.Bottlenecks) > 0 {
			sb.WriteString("🔥 Bottlenecks Identified:\n\n")
			for i, b := range resp.Msg.Bottlenecks {
				sb.WriteString(fmt.Sprintf("%d. %s [%s]\n", i+1, b.Function, b.Severity))
				sb.WriteString(fmt.Sprintf("   P95: %s (%d%% contribution)\n",
					b.P95.AsDuration().String(), b.ContributionPct))
				sb.WriteString(fmt.Sprintf("   Impact: %s\n", b.Impact))
				sb.WriteString(fmt.Sprintf("   → %s\n\n", b.Recommendation))
			}
		}

		if resp.Msg.Recommendation != "" {
			sb.WriteString(fmt.Sprintf("💡 Recommendation: %s\n\n", resp.Msg.Recommendation))
		}

		if len(resp.Msg.NextSteps) > 0 {
			sb.WriteString("Next Steps:\n")
			for _, step := range resp.Msg.NextSteps {
				sb.WriteString(fmt.Sprintf("  • %s\n", step))
			}
		}

		return mcp.NewToolResultText(sb.String()), nil
	})
}
