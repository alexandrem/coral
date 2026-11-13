package services

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/cli/fleet"
	"github.com/coral-mesh/coral/internal/config"
)

type bulkRegistry struct {
	colonyv1connect.UnimplementedColonyServiceHandler
}

func (*bulkRegistry) GetStatus(context.Context, *connect.Request[colonyv1.GetStatusRequest]) (*connect.Response[colonyv1.GetStatusResponse], error) {
	return connect.NewResponse(&colonyv1.GetStatusResponse{ColonyId: "bulk"}), nil
}

func (*bulkRegistry) ListAgents(context.Context, *connect.Request[colonyv1.ListAgentsRequest]) (*connect.Response[colonyv1.ListAgentsResponse], error) {
	return connect.NewResponse(&colonyv1.ListAgentsResponse{Agents: []*colonyv1.Agent{{AgentId: "healthy", Status: "healthy", MeshIpv4: "10.42.0.2"}, {AgentId: "degraded", Status: "degraded", MeshIpv4: "10.42.0.3"}}}), nil
}

func TestWatchAndConnectAliasShareBulkTargetPreview(t *testing.T) {
	t.Setenv("CORAL_CONFIG", t.TempDir())
	t.Setenv("CORAL_COLONY_ENDPOINT", "")
	t.Chdir(t.TempDir())
	_, handler := colonyv1connect.NewColonyServiceHandler(&bulkRegistry{})
	server := httptest.NewServer(handler)
	defer server.Close()
	loader, err := config.NewLoader()
	require.NoError(t, err)
	cfg := config.DefaultColonyConfig("bulk", "app", "test")
	cfg.Remote.Endpoint = server.URL
	require.NoError(t, loader.SaveColonyConfig(cfg))
	for _, cmd := range []*cobra.Command{NewCmd(), NewConnectAliasCmd()} {
		var output, stderr bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&stderr)
		args := []string{"api:8080:/health", "--status", "degraded", "--colony", "bulk", "--dry-run", "--format", "json"}
		if cmd.Name() == "services" {
			args = append([]string{"watch"}, args...)
		}
		cmd.SetArgs(args)
		require.NoError(t, cmd.Execute())
		var report fleet.Report
		require.NoError(t, json.Unmarshal(output.Bytes(), &report))
		require.Equal(t, "services watch", report.Operation)
		require.Equal(t, "bulk", report.ColonyID)
		require.Equal(t, 1, report.Summary.Planned)
		require.Equal(t, "degraded", report.Results[0].AgentID)
		require.Zero(t, report.Results[0].Attempts)
		require.Contains(t, string(report.Parameters), "/health")
		require.Empty(t, stderr.String(), "dry-run must not begin service registration")
	}
	// A saved failure contains enough information to preview its retry without
	// repeating the service specifications or selecting a colony again.
	filename := filepath.Join(t.TempDir(), "watch.json")
	saved := fleet.Report{Version: 1, Operation: "services watch", ColonyID: "bulk", Parameters: json.RawMessage(`[{"name":"api","port":8080,"health_endpoint":"/health"}]`), Results: []fleet.Result{{AgentID: "degraded", Status: "failed"}, {AgentID: "healthy", Status: "succeeded"}}}
	data, err := json.Marshal(saved)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filename, data, 0600))
	cmd := NewCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"watch", "--retry-failed", filename, "--dry-run", "--format", "json"})
	require.NoError(t, cmd.Execute())
	var retried fleet.Report
	require.NoError(t, json.Unmarshal(output.Bytes(), &retried))
	require.Equal(t, 1, retried.Summary.Planned)
	require.Equal(t, "degraded", retried.Results[0].AgentID)
	require.JSONEq(t, string(saved.Parameters), string(retried.Parameters))
	err = execute(NewCmd(), "watch", "api:9090", "--retry-failed", filename, "--dry-run")
	require.ErrorContains(t, err, "parameters differ")
}

func TestBulkWatchRejectsAmbiguousScopesBeforeConnecting(t *testing.T) {
	for _, args := range [][]string{
		{"watch", "api:8080", "--all", "--agent-url", "localhost:9001"},
		{"watch", "api:8080", "--all", "--wait"},
		{"watch", "api:8080", "--agent", "one", "--agents", "two"},
		{"watch", "api:8080", "--dry-run"},
		{"watch", "api:8080", "--all", "--status", "healthy"},
	} {
		require.Error(t, execute(NewCmd(), args...))
	}
}
