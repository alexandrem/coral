package server

import (
	"context"
	"fmt"

	"github.com/coral-io/coral/internal/colony/mcp"
)

// SetMCPServer sets the MCP server instance for tool execution.
func (s *Server) SetMCPServer(mcpServer *mcp.Server) {
	s.mcpServer = mcpServer
	s.logger.Info().Msg("MCP server attached to colony server")
}

// ExecuteTool executes an MCP tool by name with JSON-encoded arguments.
// Returns the formatted result string and an error if the tool fails.
func (s *Server) ExecuteTool(ctx context.Context, toolName string, argumentsJSON string) (string, error) {
	if s.mcpServer == nil {
		return "", fmt.Errorf("MCP server not initialized")
	}

	// Type assert to get the actual MCP server.
	mcpServer, ok := s.mcpServer.(*mcp.Server)
	if !ok {
		return "", fmt.Errorf("invalid MCP server type")
	}

	s.logger.Debug().
		Str("tool", toolName).
		Str("args", argumentsJSON).
		Msg("Executing MCP tool via RPC")

	// Execute the tool through the MCP server.
	result, err := mcpServer.ExecuteTool(ctx, toolName, argumentsJSON)
	if err != nil {
		s.logger.Error().
			Err(err).
			Str("tool", toolName).
			Msg("MCP tool execution failed")
		return "", fmt.Errorf("tool execution failed: %w", err)
	}

	return result, nil
}

// ListToolNames returns the list of available MCP tool names.
func (s *Server) ListToolNames() []string {
	if s.mcpServer == nil {
		return []string{}
	}

	mcpServer, ok := s.mcpServer.(*mcp.Server)
	if !ok {
		return []string{}
	}

	return mcpServer.ListToolNames()
}

// IsToolEnabled checks if a specific tool is enabled based on configuration.
func (s *Server) IsToolEnabled(toolName string) bool {
	if s.mcpServer == nil {
		return false
	}

	mcpServer, ok := s.mcpServer.(*mcp.Server)
	if !ok {
		return false
	}

	return mcpServer.IsToolEnabled(toolName)
}
