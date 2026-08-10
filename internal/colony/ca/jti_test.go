package ca

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/stretchr/testify/require"
)

// setupJTITestDB creates a Manager backed by a temporary DuckDB with the
// consumed_referral_tickets table, for RFD 109 jti consumption tests.
func setupJTITestDB(t *testing.T) *Manager {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := sql.Open("duckdb", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	m := &Manager{db: db}
	require.NoError(t, m.ensureConsumedTicketsTable())
	return m
}

func TestConsumeReferralTicketJTI_FirstConsumeSucceeds(t *testing.T) {
	m := setupJTITestDB(t)
	ctx := context.Background()

	err := m.ConsumeReferralTicketJTI(ctx, "ticket-1", time.Now().Add(time.Hour))
	require.NoError(t, err)

	consumed, err := m.IsReferralTicketConsumed(ctx, "ticket-1")
	require.NoError(t, err)
	require.True(t, consumed)
}

// TestConsumeReferralTicketJTI_SecondConsumeRejected is the RFD 109 test:
// "a second IssueCertificate call for an already-consumed jti is rejected,
// distinct from today's stateless-validation behavior."
func TestConsumeReferralTicketJTI_SecondConsumeRejected(t *testing.T) {
	m := setupJTITestDB(t)
	ctx := context.Background()

	require.NoError(t, m.ConsumeReferralTicketJTI(ctx, "ticket-1", time.Now().Add(time.Hour)))

	err := m.ConsumeReferralTicketJTI(ctx, "ticket-1", time.Now().Add(time.Hour))
	require.Error(t, err)
}

func TestConsumeReferralTicketJTI_EmptyJTIRejected(t *testing.T) {
	m := setupJTITestDB(t)
	ctx := context.Background()

	err := m.ConsumeReferralTicketJTI(ctx, "", time.Now().Add(time.Hour))
	require.Error(t, err)
}

func TestIsReferralTicketConsumed_UnknownJTIFalse(t *testing.T) {
	m := setupJTITestDB(t)
	ctx := context.Background()

	consumed, err := m.IsReferralTicketConsumed(ctx, "never-seen")
	require.NoError(t, err)
	require.False(t, consumed)
}
