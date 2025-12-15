package registry

import (
	"context"
	"testing"
	"time"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistry_PersistenceWithDatabase(t *testing.T) {
	// Create a temporary database
	tempDir := t.TempDir()
	logger := logging.NewWithComponent(logging.Config{
		Level:  "debug",
		Pretty: true,
	}, "test")

	db, err := database.New(tempDir, "test-colony", logger)
	require.NoError(t, err)
	defer db.Close()

	t.Run("register agent with services persists to database", func(t *testing.T) {
		reg := New(db)

		// Register an agent with services
		services := []*meshv1.ServiceInfo{
			{
				Name: "web-service",
				Labels: map[string]string{
					"env":     "production",
					"version": "1.0.0",
				},
			},
			{
				Name: "api-service",
				Labels: map[string]string{
					"env": "production",
				},
			},
		}

		_, err := reg.Register("agent-1", "my-component", "100.64.0.2", "fd42::2", services, nil, "v1.0")
		require.NoError(t, err)

		// Wait for async persistence to complete
		time.Sleep(100 * time.Millisecond)

		// Verify services were persisted to database
		ctx := context.Background()
		dbServices, err := db.ListAllServices(ctx)
		require.NoError(t, err)

		t.Logf("Found %d services in database", len(dbServices))
		for i, svc := range dbServices {
			t.Logf("Service %d: ID=%s, Name=%s, AgentID=%s, Labels=%s", i, svc.ID, svc.Name, svc.AgentID, svc.Labels)
		}

		assert.Len(t, dbServices, 2, "Expected 2 services to be persisted")

		// Verify service details
		serviceNames := make(map[string]bool)
		for _, svc := range dbServices {
			serviceNames[svc.Name] = true
			assert.Equal(t, "agent-1", svc.AgentID)
			assert.Equal(t, "active", svc.Status)
		}
		assert.True(t, serviceNames["web-service"])
		assert.True(t, serviceNames["api-service"])
	})

	t.Run("register legacy component persists to database", func(t *testing.T) {
		// Create a new database for this test
		tempDir2 := t.TempDir()
		db2, err := database.New(tempDir2, "test-colony-2", logger)
		require.NoError(t, err)
		defer db2.Close()

		reg := New(db2)

		// Register a legacy agent (no services, just component name)
		_, err = reg.Register("agent-legacy", "legacy-component", "100.64.0.3", "fd42::3", nil, nil, "v1.0")
		require.NoError(t, err)

		// Wait for async persistence
		time.Sleep(100 * time.Millisecond)

		// Verify legacy component was persisted as a service
		ctx := context.Background()
		dbServices, err := db2.ListAllServices(ctx)
		require.NoError(t, err)

		t.Logf("Found %d services for legacy agent", len(dbServices))
		for i, svc := range dbServices {
			t.Logf("Service %d: ID=%s, Name=%s, AgentID=%s", i, svc.ID, svc.Name, svc.AgentID)
		}

		require.Len(t, dbServices, 1, "Expected 1 service for legacy component")
		assert.Equal(t, "legacy-component", dbServices[0].Name)
		assert.Equal(t, "agent-legacy", dbServices[0].AgentID)
	})

	t.Run("LoadFromDatabase restores services", func(t *testing.T) {
		// Create a new database and registry
		tempDir3 := t.TempDir()
		db3, err := database.New(tempDir3, "test-colony-3", logger)
		require.NoError(t, err)
		defer db3.Close()

		reg1 := New(db3)

		// Register some services
		services := []*meshv1.ServiceInfo{
			{Name: "service-1"},
			{Name: "service-2"},
		}
		_, err = reg1.Register("agent-restore", "test", "100.64.0.5", "", services, nil, "v1.0")
		require.NoError(t, err)

		// Wait for persistence
		time.Sleep(100 * time.Millisecond)

		// Create a new registry (simulating restart)
		reg2 := New(db3)

		// Initially empty
		assert.Equal(t, 0, reg2.Count())

		// Load from database
		err = reg2.LoadFromDatabase(context.Background())
		require.NoError(t, err)

		// Verify services were loaded
		assert.Equal(t, 1, reg2.Count())

		entry, err := reg2.Get("agent-restore")
		require.NoError(t, err)
		assert.Len(t, entry.Services, 2)
		assert.Equal(t, "service-1", entry.Services[0].Name)
		assert.Equal(t, "service-2", entry.Services[1].Name)
	})
}
