package enrollment

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("duckdb", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	s, err := NewStore(db)
	require.NoError(t, err)
	return s
}

func TestClaim_FreshRowInsertsClaimed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	row, outcome, err := s.Claim(ctx, "rec-1", "owner-a", DefaultLease)
	require.NoError(t, err)
	require.Equal(t, OutcomeOwned, outcome)
	require.Equal(t, PhaseClaimed, row.Phase)
	require.Equal(t, "owner-a", row.OwnerID)
}

func TestClaim_CompletedRowReplays(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, _, err := s.Claim(ctx, "rec-1", "owner-a", DefaultLease)
	require.NoError(t, err)
	require.NoError(t, s.SetAuthorized(ctx, "rec-1", "owner-a", "agent-1", "colony-1", "jti-1", time.Now().Add(time.Hour), "hash-1", DefaultLease))
	require.NoError(t, s.SetCompleted(ctx, "rec-1", "owner-a", []byte("cert"), []byte("chain"), time.Now().Add(time.Hour), []byte("resp")))

	row, outcome, err := s.Claim(ctx, "rec-1", "owner-b", DefaultLease)
	require.NoError(t, err)
	require.Equal(t, OutcomeReplay, outcome)
	require.Equal(t, PhaseCompleted, row.Phase)
	require.Equal(t, []byte("cert"), row.CertificatePEM)
	require.Equal(t, []byte("resp"), row.RegisterResponse)
}

func TestListCompletedReturnsOnlyCompletedEnrollments(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, _, err := s.Claim(ctx, "completed", "owner", DefaultLease)
	require.NoError(t, err)
	require.NoError(t, s.SetAuthorized(ctx, "completed", "owner", "agent-1", "colony-1", "jti", time.Now().Add(time.Hour), "hash", DefaultLease))
	require.NoError(t, s.SetIPAllocated(ctx, "completed", "owner", "203.0.113.1:51820", "100.64.0.2", "", "pubkey-1", DefaultLease))
	require.NoError(t, s.SetCompleted(ctx, "completed", "owner", nil, nil, time.Now().Add(time.Hour), nil))

	_, _, err = s.Claim(ctx, "pending", "owner", DefaultLease)
	require.NoError(t, err)

	rows, err := s.ListCompleted(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "completed", rows[0].RecordID)
	require.Equal(t, "agent-1", rows[0].AgentID)
	require.Equal(t, "100.64.0.2", rows[0].AllocatedIP)
	require.Equal(t, "pubkey-1", rows[0].NewPubkey)
}

func TestClaim_LiveLeaseWaits(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, _, err := s.Claim(ctx, "rec-1", "owner-a", time.Minute)
	require.NoError(t, err)

	_, outcome, err := s.Claim(ctx, "rec-1", "owner-b", time.Minute)
	require.NoError(t, err)
	require.Equal(t, OutcomeWait, outcome)
}

// TestClaim_ExpiredClaimedRowIsDeletedNotResumed is the RFD 109 regression
// test: a row stuck in Claimed phase (owner crashed before validation
// completed) must be deleted and restarted from validation, never advanced
// directly to a later phase.
func TestClaim_ExpiredClaimedRowIsDeletedNotResumed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, _, err := s.Claim(ctx, "rec-1", "owner-a", 1*time.Millisecond)
	require.NoError(t, err)
	time.Sleep(5 * time.Millisecond)

	row, outcome, err := s.Claim(ctx, "rec-1", "owner-b", DefaultLease)
	require.NoError(t, err)
	require.Equal(t, OutcomeOwned, outcome)
	require.Equal(t, PhaseClaimed, row.Phase, "must restart from Claimed, never resume an unauthorized row")
	require.Equal(t, "owner-b", row.OwnerID)
}

// TestClaim_ExpiredAuthorizedLeaseIsStolenAndResumed covers the
// post-authorization crash-recovery path: lease steal must resume from the
// exact persisted phase, not from Claimed.
func TestClaim_ExpiredAuthorizedLeaseIsStolenAndResumed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, _, err := s.Claim(ctx, "rec-1", "owner-a", 1*time.Millisecond)
	require.NoError(t, err)
	require.NoError(t, s.SetAuthorized(ctx, "rec-1", "owner-a", "agent-1", "colony-1", "jti-1", time.Now().Add(time.Hour), "hash-1", 1*time.Millisecond))
	require.NoError(t, s.SetIPAllocated(ctx, "rec-1", "owner-a", "1.2.3.4:51820", "100.64.0.5", "", "pubkey-new", 1*time.Millisecond))
	time.Sleep(5 * time.Millisecond)

	row, outcome, err := s.Claim(ctx, "rec-1", "owner-b", DefaultLease)
	require.NoError(t, err)
	require.Equal(t, OutcomeOwned, outcome)
	require.Equal(t, PhaseIPAllocated, row.Phase)
	require.Equal(t, "owner-b", row.OwnerID)
	require.Equal(t, "100.64.0.5", row.AllocatedIP)
	require.Equal(t, "pubkey-new", row.NewPubkey)
}

// TestClaim_ConcurrentInsertOnlyOneWinner simulates the dial-loop race:
// two goroutines racing to claim the same record_id — exactly one must win
// the insert and proceed; the other observes it (Wait or, once completed,
// Replay).
func TestClaim_ConcurrentInsertOnlyOneWinner(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const n = 8
	var wg sync.WaitGroup
	owned := make([]bool, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, outcome, err := s.Claim(ctx, "rec-race", "owner", time.Minute)
			require.NoError(t, err)
			owned[i] = outcome == OutcomeOwned
		}(i)
	}
	wg.Wait()

	winners := 0
	for _, o := range owned {
		if o {
			winners++
		}
	}
	require.Equal(t, 1, winners, "exactly one caller should win the atomic insert")
}

func TestSetPhase_LostOwnershipErrors(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, _, err := s.Claim(ctx, "rec-1", "owner-a", DefaultLease)
	require.NoError(t, err)

	err = s.SetPhase(ctx, "rec-1", "wrong-owner", PhaseAuthorized, DefaultLease)
	require.Error(t, err)
}

func TestDelete_RemovesOwnedRow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, _, err := s.Claim(ctx, "rec-1", "owner-a", DefaultLease)
	require.NoError(t, err)
	require.NoError(t, s.Delete(ctx, "rec-1", "owner-a"))

	_, err = s.Get(ctx, "rec-1")
	require.ErrorIs(t, err, ErrNotFound)
}
