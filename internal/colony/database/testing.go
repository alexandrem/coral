package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// PopulateTestServices is a test helper that populates the services table
// with test service data. This is useful for tests that need to verify
// service registry functionality.
func PopulateTestServices(t *testing.T, db *Database, services ...*Service) {
	t.Helper()
	ctx := context.Background()

	for _, svc := range services {
		// Set default values if not provided.
		if svc.Status == "" {
			svc.Status = "active"
		}
		if svc.RegisteredAt.IsZero() {
			svc.RegisteredAt = time.Now().Add(-30 * time.Minute)
		}
		if svc.LastSeen.IsZero() {
			svc.LastSeen = time.Now().Add(-5 * time.Minute)
		}

		err := db.UpsertService(ctx, svc)
		require.NoError(t, err, "failed to populate test service: %s", svc.Name)
	}
}
