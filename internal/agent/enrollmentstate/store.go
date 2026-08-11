// Package enrollmentstate persists the Agent's WireGuard identity and mesh
// enrollment assignment as one versioned, atomically-committed checkpoint.
//
// A certificate on disk is not proof that mesh enrollment succeeded: the
// Agent's WireGuard key and Colony-assigned mesh IP/subnet are only ever
// returned in memory unless this package durably records them. Startup must
// treat a checkpoint whose state is not StateEnrolled as an incomplete
// enrollment, never as a completed one.
package enrollmentstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/auth"
	"github.com/coral-mesh/coral/internal/privilege"
)

const (
	// SchemaVersion is the current on-disk checkpoint schema version.
	SchemaVersion = 1

	// CheckpointFileName is the checkpoint file name within the certs directory.
	CheckpointFileName = "enrollment.json"

	// StatePending indicates a WireGuard identity has been generated and
	// persisted, but mesh enrollment has not completed.
	StatePending = "pending"

	// StateEnrolled indicates certificate issuance and mesh enrollment
	// completed and were committed together.
	StateEnrolled = "enrolled"
)

// ErrNotExist indicates no checkpoint file exists yet.
var ErrNotExist = errors.New("enrollment checkpoint does not exist")

// ErrInvalid indicates a checkpoint file exists but is corrupt, truncated,
// or uses an unsupported schema version. Callers must treat this the same
// as an incomplete enrollment, archiving it rather than trusting it.
var ErrInvalid = errors.New("enrollment checkpoint is invalid")

// Checkpoint is the versioned local record of one completed or in-progress
// Agent enrollment. It never stores live Discovery/STUN endpoints -- those
// must always be resolved dynamically.
type Checkpoint struct {
	SchemaVersion       int       `json:"schema_version"`
	State               string    `json:"state"`
	AgentID             string    `json:"agent_id"`
	ColonyID            string    `json:"colony_id"`
	WireGuardPrivateKey string    `json:"wireguard_private_key"`
	WireGuardPublicKey  string    `json:"wireguard_public_key"`
	AssignedIP          string    `json:"assigned_ip,omitempty"`
	MeshSubnet          string    `json:"mesh_subnet,omitempty"`
	CertificateSHA256   string    `json:"certificate_sha256,omitempty"`
	CertificateSerial   string    `json:"certificate_serial,omitempty"`
	EnrolledAt          time.Time `json:"enrolled_at,omitzero"`
}

// Store manages the enrollment checkpoint on disk, beneath the Agent's
// certificate directory.
type Store struct {
	dir    string
	logger zerolog.Logger
}

// NewStore creates a Store rooted at dir (the Agent's certs directory).
func NewStore(dir string, logger zerolog.Logger) *Store {
	return &Store{dir: dir, logger: logger}
}

func (s *Store) path() string {
	return filepath.Join(s.dir, CheckpointFileName)
}

// Load reads and validates the checkpoint. It returns ErrNotExist if no
// checkpoint file exists, or a wrapped ErrInvalid if the file is corrupt,
// truncated, or uses an unsupported schema version.
func (s *Store) Load() (*Checkpoint, error) {
	// #nosec G304: path is constructed from trusted configuration.
	data, err := os.ReadFile(s.path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotExist
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}

	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}

	if cp.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%w: unsupported schema version %d", ErrInvalid, cp.SchemaVersion)
	}
	if cp.AgentID == "" || cp.ColonyID == "" || cp.WireGuardPrivateKey == "" || cp.WireGuardPublicKey == "" {
		return nil, fmt.Errorf("%w: missing required fields", ErrInvalid)
	}

	s.logger.Info().
		Str("event", "agent_enrollment_checkpoint_loaded").
		Str("agent_id", cp.AgentID).
		Str("colony_id", cp.ColonyID).
		Str("state", cp.State).
		Msg("Loaded enrollment checkpoint")

	return &cp, nil
}

// SavePendingIdentity durably records a WireGuard identity before it is
// advertised to Discovery or the Colony, so a retry after a crash reuses the
// same key instead of generating a new one. If a checkpoint already exists
// for the same agent/colony/key it is left untouched.
func (s *Store) SavePendingIdentity(agentID, colonyID string, keys *auth.WireGuardKeyPair) (*Checkpoint, error) {
	if existing, err := s.Load(); err == nil {
		if existing.AgentID == agentID && existing.ColonyID == colonyID && existing.WireGuardPublicKey == keys.PublicKey {
			return existing, nil
		}
	}

	cp := &Checkpoint{
		SchemaVersion:       SchemaVersion,
		State:               StatePending,
		AgentID:             agentID,
		ColonyID:            colonyID,
		WireGuardPrivateKey: keys.PrivateKey,
		WireGuardPublicKey:  keys.PublicKey,
	}

	if err := s.write(cp); err != nil {
		return nil, err
	}

	return cp, nil
}

// CommitEnrollment atomically completes the checkpoint for the pending
// identity matching agentID, colonyID, and wgPubKey, recording the mesh
// assignment and certificate identity from one completed enrollment. It
// fails if no matching pending checkpoint exists, enforcing that the
// certificate, WireGuard key, and mesh assignment all belong together.
func (s *Store) CommitEnrollment(agentID, colonyID, wgPubKey, assignedIP, meshSubnet, certSHA256, certSerial string) (*Checkpoint, error) {
	cp, err := s.Load()
	if err != nil {
		return nil, fmt.Errorf("cannot commit enrollment without a pending checkpoint: %w", err)
	}
	if cp.AgentID != agentID || cp.ColonyID != colonyID {
		return nil, fmt.Errorf("cannot commit enrollment: pending checkpoint identity %s/%s does not match %s/%s", cp.AgentID, cp.ColonyID, agentID, colonyID)
	}
	if cp.WireGuardPublicKey != wgPubKey {
		return nil, fmt.Errorf("cannot commit enrollment: pending checkpoint WireGuard key does not match the key used for registration")
	}

	cp.State = StateEnrolled
	cp.AssignedIP = assignedIP
	cp.MeshSubnet = meshSubnet
	cp.CertificateSHA256 = certSHA256
	cp.CertificateSerial = certSerial
	cp.EnrolledAt = time.Now().UTC()

	if err := s.write(cp); err != nil {
		return nil, err
	}

	s.logger.Info().
		Str("event", "agent_enrollment_checkpoint_committed").
		Str("agent_id", agentID).
		Str("colony_id", colonyID).
		Msg("Committed enrollment checkpoint")

	return cp, nil
}

// ArchiveIncomplete moves the checkpoint file out of the way into a
// timestamped recovery directory, instead of deleting it, so an operator can
// still inspect what was there. Absent files are skipped. Call this before
// re-running SavePendingIdentity/compound enrollment for local state that
// cannot be trusted (corrupt, incomplete, or identity-mismatched).
func (s *Store) ArchiveIncomplete(reason string) error {
	path := s.path()
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to stat checkpoint: %w", err)
	}

	s.logger.Info().
		Str("event", "agent_enrollment_recovery_started").
		Str("reason", reason).
		Msg("Archiving incomplete enrollment checkpoint")

	recoveryDir := filepath.Join(s.dir, fmt.Sprintf("recovery-%s", time.Now().UTC().Format("20060102T150405Z")))
	if err := os.MkdirAll(recoveryDir, 0700); err != nil {
		return fmt.Errorf("failed to create recovery directory: %w", err)
	}

	if err := os.Rename(path, filepath.Join(recoveryDir, CheckpointFileName)); err != nil {
		return fmt.Errorf("failed to archive checkpoint: %w", err)
	}

	if err := privilege.FixFileOwnership(recoveryDir); err != nil {
		s.logger.Warn().Err(err).Msg("Failed to fix recovery directory ownership")
	}

	s.logger.Info().
		Str("event", "agent_enrollment_recovery_completed").
		Str("reason", reason).
		Str("recovery_dir", recoveryDir).
		Msg("Archived incomplete enrollment checkpoint")

	return nil
}

func (s *Store) write(cp *Checkpoint) error {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return fmt.Errorf("failed to create enrollment state directory: %w", err)
	}
	// MkdirAll does not change the mode of a directory that already exists,
	// and this directory holds the private key -- enforce 0700 either way.
	// #nosec G302: 0700 is a directory mode (needs the execute bit to be traversable), not a file mode.
	if err := os.Chmod(s.dir, 0700); err != nil {
		return fmt.Errorf("failed to set enrollment state directory permissions: %w", err)
	}

	data, err := json.Marshal(cp)
	if err != nil {
		return fmt.Errorf("failed to marshal enrollment checkpoint: %w", err)
	}

	if err := writeFileAtomic(s.path(), data, 0600); err != nil {
		return fmt.Errorf("failed to write enrollment checkpoint: %w", err)
	}

	if err := privilege.FixFileOwnership(s.dir); err != nil {
		s.logger.Warn().Err(err).Msg("Failed to fix enrollment state directory ownership")
	}
	if err := privilege.FixFileOwnership(s.path()); err != nil {
		s.logger.Warn().Err(err).Msg("Failed to fix enrollment checkpoint ownership")
	}

	return nil
}
