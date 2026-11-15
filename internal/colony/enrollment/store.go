// Package enrollment implements the durable, record_id-keyed state machine
// that backs RFD 109's BootstrapAndRegister RPC: it is the actual
// idempotency mechanism for compound certificate issuance + mesh
// registration over an RFD 108 rendezvous connection, tolerating both
// concurrent deliveries of the same record_id and a Colony restart
// mid-enrollment.
package enrollment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	duckdb "github.com/marcboeker/go-duckdb"
)

// Phase is a step in the enrollment state machine. Only "authorized" and
// later phases represent a cryptographically validated attempt; a row that
// never reaches Authorized is deleted, never resumed (see Store.Claim).
type Phase string

const (
	PhaseClaimed         Phase = "claimed"
	PhaseAuthorized      Phase = "authorized"
	PhaseIPAllocated     Phase = "ip_allocated"
	PhaseOldPeerRemoved  Phase = "old_peer_removed"
	PhaseNewPeerAdded    Phase = "new_peer_added"
	PhaseRegistryUpdated Phase = "registry_updated"
	PhaseCompleted       Phase = "completed"
)

// DefaultLease is how long a caller owns a row before another caller may
// steal it (only permitted from Authorized or later — see Store.Claim).
const DefaultLease = 30 * time.Second

// ErrNotFound is returned by Get when no row exists for a record_id.
var ErrNotFound = errors.New("enrollment: no row for record_id")

// Row is one enrollment-state record, keyed by the RFD 108 rendezvous
// record_id. It is the source of truth for how far a bootstrap attempt has
// progressed, not just a cache.
type Row struct {
	RecordID       string
	Phase          Phase
	OwnerID        string
	LeaseExpiresAt time.Time

	AgentID         string
	ColonyID        string
	TicketJTI       string
	TicketExpiresAt time.Time
	RequestHash     string

	ResolvedEndpoint string
	AllocatedIP      string
	OldPubkey        string // empty if no prior peer.
	NewPubkey        string

	CertificatePEM   []byte
	CAChain          []byte
	CertExpiresAt    time.Time
	RegisterResponse []byte // marshaled mesh.v1.RegisterResponse, set at Completed.

	LastError string
}

// Outcome describes what a caller should do after Claim.
type Outcome int

const (
	// OutcomeOwned means the caller holds the lease and should proceed from
	// Row.Phase (Claimed for a fresh/restarted attempt, or a later phase if
	// a lease was stolen after a crash).
	OutcomeOwned Outcome = iota
	// OutcomeReplay means the row is Completed; replay its stored
	// certificate and RegisterResponse verbatim, no re-validation.
	OutcomeReplay
	// OutcomeWait means another caller holds a live lease; poll
	// WaitForCompletion or retry Claim after the lease would expire.
	OutcomeWait
)

// Store persists enrollment state in the Colony's DuckDB database.
type Store struct {
	db *sql.DB
}

// NewStore creates a Store, ensuring the backing table exists.
func NewStore(db *sql.DB) (*Store, error) {
	s := &Store{db: db}
	if err := s.ensureTable(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) ensureTable() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS rendezvous_enrollment_state (
		record_id TEXT PRIMARY KEY,
		phase TEXT NOT NULL,
		owner_id TEXT NOT NULL,
		lease_expires_at TIMESTAMP NOT NULL,
		created_at TIMESTAMP NOT NULL,
		updated_at TIMESTAMP NOT NULL,
		agent_id TEXT NOT NULL DEFAULT '',
		colony_id TEXT NOT NULL DEFAULT '',
		ticket_jti TEXT NOT NULL DEFAULT '',
		ticket_expires_at TIMESTAMP,
		request_hash TEXT NOT NULL DEFAULT '',
		resolved_endpoint TEXT NOT NULL DEFAULT '',
		allocated_ip TEXT NOT NULL DEFAULT '',
		old_pubkey TEXT NOT NULL DEFAULT '',
		new_pubkey TEXT NOT NULL DEFAULT '',
		certificate_pem BLOB,
		ca_chain BLOB,
		cert_expires_at TIMESTAMP,
		register_response BLOB,
		last_error TEXT NOT NULL DEFAULT ''
	)`)
	if err != nil {
		return fmt.Errorf("failed to create rendezvous_enrollment_state table: %w", err)
	}
	return nil
}

// Claim atomically claims or adopts the enrollment-state row for recordID.
// See package doc and RFD 109 "Enrollment state: concurrency and restart
// semantics" for the exact rules this implements:
//
//   - No row exists: insert a fresh Claimed row owned by ownerID.
//   - A Completed row exists: return it for replay (OutcomeReplay).
//   - A row with a live lease exists (any phase): OutcomeWait.
//   - A row in Claimed phase with an expired lease: its owner crashed
//     before validation ever completed. Delete it (never resume an
//     unauthorized row) and insert a fresh Claimed row for ownerID.
//   - A row in Authorized-or-later phase with an expired lease: steal the
//     lease (atomic compare-and-swap) and resume from that exact phase.
func (s *Store) Claim(ctx context.Context, recordID, ownerID string, lease time.Duration) (*Row, Outcome, error) {
	for range 20 {
		row, outcome, retry, err := s.tryClaim(ctx, recordID, ownerID, lease)
		if err != nil {
			return nil, 0, err
		}
		if retry {
			continue
		}
		return row, outcome, nil
	}
	return nil, 0, fmt.Errorf("enrollment: too much contention claiming record_id=%s", recordID)
}

func (s *Store) tryClaim(ctx context.Context, recordID, ownerID string, lease time.Duration) (row *Row, outcome Outcome, retry bool, err error) {
	now := time.Now()
	leaseExpiresAt := now.Add(lease)

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO rendezvous_enrollment_state (record_id, phase, owner_id, lease_expires_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		recordID, string(PhaseClaimed), ownerID, leaseExpiresAt, now, now)
	if err == nil {
		return &Row{
			RecordID:       recordID,
			Phase:          PhaseClaimed,
			OwnerID:        ownerID,
			LeaseExpiresAt: leaseExpiresAt,
		}, OutcomeOwned, false, nil
	}
	if !isConstraintViolation(err) {
		return nil, 0, false, fmt.Errorf("enrollment: failed to insert claim: %w", err)
	}

	existing, err := s.Get(ctx, recordID)
	if errors.Is(err, ErrNotFound) {
		// Raced with a delete between the insert attempt and this select.
		return nil, 0, true, nil
	}
	if err != nil {
		return nil, 0, false, err
	}

	if existing.Phase == PhaseCompleted {
		return existing, OutcomeReplay, false, nil
	}

	if existing.LeaseExpiresAt.After(now) {
		return existing, OutcomeWait, false, nil
	}

	// Lease expired. Behavior depends entirely on which side of Authorized
	// the row is on (RFD 109 Security Model: an unauthenticated Claimed row
	// is never treated as authorization).
	if existing.Phase == PhaseClaimed {
		delRes, err := s.db.ExecContext(ctx,
			`DELETE FROM rendezvous_enrollment_state WHERE record_id = ? AND lease_expires_at = ?`,
			recordID, existing.LeaseExpiresAt)
		if err != nil {
			return nil, 0, false, fmt.Errorf("enrollment: failed to delete stale claimed row: %w", err)
		}
		if n, _ := delRes.RowsAffected(); n != 1 {
			return nil, 0, true, nil // Someone else already recovered it; retry.
		}
		return nil, 0, true, nil // Retry: next loop iteration inserts a fresh claimed row.
	}

	// Authorized or later: steal the lease via compare-and-swap, resuming
	// from the exact persisted phase.
	stealRes, err := s.db.ExecContext(ctx,
		`UPDATE rendezvous_enrollment_state SET owner_id = ?, lease_expires_at = ?, updated_at = ?
		 WHERE record_id = ? AND lease_expires_at = ?`,
		ownerID, leaseExpiresAt, now, recordID, existing.LeaseExpiresAt)
	if err != nil {
		return nil, 0, false, fmt.Errorf("enrollment: failed to steal lease: %w", err)
	}
	if n, _ := stealRes.RowsAffected(); n != 1 {
		return nil, 0, true, nil // Raced with another stealer; retry.
	}
	existing.OwnerID = ownerID
	existing.LeaseExpiresAt = leaseExpiresAt
	return existing, OutcomeOwned, false, nil
}

// isConstraintViolation reports whether err is a DuckDB primary-key/unique
// constraint violation, as opposed to a real failure that should propagate.
func isConstraintViolation(err error) bool {
	var duckErr *duckdb.Error
	if errors.As(err, &duckErr) {
		if duckErr.Type == duckdb.ErrorTypeConstraint {
			return true
		}
		// Under concurrent transactions DuckDB can surface the duplicate-key
		// conflict as a commit-time TransactionContext error instead of a
		// bind-time Constraint error; match on message as a fallback.
		return strings.Contains(duckErr.Msg, "PRIMARY KEY or UNIQUE constraint violated")
	}
	return false
}

// WaitForCompletion polls for recordID to reach Completed, returning the
// row for replay. It returns ErrNotFound-wrapped context errors on timeout.
func (s *Store) WaitForCompletion(ctx context.Context, recordID string, pollInterval, timeout time.Duration) (*Row, error) {
	deadline := time.Now().Add(timeout)
	for {
		row, err := s.Get(ctx, recordID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		if err == nil && row.Phase == PhaseCompleted {
			return row, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("enrollment: timed out waiting for record_id=%s to complete", recordID)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// Get returns the row for recordID, or ErrNotFound.
func (s *Store) Get(ctx context.Context, recordID string) (*Row, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT record_id, phase, owner_id, lease_expires_at, agent_id, colony_id, ticket_jti, ticket_expires_at, request_hash,
		        resolved_endpoint, allocated_ip, old_pubkey, new_pubkey,
		        certificate_pem, ca_chain, cert_expires_at, register_response, last_error
		 FROM rendezvous_enrollment_state WHERE record_id = ?`, recordID)

	var (
		r               Row
		phase           string
		ticketExpiresAt sql.NullTime
		certExpiresAt   sql.NullTime
	)
	err := row.Scan(&r.RecordID, &phase, &r.OwnerID, &r.LeaseExpiresAt, &r.AgentID, &r.ColonyID, &r.TicketJTI, &ticketExpiresAt, &r.RequestHash,
		&r.ResolvedEndpoint, &r.AllocatedIP, &r.OldPubkey, &r.NewPubkey,
		&r.CertificatePEM, &r.CAChain, &certExpiresAt, &r.RegisterResponse, &r.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("enrollment: failed to load row record_id=%s: %w", recordID, err)
	}
	r.Phase = Phase(phase)
	if ticketExpiresAt.Valid {
		r.TicketExpiresAt = ticketExpiresAt.Time
	}
	if certExpiresAt.Valid {
		r.CertExpiresAt = certExpiresAt.Time
	}
	return &r, nil
}

// ListCompleted returns completed enrollment records. Callers must still
// verify each record against the current key mapping before using it, since an
// agent may have enrolled again with a rotated WireGuard key.
func (s *Store) ListCompleted(ctx context.Context) ([]*Row, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT record_id, agent_id, colony_id, allocated_ip, new_pubkey
		 FROM rendezvous_enrollment_state WHERE phase = ?`, string(PhaseCompleted))
	if err != nil {
		return nil, fmt.Errorf("enrollment: failed to list completed records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var completed []*Row
	for rows.Next() {
		row := &Row{Phase: PhaseCompleted}
		if err := rows.Scan(&row.RecordID, &row.AgentID, &row.ColonyID, &row.AllocatedIP, &row.NewPubkey); err != nil {
			return nil, fmt.Errorf("enrollment: failed to scan completed record: %w", err)
		}
		completed = append(completed, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("enrollment: failed while listing completed records: %w", err)
	}
	return completed, nil
}

// SetAuthorized transitions a Claimed row to Authorized after the referral
// ticket, PSK, and identity consistency checks all pass. This is the line
// between "someone claimed this record_id" and "this is a validated
// enrollment attempt" — the only line that matters for whether a future
// lease steal is permitted.
func (s *Store) SetAuthorized(ctx context.Context, recordID, ownerID, agentID, colonyID, ticketJTI string, ticketExpiresAt time.Time, requestHash string, lease time.Duration) error {
	now := time.Now()
	res, err := s.db.ExecContext(ctx,
		`UPDATE rendezvous_enrollment_state
		 SET phase = ?, lease_expires_at = ?, updated_at = ?, agent_id = ?, colony_id = ?, ticket_jti = ?, ticket_expires_at = ?, request_hash = ?
		 WHERE record_id = ? AND owner_id = ?`,
		string(PhaseAuthorized), now.Add(lease), now, agentID, colonyID, ticketJTI, ticketExpiresAt, requestHash, recordID, ownerID)
	return checkAdvanceResult(res, err, recordID, PhaseAuthorized)
}

// SetIPAllocated durably records the pre-image needed to make peer
// mutation restart-safe (resolved endpoint, allocated IP, old/new pubkey)
// before any WireGuard device mutation happens.
func (s *Store) SetIPAllocated(ctx context.Context, recordID, ownerID, resolvedEndpoint, allocatedIP, oldPubkey, newPubkey string, lease time.Duration) error {
	now := time.Now()
	res, err := s.db.ExecContext(ctx,
		`UPDATE rendezvous_enrollment_state
		 SET phase = ?, lease_expires_at = ?, updated_at = ?, resolved_endpoint = ?, allocated_ip = ?, old_pubkey = ?, new_pubkey = ?
		 WHERE record_id = ? AND owner_id = ?`,
		string(PhaseIPAllocated), now.Add(lease), now, resolvedEndpoint, allocatedIP, oldPubkey, newPubkey, recordID, ownerID)
	return checkAdvanceResult(res, err, recordID, PhaseIPAllocated)
}

// SetPhase advances the row to phase with no additional field changes.
// Used for the peer-mutation sub-phases (OldPeerRemoved, NewPeerAdded,
// RegistryUpdated), each individually idempotent against the row's
// pre-image, not live request data.
func (s *Store) SetPhase(ctx context.Context, recordID, ownerID string, phase Phase, lease time.Duration) error {
	now := time.Now()
	res, err := s.db.ExecContext(ctx,
		`UPDATE rendezvous_enrollment_state SET phase = ?, lease_expires_at = ?, updated_at = ? WHERE record_id = ? AND owner_id = ?`,
		string(phase), now.Add(lease), now, recordID, ownerID)
	return checkAdvanceResult(res, err, recordID, phase)
}

func checkAdvanceResult(res sql.Result, err error, recordID string, phase Phase) error {
	if err != nil {
		return fmt.Errorf("enrollment: failed to advance record_id=%s to phase=%s: %w", recordID, phase, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("enrollment: lost ownership of record_id=%s before phase=%s", recordID, phase)
	}
	return nil
}

// SetCompleted marks the row Completed, storing the issued certificate and
// constructed RegisterResponse for replay. This is the last write in a
// successful enrollment.
func (s *Store) SetCompleted(ctx context.Context, recordID, ownerID string, certPEM, caChain []byte, certExpiresAt time.Time, registerResponse []byte) error {
	now := time.Now()
	res, err := s.db.ExecContext(ctx,
		`UPDATE rendezvous_enrollment_state
		 SET phase = ?, certificate_pem = ?, ca_chain = ?, cert_expires_at = ?, register_response = ?, updated_at = ?
		 WHERE record_id = ? AND owner_id = ?`,
		string(PhaseCompleted), certPEM, caChain, certExpiresAt, registerResponse, now, recordID, ownerID)
	if err != nil {
		return fmt.Errorf("enrollment: failed to mark record_id=%s completed: %w", recordID, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("enrollment: lost ownership of record_id=%s before completion", recordID)
	}
	return nil
}

// Delete removes a row still owned by ownerID. Used when Claimed-phase
// validation fails: the row is deleted, not advanced, so the next attempt
// restarts from validation rather than resuming an unvalidated row.
func (s *Store) Delete(ctx context.Context, recordID, ownerID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM rendezvous_enrollment_state WHERE record_id = ? AND owner_id = ?`,
		recordID, ownerID)
	if err != nil {
		return fmt.Errorf("enrollment: failed to delete record_id=%s: %w", recordID, err)
	}
	return nil
}

// SetLastError records a non-fatal diagnostic on the row without changing
// phase or ownership, best-effort.
func (s *Store) SetLastError(ctx context.Context, recordID, ownerID, msg string) {
	_, _ = s.db.ExecContext(ctx,
		`UPDATE rendezvous_enrollment_state SET last_error = ?, updated_at = ? WHERE record_id = ? AND owner_id = ?`,
		msg, time.Now(), recordID, ownerID)
}
