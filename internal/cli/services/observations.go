package services

import (
	"sort"
	"strings"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/cli/helpers"
)

type serviceObservation struct {
	*colonyv1.ServiceSummary
	AgentIDs   []string             `json:"agent_ids"`
	DataSource string               `json:"data_source"`
	Health     string               `json:"health"`
	Checks     helpers.CheckSummary `json:"checks"`
}

func observeService(service *colonyv1.ServiceSummary, agents []*colonyv1.Agent) serviceObservation {
	view := serviceObservation{ServiceSummary: service, AgentIDs: []string{}, DataSource: "unknown"}
	sources := map[string]bool{}
	for _, agent := range agents {
		matched := false
		for _, candidate := range agent.Services {
			if strings.EqualFold(candidate.Name, service.Name) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		view.AgentIDs = append(view.AgentIDs, agent.AgentId)
		source := agent.ServicesSource
		if source == "" {
			source = "unknown"
		}
		sources[source] = true
		foundCheck := false
		if source == "live" {
			for _, status := range agent.ServiceStatuses {
				if strings.EqualFold(status.Name, service.Name) {
					view.Checks.Add(status)
					foundCheck = true
				}
			}
		}
		if !foundCheck {
			view.Checks.Unknown++
		}
	}
	if len(sources) == 1 {
		for source := range sources {
			view.DataSource = source
		}
	} else if len(sources) > 1 {
		view.DataSource = "mixed"
	}
	if len(sources) == 0 && service.Source == colonyv1.ServiceSource_SERVICE_SOURCE_OBSERVED {
		view.DataSource = "telemetry"
	}
	view.Checks.Unknown += max(int(service.InstanceCount)-view.Checks.Passing-view.Checks.Failing-view.Checks.Unknown-view.Checks.NotConfigured, 0)
	if len(view.AgentIDs) == 0 && service.GetAgentId() != "" {
		view.AgentIDs = append(view.AgentIDs, service.GetAgentId())
	}
	sort.Strings(view.AgentIDs)
	view.Health = view.Checks.Status()
	return view
}
