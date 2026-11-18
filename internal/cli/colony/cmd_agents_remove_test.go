package colony

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/cli/fleet"
	"github.com/coral-mesh/coral/internal/cli/mesh"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/colony/server"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/constants"
)

func TestRemoveAgentClearsInventoryAndMeshTargets(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()
	db, err := database.New(t.TempDir(), "test-colony", constants.DefaultConnectionsCacheTTL, logger)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	const retiredID, activeID = "worker", "worker-multi"
	stale := &database.Service{ID: "worker:api", Name: "api", AppID: "api", AgentID: retiredID, LastSeen: time.Now().Add(-24 * time.Hour)}
	active := &database.Service{ID: "worker-multi:api", Name: "api", AppID: "api", AgentID: activeID, LastSeen: time.Now()}
	require.NoError(t, db.UpsertService(ctx, stale))
	require.NoError(t, db.UpsertService(ctx, active))
	reg := registry.New(db)
	require.NoError(t, reg.LoadFromDatabase(ctx))
	retiredEntry, err := reg.Get(retiredID)
	require.NoError(t, err)

	_, handler := colonyv1connect.NewColonyServiceHandler(server.New(reg, db, nil, server.Config{ColonyID: "test-colony"}, logger))
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	t.Setenv("CORAL_CONFIG", t.TempDir())
	t.Setenv("CORAL_COLONY_ENDPOINT", "")
	t.Chdir(t.TempDir())
	loader, err := config.NewLoader()
	require.NoError(t, err)
	require.NoError(t, loader.SaveColonyConfig(&config.ColonyConfig{
		ColonyID: "local-alias",
		Remote:   config.RemoteConfig{Endpoint: httpServer.URL},
	}))

	remove := func(args ...string) (string, error) {
		cmd := NewColonyCmd()
		cmd.SilenceErrors, cmd.SilenceUsage = true, true
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs(append([]string{"agents"}, args...))
		err := cmd.Execute()
		return out.String(), err
	}

	request := httptest.NewRequest(http.MethodPost, colonyv1connect.ColonyServiceRemoveAgentProcedure,
		bytes.NewBufferString(`{"agentId":"worker","colonyId":"test-colony"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusForbidden, response.Code, "the internal listener must reject non-local callers")

	client := colonyv1connect.NewColonyServiceClient(httpServer.Client(), httpServer.URL)
	_, err = client.RemoveAgent(ctx, connect.NewRequest(&colonyv1.RemoveAgentRequest{AgentId: retiredID, ColonyId: "wrong-colony"}))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))

	_, err = remove("--colony", "local-alias", "remove", activeID)
	require.ErrorContains(t, err, "heartbeat is healthy or degraded")
	out, err := remove("remove", retiredID, "--colony", "local-alias")
	require.NoError(t, err)
	require.Contains(t, out, `Removed agent "worker" from colony "test-colony"`)
	_, err = remove("remove", retiredID, "--colony", "local-alias")
	require.NoError(t, err, "retrying a completed removal is safe")

	_, err = client.MeshPing(ctx, connect.NewRequest(&colonyv1.MeshPingRequest{AgentId: retiredID}))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	var meshOutput bytes.Buffer
	meshCmd := mesh.NewMeshCmd()
	meshCmd.SetOut(&meshOutput)
	t.Setenv("CORAL_COLONY_ENDPOINT", httpServer.URL)
	meshCmd.SetArgs([]string{"ping", "--colony", "test-colony", "--dry-run", "--format", "json"})
	require.NoError(t, meshCmd.Execute())
	var report fleet.Report
	require.NoError(t, json.Unmarshal(meshOutput.Bytes(), &report))
	require.Len(t, report.Results, 1)
	require.Equal(t, activeID, report.Results[0].AgentID)

	require.NoError(t, reg.PersistService(ctx, retiredEntry, stale), "a poll started before removal can still finish")
	reloaded := registry.New(db)
	require.NoError(t, reloaded.LoadFromDatabase(ctx))
	require.Len(t, reloaded.ListAll(), 1)
	_, err = reloaded.Get(activeID)
	require.NoError(t, err)
	var heartbeats int
	require.NoError(t, db.DB().QueryRowContext(ctx, "SELECT count(*) FROM service_heartbeats WHERE service_id = ?", stale.ID).Scan(&heartbeats))
	require.Zero(t, heartbeats)

	_, err = reloaded.Register(retiredID, "", "", "", nil, nil, "")
	require.NoError(t, err)
	removed, err := reloaded.IsRemoved(ctx, retiredID)
	require.NoError(t, err)
	require.False(t, removed, "a fresh registration clears the removal marker")
}
