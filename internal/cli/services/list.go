package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/cli/helpers"
)

type listOptions struct {
	agent  string
	local  bool
	source string
	colony string
	format string
	names  []string
	sortBy string
	wide   bool
}

func addListFlags(cmd *cobra.Command, options *listOptions) {
	cmd.Flags().StringVar(&options.agent, "agent", "", "Agent ID (resolves via colony registry)")
	cmd.Flags().BoolVar(&options.local, "local", false, "List services on the local agent")
	cmd.Flags().StringVar(&options.source, "source", "", "Filter by source: auto or watched")
	helpers.AddColonyFlag(cmd, &options.colony)
	cmd.Flags().StringVarP(&options.format, "format", "o", "table", "Output format (table, json)")
	cmd.Flags().StringSliceVar(&options.names, "name", nil, "Filter by exact service name (comma-separated)")
	cmd.Flags().StringVar(&options.sortBy, "sort", "name", "Sort by name,instances,last-seen (colony) or name,port,pid (agent); prefix with - to reverse")
	cmd.Flags().BoolVar(&options.wide, "wide", false, "Show all agent IDs in colony output")
}

func runList(cmd *cobra.Command, options *listOptions) error {
	if options.local && options.agent != "" {
		return fmt.Errorf("cannot specify both --local and --agent")
	}
	if options.local && options.colony != "" {
		return fmt.Errorf("cannot specify both --local and --colony")
	}
	options.source = strings.ToLower(options.source)
	if err := options.validateSort(); err != nil {
		return err
	}
	filter, err := sourceFilter(options.source)
	if err != nil {
		return err
	}
	if options.format != "table" && options.format != "json" {
		return fmt.Errorf("unsupported format %q, must be table or json", options.format)
	}
	if options.local {
		return listAgent(cmd, options, "local", "localhost:9001", filter)
	}
	if options.agent != "" {
		addr, err := resolveAgent(cmd.Context(), options.agent, options.colony)
		if err != nil {
			return err
		}
		return listAgent(cmd, options, options.agent, addr, filter)
	}
	return listColony(cmd, options)
}

func sourceFilter(source string) (*agentv1.ServiceNamingSource, error) {
	switch strings.ToLower(source) {
	case "":
		return nil, nil
	case "auto":
		value := agentv1.ServiceNamingSource_SERVICE_NAMING_SOURCE_AUTO
		return &value, nil
	case "watched":
		value := agentv1.ServiceNamingSource_SERVICE_NAMING_SOURCE_AUTHORITATIVE
		return &value, nil
	default:
		return nil, fmt.Errorf("invalid source filter %q (must be auto or watched)", source)
	}
}

func resolveAgent(ctx context.Context, agentID, colonyID string) (string, error) {
	client, _, err := helpers.GetColonyClientWithFallback(ctx, colonyID)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := client.ListAgents(ctx, connect.NewRequest(&colonyv1.ListAgentsRequest{}))
	if err != nil {
		return "", fmt.Errorf("failed to list colony agents: %w", err)
	}
	for _, candidate := range resp.Msg.Agents {
		if candidate.AgentId == agentID {
			return fmt.Sprintf("%s:9001", candidate.MeshIpv4), nil
		}
	}
	return "", fmt.Errorf("agent not found: %s", agentID)
}

func listAgent(cmd *cobra.Command, options *listOptions, label, address string, filter *agentv1.ServiceNamingSource) error {
	client := agentv1connect.NewAgentServiceClient(http.DefaultClient, "http://"+strings.TrimPrefix(address, "http://"))
	request := &agentv1.ListServicesRequest{SourceFilter: filter}
	ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
	defer cancel()
	resp, err := client.ListServices(ctx, connect.NewRequest(request))
	if err != nil {
		return fmt.Errorf("failed to list services on agent %s: %w", label, err)
	}
	services := options.selectAgentServices(resp.Msg.Services)
	if options.format == "json" {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"services": services})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Services on %s:\n\n", label)
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tPORT\tSOURCE\tTIER\tPID\tHEALTH")
	for _, service := range services {
		health := service.Status
		if health == "" {
			health = "-"
		}
		_, _ = fmt.Fprintf(w, "%s\t%d\t%s\t%d\t%d\t%s\n", service.Name, service.Port, serviceSource(service), service.ObservationTier, service.ProcessId, health)
	}
	return w.Flush()
}

func listColony(cmd *cobra.Command, options *listOptions) error {
	ctx := cmd.Context()
	client, _, err := helpers.GetColonyClientWithFallback(ctx, options.colony)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := client.ListServices(ctx, connect.NewRequest(&colonyv1.ListServicesRequest{TimeRange: "1h"}))
	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}
	agentsResp, err := client.ListAgents(ctx, connect.NewRequest(&colonyv1.ListAgentsRequest{}))
	if err != nil {
		return fmt.Errorf("failed to list colony agents: %w", err)
	}
	serviceAgents, serviceTypes := indexAgentServices(agentsResp.Msg.Agents)
	services := resp.Msg.Services
	if options.source != "" {
		filtered := services[:0]
		for _, service := range services {
			if colonySource(service) == options.source {
				filtered = append(filtered, service)
			}
		}
		services = filtered
	}
	services = options.selectColonyServices(services)
	if options.format == "json" {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"services": services})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Services (%d) at %s:\n\n", len(services), time.Now().UTC().Format("2006-01-02 15:04:05 UTC"))
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
	_, _ = fmt.Fprintln(w, "SERVICE\tTYPE\tINSTANCES\tSOURCE\tAGENTS")
	for _, service := range services {
		agents := serviceAgents[strings.ToLower(service.Name)]
		if len(agents) == 0 {
			agents = []string{serviceAgent(service)}
		}
		serviceType := serviceTypes[strings.ToLower(service.Name)]
		if serviceType == "" {
			serviceType = "-"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", service.Name, serviceType, service.InstanceCount, colonySource(service), compactAgents(agents, options.wide))
	}
	return w.Flush()
}

func indexAgentServices(agents []*colonyv1.Agent) (map[string][]string, map[string]string) {
	byService := make(map[string][]string)
	types := make(map[string]string)
	for _, agent := range agents {
		for _, service := range agent.Services {
			name := strings.ToLower(service.Name)
			byService[name] = append(byService[name], agent.AgentId)
			if types[name] == "" {
				types[name] = service.ServiceType
			}
		}
	}
	for _, agents := range byService {
		sort.Strings(agents)
	}
	return byService, types
}

func serviceSource(service *agentv1.ServiceStatus) string {
	if service.NamingSource == agentv1.ServiceNamingSource_SERVICE_NAMING_SOURCE_AUTO {
		return "auto"
	}
	return "watched"
}

func colonySource(service *colonyv1.ServiceSummary) string {
	if service.Source == colonyv1.ServiceSource_SERVICE_SOURCE_OBSERVED {
		return "auto"
	}
	return "watched"
}

func serviceAgent(service *colonyv1.ServiceSummary) string {
	if service.AgentId == nil || *service.AgentId == "" {
		return "-"
	}
	return *service.AgentId
}
