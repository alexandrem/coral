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
		jsonOutput bool
		colonyID   string
		filterType string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all services",
		Long: `List all services discovered across all agents in the colony.
		
This command aggregates services from all connected agents and presents a unified view.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create resolver
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}

			// Resolve colony ID
			if colonyID == "" {
				colonyID, err = resolver.ResolveColonyID()
				if err != nil {
					return fmt.Errorf("failed to resolve colony: %w\n\nRun 'coral init <app-name>' to create a colony", err)
				}
			}

			// Load colony configuration
			loader := resolver.GetLoader()
			colonyConfig, err := loader.LoadColonyConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			// Get connect port
			connectPort := colonyConfig.Services.ConnectPort
			if connectPort == 0 {
				connectPort = 9000
			}

			// Create RPC client - try localhost first, then mesh IP
			baseURL := fmt.Sprintf("http://localhost:%d", connectPort)
			client := colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

			// Call ListAgents RPC to get all agents and their services
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			req := connect.NewRequest(&colonyv1.ListAgentsRequest{})
			resp, err := client.ListAgents(ctx, req)
			if err != nil {
				// Try mesh IP as fallback
				meshIP := colonyConfig.WireGuard.MeshIPv4
				if meshIP == "" {
					meshIP = constants.DefaultColonyMeshIPv4
				}
				baseURL = fmt.Sprintf("http://%s:%d", meshIP, connectPort)
				client = colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

				ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel2()

				resp, err = client.ListAgents(ctx2, connect.NewRequest(&colonyv1.ListAgentsRequest{}))
				if err != nil {
					return fmt.Errorf("failed to list agents (is colony running?): %w", err)
				}
			}

			agents := resp.Msg.Agents
			services := aggregateServices(agents)

			// Filter by type if requested
			if filterType != "" {
				filtered := make([]aggregatedService, 0)
				for _, svc := range services {
					if strings.EqualFold(svc.Type, filterType) {
						filtered = append(filtered, svc)
					}
				}
				services = filtered
			}

			if jsonOutput {
				return outputServicesJSON(services)
			}

			return outputServicesTable(services)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	cmd.Flags().StringVar(&colonyID, "colony", "", "Colony ID (overrides auto-detection)")
	cmd.Flags().StringVar(&filterType, "type", "", "Filter services by type (e.g., http, redis)")

	return cmd
}

type aggregatedService struct {
	Name      string   `json:"name"`
	Type      string   `json:"type,omitempty"`
	Port      int32    `json:"port"`
	Instances int      `json:"instances"`
	Agents    []string `json:"agents"`
}

func aggregateServices(agents []*colonyv1.Agent) []aggregatedService {
	serviceMap := make(map[string]*aggregatedService)

	for _, agent := range agents {
		// Handle multi-service agents
		for _, svcInfo := range agent.Services {
			key := fmt.Sprintf("%s:%d", svcInfo.Name, svcInfo.Port)

			if _, exists := serviceMap[key]; !exists {
				serviceMap[key] = &aggregatedService{
					Name:      svcInfo.Name,
					Type:      svcInfo.ServiceType,
					Port:      svcInfo.Port,
					Instances: 0,
					Agents:    make([]string, 0),
				}
			}

			entry := serviceMap[key]
			entry.Instances++
			entry.Agents = append(entry.Agents, agent.AgentId)
		}

		// Handle legacy single-service agents (fallback)
		if len(agent.Services) == 0 && agent.ComponentName != "" {
			// Note: We don't have port info for legacy agents in the Agent struct directly
			// unless we parse it or it's added. Assuming default or unknown for now.
			// Actually, Agent struct doesn't have Port field.
			// But wait, Agent struct in colony.proto has:
			// string component_name = 2;
			// repeated mesh.v1.ServiceInfo services = 7;

			// If services is empty, we only have component_name.
			// We can't really aggregate effectively without port.
			// But let's try to include it.
			key := agent.ComponentName
			if _, exists := serviceMap[key]; !exists {
				serviceMap[key] = &aggregatedService{
					Name:      agent.ComponentName,
					Type:      "unknown", // Legacy doesn't propagate type easily here
					Port:      0,         // Unknown port
					Instances: 0,
					Agents:    make([]string, 0),
				}
			}
			entry := serviceMap[key]
			entry.Instances++
			entry.Agents = append(entry.Agents, agent.AgentId)
		}
	}

	// Convert map to slice
	result := make([]aggregatedService, 0, len(serviceMap))
	for _, svc := range serviceMap {
		result = append(result, *svc)
	}

	// Sort by name
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result
}

func outputServicesJSON(services []aggregatedService) error {
	data, err := json.MarshalIndent(services, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func outputServicesTable(services []aggregatedService) error {
	if len(services) == 0 {
		fmt.Println("No services found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "SERVICE\tTYPE\tPORT\tINSTANCES\tAGENTS")

	for _, svc := range services {
		agentsStr := strings.Join(svc.Agents, ", ")
		if len(svc.Agents) > 3 {
			agentsStr = fmt.Sprintf("%s, ... (%d total)", strings.Join(svc.Agents[:3], ", "), len(svc.Agents))
		}

		portStr := fmt.Sprintf("%d", svc.Port)
		if svc.Port == 0 {
			portStr = "-"
		}

		typeStr := svc.Type
		if typeStr == "" {
			typeStr = "-"
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n",
			svc.Name,
			typeStr,
			portStr,
			svc.Instances,
			agentsStr,
		)
	}

	w.Flush()
	return nil
}
