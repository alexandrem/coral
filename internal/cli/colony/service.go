//nolint:errcheck
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

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/cli/helpers"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/constants"
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
		format        string
		colonyID      string
		filterService string
		filterType    string
		filterSource  string
		verbose       bool
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all services",
		Long: `List all services discovered across agents in the colony.

This command shows the operational view of services from both the registry
(explicitly connected) and telemetry data (auto-observed). It displays which
agents are running each service, their health status, and instance counts.

Source Types:
  REGISTERED - Explicitly connected via coral connect
  OBSERVED   - Auto-observed from telemetry data only
  VERIFIED   - Registered AND has telemetry (ideal state)

Examples:
  # List all services (registry + telemetry)
  coral service list

  # Filter by service name
  coral service list --service redis

  # Filter by service type
  coral service list --type http

  # Filter by source
  coral service list --source verified     # Only verified services
  coral service list --source observed     # Only telemetry-observed

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

			// Use ListServices for dual-source discovery.
			listServicesReq := connect.NewRequest(&colonyv1.ListServicesRequest{
				TimeRange: "1h", // Default 1 hour lookback for telemetry
			})
			listServicesResp, err := client.ListServices(ctx, listServicesReq)
			if err != nil {
				return fmt.Errorf("failed to list services: %w", err)
			}

			// Get agent details for all services.
			agents := resp.Msg.Agents
			services := convertServicesToView(listServicesResp.Msg.Services, agents)

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

			// Filter by source if requested.
			if filterSource != "" {
				sourceUpper := strings.ToUpper(filterSource)
				if sourceUpper != "REGISTERED" && sourceUpper != "OBSERVED" && sourceUpper != "VERIFIED" {
					return fmt.Errorf("invalid source filter: %s (must be 'registered', 'observed', or 'verified')", filterSource)
				}
				filtered := make([]serviceView, 0)
				for _, svc := range services {
					if strings.EqualFold(svc.Source, sourceUpper) {
						filtered = append(filtered, svc)
					}
				}
				services = filtered
			}

			if format != string(helpers.FormatTable) {
				return outputServicesJSONv2(services, snapshotTime)
			}

			if verbose && filterService != "" {
				return outputServicesVerbose(services, snapshotTime)
			}

			return outputServicesTablev2(services, snapshotTime)
		},
	}

	helpers.AddFormatFlag(cmd, &format, helpers.FormatTable, []helpers.OutputFormat{
		helpers.FormatTable,
		helpers.FormatJSON,
	})
	helpers.AddColonyFlag(cmd, &colonyID)
	cmd.Flags().StringVar(&filterService, "service", "", "Filter by service name (case-insensitive)")
	cmd.Flags().StringVar(&filterType, "type", "", "Filter by service type (e.g., http, redis)")
	cmd.Flags().StringVar(&filterSource, "source", "", "Filter by source: 'registered', 'observed', or 'verified'")
	helpers.AddVerboseFlag(cmd, &verbose)

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
	Source        string          `json:"source"` // REGISTERED, OBSERVED, or VERIFIED
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

// convertServicesToView converts ListServices response to serviceView format,
// enriching with agent details for all services (registered and observed).
func convertServicesToView(services []*colonyv1.ServiceSummary, agents []*colonyv1.Agent) []serviceView {
	// Build agent maps for quick lookup.
	agentMapByService := make(map[string]map[string]*colonyv1.Agent) // service_name -> agent_id -> agent
	agentMapByID := make(map[string]*colonyv1.Agent)                 // agent_id -> agent

	for _, agent := range agents {
		agentMapByID[agent.AgentId] = agent
		for _, svcInfo := range agent.Services {
			serviceName := strings.ToLower(svcInfo.Name)
			if agentMapByService[serviceName] == nil {
				agentMapByService[serviceName] = make(map[string]*colonyv1.Agent)
			}
			agentMapByService[serviceName][agent.AgentId] = agent
		}
	}

	result := make([]serviceView, 0, len(services))
	for _, svc := range services {
		view := serviceView{
			Name:          svc.Name,
			InstanceCount: int(svc.InstanceCount),
			Source:        formatSourceEnum(svc.Source),
			Agents:        make([]agentInstance, 0),
		}

		serviceName := strings.ToLower(svc.Name)

		// For registered/verified services, get full agent details including port/health.
		if serviceAgents, hasAgents := agentMapByService[serviceName]; hasAgents {
			for _, agent := range serviceAgents {
				var lastSeen time.Time
				if agent.LastSeen != nil {
					lastSeen = agent.LastSeen.AsTime()
				}

				// Find the service info for this agent.
				for _, svcInfo := range agent.Services {
					if strings.EqualFold(svcInfo.Name, svc.Name) {
						view.Agents = append(view.Agents, agentInstance{
							AgentID:        agent.AgentId,
							MeshIPv4:       agent.MeshIpv4,
							Status:         agent.Status,
							Port:           svcInfo.Port,
							HealthEndpoint: svcInfo.HealthEndpoint,
							LastSeen:       lastSeen,
							Labels:         svcInfo.Labels,
						})
						if view.Type == "" && svcInfo.ServiceType != "" {
							view.Type = svcInfo.ServiceType
						}
						break
					}
				}
			}
		} else if svc.AgentId != nil && svc.Source == colonyv1.ServiceSource_SERVICE_SOURCE_OBSERVED {
			// For OBSERVED-only services, show basic agent info without service-specific details.
			if agent, exists := agentMapByID[*svc.AgentId]; exists {
				var lastSeen time.Time
				if agent.LastSeen != nil {
					lastSeen = agent.LastSeen.AsTime()
				}

				view.Agents = append(view.Agents, agentInstance{
					AgentID:  agent.AgentId,
					MeshIPv4: agent.MeshIpv4,
					Status:   agent.Status,
					LastSeen: lastSeen,
				})
			}
		}

		// Sort agents by ID.
		sort.Slice(view.Agents, func(i, j int) bool {
			return view.Agents[i].AgentID < view.Agents[j].AgentID
		})

		result = append(result, view)
	}

	// Sort by service name.
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result
}

// formatSourceEnum converts ServiceSource enum to string.
func formatSourceEnum(source colonyv1.ServiceSource) string {
	switch source {
	case colonyv1.ServiceSource_SERVICE_SOURCE_REGISTERED:
		return "REGISTERED"
	case colonyv1.ServiceSource_SERVICE_SOURCE_OBSERVED:
		return "OBSERVED"
	case colonyv1.ServiceSource_SERVICE_SOURCE_VERIFIED:
		return "VERIFIED"
	default:
		return "UNKNOWN"
	}
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
	fmt.Fprintln(w, "SERVICE\tTYPE\tINSTANCES\tSOURCE\tAGENTS")

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

		sourceStr := svc.Source
		if sourceStr == "" {
			sourceStr = "REGISTERED" // Default if not enriched
		}

		// TODO: errcheck
		_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n",
			svc.Name,
			typeStr,
			svc.InstanceCount,
			sourceStr,
			agentsStr,
		)
	}

	_ = w.Flush() // TODO: errcheck
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

		sourceStr := svc.Source
		if sourceStr == "" {
			sourceStr = "REGISTERED"
		}
		fmt.Printf("  Source: %s\n", sourceStr)
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
