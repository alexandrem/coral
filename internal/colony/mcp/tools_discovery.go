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

// ServiceInfo contains information about a service.
type ServiceInfo struct {
	Name          string            `json:"name"`
	Port          int32             `json:"port,omitempty"`
	ServiceType   string            `json:"service_type,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	Source        string            `json:"source"`                   // REGISTERED, OBSERVED, or VERIFIED.
	Status        string            `json:"status,omitempty"`         // ACTIVE, UNHEALTHY, DISCONNECTED, or OBSERVED_ONLY.
	InstanceCount int32             `json:"instance_count,omitempty"` // Number of instances.
	AgentID       string            `json:"agent_id,omitempty"`       // Agent ID if registered.
}

// registerQueryServicesTool registers the coral_list_services tool (RFD 054, enhanced by RFD 084).
func (s *Server) registerQueryServicesTool() {
	s.registerToolWithSchema(
		"coral_list_services",
		"List all services known to the colony - includes both explicitly registered services and auto-observed services from telemetry data (RFD 084). Returns service names, source attribution (REGISTERED/OBSERVED/VERIFIED), health status, instance counts, and metadata. Useful for discovering available services before querying metrics or traces.",
		ListServicesInput{},
		s.handleListServices,
	)
}

// handleListServices implements the coral_list_services tool handler (RFD 084 enhanced).
// Implements dual-source service discovery (registry + telemetry).
func (s *Server) handleListServices(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	s.auditToolCall("coral_list_services", request.Params.Arguments)

	// Collect services using the dual-source approach from RFD 084.
	servicesMap := make(map[string]ServiceInfo)

	// Source 1: Get services from registry (explicitly registered).
	registryServices := s.registry.ListAll()
	for _, entry := range registryServices {
		for _, svc := range entry.Services {
			if svc == nil {
				continue
			}

			servicesMap[svc.Name] = ServiceInfo{
				Name:          svc.Name,
				Port:          svc.Port,
				ServiceType:   svc.ServiceType,
				Labels:        svc.Labels,
				Source:        "REGISTERED",
				Status:        "ACTIVE", // Services in registry are active
				InstanceCount: 1,        // Will be aggregated later if needed
				AgentID:       entry.AgentID,
			}
		}
	}

	// Source 2: Get services from telemetry data (auto-observed).
	// This includes services that have sent telemetry but may not be registered.
	telemetryServices, err := s.getHistoricalServicesFromDB(ctx)
	if err != nil {
		s.logger.Warn().Err(err).Msg("Failed to fetch telemetry services from DuckDB")
		// Continue with just registry services
	} else {
		for _, serviceName := range telemetryServices {
			if existing, exists := servicesMap[serviceName]; exists {
				// Service is in both registry and telemetry - mark as VERIFIED.
				existing.Source = "VERIFIED"
				servicesMap[serviceName] = existing
			} else {
				// Service is only in telemetry - observed only.
				servicesMap[serviceName] = ServiceInfo{
					Name:   serviceName,
					Source: "OBSERVED",
					Status: "OBSERVED_ONLY",
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
