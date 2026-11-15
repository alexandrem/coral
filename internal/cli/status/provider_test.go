package status

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/constants"
)

func TestColonyStatusInfo(t *testing.T) {
	// Test that the ColonyStatusInfo struct has expected fields.
	info := ColonyStatusInfo{
		ColonyID:           "test-colony",
		Application:        "test-app",
		Environment:        "prod",
		IsDefault:          true,
		Running:            true,
		Status:             "running",
		UptimeSeconds:      3600,
		AgentCount:         5,
		ActiveAgentCount:   3,
		DegradedAgentCount: 2,
		WireGuardPort:      51820,
		ConnectPort:        constants.DefaultColonyPort,
		LocalEndpoint:      "http://localhost:9000",
		MeshEndpoint:       "http://100.64.0.1:9000",
		MeshIPv4:           "100.64.0.1",
	}

	if info.ColonyID != "test-colony" {
		t.Errorf("ColonyID = %q, want %q", info.ColonyID, "test-colony")
	}

	if !info.Running {
		t.Errorf("Running = %v, want %v", info.Running, true)
	}

	if info.UptimeSeconds != 3600 {
		t.Errorf("UptimeSeconds = %d, want %d", info.UptimeSeconds, 3600)
	}
}

type statusColony struct {
	colonyv1connect.UnimplementedColonyServiceHandler
	id         string
	agentCount int32
}

func (s *statusColony) GetStatus(_ context.Context, req *connect.Request[colonyv1.GetStatusRequest]) (*connect.Response[colonyv1.GetStatusResponse], error) {
	if req.Header().Get("Authorization") != "Bearer status-test-token" {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	return connect.NewResponse(&colonyv1.GetStatusResponse{ColonyId: s.id, Status: "running", AgentCount: s.agentCount}), nil
}

func TestStatusUsesEachRemoteEndpointWithTLSAndAuthentication(t *testing.T) {
	t.Setenv("CORAL_CONFIG", t.TempDir())
	t.Setenv("CORAL_COLONY_ENDPOINT", "")
	t.Setenv("CORAL_API_TOKEN", "status-test-token")
	t.Setenv("CORAL_CA_DATA", "")
	t.Setenv("CORAL_CA_FILE", "")
	t.Setenv("CORAL_INSECURE", "false")
	t.Chdir(t.TempDir())
	loader, err := config.NewLoader()
	require.NoError(t, err)
	for i, id := range []string{"east", "west"} {
		_, handler := colonyv1connect.NewColonyServiceHandler(&statusColony{id: id, agentCount: int32(i + 1)})
		server := httptest.NewTLSServer(handler)
		t.Cleanup(server.Close)
		cfg := config.DefaultColonyConfig(id, "app", "prod")
		cfg.Remote.Endpoint = server.URL
		cfg.Remote.CertificateAuthorityData = base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
		require.NoError(t, loader.SaveColonyConfig(cfg))
	}
	provider := NewProvider(loader)
	results := provider.QueryColoniesInParallel([]string{"east", "west"}, "east")
	for i, result := range results {
		require.True(t, result.Running)
		require.Equal(t, "running", result.Status)
		require.Empty(t, result.Error)
		require.Equal(t, int32(i+1), result.AgentCount)
		require.Contains(t, result.Endpoint, "https://")
		require.Empty(t, result.LocalEndpoint)
	}
	t.Setenv("CORAL_API_TOKEN", "wrong-token")
	unauthenticated := provider.QueryColonyStatus("east", "east")
	require.Equal(t, "unreachable", unauthenticated.Status)
	require.False(t, unauthenticated.Running)
	require.Contains(t, unauthenticated.Error, "unauthenticated")
}

func TestStatusDoesNotCallUnreachableOrWrongColonyStopped(t *testing.T) {
	t.Setenv("CORAL_CONFIG", t.TempDir())
	t.Setenv("CORAL_COLONY_ENDPOINT", "")
	t.Setenv("CORAL_API_TOKEN", "status-test-token")
	t.Chdir(t.TempDir())
	loader, err := config.NewLoader()
	require.NoError(t, err)
	_, handler := colonyv1connect.NewColonyServiceHandler(&statusColony{id: "other-colony"})
	server := httptest.NewServer(handler)
	cfg := config.DefaultColonyConfig("expected", "app", "prod")
	cfg.Remote.Endpoint = server.URL
	require.NoError(t, loader.SaveColonyConfig(cfg))
	provider := NewProvider(loader)
	mismatch := provider.QueryColonyStatus("expected", "")
	require.Equal(t, "unknown", mismatch.Status)
	require.Contains(t, mismatch.Error, "other-colony")
	server.Close()
	unreachable := provider.QueryColonyStatus("expected", "")
	require.Equal(t, "unreachable", unreachable.Status)
	require.NotEmpty(t, unreachable.Error)
	missingConfig := provider.QueryColonyStatus("missing", "")
	require.Equal(t, "unknown", missingConfig.Status)
	require.NotEmpty(t, missingConfig.Error)
}
