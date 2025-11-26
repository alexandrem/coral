package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// ListServicesInput is the input for coral_list_services tool.
type ListServicesInput struct {
	// No input parameters needed - returns all services.
}

// ListServicesOutput contains the service list result.
type ListServicesOutput struct {
	Services []ServiceInfo `json:"services"`
}

// ServiceInfo contains basic information about a service.
type ServiceInfo struct {
	Name        string            `json:"name"`
	Port        int32             `json:"port,omitempty"`
	ServiceType string            `json:"service_type,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// registerListServicesTool registers the coral_list_services tool (RFD 054).
func (s *Server) registerListServicesTool() {
	s.registerToolWithSchema(
		"coral_list_services",
		"List all services known to the colony - includes both currently connected services and historical services from observability data. Returns service names, ports, and types. Useful for discovering available services before querying metrics or traces.",
		ListServicesInput{},
		s.handleListServices,
	)
}

// handleListServices implements the coral_list_services tool handler.
func (s *Server) handleListServices(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	s.auditToolCall("coral_list_services", request.Params.Arguments)

	// Collect unique services from two sources:
	// 1. Live agents in registry (currently connected)
	// 2. Historical services in DuckDB (from observability data)
	servicesMap := make(map[string]ServiceInfo)

	// Source 1: Get services from live agents in registry.
	entries := s.registry.ListAll()
	for _, entry := range entries {
		for _, svc := range entry.Services {
			if svc == nil {
				continue
			}

			// Use service name as key to deduplicate across agents.
			if _, exists := servicesMap[svc.Name]; !exists {
				servicesMap[svc.Name] = ServiceInfo{
					Name:        svc.Name,
					Port:        svc.Port,
					ServiceType: svc.ServiceType,
					Labels:      svc.Labels,
				}
			}
		}
	}

	// Source 2: Get historical services from DuckDB observability data.
	// This includes services that have sent telemetry but may not be currently connected.
	historicalServices, err := s.getHistoricalServicesFromDB(ctx)
	if err != nil {
		s.logger.Warn().Err(err).Msg("Failed to fetch historical services from DuckDB")
		// Continue anyway with just registry services
	} else {
		for _, serviceName := range historicalServices {
			if _, exists := servicesMap[serviceName]; !exists {
				// Add historical service (no port/type/labels available from DB)
				servicesMap[serviceName] = ServiceInfo{
					Name: serviceName,
				}
			}
		}
	}

	// Convert map to slice.
	services := make([]ServiceInfo, 0, len(servicesMap))
	for _, svc := range servicesMap {
		services = append(services, svc)
	}

	// Build output.
	output := ListServicesOutput{
		Services: services,
	}

	// Marshal to JSON.
	resultJSON, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(string(resultJSON)),
		},
	}, nil
}

// executeListServicesTool executes the coral_list_services tool (RPC handler).
func (s *Server) executeListServicesTool(ctx context.Context, argumentsJSON string) (string, error) {
	// Parse arguments (empty for this tool).
	var input ListServicesInput
	if argumentsJSON != "" && argumentsJSON != "{}" {
		if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
			return "", fmt.Errorf("failed to parse arguments: %w", err)
		}
	}

	// Create MCP request.
	request := mcp.CallToolRequest{}
	request.Params.Name = "coral_list_services"
	request.Params.Arguments = map[string]interface{}{}

	// Call handler.
	result, err := s.handleListServices(ctx, request)
	if err != nil {
		return "", err
	}

	// Extract text content from result.
	if len(result.Content) > 0 {
		if textContent, ok := mcp.AsTextContent(result.Content[0]); ok {
			return textContent.Text, nil
		}
	}

	return "", fmt.Errorf("no content in result")
}

// getHistoricalServicesFromDB queries DuckDB for all unique service names
// from observability data (metrics, traces, telemetry).
func (s *Server) getHistoricalServicesFromDB(ctx context.Context) ([]string, error) {
	return s.db.QueryAllServiceNames(ctx)
}
