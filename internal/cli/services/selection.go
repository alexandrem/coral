package services

import (
	"fmt"
	"sort"
	"strings"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
)

func (o *listOptions) validateSort() error {
	allowed := []string{"name", "instances", "last-seen"}
	if o.local || o.agent != "" {
		allowed = []string{"name", "port", "pid"}
	}
	for _, key := range allowed {
		if strings.TrimPrefix(o.sortBy, "-") == key {
			return nil
		}
	}
	return fmt.Errorf("invalid sort %q for this scope (must be %s)", o.sortBy, strings.Join(allowed, ", "))
}

func (o *listOptions) matchesName(name string) bool {
	if len(o.names) == 0 {
		return true
	}
	for _, candidate := range o.names {
		if candidate == name {
			return true
		}
	}
	return false
}

func (o *listOptions) less(comparison int, a, b string) bool {
	if comparison == 0 {
		comparison = strings.Compare(a, b)
	}
	if strings.HasPrefix(o.sortBy, "-") {
		return comparison > 0
	}
	return comparison < 0
}

func (o *listOptions) selectAgentServices(services []*agentv1.ServiceStatus) []*agentv1.ServiceStatus {
	selected := make([]*agentv1.ServiceStatus, 0, len(services))
	for _, service := range services {
		if o.matchesName(service.Name) {
			selected = append(selected, service)
		}
	}
	sort.Slice(selected, func(i, j int) bool {
		a, b := selected[i], selected[j]
		comparison := 0
		switch strings.TrimPrefix(o.sortBy, "-") {
		case "port":
			comparison = int(a.Port) - int(b.Port)
		case "pid":
			if a.ProcessId < b.ProcessId {
				comparison = -1
			} else if a.ProcessId > b.ProcessId {
				comparison = 1
			}
		}
		return o.less(comparison, a.Name, b.Name)
	})
	return selected
}

func (o *listOptions) selectColonyServices(services []*colonyv1.ServiceSummary) []*colonyv1.ServiceSummary {
	selected := make([]*colonyv1.ServiceSummary, 0, len(services))
	for _, service := range services {
		if o.matchesName(service.Name) {
			selected = append(selected, service)
		}
	}
	sort.Slice(selected, func(i, j int) bool {
		a, b := selected[i], selected[j]
		comparison := 0
		switch strings.TrimPrefix(o.sortBy, "-") {
		case "instances":
			comparison = int(a.InstanceCount) - int(b.InstanceCount)
		case "last-seen":
			comparison = a.GetLastSeen().AsTime().Compare(b.GetLastSeen().AsTime())
		}
		return o.less(comparison, a.Name, b.Name)
	})
	return selected
}

func compactAgents(agents []string, wide bool) string {
	if wide || len(agents) <= 2 {
		return strings.Join(agents, ", ")
	}
	return fmt.Sprintf("%s (+%d more)", strings.Join(agents[:2], ", "), len(agents)-2)
}
