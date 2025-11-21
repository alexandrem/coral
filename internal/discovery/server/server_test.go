package server

import (
	"context"

	"io"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	discoveryv1 "github.com/coral-io/coral/coral/discovery/v1"
	"github.com/coral-io/coral/internal/constants"
	"github.com/coral-io/coral/internal/discovery"
	"github.com/coral-io/coral/internal/discovery/registry"
)

// testLogger returns a noop logger for tests.
func testLogger() zerolog.Logger {
	return zerolog.New(io.Discard)
}

// testSTUNServers returns default STUN servers for tests.
func testSTUNServers() []string {
	return []string{constants.DefaultSTUNServer}
}

// testTokenManager returns a token manager for tests.
func testTokenManager(t *testing.T) *discovery.TokenManager {

	return discovery.NewTokenManager(discovery.TokenConfig{
		SigningKey: []byte("test-key"),
		DefaultTTL: 5 * time.Minute,
		Issuer:     "test-issuer",
		Audience:   "test-audience",
	})
}

func TestServer_RegisterColony(t *testing.T) {
	reg := registry.New(5 * time.Minute)
	tokenMgr := testTokenManager(t)
	srv := New(reg, tokenMgr, "test-version", testLogger(), testSTUNServers())
	ctx := context.Background()

	t.Run("successful registration", func(t *testing.T) {
		req := connect.NewRequest(&discoveryv1.RegisterColonyRequest{
			MeshId:    "test-mesh",
			Pubkey:    "test-pubkey",
			Endpoints: []string{"10.0.0.1:41820"},
			Metadata:  map[string]string{"env": "test"},
		})

		resp, err := srv.RegisterColony(ctx, req)
		require.NoError(t, err)
		assert.True(t, resp.Msg.Success)
		assert.Equal(t, int32(300), resp.Msg.Ttl)
		assert.NotNil(t, resp.Msg.ExpiresAt)
	})

	t.Run("empty mesh_id", func(t *testing.T) {
		req := connect.NewRequest(&discoveryv1.RegisterColonyRequest{
			MeshId:    "",
			Pubkey:    "test-pubkey",
			Endpoints: []string{"10.0.0.1:41820"},
		})

		_, err := srv.RegisterColony(ctx, req)
		require.Error(t, err)

		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
		assert.Contains(t, connectErr.Message(), "mesh_id is required")
	})

	t.Run("empty pubkey", func(t *testing.T) {
		req := connect.NewRequest(&discoveryv1.RegisterColonyRequest{
			MeshId:    "test-mesh",
			Pubkey:    "",
			Endpoints: []string{"10.0.0.1:41820"},
		})

		_, err := srv.RegisterColony(ctx, req)
		require.Error(t, err)

		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
		assert.Contains(t, connectErr.Message(), "pubkey is required")
	})

	t.Run("no endpoints", func(t *testing.T) {
		req := connect.NewRequest(&discoveryv1.RegisterColonyRequest{
			MeshId:    "test-mesh",
			Pubkey:    "test-pubkey",
			Endpoints: []string{},
		})

		_, err := srv.RegisterColony(ctx, req)
		require.Error(t, err)

		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
		assert.Contains(t, connectErr.Message(), "at least one endpoint is required")
	})

	t.Run("update with same pubkey succeeds (renewal)", func(t *testing.T) {
		reg := registry.New(5 * time.Minute)
		srv := New(reg, testTokenManager(t), "test-version", testLogger(), testSTUNServers())

		// First registration
		req1 := connect.NewRequest(&discoveryv1.RegisterColonyRequest{
			MeshId:    "update-mesh",
			Pubkey:    "pubkey-1",
			Endpoints: []string{"10.0.0.1:41820"},
		})
		resp1, err := srv.RegisterColony(ctx, req1)
		require.NoError(t, err)
		expires1 := resp1.Msg.ExpiresAt.AsTime()

		time.Sleep(10 * time.Millisecond)

		// Second registration with same pubkey (renewal - should succeed)
		req2 := connect.NewRequest(&discoveryv1.RegisterColonyRequest{
			MeshId:    "update-mesh",
			Pubkey:    "pubkey-1",
			Endpoints: []string{"10.0.0.2:41820"},
		})
		resp2, err := srv.RegisterColony(ctx, req2)
		require.NoError(t, err)
		expires2 := resp2.Msg.ExpiresAt.AsTime()

		// Expiration should be updated
		assert.True(t, expires2.After(expires1))
	})
}

func TestServer_LookupColony(t *testing.T) {
	reg := registry.New(5 * time.Minute)
	srv := New(reg, testTokenManager(t), "test-version", testLogger(), testSTUNServers())
	ctx := context.Background()

	// Register a colony first
	regReq := connect.NewRequest(&discoveryv1.RegisterColonyRequest{
		MeshId:    "lookup-mesh",
		Pubkey:    "lookup-pubkey",
		Endpoints: []string{"10.0.0.1:41820", "10.0.0.2:41820"},
		Metadata:  map[string]string{"env": "prod", "region": "us-west"},
	})
	_, err := srv.RegisterColony(ctx, regReq)
	require.NoError(t, err)

	t.Run("successful lookup", func(t *testing.T) {
		req := connect.NewRequest(&discoveryv1.LookupColonyRequest{
			MeshId: "lookup-mesh",
		})

		resp, err := srv.LookupColony(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "lookup-mesh", resp.Msg.MeshId)
		assert.Equal(t, "lookup-pubkey", resp.Msg.Pubkey)
		assert.Equal(t, []string{"10.0.0.1:41820", "10.0.0.2:41820"}, resp.Msg.Endpoints)
		assert.Equal(t, "prod", resp.Msg.Metadata["env"])
		assert.Equal(t, "us-west", resp.Msg.Metadata["region"])
		assert.NotNil(t, resp.Msg.LastSeen)
	})

	t.Run("lookup nonexistent colony", func(t *testing.T) {
		req := connect.NewRequest(&discoveryv1.LookupColonyRequest{
			MeshId: "nonexistent",
		})

		_, err := srv.LookupColony(ctx, req)
		require.Error(t, err)

		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeNotFound, connectErr.Code())
		assert.Contains(t, connectErr.Message(), "colony not found")
	})

	t.Run("empty mesh_id", func(t *testing.T) {
		req := connect.NewRequest(&discoveryv1.LookupColonyRequest{
			MeshId: "",
		})

		_, err := srv.LookupColony(ctx, req)
		require.Error(t, err)

		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
	})

	t.Run("lookup expired colony", func(t *testing.T) {
		reg := registry.New(50 * time.Millisecond)
		srv := New(reg, testTokenManager(t), "test-version", testLogger(), testSTUNServers())

		// Register with short TTL
		regReq := connect.NewRequest(&discoveryv1.RegisterColonyRequest{
			MeshId:    "expire-mesh",
			Pubkey:    "expire-pubkey",
			Endpoints: []string{"10.0.0.1:41820"},
		})
		_, err := srv.RegisterColony(ctx, regReq)
		require.NoError(t, err)

		// Wait for expiration
		time.Sleep(100 * time.Millisecond)

		// Lookup should fail
		lookupReq := connect.NewRequest(&discoveryv1.LookupColonyRequest{
			MeshId: "expire-mesh",
		})
		_, err = srv.LookupColony(ctx, lookupReq)
		require.Error(t, err)

		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeNotFound, connectErr.Code())
	})
}

func TestServer_Health(t *testing.T) {
	reg := registry.New(5 * time.Minute)
	tokenMgr := testTokenManager(t)
	srv := New(reg, tokenMgr, "v1.2.3", testLogger(), testSTUNServers())
	ctx := context.Background()

	t.Run("health check with no colonies", func(t *testing.T) {
		req := connect.NewRequest(&discoveryv1.HealthRequest{})

		resp, err := srv.Health(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "ok", resp.Msg.Status)
		assert.Equal(t, "v1.2.3", resp.Msg.Version)
		assert.GreaterOrEqual(t, resp.Msg.UptimeSeconds, int64(0))
		assert.Equal(t, int32(0), resp.Msg.RegisteredColonies)
	})

	t.Run("health check with registered colonies", func(t *testing.T) {
		// Register some colonies
		for i := 1; i <= 3; i++ {
			regReq := connect.NewRequest(&discoveryv1.RegisterColonyRequest{
				MeshId:    "health-mesh-" + string(rune('0'+i)),
				Pubkey:    "pubkey",
				Endpoints: []string{"10.0.0.1:41820"},
			})
			_, err := srv.RegisterColony(ctx, regReq)
			require.NoError(t, err)
		}

		req := connect.NewRequest(&discoveryv1.HealthRequest{})

		resp, err := srv.Health(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "ok", resp.Msg.Status)
		assert.Equal(t, int32(3), resp.Msg.RegisteredColonies)
	})

	t.Run("uptime increases", func(t *testing.T) {
		// Wait a bit to ensure non-zero uptime
		time.Sleep(10 * time.Millisecond)

		req := connect.NewRequest(&discoveryv1.HealthRequest{})

		resp1, err := srv.Health(ctx, req)
		require.NoError(t, err)
		uptime1 := resp1.Msg.UptimeSeconds

		time.Sleep(1100 * time.Millisecond)

		resp2, err := srv.Health(ctx, req)
		require.NoError(t, err)
		uptime2 := resp2.Msg.UptimeSeconds

		assert.Greater(t, uptime2, uptime1)
	})
}

func TestServer_RegisterAgent(t *testing.T) {
	reg := registry.New(5 * time.Minute)
	srv := New(reg, testTokenManager(t), "test-version", testLogger(), testSTUNServers())
	ctx := context.Background()

	t.Run("successful agent registration with observed endpoint", func(t *testing.T) {
		observedEndpoint := &discoveryv1.Endpoint{
			Ip:       "203.0.113.5",
			Port:     51820,
			Protocol: "udp",
		}

		req := connect.NewRequest(&discoveryv1.RegisterAgentRequest{
			AgentId:          "agent-1",
			MeshId:           "mesh-1",
			Pubkey:           "agent-pubkey-1",
			Endpoints:        []string{},
			ObservedEndpoint: observedEndpoint,
			Metadata:         map[string]string{"env": "test"},
		})

		resp, err := srv.RegisterAgent(ctx, req)
		require.NoError(t, err)
		assert.True(t, resp.Msg.Success)
		assert.Equal(t, int32(300), resp.Msg.Ttl)
		assert.NotNil(t, resp.Msg.ExpiresAt)
		assert.Equal(t, observedEndpoint, resp.Msg.ObservedEndpoint)
	})

	t.Run("reject agent with no endpoints and no observed endpoint", func(t *testing.T) {
		req := connect.NewRequest(&discoveryv1.RegisterAgentRequest{
			AgentId:   "agent-2",
			MeshId:    "mesh-1",
			Pubkey:    "agent-pubkey-2",
			Endpoints: []string{},
		})

		_, err := srv.RegisterAgent(ctx, req)
		require.Error(t, err)

		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeInternal, connectErr.Code())
	})

	t.Run("empty agent_id", func(t *testing.T) {
		req := connect.NewRequest(&discoveryv1.RegisterAgentRequest{
			AgentId:   "",
			MeshId:    "mesh-1",
			Pubkey:    "agent-pubkey",
			Endpoints: []string{"10.0.0.1:41820"},
		})

		_, err := srv.RegisterAgent(ctx, req)
		require.Error(t, err)

		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
		assert.Contains(t, connectErr.Message(), "agent_id is required")
	})

	t.Run("empty mesh_id", func(t *testing.T) {
		req := connect.NewRequest(&discoveryv1.RegisterAgentRequest{
			AgentId:   "agent-1",
			MeshId:    "",
			Pubkey:    "agent-pubkey",
			Endpoints: []string{"10.0.0.1:41820"},
		})

		_, err := srv.RegisterAgent(ctx, req)
		require.Error(t, err)

		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
		assert.Contains(t, connectErr.Message(), "mesh_id is required")
	})

	t.Run("empty pubkey", func(t *testing.T) {
		req := connect.NewRequest(&discoveryv1.RegisterAgentRequest{
			AgentId:   "agent-1",
			MeshId:    "mesh-1",
			Pubkey:    "",
			Endpoints: []string{"10.0.0.1:41820"},
		})

		_, err := srv.RegisterAgent(ctx, req)
		require.Error(t, err)

		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
		assert.Contains(t, connectErr.Message(), "pubkey is required")
	})
}

func TestServer_LookupAgent(t *testing.T) {
	reg := registry.New(5 * time.Minute)
	srv := New(reg, testTokenManager(t), "test-version", testLogger(), testSTUNServers())
	ctx := context.Background()

	// Register an agent first
	observedEndpoint := &discoveryv1.Endpoint{
		Ip:       "203.0.113.5",
		Port:     51820,
		Protocol: "udp",
	}

	regReq := connect.NewRequest(&discoveryv1.RegisterAgentRequest{
		AgentId:          "lookup-agent",
		MeshId:           "mesh-1",
		Pubkey:           "agent-pubkey",
		Endpoints:        []string{},
		ObservedEndpoint: observedEndpoint,
		Metadata:         map[string]string{"env": "prod"},
	})
	_, err := srv.RegisterAgent(ctx, regReq)
	require.NoError(t, err)

	t.Run("successful agent lookup", func(t *testing.T) {
		req := connect.NewRequest(&discoveryv1.LookupAgentRequest{
			AgentId: "lookup-agent",
		})

		resp, err := srv.LookupAgent(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "lookup-agent", resp.Msg.AgentId)
		assert.Equal(t, "agent-pubkey", resp.Msg.Pubkey)
		assert.Equal(t, "prod", resp.Msg.Metadata["env"])
		assert.NotNil(t, resp.Msg.LastSeen)
		assert.Len(t, resp.Msg.ObservedEndpoints, 1)
	})

	t.Run("lookup nonexistent agent", func(t *testing.T) {
		req := connect.NewRequest(&discoveryv1.LookupAgentRequest{
			AgentId: "nonexistent",
		})

		_, err := srv.LookupAgent(ctx, req)
		require.Error(t, err)

		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeNotFound, connectErr.Code())
	})

	t.Run("empty agent_id", func(t *testing.T) {
		req := connect.NewRequest(&discoveryv1.LookupAgentRequest{
			AgentId: "",
		})

		_, err := srv.LookupAgent(ctx, req)
		require.Error(t, err)

		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
	})
}

func TestServer_RequestRelay(t *testing.T) {
	reg := registry.New(5 * time.Minute)
	srv := New(reg, testTokenManager(t), "test-version", testLogger(), testSTUNServers())
	ctx := context.Background()

	t.Run("successful relay allocation", func(t *testing.T) {
		req := connect.NewRequest(&discoveryv1.RequestRelayRequest{
			MeshId:       "mesh-1",
			AgentPubkey:  "agent-key",
			ColonyPubkey: "colony-key",
		})

		resp, err := srv.RequestRelay(ctx, req)
		require.NoError(t, err)
		assert.NotEmpty(t, resp.Msg.SessionId)
		assert.NotNil(t, resp.Msg.RelayEndpoint)
		assert.NotNil(t, resp.Msg.ExpiresAt)
		assert.NotEmpty(t, resp.Msg.RelayId)
	})

	t.Run("empty mesh_id", func(t *testing.T) {
		req := connect.NewRequest(&discoveryv1.RequestRelayRequest{
			MeshId:       "",
			AgentPubkey:  "agent-key",
			ColonyPubkey: "colony-key",
		})

		_, err := srv.RequestRelay(ctx, req)
		require.Error(t, err)

		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
		assert.Contains(t, connectErr.Message(), "mesh_id is required")
	})

	t.Run("empty agent_pubkey", func(t *testing.T) {
		req := connect.NewRequest(&discoveryv1.RequestRelayRequest{
			MeshId:       "mesh-1",
			AgentPubkey:  "",
			ColonyPubkey: "colony-key",
		})

		_, err := srv.RequestRelay(ctx, req)
		require.Error(t, err)

		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
		assert.Contains(t, connectErr.Message(), "agent_pubkey is required")
	})

	t.Run("empty colony_pubkey", func(t *testing.T) {
		req := connect.NewRequest(&discoveryv1.RequestRelayRequest{
			MeshId:       "mesh-1",
			AgentPubkey:  "agent-key",
			ColonyPubkey: "",
		})

		_, err := srv.RequestRelay(ctx, req)
		require.Error(t, err)

		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
		assert.Contains(t, connectErr.Message(), "colony_pubkey is required")
	})
}

func TestServer_ReleaseRelay(t *testing.T) {
	reg := registry.New(5 * time.Minute)
	srv := New(reg, testTokenManager(t), "test-version", testLogger(), testSTUNServers())
	ctx := context.Background()

	t.Run("successful relay release", func(t *testing.T) {
		// Allocate first
		allocReq := connect.NewRequest(&discoveryv1.RequestRelayRequest{
			MeshId:       "mesh-1",
			AgentPubkey:  "agent-key",
			ColonyPubkey: "colony-key",
		})
		allocResp, err := srv.RequestRelay(ctx, allocReq)
		require.NoError(t, err)

		// Release
		releaseReq := connect.NewRequest(&discoveryv1.ReleaseRelayRequest{
			SessionId: allocResp.Msg.SessionId,
			MeshId:    "mesh-1",
		})

		resp, err := srv.ReleaseRelay(ctx, releaseReq)
		require.NoError(t, err)
		assert.True(t, resp.Msg.Success)
	})

	t.Run("release nonexistent session", func(t *testing.T) {
		req := connect.NewRequest(&discoveryv1.ReleaseRelayRequest{
			SessionId: "nonexistent-session",
			MeshId:    "mesh-1",
		})

		_, err := srv.ReleaseRelay(ctx, req)
		require.Error(t, err)

		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeNotFound, connectErr.Code())
	})

	t.Run("empty session_id", func(t *testing.T) {
		req := connect.NewRequest(&discoveryv1.ReleaseRelayRequest{
			SessionId: "",
			MeshId:    "mesh-1",
		})

		_, err := srv.ReleaseRelay(ctx, req)
		require.Error(t, err)

		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
		assert.Contains(t, connectErr.Message(), "session_id is required")
	})
}

func TestServer_SplitBrainDetection(t *testing.T) {
	reg := registry.New(5 * time.Minute)
	srv := New(reg, testTokenManager(t), "test-version", testLogger(), testSTUNServers())
	ctx := context.Background()

	t.Run("prevent split-brain on colony registration", func(t *testing.T) {
		// First registration
		req1 := connect.NewRequest(&discoveryv1.RegisterColonyRequest{
			MeshId:    "split-test",
			Pubkey:    "pubkey-1",
			Endpoints: []string{"10.0.0.1:41820"},
		})
		_, err := srv.RegisterColony(ctx, req1)
		require.NoError(t, err)

		// Try to register with different pubkey
		req2 := connect.NewRequest(&discoveryv1.RegisterColonyRequest{
			MeshId:    "split-test",
			Pubkey:    "pubkey-2",
			Endpoints: []string{"10.0.0.2:41820"},
		})
		_, err = srv.RegisterColony(ctx, req2)
		require.Error(t, err)

		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeInternal, connectErr.Code())
		assert.Contains(t, connectErr.Message(), "already registered")
	})
}
