// Package fleet shares target selection and bulk operation reporting across CLI commands.
package fleet

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
)

// Selector uses OR within a filter and AND between filters.
type Selector struct {
	IDs, Statuses, Services, Runtimes, Versions []string
}

func (s *Selector) AddFlags(cmd *cobra.Command) {
	cmd.Flags().StringSliceVar(&s.IDs, "agents", nil, "Target exact agent IDs (comma-separated)")
	cmd.Flags().StringSliceVar(&s.Statuses, "status", nil, "Filter heartbeat status: healthy,degraded,unhealthy (comma-separated)")
	cmd.Flags().StringSliceVar(&s.Services, "service", nil, "Filter exact service names (comma-separated)")
	cmd.Flags().StringSliceVar(&s.Runtimes, "runtime", nil, "Filter runtime: native,docker,k8s-sidecar,k8s-daemonset,unknown (comma-separated)")
	cmd.Flags().StringSliceVar(&s.Versions, "version", nil, "Filter exact agent versions (comma-separated)")
}

func (s Selector) HasFilters() bool {
	return len(s.IDs)+len(s.Statuses)+len(s.Services)+len(s.Runtimes)+len(s.Versions) > 0
}

func (s Selector) Validate() error {
	for _, values := range [][]string{s.IDs, s.Statuses, s.Services, s.Runtimes, s.Versions} {
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("selector values must not be empty")
			}
		}
	}
	for _, status := range s.Statuses {
		if !slices.Contains([]string{"healthy", "degraded", "unhealthy"}, status) {
			return fmt.Errorf("invalid status %q (must be healthy, degraded, or unhealthy)", status)
		}
	}
	for _, runtime := range s.Runtimes {
		if !slices.Contains([]string{"native", "docker", "k8s-sidecar", "k8s-daemonset", "unknown"}, runtime) {
			return fmt.Errorf("invalid runtime %q", runtime)
		}
	}
	return nil
}

func matches(values []string, value string) bool {
	return len(values) == 0 || slices.Contains(values, value)
}

func Runtime(agent *colonyv1.Agent) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(agent.GetRuntimeContext().GetRuntimeType().String(), "RUNTIME_CONTEXT_")), "_", "-")
}

// Select returns a new slice, preserving the input inventory for summaries.
func (s Selector) Select(agents []*colonyv1.Agent) []*colonyv1.Agent {
	selected := make([]*colonyv1.Agent, 0, len(agents))
	seen := map[string]bool{}
	for _, agent := range agents {
		if seen[agent.AgentId] || !matches(s.IDs, agent.AgentId) || !matches(s.Statuses, agent.Status) || !matches(s.Runtimes, Runtime(agent)) || !matches(s.Versions, agent.GetRuntimeContext().GetVersion()) {
			continue
		}
		serviceMatch := len(s.Services) == 0
		for _, service := range agent.Services {
			serviceMatch = serviceMatch || matches(s.Services, service.Name)
		}
		if !serviceMatch {
			continue
		}
		seen[agent.AgentId] = true
		selected = append(selected, agent)
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].AgentId < selected[j].AgentId })
	return selected
}
