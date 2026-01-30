package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/mark3labs/mcp-go/mcp"
	"google.golang.org/protobuf/types/known/durationpb"

	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
)

// executeAttachUprobeTool executes the coral_attach_uprobe tool.
func (s *Server) executeAttachUprobeTool(ctx context.Context, argumentsJSON string) (string, error) {
	if s.debugService == nil {
		return "", fmt.Errorf("debug service not available")
	}

	var input AttachUprobeInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_attach_uprobe", input)

	// Parse duration
	duration := 60 * time.Second
	if input.Duration != nil {
		d, err := time.ParseDuration(*input.Duration)
		if err != nil {
			return "", fmt.Errorf("invalid duration format: %w", err)
		}
		duration = d
	}

	// Handle optional fields
	var agentID, sdkAddr string
	if input.AgentID != nil {
		agentID = *input.AgentID
	}
	if input.SDKAddr != nil {
		sdkAddr = *input.SDKAddr
	}

	// Call DebugService.AttachUprobe
	req := connect.NewRequest(&debugpb.AttachUprobeRequest{
		ServiceName:  input.Service,
		FunctionName: input.Function,
		AgentId:      agentID,
		SdkAddr:      sdkAddr,
		Duration:     durationpb.New(duration),
	})

	resp, err := s.debugService.AttachUprobe(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to attach uprobe: %w", err)
	}

	if !resp.Msg.Success {
		return "", fmt.Errorf("failed to attach uprobe: %s", resp.Msg.Error)
	}

	return fmt.Sprintf("Debug session started for %s/%s\nSession ID: %s\nExpires At: %s",
		input.Service, input.Function, resp.Msg.SessionId, resp.Msg.ExpiresAt.AsTime().Format(time.RFC3339)), nil
}

// executeDetachUprobeTool executes the coral_detach_uprobe tool.
func (s *Server) executeDetachUprobeTool(ctx context.Context, argumentsJSON string) (string, error) {
	if s.debugService == nil {
		return "", fmt.Errorf("debug service not available")
	}

	var input DetachUprobeInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_detach_uprobe", input)

	// Call DebugService.DetachUprobe
	req := connect.NewRequest(&debugpb.DetachUprobeRequest{
		SessionId: input.SessionID,
	})

	resp, err := s.debugService.DetachUprobe(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to detach uprobe: %w", err)
	}

	if !resp.Msg.Success {
		return "", fmt.Errorf("failed to detach uprobe: %s", resp.Msg.Error)
	}

	return fmt.Sprintf("Session %s detached successfully.", input.SessionID), nil
}

// registerAttachUprobeTool registers the coral_attach_uprobe tool.
func (s *Server) registerAttachUprobeTool() {
	if !s.isToolEnabled("coral_attach_uprobe") {
		return
	}

	inputSchema, err := generateInputSchema(AttachUprobeInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_attach_uprobe")
		return
	}

	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_attach_uprobe")
		return
	}

	tool := mcp.NewToolWithRawSchema(
		"coral_attach_uprobe",
		"Attach eBPF uprobe to application function for live debugging. Captures entry/exit events, measures duration. Time-limited and production-safe.",
		schemaBytes,
	)

	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if s.debugService == nil {
			return mcp.NewToolResultError("debug service not available"), nil
		}

		var input AttachUprobeInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_attach_uprobe", input)

		// Parse duration
		duration := 60 * time.Second
		if input.Duration != nil {
			d, err := time.ParseDuration(*input.Duration)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid duration format: %v", err)), nil
			}
			duration = d
		}

		// Handle optional fields
		var agentID, sdkAddr string
		if input.AgentID != nil {
			agentID = *input.AgentID
		}
		if input.SDKAddr != nil {
			sdkAddr = *input.SDKAddr
		}

		// Call DebugService.AttachUprobe
		req := connect.NewRequest(&debugpb.AttachUprobeRequest{
			ServiceName:  input.Service,
			FunctionName: input.Function,
			AgentId:      agentID,
			SdkAddr:      sdkAddr,
			Duration:     durationpb.New(duration),
		})

		resp, err := s.debugService.AttachUprobe(ctx, req)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to attach uprobe: %v", err)), nil
		}

		if !resp.Msg.Success {
			return mcp.NewToolResultError(fmt.Sprintf("failed to attach uprobe: %s", resp.Msg.Error)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Debug session started for %s/%s\nSession ID: %s\nExpires At: %s",
			input.Service, input.Function, resp.Msg.SessionId, resp.Msg.ExpiresAt.AsTime().Format(time.RFC3339))), nil
	})
}

// registerDetachUprobeTool registers the coral_detach_uprobe tool.
func (s *Server) registerDetachUprobeTool() {
	if !s.isToolEnabled("coral_detach_uprobe") {
		return
	}

	inputSchema, err := generateInputSchema(DetachUprobeInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_detach_uprobe")
		return
	}

	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_detach_uprobe")
		return
	}

	tool := mcp.NewToolWithRawSchema(
		"coral_detach_uprobe",
		"Stop debug session early and detach eBPF probes. Returns collected data summary.",
		schemaBytes,
	)

	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if s.debugService == nil {
			return mcp.NewToolResultError("debug service not available"), nil
		}

		var input DetachUprobeInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_detach_uprobe", input)

		// Call DebugService.DetachUprobe
		req := connect.NewRequest(&debugpb.DetachUprobeRequest{
			SessionId: input.SessionID,
		})

		resp, err := s.debugService.DetachUprobe(ctx, req)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to detach uprobe: %v", err)), nil
		}

		if !resp.Msg.Success {
			return mcp.NewToolResultError(fmt.Sprintf("failed to detach uprobe: %s", resp.Msg.Error)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Session %s detached successfully.", input.SessionID)), nil
	})
}
