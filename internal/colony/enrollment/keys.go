package enrollment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// KeyStore tracks the Agent-ID-to-current-WireGuard-key mapping (RFD 109
// Enrollment Processing step 7/8): the record of which public key is
// currently on file for an agent, used to detect key rotation and drive the
// remove-old/add-new peer replacement sequence.
type KeyStore struct {
	db *sql.DB
}

// NewKeyStore creates a KeyStore, ensuring the backing table exists.
func NewKeyStore(db *sql.DB) (*KeyStore, error) {
	k := &KeyStore{db: db}
	if _, err := k.db.Exec(`CREATE TABLE IF NOT EXISTS agent_wireguard_keys (
		agent_id TEXT PRIMARY KEY,
		colony_id TEXT NOT NULL,
		wireguard_pubkey TEXT NOT NULL,
		updated_at TIMESTAMP NOT NULL
	)`); err != nil {
		return nil, fmt.Errorf("failed to create agent_wireguard_keys table: %w", err)
	}
	return k, nil
}

// CurrentPubkey returns the WireGuard public key currently on record for
// agentID, or "" if none.
func (k *KeyStore) CurrentPubkey(ctx context.Context, agentID string) (string, error) {
	var pubkey string
	err := k.db.QueryRowContext(ctx,
		`SELECT wireguard_pubkey FROM agent_wireguard_keys WHERE agent_id = ?`, agentID).Scan(&pubkey)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to look up current pubkey for agent_id=%s: %w", agentID, err)
	}
	return pubkey, nil
}

// SetCurrentPubkey upserts the current WireGuard public key on record for
// agentID. Keyed by agent_id, so a second enrollment attempt for the same
// record_id (or agent) is idempotent.
func (k *KeyStore) SetCurrentPubkey(ctx context.Context, agentID, colonyID, pubkey string) error {
	_, err := k.db.ExecContext(ctx,
		`INSERT INTO agent_wireguard_keys (agent_id, colony_id, wireguard_pubkey, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (agent_id) DO UPDATE SET colony_id = EXCLUDED.colony_id, wireguard_pubkey = EXCLUDED.wireguard_pubkey, updated_at = EXCLUDED.updated_at`,
		agentID, colonyID, pubkey, time.Now())
	if err != nil {
		return fmt.Errorf("failed to update current pubkey for agent_id=%s: %w", agentID, err)
	}
	return nil
}
