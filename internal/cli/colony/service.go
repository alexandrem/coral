package colony

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	colonyv1 "github.com/coral-io/coral/coral/colony/v1"
	"github.com/coral-io/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-io/coral/internal/config"
	"github.com/coral-io/coral/internal/constants"
)

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage and inspect services",
		Long:  `Manage and inspect services running across the colony.`,
	}

	cmd.AddCommand(newServiceListCmd())

	return cmd
}

func newServiceListCmd() *cobra.Command {
	var (
		jsonOutput    bool
		colonyID      string
		filterService string
		filterType    string
		verbose       bool
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all services",
		Long: `List all services discovered across all agents in the colony.

This command aggregates services from all connected agents and presents
a service-centric view. Each service shows instance counts and the agents
running that service.

Examples:
  # List all services
  coral service list

  # Filter by service name
  coral service list --service redis

  # Filter by service type
  coral service list --type http

  # JSON output
  coral service list --json

  # Verbose output with per-agent details
  coral service list --service redis -v`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create resolver.
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}

			// Resolve colony ID.
			if colonyID == "" {
				colonyID, err = resolver.ResolveColonyID()
				if err != nil {
					return fmt.Errorf("failed to resolve colony: %w\n\nRun 'coral init <app-name>' to create a colony", err)
				}
			}

			// Load colony configuration.
			loader := resolver.GetLoader()
			colonyConfig, err := loader.LoadColonyConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			// Get connect port.
			connectPort := colonyConfig.Services.ConnectPort
			if connectPort == 0 {
				connectPort = 9000
			}

			// Create RPC client - try localhost first, then mesh IP.
			baseURL := fmt.Sprintf("http://localhost:%d", connectPort)
			client := colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

			// Call ListAgents RPC to get all agents and their services.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			snapshotTime := time.Now().UTC()
			req := connect.NewRequest(&colonyv1.ListAgentsRequest{})
			resp, err := client.ListAgents(ctx, req)
			if err != nil {
				// Try mesh IP as fallback.
				meshIP := colonyConfig.WireGuard.MeshIPv4
				if meshIP == "" {
					meshIP = constants.DefaultColonyMeshIPv4
				}
				baseURL = fmt.Sprintf("http://%s:%d", meshIP, connectPort)
				client = colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

				ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel2()

				snapshotTime = time.Now().UTC()
				resp, err = client.ListAgents(ctx2, connect.NewRequest(&colonyv1.ListAgentsRequest{}))
				if err != nil {
					return fmt.Errorf("failed to list agents (is colony running?): %w", err)
				}
			}

			agents := resp.Msg.Agents
			services := aggregateServicesWithStatus(agents)

			// Get all service names for error messages.
			allServiceNames := make([]string, 0, len(services))
			for _, svc := range services {
				allServiceNames = append(allServiceNames, svc.Name)
			}

			// Filter by service name if requested.
			if filterService != "" {
				filtered := make([]serviceView, 0)
				for _, svc := range services {
					if strings.EqualFold(svc.Name, filterService) {
						filtered = append(filtered, svc)
					}
				}
				if len(filtered) == 0 {
					return serviceNotFoundError(filterService, allServiceNames)
				}
				services = filtered
			}

			// Filter by type if requested.
			if filterType != "" {
				filtered := make([]serviceView, 0)
				for _, svc := range services {
					if strings.EqualFold(svc.Type, filterType) {
						filtered = append(filtered, svc)
					}
				}
				services = filtered
			}

			if jsonOutput {
				return outputServicesJSONv2(services, snapshotTime)
			}

			if verbose && filterService != "" {
				return outputServicesVerbose(services, snapshotTime)
			}

			return outputServicesTablev2(services, snapshotTime)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	cmd.Flags().StringVar(&colonyID, "colony", "", "Colony ID (overrides auto-detection)")
	cmd.Flags().StringVar(&filterService, "service", "", "Filter by service name (case-insensitive)")
	cmd.Flags().StringVar(&filterType, "type", "", "Filter by service type (e.g., http, redis)")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show detailed per-agent information")

	return cmd
}

// agentInstance represents an agent running a service with its status.
type agentInstance struct {
	AgentID        string            `json:"agent_id"`
	MeshIPv4       string            `json:"mesh_ipv4"`
	Status         string            `json:"status"`
	Port           int32             `json:"port"`
	HealthEndpoint string            `json:"health_endpoint,omitempty"`
	LastSeen       time.Time         `json:"last_seen"`
	Labels         map[string]string `json:"labels,omitempty"`
}

// serviceView represents an aggregated service with all its instances.
type serviceView struct {
	Name          string          `json:"service_name"`
	Type          string          `json:"service_type,omitempty"`
	InstanceCount int             `json:"instance_count"`
	Agents        []agentInstance `json:"agents"`
}

// serviceListResponse is the JSON output format per RFD 052.
type serviceListResponse struct {
	Version        string        `json:"version"`
	SnapshotTime   string        `json:"snapshot_time"`
	TotalServices  int           `json:"total_services"`
	TotalInstances int           `json:"total_instances"`
	Services       []serviceView `json:"services"`
}

// aggregateServicesWithStatus aggregates services from agents with status information.
func aggregateServicesWithStatus(agents []*colonyv1.Agent) []serviceView {
	serviceMap := make(map[string]*serviceView)

	for _, agent := range agents {
		// Determine agent status from the agent's status field or last_seen.
		agentStatus := agent.Status
		if agentStatus == "" {
			agentStatus = "unknown"
		}

		var lastSeen time.Time
		if agent.LastSeen != nil {
			lastSeen = agent.LastSeen.AsTime()
		}

		// Handle multi-service agents.
		for _, svcInfo := range agent.Services {
			// Use lowercase name for grouping (case-insensitive).
			key := strings.ToLower(svcInfo.Name)

			if _, exists := serviceMap[key]; !exists {
				serviceMap[key] = &serviceView{
					Name:          svcInfo.Name, // Preserve original casing
					Type:          svcInfo.ServiceType,
					InstanceCount: 0,
					Agents:        make([]agentInstance, 0),
				}
			}

			entry := serviceMap[key]
			entry.InstanceCount++
			entry.Agents = append(entry.Agents, agentInstance{
				AgentID:        agent.AgentId,
				MeshIPv4:       agent.MeshIpv4,
				Status:         agentStatus,
				Port:           svcInfo.Port,
				HealthEndpoint: svcInfo.HealthEndpoint,
				LastSeen:       lastSeen,
				Labels:         svcInfo.Labels,
			})
		}

		// Handle legacy single-service agents (fallback).
		if len(agent.Services) == 0 && agent.ComponentName != "" {
			key := strings.ToLower(agent.ComponentName)
			if _, exists := serviceMap[key]; !exists {
				serviceMap[key] = &serviceView{
					Name:          agent.ComponentName,
					Type:          "unknown",
					InstanceCount: 0,
					Agents:        make([]agentInstance, 0),
				}
			}
			entry := serviceMap[key]
			entry.InstanceCount++
			entry.Agents = append(entry.Agents, agentInstance{
				AgentID:  agent.AgentId,
				MeshIPv4: agent.MeshIpv4,
				Status:   agentStatus,
				Port:     0,
				LastSeen: lastSeen,
			})
		}
	}

	// Convert map to slice.
	result := make([]serviceView, 0, len(serviceMap))
	for _, svc := range serviceMap {
		// Sort agents by ID within each service.
		sort.Slice(svc.Agents, func(i, j int) bool {
			return svc.Agents[i].AgentID < svc.Agents[j].AgentID
		})
		result = append(result, *svc)
	}

	// Sort by service name.
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result
}

// statusIndicator returns a status indicator string with optional color.
func statusIndicator(status string) string {
	switch strings.ToLower(status) {
	case "healthy":
		return "✓ healthy"
	case "degraded":
		return "⚠ degraded"
	case "unhealthy":
		return "✗ unhealthy"
	default:
		return "? " + status
	}
}

// serviceNotFoundError returns a formatted error when a service is not found.
func serviceNotFoundError(serviceName string, availableServices []string) error {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Error: Service '%s' not found in colony\n", serviceName))

	if len(availableServices) > 0 {
		sb.WriteString(fmt.Sprintf("\nAvailable services (%d):\n", len(availableServices)))
		sort.Strings(availableServices)
		for _, name := range availableServices {
			sb.WriteString(fmt.Sprintf("  • %s\n", name))
		}
		sb.WriteString("\nUse 'coral service list' to see all services with their agents.")
	} else {
		sb.WriteString("\nNo services found in colony. Use 'coral connect <service>' to register agents.")
	}

	return fmt.Errorf("%s", sb.String())
}

// outputServicesJSONv2 outputs services in the RFD 052 JSON format.
func outputServicesJSONv2(services []serviceView, snapshotTime time.Time) error {
	totalInstances := 0
	for _, svc := range services {
		totalInstances += svc.InstanceCount
	}

	response := serviceListResponse{
		Version:        "1.0",
		SnapshotTime:   snapshotTime.Format(time.RFC3339),
		TotalServices:  len(services),
		TotalInstances: totalInstances,
		Services:       services,
	}

	data, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

// outputServicesTablev2 outputs services in table format with snapshot time.
func outputServicesTablev2(services []serviceView, snapshotTime time.Time) error {
	if len(services) == 0 {
		fmt.Println("No services found.")
		return nil
	}

	// Calculate total instances.
	totalInstances := 0
	for _, svc := range services {
		totalInstances += svc.InstanceCount
	}

	// Print header with snapshot time.
	fmt.Printf("Services (%d) at %s:\n\n", len(services), snapshotTime.Format("2006-01-02 15:04:05 UTC"))

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "SERVICE\tTYPE\tINSTANCES\tAGENTS")

	for _, svc := range services {
		// Build agent list with status indicators.
		agentParts := make([]string, 0, len(svc.Agents))
		for _, agent := range svc.Agents {
			agentParts = append(agentParts, fmt.Sprintf("%s (%s, %s)",
				agent.AgentID, agent.MeshIPv4, statusIndicator(agent.Status)))
		}

		agentsStr := strings.Join(agentParts, ", ")
		if len(svc.Agents) > 2 {
			// Truncate for readability.
			truncated := make([]string, 0, 2)
			for i := 0; i < 2 && i < len(svc.Agents); i++ {
				agent := svc.Agents[i]
				truncated = append(truncated, fmt.Sprintf("%s (%s, %s)",
					agent.AgentID, agent.MeshIPv4, statusIndicator(agent.Status)))
			}
			agentsStr = fmt.Sprintf("%s, ... (%d total)", strings.Join(truncated, ", "), len(svc.Agents))
		}

		typeStr := svc.Type
		if typeStr == "" {
			typeStr = "-"
		}

		fmt.Fprintf(w, "%s\t%s\t%d\t%s\n",
			svc.Name,
			typeStr,
			svc.InstanceCount,
			agentsStr,
		)
	}

	w.Flush()
	return nil
}

// outputServicesVerbose outputs detailed service information (for --verbose with --service).
func outputServicesVerbose(services []serviceView, snapshotTime time.Time) error {
	if len(services) == 0 {
		fmt.Println("No services found.")
		return nil
	}

	for _, svc := range services {
		fmt.Printf("Service: %s at %s:\n", svc.Name, snapshotTime.Format("2006-01-02 15:04:05 UTC"))

		typeStr := svc.Type
		if typeStr == "" {
			typeStr = "unknown"
		}
		fmt.Printf("  Type: %s\n", typeStr)
		fmt.Printf("  Instances: %d\n", svc.InstanceCount)
		fmt.Println()

		for _, agent := range svc.Agents {
			fmt.Printf("  Agent: %s\n", agent.AgentID)
			fmt.Printf("    Mesh IP: %s\n", agent.MeshIPv4)
			fmt.Printf("    Status: %s\n", statusIndicator(agent.Status))
			if agent.Port > 0 {
				fmt.Printf("    Port: %d\n", agent.Port)
			}
			if agent.HealthEndpoint != "" {
				fmt.Printf("    Health Endpoint: %s\n", agent.HealthEndpoint)
			}
			if !agent.LastSeen.IsZero() {
				ago := time.Since(agent.LastSeen).Round(time.Second)
				fmt.Printf("    Last Seen: %s ago\n", ago)
			}
			fmt.Println()
		}
	}

	return nil
}
