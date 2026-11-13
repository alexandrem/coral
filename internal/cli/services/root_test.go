package services

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/config"
)

func execute(cmd *cobra.Command, args ...string) error {
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestServiceCommandRejectsUnusedOptions(t *testing.T) {
	for _, args := range [][]string{
		{"watch", "api:8080", "--format", "json"},
		{"watch", "api:8080", "--local"},
		{"watch", "api:8080", "--source", "auto"},
		{"watch", "api:8080", "--colony", "prod"},
		{"watch", "api:8080", "--agent-url", "localhost:9001", "--colony", "prod"},
		{"--local", "--colony", "prod"},
		{"list", "--local", "--colony", "prod"},
		{"status"},
		{"list", "unexpected"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			require.Error(t, execute(NewCmd(), args...))
		})
	}
}

type scopeColony struct {
	colonyv1connect.UnimplementedColonyServiceHandler
	listed bool
}

func (s *scopeColony) GetStatus(context.Context, *connect.Request[colonyv1.GetStatusRequest]) (*connect.Response[colonyv1.GetStatusResponse], error) {
	return connect.NewResponse(&colonyv1.GetStatusResponse{}), nil
}

func (s *scopeColony) ListAgents(context.Context, *connect.Request[colonyv1.ListAgentsRequest]) (*connect.Response[colonyv1.ListAgentsResponse], error) {
	s.listed = true
	return connect.NewResponse(&colonyv1.ListAgentsResponse{}), nil
}

func TestWatchUsesSelectedColony(t *testing.T) {
	t.Setenv("CORAL_CONFIG", t.TempDir())
	t.Setenv("CORAL_COLONY_ENDPOINT", "")
	t.Setenv("CORAL_COLONY_ID", "default-colony")
	t.Chdir(t.TempDir())
	selected := &scopeColony{}
	_, handler := colonyv1connect.NewColonyServiceHandler(selected)
	server := httptest.NewServer(handler)
	defer server.Close()
	loader, err := config.NewLoader()
	require.NoError(t, err)
	cfg := config.DefaultColonyConfig("selected-colony", "app", "prod")
	cfg.Remote.Endpoint = server.URL
	require.NoError(t, loader.SaveColonyConfig(cfg))

	for _, cmd := range []*cobra.Command{NewCmd(), NewConnectAliasCmd()} {
		selected.listed = false
		args := []string{"api:8080", "--agent", "missing", "--colony", "selected-colony"}
		if cmd.Name() == "services" {
			args = append([]string{"watch"}, args...)
		}
		err := execute(cmd, args...)
		require.ErrorContains(t, err, "agent not found: missing")
		require.True(t, selected.listed, "must query the explicitly selected colony, not the default")
	}
}
