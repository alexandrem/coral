package agent

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/constants"
)

const colonyProbeTimeout = 5 * time.Second

// normalizeAgentAddress strips any http:// or https:// scheme prefix so the
// address can be used with an explicit scheme in client URLs.
func normalizeAgentAddress(addr string) string {
	switch {
	case strings.HasPrefix(addr, "http://"):
		return addr[len("http://"):]
	case strings.HasPrefix(addr, "https://"):
		return addr[len("https://"):]
	default:
		return addr
	}
}

// listAgentsFromColony loads colony config and calls ListAgents with localhost→mesh fallback.
func listAgentsFromColony(ctx context.Context, colonyID string) (*colonyv1.ListAgentsResponse, error) {
	resolver, err := config.NewResolver()
	if err != nil {
		return nil, fmt.Errorf("failed to create config resolver: %w", err)
	}

	if colonyID == "" {
		colonyID, err = resolver.ResolveColonyID()
		if err != nil {
			return nil, fmt.Errorf("failed to resolve colony: %w\n\nRun 'coral init <app-name>' to create a colony", err)
		}
	}

	loader := resolver.GetLoader()
	colonyConfig, err := loader.LoadColonyConfig(colonyID)
	if err != nil {
		return nil, fmt.Errorf("failed to load colony config: %w", err)
	}

	connectPort := colonyConfig.Services.ConnectPort
	if connectPort == 0 {
		connectPort = constants.DefaultColonyPort
	}

	// Try localhost first.
	baseURL := fmt.Sprintf("http://localhost:%d", connectPort)
	client := colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

	ctxTry, cancel := context.WithTimeout(ctx, colonyProbeTimeout)
	resp, err := client.ListAgents(ctxTry, connect.NewRequest(&colonyv1.ListAgentsRequest{}))
	cancel()

	if err != nil {
		// Fallback to mesh IP.
		meshIP := colonyConfig.WireGuard.MeshIPv4
		if meshIP == "" {
			meshIP = "10.42.0.1"
		}
		baseURL = fmt.Sprintf("http://%s:%d", meshIP, connectPort)
		client = colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

		ctxFallback, cancelFallback := context.WithTimeout(ctx, colonyProbeTimeout)
		resp, err = client.ListAgents(ctxFallback, connect.NewRequest(&colonyv1.ListAgentsRequest{}))
		cancelFallback()

		if err != nil {
			return nil, fmt.Errorf("failed to query colony (is colony running?): %w", err)
		}
	}

	return resp.Msg, nil
}

// resolveAgentID resolves an agent ID to mesh IP:port via colony registry (RFD 044).
// This enables targeting agents by ID instead of requiring manual mesh IP lookup.
func resolveAgentID(ctx context.Context, agentID, colonyID string) (string, error) {
	agents, err := listAgentsFromColony(ctx, colonyID)
	if err != nil {
		return "", err
	}

	for _, agent := range agents.Agents {
		if agent.AgentId == agentID {
			// Return mesh IP with agent port (default: 9001).
			// Note: This assumes agents listen on 9001, which is the default agent port.
			return fmt.Sprintf("%s:9001", agent.MeshIpv4), nil
		}
	}

	return "", fmt.Errorf("agent not found: %s\n\nAvailable agents:\n%s", agentID, formatAvailableAgents(agents.Agents))
}

// resolveServiceToAgent resolves a service name to agent mesh IP:port via colony registry.
// This enables targeting services by name instead of requiring manual agent ID lookup.
func resolveServiceToAgent(ctx context.Context, serviceName, colonyID string) (string, error) {
	agents, err := listAgentsFromColony(ctx, colonyID)
	if err != nil {
		return "", err
	}

	// Find agent with matching service name.
	// This will fail until the issue in ./issues/resolve-agent-fallback-to-name-field.md is fixed.
	for _, agent := range agents.Agents {
		for _, svc := range agent.Services {
			if svc.Name == serviceName {
				// Return mesh IP with agent port (default: 9001).
				return fmt.Sprintf("%s:9001", agent.MeshIpv4), nil
			}
		}
		// Fallback: Check deprecated ComponentName field for backward compatibility.
		if agent.ComponentName == serviceName {
			return fmt.Sprintf("%s:9001", agent.MeshIpv4), nil
		}
	}

	return "", fmt.Errorf("service not found: %s\n\nAvailable services:\n%s", serviceName, formatAvailableServices(agents.Agents))
}

// formatAvailableAgents formats the list of available agents for error messages.
func formatAvailableAgents(agents []*colonyv1.Agent) string {
	if len(agents) == 0 {
		return "  (no agents connected)"
	}

	var result strings.Builder
	for _, agent := range agents {
		result.WriteString(fmt.Sprintf("  - %s (mesh IP: %s)\n", agent.AgentId, agent.MeshIpv4))
	}
	return result.String()
}

// formatAvailableServices formats the list of available services for error messages.
func formatAvailableServices(agents []*colonyv1.Agent) string {
	if len(agents) == 0 {
		return "  (no services connected)"
	}

	var result strings.Builder
	seen := make(map[string]bool)
	for _, agent := range agents {
		for _, svc := range agent.Services {
			if !seen[svc.Name] {
				result.WriteString(fmt.Sprintf("  - %s (agent: %s, mesh IP: %s)\n", svc.Name, agent.AgentId, agent.MeshIpv4))
				seen[svc.Name] = true
			}
		}
		// Include deprecated ComponentName field.
		if agent.ComponentName != "" && !seen[agent.ComponentName] {
			result.WriteString(fmt.Sprintf("  - %s (agent: %s, mesh IP: %s)\n", agent.ComponentName, agent.AgentId, agent.MeshIpv4))
			seen[agent.ComponentName] = true
		}
	}
	if result.Len() == 0 {
		return "  (no services found)"
	}
	return result.String()
}
