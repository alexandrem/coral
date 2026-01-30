// Package debug provides debug session orchestration for the colony.
package debug

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"

	"github.com/coral-mesh/coral/internal/colony/registry"
)

// realtimeQueryTimeout is for low-latency agent queries.
const realtimeQueryTimeout = 500 * time.Millisecond

// AgentCoordinator handles agent discovery and routing.
type AgentCoordinator struct {
	logger             zerolog.Logger
	registry           *registry.Registry
	agentClientFactory func(connect.HTTPClient, string, ...connect.ClientOption) agentv1connect.AgentServiceClient
}

// NewAgentCoordinator creates a new agent coordinator.
func NewAgentCoordinator(
	logger zerolog.Logger,
	registry *registry.Registry,
	agentClientFactory func(connect.HTTPClient, string, ...connect.ClientOption) agentv1connect.AgentServiceClient,
) *AgentCoordinator {
	return &AgentCoordinator{
		logger:             logger.With().Str("component", "agent_coordinator").Logger(),
		registry:           registry,
		agentClientFactory: agentClientFactory,
	}
}

// FindAgentForService discovers which agent hosts a given service.
// It queries agents in real-time to find the service.
func (ac *AgentCoordinator) FindAgentForService(ctx context.Context, serviceName string) (string, error) {
	ac.logger.Debug().
		Str("service", serviceName).
		Msg("Finding agent for service")

	// Note: registry.FindAgentForService uses cached data which may not have services populated.
	// We need to query agents in real-time to find the service.
	entries := ac.registry.ListAll()

	var foundEntry *registry.Entry

	for _, entry := range entries {
		// Query agent in real-time for services.
		agentURL := fmt.Sprintf("http://%s:9001", entry.MeshIPv4)
		client := ac.agentClientFactory(http.DefaultClient, agentURL)

		queryCtx, cancel := context.WithTimeout(ctx, realtimeQueryTimeout)
		resp, err := client.ListServices(queryCtx, connect.NewRequest(&agentv1.ListServicesRequest{}))
		cancel()

		if err != nil {
			ac.logger.Debug().
				Err(err).
				Str("agent_id", entry.AgentID).
				Msg("Failed to query agent services")
			continue
		}

		// Check if this agent has the service.
		for _, svcStatus := range resp.Msg.Services {
			if svcStatus.Name == serviceName {
				foundEntry = entry
				break
			}
		}

		if foundEntry != nil {
			break
		}
	}

	if foundEntry == nil {
		return "", fmt.Errorf("service not found")
	}

	ac.logger.Debug().
		Str("service", serviceName).
		Str("agent_id", foundEntry.AgentID).
		Msg("Found agent for service")

	return foundEntry.AgentID, nil
}

// GetServicePID queries an agent to get the PID for a given service.
func (ac *AgentCoordinator) GetServicePID(ctx context.Context, agentID, serviceName string) (int32, error) {
	ac.logger.Debug().
		Str("agent_id", agentID).
		Str("service", serviceName).
		Msg("Getting service PID")

	// Get agent entry from registry.
	entry, err := ac.registry.Get(agentID)
	if err != nil {
		return 0, fmt.Errorf("agent not found: %w", err)
	}

	// Query agent for service details to get PID.
	agentURL := fmt.Sprintf("http://%s:9001", entry.MeshIPv4)
	agentClient := ac.agentClientFactory(http.DefaultClient, agentURL)

	servicesResp, err := agentClient.ListServices(ctx, connect.NewRequest(&agentv1.ListServicesRequest{}))
	if err != nil {
		return 0, fmt.Errorf("failed to query agent services: %w", err)
	}

	for _, svc := range servicesResp.Msg.Services {
		if svc.Name == serviceName {
			ac.logger.Debug().
				Str("agent_id", agentID).
				Str("service", serviceName).
				Int32("pid", svc.ProcessId).
				Msg("Found service PID")
			return svc.ProcessId, nil
		}
	}

	return 0, fmt.Errorf("service %s not found on agent %s", serviceName, agentID)
}
