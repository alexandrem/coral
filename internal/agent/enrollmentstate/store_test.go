package enrollmentstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coral-mesh/coral/internal/auth"
)

func testKeys(t *testing.T) *auth.WireGuardKeyPair {
	t.Helper()
	keys, err := auth.GenerateWireGuardKeyPair()
	require.NoError(t, err)
	return keys
}

func TestLoad_NotExist(t *testing.T) {
	store := NewStore(t.TempDir(), zerolog.Nop())

	_, err := store.Load()
	require.ErrorIs(t, err, ErrNotExist)
}

func TestSavePendingIdentity_Permissions(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir, zerolog.Nop())
	keys := testKeys(t)

	_, err := store.SavePendingIdentity("agent-1", "colony-1", keys)
	require.NoError(t, err)

	dirInfo, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0700), dirInfo.Mode().Perm())

	fileInfo, err := os.Stat(filepath.Join(dir, CheckpointFileName))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), fileInfo.Mode().Perm())
}

func TestSavePendingIdentity_IdempotentForSameIdentity(t *testing.T) {
	store := NewStore(t.TempDir(), zerolog.Nop())
	keys := testKeys(t)

	first, err := store.SavePendingIdentity("agent-1", "colony-1", keys)
	require.NoError(t, err)

	second, err := store.SavePendingIdentity("agent-1", "colony-1", keys)
	require.NoError(t, err)

	assert.Equal(t, first.WireGuardPrivateKey, second.WireGuardPrivateKey)
	assert.Equal(t, first.WireGuardPublicKey, second.WireGuardPublicKey)
}

func TestSavePendingIdentity_ReplacesDifferentIdentity(t *testing.T) {
	store := NewStore(t.TempDir(), zerolog.Nop())

	_, err := store.SavePendingIdentity("agent-1", "colony-1", testKeys(t))
	require.NoError(t, err)

	newKeys := testKeys(t)
	cp, err := store.SavePendingIdentity("agent-2", "colony-1", newKeys)
	require.NoError(t, err)

	assert.Equal(t, "agent-2", cp.AgentID)
	assert.Equal(t, newKeys.PublicKey, cp.WireGuardPublicKey)
}

func TestCommitEnrollment_RoundTrip(t *testing.T) {
	store := NewStore(t.TempDir(), zerolog.Nop())
	keys := testKeys(t)

	_, err := store.SavePendingIdentity("agent-1", "colony-1", keys)
	require.NoError(t, err)

	committed, err := store.CommitEnrollment("agent-1", "colony-1", keys.PublicKey, "100.64.0.5", "100.64.0.0/10", "deadbeef", "12345")
	require.NoError(t, err)
	assert.Equal(t, StateEnrolled, committed.State)
	assert.False(t, committed.EnrolledAt.IsZero())

	loaded, err := store.Load()
	require.NoError(t, err)
	assert.Equal(t, StateEnrolled, loaded.State)
	assert.Equal(t, "agent-1", loaded.AgentID)
	assert.Equal(t, "colony-1", loaded.ColonyID)
	assert.Equal(t, keys.PrivateKey, loaded.WireGuardPrivateKey)
	assert.Equal(t, keys.PublicKey, loaded.WireGuardPublicKey)
	assert.Equal(t, "100.64.0.5", loaded.AssignedIP)
	assert.Equal(t, "100.64.0.0/10", loaded.MeshSubnet)
	assert.Equal(t, "deadbeef", loaded.CertificateSHA256)
	assert.Equal(t, "12345", loaded.CertificateSerial)
}

func TestCommitEnrollment_RequiresMatchingPendingCheckpoint(t *testing.T) {
	t.Run("no pending checkpoint", func(t *testing.T) {
		store := NewStore(t.TempDir(), zerolog.Nop())
		_, err := store.CommitEnrollment("agent-1", "colony-1", "pubkey", "100.64.0.5", "100.64.0.0/10", "sha", "serial")
		require.Error(t, err)
	})

	t.Run("identity mismatch", func(t *testing.T) {
		store := NewStore(t.TempDir(), zerolog.Nop())
		keys := testKeys(t)
		_, err := store.SavePendingIdentity("agent-1", "colony-1", keys)
		require.NoError(t, err)

		_, err = store.CommitEnrollment("agent-2", "colony-1", keys.PublicKey, "100.64.0.5", "100.64.0.0/10", "sha", "serial")
		require.Error(t, err)
	})

	t.Run("wireguard key mismatch", func(t *testing.T) {
		store := NewStore(t.TempDir(), zerolog.Nop())
		keys := testKeys(t)
		_, err := store.SavePendingIdentity("agent-1", "colony-1", keys)
		require.NoError(t, err)

		_, err = store.CommitEnrollment("agent-1", "colony-1", "not-the-registered-key", "100.64.0.5", "100.64.0.0/10", "sha", "serial")
		require.Error(t, err)
	})
}

func TestLoad_CorruptAndUnsupportedVersion(t *testing.T) {
	t.Run("truncated json", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, CheckpointFileName), []byte("{not json"), 0600))

		store := NewStore(dir, zerolog.Nop())
		_, err := store.Load()
		require.ErrorIs(t, err, ErrInvalid)
	})

	t.Run("unsupported schema version", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, CheckpointFileName), []byte(`{"schema_version":99,"agent_id":"a","colony_id":"c","wireguard_private_key":"x","wireguard_public_key":"y"}`), 0600))

		store := NewStore(dir, zerolog.Nop())
		_, err := store.Load()
		require.ErrorIs(t, err, ErrInvalid)
	})

	t.Run("missing required fields", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, CheckpointFileName), []byte(`{"schema_version":1}`), 0600))

		store := NewStore(dir, zerolog.Nop())
		_, err := store.Load()
		require.ErrorIs(t, err, ErrInvalid)
	})
}

func TestArchiveIncomplete(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir, zerolog.Nop())
	keys := testKeys(t)

	_, err := store.SavePendingIdentity("agent-1", "colony-1", keys)
	require.NoError(t, err)

	require.NoError(t, store.ArchiveIncomplete("test_reason"))

	// The key remains pending, including if recovery is interrupted here.
	pending, err := store.Load()
	require.NoError(t, err)
	assert.Equal(t, StatePending, pending.State)
	assert.Equal(t, keys.PrivateKey, pending.WireGuardPrivateKey)

	// But it was moved, not deleted.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var foundRecoveryDir bool
	for _, e := range entries {
		if e.IsDir() {
			foundRecoveryDir = true
			archived, err := os.ReadDir(filepath.Join(dir, e.Name()))
			require.NoError(t, err)
			require.Len(t, archived, 1)
			assert.Equal(t, CheckpointFileName, archived[0].Name())
		}
	}
	assert.True(t, foundRecoveryDir, "expected a recovery directory to be created")
}

func TestArchiveIncomplete_NoCheckpointIsNoop(t *testing.T) {
	store := NewStore(t.TempDir(), zerolog.Nop())
	require.NoError(t, store.ArchiveIncomplete("nothing_to_archive"))
}

func TestSavePendingIdentity_ReusableAcrossRetries(t *testing.T) {
	// Simulates a crash after the pending identity was persisted but before
	// compound enrollment completed: the next attempt must reuse the same key.
	store := NewStore(t.TempDir(), zerolog.Nop())
	keys := testKeys(t)

	_, err := store.SavePendingIdentity("agent-1", "colony-1", keys)
	require.NoError(t, err)

	loaded, err := store.Load()
	require.NoError(t, err)
	assert.Equal(t, keys.PrivateKey, loaded.WireGuardPrivateKey)
	assert.Equal(t, keys.PublicKey, loaded.WireGuardPublicKey)
	assert.Equal(t, StatePending, loaded.State)
}

func TestLoadRejectsCorruptIdentity(t *testing.T) {
	for _, mutation := range []string{"private key", "public key", "key mismatch", "state"} {
		t.Run(mutation, func(t *testing.T) {
			dir := t.TempDir()
			store := NewStore(dir, zerolog.Nop())
			cp, err := store.SavePendingIdentity("agent-1", "colony-1", testKeys(t))
			require.NoError(t, err)
			switch mutation {
			case "private key":
				cp.WireGuardPrivateKey = "truncated"
			case "public key":
				cp.WireGuardPublicKey = "invalid"
			case "key mismatch":
				cp.WireGuardPublicKey = testKeys(t).PublicKey
			case "state":
				cp.State = "unknown"
			}
			data, err := json.Marshal(cp)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(store.path(), data, 0600))
			_, err = store.Load()
			require.ErrorIs(t, err, ErrInvalid)
			require.NoError(t, store.ArchiveIncomplete("invalid"))
			_, err = store.Load()
			require.ErrorIs(t, err, ErrNotExist)
		})
	}
}

func TestCommitEnrollmentRejectsInvalidAssignment(t *testing.T) {
	for _, assignment := range []struct{ ip, subnet string }{
		{"", "100.64.0.0/10"}, {"100.64.0.5", "invalid"}, {"192.0.2.1", "100.64.0.0/10"},
	} {
		t.Run(assignment.ip+"/"+assignment.subnet, func(t *testing.T) {
			store := NewStore(t.TempDir(), zerolog.Nop())
			keys := testKeys(t)
			_, err := store.SavePendingIdentity("a", "c", keys)
			require.NoError(t, err)
			_, err = store.CommitEnrollment("a", "c", keys.PublicKey, assignment.ip, assignment.subnet, "sha", "serial")
			require.Error(t, err)
			cp, err := store.Load()
			require.NoError(t, err)
			assert.Equal(t, StatePending, cp.State)
		})
	}
}

func TestLoadIgnoresUncommittedManifest(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir, zerolog.Nop())
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".tmp-enrollment.json-interrupted"), []byte(`{"state":"enrolled"}`), 0600))
	_, err := store.Load()
	require.ErrorIs(t, err, ErrNotExist)
}

func TestArchivePreservesIdentityAndOriginalCommit(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir, zerolog.Nop())
	keys := testKeys(t)
	_, err := store.SavePendingIdentity("a", "c", keys)
	require.NoError(t, err)
	_, err = store.CommitEnrollment("a", "c", keys.PublicKey, "100.64.0.5", "100.64.0.0/10", "sha", "serial")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.key"), []byte("private"), 0600))
	require.NoError(t, store.ArchiveIncomplete("interrupted", "agent.key"))
	cp, err := store.Load()
	require.NoError(t, err)
	assert.Equal(t, StatePending, cp.State)
	assert.Equal(t, keys.PrivateKey, cp.WireGuardPrivateKey)
	assert.Empty(t, cp.AssignedIP)
	archives, err := filepath.Glob(filepath.Join(dir, "recovery-*"))
	require.NoError(t, err)
	require.Len(t, archives, 1)
	archived, err := NewStore(archives[0], zerolog.Nop()).Load()
	require.NoError(t, err)
	assert.Equal(t, StateEnrolled, archived.State)
	assert.FileExists(t, filepath.Join(archives[0], "agent.key"))
	// A second recovery in the same second must not overwrite the archive.
	require.NoError(t, store.ArchiveIncomplete("retry"))
	archives, err = filepath.Glob(filepath.Join(dir, "recovery-*"))
	require.NoError(t, err)
	require.Len(t, archives, 2)
}
