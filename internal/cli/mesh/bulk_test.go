package mesh

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/cli/fleet"
	"github.com/coral-mesh/coral/internal/config"
)

type diagnosticColony struct {
	colonyv1connect.UnimplementedColonyServiceHandler
	mu                    sync.Mutex
	pingCalls, auditCalls []string
	recovered             bool
}

func (*diagnosticColony) GetStatus(context.Context, *connect.Request[colonyv1.GetStatusRequest]) (*connect.Response[colonyv1.GetStatusResponse], error) {
	return connect.NewResponse(&colonyv1.GetStatusResponse{ColonyId: "diagnostics"}), nil
}

func (*diagnosticColony) ListAgents(context.Context, *connect.Request[colonyv1.ListAgentsRequest]) (*connect.Response[colonyv1.ListAgentsResponse], error) {
	return connect.NewResponse(&colonyv1.ListAgentsResponse{Agents: []*colonyv1.Agent{
		{AgentId: "one", Status: "healthy", Services: []*meshv1.ServiceInfo{{Name: "api"}}},
		{AgentId: "two", Status: "degraded", Services: []*meshv1.ServiceInfo{{Name: "api"}}},
		{AgentId: "three", Status: "healthy"},
	}}), nil
}

func (s *diagnosticColony) MeshPing(_ context.Context, req *connect.Request[colonyv1.MeshPingRequest]) (*connect.Response[colonyv1.MeshPingResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pingCalls = append(s.pingCalls, req.Msg.AgentId)
	result := &colonyv1.MeshPingResponse_AgentPingResult{AgentId: req.Msg.AgentId, Sent: req.Msg.Count, Received: req.Msg.Count, MeshIp: "10.42.0.2"}
	if req.Msg.AgentId == "two" && !s.recovered {
		result.Received, result.PacketLossPercentage = 0, 100
	}
	return connect.NewResponse(&colonyv1.MeshPingResponse{Results: []*colonyv1.MeshPingResponse_AgentPingResult{result}}), nil
}

func (s *diagnosticColony) MeshAudit(_ context.Context, req *connect.Request[colonyv1.MeshAuditRequest]) (*connect.Response[colonyv1.MeshAuditResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditCalls = append(s.auditCalls, req.Msg.AgentId)
	return connect.NewResponse(&colonyv1.MeshAuditResponse{Results: []*colonyv1.MeshAuditAgentResult{{AgentId: req.Msg.AgentId, NatType: "symmetric", HandshakeAgeSeconds: 3}}}), nil
}

func setupDiagnostics(t *testing.T) *diagnosticColony {
	t.Helper()
	t.Setenv("CORAL_CONFIG", t.TempDir())
	t.Setenv("CORAL_COLONY_ENDPOINT", "")
	t.Chdir(t.TempDir())
	mock := &diagnosticColony{}
	_, handler := colonyv1connect.NewColonyServiceHandler(mock)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	loader, err := config.NewLoader()
	require.NoError(t, err)
	cfg := config.DefaultColonyConfig("diagnostics", "app", "test")
	cfg.Remote.Endpoint = server.URL
	require.NoError(t, loader.SaveColonyConfig(cfg))
	return mock
}

func executeDiagnostic(cmd *cobra.Command, args ...string) (string, error) {
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs(append([]string{"--colony", "diagnostics"}, args...))
	err := cmd.Execute()
	return output.String(), err
}

func TestPingDryRunPartialFailureAndFailedTargetRetry(t *testing.T) {
	mock := setupDiagnostics(t)
	output, err := executeDiagnostic(newPingCmd(), "--service", "api", "--dry-run", "--format", "json")
	require.NoError(t, err)
	var report fleet.Report
	require.NoError(t, json.Unmarshal([]byte(output), &report))
	require.Equal(t, 2, report.Summary.Planned)
	require.Empty(t, mock.pingCalls)
	filename := filepath.Join(t.TempDir(), "ping.json")
	output, err = executeDiagnostic(newPingCmd(), "--service", "api", "--count", "2", "--timeout", "500ms", "--report", filename, "--format", "json")
	require.Error(t, err)
	require.NoError(t, json.Unmarshal([]byte(output), &report))
	require.Equal(t, 1, report.Summary.Succeeded)
	require.Equal(t, 1, report.Summary.Failed)
	mock.mu.Lock()
	mock.pingCalls, mock.recovered = nil, true
	mock.mu.Unlock()
	output, err = executeDiagnostic(newPingCmd(), "--retry-failed", filename)
	require.NoError(t, err)
	require.Equal(t, []string{"two"}, mock.pingCalls)
	require.Contains(t, output, "MESH IP")
	require.Contains(t, output, "LOSS")
	require.Contains(t, output, "two")
	_, err = executeDiagnostic(newPingCmd(), "--retry-failed", filename, "--count", "3")
	require.ErrorContains(t, err, "parameters differ")
}

func TestAuditUsesTheSameSelectorsAndReportsFindings(t *testing.T) {
	mock := setupDiagnostics(t)
	output, err := executeDiagnostic(newAuditCmd(), "--status", "degraded", "--format", "json")
	require.NoError(t, err)
	var report fleet.Report
	require.NoError(t, json.Unmarshal([]byte(output), &report))
	require.Equal(t, []string{"two"}, mock.auditCalls)
	require.Equal(t, 1, report.Summary.Succeeded)
	require.Contains(t, string(report.Results[0].Data), "symmetric")
}

func TestInvalidDiagnosticOptionsFailBeforeNetworking(t *testing.T) {
	for _, args := range [][]string{{"--concurrency", "0"}, {"--count", "0"}, {"--timeout", "0s"}, {"one", "--agents", "two"}, {"--all", "--status", "healthy"}, {"--retries", "-1"}} {
		_, err := executeDiagnostic(newPingCmd(), args...)
		require.Error(t, err)
	}
}
