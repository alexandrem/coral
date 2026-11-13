package colony

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/cli/helpers"
)

type agentListOptions struct {
	statuses, services, runtimes, versions []string
	sortBy                                 string
	wide                                   bool
}

func (o *agentListOptions) addFlags(cmd *cobra.Command) {
	cmd.Flags().StringSliceVar(&o.statuses, "status", nil, "Filter heartbeat status: healthy,degraded,unhealthy (comma-separated)")
	cmd.Flags().StringSliceVar(&o.services, "service", nil, "Filter by exact service name (comma-separated)")
	cmd.Flags().StringSliceVar(&o.runtimes, "runtime", nil, "Filter runtime: native,docker,k8s-sidecar,k8s-daemonset,unknown (comma-separated)")
	cmd.Flags().StringSliceVar(&o.versions, "version", nil, "Filter by exact agent version (comma-separated)")
	cmd.Flags().StringVar(&o.sortBy, "sort", "id", "Sort by id,status,last-seen,runtime,version; prefix with - to reverse")
	cmd.Flags().BoolVar(&o.wide, "wide", false, "Show mesh addresses and full service lists")
}

func (o *agentListOptions) validate() error {
	for _, status := range o.statuses {
		if !matches([]string{"healthy", "degraded", "unhealthy"}, status) {
			return fmt.Errorf("invalid status %q (must be healthy, degraded, or unhealthy)", status)
		}
	}
	for _, runtime := range o.runtimes {
		if !matches([]string{"native", "docker", "k8s-sidecar", "k8s-daemonset", "unknown"}, runtime) {
			return fmt.Errorf("invalid runtime %q", runtime)
		}
	}
	if !matches([]string{"id", "status", "last-seen", "runtime", "version"}, strings.TrimPrefix(o.sortBy, "-")) {
		return fmt.Errorf("invalid sort %q (must be id, status, last-seen, runtime, or version)", o.sortBy)
	}
	return nil
}

func matches(values []string, value string) bool {
	if len(values) == 0 {
		return true
	}
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func agentRuntime(agent *colonyv1.Agent) string {
	name := agent.GetRuntimeContext().GetRuntimeType().String()
	return strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(name, "RUNTIME_CONTEXT_")), "_", "-")
}

func (o *agentListOptions) selectAgents(agents []*colonyv1.Agent) []*colonyv1.Agent {
	selected := make([]*colonyv1.Agent, 0, len(agents))
	for _, agent := range agents {
		if !matches(o.statuses, agent.Status) || !matches(o.runtimes, agentRuntime(agent)) || !matches(o.versions, agent.GetRuntimeContext().GetVersion()) {
			continue
		}
		serviceMatch := len(o.services) == 0
		for _, service := range agent.Services {
			serviceMatch = serviceMatch || matches(o.services, service.Name)
		}
		if !serviceMatch {
			continue
		}
		selected = append(selected, agent)
	}
	sort.Slice(selected, func(i, j int) bool {
		a, b := selected[i], selected[j]
		comparison := 0
		switch strings.TrimPrefix(o.sortBy, "-") {
		case "last-seen":
			comparison = a.GetLastSeen().AsTime().Compare(b.GetLastSeen().AsTime())
		case "status":
			comparison = statusPriority(a.Status) - statusPriority(b.Status)
		case "runtime":
			comparison = strings.Compare(agentRuntime(a), agentRuntime(b))
		case "version":
			comparison = strings.Compare(a.GetRuntimeContext().GetVersion(), b.GetRuntimeContext().GetVersion())
		}
		if comparison == 0 {
			comparison = strings.Compare(a.AgentId, b.AgentId)
		}
		if strings.HasPrefix(o.sortBy, "-") {
			return comparison > 0
		}
		return comparison < 0
	})
	return selected
}

func statusPriority(status string) int {
	switch status {
	case "unhealthy":
		return 0
	case "degraded":
		return 1
	case "healthy":
		return 2
	default:
		return 3
	}
}

func outputAgentSummary(out io.Writer, agents []*colonyv1.Agent, selected int) error {
	counts := map[string]int{}
	for _, agent := range agents {
		counts[agent.Status]++
	}
	_, err := fmt.Fprintf(out, "Agents: %d registered (heartbeat: %d healthy, %d degraded, %d unhealthy); %d shown\n\n", len(agents), counts["healthy"], counts["degraded"], counts["unhealthy"], selected)
	return err
}

func outputAgentsTable(out io.Writer, agents []*colonyv1.Agent, wide bool) error {
	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	header := "AGENT ID\tSERVICES\tRUNTIME\tVERSION\tHEARTBEAT\tLAST HEARTBEAT\tCHECKS\tLAST CHECK\tSERVICE DATA"
	if wide {
		header += "\tMESH IP\tSERVICE NAMES"
	}
	if _, err := fmt.Fprintln(w, header); err != nil {
		return err
	}
	for _, agent := range agents {
		version := agent.GetRuntimeContext().GetVersion()
		if version == "" {
			version = "-"
		}
		row := []string{agent.AgentId, strconv.Itoa(len(agent.Services)), agentRuntime(agent), version, agent.Status, helpers.FormatAge(agent.LastSeen, time.Now())}
		source := agent.ServicesSource
		if source == "" {
			source = "unknown"
		}
		checks := helpers.AgentChecks(agent.ServiceStatuses, len(agent.Services), source)
		data := source
		if source == "live" {
			data += " (" + helpers.FormatAge(agent.ServicesObservedAt, time.Now()) + ")"
		}
		row = append(row, checks.Status(), helpers.FormatAge(checks.LastCheck, time.Now()), data)
		if wide {
			row = append(row, agent.MeshIpv4, formatServicesList(agent.Services))
		}
		if _, err := fmt.Fprintln(w, strings.Join(row, "\t")); err != nil {
			return err
		}
	}
	return w.Flush()
}
