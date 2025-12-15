package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/colony/ca"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/registry"
)

func newTestServer(t *testing.T, config Config) *Server {
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)

	// Create temporary database for testing.
	tmpDir := t.TempDir()
	db, err := database.New(tmpDir, config.ColonyID, logger)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	reg := registry.New(db)

	// Create CA directory within temp directory.
	caDir := filepath.Join(tmpDir, "ca")

	// Initialize CA manager for testing (RFD 047).
	jwtSigningKey := []byte("test-signing-key")
	caManager, err := ca.NewManager(db.DB(), ca.Config{
		ColonyID:      config.ColonyID,
		CADir:         caDir,
		JWTSigningKey: jwtSigningKey,
	})
	if err != nil {
		t.Fatalf("Failed to create test CA manager: %v", err)
	}

	return New(reg, db, caManager, config, logger)
}

func TestServer_GetStatus(t *testing.T) {
	t.Run("status with zero agents", func(t *testing.T) {
		config := Config{
			ColonyID:        "test-colony",
			ApplicationName: "TestApp",
			Environment:     "test",
			DashboardPort:   3000,
			StoragePath:     "",
		}
		server := newTestServer(t, config)

		// Sleep briefly to ensure non-zero uptime.
		time.Sleep(10 * time.Millisecond)

		req := connect.NewRequest(&colonyv1.GetStatusRequest{})
		resp, err := server.GetStatus(context.Background(), req)

		require.NoError(t, err)
		assert.Equal(t, "test-colony", resp.Msg.ColonyId)
		assert.Equal(t, "TestApp", resp.Msg.AppName)
		assert.Equal(t, "test", resp.Msg.Environment)
		assert.Equal(t, "running", resp.Msg.Status) // Colony is running, just no agents yet.
		assert.Equal(t, int32(0), resp.Msg.AgentCount)
		assert.Equal(t, "http://localhost:3000", resp.Msg.DashboardUrl)
		assert.NotNil(t, resp.Msg.StartedAt)
		assert.GreaterOrEqual(t, resp.Msg.UptimeSeconds, int64(0))
	})

	t.Run("status with all healthy agents", func(t *testing.T) {
		config := Config{
			ColonyID:        "test-colony",
			ApplicationName: "TestApp",
			Environment:     "production",
			DashboardPort:   3000,
			StoragePath:     "",
		}
		server := newTestServer(t, config)

		// Register healthy agents.
		_, _ = server.registry.Register("agent-1", "frontend", "100.64.0.2", "fd42::2", nil, nil, "")
		_, _ = server.registry.Register("agent-2", "api", "100.64.0.3", "fd42::3", nil, nil, "")

		req := connect.NewRequest(&colonyv1.GetStatusRequest{})
		resp, err := server.GetStatus(context.Background(), req)

		require.NoError(t, err)
		assert.Equal(t, "running", resp.Msg.Status)
		assert.Equal(t, int32(2), resp.Msg.AgentCount)
	})

	t.Run("status with degraded agents", func(t *testing.T) {
		config := Config{
			ColonyID:        "test-colony",
			ApplicationName: "TestApp",
			Environment:     "production",
			DashboardPort:   3000,
			StoragePath:     "",
		}
		server := newTestServer(t, config)

		// Register agents and manipulate their LastSeen.
		_, _ = server.registry.Register("agent-healthy", "frontend", "100.64.0.2", "fd42::2", nil, nil, "")
		_, _ = server.registry.Register("agent-degraded", "api", "100.64.0.3", "fd42::3", nil, nil, "")

		// Manually set LastSeen to make one degraded.
		entries := server.registry.ListAll()
		now := time.Now()
		for _, entry := range entries {
			if entry.AgentID == "agent-degraded" {
				entry.LastSeen = now.Add(-60 * time.Second) // Degraded.
			}
		}

		req := connect.NewRequest(&colonyv1.GetStatusRequest{})
		resp, err := server.GetStatus(context.Background(), req)

		require.NoError(t, err)
		assert.Equal(t, "running", resp.Msg.Status) // Colony status is decoupled from agent health.
		assert.Equal(t, int32(2), resp.Msg.AgentCount)
	})

	t.Run("status with unhealthy agents", func(t *testing.T) {
		config := Config{
			ColonyID:        "test-colony",
			ApplicationName: "TestApp",
			Environment:     "production",
			DashboardPort:   3000,
			StoragePath:     "",
		}
		server := newTestServer(t, config)

		// Register agents and manipulate their LastSeen.
		_, _ = server.registry.Register("agent-healthy", "frontend", "100.64.0.2", "fd42::2", nil, nil, "")
		_, _ = server.registry.Register("agent-unhealthy", "api", "100.64.0.3", "fd42::3", nil, nil, "")

		// Manually set LastSeen to make one unhealthy.
		entries := server.registry.ListAll()
		now := time.Now()
		for _, entry := range entries {
			if entry.AgentID == "agent-unhealthy" {
				entry.LastSeen = now.Add(-5 * time.Minute) // Unhealthy.
			}
		}

		req := connect.NewRequest(&colonyv1.GetStatusRequest{})
		resp, err := server.GetStatus(context.Background(), req)

		require.NoError(t, err)
		assert.Equal(t, "running", resp.Msg.Status) // Colony status is decoupled from agent health.
		assert.Equal(t, int32(2), resp.Msg.AgentCount)
	})

	t.Run("status with no dashboard port", func(t *testing.T) {
		config := Config{
			ColonyID:        "test-colony",
			ApplicationName: "TestApp",
			Environment:     "test",
			DashboardPort:   0,
			StoragePath:     "",
		}
		server := newTestServer(t, config)

		req := connect.NewRequest(&colonyv1.GetStatusRequest{})
		resp, err := server.GetStatus(context.Background(), req)

		require.NoError(t, err)
		assert.Equal(t, "", resp.Msg.DashboardUrl)
	})

	t.Run("status with storage calculation", func(t *testing.T) {
		// Create temporary directory with files.
		tmpDir, err := os.MkdirTemp("", "coral-server-test-*")
		require.NoError(t, err)
		defer func() { _ = os.RemoveAll(tmpDir) }()

		testFile := filepath.Join(tmpDir, "test.db")
		testContent := []byte("test database content")
		err = os.WriteFile(testFile, testContent, 0644)
		require.NoError(t, err)

		config := Config{
			ColonyID:        "test-colony",
			ApplicationName: "TestApp",
			Environment:     "test",
			DashboardPort:   3000,
			StoragePath:     tmpDir,
		}
		server := newTestServer(t, config)

		req := connect.NewRequest(&colonyv1.GetStatusRequest{})
		resp, err := server.GetStatus(context.Background(), req)

		require.NoError(t, err)
		assert.Equal(t, int64(len(testContent)), resp.Msg.StorageBytes)
	})
}

func TestServer_ListAgents(t *testing.T) {
	t.Run("empty registry", func(t *testing.T) {
		config := Config{
			ColonyID:        "test-colony",
			ApplicationName: "TestApp",
			Environment:     "test",
		}
		server := newTestServer(t, config)

		req := connect.NewRequest(&colonyv1.ListAgentsRequest{})
		resp, err := server.ListAgents(context.Background(), req)

		require.NoError(t, err)
		assert.Empty(t, resp.Msg.Agents)
	})

	t.Run("multiple agents", func(t *testing.T) {
		config := Config{
			ColonyID:        "test-colony",
			ApplicationName: "TestApp",
			Environment:     "test",
		}
		server := newTestServer(t, config)

		// Register multiple agents.
		_, _ = server.registry.Register("agent-1", "frontend", "100.64.0.2", "fd42::2", nil, nil, "")
		_, _ = server.registry.Register("agent-2", "api", "100.64.0.3", "fd42::3", nil, nil, "")
		_, _ = server.registry.Register("agent-3", "worker", "100.64.0.4", "fd42::4", nil, nil, "")

		req := connect.NewRequest(&colonyv1.ListAgentsRequest{})
		resp, err := server.ListAgents(context.Background(), req)

		require.NoError(t, err)
		assert.Len(t, resp.Msg.Agents, 3)

		// Verify all agents are present.
		agentIDs := make(map[string]bool)
		for _, agent := range resp.Msg.Agents {
			agentIDs[agent.AgentId] = true
			//nolint:staticcheck // ComponentName is deprecated but kept for backward compatibility
			assert.NotEmpty(t, agent.ComponentName)
			assert.NotEmpty(t, agent.Status)
			assert.NotNil(t, agent.LastSeen)
		}
		assert.True(t, agentIDs["agent-1"])
		assert.True(t, agentIDs["agent-2"])
		assert.True(t, agentIDs["agent-3"])
	})

	t.Run("agent status determination", func(t *testing.T) {
		config := Config{
			ColonyID:        "test-colony",
			ApplicationName: "TestApp",
			Environment:     "test",
		}
		server := newTestServer(t, config)

		// Register agents.
		_, _ = server.registry.Register("agent-healthy", "frontend", "100.64.0.2", "fd42::2", nil, nil, "")
		_, _ = server.registry.Register("agent-degraded", "api", "100.64.0.3", "fd42::3", nil, nil, "")
		_, _ = server.registry.Register("agent-unhealthy", "worker", "100.64.0.4", "fd42::4", nil, nil, "")

		// Manually set LastSeen timestamps.
		entries := server.registry.ListAll()
		now := time.Now()
		for _, entry := range entries {
			switch entry.AgentID {
			case "agent-healthy":
				entry.LastSeen = now.Add(-10 * time.Second)
			case "agent-degraded":
				entry.LastSeen = now.Add(-60 * time.Second)
			case "agent-unhealthy":
				entry.LastSeen = now.Add(-5 * time.Minute)
			}
		}

		req := connect.NewRequest(&colonyv1.ListAgentsRequest{})
		resp, err := server.ListAgents(context.Background(), req)

		require.NoError(t, err)
		assert.Len(t, resp.Msg.Agents, 3)

		// Verify status of each agent.
		agentStatuses := make(map[string]string)
		for _, agent := range resp.Msg.Agents {
			agentStatuses[agent.AgentId] = agent.Status
		}
		assert.Equal(t, "healthy", agentStatuses["agent-healthy"])
		assert.Equal(t, "degraded", agentStatuses["agent-degraded"])
		assert.Equal(t, "unhealthy", agentStatuses["agent-unhealthy"])
	})
}

func TestServer_GetTopology(t *testing.T) {
	t.Run("empty topology", func(t *testing.T) {
		config := Config{
			ColonyID:        "test-colony",
			ApplicationName: "TestApp",
			Environment:     "test",
		}
		server := newTestServer(t, config)

		req := connect.NewRequest(&colonyv1.GetTopologyRequest{})
		resp, err := server.GetTopology(context.Background(), req)

		require.NoError(t, err)
		assert.Equal(t, "test-colony", resp.Msg.ColonyId)
		assert.Empty(t, resp.Msg.Agents)
		assert.Empty(t, resp.Msg.Connections)
	})

	t.Run("topology with agents", func(t *testing.T) {
		config := Config{
			ColonyID:        "test-colony",
			ApplicationName: "TestApp",
			Environment:     "production",
		}
		server := newTestServer(t, config)

		// Register agents.
		_, _ = server.registry.Register("agent-1", "frontend", "100.64.0.2", "fd42::2", nil, nil, "")
		_, _ = server.registry.Register("agent-2", "api", "100.64.0.3", "fd42::3", nil, nil, "")

		req := connect.NewRequest(&colonyv1.GetTopologyRequest{})
		resp, err := server.GetTopology(context.Background(), req)

		require.NoError(t, err)
		assert.Equal(t, "test-colony", resp.Msg.ColonyId)
		assert.Len(t, resp.Msg.Agents, 2)
		assert.Empty(t, resp.Msg.Connections) // Connections deferred for future enhancement.

		// Verify agents are present.
		agentIDs := make(map[string]bool)
		for _, agent := range resp.Msg.Agents {
			agentIDs[agent.AgentId] = true
		}
		assert.True(t, agentIDs["agent-1"])
		assert.True(t, agentIDs["agent-2"])
	})
}

func TestServer_determineColonyStatus(t *testing.T) {
	tests := []struct {
		name           string
		setupAgents    func(*registry.Registry)
		expectedStatus string
	}{
		{
			name: "no agents - running",
			setupAgents: func(reg *registry.Registry) {
				// No agents registered.
			},
			expectedStatus: "running",
		},
		{
			name: "all healthy agents",
			setupAgents: func(reg *registry.Registry) {
				_, _ = reg.Register("agent-1", "frontend", "100.64.0.2", "fd42::2", nil, nil, "")
				_, _ = reg.Register("agent-2", "api", "100.64.0.3", "fd42::3", nil, nil, "")
			},
			expectedStatus: "running",
		},
		{
			name: "one degraded agent",
			setupAgents: func(reg *registry.Registry) {
				_, _ = reg.Register("agent-1", "frontend", "100.64.0.2", "fd42::2", nil, nil, "")
				_, _ = reg.Register("agent-2", "api", "100.64.0.3", "fd42::3", nil, nil, "")

				// Make agent-2 degraded.
				entries := reg.ListAll()
				now := time.Now()
				for _, entry := range entries {
					if entry.AgentID == "agent-2" {
						entry.LastSeen = now.Add(-60 * time.Second)
					}
				}
			},
			expectedStatus: "running", // Colony status is decoupled from agent health.
		},
		{
			name: "one unhealthy agent",
			setupAgents: func(reg *registry.Registry) {
				_, _ = reg.Register("agent-1", "frontend", "100.64.0.2", "fd42::2", nil, nil, "")
				_, _ = reg.Register("agent-2", "api", "100.64.0.3", "fd42::3", nil, nil, "")

				// Make agent-2 unhealthy.
				entries := reg.ListAll()
				now := time.Now()
				for _, entry := range entries {
					if entry.AgentID == "agent-2" {
						entry.LastSeen = now.Add(-5 * time.Minute)
					}
				}
			},
			expectedStatus: "running", // Colony status is decoupled from agent health.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := Config{
				ColonyID:        "test-colony",
				ApplicationName: "TestApp",
				Environment:     "test",
			}
			server := newTestServer(t, config)
			tt.setupAgents(server.registry)

			status := server.determineColonyStatus()
			assert.Equal(t, tt.expectedStatus, status)
		})
	}
}
