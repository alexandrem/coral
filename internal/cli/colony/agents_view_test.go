package colony

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
)

func TestSelectAgents(t *testing.T) {
	now := time.Now()
	agents := []*colonyv1.Agent{
		{AgentId: "api-new", Status: "healthy", LastSeen: timestamppb.New(now), Services: []*meshv1.ServiceInfo{{Name: "api"}}, RuntimeContext: &agentv1.RuntimeContextResponse{RuntimeType: agentv1.RuntimeContext_RUNTIME_CONTEXT_DOCKER, Version: "v2"}},
		{AgentId: "api-old", Status: "degraded", LastSeen: timestamppb.New(now.Add(-time.Minute)), Services: []*meshv1.ServiceInfo{{Name: "api"}}, RuntimeContext: &agentv1.RuntimeContextResponse{RuntimeType: agentv1.RuntimeContext_RUNTIME_CONTEXT_DOCKER, Version: "v1"}},
		{AgentId: "legacy", Status: "unhealthy"},
	}
	tests := []struct {
		name    string
		options agentListOptions
		ids     []string
	}{
		{"default stable order", agentListOptions{sortBy: "id"}, []string{"api-new", "api-old", "legacy"}},
		{"OR statuses, AND other filters", agentListOptions{statuses: []string{"healthy", "degraded"}, services: []string{"api"}, runtimes: []string{"docker"}, versions: []string{"v1"}, sortBy: "id"}, []string{"api-old"}},
		{"oldest heartbeat first", agentListOptions{sortBy: "last-seen"}, []string{"legacy", "api-old", "api-new"}},
		{"reverse heartbeat", agentListOptions{sortBy: "-last-seen"}, []string{"api-new", "api-old", "legacy"}},
		{"problems first", agentListOptions{sortBy: "status"}, []string{"legacy", "api-old", "api-new"}},
		{"unknown runtime", agentListOptions{runtimes: []string{"unknown"}, sortBy: "id"}, []string{"legacy"}},
		{"no matches", agentListOptions{services: []string{"missing"}, sortBy: "id"}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, tt.options.validate())
			selected := tt.options.selectAgents(agents)
			ids := make([]string, 0, len(selected))
			for _, agent := range selected {
				ids = append(ids, agent.AgentId)
			}
			require.Equal(t, tt.ids, ids)
		})
	}
	require.Equal(t, "api-new", agents[0].AgentId, "selection must not reorder the source inventory")
}

func TestAgentTableKeepsCopyableIDs(t *testing.T) {
	agents := []*colonyv1.Agent{{AgentId: "production-us-east-api-instance-123456789", Status: "healthy", MeshIpv4: "10.42.0.15", Services: []*meshv1.ServiceInfo{{Name: "payments"}}}}
	var output bytes.Buffer
	outputAgentSummary(&output, agents, 1)
	require.NoError(t, outputAgentsTable(&output, agents, false))
	require.Contains(t, output.String(), agents[0].AgentId)
	require.Contains(t, output.String(), "1 healthy")
	require.NotContains(t, output.String(), "10.42.0.15")
	output.Reset()
	require.NoError(t, outputAgentsTable(&output, agents, true))
	require.Contains(t, output.String(), "10.42.0.15")
	require.Contains(t, output.String(), "payments")
}

func TestInvalidAgentFiltersFailBeforeConnecting(t *testing.T) {
	for _, args := range [][]string{{"--status", "broken"}, {"--runtime", "banana"}, {"--sort", "missing"}} {
		cmd := newAgentsCmd()
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		cmd.SetArgs(args)
		require.ErrorContains(t, cmd.Execute(), "invalid")
	}
}
