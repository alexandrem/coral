package services

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
)

func TestServiceSelection(t *testing.T) {
	services := []*colonyv1.ServiceSummary{
		{Name: "api", InstanceCount: 3, LastSeen: timestamppb.New(time.Now())},
		{Name: "worker", InstanceCount: 1},
		{Name: "cache", InstanceCount: 2},
	}
	o := listOptions{names: []string{"api", "cache"}, sortBy: "-instances"}
	require.NoError(t, o.validateSort())
	require.Equal(t, []*colonyv1.ServiceSummary{services[0], services[2]}, o.selectColonyServices(services))
	o.names, o.sortBy = nil, "last-seen"
	require.Equal(t, []*colonyv1.ServiceSummary{services[2], services[1], services[0]}, o.selectColonyServices(services))
	o.local = true
	require.Error(t, o.validateSort(), "reject a colony-only sort on a local agent")
	o.sortBy, o.names = "port", []string{"api", "worker"}
	require.NoError(t, o.validateSort())
	local := []*agentv1.ServiceStatus{{Name: "api", Port: 8080}, {Name: "worker", Port: 0}, {Name: "cache", Port: 6379}}
	require.Equal(t, []*agentv1.ServiceStatus{local[1], local[0]}, o.selectAgentServices(local))
}

func TestCompactAgents(t *testing.T) {
	agents := []string{"full-agent-id-1", "full-agent-id-2", "full-agent-id-3"}
	require.Equal(t, "full-agent-id-1, full-agent-id-2 (+1 more)", compactAgents(agents, false))
	require.Equal(t, "full-agent-id-1, full-agent-id-2, full-agent-id-3", compactAgents(agents, true))
}
