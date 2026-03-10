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

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
)

// parseStrategyKind converts a strategy name string to a StrategyKind enum.
func parseStrategyKind(s string) (agentv1.StrategyKind, error) {
	switch strings.ToLower(s) {
	case "rate_gate":
		return agentv1.StrategyKind_RATE_GATE, nil
	case "edge_trigger":
		return agentv1.StrategyKind_EDGE_TRIGGER, nil
	case "causal_pair":
		return agentv1.StrategyKind_CAUSAL_PAIR, nil
	case "absence":
		return agentv1.StrategyKind_ABSENCE, nil
	case "percentile_alarm":
		return agentv1.StrategyKind_PERCENTILE_ALARM, nil
	case "sequence":
		return agentv1.StrategyKind_SEQUENCE, nil
	default:
		return 0, fmt.Errorf("unknown strategy %q: must be one of rate_gate, edge_trigger, causal_pair, absence, percentile_alarm, sequence", s)
	}
}

// parseActionKind converts an action name string to an ActionKind enum.
func parseActionKind(s string) (agentv1.ActionKind, error) {
	switch strings.ToLower(s) {
	case "emit_event":
		return agentv1.ActionKind_EMIT_EVENT, nil
	case "goroutine_snapshot":
		return agentv1.ActionKind_GOROUTINE_SNAPSHOT, nil
	case "cpu_profile":
		return agentv1.ActionKind_CPU_PROFILE, nil
	default:
		return 0, fmt.Errorf("unknown action %q: must be one of emit_event, goroutine_snapshot, cpu_profile", s)
	}
}

// buildSourceSpec constructs a SourceSpec from probe and filter strings.
func buildSourceSpec(probe, filterExpr *string) *agentv1.SourceSpec {
	if probe == nil || *probe == "" {
		return nil
	}
	spec := &agentv1.SourceSpec{Probe: *probe}
	if filterExpr != nil {
		spec.FilterExpr = *filterExpr
	}
	return spec
}

// buildCorrelationDescriptor constructs a CorrelationDescriptor from the MCP input.
func buildCorrelationDescriptor(input DeployCorrelationInput) (*agentv1.CorrelationDescriptor, error) {
	strategy, err := parseStrategyKind(input.Strategy)
	if err != nil {
		return nil, err
	}

	action, err := parseActionKind(input.Action)
	if err != nil {
		return nil, err
	}

	desc := &agentv1.CorrelationDescriptor{
		Strategy: strategy,
		Action: &agentv1.ActionSpec{
			Kind: action,
		},
	}

	if input.ProfileDurationMs != nil {
		desc.Action.ProfileDurationMs = *input.ProfileDurationMs
	}

	if input.CooldownMs != nil {
		desc.CooldownMs = *input.CooldownMs
	}

	if input.Window != nil && *input.Window != "" {
		d, err := time.ParseDuration(*input.Window)
		if err != nil {
			return nil, fmt.Errorf("invalid window duration %q: %w", *input.Window, err)
		}
		desc.Window = durationpb.New(d)
	}

	if input.Threshold != nil {
		desc.Threshold = *input.Threshold
	}

	if input.Field != nil {
		desc.Field = *input.Field
	}

	if input.Percentile != nil {
		desc.Percentile = *input.Percentile
	}

	if input.JoinOn != nil {
		desc.JoinOn = *input.JoinOn
	}

	// Primary source (most strategies).
	desc.Source = buildSourceSpec(input.Probe, input.FilterExpr)

	// Dual-source strategies (causal_pair, sequence).
	desc.SourceA = buildSourceSpec(input.ProbeA, input.FilterExprA)
	desc.SourceB = buildSourceSpec(input.ProbeB, input.FilterExprB)

	return desc, nil
}

// registerDeployCorrelationTool registers the coral_deploy_correlation tool (RFD 091).
func (s *Server) registerDeployCorrelationTool() {
	if !s.isToolEnabled("coral_deploy_correlation") {
		return
	}

	inputSchema, err := generateInputSchema(DeployCorrelationInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_deploy_correlation")
		return
	}

	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_deploy_correlation")
		return
	}

	tool := mcp.NewToolWithRawSchema(
		"coral_deploy_correlation",
		"Deploy a stateful correlation descriptor to an agent. The agent evaluates the strategy against the live event stream and fires an action (emit_event, goroutine_snapshot, cpu_profile) when the condition is met. Strategies: rate_gate (N events in window), edge_trigger (first match after cooldown), causal_pair (A followed by B sharing a field), absence (no event in window), percentile_alarm (Pxx exceeds threshold), sequence (A then B in order).",
		schemaBytes,
	)

	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if s.debugService == nil {
			return mcp.NewToolResultError("debug service not available"), nil
		}

		var input DeployCorrelationInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_deploy_correlation", input)

		desc, err := buildCorrelationDescriptor(input)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		req := connect.NewRequest(&debugpb.ColonyDeployCorrelationRequest{
			ServiceName: input.Service,
			Descriptor_: desc,
		})

		resp, err := s.debugService.DeployCorrelation(ctx, req)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to deploy correlation: %v", err)), nil
		}

		if !resp.Msg.Success {
			return mcp.NewToolResultError(fmt.Sprintf("failed to deploy correlation: %s", resp.Msg.Error)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf(
			"Correlation deployed.\nID:       %s\nAgent ID: %s\nService:  %s\nStrategy: %s\nAction:   %s",
			resp.Msg.CorrelationId, resp.Msg.AgentId, input.Service, input.Strategy, input.Action,
		)), nil
	})
}

// registerRemoveCorrelationTool registers the coral_remove_correlation tool (RFD 091).
func (s *Server) registerRemoveCorrelationTool() {
	if !s.isToolEnabled("coral_remove_correlation") {
		return
	}

	inputSchema, err := generateInputSchema(RemoveCorrelationInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_remove_correlation")
		return
	}

	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_remove_correlation")
		return
	}

	tool := mcp.NewToolWithRawSchema(
		"coral_remove_correlation",
		"Remove an active correlation descriptor from the agent. Use this when the investigation condition is no longer needed or when the descriptor was deployed in error.",
		schemaBytes,
	)

	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if s.debugService == nil {
			return mcp.NewToolResultError("debug service not available"), nil
		}

		var input RemoveCorrelationInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_remove_correlation", input)

		serviceName := ""
		if input.Service != nil {
			serviceName = *input.Service
		}

		req := connect.NewRequest(&debugpb.ColonyRemoveCorrelationRequest{
			CorrelationId: input.CorrelationID,
			ServiceName:   serviceName,
		})

		resp, err := s.debugService.RemoveCorrelation(ctx, req)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to remove correlation: %v", err)), nil
		}

		if !resp.Msg.Success {
			return mcp.NewToolResultError(fmt.Sprintf("failed to remove correlation: %s", resp.Msg.Error)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Correlation %s removed successfully.", input.CorrelationID)), nil
	})
}

// registerListCorrelationsTool registers the coral_list_correlations tool (RFD 091).
func (s *Server) registerListCorrelationsTool() {
	if !s.isToolEnabled("coral_list_correlations") {
		return
	}

	inputSchema, err := generateInputSchema(ListCorrelationsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_list_correlations")
		return
	}

	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_list_correlations")
		return
	}

	tool := mcp.NewToolWithRawSchema(
		"coral_list_correlations",
		"List all active correlation descriptors across the agent mesh. Use this to review what pattern-detection rules are currently deployed before adding new ones or after an investigation.",
		schemaBytes,
	)

	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if s.debugService == nil {
			return mcp.NewToolResultError("debug service not available"), nil
		}

		var input ListCorrelationsInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_list_correlations", input)

		serviceName := ""
		if input.Service != nil {
			serviceName = *input.Service
		}

		req := connect.NewRequest(&debugpb.ColonyListCorrelationsRequest{
			ServiceName: serviceName,
		})

		resp, err := s.debugService.ListCorrelations(ctx, req)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list correlations: %v", err)), nil
		}

		if len(resp.Msg.Descriptors) == 0 {
			return mcp.NewToolResultText("No active correlations found."), nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Found %d active correlation(s):\n\n", len(resp.Msg.Descriptors)))
		for _, d := range resp.Msg.Descriptors {
			sb.WriteString(fmt.Sprintf("ID:       %s\n", d.Id))
			sb.WriteString(fmt.Sprintf("Strategy: %s\n", d.Strategy.String()))
			sb.WriteString(fmt.Sprintf("Action:   %s\n", d.Action.GetKind().String()))
			if d.Source != nil && d.Source.Probe != "" {
				sb.WriteString(fmt.Sprintf("Probe:    %s\n", d.Source.Probe))
				if d.Source.FilterExpr != "" {
					sb.WriteString(fmt.Sprintf("Filter:   %s\n", d.Source.FilterExpr))
				}
			}
			if d.SourceA != nil && d.SourceA.Probe != "" {
				sb.WriteString(fmt.Sprintf("ProbeA:   %s\n", d.SourceA.Probe))
			}
			if d.SourceB != nil && d.SourceB.Probe != "" {
				sb.WriteString(fmt.Sprintf("ProbeB:   %s\n", d.SourceB.Probe))
			}
			if d.Window != nil {
				sb.WriteString(fmt.Sprintf("Window:   %s\n", d.Window.AsDuration()))
			}
			if d.Threshold != 0 {
				sb.WriteString(fmt.Sprintf("Threshold: %g\n", d.Threshold))
			}
			sb.WriteString("\n")
		}

		return mcp.NewToolResultText(sb.String()), nil
	})
}
