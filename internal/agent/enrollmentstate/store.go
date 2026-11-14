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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/crypto/curve25519"

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

func validateKeys(keys *auth.WireGuardKeyPair) error {
	if keys == nil {
		return fmt.Errorf("missing WireGuard keypair")
	}
	if err := auth.ValidateWireGuardKey(keys.PrivateKey); err != nil {
		return fmt.Errorf("invalid WireGuard private key")
	}
	if err := auth.ValidateWireGuardKey(keys.PublicKey); err != nil {
		return fmt.Errorf("invalid WireGuard public key")
	}
	privateKey, _ := base64.StdEncoding.DecodeString(keys.PrivateKey)
	publicKey, err := curve25519.X25519(privateKey, curve25519.Basepoint)
	if err != nil || base64.StdEncoding.EncodeToString(publicKey) != keys.PublicKey {
		return fmt.Errorf("WireGuard public key does not match private key")
	}
	return nil
}

// ValidateAssignment checks that an assigned mesh IP belongs to its subnet.
func ValidateAssignment(assignedIP, meshSubnet string) error {
	ip := net.ParseIP(assignedIP)
	if ip == nil {
		return fmt.Errorf("invalid assigned mesh IP")
	}
	_, subnet, err := net.ParseCIDR(meshSubnet)
	if err != nil {
		return fmt.Errorf("invalid mesh subnet")
	}
	if !subnet.Contains(ip) {
		return fmt.Errorf("assigned mesh IP is outside mesh subnet")
	}
	return nil
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
	if cp.State != StatePending && cp.State != StateEnrolled {
		return nil, fmt.Errorf("%w: unsupported state", ErrInvalid)
	}
	if err := validateKeys(&auth.WireGuardKeyPair{PrivateKey: cp.WireGuardPrivateKey, PublicKey: cp.WireGuardPublicKey}); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	// Also secure checkpoints created by older versions or restored from backups.
	// #nosec G302: private directory requires traversal.
	if err := os.Chmod(s.dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to secure enrollment directory: %w", err)
	}
	if err := os.Chmod(s.path(), 0600); err != nil {
		return nil, fmt.Errorf("failed to secure enrollment checkpoint: %w", err)
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
// in pending state for the same agent/colony/key it is left untouched. An old
// commit marker is replaced before any new certificate material is written.
func (s *Store) SavePendingIdentity(agentID, colonyID string, keys *auth.WireGuardKeyPair) (*Checkpoint, error) {
	if agentID == "" || colonyID == "" {
		return nil, fmt.Errorf("pending identity requires agent and colony IDs")
	}
	if err := validateKeys(keys); err != nil {
		return nil, err
	}
	if existing, err := s.Load(); err == nil {
		if existing.State == StatePending && existing.AgentID == agentID && existing.ColonyID == colonyID && existing.WireGuardPublicKey == keys.PublicKey {
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
	if cp.State != StatePending {
		return nil, fmt.Errorf("cannot commit enrollment without a pending checkpoint")
	}
	if cp.AgentID != agentID || cp.ColonyID != colonyID {
		return nil, fmt.Errorf("cannot commit enrollment: pending checkpoint identity %s/%s does not match %s/%s", cp.AgentID, cp.ColonyID, agentID, colonyID)
	}
	if cp.WireGuardPublicKey != wgPubKey {
		return nil, fmt.Errorf("cannot commit enrollment: pending checkpoint WireGuard key does not match the key used for registration")
	}
	if err := ValidateAssignment(assignedIP, meshSubnet); err != nil {
		return nil, err
	}
	if certSHA256 == "" || certSerial == "" {
		return nil, fmt.Errorf("cannot commit enrollment without certificate hash and serial")
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

// ArchiveIncomplete archives the checkpoint and supplied certificate files in a
// timestamped recovery directory, instead of deleting it, so an operator can
// still inspect what was there. A valid WireGuard identity remains pending in
// the active directory so a crash during recovery cannot lose it. Absent files
// are skipped. Call this before
// re-running SavePendingIdentity/compound enrollment for local state that
// cannot be trusted (corrupt, incomplete, or identity-mismatched).
func (s *Store) ArchiveIncomplete(reason string, certificateFiles ...string) error {
	var files []string
	for _, name := range append([]string{CheckpointFileName}, certificateFiles...) {
		if filepath.Base(name) != name {
			return fmt.Errorf("invalid enrollment file name")
		}
		if _, err := os.Stat(filepath.Join(s.dir, name)); err == nil {
			files = append(files, name)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("failed to stat enrollment file: %w", err)
		}
	}
	if len(files) == 0 {
		return nil
	}

	s.logger.Info().
		Str("event", "agent_enrollment_recovery_started").
		Str("reason", reason).
		Msg("Archiving incomplete enrollment checkpoint")

	recoveryDir, err := os.MkdirTemp(s.dir, "recovery-"+time.Now().UTC().Format("20060102T150405Z")+"-*")
	if err != nil {
		return fmt.Errorf("failed to create recovery directory: %w", err)
	}

	for _, name := range files {
		path := filepath.Join(recoveryDir, name)
		if name == CheckpointFileName {
			// Preserve the original manifest for inspection, then invalidate its
			// commit marker before moving any certificates. Keep a usable key
			// at the active path throughout recovery.
			cp, loadErr := s.Load()
			if loadErr == nil {
				data, err := json.Marshal(cp)
				if err != nil {
					return fmt.Errorf("failed to encode archived checkpoint: %w", err)
				}
				if err := WriteFileAtomic(path, data, 0600); err != nil {
					return err
				}
				keys := &auth.WireGuardKeyPair{PrivateKey: cp.WireGuardPrivateKey, PublicKey: cp.WireGuardPublicKey}
				if _, err := s.SavePendingIdentity(cp.AgentID, cp.ColonyID, keys); err != nil {
					return err
				}
			} else if !errors.Is(loadErr, ErrInvalid) {
				return loadErr
			} else if err := os.Rename(s.path(), path); err != nil {
				return fmt.Errorf("failed to archive invalid checkpoint: %w", err)
			}
		} else if err := os.Rename(filepath.Join(s.dir, name), path); err != nil {
			return fmt.Errorf("failed to archive enrollment file: %w", err)
		}
		if err := os.Chmod(path, 0600); err != nil {
			return fmt.Errorf("failed to secure archived enrollment file: %w", err)
		}
		if err := privilege.FixFileOwnership(path); err != nil {
			s.logger.Warn().Err(err).Msg("Failed to fix archived file ownership")
		}
	}
	syncDir(recoveryDir)
	syncDir(s.dir)

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

	if err := WriteFileAtomic(s.path(), data, 0600); err != nil {
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
