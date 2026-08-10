package enrollment

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/stretchr/testify/require"
)

func newTestKeyStore(t *testing.T) *KeyStore {
	t.Helper()
	db, err := sql.Open("duckdb", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	k, err := NewKeyStore(db)
	require.NoError(t, err)
	return k
}

func TestKeyStore_CurrentPubkeyEmptyWhenUnset(t *testing.T) {
	k := newTestKeyStore(t)
	ctx := context.Background()

	pk, err := k.CurrentPubkey(ctx, "agent-1")
	require.NoError(t, err)
	require.Empty(t, pk)
}

func TestKeyStore_SetThenGet(t *testing.T) {
	k := newTestKeyStore(t)
	ctx := context.Background()

	require.NoError(t, k.SetCurrentPubkey(ctx, "agent-1", "colony-1", "pubkey-a"))
	pk, err := k.CurrentPubkey(ctx, "agent-1")
	require.NoError(t, err)
	require.Equal(t, "pubkey-a", pk)
}

func TestKeyStore_RotationOverwrites(t *testing.T) {
	k := newTestKeyStore(t)
	ctx := context.Background()

	require.NoError(t, k.SetCurrentPubkey(ctx, "agent-1", "colony-1", "pubkey-a"))
	require.NoError(t, k.SetCurrentPubkey(ctx, "agent-1", "colony-1", "pubkey-b"))

	pk, err := k.CurrentPubkey(ctx, "agent-1")
	require.NoError(t, err)
	require.Equal(t, "pubkey-b", pk)
}
