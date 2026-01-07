package database

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/coral-mesh/coral/internal/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpsertService(t *testing.T) {
	tempDir := t.TempDir()
	logger := logging.NewWithComponent(logging.Config{Level: "debug", Pretty: true}, "test")
	db, err := New(tempDir, "test-colony", logger)
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()

	t.Run("insert new service", func(t *testing.T) {
		service := &Service{
			ID:       "agent-1:web-service",
			Name:     "web-service",
			AppID:    "web-app",
			Version:  "1.0.0",
			AgentID:  "agent-1",
			Labels:   `{"env":"production"}`,
			LastSeen: time.Now(),
			Status:   "active",
		}

		err := db.UpsertService(ctx, service)
		require.NoError(t, err)

		// Verify it was inserted
		retrieved, err := db.GetServiceByName(ctx, "web-service")
		require.NoError(t, err)
		require.NotNil(t, retrieved)
		assert.Equal(t, "web-service", retrieved.Name)
		assert.Equal(t, "agent-1", retrieved.AgentID)
		assert.Equal(t, "active", retrieved.Status)
	})

	t.Run("update existing service", func(t *testing.T) {
		// Insert initial service
		service := &Service{
			ID:       "agent-2:api-service",
			Name:     "api-service",
			AppID:    "api-app",
			Version:  "1.0.0",
			AgentID:  "agent-2",
			Labels:   `{"env":"staging"}`,
			LastSeen: time.Now(),
			Status:   "active",
		}

		err := db.UpsertService(ctx, service)
		require.NoError(t, err)

		// Update the same service
		updatedTime := time.Now().Add(1 * time.Minute)
		service.Version = "2.0.0"
		service.LastSeen = updatedTime
		service.Labels = `{"env":"production"}`

		err = db.UpsertService(ctx, service)
		require.NoError(t, err)

		// Verify it was updated
		retrieved, err := db.GetServiceByName(ctx, "api-service")
		require.NoError(t, err)
		require.NotNil(t, retrieved)
		assert.Equal(t, "2.0.0", retrieved.Version)
		assert.Equal(t, `{"env":"production"}`, retrieved.Labels)
		// LastSeen should be updated
		assert.True(t, retrieved.LastSeen.After(service.LastSeen.Add(-2*time.Second)))
	})

	t.Run("upsert service with indexed columns", func(t *testing.T) {
		// This test specifically validates that we can update columns that have indexes
		// (agent_id, status, last_seen) - this was the bug we fixed
		service := &Service{
			ID:       "agent-3:worker-service",
			Name:     "worker-service",
			AppID:    "worker-app",
			Version:  "1.0.0",
			AgentID:  "agent-3",
			Labels:   "{}",
			LastSeen: time.Now(),
			Status:   "active",
		}

		err := db.UpsertService(ctx, service)
		require.NoError(t, err)

		// Update with new status and last_seen (both are indexed columns)
		newLastSeen := time.Now().Add(5 * time.Minute)
		service.Status = "degraded"
		service.LastSeen = newLastSeen

		err = db.UpsertService(ctx, service)
		require.NoError(t, err, "Should be able to update indexed columns (status, last_seen)")

		// Verify the update worked
		retrieved, err := db.GetServiceByName(ctx, "worker-service")
		require.NoError(t, err)
		require.NotNil(t, retrieved)
		assert.Equal(t, "degraded", retrieved.Status)
	})

	t.Run("upsert service with zero LastSeen initializes to now", func(t *testing.T) {
		// Regression test: verify that when LastSeen is zero (time.Time{}),
		// UpsertService initializes it to time.Now() to prevent epoch zero timestamps.
		service := &Service{
			ID:      "agent-zero:test-service",
			Name:    "test-service",
			AppID:   "test-app",
			Version: "1.0.0",
			AgentID: "agent-zero",
			Labels:  "{}",
			// LastSeen intentionally not set (zero value)
			Status: "active",
		}

		// Verify LastSeen is zero before upsert.
		assert.True(t, service.LastSeen.IsZero())

		err := db.UpsertService(ctx, service)
		require.NoError(t, err)

		// Retrieve and verify LastSeen was initialized to a non-zero time.
		retrieved, err := db.GetServiceByName(ctx, "test-service")
		require.NoError(t, err)
		require.NotNil(t, retrieved)
		assert.False(t, retrieved.LastSeen.IsZero(), "LastSeen should be initialized to non-zero time")
		assert.True(t, retrieved.LastSeen.After(time.Now().Add(-1*time.Minute)), "LastSeen should be recent")
	})
}

func TestGetServiceByName(t *testing.T) {
	tempDir := t.TempDir()
	logger := logging.NewWithComponent(logging.Config{Level: "debug", Pretty: true}, "test")
	db, err := New(tempDir, "test-colony", logger)
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()

	t.Run("service not found", func(t *testing.T) {
		service, err := db.GetServiceByName(ctx, "nonexistent-service")
		require.NoError(t, err)
		assert.Nil(t, service)
	})

	t.Run("get existing service", func(t *testing.T) {
		// Insert a service
		service := &Service{
			ID:       "agent-4:cache-service",
			Name:     "cache-service",
			AppID:    "cache-app",
			Version:  "1.0.0",
			AgentID:  "agent-4",
			Labels:   `{"type":"redis"}`,
			LastSeen: time.Now(),
			Status:   "active",
		}

		err := db.UpsertService(ctx, service)
		require.NoError(t, err)

		// Retrieve it
		retrieved, err := db.GetServiceByName(ctx, "cache-service")
		require.NoError(t, err)
		require.NotNil(t, retrieved)
		assert.Equal(t, "cache-service", retrieved.Name)
		assert.Equal(t, "agent-4", retrieved.AgentID)
		assert.Equal(t, `{"type":"redis"}`, retrieved.Labels)
	})

	t.Run("multiple services with same name returns most recent", func(t *testing.T) {
		// Insert first instance
		service1 := &Service{
			ID:       "agent-5:db-service",
			Name:     "db-service",
			AppID:    "db-app",
			Version:  "1.0.0",
			AgentID:  "agent-5",
			Labels:   "{}",
			LastSeen: time.Now().Add(-1 * time.Hour),
			Status:   "active",
		}
		err := db.UpsertService(ctx, service1)
		require.NoError(t, err)

		// Insert second instance with same name but different agent (more recent)
		service2 := &Service{
			ID:       "agent-6:db-service",
			Name:     "db-service",
			AppID:    "db-app",
			Version:  "1.0.0",
			AgentID:  "agent-6",
			Labels:   "{}",
			LastSeen: time.Now(),
			Status:   "active",
		}
		err = db.UpsertService(ctx, service2)
		require.NoError(t, err)

		// Should return the most recent one (agent-6)
		retrieved, err := db.GetServiceByName(ctx, "db-service")
		require.NoError(t, err)
		require.NotNil(t, retrieved)
		assert.Equal(t, "agent-6", retrieved.AgentID, "Should return most recently seen instance")
	})
}

func TestListAllServices(t *testing.T) {
	tempDir := t.TempDir()
	logger := logging.NewWithComponent(logging.Config{Level: "debug", Pretty: true}, "test")
	db, err := New(tempDir, "test-colony", logger)
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()

	t.Run("empty database", func(t *testing.T) {
		services, err := db.ListAllServices(ctx)
		require.NoError(t, err)
		assert.Empty(t, services)
	})

	t.Run("list all services", func(t *testing.T) {
		// Insert multiple services
		services := []*Service{
			{
				ID:       "agent-7:web",
				Name:     "web",
				AppID:    "web-app",
				Version:  "1.0.0",
				AgentID:  "agent-7",
				Labels:   "{}",
				LastSeen: time.Now(),
				Status:   "active",
			},
			{
				ID:       "agent-7:api",
				Name:     "api",
				AppID:    "api-app",
				Version:  "1.0.0",
				AgentID:  "agent-7",
				Labels:   "{}",
				LastSeen: time.Now(),
				Status:   "active",
			},
			{
				ID:       "agent-8:worker",
				Name:     "worker",
				AppID:    "worker-app",
				Version:  "1.0.0",
				AgentID:  "agent-8",
				Labels:   "{}",
				LastSeen: time.Now(),
				Status:   "active",
			},
		}

		for _, svc := range services {
			err := db.UpsertService(ctx, svc)
			require.NoError(t, err)
		}

		// List all
		retrieved, err := db.ListAllServices(ctx)
		require.NoError(t, err)
		assert.Len(t, retrieved, 3)

		// Verify they're ordered by agent_id, name
		assert.Equal(t, "agent-7", retrieved[0].AgentID)
		assert.Equal(t, "api", retrieved[0].Name)
		assert.Equal(t, "agent-7", retrieved[1].AgentID)
		assert.Equal(t, "web", retrieved[1].Name)
		assert.Equal(t, "agent-8", retrieved[2].AgentID)
	})
}

// TestUpdateServiceLastSeen tests bulk updating last_seen for all services of an agent.
func TestUpdateServiceLastSeen(t *testing.T) {
	tempDir := t.TempDir()
	logger := logging.NewWithComponent(logging.Config{Level: "debug", Pretty: true}, "test")
	db, err := New(tempDir, "test-colony", logger)
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()

	// Insert multiple services for the same agent.
	services := []*Service{
		{
			ID:       "agent-update:service-1",
			Name:     "service-1",
			AppID:    "app-1",
			Version:  "1.0.0",
			AgentID:  "agent-update",
			Labels:   "{}",
			LastSeen: time.Now().Add(-1 * time.Hour),
			Status:   "active",
		},
		{
			ID:       "agent-update:service-2",
			Name:     "service-2",
			AppID:    "app-2",
			Version:  "1.0.0",
			AgentID:  "agent-update",
			Labels:   "{}",
			LastSeen: time.Now().Add(-1 * time.Hour),
			Status:   "active",
		},
	}

	for _, svc := range services {
		err := db.UpsertService(ctx, svc)
		require.NoError(t, err)
	}

	// Update last_seen for all services of this agent.
	newLastSeen := time.Now()
	err = db.UpdateServiceLastSeen(ctx, "agent-update", newLastSeen)
	require.NoError(t, err)

	// Verify both services were updated.
	retrieved1, err := db.GetServiceByName(ctx, "service-1")
	require.NoError(t, err)
	require.NotNil(t, retrieved1)
	assert.True(t, retrieved1.LastSeen.After(newLastSeen.Add(-2*time.Second)), "service-1 last_seen should be updated")

	retrieved2, err := db.GetServiceByName(ctx, "service-2")
	require.NoError(t, err)
	require.NotNil(t, retrieved2)
	assert.True(t, retrieved2.LastSeen.After(newLastSeen.Add(-2*time.Second)), "service-2 last_seen should be updated")
}

func TestServicePersistenceRegression(t *testing.T) {
	// This is a regression test for the service persistence refactoring.
	// Ensures that UpsertService correctly handles updates to all fields including
	// previously problematic indexed columns (status, agent_id).
	// With the new two-table design, this should work seamlessly.

	tempDir := t.TempDir()
	logger := logging.NewWithComponent(logging.Config{Level: "debug", Pretty: true}, "test")
	db, err := New(tempDir, "test-colony-regression", logger)
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()

	t.Run("regression: upsert must work with indexed columns", func(t *testing.T) {
		service := &Service{
			ID:       "regression-agent:regression-service",
			Name:     "regression-service",
			AppID:    "regression-app",
			Version:  "1.0.0",
			AgentID:  "regression-agent", // agent_id has an index
			Labels:   "{}",
			LastSeen: time.Now(), // last_seen has an index
			Status:   "active",   // status has an index
		}

		// First insert should work
		err := db.UpsertService(ctx, service)
		require.NoError(t, err, "Initial insert should succeed")

		// Update all indexed columns
		service.AgentID = "regression-agent" // Same agent (but in real world this stays the same due to ID format)
		service.LastSeen = time.Now().Add(1 * time.Minute)
		service.Status = "degraded"

		// Second upsert should work (this failed before the fix)
		err = db.UpsertService(ctx, service)
		require.NoError(t, err, "Upsert with indexed column updates should succeed - if this fails, the ON CONFLICT DO UPDATE bug has returned")

		// Verify the update
		retrieved, err := db.GetServiceByName(ctx, "regression-service")
		require.NoError(t, err)
		require.NotNil(t, retrieved)
		assert.Equal(t, "degraded", retrieved.Status)
	})
}

// TestConcurrentServiceUpserts tests that concurrent upserts to the same service don't cause errors.
// This is a regression test for the duplicate key constraint errors that occurred with the
// UPDATE-first pattern when multiple goroutines tried to upsert the same service simultaneously.
func TestConcurrentServiceUpserts(t *testing.T) {
	tempDir := t.TempDir()
	logger := logging.NewWithComponent(logging.Config{Level: "warn", Pretty: true}, "test")
	db, err := New(tempDir, "test-colony", logger)
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()

	// Use a WaitGroup to synchronize goroutines.
	// Use realistic concurrency: 5 concurrent agents, each upserting 5 times.
	// This simulates multiple agents sending heartbeats at similar times.
	var wg sync.WaitGroup
	const numGoroutines = 5
	const numUpserts = 5

	// Track errors from goroutines.
	errChan := make(chan error, numGoroutines*numUpserts)

	// Launch multiple goroutines that all try to upsert the same service.
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			for j := 0; j < numUpserts; j++ {
				service := &Service{
					ID:       "concurrent-agent:concurrent-service",
					Name:     "concurrent-service",
					AppID:    "concurrent-app",
					Version:  "1.0.0",
					AgentID:  "concurrent-agent",
					Labels:   "{}",
					LastSeen: time.Now(),
					Status:   "active",
				}

				if err := db.UpsertService(ctx, service); err != nil {
					errChan <- err
				}
			}
		}(i)
	}

	// Wait for all goroutines to complete.
	wg.Wait()
	close(errChan)

	// Check if any errors occurred.
	var errors []error
	for err := range errChan {
		errors = append(errors, err)
	}

	// All upserts should succeed without errors.
	require.Empty(t, errors, "Concurrent upserts should not produce errors. Got: %v", errors)

	// Verify the service exists and can be retrieved.
	retrieved, err := db.GetServiceByName(ctx, "concurrent-service")
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	assert.Equal(t, "concurrent-service", retrieved.Name)
	assert.Equal(t, "concurrent-agent", retrieved.AgentID)
}
