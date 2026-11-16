package helpers

import (
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
)

// CheckSummary describes explicit health checks, independently of heartbeats and registration.
type CheckSummary struct {
	Passing       int `json:"passing"`
	Failing       int `json:"failing"`
	Unknown       int `json:"unknown"`
	NotConfigured int `json:"not_configured"`
	// Oldest result contributing to this summary, so freshness is conservative.
	LastCheck *timestamppb.Timestamp `json:"oldest_check,omitempty"`
}

func (s *CheckSummary) Add(service *agentv1.ServiceStatus) {
	if !service.HasMonitor {
		s.NotConfigured++
		return
	}
	if service.LastCheck == nil || service.LastCheck.CheckValid() != nil || service.LastCheck.AsTime().IsZero() {
		s.Unknown++
		return
	}
	if s.LastCheck == nil || service.LastCheck.AsTime().Before(s.LastCheck.AsTime()) {
		s.LastCheck = service.LastCheck
	}
	switch service.Status {
	case "healthy":
		s.Passing++
	case "unhealthy":
		s.Failing++
	default:
		s.Unknown++
	}
}

func (s CheckSummary) Status() string {
	switch {
	case s.Failing > 0:
		return "unhealthy"
	case s.Unknown > 0:
		return "unknown"
	case s.Passing > 0:
		return "healthy"
	case s.NotConfigured > 0:
		return "not-configured"
	default:
		return "unknown"
	}
}

// AgentChecks never infers service health from an agent's heartbeat status.
func AgentChecks(statuses []*agentv1.ServiceStatus, serviceCount int, source string) CheckSummary {
	var summary CheckSummary
	if source == "live" {
		for _, service := range statuses {
			summary.Add(service)
		}
	}
	summary.Unknown += max(serviceCount-summary.Passing-summary.Failing-summary.Unknown-summary.NotConfigured, 0)
	return summary
}
