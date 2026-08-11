// Package startup provides agent server initialization and lifecycle management.
package startup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/agent/bootstrap"
	"github.com/coral-mesh/coral/internal/agent/certs"
	"github.com/coral-mesh/coral/internal/agent/enrollmentstate"
	"github.com/coral-mesh/coral/internal/auth"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/constants"
	"github.com/coral-mesh/coral/internal/discovery"
	"github.com/coral-mesh/coral/internal/logging"
)

// BootstrapPhase handles certificate bootstrap during agent startup.
// Implements RFD 048 - Agent Certificate Bootstrap.
type BootstrapPhase struct {
	logger           logging.Logger
	agentConfig      *config.AgentConfig
	colonyID         string
	agentID          string
	agentKeys        *auth.WireGuardKeyPair
	services         []*meshv1.ServiceInfo
	runtimeContext   *agentv1.RuntimeContextResponse
	protocolVersion  string
	observedEndpoint *discovery.Endpoint
}

// BootstrapResult contains the result of the bootstrap phase.
type BootstrapResult struct {
	// CertManager is the certificate manager with loaded credentials.
	CertManager *certs.Manager

	// Bootstrapped indicates whether a new certificate was obtained.
	Bootstrapped bool

	// Registration is set when RFD 109 completed mesh enrollment over the
	// rendezvous dial-back connection.
	Registration *meshv1.RegisterResponse
}

// NewBootstrapPhase creates a new bootstrap phase handler.
func NewBootstrapPhase(
	logger logging.Logger,
	agentConfig *config.AgentConfig,
	colonyID string,
	agentID string,
	agentKeys *auth.WireGuardKeyPair,
	services []*meshv1.ServiceInfo,
	runtimeContext *agentv1.RuntimeContextResponse,
	protocolVersion string,
	observedEndpoint *discovery.Endpoint,
) *BootstrapPhase {
	return &BootstrapPhase{
		logger:           logger,
		agentConfig:      agentConfig,
		colonyID:         colonyID,
		agentID:          agentID,
		agentKeys:        agentKeys,
		services:         services,
		runtimeContext:   runtimeContext,
		protocolVersion:  protocolVersion,
		observedEndpoint: observedEndpoint,
	}
}

// Execute runs the bootstrap phase.
// Returns a BootstrapResult indicating the state of certificate credentials.
func (bp *BootstrapPhase) Execute(ctx context.Context) (*BootstrapResult, error) {
	// Get bootstrap config.
	bootstrapCfg := bp.agentConfig.Agent.Bootstrap

	// Check if bootstrap is enabled.
	// Default is enabled unless explicitly disabled.
	// Environment variables are loaded via config.MergeFromEnv
	if !bootstrapCfg.Enabled && bootstrapCfg.CAFingerprint == "" {
		return nil, fmt.Errorf("certificate bootstrap required: set ca_fingerprint or CORAL_CA_FINGERPRINT")
	}

	// Create certificate manager.
	certsDir := certs.ResolveDir(bootstrapCfg.CertsDir)
	certManager := certs.NewManager(certs.Config{
		CertsDir: certsDir,
		Logger:   bp.logger,
	})
	store := enrollmentstate.NewStore(certsDir, bp.logger)

	// A certificate alone is not proof that mesh enrollment is locally
	// usable -- only a committed checkpoint whose identity and certificate
	// hash match the currently loaded certificate is (RFD 109 restart-state
	// fix). Any other case below archives what's on disk and falls through
	// to run compound enrollment fresh.
	if certManager.CertificateExists() {
		if err := certManager.Load(); err == nil {
			info := certManager.GetCertificateInfo()
			switch info.Status {
			case certs.CertStatusValid, certs.CertStatusRenewalNeeded:
				bp.logger.Info().
					Str("agent_id", info.AgentID).
					Int("days_remaining", info.DaysRemaining).
					Str("status", string(info.Status)).
					Msg("Found existing certificate")

				if result := bp.tryRestoreEnrollment(store, certManager, info); result != nil {
					return result, nil
				}

			case certs.CertStatusExpiringSoon:
				bp.logger.Warn().
					Str("agent_id", info.AgentID).
					Int("days_remaining", info.DaysRemaining).
					Msg("Certificate expiring soon, attempting renewal")
				// Try to renew, but continue if it fails.

			case certs.CertStatusExpired:
				bp.logger.Warn().Msg("Certificate expired, need to bootstrap")
				// Fall through to bootstrap.
			}
		} else {
			bp.logger.Warn().Err(err).Msg("Failed to load existing certificate")
		}
	}

	// Ensure a pending WireGuard identity checkpoint exists before running
	// compound enrollment, so the result can be committed against it below.
	if _, err := store.SavePendingIdentity(bp.agentID, bp.colonyID, bp.agentKeys); err != nil {
		return nil, fmt.Errorf("failed to persist pending WireGuard identity: %w", err)
	}

	// CA fingerprint is loaded from config (env var override via MergeFromEnv)
	fingerprint := bootstrapCfg.CAFingerprint

	if fingerprint == "" {
		return nil, fmt.Errorf("certificate bootstrap required but no CA fingerprint configured")
	}

	// Load discovery endpoint from global config (defaults + env var overrides via MergeFromEnv).
	// NewLoader always succeeds, even in containerized environments without a home directory,
	// so CORAL_DISCOVERY_ENDPOINT is always picked up via the env struct tag.
	discoveryURL := ""
	loader, err := config.NewLoader()
	if err == nil {
		globalCfg, err := loader.LoadGlobalConfig()
		if err == nil && globalCfg.Discovery.Endpoint != "" {
			discoveryURL = globalCfg.Discovery.Endpoint
		}
	}

	if discoveryURL == "" {
		return nil, fmt.Errorf("certificate bootstrap required but no discovery endpoint configured")
	}

	// Perform bootstrap.
	bp.logger.Info().
		Str("colony_id", bp.colonyID).
		Str("agent_id", bp.agentID).
		Msg("Starting certificate bootstrap")

	// Bootstrap PSK is loaded from config (env var override via MergeFromEnv)
	bootstrapPSK := bootstrapCfg.BootstrapPSK
	bootstrapPublicEndpoint := resolveBootstrapPublicEndpoint(
		bootstrapCfg.BootstrapPublicEndpoint,
		bp.observedEndpoint,
		bootstrapCfg.BootstrapListenPort,
	)
	if bootstrapCfg.BootstrapPublicEndpoint == "" && bootstrapPublicEndpoint != "" {
		bp.logger.Info().
			Str("endpoint", bootstrapPublicEndpoint).
			Msg("Derived public bootstrap endpoint from Discovery-registered STUN address")
	}

	client := bootstrap.NewClient(bootstrap.Config{
		AgentID:                     bp.agentID,
		ColonyID:                    bp.colonyID,
		CAFingerprint:               fingerprint,
		BootstrapPSK:                bootstrapPSK,
		DiscoveryEndpoint:           discoveryURL,
		ColonyEndpoint:              bootstrapCfg.ColonyEndpoint,
		BootstrapPublicEndpoint:     bootstrapPublicEndpoint,
		VerifyBootstrapReachability: bootstrapCfg.VerifyBootstrapReachability,
		BootstrapListenPort:         bootstrapCfg.BootstrapListenPort,
		WireGuardPubkey:             bp.agentKeys.PublicKey,
		Services:                    bp.services,
		RuntimeContext:              bp.runtimeContext,
		ProtocolVersion:             bp.protocolVersion,
		Logger:                      bp.logger,
	})

	// Apply timeout from config.
	timeout := bootstrapCfg.TotalTimeout
	if timeout == 0 {
		timeout = 5 * time.Minute // Default timeout.
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Initialize metrics for telemetry (RFD 048).
	metrics := bootstrap.NewMetrics(bp.logger)
	startTime := time.Now()

	result, err := client.Bootstrap(ctx)
	duration := time.Since(startTime)

	if err != nil {
		bp.logger.Error().Err(err).Msg("Certificate bootstrap failed")

		// Record metrics based on error type.
		metricResult := bootstrap.MetricResultFailure
		if errors.Is(err, context.DeadlineExceeded) {
			metricResult = bootstrap.MetricResultTimeout
		}

		metrics.RecordBootstrapAttempt(metricResult, duration, bp.agentID, bp.colonyID, err.Error())
		return nil, fmt.Errorf("certificate bootstrap failed: %w", err)
	}

	// Record success metric.
	metrics.RecordBootstrapAttempt(bootstrap.MetricResultSuccess, duration, bp.agentID, bp.colonyID, "")

	// Save the certificate.
	if err := certManager.Save(result); err != nil {
		bp.logger.Error().Err(err).Msg("Failed to save bootstrap certificate")
		return nil, fmt.Errorf("failed to save certificate: %w", err)
	}

	// Save agent ID for persistence.
	if err := certManager.SaveAgentID(bp.agentID); err != nil {
		bp.logger.Warn().Err(err).Msg("Failed to persist agent ID")
	}

	// Reload the certificate.
	if err := certManager.Load(); err != nil {
		bp.logger.Error().Err(err).Msg("Failed to load bootstrap certificate")
		return nil, fmt.Errorf("failed to load certificate: %w", err)
	}

	bp.logger.Info().
		Str("spiffe_id", result.AgentSPIFFEID).
		Time("expires_at", result.ExpiresAt).
		Msg("Certificate bootstrap completed successfully")

	// Only a compound BootstrapAndRegister result carries a mesh assignment.
	// Committing the checkpoint here, rather than after ordinary
	// MeshService.Register, keeps invariant 3: certificate, WireGuard key,
	// and mesh assignment belong to the same completed enrollment.
	if result.Registration != nil {
		info := certManager.GetCertificateInfo()
		certSHA256, hashErr := certSHA256Hex(certManager.GetCertPath())
		if hashErr != nil {
			return nil, fmt.Errorf("failed to hash issued certificate: %w", hashErr)
		}
		if _, err := store.CommitEnrollment(
			bp.agentID,
			bp.colonyID,
			bp.agentKeys.PublicKey,
			result.Registration.AssignedIp,
			result.Registration.MeshSubnet,
			certSHA256,
			info.SerialNumber,
		); err != nil {
			return nil, fmt.Errorf("failed to commit enrollment checkpoint: %w", err)
		}
	}

	return &BootstrapResult{
		CertManager:  certManager,
		Bootstrapped: true,
		Registration: result.Registration,
	}, nil
}

// tryRestoreEnrollment returns a BootstrapResult reusing the existing
// certificate and checkpoint if, and only if, both the certificate identity
// and a committed checkpoint match the current startup identity and
// certificate. Otherwise it archives whatever local state cannot be trusted
// and returns nil so the caller falls through to compound enrollment.
func (bp *BootstrapPhase) tryRestoreEnrollment(store *enrollmentstate.Store, certManager *certs.Manager, info *certs.CertificateInfo) *BootstrapResult {
	if !identityMatches(info, bp.agentID, bp.colonyID) {
		bp.logger.Warn().
			Str("event", "agent_enrollment_identity_mismatch").
			Str("cert_agent_id", info.AgentID).
			Str("cert_colony_id", info.ColonyID).
			Str("agent_id", bp.agentID).
			Str("colony_id", bp.colonyID).
			Msg("Existing certificate identity does not match current startup identity; refusing reuse")
		if err := store.ArchiveIncomplete("identity_mismatch"); err != nil {
			bp.logger.Warn().Err(err).Msg("Failed to archive mismatched enrollment state")
		}
		return nil
	}

	certSHA256, err := certSHA256Hex(certManager.GetCertPath())
	if err != nil {
		bp.logger.Warn().Err(err).Msg("Failed to hash existing certificate")
		return nil
	}

	cp, err := store.Load()
	if err != nil {
		bp.logger.Info().
			Str("event", "agent_enrollment_state_incomplete").
			Err(err).
			Msg("Certificate exists without a usable enrollment checkpoint")
		if archErr := store.ArchiveIncomplete("missing_or_invalid_checkpoint"); archErr != nil {
			bp.logger.Warn().Err(archErr).Msg("Failed to archive incomplete enrollment state")
		}
		return nil
	}

	if err := validateCheckpoint(cp, bp.agentID, bp.colonyID, bp.agentKeys.PublicKey, certSHA256, info.SerialNumber); err != nil {
		bp.logger.Info().
			Str("event", "agent_enrollment_state_incomplete").
			Err(err).
			Msg("Enrollment checkpoint does not match the current certificate and identity")
		if archErr := store.ArchiveIncomplete("checkpoint_mismatch"); archErr != nil {
			bp.logger.Warn().Err(archErr).Msg("Failed to archive mismatched enrollment state")
		}
		return nil
	}

	bp.logger.Info().
		Str("event", "mesh_enrollment_restored").
		Str("mesh_ip", cp.AssignedIP).
		Str("mesh_subnet", cp.MeshSubnet).
		Msg("Restored mesh enrollment from checkpoint")

	return &BootstrapResult{
		CertManager:  certManager,
		Bootstrapped: false,
		Registration: &meshv1.RegisterResponse{
			Accepted:   true,
			AssignedIp: cp.AssignedIP,
			MeshSubnet: cp.MeshSubnet,
		},
	}
}

// identityMatches reports whether a loaded certificate's Agent ID and
// Colony ID match the identity selected for the current startup.
func identityMatches(info *certs.CertificateInfo, agentID, colonyID string) bool {
	return info.AgentID == agentID && info.ColonyID == colonyID
}

// validateCheckpoint reports an error if the checkpoint is not a complete,
// committed enrollment that belongs to the given identity, WireGuard key,
// and certificate.
func validateCheckpoint(cp *enrollmentstate.Checkpoint, agentID, colonyID, wgPubKey, certSHA256, certSerial string) error {
	if cp.State != enrollmentstate.StateEnrolled {
		return fmt.Errorf("checkpoint state %q is not enrolled", cp.State)
	}
	if cp.AgentID != agentID || cp.ColonyID != colonyID {
		return fmt.Errorf("checkpoint identity %s/%s does not match %s/%s", cp.AgentID, cp.ColonyID, agentID, colonyID)
	}
	if cp.WireGuardPublicKey != wgPubKey {
		return fmt.Errorf("checkpoint WireGuard key does not match the active identity")
	}
	if cp.CertificateSHA256 != certSHA256 {
		return fmt.Errorf("checkpoint certificate hash does not match the loaded certificate")
	}
	if cp.CertificateSerial != certSerial {
		return fmt.Errorf("checkpoint certificate serial does not match the loaded certificate")
	}
	if net.ParseIP(cp.AssignedIP) == nil {
		return fmt.Errorf("checkpoint assigned IP %q is invalid", cp.AssignedIP)
	}
	if _, _, err := net.ParseCIDR(cp.MeshSubnet); err != nil {
		return fmt.Errorf("checkpoint mesh subnet %q is invalid: %w", cp.MeshSubnet, err)
	}
	return nil
}

// certSHA256Hex returns the hex-encoded SHA-256 hash of the certificate
// file's raw bytes, used to bind an enrollment checkpoint to the exact
// certificate it was issued with.
func certSHA256Hex(certPath string) (string, error) {
	// #nosec G304: path is constructed from trusted configuration.
	data, err := os.ReadFile(certPath)
	if err != nil {
		return "", fmt.Errorf("failed to read certificate: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// resolveBootstrapPublicEndpoint keeps explicit configuration as the
// authoritative override, otherwise deriving the temporary TCP listener from
// the public IP STUN already discovered for Agent registration.
func resolveBootstrapPublicEndpoint(configured string, observed *discovery.Endpoint, listenPort int) string {
	if configured != "" {
		return configured
	}
	if observed == nil || net.ParseIP(observed.IP) == nil {
		return ""
	}
	if listenPort == 0 {
		listenPort = constants.DefaultAgentBootstrapPort
	}
	if listenPort < 1 || listenPort > 65535 {
		return ""
	}
	return net.JoinHostPort(observed.IP, strconv.Itoa(listenPort))
}

// ShouldBootstrap checks if bootstrap is needed based on configuration.
func (bp *BootstrapPhase) ShouldBootstrap() bool {
	bootstrapCfg := bp.agentConfig.Agent.Bootstrap

	// Bootstrap is enabled if:
	// 1. Explicitly enabled in config, or
	// 2. CA fingerprint is configured (implies bootstrap intent).
	if bootstrapCfg.Enabled {
		return true
	}

	// CA fingerprint check handled by config loader
	if bootstrapCfg.CAFingerprint != "" {
		return true
	}

	return false
}
